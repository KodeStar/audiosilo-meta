package importer

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/kodestar/audiosilo-meta/pkg/model"
	"golang.org/x/text/unicode/norm"
)

// languageMap turns a source's language word into an ISO 639-1 code. A word
// that is not here is unknown and the caller skips the book, so a wrong entry
// is worse than a missing one - every entry is a language whose 639-1 code is
// unambiguous.
//
// The second block was added after seed wave 5 refused 241 rows purely for
// want of a mapping. Counts are books in the full libex dump. The schema
// accepts any two-letter code, so nothing else had to change.
//
// Deliberately NOT mapped, though measured and available:
//
//   - "mandarin_chinese" (440), "simplified_chinese" (4), "traditional_chinese"
//     (3). All three would land on the "zh" that "chinese" already has, and the
//     last two are SCRIPT distinctions rather than languages. Collapsing four
//     source spellings onto one code is a call for a maintainer, not a mapping
//     table entry.
//   - "luo" (1). It has no ISO 639-1 code at all, only 639-3.
//   - "unknown" (643) and the empty value (22,428). Neither is a language.
//   - "ukranian" (6). A misspelling of "ukrainian", and every one of the six
//     rows is outside the importable universe, so the alias would be dead code.
//   - the remaining long tail (tamil, korean, catalan, indonesian, urdu, ...).
//     Each is unambiguous and each is a one-line addition when a wave needs it;
//     they are left out because nothing has asked for them and an unexercised
//     mapping is an untested one.
var languageMap = map[string]string{
	"english":    "en",
	"turkish":    "tr",
	"german":     "de",
	"french":     "fr",
	"spanish":    "es",
	"italian":    "it",
	"japanese":   "ja",
	"portuguese": "pt",
	"dutch":      "nl",
	"polish":     "pl",
	"russian":    "ru",
	"chinese":    "zh",

	"danish":    "da", // 7,342
	"swedish":   "sv", // 4,864
	"arabic":    "ar", // 4,869
	"hindi":     "hi", // 2,305
	"hebrew":    "he", // 968
	"czech":     "cs", // 495
	"hungarian": "hu", // 252
	"finnish":   "fi", // 171
	"norwegian": "no", // 154
	"greek":     "el", // 153

	// The third block, added after the seed's create phase: the three languages
	// the waves refused most rows for (~239 of them, recoverable by a later
	// backfill import of exactly those rows). Each 639-1 code is unambiguous,
	// and the dump spells each language with the one word listed.
	"marathi":   "mr", // 2,186
	"romanian":  "ro", // 591
	"malayalam": "ml", // 456
}

// isoCodes is the set of ISO 639-1 codes languageMap produces, so a source that
// already carries a code (the audiosilo-books projection stores the mapped code,
// not the word) resolves to exactly the same accepted set as a source that
// carries the English word.
var isoCodes = func() map[string]bool {
	m := make(map[string]bool, len(languageMap))
	for _, code := range languageMap {
		m[code] = true
	}
	return m
}()

// mapLanguage resolves a language word (case-insensitive) to its ISO code, or
// accepts an already-valid ISO 639-1 code from the accepted set verbatim. ok is
// false for an unknown or empty value.
func mapLanguage(word string) (code string, ok bool) {
	w := strings.ToLower(strings.TrimSpace(word))
	if code, ok = languageMap[w]; ok {
		return code, true
	}
	if isoCodes[w] {
		return w, true
	}
	return "", false
}

// marketplaces is the set of Audible marketplace regions the recording schema
// accepts (mirrors recording.schema.json asin.region enum).
var marketplaces = map[string]bool{
	"us": true, "uk": true, "ca": true, "au": true, "de": true, "fr": true,
	"es": true, "it": true, "jp": true, "in": true, "br": true,
}

// regionAliases maps alternate spellings of a marketplace onto the code the
// recording schema accepts. Audible's UK marketplace is "uk" in the enum, but an
// ISO-3166-shaped mirror (libex) carries "gb" for the same marketplace.
var regionAliases = map[string]string{"gb": "uk"}

// mapRegion lowercases a region word, resolves the known aliases, and reports
// whether the result is a known marketplace. ok is false for an unknown or empty
// region.
func mapRegion(word string) (region string, ok bool) {
	region = strings.ToLower(strings.TrimSpace(word))
	if alias, isAlias := regionAliases[region]; isAlias {
		region = alias
	}
	if region == "" || !marketplaces[region] {
		return "", false
	}
	return region, true
}

// sequencePattern matches a series position AS A SOURCE MAY SPELL IT: a number,
// or an omnibus range whose dash may carry whitespace on either side. The
// schema's own pattern (series.schema.json) admits no whitespace at all, which
// is the point of NORMALIZING rather than merely validating - what a source
// typed and what the record stores are two different strings, and only the
// canonical one is ever written.
//
// The whitespace tolerance was measured over the full 1.13M-book dump: of the
// 345,140 stated series positions, 4,080 fail the schema pattern and 104 of
// those (100 distinct books, 53 distinct spellings) are an omnibus range spelled
// with a space - "1 - 3", "3040 - 3049", and the one-sided "14 -15", "1.5- 3.5".
// Every one of them was dropped, which cost the book its place in its series.
// ZERO of the 104 use an en or em dash, so the dash class stays the plain hyphen
// the schema pattern names: the tolerance is whitespace, nothing else.
var sequencePattern = regexp.MustCompile(`^\d+(\.\d+)?(\s*-\s*\d+(\.\d+)?)?$`)

// NormalizeSequence trims a raw series_sequence, canonicalizes it, and reports
// whether it is a valid position (a single number or a range like "1-3.5").
//
// A position is a STRING in the schema, so two spellings of the same number are
// two different positions to every rule that compares them (series membership,
// the position-uniqueness check, the importer's same-position merge test).
// Sources spell them differently - a Postgres numeric renders "1" as "1.0", and
// a range is written both "1-3" and "1 - 3" - so trailing fractional zeros are
// stripped ("1.0" -> "1", "2.50" -> "2.5", both endpoints of a range) and the
// whitespace around a range's dash is removed. One book cannot occupy a series
// twice, and what is stored always satisfies the schema pattern.
func NormalizeSequence(raw string) (pos string, ok bool) {
	pos = strings.TrimSpace(raw)
	if pos == "" || !sequencePattern.MatchString(pos) {
		return "", false
	}
	// Removing ALL whitespace is equivalent to removing it around the dash: the
	// value has been trimmed and has matched sequencePattern, whose only
	// whitespace is the `\s*` on either side of that dash. A regexp replace here
	// cost ~130ns and an allocation on every position; the guard keeps the
	// overwhelmingly common tight spelling free.
	if strings.ContainsAny(pos, " \t\n\v\f\r") {
		pos = strings.Join(strings.Fields(pos), "")
	}
	if !strings.Contains(pos, ".") {
		return pos, true
	}
	parts := strings.Split(pos, "-")
	for i, part := range parts {
		parts[i] = trimFractionalZeros(part)
	}
	return strings.Join(parts, "-"), true
}

