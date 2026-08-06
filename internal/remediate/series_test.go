package remediate

import (
	"reflect"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/testpack"
)

// TestDramatizedSeriesCollapsesPartsOntoTheBookPosition: a dramatized series
// parks a book's parts at decimal positions, which is one book, not three.
func TestDramatizedSeriesCollapsesPartsOntoTheBookPosition(t *testing.T) {
	dir := t.TempDir()
	seedWorks(t, dir, blackPrismParts()...)
	seedSeries(t, dir, "lightbringer-dramatized-adaptation", "Lightbringer [Dramatized Adaptation]", [][2]string{
		{"black-prism-1-of-3-dramatized-adaptation", "1.1"},
		{"black-prism-2-of-3-dramatized-adaptation", "1.2"},
		{"black-prism-3-of-3-dramatized-adaptation", "1.3"},
	})

	run(t, dir, true, "")

	got := readSeries(t, dir, "lightbringer-dramatized-adaptation")
	want := []string{"black-prism-dramatized-adaptation@1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("series = %v, want %v", got, want)
	}
}

// TestDramatizedSeriesCollapseCollisionIsRefused: collapsing onto a position a
// DIFFERENT work already holds would make two books share a slot, so the run
// leaves both the series and the book's parts exactly as it found them.
func TestDramatizedSeriesCollapseCollisionIsRefused(t *testing.T) {
	dir := t.TempDir()
	works := append(blackPrismParts(), workSpec{
		Slug: "some-other-book-dramatized-adaptation", Title: "Some Other Book [Dramatized Adaptation]",
		Authors: []string{"brent-weeks"}, Rec: recSpec{Key: "full-cast-2021", ASINs: []string{"us:B0OTHER001"}},
	})
	seedWorks(t, dir, works...)
	seedSeries(t, dir, "lightbringer-dramatized-adaptation", "Lightbringer [Dramatized Adaptation]", [][2]string{
		{"black-prism-1-of-3-dramatized-adaptation", "1.1"},
		{"black-prism-2-of-3-dramatized-adaptation", "1.2"},
		{"some-other-book-dramatized-adaptation", "1"},
	})

	rep := run(t, dir, true, "")

	if len(rep.Merged) != 0 {
		t.Errorf("merged = %+v, want nothing merged", rep.Merged)
	}
	if len(refusalsIn(rep, catSeriesCollision)) == 0 {
		t.Errorf("refusals = %v, want a series-collision refusal", rep.Refusals)
	}
	for _, p := range blackPrismParts()[:2] {
		if !testpack.Exists(t, dir, workAddr(p.Slug)) {
			t.Errorf("part %s was deleted even though its series could not be repaired", p.Slug)
		}
	}
	got := readSeries(t, dir, "lightbringer-dramatized-adaptation")
	want := []string{
		"black-prism-1-of-3-dramatized-adaptation@1.1",
		"black-prism-2-of-3-dramatized-adaptation@1.2",
		"some-other-book-dramatized-adaptation@1",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("series = %v, want it untouched (%v)", got, want)
	}
}

// TestPlainSeriesSlotGoesToThePlainEdition: a plain series' numeric slot names
// the book, so the plain text edition takes it back - matched article-tolerantly,
// because Audible drops "The" from the dramatization's title and keeps it on the
// plain one.
func TestPlainSeriesSlotGoesToThePlainEdition(t *testing.T) {
	dir := t.TempDir()
	works := append(blackPrismParts(), workSpec{
		Slug: "the-black-prism", Title: "The Black Prism", Authors: []string{"brent-weeks"},
		Rec: recSpec{Key: "simon-vance-2010", ASINs: []string{"us:B003PLAIN1"}, Publisher: "Orbit"},
	})
	seedWorks(t, dir, works...)
	seedSeries(t, dir, "lightbringer-saga", "Lightbringer Saga", [][2]string{
		{"black-prism-3-of-3-dramatized-adaptation", "1"},
	})

	rep := run(t, dir, true, "")
	if rep.SwappedToPlain != 1 {
		t.Errorf("swapped %d slots, want 1", rep.SwappedToPlain)
	}
	got := readSeries(t, dir, "lightbringer-saga")
	if want := []string{"the-black-prism@1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("series = %v, want %v", got, want)
	}
	// The merge still happened; only the series slot moved.
	if !testpack.Exists(t, dir, workAddr("black-prism-dramatized-adaptation")) {
		t.Errorf("the merged dramatization must still exist")
	}
}

