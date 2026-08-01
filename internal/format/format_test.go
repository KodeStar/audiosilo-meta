package format

import (
	"errors"
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
		t.Fatalf("tree is not clean after --write:\n  %s", strings.Join(rep.Lines(), "\n  "))
	}
}

// assertCanonical guards the healed tree against the invariant metafmt exists
// to enforce: everything it writes is already in canonical form.
func assertCanonical(t *testing.T, dir string) {
	t.Helper()
	bad, _, err := canonical.CheckTree(dir)
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
// given entries, so a test exercises formatting and structure in one run.
func packOf(entries ...string) string {
	return `{"entries":{` + strings.Join(entries, ",") + `}}`
}

func keyed(slug string, pad int) string {
	return fmt.Sprintf("%q:%s", slug, entry(slug, pad))
}

// A contributor may add an entry to the wrong pack; --write moves it and the
// tree comes out structurally clean.
func TestWriteRelocatesAMisplacedEntry(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "people", "0.json"), packOf(keyed("aa", 4), keyed("zz", 4)))
	writeFile(t, filepath.Join(dir, "people", "mm.json"), packOf(keyed("mm", 4)))

	rep := mustWrite(t, dir)
	if len(rep.Pending.Misplaced) != 1 || rep.Pending.Misplaced[0].Slug != "zz" {
		t.Fatalf("misplaced = %+v, want just zz", rep.Pending.Misplaced)
	}
	if got, want := rep.Pending.Misplaced[0].To.Path(), "people/mm.json"; got != want {
		t.Errorf("target = %s, want %s", got, want)
	}
	// The sentence is pkg/pack's, rendered beneath the data directory.
	want := `entry "zz" belongs in ` + filepath.Join(dir, "people", "mm.json")
	if !hasPrefix(rep.Lines(), want) {
		t.Errorf("lines = %v, want one starting %q", rep.Lines(), want)
	}
	if !hasLine(rep.Lines(), "wrote "+filepath.Join(dir, "people", "mm.json")) {
		t.Errorf("lines = %v, want the written pack named", rep.Lines())
	}

	if got := readPack(t, dir, "people/0.json").Slugs(); len(got) != 1 || got[0] != "aa" {
		t.Errorf("people/0.json = %v, want [aa]", got)
	}
	if got := readPack(t, dir, "people/mm.json").Slugs(); len(got) != 2 || got[1] != "zz" {
		t.Errorf("people/mm.json = %v, want [mm zz]", got)
	}
	assertClean(t, dir)
	assertCanonical(t, dir)
}

