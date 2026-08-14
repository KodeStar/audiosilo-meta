package serve

import (
	"database/sql"
	"fmt"
	"log"
	"slices"
	"strconv"
	"strings"
	"sync"

	_ "modernc.org/sqlite"
)

// snapshot is one loaded, read-only view of the SQLite artifact. It is immutable
// once built; the server swaps the whole pointer to adopt a newer artifact, so
// in-flight requests keep reading the handle they started with.
type snapshot struct {
	db            *sql.DB
	tag           string // release tag this artifact came from ("" for a local --db)
	path          string // on-disk path of the artifact
	stats         Stats  // precomputed once, at load
	schemaVersion int    // meta(schema_version); characters/recaps arrived in v2, recap_summaries in v3, work_genres in v4, redirects in v5

	// hasRedirects is whether the artifact's tombstone table holds anything at
	// all, asked once at load. Until the first duplicate-merge lands it is empty
	// in every published release, and the redirect probe sits on paths that are
	// not failures - the chapters route consults it whenever a recording's list
	// comes back empty - so without this an ordinary 200 would pay a SQL round
	// trip to learn there is nothing to find.
	hasRedirects bool

	// log is the Server's injected logger, for the query layer's degradation
	// notices (a request that serves a lesser answer rather than failing). It is
	// set when the Server adopts the snapshot; a snapshot built directly - a test,
	// or any future caller - logs to the standard logger instead, never panics.
	log *log.Logger

	// The series-gap set is derived from the whole series table, so it is
	// computed lazily and kept: the artifact behind a snapshot never changes.
	gapsMu   sync.Mutex
	gaps     []seriesGap
	gapsDone bool
}

// logf writes a degradation notice through the Server's logger when the
// snapshot has one.
func (s *snapshot) logf(format string, args ...any) {
	if s.log != nil {
		s.log.Printf(format, args...)
		return
	}
	log.Printf(format, args...)
}

// Stats is the /api/v1/stats payload; it is cached per snapshot.
type Stats struct {
	Works           int    `json:"works"`
	Recordings      int    `json:"recordings"`
	People          int    `json:"people"`
	Series          int    `json:"series"`
	TotalRuntimeMin int    `json:"total_runtime_min"`
	TotalChapters   int    `json:"total_chapters"`
	BuiltAt         string `json:"built_at"`
}

// openSnapshot opens the artifact at path read-only and precomputes its stats.
func openSnapshot(path, tag string) (*snapshot, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	s := &snapshot{db: db, tag: tag, path: path}
	if err := s.loadStats(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *snapshot) close() {
	if s != nil && s.db != nil {
		_ = s.db.Close()
	}
}

func (s *snapshot) loadStats() error {
	st := Stats{}
	q := func(dst *int, query string) error {
		return s.db.QueryRow(query).Scan(dst)
	}
	if err := q(&st.Works, `SELECT COUNT(*) FROM works`); err != nil {
		return err
	}
	if err := q(&st.Recordings, `SELECT COUNT(*) FROM recordings`); err != nil {
		return err
	}
	if err := q(&st.People, `SELECT COUNT(*) FROM people`); err != nil {
		return err
	}
	if err := q(&st.Series, `SELECT COUNT(*) FROM series`); err != nil {
		return err
	}
	if err := q(&st.TotalRuntimeMin, `SELECT COALESCE(SUM(runtime_min), 0) FROM recordings`); err != nil {
		return err
	}
	if err := q(&st.TotalChapters, `SELECT COUNT(*) FROM chapters`); err != nil {
		return err
	}
	if err := s.db.QueryRow(`SELECT value FROM meta WHERE key='built_at'`).Scan(&st.BuiltAt); err != nil && err != sql.ErrNoRows {
		return err
	}
	// schema_version tells us which optional tables exist. The characters/recaps
	// tables arrived in v2; a newer binary may briefly serve an older artifact,
	// so the query methods gate on this rather than probing for the tables.
	var sv string
	if err := s.db.QueryRow(`SELECT value FROM meta WHERE key='schema_version'`).Scan(&sv); err != nil && err != sql.ErrNoRows {
		return err
	}
	s.schemaVersion, _ = strconv.Atoi(sv)
	// Asked here, behind the version gate, so a newer binary serving an older
	// release never touches the table at all. An artifact that CLAIMS version 5
	// and has no such table fails the load, exactly as it does for the tables
	// counted above: the builder writes the version and the table together, so
	// that combination is a corrupt artifact, not an old one - and the error says
	// which claim was being enforced, because "no such table: redirects" on its own
	// reads as a bug in the server rather than as a broken file.
	if s.schemaVersion >= redirectSchemaVersion {
		if err := s.db.QueryRow(anyRedirectSQL).Scan(&s.hasRedirects); err != nil {
			return fmt.Errorf("%s: artifact schema_version %d requires the redirects table: %w",
				s.path, s.schemaVersion, err)
		}
	}
	s.stats = st
	return nil
}

// personRef is the {id,name} shape used everywhere a person is referenced.
type personRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// seriesRef is a work's membership summary: {id,name,position}.
type seriesRef struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Position string `json:"position"`
}

