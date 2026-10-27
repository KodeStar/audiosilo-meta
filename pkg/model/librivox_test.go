package model

import "testing"

// TestLibrivoxLanguageNamesAreOneToOne pins what LibrivoxLanguageName rests on:
// the two tables a LibriVox name resolves through are disjoint and, together,
// map no two names onto one tag - so the reverse map has exactly one name per tag.
func TestLibrivoxLanguageNamesAreOneToOne(t *testing.T) {
	byTag := map[string]string{}
	for _, m := range []map[string]string{languageWords, librivoxLanguages} {
		for name, tag := range m {
			if prev, ok := byTag[tag]; ok {
				t.Errorf("tag %q has two LibriVox names, %q and %q", tag, prev, name)
			}
			byTag[tag] = name
		}
	}
	for name := range librivoxLanguages {
		if _, ok := languageWords[name]; ok {
			t.Errorf("%q is in both language tables", name)
		}
	}
}

func TestLibrivoxLanguageName(t *testing.T) {
	for tag, want := range map[string]string{
		"en":    "English",
		"fr":    "French",
		"grc":   "Ancient Greek",
		"cu":    "Church Slavonic",
		"de-at": "German", // the primary subtag
		"xx":    "",
		"":      "",
	} {
		if got := LibrivoxLanguageName(tag); got != want {
			t.Errorf("LibrivoxLanguageName(%q) = %q, want %q", tag, got, want)
		}
	}
}
