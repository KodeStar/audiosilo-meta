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
// L-MIX adds three more, which its detector withholds by construction (the locks it
// reads off the other classes) and this pins: a membership moved by two proposals, a
// work moved into one series twice, and a work or series an L-MIX op changes (or a
// drop relies on as the work's home) that a merge in the same audit retires or
// rewrites. And one the reviewed decisions need: two splits of one series keeping it
// for different languages (two orientations).
//
// It is asserted over every fixture that produces proposals, and over the real tree
// by the sampling run, because it is a property of the SET and no single detector
// can see it.
func assertProposalsConsistent(t testing.TB, rep *Report) {
	t.Helper()
	for _, conflict := range proposalConflicts(rep).conflicts {
		t.Error(conflict)
	}
	// metarepair drops a membership naming no home unconditionally - the shape of an
	// asserted drop - so a DETECTOR's drop of a work (L-MIX's) must name its homes.
	for _, class := range classOrder {
		for _, fd := range rep.class(class).rows {
			if p := fd.Propose; p.Op == OpDropMembership && p.Target != "" && len(p.Others) == 0 && fd.Subclass != SubclassAsserted {
				t.Errorf("%s %s drops %s from %s naming no home: only an assertion may", class, fd.Key, p.Target, p.Series)
			}
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
		"translation editions": fixture(t, mergeFiles(sagaTree(t), fateTree(t))),
		"language mix":         mergeFiles(mixSagaTree(t), moveTree(t, "s1@1", "s2@2"), narratedTree(t)),
		"family spellings": seriesFixture(t, []string{"one", "two", "three", "four"}, mergeFiles(dragonFamily(t), map[string]string{
			"series/dh/dh.json":       seriesJSON(t, "dh", "Dragon Heart", "one@1", "two@2", "three@3", "four@4"),
			"series/dh/dh-books.json": seriesJSON(t, "dh-books", "Dragon Heart Books", "one@1", "two@2"),
		})),
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

// A mechanical drop conflicts with every other mechanical proposal changing the same
// membership - an addition of it, a move into the series, a restate, a merge folding
// the work or the series - in either order, since the two would apply or go stale by
// run order; a drop beside a change to another membership is consistent.
func TestADropConflictsWithAnotherChangeToItsMembership(t *testing.T) {
	drop := Proposal{Op: OpDropMembership, Target: "w", Series: "s", Field: "position", From: "1-3"}
	for name, other := range map[string]Proposal{
		"an addition":           {Op: OpAddSeriesMember, Target: "w", Series: "s", Field: "series", To: "4"},
		"a move into":           {Op: OpMoveMembership, Target: "w", Series: "elsewhere", Others: []string{"s"}, Field: "position", From: "2", To: "4"},
		"a restate":             {Op: OpRestatePosition, Target: "w", Series: "s", Field: "position", From: "1-3", To: "1-4"},
		"a merge of the work":   {Op: OpMergeWorks, Target: "survivor", Others: []string{"w"}},
		"a merge of the series": {Op: OpMergeSeries, Target: "survivor", Others: []string{"s"}},
	} {
		for _, order := range [][]Proposal{{drop, other}, {other, drop}} {
			if c := proposalConflicts(proposalReport(order...)).conflicts; len(c) == 0 {
				t.Errorf("%s: %+v reports no conflict", name, order)
			}
		}
	}
	unrelated := Proposal{Op: OpAddSeriesMember, Target: "v", Series: "s", Field: "series", To: "4"}
	for _, order := range [][]Proposal{{drop, unrelated}, {unrelated, drop}} {
		if c := proposalConflicts(proposalReport(order...)).conflicts; len(c) != 0 {
			t.Errorf("%+v: %v", order, c)
		}
	}
}

// A DETECTOR's retitle of a merge loser is consistent (either order leaves one
// catalogue, and the real tree holds such pairs); a REVIEWED one (Finding.reviewed: an
// accepted detector proposal or an assertion) conflicts with a merge folding its work,
// in either order, and with any second retitle of it. The mark, not the subclass, is
// what is read: an unreviewed asserted-subclass finding is not protected. A retitle of
// the merge's survivor is no conflict either way.
func TestAReviewedRetitleConflictsWithAMergeOfItsWork(t *testing.T) {
	merge := Finding{Key: "merge", Propose: Proposal{Op: OpMergeWorks, Target: "survivor", Others: []string{"w"}}}
	retitle := func(subclass string, reviewed bool, target, to string) Finding {
		return Finding{Key: subclass + "/" + target + "/" + to, Subclass: subclass, reviewed: reviewed,
			Propose: Proposal{Op: OpRetitle, Target: target, Field: "title", From: "Old", To: to}}
	}
	report := func(fds ...Finding) *Report {
		f := &findings{class: ClassWorkTitle}
		for _, fd := range fds {
			f.add(fd)
		}
		return &Report{classes: []*findings{f}}
	}
	for _, tc := range []struct {
		name     string
		fds      []Finding
		conflict bool
	}{
		{"detector retitle of the loser", []Finding{merge, retitle("decorated", false, "w", "New")}, false},
		{"unreviewed asserted-subclass retitle of the loser", []Finding{merge, retitle(SubclassAsserted, false, "w", "New")}, false},
		{"asserted retitle of the loser", []Finding{merge, retitle(SubclassAsserted, true, "w", "New")}, true},
		{"asserted retitle of the loser first", []Finding{retitle(SubclassAsserted, true, "w", "New"), merge}, true},
		{"accepted retitle of the loser", []Finding{merge, retitle("decorated", true, "w", "New")}, true},
		{"accepted retitle of the loser first", []Finding{retitle("decorated", true, "w", "New"), merge}, true},
		{"asserted retitle of the survivor", []Finding{merge, retitle(SubclassAsserted, true, "survivor", "New")}, false},
		{"accepted retitle of the survivor", []Finding{merge, retitle("decorated", true, "survivor", "New")}, false},
		{"two asserted retitles", []Finding{retitle(SubclassAsserted, true, "w", "New"), retitle(SubclassAsserted, true, "w", "Newer")}, true},
		{"a detector's and an asserted retitle", []Finding{retitle("decorated", false, "w", "New"), retitle(SubclassAsserted, true, "w", "Newer")}, true},
	} {
		if c := proposalConflicts(report(tc.fds...)).conflicts; (len(c) > 0) != tc.conflict {
			t.Errorf("%s: conflicts = %v, want conflict %v", tc.name, c, tc.conflict)
		}
	}
}
