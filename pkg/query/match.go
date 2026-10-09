package query

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kodestar/audiosilo-meta/internal/httpx"
)

// Structured match: GET /api/v1/works/match.
//
// WHY A SECOND SEARCH. works/search ANDs every word of one query, which is right
// for a search box and wrong for identifying a FILE: a file's tags are often
// garbage (a title tag reading "Bernard Cornwell", an author tag reading
// "Sharpe's Eagle (Sharpe 08)") while its folder path is good ("Bernard
// Cornwell/Richard Sharpe/Sharpe - 08 - Sharpe's Eagle"), and no single query
// string built from both finds the book. Measured on a real 1,166-book library,
// 463 books were left unmatched by text search although every one of them is in
// the catalogue; an offline prototype of this algorithm matched 434 of them with
// no wrong pick.
//
// So this endpoint takes the facts SEPARATELY - several title guesses, several
// author guesses, a series name or two and a position, a runtime, an
// identifier, and optionally the free text a person typed - gathers candidates
// along each of them, and ranks every candidate by how well its OWN facts
// agree (the scoring model is in matchscore.go). Each result is the work card
// search returns plus a 0-100 score and the reasons behind it, so a client can
// explain the rank.
//
// CANDIDATES come from five bounded probes, unioned in this order:
//
//  1. the identifier (lookupIDs, /lookup without the card), a certain answer;
//  2. the free text, exactly as works/search reads it (the FTS page and both
//     boosts), plus the exact-title probe of its leading and trailing word runs
//     ("dune herbert" names the title "dune");
//  3. every title hypothesis matched as whole words (ftsPhrase), plus the
//     exact-title probe;
//  4. the members of the named series, ahead of the authors because a series
//     resolves to at most a few series while a bare surname can resolve to
//     hundreds of people whose works would fill the pool;
//  5. the WORKS OF THE NAMED AUTHORS - the probe that finds a book whose title
//     is misspelt in the folder name or garbage in the tags. An author guess is
//     resolved to people through the FTS person rows: every word of the name
//     first, then the surname alone, keeping the people whose surname IS that
//     word (and whose first name agrees, when any does).
//
// WHAT KEEPS IT CHEAP. Like every route here it is unauthenticated and
// CORS-open, so a request must not be able to price itself:
//
//   - every text input is cut to maxQueryBytes, as a search query is, and at
//     most matchHypothesesMax values of each repeatable field are read;
//   - an author guess resolves through a matchPeopleWindow-row FTS window, and
//     the author and series probes together add at most matchCandidatesMax
//     works (the most prolific author in the catalogue has ~1,100);
//   - every comparison is bounded (maxEditRunes) and runs in Go over facts read
//     in a fixed number of batched, indexed queries per 400 ids;
//   - at most HandlerOptions.MatchConcurrency matches run at once, and each runs
//     under HandlerOptions.MatchBudget (PublicMatchBudget on metaserve): past it the request is a 503 rather than a long tail. Every
//     read takes the budget's context, the ones shared with search and lookup
//     included, so a spent budget stops the next query instead of waiting it out.
//
// Nothing about the artifact changes: it reads tables that have existed since
// schema_version 1, so it answers against every published release.

// The bounds, see the file header.
const (
	matchHypothesesMax  = 4
	matchSeriesMax      = 2
	matchNamesMax       = 6
	matchLimitDefault   = 10
	matchLimitMax       = 20
	matchPeopleWindow   = 400
	matchCandidatesMax  = 2500
	matchTitleWindow    = 10
	matchTextWindow     = 20
	matchConcurrency    = 8
	maxMatchIdentifier  = 20
	maxMatchRuntimeSecs = 1_000_000
)

// PublicMatchBudget is the works/match budget a PUBLIC deployment runs under
// (metaserve passes it as HandlerOptions.MatchBudget): past it the request is a
// 503 rather than a long tail, so a flood on an unauthenticated, CORS-open route
// cannot price itself. An in-process consumer takes the zero value instead (see
// defaultMatchBudget).
const PublicMatchBudget = 2 * time.Second

// errMatchBusy is a match that could not start or finish inside its budget.
var errMatchBusy = errors.New("match: over its time budget")

// matchRequest is one works/match request, parsed and bounded.
type matchRequest struct {
	text     string
	titles   []string
	authors  []string
	series   []string
	position string
	runtime  int // seconds; 0 = unknown
	asin     string
	isbn     string
	limit    int
}

