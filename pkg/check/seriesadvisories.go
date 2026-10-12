package check

import (
	"slices"

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
// by internal/importer's CleanSeriesName and its qualifier index); these count what
// the tree already holds, in every metacheck run, so the repair is a number and a
// regrowth is visible the wave after it happens.
//
// Both are WARNINGS and must stay so. Measured over the 282,710-work tree at
// origin/main (2026-10-11): 189 series carry a recording qualifier and 197 are a
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
// floor of TWO reports 199 (80 of them two-work series) - 197 once a plain series
// inside a recording variant is no longer read as the duplicate (checkDuplicateSeries;
// the three it took were "Bodenstein & Kirchhoff" beside its "(gekürzt)" twin and two
// like it) - nearly all of them one series twice: a recording variant ("Harry Potter (Narrated by Stephen Fry)" in
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
// A recording-qualified series is never named as the series a PLAIN one duplicates,
// whatever the two hold: "Bodenstein & Kirchhoff" is not a duplicate of the
// "(gekürzt)" variant that happens to list more of its volumes - the variant is the
// recording spelled as a series (series-recording-qualifier names it), and reading the
// containment the other way would point a repair at retiring the plain slug.
//
// Pairs that are LINKED are not duplicates, whatever they list: a translation and its
// original (translation_of either way) and two members of one ordering family
// (model.Series.OrderingPrimary - a chronological order may legitimately list exactly
// its primary's works at the same numbers).
//
// A pair a reviewed decision recorded as two series (reviewed - a `merge-series`
// rejection) is not a duplicate either: the superset drops off the subset's list, and
// a subset left with none is not reported.
//
// Positions compare as SLOTS (model.SameSlot, the one slot rule internal/importer,
// internal/audit and internal/repair read too), so "02" and "2" are one place and "2",
// "2.5" and "1-3.5" stay three.
//
// Cost is one index of every membership by work, then, per series of at least
// duplicateSeriesMinShared members, only the series listing its FIRST member - never
// every pair of series. A series below the floor is not indexed at all: it can be
// neither the subset (it is under the floor) nor a superset (that holds at least as
// many works as a subset that is not).
func checkDuplicateSeries(cat *model.Catalog, idx *pathIndex, reviewed *reviewedPairs, warn addFunc) {
	type indexed struct {
		pos       map[string]string // work -> its first position
		qualified bool              // the name carries a recording qualifier
	}
	byWork := map[string][]*model.Series{}
	in := map[*model.Series]indexed{}
	for _, s := range cat.Series {
		if len(s.Works) < duplicateSeriesMinShared {
			continue
		}
		m := make(map[string]string, len(s.Works))
		for _, sw := range s.Works {
			if _, dup := m[sw.Work]; dup {
				continue
			}
			m[sw.Work] = sw.Position
			byWork[sw.Work] = append(byWork[sw.Work], s)
		}
		_, q := titlerule.StripSeriesRecordingQualifiers(s.Name)
		in[s] = indexed{pos: m, qualified: q}
	}
	contains := func(sup, sub map[string]string) bool {
		for w, p := range sub {
			q, ok := sup[w]
			if !ok || !model.SameSlot(p, q) {
				return false
			}
		}
		return true
	}
	for _, s := range cat.Series {
		me, ok := in[s]
		if !ok || len(me.pos) < duplicateSeriesMinShared {
			continue
		}
		first := s.Works[0].Work
		var supersets []string
		for _, t := range byWork[first] {
			them := in[t]
			if t.ID == s.ID || len(them.pos) < len(me.pos) || (them.qualified && !me.qualified) ||
				!model.SameSlot(them.pos[first], me.pos[first]) || linkedSeries(s, t) || !contains(them.pos, me.pos) {
				continue
			}
			if len(them.pos) == len(me.pos) && !seriesDuplicateSide(s, t, me.qualified, them.qualified) {
				continue // the same list: reported once, against the other side
			}
			if reviewed.withheld(model.RedirectSeries, s.ID, t.ID) {
				continue
			}
			supersets = append(supersets, t.ID)
		}
		if len(supersets) == 0 {
			continue
		}
		slices.Sort(supersets)
		supersets = slices.Compact(supersets) // a series listed twice by one id is named once
		warn(idx.series[s], "series %q (%q) lists %d works, every one at the same position in %s: %s",
			s.ID, s.Name, len(me.pos), quotedList(supersets), duplicateSeriesMarker)
	}
}

// linkedSeries reports whether two series are a modelled pair rather than a
// duplicate: a translation link either way, or one ordering family.
func linkedSeries(a, b *model.Series) bool {
	return slices.Contains(a.TranslationOf, b.ID) || slices.Contains(b.TranslationOf, a.ID) ||
		a.OrderingPrimary() == b.OrderingPrimary()
}

// seriesDuplicateSide decides which of two series holding the SAME list is the
// duplicate to report, and reports whether a is that side; qa and qb say whether each
// name carries a recording qualifier. A recording-qualified name is never the survivor
// over a plain one; between two names alike in that, the survivor is
// titlerule.SeriesRank's choice - the ladder internal/audit's SER-DUP and
// internal/repair name a merge's survivor by - so where the qualifier does not decide,
// this advisory never names the other side of a pair a repair would keep.
func seriesDuplicateSide(a, b *model.Series, qa, qb bool) bool {
	if qa != qb {
		return qa
	}
	return titlerule.SeriesRank{Works: len(b.Works), ID: b.ID}.Better(titlerule.SeriesRank{Works: len(a.Works), ID: a.ID})
}
