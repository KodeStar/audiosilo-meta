package query

import (
	"cmp"
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kodestar/audiosilo-meta/internal/httpx"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// The LIBRIVOX-SHAPED FEED (LIBRIVOX-PLAN.md decision 6): GET
// /api/v1/librivox/audiobooks answers LibriVox's own audiobooks feed
// (librivox.org/api/feed/audiobooks) from the mirror, with LibriVox's parameters
// and LibriVox's JSON shape, so an app that reads LibriVox can move here by
// changing its base URL. The trailing-slash spelling LibriVox documents
// (".../audiobooks/?id=52") is served too.
//
// The parameters, as LibriVox's API applies them (its Librivox_API library):
//
//   - id: one project. since: a UNIX time; the projects whose catalogue date is
//     on or after it, compared as LibriVox does, at DAY granularity (a project
//     catalogued on a day counts from that day's midnight UTC). Our catalogue
//     date is the day the mirror imported the recording (its added_at), not the
//     day LibriVox catalogued it: a client syncing LibriVox's own new projects
//     reads LibriVox's since; ours answers "what reached the mirror since".
//   - title, author, genre: an exact, case-insensitive match, or with a leading
//     "^" a prefix match. title is the project's own title, LibriVox's repeat
//     markers ("(version N)", "(Dramatic Reading)") and spelling kept
//     (recording.librivox_title); author is an author's LAST name - the last
//     word of the name - and never a translator; genre is one of the project's
//     own LibriVox genre names (recording.librivox_genres), exactly as LibriVox
//     filters, never widened through the vocabulary mapping. LibriVox's own "^"
//     on author matches the END of the name (an inverted LIKE); here it is the
//     prefix it documents. Without a filled recording layer (librivoxFeedLayer:
//     no project title or genre names stored) title is the work's title and genre a LibriVox name the importer maps or a
//     vocabulary value, as before.
//   - extended=1 adds url_iarchive, sections, genres and translators;
//     coverart=1 adds coverart_jpg, coverart_pdf and coverart_thumbnail (null when
//     unstated); fields ({id,title}, a comma list, or fields[]=) keeps only the
//     named top-level fields; an empty or "0" flag is off, as in PHP.
//   - limit defaults to 50 and is capped at 500: over 500 is a 400, as LibriVox
//     answers. offset skips rows. sort_order=desc orders by project id
//     descending, anything else ascending.
//   - format: json only (or absent). Anything else is a 400: the XML, JSONP and
//     PHP forms are not served.
//
// What comes back differs from LibriVox's answer in the facts the mirror does
// not keep, by design (LICENSING.md, facts only): description is always "",
// url_project and url_other are "", coverart_pdf and coverart_thumbnail are null,
// a section's id is "", a section's language is the project's language, a
// genre's id is "", an author's first_name/last_name are the stored name split
// at its last word, and dob/dod are years or "" (LibriVox's "alive" is not a
// year). A genre LibriVox lists twice on one project is listed once. Everything
// else is LibriVox's value as the mirror stored it (the LIBRIVOX RECORDING
// layer, librivoxRecordingSchemaVersion): the title is the project's own
// (recording.librivox_title), a section's section_number LibriVox's own (from
// 0 on some projects), a section's readers its own (chapter readers, or the
// recording's one narrator on every section), the translators the project's
// own (the recording's credits, never the work's), the genres LibriVox's names
// (recording.librivox_genres) - and ids, the language name, copyright_year,
// URLs, runtimes, section titles, files and playtimes, author and translator ids
// and years. The golden test compares each one with the real LibriVox responses
// in the importer's fixture. A person's LibriVox id and years come from the
// person record, so a translator the importer resolved by name alone names none,
// and a person LibriVox lists under two ids states their record's own id
// (librivox_author_id, librivox_reader_id) on every project, also where
// LibriVox's answer states the second: it is an id LibriVox holds for that
// person, and the alias (librivox_author_aliases, librivox_reader_aliases) stays
// in the data, not the artifact.
//
// On an artifact older than librivoxRecordingSchemaVersion, or one that carries
// the layer EMPTY (a release cut before the backfill data landed:
// librivoxFeedLayer), the answer is what the mirror stored before: the work's
// title without its repeat markers, the 1-based position as section_number, the
// work's translators, the vocabulary genres, and readers only for a
// one-narrator recording.
//
// No results is LibriVox's 404 {"error": "Audiobooks could not be found"}, which
// is also every answer of an artifact older than librivoxSchemaVersion (it holds
// no LibriVox recording). Bulk readers belong on the meta.sqlite.gz release
// artifact, not on paging this feed.

// LibrivoxFeedPath is the feed's route, without its trailing-slash twin.
const LibrivoxFeedPath = "/api/v1/librivox/audiobooks"

// Feed paging: LibriVox's default and cap (its 2026-09 API update).
const (
	librivoxFeedDefault = 50
	LibrivoxFeedMax     = 500
)

// librivoxNotFound is LibriVox's own answer for no results.
const librivoxNotFound = "Audiobooks could not be found"

// librivoxArchiveDetails is the prefix LibriVox states every url_iarchive with;
// the mirror keeps only the item id (archive_id).
const librivoxArchiveDetails = "https://www.archive.org/details/"

// librivoxFeedParams is one request's parsed parameters.
type librivoxFeedParams struct {
	id       int
	since    int64
	hasSince bool
	title    textMatch
	author   textMatch
	genre    textMatch
	extended bool
	coverart bool
	fields   map[string]bool // nil = every field
	limit    int
	offset   int
	desc     bool
}

// textMatch is a title/author/genre parameter: its value, and whether a leading
// "^" asked for a prefix match.
type textMatch struct {
	value  string
	prefix bool
}

func parseTextMatch(raw string) textMatch {
	raw = strings.TrimSpace(raw)
	if rest, ok := strings.CutPrefix(raw, "^"); ok {
		return textMatch{value: strings.TrimSpace(rest), prefix: true}
	}
	return textMatch{value: raw}
}

// phpFlag reads a flag the way LibriVox's PHP does: empty() is false for "" and
// "0", true for anything else.
func phpFlag(raw string) bool { return raw != "" && raw != "0" }

// parseLibrivoxFeed reads the query string, or returns the 400's message.
func parseLibrivoxFeed(q url.Values) (librivoxFeedParams, error) {
	p := librivoxFeedParams{limit: librivoxFeedDefault}
	if f := strings.ToLower(strings.TrimSpace(q.Get("format"))); f != "" && f != "json" {
		return p, fmt.Errorf("format: only json is served here, got %q", q.Get("format"))
	}
	if raw := strings.TrimSpace(q.Get("limit")); raw != "" && raw != "0" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			return p, fmt.Errorf("limit: %q is not a number of records", raw)
		}
		if n > LibrivoxFeedMax {
			return p, fmt.Errorf("too many records requested: limit is at most %d (for the whole catalogue, use the meta.sqlite.gz release artifact)", LibrivoxFeedMax)
		}
		p.limit = n
	}
	if raw := strings.TrimSpace(q.Get("offset")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			return p, fmt.Errorf("offset: %q is not a row offset", raw)
		}
		p.offset = n
	}
	if raw := strings.TrimSpace(q.Get("id")); raw != "" {
		id, err := parseLibrivoxID(raw)
		if err != nil {
			return p, fmt.Errorf("id: %q is not a LibriVox project id", raw)
		}
		p.id = id
	}
	if raw := strings.TrimSpace(q.Get("since")); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return p, fmt.Errorf("since: %q is not a UNIX time", raw)
		}
		p.since, p.hasSince = n, true
	}
	p.title = parseTextMatch(q.Get("title"))
	p.author = parseTextMatch(q.Get("author"))
	p.genre = parseTextMatch(q.Get("genre"))
	p.extended = phpFlag(strings.TrimSpace(q.Get("extended")))
	p.coverart = phpFlag(strings.TrimSpace(q.Get("coverart")))
	p.desc = strings.TrimSpace(q.Get("sort_order")) == "desc"
	for _, v := range append(q["fields"], q["fields[]"]...) {
		for _, f := range strings.Split(strings.Trim(strings.TrimSpace(v), "{}"), ",") {
			if f = strings.TrimSpace(f); f != "" {
				if p.fields == nil {
					p.fields = map[string]bool{}
				}
				p.fields[f] = true
			}
		}
	}
	return p, nil
}

