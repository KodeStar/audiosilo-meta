package audit

import (
	"fmt"
	"sort"

	"github.com/kodestar/audiosilo-meta/internal/importer"
)

// vetoruntime.go holds the two vetoes that were found by MEASURING what the repair pass
// would actually retire, rather than by reading the proposals: 7 of the 1,844 clusters
// (0.38%, 14 public slugs) were sets of DISTINCT VOLUMES whose titles had lost their
// numbers, and the existing ladder cleared every rung.
//
// The archetype is Bravelands: six Erin Hunter volumes stored as `bravelands-book-1` ..
// `bravelands-book-6`, each titled the bare series name, none of them modeled in a series
// (so the position veto has nothing to read), none of them a collection, and a longest
// runtime ratio of 1.47 - just under the 1.5x rung. Both vetoes below catch it, and each
// catches it for a different reason, which is why both are here: one reads the RECORDINGS
// (the same narrator cannot have read one book at two lengths), the other reads the SLUGS
// (the id kept the volume number the title lost).

// vetoUnexplainedRuntimeGap: two members carry recordings by the SAME narrator set whose
// runtimes are more than 10% apart, and nothing in the data explains the gap.
//
// The rule of record for "could these be one production" is the importer's ASIN-merge
// guard, `importer.RuntimesCompatible`, and it is asked here rather than restated. What
// this veto adds is the ONE legitimate explanation for a same-narrator gap: an
// ABRIDGEMENT beside its unabridged twin is one work with two productions (the Mageling
// shape - the importer even seeds the flag from the title's edition marker), so a cluster
// whose two sides DISAGREE about `abridged` keeps its merge. A gap with no such
// explanation is evidence of two different books, because one narrator reading one book
// twice at different lengths is not a thing that happens - a second narrator reading it is.
//
// NOTE on the tri-state: `model.Recording.Abridged` is a plain bool, so the loaded
// catalogue cannot tell "stated false" from "unstated" (the schema's absent flag means
// unknown - see the data model). The veto therefore reads a DIFFERENCE between the two
// sides as the explanation, which is exactly "one of them is an abridgement and the other
// is not" as far as any reader of the catalogue can tell; two sides that agree (both
// false, i.e. both unabridged-or-unknown, or both true) explain nothing.
func vetoUnexplainedRuntimeGap(members []dupMember) (string, bool) {
	type rec struct {
		work      string
		narrators string
		runtime   int
		abridged  bool
	}
	var recs []rec
	for _, m := range members {
		for _, r := range m.work.Recordings {
			if r.RuntimeMin <= 0 {
				continue // an unknown runtime contradicts nothing
			}
			recs = append(recs, rec{
				work: m.work.ID, narrators: narratorKey(r.Narrators),
				runtime: r.RuntimeMin, abridged: r.Abridged,
			})
		}
	}
	for i := range recs {
		for j := i + 1; j < len(recs); j++ {
			a, b := recs[i], recs[j]
			if a.work == b.work {
				continue // two productions of ONE record are not what a merge pairs
			}
			if a.narrators == "" || a.narrators != b.narrators {
				continue
			}
			if importer.RuntimesCompatible(a.runtime, b.runtime) {
				continue
			}
			if a.abridged != b.abridged {
				continue // an abridgement beside its unabridged twin explains the gap
			}
			return fmt.Sprintf("%s and %s are read by the same narrator(s) at %d and %d minutes, more than 10%% apart, "+
				"and neither states an abridgement that would explain it: one narrator does not read one book at two lengths",
				a.work, b.work, a.runtime, b.runtime), true
		}
	}
	return "", false
}

