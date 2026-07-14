package importer

import (
	"reflect"
	"testing"
)

func TestMapLanguage(t *testing.T) {
	cases := []struct {
		in     string
		want   string
		wantOK bool
	}{
		{"english", "en", true},
		{"English", "en", true},
		{"  TURKISH ", "tr", true},
		{"german", "de", true},
		{"french", "fr", true},
		{"spanish", "es", true},
		{"italian", "it", true},
		{"japanese", "ja", true},
		{"portuguese", "pt", true},
		{"dutch", "nl", true},
		{"polish", "pl", true},
		{"russian", "ru", true},
		{"chinese", "zh", true},
		{"klingon", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := mapLanguage(c.in)
		if got != c.want || ok != c.wantOK {
			t.Errorf("mapLanguage(%q) = (%q,%v), want (%q,%v)", c.in, got, ok, c.want, c.wantOK)
		}
	}
}

func TestMapRegion(t *testing.T) {
	cases := []struct {
		in     string
		want   string
		wantOK bool
	}{
		{"UK", "uk", true},
		{"US", "us", true},
		{" jp ", "jp", true},
		{"br", "br", true},
		{"XX", "", false},
		{"", "", false},
		{"europe", "", false},
	}
	for _, c := range cases {
		got, ok := mapRegion(c.in)
		if got != c.want || ok != c.wantOK {
			t.Errorf("mapRegion(%q) = (%q,%v), want (%q,%v)", c.in, got, ok, c.want, c.wantOK)
		}
	}
}

func TestNormalizeSequence(t *testing.T) {
	cases := []struct {
		in     string
		want   string
		wantOK bool
	}{
		{"3", "3", true},
		{"0.5", "0.5", true},
		{"1-3.5", "1-3.5", true},
		{" 2 ", "2", true},
		{"12-13", "12-13", true},
		{"", "", false},
		{"one", "", false},
		{"1.2.3", "", false},
		{"1-", "", false},
		{"-2", "", false},
	}
	for _, c := range cases {
		got, ok := NormalizeSequence(c.in)
		if got != c.want || ok != c.wantOK {
			t.Errorf("NormalizeSequence(%q) = (%q,%v), want (%q,%v)", c.in, got, ok, c.want, c.wantOK)
		}
	}
}

func TestSplitNames(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"Dennis Vanderkerken, Dakota Krout", []string{"Dennis Vanderkerken", "Dakota Krout"}},
		{"  Solo Author  ", []string{"Solo Author"}},
		{"A,,B, ,C", []string{"A", "B", "C"}},
		{"", nil},
		{" , ", nil},
	}
	for _, c := range cases {
		got := SplitNames(c.in)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("SplitNames(%q) = %#v, want %#v", c.in, got, c.want)
		}
	}
}

