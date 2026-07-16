package canonical

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// jsonFiles returns every *.json file under dir, sorted.
func jsonFiles(dir string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.EqualFold(filepath.Ext(path), ".json") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

// CheckTree returns the JSON files under dir that are not in canonical form.
// A file that fails to parse is reported (so metafmt surfaces it too).
func CheckTree(dir string) (nonCanonical []string, err error) {
	files, err := jsonFiles(dir)
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		ok, ferr := IsCanonical(raw)
		if ferr != nil || !ok {
			nonCanonical = append(nonCanonical, f)
		}
	}
	return nonCanonical, nil
}

// WriteTree rewrites every non-canonical JSON file under dir in place and
// returns the paths it changed. Files that fail to parse are returned in failed
// and left untouched.
func WriteTree(dir string) (changed, failed []string, err error) {
	files, err := jsonFiles(dir)
	if err != nil {
		return nil, nil, err
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			return nil, nil, err
		}
		formatted, ferr := Format(raw)
		if ferr != nil {
			failed = append(failed, f)
			continue
		}
		if !equal(raw, formatted) {
			info, err := os.Stat(f)
			if err != nil {
				return nil, nil, err
			}
			if err := os.WriteFile(f, formatted, info.Mode().Perm()); err != nil {
				return nil, nil, err
			}
			changed = append(changed, f)
		}
	}
	return changed, failed, nil
}

func equal(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
