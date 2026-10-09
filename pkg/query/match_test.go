package query

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// matchCatalog is a fixture shaped like the real records the endpoint was built
// for (titles, series, positions and runtimes copied from the catalogue), plus
// the decoys that make each case a real choice: two Cornwells, a same-titled
// "Inferno" by another author, the Dork Diaries' duplicate generic record.
func matchCatalog() *model.Catalog {
	person := func(id, name string) *model.Person {
		return &model.Person{ID: id, Name: name, License: "CC0-1.0"}
	}
	work := func(id, title, author string, runtime int) *model.Work {
		w := &model.Work{ID: id, Title: title, Language: "en", Authors: []string{author}, License: "CC0-1.0"}
		if runtime > 0 {
			w.Recordings = []*model.Recording{{
				ID: id + "-rec", Work: id, Language: "en", RuntimeMin: runtime, License: "CC0-1.0",
				Narrators: []string{"rupert-farley"},
			}}
		}
		return w
	}
	series := func(id, name string, members ...string) *model.Series {
		s := &model.Series{ID: id, Name: name, License: "CC0-1.0"}
		for i := 0; i < len(members); i += 2 {
			s.Works = append(s.Works, model.SeriesWork{Work: members[i], Position: members[i+1]})
		}
		return s
	}
	eagle := work("sharpes-eagle", "Sharpe’s Eagle", "bernard-cornwell", 660)
	eagle.Recordings[0].ASIN = []model.ASIN{{Region: "us", ASIN: "B002SQ7KVE"}}
	return &model.Catalog{
		People: []*model.Person{
			person("bernard-cornwell", "Bernard Cornwell"), person("patricia-cornwell", "Patricia Cornwell"),
			person("j-n-chaney", "J.N. Chaney"), person("rachel-renee-russell", "Rachel Renée Russell"),
			person("travis-bagwell", "Travis Bagwell"), person("dan-brown", "Dan Brown"),
			person("frank-herbert", "Frank Herbert"), person("brian-herbert", "Brian Herbert"),
			person("rupert-farley", "Rupert Farley"),
		},
		Works: []*model.Work{
			eagle,
			work("sharpes-battle", "Sharpe’s Battle", "bernard-cornwell", 700),
			work("sharpes-siege", "Sharpe’s Siege", "bernard-cornwell", 610),
			work("sharpes-revenge", "Sharpe’s Revenge", "bernard-cornwell", 640),
			work("postmortem", "Postmortem", "patricia-cornwell", 620),
			work("a-broken-alliance", "A Broken Alliance", "j-n-chaney", 555),
			work("an-alliance-reforged", "An Alliance Reforged", "j-n-chaney", 540),
			work("crush-catastrophe", "Crush Catastrophe", "rachel-renee-russell", 198),
			work("dork-diaries-12", "Dork Diaries 12", "rachel-renee-russell", 198),
			work("dork-diaries-6", "Dork Diaries 6", "rachel-renee-russell", 228),
			work("ice-princess", "Tales from a Not-So-Graceful Ice Princess", "rachel-renee-russell", 218),
			work("puppy-love", "Puppy Love: Dork Diaries, Book 10", "rachel-renee-russell", 206),
			work("awaken-online-inferno", "Awaken Online: Inferno", "travis-bagwell", 1404),
			work("inferno-dan-brown", "Inferno", "dan-brown", 1100),
			work("dune", "Dune", "frank-herbert", 1262),
			work("hunters-of-dune", "Hunters of Dune", "brian-herbert", 1000),
		},
		Series: []*model.Series{
			series("richard-sharpe-novels", "Richard Sharpe Novels",
				"sharpes-eagle", "8", "sharpes-battle", "12", "sharpes-siege", "19", "sharpes-revenge", "20"),
			series("sentenced-to-war", "Sentenced to War", "a-broken-alliance", "5", "an-alliance-reforged", "6"),
			series("dork-diaries", "Dork Diaries", "ice-princess", "4", "dork-diaries-6", "6", "puppy-love", "10", "crush-catastrophe", "12"),
			series("awaken-online-tarot", "Awaken Online: Tarot", "awaken-online-inferno", "3"),
		},
	}
}

