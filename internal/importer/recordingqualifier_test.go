package importer

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/testpack"
)

// A RECORDING-qualified series name (titlerule.StripSeriesRecordingQualifiers) names
// the series it is a recording of: narration, abridgement and production are facts
// about a recording, never a series. Every test here mirrors a shape the tree held on
// 2026-10-11 - "Harry Potter (Narrated by Stephen Fry)", "Harry Potter (Full-Cast
// Editions)", "Harry Potter - Gesprochen von Rufus Beck", "Discworld: Tiffany Aching
// (abridged)" - each a series of its own beside the plain one, stamping a second and
// third series onto every book a server matched.

// A Fry-narrated or full-cast row joins `harry-potter` and lands as a recording of the
// book the series holds at that position: no series and no work is founded.
func TestARecordingQualifiedClaimJoinsTheSeries(t *testing.T) {
	for _, name := range []string{"Harry Potter (Narrated by Stephen Fry)", "Harry Potter (Full-Cast Editions)"} {
		dataDir := hpTree(t, "Harry Potter")
		sum := runLibexOver(t, dataDir,
			tombRow("B0HPFRY001", "Philosophers Stone", "J. K. Rowling", "Stephen Fry", 500, name, "1"))
		if sum.NewSeries != 0 || sum.NewWorks != 0 {
			t.Errorf("%q: NewSeries/NewWorks = %d/%d, want 0/0", name, sum.NewSeries, sum.NewWorks)
		}
		for _, slug := range []string{"harry-potter-narrated-by-stephen-fry", "harry-potter-full-cast-editions", "harry-potter-2"} {
			if entryExists(t, dataDir, seriesAddr(slug)) {
				t.Errorf("%q: series %s was founded", name, slug)
			}
		}
		want := map[string]string{"philosophers-stone": "1", "chamber-of-secrets": "2"}
		if got := seriesWorks(t, dataDir, "harry-potter"); !reflect.DeepEqual(got, want) {
			t.Errorf("%q: harry-potter = %v, want %v", name, got, want)
		}
		if recs := recSlugsOf(t, dataDir, "philosophers-stone"); len(recs) != 2 {
			t.Errorf("%q: philosophers-stone recordings = %v, want the Fry narration beside r1", name, recs)
		}
		assertTreeValid(t, dataDir)
	}
}

// The German separator form: "Harry Potter - Gesprochen von Rufus Beck" is the German
// edition series (closed to the English one by language, reached by the qualifier
// index), and the title's own narrator tail comes off the work identity, so the row is
// a recording of the catalogued "Stein der Weisen".
func TestAGermanNarratorSegmentJoinsTheGermanSeries(t *testing.T) {
	dataDir := hpTree(t, "Harry Potter")
	sum := runLibexOver(t, dataDir,
		langRow("B0HPBECK01", "Stein der Weisen - Gesprochen von Rufus Beck", "J. K. Rowling", "german",
			"Harry Potter - Gesprochen von Rufus Beck", "1"))
	if sum.NewSeries != 0 || sum.NewWorks != 0 {
		t.Errorf("NewSeries/NewWorks = %d/%d, want 0/0", sum.NewSeries, sum.NewWorks)
	}
	if entryExists(t, dataDir, seriesAddr("harry-potter-gesprochen-von-rufus-beck")) {
		t.Error("the narrator-qualified series was founded")
	}
	want := map[string]string{"stein-der-weisen": "1", "kammer-des-schreckens": "2"}
	if got := seriesWorks(t, dataDir, "harry-potter-german-edition"); !reflect.DeepEqual(got, want) {
		t.Errorf("harry-potter-german-edition = %v, want %v", got, want)
	}
	if sum.MergedASINs+sum.NewRecordings != 1 {
		t.Errorf("MergedASINs/NewRecordings = %d/%d, want the row on stein-der-weisen", sum.MergedASINs, sum.NewRecordings)
	}
	assertTreeValid(t, dataDir)
}

