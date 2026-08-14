package repair

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/audit"
	"github.com/kodestar/audiosilo-meta/internal/format"
	"github.com/kodestar/audiosilo-meta/pkg/check"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// run_test.go covers the run-level guarantees: the dry run writes nothing, a second run
// over repaired data does nothing, two runs over one tree are byte-identical, and
// --write refuses a tree it cannot safely write into.

// The default is a dry run, and a dry run must leave the tree byte-identical - the
// property that makes the plan reviewable before anything is applied.
func TestADryRunWritesNothing(t *testing.T) {
	data := seedTree(t, hammeredCluster(t))
	before := treeBytes(t, data)

	rep := run(t, Options{DataDir: data, Ops: []string{audit.OpMergeWorks}})
	if len(rep.Applied) != 1 {
		t.Fatalf("the plan is empty: %+v / %+v", rep.Applied, rep.Refused)
	}
	if rep.Wrote {
		t.Error("a run without Write reported itself as applied")
	}
	if got := treeBytes(t, data); !equalTrees(got, before) {
		t.Error("the dry run changed the tree")
	}
}

// IDEMPOTENT BY CONSTRUCTION: the second run re-audits the repaired tree, finds nothing
// to propose, and therefore does nothing - no refusals to triage, no writes.
func TestASecondRunOverRepairedDataIsANoOp(t *testing.T) {
	data := seedTree(t, hammeredCluster(t))
	first := run(t, Options{DataDir: data, Write: true})
	if len(first.Applied) == 0 {
		t.Fatalf("the first run applied nothing: %+v", first.Refused)
	}
	after := treeBytes(t, data)

	second := run(t, Options{DataDir: data, Write: true})
	if len(second.Applied) != 0 || len(second.Refused) != 0 {
		t.Fatalf("the second run applied %+v and refused %+v", second.Applied, second.Refused)
	}
	if second.Considered != 0 {
		t.Errorf("considered = %d, want nothing left to propose", second.Considered)
	}
	if got := treeBytes(t, data); !equalTrees(got, after) {
		t.Error("the second run changed the tree")
	}
}

// Two runs over one tree produce byte-identical reports, which is what makes a wave
// reviewable in a diff.
func TestTheReportIsDeterministic(t *testing.T) {
	data := seedTree(t, twoClusters(t))
	one, two := filepath.Join(t.TempDir(), "a"), filepath.Join(t.TempDir(), "b")
	run(t, Options{DataDir: data, OutDir: one})
	run(t, Options{DataDir: data, OutDir: two})
	for _, name := range []string{appliedFile, refusedFile, summaryFile} {
		a, err := os.ReadFile(filepath.Join(one, name))
		if err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(filepath.Join(two, name))
		if err != nil {
			t.Fatal(err)
		}
		if string(a) != string(b) {
			t.Errorf("%s differs between two runs over one tree", name)
		}
	}
}

// After a write the tree is canonical, well-placed and valid - and the run says so.
// Leaving a red tree quietly behind is the failure mode this gate exists for.
func TestAWriteLeavesTheTreeGreenAndCanonical(t *testing.T) {
	files := hammeredCluster(t)
	files["works/ha/hammered/characters.json"] = charactersJSON(t, "hammered", "atticus")
	data := seedTree(t, files)

	rep := run(t, Options{DataDir: data, Write: true})
	if !rep.Wrote || len(rep.PostProblems) != 0 {
		t.Fatalf("wrote=%v post-write problems=%v", rep.Wrote, rep.PostProblems)
	}
	if res := check.Load(data); len(res.Problems) > 0 {
		t.Errorf("metacheck reports problems after the repair: %v", res.Problems)
	}
	fr, err := format.Check(data)
	if err != nil {
		t.Fatal(err)
	}
	if !fr.Clean() {
		t.Errorf("metafmt --check is not clean after the repair: %s", fr.Summary())
	}
}

