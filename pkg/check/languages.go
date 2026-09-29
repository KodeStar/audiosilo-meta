package check

// languages.go is the ONE rule of record for the Languages Phase 2 links:
// translation_of on a work and on a series, and a series' ordering /
// ordering_of. The schema pins the shapes (a non-empty slug set; the ordering
// enum; ordering_of requiring ordering); what the schema cannot see is what the
// slugs NAME, and that is these rules' job.
//
// The rules are asked through three doors, each over its own state and each in
// its own words: this package's load (over the assembled catalogue, as
// path-anchored problems and advisories), internal/issueform's correct-data form
// (over the catalogue with the one corrected record overlaid, as verdicts) and
// internal/repair's merges (over the staged plan, as refusals). All three read
// LinkFaults, which answers in CODES rather than prose, so a rule is changed here
// and nowhere else.
//
// The links are STATED evidence only - nothing infers a translation or an
// ordering - so every rule here is about keeping a statement resolvable and one
// hop deep, never about whether it is true.

import (
	"fmt"
	"maps"
	"slices"

	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// The link members, as the schema spells them and a LinkFault names them.
const (
	FieldTranslationOf = "translation_of"
	FieldOrdering      = "ordering"
	FieldOrderingOf    = "ordering_of"
)

// LinkView is the state the link rules read. Every id is in the namespace of the
// family kind names (works and series never link across), and every answer is
// about the state the caller wants judged - the loaded catalogue, a catalogue with
// one record overlaid, a staged merge.
type LinkView interface {
	// Holds reports whether the family holds a LIVE record under id.
	Holds(kind model.RedirectKind, id string) bool
	// Survivor is the tombstone lookup: the live id a retired one resolves to.
	Survivor(kind model.RedirectKind, id string) (string, bool)
	// Language is a record's language tag, "" when unknown: a work's own field, a
	// series' model.SeriesLanguage over its members (so "" on a tie).
	Language(kind model.RedirectKind, id string) string
	// TranslationOf is the record's translation_of set as stated.
	TranslationOf(kind model.RedirectKind, id string) []string
	// TranslatedBy is every record of the family whose translation_of names id,
	// sorted.
	TranslatedBy(kind model.RedirectKind, id string) []string
	// Ordering and OrderingOf are a series' two ordering members, "" when unstated.
	Ordering(series string) string
	OrderingOf(series string) string
	// Variants is every series whose ordering_of names series, sorted.
	Variants(series string) []string
	// Members is the work ids a series lists, in its own order.
	Members(series string) []string
}

// LinkFaultCode names which link rule a fault breaks.
type LinkFaultCode string

// The link rules. Every one is a PROBLEM except where LinkFault.Severity says
// otherwise: a series translation whose two sides DERIVE one language, and an
// ordering variant listing a work its primary does not, are advisories.
const (
	// LinkDead: the link names no live id of its family, and no tombstone either.
	LinkDead LinkFaultCode = "dead"
	// LinkRetired: the link names a retired id; Survivor is the one it resolves to.
	LinkRetired LinkFaultCode = "retired"
	// LinkSelf: the link names the record stating it.
	LinkSelf LinkFaultCode = "self"
	// LinkSameLanguage: a translation and its original share a primary language.
	LinkSameLanguage LinkFaultCode = "same-language"
	// LinkChain: translation_of names a record that is itself a translation
	// (Others: that record's own originals).
	LinkChain LinkFaultCode = "chain"
	// LinkOneHop: ordering_of names a series that is itself a variant (Others: the
	// primary it names).
	LinkOneHop LinkFaultCode = "one-hop"
	// LinkDuplicateOrdering: two members of one ordering family state one ordering
	// (To: the family's primary; Others: the earlier member stating it).
	LinkDuplicateOrdering LinkFaultCode = "duplicate-ordering"
	// LinkUnsorted: a translation_of set is not in ascending order (Others: the
	// element To comes after).
	LinkUnsorted LinkFaultCode = "unsorted"
	// LinkNotSubset: an ordering variant lists works its primary does not (Works).
	LinkNotSubset LinkFaultCode = "not-subset"
)

// LinkSeverity is whether a fault fails a tree or is only worth a look.
type LinkSeverity int

const (
	// LinkProblem fails a load, refuses a repair proposal and refuses a correction.
	LinkProblem LinkSeverity = iota
	// LinkAdvisory is an advisory in a load and never refuses a repair proposal.
	LinkAdvisory
)

// LinkFault is one broken link rule. From is the record stating the link the
// fault is about - which is also the record a load reports it against - and every
// id but Works is in Kind's namespace.
type LinkFault struct {
	Code     LinkFaultCode
	Severity LinkSeverity
	Kind     model.RedirectKind
	From     string
	// Field is the member the fault is about: FieldTranslationOf, FieldOrderingOf,
	// or FieldOrdering for a duplicate ordering.
	Field string
	// To is the id the link names; for a family fault (duplicate ordering, not a
	// subset) the family's primary.
	To string
	// Survivor is LinkRetired's live id; Language is LinkSameLanguage's shared
	// primary subtag; Ordering is LinkDuplicateOrdering's ordering.
	Survivor string
	Language string
	Ordering string
	// Others are the further ids of the family the fault names (see each code).
	Others []string
	// Works are LinkNotSubset's work ids, sorted.
	Works []string
}

// Mentions reports whether the fault names id as a record of its family.
func (f LinkFault) Mentions(id string) bool {
	return f.From == id || f.To == id || slices.Contains(f.Others, id)
}

// LinkFaults is every fault of the link rules that involves the record id of the
// family kind: the faults on the links it states, and the faults on links OTHER
// records state that name it - a translation of it, a variant of it, a fellow
// member of its ordering family. It is the question a writer asks of a record it
// changed, so a change that breaks a rule from EITHER end (a work gaining
// translation_of while something already translates it) is seen.
//
// Faults on links id states come first, in the order the record states them; the
// others follow in slug order of the record stating them.
func LinkFaults(v LinkView, kind model.RedirectKind, id string) []LinkFault {
	out := ownLinkFaults(v, kind, id)
	for _, other := range linkNeighbours(v, kind, id) {
		for _, f := range ownLinkFaults(v, kind, other) {
			if f.Mentions(id) {
				out = append(out, f)
			}
		}
	}
	return out
}

// linkNeighbours is every OTHER record whose own links can name id, sorted.
func linkNeighbours(v LinkView, kind model.RedirectKind, id string) []string {
	set := map[string]bool{}
	for _, src := range v.TranslatedBy(kind, id) {
		set[src] = true
	}
	if kind == model.RedirectSeries {
		for _, vr := range v.Variants(id) {
			set[vr] = true
		}
		if of := v.OrderingOf(id); of != "" && of != id && v.Holds(kind, of) {
			set[of] = true
			for _, vr := range v.Variants(of) {
				set[vr] = true
			}
		}
	}
	delete(set, id)
	return slices.Sorted(maps.Keys(set))
}

// ownLinkFaults is the faults on the links record id itself states - the grain a
// load reports at, since every record is judged in turn.
func ownLinkFaults(v LinkView, kind model.RedirectKind, id string) []LinkFault {
	out := translationFaults(v, kind, id)
	if kind == model.RedirectSeries {
		out = append(out, orderingFaults(v, id)...)
	}
	return out
}

// translationFaults judges one record's translation_of: every target a LIVE id of
// the record's own family (a retired one named with its survivor), never the
// record itself, in another language, not itself a translation (no chains), and
// the set in ascending order (the canonical byte-form; uniqueness is the schema's
// uniqueItems). A language that is unknown on either side - a series tie, most
// often - is not judged (model.SameLanguage).
func translationFaults(v LinkView, kind model.RedirectKind, id string) []LinkFault {
	targets := v.TranslationOf(kind, id)
	if len(targets) == 0 {
		return nil
	}
	fault := func(code LinkFaultCode, to string) LinkFault {
		return LinkFault{Code: code, Kind: kind, From: id, Field: FieldTranslationOf, To: to}
	}
	var out []LinkFault
	forEachUnsorted(targets, func(cur, prev string) {
		f := fault(LinkUnsorted, cur)
		f.Others = []string{prev}
		out = append(out, f)
	})
	lang, langRead := "", false
	for _, target := range targets {
		if target == id {
			out = append(out, fault(LinkSelf, target))
			continue
		}
		if !v.Holds(kind, target) {
			out = append(out, deadLink(v, fault(LinkDead, target)))
			continue
		}
		if originals := v.TranslationOf(kind, target); len(originals) > 0 {
			f := fault(LinkChain, target)
			f.Others = slices.Clone(originals)
			out = append(out, f)
		}
		if !langRead {
			lang, langRead = v.Language(kind, id), true
		}
		if targetLang := v.Language(kind, target); model.SameLanguage(lang, targetLang) {
			f := fault(LinkSameLanguage, target)
			f.Language = model.PrimarySubtag(lang)
			// A series' language is DERIVED from its members, so two sides agreeing
			// says a member is misfiled rather than that the link is false - and a
			// sync-bot volume must never turn a link it did not write red.
			if kind == model.RedirectSeries {
				f.Severity = LinkAdvisory
			}
			out = append(out, f)
		}
	}
	return out
}

// deadLink completes a dead-link fault: retired (naming the survivor the link
// should point at instead) or simply not held.
func deadLink(v LinkView, f LinkFault) LinkFault {
	if to, retired := v.Survivor(f.Kind, f.To); retired {
		f.Code, f.Survivor = LinkRetired, to
	}
	return f
}

// orderingFaults judges one series' ordering links: its ordering_of names a LIVE
// series (a retired one named with its survivor), never itself, and one that is
// not itself a variant (an ordering family is ONE hop deep); within the family it
// then belongs to - the primary and every series whose ordering_of names it - no
// earlier member states the same ordering (a second series in the same order is a
// duplicate to fold rather than a view); and, as an ADVISORY, a variant listing a
// work its primary does not (legitimate for a chronological order holding a
// prequel novella the publication order never numbered).
//
// Family order is the primary first, then its variants in slug order, so which
// member of a duplicate pair a fault is on does not depend on catalogue order.
func orderingFaults(v LinkView, id string) []LinkFault {
	kind := model.RedirectSeries
	fault := func(code LinkFaultCode, field, to string) LinkFault {
		return LinkFault{Code: code, Kind: kind, From: id, Field: field, To: to}
	}
	primary := id
	if of := v.OrderingOf(id); of != "" {
		switch {
		case of == id:
			return []LinkFault{fault(LinkSelf, FieldOrderingOf, of)}
		case !v.Holds(kind, of):
			return []LinkFault{deadLink(v, fault(LinkDead, FieldOrderingOf, of))}
		case v.OrderingOf(of) != "":
			f := fault(LinkOneHop, FieldOrderingOf, of)
			f.Others = []string{v.OrderingOf(of)}
			return []LinkFault{f}
		}
		primary = of
	}

	var out []LinkFault
	if ordering := v.Ordering(id); ordering != "" {
		for _, m := range append([]string{primary}, v.Variants(primary)...) {
			if m == id {
				break
			}
			if v.Ordering(m) == ordering {
				f := fault(LinkDuplicateOrdering, FieldOrdering, primary)
				f.Others, f.Ordering = []string{m}, ordering
				out = append(out, f)
				break
			}
		}
	}
	if primary != id {
		inPrimary := idSet(v.Members(primary), func(w string) string { return w })
		if extra := extraOf(idSet(v.Members(id), func(w string) string { return w }), inPrimary); len(extra) > 0 {
			f := fault(LinkNotSubset, FieldOrderingOf, primary)
			f.Severity, f.Works = LinkAdvisory, extra
			out = append(out, f)
		}
	}
	return out
}

// forEachUnsorted calls report for every element of a list that sorts before the
// one ahead of it - the adjacent-pair test every "must be sorted" rule makes.
func forEachUnsorted(xs []string, report func(cur, prev string)) {
	for i := 1; i < len(xs); i++ {
		if xs[i] < xs[i-1] {
			report(xs[i], xs[i-1])
		}
	}
}

// NewLinkView is the LinkView over a catalogue held as id maps (first record wins
// on a duplicate id, as every whole-catalogue reader keys it) and its tombstone
// table. The inverse links are indexed once, at construction; a series' derived
// language is computed at most once per series for the life of the view, since a
// popular original is otherwise re-walked once per translation of it.
func NewLinkView(works map[string]*model.Work, series map[string]*model.Series, reds model.Redirects) LinkView {
	v := &catalogLinkView{
		works: works, series: series, reds: reds,
		translatedBy: map[model.RedirectKind]map[string][]string{
			model.RedirectWorks: {}, model.RedirectSeries: {},
		},
		variants:   map[string][]string{},
		seriesLang: map[string]string{},
	}
	for id, w := range works {
		for _, to := range w.TranslationOf {
			v.translatedBy[model.RedirectWorks][to] = append(v.translatedBy[model.RedirectWorks][to], id)
		}
	}
	for id, s := range series {
		for _, to := range s.TranslationOf {
			v.translatedBy[model.RedirectSeries][to] = append(v.translatedBy[model.RedirectSeries][to], id)
		}
		if s.OrderingOf != "" {
			v.variants[s.OrderingOf] = append(v.variants[s.OrderingOf], id)
		}
	}
	for _, inv := range v.translatedBy {
		for _, ids := range inv {
			slices.Sort(ids)
		}
	}
	for _, ids := range v.variants {
		slices.Sort(ids)
	}
	return v
}

type catalogLinkView struct {
	works        map[string]*model.Work
	series       map[string]*model.Series
	reds         model.Redirects
	translatedBy map[model.RedirectKind]map[string][]string
	variants     map[string][]string
	seriesLang   map[string]string
}

func (v *catalogLinkView) Holds(kind model.RedirectKind, id string) bool {
	if kind == model.RedirectWorks {
		return v.works[id] != nil
	}
	return v.series[id] != nil
}

func (v *catalogLinkView) Survivor(kind model.RedirectKind, id string) (string, bool) {
	return v.reds.Survivor(kind, id)
}

func (v *catalogLinkView) Language(kind model.RedirectKind, id string) string {
	if kind == model.RedirectWorks {
		if w := v.works[id]; w != nil {
			return w.Language
		}
		return ""
	}
	if lang, ok := v.seriesLang[id]; ok {
		return lang
	}
	lang := ""
	if s := v.series[id]; s != nil {
		lang = model.SeriesLanguageOf(s.Works, v.works)
	}
	v.seriesLang[id] = lang
	return lang
}

func (v *catalogLinkView) TranslationOf(kind model.RedirectKind, id string) []string {
	if kind == model.RedirectWorks {
		if w := v.works[id]; w != nil {
			return w.TranslationOf
		}
		return nil
	}
	if s := v.series[id]; s != nil {
		return s.TranslationOf
	}
	return nil
}

func (v *catalogLinkView) TranslatedBy(kind model.RedirectKind, id string) []string {
	return v.translatedBy[kind][id]
}

func (v *catalogLinkView) Ordering(id string) string {
	if s := v.series[id]; s != nil {
		return s.Ordering
	}
	return ""
}

func (v *catalogLinkView) OrderingOf(id string) string {
	if s := v.series[id]; s != nil {
		return s.OrderingOf
	}
	return ""
}

func (v *catalogLinkView) Variants(id string) []string { return v.variants[id] }

func (v *catalogLinkView) Members(id string) []string {
	s := v.series[id]
	if s == nil {
		return nil
	}
	out := make([]string, len(s.Works))
	for i, sw := range s.Works {
		out[i] = sw.Work
	}
	return out
}

// checkLanguageLinks is the load's door onto the link rules: every record that
// states a link is judged on the links it states (ownLinkFaults - each record in
// turn, so a fault is reported once, against the record stating the link), and
// each fault is rendered as a problem or an advisory against that record's own
// pack entry.
//
// It is profile-gated per LoadProfile's skip rule, arm by arm: the work arm needs
// the works family and the series arm the series family. Under ProfileCommunity
// neither is held, so nothing runs; under ProfileCore (and so on the core side of
// LoadComposed) both are, so everything does. A series' LANGUAGE is derived from
// its members' works, so under a profile without works it is unknown and the
// series language rule stands down, as it does on any unknown.
//
// works is checkIntegrity's first-record-wins id map, reused rather than rebuilt,
// and a record whose id is held twice is judged once, as the record that id
// resolves to (the duplicate is its own problem). The view is built only when some
// record carries a link, so a catalogue with none of these fields - every tree
// until Phase 3's data lands - pays one pass over the works and the series.
func checkLanguageLinks(profile pack.Profile, cat *model.Catalog, works map[string]*model.Work, idx *pathIndex, add, warn addFunc) {
	var linkedWorks []*model.Work
	if profile.Has(pack.FamilyWorks) {
		for _, w := range cat.Works {
			if len(w.TranslationOf) > 0 && works[w.ID] == w {
				linkedWorks = append(linkedWorks, w)
			}
		}
	}
	var series map[string]*model.Series
	var linkedSeries []*model.Series
	if profile.Has(pack.FamilySeries) {
		series = map[string]*model.Series{}
		for _, s := range cat.Series {
			if _, dup := series[s.ID]; dup {
				continue
			}
			series[s.ID] = s
			if len(s.TranslationOf) > 0 || s.OrderingOf != "" {
				linkedSeries = append(linkedSeries, s)
			}
		}
	}
	if len(linkedWorks) == 0 && len(linkedSeries) == 0 {
		return
	}
	v := NewLinkView(works, series, cat.Redirects)
	for _, w := range linkedWorks {
		reportLinkFaults(ownLinkFaults(v, model.RedirectWorks, w.ID), idx.work[w], add, warn)
	}
	for _, s := range linkedSeries {
		reportLinkFaults(ownLinkFaults(v, model.RedirectSeries, s.ID), idx.series[s], add, warn)
	}
}

// reportLinkFaults renders a record's link faults in the load's words.
func reportLinkFaults(faults []LinkFault, rel string, add, warn addFunc) {
	for _, f := range faults {
		report := add
		if f.Severity == LinkAdvisory {
			report = warn
		}
		report(rel, "%s", linkFaultMessage(f))
	}
}

// linkFaultMessage is the load's sentence for a fault.
func linkFaultMessage(f LinkFault) string {
	noun := familyNoun(f.Kind)
	switch f.Code {
	case LinkUnsorted:
		return fmt.Sprintf("%s must be sorted: %q comes after %q", f.Field, f.To, f.Others[0])
	case LinkSelf:
		if f.Field == FieldOrderingOf {
			return "ordering_of names the series itself: a variant names its franchise's PRIMARY ordering, " +
				"which is a different series"
		}
		return fmt.Sprintf("translation_of names the record itself: a translation links to its ORIGINAL, "+
			"which is a different %s record", noun)
	case LinkDead:
		return fmt.Sprintf("%s %q is no live %s id", f.Field, f.To, noun)
	case LinkRetired:
		return fmt.Sprintf("%s %q is retired - point at %q", f.Field, f.To, f.Survivor)
	case LinkChain:
		return fmt.Sprintf("translation_of %q is itself a translation (of %s): link to the original directly - "+
			"translations do not chain", f.To, quotedList(f.Others))
	case LinkSameLanguage:
		if f.Severity == LinkAdvisory {
			return fmt.Sprintf("translation_of %q derives the same language (%q) as this series from its members: %s",
				f.To, f.Language, seriesSameLanguage)
		}
		return fmt.Sprintf("translation_of %q is in the same language (%q) as this %s: a translation is into "+
			"another language", f.To, f.Language, noun)
	case LinkOneHop:
		return fmt.Sprintf("ordering_of %q is itself a variant (of %q): name the primary directly - an ordering "+
			"family is one hop deep", f.To, f.Others[0])
	case LinkDuplicateOrdering:
		return fmt.Sprintf("series %q and %q both state the %q ordering of the family whose primary is %q: "+
			"a second series in the same order is a duplicate to fold, not a view", f.Others[0], f.From, f.Ordering, f.To)
	case LinkNotSubset:
		return fmt.Sprintf("ordering variant lists %s, which its primary ordering %q does not: %s",
			quotedList(f.Works), f.To, orderingNotSubset)
	}
	return fmt.Sprintf("%s %q breaks the link rule %q", f.Field, f.To, f.Code)
}

// orderingNotSubset is the tail the ordering-variant-not-subset advisory ends
// in, and the marker advisoryMarkers files it under.
const orderingNotSubset = "a variant listing works its primary does not (legitimate for, say, " +
	"a chronological order holding a prequel novella)"

// seriesSameLanguage is the tail the series-translation-same-language advisory
// ends in, and the marker advisoryMarkers files it under.
const seriesSameLanguage = "the link stands, so a member is probably filed under the wrong language " +
	"(a series' language is its members' majority)"

// familyNoun is the singular noun a message names a record of kind by.
func familyNoun(kind model.RedirectKind) string {
	if kind == model.RedirectWorks {
		return "work"
	}
	return string(kind)
}