// parseMatchRequest reads the query parameters. Every text value is trimmed and
// cut to maxQueryBytes, empty values are dropped, and repeatable fields keep
// their first matchHypothesesMax values (matchSeriesMax for the series: a
// folder's and a tag's). A position or runtime that does not parse is ignored
// rather than refused, as an out-of-range limit is: these come from file tags,
// and a garbage tag must not cost the rest of the request.
func parseMatchRequest(q url.Values) (matchRequest, bool) {
	text := func(v string) string { return strings.TrimSpace(boundQuery(strings.TrimSpace(v))) }
	list := func(vs []string, most int) []string {
		var out []string
		for _, v := range vs {
			if t := text(v); t != "" && !slices.Contains(out, t) {
				out = append(out, t)
			}
			if len(out) == most {
				break
			}
		}
		return out
	}
	r := matchRequest{
		text:    text(q.Get("q")),
		titles:  list(q["title"], matchHypothesesMax),
		authors: list(q["author"], matchHypothesesMax),
		series:  list(q["series"], matchSeriesMax),
		asin:    strings.ToUpper(strings.TrimSpace(q.Get("asin"))),
		isbn:    strings.ToUpper(normalizeISBN(q.Get("isbn"))),
		limit:   clampLimit(q.Get("limit"), matchLimitDefault, matchLimitMax),
	}
	if len(r.asin) > maxMatchIdentifier {
		r.asin = ""
	}
	if len(r.isbn) > maxMatchIdentifier {
		r.isbn = ""
	}
	if pos, ok := positionToken(strings.TrimSpace(q.Get("position"))); ok {
		r.position = pos
	}
	if n, err := strconv.Atoi(strings.TrimSpace(q.Get("runtime"))); err == nil && n > 0 && n <= maxMatchRuntimeSecs {
		r.runtime = n
	}
	ok := r.text != "" || len(r.titles) > 0 || len(r.authors) > 0 || len(r.series) > 0 || r.asin != "" || r.isbn != ""
	return r, ok
}

// matchResult is one ranked candidate: the work as works/search returns it,
// plus the score and its reasons. workResult is embedded, so the card's fields
// arrive exactly where a search hit carries them.
type matchResult struct {
	workResult
	// Score is 0-100: how well the work's own facts agree with the request.
	Score int `json:"score"`
	// RecordingID is the recording the identifier named, else the one whose
	// runtime fits the requested one best; omitted when neither applies.
	RecordingID string       `json:"recording_id,omitempty"`
	Reasons     matchReasons `json:"reasons"`
}

// matchReasons says which facts agreed. Every field is omitted when the request
// did not let it be judged, so a client never explains a score by a fact nobody
// asked about.
type matchReasons struct {
	// Title is the best title similarity (0-1) over the title hypotheses.
	Title *float64 `json:"title,omitempty"`
	// Text is the similarity (0-1) of the free text, read as a title once the
	// work's own author and series words are taken out of it; 1 when it named
	// the work's series and volume ("sharpe 8").
	Text *float64 `json:"text,omitempty"`
	// Author is "full", "surname" or "none".
	Author authorAgreement `json:"author,omitempty"`
	// Series is "position", "name", "conflict" or "none".
	Series seriesAgreement `json:"series,omitempty"`
	// Runtime is the relative difference between the requested runtime and the
	// closest recording's (0.02 = 2%).
	Runtime *float64 `json:"runtime,omitempty"`
	// Identifier is "asin" or "isbn" when the work was found by it.
	Identifier string `json:"identifier,omitempty"`
}

// handleMatch serves GET /api/v1/works/match.
func (s *handler) handleMatch(w http.ResponseWriter, r *http.Request, snap *DB) {
	req, ok := parseMatchRequest(r.URL.Query())
	if !ok {
		httpx.WriteErr(w, http.StatusBadRequest, "one of q, title, author, series, asin or isbn is required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.MatchBudget)
	defer cancel()
	select {
	case s.matchSlots <- struct{}{}:
		defer func() { <-s.matchSlots }()
	case <-ctx.Done():
		s.matchBusy(w)
		return
	}
	results, err := snap.match(ctx, req)
	switch {
	case errors.Is(err, errMatchBusy) || (err != nil && ctx.Err() != nil):
		s.matchBusy(w)
	case err != nil:
		s.fail(w, r, err)
	default:
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"results": results})
	}
}

// matchBusy answers a match that ran out of budget: 503, retry in a second.
func (s *handler) matchBusy(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "1")
	httpx.WriteErr(w, http.StatusServiceUnavailable, "the match did not finish in time; retry shortly")
}

