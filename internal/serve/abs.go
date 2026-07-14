package serve

import (
	"net/http"
	"strings"
)

// This file implements the Audiobookshelf (ABS) custom metadata provider
// endpoint. ABS admins configure a base URL (ours is
// https://meta.audiosilo.app/abs) and ABS appends "/search", so the single
// public entrypoint is GET /abs/search. The contract is verified against ABS's
// server/providers/CustomProviderAdapter.js + custom-metadata-provider-
// specification.yaml:
//
//   - ABS sends query params: mediaType (always "book", ignored), query
//     (required), author (optional), isbn (optional). ABS never sends an ASIN.
//   - The response is {"matches": [BookMetadata...]}; ABS hard-fails if "matches"
//     is missing or not an array, so it is always a (possibly empty) array.
//   - Only "title" is required on a BookMetadata; every other field is omitted
//     when empty. "duration" is in MINUTES (our runtime_min maps directly);
//     "publishedYear" is a string; a series entry is {"series","sequence"}.
//   - No auth (our data is public); an inbound Authorization header is ignored.
//
// A match is one BookMetadata per RECORDING, since a recording is what ABS is
// matching a local audiobook against. Business logic lives in testable methods
// on *snapshot; the handler is transport-only.

// absMaxMatches caps the number of BookMetadata entries returned. ABS shows the
// admin a short pick-list, so a large result set is noise.
const absMaxMatches = 10

// absSeriesRef is one ABS series membership: the series name plus a string
// sequence ("2", "2.5", or an omnibus range like "1-3.5", passed through as-is).
type absSeriesRef struct {
	Series   string `json:"series"`
	Sequence string `json:"sequence,omitempty"`
}

// absBook is the ABS BookMetadata shape. Only Title is required; the rest are
// omitted when empty. Names are comma-joined; Duration is minutes; PublishedYear
// is a string. genres/tags are always empty for us (the data model deliberately
// does not carry publisher genres/tags - see LICENSING.md), so they are omitted.
type absBook struct {
	Title         string         `json:"title"`
	Subtitle      string         `json:"subtitle,omitempty"`
	Author        string         `json:"author,omitempty"`
	Narrator      string         `json:"narrator,omitempty"`
	Publisher     string         `json:"publisher,omitempty"`
	PublishedYear string         `json:"publishedYear,omitempty"`
	Description   string         `json:"description,omitempty"`
	Cover         string         `json:"cover,omitempty"`
	ISBN          string         `json:"isbn,omitempty"`
	ASIN          string         `json:"asin,omitempty"`
	Genres        []string       `json:"genres,omitempty"`
	Tags          []string       `json:"tags,omitempty"`
	Series        []absSeriesRef `json:"series,omitempty"`
	Language      string         `json:"language,omitempty"`
	Duration      int            `json:"duration,omitempty"`
}

// handleABSSearch is the transport-only handler for GET /abs/search. ABS always
// sends a query; a missing/empty one is a 400. It never 404s: a no-match is a
// 200 with an empty array.
func (s *Server) handleABSSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("query"))
	if q == "" {
		writeErr(w, http.StatusBadRequest, "query is required")
		return
	}
	author := strings.TrimSpace(r.URL.Query().Get("author"))
	isbn := strings.TrimSpace(r.URL.Query().Get("isbn"))

	matches, err := s.current().absSearch(q, author, isbn, absMaxMatches)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if matches == nil {
		matches = []absBook{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"matches": matches})
}

// absSearch resolves ABS query params into ranked BookMetadata matches:
//
//  1. If isbn is present, try an exact identifier lookup first (the same
//     resolution /lookup?isbn= uses). On a hit, return that work's recordings
//     with the matched recording first, and stop.
//  2. Otherwise (or if the isbn missed), FTS-search the query restricted to
//     works (reusing ftsQuery's defensive escaping). When author is given,
//     works whose authors match it loosely are boosted ahead of the rest.
//  3. Emit one BookMetadata per recording of each matched work, best-ranked
//     first, capped at limit.
//
// It always returns a non-nil slice.
func (s *snapshot) absSearch(query, author, isbn string, limit int) ([]absBook, error) {
	if limit <= 0 {
		limit = absMaxMatches
	}

	if isbn != "" {
		res, err := s.lookup("", isbn)
		if err != nil {
			return nil, err
		}
		if res != nil {
			d, err := s.workForABS(res.Work.ID)
			if err != nil {
				return nil, err
			}
			if d != nil {
				return capABS(absBooksFor(d, res.RecordingID), limit), nil
			}
		}
		// isbn missed: fall through to a title search.
	}

	workIDs, err := s.absWorkSearch(query, limit*3)
	if err != nil {
		return nil, err
	}
	if author != "" {
		workIDs, err = s.rankByAuthor(workIDs, author)
		if err != nil {
			return nil, err
		}
	}

	out := []absBook{}
	for _, id := range workIDs {
		if len(out) >= limit {
			break
		}
		d, err := s.workForABS(id)
		if err != nil {
			return nil, err
		}
		if d == nil {
			continue
		}
		out = append(out, absBooksFor(d, "")...)
	}
	return capABS(out, limit), nil
}

// absWorkSearch runs the FTS query restricted to works and returns matched work
// ids best-ranked first. It reuses ftsQuery so no user input can break the MATCH.
func (s *snapshot) absWorkSearch(query string, limit int) ([]string, error) {
	if limit <= 0 {
		limit = absMaxMatches
	}
	rows, err := s.db.Query(
		`SELECT id FROM search_fts WHERE search_fts MATCH ? AND kind='work' ORDER BY bm25(search_fts) LIMIT ?`,
		ftsQuery(query), limit)
	if err != nil {
		return nil, err
	}
	return scanIDs(rows)
}

