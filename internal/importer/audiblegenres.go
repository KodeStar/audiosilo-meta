package importer

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
)

// audiblegenres.go owns the retailer-genre-string -> project-vocabulary mapping.
// LICENSING.md ("Genres") forbids storing a retailer's genre taxonomy verbatim:
// its node names, hierarchy and typed tags are that retailer's editorial
// arrangement. So the importer never emits a source genre string - it maps it
// onto the controlled vocabulary (schema/common.schema.json #/$defs/genre) in
// code and DROPS anything that does not map. The table below is that mapping,
// kept as data (one file, reviewable) rather than a Go literal.
//
// The table is source-neutral: its keys are Audible's own browse-node ids and
// category names, so any source mirroring Audible's taxonomy (libex today,
// another mirror tomorrow) resolves through the same file.
//
// Lookup order for one claim is by browse-node id first (stable across locales,
// so a German node maps without depending on its localized name), then by the
// category PATH, then by the lowercased/trimmed name. by_asin (the on-disk key
// name; its keys are browse-node ids, not product ASINs) carries the nodes whose
// NAME is ambiguous in the taxonomy - "History" under Fiction is not the History
// genre, and "Contemporary" is contemporary-romance only under Romance - and the
// localized nodes worth pinning; by_name carries the ~1,200 Audible category
// strings observed across the marketplaces libex mirrors.
//
// by_path is for the sources that state a category by its NAMES rather than its
// node id: an OpenAudible books.json carries one colon-joined ladder per book
// ("Romance:Contemporary"), and its leaf name alone cannot say what the node
// would have said. It is keyed by MARKETPLACE, then by the lowercased, trimmed
// segments joined with ":" (GenrePathKey), and the path lookup is
//
//	by_path[region][path]  else  by_path["us"][path]  else  by_name[leaf]
//
// with region the row's own marketplace. An entry's value is the path's NODE ID,
// resolved like any node (ResolveGenreNode: by_asin, else by_name of the leaf),
// so re-pinning a node in by_asin reaches every path naming it with no
// regeneration; a node that resolves to nothing stops the lookup there (a
// suppression, so an English path the US pins cannot override a marketplace
// whose own node is unmapped). It is DERIVED, never hand-authored:
// scripts/genrepaths walks every marketplace's taxonomy (libex's /categories)
// and writes an entry exactly where that chain would otherwise answer
// differently from the path's node in that marketplace. So a path-stating
// source lands exactly where a node-stating one does for the same marketplace,
// and a marketplace whose taxonomy lacks the path gets the US answer.
// testdata/genrepaths.json is the generator's verification file, which
// TestGenrePathsMatchTheirNodes re-checks against the node table.
//
// A CHILDREN'S category never produces adult ADVICE vocabulary. Audible files a
// children's book under subject tags that read like adult self-help when you
// look at the leaf alone - "Social & Life Skills", "Difficult Discussions" and
// "Family Life" all sit under Children's Audiobooks - and mapping those onto
// self-help / parenting-relationships / business labelled Anne of Green Gables
// a self-help book and a parenting guide. So a node whose taxonomy path is
// rooted at Children's Audiobooks (Kinder-Hörbücher, Audiolibros infantiles,
// Jeunesse, ...) maps only to what is TRUE of a children's book - childrens,
// coming-of-age, or the same TOPIC an adult book would get (Math -> science,
// Government -> politics) - and otherwise maps to nothing at all. Note the
// mechanism: an override cannot SUPPRESS a claim, it can only redirect it (a
// node the table does not carry falls through to its name), so suppressing a
// children's leaf means dropping BOTH its node id and its name, and a name that
// an adult node legitimately shares ("Careers") is neutralized by pinning each
// children's node id to childrens instead.
//
// TestChildrensClaimsAvoidAdultAdviceGenres pins the rule; the children's node
// ids it names came from libex's own /categories taxonomy for every marketplace
// the table covers.
//
// A FORMAT node says how a book was PRODUCED, not what it is about. Audible's
// Arts & Entertainment tree carries a handful of them - Audio Performances &
// Dramatizations (and its Dramatizations and Storytelling children), and under
// Entertainment & Performing Arts the Radio and Film & TV leaves, with their
// equivalents in every marketplace - and it files a full-cast BBC radio
// dramatization of an Agatha Christie novel there beside Mystery. Every one of
// them maps to arts-entertainment (Storytelling to writing-publishing), and a
// book stating a leaf also states its ancestors (the Arts & Entertainment root,
// Entertainment & Performing Arts), which map to arts-entertainment too. So a
// Christie dramatization was an arts book. The fiction-side "Dramatizations"
// nodes (Mystery > Dramatizations, ...) already map to nothing; this is the rule
// that makes the Arts & Entertainment side agree.
//
// The rule is ROW-LEVEL (mapGenres): a claim that is FORMAT-DERIVED yields its
// genre only when the row maps to NO other genre. A claim is format-derived when
// it is a format node, or an ancestor of a format node the row states, AND the
// row states no non-format descendant of it - an ancestor the row also reaches
// through a subject node ("Art", "Music", "Theater") is justified by that subject
// and keeps its genre. So the full-cast Christie (mystery, thriller) drops
// arts-entertainment, a radio panel show whose row maps nothing else keeps it,
// and a row also stating Arts & Entertainment > Art keeps it through Art.
//
// The data is three keys of the table. "format" is the hand-curated list of
// format node ids across every marketplace (a decision, like by_asin). The other
// two are DERIVED from it by scripts/genrepaths (DeriveFormatTree), already in
// the shape the rule consumes, so the rule does map lookups and nothing else:
// "format_tree" maps every node of every root subtree holding a format node to
// whether it is a format node and its ancestors (transitively closed, over every
// path the node sits at - Opera is under both Entertainment & Performing Arts
// and Music), and "format_paths" maps, per marketplace, every path of those
// subtrees to its node, for a source stating a LADDER of names rather than node
// ids (the claim's marketplace, else the US table, as by_path falls back). A
// claim outside the tree is never format-derived and costs one map lookup.
// TestFormatNodesArePinned pins the format list per marketplace by path, and
// TestFormatTreeMatchesGenrePaths re-derives the two keys from the verification
// file.

