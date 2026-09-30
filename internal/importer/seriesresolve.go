package importer

import (
	"slices"
	"sort"
	"strings"

	"github.com/kodestar/audiosilo-meta/internal/titlerule"
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
//
// LANGUAGE is judged before authors, at every step (languageCloses): a candidate
// whose DERIVED language is known and is not the claim's is CLOSED to it, exactly
// as a series its authors do not fit is, whatever the authors say - Hurwitz's
// English "Orphan X" volume is not a member of the German "Orphan X", which is
// his series too. A catalogued candidate's language is the snapshot's
// (model.SeriesLanguageOf over its catalogued members, SeriesAuthorIndex), so no
// batch can flip it by arriving; a series the batch founds takes the language
// of the claims that founded or joined it (seriesCandidate.language, the same
// model.SeriesLanguage). Clusters are formed within one language, so German rows
// of one author claiming an English-catalogued name found ONE German series
// together rather than one each, and never share it with the English rows of the
// same batch. A tie ("") and a claim of unknown language are never judged. The
// rule was measured over the libex dump when it landed (see CLAUDE.md): the
// cross-language joins it refuses were the eight September sync-bot memberships
// that grew the mixed-language series L-MIX repairs.

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
	// why, and chain where slug sits on the name's chain, when it founds a new one.
	stepped []steppedSeries
	chain   int
	// name is the spelling a FOUNDED series is written under: the group's
	// canonical claim's (its lowest claimOrder), so when one group holds several
	// spellings of one name (case, or a respelled decoration - SameSeriesName) the
	// record does not take whichever row happened to be placed first. "" on a
	// joined or refused target.
	name string
}

// steppedSeries is a same-named catalogued series a claim did not join.
type steppedSeries struct {
	slug string
	// language is the series' derived language when THAT closed it to the claim
	// (languageCloses), "" when its authors did.
	language string
}

// steppedSlugs is the stepped series' slugs, in chain order.
func steppedSlugs(st []steppedSeries) []string {
	var out []string
	for _, s := range st {
		out = append(out, s.slug)
	}
	return out
}

// describeStepped says why a claim stepped past its same-named series, for the
// warnings and notes that report it: "a, b belongs to other authors" (the
// wording the author rule has always used), "c is in another language (de)",
// or both, separated by "; ".
func describeStepped(st []steppedSeries) string {
	var authors, langSlugs, langs []string
	for _, s := range st {
		if s.language == "" {
			authors = append(authors, s.slug)
			continue
		}
		langSlugs = append(langSlugs, s.slug)
		if !slices.Contains(langs, s.language) {
			langs = append(langs, s.language)
		}
	}
	var parts []string
	if len(authors) > 0 {
		parts = append(parts, strings.Join(authors, ", ")+" belongs to other authors")
	}
	if len(langSlugs) > 0 {
		parts = append(parts, strings.Join(langSlugs, ", ")+" is in another language ("+strings.Join(langs, ", ")+")")
	}
	return strings.Join(parts, "; ")
}

// languageCloses is the LANGUAGE half of series resolution: a series whose
// derived language is series is closed to a claim in language row iff both are
// known and their primary subtags differ (model.SameLanguage). A tie or an
// unknown series language ("") and an unknown row language are never judged.
func languageCloses(series, row string) bool {
	return series != "" && row != "" && !model.SameLanguage(series, row)
}

