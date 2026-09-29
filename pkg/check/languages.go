package check

// languages.go holds the cross-record rules over the Languages Phase 2 links:
// translation_of on a work and on a series, and a series' ordering /
// ordering_of. The schema pins the shapes (a non-empty slug set; the ordering
// enum; ordering_of requiring ordering); what the schema cannot see is what the
// slugs NAME, and that is these rules' job. Every one of them runs over the
// assembled catalogue after the parallel per-pack phase, like every other
// cross-record rule, and reports against the record's own pack entry.
//
// The links are STATED evidence only - nothing infers a translation or an
// ordering - so every rule here is about keeping a statement resolvable and
// one hop deep, never about whether it is true.

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// translationLink is one record's translation_of as the rules over it see it:
// a work or a series, reduced to what the five rules ask.
type translationLink struct {
	kind    model.RedirectKind // the family spelling, which is also the redirect namespace
	id      string
	path    string
	targets []string
}

// checkLanguageLinks runs the translation_of rules over works and series and the
// ordering rules over series.
//
// It is profile-gated per LoadProfile's skip rule, arm by arm: the work arm
// needs the works family and the series arms the series family. Under
// ProfileCommunity neither is held, so nothing runs; under ProfileCore (and so
// on the core side of LoadComposed) both are, so everything does. A series'
// LANGUAGE is derived from its members' works, so the series language rule is
// asked only where the works are held too - and it already stands down on an
// unknown language, which is what a tree without works would give it.
//
// works is checkIntegrity's first-record-wins id map, reused rather than
// rebuilt. The series map is built HERE and only when some series carries a link,
// so a catalogue with none of these fields - every tree until Phase 3's data
// lands - pays one pass over the series and nothing more.
func checkLanguageLinks(profile pack.Profile, cat *model.Catalog, works map[string]*model.Work, idx *pathIndex, add, warn addFunc) {
	if profile.Has(pack.FamilyWorks) {
		checkWorkTranslations(cat, works, idx, add)
	}
	if !profile.Has(pack.FamilySeries) {
		return
	}
	linked := slices.ContainsFunc(cat.Series, func(s *model.Series) bool {
		return len(s.TranslationOf) > 0 || s.OrderingOf != ""
	})
	if !linked {
		return
	}
	series := map[string]*model.Series{}
	for _, s := range cat.Series {
		if _, dup := series[s.ID]; !dup {
			series[s.ID] = s
		}
	}
	checkSeriesTranslations(cat, works, series, idx, add)
	checkSeriesOrderings(cat, series, idx, add, warn)
}

// checkWorkTranslations is the translation_of rule set over the works family.
// A work's language is its own field, required by the schema, so the language
// rule always has both sides to compare.
func checkWorkTranslations(cat *model.Catalog, works map[string]*model.Work, idx *pathIndex, add addFunc) {
	for _, w := range cat.Works {
		if len(w.TranslationOf) == 0 {
			continue
		}
		link := translationLink{kind: model.RedirectWorks, id: w.ID, path: idx.work[w], targets: w.TranslationOf}
		checkTranslationLink(link, cat.Redirects, func(id string) (lang string, links []string, live bool) {
			t := works[id]
			if t == nil {
				return "", nil, false
			}
			return t.Language, t.TranslationOf, true
		}, w.Language, add)
	}
}

// checkSeriesTranslations is the same rule set over the series family. A
// series has no language of its own: both sides are model.SeriesLanguage over
// their members, and the language rule is SKIPPED when either is "" - a tie
// cannot be judged, and a sync-bot volume that turns a linked series into a
// tie must not turn the link red.
func checkSeriesTranslations(cat *model.Catalog, works map[string]*model.Work, series map[string]*model.Series, idx *pathIndex, add addFunc) {
	langOf := func(workID string) string {
		if w := works[workID]; w != nil {
			return w.Language
		}
		return ""
	}
	language := func(s *model.Series) string { return model.SeriesLanguage(s, langOf) }
	for _, s := range cat.Series {
		if len(s.TranslationOf) == 0 {
			continue
		}
		link := translationLink{kind: model.RedirectSeries, id: s.ID, path: idx.series[s], targets: s.TranslationOf}
		checkTranslationLink(link, cat.Redirects, func(id string) (lang string, links []string, live bool) {
			t := series[id]
			if t == nil {
				return "", nil, false
			}
			return language(t), t.TranslationOf, true
		}, language(s), add)
	}
}

