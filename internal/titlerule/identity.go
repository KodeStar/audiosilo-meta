package titlerule

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// identity.go holds the NORMALIZED WORK IDENTITY key: the one answer to "do these
// two records name the same book, as far as their titles can say".
//
// It is the rule three separate defences read, which is the whole reason it is one
// function here rather than a formula spelled at each of them:
//
//   - internal/issueform's intake gate, so a submitted title that normalizes onto a
//     catalogued work is a duplicate verdict naming that work instead of a new
//     record;
//   - internal/importer's create guard, so a bulk row whose identity a work already
//     holds is skipped and reported instead of minting a sibling;
//   - pkg/check's advisory census, so the collisions already in the tree are counted
//     in every metacheck run and a repair wave's progress is a number.
//
// The three would otherwise be three thresholds, and a defect class that one of
// them called a duplicate while another minted it is exactly how the 4,596
// near-duplicate clusters accumulated in the first place: the bulk importer's own
// identity (a title SLUG plus an author set) cannot see through a retailer's
// decoration, so "Hammered" and "Hammered: The Iron Druid Chronicles, Book 3"
// are two works to it and one book to a reader.

// IdentityTitleKey is a work title's normalized identity: the title cleaned of
// retailer decoration (Clean) and reduced to its comparison form (CompareKey), so
// case, spacing, punctuation, diacritics, a leading article, edition markers,
// volume markers, genre-subtitle fluff and the series name are all NOT identity.
//
// series is the series name the title is read against, or "" - see SeriesNameFor
// for which of a work's memberships that is.
//
// It returns "" - NO IDENTITY, matching nothing, ever - for a title whose cleaned
// residual does not name a book (CarriesIdentity: packaging vocabulary, a bare
// number, a volume marker and nothing else). That guard is the whole difference
// between a key and a hazard, and an empty-residual test is not enough:
//
//	Clean("Cars 2", "Cars")  == "2"        both reduce to the SAME key as
//	Clean("Hawk 2", "Hawk")  == "2"        each other and as 945 other works
//
// A one-word series name is stripped out of its own titles (which is right - it is
// how "Unintended Cultivator: Volume 9" reduces at all), and what survives is the
// volume number. Keyed on that, 199 purely numeric keys covered 3,287 real works in
// the tree, and a duplicate gate reading them would refuse an unrelated book for
// every sequel it saw. The same held for the packaging residuals ("the", "boxset",
// "omnibus"). CarriesIdentity already owns the question "does this name a book", so
// the fix is to ask it: a residual that names none is not an identity to compare.
//
// A consumer that WANTS those groups anyway keys them by something else in addition -
// internal/audit's W-DUP appends the series id, which is what keeps two records of
// one omnibus together without merging two different collections.
//
// It is deliberately COARSER than the importer's own work identity and must stay
// that way. The importer's identity decides where a record is STORED (its slug) and
// may never widen without moving records; this decides whether to REFUSE a new
// record, which is reversible - a refused row is re-importable, a wrong merge is
// not. So a collision here is a duplicate CANDIDATE, and every caller pairs it with
// the author-set, language, stated-volume and collection rules before it acts.
// A residual that IS ITS NUMBERS is the one exception - see numericIdentity.
//
// It returns NO IDENTITY for a residual that WELDS two function words together
// (hasWeldedFunctionWords, the interior arm of the retitle proposal's own
// RefuseFragment test): two joining words abutting, or one doubled, is the scar of a
// cleaning that cut words out of the MIDDLE of a title, and a key with that scar in it
// no longer describes one book. The measured pair is the two travel guides in Clean's
// header, whose excised residuals both read "The Best of for Short Stay Travel".
// Anchoring the strip is what stops that residual being produced at all; this is the
// second layer, judging what is left however it got there - and a title with no key is
// refused by no gate and merged by no wave, the same posture the CarriesIdentity
// refusal above has always had.
//
// It reads the WELD arm only, not the proposal's edge arms, and it is measured: see
// hasWeldedFunctionWords for why a joining word at an edge disqualifies a title and
// not a key.
func IdentityTitleKey(title, series string) string {
	cleaned := Clean(title, series)
	if !CarriesIdentity(cleaned) && !numericIdentity(cleaned) {
		return ""
	}
	if hasWeldedFunctionWords(identityWords(cleaned)) {
		return ""
	}
	return CompareKey(cleaned)
}

