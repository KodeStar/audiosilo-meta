package importer

import (
	"sort"
	"strings"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// seriesresolve.go decides, for a whole BATCH of series claims at once, which
// series each claim lands in - an existing same-named series its authors fit
// (seriesauthors.go), or a new one at the next free candidate of the name's slug
// chain. Every writer resolves through it: the importer's planner in a pre-pass
// before any row is planned (planner.resolveSeriesTargets), libex-select for the
// rows it keeps, and the intake form for its one submission.
//
// It decides from a SNAPSHOT - the catalogue's evidence plus a census of the
// whole batch - and never from evidence a run accumulates row by row, so no
// answer depends on the order the rows arrive in (the same discipline as the
// initials decision, initials.go). Per series name, in four steps over the
// catalogue's same-named candidates (chain order):
//
//  1. ANCHOR: a claim whose authors a candidate SHARES joins it, and its authors
//     become that candidate's evidence; repeated until nothing more anchors, each
//     round judged against the evidence as it stood when the round began.
//  2. OPEN: a claim still unplaced joins the first candidate that is OPEN to it,
//     judged against the anchored evidence.
//  3. CLUSTER: the rest will found new series. They are grouped by shared author
//     (personForm.same, transitively), and the groups are taken largest first
//     (ties by their smallest canonical key): each joins the first new series
//     OPEN to it, or founds the next one. A claim that places nothing adds no
//     evidence, so a group whose claims state no usable position founds a series
//     every later group is OPEN to, rather than one that refuses them from a
//     slug it will never write.
//  4. MINT: the new series take the chain's free slugs in the order they were
//     founded, skipping every held, retired or already-allocated candidate - a
//     series two names share a base with ("Saga" and "Saga!!") included.
//
// That is what separates the seed-wave shape the importer used to get wrong
// whatever the order: Hawke's Lost Fleet 1-3 and Campbell's 4-6 in ONE batch with
// no catalogue series found two groups; Hawke's (four rows) founds `lost-fleet`
// and Campbell's is refused by it and founds `lost-fleet-2`.

// nameClaim is one row's claim to a named series, as the resolution reads it.
type nameClaim struct {
	name string
	row  *SeriesRow
	// order is the claim's canonical tie-break key, built from the row's own
	// facts (never its input position), so every ordering of one batch resolves
	// alike.
	order string
	// evidence says whether the claim will place a member if it lands - a row
	// the run deduplicates away, a claim stating no usable position, or a mode
	// that places nothing, is no evidence.
	evidence bool
}

// seriesTarget is where a claim lands.
type seriesTarget struct {
	// slug is the series the claim joins or founds; "" when the name has no
	// addressable slug (the claim is refused, see getOrCreateSeries).
	slug string
	// found is true when slug is a series the catalogue already holds.
	found bool
	// via is the retired base slug a found series was reached through, or "".
	via string
	// stepped are the same-named catalogued series the claim did not fit, when it
	// founds a new one.
	stepped []string
}

// seriesCatalogue is what the resolution reads about the catalogue.
type seriesCatalogue struct {
	stored    func(slug string) (string, bool)
	redirects model.Redirects
	evidence  func(slug string) *SeriesAuthors
	large     map[string]bool
}

// claimOrder is a claim's canonical key: its authors, titles and publishers and
// an identifier, none of which depends on where the row sat in its input.
func claimOrder(row *SeriesRow, id string) string {
	slugs := make([]string, 0, len(row.Authors))
	for _, a := range row.Authors {
		slugs = append(slugs, a.Slug)
	}
	sort.Strings(slugs)
	return strings.Join(slugs, ",") + "\x00" + strings.Join(row.Titles, "|") + "\x00" +
		strings.Join(row.Publishers, "|") + "\x00" + id
}

// resolveSeriesClaims resolves every claim of a batch; the result is parallel to
// claims.
func resolveSeriesClaims(cat seriesCatalogue, claims []nameClaim) []seriesTarget {
	out := make([]seriesTarget, len(claims))
	groups := map[string][]int{}
	for i, c := range claims {
		base := Slugify(c.name)
		if base == "" {
			continue // unaddressable: the zero target, a refused claim
		}
		key := base + "\x00" + strings.ToLower(c.name)
		groups[key] = append(groups[key], i)
	}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	allocated := map[string]map[string]bool{} // base -> slugs minted this batch
	for _, k := range keys {
		resolveSeriesGroup(cat, claims, groups[k], out, allocated)
	}
	return out
}

// seriesCandidate is a catalogued same-named series on a name's chain.
type seriesCandidate struct {
	slug, via string
	ev        *SeriesAuthors
}

// seriesCandidates walks a name's chain over the catalogue: every held slug whose
// name matches case-insensitively, and a retired BASE's live survivor, in chain
// order and each once. The walk ends at the first free slug - nothing beyond it
// can have been minted - and every other held or retired slug is occupied.
func seriesCandidates(cat seriesCatalogue, base, name string) []seriesCandidate {
	var out []seriesCandidate
	seen := map[string]bool{}
	add := func(slug, via string) {
		if seen[slug] {
			return
		}
		seen[slug] = true
		var ev *SeriesAuthors
		if cat.evidence != nil {
			ev = cat.evidence(slug)
		}
		out = append(out, seriesCandidate{slug: slug, via: via, ev: ev.clone()})
	}
	for i := 0; ; i++ {
		slug := SeriesSlugAt(base, i)
		if held, exists := cat.stored(slug); exists {
			if strings.EqualFold(held, name) {
				add(slug, "")
			}
			continue
		}
		to, retired := cat.redirects.Survivor(model.RedirectSeries, slug)
		if !retired {
			return out
		}
		if i == 0 {
			if _, live := cat.stored(to); live {
				add(to, slug)
			}
		}
	}
}

// mintSlug is the next free slug on base's chain: not held, not retired, not
// already minted by this batch.
func mintSlug(cat seriesCatalogue, base string, allocated map[string]map[string]bool) string {
	used := allocated[base]
	if used == nil {
		used = map[string]bool{}
		allocated[base] = used
	}
	for i := 0; ; i++ {
		slug := SeriesSlugAt(base, i)
		if _, held := cat.stored(slug); held {
			continue
		}
		if _, retired := cat.redirects.Survivor(model.RedirectSeries, slug); retired {
			continue
		}
		if used[slug] {
			continue
		}
		used[slug] = true
		return slug
	}
}

// resolveSeriesGroup resolves the claims (indexes into claims) that name one
// series.
func resolveSeriesGroup(cat seriesCatalogue, claims []nameClaim, idx []int, out []seriesTarget, allocated map[string]map[string]bool) {
	sort.SliceStable(idx, func(a, b int) bool { return claims[idx[a]].order < claims[idx[b]].order })
	name := claims[idx[0]].name
	base := Slugify(name)
	cands := seriesCandidates(cat, base, name)
	const unplaced = -1
	placed := make([]int, len(idx))
	for i := range placed {
		placed[i] = unplaced
	}
	addEvidence := func(ev *SeriesAuthors, c nameClaim) {
		if c.evidence {
			ev.add(c.row.Authors, c.row.Publishers)
		}
	}

	// 1. Anchor, in rounds judged against the evidence at the round's start.
	for {
		type anchor struct{ k, cand int }
		var round []anchor
		for k, ci := range idx {
			if placed[k] != unplaced {
				continue
			}
			for c := range cands {
				if cands[c].ev.fit(claims[ci].row, cat.large) == SeriesShared {
					round = append(round, anchor{k, c})
					break
				}
			}
		}
		if len(round) == 0 {
			break
		}
		for _, a := range round {
			placed[a.k] = a.cand
			addEvidence(cands[a.cand].ev, claims[idx[a.k]])
		}
	}

	// 2. Open catalogued candidates, against the anchored evidence.
	for k, ci := range idx {
		if placed[k] != unplaced {
			continue
		}
		for c := range cands {
			if cands[c].ev.fit(claims[ci].row, cat.large) == SeriesOpen {
				placed[k] = c
				break
			}
		}
	}
	for k, ci := range idx {
		if placed[k] != unplaced {
			c := cands[placed[k]]
			out[ci] = seriesTarget{slug: c.slug, found: true, via: c.via}
		}
	}

	// 3. Cluster what is left by shared author, and found new series.
	var rest []int // positions in idx
	for k := range idx {
		if placed[k] == unplaced {
			rest = append(rest, k)
		}
	}
	if len(rest) == 0 {
		return
	}
	var stepped []string
	for _, c := range cands {
		stepped = append(stepped, c.slug)
	}
	clusters := authorClusters(claims, idx, rest)
	type newSeries struct {
		slug string
		ev   *SeriesAuthors
	}
	var founded []*newSeries
	for _, cl := range clusters {
		combined := combinedRow(claims, idx, cl)
		var home *newSeries
		for _, ns := range founded {
			if ns.ev.fit(combined, cat.large) != SeriesClosed {
				home = ns
				break
			}
		}
		if home == nil {
			home = &newSeries{slug: mintSlug(cat, base, allocated), ev: &SeriesAuthors{}}
			founded = append(founded, home)
		}
		for _, k := range cl {
			addEvidence(home.ev, claims[idx[k]])
			out[idx[k]] = seriesTarget{slug: home.slug, stepped: stepped}
		}
	}
}

// authorClusters groups the claims at positions rest (into idx) by shared author,
// transitively, and orders the groups largest first, ties by their smallest
// canonical key. A claim with no individual author is a group of its own.
func authorClusters(claims []nameClaim, idx, rest []int) [][]int {
	parent := make(map[int]int, len(rest))
	find := func(x int) int {
		for parent[x] != x {
			parent[x] = parent[parent[x]]
			x = parent[x]
		}
		return x
	}
	union := func(a, b int) {
		if ra, rb := find(a), find(b); ra != rb {
			parent[ra] = rb
		}
	}
	for _, k := range rest {
		parent[k] = k
	}
	// One representative claim per author slug, then the spelling rungs across
	// the distinct slugs.
	bySlug := map[string]int{}
	var slugs []string
	forms := map[string]personForm{}
	for _, k := range rest {
		for _, f := range claims[idx[k]].row.individuals() {
			if first, ok := bySlug[f.slug]; ok {
				union(k, first)
				continue
			}
			bySlug[f.slug] = k
			slugs = append(slugs, f.slug)
			forms[f.slug] = f
		}
	}
	sort.Strings(slugs)
	for i := range slugs {
		for j := i + 1; j < len(slugs); j++ {
			if forms[slugs[i]].same(forms[slugs[j]]) {
				union(bySlug[slugs[i]], bySlug[slugs[j]])
			}
		}
	}
	grouped := map[int][]int{}
	for _, k := range rest {
		r := find(k)
		grouped[r] = append(grouped[r], k)
	}
	out := make([][]int, 0, len(grouped))
	for _, g := range grouped {
		sort.Ints(g) // rest is in canonical order, so positions sort canonically
		out = append(out, g)
	}
	sort.Slice(out, func(a, b int) bool {
		if len(out[a]) != len(out[b]) {
			return len(out[a]) > len(out[b])
		}
		return claims[idx[out[a][0]]].order < claims[idx[out[b][0]]].order
	})
	return out
}

// combinedRow is a cluster's claims read as one row: every author, title and
// publisher any of them states.
func combinedRow(claims []nameClaim, idx, cluster []int) *SeriesRow {
	row := &SeriesRow{}
	seen := map[string]bool{}
	for _, k := range cluster {
		r := claims[idx[k]].row
		for _, a := range r.Authors {
			if !seen[a.Slug] {
				seen[a.Slug] = true
				row.Authors = append(row.Authors, a)
			}
		}
		row.Titles = append(row.Titles, r.Titles...)
		row.Publishers = append(row.Publishers, r.Publishers...)
	}
	return row
}

// seriesRowOf is a source row's SeriesRow: its cleaned author credits, each at
// the person slug resolve maps it to, its title spellings and its publisher. The
// importer and libex-select both build rows here, so the two cannot judge one row
// differently.
func seriesRowOf(credits []credit, b sourceBook, resolve func(slug string) string) *SeriesRow {
	row := &SeriesRow{Titles: []string{b.str("title"), b.str("title_short")}}
	for _, c := range credits {
		slug, _ := personSlug(c.name)
		if resolve != nil {
			slug = resolve(slug)
		}
		row.Authors = append(row.Authors, SeriesPerson{Slug: slug, Name: c.name})
	}
	if pub := b.str("publisher"); pub != "" {
		row.Publishers = []string{pub}
	}
	return row
}

// SeriesAuthorIndex is the catalogue's series evidence for a writer that keeps no
// planner of its own (the intake form): every series' member authors, and the
// catalogue-wide publishers the publisher arm ignores.
type SeriesAuthorIndex struct {
	series map[string]*SeriesAuthors
	large  map[string]bool
}

// NewSeriesAuthorIndex builds the index over cat. A nil catalogue is an empty
// index, under which every series is open.
func NewSeriesAuthorIndex(cat *model.Catalog) *SeriesAuthorIndex {
	if cat == nil {
		return &SeriesAuthorIndex{}
	}
	return &SeriesAuthorIndex{series: seriesAuthorsOf(cat), large: largePublishersOf(cat)}
}

// SeriesMatch is where a series name resolves for one row.
type SeriesMatch struct {
	// Slug is the catalogued series the row joins when Found, else the slug a
	// new series for the name would be minted at; "" when the name has no
	// addressable slug.
	Slug  string
	Found bool
	// Via is the retired base slug the answer was reached through, or "".
	Via string
	// Stepped are the same-named series the row did not fit, in chain order.
	Stepped []string
}

// Resolve is resolveSeriesClaims for one row's claim to name: stored reports the
// name a series slug holds, reds is the tombstone table. A nil index judges no
// authors (the name-only walk).
func (ix *SeriesAuthorIndex) Resolve(name string, reds model.Redirects, stored func(slug string) (string, bool), row *SeriesRow) SeriesMatch {
	cat := seriesCatalogue{stored: stored, redirects: reds}
	if ix != nil {
		cat.evidence = func(slug string) *SeriesAuthors { return ix.series[slug] }
		cat.large = ix.large
	}
	if row == nil {
		row = &SeriesRow{}
	}
	t := resolveSeriesClaims(cat, []nameClaim{{name: name, row: row, order: claimOrder(row, "")}})[0]
	return SeriesMatch{Slug: t.slug, Found: t.found, Via: t.via, Stepped: t.stepped}
}
