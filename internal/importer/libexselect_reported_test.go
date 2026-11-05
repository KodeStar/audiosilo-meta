package importer

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/check"
)

// libexselect_reported_test.go pins `libex-select --reported`, the sync bot's
// reported-ASIN lane: a row stating no series is kept under every refusal that does
// not read a series, and a row claiming one is judged exactly as it is without the
// flag.

// standaloneRow renders a series-less row with the given credits and marketplace.
func standaloneRow(asin, title, region, language, author, narrator string) string {
	return `{"asin":"` + asin + `","title":"` + title + `","region":"` + region + `","language":"` + language +
		`","authors":[{"name":"` + author + `"}],"narrators":[{"name":"` + narrator + `"}],"series":[]}`
}

// selectRun is one libex-select run with its worklists read back.
type selectRun struct {
	res      SelectResult
	subset   []string
	refusals []RowSkip
}

// selectWith runs the selector over rows under opts and reads the subset back.
func selectWith(t *testing.T, dataDir string, rows []string, opts SelectOptions) selectRun {
	t.Helper()
	res, out, refusals := selectIntoWith(t, dataDir, rows, opts)
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return selectRun{res: res, subset: worklistLines(t, string(raw)), refusals: refusals}
}

// importAsBot is the bot's two steps: select --reported --attach-editions, then
// import what was kept with --existing-series-only --attach-editions.
func importAsBot(t *testing.T, dataDir string, rows []string) (selectRun, Summary) {
	t.Helper()
	run := selectWith(t, dataDir, rows, SelectOptions{Reported: true, AttachEditions: true, ImportDate: testImportDate})
	return run, runLibexWith(t, dataDir, Options{ExistingSeriesOnly: true, AttachEditions: true}, run.subset...)
}

