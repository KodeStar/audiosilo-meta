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
//     "^" a prefix match. title is the work's title (LibriVox's "(version N)"
//     marker is not stored: it numbers projects, not works); author is an
//     author's LAST name - the last word of the name - and never a translator;
//     genre is a LibriVox genre name the importer maps (the same table) or a
//     genre of this project's vocabulary. LibriVox's own "^" on author matches the
//     END of the name (an inverted LIKE); here it is the prefix it documents.
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
// a section's id is "" and its section_number is its 1-based position, genres are
// this project's vocabulary (never LibriVox's taxonomy verbatim) with an empty id,
// an author's first_name/last_name are the stored name split at its last word,
// and dob/dod are years or "" (LibriVox's "alive" is not a year). Per-section
// readers are not stored: a recording with ONE narrator names that reader on
// every section, a multi-reader recording names none. Everything else - ids,
// titles, the language name, copyright_year, URLs, runtimes, section titles,
// files and playtimes, author and translator ids and years - is LibriVox's
// value as the mirror stored it, and the golden test compares each one with the
// real LibriVox responses in the importer's fixture.
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

// librivoxFeedSelect is the feed page's row: the recording, its work's title,
// its LibriVox columns. Every page reads idx_recordings_librivox in id order.
const librivoxFeedSelect = `SELECT r.work_id, r.id, r.librivox_id, w.title, r.language, r.copyright_year, ` +
	`r.link_librivox, r.link_zip, r.link_rss, r.link_text, r.archive_id, r.cover_url, r.runtime_min ` +
	`FROM recordings r JOIN works w ON w.id = r.work_id WHERE r.librivox_id IS NOT NULL`

// librivoxFeedQuery builds the page query for p: the filters as SQL arms, the
// project-id order and the window. Every filter is an arm evaluated per LibriVox
// recording as idx_recordings_librivox walks them in id order - the author and
// genre ones as EXISTS probes through each work's own index - so a filtered page
// costs at most one walk of the LibriVox range, never a scan of the people or
// genre tables.
func librivoxFeedQuery(p librivoxFeedParams) (string, []any) {
	query, args := librivoxFeedSelect, []any{}
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
		if p.title.prefix {
			query += ` AND w.title LIKE ? ESCAPE '\'`
			args = append(args, likeEscape(p.title.value)+"%")
		} else {
			query += ` AND w.title = ? COLLATE NOCASE`
			args = append(args, p.title.value)
		}
	}
	if p.author.value != "" {
		// An author, never a translator: the work's authors list carries its
		// translators too (they are credited AND identity authors), and LibriVox's
		// author parameter reads its authors alone.
		match, arg := lastNameSQL+` = ? COLLATE NOCASE`, any(p.author.value)
		if p.author.prefix {
			match, arg = lastNameSQL+` LIKE ? ESCAPE '\'`, likeEscape(p.author.value)+"%"
		}
		query += ` AND EXISTS (SELECT 1 FROM work_authors wa JOIN people p ON p.id = wa.person_id WHERE wa.work_id = r.work_id AND ` + match +
			` AND ` + notTranslatorArm + `)`
		args = append(args, arg)
	}
	if p.genre.value != "" {
		slugs, like := librivoxGenreSlugs(p.genre)
		arms := []string{}
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
		query += ` AND EXISTS (SELECT 1 FROM work_genres g WHERE g.work_id = r.work_id AND (` + strings.Join(arms, " OR ") + `))`
	}
	order := ` ORDER BY r.librivox_id ASC`
	if p.desc {
		order = ` ORDER BY r.librivox_id DESC`
	}
	return query + order + ` LIMIT ? OFFSET ?`, append(args, p.limit, p.offset)
}

// librivoxGenres is model.LibrivoxGenres, copied once.
var librivoxGenres = model.LibrivoxGenres()

// librivoxGenreSlugs resolves a genre parameter to the vocabulary values it
// names: a LibriVox genre name through the importer's own table
// (model.LibrivoxGenres) or a vocabulary value as itself, case-insensitively.
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
}

// lvPerson is an author or translator as the feed names one.
type lvPerson struct {
	name                 string
	librivoxID           int
	birthYear, deathYear int
}

