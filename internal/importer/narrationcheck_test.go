package importer

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// narrationCatalog holds the merged Martian recording (two US ASINs, two
// narrations), a re-release listed twice (two US ASINs, one narrator), a recording
// with one ASIN per region (no suspect), and a full-cast production.
func narrationCatalog() *model.Catalog {
	return &model.Catalog{
		People: []*model.Person{{ID: "full-cast", Kind: "group"}},
		Works: []*model.Work{
			{ID: "the-martian", Recordings: []*model.Recording{{
				ID: "r-c-bray-2020", Narrators: []string{"r-c-bray"},
				Sources: []model.Source{{Type: "audiosilo-books-import"}},
				ASIN:    []model.ASIN{{ASIN: "BWHEATON1", Region: "us"}, {ASIN: "BBRAY0001", Region: "us"}, {ASIN: "BWHEATON2", Region: "uk"}},
			}}},
			{ID: "reissued", Recordings: []*model.Recording{{
				ID: "jane-doe-2010", Narrators: []string{"jane-doe"},
				Sources: []model.Source{{Type: "libex-import"}},
				ASIN:    []model.ASIN{{ASIN: "BJANE0001", Region: "us"}, {ASIN: "BJANE0002", Region: "us"}},
			}}},
			{ID: "plain", Recordings: []*model.Recording{{
				ID: "john-roe-2015", Narrators: []string{"john-roe"},
				ASIN: []model.ASIN{{ASIN: "BJOHN0001", Region: "us"}, {ASIN: "BJOHN0002", Region: "uk"}},
			}}},
			{ID: "dramatised", Recordings: []*model.Recording{{
				ID: "full-cast-2007", Narrators: []string{"full-cast"},
				ASIN: []model.ASIN{{ASIN: "BCAST0001", Region: "us"}, {ASIN: "BCAST0002", Region: "us"}},
			}}},
		},
	}
}

func narrationServer(t *testing.T) *libexFill {
	t.Helper()
	rec := func(asin string, minutes int, narrators ...string) string {
		var ns []string
		for _, n := range narrators {
			ns = append(ns, `{"name":"`+n+`"}`)
		}
		b, _ := json.Marshal(minutes)
		return `{"asin":"` + asin + `","title":"T","lengthMinutes":` + string(b) + `,"narrators":[` + strings.Join(ns, ",") + `]}`
	}
	return &libexFill{records: map[string]string{
		"BWHEATON1": rec("BWHEATON1", 659, "Wil Wheaton"),
		"BWHEATON2": rec("BWHEATON2", 659, "Wil Wheaton"),
		"BBRAY0001": rec("BBRAY0001", 653, "R. C. Bray"),
		"BJANE0001": rec("BJANE0001", 600, "Jane Doe"),
		"BJANE0002": rec("BJANE0002", 601, "Jane Doe", "Guest Reader"),
		"BCAST0001": rec("BCAST0001", 200, "Full Cast"),
		"BCAST0002": rec("BCAST0002", 200, "An Ensemble Cast"),
	}}
}

func TestSelectNarrationSuspectsNeedsTwoASINsInOneMarketplace(t *testing.T) {
	got := SelectNarrationSuspects(narrationCatalog(), nil)
	var ids []string
	for _, s := range got {
		ids = append(ids, s.Work)
	}
	if want := "dramatised,reissued,the-martian"; strings.Join(ids, ",") != want {
		t.Fatalf("suspects = %v, want %s (one ASIN per marketplace is no suspect)", ids, want)
	}
	if n := got[0].Narrators; len(n) != 0 {
		t.Errorf("a group credit must not be compared: narrators = %v", n)
	}
	// Naming a work checks every recording of it, suspect or not.
	if got := SelectNarrationSuspects(narrationCatalog(), map[string]bool{"plain": true}); len(got) != 1 || got[0].Work != "plain" {
		t.Errorf("--works selection = %+v", got)
	}
	ul := UserLibraryNarrations(SelectNarrationSuspects(narrationCatalog(), nil), narrationCatalog())
	if len(ul) != 1 || ul[0].Work != "the-martian" {
		t.Errorf("user-library suspects = %+v, want the Martian alone", ul)
	}
}

func TestCheckNarrationsReportsTwoProductionsOnly(t *testing.T) {
	f := narrationServer(t)
	c := fillClient(t, libexFillServer(t, f))
	var rows bytes.Buffer
	findings, rep, err := c.CheckNarrations(context.Background(), SelectNarrationSuspects(narrationCatalog(), nil), &rows)
	if err != nil {
		t.Fatal(err)
	}
	// The re-release (a co-narrator added) and the cast spellings are one
	// production each: only the Martian is a finding.
	if len(findings) != 1 || rep.Findings != 1 {
		t.Fatalf("findings = %+v", findings)
	}
	m := findings[0]
	if m.Kind != NarrationTwoProductions || m.Work != "the-martian" || len(m.Groups) != 2 {
		t.Fatalf("finding = %+v", m)
	}
	if g := m.Groups[0]; strings.Join(g.ASINs, ",") != "BWHEATON1,BWHEATON2" || g.Matches {
		t.Errorf("Wheaton group = %+v, want both Wheaton ASINs, not the recording's narrator", g)
	}
	if g := m.Groups[1]; strings.Join(g.ASINs, ",") != "BBRAY0001" || !g.Matches {
		t.Errorf("Bray group = %+v, want the Bray ASIN matching r-c-bray (an initials spelling)", g)
	}
	// The rows are the finding's ASINs only, in the export shape.
	if n := strings.Count(rows.String(), "\n"); n != 3 || !strings.Contains(rows.String(), `"asin":"BBRAY0001"`) {
		t.Errorf("rows = %q", rows.String())
	}
	if rep.Lookups != 7 {
		t.Errorf("lookups = %d, want 7", rep.Lookups)
	}
}

