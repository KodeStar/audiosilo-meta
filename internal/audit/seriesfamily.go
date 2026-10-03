package audit

import (
	"cmp"
	"fmt"
	"slices"

	"github.com/kodestar/audiosilo-meta/internal/importer"
	"github.com/kodestar/audiosilo-meta/internal/titlerule"
)

// seriesfamily.go is SER-DUP's FAMILY folds: the plain spellings inside a group the
// whole-group proposal must leave alone.
//
// A group holding two members of one reading-order family (vetoSeriesOrderingFamily) or
// a translation (vetoSeriesLanguagesDisagree) is rightly review-only AS A WHOLE: a
// publication order and a chronological one are two lists to keep, and so are an
// English series and its Italian edition. But that veto also withheld the folds nobody
// disputes. "Chronicles of Narnia" holds the very list "The Chronicles of Narnia
// (Author's Preferred Order)" holds, slot for slot, and states no ordering at all; "The
// Kingsbridge Novels" is "Kingsbridge" (the chronological variant) spelled again, and
// "The Kingsbridge Novels (abridged)" a part of it. Those are spellings of ONE member
// of the family, and retiring them is this class's ordinary business.
//
// So every PLAIN member of such a group - one no ordering_of names or carries, no
// translation_of touches, whose stated ordering (the field, else its name's qualifier)
// is none or the target's, in no other language - is judged against each FAMILY member
// as a pair, by the whole-group veto list (seriesMergeVetoesWith) plus foldMovesNothing:
// a fold here may only RETIRE a spelling, because adding memberships to a reading order
// is a claim about the order. A family member is the only possible target, so a
// spelling folds onto the member it repeats, never onto another plain spelling - which
// is also what keeps the set consistent (a target here is never a loser anywhere).
//
// One proposal per loser, keyed "<group>/<loser>" and naming that loser alone, so a
// reviewed acceptance names one stable fold and never goes stale because a sibling
// spelling landed first.
//
//   - family-spelling: exactly one family member passes. Mechanical only when it is not
//     a variant: the importer's qualifier index (internal/importer/seriesqualified.go)
//     reaches a variant ONLY under its stated ordering, so once the plain name is
//     retired into one an unqualified claim of that name reaches it only through the
//     slug tombstone - which joins the survivor without comparing names - and whether
//     the plain name really means that reading order is a human's call. Several
//     passing members are an ambiguity a CONTAINING spelling may settle (closesToRoot);
//     otherwise it is a review.
//   - before the work merges land the two lists usually differ (Narnia's slot 7 holds
//     `07-the-last-battle` here and `the-last-battle` there): the member sharing the
//     most same-slot works is named in a REVIEW carrying the vetoes and the W-DUP
//     clusters holding each conflicting slot - never a mechanical fold in the same run
//     as the merge-works that would unblock it.
//   - family-renumbered: a loser STATING the target's ordering, listing only works the
//     target lists, in the same relative order, at other numbers ("Ranger's Apprentice
//     (published order)" follows Audible's numbering, which omits The Lost Stories). An
//     ALWAYS-advisory merge-series with Field "position": the target's numbering stands
//     and the loser's is dropped, each dropped number named in the repair's notes.
const (
	serDupFamily     = "family-spelling"
	serDupRenumbered = "family-renumbered"
)

// familyVetoed reports whether a whole group's proposal is withheld for holding an
// ordering family or a translation - the two vetoes under which a plain spelling still
// has a fold of its own.
func familyVetoed(ix *index, group []seriesKeys) bool {
	if _, vetoed := vetoSeriesOrderingFamily(group); vetoed {
		return true
	}
	_, vetoed := vetoSeriesLanguagesDisagree(seriesSides(ix, group))
	return vetoed
}

// inOrderingFamily reports whether a series is a member of a reading-order family:
// a variant, or the primary some variant names.
func (ix *index) inOrderingFamily(id string) bool {
	s := ix.seriesByID[id]
	return s != nil && (s.OrderingOf != "" || len(ix.variantsOf[id]) > 0)
}

// statedOrdering is the ordering a series states: its field, else its name's ordering
// qualifier (the importer's own reading, titlerule.ReadSeriesQualifiers).
func statedOrdering(k seriesKeys) string {
	return cmp.Or(k.series.Ordering, titlerule.ReadSeriesQualifiers(k.series.Name).Ordering)
}

