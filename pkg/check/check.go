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
// It walks the tree ONCE: the pack listing, the layout detection and the file
// accounting all come out of that one walk (pack.Listing). A caller that is also
// WRITING calls LoadStore instead and hands over its pack.Store, so the two
// share the walk and the parse as well.
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
	dir string
	// rdr is the parse cache this load SHARES with a writer (a pack.Store, via
	// LoadStore): a pack the store has already parsed is not parsed again, and
	// one this load parses is left there for the store. It is nil for a plain
	// Load, which shares with nobody and keeps no pack it has finished with.
	rdr     *pack.Reader
	cat     *model.Catalog
	idx     *pathIndex
	schemas schemaSet
	add     addFunc
	warn    addFunc
}

// Load walks dir, validates it, and returns the result. dir is the data root.
//
// Nothing else is reading the tree, so nothing is kept: a pack is parsed,
// validated and released as the walk moves past it, and the load's peak memory
// is one pack plus the Catalog it is building. A caller that is also WRITING
// wants its parses kept and shared - that is LoadStore.
func Load(dir string) Result {
	lst, err := pack.List(dir)
	if err != nil {
		return Result{Problems: []Problem{{Path: dir, Msg: err.Error()}}}
	}
	return load(lst, nil)
}

// LoadStore validates the tree store s was opened on, sharing the store's walk
// and its parsed packs.
//
// It is Load with one difference: nothing is read or parsed twice. A writer
// validates the catalogue before it plans (that is where its identity maps come
// from) and then reads the same packs back through the store to compose its
// entries, which used to mean every pack the run touched was read and parsed
// once for each. Here the two share one Reader, so whichever side reaches a pack
// first is the only side that parses it.
//
// The tree it validates is the one the store saw at Open, which is exactly the
// tree the store's reads and writes are planned against. Once a Flush has made
// that walk stale, LoadStore takes a fresh one and re-reads everything - so
// post-write validation is a full, independent load, as it must be.
func LoadStore(s *pack.Store) Result {
	lst := s.Listing()
	if lst == nil {
		return Load(s.Dir())
	}
	return load(lst, s.Reader())
}

// load validates a walked tree. rdr is the parse cache the load shares with a
// writer, or nil when it shares with nobody and each pack is released as soon
// as it has been validated.
func load(lst *pack.Listing, rdr *pack.Reader) Result {
	dir := lst.Dir()
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

	layouts, err := lst.Layouts()
	if err != nil {
		return Result{Problems: []Problem{{Path: dir, Msg: err.Error()}}}
	}

	l := &loader{dir: dir, rdr: rdr, cat: &model.Catalog{}, idx: newPathIndex(), schemas: schemas, add: add, warn: warn}

	// A file under no family root at all belongs to nothing.
	for _, rel := range lst.Stray() {
		add(rel, "unrecognized location (not under any of the %s roots)", familyRoots())
	}

	var recs []recordWithPath
	for _, def := range pack.Families() {
		// A pack-layout family is read from its pack listing; the walk's own
		// file list is handed over too, to spot the files that listing does not
		// account for.
		if layouts[def.Family] != pack.LayoutPack {
			l.loadLegacyFamily(def.Family, lst.Files(def.Family))
			continue
		}
		// A works entry composes its own recordings, so these arrive already
		// attached to their work and only need reporting paths.
		recs = append(recs, l.loadPackFamily(def, lst.Tree(def.Family), lst.Files(def.Family))...)
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

