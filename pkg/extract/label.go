package extract

import (
	"regexp"
	"strconv"
	"strings"
)

// Confidence records HOW a toc label yielded a chapter number, because the two
// answers need different treatment downstream.
//
// The distinction exists because a toc label is ambiguous in a way an embedded
// audio chapter marker is not. "Chapter IV. The Sign" states a chapter number;
// "I An Irate Neighbor" only looks like one - "I" is also an English word, and
// "MIX" is simultaneously a plausible chapter title and the Roman numeral 1009.
// Neither reading can be settled from one label. It can be settled from the WHOLE
// book: if every label in the run resolves to a gapless 1..N sequence, the loose
// reading was right. So this type lets the parser stay honest about what it knows
// and pushes the decision to Contiguous, which has the evidence.
type Confidence int

const (
	// NotAChapter: the label states no chapter number we are willing to read.
	NotAChapter Confidence = iota
	// Strict: the label explicitly names a chapter ("Chapter 7", "7. The Thing").
	// Safe to trust per-label.
	Strict
	// Loose: a plausible reading that only a whole-book contiguity check can
	// confirm ("I An Irate Neighbor", "1 I ACCIDENTALLY VAPORIZE").
	Loose
)

// maxRomanChapter bounds a Roman-numeral reading. Roman letters spell real English
// words, and several of them parse as large numerals - MIX is 1009, DID is 999,
// DIM is 1499 - so an unbounded reader turns a one-word chapter title into a
// chapter number. No real book has this many chapters (the longest in the
// validation corpus, Don Quixote, has 126), so anything past the cap is a word.
const maxRomanChapter = 200

// sep is the separator set that divides a chapter number from its title across the
// epub typesetting conventions seen in the wild: "7. Title", "7: Title",
// "7 - Title", "7 – Title", "7 — Title", "7 • Title", "7) Title".
const sep = `[:.)•·\x{2013}\x{2014}-]`

// numberWords are the number words an epub toc spells out, with their values.
// Composition is additive over a hyphen or space ("twenty-one" = 21), which covers
// every form actually used for chapter numbering; "one hundred and five" is not
// supported because no observed toc writes chapter numbers that way.
var numberWords = map[string]int{
	"one": 1, "two": 2, "three": 3, "four": 4, "five": 5, "six": 6, "seven": 7,
	"eight": 8, "nine": 9, "ten": 10, "eleven": 11, "twelve": 12, "thirteen": 13,
	"fourteen": 14, "fifteen": 15, "sixteen": 16, "seventeen": 17, "eighteen": 18,
	"nineteen": 19, "twenty": 20, "thirty": 30, "forty": 40, "fifty": 50,
	"sixty": 60, "seventy": 70, "eighty": 80, "ninety": 90, "hundred": 100,
}

var (
	// reStructural matches a heading that divides a book WITHOUT being a chapter.
	// Reading "Part Two" as chapter 2 would collide with the real chapter 2 and
	// silently shift every downstream spoiler position, so these are refused
	// outright rather than left to the contiguity check.
	reStructural = regexp.MustCompile(`(?i)^(part|book|volume|section|act|scene)\b`)

	// --- Strict readings: the label explicitly names a chapter. ---

	reBareNumber    = regexp.MustCompile(`(?i)^(?:chapter\s+)?(\d+)$`)
	reChapterNumber = regexp.MustCompile(`(?i)^chapter\s+(\d+)\b\s*` + sep + `?\s*(.*)$`)
	reNumberTitle   = regexp.MustCompile(`(?i)^(\d+)\s*` + sep + `\s*(.+)$`)
	reChapterRest   = regexp.MustCompile(`(?i)^chapter\s+(.*)$`)
	reChapterRoman  = regexp.MustCompile(`(?i)^chapter\s+([ivxlcdm]+)\b\s*` + sep + `?\s*(.*)$`)

	// --- Loose readings: plausible, but only contiguity can confirm them. ---

	// reRomanTitle captures the separator as its own group so titleFollowsNumeral
	// can tell "the numeral is explicitly punctuated" from "there happens to be a
	// full stop later in the sentence".
	reRomanTitle  = regexp.MustCompile(`(?i)^([ivxlcdm]+)\s*(` + sep + `?)\s+(\S.*)$`)
	reRomanAlone  = regexp.MustCompile(`(?i)^([ivxlcdm]+)\s*\.?\s*$`)
	reNumberSpace = regexp.MustCompile(`^(\d+)\s+(\S.*)$`)
	// reChapterSuffix catches a toc entry that leads with a teaser sentence and
	// ends with the heading, e.g. "He rode a black horse. CHAPTER III." - a real
	// shape in the Project Gutenberg Pride and Prejudice epub.
	reChapterSuffix = regexp.MustCompile(`(?i)\bchapter\s+([ivxlcdm]+|\d+)\s*\.?\s*$`)

	// reTitleTrim strips a leading separator run and the dot leaders some tocs pad
	// with ("Chapter One ......................").
	reTitleTrim = regexp.MustCompile(`^[\s:.)•·\x{2013}\x{2014}-]+|[\s.]+$`)
)

