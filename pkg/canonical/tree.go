package canonical

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kodestar/audiosilo-meta/internal/parallel"
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
	// Every file is judged on its own, so the judging runs on the shared pool:
	// a worker writes only its own slot, and the lists are assembled in walk
	// order afterwards, so the result does not depend on the scheduler.
	results := make([]fileResult, len(files))
	parallel.Do(len(files), func(i int) {
		raw, err := os.ReadFile(files[i])
		if err != nil {
			results[i].err = err
			return
		}
		ok, ferr := IsCanonical(raw)
		results[i].invalid = ferr != nil
		results[i].changed = ferr != nil || !ok
	})
	for i, r := range results {
		if r.err != nil {
			return nil, nil, r.err
		}
		if r.changed {
			nonCanonical = append(nonCanonical, files[i])
		}
		if r.invalid {
			invalid = append(invalid, files[i])
		}
	}
	return nonCanonical, invalid, nil
}

// fileResult is what one worker of a tree walk found about one file: whether it
// is (or, for a write, was) out of canonical form, whether it parsed at all, and
// the I/O error that stopped it. The walk reports the FIRST error in walk order,
// as the sequential loop it replaced did; on a write the files other workers
// had already rewritten stay rewritten, which is only ever canonical form.
type fileResult struct {
	changed, invalid bool
	err              error
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
	// One file per worker, as in CheckTreeExcept: each rewrite touches only its
	// own file, and the lists are assembled in walk order.
	results := make([]fileResult, len(files))
	parallel.Do(len(files), func(i int) {
		results[i] = writeFile(files[i])
	})
	for i, r := range results {
		if r.err != nil {
			return nil, nil, r.err
		}
		if r.invalid {
			failed = append(failed, files[i])
		}
		if r.changed {
			changed = append(changed, files[i])
		}
	}
	return changed, failed, nil
}

// writeFile rewrites one file in canonical form if it is not already, leaving
// a file that does not parse untouched.
func writeFile(f string) fileResult {
	raw, err := os.ReadFile(f)
	if err != nil {
		return fileResult{err: err}
	}
	formatted, ferr := Format(raw)
	if ferr != nil {
		return fileResult{invalid: true}
	}
	if equal(raw, formatted) {
		return fileResult{}
	}
	info, err := os.Stat(f)
	if err != nil {
		return fileResult{err: err}
	}
	if err := os.WriteFile(f, formatted, info.Mode().Perm()); err != nil {
		return fileResult{err: err}
	}
	return fileResult{changed: true}
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
