package audit_test

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/audit"
	"github.com/kodestar/audiosilo-meta/internal/repair"
	"github.com/kodestar/audiosilo-meta/internal/testpack"
	"github.com/kodestar/audiosilo-meta/pkg/canonical"
	"github.com/kodestar/audiosilo-meta/pkg/check"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// SER-DUP's two ADVISORY family folds reach metarepair through a reviewed acceptance and
// nothing else, through the real fresh-audit gate and writer: a plain spelling folded
// onto the reading-order VARIANT it repeats (the survivor keeps its ordering and
// ordering_of), and a renumbered spelling folded onto the primary whose numbering
// stands - its dropped number named in the applied record's notes.
func TestReviewedFamilyFoldsReachMetarepair(t *testing.T) {
	files := map[string]string{
		"people/xx/jane-doe.json":      testpack.PersonJSON(t, "jane-doe", "Jane Doe"),
		"people/xx/nate-narrator.json": testpack.PersonJSON(t, "nate-narrator", "Nate Narrator"),
		"series/dh/dh-pub.json": testpack.WithField(t, testpack.SeriesJSON(t, "dh-pub", "Dragon Heart (Publication Order)",
			"aa@1", "bb@2", "cc@3"), "ordering", "publication"),
		"series/dh/dh-chrono.json": testpack.WithFields(t, testpack.SeriesJSON(t, "dh-chrono", "Dragon Heart (Chronological Order)",
			"cc@1", "aa@2", "bb@3"), map[string]any{"ordering": "chronological", "ordering_of": "dh-pub"}),
		"series/dh/dh.json": testpack.SeriesJSON(t, "dh", "Dragon Heart", "cc@1", "aa@2", "bb@3"),
		"series/dh/dh-published.json": testpack.WithField(t, testpack.SeriesJSON(t, "dh-published", "Dragon Heart (published order)",
			"aa@1", "cc@2"), "ordering", "publication"),
	}
	for _, id := range []string{"aa", "bb", "cc"} {
		for k, v := range testpack.WorkFiles(t, id, "en", "nate-narrator") {
			files[k] = v
		}
	}
	root := t.TempDir()
	data := filepath.Join(root, "data")
	testpack.Seed(t, data, files)
	load := check.LoadProfile(data, pack.ProfileCore)
	if len(load.Problems) > 0 {
		t.Fatalf("invalid fixture: %v", load.Problems)
	}
	var decisions []map[string]any
	for _, fd := range audit.Analyze(load).Findings(audit.ClassSeriesDup) {
		if fd.Subclass != "family-spelling" && fd.Subclass != "family-renumbered" {
			continue
		}
		p := fd.Propose
		if p.Op != audit.OpMergeSeries || !p.Advisory {
			t.Fatalf("%s proposal = %+v, want an advisory merge-series", fd.Subclass, p)
		}
		raw, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		var r map[string]any
		if err := json.Unmarshal(raw, &r); err != nil {
			t.Fatal(err)
		}
		delete(r, "advisory")
		r["reason"] = "fixture fold reviewed"
		r["decision"] = "accept"
		decisions = append(decisions, r)
	}
	if len(decisions) != 2 {
		t.Fatalf("want a family-spelling and a family-renumbered proposal, got %d", len(decisions))
	}
	// The list must be sorted by identity: both are merge-series, so by target.
	slices.SortFunc(decisions, func(a, b map[string]any) int { return strings.Compare(a["target"].(string), b["target"].(string)) })
	raw, err := json.Marshal(decisions)
	if err != nil {
		t.Fatal(err)
	}
	if raw, err = canonical.Format(raw); err != nil {
		t.Fatal(err)
	}
	audit.SetReviewedForTest(t, raw)
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.email", "fixture@example.com"}, {"config", "user.name", "Fixture"}, {"config", "commit.gpgsign", "false"}, {"add", "-A"}, {"commit", "-qm", "seed"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	opts := repair.Options{DataDir: data, Profile: pack.ProfileCore, Ops: []string{audit.OpMergeSeries}, Write: true}
	rep, err := repair.Run(opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Applied) != 2 || len(rep.Refused) != 0 {
		t.Fatalf("repair applied %+v, refused %+v", rep.Applied, rep.Refused)
	}
	var notes []string
	for _, a := range rep.Applied {
		notes = append(notes, a.Notes...)
	}
	if want := `position of cc: kept "3", dropped "2" from dh-published`; !strings.Contains(strings.Join(notes, "\n"), want) {
		t.Errorf("notes = %v, want %q", notes, want)
	}
	after := check.LoadProfile(data, pack.ProfileCore)
	if len(after.Problems) > 0 {
		t.Fatalf("after repair: %v", after.Problems)
	}
	got := map[string]string{}
	for _, s := range after.Catalog.Series {
		got[s.ID] = s.Ordering + "/" + s.OrderingOf + ":"
		for _, sw := range s.Works {
			got[s.ID] += " " + sw.Work + "@" + sw.Position
		}
	}
	want := map[string]string{
		"dh-pub":    "publication/: aa@1 bb@2 cc@3",
		"dh-chrono": "chronological/dh-pub: cc@1 aa@2 bb@3",
	}
	if len(got) != len(want) || got["dh-pub"] != want["dh-pub"] || got["dh-chrono"] != want["dh-chrono"] {
		t.Fatalf("series after the folds = %v, want %v", got, want)
	}
	if r := after.Catalog.Redirects["series"]; r["dh"] != "dh-chrono" || r["dh-published"] != "dh-pub" {
		t.Errorf("series tombstones = %v", r)
	}
	opts.Write = false
	again, err := repair.Run(opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Applied) != 0 || again.Considered != 0 {
		t.Fatalf("second run=%+v", again)
	}
}