// familyVerdict is what one plain spelling's pair judgements came to.
type familyVerdict struct {
	loser seriesKeys
	// passers are the family members it folds onto as a pure retirement.
	passers []seriesKeys
	// renumbered is the one member it is a renumbering of, when it has no passer.
	renumbered *seriesKeys
	// affinity is the member sharing the most same-slot works, for a review.
	affinity *seriesKeys
}

// root is the family member the spelling resolves toward, for closesToRoot: its one
// passer, else its renumbering target, else its affinity.
func (v familyVerdict) root() string {
	switch {
	case len(v.passers) == 1:
		return v.passers[0].series.ID
	case v.renumbered != nil:
		return v.renumbered.series.ID
	case v.affinity != nil:
		return v.affinity.series.ID
	}
	return ""
}

// familySpellingFolds is the family folds of one vetoed group, keyed under its key.
// claimed are the series a mechanical same-decoration subgroup already folds; they are
// neither a loser nor a target here. clustersOf is W-DUP's work -> cluster keys.
func familySpellingFolds(ix *index, key string, group []seriesKeys, claimed map[string]bool, clustersOf map[string][]string) []Finding {
	var family, plain []seriesKeys
	for _, k := range group {
		switch id := k.series.ID; {
		case claimed[id]:
		case ix.inOrderingFamily(id):
			family = append(family, k)
		case !ix.translationLinked[id]:
			plain = append(plain, k)
		}
	}
	if len(family) == 0 || len(plain) == 0 {
		return nil
	}
	sides := map[string]seriesSide{}
	for _, k := range append(slices.Clone(family), plain...) {
		sides[k.series.ID] = seriesSideOf(ix, k.series)
	}

	verdicts := make([]familyVerdict, 0, len(plain))
	for _, l := range plain {
		v := familyVerdict{loser: l}
		var renumbered []seriesKeys
		bestShared := 0
		for i, f := range family {
			if !familyEligible(l, f, sides) {
				continue
			}
			ls, fs := sides[l.series.ID], sides[f.series.ID]
			pair := []seriesKeys{l, f}
			if foldMovesNothing(fs, ls) && len(seriesMergeVetoesWith(ix, pair, f.series.ID, vetoOptions{restatedOrdering: f.series.Ordering})) == 0 {
				v.passers = append(v.passers, f)
				continue
			}
			if renumberedOnto(fs, ls, statedOrdering(l), statedOrdering(f)) &&
				len(seriesMergeVetoesWith(ix, pair, f.series.ID, vetoOptions{restatedOrdering: f.series.Ordering, renumbered: true})) == 0 {
				renumbered = append(renumbered, f)
			}
			// The affinity ladder: most same-slot works, then titlerule's own series rank.
			if n := sameSlotWorks(fs, ls); n > 0 && (n > bestShared || n == bestShared &&
				(titlerule.SeriesRank{Works: len(f.series.Works), ID: f.series.ID}).Better(
					titlerule.SeriesRank{Works: len(v.affinity.series.Works), ID: v.affinity.series.ID})) {
				bestShared, v.affinity = n, &family[i]
			}
		}
		if len(v.passers) == 0 && len(renumbered) == 1 {
			v.renumbered = &renumbered[0]
		}
		verdicts = append(verdicts, v)
	}

	var out []Finding
	for _, v := range verdicts {
		l := v.loser
		fd := Finding{Subclass: serDupFamily, Key: key + "/" + l.series.ID}
		notes := []string{"ordering family in this group: " + truncateList(seriesIDs(family), 6)}
		var target seriesKeys
		switch {
		case len(v.passers) == 1:
			target = v.passers[0]
			fd.Propose = familySpellingProposal(target, l)
		case len(v.passers) > 1:
			if root, ok := closesToRoot(v, verdicts, sides); ok {
				target = root
				fd.Propose = familySpellingProposal(target, l)
				fd.Propose.Reason += "; several orderings hold its list, and the spelling that contains it resolves to " + root.series.ID
				break
			}
			target = v.passers[0]
			fd.Propose = Proposal{Op: OpReview, Target: target.series.ID, Others: []string{l.series.ID}, Advisory: true,
				Reason: fmt.Sprintf("%s repeats the list of %s alike, slot for slot: which reading order the plain name means is a human's call",
					l.series.ID, truncateList(seriesIDs(v.passers), 4))}
		case v.renumbered != nil:
			target = *v.renumbered
			fd.Subclass = serDupRenumbered
			fd.Propose = renumberedProposal(sides[target.series.ID], sides[l.series.ID], statedOrdering(target))
		case v.affinity != nil:
			target = *v.affinity
			var conflicts []string
			fd.Propose, conflicts = familyReview(ix, target, l, sides, clustersOf)
			notes = append(notes, conflicts...)
		default:
			continue // nothing in the family shares a slot with it: no evidence it is one of them
		}
		fd.Series = []SeriesRef{ix.seriesRef(target.series), ix.seriesRef(l.series)}
		fd.Notes = notes
		out = append(out, fd)
	}
	return out
}

