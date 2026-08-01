package serve

import (
	"database/sql"
	"fmt"
	"sort"
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

// workCard builds the card for a work id. It returns (nil, nil) when the work
// does not exist.
func (s *snapshot) workCard(id string) (*workCard, error) {
	var title string
	var addedAt sql.NullString
	err := s.db.QueryRow(`SELECT title, added_at FROM works WHERE id=?`, id).Scan(&title, &addedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	wc := &workCard{ID: id, Title: title, Authors: []personRef{}}
	if addedAt.Valid {
		wc.AddedAt = &addedAt.String
	}
	if wc.Authors, err = s.authorsOf(id); err != nil {
		return nil, err
	}
	if wc.Series, err = s.firstSeriesOf(id); err != nil {
		return nil, err
	}
	if wc.CoverURL, err = s.coverOf(id); err != nil {
		return nil, err
	}
	return wc, nil
}

// idChunkSize bounds one IN (...) list. SQLite's default bound-parameter limit
// is far higher, but a person's credit list is unbounded (a corporate credit
// like "Full Cast" already narrates ~2,000 works), so the batch queries chunk
// rather than assume the list is small.
const idChunkSize = 400

// chunkIDs splits ids into slices of at most n (the last one shorter). An empty
// input yields no chunks, so a caller's loop body never runs on an empty IN ().
func chunkIDs(ids []string, n int) [][]string {
	var out [][]string
	for len(ids) > n {
		out = append(out, ids[:n])
		ids = ids[n:]
	}
	if len(ids) > 0 {
		out = append(out, ids)
	}
	return out
}

// idArgs renders "?,?,..." for n bound parameters plus the args slice for them.
func idArgs(ids []string) (string, []any) {
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return strings.TrimSuffix(strings.Repeat("?,", len(ids)), ","), args
}

// eachChunk runs fn over each id chunk with the rendered placeholder list and
// bound args. It is the single place the chunking rule lives, so no batch query
// can forget it.
func eachChunk(ids []string, fn func(placeholders string, args []any) error) error {
	for _, c := range chunkIDs(ids, idChunkSize) {
		ph, args := idArgs(c)
		if err := fn(ph, args); err != nil {
			return err
		}
	}
	return nil
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
	err := eachChunk(ids, func(ph string, args []any) error {
		rows, err := s.db.Query(`SELECT id, title, added_at FROM works WHERE id IN (`+ph+`)`, args...)
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

	// Only ids that actually exist need the follow-up queries.
	found := make([]string, 0, len(out))
	for id := range out {
		found = append(found, id)
	}
	sort.Strings(found) // deterministic query shape; the per-work order is the SQL's

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
		rows, err := s.db.Query(
			`SELECT wa.work_id, p.id, p.name FROM work_authors wa JOIN people p ON p.id = wa.person_id `+
				`WHERE wa.work_id IN (`+ph+`) ORDER BY wa.work_id, wa.ord`, args...)
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

// firstSeriesByWork returns each work's FIRST series membership (series id
// order, matching firstSeriesOf), keyed by work id. Works with no series are
// absent from the map.
func (s *snapshot) firstSeriesByWork(ids []string) (map[string]*seriesRef, error) {
	out := map[string]*seriesRef{}
	err := eachChunk(ids, func(ph string, args []any) error {
		rows, err := s.db.Query(
			`SELECT sw.work_id, s.id, s.name, sw.position FROM series_works sw JOIN series s ON s.id = sw.series_id `+
				`WHERE sw.work_id IN (`+ph+`) ORDER BY sw.work_id, s.id`, args...)
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

// coversByWork returns each work's first non-empty recording cover URL
// (recording id order, matching coverOf), keyed by work id.
func (s *snapshot) coversByWork(ids []string) (map[string]*string, error) {
	out := map[string]*string{}
	err := eachChunk(ids, func(ph string, args []any) error {
		rows, err := s.db.Query(
			`SELECT work_id, cover_url FROM recordings WHERE work_id IN (`+ph+`) `+
				`AND cover_url IS NOT NULL AND cover_url <> '' ORDER BY work_id, id`, args...)
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

func (s *snapshot) authorsOf(workID string) ([]personRef, error) {
	rows, err := s.db.Query(
		`SELECT p.id, p.name FROM work_authors wa JOIN people p ON p.id = wa.person_id WHERE wa.work_id=? ORDER BY wa.ord`, workID)
	if err != nil {
		return nil, err
	}
	return scanPersonRefs(rows)
}

func (s *snapshot) firstSeriesOf(workID string) (*seriesRef, error) {
	var sr seriesRef
	err := s.db.QueryRow(
		`SELECT s.id, s.name, sw.position FROM series_works sw JOIN series s ON s.id = sw.series_id WHERE sw.work_id=? ORDER BY s.id LIMIT 1`, workID).
		Scan(&sr.ID, &sr.Name, &sr.Position)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &sr, nil
}

func (s *snapshot) coverOf(workID string) (*string, error) {
	var cover string
	err := s.db.QueryRow(
		`SELECT cover_url FROM recordings WHERE work_id=? AND cover_url IS NOT NULL AND cover_url <> '' ORDER BY id LIMIT 1`, workID).
		Scan(&cover)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &cover, nil
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