// TestReportedSelectTable is each row's fate alone against the catalogue, under
// --reported with and without --attach-editions (the bot passes both).
func TestReportedSelectTable(t *testing.T) {
	const ada, bea = "Ada Mapmaker", "Bea Reader"
	cases := []struct {
		name string
		row  string
		want string // the refusal code, "" when the row is kept
		// standalone says a kept row is in no series.
		standalone bool
	}{
		{name: "series-less row is kept", row: standaloneRow("B0ALONE001", "A Lone Book", "us", "english", ada, bea), standalone: true},
		{name: "no series key at all is kept",
			row:        `{"asin":"B0ALONE002","title":"A Lone Book","region":"us","language":"english","authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":"Bea Reader"}]}`,
			standalone: true},
		{name: "an entry stating nothing is no claim",
			row:        `{"asin":"B0ALONE003","title":"A Lone Book","region":"us","language":"english","authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":"Bea Reader"}],"series":[{"name":"","position":""}]}`,
			standalone: true},
		{name: "a null series is no claim",
			row:        `{"asin":"B0ALONE012","title":"A Lone Book","region":"us","language":"english","authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":"Bea Reader"}],"series":null}`,
			standalone: true},
		{name: "a preorder is still a row", row: `{"asin":"B0ALONE004","title":"A Lone Book","region":"us","language":"english","releaseDate":"2099-01-01","authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":"Bea Reader"}],"series":[]}`, standalone: true},

		// A series the parser cannot read was still stated, so the row is no standalone.
		{name: "a position with no name", row: `{"asin":"B0SHAPE004","title":"A Lone Book","region":"us","language":"english","authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":"Bea Reader"}],"series":[{"name":"","position":"3"}]}`, want: RefusalNoCatalogueSeries},
		{name: "a series ASIN with no name", row: `{"asin":"B0SHAPE005","title":"A Lone Book","region":"us","language":"english","authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":"Bea Reader"}],"series":[{"asin":"B0SERIES01"}]}`, want: RefusalNoCatalogueSeries},
		{name: "a series as a bare string", row: `{"asin":"B0SHAPE001","title":"A Lone Book","region":"us","language":"english","authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":"Bea Reader"}],"series":"Some Saga"}`, want: RefusalNoCatalogueSeries},
		{name: "a series as one object", row: `{"asin":"B0SHAPE002","title":"A Lone Book","region":"us","language":"english","authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":"Bea Reader"}],"series":{"name":"Some Saga","position":"1"}}`, want: RefusalNoCatalogueSeries},
		{name: "a series as an array of strings", row: `{"asin":"B0SHAPE003","title":"A Lone Book","region":"us","language":"english","authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":"Bea Reader"}],"series":["Some Saga"]}`, want: RefusalNoCatalogueSeries},

		{name: "AI narrator", row: standaloneRow("B0ALONE005", "A Lone Book", "us", "english", ada, "Virtual Voice"), want: RefusalAINarrator},
		{name: "AI author", row: standaloneRow("B0ALONE006", "A Lone Book", "us", "english", "Virtual Voice", bea), want: RefusalAINarrator},
		{name: "unmapped language", row: standaloneRow("B0ALONE007", "A Lone Book", "us", "klingon", ada, bea), want: RefusalUnmappedLanguage},
		{name: "unmapped region", row: standaloneRow("B0ALONE008", "A Lone Book", "zz", "english", ada, bea), want: RefusalUnmappedRegion},
		{name: "platform account credit", row: standaloneRow("B0ALONE009", "A Lone Book", "us", "english", ada, "acx-dev+gamma4"), want: RefusalCreditPlatformAccount},
		{name: "list of people credit", row: standaloneRow("B0ALONE010", "A Lone Book", "us", "english", "Ada Mapmaker; Bea Reader", bea), want: RefusalCreditListOfPeople},
		{name: "unidentifiable credit", row: standaloneRow("B0ALONE011", "A Lone Book", "us", "english", ada, "!!!"), want: RefusalCreditNotAPerson},
		{name: "ASIN already held", row: standaloneRow("B0PRESENT1", "A Lone Book", "us", "english", ada, bea), want: RefusalASINInCatalogue},
		{name: "malformed ASIN", row: standaloneRow("nope", "A Lone Book", "us", "english", ada, bea), want: RefusalMalformedASIN},

		// A row claiming a series gets nothing from the flag.
		{name: "volume of a held series is kept", row: selectRow("B0VOLUME02", "Volume Two", "us", "english", seriesName, "2")},
		{name: "unheld series is refused", row: selectRow("B0UNHELD01", "Unrelated Book", "us", "english", "Some Other Series", "1"), want: RefusalNoCatalogueSeries},
		{name: "a claim with no addressable name is still a claim", row: selectRow("B0UNHELD02", "Unrelated Book", "us", "english", "!!!", "1"), want: RefusalNoCatalogueSeries},
		{name: "a second, unheld claim mints", row: selectRowClaiming("B0UNHELD03", "Volume Two", "us", "english",
			claimSpec{seriesName, "2"}, claimSpec{"Unknown Saga", "1"}), want: RefusalExtraSeriesUncatalogued},
		{name: "held series, no position", row: selectRow("B0NOPOS001", "Volume Two", "us", "english", seriesName, ""), want: RefusalPositionUnparseable},
		{name: "held series, filled position", row: selectRow("B0FILLED01", "Something Else", "us", "english", seriesName, "1"), want: RefusalPositionClaimed},
	}
	dataDir := seedSelectCatalogue(t) // a selection never writes to it
	for _, tc := range cases {
		for _, attach := range []bool{false, true} {
			name := tc.name
			if attach {
				name += " (attach-editions)"
			}
			t.Run(name, func(t *testing.T) {
				run := selectWith(t, dataDir, []string{tc.row}, SelectOptions{Reported: true, AttachEditions: attach})
				if tc.want != "" {
					if len(run.subset) != 0 {
						t.Errorf("subset = %v, want the row refused", run.subset)
					}
					if len(run.refusals) != 1 || run.refusals[0].Reason != tc.want {
						t.Errorf("refusals = %+v, want one %s", run.refusals, tc.want)
					}
					return
				}
				if len(run.refusals) != 0 {
					t.Errorf("refusals = %+v, want the row kept", run.refusals)
				}
				// Verbatim, like every kept row.
				if len(run.subset) != 1 || run.subset[0] != tc.row {
					t.Errorf("subset = %v, want the row byte for byte", run.subset)
				}
				if got := run.res.StandaloneRows == 1; got != tc.standalone {
					t.Errorf("StandaloneRows = %d, standalone = %v", run.res.StandaloneRows, tc.standalone)
				}
			})
		}
	}
}

