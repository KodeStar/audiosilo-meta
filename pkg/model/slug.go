package model

import (
	"regexp"
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

var (
	apostrophes = strings.NewReplacer("'", "", "’", "", "ʼ", "", "`", "")
	multiHyphen = regexp.MustCompile(`-+`)
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
		case IsCombiningMark(r):
			// drop
		default:
			b.WriteByte('-')
		}
	}

	slug := multiHyphen.ReplaceAllString(b.String(), "-")
	slug = strings.Trim(slug, "-")
	if len(slug) > MaxSlugLen {
		slug = strings.Trim(slug[:MaxSlugLen], "-")
	}
	return slug
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
