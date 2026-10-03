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
	// language is each catalogued series' derived language, holding only the
	// known ones; nil (a lookup answers "") judges no languages.
	language map[string]string
	large    map[string]bool
	// qualified is the qualifier index (seriesqualified.go), built on first
	// call; nil reaches nothing beyond the chain.
	qualified func() qualifiedIndex
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
	for _, unit := range seriesUnits(cat, claims, groups, keys) {
		members := make([][]int, len(unit))
		for i, k := range unit {
			members[i] = groups[k]
		}
		resolveSeriesUnit(cat, claims, members, out, allocated)
	}
	return out
}

// seriesUnits partitions the name groups (keys, sorted) into RESOLUTION UNITS: the
// groups that can reach one catalogued series - on a chain, or through the
// qualifier index (seriesqualified.go) - are one unit, transitively, so the
// spellings of one series ("Saga [German Edition]", "Saga (Deutsche Ausgabe)", a
// German "Saga") are judged against ONE evidence for it, exactly as one spelling
// would be. Each unit lists its keys in order, and the units are ordered by their
// first key. A batch of one group is one unit without asking the index.
func seriesUnits(cat seriesCatalogue, claims []nameClaim, groups map[string][]int, keys []string) [][]string {
	if len(keys) < 2 {
		if len(keys) == 0 {
			return nil
		}
		return [][]string{keys}
	}
	parent := make([]int, len(keys))
	for i := range parent {
		parent[i] = i
	}
	find := func(x int) int {
		for parent[x] != x {
			parent[x] = parent[parent[x]]
			x = parent[x]
		}
		return x
	}
	owner := map[string]int{} // catalogued slug -> the first group reaching it
	for i, k := range keys {
		idx := groups[k]
		name := claims[idx[0]].name
		chain := seriesCandidates(cat, Slugify(name), name, nil)
		for _, c := range append(chain, qualifiedCandidates(cat, name, claims, idx, chain, nil)...) {
			first, seen := owner[c.slug]
			if !seen {
				owner[c.slug] = i
				continue
			}
			if a, b := find(first), find(i); a != b {
				parent[max(a, b)] = min(a, b)
			}
		}
	}
	var out [][]string
	at := map[int]int{}
	for i, k := range keys {
		r := find(i)
		u, ok := at[r]
		if !ok {
			u = len(out)
			at[r] = u
			out = append(out, nil)
		}
		out[u] = append(out[u], k)
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
		key := titlerule.SeriesNameGroupKey(c.name)
		if key == "" {
			continue
		}
		groups[key] = append(groups[key], i)
	}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return groups, keys
}

// candidateTier is a candidate's precedence: a cluster joins the first candidate
// that admits it in tier order, so a tier is only ever tried when every earlier
// one declined.
type candidateTier int

const (
	// tierChain: a catalogued series on the name's own slug chain.
	tierChain candidateTier = iota
	// tierDecorated: a catalogued series the qualifier index reached under the
	// claim's own edition decoration (seriesqualified.go).
	tierDecorated
	// tierLanguage: one it reached under a row's language (the facet).
	tierLanguage
	// tierFounded: a series this batch founds.
	tierFounded
)

// seriesCandidate is a series a name's claims may land in: a catalogued one on
// its chain or reached by its qualifiers, or one the batch founds. Its evidence
// is the catalogue's own until the batch first extends it, and a private copy
// after (owned).
type seriesCandidate struct {
	slug, via string
	chain     int
	tier      candidateTier
	// facet is the row language a tierLanguage candidate serves: only a claim
	// stating it reaches the candidate.
	facet string
	// pool is the candidate's evidence, SHARED by every candidate of the same
	// catalogued series in one resolution unit (resolveSeriesUnit), so a row one
	// name group places there is evidence every other group judges it by.
	pool *evidencePool
	// lang is a catalogued candidate's derived language, fixed at the snapshot.
	lang string
	// languages is a founded series' books' languages, keyed by book (one entry
	// per book, as the evidence counts them), which its language derives from.
	languages map[string]string
	// foundedLang memoizes language() for a founded candidate; extend clears it.
	foundedLang *string
}

