package query

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

const (
	abridgementSchemaVersion = 12
	CataloguePageMax         = 100
	listeningRecordingsLimit = 5
)

// abridgementColumn never treats a legacy false as proof of unabridgement.
func abridgementColumn(prefix string, present bool) string {
	if present {
		return `COALESCE(` + prefix + `abridgement, CASE WHEN ` + prefix + `abridged <> 0 THEN 'abridged' ELSE '' END)`
	}
	return `CASE WHEN ` + prefix + `abridged <> 0 THEN 'abridged' ELSE '' END`
}

// RecordingSummary contains catalogue facts without identifiers or chapter bodies.
type RecordingSummary struct {
	ID           string      `json:"id"`
	Narrators    []PersonRef `json:"narrators"`
	Language     string      `json:"language,omitempty"`
	Abridgement  string      `json:"abridgement,omitempty"`
	RuntimeMin   int         `json:"runtime_min,omitempty"`
	ReleaseDate  string      `json:"release_date,omitempty"`
	Publisher    string      `json:"publisher,omitempty"`
	ChapterCount int         `json:"chapter_count,omitempty"`
}

type ListeningGuideEntry struct {
	Position        string             `json:"position"`
	Work            *WorkCard          `json:"work"`
	RecordingsTotal int                `json:"recordings_total"`
	Recordings      []RecordingSummary `json:"recordings"`
	RecordingsLimit int                `json:"recordings_limit"`
}

type SeriesListeningGuide struct {
	ID            string                `json:"id"`
	Name          string                `json:"name"`
	Language      string                `json:"language,omitempty"`
	Ordering      string                `json:"ordering,omitempty"`
	OrderingOf    string                `json:"ordering_of,omitempty"`
	Authors       []PersonRef           `json:"authors"`
	Orderings     []SeriesOrdering      `json:"orderings,omitempty"`
	TranslationOf []SeriesTranslation   `json:"translation_of,omitempty"`
	Translations  []SeriesTranslation   `json:"translations,omitempty"`
	Entries       []ListeningGuideEntry `json:"entries"`
	WorksTotal    int                   `json:"works_total"`
	Limit         int                   `json:"limit"`
	Offset        int                   `json:"offset"`
}

func catalogueWindow(limit, offset int) (int, int) {
	if limit <= 0 {
		limit = 50
	}
	if limit > CataloguePageMax {
		limit = CataloguePageMax
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

// SQL casts the start of both decimal and range positions ("2.5", "1-3.5")
// numerically. The remaining columns give ties a stable, deterministic order.
const seriesListeningMembersSQL = seriesWorksSQL + ` ORDER BY CAST(sw.position AS REAL), sw.position, sw.work_id LIMIT ? OFFSET ?`
const seriesListeningTotalSQL = `SELECT COUNT(*) FROM series_works sw JOIN works w ON w.id=sw.work_id WHERE sw.series_id=?`

// SeriesListeningGuide resolves only a bounded member page, then batches its
// recording alternatives and narrator credits. It never builds WorkDetail.
func (s *DB) SeriesListeningGuide(ctx context.Context, id string, limit, offset int) (*SeriesListeningGuide, error) {
	limit, offset = catalogueWindow(limit, offset)
	d := SeriesListeningGuide{Limit: limit, Offset: offset, Entries: []ListeningGuideEntry{}}
	var lang, ordering, orderingOf sql.NullString
	err := s.db.QueryRowContext(ctx, seriesHeaderSQL(s.schemaVersion >= languagesSchemaVersion), id).
		Scan(&d.ID, &d.Name, &lang, &ordering, &orderingOf)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	d.Language, d.Ordering, d.OrderingOf = lang.String, ordering.String, orderingOf.String
	if d.TranslationOf, d.Translations, err = s.seriesTranslations(id); err != nil {
		return nil, err
	}
	if d.Orderings, err = s.orderingFamily(&SeriesDetail{ID: id, OrderingOf: d.OrderingOf}); err != nil {
		return nil, err
	}
	arows, err := s.db.QueryContext(ctx, seriesAuthorsSQL, id)
	if err != nil {
		return nil, err
	}
	if d.Authors, err = scanPersonRefs(arows); err != nil {
		return nil, err
	}
	if err = s.db.QueryRowContext(ctx, seriesListeningTotalSQL, id).Scan(&d.WorksTotal); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, seriesListeningMembersSQL, id, limit, offset)
	if err != nil {
		return nil, err
	}
	members, err := scanPairs(rows, func(id, pos string) seriesMember { return seriesMember{id, pos} })
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(members))
	for i, m := range members {
		ids[i] = m.workID
	}
	cards, err := s.cardsByID(ctx, ids)
	if err != nil {
		return nil, err
	}
	credits, totals, err := s.listeningCandidates(ctx, ids)
	if err != nil {
		return nil, err
	}
	summaries, err := s.recordingSummaries(ctx, credits)
	if err != nil {
		return nil, err
	}
	byWork := map[string][]RecordingSummary{}
	for _, c := range credits {
		byWork[c.workID] = append(byWork[c.workID], summaries[c])
	}
	for _, m := range members {
		recs := byWork[m.workID]
		if recs == nil {
			recs = []RecordingSummary{}
		}
		d.Entries = append(d.Entries, ListeningGuideEntry{m.position, cards[m.workID], totals[m.workID], recs, listeningRecordingsLimit})
	}
	return &d, nil
}

