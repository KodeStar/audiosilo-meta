package pack

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// entriesOf collects every slug held by every JSON file under a family root,
// whatever shape the tree is in. It is how the tests assert the one thing that
// must never fail: healing loses no entry.
func entriesOf(t *testing.T, dir string, f Family) []string {
	t.Helper()
	rels, err := jsonFilesUnder(dir, f.Root())
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, rel := range rels {
		raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		file, err := Parse(raw)
		if err != nil {
			continue
		}
		out = append(out, file.Slugs()...)
	}
	sort.Strings(out)
	return out
}

func pendingOf(t *testing.T, dir string, f Family) Pending {
	t.Helper()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.Pending(f)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// healFlush runs the self-healing pair and returns what it wrote.
func healFlush(t *testing.T, dir string, f Family) Written {
	t.Helper()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Heal(f); err != nil {
		t.Fatal(err)
	}
	w, err := s.Flush()
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// entry renders a minimal entry body carrying a marker, so a test can tell two
// copies of one slug apart.
func entry(slug, marker string) string {
	return `{"id":"` + slug + `","name":"` + marker + `"}`
}

func packOfEntries(pairs ...[2]string) string {
	var b strings.Builder
	b.WriteString(`{"entries":{`)
	for i, p := range pairs {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`"` + p[0] + `":` + entry(p[0], p[1]))
	}
	b.WriteString(`}}`)
	return b.String()
}

// THE data-loss reproduction. A pack filed under a directory that does not cover
// it interposes a foreign directory in bound order, which used to split one
// directory into two planned groups sharing a name - and the second group's
// pack was then rebound onto the first's path, so writing it silently replaced
// every entry the first held.
func TestMisfiledPackNeverClobbersAnother(t *testing.T) {
	cases := []struct {
		name        string
		interposed  string // path of the pack filed under a directory that does not cover it
		wantRefusal string // the guard plain Flush must trip
		wantSalvage string // the file healing sets aside
	}{
		{
			// The directory sorts between two packs of another directory, so
			// bound order interleaves them: one directory plans as two groups,
			// and the second group's first pack is rebound onto the first
			// group's path.
			name:        "collision",
			interposed:  "aa/aa.json",
			wantRefusal: "one path",
			wantSalvage: "works/0/nn.json",
		},
		{
			// The pack sorts BELOW its own directory's bound, so normalizing it
			// to that bound would raise it and orphan its entries.
			name:        "raised bound",
			interposed:  "zz/mm.json",
			wantRefusal: "orphan",
			wantSalvage: "works/zz/mm.json",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			seed := func(t *testing.T) (string, []string) {
				dir := t.TempDir()
				writeFile(t, filepath.Join(dir, "works", "0", "0.json"),
					packOfEntries([2]string{"ab", "A"}, [2]string{"bb", "B"}))
				writeFile(t, filepath.Join(dir, "works", "0", "nn.json"),
					packOfEntries([2]string{"nn", "N"}))
				parts := strings.Split(c.interposed, "/")
				slug, _ := packBound(parts[1])
				writeFile(t, filepath.Join(dir, "works", parts[0], parts[1]),
					packOfEntries([2]string{slug, "X"}))
				want := []string{"ab", "bb", "nn", slug}
				sort.Strings(want)
				return dir, want
			}

			// Without healing, Flush must refuse rather than write one pack
			// over another.
			dir, want := seed(t)
			s, err := Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.Flush(); err == nil {
				t.Fatal("Flush accepted a tree it cannot lay out without losing data")
			} else if !strings.Contains(err.Error(), c.wantRefusal) {
				t.Fatalf("error = %v, want it to name %q", err, c.wantRefusal)
			}
			if got := entriesOf(t, dir, FamilyWorks); !equalStrings(got, want) {
				t.Errorf("entries = %v, want %v untouched", got, want)
			}

			// With healing, the misfiled pack is content and every entry
			// survives.
			dir, want = seed(t)
			p := pendingOf(t, dir, FamilyWorks)
			if len(p.Salvage) != 1 || p.Salvage[0].Path != c.wantSalvage {
				t.Fatalf("Pending salvage = %+v, want %s", p.Salvage, c.wantSalvage)
			}
			if p.Empty() {
				t.Error("Pending reported a tree that loses data as clean")
			}
			healFlush(t, dir, FamilyWorks)
			if got := entriesOf(t, dir, FamilyWorks); !equalStrings(got, want) {
				t.Fatalf("entries = %v, want %v", got, want)
			}
			if after := pendingOf(t, dir, FamilyWorks); !after.Empty() {
				t.Errorf("still pending after one pass: %v", after.Lines())
			}
		})
	}
}

