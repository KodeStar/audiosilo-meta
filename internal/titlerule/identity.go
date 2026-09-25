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
// It reads BareSeq's two forms (markerSeq's book/vol/part/episode vocabulary in
// digits, and a residual that is nothing but a number) WIDENED by three forms
// markerSeq cannot see, every one of them a residual the KEY throws away and
// therefore a hazard if nothing states it:
//
//   - the DIVISION-class markers (ordinalVolume, romanDivision), numbered in
//     digits, words or roman numerals. wideGenreFluff drops a trailing ": Season 2"
//     or "- Level 3" as packaging, so "Foo: Season 1" and "Foo: Season 2" reduce to
//     one key - and markerSeq knows none of those words, so the pair read as two
//     records of one book rather than as two seasons. The WORD spelling ("Wildwood
//     (Season One)", "Powder River - Season Four") was read by divisionSequence all
//     along and by this rule only since issue #2258: a volume-conflict cluster of
//     such titles stated no volume a report could name.
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
// A title can carry SEVERAL markers ("Season 1 - Ep. 3", "Book Two, Part V"), and
// WHICH one is the volume is decided in tiers, the first that reads anything
// answering:
//
//  1. BareSeq: a VOLUME marker (book/vol/part/episode) in DIGITS, the earliest
//     (markerSeq), else a residual that is nothing but a number. The two are one call
//     so the digit rule has one spelling (match.go's); answering the bare-number half
//     this early changes nothing, since a residual of nothing but digits carries no
//     keyword any later tier could read;
//  2. a VOLUME marker in ROMAN numerals or WORDS, the earliest of the two spellings;
//  3. a DIVISION marker (season/series/level/lesson/unit/year) in digits, words or
//     roman numerals, the earliest of the three.
//
// Inside a tier POSITION decides and the spelling never does (issue #2258: the roman
// and the word arm used to be tried one after the other, so "Book Two, Part V" read
// volume 5 and "Volume One, Part II" volume 2), and an arm's match it cannot read (a
// COMPOSITE word number, "Unit One Hundred") is skipped for that arm's next one, so it
// never hides a readable marker later in the title. ACROSS tiers the order is kept
// rather than flattened into "the earliest marker wins", because the recorded series
// positions say so. Measured over the 14,366 memberships whose title states a volume
// against its series name, earliest-overall agreed with the recorded position 57
// times more and 27 times LESS than main's arm order - every loss a nested serial
// whose volume is the INNER number ("Yesterday's Gone: Season 1 - Ep. 3" at 3, "The
// Barren Author: Series 1 - Episode 4" at 4, "Werewolves of Shade: Beautiful
// Immortals Series One, Book 6" at 6) - and putting the word/roman volume tier level
// with the digit one lost 3 more, each a leading subseries part ahead of the
// retailer's own trailing "(Series, Book N)" ("Ghosts: Adrian's March, Part Five
// (Adrian's Undead Diary, Book 13)" at 13).
//
// The tier order DOES change readings main made, for a title carrying a word or roman
// VOLUME marker and a DIVISION marker at once: "Book Two, Season 3" read 3 on main
// (the division ordinal was tried before the word arm) and reads 2 here. That no
// reading on the tree moved is a fact about TODAY's titles, not a property of the
// rule: over the 279,367 titles of 2026-09-25 the only reading main made that this one
// does not is "Epi-paleo Rx" (main read "Ep" + "i" as Episode 1; see romanNumeral),
// and no title carries a word or roman volume marker beside a division marker. A
// title that does will read differently from main, deliberately, and
// TestStatedVolumeTierOrder pins those cases, and the importer's
// TestTitleVolumeTierDecidesPlacement pins where such a row is placed.
//
// A division word numbered in WORDS is read only in MARKER POSITION (divisionMarkerAt):
// "Season", "Series", "Level", "Unit" and "Lesson" are ordinary title words, and "A
// Series Two-Step", "Level One Dropout" and "Level One God" state no volume. A stated
// volume is not only a caution for the writers: their POSITIVE test (a title stating a
// volume must be placed at it, silence is a veto) turns a reading into a CREATE, so a
// title word misread as a volume would mint a decorated duplicate as a sibling work.
// Even in marker position the reading is the least certain one this rule makes, so
// the writers do not read it at all (ClaimedVolume); only the checks that withhold a
// merge do. The digit spelling keeps main's reading everywhere; the sequence (divisionSequence)
// keeps reading the word spelling anywhere, as it did before - a title where the two
// differ is one whose primary is SILENT, and silence never contradicts.
//
// The widening is layered HERE rather than in markerSeq/bareSeq, which are copied
// from audiosilo-server's pkg/match (see match.go's delta list): the copy stays
// re-diffable, and internal/audit's own volume-conflict measurement - taken with
// BareSeq - is left exactly as it was measured.
//
// What a reading COSTS is asymmetric, and it is why the vocabulary may be generous
// about markers but must never read an ordinary title word. A title that states a
// volume is refused as a duplicate only when the catalogue positively places the
// matched work at that volume (the positive test in both writers), so a false reading
// never causes a wrong refusal - it causes a missed one: the row is created as a
// sibling work. That is recoverable (a repair wave merges it) where a wrong refusal of
// a real book is a silent loss, but it is not free, and it is the reason for the
// marker-position rule above.
func StatedVolume(title, series string) (float64, bool) {
	return statedVolume(title, series, divisionAnySpelling)
}