func (s *handler) handleLibrivoxFeed(w http.ResponseWriter, r *http.Request, snap *DB) {
	p, err := parseLibrivoxFeed(r.URL.Query())
	if err != nil {
		httpx.WriteErr(w, http.StatusBadRequest, err.Error())
		return
	}
	books, err := snap.librivoxFeed(r.Context(), p)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if len(books) == 0 {
		httpx.WriteErr(w, http.StatusNotFound, librivoxNotFound)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"books": books})
}

// likeEscape escapes LIKE's wildcards in a literal, for `LIKE ? ESCAPE '\'`.
func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// lastNameSQL is the last word of a person's name in SQL: rtrim strips back to
// the last space (every character the name holds that is not a space), and the
// word is what follows. A one-word name ("Voltaire") is the whole name.
const lastNameSQL = `substr(p.name, length(rtrim(p.name, replace(p.name, ' ', ''))) + 1)`

// librivoxFeedSelect is the feed page's row: the recording, its title (the
// project's own, else its work's: lvTitleExpr), its LibriVox columns, and
// whether the recording pass has restated it (lvRestatedExpr). Every page reads
// idx_recordings_librivox in id order.
func librivoxFeedSelect(recordingLayer bool) string {
	return `SELECT r.work_id, r.id, r.librivox_id, ` + lvTitleExpr(recordingLayer) + `, r.language, r.copyright_year, ` +
		`r.link_librivox, r.link_zip, r.link_rss, r.link_text, r.archive_id, r.cover_url, r.runtime_min, ` +
		lvRestatedExpr(recordingLayer) + ` ` +
		`FROM recordings r JOIN works w ON w.id = r.work_id WHERE r.librivox_id IS NOT NULL`
}

