package serve

import (
	"database/sql"
	"sort"
	"strconv"
	"strings"
)

// scalarInt runs a query expected to return a single integer (COUNT, etc.).
func (s *snapshot) scalarInt(query string, args ...any) (int, error) {
	var n int
	err := s.db.QueryRow(query, args...).Scan(&n)
	return n, err
}

// scanIDs collects a single string column into a slice.
func scanIDs(rows *sql.Rows) ([]string, error) {
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// latestSeriesCap is the per-series diversity cap for the latest-works list: a
// bulk import shares one added_at date, so without a cap the title tie-break
// clusters one series' volumes and fills the whole grid with them.
const latestSeriesCap = 2

// latestWorks returns up to limit work cards ordered by added_at DESC (NULLS
// LAST), then title, with at most latestSeriesCap works from any one series
// (keyed by the work's first series membership, the one the card carries).
// Works with no series are each their own bucket and are never capped. It
// over-fetches candidate rows so capped skips still leave enough to fill the
// page; the SQL ordering (id as the final tie-break) keeps the walk
// deterministic.
//
// It runs in TWO phases because the cap only needs to know each candidate's
// series: phase one resolves just that over the ~200 candidates, phase two
// builds full cards for the <=limit survivors. Building cards for every
// candidate would mean authors, series and covers for 200 works to return 12 -
// on the home page, the most-hit endpoint there is.
func (s *snapshot) latestWorks(limit int) ([]*workCard, error) {
	fetch := limit * 4
	if fetch < 200 {
		fetch = 200
	}
	rows, err := s.db.Query(
		`SELECT id FROM works ORDER BY (added_at IS NULL) ASC, added_at DESC, title ASC, id ASC LIMIT ?`, fetch)
	if err != nil {
		return nil, err
	}
	ids, err := scanIDs(rows)
	if err != nil {
		return nil, err
	}
	series, err := s.firstSeriesByWork(ids)
	if err != nil {
		return nil, err
	}
	winners := make([]string, 0, limit)
	perSeries := map[string]int{}
	for _, id := range ids {
		if len(winners) == limit {
			break
		}
		if sr := series[id]; sr != nil {
			if perSeries[sr.ID] >= latestSeriesCap {
				continue
			}
			perSeries[sr.ID]++
		}
		winners = append(winners, id)
	}
	return s.cards(winners)
}

// cards builds work cards for the given ids, preserving order and skipping ids
// with no work. It resolves the whole set in a fixed number of queries
// (cardsByID) rather than four per id: the caller's list length is the size of a
// person's or series' credit list, which is unbounded.
func (s *snapshot) cards(ids []string) ([]*workCard, error) {
	byID, err := s.cardsByID(ids)
	if err != nil {
		return nil, err
	}
	out := make([]*workCard, 0, len(ids))
	for _, id := range ids {
		if wc := byID[id]; wc != nil {
			out = append(out, wc)
		}
	}
	return out, nil
}

// ---- work detail ------------------------------------------------------------

type workXref struct {
	Wikidata    string   `json:"wikidata,omitempty"`
	Openlibrary string   `json:"openlibrary,omitempty"`
	Goodreads   string   `json:"goodreads,omitempty"`
	ISBN        []string `json:"isbn,omitempty"`
}

type recordingDetail struct {
	ID           string      `json:"id"`
	Narrators    []personRef `json:"narrators"`
	Abridged     bool        `json:"abridged,omitempty"`
	RuntimeMin   int         `json:"runtime_min,omitempty"`
	ReleaseDate  string      `json:"release_date,omitempty"`
	Publisher    string      `json:"publisher,omitempty"`
	ASIN         []asinRef   `json:"asin"`
	ISBN         []string    `json:"isbn"`
	CoverURL     string      `json:"cover_url,omitempty"`
	ChapterCount int         `json:"chapter_count"`
}

type asinRef struct {
	Region string `json:"region"`
	ASIN   string `json:"asin"`
}

type positionOut struct {
	Chapter int `json:"chapter"`
}

type characterXref struct {
	Wikidata  string `json:"wikidata,omitempty"`
	Goodreads string `json:"goodreads,omitempty"`
}

type characterOut struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Aliases     []string       `json:"aliases,omitempty"`
	Role        string         `json:"role,omitempty"`
	Reveal      positionOut    `json:"reveal"`
	Description string         `json:"description,omitempty"`
	Xref        *characterXref `json:"xref,omitempty"`
}