// workCard is the compact work representation reused by lists and lookups.
type workCard struct {
	ID       string      `json:"id"`
	Title    string      `json:"title"`
	Authors  []personRef `json:"authors"`
	Series   *seriesRef  `json:"series"`
	CoverURL *string     `json:"cover_url"`
	AddedAt  *string     `json:"added_at"`
}

// workCard builds the card for a single work id, through the same batched
// implementation the lists use. It returns (nil, nil) when the work does not
// exist. Keeping ONE implementation is what stops the card's composition rules
// (which series membership wins, which cover wins) from being spelled twice and
// drifting apart.
func (s *snapshot) workCard(id string) (*workCard, error) {
	byID, err := s.cardsByID([]string{id})
	if err != nil {
		return nil, err
	}
	return byID[id], nil
}

// idChunkSize bounds one IN (...) list. SQLite's default bound-parameter limit
// is far higher, but a person's credit list is unbounded (a corporate credit
// like "Full Cast" already narrates ~2,000 works), so the batch queries chunk
// rather than assume the list is small.
const idChunkSize = 400

// eachChunk runs fn over each id chunk with the rendered placeholder list and
// bound args. It is the single place the chunking rule lives, so no batch query
// can forget it. An empty input runs fn zero times, so no caller can issue an
// empty IN ().
//
// The ids are DEDUPED first, in first-seen order. Callers legitimately repeat
// one - a narrator credited on two recordings of a work yields that work twice -
// and a repeat is not merely wasted: every helper here APPENDS its rows into a
// map keyed by id, so two chunks that both name a work would append its
// narrators twice. Doing it here rather than per helper is what makes that true
// of all of them at once.
func eachChunk(ids []string, fn func(placeholders string, args []any) error) error {
	for c := range slices.Chunk(dedupeIDs(ids), idChunkSize) {
		args := make([]any, len(c))
		for i, id := range c {
			args[i] = id
		}
		ph := strings.TrimSuffix(strings.Repeat("?,", len(c)), ",")
		if err := fn(ph, args); err != nil {
			return err
		}
	}
	return nil
}

