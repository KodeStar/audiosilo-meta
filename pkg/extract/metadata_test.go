package extract

import (
	"reflect"
	"testing"
)

// TestISBNFromAcceptsOnlyRealISBNs is the identity guard. A dc:identifier is far
// more often a UUID or a publisher key than an ISBN, and the ISBN decides which
// catalogue work a book is attached to - so a value must pass its check digit
// before it is called one.
func TestISBNFromAcceptsOnlyRealISBNs(t *testing.T) {
	accepted := []struct{ value, scheme, want string }{
		{"urn:isbn:9780141439518", "", "9780141439518"},
		{"URN:ISBN:978-0-14-143951-8", "", "9780141439518"},
		{"9780141439518", "ISBN", "9780141439518"},
		{"isbn:9780141439518", "", "9780141439518"},
		{"0-14-143951-3", "ISBN", "0141439513"}, // valid ISBN-10, hyphens stripped
		{"080442957X", "", "080442957X"},        // ISBN-10 with an X check digit
		{"9780141439518", "", "9780141439518"},  // bare, untagged, but valid
	}
	for _, c := range accepted {
		got, ok := isbnFrom(c.value, c.scheme)
		if !ok || got != c.want {
			t.Errorf("isbnFrom(%q, %q) = (%q, %v), want (%q, true)", c.value, c.scheme, got, ok, c.want)
		}
	}

	denied := []struct{ value, scheme string }{
		{"urn:uuid:9f1a2b3c-4d5e-6f70-8192-a3b4c5d6e7f8", ""},
		{"c2a1e0f4-1111-2222-3333-444455556666", ""},
		{"9780141439519", ""},     // ISBN-13 with a bad check digit
		{"0-14-143951-2", "ISBN"}, // ISBN-10 with a bad check digit
		{"1234567890123", ""},     // 13 digits, not a 978/979 prefix
		{"calibre:1234", ""},
		{"", ""},
		{"9780141439518000", "ISBN"}, // too long
	}
	for _, c := range denied {
		if got, ok := isbnFrom(c.value, c.scheme); ok {
			t.Errorf("isbnFrom(%q, %q) = (%q, true), want rejected", c.value, c.scheme, got)
		}
	}
}

func TestMetadataOfEPUB2Calibre(t *testing.T) {
	pkg := &opfPackage{Meta: opfMetadata{
		Titles:     []opfText{{Value: "The Lightning Thief"}},
		Creators:   []opfText{{Value: "Rick Riordan", Role: "aut"}, {Value: "Jesse Bernstein", Role: "nrt"}},
		Languages:  []string{"en"},
		Publishers: []string{"Puffin"},
		Identifiers: []opfIdent{
			{Value: "urn:uuid:1111-2222", ID: "uuid_id"},
			{Value: "9780141346809", Scheme: "ISBN"},
		},
		Metas: []opfMeta{
			{Name: "calibre:series", Content: "Percy Jackson and the Olympians"},
			{Name: "calibre:series_index", Content: "1.00"},
		},
	}}
	got := metadataOf(pkg)

	if got.Title != "The Lightning Thief" || got.Language != "en" || got.Publisher != "Puffin" {
		t.Errorf("title/language/publisher = %q/%q/%q", got.Title, got.Language, got.Publisher)
	}
	// Only the creator tagged as an author; the narrator is not an author.
	if !reflect.DeepEqual(got.Authors, []string{"Rick Riordan"}) {
		t.Errorf("Authors = %v, want [Rick Riordan]", got.Authors)
	}
	if got.ISBN != "9780141346809" {
		t.Errorf("ISBN = %q, want 9780141346809", got.ISBN)
	}
	if got.Series != "Percy Jackson and the Olympians" || got.SeriesPos != "1" {
		t.Errorf("series = %q / %q, want the series and a trimmed 1", got.Series, got.SeriesPos)
	}
	// The UUID is still recorded verbatim, just never mistaken for an ISBN.
	if got.Identifiers["urn"] != "urn:uuid:1111-2222" {
		t.Errorf("Identifiers = %v, want the uuid retained", got.Identifiers)
	}
}

