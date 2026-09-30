package titlerule

import "testing"

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
		{"Hurricane Wars (Japanese Edition)", "ja"},
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

func TestSplitEditionName(t *testing.T) {
	for _, tc := range []struct {
		name, base, lang string
	}{
		{"Throne of Glass[French Edition]", "Throne of Glass", "fr"},
		{"NOMADS Legacy (German Edition)", "NOMADS Legacy", "de"},
		{"Harry Hole - [Spanish Edition]", "Harry Hole", "es"},
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
