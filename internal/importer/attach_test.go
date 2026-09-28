package importer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// The --refusals contract and the attach rule (attach.go). The worklist tests
// select against seedSelectCatalogue, the attach tests against
// seedAttachCatalogue: the same shape with a title a retailer decorates the way
// real ones are ("Volume One" is itself a volume marker, which the title rule
// reads as decoration and nothing else).

// seedAttachCatalogue is "Cartographer Chronicles" holding "The Lost Coast" by
// Ada Mapmaker at position "1.0" (a stored spelling of 1), whose one recording
// (Bea Reader) carries B0PRESENT1.
func seedAttachCatalogue(t *testing.T) string {
	t.Helper()
	dataDir := t.TempDir()
	seedTree(t, dataDir, map[string]string{
		"people/ad/ada-mapmaker.json": `{"id":"ada-mapmaker","license":"CC0-1.0","name":"Ada Mapmaker","sources":[{"type":"user"}]}`,
		"people/be/bea-reader.json":   `{"id":"bea-reader","license":"CC0-1.0","name":"Bea Reader","sources":[{"type":"user"}]}`,
		"works/th/the-lost-coast/work.json": `{"authors":["ada-mapmaker"],"id":"the-lost-coast","language":"en","license":"CC0-1.0",` +
			`"sources":[{"type":"user"}],"title":"The Lost Coast"}`,
		"works/th/the-lost-coast/recordings/bea-reader-2024.json": `{"asin":[{"asin":"B0PRESENT1","region":"us"}],"id":"bea-reader-2024",` +
			`"language":"en","license":"CC0-1.0","narrators":["bea-reader"],"sources":[{"type":"user"}],"work":"the-lost-coast"}`,
		"series/ca/cartographer-chronicles.json": `{"id":"cartographer-chronicles","license":"CC0-1.0","name":"Cartographer Chronicles",` +
			`"sources":[{"type":"user"}],"works":[{"position":"1.0","work":"the-lost-coast"}]}`,
	})
	return dataDir
}

// TestRefusalCodesAreStable pins the --refusals codes. They are a CONTRACT with
// audiosilo-meta-sync: renaming, reordering or dropping one breaks the bot, so a
// change here is a change made in both repositories on purpose.
func TestRefusalCodesAreStable(t *testing.T) {
	want := []string{
		"malformed-asin", "asin-in-catalogue", "duplicate-asin",
		"no-catalogue-series", "series-other-authors", "extra-series-uncatalogued",
		"position-unparseable", "unmapped-language", "unmapped-region",
		"ai-narrator", "credit-platform-account", "credit-list-of-people",
		"credit-cast-placeholder", "credit-not-a-person",
		"position-claimed", "over-series-cap",
	}
	if got := RefusalCodes(); !reflect.DeepEqual(got, want) {
		t.Errorf("RefusalCodes() = %q\nwant %q", got, want)
	}
}

// Every report reason has exactly one code, in the same order, and no two
// reasons share one: the worklist can name every refusal the report counts.
func TestEveryReasonHasARefusalCode(t *testing.T) {
	if len(refusalCodeOf) != len(reasonOrder) {
		t.Fatalf("refusalCodeOf has %d entries, reasonOrder %d", len(refusalCodeOf), len(reasonOrder))
	}
	codes := RefusalCodes()
	for i, reason := range reasonOrder {
		if got := refusalCodeOf[reason]; got != codes[i] {
			t.Errorf("reason %q -> %q, want %q (the report's position %d)", reason, got, codes[i], i)
		}
	}
}

// attachExportRow is a libex row for Cartographer Chronicles with its own author,
// narrator, title, region and language.
func attachExportRow(asin, title, region, language, author, narrator, position string) string {
	return `{"asin":"` + asin + `","title":"` + title + `","region":"` + region + `","language":"` + language +
		`","authors":[{"name":"` + author + `"}],"narrators":[{"name":"` + narrator + `"}],` +
		`"series":[{"name":"` + seriesName + `","position":"` + position + `"}]}`
}

