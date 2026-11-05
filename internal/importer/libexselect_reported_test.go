package importer

import (
	"encoding/json"
	"errors"
	"fmt"
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
// title and author is another recording of that work, untouched by the standalone
// guard, and a decorated spelling is refused by the selection as identity-duplicate,
// which the bot's --refusals worklist memoizes. GOVERNANCE.md states both.
func TestReportedStandaloneMeetsAHeldWork(t *testing.T) {
	t.Run("same title and author is another recording", func(t *testing.T) {
		dataDir := seedHeldBook(t)
		run, sum := importAsBot(t, dataDir, []string{standaloneRow("B0SAMEBK01", "The Held Book", "us", "english", "Ada Mapmaker", "Dee Voice")})
		if len(run.subset) != 1 || len(run.refusals) != 0 {
			t.Fatalf("subset %v, refusals %+v; want the row kept", run.subset, run.refusals)
		}
		if got := listWorks(t, dataDir); !slices.Equal(got, []string{"the-held-book", "volume-one"}) {
			t.Errorf("works = %v, want no new work", got)
		}
		if sum.NewWorks != 0 || sum.NewSeries != 0 || sum.NewRecordings != 1 || len(sum.Skips) != 0 {
			t.Errorf("summary = %+v; want the narration as a recording of the-held-book", sum)
		}
		assertTreeValid(t, dataDir)
	})
	t.Run("a decorated spelling is refused by the selection", func(t *testing.T) {
		run := selectWith(t, seedHeldBook(t), []string{standaloneRow("B0DECOR001", "The Held Book: A LitRPG Adventure", "us", "english", "Ada Mapmaker", "Bea Reader")},
			SelectOptions{Reported: true, AttachEditions: true})
		assertRefusedAsDuplicate(t, run, "B0DECOR001")
	})
}

// assertRefusedAsDuplicate checks a one-row selection refused its row as
// identity-duplicate, kept nothing and says so in the report.
func assertRefusedAsDuplicate(t *testing.T, run selectRun, asin string) {
	t.Helper()
	if len(run.subset) != 0 || len(run.refusals) != 1 || run.refusals[0].ASIN != asin || run.refusals[0].Reason != RefusalIdentityDuplicate {
		t.Errorf("subset %v, refusals %+v; want %s refused as identity-duplicate", run.subset, run.refusals, asin)
	}
	if run.res.StandaloneRows != 0 || run.res.ProjectedWorks != 0 {
		t.Errorf("StandaloneRows %d, ProjectedWorks %d; a refused row projects nothing", run.res.StandaloneRows, run.res.ProjectedWorks)
	}
	if want := "  the catalogue holds the book under another title 1 (--reported)\n"; !strings.Contains(run.res.Report(), want) {
		t.Errorf("report lacks %q:\n%s", want, run.res.Report())
	}
}

// heldWork seeds one held work with a single recording (the ASIN B0HELD plus a
// suffix), by authors given as person slugs, and an optional series membership.
func heldWork(t *testing.T, dataDir, slug, title, lang string, authors []string, asinSuffix string) {
	t.Helper()
	quoted := make([]string, len(authors))
	for i, a := range authors {
		quoted[i] = `"` + a + `"`
	}
	dir := "works/" + slug[:2] + "/" + slug
	seedTree(t, dataDir, map[string]string{
		dir + "/work.json": `{"authors":[` + strings.Join(quoted, ",") + `],"id":"` + slug + `","language":"` + lang +
			`","license":"CC0-1.0","sources":[{"type":"user"}],"title":"` + title + `"}`,
		dir + "/recordings/bea-reader-2020.json": `{"asin":[{"asin":"B0HELD` + asinSuffix + `","region":"us"}],"id":"bea-reader-2020",` +
			`"language":"` + lang + `","license":"CC0-1.0","narrators":["bea-reader"],"sources":[{"type":"user"}],"work":"` + slug + `"}`,
	})
}

// TestReportedStandaloneGuard is decision 15's rule, one held work and one row a
// case, shaped on the 2026-11-05 dry run's duplicates: a series-less row that
// would CREATE a work is refused as identity-duplicate when a held work shares an
// author and the identity keys of the full titles, or of one side's main title
// against the other's full title, are equal.
func TestReportedStandaloneGuard(t *testing.T) {
	const ada, cal = "Ada Mapmaker", "Cal Stranger"
	type held struct {
		slug, title, lang string
		authors           []string
	}
	cases := []struct {
		name   string
		held   []held
		series string // a series JSON holding the first held work, or ""
		row    string
		refuse bool
	}{
		{name: "author subset (Micro)", refuse: true,
			held: []held{{"micro", "Micro", "en", []string{"ada-mapmaker", "cal-stranger"}}},
			row:  standaloneRow("B0MICRO001", "Micro", "us", "english", ada, "Dee Voice")},
		{name: "subtitle on the held side, in a series (The New World)", refuse: true,
			held: []held{{"the-new-world-prequel-to-the-chaos-walking-trilogy", "The New World: Prequel to the Chaos Walking Trilogy", "en", []string{"ada-mapmaker"}}},
			series: `{"id":"chaos-walking","license":"CC0-1.0","name":"Chaos Walking","sources":[{"type":"user"}],` +
				`"works":[{"position":"0.5","work":"the-new-world-prequel-to-the-chaos-walking-trilogy"}]}`,
			row: standaloneRow("B0NEWWLD01", "The New World", "de", "english", ada, "Dee Voice")},
		{name: "subtitle on the held side (Ford County)", refuse: true,
			held: []held{{"ford-county-stories", "Ford County: Stories", "en", []string{"ada-mapmaker"}}},
			row:  standaloneRow("B0FORDCO01", "Ford County", "us", "english", ada, "Dee Voice")},
		{name: "a foreword writer held as a second author (Infinite Jest)", refuse: true,
			held: []held{{"infinite-jest", "Infinite Jest", "en", []string{"ada-mapmaker", "cal-stranger"}}},
			row:  standaloneRow("B0INFJEST1", "Infinite Jest", "us", "english", ada, "Dee Voice")},
		{name: "subtitle on the row side", refuse: true,
			held: []held{{"the-held-book", "The Held Book", "en", []string{"ada-mapmaker"}}},
			row:  standaloneRow("B0SUBTIT01", "The Held Book: The Cartographer's Tale", "us", "english", ada, "Bea Reader")},
		{name: "a distinct book sharing a main title (accepted false refusal)", refuse: true,
			held: []held{{"empires-of-eve-volume-2", "Empires of EVE: Volume 2", "en", []string{"ada-mapmaker"}}},
			row:  standaloneRow("B0EMPIRE01", "Empires of EVE", "us", "english", ada, "Dee Voice")},
		{name: "a bare title naming several held volumes", refuse: true,
			held: []held{
				{"wayward-book-1", "Wayward", "en", []string{"ada-mapmaker"}},
				{"wayward-book-2", "Wayward", "en", []string{"ada-mapmaker"}},
			},
			row: standaloneRow("B0WAYWARD3", "Wayward", "us", "english", ada, "Bea Reader")},

		{name: "same title by a different author is kept",
			held: []held{{"micro", "Micro", "en", []string{"ada-mapmaker"}}},
			row:  standaloneRow("B0MICRO002", "Micro", "us", "english", cal, "Dee Voice")},
		{name: "another language is kept",
			held: []held{{"micro-french-version", "Micro [French Version]", "fr", []string{"ada-mapmaker"}}},
			row:  standaloneRow("B0MICRO003", "Micro", "us", "english", ada, "Dee Voice")},
		{name: "two main titles are not compared",
			held: []held{{"dune-house-atreides", "Dune: House Atreides", "en", []string{"ada-mapmaker"}}},
			row:  standaloneRow("B0DUNEHH01", "Dune: House Harkonnen", "us", "english", ada, "Dee Voice")},
		{name: "a row stating a volume the held title does not is kept",
			held: []held{{"circus-of-the-dead", "Circus of the Dead", "en", []string{"ada-mapmaker"}}},
			row:  standaloneRow("B0CIRCUS02", "Circus of the Dead, Book 2", "us", "english", ada, "Dee Voice")},
		{name: "a row stating another volume is kept",
			held: []held{{"circus-of-the-dead-book-1", "Circus of the Dead, Book 1", "en", []string{"ada-mapmaker"}}},
			row:  standaloneRow("B0CIRCUS03", "Circus of the Dead, Book 2", "us", "english", ada, "Dee Voice")},
		{name: "a title that only starts like a held one is kept",
			held: []held{{"the-barefoot-investor-for-families", "The Barefoot Investor for Families", "en", []string{"ada-mapmaker"}}},
			row:  standaloneRow("B0BAREFT01", "The Barefoot Investor", "us", "english", ada, "Dee Voice")},
	}
	for _, tc := range cases {
		for _, attach := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s (attach=%v)", tc.name, attach), func(t *testing.T) {
				dataDir := seedSelectCatalogue(t)
				for i, h := range tc.held {
					heldWork(t, dataDir, h.slug, h.title, h.lang, h.authors, fmt.Sprintf("%04d", i))
				}
				if tc.series != "" {
					seedTree(t, dataDir, map[string]string{"series/ch/chaos-walking.json": tc.series})
				}
				run := selectWith(t, dataDir, []string{tc.row}, SelectOptions{Reported: true, AttachEditions: attach})
				if tc.refuse {
					var row struct{ ASIN string }
					if err := json.Unmarshal([]byte(tc.row), &row); err != nil {
						t.Fatal(err)
					}
					assertRefusedAsDuplicate(t, run, row.ASIN)
					return
				}
				if len(run.subset) != 1 || len(run.refusals) != 0 || run.res.StandaloneRows != 1 {
					t.Errorf("subset %v, refusals %+v, StandaloneRows %d; want the row kept as a standalone",
						run.subset, run.refusals, run.res.StandaloneRows)
				}
			})
		}
	}
}