// trimFractionalZeros drops the trailing zeros (and then a bare trailing dot) of
// a decimal number. It is only ever called on a value the sequence pattern
// matched, and only touches a value that HAS a fractional part - so "10" keeps
// its zero.
func trimFractionalZeros(n string) string {
	if !strings.Contains(n, ".") {
		return n
	}
	return strings.TrimSuffix(strings.TrimRight(n, "0"), ".")
}

var (
	// isbnPattern mirrors common.schema.json #/$defs/isbn, so an ISBN that
	// passes NormalizeISBN passes schema validation.
	isbnPattern = regexp.MustCompile(`^(\d{9}[0-9Xx]|\d{13})$`)
	isbnStripRE = regexp.MustCompile(`[-\s]`)
)

// NormalizeISBN strips the hyphens and whitespace an ISBN is printed with
// ("978-1-234-56789-7") and reports whether the remainder is a well-formed
// ISBN-10/13. It is the repo's ONE definition of an acceptable ISBN, shared with
// internal/issueform so a typed form field and a bulk import accept the same
// values.
func NormalizeISBN(raw string) (isbn string, ok bool) {
	isbn = isbnStripRE.ReplaceAllString(strings.TrimSpace(raw), "")
	if !isbnPattern.MatchString(isbn) {
		return "", false
	}
	return isbn, true
}

// seriesNarratorQualifiers are the lead-in phrases of a trailing parenthetical
// that qualifies a series name BY ITS NARRATOR rather than naming a different
// series: "Sherlock Holmes - Die galaktischen Fälle (gelesen von Peter Bocek)".
// The qualifier is an artifact of one retailer listing each re-narration as its
// own series - four of them for that one title - so a single book minted FOUR
// works, each in a series of its own. Narration is modeled by
// recording.narrators; it is not part of a series' identity.
//
// This is the same class of see-through as the "(Full-Cast Edition)" production
// qualifier on a work title (recordings.go), and it is held to the same
// evidence bar as roleQualifiers and the AI vocabulary: measured over the full
// dump, in the trailing-bracket position only, and CLOSED. Counts are distinct
// series titles and the books behind them.
//
// Keys are in foldCredit form (lowercased, diacritics folded), so one entry
// covers "Hörspiele" and "Horspiele" alike.
//
// Deliberately NOT included, though both were candidates: "read by" and "lu
// par". Neither occurs in the trailing-bracket position anywhere in the dump,
// and both occur elsewhere as false positives - "A Read by the Sea Wedding
// Romance" is a title, and «"À la recherche du temps perdu" lu par de grands
// acteurs» credits "great actors" rather than a person. Adding a phrase the
// data does not attest would be a guess at what a retailer might emit, which is
// exactly what this vocabulary style refuses. Each is one line away the day a
// dump carries one.
var seriesNarratorQualifiers = []string{
	"gelesen von",    // 15 series titles, 446 books - German "read by"
	"narrated by",    // 3 titles, 91 books
	"gesprochen von", // 1 title, 8 books - German "spoken by"
	"horspiele von",  // 2 titles, 6 books - German "audio dramas by"
}

// cleanSeriesName strips a trailing narrator qualifier from a series name so
// every narration of one serial resolves to ONE series. The marker must be a
// trailing parenthetical (or bracket) whose content BEGINS with a listed phrase
// at a word boundary and continues with something: a bare "(gelesen von)" names
// nobody, and a strip that would leave an empty name is refused (the dump does
// carry series titles that are nothing but a bracket).
//
// Only the trailing bracket is read. The dump also spells the qualifier
// dash-separated ("Sherlock Holmes gelesen von Rupert Pichler"), which this
// deliberately does not touch: an unbracketed tail is indistinguishable from a
// series name that legitimately contains the words, and the bracketed form is
// what the measurement covers.
func cleanSeriesName(name string) string {
	trimmed := strings.TrimSpace(name)
	if !strings.ContainsAny(trimmed, closeBracketChars) {
		return trimmed
	}
	m := trailingParenRE.FindStringSubmatchIndex(trimmed)
	if m == nil {
		return trimmed
	}
	marker := foldCredit(trimmed[m[2]:m[3]])
	for _, phrase := range seriesNarratorQualifiers {
		rest, found := strings.CutPrefix(marker, phrase)
		if !found || continuesWord(rest) || strings.TrimSpace(rest) == "" {
			continue
		}
		stripped := strings.TrimSpace(trimmed[:m[0]])
		if stripped == "" || !bracketsBalanced(stripped) {
			continue
		}
		return stripped
	}
	return trimmed
}

// titleNarratorQualifiers are the narrator lead-ins that appear INSIDE a title
// or subtitle rather than at the end of a series name. They are the subset of
// seriesNarratorQualifiers the dump spells in that position (pinned by
// TestTitleNarratorVocabularyIsASeriesSubset, so the two lists can never drift
// into two different ideas of what a narrator lead-in is).
//
// "horspiele von" is deliberately NOT here. In the series position it means
// "audio dramas read by"; in a title it is an AUTHORSHIP phrase - "Die schönsten
// Märchen-Hörspiele von Grimm, Hauff und Andersen" credits the Brothers Grimm,
// not a narrator - and stripping it would delete the authors from a title.
//
// Keys are in foldCredit form (lowercased, diacritics folded).
var titleNarratorQualifiers = []string{
	"gelesen von",    // 11 of the 13 books in the bounded shape below
	"narrated by",    // 2
	"gesprochen von", // 0 in the bounded shape; 16 books spell it elsewhere in the position
}

// titleVolumeSuffixRE is the BOUND that makes the mid-title strip safe: the
// qualifier must be followed by a comma and a volume marker that ends the
// string. That is the shape a retailer emits when it lists each re-narration of
// one serial as its own product - "Die galaktischen Fälle des Sherlock Holmes -
// gelesen von Andreas Lange, Band 11" beside "... - gelesen von Peter Bocek,
// Band 11" beside the undecorated "..., Band 11" - which minted THREE works for
// one book. Narration is modeled by recording.narrators; it is not part of a
// work's identity, and it is the same see-through cleanSeriesName performs one
// level up.
//
// Measured over the full 1.13M-book dump, the bounded shape matches 13 books
// (11 subtitles "gelesen von", 2 "narrated by") and ZERO titles, every one of
// them a Sherlock Holmes serial and every one a genuine narrator qualifier.
//
// The bound is what keeps the rule off the titles that use the words for real:
// "The Gospel Narrated by Jesus", "Life of Josiah Henson ... as Narrated by
// Himself", "Narrated by the Author: How to Produce an Audiobook on a Budget"
// and "the iconic classic narrated by BAFTA and Oscar-nominated actor Saoirse
// Ronan" are all in the dump and none of them carries a trailing volume marker.
// The marker words are the three the shape is measured with; a fourth is one
// line away the day a dump carries one, and inventing them now would widen an
// unmeasured rule.
var titleVolumeSuffixRE = regexp.MustCompile(`(?i),\s*(?:band|folge|episode)\s*\d+(?:\.\d+)?\s*$`)

// narratorObjectLeads are the words that begin a narration credit naming NOBODY
// - a reflexive or a generic object rather than a person. Each is taken from the
// dump's own false-positive family above ("as Narrated by Himself", "narrated by
// the monster himself", "Narrated by the Author"). None of them can be the start
// of a narrator's name, so a qualifier leading with one is left alone even when
// it does carry a volume marker: the belt to titleVolumeSuffixRE's braces.
var narratorObjectLeads = map[string]bool{
	"himself": true, "herself": true, "themselves": true,
	"the": true, "a": true, "an": true,
}

