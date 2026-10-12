package query

import (
	"context"
	"slices"
	"sort"
)

// Exact-series-name ranking: a query that IS a series' name puts the series of
// that name FIRST among the page's series rows, and the PRIMARY series - not a
// translation, not a reading-order variant - leads the same-named rest; any
// series rows of the page sharing one name, typed whole or not, are put in that
// same order inside the slots they hold; and where a window too small for the
// tie lost the primary, the page's own links name it and it is pulled back.
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
//  0. a primary: states no translation_of and no ordering_of - or states a link
//     whose target is not among the same-named rows being ranked;
//  1. a translation: one of its translation_of targets is among them (and it is
//     no variant of one);
//  2. a reading-order variant: its ordering_of target is among them.
//
// A link demotes a row only toward the series it names: two unrelated series of
// one name, one of them a translation or variant of some THIRD series, are not
// reordered by a link that says nothing about the other.
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
// the lift has already put in tier-then-id order - is ranked by TIER ALONE
// INTO THE SLOTS THE SET ALREADY HOLDS: no other row moves, no row joins the
// page (THE PULL below is the one exception), and the set does not move up; only
// who sits in its slots changes. Inside
// a tier the page's own bm25 order stands, so a set of primaries alone is left
// exactly as it came: two unrelated series one name apart under nameKey
// ("Reaper's MC" and "Reapers MC", met by the possessive fold) score differently
// and nothing here says which is meant. Only the lift, which has an equality to
// go on, breaks ties by id.
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
// order, and the PRIMARY may be the one left off (series/search with limit=1..3
// over the SameNamedSeries fixture holds translations and the variant, never the
// primary). No default window is that small against any franchise in the
// catalogue, and THE PULL closes it without a probe.
//
// THE PULL. With its target off the page, a translation or variant is tier 0, so
// a window that lost the primary would lead with whichever edition the tie let
// in. But a same-named set left with no primary of its name - any number of
// rows of one name, a lone one included, typed whole or not - usually names that
// primary itself: a translation's translation_of and a variant's ordering_of are
// on the row already, with the target's name. A row is a primary OF ITS NAME
// when none of its links names a series of the same nameKey, the name-level
// twin of tierOf's group test. When a set holding none links to a series of the
// SAME nameKey that the page does not hold, those ids are read in ONE batch
// (seriesResultsByID, the summary read the page's own series rows came from, so
// the index guards cover it) and each that comes back a primary of that name,
// inside the live ?lang= filter, is put at the set's first slot. The pulled
// primary JOINS THE GROUP, so the page is ranked again - the rows linking to it
// now judge their link against a group holding its target and fall behind it,
// translations before variants - and is cut back to its limit, so the last row
// drops. This is the one place a row joins the page, and only a series the
// page's own rows link to under the same name. planPrimaryPulls decides it
// purely, so the read is issued on such a page alone and never on an ordinary
// one: a primary of the name on the page, a link to a differently-named series
// ("Die Saga" translating "The Saga") and a translation_of target whose stated
// language the filter refuses all plan nothing, so a lone German edition under
// lang=de costs no read. A lone row is a set too: "harry pot" with limit=1 holds
// the reading-order variant alone, and it names its primary. A failed read
// serves the ranked page, logged.
//
// WITH ?lang= the page is already filtered at the source, so under lang=de the
// English primary is not on it and the German edition - tier 0 there, its target
// being off the page, and the only exact match left - leads, which is the right
// answer for a reader who asked for German; the pull does not bring the English
// primary back (see THE PULL), and a pulled series is admitted by the filter's
// own rule, so a filtered page never gains a series outside it.
//
// AN OLDER ARTIFACT (or an empty languages layer) states no link, so every exact
// name is tier 0 and the rule is "the exact names first, by id".
//
// BYTE-IDENTITY. A page that names no series outright and holds no two series of
// one name is exactly as it came, and so is one whose exact rows already sat in
// tier-then-id order, whose other same-named rows already sat in tier order and
// none of whose sets lacking a primary of their name links to a same-named
// series off the page.
// Measured on the 282k-work artifact, the pull planned no read on any of 2,242
// series names and typing prefixes at the default window (unfiltered and
// lang=de), nor on the 931 franchise queries at limit=1 and 2 - the real
// primaries win their ties (the plans the name-level primary test admits are a
// subset of what was measured) - and one pull read costs about 0.1ms. Over 400
// random series names and the 238 franchise names carrying a translation or an
// ordering variant, on both surfaces, the lift moved one page ("Of Magic", whose
// exact name now leads "Magic of Magic"), and the timings were within noise of
// the base. The groups, measured over the typing prefixes of the same names (a first
// word cut to three letters, the first word, the first word plus half the second,
// the last word): of the random names' 1,152 prefixes, 2 combined and 4 series
// pages moved, every one the real case of the bug - "Kin", "King", "Kings" left
// the Kingsbridge reading-order variant ahead of its primary - and of the
// franchise names' 727, the same Kingsbridge pages and nothing else. The real
// Harry Potter series already tie in tier order. The grouping costs about 6us a
// page.

