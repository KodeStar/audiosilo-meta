package importer

import (
	"slices"
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
// initials decision, initials.go). A book counts once however many rows state it
// (nameClaim.work), and a claim that places nothing is no evidence at all. Per
// series name, in three steps over the catalogue's same-named candidates (chain
// order):
//
//  1. ANCHOR: a claim whose authors a candidate SHARES joins it, and its authors
//     become that candidate's evidence; repeated until nothing more anchors, each
//     round judged against the evidence as it stood when the round began.
//  2. CLUSTER: the rest are grouped by shared author (personForm.same,
//     transitively) and the groups taken largest first (ties by their smallest
//     canonical key). A group joins the first catalogued candidate that ADMITS it
//     (seriesAuthors.admits: open to the group, and not handed to the group's
//     authors by its arrival), else the first series this batch founded that
//     admits it, else founds the next one.
//  3. MINT: the new series take the chain's free slugs in the order they were
//     founded, skipping every held, retired or already-allocated candidate - a
//     series two names share a base with ("Saga" and "Saga!!") included.
//
// That is what separates the seed-wave shape the importer used to get wrong
// whatever the order: Hawke's Lost Fleet 1-3 and Campbell's 4-6 in ONE batch with
// no catalogue series found two groups; Hawke's founds `lost-fleet` and
// Campbell's is refused by it and founds `lost-fleet-2`. And a catalogue holding
// only Hawke's first volume - OPEN to any one row - is still not handed to a
// batch of six Campbell rows, which would make him its dominant author.

// nameClaim is one row's claim to a named series, as the resolution reads it.
type nameClaim struct {
	name string
	row  *SeriesRow
	// order is the claim's canonical tie-break key, built from the row's own
	// facts (never its input position), so every ordering of one batch resolves
	// alike.
	order string
	// work is the book the claim is for: rows sharing it (a title's per-region
	// sibling rows, an in-batch duplicate) are one member of the evidence, as the
	// catalogue counts one work once.
	work string
	// evidence says whether the claim will place a member if it lands - a row
	// the run drops, a claim stating no usable position, or a mode that places
	// nothing, is no evidence.
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
	// stepped are the same-named catalogued series the claim did not fit, and
	// chain where slug sits on the name's chain, when it founds a new one.
	stepped []string
	chain   int
}

// seriesCatalogue is what the resolution reads about the catalogue
// (SeriesAuthorIndex.catalogue builds it).
type seriesCatalogue struct {
	stored    func(slug string) (string, bool)
	redirects model.Redirects
	evidence  func(slug string) *seriesAuthors
	large     map[string]bool
}

// claimOrder is a claim's canonical key: its authors, titles and publishers and
// an identifier, none of which depends on where the row sat in its input.
func claimOrder(row *SeriesRow, id string) string {
	slugs := make([]string, 0, len(row.authors))
	for _, a := range row.authors {
		slugs = append(slugs, a.slug)
	}
	sort.Strings(slugs)
	return strings.Join(slugs, ",") + "\x00" + strings.Join(row.titles, "|") + "\x00" +
		strings.Join(row.publishers, "|") + "\x00" + id
}

// claimsOf is one row's claims as the resolution reads them: id is the row's
// identifier (the canonical tie-break), places whether the row will be written,
// and work the book it is for ("" reads it off the row: its first title and its
// individual authors).
func claimsOf(refs []seriesRef, row *SeriesRow, id string, places bool, work string) []nameClaim {
	order := claimOrder(row, id)
	if work == "" {
		work = rowWork(row)
	}
	out := make([]nameClaim, len(refs))
	for i, r := range refs {
		out[i] = nameClaim{name: r.name, row: row, order: order, work: work, evidence: places && r.seqOK}
	}
	return out
}

// rowWork is a row's book as far as its own facts say, for a caller that names
// none (the create path names the work it resolves, planner.rowWorkKey): its
// first stated title, cleaned as a work title is, and its individual authors,
// which a title's sibling rows share.
func rowWork(row *SeriesRow) string {
	var slugs []string
	for _, f := range row.individuals() {
		slugs = append(slugs, f.slug)
	}
	sort.Strings(slugs)
	title := ""
	for _, t := range row.titles {
		if t != "" {
			title = model.Slugify(cleanWorkTitle(t))
			break
		}
	}
	return "row:" + title + "\x00" + strings.Join(slugs, ",")
}

// resolveSeriesClaims resolves every claim of a batch; the result is parallel to
// claims.
func resolveSeriesClaims(cat seriesCatalogue, claims []nameClaim) []seriesTarget {
	out := make([]seriesTarget, len(claims))
	groups, keys := claimGroups(claims)
	allocated := map[string]map[string]bool{} // base -> slugs minted this batch
	for _, k := range keys {
		resolveSeriesGroup(cat, claims, groups[k], out, allocated)
	}
	return out
}

// claimGroups groups the claims (indexes) by the series name they state, the
// unit resolveSeriesGroup resolves, with the group keys sorted. A claim whose
// name has no addressable slug is in no group: its zero target is a refusal.
func claimGroups(claims []nameClaim) (map[string][]int, []string) {
	groups := map[string][]int{}
	for i, c := range claims {
		base := Slugify(c.name)
		if base == "" {
			continue
		}
		key := base + "\x00" + strings.ToLower(c.name)
		groups[key] = append(groups[key], i)
	}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return groups, keys
}

// seriesCandidate is a series a name's claims may land in: a catalogued one on
// its chain, or one the batch founds. Its evidence is the catalogue's own until
// the batch first extends it, and a private copy after (owned).
type seriesCandidate struct {
	slug, via string
	chain     int
	ev        *seriesAuthors
	owned     bool
}

// extend adds a claim's book to the candidate's evidence, when it places one.
func (c *seriesCandidate) extend(cl nameClaim) {
	if !cl.evidence {
		return
	}
	if !c.owned {
		c.ev, c.owned = c.ev.clone(), true
	}
	cl.row.prepare()
	c.ev.add(cl.work, cl.row.forms, cl.row.pubKeys)
}

// seriesCandidates walks a name's chain over the catalogue: every held slug whose
// name matches case-insensitively, and a retired BASE's live survivor, in chain
// order and each once. The walk ends at the first free slug - nothing beyond it
// can have been minted - and every other held or retired slug is occupied.
func seriesCandidates(cat seriesCatalogue, base, name string) []seriesCandidate {
	var out []seriesCandidate
	add := func(slug, via string) {
		if slices.ContainsFunc(out, func(c seriesCandidate) bool { return c.slug == slug }) {
			return
		}
		var ev *seriesAuthors
		if cat.evidence != nil {
			ev = cat.evidence(slug)
		}
		out = append(out, seriesCandidate{slug: slug, via: via, ev: ev})
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

// mintSlug is the next free slug on base's chain - not held, not retired, not
// already minted by this batch - and where it sits on the chain.
func mintSlug(cat seriesCatalogue, base string, allocated map[string]map[string]bool) (string, int) {
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
		return slug, i
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

	// 1. Anchor, in rounds judged against the evidence at the round's start.
	for {
		type anchor struct{ k, cand int }
		var round []anchor
		for k, ci := range idx {
			if placed[k] != unplaced {
				continue
			}
			for c := range cands {
				if cands[c].ev.fit(claims[ci].row, cat.large) == seriesShared {
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
			cands[a.cand].extend(claims[idx[a.k]])
		}
	}
	var rest []int // positions in idx
	for k, ci := range idx {
		if placed[k] == unplaced {
			rest = append(rest, k)
			continue
		}
		c := cands[placed[k]]
		out[ci] = seriesTarget{slug: c.slug, found: true, via: c.via}
	}
	if len(rest) == 0 {
		return
	}

	// 2. Cluster what is left by shared author: each cluster joins the first
	// catalogued candidate, or series this batch founded, that admits it.
	var stepped []string
	for _, c := range cands {
		stepped = append(stepped, c.slug)
	}
	var founded []*seriesCandidate
	for _, cl := range authorClusters(claims, idx, rest) {
		combined, add := clusterEvidence(claims, idx, cl)
		var home *seriesCandidate
		found := false
		for c := range cands {
			if cands[c].ev.admits(combined, add, cat.large) {
				home, found = &cands[c], true
				break
			}
		}
		if home == nil {
			for _, ns := range founded {
				if ns.ev.admits(combined, add, cat.large) {
					home = ns
					break
				}
			}
		}
		if home == nil {
			// 3. Mint.
			slug, chain := mintSlug(cat, base, allocated)
			home = &seriesCandidate{slug: slug, chain: chain, ev: &seriesAuthors{}, owned: true}
			founded = append(founded, home)
		}
		for _, k := range cl {
			home.extend(claims[idx[k]])
			if found {
				out[idx[k]] = seriesTarget{slug: home.slug, found: true, via: home.via}
			} else {
				out[idx[k]] = seriesTarget{slug: home.slug, stepped: stepped, chain: home.chain}
			}
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

// clusterEvidence is a cluster's claims read as one row - every author, title and
// publisher any of them states - and as the evidence the cluster would bring to
// a series it joins.
func clusterEvidence(claims []nameClaim, idx, cluster []int) (*SeriesRow, *seriesAuthors) {
	row := &SeriesRow{}
	add := &seriesAuthors{}
	seen := map[string]bool{}
	for _, k := range cluster {
		cl := claims[idx[k]]
		r := cl.row
		for _, a := range r.authors {
			if !seen[a.slug] {
				seen[a.slug] = true
				row.authors = append(row.authors, a)
			}
		}
		row.titles = append(row.titles, r.titles...)
		row.publishers = append(row.publishers, r.publishers...)
		if cl.evidence {
			r.prepare()
			add.add(cl.work, r.forms, r.pubKeys)
		}
	}
	return row, add
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
	if row == nil {
		row = &SeriesRow{}
	}
	t := resolveSeriesClaims(ix.catalogue(stored, reds), []nameClaim{{name: name, row: row, order: claimOrder(row, "")}})[0]
	return SeriesMatch{Slug: t.slug, Found: t.found, Via: t.via, Stepped: t.stepped}
}
