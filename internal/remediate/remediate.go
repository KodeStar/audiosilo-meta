// Package remediate is metaremediate's business logic: the one-off repair that
// folds GraphicAudio's multi-part dramatized-adaptation products back into one
// work per book and gives the series slots those parts hijacked back to the
// plain text editions.
//
// It exists because a user-library import seeded the catalogue with Audible's
// part PRODUCTS as separate works - "The Blood Mirror (2 of 2) [Dramatized
// Adaptation]", "Oathbringer (3 of 6)" - each with its own slug, its own
// recording and, worse, its own numeric slot in the book's series. The parts are
// not books; they are how one dramatization is sold.
//
// Its posture is metamigrate's, the project's other one-off: it plans
// everything before it writes anything, it is deterministic, and it refuses
// loudly rather than guessing. A book it cannot resolve is left exactly as it
// was and named in the report, because a half-merged book is worse than an
// unmerged one. Every write goes through pkg/pack, so the tree it leaves is one
// metacheck and metafmt agree with. Every normalization it needs - a region
// spelling, a date, an ASIN, a chapter list, a series position, a bounded slug -
// comes from internal/importer rather than from a second copy of the rule.
package remediate

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// errNoSoleRecording is the internal failure of a fold whose input turned out
// not to carry exactly one recording. The callers check first, so it only ever
// surfaces as a bug report rather than as data loss.
var errNoSoleRecording = errors.New("work does not carry exactly one recording")

// Options configure a run.
type Options struct {
	// Dir is the data root.
	Dir string
	// CompleteSets is an optional NDJSON file of libex dump rows for the
	// whole-book products (see completeset.go). Without it the merge composes a
	// derived title and carries the parts' identifiers only.
	CompleteSets string
	// Write applies the plan. The zero value reports it and touches nothing.
	Write bool
	// Today is the date new provenance is stamped with, defaulting to the
	// current UTC day. Tests set it so their output is fixed.
	Today string
}

// MissingPlain is a book whose dramatization this run merged but whose plain
// text edition the catalogue does not hold, so a plain series slot is still
// occupied by the dramatization. It is the worklist for the targeted import
// that follows, which is why it carries the identifiers: they are what an
// operator matches a dump row by.
type MissingPlain struct {
	Work    string
	Title   string
	Base    string
	Authors []string
	ASINs   []string
	Series  []string
}

// Report is everything a run did or declined to do.
type Report struct {
	PartWorks      int
	Groups         int
	Merged         []merged
	Deleted        []string
	Twins          []twinMerge
	Series         []seriesPlan
	MissingPlain   []MissingPlain
	Refusals       []Refusal
	Wrote          []string
	RemovedPacks   []string
	Applied        bool
	CompleteSets   int
	MatchedSets    int
	SetProblems    []rowProblem
	SwappedToPlain int
}

// Run plans the remediation over opts.Dir and, with opts.Write, applies it.
func Run(opts Options) (*Report, error) {
	today := opts.Today
	if today == "" {
		today = time.Now().UTC().Format("2006-01-02")
	}
	store, err := pack.OpenFor(opts.Dir, pack.FamilyWorks, pack.FamilySeries)
	if err != nil {
		return nil, err
	}
	idx, err := scan(opts.Dir, store)
	if err != nil {
		return nil, err
	}
	rows, rowProblems, err := loadCompleteSets(opts.CompleteSets)
	if err != nil {
		return nil, err
	}

	groups, refusals := buildGroups(idx)
	refusals = append(refusals, matchTargets(idx, groups)...)
	refusals = append(refusals, matchCompleteSets(groups, rows)...)

	rep := &Report{Groups: len(groups), CompleteSets: len(rows), SetProblems: rowProblems}
	for _, g := range groups {
		rep.PartWorks += len(g.Parts)
		if g.Set != nil {
			rep.MatchedSets++
		}
	}

	var res *planned
	// A series this run cannot repair refuses the books it references, which
	// can free a collision one round later; the loop is bounded by the group
	// count because every round refuses at least one more group.
	for range len(groups) + 1 {
		out, blocked, blockedBy := buildPlan(idx, groups, today)
		if len(blocked) == 0 {
			res = out
			break
		}
		refusals = append(refusals, blockedBy...)
		for _, g := range groups {
			if blocked[g] {
				g.refused = true
				refusals = append(refusals, Refusal{
					Category: catSeriesCollision, Subject: g.Base,
					Reason:  "a series this book sits in could not be repaired, so its parts are left as they are",
					Entries: g.partSlugs(),
				})
			}
		}
	}
	if res == nil {
		return nil, fmt.Errorf("remediate: the plan did not settle; refuse the affected books by hand")
	}

	rep.Refusals = sortRefusals(dedupeRefusals(append(refusals, res.refusals...)))
	rep.Merged = res.merged
	rep.Deleted = res.deletes
	rep.Twins = res.twins
	rep.Series = res.series
	rep.MissingPlain = res.missing
	rep.SwappedToPlain = res.swapped

	if !opts.Write {
		return rep, nil
	}
	if err := apply(store, res); err != nil {
		return rep, err
	}
	written, err := store.Flush()
	if err != nil {
		return rep, err
	}
	rep.Applied = true
	rep.Wrote = written.Wrote
	rep.RemovedPacks = written.Deleted
	return rep, nil
}

