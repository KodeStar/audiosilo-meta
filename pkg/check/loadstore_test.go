package check

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// LoadStore exists to save a read, not to change an answer. These cases pin
// that: whatever the tree looks like - well-formed, holding strays, misfiled,
// unreadable, half-absent, still legacy - loading it through a store must
// produce exactly what loading it by path does, down to the problem strings and
// the catalog.

// loadCase is one tree both loads are run against, plus what it is there to
// produce. The expectations are not the point of the comparison - they keep a
// case from quietly becoming a clean tree, which would make the comparison
// prove nothing.
type loadCase struct {
	files    map[string]string
	problems bool
	warnings bool
}

// loadCases are the trees both loads are compared on, each exercising a
// different shape the walker has to account for.
func loadCases() map[string]loadCase {
	tooDeep := packValid()
	tooDeep["works/0/0/deeper.json"] = packOf(map[string]string{"book-two": pkWork("book-two")})

	stray := packValid()
	stray["loose.json"] = `{"id":"nope"}`
	stray["people/notes/aside.json"] = `{"id":"nope"}`

	misfiled := packValid()
	misfiled["people/zz.json"] = packOf(map[string]string{"aaron-early": pkAuthorOne})

	unreadable := packValid()
	unreadable["series/0.json"] = `{"entries": {`

	legacy := packValid()
	delete(legacy, "series/0.json")
	legacy["series/se/series-one.json"] = pkSeriesOne

	absent := packValid()
	delete(absent, "works-community/0/0.json")

	oversized := packValid()
	oversized["works/0/0.json"] = packOf(map[string]string{
		"book-one": composite(bigWork("book-one", 600_000), map[string]string{"rec-one": pkRecOne}),
	})

	return map[string]loadCase{
		"valid":      {files: packValid()},
		"too deep":   {files: tooDeep, problems: true},
		"stray":      {files: stray, problems: true},
		"misfiled":   {files: misfiled, problems: true},
		"unreadable": {files: unreadable, problems: true},
		"legacy":     {files: legacy, problems: true},
		"absent":     {files: absent},
		"oversized":  {files: oversized, warnings: true},
		"empty":      {},
	}
}

// TestLoadStoreMatchesLoad is the before/after comparison the shared read has to
// survive: same problems, same advisories, same catalog, on every tree shape.
func TestLoadStoreMatchesLoad(t *testing.T) {
	for name, tc := range loadCases() {
		t.Run(name, func(t *testing.T) {
			byPath := t.TempDir()
			writeTree(t, byPath, tc.files)
			viaStore := t.TempDir()
			writeTree(t, viaStore, tc.files)

			want := Load(byPath)
			if got := len(want.Problems) > 0; got != tc.problems {
				t.Fatalf("case produces problems = %v, want %v: %s", got, tc.problems, joinProblems(want.Problems))
			}
			if got := len(want.Warnings) > 0; got != tc.warnings {
				t.Fatalf("case produces warnings = %v, want %v: %s", got, tc.warnings, joinProblems(want.Warnings))
			}
			s, err := pack.Open(viaStore)
			if err != nil {
				t.Fatal(err)
			}
			got := LoadStore(s)

			if a, b := joinProblems(want.Problems), joinProblems(got.Problems); a != b {
				t.Errorf("problems differ\nLoad:\n%s\nLoadStore:\n%s", a, b)
			}
			if a, b := joinProblems(want.Warnings), joinProblems(got.Warnings); a != b {
				t.Errorf("warnings differ\nLoad:\n%s\nLoadStore:\n%s", a, b)
			}
			if a, b := catalogDigest(t, want.Catalog), catalogDigest(t, got.Catalog); a != b {
				t.Errorf("catalog differs\nLoad:\n%s\nLoadStore:\n%s", a, b)
			}
		})
	}
}

