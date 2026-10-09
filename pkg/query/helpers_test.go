package query

import (
	"flag"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/artifacttest"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// The shared fixtures, artifact builders and HTTP helpers live in
// internal/artifacttest (internal/serve's tests and pkg/query/querytest use the
// same ones); this file holds only what is this package's own.

// testSiteURL is the origin the watch feeds are told they link to, so every
// absolute URL in their goldens is stable and obviously not production.
const testSiteURL = "https://meta.test"

// openTestDB opens an artifact path for the test's lifetime.
func openTestDB(t *testing.T, dbPath string) *DB {
	t.Helper()
	db, err := Open(dbPath, "")
	if err != nil {
		t.Fatal(err)
	}
	db.SetLogger(artifacttest.QuietLogger())
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// serveDB serves NewHandler over the artifact at dbPath with opts (a test
// logger unless opts names one), for the test's lifetime.
func serveDB(t *testing.T, dbPath string, opts HandlerOptions) (*DB, *httptest.Server) {
	t.Helper()
	db := openTestDB(t, dbPath)
	if opts.Logger == nil {
		opts.Logger = artifacttest.QuietLogger()
	}
	ts := httptest.NewServer(NewHandler(func() *DB { return db }, opts))
	t.Cleanup(ts.Close)
	return db, ts
}

func newTestServer(t *testing.T) (*DB, *httptest.Server) {
	t.Helper()
	return newTestServerForCatalog(t, artifacttest.Fixture())
}

func newTestServerForCatalog(t *testing.T, catalog *model.Catalog) (*DB, *httptest.Server) {
	t.Helper()
	return serveDB(t, artifacttest.Build(t, catalog), HandlerOptions{})
}

// downgradedServer serves an artifact rolled back to an OLDER shape - an
// artifacttest.Downgraded or Altered path. That is the "a newer binary briefly
// serves an older release" case every version-gated query has to tolerate: it
// must degrade to "no data" rather than 500 on the missing table.
func downgradedServer(t *testing.T, dbPath string) *httptest.Server {
	t.Helper()
	_, ts := serveDB(t, dbPath, HandlerOptions{})
	return ts
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
