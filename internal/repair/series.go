package repair

import (
	"github.com/kodestar/audiosilo-meta/internal/audit"
	"github.com/kodestar/audiosilo-meta/internal/importer"
	"github.com/kodestar/audiosilo-meta/internal/rawentry"
	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// series.go applies SER-DUP's merge-series proposal: the losers' membership lists
// fold onto the canonical series and their slugs are tombstoned.
//
// It rewrites nothing outside the series family, and that is a property of the data
// model rather than an omission: a work does not reference its series, a series
// references its works. So folding two spellings of one series is a union of two
// membership lists - plus the series-to-series links (translation_of, ordering_of),
// which links.go re-points on every OTHER series naming a loser, and then holds the
// staged merge to pkg/check's link rules (a chain, a second hop, two series of one
// ordering in a family are refused; a merge that promotes or moves a variant is refused
// before anything is re-pointed).
//
// Field audit.FieldPosition is SER-DUP's family-renumbered fold (internal/audit/seriesfamily.go):
// a loser stating the target's ordering under other NUMBERS. There a loser membership
// whose work the target lists at another slot is not a conflict - the target's slot is
// kept and the loser's number is NAMED in the notes through noteLost, the one loss
// format - while a loser membership the target does not list at all still refuses: a
// renumbered fold only ever retires a spelling, it never adds to an ordering. No other
// field names a merge-series, so any other is malformed rather than ignored.
func (rn *runner) mergeSeries(t *txn, fd audit.Finding) error {
	target := fd.Propose.Target
	if f := fd.Propose.Field; f != "" && f != audit.FieldPosition {
		return refusef(CatMalformed, "merge-series names field %q; the only merge-series field is %q "+
			"(keep the target's numbering)", f, audit.FieldPosition)
	}
	// The explicit opt-in a reviewer accepted: never inferred from the lists.
	renumbered := fd.Propose.Field == audit.FieldPosition
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
				if importer.SameSlot(have, sw.Position) {
					continue // the same membership, spelled in two series
				}
				if renumbered {
					t.noteLost([]mergedFacts{{field: "position of " + sw.Work, kept: have, dropped: sw.Position}}, slug)
					continue
				}
				return refusef(CatPositionConflict,
					"%s lists %s at position %q while %s lists it at %q: two orderings are not one series",
					target, sw.Work, have, slug, sw.Position)
			}
			if renumbered {
				return refusef(CatPositionConflict,
					"%s lists %s at %q and %s does not list it: a renumbered fold keeps the target's numbering and only "+
						"retires a spelling, so it never adds a membership", slug, sw.Work, sw.Position, target)
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
		t.noteLost(mergeSeriesFields(merged, le), slug)
	}
	if err := refuseDuplicatePositions(target, works); err != nil {
		return err
	}
	retiring := importer.ToSet(sorted)
	if err := t.relinkTranslations(pack.FamilySeries, target, merged, sorted, loserEntries, retiring); err != nil {
		return err
	}
	if err := t.relinkOrderings(target, merged, sorted, loserEntries, retiring); err != nil {
		return err
	}
	t.setSeries(target, merged, works)
	for _, slug := range sorted {
		t.series.remove(slug)
		t.retire(pack.FamilySeries, slug)
		t.redirect(model.RedirectSeries, slug, target)
	}
	t.note("folded %d membership(s) onto %s and retired %s with a redirect", moved, target, joinList(sorted))
	return t.refuseLinkFaults(target)
}

// mergeSeriesFields folds a loser series' own facts into the surviving record and returns
// every value it could not keep, for the applied record's notes. The name is the
// target's - the ladder chose it - and everything else follows merge-works' rule: union
// the lists, fill only what the target does not state.
//
// A differing NAME is reported, compared exactly: a retired spelling is not cosmetic,
// since the importer joins a series by its name, so a source still spelling it the
// loser's way stops meeting the series once that spelling is gone - and a reviewer can
// only weigh that if the note names it. A case-only difference is reported too; the note
// is the audit trail, and saying less than what was deleted is what it exists to prevent.
// A differing xref member is reported by fillXref, exactly as for a work.
func mergeSeriesFields(merged, loser entry) []mergedFacts {
	rawentry.SetListOrDrop(merged, "authors", rawentry.AppendUnique(merged.Strs("authors"), loser.Strs("authors")))
	merged.Set("sources", rawentry.UnionSources(merged.Sources(), loser.Sources()))
	// name is schema-required, so the target always states one and fillStrings only
	// ever reports it.
	lost := fillStrings(merged, loser, "name")
	return append(lost, fillXref(merged, loser)...)
}
