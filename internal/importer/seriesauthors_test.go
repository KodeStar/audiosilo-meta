package importer

import (
	"fmt"
	"reflect"
	"strings"
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

	// And the selector and the importer resolve the name to the same series.
	p := plannerOver(t, dataDir)
	campbell := func() *SeriesRow { return testRow("Jack Campbell") }
	idx, _ := loadSeriesIndex(dataDir)
	sel, _ := findInIndex(idx, "Lost Fleet", campbell())
	ref := p.refFor("Lost Fleet", campbell())
	created := p.getOrCreateSeries(ref, func(string, ...any) {})
	if sel != "lost-fleet-2" || p.seriesFor(ref) == nil || ref.target.slug != sel || created.slug != sel {
		t.Errorf("select %q and import %q disagree", sel, ref.target.slug)
	}
}

// The selection is re-resolved as a BATCH, as the import of exactly those rows
// will be: a one-member catalogue series is open to a stranger on its own, but
// not once the other kept rows have made it their author's.
func TestSelectorConfirmsTheBatch(t *testing.T) {
	dataDir := seedTombstoneTree(t, map[string]string{
		"works/on/one/work.json":          testpack.WorkJSON(t, "one", "One", testpack.WithAuthors("ada-mapmaker")),
		"works/on/one/recordings/r1.json": testpack.RecJSON(t, "r1", "one", testpack.WithNarrators("bea-reader")),
		"series/sa/saga.json":             testpack.SeriesJSON(t, "saga", "Saga", "one@1"),
	}, nil)
	rows := []string{
		tombRow("B0SAGA0002", "Two", "Ada Mapmaker", "Bea Reader", 600, "Saga", "2"),
		tombRow("B0SAGA0003", "Three", "Ada Mapmaker", "Bea Reader", 600, "Saga", "3"),
		tombRow("B0SAGA0009", "Stranger", "Zed Stranger", "Bea Reader", 600, "Saga", "9"),
	}
	res, kept := runSelect(t, dataDir, rows, 0)
	if len(kept) != 2 || res.Excluded[reasonSeriesAuthors] != 1 {
		t.Errorf("kept %d, excluded %v; want Ada's two volumes kept and the stranger dropped", len(kept), res.Excluded)
	}
}

// The batch can send a kept row to ANOTHER catalogued series of the same name -
// still a completion - and the selection follows it there: alone, a stranger's
// row fits Hawke's one-volume "Lost Fleet"; with her own next two volumes in the
// batch that series is hers, and the row completes the other "Lost Fleet" instead.
// A row whose position the new series already fills is not a completion there.
func TestSelectorFollowsTheBatchToAnotherSeries(t *testing.T) {
	dataDir := seedTombstoneTree(t, map[string]string{
		"people/sa/sarah-hawke.json":            testpack.PersonJSON(t, "sarah-hawke", "Sarah Hawke"),
		"people/ja/jack-campbell.json":          testpack.PersonJSON(t, "jack-campbell", "Jack Campbell"),
		"people/na/nate-narrator.json":          testpack.PersonJSON(t, "nate-narrator", "Nate Narrator"),
		"works/in/incursion/work.json":          testpack.WorkJSON(t, "incursion", "Incursion", testpack.WithAuthors("sarah-hawke")),
		"works/in/incursion/recordings/r1.json": testpack.RecJSON(t, "r1", "incursion"),
		"works/da/dauntless/work.json":          testpack.WorkJSON(t, "dauntless", "Dauntless", testpack.WithAuthors("jack-campbell")),
		"works/da/dauntless/recordings/r1.json": testpack.RecJSON(t, "r1", "dauntless"),
		"series/lo/lost-fleet.json":             testpack.SeriesJSON(t, "lost-fleet", "Lost Fleet", "incursion@1"),
		"series/lo/lost-fleet-2.json":           testpack.SeriesJSON(t, "lost-fleet-2", "Lost Fleet", "dauntless@1"),
	}, nil)
	rows := []string{
		tombRow("B0HAWKE002", "Insurrection", "Sarah Hawke", "Bea Reader", 600, "Lost Fleet", "2"),
		tombRow("B0HAWKE003", "Invasion", "Sarah Hawke", "Bea Reader", 600, "Lost Fleet", "3"),
		tombRow("B0STRANG09", "Stranger", "Zed Stranger", "Bea Reader", 600, "Lost Fleet", "9"),
		tombRow("B0OTHER001", "Other", "Yan Other", "Bea Reader", 600, "Lost Fleet", "1"),
	}
	res, kept := runSelect(t, dataDir, rows, 0)
	got := map[string]int{}
	for _, c := range res.PerSeries {
		got[c.Series] = c.Rows
	}
	want := map[string]int{"lost-fleet": 2, "lost-fleet-2": 1}
	if len(kept) != 3 || !reflect.DeepEqual(got, want) || res.Excluded[reasonPositionTaken] != 1 || res.Excluded[reasonSeriesAuthors] != 0 {
		t.Errorf("per series %v, excluded %v; want the stranger completing lost-fleet-2 and the row at its taken 1 dropped", got, res.Excluded)
	}
}

