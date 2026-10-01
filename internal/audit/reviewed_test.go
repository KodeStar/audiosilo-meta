package audit

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/testpack"
	"github.com/kodestar/audiosilo-meta/pkg/canonical"
	"github.com/kodestar/audiosilo-meta/pkg/check"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// reviewed_test.go pins the shared reviewed-decision list: the embedded file's
// validity, what a matching entry does to a proposal, what a non-matching one does not,
// and the stale count SUMMARY.md reports.

// runFixtureRejecting is runFixture over a given rejection list instead of the
// embedded one.
func runFixtureRejecting(t testing.TB, files map[string]string, rejections ...reviewedDecision) *Report {
	t.Helper()
	return runFixtureRejectingWith(t, files, "", rejections...)
}

// runFixtureRejectingWith is runFixtureRejecting over a tree that also carries the
// given data/redirects.json ("" for none).
func runFixtureRejectingWith(t testing.TB, files map[string]string, redirects string, rejections ...reviewedDecision) *Report {
	t.Helper()
	data := filepath.Join(t.TempDir(), "data")
	testpack.Seed(t, data, files)
	if redirects != "" {
		if err := os.WriteFile(filepath.Join(data, "redirects.json"), []byte(redirects), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	res := check.Load(data)
	if res.Catalog == nil || len(res.Problems) > 0 {
		t.Fatalf("fixture does not validate: %v", res.Problems)
	}
	return analyzeWith(res, rejections)
}

func TestReviewedListIsNotEmpty(t *testing.T) {
	if len(reviewedDecisions) == 0 {
		t.Fatalf("%s holds no entries", reviewedPath)
	}
}

func TestParseReviewedRefuses(t *testing.T) {
	entry := func(op, target, to, reason string) string {
		return `{"decision":"reject","field":"translation_of","op":"` + op + `","target":"` + target + `","to":"` + to + `","reason":"` + reason + `"}`
	}
	good := entry("add-series-link", "b-german", "b", "why")
	const badReason = "one-line, trimmed reason in hyphens"
	for _, tc := range []struct {
		name, raw, want string
		literal         bool // parse raw as written; otherwise canonicalize it first, so it reaches the rule it is about
	}{
		{"not canonical", `[` + good + `]`, "re-render it canonically", true},
		{"duplicate key", "[\n  {\n    \"op\": \"add-work-link\",\n    \"reason\": \"why\",\n" +
			"    \"target\": \"b-german\",\n    \"target\": \"c-german\",\n    \"to\": \"b\"\n  }\n]\n", "canonical", true},
		{"unknown op", `[` + entry("unknown-op", "b-german", "b", "why") + `]`, "unknown op", false},
		{"target not a slug", `[` + entry("add-work-link", "B German", "b", "why") + `]`, "is not a slug", false},
		{"to not a slug", `[` + entry("add-work-link", "b-german", "B!", "why") + `]`, "is not a slug", false},
		{"self link", `[` + entry("add-work-link", "b", "b", "why") + `]`, "both", false},
		{"empty reason", `[` + entry("add-work-link", "b-german", "b", "") + `]`, badReason, false},
		{"padded reason", `[` + entry("add-work-link", "b-german", "b", " why") + `]`, badReason, false},
		{"two-line reason", `[` + entry("add-work-link", "b-german", "b", `why\nnot`) + `]`, badReason, false},
		{"em dash", `[` + entry("add-work-link", "b-german", "b", "why \u2014 not") + `]`, badReason, false},
		{"en dash", `[` + entry("add-work-link", "b-german", "b", "1\u20132") + `]`, badReason, false},
		{"unsorted", `[` + good + `,` + entry("add-series-link", "a-german", "a", "why") + `]`, "sorted", false},
		{"duplicate", `[` + good + `,` + good + `]`, "no duplicates", false},
		{"unknown field", `[{"decision":"reject","field":"translation_of","op":"add-work-link","target":"b-german","to":"b","reason":"why","note":"x"}]`, "unknown field", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := []byte(tc.raw)
			if !tc.literal {
				var err error
				if raw, err = canonical.Format(raw); err != nil {
					t.Fatal(err)
				}
			}
			_, err := parseReviewed(raw)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one naming %q", err, tc.want)
			}
		})
	}
}