// discworldTree is Terry Pratchett's "Discworld: Tiffany Aching" holding "A Hat Full
// of Sky" at 2 and "Wintersmith" at 3, the unabridged Stephen Briggs recordings.
func discworldTree(t *testing.T, extra map[string]string) string {
	t.Helper()
	files := map[string]string{
		"people/te/terry-pratchett.json": testpack.PersonJSON(t, "terry-pratchett", "Terry Pratchett"),
		"people/st/stephen-briggs.json":  testpack.PersonJSON(t, "stephen-briggs", "Stephen Briggs"),
		"series/di/discworld-tiffany-aching.json": testpack.SeriesJSON(t, "discworld-tiffany-aching", "Discworld: Tiffany Aching",
			"a-hat-full-of-sky@2", "wintersmith@3"),
	}
	for w, title := range map[string]string{"a-hat-full-of-sky": "A Hat Full of Sky", "wintersmith": "Wintersmith"} {
		files["works/"+shard(w)+"/"+w+"/work.json"] = testpack.WorkJSON(t, w, title, testpack.WithAuthors("terry-pratchett"))
		files["works/"+shard(w)+"/"+w+"/recordings/stephen-briggs-2006.json"] = testpack.RecJSON(t, "stephen-briggs-2006", w,
			testpack.WithNarrators("stephen-briggs"), testpack.WithRuntime(516), testpack.WithAbridged(false))
	}
	for k, v := range extra {
		files[k] = v
	}
	return seedTombstoneTree(t, files, nil)
}

// wintersmithAbridged is the libex row behind `wintersmith-discworld-novel-35`: Tony
// Robinson's abridged reading, titled "Wintersmith" with the subtitle "(Discworld
// Novel 35)", claiming the abridged Tiffany Aching series at 3.
const wintersmithAbridged = `{"asin":"B0032NAM9Q","title":"Wintersmith","subtitle":"(Discworld Novel 35)","region":"us",` +
	`"language":"english","bookFormat":"abridged","lengthMinutes":260,"authors":[{"name":"Terry Pratchett"}],` +
	`"narrators":[{"name":"Tony Robinson"}],"series":[{"name":"Discworld: Tiffany Aching (abridged)","position":"3"}]}`

// The abridged Wintersmith joins the plain series and lands as an abridged recording
// of the catalogued `wintersmith` - not a `wintersmith-discworld-novel-35` work in a
// `discworld-tiffany-aching-abridged` series - on the create path and on the sync
// bot's attach path alike.
func TestAnAbridgedClaimLandsOnTheCataloguedWork(t *testing.T) {
	for _, opts := range []Options{{}, {ExistingSeriesOnly: true, AttachEditions: true}} {
		dataDir := discworldTree(t, nil)
		sum := runLibexWith(t, dataDir, opts, wintersmithAbridged)
		if sum.NewSeries != 0 || sum.NewWorks != 0 || sum.SeriesClaimsDropped != 0 {
			t.Errorf("%+v: NewSeries/NewWorks/SeriesClaimsDropped = %d/%d/%d, want 0/0/0", opts, sum.NewSeries, sum.NewWorks, sum.SeriesClaimsDropped)
		}
		if entryExists(t, dataDir, seriesAddr("discworld-tiffany-aching-abridged")) || entryExists(t, dataDir, workAddr("wintersmith-discworld-novel-35")) {
			t.Errorf("%+v: the recording-variant series or the duplicate work was minted", opts)
		}
		recs := recSlugsOf(t, dataDir, "wintersmith")
		if len(recs) != 2 {
			t.Fatalf("%+v: wintersmith recordings = %v, want the Tony Robinson reading beside Briggs", opts, recs)
		}
		var rec struct {
			Narrators []string `json:"narrators"`
			Abridged  *bool    `json:"abridged"`
		}
		i := slices.IndexFunc(recs, func(s string) bool { return strings.HasPrefix(s, "tony-robinson") })
		if i < 0 {
			t.Fatalf("%+v: no tony-robinson recording among %v", opts, recs)
		}
		readEntity(t, dataDir, recAddr("wintersmith", recs[i]), &rec)
		if rec.Abridged == nil || !*rec.Abridged {
			t.Errorf("%+v: the abridgement was lost: %+v", opts, rec)
		}
		want := map[string]string{"a-hat-full-of-sky": "2", "wintersmith": "3"}
		if got := seriesWorks(t, dataDir, "discworld-tiffany-aching"); !reflect.DeepEqual(got, want) {
			t.Errorf("%+v: discworld-tiffany-aching = %v, want %v", opts, got, want)
		}
		assertTreeValid(t, dataDir)
	}
}

