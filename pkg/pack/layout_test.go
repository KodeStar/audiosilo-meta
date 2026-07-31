package pack

import (
	"path/filepath"
	"testing"
)

func TestDetectLayout(t *testing.T) {
	dir := t.TempDir()
	// Pack layout.
	writeFile(t, filepath.Join(dir, "works", "0", "0.json"), `{"entries":{"dune":{"id":"dune"}}}`)
	// Legacy file-per-entity layout.
	writeFile(t, filepath.Join(dir, "people", "an", "ann-doe.json"), `{"id":"ann-doe"}`)
	// series/ is absent entirely.

	got, err := Detect(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := map[Family]Layout{
		FamilyWorks:          LayoutPack,
		FamilyPeople:         LayoutLegacy,
		FamilySeries:         LayoutAbsent,
		FamilyWorksCommunity: LayoutAbsent,
	}
	for f, w := range want {
		if got[f] != w {
			t.Errorf("%s = %s, want %s", f, got[f], w)
		}
	}
}

// A people family that has gained a directory level sits at the same depth as a
// legacy people record, so the wrapper - not the path - is the discriminator.
func TestDetectLayoutPackedFamilyWithDirectories(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "people", "0", "0.json"), `{"entries":{"ann-doe":{"id":"ann-doe"}}}`)
	l, err := DetectLayout(dir, FamilyPeople)
	if err != nil {
		t.Fatal(err)
	}
	if l != LayoutPack {
		t.Errorf("layout = %s, want pack", l)
	}
}

// The current live tree shape: works/<shard>/<slug>/work.json with sidecars
// alongside and no works-community family at all.
func TestDetectLayoutLegacyWorksTree(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "works", "du", "dune", "work.json"), `{"id":"dune"}`)
	writeFile(t, filepath.Join(dir, "works", "du", "dune", "characters.json"), `{"work":"dune"}`)
	writeFile(t, filepath.Join(dir, "works", "du", "dune", "recordings", "rec.json"), `{"id":"rec"}`)

	l, err := DetectLayout(dir, FamilyWorks)
	if err != nil {
		t.Fatal(err)
	}
	if l != LayoutLegacy {
		t.Errorf("layout = %s, want legacy", l)
	}
	if l, err := DetectLayout(dir, FamilyWorksCommunity); err != nil || l != LayoutAbsent {
		t.Errorf("works-community = %s/%v, want absent", l, err)
	}
}

func TestLayoutString(t *testing.T) {
	cases := map[Layout]string{LayoutAbsent: "absent", LayoutPack: "pack", LayoutLegacy: "legacy"}
	for l, want := range cases {
		if got := l.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", l, got, want)
		}
	}
}
