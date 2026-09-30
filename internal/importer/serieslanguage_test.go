package importer

import (
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/testpack"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// Series resolution is LANGUAGE-aware (seriesresolve.go's languageCloses): a
// same-named series whose derived language is known and is not the row's is
// closed to the row, exactly as a series its authors do not fit is. The shape
// every test here starts from is the one the September sync tranches grew:
// Gregg Hurwitz's English "Last Man Standing" joined the German "Orphan X" as
// its eleventh volume, because the two share a name and an author.

// langRow is tombRow in the libex language word lang.
func langRow(asin, title, author, lang, series, pos string) string {
	return strings.Replace(tombRow(asin, title, author, "Bea Reader", 600, series, pos),
		`"language":"english"`, `"language":"`+lang+`"`, 1)
}

// orphanXTree is Gregg Hurwitz's German "Orphan X", volumes 1-2.
func orphanXTree(t *testing.T) string {
	t.Helper()
	files := map[string]string{
		"people/gr/gregg-hurwitz.json": testpack.PersonJSON(t, "gregg-hurwitz", "Gregg Hurwitz"),
		"series/or/orphan-x.json":      testpack.SeriesJSON(t, "orphan-x", "Orphan X", "orphan-x-der-auftrag@1", "der-unbekannte@2"),
	}
	for _, w := range []string{"orphan-x-der-auftrag", "der-unbekannte"} {
		files["works/"+shard(w)+"/"+w+"/work.json"] = testpack.WorkJSON(t, w, w,
			testpack.WithAuthors("gregg-hurwitz"), testpack.WithLanguage("de"))
		files["works/"+shard(w)+"/"+w+"/recordings/r1.json"] = testpack.RecJSON(t, "r1", w, testpack.WithNarrators("bea-reader"))
	}
	return seedTombstoneTree(t, files, nil)
}

// The Orphan X shape: the author's own English volume does not join the German
// series - it founds the English one at the next chain slug and says why - while
// the next German volume still joins the German series.
func TestAnotherLanguagesRowDoesNotJoinASeries(t *testing.T) {
	dataDir := orphanXTree(t)
	sum := runLibexOver(t, dataDir,
		langRow("B0ORPHANEN", "Last Man Standing", "Gregg Hurwitz", "english", "Orphan X", "11"),
		langRow("B0ORPHANDE", "Der Unsichtbare", "Gregg Hurwitz", "german", "Orphan X", "3"))

	if got, want := seriesWorks(t, dataDir, "orphan-x"),
		map[string]string{"orphan-x-der-auftrag": "1", "der-unbekannte": "2", "der-unsichtbare": "3"}; !reflect.DeepEqual(got, want) {
		t.Errorf("orphan-x = %v, want %v", got, want)
	}
	if got := seriesWorks(t, dataDir, "orphan-x-2"); !reflect.DeepEqual(got, map[string]string{"last-man-standing": "11"}) {
		t.Errorf("orphan-x-2 = %v, want the English volume alone", got)
	}
	if sum.NewSeries != 1 {
		t.Errorf("NewSeries = %d, want 1", sum.NewSeries)
	}
	if !hasWarning(sum.Warnings, `series "Orphan X": orphan-x is in another language (de); created "orphan-x-2"`) {
		t.Errorf("the mint does not say why: %v", sum.Warnings)
	}
	assertTreeValid(t, dataDir)

	// Under ExistingSeriesOnly the English claim is dropped, naming why, and the
	// German one is placed.
	dataDir = orphanXTree(t)
	sum = runLibexWith(t, dataDir, Options{ExistingSeriesOnly: true},
		langRow("B0ORPHANEN", "Last Man Standing", "Gregg Hurwitz", "english", "Orphan X", "11"),
		langRow("B0ORPHANDE", "Der Unsichtbare", "Gregg Hurwitz", "german", "Orphan X", "3"))
	if sum.NewSeries != 0 || sum.SeriesClaimsDropped != 1 || seriesWorks(t, dataDir, "orphan-x")["der-unsichtbare"] != "3" {
		t.Errorf("NewSeries/SeriesClaimsDropped = %d/%d, orphan-x = %v; want 0/1 with the German volume placed",
			sum.NewSeries, sum.SeriesClaimsDropped, seriesWorks(t, dataDir, "orphan-x"))
	}
	if !hasWarning(sum.Warnings, `"Orphan X" (B0ORPHANEN) [orphan-x is in another language (de)]`) {
		t.Errorf("the drop does not say why: %v", sum.Warnings)
	}
	assertTreeValid(t, dataDir)
}

// A batch cannot flip a catalogued series' language by arriving: the
// catalogue's one-volume English "Lost Fleet" is OPEN to its own author by the
// author rule, but Hawke's four German volumes are judged against the
// snapshot's language, so they found ONE German series together - one slug,
// not one each - and the English series is untouched.
func TestABatchOfAnotherLanguageFoundsOneSeries(t *testing.T) {
	dataDir := seedTombstoneTree(t, map[string]string{
		"people/sa/sarah-hawke.json":            testpack.PersonJSON(t, "sarah-hawke", "Sarah Hawke"),
		"works/in/incursion/work.json":          testpack.WorkJSON(t, "incursion", "Incursion", testpack.WithAuthors("sarah-hawke")),
		"works/in/incursion/recordings/r1.json": testpack.RecJSON(t, "r1", "incursion", testpack.WithNarrators("bea-reader")),
		"series/lo/lost-fleet.json":             testpack.SeriesJSON(t, "lost-fleet", "Lost Fleet", "incursion@1"),
	}, nil)
	var rows []string
	for i, title := range []string{"Aufstand", "Invasion", "Abtruennig", "Vergeltung"} {
		rows = append(rows, langRow("B0HAWKDE0"+strconv.Itoa(i), title, "Sarah Hawke", "german", "Lost Fleet", strconv.Itoa(2+i)))
	}
	sum := runLibexOver(t, dataDir, rows...)

	if got := seriesWorks(t, dataDir, "lost-fleet"); !reflect.DeepEqual(got, map[string]string{"incursion": "1"}) {
		t.Errorf("lost-fleet = %v, want the English volume alone", got)
	}
	want := map[string]string{"aufstand": "2", "invasion": "3", "abtruennig": "4", "vergeltung": "5"}
	if got := seriesWorks(t, dataDir, "lost-fleet-2"); !reflect.DeepEqual(got, want) {
		t.Errorf("lost-fleet-2 = %v, want %v", got, want)
	}
	if sum.NewSeries != 1 || entryExists(t, dataDir, seriesAddr("lost-fleet-3")) {
		t.Errorf("NewSeries = %d, want ONE German series", sum.NewSeries)
	}
	assertTreeValid(t, dataDir)
}

// Order independence holds with mixed languages: Hawke's English and German
// volumes and Campbell's English ones, in one batch with nothing catalogued,
// land in the same three series whatever order the rows arrive in - each series
// one language, each author-and-language group in one series.
func TestSeriesLanguageResolutionIsOrderIndependent(t *testing.T) {
	rows := []string{
		langRow("B0HAWKE001", "Incursion", "Sarah Hawke", "english", "Lost Fleet", "1"),
		langRow("B0HAWKE002", "Insurrection", "Sarah Hawke", "english", "Lost Fleet", "2"),
		langRow("B0HAWKD001", "Einfall", "Sarah Hawke", "german", "Lost Fleet", "1"),
		langRow("B0HAWKD002", "Aufstand", "Sarah Hawke", "german", "Lost Fleet", "2"),
		langRow("B0CAMPB004", "Valiant", "Jack Campbell", "english", "Lost Fleet", "4"),
		langRow("B0CAMPB005", "Relentless", "Jack Campbell", "english", "Lost Fleet", "5"),
	}
	membership := func(order []int) map[string]map[string]string {
		dataDir := seedTombstoneTree(t, nil, nil)
		var in []string
		for _, i := range order {
			in = append(in, rows[i])
		}
		runLibexOver(t, dataDir, in...)
		assertTreeValid(t, dataDir)
		out := map[string]map[string]string{}
		for _, slug := range []string{"lost-fleet", "lost-fleet-2", "lost-fleet-3", "lost-fleet-4"} {
			if entryExists(t, dataDir, seriesAddr(slug)) {
				out[slug] = seriesWorks(t, dataDir, slug)
			}
		}
		return out
	}
	forward := membership([]int{0, 1, 2, 3, 4, 5})
	groups := []map[string]string{
		{"incursion": "1", "insurrection": "2"},
		{"einfall": "1", "aufstand": "2"},
		{"valiant": "4", "relentless": "5"},
	}
	if len(forward) != 3 {
		t.Fatalf("forward order: %v, want three series", forward)
	}
	for _, g := range groups {
		found := false
		for _, members := range forward {
			found = found || reflect.DeepEqual(members, g)
		}
		if !found {
			t.Errorf("no series holds exactly %v: %v", g, forward)
		}
	}
	for name, order := range map[string][]int{"reverse": {5, 4, 3, 2, 1, 0}, "interleaved": {2, 4, 0, 5, 3, 1}} {
		if got := membership(order); !reflect.DeepEqual(got, forward) {
			t.Errorf("%s order: %v, want %v", name, got, forward)
		}
	}
}

// A TIE is never judged: a series holding one English and one German volume
// derives no language, so both languages' next volumes join it.
func TestATiedSeriesIsOpenToEveryLanguage(t *testing.T) {
	dataDir := seedTombstoneTree(t, map[string]string{
		"people/sa/sarah-hawke.json":            testpack.PersonJSON(t, "sarah-hawke", "Sarah Hawke"),
		"works/in/incursion/work.json":          testpack.WorkJSON(t, "incursion", "Incursion", testpack.WithAuthors("sarah-hawke")),
		"works/in/incursion/recordings/r1.json": testpack.RecJSON(t, "r1", "incursion", testpack.WithNarrators("bea-reader")),
		"works/ei/einfall/work.json": testpack.WorkJSON(t, "einfall", "Einfall",
			testpack.WithAuthors("sarah-hawke"), testpack.WithLanguage("de")),
		"works/ei/einfall/recordings/r1.json": testpack.RecJSON(t, "r1", "einfall", testpack.WithNarrators("bea-reader")),
		"series/lo/lost-fleet.json":           testpack.SeriesJSON(t, "lost-fleet", "Lost Fleet", "incursion@1", "einfall@2"),
	}, nil)
	sum := runLibexOver(t, dataDir,
		langRow("B0HAWKE003", "Invasion", "Sarah Hawke", "english", "Lost Fleet", "3"),
		langRow("B0HAWKD004", "Vergeltung", "Sarah Hawke", "german", "Lost Fleet", "4"))
	if got := seriesWorks(t, dataDir, "lost-fleet"); got["invasion"] != "3" || got["vergeltung"] != "4" || sum.NewSeries != 0 {
		t.Errorf("lost-fleet = %v, NewSeries %d; want both volumes joined", got, sum.NewSeries)
	}
	assertTreeValid(t, dataDir)
}

// Enrichment never creates a series, so a German work claiming the English
// series is simply not placed there.
func TestEnrichDoesNotPlaceIntoAnotherLanguagesSeries(t *testing.T) {
	dataDir := lostFleetTree(t, map[string]string{
		"works/ab/abtruennig/work.json": testpack.WorkJSON(t, "abtruennig", "Abtruennig",
			testpack.WithAuthors("sarah-hawke"), testpack.WithLanguage("de")),
		"works/ab/abtruennig/recordings/r1.json": testpack.RecJSON(t, "r1", "abtruennig",
			testpack.WithASIN("B0HAWKD004"), testpack.WithNarrators("bea-reader")),
	})
	sum, err := RunLibex(writeBooks(t, langRow("B0HAWKD004", "Abtruennig", "Sarah Hawke", "german", "Lost Fleet", "4")+"\n"),
		Options{DataDir: dataDir, ImportDate: testImportDate, Mode: ModeEnrich})
	if err != nil {
		t.Fatal(err)
	}
	if sum.SeriesPlacements != 0 || len(seriesWorks(t, dataDir, "lost-fleet")) != 3 {
		t.Errorf("enrichment placed a German work into the English series: placements %d, %v",
			sum.SeriesPlacements, seriesWorks(t, dataDir, "lost-fleet"))
	}
	assertTreeValid(t, dataDir)
}

// libex-select judges completion by the same rule, under its own stable code:
// a German row of the series' own author is not a completion of the English
// series, while the English row beside it is.
func TestSelectorSkipsAnotherLanguagesSeries(t *testing.T) {
	rows := []string{
		selectRow("B0SELDE002", "Band Zwei", "de", "german", seriesName, "2"),
		selectRow("B0SELEN003", "Volume Three", "us", "english", seriesName, "3"),
	}
	res, subset, lines := selectInto(t, seedSelectCatalogue(t), rows)
	if res.RowsSelected != 1 || res.Excluded[reasonSeriesLanguage.report] != 1 || res.Excluded[reasonSeriesAuthors.report] != 0 {
		t.Errorf("selected %d, excluded %v; want the English row kept and the German one refused as another language",
			res.RowsSelected, res.Excluded)
	}
	if len(lines) != 1 || lines[0] != (RowSkip{ASIN: "B0SELDE002", Reason: RefusalSeriesOtherLanguage}) {
		t.Errorf("refusals = %+v, want the German row under %s", lines, RefusalSeriesOtherLanguage)
	}
	if body, _ := os.ReadFile(subset); !strings.Contains(string(body), "B0SELEN003") {
		t.Error("the English completion was not selected")
	}
	if !strings.Contains(res.Report(), reasonSeriesLanguage.report) {
		t.Errorf("the report does not name the reason:\n%s", res.Report())
	}
}

// languageCloses is the whole rule: both sides known and in different primary
// languages. A tie or an unknown on either side is never judged.
func TestLanguageCloses(t *testing.T) {
	for _, tc := range []struct {
		series, row string
		want        bool
	}{
		{"de", "en", true},
		{"en", "en-GB", false},
		{"pt-BR", "pt", false},
		{"", "en", false}, // a tie, or no member states a language
		{"de", "", false}, // the row states none the importer knows
		{"", "", false},
	} {
		if got := languageCloses(tc.series, tc.row); got != tc.want {
			t.Errorf("languageCloses(%q, %q) = %v, want %v", tc.series, tc.row, got, tc.want)
		}
	}
}

// Every caller reads the one resolution: SeriesAuthorIndex.Resolve (the intake
// form's door) steps past a series in another language and names why, and a row
// stating no language is never judged.
func TestResolveReadsTheSeriesLanguage(t *testing.T) {
	cat := &model.Catalog{
		People: []*model.Person{{ID: "gregg-hurwitz", Name: "Gregg Hurwitz"}},
		Works: []*model.Work{
			{ID: "orphan-x-der-auftrag", Title: "Orphan X", Language: "de", Authors: []string{"gregg-hurwitz"}},
			{ID: "der-unbekannte", Title: "Der Unbekannte", Language: "de", Authors: []string{"gregg-hurwitz"}},
		},
		Series: []*model.Series{{ID: "orphan-x", Name: "Orphan X", Works: []model.SeriesWork{
			{Work: "orphan-x-der-auftrag", Position: "1"}, {Work: "der-unbekannte", Position: "2"}}}},
	}
	ix := NewSeriesAuthorIndex(cat)
	stored := func(slug string) (string, bool) {
		if slug == "orphan-x" {
			return "Orphan X", true
		}
		return "", false
	}
	row := func(lang string) *SeriesRow {
		return SeriesRowFor([]string{"Gregg Hurwitz"}, []string{"Last Man Standing"}, "", lang, nil)
	}
	if m := ix.Resolve("Orphan X", nil, stored, row("en")); m.Found || m.Slug != "orphan-x-2" ||
		!reflect.DeepEqual(m.Stepped, []string{"orphan-x"}) || m.Why() != "orphan-x is in another language (de)" {
		t.Errorf("an English row resolved to %+v (%q), want orphan-x-2 stepping past the German orphan-x", m, m.Why())
	}
	for _, lang := range []string{"de", ""} {
		if m := ix.Resolve("Orphan X", nil, stored, row(lang)); !m.Found || m.Slug != "orphan-x" {
			t.Errorf("a row in %q resolved to %+v, want the German orphan-x", lang, m)
		}
	}
}
