package pack

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// A survey parses its family on the worker pool (Reader.prefetch) and warms the
// renders its report sizes on it too, so what Pending and HealPending report has
// to be a function of the tree and not of the scheduler. The fixture plants one
// of each kind of structural problem among more packs than there are workers,
// and the report is compared between one worker and many.

func pinProcs(t *testing.T, n int) {
	t.Helper()
	restore := runtime.GOMAXPROCS(n)
	t.Cleanup(func() { runtime.GOMAXPROCS(restore) })
}

// surveyFixture is a works family in its directory layout holding a misplaced
// entry, a duplicate slug, a file nested too deep, an unreadable file and a pack
// over its entry cap, beside twenty well-formed packs.
func surveyFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		// "ab" sorts below "bb", so it belongs in 0.json: misplaced.
		"works/0/0.json":  packOfEntries([2]string{"aa", "A"}),
		"works/0/bb.json": packOfEntries([2]string{"bb", "B"}, [2]string{"ab", "misfiled"}),
		// Over the entry cap set below: a due split.
		"works/0/cc.json": packOfEntries([2]string{"cc", "C"}, [2]string{"cd", "C2"}, [2]string{"ce", "C3"}),
		// Not JSON at all, and a file nested deeper than a pack can sit.
		"works/0/dd.json":        `{`,
		"works/0/extra/far.json": packOfEntries([2]string{"zz", "Z"}),
	}
	for i := range 20 {
		slug := fmt.Sprintf("k%02d", i)
		pairs := [][2]string{{slug, "K"}}
		// k05 is also in k06's pack: a duplicate entry.
		if i == 6 {
			pairs = append(pairs, [2]string{"k05", "copy"})
		}
		files["works/0/"+slug+".json"] = packOfEntries(pairs...)
	}
	writeFiles(t, dir, files)
	return dir
}

func TestSurveyIsTheSameOnOneWorkerAndMany(t *testing.T) {
	withCaps(t, FamilyWorks, Caps{TargetSize: 1 << 20, HardSize: 1 << 21, Entries: 2, DirPacks: 512})

	var want []string
	for _, procs := range []int{1, 8} {
		pinProcs(t, procs)
		p := pendingOf(t, surveyFixture(t), FamilyWorks)
		for what, n := range map[string]int{
			"misplaced": len(p.Misplaced), "conflicts": len(p.Conflicts), "salvage": len(p.Salvage),
			"unreadable": len(p.Unreadable), "due splits": len(p.Packs),
		} {
			if n == 0 {
				t.Errorf("GOMAXPROCS=%d: the fixture's %s were not reported:\n%s", procs, what, strings.Join(p.Lines(), "\n"))
			}
		}
		got := p.Lines()
		if want == nil {
			want = got
			continue
		}
		if !slices.Equal(got, want) {
			t.Errorf("GOMAXPROCS=%d reports differently:\n--- one worker ---\n%s\n--- many ---\n%s",
				procs, strings.Join(want, "\n"), strings.Join(got, "\n"))
		}
	}

	// HealPending reports exactly what Pending does, on the pool as well.
	dir := surveyFixture(t)
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	hp, err := s.HealPending(FamilyWorks)
	if err != nil {
		t.Fatal(err)
	}
	if got := hp.Lines(); !slices.Equal(got, want) {
		t.Errorf("HealPending on the pool differs from Pending:\n%s\n---\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// prefetch only ever ADDS parses: a file it cannot read or parse is left for the
// Read that follows, which must then answer exactly as it would have without
// the prefetch - and a pack already held is not read again.
func TestPrefetchLeavesBrokenFilesToRead(t *testing.T) {
	pinProcs(t, 8)
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"people/0.json":  packOf1("ann-doe"),
		"people/bb.json": packOf1("bob-roe"),
		"people/zz.json": `{`,
	})
	r := NewReader(dir)
	held, err := r.Read("people/0.json")
	if err != nil {
		t.Fatal(err)
	}
	// Gone from disk: a prefetch that re-read it would cache an absence.
	if err := os.Remove(filepath.Join(dir, "people", "0.json")); err != nil {
		t.Fatal(err)
	}

	r.prefetch([]string{"people/0.json", "people/bb.json", "people/zz.json", "people/gone.json"})

	if f, ok := r.Cached("people/0.json"); !ok || f != held {
		t.Error("prefetch replaced a pack the reader already held")
	}
	if f, ok := r.Cached("people/bb.json"); !ok {
		t.Error("prefetch did not cache a pack that parsed")
	} else if _, ok := f.Get("bob-roe"); !ok {
		t.Error("the prefetched pack lost its entry")
	}
	for _, rel := range []string{"people/zz.json", "people/gone.json"} {
		if _, ok := r.peek(rel); ok {
			t.Errorf("prefetch cached %s, which it could not parse", rel)
		}
	}
	if _, err := r.Read("people/zz.json"); err == nil || !strings.Contains(err.Error(), "people/zz.json") {
		t.Errorf("Read after prefetch = %v, want the parse error naming the path", err)
	}
	if f, err := r.Read("people/gone.json"); err != nil || f.Len() != 0 {
		t.Errorf("Read of a missing file after prefetch = %v, %v; want the empty stand-in", f, err)
	}
	if _, ok := r.Cached("people/gone.json"); ok {
		t.Error("the missing file's stand-in is answering as a real pack")
	}
}
