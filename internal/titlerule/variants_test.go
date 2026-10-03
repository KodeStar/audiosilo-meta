package titlerule

import (
	"strings"
	"testing"
)

func TestSeriesRefInFoldsApostrophesAndSeparators(t *testing.T) {
	for _, c := range []struct {
		title, series string
		want          bool
	}{
		// The motivating title: a curly apostrophe and a spaced dash where the series
		// record has a straight apostrophe and a colon.
		{"The Tournament at Gorlan: Ranger’s Apprentice - The Early Years, Book 1", "Ranger's Apprentice: The Early Years", true},
		{"Emergence: Jake’s Dragon, Book 5", "Jake's Dragon", true},
		{"Oberon's Meaty Mysteries: The Squirrel on the Train", "Oberon’s Meaty Mysteries", true},
		{"Avatar – Der Herr der Elemente: Das Vermächtnis von Yangchen", "Avatar: Der Herr der Elemente", true},
		{"Kingsbridge – WDR Hörspiel: Der Abend", "Kingsbridge - WDR Hörspiel", true},
		// ONE-WAY: a dash in the NAME is not read as a title's colon, which is where the
		// book's own title usually ends.
		{"Quicksilver: Saga Alquimia & Fae, Vol. 1", "Quicksilver - Saga Alquimia", false},
		// An unspaced hyphen belongs to a word, not to the separator class.
		{"Avatar-Der Herr der Elemente", "Avatar: Der Herr der Elemente", false},
	} {
		_, got := SeriesRefIn(strings.ToLower(c.title), c.series)
		if got != c.want {
			t.Errorf("SeriesRefIn(%q, %q) = %v, want %v", c.title, c.series, got, c.want)
		}
	}
}

// The fold reaches every reader of the forms, so the clean and the retitle proposal
// treat the typographic spelling exactly as its straight twin.
func TestCleanStripsATypographicSeriesSpelling(t *testing.T) {
	const series = "Ranger's Apprentice: The Early Years"
	if got := Clean("The Tournament at Gorlan: Ranger’s Apprentice - The Early Years, Book 1", series); got != "The Tournament at Gorlan" {
		t.Errorf("Clean = %q, want %q", got, "The Tournament at Gorlan")
	}
	if got, ok := ProposeTitle("The Tournament at Gorlan: Ranger’s Apprentice - The Early Years, Book 1", series); !ok || got != "The Tournament at Gorlan" {
		t.Errorf("ProposeTitle = %q, %v; want %q", got, ok, "The Tournament at Gorlan")
	}
}

func TestPunctuationVariantsLeaveAPlainNameAlone(t *testing.T) {
	if got := SeriesForms("Jack Reacher"); len(got) != 1 || got[0] != "Jack Reacher" {
		t.Errorf("SeriesForms(%q) = %q, want the name alone", "Jack Reacher", got)
	}
}

func TestSpellingVariantKey(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"The Armour of Light", "armoroflight"},
		{"The Armor of Light", ""}, // already the American spelling: nothing changes
		{"The Colours of All the Cattle", "colorsofallthecattle"},
		{"Agnes Grey", "agnesgray"},
		{"Death of a Travelling Man", "deathofatravelingman"},
		// The table is CLOSED and whole-word: no suffix rule reaches these.
		{"Four Hours of Your Life", ""},
		{"The Promise of Rising Wisdom", ""},
		{"Armourer", ""},
	} {
		if got := SpellingVariantKey(c.in); got != c.want {
			t.Errorf("SpellingVariantKey(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// A key the table changes meets the American title's plain comparison key.
	if a, b := SpellingVariantKey("The Armour of Light"), CompareKey("The Armor of Light"); a != b {
		t.Errorf("variant key %q does not meet the US comparison key %q", a, b)
	}
}

func TestSameVariantTitleUnderCommonSeries(t *testing.T) {
	if !SameVariantTitleUnderCommonSeries("The Armour of Light", "Kingsbridge", "The Armor of Light", "The Kingsbridge Novels") {
		t.Error("a UK and a US title of one book do not agree under the variant key")
	}
	if SameVariantTitleUnderCommonSeries("The Armour of Light", "Kingsbridge", "The Armor of Darkness", "") {
		t.Error("two different titles agreed under the variant key")
	}
}