// THE second data-loss reproduction, from a real enrich run. A pack that holds
// a LATER pack's first slug splits into a bound that pack already carries, so
// the flush planned two packs onto one path - and only found out half a
// catalogue into writing it.
//
// The tree got that way the ordinary way: an import created new packs, the
// operator reverted with `git checkout -- data/` (which restores the tracked
// files but leaves the untracked new packs behind), and the restored pack held
// the entries the split had moved out of it. Every one of those entries is
// invisible to a reader - Locate answers from the pack whose bound covers the
// slug - so the writer must not act on the tree at all until it is healed.
func TestFlushRefusesAPackHoldingALaterPacksEntries(t *testing.T) {
	// One entry per pack, so the pack carrying the leftover owes a split - the
	// enrich's shape, where growing entries pushed pack after pack over its cap.
	withCaps(t, FamilyWorks, Caps{TargetSize: 100, HardSize: 150, Entries: 1, DirPacks: DirPackCap})
	dir := t.TempDir()
	// nn.json covers [nn, zz), so its "zz" entry is the leftover the split had
	// already moved into zz.json.
	writeFile(t, filepath.Join(dir, "works", "0", "0.json"), packOfEntries([2]string{"ab", "A"}))
	writeFile(t, filepath.Join(dir, "works", "0", "nn.json"),
		packOfEntries([2]string{"nn", "N"}, [2]string{"zz", "STALE"}))
	writeFile(t, filepath.Join(dir, "works", "0", "zz.json"), packOfEntries([2]string{"zz", "CURRENT"}))
	writeFile(t, filepath.Join(dir, "people", "0.json"), packOfEntries([2]string{"aa", "A"}))
	before := treeBytes(t, dir)

	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	// A write into the offending pack, and one into a family that is planned
	// BEFORE works - the refusal has to precede every write, not just the ones
	// after it.
	if err := s.Upsert(FamilyWorks, "nn", json.RawMessage(entry("nn", "EDITED"))); err != nil {
		t.Fatal(err)
	}
	if err := s.Upsert(FamilyPeople, "cc", json.RawMessage(entry("cc", "NEW"))); err != nil {
		t.Fatal(err)
	}
	_, err = s.Flush()
	if err == nil {
		t.Fatal("Flush wrote into a tree whose packs hold each other's entries")
	}
	if !errors.Is(err, ErrMisplacedEntries) {
		t.Errorf("error = %v, want it to be ErrMisplacedEntries", err)
	}
	for _, want := range []string{`"zz"`, "works/0/zz.json", "metafmt --write"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to name %s", err, want)
		}
	}
	// The whole plan set is validated before the first byte, so a refused flush
	// leaves nothing for an operator to heal on top of the refusal.
	if after := treeBytes(t, dir); !sameTree(before, after) {
		t.Errorf("a refused flush wrote to the tree:\nbefore %v\nafter  %v", keysOf(before), keysOf(after))
	}

	// Healing is the documented remedy, and it keeps the copy readers see.
	healFlush(t, dir, FamilyWorks)
	if got := entriesOf(t, dir, FamilyWorks); !equalStrings(got, []string{"ab", "nn", "zz"}) {
		t.Fatalf("entries = %v, want [ab nn zz]", got)
	}
	if got := readPack(t, dir, "works/0/zz.json"); !strings.Contains(string(mustEntry(t, got, "zz")), "CURRENT") {
		t.Error("healing kept the stale copy over the one every reader was seeing")
	}
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s2.Upsert(FamilyWorks, "nn", json.RawMessage(entry("nn", "EDITED"))); err != nil {
		t.Fatal(err)
	}
	if _, err := s2.Flush(); err != nil {
		t.Fatalf("flush into the healed tree: %v", err)
	}
}

// The backstop behind checkInRange: whatever route produced them, two packs on
// one path are refused, and the message names both so the shape is diagnosable.
func TestPlannedPathsRefusesTwoPacksOnOnePath(t *testing.T) {
	def, _ := Def(FamilyWorks)
	_, _, err := plannedPaths(def, []planPack{
		{src: "works/0/aa.json", dir: "0", bound: "mm"},
		{src: "works/0/mm.json", dir: "0", bound: "mm"},
	})
	if err == nil {
		t.Fatal("plannedPaths accepted two packs on one path")
	}
	for _, want := range []string{"works/0/mm.json", "works/0/aa.json", "one path"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to name %s", err, want)
		}
	}
}