type recapOut struct {
	Through positionOut `json:"through"`
	Scope   string      `json:"scope,omitempty"`
	Text    string      `json:"text"`
}

// recapSummaryOut is the per-work whole-book summary: a one-paragraph refresher
// and a plain statement of how the book ends. Both fields are optional.
type recapSummaryOut struct {
	InShort string `json:"in_short,omitempty"`
	Ending  string `json:"ending,omitempty"`
}

type workDetail struct {
	ID             string            `json:"id"`
	Title          string            `json:"title"`
	Subtitle       string            `json:"subtitle,omitempty"`
	Authors        []personRef       `json:"authors"`
	Language       string            `json:"language"`
	FirstPublished string            `json:"first_published,omitempty"`
	Description    string            `json:"description,omitempty"`
	Genres         []string          `json:"genres,omitempty"`
	Series         []seriesRef       `json:"series"`
	Xref           *workXref         `json:"xref,omitempty"`
	Recordings     []recordingDetail `json:"recordings"`
	Characters     []characterOut    `json:"characters,omitempty"`
	Recaps         []recapOut        `json:"recaps,omitempty"`
	RecapSummary   *recapSummaryOut  `json:"recap_summary,omitempty"`
}

// workDetail returns the full work document, or (nil, nil) when absent.
func (s *snapshot) workDetail(id string) (*workDetail, error) {
	var d workDetail
	var subtitle, firstPub, desc, wiki, ol, gr sql.NullString
	err := s.db.QueryRow(
		`SELECT id, title, subtitle, language, first_published, description, wikidata, openlibrary, goodreads FROM works WHERE id=?`, id).
		Scan(&d.ID, &d.Title, &subtitle, &d.Language, &firstPub, &desc, &wiki, &ol, &gr)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	d.Subtitle = subtitle.String
	d.FirstPublished = firstPub.String
	d.Description = desc.String

	if d.Authors, err = s.authorsOf(id); err != nil {
		return nil, err
	}
	if d.Genres, err = s.workGenres(id); err != nil {
		return nil, err
	}
	if d.Series, err = s.seriesOf(id); err != nil {
		return nil, err
	}

	isbns, err := s.workISBNs(id)
	if err != nil {
		return nil, err
	}
	if wiki.String != "" || ol.String != "" || gr.String != "" || len(isbns) > 0 {
		d.Xref = &workXref{Wikidata: wiki.String, Openlibrary: ol.String, Goodreads: gr.String, ISBN: isbns}
	}

	if d.Recordings, err = s.recordingsOf(id); err != nil {
		return nil, err
	}
	if d.Characters, err = s.charactersOf(id); err != nil {
		return nil, err
	}
	if d.Recaps, err = s.recapsOf(id); err != nil {
		return nil, err
	}
	if d.RecapSummary, err = s.recapSummaryOf(id); err != nil {
		return nil, err
	}
	return &d, nil
}

// workForABS returns just the slice of a work absBooksFor consumes: the work
// row, authors, series, print ISBNs, and recordings (with narrators/asins/isbns
// but NO chapter count), and NONE of the characters/recaps/recap-summary
// sidecars. absSearch calls it once per candidate on the public /abs/search hot
// path, so it deliberately skips workDetail's ~100+ discarded round-trips.
// genresByWork is the batched work id -> genre slugs map absSearch resolves for
// the whole candidate set up front, so this runs no per-work genre query either.
// Returns (nil, nil) when the work is absent.
func (s *snapshot) workForABS(id string, genresByWork map[string][]string) (*workDetail, error) {
	var d workDetail
	var subtitle, firstPub, desc sql.NullString
	err := s.db.QueryRow(
		`SELECT id, title, subtitle, language, first_published, description FROM works WHERE id=?`, id).
		Scan(&d.ID, &d.Title, &subtitle, &d.Language, &firstPub, &desc)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	d.Subtitle = subtitle.String
	d.FirstPublished = firstPub.String
	d.Description = desc.String

	if d.Authors, err = s.authorsOf(id); err != nil {
		return nil, err
	}
	d.Genres = genresByWork[id]
	if d.Series, err = s.seriesOf(id); err != nil {
		return nil, err
	}
	// absBooksFor only reads the work's print ISBN off Xref; skip the other xref
	// identifiers entirely.
	isbns, err := s.workISBNs(id)
	if err != nil {
		return nil, err
	}
	if len(isbns) > 0 {
		d.Xref = &workXref{ISBN: isbns}
	}
	if d.Recordings, err = s.recordingsBase(id); err != nil {
		return nil, err
	}
	return &d, nil
}

