package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/importer"
	"github.com/kodestar/audiosilo-meta/internal/testpack"
)

// libex-fill --released-preorders selects the released preorder-era recording,
// fetches its record and chapters, and with --dry-run prints what the refresh
// would change per field without writing a byte.
func TestLibexFillReleasedPreordersDryRun(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	testpack.Seed(t, dataDir, map[string]string{
		"people/ad/ada-mapmaker.json": `{"id":"ada-mapmaker","license":"CC0-1.0","name":"Ada Mapmaker","sources":[{"type":"libex-import"}]}`,
		"people/be/bea-reader.json":   `{"id":"bea-reader","license":"CC0-1.0","name":"Bea Reader","sources":[{"type":"libex-import"}]}`,
		"works/bi/bird/work.json": `{"authors":["ada-mapmaker"],"id":"bird","language":"en","license":"CC0-1.0",` +
			`"sources":[{"imported_at":"2026-08-02","ref":"B0EST00001","type":"libex-import"}],"title":"Bird"}`,
		"works/bi/bird/recordings/bea-reader-2026.json": `{"asin":[{"asin":"B0EST00001","region":"us"}],` +
			`"cover_url":"https://m.media-amazon.com/images/I/old.jpg","id":"bea-reader-2026","language":"en","license":"CC0-1.0",` +
			`"narrators":["bea-reader"],"release_date":"2026-09-08","runtime_min":840,` +
			`"sources":[{"imported_at":"2026-08-02","ref":"B0EST00001","type":"libex-import"}],"work":"bird"}`,
	})
	record := `{"asin":"B0EST00001","title":"Bird","region":"us","language":"english","bookFormat":"unabridged",` +
		`"releaseDate":"2026-09-08T00:00:00+00:00","lengthMinutes":970,"imageUrl":"https://m.media-amazon.com/images/I/new.jpg",` +
		`"productState":"AVAILABLE","updatedAt":"2026-10-05T10:00:00+00:00",` +
		`"authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":"Bea Reader"}]}`
	chapters := fmt.Sprintf(`{"isAccurate":true,"runtimeLengthMs":%d,"chapters":[`+
		`{"title":"One","startOffsetMs":0,"lengthMs":%d},{"title":"Two","startOffsetMs":%d,"lengthMs":%d}]}`,
		970*60000, 485*60000, 485*60000, 485*60000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/db/book/B0EST00001":
			_, _ = w.Write([]byte(record))
		case "/db/book/B0EST00001/chapters":
			_, _ = w.Write([]byte(chapters))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	before := testpack.Snapshot(t, dataDir)
	out := captureStdout(t, func() {
		if code := runLibexFill([]string{"--data", dataDir, "--libex", srv.URL, "--released-preorders",
			"--date", "2026-10-07", "--dry-run"}); code != 0 {
			t.Errorf("exit code = %d, want 0", code)
		}
	})
	for _, want := range []string{
		"1 released preorder-era recording to refresh",
		"chapters: 1 attached",
		"refreshed 1 released preorder recording: 1 runtimes, 1 covers, 1 chapter lists, 0 release dates replaced",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	after := testpack.Snapshot(t, dataDir)
	for k, v := range before {
		if after[k] != v {
			t.Errorf("a dry run changed %s", k)
		}
	}

	// Not released yet by --date: nothing is selected, nothing fetched.
	out = captureStdout(t, func() {
		if code := runLibexFill([]string{"--data", dataDir, "--libex", srv.URL, "--released-preorders",
			"--date", "2026-09-01", "--dry-run"}); code != 0 {
			t.Errorf("exit code = %d, want 0", code)
		}
	})
	if !strings.Contains(out, "nothing to refresh") {
		t.Errorf("output = %q, want nothing to refresh before the release", out)
	}
}

func TestPrintSummaryRefreshLine(t *testing.T) {
	out := captureStdout(t, func() {
		printSummary(importer.Summary{EnrichedRecordings: 3, RefreshedRecordings: 3, RefreshedRuntimes: 2,
			RefreshedCovers: 1, RefreshedChapters: 3, RefreshedReleaseDates: 0, MovedPreorderDates: 2}, true, importer.ModeEnrich)
	})
	if !strings.Contains(out, "refreshed 3 released preorder recordings: 2 runtimes, 1 covers, 3 chapter lists, 0 release dates replaced") {
		t.Errorf("summary missing the refresh line: %q", out)
	}
	if !strings.Contains(out, "moved 2 preorder release dates to the date libex now lists") {
		t.Errorf("summary missing the moved-date line: %q", out)
	}
	out = captureStdout(t, func() { printSummary(importer.Summary{EnrichedRecordings: 1}, false, importer.ModeEnrich) })
	if strings.Contains(out, "refreshed") || strings.Contains(out, "moved") {
		t.Errorf("a run that refreshed nothing printed a refresh line: %q", out)
	}
}
