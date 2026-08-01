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

var apostrophes = strings.NewReplacer("'", "", "’", "", "ʼ", "", "`", "")

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
	// A separator is written lazily: pending records that a run of
	// non-alphanumeric runes was seen, and the hyphen is only emitted once the
	// NEXT kept rune arrives. That collapses runs and drops a leading or
	// trailing hyphen in the single pass, so no separate collapse-and-trim
	// pass is needed. Every kept rune is ASCII by construction.
	pending := false
	for _, r := range decomposed {
		var keep byte
		switch {
		case r >= 'A' && r <= 'Z':
			keep = byte(r) + ('a' - 'A')
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			keep = byte(r)
		case IsCombiningMark(r):
			continue // a dropped diacritic is not a separator
		default:
			pending = true
			continue
		}
		if pending && b.Len() > 0 {
			b.WriteByte('-')
		}
		pending = false
		b.WriteByte(keep)
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
// It lives here, beside Slugify, because the importer mints person ids with it
// and pkg/check verifies them against it (checkPersonSlug) - two packages that
// cannot import each other. A second copy would be a contract with two
// definitions, which is exactly how a record ends up at an address nothing will
// probe again.
func PersonSlug(name string) (slug string, fellBack bool) {
	if slug = Slugify(name); slug == "" {
		return UnslugPersonID, true
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
