package audit

import (
	"fmt"

	"github.com/kodestar/audiosilo-meta/internal/titlerule"
)

// vetostated.go holds the ONE-SIDED STATEMENT vetoes: one title STATES something - a
// later volume, an edition ordinal, an adaptation, a series edition - and its
// plain-titled sibling states nothing. One-sided SILENCE is deliberately never a
// disagreement (VolumeStatement.Agrees; the general rule was measured and declined -
// see vetoStatedVolumeElsewhere), so each veto here is one SPECIFIC stated shape, over a
// titlerule predicate (stated.go there), advisory at SOURCE so internal/repair inherits
// it through its fresh-audit gate. The measurement is recorded in CLAUDE.md's
// internal/audit entry. The bundle shape ("(2 in 1)") needed no veto here: it is
// titlerule.IsCollection's, so vetoCollectionOneSide refuses it.

// vetoLaterVolumeOfPlainTitle: a member's title is ANOTHER member's whole title followed
// by a volume marker numbering it 2 or later ("Our Vietnam Wars, Volume 2" beside "Our
// Vietnam Wars"), so the plain title is the whole work or its first volume - never a
// second record of this one. (A part beside the whole it divides - "A Game of Thrones
// (Part Two)" - is a human's call too: whether a part product is a recording of the
// work is a modeling decision, not a mechanical one.)
//
// Two arms, because "Book N" after a title is ALSO the retailer's series-position
// convention (the "All In, Book 3" shape vetoStatedVolumeElsewhere's doc records). The
// marker alone decides only for a PART-class word (VolumeHead.IsPart); any other marker
// needs the CATALOGUE to say the head is a series name - the stating member is modeled,
// at a position that is not 1, in a series whose name IS the plain title ("The Shadow
// Weaver, Book 2" at position 2 of "The Shadow Weaver").
//
// The plain side is a member stating no volume whose title, cleaned against no series,
// IS the head - so a decorated plain twin ("Our Vietnam Wars (Unabridged)") is read as
// the plain title it is. A head that already carries the number (VolumeHead.
// RestatesVolume) is a series position said twice, not a part. The veto stands down when
// the catalogue places the plain member at the stated volume of the series that volume
// refers to - the named series, or the stating member's own - since the two records
// then agree about which book it is.
func vetoLaterVolumeOfPlainTitle(ix *index, members []dupMember) (string, bool) {
	// The plain-side key of every member, once: "" for a member that states a volume,
	// which is never the plain side.
	plain := make([]string, len(members))
	for i, m := range members {
		if !ix.statement(m.work).States {
			plain[i] = titlerule.CompareKey(ix.derived(m.work).plain)
		}
	}
	for _, a := range members {
		h, ok := ix.volumeHead(a.work)
		if !ok || h.Volume < 2 || h.RestatesVolume() {
			continue
		}
		head := titlerule.CompareKey(titlerule.Clean(h.Head, ""))
		if head == "" {
			continue
		}
		sid := ix.derived(a.work).seriesID
		if !h.IsPart() {
			if sid = ix.laterVolumeOfSeriesNamed(a.work.ID, head); sid == "" {
				continue
			}
		}
		for i, b := range members {
			if b.work.ID == a.work.ID || plain[i] != head {
				continue
			}
			if span, placed := ix.positionSpans(b.work.ID)[sid]; sid != "" && placed && spanCovers(span, h.Volume) {
				continue
			}
			if h.IsPart() {
				return fmt.Sprintf("%s is %s's own title followed by %q %s: the plain title is the whole work or its first part, "+
					"not a second record of this part", a.work.ID, b.work.ID, h.Marker, formatSeq(h.Volume)), true
			}
			return fmt.Sprintf("%s states %q %s after a title the catalogue models as the name of its series %s, which is %s's "+
				"whole title: the plain record names the series (or its first volume), not this later one",
				a.work.ID, h.Marker, formatSeq(h.Volume), sid, b.work.ID), true
		}
	}
	return "", false
}

// laterVolumeOfSeriesNamed is the series (the first in id order, so the reason is
// deterministic) a work is modeled in whose NAME compares equal to head, at a span that
// does not reach position 1 - or "".
func (ix *index) laterVolumeOfSeriesNamed(workID, head string) string {
	spans := ix.positionSpans(workID)
	for _, sid := range sortedKeys(spans) {
		if s := ix.seriesByID[sid]; s != nil && spans[sid][0] >= 2 && titlerule.CompareKey(s.Name) == head {
			return sid
		}
	}
	return ""
}

// vetoEditionOrdinalDiffers: two members' titles state DIFFERENT edition ordinals
// ("Security Analysis (Sixth Edition)" against "... (Seventh Edition)"). A numbered
// edition is a revised text, so two of them are two works a human may still choose to
// fold - never a mechanical merge. One-sided is NOT a veto: a single "Tenth Anniversary
// Edition" beside the plain title is a re-release (titlerule.EditionOrdinal does not
// even read an anniversary), and the catalogue's many "(AmazonClassics Edition)"
// records are the correct merges this class exists for.
func vetoEditionOrdinalDiffers(members []dupMember) (string, bool) {
	first, firstID := 0, ""
	for _, m := range members {
		n, ok := titlerule.EditionOrdinal(m.work.Title)
		if !ok {
			continue
		}
		if first == 0 {
			first, firstID = n, m.work.ID
			continue
		}
		if n != first {
			return fmt.Sprintf("%s states edition %d and %s edition %d: two numbered editions are revised texts, not two records of one",
				firstID, first, m.work.ID, n), true
		}
	}
	return "", false
}

// vetoAdaptedEditionOneSide: one member announces a DERIVED edition - a young-readers
// adaptation ("Notes from a Young Black Chef (Adapted for Young Adults)") or a
// "<title>: The Series" edition ("Tangled: The Series", the book of a television show) -
// and another does not. Either is a different text from the original rather than a
// recording of it. A dramatization is deliberately not in this vocabulary: "The Woman in
// White (Dramatized)" is the same work produced as a drama, and those merges are correct.
func vetoAdaptedEditionOneSide(members []dupMember) (string, bool) {
	for _, pred := range []struct {
		what string
		is   func(string) bool
	}{
		{"a young-readers adaptation", titlerule.IsYoungReadersAdaptation},
		{`a "<title>: The Series" edition`, titlerule.IsSeriesEdition},
	} {
		yes, no := oneSided(members, func(m dupMember) bool { return pred.is(m.work.Title) })
		if len(yes) > 0 && len(no) > 0 {
			return fmt.Sprintf("%s announce %s and %s do not: an adaptation or series edition is not a second record of the original",
				truncateList(yes, 3), pred.what, truncateList(no, 3)), true
		}
	}
	return "", false
}