// matchFacts is a request read for scoring: every hypothesis folded once.
type matchFacts struct {
	// titles holds the INFORMATIVE title variants (see informativeTitle);
	// structured is whether the request stated any structured fact at all.
	titles     []titleGuess
	structured bool
	authors    []personName
	series     []termForm
	// seriesNames are the series as probed: the request's, numbering cut off
	// ("03 - Tawny Man" is "Tawny Man"). series holds every hypothesis's
	// forms together, so a candidate's agreement is the best any of them
	// reaches (seriesVerdict) and position applies to whichever that is.
	seriesNames []string
	// position is the request's, else the number a title guess's numbering
	// carried ("Sharpe - 08 - Sharpe's Eagle" is volume 8).
	position string
	runtime  int
	// text is the free text's terms, nil when it says nothing identifying;
	// textTitle is the whole of it as a title guess, the first one textFacts
	// reads for every candidate, so it is built once here.
	text      []string
	textTitle titleGuess
	// namesVolume marks typed text read as a series and a volume number
	// (textFacts): the volume it names stands in for the title, at full weight.
	namesVolume bool
	// edit is the edit distance's scratch, reused by every comparison of the
	// request (judging runs on one goroutine).
	edit *editBuf
}

// titleGuess is one informative title variant with its sorted-name key, which
// is what candidate.isAuthorName compares. probe is the variant as written,
// which is what the title probes search for: the folded terms read "&" as
// "and" and strip diacritics, so a stored "Pride & Prejudice" holds no "and"
// for an FTS phrase to find and nameKey keeps "Renée" accented.
type titleGuess struct {
	termForm
	key   string
	probe string
}

func newTitleGuess(terms []string) titleGuess {
	return titleGuess{termForm: newTermForm(terms), key: sortedKey(terms)}
}

// readFacts folds a request into matchFacts.
func readFacts(req matchRequest) *matchFacts {
	f := &matchFacts{position: req.position, runtime: req.runtime, edit: &editBuf{}}
	names := map[string]bool{}
	for _, a := range req.authors {
		for _, n := range splitAuthors(a) {
			if key := n.key(); len(f.authors) < matchNamesMax && !names[key] {
				names[key] = true
				f.authors = append(f.authors, n)
			}
		}
	}
	for _, series := range req.series {
		cut, _ := cutNumbering(series)
		if cut != "" && !slices.Contains(f.seriesNames, cut) {
			f.seriesNames = append(f.seriesNames, cut)
		}
		for _, v := range []string{series, cut} {
			form := newTermForm(matchTerms(v))
			if len(form.terms) > 0 && !slices.ContainsFunc(f.series, func(x termForm) bool { return x.joined == form.joined }) {
				f.series = append(f.series, form)
			}
		}
	}
	// Every guess is read for numbering once, here: the number its prefix
	// carried ("Sharpe - 08 - ") and a variant that is the series' name and a
	// number ("The Primal Hunter 1") are POSITION evidence, never a title -
	// judged as a title the latter would read the series' first volume as the
	// answer to every volume. The name ALONE is kept as a title: plenty of
	// first volumes are titled after their series ("Spacers", "Artemis Fowl").
	seen := map[string]bool{}
	implied := ""
	imply := func(n string) {
		if p, ok := positionToken(n); ok && implied == "" {
			implied = p
		}
	}
	// Each guess is read beside each series name (titleVariants takes the
	// name off), the first name's variants first.
	besides := f.seriesNames
	if len(besides) == 0 {
		besides = []string{""}
	}
	for _, h := range req.titles {
		for _, name := range besides {
			variants, number := titleVariants(h, name)
			imply(number)
			for _, v := range variants {
				terms := matchTerms(v)
				key := strings.Join(terms, " ")
				if !informativeTitle(terms) || seen[key] {
					continue
				}
				if n, numbering := volumeOf(terms, f.series); numbering {
					imply(n)
					continue
				}
				seen[key] = true
				g := newTitleGuess(terms)
				g.probe = v
				f.titles = append(f.titles, g)
			}
		}
	}
	if f.position == "" {
		f.position = implied
	}
	f.structured = len(req.titles) > 0 || len(f.authors) > 0 || len(f.series) > 0
	if terms := matchTerms(req.text); informativeTitle(terms) {
		f.text, f.textTitle = terms, newTitleGuess(terms)
	}
	return f
}

// candidate is one work being judged: its facts, read in batches.
type candidate struct {
	id     string
	title  []string    // the title's own terms
	titles []titleForm // every form the title is compared in (candidateTitleForms)
	// authors are the credited names, authorKeys their sorted-name keys
	// (isAuthorName) and authorWord every word of them.
	authors    []personName
	authorKeys []string
	authorWord map[string]bool
	series     []candidateSeries
	runtimes   []recordingRuntime
	// identified is the identifier lookup that named this work, if one did.
	identified identification
}

// isAuthorName reports whether a title guess is exactly the name of one of the
// candidate's authors, in either order ("bernard cornwell", "cornwell
// bernard").
func (c *candidate) isAuthorName(g *titleGuess) bool { return slices.Contains(c.authorKeys, g.key) }

