package importer

import (
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/check"
)

// junkCreditRows is the fixture the exclusion's acceptance tests read. The first
// row is the real one, lifted verbatim from the libex dump: an ACX developer
// mailbox credited as the narrator of a book, publisher "test company", 43
// minutes of runtime for a full-length novel. The second is the same account on
// the AUTHOR side (no such row is in the dump; it pins that both credit lists are
// judged, the way the unidentifiable-name rule is). The rest are the negative
// controls - real narrators whose names contain "test" or "dev" as an ordinary
// part of a person's name, which is why this rule is an exact-name list and not a
// token search.
func junkCreditRows() []string {
	return []string{
		`{"asin":"B0CTW69SVQ","title":"Psion Gamma (Psion series # 2)","region":"us","publisher":"test company",` +
			`"language":"english","bookFormat":"unabridged","lengthMinutes":43,` +
			`"authors":[{"name":"Jacob Gowans"}],"narrators":[{"name":"acx-dev+gamma4"}]}`,
		`{"asin":"B0JUNKAUT1","title":"Author Side","region":"us","language":"english","lengthMinutes":300,` +
			`"authors":[{"name":"acx-dev+gamma4"}],"narrators":[{"name":"Bea Reader"}]}`,
		`{"asin":"B0JUNKCTL1","title":"Sample Control","region":"us","language":"english","lengthMinutes":300,` +
			`"authors":[{"name":"Dev J. Haldar"}],"narrators":[{"name":"Tim Sample"}]}`,
	}
}

func junkCreditFixture() string {
	return strings.Join(junkCreditRows(), "\n") + "\n"
}

// TestLibexRefusesJunkCredits is the rule's acceptance test: a row crediting a
// platform account is refused by the PARSE layer, so "acx-dev-gamma4" never
// becomes a narrator of record - while the control row, whose credits merely
// CONTAIN "dev" and "sample" as parts of real people's names, imports normally.
func TestLibexRefusesJunkCredits(t *testing.T) {
	sum, dataDir := runLibex(t, junkCreditFixture(), false)

	if sum.SkippedRows != 2 {
		t.Errorf("SkippedRows = %d, want 2 (both rows crediting the account)", sum.SkippedRows)
	}
	if sum.NewWorks != 1 || sum.NewRecordings != 1 || sum.NewPeople != 2 {
		t.Errorf("summary = %d works / %d recordings / %d people, want 1/1/2 (the control row only)",
			sum.NewWorks, sum.NewRecordings, sum.NewPeople)
	}
	for _, rel := range []string{
		"works/sa/sample-control/work.json",
		"people/de/dev-j-haldar.json",
		"people/ti/tim-sample.json",
	} {
		if !entryExists(t, dataDir, rel) {
			t.Errorf("the control row did not import %s", rel)
		}
	}
	// Nothing from a refused row reached the tree - above all not the account
	// itself, and not the real author it was credited beside.
	for _, rel := range []string{
		"people/ac/acx-dev-gamma4.json",
		"people/ja/jacob-gowans.json",
		"works/ps/psion-gamma/work.json",
		"works/au/author-side/work.json",
	} {
		if entryExists(t, dataDir, rel) {
			t.Errorf("a refused row created %s", rel)
		}
	}
	if res := check.Load(dataDir); !res.OK() {
		t.Fatalf("imported tree failed validation:\n%v", res.Problems)
	}
}

// TestLibexJunkCreditWarningLines pins how the refusal reads in both reporting
// shapes, the way the AI-narration and unidentifiable-name rules are pinned. The
// aggregate names the ROW, not the credit: the vocabulary is a settled list, so
// the row is what an operator goes and looks at.
func TestLibexJunkCreditWarningLines(t *testing.T) {
	parsed, err := parseLibex([]byte(junkCreditFixture()))
	if err != nil {
		t.Fatal(err)
	}

	perRow := parsed.warningLines(false)
	if len(perRow) != 2 {
		t.Fatalf("create-mode lines = %v, want one per refused row", perRow)
	}
	joined := strings.Join(perRow, "\n")
	for _, want := range []string{
		`libex: B0CTW69SVQ: credit "acx-dev+gamma4" is a platform account, not a person; row skipped`,
		`libex: B0JUNKAUT1: credit "acx-dev+gamma4" is a platform account, not a person; row skipped`,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("per-row lines missing %q:\n%s", want, joined)
		}
	}

	aggregate := parsed.warningLines(true)
	if len(aggregate) != 1 {
		t.Fatalf("aggregate lines = %v, want exactly one", aggregate)
	}
	const want = "libex: 2 rows skipped: a credited name is a platform account"
	if !strings.HasPrefix(aggregate[0], want) {
		t.Errorf("aggregate line = %q, want it to start with %q", aggregate[0], want)
	}
	if !strings.Contains(aggregate[0], "B0CTW69SVQ") {
		t.Errorf("aggregate line names no example row: %q", aggregate[0])
	}
}

