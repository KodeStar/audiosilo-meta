package remediate

import (
	"reflect"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/testpack"
	"github.com/kodestar/audiosilo-meta/pkg/check"
)

// TestNarratorDisagreementIsRefused: a dramatization's parts really do credit
// different slices of one cast, so overlap is the test. Two parts that share
// not one performer are two productions and the book is left alone.
func TestNarratorDisagreementIsRefused(t *testing.T) {
	dir := t.TempDir()
	parts := blackPrismParts()[:2]
	parts[0].Rec.Narrators = []string{"alice-one", "bob-two"}
	parts[1].Rec.Narrators = []string{"carol-three", "dave-four"}
	seedWorks(t, dir, parts...)

	rep := run(t, dir, true, "")

	if len(refusalsIn(rep, catNarrators)) != 1 {
		t.Fatalf("refusals = %v, want one narrator-disagreement refusal", rep.Refusals)
	}
	for _, p := range parts {
		if !testpack.Exists(t, dir, workAddr(p.Slug)) {
			t.Errorf("part %s was merged despite the refusal", p.Slug)
		}
	}
}

// TestOverlappingCastsMerge is the passing half of the same rule: parts sharing
// even one performer are one production.
func TestOverlappingCastsMerge(t *testing.T) {
	dir := t.TempDir()
	parts := blackPrismParts()[:2]
	parts[0].Rec.Narrators = []string{"alice-one", "bob-two"}
	parts[1].Rec.Narrators = []string{"bob-two", "carol-three"}
	seedWorks(t, dir, parts...)

	run(t, dir, true, "")

	_, rec := readRecording(t, dir, "black-prism-dramatized-adaptation")
	want := []string{"alice-one", "bob-two", "carol-three"}
	if got := rec.strs("narrators"); !reflect.DeepEqual(got, want) {
		t.Errorf("narrators = %v, want the union in first-seen order (%v)", got, want)
	}
}

// TestACollectiveCreditNeverDisagrees: a part credited only to the shared
// "full-cast" record states no identifying narrator, so it can neither agree
// nor disagree - which is what keeps the live Warbreaker-shaped groups mergeable.
func TestACollectiveCreditNeverDisagrees(t *testing.T) {
	dir := t.TempDir()
	parts := blackPrismParts()[:2]
	parts[0].Rec.Narrators = []string{"full-cast"}
	parts[1].Rec.Narrators = []string{"alice-one", "bob-two"}
	seedWorks(t, dir, parts...)

	rep := run(t, dir, true, "")
	if len(refusalsIn(rep, catNarrators)) != 0 {
		t.Errorf("refusals = %v, want none", rep.Refusals)
	}
}

// TestAuthorSplitIsRefused: one book's parts recorded under two author sets
// means one of them is wrong, and choosing would be inventing the answer.
func TestAuthorSplitIsRefused(t *testing.T) {
	dir := t.TempDir()
	parts := blackPrismParts()[:2]
	parts[1].Authors = []string{"brent-weeks", "a-stray-narrator"}
	seedWorks(t, dir, parts...)

	rep := run(t, dir, true, "")

	if got := len(refusalsIn(rep, catAuthorSplit)); got != 2 {
		t.Fatalf("author-split refusals = %d, want both sides refused: %v", got, rep.Refusals)
	}
	for _, p := range parts {
		if !testpack.Exists(t, dir, workAddr(p.Slug)) {
			t.Errorf("part %s was merged despite the refusal", p.Slug)
		}
	}
}

// TestAPartWithSeveralRecordingsIsRefused: which production the part belongs to
// is not stated, so the merge cannot pick one.
func TestAPartWithSeveralRecordingsIsRefused(t *testing.T) {
	dir := t.TempDir()
	parts := blackPrismParts()[:2]
	// A second recording on part 1, the shape 14 live works carry: a duplicate
	// of the same production credited only to the collective record.
	parts[0].Extra = &recSpec{Key: "full-cast-2021", ASINs: []string{"us:B0DUPE0001"}}
	seedWorks(t, dir, parts...)

	rep := run(t, dir, true, "")
	if len(refusalsIn(rep, catMultiRecording)) != 1 {
		t.Fatalf("refusals = %v, want one multi-recording refusal", rep.Refusals)
	}
	if len(rep.Merged) != 0 {
		t.Errorf("merged = %+v, want nothing", rep.Merged)
	}
}

