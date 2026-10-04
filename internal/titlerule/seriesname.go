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
// whole is keyed with its bracket characters, and the whitespace touching them,
// removed - each group's own text is kept (case folded), and so is everything around
// it - so "NOMADS Legacy (German Edition)" and "NOMADS Legacy [German Edition]", or
// "Throne of Glass[French Edition]" and "Throne of Glass [French Edition]", are one
// name. That is the respell
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
//   - anything INSIDE a group beyond the same two ("(Books 1.5)" / "(Books 1-5)",
//     "(Édition)" / "(Edition)"): DecorationKey's slug form folds punctuation and
//     diacritics, so it gates the rule but is not the key;
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
	if DecorationKey(folded) == "" || !hasAlnum(spacedParenGroup.ReplaceAllString(folded, "")) {
		return folded
	}
	// Each group becomes its own case-folded text, trimmed, between markers: the
	// bracket characters and the spacing touching them go, and nothing else does.
	// DecorationKey is only the GATE (every group readable whole); its slug form is
	// not the key, since it also folds punctuation and diacritics inside a group -
	// "(Books 1.5)" and "(Books 1-5)", "(Édition)" and "(Edition)" - which is wider
	// than the respell this rule exists for.
	return "\x01" + spacedParenGroup.ReplaceAllStringFunc(folded, func(g string) string {
		g = strings.TrimSpace(g)
		return "\x1f" + strings.TrimSpace(g[1:len(g)-1]) + "\x1f"
	})
}

// SameSeriesName reports whether a and b name one series: an addressable slug they
// share (a name with none is no series at all, so it matches nothing) and one
// SeriesNameKey.
func SameSeriesName(a, b string) bool { return NewSeriesName(a).Same(b) }

// SeriesName is one side of SameSeriesName with its slug and key computed once,
// for a caller comparing ONE name against many candidates (the chain walk, the
// libex position lookup). It is the rule itself, not a second spelling of it:
// SameSeriesName is built on it.
type SeriesName struct{ slug, key string }

// NewSeriesName prepares name for repeated SameSeriesName comparisons.
func NewSeriesName(name string) SeriesName {
	slug := model.Slugify(name)
	if slug == "" {
		return SeriesName{}
	}
	return SeriesName{slug: slug, key: SeriesNameKey(name)}
}

// Slug is the name's addressable slug, "" when it has none.
func (n SeriesName) Slug() string { return n.slug }

// Key is the name's equality key: its slug and its SeriesNameKey, so two names
// have one Key exactly when SameSeriesName calls them one. "" when the name has
// no addressable slug (it names no series).
func (n SeriesName) Key() string {
	if n.slug == "" {
		return ""
	}
	return n.slug + "\x00" + n.key
}

// SeriesNameGroupKey is NewSeriesName(name).Key(): the key a writer groups or
// indexes series names by.
func SeriesNameGroupKey(name string) string { return NewSeriesName(name).Key() }

// Same reports SameSeriesName(n's name, other). The candidate's key is computed
// only once its slug agrees, which on a chain walk it nearly always does, but
// which a lookup over another source's series list mostly does not.
func (n SeriesName) Same(other string) bool {
	return n.slug != "" && n.slug == model.Slugify(other) && n.key == SeriesNameKey(other)
}

// foldCase maps every rune to the smallest member of its simple case-folding orbit,
// so foldCase(a) == foldCase(b) exactly when strings.EqualFold(a, b): the key cannot
// be narrower (or wider) than the case-insensitive comparison it replaced. The key
// derives everything else from the folded string, so two names EqualFold calls equal
// always get one key. It is SIMPLE folding on purpose, not golang.org/x/text/cases.Fold:
// full folding (ß -> ss) would widen which names join beyond the measured change, and a
// wrong series join is worse than a duplicate, so parity with the EqualFold rule it
// replaced is the requirement rather than an accident.
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

// SameSeriesSpelling reports whether two series names are one series spelled twice, as
// internal/audit's disjoint-series veto reads them: one SeriesKey, and either the SAME
// parenthetical decoration (DecorationKey, compared only where it can be read whole) or a
// decoration on ONE side only. It is LOOSER than SameSeriesName - a catalogue's
// "(published order)" or "[Dramatized Adaptation]" note does not separate - which is why
// it only ever lifts a veto and never joins a series.
//
// The decoration clause is measured, not cautious. SeriesKey removes a parenthetical, so
// on its own it called "Pimsleur Chinese (Cantonese)" and "Pimsleur Chinese (Mandarin)"
// one series, and seven Cantonese/Mandarin and Brazilian/European Portuguese courses - the
// same lesson numbers at the same slots - went mechanical. Two DIFFERENT decorations are
// two products; a decoration on one side is the catalogue's ordering, format or edition
// note on the plain series ("(published order)", "[Dramatized Adaptation]", "(Abridged)",
// "[Spanish Edition]"), which is where every other newly-shared slot of the measurement
// sat. The clause's price is 10 correct merges left advisory where both sides carry a
// different note - six James Bond novels under "(Celebrity Performances)" beside
// "(Original)" among them - which is the right way round.
//
// Whether a side is decorated is read off the name, not off DecorationKey: that key is
// also empty for a decoration it cannot read whole, and two such names must not agree.
func SameSeriesSpelling(a, b string) bool {
	ka := SeriesKey(a)
	if ka == "" || ka != SeriesKey(b) {
		return false
	}
	if stripParenGroups(a) == a || stripParenGroups(b) == b {
		return true // undecorated, or decorated on one side only
	}
	dk := DecorationKey(a)
	return dk != "" && dk == DecorationKey(b)
}