// A refused duplicate leaves the rest of the batch alone, and is judged against
// held works only: two regional rows of one NEW book are siblings of one work, not
// duplicates of each other, and both are kept beside a refused one.
func TestReportedGuardReadsHeldWorksOnly(t *testing.T) {
	dataDir := seedSelectCatalogue(t)
	heldWork(t, dataDir, "micro", "Micro", "en", []string{"ada-mapmaker", "cal-stranger"}, "0001")
	rows := []string{
		standaloneRow("B0MICRO001", "Micro", "us", "english", "Ada Mapmaker", "Dee Voice"),
		standaloneRow("B0NEWBK01A", "A New Book", "us", "english", "Ada Mapmaker", "Dee Voice"),
		standaloneRow("B0NEWBK01B", "A New Book", "gb", "english", "Ada Mapmaker", "Dee Voice"),
		selectRow("B0SELECT02", "Volume Two", "us", "english", seriesName, "2"),
	}
	run := selectWith(t, dataDir, rows, SelectOptions{Reported: true, AttachEditions: true})
	if len(run.subset) != 3 || len(run.refusals) != 1 || run.refusals[0].ASIN != "B0MICRO001" || run.refusals[0].Reason != RefusalIdentityDuplicate {
		t.Errorf("subset %v, refusals %+v; want Micro alone refused", run.subset, run.refusals)
	}
	if run.res.StandaloneRows != 2 || run.res.StandaloneWorks != 1 || run.res.ProjectedWorks != 2 {
		t.Errorf("standalone rows/works %d/%d, projected %d; want 2/1 and 2", run.res.StandaloneRows, run.res.StandaloneWorks, run.res.ProjectedWorks)
	}
}