// A catalogue that loaded nothing stops a --reported run, since every series-less
// row would read as new against it. A plain selection only warns, as it always has:
// it keeps nothing there.
func TestReportedRefusesACatalogueThatLoadedNothing(t *testing.T) {
	legacy := t.TempDir()
	workDir := filepath.Join(legacy, "works", "so", "some-book")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "work.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirs := map[string]string{
		"an unopenable layout": legacy,
		"a missing directory":  filepath.Join(t.TempDir(), "nope"),
		"an empty directory":   t.TempDir(),
	}
	row := standaloneRow("B0ALONE001", "A Lone Book", "us", "english", "Ada Mapmaker", "Bea Reader")
	for name, dataDir := range dirs {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			in, out := filepath.Join(dir, "rows.ndjson"), filepath.Join(dir, "subset.ndjson")
			if err := os.WriteFile(in, []byte(row+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := SelectLibex(in, out, SelectOptions{DataDir: dataDir, Reported: true}); err == nil {
				t.Error("SelectLibex --reported succeeded against a catalogue that loaded nothing")
			}
			if _, err := os.Stat(out); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("a refused run left a subset behind: %v", err)
			}
			res, err := SelectLibex(in, out, SelectOptions{DataDir: dataDir})
			if err != nil || res.RowsSelected != 0 {
				t.Errorf("plain selection: selected %d, err %v; want nothing kept and no error", res.RowsSelected, err)
			}
		})
	}
}

// A same-named series closed to the row's author stays refused: the import would
// have to found a series for it, which the lane may not.
func TestReportedRefusesAnotherAuthorsSeries(t *testing.T) {
	row := tombRow("B0CAMPB004", "Valiant", "Jack Campbell", "Bea Reader", 600, "Lost Fleet", "4")
	run := selectWith(t, lostFleetTree(t, nil), []string{row}, SelectOptions{Reported: true})
	if len(run.subset) != 0 || len(run.refusals) != 1 || run.refusals[0].Reason != RefusalSeriesOtherAuthors {
		t.Errorf("subset %v, refusals %+v; want one series-other-authors", run.subset, run.refusals)
	}
}

// Without the flag a series-less row is refused as it always was, and the report
// says nothing about standalones.
func TestSeriesLessRowIsRefusedWithoutReported(t *testing.T) {
	rows := []string{standaloneRow("B0ALONE001", "A Lone Book", "us", "english", "Ada Mapmaker", "Bea Reader")}
	run := selectWith(t, seedSelectCatalogue(t), rows, SelectOptions{})
	if len(run.subset) != 0 || len(run.refusals) != 1 || run.refusals[0].Reason != RefusalNoCatalogueSeries {
		t.Errorf("subset %v, refusals %+v; want one no-catalogue-series", run.subset, run.refusals)
	}
	if run.res.StandaloneRows != 0 || run.res.StandaloneWorks != 0 || strings.Contains(run.res.Report(), "standalone") {
		t.Errorf("a run without --reported reports standalones:\n%s", run.res.Report())
	}
}

// The flag changes nothing for rows that claim a series: over the whole completion
// fixture the subset, the worklist, the series figures and the report are identical
// with and without it, under every option the flag can sit beside.
func TestReportedLeavesClaimingRowsAlone(t *testing.T) {
	rows := append(selectExportRows(),
		selectRowClaiming("B0TWOCLM08", "Volume Eight", "us", "english", claimSpec{seriesName, "8"}, claimSpec{"Unknown Saga", "1"}),
		selectRow("B0ATTACH01", "Volume One", "gb", "english", seriesName, "1"),
		selectRow("B0NOPOS001", "Volume Nine", "us", "english", seriesName, ""),
	)
	dataDir := seedSelectCatalogue(t) // a selection never writes to it
	for _, opts := range []SelectOptions{{}, {MaxPerSeries: 2}, {AttachEditions: true}, {MaxPerSeries: 2, AttachEditions: true}} {
		reported := opts
		reported.Reported = true
		off := selectWith(t, dataDir, rows, opts)
		on := selectWith(t, dataDir, rows, reported)
		if !slices.Equal(off.subset, on.subset) {
			t.Errorf("%+v: subset\n off %v\n on  %v", opts, off.subset, on.subset)
		}
		if !slices.EqualFunc(off.refusals, on.refusals, func(a, b RowSkip) bool { return a.ASIN == b.ASIN && a.Reason == b.Reason }) {
			t.Errorf("%+v: refusals\n off %+v\n on  %+v", opts, off.refusals, on.refusals)
		}
		if a, b := off.res.Report(), on.res.Report(); a != b {
			t.Errorf("%+v: report\n off:\n%s\n on:\n%s", opts, a, b)
		}
	}
}

