package canonical

import (
	"os"
	"strings"
	"testing"
)

// TestRealDataCanonical guards that the committed seed data is canonically
// formatted (the metafmt --check invariant), so it can never silently drift.
func TestRealDataCanonical(t *testing.T) {
	const dataDir = "../../data"
	if _, err := os.Stat(dataDir); err != nil {
		t.Skipf("no data tree at %s: %v", dataDir, err)
	}
	bad, err := CheckTree(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(bad) != 0 {
		t.Fatalf("non-canonical data files (run metafmt --write):\n  %s", strings.Join(bad, "\n  "))
	}
}
