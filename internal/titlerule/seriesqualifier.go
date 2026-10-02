package titlerule

import (
	"strings"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// seriesqualifier.go is the ONE reader of the QUALIFIERS a series name carries about
// itself: the trailing bracketed groups that say which language's EDITION the series
// is ("Harry Potter [German Edition]") and which reading ORDER its positions state
// ("Drenai [publication order]"). The importer's series resolution reads it to reach a
// held series by what its name states rather than by the exact spelling of the
// qualifier (internal/importer, seriesresolve.go): "Throne of Glass (Deutsche
// Ausgabe)" is the "[German Edition]" of the same base, and a held series renamed to
// its plain base is still found by a claim that spells the decoration.
//
// Both vocabularies are CLOSED. The edition groups are exactly the ones
// SplitEditionName and EditionLanguage read (editionLanguagePhrases, language.go) -
// one vocabulary, not a copy of it. The ordering groups are the phrases measured over
// the libex dump's 87,342 series names and the catalogue's own (2026-10-02):
// "chronological order" 22, "publication order" 15, "published order" 10,
// "chronological" 2, "author's preferred order" 2, "recommended listening order" 2,
// "in chronologischer Reihenfolge" 2 and "in Veröffentlichungsreihenfolge" 2 - every
// bracketed ordering statement either side carries. Each maps onto the model's
// ordering enum; an author's preferred order and a recommended listening order are
// both model.OrderingRecommended, as the schema says. A group naming anything else
// ("(Abridged)", "(Light Novel)", "[Mecca]") is not a qualifier here, and a name both
// readers decline has none.

// orderingPhrases is every ordering group the reader accepts, keyed by the slug of
// the group's contents (model.SlugifyWhole, so case, spacing, apostrophes and
// diacritics are not identity), mapped to the model ordering it states.
var orderingPhrases = map[string]string{
	"chronological-order":             model.OrderingChronological,
	"chronological":                   model.OrderingChronological,
	"in-chronologischer-reihenfolge":  model.OrderingChronological,
	"publication-order":               model.OrderingPublication,
	"published-order":                 model.OrderingPublication,
	"in-veroffentlichungsreihenfolge": model.OrderingPublication,
	"authors-preferred-order":         model.OrderingRecommended,
	"recommended-listening-order":     model.OrderingRecommended,
}

// groupOrdering is the ordering a bracketed group's contents state, or "".
func groupOrdering(group string) string {
	return orderingPhrases[model.SlugifyWhole(group[1:len(group)-1])]
}

// SeriesQualifiers is what a series name's trailing qualifiers state.
type SeriesQualifiers struct {
	// Base is the name with every qualifier read removed, a trailing separator
	// trimmed; the name itself when it carries none.
	Base string
	// Language is the primary subtag an own-language edition group states, "" when
	// none does.
	Language string
	// Ordering is the model ordering an ordering group states, "" when none does.
	Ordering string
}

// ReadSeriesQualifiers reads the qualifiers at the END of a series name: at most one
// edition group and one ordering group, in either order ("X (Chronological Order)
// [German Edition]" and "X [German Edition] (Chronological Order)" read alike). It
// stops at the first trailing group that is neither, so a qualifier is only ever read
// where it qualifies the whole name. A group whose removal would leave no letter or
// digit is not read: a name that is nothing but its qualifier names no series to
// qualify.
func ReadSeriesQualifiers(name string) SeriesQualifiers {
	q := SeriesQualifiers{Base: name}
	if !strings.ContainsAny(name, ")]") {
		return q
	}
	for range 2 {
		rest, group, ok := peelTrailingGroup(q.Base)
		if !ok {
			break
		}
		if l := groupLanguage(group); l != "" && q.Language == "" {
			q.Language = l
		} else if o := groupOrdering(group); o != "" && q.Ordering == "" {
			q.Ordering = o
		} else {
			break
		}
		q.Base = rest
	}
	return q
}

// peelTrailingGroup splits a name ending in a bracketed group into what precedes it
// (a trailing separator trimmed) and the group itself; ok is false when the name does
// not end in a group or nothing nameable precedes it. It is the one shape both
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
