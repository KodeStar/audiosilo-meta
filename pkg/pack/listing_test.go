package pack

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// A Listing replaced a walk that used to happen once per family per question,
// so what it has to prove is EQUIVALENCE: the tree it derives and the layout it
// detects must be what a fresh directory scan would have said, on every shape a
// data root can be in.

// listingTrees are the trees the equivalence is checked on: pack files at both
// depths, files that are no packs, and the odd spellings.
func listingTrees() map[string]map[string]string {
	return map[string]map[string]string{
		"flat and deep": {
			"people/0.json":            packOf1("ann-doe"),
			"people/bob.json":          packOf1("bob-roe"),
			"works/0/0.json":           packOf1("book-one"),
			"works/0/mid.json":         packOf1("mid-book"),
			"works/zed/zed.json":       packOf1("zed-book"),
			"works-community/0/0.json": packOf1("book-one"),
			"series/0.json":            packOf1("series-one"),
		},
		"too deep": {
			"works/0/0.json":        packOf1("book-one"),
			"works/0/0/deeper.json": packOf1("deep-book"),
		},
		// The upper-case spelling is a file of its own (a different base name,
		// since a case-insensitive filesystem would otherwise make it the same
		// file as 0.json): pkg/pack reads ".json" case-sensitively, so it names
		// no bound, but a listing still has to hold it.
		"not json": {
			"people/0.json":  packOf1("ann-doe"),
			"people/README":  "not json at all",
			"people/up.JSON": packOf1("case-doe"),
			"people/.json":   packOf1("dot-doe"),
			"series/00.json": packOf1("series-one"),
		},
		"stray": {
			"loose.json":    packOf1("nope"),
			"notes/x.json":  packOf1("nope"),
			"people/0.json": packOf1("ann-doe"),
		},
		"empty": {},
	}
}

// packOf1 renders a one-entry pack.
func packOf1(slug string) string { return `{"entries":{"` + slug + `":{"id":"` + slug + `"}}}` }

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestListingTreeMatchesReadTree pins the walk-derived tree against the
// directory-scan one it replaced.
func TestListingTreeMatchesReadTree(t *testing.T) {
	for name, files := range listingTrees() {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeFiles(t, dir, files)
			l, err := List(dir)
			if err != nil {
				t.Fatal(err)
			}
			for _, d := range Families() {
				want, err := ReadTree(dir, d.Family)
				if err != nil {
					t.Fatal(err)
				}
				if got := l.Tree(d.Family); !reflect.DeepEqual(got.Packs(), want.Packs()) {
					t.Errorf("%s tree = %v, ReadTree = %v", d.Family, got.Packs(), want.Packs())
				}
			}
		})
	}
}

// TestListingLayoutsMatchDetectLayout pins the walk-derived layouts (Detect,
// one walk of the tree) against the per-family detection (DetectLayout, a scan
// of one family root), legacy trees included. The two list a family's files by
// different routes and must never disagree about what layout they imply.
func TestListingLayoutsMatchDetectLayout(t *testing.T) {
	trees := listingTrees()
	trees["legacy"] = map[string]string{
		"people/an/ann-doe.json": `{"id":"ann-doe"}`,
		"works/0/0.json":         packOf1("book-one"),
	}
	for name, files := range trees {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeFiles(t, dir, files)
			want := map[Family]Layout{}
			for _, d := range Families() {
				lay, err := DetectLayout(dir, d.Family)
				if err != nil {
					t.Fatal(err)
				}
				want[d.Family] = lay
			}
			got, err := Detect(dir)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("Detect = %v, per-family DetectLayout = %v", got, want)
			}
		})
	}
}

// The listing is what a caller ACCOUNTS for, so it holds every JSON file under
// a family root - including the ones that are no packs, which is how pkg/check
// can report a file nothing reads.
func TestListingFilesHoldEverythingUnderARoot(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, listingTrees()["not json"])
	l, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"people/.json", "people/0.json", "people/up.JSON"}
	if got := l.Files(FamilyPeople); !reflect.DeepEqual(got, want) {
		t.Errorf("people files = %v, want %v", got, want)
	}
	// None of the odd spellings bounds a pack, so the tree holds only the one
	// real pack - the same answer a directory scan gives.
	if got := l.Tree(FamilyPeople); got.Len() != 1 || got.Packs()[0].Bound != "0" {
		t.Errorf("people tree = %v, want the single 0 pack", got.Packs())
	}
}

