package query

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kodestar/audiosilo-meta/internal/artifacttest"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// rawResults is one works/match answer, each result's members still as sent.
func rawResults(t *testing.T, body []byte) []map[string]json.RawMessage {
	t.Helper()
	var out struct {
		Results []map[string]json.RawMessage `json:"results"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return out.Results
}

func matchPath(params url.Values) string { return "/api/v1/works/match?" + params.Encode() }

// TestMatchExpandIsTheWorkDocument: the best result's work is byte for byte what
// GET works/{id} serves, the runner-up carries nothing inlined, and the rest of the
// result is what it is without expand.
func TestMatchExpandIsTheWorkDocument(t *testing.T) {
	_, ts := newTestServerForCatalog(t, matchCatalog())
	params := url.Values{"title": {"Sharpe's Eagle"}, "author": {"Bernard Cornwell"}, "series": {"Richard Sharpe Novels"}, "limit": {"2"}}
	_, plain := getRaw(t, ts.URL, matchPath(params))
	params.Set("expand", "work")
	code, body := getRaw(t, ts.URL, matchPath(params))
	if code != http.StatusOK {
		t.Fatalf("status %d: %s", code, body)
	}
	results, before := rawResults(t, body), rawResults(t, plain)
	if len(results) != 2 || len(before) != 2 {
		t.Fatalf("results = %d and %d, want a best and a runner-up in each", len(results), len(before))
	}
	_, doc := getRaw(t, ts.URL, "/api/v1/works/sharpes-eagle")
	if got := append(bytes.Clone(results[0]["work"]), '\n'); !bytes.Equal(got, doc) {
		t.Errorf("inlined work:\n%s\nGET works/{id}:\n%s", got, doc)
	}
	if !strings.Contains(string(results[0]["series_facts"]), `"richard-sharpe-novels"`) {
		t.Errorf("series_facts = %s", results[0]["series_facts"])
	}
	for _, key := range []string{"work", "series_facts"} {
		if _, has := results[1][key]; has {
			t.Errorf("the runner-up carries %s", key)
		}
	}
	// The result's own members are untouched.
	for i := range results {
		delete(results[i], "work")
		delete(results[i], "series_facts")
		a, _ := json.Marshal(results[i])
		b, _ := json.Marshal(before[i])
		if !bytes.Equal(a, b) {
			t.Errorf("result %d changed by expand:\n%s\nwas:\n%s", i, a, b)
		}
	}
}

// TestMatchExpandSeriesFactsAreTheSeriesRecords: each fact is the value GET
// series/{id} serves, and a variant reading order, which no book is in and the
// client's rules ask nothing of, is left out.
func TestMatchExpandSeriesFactsAreTheSeriesRecords(t *testing.T) {
	_, ts := newTestServerForCatalog(t, artifacttest.Languages())
	translated := matchJSON(t, ts.URL, url.Values{"title": {"Buch Eins"}, "language": {"de"}, "expand": {"work"}})
	if len(translated) == 0 || translated[0]["id"] != "buch-eins" {
		t.Fatalf("best = %v", matchIDs(translated))
	}
	facts, _ := translated[0]["series_facts"].(map[string]any)
	if len(facts) == 0 {
		t.Fatalf("no series_facts on %v", translated[0])
	}
	for id, f := range facts {
		_, record := artifacttest.GetJSON(t, ts.URL, "/api/v1/series/"+id)
		want := map[string]any{}
		if lang, ok := record["language"]; ok {
			want["language"] = lang
		}
		if of, ok := record["translation_of"]; ok {
			want["translation_of"] = ids(t, of)
		}
		a, _ := json.Marshal(f)
		b, _ := json.Marshal(want)
		if !bytes.Equal(a, b) {
			t.Errorf("facts of %s = %s, the record says %s", id, a, b)
		}
	}
	if of, _ := facts["die-saga"].(map[string]any); of == nil || fmt.Sprint(of["translation_of"]) != "[the-saga]" {
		t.Errorf("die-saga's facts = %v, want it to translate the-saga", facts["die-saga"])
	}

	// book-one sits in the primary and its variant orders.
	original := matchJSON(t, ts.URL, url.Values{"title": {"Book One"}, "language": {"en"}, "expand": {"work"}})
	work, _ := original[0]["work"].(map[string]any)
	var variants int
	for _, s := range work["series"].([]any) {
		ref := s.(map[string]any)
		if ref["ordering_of"] != nil {
			variants++
			if _, listed := original[0]["series_facts"].(map[string]any)[ref["id"].(string)]; listed {
				t.Errorf("series_facts lists the variant %v", ref["id"])
			}
		}
	}
	if variants == 0 {
		t.Errorf("fixture: book-one lists no variant order: %v", work["series"])
	}
}

// TestMatchExpandSeriesFactsOnAV6Artifact: before the languages layer a series has
// no language column, so its facts are empty rather than the read failing, and the
// expansion is whole.
func TestMatchExpandSeriesFactsOnAV6Artifact(t *testing.T) {
	db := openTestDB(t, v6ShapedDB(t, artifacttest.Languages()))
	srv := newHandler(func() *DB { return db }, HandlerOptions{Logger: artifacttest.QuietLogger()})
	code, body := getRaw(t, httptestServer(t, srv), matchPath(url.Values{"title": {"Book One"}, "expand": {"work"}}))
	if code != http.StatusOK {
		t.Fatalf("status %d: %s", code, body)
	}
	results := rawResults(t, body)
	var facts map[string]seriesFacts
	if err := json.Unmarshal(results[0]["series_facts"], &facts); err != nil || len(facts) == 0 {
		t.Fatalf("series_facts = %s (%v), want an entry per held series", results[0]["series_facts"], err)
	}
	for id, f := range facts {
		if f.Language != "" || len(f.TranslationOf) != 0 {
			t.Errorf("facts of %s on a v6 artifact = %+v, want none", id, f)
		}
	}
}

// TestMatchExpandIsBounded: a series adds its facts and none of its members,
// and a work listed in more series than any real one reads the facts of the first
// matchExpandSeriesMax alone.
func TestMatchExpandIsBounded(t *testing.T) {
	cat := matchCatalog()
	long := &model.Series{ID: "long-saga", Name: "Long Saga", License: "CC0-1.0"}
	listed := &model.Work{ID: "much-listed", Title: "Much Listed", Language: "en", Authors: []string{"dan-brown"}, License: "CC0-1.0"}
	cat.Works = append(cat.Works, listed)
	long.Works = append(long.Works, model.SeriesWork{Work: listed.ID, Position: "1"})
	for i := range 3 {
		w := &model.Work{ID: "saga-" + strconv.Itoa(i), Title: "Saga Volume " + strconv.Itoa(i), Language: "en", Authors: []string{"dan-brown"}, License: "CC0-1.0"}
		cat.Works = append(cat.Works, w)
		long.Works = append(long.Works, model.SeriesWork{Work: w.ID, Position: strconv.Itoa(i + 2)})
	}
	cat.Series = append(cat.Series, long)
	for i := range matchExpandSeriesMax + 4 {
		cat.Series = append(cat.Series, &model.Series{
			ID: "side-" + strconv.Itoa(i), Name: "Side " + strconv.Itoa(i), License: "CC0-1.0",
			Works: []model.SeriesWork{{Work: listed.ID, Position: "1"}},
		})
	}
	_, ts := newTestServerForCatalog(t, cat)

	params := url.Values{"title": {"Much Listed"}, "author": {"Dan Brown"}, "limit": {"2"}, "expand": {"work"}}
	code, body := getRaw(t, ts.URL, matchPath(params))
	if code != http.StatusOK {
		t.Fatalf("status %d: %s", code, body)
	}
	results := rawResults(t, body)
	var work WorkDetail
	if err := json.Unmarshal(results[0]["work"], &work); err != nil {
		t.Fatal(err)
	}
	if work.ID != listed.ID {
		t.Fatalf("best work = %s", work.ID)
	}
	var facts map[string]seriesFacts
	if err := json.Unmarshal(results[0]["series_facts"], &facts); err != nil {
		t.Fatal(err)
	}
	if len(work.Series) != matchExpandSeriesMax+5 || len(facts) != matchExpandSeriesMax {
		t.Errorf("a work in %d series read %d facts, want the first %d", len(work.Series), len(facts), matchExpandSeriesMax)
	}
	for _, ref := range work.Series[:matchExpandSeriesMax] {
		if _, ok := facts[ref.ID]; !ok {
			t.Errorf("no facts for %s, one of the first %d", ref.ID, matchExpandSeriesMax)
		}
	}
	if bytes.Contains(results[0]["series_facts"], []byte("saga-")) {
		t.Errorf("series_facts names members of the series: %s", results[0]["series_facts"])
	}
}

// TestMatchExpandValues: an unknown value is ignored, so the answer is the plain
// one to the byte; the value is read from a list and from repeats.
func TestMatchExpandValues(t *testing.T) {
	_, ts := newTestServerForCatalog(t, matchCatalog())
	params := url.Values{"title": {"Dune"}, "author": {"Frank Herbert"}}
	_, plain := getRaw(t, ts.URL, matchPath(params))
	for _, values := range [][]string{{"future"}, {""}, {"works"}, {"WORK"}, {"future,other", "another"}} {
		params["expand"] = values
		code, body := getRaw(t, ts.URL, matchPath(params))
		if code != http.StatusOK || !bytes.Equal(body, plain) {
			t.Errorf("expand=%q: status %d, body differs from the plain answer:\n%s", values, code, body)
		}
	}
	for _, values := range [][]string{{"work"}, {"future,work"}, {" work , future"}, {"future", "work"}} {
		params["expand"] = values
		code, body := getRaw(t, ts.URL, matchPath(params))
		if code != http.StatusOK {
			t.Fatalf("expand=%q: status %d", values, code)
		}
		if _, has := rawResults(t, body)[0]["work"]; !has {
			t.Errorf("expand=%q inlined nothing: %s", values, body)
		}
	}
	// Expand finds nothing of its own, so it is no fact for the 400.
	if code, _ := getRaw(t, ts.URL, matchPath(url.Values{"expand": {"work"}})); code != http.StatusBadRequest {
		t.Errorf("expand alone: status %d, want 400", code)
	}
}

// TestMatchWithoutExpandIsByteIdentical pins the answer every shipped client reads
// to the members it had before expand existed: the body re-encoded through that
// shape is the body, so nothing was added, moved or reformatted.
func TestMatchWithoutExpandIsByteIdentical(t *testing.T) {
	_, ts := newTestServerForCatalog(t, matchCatalog())
	type before struct {
		workResult
		Score       int          `json:"score"`
		RecordingID string       `json:"recording_id,omitempty"`
		Reasons     matchReasons `json:"reasons"`
	}
	for _, params := range []url.Values{
		{"title": {"Sharpe's Eagle"}, "author": {"Bernard Cornwell"}, "runtime": {"39600"}, "limit": {"2"}},
		{"asin": {"B002SQ7KVE"}},
		{"q": {"dork diaries 6"}},
		{"title": {"Nothing Holds This Title"}},
	} {
		code, body := getRaw(t, ts.URL, matchPath(params))
		if code != http.StatusOK {
			t.Fatalf("%v: status %d", params, code)
		}
		var decoded struct {
			Results []before `json:"results"`
		}
		if err := json.Unmarshal(body, &decoded); err != nil {
			t.Fatal(err)
		}
		var again bytes.Buffer
		if err := json.NewEncoder(&again).Encode(map[string]any{"results": decoded.Results}); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(again.Bytes(), body) {
			t.Errorf("%v:\nsent %s\nwant %s", params, body, again.Bytes())
		}
	}
}

// TestMatchExpandFollowsTheClientsBest: a client takes the identifier's work as
// the match, and a run reads only that one, so that is the one inlined. It ranks
// first even beside a work whose title agrees better at the same score.
func TestMatchExpandFollowsTheClientsBest(t *testing.T) {
	_, ts := newTestServerForCatalog(t, matchCatalog())
	params := url.Values{"title": {"Sharpe's Battle"}, "author": {"Bernard Cornwell"}, "asin": {"B002SQ7KVE"}, "limit": {"2"}, "expand": {"work"}}
	res := matchJSON(t, ts.URL, params)
	if len(res) != 2 || res[0]["id"] != "sharpes-eagle" || res[1]["id"] != "sharpes-battle" || res[0]["score"] != res[1]["score"] {
		t.Fatalf("fixture: want the identifier's work ranked over the title's at one score, got %v", res)
	}
	if work, _ := res[0]["work"].(map[string]any); work == nil || work["id"] != "sharpes-eagle" {
		t.Errorf("the identifier's result carries %v", res[0]["work"])
	}
	if _, has := res[1]["work"]; has {
		t.Error("the title's work is inlined, not only the identifier's")
	}
}

// TestMatchExpandDegradesOutOfBudget: an expansion the budget or a read refuses
// leaves the answer exactly what it is without expand, never a failure, so the
// client fetches as it always did.
func TestMatchExpandDegradesOutOfBudget(t *testing.T) {
	db := openTestDB(t, artifacttest.Build(t, matchCatalog()))
	db.SetLogger(artifacttest.QuietLogger())
	req, _ := parseMatchRequest(url.Values{"title": {"Dune"}, "author": {"Frank Herbert"}})
	plain := func() []matchResult {
		results, err := db.match(t.Context(), req)
		if err != nil || len(results) == 0 {
			t.Fatalf("match = %v, %v", results, err)
		}
		return results
	}
	want, _ := json.Marshal(plain())

	spent, cancel := context.WithCancel(t.Context())
	cancel()
	results := plain()
	if db.expandBest(spent, results) {
		t.Error("an expansion past the budget reported whole")
	}
	if got, _ := json.Marshal(results); !bytes.Equal(got, want) {
		t.Errorf("past the budget:\n%s\nwant %s", got, want)
	}

	// A read that fails for its own reasons (the database gone) does the same.
	gone := openTestDB(t, artifacttest.Build(t, matchCatalog()))
	gone.SetLogger(artifacttest.QuietLogger())
	if err := gone.Close(); err != nil {
		t.Fatal(err)
	}
	results = plain()
	if gone.expandBest(t.Context(), results) {
		t.Error("an expansion after a failed read reported whole")
	}
	if got, _ := json.Marshal(results); !bytes.Equal(got, want) {
		t.Errorf("after a failed read:\n%s\nwant %s", got, want)
	}

	// A work the artifact doesn't hold is no expansion either.
	results = plain()
	results[0].ID = "no-such-work"
	if db.expandBest(t.Context(), results) {
		t.Error("an unknown work's expansion reported whole")
	}
	if results[0].Work != nil || results[0].SeriesFacts != nil {
		t.Errorf("an unknown work expanded to %v, %v", results[0].Work, results[0].SeriesFacts)
	}

	// Whole: the expansion read, and an empty answer with nothing to expand.
	if results = plain(); !db.expandBest(t.Context(), results) || results[0].Work == nil {
		t.Error("a successful expansion did not report whole")
	}
	if !db.expandBest(t.Context(), nil) {
		t.Error("an empty answer did not report whole")
	}
}

// TestMatchDegradedAnswerIsNotCached: a 200 whose expansion did not fit is sent
// no-store, so no cache repeats it and sends its clients to the fetches; a whole
// answer, and one that never asked for the expansion, state no Cache-Control and
// take metaserve's default.
func TestMatchDegradedAnswerIsNotCached(t *testing.T) {
	serve := func(db *DB, params url.Values) *httptest.ResponseRecorder {
		t.Helper()
		srv := newHandler(func() *DB { return db }, HandlerOptions{Logger: artifacttest.QuietLogger()})
		rec := httptest.NewRecorder()
		srv.mux.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, matchPath(params), nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d: %s", rec.Code, rec.Body)
		}
		return rec
	}
	expand := url.Values{"title": {"Sharpe's Eagle"}, "author": {"Bernard Cornwell"}, "expand": {"work"}}
	plain := url.Values{"title": {"Sharpe's Eagle"}, "author": {"Bernard Cornwell"}}

	// A work's document reads its genres and the match does not, so without the
	// table the match answers and its expansion fails, every time.
	broken := openTestDB(t, artifacttest.Altered(t, matchCatalog(), MaxSchemaVersion, "DROP TABLE work_genres"))
	broken.SetLogger(artifacttest.QuietLogger())
	rec := serve(broken, expand)
	if bytes.Contains(rec.Body.Bytes(), []byte(`"work":`)) {
		t.Fatalf("the expansion was inlined: %s", rec.Body)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("degraded answer Cache-Control = %q, want no-store", cc)
	}
	if cc := serve(broken, plain).Header().Get("Cache-Control"); cc != "" {
		t.Errorf("an answer that asked for no expansion: Cache-Control = %q, want none", cc)
	}

	rec = serve(openTestDB(t, artifacttest.Build(t, matchCatalog())), expand)
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"work":`)) {
		t.Fatalf("nothing inlined: %s", rec.Body)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "" {
		t.Errorf("whole answer Cache-Control = %q, want none", cc)
	}
}