func TestCheckNarrationsReportsARecordingCreditingOthers(t *testing.T) {
	f := narrationServer(t)
	f.records["BJOHN0001"] = `{"asin":"BJOHN0001","narrators":[{"name":"Someone Else"}],"lengthMinutes":300}`
	c := fillClient(t, libexFillServer(t, f))
	findings, _, err := c.CheckNarrations(context.Background(), SelectNarrationSuspects(narrationCatalog(), map[string]bool{"plain": true}), nil)
	if err != nil {
		t.Fatal(err)
	}
	// BJOHN0002 is not in libex: unchecked, and it decides nothing.
	if len(findings) != 1 || findings[0].Kind != NarrationStoredDisagrees || strings.Join(findings[0].Unchecked, ",") != "BJOHN0002" {
		t.Fatalf("findings = %+v", findings)
	}
}

func TestCheckNarrationsAgreeingASINsAreNoFinding(t *testing.T) {
	f := narrationServer(t)
	f.records["BJOHN0001"] = `{"asin":"BJOHN0001","narrators":[{"name":"John Roe"}]}`
	f.records["BJOHN0002"] = `{"asin":"BJOHN0002","narrators":[{"name":"John Roe"}]}`
	c := fillClient(t, libexFillServer(t, f))
	findings, _, err := c.CheckNarrations(context.Background(), SelectNarrationSuspects(narrationCatalog(), map[string]bool{"plain": true}), nil)
	if err != nil || len(findings) != 0 {
		t.Fatalf("findings = %+v, err = %v", findings, err)
	}
}

// A later ASIN sharing a narrator with two earlier groups joins them into one
// production.
func TestJudgeNarrationBridgesGroups(t *testing.T) {
	s := NarrationSuspect{Work: "w", Recording: "r", Narrators: []string{"ann-lee"}}
	_, ok := judgeNarration(s, []lookedASIN{
		{asin: "A", narrators: []string{"Ann Lee"}},
		{asin: "B", narrators: []string{"Bob Ray"}},
		{asin: "C", narrators: []string{"Ann Lee", "Bob Ray"}},
	})
	if ok {
		t.Error("a bridging ASIN must fold the two groups into one production")
	}
}

// A middle initial stated or omitted, and two narrators credited in one name, are
// one production: no finding.
func TestJudgeNarrationFoldsSpellingsOfOneCredit(t *testing.T) {
	cases := []struct {
		name   string
		stored []string
		ls     []lookedASIN
	}{
		{"a middle initial", []string{"richard-m-davidson"}, []lookedASIN{
			{asin: "A", narrators: []string{"Richard Davidson"}},
			{asin: "B", narrators: []string{"Richard M Davidson"}},
		}},
		{"two names in one credit", []string{"kate-reading", "michael-kramer"}, []lookedASIN{
			{asin: "A", narrators: []string{"Kate Reading", "Michael Kramer"}},
			{asin: "B", narrators: splitCredit("Kate Reading and Michael Kramer")},
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if f, ok := judgeNarration(NarrationSuspect{Work: "w", Recording: "r", Narrators: c.stored}, c.ls); ok {
				t.Errorf("finding = %+v, want none", f)
			}
		})
	}
	// Different people stay apart, initials or not.
	if _, ok := judgeNarration(NarrationSuspect{Work: "w", Recording: "r", Narrators: []string{"ann-lee"}}, []lookedASIN{
		{asin: "A", narrators: []string{"Ann Lee"}},
		{asin: "B", narrators: []string{"Ann B Lean"}},
	}); !ok {
		t.Error("two different narrators must still be a finding")
	}
}

func TestSplitCreditSplitsOnlyTwoFullNames(t *testing.T) {
	for in, want := range map[string]string{
		"Kate Reading and Michael Kramer": "Kate Reading|Michael Kramer",
		"Kate Reading & Michael Kramer":   "Kate Reading|Michael Kramer",
		"Simon and Garfunkel":             "Simon and Garfunkel",
		"Wil Wheaton":                     "Wil Wheaton",
	} {
		if got := strings.Join(splitCredit(in), "|"); got != want {
			t.Errorf("splitCredit(%q) = %q, want %q", in, got, want)
		}
	}
}
