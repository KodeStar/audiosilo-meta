package query_test

// The public surface, exercised the way a downstream consumer (a mirror-mode
// AudioSilo server) uses it: an artifact from querytest, opened with Open and
// served with NewHandler over a Go http.Client. This file is an EXTERNAL test
// package on purpose, so it can only reach what is exported.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kodestar/audiosilo-meta/internal/build"
	"github.com/kodestar/audiosilo-meta/pkg/query"
	"github.com/kodestar/audiosilo-meta/pkg/query/querytest"
)

// TestMaxSchemaVersionIsTheBuilders pins the coupling the mirror mode rests on:
// the newest schema this code reads is the one the builder writes, so a builder
// bump that is not a deliberate reader change too fails here.
func TestMaxSchemaVersionIsTheBuilders(t *testing.T) {
	if query.MaxSchemaVersion != build.SchemaVersion {
		t.Fatalf("query.MaxSchemaVersion = %d, build.SchemaVersion = %d: the reader and the builder disagree",
			query.MaxSchemaVersion, build.SchemaVersion)
	}
}

func openFixture(t *testing.T, tag string) *query.DB {
	t.Helper()
	db, err := query.Open(querytest.Build(t, t.TempDir()), tag)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// TestOpenRefusesWhatIsNotAnArtifact: a file that is not SQLite, a SQLite file
// that is not an artifact, and a path that names nothing are all errors - and
// the missing path is not CREATED, which a read-write open would do.
func TestOpenRefusesWhatIsNotAnArtifact(t *testing.T) {
	dir := t.TempDir()
	garbage := filepath.Join(dir, "garbage.sqlite")
	if err := os.WriteFile(garbage, []byte("this is not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(dir, "empty.sqlite")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "missing.sqlite")
	for _, path := range []string{garbage, empty, missing} {
		if db, err := query.Open(path, ""); err == nil {
			_ = db.Close()
			t.Errorf("Open(%s) succeeded, want an error", filepath.Base(path))
		}
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Errorf("Open created %s (stat: %v): the open must be read-only", missing, err)
	}
}

// TestInfo reports the artifact: its schema version, build time, tag and counts,
// with Version falling back to built_at when the copy has no tag.
func TestInfo(t *testing.T) {
	tagged := openFixture(t, "data-v2026.07.11-abc1234-def5678").Info()
	if tagged.SchemaVersion != query.MaxSchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d", tagged.SchemaVersion, query.MaxSchemaVersion)
	}
	if want, _ := time.Parse(time.RFC3339, querytest.BuiltAt); !tagged.BuiltAt.Equal(want) {
		t.Errorf("BuiltAt = %v, want %v", tagged.BuiltAt, want)
	}
	if tagged.Tag != "data-v2026.07.11-abc1234-def5678" || tagged.Version != tagged.Tag {
		t.Errorf("Tag = %q, Version = %q, want both the tag", tagged.Tag, tagged.Version)
	}
	st := tagged.Stats
	if st.Works != querytest.Works || st.Recordings != querytest.Recordings ||
		st.People != querytest.People || st.Series != querytest.Series {
		t.Errorf("Stats = %+v, want %d works, %d recordings, %d people, %d series", st,
			querytest.Works, querytest.Recordings, querytest.People, querytest.Series)
	}

	local := openFixture(t, "").Info()
	if local.Tag != "" || local.Version != querytest.BuiltAt {
		t.Errorf("untagged Tag = %q, Version = %q, want \"\" and built_at %s", local.Tag, local.Version, querytest.BuiltAt)
	}
}

// TestNewHandlerWithoutADatabase: while current returns nil every route answers
// 503 with the Retry-After the options give (a default without one), and
// /healthz says it is starting - the shape metaserve answers during a cold boot.
func TestNewHandlerWithoutADatabase(t *testing.T) {
	none := func() *query.DB { return nil }
	for _, tc := range []struct {
		name  string
		opts  query.HandlerOptions
		retry string
	}{
		{"default", query.HandlerOptions{}, "30"},
		{"from the options", query.HandlerOptions{RetryAfter: func() time.Duration { return 1500 * time.Millisecond }}, "2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := query.NewHandler(none, tc.opts)
			for _, path := range []string{"/api/v1/lookup?asin=" + querytest.ASIN, "/api/v1/works/" + querytest.ASINWork, "/healthz"} {
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
				if rec.Code != http.StatusServiceUnavailable {
					t.Errorf("GET %s = %d, want 503", path, rec.Code)
				}
				if got := rec.Header().Get("Retry-After"); got != tc.retry {
					t.Errorf("GET %s Retry-After = %q, want %q", path, got, tc.retry)
				}
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
			if body := strings.TrimSpace(rec.Body.String()); body != `{"status":"starting"}` {
				t.Errorf("/healthz body = %s", body)
			}
		})
	}
}

// TestNewHandlerSwapsWithCurrent: the handler reads current per request, so a
// database installed after the handler was built is served without rebuilding
// it - the property a hot swap or a mirror's update rests on.
func TestNewHandlerSwapsWithCurrent(t *testing.T) {
	var cur atomic.Pointer[query.DB]
	ts := httptest.NewServer(query.NewHandler(cur.Load, query.HandlerOptions{}))
	t.Cleanup(ts.Close)
	if code := get(t, ts.URL+"/healthz", nil); code != http.StatusServiceUnavailable {
		t.Fatalf("before: /healthz = %d, want 503", code)
	}
	cur.Store(openFixture(t, ""))
	if code := get(t, ts.URL+"/healthz", nil); code != http.StatusOK {
		t.Fatalf("after: /healthz = %d, want 200", code)
	}
}

// get issues a GET with the default client (which follows redirects) and
// decodes a JSON body into out when out is non-nil.
func get(t *testing.T, url string, out any) int {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if out != nil && resp.StatusCode == http.StatusOK {
		if err := json.Unmarshal(body, out); err != nil {
			t.Fatalf("GET %s: %v in %s", url, err, body)
		}
	}
	return resp.StatusCode
}

// TestHandlerRoundTrip drives the routes a mirror-mode server calls through
// NewHandler over the querytest artifact, asserting each querytest constant
// against what the API actually answers - so a fixture change that would make a
// constant lie fails here, where the constants are defined.
func TestHandlerRoundTrip(t *testing.T) {
	db := openFixture(t, "")
	ts := httptest.NewServer(query.NewHandler(func() *query.DB { return db }, query.HandlerOptions{}))
	t.Cleanup(ts.Close)

	type lookup struct {
		Work        struct{ ID string } `json:"work"`
		RecordingID string              `json:"recording_id"`
	}
	t.Run("lookup by ASIN", func(t *testing.T) {
		var got lookup
		if code := get(t, ts.URL+"/api/v1/lookup?asin="+querytest.ASIN, &got); code != http.StatusOK {
			t.Fatalf("status = %d", code)
		}
		if got.Work.ID != querytest.ASINWork || got.RecordingID != querytest.ASINRecording {
			t.Errorf("lookup = %+v, want %s / %s", got, querytest.ASINWork, querytest.ASINRecording)
		}
	})
	t.Run("lookup by ISBN", func(t *testing.T) {
		var got lookup
		if code := get(t, ts.URL+"/api/v1/lookup?isbn="+querytest.ISBN, &got); code != http.StatusOK {
			t.Fatalf("status = %d", code)
		}
		if got.Work.ID != querytest.ISBNWork || got.RecordingID != querytest.ISBNRecording {
			t.Errorf("lookup = %+v, want %s / %s", got, querytest.ISBNWork, querytest.ISBNRecording)
		}
	})
	t.Run("lookup of a missing ASIN", func(t *testing.T) {
		if code := get(t, ts.URL+"/api/v1/lookup?asin="+querytest.MissingASIN, nil); code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", code)
		}
	})
	t.Run("work with community content", func(t *testing.T) {
		var got query.WorkDetail
		if code := get(t, ts.URL+"/api/v1/works/"+querytest.CommunityWork, &got); code != http.StatusOK {
			t.Fatalf("status = %d", code)
		}
		if len(got.Characters) == 0 || len(got.Recaps) == 0 || got.RecapSummary == nil ||
			got.RecapSummary.InShort == "" || got.RecapSummary.Ending == "" || got.CommunityDescription == nil {
			t.Errorf("community layer incomplete: characters %d, recaps %d, summary %+v, description %v",
				len(got.Characters), len(got.Recaps), got.RecapSummary, got.CommunityDescription)
		}
	})
	t.Run("series in numeric order", func(t *testing.T) {
		var got query.SeriesDetail
		if code := get(t, ts.URL+"/api/v1/series/"+querytest.SeriesID, &got); code != http.StatusOK {
			t.Fatalf("status = %d", code)
		}
		var positions []string
		for _, e := range got.Works {
			positions = append(positions, e.Position)
		}
		if len(got.Works) != querytest.SeriesWorks || !slices.Equal(positions, []string{"1", "2", "10"}) ||
			got.Works[0].Work.ID != querytest.SeriesWork {
			t.Errorf("series works = %v (first %+v), want %d at 1, 2, 10 starting with %s",
				positions, got.Works[0].Work, querytest.SeriesWorks, querytest.SeriesWork)
		}
	})
	t.Run("chapters", func(t *testing.T) {
		var got struct{ Chapters []query.ChapterOut }
		path := "/api/v1/works/" + querytest.ChapteredWork + "/recordings/" + querytest.ChapteredRecording + "/chapters"
		if code := get(t, ts.URL+path, &got); code != http.StatusOK {
			t.Fatalf("status = %d", code)
		}
		if len(got.Chapters) != querytest.Chapters {
			t.Errorf("chapters = %d, want %d", len(got.Chapters), querytest.Chapters)
		}
	})
	t.Run("a retired slug redirects and the client follows it", func(t *testing.T) {
		client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		resp, err := client.Get(ts.URL + "/api/v1/works/" + querytest.RetiredWork)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusMovedPermanently ||
			resp.Header.Get("Location") != "/api/v1/works/"+querytest.RetiredWorkTarget ||
			resp.Header.Get("Cache-Control") != query.RedirectMaxAge {
			t.Fatalf("status %d, Location %q, Cache-Control %q", resp.StatusCode,
				resp.Header.Get("Location"), resp.Header.Get("Cache-Control"))
		}
		// The Location is host-relative, which the default client resolves
		// against the request - so a consumer's ordinary client lands on the live
		// work.
		var got query.WorkDetail
		if code := get(t, ts.URL+"/api/v1/works/"+querytest.RetiredWork, &got); code != http.StatusOK || got.ID != querytest.RetiredWorkTarget {
			t.Errorf("followed: status %d, id %q, want 200 %s", code, got.ID, querytest.RetiredWorkTarget)
		}
	})
	t.Run("works search and match", func(t *testing.T) {
		var search struct{ Results []struct{ ID string } }
		if code := get(t, ts.URL+"/api/v1/works/search?q=hail+mary", &search); code != http.StatusOK {
			t.Fatalf("search status = %d", code)
		}
		if len(search.Results) == 0 || search.Results[0].ID != querytest.ASINWork {
			t.Errorf("works/search = %+v, want %s first", search.Results, querytest.ASINWork)
		}
		var match struct {
			Results []struct {
				ID    string
				Score int
			}
		}
		if code := get(t, ts.URL+"/api/v1/works/match?title=Project+Hail+Mary&author=Andy+Weir", &match); code != http.StatusOK {
			t.Fatalf("match status = %d", code)
		}
		if len(match.Results) == 0 || match.Results[0].ID != querytest.ASINWork || match.Results[0].Score <= 0 {
			t.Errorf("works/match = %+v, want %s first with a score", match.Results, querytest.ASINWork)
		}
	})
	t.Run("healthz", func(t *testing.T) {
		var got struct {
			Status  string `json:"status"`
			BuiltAt string `json:"built_at"`
			Works   int    `json:"works"`
		}
		if code := get(t, ts.URL+"/healthz", &got); code != http.StatusOK {
			t.Fatalf("status = %d", code)
		}
		if got.Status != "ok" || got.BuiltAt != querytest.BuiltAt || got.Works != querytest.Works {
			t.Errorf("healthz = %+v", got)
		}
	})
}

// TestRoutesListsTheMirrorRoutes: the routes a mirror-mode server calls are in
// the exported table, so a consumer that registers or checks patterns finds
// them (and the record routes name the namespace a retired slug resolves in).
func TestRoutesListsTheMirrorRoutes(t *testing.T) {
	got := map[string]query.Route{}
	for _, r := range query.Routes() {
		got[r.Pattern] = r
	}
	for _, pattern := range []string{
		query.HealthzPattern,
		"GET /api/v1/lookup",
		"GET /api/v1/works/{id}",
		"GET /api/v1/works/{id}/recordings/{rid}/chapters",
		"GET /api/v1/series/{id}",
		"GET /api/v1/works/search",
		"GET /api/v1/works/match",
	} {
		if _, ok := got[pattern]; !ok {
			t.Errorf("Routes() lacks %s", pattern)
		}
	}
	if ns := got["GET /api/v1/series/{id}"].Namespace; ns != "series" {
		t.Errorf("series route namespace = %q, want series", ns)
	}
}
