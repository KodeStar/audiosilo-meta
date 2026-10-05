package audit

import (
	"fmt"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/testpack"
	"github.com/kodestar/audiosilo-meta/pkg/canonical"
	"github.com/kodestar/audiosilo-meta/pkg/check"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// reviewed_assert_test.go pins the third reviewed decision, assert: an entry that
// SOURCES a proposal no detector makes.

// rangerTree holds books no detector relates: an alternate title beside the series
// member it is, a reissue's original title, a novel stating no series, and an omnibus
// listed at a range beside the volumes it collects (and alone in a series of its own).
func rangerTree(t testing.TB) map[string]string {
	t.Helper()
	files := map[string]string{
		"people/ja/jane-doe.json":      personJSON(t, "jane-doe", "Jane Doe"),
		"people/na/nate-narrator.json": personJSON(t, "nate-narrator", "Nate Narrator"),
		"series/ra/rangers-apprentice.json": seriesJSON(t, "rangers-apprentice", "Ranger's Apprentice",
			"the-ruins-of-gorlan@1", "the-battle-for-skandia@4"),
		"series/ra/the-ranger-chronicles.json": seriesJSON(t, "the-ranger-chronicles", "The Ranger Chronicles", "the-ruins-of-gorlan@1"),
		"series/ki/kingsbridge.json":           seriesJSON(t, "kingsbridge", "Kingsbridge", "world-without-end@2"),
		"series/na/narnia.json": seriesJSON(t, "narnia", "The Chronicles of Narnia",
			"the-magicians-nephew@1", "prince-caspian@4", "the-complete-chronicles@1-7"),
		"series/na/narnia-collection.json": seriesJSON(t, "narnia-collection", "Narnia Collection", "the-complete-chronicles@1"),
	}
	for id, title := range map[string]string{
		"the-ruins-of-gorlan":      "The Ruins of Gorlan",
		"the-battle-for-skandia":   "The Battle for Skandia",
		"oakleaf-bearers":          "Oakleaf Bearers",
		"the-pillars-of-the-earth": "The Pillars of the Earth",
		"world-without-end":        "World Without End",
		"the-magicians-nephew":     "The Magician's Nephew",
		"prince-caspian":           "Prince Caspian",
		"the-complete-chronicles":  "The Complete Chronicles",
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

// dropAssertion is an asserted drop-membership: target listed in series at from.
func dropAssertion(target, series, field, from string) reviewedDecision {
	return reviewedDecision{Op: OpDropMembership, Target: target, Series: series, Field: field, From: from,
		Decision: "assert", Reason: "an omnibus is not a volume of the series"}
}

var (
	assertOakleaf = assertion(OpMergeWorks, "the-battle-for-skandia", "", "", "", "oakleaf-bearers")
	assertChronic = assertion(OpMergeSeries, "rangers-apprentice", "", "", "", "the-ranger-chronicles")
	assertKingsbr = assertion(OpAddSeriesMember, "the-pillars-of-the-earth", "kingsbridge", "series", "1")
	// The field is omitted: an asserted drop is read with the "position" L-MIX states.
	assertOmnibus  = dropAssertion("the-complete-chronicles", "narnia", "", "1-7")
	assertedRecord = map[string]string{OpMergeWorks: ClassWorkDup, OpMergeSeries: ClassSeriesDup, OpAddSeriesMember: ClassWorkNoSeries,
		OpDropMembership: ClassSeriesInteg}
)

func TestReviewedAssertSourcesAProposal(t *testing.T) {
	for _, tc := range []struct {
		r   reviewedDecision
		key string
	}{
		{assertOakleaf, "asserted/the-battle-for-skandia"},
		{assertChronic, "asserted/rangers-apprentice"},
		{assertKingsbr, "asserted/the-pillars-of-the-earth@kingsbridge"},
		{assertOmnibus, "asserted/the-complete-chronicles@narnia"},
	} {
		t.Run(tc.r.Op, func(t *testing.T) {
			// The baseline carries no decisions: the committed reviewed.json is real-tree
			// policy and may name these very slugs.
			before := runFixtureRejecting(t, rangerTree(t))
			class := assertedRecord[tc.r.Op]
			rep := runFixtureRejecting(t, rangerTree(t), tc.r)
			got := subclassOf(t, rep, class, SubclassAsserted)
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
			if tc.r.Op == OpDropMembership && (fd.Propose.Field != "position" || fd.Propose.From != "1-7" || len(fd.Propose.Others) != 0) {
				t.Errorf("asserted drop = %+v, want field position, from 1-7 and no home", fd.Propose)
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
	// No detector emits a homeless drop today; one that did would be met the same way.
	drop := Proposal{Op: OpDropMembership, Target: "d", Series: "s", Field: "position", From: "1-3", Advisory: true, Reason: "a veto"}
	rep := proposalReport(advisory, mechanical, drop)
	asserts := []reviewedDecision{
		assertion(OpAddSeriesMember, "c", "s", "", "2"), // field omitted: read as the detector's "series"
		assertion(OpMergeWorks, "a", "", "", "", "b"),
		dropAssertion("d", "s", "", "1-3"), // field omitted: read as "position"
	}
	rep.Reviewed = applyReviewed(rep, asserts, nil, nil)
	rows := rep.classes[0].rows
	if len(rows) != 3 || rows[0].Propose.Advisory || rows[1].Propose.Advisory || rows[2].Propose.Advisory {
		t.Fatalf("rows = %+v, want every proposal mechanical and nothing added", rows)
	}
	for _, o := range rep.Reviewed.Outcomes() {
		if o.Status != "redundant" || !strings.Contains(o.Why, "already proposed as "+ClassLangMix) {
			t.Fatalf("outcomes = %+v", rep.Reviewed.Outcomes())
		}
	}
	md := summary(rep)
	for _, want := range []string{"... asserted, already proposed (redundant) | 3", "- redundant `", "rewrite it as an accept"} {
		if !strings.Contains(md, want) {
			t.Errorf("SUMMARY.md lacks %q:\n%s", want, md)
		}
	}
	assertProposalsConsistent(t, rep)
}

// Once applied, an assertion's records are retired onto its target (or its membership
// is listed), so it reads STALE and proposes nothing: that is what makes a re-run
// idempotent. A slug naming no record, live or retired, is a typo and is REFUSED, so it
// cannot read as applied.
func TestReviewedAssertStaleWhenARecordIsRetired(t *testing.T) {
	files := rangerTree(t)
	delete(files, "works/xx/oakleaf-bearers/work.json")
	delete(files, "works/xx/oakleaf-bearers/recordings/r.json")
	files["series/ki/kingsbridge.json"] = seriesJSON(t, "kingsbridge", "Kingsbridge", "the-pillars-of-the-earth@1", "world-without-end@2")
	missing := assertion(OpMergeWorks, "the-ruins-of-gorlan", "", "", "", "no-such-book")
	rep := runFixtureRejectingWith(t, files, `{"people":{},"series":{},"works":{"oakleaf-bearers":"the-battle-for-skandia"}}`,
		assertOakleaf, assertKingsbr, missing)
	for _, class := range []string{ClassWorkDup, ClassWorkNoSeries} {
		if got := subclassOf(t, rep, class, SubclassAsserted); len(got) != 0 {
			t.Fatalf("%s sourced %+v from a stale assertion", class, got)
		}
	}
	whys := map[string]string{}
	for _, o := range rep.Reviewed.Stale() {
		whys[o.Entry.Target] = o.Why
	}
	out := rep.Reviewed.Outcomes()
	if len(out) != 1 || out[0].Status != statusRefused || !strings.Contains(out[0].Why, "no such work no-such-book") || len(whys) != 2 ||
		!strings.Contains(whys["the-battle-for-skandia"], "applied: every record it folds") ||
		!strings.Contains(whys["the-pillars-of-the-earth"], "applied: series kingsbridge lists") {
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
	for i, want := range []string{"", "both claim series slot kingsbridge@1", "is told to fold onto both " + detected.Propose.Target,
		"a reviewed rejection resolves to the same proposal"} {
		o := rep.Reviewed.Outcomes()[i]
		if want == "" && o.Status != "asserted" || want != "" && (o.Status != "refused" || !strings.Contains(o.Why, want)) {
			t.Fatalf("outcome %d = %+v, want %q", i, o, want)
		}
	}
	if got := subclassOf(t, rep, ClassWorkNoSeries, SubclassAsserted); len(got) != 1 {
		t.Fatalf("asserted memberships = %+v, want only the first", got)
	}
	if got := subclassOf(t, rep, ClassWorkDup, SubclassAsserted); len(got) != 0 {
		t.Fatalf("refused merges were sourced: %+v", got)
	}
	if md := summary(rep); !strings.Contains(md, "- refused `") || !strings.Contains(md, "fold onto both") {
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
		{"duplicate others", `[{"decision":"assert","op":"merge-works","others":["b","b"],"reason":"why","target":"a"}]`, "others must be sorted, unique"},
		{"non-canonical position", `[{"decision":"assert","field":"series","op":"add-series-member","reason":"why","series":"s","target":"a","to":"1 - 3"}]`, "not a canonical series position"},
		{"drop with a home", `[{"decision":"assert","from":"1","op":"drop-membership","others":["h"],"reason":"why","series":"s","target":"a"}]`, "names target, series and from"},
		{"drop with a destination", `[{"decision":"assert","from":"1","op":"drop-membership","reason":"why","series":"s","target":"a","to":"2"}]`, "names target, series and from"},
		{"drop with another field", `[{"decision":"assert","field":"work","from":"1","op":"drop-membership","reason":"why","series":"s","target":"a"}]`, `field, if stated, "position"`},
		{"drop naming no series", `[{"decision":"assert","from":"1","op":"drop-membership","reason":"why","target":"a"}]`, "names target, series and from"},
		{"drop without position", `[{"decision":"assert","op":"drop-membership","reason":"why","series":"s","target":"a"}]`, "not a canonical series position"},
		{"drop at a non-canonical position", `[{"decision":"assert","from":"1 - 7","op":"drop-membership","reason":"why","series":"s","target":"a"}]`, "not a canonical series position"},
		{"one drop twice", `[{"decision":"assert","from":"1","op":"drop-membership","reason":"why","series":"s","target":"a"},` +
			`{"decision":"assert","field":"position","from":"1","op":"drop-membership","reason":"why","series":"s","target":"a"}]`, "the same proposal as entry 0"},
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
		`{"decision":"assert","from":"1-7","op":"drop-membership","reason":"why","series":"s","target":"a"},` +
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

// Two assertions resolving to one proposal through the tombstone table (the target
// renamed since one was written) source it once.
func TestReviewedAssertsConvergingSourceOnce(t *testing.T) {
	old := assertOakleaf
	old.Target = "battle-for-skandia-old"
	files := rangerTree(t)
	rep := runFixtureRejectingWith(t, files, `{"people":{},"series":{},"works":{"battle-for-skandia-old":"the-battle-for-skandia"}}`, old, assertOakleaf)
	if got := subclassOf(t, rep, ClassWorkDup, SubclassAsserted); len(got) != 1 {
		t.Fatalf("sourced %+v, want one", got)
	}
	if o := rep.Reviewed.Outcomes(); len(o) != 2 || o[0].Status != "asserted" || o[1].Status != "redundant" {
		t.Fatalf("outcomes = %+v", o)
	}
}

// A loser retired onto ANOTHER survivor is refused naming it: folding that survivor onto
// the target is not what was reviewed. Only a loser resolving to the target is applied.
func TestReviewedAssertRefusedWhenALoserMergedElsewhere(t *testing.T) {
	widened := assertion(OpMergeWorks, "the-battle-for-skandia", "", "", "", "oakleaf-bearers-old")
	rep := runFixtureRejectingWith(t, rangerTree(t), `{"people":{},"series":{},"works":{"oakleaf-bearers-old":"oakleaf-bearers"}}`, widened)
	if got := subclassOf(t, rep, ClassWorkDup, SubclassAsserted); len(got) != 0 {
		t.Fatalf("widened the merge: %+v", got)
	}
	o := rep.Reviewed.All
	if len(o) != 1 || o[0].Status != statusRefused || !strings.Contains(o[0].Why, "oakleaf-bearers-old -> oakleaf-bearers") {
		t.Fatalf("tally = %+v", rep.Reviewed)
	}
}

// A membership assertion whose work the series already lists at ANOTHER position is
// refused naming it: only the same slot is "applied".
func TestReviewedAssertRefusedWhenListedElsewhereInTheSeries(t *testing.T) {
	files := rangerTree(t)
	files["series/ki/kingsbridge.json"] = seriesJSON(t, "kingsbridge", "Kingsbridge", "the-pillars-of-the-earth@3", "world-without-end@2")
	rep := runFixtureRejecting(t, files, assertKingsbr)
	o := rep.Reviewed.All
	if len(o) != 1 || o[0].Status != statusRefused || !strings.Contains(o[0].Why, `lists the-pillars-of-the-earth at position "3", not "1"`) {
		t.Fatalf("tally = %+v", rep.Reviewed)
	}
}

// One decision meeting several findings reports by precedence, not finding order: a
// promotion beats a no-op, and a refusal beats both.
func TestReviewedStatusPrecedence(t *testing.T) {
	p := Proposal{Op: OpRetitle, Target: "book", Field: "title", From: "Old", To: "New"}
	q := p
	q.Advisory = true
	for _, rows := range [][]Proposal{{p, q}, {q, p}} {
		rep := proposalReport(rows...)
		if tally := applyReviewed(rep, []reviewedDecision{review(p, "accept")}, nil, nil); tally.All[0].Status != statusAccepted {
			t.Fatalf("rows %+v: tally = %+v", rows, tally)
		}
	}
	add := Proposal{Op: OpAddSeriesMember, Target: "w", Series: "s", Field: "series", To: "1", Advisory: true}
	// The second copy claims the slot the first was just promoted into.
	if tally := applyReviewed(proposalReport(add, add), []reviewedDecision{review(add, "accept")}, nil, nil); tally.All[0].Status != statusRefused {
		t.Fatalf("tally = %+v", tally)
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
	if got := subclassOf(t, rep, ClassWorkDup, SubclassAsserted); len(got) != 0 {
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
	if got := subclassOf(t, rep, ClassWorkNoSeries, SubclassAsserted); len(got) != 0 {
		t.Fatalf("sourced %+v into a held slot", got)
	}
	o := rep.Reviewed.All
	if len(o) != 1 || o[0].Status != statusRefused || !strings.Contains(o[0].Why, `position "2" of series kingsbridge is held by world-without-end`) {
		t.Fatalf("tally = %+v", rep.Reviewed)
	}
}

// A survivor may absorb several merges with disjoint losers; a loser belongs in one.
func TestMergesOntoOneTargetAreConsistentButALoserFoldsOnce(t *testing.T) {
	for _, op := range []string{OpMergeWorks, OpMergeSeries} {
		books := Proposal{Op: op, Target: "dh-pub", Others: []string{"dh-books"}}
		novels := Proposal{Op: op, Target: "dh-pub", Others: []string{"dh-novels"}}
		if c := proposalConflicts(proposalReport(books, novels)).conflicts; len(c) != 0 {
			t.Fatalf("%s: two merges onto one target conflict: %v", op, c)
		}
		// The loser check reads only OTHER proposals' losers: one merge folding several
		// losers is not its own conflict.
		several := Proposal{Op: op, Target: "t", Others: []string{"l1", "l2", "l3"}}
		if c := proposalConflicts(proposalReport(several)).conflicts; len(c) != 0 {
			t.Fatalf("%s: one merge conflicts with itself: %v", op, c)
		}
		again := Proposal{Op: op, Target: "dh-pub", Others: []string{"dh-books", "dh-extra"}, Advisory: true}
		rep := proposalReport(books, novels, again)
		tally := applyReviewed(rep, []reviewedDecision{review(again, "accept")}, nil, nil)
		if o := tally.All[0]; o.Status != statusRefused || !strings.Contains(o.Why, "merges dh-books, which proposal-0 already merges") {
			t.Fatalf("%s: re-folding a loser: %+v", op, tally)
		}
		assertProposalsConsistent(t, rep)
	}
}

// A class sourcing had to create is withdrawn again if its only row is refused, and kept
// when the row stands; the classes a run starts with are never touched.
func TestSourcedClassIsWithdrawnWhenEmpty(t *testing.T) {
	data := t.TempDir()
	testpack.Seed(t, data, rangerTree(t))
	ix := newIndex(check.Load(data).Catalog)
	other := Proposal{Op: OpMergeWorks, Target: "the-ruins-of-gorlan", Others: []string{"oakleaf-bearers"}}
	for _, tc := range []struct {
		existing []Proposal
		classes  int
	}{{[]Proposal{other}, 1}, {nil, 2}} {
		rep := proposalReport(tc.existing...)
		applyReviewed(rep, []reviewedDecision{assertOakleaf}, nil, ix)
		if len(rep.classes) != tc.classes {
			t.Fatalf("with %v: classes = %d, want %d", tc.existing, len(rep.classes), tc.classes)
		}
		for _, c := range rep.classes {
			if len(c.rows) == 0 && c.class == ClassWorkDup {
				t.Fatalf("an empty sourced class was left behind")
			}
		}
	}
}

// A malformed asserted drop is a defect of the embedded policy, so the init-time parse
// panics rather than sourcing a drop nothing stands behind.
func TestReviewedAssertedDropMalformedPanics(t *testing.T) {
	raw, err := canonical.Format([]byte(`[{"decision":"assert","from":"1-7","op":"drop-membership","others":["home"],"reason":"why","series":"s","target":"a"}]`))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if r := recover(); r == nil || !strings.Contains(fmt.Sprint(r), "names target, series and from") {
			t.Fatalf("recovered %v, want a panic naming the drop's shape", r)
		}
	}()
	mustParseReviewed(raw)
}

// An asserted drop is looked up as an asserted membership is: a work the series no
// longer lists is the applied steady state (STALE), and a typo, a listing at another
// position or a drop of the series' last member is REFUSED naming it - nothing sourced.
func TestReviewedAssertDropLookups(t *testing.T) {
	for _, tc := range []struct {
		name   string
		r      reviewedDecision
		status outcomeStatus
		why    string
	}{
		{"not listed", dropAssertion("oakleaf-bearers", "narnia", "", "2"), statusStale, "applied: series narnia does not list oakleaf-bearers"},
		{"listed elsewhere", dropAssertion("the-complete-chronicles", "narnia", "", "1"), statusRefused,
			`series narnia lists the-complete-chronicles at position "1-7", not "1"`},
		{"the last member", dropAssertion("the-complete-chronicles", "narnia-collection", "position", "1"), statusRefused,
			"would leave series narnia-collection with no members: retiring a series is a merge-series"},
		{"no such series", dropAssertion("the-complete-chronicles", "narnia-typo", "", "1-7"), statusRefused, "no such series narnia-typo"},
		{"no such work", dropAssertion("the-complete-chronicle", "narnia", "", "1-7"), statusRefused, "no such work the-complete-chronicle"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rep := runFixtureRejecting(t, rangerTree(t), tc.r)
			if got := subclassOf(t, rep, ClassSeriesInteg, SubclassAsserted); len(got) != 0 {
				t.Fatalf("sourced %+v", got)
			}
			o := rep.Reviewed.All
			if len(o) != 1 || o[0].Status != tc.status || !strings.Contains(o[0].Why, tc.why) {
				t.Fatalf("tally = %+v, want %s naming %q", rep.Reviewed, tc.status, tc.why)
			}
		})
	}
}

// Both slugs of an asserted drop resolve through the tombstones, so an assertion
// written before a merge still sources the drop under the survivors - and one written
// after it meets that same finding, as a second assertion of one proposal does.
func TestReviewedAssertDropResolvesTombstones(t *testing.T) {
	old := dropAssertion("complete-chronicles-old", "narnia-old", "", "1-7")
	rep := runFixtureRejectingWith(t, rangerTree(t),
		`{"people":{},"series":{"narnia-old":"narnia"},"works":{"complete-chronicles-old":"the-complete-chronicles"}}`, old, assertOmnibus)
	got := subclassOf(t, rep, ClassSeriesInteg, SubclassAsserted)
	if len(got) != 1 || got[0].Propose.Target != "the-complete-chronicles" || got[0].Propose.Series != "narnia" || got[0].Propose.Advisory {
		t.Fatalf("sourced %+v, want one mechanical drop under the live slugs", got)
	}
	if o := rep.Reviewed.Outcomes(); len(o) != 2 || o[0].Status != statusAsserted || o[1].Status != statusRedundant ||
		!strings.Contains(o[1].Why, "another assertion already sources it") {
		t.Fatalf("outcomes = %+v", o)
	}
	assertProposalsConsistent(t, rep)
}

// A drop of a work a mechanical merge folds, or from a series one folds, would apply
// or go stale by run order: the later assertion is refused naming the conflict.
func TestReviewedAssertDropRefusedOnConflict(t *testing.T) {
	foldWork := assertion(OpMergeWorks, "the-magicians-nephew", "", "", "", "the-complete-chronicles")
	foldSeries := assertion(OpMergeSeries, "narnia-collection", "", "", "", "narnia")
	for _, tc := range []struct {
		first reviewedDecision
		why   string
	}{
		{foldWork, "moves the-complete-chronicles, which asserted/the-magicians-nephew merges"},
		{foldSeries, "changes series narnia, which asserted/narnia-collection merges"},
	} {
		t.Run(tc.first.Op, func(t *testing.T) {
			rep := runFixtureRejecting(t, rangerTree(t), tc.first, assertOmnibus)
			o := rep.Reviewed.All
			if len(o) != 2 || o[0].Status != statusAsserted || o[1].Status != statusRefused || !strings.Contains(o[1].Why, tc.why) {
				t.Fatalf("tally = %+v, want the drop refused naming %q", rep.Reviewed, tc.why)
			}
			if got := subclassOf(t, rep, ClassSeriesInteg, SubclassAsserted); len(got) != 0 {
				t.Fatalf("a refused drop was sourced: %+v", got)
			}
			assertProposalsConsistent(t, rep)
		})
	}
}