// matchJSON runs one works/match request and returns its results.
func matchJSON(t *testing.T, base string, params url.Values) []map[string]any {
	t.Helper()
	code, body := getJSON(t, base, "/api/v1/works/match?"+params.Encode())
	if code != http.StatusOK {
		t.Fatalf("works/match?%s: status %d, body %v", params.Encode(), code, body)
	}
	raw, _ := body["results"].([]any)
	out := make([]map[string]any, len(raw))
	for i, r := range raw {
		out[i] = r.(map[string]any)
	}
	return out
}

// TestMatchExamples are the cases the endpoint exists for: each names the book
// a real library's path and tags described, and what must rank first.
func TestMatchExamples(t *testing.T) {
	_, ts := newTestServerForCatalog(t, matchCatalog())
	for _, tc := range []struct {
		name     string
		params   url.Values
		want     string
		minScore float64
		// below, when set, must rank after want.
		below string
	}{
		{
			name: "garbage tags, good path",
			params: url.Values{
				"title":  {"Bernard Cornwell", "Sharpe's Eagle"},
				"author": {"Sharpe's Eagle (Sharpe 08)", "Bernard Cornwell"},
				"series": {"Richard Sharpe"}, "position": {"08"}, "runtime": {"39700"},
			},
			want: "sharpes-eagle", minScore: 95,
		},
		{
			name: "the path's numbering alone",
			params: url.Values{
				"title":  {"Bernard Cornwell"},
				"author": {"Bernard Cornwell"}, "series": {"Richard Sharpe"}, "position": {"8"},
			},
			want: "sharpes-eagle", minScore: 90, below: "sharpes-battle",
		},
		{
			name: "a named title beats the numbering",
			params: url.Values{
				"title": {"Sharpe's Siege"}, "author": {"Bernard Cornwell"}, "series": {"Richard Sharpe"}, "position": {"20"},
			},
			want: "sharpes-siege", minScore: 80, below: "sharpes-revenge",
		},
		{
			name: "a typo in the folder name",
			params: url.Values{
				"title": {"SW06 - An Alliance Reformed"}, "author": {"J.N. Chaney"}, "series": {"Sentenced to War"}, "position": {"6"},
			},
			want: "an-alliance-reforged", minScore: 90,
		},
		{
			name: "a long marketing title over a short stored one",
			params: url.Values{
				"title":  {"Tales from a Not-So-Secret Crush Catastrophe"},
				"author": {"Rachel Renee Russell"}, "series": {"Dork Diaries"}, "position": {"12"}, "runtime": {"11880"},
			},
			want: "crush-catastrophe", minScore: 90, below: "dork-diaries-12",
		},
		{
			name: "a generic stored title over a sibling sharing the naming pattern",
			params: url.Values{
				"title":  {"Tales from a Not-So-Happy Heartbreaker"},
				"author": {"Rachel Renee Russell"}, "series": {"Dork Diaries"}, "position": {"6"},
			},
			want: "dork-diaries-6", minScore: 80, below: "ice-princess",
		},
		{
			// The sibling shares five words of the naming pattern; the volume at
			// the path's number shares none, and still wins.
			name: "the numbered volume over a sibling sharing the naming pattern",
			params: url.Values{
				"title":  {"Tales from a Not-So-Perfect Pet Sitter"},
				"author": {"Rachel Renee Russell"}, "series": {"Dork Diaries"}, "position": {"10"},
			},
			want: "puppy-love", below: "ice-princess",
		},
		{
			// A misspelt author: the FTS page (every word) finds nothing, and
			// the exact-title probe of the text's leading run does.
			name:   "typed text with a misspelt author",
			params: url.Values{"q": {"dune herbret"}},
			want:   "dune",
		},
		{
			name: "the right author's book over a same-titled one",
			params: url.Values{
				"title": {"T03 - Inferno"}, "author": {"Travis Bagwell"}, "series": {"Tarot"}, "position": {"3"}, "runtime": {"84240"},
			},
			want: "awaken-online-inferno", minScore: 90, below: "inferno-dan-brown",
		},
		{
			name:   "typed text with the author after the title",
			params: url.Values{"q": {"dune herbert"}},
			want:   "dune", minScore: 100, below: "hunters-of-dune",
		},
		{
			name:   "typed text naming the series and the volume",
			params: url.Values{"q": {"sharpe 8"}},
			want:   "sharpes-eagle", minScore: 100, below: "sharpes-battle",
		},
		{
			name:   "an identifier",
			params: url.Values{"asin": {"B002SQ7KVE"}, "title": {"Something Else Entirely"}},
			want:   "sharpes-eagle", minScore: 100,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := matchJSON(t, ts.URL, tc.params)
			if len(res) == 0 {
				t.Fatal("no results")
			}
			if res[0]["id"] != tc.want {
				t.Fatalf("top = %v (%v), want %s; results %v", res[0]["id"], res[0]["score"], tc.want, matchIDs(res))
			}
			if s := res[0]["score"].(float64); s < tc.minScore {
				t.Errorf("score = %v, want at least %v (reasons %v)", s, tc.minScore, res[0]["reasons"])
			}
			if tc.below != "" && !ranksAfter(res, tc.want, tc.below) {
				t.Errorf("%s does not rank below %s: %v", tc.below, tc.want, matchIDs(res))
			}
		})
	}
}