// treeBytes reads every file under the data root, so a test can assert that a
// refused flush changed nothing at all.
func treeBytes(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
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

func sameTree(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func mustEntry(t *testing.T, f *File, slug string) json.RawMessage {
	t.Helper()
	e, ok := f.Get(slug)
	if !ok {
		t.Fatalf("pack has no entry %q", slug)
	}
	return e
}

// rebind may only ever widen a range downward. Raising a bound orphans every
// entry below the new bound, so it is refused rather than performed.
func TestFlushRefusesToRaiseABound(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "works", "0", "0.json"), packOfEntries([2]string{"aa", "A"}))
	writeFile(t, filepath.Join(dir, "works", "mm", "bb.json"), packOfEntries([2]string{"bb", "B"}))

	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Flush(); err == nil {
		t.Fatal("Flush raised a pack bound instead of refusing")
	} else if !strings.Contains(err.Error(), "orphan") {
		t.Fatalf("error = %v, want it to name the orphaning", err)
	}

	// Healing resolves it the right way: the misfiled pack is content.
	healFlush(t, dir, FamilyWorks)
	if got := entriesOf(t, dir, FamilyWorks); !equalStrings(got, []string{"aa", "bb"}) {
		t.Fatalf("entries = %v, want [aa bb]", got)
	}
	if after := pendingOf(t, dir, FamilyWorks); !after.Empty() {
		t.Errorf("still pending: %v", after.Lines())
	}
}

// A slug held twice resolves to the CORRECTLY-PLACED copy - the one every reader
// was already seeing. Taking the misfiled one would quietly revert it, which is
// exactly what a merge-conflict union produces.
func TestHealKeepsTheCorrectlyPlacedCopy(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "people", "0.json"), packOfEntries([2]string{"aa", "CURRENT"}))
	writeFile(t, filepath.Join(dir, "people", "mm.json"),
		packOfEntries([2]string{"aa", "STALE"}, [2]string{"mm", "M"}))

	p := pendingOf(t, dir, FamilyPeople)
	if len(p.Conflicts) != 1 {
		t.Fatalf("Pending conflicts = %+v, want one", p.Conflicts)
	}
	c := p.Conflicts[0]
	if c.Slug != "aa" || c.Kept != "people/0.json" || c.Dropped != "people/mm.json" || c.Identical {
		t.Fatalf("conflict = %+v, want aa kept in 0.json, differing", c)
	}
	if len(p.Misplaced) != 0 {
		t.Errorf("misplaced = %+v, want none: the duplicate is dropped, not moved", p.Misplaced)
	}
	if !strings.Contains(c.String(), "correctly-placed copy is kept") {
		t.Errorf("conflict message = %q", c.String())
	}

	healFlush(t, dir, FamilyPeople)
	raw, err := os.ReadFile(filepath.Join(dir, "people", "0.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "CURRENT") || strings.Contains(string(raw), "STALE") {
		t.Errorf("0.json = %s, want the current copy kept", raw)
	}
	if got := readPack(t, dir, "people/mm.json").Slugs(); !equalStrings(got, []string{"mm"}) {
		t.Errorf("mm.json = %v, want the duplicate dropped", got)
	}
	if after := pendingOf(t, dir, FamilyPeople); !after.Empty() {
		t.Errorf("still pending: %v", after.Lines())
	}
}

// An identical duplicate is still reported, so nobody has to wonder whether the
// drop lost anything.
func TestHealReportsAnIdenticalDuplicate(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "people", "0.json"), packOfEntries([2]string{"aa", "SAME"}))
	writeFile(t, filepath.Join(dir, "people", "mm.json"),
		packOfEntries([2]string{"aa", "SAME"}, [2]string{"mm", "M"}))
	p := pendingOf(t, dir, FamilyPeople)
	if len(p.Conflicts) != 1 || !p.Conflicts[0].Identical {
		t.Fatalf("conflicts = %+v, want one identical", p.Conflicts)
	}
	if !strings.Contains(p.Conflicts[0].String(), "identical") {
		t.Errorf("message = %q", p.Conflicts[0].String())
	}
}

