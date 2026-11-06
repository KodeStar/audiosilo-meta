package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRunStore drives `metaimport store` over an empty tree: the row lands, a
// rerun skips it, and a bad date is a usage error before anything runs.
func TestRunStore(t *testing.T) {
	dataDir := t.TempDir()
	in := filepath.Join(t.TempDir(), "listings.ndjson")
	row := `{"store":"graphicaudio","url":"https://www.graphicaudio.net/the-way-of-kings-1-of-5.html","title":"The Way of Kings (1 of 5)",` +
		`"authors":[{"name":"Brandon Sanderson"}],"narrators":[{"name":"A Full Cast"}],"lengthMinutes":360,"language":"english"}` + "\n"
	if err := os.WriteFile(in, []byte(row), 0o644); err != nil {
		t.Fatal(err)
	}
	var code int
	out := captureStdout(t, func() { code = runStore([]string{in, "--data", dataDir, "--date", "2026-11-06"}) })
	if code != 0 || !strings.Contains(out, "1 new works, 1 new recordings") {
		t.Fatalf("exit %d, output:\n%s", code, out)
	}
	out = captureStdout(t, func() { code = runStore([]string{in, "--data", dataDir, "--date", "2026-11-06"}) })
	if code != 0 || !strings.Contains(out, "1 skipped (already present)") {
		t.Fatalf("rerun exit %d, output:\n%s", code, out)
	}
	if code = runStore([]string{in, "--data", dataDir, "--date", "06/11/2026"}); code != 2 {
		t.Errorf("a malformed --date exited %d, want 2", code)
	}
}
