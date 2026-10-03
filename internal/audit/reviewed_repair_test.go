package audit_test

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/audit"
	"github.com/kodestar/audiosilo-meta/internal/repair"
	"github.com/kodestar/audiosilo-meta/internal/testpack"
	"github.com/kodestar/audiosilo-meta/pkg/canonical"
	"github.com/kodestar/audiosilo-meta/pkg/check"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// Exercise the real fresh-audit gate and writer, not a direct call to the planner.
// No repair code, options or worklist can bypass the embedded policy.
func TestReviewedDecisionsReachMetarepair(t *testing.T) {
	files := map[string]string{
		"people/xx/jane-doe.json":                    testpack.PersonJSON(t, "jane-doe", "Jane Doe"),
		"people/xx/nate-narrator.json":               testpack.PersonJSON(t, "nate-narrator", "Nate Narrator"),
		"people/xx/anna-sprecher.json":               testpack.PersonJSON(t, "anna-sprecher", "Anna Sprecher"),
		"series/xx/saga.json":                        testpack.SeriesJSON(t, "saga", "Saga", "a@1", "b@2", "c@3", "d@4", "e@5"),
		"works/xx/fixture-gem-red/work.json":         testpack.WorkJSON(t, "fixture-gem-red", "Fixture Gem Red"),
		"works/xx/fixture-gem-red/recordings/r.json": testpack.RecJSON(t, "r", "fixture-gem-red", testpack.WithRecLanguage("de"), testpack.WithNarrators("anna-sprecher")),
	}
	for id, lang := range map[string]string{"a": "en", "b": "en", "c": "en", "d": "de", "e": "de"} {
		narrator := "nate-narrator"
		if lang == "de" {
			narrator = "anna-sprecher"
		}
		for k, v := range testpack.WorkFiles(t, id, lang, narrator) {
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
	fresh := audit.Analyze(load)
	var decisions []map[string]any
	for _, op := range []string{audit.OpSetWorkLanguage, audit.OpSplitSeries} {
		found := false
		for _, fd := range fresh.Findings(audit.ClassLangMix) {
			if fd.Propose.Op != op || fd.Subclass == "other-keeper" {
				continue
			}
			found = true
			if fd.Propose.Advisory != (op == audit.OpSetWorkLanguage) {
				t.Fatalf("source proposal=%+v", fd.Propose)
			}
			raw, err := json.Marshal(fd.Propose)
			if err != nil {
				t.Fatal(err)
			}
			var r map[string]any
			if err := json.Unmarshal(raw, &r); err != nil {
				t.Fatal(err)
			}
			delete(r, "advisory")
			r["reason"] = "fixture source reviewed"
			r["decision"] = "reject"
			if op == audit.OpSetWorkLanguage {
				r["decision"] = "accept"
			}
			decisions = append(decisions, r)
		}
		if !found {
			t.Fatalf("no fresh %s proposal", op)
		}
	}
	raw, err := json.Marshal(decisions)
	if err != nil {
		t.Fatal(err)
	}
	raw, err = canonical.Format(raw)
	if err != nil {
		t.Fatal(err)
	}
	audit.SetReviewedForTest(t, raw)
	// Only the disposable fixture needs commits to exercise repair's clean-tree gate.
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.email", "fixture@example.com"}, {"config", "user.name", "Fixture"}, {"config", "commit.gpgsign", "false"}, {"add", "-A"}, {"commit", "-qm", "seed"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	opts := repair.Options{DataDir: data, Profile: pack.ProfileCore, Ops: []string{audit.OpSetWorkLanguage, audit.OpSplitSeries}, Write: true}
	rep, err := repair.Run(opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Applied) != 1 || rep.Applied[0].Op != audit.OpSetWorkLanguage || len(rep.Refused) != 0 {
		t.Fatalf("repair=%+v", rep)
	}
	after := check.LoadProfile(data, pack.ProfileCore)
	if len(after.Problems) > 0 {
		t.Fatalf("after repair: %v", after.Problems)
	}
	for _, w := range after.Catalog.Works {
		if w.ID == "fixture-gem-red" {
			if w.Language != "de" || w.Recordings[0].Language != "de" {
				t.Fatalf("language correction not written: %+v", w)
			}
		}
	}
	if len(after.Catalog.Series) != 1 || len(after.Catalog.Series[0].Works) != 5 {
		t.Fatal("rejected split changed memberships")
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

// A reviewed acceptance of a contested series' OTHER orientation reaches metarepair: the
// German minority keeps the series' slug and the English majority moves to a new series
// of the same name, positions preserved.
func TestReviewedOtherOrientationReachesMetarepair(t *testing.T) {
	files := map[string]string{
		"people/xx/jane-doe.json":      testpack.PersonJSON(t, "jane-doe", "Jane Doe"),
		"people/xx/otto-autor.json":    testpack.PersonJSON(t, "otto-autor", "Otto Autor"),
		"people/xx/nate-narrator.json": testpack.PersonJSON(t, "nate-narrator", "Nate Narrator"),
		"people/xx/anna-sprecher.json": testpack.PersonJSON(t, "anna-sprecher", "Anna Sprecher"),
		"series/xx/saga.json":          testpack.SeriesJSON(t, "saga", "Saga", "a@1", "b@2", "c@3", "d@4", "e@5"),
	}
	for id, lang := range map[string]string{"a": "en", "b": "en", "c": "en", "d": "de", "e": "de"} {
		narrator, author := "nate-narrator", "jane-doe"
		if lang == "de" {
			narrator, author = "anna-sprecher", "otto-autor" // two franchises of one name: the COLLISION signal
		}
		for k, v := range testpack.WorkFiles(t, id, lang, narrator, testpack.WithAuthors(author)) {
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
	for _, fd := range audit.Analyze(load).Findings(audit.ClassLangMix) {
		p := fd.Propose
		if p.Op != audit.OpSplitSeries || p.From != "de" {
			continue
		}
		if !p.Advisory || p.To != "en" || len(p.Others) != 3 {
			t.Fatalf("other orientation = %+v", p)
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
		r["reason"] = "the German books are the series; the English franchise of the name is another series"
		r["decision"] = "accept"
		decisions = append(decisions, r)
	}
	if len(decisions) != 1 {
		t.Fatalf("want one other-orientation split, got %d", len(decisions))
	}
	raw, err := json.Marshal(decisions)
	if err != nil {
		t.Fatal(err)
	}
	raw, err = canonical.Format(raw)
	if err != nil {
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
	opts := repair.Options{DataDir: data, Profile: pack.ProfileCore, Ops: []string{audit.OpSplitSeries}, Write: true}
	rep, err := repair.Run(opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Applied) != 1 || len(rep.Refused) != 0 {
		t.Fatalf("repair=%+v", rep)
	}
	after := check.LoadProfile(data, pack.ProfileCore)
	if len(after.Problems) > 0 {
		t.Fatalf("after repair: %v", after.Problems)
	}
	members := map[string]string{}
	for _, s := range after.Catalog.Series {
		for _, sw := range s.Works {
			members[s.ID] += sw.Work + "@" + sw.Position + " "
		}
		if s.ID == "saga-2" && s.Name != "Saga" {
			t.Errorf("new series named %q", s.Name)
		}
	}
	if len(members) != 2 || members["saga"] != "d@4 e@5 " || members["saga-2"] != "a@1 b@2 c@3 " {
		t.Fatalf("series after the split = %v, want the German minority keeping saga", members)
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

// A reviewed ASSERTION reaches metarepair through the same fresh-audit gate: the merge
// no title shows (an alternate title of a series member) and the membership no record
// states are applied, and a re-run finds both STALE and proposes nothing.
func TestReviewedAssertReachesMetarepair(t *testing.T) {
	files := map[string]string{
		"people/xx/jane-doe.json":      testpack.PersonJSON(t, "jane-doe", "Jane Doe"),
		"people/xx/nate-narrator.json": testpack.PersonJSON(t, "nate-narrator", "Nate Narrator"),
		"series/xx/rangers-apprentice.json": testpack.SeriesJSON(t, "rangers-apprentice", "Ranger's Apprentice",
			"the-ruins-of-gorlan@1", "the-battle-for-skandia@4"),
		"series/xx/kingsbridge.json": testpack.SeriesJSON(t, "kingsbridge", "Kingsbridge", "world-without-end@2"),
	}
	for id, title := range map[string]string{
		"the-ruins-of-gorlan":      "The Ruins of Gorlan",
		"the-battle-for-skandia":   "The Battle for Skandia",
		"oakleaf-bearers":          "Oakleaf Bearers",
		"the-pillars-of-the-earth": "The Pillars of the Earth",
		"world-without-end":        "World Without End",
	} {
		files["works/xx/"+id+"/work.json"] = testpack.WorkJSON(t, id, title)
		files["works/xx/"+id+"/recordings/r.json"] = testpack.RecJSON(t, "r", id)
	}
	root := t.TempDir()
	data := filepath.Join(root, "data")
	testpack.Seed(t, data, files)
	audit.SetReviewedForTest(t, []byte(`[
  {
    "decision": "assert",
    "field": "series",
    "op": "add-series-member",
    "reason": "the first Kingsbridge novel states no series",
    "series": "kingsbridge",
    "target": "the-pillars-of-the-earth",
    "to": "1"
  },
  {
    "decision": "assert",
    "op": "merge-works",
    "others": [
      "oakleaf-bearers"
    ],
    "reason": "Oakleaf Bearers is the US title of Ranger's Apprentice 4",
    "target": "the-battle-for-skandia"
  }
]
`))
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.email", "fixture@example.com"}, {"config", "user.name", "Fixture"}, {"config", "commit.gpgsign", "false"}, {"add", "-A"}, {"commit", "-qm", "seed"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	opts := repair.Options{DataDir: data, Ops: []string{audit.OpMergeWorks, audit.OpAddSeriesMember}, Write: true}
	rep, err := repair.Run(opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Applied) != 2 || len(rep.Refused) != 0 {
		t.Fatalf("repair applied %+v, refused %+v", rep.Applied, rep.Refused)
	}
	for _, a := range rep.Applied {
		if a.Subclass != "asserted" {
			t.Errorf("applied %+v, want the asserted findings", a)
		}
	}
	after := check.Load(data)
	if len(after.Problems) > 0 {
		t.Fatalf("after repair: %v", after.Problems)
	}
	if to, ok := after.Catalog.Redirects.Survivor("works", "oakleaf-bearers"); !ok || to != "the-battle-for-skandia" {
		t.Fatalf("oakleaf-bearers resolves to %q, %v", to, ok)
	}
	for _, s := range after.Catalog.Series {
		if s.ID == "kingsbridge" && (len(s.Works) != 2 || s.Works[1].Work != "the-pillars-of-the-earth" || s.Works[1].Position != "1") {
			t.Fatalf("kingsbridge = %+v", s.Works)
		}
	}
	fresh := audit.Analyze(after)
	if len(fresh.Reviewed.Stale()) != 2 || len(fresh.Reviewed.Outcomes()) != 0 {
		t.Fatalf("after applying, reviewed = %+v, want both assertions stale", fresh.Reviewed)
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
