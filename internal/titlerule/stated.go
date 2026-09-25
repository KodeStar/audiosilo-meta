package titlerule

import (
	"regexp"
	"strconv"
	"strings"
)

// stated.go holds the ONE-SIDED STATEMENTS a title can make about which product it
// is - a later volume of a title that is itself the whole work, an edition ordinal, a
// young-readers adaptation, a TV-series tie-in - read by internal/audit's
// one-sided-statement merge vetoes (vetostated.go there).
//
// They exist because one-sided SILENCE is never a disagreement (VolumeStatement.Agrees:
// "Hammered" beside "Hammered: The Iron Druid Chronicles, Book 3" is the pair the
// duplicate gates exist to catch), so a title that states something its plain-titled
// sibling does not reached the mechanical merge path whenever nothing else vetoed it.
// Each predicate here is one specific STATED shape, not a general silence rule - the
// general rule was measured and declined (57 correct merges withheld; see
// internal/audit's vetoStatedVolumeElsewhere). They are refusal inputs: a true where
// false was right costs a missed merge, never a wrong one.

// VolumeHead is a title split at its FIRST volume marker: the text before it, the
// marker word, the number it states and the text after it.
type VolumeHead struct {
	Head   string  // the title before the marker, tidied ("Our Vietnam Wars")
	Marker string  // the marker word, lower-cased ("volume", "pt", "book")
	Volume float64 // the number the marker states
	Tail   string  // the title after the number, tidied ("" for a trailing marker)
}

// headDigits and headWords are the two spellings of a volume marker VolumeHeadOf reads:
// a DIGIT number after markerSeq's optional separator ("Pt.2", "Volume 2", "Book3"),
// and a WORD or ROMAN number after wordVolumeMarker's REQUIRED whitespace ("Book Two",
// "Part II" - the whitespace is what keeps the surname "Partone" from reading as "Part
// One"). The marker vocabulary is volumeMarkerWords, the one the identity strip and
// the volume statements share, with the marker word CAPTURED because the vetoes care
// which class it is.
var (
	headDigits = regexp.MustCompile(`(?i)\b(` + volumeMarkerWords + `)\s*\.?\s*(\d+(?:\.\d+)?)\b`)
	headWords  = regexp.MustCompile(`(?i)\b(` + volumeMarkerWords + `)\s+(` + volumeNumberWords + `|x{0,2}(?:` + romanCore + `))\b`)
)

// VolumeHeadOf splits a title at its earliest volume marker, and reports whether it
// has one whose number can be read. A composite word number ("Book One Hundred") reads
// as nothing, as it does for the volume statements.
func VolumeHeadOf(title string) (VolumeHead, bool) {
	best := []int(nil)
	var vol float64
	if m := headDigits.FindStringSubmatchIndex(title); m != nil {
		if v, err := strconv.ParseFloat(groupAt(title, m, 2), 64); err == nil {
			best, vol = m, v
		}
	}
	if m := headWords.FindStringSubmatchIndex(title); m != nil && (best == nil || m[0] < best[0]) {
		word := groupAt(title, m, 2)
		v, ok := wordValue(word)
		if !ok {
			v, ok = romanValue(strings.ToLower(word))
		}
		if ok && !compositeNumberTail.MatchString(title[m[1]:]) {
			best, vol = m, v
		}
	}
	if best == nil {
		return VolumeHead{}, false
	}
	return VolumeHead{
		Head:   tidyTitle(strings.TrimRight(title[:best[0]], " ([")),
		Marker: strings.ToLower(groupAt(title, best, 1)),
		Volume: vol,
		Tail:   tidyTitle(strings.Trim(title[best[1]:], " )]")),
	}, true
}

// partMarker is the PART-class subset of the marker vocabulary: the words that divide
// ONE WORK into pieces ("Our Vietnam Wars, Volume 2", "Medical Mysteries Across
// History, Pt.2") rather than numbering a book within a series. "Book N" is the
// retailer's series-position convention - "All In, Book 3" beside "All In" is one
// book, and 35 correct merges of that shape are what declined the general rule - so it
// is deliberately absent, as are the non-English markers (a German "Band 2" and a
// French "Tome 2" are series positions far more often than parts).
var partMarker = map[string]bool{"vol": true, "vols": true, "volume": true, "volumes": true, "part": true, "parts": true, "pt": true, "pts": true}

