package repair

import (
	"fmt"
	"maps"
	"sort"

	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
	"github.com/kodestar/audiosilo-meta/pkg/redirects"
)

// plan.go is the two-phase discipline internal/remediate and internal/migrate set:
// the WHOLE run is worked out in memory before a byte is written, and a proposal
// that cannot be carried out leaves the tree exactly as it found it.
//
// It is two phases with two different grains, which is the one place this pass
// differs from its precedents. Those are one-off repairs of one cohort, so a
// refusal anywhere refuses that book and nothing else. This pass consumes an audit
// worklist of independent proposals, so the grain of "all or nothing" is the
// PROPOSAL: each is planned inside a txn against the plan so far, and a txn either
// commits whole or is discarded whole and named in the report. Nothing reaches disk
// until every proposal has been planned.

// plan is the tree as the run intends to leave it: the entries it rewrites, the
// entries it deletes, the tombstones it records, and the derived indexes a later
// proposal has to see the earlier ones' effects through.
type plan struct {
	works     *view
	community *view
	series    *view

	// redirects is the tombstone table as the run will write it.
	redirects model.Redirects
	// redirectsChanged reports whether anything was added to it.
	redirectsChanged bool

	// retired names every record an earlier proposal deleted - keyed by FAMILY and
	// slug, since the three id namespaces are independent - mapped to the proposal
	// that retired it. A later proposal naming one is refused: the record it was
	// written against is gone.
	retired map[string]string

	// seriesMembers maps a series id to the work slugs it lists, and seriesOf is
	// the inverse. Both are maintained as series entries are staged, so a proposal
	// asking "which series name this work" sees the memberships earlier proposals
	// rewrote rather than the ones the tree held at load.
	seriesMembers map[string]map[string]bool
	seriesOf      map[string]map[string]bool
}

// newPlan builds the plan over a store and the catalogue that was loaded from it.
func newPlan(store *pack.Store, cat *model.Catalog, table model.Redirects) *plan {
	p := &plan{
		works:         newView(store, pack.FamilyWorks),
		community:     newView(store, pack.FamilyWorksCommunity),
		series:        newView(store, pack.FamilySeries),
		redirects:     cloneRedirects(table),
		retired:       map[string]string{},
		seriesMembers: make(map[string]map[string]bool, len(cat.Series)),
		seriesOf:      map[string]map[string]bool{},
	}
	for _, s := range cat.Series {
		set := make(map[string]bool, len(s.Works))
		for _, sw := range s.Works {
			set[sw.Work] = true
			if p.seriesOf[sw.Work] == nil {
				p.seriesOf[sw.Work] = map[string]bool{}
			}
			p.seriesOf[sw.Work][s.ID] = true
		}
		p.seriesMembers[s.ID] = set
	}
	return p
}

// seriesNaming returns the series ids that list any of these works, sorted. It
// reads the plan's own index, so a membership an earlier proposal added or moved is
// included.
func (p *plan) seriesNaming(works ...string) []string {
	set := map[string]bool{}
	for _, w := range works {
		for sid := range p.seriesOf[w] {
			set[sid] = true
		}
	}
	out := make([]string, 0, len(set))
	for sid := range set {
		out = append(out, sid)
	}
	sort.Strings(out)
	return out
}

// noteSeriesMembers re-indexes one series' membership list.
func (p *plan) noteSeriesMembers(seriesID string, works []model.SeriesWork) {
	next := make(map[string]bool, len(works))
	for _, sw := range works {
		next[sw.Work] = true
	}
	for w := range p.seriesMembers[seriesID] {
		if !next[w] {
			delete(p.seriesOf[w], seriesID)
		}
	}
	for w := range next {
		if p.seriesOf[w] == nil {
			p.seriesOf[w] = map[string]bool{}
		}
		p.seriesOf[w][seriesID] = true
	}
	p.seriesMembers[seriesID] = next
}

// view is one family's read-through, write-behind state inside the plan.
type view struct {
	store  *pack.Store
	family pack.Family
	held   map[string]entry
	dirty  map[string]bool
	gone   map[string]bool
}

func newView(store *pack.Store, f pack.Family) *view {
	return &view{store: store, family: f, held: map[string]entry{}, dirty: map[string]bool{}, gone: map[string]bool{}}
}

// get returns the entry the plan holds for slug, reading through to the store the
// first time. The returned entry is the PLAN's - a caller that means to change it
// clones it first and puts the clone back.
func (v *view) get(slug string) (entry, bool, error) {
	if v.gone[slug] {
		return nil, false, nil
	}
	if e, ok := v.held[slug]; ok {
		return e, true, nil
	}
	raw, ok, err := v.store.Get(v.family, slug)
	if err != nil {
		return nil, false, fmt.Errorf("read %s entry %q: %w", v.family.Root(), slug, err)
	}
	if !ok {
		return nil, false, nil
	}
	e, err := decodeEntry(raw)
	if err != nil {
		return nil, false, fmt.Errorf("parse %s entry %q: %w", v.family.Root(), slug, err)
	}
	v.held[slug] = e
	return e, true, nil
}

