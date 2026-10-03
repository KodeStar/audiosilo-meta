package titlerule

import (
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

func TestEditionLanguage(t *testing.T) {
	for _, tc := range []struct {
		text, want string
	}{
		{"Families First (German Edition)", "de"},
		{"Throne of Glass[French Edition]", "fr"},
		{"Throne of Glass [ French Edition ]", "fr"},
		{"Kill Joy (German edition)", "de"},
		{"Evil Eye (Castilian Spanish Edition)", "es"},
		{"Skyshade (edición en español) (Lightlark 3)", "es"},
		{"Il nome (edizione italiana)", "it"},
		{"Die Saga (Deutsche Ausgabe)", "de"},
		{"The Saga [Fench Edition]", "fr"},
		{"The Gambler [Persian Edition]", "fa"},
		{"White Nights (Persian Edition)", "fa"},
		{"Hurricane Wars (Japanese Edition)", "ja"},
		// A language the importer maps is a language whose edition is read.
		{"Kuolema (Finnish Edition)", "fi"},
		// Not a language edition: nothing is stated.
		{"Fifteen Dogs (Tenth Anniversary Edition)", ""},
		{"Pride and Prejudice (AmazonClassics Edition)", ""},
		{"Diccionario (English and Spanish Edition)", ""},
		{"German Edition", ""},
		{"Plain Title", ""},
		// Two languages state nothing.
		{"X (German Edition) [French Edition]", ""},
		// The same language twice is still that language.
		{"X (German Edition) [German Edition]", "de"},
	} {
		got, ok := EditionLanguage(tc.text)
		if got != tc.want || ok != (tc.want != "") {
			t.Errorf("EditionLanguage(%q) = %q, %v; want %q", tc.text, got, ok, tc.want)
		}
	}
}

// Every language the project's word-to-code table maps is readable as an edition
// language, so the importer's table and the edition reader cannot drift apart.
func TestEveryMappedLanguageReadsAsAnEdition(t *testing.T) {
	for word, code := range model.LanguageWords() {
		if got, ok := EditionLanguage("X (" + word + " Edition)"); !ok || got != code {
			t.Errorf("EditionLanguage(%q Edition) = %q, %v; want %q", word, got, ok, code)
		}
	}
}

// A title and a subtitle are read as one record: two languages across them state
// nothing, one language in either states it.
func TestEditionLanguageReadsTitleAndSubtitleTogether(t *testing.T) {
	if got, ok := EditionLanguage("A Game of Fate", "(French Edition)"); !ok || got != "fr" {
		t.Errorf("subtitle decoration = %q, %v", got, ok)
	}
	if _, ok := EditionLanguage("X (German Edition)", "[French Edition]"); ok {
		t.Error("two languages across title and subtitle stated one")
	}
}

// A text NAMES a language when a language word stands in it outside its own edition
// decoration: the language-course shape, whose decoration names the language taught.
func TestNamesALanguage(t *testing.T) {
	for _, tc := range []struct {
		text string
		want bool
	}{
		{"Learn German: By Reading Fantasy (German Edition)", true},
		{"101 Conversations in Simple Spanish (Spanish Edition)", true},
		{"Persian Grammar [Persian Edition]", true},
		{"A Castilian Spanish Primer", true},
		// The decoration alone names nothing: it is what the reader strips.
		{"Steelheart [German Edition]", false},
		{"The Gambler [Persian Edition]", false},
		// A language word inside another word is not one.
		{"Germany 1945", false},
		{"Englishman's Holiday", false},
		{"", false},
	} {
		if got := NamesALanguage(tc.text); got != tc.want {
			t.Errorf("NamesALanguage(%q) = %v, want %v", tc.text, got, tc.want)
		}
	}
}

func TestEditionLanguageOfDecoration(t *testing.T) {
	for decor, want := range map[string]string{
		DecorationKey("Throne of Glass [French Edition]"):         "fr",
		DecorationKey("Foo (Book Club) [German Edition]"):         "de",
		DecorationKey("Foo [German Edition] (Publication Order)"): "",
		"": "",
	} {
		if got := EditionLanguageOfDecoration(decor); got != want {
			t.Errorf("EditionLanguageOfDecoration(%q) = %q, want %q", decor, got, want)
		}
	}
}

func TestSplitEditionName(t *testing.T) {
	for _, tc := range []struct {
		name, base, lang string
	}{
		{"Throne of Glass[French Edition]", "Throne of Glass", "fr"},
		{"NOMADS Legacy (German Edition)", "NOMADS Legacy", "de"},
		{"Harry Hole - [Spanish Edition]", "Harry Hole", "es"},
		{"Harry Hole\t- [Spanish Edition]", "Harry Hole", "es"},
		{"Vernon Subutex [English Edition]", "Vernon Subutex", "en"},
		// The decoration must END the name.
		{"The Saga [German Edition] Book One", "", ""},
		// A name that is nothing but its decoration names no series.
		{"[German Edition]", "", ""},
		// Not a language: an ordering is SER-PAREN's business.
		{"Vorkosigan Saga (Publication Order)", "", ""},
		// A broken nested bracket reads as nothing.
		{"[My Father's Dragon [German Edition]", "", ""},
	} {
		base, lang, ok := SplitEditionName(tc.name)
		if base != tc.base || lang != tc.lang || ok != (tc.lang != "") {
			t.Errorf("SplitEditionName(%q) = %q, %q, %v; want %q, %q", tc.name, base, lang, ok, tc.base, tc.lang)
		}
	}
}

func TestStripEditionLanguage(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"Families First, Volume 2 (German Edition)", "Families First, Volume 2"},
		{"Carry On (Simon Snow 1) (Spanish Edition)", "Carry On (Simon Snow 1)"},
		{"Plain", "Plain"},
	} {
		if got := StripEditionLanguage(tc.in); got != tc.want {
			t.Errorf("StripEditionLanguage(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSameUntranslatedTitle(t *testing.T) {
	for _, tc := range []struct {
		translation, original string
		want                  bool
	}{
		{"A Game of Fate (French Edition)", "A Game of Fate", true},
		{"Agency for Scandal (German Edition)", "The Agency for Scandal", true},
		{"Dear Love I Hate You (German Edition)", "Dear Love, I Hate You", true},
		{"Omertà (Italian Edition)", "Omerta", true},
		{"Mageling (German Edition)", "Mageling (Unabridged)", true},
		{"Families First, Volume 2 (German Edition)", "Families First", false},
		{"Mia & Korum (Die komplette Krinar Chroniken Trilogie) [German Edition]", "Mia & Korum", false},
		{"City of Thorns (German edition)", "City of Thorns (Dramatized Adaptation)", false},
		{"Insatiable (French Edition)", "Insatiable: Cloverleigh Farms Series", false},
		// A script CompareKey folds away compares whole.
		{"Метро (Russian Edition)", "Метро", true},
		{"Метро (Russian Edition)", "Пикник", false},
	} {
		if got := SameUntranslatedTitle(tc.translation, tc.original); got != tc.want {
			t.Errorf("SameUntranslatedTitle(%q, %q) = %v, want %v", tc.translation, tc.original, got, tc.want)
		}
	}
}

func TestSameUntranslatedSubtitle(t *testing.T) {
	for _, tc := range []struct {
		translation, original string
		want                  bool
	}{
		{"", "", true},
		{"(French Edition)", "", true},
		{"(French Edition)", "(Unabridged)", true},
		{"A Novel (French Edition)", "A Novel", true},
		{"Volume 2 (French Edition)", "", false},
		{"(French Edition)", "Book 2", false},
		{"Volume 2", "Volume 3", false},
	} {
		if got := SameUntranslatedSubtitle(tc.translation, tc.original); got != tc.want {
			t.Errorf("SameUntranslatedSubtitle(%q, %q) = %v, want %v", tc.translation, tc.original, got, tc.want)
		}
	}
}

func TestIsDramatization(t *testing.T) {
	for _, tc := range []struct {
		title string
		want  bool
	}{
		{"City of Thorns (Dramatized Adaptation)", true},
		{"The Spy Who Came in from the Cold (Dramatised)", true},
		{"A Full-Cast Dramatization", true},
		{"Drama Queen", false},
		{"Dramatic Irony", false},
	} {
		if got := IsDramatization(tc.title); got != tc.want {
			t.Errorf("IsDramatization(%q) = %v, want %v", tc.title, got, tc.want)
		}
	}
}