// ChapterFromLabel reads a logical chapter number out of a table-of-contents label.
//
// It returns the number, the chapter's own title (empty when the label carries
// none), and how much the reading can be trusted. A caller should take Strict
// readings at face value and accept Loose ones only when the book's labels as a
// whole form a contiguous run - see Contiguous.
//
// It is deliberately a parser, not a guesser: a label that states no number
// ("Prologue", "The Golden Age of Cannibalism") returns NotAChapter, and a
// structural divider ("Part Two") is refused outright, because reading it as a
// chapter number would collide with a real chapter and shift every spoiler
// position after it.
//
// The vocabulary is the union of the forms observed across a 34-book validation
// corpus. A near-twin lives in audiosilo-sidecars' internal/audio for embedded m4b
// chapter markers; the two are kept separate on purpose, since marker titles come
// from audiobook encoders and toc labels from typesetters, and each carries traps
// the other never sees.
func ChapterFromLabel(label string) (num int, title string, conf Confidence) {
	s := strings.TrimSpace(label)
	if s == "" || reStructural.MatchString(s) {
		return 0, "", NotAChapter
	}

	if m := reBareNumber.FindStringSubmatch(s); m != nil {
		if n, ok := atoiChapter(m[1]); ok {
			return n, "", Strict
		}
	}
	for _, re := range []*regexp.Regexp{reChapterNumber, reNumberTitle} {
		if m := re.FindStringSubmatch(s); m != nil {
			if n, ok := atoiChapter(m[1]); ok {
				return n, cleanTitle(m[2]), Strict
			}
		}
	}
	if m := reChapterRest.FindStringSubmatch(s); m != nil {
		if n, rest, ok := leadingNumberWords(m[1]); ok {
			return n, cleanTitle(rest), Strict
		}
	}
	if m := reChapterRoman.FindStringSubmatch(s); m != nil {
		if n, ok := romanToNumber(m[1]); ok {
			return n, cleanTitle(m[2]), Strict
		}
	}

	// The suffix reading is tried before the bare-Roman one: an explicit "CHAPTER
	// II" at the end of the label is a statement, whereas a leading "I" is only an
	// appearance, and some tocs carry both in one entry.
	if m := reChapterSuffix.FindStringSubmatch(s); m != nil {
		if n, ok := romanToNumber(m[1]); ok {
			return n, "", Loose
		}
		if n, ok := atoiChapter(m[1]); ok {
			return n, "", Loose
		}
	}
	if m := reRomanTitle.FindStringSubmatch(s); m != nil {
		if n, ok := romanToNumber(m[1]); ok && titleFollowsNumeral(m[2], m[3]) {
			return n, cleanTitle(m[3]), Loose
		}
	}
	if m := reRomanAlone.FindStringSubmatch(s); m != nil {
		if n, ok := romanToNumber(m[1]); ok {
			return n, "", Loose
		}
	}
	if m := reNumberSpace.FindStringSubmatch(s); m != nil {
		if n, ok := atoiChapter(m[1]); ok {
			return n, cleanTitle(m[2]), Loose
		}
	}
	return 0, "", NotAChapter
}

// titleFollowsNumeral guards the bare-Roman reading against ordinary English prose.
// "I", "V" and "MIX" are words as well as numerals, so "I hope Mr. Bingley will
// like it." would otherwise read as chapter 1 - a real entry in the Project
// Gutenberg Pride and Prejudice toc, where the entry is a teaser sentence.
//
// The discriminator is what follows: a chapter title is capitalized ("I An Irate
// Neighbor"), whereas a sentence continues in lower case ("I hope ..."). A label
// that punctuates the numeral explicitly ("I. Silver Blaze") needs no such
// evidence.
//
// separator must be the punctuation captured IMMEDIATELY after the numeral, not
// any punctuation found later in the label - "I hope Mr. Bingley will like it"
// carries a full stop in "Mr." that has nothing to do with the numeral.
func titleFollowsNumeral(separator, title string) bool {
	if separator != "" {
		return true
	}
	for _, r := range title {
		return r >= 'A' && r <= 'Z'
	}
	return false
}

