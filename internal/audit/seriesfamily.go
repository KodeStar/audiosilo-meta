package audit

import (
	"cmp"
	"fmt"
	"slices"

	"github.com/kodestar/audiosilo-meta/internal/importer"
	"github.com/kodestar/audiosilo-meta/internal/titlerule"
	"github.com/kodestar/audiosilo-meta/pkg/model"
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
// as a pair, by the whole-group veto list (seriesMergeVetoes) plus foldMovesNothing:
// a fold here may only RETIRE a spelling, because adding memberships to a reading order
// is a claim about the order. A family member is the only possible target, so a
// spelling folds onto the member it repeats, never onto another plain spelling - which
// is also what keeps the set consistent (a target here is never a loser anywhere).
//
// One proposal per loser, keyed "<group>/<loser>" and naming that loser alone, so a
// reviewed acceptance names one stable fold and never goes stale because a sibling
// spelling landed first.
//
// The pair judgement is foldOnto, shared with ordering-twin (seriestwin.go): the two
// are candidate generators over one rule.
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
	// SubclassFamilySpelling and SubclassFamilyRenumbered are exported because
	// internal/repair and the integration tests name them.
	SubclassFamilySpelling   = "family-spelling"
	SubclassFamilyRenumbered = "family-renumbered"
	// FieldPosition is the Field of a family-renumbered merge-series: the target's
	// numbering stands and the loser's is dropped. internal/repair applies a
	// merge-series naming it only on that explicit opt-in, never by inference.
	FieldPosition = "position"
)

// hasFamilyMember is familyVetoed's cheap gate: without a member of an ordering family
// the group has no target to fold onto, whatever vetoed it.
func hasFamilyMember(ix *index, group []seriesKeys) bool {
	return slices.ContainsFunc(group, func(k seriesKeys) bool { return ix.inOrderingFamily(k.series.ID) })
}

