package importer

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// anotherBookRow is a libex row for the seeded recording's ASIN, stating title and author.
func anotherBookRow(title, author string) string {
	return anotherBookRowSub(title, "", author)
}

func anotherBookRowSub(title, subtitle, author string) string {
	return `[{"asin":"B0LIBEX001","title":"` + title + `","subtitle":"` + subtitle + `","region":"us","language":"english",` +
		`"lengthMinutes":600,"authors":[{"name":"` + author + `"}],"narrators":[{"name":"Bea Reader"}]}]`
}

// A row whose ASIN sits on a work with neither an author's surname nor a title word in
// common is about another book: nothing of it is applied, in either tier, and the
// worklist says so. Only a user's own library counts as a disagreement.
func TestRowNamingAnotherBookIsRefused(t *testing.T) {
	t.Run("libex enrichment", func(t *testing.T) {
		dataDir := seedTierTree(t, nil)
		before := snapshotTree(t, dataDir)
		sum, rows := runEnrichWithWorklist(t, dataDir, anotherBookRow("Reflections in a Golden Eye", "Carson McCullers"))
		if sum.Conflicts != 0 {
			t.Errorf("Conflicts = %d, want 0: a bulk mirror is not a person disagreeing", sum.Conflicts)
		}
		if !hasWarning(sum.Warnings, `names "Reflections in a Golden Eye" by Carson McCullers`) {
			t.Errorf("warnings = %v, want the other book named", sum.Warnings)
		}
		assertTreeUnchanged(t, dataDir, before)
		got := fmt.Sprint(rows)
		for _, want := range []string{`"field":"work"`, `"recorded":"The Lost Cartographer"`, `"stated":"Reflections in a Golden Eye"`} {
			if !strings.Contains(got, want) {
				t.Errorf("worklist %s lacks %s", got, want)
			}
		}
	})
	t.Run("a subtitle sharing a word does not make it the same book", func(t *testing.T) {
		dataDir := seedTierTree(t, nil)
		before := snapshotTree(t, dataDir)
		sum, _ := runEnrichWithWorklist(t, dataDir, anotherBookRowSub("Indy Cars", "The Lost Laps", "Sean McCollum"))
		if !hasWarning(sum.Warnings, `names "Indy Cars"`) {
			t.Errorf("warnings = %v, want the other book named", sum.Warnings)
		}
		assertTreeUnchanged(t, dataDir, before)
	})
	t.Run("a user library does not take the mirror seed over", func(t *testing.T) {
		dataDir := seedTierTree(t, nil)
		before := snapshotTree(t, dataDir)
		env := `{"format":"audiosilo-books","version":1,"books":[{"title":"GoldenEye","authors":["John Gardner"],` +
			`"narrators":["Bea Reader"],"language":"en","asin":"B0LIBEX001","runtime_min":600,"publisher":"Somebody Else"}]}`
		sum, err := RunAudiosiloBooks(writeBooks(t, env), Options{DataDir: dataDir, ImportDate: testImportDate})
		if err != nil {
			t.Fatal(err)
		}
		if sum.Conflicts != 1 || sum.AttestedRecordings != 0 {
			t.Errorf("Conflicts/AttestedRecordings = %d/%d, want 1/0", sum.Conflicts, sum.AttestedRecordings)
		}
		assertTreeUnchanged(t, dataDir, before)
	})
}

// Either half alone is ordinary, so neither refuses: a second spelling of an author or
// an editor credit shares the title, a series-titled retail listing shares the author.
func TestRowSharingATitleWordOrASurnameIsNotAnotherBook(t *testing.T) {
	for _, tc := range []struct{ name, title, author string }{
		{"the author's listing under a series title", "Mistborn", "Ada Mapmaker"},
		{"a co-author's or another initial's spelling", "Mistborn", "A. Mapmaker"},
		{"another credit on the same title", "The Lost Cartographer", "Martin Greenberg"},
		{"the same title word, decorated", "Cartographer: A Novel", "Martin Greenberg"},
		{"a title of noise words alone says nothing", "The Book", "Martin Greenberg"},
		{"a generational suffix is no surname", "Breakfast", "Ada Mapmaker Jr."},
		{"a transliterated surname is one edit apart", "Krieg", "Ada Mapmakr"},
		{"a family name written first", "Kaze no Uta", "Mapmaker Ada"},
		{"a collective credit names nobody", "Other Title", "Various"},
		{"an anonymous credit names nobody", "Other Title", "Anonymous"},
		{"a title and name in another script leave scraps", "ハリー・ポッターと賢者の石 1", "J.K.ローリング"},
		{"digits are all the slug keeps of a title", "Сборник стихов 001", "Martin Greenberg"},
		{"nothing of the name survives the slug", "Other Title", "村上春樹"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sum, err := RunLibex(writeBooks(t, anotherBookRow(tc.title, tc.author)),
				Options{DataDir: seedTierTree(t, nil), ImportDate: testImportDate, Mode: ModeEnrich})
			if err != nil {
				t.Fatal(err)
			}
			if hasWarning(sum.Warnings, "this row's ASIN names") {
				t.Errorf("warnings = %v, want the row used", sum.Warnings)
			}
		})
	}
}