func matchIDs(res []map[string]any) []any {
	out := make([]any, len(res))
	for i, r := range res {
		out[i] = r["id"]
	}
	return out
}

// ranksAfter reports whether b is absent or after a in res.
func ranksAfter(res []map[string]any, a, b string) bool {
	for _, r := range res {
		switch r["id"] {
		case a:
			return true
		case b:
			return false
		}
	}
	return false
}

// TestMatchAnotherAuthorsTitleIsNotConfident: a work whose title is exactly the
// file's but which none of the named authors wrote is a weak candidate.
func TestMatchAnotherAuthorsTitleIsNotConfident(t *testing.T) {
	_, ts := newTestServerForCatalog(t, matchCatalog())
	for _, r := range matchJSON(t, ts.URL, url.Values{"title": {"Inferno"}, "author": {"Travis Bagwell"}}) {
		if r["id"] == "inferno-dan-brown" && r["score"].(float64) > 60 {
			t.Errorf("Dan Brown's Inferno scored %v for a Travis Bagwell file (reasons %v)", r["score"], r["reasons"])
		}
	}
}

// TestMatchProbesTheTitleAsWritten: a title guess is probed as written as well
// as folded. The folded terms read "&" as "and", a word a stored "Pride &
// Prejudice" does not hold, so probing only them found nothing at all.
func TestMatchProbesTheTitleAsWritten(t *testing.T) {
	cat := matchCatalog()
	cat.Works = append(cat.Works, &model.Work{
		ID: "pride-and-prejudice", Title: "Pride & Prejudice", Language: "en",
		Authors: []string{"dan-brown"}, License: "CC0-1.0",
	})
	_, ts := newTestServerForCatalog(t, cat)
	if res := matchJSON(t, ts.URL, url.Values{"title": {"Pride & Prejudice"}}); len(res) == 0 || res[0]["id"] != "pride-and-prejudice" {
		t.Fatalf("results = %v, want pride-and-prejudice first", matchIDs(res))
	}
}

// TestMatchIdentifierFallsBackToTheISBN: an ASIN the catalogue does not hold
// must not hide an ISBN it does - lookup reads one identifier, so each is
// asked on its own.
func TestMatchIdentifierFallsBackToTheISBN(t *testing.T) {
	cat := matchCatalog()
	cat.Works[1].Recordings[0].ISBN = []model.ISBNRef{{ISBN: "9780575082441"}} // sharpes-battle
	_, ts := newTestServerForCatalog(t, cat)
	res := matchJSON(t, ts.URL, url.Values{"asin": {"B000NOTHERE"}, "isbn": {"978-0-575-08244-1"}})
	if len(res) == 0 || res[0]["id"] != "sharpes-battle" {
		t.Fatalf("results = %v, want sharpes-battle", matchIDs(res))
	}
	if r := res[0]["reasons"].(map[string]any); r["identifier"] != "isbn" {
		t.Errorf("identifier reason = %v, want isbn", r["identifier"])
	}
}

// TestMatchAnAuthorAloneIsNotAMatch: every book by a named author is a
// candidate, but without a title or a position nothing can say WHICH, so none
// may read as a confident match.
func TestMatchAnAuthorAloneIsNotAMatch(t *testing.T) {
	_, ts := newTestServerForCatalog(t, matchCatalog())
	res := matchJSON(t, ts.URL, url.Values{"author": {"Bernard Cornwell"}})
	if len(res) < 4 {
		t.Fatalf("results = %v, want every Cornwell work", matchIDs(res))
	}
	for _, r := range res {
		if r["score"].(float64) > 40 {
			t.Errorf("%v scored %v on the author alone", r["id"], r["score"])
		}
	}
}