// A pack the validation read is NOT left with the store. Keeping it would save
// the store a re-read of the few packs a run writes to and cost it the whole
// tree resident for the rest of the run; declining is what keeps a validating
// writer's memory where it was before the walk was shared. Deleting the file
// after the load is what proves it: the store now finds nothing there, exactly
// as it would have without a shared reader at all.
func TestLoadStoreKeepsNoPackTheStoreDidNotRead(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, packValid())
	s, err := pack.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if res := LoadStore(s); !res.OK() {
		t.Fatalf("valid tree: %v", res.Problems)
	}
	if err := os.Remove(filepath.Join(dir, "people", "0.json")); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.Get(pack.FamilyPeople, "author-one"); err != nil || ok {
		t.Fatalf("the load left its parse in the store's reader (ok=%v err=%v)", ok, err)
	}
}

// The retention regression, measured. Whatever the shared walk saves, it may not
// cost residency: validating through a store must hold no more live memory than
// validating by path, which held one pack at a time. Holding every parse (and
// the canonical render memo the cap check leaves on each) made this several
// times Load's on a real tree.
func TestLoadStoreRetainsNoMoreThanLoad(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, bulkyTree(48, 120_000))

	// Warm up: the first load compiles the schemas, and that cost is nobody's
	// retention but it would land on whichever measurement came first.
	if res := Load(dir); !res.OK() {
		t.Fatalf("fixture tree is not valid:\n%s", joinProblems(res.Problems))
	}

	byPath := heldHeap(func() any { return Load(dir) })
	viaStore := heldHeap(func() any {
		s, err := pack.Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		return [2]any{s, LoadStore(s)}
	})
	// Both retain the Catalog they built, which is the bulk of a legitimate
	// load. The margin is for that shared baseline moving a little between the
	// two runs, not for a second copy of the tree - which was 3x, not 1.5x.
	if limit := byPath * 3 / 2; viaStore > limit {
		t.Errorf("LoadStore holds %.1fMB live, Load holds %.1fMB: the shared reader is retaining the tree",
			float64(viaStore)/1e6, float64(byPath)/1e6)
	} else {
		t.Logf("live after load: byPath=%.1fMB viaStore=%.1fMB", float64(byPath)/1e6, float64(viaStore)/1e6)
	}
}

// heldHeap runs f and reports how much live heap whatever it returned is
// holding: the heap after it, minus the heap before, with the value kept alive
// across the measurement so nothing it owns can be collected first.
func heldHeap(f func() any) uint64 {
	before := liveHeap()
	v := f()
	after := liveHeap()
	runtime.KeepAlive(v)
	if after < before {
		return 0
	}
	return after - before
}

// liveHeap is the live heap after collection - what is still reachable, not what
// has been allocated.
func liveHeap() uint64 {
	runtime.GC()
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapAlloc
}

// bulkyTree returns a valid people family of n packs of roughly size bytes
// each, which is what makes a per-pack retention difference measurable.
func bulkyTree(n, size int) map[string]string {
	const perEntry = 600
	files := map[string]string{}
	slug := 0
	for p := range n {
		entries := map[string]string{}
		first := ""
		for range size / perEntry {
			id := fmt.Sprintf("person-%06d", slug)
			slug++
			if first == "" {
				first = id
			}
			entries[id] = `{"id":"` + id + `","license":"CC0-1.0","name":"` +
				strings.Repeat("Na", perEntry/2-120) + `","sources":[{"type":"user"}]}`
		}
		name := first
		if p == 0 {
			name = "0"
		}
		files["people/"+name+".json"] = packOf(entries)
	}
	return files
}

// And the other direction: a pack the store read first is not read again by the
// validation.
func TestLoadStoreUsesThePacksTheStoreRead(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, packValid())
	s, err := pack.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Get(pack.FamilyPeople, "author-one"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "people", "0.json")); err != nil {
		t.Fatal(err)
	}
	res := LoadStore(s)
	if !res.OK() {
		t.Fatalf("the load re-read a pack the store had already parsed: %v", res.Problems)
	}
	if len(res.Catalog.People) != 2 {
		t.Fatalf("people = %d, want 2", len(res.Catalog.People))
	}
}