// A translated title shares no word with its original, so a row in another language than
// the work says nothing about it, however its author is transliterated. Run through the
// same row in the work's language, which is refused, so the language is what stands it down.
func TestRowInAnotherLanguageIsNotAnotherBook(t *testing.T) {
	row := anotherBookRow("Krieg und Frieden", "Lew Tolstoi")
	for _, tc := range []struct {
		row     string
		refused bool
	}{
		{row, true},
		{strings.Replace(row, `"language":"english"`, `"language":"german"`, 1), false},
	} {
		sum, err := RunLibex(writeBooks(t, tc.row),
			Options{DataDir: seedTierTree(t, nil), ImportDate: testImportDate, Mode: ModeEnrich})
		if err != nil {
			t.Fatal(err)
		}
		if got := hasWarning(sum.Warnings, "this row's ASIN names"); got != tc.refused {
			t.Errorf("row %s: refused = %v, want %v (warnings %v)", tc.row, got, tc.refused, sum.Warnings)
		}
	}
}

// A suffix two people share is no surname in common, and a person's recorded name is
// what is read, so a slug that keeps only scraps of it says nothing.
func TestRowAuthorsApartReadsNames(t *testing.T) {
	p := &planner{people: map[string]string{"j-k": "J.K.ローリング"}}
	for _, tc := range []struct {
		author string
		work   string
		apart  bool
	}{
		{"Kurt Vonnegut Jr.", "robert-downey-jr", true},
		{"Kurt Vonnegut Jr.", "kurt-vonnegut", false},
		{"Carson McCullers", "john-gardner", true},
		{"Carson McCullers", "j-k", false},
		{"Carson McCullers", "various", false},
	} {
		if got := p.rowAuthorsApart(sourceBook{authors: []string{tc.author}}, []string{tc.work}); got != tc.apart {
			t.Errorf("%q against %q: apart = %v, want %v", tc.author, tc.work, got, tc.apart)
		}
	}
}

// The slug cap would cut the tail a row may share with a long title.
func TestTitleWordsReadTheWholeTitle(t *testing.T) {
	long := strings.Repeat("Winding Historical Account ", 5) + "Cartographer"
	if !titleWords(long)["cartographer"] {
		t.Errorf("titleWords(%q) lost the word past the slug cap", long)
	}
	if got := titleWords("Сборник стихов 001"); len(got) != 0 {
		t.Errorf("titleWords kept the scraps of another script: %v", got)
	}
}

// A row whose ASIN names another book casts no genre vote, and leaves its recording
// uncovered, so the work's set is not trimmed on the strength of the other rows.
func TestRegenerateGenresIgnoresARowNamingAnotherBook(t *testing.T) {
	dataDir := seedRegen(t, map[string]string{
		"works/st/stray/work.json":         regenWorkJSON("stray", []string{"mystery", "westerns"}, "libex-import"),
		"works/st/stray/recordings/a.json": regenRecJSON("stray", "a", "B0STRAY0A1"),
		"works/st/stray/recordings/b.json": regenRecJSON("stray", "b", "B0STRAY0B1"),
	})
	other := strings.Replace(regenRow("B0STRAY0B1", "Science Fiction"), "Ada Mapmaker", "Carson McCullers", 1)
	sum := runRegen(t, dataDir, regenRow("B0STRAY0A1", "Mystery"), other)
	if got, want := regenGenres(t, dataDir, "stray"), []string{"mystery", "westerns"}; !reflect.DeepEqual(got, want) {
		t.Errorf("stray genres = %v, want %v", got, want)
	}
	if sum.GenreRowsAnotherBook != 1 || sum.GenreRowsContradicted != 0 {
		t.Errorf("GenreRowsAnotherBook/GenreRowsContradicted = %d/%d, want 1/0", sum.GenreRowsAnotherBook, sum.GenreRowsContradicted)
	}
	if !hasNote(sum.Notes, "1 row named another book") {
		t.Errorf("notes = %v", sum.Notes)
	}
}
