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
//
// A PER-LANGUAGE provider is the same endpoint under a language segment: an admin
// whose library is German configures https://meta.audiosilo.app/abs/de, and ABS
// calls /abs/de/search. ABS builds that URL by plain concatenation
// (CustomProviderAdapter.js: `${provider.url}/search?...`, the configured string
// kept as typed), which is why the language rides in the PATH - ABS sends only
// its four query parameters and there is nowhere else to put it. A base URL
// configured with a trailing slash arrives as /abs/de//search, which ServeMux's
// path cleaning answers with a 307 to /abs/de/search (query kept), and ABS's axios
// follows a redirected GET.
//
// The segment RANKS, it never filters. ABS asks "which book is this file", and
// the answer may be a German library's English original; a filter would turn
// that into "no results". So the candidates are the language-matched hits
// followed by the unfiltered ones, and the author - evidence about THIS book -
// still dominates the language, which is only a library-wide default:
// (author + language, author, language, the rest).

// absLangWildcard is the path wildcard /abs/{lang}/search reads its filter from.
const absLangWildcard = "lang"

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
// is a string. Genres carry the work's entries from this project's own
// normalized vocabulary, never a retailer's taxonomy (see LICENSING.md), as
// human-facing DISPLAY LABELS (ABS renders them as chips for an admin) rather
// than the raw slugs the JSON API serves; a work with none omits the key. Tags
// stay empty - the data model has no tag concept, so there is nothing to fill
// them with.
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
	s.serveABS(w, r, nil)
}

// handleABSLangSearch is GET /abs/{lang}/search: the same search ranked toward
// the languages the segment names. A segment that is not a language list is a
// 404 - it names no provider this server offers, and ABS shows "no results"
// either way - rather than the 400 a malformed ?lang= is.
func (s *Server) handleABSLangSearch(w http.ResponseWriter, r *http.Request) {
	lang, err := parseLangFilter(r.PathValue(absLangWildcard))
	if err != nil || len(lang) == 0 {
		writeErr(w, http.StatusNotFound, "unknown provider language")
		return
	}
	s.serveABS(w, r, lang)
}

