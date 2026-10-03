package titlerule

import (
	"slices"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

func TestGlossOf(t *testing.T) {
	publishers := []string{"Fixture Audio"}
	series := []string{"Reihe"}
	for _, tc := range []struct {
		title, head, gloss string
	}{
		{"La Odisea [The Odyssey]", "La Odisea", "The Odyssey"},
		{"Anjo Negro Alado [Winged Black Angel]", "Anjo Negro Alado", "Winged Black Angel"},
		{"Desespero [Despair]", "Desespero", "Despair"},
		// Not a gloss: the reason is the case's own title.
		{"Faust [Fixture Audio]", "", ""},      // a recording's publisher
		{"Faust [Reihe]", "", ""},              // the record's own series
		{"Adrift [Movie Tie-in]", "", ""},      // a tie-in
		{"Faust [The Dragon Box Set]", "", ""}, // a collection
		{"Small Talk [5-in-1]", "", ""},        // a bundle
		{"Gravity [1980033501]", "", ""},       // a number
		{"Hamlet [Dramatized Adaptation]", "", ""},
		{"Steelheart [German Edition]", "", ""},    // a language edition
		{"Mageling [Unabridged]", "", ""},          // an edition marker
		{"A Christmas Carol [Una Novela]", "", ""}, // an English head
		{"Faust [Faust]", "", ""},                  // the head itself
		{"La Odisea (The Odyssey)", "", ""},        // a parenthetical is not the shape
		{"La Odisea", "", ""},
	} {
		head, gloss, ok := GlossOf(tc.title, publishers, series)
		if head != tc.head || gloss != tc.gloss || ok != (tc.gloss != "") {
			t.Errorf("GlossOf(%q) = %q, %q, %v; want %q, %q", tc.title, head, gloss, ok, tc.head, tc.gloss)
		}
	}
}

func TestTitleFunctionWords(t *testing.T) {
	for _, tc := range []struct {
		lang  string
		texts []string
		want  string
	}{
		{"it", []string{"Il Cuore Spezzato Di Arelium"}, "di,il"},
		{"es", []string{"La Expedición de Sabina para Deterner el Apocalipsis [Sabina's Expedition]"}, "de,el,la,para"},
		{"fr", []string{"Une page", "d'amour et de mort"}, "de,et,une"}, // the subtitle is read too
		{"it", []string{"Alex Cross Must Die"}, ""},                     // no Italian word at all
		{"it", []string{"La La Land"}, ""},                              // one distinct word is not two
		{"it", []string{"The Cuore of Il Spezzato Di Arelium"}, ""},     // English beside the Italian
		{"de", []string{"Die Den"}, ""},                                 // English words are no evidence
		{"pt", []string{"O Brother A Song"}, ""},                        // one-letter words are left out
		{"it", []string{"Faust [Il Cuore Di Roma]"}, ""},                // bracketed groups are not read
		{"en", []string{"The Lord of the Rings"}, ""},                   // English has no list
		{"ru", []string{"Il Cuore Di Roma"}, ""},                        // nor does an excluded language
	} {
		got := strings.Join(TitleFunctionWords(tc.lang, tc.texts...), ",")
		if got != tc.want {
			t.Errorf("TitleFunctionWords(%s, %q) = %q, want %q", tc.lang, tc.texts, got, tc.want)
		}
	}
}

// Every language the edition vocabulary can state either has a function-word list or is
// excluded on purpose, so adding a language forces a decision - and a list is never
// shadowed by an exclusion.
func TestEveryEditionLanguageHasAFunctionWordDecision(t *testing.T) {
	codes := map[string]bool{}
	for _, words := range []map[string]string{model.LanguageWords(), editionOnlyWords, editionLanguagePhrases} {
		for _, code := range words {
			codes[code] = true
		}
	}
	for code := range codes {
		_, listed := functionWords[code]
		_, excluded := noFunctionWords[code]
		switch {
		case code == "en":
			if listed || excluded {
				t.Errorf("en is englishFunctionWords, not a list or an exclusion")
			}
		case listed == excluded:
			t.Errorf("language %q: listed %v, excluded %v - it needs exactly one", code, listed, excluded)
		}
	}
	for code := range noFunctionWords {
		if !codes[code] {
			t.Errorf("exclusion %q is no language the edition vocabulary states", code)
		}
	}
}

// The English set is titleStopwords and more: the copied matcher's stopwords are never
// read as another language's words.
func TestEnglishFunctionWordsHoldTheTitleStopwords(t *testing.T) {
	for w := range titleStopwords {
		if !englishFunctionWords[w] {
			t.Errorf("%q is a title stopword but no English function word", w)
		}
	}
	for lang, set := range functionWordSets {
		for w := range set {
			if englishFunctionWords[w] || len(w) == 1 {
				t.Errorf("%s set holds %q", lang, w)
			}
		}
	}
	if !slices.Contains(functionWords["it"], "è") || !functionWordSets["it"]["è"] {
		t.Error("an accented one-letter word is a word, not an initial")
	}
}
