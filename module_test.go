package meta

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDataTreeIsANestedModule pins data/go.mod, the file that keeps the
// catalogue out of this module's zip. The go command omits any subdirectory
// holding its own go.mod from a module zip, and data/ alone is ~1.6 GB - three
// times the 500 MiB ceiling - so without it no tag of this module can be
// fetched by an importer of pkg/* (audiosilo-sidecars). Nothing else would
// notice it going: the build, the tests and every data tool are indifferent to
// it, and the failure surfaces only in another repository, at `go get` time.
func TestDataTreeIsANestedModule(t *testing.T) {
	path := filepath.Join("data", "go.mod")
	fi, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("%s: %v - it keeps data/ out of the module zip (see the file's own comment)", path, err)
	}
	// The go command tests for a REGULAR file (a symlink or directory there
	// does not start a module), so this does too.
	if !fi.Mode().IsRegular() {
		t.Fatalf("%s is not a regular file (%s), so the go command would not treat data/ as a nested module", path, fi.Mode())
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	const want = "module github.com/kodestar/audiosilo-meta/data"
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == want {
			return
		}
	}
	t.Fatalf("%s does not declare %q", path, want)
}