// Without --reported the guard never runs: a series-less row is refused as
// no-catalogue-series, as it always was, and the report has no identity line. A row
// claiming a series is never judged by it either, flag or no flag, even when its
// subtitle would name a held work.
func TestReportedGuardNeedsTheFlag(t *testing.T) {
	dataDir := seedSelectCatalogue(t)
	heldWork(t, dataDir, "micro", "Micro", "en", []string{"ada-mapmaker", "cal-stranger"}, "0001")
	rows := []string{
		standaloneRow("B0MICRO001", "Micro", "us", "english", "Ada Mapmaker", "Dee Voice"),
		selectRow("B0VOLONE02", "Volume One: The Return", "us", "english", seriesName, "2"),
	}
	off := selectWith(t, dataDir, rows, SelectOptions{AttachEditions: true})
	if len(off.refusals) != 1 || off.refusals[0].ASIN != "B0MICRO001" || off.refusals[0].Reason != RefusalNoCatalogueSeries {
		t.Errorf("refusals = %+v, want Micro refused as no-catalogue-series", off.refusals)
	}
	if strings.Contains(off.res.Report(), reasonIdentityDuplicate.report) {
		t.Errorf("a run without --reported reports the guard:\n%s", off.res.Report())
	}
	on := selectWith(t, dataDir, rows, SelectOptions{Reported: true, AttachEditions: true})
	if !slices.Contains(on.subset, rows[1]) || !slices.Contains(off.subset, rows[1]) {
		t.Errorf("the completion row was dropped: off %v, on %v", off.subset, on.subset)
	}
}

// End to end, as the bot runs it: the guard's refusals never reach the import, a
// kept standalone still imports as a work in no series, and the tree is valid.
func TestReportedGuardEndToEnd(t *testing.T) {
	dataDir := seedSelectCatalogue(t)
	heldWork(t, dataDir, "ford-county-stories", "Ford County: Stories", "en", []string{"ada-mapmaker"}, "0001")
	rows := []string{
		standaloneRow("B0FORDCO01", "Ford County", "us", "english", "Ada Mapmaker", "Dee Voice"),
		standaloneRow("B0ALONE001", "A Lone Book", "us", "english", "Ada Mapmaker", "Dee Voice"),
	}
	run, sum := importAsBot(t, dataDir, rows)
	if len(run.subset) != 1 || len(run.refusals) != 1 || run.refusals[0].Reason != RefusalIdentityDuplicate {
		t.Fatalf("subset %v, refusals %+v", run.subset, run.refusals)
	}
	if sum.NewWorks != 1 || sum.NewSeries != 0 || len(sum.Skips) != 0 {
		t.Errorf("summary = %+v, want one new work", sum)
	}
	if got := listWorks(t, dataDir); !slices.Equal(got, []string{"a-lone-book", "ford-county-stories", "volume-one"}) {
		t.Errorf("works = %v", got)
	}
	assertTreeValid(t, dataDir)
}