// stripTitleNarratorQualifier removes a mid-title narrator qualifier - the
// qualifier itself and the separator that introduced it - leaving the volume
// marker in place, so every re-narration of one volume resolves to ONE work
// title. "X: Y - gelesen von Andreas Lange, Band 11" becomes "X: Y, Band 11",
// which is byte-for-byte the title the undecorated listing of the same volume
// already carries.
//
// It returns the title unchanged unless every condition holds: the trailing
// volume marker (titleVolumeSuffixRE), a listed lead-in at a WORD boundary
// before it, a credit that names somebody (narratorObjectLeads), a non-empty
// remainder, and brackets that still balance - the same posture cleanSeriesName
// takes, for the same reason (a title we cannot take the qualifier off cleanly
// is left exactly as the source spelled it).
func stripTitleNarratorQualifier(title string) string {
	m := titleVolumeSuffixRE.FindStringIndex(title)
	if m == nil {
		return title
	}
	head := title[:m[0]]
	// One fold for the common case: a title carrying the volume marker but no
	// lead-in at all never reaches the positional scan.
	if !containsNarratorLeadIn(foldCredit(head)) {
		return title
	}
	cut, credit := lastNarratorLeadIn(head)
	if cut < 0 || narratorObjectLeads[firstFoldedWord(credit)] {
		return title
	}
	kept := strings.TrimSpace(strings.TrimRight(strings.TrimSpace(head[:cut]), "-–—"))
	if kept == "" || !bracketsBalanced(kept) {
		return title
	}
	return kept + title[m[0]:]
}

// containsNarratorLeadIn reports whether a FOLDED string holds any listed
// lead-in at all. It is the cheap prefilter for the positional scan.
func containsNarratorLeadIn(folded string) bool {
	for _, phrase := range titleNarratorQualifiers {
		if strings.Contains(folded, phrase) {
			return true
		}
	}
	return false
}

// lastNarratorLeadIn locates the LAST listed lead-in that starts a word in head,
// returning its byte offset and the folded credit that follows it (empty and -1
// when there is none). The scan folds each candidate suffix rather than folding
// head once, because foldCredit is not length-preserving - an index into the
// folded form is not an index into the title, and cutting a title at the wrong
// byte is how a strip mangles a name.
func lastNarratorLeadIn(head string) (int, string) {
	best, credit := -1, ""
	for i := range head {
		if i > 0 && endsWord(head[:i]) {
			continue // mid-word, so not a lead-in
		}
		folded := foldCredit(head[i:])
		for _, phrase := range titleNarratorQualifiers {
			rest, found := strings.CutPrefix(folded, phrase+" ")
			if found && strings.TrimSpace(rest) != "" {
				best, credit = i, strings.TrimSpace(rest)
			}
		}
	}
	return best, credit
}

// firstFoldedWord is the first word of an already-folded string, for the
// object-lead check.
func firstFoldedWord(folded string) string {
	name, _, _ := strings.Cut(folded, " ")
	return name
}

// bracketsBalanced reports whether s leaves no bracket open. It guards the strip
// above: the regex matches the LAST parenthetical, so a doubled opener ("Foo
// ((gelesen von Peter)") leaves "Foo (" behind - a series named after a dangling
// bracket, whose slug is "foo" and which therefore collides with the real "Foo".
// A name we cannot take the qualifier off cleanly is left exactly as the source
// spelled it, which is the same posture every other strip rule takes when its
// remainder is unusable.
func bracketsBalanced(s string) bool {
	open, square := 0, 0
	for _, r := range s {
		switch r {
		case '(':
			open++
		case ')':
			open--
		case '[':
			square++
		case ']':
			square--
		}
		if open < 0 || square < 0 {
			return false
		}
	}
	return open == 0 && square == 0
}

// isoDatePart reduces an ISO timestamp to its YYYY-MM-DD date part;
// addRecording validates the result before use. Both separators a source may
// use are cut: the ISO "T" ("2018-10-18T23:00:00") and the space a SQL
// timestamp renders ("2018-10-18 23:00:00"). Shared by every source whose
// release date arrives as a timestamp (Libation, libex).
func isoDatePart(ts string) string {
	ts = strings.TrimSpace(ts)
	if i := strings.IndexAny(ts, "T "); i >= 0 {
		ts = ts[:i]
	}
	return ts
}