// An EXISTING squat does not make the squatter's next volume welcome: in a series
// one author dominates, sharing only a minority author is no evidence.
func TestAnExistingSquatterDoesNotKeepSquatting(t *testing.T) {
	dataDir := lostFleetTree(t, map[string]string{
		"works/re/renegade/work.json":           testpack.WorkJSON(t, "renegade", "Renegade", testpack.WithAuthors("sarah-hawke")),
		"works/re/renegade/recordings/r1.json":  testpack.RecJSON(t, "r1", "renegade"),
		"works/da/dauntless/work.json":          testpack.WorkJSON(t, "dauntless", "Dauntless", testpack.WithAuthors("jack-campbell")),
		"works/da/dauntless/recordings/r1.json": testpack.RecJSON(t, "r1", "dauntless"),
		"series/lo/lost-fleet.json": testpack.SeriesJSON(t, "lost-fleet", "Lost Fleet",
			"incursion@1", "insurrection@2", "invasion@3", "renegade@4", "dauntless@5"),
	})
	runLibexOver(t, dataDir, tombRow("B0CAMPB006", "Victorious", "Jack Campbell", "Bea Reader", 600, "Lost Fleet", "6"))
	if got := seriesWorks(t, dataDir, "lost-fleet"); got["victorious"] != "" {
		t.Errorf("the squatter's next volume joined the squatted series: %v", got)
	}
	if got := seriesWorks(t, dataDir, "lost-fleet-2"); got["victorious"] != "6" {
		t.Errorf("lost-fleet-2 = %v, want Victorious at 6", got)
	}
	assertTreeValid(t, dataDir)
}

// Resolution is a BATCH decision over a snapshot, so the rows' order changes
// nothing: the seed-wave shape - Hawke's volumes and Campbell's in one batch with
// no series catalogued yet - splits into the same two series either way round.
func TestSeriesResolutionIsOrderIndependent(t *testing.T) {
	rows := []string{
		tombRow("B0HAWKE001", "Incursion", "Sarah Hawke", "Bea Reader", 600, "Lost Fleet", "1"),
		tombRow("B0HAWKE002", "Insurrection", "Sarah Hawke", "Bea Reader", 600, "Lost Fleet", "2"),
		tombRow("B0HAWKE003", "Invasion", "Sarah Hawke", "Bea Reader", 600, "Lost Fleet", "3"),
		tombRow("B0CAMPB004", "Valiant", "Jack Campbell", "Bea Reader", 600, "Lost Fleet", "4"),
		tombRow("B0CAMPB005", "Relentless", "Jack Campbell", "Bea Reader", 600, "Lost Fleet", "5"),
	}
	membership := func(order []int) map[string]map[string]string {
		dataDir := seedTombstoneTree(t, nil, nil)
		var in []string
		for _, i := range order {
			in = append(in, rows[i])
		}
		runLibexOver(t, dataDir, in...)
		out := map[string]map[string]string{}
		for _, slug := range []string{"lost-fleet", "lost-fleet-2"} {
			if entryExists(t, dataDir, seriesAddr(slug)) {
				out[slug] = seriesWorks(t, dataDir, slug)
			}
		}
		return out
	}
	forward := membership([]int{0, 1, 2, 3, 4})
	reverse := membership([]int{4, 3, 2, 1, 0})
	interleaved := membership([]int{3, 0, 4, 1, 2})
	want := map[string]map[string]string{
		"lost-fleet":   {"incursion": "1", "insurrection": "2", "invasion": "3"},
		"lost-fleet-2": {"valiant": "4", "relentless": "5"},
	}
	for name, got := range map[string]map[string]map[string]string{"forward": forward, "reverse": reverse, "interleaved": interleaved} {
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s order: %v, want %v", name, got, want)
		}
	}
}

