package pack

import (
	"errors"
	"fmt"
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

// RedirectsFile is the data-relative path of the one recognized NON-PACK file
// the data root may hold: the slug tombstone table (model.Redirects). It is named
// here because this package owns the tree's file accounting, and that accounting
// is the only thing pkg/pack has to know about it: it is not a family, it holds
// no entity, and no reader or writer here ever opens it.
const RedirectsFile = "redirects.json"

// auxFile reports whether the data-relative path rel is a recognized non-pack
// file of the data tree under profile p. The match is the EXACT path, so a file
// of the same name inside a family root is judged as that family's file, exactly
// as any other name there would be - nothing about this exemption reaches inside
// a family. A profile that does not carry the tombstone table recognizes nothing
// here, so the path falls through to Stray like any other file that belongs
// nowhere.
func auxFile(p Profile, rel string) bool { return p.Redirects() && rel == RedirectsFile }

// Listing is one walk of a data tree: every JSON file under it, partitioned by
// the family root it sits under.
//
// It is a SNAPSHOT and is never refreshed, so anything that writes to the tree
// invalidates it - which is why Store drops the listing it was opened with as
// soon as it flushes.
//
// It carries the PROFILE it was taken under (see Profile): which family roots
// count as family roots at all, and whether the tombstone table is a recognized
// file. Everything downstream of the walk - the layouts, the store, the
// validation - reads the profile off the listing rather than being told it
// again, so one walk and everything derived from it can never disagree about
// what the tree is meant to hold.
type Listing struct {
	dir     string
	profile Profile
	files   map[Family][]string
	aux     []string
	stray   []string

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
func List(dataDir string) (*Listing, error) { return ListProfile(dataDir, ProfileAll) }

// ListProfile is List over a data root that holds only profile p's families (see
// Profile). List is this with ProfileAll, which is what a root holding the whole
// database is.
//
// The profile changes exactly two things about the walk, both of them
// accounting: only its own family roots are checked for and partitioned into,
// and the tombstone table is a recognized file only if the profile carries one.
// A file under a family root the profile does not name is therefore a STRAY -
// which is the whole mechanism by which a leftover works-community/ under a core
// root becomes a loud check failure rather than a directory nobody reads.
func ListProfile(dataDir string, profile Profile) (*Listing, error) {
	if !profile.Valid() {
		return nil, fmt.Errorf("unknown tree profile %q", profile)
	}
	profile = profile.resolve()
	if err := mustBeDir(dataDir); err != nil {
		return nil, err
	}
	for _, d := range profile.Families() {
		root := filepath.Join(dataDir, filepath.FromSlash(d.Family.Root()))
		if err := mustBeDir(root); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}
	l := &Listing{dir: dataDir, profile: profile, files: map[Family][]string{}}
	err := filepath.WalkDir(dataDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(dataDir, p)
		if rerr != nil {
			return rerr
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			// A DIRECTORY where a recognized file belongs is refused rather than
			// walked past. The walk otherwise ignores directories, so a
			// data/redirects.json that is one would read as "the tree holds no
			// redirects" - the silent answer, where every other wrong shape in
			// this tree is loud (compare mustBeDir above, its mirror image).
			//
			// The test is the PATH, under every profile - deliberately not
			// auxFile, which asks whether this tree carries a tombstone table.
			// That path names a file in this project full stop, and a profile
			// that does not carry the table has less use for a directory there,
			// not more: descending into it silently would file whatever it holds
			// as strays and never say that the path itself is the mistake.
			if rel == RedirectsFile {
				return &fs.PathError{Op: "read", Path: p, Err: errIsDirectory}
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(p), jsonExt) {
			return nil
		}
		l.add(rel)
		return nil
	})
	if err != nil {
		return nil, err
	}
	for f := range l.files {
		sort.Strings(l.files[f])
	}
	sort.Strings(l.aux)
	sort.Strings(l.stray)
	return l, nil
}

// errNotDirectory is what a path that exists but is not a directory fails with,
// wherever a directory is what has to be there.
var errNotDirectory = errors.New("not a directory")

// errIsDirectory is its mirror image: a path that has to be a FILE (see
// RedirectsFile) and is a directory instead.
var errIsDirectory = errors.New("is a directory, but this path names a file")

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
func emptyListing(dataDir string, profile Profile) *Listing {
	return &Listing{dir: dataDir, profile: profile.resolve(), files: map[Family][]string{}}
}

// add files one walked path under its family, as a recognized non-pack file of
// the tree, or under nothing at all.
//
// The three-way split is what keeps the accounting total: a caller that demands
// every file be accounted for (pkg/check's unrecognized location) reads Stray,
// so a file the tree legitimately holds outside every family - RedirectsFile -
// has to be classified here rather than exempted at each such caller.
//
// The family test is the PROFILE's, not the package's full family table: a root
// the profile does not name is no family root of this tree, so its files are
// strays and the caller that demands total accounting reports them. That is the
// whole of what a profile does to the walk.
func (l *Listing) add(rel string) {
	if root, _, ok := strings.Cut(rel, "/"); ok {
		if d, known := Def(Family(root)); known && l.profile.Has(d.Family) {
			l.files[d.Family] = append(l.files[d.Family], rel)
			return
		}
	}
	if auxFile(l.profile, rel) {
		l.aux = append(l.aux, rel)
		return
	}
	l.stray = append(l.stray, rel)
}

// Dir returns the data root the listing was taken on.
func (l *Listing) Dir() string { return l.dir }

// Profile returns the tree profile the listing was taken under: which families
// this root holds, and whether it carries the tombstone table. Everything derived
// from a listing reads it from here rather than being told separately.
func (l *Listing) Profile() Profile { return l.profile.resolve() }

// Files returns every JSON file the walk found under family f's root, sorted.
//
// It is the ACCOUNTING set, not the pack set: a file listed here need not be a
// pack (Tree keeps only the ones that are), which is what lets a caller report
// the files a family holds that nothing reads. The slice is the listing's own -
// read it, do not write to it.
func (l *Listing) Files(f Family) []string { return l.files[f] }

// Aux returns the recognized non-pack files the walk found, sorted (see
// auxFile). They are accounted for and belong where they are, so nothing here
// reports or rewrites them; they are exposed because the caller that VALIDATES
// the tree reads its files through the listing (check.loadRedirects), and because
// the accounting it does has to be over a partition it can see all of.
func (l *Listing) Aux() []string { return l.aux }

// Stray returns the JSON files that sit under no family root at all and are no
// recognized file of the tree either, sorted.
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

// Layouts reports the layout of every family IN THE LISTING'S PROFILE, reading
// only the files the walk already found. It is Detect without the walk, and the
// answer is memoized. A family outside the profile is absent from the map: it is
// not a family of this tree, and its files are already accounted for as strays.
func (l *Listing) Layouts() (map[Family]Layout, error) {
	if l.layouts != nil {
		return l.layouts, nil
	}
	out := make(map[Family]Layout, len(defs))
	for _, d := range l.Profile().Families() {
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

// listFor takes a listing of dataDir under profile, tolerating a data root that
// does not exist yet: every family reads as absent and the first Flush creates
// what it needs. A walk that fails for any other reason is still an error.
func listFor(dataDir string, profile Profile) (*Listing, error) {
	l, err := ListProfile(dataDir, profile)
	if err == nil {
		return l, nil
	}
	if !profile.Valid() {
		return nil, err // a bad profile is not an absent root
	}
	if _, serr := os.Stat(dataDir); os.IsNotExist(serr) {
		return emptyListing(dataDir, profile), nil
	}
	return nil, err
}
