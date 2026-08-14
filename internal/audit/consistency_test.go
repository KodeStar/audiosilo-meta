package audit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/testpack"
)

// assertProposalsConsistent is the invariant a repair pass depends on: the set of
// NON-ADVISORY proposals must be applicable in any order and produce one catalogue.
//
// Three ways it can fail, all of which the first draft did on the real tree:
//
//   - a work named by two merges (25 works were told to fold onto two targets);
//   - a work that is a target in one merge and a loser in another (9 were);
//   - two proposals claiming one series slot.
//
// It is asserted over every fixture that produces proposals, and over the real tree
// by the sampling run, because it is a property of the SET and no single detector
// can see it.
func assertProposalsConsistent(t testing.TB, rep *Report) {
	t.Helper()
	mergeTarget := map[string]string{} // work -> the target it was told to fold onto
	isTarget := map[string]string{}    // work -> the record naming it as a target
	slot := map[string]string{}        // "<series>@<pos>" -> the record claiming it

	for _, class := range classOrder {
		for _, r := range rep.class(class).rows {
			p := r.Propose
			if p.Advisory {
				continue // an advisory proposal is a reading list, not an instruction
			}
			switch p.Op {
			case OpMergeWorks, OpMergeSeries:
				if p.Target != "" {
					isTarget[p.Target] = r.Key
				}
				for _, o := range p.Others {
					if prev, dup := mergeTarget[o]; dup && prev != p.Target {
						t.Errorf("%s: %s is told to fold onto both %s and %s", r.Key, o, prev, p.Target)
					}
					mergeTarget[o] = p.Target
				}
			case OpAddSeriesMember:
				if p.Series == "" || p.To == "" {
					continue
				}
				key := p.Series + "@" + p.To
				if prev, dup := slot[key]; dup {
					t.Errorf("%s and %s both claim series slot %s", prev, r.Key, key)
				}
				slot[key] = r.Key
			}
		}
	}
	for w, by := range mergeTarget {
		if other, both := isTarget[w]; both {
			t.Errorf("%s is a merge target in %s and a loser in the proposal that folds it onto %s", w, other, by)
		}
	}
}

// A tree built to produce overlapping clusters: three records of one book whose keys
// only pairwise agree, so the naive grouping emits two clusters sharing a work.
func TestProposalsAreConsistentAcrossOverlappingClusters(t *testing.T) {
	files := fixture(t, map[string]string{
		"works/ha/hammered/work.json":                    workJSON(t, "hammered", "Hammered"),
		"works/ha/hammered/recordings/a.json":            recJSON(t, "a", "hammered"),
		"works/ha/hammered-book-3/work.json":             workJSON(t, "hammered-book-3", "Hammered: The Druid Tales, Book 3"),
		"works/ha/hammered-book-3/recordings/b.json":     recJSON(t, "b", "hammered-book-3"),
		"works/ha/hammered-unabridged/work.json":         workJSON(t, "hammered-unabridged", "Hammered (Unabridged)"),
		"works/ha/hammered-unabridged/recordings/c.json": recJSON(t, "c", "hammered-unabridged"),
		"series/dr/druid-tales.json":                     seriesJSON(t, "druid-tales", "The Druid Tales", "hammered@3"),
	})
	rep := runFixture(t, files)
	assertProposalsConsistent(t, rep)

	// And they really did land in ONE cluster rather than two overlapping ones.
	got := subclassOf(t, rep, ClassWorkDup, dupTitleAuthor)
	if len(got) != 1 {
		t.Fatalf("want one closed cluster, got %d: %+v", len(got), got)
	}
	if len(got[0].Works) != 3 {
		t.Errorf("cluster holds %d works, want all 3", len(got[0].Works))
	}
}

