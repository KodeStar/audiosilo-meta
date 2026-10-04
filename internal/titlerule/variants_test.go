package titlerule

import (
	"slices"
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

func TestSpellingKeyIsCompareKeyWithoutATableWord(t *testing.T) {
	for _, title := range []string{"The Armor of Light", "Four Hours of Your Life", "A Café in Zürich", "", "1984"} {
		if got, want := spellingKey(title), CompareKey(title); got != want {
			t.Errorf("spellingKey(%q) = %q, want CompareKey's %q", title, got, want)
		}
	}
}

func TestSameSeriesSpelling(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"The Kingsbridge Novels", "Kingsbridge", true},
		{"Ranger's Apprentice", "Ranger's Apprentice (published order)", true},
		{"Mistborn", "Mistborn [Dramatized Adaptation]", true},
		{"Throne of Glass (French Edition)", "Throne of Glass [French Edition]", true},
		{"Pimsleur Chinese (Cantonese)", "Pimsleur Chinese (Mandarin)", false},
		{"Kingsbridge", "Kingsbridge (Abridged)", true},
		{"Los Juegos del Hambre", "Los Juegos del Hambre (Narración en Castellano)", true},
		{"Lock In", "Lock In (Narrated by Amber Benson)", true},
		// A ONE-SIDED decoration counts only from the closed vocabulary: a dialect or
		// variety is a different product, and a translated series title is no note.
		{"Pimsleur Spanish", "Pimsleur Spanish (Spain-Castilian)", false},
		{"Pimsleur Portuguese", "Pimsleur Portuguese (Brazilian)", false},
		{"Los Reyes Malditos", "Los Reyes Malditos [The Accursed Kings]", false},
		// A decoration DecorationKey cannot read whole agrees with nothing decorated.
		{"Night Watch (Книга 1)", "Night Watch (Том 1)", false},
		{"Alpha Cycle", "Beta Cycle", false},
	} {
		if got := SameSeriesSpelling(c.a, c.b); got != c.want {
			t.Errorf("SameSeriesSpelling(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestSpelledAsInWritesTheTitlesGlyph(t *testing.T) {
	text := "The Tournament at Gorlan: Ranger’s Apprentice - The Early Years"
	low := LowerFold(text)
	start := strings.Index(low, "ranger's")
	if got := SpelledAsIn("Ranger's Apprentice", text, start, start+len("ranger's apprentice")); got != "Ranger’s Apprentice" {
		t.Errorf("SpelledAsIn = %q, want the title's curly glyph", got)
	}
	if got := SpelledAsIn("Ranger’s Apprentice", "Ranger's Apprentice", 0, 19); got != "Ranger's Apprentice" {
		t.Errorf("SpelledAsIn = %q, want the title's straight glyph", got)
	}
}

// ONE-WAY in a name holding BOTH separators: its colon may be read as a dash, its dash
// is never read as a colon.
func TestSeparatorVariantsOfAMixedNameNeverColonizeADash(t *testing.T) {
	const name = "Star Wars: Episode I - The Phantom Menace"
	forms := withSeparatorVariants([]string{name})
	for _, f := range forms {
		if strings.Contains(f, "Episode I: ") {
			t.Errorf("variant %q reads the name's dash as a colon", f)
		}
	}
	if !slices.Contains(forms, "Star Wars - Episode I - The Phantom Menace") {
		t.Errorf("forms = %q, want the colon read as a dash", forms)
	}
	if _, ok := SeriesRefIn("phantom: star wars: episode i: the phantom menace", name); ok {
		t.Error("a title colon matched the name's dash")
	}
}

// The list a caller hands in is never written through its spare capacity.
func TestSeparatorVariantsDoNotWriteIntoTheCallersSlice(t *testing.T) {
	backing := make([]string, 1, 4)
	backing[0] = "Ranger's Apprentice: The Early Years"
	spare := backing[:2]
	spare[1] = "untouched"
	_ = withSeparatorVariants(backing)
	if spare[1] != "untouched" {
		t.Errorf("withSeparatorVariants wrote %q into the caller's spare capacity", spare[1])
	}
}

func TestCutsRange(t *testing.T) {
	for _, c := range []struct {
		orig, proposed string
		want           bool
	}{
		// A year range kept whole, dropped whole, and cut.
		{"The Great War, 1914-1918 (Unabridged)", "The Great War, 1914-1918", false},
		{"The Great War: 1914-1918", "The Great War", false},
		{"The Great War 1914-1918", "The Great War 1914", true},
		// A range at the very start of the title: no word in front of the low end, so only
		// the high end with the word after it can show a cut.
		{"1-3: The Omnibus", "3: The Omnibus", true},
		{"1-3: The Omnibus", "The Omnibus", false},
		// An en dash, at the end and mid-title.
		{"Russia's Last Gasp: The Eastern Front 1916–17", "Russia's Last Gasp", false},
		{"Russia's Last Gasp: The Eastern Front 1916–17", "The Eastern Front 1916", true},
		{"Star Wars Episode 1–8", "Star Wars –8", true},
		{"Books 1 – 3: Origins", "3: Origins", true},
		// A spaced range at the end, whose bare high end is also an unrelated number.
		{"Agent 6: The Agent Series, Books 4 - 6", "Agent 6", false},
	} {
		if got := cutsRange(c.orig, c.proposed); got != c.want {
			t.Errorf("cutsRange(%q, %q) = %v, want %v", c.orig, c.proposed, got, c.want)
		}
	}
	// The whole strip keeps a year range a title states.
	if got, _, ok := StripDecoration("The Great War, 1914-1918 (Unabridged)", ""); !ok || got != "The Great War, 1914-1918" {
		t.Errorf("StripDecoration = %q, %v", got, ok)
	}
}
