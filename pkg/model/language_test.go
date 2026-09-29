package model

import "testing"

func TestPrimarySubtag(t *testing.T) {
	for tag, want := range map[string]string{
		"en":         "en",
		"en-GB":      "en",
		"EN-gb":      "en",
		"zh-Hant-TW": "zh",
		"fil":        "fil",
		"":           "",
	} {
		if got := PrimarySubtag(tag); got != want {
			t.Errorf("PrimarySubtag(%q) = %q, want %q", tag, got, want)
		}
	}
}

func TestSeriesLanguage(t *testing.T) {
	langs := map[string]string{
		"en-1": "en", "en-2": "en", "en-gb": "en-GB", "de-1": "de", "de-2": "de-AT",
		"fr-1": "fr", "none": "",
	}
	langOf := func(id string) string { return langs[id] }
	series := func(ids ...string) []SeriesWork {
		var works []SeriesWork
		for _, id := range ids {
			works = append(works, SeriesWork{Work: id})
		}
		return works
	}
	cases := []struct {
		name  string
		works []string
		want  string
	}{
		{"one member", []string{"en-1"}, "en"},
		{"unanimous", []string{"en-1", "en-2"}, "en"},
		{"regions fold onto the primary subtag", []string{"en-gb", "de-1", "en-1"}, "en"},
		{"a region on a minority member still counts for its language", []string{"de-1", "de-2", "en-1"}, "de"},
		{"strict majority", []string{"en-1", "en-2", "de-1"}, "en"},
		{"a 1-1 tie has no majority", []string{"en-1", "de-1"}, ""},
		{"a tie at the top ignores a smaller third", []string{"en-1", "en-2", "de-1", "de-2", "fr-1"}, ""},
		{"a later, higher count clears a lower tie", []string{"de-1", "fr-1", "en-1", "en-2", "en-gb"}, "en"},
		{"unknown languages are no vote", []string{"none", "unknown-work", "de-1"}, "de"},
		{"nothing known", []string{"none", "unknown-work"}, ""},
		{"no members", nil, ""},
		{"a work listed twice counts twice", []string{"en-1", "en-1", "de-1"}, "en"},
	}
	for _, c := range cases {
		if got := SeriesLanguage(series(c.works...), langOf); got != c.want {
			t.Errorf("%s: SeriesLanguage(%v) = %q, want %q", c.name, c.works, got, c.want)
		}
	}
	byID := map[string]*Work{"en-1": {Language: "en"}, "de-1": {Language: "de-AT"}, "en-2": {Language: "en-GB"}}
	if got := SeriesLanguageOf(series("en-1", "de-1", "en-2", "unknown-work"), byID); got != "en" {
		t.Errorf("SeriesLanguageOf = %q, want \"en\"", got)
	}
}

func TestSameLanguage(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"en", "en", true},
		{"en-GB", "EN-us", true},
		{"en", "de", false},
		{"", "", false},
		{"en", "", false},
		{"", "en", false},
	} {
		if got := SameLanguage(c.a, c.b); got != c.want {
			t.Errorf("SameLanguage(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