type candidateSeries struct {
	name     *termForm // shared by every candidate in the series (factMemo)
	position string
}

type recordingRuntime struct {
	id     string
	minute int
}

// identification is the recording an identifier lookup named, with the
// identifier's kind ("asin" / "isbn"); the zero value is none.
type identification struct{ recording, by string }

// scored is a candidate's verdict, what the ranking sorts by.
type scored struct {
	id          string
	score       float64
	title       float64 // best structured title similarity, for ties
	runtimeDiff float64 // closest recording's runtime difference; +Inf when not judged
	recordingID string
	reasons     matchReasons
}

// match runs one structured match over the snapshot. It returns at most
// req.limit results, best first; an empty answer is an empty slice.
func (s *DB) match(ctx context.Context, req matchRequest) ([]matchResult, error) {
	facts := readFacts(req)
	pool := newCandidatePool()

	// lookupIDs reads ONE identifier (the ASIN when both are given), so each is
	// asked on its own: a stale or regional ASIN must not hide a good ISBN.
	for _, id := range []struct{ by, asin, isbn string }{{"asin", req.asin, ""}, {"isbn", "", req.isbn}} {
		if id.asin == "" && id.isbn == "" {
			continue
		}
		workID, rid, found, err := s.lookupIDs(ctx, id.asin, id.isbn)
		if err != nil {
			return nil, err
		}
		if found {
			pool.addIdentified(workID, identification{recording: rid, by: id.by})
			break
		}
	}
	// A title the typed text and a title guess share is probed once: the
	// probe's own equality is nameKey's, so two spellings of one key are one
	// probe.
	probed := map[string]bool{}
	exactTitle := func(phrase string) error {
		key := nameKey(phrase)
		if probed[key] {
			return nil
		}
		probed[key] = true
		ids, err := s.exactTitleHits(ctx, phrase, nil)
		pool.add(ids...)
		return err
	}
	if facts.text != nil {
		hits, err := s.ftsHits(ctx, kindWork, ftsQuery(req.text), matchTextWindow, nil)
		if err != nil {
			return nil, err
		}
		// boostedWorks runs the exact-title probe of the whole text, so a title
		// guess spelling the same key is not probed again.
		pool.add(s.boostedWorks(ctx, req.text, nil)...)
		probed[nameKey(req.text)] = true
		for _, h := range hits {
			pool.add(h.id)
		}
		// Typed text is often a title with an author before or after it ("dune
		// herbert"): the FTS page ranks every Herbert-and-Dune row alike, so
		// the work whose title is the rest is probed for by exact title.
		for _, part := range textTitleRuns(req.text) {
			if err := exactTitle(part); err != nil {
				return nil, err
			}
		}
	}
	for _, g := range facts.titles {
		if err := ctx.Err(); err != nil {
			return nil, errMatchBusy
		}
		// The guess as written, then folded: the written form finds a stored
		// "&" or accent, the folded one a stored "and".
		for _, phrase := range []string{g.probe, g.joined} {
			if err := exactTitle(phrase); err != nil {
				return nil, err
			}
		}
		hits, err := s.ftsHits(ctx, kindWork, ftsPhrase(g.probe), matchTitleWindow, nil)
		if err != nil {
			return nil, err
		}
		for _, h := range hits {
			pool.add(h.id)
		}
	}
	// The series before the authors: it resolves at most seriesProbeLimit
	// series, while a bare common surname resolves hundreds of people whose
	// works would fill the pool and leave the named series unprobed.
	for _, name := range facts.seriesNames {
		if pool.full() || !worthProbing(name) {
			continue
		}
		ids, err := s.seriesMembers(ctx, name, matchCandidatesMax-pool.len())
		if err != nil {
			return nil, err
		}
		pool.add(ids...)
	}
	if len(facts.authors) > 0 && !pool.full() {
		var people []string
		for _, name := range facts.authors {
			ids, err := s.peopleNamed(ctx, name)
			if err != nil {
				return nil, err
			}
			people = append(people, ids...)
		}
		ids, err := s.idsByChunk(ctx, people, worksByPeopleSQL, matchCandidatesMax-pool.len())
		if err != nil {
			return nil, err
		}
		pool.add(ids...)
	}
	if err := ctx.Err(); err != nil {
		return nil, errMatchBusy
	}

	cands, err := s.candidateFacts(ctx, pool)
	if err != nil {
		if ctx.Err() != nil {
			return nil, errMatchBusy
		}
		return nil, err
	}
	verdicts := make([]scored, 0, len(cands))
	for i, c := range cands {
		// Judging is pure CPU over up to matchCandidatesMax works, so it asks
		// the budget too rather than running on past it holding a slot.
		if i%256 == 0 && ctx.Err() != nil {
			return nil, errMatchBusy
		}
		if v := facts.judge(c); v.score > 0 {
			verdicts = append(verdicts, v)
		}
	}
	sort.Slice(verdicts, func(i, j int) bool { return verdicts[i].better(verdicts[j]) })
	if len(verdicts) > req.limit {
		verdicts = verdicts[:req.limit]
	}
	return s.matchResults(ctx, verdicts)
}