//go:embed audiblegenres.json
var audibleGenresFS embed.FS

// genreTable maps source genre claims onto the project's genre vocabulary.
type genreTable struct {
	// ByASIN is keyed by browse-node id (the JSON key stays "by_asin", the name
	// the exports use for the field).
	ByASIN map[string]string `json:"by_asin"`
	ByName map[string]string `json:"by_name"`
	// ByPath is keyed by marketplace, then by GenrePathKey (see the file
	// comment); a value is the browse-node id the path names, resolved through
	// ResolveGenreNode.
	ByPath map[string]map[string]string `json:"by_path"`
	// Format is the hand-curated list of FORMAT browse-node ids (see the file
	// comment), across every marketplace: the generator's input, not read by the
	// rule itself.
	Format []string `json:"format"`
	// FormatTree and FormatPaths are DERIVED by scripts/genrepaths, never
	// hand-authored (DeriveFormatTree): every node of a root subtree holding a
	// format node, and every path of those subtrees per marketplace. A table
	// without them (a test's bare literal) has the format rule off.
	FormatTree  map[string]FormatNode        `json:"format_tree"`
	FormatPaths map[string]map[string]string `json:"format_paths"`
	// memo caches name resolution keyed by the RAW claim name, so a bulk import
	// lowercases/trims each distinct spelling once instead of once per book. It
	// is bounded by the number of distinct names in the input (a retailer
	// taxonomy, so thousands at most) and records a miss as "". It is optional -
	// a table without one simply resolves every time - and it is PER RUN
	// (withRunMemo): the parsed table itself is a process-wide singleton, so a
	// memo on it would be a data race the moment two imports overlap.
	memo map[string]string
}

// genreClaim is one raw genre claim from a source row: the retailer's
// browse-node id (when it states one), and its display name. A source that
// states the category LADDER rather than the node (pathGenreClaims) also sets
// path (already in GenrePathKey form, so a lookup never re-normalizes it),
// region (the row's marketplace, which picks the by_path table) and ladder (the
// whole ladder as the source spelled it, which is what the unmapped report
// names). Both Audible "Genres" and "Tags" nodes are eligible claims - the
// mapping table, not the node type, decides what becomes a vocabulary genre.
type genreClaim struct {
	node   string
	name   string
	path   string
	region string
	ladder string
}

