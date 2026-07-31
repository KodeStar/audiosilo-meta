package format

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/canonical"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// snapshot maps every file under dir to its bytes, so two runs can be compared
// byte for byte.
func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
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

func paths(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func mustWrite(t *testing.T, dir string) Report {
	t.Helper()
	rep, err := Write(dir)
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func mustCheck(t *testing.T, dir string) Report {
	t.Helper()
	rep, err := Check(dir)
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func assertClean(t *testing.T, dir string) {
	t.Helper()
	rep := mustCheck(t, dir)
	if !rep.Clean() {
		t.Fatalf("tree is not clean after --write:\n  %s", strings.Join(rep.CheckLines(), "\n  "))
	}
}

// assertCanonical guards the healed tree against the invariant metafmt exists
// to enforce: everything it writes is already in canonical form.
func assertCanonical(t *testing.T, dir string) {
	t.Helper()
	bad, err := canonical.CheckTree(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(bad) > 0 {
		t.Errorf("non-canonical files after --write: %v", bad)
	}
}

func entry(slug string, pad int) string {
	return fmt.Sprintf(`{"id":%q,"pad":%q}`, slug, strings.Repeat("x", pad))
}

// packOf renders a compact (deliberately non-canonical) pack file holding the
// given entries, so a test exercises formatting and placement in one run.
func packOf(entries ...string) string {
	return `{"entries":{` + strings.Join(entries, ",") + `}}`
}

func keyed(slug string, pad int) string {
	return fmt.Sprintf("%q:%s", slug, entry(slug, pad))
}

// A contributor may add an entry to the wrong pack; --write moves it and the
// tree comes out placement-clean.
func TestWriteRelocatesAMisplacedEntry(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "people", "0.json"), packOf(keyed("aa", 4), keyed("zz", 4)))
	writeFile(t, filepath.Join(dir, "people", "mm.json"), packOf(keyed("mm", 4)))

	rep := mustWrite(t, dir)
	if len(rep.Misplaced) != 1 || rep.Misplaced[0].Slug != "zz" {
		t.Fatalf("misplaced = %+v, want just zz", rep.Misplaced)
	}
	if got, want := rep.Misplaced[0].To.Path(), "people/mm.json"; got != want {
		t.Errorf("target = %s, want %s", got, want)
	}
	// Placement lines carry the data-dir prefix, like the formatted lines.
	want := fmt.Sprintf("moved entry %q to %s (from %s)", "zz",
		filepath.Join(dir, "people", "mm.json"), filepath.Join(dir, "people", "0.json"))
	if !hasLine(rep.WriteLines(), want) {
		t.Errorf("write lines = %v, want one reading %q", rep.WriteLines(), want)
	}

	lower := readPack(t, dir, "people/0.json")
	if got := lower.Slugs(); len(got) != 1 || got[0] != "aa" {
		t.Errorf("people/0.json = %v, want [aa]", got)
	}
	upper := readPack(t, dir, "people/mm.json")
	if got := upper.Slugs(); len(got) != 2 || got[1] != "zz" {
		t.Errorf("people/mm.json = %v, want [mm zz]", got)
	}
	assertClean(t, dir)
	assertCanonical(t, dir)
}

// An entry count over the cap is the case a pack's on-disk size cannot reveal,
// so it is the one that proves the split pass is driven by Pending.
func TestWriteSplitsAPackOverTheEntryCap(t *testing.T) {
	dir := t.TempDir()
	def, _ := pack.Def(pack.FamilyPeople)
	var entries []string
	for i := 0; i <= def.Caps.Entries; i++ {
		entries = append(entries, keyed(fmt.Sprintf("p%04d", i), 4))
	}
	writeFile(t, filepath.Join(dir, "people", "0.json"), packOf(entries...))

	rep := mustWrite(t, dir)
	if len(rep.Splits) != 1 || rep.Splits[0].Reason != "entry count" {
		t.Fatalf("splits = %+v, want one entry-count split", rep.Splits)
	}
	if len(rep.Wrote) < 2 {
		t.Fatalf("wrote %v, want the pack split in two or more", rep.Wrote)
	}
	tree, err := pack.ReadTree(dir, pack.FamilyPeople)
	if err != nil {
		t.Fatal(err)
	}
	if tree.Len() < 2 {
		t.Fatalf("packs = %d, want the family split", tree.Len())
	}
	if tree.Packs()[0].Bound != pack.MinBound {
		t.Errorf("lowest bound = %s, want %s", tree.Packs()[0].Bound, pack.MinBound)
	}
	assertClean(t, dir)
	assertCanonical(t, dir)

	// Deterministic and minimal: a second run changes nothing at all.
	before := snapshot(t, dir)
	second := mustWrite(t, dir)
	if !second.Clean() {
		t.Errorf("second run reported work: %v", second.WriteLines())
	}
	after := snapshot(t, dir)
	if diff := treeDiff(before, after); diff != "" {
		t.Errorf("second run changed the tree: %s", diff)
	}
}

func TestWriteSplitsAPackOverTheSizeCap(t *testing.T) {
	dir := t.TempDir()
	def, _ := pack.Def(pack.FamilyWorks)
	// Comfortably over the hard size cap, well under the entry cap.
	var entries []string
	for i := 0; i < 600; i++ {
		entries = append(entries, keyed(fmt.Sprintf("w%04d", i), 1000))
	}
	writeFile(t, filepath.Join(dir, "works", "0", "0.json"), packOf(entries...))

	rep := mustWrite(t, dir)
	if len(rep.Splits) != 1 || rep.Splits[0].Reason != "size" {
		t.Fatalf("splits = %+v, want one size split", rep.Splits)
	}
	tree, err := pack.ReadTree(dir, pack.FamilyWorks)
	if err != nil {
		t.Fatal(err)
	}
	if tree.Len() < 2 {
		t.Fatalf("packs = %d, want the pack split", tree.Len())
	}
	for _, ref := range tree.Packs() {
		info, serr := os.Stat(filepath.Join(dir, filepath.FromSlash(ref.Path())))
		if serr != nil {
			t.Fatal(serr)
		}
		if int(info.Size()) > def.Caps.HardSize {
			t.Errorf("%s is %d bytes, still over the hard cap", ref.Path(), info.Size())
		}
	}
	assertClean(t, dir)
}

// A flat family over the per-directory pack cap gains the directory level.
func TestWriteSplitsAFlatFamilyIntoDirectories(t *testing.T) {
	dir := t.TempDir()
	def, _ := pack.Def(pack.FamilySeries)
	writeFile(t, filepath.Join(dir, "series", "0.json"), packOf(keyed("s0000", 4)))
	for i := 1; i <= def.Caps.DirPacks; i++ {
		slug := fmt.Sprintf("s%04d", i)
		writeFile(t, filepath.Join(dir, "series", slug+".json"), packOf(keyed(slug, 4)))
	}

	rep := mustWrite(t, dir)
	if len(rep.Dirs) != 1 || rep.Dirs[0].Dir != "" {
		t.Fatalf("dirs = %+v, want the flat family over its cap", rep.Dirs)
	}
	if !hasPrefix(rep.WriteLines(), "split family "+filepath.Join(dir, "series")+" into directories") {
		t.Errorf("write lines lack the directory split: %v", rep.Dirs)
	}
	tree, err := pack.ReadTree(dir, pack.FamilySeries)
	if err != nil {
		t.Fatal(err)
	}
	dirs := tree.Dirs()
	if len(dirs) < 2 {
		t.Fatalf("directories = %v, want the family split across several", dirs)
	}
	for _, d := range dirs {
		if d == "" {
			t.Fatal("a pack stayed at the family root after the directory split")
		}
		packs := tree.DirPacks(d)
		if len(packs) > def.Caps.DirPacks {
			t.Errorf("directory %s holds %d packs, over the cap", d, len(packs))
		}
		if packs[0].Bound != d {
			t.Errorf("directory %s is not named by its first pack %s", d, packs[0].Bound)
		}
	}
	assertClean(t, dir)

	before := snapshot(t, dir)
	mustWrite(t, dir)
	if diff := treeDiff(before, snapshot(t, dir)); diff != "" {
		t.Errorf("second run changed the tree: %s", diff)
	}
}

// A well-formed pack tree is a byte-level no-op.
func TestWriteIsANoOpOnAWellFormedPackTree(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "people", "0.json"), packOf(keyed("aa", 4), keyed("bb", 4)))
	writeFile(t, filepath.Join(dir, "works", "0", "0.json"), packOf(keyed("dune", 4)))
	mustWrite(t, dir)
	assertClean(t, dir)

	before := snapshot(t, dir)
	rep := mustWrite(t, dir)
	if !rep.Clean() {
		t.Errorf("no-op run reported work: %v", rep.WriteLines())
	}
	if len(rep.Wrote) != 0 || len(rep.Deleted) != 0 {
		t.Errorf("no-op run touched files: wrote %v, deleted %v", rep.Wrote, rep.Deleted)
	}
	if diff := treeDiff(before, snapshot(t, dir)); diff != "" {
		t.Errorf("no-op run changed the tree: %s", diff)
	}
}

