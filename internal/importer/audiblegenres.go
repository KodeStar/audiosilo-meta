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
// would have said. It is keyed by MARKETPLACE, then by the lowercased, trimmed
// segments joined with ":" (genrePathKey), and the path lookup is
//
//	by_path[region][path]  else  by_path["us"][path]  else  by_name[leaf]
//
// with region the row's own marketplace. It is DERIVED, never hand-authored:
// scripts/genrepaths walks every marketplace's taxonomy (libex's /categories)
// and writes an entry exactly where that chain would otherwise answer
// differently from the path's NODE in that marketplace - holding the node's
// answer, or "" where the node maps to nothing (a suppression, so an English
// path the US pins cannot override a marketplace whose own node is unmapped).
// So a path-stating source lands exactly where a node-stating one does for the
// same marketplace, and a marketplace whose taxonomy lacks the path gets the US
// answer. testdata/genrepaths.json is the generator's verification file, which
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

//go:embed audiblegenres.json
var audibleGenresFS embed.FS

// genreTable maps source genre claims onto the project's genre vocabulary.
type genreTable struct {
	// ByASIN is keyed by browse-node id (the JSON key stays "by_asin", the name
	// the exports use for the field).
	ByASIN map[string]string `json:"by_asin"`
	ByName map[string]string `json:"by_name"`
	// ByPath is keyed by marketplace, then by genrePathKey (see the file
	// comment): the category paths whose lookup chain would otherwise resolve
	// differently from their node. A "" value is a suppression.
	ByPath map[string]map[string]string `json:"by_path"`
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
// path (already in genrePathKey form, so a lookup never re-normalizes it),
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
// Fantasy:Fantasy:Epic", OpenAudible's genre field) stated in marketplace region
// into one claim per level - the root, the root's child, ... the leaf - each
// carrying its normalized path from the root and its own name. Every level is a
// claim because a node-stating source (libex) states every level too, so the
// two resolve to the same set. Empty segments are dropped; an empty ladder
// yields nil.
func pathGenreClaims(ladder, region string) []genreClaim {
	var segs, keys []string
	for _, s := range strings.Split(ladder, ":") {
		if s = strings.TrimSpace(s); s != "" {
			segs = append(segs, s)
			keys = append(keys, strings.ToLower(s))
		}
	}
	if len(segs) == 0 {
		return nil
	}
	display := strings.Join(segs, ":")
	claims := make([]genreClaim, 0, len(segs))
	for i := range segs {
		claims = append(claims, genreClaim{
			name: segs[i], path: strings.Join(keys[:i+1], ":"), region: region, ladder: display,
		})
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
// against), key in genrePathKey form. found reports that the table DECIDED the
// path - including a "" suppression, which must stop the lookup rather than fall
// through to the leaf name.
func (t genreTable) lookupPath(region, key string) (g string, found bool) {
	if g, found = t.ByPath[region][key]; found {
		return g, true
	}
	if region != "us" {
		g, found = t.ByPath["us"][key]
	}
	return g, found
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
func (t genreTable) mapGenres(claims []genreClaim, unmapped map[string]bool) []string {
	if len(claims) == 0 {
		return nil
	}
	seen := map[string]bool{}
	ladderMapped := map[string]bool{}
	var out []string
	for _, c := range claims {
		if c.ladder != "" {
			if _, ok := ladderMapped[c.ladder]; !ok {
				ladderMapped[c.ladder] = false
			}
		}
		slug, ok := t.lookup(c)
		if !ok {
			if c.ladder == "" {
				if label := strings.TrimSpace(firstNonEmpty(c.name, c.node)); label != "" {
					unmapped[label] = true
				}
			}
			continue
		}
		if c.ladder != "" {
			ladderMapped[c.ladder] = true
		}
		if seen[slug] {
			continue
		}
		seen[slug] = true
		out = append(out, slug)
	}
	for ladder, mapped := range ladderMapped {
		if !mapped {
			unmapped[ladder] = true
		}
	}
	sort.Strings(out)
	return out
}
