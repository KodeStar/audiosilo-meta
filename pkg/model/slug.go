package model

import (
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// slug.go holds the ONE implementation of "what slug does this text produce".
//
// It lives in pkg/model - the leaf every other package can reach - because two
// packages that may not import each other both need it: internal/importer mints
// ids with it, and pkg/check verifies a person's id against their name with it
// (checkPersonSlug). The importer imports pkg/check, so the reverse import is a
// cycle and a second copy of the rule would be a contract with two definitions.
// internal/importer.Slugify stays as the name every existing caller knows,
// delegating here.

// apostrophes is the closed set of apostrophe-position glyphs a source spells
// the same mark with. Stripping rather than separating is what makes
// "Philosopher's" one word; a glyph missing from this set splits the word
// instead, so two spellings of ONE name land on two slugs ("Cheng’en Wu" ->
// chengen-wu but "Cheng‘en Wu" -> cheng-en-wu).
//
// Counts are distinct dump credit names carrying the glyph (of 481,313), the
// same census the credit vocabularies are measured against:
//
//	'  U+0027 apostrophe          the ASCII spelling
//	’  U+2019 right single quote  352 - the typographic default
//	‘  U+2018 left single quote    12 - "Dave ‘Davey D’ Cook"; the PAIR of the
//	                                    one already listed, and its absence was
//	                                    the defect
//	´  U+00B4 acute accent         12 - "Luis D´Ors"; an accent glyph typed in
//	                                    the apostrophe's place
//	`  U+0060 grave accent         16 - the same typo on the other key
//	ʼ  U+02BC modifier apostrophe   0 - kept; the Unicode-correct spelling
//	ʻ  U+02BB modifier turned comma 2 - the Hawaiian okina ("Auliʻi Cravalho")
var apostrophes = strings.NewReplacer(
	"'", "", "’", "", "‘", "", "´", "", "`", "", "ʼ", "", "ʻ", "",
)

// transliterations romanizes the Latin letters that NFD leaves WHOLE - the ones
// with no combining-mark decomposition, which the fold would otherwise drop on
// the floor. Dropping them is not a fold, it is data loss: "Fabian Weiß" slugged
// to fabian-wei and "Katja Keßler" to katja-ke-ler, minting ids that are neither
// the person's name nor anything a later probe would find.
//
// Each mapping is MEASURED, not assumed. The corpus is the libex dump's 1,446,523
// distinct name strings (author, narrator, book title and series title); the
// count is how many of them carry the letter. Where a letter had two plausible
// romanizations, the tiebreak was the corpus itself: take every name carrying
// the letter, spell it BOTH ways, and ask which spelling the corpus attests for
// some other name. That census is decisive in every contested case:
//
//	letter  ->   corpus  base-only  digraph-only   what the evidence says
//	ø Ø     o     1,795         25             0   Møller/Moller, never Moeller
//	ß       ss    2,482          2            49   Feßler/Fessler, not Fesler
//	æ Æ     ae    1,171          0             3   Blædel/Blaedel, not Bladel
//	œ Œ     oe      249          0             3   Cœur/Coeur, not Cour
//	ð Ð     d        54          6             0   Sigurðardóttir/Sigurdardottir
//	þ Þ     th       10          0             1   Þór/Thor
//
// ø is the one that could have gone either way, and the answer is NOT the German
// umlaut convention: 25 dump names attest the plain "o" spelling and none attest
// "oe". That is also the only answer consistent with the fold this table extends
// - ö/ó/õ already fold to "o" through their combining marks, and a stroke is a
// diacritic Unicode simply did not factor out, so o-with-stroke folding to
// anything else would make one letter's slug depend on how Unicode chose to
// encode it. The umlaut rule itself is untouched.
//
// The uncontested letters follow the same split. A letter whose glyph is an
// ASCII letter plus a stroke, bar or hook folds to that BASE letter (ł -> l,
// đ -> d, ı -> i, ɑ -> a, Ɲ -> n, Ʞ -> k); a letter or ligature with no ASCII
// base takes its standard romanization (ß -> ss, æ -> ae, ﬀ -> ff).
//
// What is deliberately NOT here:
//
//   - ʔ U+0294 glottal stop (1 name, "Rachel yacaaʔał George"). Its conventional
//     ASCII rendering is the apostrophe, which Slugify already strips, so the
//     current outcome is the right one and a letter mapping would be wrong.
//   - The FULLWIDTH Latin forms U+FF21-FF5A (up to 142 names each, "メンタリスト
//     ＤａｉＧｏ"). Every occurrence sits inside a CJK string, and they are a
//     compatibility-WIDTH question (NFKC), not a transliteration one - the same
//     block's digits, punctuation and ideographic space would all have to come
//     with them for the answer to be coherent. No committed record is affected.
//     A width normalization is its own change with its own evidence.
//
// Adding to this table is mechanical: a Latin letter the corpus attests as
// dropped, romanized by base letter or by the corpus's own attested spelling,
// and always with its CASE PARTNER, since a slug that depended on capitalization
// would be a second defect.
var transliterations = map[rune]string{
	// Contested, resolved by the corpus census above.
	'ß': "ss", 'ẞ': "ss", // capital eszett: the case partner, unattested but not optional
	'ø': "o", 'Ø': "o",
	'æ': "ae", 'Æ': "ae",
	'œ': "oe", 'Œ': "oe",
	'ð': "d", 'Ð': "d",
	'þ': "th", 'Þ': "th",

	// Base letter plus a stroke, bar or hook - the shape ö/ó already fold by.
	'ł': "l", 'Ł': "l", // 424 / 16 - Watała, Łukasz Radecki
	'đ': "d", 'Đ': "d", // 34 / 32 - Vietnamese "Bóng Đá"
	'ı': "i", // 422 - Turkish dotless i. Its case partner İ is NOT here: NFD
	//               already decomposes it to "I" + a combining dot, so an entry
	//               for it would be one this table could never reach.
	'ɑ': "a",           // 1 - Latin alpha
	'Ɲ': "n",           // 1
	'Ʞ': "k",           // 1 - turned K, "DARꞰBORN"
	'ə': "e", 'Ə': "e", // 4 - schwa; Azerbaijani romanizes it "e"
	'ɛ': "e", 'Ɛ': "e", // 1 - open e, "WHƐNƐVƐR"

	// Ordinal indicators. Spanish "Mª" abbreviates María and Portuguese "1º"
	// the masculine ordinal; both are the bare letter with a raised tail.
	'ª': "a", // 37 - "Ana Mª de la Fuente"
	'º': "o", // 56 - "1º de julho"

	// Latin typographic ligatures. ﬀ is attested (3: "Marek Harloﬀ", "Steﬀen
	// Lehmann"); the rest are its family, and a ligature is unambiguously the
	// letters it draws.
	'ﬀ': "ff", 'ﬁ': "fi", 'ﬂ': "fl", 'ﬃ': "ffi", 'ﬄ': "ffl", 'ﬅ': "st", 'ﬆ': "st",
}

// Slugify turns arbitrary text into a slug matching the dataset's slug rules:
// lowercase, ASCII-folded diacritics, undecomposable Latin letters
// transliterated (see transliterations), apostrophes stripped, every other
// non-alphanumeric run collapsed to a single hyphen, trimmed, capped at
// MaxSlugLen. It returns "" when nothing slug-worthy survives (for example a
// title in a non-Latin script that folds away entirely); callers substitute a
// fallback token.
func Slugify(s string) string {
	slug, _ := SlugifyCut(s)
	return slug
}

// SlugifyCut is Slugify reporting whether the MaxSlugLen cap CUT the slug - the
// one signal a caller has that the tail of the text (for a long retail title, the
// volume number) is not in the slug. It is the rule's own answer, not a guess
// from the result's length: a slug that happens to be exactly MaxSlugLen long was
// not cut.
func SlugifyCut(s string) (slug string, cut bool) {
	slug = SlugifyWhole(s)
	if len(slug) > MaxSlugLen {
		// The cut can land just after a hyphen, which is the one way a trailing
		// separator can survive the loop.
		return strings.Trim(slug[:MaxSlugLen], "-"), true
	}
	return slug, false
}

// SlugifyWhole is Slugify WITHOUT the MaxSlugLen cap: the slug form of the
// whole text, for COMPARING two texts where a cut would hide exactly the tail
// that tells them apart. It is never an id - only Slugify's result is.
func SlugifyWhole(s string) string {
	// Strip apostrophes first so "Philosopher's" -> "philosophers", not
	// "philosopher-s".
	s = apostrophes.Replace(s)

	// Decompose accented letters, then drop the combining marks so "café" folds
	// to "cafe" and "Motörhead" to "motorhead".
	decomposed := norm.NFD.String(s)
	var b strings.Builder
	b.Grow(len(decomposed))
	// A separator is written lazily: pending records that a run of
	// non-alphanumeric runes was seen, and the hyphen is only emitted once the
	// NEXT kept rune arrives. That collapses runs and drops a leading or
	// trailing hyphen in the single pass, so no separate collapse-and-trim
	// pass is needed. Every kept rune is ASCII by construction.
	pending := false
	// emitSep pays off the separator a run of non-alphanumeric runes owes, at
	// the moment the next KEPT rune arrives. Every branch that writes calls it
	// first; the branches that drop a rune do not, which is what makes a
	// diacritic invisible rather than a word boundary.
	emitSep := func() {
		if pending && b.Len() > 0 {
			b.WriteByte('-')
		}
		pending = false
	}
	for _, r := range decomposed {
		switch {
		case r >= 'A' && r <= 'Z':
			emitSep()
			b.WriteByte(byte(r) + ('a' - 'A'))
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			emitSep()
			b.WriteByte(byte(r))
		case IsCombiningMark(r):
			// a dropped diacritic is not a separator
		default:
			// A Latin letter NFD left whole. Transliterating it HERE - after
			// the decomposition, so an accented form like "ǿ" has already shed
			// its combining mark down to "ø" - is what keeps the letter from
			// falling into the separator branch and vanishing.
			ascii, ok := transliterations[r]
			if !ok {
				pending = true
				continue
			}
			emitSep()
			b.WriteString(ascii)
		}
	}

	return b.String()
}

// UnslugPersonID is the shared catch-all person id PersonSlug substitutes for a
// name that slugs away to nothing. It would be one record standing in for every
// such credit, so no writer credits it: libex and the intake forms refuse the
// name, every other importer leaves the credit off and reports it (a LibriVox
// reader takes LibrivoxUnslugReaderSlug, an author with a LibriVox author id
// LibrivoxUnslugAuthorSlug), and pkg/check refuses the catch-all
// wherever it appears.
const UnslugPersonID = "person"

// PersonSlug derives a credit name's person identity, substituting the shared
// UnslugPersonID fallback when the name slugs away to nothing (a name written
// entirely in a script Slugify folds - Korean, Cyrillic, CJK). fellBack reports
// that substitution so a caller that CREATES the record can warn about it,
// while a caller that only MATCHES stays silent.
//
// A name whose slug is a RESERVED route literal takes the one canonical variant
// instead (ReservedPersonSlug): a person named "Search" is `search-person`. That
// step happens here, in the one function both the minting and the checking call,
// so the two rules a reserved person slug sits between - "the id IS the slug of
// the name" and "no id is a route literal" - are satisfied by the same answer
// rather than by two that could disagree. It is not a fallBack: nothing was lost
// and no conflation happened, so a caller that warns on the catch-all stays
// quiet here.
//
// It lives here, beside Slugify, because the importer mints person ids with it
// and pkg/check verifies them against it (checkPersonSlug) - two packages that
// cannot import each other. A second copy would be a contract with two
// definitions, which is exactly how a record ends up at an address nothing will
// probe again.
func PersonSlug(name string) (slug string, fellBack bool) {
	if slug = Slugify(name); slug == "" {
		return UnslugPersonID, true
	}
	if IsReservedSlug(slug) {
		return ReservedPersonSlug(slug), false
	}
	return slug, false
}

// librivoxReaderTail is the fixed segment a disambiguated LibriVox reader's id
// carries between their name's slug and their reader id.
const librivoxReaderTail = "-librivox-"

// LibrivoxReaderSlug is the id a LibriVox reader takes when the slug of their
// name is already held by a person carrying a DIFFERENT librivox_reader_id:
// PersonSlug(name) plus "-librivox-<reader id>" ("Martina", reader 634 ->
// `martina-librivox-634`), the name's part shortened to fit MaxSlugLen and the
// id kept whole (BoundedSlugTail).
//
// LibriVox readers are accounts, and two accounts with one display name ("Lisa",
// "Martina", "eva") are two people as far as anything here can tell; a slug of
// the name alone would credit them as one. The first holder of a name keeps the
// plain slug; every other reader of that name is this.
//
// ok is false for a reader id below 1 and for a name PersonSlug falls back on
// (the shared catch-all is no one's name, so no reader is disambiguated off it).
// A reserved name keeps its canonical variant as the base ("Search", reader 5 ->
// `search-person-librivox-5`), so the result is never a route literal either.
//
// It is the one composition the importer mints the id with and PersonIDMatches
// (pkg/check's checkPersonSlug) accepts it through - the same one-function rule
// PersonSlug follows.
func LibrivoxReaderSlug(name string, readerID int) (slug string, ok bool) {
	return librivoxTailSlug(name, librivoxReaderTail, readerID)
}

// librivoxTailSlug is PersonSlug(name) plus tail and a LibriVox id, the name's
// part bounded (BoundedSlugTail): the composition LibrivoxReaderSlug and
// LibrivoxAuthorSlug share. ok is false for an id below 1 and for a name
// PersonSlug falls back on.
func librivoxTailSlug(name, tail string, id int) (slug string, ok bool) {
	if id < 1 {
		return "", false
	}
	base, fellBack := PersonSlug(name)
	if fellBack {
		return "", false
	}
	return BoundedSlugTail(base, tail+strconv.Itoa(id)), true
}

// librivoxUnslugReaderPrefix is the fixed head of the id a LibriVox reader whose
// name slugs to nothing takes, ahead of their reader id.
const librivoxUnslugReaderPrefix = "librivox-reader-"

// LibrivoxUnslugReaderSlug is the id a LibriVox reader takes when their name
// slugs away to nothing (PersonSlug falls back): "librivox-reader-<reader id>"
// (reader 11294 "Ἑλένη Κεμικτσή" -> `librivox-reader-11294`). The record keeps
// the name as written; only the address is built from the reader id.
//
// Without it every such reader would land on the shared catch-all
// (UnslugPersonID), one record credited as the narrator of every unrelated
// project a Greek, Cyrillic or CJK name read. A LibriVox reader is an account
// with an id, so the id is an identity of its own where the name gives none.
//
// ok is false for a reader id below 1 and for a name that slugs to something:
// such a reader has a plain slug (PersonSlug) or, when another reader id holds
// that, LibrivoxReaderSlug. It is the one composition the importer mints the id
// with and PersonIDMatches accepts it through.
func LibrivoxUnslugReaderSlug(name string, readerID int) (slug string, ok bool) {
	return librivoxPrefixSlug(name, librivoxUnslugReaderPrefix, readerID)
}

// librivoxPrefixSlug is prefix plus a LibriVox id, for a name that slugs to
// nothing: the composition LibrivoxUnslugReaderSlug and
// LibrivoxUnslugAuthorSlug share. ok is false for an id below 1 and for a name
// that slugs to something.
func librivoxPrefixSlug(name, prefix string, id int) (slug string, ok bool) {
	if id < 1 {
		return "", false
	}
	if _, fellBack := PersonSlug(name); !fellBack {
		return "", false
	}
	return prefix + strconv.Itoa(id), true
}

// librivoxAuthorTail is the fixed segment a disambiguated LibriVox author's id
// carries between their name's slug and their author id. It differs from the
// reader tail, so a person's id says which of their two LibriVox ids it is
// built from.
const librivoxAuthorTail = "-librivox-author-"

// LibrivoxAuthorSlug is the author twin of LibrivoxReaderSlug: the id a
// LibriVox author (or translator) takes when their name's slug is another
// person's and LibriVox states no year to mint a dated name by -
// PersonSlug(name) plus "-librivox-author-<author id>" ("Ralph Scott", author
// 16428, beside a retail narrator of that name -> `ralph-scott-librivox-author-16428`),
// the name's part shortened to fit MaxSlugLen and the id kept whole.
//
// A LibriVox author id is an account of LibriVox's catalogue as a reader id is,
// so where the name alone cannot tell two people apart the id does. ok is false
// for an author id below 1 and for a name PersonSlug falls back on; a reserved
// name keeps its canonical variant as the base. It is the one composition the
// importer mints the id with and PersonIDMatches accepts it through.
func LibrivoxAuthorSlug(name string, authorID int) (slug string, ok bool) {
	return librivoxTailSlug(name, librivoxAuthorTail, authorID)
}

// librivoxUnslugAuthorPrefix is the fixed head of the id a LibriVox author whose
// name slugs to nothing takes, ahead of their author id.
const librivoxUnslugAuthorPrefix = "librivox-author-"

// LibrivoxUnslugAuthorSlug is the author twin of LibrivoxUnslugReaderSlug: the
// id a LibriVox author or translator whose name slugs away to nothing takes,
// "librivox-author-<author id>" (author 17538 "Зинаида Гиппиус" ->
// `librivox-author-17538`), the record keeping the name as written. Without it
// such a credit would be the shared catch-all (UnslugPersonID) or no credit at
// all. ok is false for an author id below 1 and for a name that slugs to
// something.
func LibrivoxUnslugAuthorSlug(name string, authorID int) (slug string, ok bool) {
	return librivoxPrefixSlug(name, librivoxUnslugAuthorPrefix, authorID)
}

// librivoxIDSlug matches the id forms a person slug composes from a LibriVox id:
// the reader's and the author's, tail and prefix alike. Groups: the reader id,
// else the author id.
var librivoxIDSlug = regexp.MustCompile(`(?:^` + librivoxUnslugReaderPrefix + `|.` + librivoxReaderTail + `)(\d+)$` +
	`|(?:^` + librivoxUnslugAuthorPrefix + `|.` + librivoxAuthorTail + `)(\d+)$`)

// LibrivoxIDOfSlug is the LibriVox id a person slug composes, 0 and 0 for a slug
// of no LibriVox form: the reader id of LibrivoxReaderSlug and
// LibrivoxUnslugReaderSlug, the author id of LibrivoxAuthorSlug and
// LibrivoxUnslugAuthorSlug.
func LibrivoxIDOfSlug(slug string) (readerID, authorID int) {
	m := librivoxIDSlug.FindStringSubmatch(slug)
	if m == nil {
		return 0, 0
	}
	readerID, _ = strconv.Atoi(m[1])
	authorID, _ = strconv.Atoi(m[2])
	return readerID, authorID
}

// PersonIDMatches reports whether id is a legal id for a person named name who
// carries librivoxReaderID and librivoxAuthorID (0 = none): PersonSlug(name),
// or - only for a person carrying that LibriVox id - LibrivoxReaderSlug or
// LibrivoxAuthorSlug over it or, for a name that slugs to nothing,
// LibrivoxUnslugReaderSlug or LibrivoxUnslugAuthorSlug, each for exactly that
// id.
//
// Every form is reachable from what the record itself states, which is the
// whole point of the person-slug rule (pkg/check's checkPersonSlug, the
// Madeleine L'Engle fork): the plain form from the name, which every importer
// slugs; the LibriVox forms from a LibriVox id, which the LibriVox import
// resolves a credit by before it ever slugs a name. A record whose id is a
// LibriVox form for some OTHER id, or that carries no such id at all, or a
// `librivox-reader-<id>` / `librivox-author-<id>` record whose name has a slug
// of its own, is reachable from none and is refused.
func PersonIDMatches(id, name string, librivoxReaderID, librivoxAuthorID int) bool {
	if want, _ := PersonSlug(name); id == want {
		return true
	}
	for _, form := range []struct {
		mint func(string, int) (string, bool)
		lv   int
	}{
		{LibrivoxReaderSlug, librivoxReaderID},
		{LibrivoxUnslugReaderSlug, librivoxReaderID},
		{LibrivoxAuthorSlug, librivoxAuthorID},
		{LibrivoxUnslugAuthorSlug, librivoxAuthorID},
	} {
		if lv, ok := form.mint(name, form.lv); ok && id == lv {
			return true
		}
	}
	return false
}

// WantPersonID is the id a person named name carrying librivoxReaderID and
// librivoxAuthorID should be filed at when nothing else decides it: the slug
// of the name, or for a name that slugs to nothing the reader's, else the
// author's, LibriVox-id form (the catch-all only when the record carries
// neither id). It is the suggestion a refusal names (pkg/check's
// checkPersonSlug, the correct-data form's rename refusal), never a minting
// rule: a held name's disambiguated forms are a run's decision, not the name's.
func WantPersonID(name string, librivoxReaderID, librivoxAuthorID int) string {
	want, fellBack := PersonSlug(name)
	if !fellBack {
		return want
	}
	if lv, ok := LibrivoxUnslugReaderSlug(name, librivoxReaderID); ok {
		return lv
	}
	if lv, ok := LibrivoxUnslugAuthorSlug(name, librivoxAuthorID); ok {
		return lv
	}
	return want
}

// IsCombiningMark reports whether r is a Unicode combining diacritical mark
// (the ranges NFD decomposition produces for accented Latin letters). Exported
// because every folding rule in the project - Slugify here, the importer's
// AI-narrator comparison form - has to agree on what a diacritic is.
func IsCombiningMark(r rune) bool {
	return (r >= 0x0300 && r <= 0x036f) || // combining diacritical marks
		(r >= 0x1ab0 && r <= 0x1aff) ||
		(r >= 0x1dc0 && r <= 0x1dff) ||
		(r >= 0x20d0 && r <= 0x20ff) ||
		(r >= 0xfe20 && r <= 0xfe2f)
}
