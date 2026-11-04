package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/testpack"
)

// narrations reads the tree and libex, writes its worklist and rows, and leaves
// the tree untouched.
func TestNarrationsWritesTheWorklistAndTouchesNoData(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	testpack.Seed(t, dataDir, map[string]string{
		"people/an/andy-writer.json": `{"id":"andy-writer","license":"CC0-1.0","name":"Andy Writer","sources":[{"type":"libex-import"}]}`,
		"people/ra/ray-reader.json":  `{"id":"ray-reader","license":"CC0-1.0","name":"Ray Reader","sources":[{"type":"libex-import"}]}`,
		"works/ma/mars/work.json": `{"authors":["andy-writer"],"id":"mars","language":"en","license":"CC0-1.0",` +
			`"sources":[{"type":"libex-import"}],"title":"Mars"}`,
		"works/ma/mars/recordings/ray-reader-2013.json": `{"asin":[{"asin":"B0RAY00001","region":"us"},{"asin":"B0WIL00001","region":"us"}],` +
			`"id":"ray-reader-2013","language":"en","license":"CC0-1.0","narrators":["ray-reader"],` +
			`"sources":[{"type":"audiosilo-books-import"}],"work":"mars"}`,
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/db/book/B0RAY00001":
			_, _ = w.Write([]byte(`{"asin":"B0RAY00001","narrators":[{"name":"Ray Reader"}],"lengthMinutes":650}`))
		case "/db/book/B0WIL00001":
			_, _ = w.Write([]byte(`{"asin":"B0WIL00001","narrators":[{"name":"Will Other"}],"lengthMinutes":659}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	worklist, rows := filepath.Join(dir, "narrations.ndjson"), filepath.Join(dir, "rows.ndjson")
	before := testpack.Snapshot(t, dataDir)
	out := captureStdout(t, func() {
		if code := runNarrations([]string{"--data", dataDir, "--libex", srv.URL, "--user-library", "-o", worklist, "--rows", rows}); code != 0 {
			t.Errorf("exit code = %d, want 0", code)
		}
	})
	if !strings.Contains(out, "1 recording checked, 2 ASIN lookups = 2 answered + 0 not in libex + 0 failed") || !strings.Contains(out, "1 finding") {
		t.Errorf("output = %q", out)
	}
	got, err := os.ReadFile(worklist)
	if err != nil || !strings.Contains(string(got), `"kind":"asins-name-different-narrators"`) || !strings.Contains(string(got), `"recording":"ray-reader-2013"`) {
		t.Errorf("worklist = %q, err = %v", got, err)
	}
	if b, err := os.ReadFile(rows); err != nil || strings.Count(string(b), "\n") != 2 {
		t.Errorf("rows = %q, err = %v", b, err)
	}
	after := testpack.Snapshot(t, dataDir)
	for k, v := range before {
		if after[k] != v {
			t.Errorf("narrations changed %s", k)
		}
	}

	// -o is required.
	if code := runNarrations([]string{"--data", dataDir}); code != 2 {
		t.Errorf("no -o: exit code = %d, want 2", code)
	}
}