// The list is sorted by op first: add-series-link sorts before add-work-link whatever
// the slugs, so this order is valid.
func TestParseReviewedSortsByOpFirst(t *testing.T) {
	raw, err := canonical.Format([]byte(`[` +
		`{"decision":"reject","field":"translation_of","op":"add-series-link","target":"b-german","to":"b","reason":"why"},` +
		`{"decision":"reject","field":"translation_of","op":"add-work-link","target":"a-german","to":"a","reason":"why"}]`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseReviewed(raw); err != nil {
		t.Fatalf("refused a correctly ordered list: %v", err)
	}
}

// A merge wave retiring either side of a rejected link must not bring the link back
// as mechanical under the survivor's slug: the entry is read through the tombstone
// table, in the family its op names.
func TestRejectionNamingRetiredSlugsStillSuppresses(t *testing.T) {
	rejection := reviewedDecision{Decision: "reject", Field: "translation_of",
		Op: OpAddSeriesLink, Target: "saga-german-old", To: "saga-old", Reason: "reviewed under the old slugs",
	}
	rep := runFixtureRejectingWith(t, sagaTree(t),
		`{"people":{},"series":{"saga-german-old":"the-saga-german","saga-old":"the-saga"},"works":{}}`,
		rejection)
	fd := linkFinding(t, rep, "the-saga-german")
	if fd == nil || !fd.Propose.Advisory || fd.Propose.To != "the-saga" ||
		!strings.Contains(fd.Propose.Reason, "reviewed under the old slugs") {
		t.Fatalf("proposal = %+v, want the survivors' link advisory with the review's reason", fd)
	}
	if got := rep.Reviewed; len(got.Outcomes) != 1 || len(got.Stale) != 0 {
		t.Errorf("tally = %+v, want the retired entry matched, none stale", got)
	}

	// The same slugs retired in ANOTHER family's namespace do not resolve it.
	rep = runFixtureRejectingWith(t, sagaTree(t),
		`{"people":{},"series":{},"works":{"saga-german-old":"dawn","saga-old":"dusk"}}`,
		rejection)
	if fd := linkFinding(t, rep, "the-saga-german"); fd == nil || fd.Propose.Advisory {
		t.Fatalf("proposal = %+v, want it mechanical: a works tombstone says nothing of a series", fd)
	}
	if got := rep.Reviewed; len(got.Outcomes) != 0 || len(got.Stale) != 1 {
		t.Errorf("tally = %+v, want the entry stale", got)
	}
}

func TestReviewedRejectionTurnsTheProposalAdvisory(t *testing.T) {
	rep := runFixtureRejecting(t, sagaTree(t), reviewedDecision{Decision: "reject", Field: "translation_of",
		Op: OpAddSeriesLink, Target: "the-saga-german", To: "the-saga", Reason: "the base is itself a translation",
	})
	fd := linkFinding(t, rep, "the-saga-german")
	if fd == nil {
		t.Fatal("the rejected proposal was dropped: it must stay in the report")
	}
	p := fd.Propose
	if !p.Advisory || p.To != "the-saga" ||
		!strings.HasPrefix(p.Reason, "reviewed and rejected: the base is itself a translation") {
		t.Errorf("proposal = %+v, want an advisory carrying the review's reason", p)
	}
	if !strings.HasPrefix(fd.Action, "do NOT apply mechanically") {
		t.Errorf("action = %q", fd.Action)
	}
	if got := rep.Reviewed; got.Entries() != 1 || len(got.Outcomes) != 1 || len(got.Stale) != 0 {
		t.Errorf("tally = %+v, want 1 entry, 1 matched, none stale", got)
	}
	assertProposalsConsistent(t, rep)
}

func TestReviewedRejectionOfAWorkLink(t *testing.T) {
	rep := runFixtureRejecting(t, fateTree(t), reviewedDecision{Decision: "reject", Field: "translation_of",
		Op: OpAddWorkLink, Target: "a-game-of-fate-fr", To: "a-game-of-fate", Reason: "direction not established",
	})
	fd := linkFinding(t, rep, "a-game-of-fate-fr")
	if fd == nil || !fd.Propose.Advisory || !strings.Contains(fd.Propose.Reason, "direction not established") {
		t.Fatalf("proposal = %+v, want an advisory carrying the review's reason", fd)
	}
	assertProposalsConsistent(t, rep)
}

// The rejection is of ONE link: the same record proposed onto a different original is
// judged afresh, and the entry that named the old one is stale.
func TestRejectionOfAnotherTargetLeavesTheProposalMechanical(t *testing.T) {
	stale := reviewedDecision{Decision: "reject", Field: "translation_of", Op: OpAddSeriesLink, Target: "the-saga-german", To: "an-older-saga", Reason: "why"}
	rep := runFixtureRejecting(t, sagaTree(t), stale,
		// The same key under the OTHER op names no series proposal either.
		reviewedDecision{Decision: "reject", Field: "translation_of", Op: OpAddWorkLink, Target: "the-saga-german", To: "the-saga", Reason: "why"},
	)
	fd := linkFinding(t, rep, "the-saga-german")
	if fd == nil || fd.Propose.Advisory || fd.Propose.To != "the-saga" {
		t.Fatalf("proposal = %+v, want the mechanical link onto the-saga", fd)
	}
	if got := rep.Reviewed; len(got.Outcomes) != 0 || len(got.Stale) != 2 || !reflect.DeepEqual(got.Stale[0], stale) {
		t.Errorf("tally = %+v, want both entries stale, in the list's order", got)
	}
	assertProposalsConsistent(t, rep)
}

// A proposal a veto already made advisory keeps the veto's reason behind the review's.
func TestRejectionOfAnAdvisoryProposalKeepsItsVeto(t *testing.T) {
	files := sagaTree(t)
	files["works/da/dawn/work.json"] = workJSON(t, "dawn", "Dawn", withLanguage("fr"))
	files["works/du/dusk/work.json"] = workJSON(t, "dusk", "Dusk", withLanguage("fr"))
	rep := runFixtureRejecting(t, files,
		reviewedDecision{Decision: "reject", Field: "translation_of", Op: OpAddSeriesLink, Target: "the-saga-german", To: "the-saga", Reason: "reviewed"})
	fd := linkFinding(t, rep, "the-saga-german")
	if fd == nil || !fd.Propose.Advisory ||
		!strings.Contains(fd.Propose.Reason, ": reviewed; a human should confirm: ") ||
		!strings.Contains(fd.Propose.Reason, "derives fr, not en") {
		t.Fatalf("proposal = %+v, want the review's reason followed by the veto's", fd)
	}
	if len(rep.Reviewed.Outcomes) != 1 {
		t.Errorf("tally = %+v, want the advisory proposal counted as matched", rep.Reviewed)
	}
}

func TestSummaryReportsReviewedRejections(t *testing.T) {
	rep := runFixtureRejecting(t, sagaTree(t),
		reviewedDecision{Decision: "reject", Field: "translation_of", Op: OpAddSeriesLink, Target: "the-saga-german", To: "the-saga", Reason: "why"},
		reviewedDecision{Decision: "reject", Field: "translation_of", Op: OpAddWorkLink, Target: "gone-german", To: "gone", Reason: "why"},
	)
	md := summary(rep)
	for _, want := range []string{
		"Reviewed decisions (`" + reviewedPath + "`)",
		"reviewed decisions on the list | 2",
		"... rejected (made advisory) | 1",
		"... matching no proposal (STALE) | 1",
		"- STALE `" + decisionIdentity(rep.Reviewed.Stale[0]) + "`: reject: why\n",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("SUMMARY.md lacks %q:\n%s", want, md)
		}
	}
	if strings.Contains(md, "- STALE `"+decisionIdentity(rep.Reviewed.Outcomes[0].Entry)+"`") {
		t.Error("SUMMARY.md lists a matched entry as stale")
	}
}

// encoding/json matches keys case-insensitively, so a case-variant key is a second,
// hidden value for one field; the round-trip refuses it.
func TestParseReviewedRefusesACaseVariantKey(t *testing.T) {
	raw, err := canonical.Format([]byte(`[{"decision":"reject","field":"translation_of","Target":"c-german","op":"add-work-link",` +
		`"reason":"why","target":"b-german","to":"b"}]`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseReviewed(raw); err == nil || !strings.Contains(err.Error(), "exactly the keys") {
		t.Fatalf("err = %v, want the case-variant key refused", err)
	}
}

// Two entries meeting on one key once a merge retires a side keep both reviews' reasons.
func TestRejectionsMeetingOnOneKeyKeepEveryReason(t *testing.T) {
	rep := runFixtureRejectingWith(t, sagaTree(t),
		`{"people":{},"series":{"saga-old":"the-saga"},"works":{}}`,
		reviewedDecision{Decision: "reject", Field: "translation_of", Op: OpAddSeriesLink, Target: "the-saga-german", To: "saga-old", Reason: "first review"},
		reviewedDecision{Decision: "reject", Field: "translation_of", Op: OpAddSeriesLink, Target: "the-saga-german", To: "the-saga", Reason: "second review"},
	)
	fd := linkFinding(t, rep, "the-saga-german")
	if fd == nil || !fd.Propose.Advisory || !strings.Contains(fd.Propose.Reason, "first review; second review") {
		t.Fatalf("proposal = %+v, want both reasons", fd)
	}
	if got := rep.Reviewed; len(got.Outcomes) != 2 || len(got.Stale) != 0 {
		t.Errorf("tally = %+v, want both matched", got)
	}
}

func TestReviewedCommittedCanonicalSorted(t *testing.T) {
	raw, err := os.ReadFile("reviewed.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, reviewedFile) {
		t.Fatal("embedded bytes differ from committed file")
	}
	if _, err := parseReviewed(raw); err != nil {
		t.Fatal(err)
	}
}

func review(p Proposal, decision string) reviewedDecision {
	return reviewedDecision{Op: p.Op, Target: p.Target, Series: p.Series, Field: p.Field, From: p.From, To: p.To,
		Others: p.Others, Decision: decision, Reason: "stated source reviewed"}
}

// The round-trip check re-encodes through encoding/json, which escapes &, < and > -
// canonical.Format decodes and re-renders without HTML escaping, so a reason naming
// "Fate & Flame" is not refused as a key mismatch.
func TestReviewedReasonKeepsHTMLCharacters(t *testing.T) {
	raw, err := canonical.Format([]byte(`[{"decision":"reject","op":"add-series-link","reason":"Fate & Flame <mixed> franchises","target":"a","to":"b"}]`))
	if err != nil {
		t.Fatal(err)
	}
	rs, err := parseReviewed(raw)
	if err != nil || len(rs) != 1 || rs[0].Reason != "Fate & Flame <mixed> franchises" {
		t.Fatalf("parse = %+v, %v", rs, err)
	}
}

func TestReviewedValidation(t *testing.T) {
	for _, tc := range []struct{ name, raw, want string }{
		{"bad decision", `[{"op":"retitle-work","decision":"maybe","reason":"why"}]`, "bad decision"},
		{"missing decision", `[{"op":"retitle-work","reason":"why"}]`, "bad decision"},
		{"null", `null`, "array"},
		{"case decision", `[{"op":"retitle-work","decision":"reject","Decision":"accept","reason":"why"}]`, "exactly the keys"},
		{"duplicate opposite decisions", `[{"op":"retitle-work","decision":"accept","reason":"why"},{"op":"retitle-work","decision":"reject","reason":"why"}]`, "no duplicates"},
		{"no record", `[{"op":"review","decision":"reject","reason":"why"}]`, "names no record"},
		{"accepted review", `[{"decision":"accept","op":"review","reason":"why","target":"a"}]`, "cannot be accepted"},
		{"accepted rename candidate", `[{"decision":"accept","op":"rename-candidate","reason":"why","target":"a"}]`, "cannot be accepted"},
		{"accepted sidecar re-point", `[{"decision":"accept","op":"repoint-sidecar","reason":"why","target":"a"}]`, "cannot be accepted"},
		{"others unsorted", `[{"op":"merge-works","target":"a","others":["c","b"],"decision":"accept","reason":"why"}]`, "others must be sorted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := canonical.Format([]byte(tc.raw))
			if err != nil {
				t.Fatal(err)
			}
			_, err = parseReviewed(raw)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %s", err, tc.want)
			}
		})
	}
	t.Run("init panic", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil || !strings.Contains(fmt.Sprint(r), reviewedPath) {
				t.Fatalf("panic = %v", r)
			}
		}()
		mustParseReviewed([]byte("bad"))
	})
}

