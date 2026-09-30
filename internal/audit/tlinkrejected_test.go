package audit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/testpack"
	"github.com/kodestar/audiosilo-meta/pkg/canonical"
	"github.com/kodestar/audiosilo-meta/pkg/check"
)

// tlinkrejected_test.go pins T-LINK's reviewed-rejection list: the embedded file's
// validity, what a matching entry does to a proposal, what a non-matching one does not,
// and the stale count SUMMARY.md reports.

// runFixtureRejecting is runFixture over a given rejection list instead of the
// embedded one.
func runFixtureRejecting(t testing.TB, files map[string]string, rejections ...linkRejection) *Report {
	t.Helper()
	return runFixtureRejectingWith(t, files, "", rejections...)
}

// runFixtureRejectingWith is runFixtureRejecting over a tree that also carries the
// given data/redirects.json ("" for none).
func runFixtureRejectingWith(t testing.TB, files map[string]string, redirects string, rejections ...linkRejection) *Report {
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

func TestLinkRejectionsListIsNotEmpty(t *testing.T) {
	if len(linkRejections) == 0 {
		t.Fatalf("%s holds no entries", linkRejectionsPath)
	}
}

func TestParseLinkRejectionsRefuses(t *testing.T) {
	entry := func(op, target, to, reason string) string {
		return `{"op":"` + op + `","target":"` + target + `","to":"` + to + `","reason":"` + reason + `"}`
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
		{"unknown op", `[` + entry("merge-works", "b-german", "b", "why") + `]`, "not a link op", false},
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
		{"unknown field", `[{"op":"add-work-link","target":"b-german","to":"b","reason":"why","note":"x"}]`, "unknown field", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := []byte(tc.raw)
			if !tc.literal {
				var err error
				if raw, err = canonical.Format(raw); err != nil {
					t.Fatal(err)
				}
			}
			_, err := parseLinkRejections(raw)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one naming %q", err, tc.want)
			}
		})
	}
}

