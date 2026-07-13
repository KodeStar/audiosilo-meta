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
	}
	for _, tt := range tests {
		if got := splitPeople(tt.in); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("splitPeople(%q) = %v, want %v", tt.in, got, tt.want)
		}
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