// The same assertion over every other fixture that proposes anything, so a future
// detector cannot introduce a contradiction unnoticed.
func TestProposalsAreConsistentAcrossTheFixtures(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"duplicate pair": fixture(t, map[string]string{
			"works/ha/hammered/work.json":                workJSON(t, "hammered", "Hammered"),
			"works/ha/hammered/recordings/a.json":        recJSON(t, "a", "hammered"),
			"works/ha/hammered-book-3/work.json":         workJSON(t, "hammered-book-3", "Hammered: The Druid Tales, Book 3"),
			"works/ha/hammered-book-3/recordings/b.json": recJSON(t, "b", "hammered-book-3"),
			"series/dr/druid-tales.json":                 seriesJSON(t, "druid-tales", "The Druid Tales", "hammered@3"),
		}),
		"two works claiming one slot": fixture(t, map[string]string{
			"works/ch/chaos-seeds-book-3-a/work.json":         workJSON(t, "chaos-seeds-book-3-a", "Alpha: Chaos Seeds, Book 3"),
			"works/ch/chaos-seeds-book-3-a/recordings/a.json": recJSON(t, "a", "chaos-seeds-book-3-a"),
			"works/ch/chaos-seeds-book-3-b/work.json":         workJSON(t, "chaos-seeds-book-3-b", "Beta: Chaos Seeds, Book 3"),
			"works/ch/chaos-seeds-book-3-b/recordings/b.json": recJSON(t, "b", "chaos-seeds-book-3-b"),
			"works/fo/founding/work.json":                     workJSON(t, "founding", "The Founding"),
			"works/fo/founding/recordings/c.json":             recJSON(t, "c", "founding"),
			"series/ch/chaos-seeds.json":                      seriesJSON(t, "chaos-seeds", "Chaos Seeds", "founding@1"),
		}),
		"duplicate series spellings": fixture(t, map[string]string{
			"works/on/one/work.json":         workJSON(t, "one", "One"),
			"works/on/one/recordings/a.json": recJSON(t, "a", "one"),
			"works/tw/two/work.json":         workJSON(t, "two", "Two"),
			"works/tw/two/recordings/b.json": recJSON(t, "b", "two"),
			"series/aa/alpha.json":           seriesJSON(t, "alpha", "Dragon Heart", "one@1"),
			"series/bb/beta.json":            seriesJSON(t, "beta", "Dragon Heart Series", "two@1"),
		}),
	} {
		t.Run(name, func(t *testing.T) {
			assertProposalsConsistent(t, runFixture(t, files))
		})
	}
}

// Two works whose titles both state the same series and slot: neither may take it.
func TestWorkNoSeriesRefusesAContestedSlot(t *testing.T) {
	rep := runFixture(t, fixture(t, map[string]string{
		"works/ch/alpha-book-3/work.json":         workJSON(t, "alpha-book-3", "Alpha: Chaos Seeds, Book 3"),
		"works/ch/alpha-book-3/recordings/a.json": recJSON(t, "a", "alpha-book-3"),
		"works/ch/beta-book-3/work.json":          workJSON(t, "beta-book-3", "Beta: Chaos Seeds, Book 3"),
		"works/ch/beta-book-3/recordings/b.json":  recJSON(t, "b", "beta-book-3"),
		"works/fo/founding/work.json":             workJSON(t, "founding", "The Founding"),
		"works/fo/founding/recordings/c.json":     recJSON(t, "c", "founding"),
		"series/ch/chaos-seeds.json":              seriesJSON(t, "chaos-seeds", "Chaos Seeds", "founding@1"),
	}))
	got := subclassOf(t, rep, ClassWorkNoSeries, noSeriesAndPosition)
	if len(got) != 2 {
		t.Fatalf("want two records, got %d", len(got))
	}
	for _, r := range got {
		if !r.Propose.Advisory {
			t.Errorf("%s: a contested slot must not be claimed mechanically", r.Key)
		}
		if !strings.Contains(r.Propose.Reason, "claimed by") {
			t.Errorf("%s: reason = %q, want the rival claimants named", r.Key, r.Propose.Reason)
		}
	}
}

