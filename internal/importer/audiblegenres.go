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
// lowercased/trimmed name. by_asin (the on-disk key name; its keys are
// browse-node ids, not product ASINs) carries the nodes whose NAME is ambiguous
// in the taxonomy - "History" under Fiction is not the History genre - and the
// localized nodes worth pinning; by_name carries the ~1,200 Audible category
// strings observed across the marketplaces libex mirrors.

//go:embed audiblegenres.json
var audibleGenresFS embed.FS

// genreTable maps source genre claims onto the project's genre vocabulary.
type genreTable struct {
	// ByASIN is keyed by browse-node id (the JSON key stays "by_asin", the name
	// the exports use for the field).
	ByASIN map[string]string `json:"by_asin"`
	ByName map[string]string `json:"by_name"`
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
// browse-node id (when it states one) and its display name. Both Audible
// "Genres" and "Tags" nodes are eligible claims - the mapping table, not the
// node type, decides what becomes a vocabulary genre.
type genreClaim struct {
	node string
	name string
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
// lowercased/trimmed display name. ok is false for a claim that does not map -
// the caller drops it (and reports the unmapped string once per run).
func (t genreTable) lookup(c genreClaim) (string, bool) {
	if node := strings.TrimSpace(c.node); node != "" {
		if g, ok := t.ByASIN[node]; ok {
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
// claim that does not map is recorded in unmapped (keyed by its display name, or
// its node id when it has no name) so the run can report each distinct unmapped
// string exactly once. It is called only where the result is stored (a work this
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
			if label := strings.TrimSpace(firstNonEmpty(c.name, c.node)); label != "" {
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
