package serve

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kodestar/audiosilo-meta/internal/artifacttest"
	"github.com/kodestar/audiosilo-meta/internal/httpx"
	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/query"
)

// The shared fixtures, artifact builders and HTTP helpers (including the quiet
// logger a Server built directly in a test needs - Server.log is not optional)
// live in internal/artifacttest, which pkg/query's tests use too.

// downgradedServer serves an artifact rolled back to an OLDER shape - an
// artifacttest.Downgraded or Altered path. That is the "a newer metaserve binary briefly
// serves an older release" case every version-gated query has to tolerate: it
// must degrade to "no data" rather than 500 on the missing table.
func downgradedServer(t *testing.T, dbPath string) *httptest.Server {
	t.Helper()
	// The origin is fixed here rather than left to the default so a caller can
	// assert an absolute URL a downgraded artifact renders (the sitemap locs).
	srv, err := New(Config{
		DBPath:    dbPath,
		SiteURL:   testSiteURL,
		swapGrace: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func newTestServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	return newTestServerForCatalog(t, artifacttest.Fixture())
}

func newTestServerForCatalog(t *testing.T, catalog *model.Catalog) (*Server, *httptest.Server) {
	t.Helper()
	dbPath := artifacttest.Build(t, catalog)
	srv, err := New(Config{DBPath: dbPath, swapGrace: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return srv, ts
}

func TestCORSHeader(t *testing.T) {
	_, ts := newTestServer(t)
	resp, err := http.Get(ts.URL + "/api/v1/stats")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("ACAO = %q", got)
	}
	if got := resp.Header.Get("Vary"); got == "" {
		t.Errorf("Vary header missing")
	}
}

// TestHotSwap builds two artifacts with different work counts, hammers /stats
// concurrently, swaps mid-flight, and asserts the stat flips atomically without
// a race (run under -race).
func TestHotSwap(t *testing.T) {
	db1 := artifacttest.Build(t, artifacttest.Fixture())

	cat2 := artifacttest.Fixture()
	cat2.Works = append(cat2.Works, &model.Work{
		ID: "artemis", Title: "Artemis", Language: "en",
		Authors: []string{"andy-weir"}, License: "CC0-1.0",
	})
	db2 := artifacttest.Build(t, cat2)

	srv, err := New(Config{DBPath: db1, swapGrace: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	handler := srv.Handler()

	snap2, err := query.Open(db2, "v2")
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	var bad atomic.Int32
	stop := make(chan struct{})
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				rec := httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodGet, "/api/v1/stats", nil)
				handler.ServeHTTP(rec, req)
				var st query.Stats
				if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
					bad.Add(1)
					return
				}
				if st.Works != 4 && st.Works != 5 {
					bad.Add(1)
					return
				}
			}
		}()
	}
	time.Sleep(10 * time.Millisecond)
	srv.swap(snap2)
	time.Sleep(20 * time.Millisecond)
	close(stop)
	wg.Wait()

	if bad.Load() != 0 {
		t.Fatalf("%d requests observed an inconsistent state", bad.Load())
	}
	if got := srv.current().Stats().Works; got != 5 {
		t.Errorf("after swap works = %d, want 5", got)
	}
}

// TestInternalErrorsAreNotReflected pins the 500 body. Every API route here is
// public and CORS-open, so an error's own text - a SQL statement, the cache
// volume's layout, a driver message - is reflected to anyone who can provoke it;
// the detail belongs in the log instead. The 4xx bodies are deliberately NOT
// covered: those describe the request, which the caller sent.
//
// The failure is induced the way TestSitemapErrorCarriesNoCacheHeaders induces
// one - the snapshot's db is closed under the request - so it reaches the real
// error paths of the four files that write a 500 (serve.go, abs.go, sitemap.go,
// watchfeed.go).
func TestInternalErrorsAreNotReflected(t *testing.T) {
	var logged bytes.Buffer
	cfg := quietConfig(t, artifacttest.Fixture(), markedShells)
	cfg.Logger = log.New(&logged, "", 0)
	srv, ts := newPageServerFrom(t, cfg)
	_ = srv.current().Close()

	for _, path := range []string{
		"/api/v1/works/project-hail-mary",
		"/api/v1/works/project-hail-mary/recordings/ray-porter-2021/chapters",
		"/api/v1/people/andy-weir",
		"/api/v1/series/the-stormlight-archive",
		"/api/v1/works/latest",
		"/api/v1/search?q=hail",
		"/api/v1/works/search?q=hail",
		"/api/v1/lookup?asin=B08G9PRS1K",
		"/api/v1/coverage",
		"/api/v1/coverage/works?filter=missing",
		"/api/v1/coverage/series-gaps",
		"/abs/search?query=hail",
		"/sitemaps/works-0.xml",
		"/api/v1/watch/feed.atom?s=the-stormlight-archive",
	} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusInternalServerError {
			t.Errorf("GET %s = %d, want 500 over a closed db; body %s", path, resp.StatusCode, body)
			continue
		}
		var out map[string]string
		if err := json.Unmarshal(body, &out); err != nil {
			t.Errorf("GET %s body is not the JSON error envelope: %s", path, body)
			continue
		}
		if out["error"] != httpx.InternalErrMsg {
			t.Errorf("GET %s leaks the internal error: %q", path, out["error"])
		}
	}
	// The detail is not lost - it is written where an operator reads it.
	if !strings.Contains(logged.String(), `500 "GET" "/api/v1/works/project-hail-mary"`) {
		t.Errorf("the 500s were not logged with their detail:\n%s", logged.String())
	}
	if !strings.Contains(logged.String(), "sql: database is closed") {
		t.Errorf("the log does not carry the driver's own message:\n%s", logged.String())
	}
}

