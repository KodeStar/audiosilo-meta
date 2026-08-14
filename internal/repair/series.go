package repair

import (
	"sort"

	"github.com/kodestar/audiosilo-meta/internal/audit"
	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// series.go applies SER-DUP's merge-series proposal: the losers' membership lists
// fold onto the canonical series and their slugs are tombstoned.
//
// It rewrites nothing outside the series family, and that is a property of the data
// model rather than an omission: a work does not reference its series, a series
// references its works. So folding two spellings of one series is exactly a union of
// two membership lists - which is also why it is a much smaller change than
// merge-works, with one refusal instead of three.
func (rn *runner) mergeSeries(t *txn, fd audit.Finding) error {
	target, losers := fd.Propose.Target, fd.Propose.Others
	if target == "" || len(losers) == 0 {
		return refusef(catMalformed, "the proposal names no target or no other members")
	}
	for _, slug := range append([]string{target}, losers...) {
		if by, ok := t.p.retiredBy(pack.FamilySeries, slug); ok {
			return refusef(catRetired, "series %q was retired by an earlier proposal in this run (%s)", slug, by)
		}
	}
	te, ok, err := t.series.get(target)
	if err != nil {
		return err
	}
	if !ok {
		return refusef(catMissing, "the target series %q is not in the tree", target)
	}
	sorted := append([]string{}, losers...)
	sort.Strings(sorted)

	merged := te.clone()
	works := merged.seriesWorks()
	// byWork and byPosition are the two ways a union can contradict itself: one
	// work at two positions, and two works at one position. Both are refusals, and
	// the second is what pkg/check enforces.
	byWork := map[string]string{}
	byPosition := map[string]string{}
	for _, sw := range works {
		byWork[sw.Work] = sw.Position
		byPosition[sw.Position] = sw.Work
	}
	var moved int
	for _, slug := range sorted {
		if slug == target {
			return refusef(catMalformed, "the proposal names %q as both the target and a loser", slug)
		}
		le, ok, err := t.series.get(slug)
		if err != nil {
			return err
		}
		if !ok {
			return refusef(catMissing, "the series %q the proposal folds onto %q is not in the tree", slug, target)
		}
		for _, sw := range le.seriesWorks() {
			if have, member := byWork[sw.Work]; member {
				if samePosition(have, sw.Position) {
					continue // the same membership, spelled in two series
				}
				return refusef(catPositionConflict,
					"%s lists %s at position %q while %s lists it at %q: two orderings are not one series",
					target, sw.Work, have, slug, sw.Position)
			}
			if other, taken := byPosition[sw.Position]; taken {
				return refusef(catPositionConflict,
					"position %q of %s is held by %s, and %s puts %s there: the merged series would hold two works at one position",
					sw.Position, target, other, slug, sw.Work)
			}
			works = append(works, sw)
			byWork[sw.Work] = sw.Position
			byPosition[sw.Position] = sw.Work
			moved++
		}
		mergeSeriesFields(merged, le)
	}
	if err := refuseDuplicatePositions(target, works); err != nil {
		return err
	}
	t.setSeries(target, merged, works)
	for _, slug := range sorted {
		t.series.remove(slug)
		t.retire(pack.FamilySeries, slug)
		t.redirect(model.RedirectSeries, slug, target)
	}
	t.note("folded %d membership(s) onto %s and retired %s with a redirect", moved, target, joinList(sorted))
	return nil
}

// mergeSeriesFields folds a loser series' own facts into the surviving record. The
// name is the target's - the ladder chose it - and everything else follows
// merge-works' rule: union the lists, fill only what the target does not state.
func mergeSeriesFields(merged, loser entry) {
	setListOrDrop(merged, "authors", appendUnique(merged.strs("authors"), loser.strs("authors")))
	merged.set("sources", unionSources(merged.sources(), loser.sources()))
	fillXref(merged, loser)
}