func listeningCandidatesSQL(ph string) string {
	return `SELECT work_id, id, total FROM (SELECT work_id, id, COUNT(*) OVER (PARTITION BY work_id) AS total, ` +
		`ROW_NUMBER() OVER (PARTITION BY work_id ORDER BY id) AS alternative FROM recordings WHERE work_id IN (` + ph +
		`)) WHERE alternative <= ? ORDER BY work_id, id`
}

func (s *DB) listeningCandidates(ctx context.Context, ids []string) ([]narratedCredit, map[string]int, error) {
	var out []narratedCredit
	totals := map[string]int{}
	err := eachChunk(ids, func(ph string, args []any) error {
		rows, err := s.db.QueryContext(ctx, listeningCandidatesSQL(ph), append(args, listeningRecordingsLimit)...)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var c narratedCredit
			var total int
			if err := rows.Scan(&c.workID, &c.recordingID, &total); err != nil {
				return err
			}
			out = append(out, c)
			totals[c.workID] = total
		}
		return rows.Err()
	})
	return out, totals, err
}

// selectedRecordingsSQL is a bounded in-memory relation of recording keys. Its
// joins read only those recordings, including only their own narrator credits.
func selectedRecordingsSQL(credits []narratedCredit) (string, []any) {
	values := make([]string, len(credits))
	args := make([]any, 0, 2*len(credits))
	for i, c := range credits {
		values[i] = `(?,?)`
		args = append(args, c.workID, c.recordingID)
	}
	return `WITH selected(work_id, recording_id) AS (VALUES ` + strings.Join(values, ",") + `) `, args
}

func recordingSummariesSQL(selected string, presence bool) string {
	return selected + `SELECT r.work_id, r.id, r.language, ` + abridgementColumn("r.", presence) +
		`, COALESCE(r.runtime_min,0), COALESCE(r.release_date,''), COALESCE(r.publisher,''), ` +
		`(SELECT COUNT(*) FROM chapters c WHERE c.work_id=r.work_id AND c.recording_id=r.id) ` +
		`FROM selected x JOIN recordings r ON r.work_id=x.work_id AND r.id=x.recording_id`
}

func recordingSummaryNarratorsSQL(selected string) string {
	return selected + `SELECT rn.work_id, rn.recording_id, p.id, p.name FROM selected x ` +
		`JOIN recording_narrators rn ON rn.work_id=x.work_id AND rn.recording_id=x.recording_id ` +
		`JOIN people p ON p.id=rn.person_id GROUP BY rn.work_id, rn.recording_id, p.id, p.name ` +
		`ORDER BY rn.work_id, rn.recording_id, MIN(rn.ord), p.id`
}

