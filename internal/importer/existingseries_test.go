package importer

import (
	"reflect"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/testpack"
)

// Options.ExistingSeriesOnly: a run that may not FOUND a series drops the claim
// that would, and keeps everything else the row states. The shape every test here
// is measured against is the one that stopped the series-completion sync bot on
// 2026-09-26: libex row B0GDJN7NZQ "Chute Libre", the French edition of David
// Freed's eighth Cordell Logan mystery, claims BOTH "The Cordell Logan Mysteries"
// #8 (catalogued) and "Une Enquête de Cordell Logan" #1 (not).

// multiSeriesRow is a libex row claiming every (name, position) pair in series.
func multiSeriesRow(asin, title, author, narrator, language string, series ...[2]string) string {
	row := `{"asin":"` + asin + `","title":"` + title + `","region":"us","language":"` + language + `",` +
		`"bookFormat":"unabridged","lengthMinutes":420,"authors":[{"name":"` + author + `"}],` +
		`"narrators":[{"name":"` + narrator + `"}]`
	var refs []string
	for _, s := range series {
		refs = append(refs, `{"name":"`+s[0]+`","position":"`+s[1]+`"}`)
	}
	if len(refs) > 0 {
		row += `,"series":[` + strings.Join(refs, ",") + `]`
	}
	return row + "}"
}

// cordellLoganTree is David Freed's catalogued series, volumes 1-2, in the
// French edition the Chute Libre row belongs to. The real catalogue holds the
// ENGLISH series, which the language rule closes to that French row
// (TestExistingSeriesOnlyDropsACrossLanguageClaim); this fixture keeps the
// completion the sync bot was stopped on, in the row's own language.
func cordellLoganTree(t *testing.T) string {
	t.Helper()
	return cordellLoganTreeIn(t, "fr")
}

// cordellLoganTreeIn is cordellLoganTree with its works in language lang.
func cordellLoganTreeIn(t *testing.T, lang string) string {
	t.Helper()
	files := map[string]string{
		"people/da/david-freed.json": testpack.PersonJSON(t, "david-freed", "David Freed"),
		"series/th/the-cordell-logan-mysteries.json": testpack.SeriesJSON(t, "the-cordell-logan-mysteries",
			"The Cordell Logan Mysteries", "flat-spin@1", "fangs-out@2"),
	}
	for _, w := range []string{"flat-spin", "fangs-out"} {
		files["works/"+shard(w)+"/"+w+"/work.json"] = testpack.WorkJSON(t, w, w, testpack.WithAuthors("david-freed"), testpack.WithLanguage(lang))
		files["works/"+shard(w)+"/"+w+"/recordings/r1.json"] = testpack.RecJSON(t, "r1", w, testpack.WithNarrators("bea-reader"))
	}
	return seedTombstoneTree(t, files, nil)
}

var chuteLibre = multiSeriesRow("B0GDJN7NZQ", "Chute Libre", "David Freed", "Bea Reader", "french",
	[2]string{"The Cordell Logan Mysteries", "8"}, [2]string{"Une Enquête de Cordell Logan", "1"})

func runLibexWith(t *testing.T, dataDir string, opts Options, rows ...string) Summary {
	t.Helper()
	opts.DataDir = dataDir
	if opts.ImportDate == "" {
		opts.ImportDate = testImportDate
	}
	sum, err := RunLibex(writeBooks(t, strings.Join(rows, "\n")+"\n"), opts)
	if err != nil {
		t.Fatalf("import run: %v", err)
	}
	return sum
}

// The Chute Libre shape: the catalogued claim is placed, the founding one is
// dropped and named, and no series is created.
func TestExistingSeriesOnlyDropsTheFoundingClaim(t *testing.T) {
	dataDir := cordellLoganTree(t)
	sum := runLibexWith(t, dataDir, Options{ExistingSeriesOnly: true}, chuteLibre)

	if sum.NewWorks != 1 || sum.NewSeries != 0 || sum.SeriesClaimsDropped != 1 {
		t.Errorf("NewWorks/NewSeries/SeriesClaimsDropped = %d/%d/%d, want 1/0/1", sum.NewWorks, sum.NewSeries, sum.SeriesClaimsDropped)
	}
	want := map[string]string{"flat-spin": "1", "fangs-out": "2", "chute-libre": "8"}
	if got := seriesWorks(t, dataDir, "the-cordell-logan-mysteries"); !reflect.DeepEqual(got, want) {
		t.Errorf("the-cordell-logan-mysteries = %v, want %v", got, want)
	}
	if entryExists(t, dataDir, seriesAddr("une-enquete-de-cordell-logan")) {
		t.Error("a series was founded under ExistingSeriesOnly")
	}
	if n := countWarnings(sum.Warnings, "1 series claims dropped: no catalogued series fits the claim"); n != 1 {
		t.Errorf("want ONE aggregated drop warning, got %d: %v", n, sum.Warnings)
	}
	if !hasWarning(sum.Warnings, `"Une Enquête de Cordell Logan" (B0GDJN7NZQ)`) {
		t.Errorf("the drop warning does not name the series and the row: %v", sum.Warnings)
	}
	if sum.RunLevelWarnings == 0 || !strings.Contains(strings.Join(sum.Warnings[:sum.RunLevelWarnings], "\n"), "series claims dropped") {
		t.Errorf("the drop warning is not run-level: %v", sum.Warnings)
	}
	assertTreeValid(t, dataDir)
}