// dedupeIDs returns ids without repeats, in first-seen order. Order matters:
// cardsByID walks the deduped list to decide chunk boundaries, so a sort would
// make the queries depend on the ids' spelling rather than on the caller's page.
func dedupeIDs(ids []string) []string {
	out := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// The batch queries' SQL, rendered around an IN placeholder list. They are
// functions rather than inline strings so the index guard in the tests can
// EXPLAIN exactly what runs instead of a hand-copied lookalike that silently
// drifts.
func worksByIDSQL(ph string) string {
	return `SELECT id, title, added_at FROM works WHERE id IN (` + ph + `)`
}

func authorsByWorkSQL(ph string) string {
	return `SELECT wa.work_id, p.id, p.name FROM work_authors wa JOIN people p ON p.id = wa.person_id ` +
		`WHERE wa.work_id IN (` + ph + `) ORDER BY wa.work_id, wa.ord`
}

func firstSeriesByWorkSQL(ph string) string {
	return `SELECT sw.work_id, s.id, s.name, sw.position FROM series_works sw JOIN series s ON s.id = sw.series_id ` +
		`WHERE sw.work_id IN (` + ph + `) ORDER BY sw.work_id, s.id`
}

func coversByWorkSQL(ph string) string {
	return `SELECT work_id, cover_url FROM recordings WHERE work_id IN (` + ph + `) ` +
		`AND cover_url IS NOT NULL AND cover_url <> '' ORDER BY work_id, id`
}

// narratorsByWorkSQL reads the distinct narrators across each work's recordings.
// It rides the (work_id, recording_id) index by prefix. The GROUP BY carries the
// work id so ONE query serves a whole page of hits, and MIN(ord) keeps each
// work's narrators in credit order.
//
// The person id breaks ties in that order. Two narrators of a dual-narrator
// recording legitimately share a MIN(ord) across a work's recordings (each was
// first on one of them), and MIN(ord) alone would leave which one is listed
// first to the query planner - so the same artifact could answer the same
// request in two orders.
func narratorsByWorkSQL(ph string) string {
	return `SELECT rn.work_id, p.id, p.name FROM recording_narrators rn JOIN people p ON p.id = rn.person_id ` +
		`WHERE rn.work_id IN (` + ph + `) GROUP BY rn.work_id, p.id, p.name ORDER BY rn.work_id, MIN(rn.ord), p.id`
}

func namesByPersonIDSQL(ph string) string {
	return `SELECT id, name FROM people WHERE id IN (` + ph + `)`
}

// seriesSummariesByIDSQL reads each series' name AND its member count in one
// query. The count is the expensive half of a series search hit, and a scoped
// page is 100% series, so it is the join rather than a COUNT per row. The LEFT
// JOIN keeps a series with no member works on the page, with a count of zero,
// instead of dropping it.
func seriesSummariesByIDSQL(ph string) string {
	return `SELECT s.id, s.name, COUNT(sw.work_id) FROM series s LEFT JOIN series_works sw ON sw.series_id = s.id ` +
		`WHERE s.id IN (` + ph + `) GROUP BY s.id, s.name`
}

// cardsByID builds the work cards for a whole id set in a FIXED number of
// queries per chunk (works + authors + series + covers), instead of the four
// queries per work a workCard loop costs. That is what keeps a prolific
// narrator's page from issuing thousands of sequential round-trips. Ids absent
// from the catalogue are simply missing from the map.
func (s *snapshot) cardsByID(ids []string) (map[string]*workCard, error) {
	out := make(map[string]*workCard, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	// eachChunk dedupes too; this call is what gives `found` below a
	// repeat-free list to walk in a deterministic order.
	uniq := dedupeIDs(ids)

	err := eachChunk(uniq, func(ph string, args []any) error {
		rows, err := s.db.Query(worksByIDSQL(ph), args...)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var id, title string
			var addedAt sql.NullString
			if err := rows.Scan(&id, &title, &addedAt); err != nil {
				return err
			}
			wc := &workCard{ID: id, Title: title, Authors: []personRef{}}
			if addedAt.Valid {
				wc.AddedAt = &addedAt.String
			}
			out[id] = wc
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}

	// Only ids that actually exist need the follow-up queries. Walking uniq (not
	// the map) keeps the chunk boundaries deterministic without a sort.
	found := make([]string, 0, len(out))
	for _, id := range uniq {
		if _, ok := out[id]; ok {
			found = append(found, id)
		}
	}

	authors, err := s.authorsByWork(found)
	if err != nil {
		return nil, err
	}
	series, err := s.firstSeriesByWork(found)
	if err != nil {
		return nil, err
	}
	covers, err := s.coversByWork(found)
	if err != nil {
		return nil, err
	}
	for id, wc := range out {
		if a := authors[id]; a != nil {
			wc.Authors = a
		}
		wc.Series = series[id]
		wc.CoverURL = covers[id]
	}
	return out, nil
}

// authorsByWork returns each work's authors in credit order, keyed by work id.
func (s *snapshot) authorsByWork(ids []string) (map[string][]personRef, error) {
	out := map[string][]personRef{}
	err := eachChunk(ids, func(ph string, args []any) error {
		rows, err := s.db.Query(authorsByWorkSQL(ph), args...)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var workID string
			var p personRef
			if err := rows.Scan(&workID, &p.ID, &p.Name); err != nil {
				return err
			}
			out[workID] = append(out[workID], p)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// firstSeriesByWork returns each work's FIRST series membership in series id
// order, keyed by work id. Works with no series are absent from the map. This is
// the only definition of "the card's series" - workCard goes through it too.
func (s *snapshot) firstSeriesByWork(ids []string) (map[string]*seriesRef, error) {
	out := map[string]*seriesRef{}
	err := eachChunk(ids, func(ph string, args []any) error {
		rows, err := s.db.Query(firstSeriesByWorkSQL(ph), args...)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var workID string
			var sr seriesRef
			if err := rows.Scan(&workID, &sr.ID, &sr.Name, &sr.Position); err != nil {
				return err
			}
			if _, seen := out[workID]; !seen { // ORDER BY makes the first row the winner
				out[workID] = &sr
			}
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// coversByWork returns each work's first non-empty recording cover URL in
// recording id order, keyed by work id. As with firstSeriesByWork, this is the
// only place the "which cover wins" rule is written down.
func (s *snapshot) coversByWork(ids []string) (map[string]*string, error) {
	out := map[string]*string{}
	err := eachChunk(ids, func(ph string, args []any) error {
		rows, err := s.db.Query(coversByWorkSQL(ph), args...)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var workID, cover string
			if err := rows.Scan(&workID, &cover); err != nil {
				return err
			}
			if _, seen := out[workID]; !seen {
				c := cover
				out[workID] = &c
			}
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// narratorsByWork returns each work's distinct narrators in credit order, keyed
// by work id. A work with no narrators is absent from the map.
func (s *snapshot) narratorsByWork(ids []string) (map[string][]personRef, error) {
	out := map[string][]personRef{}
	err := eachChunk(ids, func(ph string, args []any) error {
		rows, err := s.db.Query(narratorsByWorkSQL(ph), args...)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var workID string
			var p personRef
			if err := rows.Scan(&workID, &p.ID, &p.Name); err != nil {
				return err
			}
			out[workID] = append(out[workID], p)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// namesByPersonID returns the display name of each person id. Ids absent from
// the catalogue are simply missing from the map.
func (s *snapshot) namesByPersonID(ids []string) (map[string]string, error) {
	out := map[string]string{}
	err := eachChunk(ids, func(ph string, args []any) error {
		rows, err := s.db.Query(namesByPersonIDSQL(ph), args...)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var id, name string
			if err := rows.Scan(&id, &name); err != nil {
				return err
			}
			out[id] = name
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// seriesSummary is a series' name plus how many works it holds - what a series
// search hit carries.
type seriesSummary struct {
	name  string
	works int
}

// seriesSummariesByID returns the name and member count of each series id, in
// one query per chunk. Ids absent from the catalogue are missing from the map.
func (s *snapshot) seriesSummariesByID(ids []string) (map[string]seriesSummary, error) {
	out := map[string]seriesSummary{}
	err := eachChunk(ids, func(ph string, args []any) error {
		rows, err := s.db.Query(seriesSummariesByIDSQL(ph), args...)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var id string
			var sum seriesSummary
			if err := rows.Scan(&id, &sum.name, &sum.works); err != nil {
				return err
			}
			out[id] = sum
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// authorsOfSQL is the single-work author query (workDetail and the ABS path read
// a work's authors on their own, without a card). Shared with the index guard.
const authorsOfSQL = `SELECT p.id, p.name FROM work_authors wa JOIN people p ON p.id = wa.person_id WHERE wa.work_id=? ORDER BY wa.ord`

func (s *snapshot) authorsOf(workID string) ([]personRef, error) {
	rows, err := s.db.Query(authorsOfSQL, workID)
	if err != nil {
		return nil, err
	}
	return scanPersonRefs(rows)
}

func scanPersonRefs(rows *sql.Rows) ([]personRef, error) {
	defer func() { _ = rows.Close() }()
	out := []personRef{}
	for rows.Next() {
		var p personRef
		if err := rows.Scan(&p.ID, &p.Name); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
