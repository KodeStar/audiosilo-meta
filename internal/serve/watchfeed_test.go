package serve

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

var watchFeedNow = time.Date(2026, 10, 20, 15, 30, 0, 0, time.UTC)

func watchFeedCatalog() *model.Catalog {
	cat := fixtureCatalog()
	for _, work := range cat.Works {
		switch work.ID {
		case "the-way-of-kings":
			work.Recordings[0].ReleaseDate = "2026-10-20"
		case "words-of-radiance":
			work.Recordings = []*model.Recording{{
				ID: "future-recording", Work: work.ID, Language: "en",
				ReleaseDate: "2026-11", License: "CC0-1.0",
			}}
		case "edgedancer":
			work.AddedAt = "2026-10-19T12:00:00Z"
		}
	}
	return cat
}

func newWatchFeedServer(t *testing.T, cat *model.Catalog) (*Server, *httptest.Server) {
	t.Helper()
	srv, err := New(Config{
		DBPath:    buildFixtureDB(t, cat),
		SiteURL:   testSiteURL,
		swapGrace: time.Minute,
		now:       func() time.Time { return watchFeedNow },
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return srv, ts
}

func watchFeedResponse(t *testing.T, ts *httptest.Server, path string) (*http.Response, []byte) {
	t.Helper()
	resp, err := http.Get(ts.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, body
}

func TestWatchFeedGoldens(t *testing.T) {
	_, ts := newWatchFeedServer(t, watchFeedCatalog())
	for _, tc := range []struct {
		name        string
		path        string
		contentType string
	}{
		{"atom", "/api/v1/watch/feed.atom?s=the-stormlight-archive,not-here", "application/atom+xml; charset=utf-8"},
		{"json", "/api/v1/watch/feed.json?s=the-stormlight-archive,not-here", "application/feed+json; charset=utf-8"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp, got := watchFeedResponse(t, ts, tc.path)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, body %s", resp.StatusCode, got)
			}
			if ct := resp.Header.Get("Content-Type"); ct != tc.contentType {
				t.Errorf("Content-Type = %q, want %q", ct, tc.contentType)
			}
			if got := resp.Header.Get("Cache-Control"); got != watchFeedMaxAge {
				t.Errorf("Cache-Control = %q", got)
			}
			if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "*" {
				t.Errorf("Access-Control-Allow-Origin = %q", got)
			}
			golden := filepath.Join("testdata", "golden", "watch-feed."+tc.name)
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("read %s: %v\nrendered body:\n%s", golden, err, got)
			}
			if string(got) != string(want) {
				t.Errorf("rendered feed differs from %s\nrendered body:\n%s", golden, got)
			}
		})
	}
}

func TestWatchItemWindowAndDatePrecision(t *testing.T) {
	now := time.Date(2026, 9, 21, 15, 0, 0, 0, time.UTC)
	card := func(id, release, added string) *workCard {
		var addedAt *string
		if added != "" {
			addedAt = &added
		}
		return &workCard{ID: id, Title: id, ReleaseDate: release, AddedAt: addedAt}
	}
	cases := []struct {
		name    string
		card    *workCard
		keep    bool
		state   string
		updated string
	}{
		{"year uses first day", card("year", "2026", ""), false, "", ""},
		{"future month uses first day", card("month", "2026-10", ""), true, "preorder", "2026-10-01T00:00:00Z"},
		{"today is released", card("today", "2026-09-21", ""), true, "released", "2026-09-21T00:00:00Z"},
		{"release boundary included", card("boundary", "2026-06-23", ""), true, "released", "2026-06-23T00:00:00Z"},
		{"release before boundary excluded", card("old", "2026-06-22", ""), false, "", ""},
		{"unknown exact boundary included", card("unknown", "", "2026-06-23T15:00:00Z"), true, "date unknown", "2026-06-23T15:00:00Z"},
		{"unknown before boundary excluded", card("older-unknown", "", "2026-06-23T14:59:59Z"), false, "", ""},
		{"unknown date boundary included", card("unknown-date", "", "2026-06-23"), true, "date unknown", "2026-06-23T00:00:00Z"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			item, ok := watchItem("Series", seriesEntry{Position: "1", Work: tc.card}, 90, now, testSiteURL)
			if ok != tc.keep {
				t.Fatalf("keep = %v, want %v", ok, tc.keep)
			}
			if !ok {
				return
			}
			if item.state != tc.state || item.updated.Format(time.RFC3339) != tc.updated {
				t.Errorf("item state/time = %q %s, want %q %s", item.state, item.updated.Format(time.RFC3339), tc.state, tc.updated)
			}
		})
	}
	item, ok := watchItem("Series", seriesEntry{Work: card("unpositioned", "2026-10", "")}, 90, now, testSiteURL)
	if !ok || item.title != "Series: unpositioned" {
		t.Errorf("unpositioned title = %q, keep %v", item.title, ok)
	}
}

func TestWatchFeedReportsUnknownAndResolvesRetiredSeries(t *testing.T) {
	_, ts := newWatchFeedServer(t, watchFeedCatalog())
	_, body := watchFeedResponse(t, ts, "/api/v1/watch/feed.json?s=stormlight-archive,missing-series")
	var feed jsonFeed
	if err := json.Unmarshal(body, &feed); err != nil {
		t.Fatal(err)
	}
	if feed.Description != "The Stormlight Archive. 1 slugs not found: missing-series" {
		t.Errorf("description = %q", feed.Description)
	}
	if len(feed.Items) != 3 {
		t.Fatalf("items = %d, want 3", len(feed.Items))
	}
	if !strings.HasPrefix(feed.Items[0].URL, testSiteURL+"/works/") {
		t.Errorf("work URL = %q", feed.Items[0].URL)
	}
}