func TestReviewedEveryIdentityField(t *testing.T) {
	p := Proposal{Op: OpMoveMembership, Target: "work", Series: "source", Field: "position", From: "1", To: "1", Others: []string{"dest"}}
	for _, field := range []string{"op", "target", "series", "field", "from", "to", "others"} {
		t.Run(field, func(t *testing.T) {
			r := review(p, "reject")
			switch field {
			case "op":
				r.Op = OpDropMembership
			case "target":
				r.Target = "another"
			case "series":
				r.Series = "another"
			case "field":
				r.Field = "another"
			case "from":
				r.From = "2"
			case "to":
				r.To = "2"
			case "others":
				r.Others = []string{"another"}
			}
			rep := proposalReport(p)
			tally := applyReviewed(rep, []reviewedDecision{r}, nil)
			if len(tally.Stale) != 1 || rep.classes[0].rows[0].Propose.Advisory {
				t.Fatalf("identity field %s was ignored", field)
			}
		})
	}
}

func proposalReport(ps ...Proposal) *Report {
	f := &findings{class: ClassLangMix}
	for i, p := range ps {
		f.add(Finding{Key: fmt.Sprintf("proposal-%d", i), Propose: p})
	}
	return &Report{classes: []*findings{f}}
}

func TestReviewedAcceptRejectAndNoOpsAcrossClasses(t *testing.T) {
	for _, class := range classOrder {
		for _, advisory := range []bool{false, true} {
			for _, decision := range []string{"accept", "reject"} {
				t.Run(fmt.Sprintf("%s/%v/%s", class, advisory, decision), func(t *testing.T) {
					p := Proposal{Op: OpRetitle, Target: "book", Field: "title", From: "Old", To: "New", Advisory: advisory, Reason: "source reason"}
					rep := proposalReport(p)
					rep.classes[0].class = class
					rep.Reviewed = applyReviewed(rep, []reviewedDecision{review(p, decision)}, nil)
					got := rep.classes[0].rows[0]
					if got.Propose.Advisory != (decision == "reject") {
						t.Fatalf("proposal = %+v", got.Propose)
					}
					status := map[string]string{"accept": "accepted", "reject": "rejected"}[decision]
					if advisory == (decision == "reject") {
						status = "no-op"
					}
					if rep.Reviewed.Outcomes[0].Status != status {
						t.Fatalf("tally = %+v", rep.Reviewed)
					}
					if !strings.Contains(strings.Join(got.Notes, ";"), "reviewed and "+map[string]string{"accept": "accepted", "reject": "rejected"}[decision]+": stated source reviewed") {
						t.Fatalf("notes = %v", got.Notes)
					}
					if status == "no-op" && !strings.Contains(summary(rep), "- no-op") {
						t.Fatal("no-op missing from summary")
					}
					assertProposalsConsistent(t, rep)
				})
			}
		}
	}
}

