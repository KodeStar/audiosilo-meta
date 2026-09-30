package audit

import (
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
	data := filepath.Join(t.TempDir(), "data")
	testpack.Seed(t, data, files)
	res := check.Load(data)
	if res.Catalog == nil || len(res.Problems) > 0 {
		t.Fatalf("fixture does not validate: %v", res.Problems)
	}
	return analyzeWith(res, rejections)
}

func TestLinkRejectionsFileIsValid(t *testing.T) {
	rs, err := parseLinkRejections(linkRejectionsFile)
	if err != nil {
		t.Fatalf("%s: %v", linkRejectionsPath, err)
	}
	if len(rs) == 0 {
		t.Fatalf("%s holds no entries", linkRejectionsPath)
	}
	if len(linkRejections) != len(rs) {
		t.Errorf("the package holds %d rejections, the file %d", len(linkRejections), len(rs))
	}
}

func TestParseLinkRejectionsRefuses(t *testing.T) {
	entry := func(op, id, target, reason string) string {
		return `{"op":"` + op + `","id":"` + id + `","target":"` + target + `","reason":"` + reason + `"}`
	}
	good := entry("add-series-link", "b-german", "b", "why")
	for _, tc := range []struct {
		name, raw, want string
		raw2canon       bool // canonicalize raw first, so the case reaches the rule it is about
	}{
		{"not canonical", `[` + good + `]`, "canonical", false},
		{"unknown op", `[` + entry("merge-works", "b-german", "b", "why") + `]`, "not a link op", true},
		{"id not a slug", `[` + entry("add-work-link", "B German", "b", "why") + `]`, "is not a slug", true},
		{"target not a slug", `[` + entry("add-work-link", "b-german", "B!", "why") + `]`, "is not a slug", true},
		{"self link", `[` + entry("add-work-link", "b", "b", "why") + `]`, "both", true},
		{"empty reason", `[` + entry("add-work-link", "b-german", "b", "") + `]`, "empty or padded", true},
		{"padded reason", `[` + entry("add-work-link", "b-german", "b", " why") + `]`, "empty or padded", true},
		{"two-line reason", `[` + entry("add-work-link", "b-german", "b", `why\nnot`) + `]`, "more than one line", true},
		{"em dash", `[` + entry("add-work-link", "b-german", "b", "why \u2014 not") + `]`, "hyphens only", true},
		{"en dash", `[` + entry("add-work-link", "b-german", "b", "1\u20132") + `]`, "hyphens only", true},
		{"unsorted by op", `[` + good + `,` + entry("add-series-link", "a-german", "a", "why") + `]`, "sorted", true},
		{"unsorted across ops", `[` + good + `,` + entry("add-work-link", "a-german", "a", "why") + `]`, "", true},
		{"duplicate", `[` + good + `,` + good + `]`, "no duplicates", true},
		{"unknown field", `[{"op":"add-work-link","id":"b-german","target":"b","reason":"why","note":"x"}]`, "unknown field", true},
		{"duplicate key", "[\n  {\n    \"id\": \"b-german\",\n    \"id\": \"c-german\",\n    \"op\": \"add-work-link\",\n" +
			"    \"reason\": \"why\",\n    \"target\": \"b\"\n  }\n]\n", "duplicate", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := []byte(tc.raw)
			if tc.raw2canon {
				var err error
				if raw, err = canonical.Format(raw); err != nil {
					t.Fatal(err)
				}
			}
			_, err := parseLinkRejections(raw)
			if tc.want == "" {
				// add-series-link sorts before add-work-link, so this order is VALID.
				if err != nil {
					t.Fatalf("refused a correctly ordered list: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one naming %q", err, tc.want)
			}
		})
	}
}

func TestReviewedRejectionTurnsTheProposalAdvisory(t *testing.T) {
	rep := runFixtureRejecting(t, sagaTree(t), linkRejection{
		Op: OpAddSeriesLink, ID: "the-saga-german", Target: "the-saga", Reason: "the base is itself a translation",
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
	if got := rep.LinkRejections; got.Entries != 1 || got.Matched != 1 || len(got.Stale) != 0 {
		t.Errorf("tally = %+v, want 1 entry, 1 matched, none stale", got)
	}
	assertProposalsConsistent(t, rep)
}

func TestReviewedRejectionOfAWorkLink(t *testing.T) {
	rep := runFixtureRejecting(t, fateTree(t), linkRejection{
		Op: OpAddWorkLink, ID: "a-game-of-fate-fr", Target: "a-game-of-fate", Reason: "direction not established",
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
	stale := linkRejection{Op: OpAddSeriesLink, ID: "the-saga-german", Target: "an-older-saga", Reason: "why"}
	rep := runFixtureRejecting(t, sagaTree(t), stale,
		// The same key under the OTHER op names no series proposal either.
		linkRejection{Op: OpAddWorkLink, ID: "the-saga-german", Target: "the-saga", Reason: "why"},
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
		linkRejection{Op: OpAddSeriesLink, ID: "the-saga-german", Target: "the-saga", Reason: "reviewed"})
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
		linkRejection{Op: OpAddSeriesLink, ID: "the-saga-german", Target: "the-saga", Reason: "why"},
		linkRejection{Op: OpAddWorkLink, ID: "gone-german", Target: "gone", Reason: "why"},
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
