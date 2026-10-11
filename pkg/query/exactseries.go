package query

import (
	"slices"
	"sort"
)

// Exact-series-name ranking: a query that IS a series' name puts the series of
// that name FIRST among the page's series rows, and the PRIMARY series - not a
// translation, not a reading-order variant - leads the same-named rest; and any
// series rows of the page sharing one name, typed whole or not, are put in that
// same order inside the slots they hold.
//
// THE BUG. A search for "harry potter" listed three series rows all named
// "Harry Potter": the English series and its German and Spanish editions
// (harry-potter-german-edition and harry-potter-spanish-edition, each stating
// translation_of harry-potter). Identical names are identical FTS rows, so bm25
// gives them identical scores and the order among them is whatever the tie falls
// to - nothing in a relevance score can say "the original first", so this file
// says it. Unlike the two work boosts (exacttitle.go, seriespos.go) it is ranking
// alone, and query-side for the same reason: a relevance score cannot express an
// equality, and nothing about the artifact changes.
//
// THE RULE. Among the page's series rows, those whose name equals the query
// under nameKey - the comparison the exact-title boost and preferWholeName make -
// are ranked by TIER, then by id:
//
//  0. a primary: states no translation_of and no ordering_of;
//  1. a translation: states translation_of (and is no variant);
//  2. a reading-order variant: states ordering_of.
//
// and the tiered block takes the place of the page's FIRST series row, each
// member dropped from wherever else it sat. The partial matches ("Harry Potter
// (Full-Cast Editions)") follow in their bm25 order, and works and people keep
// their RELATIVE order: the rows ahead of the first series row - the work boosts
// always among them - keep their slots, and a row between the first series row
// and an exact one moves down by the exact rows lifted past it. A same-named
// translation keeps its place
// ahead of the partial matches: it is an exact match the reader may well want,
// and the site labels it ("Translation of Harry Potter"), which is what makes
// three identical names readable at all.
//
// THE GROUPS. The lift needs the WHOLE name, and a reader is still typing it:
// "harry pot" or "potter" holds the same four "Harry Potter" rows, tied in bm25
// just the same, and nothing lifts them. So every set of two or more series rows
// on the page sharing one name under nameKey - the exact block included, which
// the lift has already put in this order - is ranked by the same tier-then-id
// rule INTO THE SLOTS THE SET ALREADY HOLDS: no other row moves, no row joins the
// page, and the set does not move up; only who sits in its slots changes. A set
// of primaries alone is put in id order, which is the tie order bm25 falls to
// for identical names in any case.
//
// WHY THE PAGE AND NOT A PROBE. The rule reads only rows the page already
// composed: seriesResultsByID has read every series row's name, ordering_of and
// translation_of in its batches, so the ranking costs no query at all - nameKey
// over at most limit names. A probe of its own (the exact-title probe's shape,
// filtered to series rows) could also pull an exact name the page did not hold;
// measured on the 282k-work artifact it cost every keystroke of a common word a
// whole extra walk of its posting list ("book" 66 -> 96ms on the combined page,
// 43 -> 78ms on series/search, "love" 32 -> 43ms) to answer a case bm25 does not
// produce: an exact name is the shortest series row holding every query word, so
// it ranks at the top of the series rows and is on the page whenever the page
// holds any of them. Over 400 random series names and the 238 franchises carrying
// a translation or an ordering variant, probe and page agreed on every page. The
// one gap is a window smaller than the tie: several same-named series score
// alike, so which of them reach a page shorter than their count is bm25's tie
// order, and the PRIMARY may be the one left off (series/search with limit=2 over
// the SameNamedSeries fixture holds a translation and the variant). No default
// window is that small against any franchise in the catalogue.
//
// WITH ?lang= there is nothing more to do: the page is already filtered at the
// source, so under lang=de the English primary is not on it and the German
// edition - tier 1, the only exact match left - leads, which is the right answer
// for a reader who asked for German.
//
// AN OLDER ARTIFACT (or an empty languages layer) states no link, so every exact
// name is tier 0 and the rule is "the exact names first, by id".
//
// BYTE-IDENTITY. A page that names no series outright and holds no two series of
// one name is exactly as it came, and so is one whose same-named rows already sat
// in tier-then-id order. Measured on the 282k-work artifact, over 400 random
// series names and the 238 franchise names carrying a translation or an ordering
// variant, on both surfaces: the lift moved one page ("Of Magic", whose exact
// name now leads "Magic of Magic"), and the timings were within noise of the
// base. The groups, measured over the typing prefixes of the same names (a first
// word cut to three letters, the first word, the first word plus half the second,
// the last word): of the random names' 1,152 prefixes, 2 combined and 4 series
// pages moved, every one the real case of the bug - "Kin", "King", "Kings" left
// the Kingsbridge reading-order variant ahead of its primary; of the franchise
// names' 727, the same Kingsbridge pages and one more, a pair of primaries put in
// id order ("reaper": "Reaper's MC" and "Reapers MC", one name under the
// possessive fold). The real Harry Potter series already tie in tier order. The
// grouping costs about 6us a page.