// A file under no family root belongs to nothing and has to be reportable.
func TestListingStray(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, listingTrees()["stray"])
	l, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"loose.json", "notes/x.json"}
	if got := l.Stray(); !reflect.DeepEqual(got, want) {
		t.Errorf("stray = %v, want %v", got, want)
	}
}

// TestListingAccountsForTheRedirectsFile pins the tree's one recognized non-pack
// file. It sits under no family root, so without this the accounting would report
// the slug tombstone table as a file belonging nowhere - on every metacheck run,
// forever - and metafmt would offer to salvage it.
func TestListingAccountsForTheRedirectsFile(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		RedirectsFile:   `{"people":{},"series":{},"works":{}}`,
		"people/0.json": packOf1("ann-doe"),
		// The exemption is the EXACT path: the same name inside a family root is
		// that family's problem, not this one.
		"people/" + RedirectsFile: `{"people":{}}`,
	})
	l, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := l.Aux(); !reflect.DeepEqual(got, []string{RedirectsFile}) {
		t.Errorf("aux = %v, want [%s]", got, RedirectsFile)
	}
	if got := l.Stray(); len(got) != 0 {
		t.Errorf("stray = %v, want none", got)
	}
	want := []string{"people/0.json", "people/" + RedirectsFile}
	if got := l.Files(FamilyPeople); !reflect.DeepEqual(got, want) {
		t.Errorf("people files = %v, want %v", got, want)
	}
	// The in-family copy is judged as that family's file, exactly as any other
	// name would be ("redirects" is a valid bound, so it reads as a pack there and
	// the reader reports it as one that holds no entries). Nothing about the data
	// root's exemption reaches inside a family.
	if got := l.Tree(FamilyPeople); got.Len() != 2 {
		t.Errorf("people tree = %v, want both files read as packs", got.Packs())
	}
	if !IsAuxFile(RedirectsFile) || IsAuxFile("people/"+RedirectsFile) || IsAuxFile("works/0/0.json") {
		t.Error("IsAuxFile does not match exactly the redirects file")
	}
}

// A data root that does not exist is an error to List - pkg/check reports it as
// one - but not to Open, which has to be able to create a tree from nothing.
func TestListMissingRoot(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope")
	if _, err := List(missing); err == nil {
		t.Fatal("List of a missing root returned no error")
	}
	s, err := Open(missing)
	if err != nil {
		t.Fatalf("Open of a missing root: %v", err)
	}
	for _, d := range Families() {
		if got := s.Layout(d.Family); got != LayoutAbsent {
			t.Errorf("%s layout = %v, want absent", d.Family, got)
		}
	}
	if err := s.Upsert(FamilyPeople, "ann-doe", []byte(`{"id":"ann-doe"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(missing, "people", "0.json")); err != nil {
		t.Fatalf("first write into a missing root: %v", err)
	}
}

// A data root, or a family root, that is a FILE is a broken tree, not an empty
// one. A walk alone cannot say so - handed a regular file it yields that file
// and stops, which reads as "no packs here" - so List states it separately and
// every reader inherits the refusal.
func TestListRefusesANonDirectoryRoot(t *testing.T) {
	t.Run("data root", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "data")
		if err := os.WriteFile(root, []byte("not a tree"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := List(root); err == nil {
			t.Fatal("List read a regular file as an empty data tree")
		} else if !strings.Contains(err.Error(), "not a directory") {
			t.Errorf("err = %v, want it to say the root is not a directory", err)
		}
		if _, err := Open(root); err == nil {
			t.Error("Open read a regular file as an empty data tree")
		}
	})
	t.Run("family root", func(t *testing.T) {
		dir := t.TempDir()
		writeFiles(t, dir, map[string]string{"works/0/0.json": packOf1("book-one")})
		if err := os.WriteFile(filepath.Join(dir, "people"), []byte("not a family"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := List(dir); err == nil {
			t.Fatal("List read a regular file as an absent people family")
		} else if !strings.Contains(err.Error(), "not a directory") {
			t.Errorf("err = %v, want it to say the family root is not a directory", err)
		}
		if _, err := Open(dir); err == nil {
			t.Error("Open read a regular file as an absent people family")
		}
		// And the per-family scan the writers use says the same thing.
		if _, err := jsonFilesUnder(dir, "people"); err == nil {
			t.Error("the family scan read a regular file as an absent family")
		}
		if _, err := ReadTree(dir, FamilyPeople); err == nil {
			t.Error("ReadTree read a regular file as an empty family")
		}
	})
}
