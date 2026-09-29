package repair

import (
	"slices"

	"github.com/kodestar/audiosilo-meta/internal/rawentry"
	"github.com/kodestar/audiosilo-meta/pkg/check"
	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// links.go keeps the cross-record LINKS pointing at live records when a merge retires
// a slug: translation_of (a work onto the work it translates, a series onto the series
// it translates) and ordering_of (a series' variant reading order onto its primary).
//
// A merge-works or merge-series is the one pass here that deletes records, and unlike a
// series membership (which a merge already re-points) these links sit on records that
// are NOT part of the merge: a French edition naming the English work a wave is about
// to fold, a chronological list naming the publication-order series being retired. A
// retired slug still RESOLVES through its tombstone, but pkg/check holds every core
// reference to a live id, so a merge that left one pointing at the loser would fail the
// post-write gate with the tree already written.
//
// The file has two halves. The RE-POINTING is repair's own (relinkTranslations,
// relinkOrderings): every link naming a loser is moved onto the survivor inside the txn,
// and every value the merge could not keep is named through txn.noteLost. The RULES the
// result is then held to are not repair's: refuseLinkFaults asks pkg/check's one rule of
// record (check.LinkFaults) over the STAGED merge, for every record the merge touched, and
// refuses on any PROBLEM it reports - a chain, a variant's second hop, two series of one
// ordering in a family, a translation in its original's language. An advisory (a series
// translation whose sides derive one language, a variant listing works its primary does
// not) never refuses a proposal, exactly as it never fails a load. The post-write pkg/check
// load enforces the same rules again; it is the backstop, never the mechanism.
//
// WHO LINKS TO X is the question every merge asks, and a link lives on the record that
// states it, so the plan carries the INVERSE index (linkIndex) - built once at load and
// kept current as proposals commit, for the reason seriesMembers is: a later proposal
// must see the links an earlier one re-pointed.

// fieldTranslationOf, fieldOrderingOf and fieldOrdering are the link members, spelled
// once, by the rule of record.
const (
	fieldTranslationOf = check.FieldTranslationOf
	fieldOrderingOf    = check.FieldOrderingOf
	fieldOrdering      = check.FieldOrdering
)

// linkIndex is one family's links, forward and inverse. The forward maps hold only the
// records that state a link, so a tree carrying none (every tree today) costs two empty
// maps.
type linkIndex struct {
	translationOf map[string][]string
	translatedBy  map[string]map[string]bool
	orderingOf    map[string]string
	variants      map[string]map[string]bool
}

func newLinkIndex() *linkIndex {
	return &linkIndex{
		translationOf: map[string][]string{},
		translatedBy:  map[string]map[string]bool{},
		orderingOf:    map[string]string{},
		variants:      map[string]map[string]bool{},
	}
}

// set records what one record states NOW (nothing, for a deleted one), retracting what it
// stated before from the inverse side.
func (ix *linkIndex) set(slug string, translationOf []string, orderingOf string) {
	for _, to := range ix.translationOf[slug] {
		delete(ix.translatedBy[to], slug)
	}
	delete(ix.translationOf, slug)
	if old, ok := ix.orderingOf[slug]; ok {
		delete(ix.variants[old], slug)
		delete(ix.orderingOf, slug)
	}
	if len(translationOf) > 0 {
		ix.translationOf[slug] = slices.Clone(translationOf)
		for _, to := range translationOf {
			if ix.translatedBy[to] == nil {
				ix.translatedBy[to] = map[string]bool{}
			}
			ix.translatedBy[to][slug] = true
		}
	}
	if orderingOf != "" {
		ix.orderingOf[slug] = orderingOf
		if ix.variants[orderingOf] == nil {
			ix.variants[orderingOf] = map[string]bool{}
		}
		ix.variants[orderingOf][slug] = true
	}
}

// setEntry re-indexes one staged entry; nil is a deletion.
func (ix *linkIndex) setEntry(slug string, e entry) {
	if e == nil {
		ix.set(slug, nil, "")
		return
	}
	ix.set(slug, e.Strs(fieldTranslationOf), e.Str(fieldOrderingOf))
}

// empty reports whether the family states no link at all.
func (ix *linkIndex) empty() bool { return len(ix.translationOf) == 0 && len(ix.orderingOf) == 0 }

// linkers returns the records OUTSIDE the cluster whose link (the inverse map given) names
// any cluster member, sorted.
func linkers(inverse map[string]map[string]bool, retiring map[string]bool, members []string) []string {
	set := map[string]bool{}
	for _, m := range members {
		for src := range inverse[m] {
			if !retiring[src] {
				set[src] = true
			}
		}
	}
	return rawentry.SortedKeys(set)
}

// newLinkIndexes builds the two families' indexes from the loaded catalogue (set records
// nothing for a record stating no link).
func newLinkIndexes(cat *model.Catalog) (works, series *linkIndex) {
	works, series = newLinkIndex(), newLinkIndex()
	for _, w := range cat.Works {
		works.set(w.ID, w.TranslationOf, "")
	}
	for _, s := range cat.Series {
		series.set(s.ID, s.TranslationOf, s.OrderingOf)
	}
	return works, series
}

// linkedMemberLanguages is the primary language subtag of every work a series on EITHER
// end of a translation link lists, read off the loaded catalogue - so judging a linked
// series' derived language never parses a pack for a member the run has not touched.
// Empty (and free) on a tree carrying no series translation.
func linkedMemberLanguages(cat *model.Catalog, series *linkIndex) map[string]string {
	if len(series.translationOf) == 0 {
		return map[string]string{}
	}
	want := map[string]bool{}
	for _, s := range cat.Series {
		if _, states := series.translationOf[s.ID]; states || len(series.translatedBy[s.ID]) > 0 {
			for _, sw := range s.Works {
				want[sw.Work] = true
			}
		}
	}
	out := make(map[string]string, len(want))
	for _, w := range cat.Works {
		if want[w.ID] {
			if _, seen := out[w.ID]; !seen {
				out[w.ID] = model.PrimarySubtag(w.Language)
			}
		}
	}
	return out
}

// linksFor is the plan's index for a family that carries links.
func (p *plan) linksFor(f pack.Family) *linkIndex {
	switch f {
	case pack.FamilyWorks:
		return p.workLinks
	case pack.FamilySeries:
		return p.seriesLinks
	}
	panic("repair: no link index for family " + f.Root())
}

// reindexLinks folds a committed stage's writes into the family's link index. The puts
// and the deletes are disjoint (stage.put and stage.remove each clear the other), so the
// order they are applied in is immaterial.
func (s *stage) reindexLinks(ix *linkIndex) {
	for slug, e := range s.puts {
		ix.setEntry(slug, e)
	}
	for slug := range s.dels {
		ix.setEntry(slug, nil)
	}
}

// linkFamily is the one mapping from a pack family to what the link rules call it.
func linkFamily(f pack.Family) (kind model.RedirectKind, noun string) {
	if f == pack.FamilySeries {
		return model.RedirectSeries, "series"
	}
	return model.RedirectWorks, "work"
}

// relinkTranslations is the translation_of half of a merge, for either family (a work
// merge re-points works, a series merge series; the two id namespaces never link across).
//
//   - the survivor's own set and every loser's are UNIONED onto the survivor - a set-valued
//     fact, so the merge loses none of it - with every retiring slug read as the survivor;
//   - every record OUTSIDE the cluster naming a loser is re-pointed onto the survivor,
//     deduped and re-sorted;
//   - a link that would then name the record it sits on is dropped and noted.
//
// Whether the result is sound - the survivor now both a translation and an original, or
// naming an original that is itself a translation - is refuseLinkFaults' question, asked
// once the whole merge is staged.
func (t *txn) relinkTranslations(f pack.Family, target string, merged entry, losers []string, loserEntries []entry, retiring map[string]bool) error {
	_, noun := linkFamily(f)
	t.noteLost(foldTranslationOf(merged, nil, retiring, target, target), target)
	for i, slug := range losers {
		t.noteLost(foldTranslationOf(merged, loserEntries[i], retiring, target, target), slug)
	}

	st := t.stageFor(f)
	ix := t.p.linksFor(f)
	for _, src := range linkers(ix.translatedBy, retiring, cluster(target, losers)) {
		if !slices.ContainsFunc(ix.translationOf[src], func(s string) bool { return retiring[s] }) {
			continue // it names the survivor already, and nothing else in the cluster
		}
		e, ok, err := st.get(src)
		if err != nil {
			return err
		}
		if !ok {
			continue // a dangling link is pkg/check's to report; Run refuses a tree it reports on
		}
		next := e.Clone()
		t.noteLost(foldTranslationOf(next, nil, retiring, target, src), src)
		st.put(src, next)
		t.note("re-pointed translation_of of %s %s onto %s", noun, src, target)
	}
	return nil
}

// foldTranslationOf rewrites into's translation_of as its own set unioned with from's
// (from nil: its own set alone), every retiring slug read as target, deduped and in the
// ascending order pkg/check requires of a set. It returns the links it DROPPED because
// they would then name self, the record the set sits on - every one of them, from either
// set, for the note naming them.
//
// An unchanged record is left byte for byte: a set that reads the same is not re-set, and
// an absent one stays absent.
func foldTranslationOf(into, from entry, retiring map[string]bool, target, self string) []mergedFacts {
	own := into.Strs(fieldTranslationOf)
	all := slices.Clone(own)
	if from != nil {
		all = append(all, from.Strs(fieldTranslationOf)...)
	}
	var out, dropped []string
	for _, s := range all {
		r := s
		if retiring[s] {
			r = target
		}
		if r == self {
			dropped = append(dropped, s)
		} else {
			out = append(out, r)
		}
	}
	slices.Sort(out)
	out = slices.Compact(out)
	if !slices.Equal(out, own) {
		rawentry.SetListOrDrop(into, fieldTranslationOf, out)
	}
	lost := make([]mergedFacts, 0, len(dropped))
	for _, s := range slices.Compact(slices.Sorted(slices.Values(dropped))) {
		lost = append(lost, mergedFacts{field: fieldTranslationOf, kept: joinList(out), dropped: s})
	}
	return lost
}

// relinkOrderings is the ordering_of half of a merge-series. With S the survivor and L a
// loser:
//
//   - (b) a series OUTSIDE the cluster whose ordering_of names L is re-pointed onto S;
//   - (c) L.ordering_of naming S (a variant folded into its own primary): the link goes
//     with L, and is noted;
//   - (d) L and S variants of the same primary: S's link is kept, nothing is lost;
//   - the `ordering` scalar is always the survivor's, and a differing loser value is
//     noted as lost - it described the loser's position list, which does not survive.
//
// Two shapes are refused here, before anything is re-pointed, because they are about
// what the MERGE does rather than about the links it leaves: S a variant of a loser (the
// fold would promote the variant to primary - merge the other way), and (e) L a variant
// of a primary S is not a variant of (the fold would move a variant out of its family,
// dropping a link it cannot note as a set union). Everything the re-point then leaves -
// a variant re-pointed onto a survivor that is itself a variant (a second hop), a family
// holding two series of one ordering - is refuseLinkFaults' to judge, by pkg/check's
// rules over the staged merge.
func (t *txn) relinkOrderings(target string, merged entry, losers []string, loserEntries []entry, retiring map[string]bool) error {
	sOf := merged.Str(fieldOrderingOf)
	if retiring[sOf] {
		return refusef(CatOrderingLink,
			"series %s is a variant ordering of %s, which this merge retires: folding a primary into its own variant "+
				"would promote the variant - merge the other way, or by hand", target, sOf)
	}
	for i, slug := range losers {
		lOf := loserEntries[i].Str(fieldOrderingOf)
		if lOf != "" && !retiring[lOf] && lOf != target && lOf != sOf {
			return refusef(CatOrderingLink,
				"series %s is a variant ordering of %s but %s is not: the merge would move a variant out of its ordering family",
				slug, lOf, target)
		}
		t.noteLost(mergeOrderingFields(merged, loserEntries[i], retiring, target), slug)
	}

	st := t.series
	for _, src := range linkers(t.p.seriesLinks.variants, retiring, losers) {
		e, ok, err := st.get(src)
		if err != nil {
			return err
		}
		if !ok {
			continue // a dangling link is pkg/check's to report; Run refuses a tree it reports on
		}
		next := e.Clone()
		next.Set(fieldOrderingOf, target)
		st.put(src, next)
		t.note("re-pointed ordering_of of series %s from %s onto %s", src, e.Str(fieldOrderingOf), target)
	}
	return nil
}

// mergeOrderingFields reports what a merge-series chose away of a loser's ordering: its
// `ordering` scalar where it differs from the survivor's (kept always - see
// relinkOrderings), and its ordering_of where that named the survivor or another retiring
// series (case (c): the link goes with the variant folded into its primary). A link equal
// to the survivor's own (case (d)) is the same fact and loses nothing. The survivor's
// entry is not changed: its own two members are what it keeps.
func mergeOrderingFields(merged, loser entry, retiring map[string]bool, target string) []mergedFacts {
	var lost []mergedFacts
	if o := loser.Str(fieldOrdering); o != "" && o != merged.Str(fieldOrdering) {
		lost = append(lost, mergedFacts{field: fieldOrdering, kept: merged.Str(fieldOrdering), dropped: o})
	}
	if of := loser.Str(fieldOrderingOf); of != "" && (of == target || retiring[of]) {
		lost = append(lost, mergedFacts{field: fieldOrderingOf, kept: merged.Str(fieldOrderingOf), dropped: of})
	}
	return lost
}

// refuseLinkFaults holds a staged merge to pkg/check's link rules: every staged work and
// series is asked check.LinkFaults over the STAGED view - its own links and the links
// naming it - and the first PROBLEM refuses the proposal, under translation-link-conflict
// for a translation_of fault and ordering-link-conflict for an ordering one. An advisory
// never refuses. target is the merge's survivor, which the refusal's wording turns on.
//
// It is asked at the end of each merge planner, once everything is staged. A tree stating
// no link at all - every tree today - returns before building anything.
func (t *txn) refuseLinkFaults(target string) error {
	if t.p.workLinks.empty() && t.p.seriesLinks.empty() {
		return nil
	}
	v := &stagedLinkView{t: t, seriesLang: map[string]string{}}
	for _, f := range []pack.Family{pack.FamilyWorks, pack.FamilySeries} {
		kind, noun := linkFamily(f)
		for _, slug := range rawentry.SortedKeys(t.stageFor(f).puts) {
			faults := check.LinkFaults(v, kind, slug)
			if v.err != nil {
				return v.err
			}
			for _, fault := range faults {
				if fault.Severity == check.LinkProblem {
					return linkRefusal(fault, noun, target)
				}
			}
		}
	}
	return nil
}

// linkRefusal words a link fault as a repair refusal.
func linkRefusal(f check.LinkFault, noun, target string) error {
	cat := CatOrderingLink
	if f.Field == fieldTranslationOf {
		cat = CatTranslationLink
	}
	switch f.Code {
	case check.LinkChain:
		if f.To == target {
			return refusef(cat, "after the merge %s %s would state translation_of [%s] while %s names it as its original: "+
				"a translation is one hop from its original, so which link is wrong has to be decided by hand",
				noun, f.To, joinList(f.Others), f.From)
		}
		return refusef(cat, "after the merge %s %s would name %s as its original, and %s is itself a translation of [%s]: "+
			"a translation is one hop from its original", noun, f.From, f.To, f.To, joinList(f.Others))
	case check.LinkSameLanguage:
		return refusef(cat, "after this change %s %s would be in %q, the same language as %s %s, which it names in translation_of: "+
			"a translation is in a different language from its original, so the link or the change needs a human",
			noun, f.From, f.Language, noun, f.To)
	case check.LinkOneHop:
		return refusef(cat, "after the merge series %s would name %s as its primary ordering, and re-pointing onto it would make "+
			"a chain: %s is itself a variant of %s", f.From, f.To, f.To, f.Others[0])
	case check.LinkDuplicateOrdering:
		return refusef(cat, "after the merge series %s and %s would both state the %s ordering of %s's family: a second series "+
			"in one order is a duplicate to fold by hand, not a view", f.Others[0], f.From, f.Ordering, f.To)
	}
	return refusef(cat, "after the merge %s %s would break the %s rule on its %s naming %s", noun, f.From, f.Code, f.Field, f.To)
}

// stagedLinkView is the plan as one txn would leave it, as the link rules read it: every
// record through the txn's stages (so a re-point this proposal made is judged as it will
// be written), the inverse links through the plan's index corrected for what the txn
// staged, and the tombstones the txn is about to record. A read error is latched in err
// and every later answer is empty; the caller checks it.
type stagedLinkView struct {
	t          *txn
	err        error
	seriesLang map[string]string // derived language, memoized per judgement
}

func (v *stagedLinkView) stage(kind model.RedirectKind) *stage {
	if kind == model.RedirectSeries {
		return v.t.series
	}
	return v.t.works
}

func (v *stagedLinkView) entry(kind model.RedirectKind, id string) entry {
	if v.err != nil {
		return nil
	}
	e, ok, err := v.stage(kind).get(id)
	if err != nil {
		v.err = err
		return nil
	}
	if !ok {
		return nil
	}
	return e
}

func (v *stagedLinkView) Holds(kind model.RedirectKind, id string) bool {
	return v.entry(kind, id) != nil
}

func (v *stagedLinkView) Survivor(kind model.RedirectKind, id string) (string, bool) {
	for _, tb := range v.t.tombs {
		if tb.kind == kind && tb.from == id {
			return tb.to, true
		}
	}
	return v.t.p.redirects.Survivor(kind, id)
}

func (v *stagedLinkView) Language(kind model.RedirectKind, id string) string {
	if kind == model.RedirectWorks {
		return v.workLanguage(id)
	}
	if lang, ok := v.seriesLang[id]; ok {
		return lang
	}
	lang := ""
	if e := v.entry(kind, id); e != nil {
		lang = model.SeriesLanguage(e.SeriesWorks(), v.workLanguage)
	}
	v.seriesLang[id] = lang
	return lang
}

// workLanguage is a work's primary language subtag over the staged view. A work neither
// this txn nor an earlier proposal has touched reads from the load's member-language map
// when it is there, so deriving a linked series' language parses no pack for it.
func (v *stagedLinkView) workLanguage(id string) string {
	st, p := v.t.works, v.t.p
	if _, staged := st.puts[id]; !staged && !st.dels[id] && !p.works.dirty[id] && !p.works.gone[id] {
		if lang, ok := p.memberLang[id]; ok {
			return lang
		}
	}
	if e := v.entry(model.RedirectWorks, id); e != nil {
		return model.PrimarySubtag(e.Str("language"))
	}
	return ""
}

func (v *stagedLinkView) TranslationOf(kind model.RedirectKind, id string) []string {
	if e := v.entry(kind, id); e != nil {
		return e.Strs(fieldTranslationOf)
	}
	return nil
}

func (v *stagedLinkView) TranslatedBy(kind model.RedirectKind, id string) []string {
	ix := v.t.p.workLinks
	if kind == model.RedirectSeries {
		ix = v.t.p.seriesLinks
	}
	return v.inverse(v.stage(kind), ix.translatedBy[id], func(e entry) bool {
		return slices.Contains(e.Strs(fieldTranslationOf), id)
	})
}

func (v *stagedLinkView) Ordering(id string) string {
	if e := v.entry(model.RedirectSeries, id); e != nil {
		return e.Str(fieldOrdering)
	}
	return ""
}

func (v *stagedLinkView) OrderingOf(id string) string {
	if e := v.entry(model.RedirectSeries, id); e != nil {
		return e.Str(fieldOrderingOf)
	}
	return ""
}

func (v *stagedLinkView) Variants(id string) []string {
	return v.inverse(v.t.series, v.t.p.seriesLinks.variants[id], func(e entry) bool {
		return e.Str(fieldOrderingOf) == id
	})
}

// inverse is an index's inverse set corrected for the txn: a record the txn staged is
// judged by its staged entry (names reports whether it names the id), one it deleted
// names nothing, and every other record answers as the plan's index says. Sorted.
func (v *stagedLinkView) inverse(st *stage, indexed map[string]bool, names func(entry) bool) []string {
	set := map[string]bool{}
	for src := range indexed {
		if _, staged := st.puts[src]; !staged && !st.dels[src] {
			set[src] = true
		}
	}
	for src, e := range st.puts {
		if names(e) {
			set[src] = true
		}
	}
	return rawentry.SortedKeys(set)
}

func (v *stagedLinkView) Members(id string) []string {
	e := v.entry(model.RedirectSeries, id)
	if e == nil {
		return nil
	}
	sws := e.SeriesWorks()
	out := make([]string, len(sws))
	for i, sw := range sws {
		out[i] = sw.Work
	}
	return out
}