// TestSlugCollisionIsRefused: the merged work would claim an id another work
// already holds, which would silently replace it.
func TestSlugCollisionIsRefused(t *testing.T) {
	dir := t.TempDir()
	works := append(blackPrismParts()[:2], workSpec{
		// Same slug the mint would produce, but a different book (different
		// authors), so it is not the group's complete-set work.
		Slug: "black-prism-dramatized-adaptation", Title: "Black Prism [Dramatized Adaptation]",
		Authors: []string{"someone-else"}, Rec: recSpec{Key: "full-cast-2019", ASINs: []string{"us:B0OTHER999"}},
	})
	seedWorks(t, dir, works...)

	rep := run(t, dir, true, "")
	if len(refusalsIn(rep, catSlugCollision)) != 1 {
		t.Fatalf("refusals = %v, want one slug-collision refusal", rep.Refusals)
	}
	if got := readWork(t, dir, "black-prism-dramatized-adaptation").strs("authors"); !reflect.DeepEqual(got, []string{"someone-else"}) {
		t.Errorf("the existing work was overwritten: authors = %v", got)
	}
}

// TestNonGraphicAudioPartTitlesAreLeftAlone: other publishers really do spell a
// series volume "Book 1 of 10", and those are separate books.
func TestNonGraphicAudioPartTitlesAreLeftAlone(t *testing.T) {
	dir := t.TempDir()
	seedWorks(t, dir,
		workSpec{Slug: "riftborn-1-of-10", Title: "The Riftborn Chronicles (1 of 10): Awakening",
			Authors: []string{"bradford-m-smith"},
			Rec:     recSpec{Key: "reader-2021", Publisher: "Bradford M. Smith", ASINs: []string{"us:B0RIFT0001"}}},
		workSpec{Slug: "riftborn-3-of-10", Title: "The Riftborn Chronicles (3 of 10): The War",
			Authors: []string{"bradford-m-smith"},
			Rec:     recSpec{Key: "reader-2021", Publisher: "Bradford M. Smith", ASINs: []string{"us:B0RIFT0003"}}},
	)

	rep := run(t, dir, true, "")
	if rep.PartWorks != 0 || len(rep.Merged) != 0 {
		t.Fatalf("report = %d part works, %d merges; want the cohort gate to exclude them", rep.PartWorks, len(rep.Merged))
	}
	if !testpack.Exists(t, dir, workAddr("riftborn-1-of-10")) {
		t.Error("a non-GraphicAudio work was merged away")
	}
}

// TestSecondRunIsANoOp: the tool converges, so re-running it after an import
// (or by accident) changes nothing.
func TestSecondRunIsANoOp(t *testing.T) {
	dir := t.TempDir()
	works := append(blackPrismParts(), workSpec{
		Slug: "the-black-prism", Title: "The Black Prism", Authors: []string{"brent-weeks"},
		Rec: recSpec{Key: "simon-vance-2010", ASINs: []string{"us:B003PLAIN1"}, Publisher: "Orbit"},
	})
	seedWorks(t, dir, works...)
	seedSeries(t, dir, "lightbringer-saga", "Lightbringer Saga", [][2]string{
		{"black-prism-1-of-3-dramatized-adaptation", "1"},
	})

	run(t, dir, true, "")
	first := treeBytes(t, dir)

	rep := run(t, dir, true, "")
	if len(rep.Merged) != 0 || len(rep.Deleted) != 0 || len(rep.Series) != 0 {
		t.Errorf("second run planned work: %d merges, %d deletes, %d series", len(rep.Merged), len(rep.Deleted), len(rep.Series))
	}
	if got := treeBytes(t, dir); !reflect.DeepEqual(got, first) {
		t.Errorf("the second run changed the tree")
	}
}