// ClaimedVolume is StatedVolume for the question a WRITER asks: does this title make
// a volume claim strong enough to unlock a CREATE? It is StatedVolume minus one
// reading - a division word numbered in WORDS ("Season One", "Level Three") - and
// answers exactly as main's rule did for every title carrying one.
//
// The two writers turn a stated volume into a create: their POSITIVE test refuses a
// row as a duplicate of a catalogued work only when the catalogue positively places
// that work at the stated volume, so a reading nothing confirms MINTS the row as a
// sibling work (importer.seriesClaim.places, issueform.titleContext.placesMatch, and
// the intake's series-volume gate and title-arbitrated placement, which read the same
// number). The word-numbered division reading arrived with issue #2258 and even in
// marker position it is the least certain reading the rule makes. Measured over the
// 279k-work tree, letting it unlock a create flipped the decision for a decorated
// second listing of 19 catalogued works from refuse to create - "Stranger Things:
// Season One", "Locked In: Season One", four "Demigods Academy" season omnibuses -
// each a duplicate main refused. So it is a statement for the checks that WITHHOLD
// a merge (SameStatedVolume, internal/audit) and never for the ones that decide to
// create: a reading withheld here costs at most a refusal of a row we might not
// hold, which is recoverable, where a create is a duplicate a repair wave must find.
func ClaimedVolume(title, series string) (float64, bool) {
	return statedVolume(title, series, divisionDigitOrRoman)
}

// statedVolume is the tier walk StatedVolume documents, with the DIVISION tier's
// arms supplied by the caller.
func statedVolume(title, series string, divisionArms []volumeArm) (float64, bool) {
	if v, ok := BareSeq(title, series); ok {
		return v, true
	}
	residual := stripSeries(title, series)
	if v, ok := earliestVolume(residual, volumeRomanOrWord); ok {
		return v, true
	}
	return earliestVolume(residual, divisionArms)
}

// VolumeStatement is everything a title says about which volume it is, against one
// series name: the volume StatedVolume reads and the whole division sequence. It is
// what SameStatedVolume compares, computed once per title so a caller comparing many
// pairs (internal/audit's clusters) does not re-run the rules per pair.
type VolumeStatement struct {
	Volume    float64   // StatedVolume's number, meaningful only when States
	States    bool      // whether StatedVolume reads one
	Divisions []float64 // divisionSequence: every division marker, in title order
}

// StatementOf is the VolumeStatement of a title read against a series name.
func StatementOf(title, series string) VolumeStatement {
	v, ok := StatedVolume(title, series)
	return VolumeStatement{Volume: v, States: ok, Divisions: divisionSequence(title, series)}
}

// Agrees is SameStatedVolume over two statements already derived.
func (a VolumeStatement) Agrees(b VolumeStatement) bool {
	if a.States && b.States && a.Volume != b.Volume {
		return false
	}
	if len(a.Divisions) == 0 || len(b.Divisions) == 0 {
		return true
	}
	return slices.Equal(a.Divisions, b.Divisions)
}

// divisionWords is the DIVISION-class marker vocabulary - a season, a level, a
// lesson, a unit, a numbered year. One spelling shared by ordinalVolume (the
// primary-number probe) and divisionMarker (the sequence), and both read the number
// after it through divisionNumberArms, so neither a word nor a number spelling added
// to one can make StatedVolume and divisionSequence read a DIVISION marker
// differently: a title whose markers are all division markers states
// divisionSequence's first element as its volume. Which marker is the volume of a
// title that ALSO carries a book/vol/part/episode marker is StatedVolume's tier
// order, which the sequence - a list of every marker - has no need of. (The two
// still differ at the edges of what each reads at all: StatedVolume alone reads a
// roman numeral and the non-English volume words' word numbers, divisionSequence
// alone the plural digit forms such as "Books 1-3" - each a statement the other is
// silent about, and silence is never a contradiction.)
const divisionWords = `seasons?|staffel|temporadas?|saisons?|series|levels?|lessons?|units?|jahr`