// --check names where a misplaced entry belongs and what to run, and writes
// nothing.
func TestCheckNamesTheCanonicalLocation(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "people", "0.json"), packOf(keyed("aa", 4), keyed("zz", 4)))
	writeFile(t, filepath.Join(dir, "people", "mm.json"), packOf(keyed("mm", 4)))
	before := snapshot(t, dir)

	rep := mustCheck(t, dir)
	if rep.Clean() {
		t.Fatal("Check reported a misplaced entry as clean")
	}
	if len(rep.NonCanonical) != 2 {
		t.Errorf("non-canonical = %v, want both compact packs", rep.NonCanonical)
	}
	if !hasPrefix(rep.CheckLines(), `entry "zz" belongs in `+filepath.Join(dir, "people", "mm.json")) {
		t.Errorf("check lines do not name the canonical location: %v", rep.CheckLines())
	}
	if want := "misplaced entry"; !strings.Contains(rep.Summary(), want) {
		t.Errorf("summary = %q, want it to mention %q", rep.Summary(), want)
	}
	if diff := treeDiff(before, snapshot(t, dir)); diff != "" {
		t.Errorf("--check wrote to the tree: %s", diff)
	}
}

func TestCheckReportsDueSplits(t *testing.T) {
	dir := t.TempDir()
	def, _ := pack.Def(pack.FamilyPeople)
	var entries []string
	for i := 0; i <= def.Caps.Entries; i++ {
		entries = append(entries, keyed(fmt.Sprintf("p%04d", i), 4))
	}
	writeFile(t, filepath.Join(dir, "people", "0.json"), packOf(entries...))

	rep := mustCheck(t, dir)
	if len(rep.Splits) != 1 {
		t.Fatalf("splits = %+v, want one", rep.Splits)
	}
	if !hasPrefix(rep.CheckLines(), "pack "+filepath.Join(dir, "people", "0.json")+" is over its hard entry count cap") {
		t.Errorf("check lines do not name the due split: %v", rep.Splits)
	}
	if !strings.Contains(rep.Summary(), "pack split due") {
		t.Errorf("summary = %q, want the due split counted", rep.Summary())
	}
}