// roleQualifiers are the credit roles retailers append to a name as a trailing
// " - <role>" qualifier ("J. Kharkova - translator", "Valeria Kornosenko -
// introduction"). Matching is case-insensitive against this exact list only -
// never strip an arbitrary " - X" suffix, since a band/pen name can legitimately
// contain a spaced hyphen ("The All - Stars").
//
// It is BOTH the strip vocabulary and the qualifier -> role mapping. The value
// is the set of schema roles the qualifier states, which is how a stripped
// qualifier survives as data (work.credits) instead of being discarded: one
// role for almost every entry, SEVERAL for the combined strings the dump really
// carries ("editor and translator"), and NONE for a qualifier that is a genuine
// credit word with no home in the controlled vocabulary. A nil value means
// "strip exactly as before, emit no credit" - facts only, never a guessed role.
//
// The list is EVIDENCE-DRIVEN, measured over the full libex dump (~1.06M books;
// the counts in the comments are distinct books backing that exact spelling).
// The inclusion bar is 3 books, which is what keeps out the long tail of
// one-off scraping typos. It is deliberately NOT a guess at what a retailer
// might emit - widening it risks eating a real name. Ambiguous fragments are
// excluded on purpose: a bare "with" is a co-author connector as often as a
// credit role, so it is not here.
//
// Keys are lowercase, single-spaced, NFC-normalized and hyphen-free (pinned by
// TestRoleQualifiersVocabulary); lookup lowercases with the Unicode-aware
// strings.ToLower and re-composes to NFC, so "Übersetzer" matches "übersetzer"
// whether the source spells it precomposed (NFC) or decomposed (NFD - a Mac
// filesystem or an older exporter really does emit "U" + U+0308). Both the
// accented and the ASCII-folded spelling of a German role are listed because the
// dump carries both.
var roleQualifiers = map[string][]string{
	// English.
	"translator":         {model.RoleTranslator},   // 10,624
	"translated by":      {model.RoleTranslator},   // 54
	"translation":        {model.RoleTranslator},   // 53
	"translated":         {model.RoleTranslator},   // 7, the bare participle
	"introduction":       {model.RoleIntroduction}, // 2,490
	"introduction by":    {model.RoleIntroduction}, // 10
	"introductions":      {model.RoleIntroduction}, // 5
	"intro":              {model.RoleIntroduction}, // 2, kept from the pre-measurement list
	"foreword":           {model.RoleForeword},     // 2,175
	"foreword by":        {model.RoleForeword},     // 96
	"foreward":           {model.RoleForeword},     // 21, a misspelling the dump carries
	"afterword":          {model.RoleAfterword},    // 89
	"afterword by":       {model.RoleAfterword},    // 5
	"preface":            {model.RolePreface},      // 72
	"editor":             {model.RoleEditor},       // 2,454
	"edited by":          {model.RoleEditor},       // 37
	"edited":             {model.RoleEditor},       // 3, the bare participle
	"illustrator":        {model.RoleIllustrator},  // 852
	"illustration":       {model.RoleIllustrator},  // 54
	"illustrated by":     {model.RoleIllustrator},  // 21
	"illustrations":      {model.RoleIllustrator},  // 7
	"cover illustrator":  {model.RoleIllustrator},  // 7
	"cover illustration": {model.RoleIllustrator},  // 1, riding along with it
	"ilustrator":         {model.RoleIllustrator},  // 3, a misspelling
	"adaptation":         {model.RoleAdaptation},   // 248
	"adaptor":            {model.RoleAdaptation},   // 84
	"adapter":            {model.RoleAdaptation},   // 18
	"adaption":           {model.RoleAdaptation},   // 8, a variant spelling
	"adaptations":        {model.RoleAdaptation},   // 3
	"contributor":        {model.RoleContributor},  // 435

	// Stripped, but NOT credit roles in the controlled vocabulary. They stay in
	// the strip list because they are real qualifiers a name should not keep;
	// they map to nothing because the vocabulary has no honest home for them and
	// inventing one would be a guess:
	"narrator":             nil, // narration is modeled by recording.narrators
	"ghostwriter":          nil, // uncredited authorship, not a contributor role
	"compilation":          nil, // a `compiler` role is a separate, later call
	"creator":              nil, // ~14 books; under-attested, held for a maintainer call
	"producer":             nil, // 3 books, all narrator-side
	"instrumental soloist": nil, // 1 book, narrator-side
	// A dramatized production's director. 36 books / 12 distinct names pooled
	// across the author and narrator sides ("Alison Belle Bews - director",
	// "Sir Peter Hall - director", "Cassandra de Cuir - director"), well clear
	// of the 3-book bar. It maps to nothing because the credit vocabulary
	// excludes direction deliberately - it is a production role, not a
	// contribution to the TEXT, which is what credits models. Leaving it out of
	// the strip list is what minted alison-belle-bews-director, a second
	// identity for a real person.
	"director":  nil, // 36 books / 12 names
	"directeur": nil, // 9 books / 1 name - the French spelling, same non-role
	// The dramatization family - the person who turned a book into an audio
	// drama. Four spellings, all measured over the full dump and all well clear
	// of the 3-book bar, and they name the SAME people ("Jerry Robbins -
	// dramatization" and "Jerry Robbins - dramatizer" are one man), which is why
	// they ride in together rather than one at a time. They map to nothing on
	// purpose: whether a dramatized PRODUCTION is an "adaptation" of the text in
	// the credit vocabulary's sense is a maintainer call, and guessing it would
	// put a role on a person no source stated. Leaving them out of the strip list
	// is what minted jerry-robbins-dramatization and its siblings.
	"dramatization": nil, // 9 books / 5 names
	"dramatizer":    nil, // 7 books / 3 names
	"dramatist":     nil, // 6 books / 2 names
	"dramatisation": nil, // 1, the British spelling riding along
	// The prologue family. A prologue is its own front-matter element - the
	// enum's foreword, preface and introduction are three different things, and
	// picking one of them for it would be an invention - so it strips and states
	// nothing. Spanish "prólogo" is listed with its ASCII-folded spelling, as
	// every other accented role is.
	"prologue": nil, // 11 books / 11 names
	"prólogo":  nil, // 4 books / 3 names
	"prologo":  nil, // 2 on its own, the unaccented spelling riding along
	// The cover-art family. "cover design" (14 books) is what clears the bar and
	// the rest are its spellings, riding along as the combined qualifiers do.
	// They map to nothing for the same reason "director" does: the credit
	// vocabulary models contributions to the TEXT, and a cover is not one.
	// Leaving them out of the strip list minted henrik-koitzsch-cover-design and
	// its siblings - second identities for real designers.
	"cover design":   nil, // 14
	"cover art":      nil, // 2
	"cover artwork":  nil, // 1
	"cover designer": nil, // 1

	// German.
	"herausgeber":  {model.RoleEditor},       // 44
	"übersetzer":   {model.RoleTranslator},   // 5,364
	"ubersetzer":   {model.RoleTranslator},   // the ASCII-folded spelling
	"übersetzerin": {model.RoleTranslator},   // 65
	"ubersetzerin": {model.RoleTranslator},   //
	"übersetzung":  {model.RoleTranslator},   // 13
	"ubersetzung":  {model.RoleTranslator},   //
	"vorwort":      {model.RoleForeword},     // 10
	"einführung":   {model.RoleIntroduction}, // 4
	"einfuhrung":   {model.RoleIntroduction}, //
	"bearbeitung":  {model.RoleAdaptation},   // 6

	// French.
	"traducteur":    {model.RoleTranslator},   // 1,191
	"traductrice":   {model.RoleTranslator},   // 154
	"traduction":    {model.RoleTranslator},   // 12
	"éditeur":       {model.RoleEditor},       // 3; the ASCII-folded spelling rides along
	"editeur":       {model.RoleEditor},       // 1 on its own, below the bar
	"illustrateur":  {model.RoleIllustrator},  // 136
	"illustratrice": {model.RoleIllustrator},  // 58
	"illustateur":   {model.RoleIllustrator},  // 10, a misspelling
	"préface":       {model.RolePreface},      // 10
	"présentation":  {model.RoleIntroduction}, // 4
	"postface":      {model.RoleAfterword},    // 3

	// Italian.
	"traduttore":   {model.RoleTranslator},   // 1,516
	"traduttrice":  {model.RoleTranslator},   // the feminine form
	"traduzione":   {model.RoleTranslator},   // 35
	"curatore":     {model.RoleEditor},       // 29
	"illustratore": {model.RoleIllustrator},  // 11
	"introduzione": {model.RoleIntroduction}, // 10
	"prefazione":   {model.RolePreface},      // 8
	"postfazione":  {model.RoleAfterword},    // 7

	// Spanish / Portuguese / Romanian.
	"traductor":    {model.RoleTranslator},   // 668
	"tradutor":     {model.RoleTranslator},   // 286
	"tradução":     {model.RoleTranslator},   // 229
	"traducător":   {model.RoleTranslator},   // 32
	"traductora":   {model.RoleTranslator},   // 20
	"traduccion":   {model.RoleTranslator},   // 3, the unaccented spelling
	"adaptador":    {model.RoleAdaptation},   // 19
	"editora":      {model.RoleEditor},       // 17, the feminine "editor" in a credit position
	"ilustrador":   {model.RoleIllustrator},  // 7
	"adaptação":    {model.RoleAdaptation},   // 4 across both numbers
	"adaptações":   {model.RoleAdaptation},   //
	"introducción": {model.RoleIntroduction}, // 3

	// Combined qualifiers: one string naming TWO roles. They are a real,
	// recurring pattern in the dump, and they are exactly why credits is a list
	// of (person, role) pairs rather than a role field on the person - the same
	// person gets one entry per role. Spellings of a combo whose dominant form
	// clears the bar ride along with it.
	"editor and translator":   {model.RoleEditor, model.RoleTranslator},   // 9
	"editor translator":       {model.RoleEditor, model.RoleTranslator},   // 6
	"translator and editor":   {model.RoleEditor, model.RoleTranslator},   // 3
	"translator/editor":       {model.RoleEditor, model.RoleTranslator},   // 3
	"editor/translator":       {model.RoleEditor, model.RoleTranslator},   // 3
	"editor introduction":     {model.RoleEditor, model.RoleIntroduction}, // 7
	"editor and introduction": {model.RoleEditor, model.RoleIntroduction}, // 6
	"editor/introduction":     {model.RoleEditor, model.RoleIntroduction}, // 2
	"introduction editor":     {model.RoleEditor, model.RoleIntroduction}, // 2
	"editor and contributor":  {model.RoleContributor, model.RoleEditor},  // 4
	// "author" is not a credit role - authorship is already the work's authors
	// list - so these two state exactly one role that credits can carry.
	"author/editor": {model.RoleEditor}, // 4
	"editor/author": {model.RoleEditor}, // 4
}