// planned is one settled plan: what to write, what to delete, and what the run
// declined to do.
type planned struct {
	works    map[string]obj
	deletes  []string
	series   []seriesPlan
	twins    []twinMerge
	refusals []Refusal
	missing  []MissingPlain
	merged   []merged
	swapped  int
}

// buildPlan works the whole change out without writing anything. blocked names
// the groups a series refusal makes unmergeable; a non-empty blocked set means
// the plan has to be rebuilt without them, and the refusals that explain why
// travel out with it (the next round sees an untouched series and would report
// nothing at all).
//
// The order is deliberate: compose, then collapse twins, THEN build the rewrite
// and swap maps once, with every survivor already resolved. Building them first
// meant three retroactive patch-up passes over maps that had just been written.
func buildPlan(idx *index, groups []*group, today string) (*planned, map[*group]bool, []Refusal) {
	p := &planned{works: map[string]obj{}}

	var active []*group
	composed := map[string]merged{}
	slugOf := map[*group]string{}  // the group -> the slug it composed onto
	groupOf := map[string]*group{} // merged slug -> the group that composed it
	partOfGroup := map[string]*group{}
	for _, g := range groups {
		if g.refused {
			continue
		}
		m, r, ok := compose(idx, g, today)
		if !ok {
			g.refused = true
			p.refusals = append(p.refusals, r)
			continue
		}
		if prev, ok := composed[m.Slug]; ok {
			// Two books composing onto one slug would make the second write
			// silently replace the first. The grouping key makes this
			// unreachable today; it is the backstop that keeps it a refusal
			// rather than a lost record if that ever stops being true.
			g.refused = true
			p.refusals = append(p.refusals, Refusal{Category: catSlugCollision, Subject: g.Base,
				Reason:  "another book in this run already composes onto slug " + m.Slug,
				Entries: append([]string{prev.Slug}, g.partSlugs()...)})
			continue
		}
		active = append(active, g)
		composed[m.Slug] = m
		slugOf[g] = m.Slug
		groupOf[m.Slug] = g
		for _, s := range m.Parts {
			partOfGroup[s] = g
		}
	}

	// The twin pass reads the post-merge catalogue: the works this run composed
	// plus the dramatized cohort works it left alone (never a part, which is
	// about to be deleted).
	view := map[string]obj{}
	for slug, m := range composed {
		view[slug] = m.Work
	}
	for _, slug := range sortedKeys(idx.candidates) {
		c := idx.candidates[slug]
		if _, ok := view[slug]; ok || partOfGroup[slug] != nil {
			continue
		}
		if c.graphicAudio && isDramatized(c.title()) {
			view[slug] = c.obj
		}
	}
	twinChanged, twinMerges, twinRefusals := planTwins(view)
	p.refusals = append(p.refusals, twinRefusals...)
	p.twins = twinMerges

	survivorOf := map[string]string{}
	for _, tm := range twinMerges {
		for _, a := range tm.Absorbed {
			survivorOf[a] = tm.Survivor
		}
	}
	// finalWork resolves a slug to what will exist after the twin collapse.
	finalWork := func(slug string) string {
		if s, ok := survivorOf[slug]; ok {
			return s
		}
		return slug
	}

	rewrites := map[string]rewrite{}
	deletes := map[string]bool{}
	swaps := map[string]string{}
	for _, g := range active {
		final := finalWork(slugOf[g])
		for _, s := range g.partSlugs() {
			rewrites[s] = rewrite{To: final, FromPart: true, Group: g}
			deletes[s] = true
		}
		// The plain text edition this book's series slot belongs to.
		switch twins := idx.plainTwins(g.Base, g.Authors); len(twins) {
		case 0:
			p.missing = append(p.missing, missingPlainFor(idx, g, slugOf[g], composed[slugOf[g]].Title))
		case 1:
			swaps[final] = twins[0]
		default:
			p.refusals = append(p.refusals, Refusal{Category: catAmbiguousTwin, Subject: g.Base,
				Reason:  "more than one plain work matches this book, so no series slot was swapped",
				Entries: twins})
		}
	}
	for loser, survivor := range survivorOf {
		if _, ok := rewrites[loser]; !ok {
			rewrites[loser] = rewrite{To: survivor, Group: groupOf[loser]}
		}
		if idx.works[loser] {
			deletes[loser] = true
		}
	}

	for slug, m := range composed {
		if _, gone := survivorOf[slug]; gone {
			continue
		}
		p.works[slug] = m.Work
		p.merged = append(p.merged, m)
	}
	for slug, w := range twinChanged {
		p.works[slug] = w
	}
	slices.SortFunc(p.merged, func(a, b merged) int { return strings.Compare(a.Slug, b.Slug) })

	series, seriesRefusals, blocked := planSeries(idx, rewrites, swaps)
	if len(blocked) > 0 {
		return nil, blocked, seriesRefusals
	}
	p.refusals = append(p.refusals, seriesRefusals...)
	p.series = series
	p.swapped = countSwaps(idx, series, swaps)
	p.deletes = sortedKeys(deletes)
	return p, nil, nil
}

