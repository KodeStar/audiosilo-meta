package canonical

import (
	"os"
	"strings"
	"testing"
)

// TestRealDataCanonical guards that the committed seed data is canonically
// formatted (the metafmt --check invariant), so it can never silently drift.
//
// It does not run under -race: CheckTree starts no goroutine, so the detector
// has nothing to find, and walking the ~1.6GB tree under it cost ~6 minutes -
// over half Go's 10-minute default test timeout. The non-race run still reads
// every file, and CI's metafmt --check covers the same invariant.
func TestRealDataCanonical(t *testing.T) {
	if raceEnabled {
		t.Skip("skipped under -race: CheckTree is sequential, so the real tree adds no race coverage; the non-race run and metafmt --check validate it")
	}
	const dataDir = "../../data"
	if _, err := os.Stat(dataDir); err != nil {
		t.Skipf("no data tree at %s: %v", dataDir, err)
	}
	bad, _, err := CheckTree(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(bad) != 0 {
		t.Fatalf("non-canonical data files (run metafmt --write):\n  %s", strings.Join(bad, "\n  "))
	}
}