// TestUnplacedClaimsDoNotTakeTheBareSlug is the tranche replay's Oxford History
// shape: the largest author group in a batch states no usable position, so it
// never writes its series. Counted as evidence it founded the bare slug and
// refused everyone else from it, leaving the bare slug empty and the real series
// at "-2".
func TestUnplacedClaimsDoNotTakeTheBareSlug(t *testing.T) {
	dataDir := seedTombstoneTree(t, nil, nil)
	runLibexOver(t, dataDir,
		tombRow("B0HAWKE001", "Incursion", "Sarah Hawke", "Bea Reader", 600, "Lost Fleet", "1: Part One"),
		tombRow("B0HAWKE002", "Insurrection", "Sarah Hawke", "Bea Reader", 600, "Lost Fleet", "1: Part One"),
		tombRow("B0HAWKE003", "Invasion", "Sarah Hawke", "Bea Reader", 600, "Lost Fleet", "1: Part One"),
		tombRow("B0CAMPB004", "Valiant", "Jack Campbell", "Bea Reader", 600, "Lost Fleet", "4"),
	)
	if got, want := seriesWorks(t, dataDir, "lost-fleet"), map[string]string{"valiant": "4"}; !reflect.DeepEqual(got, want) {
		t.Errorf("lost-fleet = %v, want %v", got, want)
	}
	if entryExists(t, dataDir, seriesAddr("lost-fleet-2")) {
		t.Error("lost-fleet-2 was created for claims that place nothing")
	}
}

// testRow is a SeriesRow crediting names, with no title or publisher.
func testRow(names ...string) *SeriesRow { return SeriesRowFor(names, nil, "", nil) }

// samePersonName is personForm.same over two bare names.
func samePersonName(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	sa, _ := model.PersonSlug(a)
	sb, _ := model.PersonSlug(b)
	return formOf(sa, a).same(formOf(sb, b))
}

