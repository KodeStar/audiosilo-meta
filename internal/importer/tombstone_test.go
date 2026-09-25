package importer

import (
	"strconv"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/testpack"
	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
	"github.com/kodestar/audiosilo-meta/pkg/redirects"
)

// The minters follow the slug tombstone table (tombstone.go). Every test here
// seeds a tree whose data/redirects.json retires a slug the incoming row's name
// lands on, and ends by validating the tree - which is the assertion issue #2320
// failed: a series name that slugged to a retired id was minted there, and
// metacheck refused the whole import for re-creating the merged duplicate.

// seedTombstoneTree seeds the records every tombstone test shares and writes the
// given tombstone table beside them.
func seedTombstoneTree(t *testing.T, files map[string]string, reds model.Redirects) string {
	t.Helper()
	dataDir := t.TempDir()
	base := map[string]string{
		"people/ad/ada-mapmaker.json": testpack.PersonJSON(t, "ada-mapmaker", "Ada Mapmaker"),
		"people/be/bea-reader.json":   testpack.PersonJSON(t, "bea-reader", "Bea Reader"),
	}
	for k, v := range files {
		base[k] = v
	}
	testpack.Seed(t, dataDir, base)
	if err := redirects.Write(dataDir, reds); err != nil {
		t.Fatalf("write redirects: %v", err)
	}
	assertTreeValid(t, dataDir)
	return dataDir
}

// tombRow is a minimal importable libex row; an empty series places it nowhere.
func tombRow(asin, title, author, narrator string, minutes int, series, pos string) string {
	row := `{"asin":"` + asin + `","title":"` + title + `","region":"us","language":"english",` +
		`"bookFormat":"unabridged","lengthMinutes":` + strconv.Itoa(minutes) + `,"authors":[{"name":"` + author + `"}],` +
		`"narrators":[{"name":"` + narrator + `"}]`
	if series != "" {
		row += `,"series":[{"name":"` + series + `","position":"` + pos + `"}]`
	}
	return row + "}"
}

func runLibexOver(t *testing.T, dataDir string, rows ...string) Summary {
	t.Helper()
	sum, err := RunLibex(writeBooks(t, strings.Join(rows, "\n")+"\n"), Options{DataDir: dataDir, ImportDate: testImportDate})
	if err != nil {
		t.Fatalf("import run: %v", err)
	}
	return sum
}

// TestSeriesBaseTombstoneJoinsTheSurvivor is issue #2320's own shape: a repair
// wave merged "A Kate Wise Mystery" into "Kate Wise Mystery Series", and a
// library row naming the retired spelling must join the survivor - with no name
// comparison, since the survivor's name is the other spelling - rather than mint
// the duplicate the merge removed.
func TestSeriesBaseTombstoneJoinsTheSurvivor(t *testing.T) {
	dataDir := seedTombstoneTree(t, map[string]string{
		"works/if/if-she-knew/work.json":          testpack.WorkJSON(t, "if-she-knew", "If She Knew", testpack.WithAuthors("ada-mapmaker")),
		"works/if/if-she-knew/recordings/r1.json": testpack.RecJSON(t, "r1", "if-she-knew", testpack.WithNarrators("bea-reader")),
		"series/ka/kate-wise-mystery-series.json": testpack.SeriesJSON(t, "kate-wise-mystery-series", "Kate Wise Mystery Series", "if-she-knew@1"),
	}, model.Redirects{model.RedirectSeries: {"a-kate-wise-mystery": "kate-wise-mystery-series"}})

	sum := runLibexOver(t, dataDir, tombRow("B0TOMBS001", "If She Ran", "Ada Mapmaker", "Bea Reader", 300, "A Kate Wise Mystery", "2"))

	if sum.NewSeries != 0 {
		t.Errorf("NewSeries = %d, want 0: the retired spelling names the survivor", sum.NewSeries)
	}
	if entryExists(t, dataDir, seriesAddr("a-kate-wise-mystery")) {
		t.Error("a series was minted at the retired slug a-kate-wise-mystery")
	}
	var ser struct {
		Works []struct{ Work, Position string } `json:"works"`
	}
	readEntity(t, dataDir, seriesAddr("kate-wise-mystery-series"), &ser)
	if len(ser.Works) != 2 || ser.Works[1].Work != "if-she-ran" || ser.Works[1].Position != "2" {
		t.Errorf("survivor works = %+v, want if-she-ran appended at 2", ser.Works)
	}
	if !hasWarning(sum.Notes, "series a-kate-wise-mystery -> kate-wise-mystery-series") {
		t.Errorf("no note naming the ride: %v", sum.Notes)
	}
	assertTreeValid(t, dataDir)
}