// seriesTier ranks same-named series: lower leads (see the file header).
type seriesTier int

const (
	tierPrimary seriesTier = iota
	tierTranslation
	tierVariant
)

// tierOf places a series hit by the links it states. A variant is a variant
// whatever else it states: it is a VIEW of another series, which is further from
// "the series this name means" than a translation of it is.
func tierOf(r seriesResult) seriesTier {
	switch {
	case r.OrderingOf != "":
		return tierVariant
	case len(r.TranslationOf) > 0:
		return tierTranslation
	}
	return tierPrimary
}

// rankExactSeries orders same-named series hits by tier, then id. Ordering by id
// inside a tier makes the answer independent of the order bm25 happened to tie
// them in.
func rankExactSeries(exact []seriesResult) {
	sort.SliceStable(exact, func(i, j int) bool {
		ti, tj := tierOf(exact[i]), tierOf(exact[j])
		if ti != tj {
			return ti < tj
		}
		return exact[i].ID < exact[j].ID
	})
}

// rankSeriesRows applies the rule to one composed page: the series rows whose
// name IS q, ranked by rankExactSeries, take the place of the page's first series
// row, and every other row keeps its relative order. The page keeps its length
// and its rows, and only series rows are reordered - a row the lifted block
// passes moves down a slot per row lifted past it. A page with no such row is
// returned as it came.
func rankSeriesRows(page []any, q string) []any {
	want := nameKey(q)
	if want == "" {
		return page
	}
	first := -1
	var exact []seriesResult
	var at []int // the page index of each exact row, ascending
	for i, r := range page {
		sr, ok := r.(seriesResult)
		if !ok {
			continue
		}
		if first < 0 {
			first = i
		}
		if nameKey(sr.Name) == want {
			exact = append(exact, sr)
			at = append(at, i)
		}
	}
	if len(exact) == 0 {
		return rankSameNamedGroups(page)
	}
	rankExactSeries(exact)
	out := make([]any, 0, len(page))
	out = append(out, page[:first]...)
	for _, r := range exact {
		out = append(out, r)
	}
	for i, r := range page[first:] {
		if len(at) > 0 && at[0] == first+i {
			at = at[1:]
			continue
		}
		out = append(out, r)
	}
	return rankSameNamedGroups(out)
}

// rankSameNamedGroups is the rule's second half (see THE GROUPS in the file
// header): every set of two or more series rows on the page sharing one name
// under nameKey is ranked by rankExactSeries INTO THE SLOTS THE SET ALREADY
// HOLDS, so no other row moves and nothing joins the page. A page holding no such
// set is returned as it came; one is copied before it is reordered, never
// rewritten in place. After the whole-query lift the exact rows are one set
// already in tier order, so this leaves them where the lift put them.
func rankSameNamedGroups(page []any) []any {
	var keys []string // the nameKey of each page row, "" for a non-series row
	var count map[string]int
	grouped := false
	for i, r := range page {
		sr, ok := r.(seriesResult)
		if !ok {
			continue
		}
		k := nameKey(sr.Name)
		if k == "" {
			continue
		}
		if keys == nil {
			keys = make([]string, len(page))
			count = map[string]int{}
		}
		keys[i] = k
		count[k]++
		if count[k] > 1 {
			grouped = true
		}
	}
	if !grouped {
		return page
	}
	out := slices.Clone(page)
	done := map[string]bool{}
	for i, k := range keys {
		if k == "" || count[k] < 2 || done[k] {
			continue
		}
		done[k] = true
		var slots []int
		var rows []seriesResult
		for j := i; j < len(keys); j++ {
			if keys[j] == k {
				slots = append(slots, j)
				rows = append(rows, page[j].(seriesResult))
			}
		}
		rankExactSeries(rows)
		for n, slot := range slots {
			out[slot] = rows[n]
		}
	}
	return out
}