// credentialTitles are the academic and generational title fragments the source
// stacks AFTER a role in one qualifier blob ("X - Introduction M.D.", "X -
// Editor Jr."). roleSuffixRE captures the whole tail as one string, so the role
// would not match its own vocabulary entry with the title still attached.
//
// Trimming them is safe because it only ever CHANGES an outcome when what
// remains is a listed role: "smith jr." trims to "smith", which is not a role,
// so that name keeps its suffix exactly as before.
//
// What it must NOT do is discard the fragment. "Jr." is a generational suffix -
// part of the person's name, and part of their identity: dropping it merges
// "Theodore C. Van Alst Jr." into "Theodore C. Van Alst", who may well be his
// father. So the trimmed words are handed back to the caller (see
// trimCredentialTitles' second return) and re-appended to the NAME the
// qualifier was stripped from.
var credentialTitles = map[string]bool{
	"m.d.": true, "md": true, "ph.d.": true, "phd": true, "jr.": true, "jr": true,
}

// trimCredentialTitles drops trailing credential fragments from a captured
// qualifier, repeatedly - the dump really does stack them ("introduction md
// md", "editor ph.d. ph.d."). It never returns an empty string: a qualifier
// that is NOTHING but a credential ("md") is left whole, and would not match a
// role anyway.
//
// dropped is how many trailing WORDS it removed, which is what lets the caller
// take the same count off the original (uncased, un-normalized) capture and put
// those words back on the name.
func trimCredentialTitles(qualifier string) (trimmed string, dropped int) {
	words := strings.Fields(qualifier)
	kept := len(words)
	for kept > 1 && credentialTitles[words[kept-1]] {
		kept--
	}
	return strings.Join(words[:kept], " "), len(words) - kept
}

// prefixCredits are the leading credit phrases the dump carries in front of a
// name ("Created by Stan Lee", the Italian "Creato da Stan Lee"). Evidence-driven
// like roleQualifiers: these two are the only prefix forms observed, so no other
// phrase is stripped. Matching is case-insensitive; each entry keeps its trailing
// space so a name merely starting with the word is untouched.
var prefixCredits = []string{"created by ", "creato da "}

// roleSuffixRE locates a trailing credit qualifier's separator. The separator is
// deliberately loose - the dump spells it " - ", " -", " -- ", with repeated
// whitespace, and with an EN or EM dash in place of the hyphen ("Bernhard Kempen
// – Übersetzer") - while the ROLE itself is not: the capture stops at a dash and
// is then checked against roleQualifiers, so only a listed role ever strips.
//
// The dash CLASS is studiotail.go's dashSepRE verbatim, which is the point: one
// source spells its separator one way and the two rules that read a trailing
// qualifier must agree on what a dash IS. Reading the hyphen only cost 176
// German translator credits in the dump - which do not merely lose their role,
// they mint a BOGUS PERSON ("Bernhard Kempen – Übersetzer" slugs to
// bernhard-kempen-ubersetzer, a second identity for a real translator who is also
// in the catalogue under his own name).
//
// Two deliberate narrowings come with the wider dash class:
//
//   - `-{1,2}` rather than `-+`. Measured over the full 1.13M-row dump, ZERO
//     credit names carry a 3-or-more hyphen separator, so the bound costs nothing
//     and keeps the two rules character-for-character identical.
//   - the capture excludes the en and em dash as well as the hyphen. That is what
//     makes a DOUBLED qualifier still strip one role at a time however the source
//     spelled its separators: the leftmost match whose capture reaches the end of
//     the string is the last separator, so "X – Translator – translator" resolves
//     the same way "X - Translator - translator" always did.
//
// WHITESPACE IS OPTIONAL ON BOTH SIDES OF THE DASH (`\s*`), which is where this
// rule and dashSepRE deliberately part company. The trailing `\s*` was always
// there ("X -translated by"); the LEADING one was added after the seed, for the
// shape that welds the role straight onto the surname with nothing but a hyphen:
// "Gigi Rosa-traduttore", which minted gigi-rosa-traduttore as a person.
//
// The two rules can differ here because their tails are different kinds of
// string. This rule's tail is a CLOSED vocabulary, so a hyphen that is really
// part of a surname ("Alex Hyde-White") simply fails the lookup and nothing is
// stripped. dashSepRE's tail is free text, so it must keep the whitespace as its
// only evidence that the dash is a separator at all - an en dash inside a surname
// is not a boundary (studiotail.go).
//
// Measured over the full dump, that is exactly what the relaxation costs and
// buys: of the 436,101 distinct credit names, 28 (29 books) have a trailing
// dash-welded segment that IS a listed role, and every one of the 28 is a real
// person carrying a real role qualifier - "Fiamma Izzo-traduttore", "Marina
// Pugliano-translator", "Sandra Schwittau-Übersetzer", "Mirron Willis-
// Introduction". ZERO are hyphenated surnames. Two of the 28 are a bonus: the
// dump spells their separator with a NON-BREAKING space ("Daniel Hayes -
// editor"), which `\s` does not match and `\s*` therefore steps over, leaving
// strings.TrimSpace (which does know U+00A0) to take it off the name.
//
// Like the studio-tail rule, this one reaches populations the dump never
// measured, through the two PUBLIC doors: SplitNames (pkg/scan, over whatever an
// ID3 tag holds) and CleanCreditName (internal/issueform, over a name a
// contributor typed). It is bounded there by its own SHAPE rather than by the
// measurement - the tail must be a WHOLE listed role, matched in full against a
// closed vocabulary, and a strip that would leave an empty name is refused - so
// the worst an unmeasured population can do is fail to strip. Nothing here can
// invent a name; it can only decline to shorten one.
var roleSuffixRE = regexp.MustCompile(`\s*(?:-{1,2}|[–—]{1,2})\s*([^-–—]+)$`)

// minDoubledHalfWords is the smallest half a doubled-name collapse will accept.
// Requiring TWO words per half is what keeps "Duran Duran" intact: a single
// repeated word is a legitimate name, while a repeated multi-word run
// ("Melissa K. Roehrich Melissa K. Roehrich") is a source-side duplication.
const minDoubledHalfWords = 2