// A pair can conflict in MORE THAN ONE series, and which one the veto names must not
// depend on map iteration order - it did, and the real-tree report differed between
// runs over an unchanged tree.
func TestPositionConflictVetoNamesASeriesDeterministically(t *testing.T) {
	files := fixture(t, map[string]string{
		"works/ho/hotdog-collected/work.json":          workJSON(t, "hotdog-collected", "The Great Adventures of Hotdog Man"),
		"works/ho/hotdog-collected/recordings/a.json":  recJSON(t, "a", "hotdog-collected", withRuntime(486)),
		"works/ho/hotdog-volume-one/work.json":         workJSON(t, "hotdog-volume-one", "The Great Adventures of Hotdog Man."),
		"works/ho/hotdog-volume-one/recordings/b.json": recJSON(t, "b", "hotdog-volume-one", withRuntime(480)),
		// BOTH works sit in both series, at conflicting positions in each.
		"series/th/the-collected-adventures.json": seriesJSON(t, "the-collected-adventures", "The Collected Adventures of Hotdog Man",
			"hotdog-collected@1-6", "hotdog-volume-one@1"),
		"series/th/the-great-adventures.json": seriesJSON(t, "the-great-adventures", "The Great Adventures of Hotdog Man Series",
			"hotdog-collected@1-6", "hotdog-volume-one@1"),
	})
	data := filepath.Join(t.TempDir(), "data")
	testpack.Seed(t, data, files)

	var reasons []string
	for i := 0; i < 5; i++ {
		out := filepath.Join(t.TempDir(), "out")
		rep, err := Run(Options{DataDir: data, OutDir: out})
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		var got string
		for _, r := range rep.class(ClassWorkDup).rows {
			if strings.Contains(r.Propose.Reason, "different positions") {
				got = r.Propose.Reason
			}
		}
		if got == "" {
			t.Fatalf("run %d: the position-conflict veto did not fire", i)
		}
		reasons = append(reasons, got)
	}
	for i := 1; i < len(reasons); i++ {
		if reasons[i] != reasons[0] {
			t.Fatalf("the veto named a different series between runs:\n  %s\n  %s", reasons[0], reasons[i])
		}
	}
	// Sorted order means the alphabetically first series id is the one named.
	if !strings.Contains(reasons[0], "the-collected-adventures") {
		t.Errorf("reason = %q, want the alphabetically first series id", reasons[0])
	}
}

// A symlink outside the tree pointing into it defeats a lexical containment test.
func TestOutDirGuardResolvesSymlinks(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "data")
	testpack.Seed(t, data, map[string]string{
		"works/pl/plain/work.json": workJSON(t, "plain", "Plain"),
		"people/ja/jane-doe.json":  personJSON(t, "jane-doe", "Jane Doe"),
	})
	link := filepath.Join(root, "sneaky")
	if err := os.Symlink(data, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	for _, out := range []string{link, filepath.Join(link, "report")} {
		if _, err := Run(Options{DataDir: data, OutDir: out}); err == nil {
			t.Errorf("-o %s was accepted; it resolves inside the data root", out)
		} else if !strings.Contains(err.Error(), "inside the data root") {
			t.Errorf("-o %s failed with the wrong error: %v", out, err)
		}
	}
	// The data root reached THROUGH the link must be recognized too.
	if _, err := Run(Options{DataDir: link, OutDir: filepath.Join(data, "report")}); err == nil {
		t.Error("a report inside the data root was accepted when the root was named by its link")
	}
	// Nothing was written into the tree.
	for _, name := range []string{"report", "SUMMARY.md"} {
		if _, err := os.Stat(filepath.Join(data, name)); !os.IsNotExist(err) {
			t.Errorf("a refused run created data/%s (stat err = %v)", name, err)
		}
	}
}
