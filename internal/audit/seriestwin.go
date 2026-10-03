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
// Neither side may be touched by a translation_of link (ix.translationLinked), as in
// the family folds: a translation is a different series, never a spelling to retire.
//
// The ORPHAN O states an ordering (the field, else its name's qualifier), is its own
// family's primary (no ordering_of) and no variant names it, and sits in no SER-DUP
// group of two or more (a grouped series is the normalized-name class's business). Its
// TWIN T states the same ordering, is not a variant, shares O's franchise key and a
// member-work author (samePersonSpelling), already holds every membership O holds at the
// same slot (foldMovesNothing - a twin retires a spelling, never adds to an order), and
// clears every pair veto. Exactly one twin, or no proposal.
//
// Two orphans holding the SAME list are each other's twin, so the fold is proposed in
// one direction only, onto the spelling titlerule.SeriesRank prefers - two proposals
// each retiring the other's target would be a contradiction a reviewer can only half
// accept. And a twin a mechanical SER-DUP proposal already retires is no target: the
// fold is judged again, against the survivor, once that merge lands.
//
// What must never fold is a SUB-SERIES onto its parent: Rincewind 1 is Discworld 1, so a
// Rincewind publication order moves nothing into Discworld's - which is exactly why the
// guard is not the slots but the NAME: O must state an ordering, the franchise keys must
// be equal (Rincewind and Discworld are not), and a name of the "<parent>: <sub-series>"
// shape whose head is the twin's own base is refused outright.
const serDupTwin = "ordering-twin"

// detectOrderingTwins adds the ordering-twin proposals to SER-DUP's findings. The twin
// is a candidate generator over foldOnto, the family folds' own pair judgement, with
// the verdict forced advisory.
func detectOrderingTwins(ix *index, keys []seriesKeys, f *findings) {
	grouped := map[string]int{}
	for _, k := range keys {
		grouped[k.tight]++
	}
	retired := map[string]bool{}
	for _, fd := range f.rows {
		if fd.Propose.Op == OpMergeSeries && !fd.Propose.Advisory {
			for _, id := range fd.Propose.Others {
				retired[id] = true
			}
		}
	}
	// ONE pass over the catalogue keeps what either side of a twin must be - a series
	// stating an ordering that is no variant - so the franchise key, the sub-series head
	// and the stated ordering are computed for those few alone, once each.
	type ordered struct {
		k                seriesKeys
		ordering         string
		franchise        string
		headKey, baseKey string
	}
	var cands []ordered
	for _, k := range keys {
		if k.series.OrderingOf != "" {
			continue
		}
		o := statedOrdering(k.series)
		if o == "" {
			continue
		}
		base := titlerule.ReadSeriesQualifiers(k.series.Name).Base
		c := ordered{k: k, ordering: o, franchise: titlerule.SeriesFranchiseKey(k.series.Name), baseKey: titlerule.SeriesKey(base)}
		if head, _, found := strings.Cut(base, ":"); found {
			c.headKey = titlerule.SeriesKey(head)
		}
		cands = append(cands, c)
	}
	byFranchise, _ := groupBy(cands, func(c ordered) string { return c.franchise })
	for _, o := range cands {
		s := o.k.series
		if len(ix.variantsOf[s.ID]) > 0 || grouped[o.k.tight] > 1 || ix.translationLinked(s.ID) {
			continue
		}
		var twins []seriesKeys
		for _, t := range byFranchise[o.franchise] {
			// A "<parent>: <sub-series>" name headed by the twin's own base is its sub-series.
			id := t.k.series.ID
			if id != s.ID && !retired[id] && !ix.translationLinked(id) && t.ordering == o.ordering &&
				(o.headKey == "" || o.headKey != t.baseKey) {
				twins = append(twins, t.k)
			}
		}
		if len(twins) == 0 {
			continue
		}
		sides := map[string]seriesSide{s.ID: seriesSideOf(ix, s)}
		authored := twins[:0]
		for _, t := range twins {
			ts := seriesSideOf(ix, t.series)
			if anySamePerson(ix, ts.authors, sides[s.ID].authors) {
				sides[t.series.ID] = ts
				authored = append(authored, t)
			}
		}
		v := foldOnto(ix, o.k, authored, sides)
		if v.kind != foldSpelling {
			continue
		}
		t := v.target
		// The same list both ways: one direction, onto the better-ranked spelling.
		if foldMovesNothing(sides[s.ID], sides[t.series.ID]) &&
			(titlerule.SeriesRank{Works: len(s.Works), ID: s.ID}).Better(titlerule.SeriesRank{Works: len(t.series.Works), ID: t.series.ID}) {
			continue
		}
		f.add(Finding{
			Subclass: serDupTwin,
			Key:      s.ID,
			Series:   []SeriesRef{ix.seriesRef(t.series), ix.seriesRef(s)},
			Propose: Proposal{
				Op:       OpMergeSeries,
				Target:   t.series.ID,
				Others:   []string{s.ID},
				Advisory: true,
				Reason: "both state the " + o.ordering + " order of one franchise under two franchise words (" +
					`"` + s.Name + `" and "` + t.series.Name + `"), and ` + t.series.ID + " already holds every membership " +
					s.ID + " holds at the same slot: confirm the two names are one franchise",
			},
		})
	}
}