// lvRestatedExpr is whether a feed row's recording was restated by the import's
// recording pass: every restated LibriVox recording carries its project's
// librivox_title, so a NULL one is a recording the pass has not reached yet
// (an artifact cut while the backfill is only part-landed). Such a recording
// answers genres and translators as version 8 did - its work's - instead of
// the empty lists its absent rows would otherwise read as. Never on an older
// artifact.
func lvRestatedExpr(recordingLayer bool) string {
	if recordingLayer {
		return `(r.librivox_title IS NOT NULL)`
	}
	return `0`
}

// lvTitleExpr is a feed row's title: the project's own (librivox_title, from
// librivoxRecordingSchemaVersion), else the work's.
func lvTitleExpr(recordingLayer bool) string {
	if recordingLayer {
		return `coalesce(r.librivox_title, w.title)`
	}
	return `w.title`
}

// librivoxFeedQuery builds the page query for p: the filters as SQL arms, the
// project-id order and the window. Every filter is an arm evaluated per LibriVox
// recording as idx_recordings_librivox walks them in id order - the author and
// genre ones as EXISTS probes through each work's own index - so a filtered page
// costs at most one walk of the LibriVox range, never a scan of the people or
// genre tables.
//
// recordingLayer is whether the artifact carries the LIBRIVOX RECORDING layer
// (librivoxRecordingSchemaVersion): title and genre then match the project's own
// title and genre names.
func librivoxFeedQuery(p librivoxFeedParams, recordingLayer bool) (string, []any) {
	query, args := librivoxFeedSelect(recordingLayer), []any{}
	if p.id != 0 {
		query += ` AND r.librivox_id = ?`
		args = append(args, p.id)
	}
	if p.hasSince {
		// The day a recording was added on or after the first day that starts at
		// or after since: a plain comparison against its YYYY-MM-DD prefix.
		query += ` AND r.added_at >= ?`
		args = append(args, sinceDay(p.since))
	}
	if p.title.value != "" {
		match, arg := textMatchSQL(lvTitleExpr(recordingLayer), p.title)
		query += ` AND ` + match
		args = append(args, arg)
	}
	if p.author.value != "" {
		// An author, never a translator: the work's authors list carries its
		// translators too (they are credited AND identity authors), and LibriVox's
		// author parameter reads its authors alone.
		match, arg := textMatchSQL(lastNameSQL, p.author)
		query += ` AND EXISTS (SELECT 1 FROM work_authors wa JOIN people p ON p.id = wa.person_id WHERE wa.work_id = r.work_id AND ` + match +
			` AND ` + notTranslatorArm + `)`
		args = append(args, arg)
	}
	if p.genre.value != "" {
		vocab, vocabArgs := vocabularyGenreArm(p.genre)
		if recordingLayer {
			// A restated recording matches the project's own genre names, exactly
			// as LibriVox filters them, never widened through the vocabulary
			// mapping (meta#2761); one the pass has not reached matches its work's
			// genres as version 8 did (lvRestatedExpr).
			match, arg := textMatchSQL(`g.genre`, p.genre)
			query += ` AND ((r.librivox_title IS NOT NULL AND EXISTS (SELECT 1 FROM recording_librivox_genres g WHERE g.work_id = r.work_id AND g.recording_id = r.id AND ` + match + `))` +
				` OR (r.librivox_title IS NULL AND ` + vocab + `))`
			args = append(append(args, arg), vocabArgs...)
		} else {
			query += ` AND ` + vocab
			args = append(args, vocabArgs...)
		}
	}
	order := ` ORDER BY r.librivox_id ASC`
	if p.desc {
		order = ` ORDER BY r.librivox_id DESC`
	}
	return query + order + ` LIMIT ? OFFSET ?`, append(args, p.limit, p.offset)
}