// --write refuses a data tree with uncommitted changes. This pass DELETES records, so
// `git checkout -- data` is the whole recovery story, and a dirty tree has no such net.
func TestWriteRefusesAnUncommittedTree(t *testing.T) {
	data := gitRepo(t, hammeredCluster(t))
	// The shape a half-applied run leaves: a change nobody committed.
	if err := os.WriteFile(filepath.Join(data, "scratch.txt"), []byte("uncommitted\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Run(Options{DataDir: data, Write: true})
	if err == nil {
		t.Fatal("an uncommitted tree was written to")
	}
	for _, want := range []string{"uncommitted changes", "git checkout"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want it to mention %q", err, want)
		}
	}
	// The same tree, committed, applies - so the refusal is not merely "is this a
	// repository".
	if err := os.Remove(filepath.Join(data, "scratch.txt")); err != nil {
		t.Fatal(err)
	}
	rep := run(t, Options{DataDir: data, Write: true})
	if len(rep.Applied) == 0 {
		t.Errorf("a committed tree applied nothing: %+v", rep.Refused)
	}
}

// A tree that does not validate is planned (so a maintainer can see what the audit
// says) but never written to: a record the loader dropped is invisible to the plan, and
// writing over it would destroy it silently.
func TestWriteRefusesATreeThatDoesNotValidate(t *testing.T) {
	files := hammeredCluster(t)
	files["works/ha/hammered/work.json"] = workJSON(t, "hammered", "Hammered", withoutLanguage())
	data := seedTreeAllowingProblems(t, files)

	rep, err := Run(Options{DataDir: data, Write: true})
	if err == nil {
		t.Fatal("a tree with validation problems was written to")
	}
	if !strings.Contains(err.Error(), "metacheck") {
		t.Errorf("err = %v, want it to name metacheck", err)
	}
	if rep == nil || rep.LoaderProblems == 0 {
		t.Errorf("the report does not carry the loader's problem count: %+v", rep)
	}
	// The dry run over the same tree still reports the plan, with the warning.
	dry := run(t, Options{DataDir: data})
	if dry.LoaderProblems == 0 {
		t.Error("the dry run did not report the tree's validation problems")
	}
}

// treeBytes reads every pack file plus the tombstone table, so a test can assert a run
// left the tree untouched byte for byte.
func treeBytes(t testing.TB, data string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(data, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		raw, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		rel, rerr := filepath.Rel(data, path)
		if rerr != nil {
			return rerr
		}
		out[rel] = string(raw)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func equalTrees(a, b map[string]string) bool {
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

// The real tree, as a DRY RUN. It is the only test that sees the catalogue the pass is
// built for, and what it proves is that the plan composes over 280k works rather than
// refusing everything - the failure a fixture cannot show.
//
// IT MUST NOT DEPEND ON HOW FAR THE WAVE HAS GOT, which is what it read as a failure
// once two chunks had landed. Two properties keep it independent: it plans the WHOLE
// class rather than the first --limit proposals of it, and it holds the deliberate
// human-queue refusals out of the ratio instead of counting them as composition
// failures. Both are stated at the assertions.
//
// It does not run under -race, for pkg/check's reason (TestRealDataTree): the fixtures
// cover every code path, the real tree adds DATA coverage, and the load costs minutes
// under the detector.
func TestRealTreeDryRunComposesAPlan(t *testing.T) {
	if raceEnabled {
		t.Skip("skipped under -race: the fixtures cover every path; the real tree is data coverage, and CI plans it in the non-race run")
	}
	const dataDir = "../../data"
	if _, err := os.Stat(dataDir); err != nil {
		t.Skipf("no data tree at %s: %v", dataDir, err)
	}
	layouts, err := pack.Detect(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, def := range pack.Families() {
		if layouts[def.Family] == pack.LayoutLegacy {
			t.Skipf("%s/%s predates the pack migration", dataDir, def.Family.Root())
		}
	}
	before := treeStat(t, dataDir)

	// The WHOLE class, not a --limit window. The limit made this test read the
	// alphabetical FRONT of whatever the tree still holds, which a repair wave moves:
	// sidecar-member-collision is refused by every run and therefore never leaves the
	// list, so after two applied chunks the first 50 were 28 of them - expected campaign
	// state failing a composition assertion. The whole class is wave-independent, and it
	// costs 12 seconds over the load this test already pays for (36s -> 48s over 280k
	// works, 1,385 proposals planned).
	rep, err := Run(Options{DataDir: dataDir, Ops: []string{audit.OpMergeWorks}})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Considered == 0 {
		t.Fatal("no non-advisory merge proposal at all: is the tree short of duplicates?")
	}
	if rep.BeyondLimit != 0 {
		t.Errorf("%d proposals beyond a limit this run sets none of", rep.BeyondLimit)
	}
	// The HUMAN QUEUE is separated from the ratio rather than counted in it: both halves
	// of a duplicate carrying the same works-community member is a real shape on this
	// tree and a deliberate refusal (which CC BY-SA entry describes the survivor is a
	// human decision), so those proposals accumulate at the front of the class as the
	// mechanical ones are applied away.
	humanQueue := map[Category]bool{CatSidecarCollision: true}
	var mechanicalRefusals []Refusal
	for _, r := range rep.Refused {
		if !slices.Contains(Categories(), r.Category) {
			t.Errorf("refusal %s carries the unknown category %q, which no triage view can read", r.Key, r.Category)
		}
		if !humanQueue[r.Category] {
			mechanicalRefusals = append(mechanicalRefusals, r)
		}
	}
	actionable := rep.Considered - (len(rep.Refused) - len(mechanicalRefusals))
	if actionable == 0 {
		t.Skip("every remaining proposal in the class is a human-queue refusal: the mechanical wave is finished")
	}
	if len(rep.Applied) == 0 {
		t.Fatalf("the plan refused all %d actionable proposals: %+v", actionable,
			rep.Refused[:min(3, len(rep.Refused))])
	}
	if len(mechanicalRefusals) > len(rep.Applied) {
		t.Errorf("refused %d of %d actionable proposals mechanically, which is more than half: %+v",
			len(mechanicalRefusals), actionable, mechanicalRefusals)
	}
	for _, a := range rep.Applied {
		if len(a.Notes) == 0 {
			t.Errorf("applied %s carries no notes, so the report says nothing about what it did", a.Key)
		}
	}
	if got := treeStat(t, dataDir); got != before {
		t.Error("the dry run modified the real data tree")
	}
}

// treeStat is a cheap fingerprint of the real tree (file count and total size), for the
// dry-run-writes-nothing assertion at 1.4GB, where reading every byte twice is not.
func treeStat(t testing.TB, data string) [2]int64 {
	t.Helper()
	var files, bytes int64
	err := filepath.Walk(data, func(_ string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		files++
		bytes += info.Size()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return [2]int64{files, bytes}
}