// checkTranslationLink judges one record's translation_of against the five
// rules: every target a LIVE id of the record's own family (a retired one named
// with its survivor), never the record itself, in another language, not itself a
// translation (no chains), and the set in ascending order (the canonical
// byte-form; uniqueness is the schema's uniqueItems).
//
// lookup answers a target id's language, its own translation_of and whether the
// family holds it; lang is the linking record's language. Either language being
// "" skips the language rule for that pair rather than calling it a match.
func checkTranslationLink(link translationLink, reds model.Redirects,
	lookup func(id string) (lang string, links []string, live bool), lang string, add addFunc) {
	for i, target := range link.targets {
		if i > 0 && target < link.targets[i-1] {
			add(link.path, "translation_of must be sorted: %q comes after %q", target, link.targets[i-1])
		}
		if target == link.id {
			add(link.path, "translation_of names the record itself: a translation links to its ORIGINAL, "+
				"which is a different %s record", familyNoun(link.kind))
			continue
		}
		targetLang, targetLinks, live := lookup(target)
		if !live {
			add(link.path, "translation_of %s", deadTarget(reds, link.kind, target))
			continue
		}
		if len(targetLinks) > 0 {
			add(link.path, "translation_of %q is itself a translation (of %s): link to the original directly - "+
				"translations do not chain", target, quoteAll(targetLinks))
		}
		if lang == "" || targetLang == "" {
			continue
		}
		if model.PrimarySubtag(lang) == model.PrimarySubtag(targetLang) {
			add(link.path, "translation_of %q is in the same language (%q) as this %s: a translation is into "+
				"another language", target, model.PrimarySubtag(lang), familyNoun(link.kind))
		}
	}
}

// checkSeriesOrderings is the ordering rule set: a variant's ordering_of names a
// LIVE series (a retired one named with its survivor), never itself, and one
// that is not itself a variant (an ordering family is ONE hop deep); and no two
// members of one ordering FAMILY - a primary and every series whose ordering_of
// names it - state the same ordering, since a second series in the same order is
// a duplicate to fold rather than a view.
//
// Alongside it, the ordering-variant-not-subset ADVISORY: a variant listing a
// work its primary does not. That is legitimate - a chronological order holding
// a prequel novella the publication order never numbered - so it is only ever a
// warning.
func checkSeriesOrderings(cat *model.Catalog, series map[string]*model.Series, idx *pathIndex, add, warn addFunc) {
	// families maps a primary's id to the variants naming it, in catalogue order.
	families := map[string][]*model.Series{}
	for _, s := range cat.Series {
		if s.OrderingOf == "" {
			continue
		}
		rel := idx.series[s]
		if s.OrderingOf == s.ID {
			add(rel, "ordering_of names the series itself: a variant names its franchise's PRIMARY ordering, "+
				"which is a different series")
			continue
		}
		primary := series[s.OrderingOf]
		if primary == nil {
			add(rel, "ordering_of %s", deadTarget(cat.Redirects, model.RedirectSeries, s.OrderingOf))
			continue
		}
		if primary.OrderingOf != "" {
			add(rel, "ordering_of %q is itself a variant (of %q): name the primary directly - an ordering "+
				"family is one hop deep", s.OrderingOf, primary.OrderingOf)
			continue
		}
		families[primary.ID] = append(families[primary.ID], s)
	}

	for _, id := range slices.Sorted(maps.Keys(families)) {
		primary := series[id]
		members := append([]*model.Series{primary}, families[id]...)
		// Judged in id order so which member of a duplicate pair is reported, and
		// which one it is reported against, does not depend on the catalogue's.
		slices.SortStableFunc(members[1:], func(a, b *model.Series) int { return cmp.Compare(a.ID, b.ID) })
		stated := map[string]*model.Series{}
		for _, m := range members {
			if m.Ordering == "" {
				continue
			}
			if first, dup := stated[m.Ordering]; dup {
				add(idx.series[m], "series %q and %q both state the %q ordering of the family whose primary is %q: "+
					"a second series in the same order is a duplicate to fold, not a view",
					first.ID, m.ID, m.Ordering, id)
				continue
			}
			stated[m.Ordering] = m
		}

		inPrimary := map[string]bool{}
		for _, sw := range primary.Works {
			inPrimary[sw.Work] = true
		}
		for _, v := range members[1:] {
			var extra []string
			for _, sw := range v.Works {
				if !inPrimary[sw.Work] && !slices.Contains(extra, sw.Work) {
					extra = append(extra, sw.Work)
				}
			}
			if len(extra) == 0 {
				continue
			}
			slices.Sort(extra)
			warn(idx.series[v], "ordering variant lists %s, which its primary ordering %q does not: %s",
				quoteAll(extra), id, orderingNotSubset)
		}
	}
}

// orderingNotSubset is the tail the ordering-variant-not-subset advisory ends
// in, and the marker advisoryMarkers files it under.
const orderingNotSubset = "a variant listing works its primary does not (legitimate for, say, " +
	"a chronological order holding a prequel novella)"

// deadTarget renders why a link's target is not a live id: retired (naming the
// survivor the link should point at instead) or simply not held.
func deadTarget(reds model.Redirects, kind model.RedirectKind, target string) string {
	if to, retired := reds.Survivor(kind, target); retired {
		return fmt.Sprintf("%q is retired - point at %q", target, to)
	}
	return fmt.Sprintf("%q is no live %s id", target, familyNoun(kind))
}

// familyNoun is the singular noun a message names a record of kind by.
func familyNoun(kind model.RedirectKind) string {
	if kind == model.RedirectWorks {
		return "work"
	}
	return string(kind)
}

// quoteAll renders slugs as a comma-separated list of Go-quoted strings.
func quoteAll(ids []string) string {
	q := make([]string, len(ids))
	for i, id := range ids {
		q[i] = strconv.Quote(id)
	}
	return strings.Join(q, ", ")
}