// Contiguous reports whether nums form a gapless run starting at 0 or 1 with no
// repeats - the same test the audio pipeline applies to embedded chapter markers.
// It is what promotes a set of Loose readings into a trustworthy chapter universe:
// a handful of English words that happen to look like Roman numerals will not
// accidentally enumerate 1..N.
//
// Fewer than minContiguousRun numbers is never contiguous, because a two-element
// run is far too easy to hit by chance.
func Contiguous(nums []int) bool {
	if len(nums) < minContiguousRun {
		return false
	}
	seen := make(map[int]bool, len(nums))
	lo, hi := nums[0], nums[0]
	for _, n := range nums {
		if seen[n] {
			return false
		}
		seen[n] = true
		if n < lo {
			lo = n
		}
		if n > hi {
			hi = n
		}
	}
	if lo != 0 && lo != 1 {
		return false
	}
	return hi-lo == len(nums)-1
}

// minContiguousRun is the shortest run Contiguous will accept. Three is the floor
// at which a gapless sequence stops being plausible by chance.
const minContiguousRun = 3

// atoiChapter parses a decimal chapter number, rejecting values that cannot be a
// chapter. The regexes prove the digits; this proves they fit a sane range, so a
// year in a title ("1984 Was Different") cannot become chapter 1984.
func atoiChapter(s string) (int, bool) {
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 || n > maxDecimalChapter {
		return 0, false
	}
	return n, true
}

// maxDecimalChapter bounds a digit reading. Generous next to the Roman cap because
// digits are far less likely to be a word, but still short of a year.
const maxDecimalChapter = 999

// leadingNumberWords consumes the longest run of spelled-out number words at the
// start of rest ("Twenty-One: The Thing" -> 21, ": The Thing") and returns the
// remainder for the title.
//
// It consumes greedily rather than matching a fixed pattern because the separator
// between number and title is not reliable: "Chapter Twenty-One" hyphenates INSIDE
// the number, "Chapter Twenty One - The Thing" spaces it, and "Chapter One The
// Beginning" has no separator at all. Splitting on the first hyphen would read
// "Twenty-One" as 20, quietly duplicating chapter 20 and shifting every spoiler
// position after it - so the number words themselves define where the number ends.
func leadingNumberWords(rest string) (n int, remainder string, ok bool) {
	i, total, consumed := 0, 0, 0
	for i < len(rest) {
		// Skip the separator between number words (space or hyphen only - any
		// other separator ends the number).
		start := i
		for i < len(rest) && (rest[i] == ' ' || rest[i] == '-') {
			i++
		}
		if consumed > 0 && i == start {
			break // no separator: the previous word ended the number
		}
		wordStart := i
		for i < len(rest) && isASCIILetter(rest[i]) {
			i++
		}
		v, found := numberWords[strings.ToLower(rest[wordStart:i])]
		if !found {
			i = start
			break
		}
		total += v
		consumed++
	}
	if consumed == 0 || total <= 0 || total > maxDecimalChapter {
		return 0, "", false
	}
	return total, rest[i:], true
}

func isASCIILetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

// romanToNumber converts a Roman numeral, requiring CANONICAL form so that English
// words made of Roman letters are rejected: the value must render back to the same
// numeral. That refuses "IIII", "VV" and most word-shaped inputs, and the
// maxRomanChapter cap refuses the rest ("MIX" would otherwise be 1009).
func romanToNumber(s string) (int, bool) {
	s = strings.ToUpper(strings.TrimSpace(s))
	if s == "" {
		return 0, false
	}
	values := map[byte]int{'I': 1, 'V': 5, 'X': 10, 'L': 50, 'C': 100, 'D': 500, 'M': 1000}
	total, prev := 0, 0
	for i := len(s) - 1; i >= 0; i-- {
		v, ok := values[s[i]]
		if !ok {
			return 0, false
		}
		if v < prev {
			total -= v
		} else {
			total += v
			prev = v
		}
	}
	if total <= 0 || total > maxRomanChapter {
		return 0, false
	}
	if numberToRoman(total) != s {
		return 0, false
	}
	return total, true
}

// numberToRoman renders n in canonical Roman form, so romanToNumber can round-trip
// its input and reject non-canonical spellings.
func numberToRoman(n int) string {
	steps := []struct {
		v int
		s string
	}{
		{1000, "M"}, {900, "CM"}, {500, "D"}, {400, "CD"}, {100, "C"}, {90, "XC"},
		{50, "L"}, {40, "XL"}, {10, "X"}, {9, "IX"}, {5, "V"}, {4, "IV"}, {1, "I"},
	}
	var b strings.Builder
	for _, st := range steps {
		for n >= st.v {
			b.WriteString(st.s)
			n -= st.v
		}
	}
	return b.String()
}

// cleanTitle trims the separator run and dot leaders a toc pads a title with.
func cleanTitle(s string) string {
	return normalizeLabel(reTitleTrim.ReplaceAllString(strings.TrimSpace(s), ""))
}
