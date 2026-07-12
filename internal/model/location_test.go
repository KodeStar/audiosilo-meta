package model

import "testing"

func TestValidSlug(t *testing.T) {
	cases := map[string]bool{
		"harry-potter": true,
		"a":            true,
		"j-k-rowling":  true,
		"book1":        true,
		"9-lives":      true,
		"":             false,
		"Harry-Potter": false,
		"harry_potter": false,
		"-leading":     false,
		"trailing-":    false,
		"double--dash": false,
		"has space":    false,
		"café":         false,
	}
	for in, want := range cases {
		if got := ValidSlug(in); got != want {
			t.Errorf("ValidSlug(%q) = %v, want %v", in, got, want)
		}
	}
	long := make([]byte, MaxSlugLen+1)
	for i := range long {
		long[i] = 'a'
	}
	if ValidSlug(string(long)) {
		t.Errorf("slug over %d chars should be invalid", MaxSlugLen)
	}
}

func TestShard(t *testing.T) {
	cases := map[string]string{
		"harry-potter": "ha",
		"j-k-rowling":  "j-",
		"a":            "a",
		"ab":           "ab",
		"abc":          "ab",
	}
	for in, want := range cases {
		if got := Shard(in); got != want {
			t.Errorf("Shard(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseLocation(t *testing.T) {
	cases := []struct {
		rel      string
		ok       bool
		kind     Kind
		slug     string
		workSlug string
		shard    string
	}{
		{"works/ha/harry-potter/work.json", true, KindWork, "harry-potter", "", "ha"},
		{"works/ha/harry-potter/recordings/fry-2015.json", true, KindRecording, "fry-2015", "harry-potter", "ha"},
		{"works/ha/harry-potter/characters.json", true, KindCharacters, "harry-potter", "harry-potter", "ha"},
		{"works/ha/harry-potter/recaps.json", true, KindRecaps, "harry-potter", "harry-potter", "ha"},
		{"people/j-/j-k-rowling.json", true, KindPerson, "j-k-rowling", "", "j-"},
		{"series/ha/harry-potter.json", true, KindSeries, "harry-potter", "", "ha"},
		// Unrecognized locations.
		{"works/ha/harry-potter/notes.json", false, "", "", "", ""},
		{"works/ha/harry-potter.json", false, "", "", "", ""},
		{"people/j-/x/y.json", false, "", "", "", ""},
		{"random/thing.json", false, "", "", "", ""},
		{"works/ha/harry-potter/recordings/nested/x.json", false, "", "", "", ""},
		{"works/ha/harry-potter/recordings/.json", false, "", "", "", ""},
	}
	for _, c := range cases {
		loc, ok := ParseLocation(c.rel)
		if ok != c.ok {
			t.Errorf("ParseLocation(%q) ok = %v, want %v", c.rel, ok, c.ok)
			continue
		}
		if !ok {
			continue
		}
		if loc.Kind != c.kind || loc.Slug != c.slug || loc.WorkSlug != c.workSlug || loc.Shard != c.shard {
			t.Errorf("ParseLocation(%q) = %+v, want kind=%s slug=%s work=%s shard=%s",
				c.rel, loc, c.kind, c.slug, c.workSlug, c.shard)
		}
	}
}