func TestEmptyWatchFeedUsesNowAsUpdated(t *testing.T) {
	_, ts := newWatchFeedServer(t, watchFeedCatalog())
	_, body := watchFeedResponse(t, ts, "/api/v1/watch/feed.atom?s=missing-series")
	if !strings.Contains(string(body), "<updated>"+watchFeedNow.Format(time.RFC3339)+"</updated>") {
		t.Errorf("empty feed does not use now for updated:\n%s", body)
	}
	if strings.Contains(string(body), "<entry>") {
		t.Errorf("empty feed contains an entry:\n%s", body)
	}
}

func TestWatchFeedRejectsInvalidParameters(t *testing.T) {
	_, ts := newWatchFeedServer(t, watchFeedCatalog())
	tooMany := strings.TrimSuffix(strings.Repeat("the-stormlight-archive,", maxWatchSeries+1), ",")
	for _, path := range []string{
		"/api/v1/watch/feed.json",
		"/api/v1/watch/feed.json?s=z:not!base64",
		"/api/v1/watch/feed.json?s=z:anVuaw",
		"/api/v1/watch/feed.json?s=not_a_slug",
		"/api/v1/watch/feed.json?s=the-stormlight-archive&window=0",
		"/api/v1/watch/feed.json?s=the-stormlight-archive&window=366",
		"/api/v1/watch/feed.json?s=the-stormlight-archive&window=nope",
		"/api/v1/watch/feed.json?s=" + url.QueryEscape(tooMany),
	} {
		resp, body := watchFeedResponse(t, ts, path)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("GET %s status = %d, want 400; body %s", path, resp.StatusCode, body)
		}
		var out map[string]string
		if err := json.Unmarshal(body, &out); err != nil || out["error"] == "" {
			t.Errorf("GET %s body = %s, want {error}", path, body)
		}
	}
}

func TestWatchFeedETagAnd304(t *testing.T) {
	_, ts := newWatchFeedServer(t, watchFeedCatalog())
	path := "/api/v1/watch/feed.atom?s=the-stormlight-archive"
	resp, _ := watchFeedResponse(t, ts, path)
	etag := resp.Header.Get("ETag")
	if etag == "" {
		t.Fatal("feed has no ETag")
	}
	req, err := http.NewRequest(http.MethodGet, ts.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("If-None-Match", etag)
	conditional, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conditional.Body.Close() }()
	if conditional.StatusCode != http.StatusNotModified {
		t.Errorf("conditional status = %d, want 304", conditional.StatusCode)
	}
	if conditional.Header.Get("Cache-Control") != watchFeedMaxAge {
		t.Errorf("304 Cache-Control = %q", conditional.Header.Get("Cache-Control"))
	}

	other, _ := watchFeedResponse(t, ts, path+"&window=30")
	if other.Header.Get("ETag") == etag {
		t.Errorf("raw window parameter did not change ETag %q", etag)
	}
	jsonResp, _ := watchFeedResponse(t, ts, strings.Replace(path, ".atom", ".json", 1))
	if jsonResp.Header.Get("ETag") == etag {
		t.Errorf("Atom and JSON representations share ETag %q", etag)
	}
}

func TestWatchFeedCapsItemsAfterOrdering(t *testing.T) {
	author := &model.Person{ID: "author", Name: "Author", License: "CC0-1.0"}
	series := &model.Series{ID: "long-series", Name: "Long Series", License: "CC0-1.0"}
	cat := &model.Catalog{People: []*model.Person{author}, Series: []*model.Series{series}}
	for i := 1; i <= maxWatchFeedItems+5; i++ {
		id := "work-" + strconv.Itoa(i)
		cat.Works = append(cat.Works, &model.Work{
			ID: id, Title: "Work " + strconv.Itoa(i), Authors: []string{author.ID}, Language: "en", License: "CC0-1.0",
			Recordings: []*model.Recording{{
				ID: "recording", Work: id, Language: "en", ReleaseDate: "2027-01-01", License: "CC0-1.0",
			}},
		})
		series.Works = append(series.Works, model.SeriesWork{Work: id, Position: strconv.Itoa(i)})
	}
	_, ts := newWatchFeedServer(t, cat)
	_, body := watchFeedResponse(t, ts, "/api/v1/watch/feed.json?s=long-series")
	var feed jsonFeed
	if err := json.Unmarshal(body, &feed); err != nil {
		t.Fatal(err)
	}
	if len(feed.Items) != maxWatchFeedItems {
		t.Fatalf("items = %d, want %d", len(feed.Items), maxWatchFeedItems)
	}
	if !strings.Contains(feed.Items[0].Title, "#1:") || !strings.Contains(feed.Items[len(feed.Items)-1].Title, "#200:") {
		t.Errorf("cap applied before position ordering: first %q, last %q", feed.Items[0].Title, feed.Items[len(feed.Items)-1].Title)
	}
}

func TestWatchFeedsRequireSnapshot(t *testing.T) {
	srv := &Server{cfg: Config{SiteURL: testSiteURL, now: time.Now}}
	srv.mux = srv.buildMux()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/watch/feed.json?s=series", nil)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}