// An entry count over the cap is the case a pack's on-disk size cannot reveal,
// so it is the one that proves the split is driven by the structural report.
func TestWriteSplitsAPackOverTheEntryCap(t *testing.T) {
	dir := t.TempDir()
	def, _ := pack.Def(pack.FamilyPeople)
	var entries []string
	for i := 0; i <= def.Caps.Entries; i++ {
		entries = append(entries, keyed(fmt.Sprintf("p%04d", i), 4))
	}
	writeFile(t, filepath.Join(dir, "people", "0.json"), packOf(entries...))

	rep := mustWrite(t, dir)
	if len(rep.Pending.Packs) != 1 || rep.Pending.Packs[0].Reason != "entry count" {
		t.Fatalf("splits = %+v, want one entry-count split", rep.Pending.Packs)
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
		t.Errorf("second run reported work: %v", second.Lines())
	}
	if diff := treeDiff(before, snapshot(t, dir)); diff != "" {
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
	if len(rep.Pending.Packs) != 1 || rep.Pending.Packs[0].Reason != "size" {
		t.Fatalf("splits = %+v, want one size split", rep.Pending.Packs)
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
	if len(rep.Pending.Dirs) != 1 || rep.Pending.Dirs[0].Dir != "" {
		t.Fatalf("dirs = %+v, want the flat family over its cap", rep.Pending.Dirs)
	}
	if !hasPrefix(rep.Lines(), "family "+filepath.Join(dir, "series")+" holds") {
		t.Errorf("lines lack the directory split: %v", rep.Pending.Dirs)
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
		t.Errorf("no-op run reported work: %v", rep.Lines())
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
	if !hasPrefix(rep.Lines(), `entry "zz" belongs in `+filepath.Join(dir, "people", "mm.json")) {
		t.Errorf("lines do not name the canonical location: %v", rep.Lines())
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
	if len(rep.Pending.Packs) != 1 {
		t.Fatalf("splits = %+v, want one", rep.Pending.Packs)
	}
	if !hasPrefix(rep.Lines(), "pack "+filepath.Join(dir, "people", "0.json")+" is over its hard entry count cap") {
		t.Errorf("lines do not name the due split: %v", rep.Lines())
	}
	if !strings.Contains(rep.Summary(), "pack split due") {
		t.Errorf("summary = %q, want the due split counted", rep.Summary())
	}
}

// metafmt is a writer, so a tree that is not in the pack layout is REFUSED
// before it rewrites anything. Formatting it would leave thousands of tidy files
// that no reader will load, which reads as a maintained tree and is not one.
func TestLegacyTreeIsRefused(t *testing.T) {
	dir := t.TempDir()
	work := filepath.Join(dir, "works", "du", "dune", "work.json")
	const body = `{"title":"Dune","id":"dune"}`
	writeFile(t, work, body)

	for name, run := range map[string]func(string) (Report, error){"check": Check, "write": Write} {
		_, err := run(dir)
		if !errors.Is(err, pack.ErrLegacyLayout) {
			t.Errorf("%s error = %v, want it to wrap pack.ErrLegacyLayout", name, err)
		}
		if err != nil && !strings.Contains(err.Error(), "cmd/metamigrate") {
			t.Errorf("%s error does not name the fix: %v", name, err)
		}
	}
	raw, err := os.ReadFile(work)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != body {
		t.Errorf("a refused run rewrote the file: %s", raw)
	}
}

// A file the tooling cannot read is named and left exactly as it is - both the
// one that is not JSON and the one that is JSON but not a pack - while the rest
// of the family still heals. It used to abort the whole run with an empty
// report, after the formatting pass had already rewritten files.
func TestUnreadableFilesAreNamedAndTheRestStillHeals(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"not JSON", `{"entries":{`},
		{"not a pack wrapper", `{"id":"zz-bad","name":"a record, not a pack"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, filepath.Join(dir, "people", "0.json"), packOf(keyed("aa", 4), keyed("zz", 4)))
			writeFile(t, filepath.Join(dir, "people", "mm.json"), packOf(keyed("mm", 4)))
			broken := filepath.Join(dir, "people", "zz-bad.json")
			writeFile(t, broken, tc.body)

			check := mustCheck(t, dir)
			if len(check.Pending.Unreadable) != 1 || check.Pending.Unreadable[0].Path != "people/zz-bad.json" {
				t.Fatalf("unreadable = %+v, want people/zz-bad.json", check.Pending.Unreadable)
			}
			if check.Pending.Healable() {
				t.Error("Healable reported a tree holding an unreadable file")
			}
			if !check.NeedsHuman() || check.Clean() {
				t.Error("Check called an unreadable tree clean or fixable")
			}
			// The rest of the report survives: the misplaced entry is still named.
			if len(check.Pending.Misplaced) != 1 {
				t.Errorf("misplaced = %+v, want zz still reported", check.Pending.Misplaced)
			}
			if !hasPrefix(check.Lines(), broken+" cannot be read as a pack") {
				t.Errorf("lines do not name the unreadable file: %v", check.Lines())
			}
			// One problem, one line: it is not also listed as plain invalid JSON.
			if len(check.Invalid) != 0 {
				t.Errorf("invalid = %v, want the unreadable file reported once", check.Invalid)
			}

			rep := mustWrite(t, dir)
			if !rep.NeedsHuman() {
				t.Error("Write did not report that the tree still needs a human")
			}
			if len(rep.Pending.Misplaced) != 1 {
				t.Errorf("Write did not heal the rest: %+v", rep.Pending)
			}
			if got := readPack(t, dir, "people/mm.json").Slugs(); len(got) != 2 {
				t.Errorf("people/mm.json = %v, want the relocated entry", got)
			}
			// The file is still there, holding what it held. Canonical
			// formatting may have reindented it - that is the layout-agnostic
			// formatter doing its job and it changes no meaning - but nothing
			// restructured or deleted it.
			raw, err := os.ReadFile(broken)
			if err != nil {
				t.Fatal(err)
			}
			if string(raw) != tc.body && string(raw) != canonicalOf(tc.body) {
				t.Errorf("the unreadable file was rewritten:\n%s", raw)
			}

			// What is left needs a human, so the advice must not send the
			// contributor back to --write.
			after := mustCheck(t, dir)
			if len(after.Pending.Misplaced) != 0 || len(after.Pending.Salvage) != 0 {
				t.Errorf("the heal did not converge: %+v", after.Pending)
			}
			if got, want := after.Advice(), "they need a human"; !strings.Contains(got, want) {
				t.Errorf("advice = %q, want it to mention %q", got, want)
			}
			// A tree whose only remaining problem is unreadable still settles:
			// re-running --write forever must not rewrite anything.
			snap := snapshot(t, dir)
			mustWrite(t, dir)
			if diff := treeDiff(snap, snapshot(t, dir)); diff != "" {
				t.Errorf("a second --write changed the tree: %s", diff)
			}
		})
	}
}

// canonicalOf renders a fixture body the way the formatter would.
func canonicalOf(body string) string {
	out, err := canonical.Format([]byte(body))
	if err != nil {
		return body
	}
	return string(out)
}

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
