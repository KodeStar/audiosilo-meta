package check

import (
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/kodestar/audiosilo-meta/internal/titlerule"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// seriesdesignation.go is the SERIES-DESIGNATION duplicate census: two works by the
// same authors in one language whose titles are one book's title, one of them
// wrapped in a designation that names the series it belongs to - "The Ghost
// Brigades" beside "The Ghost Brigades: Old Man's War Book 2", "The Lightning Thief"
// beside "Percy Jackson and the Lightning Thief", "The Bourne Sanction" beside
// "Robert Ludlum's The Bourne Sanction", "Angels and Demons" beside "Angels &
// Demons".
//
// The normalized-identity census (checkNormalizedDuplicateWorks) cannot see them,
// and by design: its key strips a series name only where the work is LISTED in that
// series, and the decorated twin is almost always the unplaced one - a retailer row
// that never met the series it names. So the identity key of "The Skinner:
// Spatterjay 1" keeps "spatterjay" while "The Skinner" sits at Spatterjay 1, and the
// two keys differ. Teaching the identity key to strip unlisted series names would
// widen what the importer's create guard and the intake gate REFUSE, which is a
// writer change with its own evidence; this class only COUNTS, so it can afford a
// reading the writers must not take.
//
// The LibraForge benchmark (2026-11-04) is where the class surfaced: a matched book's
// runner-up at score 90+ was, outside the cross-language editions the matcher's
// language factor answers, this shape, and it holds a confident match below the
// lead the server's gate asks for.
//
// What a DESIGNATION is (designationKind), the leftover once the shorter title is
// found inside the longer one at a separator or word boundary:
//
//   - a series name with a volume number, or a bare volume number ("Old Man's War
//     Book 2", "Spatterjay 1", "Book 3", "The Dark Tower II"), where a NAMED series
//     must be one either work is listed in (its whole name, or a word-prefix of at
//     least two words) at that number, and an unnamed or unlisted one must state
//     the very position the PLAIN work holds; a number word counts only after a
//     volume word ("Book Two", never "Take Two");
//   - a series name alone, but only one either work is listed in ("Dune: ...",
//     "Percy Jackson and the ...", never a bare "and" joining two books);
//   - the author's possessive ("Robert Ludlum's");
//   - a genre tag ("A Novel", "A Slow Living LitRPG", "A James Bond Adventure").
//
// Anything else - a real subtitle, "and Other Stories", "The Fitzgerald
// Translation" - is not a designation, so the pair is not this class.
//
// And what keeps a pair OUT, beyond the shape: an incompatible language or a
// translation link (a translation is another work), author lists that do not nest
// (oneBookReason, the identity-equal census's own test), a series both works are
// listed in at different positions (two volumes), a stated number contradicting the
// position the plain work holds ("Backyard Starship: Origins 2" beside Backyard
// Starship 1), a collection beside a single book, runtimes no two recordings of
// which come within a factor of two, the slug-tail volume rule (differentVolumes),
// and a reviewed rejection. A pair the identity key already joins is the
// normalized-duplicate class's finding and a same-slug pair the identity-equal
// class's, so the three classes never count one pair twice.
//
// ADVISORY, as every duplicate census is: the fix is a merge (internal/repair, an
// `assert` in internal/audit/reviewed.json), and only a reviewer can say a reissue
// is one book rather than a revised edition.

// seriesDesignationMarker is the class's marker (advisoryMarkers).
const seriesDesignationMarker = "a series designation around one book's title"

// sdWork is one work as the census reads it, every derivation computed once.
type sdWork struct {
	w *model.Work
	// segs are the title's separator-delimited segments as folded tokens.
	segs [][]string
	// fold is the whole title's comparison form: its tokens, leading article dropped.
	fold string
	// members are the work's series memberships.
	members []sdMembership
	// runtimes are the recordings' stated runtimes, minutes.
	runtimes []int
	// slug is asked only of the few works a candidate pair reaches, so it is
	// memoized on first use rather than derived for every work.
	slug      string
	slugKnown bool
}

func (s *sdWork) titleSlug() string {
	if !s.slugKnown {
		s.slug, s.slugKnown = model.SlugifyWhole(s.w.Title), true
	}
	return s.slug
}

type sdMembership struct {
	series   string
	name     []string
	position string
}

// sdSeparators split a title into segments: a subtitle colon, a spaced dash, a
// bracketed group. A comma is deliberately not one ("Life, the Universe and
// Everything").
var sdSeparators = strings.NewReplacer(":", "|", ";", "|", " - ", "|", " \u2013 ", "|", " \u2014 ", "|",
	"(", "|", ")", "|", "[", "|", "]", "|")

// sdTokens folds text into comparison tokens: the slug's words, with "&" read as
// "and" (Slugify drops it, which is what keeps "Angels & Demons" off its twin).
func sdTokens(s string) []string {
	slug := model.SlugifyWhole(strings.ReplaceAll(s, "&", " and "))
	if slug == "" {
		return nil
	}
	return strings.Split(slug, "-")
}

func dropArticle(toks []string) []string {
	if len(toks) > 1 && (toks[0] == "the" || toks[0] == "a" || toks[0] == "an") {
		return toks[1:]
	}
	return toks
}

func sdFold(toks []string) string { return strings.Join(dropArticle(toks), "-") }

func newSDWork(w *model.Work, members []sdMembership) *sdWork {
	sw := &sdWork{w: w, members: members}
	for _, seg := range strings.Split(sdSeparators.Replace(w.Title), "|") {
		if t := sdTokens(seg); len(t) > 0 {
			sw.segs = append(sw.segs, t)
		}
	}
	sw.fold = sdFold(slices.Concat(sw.segs...))
	for _, r := range w.Recordings {
		if r.RuntimeMin > 0 {
			sw.runtimes = append(sw.runtimes, r.RuntimeMin)
		}
	}
	return sw
}

// checkSeriesDesignationDuplicateWorks reports each pair once, against the
// decorated (longer) work.
func checkSeriesDesignationDuplicateWorks(cat *model.Catalog, ix *WorkIdentity, idx *pathIndex, reviewed *reviewedPairs, warn addFunc) {
	for _, p := range seriesDesignationPairs(cat, ix, reviewed) {
		warn(idx.work[p.decorated], "work %q and %q are one title by the same authors, the second spelled with %q (%s): "+
			seriesDesignationMarker, p.plain.ID, p.decorated.ID, p.designation, p.kind)
	}
}

// seriesDesignationPair is one finding of the class: the plain work, the decorated
// one, the designation the decorated title adds and what kind of designation it
// is.
type seriesDesignationPair struct {
	plain, decorated *model.Work
	designation      string
	kind             string
}

func seriesDesignationPairs(cat *model.Catalog, ix *WorkIdentity, reviewed *reviewedPairs) []seriesDesignationPair {
	members := map[string][]sdMembership{}
	prefixes := map[string]bool{} // every series name's leading word runs, folded
	names := map[string]bool{}    // every series' whole name, folded
	for _, s := range cat.Series {
		name := dropArticle(sdTokens(s.Name))
		if len(name) == 0 {
			continue
		}
		for k := 1; k <= len(name); k++ {
			prefixes[strings.Join(name[:k], "-")] = true
		}
		names[strings.Join(name, "-")] = true
		for _, sw := range s.Works {
			members[sw.Work] = append(members[sw.Work], sdMembership{series: s.ID, name: name, position: sw.Position})
		}
	}
	works := make([]*sdWork, 0, len(cat.Works))
	byFold := map[string][]*sdWork{}
	for _, w := range cat.Works {
		sw := newSDWork(w, members[w.ID])
		works = append(works, sw)
		if sw.fold != "" {
			byFold[sw.fold] = append(byFold[sw.fold], sw)
		}
	}
	seen := map[[2]string]bool{}
	var out []seriesDesignationPair
	consider := func(plain, dec *sdWork, extra [][]string) {
		if plain.w.ID == dec.w.ID {
			return
		}
		key := [2]string{plain.w.ID, dec.w.ID}
		if key[1] < key[0] {
			key[0], key[1] = key[1], key[0]
		}
		if seen[key] {
			return
		}
		kind, ok := designationKind(plain, dec, extra)
		if !ok || !sdPairQualifies(plain, dec, ix) {
			return
		}
		// Settled once it qualifies, withheld or not: a pair several readings reach
		// ("Percy Jackson and" / "Percy Jackson and the") is one suppression, not one
		// per reading.
		seen[key] = true
		if reviewed.withheld(model.RedirectWorks, plain.w.ID, dec.w.ID) {
			return
		}
		out = append(out, seriesDesignationPair{plain: plain.w, decorated: dec.w, designation: joinDesignation(extra), kind: kind})
	}
	for _, dec := range works {
		if len(dec.segs) == 0 {
			continue
		}
		// The same title spelled two ways ("&" for "and"): no designation at all. A
		// same-slug pair is the identity-equal class's, which sdPairQualifies vetoes.
		for _, plain := range byFold[dec.fold] {
			if plain.w.ID < dec.w.ID {
				consider(plain, dec, nil)
			}
		}
		n := len(dec.segs)
		// Segment level: the plain title is a contiguous run of whole segments, the
		// rest designations.
		for i := 0; i < n; i++ {
			for j := i; j < n; j++ {
				if i == 0 && j == n-1 {
					continue
				}
				extra := append(append([][]string{}, dec.segs[:i]...), dec.segs[j+1:]...)
				for _, plain := range byFold[sdFold(slices.Concat(dec.segs[i:j+1]...))] {
					consider(plain, dec, extra)
				}
			}
		}
		// Word level, inside the first segment: a leading series name or author
		// possessive with no separator ("Percy Jackson and the Lightning Thief",
		// "Robert Ludlum's The Bourne Sanction"), the remainder standing alone or
		// followed by the title's other segments as designations.
		seg0 := dec.segs[0]
		// A first segment that IS a series' whole name ("A Street Cat Named Bob", "My
		// Daughter's Boyfriend") is that series' own book: a lead cutting inside the
		// name leaves only its tail ("Bob", "Boyfriend"), which is no book's title.
		// The title's other segments may still be one ("The Adventures of Sherlock
		// Holmes: The Boscombe Valley Mystery").
		seriesTitled := names[sdFold(seg0)]
		for k := 1; k < len(seg0); k++ {
			lead := seg0[:k]
			// "Percy Jackson and the Lightning Thief" joins a series name to its
			// book by "and the"; a bare "and" joins two books ("Alice in Wonderland
			// and Through the Looking Glass").
			if lead[k-1] == "and" && seg0[k] != "the" {
				continue
			}
			if !prefixes[strings.Join(trimConnective(lead), "-")] && !possessiveOfAuthor(lead, dec.w) {
				continue
			}
			rest := seg0[k:]
			if !seriesTitled || n > 1 {
				whole := slices.Concat(append([][]string{rest}, dec.segs[1:]...)...)
				for _, plain := range byFold[sdFold(whole)] {
					consider(plain, dec, [][]string{lead})
				}
			}
			if n > 1 && !seriesTitled {
				extra := append([][]string{lead}, dec.segs[1:]...)
				for _, plain := range byFold[sdFold(rest)] {
					consider(plain, dec, extra)
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].decorated.ID != out[j].decorated.ID {
			return out[i].decorated.ID < out[j].decorated.ID
		}
		return out[i].plain.ID < out[j].plain.ID
	})
	return out
}

func joinDesignation(extra [][]string) string {
	parts := make([]string, 0, len(extra))
	for _, e := range extra {
		parts = append(parts, strings.Join(e, " "))
	}
	return strings.Join(parts, " / ")
}

// trimConnective drops the trailing "and" (or "and the") a leading series name is
// joined to its book's title by: "Percy Jackson and" the Lightning Thief.
func trimConnective(toks []string) []string {
	for len(toks) > 1 && (toks[len(toks)-1] == "and" || toks[len(toks)-1] == "the") {
		toks = toks[:len(toks)-1]
	}
	return dropArticle(toks)
}

// possessiveOfAuthor reports whether toks is "<an author's name>'s", which the slug
// fold spells with the s fused onto the surname ("robert ludlums").
func possessiveOfAuthor(toks []string, w *model.Work) bool {
	if len(toks) == 0 {
		return false
	}
	last := toks[len(toks)-1]
	if !strings.HasSuffix(last, "s") || len(last) < 3 {
		return false
	}
	want := strings.Join(append(append([]string{}, toks[:len(toks)-1]...), strings.TrimSuffix(last, "s")), "-")
	return slices.Contains(w.Authors, want)
}

// designationKind reads the leftover segments of a decorated title against the
// plain one. Every segment must be a designation; the kind names the strongest
// evidence among them. A nil extra is the "&"-for-"and" spelling.
func designationKind(plain, dec *sdWork, extra [][]string) (string, bool) {
	if len(extra) == 0 {
		return "same title, other spelling", true
	}
	kind := ""
	for _, seg := range extra {
		k, ok := segmentDesignation(seg, plain, dec)
		if !ok {
			return "", false
		}
		if kind == "" || designationRank[k] > designationRank[kind] {
			kind = k
		}
	}
	return kind, true
}

const (
	sdKindSeriesPosition = "a listed series at its position"
	sdKindPosition       = "the position the plain work holds"
	sdKindSeries         = "a listed series"
	sdKindPossessive     = "the author's possessive"
	sdKindGenre          = "a genre tag"
)

var designationRank = map[string]int{
	sdKindGenre: 1, sdKindPossessive: 2, sdKindSeries: 3, sdKindPosition: 4, sdKindSeriesPosition: 5,
}

// sdVolumeWords may stand between a series name and its number.
var sdVolumeWords = map[string]bool{"book": true, "books": true, "volume": true, "vol": true, "part": true,
	"tome": true, "no": true, "number": true, "bk": true, "series": true}

// sdGenreHeads end a genre-tag subtitle ("A Slow Living LitRPG", "A Novel").
var sdGenreHeads = map[string]bool{"novel": true, "novella": true, "litrpg": true, "gamelit": true,
	"adventure": true, "thriller": true, "mystery": true, "romance": true, "fantasy": true}

// sdNumberWords and the roman numerals are the volume spellings a designation ends
// in besides digits.
var sdNumberWords = map[string]int{"one": 1, "two": 2, "three": 3, "four": 4, "five": 5, "six": 6,
	"seven": 7, "eight": 8, "nine": 9, "ten": 10}

var sdRoman = map[string]int{"i": 1, "ii": 2, "iii": 3, "iv": 4, "v": 5, "vi": 6, "vii": 7, "viii": 8,
	"ix": 9, "x": 10, "xi": 11, "xii": 12}

func sdNumber(tok string) (int, bool) {
	if n, err := strconv.Atoi(tok); err == nil && n > 0 {
		return n, true
	}
	if n, ok := sdNumberWords[tok]; ok {
		return n, true
	}
	n, ok := sdRoman[tok]
	return n, ok
}

func segmentDesignation(seg []string, plain, dec *sdWork) (string, bool) {
	if len(seg) == 0 {
		return "", false
	}
	if len(seg) >= 2 && (seg[0] == "a" || seg[0] == "an") && sdGenreHeads[seg[len(seg)-1]] {
		return sdKindGenre, true
	}
	if possessiveOfAuthor(seg, dec.w) || possessiveOfAuthor(seg, plain.w) {
		return sdKindPossessive, true
	}
	name, last := seg, seg[len(seg)-1]
	num, hasNum := sdNumber(last)
	worded := false
	if hasNum {
		name = seg[:len(seg)-1]
		for len(name) > 0 && sdVolumeWords[name[len(name)-1]] {
			// "series" names what follows it in "Corps Justice Series, Book 9", but
			// it is no volume word before a number word ("Series One").
			worded = worded || name[len(name)-1] != "series"
			name = name[:len(name)-1]
		}
		// A number WORD is a volume only after a volume word ("Book Two"): bare,
		// it is a name's own word ("Take Two", "Series One").
		if sdNumberWords[last] > 0 && !worded {
			name, hasNum = seg, false
		}
	}
	name = trimConnective(name)
	if len(name) == 1 && name[0] == "the" {
		name = nil
	}
	listed := listedSeriesNamed(name, plain, dec)
	switch {
	case hasNum && listed != nil:
		for _, m := range listed {
			if positionIs(m.position, num) {
				return sdKindSeriesPosition, true
			}
		}
		return "", false // a listed series at another position: another volume
	case hasNum:
		// No listed series by that name (or none named). A one-letter numeral bare
		// there is a word ("Me and I", "Malcolm X"), as a bare number word is.
		if _, roman := sdRoman[last]; roman && len(last) == 1 && !worded {
			return "", false
		}
		// Otherwise the number must be a position the PLAIN work holds: the
		// decorated one restating its own position says nothing about the other
		// ("The Directors: Take Two" at The Directors 2).
		for _, m := range plain.members {
			if positionIs(m.position, num) {
				return sdKindPosition, true
			}
		}
		return "", false
	case listed != nil:
		return sdKindSeries, true
	}
	return "", false
}

// listedSeriesNamed returns the memberships of either work whose series name the
// designation's name is (a word-prefix of, at least the first word): "Old Mans War"
// names "Old Man's War", "Percy Jackson" names "Percy Jackson and the Olympians".
func listedSeriesNamed(name []string, works ...*sdWork) []sdMembership {
	if len(name) == 0 {
		return nil
	}
	var out []sdMembership
	for _, w := range works {
		for _, m := range w.members {
			// The whole name, or a word-prefix of at least two words: one word
			// ("Gothic") prefixes too many series names to name one.
			if (len(name) == len(m.name) || len(name) >= 2 && len(name) < len(m.name)) &&
				slices.Equal(m.name[:len(name)], name) {
				out = append(out, m)
			}
		}
	}
	return out
}

func positionIs(pos string, n int) bool {
	lo, hi, ok := model.ParsePositionRange(pos)
	return ok && lo == hi && lo == float64(n)
}

// sdPairQualifies is every veto the shape does not already answer.
func sdPairQualifies(plain, dec *sdWork, ix *WorkIdentity) bool {
	a, b := plain.w, dec.w
	// A same-slug pair is the identity-equal class's finding.
	if plain.titleSlug() == dec.titleSlug() {
		return false
	}
	if !languagesCompatible(a.Language, b.Language) || translationLinked(a, b) {
		return false
	}
	if oneBookReason(a, b) == "" || differentVolumes(a, b) {
		return false
	}
	// A collection beside a single book, by the one collection rule
	// (titlerule.SameCollectionStatus): each title read against its series, and an
	// unplaced twin against the plain work's, the series its designation names - so
	// the collection word of a series' own name ("Out of the Silent Planet: Ransom
	// Trilogy, Book 1") is not read as a box set.
	aSeries, bSeries := ix.SeriesNameOf(a.ID), ix.SeriesNameOf(b.ID)
	if bSeries == "" {
		bSeries = aSeries
	}
	if !titlerule.SameCollectionStatus(a.Title, aSeries, b.Title, bSeries) {
		return false
	}
	// Two volumes of one series.
	for _, ma := range plain.members {
		for _, mb := range dec.members {
			if ma.series == mb.series && ma.position != mb.position {
				return false
			}
		}
	}
	// The normalized-duplicate class's finding.
	if ka := ix.Key(a.Title, ix.SeriesNameOf(a.ID)); ka != "" && ka == ix.Key(b.Title, ix.SeriesNameOf(b.ID)) {
		return false
	}
	return runtimesComparable(plain.runtimes, dec.runtimes)
}

func translationLinked(a, b *model.Work) bool {
	return slices.Contains(a.TranslationOf, b.ID) || slices.Contains(b.TranslationOf, a.ID)
}

// runtimesComparable reports whether some recording of each comes within a factor
// of two of the other, or either states no runtime at all.
func runtimesComparable(a, b []int) bool {
	if len(a) == 0 || len(b) == 0 {
		return true
	}
	for _, x := range a {
		for _, y := range b {
			if x <= 2*y && y <= 2*x {
				return true
			}
		}
	}
	return false
}