// serveABS is the transport both ABS routes share: lang is nil on the unscoped
// one.
func (s *Server) serveABS(w http.ResponseWriter, r *http.Request, lang langFilter) {
	q := strings.TrimSpace(r.URL.Query().Get("query"))
	if q == "" {
		writeErr(w, http.StatusBadRequest, "query is required")
		return
	}
	author := strings.TrimSpace(r.URL.Query().Get("author"))
	// ABS commonly sends a hyphenated ISBN (e.g. "978-0-575-08244-1"), but stored
	// ISBNs are bare 10/13-digit strings, so the exact /lookup?isbn= resolution
	// would never match and the request would silently degrade to an FTS title
	// search. Normalize to the bare form here so the exact-lookup path fires.
	isbn := normalizeISBN(r.URL.Query().Get("isbn"))

	matches, err := s.current().absSearch(q, author, isbn, absMaxMatches, lang)
	if err != nil {
		s.fail(w, r, err)
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
// lang (the /abs/{lang}/search segment, nil on /abs/search) widens step 2 into
// two windows - the language-matched hits, then the unfiltered ones - and ranks
// the language second to the author (see the file header). An exact ISBN is not
// re-ranked: it names one recording outright. Below languagesSchemaVersion lang
// is ignored, and the answer is the unscoped one.
//
// It always returns a non-nil slice.
func (s *snapshot) absSearch(query, author, isbn string, limit int, lang langFilter) ([]absBook, error) {
	if limit <= 0 {
		limit = absMaxMatches
	}
	lang = s.liveLang(lang)

	if isbn != "" {
		res, err := s.lookup("", isbn)
		if err != nil {
			return nil, err
		}
		if res != nil {
			genres, descriptions, err := s.absWorkFacets([]string{res.Work.ID})
			if err != nil {
				return nil, err
			}
			d, err := s.workForABS(res.Work.ID, genres, descriptions)
			if err != nil {
				return nil, err
			}
			if d != nil {
				return capABS(absBooksFor(d, res.RecordingID), limit), nil
			}
		}
		// isbn missed: fall through to a title search.
	}

	workIDs, inLang, err := s.absCandidates(query, limit*3, lang)
	if err != nil {
		return nil, err
	}
	if author != "" || inLang != nil {
		workIDs, err = s.rankByAuthor(workIDs, author, inLang)
		if err != nil {
			return nil, err
		}
	}
	genres, descriptions, err := s.absWorkFacets(workIDs)
	if err != nil {
		return nil, err
	}

	out := []absBook{}
	for _, id := range workIDs {
		if len(out) >= limit {
			break
		}
		d, err := s.workForABS(id, genres, descriptions)
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

// absCandidates is step 2's candidate list. With no language it is the one
// unfiltered window it always was, and inLang is nil. With one it is the
// language-matched window followed by the unfiltered window, deduplicated (each
// limit long, so a language-matched work outranked by limit others still makes
// the list, and an original in another language is never lost to the preference),
// and inLang is the set the first window found - what the ranking reads.
//
// Membership is read off the filtered query rather than off each work's tag: a
// work in the unfiltered window that matches the language is, by bm25 order,
// already in the filtered one.
func (s *snapshot) absCandidates(query string, limit int, lang langFilter) (ids []string, inLang map[string]bool, err error) {
	all, err := s.absWorkSearch(query, limit, nil)
	if err != nil || lang == nil {
		return all, nil, err
	}
	matched, err := s.absWorkSearch(query, limit, lang)
	if err != nil {
		return nil, nil, err
	}
	inLang = make(map[string]bool, len(matched))
	for _, id := range matched {
		inLang[id] = true
	}
	ids = matched
	for _, id := range all {
		if !inLang[id] {
			ids = append(ids, id)
		}
	}
	return ids, inLang, nil
}

// absWorkSearch runs the FTS query restricted to works and returns matched work
// ids best-ranked first. It goes through the same ftsHits the JSON search
// endpoints use - one SQL constant, one escaping, one ranking - and keeps only
// the ids, the kind being kindWork by construction. A ranking change therefore
// lands on both surfaces at once rather than on whichever one was remembered.
// lang must be live; nil is the unfiltered search.
func (s *snapshot) absWorkSearch(query string, limit int, lang langFilter) ([]string, error) {
	if limit <= 0 {
		limit = absMaxMatches
	}
	hits, err := s.ftsHits(kindWork, ftsQuery(query), limit, lang)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(hits))
	for _, h := range hits {
		ids = append(ids, h.id)
	}
	return ids, nil
}

// rankByAuthor is a stable partition: works whose authors match the author query
// loosely come first (preserving FTS order within each group), the rest follow.
// It boosts rather than filters, so a wrong author never empties the results.
//
// inLang, when the request named a language, splits each of those two groups
// again - the language-matched works first - so the order is (author + language,
// author, language, the rest): the author is evidence about this one book, the
// language a library-wide default, so the author dominates. With inLang nil the
// partition is the author one alone, byte for byte what /abs/search always did.
func (s *snapshot) rankByAuthor(workIDs []string, author string, inLang map[string]bool) ([]string, error) {
	var namesByWork map[string][]string
	if author != "" {
		var err error
		if namesByWork, err = s.authorNamesForWorks(workIDs); err != nil {
			return nil, err
		}
	}
	var groups [4][]string
	for _, id := range workIDs {
		g := 0
		if !authorMatches(namesByWork[id], author) {
			g += 2
		}
		if inLang != nil && !inLang[id] {
			g++
		}
		groups[g] = append(groups[g], id)
	}
	out := make([]string, 0, len(workIDs))
	for _, g := range groups {
		out = append(out, g...)
	}
	return out, nil
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

// genresForWorks fetches every genre slug for the given works in one query (work
// id -> slugs ascending), mirroring authorNamesForWorks: absSearch resolves the
// whole candidate set up front so workForABS runs no per-work genre query on the
// public hot path. An empty input, or an artifact predating the work_genres
// table, yields an empty map.
func (s *snapshot) genresForWorks(workIDs []string) (map[string][]string, error) {
	out := make(map[string][]string, len(workIDs))
	if len(workIDs) == 0 || s.schemaVersion < genresSchemaVersion {
		return out, nil
	}
	placeholders := make([]string, len(workIDs))
	args := make([]any, len(workIDs))
	for i, id := range workIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	rows, err := s.db.Query(
		`SELECT work_id, genre FROM work_genres WHERE work_id IN (`+strings.Join(placeholders, ",")+
			`) ORDER BY work_id, genre`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var wid, genre string
		if err := rows.Scan(&wid, &genre); err != nil {
			return nil, err
		}
		out[wid] = append(out[wid], genre)
	}
	return out, rows.Err()
}

// absWorkFacets resolves the two per-work facets workForABS is HANDED rather
// than reading itself - genres and the community description - for a whole
// candidate set in one query each. It exists so the two cannot drift apart: both
// are batched for the same reason (a per-candidate read is up to absMaxMatches
// sequential round trips on a public, unauthenticated endpoint), and a third
// facet added later has one place to be added.
func (s *snapshot) absWorkFacets(workIDs []string) (map[string][]string, map[string]*descriptionOut, error) {
	genres, err := s.genresForWorks(workIDs)
	if err != nil {
		return nil, nil, err
	}
	descriptions, err := s.descriptionsForWorks(workIDs)
	if err != nil {
		return nil, nil, err
	}
	return genres, descriptions, nil
}

// genreAcronyms are the vocabulary values whose display label is not plain title
// case. A new acronym-shaped or oddly-capitalized value added to the
// common.schema.json genre enum needs an entry here; a plain word never does.
var genreAcronyms = map[string]string{
	"lgbtq":  "LGBTQ",
	"litrpg": "LitRPG",
}

// genreLabel renders a stored genre slug as the human-facing label ABS shows in
// its match chips ("hard-science-fiction" -> "Hard Science Fiction"). It lives on
// the ABS edge only: the JSON API keeps the raw slug, since that is the machine
// contract consumers match on.
func genreLabel(slug string) string {
	if label, ok := genreAcronyms[slug]; ok {
		return label
	}
	words := strings.Split(slug, "-")
	for i, w := range words {
		if w == "" {
			continue
		}
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}

// genreLabels maps a work's stored slugs to display labels, nil for none (so the
// absBook omits the key rather than emitting an empty array).
func genreLabels(slugs []string) []string {
	if len(slugs) == 0 {
		return nil
	}
	out := make([]string, 0, len(slugs))
	for _, s := range slugs {
		out = append(out, genreLabel(s))
	}
	return out
}

// displayDescription is the paragraph a HUMAN-facing surface shows for a work:
// the community's own-words, spoiler-free description where one exists, and the
// CC0 record's own description otherwise. Both the ABS facade and the HTML work
// page read it, so the two cannot disagree about which text a work "has".
//
// The community text WINS because it is the one somebody wrote for a reader:
// nothing writes works.description today, and if something ever does, an
// own-words paragraph still beats an imported one. The two are kept apart in the
// JSON (see descriptionOut) precisely so a consumer that must attribute can tell
// them apart; this helper is for the surfaces that only need prose.
//
// It answers BOTH halves at once - WHICH text, and whether that text is the
// community's - because they are one question, and a surface that asked them
// separately could print the CC BY-SA notice beside the CC0 field, or omit it
// beside share-alike prose. Every surface goes through this: the work page's
// view (which prints the notice), the JSON-LD, and absDescription below (which
// appends the credit, having no notice to print).
func displayDescription(d *workDetail) (text string, community bool) {
	if t := communityDescriptionText(d); t != "" {
		return t, true
	}
	return d.Description, false
}

// absCommunityAttribution is the plain-text credit appended to a COMMUNITY
// description on the /abs/search surface, and only there.
//
// Every other surface that shows this text prints a rel="license" notice beside
// it - the work page's fact sheet, the guide pages, the JSON-LD's `license` -
// and the JSON API hands the license back as a field of its own (descriptionOut).
// absBook has no license field: the shape is Audiobookshelf's, we do not own it,
// and ABS pastes the description straight into a library record. So the credit
// travels IN the text or it does not travel at all, and shipping CC BY-SA prose
// with no attribution is not something LICENSING.md permits.
//
// The CC0 fallback gets no suffix: it is not share-alike and crediting the
// community for a record field they did not write would be its own falsehood.
const absCommunityAttribution = "\n\n(Description CC BY-SA 4.0, AudioSilo Meta community)"

// absDescription is displayDescription for the ABS payload: the same choice of
// text, with the attribution suffix when the choice was the community's.
func absDescription(d *workDetail) string {
	text, community := displayDescription(d)
	if community {
		return text + absCommunityAttribution
	}
	return text
}

// absBooksFor maps a work's detail to one BookMetadata per recording (or a
// single work-only entry when the work has no recordings). Work-level fields
// (title/subtitle/authors/language/publishedYear/description/genres/series) are shared;
// recording-level fields (narrators/publisher/cover/duration/asin/isbn) vary. If
// preferredRID is set and present, that recording is moved to the front. This is
// the ONE place genre slugs become display labels.
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
		PublishedYear: publishedYear(d.FirstPublished),
		Description:   absDescription(d),
		Language:      d.Language,
		Genres:        genreLabels(d.Genres),
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

// publishedYear reduces a work's first_published to the bare 4-digit year ABS's
// publishedYear field wants. The schema's date_year allows both "YYYY" and a
// full "YYYY-MM-DD" (see common.schema.json), so a work may carry either form.
// If the string starts with exactly 4 digits (optionally followed by "-..."),
// the leading year is returned; anything else is passed through unchanged so an
// unexpected value is never mangled. It is a small local helper on purpose - the
// serve package must not import internal/importer.
func publishedYear(firstPublished string) string {
	if len(firstPublished) < 4 {
		return firstPublished
	}
	for i := 0; i < 4; i++ {
		if firstPublished[i] < '0' || firstPublished[i] > '9' {
			return firstPublished
		}
	}
	// A 5th char must be the "-" of "YYYY-MM-DD"; a longer run of digits (e.g. a
	// stray "20211") is not a year we recognize, so leave it untouched.
	if len(firstPublished) > 4 && firstPublished[4] != '-' {
		return firstPublished
	}
	return firstPublished[:4]
}

// normalizeISBN strips the ASCII hyphens and whitespace ABS sends in an ISBN
// (e.g. "978-0-575-08244-1" or " 9780575082441 ") down to the bare digit form
// stored on recordings, so the exact /lookup?isbn= resolution matches instead of
// silently degrading to an FTS title search. It is kept local to abs.go - the
// serve package must not import internal/issueform. The raw hyphenated form is
// not preserved anywhere: stored ISBNs are always bare.
func normalizeISBN(isbn string) string {
	var b strings.Builder
	b.Grow(len(isbn))
	for _, r := range isbn {
		if r == '-' || r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
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
