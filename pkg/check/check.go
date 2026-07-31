// Package check loads the data/ tree, validates every record against its JSON
// Schema, and enforces the cross-record rules (placement, key/id agreement,
// referential integrity, global uniqueness, chapter ordering, series
// positions). It returns the discovered problems, any advisory warnings, and,
// best-effort, the loaded Catalog so metabuild can reuse the same load.
//
// Load handles both storage layouts, detected per family (pack.DetectLayout):
// the range-packed layout pkg/pack defines, and - for the migration window
// only - the file-per-entity layout in legacy.go. A mixed tree is legal, so a
// family can convert on its own. Whichever walker reads a family, the resulting
// model.Catalog is the same, and every cross-record rule in rules.go runs on
// that Catalog without knowing which layout produced it.
//
// This package is PUBLIC API: it is consumed by the sibling audiosilo-sidecars
// tool as an ordinary module dependency, so its exported surface is a contract.
package check

import (
	"fmt"
	"sort"
	"strings"

	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// Problem is one validation failure, tied to the file it came from.
//
// Path names the smallest thing that can be wrong. In the legacy layout that is
// a file. In the pack layout a file holds many entities, so Path carries the
// entry too, in the form model.PackLocation renders:
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
// path (file, or pack entry) it was read from, so it can be attached to its
// work and reported against after all works are read.
type recordWithPath struct {
	rec      *model.Recording
	workSlug string
	path     string
	// attached reports that the walker already hung this recording off its
	// work. A pack works entry is a composite, so its recordings arrive
	// attached; a legacy recording is a separate file and is attached by Load
	// once every work has been read.
	attached bool
}

// pathIndex remembers where each entity was loaded from, for later problem
// reporting during cross-record checks. Both walkers fill it, so the rules in
// rules.go report identically whichever layout a family is in.
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
	// walker only to spot files the listing does not account for. Everything
	// else - including a file under no family root at all - goes to the legacy
	// reader, which reports an unrecognized location.
	packFiles := map[pack.Family][]string{}
	var legacyFiles []string
	for _, abs := range files {
		rel := relSlash(dir, abs)
		if f, ok := familyOf(rel); ok && layouts[f] == pack.LayoutPack {
			packFiles[f] = append(packFiles[f], rel)
			continue
		}
		legacyFiles = append(legacyFiles, rel)
	}

	recs := l.loadLegacy(dir, legacyFiles)
	for _, def := range pack.Families() {
		if layouts[def.Family] != pack.LayoutPack {
			continue
		}
		// A pack works entry composes its own recordings, so these arrive
		// already attached to their work and only need reporting paths.
		recs = append(recs, l.loadPackFamily(dir, def, packFiles[def.Family])...)
	}

	cat, idx := l.cat, l.idx
	workByID := map[string]*model.Work{}
	for _, w := range cat.Works {
		if _, dup := workByID[w.ID]; !dup {
			workByID[w.ID] = w
		}
	}
	// Attach legacy recordings to their parent works (integrity flags orphans
	// below). Pack recordings are already attached, so they are skipped here.
	for _, pr := range recs {
		if pr.attached {
			continue
		}
		if w := workByID[pr.workSlug]; w != nil {
			w.Recordings = append(w.Recordings, pr.rec)
		}
	}

	checkIntegrity(cat, workByID, recs, idx, add)
	checkUniqueness(cat, recs, idx, add)
	checkChapters(recs, add)
	checkSeriesPositions(cat, idx, add)
	checkGenresSorted(cat, idx, add)
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