// maxCleanPasses bounds the fixpoint loop in CleanCreditName. Each pass can only
// shorten the name, so the loop converges long before this; the cap exists so a
// future rule that could grow a name cannot spin.
const maxCleanPasses = 8

// CleanCreditName normalizes one credit name from an external source. It applies
// five evidence-driven rules, repeatedly until the name stops changing (the dump
// really does carry doubled qualifiers like "Dan Veksler - Translator -
// translator"), and then folds the result onto a canonical collective record if
// that is what it names (rule 6, once, after the loop):
//
//  1. A leading credit phrase from prefixCredits is dropped ("Created by Stan
//     Lee" -> "Stan Lee").
//  2. A trailing role qualifier is dropped when the role is in roleQualifiers,
//     matched case-insensitively after Unicode-aware lowercasing. Both spellings
//     the sources use are read: the dash form "X - translator"
//     (stripRoleQualifier), whose separator tolerates a missing space after the
//     hyphen ("X -translated by") and repeated hyphens, and the parenthetical
//     form "X (introduction)" (stripParenRoleQualifier). The role itself never
//     tolerates anything: only a listed qualifier, matched in FULL, ever strips,
//     so "Frank (Dean) Martin" and a title's "(Unabridged)" are untouched.
//  3. An exactly-doubled name is collapsed to one half ("Full Cast Full Cast" ->
//     "Full Cast"). Only exact halves of at least two words each, so "Duran
//     Duran" and "Mitz Mitz Vah" are left alone.
//  4. A leading courtesy title is dropped when the bare name is already a
//     credit somewhere ("Sir Arthur Conan Doyle" -> "Arthur Conan Doyle"). Like
//     rule 5 it needs the CENSUS, so it never fires here - see honorific.go.
//  5. A concatenated studio/production credit is removed ("Alex Hyde-White Punch
//     Audio" -> "Alex Hyde-White"). Its evidence bar is the strictest of the
//     five, and one of its three tiers needs a CENSUS of credit names this entry
//     point has no access to - see studiotail.go and creditWithRoles.
//  6. A collective or unknown-identity credit is folded onto its canonical
//     record name ("Narratori Vari" -> "Various", "N.N." -> "Unknown"), so one
//     statement has one person of record rather than one per language. Whole
//     names only, so the branded ensembles ("Museum Audiobooks cast") are
//     untouched - see collective.go.
//
// The person stays in the credit list under the cleaned name. The stripped role
// is no longer discarded - CreditWithRoles returns it, and the importer records
// it as a work credit (schema work.credits). Every rule is a no-op when it would
// leave an empty name, so a degenerate input like "- translator" is returned
// unchanged.
func CleanCreditName(name string) string {
	cleaned, _ := CreditWithRoles(name)
	return cleaned
}

// CreditWithRoles is CleanCreditName plus the ROLES the qualifiers it stripped
// stated, mapped onto the schema's controlled vocabulary and returned sorted and
// deduplicated (so "X - Translator - translator" states translator once).
//
// roles is nil unless a stripped qualifier actually maps: a qualifier with no
// vocabulary entry strips exactly as it always did and states nothing, which is
// the facts-only posture - an unmapped role is not a role we may invent.
//
// It is the shared half of the two questions every caller has about a source
// credit ("what is this person called?" and "what did the source say they
// did?"), answered in ONE pass so the name and the roles can never come from
// different cleanings.
func CreditWithRoles(name string) (cleaned string, roles []string) {
	return creditWithRoles(name, nil)
}

// creditCensus is the evidence the two census-consulting cleaning rules read.
// Its two universes are deliberately different questions, because the two rules
// need different answers:
//
//	anySide   "is this string independently a credit SOMEWHERE?" - the
//	          studio-concatenation rule's tier 3 (studiotail.go). A studio is
//	          evidenced by having a record at all, on whichever side it was
//	          credited, so narrowing this would only lose cleanups.
//	sameSide  "is this string a credit on THIS side - author or narrator?" - the
//	          honorific rule (honorific.go). "Steve West" the narrator does not
//	          attest "Dr. Steve West" the author, and treating it as evidence
//	          merged 7 measured pairs of different humans into one record.
//
// A zero value has seen nothing, so the tiers that carry their own evidence
// still apply and the two census-consulting rules never fire - which is exactly
// what a caller with no catalogue in hand (pkg/scan reading tags, a single typed
// issue-form name) should get, and what CreditWithRoles, the public door,
// passes.
type creditCensus struct {
	anySide  creditSeenFunc
	sameSide creditSeenFunc
	// onHonorific, when set, is notified of every honorific merge the cleaning
	// performs, so a run can report the list it made (Summary.HonorificMerges).
	onHonorific func(from, to string)
}

// creditWithRoles is CreditWithRoles with ONE census supplied, answering both
// census questions from a single universe. It is the shape every caller had
// before the honorific rule needed a side, and it is still the right shape for
// a caller that legitimately has one universe and no credit side to speak of -
// a test, or a probe over a hand-built name list.
//
// The IMPORT pipeline must not use it: a row states its credits on two sides
// and they are different populations of humans (see creditCensus). It goes
// through sourceCredits, which carries the side's census.
func creditWithRoles(name string, seen creditSeenFunc) (cleaned string, roles []string) {
	return creditWithRolesSided(name, creditCensus{anySide: seen, sameSide: seen})
}

// creditWithRolesSided is creditWithRoles with the two census universes given
// separately, which is what the import pipeline has.
func creditWithRolesSided(name string, c creditCensus) (cleaned string, roles []string) {
	cleaned = name
	var stated []string
	for i := 0; i < maxCleanPasses; i++ {
		stripped, passRoles := stripRoleQualifier(stripPrefixCredit(cleaned))
		stripped, parenRoles := stripParenRoleQualifier(stripped)
		if bare := stripHonorific(stripped, c.sameSide); bare != stripped {
			if c.onHonorific != nil {
				c.onHonorific(stripped, bare)
			}
			stripped = bare
		}
		next, tailRoles := stripStudioConcat(collapseDoubledName(stripped), c.anySide)
		if next == cleaned {
			break
		}
		cleaned = next
		stated = append(stated, passRoles...)
		stated = append(stated, parenRoles...)
		stated = append(stated, tailRoles...)
	}
	// The collective/placeholder fold is the LAST step, outside the loop and
	// after every cleaning rule has run: it answers "which record does this
	// credit name?", which is only askable once the name is the one the import
	// would store. See collective.go for why this is the one place it happens.
	return canonicalCreditName(cleaned), sortedUniqueRoles(stated)
}

// sortedUniqueRoles sorts and deduplicates a collected role list, returning nil
// for an empty one. Sorting is what makes an import DETERMINISTIC: the order
// roles were stripped in is an artifact of how the source spelled the name, not
// a fact about the book.
func sortedUniqueRoles(roles []string) []string {
	if len(roles) == 0 {
		return nil
	}
	slices.Sort(roles)
	return slices.Compact(roles)
}

// stripPrefixCredit drops one leading phrase from prefixCredits when a non-empty
// name remains.
func stripPrefixCredit(name string) string {
	for _, prefix := range prefixCredits {
		rest, ok := cutPrefixFold(name, prefix)
		if !ok {
			continue
		}
		if rest = strings.TrimSpace(rest); rest != "" {
			return rest
		}
	}
	return name
}