// A standalone has no slug for the batch re-check, the cap or the tallies to read,
// and each would misread an empty one: confirmBatch's verdict drops a row with no
// completion claim as series-other-authors, the cap would group every standalone
// into one series named "" and cut them, and summarize would count "" as a matched
// series. It stays in the batch, and the series rows beside it keep their figures.
func TestStandaloneThroughBatchCapAndSummary(t *testing.T) {
	rows := []string{
		standaloneRow("B0ALONE01A", "A Lone Book", "us", "english", "Ada Mapmaker", "Bea Reader"),
		// A regional sibling of it: one work.
		standaloneRow("B0ALONE01B", "A Lone Book", "gb", "english", "Ada Mapmaker", "Bea Reader"),
		// The same title by someone else: another work.
		standaloneRow("B0ALONE002", "A Lone Book", "us", "english", "Cal Stranger", "Bea Reader"),
		standaloneRow("B0ALONE003", "Another Book", "us", "english", "Ada Mapmaker", "Bea Reader"),
		selectRow("B0SELECT02", "Volume Two", "us", "english", seriesName, "2"),
		selectRow("B0SELECT03", "Volume Three", "us", "english", seriesName, "3"),
	}
	for _, attach := range []bool{false, true} {
		run := selectWith(t, seedSelectCatalogue(t), rows, SelectOptions{Reported: true, MaxPerSeries: 1, AttachEditions: attach})
		res := run.res
		// The cap cuts Volume Three and no standalone.
		if len(run.subset) != 5 || len(run.refusals) != 1 || run.refusals[0].ASIN != "B0SELECT03" || run.refusals[0].Reason != RefusalOverSeriesCap {
			t.Errorf("attach=%v: subset %v, refusals %+v", attach, run.subset, run.refusals)
		}
		if res.StandaloneRows != 4 || res.StandaloneWorks != 3 {
			t.Errorf("attach=%v: standalone rows/works = %d/%d, want 4/3", attach, res.StandaloneRows, res.StandaloneWorks)
		}
		if res.ProjectedWorks != 4 {
			t.Errorf("attach=%v: ProjectedWorks = %d, want 4 (three standalones, one volume)", attach, res.ProjectedWorks)
		}
		if res.SeriesMatched != 1 || len(res.PerSeries) != 1 || res.PerSeries[0].Series != "cartographer-chronicles" ||
			res.PerSeries[0].Works != 1 || res.PerSeries[0].Rows != 1 {
			t.Errorf("attach=%v: SeriesMatched %d, PerSeries %+v; a standalone is in no series", attach, res.SeriesMatched, res.PerSeries)
		}
		if len(res.Attachments) != 0 {
			t.Errorf("attach=%v: Attachments = %+v", attach, res.Attachments)
		}
		report := res.Report()
		for _, want := range []string{
			"selected 5 of 6 rows: 4 projected new works across 1 catalogue series\n",
			"  total: 1 series, 4 works, 5 rows\n",
			"  of which standalone: 3 works in no series, 4 rows (--reported)\n",
		} {
			if !strings.Contains(report, want) {
				t.Errorf("attach=%v: report lacks %q:\n%s", attach, want, report)
			}
		}
	}
}

// A standalone alone, with nothing else in the batch, survives the fixpoint: a lane
// cycle can hold nothing but standalones.
func TestOnlyStandalonesSurviveTheBatch(t *testing.T) {
	rows := []string{standaloneRow("B0ALONE001", "A Lone Book", "us", "english", "Ada Mapmaker", "Bea Reader")}
	run := selectWith(t, seedSelectCatalogue(t), rows, SelectOptions{Reported: true, MaxPerSeries: 1, AttachEditions: true})
	if run.res.RowsSelected != 1 || run.res.ProjectedWorks != 1 || run.res.SeriesMatched != 0 || len(run.res.PerSeries) != 0 {
		t.Errorf("result = %+v", run.res)
	}
}

