package format

// The reproductions from the adversarial review, at the metafmt level. Each one
// is a tree a contributor could plausibly produce, and each one used to end in
// metafmt calling a broken tree clean - or, worse, "fixing" it by overwriting a
// pack and losing its entries while exiting 0.
//
// The property every case asserts is the same, and it is the contract metafmt
// owes a contributor:
//
//  1. metacheck rejects the tree, and --check therefore must NOT call it clean.
//     Agreement between the two is the root-cause fix: metafmt's model of wrong
//     has to be a superset of metacheck's, or it rewrites what it cannot see.
//  2. --check names the fix, in a line the contributor can act on.
//  3. ONE --write converges: metacheck-green afterwards, with every entry still
//     present (bar a duplicate the conflict rule deliberately drops).
//  4. A second --write is a byte-level no-op.

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/check"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// The fixture records are minimal but schema-valid, so check.Load's verdict is
// about structure rather than about missing fields. A person's name is the
// rendering of their slug because the id has to BE the name's slug
// (checkPersonSlug) - the one fixture that deliberately breaks that is the
// stale duplicate below, which every case drops before metacheck sees it.
func personRec(slug, name string) string {
	return `{"id":"` + slug + `","license":"CC0-1.0","name":"` + name + `","sources":[{"type":"user"}]}`
}

func workRec(slug string) string {
	return `{"authors":["p-aa"],"id":"` + slug + `","language":"en","license":"CC0-1.0","sources":[{"type":"user"}],"title":"Title ` + slug + `"}`
}

// pack renders a pack file from slug -> record, compactly: a fixture is exactly
// what a contributor's editor might leave behind, not canonical output.
func packWith(entries map[string]string) string {
	keys := make([]string, 0, len(entries))
	for k := range entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, `"`+k+`":`+entries[k])
	}
	return `{"entries":{` + strings.Join(parts, ",") + `}}`
}

// people is the base every fixture carries: the author the works reference, so
// nothing fails integrity for a reason that is not the point of the case.
func peopleBase() map[string]string {
	return map[string]string{"p-aa": personRec("p-aa", "P Aa")}
}

// entrySet lists every entry in the tree as "<family>/<slug>", so a heal can be
// checked for losing one. Files that cannot be read as packs are skipped, which
// is what the unreadable category means.
func entrySet(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".json") {
			return err
		}
		raw, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		f, perr := pack.Parse(raw)
		if perr != nil {
			return nil
		}
		rel, rerr := filepath.Rel(dir, p)
		if rerr != nil {
			return rerr
		}
		family := strings.Split(filepath.ToSlash(rel), "/")[0]
		for _, slug := range f.Slugs() {
			out = append(out, family+"/"+slug)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}

func metacheckProblems(t *testing.T, dir string) []string {
	t.Helper()
	res := check.Load(dir)
	out := make([]string, 0, len(res.Problems))
	for _, p := range res.Problems {
		out = append(out, p.String())
	}
	return out
}

