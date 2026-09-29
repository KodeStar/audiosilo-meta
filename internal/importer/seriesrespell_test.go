package importer

import (
	"reflect"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/testpack"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// A series Audible RESPELLS under an unchanged series ASIN - the same decoration
// in other brackets, or with other spacing around them - is the series the
// catalogue already holds. The chain walk used to compare names with EqualFold,
// stepped past the catalogued spelling (and past a retired numbered candidate)
// and founded a `-2`/`-3` duplicate, or, under ExistingSeriesOnly, dropped the
// claim and left the book in no series. That is how both duplicates #2441 folded
// arose: libex B0G6YBCNQF "NOMADS Legacy (German Edition)" against the catalogued
// "NOMADS Legacy [German Edition]", and B0DQCSB5Z4 "Throne of Glass [French
// Edition]" against "Throne of Glass[French Edition]".

// respellTree holds a two-volume series by Ada Mapmaker under the given name, and
// the given tombstones.
func respellTree(t *testing.T, slug, name string, reds model.Redirects) string {
	t.Helper()
	files := map[string]string{
		"series/" + slug[:2] + "/" + slug + ".json": testpack.SeriesJSON(t, slug, name, "band-one@1", "band-two@2"),
	}
	for _, w := range []string{"band-one", "band-two"} {
		files["works/"+shard(w)+"/"+w+"/work.json"] = testpack.WorkJSON(t, w, w, testpack.WithAuthors("ada-mapmaker"))
		files["works/"+shard(w)+"/"+w+"/recordings/r1.json"] = testpack.RecJSON(t, "r1", w, testpack.WithNarrators("bea-reader"))
	}
	return seedTombstoneTree(t, files, reds)
}

func TestARespelledDecorationJoinsTheCataloguedSeries(t *testing.T) {
	const slug = "nomads-legacy-german-edition"
	for _, existingOnly := range []bool{false, true} {
		dataDir := respellTree(t, slug, "NOMADS Legacy [German Edition]", nil)
		sum := runLibexWith(t, dataDir, Options{ExistingSeriesOnly: existingOnly},
			tombRow("B0G6YBCNQF", "Band Three", "Ada Mapmaker", "Bea Reader", 300, "NOMADS Legacy (German Edition)", "3"))

		if sum.NewWorks != 1 || sum.NewSeries != 0 || sum.SeriesClaimsDropped != 0 {
			t.Errorf("existingOnly=%v: NewWorks/NewSeries/SeriesClaimsDropped = %d/%d/%d, want 1/0/0",
				existingOnly, sum.NewWorks, sum.NewSeries, sum.SeriesClaimsDropped)
		}
		want := map[string]string{"band-one": "1", "band-two": "2", "band-three": "3"}
		if got := seriesWorks(t, dataDir, slug); !reflect.DeepEqual(got, want) {
			t.Errorf("existingOnly=%v: %s = %v, want %v", existingOnly, slug, got, want)
		}
		if entryExists(t, dataDir, seriesAddr(slug+"-2")) {
			t.Errorf("existingOnly=%v: a respelled decoration founded %s-2", existingOnly, slug)
		}
		assertTreeValid(t, dataDir)
	}
}

// The Throne of Glass shape: the spacing before the bracket differs, and the
// chain's -2 is a retired candidate the old walk stepped past to found -3.
func TestARespelledDecorationJoinsPastARetiredCandidate(t *testing.T) {
	const slug = "throne-of-glass-french-edition"
	reds := model.Redirects{model.RedirectSeries: {slug + "-2": slug}}
	for _, existingOnly := range []bool{false, true} {
		dataDir := respellTree(t, slug, "Throne of Glass[French Edition]", reds)
		sum := runLibexWith(t, dataDir, Options{ExistingSeriesOnly: existingOnly},
			tombRow("B0DQCSB5Z4", "Band Three", "Ada Mapmaker", "Bea Reader", 300, "Throne of Glass [French Edition]", "3"))

		if sum.NewSeries != 0 || sum.SeriesClaimsDropped != 0 {
			t.Errorf("existingOnly=%v: NewSeries/SeriesClaimsDropped = %d/%d, want 0/0", existingOnly, sum.NewSeries, sum.SeriesClaimsDropped)
		}
		if got := seriesWorks(t, dataDir, slug); got["band-three"] != "3" {
			t.Errorf("existingOnly=%v: %s = %v, want band-three at 3", existingOnly, slug, got)
		}
		if entryExists(t, dataDir, seriesAddr(slug+"-3")) {
			t.Errorf("existingOnly=%v: a respelled decoration founded %s-3", existingOnly, slug)
		}
		assertTreeValid(t, dataDir)
	}
}

// Widening NAME equality never widens who may join: the author-aware fit still
// judges the series the respelled name reaches, so another author's row steps
// past it to its own series exactly as it would under the catalogued spelling.
func TestARespelledDecorationStillAsksTheAuthorFit(t *testing.T) {
	const slug = "nomads-legacy-german-edition"
	dataDir := respellTree(t, slug, "NOMADS Legacy [German Edition]", nil)
	sum := runLibexWith(t, dataDir, Options{},
		tombRow("B0OTHER003", "Fremdes Buch", "Carl Stranger", "Bea Reader", 300, "NOMADS Legacy (German Edition)", "3"))

	if sum.NewSeries != 1 {
		t.Errorf("NewSeries = %d, want 1 (the closed series is stepped past)", sum.NewSeries)
	}
	if got := seriesWorks(t, dataDir, slug); len(got) != 2 {
		t.Errorf("%s = %v, want its own two volumes only", slug, got)
	}
	if got := seriesWorks(t, dataDir, slug+"-2"); got["fremdes-buch"] != "3" {
		t.Errorf("%s-2 = %v, want fremdes-buch at 3", slug, got)
	}
	assertTreeValid(t, dataDir)
}

// A DIFFERENT decoration in the same slug bucket is not the same name: a
// punctuation difference outside the groups still founds its own series, as it
// always did.
func TestADifferentSpellingOutsideTheDecorationStaysApart(t *testing.T) {
	const slug = "mr-x-german-edition"
	dataDir := respellTree(t, slug, "Mr. X [German Edition]", nil)
	sum := runLibexWith(t, dataDir, Options{},
		tombRow("B0MRX00003", "Band Three", "Ada Mapmaker", "Bea Reader", 300, "Mr X (German Edition)", "3"))
	if sum.NewSeries != 1 {
		t.Errorf("NewSeries = %d, want 1", sum.NewSeries)
	}
	if got := seriesWorks(t, dataDir, slug+"-2"); got["band-three"] != "3" {
		t.Errorf("%s-2 = %v, want band-three at 3", slug, got)
	}
	assertTreeValid(t, dataDir)
}

// Two rows of ONE batch spelling one decoration two ways are one claim group: they
// found ONE series between them, whatever order they arrive in.
func TestRespelledClaimsInOneBatchFoundOneSeries(t *testing.T) {
	rows := []string{
		tombRow("B0NEWSER01", "Harbour One", "Ada Mapmaker", "Bea Reader", 300, "Harbour Saga [German Edition]", "1"),
		tombRow("B0NEWSER02", "Harbour Two", "Ada Mapmaker", "Bea Reader", 300, "Harbour Saga (German Edition)", "2"),
	}
	names := map[string]string{}
	for name, order := range map[string][]int{"forward": {0, 1}, "reverse": {1, 0}} {
		dataDir := seedTombstoneTree(t, nil, nil)
		var in []string
		for _, i := range order {
			in = append(in, rows[i])
		}
		sum := runLibexWith(t, dataDir, Options{}, in...)
		if sum.NewSeries != 1 {
			t.Errorf("%s: NewSeries = %d, want 1", name, sum.NewSeries)
		}
		want := map[string]string{"harbour-one": "1", "harbour-two": "2"}
		if got := seriesWorks(t, dataDir, "harbour-saga-german-edition"); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: harbour-saga-german-edition = %v, want %v", name, got, want)
		}
		var ser struct {
			Name string `json:"name"`
		}
		readEntity(t, dataDir, seriesAddr("harbour-saga-german-edition"), &ser)
		names[name] = ser.Name
		assertTreeValid(t, dataDir)
	}
	// The spelling the founded series is written under is the group's canonical
	// claim's, never whichever row happened to be placed first.
	if names["forward"] != names["reverse"] {
		t.Errorf("the founded series' name depends on row order: forward %q, reverse %q", names["forward"], names["reverse"])
	}
}

// The libex position lookup reads the same rule: a position libex states under a
// respelled decoration is a position for the row's series.
func TestLookedUpPositionReadsARespelledDecoration(t *testing.T) {
	refs := []SeriesPosition{{Name: "NOMADS Legacy (German Edition)", Position: "3"}}
	if pos, ok := lookedUpPosition(refs, "NOMADS Legacy [German Edition]"); !ok || pos != "3" {
		t.Errorf("lookedUpPosition = (%q, %v), want (3, true)", pos, ok)
	}
	if _, ok := lookedUpPosition(refs, "NOMADS Legacy [French Edition]"); ok {
		t.Error("a different decoration's position was used")
	}
}
