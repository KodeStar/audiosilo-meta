package importer

import (
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/check"
)

// unnamedCreditRows is the fixture the exclusion's acceptance tests read: three
// rows whose credits name someone this catalogue cannot identify, and one
// control whose Latin-with-diacritics credits slug perfectly well.
//
// The names are the shapes the seed wave's 6,633 empty-slug warnings were made
// of - Korean, Cyrillic, CJK - not invented ones.
func unnamedCreditRows() []string {
	return []string{
		// An author written entirely in Hangul: Slugify keeps nothing.
		`{"asin":"B0UNNAMED1","title":"Korean Author","region":"us","language":"english","lengthMinutes":300,` +
			`"authors":[{"name":"김영하"}],"narrators":[{"name":"Bea Reader"}]}`,
		// The NARRATOR side counts too: a row is one book, and a production
		// credited to nobody is not a fact worth recording.
		`{"asin":"B0UNNAMED2","title":"Cyrillic Narrator","region":"us","language":"english","lengthMinutes":300,` +
			`"authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":"Александр Пушкин"}]}`,
		// A MIXED list: one perfectly good author beside one that identifies
		// nobody. The row is refused whole - importing it would record a book
		// whose author list is missing a stated author.
		`{"asin":"B0UNNAMED3","title":"Mixed Credits","region":"us","language":"english","lengthMinutes":300,` +
			`"authors":[{"name":"Ada Mapmaker"},{"name":"村上春樹"}],"narrators":[{"name":"Bea Reader"}]}`,
		// The negative control: every credit is Latin, heavy with diacritics and
		// punctuation that Slugify folds rather than drops.
		`{"asin":"B0UNNAMED4","title":"Latin Control","region":"us","language":"english","lengthMinutes":300,` +
			`"authors":[{"name":"Ólafur Jóhann Ólafsson"}],"narrators":[{"name":"Åsne Seierstad"}]}`,
	}
}

func unnamedCreditFixture() string {
	return strings.Join(unnamedCreditRows(), "\n") + "\n"
}

// TestLibexRefusesUnidentifiableCredits is the rule's acceptance test: a libex
// row crediting a name that resolves to no person of its own is refused by the
// PARSE layer, so the shared catch-all "person" record never becomes the author
// or narrator of a book it had nothing to do with - while the control row, whose
// credits differ only by being written in a script Slugify can fold, imports
// normally.
func TestLibexRefusesUnidentifiableCredits(t *testing.T) {
	sum, dataDir := runLibex(t, unnamedCreditFixture(), false)

	if sum.SkippedRows != 3 {
		t.Errorf("SkippedRows = %d, want 3 (the rows with an unidentifiable credit)", sum.SkippedRows)
	}
	if sum.NewWorks != 1 || sum.NewRecordings != 1 || sum.NewPeople != 2 {
		t.Errorf("summary = %d works / %d recordings / %d people, want 1/1/2 (the control row only)",
			sum.NewWorks, sum.NewRecordings, sum.NewPeople)
	}
	// The control imported, under the slugs its folded names produce.
	for _, rel := range []string{
		"works/la/latin-control/work.json",
		"people/ol/olafur-johann-olafsson.json",
		"people/as/asne-seierstad.json",
	} {
		if !entryExists(t, dataDir, rel) {
			t.Errorf("the control row did not import %s", rel)
		}
	}
	// Nothing from a refused row reached the tree - above all not the catch-all.
	for _, rel := range []string{
		"people/pe/person.json",
		"works/ko/korean-author/work.json",
		"works/cy/cyrillic-narrator/work.json",
		"works/mi/mixed-credits/work.json",
	} {
		if entryExists(t, dataDir, rel) {
			t.Errorf("a refused row created %s", rel)
		}
	}
	if res := check.Load(dataDir); !res.OK() {
		t.Fatalf("imported tree failed validation:\n%v", res.Problems)
	}
}

