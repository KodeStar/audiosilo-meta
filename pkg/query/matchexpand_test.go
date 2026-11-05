package query

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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

// getBody answers GET path with its status and exact bytes.
func getBody(t *testing.T, base, path string) (int, []byte) {
	t.Helper()
	resp, err := http.Get(base + path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, body
}

func matchPath(params url.Values) string { return "/api/v1/works/match?" + params.Encode() }

// TestMatchExpandIsTheWorkDocument: the best result's work is byte for byte what
// GET works/{id} serves, the runner-up carries nothing inlined, and the rest of the
// result is what it is without expand.
func TestMatchExpandIsTheWorkDocument(t *testing.T) {
	_, ts := newTestServerForCatalog(t, matchCatalog())
	params := url.Values{"title": {"Sharpe's Eagle"}, "author": {"Bernard Cornwell"}, "series": {"Richard Sharpe Novels"}, "limit": {"2"}}
	_, plain := getBody(t, ts.URL, matchPath(params))
	params.Set("expand", "work")
	code, body := getBody(t, ts.URL, matchPath(params))
	if code != http.StatusOK {
		t.Fatalf("status %d: %s", code, body)
	}
	results, before := rawResults(t, body), rawResults(t, plain)
	if len(results) != 2 || len(before) != 2 {
		t.Fatalf("results = %d and %d, want a best and a runner-up in each", len(results), len(before))
	}
	_, doc := getBody(t, ts.URL, "/api/v1/works/sharpes-eagle")
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
		if of, ok := record["translation_of"].([]any); ok {
			var ids []any
			for _, link := range of {
				ids = append(ids, link.(map[string]any)["id"])
			}
			want["translation_of"] = ids
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

// TestMatchExpandIsBounded: a long series adds its facts and none of its members,
// and a work listed in more series than any real one reads the facts of the first
// matchExpandSeriesMax alone.
func TestMatchExpandIsBounded(t *testing.T) {
	cat := matchCatalog()
	long := &model.Series{ID: "long-saga", Name: "Long Saga", License: "CC0-1.0"}
	listed := &model.Work{ID: "much-listed", Title: "Much Listed", Language: "en", Authors: []string{"dan-brown"}, License: "CC0-1.0"}
	cat.Works = append(cat.Works, listed)
	long.Works = append(long.Works, model.SeriesWork{Work: listed.ID, Position: "1"})
	for i := range 600 {
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
	code, body := getBody(t, ts.URL, matchPath(params))
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
	_, record := getBody(t, ts.URL, "/api/v1/series/long-saga")
	if len(results[0]["series_facts"]) >= len(record)/50 {
		t.Errorf("series_facts is %d bytes beside a %d-byte series record", len(results[0]["series_facts"]), len(record))
	}
}

// TestMatchExpandValues: an unknown value is ignored, so the answer is the plain
// one to the byte; the value is read from a list and from repeats.
func TestMatchExpandValues(t *testing.T) {
	_, ts := newTestServerForCatalog(t, matchCatalog())
	params := url.Values{"title": {"Dune"}, "author": {"Frank Herbert"}}
	_, plain := getBody(t, ts.URL, matchPath(params))
	for _, values := range [][]string{{"future"}, {""}, {"works"}, {"WORK"}, {"future,other", "another"}} {
		params["expand"] = values
		code, body := getBody(t, ts.URL, matchPath(params))
		if code != http.StatusOK || !bytes.Equal(body, plain) {
			t.Errorf("expand=%q: status %d, body differs from the plain answer:\n%s", values, code, body)
		}
	}
	for _, values := range [][]string{{"work"}, {"future,work"}, {" work , future"}, {"future", "work"}} {
		params["expand"] = values
		code, body := getBody(t, ts.URL, matchPath(params))
		if code != http.StatusOK {
			t.Fatalf("expand=%q: status %d", values, code)
		}
		if _, has := rawResults(t, body)[0]["work"]; !has {
			t.Errorf("expand=%q inlined nothing: %s", values, body)
		}
	}
	// Expand finds nothing of its own, so it is no fact for the 400.
	if code, _ := getBody(t, ts.URL, matchPath(url.Values{"expand": {"work"}})); code != http.StatusBadRequest {
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
		code, body := getBody(t, ts.URL, matchPath(params))
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

// TestMatchExpandFollowsTheClientsBest: a client takes the identifier's work first
// at one score, whatever the ranking's title tie-break says, and a run reads only
// that one, so that is the one inlined.
func TestMatchExpandFollowsTheClientsBest(t *testing.T) {
	_, ts := newTestServerForCatalog(t, matchCatalog())
	params := url.Values{"title": {"Sharpe's Battle"}, "author": {"Bernard Cornwell"}, "asin": {"B002SQ7KVE"}, "limit": {"2"}, "expand": {"work"}}
	res := matchJSON(t, ts.URL, params)
	if len(res) != 2 || res[0]["id"] != "sharpes-battle" || res[1]["id"] != "sharpes-eagle" || res[0]["score"] != res[1]["score"] {
		t.Fatalf("fixture: want the title's work ranked over the identifier's at one score, got %v", res)
	}
	if _, has := res[0]["work"]; has {
		t.Error("the title's work is inlined, not the identifier's")
	}
	if work, _ := res[1]["work"].(map[string]any); work == nil || work["id"] != "sharpes-eagle" {
		t.Errorf("the identifier's result carries %v", res[1]["work"])
	}
	// Below the top score an identifier cannot be: it always scores 100.
	for _, tc := range []struct {
		name    string
		results []matchResult
		want    int
	}{
		{"no identifier", []matchResult{{Score: 90}, {Score: 90}}, 0},
		{"identifier first", []matchResult{{Score: 100, Reasons: matchReasons{Identifier: "asin"}}, {Score: 100}}, 0},
		{"identifier in the tie", []matchResult{{Score: 100}, {Score: 100}, {Score: 100, Reasons: matchReasons{Identifier: "isbn"}}}, 2},
		{"a lower score ends it", []matchResult{{Score: 100}, {Score: 99}, {Score: 99, Reasons: matchReasons{Identifier: "asin"}}}, 0},
	} {
		if got := bestResult(tc.results); got != tc.want {
			t.Errorf("%s: bestResult = %d, want %d", tc.name, got, tc.want)
		}
	}
}

// TestMatchExpandDegradesOutOfBudget: an expansion the budget or a read refuses
// leaves the answer exactly what it is without expand, never a failure, so the
// client fetches as it always did.
func TestMatchExpandDegradesOutOfBudget(t *testing.T) {
	db := openTestDB(t, artifacttest.Build(t, matchCatalog()))
	srv := newHandler(func() *DB { return db }, HandlerOptions{Logger: artifacttest.QuietLogger()})
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
	srv.expandBest(spent, db, results)
	if got, _ := json.Marshal(results); !bytes.Equal(got, want) {
		t.Errorf("past the budget:\n%s\nwant %s", got, want)
	}

	// A read that fails for its own reasons (the database gone) does the same.
	gone := openTestDB(t, artifacttest.Build(t, matchCatalog()))
	if err := gone.Close(); err != nil {
		t.Fatal(err)
	}
	results = plain()
	srv.expandBest(t.Context(), gone, results)
	if got, _ := json.Marshal(results); !bytes.Equal(got, want) {
		t.Errorf("after a failed read:\n%s\nwant %s", got, want)
	}

	// A work the artifact doesn't hold is no expansion either.
	results = plain()
	results[0].ID = "no-such-work"
	srv.expandBest(t.Context(), db, results)
	if results[0].Work != nil || results[0].SeriesFacts != nil {
		t.Errorf("an unknown work expanded to %v, %v", results[0].Work, results[0].SeriesFacts)
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
		code, body := getBody(t, httptestServer(t, srv), matchPath(params))
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