// TestPlainSeriesKeepsTheDramatizationWhenNoPlainEditionExists is the other
// half: the tool never invents a work, so the slot keeps the merged
// dramatization and the book is reported as still lacking a plain edition.
func TestPlainSeriesKeepsTheDramatizationWhenNoPlainEditionExists(t *testing.T) {
	dir := t.TempDir()
	seedWorks(t, dir, blackPrismParts()...)
	seedSeries(t, dir, "lightbringer-saga", "Lightbringer Saga", [][2]string{
		{"black-prism-1-of-3-dramatized-adaptation", "1"},
		{"black-prism-2-of-3-dramatized-adaptation", "2"},
	})

	rep := run(t, dir, true, "")

	got := readSeries(t, dir, "lightbringer-saga")
	if want := []string{"black-prism-dramatized-adaptation@1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("series = %v, want the two hijacked slots collapsed onto one (%v)", got, want)
	}
	if len(rep.MissingPlain) != 1 || rep.MissingPlain[0].Base != "Black Prism" {
		t.Fatalf("missing-plain = %+v, want the one book", rep.MissingPlain)
	}
	if got, want := rep.MissingPlain[0].Series, []string{"lightbringer-saga"}; !reflect.DeepEqual(got, want) {
		t.Errorf("missing-plain series = %v, want %v", got, want)
	}
}

// TestARecordedSlotBeatsAHijackedOne: when the merged work already sits in the
// series at its own position, the slot a part took is dropped rather than
// leaving one book at two positions.
func TestARecordedSlotBeatsAHijackedOne(t *testing.T) {
	dir := t.TempDir()
	works := append(blackPrismParts(), workSpec{
		Slug: "black-prism-dramatized-adaptation", Title: "Black Prism [Dramatized Adaptation]",
		Authors: []string{"brent-weeks"},
		Rec:     recSpec{Key: "full-cast-2019", ASINs: []string{"us:B09FRBS927"}, Runtime: 1173},
	})
	seedWorks(t, dir, works...)
	seedSeries(t, dir, "lightbringer-saga", "Lightbringer Saga", [][2]string{
		{"black-prism-1-of-3-dramatized-adaptation", "3"},
		{"black-prism-dramatized-adaptation", "1"},
	})

	run(t, dir, true, "")

	got := readSeries(t, dir, "lightbringer-saga")
	if want := []string{"black-prism-dramatized-adaptation@1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("series = %v, want the recorded slot kept and the hijacked one dropped (%v)", got, want)
	}
}

// TestOmnibusPositionsAreRefused: a range position ("1-3") names several books,
// so it has no single floor to collapse onto.
func TestOmnibusPositionsAreRefused(t *testing.T) {
	dir := t.TempDir()
	seedWorks(t, dir, blackPrismParts()...)
	seedSeries(t, dir, "lightbringer-saga", "Lightbringer Saga", [][2]string{
		{"black-prism-1-of-3-dramatized-adaptation", "1-3"},
	})

	rep := run(t, dir, true, "")
	if len(refusalsIn(rep, catSeriesPosition)) == 0 {
		t.Errorf("refusals = %v, want a series-position refusal", rep.Refusals)
	}
	if len(rep.Merged) != 0 {
		t.Errorf("merged = %+v, want nothing merged behind an unrepairable series", rep.Merged)
	}
}

func TestFloorPosition(t *testing.T) {
	cases := []struct {
		in   string
		want int
		ok   bool
	}{
		{"1", 1, true}, {"1.1", 1, true}, {"1.5", 1, true}, {"12.10", 12, true},
		{"0", 0, true}, {"1-3", 0, false}, {"1-3.5", 0, false}, {"", 0, false},
		{"1.", 0, false}, {".5", 0, false}, {"a", 0, false},
	}
	for _, c := range cases {
		got, ok := floorPosition(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("floorPosition(%q) = %d,%v; want %d,%v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestPositionLess(t *testing.T) {
	if !positionLess("2", "10") {
		t.Error("positions must order numerically, not lexicographically")
	}
	if !positionLess("1.5", "2") {
		t.Error("1.5 sorts before 2")
	}
	if !positionLess("3", "1-3") {
		t.Error("a plain number sorts before a range")
	}
}