// numericIdentity reports whether a residual is nothing but numbers AND those numbers
// name a book: a year ("1984") or a date range ("Without a Trace: 1881-1968"), which
// CarriesIdentity calls identity-less because it discounts every pure number.
//
// It is not a new rule - numbersAreIdentity is the same test the dangling-tail peel
// uses to tell a year from a volume marker (two or more numbers, or a single number of
// four digits or more) - but it is deliberately bounded to a residual with NO WORDS in
// it. Applied to any identity-less residual it let the packaging back in: "Level 1
// Lessons 1-5" holds three numbers, so 36 Pimsleur courses in as many languages keyed
// alike on a residual that names no book at all. If words survived the clean, they are
// what the residual says, and CarriesIdentity has already judged them.
func numericIdentity(cleaned string) bool {
	words := identityWords(cleaned)
	if len(words) == 0 {
		return false
	}
	for _, w := range words {
		if !isAllDigits(w) {
			return false
		}
	}
	return numbersAreIdentity(cleaned)
}

// SeriesNameFor picks the series name a work's title is read against, out of the
// names of the series it belongs to: the one the title actually SPELLS OUT if there
// is one, else the first of the list.
//
// Both halves are load-bearing. A work in two series must clean the same way
// whoever asks (so the choice cannot depend on map order - the caller passes the
// names in a deterministic order, by series id), and a title that names one of its
// series should be cleaned against THAT one rather than against a sibling series
// whose name it does not carry.
//
// It takes NAMES rather than a catalogue on purpose: this package never reads a
// tree (see the package doc), and every caller already has the memberships in hand.
func SeriesNameFor(title string, names []string) string {
	if len(names) == 0 {
		return ""
	}
	lower := strings.ToLower(title)
	for _, name := range names {
		if _, ok := SeriesRefIn(lower, name); ok {
			return name
		}
	}
	return names[0]
}

// StatedVolume is the volume number a title spells out against a series name, and
// whether it states one at all - "does this title say which volume it is", the
// question that separates two records of one book from two volumes of one serial.
//
// It is BareSeq (markerSeq's book/vol/part/episode vocabulary, plus a residual that
// is nothing but a number) WIDENED by two forms markerSeq cannot see, both of them
// residuals the KEY throws away and therefore hazards if nothing states them:
//
//   - the DIVISION-class ordinals (ordinalVolume). wideGenreFluff drops a trailing
//     ": Season 2" or "- Level 3" as packaging, so "Foo: Season 1" and "Foo: Season
//     2" reduce to one key - and markerSeq knows none of those words, so the pair
//     read as two records of one book rather than as two seasons.
//   - a ROMAN volume number (romanVolume). wordVolumeMarker already recognizes
//     "Volume II" as a marker to STRIP, so the key loses it; without a number to
//     compare, "Volume I" and "Volume II" reduced to one identity too.
//
// The widening is layered HERE rather than in markerSeq/bareSeq, which are copied
// from audiosilo-server's pkg/match (see match.go's delta list): the copy stays
// re-diffable, and internal/audit's own volume-conflict measurement - taken with
// BareSeq - is left exactly as it was measured.
//
// The vocabulary can only make a duplicate gate MORE conservative, which is what
// makes a generous list safe: a title that states a volume is refused only when the
// catalogue positively places the matched work at that volume (the positive test in
// both writers), so a false positive here costs a missed duplicate refusal, never a
// wrong one.
func StatedVolume(title, series string) (float64, bool) {
	if v, ok := BareSeq(title, series); ok {
		return v, true
	}
	residual := stripSeries(title, series)
	if m := ordinalVolume.FindStringSubmatch(residual); m != nil {
		if v, err := strconv.ParseFloat(m[1], 64); err == nil {
			return v, true
		}
	}
	if m := romanVolume.FindStringSubmatch(residual); m != nil {
		if v, ok := romanValue(m[1]); ok {
			return v, true
		}
	}
	return 0, false
}

// ordinalVolume matches a DIVISION-class marker - a season, a level, a lesson, a
// unit, a numbered year - which names WHICH PART of a product this is exactly as
// "Book 3" does. Every word here is in wideGenreFluff, which is precisely the
// problem: the key drops them.
var ordinalVolume = regexp.MustCompile(`(?i)\b(?:seasons?|staffel|temporadas?|saisons?|series|levels?|lessons?|units?|jahr)\s*\.?\s*(\d+(?:\.\d+)?)\b`)