// TestHealsTheReviewReproductions is the metafmt-level convergence property.
func TestHealsTheReviewReproductions(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		// wantLine is a fragment --check has to print, naming the fix.
		wantLine string
		// wantDropped are entries the heal deliberately does not keep: only a
		// duplicate the conflict rule resolves.
		wantDropped []string
	}{
		{
			// A whole pack moved into a directory whose range does not cover
			// it. This is the data-loss case: laying it out as though its name
			// were a real bound planned two packs onto one path.
			name: "pack in the wrong directory",
			files: map[string]string{
				"people/0.json":        packWith(peopleBase()),
				"works/0/0.json":       packWith(map[string]string{"a-one": workRec("a-one")}),
				"works/0/zz-one.json":  packWith(map[string]string{"zz-one": workRec("zz-one")}),
				"works/kk/kk-one.json": packWith(map[string]string{"kk-one": workRec("kk-one")}),
			},
			wantLine: "works/0/zz-one.json is not a pack",
		},
		{
			// A contributor invents a directory and a pack in it, and puts
			// entries there that belong in the packs that already exist.
			name: "contributor-invented pack",
			files: map[string]string{
				"people/0.json": packWith(peopleBase()),
				"works/0/0.json": packWith(map[string]string{
					"a-one": workRec("a-one"), "b-two": workRec("b-two"),
				}),
				"works/new-dir/new-dir.json": packWith(map[string]string{"a-three": workRec("a-three")}),
			},
			wantLine: `entry "a-three" belongs in`,
		},
		{
			// One stray subdirectory in a flat family. Treating it as the
			// family's shape reclassified every correctly-placed pack as
			// misplaced - five problems became 1,236.
			name: "stray subdirectory in a flat family",
			files: map[string]string{
				"people/0.json":        packWith(peopleBase()),
				"people/p-mm.json":     packWith(map[string]string{"p-mm": personRec("p-mm", "P Mm")}),
				"people/sub/p-zz.json": packWith(map[string]string{"p-zz": personRec("p-zz", "P Zz")}),
				"works/0/0.json":       packWith(map[string]string{"a-one": workRec("a-one")}),
			},
			wantLine: "people/sub/p-zz.json is not a pack",
		},
		{
			// A file whose name is not a slug names no bound. --check used to
			// instruct the contributor to move entries INTO it.
			name: "invalid slug as a pack name",
			files: map[string]string{
				"people/0.json":        packWith(peopleBase()),
				"people/Bad_Name.json": packWith(map[string]string{"p-zz": personRec("p-zz", "P Zz")}),
				"works/0/0.json":       packWith(map[string]string{"a-one": workRec("a-one")}),
			},
			wantLine: "people/Bad_Name.json is not a pack",
		},
		{
			// An empty pack covers a range and holds nothing, so every slug in
			// that range has nowhere to be.
			name: "empty pack",
			files: map[string]string{
				"people/0.json":    packWith(peopleBase()),
				"people/p-mm.json": `{"entries":{}}`,
				"works/0/0.json":   packWith(map[string]string{"a-one": workRec("a-one")}),
			},
			wantLine: "people/p-mm.json is not a pack",
		},
		{
			// The same slug in two packs, the shape a merge-conflict union
			// leaves behind. The correctly-placed copy is the one readers see,
			// so it is the one that survives.
			name: "duplicate entry across two packs",
			files: map[string]string{
				"people/0.json": packWith(map[string]string{
					"p-aa": personRec("p-aa", "P Aa"),
					"p-mm": personRec("p-mm", "P Mm Stale Copy"),
				}),
				"people/p-mm.json": packWith(map[string]string{"p-mm": personRec("p-mm", "P Mm")}),
				"works/0/0.json":   packWith(map[string]string{"a-one": workRec("a-one")}),
			},
			wantLine: `entry "p-mm" is in both`,
			// The stale duplicate is dropped, so the slug is still present once.
			wantDropped: []string{"people/p-mm"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for rel, body := range tc.files {
				writeFile(t, filepath.Join(dir, filepath.FromSlash(rel)), body)
			}
			before := entrySet(t, dir)

			// 1. metacheck rejects it, so --check must not call it clean.
			problems := metacheckProblems(t, dir)
			if len(problems) == 0 {
				t.Fatalf("metacheck found nothing wrong with the fixture; it no longer reproduces")
			}
			rep := mustCheck(t, dir)
			if rep.Clean() {
				t.Fatalf("--check called a tree clean that metacheck rejects:\n  %s",
					strings.Join(problems, "\n  "))
			}

			// 2. it names the fix.
			if !containsFragment(rep.Lines(), tc.wantLine) {
				t.Errorf("--check lines do not name %q:\n  %s", tc.wantLine,
					strings.Join(rep.Lines(), "\n  "))
			}
			if rep.Summary() == "" {
				t.Error("--check summary is empty for a broken tree")
			}

			// 3. ONE --write converges, losing nothing it did not say it would.
			mustWrite(t, dir)
			if got := metacheckProblems(t, dir); len(got) != 0 {
				t.Fatalf("one --write did not converge, metacheck still reports:\n  %s",
					strings.Join(got, "\n  "))
			}
			assertClean(t, dir)
			assertCanonical(t, dir)
			if got, want := entrySet(t, dir), without(before, tc.wantDropped); !equalStrings(got, want) {
				t.Errorf("entries after the heal = %v, want %v", got, want)
			}

			// 4. a second --write is a byte-level no-op.
			snap := snapshot(t, dir)
			second := mustWrite(t, dir)
			if !second.Clean() {
				t.Errorf("second --write reported work:\n  %s", strings.Join(second.Lines(), "\n  "))
			}
			if diff := treeDiff(snap, snapshot(t, dir)); diff != "" {
				t.Errorf("second --write changed the tree: %s", diff)
			}
		})
	}
}