// End to end, as the bot runs it: select --reported --attach-editions, import with
// --existing-series-only --attach-editions, and validate. A series-less work is a
// clean tree (no metacheck rule requires a series), no series is created or changed
// by a standalone, and a volume of the held series still lands in it.
func TestReportedSelectionImportsCleanly(t *testing.T) {
	dataDir := seedSelectCatalogue(t)
	before := listSeries(t, dataDir)
	rows := []string{
		standaloneRow("B0ALONE001", "A Lone Book", "us", "english", "Cal Stranger", "Dee Voice"),
		selectRow("B0SELECT02", "Volume Two", "us", "english", seriesName, "2"),
		selectRow("B0UNHELD01", "Unrelated Book", "us", "english", "Some Other Series", "1"),
	}
	run, sum := importAsBot(t, dataDir, rows)
	if len(run.subset) != 2 {
		t.Fatalf("subset = %v", run.subset)
	}
	if sum.NewWorks != 2 || sum.NewSeries != 0 || sum.Attached != 0 || len(sum.Skips) != 0 {
		t.Errorf("summary = %+v, want 2 new works and no series", sum)
	}
	if got := listSeries(t, dataDir); !slices.Equal(got, before) {
		t.Errorf("series changed from %v to %v", before, got)
	}
	assertTreeValid(t, dataDir)
	lone := slices.Contains(listWorks(t, dataDir), "a-lone-book")
	var placed bool
	for _, s := range check.Load(dataDir).Catalog.Series {
		for _, m := range s.Works {
			if m.Work == "a-lone-book" {
				t.Errorf("the standalone is in series %s", s.ID)
			}
			placed = placed || m.Work == "volume-two"
		}
	}
	if !lone || !placed {
		t.Errorf("standalone landed = %v, volume placed = %v", lone, placed)
	}
}

// seedHeldBook is the selection catalogue plus a standalone it already holds.
func seedHeldBook(t *testing.T) string {
	t.Helper()
	dataDir := seedSelectCatalogue(t)
	seedTree(t, dataDir, map[string]string{
		"works/th/the-held-book/work.json": `{"authors":["ada-mapmaker"],"id":"the-held-book","language":"en","license":"CC0-1.0",` +
			`"sources":[{"type":"user"}],"title":"The Held Book"}`,
		"works/th/the-held-book/recordings/bea-reader-2024.json": `{"asin":[{"asin":"B0HELDBK01","region":"us"}],"id":"bea-reader-2024",` +
			`"language":"en","license":"CC0-1.0","narrators":["bea-reader"],"sources":[{"type":"user"}],"work":"the-held-book"}`,
	})
	return dataDir
}

// A standalone naming a book the catalogue holds mints no second work: the same
// title and author is another recording of that work, and a spelling the duplicate-identity
// guard recognises is refused, which the bot's --skipped worklist memoizes.
// GOVERNANCE.md states both.
func TestReportedStandaloneMeetsAHeldWork(t *testing.T) {
	importOne := func(t *testing.T, row string) Summary {
		t.Helper()
		dataDir := seedHeldBook(t)
		run, sum := importAsBot(t, dataDir, []string{row})
		if len(run.subset) != 1 {
			t.Fatalf("subset = %v, want the row kept", run.subset)
		}
		if got := listWorks(t, dataDir); !slices.Equal(got, []string{"the-held-book", "volume-one"}) {
			t.Errorf("works = %v, want no new work", got)
		}
		if sum.NewWorks != 0 || sum.NewSeries != 0 {
			t.Errorf("NewWorks %d, NewSeries %d, want neither", sum.NewWorks, sum.NewSeries)
		}
		assertTreeValid(t, dataDir)
		return sum
	}
	t.Run("same title and author is another recording", func(t *testing.T) {
		sum := importOne(t, standaloneRow("B0SAMEBK01", "The Held Book", "us", "english", "Ada Mapmaker", "Dee Voice"))
		if sum.NewRecordings != 1 || len(sum.Skips) != 0 {
			t.Errorf("NewRecordings %d, Skips %+v; want the narration as a recording of the-held-book", sum.NewRecordings, sum.Skips)
		}
	})
	t.Run("a decorated spelling is refused", func(t *testing.T) {
		sum := importOne(t, standaloneRow("B0DECOR001", "The Held Book: A LitRPG Adventure", "us", "english", "Ada Mapmaker", "Bea Reader"))
		if sum.NewRecordings != 0 || len(sum.Skips) != 1 || sum.Skips[0].ASIN != "B0DECOR001" || sum.Skips[0].Reason != RefusalIdentityDuplicate {
			t.Errorf("NewRecordings %d, Skips %+v; want one identity-duplicate", sum.NewRecordings, sum.Skips)
		}
	})
}