// GenrePathKey normalizes a category path for by_path: each ":"-separated
// segment trimmed and lowercased (the name rule), empty segments dropped,
// rejoined with ":". "" when nothing is left. Exported for scripts/genrepaths,
// which must key the table exactly as the lookup does.
func GenrePathKey(path string) string {
	segs := strings.Split(path, ":")
	out := segs[:0]
	for _, s := range segs {
		if s = strings.ToLower(strings.TrimSpace(s)); s != "" {
			out = append(out, s)
		}
	}
	return strings.Join(out, ":")
}

// pathGenreClaims lifts a colon-joined category ladder ("Science Fiction &
// Fantasy:Fantasy:Epic", OpenAudible's genre field) stated in marketplace region
// into one claim per level - the root, the root's child, ... the leaf - each
// carrying its normalized path from the root and its own name. Every level is a
// claim because a node-stating source (libex) states every level too, so the
// two resolve to the same set. Empty segments are dropped; an empty ladder
// yields nil.
func pathGenreClaims(ladder, region string) []genreClaim {
	var segs []string
	for _, s := range strings.Split(ladder, ":") {
		// Each level is decoded AFTER the split, so an escaped colon cannot
		// become a level boundary (entities.go).
		if s = strings.TrimSpace(DecodeHTMLEntities(s)); s != "" {
			segs = append(segs, s)
		}
	}
	if len(segs) == 0 {
		return nil
	}
	display := strings.Join(segs, ":")
	claims := make([]genreClaim, 0, len(segs))
	for i := range segs {
		claims = append(claims, genreClaim{
			name: segs[i], path: GenrePathKey(strings.Join(segs[:i+1], ":")), region: region, ladder: display,
		})
	}
	return claims
}

// ResolveGenreNode is what a browse node answers: its by_asin pin, else its own
// (leaf) name through by_name, else "" (maps to nothing). leaf is the name in
// lowercased, trimmed form. It is the ONE statement of a node's answer, shared by
// the by_path lookup, scripts/genrepaths and the drift test.
func ResolveGenreNode(byASIN, byName map[string]string, node, leaf string) string {
	if g, ok := byASIN[node]; ok {
		return g
	}
	return byName[leaf]
}

// audibleGenreTable returns the embedded mapping table, parsed once. A parse
// failure is a build-time programming error (the file is embedded and covered by
// TestAudibleGenreTable), so it panics rather than silently importing every book
// with no genres.
var audibleGenreTable = sync.OnceValue(func() genreTable {
	t, err := loadGenreTable()
	if err != nil {
		panic(fmt.Sprintf("importer: embedded audiblegenres.json is invalid: %v", err))
	}
	return t
})

// loadGenreTable parses the embedded table. Split out from audibleGenreTable so
// a test can assert the parse (and the enum drift guard) without a panic.
func loadGenreTable() (genreTable, error) {
	data, err := audibleGenresFS.ReadFile("audiblegenres.json")
	if err != nil {
		return genreTable{}, err
	}
	var t genreTable
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&t); err != nil {
		return genreTable{}, err
	}
	return t, nil
}

// withRunMemo returns a copy of the table carrying a fresh, private name
// memo (see genreTable.memo). The maps holding the mapping itself stay shared -
// they are read-only.
func (t genreTable) withRunMemo() genreTable {
	t.memo = map[string]string{}
	return t
}

// lookup resolves one genre claim to a vocabulary slug: the browse-node id
// first (trimmed; node ids are numeric, so there is no case to fold), then the
// category path in the claim's marketplace (lookupPath), then the
// lowercased/trimmed display name. ok is false for a claim that does not map -
// the caller drops it (and reports it once per run).
func (t genreTable) lookup(c genreClaim) (string, bool) {
	if node := strings.TrimSpace(c.node); node != "" {
		if g, ok := t.ByASIN[node]; ok {
			return g, true
		}
	}
	if c.path != "" {
		if g, found := t.lookupPath(c.region, c.path); found {
			return g, g != ""
		}
	}
	return t.lookupName(c.name)
}

