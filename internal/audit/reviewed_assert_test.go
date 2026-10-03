package audit

import (
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/canonical"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// reviewed_assert_test.go pins the third reviewed decision, assert: an entry that
// SOURCES a proposal no detector makes.

// rangerTree holds books no detector relates: an alternate title beside the series
// member it is, a reissue's original title, and a novel stating no series.
func rangerTree(t testing.TB) map[string]string {
	t.Helper()
	files := map[string]string{
		"people/ja/jane-doe.json":      personJSON(t, "jane-doe", "Jane Doe"),
		"people/na/nate-narrator.json": personJSON(t, "nate-narrator", "Nate Narrator"),
		"series/ra/rangers-apprentice.json": seriesJSON(t, "rangers-apprentice", "Ranger's Apprentice",
			"the-ruins-of-gorlan@1", "the-battle-for-skandia@4"),
		"series/ra/the-ranger-chronicles.json": seriesJSON(t, "the-ranger-chronicles", "The Ranger Chronicles", "the-ruins-of-gorlan@1"),
		"series/ki/kingsbridge.json":           seriesJSON(t, "kingsbridge", "Kingsbridge", "world-without-end@2"),
	}
	for id, title := range map[string]string{
		"the-ruins-of-gorlan":      "The Ruins of Gorlan",
		"the-battle-for-skandia":   "The Battle for Skandia",
		"oakleaf-bearers":          "Oakleaf Bearers",
		"the-pillars-of-the-earth": "The Pillars of the Earth",
		"world-without-end":        "World Without End",
	} {
		files["works/xx/"+id+"/work.json"] = workJSON(t, id, title)
		files["works/xx/"+id+"/recordings/r.json"] = recJSON(t, "r", id)
	}
	return files
}

func assertion(op, target, series, field, to string, others ...string) reviewedDecision {
	return reviewedDecision{Op: op, Target: target, Series: series, Field: field, To: to, Others: others,
		Decision: "assert", Reason: "the publisher lists one under the other's title"}
}

var (
	assertOakleaf  = assertion(OpMergeWorks, "the-battle-for-skandia", "", "", "", "oakleaf-bearers")
	assertChronic  = assertion(OpMergeSeries, "rangers-apprentice", "", "", "", "the-ranger-chronicles")
	assertKingsbr  = assertion(OpAddSeriesMember, "the-pillars-of-the-earth", "kingsbridge", "series", "1")
	assertedRecord = map[string]string{OpMergeWorks: ClassWorkDup, OpMergeSeries: ClassSeriesDup, OpAddSeriesMember: ClassWorkNoSeries}
)

func TestReviewedAssertSourcesAProposal(t *testing.T) {
	for _, tc := range []struct {
		r   reviewedDecision
		key string
	}{
		{assertOakleaf, "asserted/the-battle-for-skandia"},
		{assertChronic, "asserted/rangers-apprentice"},
		{assertKingsbr, "asserted/the-pillars-of-the-earth@kingsbridge"},
	} {
		t.Run(tc.r.Op, func(t *testing.T) {
			before := runFixture(t, rangerTree(t))
			class := assertedRecord[tc.r.Op]
			rep := runFixtureRejecting(t, rangerTree(t), tc.r)
			got := subclassOf(t, rep, class, subclassAsserted)
			if len(got) != 1 || len(classOf(t, rep, class)) != len(classOf(t, before, class))+1 {
				t.Fatalf("%s = %+v, want the one asserted finding beside the detector's", class, classOf(t, rep, class))
			}
			fd := got[0]
			if fd.Key != tc.key || fd.Propose.Advisory || keyOf(fd.Propose) != keyOf(tc.r.proposal()) ||
				fd.Propose.Reason != "asserted by review: "+tc.r.Reason || fd.Action == "" {
				t.Fatalf("finding = %+v", fd)
			}
			if len(fd.Works)+len(fd.Series) == 0 {
				t.Errorf("finding cites no record: %+v", fd)
			}
			if o := rep.Reviewed.Outcomes(); len(o) != 1 || o[0].Status != "asserted" {
				t.Fatalf("tally = %+v", rep.Reviewed)
			}
			if md := summary(rep); !strings.Contains(md, "... asserted (sourced a mechanical proposal) | 1") {
				t.Errorf("SUMMARY.md lacks the asserted count:\n%s", md)
			}
			assertProposalsConsistent(t, rep)
		})
	}
}

// An assertion a detector already makes (an omitted membership field read as "series")
// is an acceptance of it: an advisory proposal is
// made mechanical, nothing is added, and SUMMARY.md says to rewrite it as an accept.
func TestReviewedAssertRedundantWithADetectorActsAsAccept(t *testing.T) {
	advisory := Proposal{Op: OpMergeWorks, Target: "a", Others: []string{"b"}, Advisory: true, Reason: "a veto"}
	mechanical := Proposal{Op: OpAddSeriesMember, Target: "c", Series: "s", Field: "series", To: "2"}
	rep := proposalReport(advisory, mechanical)
	asserts := []reviewedDecision{
		assertion(OpAddSeriesMember, "c", "s", "", "2"), // field omitted: read as the detector's "series"
		assertion(OpMergeWorks, "a", "", "", "", "b"),
	}
	rep.Reviewed = applyReviewed(rep, asserts, nil, nil)
	rows := rep.classes[0].rows
	if len(rows) != 2 || rows[0].Propose.Advisory || rows[1].Propose.Advisory {
		t.Fatalf("rows = %+v, want both proposals mechanical and nothing added", rows)
	}
	for _, o := range rep.Reviewed.Outcomes() {
		if o.Status != "redundant" || !strings.Contains(o.Why, "already proposed as "+ClassLangMix) {
			t.Fatalf("outcomes = %+v", rep.Reviewed.Outcomes())
		}
	}
	md := summary(rep)
	for _, want := range []string{"... asserted, already proposed (redundant) | 2", "- redundant `", "rewrite it as an accept"} {
		if !strings.Contains(md, want) {
			t.Errorf("SUMMARY.md lacks %q:\n%s", want, md)
		}
	}
	assertProposalsConsistent(t, rep)
}

// Once applied, an assertion's records are retired onto its target (or its membership
// is listed), so it reads STALE and proposes nothing: that is what makes a re-run
// idempotent. A record that never existed is STALE too, never fatal.
func TestReviewedAssertStaleWhenARecordIsRetired(t *testing.T) {
	files := rangerTree(t)
	delete(files, "works/xx/oakleaf-bearers/work.json")
	delete(files, "works/xx/oakleaf-bearers/recordings/r.json")
	files["series/ki/kingsbridge.json"] = seriesJSON(t, "kingsbridge", "Kingsbridge", "the-pillars-of-the-earth@1", "world-without-end@2")
	missing := assertion(OpMergeWorks, "the-ruins-of-gorlan", "", "", "", "no-such-book")
	rep := runFixtureRejectingWith(t, files, `{"people":{},"series":{},"works":{"oakleaf-bearers":"the-battle-for-skandia"}}`,
		assertOakleaf, assertKingsbr, missing)
	for _, class := range []string{ClassWorkDup, ClassWorkNoSeries} {
		if got := subclassOf(t, rep, class, subclassAsserted); len(got) != 0 {
			t.Fatalf("%s sourced %+v from a stale assertion", class, got)
		}
	}
	whys := map[string]string{}
	for _, o := range rep.Reviewed.Stale() {
		whys[o.Entry.Target] = o.Why
	}
	if len(rep.Reviewed.Outcomes()) != 0 || len(whys) != 3 ||
		!strings.Contains(whys["the-battle-for-skandia"], "applied: every record it folds") ||
		!strings.Contains(whys["the-pillars-of-the-earth"], "applied: series kingsbridge lists") ||
		!strings.Contains(whys["the-ruins-of-gorlan"], "no live work no-such-book") {
		t.Fatalf("tally = %+v", rep.Reviewed)
	}
	if md := summary(rep); !strings.Contains(md, "- STALE `") || !strings.Contains(md, ": assert: ") {
		t.Errorf("SUMMARY.md lacks the stale assertions:\n%s", md)
	}
}

// An assertion that would break the mechanical set's consistency stays out and is
// reported refused, naming the conflict, as a conflicting acceptance is.
func TestReviewedAssertRefusedOnConflict(t *testing.T) {
	files := rangerTree(t)
	files["works/xx/mageling/work.json"] = workJSON(t, "mageling", "Mageling")
	files["works/xx/mageling/recordings/r.json"] = recJSON(t, "r", "mageling")
	files["works/xx/mageling-unabridged/work.json"] = workJSON(t, "mageling-unabridged", "Mageling (Unabridged)")
	files["works/xx/mageling-unabridged/recordings/r.json"] = recJSON(t, "r", "mageling-unabridged")
	var detected *Finding
	for _, fd := range classOf(t, runFixture(t, files), ClassWorkDup) {
		if fd.Propose.Op == OpMergeWorks && !fd.Propose.Advisory {
			detected = &fd
		}
	}
	if detected == nil {
		t.Fatal("fixture must carry a mechanical W-DUP merge")
	}
	loser := detected.Propose.Others[0]
	slotTwice := assertion(OpAddSeriesMember, "oakleaf-bearers", "kingsbridge", "series", "1")
	rejectedTwin := assertion(OpMergeWorks, "world-without-end", "", "", "", "the-ruins-of-gorlan")
	rejection := rejectedTwin
	rejection.Decision = "reject"
	rep := runFixtureRejecting(t, files,
		assertKingsbr, slotTwice,
		assertion(OpMergeWorks, "the-pillars-of-the-earth", "", "", "", loser),
		rejectedTwin, rejection)
	for i, want := range []string{"", "both claim series slot kingsbridge@1", "which " + detected.Key + " already merges",
		"a reviewed rejection resolves to the same proposal"} {
		o := rep.Reviewed.Outcomes()[i]
		if want == "" && o.Status != "asserted" || want != "" && (o.Status != "refused" || !strings.Contains(o.Why, want)) {
			t.Fatalf("outcome %d = %+v, want %q", i, o, want)
		}
	}
	if got := subclassOf(t, rep, ClassWorkNoSeries, subclassAsserted); len(got) != 1 {
		t.Fatalf("asserted memberships = %+v, want only the first", got)
	}
	if got := subclassOf(t, rep, ClassWorkDup, subclassAsserted); len(got) != 0 {
		t.Fatalf("refused merges were sourced: %+v", got)
	}
	if md := summary(rep); !strings.Contains(md, "- refused `") || !strings.Contains(md, "already merges") {
		t.Errorf("SUMMARY.md lacks the refusals:\n%s", md)
	}
	assertProposalsConsistent(t, rep)
}

func TestReviewedAssertRejectsAnUnsupportedOp(t *testing.T) {
	for _, tc := range []struct{ name, raw, want string }{
		{"unsupported op", `[{"decision":"assert","field":"title","from":"A","op":"retitle-work","reason":"why","target":"a","to":"B"}]`, "cannot be asserted"},
		{"review", `[{"decision":"assert","op":"review","reason":"why","target":"a"}]`, "cannot be asserted"},
		{"merge onto itself", `[{"decision":"assert","op":"merge-works","others":["a"],"reason":"why","target":"a"}]`, "onto itself"},
		{"merge with no others", `[{"decision":"assert","op":"merge-series","reason":"why","target":"a"}]`, "a target and others"},
		{"merge naming a series", `[{"decision":"assert","op":"merge-works","others":["b"],"reason":"why","series":"s","target":"a"}]`, "a target and others"},
		{"membership with another field", `[{"decision":"assert","field":"position","op":"add-series-member","reason":"why","series":"s","target":"a","to":"1"}]`, `field, if stated, "series"`},
		{"membership without position", `[{"decision":"assert","field":"series","op":"add-series-member","reason":"why","series":"s","target":"a"}]`, "not a canonical series position"},
		{"one proposal twice", `[{"decision":"assert","op":"add-series-member","reason":"why","series":"s","target":"a","to":"1"},` +
			`{"decision":"assert","field":"series","op":"add-series-member","reason":"why","series":"s","target":"a","to":"1"}]`, "the same proposal as entry 0"},
		{"non-canonical position", `[{"decision":"assert","field":"series","op":"add-series-member","reason":"why","series":"s","target":"a","to":"1 - 3"}]`, "not a canonical series position"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := canonical.Format([]byte(tc.raw))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := parseReviewed(raw); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one naming %q", err, tc.want)
			}
		})
	}
	raw, err := canonical.Format([]byte(`[{"decision":"assert","field":"series","op":"add-series-member","reason":"why","series":"s","target":"a","to":"1-3"},` +
		`{"decision":"assert","op":"merge-works","others":["b","c"],"reason":"why","target":"a"}]`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseReviewed(raw); err != nil {
		t.Fatalf("refused well-formed assertions: %v", err)
	}
	// Every op an assertion may source is one a reviewed acceptance could promote, and
	// the repair applies (TestAcceptableOpsAreAppliable).
	for op := range assertClass {
		if !AcceptableOp(op) {
			t.Errorf("%s may be asserted but not accepted", op)
		}
	}
}

// Two assertions resolving to one proposal through the tombstone table source it once.
func TestReviewedAssertsConvergingSourceOnce(t *testing.T) {
	old := assertOakleaf
	old.Others = []string{"oakleaf-bearers-old"}
	files := rangerTree(t)
	rep := runFixtureRejectingWith(t, files, `{"people":{},"series":{},"works":{"oakleaf-bearers-old":"oakleaf-bearers"}}`, assertOakleaf, old)
	if got := subclassOf(t, rep, ClassWorkDup, subclassAsserted); len(got) != 1 {
		t.Fatalf("sourced %+v, want one", got)
	}
	if o := rep.Reviewed.Outcomes(); len(o) != 2 || o[0].Status != "asserted" || o[1].Status != "redundant" {
		t.Fatalf("outcomes = %+v", o)
	}
}

// A merge assertion an earlier wave applied in part names the rest of its cluster:
// a detector proposing exactly that rest makes it redundant rather than a second,
// conflicting merge of the same records.
func TestReviewedAssertPartlyAppliedMeetsTheDetector(t *testing.T) {
	detector := Proposal{Op: OpMergeWorks, Target: "a", Others: []string{"c"}, Advisory: true, Reason: "a veto"}
	rep := proposalReport(detector)
	partly := assertion(OpMergeWorks, "a", "", "", "", "b", "c")
	rep.Reviewed = applyReviewed(rep, []reviewedDecision{partly}, model.Redirects{model.RedirectWorks: {"b": "a"}}, nil)
	if rows := rep.classes[0].rows; len(rows) != 1 || rows[0].Propose.Advisory {
		t.Fatalf("rows = %+v, want the detector's proposal made mechanical and nothing added", rows)
	}
	if o := rep.Reviewed.Outcomes(); len(o) != 1 || o[0].Status != statusRedundant {
		t.Fatalf("outcomes = %+v", rep.Reviewed.All)
	}
}

// A rejection that keeps an assertion out is EFFECTIVE, never stale: a reviewer pruning
// stale entries would otherwise delete it, and the assertion would then apply.
func TestReviewedRejectionWithholdingAnAssertionIsNotStale(t *testing.T) {
	rejection := assertOakleaf
	rejection.Decision = "reject"
	rep := runFixtureRejecting(t, rangerTree(t), assertOakleaf, rejection)
	if got := subclassOf(t, rep, ClassWorkDup, subclassAsserted); len(got) != 0 {
		t.Fatalf("a rejected assertion was sourced: %+v", got)
	}
	all := rep.Reviewed.All
	if len(rep.Reviewed.Stale()) != 0 || len(all) != 2 || all[0].Status != statusRefused || all[1].Status != statusWithholds {
		t.Fatalf("tally = %+v", rep.Reviewed)
	}
	md := summary(rep)
	for _, want := range []string{"... rejections withholding an assertion | 1", "- rejected-an-assertion `", "... matching no proposal (STALE) | 0"} {
		if !strings.Contains(md, want) {
			t.Errorf("SUMMARY.md lacks %q:\n%s", want, md)
		}
	}
}

// A membership assertion into a slot the series already holds is refused here, naming
// the holder, rather than sourced for metarepair to refuse on every run.
func TestReviewedAssertRefusedOnAHeldSlot(t *testing.T) {
	held := assertion(OpAddSeriesMember, "the-pillars-of-the-earth", "kingsbridge", "series", "2")
	rep := runFixtureRejecting(t, rangerTree(t), held)
	if got := subclassOf(t, rep, ClassWorkNoSeries, subclassAsserted); len(got) != 0 {
		t.Fatalf("sourced %+v into a held slot", got)
	}
	o := rep.Reviewed.All
	if len(o) != 1 || o[0].Status != statusRefused || !strings.Contains(o[0].Why, `position "2" of series kingsbridge is held by world-without-end`) {
		t.Fatalf("tally = %+v", rep.Reviewed)
	}
}