// slotProbe is a ResponseWriter that notes how many match slots are held whenever the
// answer is written.
type slotProbe struct {
	http.ResponseWriter
	slots       chan struct{}
	wrote, held int
}

func (p *slotProbe) Write(b []byte) (int, error) {
	p.wrote += len(b)
	p.held = max(p.held, len(p.slots))
	return p.ResponseWriter.Write(b)
}

// TestMatchAnswerIsWrittenOutsideItsSlot: the slots bound the reads, so a client slow
// to take an expanded answer holds none of them while it does.
func TestMatchAnswerIsWrittenOutsideItsSlot(t *testing.T) {
	db := openTestDB(t, artifacttest.Build(t, matchCatalog()))
	srv := newHandler(func() *DB { return db }, HandlerOptions{Logger: artifacttest.QuietLogger()})
	params := url.Values{"title": {"Dune"}, "author": {"Frank Herbert"}, "expand": {"work"}}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, matchPath(params), nil)
	rec := httptest.NewRecorder()
	probe := &slotProbe{ResponseWriter: rec, slots: srv.matchSlots}
	srv.mux.ServeHTTP(probe, req)
	if rec.Code != http.StatusOK || probe.wrote == 0 {
		t.Fatalf("status %d, %d bytes written: %s", rec.Code, probe.wrote, rec.Body)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"work":`)) {
		t.Errorf("nothing inlined: %s", rec.Body)
	}
	if probe.held != 0 {
		t.Errorf("%d match slots held while the answer was written", probe.held)
	}
}

// TestMatchExpandUnderAnyBudget sweeps budgets from none to plenty, so the deadline
// lands in the match, between the two and among the expansion's reads: every answer
// is the match's 503, or a 200 with the expansion whole or absent. Never a 500, nor
// a work cut short by the reads the deadline stopped.
func TestMatchExpandUnderAnyBudget(t *testing.T) {
	db := openTestDB(t, artifacttest.Build(t, matchCatalog()))
	whole, err := db.WorkDetail("sharpes-eagle")
	if err != nil {
		t.Fatal(err)
	}
	wholeDoc, _ := json.Marshal(whole)
	params := url.Values{"title": {"Sharpe's Eagle"}, "author": {"Bernard Cornwell"}, "expand": {"work"}}
	statuses := map[int]int{}
	for budget := 50 * time.Microsecond; budget < 2*time.Second; budget = budget * 5 / 4 {
		srv := newHandler(func() *DB { return db }, HandlerOptions{MatchBudget: budget, Logger: artifacttest.QuietLogger()})
		code, body := getRaw(t, httptestServer(t, srv), matchPath(params))
		statuses[code]++
		switch code {
		case http.StatusServiceUnavailable:
		case http.StatusOK:
			best := rawResults(t, body)[0]
			work, hasWork := best["work"]
			_, hasFacts := best["series_facts"]
			if hasWork != hasFacts {
				t.Errorf("budget %v: half an expansion: %s", budget, body)
			}
			if hasWork && !bytes.Equal(work, wholeDoc) {
				t.Errorf("budget %v: a work cut short: %s", budget, work)
			}
		default:
			t.Errorf("budget %v: status %d: %s", budget, code, body)
		}
	}
	if statuses[http.StatusOK] == 0 {
		t.Errorf("no budget up to 2s answered: %v", statuses)
	}
}

// BenchmarkMatchExpansion is what expand adds to a request, the reads and the
// encoding of them: a work and its series' facts. Fixture only; the figure for the
// real artifact is in PACKAGES.md.
func BenchmarkMatchExpansion(b *testing.B) {
	db, err := Open(artifacttest.Build(b, matchCatalog()), "")
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	for b.Loop() {
		work, facts, err := db.matchExpansion(ctx, "sharpes-eagle")
		if err != nil {
			b.Fatal(err)
		}
		if err := json.NewEncoder(io.Discard).Encode(matchResult{Work: work, SeriesFacts: facts, workResult: workResult{WorkCard: &WorkCard{}}}); err != nil {
			b.Fatal(err)
		}
	}
}
