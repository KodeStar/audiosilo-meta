package check

import (
	"os"
	"path/filepath"
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

// A pack the validation read must not be read again by the store. Deleting the
// file after the load is what proves it: a second read would fail, so the entry
// still being there can only have come from the shared parse.
func TestLoadStoreLeavesItsParseForTheStore(t *testing.T) {
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
	entry, ok, err := s.Get(pack.FamilyPeople, "author-one")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || len(entry) == 0 {
		t.Fatal("the store re-read a pack the load had already parsed")
	}
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