func TestSeriesAuthorsFit(t *testing.T) {
	names := map[string]string{
		"sarah-hawke": "Sarah Hawke", "ted-bell": "Ted Bell", "various": "Various",
		"ann": "Ann Author", "bob": "Bob Author", "cat": "Cat Author", "jack-campbell": "Jack Campbell",
		"a-b-kovacs": "A.B. Kovacs", "cheree-alsop": "Cheree Alsop", "cheree-lynn-alsop": "Cheree Lynn Alsop",
		"guest": "Guest Writer",
	}
	series := func(pub string, members ...[]string) *seriesAuthors {
		sa := &seriesAuthors{}
		for i, m := range members {
			var people []personForm
			for _, slug := range m {
				if individualAuthor(slug) {
					people = append(people, formOf(slug, names[slug]))
				}
			}
			sa.add(string(rune('a'+i)), people, []string{publisherKey(pub)})
		}
		return sa
	}
	withTitle := func(r *SeriesRow, title string) *SeriesRow { r.titles = []string{title}; return r }
	withPub := func(r *SeriesRow, pub string) *SeriesRow { r.publishers = []string{pub}; return r }
	hawke := series("Royal Guard Publishing LLC", []string{"sarah-hawke"}, []string{"sarah-hawke"}, []string{"sarah-hawke"})
	squatted := series("Royal Guard Publishing LLC", []string{"sarah-hawke"}, []string{"sarah-hawke"}, []string{"sarah-hawke"}, []string{"jack-campbell"})
	bell := series("Penguin", []string{"ted-bell"}, []string{"ted-bell"})
	large := map[string]bool{publisherKey("Tantor Audio"): true}
	tantor := series("Tantor Media", []string{"sarah-hawke"}, []string{"sarah-hawke"})
	// One person whose records forked into two slugs: by slug neither holds three
	// quarters, as one author they do.
	forked := series("x", []string{"cheree-alsop"}, []string{"cheree-alsop"}, []string{"cheree-lynn-alsop"}, []string{"guest"})
	// A guest co-crediting a member with the author a forked person is.
	coWritten := series("x", []string{"cheree-alsop"}, []string{"cheree-lynn-alsop"}, []string{"cheree-alsop", "guest"}, []string{"cheree-alsop"})
	for _, tc := range []struct {
		name string
		sa   *seriesAuthors
		row  *SeriesRow
		want seriesFit
	}{
		{"same author", hawke, testRow("Sarah Hawke"), seriesShared},
		{"initials spelling of the same author", series("x", []string{"a-b-kovacs"}, []string{"a-b-kovacs"}), testRow("AB Kovacs"), seriesShared},
		{"another author", hawke, testRow("Jack Campbell"), seriesClosed},
		{"a minority author of a dominated series", squatted, testRow("Jack Campbell"), seriesClosed},
		{"the dominant author of a squatted series", squatted, testRow("Sarah Hawke"), seriesShared},
		{"empty series", &seriesAuthors{}, testRow("Jack Campbell"), seriesOpen},
		{"nil series", nil, testRow("Jack Campbell"), seriesOpen},
		{"one member", series("x", []string{"sarah-hawke"}), testRow("Jack Campbell"), seriesOpen},
		{"no dominant author", series("x", []string{"ann"}, []string{"bob"}, []string{"cat"}, []string{"ann"}), testRow("Dee Author"), seriesOpen},
		{"a collective row states nobody", hawke, testRow("Various"), seriesOpen},
		{"collective members are no evidence", series("x", []string{"various"}, []string{"various"}), testRow("Jack Campbell"), seriesOpen},
		{"the title names the series' author", bell, withTitle(testRow("Ryan Steck"), "Ted Bell's Monarch"), seriesOpen},
		{"the row shares a small publisher", hawke, withPub(testRow("Jack Campbell"), "Royal Guard Publishing"), seriesOpen},
		{"another publisher", hawke, withPub(testRow("Jack Campbell"), "Audible Studios"), seriesClosed},
		{"a catalogue-wide house is no evidence", tantor, withPub(testRow("Jack Campbell"), "Tantor Audio"), seriesClosed},
		{"a person split across two slugs still dominates", forked, testRow("Jack Campbell"), seriesClosed},
		{"either spelling of a forked person shares", forked, testRow("Cheree Lynn Alsop"), seriesShared},
		{"a co-author of a forked dominant person shares", coWritten, testRow("Guest Writer"), seriesShared},
	} {
		if got := tc.sa.fit(tc.row, large); got != tc.want {
			t.Errorf("%s: fit = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// One book stated by several rows is one member, however many rows state it.
func TestSeriesEvidenceCountsWorks(t *testing.T) {
	sa := &seriesAuthors{}
	hawke := []personForm{formOf("sarah-hawke", "Sarah Hawke")}
	sa.add("incursion", hawke, nil)
	sa.add("incursion", hawke, nil) // the same book's other region
	if sa.members != 1 {
		t.Errorf("members = %d, want 1: one book is one member", sa.members)
	}
}

func TestSamePersonName(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"Sarah Hawke", "Sarah Hawke", true},
		{"A.B. Kovacs", "AB Kovacs", true},
		{"Cheree Alsop", "Cheree Lynn Alsop", true},
		{"Michael Salla PH.D.", "Michael Salla", true},
		{"Dr. Samuel Li", "Samuel Li", true},
		{"Eric Flint - edited", "Eric Flint", true},
		{"Innovative Language Learning LLC", "Innovative Language Learning", true},
		{"Christopher Shevlin", "Christopher Shevlinn", true}, // one edit over the whole name
		{"L. Frank Baum", "Lyman Frank Baum", true},           // an initial for a word
		{"Robert E. Howard", "Robert Ervin Howard", true},
		{"J.F. Holmes", "John F. Holmes", true},
		{"J. Campbell", "Jack Campbell", true},
		{"J.F. Holmes", "John Holmes", false},   // the F consumes no word
		{"J.F. Holmes", "Jane F. Holmes", true}, // a letter says only which word it begins
		{"SJ Bennett", "Sophia Bennett", false}, // two initials are not one word
		{"S.J. Bennett", "Sophia Bennett", false},
		{"Jack Campbell", "Sarah Hawke", false},
		{"Jack Campbell", "Joseph Campbell", false}, // surname and initial are not a person
		{"James Patterson", "Jennifer Patterson", false},
		{"Doug Hirt", "Douglas Hirt", false},
		{"Aijan", "Aijan Kashkaeva", false}, // a one-word name matching an end is not a person
		{"Sarah Maas", "Sarah Pinsker", false},
		{"Marion Chesney", "M. C. Beaton", false}, // a pen name no spelling rule can see
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

// A publisher is a catalogue-wide house above the catalogue share AND the
// minimum count: in a small catalogue a small press's own series stays a small
// press's.
func TestLargeHouses(t *testing.T) {
	counts := map[string]int{"tantor": 3000, "podium": 2700, "royal guard": 60}
	if got := largeHouses(counts, 279000); !got["tantor"] || got["podium"] || got["royal guard"] {
		t.Errorf("279k works: large = %v, want tantor only (podium is under 1%%)", got)
	}
	if got := largeHouses(counts, 1000); got["royal guard"] {
		t.Errorf("1k works: large = %v, want no small press counted however large its share", got)
	}
}

// The publisher arm works in a small catalogue: a row from the press that
// released the series' volumes may join it.
func TestSmallPressJoinsItsOwnLine(t *testing.T) {
	dataDir := lostFleetTree(t, nil)
	row := tombRow("B0CAMPB004", "Valiant", "Jack Campbell", "Bea Reader", 600, "Lost Fleet", "4")
	row = strings.TrimSuffix(row, "}") + `,"publisher":"Fixture Audio"}`
	runLibexOver(t, dataDir, row)
	if got := seriesWorks(t, dataDir, "lost-fleet"); got["valiant"] != "4" {
		t.Errorf("lost-fleet = %v, want the press's own row at 4", got)
	}
}

// A catalogue series of ONE volume is open to any one row, but not to a batch
// that would arrive as its dominant author: Campbell's six Lost Fleet rows do not
// take over Hawke's one-volume series.
func TestABatchDoesNotTakeOverAnOpenSeries(t *testing.T) {
	dataDir := seedTombstoneTree(t, map[string]string{
		"people/sa/sarah-hawke.json":            testpack.PersonJSON(t, "sarah-hawke", "Sarah Hawke"),
		"people/na/nate-narrator.json":          testpack.PersonJSON(t, "nate-narrator", "Nate Narrator"),
		"works/in/incursion/work.json":          testpack.WorkJSON(t, "incursion", "Incursion", testpack.WithAuthors("sarah-hawke")),
		"works/in/incursion/recordings/r1.json": testpack.RecJSON(t, "r1", "incursion"),
		"series/lo/lost-fleet.json":             testpack.SeriesJSON(t, "lost-fleet", "Lost Fleet", "incursion@1"),
	}, nil)
	var rows []string
	for i, title := range []string{"Dauntless", "Fearless", "Courageous", "Valiant", "Relentless", "Victorious"} {
		rows = append(rows, tombRow("B0CAMPB00"+string(rune('1'+i)), title, "Jack Campbell", "Bea Reader", 600, "Lost Fleet", string(rune('1'+i))))
	}
	runLibexOver(t, dataDir, rows...)
	if got := seriesWorks(t, dataDir, "lost-fleet"); len(got) != 1 {
		t.Errorf("lost-fleet = %v, want Hawke's one volume alone", got)
	}
	if got := seriesWorks(t, dataDir, "lost-fleet-2"); len(got) != 6 {
		t.Errorf("lost-fleet-2 = %v, want Campbell's six volumes", got)
	}
	assertTreeValid(t, dataDir)
}

// A group of rows the run drops founds no series: rows refused at admission are
// no evidence, and a series founded for rows a later guard refuses gives its slug
// back, so the surviving series takes the bare slug rather than leaving a gap the
// next run would fill with a second series of the name.
func TestDroppedRowsLeaveNoGapInTheChain(t *testing.T) {
	// Rows the run refuses at admission are no evidence: two of Ada's volumes in a
	// language the schema does not know would, counted, make her one-volume series
	// hers and close it to the one row that is written.
	t.Run("admission", func(t *testing.T) {
		dataDir := seedTombstoneTree(t, map[string]string{
			"works/on/one/work.json":          testpack.WorkJSON(t, "one", "One", testpack.WithAuthors("ada-mapmaker")),
			"works/on/one/recordings/r1.json": testpack.RecJSON(t, "r1", "one", testpack.WithNarrators("bea-reader")),
			"series/sa/saga.json":             testpack.SeriesJSON(t, "saga", "Saga", "one@1"),
		}, nil)
		runLibexOver(t, dataDir,
			libexRow{asin: "B0DROP0002", title: "Two", authors: `{"name":"Ada Mapmaker"}`, language: "klingon",
				series: `{"name":"Saga","position":"2"}`}.render(),
			libexRow{asin: "B0DROP0003", title: "Three", authors: `{"name":"Ada Mapmaker"}`, language: "klingon",
				series: `{"name":"Saga","position":"3"}`}.render(),
			libexRow{asin: "B0KEEP0009", title: "Stranger", authors: `{"name":"Zed Stranger"}`,
				series: `{"name":"Saga","position":"9"}`}.render(),
		)
		if got := seriesWorks(t, dataDir, "saga"); got["stranger"] != "9" {
			t.Errorf("saga = %v, want the one written row in the one-volume series", got)
		}
		if entryExists(t, dataDir, seriesAddr("saga-2")) {
			t.Error("rows that were never written closed the series to the one that was")
		}
	})
	// A drop the pre-pass cannot foresee - the duplicate-identity guard reads the
	// resolved claim - leaves the series founded for it unwritten: the series the
	// batch minted after it closes up onto its slug.
	t.Run("unforeseen drop", func(t *testing.T) {
		p := plannerOver(t, seedTombstoneTree(t, nil, nil))
		var warnings []string
		warn := func(f string, a ...any) { warnings = append(warnings, fmt.Sprintf(f, a...)) }
		r := seriesRef{name: "Druid Saga", seq: "2", seqOK: true, target: seriesTarget{slug: "druid-saga-2"}}
		p.addToSeries(r, "other-book", "2", warn)
		p.finalizeSeries()
		if ss := p.series["druid-saga"]; ss == nil || ss.members["other-book"] != "2" || ss.out.ID != "druid-saga" {
			t.Errorf("the written series did not close up onto the bare slug: %v", p.series)
		}
		if p.series["druid-saga-2"] != nil || len(warnings) != 0 {
			t.Errorf("druid-saga-2 kept, or a slug note for a slug not taken: %v", warnings)
		}
	})

}

// refFor is a claim to name resolved for row over the planner's catalogue, as
// the batch pre-pass would resolve a one-row batch.
func (p *planner) refFor(name string, row *SeriesRow) seriesRef {
	r := seriesRef{name: name}
	r.target = resolveSeriesClaims(p.seriesCatalogue(), []nameClaim{{name: name, row: row, order: claimOrder(row, ""), evidence: true}})[0]
	return r
}

// findInIndex is libex-select's resolution of name for row alone.
func findInIndex(idx seriesIndex, name string, row *SeriesRow) (string, bool) {
	t := resolveSeriesClaims(idx.catalogue(), []nameClaim{{name: name, row: row, order: claimOrder(row, ""), evidence: true}})[0]
	if !t.found {
		return "", false
	}
	return t.slug, true
}
