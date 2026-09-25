package titlerule

import (
	"regexp"
	"strconv"
	"strings"
)

// stated.go holds the ONE-SIDED STATEMENTS a title can make about which product it
// is - a later volume of a title that is itself the whole work, an edition ordinal, a
// young-readers adaptation, a series edition - read by internal/audit's one-sided
// statement vetoes (vetostated.go there, which says why one-sided SILENCE is not among
// them). They are refusal inputs: a true where false was right costs a missed merge,
// never a wrong one.

// VolumeHead is a title split at the volume marker its volume is read from: the text
// before the marker, the marker word and the number it states.
type VolumeHead struct {
	Head   string  // the title before the marker, tidied ("Our Vietnam Wars")
	Marker string  // the marker word, lower-cased ("volume", "pt", "book", "#")
	Volume float64 // the number the marker states
}

// VolumeHeadOf splits a title at the marker its volume is READ from, and reports
// whether it has one. It is the same reading as the volume statements' marker tiers
// (VolumePolicy.volumeOf with no series name): a DIGIT marker first (markerSeq, BareSeq's
// rule), else the earliest READABLE roman or word marker (volumeRomanOrWord through
// earliestVolumeMatch, so "Vol.II" is read and an unreadable "Book One Hundred" is passed
// over for a later marker). The division tier is not read: a season or a level is not a
// volume of the title before it. A bare trailing number is no marker and has no head.
func VolumeHeadOf(title string) (VolumeHead, bool) {
	if m := markerSeq.FindStringSubmatchIndex(title); m != nil {
		if v, ok := readDigits(title, m); ok {
			return headAt(title, m, v), true
		}
	}
	if v, m := earliestVolumeMatch(title, volumeRomanOrWord); m != nil {
		return headAt(title, m, v), true
	}
	return VolumeHead{}, false
}

// headAt composes a VolumeHead from a marker match whose group 1 is the number: every
// arm VolumeHeadOf reads has that shape, so the marker word is the match's text before
// the number, less its separator.
func headAt(title string, m []int, v float64) VolumeHead {
	return VolumeHead{
		Head:   tidyTitle(strings.TrimRight(title[:m[0]], " ([")),
		Marker: strings.ToLower(strings.TrimRight(title[m[0]:m[2]], " .\t")),
		Volume: v,
	}
}

// partMarkers are the PART-class marker words: the ones that divide ONE WORK into pieces
// ("Our Vietnam Wars, Volume 2", "Medical Mysteries Across History, Pt.2") rather than
// numbering a book within a series. "Book N" is the retailer's series-position convention
// ("All In, Book 3" beside "All In" is one book), so it is deliberately absent, as are
// the non-English markers (a German "Band 2" or a French "Tome 2" is a series position far
// more often than a part).
var partMarkers = map[string]bool{"vol": true, "vols": true, "volume": true, "volumes": true, "part": true, "parts": true, "pt": true, "pts": true}

// IsPart reports whether the head's marker is PART-class (see partMarkers).
func (h VolumeHead) IsPart() bool { return partMarkers[h.Marker] }

// segmentSplit cuts a head into its title segments, at segmentPunct or a spaced dash.
var segmentSplit = regexp.MustCompile(`[` + segmentPunct + `]|\s[-–—]\s`)

// RestatesVolume reports that the head already states the marker's number as the LAST
// token of one of its segments: "Z-Burbia 2: Parkway To Hell, Volume 2" (or "Z-Burbia
// Two: ...") is volume 2 of Z-Burbia said twice, not the second part of a work called
// "Z-Burbia 2: Parkway To Hell". A number anywhere else is not a restatement - "2 States,
// Part 2" is part 2 of "2 States". Digits, number words and roman numerals of two or more
// letters are read ("I", "V" and "X" are too often words).
func (h VolumeHead) RestatesVolume() bool {
	for _, seg := range segmentSplit.Split(h.Head, -1) {
		words := strings.Fields(seg)
		if len(words) < 2 {
			continue
		}
		last := strings.ToLower(strings.Trim(words[len(words)-1], ".()[]"))
		v, err := strconv.ParseFloat(last, 64)
		ok := err == nil
		if !ok {
			v, ok = wordValue(last)
		}
		if !ok && len(last) >= 2 {
			v, ok = romanValue(last)
		}
		if ok && v == h.Volume {
			return true
		}
	}
	return false
}

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

// editionOrdinal matches "<ordinal> Edition" (or "ed."), in digits or in words,
// optionally with ONE revision word between ("Third Revised Edition"). "Anniversary" is
// deliberately not one of them: "Fifteen Dogs (Tenth Anniversary Edition)" is the same
// text re-released, where a "Seventh Edition" of Security Analysis is a revised text.
var editionOrdinal = regexp.MustCompile(`(?i)\b(?:(\d{1,2})(?:st|nd|rd|th)|(` + strings.Join(ordinalWordList, "|") +
	`))\s+(?:(?:revised|updated|expanded)\s+)?(?:edition|ed)\b`)

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
// Adaptation", "Children's Edition". Such an edition is a different, shorter text, not a
// second recording of the original. The bare audience phrases ("for Kids", "for Teens")
// are NOT here: they are how a children's book is subtitled, not how an adaptation of an
// adult one announces itself.
var youngReaders = regexp.MustCompile(`(?i)\badapted\s+for\s+(?:young\s+(?:readers?|adults?|people)|children|kids|teens|middle[- ]grade)\b` +
	`|\byoung\s+(?:readers?|adults?|people)(?:['’]s|s['’])?\s+(?:edition|adaptation|version)\b` +
	`|\b(?:children|kids)(?:['’]s|s['’])?\s+(?:edition|adaptation|version)\b` +
	`|\bmiddle[- ]grade\s+(?:edition|adaptation|version)\b`)

// IsYoungReadersAdaptation reports whether a title announces a young-readers
// adaptation (see youngReaders).
func IsYoungReadersAdaptation(title string) bool { return youngReaders.MatchString(title) }

// seriesEdition matches a title whose whole trailing segment is "The Series" (or "The
// Animated/TV Series"): "Tangled: The Series" is the book of a television show, a
// different work from the film's "Tangled". A series NAME merely ending in "Series"
// ("Zak Bates Eco-Adventure Series, Book 2") has other words in its segment and does
// not match.
var seriesEdition = regexp.MustCompile(`(?i)[:\-–—(,]\s*the\s+(?:animated\s+|tv\s+|television\s+)?series\s*\)?\s*$`)

// IsSeriesEdition reports whether a title announces itself as "<title>: The Series".
// Retailers spell a whole-series OMNIBUS that way too ("X - The Series, Books 1-3"), so a
// title that announces a collection is not read as one here - that shape is
// IsCollection's.
func IsSeriesEdition(title string) bool {
	return seriesEdition.MatchString(title) && !IsCollection(title)
}