// numberedTombstoneTree holds a series chain whose "-2" candidate is retired:
// "saga" belongs to a differently-named series, "saga-2" is a tombstone and
// "saga-3" is the live "Saga". reds is the tombstone table it carries.
func numberedTombstoneTree(t *testing.T, reds model.Redirects) string {
	t.Helper()
	return seedTombstoneTree(t, map[string]string{
		"works/on/one/work.json":          testpack.WorkJSON(t, "one", "One", testpack.WithAuthors("ada-mapmaker")),
		"works/on/one/recordings/r1.json": testpack.RecJSON(t, "r1", "one", testpack.WithNarrators("bea-reader")),
		"works/tw/two/work.json":          testpack.WorkJSON(t, "two", "Two", testpack.WithAuthors("ada-mapmaker")),
		"works/tw/two/recordings/r1.json": testpack.RecJSON(t, "r1", "two", testpack.WithNarrators("bea-reader")),
		"series/sa/saga.json":             testpack.SeriesJSON(t, "saga", "Saga!!", "one@1"),
		"series/sa/saga-3.json":           testpack.SeriesJSON(t, "saga-3", "Saga", "two@1"),
	}, reds)
}

// retiredSagaTwo is the tombstone every numbered-chain test starts from.
var retiredSagaTwo = model.Redirects{model.RedirectSeries: {"saga-2": "saga"}}

// TestNumberedSeriesTombstoneIsSteppedPast: a retired NUMBERED candidate is
// occupied, not a join - which name that "-2" carried is not recorded. It must
// neither stop the walk (the live "saga-3" beyond it is the row's series) nor be
// minted at.
func TestNumberedSeriesTombstoneIsSteppedPast(t *testing.T) {
	dataDir := numberedTombstoneTree(t, retiredSagaTwo)
	sum := runLibexOver(t, dataDir, tombRow("B0TOMBS002", "Three", "Ada Mapmaker", "Bea Reader", 300, "Saga", "2"))

	if sum.NewSeries != 0 {
		t.Errorf("NewSeries = %d, want 0: the walk must reach the live saga-3", sum.NewSeries)
	}
	if entryExists(t, dataDir, seriesAddr("saga-2")) {
		t.Error("a series was minted at the retired numbered slug saga-2")
	}
	var ser struct {
		Works []struct{ Work, Position string } `json:"works"`
	}
	readEntity(t, dataDir, seriesAddr("saga-3"), &ser)
	if len(ser.Works) != 2 {
		t.Errorf("saga-3 works = %+v, want the new volume beside two", ser.Works)
	}
	if len(sum.Notes) != 0 {
		t.Errorf("a stepped-past tombstone is not a ride, but the run noted: %v", sum.Notes)
	}
	assertTreeValid(t, dataDir)
}

// TestNumberedTombstoneMintsBeyondIt: a NEW series whose chain crosses a retired
// numbered slug mints at the next free candidate, never at the retired one.
// "Saga?" slugs onto the saga chain but matches neither stored name.
func TestNumberedTombstoneMintsBeyondIt(t *testing.T) {
	dataDir := numberedTombstoneTree(t, retiredSagaTwo)
	sum := runLibexOver(t, dataDir, tombRow("B0TOMBS003", "Three", "Ada Mapmaker", "Bea Reader", 300, "Saga?", "1"))

	if sum.NewSeries != 1 {
		t.Errorf("NewSeries = %d, want 1", sum.NewSeries)
	}
	if entryExists(t, dataDir, seriesAddr("saga-2")) {
		t.Error("a series was minted at the retired numbered slug saga-2")
	}
	if !entryExists(t, dataDir, seriesAddr("saga-4")) {
		t.Error("the new series did not step past the retired saga-2 and the live saga-3 onto saga-4")
	}
	assertTreeValid(t, dataDir)
}

