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
// IT DELIBERATELY DOES NOT READ THE PROPOSAL'S FRAGMENT TEST
// (hasDanglingConnective), and both arms of that decision are measured over the
// 279k-work tree. A residual that reads as a fragment makes a poor TITLE to WRITE and
// a perfectly discriminating KEY:
//
//   - its EDGE arms (a leading joining word, a trailing stopword) cost 8 correct merge
//     proposals for no false one - "At the Mountains of Madness [Blackstone Edition]",
//     "By Royal Command", "E-Day [Dramatized Adaptation]", "All In, Book 3", each of
//     which collides with nothing but its own undecorated twin;
//   - its INTERIOR arm (two function words abutting - the scar a removal from the
//     MIDDLE of a title leaves, "The Best of for Short Stay Travel") was tried here and
//     withdrawn. It prevented ZERO wrong merges: the boundary anchoring in Clean is
//     what stops that residual being produced at all, and nothing else on the tree
//     produced one. What it cost was measurable in three places - one correct cluster
//     (the "(Dramatised)" twin of "The Spy Who Came in from the Cold"), three correct
//     retitles and one census group - and, worse, it silently emptied the key of ~198
//     works whose titles simply ABUT two function words: "To Have and to Hold", "In
//     Sickness and in Health", "For Better or for Worse", "Snowed in with the Tycoon",
//     "Murder in E Minor", eleven volumes of "Girls from da Hood". An empty key is read
//     by the intake gate and the importer's create guard as "no identity to collide
//     with", so those works lost their duplicate PREVENTION - the very defect class
//     this key exists to close, in the one direction a repair wave cannot undo.
func IdentityTitleKey(title, series string) string {
	cleaned := Clean(title, series)
	if !CarriesIdentity(cleaned) && !numericIdentity(cleaned) {
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

// SameTitleUnderCommonSeries reports whether two titles still reduce to ONE
// comparison key when the SAME series name is removed from both - the soundness
// condition on a key equality that was reached by removing a DIFFERENT name from
// each side.
//
// IdentityTitleKey is a one-sided function: it cleans one title against one series
// name. Two records meeting on its output is evidence they are one book only when
// the decoration each of them shed was the same decoration. When it was not, the
// key equality says nothing at all - what the two sides have in common is the part
// neither of them shed, and the parts that told them apart were each removed as
// somebody else's series name:
//
//	"Cold War: A History from Beginning to End"                against "Cold War"
//	"The Hundred Years War: A History from Beginning to End"   against "The Hundred Years War"
//	    both -> "A History from Beginning to End"
//
//	"Ladybird Audio Adventures: Outer Space"                   against "Outer Space"
//	"Ladybird Audio Adventures: The Frozen World"              against "The Frozen World"
//	    both -> "Ladybird Audio Adventures"
//
// Those are four different books and two proposed merges, and the shape reaches both
// title boundaries: the first pair sheds its LEADING segment (the subject of an
// Hourly History template) and the second its TRAILING one (the volume of a
// children's series). The boundary anchoring in Clean is what makes the strip a whole
// segment; it cannot say whether the segment should have come off at all, because the
// name being removed was resolved from the title itself (SeriesNameIn) or from a
// membership that only one side holds.
//
// The rule is therefore pairwise, and it is the WEAKEST test that separates them:
// read both titles against ONE name - either side's - and require the keys to still
// agree. A decorated title and its plain twin agree under the decorated side's name
// ("Hammered: The Iron Druid Chronicles, Book 3" and "Hammered" both reduce to
// "Hammered" against that series), which is the whole calibration this class was
// built on, while the pairs above agree under neither.
//
// It compares CompareKey(Clean(...)) rather than IdentityTitleKey, so a residual that
// carries no identity of its own is compared as the string it is: internal/audit keys
// those groups by their series as well ("La Guerra de los Cielos: Volumen 2" twice,
// where the residual is "Volumen 2"), and a rule reading IdentityTitleKey would see
// two empty keys and call a correct cluster a disagreement.
//
// Equal names are the short circuit and the reason this is not simply a second key:
// when both sides were read against the same name, one strip was applied to both and
// the caller's own key equality has already said everything this could.
//
// MEASURED over the 279k-work tree, against the 1,393 non-advisory merge-works
// proposals the audit made: 8 clusters fail it. Five are wrong merges - the two pairs
// above, "NPR American Chronicles: The Civil War" against "World War II", "Sherlock
// Holmes: Gods of War" against "The Devil's Dust", and "Edgar Allan Poe -
// Kurzbiografie kompakt" against "George Washington -" the same. Three are correct
// merges it withholds, all of them a series with TWO names: D.M. Cornish's trilogy is
// "Monster Blood Tattoo" in one market and "The Foundling's Tale" in another
// (Lamplighter and Factotum, one narrator, runtimes 963/964 and 1029/1029), and M.D.
// Massey's "THEM: Incursion" is also sold as "Incursion: Vampire Apocalypse (THEM
// Post-Apocalyptic Series, Book 2)". Those three stay in the report as advisory
// clusters for a human to merge by hand, which is the direction this project takes
// every time: a withheld merge is re-findable, a wrong one deletes a record.
//
// ITS ONE CALLER IS THE MERGE PATH, deliberately. The rule lives here, at the leaf
// beside the key it qualifies, so any consumer that needs it reads one sentence rather
// than a second spelling - but pkg/check's pairwise predicate does not ask it, because
// the same measurement in the census direction cost 58 groups to gain 3 and about half
// of what it removed was a real duplicate whose PREVENTION the two writer gates rest
// on (see the note on check.matches). A refusal is recoverable, a deletion is not, and
// this rule is priced for the side that deletes.
func SameTitleUnderCommonSeries(titleA, seriesA, titleB, seriesB string) bool {
	if seriesA == seriesB {
		return true
	}
	for _, s := range [2]string{seriesA, seriesB} {
		a, b := CompareKey(Clean(titleA, s)), CompareKey(Clean(titleB, s))
		if a != "" && a == b {
			return true
		}
	}
	return false
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
// is nothing but a number) WIDENED by three forms markerSeq cannot see, every one of
// them a residual the KEY throws away and therefore a hazard if nothing states it:
//
//   - the DIVISION-class ordinals (ordinalVolume). wideGenreFluff drops a trailing
//     ": Season 2" or "- Level 3" as packaging, so "Foo: Season 1" and "Foo: Season
//     2" reduce to one key - and markerSeq knows none of those words, so the pair
//     read as two records of one book rather than as two seasons.
//   - a ROMAN volume number (romanVolume). wordVolumeMarker already recognizes
//     "Volume II" as a marker to STRIP, so the key loses it; without a number to
//     compare, "Volume I" and "Volume II" reduced to one identity too.
//   - a WORD volume number, read back through wordVolumeMarker's own capture (the
//     rule that strips the shape). markerSeq requires a digit, so until this arm
//     existed a serial
//     numbering its volumes in words stated no volume at all - while the key lost
//     those words in the two places it takes a whole segment: inside a decorative
//     group ("Wildwood (Book One)" and "Wildwood (Book Two)" both reduce to
//     "Wildwood") and in the tail the series strip leaves dangling ("Hellmervick,
//     Book Two: The Black Forest" against that series reduces to "Hellmervick").
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
	if m := wordVolumeMarker.FindStringSubmatchIndex(residual); m != nil {
		if word := groupAt(residual, m, 1); word != "" && !compositeNumberTail.MatchString(residual[m[3]:]) {
			if v, ok := wordValue(word); ok {
				return v, true
			}
		}
	}
	return 0, false
}

// divisionWords is the DIVISION-class marker vocabulary - a season, a level, a
// lesson, a unit, a numbered year. One spelling shared by ordinalVolume (the
// primary-number probe) and divisionMarker (the sequence), so a word added to one
// cannot make StatedVolume and divisionSequence answer differently about one title.
const divisionWords = `seasons?|staffel|temporadas?|saisons?|series|levels?|lessons?|units?|jahr`

// ordinalVolume matches a DIVISION-class marker - a season, a level, a lesson, a
// unit, a numbered year - which names WHICH PART of a product this is exactly as
// "Book 3" does. Every word here is in wideGenreFluff, which is precisely the
// problem: the key drops them.
var ordinalVolume = regexp.MustCompile(`(?i)\b(?:` + divisionWords + `)\s*\.?\s*(\d+(?:\.\d+)?)\b`)

// romanVolume matches a volume marker whose number is a ROMAN numeral. The keyword
// list is wordVolumeMarker's (the rule that already strips this shape) plus the two
// division words measured with roman numbering, and the numeral alternation is its
// too, so what the key removes is exactly what this can read back.
var romanVolume = regexp.MustCompile(`(?i)\b(?:` + volumeMarkerWords + `|seasons?|levels?)\s*\.?\s*(x{0,2}(?:ix|iv|vi{1,3}|i{1,3}|v|x))\b`)

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

// volumeMarkerWords is wordVolumeMarker's marker half, named so the rule that STRIPS
// this shape (rules.go) and the rules that READ it back here cannot drift apart.
const volumeMarkerWords = `books?|bks?|vols?|volumes?|parts?|pts?|episodes?|eps?|b(?:a|ae|ä)nde?|teile?|tomes?|libros?`

// volumeNumberWordList is the ONE spelling of the word-number vocabulary: the
// alternation wordVolumeMarker strips and the values wordValue reads back are both
// derived from it, so a widening cannot reach one and miss the other - a word the
// strip knew and the read did not would lose the key a volume nothing could state.
// A word's value is its index + 1.
//
// The BOUND is wordVolumeMarker's own, and it is the right one rather than an
// arbitrary stopping point: a title saying "Book Thirteen" keeps those two words
// through Clean, so it still tells itself apart from its siblings and there is
// nothing here to read back. pkg/extract keeps its own, WIDER word-number table
// (composable, up to "hundred") for chapter labels; the two stay separate because
// reading through it here would state volumes the key never lost - and this package
// is a leaf.
var volumeNumberWordList = []string{
	"one", "two", "three", "four", "five", "six",
	"seven", "eight", "nine", "ten", "eleven", "twelve",
}

var (
	volumeNumberWords = strings.Join(volumeNumberWordList, "|")

	wordNumerals = func() map[string]float64 {
		m := make(map[string]float64, len(volumeNumberWordList))
		for i, w := range volumeNumberWordList {
			m[w] = float64(i + 1)
		}
		return m
	}()
)

func wordValue(s string) (float64, bool) {
	v, ok := wordNumerals[strings.ToLower(s)]
	return v, ok
}

// compositeNumberTail matches a scale word continuing a word number: in "Book One
// Hundred" the capture "One" is the first word of a larger number, not the volume,
// and reading it would state a volume the title does not - so both read arms refuse
// the capture instead. The STRIP is deliberately untouched (changing what the key
// drops is a measured change of its own); RE2 has no lookahead, so the refusal is a
// second probe of the text after the capture rather than part of the pattern.
var compositeNumberTail = regexp.MustCompile(`(?i)^\s+(?:hundred|thousand|million)\b`)

// groupAt returns capture group g's text, or "" when it did not participate.
func groupAt(s string, m []int, g int) string {
	if m[2*g] < 0 {
		return ""
	}
	return s[m[2*g] : m[2*g+1]]
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
//     nests them - word and digit spellings read into one vocabulary, so "Part One,
//     Episode 2" and "Part Two, Episode 3" compare as the nested statements they
//     are. NOTE the primary probe stays spelling-DEPENDENT for a nested title
//     (BareSeq reads the first DIGIT marker, so "Part One, Episode 2" has primary 2
//     where "Part 1, Episode 2" has primary 1) and it short-circuits first, so one
//     nested sequence spelled two ways still reads as a contradiction. That
//     asymmetry predates the word widening and is kept: relaxing it - equal
//     sequences overriding a primary disagreement - widens the duplicate gates,
//     which is a measured change of its own. The measured population is the
//     Pimsleur language courses: "Level 1
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
//
// The number is a DIGIT (group 1) or a WORD (group 2), one alternation rather than a
// second regex, because the sequence is ORDERED: "Part Two, Episode 3" states two
// divisions and a rule reading the two spellings separately could not say which came
// first. The digit arm is markerSeq's separator and the word arm is
// wordVolumeMarker's required whitespace - see that rule for why it is not cosmetic.
var divisionMarker = regexp.MustCompile(`(?i)\b(?:books?|bks?|vols?|volumes?|parts?|pts?|episodes?|eps?|#|` + divisionWords + `)` +
	`(?:\s*\.?\s*(\d+(?:\.\d+)?)|\s+(` + volumeNumberWords + `))\b`)

// divisionSequence is every division number a title states, in the order it states
// them, with the series name removed first (so a digit in the series' own name is not
// read as a division).
func divisionSequence(title, series string) []float64 {
	residual := stripSeries(title, series)
	ms := divisionMarker.FindAllStringSubmatchIndex(residual, -1)
	if len(ms) == 0 {
		return nil
	}
	out := make([]float64, 0, len(ms))
	for _, m := range ms {
		word := groupAt(residual, m, 2)
		if word != "" && compositeNumberTail.MatchString(residual[m[5]:]) {
			return nil // "Part One Hundred": a number this vocabulary cannot read
		}
		v, ok := divisionNumber(groupAt(residual, m, 1), word)
		if !ok {
			return nil // unreadable: state nothing rather than half a sequence
		}
		out = append(out, v)
	}
	return out
}

// divisionNumber reads whichever of divisionMarker's two number arms matched. Exactly
// one of them is ever non-empty, so an empty pair is a match with no number in it,
// which the regex cannot produce and which this refuses rather than reads as zero.
func divisionNumber(digits, word string) (float64, bool) {
	if digits != "" {
		v, err := strconv.ParseFloat(digits, 64)
		return v, err == nil
	}
	if word != "" {
		return wordValue(word)
	}
	return 0, false
}