func TestSlugify(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"Harry Potter and the Philosopher's Stone", "harry-potter-and-the-philosophers-stone"},
		{"Café Society", "cafe-society"},
		{"Motörhead: Overkill", "motorhead-overkill"},
		{"  Spaced   Out  ", "spaced-out"},
		{"J.R.R. Tolkien", "j-r-r-tolkien"},
		{"It's a Test — Really", "its-a-test-really"},
		{"ALL CAPS", "all-caps"},
		{"日本語", ""}, // non-Latin folds away; caller supplies a fallback
	}
	for _, c := range cases {
		if got := Slugify(c.in); got != c.want {
			t.Errorf("Slugify(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSlugifyMaxLen(t *testing.T) {
	long := ""
	for i := 0; i < 60; i++ {
		long += "ab "
	}
	got := Slugify(long)
	if len(got) > 100 {
		t.Errorf("Slugify long slug len = %d, want <= 100", len(got))
	}
	if got[len(got)-1] == '-' {
		t.Errorf("Slugify trimmed slug ends with a hyphen: %q", got)
	}
}

func TestYearOf(t *testing.T) {
	cases := map[string]string{
		"2025-11-20": "2025",
		"2001":       "2001",
		"":           "",
		"garbage":    "",
		"20-01-2020": "",
	}
	for in, want := range cases {
		if got := YearOf(in); got != want {
			t.Errorf("YearOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCoercion(t *testing.T) {
	book := rawBook{}
	if err := jsonInto(`{"a":"3","b":3,"c":3.9,"d":true,"e":null,"f":"x"}`, &book); err != nil {
		t.Fatal(err)
	}
	if got := book.str("a"); got != "3" {
		t.Errorf("str(a)=%q", got)
	}
	if got := book.str("b"); got != "3" {
		t.Errorf("str(number b)=%q", got)
	}
	if n, ok := book.intVal("b"); !ok || n != 3 {
		t.Errorf("intVal(b)=(%d,%v)", n, ok)
	}
	if n, ok := book.intVal("c"); !ok || n != 3 { // 3.9 truncates
		t.Errorf("intVal(c)=(%d,%v)", n, ok)
	}
	if n, ok := book.intVal("a"); !ok || n != 3 { // "3" as string
		t.Errorf("intVal(string a)=(%d,%v)", n, ok)
	}
	if _, ok := book.intVal("f"); ok {
		t.Errorf("intVal(non-numeric f) should fail")
	}
	if _, ok := book.intVal("missing"); ok {
		t.Errorf("intVal(missing) should fail")
	}
}

func TestBoolPtrTriState(t *testing.T) {
	book := rawBook{}
	if err := jsonInto(`{"t":true,"f":false,"n":null,"s":"true","x":"maybe"}`, &book); err != nil {
		t.Fatal(err)
	}
	if p := book.boolPtr("t"); p == nil || *p != true {
		t.Errorf("boolPtr(true) = %v", p)
	}
	if p := book.boolPtr("f"); p == nil || *p != false {
		t.Errorf("boolPtr(false) = %v", p)
	}
	if p := book.boolPtr("n"); p != nil {
		t.Errorf("boolPtr(null) = %v, want nil (unknown)", *p)
	}
	if p := book.boolPtr("missing"); p != nil {
		t.Errorf("boolPtr(absent) should be nil")
	}
	if p := book.boolPtr("s"); p == nil || *p != true {
		t.Errorf(`boolPtr("true" string) = %v`, p)
	}
	if p := book.boolPtr("x"); p != nil {
		t.Errorf("boolPtr(non-bool string) should be nil")
	}
}

func TestStripRoleQualifier(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		// Every known role strips, case-insensitively.
		{"J. Kharkova - translator", "J. Kharkova"},
		{"Valeria Kornosenko - introduction", "Valeria Kornosenko"},
		{"Graham Hancock - Introduction", "Graham Hancock"},
		{"Charles S. Terry - TRANSLATOR", "Charles S. Terry"},
		{"Someone - intro", "Someone"},
		{"Someone - foreword", "Someone"},
		{"Someone - afterword", "Someone"},
		{"Someone - preface", "Someone"},
		{"Someone - editor", "Someone"},
		{"Someone - illustrator", "Someone"},
		{"Someone - adaptation", "Someone"},
		{"Someone - contributor", "Someone"},
		{"Someone - narrator", "Someone"},
		{"Someone - ghostwriter", "Someone"},
		{"Someone - compilation", "Someone"},
		// Never strip an arbitrary " - X" suffix.
		{"The All - Stars", "The All - Stars"},
		{"Jean - Luc Picard", "Jean - Luc Picard"},
		{"Ampersand - Sound", "Ampersand - Sound"},
		// Empty-after-strip falls back to the unstripped name.
		{"- translator", "- translator"},
		// No qualifier at all.
		{"Plain Name", "Plain Name"},
	}
	for _, c := range cases {
		if got := StripRoleQualifier(c.in); got != c.want {
			t.Errorf("StripRoleQualifier(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// The " - translator" (leading-space) shape: idx 0, cleaned empty -> original.
	if got := StripRoleQualifier(" - translator"); got != " - translator" {
		t.Errorf("empty-after-strip fallback failed: %q", got)
	}
}

func TestSplitNamesStripsRoles(t *testing.T) {
	got := SplitNames("Kirill Klevanski, Valeria Kornosenko - introduction, J. Kharkova - Translator, The All - Stars")
	want := []string{"Kirill Klevanski", "Valeria Kornosenko", "J. Kharkova", "The All - Stars"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SplitNames role stripping = %#v, want %#v", got, want)
	}
}