// cutPrefixFold reports whether s begins with prefix under case folding and
// returns the remainder. It walks RUNES rather than slicing s at len(prefix):
// a byte slice would cut a multibyte leading name mid-rune (producing invalid
// UTF-8 that no fold can match), and a byte-length comparison structurally
// cannot match a fold whose case mapping changes the byte length.
func cutPrefixFold(s, prefix string) (rest string, ok bool) {
	i := 0
	for _, want := range prefix {
		if i >= len(s) {
			return "", false
		}
		got, size := utf8.DecodeRuneInString(s[i:])
		if got == utf8.RuneError && size == 1 {
			return "", false // invalid UTF-8; never a prefix match
		}
		if unicode.ToLower(got) != unicode.ToLower(want) {
			return "", false
		}
		i += size
	}
	return s[i:], true
}

// stripRoleQualifier drops ONE trailing " - <role>" qualifier (see roleSuffixRE
// and roleQualifiers) and reports the schema roles that qualifier stated. It
// returns the name unchanged, and no roles, when the suffix is not a listed role
// or when stripping would leave nothing. The parenthetical spelling of the same
// qualifier is stripParenRoleQualifier's job.
//
// The captured role is lowercased, collapsed to single spaces AND re-composed to
// NFC before the lookup (lookupRoleQualifier): the map keys are NFC, so a
// decomposed "Übersetzer" ("U" + U+0308) would otherwise never match its own
// entry. A capture that does not match is retried with its trailing credential
// titles trimmed, which is the one shape the source stacks onto an otherwise
// ordinary role.
//
// A trim that ENABLES the match hands its words back to the name, in their
// original spelling ("Theodore C. Van Alst - editor Jr." -> "Theodore C. Van
// Alst Jr.", credited as editor). The alternative - dropping them with the
// qualifier - would silently rewrite a generational suffix out of a person's
// identity and merge them with the person of the same name who has none.
func stripRoleQualifier(name string) (string, []string) {
	m := roleSuffixRE.FindStringSubmatchIndex(name)
	if m == nil {
		return name, nil
	}
	if cleaned, roles, ok := stripQualifierAt(name, m); ok {
		return cleaned, roles
	}
	return name, nil
}

// stripParenRoleQualifier drops ONE trailing PARENTHETICAL role qualifier ("Neil
// Gaiman (introduction)" -> "Neil Gaiman") and reports the schema roles it
// stated. It is the SAME rule as stripRoleQualifier against the SAME vocabulary,
// for the other separator the source spells a qualifier with: libex writes both
// "Valeria Kornosenko - introduction" and "Neil Gaiman (introduction)", and
// nothing about the qualifier changes with the brackets around it.
//
// Only a full-content match ever strips, which is what keeps the rule off the
// parentheticals that are part of a name rather than a qualifier: a nickname
// ("Frank (Dean) Martin"), a disambiguator, and the edition marker
// "(Unabridged)" - which belongs to TITLE cleaning (recordings.go), not to a
// credit - all leave the credit exactly as the source spelled it, because none
// of their contents is a listed role. Measured over the full libex dump, the
// non-role parenthetical is by far the commoner shape, so the narrow test is
// doing real work.
//
// Brackets ride along with parentheses because trailingParenRE reads both and
// the dump spells the qualifier either way.
func stripParenRoleQualifier(name string) (string, []string) {
	// trailingParenRE cannot match without a closing bracket, and almost no
	// credit name has one, so the prefilter keeps the regex off the common path.
	if !strings.ContainsAny(name, closeBracketChars) {
		return name, nil
	}
	m := trailingParenRE.FindStringSubmatchIndex(name)
	if m == nil {
		return name, nil
	}
	if cleaned, roles, ok := stripQualifierAt(name, m); ok {
		return cleaned, roles
	}
	return name, nil
}

// stripQualifierAt is the half the two qualifier spellings share: given a match
// whose capture (m[2]:m[3]) is the qualifier and whose start (m[0]) is where the
// separator begins, it resolves the qualifier against the vocabulary and returns
// the shortened name. ok is false when the qualifier is not listed or when
// stripping it would leave nothing - both of which mean "leave the name alone",
// so the two callers can hand back their input unchanged.
func stripQualifierAt(name string, m []int) (string, []string, bool) {
	// The capture's words in their SOURCE form, alongside the normalized string
	// the vocabulary is keyed by. Neither lowercasing nor NFC changes the word
	// count, so the two stay index-for-index aligned.
	words := strings.Fields(name[m[2]:m[3]])
	roles, keep, listed := lookupRoleQualifier(words)
	if !listed {
		return "", nil, false
	}
	cleaned := strings.TrimSpace(name[:m[0]])
	if cleaned == "" {
		return "", nil, false
	}
	if keep != "" {
		cleaned += " " + keep
	}
	return cleaned, roles, true
}

// lookupRoleQualifier resolves one captured qualifier's words against
// roleQualifiers. keep is the credential words the trim handed back for the NAME
// to re-carry (empty unless a trim is what enabled the match); listed reports
// whether the qualifier is in the vocabulary at all, which is not the same
// question as whether it states a role - a listed qualifier may map to none.
func lookupRoleQualifier(words []string) (roles []string, keep string, listed bool) {
	role := norm.NFC.String(strings.ToLower(strings.Join(words, " ")))
	if roles, listed = roleQualifiers[role]; listed {
		return roles, "", true
	}
	if trimmed, dropped := trimCredentialTitles(role); dropped > 0 {
		if roles, listed = roleQualifiers[trimmed]; listed {
			return roles, strings.Join(words[len(words)-dropped:], " "), true
		}
	}
	return nil, "", false
}

// maxRoleQualifierWords is the longest roleQualifiers key, in words, derived
// from the vocabulary rather than spelled out so the two cannot drift.
var maxRoleQualifierWords = func() int {
	most := 0
	for role := range roleQualifiers {
		if n := len(strings.Fields(role)); n > most {
			most = n
		}
	}
	return most
}()

// leadingRoleQualifier reports the schema roles the LEADING words of a removed
// tail state, longest match first ("editor and translator" before "editor").
//
// It is the counterpart to stripRoleQualifier, for the one shape that reaches
// the credit rules from the wrong end: a source that appends a role AND a studio
// behind one separator ("Jane Doe - translator Punch Audio") hands the qualifier
// to the studio-tail rule as part of the tail, where stripRoleQualifier can no
// longer see it. Only a listed role ever matches, so the worst case is silence -
// and it states roles about a tail that has already been REMOVED from the name,
// never about the name itself.
func leadingRoleQualifier(tail []string) []string {
	for n := min(maxRoleQualifierWords, len(tail)); n >= 1; n-- {
		role := norm.NFC.String(strings.ToLower(strings.Join(tail[:n], " ")))
		if roles, listed := roleQualifiers[role]; listed {
			return roles
		}
	}
	return nil
}