// TestMatchFreeTextAndFactsBothCount: typed text and structured facts are two
// sets of evidence, and the better one scores each candidate, so an admin who
// types another book's title still sees it beside the path's answer.
func TestMatchFreeTextAndFactsBothCount(t *testing.T) {
	_, ts := newTestServerForCatalog(t, matchCatalog())
	res := matchJSON(t, ts.URL, url.Values{
		"q": {"dune"}, "title": {"Sharpe's Eagle"}, "author": {"Bernard Cornwell"},
		"series": {"Richard Sharpe"}, "position": {"8"},
	})
	got := map[any]float64{}
	for _, r := range res {
		got[r["id"]] = r["score"].(float64)
	}
	if got["sharpes-eagle"] < 95 || got["dune"] < 95 {
		t.Fatalf("scores = %v, want both the facts' and the text's answer near 100", got)
	}
}

// TestMatchSeriesHypotheses: series is repeatable, so a client sends the
// path's series folder and the tagged series without choosing. A "Fiction/
// Bernard Cornwell/..." layout makes the author's folder the path's series
// guess; the tag's series still agrees, at the path's position, in either
// order, and the wrong guess alone agrees with nothing.
func TestMatchSeriesHypotheses(t *testing.T) {
	_, ts := newTestServerForCatalog(t, matchCatalog())
	for _, series := range [][]string{{"Bernard Cornwell", "Richard Sharpe"}, {"Richard Sharpe", "Bernard Cornwell"}} {
		res := matchJSON(t, ts.URL, url.Values{
			"title": {"Bernard Cornwell"}, "author": {"Bernard Cornwell"}, "series": series, "position": {"8"},
		})
		if len(res) == 0 || res[0]["id"] != "sharpes-eagle" || res[0]["score"].(float64) < 90 {
			t.Fatalf("series %q: results %v, want sharpes-eagle first at 90 or more", series, matchIDs(res))
		}
		if got := res[0]["reasons"].(map[string]any)["series"]; got != "position" {
			t.Errorf("series %q: reasons.series = %v, want position", series, got)
		}
	}
	res := matchJSON(t, ts.URL, url.Values{
		"title": {"Bernard Cornwell"}, "author": {"Bernard Cornwell"}, "series": {"Bernard Cornwell"}, "position": {"8"},
	})
	for _, r := range res {
		if got := r["reasons"].(map[string]any)["series"]; got != "none" {
			t.Errorf("the wrong guess alone: %v has reasons.series %v, want none", r["id"], got)
		}
	}
}

// TestMatchResponseShape pins the wire shape: a search WorkResult plus score,
// recording_id and reasons, with every reason the request let be judged.
func TestMatchResponseShape(t *testing.T) {
	_, ts := newTestServerForCatalog(t, matchCatalog())
	res := matchJSON(t, ts.URL, url.Values{
		"title": {"Sharpe's Eagle"}, "author": {"Bernard Cornwell"}, "series": {"Richard Sharpe"},
		"position": {"8"}, "runtime": {"39600"}, "q": {"sharpes eagle"}, "limit": {"2"},
	})
	if len(res) != 2 {
		t.Fatalf("limit=2 returned %d results", len(res))
	}
	top := res[0]
	for _, field := range []string{"kind", "id", "title", "authors", "language", "narrators", "score", "reasons"} {
		if _, ok := top[field]; !ok {
			t.Errorf("result lacks %q: %v", field, top)
		}
	}
	if top["kind"] != "work" || top["recording_id"] != "sharpes-eagle-rec" || top["score"].(float64) != 100 {
		t.Errorf("top = %v", top)
	}
	reasons := top["reasons"].(map[string]any)
	want := map[string]any{"title": 1.0, "text": 1.0, "author": "full", "series": "position", "runtime": 0.0}
	for k, v := range want {
		if reasons[k] != v {
			t.Errorf("reasons[%s] = %v, want %v (all: %v)", k, reasons[k], v, reasons)
		}
	}
	if _, ok := reasons["identifier"]; ok {
		t.Errorf("identifier reason without an identifier: %v", reasons)
	}

	// A request that states no series and no runtime omits those reasons.
	res = matchJSON(t, ts.URL, url.Values{"title": {"Dune"}})
	reasons = res[0]["reasons"].(map[string]any)
	for _, k := range []string{"series", "runtime", "author", "text", "identifier"} {
		if _, ok := reasons[k]; ok {
			t.Errorf("reason %q present although the request did not state it: %v", k, reasons)
		}
	}
}