func TestMetadataOfEPUB3Refines(t *testing.T) {
	pkg := &opfPackage{Meta: opfMetadata{
		Titles: []opfText{
			{Value: "A Study in Scarlet", ID: "t1"},
			{Value: "A Sherlock Holmes Mystery", ID: "t2"},
		},
		Creators: []opfText{{Value: "Arthur Conan Doyle", ID: "c1"}},
		Metas: []opfMeta{
			{Refines: "#t2", Property: "title-type", Value: "subtitle"},
			{Refines: "#c1", Property: "role", Value: "aut"},
			{ID: "col", Property: "belongs-to-collection", Value: "Sherlock Holmes"},
			{Refines: "#col", Property: "group-position", Value: "1"},
		},
	}}
	got := metadataOf(pkg)

	if got.Title != "A Study in Scarlet" || got.Subtitle != "A Sherlock Holmes Mystery" {
		t.Errorf("title/subtitle = %q / %q", got.Title, got.Subtitle)
	}
	if !reflect.DeepEqual(got.Authors, []string{"Arthur Conan Doyle"}) {
		t.Errorf("Authors = %v", got.Authors)
	}
	if got.Series != "Sherlock Holmes" || got.SeriesPos != "1" {
		t.Errorf("series = %q / %q, want Sherlock Holmes / 1", got.Series, got.SeriesPos)
	}
}

// TestMetadataOfKeepsUntaggedCreators: most epubs record no role at all. Dropping
// every creator because none is tagged "aut" would lose the author entirely, which
// is the field identity matching leans on hardest.
func TestMetadataOfKeepsUntaggedCreators(t *testing.T) {
	pkg := &opfPackage{Meta: opfMetadata{
		Titles:   []opfText{{Value: "Dracula"}},
		Creators: []opfText{{Value: "Bram Stoker"}, {Value: "Some Editor"}},
	}}
	got := metadataOf(pkg)
	if !reflect.DeepEqual(got.Authors, []string{"Bram Stoker", "Some Editor"}) {
		t.Errorf("Authors = %v, want both creators kept when no role is stated", got.Authors)
	}
}

// TestMetadataOfIgnoresCollectionTitle: EPUB 3 types each dc:title, and a
// "collection" title is the SERIES name. Taking it as the book's title (it is
// often written first) would attach the book to the wrong catalogue work - and a
// series name reads as a perfectly plausible title, so nothing downstream could
// catch it.
func TestMetadataOfIgnoresCollectionTitle(t *testing.T) {
	pkg := &opfPackage{Meta: opfMetadata{
		Titles: []opfText{
			{Value: "The Lord of the Rings", ID: "t1"},
			{Value: "The Fellowship of the Ring", ID: "t2"},
		},
		Metas: []opfMeta{
			{Refines: "#t1", Property: "title-type", Value: "collection"},
			{Refines: "#t2", Property: "title-type", Value: "main"},
		},
	}}
	if got := metadataOf(pkg); got.Title != "The Fellowship of the Ring" {
		t.Errorf("Title = %q, want the main title, not the collection", got.Title)
	}
}

// TestTrimSeriesIndex: calibre writes the index as a fixed-point number, so the
// catalogue's "1" and "1.5" arrive as "1.00" and "1.50".
func TestTrimSeriesIndex(t *testing.T) {
	cases := map[string]string{
		"1.00": "1", "1.0": "1", "1.50": "1.5", "2.25": "2.25",
		"10": "10", "1": "1", "": "", "1.5a": "1.5a", "first": "first",
	}
	for in, want := range cases {
		if got := trimSeriesIndex(in); got != want {
			t.Errorf("trimSeriesIndex(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMetadataOfEmpty(t *testing.T) {
	got := metadataOf(&opfPackage{})
	if got == nil {
		t.Fatal("metadataOf(empty) = nil, want an empty Metadata")
	}
	if got.Title != "" || len(got.Authors) != 0 || got.ISBN != "" || got.Series != "" {
		t.Errorf("metadataOf(empty) = %+v, want everything empty rather than guessed", got)
	}
}
