package serve

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// snapshot is one loaded, read-only view of the SQLite artifact. It is immutable
// once built; the server swaps the whole pointer to adopt a newer artifact, so
// in-flight requests keep reading the handle they started with.
type snapshot struct {
	db    *sql.DB
	tag   string // release tag this artifact came from ("" for a local --db)
	path  string // on-disk path of the artifact
	stats Stats  // precomputed once, at load
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
