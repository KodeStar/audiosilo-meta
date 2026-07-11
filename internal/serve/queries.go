package serve

import (
	"database/sql"
	"sort"
	"strconv"
	"strings"
)

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
	out := make([]*workCard, 0, limit)
	perSeries := map[string]int{}
	for _, id := range ids {
		if len(out) == limit {
			break
		}
		card, err := s.workCard(id)
		if err != nil {
			return nil, err
		}
		if card == nil {
			continue
		}
		if card.Series != nil {
			if perSeries[card.Series.ID] >= latestSeriesCap {
				continue
			}
			perSeries[card.Series.ID]++
		}
		out = append(out, card)
	}
	return out, nil
}

// cards builds work cards for the given ids, preserving order.
func (s *snapshot) cards(ids []string) ([]*workCard, error) {
	out := make([]*workCard, 0, len(ids))
	for _, id := range ids {
		wc, err := s.workCard(id)
		if err != nil {
			return nil, err
		}
		if wc != nil {
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

type workDetail struct {
	ID             string            `json:"id"`
	Title          string            `json:"title"`
	Subtitle       string            `json:"subtitle,omitempty"`
	Authors        []personRef       `json:"authors"`
	Language       string            `json:"language"`
	FirstPublished string            `json:"first_published,omitempty"`
	Description    string            `json:"description,omitempty"`
	Series         []seriesRef       `json:"series"`
	Xref           *workXref         `json:"xref,omitempty"`
	Recordings     []recordingDetail `json:"recordings"`
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
	return &d, nil
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

func (s *snapshot) workISBNs(workID string) ([]string, error) {
	rows, err := s.db.Query(`SELECT isbn FROM work_isbns WHERE work_id=? ORDER BY isbn`, workID)
	if err != nil {
		return nil, err
	}
	return scanIDs(rows)
}

func (s *snapshot) recordingsOf(workID string) ([]recordingDetail, error) {
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
		if err = s.db.QueryRow(`SELECT COUNT(*) FROM chapters WHERE work_id=? AND recording_id=?`, workID, rid).
			Scan(&recs[i].ChapterCount); err != nil {
			return nil, err
		}
	}
	return recs, nil
}

func (s *snapshot) narratorsOf(workID, rid string) ([]personRef, error) {
	rows, err := s.db.Query(
		`SELECT p.id, p.name FROM recording_narrators rn JOIN people p ON p.id = rn.person_id WHERE rn.work_id=? AND rn.recording_id=? ORDER BY rn.ord`, workID, rid)
	if err != nil {
		return nil, err
	}
	return scanPersonRefs(rows)
}

func (s *snapshot) asinsOf(workID, rid string) ([]asinRef, error) {
	rows, err := s.db.Query(
		`SELECT region, asin FROM recording_asins WHERE work_id=? AND recording_id=? ORDER BY region, asin`, workID, rid)
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
	rows, err := s.db.Query(
		`SELECT isbn FROM recording_isbns WHERE work_id=? AND recording_id=? ORDER BY isbn`, workID, rid)
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

type personDetail struct {
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	SortName string          `json:"sort_name,omitempty"`
	Authored []*workCard     `json:"authored"`
	Narrated []narratedEntry `json:"narrated"`
}

func (s *snapshot) person(id string) (*personDetail, error) {
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

	rows, err := s.db.Query(
		`SELECT wa.work_id FROM work_authors wa JOIN works w ON w.id = wa.work_id WHERE wa.person_id=? ORDER BY w.title, wa.work_id`, id)
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

	nrows, err := s.db.Query(
		`SELECT rn.work_id, rn.recording_id FROM recording_narrators rn JOIN works w ON w.id = rn.work_id WHERE rn.person_id=? ORDER BY w.title, rn.work_id, rn.recording_id`, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = nrows.Close() }()
	d.Narrated = []narratedEntry{}
	for nrows.Next() {
		var workID, rid string
		if err := nrows.Scan(&workID, &rid); err != nil {
			return nil, err
		}
		card, err := s.workCard(workID)
		if err != nil {
			return nil, err
		}
		if card != nil {
			d.Narrated = append(d.Narrated, narratedEntry{Work: card, RecordingID: rid})
		}
	}
	if err := nrows.Err(); err != nil {
		return nil, err
	}
	return &d, nil
}

// ---- series -----------------------------------------------------------------

type seriesEntry struct {
	Position string    `json:"position"`
	Work     *workCard `json:"work"`
}

type seriesDetail struct {
	ID      string        `json:"id"`
	Name    string        `json:"name"`
	Authors []personRef   `json:"authors"`
	Works   []seriesEntry `json:"works"`
}

func (s *snapshot) series(id string) (*seriesDetail, error) {
	var d seriesDetail
	err := s.db.QueryRow(`SELECT id, name FROM series WHERE id=?`, id).Scan(&d.ID, &d.Name)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	arows, err := s.db.Query(
		`SELECT p.id, p.name FROM series_authors sa JOIN people p ON p.id = sa.person_id WHERE sa.series_id=? ORDER BY sa.ord`, id)
	if err != nil {
		return nil, err
	}
	if d.Authors, err = scanPersonRefs(arows); err != nil {
		return nil, err
	}

	wrows, err := s.db.Query(`SELECT work_id, position FROM series_works WHERE series_id=?`, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = wrows.Close() }()
	d.Works = []seriesEntry{}
	for wrows.Next() {
		var workID, pos string
		if err := wrows.Scan(&workID, &pos); err != nil {
			return nil, err
		}
		card, err := s.workCard(workID)
		if err != nil {
			return nil, err
		}
		if card != nil {
			d.Works = append(d.Works, seriesEntry{Position: pos, Work: card})
		}
	}
	if err := wrows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(d.Works, func(i, j int) bool {
		return positionStart(d.Works[i].Position) < positionStart(d.Works[j].Position)
	})
	return &d, nil
}

// positionStart returns the numeric start of a series position string: "2.5"
// -> 2.5, "1-3.5" -> 1, unparseable -> +Inf-ish large so it sorts last.
func positionStart(pos string) float64 {
	pos = strings.TrimSpace(pos)
	if i := strings.IndexByte(pos, '-'); i > 0 {
		pos = pos[:i]
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(pos), 64)
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
