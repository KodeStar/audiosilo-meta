package repair

import (
	"github.com/kodestar/audiosilo-meta/internal/audit"
	"github.com/kodestar/audiosilo-meta/internal/rawentry"
	"github.com/kodestar/audiosilo-meta/internal/titlerule"
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
	target := fd.Propose.Target
	te, sorted, loserEntries, err := t.loadCluster(pack.FamilySeries, "series", fd.Propose)
	if err != nil {
		return err
	}

	merged := te.Clone()
	works := merged.SeriesWorks()
	// byWork and byPosition are the two ways a union can contradict itself: one work at
	// two positions, and two works at one position. Both are refusals. byPosition is keyed
	// by SLOT rather than by the stored string, because "3" and "03" are one place in the
	// order while pkg/check - which compares the strings - would accept the result.
	byWork := map[string]string{}
	byPosition := map[string]string{}
	for _, sw := range works {
		byWork[sw.Work] = sw.Position
		byPosition[slotKey(sw.Position)] = sw.Work
	}
	var moved int
	for i, slug := range sorted {
		le := loserEntries[i]
		for _, sw := range le.SeriesWorks() {
			if have, member := byWork[sw.Work]; member {
				if titlerule.SameSlot(have, sw.Position) {
					continue // the same membership, spelled in two series
				}
				return refusef(CatPositionConflict,
					"%s lists %s at position %q while %s lists it at %q: two orderings are not one series",
					target, sw.Work, have, slug, sw.Position)
			}
			if other, taken := byPosition[slotKey(sw.Position)]; taken {
				return refusef(CatPositionConflict,
					"position %q of %s is held by %s, and %s puts %s there: the merged series would hold two works at one position",
					sw.Position, target, other, slug, sw.Work)
			}
			works = append(works, sw)
			byWork[sw.Work] = sw.Position
			byPosition[slotKey(sw.Position)] = sw.Work
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
	rawentry.SetListOrDrop(merged, "authors", rawentry.AppendUnique(merged.Strs("authors"), loser.Strs("authors")))
	merged.Set("sources", rawentry.UnionSources(merged.Sources(), loser.Sources()))
	fillXref(merged, loser)
}
