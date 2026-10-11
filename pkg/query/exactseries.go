package query

import "sort"

// Exact-series-name ranking: a query that IS a series' name puts the series of
// that name FIRST among the page's series rows, and the PRIMARY series - not a
// translation, not a reading-order variant - leads the same-named rest.
//
// THE BUG. A search for "harry potter" listed three series rows all named
// "Harry Potter": the English series and its German and Spanish editions
// (harry-potter-german-edition and harry-potter-spanish-edition, each stating
// translation_of harry-potter). Identical names are identical FTS rows, so bm25
// gives them identical scores and the order among them is whatever the tie falls
// to - nothing in a relevance score can say "the original first". On today's
// catalogue the tie happens to fall the right way (the edition slugs carry a
// suffix, so they sort after the original and their rows were inserted after
// it), but that is an accident of slug spelling, not a rule; this file makes it
// one. Unlike the two work boosts (exacttitle.go, seriespos.go) it is ranking
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
// (Full-Cast Editions)") follow in their bm25 order; works and people keep their
// slots, the work boosts included. A same-named translation keeps its place
// ahead of the partial matches: it is an exact match the reader may well want,
// and the site labels it ("Translation of Harry Potter"), which is what makes
// three identical names readable at all.
//
// WHY THE PAGE AND NOT A PROBE. The rule reads only rows the page already
// composed: seriesResultsByID has read every series row's name, ordering_of and
// translation_of in its batches, so the ranking costs no query at all - nameKey
// over at most limit names. A first version ran its own FTS probe (the
// exact-title probe's shape, filtered to series rows) so it could also pull an
// exact name the page did not hold; measured on the 282k-work artifact that
// probe cost every keystroke of a common word a whole extra walk of its posting
// list ("book" 66 -> 96ms on the combined page, 43 -> 78ms on series/search,
// "love" 32 -> 43ms) to answer a case bm25 does not produce: an exact name is the
// shortest series row holding every query word, so it ranks at the top of the
// series rows and is on the page whenever the page holds any of them. Over 400
// random series names and the 238 franchises carrying a translation or an
// ordering variant the two versions agreed on every page.
//
// WITH ?lang= there is nothing more to do: the page is already filtered at the
// source, so under lang=de the English primary is not on it and the German
// edition - tier 1, the only exact match left - leads, which is the right answer
// for a reader who asked for German.
//
// AN OLDER ARTIFACT (or an empty languages layer) states no link, so every exact
// name is tier 0 and the rule is "the exact names first, by id".
//
// BYTE-IDENTITY. A query that names no series on the page outright leaves the
// page exactly as it was, and so does one whose exact names already led the
// series rows in tier order. Measured on the 282k-work artifact, over 400 random
// series names and the 238 franchise names carrying a translation or an ordering
// variant, on both surfaces: one page moved ("Of Magic", whose exact name now
// leads "Magic of Magic"), and the timings were within noise of the base.

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
// and its rows; only series rows move. A page with no such row is returned as it
// came.
func rankSeriesRows(page []any, q string) []any {
	want := nameKey(q)
	if want == "" {
		return page
	}
	first := -1
	var exact []seriesResult
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
		}
	}
	if len(exact) == 0 {
		return page
	}
	rankExactSeries(exact)
	isExact := make(map[string]bool, len(exact))
	for _, r := range exact {
		isExact[r.ID] = true
	}
	out := make([]any, 0, len(page))
	out = append(out, page[:first]...)
	for _, r := range exact {
		out = append(out, r)
	}
	for _, r := range page[first:] {
		if sr, ok := r.(seriesResult); ok && isExact[sr.ID] {
			continue
		}
		out = append(out, r)
	}
	return out
}