func (s *DB) recordingSummaries(ctx context.Context, credits []narratedCredit) (map[narratedCredit]RecordingSummary, error) {
	out := map[narratedCredit]RecordingSummary{}
	if len(credits) == 0 {
		return out, nil
	}
	selected, args := selectedRecordingsSQL(credits)
	rows, err := s.db.QueryContext(ctx, recordingSummariesSQL(selected, s.schemaVersion >= abridgementSchemaVersion), args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var workID string
		d := RecordingSummary{Narrators: []PersonRef{}}
		if err := rows.Scan(&workID, &d.ID, &d.Language, &d.Abridgement, &d.RuntimeMin, &d.ReleaseDate, &d.Publisher, &d.ChapterCount); err != nil {
			_ = rows.Close()
			return nil, err
		}
		out[narratedCredit{workID, d.ID}] = d
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	rows, err = s.db.QueryContext(ctx, recordingSummaryNarratorsSQL(selected), args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var c narratedCredit
		var person PersonRef
		if err := rows.Scan(&c.workID, &c.recordingID, &person.ID, &person.Name); err != nil {
			return nil, err
		}
		d := out[c]
		if d.ID == "" {
			continue
		}
		d.Narrators = append(d.Narrators, person)
		out[c] = d
	}
	return out, rows.Err()
}

type RecordingSeries struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Position string `json:"position"`
}

type PersonRecordingEntry struct {
	Work      *WorkCard         `json:"work"`
	Recording RecordingSummary  `json:"recording"`
	Series    []RecordingSeries `json:"series"`
}

type PersonRecordingsPage struct {
	Recordings []PersonRecordingEntry `json:"recordings"`
	Total      int                    `json:"total"`
	Limit      int                    `json:"limit"`
	Offset     int                    `json:"offset"`
}

func personRecordingCandidatesSQL(seriesID string, lang langFilter) (string, []any) {
	query := ` FROM recording_narrators rn JOIN recordings r ON r.work_id=rn.work_id AND r.id=rn.recording_id ` +
		`JOIN works w ON w.id=rn.work_id WHERE rn.person_id=?`
	var args []any
	if seriesID != "" {
		query += ` AND EXISTS (SELECT 1 FROM series_works sw WHERE sw.work_id=rn.work_id AND sw.series_id=?)`
		args = append(args, seriesID)
	}
	if len(lang) > 0 {
		pred, langArgs := lang.predicate("r.language", false)
		query += ` AND ` + pred
		args = append(args, langArgs...)
	}
	return query, args
}