// named reports whether the claim's name spells the candidate - the chain's, or
// one its own decoration reached - which is what a claim that founds instead
// reports as stepped past.
func (c *seriesCandidate) named() bool { return c.tier <= tierDecorated }

// reaches reports whether cl reaches the candidate at all: a tierLanguage one
// only by a claim in its facet.
func (c *seriesCandidate) reaches(cl nameClaim) bool {
	return c.tier != tierLanguage || c.facet == cl.row.language
}

// language is the candidate's derived language: the snapshot's for a
// catalogued series (a batch never flips it), and model.SeriesLanguage over its
// own books for a founded one.
func (c *seriesCandidate) language() string {
	if c.tier != tierFounded {
		return c.lang
	}
	if c.foundedLang == nil {
		members := make([]model.SeriesWork, 0, len(c.languages))
		for w := range c.languages {
			members = append(members, model.SeriesWork{Work: w})
		}
		lang := model.SeriesLanguage(members, func(work string) string { return c.languages[work] })
		c.foundedLang = &lang
	}
	return *c.foundedLang
}

// closedTo reports whether the candidate's language closes it to cl
// (languageCloses).
func (c *seriesCandidate) closedTo(cl nameClaim) bool {
	return languageCloses(c.language(), cl.row.language)
}

// admits reports whether a cluster led by lead may join the candidate: lead
// reaches it, its language does not close it, and its evidence admits the
// cluster.
func (c *seriesCandidate) admits(lead nameClaim, row *SeriesRow, add *seriesAuthors, large map[string]bool) bool {
	return c.reaches(lead) && !c.closedTo(lead) && c.pool.ev.admits(row, add, large)
}

// extend adds a claim's book to the candidate's evidence, when it places one.
func (c *seriesCandidate) extend(cl nameClaim) {
	if !cl.evidence {
		return
	}
	if !c.pool.owned {
		c.pool.ev, c.pool.owned = c.pool.ev.clone(), true
	}
	cl.row.prepare()
	c.pool.ev.add(cl.work, cl.row.forms, cl.row.pubKeys)
	if c.tier == tierFounded {
		c.languages[cl.work] = cl.row.language
		c.foundedLang = nil
	}
}

// evidencePool is one series' evidence as a resolution unit extends it: the
// catalogue's own until the unit first places a row there, a private copy after
// (owned).
type evidencePool struct {
	ev    *seriesAuthors
	owned bool
}

// evidencePools is a resolution unit's pools, one per catalogued series slug, so
// every candidate of one series - reached by any name group of the unit - shares
// one. A nil set hands out a fresh pool per call (a reach that only asks which
// series a name reaches).
type evidencePools map[string]*evidencePool

// of is slug's pool, created from the catalogue's evidence on first use.
func (p evidencePools) of(cat seriesCatalogue, slug string) *evidencePool {
	if e := p[slug]; e != nil {
		return e
	}
	e := &evidencePool{}
	if cat.evidence != nil {
		e.ev = cat.evidence(slug)
	}
	if p != nil {
		p[slug] = e
	}
	return e
}