// An empty pack is deleted, never filled. Treating it as a real bound would
// relocate a slice of the family into a file a contributor created by accident.
func TestEmptyPackIsDeletedNotFilled(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "people", "0.json"),
		packOfEntries([2]string{"aa", "A"}, [2]string{"nn", "N"}, [2]string{"zz", "Z"}))
	writeFile(t, filepath.Join(dir, "people", "mm.json"), `{"entries":{}}`)

	p := pendingOf(t, dir, FamilyPeople)
	if len(p.Salvage) != 1 || p.Salvage[0].Entries != 0 {
		t.Fatalf("salvage = %+v, want the empty pack", p.Salvage)
	}
	if len(p.Misplaced) != 0 {
		t.Fatalf("misplaced = %+v, want nothing moved into an accidental file", p.Misplaced)
	}
	healFlush(t, dir, FamilyPeople)
	if got := packPaths(t, dir, FamilyPeople); !equalStrings(got, []string{"people/0.json"}) {
		t.Fatalf("packs = %v, want the empty one gone", got)
	}
	if got := entriesOf(t, dir, FamilyPeople); !equalStrings(got, []string{"aa", "nn", "zz"}) {
		t.Errorf("entries = %v", got)
	}
}

// A pack whose NAME is not a slug bound is content, not a destination. metafmt
// used to tell contributors to move the family into it.
func TestInvalidlyNamedPackIsSalvaged(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "people", "0.json"), packOfEntries([2]string{"aa", "A"}))
	writeFile(t, filepath.Join(dir, "people", "Not A Bound.json"), packOfEntries([2]string{"zz", "Z"}))

	p := pendingOf(t, dir, FamilyPeople)
	if len(p.Salvage) != 1 || !strings.Contains(p.Salvage[0].Reason, "valid slug bound") {
		t.Fatalf("salvage = %+v, want the invalid name", p.Salvage)
	}
	if len(p.Misplaced) != 0 {
		t.Fatalf("misplaced = %+v, want nothing moved INTO the invalid file", p.Misplaced)
	}
	healFlush(t, dir, FamilyPeople)
	if got := packPaths(t, dir, FamilyPeople); !equalStrings(got, []string{"people/0.json"}) {
		t.Fatalf("packs = %v", got)
	}
	if got := entriesOf(t, dir, FamilyPeople); !equalStrings(got, []string{"aa", "zz"}) {
		t.Errorf("entries = %v, want both preserved", got)
	}
}

// A stray subdirectory must not convert a flat family into a directory-level
// one; the minority shape is the anomaly.
func TestStrayFilesAreSalvagedWithoutReshapingTheFamily(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "people", "0.json"), packOfEntries([2]string{"aa", "A"}))
	writeFile(t, filepath.Join(dir, "people", "nn.json"), packOfEntries([2]string{"nn", "N"}))
	writeFile(t, filepath.Join(dir, "people", "sub", "x.json"), packOfEntries([2]string{"zz", "Z"}))
	// A works file nested deeper than a pack can sit.
	writeFile(t, filepath.Join(dir, "works", "0", "0.json"), packOfEntries([2]string{"ww", "W"}))
	writeFile(t, filepath.Join(dir, "works", "0", "extra", "note.json"), packOfEntries([2]string{"yy", "Y"}))

	p := pendingOf(t, dir, FamilyPeople)
	if len(p.Salvage) != 1 || p.Salvage[0].Path != "people/sub/x.json" {
		t.Fatalf("people salvage = %+v", p.Salvage)
	}
	healFlush(t, dir, FamilyPeople)
	if got := packPaths(t, dir, FamilyPeople); !equalStrings(got, []string{"people/0.json", "people/nn.json"}) {
		t.Fatalf("people packs = %v, want the family still flat", got)
	}
	if got := entriesOf(t, dir, FamilyPeople); !equalStrings(got, []string{"aa", "nn", "zz"}) {
		t.Errorf("people entries = %v", got)
	}

	wp := pendingOf(t, dir, FamilyWorks)
	if len(wp.Salvage) != 1 || !strings.Contains(wp.Salvage[0].Reason, "deeper") {
		t.Fatalf("works salvage = %+v", wp.Salvage)
	}
	healFlush(t, dir, FamilyWorks)
	if got := entriesOf(t, dir, FamilyWorks); !equalStrings(got, []string{"ww", "yy"}) {
		t.Errorf("works entries = %v, want both preserved", got)
	}
	if after := pendingOf(t, dir, FamilyWorks); !after.Empty() {
		t.Errorf("still pending: %v", after.Lines())
	}
}

