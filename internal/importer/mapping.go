package importer

import (
	"fmt"
	"regexp"
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
// The list is EVIDENCE-DRIVEN, measured over the full libex dump (~1.06M books):
// every entry below appears as a trailing qualifier on at least three distinct
// credit names. It is deliberately NOT a guess at what a retailer might emit -
// widening it risks eating a real name. Ambiguous fragments are excluded on
// purpose: a bare "with" is a co-author connector as often as a credit role, so
// it is not here.
//
// Keys are lowercase, single-spaced, NFC-normalized and hyphen-free (pinned by
// TestRoleQualifiersVocabulary); lookup lowercases with the Unicode-aware
// strings.ToLower and re-composes to NFC, so "Übersetzer" matches "übersetzer"
// whether the source spells it precomposed (NFC) or decomposed (NFD - a Mac
// filesystem or an older exporter really does emit "U" + U+0308). Both the
// accented and the ASCII-folded spelling of a German role are listed because the
// dump carries both.
var roleQualifiers = map[string]bool{
	// English.
	"translator":           true,
	"translated by":        true,
	"translation":          true,
	"introduction":         true,
	"intro":                true,
	"foreword":             true,
	"foreword by":          true,
	"afterword":            true,
	"preface":              true,
	"editor":               true,
	"edited by":            true,
	"illustrator":          true,
	"illustration":         true,
	"adaptation":           true,
	"adaptor":              true,
	"contributor":          true,
	"narrator":             true,
	"ghostwriter":          true,
	"compilation":          true,
	"creator":              true,
	"producer":             true,
	"instrumental soloist": true,

	// German.
	"herausgeber":  true,
	"übersetzer":   true,
	"übersetzerin": true,
	"ubersetzer":   true,

	// French.
	"traducteur":    true,
	"traductrice":   true,
	"illustrateur":  true,
	"illustratrice": true,

	// Italian.
	"traduttore":  true,
	"traduttrice": true,
	"curatore":    true,

	// Spanish / Portuguese / Romanian.
	"traductor":  true,
	"traductora": true,
	"tradutor":   true,
	"tradução":   true,
	"traducător": true,
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
// three evidence-driven rules, repeatedly until the name stops changing (the dump
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
//
// The person stays in the credit list under the cleaned name - there is no role
// modeling yet (a future schema item; see the roadmap note in CLAUDE.md). Every
// rule is a no-op when it would leave an empty name, so a degenerate input like
// "- translator" is returned unchanged.
func CleanCreditName(name string) string {
	cleaned := name
	for i := 0; i < maxCleanPasses; i++ {
		next := collapseDoubledName(stripRoleQualifier(stripPrefixCredit(cleaned)))
		if next == cleaned {
			break
		}
		cleaned = next
	}
	return cleaned
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
// roleQualifiers). It returns the name unchanged when the suffix is not a listed
// role or when stripping would leave nothing.
//
// The captured role is lowercased, collapsed to single spaces AND re-composed to
// NFC before the lookup: the map keys are NFC, so a decomposed "Übersetzer"
// ("U" + U+0308) would otherwise never match its own entry.
func stripRoleQualifier(name string) string {
	m := roleSuffixRE.FindStringSubmatchIndex(name)
	if m == nil {
		return name
	}
	role := norm.NFC.String(strings.ToLower(strings.Join(strings.Fields(name[m[2]:m[3]]), " ")))
	if !roleQualifiers[role] {
		return name
	}
	cleaned := strings.TrimSpace(name[:m[0]])
	if cleaned == "" {
		return name
	}
	return cleaned
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

// SplitNames splits a comma-joined list of names ("A, B, C"), trimming each,
// cleaning the credit (CleanCreditName), and dropping empties. It returns nil
// when nothing usable remains.
func SplitNames(joined string) []string {
	var out []string
	for _, part := range strings.Split(joined, ",") {
		if name := strings.TrimSpace(part); name != "" {
			out = append(out, CleanCreditName(name))
		}
	}
	return out
}

var (
	apostrophes  = strings.NewReplacer("'", "", "’", "", "ʼ", "", "`", "")
	multiHyphen  = regexp.MustCompile(`-+`)
	yearPrefixRE = regexp.MustCompile(`^\d{4}`)
)

// Slugify turns arbitrary text into a slug matching the dataset's slug rules:
// lowercase, ASCII-folded diacritics, apostrophes stripped, every other
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
	for _, r := range decomposed {
		switch {
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
		case isCombiningMark(r):
			// drop
		default:
			b.WriteByte('-')
		}
	}

	slug := multiHyphen.ReplaceAllString(b.String(), "-")
	slug = strings.Trim(slug, "-")
	if len(slug) > model.MaxSlugLen {
		slug = strings.Trim(slug[:model.MaxSlugLen], "-")
	}
	return slug
}

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

// isCombiningMark reports whether r is a Unicode combining diacritical mark
// (the ranges NFD decomposition produces for accented Latin letters).
func isCombiningMark(r rune) bool {
	return (r >= 0x0300 && r <= 0x036f) || // combining diacritical marks
		(r >= 0x1ab0 && r <= 0x1aff) ||
		(r >= 0x1dc0 && r <= 0x1dff) ||
		(r >= 0x20d0 && r <= 0x20ff) ||
		(r >= 0xfe20 && r <= 0xfe2f)
}

// YearOf returns the four-digit year prefix of a date string, or "" when the
// string does not begin with one.
func YearOf(date string) string {
	return yearPrefixRE.FindString(strings.TrimSpace(date))
}
