// Package check loads the data/ tree, validates every record against its JSON
// Schema, and enforces the cross-record rules (placement, key/id agreement,
// referential integrity, global uniqueness, chapter ordering, series
// positions). It returns the discovered problems, any advisory warnings, and,
// best-effort, the loaded Catalog so metabuild can reuse the same load.
//
// Load reads ONE storage layout: the range-packed one pkg/pack defines. The
// file-per-entity tree it replaced is gone (cmd/metamigrate converted it), and a
// family still in that shape is reported as a problem rather than read - see
// loadLegacyFamily, which is the whole of what is left of the old layout here.
//
// This package is PUBLIC API: it is consumed by the sibling audiosilo-sidecars
// tool as an ordinary module dependency, so its exported surface is a contract.
package check

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// Problem is one validation failure, tied to the file it came from.
//
// Path names the smallest thing that can be wrong. A pack file holds many
// entities, so Path carries the entry too, in the form model.PackLocation
// renders:
//
//	works/0/0.json                                    (a pack-wide problem)
//	works/0/0.json: entry book-one                    (an entry's own problem)
//	works/0/0.json: entry book-one: recording rec-one  (a nested recording)
//	works-community/0/0.json: entry book-one: characters
//
// String() joins Path and Msg with ": ", so a problem always reads as one line
// that locates itself from the data root down to the field.
type Problem struct {
	Path string // data-relative, slash-separated path, plus the entry for a pack
	Msg  string
}

func (p Problem) String() string { return p.Path + ": " + p.Msg }

// Result is the outcome of a load: any problems, any advisories, and the
// loaded catalog.
type Result struct {
	// Problems are rule violations. Any of them fails the check.
	Problems []Problem
	// Warnings are advisories: something worth a look that is not a violation.
	// They never fail a check, so a caller that ignores them still behaves
	// exactly as it did before this field existed.
	Warnings []Problem
	Catalog  *model.Catalog
}

// OK reports whether the load found no problems. Warnings are advisory and do
// not affect it.
func (r Result) OK() bool { return len(r.Problems) == 0 }

// addFunc accumulates a formatted problem or warning for a path.
type addFunc func(path, format string, args ...any)

// recordWithPath carries a recording alongside its parent-work slug and the
// pack entry it was read from, so it can be reported against during the
// cross-record checks. A works entry is a composite, so the recording is
// already hung off its work by the time it gets here.
type recordWithPath struct {
	rec      *model.Recording
	workSlug string
	path     string
}

// pathIndex remembers where each entity was loaded from, for later problem
// reporting during cross-record checks.
type pathIndex struct {
	work       map[*model.Work]string
	rec        map[*model.Recording]string
	person     map[*model.Person]string
	series     map[*model.Series]string
	characters map[*model.Characters]string
	recaps     map[*model.Recaps]string
}

func newPathIndex() *pathIndex {
	return &pathIndex{
		work:       map[*model.Work]string{},
		rec:        map[*model.Recording]string{},
		person:     map[*model.Person]string{},
		series:     map[*model.Series]string{},
		characters: map[*model.Characters]string{},
		recaps:     map[*model.Recaps]string{},
	}
}

// loader is one walk's accumulating state, shared by both layout walkers.
type loader struct {
	cat     *model.Catalog
	idx     *pathIndex
	schemas schemaSet
	add     addFunc
	warn    addFunc
}