// familyVetoed reports whether a whole group's proposal is withheld for holding an
// ordering family or a translation - the two vetoes under which a plain spelling still
// has a fold of its own.
func familyVetoed(group []seriesKeys, sides []seriesSide) bool {
	if _, vetoed := vetoSeriesOrderingFamily(group); vetoed {
		return true
	}
	_, vetoed := vetoSeriesLanguagesDisagree(sides)
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
func statedOrdering(s *model.Series) string {
	return cmp.Or(s.Ordering, titlerule.ReadSeriesQualifiers(s.Name).Ordering)
}

// foldKind is what one loser's pair judgements came to.
type foldKind int

const (
	foldNone       foldKind = iota // no candidate shares a slot with it
	foldSpelling                   // exactly one candidate retires it cleanly
	foldAmbiguous                  // several do
	foldRenumbered                 // none does, and exactly one holds its list under other numbers
	foldReview                     // none does: the candidate sharing the most same-slot works
)

// foldVerdict is foldOnto's answer for one loser. target is set once, by the kind:
// the candidate it folds onto or names in a review (for foldAmbiguous, the first passer).
type foldVerdict struct {
	loser   seriesKeys
	kind    foldKind
	target  seriesKeys
	passers []seriesKeys
	// vetoes are the pair vetoes against target, for the review that names them.
	vetoes []string
	// reviewedBy are the containers whose REVIEW settled an ambiguity (closesToRoot).
	reviewedBy []string
}

// root is the candidate the loser resolves toward, for closesToRoot: none for an
// ambiguous or empty verdict, which resolve toward nothing.
func (v foldVerdict) root() string {
	if v.kind == foldNone || v.kind == foldAmbiguous {
		return ""
	}
	return v.target.series.ID
}

// foldOnto judges a loser against every candidate as a pair - eligibility, then the
// whole veto list plus foldMovesNothing (retiresCleanly) - and returns the one verdict
// both family-spelling and ordering-twin read. sides holds the loser's and every
// candidate's side.
func foldOnto(ix *index, loser seriesKeys, candidates []seriesKeys, sides map[string]seriesSide) foldVerdict {
	v := foldVerdict{loser: loser}
	var renumbered []seriesKeys
	var affinity *seriesKeys
	var affinityVetoes []string
	bestShared := 0
	ls := sides[loser.series.ID]
	for i, c := range candidates {
		if !foldEligible(loser, c, sides) {
			continue
		}
		cs := sides[c.series.ID]
		clean, vetoes := retiresCleanly(ix, loser, c, sides)
		if clean {
			v.passers = append(v.passers, c)
			continue
		}
		// A renumbering always trips the ordering-agreement veto, so the ONLY veto it
		// may carry is that one.
		if len(vetoes) == 1 && renumberedOnto(cs, ls) {
			renumbered = append(renumbered, c)
		}
		// The affinity ladder: most same-slot works, then titlerule's own series rank.
		if n := sameSlotWorks(cs, ls); n > 0 && (n > bestShared || n == bestShared &&
			(titlerule.SeriesRank{Works: len(c.series.Works), ID: c.series.ID}).Better(
				titlerule.SeriesRank{Works: len(affinity.series.Works), ID: affinity.series.ID})) {
			bestShared, affinity, affinityVetoes = n, &candidates[i], vetoes
		}
	}
	switch {
	case len(v.passers) == 1:
		v.kind, v.target = foldSpelling, v.passers[0]
	case len(v.passers) > 1:
		v.kind, v.target = foldAmbiguous, v.passers[0]
	case len(renumbered) == 1:
		v.kind, v.target = foldRenumbered, renumbered[0]
	case affinity != nil:
		v.kind, v.target, v.vetoes = foldReview, *affinity, affinityVetoes
	}
	return v
}

// retiresCleanly is THE fold test: the candidate already holds every loser membership
// at the same slot and no veto fires. vetoes are returned for the caller that reads
// them (a renumbering, a review).
func retiresCleanly(ix *index, loser, c seriesKeys, sides map[string]seriesSide) (bool, []string) {
	vetoes := seriesMergeVetoesOver(ix, []seriesKeys{loser, c},
		[]seriesSide{sides[loser.series.ID], sides[c.series.ID]}, c.series.ID)
	return len(vetoes) == 0 && foldMovesNothing(sides[c.series.ID], sides[loser.series.ID]), vetoes
}

// foldEligible reports whether candidate c may be loser l's target at all: l states no
// ordering or c's, and no stated member language disagrees.
func foldEligible(l, c seriesKeys, sides map[string]seriesSide) bool {
	if o := statedOrdering(l.series); o != "" && o != statedOrdering(c.series) {
		return false
	}
	_, disagree := vetoSeriesLanguagesDisagree([]seriesSide{sides[l.series.ID], sides[c.series.ID]})
	return !disagree
}

// familySpellingFolds is the family folds of one vetoed group, keyed under its key.
// claimed are the series a mechanical same-decoration subgroup already folds; they are
// neither a loser nor a target here. sides are the group's, by id. clustersOf is W-DUP's
// work -> cluster keys.
func familySpellingFolds(ix *index, key string, group []seriesKeys, sides map[string]seriesSide, claimed map[string]bool, clustersOf map[string][]string) []Finding {
	var family, plain []seriesKeys
	for _, k := range group {
		switch id := k.series.ID; {
		case claimed[id]:
		case ix.inOrderingFamily(id):
			family = append(family, k)
		case !ix.translationLinked(id):
			plain = append(plain, k)
		}
	}
	if len(family) == 0 || len(plain) == 0 {
		return nil
	}
	verdicts := make([]foldVerdict, 0, len(plain))
	for _, l := range plain {
		verdicts = append(verdicts, foldOnto(ix, l, family, sides))
	}
	// An ambiguity is settled against the verdicts as JUDGED, so no settlement depends
	// on the order another was settled in.
	settled := slices.Clone(verdicts)
	for i, v := range verdicts {
		if v.kind != foldAmbiguous {
			continue
		}
		if root, reviewed, ok := closesToRoot(v, verdicts, sides); ok {
			settled[i].kind, settled[i].target, settled[i].reviewedBy = foldSpelling, root, reviewed
		}
	}

	var out []Finding
	for i, v := range settled {
		l, t := v.loser, v.target
		fd := Finding{Subclass: SubclassFamilySpelling, Key: key + "/" + l.series.ID}
		notes := []string{"ordering family in this group: " + truncateList(seriesIDs(family), 6)}
		switch v.kind {
		case foldSpelling:
			fd.Propose = familySpellingProposal(t, l)
			if verdicts[i].kind == foldAmbiguous {
				fd.Propose.Reason += "; several orderings hold its list, and the spelling that contains it resolves to " + t.series.ID
			}
			if len(v.reviewedBy) > 0 {
				// A root read off a REVIEW (the container's best match, not a fold) is
				// review-level evidence, so the fold it settles is no surer than that.
				fd.Propose.Advisory = true
				fd.Propose.Reason += "; but " + truncateList(v.reviewedBy, 4) + " resolves there only as a review " +
					"(its own record), so this fold waits on that review"
			}
		case foldAmbiguous:
			fd.Propose = Proposal{Op: OpReview, Target: t.series.ID, Others: []string{l.series.ID}, Advisory: true,
				Reason: fmt.Sprintf("%s repeats the list of %s alike, slot for slot: which reading order the plain name means is a human's call",
					l.series.ID, truncateList(seriesIDs(v.passers), 4))}
		case foldRenumbered:
			fd.Subclass = SubclassFamilyRenumbered
			fd.Propose = renumberedProposal(sides[t.series.ID], sides[l.series.ID], statedOrdering(t.series))
		case foldReview:
			var conflicts []string
			fd.Propose, conflicts = familyReview(t, l, v.vetoes, sides, clustersOf)
			notes = append(notes, conflicts...)
		default:
			continue // nothing in the family shares a slot with it: no evidence it is one of them
		}
		fd.Series = []SeriesRef{ix.seriesRef(t.series), ix.seriesRef(l.series)}
		fd.Notes = notes
		out = append(out, fd)
	}
	return out
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
//
// reviewed names the containers whose verdict is itself only a REVIEW (foldReview: its
// best-match member, not a fold): a root read off one is review-level evidence, so the
// fold it settles is advisory, however clean it is on its own.
func closesToRoot(v foldVerdict, all []foldVerdict, sides map[string]seriesSide) (root seriesKeys, reviewed []string, ok bool) {
	var roots []string
	for _, o := range all {
		if o.loser.series.ID == v.loser.series.ID || !foldMovesNothing(sides[o.loser.series.ID], sides[v.loser.series.ID]) {
			continue
		}
		r := o.root()
		if r == "" {
			continue
		}
		if !slices.Contains(roots, r) {
			roots = append(roots, r)
		}
		if o.kind == foldReview {
			reviewed = append(reviewed, o.loser.series.ID)
		}
	}
	if len(roots) != 1 {
		return seriesKeys{}, nil, false
	}
	for _, p := range v.passers {
		if p.series.ID == roots[0] {
			return p, sortedUnique(reviewed), true
		}
	}
	return seriesKeys{}, nil, false
}

// familyReview is the review a plain spelling gets when no member passes: the member
// it shares the most same-slot works with, the vetoes foldOnto judged the pair by, and -
// per conflicting slot - the W-DUP clusters holding the works there, since a duplicate
// work is usually all that stands between the two lists. conflicts are the per-slot notes.
func familyReview(t, l seriesKeys, vetoes []string, sides map[string]seriesSide, clustersOf map[string][]string) (Proposal, []string) {
	ts, ls := sides[t.series.ID], sides[l.series.ID]
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
		held := ts.positions()
		for _, m := range ls.members {
			if _, ok := held[m.work]; !ok {
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
	at := target.positions()
	n := 0
	for _, m := range loser.members {
		if pos, held := at[m.work]; held && importer.SameSlot(pos, m.position) {
			n++
		}
	}
	return n
}

// renumberedOnto reports whether a loser stating the target's ordering (statedOrdering,
// both sides) is the target's list under other NUMBERS: every work it lists the target
// lists, the two agree on their relative order (the loser's members sorted by its own
// positions sit at non-decreasing positions in the target), and at least one number
// differs - which is what separates it from a pure retirement. Positions are read
// through importer.PositionSpan, the package's one position grammar; a position it
// rejects decides nothing. vetoSeriesOrderingDisagrees asks it, so a renumbering is a
// veto with a reason of its own.
func renumberedOnto(target, loser seriesSide) bool {
	ordering := statedOrdering(loser.series)
	if ordering == "" || ordering != statedOrdering(target.series) || len(loser.members) == 0 {
		return false
	}
	at := make(map[string][2]float64, len(target.members))
	for w, pos := range target.positions() {
		span, ok := importer.PositionSpan(pos)
		if !ok {
			return false
		}
		at[w] = span
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
	at := target.positions()
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
		Field:    FieldPosition,
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