// TestMatchRequiresAFact: with nothing to match on the request is a 400 - a
// position or runtime alone identifies nothing.
func TestMatchRequiresAFact(t *testing.T) {
	_, ts := newTestServerForCatalog(t, matchCatalog())
	for _, q := range []string{"", "q=+", "title=&author=", "position=8", "runtime=3600", "limit=5"} {
		if code, body := getJSON(t, ts.URL, "/api/v1/works/match?"+q); code != http.StatusBadRequest || body["error"] == "" {
			t.Errorf("?%s: status %d %v, want a 400", q, code, body)
		}
	}
}

// TestParseMatchRequestBounds pins every input bound: the endpoint is public, so
// no parameter may let a request price itself.
func TestParseMatchRequestBounds(t *testing.T) {
	long := strings.Repeat("word ", 200)
	q := url.Values{
		"title":    {"a", "b", "c", "d", "e", "f", long},
		"author":   {"A", "A", "B", "C", "D", "E"},
		"q":        {long},
		"series":   {long, "B", "C"},
		"position": {"not-a-position"},
		"runtime":  {"-5"},
		"asin":     {strings.Repeat("B", 40)},
		"isbn":     {"978-1-4272-0143-0"},
		"limit":    {"500"},
	}
	r, ok := parseMatchRequest(q)
	if !ok {
		t.Fatal("parseMatchRequest refused a request with titles")
	}
	if len(r.titles) != matchHypothesesMax || len(r.authors) != matchHypothesesMax {
		t.Errorf("titles %d, authors %d, want %d each", len(r.titles), len(r.authors), matchHypothesesMax)
	}
	if strings.Join(r.authors, ",") != "A,B,C,D" {
		t.Errorf("authors = %q, want the first four distinct", r.authors)
	}
	if len(r.series) != matchSeriesMax || len(r.text) > maxQueryBytes || len(r.series[0]) > maxQueryBytes {
		t.Errorf("series %d (first %d bytes), text %d bytes; want %d series, at most %d bytes each", len(r.series), len(r.series[0]), len(r.text), matchSeriesMax, maxQueryBytes)
	}
	if r.position != "" || r.runtime != 0 || r.asin != "" {
		t.Errorf("position %q, runtime %d, asin %q: garbage must be ignored", r.position, r.runtime, r.asin)
	}
	if r.isbn != "9781427201430" || r.limit != matchLimitMax {
		t.Errorf("isbn %q, limit %d", r.isbn, r.limit)
	}
	if r, _ := parseMatchRequest(url.Values{"title": {"x"}, "position": {"#08"}, "runtime": {"3600"}}); r.position != "08" || r.runtime != 3600 || r.limit != matchLimitDefault {
		t.Errorf("valid position/runtime/limit read as %q / %d / %d", r.position, r.runtime, r.limit)
	}
}

// TestMatchBudget: a match that cannot get a slot inside its budget is a 503
// with Retry-After, never a request that waits indefinitely.
func TestMatchBudget(t *testing.T) {
	db := openTestDB(t, buildFixtureDB(t, matchCatalog()))
	srv := newHandler(func() *DB { return db }, HandlerOptions{MatchBudget: 20 * time.Millisecond, Logger: testLogger()})
	for range cap(srv.matchSlots) {
		srv.matchSlots <- struct{}{}
	}
	ts := httptestServer(t, srv)
	resp, err := http.Get(ts + "/api/v1/works/match?title=Dune")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable || resp.Header.Get("Retry-After") == "" {
		t.Fatalf("status %d, Retry-After %q; want 503 with Retry-After", resp.StatusCode, resp.Header.Get("Retry-After"))
	}
}

// TestMatchStopsOnACancelledContext: the work stops when the request's context
// does (a client gone, the budget spent), and says so as errMatchBusy.
func TestMatchStopsOnACancelledContext(t *testing.T) {
	snap := snapshotFor(t, matchCatalog())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := snap.match(ctx, matchRequest{titles: []string{"Sharpe's Eagle"}, authors: []string{"Bernard Cornwell"}, limit: 5})
	if !errors.Is(err, errMatchBusy) {
		t.Fatalf("err = %v, want errMatchBusy", err)
	}
}