// The live tree until the migration PR: formatting only, exactly as before.
func TestLegacyTreeIsFormattedOnly(t *testing.T) {
	dir := t.TempDir()
	work := filepath.Join(dir, "works", "du", "dune", "work.json")
	person := filepath.Join(dir, "people", "fr", "frank-herbert.json")
	series := filepath.Join(dir, "series", "du", "dune.json")
	writeFile(t, work, `{"title":"Dune","id":"dune"}`)
	writeFile(t, person, `{"name":"Frank Herbert","id":"frank-herbert"}`)
	writeFile(t, series, `{"name":"Dune","id":"dune"}`)

	check := mustCheck(t, dir)
	if len(check.NonCanonical) != 3 {
		t.Errorf("non-canonical = %v, want all three legacy files", check.NonCanonical)
	}
	if len(check.Misplaced) != 0 || len(check.Splits) != 0 || len(check.Dirs) != 0 {
		t.Errorf("legacy tree reported placement work: %+v", check)
	}

	rep := mustWrite(t, dir)
	if len(rep.Formatted) != 3 {
		t.Errorf("formatted = %v, want all three legacy files", rep.Formatted)
	}
	if len(rep.Wrote) != 0 || len(rep.Deleted) != 0 || len(rep.Misplaced) != 0 {
		t.Errorf("legacy tree was restructured: %+v", rep)
	}
	got := paths(snapshot(t, dir))
	want := []string{"people/fr/frank-herbert.json", "series/du/dune.json", "works/du/dune/work.json"}
	if !sameSet(got, want) {
		t.Errorf("legacy files moved: %v, want %v", got, want)
	}
	assertClean(t, dir)
	assertCanonical(t, dir)
}