// better is the ranking: score, then title similarity (an exact title first),
// then the closer runtime, then the id, so equal facts always rank the same way.
func (a scored) better(b scored) bool {
	switch {
	case a.score != b.score:
		return a.score > b.score
	case a.title != b.title:
		return a.title > b.title
	case a.runtimeDiff != b.runtimeDiff:
		return a.runtimeDiff < b.runtimeDiff
	}
	return a.id < b.id
}

// candidatePool is the ordered, capped, deduplicated candidate id set.
type candidatePool struct {
	ids        []string
	seen       map[string]bool
	identified map[string]identification
}

func newCandidatePool() *candidatePool {
	return &candidatePool{seen: map[string]bool{}, identified: map[string]identification{}}
}

func (p *candidatePool) add(ids ...string) {
	for _, id := range ids {
		if p.full() {
			return
		}
		if !p.seen[id] {
			p.seen[id] = true
			p.ids = append(p.ids, id)
		}
	}
}

func (p *candidatePool) addIdentified(id string, by identification) {
	p.identified[id] = by
	p.add(id)
}

func (p *candidatePool) len() int   { return len(p.ids) }
func (p *candidatePool) full() bool { return len(p.ids) >= matchCandidatesMax }

// ---- the author and series probes -------------------------------------------

// personMatchSQL reads the person rows of the FTS index whose NAME matches. The
// window is unordered (no bm25 sort over a common surname's whole match set):
// every row it returns is checked in Go, so rank adds nothing.
const personMatchSQL = `SELECT id, title FROM search_fts WHERE search_fts MATCH ? AND kind='` + string(kindPerson) + `' LIMIT ?`

// peopleNamed resolves one author guess to person ids. Every word of the name
// is tried first ("bernard cornwell" is Bernard Cornwell); when that finds
// nobody, the surname alone is, keeping the people whose surname IS that word
// and, when any of them has a first name that agrees, only those. A surname with
// no agreeing first name keeps every bearer: the title decides between Bernard
// and Patricia Cornwell, and a folder that says "B Cornwell" still finds him.
func (s *DB) peopleNamed(ctx context.Context, n personName) ([]string, error) {
	if len(n.given) > 0 {
		people, err := s.peopleMatching(ctx, strings.Join(n.terms(), " "))
		if err != nil {
			return nil, err
		}
		if ids, _ := bearers(people, n); len(ids) > 0 {
			return ids, nil
		}
	}
	people, err := s.peopleMatching(ctx, n.surname)
	if err != nil {
		return nil, err
	}
	ids, all := bearers(people, n)
	if len(ids) > 0 {
		return ids, nil
	}
	return all, nil
}

// personRow is one person the FTS probe returned, with the name parsed.
type personRow struct {
	id   string
	name personName
}

// peopleMatching runs one person probe over the name words.
func (s *DB) peopleMatching(ctx context.Context, words string) ([]personRow, error) {
	rows, err := s.db.QueryContext(ctx, personMatchSQL, titleMatch(words), matchPeopleWindow)
	if err != nil {
		return nil, err
	}
	pairs, err := scanPairs(rows, func(id, name string) personRow {
		n, ok := parseName(name)
		if !ok {
			return personRow{}
		}
		return personRow{id: id, name: n}
	})
	return slices.DeleteFunc(pairs, func(r personRow) bool { return r.id == "" }), err
}

// bearers sorts the people bearing n's surname: agreeing are those whose first
// name agrees too, all every bearer.
func bearers(people []personRow, n personName) (agreeing, all []string) {
	for _, p := range people {
		if p.name.surname != n.surname {
			continue
		}
		all = append(all, p.id)
		if givenCompatible(n.given, p.name.given) {
			agreeing = append(agreeing, p.id)
		}
	}
	return agreeing, all
}

// worksByPeopleSQL reads the works crediting any of a set of people as author,
// through idx_work_authors_person. DISTINCT, so a work two of the people wrote
// together takes one of the LIMIT's places, not two.
func worksByPeopleSQL(ph string) string {
	return `SELECT DISTINCT work_id FROM work_authors WHERE person_id IN (` + ph + `) LIMIT ?`
}

// seriesMembersByIDSQL reads the members of a set of series through
// idx_series_works_series, each once however many of the series hold it.
func seriesMembersByIDSQL(ph string) string {
	return `SELECT DISTINCT work_id FROM series_works WHERE series_id IN (` + ph + `) LIMIT ?`
}