func (v *view) put(slug string, e entry) {
	v.held[slug] = e
	v.dirty[slug] = true
	delete(v.gone, slug)
}

func (v *view) del(slug string) {
	delete(v.held, slug)
	delete(v.dirty, slug)
	v.gone[slug] = true
}

// queue puts the view's writes and deletes on the store, in slug order. Nothing
// reaches disk until the store is flushed.
func (v *view) queue() error {
	for _, slug := range sortedKeys(v.dirty) {
		raw, err := v.held[slug].raw()
		if err != nil {
			return fmt.Errorf("render %s entry %q: %w", v.family.Root(), slug, err)
		}
		if err := v.store.Upsert(v.family, slug, raw); err != nil {
			return err
		}
	}
	for _, slug := range sortedKeys(v.gone) {
		if err := v.store.Delete(v.family, slug); err != nil {
			return err
		}
	}
	return nil
}

// tomb is a redirect a txn intends to record.
type tomb struct {
	kind model.RedirectKind
	from string
	to   string
}

// txn is ONE proposal's staged change. Reads fall through to the plan, writes stay
// here until commit, and a refusal simply drops it - which is what makes the
// per-proposal grain real rather than a claim.
type txn struct {
	p         *plan
	works     *stage
	community *stage
	series    *stage
	tombs     []tomb
	retires   []string
	notes     []string
}

func (p *plan) begin() *txn {
	return &txn{
		p:         p,
		works:     newStage(p.works),
		community: newStage(p.community),
		series:    newStage(p.series),
	}
}

// note records what the proposal did, for the apply-report. Notes are the audit
// trail a reviewer reads: which recordings moved, which were re-keyed, which
// sidecar members travelled.
func (t *txn) note(format string, a ...any) { t.notes = append(t.notes, fmt.Sprintf(format, a...)) }

// retire records that a family's entry is being deleted, so a later proposal naming
// it is refused rather than written against a record that is gone.
func (t *txn) retire(f pack.Family, slug string) {
	t.retires = append(t.retires, retiredKey(f, slug))
}

// retiredKey addresses the ledger. The family is part of the key because the id
// namespaces are independent: retiring a series must not make a work of the same slug
// unreachable to the next proposal.
func retiredKey(f pack.Family, slug string) string { return f.Root() + "/" + slug }

// retiredBy reports which proposal retired a family's entry, if any.
func (p *plan) retiredBy(f pack.Family, slug string) (string, bool) {
	by, ok := p.retired[retiredKey(f, slug)]
	return by, ok
}

// redirect records a tombstone. It is validated at commit, through
// redirects.Add over a copy of the table, so a refusal costs nothing.
func (t *txn) redirect(kind model.RedirectKind, from, to string) {
	t.tombs = append(t.tombs, tomb{kind: kind, from: from, to: to})
}

// commit folds the staged change into the plan. The tombstones are validated
// FIRST, over a copy, so a table redirects.Add refuses cannot leave the entries
// committed beside a tombstone that was never recorded.
func (t *txn) commit(key string) error {
	table := cloneRedirects(t.p.redirects)
	for _, tb := range t.tombs {
		if err := redirects.Add(table, tb.kind, tb.from, tb.to); err != nil {
			return err
		}
	}
	t.p.redirects = table
	if len(t.tombs) > 0 {
		t.p.redirectsChanged = true
	}
	t.works.commit()
	t.community.commit()
	t.series.commit()
	for _, slug := range t.retires {
		t.p.retired[slug] = key
	}
	return nil
}

// stage is one family's staged writes inside a txn.
type stage struct {
	v   *view
	put map[string]entry
	del map[string]bool
}

func newStage(v *view) *stage {
	return &stage{v: v, put: map[string]entry{}, del: map[string]bool{}}
}

// get reads the staged entry, else the plan's. As with view.get the entry belongs
// to the plan; clone it before editing.
func (s *stage) get(slug string) (entry, bool, error) {
	if s.del[slug] {
		return nil, false, nil
	}
	if e, ok := s.put[slug]; ok {
		return e, true, nil
	}
	return s.v.get(slug)
}

func (s *stage) set(slug string, e entry) {
	s.put[slug] = e
	delete(s.del, slug)
}

func (s *stage) remove(slug string) {
	delete(s.put, slug)
	s.del[slug] = true
}

func (s *stage) commit() {
	for _, slug := range sortedKeys(s.put) {
		s.v.put(slug, s.put[slug])
	}
	for _, slug := range sortedKeys(s.del) {
		s.v.del(slug)
	}
}

// setSeries stages a series entry's membership list and re-indexes it, so the next
// proposal in the same run sees the memberships this one wrote.
func (t *txn) setSeries(slug string, e entry, works []model.SeriesWork) {
	e.set("works", works)
	t.series.set(slug, e)
	t.p.noteSeriesMembers(slug, works)
}

// cloneRedirects deep-copies a tombstone table. maps.Clone alone is not enough -
// the namespaces are maps of their own, and a shallow copy would let a validation
// that must be discardable write into the plan's table.
func cloneRedirects(r model.Redirects) model.Redirects {
	out := model.NewRedirects()
	for kind, table := range r {
		out[kind] = maps.Clone(table)
	}
	return out
}