func TestReviewedRefusesConflictingAcceptances(t *testing.T) {
	move := Proposal{Op: OpMoveMembership, Target: "work", Series: "source", Field: "position", From: "1", To: "1", Others: []string{"dest"}}
	for _, tc := range []struct {
		name             string
		existing, accept Proposal
		why              string
	}{
		{"restated membership", Proposal{Op: OpRestatePosition, Target: "work", Series: "source", From: "1.0", To: "1"}, move, "restates"},
		{"language of moved work", move, Proposal{Op: OpSetWorkLanguage, Target: "work", Field: "language", From: "en", To: "de"}, "moves"},
		{"membership twice", move, Proposal{Op: OpDropMembership, Target: "work", Series: "source", Field: "position", From: "1"}, "both move"},
		{"slot twice", move, Proposal{Op: OpAddSeriesMember, Target: "other", Series: "dest", To: "1"}, "both claim"},
		{"work merged", move, Proposal{Op: OpMergeWorks, Target: "work", Others: []string{"other"}}, "merges"},
		{"series merged", move, Proposal{Op: OpMergeSeries, Target: "source", Others: []string{"other"}}, "merges"},
		{"destination merged", move, Proposal{Op: OpMergeSeries, Target: "dest", Others: []string{"other"}}, "merges"},
		{"drop home merged", Proposal{Op: OpDropMembership, Target: "work", Series: "source", Field: "position", From: "1", Others: []string{"home"}},
			Proposal{Op: OpMergeSeries, Target: "other", Others: []string{"home"}}, "as its work's home"},
		{"split membership", Proposal{Op: OpSplitSeries, Target: "source", Others: []string{"work"}}, move, "both move"},
		{"joins twice", move, Proposal{Op: OpMoveMembership, Target: "work", Series: "elsewhere", To: "2", Others: []string{"dest"}}, "both move"},
		{"language twice", Proposal{Op: OpSetWorkLanguage, Target: "work", To: "de"}, Proposal{Op: OpSetWorkLanguage, Target: "work", To: "fr"}, "both set"},
		{"language of merged work", Proposal{Op: OpSetWorkLanguage, Target: "work", To: "de"}, Proposal{Op: OpMergeWorks, Target: "other", Others: []string{"work"}}, "merges"},
		{"merge overlap", Proposal{Op: OpMergeWorks, Target: "a", Others: []string{"b"}}, Proposal{Op: OpMergeWorks, Target: "c", Others: []string{"b"}}, "fold onto both"},
		{"merge target loser", Proposal{Op: OpMergeWorks, Target: "a", Others: []string{"b"}}, Proposal{Op: OpMergeWorks, Target: "b", Others: []string{"c"}}, "merge target"},
		{"link chain", Proposal{Op: OpAddWorkLink, Target: "a", To: "b"}, Proposal{Op: OpAddWorkLink, Target: "b", To: "c"}, "translation in one"},
		{"link two originals", Proposal{Op: OpAddWorkLink, Target: "a", To: "b"}, Proposal{Op: OpAddWorkLink, Target: "a", To: "c"}, "linked to both"},
	} {
		for _, reverse := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/reverse=%v", tc.name, reverse), func(t *testing.T) {
				existing, accept := tc.existing, tc.accept
				if reverse {
					existing, accept = accept, existing
				}
				accept.Advisory = true
				rep := proposalReport(existing, accept)
				rep.Reviewed = applyReviewed(rep, []reviewedDecision{review(accept, "accept")}, nil)
				if !rep.classes[0].rows[1].Propose.Advisory || rep.Reviewed.Outcomes[0].Status != "refused" || !strings.Contains(rep.Reviewed.Outcomes[0].Why, tc.why) {
					t.Fatalf("outcome = %+v", rep.Reviewed)
				}
				if !strings.Contains(summary(rep), tc.why) {
					t.Fatal("refusal missing from summary")
				}
				assertProposalsConsistent(t, rep)
			})
		}
	}
	// Two accepted advisory moves compete too; the later decision is refused.
	other := move
	other.Series = "elsewhere"
	move.Advisory = true
	other.Advisory = true
	rep := proposalReport(move, other)
	tally := applyReviewed(rep, []reviewedDecision{review(move, "accept"), review(other, "accept")}, nil)
	if tally.Outcomes[0].Status != "accepted" || tally.Outcomes[1].Status != "refused" {
		t.Fatalf("tally=%+v", tally)
	}
	assertProposalsConsistent(t, rep)
}