// idsByChunk runs an id-list query (its SQL ends in "LIMIT ?") over ids in
// eachChunk batches and returns up to limit of the ids it reads.
func (s *DB) idsByChunk(ctx context.Context, ids []string, sqlFor func(ph string) string, limit int) ([]string, error) {
	var out []string
	err := eachChunk(ids, func(ph string, args []any) error {
		if len(out) >= limit {
			return nil
		}
		rows, err := s.db.QueryContext(ctx, sqlFor(ph), append(args, limit-len(out))...)
		if err != nil {
			return err
		}
		got, err := scanIDs(rows)
		out = append(out, got...)
		return err
	})
	return out, err
}

// seriesMembers resolves a series name exactly as the series-position boost
// does (seriesMatching + preferWholeName) and returns up to limit of its
// members.
func (s *DB) seriesMembers(ctx context.Context, name string, limit int) ([]string, error) {
	cands, err := s.seriesMatching(ctx, name, seriesProbeLimit, nil)
	if err != nil || len(cands) == 0 {
		return nil, err
	}
	cands = preferWholeName(cands, name)
	ids := make([]string, len(cands))
	for i, c := range cands {
		ids[i] = c.id
	}
	return s.idsByChunk(ctx, ids, seriesMembersByIDSQL, limit)
}

// ---- the candidates' facts ----------------------------------------------------

// The batched reads that load every candidate's facts: ONE query per kind of
// fact per 400 ids (eachChunk), each through an index the guard pins
// (TestBatchLookupsAreIndexed). The memberships are the card's own read,
// firstSeriesByWorkSQL, every row of it.
func matchWorksSQL(ph string) string {
	return `SELECT id, title, COALESCE(subtitle, '') FROM works WHERE id IN (` + ph + `)`
}

func matchRuntimesSQL(ph string) string {
	return `SELECT work_id, id, runtime_min FROM recordings WHERE work_id IN (` + ph + `) AND runtime_min > 0`
}

// factMemo folds each person and series name once per request: the author and
// series probes pool many works crediting one person or sitting in one series.
type factMemo struct {
	people map[string]memoPerson // by person id
	series map[string]*termForm  // by series name
}

type memoPerson struct {
	name personName
	ok   bool
	key  string
	// words is every word of the name, parseable or not.
	words []string
}

func (m *factMemo) person(id, name string) memoPerson {
	if p, ok := m.people[id]; ok {
		return p
	}
	n, ok := parseName(name)
	p := memoPerson{name: n, ok: ok, words: matchTerms(name)}
	if ok {
		p.key = n.key()
	}
	m.people[id] = p
	return p
}

func (m *factMemo) seriesName(name string) *termForm {
	if f, ok := m.series[name]; ok {
		return f
	}
	f := newTermForm(matchTerms(name))
	m.series[name] = &f
	return &f
}

