package repair

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/audit"
)

// worklist_test.go is the central rule's test: a report directory can only NARROW what
// the fresh audit proposes, and a record the fresh run no longer makes the same way is
// refused rather than applied.

// auditReport writes a real metaaudit report for a tree and returns its directory.
func auditReport(t testing.TB, data string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "audit")
	if _, err := audit.Run(audit.Options{DataDir: data, OutDir: dir}); err != nil {
		t.Fatalf("audit.Run: %v", err)
	}
	return dir
}

// A worklist restricts the run to the records it holds, and says how many it did not
// take.
func TestTheWorklistNarrowsTheRun(t *testing.T) {
	data := seedTree(t, twoClusters(t))
	report := auditReport(t, data)
	dropClusterFromReport(t, report, "second")

	rep := run(t, Options{DataDir: data, ReportDir: report, Ops: []string{audit.OpMergeWorks}})
	if len(rep.Applied) != 1 {
		t.Fatalf("applied %+v, want only the cluster the worklist holds", rep.Applied)
	}
	if rep.Applied[0].Target != "hammered" {
		t.Errorf("target = %q, want the first cluster's", rep.Applied[0].Target)
	}
	if rep.NotInWorklist != 1 {
		t.Errorf("not-in-worklist = %d, want 1", rep.NotInWorklist)
	}
}

// THE staleness case: the worklist was reviewed, the tree has since been repaired, and
// the record no longer describes it. Refused and named - which is what makes a second
// run over an old report safe.
func TestAWorklistRecordTheFreshAuditNoLongerMakesIsRefused(t *testing.T) {
	data := seedTree(t, hammeredCluster(t))
	report := auditReport(t, data)

	// The wave lands.
	first := run(t, Options{DataDir: data, Ops: []string{audit.OpMergeWorks}, Write: true})
	if len(first.Applied) != 1 {
		t.Fatalf("the first run applied %+v", first.Applied)
	}
	// Somebody re-runs the SAME reviewed report against the repaired tree.
	second := run(t, Options{DataDir: data, ReportDir: report, Ops: []string{audit.OpMergeWorks}, Write: true})
	if len(second.Applied) != 0 {
		t.Fatalf("a stale worklist record was applied: %+v", second.Applied)
	}
	if len(second.Refused) != 1 || second.Refused[0].Category != CatStaleProposal {
		t.Fatalf("refusals = %+v, want one %s", second.Refused, CatStaleProposal)
	}
	if !strings.Contains(second.Refused[0].Reason, "does not report this finding at all") {
		t.Errorf("reason = %q, want it to say the fresh audit no longer reports it", second.Refused[0].Reason)
	}
}

// A worklist record whose PROPOSAL differs from the fresh run's is refused too, with
// both decisions in the message: the file says one thing and the tree says another, and
// a repair may not pick.
func TestAChangedProposalIsRefusedWithBothDecisionsNamed(t *testing.T) {
	data := seedTree(t, hammeredCluster(t))
	report := auditReport(t, data)
	rewriteReport(t, report, audit.ClassWorkDup, func(fd *audit.Finding) bool {
		if fd.Propose.Op != audit.OpMergeWorks {
			return false
		}
		// The reviewed file names the OTHER record as the survivor.
		fd.Propose.Target, fd.Propose.Others = fd.Propose.Others[0], []string{fd.Propose.Target}
		return true
	})

	rep := run(t, Options{DataDir: data, ReportDir: report, Ops: []string{audit.OpMergeWorks}})
	if len(rep.Applied) != 0 {
		t.Fatalf("a changed proposal was applied: %+v", rep.Applied)
	}
	if len(rep.Refused) != 1 || rep.Refused[0].Category != CatStaleProposal {
		t.Fatalf("refusals = %+v, want one %s", rep.Refused, CatStaleProposal)
	}
	for _, want := range []string{"the worklist asks for", "now proposes"} {
		if !strings.Contains(rep.Refused[0].Reason, want) {
			t.Errorf("reason = %q, want it to mention %q", rep.Refused[0].Reason, want)
		}
	}
}

