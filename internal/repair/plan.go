package repair

import (
	"fmt"
	"maps"
	"slices"
	"sort"

	"github.com/kodestar/audiosilo-meta/internal/audit"
	"github.com/kodestar/audiosilo-meta/internal/rawentry"
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

	// redirects is the tombstone table as the run will write it, and tombs counts what
	// this run added to it - accumulated where the writes happen, so the report never
	// re-derives it by enumerating which ops retire a slug (a third such enumeration,
	// beside AppliableOps and planOne's switch, would be a third thing to keep in step).
	redirects model.Redirects
	tombs     int

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
//
// communityRO is the READ-ONLY community root (metarepair --community), nil unless
// one was given. It exists because the sidecar-collision refusal is a question
// about data this repository no longer holds - see sidecarSource.
func newPlan(store *pack.Store, communityRO *pack.Store, cat *model.Catalog, table model.Redirects) *plan {
	p := &plan{
		works:         newView(store, pack.FamilyWorks),
		community:     newCommunityView(store, communityRO),
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

// sidecarSource says how a run can answer the question the sidecar-collision
// refusal is made of: "does this work carry a characters or recaps member".
//
// Before the community-repo split there was one answer - read the works-community
// family out of the same tree - and the refusal (both halves of a duplicate
// carrying the same member kind, which is a human decision because the CC BY-SA
// layer is the most expensive data in the project) simply worked. After the split
// this repository does not hold that family, and reading it EMPTY made the
// refusal STRUCTURALLY BLIND: every merge answered "no sidecars", a wave folded
// two sidecar-carrying works together, and the collision surfaced later as a
// release-blocking LoadComposed error in a build nobody could attribute to the
// wave that caused it.
//
// Safety here is ABSOLUTE AND NEVER INFERRED. Three modes, and the middle one is
// what --community buys:
type sidecarSource int

const (
	// sidecarInTree: the root holds works-community (a whole-database tree, the
	// pre-split shape). Read and written exactly as before.
	sidecarInTree sidecarSource = iota
	// sidecarReadOnly: metarepair --community names the community checkout's
	// data/. The collision question is answered from THERE, so the refusal works
	// exactly as it did pre-split - but nothing is written to it: it is another
	// repository, and moving a member is that repository's own change. The
	// sidecars keep their retired keys and ride the slug tombstone to the
	// survivor (check.LoadComposed's compose-time re-key) until the community
	// re-key sweep lands them durably.
	sidecarReadOnly
	// sidecarUnknown: the root does not hold the family and no --community was
	// given. NOTHING can be inferred - either side of any cluster might carry a
	// member - so every merge-works proposal is refused with CatCommunityRequired,
	// naming the flag. A refused merge is recoverable; a silently folded sidecar
	// pair is not.
	sidecarUnknown
)

// view is one family's read-through, write-behind state inside the plan.
type view struct {
	// read is where get reads through to; nil means nothing is readable and the
	// view answers empty. write is where queue puts its writes; nil means this
	// view is planned but never written (sidecarReadOnly). They are the same
	// store for every ordinary family.
	read   *pack.Store
	write  *pack.Store
	family pack.Family
	held   map[string]entry
	dirty  map[string]bool
	gone   map[string]bool
	// sidecar is meaningful on the works-community view alone and is what
	// mergeSidecars branches on.
	sidecar sidecarSource
}

func newView(store *pack.Store, f pack.Family) *view {
	return &view{
		read:    store,
		write:   store,
		family:  f,
		held:    map[string]entry{},
		dirty:   map[string]bool{},
		gone:    map[string]bool{},
		sidecar: sidecarInTree,
	}
}

// newCommunityView builds the works-community view over whichever root can answer
// for it: the tree itself when its profile holds the family, else the read-only
// community root, else neither.
//
// The STAGING is identical in all three modes, deliberately. A read-only run still
// puts the merged entry and removes the losers, so a LATER proposal in the same run
// reads what the earlier ones decided - which is the only thing that catches two
// clusters folding two `characters`-carrying works onto one target. queue() is
// where the modes part: it writes nothing for a view with no write store.
func newCommunityView(store, communityRO *pack.Store) *view {
	v := &view{
		family: pack.FamilyWorksCommunity,
		held:   map[string]entry{},
		dirty:  map[string]bool{},
		gone:   map[string]bool{},
	}
	switch {
	case store.Profile().Has(pack.FamilyWorksCommunity):
		v.read, v.write, v.sidecar = store, store, sidecarInTree
	case communityRO != nil:
		v.read, v.write, v.sidecar = communityRO, nil, sidecarReadOnly
	default:
		v.read, v.write, v.sidecar = nil, nil, sidecarUnknown
	}
	return v
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
	if v.read == nil {
		return nil, false, nil
	}
	raw, ok, err := v.read.Get(v.family, slug)
	if err != nil {
		return nil, false, fmt.Errorf("read %s entry %q: %w", v.family.Root(), slug, err)
	}
	if !ok {
		return nil, false, nil
	}
	e, err := rawentry.Decode(raw)
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
//
// A view with NO WRITE STORE queues nothing. That is only the works-community view
// under --community (sidecarReadOnly): its staged state exists to answer questions
// within the run, and the entries themselves belong to another repository, which
// this pass may not write. Returning early rather than at each Upsert keeps that
// one decision in one place.
func (v *view) queue() error {
	if v.write == nil {
		return nil
	}
	for _, slug := range rawentry.SortedKeys(v.dirty) {
		raw, err := v.held[slug].Raw()
		if err != nil {
			return fmt.Errorf("render %s entry %q: %w", v.family.Root(), slug, err)
		}
		if err := v.write.Upsert(v.family, slug, raw); err != nil {
			return err
		}
	}
	for _, slug := range rawentry.SortedKeys(v.gone) {
		if err := v.write.Delete(v.family, slug); err != nil {
			return err
		}
	}
	return nil
}

// loadCluster is the preamble BOTH merge ops share: read the proposal's target and its
// losers out of one family, in slug order, refusing what a merge may not act on before it
// composes anything.
//
// It is one function rather than two near-identical openings because the five refusals
// below are the same five judgements about the same shape - a target and the records
// folding onto it - and two spellings of them would eventually disagree about which is
// checked first, which is what a REFUSED.ndjson consumer reads. noun ("work", "series") is
// the only thing that differs, and it appears only in the messages.
func (t *txn) loadCluster(f pack.Family, noun string, p audit.Proposal) (target entry, losers []string, entries []entry, err error) {
	if p.Target == "" || len(p.Others) == 0 {
		return nil, nil, nil, refusef(CatMalformed, "the proposal names no target or no other members")
	}
	st := t.stageFor(f)
	for _, slug := range cluster(p.Target, p.Others) {
		if by, ok := t.p.retiredBy(f, slug); ok {
			return nil, nil, nil, refusef(CatRetired, "%s %q was retired by an earlier proposal in this run (%s)", noun, slug, by)
		}
	}
	target, ok, rerr := st.get(p.Target)
	if rerr != nil {
		return nil, nil, nil, rerr
	}
	if !ok {
		return nil, nil, nil, refusef(CatMissing, "the target %s %q is not in the tree", noun, p.Target)
	}
	losers = slices.Sorted(slices.Values(p.Others))
	entries = make([]entry, 0, len(losers))
	for _, slug := range losers {
		if slug == p.Target {
			return nil, nil, nil, refusef(CatMalformed, "the proposal names %q as both the target and a loser", slug)
		}
		e, ok, rerr := st.get(slug)
		if rerr != nil {
			return nil, nil, nil, rerr
		}
		if !ok {
			return nil, nil, nil, refusef(CatMissing, "the %s %q the proposal folds onto %q is not in the tree",
				noun, slug, p.Target)
		}
		entries = append(entries, e)
	}
	return target, losers, entries, nil
}

// stageFor is the txn's stage for a family. The three are separate fields (each view is
// typed by its family), and this is the one place that mapping is spelled.
func (t *txn) stageFor(f pack.Family) *stage {
	switch f {
	case pack.FamilyWorks:
		return t.works
	case pack.FamilyWorksCommunity:
		return t.community
	case pack.FamilySeries:
		return t.series
	}
	panic("repair: no stage for family " + f.Root())
}

// cluster is a proposal's whole membership: its target first, then the others. Four sites
// spelled the same append.
func cluster(target string, others []string) []string {
	return append([]string{target}, others...)
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
	// reindex holds the membership lists this proposal wrote, so the plan's
	// series index is updated at commit rather than as the plan is composed.
	reindex map[string][]model.SeriesWork
}

func (p *plan) begin() *txn {
	return &txn{
		p:         p,
		works:     newStage(p.works),
		community: newStage(p.community),
		series:    newStage(p.series),
		reindex:   map[string][]model.SeriesWork{},
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
	// The copy is what makes a refused tombstone free of consequence, so it is taken
	// only when there IS one: a wave of 20k retitles against a table of 50k entries
	// would otherwise copy the whole table 20k times over for nothing.
	if len(t.tombs) > 0 {
		table := cloneRedirects(t.p.redirects)
		for _, tb := range t.tombs {
			if err := redirects.Add(table, tb.kind, tb.from, tb.to); err != nil {
				return err
			}
		}
		t.p.redirects = table
		t.p.tombs += len(t.tombs)
	}
	t.works.commit()
	t.community.commit()
	t.series.commit()
	for _, slug := range rawentry.SortedKeys(t.reindex) {
		t.p.noteSeriesMembers(slug, t.reindex[slug])
	}
	for _, slug := range t.retires {
		t.p.retired[slug] = key
	}
	return nil
}

// stage is one family's staged writes inside a txn.
type stage struct {
	v    *view
	puts map[string]entry
	dels map[string]bool
}

func newStage(v *view) *stage {
	return &stage{v: v, puts: map[string]entry{}, dels: map[string]bool{}}
}

// sidecar is the view's sidecarSource, so a planner branching on it (mergeSidecars)
// asks the stage it already holds rather than reaching past it into the plan.
func (s *stage) sidecar() sidecarSource { return s.v.sidecar }

// get reads the staged entry, else the plan's. As with view.get the entry belongs
// to the plan; clone it before editing.
func (s *stage) get(slug string) (entry, bool, error) {
	if s.dels[slug] {
		return nil, false, nil
	}
	if e, ok := s.puts[slug]; ok {
		return e, true, nil
	}
	return s.v.get(slug)
}

func (s *stage) put(slug string, e entry) {
	s.puts[slug] = e
	delete(s.dels, slug)
}

func (s *stage) remove(slug string) {
	delete(s.puts, slug)
	s.dels[slug] = true
}

func (s *stage) commit() {
	for _, slug := range rawentry.SortedKeys(s.puts) {
		s.v.put(slug, s.puts[slug])
	}
	for _, slug := range rawentry.SortedKeys(s.dels) {
		s.v.del(slug)
	}
}

// setSeries stages a series entry's membership list, and stages the RE-INDEX with it
// so the next proposal in the run sees the memberships this one wrote.
//
// The index update waits for commit like everything else: a txn that stages a series
// rewrite and then refuses (a recording key it cannot free, an entry that will not
// render) must leave the plan's idea of who is in which series exactly as it found it,
// or the proposal after it would read a membership that was never written.
func (t *txn) setSeries(slug string, e entry, works []model.SeriesWork) {
	e.Set("works", works)
	t.series.put(slug, e)
	t.reindex[slug] = works
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
