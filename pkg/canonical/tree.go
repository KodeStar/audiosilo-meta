package canonical

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// jsonFiles returns every *.json file under dir, sorted, minus anything under
// the skipped paths.
//
// skip holds paths RELATIVE to dir, slash-separated, naming either a directory
// (whose whole subtree is left alone) or a single file. It is deliberately just
// paths: this package owns canonical FORM and knows nothing about families,
// profiles or the pack layout, so a caller that wants part of a tree left
// untouched says which part in the one vocabulary both sides share.
func jsonFiles(dir string, skip []string) ([]string, error) {
	// The skip entries are pre-joined to the walk's own path spelling once, so
	// the membership test inside the walk is a single map lookup per entry
	// rather than a per-file Rel+ToSlash allocation pair - the walk visits every
	// file in the tree, and the skip list can only match a handful of them.
	var skipped map[string]bool
	if len(skip) > 0 {
		skipped = make(map[string]bool, len(skip))
		for _, s := range skip {
			skipped[filepath.Join(dir, filepath.FromSlash(s))] = true
		}
	}
	var files []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if skipped[path] {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
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

// CheckTree returns the JSON files under dir that are not in canonical form,
// and separately the ones that do not parse at all. An unparseable file appears
// in both: it is not canonical, and naming it invalid saves every caller a
// second read to work out why. The split mirrors WriteTree's changed/failed.
func CheckTree(dir string) (nonCanonical, invalid []string, err error) {
	return CheckTreeExcept(dir, nil)
}

// CheckTreeExcept is CheckTree over everything but the skipped paths (see
// jsonFiles): dir-relative, slash-separated, a directory subtree or a single
// file. CheckTree is this with nothing skipped.
func CheckTreeExcept(dir string, skip []string) (nonCanonical, invalid []string, err error) {
	files, err := jsonFiles(dir, skip)
	if err != nil {
		return nil, nil, err
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			return nil, nil, err
		}
		ok, ferr := IsCanonical(raw)
		if ferr != nil {
			nonCanonical = append(nonCanonical, f)
			invalid = append(invalid, f)
			continue
		}
		if !ok {
			nonCanonical = append(nonCanonical, f)
		}
	}
	return nonCanonical, invalid, nil
}

// WriteTree rewrites every non-canonical JSON file under dir in place and
// returns the paths it changed. Files that fail to parse are returned in failed
// and left untouched.
func WriteTree(dir string) (changed, failed []string, err error) {
	return WriteTreeExcept(dir, nil)
}

// WriteTreeExcept is WriteTree over everything but the skipped paths (see
// jsonFiles). A skipped file is never READ, so it can neither be rewritten nor
// reported - which is the point: a caller that disclaims part of a tree must not
// leave working-tree churn in it.
func WriteTreeExcept(dir string, skip []string) (changed, failed []string, err error) {
	files, err := jsonFiles(dir, skip)
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