func TestReviewedRejectsBeforeAccepting(t *testing.T) {
	p := Proposal{Op: OpMergeWorks, Target: "a", Others: []string{"b"}}
	q := Proposal{Op: OpMergeWorks, Target: "c", Others: []string{"b"}, Advisory: true}
	rep := proposalReport(p, q)
	tally := applyReviewed(rep, []reviewedDecision{review(q, "accept"), review(p, "reject")}, nil)
	if tally.Outcomes[0].Status != "accepted" {
		t.Fatalf("tally=%+v", tally)
	}
	assertProposalsConsistent(t, rep)
}

func TestReviewedMembershipTombstonesAndSharedDecisions(t *testing.T) {
	p := Proposal{Op: OpMoveMembership, Target: "work", Series: "source", Field: "position", From: "1", To: "1", Others: []string{"dest"}, Advisory: true}
	old := p
	old.Target = "old-work"
	old.Series = "old-source"
	old.Others = []string{"old-dest"}
	reds := model.NewRedirects()
	reds[model.RedirectWorks]["old-work"] = "work"
	reds[model.RedirectSeries]["old-source"] = "source"
	reds[model.RedirectSeries]["old-dest"] = "dest"
	for _, decision := range []string{"accept", "reject"} {
		rep := proposalReport(p)
		first, second := review(old, decision), review(p, decision)
		first.Reason = "first"
		second.Reason = "second"
		tally := applyReviewed(rep, []reviewedDecision{first, second}, reds)
		if len(tally.Outcomes) != 2 || !strings.Contains(rep.classes[0].rows[0].Propose.Reason, "first") || !strings.Contains(rep.classes[0].rows[0].Propose.Reason, "second") {
			t.Fatalf("tally=%+v, proposal=%+v", tally, rep.classes[0].rows[0])
		}
	}
	rep := proposalReport(p)
	tally := applyReviewed(rep, []reviewedDecision{review(old, "accept"), review(p, "reject")}, reds)
	if tally.Outcomes[0].Status != "refused" || !rep.classes[0].rows[0].Propose.Advisory {
		t.Fatalf("opposite decisions: %+v", tally)
	}
}

