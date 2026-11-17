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
		{"a strict plurality need not be over half", []string{"en-1", "en-2", "de-1", "fr-1"}, "en"},
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

// TestReadLanguage: a language as a file's tag spells it, read as the
// catalogue's tag - a BCP-47 tag's primary subtag, a 639-2 code, an English, a
// LibriVox or an own-language name - and "" for anything that names no single
// language this table reads.
func TestReadLanguage(t *testing.T) {
	for in, want := range map[string]string{
		"en": "en", "EN": "en", "en-US": "en", "en_gb": "en", "eng": "en", "English": "en",
		"de": "de", "ger": "de", "deu": "de", "German": "de", "Deutsch": "de",
		"fr": "fr", "fra": "fr", "Français": "fr", "es-419": "es", "Español": "es",
		"ja": "ja", "jpn": "ja", "nb": "no", "nn": "no", "no": "no", "Norwegian": "no",
		"pt-br": "pt", "sv": "sv", " Português ": "pt",
		// LibriVox's name table, and its 639-2-only tags kept as stored.
		"Catalan": "ca", "Ancient Greek": "grc", "grc": "grc", "ang": "ang", "Multilingual": "",
		// A tag no table maps onto is unknown, never a language of its own
		// ("kor" is not judged against "ko", nor "xx" against anything).
		"kor": "", "ko": "ko", "xx": "", "NA": "", "zz-ZZ": "",
		"": "", "und": "", "mul": "", "zxx": "", "mis": "", "Klingon": "", "en,de": "", "12": "", "English; German": "",
	} {
		if got := ReadLanguage(in); got != want {
			t.Errorf("ReadLanguage(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestLanguageSpellingsIsACopy: the exported table is the reader's, and a
// caller changing its copy changes nothing ReadLanguage reads.
func TestLanguageSpellingsIsACopy(t *testing.T) {
	m := LanguageSpellings()
	if m["deutsch"] != "de" || m["eng"] != "en" || m["english"] != "en" || m["catalan"] != "ca" {
		t.Fatalf("LanguageSpellings misses the names, codes or LibriVox names: %d entries", len(m))
	}
	if v, ok := m["und"]; !ok || v != "" {
		t.Errorf(`LanguageSpellings["und"] = %q, %v; want "" (no single language)`, v, ok)
	}
	m["deutsch"] = "xx"
	if ReadLanguage("Deutsch") != "de" {
		t.Error("changing the copy changed ReadLanguage")
	}
}
