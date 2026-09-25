package audit

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/kodestar/audiosilo-meta/internal/importer"
	"github.com/kodestar/audiosilo-meta/internal/titlerule"
)

// vetostated.go holds the ONE-SIDED STATEMENT vetoes, found by hand-reviewing the
// non-advisory title-author proposals left after the clean-twin wave (PR #2372): ten
// of them were wrong or doubtful and were kept out of the wave only by worklist
// narrowing, so an unfiltered metarepair run would have merged them. Most share one
// shape - one title STATES something (a later volume, an edition ordinal, a
// young-readers adaptation, a TV tie-in, a bundle) and its plain-titled sibling states
// nothing - and one-sided silence is deliberately never read as a disagreement
// (VolumeStatement.Agrees; the general "silence is a veto" rule was measured and
// declined at 57 correct merges, see vetoStatedVolumeElsewhere). So each veto below is
// one SPECIFIC stated shape, over a titlerule predicate (stated.go there), and all of
// them are advisory at SOURCE, reaching internal/repair through its fresh-audit gate.
//
// The bundle shape needed no veto of its own: "(2 in 1)" carried no collection word,
// so titlerule.IsCollection now reads the "N in 1" announcement and the existing
// vetoCollectionOneSide refuses the pair.

// vetoLaterVolumeOfPlainTitle: a member's title is ANOTHER member's whole title followed
// by a volume marker numbering it 2 or later ("Our Vietnam Wars, Volume 2" beside "Our
// Vietnam Wars"), so the plain title is the whole work or its first volume - never a
// second record of this one. (A part beside the whole it divides - "A Game of Thrones
// (Part Two)" - is a human's call too: whether a part product is a recording of the work
// is a modeling decision, not a mechanical one.)
//
// Two arms, because "Book N" after a title is ALSO the retailer's series-position
// convention - "All In, Book 3" beside "All In" is one book, and 35 correct merges of
// that shape are what declined the general rule. So the marker alone decides only for
// a PART-class word (titlerule.IsPartMarker: volume/vol/part/pt, which divide one work
// into pieces); any other marker needs the CATALOGUE to say the head is a series
// name - the stating member is modeled, at a position that is not 1, in a series whose
// name IS the plain title ("The Shadow Weaver, Book 2" at position 2 of "The Shadow
// Weaver"), where the plain record is that series' name and not its second volume.
//
// The plain member must state no volume of its own (two statements are statedVolumes'
// question), and the veto stands down when the catalogue already places the plain
// member at the stated volume - then the two records agree about which book it is.
func vetoLaterVolumeOfPlainTitle(ix *index, members []dupMember) (string, bool) {
	for _, a := range members {
		h, ok := titlerule.VolumeHeadOf(a.work.Title)
		if !ok || h.Volume < 2 {
			continue
		}
		head := titlerule.CompareKey(h.Head)
		if head == "" || headRestatesVolume(h) {
			continue
		}
		part := titlerule.IsPartMarker(h.Marker)
		series, named := "", false
		if !part {
			series, named = ix.laterVolumeOfSeriesNamed(a.work.ID, head)
			if !named {
				continue
			}
		}
		for _, b := range members {
			if b.work.ID == a.work.ID || titlerule.CompareKey(b.work.Title) != head || ix.statement(b.work).States {
				continue
			}
			if placedAt(ix.positionSpans(b.work.ID), h.Volume) {
				continue
			}
			if part {
				return fmt.Sprintf("%s is %s's own title followed by %q %s: the plain title is the whole work or its first part, "+
					"not a second record of this part", a.work.ID, b.work.ID, h.Marker, formatSeq(h.Volume)), true
			}
			return fmt.Sprintf("%s states %q %s after a title the catalogue models as the name of its series %s, which is %s's "+
				"whole title: the plain record names the series (or its first volume), not this later one",
				a.work.ID, h.Marker, formatSeq(h.Volume), series, b.work.ID), true
		}
	}
	return "", false
}