// lvChapter is one section as the feed names one.
type lvChapter struct {
	title    string
	lengthMS int64
	audioURL string
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
	query, args := librivoxFeedQuery(p)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	page, err := scanLibrivoxRows(rows)
	if err != nil || len(page) == 0 {
		return nil, err
	}
	workIDs := make([]string, 0, len(page))
	for _, r := range page {
		workIDs = append(workIDs, r.workID)
	}
	facts, err := s.librivoxFeedFacts(ctx, workIDs, p.extended)
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
			&lvLink, &zip, &rss, &text, &archive, &cover, &runtime); err != nil {
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

// lvFacts is what a feed page reads per work and per recording, batched.
type lvFacts struct {
	authors     map[string][]lvPerson // by work: authors that are not translators
	translators map[string][]lvPerson // by work
	genres      map[string][]string   // by work
	chapters    map[recKey][]lvChapter
	readers     map[recKey][]lvReader
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

func lvTranslatorsSQL(ph string) string {
	return `SELECT wc.work_id, p.name, p.librivox_author_id, p.birth_year, p.death_year FROM work_credits wc ` +
		`JOIN people p ON p.id = wc.person_id WHERE wc.role = '` + model.RoleTranslator + `' AND wc.work_id IN (` + ph + `) ORDER BY wc.work_id, wc.ord`
}

func lvGenresSQL(ph string) string {
	return `SELECT work_id, genre FROM work_genres WHERE work_id IN (` + ph + `) ORDER BY work_id, genre`
}

// The chapters and readers are read for the page's LIBRIVOX recordings only,
// driven from the works' recordings so no other recording's rows are read: a
// public-domain classic often carries a dozen retail recordings beside its
// LibriVox one, each with its own chapter list, and none of them is on the page.
// CROSS JOIN is SQLite's join-order pin: left to its own plan (no sqlite_stat1)
// the planner drives from chapters through the transitive work_id constraint,
// reading every recording's chapters and filtering after.
func lvChaptersSQL(ph string) string {
	return `SELECT r.work_id, r.id, c.title, c.length_ms, c.audio_url FROM recordings r ` +
		`CROSS JOIN chapters c ON c.work_id = r.work_id AND c.recording_id = r.id ` +
		`WHERE r.work_id IN (` + ph + `) AND r.librivox_id IS NOT NULL ORDER BY r.work_id, r.id, c.idx`
}

func lvReadersSQL(ph string) string {
	return `SELECT r.work_id, r.id, p.name, p.librivox_reader_id FROM recordings r ` +
		`CROSS JOIN recording_narrators rn ON rn.work_id = r.work_id AND rn.recording_id = r.id ` +
		`JOIN people p ON p.id = rn.person_id WHERE r.work_id IN (` + ph + `) AND r.librivox_id IS NOT NULL ORDER BY r.work_id, r.id, rn.ord`
}

// librivoxFeedFacts reads the page's per-work and per-recording facts. The
// translators, genres and readers appear only on an extended page, so a plain
// one does not read them.
func (s *DB) librivoxFeedFacts(ctx context.Context, workIDs []string, extended bool) (*lvFacts, error) {
	f := &lvFacts{
		authors: map[string][]lvPerson{}, translators: map[string][]lvPerson{}, genres: map[string][]string{},
		chapters: map[recKey][]lvChapter{}, readers: map[recKey][]lvReader{},
	}
	// each runs one batched read over the page's works, handing every row to scan.
	each := func(sqlOf func(string) string, scan func(*sql.Rows) error) error {
		return eachChunk(workIDs, func(ph string, args []any) error {
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
	people := func(into map[string][]lvPerson) func(*sql.Rows) error {
		return func(rows *sql.Rows) error {
			var work string
			var p lvPerson
			var lvID, birth, death sql.NullInt64
			if err := rows.Scan(&work, &p.name, &lvID, &birth, &death); err != nil {
				return err
			}
			p.librivoxID, p.birthYear, p.deathYear = int(lvID.Int64), int(birth.Int64), int(death.Int64)
			into[work] = append(into[work], p)
			return nil
		}
	}
	if err := each(lvAuthorsSQL, people(f.authors)); err != nil {
		return nil, err
	}
	if err := each(lvChaptersSQL, func(rows *sql.Rows) error {
		var k recKey
		var c lvChapter
		var audio sql.NullString
		if err := rows.Scan(&k.work, &k.rec, &c.title, &c.lengthMS, &audio); err != nil {
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
	if err := each(lvTranslatorsSQL, people(f.translators)); err != nil {
		return nil, err
	}
	if err := each(lvGenresSQL, func(rows *sql.Rows) error {
		var work, genre string
		if err := rows.Scan(&work, &genre); err != nil {
			return err
		}
		f.genres[work] = append(f.genres[work], genre)
		return nil
	}); err != nil {
		return nil, err
	}
	if err := each(lvReadersSQL, func(rows *sql.Rows) error {
		var k recKey
		var rd lvReader
		var id sql.NullInt64
		if err := rows.Scan(&k.work, &k.rec, &rd.name, &id); err != nil {
			return err
		}
		rd.readerID = int(id.Int64)
		f.readers[k] = append(f.readers[k], rd)
		return nil
	}); err != nil {
		return nil, err
	}
	return f, nil
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
		book["sections"] = librivoxSections(chapters, f.readers[key], language)
		genres := make([]map[string]string, 0, len(f.genres[r.workID]))
		for _, g := range f.genres[r.workID] {
			genres = append(genres, map[string]string{"id": "", "name": g})
		}
		book["genres"] = genres
		book["translators"] = librivoxPeople(f.translators[r.workID])
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

// librivoxSections renders chapters as LibriVox sections. Readers are recording
// narrators: named on every section when the recording has exactly one, else
// none (a per-section reader is not stored).
func librivoxSections(chapters []lvChapter, narrators []lvReader, language string) []map[string]any {
	readers := []map[string]string{}
	if len(narrators) == 1 {
		readers = append(readers, map[string]string{"reader_id": intOr(narrators[0].readerID, ""), "display_name": narrators[0].name})
	}
	out := make([]map[string]any, 0, len(chapters))
	for i, c := range chapters {
		out = append(out, map[string]any{
			"id":             "",
			"section_number": strconv.Itoa(i + 1),
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