// candidateFacts loads the facts of every pooled id, in pool order. An id the
// works table does not hold is dropped.
func (s *DB) candidateFacts(ctx context.Context, pool *candidatePool) ([]*candidate, error) {
	memo := &factMemo{people: map[string]memoPerson{}, series: map[string]*termForm{}}
	byID := make(map[string]*candidate, pool.len())
	err := eachChunk(pool.ids, func(ph string, args []any) error {
		return queryEach(ctx, s.db, matchWorksSQL(ph), args, func(rows *sql.Rows) error {
			var id, title, subtitle string
			if err := rows.Scan(&id, &title, &subtitle); err != nil {
				return err
			}
			terms := matchTerms(title)
			byID[id] = &candidate{
				id: id, title: terms, titles: candidateTitleForms(title, terms, subtitle),
				authorWord: map[string]bool{}, identified: pool.identified[id],
			}
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	found := make([]string, 0, len(byID))
	out := make([]*candidate, 0, len(byID))
	for _, id := range pool.ids {
		if c := byID[id]; c != nil {
			found = append(found, id)
			out = append(out, c)
		}
	}
	err = eachChunk(found, func(ph string, args []any) error {
		if err := queryEach(ctx, s.db, authorsByWorkSQL(ph), args, func(rows *sql.Rows) error {
			var workID, personID, name string
			if err := rows.Scan(&workID, &personID, &name); err != nil {
				return err
			}
			c, p := byID[workID], memo.person(personID, name)
			if p.ok {
				c.authors = append(c.authors, p.name)
				c.authorKeys = append(c.authorKeys, p.key)
			}
			for _, t := range p.words {
				c.authorWord[t] = true
			}
			return nil
		}); err != nil {
			return err
		}
		if err := queryEach(ctx, s.db, firstSeriesByWorkSQL(ph, false), args, func(rows *sql.Rows) error {
			var workID, seriesID, name, position string
			var orderingOf sql.NullString
			if err := rows.Scan(&workID, &seriesID, &name, &position, &orderingOf); err != nil {
				return err
			}
			c := byID[workID]
			c.series = append(c.series, candidateSeries{name: memo.seriesName(name), position: position})
			return nil
		}); err != nil {
			return err
		}
		return queryEach(ctx, s.db, matchRuntimesSQL(ph), args, func(rows *sql.Rows) error {
			var workID, rid string
			var minutes int
			if err := rows.Scan(&workID, &rid, &minutes); err != nil {
				return err
			}
			c := byID[workID]
			c.runtimes = append(c.runtimes, recordingRuntime{id: rid, minute: minutes})
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// queryEach runs query under ctx and hands each row to fn, closing the rows.
func queryEach(ctx context.Context, db *sql.DB, query string, args []any, fn func(*sql.Rows) error) error {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		if err := fn(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

// ---- judging ---------------------------------------------------------------------

// judge scores one candidate against the request: the better of the structured
// facts and the free text (see matchscore.go), or 100 for an identifier hit.
func (f *matchFacts) judge(c *candidate) scored {
	v := scored{id: c.id, runtimeDiff: math.Inf(1)}

	// Runtime is shared by both sets: each starts from it.
	var base wavg
	if f.runtime > 0 {
		best := 0.0
		for _, r := range c.runtimes {
			if fit, diff := runtimeFit(r.minute, f.runtime); diff < v.runtimeDiff {
				v.runtimeDiff, best, v.recordingID = diff, fit, r.id
			}
		}
		if !math.IsInf(v.runtimeDiff, 1) {
			d := math.Round(v.runtimeDiff*1000) / 1000
			v.reasons.Runtime = &d
			base.add(weightRuntime, best)
		}
	}

	if f.structured {
		v.score = f.structuredScore(c, &v, base)
	}
	if f.text != nil {
		// The typed text is judged as the facts it states about THIS work,
		// by the same rules; only its title agreement is reported, as text.
		t := scored{}
		v.score = max(v.score, f.textFacts(c).structuredScore(c, &t, base))
		text := t.title
		if t.reasons.Series == seriesPosition && t.reasons.Title == nil {
			text = 1 // it named the volume, which stood in for the title
		}
		v.reasons.Text = round2(text)
		if !f.structured {
			// The text's title is then the only one, so it is what the
			// ranking's exact-title-first tie-break reads.
			v.title = t.title
		}
	}
	if c.identified.by != "" {
		v.score = 1
		v.reasons.Identifier = c.identified.by
		v.recordingID = c.identified.recording
	}
	v.score = math.Round(v.score*100) / 100
	return v
}

// structuredScore is the weighted agreement of the structured facts, added to
// w (the runtime's).
func (f *matchFacts) structuredScore(c *candidate, v *scored, w wavg) float64 {
	// A title hypothesis that is the name of one of THIS work's authors ("Bernard
	// Cornwell" in a title tag) says nothing about which of their works it is,
	// so it is not judged against them. It is decided per candidate because the
	// strings alone cannot tell which field is the garbage one: beside a title
	// tag reading "Bernard Cornwell" may sit an author tag reading "Sharpe's
	// Eagle (Sharpe 08)", and dropping every title that equals some author
	// hypothesis would throw away the real title with the fake name.
	t, titleJudged := 0.0, false
	for i := range f.titles {
		h := &f.titles[i]
		if c.isAuthorName(h) {
			continue
		}
		titleJudged = true
		t = max(t, bestTitle(&h.termForm, c.titles, f.edit))
	}
	v.title = t
	series, titleValue := seriesNone, t
	if len(f.series) > 0 {
		series, titleValue = f.seriesVerdict(c, t)
		v.reasons.Series = series
	}
	switch {
	case titleJudged:
		v.reasons.Title = round2(t)
		w.add(weightTitle, titleValue)
	case f.namesVolume && series == seriesPosition:
		w.add(weightTitle, 1)
	case series != seriesPosition:
		// Titles that say nothing cannot confirm identity, so their weight
		// stays in the denominator unless the series and its position did.
		w.add(weightTitle, 0)
	}

	author := authorNone
	if len(f.authors) > 0 {
		author = agreeAuthors(f.authors, c.authors)
		v.reasons.Author = author
		w.add(weightAuthor, author.value())
	}
	if len(f.series) > 0 {
		w.add(weightSeries, series.value())
	}
	score := w.value()
	// Named authors none of whom wrote this: the strongest single sign of a
	// different book under the same title ("Inferno" by Dan Brown for a
	// folder of Travis Bagwell's).
	if len(f.authors) > 0 && author == authorNone {
		score *= authorMismatchFactor
	}
	return score
}

// seriesVerdict is the ONE place a candidate's numbering is judged: its
// memberships, and its title when that is only the named series' numbering, are
// compared with the series and position the request names, and arbitrated
// against its best title similarity t. It returns the agreement and the value
// the title then counts at:
//
//   - a GENERIC title - the series' name and a number, "Dork Diaries 6" (a
//     record stored under its volume number) - is numbering: the number
//     agreeing is the series agreeing and stands in for the title at
//     genericTitleValue (a real title that agrees still ranks first); a
//     different number is a different volume;
//   - a named title that clearly disagrees outranks a numbering match (see
//     seriesDistrustLow/High);
//   - in the named series but at ANOTHER volume, unless the title itself agrees
//     ("Sharpe's Siege" filed as volume 20, which is volume 19), whatever it
//     shares with the request's title is the series' naming pattern ("Tales
//     from a Not-So-..."), not identity, so the title counts nothing.
func (f *matchFacts) seriesVerdict(c *candidate, t float64) (seriesAgreement, float64) {
	agreement := seriesNone
	for _, m := range c.series {
		fits := slices.ContainsFunc(f.series, func(h termForm) bool { return seriesNameFits(&h, m.name, f.edit) })
		switch {
		case !fits:
		case f.position == "":
			agreement = seriesName
		case positionsEqual(f.position, m.position):
			agreement = seriesPosition
		case agreement == seriesNone:
			agreement = seriesConflict
		}
		if agreement == seriesPosition {
			break
		}
	}
	elsewhere := agreement == seriesConflict
	if agreement == seriesPosition && t >= seriesDistrustLow && t < seriesDistrustHigh {
		agreement = seriesConflict
	}
	value := t
	if n, ok := volumeOf(c.title, f.series); ok && f.position != "" {
		if positionsEqual(n, f.position) {
			agreement, elsewhere, value = seriesPosition, false, genericTitleValue
		} else {
			agreement, elsewhere = seriesConflict, true
		}
	}
	if elsewhere && t < seriesDistrustHigh {
		value = 0
	}
	return agreement, value
}

// textFacts reads the typed text as the structured facts it states about one
// candidate, which structuredScore then judges like any request's: the words
// naming one of its authors are that author ("dune herbert"); the rest, when
// it is one of its series' names (or part of one) and the number of a volume
// it holds there ("sharpe 8", "cornwell sharpe book 8"), is that series and
// position; otherwise the text is a title guess as typed, without the author's
// words, without the series' words, and without both.
func (f *matchFacts) textFacts(c *candidate) *matchFacts {
	t := &matchFacts{structured: true, edit: f.edit}
	for _, a := range c.authors {
		if slices.Contains(f.text, a.surname) {
			t.authors = []personName{{surname: a.surname}}
			break
		}
	}
	rest := filter(f.text, func(w string) bool { return c.authorWord[w] })
	if name, n, ok := splitVolume(rest); ok && informativeTitle(name) {
		words := filter(name, func(w string) bool { return probeStopwords[w] })
		if slices.ContainsFunc(c.series, func(m candidateSeries) bool {
			return positionsEqual(n, m.position) && !slices.ContainsFunc(words, func(w string) bool { return !m.name.set[w] })
		}) {
			t.series, t.position, t.namesVolume = []termForm{newTermForm(name)}, n, true
			return t
		}
	}
	isSeries := func(w string) bool {
		return slices.ContainsFunc(c.series, func(m candidateSeries) bool { return m.name.set[w] })
	}
	t.titles = []titleGuess{f.textTitle}
	seen := map[string]bool{f.textTitle.joined: true}
	for _, q := range [][]string{rest, filter(f.text, isSeries), filter(rest, isSeries)} {
		if key := strings.Join(q, " "); informativeTitle(q) && !seen[key] {
			seen[key] = true
			t.titles = append(t.titles, newTitleGuess(q))
		}
	}
	return t
}

// matchResults composes the ranked verdicts into their wire shape, reading the
// cards and narrators in one batch each, exactly as a search page does.
func (s *DB) matchResults(ctx context.Context, verdicts []scored) ([]matchResult, error) {
	ids := make([]string, len(verdicts))
	for i, v := range verdicts {
		ids[i] = v.id
	}
	works, err := s.workResults(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make([]matchResult, 0, len(verdicts))
	for _, v := range verdicts {
		if w, ok := works[v.id]; ok {
			out = append(out, matchResult{workResult: w, Score: int(math.Round(v.score * 100)), RecordingID: v.recordingID, Reasons: v.reasons})
		}
	}
	return out, nil
}
