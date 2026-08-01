package serve

import (
	"database/sql"
	"fmt"
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
	schemaVersion int    // meta(schema_version); characters/recaps arrived in v2, recap_summaries in v3, work_genres in v4

	// The series-gap set is derived from the whole series table, so it is
	// computed lazily and kept: the artifact behind a snapshot never changes.
	gapsMu   sync.Mutex
	gaps     []seriesGap
	gapsDone bool
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
func eachChunk(ids []string, fn func(placeholders string, args []any) error) error {
	for c := range slices.Chunk(ids, idChunkSize) {
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
	// Callers legitimately repeat ids - a narrator credited on two recordings of
	// one work yields that work twice - and a repeat would otherwise ride along
	// in every IN list and count against the chunk size.
	uniq := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		uniq = append(uniq, id)
	}

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
