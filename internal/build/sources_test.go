package build

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kodestar/audiosilo-meta/internal/testpack"
)

// sources_test.go pins the property the repository split rests on: the ARTIFACT
// is the same whether the database is one tree or two.
//
// pkg/check's compose_test.go proves the catalogues match; this proves what a
// consumer actually receives does, which is the claim the cutover makes. At
// fixture scale here, and at real scale by hand against the seeded tree (the
// metamigrate equivalence-proof discipline, applied to the split).

// splitFixture is one small database in three shapes: the whole tree, its CC0
// core alone, and its CC BY-SA sidecars alone.
func splitFixture(t *testing.T) (whole, core, community map[string]string) {
	t.Helper()
	core = map[string]string{
		"people/ja/jane-doe.json":                   testpack.PersonJSON(t, "jane-doe", "Jane Doe"),
		"people/na/nate-narrator.json":              testpack.PersonJSON(t, "nate-narrator", "Nate Narrator"),
		"works/bo/book-one/work.json":               testpack.WorkJSON(t, "book-one", "Book One"),
		"works/bo/book-one/recordings/rec-one.json": testpack.RecJSON(t, "rec-one", "book-one"),
		"works/bt/book-two/work.json":               testpack.WorkJSON(t, "book-two", "Book Two"),
		"works/bt/book-two/recordings/rec-two.json": testpack.RecJSON(t, "rec-two", "book-two"),
		"series/se/series-one.json":                 testpack.SeriesJSON(t, "series-one", "Series One", "book-one@1", "book-two@2"),
	}
	community = map[string]string{
		"works/bo/book-one/characters.json": testpack.CharactersJSON(t, "book-one", "hero"),
		"works/bo/book-one/recaps.json":     testpack.RecapsJSON(t, "book-one", 3),
		"works/bt/book-two/characters.json": testpack.CharactersJSON(t, "book-two", "villain"),
	}
	whole = map[string]string{}
	for k, v := range core {
		whole[k] = v
	}
	for k, v := range community {
		whole[k] = v
	}
	return whole, core, community
}

// buildFrom compiles the sources into an artifact and returns its bytes. The
// timestamp is fixed, because meta(built_at) is the one value in the artifact
// that is not a function of the data.
func buildFrom(t *testing.T, s Sources) []byte {
	t.Helper()
	res := Load(s)
	if !res.OK() {
		t.Fatalf("load %+v reported problems: %v", s, res.Problems)
	}
	out := filepath.Join(t.TempDir(), "meta.sqlite")
	at := time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC)
	if err := Build(res.Catalog, out, at); err != nil {
		t.Fatalf("build %+v: %v", s, err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestComposedArtifactEqualsSingleTree is the EQUIVALENCE PROOF: splitting a
// tree's works-community family into a second root and composing it back with
// --community produces the artifact byte for byte.
func TestComposedArtifactEqualsSingleTree(t *testing.T) {
	whole, core, community := splitFixture(t)

	wholeDir := t.TempDir()
	testpack.Seed(t, wholeDir, whole)
	coreDir, comDir := t.TempDir(), t.TempDir()
	testpack.Seed(t, coreDir, core)
	testpack.Seed(t, comDir, community)

	single := buildFrom(t, Sources{Data: wholeDir})
	composed := buildFrom(t, Sources{Data: coreDir, Community: comDir})

	if !bytes.Equal(single, composed) {
		t.Fatalf("the artifact changed across the split: %d bytes as one tree, %d composed",
			len(single), len(composed))
	}
}

// TestLoadWithoutCommunityIsTheSingleTreeLoad pins the other half of the flag's
// contract: an empty Community is not "compose with nothing", it is the load
// metabuild has always done, over a root holding the whole database - which is
// what this repository is until the split lands.
func TestLoadWithoutCommunityIsTheSingleTreeLoad(t *testing.T) {
	whole, _, _ := splitFixture(t)
	dir := t.TempDir()
	testpack.Seed(t, dir, whole)

	res := Load(Sources{Data: dir})
	if !res.OK() {
		t.Fatalf("the whole tree reported problems: %v", res.Problems)
	}
	if len(res.Catalog.Characters) != 2 || len(res.Catalog.Recaps) != 1 {
		t.Errorf("sidecars did not load: characters=%d recaps=%d",
			len(res.Catalog.Characters), len(res.Catalog.Recaps))
	}
}

// TestLoadRefusesADanglingSidecarKey pins that the cross-tree existence rule
// reaches the builder: a community root keyed by a work the core does not hold
// fails the build rather than dropping the sidecar out of the artifact.
func TestLoadRefusesADanglingSidecarKey(t *testing.T) {
	_, core, _ := splitFixture(t)
	coreDir, comDir := t.TempDir(), t.TempDir()
	testpack.Seed(t, coreDir, core)
	testpack.Seed(t, comDir, map[string]string{
		"works/bo/book-nine/characters.json": testpack.CharactersJSON(t, "book-nine", "hero"),
	})

	if res := Load(Sources{Data: coreDir, Community: comDir}); res.OK() {
		t.Fatal("a sidecar for a work the core tree does not hold built clean")
	}
}

// TestLoadRefusesAnEmptyCommunityRoot is the flag's own trap: --community
// pointed at a directory that is not a community data root (the community
// repository's top level rather than its data/ subdirectory) would otherwise
// build an artifact with the whole CC BY-SA layer silently missing.
func TestLoadRefusesAnEmptyCommunityRoot(t *testing.T) {
	_, core, _ := splitFixture(t)
	coreDir := t.TempDir()
	testpack.Seed(t, coreDir, core)

	if res := Load(Sources{Data: coreDir, Community: t.TempDir()}); res.OK() {
		t.Fatal("a community root holding no sidecars built clean")
	}
}
