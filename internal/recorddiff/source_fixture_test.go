package recorddiff

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// source_fixture_test.go is the second implementation of Source: the one that
// reads a data tree out of a directory. It lives in a test file because it has
// no production caller - Compute takes any pair of Sources precisely so the
// comparison can be driven from fixture trees, and exporting a directory reader
// beside GitSource would claim a production contract nothing asks for.

// dirSource reads the data tree from a directory on disk.
type dirSource struct{ Dir string }

// Read returns the file's bytes, or not-found for a path this tree does not hold.
func (d dirSource) Read(rel string) ([]byte, bool, error) {
	raw, err := os.ReadFile(filepath.Join(d.Dir, filepath.FromSlash(rel)))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return raw, true, nil
}

// dirPaths lists every JSON file either directory holds, data-relative and
// sorted. It is the directory equivalent of GitChangedPaths, deliberately
// WIDER: it names every file rather than the changed ones, which Compute
// classifies to the same answer (an entry identical on both sides is unchanged
// however it was reached) at a cost only a fixture tree can afford.
func dirPaths(dirs ...string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, dir := range dirs {
		err := filepath.WalkDir(dir, func(p string, e fs.DirEntry, err error) error {
			if err != nil {
				if os.IsNotExist(err) && p == dir {
					return nil
				}
				return err
			}
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
				return nil
			}
			rel, err := filepath.Rel(dir, p)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if !seen[rel] {
				seen[rel] = true
				out = append(out, rel)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	slices.Sort(out)
	return out, nil
}
