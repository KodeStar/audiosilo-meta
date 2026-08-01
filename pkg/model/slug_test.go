package model

import "testing"

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
	if len(got) > MaxSlugLen {
		t.Errorf("Slugify long slug len = %d, want <= %d", len(got), MaxSlugLen)
	}
	if got[len(got)-1] == '-' {
		t.Errorf("Slugify trimmed slug ends with a hyphen: %q", got)
	}
}

// TestSlugifyCollapsesSeparators pins the property the single-pass builder
// replaced a collapse-and-trim regexp with: a run of separators is ONE hyphen,
// and a slug never starts or ends on one however the input is padded.
func TestSlugifyCollapsesSeparators(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"---", ""},
		{"  --  a  --  b  --  ", "a-b"},
		{"a...b", "a-b"},
		{"(Bracketed) [Title]", "bracketed-title"},
		{"a/b|c", "a-b-c"},
	}
	for _, c := range cases {
		if got := Slugify(c.in); got != c.want {
			t.Errorf("Slugify(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestPersonSlug pins the one identity the catalogue's person ids are derived
// through, including the shared catch-all a fully-folding name falls back to -
// the single record whose id is legitimately not its name's slug.
func TestPersonSlug(t *testing.T) {
	cases := []struct {
		name         string
		wantSlug     string
		wantFellBack bool
	}{
		{"Madeleine L'Engle", "madeleine-lengle", false},
		{"Ramón De Ocampo", "ramon-de-ocampo", false},
		{"김영하", UnslugPersonID, true},
		{"", UnslugPersonID, true},
	}
	for _, c := range cases {
		slug, fellBack := PersonSlug(c.name)
		if slug != c.wantSlug || fellBack != c.wantFellBack {
			t.Errorf("PersonSlug(%q) = (%q, %v), want (%q, %v)", c.name, slug, fellBack, c.wantSlug, c.wantFellBack)
		}
	}
}