// The option is off by default, and off it is today's behaviour: the second
// claim founds its series.
func TestExistingSeriesOnlyOffFoundsTheSeries(t *testing.T) {
	dataDir := cordellLoganTree(t)
	sum := runLibexWith(t, dataDir, Options{}, chuteLibre)

	if sum.NewSeries != 1 || sum.SeriesClaimsDropped != 0 {
		t.Errorf("NewSeries/SeriesClaimsDropped = %d/%d, want 1/0", sum.NewSeries, sum.SeriesClaimsDropped)
	}
	if got := seriesWorks(t, dataDir, "une-enquete-de-cordell-logan"); got["chute-libre"] != "1" {
		t.Errorf("une-enquete-de-cordell-logan = %v, want Chute Libre at 1", got)
	}
	if got := seriesWorks(t, dataDir, "the-cordell-logan-mysteries"); got["chute-libre"] != "8" {
		t.Errorf("the-cordell-logan-mysteries = %v, want Chute Libre at 8", got)
	}
	if hasWarning(sum.Warnings, "series claims dropped") {
		t.Errorf("a run without the option reported a drop: %v", sum.Warnings)
	}
	assertTreeValid(t, dataDir)
}

// The REAL Chute Libre shape: the catalogued "The Cordell Logan Mysteries" is
// English, and the French edition's claim to #8 is a cross-language join the
// language rule closes (seriesresolve.go's languageCloses). Under the option both
// claims are dropped - the catalogued one naming why - and without it the
// claim founds the French row's own series one step down the chain.
func TestExistingSeriesOnlyDropsACrossLanguageClaim(t *testing.T) {
	dataDir := cordellLoganTreeIn(t, "en")
	sum := runLibexWith(t, dataDir, Options{ExistingSeriesOnly: true}, chuteLibre)
	if sum.NewWorks != 1 || sum.NewSeries != 0 || sum.SeriesClaimsDropped != 2 {
		t.Errorf("NewWorks/NewSeries/SeriesClaimsDropped = %d/%d/%d, want 1/0/2", sum.NewWorks, sum.NewSeries, sum.SeriesClaimsDropped)
	}
	if got := seriesWorks(t, dataDir, "the-cordell-logan-mysteries"); got["chute-libre"] != "" {
		t.Errorf("the French edition joined the English series: %v", got)
	}
	if !hasWarning(sum.Warnings, `"The Cordell Logan Mysteries" (B0GDJN7NZQ) [the-cordell-logan-mysteries is in another language (en)]`) {
		t.Errorf("the drop does not say the series is in another language: %v", sum.Warnings)
	}
	assertTreeValid(t, dataDir)

	dataDir = cordellLoganTreeIn(t, "en")
	sum = runLibexWith(t, dataDir, Options{}, chuteLibre)
	if got := seriesWorks(t, dataDir, "the-cordell-logan-mysteries-2"); got["chute-libre"] != "8" {
		t.Errorf("the-cordell-logan-mysteries-2 = %v, want the French edition's own series with Chute Libre at 8", got)
	}
	if got := seriesWorks(t, dataDir, "the-cordell-logan-mysteries"); len(got) != 2 {
		t.Errorf("the English series changed: %v", got)
	}
	if !hasWarning(sum.Warnings, `series "The Cordell Logan Mysteries": the-cordell-logan-mysteries is in another language (en); created "the-cordell-logan-mysteries-2"`) {
		t.Errorf("the mint does not say why: %v", sum.Warnings)
	}
	assertTreeValid(t, dataDir)
}

// A row whose ONLY claim would found a series still imports - as a work in no
// series, the ordinary outcome for a row with no series - and a second row of the
// same batch, which would have joined the series the first founded, is dropped
// too: both claims are counted, under one warning.
func TestExistingSeriesOnlyKeepsARowWithNoCatalogueSeries(t *testing.T) {
	dataDir := seedTombstoneTree(t, nil, nil)
	sum := runLibexWith(t, dataDir, Options{ExistingSeriesOnly: true},
		tombRow("B0NOSER001", "Harbour Lights", "Ada Mapmaker", "Bea Reader", 300, "Harbour Saga", "1"),
		tombRow("B0NOSER002", "Harbour Tides", "Ada Mapmaker", "Bea Reader", 300, "Harbour Saga", "2"))

	if sum.NewWorks != 2 || sum.NewRecordings != 2 || sum.NewSeries != 0 || sum.SeriesClaimsDropped != 2 {
		t.Errorf("NewWorks/NewRecordings/NewSeries/SeriesClaimsDropped = %d/%d/%d/%d, want 2/2/0/2",
			sum.NewWorks, sum.NewRecordings, sum.NewSeries, sum.SeriesClaimsDropped)
	}
	for _, w := range []string{"harbour-lights", "harbour-tides"} {
		if !entryExists(t, dataDir, workAddr(w)) {
			t.Errorf("work %s was not imported", w)
		}
	}
	if entryExists(t, dataDir, seriesAddr("harbour-saga")) {
		t.Error("a series was founded under ExistingSeriesOnly")
	}
	if n := countWarnings(sum.Warnings, "2 series claims dropped"); n != 1 {
		t.Errorf("want ONE aggregated line for both claims, got %d: %v", n, sum.Warnings)
	}
	assertTreeValid(t, dataDir)
}