// familyEligible reports whether family member f may be a plain spelling l's target
// at all: l states no ordering or f's, and no stated member language disagrees.
func familyEligible(l, f seriesKeys, sides map[string]seriesSide) bool {
	if o := statedOrdering(l); o != "" && o != statedOrdering(f) {
		return false
	}
	_, disagree := vetoSeriesLanguagesDisagree([]seriesSide{sides[l.series.ID], sides[f.series.ID]})
	return !disagree
}

// familySpellingProposal is the fold of plain spelling l onto family member t:
// mechanical onto a primary, advisory onto a variant (see the file comment for why).
func familySpellingProposal(t, l seriesKeys) Proposal {
	p := Proposal{
		Op:     OpMergeSeries,
		Target: t.series.ID,
		Others: []string{l.series.ID},
		Reason: fmt.Sprintf("%s holds only memberships %s already holds at the same slots and states no other ordering: "+
			"retire the spelling into the ordering it repeats", l.series.ID, t.series.ID),
	}
	if t.series.OrderingOf != "" {
		p.Advisory = true
		p.Reason += fmt.Sprintf("; but %s is a reading-order VARIANT (ordering_of %s), which the importer's qualifier index reaches "+
			"only under its stated ordering (%s), so after the fold an unqualified claim of %q reaches it only through the slug "+
			"tombstone, which joins the survivor without comparing names - confirm the plain name means this order",
			t.series.ID, t.series.OrderingOf, t.series.Ordering, l.series.Name)
	}
	return p
}

// closesToRoot settles a spelling several family members hold alike: a plain spelling of
// the group that CONTAINS it (every membership at the same slot) and resolves to one of
// those members is the evidence of which one this spelling repeats. "The Kingsbridge
// Novels (abridged)" holds two volumes the publication and the chronological orders
// number alike; "The Kingsbridge Novels", which holds those two and two more, resolves
// to the chronological "Kingsbridge" - so the abridged spelling folds there, and the two
// spellings close onto one root rather than onto two orders.
func closesToRoot(v familyVerdict, all []familyVerdict, sides map[string]seriesSide) (seriesKeys, bool) {
	var roots []string
	for _, o := range all {
		if o.loser.series.ID == v.loser.series.ID || !foldMovesNothing(sides[o.loser.series.ID], sides[v.loser.series.ID]) {
			continue
		}
		if r := o.root(); r != "" && !slices.Contains(roots, r) {
			roots = append(roots, r)
		}
	}
	if len(roots) != 1 {
		return seriesKeys{}, false
	}
	for _, p := range v.passers {
		if p.series.ID == roots[0] {
			return p, true
		}
	}
	return seriesKeys{}, false
}

// familyReview is the review a plain spelling gets when no member passes: the member
// it shares the most same-slot works with, the vetoes, and - per conflicting slot - the
// W-DUP clusters holding the works there, since a duplicate work is usually all that
// stands between the two lists. conflicts are the per-slot notes.
func familyReview(ix *index, t, l seriesKeys, sides map[string]seriesSide, clustersOf map[string][]string) (Proposal, []string) {
	ts, ls := sides[t.series.ID], sides[l.series.ID]
	vetoes := seriesMergeVetoesWith(ix, []seriesKeys{l, t}, t.series.ID, vetoOptions{restatedOrdering: t.series.Ordering})
	var conflicts, clusterKeys []string
	for _, a := range ts.members {
		for _, b := range ls.members {
			sameWork, sameSlot := a.work == b.work, importer.SameSlot(a.position, b.position)
			if sameWork == sameSlot {
				continue // the same membership, or two unrelated ones
			}
			keys := sortedUnique(append(slices.Clone(clustersOf[a.work]), clustersOf[b.work]...))
			clusterKeys = append(clusterKeys, keys...)
			line := fmt.Sprintf("%s at %s in %s, %s at %s here", a.work, a.position, t.series.ID, b.work, b.position)
			if len(keys) > 0 {
				line += " (W-DUP " + truncateList(keys, 3) + ")"
			}
			conflicts = append(conflicts, line)
		}
	}
	if len(vetoes) == 0 {
		var added []string
		held := map[string]bool{}
		for _, m := range ts.members {
			held[m.work] = true
		}
		for _, m := range ls.members {
			if !held[m.work] {
				added = append(added, m.work+"@"+m.position)
			}
		}
		vetoes = []string{fmt.Sprintf("the fold would add %s to %s: adding to a reading order is a claim about the order, "+
			"not a retired spelling", truncateList(added, 4), t.series.ID)}
	}
	reason := "do not fold on this evidence: " + truncateList(vetoes, 4)
	if clusterKeys = sortedUnique(clusterKeys); len(clusterKeys) > 0 {
		reason += "; W-DUP " + truncateList(clusterKeys, 4) + " hold(s) the conflicting slots - merge those works first, " +
			"and the next audit judges this fold again"
	}
	return Proposal{Op: OpReview, Target: t.series.ID, Others: []string{l.series.ID}, Advisory: true, Reason: reason}, conflicts
}