// lookupPath is the by_path half of lookup: the claim's own marketplace first,
// then the US table (the fallback the generator derives every other marketplace
// against), key in GenrePathKey form, the node found resolved through
// ResolveGenreNode. found reports that the table DECIDED the path - including a
// node that resolves to nothing, which must stop the lookup rather than fall
// through to the leaf name.
func (t genreTable) lookupPath(region, key string) (g string, found bool) {
	node, found := t.ByPath[region][key]
	if !found && region != "us" {
		node, found = t.ByPath["us"][key]
	}
	if !found {
		return "", false
	}
	return ResolveGenreNode(t.ByASIN, t.ByName, node, key[strings.LastIndex(key, ":")+1:]), true
}

// lookupName resolves a raw display name, through the memo when the table has
// one (see genreTable.memo). A vocabulary slug is never empty, so a memoized ""
// is exactly a remembered miss.
func (t genreTable) lookupName(raw string) (string, bool) {
	if g, cached := t.memo[raw]; cached {
		return g, g != ""
	}
	key := strings.ToLower(strings.TrimSpace(raw))
	if key == "" {
		return "", false
	}
	g := t.ByName[key]
	if t.memo != nil {
		t.memo[raw] = g
	}
	return g, g != ""
}

// mapGenres resolves a row's genre claims to a sorted, deduplicated slice of
// vocabulary slugs (sorted because checkGenresSorted pins the order). An
// unmapped claim is recorded in unmapped so the run can report each distinct
// string exactly once, at two granularities:
//
//   - a node-stating claim (libex) is reported by its display name, else its
//     node id;
//   - a LADDER claim is reported only when NO level of its ladder mapped, and
//     then by the whole ladder. Every level of a ladder is a claim, and the
//     umbrella levels ("Literature & Fiction", "Science Fiction & Fantasy") are
//     unmapped on purpose, so reporting them per level would name them on every
//     run; a ladder that yielded nothing is what a maintainer can act on.
//
// It is called only where the result is stored, so a row whose genres would
// never persist adds no noise to that report.
//
// The FORMAT rule (see the file comment) is applied here, over the whole row: a
// format-derived claim's genre is kept only when no other claim of the row maps
// to anything. It changes which MAPPED genres are kept, never what is reported as
// unmapped.
func (t genreTable) mapGenres(claims []genreClaim, unmapped map[string]bool) []string {
	if len(claims) == 0 {
		return nil
	}
	// A row states at most one ladder (every claim of a pathGenreClaims slice
	// carries the same one), so one name and one hit flag cover it.
	ladder, hit := "", false
	slugs := make([]string, len(claims))
	for i, c := range claims {
		slug, ok := t.lookup(c)
		if c.ladder != "" {
			ladder, hit = c.ladder, hit || ok
		}
		if !ok {
			if c.ladder == "" {
				if label := strings.TrimSpace(firstNonEmpty(c.name, c.node)); label != "" {
					unmapped[label] = true
				}
			}
			continue
		}
		slugs[i] = slug
	}
	if ladder != "" && !hit {
		unmapped[ladder] = true
	}
	derived := t.formatDerived(claims)
	out := distinctSorted(slugs, derived)
	if len(out) == 0 && derived != nil {
		// Nothing but format-derived claims mapped: the row is about its format
		// (a radio panel show), so the format genre is all it has to say.
		out = distinctSorted(slugs, nil)
	}
	return out
}

// distinctSorted is the sorted, duplicate-free set of the non-empty slugs whose
// skip flag is not set (nil skips nothing).
func distinctSorted(slugs []string, skip []bool) []string {
	seen := map[string]bool{}
	var out []string
	for i, slug := range slugs {
		if slug == "" || seen[slug] || (skip != nil && skip[i]) {
			continue
		}
		seen[slug] = true
		out = append(out, slug)
	}
	slices.Sort(out)
	return out
}

// FormatNode is one node of the derived format tree (see the file comment):
// whether it is a format node, and its ancestors, transitively closed and
// sorted.
type FormatNode struct {
	Format    bool     `json:"format,omitempty"`
	Ancestors []string `json:"ancestors"`
}