// narratorKey is a recording's narrator SET as one comparable string, through the
// importer's own set rules so "the same narrators" means here what it means there.
func narratorKey(narrators []string) string {
	if len(narrators) == 0 {
		return ""
	}
	set := importer.ToSet(narrators)
	out := make([]string, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	key := ""
	for _, n := range out {
		key += n + "\x00"
	}
	return key
}

// vetoSlugOrdinal: the members' SLUGS differ only by a trailing volume ordinal, so the id
// still carries the evidence the title lost.
//
// A slug is minted from the title, so `bravelands-book-1` and `bravelands-book-2` are two
// records whose titles ONCE said "Book 1" and "Book 2" - the numbers have since been
// cleaned out of the titles (or were never stored there), leaving a cluster that looks
// like one book under six ids. The real shapes measured over the 279k-work tree:
// `dane-maddock-origins-omnibus-1/2/3`, `french-saunders-titting-about-series-4/5/6`,
// `savarkar-vol-1-part-1/2`, `hancocks-half-hour-series-5/6` and Bravelands itself.
//
// It is deliberately narrow, so it stays high-precision for exactly that shape:
//
//   - BOTH tails must be non-empty. `hammered` against `hammered-book-3` is the
//     CALIBRATION pair - a retailer's decorated second record of one book - and its
//     shorter side has no tail at all, so it keeps its merge.
//   - both tails must be made of ordinal material only (a number, or a volume-marker
//     word), and each must carry a NUMBER. `1984` against `1984-george-orwell` has an
//     author suffix, not an ordinal.
//   - a volume-MARKER word must be present, either as the last shared segment or inside a
//     tail. That is what keeps the importer's bare numeric collision suffix (`x` vs `x-2`,
//     which says "two different works met on one slug") out of this veto's reach.
//
// WHY THE TABLE BELOW IS ITS OWN. The title-side vocabulary is titlerule's
// (StatedVolume/SameStatedVolume, which statedVolumes now consumes), and this rule reads
// SLUGS: a slug is `model.Slugify`'s output, so it is lowercase ASCII segments with no
// punctuation and no roman numerals to speak of, and the words below are the segment forms
// that survive that fold. Sharing titlerule's regexp vocabulary here would mean matching
// title syntax against a string that has none. What the two must agree on is the CONCEPT -
// which words name a division of a product - and any word added there and not here costs a
// missed veto (a cluster stays mechanical), never a wrong one.
func vetoSlugOrdinal(members []dupMember) (string, bool) {
	for i := range members {
		for j := i + 1; j < len(members); j++ {
			a, b := members[i].work.ID, members[j].work.ID
			if ok := ordinalSiblingSlugs(a, b); ok {
				return fmt.Sprintf("the slugs %s and %s differ only by a trailing volume ordinal, so the ids still carry the "+
					"volume evidence the titles lost: these are distinct volumes, not one book recorded twice", a, b), true
			}
		}
	}
	return "", false
}

// ordinalSiblingSlugs reports whether two slugs differ only by a trailing volume ordinal.
func ordinalSiblingSlugs(a, b string) bool {
	if a == b {
		return false
	}
	as, bs := slugSegments(a), slugSegments(b)
	p := 0
	for p < len(as) && p < len(bs) && as[p] == bs[p] {
		p++
	}
	ta, tb := as[p:], bs[p:]
	if len(ta) == 0 || len(tb) == 0 {
		return false
	}
	if !ordinalTail(ta) || !ordinalTail(tb) {
		return false
	}
	if !hasNumberSegment(ta) || !hasNumberSegment(tb) {
		return false
	}
	// The numbers must DIFFER, or the two slugs name one volume in two spellings rather
	// than two volumes: `the-beasts-of-tarzan-tarzan-series-3` against
	// `...-tarzan-series-book-3` is one book whose ids disagree about how to write "Book 3",
	// and `blue-at-the-mizzen-aubrey-maturin-book-20` against `...-series-book-20` is
	// another. Both were vetoed by the first draft of this rule and both are exactly the
	// duplicate a repair should fold.
	if sameNumbers(ta, tb) {
		return false
	}
	// The marker evidence: the shared segment the tails hang off, or a marker inside one.
	if p > 0 && volumeMarkerSegment(as[p-1]) {
		return true
	}
	return hasMarkerSegment(ta) || hasMarkerSegment(tb)
}

func slugSegments(slug string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(slug); i++ {
		if i == len(slug) || slug[i] == '-' {
			if i > start {
				out = append(out, slug[start:i])
			}
			start = i + 1
		}
	}
	return out
}

// ordinalTail reports whether every segment is ordinal material: a number or a
// volume-marker word.
func ordinalTail(segs []string) bool {
	for _, s := range segs {
		if !numberSegment(s) && !volumeMarkerSegment(s) {
			return false
		}
	}
	return true
}

// sameNumbers reports whether two tails state the same ordinal numbers in the same order,
// which is what tells "Book 3 spelled two ways" from "Book 3 beside Book 4".
func sameNumbers(a, b []string) bool {
	na, nb := numberSegments(a), numberSegments(b)
	if len(na) != len(nb) {
		return false
	}
	for i := range na {
		if na[i] != nb[i] {
			return false
		}
	}
	return true
}

// numberSegments is the numeric segments of a tail, in order, with leading zeros trimmed so
// "03" and "3" are one ordinal.
func numberSegments(segs []string) []string {
	var out []string
	for _, s := range segs {
		if numberSegment(s) {
			out = append(out, trimLeadingZeros(s))
		}
	}
	return out
}

func trimLeadingZeros(s string) string {
	i := 0
	for i < len(s)-1 && s[i] == '0' {
		i++
	}
	return s[i:]
}

func hasNumberSegment(segs []string) bool {
	for _, s := range segs {
		if numberSegment(s) {
			return true
		}
	}
	return false
}

func hasMarkerSegment(segs []string) bool {
	for _, s := range segs {
		if volumeMarkerSegment(s) {
			return true
		}
	}
	return false
}

// numberSegment reports whether a slug segment is a plain (possibly multi-digit) number.
// Slugs are ASCII by construction (model.Slugify folds everything else away).
func numberSegment(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// volumeMarkers are the words a slug spells a DIVISION of a product with. It is this
// veto's own narrow table - see the TODO on vetoSlugOrdinal - and every entry is a form
// that appears in the measured shapes or is its immediate plural/translation sibling.
var volumeMarkers = map[string]bool{
	"book": true, "books": true, "vol": true, "vols": true, "volume": true, "volumes": true,
	"part": true, "parts": true, "series": true, "season": true, "seasons": true,
	"episode": true, "episodes": true, "omnibus": true, "tome": true, "tomes": true,
	"band": true, "buch": true, "teil": true, "libro": true, "livre": true, "deel": true,
}

func volumeMarkerSegment(s string) bool { return volumeMarkers[s] }
