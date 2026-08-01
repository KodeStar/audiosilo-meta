// Package build compiles a validated Catalog into a read-only SQLite artifact
// (see SchemaVersion) with an FTS5 search index and covering indexes for ASIN
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

	"github.com/kodestar/audiosilo-meta/pkg/model"
	_ "modernc.org/sqlite"
)

// SchemaVersion is written to meta(schema_version). Bumped to 2 when the
// characters/recaps tables were added; bumped to 3 when the recap_summaries
// table (per-work in_short / ending) was added; bumped to 4 when work genres
// arrived in the work_genres table.
const SchemaVersion = 4

const ddl = `
CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);

CREATE TABLE works (
  id             TEXT PRIMARY KEY,
  title          TEXT NOT NULL,
  subtitle       TEXT,
  language       TEXT NOT NULL,
  first_published TEXT,
  description    TEXT,
  added_at       TEXT,
  wikidata       TEXT,
  openlibrary    TEXT,
  goodreads      TEXT,
  license        TEXT NOT NULL
);
CREATE TABLE work_authors (work_id TEXT NOT NULL, person_id TEXT NOT NULL, ord INTEGER NOT NULL);
CREATE INDEX idx_work_authors_person ON work_authors(person_id);
CREATE INDEX idx_work_authors_work ON work_authors(work_id);
CREATE TABLE work_isbns (work_id TEXT NOT NULL, isbn TEXT NOT NULL);
CREATE INDEX idx_work_isbns_isbn ON work_isbns(isbn);
CREATE TABLE work_genres (work_id TEXT NOT NULL, genre TEXT NOT NULL);
CREATE INDEX idx_work_genres_work ON work_genres(work_id);

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

CREATE TABLE characters (
  work_id        TEXT NOT NULL,
  id             TEXT NOT NULL,
  name           TEXT NOT NULL,
  role           TEXT,
  reveal_chapter INTEGER NOT NULL,
  description    TEXT,
  wikidata       TEXT,
  goodreads      TEXT,
  ord            INTEGER NOT NULL,
  license        TEXT NOT NULL,
  PRIMARY KEY (work_id, id)
);
CREATE INDEX idx_characters_work ON characters(work_id);
CREATE TABLE character_aliases (
  work_id      TEXT NOT NULL,
  character_id TEXT NOT NULL,
  alias        TEXT NOT NULL,
  ord          INTEGER NOT NULL
);

CREATE TABLE recaps (
  work_id         TEXT NOT NULL,
  through_chapter INTEGER NOT NULL,
  scope           TEXT,
  text            TEXT NOT NULL,
  license         TEXT NOT NULL,
  PRIMARY KEY (work_id, through_chapter)
);
CREATE INDEX idx_recaps_work ON recaps(work_id);

CREATE TABLE recap_summaries (
  work_id  TEXT PRIMARY KEY,
  in_short TEXT,
  ending   TEXT,
  license  TEXT NOT NULL
);

CREATE VIRTUAL TABLE search_fts USING fts5(kind UNINDEXED, id UNINDEXED, title, names);
`