// A file that parses as JSON but is not a pack wrapper is a category, not a
// crash: reporting the whole family as unreadable used to abort the run with an
// empty report.
func TestInvalidWrapperIsReportedNotFatal(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "people", "0.json"), packOfEntries([2]string{"aa", "A"}))
	writeFile(t, filepath.Join(dir, "people", "mm.json"), `{"entries":{},"version":1}`)
	writeFile(t, filepath.Join(dir, "people", "zz.json"), `{`)

	p := pendingOf(t, dir, FamilyPeople)
	if len(p.Unreadable) != 2 {
		t.Fatalf("unreadable = %+v, want both files", p.Unreadable)
	}
	if p.Healable() {
		t.Error("Healable reported a tree with an unreadable file as fixable")
	}
	if p.Empty() {
		t.Error("Empty reported it as clean")
	}
	// The rest of the family is still reported and still healed.
	healFlush(t, dir, FamilyPeople)
	for _, rel := range []string{"people/mm.json", "people/zz.json"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); err != nil {
			t.Errorf("%s was touched: %v", rel, err)
		}
	}
}

// A slug below the family's lowest bound is a rebind, not a relocation: the pack
// it sits in IS the pack it looks up to, and the fix is to widen that pack's
// bound down to the reserved minimum.
func TestSlugBelowTheLowestBoundIsARebind(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "people", "mm.json"),
		packOfEntries([2]string{"aa", "A"}, [2]string{"mm", "M"}))

	p := pendingOf(t, dir, FamilyPeople)
	if len(p.Misplaced) != 0 {
		t.Fatalf("misplaced = %+v, want none (that message would read 'belongs in X, not X')", p.Misplaced)
	}
	if len(p.Rebinds) != 1 || p.Rebinds[0].To.Bound != MinBound {
		t.Fatalf("rebinds = %+v, want mm.json to become 0.json", p.Rebinds)
	}
	if !strings.Contains(p.Rebinds[0].String(), "people/0.json") {
		t.Errorf("rebind message = %q", p.Rebinds[0].String())
	}
	healFlush(t, dir, FamilyPeople)
	if got := packPaths(t, dir, FamilyPeople); !equalStrings(got, []string{"people/0.json"}) {
		t.Fatalf("packs = %v", got)
	}
	if after := pendingOf(t, dir, FamilyPeople); !after.Empty() {
		t.Errorf("still pending: %v", after.Lines())
	}
}

// A repeated key silently keeps the last value in encoding/json; rewriting the
// pack would then make that loss permanent.
func TestParseRejectsDuplicateKeys(t *testing.T) {
	cases := map[string]string{
		"duplicate entry":  `{"entries":{"aa":{"id":"aa"},"aa":{"id":"aa"}}}`,
		"duplicate nested": `{"entries":{"aa":{"id":"aa","recordings":{"r":{},"r":{}}}}}`,
		"duplicate outer":  `{"entries":{},"entries":{}}`,
	}
	for name, raw := range cases {
		if _, err := Parse([]byte(raw)); err == nil {
			t.Errorf("%s: Parse accepted %s", name, raw)
		} else if !strings.Contains(err.Error(), "duplicate key") {
			t.Errorf("%s: error = %v, want it to name the duplicate", name, err)
		}
	}
	if _, err := DecodeEntry(json.RawMessage(`{"id":"aa","id":"bb"}`)); err == nil {
		t.Error("DecodeEntry accepted a duplicate key")
	}
	if _, err := Parse([]byte(`{"entries":{"aa":{"id":"aa"},"bb":{"id":"bb"}}}`)); err != nil {
		t.Errorf("Parse rejected a clean pack: %v", err)
	}
}

// A pack file is spelled ".json", exactly. Listing "X.JSON" as a pack with bound
// "X" would point every read and write at an "X.json" that does not exist.
func TestPackFilesAreLowercaseJSONOnly(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "people", "0.json"), packOfEntries([2]string{"aa", "A"}))
	writeFile(t, filepath.Join(dir, "people", "MM.JSON"), packOfEntries([2]string{"mm", "M"}))

	tr, err := ReadTree(dir, FamilyPeople)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Len() != 1 || tr.Packs()[0].Bound != MinBound {
		t.Fatalf("packs = %v, want only 0.json", tr.Packs())
	}
	if !isJSONFile("x.json") || isJSONFile("x.JSON") || isJSONFile("x.Json") {
		t.Error("isJSONFile is not case-sensitive")
	}
}