// TestEnrichRefusesJunkCredits proves the refusal is the parse layer's and
// therefore holds in every libex mode: a row that MATCHES a catalogued recording
// by ASIN still never reaches the planner.
func TestEnrichRefusesJunkCredits(t *testing.T) {
	dataDir := seedEnrichTree(t, nil)
	row := `[{"asin":"B0LIBEX001","title":"The Lost Cartographer","region":"gb","language":"english",` +
		`"publisher":"Lost Press","lengthMinutes":600,` +
		`"authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":"acx-dev+gamma4"}]}]`
	sum := runEnrich(t, dataDir, row, false)

	if sum.SkippedRows != 1 {
		t.Errorf("SkippedRows = %d, want 1", sum.SkippedRows)
	}
	if sum.Matched != 0 || sum.EnrichedRecordings != 0 || sum.EnrichedWorks != 0 {
		t.Errorf("a refused row was planned: matched=%d works=%d recordings=%d",
			sum.Matched, sum.EnrichedWorks, sum.EnrichedRecordings)
	}
}

// TestFirstJunkCredit is the rule's passing/violating table. The ADMISSIONS are
// the load-bearing half: every name there is a real credit in the dump that a
// token rule over "test"/"dev"/"qa"/"sample", or a rule over the email-plus-tag
// shape, would wrongly refuse.
func TestFirstJunkCredit(t *testing.T) {
	accounts := []string{
		"acx-dev+gamma4",
		"ACX-Dev+Gamma4", // the lookup is case-insensitive (foldCredit)
		"  acx-dev+gamma4  ",
	}
	for _, name := range accounts {
		if _, junk := firstJunkCredit([]string{name}, nil); !junk {
			t.Errorf("firstJunkCredit(%q) admitted a platform account on the author side", name)
		}
		if _, junk := firstJunkCredit(nil, []string{name}); !junk {
			t.Errorf("firstJunkCredit(%q) admitted it on the narrator side", name)
		}
	}

	people := []string{
		// Real credits a "dev"/"test"/"qa"/"sample" token rule would eat.
		"Dev J. Haldar", "Dev Joshi", "Arvind Dev", "Sahil Dev",
		"Tim Sample", "Kate Sample", "Catherine Fowler Sample",
		"Christopher Tester", "Erik Testerman",
		// A real publisher-as-person credit the email-plus-tag shape would eat.
		"I+Everything", "R+V", "B+R Publishing", "E+V Ediciones",
		// Placeholder-LOOKING names the rule deliberately does not claim: they
		// are not provably accounts, and refusing them is not this rule's call.
		"Test Narrator", "RocQET QA", "Tom Test",
		// Ordinary credits.
		"Stephen Fry", "Audible Studios", "", "   ",
	}
	for _, name := range people {
		if got, junk := firstJunkCredit([]string{name}, nil); junk {
			t.Errorf("firstJunkCredit refused %q (named %q); it is not in the vocabulary", name, got)
		}
	}

	// ANY, not all - and the name it reports is the offending one in the
	// source's own spelling.
	name, junk := firstJunkCredit([]string{"Jacob Gowans"}, []string{"acx-dev+gamma4"})
	if !junk {
		t.Error("a mixed credit list was admitted; the rule is ANY, not ALL")
	}
	if name != "acx-dev+gamma4" {
		t.Errorf("named %q, want the offending credit", name)
	}
	if _, junk := firstJunkCredit(nil, nil); junk {
		t.Error("an empty credit list was refused")
	}
}

// TestSelectExcludesJunkCredit is the selector half: a row the importer would
// refuse must not be selected into a tranche, or the report would promise a
// completion the import cannot deliver and the refused row would claim the
// series position an importable sibling could have filled.
func TestSelectExcludesJunkCredit(t *testing.T) {
	dataDir := seedSelectCatalogue(t)
	junkRow := `{"asin":"B0JUNKSEL2","title":"Volume Two","region":"us","language":"english",` +
		`"authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":"acx-dev+gamma4"}],` +
		`"series":[{"name":"` + seriesName + `","position":"2"}]}`
	goodRow := selectRow("B0GOODROW2", "Volume Two", "gb", "english", seriesName, "2")

	res, lines := runSelect(t, dataDir, []string{junkRow, goodRow}, 0)

	if res.Excluded[reasonJunkCredit] != 1 {
		t.Errorf("Excluded[%q] = %d, want 1", reasonJunkCredit, res.Excluded[reasonJunkCredit])
	}
	if res.RowsSelected != 1 || len(lines) != 1 || !strings.Contains(lines[0], "B0GOODROW2") {
		t.Errorf("selected %d rows (%v), want only the importable one", res.RowsSelected, lines)
	}
	total := res.RowsSelected
	for _, n := range res.Excluded {
		total += n
	}
	if total != res.RowsRead {
		t.Errorf("selected + excluded = %d, want RowsRead = %d", total, res.RowsRead)
	}
}

// TestJunkCreditVocabularyIsCanonical pins the table's shape, the way
// TestAINarratorVocabularyIsCanonical pins the AI tables: lookups go through
// foldCredit, so a key that is not already in that form can never match.
func TestJunkCreditVocabularyIsCanonical(t *testing.T) {
	for entry := range junkCreditNames {
		if entry == "" {
			t.Error("junkCreditNames contains an empty key")
			continue
		}
		if got := foldCredit(entry); got != entry {
			t.Errorf("junkCreditNames key %q is not canonical (foldCredit gives %q); it can never match", entry, got)
		}
	}
}