// seriesTier ranks same-named series: lower leads (see the file header).
type seriesTier int

const (
	tierPrimary seriesTier = iota
	tierTranslation
	tierVariant
)

// tierOf places a series hit by the links it states toward the same-named rows
// being ranked (group, by id); a link to a series outside them demotes nothing.
// A variant of one of them is a variant whatever else it states: it is a VIEW of
// another series, which is further from "the series this name means" than a
// translation of it is.
func tierOf(r seriesResult, group map[string]bool) seriesTier {
	if r.OrderingOf != "" && group[r.OrderingOf] {
		return tierVariant
	}
	for _, t := range r.TranslationOf {
		if group[t.ID] {
			return tierTranslation
		}
	}
	return tierPrimary
}

// groupIDs is the id set tierOf judges links against.
func groupIDs(rows []seriesResult) map[string]bool {
	ids := make(map[string]bool, len(rows))
	for _, r := range rows {
		ids[r.ID] = true
	}
	return ids
}

// rankExactSeries orders same-named series hits by tier, then id. Ordering by id
// inside a tier makes the answer independent of the order bm25 happened to tie
// them in.
func rankExactSeries(exact []seriesResult) {
	group := groupIDs(exact)
	sort.SliceStable(exact, func(i, j int) bool {
		ti, tj := tierOf(exact[i], group), tierOf(exact[j], group)
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

// rankByTier orders same-named series hits by tier alone, keeping the order they
// came in within a tier.
func rankByTier(rows []seriesResult) {
	group := groupIDs(rows)
	sort.SliceStable(rows, func(i, j int) bool { return tierOf(rows[i], group) < tierOf(rows[j], group) })
}

// rankSameNamedGroups is the rule's second half (see THE GROUPS in the file
// header): every set of two or more series rows on the page sharing one name
// under nameKey is ranked by tier alone, stably (rankByTier), INTO THE SLOTS THE
// SET ALREADY HOLDS, so no other row moves and nothing joins the page and rows of
// one tier keep the page's order. A page holding no such
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
		rankByTier(rows)
		for n, slot := range slots {
			out[slot] = rows[n]
		}
	}
	return out
}

// primaryPull is one same-named set of a composed page that holds no primary of
// its name but links to one the page does not hold (see THE PULL in the file
// header).
type primaryPull struct {
	slot int      // the page index of the set's first row
	key  string   // the set's nameKey
	ids  []string // the linked, same-named series the page lacks, sorted
}

// sameNamedLinks appends to ids every series r links to - translation_of or
// ordering_of - whose stated name has nameKey k, a translation_of target only
// when the live filter admits its stated language. It reports whether r states
// any same-named link at all, admitted or not: a row stating none is a primary
// of the name k (see THE PULL in the file header).
func sameNamedLinks(r seriesResult, k string, lang langFilter, ids []string) ([]string, bool) {
	linked := false
	for _, t := range r.TranslationOf {
		if nameKey(t.Name) == k {
			linked = true
			if admits(lang, t.Language) {
				ids = append(ids, t.ID)
			}
		}
	}
	if r.OrderingOf != "" && nameKey(r.OrderingOfName) == k {
		linked = true
		ids = append(ids, r.OrderingOf)
	}
	return ids, linked
}

// planPrimaryPulls finds the sets the pull applies to on a page rankSeriesRows
// has ranked: the series rows sharing one nameKey - one or more of them, typed
// whole or not - none of which is a primary of that name, where a row's
// translation_of or ordering_of names a series whose stated name has the same
// nameKey and which the page does not hold. A translation_of target also states
// its language, so one the live filter would refuse is not planned at all - a
// German edition alone on a lang=de page costs no read for the English original
// it names. It is pure, so a page it answers nil for costs no query: that is
// every page holding no such set, which is every ordinary page.
func planPrimaryPulls(page []any, lang langFilter) []primaryPull {
	onPage := map[string]bool{}
	first := map[string]int{}
	var order []string // the set keys in first-slot order
	var primary map[string]bool
	var linked map[string][]string
	for i, r := range page {
		sr, ok := r.(seriesResult)
		if !ok {
			continue
		}
		onPage[sr.ID] = true
		k := nameKey(sr.Name)
		if k == "" {
			continue
		}
		if primary == nil {
			primary, linked = map[string]bool{}, map[string][]string{}
		}
		if _, seen := first[k]; !seen {
			first[k] = i
			order = append(order, k)
		}
		var states bool
		if linked[k], states = sameNamedLinks(sr, k, lang, linked[k]); !states {
			primary[k] = true
		}
	}
	var out []primaryPull
	for _, k := range order {
		if primary[k] {
			continue
		}
		var ids []string
		for _, id := range linked[k] {
			if !onPage[id] {
				ids = append(ids, id)
			}
		}
		if len(ids) == 0 {
			continue
		}
		slices.Sort(ids)
		out = append(out, primaryPull{slot: first[k], key: k, ids: slices.Compact(ids)})
	}
	return out
}

// admits is the live filter's rule for a series' language as the FTS side reads
// it: no filter admits everything, and an unknown (tied) language passes.
func admits(lang langFilter, language string) bool {
	return len(lang) == 0 || language == "" || lang.has(language)
}

// placePrimaries puts each pull's fetched primaries - read back still of the
// set's name and still a primary of it, admitted by the live language filter
// (an unknown language passes, as on the FTS side) - at the set's first slot, by
// id, every row from that slot on moving down one per row placed. The page is
// then ranked again for q, so each placed primary joins its group and the rows
// linking to it fall behind it, and cut back to limit, so the last rows drop. A
// pull none of whose ids survives places nothing, and the page is returned as
// it came.
func placePrimaries(page []any, pulls []primaryPull, fetched map[string]seriesResult, q string, limit int, lang langFilter) []any {
	at := map[int][]any{}
	placed := false
	for _, p := range pulls {
		for _, id := range p.ids {
			r, ok := fetched[id]
			if !ok || nameKey(r.Name) != p.key || !admits(lang, r.Language) {
				continue
			}
			if _, states := sameNamedLinks(r, p.key, nil, nil); states {
				continue
			}
			at[p.slot] = append(at[p.slot], r)
			placed = true
		}
	}
	if !placed {
		return page
	}
	out := make([]any, 0, len(page)+len(at))
	for i, r := range page {
		out = append(out, at[i]...)
		out = append(out, r)
	}
	out = rankSeriesRows(out, q)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// pullPrimaries is the pull over one ranked page: plan it, read every planned id
// in ONE batch (seriesResultsByID, the summary read the page's own series rows
// came from), and place what qualifies. A page with nothing to pull is returned
// as it came, with no query. A failed read degrades to the ranked page, logged,
// as a failed boost probe does: the pull refines a page the search produced on
// its own.
func (s *DB) pullPrimaries(ctx context.Context, page []any, q string, limit int, lang langFilter) []any {
	pulls := planPrimaryPulls(page, lang)
	if len(pulls) == 0 {
		return page
	}
	var ids []string
	for _, p := range pulls {
		ids = append(ids, p.ids...)
	}
	fetched, err := s.seriesResultsByID(ctx, ids)
	if err != nil {
		if ctx.Err() == nil {
			s.logf("query: primary-series read for %q failed, serving the ranked page: %v", q, err)
		}
		return page
	}
	return placePrimaries(page, pulls, fetched, q, limit, lang)
}
