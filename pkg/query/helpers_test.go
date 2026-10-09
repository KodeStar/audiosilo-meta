package query

import (
	"encoding/json"
	"flag"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/artifacttest"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// The shared fixtures live in internal/artifacttest (internal/serve's tests and
// pkg/query/querytest build from the same ones); these are this package's
// spellings of them.
func fixtureCatalog() *model.Catalog   { return artifacttest.Fixture() }
func languagesCatalog() *model.Catalog { return artifacttest.Languages() }

// buildFixtureDB writes a fixture artifact and returns its path.
func buildFixtureDB(t *testing.T, cat *model.Catalog) string {
	t.Helper()
	return artifacttest.Build(t, cat)
}

// downgradedDB and alteredDB roll a fixture artifact back to an older shape (see
// artifacttest.Downgraded / Altered).
func downgradedDB(t *testing.T, cat *model.Catalog, version int, dropTables ...string) string {
	t.Helper()
	return artifacttest.Downgraded(t, cat, version, dropTables...)
}

func alteredDB(t *testing.T, cat *model.Catalog, version int, stmts ...string) string {
	t.Helper()
	return artifacttest.Altered(t, cat, version, stmts...)
}

// testSiteURL is the origin the watch feeds are told they link to, so every
// absolute URL in their goldens is stable and obviously not production.
const testSiteURL = "https://meta.test"

// testLogger is the logger a handler or database built in a test gets, so a
// degradation notice does not land in the test output.
func testLogger() *log.Logger { return log.New(io.Discard, "", 0) }

// openTestDB opens an artifact path for the test's lifetime.
func openTestDB(t *testing.T, dbPath string) *DB {
	t.Helper()
	db, err := Open(dbPath, "")
	if err != nil {
		t.Fatal(err)
	}
	db.SetLogger(testLogger())
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// serveDB serves NewHandler over the artifact at dbPath with opts (a test
// logger unless opts names one), for the test's lifetime.
func serveDB(t *testing.T, dbPath string, opts HandlerOptions) (*DB, *httptest.Server) {
	t.Helper()
	db := openTestDB(t, dbPath)
	if opts.Logger == nil {
		opts.Logger = testLogger()
	}
	ts := httptest.NewServer(NewHandler(func() *DB { return db }, opts))
	t.Cleanup(ts.Close)
	return db, ts
}

func newTestServer(t *testing.T) (*DB, *httptest.Server) {
	t.Helper()
	return newTestServerForCatalog(t, fixtureCatalog())
}

func newTestServerForCatalog(t *testing.T, catalog *model.Catalog) (*DB, *httptest.Server) {
	t.Helper()
	return serveDB(t, buildFixtureDB(t, catalog), HandlerOptions{})
}

// downgradedServer serves an artifact rolled back to an OLDER shape - a
// downgradedDB or alteredDB path. That is the "a newer binary briefly serves an
// older release" case every version-gated query has to tolerate: it must degrade
// to "no data" rather than 500 on the missing table.
func downgradedServer(t *testing.T, dbPath string) *httptest.Server {
	t.Helper()
	_, ts := serveDB(t, dbPath, HandlerOptions{})
	return ts
}

// getJSON fetches path and decodes the body into a generic map.
func getJSON(t *testing.T, base, path string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Get(base + path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if len(body) > 0 {
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatalf("GET %s: decode %q: %v", path, body, err)
		}
	}
	return resp.StatusCode, out
}

// updateGolden regenerates the committed goldens in testdata/golden (the
// unscoped /abs/search bytes): `go test ./pkg/query -run Golden -update-golden`
// after a deliberate change.
var updateGolden = flag.Bool("update-golden", false, "rewrite the goldens in testdata/golden")

// assertGolden compares a response body with testdata/golden/<file>, or rewrites
// that file under -update-golden (artifacttest.Golden).
func assertGolden(t *testing.T, file string, got []byte) {
	t.Helper()
	artifacttest.Golden(t, filepath.Join("testdata", "golden", file), got, *updateGolden)
}

// getNoFollow issues the request WITHOUT following redirects, which is the whole
// point wherever a redirect is the thing under test: http.DefaultClient would
// follow it and every assertion would be about the destination instead. The
// caller does not close the body.
func getNoFollow(t *testing.T, base, path string) *http.Response {
	t.Helper()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(base + path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}
