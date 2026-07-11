// Package build compiles a validated Catalog into a read-only SQLite artifact
// (schema_version 1) with an FTS5 search index and covering indexes for ASIN
// and ISBN lookup. The build is deterministic: entities are inserted in id
// order so an unchanged dataset yields byte-stable table contents.
package build

import (
	"database/sql"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kodestar/audiosilo-meta/internal/model"
	_ "modernc.org/sqlite"
)

// SchemaVersion is written to meta(schema_version).
const SchemaVersion = 1

const ddl = `
CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);

CREATE TABLE works (
  id             TEXT PRIMARY KEY,
  title          TEXT NOT NULL,
  subtitle       TEXT,
  language       TEXT NOT NULL,
  first_published TEXT,
  description    TEXT,
  wikidata       TEXT,
  openlibrary    TEXT,
  goodreads      TEXT,
  license        TEXT NOT NULL
);
CREATE TABLE work_authors (work_id TEXT NOT NULL, person_id TEXT NOT NULL, ord INTEGER NOT NULL);
CREATE INDEX idx_work_authors_person ON work_authors(person_id);
CREATE TABLE work_isbns (work_id TEXT NOT NULL, isbn TEXT NOT NULL);
CREATE INDEX idx_work_isbns_isbn ON work_isbns(isbn);

CREATE TABLE recordings (
  work_id      TEXT NOT NULL,
  id           TEXT NOT NULL,
  abridged     INTEGER NOT NULL,
  language     TEXT NOT NULL,
  runtime_min  INTEGER,
  release_date TEXT,
  publisher    TEXT,
  cover_url    TEXT,
  license      TEXT NOT NULL,
  PRIMARY KEY (work_id, id)
);
CREATE TABLE recording_narrators (work_id TEXT NOT NULL, recording_id TEXT NOT NULL, person_id TEXT NOT NULL, ord INTEGER NOT NULL);
CREATE INDEX idx_recording_narrators_person ON recording_narrators(person_id);

CREATE TABLE recording_asins (
  region       TEXT NOT NULL,
  asin         TEXT NOT NULL,
  work_id      TEXT NOT NULL,
  recording_id TEXT NOT NULL,
  UNIQUE (region, asin)
);
CREATE INDEX idx_recording_asins_asin ON recording_asins(asin);

CREATE TABLE recording_isbns (work_id TEXT NOT NULL, recording_id TEXT NOT NULL, isbn TEXT NOT NULL);
CREATE INDEX idx_recording_isbns_isbn ON recording_isbns(isbn);

CREATE TABLE chapters (
  work_id      TEXT NOT NULL,
  recording_id TEXT NOT NULL,
  idx          INTEGER NOT NULL,
  title        TEXT NOT NULL,
  start_ms     INTEGER NOT NULL,
  length_ms    INTEGER NOT NULL,
  PRIMARY KEY (work_id, recording_id, idx)
);

CREATE TABLE people (
  id          TEXT PRIMARY KEY,
  name        TEXT NOT NULL,
  sort_name   TEXT,
  description TEXT,
  wikidata    TEXT,
  openlibrary TEXT,
  audible     TEXT,
  license     TEXT NOT NULL
);

CREATE TABLE series (
  id       TEXT PRIMARY KEY,
  name     TEXT NOT NULL,
  wikidata TEXT,
  goodreads TEXT,
  license  TEXT NOT NULL
);
CREATE TABLE series_works (series_id TEXT NOT NULL, work_id TEXT NOT NULL, position TEXT NOT NULL);
CREATE INDEX idx_series_works_work ON series_works(work_id);
CREATE TABLE series_authors (series_id TEXT NOT NULL, person_id TEXT NOT NULL, ord INTEGER NOT NULL);

CREATE VIRTUAL TABLE search_fts USING fts5(kind UNINDEXED, id UNINDEXED, title, names);
`