// --only names records explicitly, and a key that selects nothing is reported: a
// mistyped key must not read as "there was nothing to do".
func TestOnlySelectsByKeyAndReportsAKeyThatMatchesNothing(t *testing.T) {
	data := seedTree(t, twoClusters(t))
	only := filepath.Join(t.TempDir(), "only.txt")
	body := "# a wave, reviewed by hand\nhammered\nno-such-work\n"
	if err := os.WriteFile(only, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	rep := run(t, Options{DataDir: data, OnlyFile: only, Ops: []string{audit.OpMergeWorks}})
	if len(rep.Applied) != 1 || rep.Applied[0].Target != "hammered" {
		t.Fatalf("applied %+v, want only the named cluster", rep.Applied)
	}
	if len(rep.Refused) != 1 || rep.Refused[0].Category != CatNotProposed || rep.Refused[0].Key != "no-such-work" {
		t.Fatalf("refusals = %+v, want the unmatched key named", rep.Refused)
	}
}

// --limit is how a wave is chunked, so it has to take the SAME first N over an
// unchanged tree however often it runs.
func TestLimitTakesTheFirstNInReportOrder(t *testing.T) {
	data := seedTree(t, twoClusters(t))
	first := run(t, Options{DataDir: data, Ops: []string{audit.OpMergeWorks}, Limit: 1})
	if len(first.Applied) != 1 || first.BeyondLimit != 1 {
		t.Fatalf("applied %d (beyond limit %d), want 1 and 1", len(first.Applied), first.BeyondLimit)
	}
	second := run(t, Options{DataDir: data, Ops: []string{audit.OpMergeWorks}, Limit: 1})
	if first.Applied[0].Key != second.Applied[0].Key {
		t.Errorf("two limited runs took different records: %q then %q", first.Applied[0].Key, second.Applied[0].Key)
	}
	all := run(t, Options{DataDir: data, Ops: []string{audit.OpMergeWorks}})
	if len(all.Applied) != 2 {
		t.Fatalf("without a limit the run applied %d, want both clusters", len(all.Applied))
	}
	if all.Applied[0].Key != first.Applied[0].Key {
		t.Errorf("the limited run did not take the report's first record: %q vs %q", first.Applied[0].Key, all.Applied[0].Key)
	}
}

// An advisory proposal is never applied and is never a refusal either: it is counted.
// The report holds tens of thousands of them, and naming each as a refusal would bury
// the ones somebody has to read.
func TestAdvisoryProposalsAreCountedAndNeverApplied(t *testing.T) {
	// A cluster whose members hold different positions in one series: the audit's own
	// position veto makes the merge advisory.
	files := hammeredCluster(t)
	files["series/dr/druid-tales.json"] = seriesJSON(t, "druid-tales", "The Druid Tales", "hammered@3", "hammered-book-3@4")
	data := seedTree(t, files)

	rep := run(t, Options{DataDir: data, Ops: []string{audit.OpMergeWorks}, Write: true})
	if len(rep.Applied) != 0 || len(rep.Refused) != 0 {
		t.Fatalf("applied %+v, refused %+v: an advisory proposal must be left alone", rep.Applied, rep.Refused)
	}
	if rep.Advisory == 0 {
		t.Error("the advisory proposal was not counted")
	}
}

// The flag surface refuses what it cannot act on, before it reads anything.
func TestBadOptionsAreRefused(t *testing.T) {
	data := seedTree(t, hammeredCluster(t))
	t.Run("unknown op", func(t *testing.T) {
		if _, err := Run(Options{DataDir: data, Ops: []string{"delete-everything"}}); err == nil {
			t.Fatal("an unknown --op was accepted")
		}
	})
	t.Run("report directory that is not one", func(t *testing.T) {
		_, err := Run(Options{DataDir: data, ReportDir: t.TempDir()})
		if err == nil || !strings.Contains(err.Error(), "class files") {
			t.Fatalf("err = %v, want a refusal naming the missing class files", err)
		}
	})
	t.Run("output inside the data root", func(t *testing.T) {
		_, err := Run(Options{DataDir: data, OutDir: filepath.Join(data, "report")})
		if err == nil || !strings.Contains(err.Error(), "inside the data root") {
			t.Fatalf("err = %v, want a refusal naming the containment", err)
		}
	})
	t.Run("empty --only file", func(t *testing.T) {
		empty := filepath.Join(t.TempDir(), "none.txt")
		if err := os.WriteFile(empty, []byte("# nothing but a comment\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Run(Options{DataDir: data, OnlyFile: empty}); err == nil {
			t.Fatal("an --only file naming no keys was accepted")
		}
	})
}

// twoClusters is two independent duplicate pairs, for the filter and limit tests.
func twoClusters(t testing.TB) map[string]string {
	t.Helper()
	files := hammeredCluster(t)
	files["works/se/second/work.json"] = workJSON(t, "second", "Second Sight", withAuthors("john-smith"))
	files["works/se/second/recordings/nate-narrator-2020.json"] = recJSON(t, "nate-narrator-2020", "second")
	files["works/se/second-sight-unabridged/work.json"] = workJSON(t, "second-sight-unabridged", "Second Sight (Unabridged)", withAuthors("john-smith"))
	files["works/se/second-sight-unabridged/recordings/nate-narrator-2021.json"] = recJSON(t, "nate-narrator-2021", "second-sight-unabridged")
	files["people/jo/john-smith.json"] = personJSON(t, "john-smith", "John Smith")
	return files
}

// dropClusterFromReport removes every W-DUP record naming a slug from the report, which
// is how a maintainer's reviewed worklist ends up holding a subset.
func dropClusterFromReport(t testing.TB, dir, nameFragment string) {
	t.Helper()
	rewriteReport(t, dir, audit.ClassWorkDup, func(fd *audit.Finding) bool {
		if !strings.Contains(fd.Key, nameFragment) {
			return false
		}
		fd.Propose.Advisory = true // an advisory record is not part of the worklist
		return true
	})
}

// rewriteReport edits a class file in place, applying fn to every record; fn reports
// whether it changed the record.
func rewriteReport(t testing.TB, dir, class string, fn func(*audit.Finding) bool) {
	t.Helper()
	path := filepath.Join(dir, audit.ClassFile(class))
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	sc := bufio.NewScanner(strings.NewReader(string(raw)))
	sc.Buffer(make([]byte, 0, 1<<20), 8<<20)
	changed := 0
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var fd audit.Finding
		if err := json.Unmarshal([]byte(line), &fd); err != nil {
			t.Fatal(err)
		}
		if fn(&fd) {
			changed++
		}
		enc, err := json.Marshal(fd)
		if err != nil {
			t.Fatal(err)
		}
		out.Write(enc)
		out.WriteString("\n")
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if changed == 0 {
		t.Fatalf("rewriteReport changed nothing in %s: the fixture does not produce the record the test needs", path)
	}
	if err := os.WriteFile(path, []byte(out.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}
