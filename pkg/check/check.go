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
// WRITING calls LoadStore instead and hands over its pack.Store, so the walk is
// shared too. What is never shared is residency: a pack is released as soon as
// it has been validated, so validating a tree costs one pack at a time however
// it was entered.
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
	// rdr is a writer's parse cache (a pack.Store's, via LoadStore), READ but
	// never filled: a pack the store has already parsed is taken from it, and a
	// pack this load reads itself is released once validated rather than left
	// there, so validating a tree never grows a writer's residency. It is nil for
	// a plain Load, which shares with nobody.
	rdr     *pack.Reader
	cat     *model.Catalog
	idx     *pathIndex
	schemas schemaSet
	add     addFunc
	warn    addFunc
}

// Load walks dir, validates it, and returns the result. dir is the data root.
//
// Nothing is kept: a pack is parsed, validated and released as the walk moves
// past it, so the load's peak memory is one pack plus the Catalog it is
// building. A caller that is also WRITING calls LoadStore, which shares the
// writer's walk - and the same release-as-you-go applies there.
func Load(dir string) Result {
	lst, err := pack.List(dir)
	if err != nil {
		return Result{Problems: []Problem{{Path: dir, Msg: err.Error()}}}
	}
	return load(lst, nil)
}

// LoadStore validates the tree store s was opened on, over the store's own walk
// of it and its already-parsed packs.
//
// It is Load without the second walk. A writer validates the catalogue before it
// plans (that is where its identity maps come from) and then writes into the
// same tree, which used to mean walking it twice; here both halves of the run
// share the walk the store took at Open, and a pack the store has already parsed
// is validated from that parse rather than read again.
//
// AS-OF-OPEN, deliberately. This validates the tree the store's reads and writes
// are planned against, which is the tree as it was when the store was opened,
// not as it is at the moment of the call:
//
//   - the file SET is the store's Open-time walk, so a file created or deleted
//     since then is not part of what is validated;
//   - a pack the store has already read is validated from those bytes, so if
//     something replaced that file since, the replacement is not what is
//     checked.
//
// That is the right contract for its purpose - a writer must validate what it is
// planning against - but it is not "the tree is valid right now". Nothing else
// may be writing to the tree while a store is open, which is what makes the two
// the same in practice; a caller that needs an independent, as-of-now answer
// calls Load. Once a Flush has made the store's walk stale, LoadStore takes a
// fresh one and re-reads everything, so post-write validation IS a full
// independent load.
func LoadStore(s *pack.Store) Result {
	lst := s.Listing()
	if lst == nil {
		return Load(s.Dir())
	}
	return load(lst, s.Reader())
}

// load validates a walked tree. rdr is a writer's parse cache to read packs
// from, or nil. Either way each pack this load reads itself is released as soon
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
	checkRegionalPublishers(recs, add)
	checkChapters(recs, add)
	checkSeriesPositions(cat, idx, add)
	checkGenresSorted(cat, idx, add)
	checkCreditPairs(cat, idx, add)
	checkPersonSlug(cat, idx, add)
	checkSidecarUniqueness(cat, idx, add)
	checkCharacters(cat, idx, add)
	checkRecaps(cat, idx, add)

	// Advisories (warn, never fail): the shapes a bulk import can produce that
	// no rule can call wrong on its own evidence. See advisories.go.
	checkCrossLanguageRecordings(cat, idx, warn)
	checkHonorificPersonPairs(cat, idx, warn)
	checkIdentityEqualWorks(cat, idx, warn)
	checkOrphanPeople(cat, recs, idx, warn)
	checkSidecarPositionScale(cat, idx, warn)

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
