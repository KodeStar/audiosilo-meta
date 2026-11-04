package importer

import (
	"fmt"
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