// seriesCatalogue is what the resolution reads about the catalogue
// (SeriesAuthorIndex.catalogue builds it).
type seriesCatalogue struct {
	stored    func(slug string) (string, bool)
	redirects model.Redirects
	evidence  func(slug string) *seriesAuthors
	// language is a catalogued series' derived language, "" when unknown or a
	// tie; nil judges no languages.
	language func(slug string) string
	large    map[string]bool
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
// unit resolveSeriesGroup resolves, with the group keys sorted. Two claims share
// a group exactly when titlerule.SameSeriesName calls their names one (the slug
// plus titlerule.SeriesNameKey, its grouping form), so a respelled decoration -
// "(German Edition)" beside "[German Edition]" - is one group rather than two
// that would each found a series. A claim whose name has no addressable slug is
// in no group: its zero target is a refusal.
func claimGroups(claims []nameClaim) (map[string][]int, []string) {
	groups := map[string][]int{}
	for i, c := range claims {
		base := Slugify(c.name)
		if base == "" {
			continue
		}
		key := base + "\x00" + titlerule.SeriesNameKey(c.name)
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
	// lang is a catalogued candidate's derived language, fixed at the snapshot.
	lang string
	// founded marks a series this batch founds, whose language is derived from
	// the books that founded or joined it instead: members (one per book, as the
	// evidence counts them) and each one's language.
	founded   bool
	members   []model.SeriesWork
	languages map[string]string
}

// language is the candidate's derived language: the snapshot's for a
// catalogued series (a batch never flips it), and model.SeriesLanguage over its
// own books for a founded one.
func (c *seriesCandidate) language() string {
	if !c.founded {
		return c.lang
	}
	return model.SeriesLanguage(c.members, func(work string) string { return c.languages[work] })
}

// closedTo reports whether the candidate's language closes it to cl
// (languageCloses).
func (c *seriesCandidate) closedTo(cl nameClaim) bool {
	return languageCloses(c.language(), cl.row.language)
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
	if c.founded {
		if _, seen := c.languages[cl.work]; !seen {
			c.members = append(c.members, model.SeriesWork{Work: cl.work})
			c.languages[cl.work] = cl.row.language
		}
	}
}

// seriesCandidates walks a name's chain over the catalogue: every held slug whose
// name is the same series name (titlerule.SameSeriesName: case, and a bracketed
// decoration's bracket style and surrounding spacing, are not identity - a
// different decoration still is), and a retired BASE's live survivor, in chain
// order and each once. The walk ends at the first free slug - nothing beyond it
// can have been minted - and every other held or retired slug is occupied.
func seriesCandidates(cat seriesCatalogue, base, name string) []seriesCandidate {
	var out []seriesCandidate
	named := titlerule.NewSeriesName(name) // prepared once: every held name on the chain is compared to it
	add := func(slug, via string) {
		if slices.ContainsFunc(out, func(c seriesCandidate) bool { return c.slug == slug }) {
			return
		}
		var ev *seriesAuthors
		if cat.evidence != nil {
			ev = cat.evidence(slug)
		}
		var lang string
		if cat.language != nil {
			lang = cat.language(slug)
		}
		out = append(out, seriesCandidate{slug: slug, via: via, ev: ev, lang: lang})
	}
	for i := 0; ; i++ {
		slug := SeriesSlugAt(base, i)
		if held, exists := cat.stored(slug); exists {
			if named.Same(held) {
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
				if cands[c].closedTo(claims[ci]) {
					continue
				}
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

	// 2. Cluster what is left by shared author, within one language: each
	// cluster joins the first catalogued candidate, or series this batch
	// founded, that its language does not close and that admits it.
	var founded []*seriesCandidate
	for _, cl := range authorClusters(claims, idx, rest) {
		combined, add := clusterEvidence(claims, idx, cl)
		lead := claims[idx[cl[0]]] // every claim of a cluster states one language
		var home *seriesCandidate
		found := false
		for c := range cands {
			if !cands[c].closedTo(lead) && cands[c].ev.admits(combined, add, cat.large) {
				home, found = &cands[c], true
				break
			}
		}
		if home == nil {
			for _, ns := range founded {
				if !ns.closedTo(lead) && ns.ev.admits(combined, add, cat.large) {
					home = ns
					break
				}
			}
		}
		var stepped []steppedSeries
		if !found {
			for c := range cands {
				s := steppedSeries{slug: cands[c].slug}
				if cands[c].closedTo(lead) {
					s.language = cands[c].language()
				}
				stepped = append(stepped, s)
			}
		}
		if home == nil {
			// 3. Mint.
			slug, chain := mintSlug(cat, base, allocated)
			home = &seriesCandidate{slug: slug, chain: chain, ev: &seriesAuthors{}, owned: true,
				founded: true, languages: map[string]string{}}
			founded = append(founded, home)
		}
		for _, k := range cl {
			home.extend(claims[idx[k]])
			if found {
				out[idx[k]] = seriesTarget{slug: home.slug, found: true, via: home.via}
			} else {
				out[idx[k]] = seriesTarget{slug: home.slug, stepped: stepped, chain: home.chain, name: name}
			}
		}
	}
}

// authorClusters groups the claims at positions rest (into idx) by shared author,
// transitively, and orders the groups largest first, ties by their smallest
// canonical key. A claim with no individual author is a group of its own, and
// claims of different languages are never grouped.
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
	// One representative claim per author slug IN ONE LANGUAGE, then the
	// spelling rungs across the distinct slugs of that language: a cluster never
	// spans two languages (languageCloses judges it by its lead claim), so one
	// author's German and English rows are two clusters.
	type authorKey struct{ lang, slug string }
	bySlug := map[authorKey]int{}
	var keys []authorKey
	forms := map[authorKey]personForm{}
	for _, k := range rest {
		lang := claims[idx[k]].row.language
		for _, f := range claims[idx[k]].row.individuals() {
			key := authorKey{lang, f.slug}
			if first, ok := bySlug[key]; ok {
				union(k, first)
				continue
			}
			bySlug[key] = k
			keys = append(keys, key)
			forms[key] = f
		}
	}
	sort.Slice(keys, func(a, b int) bool {
		if keys[a].lang != keys[b].lang {
			return keys[a].lang < keys[b].lang
		}
		return keys[a].slug < keys[b].slug
	})
	for i := range keys {
		for j := i + 1; j < len(keys) && keys[j].lang == keys[i].lang; j++ {
			if forms[keys[i]].same(forms[keys[j]]) {
				union(bySlug[keys[i]], bySlug[keys[j]])
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
	// stepped carries why each was stepped past (Why).
	stepped []steppedSeries
}

// Why says why the row stepped past its same-named series, in the words the
// importer's own warnings use: "a, b belongs to other authors", "c is in
// another language (de)", or both; "" when nothing was stepped past.
func (m SeriesMatch) Why() string { return describeStepped(m.stepped) }

// Resolve is resolveSeriesClaims for one row's claim to name: stored reports the
// name a series slug holds, reds is the tombstone table. A nil index judges no
// authors and no languages (the name-only walk).
func (ix *SeriesAuthorIndex) Resolve(name string, reds model.Redirects, stored func(slug string) (string, bool), row *SeriesRow) SeriesMatch {
	if row == nil {
		row = &SeriesRow{}
	}
	t := resolveSeriesClaims(ix.catalogue(stored, reds), []nameClaim{{name: name, row: row, order: claimOrder(row, "")}})[0]
	return SeriesMatch{Slug: t.slug, Found: t.found, Via: t.via, Stepped: steppedSlugs(t.stepped), stepped: t.stepped}
}
