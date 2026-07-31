package pack

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/canonical"
)

// withCaps narrows a family's caps for the duration of a test, so a handful of
// small entries can exercise the same split paths 512KB would.
func withCaps(t *testing.T, f Family, c Caps) {
	t.Helper()
	orig := defs[f]
	nd := orig
	nd.Caps = c
	defs[f] = nd
	t.Cleanup(func() { defs[f] = orig })
}

func mustFlush(t *testing.T, s *Store) Written {
	t.Helper()
	w, err := s.Flush()
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// packPaths lists a family's pack files on disk, sorted.
func packPaths(t *testing.T, dir string, f Family) []string {
	t.Helper()
	got, err := jsonFilesUnder(dir, f.Root())
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func readPack(t *testing.T, dir, rel string) *File {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	f, err := Parse(raw)
	if err != nil {
		t.Fatalf("%s: %v", rel, err)
	}
	return f
}

// A run has to see its own writes: the importer composes a record over several
// steps and reads it back between them.
func TestStoreGetSeesQueuedWrites(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.Get(FamilyPeople, "ann-doe"); ok {
		t.Fatal("Get found an entry in an empty store")
	}
	if err := s.Upsert(FamilyPeople, "ann-doe", json.RawMessage(`{"id":"ann-doe"}`)); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.Get(FamilyPeople, "ann-doe")
	if err != nil || !ok {
		t.Fatalf("Get after Upsert = %v/%v", ok, err)
	}
	if string(got) != `{"id":"ann-doe"}` {
		t.Errorf("Get = %s, want the queued entry", got)
	}
	if err := s.Delete(FamilyPeople, "ann-doe"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.Get(FamilyPeople, "ann-doe"); ok {
		t.Error("Get after Delete still found the entry")
	}
}

func TestStoreFlushCreatesTheReservedFirstPack(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Upsert(FamilyPeople, "ann-doe", json.RawMessage(`{"id":"ann-doe"}`)); err != nil {
		t.Fatal(err)
	}
	if err := s.Upsert(FamilyWorks, "dune", json.RawMessage(`{"id":"dune"}`)); err != nil {
		t.Fatal(err)
	}
	w := mustFlush(t, s)
	if !equalStrings(w.Wrote, []string{"people/0.json", "works/0/0.json"}) {
		t.Fatalf("wrote %v, want the two reserved first packs", w.Wrote)
	}
	if len(w.Deleted) != 0 {
		t.Errorf("deleted %v, want nothing", w.Deleted)
	}
	// works carries a directory level from day one; people starts flat.
	if got := packPaths(t, dir, FamilyWorks); !equalStrings(got, []string{"works/0/0.json"}) {
		t.Errorf("works = %v", got)
	}
	if got := packPaths(t, dir, FamilyPeople); !equalStrings(got, []string{"people/0.json"}) {
		t.Errorf("people = %v", got)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "people", "0.json"))
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := canonical.IsCanonical(raw); err != nil || !ok {
		t.Errorf("written pack is not canonical: %s", raw)
	}
}

// Reads after a flush come off disk, and the store stays usable.
func TestStoreReusableAfterFlush(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Upsert(FamilySeries, "dune", json.RawMessage(`{"id":"dune"}`)); err != nil {
		t.Fatal(err)
	}
	mustFlush(t, s)
	if _, ok, err := s.Get(FamilySeries, "dune"); err != nil || !ok {
		t.Fatalf("Get after Flush = %v/%v", ok, err)
	}
	if err := s.Delete(FamilySeries, "dune"); err != nil {
		t.Fatal(err)
	}
	w := mustFlush(t, s)
	if !equalStrings(w.Deleted, []string{"series/0.json"}) {
		t.Fatalf("deleted %v, want the emptied pack", w.Deleted)
	}
	if got := packPaths(t, dir, FamilySeries); len(got) != 0 {
		t.Errorf("series = %v, want nothing left", got)
	}
}

// A second flush of identical state writes nothing: metafmt and the importer
// have to be idempotent.
func TestStoreFlushIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	seed := func() *Store {
		s, err := Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 8; i++ {
			slug := fmt.Sprintf("p%02d", i)
			if err := s.Upsert(FamilyPeople, slug, json.RawMessage(`{"id":"`+slug+`"}`)); err != nil {
				t.Fatal(err)
			}
		}
		return s
	}
	first := mustFlush(t, seed())
	if len(first.Wrote) == 0 {
		t.Fatal("first flush wrote nothing")
	}
	second := mustFlush(t, seed())
	if len(second.Wrote) != 0 || len(second.Deleted) != 0 {
		t.Errorf("re-flushing identical state touched %v / %v", second.Wrote, second.Deleted)
	}
}

// The same inserts, applied in a different order, produce the same tree.
func TestStoreFlushIsDeterministic(t *testing.T) {
	withCaps(t, FamilyPeople, Caps{TargetSize: 200, HardSize: 400, Entries: 1000, DirPacks: 512})
	build := func(order []int) (string, []string) {
		dir := t.TempDir()
		s, err := Open(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, i := range order {
			slug := fmt.Sprintf("p%02d", i)
			if err := s.Upsert(FamilyPeople, slug, entryOf(120)); err != nil {
				t.Fatal(err)
			}
		}
		mustFlush(t, s)
		return dir, packPaths(t, dir, FamilyPeople)
	}
	forward := []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}
	backward := []int{9, 8, 7, 6, 5, 4, 3, 2, 1, 0}
	dirA, a := build(forward)
	dirB, b := build(backward)
	if !equalStrings(a, b) {
		t.Fatalf("insert order changed the tree: %v vs %v", a, b)
	}
	for _, p := range a {
		x, err := os.ReadFile(filepath.Join(dirA, filepath.FromSlash(p)))
		if err != nil {
			t.Fatal(err)
		}
		y, err := os.ReadFile(filepath.Join(dirB, filepath.FromSlash(p)))
		if err != nil {
			t.Fatal(err)
		}
		if string(x) != string(y) {
			t.Errorf("%s differs between runs", p)
		}
	}
}

func TestStoreFlushSplitsAnOverfullPack(t *testing.T) {
	withCaps(t, FamilyPeople, Caps{TargetSize: 150, HardSize: 300, Entries: 1000, DirPacks: 512})
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		if err := s.Upsert(FamilyPeople, fmt.Sprintf("p%02d", i), entryOf(100)); err != nil {
			t.Fatal(err)
		}
	}
	mustFlush(t, s)
	got := packPaths(t, dir, FamilyPeople)
	if len(got) < 3 {
		t.Fatalf("packs = %v, want the family split", got)
	}
	if got[0] != "people/0.json" {
		t.Errorf("lowest pack = %s, want the reserved bound", got[0])
	}
	// Every entry lands in the pack its bound points at.
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s2.Pending(FamilyPeople)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Empty() {
		t.Errorf("split tree is not well-formed: %+v", p)
	}
}