func TestReviewedRealDetectorAcceptsAndStaleSummary(t *testing.T) {
	files := mixSagaTree(t)
	files["works/xx/morgen/work.json"] = workJSON(t, "morgen", "Morgen", withLanguage("de"), testpack.WithAddedAt("2025-06-01"))
	source := onlyMix(t, runFixtureRejecting(t, files), lMixSplit)
	if !source.Propose.Advisory {
		t.Fatal("fixture must start advisory")
	}
	r := review(source.Propose, "accept")
	stale := r
	stale.To = "fr"
	rep := runFixtureRejecting(t, files, r, stale)
	if onlyMix(t, rep, lMixSplit).Propose.Advisory || len(rep.Reviewed.Stale) != 1 || !strings.Contains(summary(rep), "- STALE") {
		t.Fatalf("reviewed=%+v", rep.Reviewed)
	}
	assertProposalsConsistent(t, rep)
}

// Adding a Proposal field requires deciding whether it participates in identity.
func TestReviewedIdentityCoversProposalSchema(t *testing.T) {
	typ := reflect.TypeFor[Proposal]()
	var fields []string
	for f := range typ.Fields() {
		fields = append(fields, strings.Split(f.Tag.Get("json"), ",")[0])
	}
	slices.Sort(fields)
	want := []string{"advisory", "field", "from", "op", "others", "reason", "series", "target", "to"}
	if !slices.Equal(fields, want) {
		t.Fatalf("Proposal changed: %v; review the decision key", fields)
	}
	for op := range opPhrase {
		if op != OpNone && !knownReviewOp(op) {
			t.Errorf("reviewed decisions do not recognize %s", op)
		}
	}
}

