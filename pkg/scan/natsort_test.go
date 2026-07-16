package scan

import (
	"reflect"
	"sort"
	"testing"
)

func TestNaturalLess(t *testing.T) {
	order := func(in []string) []string {
		out := append([]string(nil), in...)
		sort.SliceStable(out, func(i, j int) bool { return NaturalLess(out[i], out[j]) })
		return out
	}
	cases := []struct {
		name     string
		in, want []string
	}{
		// Unpadded numbers order by value, not lexically: "2" < "10".
		{"unpadded", []string{"ch10", "ch1", "ch2"}, []string{"ch1", "ch2", "ch10"}},
		{"unpadded chapter", []string{"Chapter 10", "Chapter 2", "Chapter 1"},
			[]string{"Chapter 1", "Chapter 2", "Chapter 10"}},
		{"mixed padding", []string{"Chapter 10.mp3", "Chapter 2.mp3", "Chapter 1.mp3", "Chapter 02.mp3"},
			[]string{"Chapter 1.mp3", "Chapter 02.mp3", "Chapter 2.mp3", "Chapter 10.mp3"}},
		{"case-insensitive words", []string{"Beta 2", "alpha 10", "alpha 2"},
			[]string{"alpha 2", "alpha 10", "Beta 2"}},
		{"non-numeric", []string{"gamma", "alpha", "beta"}, []string{"alpha", "beta", "gamma"}},
		// Leading zeros do not change value ("03" == "3"); the raw byte tiebreak
		// then keeps a deterministic order (space+'0' < space+'3', so "03" first).
		{"leading zero equals value", []string{"track 03", "track 3", "track 1"},
			[]string{"track 1", "track 03", "track 3"}},
		// A shared numeric run followed by differing suffixes falls back to the
		// suffix compare, not to the (equal) number.
		{"shared numeric run then suffix", []string{"Chapter 3 - End", "Chapter 3 - Begin"},
			[]string{"Chapter 3 - Begin", "Chapter 3 - End"}},
	}
	for _, c := range cases {
		if got := order(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: natural order = %v, want %v", c.name, got, c.want)
		}
	}

	// Direct comparator assertions for the tricky tie-break rules.
	direct := []struct {
		a, b string
		want bool // want NaturalLess(a, b)
	}{
		{"Chapter 2", "Chapter 10", true},  // unpadded: value compare, 2 < 10
		{"Chapter 10", "Chapter 2", false}, // and its reverse
		{"track 02", "track 2", true},      // equal value, raw tiebreak: '0' < '2'
		{"track 2", "track 02", false},     // reverse of the raw tiebreak
		{"ALPHA 2", "alpha 2", true},       // case-insensitive equal, then raw: 'A' < 'a'
		{"file 5 a", "file 5 b", true},     // differ only after a shared numeric run
		{"file 5 b", "file 5 a", false},    // reverse
	}
	for _, d := range direct {
		if got := NaturalLess(d.a, d.b); got != d.want {
			t.Errorf("NaturalLess(%q, %q) = %v, want %v", d.a, d.b, got, d.want)
		}
	}

	// Ties are stable: equal keys keep their input order under SliceStable.
	dup := order([]string{"track 3", "track 3", "track 1"})
	if !reflect.DeepEqual(dup, []string{"track 1", "track 3", "track 3"}) {
		t.Errorf("stable tie order = %v", dup)
	}
	// The comparator is a strict weak ordering: never a < a.
	if NaturalLess("Chapter 2.mp3", "Chapter 2.mp3") {
		t.Error("NaturalLess must be false for equal inputs")
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