// A family over the per-directory pack cap gains the directory level.
func TestStoreFlushSplitsDirectories(t *testing.T) {
	withCaps(t, FamilySeries, Caps{TargetSize: 60, HardSize: 120, Entries: 1000, DirPacks: 3})
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		if err := s.Upsert(FamilySeries, fmt.Sprintf("s%02d", i), entryOf(60)); err != nil {
			t.Fatal(err)
		}
	}
	mustFlush(t, s)
	got := packPaths(t, dir, FamilySeries)
	if len(got) < 4 {
		t.Fatalf("packs = %v, want several", got)
	}
	if got[0] != "series/0/0.json" {
		t.Errorf("first pack = %s, want series/0/0.json", got[0])
	}
	tr, err := ReadTree(dir, FamilySeries)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range tr.Dirs() {
		packs := tr.DirPacks(d)
		if len(packs) > 3 {
			t.Errorf("directory %s holds %d packs, over the cap", d, len(packs))
		}
		if packs[0].Bound != d {
			t.Errorf("directory %s is not named by its first pack %s", d, packs[0].Bound)
		}
	}
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s2.Pending(FamilySeries)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Empty() {
		t.Errorf("directory-split tree is not well-formed: %+v", p)
	}
}

// Deleting every entry of the lowest pack cannot leave the family's lowest
// bound above the reserved minimum, or slugs below it would have no home.
func TestStoreFlushRebindsTheLowestPack(t *testing.T) {
	withCaps(t, FamilyPeople, Caps{TargetSize: 150, HardSize: 300, Entries: 1000, DirPacks: 512})
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "people", "0.json"), `{"entries":{"aa":{"id":"aa"}}}`)
	writeFile(t, filepath.Join(dir, "people", "mm.json"), `{"entries":{"mm":{"id":"mm"}}}`)

	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(FamilyPeople, "aa"); err != nil {
		t.Fatal(err)
	}
	mustFlush(t, s)
	if got := packPaths(t, dir, FamilyPeople); !equalStrings(got, []string{"people/0.json"}) {
		t.Fatalf("packs = %v, want the survivor rebound to 0", got)
	}
	if got := readPack(t, dir, "people/0.json").Slugs(); !equalStrings(got, []string{"mm"}) {
		t.Errorf("entries = %v, want [mm]", got)
	}
}

