package importer

import (
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/testpack"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// Series resolution is author-aware (seriesauthors.go): a same-named series is
// joined only when it fits the row's authors, so an unrelated author's book never
// squats another author's slots. The shape every test here starts from is the
// Lost Fleet hijack: Jack Campbell's "Lost Fleet" books landed at positions 4-6 of
// Sarah Hawke's `lost-fleet` and pushed her real volumes 4-5 out.

// lostFleetTree is Sarah Hawke's three-volume `lost-fleet`, plus any extra files.
func lostFleetTree(t *testing.T, extra map[string]string) string {
	t.Helper()
	files := map[string]string{
		"people/sa/sarah-hawke.json":   testpack.PersonJSON(t, "sarah-hawke", "Sarah Hawke"),
		"people/ja/jack-campbell.json": testpack.PersonJSON(t, "jack-campbell", "Jack Campbell"),
		"people/na/nate-narrator.json": testpack.PersonJSON(t, "nate-narrator", "Nate Narrator"),
		"series/lo/lost-fleet.json":    testpack.SeriesJSON(t, "lost-fleet", "Lost Fleet", "incursion@1", "insurrection@2", "invasion@3"),
	}
	for _, w := range []string{"incursion", "insurrection", "invasion"} {
		files["works/in/"+w+"/work.json"] = testpack.WorkJSON(t, w, w, testpack.WithAuthors("sarah-hawke"))
		files["works/in/"+w+"/recordings/r1.json"] = testpack.RecJSON(t, "r1", w)
	}
	for k, v := range extra {
		files[k] = v
	}
	return seedTombstoneTree(t, files, nil)
}

// seriesWorks reads a series entry's (work, position) pairs.
func seriesWorks(t *testing.T, dataDir, slug string) map[string]string {
	t.Helper()
	var ser struct {
		Name  string                            `json:"name"`
		Works []struct{ Work, Position string } `json:"works"`
	}
	readEntity(t, dataDir, seriesAddr(slug), &ser)
	out := map[string]string{}
	for _, sw := range ser.Works {
		out[sw.Work] = sw.Position
	}
	return out
}

// The Lost Fleet shape: a Campbell row naming "Lost Fleet" does not land in
// Hawke's series - it starts Campbell's own at the next chain candidate, and says
// so - while Hawke's own next volume still joins hers.
func TestAnotherAuthorsRowDoesNotSquatASeries(t *testing.T) {
	dataDir := lostFleetTree(t, nil)
	sum := runLibexOver(t, dataDir,
		tombRow("B0CAMPB004", "Valiant", "Jack Campbell", "Bea Reader", 600, "Lost Fleet", "4"),
		tombRow("B0CAMPB005", "Relentless", "Jack Campbell", "Bea Reader", 600, "Lost Fleet", "5"),
		tombRow("B0HAWKE004", "Renegade", "Sarah Hawke", "Bea Reader", 600, "Lost Fleet", "4"))

	hawke := seriesWorks(t, dataDir, "lost-fleet")
	if len(hawke) != 4 || hawke["renegade"] != "4" {
		t.Errorf("lost-fleet = %v, want Hawke's three volumes plus her Renegade at 4", hawke)
	}
	campbell := seriesWorks(t, dataDir, "lost-fleet-2")
	if campbell["valiant"] != "4" || campbell["relentless"] != "5" || len(campbell) != 2 {
		t.Errorf("lost-fleet-2 = %v, want Campbell's Valiant at 4 and Relentless at 5", campbell)
	}
	if sum.NewSeries != 1 {
		t.Errorf("NewSeries = %d, want 1 (Campbell's second row joins the series his first minted)", sum.NewSeries)
	}
	if !hasWarning(sum.Warnings, "lost-fleet belongs to other authors") {
		t.Errorf("the mint does not say why: %v", sum.Warnings)
	}
	assertTreeValid(t, dataDir)
}

// Once Campbell's series exists, a later run's Campbell row walks PAST Hawke's
// same-named base to it rather than minting a third "Lost Fleet".
func TestAuthorsSeriesDownTheChainIsFound(t *testing.T) {
	dataDir := lostFleetTree(t, map[string]string{
		"works/da/dauntless/work.json":          testpack.WorkJSON(t, "dauntless", "Dauntless", testpack.WithAuthors("jack-campbell")),
		"works/da/dauntless/recordings/r1.json": testpack.RecJSON(t, "r1", "dauntless"),
		"series/lo/lost-fleet-2.json":           testpack.SeriesJSON(t, "lost-fleet-2", "Lost Fleet", "dauntless@1"),
	})
	sum := runLibexOver(t, dataDir, tombRow("B0CAMPB002", "Fearless", "Jack Campbell", "Bea Reader", 600, "Lost Fleet", "2"))

	if sum.NewSeries != 0 {
		t.Errorf("NewSeries = %d, want 0", sum.NewSeries)
	}
	if got := seriesWorks(t, dataDir, "lost-fleet-2"); got["fearless"] != "2" {
		t.Errorf("lost-fleet-2 = %v, want Fearless at 2", got)
	}
	if got := seriesWorks(t, dataDir, "lost-fleet"); len(got) != 3 {
		t.Errorf("Hawke's lost-fleet changed: %v", got)
	}
	assertTreeValid(t, dataDir)
}

// A shared universe has no dominant author, so a writer it has never credited
// joins it: three volumes by three writers is a franchise, not somebody's series.
func TestSharedUniverseAcceptsANewAuthor(t *testing.T) {
	files := map[string]string{
		"series/st/star-saga.json": testpack.SeriesJSON(t, "star-saga", "Star Saga", "one@1", "two@2", "three@3"),
	}
	for i, a := range []string{"ann-author", "bob-author", "cat-author"} {
		w := []string{"one", "two", "three"}[i]
		files["people/"+a[:2]+"/"+a+".json"] = testpack.PersonJSON(t, a, []string{"Ann Author", "Bob Author", "Cat Author"}[i])
		files["works/"+w[:2]+"/"+w+"/work.json"] = testpack.WorkJSON(t, w, w, testpack.WithAuthors(a))
		files["works/"+w[:2]+"/"+w+"/recordings/r1.json"] = testpack.RecJSON(t, "r1", w)
	}
	files["people/na/nate-narrator.json"] = testpack.PersonJSON(t, "nate-narrator", "Nate Narrator")
	dataDir := seedTombstoneTree(t, files, nil)
	sum := runLibexOver(t, dataDir, tombRow("B0SAGA0004", "Four", "Dee Author", "Bea Reader", 600, "Star Saga", "4"))

	if sum.NewSeries != 0 || seriesWorks(t, dataDir, "star-saga")["four"] != "4" {
		t.Errorf("the shared universe refused a new writer: NewSeries %d, warnings %v", sum.NewSeries, sum.Warnings)
	}
	assertTreeValid(t, dataDir)
}

// A series with a single member is nobody's franchise yet: anyone joins it.
func TestSingleMemberSeriesAcceptsAnyone(t *testing.T) {
	dataDir := seedTombstoneTree(t, map[string]string{
		"works/on/one/work.json":          testpack.WorkJSON(t, "one", "One", testpack.WithAuthors("ada-mapmaker")),
		"works/on/one/recordings/r1.json": testpack.RecJSON(t, "r1", "one", testpack.WithNarrators("bea-reader")),
		"series/sa/saga.json":             testpack.SeriesJSON(t, "saga", "Saga", "one@1"),
	}, nil)
	sum := runLibexOver(t, dataDir, tombRow("B0SAGA0002", "Two", "Zed Stranger", "Bea Reader", 600, "Saga", "2"))
	if sum.NewSeries != 0 || seriesWorks(t, dataDir, "saga")["two"] != "2" {
		t.Errorf("a one-member series refused a new author: NewSeries %d, warnings %v", sum.NewSeries, sum.Warnings)
	}
	assertTreeValid(t, dataDir)
}

// Enrichment never creates a series, so a work whose claimed series belongs only
// to other authors is simply not placed.
func TestEnrichDoesNotPlaceIntoAnotherAuthorsSeries(t *testing.T) {
	dataDir := lostFleetTree(t, map[string]string{
		"works/va/valiant/work.json": testpack.WorkJSON(t, "valiant", "Valiant", testpack.WithAuthors("jack-campbell")),
		"works/va/valiant/recordings/r1.json": testpack.RecJSON(t, "r1", "valiant",
			testpack.WithASIN("B0CAMPB004"), testpack.WithNarrators("bea-reader")),
	})
	sum, err := RunLibex(writeBooks(t, tombRow("B0CAMPB004", "Valiant", "Jack Campbell", "Bea Reader", 600, "Lost Fleet", "4")+"\n"),
		Options{DataDir: dataDir, ImportDate: testImportDate, Mode: ModeEnrich})
	if err != nil {
		t.Fatal(err)
	}
	if sum.SeriesPlacements != 0 || len(seriesWorks(t, dataDir, "lost-fleet")) != 3 {
		t.Errorf("enrichment placed Campbell into Hawke's series: placements %d, %v", sum.SeriesPlacements, seriesWorks(t, dataDir, "lost-fleet"))
	}
	assertTreeValid(t, dataDir)
}

// libex-select judges completion by the same rule: a row whose only same-named
// series belongs to other authors is not a completion (the import would mint a
// new series for it), and is reported under its own reason; with the author's own
// series down the chain it completes THAT one.
func TestSelectorSkipsAnotherAuthorsSeries(t *testing.T) {
	row := tombRow("B0CAMPB004", "Valiant", "Jack Campbell", "Bea Reader", 600, "Lost Fleet", "4")

	res, kept := runSelect(t, lostFleetTree(t, nil), []string{row}, 0)
	if len(kept) != 0 || res.Excluded[reasonSeriesAuthors] != 1 {
		t.Errorf("kept %d, excluded %v; want the row excluded as another author's series", len(kept), res.Excluded)
	}

	dataDir := lostFleetTree(t, map[string]string{
		"works/da/dauntless/work.json":          testpack.WorkJSON(t, "dauntless", "Dauntless", testpack.WithAuthors("jack-campbell")),
		"works/da/dauntless/recordings/r1.json": testpack.RecJSON(t, "r1", "dauntless"),
		"series/lo/lost-fleet-2.json":           testpack.SeriesJSON(t, "lost-fleet-2", "Lost Fleet", "dauntless@1"),
	})
	res, kept = runSelect(t, dataDir, []string{row}, 0)
	if len(kept) != 1 || len(res.PerSeries) != 1 || res.PerSeries[0].Series != "lost-fleet-2" {
		t.Errorf("kept %d into %+v; want the row completing lost-fleet-2", len(kept), res.PerSeries)
	}

	// And every walker agrees on which series that is.
	p := plannerOver(t, dataDir)
	seriesRow := SeriesRow{Authors: []SeriesPerson{{Slug: "jack-campbell", Name: "Jack Campbell"}}}
	idx, _ := loadSeriesIndex(dataDir)
	sel, _, _ := idx.find("Lost Fleet", seriesRow)
	found := p.findSeries("Lost Fleet", seriesRow)
	created := p.getOrCreateSeries("Lost Fleet", seriesRow, func(string, ...any) {})
	if sel != "lost-fleet-2" || found == nil || found.slug != sel || created.slug != sel {
		t.Errorf("walkers disagree: select %q, findSeries %v, getOrCreateSeries %q", sel, found, created.slug)
	}
}

func TestSeriesAuthorsFit(t *testing.T) {
	names := map[string]string{
		"sarah-hawke": "Sarah Hawke", "ted-bell": "Ted Bell", "various": "Various",
		"ann": "Ann Author", "bob": "Bob Author", "cat": "Cat Author",
	}
	nameOf := func(s string) string { return names[s] }
	series := func(members ...[]string) *SeriesAuthors {
		sa := &SeriesAuthors{}
		for _, m := range members {
			sa.add(m, []string{"Royal Guard Publishing LLC"})
		}
		return sa
	}
	who := func(name string) SeriesRow {
		slug, _ := model.PersonSlug(name)
		return SeriesRow{Authors: []SeriesPerson{{Slug: slug, Name: name}}}
	}
	hawke := series([]string{"sarah-hawke"}, []string{"sarah-hawke"}, []string{"sarah-hawke"})
	bell := series([]string{"ted-bell"}, []string{"ted-bell"})
	for _, tc := range []struct {
		name string
		sa   *SeriesAuthors
		row  SeriesRow
		want SeriesFit
	}{
		{"same author", hawke, who("Sarah Hawke"), SeriesShared},
		{"spelling of the same author", hawke, who("S. Hawke"), SeriesShared},
		{"another author", hawke, who("Jack Campbell"), SeriesClosed},
		{"empty series", &SeriesAuthors{}, who("Jack Campbell"), SeriesOpen},
		{"nil series", nil, who("Jack Campbell"), SeriesOpen},
		{"one member", series([]string{"sarah-hawke"}), who("Jack Campbell"), SeriesOpen},
		{"no dominant author", series([]string{"ann"}, []string{"bob"}, []string{"cat"}, []string{"ann"}), who("Dee Author"), SeriesOpen},
		{"a collective row states nobody", hawke, who("Various"), SeriesOpen},
		{"collective members are no evidence", series([]string{"various"}, []string{"various"}), who("Jack Campbell"), SeriesOpen},
		{"the title names the series' author", bell,
			SeriesRow{Authors: who("Ryan Steck").Authors, Titles: []string{"Ted Bell's Monarch"}}, SeriesOpen},
		{"the row shares the publisher", hawke,
			SeriesRow{Authors: who("Jack Campbell").Authors, Publishers: []string{"Royal Guard Publishing"}}, SeriesOpen},
		{"another publisher", hawke,
			SeriesRow{Authors: who("Jack Campbell").Authors, Publishers: []string{"Audible Studios"}}, SeriesClosed},
	} {
		if got := tc.sa.Fit(tc.row, nameOf); got != tc.want {
			t.Errorf("%s: Fit = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestSamePersonName(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"Sarah Hawke", "Sarah Hawke", true},
		{"A.B. Kovacs", "AB Kovacs", true},
		{"M.S. Olney", "Matthew Olney", true},
		{"Doug Hirt", "Douglas Hirt", true},
		{"Julian Gyll", "Julian Gyll-Murray", true},
		{"Cheree Alsop", "Cheree Lynn Alsop", true},
		{"Aijan", "Aijan Kashkaeva", true},
		{"Lowe Key", "Lowe Keye", true},
		{"Michael Salla PH.D.", "Michael Salla", true},
		{"Dr. Samuel Li", "Samuel Xiangming Li", true},
		{"Eric Flint - edited", "Eric Flint", true},
		{"Innovative Language Learning LLC", "Innovative Language Learning", true},
		{"Jack Campbell", "Sarah Hawke", false},
		{"Sarah Maas", "Sarah Pinsker", false},
		{"Marion Chesney", "M. C. Beaton", false}, // a pen name no spelling rule can see
		{"Ed", "Ed Greenwood", false},             // a one-word name needs three letters
	} {
		if got := samePersonName(tc.a, tc.b); got != tc.want {
			t.Errorf("samePersonName(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
		if got := samePersonName(tc.b, tc.a); got != tc.want {
			t.Errorf("samePersonName(%q, %q) = %v, want %v (asymmetric)", tc.b, tc.a, got, tc.want)
		}
	}
}

func TestPublisherKey(t *testing.T) {
	for _, tc := range []struct{ a, b string }{
		{"Tantor Audio", "Tantor Media"},
		{"Blackstone Audio, Inc.", "Blackstone Publishing"},
		{"Kiddinx Media GmbH", "KIDDINX"},
		{"Simon &amp; Schuster Audio", "Simon & Schuster"},
	} {
		if publisherKey(tc.a) != publisherKey(tc.b) {
			t.Errorf("publisherKey(%q) = %q, publisherKey(%q) = %q; want one house", tc.a, publisherKey(tc.a), tc.b, publisherKey(tc.b))
		}
	}
	if publisherKey("Audio Books Ltd") != "" {
		t.Errorf("a publisher name of generic words keyed as %q, want nothing distinctive", publisherKey("Audio Books Ltd"))
	}
}