// vocabularyGenreArm is the version 8 genre filter over a recording's work:
// the vocabulary values a genre parameter names (librivoxGenreSlugs).
func vocabularyGenreArm(m textMatch) (string, []any) {
	slugs, like := librivoxGenreSlugs(m)
	var arms []string
	var args []any
	if len(slugs) > 0 {
		arms = append(arms, `genre IN (`+strings.TrimSuffix(strings.Repeat("?,", len(slugs)), ",")+`)`)
		for _, g := range slugs {
			args = append(args, g)
		}
	}
	if like != "" {
		arms = append(arms, `genre LIKE ? ESCAPE '\'`)
		args = append(args, like)
	}
	return `EXISTS (SELECT 1 FROM work_genres g WHERE g.work_id = r.work_id AND (` + strings.Join(arms, " OR ") + `))`, args
}

// textMatchSQL is the predicate and argument a title/author/genre parameter m
// adds over expr: an exact, case-insensitive match, or with m.prefix a LIKE
// prefix (its wildcards escaped).
func textMatchSQL(expr string, m textMatch) (string, any) {
	if m.prefix {
		return expr + ` LIKE ? ESCAPE '\'`, likeEscape(m.value) + "%"
	}
	return expr + ` = ? COLLATE NOCASE`, m.value
}

// librivoxGenres is model.LibrivoxGenres, copied once.
var librivoxGenres = model.LibrivoxGenres()

// librivoxGenreSlugs resolves a genre parameter to the vocabulary values it
// names, on an artifact older than librivoxRecordingSchemaVersion (one with no
// LibriVox genre names stored): a LibriVox genre name through the importer's own
// table (model.LibrivoxGenres) or a vocabulary value as itself,
// case-insensitively.
// A prefix match takes every LibriVox name that starts with the value, plus a
// LIKE pattern over the vocabulary values themselves.
func librivoxGenreSlugs(m textMatch) (slugs []string, like string) {
	v := strings.ToLower(m.value)
	set := map[string]bool{}
	for name, slug := range librivoxGenres {
		if name == v || (m.prefix && strings.HasPrefix(name, v)) {
			set[slug] = true
		}
	}
	if m.prefix {
		like = likeEscape(v) + "%"
	} else {
		set[v] = true
	}
	for g := range set {
		slugs = append(slugs, g)
	}
	sort.Strings(slugs)
	return slugs, like
}

// lvRow is one feed page row: the recording's key and the facts the page query
// read.
type lvRow struct {
	workID, recID                  string
	librivoxID                     int
	title, language                string
	copyrightYear                  int
	linkLibrivox, linkZip, linkRSS string
	linkText, archiveID, coverURL  string
	runtimeMin                     int
	// restated is whether the recording pass reached this recording
	// (lvRestatedExpr): its genres and translators are then its own.
	restated bool
}

// lvPerson is an author or translator as the feed names one.
type lvPerson struct {
	name                 string
	librivoxID           int
	birthYear, deathYear int
}

// lvChapter is one section as the feed names one: number is LibriVox's
// section_number (the stated one, else the 1-based position).
type lvChapter struct {
	title    string
	lengthMS int64
	audioURL string
	number   int
}

// lvReader is one recording narrator as the feed names a reader.
type lvReader struct {
	name     string
	readerID int
}

// librivoxFeed answers one feed request: the page of LibriVox recordings p
// selects, each composed into LibriVox's book shape. Every per-book read is
// batched over the page's works (librivoxFeedFacts), so a 500-book page is a
// fixed number of queries.
func (s *DB) librivoxFeed(ctx context.Context, p librivoxFeedParams) ([]map[string]any, error) {
	if !s.hasLibrivoxLayer() {
		return nil, nil
	}
	recordingLayer := s.librivoxFeedLayer()
	query, args := librivoxFeedQuery(p, recordingLayer)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	page, err := scanLibrivoxRows(rows)
	if err != nil || len(page) == 0 {
		return nil, err
	}
	workIDs := make([]string, 0, len(page))
	var fallback []string // the works of the rows the recording pass has not reached
	for _, r := range page {
		workIDs = append(workIDs, r.workID)
		if !r.restated {
			fallback = append(fallback, r.workID)
		}
	}
	facts, err := s.librivoxFeedFacts(ctx, workIDs, fallback, p.extended, recordingLayer)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(page))
	for _, r := range page {
		out = append(out, librivoxBook(r, facts, p))
	}
	return out, nil
}

