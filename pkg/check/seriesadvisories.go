package check

import (
	"sort"
	"strings"

	"github.com/kodestar/audiosilo-meta/internal/titlerule"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// seriesadvisories.go holds the two advisories about a SERIES that is not a series
// of its own: one whose name states a recording qualifier, and one whose every
// member another series already lists at the same position. Both name the shape a
// bulk import produced when a retailer listed each PRODUCTION of a serial as a series
// - "Harry Potter (Narrated by Stephen Fry)" holding exactly `harry-potter`'s seven
// novels, "Harry Potter (Full-Cast Editions)", "Discworld: Tiffany Aching (abridged)".
// A series is an ordered set of WORKS, and a work's series are all stamped onto a book
// a server matches, so every Harry Potter book showed three series. The importer and
// the intake form no longer found one (titlerule.StripSeriesRecordingQualifiers, read
// by internal/importer's cleanSeriesName and its qualifier index); these count what
// the tree already holds, in every metacheck run, so the repair is a number and a
// regrowth is visible the wave after it happens.
//
// Both are WARNINGS and must stay so. Measured over the 282,710-work tree at
// origin/main (2026-10-11): 189 series carry a recording qualifier and 199 are a
// duplicate of another series under the two-member floor below. Failing on them
// would turn every unrelated pull request red until a repair wave drained them, and
// both have members a human must tell apart - a sub-series numbered like its parent
// ("Farseer Trilogy" inside "Realms of the Elderlings"), a translated title spelled
// beside its original, a recording-variant series holding a volume the plain one
// does not - so the fix, a merge that retires a public slug, is a decision no rule
// may take on its own reading.

// checkSeriesRecordingQualifier reports a series whose NAME carries a recording
// qualifier (titlerule.StripSeriesRecordingQualifiers: a narrator credit, "(abridged)",
// "(gekürzt)", "(Full-Cast Editions)", "[Dramatized Adaptation]") and that states no
// link explaining it - neither translation_of nor ordering_of, either of which makes
// the series a deliberate, modelled view rather than a recording spelled as a series.
func checkSeriesRecordingQualifier(cat *model.Catalog, idx *pathIndex, warn addFunc) {
	for _, s := range cat.Series {
		if len(s.TranslationOf) > 0 || s.OrderingOf != "" {
			continue
		}
		base, ok := titlerule.StripSeriesRecordingQualifiers(s.Name)
		if !ok {
			continue
		}
		warn(idx.series[s], "series %q is named %q, the series %q with %s", s.ID, s.Name, base, recordingQualifierMarker)
	}
}

// recordingQualifierMarker is the series-recording-qualifier class's marker.
const recordingQualifierMarker = "a recording qualifier: narration and production are facts about a recording, not a series"

// duplicateSeriesMinShared is the fewest members a series must list for its
// containment in another to be reported. Measured over the tree at origin/main
// (2026-10-11): at a floor of ONE the rule reports 341 series, 142 of them a single
// work, and a 25-line sample of those 142 was about half a second spelling of the
// other series ("Edge Of Glass" / "The Edge of Glass") and half one book that
// really is the first volume of two series ("Princes of Petras" and "Irresistible
// Russian Tycoons" both opening on it) - evidence too thin to count as a class. A
// floor of TWO reports 199 (80 of them two-work series), nearly all of them one
// series twice: a recording variant ("Harry Potter (Narrated by Stephen Fry)" in
// "harry-potter", "Women's Murder Club (abridged)", "Mistborn [Dramatized
// Adaptation]"), a respelling ("The Hunger Games" / "hunger-games") or an edition
// series beside its translated-title twin ("Los Juegos del Hambre (The Hunger
// Games)"), with a minority of sub-series numbered like their parent.
const duplicateSeriesMinShared = 2

// duplicateSeriesMarker is the duplicate-series class's marker.
const duplicateSeriesMarker = "a duplicate series (a recording variant or a second spelling)"

// checkDuplicateSeries reports a series every one of whose works another series lists
// at the SAME position - a subset of, or equal to, that series - as the series it
// duplicates. It is reported against the SUBSET, naming the supersets; two series
// holding exactly the same list are reported once, against the one that is not the
// canonical spelling (seriesDuplicateSide).
//
// Pairs that are LINKED are not duplicates, whatever they list: a translation and its
// original (translation_of either way) and two members of one ordering family
// (model.Series.OrderingPrimary - a chronological order may legitimately list exactly
// its primary's works at the same numbers).
//
// Positions compare as SLOTS: equal strings, or the same span under the schema's
// position grammar (model.ParsePositionRange), so "02" and "2" are one place and "2",
// "2.5" and "1-3.5" stay three - internal/importer's SameSlot over a tree that already
// satisfies the schema's position pattern (pkg/check cannot import the importer,
// which imports it).
//
// Cost is one index of every membership by work, then, per series of at least
// duplicateSeriesMinShared members, only the series listing its FIRST member at the
// same slot - never every pair of series.
func checkDuplicateSeries(cat *model.Catalog, idx *pathIndex, warn addFunc) {
	type membership struct {
		series *model.Series
		pos    string
	}
	byWork := map[string][]membership{}
	posIn := make(map[*model.Series]map[string]string, len(cat.Series))
	for _, s := range cat.Series {
		m := make(map[string]string, len(s.Works))
		for _, sw := range s.Works {
			if _, dup := m[sw.Work]; dup {
				continue
			}
			m[sw.Work] = sw.Position
			byWork[sw.Work] = append(byWork[sw.Work], membership{s, sw.Position})
		}
		posIn[s] = m
	}
	contains := func(sup, sub *model.Series) bool {
		in := posIn[sup]
		for w, p := range posIn[sub] {
			q, ok := in[w]
			if !ok || !sameSlot(p, q) {
				return false
			}
		}
		return true
	}
	for _, s := range cat.Series {
		mine := posIn[s]
		if len(mine) < duplicateSeriesMinShared {
			continue
		}
		first := s.Works[0]
		var supersets []string
		for _, m := range byWork[first.Work] {
			t := m.series
			if t == s || t.ID == s.ID || !sameSlot(m.pos, mine[first.Work]) || linkedSeries(s, t) {
				continue
			}
			if len(posIn[t]) < len(mine) || !contains(t, s) {
				continue
			}
			if len(posIn[t]) == len(mine) && !seriesDuplicateSide(s, t) {
				continue // the same list: reported once, against the other side
			}
			supersets = append(supersets, t.ID)
		}
		if len(supersets) == 0 {
			continue
		}
		sort.Strings(supersets)
		supersets = dedupSorted(supersets)
		warn(idx.series[s], "series %q (%q) lists %d works, every one at the same position in %s: %s",
			s.ID, s.Name, len(mine), quotedList(supersets), duplicateSeriesMarker)
	}
}

// sameSlot reports whether two stored positions name one slot (see
// checkDuplicateSeries).
func sameSlot(a, b string) bool {
	if a == b {
		return true
	}
	alo, ahi, aok := model.ParsePositionRange(a)
	blo, bhi, bok := model.ParsePositionRange(b)
	return aok && bok && alo == blo && ahi == bhi
}

// linkedSeries reports whether two series are a modelled pair rather than a
// duplicate: a translation link either way, or one ordering family.
func linkedSeries(a, b *model.Series) bool {
	for _, t := range a.TranslationOf {
		if t == b.ID {
			return true
		}
	}
	for _, t := range b.TranslationOf {
		if t == a.ID {
			return true
		}
	}
	return a.OrderingPrimary() == b.OrderingPrimary()
}

// seriesDuplicateSide decides which of two series holding the SAME list is the
// duplicate to report: the one whose name carries a recording qualifier when only one
// does, else the longer id (a numbered "-2" or a decorated spelling is longer than the
// slug it was minted beside), else the later id. It reports whether a is that side.
func seriesDuplicateSide(a, b *model.Series) bool {
	qa, qb := titlerule.IsRecordingQualifiedSeriesName(a.Name), titlerule.IsRecordingQualifiedSeriesName(b.Name)
	if qa != qb {
		return qa
	}
	if len(a.ID) != len(b.ID) {
		return len(a.ID) > len(b.ID)
	}
	return strings.Compare(a.ID, b.ID) > 0
}

// dedupSorted drops adjacent repeats from a sorted list (a series listed twice by
// one id cannot be named twice).
func dedupSorted(ids []string) []string {
	out := ids[:0]
	for i, id := range ids {
		if i == 0 || id != ids[i-1] {
			out = append(out, id)
		}
	}
	return out
}