// formatNodeOf is the format-tree node a claim names, or "" when it names none:
// its browse-node id, else (a ladder-stating claim) the node its path names in
// the claim's marketplace, falling back to the US paths as by_path does.
func (t genreTable) formatNodeOf(c genreClaim) string {
	if node := strings.TrimSpace(c.node); node != "" {
		if _, ok := t.FormatTree[node]; ok {
			return node
		}
		return ""
	}
	if c.path == "" {
		return ""
	}
	node, ok := t.FormatPaths[c.region][c.path]
	if !ok && c.region != "us" {
		node = t.FormatPaths["us"][c.path]
	}
	return node
}

// formatDerived reports, per claim, whether it is FORMAT-DERIVED: a format node,
// or an ancestor of a format node the row states, that the row reaches through
// no non-format descendant. nil (the common case: no claim is a format node, or
// the rule is off) means none is.
func (t genreTable) formatDerived(claims []genreClaim) []bool {
	if !slices.ContainsFunc(claims, func(c genreClaim) bool { return t.FormatTree[t.formatNodeOf(c)].Format }) {
		return nil
	}
	nodes := make([]string, len(claims))
	formatish := map[string]bool{}
	for i, c := range claims {
		n := t.formatNodeOf(c)
		nodes[i] = n
		if fn := t.FormatTree[n]; fn.Format {
			formatish[n] = true
			for _, a := range fn.Ancestors {
				formatish[a] = true
			}
		}
	}
	// justified is every node a stated NON-format-ish tree node descends from:
	// the subject evidence that keeps an ancestor's genre.
	justified := map[string]bool{}
	for _, n := range nodes {
		if n == "" || formatish[n] {
			continue
		}
		for _, a := range t.FormatTree[n].Ancestors {
			justified[a] = true
		}
	}
	out := make([]bool, len(claims))
	for i, n := range nodes {
		out[i] = n != "" && formatish[n] && !justified[n]
	}
	return out
}

// DeriveFormatTree derives the table's format_tree and format_paths from the
// hand-curated format list and a taxonomy given as marketplace -> path key
// (GenrePathKey form) -> node id: for each marketplace, every path of every root
// subtree holding a format node, and for every node of those paths whether it is
// a format node and its transitively closed ancestors (a node's parent being the
// node at its path's parent, in the same marketplace). It is GENERATION-time
// code - scripts/genrepaths writes its result and TestFormatTreeMatchesGenrePaths
// re-derives it from the verification file - so the runtime rule only looks
// things up. A format node no path names, or a path whose parent path is
// missing, is an error.
func DeriveFormatTree(format []string, paths map[string]map[string]string) (map[string]FormatNode, map[string]map[string]string, error) {
	isFormat := map[string]bool{}
	for _, n := range format {
		isFormat[n] = true
	}
	formatPaths := map[string]map[string]string{}
	parents := map[string]map[string]bool{}
	for region, m := range paths {
		roots := map[string]bool{}
		for key, node := range m {
			if isFormat[node] {
				root, _, _ := strings.Cut(key, ":")
				roots[root] = true
			}
		}
		if len(roots) == 0 {
			continue
		}
		sub := map[string]string{}
		for key, node := range m {
			if root, _, _ := strings.Cut(key, ":"); roots[root] {
				sub[key] = node
			}
		}
		for key, node := range sub {
			if parents[node] == nil {
				parents[node] = map[string]bool{}
			}
			cut := strings.LastIndex(key, ":")
			if cut < 0 {
				continue
			}
			parent, ok := sub[key[:cut]]
			if !ok {
				return nil, nil, fmt.Errorf("%s: %q has no parent path %q", region, key, key[:cut])
			}
			parents[node][parent] = true
		}
		formatPaths[region] = sub
	}
	var visit func(node string, into map[string]bool)
	visit = func(node string, into map[string]bool) {
		for p := range parents[node] {
			if !into[p] {
				into[p] = true
				visit(p, into)
			}
		}
	}
	tree := map[string]FormatNode{}
	for node := range parents {
		anc := map[string]bool{}
		visit(node, anc)
		list := make([]string, 0, len(anc))
		for a := range anc {
			list = append(list, a)
		}
		slices.Sort(list)
		tree[node] = FormatNode{Format: isFormat[node], Ancestors: list}
	}
	for _, n := range format {
		if _, ok := tree[n]; !ok {
			return nil, nil, fmt.Errorf("format node %s is named by no path", n)
		}
	}
	return tree, formatPaths, nil
}
