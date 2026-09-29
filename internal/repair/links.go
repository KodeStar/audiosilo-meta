package repair

import (
	"slices"

	"github.com/kodestar/audiosilo-meta/internal/rawentry"
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
// post-write gate with the tree already written. Hence the rule of this file, which is
// the rule of the whole pass: every re-point is planned inside the txn before a byte is
// written, every value the merge could not keep is named through txn.noteLost, and a
// shape no mechanical rule can settle refuses the proposal. The post-write pkg/check
// load enforces the same rules again; it is the backstop, never the mechanism.
//
// WHO LINKS TO X is the question every merge asks, and a link lives on the record that
// states it, so the plan carries the INVERSE index (linkIndex) - built once at load and
// kept current as proposals commit, for the reason seriesMembers is: a later proposal
// must see the links an earlier one re-pointed.

// fieldTranslationOf and fieldOrderingOf are the two link members, and fieldOrdering is
// the scalar a variant's link depends on (the schema's dependentRequired).
const (
	fieldTranslationOf = "translation_of"
	fieldOrderingOf    = "ordering_of"
	fieldOrdering      = "ordering"
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

// newLinkIndexes builds the two families' indexes from the loaded catalogue.
func newLinkIndexes(cat *model.Catalog) (works, series *linkIndex) {
	works, series = newLinkIndex(), newLinkIndex()
	for _, w := range cat.Works {
		if len(w.TranslationOf) > 0 {
			works.set(w.ID, w.TranslationOf, "")
		}
	}
	for _, s := range cat.Series {
		if len(s.TranslationOf) > 0 || s.OrderingOf != "" {
			series.set(s.ID, s.TranslationOf, s.OrderingOf)
		}
	}
	return works, series
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

// reindexLinks folds a committed stage's writes into the family's link index.
func (s *stage) reindexLinks(ix *linkIndex) {
	for _, slug := range rawentry.SortedKeys(s.puts) {
		ix.setEntry(slug, s.puts[slug])
	}
	for _, slug := range rawentry.SortedKeys(s.dels) {
		ix.setEntry(slug, nil)
	}
}

// retiringSet is a merge's retiring slugs as a lookup: every loser, read as the target.
func retiringSet(losers []string) map[string]bool {
	out := make(map[string]bool, len(losers))
	for _, l := range losers {
		out[l] = true
	}
	return out
}

// relinkTranslations is the translation_of half of a merge, for either family (a work
// merge re-points works, a series merge series; the two id namespaces never link across).
//
//   - the survivor's own set and every loser's are UNIONED onto the survivor - a set-valued
//     fact, so the merge loses none of it - with every retiring slug read as the survivor;
//   - every record OUTSIDE the cluster naming a loser is re-pointed onto the survivor,
//     deduped and re-sorted;
//   - a link that would then name the record it sits on is dropped and noted;
//   - a survivor that would end up BOTH carrying translation_of AND being a target of one,
//     or naming a record that itself carries one, is refused: a translation is one hop
//     from its original, and which side of the chain is wrong is a human's call.
func (t *txn) relinkTranslations(f pack.Family, noun, target string, merged entry, losers []string, loserEntries []entry) error {
	retiring := retiringSet(losers)
	t.noteLost(foldTranslationOf(merged, nil, retiring, target, target), target)
	for i, slug := range losers {
		t.noteLost(foldTranslationOf(merged, loserEntries[i], retiring, target, target), slug)
	}

	st := t.stageFor(f)
	ix := t.p.linksFor(f)
	sources := linkers(ix.translatedBy, retiring, cluster(target, losers))
	if final := merged.Strs(fieldTranslationOf); len(final) > 0 {
		if len(sources) > 0 {
			return refusef(CatTranslationLink,
				"after the merge %s %s would state translation_of [%s] while %s names it as its original: a translation "+
					"is one hop from its original, so which link is wrong has to be decided by hand",
				noun, target, joinList(final), joinList(sources))
		}
		for _, to := range final {
			e, ok, err := st.get(to)
			if err != nil {
				return err
			}
			if ok && len(e.Strs(fieldTranslationOf)) > 0 {
				return refusef(CatTranslationLink,
					"after the merge %s %s would name %s as its original, and %s is itself a translation of [%s]: "+
						"a translation is one hop from its original",
					noun, target, to, to, joinList(e.Strs(fieldTranslationOf)))
			}
		}
	}
	for _, src := range sources {
		if !linksAny(ix.translationOf[src], retiring) {
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

// linksAny reports whether a link list names any retiring slug.
func linksAny(list []string, retiring map[string]bool) bool {
	for _, s := range list {
		if retiring[s] {
			return true
		}
	}
	return false
}

// foldTranslationOf rewrites into's translation_of as its own set unioned with from's
// (from nil: its own set alone), every retiring slug read as target, deduped and in the
// ascending order pkg/check requires of a set. It returns the links it DROPPED because
// they would then name self, the record the set sits on - for the note naming them.
//
// An unchanged record is left byte for byte: a set that reads the same is not re-set, and
// an absent one stays absent.
func foldTranslationOf(into, from entry, retiring map[string]bool, target, self string) []mergedFacts {
	own := into.Strs(fieldTranslationOf)
	add := own
	if from != nil {
		add = from.Strs(fieldTranslationOf)
	}
	read := func(s string) string {
		if retiring[s] {
			return target
		}
		return s
	}
	var out, dropped []string
	for _, s := range own {
		if r := read(s); r != self {
			out = append(out, r)
		}
	}
	for _, s := range add {
		if r := read(s); r == self {
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
//   - (b) a series OUTSIDE the cluster whose ordering_of names L is re-pointed onto S -
//     refused when S is itself a variant (the re-point would make a two-hop chain), which
//     includes S being a variant of L: folding a primary into its own variant would
//     promote the variant, which is a human's decision;
//   - (c) L.ordering_of naming S (a variant folded into its own primary): the link goes
//     with L, and is noted;
//   - (d) L and S variants of the same primary: S's link is kept, nothing is lost;
//   - (e) L a variant of a primary S is not a variant of (S a primary, or a variant of
//     another): refused - the merge would promote or move a variant out of its family;
//   - the `ordering` scalar is always the survivor's, and a differing loser value is
//     noted as lost - it described the loser's position list, which does not survive.
//
// And the family the survivor heads afterwards must still hold one series per ordering
// (pkg/check's rule), so a merge that would put two variants of one kind under S is
// refused too: that is a duplicate to fold by hand, not a view.
func (t *txn) relinkOrderings(target string, merged entry, losers []string, loserEntries []entry) error {
	retiring := retiringSet(losers)
	ix := t.p.seriesLinks
	sOf := merged.Str(fieldOrderingOf)
	if retiring[sOf] {
		return refusef(CatOrderingLink,
			"series %s is a variant ordering of %s, which this merge retires: folding a primary into its own variant "+
				"would promote the variant - merge the other way, or by hand", target, sOf)
	}
	variants := linkers(ix.variants, retiring, losers)
	if len(variants) > 0 && sOf != "" {
		return refusef(CatOrderingLink,
			"%s name(s) %s as its primary ordering, and re-pointing onto %s would make a chain: %s is itself a variant of %s",
			joinList(variants), joinList(losers), target, target, sOf)
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
	for _, src := range variants {
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
	if sOf != "" || len(variants) == 0 {
		// Nothing joined a family: S stays a variant of one the merge did not touch, or
		// heads its own with the losers gone, and the pre-state held one per ordering.
		return nil
	}
	// The family S heads now: S, its variants outside the cluster, and the ones just
	// re-pointed onto it.
	members := map[string]bool{}
	for v := range ix.variants[target] {
		if !retiring[v] {
			members[v] = true
		}
	}
	for _, v := range variants {
		members[v] = true
	}
	byOrdering := map[string]string{}
	if o := merged.Str(fieldOrdering); o != "" {
		byOrdering[o] = target
	}
	for _, v := range rawentry.SortedKeys(members) {
		e, ok, err := st.get(v)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		o := e.Str(fieldOrdering)
		if o == "" {
			continue
		}
		if prev, dup := byOrdering[o]; dup {
			return refusef(CatOrderingLink,
				"after the merge series %s and %s would both state the %s ordering of %s's family: a second series in one "+
					"order is a duplicate to fold by hand, not a view", prev, v, o, target)
		}
		byOrdering[o] = v
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

// judgeLinkLanguages re-asks pkg/check's translation LANGUAGE rule - a translation
// and its original are never in one primary language - of everything the proposal
// staged, before it commits. A merge moves no link across languages by itself, but
// it can move the LANGUAGE under a link: a series' language is derived from its
// members (model.SeriesLanguage), so a membership fold can flip a linked series'
// majority onto its original's, and a merge-works survivor's language is what its
// re-pointed translations are now judged against. Left to the post-write check,
// either fails the whole wave with the tree already written, so a proposal that
// would break the rule is refused here as CatTranslationLink instead.
//
// Every staged entry of both families is judged against the records it links to
// and the records linking to it, all read through the STAGED view (so a re-point
// this proposal made is judged as it will be written). A side whose language is ""
// - a series tie, or nothing known - is not judged, exactly as pkg/check skips it.
// Only an entry that carries or receives a link costs a read, so a tree without
// links (every tree today) pays nothing.
func (t *txn) judgeLinkLanguages() error {
	if err := t.judgeFamilyLanguages(pack.FamilyWorks, "work", t.workLanguage); err != nil {
		return err
	}
	return t.judgeFamilyLanguages(pack.FamilySeries, "series", t.seriesLanguage)
}

// judgeFamilyLanguages is judgeLinkLanguages for one family. lang answers a slug's
// primary language over the staged view.
func (t *txn) judgeFamilyLanguages(f pack.Family, noun string, lang func(string) (string, error)) error {
	st := t.stageFor(f)
	ix := t.p.linksFor(f)
	for _, slug := range rawentry.SortedKeys(st.puts) {
		type link struct{ from, to string }
		var pairs []link
		for _, to := range st.puts[slug].Strs(fieldTranslationOf) {
			pairs = append(pairs, link{slug, to})
		}
		for _, src := range rawentry.SortedKeys(ix.translatedBy[slug]) {
			// A staged source is judged as a key of its own (its staged links are the
			// ones that count), and a deleted one links to nothing any more.
			if _, staged := st.puts[src]; staged || st.dels[src] {
				continue
			}
			pairs = append(pairs, link{src, slug})
		}
		for _, l := range pairs {
			a, err := lang(l.from)
			if err != nil {
				return err
			}
			if a == "" {
				continue
			}
			b, err := lang(l.to)
			if err != nil {
				return err
			}
			if a == b {
				return refusef(CatTranslationLink,
					"after this change %s %s would be in %q, the same language as %s %s, which it names in translation_of: "+
						"a translation is in a different language from its original, so the link or the change needs a human",
					noun, l.from, a, noun, l.to)
			}
		}
	}
	return nil
}

// workLanguage is a work's primary language subtag over the staged view, "" for a
// work the plan does not hold or one stating none.
func (t *txn) workLanguage(slug string) (string, error) {
	e, ok, err := t.works.get(slug)
	if err != nil || !ok {
		return "", err
	}
	return model.PrimarySubtag(e.Str("language")), nil
}

// seriesLanguage is a series' DERIVED language over the staged view:
// model.SeriesLanguage over its staged membership, each member's language read
// through the staged works.
func (t *txn) seriesLanguage(slug string) (string, error) {
	e, ok, err := t.series.get(slug)
	if err != nil || !ok {
		return "", err
	}
	var firstErr error
	lang := model.SeriesLanguage(&model.Series{Works: e.SeriesWorks()}, func(work string) string {
		l, err := t.workLanguage(work)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		return l
	})
	return lang, firstErr
}