// countSwaps counts the series entries that now name a plain edition.
func countSwaps(idx *index, plans []seriesPlan, swaps map[string]string) int {
	targets := map[string]bool{}
	for _, twin := range swaps {
		targets[twin] = true
	}
	n := 0
	for _, sp := range plans {
		if dramatizedSeries(idx.series[sp.Slug].str("name")) {
			continue
		}
		for _, sw := range sp.Works {
			if targets[sw.Work] {
				n++
			}
		}
	}
	return n
}

// missingPlainFor describes a book whose plain edition the catalogue lacks.
func missingPlainFor(idx *index, g *group, slug, title string) MissingPlain {
	m := MissingPlain{Work: slug, Title: title, Base: g.Base, Authors: slices.Clone(g.Authors)}
	if g.Set != nil {
		m.ASINs = append(m.ASINs, g.Set.ASIN)
	}
	for _, p := range g.Parts {
		for _, a := range p.Rec.asins() {
			m.ASINs = append(m.ASINs, a.ASIN)
		}
	}
	seen := map[string]bool{}
	for _, w := range append(g.partSlugs(), slug) {
		for _, s := range idx.seriesOf[w] {
			if seen[s] || dramatizedSeries(idx.series[s].str("name")) {
				continue
			}
			seen[s] = true
			m.Series = append(m.Series, s)
		}
	}
	slices.Sort(m.Series)
	return m
}

// apply queues the settled plan on the store. Nothing reaches disk until the
// caller flushes.
func apply(store *pack.Store, p *planned) error {
	for _, slug := range sortedKeys(p.works) {
		raw, err := p.works[slug].raw()
		if err != nil {
			return fmt.Errorf("work %s: %w", slug, err)
		}
		if err := store.Upsert(pack.FamilyWorks, slug, raw); err != nil {
			return err
		}
	}
	for _, slug := range p.deletes {
		if err := store.Delete(pack.FamilyWorks, slug); err != nil {
			return err
		}
	}
	for _, sp := range p.series {
		entry, err := seriesEntry(store, sp)
		if err != nil {
			return err
		}
		if err := store.Upsert(pack.FamilySeries, sp.Slug, entry); err != nil {
			return err
		}
	}
	return nil
}

// seriesEntry re-renders one series entry with its repaired membership list,
// reading the record back off the store so nothing else about it is touched.
func seriesEntry(store *pack.Store, sp seriesPlan) ([]byte, error) {
	raw, ok, err := store.Get(pack.FamilySeries, sp.Slug)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("series %s: entry vanished between plan and write", sp.Slug)
	}
	o, err := decodeObj(raw)
	if err != nil {
		return nil, fmt.Errorf("series %s: %w", sp.Slug, err)
	}
	o.set("works", sp.Works)
	return o.raw()
}

// dedupeRefusals drops the repeats a multi-round plan can produce, keeping the
// first of each, so a refusal is reported once however many rounds settled it.
func dedupeRefusals(rs []Refusal) []Refusal {
	seen := map[string]bool{}
	out := rs[:0]
	for _, r := range rs {
		key := r.Category + "\x00" + r.Subject + "\x00" + r.Reason + "\x00" + strings.Join(r.Entries, ",")
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, r)
	}
	return out
}

// sortRefusals orders refusals by category then subject, so two runs report the
// same list in the same order.
func sortRefusals(rs []Refusal) []Refusal {
	slices.SortFunc(rs, func(a, b Refusal) int {
		if c := strings.Compare(a.Category, b.Category); c != 0 {
			return c
		}
		if c := strings.Compare(a.Subject, b.Subject); c != 0 {
			return c
		}
		return strings.Compare(a.Reason, b.Reason)
	})
	return rs
}