// sameSlotWorks counts the loser's memberships the target holds at the same slot.
func sameSlotWorks(target, loser seriesSide) int {
	at := make(map[string]string, len(target.members))
	for _, m := range target.members {
		at[m.work] = m.position
	}
	n := 0
	for _, m := range loser.members {
		if pos, held := at[m.work]; held && importer.SameSlot(pos, m.position) {
			n++
		}
	}
	return n
}

// renumberedOnto reports whether a loser stating the target's ordering is the target's
// list under other NUMBERS: every work it lists the target lists, the two agree on their
// relative order (the loser's members sorted by its own positions sit at non-decreasing
// positions in the target), and at least one number differs - which is what separates it
// from a pure retirement. Positions are read through importer.PositionSpan, the
// package's one position grammar; a position it rejects decides nothing.
func renumberedOnto(target, loser seriesSide, loserOrdering, targetOrdering string) bool {
	if loserOrdering == "" || loserOrdering != targetOrdering || len(loser.members) == 0 {
		return false
	}
	at := make(map[string][2]float64, len(target.members))
	for _, m := range target.members {
		span, ok := importer.PositionSpan(m.position)
		if !ok {
			return false
		}
		at[m.work] = span
	}
	type pair struct{ own, there [2]float64 }
	pairs := make([]pair, 0, len(loser.members))
	differs := false
	for _, m := range loser.members {
		there, held := at[m.work]
		own, ok := importer.PositionSpan(m.position)
		if !held || !ok {
			return false
		}
		differs = differs || own != there
		pairs = append(pairs, pair{own, there})
	}
	slices.SortFunc(pairs, func(a, b pair) int {
		return cmp.Or(cmp.Compare(a.own[0], b.own[0]), cmp.Compare(a.own[1], b.own[1]))
	})
	for i := 1; i < len(pairs); i++ {
		p, q := pairs[i-1].there, pairs[i].there
		if q[0] < p[0] || q[0] == p[0] && q[1] < p[1] {
			return false
		}
	}
	return differs
}

// renumberedProposal is the always-advisory fold keeping the target's numbering.
func renumberedProposal(target, loser seriesSide, ordering string) Proposal {
	at := make(map[string]string, len(target.members))
	for _, m := range target.members {
		at[m.work] = m.position
	}
	var differ []string
	for _, m := range loser.members {
		if pos := at[m.work]; !importer.SameSlot(pos, m.position) {
			differ = append(differ, fmt.Sprintf("%s at %s here, %s in %s", m.work, m.position, pos, target.series.ID))
		}
	}
	slices.Sort(differ)
	return Proposal{
		Op:       OpMergeSeries,
		Target:   target.series.ID,
		Others:   []string{loser.series.ID},
		Field:    "position",
		Advisory: true,
		Reason: fmt.Sprintf("%s states the same %s order as %s and lists only its works, in the same relative order, under other "+
			"numbers (%s): a fold keeps %s's numbering and drops this one's - confirm which numbering is the publisher's",
			loser.series.ID, ordering, target.series.ID, truncateList(differ, 4), target.series.ID),
	}
}

// seriesIDs is the ids of a list of keyed series, in its own order.
func seriesIDs(ks []seriesKeys) []string {
	out := make([]string, 0, len(ks))
	for _, k := range ks {
		out = append(out, k.series.ID)
	}
	return out
}