// TestLibexUnnamedCreditWarningLines pins how the refusal reads in both
// reporting shapes, exactly as the AI-narration rule is pinned: a line per row
// for a curated create-mode tranche, ONE aggregated line for a run over a large
// export - and the aggregate names the offending NAMES, because the fix for this
// class is upstream in Slugify rather than in any one row.
func TestLibexUnnamedCreditWarningLines(t *testing.T) {
	parsed, err := parseLibex([]byte(unnamedCreditFixture()))
	if err != nil {
		t.Fatal(err)
	}

	perRow := parsed.warningLines(false)
	if len(perRow) != 3 {
		t.Fatalf("create-mode lines = %v, want one per refused row", perRow)
	}
	joined := strings.Join(perRow, "\n")
	for _, want := range []string{
		`libex: B0UNNAMED1: credit "김영하" does not identify a person; row skipped`,
		`libex: B0UNNAMED2: credit "Александр Пушкин" does not identify a person; row skipped`,
		`libex: B0UNNAMED3: credit "村上春樹" does not identify a person; row skipped`,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("per-row lines missing %q:\n%s", want, joined)
		}
	}

	aggregate := parsed.warningLines(true)
	if len(aggregate) != 1 {
		t.Fatalf("aggregate lines = %v, want exactly one", aggregate)
	}
	const want = "libex: 3 rows skipped: a credited name does not identify a person"
	if !strings.HasPrefix(aggregate[0], want) {
		t.Errorf("aggregate line = %q, want it to start with %q", aggregate[0], want)
	}
	for _, name := range []string{"B0UNNAMED1", "김영하"} {
		if !strings.Contains(aggregate[0], name) {
			t.Errorf("aggregate line names neither the row nor the credit (%q): %q", name, aggregate[0])
		}
	}
}

// TestEnrichRefusesUnidentifiableCredits proves the refusal is the parse layer's
// and therefore holds in every libex mode: an enrichment row that MATCHES a
// catalogued recording by ASIN still never reaches the planner, so it fills no
// facts and stamps no provenance.
func TestEnrichRefusesUnidentifiableCredits(t *testing.T) {
	dataDir := seedEnrichTree(t, nil)
	row := `[{"asin":"B0LIBEX001","title":"The Lost Cartographer","region":"gb","language":"english",` +
		`"publisher":"Lost Press","lengthMinutes":600,` +
		`"authors":[{"name":"Ada Mapmaker"},{"name":"김영하"}],"narrators":[{"name":"Bea Reader"}]}]`
	sum := runEnrich(t, dataDir, row, false)

	if sum.SkippedRows != 1 {
		t.Errorf("SkippedRows = %d, want 1", sum.SkippedRows)
	}
	if sum.Matched != 0 || sum.EnrichedRecordings != 0 || sum.EnrichedWorks != 0 {
		t.Errorf("a refused row was planned: matched=%d works=%d recordings=%d",
			sum.Matched, sum.EnrichedWorks, sum.EnrichedRecordings)
	}
	var rec struct {
		Publisher string `json:"publisher"`
	}
	readEntity(t, dataDir, recRel, &rec)
	if rec.Publisher != "" {
		t.Errorf("publisher = %q, want none: the row that stated it was refused", rec.Publisher)
	}
}