// collapseDoubledName collapses a name whose words split into two identical
// halves ("Full Cast Full Cast" -> "Full Cast"). See minDoubledHalfWords for why
// a one-word half never collapses.
func collapseDoubledName(name string) string {
	words := strings.Fields(name)
	half := len(words) / 2
	if len(words)%2 != 0 || half < minDoubledHalfWords {
		return name
	}
	for i := 0; i < half; i++ {
		if !strings.EqualFold(words[i], words[half+i]) {
			return name
		}
	}
	return strings.Join(words[:half], " ")
}

// credit is one source credit: the cleaned name, and the roles the trailing
// qualifier it was cleaned of stated (nil for the overwhelming majority, which
// carry no qualifier at all).
type credit struct {
	name  string
	roles []string
}

// splitRawNames splits a comma-joined list of names into the source's OWN
// spellings - trimmed and de-emptied, but not cleaned. It is what a parser
// reaches for when it is filling sourceBook.authors/narrators from a bare
// string: those fields are the source's structured credit list, and cleaning
// them here would run the credit cleaner a pass too early, discarding the very
// role qualifier sourceCredits exists to read ("Rosa Vidal - Translator" would
// arrive as "Rosa Vidal", stating nothing). Every credit is cleaned exactly
// once, at sourceCredits, whichever shape the source handed it over in.
func splitRawNames(joined string) []string {
	var out []string
	for _, part := range strings.Split(joined, ",") {
		if name := strings.TrimSpace(part); name != "" {
			out = append(out, name)
		}
	}
	return out
}

// creditNamesOf is a credit list's names, for the callers that only need the
// people (every narrator path, and the recordings-only work matcher).
func creditNamesOf(credits []credit) []string {
	if len(credits) == 0 {
		return nil
	}
	out := make([]string, 0, len(credits))
	for _, c := range credits {
		out = append(out, c.name)
	}
	return out
}

// SplitNames splits a comma-joined list of names ("A, B, C"), trimming each,
// cleaning the credit (CleanCreditName), and dropping empties. It returns nil
// when nothing usable remains.
//
// It is the name-only view of sourceCredits, kept as the shared public helper
// (pkg/scan reads tags through it) because a consumer outside the importer has
// no work record to hang a role on - and, for the same reason, no credit
// census, so it cleans with the two self-evidencing studio-tail tiers only.
func SplitNames(joined string) []string {
	return creditNamesOf(sourceCredits(nil, joined, creditCensus{}))
}

// trailingParenRE captures the content of a trailing parenthetical or bracket.
// It has consumers on both sides of the package, which is why it lives here
// beside the shared credit helpers rather than with either: the title parser
// reads an edition marker with it ("... (Unabridged)", recordings.go) and the
// credit rules read a trailing marker with it (the role qualifier in
// stripParenRoleQualifier, the AI-narration check in libex.go, the studio-tail
// separator in studiotail.go). Each decides for itself what content it accepts;
// the regex only locates it.
var trailingParenRE = regexp.MustCompile(`\s*[([]([^()\[\]]*)[)\]]\s*$`)

var yearPrefixRE = regexp.MustCompile(`^\d{4}`)

// Slugify turns arbitrary text into a slug matching the dataset's slug rules.
// The implementation is model.Slugify - the leaf copy pkg/check can reach too
// (checkPersonSlug verifies a person's id against their name, and pkg/check
// cannot import this package without a cycle). This is the name the importer's
// own callers, and the sibling audiosilo-sidecars module through pkg/scan, know
// it by; new code outside this package should call model.Slugify directly.
func Slugify(s string) string { return model.Slugify(s) }

// BoundedSlugTail joins a base slug and a disambiguating tail (an author credit,
// a release year, a numeric collision suffix) into a slug of at most
// MaxSlugLen, shortening only the BASE. The tail's END survives every regime by
// design: it carries whatever tells one candidate from the next, so cutting it
// would collapse a collision chain onto one repeated slug - the defect this
// exists to prevent.
//
// The base is cut at a hyphen (word) boundary when one leaves room, which also
// keeps the join a valid slug; a base whose leading word alone overruns the room
// is hard-cut instead, with the cut edge trimmed so no stray hyphen meets the
// tail. Callers pass a NONEMPTY base that is already a valid slug (every caller
// substitutes a fallback token for an empty Slugify result), so the result can
// never be empty or start with a hyphen. Slugs are ASCII by construction
// (Slugify folds everything else away), so every cut here is a byte index that
// cannot split a rune.
//
// Bounding does NOT make a candidate globally unique: two bases agreeing up to
// the cut produce the same candidate. Chain walkers are built for that - see
// NumberedSlugAt and workSlugAt.
func BoundedSlugTail(base, tail string) string {
	if slug, ok := wordBoundedSlugTail(base, tail); ok {
		return slug
	}
	room := model.MaxSlugLen - len(tail)
	if room <= 0 {
		// Defensive regime no current caller reaches (the longest tail any of them
		// composes is an author credit plus a numeric suffix, ~53 bytes): a tail
		// that alone fills the cap leaves nothing of the base, and the suffix lives
		// at the tail's END, so keep the END and drop the tail's own head - at a
		// hyphen boundary when there is one.
		cut := len(tail) - model.MaxSlugLen
		if b := strings.IndexByte(tail[cut:], '-'); b >= 0 {
			cut += b
		}
		return strings.Trim(tail[cut:], "-")
	}
	return strings.TrimRight(base[:room], "-") + tail
}

// wordBoundedSlugTail is BoundedSlugTail's whole-words half: it reports the
// joined slug when base+tail fits within MaxSlugLen outright or after cutting
// base at a hyphen boundary, and ok=false when only a hard cut can fit it.
// Callers that want to shrink their own tail before conceding to a hard cut ask
// this first.
func wordBoundedSlugTail(base, tail string) (string, bool) {
	if len(base)+len(tail) <= model.MaxSlugLen {
		return base + tail, true
	}
	// len(base) > room follows from the overflow, so base[:room+1] is in range.
	if room := model.MaxSlugLen - len(tail); room > 0 {
		if cut := strings.LastIndexByte(base[:room+1], '-'); cut > 0 {
			return base[:cut] + tail, true
		}
	}
	return "", false
}

// NumberedSlugAt is the numeric collision-suffix formula shared by the recording
// and series candidate chains: base for the first candidate, then base-2,
// base-3, ... with the suffix bounded to MaxSlugLen. Every walker of a chain
// (getOrCreateSeries and its read-only twins findSeries and seriesIndex.find)
// must go through this one implementation, or two of them would disagree about
// which slug a name resolves to.
//
// The NUMBERED candidates are pairwise distinct: each ends in its own "-<n>", so
// two of them could only match if a hyphen matched a digit. Candidate 0 (the
// bare base) is not covered by that argument - a base that itself ends in "-<n>"
// coincides with the n'th candidate - which costs a walk one wasted iteration on
// a slug it has already tested, and nothing more: the walk stops at the first
// FREE slug either way.
func NumberedSlugAt(base string, i int) string {
	if i == 0 {
		return base
	}
	return BoundedSlugTail(base, fmt.Sprintf("-%d", i+1))
}

// YearOf returns the four-digit year prefix of a date string, or "" when the
// string does not begin with one.
func YearOf(date string) string {
	return yearPrefixRE.FindString(strings.TrimSpace(date))
}