func TestReviewedResolvesOnlySlugFields(t *testing.T) {
	reds := model.NewRedirects()
	for _, kind := range []model.RedirectKind{model.RedirectWorks, model.RedirectSeries, model.RedirectPeople} {
		reds[kind]["old"] = string(kind) + "-survivor"
	}
	for _, tc := range []struct {
		class                   string
		p                       Proposal
		target, other, from, to string
	}{
		{ClassSeriesInteg, Proposal{Op: OpReview, Target: "old", Field: "position", From: "old"}, "works-survivor", "", "old", ""},
		{ClassSeriesInteg, Proposal{Op: OpReview, Target: "old"}, "series-survivor", "", "", ""},
		{ClassPersonDup, Proposal{Op: OpReview, Others: []string{"old"}}, "", "people-survivor", "", ""},
		{ClassLangMix, Proposal{Op: OpReview, Target: "old", Series: "old", Others: []string{"old"}}, "works-survivor", "series-survivor", "", ""},
		{ClassLangMix, Proposal{Op: OpSplitSeries, Target: "old", Others: []string{"old"}, Field: "language", From: "old", To: "old"}, "series-survivor", "works-survivor", "old", "old"},
		{ClassWorkTitle, Proposal{Op: OpRetitle, Target: "old", From: "old", To: "old"}, "works-survivor", "", "old", "old"},
		{ClassHygiene, Proposal{Op: OpFillField, Target: "old", Field: "narrators"}, "old", "", "", ""},
	} {
		got := resolvedProposal(tc.p, tc.class, reds)
		if got.Target != tc.target || strings.Join(got.Others, ",") != tc.other || got.From != tc.from || got.To != tc.to {
			t.Errorf("resolved %+v = %+v", tc.p, got)
		}
		if tc.p.Series != "" && got.Series != "series-survivor" {
			t.Errorf("series=%s", got.Series)
		}
	}
}

