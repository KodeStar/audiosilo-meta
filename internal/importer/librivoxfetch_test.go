package importer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeFeed is an offline LibriVox audiobooks feed over the fixture projects: it
// pages them by limit/offset as the real feed does, answers past the end with
// the feed's own "no results" 404, and can be told to fail a request first.
type fakeFeed struct {
	t     *testing.T
	books []json.RawMessage

	mu      sync.Mutex
	offsets []int
	// failures are status codes (with an optional Retry-After) served, in
	// order, before the feed answers normally again.
	failures []fakeFailure
}

type fakeFailure struct {
	status     int
	retryAfter string
}

func newFakeFeed(t *testing.T) *fakeFeed {
	t.Helper()
	feed := &fakeFeed{t: t}
	entries, err := os.ReadDir(filepath.Join(librivoxFixture, librivoxPagesDir))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		body, err := os.ReadFile(filepath.Join(librivoxFixture, librivoxPagesDir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		var page struct {
			Books []json.RawMessage `json:"books"`
		}
		if err := json.Unmarshal(body, &page); err != nil {
			t.Fatal(err)
		}
		feed.books = append(feed.books, page.Books...)
	}
	return feed
}

func (f *fakeFeed) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	// What LibriVox asked of its clients: an identifiable User-Agent, and the
	// parameters the snapshot is defined by.
	if ua := r.Header.Get("User-Agent"); ua != LibrivoxUserAgent ||
		!strings.Contains(ua, "meta.audiosilo.app") || !strings.Contains(ua, "kode@audiosilo.app") {
		f.t.Errorf("User-Agent = %q", ua)
	}
	for k, want := range map[string]string{"extended": "1", "coverart": "1", "format": "json"} {
		if q.Get(k) != want {
			f.t.Errorf("%s = %q, want %q", k, q.Get(k), want)
		}
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	f.mu.Lock()
	f.offsets = append(f.offsets, offset)
	var fail *fakeFailure
	if len(f.failures) > 0 {
		fail = &f.failures[0]
		f.failures = f.failures[1:]
	}
	f.mu.Unlock()
	if fail != nil {
		if fail.retryAfter != "" {
			w.Header().Set("Retry-After", fail.retryAfter)
		}
		w.WriteHeader(fail.status)
		return
	}
	if offset >= len(f.books) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"Audiobooks could not be found"}`))
		return
	}
	end := min(offset+limit, len(f.books))
	body, _ := json.Marshal(map[string]any{"books": f.books[offset:end]})
	_, _ = w.Write(body)
}

func (f *fakeFeed) requested() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int(nil), f.offsets...)
}

// testFetcher is a fetcher against srv on a fake clock: Sleep advances the clock
// and records the wait, so the test sees every pause the fetcher asked for.
func testFetcher(srv *httptest.Server, limit int) (*LibrivoxFetcher, *[]time.Duration) {
	f := NewLibrivoxFetcher()
	f.FeedURL = srv.URL + "/api/feed/audiobooks"
	f.HTTP = srv.Client()
	f.Limit = limit
	now := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)
	var sleeps []time.Duration
	f.Now = func() time.Time { return now }
	f.Sleep = func(_ context.Context, d time.Duration) error {
		sleeps = append(sleeps, d)
		now = now.Add(d)
		return nil
	}
	return f, &sleeps
}

func readManifest(t *testing.T, dir string) librivoxManifest {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, librivoxManifestFile))
	if err != nil {
		t.Fatal(err)
	}
	var m librivoxManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestLibrivoxFetchPagesTheCatalogue(t *testing.T) {
	feed := newFakeFeed(t)
	srv := httptest.NewServer(feed)
	defer srv.Close()
	f, sleeps := testFetcher(srv, 2)
	dir := t.TempDir()

	res, err := f.Fetch(context.Background(), dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Two full pages, then the feed's "no results" answer ends the catalogue.
	if !reflect.DeepEqual(feed.requested(), []int{0, 2, 4}) || res.Requests != 3 || res.PagesFetched != 2 || !res.Complete || res.TotalProjects != 4 {
		t.Fatalf("offsets %v, result %+v", feed.requested(), res)
	}
	// Five seconds between requests, and no wait before the first.
	if !reflect.DeepEqual(*sleeps, []time.Duration{5 * time.Second, 5 * time.Second}) {
		t.Errorf("sleeps = %v", *sleeps)
	}
	m := readManifest(t, dir)
	if !m.Complete || m.NextOffset != 4 || m.Pages != 2 || m.Projects != 4 || m.Limit != 2 {
		t.Errorf("manifest = %+v", m)
	}
	projects, err := ReadLibrivoxSnapshot(dir)
	if err != nil {
		t.Fatal(err)
	}
	var ids []int
	for _, p := range projects {
		ids = append(ids, int(p.ID))
	}
	if !reflect.DeepEqual(ids, []int{296, 527, 801, 1870}) {
		t.Errorf("snapshot ids = %v", ids)
	}

	// A complete snapshot asks nothing more of LibriVox.
	again, _ := testFetcher(srv, 2)
	if res, err := again.Fetch(context.Background(), dir, 0); err != nil || res.Requests != 0 {
		t.Errorf("complete snapshot fetched again: %+v %v", res, err)
	}
}

func TestLibrivoxFetchShortPageEndsTheCatalogue(t *testing.T) {
	feed := newFakeFeed(t)
	srv := httptest.NewServer(feed)
	defer srv.Close()
	f, _ := testFetcher(srv, 3)
	res, err := f.Fetch(context.Background(), t.TempDir(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(feed.requested(), []int{0, 3}) || !res.Complete || res.TotalProjects != 4 {
		t.Errorf("offsets %v, result %+v; a page shorter than the limit is the last", feed.requested(), res)
	}
}

func TestLibrivoxFetchResumes(t *testing.T) {
	feed := newFakeFeed(t)
	srv := httptest.NewServer(feed)
	defer srv.Close()
	dir := t.TempDir()

	first, _ := testFetcher(srv, 2)
	res, err := first.Fetch(context.Background(), dir, 1)
	if err != nil || res.Complete || res.NextOffset != 2 {
		t.Fatalf("capped run = %+v %v", res, err)
	}
	second, _ := testFetcher(srv, 2)
	res, err = second.Fetch(context.Background(), dir, 0)
	if err != nil || !res.Complete || res.TotalProjects != 4 {
		t.Fatalf("resumed run = %+v %v", res, err)
	}
	// The first window is never fetched twice.
	if got := feed.requested(); !reflect.DeepEqual(got, []int{0, 2, 4}) {
		t.Errorf("offsets = %v", got)
	}

	// A resume with other parameters would count other windows: refused.
	other, _ := testFetcher(srv, 50)
	if _, err := other.Fetch(context.Background(), dir, 0); err == nil || !strings.Contains(err.Error(), "same parameters") {
		t.Errorf("limit change accepted: %v", err)
	}
}

func TestLibrivoxFetchBacksOff(t *testing.T) {
	feed := newFakeFeed(t)
	feed.failures = []fakeFailure{{status: 429, retryAfter: "120"}, {status: 503}, {status: 502}}
	srv := httptest.NewServer(feed)
	defer srv.Close()
	f, sleeps := testFetcher(srv, 100)
	res, err := f.Fetch(context.Background(), t.TempDir(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if res.Requests != 4 || !res.Complete || res.TotalProjects != 4 {
		t.Fatalf("result %+v", res)
	}
	// The server's Retry-After, then the exponential wait (30 s doubled per
	// attempt, the first attempt included). The 5 s spacing is already covered
	// by each backoff, so it adds no wait of its own.
	want := []time.Duration{120 * time.Second, 60 * time.Second, 120 * time.Second}
	if !reflect.DeepEqual(*sleeps, want) {
		t.Errorf("sleeps = %v, want %v", *sleeps, want)
	}
}

func TestLibrivoxFetchGivesUpResumably(t *testing.T) {
	feed := newFakeFeed(t)
	for range 10 {
		feed.failures = append(feed.failures, fakeFailure{status: 500})
	}
	srv := httptest.NewServer(feed)
	defer srv.Close()
	f, sleeps := testFetcher(srv, 100)
	f.MaxRetries = 3
	dir := t.TempDir()
	_, err := f.Fetch(context.Background(), dir, 0)
	if err == nil || !strings.Contains(err.Error(), "rerun to resume") {
		t.Fatalf("err = %v", err)
	}
	if len(feed.requested()) != 4 || len(*sleeps) != 3 || (*sleeps)[2] != 2*time.Minute {
		t.Errorf("requests %v, sleeps %v", feed.requested(), *sleeps)
	}
	if _, err := os.Stat(filepath.Join(dir, librivoxManifestFile)); !os.IsNotExist(err) {
		t.Error("a failed first page wrote a manifest")
	}
}

func TestLibrivoxFetchRefusesAClientError(t *testing.T) {
	feed := newFakeFeed(t)
	feed.failures = []fakeFailure{{status: 400}}
	srv := httptest.NewServer(feed)
	defer srv.Close()
	f, _ := testFetcher(srv, 100)
	if _, err := f.Fetch(context.Background(), t.TempDir(), 0); err == nil || !strings.Contains(err.Error(), "HTTP 400") {
		t.Fatalf("err = %v", err)
	}
	if len(feed.requested()) != 1 {
		t.Errorf("a 400 was retried: %v", feed.requested())
	}
}

func TestLibrivoxFetchRefusesAnOverLimit(t *testing.T) {
	f := NewLibrivoxFetcher()
	f.Limit = 501
	if _, err := f.Fetch(context.Background(), t.TempDir(), 0); err == nil {
		t.Fatal("limit 501 accepted; the feed caps at 500")
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	for in, want := range map[string]time.Duration{
		"":                              0,
		"30":                            30 * time.Second,
		"-1":                            0,
		"soon":                          0,
		"Tue, 27 Oct 2026 12:02:00 GMT": 2 * time.Minute,
		"Tue, 27 Oct 2026 11:00:00 GMT": 0,
	} {
		if got := parseRetryAfter(in, now); got != want {
			t.Errorf("parseRetryAfter(%q) = %v, want %v", in, got, want)
		}
	}
}