func scanLibrivoxRows(rows *sql.Rows) ([]lvRow, error) {
	defer func() { _ = rows.Close() }()
	var out []lvRow
	for rows.Next() {
		var r lvRow
		var cy, runtime sql.NullInt64
		var lvLink, zip, rss, text, archive, cover sql.NullString
		if err := rows.Scan(&r.workID, &r.recID, &r.librivoxID, &r.title, &r.language, &cy,
			&lvLink, &zip, &rss, &text, &archive, &cover, &runtime, &r.restated); err != nil {
			return nil, err
		}
		r.copyrightYear, r.runtimeMin = int(cy.Int64), int(runtime.Int64)
		r.linkLibrivox, r.linkZip, r.linkRSS, r.linkText = lvLink.String, zip.String, rss.String, text.String
		r.archiveID, r.coverURL = archive.String, cover.String
		out = append(out, r)
	}
	return out, rows.Err()
}

// recKey is a recording's key in the artifact.
type recKey struct{ work, rec string }

// lvFacts is what a feed page reads per work and per recording, batched. On an
// artifact carrying the LIBRIVOX RECORDING layer the translators, genres and
// section readers are the recording's own; on an older one the translators and
// genres are its work's (the vocabulary's), and there are no section readers.
type lvFacts struct {
	authors     map[string][]lvPerson // by work: authors that are not translators
	translators map[recKey][]lvPerson // the recording's own (the recording layer)
	genres      map[recKey][]string
	// workTranslators and workGenres are the version 8 answer - the work's
	// translator credits and vocabulary genres - for a row the recording pass
	// has not reached (lvRow.restated false).
	workTranslators map[recKey][]lvPerson
	workGenres      map[recKey][]string
	chapters        map[recKey][]lvChapter
	readers         map[recKey][]lvReader         // the recording's narrators
	sectionReaders  map[recKey]map[int][]lvReader // by chapter idx
}

// notTranslatorArm keeps a work_authors row (wa) whose person the work does not
// also credit as its translator: the authors list carries a translator too (an
// identity author AND a credit), and LibriVox's authors are its authors alone.
const notTranslatorArm = `NOT EXISTS (SELECT 1 FROM work_credits wc WHERE wc.work_id = wa.work_id AND wc.person_id = wa.person_id AND wc.role = '` + model.RoleTranslator + `')`

// The feed's batched reads, each over a page's work ids (eachChunk). A work's
// people come in their record order; chapters in timeline order.
func lvAuthorsSQL(ph string) string {
	return `SELECT wa.work_id, p.name, p.librivox_author_id, p.birth_year, p.death_year FROM work_authors wa ` +
		`JOIN people p ON p.id = wa.person_id WHERE wa.work_id IN (` + ph + `) AND ` + notTranslatorArm + ` ORDER BY wa.work_id, wa.ord`
}

// The chapters and readers are read for the page's LIBRIVOX recordings only,
// driven from the works' recordings so no other recording's rows are read: a
// public-domain classic often carries a dozen retail recordings beside its
// LibriVox one, each with its own chapter list, and none of them is on the page.
// CROSS JOIN is SQLite's join-order pin: left to its own plan (no sqlite_stat1)
// the planner drives from chapters through the transitive work_id constraint,
// reading every recording's chapters and filtering after.
func lvChaptersSQL(recordingLayer bool) func(string) string {
	number := `c.idx + 1`
	if recordingLayer {
		number = `coalesce(c.section_number, c.idx + 1)`
	}
	return func(ph string) string {
		return `SELECT r.work_id, r.id, c.title, c.length_ms, c.audio_url, ` + number + ` FROM recordings r ` +
			`CROSS JOIN chapters c ON c.work_id = r.work_id AND c.recording_id = r.id ` +
			`WHERE r.work_id IN (` + ph + `) AND r.librivox_id IS NOT NULL ORDER BY r.work_id, r.id, c.idx`
	}
}