// TestMatchReadsHonourTheContext: every read a match makes, the ones it shares
// with search and lookup included, runs under the match's context, so a spent
// budget refuses the next query outright instead of letting it run to the end
// (an FTS walk of a common word, a 2,500-work fact batch) while it holds one of
// the matchConcurrency slots. Each read is run live first, so a read that
// refused for some other reason cannot pass.
func TestMatchReadsHonourTheContext(t *testing.T) {
	snap := snapshotFor(t, matchCatalog())
	pool := newCandidatePool()
	pool.add("dune", "sharpes-eagle")
	reads := map[string]func(context.Context) error{
		"lookupIDs": func(ctx context.Context) error {
			_, _, _, err := snap.lookupIDs(ctx, "B002SQ7KVE", "")
			return err
		},
		"ftsHits": func(ctx context.Context) error {
			_, err := snap.ftsHits(ctx, kindWork, ftsQuery("sharpe"), matchTextWindow, nil)
			return err
		},
		"exactTitleHits": func(ctx context.Context) error {
			_, err := snap.exactTitleHits(ctx, "Dune", nil)
			return err
		},
		"seriesPositionHits": func(ctx context.Context) error {
			_, err := snap.seriesPositionHits(ctx, "dork diaries 6", nil)
			return err
		},
		"seriesMembers": func(ctx context.Context) error {
			_, err := snap.seriesMembers(ctx, "Dork Diaries", matchCandidatesMax)
			return err
		},
		"peopleNamed": func(ctx context.Context) error {
			_, err := snap.peopleNamed(ctx, splitAuthors("Bernard Cornwell")[0])
			return err
		},
		"candidateFacts": func(ctx context.Context) error {
			_, err := snap.candidateFacts(ctx, pool)
			return err
		},
		"workResults": func(ctx context.Context) error {
			_, err := snap.workResults(ctx, []string{"dune"})
			return err
		},
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	for name, read := range reads {
		if err := read(t.Context()); err != nil {
			t.Errorf("%s with a live context: %v", name, err)
		}
		if err := read(cancelled); !errors.Is(err, context.Canceled) {
			t.Errorf("%s with a cancelled context: err = %v, want context.Canceled", name, err)
		}
	}
	// The boosts degrade to none rather than failing; the next read then fails.
	if got := snap.boostedWorks(cancelled, "dork diaries 6", nil); got != nil {
		t.Errorf("boostedWorks with a cancelled context = %v, want none", got)
	}
}

// TestMatchOverBudgetIsA503: a request whose budget is already spent is the
// match-budget 503 whichever way the slot race goes - refused before a slot,
// or failing its first read - and never a 200 or a 500.
func TestMatchOverBudgetIsA503(t *testing.T) {
	db := openTestDB(t, buildFixtureDB(t, matchCatalog()))
	srv := NewHandler(func() *DB { return db }, HandlerOptions{MatchBudget: PublicMatchBudget, Logger: testLogger()})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	params := url.Values{"q": {"sharpe eagle"}, "title": {"Sharpe's Eagle"}, "author": {"Bernard Cornwell"}, "series": {"Sharpe"}, "asin": {"B002SQ7KVE"}}
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/v1/works/match?"+params.Encode(), nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") == "" || !strings.Contains(rec.Body.String(), "did not finish in time") {
		t.Fatalf("status %d, Retry-After %q, body %s; want the match-budget 503", rec.Code, rec.Header().Get("Retry-After"), rec.Body.String())
	}
}

// TestMatchIsBoundedByTheCandidateCap: the author probe stops at the pool's
// cap however many works the named people wrote.
func TestMatchIsBoundedByTheCandidateCap(t *testing.T) {
	snap := snapshotFor(t, matchCatalog())
	ids, err := snap.idsByChunk(context.Background(), []string{"bernard-cornwell", "patricia-cornwell"}, worksByPeopleSQL, 2)
	if err != nil || len(ids) != 2 {
		t.Fatalf("works by people with limit 2 = %v, %v", ids, err)
	}
	pool := newCandidatePool()
	for i := range matchCandidatesMax + 10 {
		pool.add("w" + strconv.Itoa(i))
	}
	if pool.len() > matchCandidatesMax {
		t.Fatalf("pool holds %d, cap %d", pool.len(), matchCandidatesMax)
	}
}