// sidecarSchemaVersion is the artifact schema_version that first carried the
// characters/recaps tables. A newer binary may briefly serve an older release,
// so the sidecar queries no-op below it rather than probing for the tables.
const sidecarSchemaVersion = 2

// summarySchemaVersion is the artifact schema_version that first carried the
// recap_summaries table (per-work in_short / ending). A newer binary serving an
// older (v2) release must degrade to "no summary", so the query no-ops below
// this version rather than probing for the table.
const summarySchemaVersion = 3

// genresSchemaVersion is the artifact schema_version that first carried the
// work_genres table. A newer binary serving an older (v3 or earlier) release
// must degrade to "no genres", so the query no-ops below this version rather
// than probing for the table.
const genresSchemaVersion = 4

// charactersOf returns the per-work character sidecar entries in authored order,
// or nil when the work has none (or the artifact predates the sidecar tables).
func (s *snapshot) charactersOf(workID string) ([]characterOut, error) {
	if s.schemaVersion < sidecarSchemaVersion {
		return nil, nil
	}
	rows, err := s.db.Query(
		`SELECT id, name, role, reveal_chapter, description, wikidata, goodreads FROM characters WHERE work_id=? ORDER BY ord`, workID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []characterOut
	for rows.Next() {
		var c characterOut
		var role, desc, wiki, gr sql.NullString
		if err := rows.Scan(&c.ID, &c.Name, &role, &c.Reveal.Chapter, &desc, &wiki, &gr); err != nil {
			return nil, err
		}
		c.Role, c.Description = role.String, desc.String
		if wiki.String != "" || gr.String != "" {
			c.Xref = &characterXref{Wikidata: wiki.String, Goodreads: gr.String}
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	arows, err := s.db.Query(characterAliasSQL, workID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = arows.Close() }()
	aliases := map[string][]string{}
	for arows.Next() {
		var cid, alias string
		if err := arows.Scan(&cid, &alias); err != nil {
			return nil, err
		}
		aliases[cid] = append(aliases[cid], alias)
	}
	if err := arows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Aliases = aliases[out[i].ID]
	}
	return out, nil
}

// recapsOf returns the per-work recap sidecar entries ordered by position, or
// nil when the work has none (or the artifact predates the sidecar tables).
func (s *snapshot) recapsOf(workID string) ([]recapOut, error) {
	if s.schemaVersion < sidecarSchemaVersion {
		return nil, nil
	}
	rows, err := s.db.Query(
		`SELECT through_chapter, scope, text FROM recaps WHERE work_id=? ORDER BY through_chapter`, workID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []recapOut
	for rows.Next() {
		var r recapOut
		var scope sql.NullString
		if err := rows.Scan(&r.Through.Chapter, &scope, &r.Text); err != nil {
			return nil, err
		}
		r.Scope = scope.String
		out = append(out, r)
	}
	return out, rows.Err()
}

// recapSummaryOf returns the per-work whole-book summary (in_short / ending), or
// nil when the work has none (or the artifact predates the recap_summaries
// table). A row present with both fields empty is treated as no summary.
func (s *snapshot) recapSummaryOf(workID string) (*recapSummaryOut, error) {
	if s.schemaVersion < summarySchemaVersion {
		return nil, nil
	}
	var inShort, ending sql.NullString
	err := s.db.QueryRow(`SELECT in_short, ending FROM recap_summaries WHERE work_id=?`, workID).
		Scan(&inShort, &ending)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if inShort.String == "" && ending.String == "" {
		return nil, nil
	}
	return &recapSummaryOut{InShort: inShort.String, Ending: ending.String}, nil
}

func (s *snapshot) seriesOf(workID string) ([]seriesRef, error) {
	rows, err := s.db.Query(
		`SELECT s.id, s.name, sw.position FROM series_works sw JOIN series s ON s.id = sw.series_id WHERE sw.work_id=? ORDER BY s.id`, workID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []seriesRef{}
	for rows.Next() {
		var sr seriesRef
		if err := rows.Scan(&sr.ID, &sr.Name, &sr.Position); err != nil {
			return nil, err
		}
		out = append(out, sr)
	}
	return out, rows.Err()
}

// workGenres returns a work's normalized genre slugs ascending, or nil when the
// work has none (or the artifact predates the work_genres table). Genres are a
// set, so genre is the order (checkGenresSorted pins the authored files to the
// same ascending order).
func (s *snapshot) workGenres(workID string) ([]string, error) {
	if s.schemaVersion < genresSchemaVersion {
		return nil, nil
	}
	rows, err := s.db.Query(`SELECT genre FROM work_genres WHERE work_id=? ORDER BY genre`, workID)
	if err != nil {
		return nil, err
	}
	return scanIDs(rows)
}

func (s *snapshot) workISBNs(workID string) ([]string, error) {
	rows, err := s.db.Query(`SELECT isbn FROM work_isbns WHERE work_id=? ORDER BY isbn`, workID)
	if err != nil {
		return nil, err
	}
	return scanIDs(rows)
}

// recordingsBase fetches a work's recordings with their narrators, ASINs, and
// ISBNs, but WITHOUT the per-recording chapter count. recordingsOf adds the
// count on top; the ABS path (workForABS) reuses this directly, since it never
// reads the count - a hot public endpoint should not run a COUNT(*) per
// recording it discards.
func (s *snapshot) recordingsBase(workID string) ([]recordingDetail, error) {
	rows, err := s.db.Query(
		`SELECT id, abridged, runtime_min, release_date, publisher, cover_url FROM recordings WHERE work_id=? ORDER BY id`, workID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var recs []recordingDetail
	for rows.Next() {
		var rd recordingDetail
		var abridged int
		var runtime sql.NullInt64
		var release, publisher, cover sql.NullString
		if err := rows.Scan(&rd.ID, &abridged, &runtime, &release, &publisher, &cover); err != nil {
			return nil, err
		}
		rd.Abridged = abridged != 0
		rd.RuntimeMin = int(runtime.Int64)
		rd.ReleaseDate = release.String
		rd.Publisher = publisher.String
		rd.CoverURL = cover.String
		recs = append(recs, rd)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range recs {
		rid := recs[i].ID
		if recs[i].Narrators, err = s.narratorsOf(workID, rid); err != nil {
			return nil, err
		}
		if recs[i].ASIN, err = s.asinsOf(workID, rid); err != nil {
			return nil, err
		}
		if recs[i].ISBN, err = s.recordingISBNs(workID, rid); err != nil {
			return nil, err
		}
	}
	return recs, nil
}

func (s *snapshot) recordingsOf(workID string) ([]recordingDetail, error) {
	recs, err := s.recordingsBase(workID)
	if err != nil {
		return nil, err
	}
	for i := range recs {
		if err = s.db.QueryRow(recordingCountSQL, workID, recs[i].ID).
			Scan(&recs[i].ChapterCount); err != nil {
			return nil, err
		}
	}
	return recs, nil
}

// The per-recording queries. GET /works/{id} runs all three once per recording,
// so their plans are the ones the covering indexes exist for; they are named
// constants so the index guard checks the real text.
const (
	narratorsOfSQL = `SELECT p.id, p.name FROM recording_narrators rn JOIN people p ON p.id = rn.person_id ` +
		`WHERE rn.work_id=? AND rn.recording_id=? ORDER BY rn.ord`
	asinsOfSQL          = `SELECT region, asin FROM recording_asins WHERE work_id=? AND recording_id=? ORDER BY region, asin`
	recordingISBNsSQL   = `SELECT isbn FROM recording_isbns WHERE work_id=? AND recording_id=? ORDER BY isbn`
	characterAliasSQL   = `SELECT character_id, alias FROM character_aliases WHERE work_id=? ORDER BY character_id, ord`
	recordingCountSQL   = `SELECT COUNT(*) FROM chapters WHERE work_id=? AND recording_id=?`
	seriesWorksCountSQL = `SELECT COUNT(*) FROM series_works WHERE series_id=?`
)

func (s *snapshot) narratorsOf(workID, rid string) ([]personRef, error) {
	rows, err := s.db.Query(narratorsOfSQL, workID, rid)
	if err != nil {
		return nil, err
	}
	return scanPersonRefs(rows)
}

func (s *snapshot) asinsOf(workID, rid string) ([]asinRef, error) {
	rows, err := s.db.Query(asinsOfSQL, workID, rid)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []asinRef{}
	for rows.Next() {
		var a asinRef
		if err := rows.Scan(&a.Region, &a.ASIN); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *snapshot) recordingISBNs(workID, rid string) ([]string, error) {
	rows, err := s.db.Query(recordingISBNsSQL, workID, rid)
	if err != nil {
		return nil, err
	}
	out, err := scanIDs(rows)
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = []string{}
	}
	return out, nil
}

// ---- chapters ---------------------------------------------------------------

type chapterOut struct {
	Title    string `json:"title"`
	StartMS  int64  `json:"start_ms"`
	LengthMS int64  `json:"length_ms"`
}

func (s *snapshot) chapters(workID, rid string) ([]chapterOut, error) {
	rows, err := s.db.Query(
		`SELECT title, start_ms, length_ms FROM chapters WHERE work_id=? AND recording_id=? ORDER BY idx`, workID, rid)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []chapterOut{}
	for rows.Next() {
		var c chapterOut
		if err := rows.Scan(&c.Title, &c.StartMS, &c.LengthMS); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ---- person -----------------------------------------------------------------

type narratedEntry struct {
	Work        *workCard `json:"work"`
	RecordingID string    `json:"recording_id"`
}

// personDetail is one person plus a PAGE of each credit list. The totals are
// the unpaged counts, so a client can tell that it is seeing a slice and page
// through it; limit/offset echo the window that was applied. Both lists use the
// same window (a person page shows both side by side).
//
// The lists are paginated because a person's credits are unbounded: corporate
// credits ("Full Cast", "Audible Studios") already narrate ~2,000 works at 9.5k
// works and grow with the catalogue. The new fields are additive - existing
// consumers that read only authored/narrated keep working.
type personDetail struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	SortName      string          `json:"sort_name,omitempty"`
	Authored      []*workCard     `json:"authored"`
	Narrated      []narratedEntry `json:"narrated"`
	AuthoredTotal int             `json:"authored_total"`
	NarratedTotal int             `json:"narrated_total"`
	Limit         int             `json:"limit"`
	Offset        int             `json:"offset"`
}

// The person page's four queries. Each total counts over exactly the join its
// page query pages, so the total and the list can never disagree; they are named
// constants so the index guard EXPLAINs what actually runs.
const (
	authoredTotalSQL = `SELECT COUNT(*) FROM work_authors wa JOIN works w ON w.id = wa.work_id WHERE wa.person_id=?`
	narratedTotalSQL = `SELECT COUNT(*) FROM recording_narrators rn JOIN works w ON w.id = rn.work_id WHERE rn.person_id=?`

	authoredPageSQL = `SELECT wa.work_id FROM work_authors wa JOIN works w ON w.id = wa.work_id WHERE wa.person_id=? ` +
		`ORDER BY w.title, wa.work_id LIMIT ? OFFSET ?`
	narratedPageSQL = `SELECT rn.work_id, rn.recording_id FROM recording_narrators rn JOIN works w ON w.id = rn.work_id ` +
		`WHERE rn.person_id=? ORDER BY w.title, rn.work_id, rn.recording_id LIMIT ? OFFSET ?`
)

// person returns one page of a person's authored and narrated credits. limit
// and offset are applied to each list independently (both are ordered by work
// title, then id, so the window is stable across requests on one artifact).
func (s *snapshot) person(id string, limit, offset int) (*personDetail, error) {
	var d personDetail
	var sortName sql.NullString
	err := s.db.QueryRow(`SELECT id, name, sort_name FROM people WHERE id=?`, id).Scan(&d.ID, &d.Name, &sortName)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	d.SortName = sortName.String
	d.Limit, d.Offset = limit, offset

	// The totals count over the SAME join as the page queries, so "showing 4 of
	// 5" can never be reported when the 5th row references a work the artifact
	// does not carry: such a row is dropped from the page and must not be counted
	// in the total either.
	if d.AuthoredTotal, err = s.scalarInt(authoredTotalSQL, id); err != nil {
		return nil, err
	}
	if d.NarratedTotal, err = s.scalarInt(narratedTotalSQL, id); err != nil {
		return nil, err
	}

	rows, err := s.db.Query(authoredPageSQL, id, limit, offset)
	if err != nil {
		return nil, err
	}
	authoredIDs, err := scanIDs(rows)
	if err != nil {
		return nil, err
	}
	if d.Authored, err = s.cards(authoredIDs); err != nil {
		return nil, err
	}
	if d.Authored == nil {
		d.Authored = []*workCard{}
	}

	nrows, err := s.db.Query(narratedPageSQL, id, limit, offset)
	if err != nil {
		return nil, err
	}
	credits, err := scanPairs(nrows, func(workID, rid string) narratedCredit {
		return narratedCredit{workID: workID, recordingID: rid}
	})
	if err != nil {
		return nil, err
	}
	narratedIDs := make([]string, len(credits))
	for i, c := range credits {
		narratedIDs[i] = c.workID
	}
	byID, err := s.cardsByID(narratedIDs)
	if err != nil {
		return nil, err
	}
	d.Narrated = []narratedEntry{}
	for _, c := range credits {
		if card := byID[c.workID]; card != nil {
			d.Narrated = append(d.Narrated, narratedEntry{Work: card, RecordingID: c.recordingID})
		}
	}
	return &d, nil
}

// narratedCredit is one row of a narrator's credit list: which recording of
// which work. The same work appears once per recording the person narrated.
type narratedCredit struct{ workID, recordingID string }

// seriesMember is one row of a series' membership: a work and its position
// string ("2", "2.5", "1-3.5").
type seriesMember struct{ workID, position string }

// scanPairs collects two-column rows through mk and closes rows. The membership
// lists it reads are resolved to work cards in one batch afterwards, so the row
// set is materialized first rather than built card-by-card inside the scan.
func scanPairs[T any](rows *sql.Rows, mk func(first, second string) T) ([]T, error) {
	defer func() { _ = rows.Close() }()
	var out []T
	for rows.Next() {
		var a, b string
		if err := rows.Scan(&a, &b); err != nil {
			return nil, err
		}
		out = append(out, mk(a, b))
	}
	return out, rows.Err()
}

// ---- series -----------------------------------------------------------------

type seriesEntry struct {
	Position string    `json:"position"`
	Work     *workCard `json:"work"`
}

// seriesDetail is a series plus its member works in position order. WorksTotal
// is the unpaged membership count and Limit/Offset echo the window that was
// applied (Limit 0 = the whole series, the default - see snapshot.series). The
// three fields are additive; a consumer reading only works is unaffected.
type seriesDetail struct {
	ID         string        `json:"id"`
	Name       string        `json:"name"`
	Authors    []personRef   `json:"authors"`
	Works      []seriesEntry `json:"works"`
	WorksTotal int           `json:"works_total"`
	Limit      int           `json:"limit"`
	Offset     int           `json:"offset"`
}

// The series detail queries. seriesWorksSQL joins works so WorksTotal counts
// exactly the members that can be rendered: a membership row pointing at a work
// the artifact does not carry is dropped from the list, so counting it in the
// total would report a page shorter than it claims.
const (
	seriesAuthorsSQL = `SELECT p.id, p.name FROM series_authors sa JOIN people p ON p.id = sa.person_id ` +
		`WHERE sa.series_id=? ORDER BY sa.ord`
	seriesWorksSQL = `SELECT sw.work_id, sw.position FROM series_works sw JOIN works w ON w.id = sw.work_id ` +
		`WHERE sw.series_id=?`
)

// series returns a series with its member works. limit 0 means "all works",
// which is the endpoint's DEFAULT: unlike a person's credit list, series
// membership is bounded by what a series is (175 works at the top of the
// catalogue today), and audiosilo-server's /libraries/{id}/meta composes the
// player's series rail from the full list (workspace CROSS-REPO.md section 17) -
// truncating it by default would silently shorten that rail. Pagination is
// available for a client that wants it, and the member cards are resolved in a
// fixed number of queries either way.
//
// Position ordering is computed in Go (positionStart parses "2.5" and "1-3.5"),
// so a window is applied AFTER sorting, not by SQL.
func (s *snapshot) series(id string, limit, offset int) (*seriesDetail, error) {
	var d seriesDetail
	err := s.db.QueryRow(`SELECT id, name FROM series WHERE id=?`, id).Scan(&d.ID, &d.Name)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	d.Limit, d.Offset = limit, offset

	arows, err := s.db.Query(seriesAuthorsSQL, id)
	if err != nil {
		return nil, err
	}
	if d.Authors, err = scanPersonRefs(arows); err != nil {
		return nil, err
	}

	wrows, err := s.db.Query(seriesWorksSQL, id)
	if err != nil {
		return nil, err
	}
	members, err := scanPairs(wrows, func(workID, pos string) seriesMember {
		return seriesMember{workID: workID, position: pos}
	})
	if err != nil {
		return nil, err
	}
	d.WorksTotal = len(members)
	sort.SliceStable(members, func(i, j int) bool {
		return positionStart(members[i].position) < positionStart(members[j].position)
	})
	if offset > len(members) {
		offset = len(members)
	}
	members = members[offset:]
	if limit > 0 && limit < len(members) {
		members = members[:limit]
	}

	ids := make([]string, len(members))
	for i, m := range members {
		ids[i] = m.workID
	}
	byID, err := s.cardsByID(ids)
	if err != nil {
		return nil, err
	}
	d.Works = []seriesEntry{}
	for _, m := range members {
		if card := byID[m.workID]; card != nil {
			d.Works = append(d.Works, seriesEntry{Position: m.position, Work: card})
		}
	}
	return &d, nil
}

// parsePositionRange parses a series position string into its numeric span:
// "2" -> (2, 2), "2.5" -> (2.5, 2.5), "1-3.5" -> (1, 3.5). ok is false when
// either bound fails to parse. Callers apply their own policy to the span (sort
// key, integer coverage); this is the single copy of the position grammar in
// this package. A possible future home is pkg/model, next to the schema's
// position rules, if a third consumer ever appears.
func parsePositionRange(pos string) (lo, hi float64, ok bool) {
	pos = strings.TrimSpace(pos)
	if i := strings.IndexByte(pos, '-'); i > 0 {
		var err1, err2 error
		lo, err1 = strconv.ParseFloat(strings.TrimSpace(pos[:i]), 64)
		hi, err2 = strconv.ParseFloat(strings.TrimSpace(pos[i+1:]), 64)
		return lo, hi, err1 == nil && err2 == nil
	}
	f, err := strconv.ParseFloat(pos, 64)
	return f, f, err == nil
}

// positionStart returns the numeric start of a series position string: "2.5"
// -> 2.5, "1-3.5" -> 1, unparseable -> +Inf-ish large so it sorts last. A
// malformed range still sorts by its parseable prefix ("1-garbage" -> 1); only
// a value with no parseable start gets the sort-last sentinel.
func positionStart(pos string) float64 {
	if lo, _, ok := parsePositionRange(pos); ok {
		return lo
	}
	p := strings.TrimSpace(pos)
	if i := strings.IndexByte(p, '-'); i > 0 {
		p = strings.TrimSpace(p[:i])
	}
	f, err := strconv.ParseFloat(p, 64)
	if err != nil {
		return 1e18
	}
	return f
}

// ---- lookup -----------------------------------------------------------------

type lookupResult struct {
	Work        *workCard `json:"work"`
	RecordingID string    `json:"recording_id"`
}

func (s *snapshot) lookup(asin, isbn string) (*lookupResult, error) {
	var workID, rid string
	find := func(query, arg string) (bool, error) {
		err := s.db.QueryRow(query, arg).Scan(&workID, &rid)
		if err == sql.ErrNoRows {
			return false, nil
		}
		return err == nil, err
	}
	found := false
	var err error
	switch {
	case asin != "":
		found, err = find(`SELECT work_id, recording_id FROM recording_asins WHERE asin=? ORDER BY region LIMIT 1`, asin)
	case isbn != "":
		if found, err = find(`SELECT work_id, recording_id FROM recording_isbns WHERE isbn=? LIMIT 1`, isbn); err == nil && !found {
			// Fall back to a print ISBN on the work; point at its first recording.
			var wid string
			e := s.db.QueryRow(`SELECT work_id FROM work_isbns WHERE isbn=? LIMIT 1`, isbn).Scan(&wid)
			if e == nil {
				workID, found = wid, true
				_ = s.db.QueryRow(`SELECT id FROM recordings WHERE work_id=? ORDER BY id LIMIT 1`, wid).Scan(&rid)
			} else if e != sql.ErrNoRows {
				err = e
			}
		}
	}
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}
	card, err := s.workCard(workID)
	if err != nil {
		return nil, err
	}
	if card == nil {
		return nil, nil
	}
	return &lookupResult{Work: card, RecordingID: rid}, nil
}