func lvReadersSQL(ph string) string {
	return `SELECT r.work_id, r.id, p.name, p.librivox_reader_id FROM recordings r ` +
		`CROSS JOIN recording_narrators rn ON rn.work_id = r.work_id AND rn.recording_id = r.id ` +
		`JOIN people p ON p.id = rn.person_id WHERE r.work_id IN (` + ph + `) AND r.librivox_id IS NOT NULL ORDER BY r.work_id, r.id, rn.ord`
}

// The translators and genres, per LibriVox recording of the page, driven from
// the recordings as lvChaptersSQL is. With the LIBRIVOX RECORDING layer
// (librivoxRecordingSchemaVersion) they are the recording's own credits and
// LibriVox genre names, in the record's order; on an older artifact its work's
// translator credits and vocabulary genres.
func lvTranslatorsSQL(recordingLayer bool) func(string) string {
	join := `CROSS JOIN work_credits rc ON rc.work_id = r.work_id `
	if recordingLayer {
		join = `CROSS JOIN recording_credits rc ON rc.work_id = r.work_id AND rc.recording_id = r.id `
	}
	return func(ph string) string {
		return `SELECT r.work_id, r.id, p.name, p.librivox_author_id, p.birth_year, p.death_year FROM recordings r ` + join +
			`JOIN people p ON p.id = rc.person_id WHERE r.work_id IN (` + ph + `) AND r.librivox_id IS NOT NULL ` +
			`AND rc.role = '` + model.RoleTranslator + `' ORDER BY r.work_id, r.id, rc.ord`
	}
}

func lvGenresSQL(recordingLayer bool) func(string) string {
	join, order := `CROSS JOIN work_genres g ON g.work_id = r.work_id `, `g.genre`
	if recordingLayer {
		join, order = `CROSS JOIN recording_librivox_genres g ON g.work_id = r.work_id AND g.recording_id = r.id `, `g.ord`
	}
	return func(ph string) string {
		return `SELECT r.work_id, r.id, g.genre FROM recordings r ` + join +
			`WHERE r.work_id IN (` + ph + `) AND r.librivox_id IS NOT NULL ORDER BY r.work_id, r.id, ` + order
	}
}

// lvSectionReadersSQL is each section's readers (the recording layer only).
func lvSectionReadersSQL(ph string) string {
	return `SELECT r.work_id, r.id, cr.chapter_idx, p.name, p.librivox_reader_id FROM recordings r ` +
		`CROSS JOIN chapter_readers cr ON cr.work_id = r.work_id AND cr.recording_id = r.id ` +
		`JOIN people p ON p.id = cr.person_id WHERE r.work_id IN (` + ph + `) AND r.librivox_id IS NOT NULL ` +
		`ORDER BY r.work_id, r.id, cr.chapter_idx, cr.ord`
}