// IsPartMarker reports whether a VolumeHead's marker word is PART-class (see partMarker).
func IsPartMarker(marker string) bool { return partMarker[strings.ToLower(marker)] }

// ordinalWordList is the spelled-out edition ordinals a title states, by value (index
// + 1). The bound is generous for editions: a twentieth edition of a textbook exists,
// a thirtieth is not a shape any title in the tree carries.
var ordinalWordList = []string{
	"first", "second", "third", "fourth", "fifth", "sixth", "seventh", "eighth", "ninth", "tenth",
	"eleventh", "twelfth", "thirteenth", "fourteenth", "fifteenth", "sixteenth", "seventeenth",
	"eighteenth", "nineteenth", "twentieth",
}

var ordinalWords = func() map[string]int {
	m := make(map[string]int, len(ordinalWordList))
	for i, w := range ordinalWordList {
		m[w] = i + 1
	}
	return m
}()

// editionOrdinal matches "<ordinal> Edition", optionally with ONE revision word
// between ("Third Revised Edition"). "Anniversary" is deliberately not one of them:
// "Fifteen Dogs (Tenth Anniversary Edition)" is the same text re-released, where a
// "Seventh Edition" of Security Analysis is a revised text.
var editionOrdinal = regexp.MustCompile(`(?i)\b(\d{1,2})(?:st|nd|rd|th)\s+(?:(?:revised|updated|expanded)\s+)?(?:edition|ed)\b|\b(` +
	strings.Join(ordinalWordList, "|") + `)\s+(?:(?:revised|updated|expanded)\s+)?edition\b`)

// EditionOrdinal is the edition number a title states ("Security Analysis (Sixth
// Edition)" -> 6, "... 7th ed." -> 7), and whether it states one.
func EditionOrdinal(title string) (int, bool) {
	m := editionOrdinal.FindStringSubmatch(title)
	if m == nil {
		return 0, false
	}
	if m[1] != "" {
		n, err := strconv.Atoi(m[1])
		return n, err == nil && n > 0
	}
	n, ok := ordinalWords[strings.ToLower(m[2])]
	return n, ok
}

// youngReaders matches a title announcing an ADAPTATION of a book for a younger
// audience - "(Adapted for Young Adults)", "Young Readers Edition", "The Young Adult
// Adaptation", "Children's Edition". Such an edition is a different, shorter text
// (Notes from a Young Black Chef's is 388 minutes against the original's 457), not a
// second recording of the original. The bare audience phrases ("for Kids", "for
// Teens") are NOT here: they are how a children's book is subtitled, not how an
// adaptation of an adult one announces itself.
var youngReaders = regexp.MustCompile(`(?i)\badapted\s+for\s+(?:young\s+(?:readers?|adults?|people)|children|kids|teens|middle[- ]grade)\b` +
	`|\byoung\s+(?:readers?|adults?|people)(?:['’]s|s['’])?\s+(?:edition|adaptation|version)\b` +
	`|\b(?:children|kids)(?:['’]s|s['’])?\s+(?:edition|adaptation|version)\b` +
	`|\bmiddle[- ]grade\s+(?:edition|adaptation|version)\b`)

// IsYoungReadersAdaptation reports whether a title announces a young-readers
// adaptation (see youngReaders).
func IsYoungReadersAdaptation(title string) bool { return youngReaders.MatchString(title) }

// seriesTieIn matches a title whose whole trailing segment is "The Series" (or "The
// Animated/TV Series"): "Tangled: The Series" is the book of a television show, a
// different work from the film's "Tangled". A series NAME merely ending in "Series"
// ("Zak Bates Eco-Adventure Series, Book 2") has other words in its segment and does
// not match.
var seriesTieIn = regexp.MustCompile(`(?i)[:\-–—(,]\s*the\s+(?:animated\s+|tv\s+|television\s+)?series\s*\)?\s*$`)

// IsSeriesTieIn reports whether a title announces a television-series tie-in (see
// seriesTieIn).
func IsSeriesTieIn(title string) bool { return seriesTieIn.MatchString(title) }