// A contributor may add an entry to the wrong pack; healing moves it and names
// where it belongs.
func TestStoreHealRelocatesMisplacedEntries(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "people", "0.json"), `{"entries":{"aa":{"id":"aa"},"zz":{"id":"zz"}}}`)
	writeFile(t, filepath.Join(dir, "people", "mm.json"), `{"entries":{"mm":{"id":"mm"}}}`)

	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.Pending(FamilyPeople)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Misplaced) != 1 || p.Misplaced[0].Slug != "zz" {
		t.Fatalf("Pending misplaced = %+v, want just zz", p.Misplaced)
	}
	if got, want := p.Misplaced[0].To.Path(), "people/mm.json"; got != want {
		t.Errorf("target = %s, want %s", got, want)
	}
	if got := p.Misplaced[0].String(); got == "" {
		t.Error("Misplaced.String is empty")
	}

	moved, err := s.Heal(FamilyPeople)
	if err != nil {
		t.Fatal(err)
	}
	if len(moved) != 1 {
		t.Fatalf("Heal moved %d entries, want 1", len(moved))
	}
	mustFlush(t, s)
	if got := readPack(t, dir, "people/0.json").Slugs(); !equalStrings(got, []string{"aa"}) {
		t.Errorf("0.json = %v, want [aa]", got)
	}
	if got := readPack(t, dir, "people/mm.json").Slugs(); !equalStrings(got, []string{"mm", "zz"}) {
		t.Errorf("mm.json = %v, want [mm zz]", got)
	}

	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	after, err := s2.Pending(FamilyPeople)
	if err != nil {
		t.Fatal(err)
	}
	if !after.Empty() {
		t.Errorf("healed tree still pending: %+v", after)
	}
}

func TestPendingReportsDueSplits(t *testing.T) {
	withCaps(t, FamilyPeople, Caps{TargetSize: 50, HardSize: 100, Entries: 1, DirPacks: 512})
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "people", "0.json"), `{"entries":{"aa":{"id":"aa"},"bb":{"id":"bb"}}}`)
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.Pending(FamilyPeople)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Packs) != 1 || p.Packs[0].Reason != "entry count" {
		t.Fatalf("Pending packs = %+v, want one entry-count split", p.Packs)
	}
	if p.Empty() {
		t.Error("Pending.Empty reported a tree with a due split as clean")
	}
	if p.Packs[0].String() == "" {
		t.Error("DueSplit.String is empty")
	}
}

func TestPendingReportsDueDirSplit(t *testing.T) {
	withCaps(t, FamilyPeople, Caps{TargetSize: 1 << 20, HardSize: 1 << 20, Entries: 1000, DirPacks: 1})
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "people", "0.json"), `{"entries":{"aa":{"id":"aa"}}}`)
	writeFile(t, filepath.Join(dir, "people", "mm.json"), `{"entries":{"mm":{"id":"mm"}}}`)
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.Pending(FamilyPeople)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Dirs) != 1 || p.Dirs[0].Packs != 2 {
		t.Fatalf("Pending dirs = %+v, want the flat family over its cap", p.Dirs)
	}
	if p.Dirs[0].String() == "" {
		t.Error("DueDirSplit.String is empty")
	}
}

func TestStoreLocate(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "people", "0.json"), `{"entries":{"aa":{"id":"aa"}}}`)
	writeFile(t, filepath.Join(dir, "people", "mm.json"), `{"entries":{"mm":{"id":"mm"}}}`)
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := s.Locate(FamilyPeople, "zz")
	if err != nil || ref.Path() != "people/mm.json" {
		t.Errorf("Locate(zz) = %s/%v, want people/mm.json", ref.Path(), err)
	}
	// An empty family locates to its reserved first pack.
	ref, err = s.Locate(FamilyWorks, "dune")
	if err != nil || ref.Path() != "works/0/0.json" {
		t.Errorf("Locate on an empty family = %s/%v, want works/0/0.json", ref.Path(), err)
	}
	if _, err := s.Locate("nope", "x"); err == nil {
		t.Error("Locate accepted an unknown family")
	}
	if err := s.Upsert("nope", "x", nil); err == nil {
		t.Error("Upsert accepted an unknown family")
	}
	if err := s.Delete("nope", "x"); err == nil {
		t.Error("Delete accepted an unknown family")
	}
	if s.Dir() != dir {
		t.Errorf("Dir() = %s, want %s", s.Dir(), dir)
	}
}
