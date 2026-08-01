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

// languageMap turns an OpenAudible language word into an ISO 639-1 code. Only
// the languages the brief enumerates are accepted; anything else is unknown and
// the caller skips the book.
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

// sequencePattern matches a series position: a number or an omnibus range. It
// mirrors the series.schema.json position pattern, so a value that passes here
// will pass schema validation.
var sequencePattern = regexp.MustCompile(`^\d+(\.\d+)?(-\d+(\.\d+)?)?$`)

// NormalizeSequence trims a raw series_sequence, canonicalizes it, and reports
// whether it is a valid position (a single number or a range like "1-3.5").
//
// A position is a STRING in the schema, so two spellings of the same number are
// two different positions to every rule that compares them (series membership,
// the position-uniqueness check, the importer's same-position merge test).
// Sources spell them differently - a Postgres numeric renders "1" as "1.0" -
// so trailing fractional zeros are stripped ("1.0" -> "1", "2.50" -> "2.5",
// both endpoints of a range) and one book cannot occupy a series twice.
func NormalizeSequence(raw string) (pos string, ok bool) {
	pos = strings.TrimSpace(raw)
	if pos == "" || !sequencePattern.MatchString(pos) {
		return "", false
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
	"translator":        {model.RoleTranslator},   // 10,624
	"translated by":     {model.RoleTranslator},   // 54
	"translation":       {model.RoleTranslator},   // 53
	"introduction":      {model.RoleIntroduction}, // 2,490
	"introduction by":   {model.RoleIntroduction}, // 10
	"introductions":     {model.RoleIntroduction}, // 5
	"intro":             {model.RoleIntroduction}, // 2, kept from the pre-measurement list
	"foreword":          {model.RoleForeword},     // 2,175
	"foreword by":       {model.RoleForeword},     // 96
	"foreward":          {model.RoleForeword},     // 21, a misspelling the dump carries
	"afterword":         {model.RoleAfterword},    // 89
	"afterword by":      {model.RoleAfterword},    // 5
	"preface":           {model.RolePreface},      // 72
	"editor":            {model.RoleEditor},       // 2,454
	"edited by":         {model.RoleEditor},       // 37
	"illustrator":       {model.RoleIllustrator},  // 852
	"illustration":      {model.RoleIllustrator},  // 54
	"illustrated by":    {model.RoleIllustrator},  // 21
	"illustrations":     {model.RoleIllustrator},  // 7
	"cover illustrator": {model.RoleIllustrator},  // 7
	"ilustrator":        {model.RoleIllustrator},  // 3, a misspelling
	"adaptation":        {model.RoleAdaptation},   // 248
	"adaptor":           {model.RoleAdaptation},   // 84
	"adapter":           {model.RoleAdaptation},   // 18
	"adaption":          {model.RoleAdaptation},   // 8, a variant spelling
	"adaptations":       {model.RoleAdaptation},   // 3
	"contributor":       {model.RoleContributor},  // 435

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
// deliberately loose - the dump spells it " - ", " -", " -- " and with repeated
// whitespace - while the ROLE itself is not: the capture stops at a hyphen and is
// then checked against roleQualifiers, so only a listed role ever strips.
var roleSuffixRE = regexp.MustCompile(`\s+-+\s*([^-]+)$`)

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
// four evidence-driven rules, repeatedly until the name stops changing (the dump
// really does carry doubled qualifiers like "Dan Veksler - Translator -
// translator"):
//
//  1. A leading credit phrase from prefixCredits is dropped ("Created by Stan
//     Lee" -> "Stan Lee").
//  2. A trailing " - <role>" qualifier is dropped when <role> is in
//     roleQualifiers, matched case-insensitively after Unicode-aware lowercasing.
//     The separator tolerates a missing space after the hyphen ("X -translated
//     by") and repeated hyphens; the role never does.
//  3. An exactly-doubled name is collapsed to one half ("Full Cast Full Cast" ->
//     "Full Cast"). Only exact halves of at least two words each, so "Duran
//     Duran" and "Mitz Mitz Vah" are left alone.
//  4. A concatenated studio/production credit is removed ("Alex Hyde-White Punch
//     Audio" -> "Alex Hyde-White"). Its evidence bar is the strictest of the
//     four, and one of its three tiers needs a CENSUS of credit names this entry
//     point has no access to - see studiotail.go and creditWithRoles.
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

// creditWithRoles is CreditWithRoles with the studio-concatenation rule's
// CENSUS supplied: the question "is this half of the string independently a
// credit somewhere?", which decides tier 3 (studiotail.go). A nil
// creditSeenFunc has seen nothing, so the two tiers that carry their own
// evidence still apply and the third never fires - which is exactly what a
// caller with no catalogue in hand (pkg/scan reading tags, a single typed
// issue-form name) should get, and it is what CreditWithRoles, the public door,
// passes.
func creditWithRoles(name string, seen creditSeenFunc) (cleaned string, roles []string) {
	cleaned = name
	var stated []string
	for i := 0; i < maxCleanPasses; i++ {
		stripped, passRoles := stripRoleQualifier(stripPrefixCredit(cleaned))
		next, tailRoles := stripStudioConcat(collapseDoubledName(stripped), seen)
		if next == cleaned {
			break
		}
		cleaned = next
		stated = append(stated, passRoles...)
		stated = append(stated, tailRoles...)
	}
	return cleaned, sortedUniqueRoles(stated)
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

// stripRoleQualifier drops ONE trailing role qualifier (see roleSuffixRE and
// roleQualifiers) and reports the schema roles that qualifier stated. It returns
// the name unchanged, and no roles, when the suffix is not a listed role or when
// stripping would leave nothing.
//
// The captured role is lowercased, collapsed to single spaces AND re-composed to
// NFC before the lookup: the map keys are NFC, so a decomposed "Übersetzer"
// ("U" + U+0308) would otherwise never match its own entry. A capture that does
// not match is retried with its trailing credential titles trimmed, which is the
// one shape the source stacks onto an otherwise ordinary role.
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
	// The capture's words in their SOURCE form, alongside the normalized string
	// the vocabulary is keyed by. Neither lowercasing nor NFC changes the word
	// count, so the two stay index-for-index aligned.
	words := strings.Fields(name[m[2]:m[3]])
	role := norm.NFC.String(strings.ToLower(strings.Join(words, " ")))
	roles, listed := roleQualifiers[role]
	keep := ""
	if !listed {
		if trimmed, dropped := trimCredentialTitles(role); dropped > 0 {
			if roles, listed = roleQualifiers[trimmed]; listed {
				keep = strings.Join(words[len(words)-dropped:], " ")
			}
		}
	}
	if !listed {
		return name, nil
	}
	cleaned := strings.TrimSpace(name[:m[0]])
	if cleaned == "" {
		return name, nil
	}
	if keep != "" {
		cleaned += " " + keep
	}
	return cleaned, roles
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
	return creditNamesOf(sourceCredits(nil, joined, nil))
}

// trailingParenRE captures the content of a trailing parenthetical or bracket.
// It has two consumers, which is why it lives here beside the shared credit
// helpers rather than with either: the title parser reads an edition marker with
// it ("... (Unabridged)", recordings.go) and the credit rules read a trailing
// marker with it (the AI-narration check in libex.go, the studio-tail separator
// in studiotail.go).
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