// Load walks dir, validates it, and returns the result. dir is the data root.
func Load(dir string) Result {
	var probs, warns []Problem
	add := func(path, format string, args ...any) {
		probs = append(probs, Problem{Path: path, Msg: fmt.Sprintf(format, args...)})
	}
	warn := func(path, format string, args ...any) {
		warns = append(warns, Problem{Path: path, Msg: fmt.Sprintf(format, args...)})
	}

	schemas, err := compileSchemas()
	if err != nil {
		return Result{Problems: []Problem{{Path: "schema", Msg: err.Error()}}}
	}

	files, err := jsonFiles(dir)
	if err != nil {
		return Result{Problems: []Problem{{Path: dir, Msg: err.Error()}}}
	}
	layouts, err := pack.Detect(dir)
	if err != nil {
		return Result{Problems: []Problem{{Path: dir, Msg: err.Error()}}}
	}

	l := &loader{cat: &model.Catalog{}, idx: newPathIndex(), schemas: schemas, add: add, warn: warn}

	// Partition the tree's JSON files by family. A pack-layout family's files
	// are walked from its pack listing instead, so they are handed to that
	// walker only to spot files the listing does not account for. A file under
	// no family root at all belongs to nothing and is reported here.
	packFiles := map[pack.Family][]string{}
	legacyFiles := map[pack.Family][]string{}
	for _, abs := range files {
		rel := relSlash(dir, abs)
		f, ok := familyOf(rel)
		if !ok {
			add(rel, "unrecognized location (not under any of the %s roots)", familyRoots())
			continue
		}
		if layouts[f] == pack.LayoutPack {
			packFiles[f] = append(packFiles[f], rel)
			continue
		}
		legacyFiles[f] = append(legacyFiles[f], rel)
	}

	var recs []recordWithPath
	for _, def := range pack.Families() {
		if layouts[def.Family] != pack.LayoutPack {
			l.loadLegacyFamily(def.Family, legacyFiles[def.Family])
			continue
		}
		// A works entry composes its own recordings, so these arrive already
		// attached to their work and only need reporting paths.
		recs = append(recs, l.loadPackFamily(dir, def, packFiles[def.Family])...)
	}

	cat, idx := l.cat, l.idx
	workByID := map[string]*model.Work{}
	for _, w := range cat.Works {
		if _, dup := workByID[w.ID]; !dup {
			workByID[w.ID] = w
		}
	}

	checkIntegrity(cat, workByID, recs, idx, add)
	checkUniqueness(cat, recs, idx, add)
	checkChapters(recs, add)
	checkSeriesPositions(cat, idx, add)
	checkGenresSorted(cat, idx, add)
	checkCreditPairs(cat, idx, add)
	checkSidecarUniqueness(cat, idx, add)
	checkCharacters(cat, idx, add)
	checkRecaps(cat, idx, add)

	sortProblems(probs)
	sortProblems(warns)

	return Result{Problems: probs, Warnings: warns, Catalog: cat}
}

func sortProblems(ps []Problem) {
	sort.Slice(ps, func(i, j int) bool {
		if ps[i].Path != ps[j].Path {
			return ps[i].Path < ps[j].Path
		}
		return ps[i].Msg < ps[j].Msg
	})
}

// familyOf maps a data-relative path onto the pack family whose root it sits
// under. ok is false for a path under no family root.
func familyOf(rel string) (pack.Family, bool) {
	root, _, ok := strings.Cut(rel, "/")
	if !ok {
		return "", false
	}
	for _, d := range pack.Families() {
		if d.Family.Root() == root {
			return d.Family, true
		}
	}
	return "", false
}

// familyRoots lists the family root directories, for the message a file
// belonging to none of them gets.
func familyRoots() string {
	roots := make([]string, 0, 4)
	for _, d := range pack.Families() {
		roots = append(roots, d.Family.Root()+"/")
	}
	return strings.Join(roots, ", ")
}

// loadLegacyFamily reports a family that is not in the pack layout. It is all
// that remains of the file-per-entity reader: the tree was converted once
// (cmd/metamigrate) and every writer refuses the old layout outright
// (pack.ErrLegacyLayout), so a family still shaped that way is a problem to
// report, not a second walker to keep working.
//
// It names the file that identified the layout, because the same detection
// answers "legacy" for a pack file the reader could not recognize - an
// unreadable or wrongly-shaped first file - and the fix for that is not the
// migration.
func (l *loader) loadLegacyFamily(f pack.Family, rels []string) {
	if len(rels) == 0 {
		return
	}
	l.add(f.Root(), "family is not in the pack layout (%d file(s), the first being %s): "+
		"convert the tree with `go run ./cmd/metamigrate`. A pack is %s/<bound>.json holding an "+
		"\"entries\" object; if this family is meant to be one already, that file is what does not read as a pack",
		len(rels), rels[0], f.Root())
}

// jsonFiles lists every .json file under dir, sorted. Every one of them has to
// be accounted for: a file the pack listing does not hold is reported rather
// than skipped, which is what keeps a stray record from sitting in the tree
// unvalidated.
func jsonFiles(dir string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.EqualFold(filepath.Ext(path), ".json") {
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

// relSlash returns path relative to dir with forward slashes.
func relSlash(dir, path string) string {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		rel = path
	}
	return filepath.ToSlash(rel)
}