// httptestServer serves srv's routes for the life of the test.
func httptestServer(t *testing.T, srv *handler) string {
	t.Helper()
	ts := httptest.NewServer(srv.serveMux())
	t.Cleanup(ts.Close)
	return ts.URL
}

// TestReadFactsPosition: the request's position wins; without one, the number
// the first title guess's numbering carried is the position, and a guess that
// is the series' name and a number is position evidence rather than a title.
func TestReadFactsPosition(t *testing.T) {
	for _, tc := range []struct {
		req        matchRequest
		want       string
		wantTitles []string
	}{
		{matchRequest{titles: []string{"Sharpe - 08 - Sharpe's Eagle"}}, "08", []string{"sharpe 08 sharpes eagle", "sharpes eagle"}},
		{matchRequest{titles: []string{"Sharpe - 08 - Sharpe's Eagle"}, position: "9"}, "9", nil},
		{matchRequest{titles: []string{"Dune", "02 - Dune Messiah"}}, "02", nil},
		{matchRequest{titles: []string{"The Primal Hunter 8"}, series: []string{"Primal Hunter"}}, "8", []string{}},
		{matchRequest{titles: []string{"The Primal Hunter"}, series: []string{"Primal Hunter"}}, "", []string{"the primal hunter"}},
	} {
		f := readFacts(tc.req)
		var titles []string
		for _, g := range f.titles {
			titles = append(titles, g.joined)
		}
		if f.position != tc.want || (tc.wantTitles != nil && strings.Join(titles, "|") != strings.Join(tc.wantTitles, "|")) {
			t.Errorf("readFacts(%+v): position %q, titles %q; want %q, %q", tc.req, f.position, titles, tc.want, tc.wantTitles)
		}
	}
	// A series folder's own numbering is not the series' name.
	if f := readFacts(matchRequest{series: []string{"03 - Tawny Man"}}); !slices.Equal(f.seriesNames, []string{"Tawny Man"}) {
		t.Errorf("seriesNames = %q, want [Tawny Man]", f.seriesNames)
	}
}

// TestTextFacts: typed text becomes the facts it states about each candidate.
func TestTextFacts(t *testing.T) {
	eagle := &candidate{
		authors:    []personName{{surname: "cornwell", given: []string{"bernard"}}},
		authorWord: map[string]bool{"bernard": true, "cornwell": true},
		series:     []candidateSeries{{name: ptr(newTermForm(matchTerms("Richard Sharpe Novels"))), position: "8"}},
	}
	f := readFacts(matchRequest{text: "cornwell sharpe book 8"}).textFacts(eagle)
	if !f.namesVolume || f.position != "8" || len(f.series) != 1 || f.series[0].joined != "sharpe" || len(f.authors) != 1 || len(f.titles) != 0 {
		t.Fatalf("volume facts = %+v", f)
	}
	// Another volume number is not this work's volume: the text stays a title.
	f = readFacts(matchRequest{text: "sharpe 9"}).textFacts(eagle)
	if f.namesVolume || len(f.titles) == 0 {
		t.Fatalf("other volume facts = %+v", f)
	}
	f = readFacts(matchRequest{text: "sharpes eagle bernard cornwell"}).textFacts(eagle)
	var titles []string
	for _, g := range f.titles {
		titles = append(titles, g.joined)
	}
	if want := "sharpes eagle bernard cornwell|sharpes eagle"; strings.Join(titles, "|") != want || len(f.authors) != 1 {
		t.Fatalf("title facts: titles %q authors %v; want %q", titles, f.authors, want)
	}
}

func ptr[T any](v T) *T { return &v }

// TestPeopleNamed: a full name resolves by every word; a surname alone keeps
// the bearers whose first name or initial agrees, else every bearer.
func TestPeopleNamed(t *testing.T) {
	snap := snapshotFor(t, matchCatalog())
	for in, want := range map[string]string{
		"Bernard Cornwell": "bernard-cornwell",
		"B. Cornwell":      "bernard-cornwell",
		"Cornwell":         "bernard-cornwell,patricia-cornwell",
		"Xavier Cornwell":  "bernard-cornwell,patricia-cornwell",
	} {
		names := splitAuthors(in)
		ids, err := snap.peopleNamed(context.Background(), names[0])
		slices.Sort(ids)
		if err != nil || strings.Join(ids, ",") != want {
			t.Errorf("peopleNamed(%q) = %v, %v; want %s", in, ids, err, want)
		}
	}
}
