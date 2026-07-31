package pack

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func refs(f Family, pairs ...[2]string) []PackRef {
	out := make([]PackRef, 0, len(pairs))
	for _, p := range pairs {
		out = append(out, PackRef{Family: f, Dir: p[0], Bound: p[1]})
	}
	return out
}

func TestPackRefPath(t *testing.T) {
	cases := []struct {
		ref  PackRef
		want string
	}{
		{PackRef{Family: FamilyWorks, Dir: MinBound, Bound: MinBound}, "works/0/0.json"},
		{PackRef{Family: FamilyWorks, Dir: "harry-potter", Bound: "harry-potter"}, "works/harry-potter/harry-potter.json"},
		{PackRef{Family: FamilyPeople, Bound: MinBound}, "people/0.json"},
		{PackRef{Family: FamilyWorksCommunity, Dir: MinBound, Bound: "ab"}, "works-community/0/ab.json"},
	}
	for _, c := range cases {
		if got := c.ref.Path(); got != c.want {
			t.Errorf("Path() = %q, want %q", got, c.want)
		}
	}
}

func TestTreeLookupCoveringPack(t *testing.T) {
	tr := NewTree(FamilyWorks, refs(FamilyWorks,
		[2]string{MinBound, MinBound},
		[2]string{MinBound, "dune"},
		[2]string{"harry-potter", "harry-potter"},
		[2]string{"harry-potter", "moby-dick"},
	))
	cases := []struct{ slug, wantBound string }{
		{"0", MinBound},          // the reserved bound is inclusive
		{"0abc", MinBound},       // a literal slug starting with 0
		{"aardvark", MinBound},   // below the second bound
		{"dune", "dune"},         // a slug exactly equal to a bound
		{"dune-messiah", "dune"}, // inside the second range
		{"harry-potter", "harry-potter"},
		{"lord-of-the-rings", "harry-potter"},
		{"moby-dick", "moby-dick"},
		{"zzz", "moby-dick"}, // the last pack is unbounded above
	}
	for _, c := range cases {
		ref, ok := tr.Lookup(c.slug)
		if !ok {
			t.Fatalf("Lookup(%q): not found", c.slug)
		}
		if ref.Bound != c.wantBound {
			t.Errorf("Lookup(%q) = %q, want %q", c.slug, ref.Bound, c.wantBound)
		}
	}
}

func TestTreeLookupEmpty(t *testing.T) {
	if _, ok := NewTree(FamilyPeople, nil).Lookup("anyone"); ok {
		t.Fatal("Lookup on an empty tree reported a pack")
	}
	if got := NewTree(FamilyPeople, nil).Index("anyone"); got != -1 {
		t.Fatalf("Index on an empty tree = %d, want -1", got)
	}
}

// A tree whose lowest bound is not the reserved minimum is malformed; lookup
// still has to answer, and it answers with the lowest pack.
func TestTreeLookupBelowLowestBound(t *testing.T) {
	tr := NewTree(FamilyPeople, refs(FamilyPeople, [2]string{"", "mm"}, [2]string{"", "zz"}))
	ref, ok := tr.Lookup("aa")
	if !ok || ref.Bound != "mm" {
		t.Fatalf("Lookup(aa) = %v/%v, want the lowest pack mm", ref.Bound, ok)
	}
}

func TestTreeRange(t *testing.T) {
	tr := NewTree(FamilyPeople, refs(FamilyPeople,
		[2]string{"", MinBound}, [2]string{"", "mm"}, [2]string{"", "zz"}))
	cases := []struct {
		i      int
		lo, hi string
	}{
		{0, MinBound, "mm"},
		{1, "mm", "zz"},
		{2, "zz", ""},
	}
	for _, c := range cases {
		lo, hi, ok := tr.Range(c.i)
		if !ok || lo != c.lo || hi != c.hi {
			t.Errorf("Range(%d) = %q,%q,%v want %q,%q,true", c.i, lo, hi, ok, c.lo, c.hi)
		}
	}
	if _, _, ok := tr.Range(3); ok {
		t.Error("Range past the end reported ok")
	}
}

func TestCovers(t *testing.T) {
	cases := []struct {
		lo, hi, slug string
		want         bool
	}{
		{MinBound, "mm", "0", true}, // the bound is inclusive
		{MinBound, "mm", "ll", true},
		{MinBound, "mm", "mm", false}, // the upper bound is exclusive
		{"mm", "", "zzz", true},       // unbounded above
		{"mm", "", "aa", false},
	}
	for _, c := range cases {
		if got := Covers(c.lo, c.hi, c.slug); got != c.want {
			t.Errorf("Covers(%q,%q,%q) = %v, want %v", c.lo, c.hi, c.slug, got, c.want)
		}
	}
}

func TestTreeDirs(t *testing.T) {
	tr := NewTree(FamilyWorks, refs(FamilyWorks,
		[2]string{MinBound, MinBound},
		[2]string{MinBound, "dune"},
		[2]string{"harry-potter", "harry-potter"},
	))
	if got, want := tr.Dirs(), []string{MinBound, "harry-potter"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Dirs() = %v, want %v", got, want)
	}
	if got := tr.DirPacks(MinBound); len(got) != 2 {
		t.Errorf("DirPacks(0) = %d packs, want 2", len(got))
	}
}

func TestReadTree(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "works", "0", "0.json"), "{}")
	writeFile(t, filepath.Join(dir, "works", "0", "dune.json"), "{}")
	writeFile(t, filepath.Join(dir, "works", "harry-potter", "harry-potter.json"), "{}")
	writeFile(t, filepath.Join(dir, "people", "0.json"), "{}")

	tr, err := ReadTree(dir, FamilyWorks)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, p := range tr.Packs() {
		got = append(got, p.Path())
	}
	want := []string{"works/0/0.json", "works/0/dune.json", "works/harry-potter/harry-potter.json"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("works packs = %v, want %v", got, want)
	}

	flat, err := ReadTree(dir, FamilyPeople)
	if err != nil {
		t.Fatal(err)
	}
	if flat.Len() != 1 || flat.Packs()[0].Dir != "" || flat.Packs()[0].Bound != MinBound {
		t.Errorf("people packs = %v, want one flat 0 pack", flat.Packs())
	}

	missing, err := ReadTree(dir, FamilySeries)
	if err != nil {
		t.Fatal(err)
	}
	if missing.Len() != 0 {
		t.Errorf("missing family = %d packs, want 0", missing.Len())
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