// TestSeriesWalkersAgreeOverTombstones pins the three walkers of one chain -
// getOrCreateSeries, findSeries and libex-select's seriesIndex.find - to one
// answer over a tree with a tombstone at both kinds of chain index. A selection
// that disagreed with the import would select rows into series they then fork.
func TestSeriesWalkersAgreeOverTombstones(t *testing.T) {
	dataDir := numberedTombstoneTree(t, model.Redirects{model.RedirectSeries: {"saga-2": "saga", "old-saga": "saga-3"}})

	idx, warns := loadSeriesIndex(dataDir)
	if len(warns) != 0 {
		t.Fatalf("loadSeriesIndex warnings: %v", warns)
	}
	for _, tc := range []struct {
		name string
		want string // "" = resolves to nothing
	}{
		{"Saga", "saga-3"},     // past a differently-named base and a retired "-2"
		{"Saga!!", "saga"},     // the base's own name
		{"Old Saga", "saga-3"}, // a retired BASE joins its survivor, whatever it is called
		{"Brand New", ""},      // nothing there
		{"Saga?", ""},          // slugs onto the chain but matches no stored name: a new series
	} {
		p := plannerOver(t, dataDir)
		got := ""
		if ss := p.findSeries(tc.name, SeriesRow{}); ss != nil {
			got = ss.slug
		}
		if got != tc.want {
			t.Errorf("findSeries(%q) = %q, want %q", tc.name, got, tc.want)
		}
		if sel, ok, _ := idx.find(tc.name, SeriesRow{}); sel != tc.want || ok != (tc.want != "") {
			t.Errorf("seriesIndex.find(%q) = %q/%v, want %q", tc.name, sel, ok, tc.want)
		}
		ss := p.getOrCreateSeries(tc.name, SeriesRow{}, func(string, ...any) {})
		if tc.want != "" && (ss.isNew || ss.slug != tc.want) {
			t.Errorf("getOrCreateSeries(%q) = %q (new %v), want the existing %q", tc.name, ss.slug, ss.isNew, tc.want)
		}
		if tc.want == "" && (!ss.isNew || ss.slug == "saga-2" || ss.slug == "old-saga") {
			t.Errorf("getOrCreateSeries(%q) = %q (new %v), want a new series off every retired slug", tc.name, ss.slug, ss.isNew)
		}
	}
}

// plannerOver loads a planner over dataDir exactly as a run does, for the tests
// that ask the walkers directly.
func plannerOver(t *testing.T, dataDir string) *planner {
	t.Helper()
	store, err := openStore(dataDir, pack.ProfileAll)
	if err != nil {
		t.Fatal(err)
	}
	p := newPlanner(store, sourceLibex, Options{DataDir: dataDir, ImportDate: testImportDate})
	p.loadExisting()
	return p
}

// TestPersonTombstoneResolvesToTheSurvivor: a person slug is the identity, so a
// credit whose name slugs onto a retired person IS the survivor - credited there,
// never re-created at the address a merge took them off.
func TestPersonTombstoneResolvesToTheSurvivor(t *testing.T) {
	dataDir := seedTombstoneTree(t, map[string]string{
		"people/jo/jon-smith.json": testpack.PersonJSON(t, "jon-smith", "Jon Smith"),
	}, model.Redirects{model.RedirectPeople: {"jonathan-q-smith": "jon-smith"}})

	sum := runLibexOver(t, dataDir, tombRow("B0TOMBS004", "A Fresh Book", "Jonathan Q. Smith", "Bea Reader", 300, "", ""))

	if sum.NewPeople != 0 {
		t.Errorf("NewPeople = %d, want 0: the author is the survivor", sum.NewPeople)
	}
	if entryExists(t, dataDir, "people/jo/jonathan-q-smith.json") {
		t.Error("a person was minted at the retired slug jonathan-q-smith")
	}
	var w struct {
		Authors []string `json:"authors"`
	}
	readEntity(t, dataDir, workAddr("a-fresh-book"), &w)
	if len(w.Authors) != 1 || w.Authors[0] != "jon-smith" {
		t.Errorf("authors = %v, want [jon-smith]", w.Authors)
	}
	if !hasWarning(sum.Notes, "people jonathan-q-smith -> jon-smith") {
		t.Errorf("no note naming the ride: %v", sum.Notes)
	}
	assertTreeValid(t, dataDir)
}