// divisionNumberArms is the number half of a division marker: a DIGIT (group 1,
// markerSeq's optional separator) or a WORD (group 2, wordVolumeMarker's REQUIRED
// whitespace - see that rule for why it is not cosmetic), then a word boundary. One
// alternation rather than two regexes, because divisionSequence is ORDERED and has
// to know which spelling came first.
var divisionNumberArms = `(?:\s*\.?\s*(\d+(?:\.\d+)?)|\s+(` + volumeNumberWords + `))\b`

// ordinalVolume matches a DIVISION-class marker - a season, a level, a lesson, a
// unit, a numbered year - numbered in digits or in words, which names WHICH PART of
// a product this is exactly as "Book 3" does. Every word here is in wideGenreFluff,
// which is precisely the problem: the key drops them. StatedVolume reads a WORD number
// off it only in marker position (readDivision); the sequence reads either anywhere.
var ordinalVolume = regexp.MustCompile(`(?i)\b(?:` + divisionWords + `)` + divisionNumberArms)

// romanNumeral is the numeral half of a ROMAN-numbered marker, with its separator:
// wordVolumeMarker's alternation widened to xxx, so what the key removes is exactly
// what the two roman rules below can read back. The separator is REQUIRED (whitespace,
// or a dot as in "Vol.II"): with markerSeq's optional one the leading \b let "Parti
// Animals" read as "Part i", volume 1 - the surname hazard wordVolumeMarker's required
// whitespace already guards against ("Partone").
const romanNumeral = `(?:\s+|\s*\.\s*)(x{0,2}(?:ix|iv|vi{1,3}|i{1,3}|v|x))\b`

// romanVolume matches a VOLUME marker whose number is a ROMAN numeral. The keyword
// list is wordVolumeMarker's (the rule that already strips this shape).
var romanVolume = regexp.MustCompile(`(?i)\b(?:` + volumeMarkerWords + `)` + romanNumeral)

// romanDivision is romanVolume for the two DIVISION words measured with roman
// numbering ("Season II", "Level III"). It is a rule of its own because the two
// families are different TIERS of StatedVolume.
var romanDivision = regexp.MustCompile(`(?i)\b(?:seasons?|levels?)` + romanNumeral)

// volumeArm is one spelling of a marker StatedVolume reads: the rule that finds it
// and how its number is read off one match (false: this match states nothing this
// arm can read).
type volumeArm struct {
	re   *regexp.Regexp
	read func(s string, m []int) (float64, bool)
}

// The two tiers of StatedVolume that read more than one spelling. Inside a tier
// the EARLIEST readable marker answers (earliestVolume), whichever arm reads it.
var (
	volumeRomanOrWord = []volumeArm{
		{romanVolume, readRoman},
		// A ROMAN capture of wordVolumeMarker misses wordValue - romanVolume reads
		// that very marker at the same start.
		{wordVolumeMarker, func(s string, m []int) (float64, bool) { return readWordNumber(s, m, 1) }},
	}
	divisionAnySpelling = []volumeArm{
		{ordinalVolume, readDivision},
		{romanDivision, readRoman},
	}
	// divisionDigitOrRoman is the division tier ClaimedVolume reads: main's two
	// spellings, the digits and the roman numeral.
	divisionDigitOrRoman = []volumeArm{
		{ordinalVolume, readDivisionDigits},
		{romanDivision, readRoman},
	}
)

// earliestVolume is the number of the earliest marker any of arms reads in s. Each
// arm contributes the first of its matches it can READ - an unreadable one (a
// composite word number, a division word out of marker position) is passed over for
// the arm's next match rather than ending the arm, so "Unit One Hundred and Level 4"
// still states 4.
func earliestVolume(s string, arms []volumeArm) (float64, bool) {
	at, vol := -1, 0.0
	for _, a := range arms {
		for _, m := range a.re.FindAllStringSubmatchIndex(s, -1) {
			if at >= 0 && m[0] >= at {
				break
			}
			if v, ok := a.read(s, m); ok {
				at, vol = m[0], v
				break
			}
		}
	}
	return vol, at >= 0
}

func readRoman(s string, m []int) (float64, bool) { return romanValue(groupAt(s, m, 1)) }

// readDivision reads an ordinalVolume match: its DIGITS wherever they stand, as main
// always did, and its WORD number only in marker position (divisionMarkerAt).
func readDivision(s string, m []int) (float64, bool) {
	if groupAt(s, m, 2) == "" {
		return readDivisionDigits(s, m)
	}
	if !divisionMarkerAt(s, m[0], m[1]) {
		return 0, false
	}
	return readWordNumber(s, m, 2)
}

// readDivisionDigits reads an ordinalVolume match's DIGITS, and nothing from its word arm.
func readDivisionDigits(s string, m []int) (float64, bool) {
	return divisionNumber(groupAt(s, m, 1), "")
}

