package importer

import (
	"regexp"
	"strings"

	"github.com/kodestar/audiosilo-meta/internal/model"
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

// mapLanguage resolves a language word (case-insensitive) to its ISO code. ok is
// false for an unknown or empty word.
func mapLanguage(word string) (code string, ok bool) {
	code, ok = languageMap[strings.ToLower(strings.TrimSpace(word))]
	return code, ok
}

// marketplaces is the set of Audible marketplace regions the recording schema
// accepts (mirrors recording.schema.json asin.region enum).
var marketplaces = map[string]bool{
	"us": true, "uk": true, "ca": true, "au": true, "de": true, "fr": true,
	"es": true, "it": true, "jp": true, "in": true, "br": true,
}

// mapRegion lowercases a region word and reports whether it is a known
// marketplace. ok is false for an unknown or empty region.
func mapRegion(word string) (region string, ok bool) {
	region = strings.ToLower(strings.TrimSpace(word))
	if region == "" || !marketplaces[region] {
		return "", false
	}
	return region, true
}

// sequencePattern matches a series position: a number or an omnibus range. It
// mirrors the series.schema.json position pattern, so a value that passes here
// will pass schema validation.
var sequencePattern = regexp.MustCompile(`^\d+(\.\d+)?(-\d+(\.\d+)?)?$`)

// NormalizeSequence trims a raw series_sequence and reports whether it is a
// valid position (single number or a range like "1-3.5").
func NormalizeSequence(raw string) (pos string, ok bool) {
	pos = strings.TrimSpace(raw)
	if pos == "" || !sequencePattern.MatchString(pos) {
		return "", false
	}
	return pos, true
}

// roleQualifiers are the credit roles Audible appends to names in the author
// field as a trailing " - <role>" qualifier ("J. Kharkova - translator",
// "Valeria Kornosenko - introduction"). Matching is case-insensitive against
// this exact list only - never strip an arbitrary " - X" suffix, since a
// band/pen name can legitimately contain a spaced hyphen.
var roleQualifiers = map[string]bool{
	"translator":   true,
	"introduction": true,
	"intro":        true,
	"foreword":     true,
	"afterword":    true,
	"preface":      true,
	"editor":       true,
	"illustrator":  true,
	"adaptation":   true,
	"contributor":  true,
	"narrator":     true,
	"ghostwriter":  true,
	"compilation":  true,
}

// StripRoleQualifier removes one trailing " - <role>" credit qualifier from a
// name when <role> is in roleQualifiers (case-insensitive). The person stays in
// the credit list under the cleaned name - there is no role modeling yet (a
// future schema item; see the roadmap note in CLAUDE.md). If stripping would
// leave an empty name, the original is returned unchanged.
func StripRoleQualifier(name string) string {
	idx := strings.LastIndex(name, " - ")
	if idx < 0 {
		return name
	}
	role := strings.ToLower(strings.TrimSpace(name[idx+len(" - "):]))
	if !roleQualifiers[role] {
		return name
	}
	cleaned := strings.TrimSpace(name[:idx])
	if cleaned == "" {
		return name
	}
	return cleaned
}

// SplitNames splits a comma-joined list of names ("A, B, C"), trimming each,
// stripping trailing role qualifiers, and dropping empties. It returns nil when
// nothing usable remains.
func SplitNames(joined string) []string {
	var out []string
	for _, part := range strings.Split(joined, ",") {
		if name := strings.TrimSpace(part); name != "" {
			out = append(out, StripRoleQualifier(name))
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
