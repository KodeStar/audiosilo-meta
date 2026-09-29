package titlerule

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// SERIES NAME EQUALITY - the one rule every writer asks "do these two names name ONE
// series?" by: the importer's series chain walk and its batch grouping, the libex
// position lookup, libex-select (through the importer's planner) and the intake
// bot (through importer.SeriesAuthorIndex.Resolve). It is NAME equality only: which
// of the series a name reaches the row may JOIN is the importer's author-aware fit
// (internal/importer/seriesauthors.go), asked afterwards and untouched by this.
//
// Two names are one series name when they share an addressable slug and a
// SeriesNameKey. The key is the name with case folded (exactly strings.EqualFold's
// equivalence), so two names that differ only in case compare as they always have.
// It widens that in ONE way: a name whose bracketed decoration DecorationKey can read
// whole is keyed by its decoration and by what is left around it, with the
// whitespace AROUND each bracketed group collapsed - so "NOMADS Legacy (German
// Edition)" and "NOMADS Legacy [German Edition]", or "Throne of Glass[French
// Edition]" and "Throne of Glass [French Edition]", are one name. That is the respell
// Audible makes under an unchanged series ASIN, and the chain walk's plain EqualFold
// stepped past it and founded a numbered duplicate.
//
// What stays different, deliberately:
//   - a different decoration ("[German Edition]" / "[French Edition]"), and a
//     decoration against none ("(Published Order)" / plain) - a decoration is
//     identity here, which is exactly the opposite of SeriesKey's comparison;
//   - anything outside the groups beyond case and the spacing that touches a
//     bracket ("Mr. X (A)" / "Mr X (A)", "X - (A)" / "X (A)"), even where the slug
//     agrees;
//   - a decoration DecorationKey cannot read whole (a script Slugify folds away,
//     "(Книга 1)"): the name falls back to exact case-insensitive equality, because
//     a half-read decoration must never agree with anything;
//   - a name that is nothing but its groups, which keeps the exact rule too.

// spacedParenGroup is one bracketed group together with the whitespace around it -
// the span SeriesNameKey collapses to a marker, so spacing that touches a bracket is
// not identity while spacing anywhere else still is. bracketGroup is the one
// definition of what a group is (match.go), shared with DecorationKey.
var spacedParenGroup = regexp.MustCompile(`\s*` + bracketGroup + `\s*`)

// SeriesNameKey is a series name's equality key, the grouping form of
// SameSeriesName: two names with an equal slug are one series name exactly when
// their keys are equal. See the block comment above for what it folds.
func SeriesNameKey(name string) string {
	folded := foldCase(name)
	if dk := DecorationKey(folded); dk != "" {
		if rest := spacedParenGroup.ReplaceAllString(folded, "\x1f"); hasAlnum(rest) {
			return "\x01" + rest + "\x00" + dk
		}
	}
	return folded
}

// SameSeriesName reports whether a and b name one series: an addressable slug they
// share (a name with none is no series at all, so it matches nothing) and one
// SeriesNameKey.
func SameSeriesName(a, b string) bool {
	slug := model.Slugify(a)
	return slug != "" && slug == model.Slugify(b) && SeriesNameKey(a) == SeriesNameKey(b)
}

// foldCase maps every rune to the smallest member of its simple case-folding orbit,
// so foldCase(a) == foldCase(b) exactly when strings.EqualFold(a, b): the key cannot
// be narrower (or wider) than the case-insensitive comparison it replaced. The key
// derives everything else from the folded string, so two names EqualFold calls equal
// always get one key.
func foldCase(s string) string {
	ascii := true
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			ascii = false
			break
		}
	}
	if ascii {
		// Every ASCII letter's orbit minimum is its upper case, except 'k' and
		// 's', whose orbits reach U+212A and U+017F - both above 'K'/'S', so the
		// minimum is still the ASCII capital.
		return strings.ToUpper(s)
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		m := r
		for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
			if f < m {
				m = f
			}
		}
		b.WriteRune(m)
	}
	return b.String()
}