// The conflict rule keeps the copy readers are seeing, not the misfiled one.
func TestConflictKeepsTheCorrectlyPlacedCopy(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "people", "0.json"), packWith(map[string]string{
		"p-aa": personRec("p-aa", "P Aa"),
		"p-mm": personRec("p-mm", "P Mm Stale Copy"),
	}))
	writeFile(t, filepath.Join(dir, "people", "p-mm.json"),
		packWith(map[string]string{"p-mm": personRec("p-mm", "P Mm")}))

	rep := mustCheck(t, dir)
	if len(rep.Pending.Conflicts) != 1 {
		t.Fatalf("conflicts = %+v, want one", rep.Pending.Conflicts)
	}
	c := rep.Pending.Conflicts[0]
	if c.Kept != "people/p-mm.json" || c.Dropped != "people/0.json" {
		t.Errorf("conflict kept %s / dropped %s, want the correctly-placed copy kept", c.Kept, c.Dropped)
	}

	mustWrite(t, dir)
	got, ok := readPack(t, dir, "people/p-mm.json").Get("p-mm")
	if !ok {
		t.Fatal("the surviving entry is gone")
	}
	if strings.Contains(string(got), "Stale") {
		t.Errorf("survivor = %s, want the correctly-placed copy", got)
	}
	if _, dup := readPack(t, dir, "people/0.json").Get("p-mm"); dup {
		t.Error("the misfiled duplicate is still there")
	}
}

// A tree that is structurally fine but not canonical stays a formatting-only
// run: nothing structural is invented for it.
func TestCheckAgreesWithMetacheckOnAHealthyTree(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "people", "0.json"), packWith(peopleBase()))
	writeFile(t, filepath.Join(dir, "works", "0", "0.json"),
		packWith(map[string]string{"a-one": workRec("a-one")}))

	if got := metacheckProblems(t, dir); len(got) != 0 {
		t.Fatalf("metacheck rejects the healthy fixture:\n  %s", strings.Join(got, "\n  "))
	}
	rep := mustCheck(t, dir)
	if !rep.Pending.Empty() {
		t.Errorf("--check invented structural work: %+v", rep.Pending)
	}
	if len(rep.NonCanonical) != 2 {
		t.Errorf("non-canonical = %v, want the two compact fixtures", rep.NonCanonical)
	}

	w := mustWrite(t, dir)
	if len(w.Wrote) != 0 || len(w.Deleted) != 0 {
		t.Errorf("a structurally healthy tree was restructured: wrote %v, deleted %v", w.Wrote, w.Deleted)
	}
	assertClean(t, dir)
	if got := metacheckProblems(t, dir); len(got) != 0 {
		t.Errorf("metacheck rejects the formatted tree:\n  %s", strings.Join(got, "\n  "))
	}
}

func containsFragment(lines []string, want string) bool {
	for _, l := range lines {
		if strings.Contains(l, want) {
			return true
		}
	}
	return false
}

func without(in, drop []string) []string {
	if len(drop) == 0 {
		return in
	}
	gone := map[string]bool{}
	for _, d := range drop {
		gone[d] = true
	}
	out := in[:0:0]
	for _, s := range in {
		if gone[s] {
			gone[s] = false // drop one occurrence only
			continue
		}
		out = append(out, s)
	}
	return out
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