// Build writes the SQLite artifact for cat to outPath. builtAt is recorded in
// meta(built_at); pass the zero time to use the current UTC time.
func Build(cat *model.Catalog, outPath string, builtAt time.Time) (err error) {
	if builtAt.IsZero() {
		builtAt = time.Now().UTC()
	}
	if err := os.Remove(outPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove existing %s: %w", outPath, err)
	}

	db, err := sql.Open("sqlite", outPath)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := db.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()

	if _, err := db.Exec(ddl); err != nil {
		return fmt.Errorf("create schema: %w", err)
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	people := append([]*model.Person(nil), cat.People...)
	sort.Slice(people, func(i, j int) bool { return people[i].ID < people[j].ID })
	nameByID := map[string]string{}
	for _, p := range people {
		nameByID[p.ID] = p.Name
	}

	works := append([]*model.Work(nil), cat.Works...)
	sort.Slice(works, func(i, j int) bool { return works[i].ID < works[j].ID })

	series := append([]*model.Series(nil), cat.Series...)
	sort.Slice(series, func(i, j int) bool { return series[i].ID < series[j].ID })

	// Map work id -> series names, for the work FTS row.
	seriesNamesByWork := map[string][]string{}
	for _, s := range series {
		for _, sw := range s.Works {
			seriesNamesByWork[sw.Work] = append(seriesNamesByWork[sw.Work], s.Name)
		}
	}

	if err = insertPeople(tx, people); err != nil {
		return err
	}
	if err = insertWorks(tx, works, nameByID, seriesNamesByWork); err != nil {
		return err
	}
	if err = insertSeries(tx, series); err != nil {
		return err
	}

	nRec := 0
	for _, w := range works {
		nRec += len(w.Recordings)
	}

	metaRows := [][2]string{
		{"schema_version", strconv.Itoa(SchemaVersion)},
		{"built_at", builtAt.UTC().Format(time.RFC3339)},
		{"count_works", strconv.Itoa(len(works))},
		{"count_recordings", strconv.Itoa(nRec)},
		{"count_people", strconv.Itoa(len(people))},
		{"count_series", strconv.Itoa(len(series))},
	}
	for _, kv := range metaRows {
		if _, err = tx.Exec(`INSERT INTO meta(key, value) VALUES(?, ?)`, kv[0], kv[1]); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func insertPeople(tx *sql.Tx, people []*model.Person) error {
	for _, p := range people {
		var wiki, ol, aud string
		if p.Xref != nil {
			wiki, ol, aud = p.Xref.Wikidata, p.Xref.Openlibrary, p.Xref.Audible
		}
		if _, err := tx.Exec(
			`INSERT INTO people(id, name, sort_name, description, wikidata, openlibrary, audible, license) VALUES(?,?,?,?,?,?,?,?)`,
			p.ID, p.Name, nullStr(p.SortName), nullStr(p.Description), nullStr(wiki), nullStr(ol), nullStr(aud), p.License,
		); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO search_fts(kind, id, title, names) VALUES('person', ?, ?, '')`, p.ID, p.Name); err != nil {
			return err
		}
	}
	return nil
}

func insertWorks(tx *sql.Tx, works []*model.Work, nameByID map[string]string, seriesNamesByWork map[string][]string) error {
	for _, w := range works {
		var wiki, ol, gr string
		var isbns []string
		if w.Xref != nil {
			wiki, ol, gr, isbns = w.Xref.Wikidata, w.Xref.Openlibrary, w.Xref.Goodreads, w.Xref.ISBN
		}
		if _, err := tx.Exec(
			`INSERT INTO works(id, title, subtitle, language, first_published, description, wikidata, openlibrary, goodreads, license) VALUES(?,?,?,?,?,?,?,?,?,?)`,
			w.ID, w.Title, nullStr(w.Subtitle), w.Language, nullStr(w.FirstPublished), nullStr(w.Description), nullStr(wiki), nullStr(ol), nullStr(gr), w.License,
		); err != nil {
			return err
		}
		for i, a := range w.Authors {
			if _, err := tx.Exec(`INSERT INTO work_authors(work_id, person_id, ord) VALUES(?,?,?)`, w.ID, a, i); err != nil {
				return err
			}
		}
		for _, isbn := range isbns {
			if _, err := tx.Exec(`INSERT INTO work_isbns(work_id, isbn) VALUES(?,?)`, w.ID, isbn); err != nil {
				return err
			}
		}

		var names []string
		for _, a := range w.Authors {
			names = appendName(names, nameByID[a])
		}

		recs := append([]*model.Recording(nil), w.Recordings...)
		sort.Slice(recs, func(i, j int) bool { return recs[i].ID < recs[j].ID })
		for _, r := range recs {
			if err := insertRecording(tx, w.ID, r); err != nil {
				return err
			}
			for _, n := range r.Narrators {
				names = appendName(names, nameByID[n])
			}
		}
		for _, sn := range seriesNamesByWork[w.ID] {
			names = appendName(names, sn)
		}

		ftsTitle := strings.TrimSpace(w.Title + " " + w.Subtitle)
		if _, err := tx.Exec(`INSERT INTO search_fts(kind, id, title, names) VALUES('work', ?, ?, ?)`, w.ID, ftsTitle, strings.Join(names, " ")); err != nil {
			return err
		}
	}
	return nil
}

func insertRecording(tx *sql.Tx, workID string, r *model.Recording) error {
	if _, err := tx.Exec(
		`INSERT INTO recordings(work_id, id, abridged, language, runtime_min, release_date, publisher, cover_url, license) VALUES(?,?,?,?,?,?,?,?,?)`,
		workID, r.ID, boolInt(r.Abridged), r.Language, nullInt(r.RuntimeMin), nullStr(r.ReleaseDate), nullStr(r.Publisher), nullStr(r.CoverURL), r.License,
	); err != nil {
		return err
	}
	for i, n := range r.Narrators {
		if _, err := tx.Exec(`INSERT INTO recording_narrators(work_id, recording_id, person_id, ord) VALUES(?,?,?,?)`, workID, r.ID, n, i); err != nil {
			return err
		}
	}
	for _, a := range r.ASIN {
		if _, err := tx.Exec(`INSERT INTO recording_asins(region, asin, work_id, recording_id) VALUES(?,?,?,?)`, a.Region, a.ASIN, workID, r.ID); err != nil {
			return err
		}
	}
	for _, isbn := range r.ISBN {
		if _, err := tx.Exec(`INSERT INTO recording_isbns(work_id, recording_id, isbn) VALUES(?,?,?)`, workID, r.ID, isbn); err != nil {
			return err
		}
	}
	for i, ch := range r.Chapters {
		if _, err := tx.Exec(`INSERT INTO chapters(work_id, recording_id, idx, title, start_ms, length_ms) VALUES(?,?,?,?,?,?)`, workID, r.ID, i, ch.Title, ch.StartMS, ch.LengthMS); err != nil {
			return err
		}
	}
	return nil
}

func insertSeries(tx *sql.Tx, series []*model.Series) error {
	for _, s := range series {
		var wiki, gr string
		if s.Xref != nil {
			wiki, gr = s.Xref.Wikidata, s.Xref.Goodreads
		}
		if _, err := tx.Exec(`INSERT INTO series(id, name, wikidata, goodreads, license) VALUES(?,?,?,?,?)`, s.ID, s.Name, nullStr(wiki), nullStr(gr), s.License); err != nil {
			return err
		}
		for _, sw := range s.Works {
			if _, err := tx.Exec(`INSERT INTO series_works(series_id, work_id, position) VALUES(?,?,?)`, s.ID, sw.Work, sw.Position); err != nil {
				return err
			}
		}
		for i, a := range s.Authors {
			if _, err := tx.Exec(`INSERT INTO series_authors(series_id, person_id, ord) VALUES(?,?,?)`, s.ID, a, i); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(`INSERT INTO search_fts(kind, id, title, names) VALUES('series', ?, ?, '')`, s.ID, s.Name); err != nil {
			return err
		}
	}
	return nil
}

func appendName(names []string, n string) []string {
	if n == "" {
		return names
	}
	return append(names, n)
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullInt(n int) any {
	if n == 0 {
		return nil
	}
	return n
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