// The qualifier index reaches a recording-variant series the catalogue ALREADY holds:
// with no plain "Discworld: Tiffany Aching" on the chain, a new volume joins the
// abridged series rather than founding a second series beside it (which the
// duplicate-series advisory would then name); with the plain series held too, the
// chain wins.
func TestARecordingVariantSeriesIsReachedOnlyAfterThePlainOne(t *testing.T) {
	variant := testpack.SeriesJSON(t, "discworld-tiffany-aching-abridged", "Discworld: Tiffany Aching (abridged)",
		"a-hat-full-of-sky@2", "wintersmith@3")
	row := tombRow("B0SHEPHER5", "The Shepherds Crown", "Terry Pratchett", "Stephen Briggs", 400, "Discworld: Tiffany Aching", "5")

	// Only the variant is held: the plain series is removed by seeding the variant
	// over a tree without it.
	dataDir := seedTombstoneTree(t, map[string]string{
		"people/te/terry-pratchett.json": testpack.PersonJSON(t, "terry-pratchett", "Terry Pratchett"),
		"people/st/stephen-briggs.json":  testpack.PersonJSON(t, "stephen-briggs", "Stephen Briggs"),
		"works/a-/a-hat-full-of-sky/work.json": testpack.WorkJSON(t, "a-hat-full-of-sky", "A Hat Full of Sky",
			testpack.WithAuthors("terry-pratchett")),
		"works/a-/a-hat-full-of-sky/recordings/r1.json":    testpack.RecJSON(t, "r1", "a-hat-full-of-sky", testpack.WithNarrators("stephen-briggs")),
		"works/wi/wintersmith/work.json":                   testpack.WorkJSON(t, "wintersmith", "Wintersmith", testpack.WithAuthors("terry-pratchett")),
		"works/wi/wintersmith/recordings/r1.json":          testpack.RecJSON(t, "r1", "wintersmith", testpack.WithNarrators("stephen-briggs")),
		"series/di/discworld-tiffany-aching-abridged.json": variant,
	}, nil)
	sum := runLibexOver(t, dataDir, row)
	if sum.NewSeries != 0 {
		t.Errorf("variant only: NewSeries = %d, want the row in the held abridged series", sum.NewSeries)
	}
	if got := seriesWorks(t, dataDir, "discworld-tiffany-aching-abridged")["the-shepherds-crown"]; got != "5" {
		t.Errorf("variant only: the-shepherds-crown at %q in the abridged series, want 5", got)
	}
	assertTreeValid(t, dataDir)

	dataDir = discworldTree(t, map[string]string{"series/di/discworld-tiffany-aching-abridged.json": variant})
	sum = runLibexOver(t, dataDir, row)
	if sum.NewSeries != 0 || seriesWorks(t, dataDir, "discworld-tiffany-aching")["the-shepherds-crown"] != "5" {
		t.Errorf("both held: NewSeries %d, plain series %v - want the row in the plain series",
			sum.NewSeries, seriesWorks(t, dataDir, "discworld-tiffany-aching"))
	}
	if _, in := seriesWorks(t, dataDir, "discworld-tiffany-aching-abridged")["the-shepherds-crown"]; in {
		t.Error("both held: the row joined the abridged variant")
	}
	assertTreeValid(t, dataDir)
}

// The resolution is a batch snapshot: a plain row and a recording-qualified row of the
// same new series found ONE series together, whatever order they arrive in.
func TestRecordingQualifiedClaimsAreOrderIndependent(t *testing.T) {
	a := tombRow("B0NEWSER01", "First Book", "Ada Mapmaker", "Bea Reader", 300, "Mapmaker Saga", "1")
	b := tombRow("B0NEWSER02", "Second Book", "Ada Mapmaker", "Bea Reader", 300, "Mapmaker Saga (Full Cast Edition)", "2")
	want := map[string]string{"first-book": "1", "second-book": "2"}
	for _, rows := range [][]string{{a, b}, {b, a}} {
		dataDir := seedTombstoneTree(t, nil, nil)
		sum := runLibexOver(t, dataDir, rows...)
		if got := seriesWorks(t, dataDir, "mapmaker-saga"); sum.NewSeries != 1 || !reflect.DeepEqual(got, want) {
			t.Errorf("NewSeries %d, mapmaker-saga = %v; want one series %v", sum.NewSeries, got, want)
		}
		assertTreeValid(t, dataDir)
	}
}