// What the guard does NOT catch, pinned so GOVERNANCE.md keeps saying only what is
// true: a subtitle it does not read as decoration mints a work beside the held one,
// as it does on every create path, and no advisory names it, since the census reads
// the same title key.
func TestReportedStandaloneWithAnUnreadSubtitleIsANewWork(t *testing.T) {
	dataDir := seedHeldBook(t)
	rows := []string{standaloneRow("B0SUBTIT01", "The Held Book: The Cartographer's Tale", "us", "english", "Ada Mapmaker", "Bea Reader")}
	_, sum := importAsBot(t, dataDir, rows)
	if sum.NewWorks != 1 || sum.NewSeries != 0 {
		t.Errorf("summary = %+v, want one new work and no series", sum)
	}
	assertTreeValid(t, dataDir)
	if warnings := check.Load(dataDir).Warnings; len(warnings) != 0 {
		t.Errorf("advisories = %+v; GOVERNANCE.md says none names this work", warnings)
	}
}

// The other thing it does not catch, for the same reason: a bare title naming several
// held volumes of a serial could be any of them or a third (identityMatch's ambiguity
// veto), so the row mints its own work and the series is left as it was. This one the
// census does name, which a sync-bot pull request shows in its comment only.
func TestReportedStandaloneNamingASerialIsANewWork(t *testing.T) {
	dataDir := seedSelectCatalogue(t)
	volume := func(n string) map[string]string {
		work := "wayward-book-" + n
		return map[string]string{
			"works/wa/" + work + "/work.json": `{"authors":["ada-mapmaker"],"id":"` + work + `","language":"en","license":"CC0-1.0",` +
				`"sources":[{"type":"user"}],"title":"Wayward"}`,
			"works/wa/" + work + "/recordings/bea-reader-2024.json": `{"asin":[{"asin":"B0WAYWARD` + n + `","region":"us"}],"id":"bea-reader-2024",` +
				`"language":"en","license":"CC0-1.0","narrators":["bea-reader"],"sources":[{"type":"user"}],"work":"` + work + `"}`,
		}
	}
	seedTree(t, dataDir, volume("1"))
	seedTree(t, dataDir, volume("2"))
	seedTree(t, dataDir, map[string]string{
		"series/wa/wayward.json": `{"id":"wayward","license":"CC0-1.0","name":"Wayward","sources":[{"type":"user"}],` +
			`"works":[{"position":"1","work":"wayward-book-1"},{"position":"2","work":"wayward-book-2"}]}`,
	})
	rows := []string{standaloneRow("B0WAYWARD3", "Wayward", "us", "english", "Ada Mapmaker", "Bea Reader")}
	_, sum := importAsBot(t, dataDir, rows)
	if sum.NewWorks != 1 || sum.NewSeries != 0 || len(sum.Skips) != 0 {
		t.Errorf("summary = %+v, want one new work, no series and no refusal", sum)
	}
	assertTreeValid(t, dataDir)
	named := slices.ContainsFunc(check.Load(dataDir).Warnings, func(p check.Problem) bool {
		return check.AdvisoryClass(p) == "identity-equal-works" && strings.Contains(p.Msg, `"wayward"`)
	})
	if !named {
		t.Error("no identity-equal-works advisory names the new work; GOVERNANCE.md says one does")
	}
}
