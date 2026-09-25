package titlerule

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
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

// VOLUME STATEMENTS. A title can say which volume of something it is - "Hammered,
// Book 3", "The Wandering Inn: Season 2", "Faraway Paladin: Volume II", "Wildwood
// (Book Two)" - and that statement is the one piece of evidence separating two records
// of one book from two volumes of one serial, because the KEY throws every such marker
// away (wideGenreFluff drops "Season 2" as packaging, wordVolumeMarker strips "Book
// Two" and "Volume II"). What is read here is what the key removes: a marker the key
// loses stays a marker something can state.
//
// A title is read against a series name, which is stripped first so a number in the
// series' own name is not read as a volume. Two readings come out, in a
// VolumeStatement:
//
//   - the VOLUME, decided in tiers - the first tier that reads anything answers, and
//     inside a tier the EARLIEST marker wins whatever its spelling:
//     1. BareSeq: a book/vol/part/episode marker in DIGITS (markerSeq), else a
//     residual that is nothing but a number;
//     2. a book/vol/part/episode marker in ROMAN numerals or WORDS;
//     3. a DIVISION marker (season, series, level, lesson, unit, year), in the
//     spellings the VolumePolicy reads.
//     A volume marker outranks a division marker and digits outrank words because a
//     nested serial's volume is its INNER number ("Yesterday's Gone: Season 1 - Ep. 3"
//     is volume 3), while a word volume marker ahead of a digit one is usually a
//     subseries part in front of the retailer's own "(Series, Book 13)". An arm's match
//     it cannot read (a composite word number, "Book One Hundred") is passed over for
//     the arm's next match, so it never hides a readable marker after it.
//   - the DIVISIONS, every division marker the title states in order (divisionSequence,
//     digits and words alike), for a title that NESTS them: "Level 1 Lessons 1-5" and
//     "Level 1 Lessons 6-10" share their first number and are two products.
//
// WHO READS WHICH is the VolumePolicy, and the split is set by the cost of being
// wrong. A division word numbered in WORDS ("Season One", "Level Three") is the least
// certain reading here - "Season", "Series", "Level", "Unit" and "Lesson" are ordinary
// title words - so it is read only in marker position (divisionMarkerAt), and only by
// a caller for whom a reading can do nothing worse than withhold a MERGE. A WRITER
// turns a reading into a CREATE twice over: its positive test refuses a row as a
// duplicate only when the catalogue places the matched work at the stated volume, and
// its contradiction test lets two stated volumes that differ keep the row apart. So
// the writers - and the census that counts what they would refuse - read the
// narrower policy.

// VolumePolicy is which spellings of a marker a caller reads - see VOLUME STATEMENTS.
// Its two values are the whole set; a caller picks one, it never builds one. They
// differ in tier 3 alone, so that is all a policy holds.
type VolumePolicy struct {
	division []volumeArm
}

var (
	// ReaderPolicy reads every spelling, a word-numbered division in marker position
	// included. It is for the rules that can only WITHHOLD a merge: internal/audit.
	ReaderPolicy = VolumePolicy{division: []volumeArm{{ordinalVolume, readDivision}, {romanDivision, readRoman}}}
	// WriterPolicy reads divisions in digits and roman numerals only. It is for every
	// rule that decides whether a record is CREATED - the importer's create guard, the
	// intake gates - and for metacheck's census, so the three defences agree about
	// what one book is.
	WriterPolicy = VolumePolicy{division: []volumeArm{{ordinalDigits, readDigits}, {romanDivision, readRoman}}}
)

// Volume is the volume a title states against a series name under this policy, and
// whether it states one at all.
func (p VolumePolicy) Volume(title, series string) (float64, bool) {
	return p.volumeOf(stripSeries(title, series))
}

// volumeOf is the tier walk over a residual the series name is already stripped from.
func (p VolumePolicy) volumeOf(residual string) (float64, bool) {
	// The residual is already stripped, and a series name of "" strips nothing, so this
	// is BareSeq's reading of the title without a second strip.
	if v, ok := BareSeq(residual, ""); ok {
		return v, true
	}
	if v, ok := earliestVolume(residual, volumeRomanOrWord); ok {
		return v, true
	}
	return earliestVolume(residual, p.division)
}

// VolumeStatement is everything a title says about which volume it is, against one
// series name under one policy (see VOLUME STATEMENTS). Derive it once per title and
// compare with Agrees, rather than re-reading two titles per compared pair.
type VolumeStatement struct {
	Volume    float64   // the volume, meaningful only when States
	States    bool      // whether the title states a volume
	Divisions []float64 // every division marker, in title order
}