// librivoxFeedFacts reads the page's per-work and per-recording facts. The
// translators, genres and readers appear only on an extended page, so a plain
// one does not read them. recordingLayer reads the recording's own (lvFacts).
//
// fallback is the works of the page's rows the recording pass has not reached
// (lvRow.restated false; every row on an older artifact): their genres and
// translators are read as version 8 did, the work's, into workGenres and
// workTranslators.
func (s *DB) librivoxFeedFacts(ctx context.Context, workIDs, fallback []string, extended, recordingLayer bool) (*lvFacts, error) {
	f := &lvFacts{
		authors: map[string][]lvPerson{}, translators: map[recKey][]lvPerson{}, genres: map[recKey][]string{},
		workTranslators: map[recKey][]lvPerson{}, workGenres: map[recKey][]string{},
		chapters: map[recKey][]lvChapter{}, readers: map[recKey][]lvReader{},
		sectionReaders: map[recKey]map[int][]lvReader{},
	}
	// over runs one batched read over ids, handing every row to scan; each is
	// over the page's works.
	over := func(ids []string, sqlOf func(string) string, scan func(*sql.Rows) error) error {
		return eachChunk(ids, func(ph string, args []any) error {
			rows, err := s.db.QueryContext(ctx, sqlOf(ph), args...)
			if err != nil {
				return err
			}
			defer func() { _ = rows.Close() }()
			for rows.Next() {
				if err := scan(rows); err != nil {
					return err
				}
			}
			return rows.Err()
		})
	}
	each := func(sqlOf func(string) string, scan func(*sql.Rows) error) error {
		return over(workIDs, sqlOf, scan)
	}
	if err := each(lvAuthorsSQL, func(rows *sql.Rows) error {
		var work string
		p, err := scanLvPerson(rows, &work)
		if err == nil {
			f.authors[work] = append(f.authors[work], p)
		}
		return err
	}); err != nil {
		return nil, err
	}
	if err := each(lvChaptersSQL(recordingLayer), func(rows *sql.Rows) error {
		var k recKey
		var c lvChapter
		var audio sql.NullString
		if err := rows.Scan(&k.work, &k.rec, &c.title, &c.lengthMS, &audio, &c.number); err != nil {
			return err
		}
		c.audioURL = audio.String
		f.chapters[k] = append(f.chapters[k], c)
		return nil
	}); err != nil {
		return nil, err
	}
	if !extended {
		return f, nil
	}
	// The translators and genres: the recordings' own (the recording layer),
	// and the works' for the rows the pass has not reached.
	credits := func(ids []string, recordingLevel bool, translators map[recKey][]lvPerson, genres map[recKey][]string) error {
		if err := over(ids, lvTranslatorsSQL(recordingLevel), func(rows *sql.Rows) error {
			var k recKey
			p, err := scanLvPerson(rows, &k.work, &k.rec)
			if err == nil {
				translators[k] = append(translators[k], p)
			}
			return err
		}); err != nil {
			return err
		}
		return over(ids, lvGenresSQL(recordingLevel), func(rows *sql.Rows) error {
			var k recKey
			var genre string
			if err := rows.Scan(&k.work, &k.rec, &genre); err != nil {
				return err
			}
			genres[k] = append(genres[k], genre)
			return nil
		})
	}
	if recordingLayer {
		if err := credits(workIDs, true, f.translators, f.genres); err != nil {
			return nil, err
		}
	}
	if len(fallback) > 0 {
		if err := credits(fallback, false, f.workTranslators, f.workGenres); err != nil {
			return nil, err
		}
	}
	if err := each(lvReadersSQL, func(rows *sql.Rows) error {
		var k recKey
		rd, err := scanLvReader(rows, &k.work, &k.rec)
		if err == nil {
			f.readers[k] = append(f.readers[k], rd)
		}
		return err
	}); err != nil {
		return nil, err
	}
	if !recordingLayer {
		return f, nil
	}
	if err := each(lvSectionReadersSQL, func(rows *sql.Rows) error {
		var k recKey
		var idx int
		rd, err := scanLvReader(rows, &k.work, &k.rec, &idx)
		if err != nil {
			return err
		}
		if f.sectionReaders[k] == nil {
			f.sectionReaders[k] = map[int][]lvReader{}
		}
		f.sectionReaders[k][idx] = append(f.sectionReaders[k][idx], rd)
		return nil
	}); err != nil {
		return nil, err
	}
	return f, nil
}

// scanLvPerson scans one people row: the key columns into key, then the name,
// LibriVox author id and life years.
func scanLvPerson(rows *sql.Rows, key ...any) (lvPerson, error) {
	var p lvPerson
	var lvID, birth, death sql.NullInt64
	err := rows.Scan(append(key, &p.name, &lvID, &birth, &death)...)
	p.librivoxID, p.birthYear, p.deathYear = int(lvID.Int64), int(birth.Int64), int(death.Int64)
	return p, err
}

// scanLvReader scans one reader row: the key columns into key, then the name
// and LibriVox reader id.
func scanLvReader(rows *sql.Rows, key ...any) (lvReader, error) {
	var rd lvReader
	var id sql.NullInt64
	err := rows.Scan(append(key, &rd.name, &id)...)
	rd.readerID = int(id.Int64)
	return rd, err
}

// librivoxBook composes one row into LibriVox's book shape, then keeps only the
// fields p names (when it names any).
func librivoxBook(r lvRow, f *lvFacts, p librivoxFeedParams) map[string]any {
	key := recKey{r.workID, r.recID}
	chapters := f.chapters[key]
	secs := 0
	if len(chapters) > 0 {
		var ms int64
		for _, c := range chapters {
			ms += c.lengthMS
		}
		secs = int(ms / 1000)
	} else {
		secs = r.runtimeMin * 60
	}
	language := cmp.Or(model.LibrivoxLanguageName(r.language), r.language)
	book := map[string]any{
		"id":              strconv.Itoa(r.librivoxID),
		"title":           r.title,
		"description":     "",
		"url_text_source": r.linkText,
		"language":        language,
		"copyright_year":  intOr(r.copyrightYear, "0"),
		"num_sections":    strconv.Itoa(len(chapters)),
		"url_rss":         r.linkRSS,
		"url_zip_file":    r.linkZip,
		"url_project":     "",
		"url_librivox":    r.linkLibrivox,
		"url_other":       "",
		"totaltime":       clockTime(secs),
		"totaltimesecs":   secs,
		"authors":         librivoxPeople(f.authors[r.workID]),
	}
	if p.coverart {
		book["coverart_jpg"] = nullIfEmpty(r.coverURL)
		book["coverart_pdf"] = nil
		book["coverart_thumbnail"] = nil
	}
	if p.extended {
		iarchive := ""
		if r.archiveID != "" {
			iarchive = librivoxArchiveDetails + r.archiveID
		}
		book["url_iarchive"] = iarchive
		book["sections"] = librivoxSections(chapters, f.readers[key], f.sectionReaders[key], language)
		names, translators := f.genres[key], f.translators[key]
		if !r.restated {
			names, translators = f.workGenres[key], f.workTranslators[key]
		}
		genres := make([]map[string]string, 0, len(names))
		for _, g := range names {
			genres = append(genres, map[string]string{"id": "", "name": g})
		}
		book["genres"] = genres
		book["translators"] = librivoxPeople(translators)
	}
	if p.fields != nil {
		for k := range book {
			if !p.fields[k] {
				delete(book, k)
			}
		}
	}
	return book
}