// A same-named catalogued series CLOSED to the row's authors is not the row's
// series, and without the option the row founds `<slug>-2`. With it the claim is
// dropped instead, while the owning author's own row still joins - and the batch
// resolves the same whatever order its rows arrive in.
func TestExistingSeriesOnlyDropsAClosedSeriesStep(t *testing.T) {
	rows := []string{
		tombRow("B0CAMPB004", "Valiant", "Jack Campbell", "Bea Reader", 600, "Lost Fleet", "4"),
		tombRow("B0HAWKE004", "Renegade", "Sarah Hawke", "Bea Reader", 600, "Lost Fleet", "4"),
	}
	type outcome struct {
		hawke              map[string]string
		stepped, valiant   bool
		newSeries, dropped int
	}
	run := func(order ...int) outcome {
		dataDir := lostFleetTree(t, nil)
		var in []string
		for _, i := range order {
			in = append(in, rows[i])
		}
		sum := runLibexWith(t, dataDir, Options{ExistingSeriesOnly: true}, in...)
		assertTreeValid(t, dataDir)
		if !hasWarning(sum.Warnings, `"Lost Fleet" (B0CAMPB004) [lost-fleet belongs to other authors]`) {
			t.Errorf("the drop warning does not name Campbell's row and the series it did not fit: %v", sum.Warnings)
		}
		return outcome{
			hawke:     seriesWorks(t, dataDir, "lost-fleet"),
			stepped:   entryExists(t, dataDir, seriesAddr("lost-fleet-2")),
			valiant:   entryExists(t, dataDir, workAddr("valiant")),
			newSeries: sum.NewSeries,
			dropped:   sum.SeriesClaimsDropped,
		}
	}
	want := outcome{
		hawke:   map[string]string{"incursion": "1", "insurrection": "2", "invasion": "3", "renegade": "4"},
		valiant: true, newSeries: 0, dropped: 1,
	}
	for name, got := range map[string]outcome{"forward": run(0, 1), "reverse": run(1, 0)} {
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s order: %+v, want %+v", name, got, want)
		}
	}
}

// A claim the run will drop is dropped BEFORE anything acts on it: no live
// position lookup is spent on it, no "placed at" line reports a placement that
// never happens, and it is counted once, as a dropped claim - not as a missing
// position.
func TestExistingSeriesOnlyDropsBeforeLookupAndArbitration(t *testing.T) {
	lookup := warhammerLookup(SeriesPosition{Name: "Warhammer 40,000", Position: "3"})
	sum, dataDir := runSeriesBooks(t, warhammerExport, Options{SeriesLookup: lookup, ExistingSeriesOnly: true})
	if lookup.calls != 0 {
		t.Errorf("lookups = %d, want 0 for a claim the run drops", lookup.calls)
	}
	if sum.SeriesClaimsDropped != 1 || sum.NewSeries != 0 {
		t.Errorf("SeriesClaimsDropped/NewSeries = %d/%d, want 1/0", sum.SeriesClaimsDropped, sum.NewSeries)
	}
	for _, s := range []string{"missing or invalid position", "taken from libex"} {
		if line, found := warningWith(sum, s); found {
			t.Errorf("a dropped claim still reported %q: %q", s, line)
		}
	}
	if entryExists(t, dataDir, seriesAddr("warhammer-40-000")) {
		t.Error("a series was founded under ExistingSeriesOnly")
	}

	sum, dataDir = runSeriesBooks(t, towerboundExport("Towerbound, Book 6", "8"), Options{ExistingSeriesOnly: true})
	if line, found := warningWith(sum, "disagrees with the title"); found {
		t.Errorf("a dropped claim still reported a title arbitration: %q", line)
	}
	if sum.SeriesClaimsDropped != 1 || sum.NewSeries != 0 {
		t.Errorf("SeriesClaimsDropped/NewSeries = %d/%d, want 1/0", sum.SeriesClaimsDropped, sum.NewSeries)
	}
	if entryExists(t, dataDir, seriesAddr("towerbound")) {
		t.Error("a series was founded under ExistingSeriesOnly")
	}
}