// StatementOf is a title's VolumeStatement against a series name under a policy.
func StatementOf(title, series string, p VolumePolicy) VolumeStatement {
	residual := stripSeries(title, series)
	v, ok := p.volumeOf(residual)
	return VolumeStatement{Volume: v, States: ok, Divisions: divisionSequenceOf(residual)}
}

// Agrees reports whether two statements are of the same volume - or, either way, do
// not contradict each other. A statement of nothing is not a disagreement: "Hammered"
// beside "Hammered: The Iron Druid Chronicles, Book 3" is the duplicate the gates
// exist to catch, and only two titles that BOTH state something, differently, are
// siblings. Two things are compared, because one number is not always the whole
// statement: the two volumes, and the two division sequences.
//
// The volume is spelling-dependent ACROSS tiers - "Part One, Episode 2" is volume 2
// (a digit marker outranks a word one) where "Part 1, Episode 2" is volume 1 - so one
// nested sequence spelled two ways reads as a contradiction. That errs the safe way:
// a false contradiction withholds a merge (or, at a writer, a refusal of a row).
func (a VolumeStatement) Agrees(b VolumeStatement) bool {
	if a.States && b.States && a.Volume != b.Volume {
		return false
	}
	if len(a.Divisions) == 0 || len(b.Divisions) == 0 {
		return true
	}
	return slices.Equal(a.Divisions, b.Divisions)
}

// Label renders a statement for a report: the volume, then the division sequence in
// brackets ("2 [1/2]" is volume 2 over Part 1, Episode 2) unless the sequence says
// nothing the volume does not (empty, or exactly [volume]). A statement with no
// volume is its sequence alone ("[1]" for "Books 1-3"); one stating neither has no
// label.
//
// It is injective over what Agrees compares - a volume and a sequence - so two
// statements that disagree never share a label, which is what lets a volume-conflict
// report always name the volumes it conflicts over.
func (a VolumeStatement) Label() (string, bool) {
	var parts []string
	if a.States {
		parts = append(parts, formatVolume(a.Volume))
	}
	if n := len(a.Divisions); n > 0 && !(a.States && n == 1 && a.Divisions[0] == a.Volume) {
		seq := make([]string, n)
		for i, v := range a.Divisions {
			seq[i] = formatVolume(v)
		}
		parts = append(parts, "["+strings.Join(seq, "/")+"]")
	}
	return strings.Join(parts, " "), len(parts) > 0
}

