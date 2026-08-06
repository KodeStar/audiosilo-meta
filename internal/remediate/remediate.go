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
// metacheck and metafmt agree with.
package remediate

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/kodestar/audiosilo-meta/pkg/model"
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
// that follows.
type MissingPlain struct {
	Work    string
	Title   string
	Base    string
	Authors []string
	ASINs   []string
	Series  []string
}

// MergedWork is one book's merge, for the report.
type MergedWork struct {
	Slug   string
	Title  string
	Minted bool
	Parts  []string
	// Runtime is the merged recording's runtime in minutes, 0 when the merge
	// deliberately states none.
	Runtime int
}

// SeriesRepair is one series' repair, for the report.
type SeriesRepair struct {
	Slug    string
	Changes []string
}

// Report is everything a run did or declined to do.
type Report struct {
	PartWorks      int
	Groups         int
	Merged         []MergedWork
	Deleted        []string
	Twins          []twinMerge
	Series         []SeriesRepair
	MissingPlain   []MissingPlain
	Refusals       []Refusal
	Wrote          []string
	RemovedPacks   []string
	Applied        bool
	CompleteSets   int
	MatchedSets    int
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
	rows, err := loadCompleteSets(opts.CompleteSets)
	if err != nil {
		return nil, err
	}

	groups, refusals := buildGroups(idx)
	refusals = append(refusals, matchTargets(idx, groups)...)
	refusals = append(refusals, matchCompleteSets(groups, rows)...)

	rep := &Report{Groups: len(groups), CompleteSets: len(rows)}
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

	rep.Refusals = dedupeRefusals(append(refusals, res.refusals...))
	sortRefusals(rep.Refusals)
	rep.Merged = res.report
	rep.Deleted = res.deletes
	rep.Twins = res.twins
	rep.MissingPlain = res.missing
	rep.SwappedToPlain = res.swapped
	for _, sp := range res.series {
		rep.Series = append(rep.Series, SeriesRepair{Slug: sp.Slug, Changes: sp.Changes})
	}

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
	report   []MergedWork
	swapped  int
}

// buildPlan works the whole change out without writing anything. blocked names
// the groups a series refusal makes unmergeable; a non-empty blocked set means
// the plan has to be rebuilt without them.
func buildPlan(idx *index, groups []*group, today string) (*planned, map[*group]bool, []Refusal) {
	p := &planned{works: map[string]obj{}}

	var active []*group
	composed := map[string]merged{}
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
	}

	rewrites := map[string]rewrite{}
	deletes := map[string]bool{}
	for _, g := range active {
		for _, s := range g.partSlugs() {
			rewrites[s] = rewrite{To: g.Slug, FromPart: true, Group: g}
			deletes[s] = true
		}
	}

	// The plain text edition each merged book's series slot belongs to.
	swaps := map[string]string{}
	for _, g := range active {
		twin, n := idx.plainTwin(g.Base, g.Authors)
		switch {
		case n == 1:
			swaps[g.Slug] = twin
		case n > 1:
			p.refusals = append(p.refusals, Refusal{Category: catAmbiguousTwin, Subject: g.Base,
				Reason:  "more than one plain work matches this book, so no series slot was swapped",
				Entries: idx.plain[plainKey(g.Base, g.Authors)]})
		default:
			p.missing = append(p.missing, missingPlainFor(idx, g))
		}
	}

	// The twin pass reads the post-merge catalogue: the works this run composed
	// plus the dramatized cohort works it left alone.
	view := map[string]cohortWork{}
	for slug, m := range composed {
		view[slug] = cohortWork{work: m.Work}
	}
	for _, slug := range sortedKeys(idx.candidates) {
		if _, ok := view[slug]; ok || deletes[slug] {
			continue
		}
		c := idx.candidates[slug]
		if !isGraphicAudio(c.obj) || !isDramatized(c.title()) {
			continue
		}
		view[slug] = cohortWork{work: c.obj}
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
	for old, rw := range rewrites {
		if s, ok := survivorOf[rw.To]; ok {
			rw.To = s
			rewrites[old] = rw
		}
	}
	for loser, survivor := range survivorOf {
		if _, ok := rewrites[loser]; !ok {
			rewrites[loser] = rewrite{To: survivor}
		}
		if idx.works[loser] {
			deletes[loser] = true
		}
	}
	for slug, twin := range swaps {
		if s, ok := survivorOf[slug]; ok {
			swaps[s] = twin
			delete(swaps, slug)
		}
	}

	for slug, m := range composed {
		if _, gone := survivorOf[slug]; gone {
			continue
		}
		p.works[slug] = m.Work
	}
	for slug, w := range twinChanged {
		p.works[slug] = w
	}

	series, seriesRefusals, blocked := planSeries(idx, rewrites, swaps)
	if len(blocked) > 0 {
		// The refusals of a round that could not settle are the ones that say
		// WHY, so they travel out with the blocked groups; the next round sees
		// an untouched series and would report nothing at all.
		return nil, blocked, seriesRefusals
	}
	p.refusals = append(p.refusals, seriesRefusals...)
	p.series = series
	p.swapped = countSwaps(idx, series, swaps)

	p.deletes = sortedKeys(deletes)
	for _, slug := range sortedKeys(composed) {
		if _, gone := survivorOf[slug]; gone {
			continue
		}
		m := composed[slug]
		runtime := 0
		if _, rec, ok := soleRecording(p.works[slug]); ok {
			runtime, _ = rec.intAt("runtime_min")
		}
		p.report = append(p.report, MergedWork{Slug: slug, Title: m.Title, Minted: m.Minted, Parts: m.Parts, Runtime: runtime})
	}
	sort.Slice(p.report, func(i, j int) bool { return p.report[i].Slug < p.report[j].Slug })
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
func missingPlainFor(idx *index, g *group) MissingPlain {
	m := MissingPlain{Work: g.Slug, Title: g.Title, Base: g.Base, Authors: append([]string(nil), g.Authors...)}
	if g.Set != nil {
		m.ASINs = append(m.ASINs, g.Set.ASIN)
	}
	for _, p := range g.Parts {
		for _, a := range p.Rec.asins() {
			m.ASINs = append(m.ASINs, a.ASIN)
		}
	}
	seen := map[string]bool{}
	for _, w := range append(g.partSlugs(), g.Slug) {
		for _, s := range idx.seriesOf[w] {
			if seen[s] || dramatizedSeries(idx.series[s].str("name")) {
				continue
			}
			seen[s] = true
			m.Series = append(m.Series, s)
		}
	}
	sortStrings(m.Series)
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
	works := make([]model.SeriesWork, len(sp.Works))
	copy(works, sp.Works)
	if err := o.set("works", works); err != nil {
		return nil, err
	}
	return o.raw()
}

// dedupeRefusals drops the repeats a multi-round plan can produce, keeping the
// first of each, so a refusal is reported once however many rounds settled it.
func dedupeRefusals(rs []Refusal) []Refusal {
	seen := map[string]bool{}
	out := rs[:0]
	for _, r := range rs {
		key := r.Category + "\x00" + r.Subject + "\x00" + r.Reason + "\x00" + joinComma(r.Entries)
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
func sortRefusals(rs []Refusal) {
	sort.Slice(rs, func(i, j int) bool {
		if rs[i].Category != rs[j].Category {
			return rs[i].Category < rs[j].Category
		}
		if rs[i].Subject != rs[j].Subject {
			return rs[i].Subject < rs[j].Subject
		}
		return rs[i].Reason < rs[j].Reason
	})
}