// headRestatesVolume reports that the head already carries the marker's number as a
// token of its own - "Z-Burbia 2: Parkway To Hell, Volume 2" is volume 2 of Z-Burbia
// said twice, not the second part of a work called "Z-Burbia 2: Parkway To Hell".
func headRestatesVolume(h titlerule.VolumeHead) bool {
	n := formatSeq(h.Volume)
	for _, tok := range strings.FieldsFunc(h.Head, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if tok == n {
			return true
		}
	}
	return false
}

// laterVolumeOfSeriesNamed reports the series (sorted-first, for a deterministic
// reason) a work is modeled in whose NAME compares equal to head, at a span that does
// not reach position 1.
func (ix *index) laterVolumeOfSeriesNamed(workID, head string) (string, bool) {
	spans := ix.positionSpans(workID)
	ids := make([]string, 0, len(spans))
	for sid := range spans {
		ids = append(ids, sid)
	}
	sort.Strings(ids)
	for _, sid := range ids {
		s := ix.seriesByID[sid]
		if s == nil || spans[sid][0] < 2 {
			continue
		}
		if titlerule.CompareKey(s.Name) == head {
			return sid, true
		}
	}
	return "", false
}

// placedAt reports whether any of a work's series spans covers volume v.
func placedAt(spans map[string][2]float64, v float64) bool {
	for _, span := range spans {
		if span[0] <= v && v <= span[1] {
			return true
		}
	}
	return false
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

// vetoAdaptedEditionOneSide: one member announces a DERIVED text - a young-readers
// adaptation ("Notes from a Young Black Chef (Adapted for Young Adults)") or a
// television tie-in ("Tangled: The Series") - and another does not. Both are
// different texts from the original rather than recordings of it. A dramatization is
// deliberately not in this vocabulary: "The Woman in White (Dramatized)" is the same
// work produced as a drama, and those merges are correct.
func vetoAdaptedEditionOneSide(members []dupMember) (string, bool) {
	for _, pred := range []struct {
		what string
		is   func(string) bool
	}{
		{"a young-readers adaptation", titlerule.IsYoungReadersAdaptation},
		{"a television-series tie-in", titlerule.IsSeriesTieIn},
	} {
		var yes, no []string
		for _, m := range members {
			if pred.is(m.work.Title) {
				yes = append(yes, m.work.ID)
			} else {
				no = append(no, m.work.ID)
			}
		}
		if len(yes) > 0 && len(no) > 0 {
			return fmt.Sprintf("%s announce %s and %s do not: an adapted text is a different work from the original",
				truncateList(yes, 3), pred.what, truncateList(no, 3)), true
		}
	}
	return "", false
}

// vetoCollectionsDiffer: EVERY member is a collection, and two of them hold no
// recording the importer's same-production rule (importer.RuntimesCompatible) would
// call one production. A collection's identity is its SELECTION, which a generic title
// ("The Friedrich Nietzsche Collection") does not state - two compilations of one
// author at 2,406 and 3,071 minutes are two selections. It is the both-sides twin of
// vetoCollectionOneSide, and asks the runtime only because the title cannot answer.
func vetoCollectionsDiffer(ix *index, members []dupMember) (string, bool) {
	for _, m := range members {
		if !ix.isCollection(m.work) {
			return "", false
		}
	}
	for i := range members {
		for j := i + 1; j < len(members); j++ {
			a, b := members[i].work, members[j].work
			known, compatible := false, false
			for _, ra := range a.Recordings {
				for _, rb := range b.Recordings {
					if ra.RuntimeMin <= 0 || rb.RuntimeMin <= 0 {
						continue
					}
					known = true
					if importer.RuntimesCompatible(ra.RuntimeMin, rb.RuntimeMin) {
						compatible = true
					}
				}
			}
			if known && !compatible {
				return fmt.Sprintf("%s and %s are both collections and no recording of one is within 10%% of a recording of the other: "+
					"a collection is its selection, and these are two selections", a.ID, b.ID), true
			}
		}
	}
	return "", false
}
