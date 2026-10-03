package audit

import (
	"strings"

	"github.com/kodestar/audiosilo-meta/internal/titlerule"
)

// seriestwin.go is SER-DUP's ORDERING TWIN: an orphan reading order with a twin under
// another franchise word.
//
// "The Jack Ryan Universe (publication order)" holds one membership, Oath of Office at
// 18 - which "A Jack Ryan Novel (publication order)" already holds at 18. Both state the
// publication order, and SER-DUP never groups them, because SeriesKey reads
// "jackryanuniverse" and "jackryan": the two names differ by the word a retailer hung on
// the franchise. titlerule.SeriesFranchiseKey peels that word too, and this detector
// proposes the fold - ALWAYS advisory, because the key is deliberately loose and
// "Universe" is part of a real name often enough that only a human can say the two are
// one franchise.
//
// The ORPHAN O states an ordering (the field, else its name's qualifier), is its own
// family's primary (no ordering_of) and no variant names it, and sits in no SER-DUP
// group of two or more (a grouped series is the normalized-name class's business). Its
// TWIN T states the same ordering, is not a variant, shares O's franchise key and a
// member-work author (samePersonSpelling), already holds every membership O holds at the
// same slot (foldMovesNothing - a twin retires a spelling, never adds to an order), and
// clears every pair veto. Exactly one twin, or no proposal.
//
// What must never fold is a SUB-SERIES onto its parent: Rincewind 1 is Discworld 1, so a
// Rincewind publication order moves nothing into Discworld's - which is exactly why the
// guard is not the slots but the NAME: O must state an ordering, the franchise keys must
// be equal (Rincewind and Discworld are not), and a name of the "<parent>: <sub-series>"
// shape whose head is the twin's own base is refused outright.
const serDupTwin = "ordering-twin"

// detectOrderingTwins adds the ordering-twin proposals to SER-DUP's findings.
func detectOrderingTwins(ix *index, keys []seriesKeys, f *findings) {
	grouped := map[string]int{}
	for _, k := range keys {
		grouped[k.tight]++
	}
	// Each name's franchise key, folded once: the 45k-series catalogue is walked twice.
	franchise := make(map[string]string, len(keys))
	for _, k := range keys {
		franchise[k.series.ID] = titlerule.SeriesFranchiseKey(k.series.Name)
	}
	byFranchise, _ := groupBy(keys, func(k seriesKeys) string { return franchise[k.series.ID] })
	for _, o := range keys {
		s := o.series
		ordering := statedOrdering(o)
		if ordering == "" || s.OrderingOf != "" || len(ix.variantsOf[s.ID]) > 0 || grouped[o.tight] > 1 {
			continue
		}
		bucket := byFranchise[franchise[s.ID]]
		if len(bucket) < 2 {
			continue
		}
		own := seriesSideOf(ix, s)
		var twins []seriesKeys
		for _, t := range bucket {
			if t.series.ID == s.ID || t.series.OrderingOf != "" || statedOrdering(t) != ordering || subSeriesOf(s.Name, t.series.Name) {
				continue
			}
			ts := seriesSideOf(ix, t.series)
			if !foldMovesNothing(ts, own) || !anySamePerson(ix, ts.authors, own.authors) {
				continue
			}
			if len(seriesMergeVetoesWith(ix, []seriesKeys{o, t}, t.series.ID, vetoOptions{restatedOrdering: t.series.Ordering})) > 0 {
				continue
			}
			twins = append(twins, t)
		}
		if len(twins) != 1 {
			continue
		}
		t := twins[0]
		f.add(Finding{
			Subclass: serDupTwin,
			Key:      s.ID,
			Series:   []SeriesRef{ix.seriesRef(t.series), ix.seriesRef(s)},
			Propose: Proposal{
				Op:       OpMergeSeries,
				Target:   t.series.ID,
				Others:   []string{s.ID},
				Advisory: true,
				Reason: "both state the " + ordering + " order of one franchise under two franchise words (" +
					`"` + s.Name + `" and "` + t.series.Name + `"), and ` + t.series.ID + " already holds every membership " +
					s.ID + " holds at the same slot: confirm the two names are one franchise",
			},
		})
	}
}

// subSeriesOf reports whether name has the "<parent>: <sub-series>" shape with the
// parent spelled as the other series' own base - "Discworld: Rincewind" beside
// "Discworld". Read over the qualifier bases, so an ordering group on either side is no
// difference.
func subSeriesOf(name, other string) bool {
	head, _, found := strings.Cut(titlerule.ReadSeriesQualifiers(name).Base, ":")
	return found && titlerule.SeriesKey(head) != "" &&
		titlerule.SeriesKey(head) == titlerule.SeriesKey(titlerule.ReadSeriesQualifiers(other).Base)
}