// workTombstoneTree retires "the-thing" onto Ada Mapmaker's "The Thing (Special
// Edition)", the shape a duplicate merge leaves: the decorated record survived
// and the plain title's slug is a tombstone.
func workTombstoneTree(t *testing.T) string {
	t.Helper()
	return seedTombstoneTree(t, map[string]string{
		"works/th/the-thing-special-edition/work.json": testpack.WorkJSON(t, "the-thing-special-edition",
			"The Thing (Special Edition)", testpack.WithAuthors("ada-mapmaker")),
		"works/th/the-thing-special-edition/recordings/r1.json": testpack.RecJSON(t, "r1", "the-thing-special-edition",
			testpack.WithNarrators("bea-reader"), testpack.WithRuntime(300)),
	}, model.Redirects{model.RedirectWorks: {"the-thing": "the-thing-special-edition"}})
}

// TestWorkTombstoneMergesIntoTheSurvivorOnTheIdentityRule: a retired work slug is
// judged as its survivor, on exactly the rules a live record at that candidate
// gets - here the same author, so the row's recording lands on the survivor and no
// work is minted at the retired slug.
func TestWorkTombstoneMergesIntoTheSurvivorOnTheIdentityRule(t *testing.T) {
	dataDir := workTombstoneTree(t)
	sum := runLibexOver(t, dataDir, tombRow("B0TOMBS005", "The Thing", "Ada Mapmaker", "Bea Reader", 300, "", ""))

	if sum.NewWorks != 0 || sum.SkippedDuplicateIdentity != 0 {
		t.Errorf("NewWorks = %d, SkippedDuplicateIdentity = %d; want the row merged into the survivor",
			sum.NewWorks, sum.SkippedDuplicateIdentity)
	}
	if entryExists(t, dataDir, workAddr("the-thing")) {
		t.Error("a work was minted at the retired slug the-thing")
	}
	found := false
	for _, rec := range recSlugsOf(t, dataDir, "the-thing-special-edition") {
		var r struct {
			ASIN []struct{ ASIN string } `json:"asin"`
		}
		readEntity(t, dataDir, recAddr("the-thing-special-edition", rec), &r)
		for _, a := range r.ASIN {
			found = found || a.ASIN == "B0TOMBS005"
		}
	}
	if !found {
		t.Error("the row's ASIN is not on any recording of the survivor")
	}
	if !hasWarning(sum.Notes, "works the-thing -> the-thing-special-edition") {
		t.Errorf("no note naming the ride: %v", sum.Notes)
	}
	assertTreeValid(t, dataDir)
}

// TestWorkTombstoneOfADifferentBookIsSteppedPast: when the survivor is NOT the
// row's book (another author), the retired candidate is occupied - the row steps
// onto the author-suffixed slug exactly as it would past a live different book,
// and nothing is minted at the retired slug.
func TestWorkTombstoneOfADifferentBookIsSteppedPast(t *testing.T) {
	dataDir := workTombstoneTree(t)
	sum := runLibexOver(t, dataDir, tombRow("B0TOMBS006", "The Thing", "Otto Other", "Bea Reader", 300, "", ""))

	if entryExists(t, dataDir, workAddr("the-thing")) {
		t.Error("a work was minted at the retired slug the-thing")
	}
	want := AuthorSuffixedWorkSlug("the-thing", "otto-other")
	if !entryExists(t, dataDir, workAddr(want)) {
		t.Errorf("no work at %q: a retired candidate for a different book is stepped past", want)
	}
	if !hasWarning(sum.Warnings, `work slug "the-thing" was retired by a merge onto "the-thing-special-edition"`) {
		t.Errorf("no warning naming the retired slug: %v", sum.Warnings)
	}
	assertTreeValid(t, dataDir)
}