// PersonRecordings lists narration credits, retaining separate recordings of
// one work and deduplicating repeated credits on the same recording.
func (s *DB) PersonRecordings(ctx context.Context, id string, limit, offset int, seriesID, language string) (*PersonRecordingsPage, error) {
	if seriesID != "" && !model.ValidSlug(seriesID) {
		return nil, fmt.Errorf("series: invalid series slug %q", seriesID)
	}
	lang, err := parseLangFilter(language)
	if err != nil {
		return nil, err
	}
	var person string
	err = s.db.QueryRowContext(ctx, `SELECT id FROM people WHERE id=?`, id).Scan(&person)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	limit, offset = catalogueWindow(limit, offset)
	d := PersonRecordingsPage{Recordings: []PersonRecordingEntry{}, Limit: limit, Offset: offset}
	base, filterArgs := personRecordingCandidatesSQL(seriesID, lang)
	args := append([]any{id}, filterArgs...)
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM (SELECT DISTINCT rn.work_id, rn.recording_id`+base+`)`, args...).Scan(&d.Total); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT rn.work_id, rn.recording_id`+base+` ORDER BY w.title, rn.work_id, rn.recording_id LIMIT ? OFFSET ?`, append(args, limit, offset)...)
	if err != nil {
		return nil, err
	}
	credits, err := scanPairs(rows, func(workID, rid string) narratedCredit { return narratedCredit{workID, rid} })
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(credits))
	for i, c := range credits {
		ids[i] = c.workID
	}
	ids = dedupeIDs(ids)
	cards, err := s.cardsByID(ctx, ids)
	if err != nil {
		return nil, err
	}
	summaries, err := s.recordingSummaries(ctx, credits)
	if err != nil {
		return nil, err
	}
	memberships := map[string][]RecordingSeries{}
	err = eachChunk(ids, func(ph string, args []any) error {
		rows, err := s.db.QueryContext(ctx, firstSeriesByWorkSQL(ph, s.hasOrderings()), args...)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var workID string
			var sr RecordingSeries
			var primary sql.NullString
			if err := rows.Scan(&workID, &sr.ID, &sr.Name, &sr.Position, &primary); err != nil {
				return err
			}
			memberships[workID] = append(memberships[workID], sr)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	for _, c := range credits {
		series := memberships[c.workID]
		if series == nil {
			series = []RecordingSeries{}
		}
		d.Recordings = append(d.Recordings, PersonRecordingEntry{cards[c.workID], summaries[c], series})
	}
	return &d, nil
}

type BrowseEntry struct{ ID, Title, Language string }
type BrowsePage struct {
	Entries              []BrowseEntry
	Total, Limit, Offset int
}

// Browse returns a bounded page of real catalogue links in stable id order.
// An unknown family is absent; an existing empty family has an empty page.
func (s *DB) Browse(ctx context.Context, family PageFamily, limit, offset int) (*BrowsePage, error) {
	f, ok := pageFamilies[family]
	if !ok {
		return nil, nil
	}
	limit, offset = catalogueWindow(limit, offset)
	d := BrowsePage{Entries: []BrowseEntry{}, Total: f.count(s), Limit: limit, Offset: offset}
	if d.Total == 0 {
		return &d, nil
	}
	rows, err := s.db.QueryContext(ctx, f.shardSQL(s), limit, offset)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		var added sql.NullString
		if err := rows.Scan(&id, &added); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	byID := map[string]BrowseEntry{}
	err = eachChunk(ids, func(ph string, args []any) error {
		query := browseEntriesSQL(family, ph)
		rows, err := s.db.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var e BrowseEntry
			if err := rows.Scan(&e.ID, &e.Title, &e.Language); err != nil {
				return err
			}
			byID[e.ID] = e
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		if entry, ok := byID[id]; ok {
			d.Entries = append(d.Entries, entry)
		}
	}
	return &d, nil
}

func browseEntriesSQL(family PageFamily, ph string) string {
	switch family {
	case PersonPages:
		return `SELECT id, name, '' FROM people WHERE id IN (` + ph + `)`
	case SeriesPages:
		return `SELECT id, name, '' FROM series WHERE id IN (` + ph + `)`
	default:
		return `SELECT id, title, language FROM works WHERE id IN (` + ph + `)`
	}
}

type RecordingChapterPage struct {
	Work                 *WorkCard
	Recording            RecordingSummary
	Chapters             []ChapterOut
	Total, Limit, Offset int
}

func pagedChaptersSQL(librivox bool) string { return chaptersOfSQL(librivox) + ` LIMIT ? OFFSET ?` }

// RecordingChapters validates the recording key before reading a bounded page
// of chapter bodies. A real recording without chapters remains distinguishable
// from a missing recording.
func (s *DB) RecordingChapters(ctx context.Context, workID, recordingID string, limit, offset int) (*RecordingChapterPage, error) {
	limit, offset = catalogueWindow(limit, offset)
	credit := narratedCredit{workID, recordingID}
	summaries, err := s.recordingSummaries(ctx, []narratedCredit{credit})
	if err != nil {
		return nil, err
	}
	recording, ok := summaries[credit]
	if !ok {
		return nil, nil
	}
	cards, err := s.cardsByID(ctx, []string{workID})
	if err != nil {
		return nil, err
	}
	if cards[workID] == nil {
		return nil, nil
	}
	d := RecordingChapterPage{Work: cards[workID], Recording: recording, Total: recording.ChapterCount, Limit: limit, Offset: offset, Chapters: []ChapterOut{}}
	librivox := s.hasLibrivoxLayer()
	rows, err := s.db.QueryContext(ctx, pagedChaptersSQL(librivox), workID, recordingID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var chapter ChapterOut
		var audio sql.NullString
		dst := []any{&chapter.Title, &chapter.StartMS, &chapter.LengthMS}
		if librivox {
			dst = append(dst, &audio)
		}
		if err := rows.Scan(dst...); err != nil {
			return nil, err
		}
		chapter.AudioURL = audio.String
		d.Chapters = append(d.Chapters, chapter)
	}
	return &d, rows.Err()
}