// TestFirstUnnamedCredit is the rule's passing/violating table. The REFUSALS are
// names in scripts a slug cannot represent; the ADMISSIONS are the ones a lazier
// rule (anything that looked at the raw bytes rather than at the slug the import
// would derive) would wrongly refuse.
func TestFirstUnnamedCredit(t *testing.T) {
	unidentifiable := []string{
		"김영하",              // Korean
		"Александр Пушкин", // Cyrillic
		"村上春樹",             // Chinese/Japanese
		"夏目漱石",             // Japanese
		"Ομηρος",           // Greek
		"نجيب محفوظ",       // Arabic
		"，、。",              // punctuation only
		// The cleaning-dependent case, and the reason this rule judges the
		// CLEANED name: raw, it slugs to a non-empty (and wrong) "created-by";
		// once the prefix credit is stripped, the person it names is unslugifiable.
		"Created by 田中",
		// A qualifier cannot rescue an unidentifiable name either.
		"김영하 - Translator",
	}
	for _, name := range unidentifiable {
		if _, unnamed := firstUnnamedCredit([]string{name}, nil); !unnamed {
			t.Errorf("firstUnnamedCredit(%q) admitted a name that identifies nobody", name)
		}
		if _, unnamed := firstUnnamedCredit(nil, []string{name}); !unnamed {
			t.Errorf("firstUnnamedCredit(%q) admitted it on the narrator side", name)
		}
	}

	identifiable := []string{
		// Diacritics fold onto their base letters; these are real people.
		"Ólafur Jóhann Ólafsson", "Åsne Seierstad", "Ramón De Ocampo", "Émile Zola",
		"Stanisław Lem", "Sigrún Eðvaldsdóttir", "Motörhead",
		// Punctuation-heavy but Latin.
		"B.V. Larson", "Ai-jen Poo", "O'Brien",
		// A corporate credit is a person record in this model (see CLAUDE.md).
		"Audible Studios", "Full Cast",
		// A name that is only whitespace conflates nobody: sourceCredits drops it
		// outright, and an absent author is addBook's rule, for every source.
		"", "   ",
	}
	for _, name := range identifiable {
		if got, unnamed := firstUnnamedCredit([]string{name}, nil); unnamed {
			t.Errorf("firstUnnamedCredit refused %q (named %q); it slugs to an identity of its own", name, got)
		}
	}

	// ANY, not all - the same rule shape the AI-narration exclusion uses, and the
	// name it reports is the offending one in the source's own spelling.
	name, unnamed := firstUnnamedCredit([]string{"Ada Mapmaker", "김영하 - Translator"}, []string{"Bea Reader"})
	if !unnamed {
		t.Error("a mixed credit list was admitted; the rule is ANY, not ALL")
	}
	if name != "김영하 - Translator" {
		t.Errorf("named %q, want the offending credit in its source spelling", name)
	}
	if _, unnamed := firstUnnamedCredit(nil, nil); unnamed {
		t.Error("an empty credit list was refused")
	}
}

// TestSelectExcludesCreditRefusals is the selector half: the rows the importer
// would refuse are excluded from a tranche and COUNTED under their own reasons,
// so the selection report still partitions every row it read. Without this the
// report would promise completions the import cannot deliver - and, because a
// position claim is first-seen-wins, a refusable row would take the slot from
// the importable sibling behind it.
func TestSelectExcludesCreditRefusals(t *testing.T) {
	dataDir := seedSelectCatalogue(t)
	unnamedRow := `{"asin":"B0UNNAMED2","title":"Volume Two","region":"us","language":"english",` +
		`"authors":[{"name":"김영하"}],"narrators":[{"name":"Bea Reader"}],` +
		`"series":[{"name":"` + seriesName + `","position":"2"}]}`
	aiRow := `{"asin":"B0AIVOICE3","title":"Volume Three","region":"us","language":"english",` +
		`"authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":"Virtual Voice"}],` +
		`"series":[{"name":"` + seriesName + `","position":"3"}]}`
	// The row BEHIND the refused one at position 2: it must still be selectable,
	// which it only is because the refused row never claimed the slot.
	goodRow := selectRow("B0GOODROW2", "Volume Two", "gb", "english", seriesName, "2")

	res, lines := runSelect(t, dataDir, []string{unnamedRow, aiRow, goodRow}, 0)

	if res.Excluded[reasonUnnamedCredit] != 1 {
		t.Errorf("Excluded[%q] = %d, want 1", reasonUnnamedCredit, res.Excluded[reasonUnnamedCredit])
	}
	if res.Excluded[reasonAINarrator] != 1 {
		t.Errorf("Excluded[%q] = %d, want 1", reasonAINarrator, res.Excluded[reasonAINarrator])
	}
	if res.RowsSelected != 1 || len(lines) != 1 || !strings.Contains(lines[0], "B0GOODROW2") {
		t.Errorf("selected %d rows (%v), want only the importable one", res.RowsSelected, lines)
	}
	// Every row read is accounted for under exactly one rule.
	total := res.RowsSelected
	for _, n := range res.Excluded {
		total += n
	}
	if total != res.RowsRead {
		t.Errorf("selected + excluded = %d, want RowsRead = %d", total, res.RowsRead)
	}
}

// TestSelectReasonsAreAllReportable guards the report's completeness: an
// exclusion reason that is not in reasonOrder is counted and then never printed.
func TestSelectReasonsAreAllReportable(t *testing.T) {
	for _, reason := range []string{reasonAINarrator, reasonJunkCredit, reasonUnnamedCredit} {
		found := false
		for _, r := range reasonOrder {
			if r == reason {
				found = true
			}
		}
		if !found {
			t.Errorf("reason %q is not in reasonOrder; it would never be reported", reason)
		}
	}
}
