package migrate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/check"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// legacyFixture returns a small file-per-entity tree: two works (one with two
// recordings and both community sidecars), two people, one series.
func legacyFixture() map[string]string {
	return map[string]string{
		"people/an/ann-doe.json":  `{"id":"ann-doe","license":"CC0-1.0","name":"Ann Doe","sources":[{"type":"user"}]}`,
		"people/bo/bob-roe.json":  `{"id":"bob-roe","license":"CC0-1.0","name":"Bob Roe","sources":[{"type":"user"}]}`,
		"series/du/dune.json":     `{"id":"dune","license":"CC0-1.0","name":"Dune","sources":[{"type":"user"}],"works":[{"position":"1","work":"dune"}]}`,
		"works/du/dune/work.json": `{"authors":["ann-doe"],"id":"dune","language":"en","license":"CC0-1.0","sources":[{"type":"user"}],"title":"Dune"}`,
		"works/du/dune/recordings/bob-roe-2020.json": `{"id":"bob-roe-2020","language":"en","license":"CC0-1.0","narrators":["bob-roe"],` +
			`"runtime_min":600,"sources":[{"type":"user"}],"work":"dune"}`,
		"works/du/dune/recordings/ann-doe-2021.json": `{"id":"ann-doe-2021","language":"en","license":"CC0-1.0","narrators":["ann-doe"],` +
			`"sources":[{"type":"user"}],"work":"dune"}`,
		"works/du/dune/characters.json": `{"characters":[{"description":"A duke's heir.","id":"paul","name":"Paul","reveal":{"chapter":1}}],` +
			`"license":"CC-BY-SA-3.0","sources":[{"type":"community"}],"work":"dune"}`,
		"works/du/dune/recaps.json": `{"license":"CC-BY-SA-3.0","recaps":[{"text":"Paul arrives on Arrakis.","through":{"chapter":1}}],` +
			`"sources":[{"type":"community"}],"work":"dune"}`,
		"works/em/emma/work.json": `{"authors":["ann-doe"],"id":"emma","language":"en","license":"CC0-1.0","sources":[{"type":"user"}],"title":"Emma"}`,
		"works/em/emma/recordings/ann-doe-2019.json": `{"id":"ann-doe-2019","language":"en","license":"CC0-1.0","narrators":["ann-doe"],` +
			`"sources":[{"type":"user"}],"work":"emma"}`,
	}
}

// writeTree materializes a fixture into dir.
func writeTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// seedLegacy writes the fixture into a fresh temp dir and returns it.
func seedLegacy(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	writeTree(t, dir, files)
	return dir
}

