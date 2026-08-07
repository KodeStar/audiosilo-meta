package model

import (
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

	slug := b.String()
	if len(slug) > MaxSlugLen {
		// The cut can land just after a hyphen, which is the one way a trailing
		// separator can survive the loop.
		slug = strings.Trim(slug[:MaxSlugLen], "-")
	}
	return slug
}

// UnslugPersonID is the shared catch-all person id PersonSlug substitutes for a
// name that slugs away to nothing. It is one record standing in for every such
// credit, which is a known, visible conflation rather than a defect - see
// PersonSlug.
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