// During the dual-layout window a family may be packed while another is still
// legacy: the packed one heals, the legacy one is only formatted.
func TestMixedTreeHealsOnlyThePackedFamily(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "works", "0", "0.json"), packOf(keyed("aa", 4), keyed("zz", 4)))
	writeFile(t, filepath.Join(dir, "works", "mm", "mm.json"), packOf(keyed("mm", 4)))
	legacy := filepath.Join(dir, "people", "fr", "frank-herbert.json")
	writeFile(t, legacy, `{"name":"Frank Herbert","id":"frank-herbert"}`)

	rep := mustWrite(t, dir)
	if len(rep.Misplaced) != 1 || rep.Misplaced[0].To.Path() != "works/mm/mm.json" {
		t.Fatalf("misplaced = %+v, want zz moved into works/mm/mm.json", rep.Misplaced)
	}
	if got := readPack(t, dir, "works/mm/mm.json").Slugs(); len(got) != 2 {
		t.Errorf("works/mm/mm.json = %v, want mm and zz", got)
	}
	files := paths(snapshot(t, dir))
	want := []string{"people/fr/frank-herbert.json", "works/0/0.json", "works/mm/mm.json"}
	if !sameSet(files, want) {
		t.Errorf("files = %v, want %v", files, want)
	}
	raw, err := os.ReadFile(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if ok, cerr := canonical.IsCanonical(raw); cerr != nil || !ok {
		t.Errorf("legacy person file was not formatted: %s", raw)
	}
	assertClean(t, dir)
}

// A file that does not parse blocks the placement pass: a pack that cannot be
// read cannot be healed.
func TestInvalidJSONSuppressesThePlacementPass(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "people", "0.json"), packOf(keyed("aa", 4), keyed("zz", 4)))
	broken := filepath.Join(dir, "people", "mm.json")
	writeFile(t, broken, `{"entries":{`)

	check := mustCheck(t, dir)
	if len(check.Invalid) != 1 || check.Invalid[0] != broken {
		t.Fatalf("invalid = %v, want %s", check.Invalid, broken)
	}
	if len(check.Misplaced) != 0 {
		t.Errorf("placement ran over an unreadable tree: %+v", check.Misplaced)
	}
	if check.Clean() {
		t.Error("Check reported an unparseable file as clean")
	}

	rep := mustWrite(t, dir)
	if len(rep.Invalid) != 1 {
		t.Fatalf("invalid = %v, want the broken pack", rep.Invalid)
	}
	if len(rep.Misplaced) != 0 || len(rep.Wrote) != 0 {
		t.Errorf("placement ran despite the broken pack: %+v", rep)
	}
	raw, err := os.ReadFile(broken)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"entries":{` {
		t.Errorf("broken file was rewritten: %s", raw)
	}
}

// The committed tree is guarded by the gate's own `metafmt --check` step and by
// pkg/canonical's real-data test; repeating that whole-tree walk here would
// only buy the same assurance twice.

func readPack(t *testing.T, dir, rel string) *pack.File {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	f, err := pack.Parse(raw)
	if err != nil {
		t.Fatalf("%s: %v", rel, err)
	}
	return f
}

func hasLine(lines []string, want string) bool {
	for _, l := range lines {
		if l == want {
			return true
		}
	}
	return false
}

func hasPrefix(lines []string, prefix string) bool {
	for _, l := range lines {
		if strings.HasPrefix(l, prefix) {
			return true
		}
	}
	return false
}

func sameSet(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	seen := map[string]bool{}
	for _, g := range got {
		seen[g] = true
	}
	for _, w := range want {
		if !seen[w] {
			return false
		}
	}
	return true
}

// treeDiff describes the first difference between two snapshots, or "" when
// they are identical.
func treeDiff(before, after map[string]string) string {
	for p, b := range before {
		a, ok := after[p]
		if !ok {
			return p + " disappeared"
		}
		if a != b {
			return p + " changed"
		}
	}
	for p := range after {
		if _, ok := before[p]; !ok {
			return p + " appeared"
		}
	}
	return ""
}
