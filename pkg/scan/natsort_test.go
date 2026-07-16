package scan

import (
	"reflect"
	"sort"
	"testing"
)

func TestNaturalLess(t *testing.T) {
	order := func(in []string) []string {
		out := append([]string(nil), in...)
		sort.SliceStable(out, func(i, j int) bool { return naturalLess(out[i], out[j]) })
		return out
	}
	cases := []struct {
		name     string
		in, want []string
	}{
		{"unpadded", []string{"ch10", "ch1", "ch2"}, []string{"ch1", "ch2", "ch10"}},
		{"mixed padding", []string{"Chapter 10.mp3", "Chapter 2.mp3", "Chapter 1.mp3", "Chapter 02.mp3"},
			[]string{"Chapter 1.mp3", "Chapter 02.mp3", "Chapter 2.mp3", "Chapter 10.mp3"}},
		{"case-insensitive words", []string{"Beta 2", "alpha 10", "alpha 2"},
			[]string{"alpha 2", "alpha 10", "Beta 2"}},
		{"non-numeric", []string{"gamma", "alpha", "beta"}, []string{"alpha", "beta", "gamma"}},
		{"leading zero equals value", []string{"track 03", "track 3", "track 1"},
			[]string{"track 1", "track 03", "track 3"}},
	}
	for _, c := range cases {
		if got := order(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: natural order = %v, want %v", c.name, got, c.want)
		}
	}
	// Ties are stable: equal keys keep their input order under SliceStable.
	dup := order([]string{"track 3", "track 3", "track 1"})
	if !reflect.DeepEqual(dup, []string{"track 1", "track 3", "track 3"}) {
		t.Errorf("stable tie order = %v", dup)
	}
	// The comparator is a strict weak ordering: never a < a.
	if naturalLess("Chapter 2.mp3", "Chapter 2.mp3") {
		t.Error("naturalLess must be false for equal inputs")
	}
}

// TestFolderBookNaturalFileOrder proves a folder book's Files array (its track
// order) is numeric-aware, so unpadded "Chapter 2.mp3" precedes "Chapter 10.mp3"
// instead of the old lexicographic order that put "10" before "2".
func TestFolderBookNaturalFileOrder(t *testing.T) {
	root := mkTree(t,
		"Some Author/Unpadded Book/Chapter 1.mp3",
		"Some Author/Unpadded Book/Chapter 2.mp3",
		"Some Author/Unpadded Book/Chapter 10.mp3",
		"Some Author/Unpadded Book/Chapter 11.mp3",
	)

	res, _ := scanNoProbe(t, root)

	var book *Book
	for i := range res.Books {
		if res.Books[i].Path == "Some Author/Unpadded Book" {
			book = &res.Books[i]
			break
		}
	}
	if book == nil {
		t.Fatalf("expected one folder book, got %d books: %+v", len(res.Books), res.Books)
	}
	want := []string{"Chapter 1.mp3", "Chapter 2.mp3", "Chapter 10.mp3", "Chapter 11.mp3"}
	if !reflect.DeepEqual(book.Files, want) {
		t.Errorf("Files = %v, want natural order %v", book.Files, want)
	}
}