func formatVolume(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// divisionWords is the DIVISION-class marker vocabulary - a season, a level, a
// lesson, a unit, a numbered year. It is the one spelling the volume's division tier
// (ordinalVolume, ordinalDigits) and the division sequence (divisionMarker) share, and
// both read the number after it the same way (divisionNumberArms), so no word and no
// number spelling can reach one and miss the other.
//
// The two still read different things on purpose. The sequence reads a word-numbered
// division ANYWHERE, while the volume reads it only in marker position and only under
// ReaderPolicy: "Level One Dropout" has the sequence [1] and states no volume, and
// "Wildwood (Season One)" has both. A title whose sequence is non-empty and whose
// volume is silent contradicts nothing through its volume - its sequence still
// compares.
const divisionWords = `seasons?|staffel|temporadas?|saisons?|series|levels?|lessons?|units?|jahr`

// divisionNumberArms is the number half of a division marker: a DIGIT (group 1,
// markerSeq's optional separator) or a WORD (group 2, wordVolumeMarker's REQUIRED
// whitespace - see that rule for why it is not cosmetic), then a word boundary. One
// alternation rather than two regexes, because divisionSequence is ORDERED and has to
// know which spelling came first. readDivisionNumber is its one reader.
var divisionNumberArms = `(?:\s*\.?\s*(\d+(?:\.\d+)?)|\s+(` + volumeNumberWords + `))\b`

// ordinalVolume matches a DIVISION-class marker numbered in digits or in words, which
// names WHICH PART of a product this is exactly as "Book 3" does. Every word here is in
// wideGenreFluff, which is precisely the problem: the key drops them.
var ordinalVolume = regexp.MustCompile(`(?i)\b(?:` + divisionWords + `)` + divisionNumberArms)

// ordinalDigits is ordinalVolume's DIGIT arm alone, for WriterPolicy's division tier -
// a policy that reads no word-numbered division has no use for matching one.
var ordinalDigits = regexp.MustCompile(`(?i)\b(?:` + divisionWords + `)\s*\.?\s*(\d+(?:\.\d+)?)\b`)

// romanCore is the numeral alternation I-X, the one wordVolumeMarker strips (rules.go)
// and the roman rules read back. Its order is wordVolumeMarker's: every consumer ends
// the numeral at a word boundary, so each input matches exactly one alternative
// whatever the order, and keeping that order keeps wordVolumeMarker - which feeds the
// identity key - the same pattern byte for byte.
const romanCore = `i{1,3}|iv|v|vi{1,3}|ix|x`

// romanNumeral is the numeral half of a ROMAN-numbered marker, with its separator:
// romanCore widened to xxx. The separator is REQUIRED (whitespace, or a dot as in
// "Vol.II"): with markerSeq's optional one the leading \b let "Parti Animals" read as
// "Part i" - the surname hazard wordVolumeMarker's required whitespace already guards
// against ("Partone").
const romanNumeral = `(?:\s+|\s*\.\s*)(x{0,2}(?:` + romanCore + `))\b`

// romanVolume matches a VOLUME marker whose number is a ROMAN numeral. The keyword
// list is wordVolumeMarker's (the rule that already strips this shape).
var romanVolume = regexp.MustCompile(`(?i)\b(?:` + volumeMarkerWords + `)` + romanNumeral)

// romanDivision is romanVolume for the two DIVISION words measured with roman
// numbering ("Season II", "Level III") - a rule of its own because divisions are a
// different tier.
var romanDivision = regexp.MustCompile(`(?i)\b(?:seasons?|levels?)` + romanNumeral)

// volumeArm is one spelling of a marker: the rule that finds it, and how its number
// is read off one match (false: this match states nothing this arm can read).
type volumeArm struct {
	re   *regexp.Regexp
	read func(s string, m []int) (float64, bool)
}

// volumeRomanOrWord is tier 2, the same under every policy. A ROMAN capture of
// wordVolumeMarker misses wordValue; romanVolume reads that very marker at the same
// start.
var volumeRomanOrWord = []volumeArm{
	{romanVolume, readRoman},
	{wordVolumeMarker, func(s string, m []int) (float64, bool) { return readWordNumber(s, m, 1) }},
}

// earliestVolume is the number of the earliest marker any of arms reads in s. Each
// arm contributes the first of its matches it can READ: an unreadable first match (a
// composite word number, a division word out of marker position) is passed over for
// the arm's next one rather than ending the arm, so "Unit One Hundred and Level 4"
// still states 4. The later matches are only looked for when the first is unreadable.
func earliestVolume(s string, arms []volumeArm) (float64, bool) {
	at, vol := -1, 0.0
	for _, a := range arms {
		m := a.re.FindStringSubmatchIndex(s)
		if m == nil || (at >= 0 && m[0] >= at) {
			continue
		}
		v, ok := a.read(s, m)
		if !ok {
			for _, n := range a.re.FindAllStringSubmatchIndex(s, -1)[1:] {
				if at >= 0 && n[0] >= at {
					break
				}
				if v, ok = a.read(s, n); ok {
					m = n
					break
				}
			}
		}
		if ok {
			at, vol = m[0], v
		}
	}
	return vol, at >= 0
}

func readRoman(s string, m []int) (float64, bool) { return romanValue(groupAt(s, m, 1)) }

// readDigits reads capture group 1 as a decimal number.
func readDigits(s string, m []int) (float64, bool) {
	v, err := strconv.ParseFloat(groupAt(s, m, 1), 64)
	return v, err == nil
}

// readDivisionNumber is the one reader of a divisionNumberArms match: its digits, else
// its word number (refused when composite).
func readDivisionNumber(s string, m []int) (float64, bool) {
	if groupAt(s, m, 1) != "" {
		return readDigits(s, m)
	}
	return readWordNumber(s, m, 2)
}

// readDivision reads an ordinalVolume match for ReaderPolicy: its digits wherever they
// stand, its word number only in marker position (divisionMarkerAt).
func readDivision(s string, m []int) (float64, bool) {
	if groupAt(s, m, 2) != "" && !divisionMarkerAt(s, m[0], m[1]) {
		return 0, false
	}
	return readDivisionNumber(s, m)
}

// divisionMarkerAt reports whether s[start:end] - a division word and its WORD
// number - stands as a MARKER rather than as words of the title: it opens a title
// segment and closes one, at the separators the title rules split segments at
// (divisionSeparators, plus brackets and a whitespace-bounded dash).
//
// It OPENS one when what precedes it ends in a separator, or is nothing but the space
// a removed series name left (stripSeries replaces the name with a space, so "Junkers
// Season Two" against "Junkers" is " Season Two"). A title that simply BEGINS with the
// words - "Level One Dropout", "Series One Collection" - is the title, not a marker on
// it. It CLOSES one when nothing follows, or a separator does: "Season Four Complete",
// "Level One God" and "Series Two-Step" run on into the title and state nothing.
func divisionMarkerAt(s string, start, end int) bool {
	if start == 0 {
		return false // the title begins with it
	}
	before := s[:start]
	if tb := strings.TrimRight(before, " \t"); tb != "" && !endsSegment(tb, len(tb) < len(before)) {
		return false
	}
	after := s[end:]
	ta := strings.TrimLeft(after, " \t")
	if ta == "" {
		return true
	}
	r, _ := utf8.DecodeRuneInString(ta)
	switch {
	case strings.ContainsRune(divisionSeparators, r), r == ')', r == ']':
		return true
	case r == '(', r == '[', isDash(r):
		// "Season One (Unabridged)", "Season One - The Brain Drain" - but not the
		// hyphen welded onto "Series Two-Step".
		return len(ta) < len(after)
	}
	return false
}

// endsSegment reports whether text ending in tb (trailing whitespace trimmed; spaced
// says whether there was any) ends a title segment, so what follows opens one: a
// separator, an opening bracket, or a dash with whitespace on both sides.
func endsSegment(tb string, spaced bool) bool {
	r, size := utf8.DecodeLastRuneInString(tb)
	switch {
	case strings.ContainsRune(divisionSeparators, r), r == '(', r == '[':
		return true
	case isDash(r):
		prev, _ := utf8.DecodeLastRuneInString(tb[:len(tb)-size])
		return spaced && (prev == ' ' || prev == '\t')
	}
	return false
}

// divisionSeparators is the punctuation a word-numbered division marker must stand
// between: the title rules' own segment punctuation (segmentPunct) plus the slash,
// which the rules do not split a title on but which does set a level apart from the
// course it belongs to ("Automatic Fluency: ... Conversation/Level One").
const divisionSeparators = segmentPunct + "/"

// isDash reports a hyphen, an en dash or an em dash - the three a title writes a
// segment break with.
func isDash(r rune) bool { return r == '-' || r == '\u2013' || r == '\u2014' }

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

// SameStatedVolume reports whether two titles state the same volume, or do not
// contradict each other, under a policy: StatementOf(A).Agrees(StatementOf(B)),
// deriving only what the answer needs - B's volume only when A states one, B's
// sequence only when A's is non-empty. A caller comparing one title against many
// derives each statement once and calls Agrees instead.
func SameStatedVolume(titleA, seriesA, titleB, seriesB string, p VolumePolicy) bool {
	ra, rb := stripSeries(titleA, seriesA), stripSeries(titleB, seriesB)
	if a, ok := p.volumeOf(ra); ok {
		if b, ok := p.volumeOf(rb); ok && a != b {
			return false
		}
	}
	sa := divisionSequenceOf(ra)
	if len(sa) == 0 {
		return true
	}
	sb := divisionSequenceOf(rb)
	return len(sb) == 0 || slices.Equal(sa, sb)
}

// divisionMarker matches any keyword that names WHICH PART of a product a title is,
// with its number - markerSeq's vocabulary (pluralized) plus the division words. It is
// the union on purpose: a title can nest two of them, and which family each keyword
// belongs to says nothing about whether the pair agrees.
var divisionMarker = regexp.MustCompile(`(?i)\b(?:books?|bks?|vols?|volumes?|parts?|pts?|episodes?|eps?|#|` + divisionWords + `)` +
	divisionNumberArms)

// divisionSequence is every division number a title states, in the order it states
// them, with the series name removed first (so a digit in the series' own name is not
// read as a division).
func divisionSequence(title, series string) []float64 {
	return divisionSequenceOf(stripSeries(title, series))
}

// divisionSequenceOf is divisionSequence over an already-stripped residual. A marker
// it cannot read (a composite word number) makes it state nothing rather than half a
// sequence.
func divisionSequenceOf(residual string) []float64 {
	ms := divisionMarker.FindAllStringSubmatchIndex(residual, -1)
	if len(ms) == 0 {
		return nil
	}
	out := make([]float64, 0, len(ms))
	for _, m := range ms {
		v, ok := readDivisionNumber(residual, m)
		if !ok {
			return nil
		}
		out = append(out, v)
	}
	return out
}