// The list is sorted by op first: add-series-link sorts before add-work-link whatever
// the slugs, so this order is valid.
func TestParseLinkRejectionsSortsByOpFirst(t *testing.T) {
	raw, err := canonical.Format([]byte(`[` +
		`{"op":"add-series-link","target":"b-german","to":"b","reason":"why"},` +
		`{"op":"add-work-link","target":"a-german","to":"a","reason":"why"}]`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseLinkRejections(raw); err != nil {
		t.Fatalf("refused a correctly ordered list: %v", err)
	}
}

// A merge wave retiring either side of a rejected link must not bring the link back
// as mechanical under the survivor's slug: the entry is read through the tombstone
// table, in the family its op names.
func TestRejectionNamingRetiredSlugsStillSuppresses(t *testing.T) {
	rejection := linkRejection{
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
	if got := rep.LinkRejections; got.Matched != 1 || len(got.Stale) != 0 {
		t.Errorf("tally = %+v, want the retired entry matched, none stale", got)
	}

	// The same slugs retired in ANOTHER family's namespace do not resolve it.
	rep = runFixtureRejectingWith(t, sagaTree(t),
		`{"people":{},"series":{},"works":{"saga-german-old":"dawn","saga-old":"dusk"}}`,
		rejection)
	if fd := linkFinding(t, rep, "the-saga-german"); fd == nil || fd.Propose.Advisory {
		t.Fatalf("proposal = %+v, want it mechanical: a works tombstone says nothing of a series", fd)
	}
	if got := rep.LinkRejections; got.Matched != 0 || len(got.Stale) != 1 {
		t.Errorf("tally = %+v, want the entry stale", got)
	}
}

func TestReviewedRejectionTurnsTheProposalAdvisory(t *testing.T) {
	rep := runFixtureRejecting(t, sagaTree(t), linkRejection{
		Op: OpAddSeriesLink, Target: "the-saga-german", To: "the-saga", Reason: "the base is itself a translation",
	})
	fd := linkFinding(t, rep, "the-saga-german")
	if fd == nil {
		t.Fatal("the rejected proposal was dropped: it must stay in the report")
	}
	p := fd.Propose
	if !p.Advisory || p.To != "the-saga" ||
		!strings.HasPrefix(p.Reason, "reviewed and rejected ("+linkRejectionsPath+"): the base is itself a translation") {
		t.Errorf("proposal = %+v, want an advisory carrying the review's reason", p)
	}
	if !strings.HasPrefix(fd.Action, "do NOT apply mechanically") {
		t.Errorf("action = %q", fd.Action)
	}
	if got := rep.LinkRejections; got.Entries() != 1 || got.Matched != 1 || len(got.Stale) != 0 {
		t.Errorf("tally = %+v, want 1 entry, 1 matched, none stale", got)
	}
	assertProposalsConsistent(t, rep)
}

func TestReviewedRejectionOfAWorkLink(t *testing.T) {
	rep := runFixtureRejecting(t, fateTree(t), linkRejection{
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
	stale := linkRejection{Op: OpAddSeriesLink, Target: "the-saga-german", To: "an-older-saga", Reason: "why"}
	rep := runFixtureRejecting(t, sagaTree(t), stale,
		// The same key under the OTHER op names no series proposal either.
		linkRejection{Op: OpAddWorkLink, Target: "the-saga-german", To: "the-saga", Reason: "why"},
	)
	fd := linkFinding(t, rep, "the-saga-german")
	if fd == nil || fd.Propose.Advisory || fd.Propose.To != "the-saga" {
		t.Fatalf("proposal = %+v, want the mechanical link onto the-saga", fd)
	}
	if got := rep.LinkRejections; got.Matched != 0 || len(got.Stale) != 2 || got.Stale[0] != stale {
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
		linkRejection{Op: OpAddSeriesLink, Target: "the-saga-german", To: "the-saga", Reason: "reviewed"})
	fd := linkFinding(t, rep, "the-saga-german")
	if fd == nil || !fd.Propose.Advisory ||
		!strings.Contains(fd.Propose.Reason, "): reviewed; a human should confirm: ") ||
		!strings.Contains(fd.Propose.Reason, "derives fr, not en") {
		t.Fatalf("proposal = %+v, want the review's reason followed by the veto's", fd)
	}
	if rep.LinkRejections.Matched != 1 {
		t.Errorf("tally = %+v, want the advisory proposal counted as matched", rep.LinkRejections)
	}
}

func TestSummaryReportsReviewedRejections(t *testing.T) {
	rep := runFixtureRejecting(t, sagaTree(t),
		linkRejection{Op: OpAddSeriesLink, Target: "the-saga-german", To: "the-saga", Reason: "why"},
		linkRejection{Op: OpAddWorkLink, Target: "gone-german", To: "gone", Reason: "why"},
	)
	md := summary(rep)
	for _, want := range []string{
		"T-LINK reviewed rejections (`" + linkRejectionsPath + "`)",
		"reviewed rejections on the list | 2",
		"... matching a proposal (made advisory) | 1",
		"... matching no proposal (stale) | 1",
		"- `add-work-link` `gone-german` -> `gone`\n",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("SUMMARY.md lacks %q:\n%s", want, md)
		}
	}
	if strings.Contains(md, "`the-saga-german` -> `the-saga`") {
		t.Error("SUMMARY.md lists a matched entry as stale")
	}
}

// encoding/json matches keys case-insensitively, so a case-variant key is a second,
// hidden value for one field; the round-trip refuses it.
func TestParseLinkRejectionsRefusesACaseVariantKey(t *testing.T) {
	raw, err := canonical.Format([]byte(`[{"Target":"c-german","op":"add-work-link",` +
		`"reason":"why","target":"b-german","to":"b"}]`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseLinkRejections(raw); err == nil || !strings.Contains(err.Error(), "exactly the keys") {
		t.Fatalf("err = %v, want the case-variant key refused", err)
	}
}

// Two entries meeting on one key once a merge retires a side keep both reviews' reasons.
func TestRejectionsMeetingOnOneKeyKeepEveryReason(t *testing.T) {
	rep := runFixtureRejectingWith(t, sagaTree(t),
		`{"people":{},"series":{"saga-old":"the-saga"},"works":{}}`,
		linkRejection{Op: OpAddSeriesLink, Target: "the-saga-german", To: "saga-old", Reason: "first review"},
		linkRejection{Op: OpAddSeriesLink, Target: "the-saga-german", To: "the-saga", Reason: "second review"},
	)
	fd := linkFinding(t, rep, "the-saga-german")
	if fd == nil || !fd.Propose.Advisory || !strings.Contains(fd.Propose.Reason, "first review; second review") {
		t.Fatalf("proposal = %+v, want both reasons", fd)
	}
	if got := rep.LinkRejections; got.Matched != 2 || len(got.Stale) != 0 {
		t.Errorf("tally = %+v, want both matched", got)
	}
}
