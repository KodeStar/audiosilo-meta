package testpack

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// Snapshot reads every file under dir into a map keyed by its dir-relative
// slash path, so a test can compare a tree before and after a run - "changed
// nothing" is two equal snapshots, and "changed exactly these files" is their
// difference. The one copy of a helper several suites had each grown.
func Snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = string(raw)
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot %s: %v", dir, err)
	}
	return out
}