// TestRecordingsOnlyFollowsAWorkTombstone: the alternate-narration pass may only
// attach to the work getOrCreateWork would have chosen, so a row whose title
// slugs onto a retired work reaches the survivor through the tombstone too -
// rather than being dropped as "not in the catalogue".
func TestRecordingsOnlyFollowsAWorkTombstone(t *testing.T) {
	dataDir := workTombstoneTree(t)
	sum := runRecordingsOnly(t, dataDir, tombRow("B0TOMBS007", "The Thing", "Ada Mapmaker", "Cy Voice", 900, "", "")+"\n", false)

	if sum.NewRecordings != 1 || sum.SkippedNoWork != 0 {
		t.Errorf("NewRecordings = %d, SkippedNoWork = %d; want the narration on the survivor", sum.NewRecordings, sum.SkippedNoWork)
	}
	if entryExists(t, dataDir, workAddr("the-thing")) {
		t.Error("a work was minted at the retired slug the-thing")
	}
	if n := len(recSlugsOf(t, dataDir, "the-thing-special-edition")); n != 2 {
		t.Errorf("survivor holds %d recordings, want 2", n)
	}
	assertTreeValid(t, dataDir)
}

// kateWiseGuardTree holds "If She Knew" (one recording, Bea Reader, 300 minutes)
// at position `at` of the SURVIVOR "Kate Wise Mystery Series", with the retired
// spelling "a-kate-wise-mystery" tombstoned onto it: the tree in which a row
// naming the retired spelling meets the same-title serial guard. extra seeds more.
func kateWiseGuardTree(t *testing.T, at string, extra map[string]string) string {
	t.Helper()
	files := map[string]string{
		"works/if/if-she-knew/work.json": testpack.WorkJSON(t, "if-she-knew", "If She Knew", testpack.WithAuthors("ada-mapmaker")),
		"works/if/if-she-knew/recordings/r1.json": testpack.RecJSON(t, "r1", "if-she-knew",
			testpack.WithNarrators("bea-reader"), testpack.WithRuntime(300)),
		"series/ka/kate-wise-mystery-series.json": testpack.SeriesJSON(t, "kate-wise-mystery-series",
			"Kate Wise Mystery Series", "if-she-knew@"+at),
	}
	for k, v := range extra {
		files[k] = v
	}
	return seedTombstoneTree(t, files, model.Redirects{model.RedirectSeries: {"a-kate-wise-mystery": "kate-wise-mystery-series"}})
}

// TestSerialGuardReadsTheSurvivorThroughATombstone: the same-title serial guard
// compares a row's series position with the ones the incumbent recording's work
// holds, and both sides must name the SAME series. A row stating the retired
// spelling reaches the survivor through the tombstone, so its volume 2 is not the
// survivor's volume 1 - keyed by NAME, the two sides named different series and
// volume 2's ASIN merged onto volume 1's recording.
func TestSerialGuardReadsTheSurvivorThroughATombstone(t *testing.T) {
	dataDir := kateWiseGuardTree(t, "1", nil)
	sum := runRecordingsOnly(t, dataDir,
		tombRow("B0TOMBS008", "If She Knew", "Ada Mapmaker", "Bea Reader", 302, "A Kate Wise Mystery", "2")+"\n", false)

	if sum.MergedASINs != 0 {
		t.Errorf("MergedASINs = %d, want 0: volume 2's ASIN was merged onto volume 1's recording", sum.MergedASINs)
	}
	assertTreeValid(t, dataDir)
}

// TestSerialGuardIgnoresASeriesTheNameDoesNotReach is the other direction: a live
// series that merely SHARES the retired spelling's name (at the numbered slug
// past the tombstone) is not the series the row names - the tombstoned base
// resolves to the survivor first - so its position must not block a re-release of
// the very volume the survivor places the work at.
func TestSerialGuardIgnoresASeriesTheNameDoesNotReach(t *testing.T) {
	dataDir := kateWiseGuardTree(t, "2", map[string]string{
		"series/ak/a-kate-wise-mystery-2.json": testpack.SeriesJSON(t, "a-kate-wise-mystery-2",
			"A Kate Wise Mystery", "if-she-knew@1"),
	})
	sum := runRecordingsOnly(t, dataDir,
		tombRow("B0TOMBS009", "If She Knew", "Ada Mapmaker", "Bea Reader", 302, "A Kate Wise Mystery", "2")+"\n", false)

	if sum.MergedASINs != 1 {
		t.Errorf("MergedASINs = %d, want 1: the survivor places the work at 2, so the row is a re-release: %v",
			sum.MergedASINs, sum.Warnings)
	}
	assertTreeValid(t, dataDir)
}