// TestMergesSameASINTwins: two works carrying one identifier are one product,
// and the shorter slug - the one without a series-name tail welded on - is the
// one that survives.
func TestMergesSameASINTwins(t *testing.T) {
	dir := t.TempDir()
	seedWorks(t, dir,
		workSpec{Slug: "dawnshard-dramatized-adaptation", Title: "Dawnshard [Dramatized Adaptation]",
			Authors: []string{"brandon-sanderson"},
			Rec:     recSpec{Key: "full-cast-2024", ASINs: []string{"us:B0DDCXWCHM"}, Runtime: 325}},
		workSpec{Slug: "dawnshard-dramatized-adaptation-the-stormlight-archive",
			Title:   "Dawnshard (Dramatized Adaptation): The Stormlight Archive",
			Authors: []string{"brandon-sanderson"},
			Rec: recSpec{Key: "andy-brownstein-2024", Narrators: []string{"andy-brownstein"},
				ASINs: []string{"us:B0DDCXWCHM", "uk:B0DDCV8PDT"}, Cover: "https://example.test/d.jpg"}},
	)
	seedSeries(t, dir, "stormlight-dramatized-adaptation", "The Stormlight Archive [Dramatized Adaptation]", [][2]string{
		{"dawnshard-dramatized-adaptation-the-stormlight-archive", "3.5"},
	})

	rep := run(t, dir, true, "")

	if len(rep.Twins) != 1 || rep.Twins[0].Survivor != "dawnshard-dramatized-adaptation" {
		t.Fatalf("twins = %+v, want the shorter slug to survive", rep.Twins)
	}
	if testpack.Exists(t, dir, workAddr("dawnshard-dramatized-adaptation-the-stormlight-archive")) {
		t.Error("the absorbed twin survived")
	}
	_, rec := readRecording(t, dir, "dawnshard-dramatized-adaptation")
	if got, want := asinValues(rec), []string{"B0DDCXWCHM", "B0DDCV8PDT"}; !reflect.DeepEqual(got, want) {
		t.Errorf("asin = %v, want the union deduplicated by value (%v)", got, want)
	}
	if got := rec.str("cover_url"); got != "https://example.test/d.jpg" {
		t.Errorf("cover_url = %q, want the absorbed twin's filled in", got)
	}
	if got := readSeries(t, dir, "stormlight-dramatized-adaptation"); !reflect.DeepEqual(got, []string{"dawnshard-dramatized-adaptation@3.5"}) {
		t.Errorf("series = %v, want the reference rewritten onto the survivor", got)
	}
}

// TestTwinsWithDifferentAuthorsAreRefused: an ASIN two DIFFERENT books share is
// a data problem to report, not a merge to perform.
func TestTwinsWithDifferentAuthorsAreRefused(t *testing.T) {
	dir := t.TempDir()
	seedWorks(t, dir,
		workSpec{Slug: "one-dramatized-adaptation", Title: "One [Dramatized Adaptation]",
			Authors: []string{"author-one"}, Rec: recSpec{Key: "full-cast-2024", ASINs: []string{"us:B0SHARED01"}}},
		workSpec{Slug: "two-dramatized-adaptation", Title: "Two [Dramatized Adaptation]",
			Authors: []string{"author-two"}, Rec: recSpec{Key: "full-cast-2024", ASINs: []string{"us:B0SHARED01"}}},
	)

	rep := run(t, dir, true, "")
	if len(refusalsIn(rep, catTwinDisagreement)) != 1 {
		t.Fatalf("refusals = %v, want one twin-disagreement refusal", rep.Refusals)
	}
	if !testpack.Exists(t, dir, workAddr("two-dramatized-adaptation")) {
		t.Error("a refused twin was merged away")
	}
}

// TestTheRepairedTreeValidates runs the project's own validator over the
// result, which is the property every write here has to hold: metacheck-green.
func TestTheRepairedTreeValidates(t *testing.T) {
	dir := t.TempDir()
	works := append(blackPrismParts(), workSpec{
		Slug: "the-black-prism", Title: "The Black Prism", Authors: []string{"brent-weeks"},
		Rec: recSpec{Key: "simon-vance-2010", ASINs: []string{"us:B003PLAIN1"}, Publisher: "Orbit"},
	})
	seedWorks(t, dir, works...)
	seedPeople(t, dir, map[string]string{
		"brent-weeks": "Brent Weeks", "full-cast": "Full Cast", "simon-vance": "Simon Vance",
	})
	seedSeries(t, dir, "lightbringer-saga", "Lightbringer Saga", [][2]string{
		{"black-prism-1-of-3-dramatized-adaptation", "1"},
	})

	if res := check.Load(dir); !res.OK() {
		t.Fatalf("the fixture tree is invalid before the run: %v", res.Problems)
	}
	run(t, dir, true, "")
	if res := check.Load(dir); !res.OK() {
		t.Fatalf("the repaired tree does not validate: %v", res.Problems)
	}
}
