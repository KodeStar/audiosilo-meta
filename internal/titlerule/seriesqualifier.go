package titlerule

import (
	"strings"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// seriesqualifier.go is the ONE reader of the qualifiers a series name states about
// itself in trailing bracketed groups: which language's EDITION the series is and
// which reading ORDER its positions state. Both vocabularies are closed. The edition
// groups are exactly SplitEditionName's (editionLanguagePhrases, through the one
// shared peelTrailingGroup); the ordering groups are the phrases measured over the
// libex dump's series names and the catalogue's own, mapped onto the model ordering
// enum. A name both readers decline carries no qualifier.

// orderingPhrases is every ordering group the reader accepts, keyed by the group's
// contents in model.SlugifyWhole form (DecorationKey's form for one group). An
// author's preferred order, a recommended listening order and a stated reading
// order are all model.OrderingRecommended.
var orderingPhrases = map[string]string{
	"chronological-order":             model.OrderingChronological,
	"chronological":                   model.OrderingChronological,
	"in-chronologischer-reihenfolge":  model.OrderingChronological,
	"publication-order":               model.OrderingPublication,
	"published-order":                 model.OrderingPublication,
	"in-veroffentlichungsreihenfolge": model.OrderingPublication,
	"authors-preferred-order":         model.OrderingRecommended,
	"recommended-listening-order":     model.OrderingRecommended,
	"reading-order":                   model.OrderingRecommended,
}

// OrderingOfDecoration is the model ordering a single bracketed group states, read
// off its DecorationKey, or "" when it states none.
func OrderingOfDecoration(decorKey string) string { return orderingPhrases[decorKey] }

// groupContents is a bracketed group's text without its brackets, or "" when group
// is not one bracketed group.
func groupContents(group string) string {
	if len(group) < 2 || !strings.ContainsRune("([", rune(group[0])) || !strings.ContainsRune(")]", rune(group[len(group)-1])) {
		return ""
	}
	return group[1 : len(group)-1]
}

// groupOrdering is the ordering a bracketed group states, or "".
func groupOrdering(group string) string {
	return OrderingOfDecoration(model.SlugifyWhole(groupContents(group)))
}

// SeriesQualifiers is what a series name's trailing qualifiers state.
type SeriesQualifiers struct {
	// Base is the name with every qualifier read removed (a trailing separator
	// trimmed); the name itself when it carries none.
	Base string
	// Language is the primary subtag an own-language edition group states, or "".
	Language string
	// Ordering is the model ordering an ordering group states, or "".
	Ordering string
}

// ReadSeriesQualifiers reads at most one edition group and one ordering group off
// the END of a series name, in either order, and stops at the first trailing group
// that is neither (or that would leave no letter or digit behind).
func ReadSeriesQualifiers(name string) SeriesQualifiers {
	q := SeriesQualifiers{Base: name}
	if !strings.ContainsAny(name, ")]") {
		return q
	}
	for q.readTrailing() {
		// one qualifier per pass; a third group is never read, since both kinds are set
	}
	return q
}

// readTrailing reads one more trailing qualifier off q.Base and reports whether it
// did.
func (q *SeriesQualifiers) readTrailing() bool {
	rest, group, ok := peelTrailingGroup(q.Base)
	if !ok {
		return false
	}
	switch {
	case q.Language == "" && groupLanguage(group) != "":
		q.Language = groupLanguage(group)
	case q.Ordering == "" && groupOrdering(group) != "":
		q.Ordering = groupOrdering(group)
	default:
		return false
	}
	q.Base = rest
	return true
}

// peelTrailingGroup splits a name ending in a bracketed group into what precedes it
// (a trailing separator trimmed) and the group itself; ok is false when the name
// does not end in a group or nothing nameable precedes it. It is the one shape both
// SplitEditionName and ReadSeriesQualifiers read.
func peelTrailingGroup(name string) (rest, group string, ok bool) {
	m := trailingGroup.FindStringSubmatch(name)
	if m == nil {
		return "", "", false
	}
	rest = strings.TrimSpace(trailingSeparatorRE.ReplaceAllString(m[1], ""))
	if !hasAlnum(rest) {
		return "", "", false
	}
	return rest, m[2], true
}
