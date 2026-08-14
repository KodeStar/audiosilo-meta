package audit

import (
	"fmt"

	"github.com/kodestar/audiosilo-meta/internal/titlerule"
)

// vetostrip.go holds the two vetoes found by REVIEWING a live wave's own merges, rather
// than by reading the proposals (which is how the first five were found) or by running
// the repair (the three in vetoruntime.go).
//
// Two of wave 1 chunk 2's pairs were different books that had met on a key only because
// each side shed a DIFFERENT series name; a full re-measurement found three more of that
// shape, and the exhaustive review of every remaining non-advisory proposal that followed
// found the second veto's shape - a title stating a volume the catalogue contradicts.
//
// The first rule is titlerule.SameTitleUnderCommonSeries - the soundness condition on a
// one-sided key, stated once at the leaf so any consumer reads the same sentence rather
// than a second spelling. See there for the mechanism, the titles it was measured on,
// the three correct merges it costs and why pkg/check's pairwise predicate deliberately
// does not ask it.

// vetoStrippedSeriesDiffers: two members meet on a key that each of them reached by
// shedding a different series name, so the key equality is about the template they
// share rather than about the book.
//
// It is asked only of a pair that MET ON THE SAME KEY. A cluster is transitively closed
// over shared works before anything is proposed (closeClusters), so two members of one
// closed cluster need not have met each other at all - they can have met a third member
// on two different keys, and the closure's premise is exactly that transitivity. Asking
// this of such a pair reads a disagreement that neither key ever claimed: it fired on
// the six Dante records, where "The Divine Comedy: Inferno" (cleaned against that
// series) and "The Inferno from The Divine Comedy" (a member of nothing, its mid-title
// series mention correctly left alone by the boundary anchoring) meet through the four
// members between them.
func vetoStrippedSeriesDiffers(members []dupMember) (string, bool) {
	for i := range members {
		for j := i + 1; j < len(members); j++ {
			a, b := members[i], members[j]
			if a.wk.key != b.wk.key {
				continue
			}
			if titlerule.SameTitleUnderCommonSeries(a.work.Title, a.wk.series, b.work.Title, b.wk.series) {
				continue
			}
			return fmt.Sprintf("%s was cleaned against series %q and %s against %q, and the two titles no longer meet when either "+
				"name is removed from both: they share the part neither shed, not the book",
				a.work.ID, a.wk.series, b.work.ID, b.wk.series), true
		}
	}
	return "", false
}

// vetoStatedVolumeElsewhere: a member's own TITLE states which volume of a series it
// is, and the catalogue places ANOTHER member of the cluster at a different position
// in that same series. The catalogue and the title then agree that these are two
// volumes, and it takes both to see it.
//
// It is the second half of internal/importer's position veto
// (refuseDuplicateIdentity's "(1) NEGATIVE" arm) asked of two CATALOGUED works, and it
// is not vetoPositionConflict: that rule compares two memberships, so it is silent
// whenever the volume-stating side is modeled in nothing - which is the shape it was
// found in. "Adaptation: (A Post-Apocalyptic Tale of Dystopian Survival) Empty Bodies
// Series, Book 2" is a member of nothing; its own title says Book 2, the catalogue
// puts "Empty Bodies" at position 1 of that series, and every other rung cleared (one
// author, one narrator, 298 against 294 minutes - two short novellas of one series).
// The cleaned title is "Empty Bodies" for both because "Adaptation" is packaging
// vocabulary and came off as a leading fluff segment, leaving the series reference to
// clean to the series name.
//
// The POSITIVE arm of the importer's rule (silence about the matched work's position
// is a veto too) is deliberately NOT taken. Refusing an import costs a re-importable
// row; withholding a merge here costs the class its whole calibration - "Hammered:
// The Iron Druid Chronicles, Book 3" beside the plain "Hammered" is only confirmable
// when the plain twin is modeled in that series, and most of this tree's duplicate
// pairs are modeled in nothing. Measured over the 279k-work tree, the NEGATIVE arm
// moves exactly ONE cluster (the one above), while the POSITIVE arm fires on 288 and
// would withhold 57 more non-advisory merges - the first of them "Endangered: Zak Bates
// Eco-Adventure, Book 2" against "Endangered: Zak Bates Eco-Adventure Series, Book 2",
// one book whose two records differ by the word "Series", where the arm fires because
// the side asking is the modeled one and the side asked about is not.
//
// A NARROWER variant was measured too and also declined: "a member states a volume
// against NO series at all, and another member states none" - the shape of "Medical
// Mysteries Across History, Pt.2" and "Our Vietnam Wars, Volume 2", where the marker
// attaches to the title itself rather than to a series reference. It withholds 37
// clusters to catch those 2, and the other 35 are correct merges of exactly the shape
// IdentityTitleKey is calibrated on ("All In, Book 3" against "All In"): the four
// Ottoline books, "The Lost Hero", "The Mark of Athena", "The Red Pyramid", the Study
// trilogy, "Iron Widow", three TimeRiders volumes, "W. A. R. P. The Reluctant
// Assassin". Those two pairs are recorded as reviewed wrong merges in the wave's
// worklist instead - which is what an exhaustively reviewed worklist is for.
func vetoStatedVolumeElsewhere(ix *index, members []dupMember) (string, bool) {
	for _, m := range members {
		d := ix.derived(m.work)
		if d.seriesID == "" || !d.hasStatedSeq {
			continue
		}
		for _, other := range members {
			if other.work.ID == m.work.ID {
				continue
			}
			span, placed := ix.positionSpans(other.work.ID)[d.seriesID]
			if !placed || (span[0] <= d.statedSeq && d.statedSeq <= span[1]) {
				continue
			}
			return fmt.Sprintf("%s states volume %s of series %s in its own title and the catalogue places %s at %s in that series: "+
				"the title and the catalogue agree that these are two volumes",
				m.work.ID, formatSeq(d.statedSeq), d.seriesID, other.work.ID, renderSpan(span)), true
		}
	}
	return "", false
}
