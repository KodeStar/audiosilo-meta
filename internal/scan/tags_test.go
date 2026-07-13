package scan

import (
	"reflect"
	"testing"
)

func TestASINFromRaw(t *testing.T) {
	tests := []struct {
		desc string
		raw  map[string]interface{}
		want string
	}{
		{"plain ASIN key", map[string]interface{}{"ASIN": "B076HYPQLK"}, "B076HYPQLK"},
		{"lowercase asin key", map[string]interface{}{"asin": "B0CGVKN3KL"}, "B0CGVKN3KL"},
		{"Audible CDEK key", map[string]interface{}{"CDEK": "B08G9PRS1K"}, "B08G9PRS1K"},
		{"AUDIBLE_ASIN key", map[string]interface{}{"AUDIBLE_ASIN": "B07KToo"}, ""}, // invalid value
		{"freeform audible atom", map[string]interface{}{"----:com.audible:ASIN": "B0142RXBIU"}, "B0142RXBIU"},
		{"TXXX frame with []byte value", map[string]interface{}{"TXXX:ASIN": []byte("B00META123")}, "B00META123"},
		{"lowercase value normalized to upper", map[string]interface{}{"asin": "b076hypqlk"}, "B076HYPQLK"},
		{"unrelated key ignored even if value looks like an ASIN", map[string]interface{}{"comment": "B076HYPQLK"}, ""},
		{"no asin anywhere", map[string]interface{}{"title": "A Book"}, ""},
		// A valid non-B0 ASIN in an explicitly ASIN-keyed tag is accepted whole
		// (importer.NormalizeASIN), even though the free-text pattern needs B0.
		{"non-B0 ASIN in an explicit key", map[string]interface{}{"ASIN": "1774248182"}, "1774248182"},
		{"explicit key with surrounding noise falls back to the B0 pattern",
			map[string]interface{}{"ASIN": "asin: B076HYPQLK (us)"}, "B076HYPQLK"},
	}
	for _, tt := range tests {
		if got := asinFromRaw(tt.raw); got != tt.want {
			t.Errorf("%s: asinFromRaw(%v) = %q, want %q", tt.desc, tt.raw, got, tt.want)
		}
	}
}

func TestISBNFromRaw(t *testing.T) {
	tests := []struct {
		raw  map[string]interface{}
		want string
	}{
		{map[string]interface{}{"ISBN": "978-0-399-59050-4"}, "9780399590504"},
		{map[string]interface{}{"isbn13": "9781401238964"}, "9781401238964"},
		{map[string]interface{}{"comment": "9780399590504"}, ""}, // key not isbn-ish
		{map[string]interface{}{"title": "x"}, ""},
	}
	for _, tt := range tests {
		if got := isbnFromRaw(tt.raw); got != tt.want {
			t.Errorf("isbnFromRaw(%v) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}

func TestSplitPeople(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"Lee Child", []string{"Lee Child"}},
		{"Terry Pratchett & Neil Gaiman", []string{"Terry Pratchett", "Neil Gaiman"}},
		{"Author One, Author Two", []string{"Author One", "Author Two"}},
		{"A; B; C", []string{"A", "B", "C"}},
		{"Jane Doe and John Roe", []string{"Jane Doe", "John Roe"}},
		{"  Spaced  ", []string{"Spaced"}},
		// Audible role qualifiers are stripped via importer.SplitNames.
		{"Kirill Klevanski, J. Kharkova - Translator", []string{"Kirill Klevanski", "J. Kharkova"}},
		{"Author One & Jane Doe - narrator", []string{"Author One", "Jane Doe"}},
	}
	for _, tt := range tests {
		if got := splitPeople(tt.in); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("splitPeople(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestMergeTags(t *testing.T) {
	d := tagInfo{
		album:       "Dhowden Album",
		authors:     []string{"Dhowden Author"},
		releaseDate: "2017",
	}
	p := tagInfo{
		album:       "Probe Album",
		narrators:   []string{"Probe Narrator"},
		publisher:   "Probe Publisher",
		releaseDate: "2017-11-02",
	}
	got := mergeTags(d, p)
	if got.album != "Dhowden Album" {
		t.Errorf("album: dhowden must win, got %q", got.album)
	}
	if len(got.authors) != 1 || got.authors[0] != "Dhowden Author" {
		t.Errorf("authors: dhowden must win, got %v", got.authors)
	}
	if len(got.narrators) != 1 || got.narrators[0] != "Probe Narrator" {
		t.Errorf("narrators: probe must fill the gap, got %v", got.narrators)
	}
	if got.publisher != "Probe Publisher" {
		t.Errorf("publisher: probe must fill the gap, got %q", got.publisher)
	}
	if got.releaseDate != "2017-11-02" {
		t.Errorf("releaseDate: the fuller date must win, got %q", got.releaseDate)
	}
	// And the fuller date must NOT be replaced by a bare year the other way.
	if got := mergeTags(tagInfo{releaseDate: "2017-11-02"}, tagInfo{releaseDate: "2017"}); got.releaseDate != "2017-11-02" {
		t.Errorf("releaseDate reverse: got %q, want 2017-11-02", got.releaseDate)
	}
}

func TestProbeTagInfo(t *testing.T) {
	got := probeTagInfo(map[string]string{
		"album":     " The Book ",
		"artist":    "Some Author",
		"series":    "The Series",
		"asin":      "B076HYPQLK",
		"publisher": "Pub House",
		"date":      "2020-01-05",
	})
	if got.album != "The Book" || got.authors[0] != "Some Author" || got.series != "The Series" {
		t.Errorf("basic mapping wrong: %+v", got)
	}
	if got.asin != "B076HYPQLK" || got.publisher != "Pub House" || got.releaseDate != "2020-01-05" {
		t.Errorf("identifier/extra mapping wrong: %+v", got)
	}
	if empty := probeTagInfo(nil); !reflect.DeepEqual(empty, tagInfo{}) {
		t.Errorf("nil map must yield a zero tagInfo, got %+v", empty)
	}
}

func TestRawValueString(t *testing.T) {
	if got := rawValueString([]byte("hello")); got != "hello" {
		t.Errorf("[]byte: got %q", got)
	}
	if got := rawValueString("world"); got != "world" {
		t.Errorf("string: got %q", got)
	}
	if got := rawValueString(42); got != "42" {
		t.Errorf("int: got %q", got)
	}
	if got := rawValueString(nil); got != "" {
		t.Errorf("nil: got %q", got)
	}
	if got := rawValueString(stringer{"str"}); got != "str" {
		t.Errorf("Stringer: got %q", got)
	}
}

type stringer struct{ s string }

func (s stringer) String() string { return s.s }