func TestReviewedMergeNamespacesStaySeparate(t *testing.T) {
	p := Proposal{Op: OpMergeWorks, Target: "a", Others: []string{"b"}, Advisory: true}
	rep := proposalReport(Proposal{Op: OpMergeSeries, Target: "b", Others: []string{"a"}}, p)
	tally := applyReviewed(rep, []reviewedDecision{review(p, "accept")}, nil)
	if tally.Outcomes[0].Status != "accepted" {
		t.Fatalf("independent namespaces refused: %+v", tally)
	}
	assertProposalsConsistent(t, rep)
}

// A failed move has already touched membership, work, series and join indexes by
// the time its occupied slot is found. None of those claims may block a later review.
func TestReviewedRefusedPromotionLeavesNoClaims(t *testing.T) {
	occupied := Proposal{Op: OpAddSeriesMember, Target: "holder", Series: "dest", To: "1"}
	refused := Proposal{Op: OpMoveMembership, Target: "work", Series: "source", To: "1", Others: []string{"dest"}, Advisory: true}
	accepted := refused
	accepted.To = "2"
	stillRefused := Proposal{Op: OpAddSeriesMember, Target: "another", Series: "dest", To: "1", Advisory: true}
	rep := proposalReport(occupied, refused, accepted, stillRefused)
	tally := applyReviewed(rep, []reviewedDecision{review(refused, "accept"), review(accepted, "accept"), review(stillRefused, "accept")}, nil)
	for i, want := range []string{"refused", "accepted", "refused"} {
		if tally.Outcomes[i].Status != want {
			t.Fatalf("outcomes = %+v", tally.Outcomes)
		}
	}
	if !strings.Contains(tally.Outcomes[2].Why, "proposal-0") || strings.Contains(tally.Outcomes[2].Why, "proposal-1") {
		t.Fatalf("original slot claimant was not restored: %+v", tally.Outcomes[2])
	}
	assertProposalsConsistent(t, rep)
}

func TestReviewedIndexesReviewByClass(t *testing.T) {
	reds := model.NewRedirects()
	rep := &Report{}
	for _, tc := range []struct {
		class string
		kind  model.RedirectKind
	}{
		{ClassPersonDup, model.RedirectPeople},
		{ClassSeriesDup, model.RedirectSeries},
		{ClassWorkDup, model.RedirectWorks},
	} {
		live := string(tc.kind) + "-survivor"
		reds[tc.kind]["old"] = live
		f := &findings{class: tc.class}
		f.add(Finding{Key: live, Propose: Proposal{Op: OpReview, Target: live}})
		rep.classes = append(rep.classes, f)
	}
	tally := applyReviewed(rep, []reviewedDecision{review(Proposal{Op: OpReview, Target: "old"}, "reject")}, reds)
	if len(tally.Outcomes) != 1 || len(tally.Stale) != 0 {
		t.Fatalf("tally = %+v", tally)
	}
	for _, c := range rep.classes {
		if !c.rows[0].Propose.Advisory {
			t.Errorf("review did not resolve in %s", c.class)
		}
	}
}
