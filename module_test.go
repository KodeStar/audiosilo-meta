package meta

import (
	"os"
	"strings"
	"testing"
)

// TestDataTreeIsANestedModule pins data/go.mod (see that file's comment).
func TestDataTreeIsANestedModule(t *testing.T) {
	const why = "data/go.mod keeps the ~1.6 GB data tree out of this module's zip; " +
		"without it pkg/* importers (audiosilo-sidecars) cannot go get a release"
	raw, err := os.ReadFile("data/go.mod")
	if err != nil {
		t.Fatalf("%s: %v", why, err)
	}
	if !strings.Contains(string(raw), "module github.com/kodestar/audiosilo-meta/data\n") {
		t.Fatalf("%s, and it no longer declares module github.com/kodestar/audiosilo-meta/data", why)
	}
}