// After a Flush the store's walk describes a tree that no longer exists, so
// LoadStore has to take a fresh one - which is what makes it usable as a
// writer's POST-write validation.
func TestLoadStoreAfterFlushSeesTheWrites(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, packValid())
	s, err := pack.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Upsert(pack.FamilyWorks, "book-two", []byte(pkWork("book-two"))); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	res := LoadStore(s)
	if !res.OK() {
		t.Fatalf("after flush: %v", res.Problems)
	}
	if len(res.Catalog.Works) != 2 {
		t.Fatalf("works = %d, want 2 (the flushed write is missing)", len(res.Catalog.Works))
	}
	if d := catalogDigest(t, res.Catalog); d != catalogDigest(t, Load(dir).Catalog) {
		t.Error("post-flush LoadStore disagrees with a fresh Load")
	}
}

// A queued write is not on disk, and LoadStore validates DISK. A create and an
// overwrite of an existing entry, both with garbage, must change nothing about
// what the validation says or what lands in the Catalog.
func TestLoadStoreIgnoresQueuedWrites(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, packValid())
	want := Load(dir)

	s, err := pack.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Upsert(pack.FamilyPeople, "zz-new", json.RawMessage(`{"id":"WRONG"}`)); err != nil {
		t.Fatal(err)
	}
	if err := s.Upsert(pack.FamilyPeople, "author-one", json.RawMessage(`{"id":"nope","license":"MIT"}`)); err != nil {
		t.Fatal(err)
	}
	got := LoadStore(s)
	if a, b := joinProblems(want.Problems), joinProblems(got.Problems); a != b {
		t.Errorf("a queued write changed what LoadStore validated\nLoad:\n%s\nLoadStore:\n%s", a, b)
	}
	if catalogDigest(t, want.Catalog) != catalogDigest(t, got.Catalog) {
		t.Error("a queued write reached the catalog")
	}
}

// The store reads a MISSING file as an empty pack - the stand-in a first write
// into a new family is composed into. That must never be handed to the
// validation as a parse: a listed pack that is not there is a read failure to
// report, and reporting "pack holds no entries" instead named the wrong problem
// on a file that does not exist.
func TestLoadStoreTellsAMissingPackFromAnEmptyOne(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, packValid())
	s, err := pack.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "people", "0.json")); err != nil {
		t.Fatal(err)
	}
	// The store touches the now-missing pack, caching the empty stand-in for it.
	if _, _, err := s.Get(pack.FamilyPeople, "author-one"); err != nil {
		t.Fatal(err)
	}
	// The walk still lists the pack (the file set is as-of-Open, by design), so
	// the load has to try to read it - and what it must say is that it could not.
	probs := joinProblems(LoadStore(s).Problems)
	if strings.Contains(probs, "holds no entries") {
		t.Errorf("a missing pack was reported as an empty one:\n%s", probs)
	}
	if !strings.Contains(probs, "people/0.json: read:") {
		t.Errorf("the missing pack was not reported as unreadable:\n%s", probs)
	}
}

// LoadStore is AS-OF-OPEN, and this is the case that pins it: a pack the store
// has already read is validated from those bytes, so a file replaced on disk
// since is not what gets checked. That is the contract a writer needs - it must
// validate what it is planning against - and it is documented on LoadStore
// precisely because it is not "the tree is valid right now". A caller that wants
// an independent, as-of-now answer calls Load, and this asserts Load still gives
// one.
func TestLoadStoreValidatesTheTreeTheStoreOpened(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, packValid())
	s, err := pack.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Get(pack.FamilyPeople, "author-one"); err != nil {
		t.Fatal(err)
	}
	// Something replaces the pack with a schema-invalid one.
	bad := `{"entries": {"author-one": {"id": "author-one", "license": "MIT"}}}`
	if err := os.WriteFile(filepath.Join(dir, "people", "0.json"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	if res := LoadStore(s); !res.OK() {
		t.Errorf("LoadStore did not validate the tree the store opened:\n%s", joinProblems(res.Problems))
	}
	if res := Load(dir); res.OK() {
		t.Error("Load certified a tree that is invalid on disk")
	}
	// And once the store has flushed, its walk is stale and LoadStore reads the
	// tree afresh - so a writer's post-write validation is never as-of-Open.
	if _, err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "people", "0.json"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	if res := LoadStore(s); res.OK() {
		t.Error("post-flush LoadStore still answered from the stale walk")
	}
}