// selectInto runs libex-select over rows with a --refusals worklist and returns
// the result, the subset path and the worklist's decoded lines.
func selectInto(t *testing.T, dataDir string, rows []string) (SelectResult, string, []refusalLine) {
	t.Helper()
	dir := t.TempDir()
	in := filepath.Join(dir, "export.ndjson")
	if err := os.WriteFile(in, []byte(strings.Join(rows, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, refusals := filepath.Join(dir, "subset.ndjson"), filepath.Join(dir, "refusals.ndjson")
	res, err := SelectLibex(in, out, SelectOptions{DataDir: dataDir, RefusalsPath: refusals})
	if err != nil {
		t.Fatalf("SelectLibex: %v", err)
	}
	raw, err := os.ReadFile(refusals)
	if err != nil {
		t.Fatalf("read refusals: %v", err)
	}
	var lines []refusalLine
	for _, l := range strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n") {
		if l == "" {
			continue
		}
		var r refusalLine
		dec := json.NewDecoder(strings.NewReader(l))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&r); err != nil {
			t.Fatalf("refusal line %q: %v", l, err)
		}
		lines = append(lines, r)
	}
	assertPartition(t, res)
	return res, out, lines
}

// TestRefusalsWorklist: one line per refused row, naming its ASIN and its code,
// with exactly the counts the report prints - and the line's key order is the
// contract's.
func TestRefusalsWorklist(t *testing.T) {
	dataDir := seedSelectCatalogue(t)
	rows := append(selectExportRows(),
		selectRow("not-an-asin", "Volume Eight", "us", "english", seriesName, "8"),
		selectRow("", "Volume Nine", "us", "english", seriesName, "9"), // no ASIN at all
		// A duplicate of a SELECTED ASIN: its line would name a row the subset
		// carries, so the worklist omits it.
		selectRow("B0SELECT02", "Volume Two", "gb", "english", seriesName, "2"),
		// A duplicate of a REFUSED one names nothing selected, so it is listed.
		selectRow("B0OTHER001", "Unrelated Book", "gb", "english", "Some Other Series", "1"),
	)
	res, subset, lines := selectInto(t, dataDir, rows)

	// Every refused row but the unnamed one and the duplicate of a selected ASIN.
	if len(lines) != res.RowsRead-res.RowsSelected-2 {
		t.Fatalf("%d refusal lines for %d refused rows (2 omitted)", len(lines), res.RowsRead-res.RowsSelected)
	}
	body, _ := os.ReadFile(subset)
	for _, l := range lines {
		if l.ASIN == "" {
			t.Errorf("a line with no ASIN: %+v", l)
		}
		if strings.Contains(string(body), `"asin":"`+l.ASIN+`"`) {
			t.Errorf("line %+v names an ASIN the subset carries", l)
		}
	}
	if res.UnnamedRefusals != 1 || !strings.Contains(res.Report(), "the --refusals worklist omits 1 refused row that state no ASIN") {
		t.Errorf("UnnamedRefusals = %d; report:\n%s", res.UnnamedRefusals, res.Report())
	}
	var got []string
	for _, l := range lines {
		if !slices.Contains(RefusalCodes(), l.Reason) {
			t.Errorf("line %+v carries a code outside the contract", l)
		}
		got = append(got, l.ASIN+" "+l.Reason)
	}
	// Stream order, the held duplicate-asin lines last.
	want := []string{
		"B0OTHER001 " + RefusalNoCatalogueSeries,
		"B0PRESENT1 " + RefusalASINInCatalogue,
		"B0LANG0003 " + RefusalUnmappedLanguage,
		"B0REGION04 " + RefusalUnmappedRegion,
		"not-an-asin " + RefusalMalformedASIN,
		"B0OTHER001 " + RefusalDuplicateASIN,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("refusals = %q\nwant %q", got, want)
	}
	raw, _ := json.Marshal(refusalLine{ASIN: "B0X", Reason: RefusalPositionClaimed})
	if string(raw) != `{"asin":"B0X","reason":"position-claimed"}` {
		t.Errorf("line shape = %s", raw)
	}
}

// The cap's cuts are worklist lines too.
func TestRefusalsWorklistNamesTheCapsCuts(t *testing.T) {
	dataDir := seedSelectCatalogue(t)
	dir := t.TempDir()
	in := filepath.Join(dir, "export.ndjson")
	if err := os.WriteFile(in, []byte(strings.Join(selectExportRows(), "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	refusals := filepath.Join(dir, "refusals.ndjson")
	res, err := SelectLibex(in, filepath.Join(dir, "subset.ndjson"), SelectOptions{DataDir: dataDir, MaxPerSeries: 2, RefusalsPath: refusals})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(refusals)
	got := strings.Count(string(raw), `"reason":"over-series-cap"`)
	if got == 0 || got != res.Excluded[reasonSeriesCap] {
		t.Errorf("over-series-cap lines = %d, report counts %d", got, res.Excluded[reasonSeriesCap])
	}
}

// A run that fails writes no worklist, like no subset: a half-written worklist
// would read as a complete one.
func TestRefusalsWorklistIsNotWrittenOnFailure(t *testing.T) {
	dataDir := seedSelectCatalogue(t)
	dir := t.TempDir()
	in := filepath.Join(dir, "export.json")
	if err := os.WriteFile(in, []byte(`[`+selectRow("B0SELECT02", "Volume Two", "us", "english", seriesName, "2")), 0o644); err != nil {
		t.Fatal(err)
	}
	refusals := filepath.Join(dir, "refusals.ndjson")
	if _, err := SelectLibex(in, filepath.Join(dir, "subset.ndjson"), SelectOptions{DataDir: dataDir, RefusalsPath: refusals}); err == nil {
		t.Fatal("a truncated export must fail")
	}
	if _, err := os.Stat(refusals); !os.IsNotExist(err) {
		t.Errorf("a failed run left a worklist behind: %v", err)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".metaimport-*")); len(left) > 0 {
		t.Errorf("a failed run left a temp file behind: %v", left)
	}
	if _, err := SelectLibex(in, filepath.Join(dir, "subset.ndjson"), SelectOptions{DataDir: dataDir, RefusalsPath: in}); err == nil {
		t.Error("--refusals over the input export must be refused")
	}
	sub := filepath.Join(dir, "subset.ndjson")
	if _, err := SelectLibex(in, sub, SelectOptions{DataDir: dataDir, RefusalsPath: sub}); err == nil {
		t.Error("--refusals over the subset must be refused")
	}
}

// TestLibexSelectAttachRule is the selector's half of the attach rule, allowed and
// denied: only a row that is The Lost Coast - by title, authors and language - is
// kept at The Lost Coast's position.
func TestLibexSelectAttachRule(t *testing.T) {
	for _, tc := range []struct {
		name   string
		row    string
		attach bool
	}{
		{"another narrator", attachExportRow("B0ATTACH01", "The Lost Coast", "us", "english", "Ada Mapmaker", "Cal Voice", "1"), true},
		{"same narrator, another region", attachExportRow("B0ATTACH02", "The Lost Coast", "gb", "english", "Ada Mapmaker", "Bea Reader", "1"), true},
		{"retailer-decorated title", attachExportRow("B0ATTACH03", "The Lost Coast: Cartographer Chronicles, Book 1", "us", "english", "Ada Mapmaker", "Cal Voice", "1"), true},
		{"stated as 1.0", attachExportRow("B0ATTACH04", "The Lost Coast", "au", "english", "Ada Mapmaker", "Bea Reader", "1.0"), true},
		{"the title's own volume agrees", attachExportRow("B0ATTACH06", "The Lost Coast, Book 1", "us", "english", "Ada Mapmaker", "Cal Voice", "1"), true},
		{"the title states another volume", attachExportRow("B0DENY0005", "The Lost Coast, Book 2", "us", "english", "Ada Mapmaker", "Cal Voice", "1"), false},
		{"one part of a split release", attachExportRow("B0DENY0008", "The Lost Coast (1 of 2) [Dramatized Adaptation]", "us", "english", "Ada Mapmaker", "Cal Voice", "1"), false},
		{"a part marker", attachExportRow("B0DENY0009", "The Lost Coast, Part 1", "us", "english", "Ada Mapmaker", "Cal Voice", "1"), false},
		{"a young-readers adaptation", attachExportRow("B0DENY0010", "The Lost Coast (Young Readers Edition)", "us", "english", "Ada Mapmaker", "Cal Voice", "1"), false},
		{"the retailer's series count", attachExportRow("B0ATTACH07", "The Lost Coast (Book 1 of 3)", "us", "english", "Ada Mapmaker", "Cal Voice", "1"), true},
		{"a dramatized adaptation of the whole book", attachExportRow("B0ATTACH08", "The Lost Coast [Dramatized Adaptation]", "us", "english", "Ada Mapmaker", "Cal Voice", "1"), true},
		{"no narrator", `{"asin":"B0DENY0007","title":"The Lost Coast","region":"us","language":"english",` +
			`"authors":[{"name":"Ada Mapmaker"}],"narrators":[],"series":[{"name":"` + seriesName + `","position":"1"}]}`, false},
		{"a co-author the work does not credit", `{"asin":"B0DENY0006","title":"The Lost Coast","region":"us","language":"english",` +
			`"authors":[{"name":"Ada Mapmaker"},{"name":"Zed Otherhand"}],"narrators":[{"name":"Cal Voice"}],` +
			`"series":[{"name":"` + seriesName + `","position":"1"}]}`, false},
		// The author test is the importer's own work identity (matchWork over
		// resolvePerson): a spelling it does not fold onto the catalogued person
		// is another author as far as any work match is concerned.
		{"an author spelling the importer does not fold", attachExportRow("B0DENY0011", "The Lost Coast", "ca", "english", "A. Mapmaker", "Bea Reader", "1"), false},
		{"a translator credit the work lacks", `{"asin":"B0ATTACH09","title":"The Lost Coast","region":"us","language":"english",` +
			`"authors":[{"name":"Ada Mapmaker"},{"name":"Tess Translator - translator"}],"narrators":[{"name":"Cal Voice"}],` +
			`"series":[{"name":"` + seriesName + `","position":"1"}]}`, true},
		{"a young-readers subtitle", `{"asin":"B0DENY0012","title":"The Lost Coast","subtitle":"Young Readers Edition","region":"us","language":"english",` +
			`"authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":"Cal Voice"}],` +
			`"series":[{"name":"` + seriesName + `","position":"1"}]}`, false},
		{"a subtitle naming another volume", `{"asin":"B0DENY0013","title":"The Lost Coast","subtitle":"Cartographer Chronicles, Book 2","region":"us","language":"english",` +
			`"authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":"Cal Voice"}],` +
			`"series":[{"name":"` + seriesName + `","position":"1"}]}`, false},
		{"different title", attachExportRow("B0DENY0001", "The Lost Coast Redux", "us", "english", "Ada Mapmaker", "Cal Voice", "1"), false},
		{"different author", attachExportRow("B0DENY0002", "The Lost Coast", "us", "english", "Zed Otherhand", "Cal Voice", "1"), false},
		{"translated title", attachExportRow("B0DENY0003", "Band Eins", "de", "german", "Ada Mapmaker", "Dora Sprecher", "1"), false},
		{"same title, another language", attachExportRow("B0DENY0004", "The Lost Coast", "de", "german", "Ada Mapmaker", "Dora Sprecher", "1"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, subset, lines := selectInto(t, seedAttachCatalogue(t), []string{tc.row})
			if !tc.attach {
				if res.RowsSelected != 0 || len(lines) != 1 || lines[0].Reason != RefusalPositionClaimed {
					t.Errorf("selected %d, refusals %+v; want one position-claimed refusal", res.RowsSelected, lines)
				}
				if len(res.Attachments) != 0 {
					t.Errorf("attachments = %+v", res.Attachments)
				}
				return
			}
			if res.RowsSelected != 1 || len(lines) != 0 {
				t.Fatalf("selected %d, refusals %+v; want the row selected", res.RowsSelected, lines)
			}
			if len(res.Attachments) != 1 || res.Attachments[0].Work != "the-lost-coast" ||
				res.Attachments[0].Series != "cartographer-chronicles" || res.Attachments[0].Position != "1" {
				t.Errorf("attachments = %+v, want the-lost-coast at cartographer-chronicles 1", res.Attachments)
			}
			if res.ProjectedWorks != 0 || res.SeriesMatched != 0 || len(res.PerSeries) != 0 {
				t.Errorf("an attachment projects no work: %d works, %d series, %+v", res.ProjectedWorks, res.SeriesMatched, res.PerSeries)
			}
			body, _ := os.ReadFile(subset)
			if strings.TrimSuffix(string(body), "\n") != tc.row {
				t.Errorf("subset = %s, want the row verbatim", body)
			}
			if !strings.Contains(res.Report(), "  attached: 1 row to 1 catalogued work already at the position claimed") {
				t.Errorf("the report does not name the attachment:\n%s", res.Report())
			}
		})
	}
}

// attachImport selects rows against a fresh catalogue and imports the subset
// exactly as the sync bot does (--existing-series-only), returning the summary
// and the tree. It asserts what no attachment may ever do: create a work beyond
// the completions (wantWorks), found a series, or move or add to the incumbent's
// series entry beyond those completions.
func attachImport(t *testing.T, wantWorks map[string]string, rows ...string) (SelectResult, Summary, string) {
	t.Helper()
	dataDir := seedAttachCatalogue(t)
	res, subset, _ := selectInto(t, dataDir, rows)
	sum, err := RunLibex(subset, Options{DataDir: dataDir, ImportDate: testImportDate, ExistingSeriesOnly: true})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if sum.NewWorks != len(wantWorks) || sum.NewSeries != 0 {
		t.Errorf("NewWorks/NewSeries = %d/%d, want %d/0", sum.NewWorks, sum.NewSeries, len(wantWorks))
	}
	want := map[string]string{"the-lost-coast": "1.0"}
	for w, pos := range wantWorks {
		want[w] = pos
	}
	if got := seriesWorks(t, dataDir, "cartographer-chronicles"); !reflect.DeepEqual(got, want) {
		t.Errorf("series = %v, want %v", got, want)
	}
	assertTreeValid(t, dataDir)
	return res, sum, dataDir
}

// Another narration of the incumbent becomes a NEW RECORDING of it.
func TestAttachAnotherNarratorIsANewRecording(t *testing.T) {
	_, sum, dataDir := attachImport(t, nil,
		attachExportRow("B0ATTACH01", "The Lost Coast", "us", "english", "Ada Mapmaker", "Cal Voice", "1"))
	if sum.Attached != 1 || sum.NewRecordings != 1 || sum.MergedASINs != 0 {
		t.Errorf("Attached/NewRecordings/MergedASINs = %d/%d/%d, want 1/1/0", sum.Attached, sum.NewRecordings, sum.MergedASINs)
	}
	var rec struct {
		Narrators []string
		ASIN      []struct{ ASIN, Region string }
		Language  string
		Sources   []struct{ Type, Ref string }
	}
	readEntity(t, dataDir, recAddr("the-lost-coast", "cal-voice"), &rec)
	if !reflect.DeepEqual(rec.Narrators, []string{"cal-voice"}) || len(rec.ASIN) != 1 || rec.ASIN[0].ASIN != "B0ATTACH01" {
		t.Errorf("recording = %+v", rec)
	}
	if len(rec.Sources) != 1 || rec.Sources[0].Type != sourceLibex || rec.Sources[0].Ref != "B0ATTACH01" {
		t.Errorf("the recording does not carry libex-import provenance: %+v", rec.Sources)
	}
	if entryExists(t, dataDir, workAddr("the-lost-coast-ada-mapmaker")) {
		t.Error("a sibling work was created")
	}
}

// The same production in another marketplace MERGES its ASIN onto the recording
// whose narrators match, and changes nothing else about it.
func TestAttachSameNarratorAnotherRegionMergesTheASIN(t *testing.T) {
	_, sum, dataDir := attachImport(t, nil,
		attachExportRow("B0ATTACH02", "The Lost Coast", "gb", "english", "Ada Mapmaker", "Bea Reader", "1"))
	if sum.Attached != 1 || sum.NewRecordings != 0 || sum.MergedASINs != 1 {
		t.Errorf("Attached/NewRecordings/MergedASINs = %d/%d/%d, want 1/0/1", sum.Attached, sum.NewRecordings, sum.MergedASINs)
	}
	var rec struct {
		ASIN []struct{ ASIN, Region string }
	}
	readEntity(t, dataDir, recAddr("the-lost-coast", "bea-reader-2024"), &rec)
	want := []struct{ ASIN, Region string }{{"B0PRESENT1", "us"}, {"B0ATTACH02", "uk"}}
	if !reflect.DeepEqual(rec.ASIN, want) {
		t.Errorf("asin = %+v, want %+v", rec.ASIN, want)
	}
}

// Completions and attachments in one tranche: the completion creates its work
// and is placed, the attachment creates nothing but its recording, and the
// work key an attachment carries never counts against the per-series cap.
func TestAttachAlongsideACompletion(t *testing.T) {
	res, sum, dataDir := attachImport(t, map[string]string{"volume-two": "2"},
		attachExportRow("B0ATTACH01", "The Lost Coast", "us", "english", "Ada Mapmaker", "Cal Voice", "1"),
		attachExportRow("B0SELECT02", "Volume Two", "us", "english", "Ada Mapmaker", "Bea Reader", "2"),
	)
	if res.RowsSelected != 2 || res.ProjectedWorks != 1 || len(res.Attachments) != 1 {
		t.Errorf("selected %d rows / %d works / %d attachments, want 2/1/1", res.RowsSelected, res.ProjectedWorks, len(res.Attachments))
	}
	if sum.Attached != 1 || sum.NewRecordings != 2 {
		t.Errorf("Attached/NewRecordings = %d/%d, want 1/2", sum.Attached, sum.NewRecordings)
	}
	if !entryExists(t, dataDir, recAddr("the-lost-coast", "cal-voice")) {
		t.Error("the attachment's recording is missing")
	}
}

// A capped selection never cuts an attachment and never counts one: the cap is
// about new works.
func TestAttachmentsAreOutsideTheSeriesCap(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "export.ndjson")
	rows := []string{
		attachExportRow("B0ATTACH01", "The Lost Coast", "us", "english", "Ada Mapmaker", "Cal Voice", "1"),
		attachExportRow("B0ATTACH02", "The Lost Coast", "gb", "english", "Ada Mapmaker", "Bea Reader", "1"),
		attachExportRow("B0SELECT02", "Volume Two", "us", "english", "Ada Mapmaker", "Bea Reader", "2"),
		attachExportRow("B0SELECT03", "Volume Three", "us", "english", "Ada Mapmaker", "Bea Reader", "3"),
	}
	if err := os.WriteFile(in, []byte(strings.Join(rows, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := SelectLibex(in, filepath.Join(dir, "subset.ndjson"), SelectOptions{DataDir: seedAttachCatalogue(t), MaxPerSeries: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Attachments) != 2 || res.ProjectedWorks != 1 || res.Excluded[reasonSeriesCap] != 1 {
		t.Errorf("attachments %d / works %d / cap cut %d, want 2/1/1", len(res.Attachments), res.ProjectedWorks, res.Excluded[reasonSeriesCap])
	}
	assertPartition(t, res)
}

// Without ExistingSeriesOnly nothing is attached: the option is off by default
// and every other caller plans the row exactly as before.
func TestAttachIsOffWithoutExistingSeriesOnly(t *testing.T) {
	dataDir := seedAttachCatalogue(t)
	sum := runLibexWith(t, dataDir, Options{},
		attachExportRow("B0ATTACH01", "The Lost Coast", "us", "english", "Ada Mapmaker", "Cal Voice", "1"))
	if sum.Attached != 0 {
		t.Errorf("Attached = %d without the option", sum.Attached)
	}
}

// TestLibexSelectReportIsUnchangedWithoutAttachments pins the report a selection
// that attaches nothing prints, byte for byte: the sync bot keeps it whole in its
// pull request body and matches its head line.
func TestLibexSelectReportIsUnchangedWithoutAttachments(t *testing.T) {
	res, _ := runSelect(t, seedSelectCatalogue(t), selectExportRows(), 0)
	const want = `selected 5 of 9 rows: 4 projected new works across 1 catalogue series
  Cartographer Chronicles                    4 works,   5 rows
  total: 1 series, 4 works, 5 rows
excluded 4 rows:
  malformed or missing ASIN              0
  ASIN already in the catalogue          1
  duplicate ASIN within the export       0
  no catalogue series                    1
  catalogue series belongs to other authors 0
  another claimed series is not in the catalogue 0
  series position missing or unparseable 0
  unmapped language                      1
  unmapped region                        1
  narrated by an AI voice                0
  a credited name is a platform account  0
  a credited name is a list of people    0
  a credited name is a cast placeholder  0
  a credited name does not identify a person 0
  series position already claimed        0
  over the per-series cap                0
`
	if got := res.Report(); got != want {
		t.Errorf("report changed:\n got:\n%s\nwant:\n%s", got, want)
	}
}

// A regional sibling of a volume this RUN creates is not an attachment: the
// position was free when the catalogue was loaded, so both rows go through the
// create path exactly as before - one new work, the sibling's ASIN merged onto
// its recording - and nothing is counted as attached or refused.
func TestAttachNeverTargetsAWorkThisRunCreated(t *testing.T) {
	dataDir := seedAttachCatalogue(t)
	res, subset, lines := selectInto(t, dataDir, []string{
		attachExportRow("B0SELECT02", "Volume Two", "us", "english", "Ada Mapmaker", "Bea Reader", "2"),
		attachExportRow("B0SELECT2B", "Volume Two", "gb", "english", "Ada Mapmaker", "Bea Reader", "2"),
	})
	if res.RowsSelected != 2 || res.ProjectedWorks != 1 || len(res.Attachments) != 0 || len(lines) != 0 {
		t.Fatalf("selected %d / works %d / attachments %d / refusals %v, want 2/1/0/none",
			res.RowsSelected, res.ProjectedWorks, len(res.Attachments), lines)
	}
	sum, err := RunLibex(subset, Options{DataDir: dataDir, ImportDate: testImportDate, ExistingSeriesOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if sum.NewWorks != 1 || sum.NewRecordings != 1 || sum.MergedASINs != 1 || sum.Attached != 0 || sum.SkippedOccupied != 0 {
		t.Errorf("NewWorks/NewRecordings/MergedASINs/Attached/SkippedOccupied = %d/%d/%d/%d/%d, want 1/1/1/0/0",
			sum.NewWorks, sum.NewRecordings, sum.MergedASINs, sum.Attached, sum.SkippedOccupied)
	}
	assertTreeValid(t, dataDir)
}

// The import's own fall-through: under ExistingSeriesOnly a row claiming an
// occupied catalogued position that is not another edition of the work there is
// REFUSED with a warning, never planned as a sibling work - whatever subset the
// caller hands it, selected or not.
func TestExistingSeriesOnlyRefusesARowAtAnOccupiedPosition(t *testing.T) {
	dataDir := seedAttachCatalogue(t)
	coAuthored := `{"asin":"B0DENY0006","title":"The Lost Coast","region":"us","language":"english",` +
		`"authors":[{"name":"Ada Mapmaker"},{"name":"Zed Otherhand"}],"narrators":[{"name":"Cal Voice"}],` +
		`"series":[{"name":"` + seriesName + `","position":"1"}]}`
	sum := runLibexWith(t, dataDir, Options{ExistingSeriesOnly: true}, coAuthored,
		attachExportRow("B0DENY0001", "The Lost Coast Redux", "us", "english", "Ada Mapmaker", "Cal Voice", "1"))
	if sum.NewWorks != 0 || sum.NewRecordings != 0 || sum.Attached != 0 || sum.SkippedOccupied != 2 || sum.NewPeople != 0 {
		t.Errorf("NewWorks/NewRecordings/Attached/SkippedOccupied/NewPeople = %d/%d/%d/%d/%d, want 0/0/0/2/0",
			sum.NewWorks, sum.NewRecordings, sum.Attached, sum.SkippedOccupied, sum.NewPeople)
	}
	if n := countWarnings(sum.Warnings, `already held by "the-lost-coast"`); n != 2 {
		t.Errorf("want a warning per refused row, got %d: %v", n, sum.Warnings)
	}
	if got := seriesWorks(t, dataDir, "cartographer-chronicles"); !reflect.DeepEqual(got, map[string]string{"the-lost-coast": "1.0"}) {
		t.Errorf("series = %v", got)
	}
	assertTreeValid(t, dataDir)
}

// --attachments lists exactly the attached rows, in the contract's shape, and
// is committed with the subset.
func TestAttachmentsWorklist(t *testing.T) {
	dataDir := seedAttachCatalogue(t)
	dir := t.TempDir()
	in := filepath.Join(dir, "export.ndjson")
	rows := []string{
		attachExportRow("B0ATTACH01", "The Lost Coast", "us", "english", "Ada Mapmaker", "Cal Voice", "1"),
		attachExportRow("B0SELECT02", "Volume Two", "us", "english", "Ada Mapmaker", "Bea Reader", "2"),
	}
	if err := os.WriteFile(in, []byte(strings.Join(rows, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	att := filepath.Join(dir, "attachments.ndjson")
	if _, err := SelectLibex(in, filepath.Join(dir, "subset.ndjson"), SelectOptions{DataDir: dataDir, AttachmentsPath: att}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(att)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"asin":"B0ATTACH01","work":"the-lost-coast","series":"cartographer-chronicles","position":"1"}` + "\n"
	if string(got) != want {
		t.Errorf("attachments = %q, want %q", got, want)
	}
}

// No output may land on the input or on another output - by name or through a
// link - and the message names the flags involved.
func TestSelectOutputsMayNotOverlap(t *testing.T) {
	dataDir := seedSelectCatalogue(t)
	dir := t.TempDir()
	in := filepath.Join(dir, "export.ndjson")
	if err := os.WriteFile(in, []byte(selectRow("B0SELECT02", "Volume Two", "us", "english", seriesName, "2")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "subset.ndjson")
	if err := os.WriteFile(sub, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.ndjson")
	if err := os.Symlink(sub, link); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		opts SelectOptions
		want string
	}{
		{"--refusals over the input", SelectOptions{RefusalsPath: in}, "refusing to write --refusals over the input export"},
		{"--attachments over the input", SelectOptions{AttachmentsPath: in}, "refusing to write --attachments over the input export"},
		{"--refusals over -o", SelectOptions{RefusalsPath: sub}, "refusing to write -o and --refusals to one file"},
		{"--refusals linked to -o", SelectOptions{RefusalsPath: link}, "refusing to write -o and --refusals to one file"},
		{"--attachments over --refusals", SelectOptions{RefusalsPath: filepath.Join(dir, "r"), AttachmentsPath: filepath.Join(dir, "r")},
			"refusing to write --refusals and --attachments to one file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.opts.DataDir = dataDir
			_, err := SelectLibex(in, sub, tc.opts)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

// All three outputs are committed together: a failed run leaves none of them
// and no temp file.
func TestSelectOutputsAreAllOrNothing(t *testing.T) {
	dataDir := seedSelectCatalogue(t)
	dir := t.TempDir()
	in := filepath.Join(dir, "export.json")
	if err := os.WriteFile(in, []byte(`[`+selectRow("B0SELECT02", "Volume Two", "us", "english", seriesName, "2")), 0o644); err != nil {
		t.Fatal(err)
	}
	opts := SelectOptions{DataDir: dataDir, RefusalsPath: filepath.Join(dir, "r.ndjson"), AttachmentsPath: filepath.Join(dir, "a.ndjson")}
	if _, err := SelectLibex(in, filepath.Join(dir, "s.ndjson"), opts); err == nil {
		t.Fatal("a truncated export must fail")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("a failed run left files behind: %v", names)
	}
}

// The incumbent's own SUBTITLE is read by the product vetoes too, both ways: a
// young-readers edition in the catalogue is not the adult book, whatever field
// said so.
func TestAttachReadsTheIncumbentsSubtitle(t *testing.T) {
	dataDir := t.TempDir()
	seedTree(t, dataDir, map[string]string{
		"people/ad/ada-mapmaker.json": `{"id":"ada-mapmaker","license":"CC0-1.0","name":"Ada Mapmaker","sources":[{"type":"user"}]}`,
		"works/th/the-lost-coast/work.json": `{"authors":["ada-mapmaker"],"id":"the-lost-coast","language":"en","license":"CC0-1.0",` +
			`"sources":[{"type":"user"}],"subtitle":"Young Readers Edition","title":"The Lost Coast"}`,
		"series/ca/cartographer-chronicles.json": `{"id":"cartographer-chronicles","license":"CC0-1.0","name":"Cartographer Chronicles",` +
			`"sources":[{"type":"user"}],"works":[{"position":"1","work":"the-lost-coast"}]}`,
	})
	res, _, lines := selectInto(t, dataDir, []string{
		attachExportRow("B0DENY0014", "The Lost Coast", "us", "english", "Ada Mapmaker", "Cal Voice", "1"),
		`{"asin":"B0ATTACH10","title":"The Lost Coast","subtitle":"Young Readers Edition","region":"us","language":"english",` +
			`"authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":"Cal Voice"}],"series":[{"name":"` + seriesName + `","position":"1"}]}`,
	})
	if len(res.Attachments) != 1 || res.Attachments[0].ASIN != "B0ATTACH10" {
		t.Errorf("attachments = %+v, want only the young-readers row", res.Attachments)
	}
	if len(lines) != 1 || lines[0].ASIN != "B0DENY0014" || lines[0].Reason != RefusalPositionClaimed {
		t.Errorf("refusals = %+v, want the adult row refused as position-claimed", lines)
	}
}

// An attached or refused row's OTHER positioned claims are placements it asked
// for and did not get, so they are reported as lost, like any row that could
// not be imported.
func TestAttachReportsTheRowsOtherClaimsAsLost(t *testing.T) {
	dataDir := seedAttachCatalogue(t)
	seedTree(t, dataDir, map[string]string{
		"series/at/atlas-cycle.json": `{"id":"atlas-cycle","license":"CC0-1.0","name":"Atlas Cycle",` +
			`"sources":[{"type":"user"}],"works":[{"position":"1","work":"the-lost-coast"}]}`,
	})
	row := `{"asin":"B0ATTACH11","title":"The Lost Coast","region":"us","language":"english",` +
		`"authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":"Cal Voice"}],` +
		`"series":[{"name":"` + seriesName + `","position":"1"},{"name":"Atlas Cycle","position":"4"}]}`
	sum := runLibexWith(t, dataDir, Options{ExistingSeriesOnly: true}, row)
	if sum.Attached != 1 {
		t.Fatalf("Attached = %d", sum.Attached)
	}
	if !hasWarning(sum.Warnings, "1 series placements lost") {
		t.Errorf("the second claim is not reported lost: %v", sum.Warnings)
	}
	if got := seriesWorks(t, dataDir, "atlas-cycle"); !reflect.DeepEqual(got, map[string]string{"the-lost-coast": "1"}) {
		t.Errorf("atlas-cycle = %v - an attachment placed the incumbent", got)
	}
}

// The --skipped worklist's source: every refused row with an ASIN and a code,
// position-claimed for a row --existing-series-only turned away.
func TestImportSkipsCarryRefusalCodes(t *testing.T) {
	dataDir := seedAttachCatalogue(t)
	sum := runLibexWith(t, dataDir, Options{ExistingSeriesOnly: true},
		attachExportRow("B0DENY0001", "The Lost Coast Redux", "us", "english", "Ada Mapmaker", "Cal Voice", "1"),
		attachExportRow("B0REGION04", "Volume Four", "zz", "english", "Ada Mapmaker", "Cal Voice", "4"),
		attachExportRow("B0LANG0003", "Volume Three", "us", "klingon", "Ada Mapmaker", "Cal Voice", "3"),
		attachExportRow("", "Volume Five", "us", "english", "Ada Mapmaker", "Cal Voice", "5"),
	)
	want := []RowSkip{
		{"B0REGION04", RefusalUnmappedRegion},
		{"B0DENY0001", RefusalPositionClaimed},
		{"B0LANG0003", RefusalUnmappedLanguage},
	}
	if !reflect.DeepEqual(sum.Skips, want) {
		t.Errorf("Skips = %+v\nwant %+v", sum.Skips, want)
	}
}

// summarize must not assume every series the cap cut still has a kept
// completion: the cap and the batch re-check run to a fixpoint, and a later
// re-check can drop what the cap kept.
func TestSummarizeACutSeriesWithNoKeptRows(t *testing.T) {
	var res SelectResult
	idx := seriesIndex{names: map[string]string{"gone": "Gone Series", "kept": "Kept Series"}}
	rows := []selectedRow{{seriesSlug: "kept", workKey: "kept\x00a"}}
	cuts := map[string]SeriesCount{"gone": {Series: "gone", CutWorks: 2, CutRows: 3}, "kept": {Series: "kept", CutWorks: 1, CutRows: 1}}
	res.Excluded = map[string]int{}
	summarize(rows, cuts, idx, &res)
	if len(res.PerSeries) != 1 || res.PerSeries[0].CutWorks != 1 {
		t.Errorf("PerSeries = %+v", res.PerSeries)
	}
	if len(res.CutOnly) != 1 || res.CutOnly[0].Name != "Gone Series" || res.CutOnly[0].CutWorks != 2 {
		t.Errorf("CutOnly = %+v", res.CutOnly)
	}
	if r := res.Report(); !strings.Contains(r, "the per-series cap cut works from 2 series") || !strings.Contains(r, "Gone Series") {
		t.Errorf("report:\n%s", r)
	}
}

// Two outputs that do not exist yet are still one file when their names differ
// only in case (APFS, NTFS) or reach one directory through a symlinked parent.
func TestSelectOutputsCatchAliasesOfNewFiles(t *testing.T) {
	dataDir := seedSelectCatalogue(t)
	dir := t.TempDir()
	in := filepath.Join(dir, "export.ndjson")
	if err := os.WriteFile(in, []byte(selectRow("B0SELECT02", "Volume Two", "us", "english", seriesName, "2")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out")
	if err := os.Mkdir(out, 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(dir, "alias")
	if err := os.Symlink(out, alias); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(out, "subset.ndjson")
	for _, tc := range []struct{ name, refusals string }{
		{"case only", filepath.Join(out, "Subset.NDJSON")},
		{"symlinked parent", filepath.Join(alias, "subset.ndjson")},
		{"input, case only", filepath.Join(dir, "EXPORT.ndjson")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := SelectLibex(in, sub, SelectOptions{DataDir: dataDir, RefusalsPath: tc.refusals})
			if err == nil || !strings.Contains(err.Error(), "refusing to write") {
				t.Errorf("err = %v, want a refusal", err)
			}
		})
	}
}

// The subset is renamed LAST: when it cannot be placed, the worklists already
// renamed are removed again, so no output of a failed run survives.
func TestSelectOutputsCommitTheSubsetLast(t *testing.T) {
	dataDir := seedSelectCatalogue(t)
	dir := t.TempDir()
	in := filepath.Join(dir, "export.ndjson")
	if err := os.WriteFile(in, []byte(selectRow("B0OTHER001", "Unrelated", "us", "english", "Elsewhere", "1")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A non-empty directory where the subset goes: its rename must fail.
	sub := filepath.Join(dir, "subset.ndjson")
	if err := os.MkdirAll(filepath.Join(sub, "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	refusals, attachments := filepath.Join(dir, "r.ndjson"), filepath.Join(dir, "a.ndjson")
	if _, err := SelectLibex(in, sub, SelectOptions{DataDir: dataDir, RefusalsPath: refusals, AttachmentsPath: attachments}); err == nil {
		t.Fatal("the subset's rename must fail")
	}
	for _, p := range []string{refusals, attachments} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s survived a failed commit: %v", p, err)
		}
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".metaimport-*")); len(left) > 0 {
		t.Errorf("temp files left: %v", left)
	}
}