// rankByAuthor is a stable partition: works whose authors match the author query
// loosely come first (preserving FTS order within each group), the rest follow.
// It boosts rather than filters, so a wrong author never empties the results.
func (s *snapshot) rankByAuthor(workIDs []string, author string) ([]string, error) {
	namesByWork, err := s.authorNamesForWorks(workIDs)
	if err != nil {
		return nil, err
	}
	matched := make([]string, 0, len(workIDs))
	rest := make([]string, 0, len(workIDs))
	for _, id := range workIDs {
		if authorMatches(namesByWork[id], author) {
			matched = append(matched, id)
		} else {
			rest = append(rest, id)
		}
	}
	return append(matched, rest...), nil
}

// authorNamesForWorks fetches every author display name for the given works in
// one query (work id -> names in authorship order), replacing rankByAuthor's
// per-work N+1. An empty input yields an empty map.
func (s *snapshot) authorNamesForWorks(workIDs []string) (map[string][]string, error) {
	out := make(map[string][]string, len(workIDs))
	if len(workIDs) == 0 {
		return out, nil
	}
	placeholders := make([]string, len(workIDs))
	args := make([]any, len(workIDs))
	for i, id := range workIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	rows, err := s.db.Query(
		`SELECT wa.work_id, p.name FROM work_authors wa JOIN people p ON p.id = wa.person_id `+
			`WHERE wa.work_id IN (`+strings.Join(placeholders, ",")+`) ORDER BY wa.work_id, wa.ord`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var wid, name string
		if err := rows.Scan(&wid, &name); err != nil {
			return nil, err
		}
		out[wid] = append(out[wid], name)
	}
	return out, rows.Err()
}

// absBooksFor maps a work's detail to one BookMetadata per recording (or a
// single work-only entry when the work has no recordings). Work-level fields
// (title/subtitle/authors/language/publishedYear/description/series) are shared;
// recording-level fields (narrators/publisher/cover/duration/asin/isbn) vary. If
// preferredRID is set and present, that recording is moved to the front.
func absBooksFor(d *workDetail, preferredRID string) []absBook {
	series := make([]absSeriesRef, 0, len(d.Series))
	for _, sr := range d.Series {
		series = append(series, absSeriesRef{Series: sr.Name, Sequence: sr.Position})
	}
	if len(series) == 0 {
		series = nil
	}
	base := absBook{
		Title:         d.Title,
		Subtitle:      d.Subtitle,
		Author:        strings.Join(personNames(d.Authors), ", "),
		PublishedYear: d.FirstPublished,
		Description:   d.Description,
		Language:      d.Language,
		Series:        series,
	}

	// A recording ISBN is preferred; a work print ISBN is the fallback.
	var workISBN string
	if d.Xref != nil && len(d.Xref.ISBN) > 0 {
		workISBN = d.Xref.ISBN[0]
	}

	if len(d.Recordings) == 0 {
		b := base
		b.ISBN = workISBN
		return []absBook{b}
	}

	out := make([]absBook, 0, len(d.Recordings))
	preferred := -1
	for i, rec := range d.Recordings {
		b := base
		b.Narrator = strings.Join(personNames(rec.Narrators), ", ")
		b.Publisher = rec.Publisher
		b.Cover = rec.CoverURL
		b.Duration = rec.RuntimeMin
		b.ASIN = pickASIN(rec.ASIN)
		if len(rec.ISBN) > 0 {
			b.ISBN = rec.ISBN[0]
		} else {
			b.ISBN = workISBN
		}
		out = append(out, b)
		if preferredRID != "" && rec.ID == preferredRID {
			preferred = i
		}
	}
	if preferred > 0 {
		pref := out[preferred]
		rest := append(out[:preferred:preferred], out[preferred+1:]...)
		out = append([]absBook{pref}, rest...)
	}
	return out
}

// pickASIN prefers a us-region ASIN, falling back to the first available.
func pickASIN(asins []asinRef) string {
	if len(asins) == 0 {
		return ""
	}
	for _, a := range asins {
		if a.Region == "us" {
			return a.ASIN
		}
	}
	return asins[0].ASIN
}

// personNames projects person refs to their display names, in order.
func personNames(refs []personRef) []string {
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		out = append(out, r.Name)
	}
	return out
}

// authorMatches reports whether any author name loosely matches the author
// query: a case-insensitive substring either way, or a shared word token (len
// >= 2). It is deliberately lenient - ABS passes a raw folder/tag author string
// that rarely matches our canonical name exactly.
func authorMatches(names []string, author string) bool {
	author = strings.ToLower(strings.TrimSpace(author))
	if author == "" {
		return false
	}
	joined := strings.ToLower(strings.Join(names, " "))
	if strings.TrimSpace(joined) == "" {
		return false
	}
	if strings.Contains(joined, author) || strings.Contains(author, joined) {
		return true
	}
	nameTokens := map[string]bool{}
	for _, t := range strings.Fields(joined) {
		if len(t) >= 2 {
			nameTokens[t] = true
		}
	}
	for _, t := range strings.Fields(author) {
		if len(t) >= 2 && nameTokens[t] {
			return true
		}
	}
	return false
}

// capABS trims a match slice to limit and guarantees a non-nil result.
func capABS(books []absBook, limit int) []absBook {
	if len(books) > limit {
		books = books[:limit]
	}
	if books == nil {
		return []absBook{}
	}
	return books
}
