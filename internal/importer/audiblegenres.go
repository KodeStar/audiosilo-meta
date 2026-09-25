package importer

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"sort"
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
// would have said. It is DERIVED, never hand-authored: every taxonomy path
// (libex's per-marketplace /categories) whose node resolves differently from its
// leaf name gets an entry holding the node's answer, keyed by the lowercased,
// trimmed segments joined with ":" (genrePathKey) - so a path-stating source
// resolves exactly the way a node-stating source does, and a path that needs no
// disambiguation simply falls through to the name. Where one English path is
// spelled identically in several marketplaces whose nodes disagree, the US
// node's answer wins.
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

//go:embed audiblegenres.json
var audibleGenresFS embed.FS

// genreTable maps source genre claims onto the project's genre vocabulary.
type genreTable struct {
	// ByASIN is keyed by browse-node id (the JSON key stays "by_asin", the name
	// the exports use for the field).
	ByASIN map[string]string `json:"by_asin"`
	ByName map[string]string `json:"by_name"`
	// ByPath is keyed by genrePathKey (see the file comment): the category
	// paths whose leaf NAME alone would resolve differently from their node.
	ByPath map[string]string `json:"by_path"`
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
// browse-node id (when it states one), the category path from the taxonomy root
// down to it (when the source states the ladder rather than the node - display
// form, segments joined with ":"), and its display name (the path's leaf). Both
// Audible "Genres" and "Tags" nodes are eligible claims - the mapping table, not
// the node type, decides what becomes a vocabulary genre.
type genreClaim struct {
	node string
	path string
	name string
}

// genrePathKey normalizes a category path for by_path: each ":"-separated
// segment trimmed and lowercased (the name rule), empty segments dropped,
// rejoined with ":". "" when nothing is left.
func genrePathKey(path string) string {
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
// Fantasy:Fantasy:Epic", OpenAudible's genre field) into one claim per level -
// the root, the root's child, ... the leaf - each carrying its path from the
// root and its own name. Every level is a claim because a node-stating source
// (libex) states every level too, so the two resolve to the same set. Empty
// segments are dropped; an empty ladder yields nil.
func pathGenreClaims(ladder string) []genreClaim {
	var segs []string
	for _, s := range strings.Split(ladder, ":") {
		if s = strings.TrimSpace(s); s != "" {
			segs = append(segs, s)
		}
	}
	claims := make([]genreClaim, 0, len(segs))
	for i := range segs {
		claims = append(claims, genreClaim{path: strings.Join(segs[:i+1], ":"), name: segs[i]})
	}
	return claims
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
// normalized category path, then the lowercased/trimmed display name. ok is
// false for a claim that does not map - the caller drops it (and reports the
// unmapped string once per run).
func (t genreTable) lookup(c genreClaim) (string, bool) {
	if node := strings.TrimSpace(c.node); node != "" {
		if g, ok := t.ByASIN[node]; ok {
			return g, true
		}
	}
	if c.path != "" {
		if g, ok := t.ByPath[genrePathKey(c.path)]; ok {
			return g, true
		}
	}
	return t.lookupName(c.name)
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
// vocabulary slugs (sorted because checkGenresSorted pins the order). Every
// claim that does not map is recorded in unmapped (keyed by its path when it has
// one - "Contemporary" alone says nothing about where it sits - else its display
// name, else its node id) so the run can report each distinct unmapped string
// exactly once. It is called only where the result is stored (a work this
// run creates), so a row whose genres would never persist adds no noise to that
// report.
func (t genreTable) mapGenres(claims []genreClaim, unmapped map[string]bool) []string {
	if len(claims) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, c := range claims {
		slug, ok := t.lookup(c)
		if !ok {
			if label := strings.TrimSpace(firstNonEmpty(c.path, c.name, c.node)); label != "" {
				unmapped[label] = true
			}
			continue
		}
		if seen[slug] {
			continue
		}
		seen[slug] = true
		out = append(out, slug)
	}
	sort.Strings(out)
	return out
}