// romanVolume matches a volume marker whose number is a ROMAN numeral. The keyword
// list is wordVolumeMarker's (the rule that already strips this shape) and the
// numeral alternation is its too, so what the key removes is exactly what this can
// read back.
var romanVolume = regexp.MustCompile(`(?i)\b(?:books?|bks?|vols?|volumes?|parts?|pts?|episodes?|eps?|seasons?|levels?|b(?:a|ae|ä)nde?|teile?|tomes?|libros?)\s*\.?\s*(x{0,2}(?:ix|iv|vi{1,3}|i{1,3}|v|x))\b`)

// romanNumerals is the value of every numeral romanVolume can match. A table rather
// than a subtractive algorithm: the alternation admits twenty-odd forms and a table
// cannot disagree with it about any of them.
var romanNumerals = map[string]float64{
	"i": 1, "ii": 2, "iii": 3, "iv": 4, "v": 5, "vi": 6, "vii": 7, "viii": 8,
	"ix": 9, "x": 10, "xi": 11, "xii": 12, "xiii": 13, "xiv": 14, "xv": 15,
	"xvi": 16, "xvii": 17, "xviii": 18, "xix": 19, "xx": 20, "xxi": 21,
	"xxii": 22, "xxiii": 23, "xxiv": 24, "xxv": 25, "xxvi": 26, "xxvii": 27,
	"xxviii": 28, "xxix": 29, "xxx": 30,
}

func romanValue(s string) (float64, bool) {
	v, ok := romanNumerals[strings.ToLower(s)]
	return v, ok
}

// SameStatedVolume reports whether two titles state the SAME volume - or, either
// way, do not contradict each other - against their series names. A contradiction is
// the one piece of evidence that turns a normalized identity collision from a
// duplicate into a pair of siblings.
//
// A title stating nothing is not a disagreement: "Hammered" beside "Hammered: The
// Iron Druid Chronicles, Book 3" is precisely the duplicate the gates exist to
// catch, and only two titles that BOTH state a division, differently, are siblings.
//
// It compares two things, because one number is not always the whole statement:
//
//   - the volume each title states (StatedVolume), the primary number a gate also
//     uses for its positive test;
//   - the whole SEQUENCE of division markers (divisionSequence), for a title that
//     nests them. The measured population is the Pimsleur language courses: "Level 1
//     Lessons 1-5" and "Level 1 Lessons 6-10" are different products whose FIRST
//     stated number is identical, so a single-number comparison read 36 units of one
//     course as 36 records of one book. Comparing the sequence separates them, and
//     separates "Level 1 Lessons 1-5" from "Level 2 Lessons 1-5" too.
func SameStatedVolume(titleA, seriesA, titleB, seriesB string) bool {
	if a, okA := StatedVolume(titleA, seriesA); okA {
		if b, okB := StatedVolume(titleB, seriesB); okB && a != b {
			return false
		}
	}
	sa, sb := divisionSequence(titleA, seriesA), divisionSequence(titleB, seriesB)
	if len(sa) == 0 || len(sb) == 0 {
		return true
	}
	return slices.Equal(sa, sb)
}

// divisionMarker matches any keyword that names WHICH PART of a product a title is,
// with its number - markerSeq's vocabulary (pluralized) plus ordinalVolume's
// division words. It is the union on purpose: a title can nest two of them, and
// which family each keyword belongs to says nothing about whether the pair agrees.
var divisionMarker = regexp.MustCompile(`(?i)\b(?:books?|bks?|vols?|volumes?|parts?|pts?|episodes?|eps?|#|seasons?|staffel|temporadas?|saisons?|series|levels?|lessons?|units?|jahr)\s*\.?\s*(\d+(?:\.\d+)?)\b`)

// divisionSequence is every division number a title states, in the order it states
// them, with the series name removed first (so a digit in the series' own name is not
// read as a division).
func divisionSequence(title, series string) []float64 {
	ms := divisionMarker.FindAllStringSubmatch(stripSeries(title, series), -1)
	if len(ms) == 0 {
		return nil
	}
	out := make([]float64, 0, len(ms))
	for _, m := range ms {
		v, err := strconv.ParseFloat(m[1], 64)
		if err != nil {
			return nil // unparseable: state nothing rather than half a sequence
		}
		out = append(out, v)
	}
	return out
}
