package check

import (
	"slices"
	"strings"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// linkoverlay.go is the ONE-RECORD overlay the link rules are asked over by a writer
// that is about to change a single record's links: internal/issueform's correct-data
// form (a correction) and internal/audit's T-LINK class (a proposed link). Both ask
// the same question - which faults would the change INTRODUCE - so both ask it here,
// and a proposal the audit calls clean is one the form would accept.

// LinkRecord is one record's link-bearing members, as a LinkOverlay states them.
type LinkRecord struct {
	// TranslationOf is the record's translation_of set, sorted.
	TranslationOf []string
	// Language is a WORK's own language tag; a series derives its language from its
	// members, so the field is unused for one.
	Language string
	// Ordering and OrderingOf are a SERIES' ordering members; unused for a work.
	Ordering   string
	OrderingOf string
}

// LinkRecordOf reads a record's current link members off a view.
func LinkRecordOf(v LinkView, kind model.RedirectKind, id string) LinkRecord {
	r := LinkRecord{TranslationOf: v.TranslationOf(kind, id)}
	if kind == model.RedirectWorks {
		r.Language = v.Language(kind, id)
	} else {
		r.Ordering, r.OrderingOf = v.Ordering(id), v.OrderingOf(id)
	}
	return r
}

// LinkOverlay is a LinkView with ONE record's link-bearing members replaced by
// Record: every answer about that record reads Record, and every inverse (who
// translates an id, which series are variants of one) is corrected for it. A WORK's
// Language moves the derived language of every series listing it, so a series'
// language is re-derived over the overlay when the overlaid work's language differs
// from the base's.
//
// Record may be changed between questions; the overlay holds no memo of its own.
type LinkOverlay struct {
	base   LinkView
	kind   model.RedirectKind
	id     string
	Record LinkRecord
}

// NewLinkOverlay is base with the record (kind, id) stated as rec.
func NewLinkOverlay(base LinkView, kind model.RedirectKind, id string, rec LinkRecord) *LinkOverlay {
	return &LinkOverlay{base: base, kind: kind, id: id, Record: rec}
}

func (o *LinkOverlay) is(kind model.RedirectKind, id string) bool {
	return kind == o.kind && id == o.id
}

// Holds, Survivor and Members are the base's: an overlay changes links, not which
// records exist or what a series lists.
func (o *LinkOverlay) Holds(kind model.RedirectKind, id string) bool { return o.base.Holds(kind, id) }
func (o *LinkOverlay) Survivor(kind model.RedirectKind, id string) (string, bool) {
	return o.base.Survivor(kind, id)
}
func (o *LinkOverlay) Members(series string) []string { return o.base.Members(series) }

func (o *LinkOverlay) Language(kind model.RedirectKind, id string) string {
	switch {
	case o.is(kind, id) && kind == model.RedirectWorks:
		return o.Record.Language
	case kind == model.RedirectSeries && o.kind == model.RedirectWorks &&
		o.Record.Language != o.base.Language(model.RedirectWorks, o.id):
		// The overlaid work may be a member: derive over the overlay, not the base.
		members := o.base.Members(id)
		sw := make([]model.SeriesWork, len(members))
		for i, m := range members {
			sw[i].Work = m
		}
		return model.SeriesLanguage(sw, func(w string) string { return o.Language(model.RedirectWorks, w) })
	}
	return o.base.Language(kind, id)
}

func (o *LinkOverlay) TranslationOf(kind model.RedirectKind, id string) []string {
	if o.is(kind, id) {
		return o.Record.TranslationOf
	}
	return o.base.TranslationOf(kind, id)
}

func (o *LinkOverlay) TranslatedBy(kind model.RedirectKind, id string) []string {
	if kind != o.kind {
		return o.base.TranslatedBy(kind, id)
	}
	return o.relink(o.base.TranslatedBy(kind, id), slices.Contains(o.Record.TranslationOf, id))
}

func (o *LinkOverlay) Ordering(id string) string {
	if o.is(model.RedirectSeries, id) {
		return o.Record.Ordering
	}
	return o.base.Ordering(id)
}

func (o *LinkOverlay) OrderingOf(id string) string {
	if o.is(model.RedirectSeries, id) {
		return o.Record.OrderingOf
	}
	return o.base.OrderingOf(id)
}

func (o *LinkOverlay) Variants(id string) []string {
	if o.kind != model.RedirectSeries {
		return o.base.Variants(id)
	}
	return o.relink(o.base.Variants(id), o.Record.OrderingOf == id)
}

// relink is an inverse list with the overlaid record's base entry replaced by its
// overlaid one: removed, and put back (sorted) when the overlay names the id.
func (o *LinkOverlay) relink(base []string, names bool) []string {
	out := slices.DeleteFunc(slices.Clone(base), func(s string) bool { return s == o.id })
	if names {
		out = append(out, o.id)
		slices.Sort(out)
	}
	return out
}

// IntroducedFaults is every fault of the link rules involving the overlaid record
// that the overlay has and its base does not - the faults the change would
// INTRODUCE. A fault the base already carries is not the change's to answer for.
// Order is LinkFaults' over the overlay.
//
// Two faults are the same fault when their faultKeys are equal - NOT when they are
// deep-equal: a related-records list is a set, and a view that answers an empty
// list where another answers nil (or the same ids in another order) must not turn a
// pre-existing fault into an introduced one.
func (o *LinkOverlay) IntroducedFaults() []LinkFault {
	before := make(map[faultKey]bool)
	for _, f := range LinkFaults(o.base, o.kind, o.id) {
		before[keyOf(f)] = true
	}
	var out []LinkFault
	for _, f := range LinkFaults(o, o.kind, o.id) {
		if !before[keyOf(f)] {
			out = append(out, f)
		}
	}
	return out
}

// faultKey is a LinkFault's identity: every scalar member as is, and each id list
// as a SET (sorted, deduplicated, nil and empty alike) joined into one string.
type faultKey struct {
	code                         LinkFaultCode
	severity                     LinkSeverity
	kind                         model.RedirectKind
	from, field, to              string
	survivor, language, ordering string
	others, works                string
}

func keyOf(f LinkFault) faultKey {
	return faultKey{
		code: f.Code, severity: f.Severity, kind: f.Kind,
		from: f.From, field: f.Field, to: f.To,
		survivor: f.Survivor, language: f.Language, ordering: f.Ordering,
		others: idSetKey(f.Others), works: idSetKey(f.Works),
	}
}

// idSetKey spells an id list as a set: sorted, deduplicated, and "" for nil and
// empty alike. A slug never holds the NUL byte, so the join is unambiguous.
func idSetKey(ids []string) string {
	return strings.Join(slices.Compact(slices.Sorted(slices.Values(ids))), "\x00")
}