// divisionMarkerAt reports whether s[start:end] - a division word and its WORD
// number - stands as a MARKER rather than as words of the title: it opens a title
// segment and closes one.
//
// It OPENS one when what precedes it ends in a segment separator (":", "(", "[", ",",
// ";", "/", or a dash with whitespace before it, " - ") or is nothing but the space a
// removed series name left (stripSeries replaces the name with a space, so "Junkers
// Season Two" against "Junkers" is " Season Two"). A title that simply BEGINS with
// the words - "Level One Dropout", "Series One Collection" - is the title, not a
// marker on it. It CLOSES one when what follows is nothing, or a separator (":", ")",
// "]", ",", ";", "/", or a whitespace-led "(", "[" or dash): "Season Four Complete",
// "Level One God" and "Series Two-Step" run on into the title and state nothing.
//
// The rule is deliberately narrower than the digit spelling's. "Season 2" can hardly
// be anything but a marker; "Series Two", "Level One" and "Unit One" are ordinary
// words, and a reading is not free (see StatedVolume: the writers' positive test turns
// a stated volume into a create).
func divisionMarkerAt(s string, start, end int) bool {
	before := s[:start]
	if tb := strings.TrimRight(before, " \t"); tb == "" {
		if before == "" {
			return false // the title begins with it
		}
	} else if !endsSegment(tb, len(before) > len(tb)) {
		return false
	}
	after := s[end:]
	ta := strings.TrimLeft(after, " \t")
	spaced := len(after) > len(ta)
	if ta == "" {
		return true
	}
	switch ta[0] {
	case ':', ')', ']', ',', ';', '/':
		return true
	case '(', '[', '-':
		return spaced // "Season One (Unabridged)", "Season One - The Brain Drain"
	}
	return spaced && (strings.HasPrefix(ta, "\u2013") || strings.HasPrefix(ta, "\u2014"))
}

// endsSegment reports whether text ending in tb (trailing whitespace trimmed; spaced
// says whether there was any) ends a title segment, so what follows opens one.
func endsSegment(tb string, spaced bool) bool {
	switch tb[len(tb)-1] {
	case ':', '(', '[', ',', ';', '/':
		return true
	case '-':
		return spaced && len(tb) > 1 && (tb[len(tb)-2] == ' ' || tb[len(tb)-2] == '\t')
	}
	return spaced && (strings.HasSuffix(tb, " \u2013") || strings.HasSuffix(tb, " \u2014"))
}

// readWordNumber reads capture group g as a word number, refusing one that is only
// the first word of a COMPOSITE number ("Book One Hundred") - reading it would state
// a volume the title does not.
func readWordNumber(s string, m []int, g int) (float64, bool) {
	word := groupAt(s, m, g)
	if word == "" || compositeNumberTail.MatchString(s[m[2*g+1]:]) {
		return 0, false
	}
	return wordValue(word)
}

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
	return s[m[2*g]:m[2*g+1]]
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
//     are. NOTE the primary probe stays spelling-DEPENDENT ACROSS its tiers for a
//     nested title (a DIGIT volume marker outranks a word or roman one, so "Part
//     One, Episode 2" has primary 2 where "Part 1, Episode 2" has primary 1) and it
//     short-circuits first, so one nested sequence spelled two ways still reads as a
//     contradiction. That asymmetry is KEPT on measurement (see StatedVolume: the
//     flat earliest-marker order it would take lost 27 recorded series positions
//     for 57), and it errs the safe way - a false contradiction withholds a merge.
//     Inside a tier the spelling no longer matters (issue #2258). The measured population is the
//     Pimsleur language courses: "Level 1
//     Lessons 1-5" and "Level 1 Lessons 6-10" are different products whose FIRST
//     stated number is identical, so a single-number comparison read 36 units of one
//     course as 36 records of one book. Comparing the sequence separates them, and
//     separates "Level 1 Lessons 1-5" from "Level 2 Lessons 1-5" too.
func SameStatedVolume(titleA, seriesA, titleB, seriesB string) bool {
	return StatementOf(titleA, seriesA).Agrees(StatementOf(titleB, seriesB))
}

// divisionMarker matches any keyword that names WHICH PART of a product a title is,
// with its number - markerSeq's vocabulary (pluralized) plus ordinalVolume's
// division words. It is the union on purpose: a title can nest two of them, and
// which family each keyword belongs to says nothing about whether the pair agrees.
//
// The number is divisionNumberArms - the very two arms ordinalVolume reads - because
// the sequence is ORDERED: "Part Two, Episode 3" states two divisions and a rule
// reading the two spellings separately could not say which came first.
var divisionMarker = regexp.MustCompile(`(?i)\b(?:books?|bks?|vols?|volumes?|parts?|pts?|episodes?|eps?|#|` + divisionWords + `)` +
	divisionNumberArms)

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