// Build writes the SQLite artifact for cat to outPath. builtAt is recorded in
// meta(built_at); pass the zero time to use the current UTC time. A work's
// added_at comes from the record itself (see addedAt), which is why this takes
// no date map: the pack layout moved that value into the data, where the
// importer and the intake bot stamp it and the migration backfilled it.
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

	st, closeStmts, err := prepareStmts(tx)
	if err != nil {
		return err
	}
	defer closeStmts()

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

	if err = insertPeople(st, people); err != nil {
		return err
	}
	if err = insertWorks(st, works, nameByID, seriesNamesByWork); err != nil {
		return err
	}
	if err = insertSeries(st, series); err != nil {
		return err
	}

	characters := append([]*model.Characters(nil), cat.Characters...)
	sort.Slice(characters, func(i, j int) bool { return characters[i].Work < characters[j].Work })
	recaps := append([]*model.Recaps(nil), cat.Recaps...)
	sort.Slice(recaps, func(i, j int) bool { return recaps[i].Work < recaps[j].Work })
	nChar := 0
	for _, c := range characters {
		nChar += len(c.Characters)
	}
	nRecap := 0
	for _, rc := range recaps {
		nRecap += len(rc.Recaps)
	}
	if err = insertCharacters(st, characters); err != nil {
		return err
	}
	if err = insertRecaps(st, recaps); err != nil {
		return err
	}
	if err = insertRecapSummaries(st, recaps); err != nil {
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
		{"count_characters", strconv.Itoa(nChar)},
		{"count_recaps", strconv.Itoa(nRecap)},
	}
	for _, kv := range metaRows {
		if _, err = tx.Exec(`INSERT INTO meta(key, value) VALUES(?, ?)`, kv[0], kv[1]); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// stmts holds every per-row INSERT statement the build writes through, prepared
// once for the whole transaction. database/sql re-parses the SQL text on every
// tx.Exec, which at catalogue scale dominates the build, so every row loop goes
// through a statement prepared once instead. The 8-row meta table stays on a
// plain tx.Exec - preparing for it would cost more than it saves.
type stmts struct {
	person    *sql.Stmt
	personFTS *sql.Stmt

	work       *sql.Stmt
	workAuthor *sql.Stmt
	workISBN   *sql.Stmt
	workGenre  *sql.Stmt
	workFTS    *sql.Stmt

	recording   *sql.Stmt
	recNarrator *sql.Stmt
	recASIN     *sql.Stmt
	recISBN     *sql.Stmt
	chapter     *sql.Stmt

	series       *sql.Stmt
	seriesWork   *sql.Stmt
	seriesAuthor *sql.Stmt
	seriesFTS    *sql.Stmt

	character      *sql.Stmt
	characterAlias *sql.Stmt

	recap        *sql.Stmt
	recapSummary *sql.Stmt
}

// prepareStmts prepares every insert statement on tx and returns the set plus a
// func releasing them all. The first failure returns immediately (closing what
// was already prepared), so a caller can never be handed a set holding a nil
// statement. Build owns the transaction, so it is the single prepare and close
// site for the whole build.
func prepareStmts(tx *sql.Tx) (*stmts, func(), error) {
	var prepared []*sql.Stmt
	closeAll := func() {
		for _, st := range prepared {
			_ = st.Close()
		}
	}

	s := &stmts{}
	for _, spec := range []struct {
		dst   **sql.Stmt
		query string
	}{
		{&s.person, `INSERT INTO people(id, name, sort_name, description, wikidata, openlibrary, audible, license) VALUES(?,?,?,?,?,?,?,?)`},
		{&s.personFTS, `INSERT INTO search_fts(kind, id, title, names) VALUES('person', ?, ?, '')`},

		{&s.work, `INSERT INTO works(id, title, subtitle, language, first_published, description, added_at, wikidata, openlibrary, goodreads, license) VALUES(?,?,?,?,?,?,?,?,?,?,?)`},
		{&s.workAuthor, `INSERT INTO work_authors(work_id, person_id, ord) VALUES(?,?,?)`},
		{&s.workISBN, `INSERT INTO work_isbns(work_id, isbn) VALUES(?,?)`},
		{&s.workGenre, `INSERT INTO work_genres(work_id, genre) VALUES(?,?)`},
		{&s.workFTS, `INSERT INTO search_fts(kind, id, title, names) VALUES('work', ?, ?, ?)`},

		{&s.recording, `INSERT INTO recordings(work_id, id, abridged, language, runtime_min, release_date, publisher, cover_url, license) VALUES(?,?,?,?,?,?,?,?,?)`},
		{&s.recNarrator, `INSERT INTO recording_narrators(work_id, recording_id, person_id, ord) VALUES(?,?,?,?)`},
		{&s.recASIN, `INSERT INTO recording_asins(region, asin, work_id, recording_id) VALUES(?,?,?,?)`},
		{&s.recISBN, `INSERT INTO recording_isbns(work_id, recording_id, isbn) VALUES(?,?,?)`},
		{&s.chapter, `INSERT INTO chapters(work_id, recording_id, idx, title, start_ms, length_ms) VALUES(?,?,?,?,?,?)`},

		{&s.series, `INSERT INTO series(id, name, wikidata, goodreads, license) VALUES(?,?,?,?,?)`},
		{&s.seriesWork, `INSERT INTO series_works(series_id, work_id, position) VALUES(?,?,?)`},
		{&s.seriesAuthor, `INSERT INTO series_authors(series_id, person_id, ord) VALUES(?,?,?)`},
		{&s.seriesFTS, `INSERT INTO search_fts(kind, id, title, names) VALUES('series', ?, ?, '')`},

		{&s.character, `INSERT INTO characters(work_id, id, name, role, reveal_chapter, description, wikidata, goodreads, ord, license) VALUES(?,?,?,?,?,?,?,?,?,?)`},
		{&s.characterAlias, `INSERT INTO character_aliases(work_id, character_id, alias, ord) VALUES(?,?,?,?)`},

		{&s.recap, `INSERT INTO recaps(work_id, through_chapter, scope, text, license) VALUES(?,?,?,?,?)`},
		{&s.recapSummary, `INSERT INTO recap_summaries(work_id, in_short, ending, license) VALUES(?,?,?,?)`},
	} {
		st, err := tx.Prepare(spec.query)
		if err != nil {
			closeAll()
			return nil, nil, fmt.Errorf("prepare %q: %w", spec.query, err)
		}
		prepared = append(prepared, st)
		*spec.dst = st
	}
	return s, closeAll, nil
}

func insertPeople(st *stmts, people []*model.Person) error {
	for _, p := range people {
		var wiki, ol, aud string
		if p.Xref != nil {
			wiki, ol, aud = p.Xref.Wikidata, p.Xref.Openlibrary, p.Xref.Audible
		}
		if _, err := st.person.Exec(
			p.ID, p.Name, nullStr(p.SortName), nullStr(p.Description), nullStr(wiki), nullStr(ol), nullStr(aud), p.License,
		); err != nil {
			return err
		}
		if _, err := st.personFTS.Exec(p.ID, p.Name); err != nil {
			return err
		}
	}
	return nil
}

func insertWorks(st *stmts, works []*model.Work, nameByID map[string]string, seriesNamesByWork map[string][]string) error {
	for _, w := range works {
		var wiki, ol, gr string
		var isbns []string
		if w.Xref != nil {
			wiki, ol, gr, isbns = w.Xref.Wikidata, w.Xref.Openlibrary, w.Xref.Goodreads, w.Xref.ISBN
		}
		if _, err := st.work.Exec(
			w.ID, w.Title, nullStr(w.Subtitle), w.Language, nullStr(w.FirstPublished), nullStr(w.Description), addedAt(w), nullStr(wiki), nullStr(ol), nullStr(gr), w.License,
		); err != nil {
			return err
		}
		for i, a := range w.Authors {
			if _, err := st.workAuthor.Exec(w.ID, a, i); err != nil {
				return err
			}
		}
		for _, isbn := range isbns {
			if _, err := st.workISBN.Exec(w.ID, isbn); err != nil {
				return err
			}
		}
		// Genres are a SET, like work_isbns: no order column, because checkGenresSorted
		// already pins the file order to ascending, so the rows are deterministic and
		// a reader just orders by genre.
		for _, g := range w.Genres {
			if _, err := st.workGenre.Exec(w.ID, g); err != nil {
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
			if err := insertRecording(st, w.ID, r); err != nil {
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
		if _, err := st.workFTS.Exec(w.ID, ftsTitle, strings.Join(names, " ")); err != nil {
			return err
		}
	}
	return nil
}

func insertRecording(st *stmts, workID string, r *model.Recording) error {
	if _, err := st.recording.Exec(
		workID, r.ID, boolInt(r.Abridged), r.Language, nullInt(r.RuntimeMin), nullStr(r.ReleaseDate), nullStr(r.Publisher), nullStr(r.CoverURL), r.License,
	); err != nil {
		return err
	}
	for i, n := range r.Narrators {
		if _, err := st.recNarrator.Exec(workID, r.ID, n, i); err != nil {
			return err
		}
	}
	for _, a := range r.ASIN {
		if _, err := st.recASIN.Exec(a.Region, a.ASIN, workID, r.ID); err != nil {
			return err
		}
	}
	for _, isbn := range r.ISBN {
		if _, err := st.recISBN.Exec(workID, r.ID, isbn); err != nil {
			return err
		}
	}
	for i, ch := range r.Chapters {
		if _, err := st.chapter.Exec(workID, r.ID, i, ch.Title, ch.StartMS, ch.LengthMS); err != nil {
			return err
		}
	}
	return nil
}

func insertSeries(st *stmts, series []*model.Series) error {
	for _, s := range series {
		var wiki, gr string
		if s.Xref != nil {
			wiki, gr = s.Xref.Wikidata, s.Xref.Goodreads
		}
		if _, err := st.series.Exec(s.ID, s.Name, nullStr(wiki), nullStr(gr), s.License); err != nil {
			return err
		}
		for _, sw := range s.Works {
			if _, err := st.seriesWork.Exec(s.ID, sw.Work, sw.Position); err != nil {
				return err
			}
		}
		for i, a := range s.Authors {
			if _, err := st.seriesAuthor.Exec(s.ID, a, i); err != nil {
				return err
			}
		}
		if _, err := st.seriesFTS.Exec(s.ID, s.Name); err != nil {
			return err
		}
	}
	return nil
}

// insertCharacters writes the per-work character sidecars. Each file's entries
// keep their authored order (ord); characters is pre-sorted by work id for a
// deterministic build.
func insertCharacters(st *stmts, characters []*model.Characters) error {
	for _, cf := range characters {
		for i, ch := range cf.Characters {
			var wiki, gr string
			if ch.Xref != nil {
				wiki, gr = ch.Xref.Wikidata, ch.Xref.Goodreads
			}
			if _, err := st.character.Exec(
				cf.Work, ch.ID, ch.Name, nullStr(ch.Role), ch.Reveal.Chapter, nullStr(ch.Description), nullStr(wiki), nullStr(gr), i, cf.License,
			); err != nil {
				return err
			}
			for j, alias := range ch.Aliases {
				if _, err := st.characterAlias.Exec(cf.Work, ch.ID, alias, j); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// insertRecaps writes the per-work recap sidecars. No stored order column is
// needed: recaps are keyed (and served) by their unique through-chapter, so
// recapsOf orders by it directly.
func insertRecaps(st *stmts, recaps []*model.Recaps) error {
	for _, rf := range recaps {
		for _, r := range rf.Recaps {
			if _, err := st.recap.Exec(rf.Work, r.Through.Chapter, nullStr(r.Scope), r.Text, rf.License); err != nil {
				return err
			}
		}
	}
	return nil
}

// insertRecapSummaries writes one recap_summaries row per work whose recaps
// sidecar carries a whole-book summary (in_short and/or ending). recaps is
// pre-sorted by work id, so rows land in a deterministic order; an empty field
// is stored as NULL and a sidecar with neither field yields no row. The row's
// license mirrors the sidecar's so a source can be retracted wholesale.
func insertRecapSummaries(st *stmts, recaps []*model.Recaps) error {
	for _, rf := range recaps {
		if rf.InShort == "" && rf.Ending == "" {
			continue
		}
		if _, err := st.recapSummary.Exec(rf.Work, nullStr(rf.InShort), nullStr(rf.Ending), rf.License); err != nil {
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

// addedAt resolves a work's added_at value: the record's own added_at when it
// has one, else the newest sources[].imported_at on it, else NULL.
//
// The field is the primary source because a pack file cannot be dated from git
// history the way one file per work could (a pack's add-date is not its
// entries'): the importer and the intake bot stamp added_at at creation, and the
// storage migration backfilled it from that history one final time. The
// imported_at fallback stays for records that carry neither.
func addedAt(w *model.Work) any {
	if w.AddedAt != "" {
		return w.AddedAt
	}
	newest, newestKey := "", ""
	for _, s := range w.Sources {
		if s.ImportedAt == "" {
			continue
		}
		if key := timeKey(s.ImportedAt); newestKey == "" || key > newestKey {
			newest, newestKey = s.ImportedAt, key
		}
	}
	if newest != "" {
		return newest
	}
	return nil
}

// timeKey returns a value that orders timestamps CHRONOLOGICALLY under string
// comparison. A plain date ("2026-07-29") is already such a key; a full RFC 3339
// timestamp is not, because the offset travels with it - "2026-07-29T01:00:00Z"
// is later than "2026-07-29T02:00:00+05:00" but sorts before it - so it is
// normalized to UTC first.
//
// The key is only ever compared, never stored: addedAt returns the ORIGINAL
// string, so normalizing here cannot change a byte in the artifact. Today's
// imported_at values are all date-only, which makes this a no-op on the current
// data by construction, and correct the moment one is not.
func timeKey(s string) string {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC().Format(time.RFC3339)
	}
	return s
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