// The memoized render must never go stale.
func TestRenderMemoIsInvalidatedByMutation(t *testing.T) {
	f := NewFile()
	f.Set("aa", json.RawMessage(`{"id":"aa"}`))
	first, err := f.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	again, err := f.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(again) {
		t.Fatal("a repeated render differed")
	}
	for _, mutate := range []func(){
		func() { f.Set("bb", json.RawMessage(`{"id":"bb"}`)) },
		func() { f.Remove("aa") },
		func() { f.Set("bb", json.RawMessage(`{"id":"bb","x":1}`)) },
	} {
		mutate()
		got, err := f.Bytes()
		if err != nil {
			t.Fatal(err)
		}
		fresh := NewFile()
		for _, s := range f.Slugs() {
			e, _ := f.Get(s)
			fresh.Set(s, e)
		}
		want, err := fresh.Bytes()
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Fatalf("cached render is stale:\n got %s\nwant %s", got, want)
		}
		total, per, err := f.Sizes()
		if err != nil {
			t.Fatal(err)
		}
		sum := 0
		for _, n := range per {
			sum += n
		}
		if len(f.Slugs()) > 0 && sum+len(packPrefix)+1+len(packSuffix) != total {
			t.Fatalf("cached sizes are stale: %d + overhead != %d", sum, total)
		}
	}
	// Bytes hands out a copy, so a caller cannot scribble on the memo.
	b, err := f.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	b[0] = 'X'
	c, err := f.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if c[0] == 'X' {
		t.Error("Bytes handed out the memo itself")
	}
}

// TestHealPendingReportsWhatItHeals pins the report HealPending hands back
// against the Pending it replaced: a caller that wants both no longer surveys
// the family twice, and must not get a different answer for it.
func TestHealPendingReportsWhatItHeals(t *testing.T) {
	trees := map[string]map[string]string{
		"clean": {
			"people/0.json": packOfEntries([2]string{"aa", "A"}),
		},
		"misplaced and salvage": {
			"people/0.json":         packOfEntries([2]string{"aa", "A"}, [2]string{"zz", "Z"}),
			"people/mm.json":        packOfEntries([2]string{"mm", "M"}),
			"people/Not Bound.json": packOfEntries([2]string{"nn", "N"}),
		},
		"duplicate slug": {
			"people/0.json":  packOfEntries([2]string{"aa", "A"}, [2]string{"mm", "X"}),
			"people/mm.json": packOfEntries([2]string{"mm", "M"}),
		},
		"unreadable": {
			"people/0.json":  packOfEntries([2]string{"aa", "A"}),
			"people/zz.json": `{`,
		},
	}
	for name, files := range trees {
		t.Run(name, func(t *testing.T) {
			want := t.TempDir()
			writeFiles(t, want, files)
			got := t.TempDir()
			writeFiles(t, got, files)

			wantP := pendingOf(t, want, FamilyPeople)
			s, err := Open(got)
			if err != nil {
				t.Fatal(err)
			}
			gotP, err := s.HealPending(FamilyPeople)
			if err != nil {
				t.Fatal(err)
			}
			if a, b := strings.Join(gotP.Lines(), "\n"), strings.Join(wantP.Lines(), "\n"); a != b {
				t.Errorf("HealPending report:\n%s\nPending report:\n%s", a, b)
			}
			// And it still heals: Heal is the same pass with the report dropped,
			// so what HealPending queues has to converge exactly as Heal's did.
			if _, err := s.Flush(); err != nil {
				t.Fatal(err)
			}
			healFlush(t, want, FamilyPeople)
			if a, b := entriesOf(t, got, FamilyPeople), entriesOf(t, want, FamilyPeople); !equalStrings(a, b) {
				t.Errorf("entries after HealPending = %v, after Heal = %v", a, b)
			}
			if a, b := packPaths(t, got, FamilyPeople), packPaths(t, want, FamilyPeople); !equalStrings(a, b) {
				t.Errorf("packs after HealPending = %v, after Heal = %v", a, b)
			}
		})
	}
}