// librivoxPeople renders authors or translators as LibriVox's person objects.
func librivoxPeople(ps []lvPerson) []map[string]string {
	out := make([]map[string]string, 0, len(ps))
	for _, p := range ps {
		first, last := splitLastWord(p.name)
		out = append(out, map[string]string{
			"id": intOr(p.librivoxID, ""), "first_name": first, "last_name": last,
			"dob": intOr(p.birthYear, ""), "dod": intOr(p.deathYear, ""),
		})
	}
	return out
}

// librivoxSections renders chapters as LibriVox sections. A section's readers
// are its own (bySection, the chapter readers, keyed by chapter index) when the
// recording states any; else the recording's narrator on every section when it
// has exactly one, and none when it has several (the readers are unknown).
func librivoxSections(chapters []lvChapter, narrators []lvReader, bySection map[int][]lvReader, language string) []map[string]any {
	render := func(rs []lvReader) []map[string]string {
		out := make([]map[string]string, 0, len(rs))
		for _, rd := range rs {
			out = append(out, map[string]string{"reader_id": intOr(rd.readerID, ""), "display_name": rd.name})
		}
		return out
	}
	solo := render(nil)
	if len(narrators) == 1 {
		solo = render(narrators)
	}
	out := make([]map[string]any, 0, len(chapters))
	for i, c := range chapters {
		readers := solo
		if bySection != nil {
			readers = render(bySection[i])
		}
		out = append(out, map[string]any{
			"id":             "",
			"section_number": strconv.Itoa(c.number),
			"title":          c.title,
			"listen_url":     c.audioURL,
			"language":       language,
			"playtime":       strconv.FormatInt(c.lengthMS/1000, 10),
			"file_name":      nil,
			"readers":        readers,
		})
	}
	return out
}

// splitLastWord splits a stored name into LibriVox's first_name and last_name at
// its last word: "Franz Kafka" -> "Franz", "Kafka"; "Voltaire" -> "", "Voltaire".
func splitLastWord(name string) (first, last string) {
	name = strings.TrimSpace(name)
	i := strings.LastIndexByte(name, ' ')
	if i < 0 {
		return "", name
	}
	return strings.TrimSpace(name[:i]), name[i+1:]
}

// intOr is a number as LibriVox spells one (a signed year, an id), or none when
// unstated (zero).
func intOr(y int, none string) string {
	if y == 0 {
		return none
	}
	return strconv.Itoa(y)
}

// sinceDay is the first calendar day (UTC, YYYY-MM-DD) whose midnight is at or
// after the UNIX time since: a record added on that day or later counts, which
// is LibriVox's day-granular comparison. Out-of-range times clamp to a day
// before or after every stored date.
func sinceDay(since int64) string {
	const day = 24 * 60 * 60
	if since > 253402300799 { // past 9999-12-31T23:59:59Z, and clear of overflow
		return "9999-99-99"
	}
	d := since / day
	if since%day > 0 {
		d++
	}
	t := time.Unix(d*day, 0).UTC()
	switch {
	case t.Year() < 1:
		return "0000-00-00"
	case t.Year() > 9999:
		return "9999-99-99"
	}
	return t.Format(time.DateOnly)
}

// clockTime renders seconds as LibriVox's totaltime, H:MM:SS.
func clockTime(secs int) string {
	return fmt.Sprintf("%d:%02d:%02d", secs/3600, secs/60%60, secs%60)
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
