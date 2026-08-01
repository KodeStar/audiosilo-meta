package pack

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// listing.go is ONE walk of a data tree, made shareable.
//
// The walk was being repeated. Loading the catalogue used to cost the caller's
// own walk (pkg/check's), plus one walk per family inside Detect, plus a
// directory scan per family and per directory inside ReadTree - and a writer
// that both validates and writes paid all of that twice, once for pkg/check and
// once for Store.Open. Every one of those passes was asking the same directory
// entries the same question.
//
// A Listing answers all three questions from a single walk: which files exist
// (and under which family), which layout each family is in, and what each
// family's pack tree is.

// Listing is one walk of a data tree: every JSON file under it, partitioned by
// the family root it sits under.
//
// It is a SNAPSHOT and is never refreshed, so anything that writes to the tree
// invalidates it - which is why Store drops the listing it was opened with as
// soon as it flushes.
type Listing struct {
	dir   string
	files map[Family][]string
	stray []string

	// layouts memoizes Layouts. A listing is a snapshot, so the layout it
	// implies cannot change under it.
	layouts map[Family]Layout
}

// List walks dataDir once and partitions every JSON file it holds by family
// root. Paths come back data-relative, slash-separated and sorted.
//
// The data root and every family root that exists must BE a directory. A walk
// alone cannot enforce that: handed a regular file, WalkDir yields that one file
// and stops, which reads as "the tree holds no packs" - a data root that is a
// file, or a data/people that is, would come back clean instead of refused. So
// each is stated separately, and a non-directory is an error rather than an
// absence.
//
// The extension match is case-insensitive, deliberately. A listing is what a
// caller ACCOUNTS for, and a "0.JSON" that pkg/pack will not read as a pack (see
// isJSONFile, which is case-sensitive on purpose) still has to be reported by
// whoever demands that every file in the tree be accounted for - pkg/check's
// unrecognized location. The stricter test is applied where a file is read AS a
// pack, so a file can be listed and still be no pack.
func List(dataDir string) (*Listing, error) {
	if err := mustBeDir(dataDir); err != nil {
		return nil, err
	}
	for _, d := range Families() {
		root := filepath.Join(dataDir, filepath.FromSlash(d.Family.Root()))
		if err := mustBeDir(root); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}
	l := &Listing{dir: dataDir, files: map[Family][]string{}}
	err := filepath.WalkDir(dataDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.EqualFold(filepath.Ext(p), jsonExt) {
			return nil
		}
		rel, rerr := filepath.Rel(dataDir, p)
		if rerr != nil {
			return rerr
		}
		l.add(filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	for f := range l.files {
		sort.Strings(l.files[f])
	}
	sort.Strings(l.stray)
	return l, nil
}

// errNotDirectory is what a path that exists but is not a directory fails with,
// wherever a directory is what has to be there.
var errNotDirectory = errors.New("not a directory")

// mustBeDir reports whether path is a directory, as an error. A path that is not
// there fails with an os.IsNotExist error, which a caller that tolerates an
// absent root can test for; a path that is there and is not a directory is
// errNotDirectory and nothing may read it as an absence.
func mustBeDir(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return &fs.PathError{Op: "read", Path: path, Err: errNotDirectory}
	}
	return nil
}

// emptyListing is the listing of a data root that does not exist yet.
func emptyListing(dataDir string) *Listing {
	return &Listing{dir: dataDir, files: map[Family][]string{}}
}

// add files one walked path under its family, or under no family at all.
func (l *Listing) add(rel string) {
	if root, _, ok := strings.Cut(rel, "/"); ok {
		if d, known := Def(Family(root)); known {
			l.files[d.Family] = append(l.files[d.Family], rel)
			return
		}
	}
	l.stray = append(l.stray, rel)
}

// Dir returns the data root the listing was taken on.
func (l *Listing) Dir() string { return l.dir }

// Files returns every JSON file the walk found under family f's root, sorted.
//
// It is the ACCOUNTING set, not the pack set: a file listed here need not be a
// pack (Tree keeps only the ones that are), which is what lets a caller report
// the files a family holds that nothing reads. The slice is the listing's own -
// read it, do not write to it.
func (l *Listing) Files(f Family) []string { return l.files[f] }

// Stray returns the JSON files that sit under no family root at all, sorted.
func (l *Listing) Stray() []string { return l.stray }

// Tree returns family f's pack listing, derived from the walk rather than from
// a fresh directory scan. It keeps exactly what ReadTree keeps: a JSON file
// directly under the family root, or one directory deeper, whatever it is
// named. Anything deeper, or named so that it bounds nothing, is not a pack and
// is left in Files for the caller to account for.
func (l *Listing) Tree(f Family) *Tree { return NewTree(f, packRefs(f, l.files[f])) }

// packRefs turns a family's file list into the pack refs it names.
func packRefs(f Family, rels []string) []PackRef {
	root := f.Root() + "/"
	out := make([]PackRef, 0, len(rels))
	for _, rel := range rels {
		parts := strings.Split(strings.TrimPrefix(rel, root), "/")
		switch len(parts) {
		case 1:
			if b, ok := packBound(parts[0]); ok {
				out = append(out, PackRef{Family: f, Bound: b})
			}
		case 2:
			if b, ok := packBound(parts[1]); ok {
				out = append(out, PackRef{Family: f, Dir: parts[0], Bound: b})
			}
		}
	}
	return out
}

// Layouts reports every family's layout, reading only the files the walk
// already found. It is Detect without the walk, and the answer is memoized.
func (l *Listing) Layouts() (map[Family]Layout, error) {
	if l.layouts != nil {
		return l.layouts, nil
	}
	out := make(map[Family]Layout, len(defs))
	for _, d := range Families() {
		lay, err := detectLayoutIn(l.dir, l.packFiles(d.Family))
		if err != nil {
			return nil, err
		}
		out[d.Family] = lay
	}
	l.layouts = out
	return out, nil
}

// packFiles returns the family's files that pkg/pack will read as packs: the
// case-sensitive JSON spelling, which is the set a directory walk of the family
// root would have produced.
func (l *Listing) packFiles(f Family) []string {
	rels := l.files[f]
	out := make([]string, 0, len(rels))
	for _, rel := range rels {
		if isJSONFile(rel) {
			out = append(out, rel)
		}
	}
	return out
}

// listFor takes a listing of dataDir, tolerating a data root that does not
// exist yet: every family reads as absent and the first Flush creates what it
// needs. A walk that fails for any other reason is still an error.
func listFor(dataDir string) (*Listing, error) {
	l, err := List(dataDir)
	if err == nil {
		return l, nil
	}
	if _, serr := os.Stat(dataDir); os.IsNotExist(serr) {
		return emptyListing(dataDir), nil
	}
	return nil, err
}