// TestFailLogsOneLinePerRequest: r.URL.Path is the DECODED path, so a request
// carrying %0A used to put a newline inside the 500 line and let the caller
// forge the rest of it as a log entry of their own. The method and the path are
// quoted, so a control character is an escape and the entry stays one line.
func TestFailLogsOneLinePerRequest(t *testing.T) {
	var logged bytes.Buffer
	cfg := quietConfig(t, artifacttest.Fixture(), markedShells)
	cfg.Logger = log.New(&logged, "", 0)
	srv, ts := newPageServerFrom(t, cfg)
	_ = srv.current().Close()

	forged := "/api/v1/works/x%0A2026-01-01%20serve:%20500%20the%20database%20is%20fine"
	resp, err := http.Get(ts.URL + forged)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 over a closed db", resp.StatusCode)
	}
	out := strings.TrimRight(logged.String(), "\n")
	if out == "" {
		t.Fatal("nothing was logged")
	}
	if n := strings.Count(out, "\n"); n != 0 {
		t.Errorf("the request forged %d extra log line(s):\n%s", n, out)
	}
	if !strings.Contains(out, `\n`) {
		t.Errorf("the newline was not escaped into the quoted path:\n%s", out)
	}
}

// snapshotFor opens a fixture catalogue's artifact, for tests that ask the
// query layer directly rather than through HTTP.
func snapshotFor(t *testing.T, cat *model.Catalog) *query.DB {
	t.Helper()
	snap, err := query.Open(artifacttest.Build(t, cat), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = snap.Close() })
	return snap
}

// TestRunWaitsForTheDrain: Serve returns as soon as Shutdown starts, and Run must
// not, on a deployment with no intake queue as on one with it, or main exits
// under a request still being answered.
func TestRunWaitsForTheDrain(t *testing.T) {
	srv, err := New(Config{DBPath: artifacttest.Build(t, artifacttest.Fixture()), swapGrace: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if srv.intake != nil {
		t.Fatal("this test needs a server with no intake queue")
	}
	entered, release := make(chan struct{}), make(chan struct{})
	srv.mux = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		_, _ = io.WriteString(w, "done")
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	finished := make(chan error, 1)
	go func() { finished <- srv.serve(ctx, ln) }()

	answered := make(chan string, 1)
	go func() {
		resp, err := http.Get("http://" + ln.Addr().String() + "/slow")
		if err != nil {
			answered <- err.Error()
			return
		}
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		answered <- string(body)
	}()
	<-entered
	cancel()
	select {
	case err := <-finished:
		t.Fatalf("Run returned (%v) with a request still in flight", err)
	case <-time.After(200 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-finished:
		if err != nil {
			t.Fatalf("Run = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after the request drained")
	}
	if got := <-answered; got != "done" {
		t.Errorf("the request in flight got %q, want its whole answer", got)
	}
}

// TestRunClosesTheIntakeQueueWhenItCannotListen: New starts the intake housekeeping
// and opens the queue's file, so a Run that never gets its port must still end
// both, as it did when the listener and the serving were one call.
func TestRunClosesTheIntakeQueueWhenItCannotListen(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = taken.Close() }()
	srv := intakeServer(t, Config{Addr: taken.Addr().String()})
	if err := srv.Run(t.Context()); err == nil {
		t.Fatal("Run on a taken port returned no error")
	}
	select {
	case <-srv.intake.done:
	default:
		t.Error("Run returned without closing the intake queue")
	}
}

// TestNoRouteAnswersAPreflight pins what an OPTIONS on a catalogue route gets: no
// route is registered for it, so the mux answers 405 (or, with a site, the site
// its 404), never a 2xx and never the CORS preflight headers. A cross-origin
// request that needs a preflight fails; docs-developers/meta/api.md says so.
func TestNoRouteAnswersAPreflight(t *testing.T) {
	for name, site := range map[string]string{"api only": "", "with a site": writeSiteFixture(t)} {
		t.Run(name, func(t *testing.T) {
			srv, err := New(Config{DBPath: artifacttest.Build(t, artifacttest.Fixture()), Site: site, swapGrace: time.Minute})
			if err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{"/api/v1/stats", "/api/v1/works/" + artifacttest.HailMary, "/api/v1/openapi.json"} {
				r := httptest.NewRequest(http.MethodOptions, path, nil)
				r.Header.Set("Origin", "https://elsewhere.example")
				r.Header.Set("Access-Control-Request-Method", http.MethodGet)
				r.Header.Set("Access-Control-Request-Headers", "x-custom")
				rec := httptest.NewRecorder()
				srv.Handler().ServeHTTP(rec, r)
				if rec.Code >= 200 && rec.Code < 300 {
					t.Errorf("OPTIONS %s = %d, want a refusal", path, rec.Code)
				}
				if name == "api only" && rec.Code != http.StatusMethodNotAllowed {
					t.Errorf("OPTIONS %s = %d, want the mux's 405", path, rec.Code)
				}
				for _, h := range []string{"Access-Control-Allow-Methods", "Access-Control-Allow-Headers"} {
					if v := rec.Header().Get(h); v != "" {
						t.Errorf("OPTIONS %s carries %s: %q", path, h, v)
					}
				}
			}
		})
	}
}