// treeSnapshot returns every file under dir, data-relative, with its bytes.
func treeSnapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		raw, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		rel, rerr := filepath.Rel(dir, p)
		if rerr != nil {
			return rerr
		}
		out[filepath.ToSlash(rel)] = string(raw)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func paths(files map[string]string) []string {
	out := make([]string, 0, len(files))
	for p := range files {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// TestConvertsToAValidPackTree is the round trip: a legacy tree in, a pack tree
// out that metacheck loads clean with every record still in it, and not one
// legacy file left behind.
func TestConvertsToAValidPackTree(t *testing.T) {
	dir := seedLegacy(t, legacyFixture())
	sum, err := Run(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if !sum.InPlace {
		t.Error("a conversion with no --out must be in place")
	}
	if sum.Works != 2 || sum.Recordings != 3 || sum.People != 2 || sum.Series != 1 || sum.Community != 1 {
		t.Errorf("counts = %+v, want 2 works, 3 recordings, 2 people, 1 series, 1 sidecar set", sum)
	}
	if sum.Removed != len(legacyFixture()) {
		t.Errorf("removed %d files, want all %d", sum.Removed, len(legacyFixture()))
	}

	want := []string{"people/0.json", "series/0.json", "works-community/0/0.json", "works/0/0.json"}
	if got := paths(treeSnapshot(t, dir)); !equalStrings(got, want) {
		t.Errorf("tree = %v, want %v", got, want)
	}

	res := check.Load(dir)
	if !res.OK() {
		t.Fatalf("the converted tree does not validate:\n%s", problems(res))
	}
	cat := res.Catalog
	if len(cat.Works) != 2 || len(cat.People) != 2 || len(cat.Series) != 1 ||
		len(cat.Characters) != 1 || len(cat.Recaps) != 1 {
		t.Errorf("catalog counts: %d works, %d people, %d series, %d characters, %d recaps",
			len(cat.Works), len(cat.People), len(cat.Series), len(cat.Characters), len(cat.Recaps))
	}
	recs := 0
	for _, w := range cat.Works {
		recs += len(w.Recordings)
	}
	if recs != 3 {
		t.Errorf("recordings in the catalog = %d, want 3", recs)
	}
}

// TestConversionIsDeterministic: the same input tree always produces the same
// pack tree, byte for byte. Nothing about the layout may depend on map order or
// on the order the filesystem hands files back.
func TestConversionIsDeterministic(t *testing.T) {
	first := seedLegacy(t, legacyFixture())
	second := seedLegacy(t, legacyFixture())
	for _, dir := range []string{first, second} {
		if _, err := Run(Options{DataDir: dir}); err != nil {
			t.Fatal(err)
		}
	}
	a, b := treeSnapshot(t, first), treeSnapshot(t, second)
	if !equalStrings(paths(a), paths(b)) {
		t.Fatalf("two runs produced different files:\n%v\n%v", paths(a), paths(b))
	}
	for _, p := range paths(a) {
		if a[p] != b[p] {
			t.Errorf("%s differs between runs:\n%s\n%s", p, a[p], b[p])
		}
	}
}

// TestEntriesAreTheLegacyBytes: an entry is the record's own bytes, not a
// re-marshalled struct. A field the tooling does not model survives, and a field
// the source never stated is not invented - the recording without runtime_min
// must not come out carrying one, and neither may carry an "abridged": false
// nobody wrote.
func TestEntriesAreTheLegacyBytes(t *testing.T) {
	files := legacyFixture()
	dir := seedLegacy(t, files)
	if _, err := Run(Options{DataDir: dir}); err != nil {
		t.Fatal(err)
	}

	entry := readEntry(t, dir, "works/0/0.json", "dune")
	for _, field := range []string{"abridged", "added_at", "description"} {
		if _, ok := entry[field]; ok {
			t.Errorf("the work entry gained a %q the record never had", field)
		}
	}
	recs, _ := entry["recordings"].(map[string]any)
	if len(recs) != 2 {
		t.Fatalf("recordings = %v, want two", recs)
	}
	rec, _ := recs["ann-doe-2021"].(map[string]any)
	if _, ok := rec["runtime_min"]; ok {
		t.Errorf("a recording that stated no runtime came out with one: %v", rec)
	}
	if _, ok := rec["abridged"]; ok {
		t.Errorf("a recording that stated no abridged came out with one: %v", rec)
	}
	other, _ := recs["bob-roe-2020"].(map[string]any)
	if fmtNumber(other["runtime_min"]) != "600" {
		t.Errorf("runtime_min = %v, want the source's 600", other["runtime_min"])
	}
}

// TestSidecarsMoveToTheCommunityFamily: both sidecars of one work become the two
// members of ONE works-community entry, keyed by the work slug, and the CC0
// families never see them.
func TestSidecarsMoveToTheCommunityFamily(t *testing.T) {
	dir := seedLegacy(t, legacyFixture())
	if _, err := Run(Options{DataDir: dir}); err != nil {
		t.Fatal(err)
	}
	entry := readEntry(t, dir, "works-community/0/0.json", "dune")
	if len(entry) != 2 || entry["characters"] == nil || entry["recaps"] == nil {
		t.Fatalf("community entry = %v, want both members", entry)
	}
	works := readPack(t, dir, "works/0/0.json")
	if _, ok := works["emma"]; !ok {
		t.Errorf("the works pack lost emma: %v", works)
	}
	if entry := readEntry(t, dir, "works/0/0.json", "dune"); entry["characters"] != nil || entry["recaps"] != nil {
		t.Errorf("a sidecar stayed in the CC0 works entry: %v", entry)
	}
}

// A tree holding a file the pre-migration layout never had is refused whole: the
// conversion DELETES what it read, so a file it does not understand would be
// deleted without ever reaching a pack.
func TestUnrecognizedFileRefusesTheRun(t *testing.T) {
	files := legacyFixture()
	files["works/du/dune/notes.json"] = `{"note":"not a record"}`
	dir := seedLegacy(t, files)

	_, err := Run(Options{DataDir: dir})
	if err == nil {
		t.Fatal("a tree with an unrecognized file was converted")
	}
	if !strings.Contains(err.Error(), "works/du/dune/notes.json") {
		t.Errorf("error does not name the file: %v", err)
	}
	if got := len(treeSnapshot(t, dir)); got != len(files) {
		t.Errorf("a refused run changed the tree: %d files, want %d", got, len(files))
	}
}

// Running twice must not repack packs into packs.
func TestAlreadyConvertedIsRefused(t *testing.T) {
	dir := seedLegacy(t, legacyFixture())
	if _, err := Run(Options{DataDir: dir}); err != nil {
		t.Fatal(err)
	}
	before := treeSnapshot(t, dir)
	_, err := Run(Options{DataDir: dir})
	if err == nil {
		t.Fatal("a converted tree was converted again")
	}
	if !strings.Contains(err.Error(), "already reads as the pack layout") {
		t.Errorf("error = %v, want it to say the tree is already converted", err)
	}
	// The message has to name the file that made the family read as converted:
	// one pack-shaped file in a legacy family hides every record under it.
	if !strings.Contains(err.Error(), "people/0.json") {
		t.Errorf("error = %v, want it to name the deciding pack file", err)
	}
	after := treeSnapshot(t, dir)
	if !equalStrings(paths(before), paths(after)) {
		t.Errorf("the refused second run changed the tree")
	}
}

// An out-of-place conversion is the rehearsal: the pack tree lands elsewhere and
// the source tree is left exactly as it was.
func TestOutOfPlaceLeavesTheSourceAlone(t *testing.T) {
	files := legacyFixture()
	dir := seedLegacy(t, files)
	out := filepath.Join(t.TempDir(), "converted")

	sum, err := Run(Options{DataDir: dir, OutDir: out})
	if err != nil {
		t.Fatal(err)
	}
	if sum.InPlace || sum.Removed != 0 {
		t.Errorf("an --out run deleted from the source: %+v", sum)
	}
	if got := len(treeSnapshot(t, dir)); got != len(files) {
		t.Errorf("source tree = %d files, want the original %d", got, len(files))
	}
	if res := check.Load(out); !res.OK() {
		t.Fatalf("the out-of-place tree does not validate:\n%s", problems(res))
	}
}

// A works family with more entries than one pack may hold splits into several,
// the first named "0" and each later one by its own first slug, all within the
// migration's fill budget - and the result is metacheck-clean, which is what
// proves the bounds and the directory level agree.
func TestManyEntriesSplitIntoBoundedPacks(t *testing.T) {
	files := map[string]string{
		"people/an/ann-doe.json": `{"id":"ann-doe","license":"CC0-1.0","name":"Ann Doe","sources":[{"type":"user"}]}`,
	}
	// Each work carries a chunky description, so the size budget binds well
	// before the entry cap does.
	filler := strings.Repeat("word ", 400)
	for i := 0; i < 400; i++ {
		slug := "book-" + strconv.Itoa(1000+i)
		files["works/"+slug[:2]+"/"+slug+"/work.json"] = `{"authors":["ann-doe"],"description":"` + filler +
			`","id":"` + slug + `","language":"en","license":"CC0-1.0","sources":[{"type":"user"}],"title":"T"}`
	}
	dir := seedLegacy(t, files)

	sum, err := Run(Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Packs[pack.FamilyWorks] < 2 {
		t.Fatalf("400 padded works fit in %d pack(s); the fill budget is not being applied", sum.Packs[pack.FamilyWorks])
	}
	if res := check.Load(dir); !res.OK() {
		t.Fatalf("the split tree does not validate:\n%s", problems(res))
	}

	tree, err := pack.ReadTree(dir, pack.FamilyWorks)
	if err != nil {
		t.Fatal(err)
	}
	def, _ := pack.Def(pack.FamilyWorks)
	budget := def.Caps.TargetSize / fillDivisor
	for i, ref := range tree.Packs() {
		if i == 0 && (ref.Bound != pack.MinBound || ref.Dir != pack.MinBound) {
			t.Errorf("first pack = %s, want the reserved %q bound in the %q directory",
				ref.Path(), pack.MinBound, pack.MinBound)
		}
		raw, rerr := os.ReadFile(filepath.Join(dir, filepath.FromSlash(ref.Path())))
		if rerr != nil {
			t.Fatal(rerr)
		}
		file, perr := pack.Parse(raw)
		if perr != nil {
			t.Fatal(perr)
		}
		if i > 0 && file.Slugs()[0] != ref.Bound {
			t.Errorf("%s is named %q but starts at %q", ref.Path(), ref.Bound, file.Slugs()[0])
		}
		// Fill to ~50% of target, plus at most the one entry that crossed it.
		if len(raw) > budget+pack.TargetSize {
			t.Errorf("%s is %d bytes, well past the %d-byte fill budget", ref.Path(), len(raw), budget)
		}
	}
}

// readPack returns a pack's entries, decoded.
func readPack(t *testing.T, dir, rel string) map[string]json.RawMessage {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	file, err := pack.Parse(raw)
	if err != nil {
		t.Fatalf("%s: %v", rel, err)
	}
	out := map[string]json.RawMessage{}
	for _, slug := range file.Slugs() {
		e, _ := file.Get(slug)
		out[slug] = e
	}
	return out
}

// readEntry returns one entry of a pack, decoded into a map.
func readEntry(t *testing.T, dir, rel, slug string) map[string]any {
	t.Helper()
	raw, ok := readPack(t, dir, rel)[slug]
	if !ok {
		t.Fatalf("%s holds no entry %q", rel, slug)
	}
	m, err := pack.DecodeEntry(raw)
	if err != nil {
		t.Fatalf("%s entry %q: %v", rel, slug, err)
	}
	return m
}

// fmtNumber renders a decoded JSON number for comparison.
func fmtNumber(v any) string {
	if n, ok := v.(json.Number); ok {
		return n.String()
	}
	return ""
}

func problems(res check.Result) string {
	var b strings.Builder
	for _, p := range res.Problems {
		b.WriteString("  " + p.String() + "\n")
	}
	return b.String()
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