// seriesCandidates walks a name's chain over the catalogue: every held slug whose
// name is the same series name (titlerule.SameSeriesName: case, and a bracketed
// decoration's bracket style and surrounding spacing, are not identity - a
// different decoration still is), and a retired BASE's live survivor, in chain
// order and each once. The walk ends at the first free slug - nothing beyond it
// can have been minted - and every other held or retired slug is occupied.
func seriesCandidates(cat seriesCatalogue, base, name string, pools evidencePools) []*seriesCandidate {
	var out []*seriesCandidate
	named := titlerule.NewSeriesName(name) // prepared once: every held name on the chain is compared to it
	add := func(slug, via string) {
		if slices.ContainsFunc(out, func(c *seriesCandidate) bool { return c.slug == slug }) {
			return
		}
		out = append(out, &seriesCandidate{slug: slug, via: via, pool: pools.of(cat, slug), lang: cat.language[slug], tier: tierChain})
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

// nameGroup is one name group of a resolution unit: its claims (in canonical
// order) and its own candidates, by tier.
type nameGroup struct {
	name, base string
	idx        []int
	cands      []*seriesCandidate
	// chainEmpty: the name's chain holds no candidate. reached: the qualifier
	// index has been asked for this group.
	chainEmpty, reached bool
}

// reach adds what the qualifier index reaches for the group's claims at idx.
func (g *nameGroup) reach(cat seriesCatalogue, claims []nameClaim, idx []int, pools evidencePools) {
	if !g.reached {
		g.reached = true
		g.cands = append(g.cands, qualifiedCandidates(cat, g.name, claims, idx, g.cands, pools)...)
	}
}

// anchors reports whether c may anchor a claim of the group: a candidate the name
// spells - the chain, or, where the chain holds nothing, what the name's own
// decoration reaches, which stands in for it (a held edition series renamed to its
// plain base sits on a slug whose stored name no longer equals the claim's, and
// the rename must be invisible to the outcome). A row-language reach is tried in
// the cluster step alone.
func (g *nameGroup) anchors(c *seriesCandidate) bool {
	return c.tier == tierChain || (c.tier == tierDecorated && g.chainEmpty)
}

// resolveSeriesUnit resolves the claims of one resolution unit (seriesUnits):
// members are its name groups' claims, in unit order. Every name group keeps its
// own candidates and tiers - its own chain first - but the candidates of one
// catalogued series share ONE evidence pool, and anchoring and clustering run over
// the unit's claims together, so a series several spellings reach admits exactly
// what it would admit were they one spelling. A unit of one group is the original
// per-name resolution exactly.
func resolveSeriesUnit(cat seriesCatalogue, claims []nameClaim, members [][]int, out []seriesTarget, allocated map[string]map[string]bool) {
	byOrder := func(idx []int) func(a, b int) bool {
		return func(a, b int) bool { return claims[idx[a]].order < claims[idx[b]].order }
	}
	pools := evidencePools{}
	groups := make([]*nameGroup, len(members))
	groupOf := map[int]*nameGroup{}
	var all []int // the unit's claims, canonical order
	for i, idx := range members {
		sort.SliceStable(idx, byOrder(idx))
		g := &nameGroup{name: claims[idx[0]].name, idx: idx}
		g.base = Slugify(g.name)
		g.cands = seriesCandidates(cat, g.base, g.name, pools)
		g.chainEmpty = len(g.cands) == 0
		if g.chainEmpty && titlerule.ReadSeriesQualifiers(g.name).Language != "" {
			g.reach(cat, claims, idx, pools)
		}
		groups[i] = g
		for _, ci := range idx {
			groupOf[ci] = g
		}
		all = append(all, idx...)
	}
	sort.SliceStable(all, byOrder(all))

	// 1. Anchor, in rounds judged against the evidence at the round's start.
	placed := map[int]*seriesCandidate{}
	for {
		type anchor struct {
			ci   int
			cand *seriesCandidate
		}
		var round []anchor
		for _, ci := range all {
			if placed[ci] != nil {
				continue
			}
			g := groupOf[ci]
			for _, c := range g.cands {
				if !g.anchors(c) || c.closedTo(claims[ci]) {
					continue
				}
				if c.pool.ev.fit(claims[ci].row, cat.large) == seriesShared {
					round = append(round, anchor{ci, c})
					break
				}
			}
		}
		if len(round) == 0 {
			break
		}
		for _, a := range round {
			placed[a.ci] = a.cand
			a.cand.extend(claims[a.ci])
		}
	}
	var rest []int // positions in all
	for k, ci := range all {
		if c := placed[ci]; c != nil {
			out[ci] = seriesTarget{slug: c.slug, found: true, via: c.via}
			continue
		}
		rest = append(rest, k)
	}
	if len(rest) == 0 {
		return
	}
	restOf := func(g *nameGroup) []int {
		var idx []int
		for _, k := range rest {
			if groupOf[all[k]] == g {
				idx = append(idx, all[k])
			}
		}
		return idx
	}
	if len(groups) > 1 {
		// A cluster's evidence for a series counts every claim of it that can
		// reach that series, so every group's reach is known before the first
		// cluster is judged. (One group asks the index lazily, below.)
		for _, g := range groups {
			g.reach(cat, claims, restOf(g), pools)
		}
	}

	// 2. Cluster what is left by shared author, within one language and across
	// the unit's spellings. Each cluster's claims are placed group by group: a
	// group joins the first of ITS candidates, by tier, that admits the cluster -
	// judged against everything in the cluster that can reach that series - else
	// founds on its own name's chain.
	for _, cl := range authorClusters(claims, all, rest) {
		type clusterView struct {
			row *SeriesRow
			add *seriesAuthors
		}
		views := map[*seriesCandidate]clusterView{}
		admits := func(c *seriesCandidate, lead nameClaim) bool {
			v, ok := views[c]
			if !ok {
				var reaching []int
				for _, k := range cl {
					ci := all[k]
					if slices.ContainsFunc(groupOf[ci].cands, func(o *seriesCandidate) bool {
						return o.slug == c.slug && o.reaches(claims[ci])
					}) {
						reaching = append(reaching, k)
					}
				}
				v.row, v.add = clusterEvidence(claims, all, reaching)
				views[c] = v
			}
			return c.admits(lead, v.row, v.add, cat.large)
		}
		for _, g := range groups {
			var sc []int // positions in all
			for _, k := range cl {
				if groupOf[all[k]] == g {
					sc = append(sc, k)
				}
			}
			if len(sc) == 0 {
				continue
			}
			lead := claims[all[sc[0]]] // every claim of a cluster states one language
			home := admitting(g.cands, tierChain, tierChain, func(c *seriesCandidate) bool { return admits(c, lead) })
			if home == nil && !g.reached {
				g.reach(cat, claims, restOf(g), pools)
			}
			if home == nil {
				home = admitting(g.cands, tierDecorated, tierFounded, func(c *seriesCandidate) bool { return admits(c, lead) })
			}
			found := home != nil && home.tier != tierFounded
			var stepped []steppedSeries
			if !found {
				for _, c := range g.cands {
					if !c.named() {
						continue
					}
					s := steppedSeries{slug: c.slug}
					if c.closedTo(lead) {
						s.language = c.language()
					}
					stepped = append(stepped, s)
				}
			}
			if home == nil {
				// 3. Mint.
				slug, chain := mintSlug(cat, g.base, allocated)
				home = &seriesCandidate{slug: slug, chain: chain, tier: tierFounded,
					pool: &evidencePool{ev: &seriesAuthors{}, owned: true}, languages: map[string]string{}}
				g.cands = append(g.cands, home)
			}
			for _, k := range sc {
				ci := all[k]
				home.extend(claims[ci])
				if found {
					out[ci] = seriesTarget{slug: home.slug, found: true, via: home.via}
				} else {
					out[ci] = seriesTarget{slug: home.slug, stepped: stepped, chain: home.chain, name: g.name}
				}
			}
		}
	}
}

// admitting is the first candidate in tiers lo..hi, by tier and then list order,
// that admits reports admitting, or nil.
func admitting(cands []*seriesCandidate, lo, hi candidateTier, admits func(*seriesCandidate) bool) *seriesCandidate {
	for t := lo; t <= hi; t++ {
		for _, c := range cands {
			if c.tier == t && admits(c) {
				return c
			}
		}
	}
	return nil
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
