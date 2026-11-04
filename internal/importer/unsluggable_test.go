package importer

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/check"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// librarySource is one user-library importer and its row spelling: authors and
// narrators as the source states them, title "A Cyrillic Book".
type librarySource struct {
	name string
	run  func(string, Options) (Summary, error)
	// export renders one book (asin) as the whole export file.
	export func(asin string, authors, narrators []string) string
}

// mustJSON is v as JSON.
func mustJSON(v any) string {
	out, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(out)
}

var librarySources = []librarySource{
	{"openaudible", Run, func(asin string, authors, narrators []string) string {
		return mustJSON([]map[string]any{{"asin": asin, "title_short": "A Cyrillic Book", "region": "US", "language": "english",
			"seconds": 18000, "author": strings.Join(authors, ", "), "narrated_by": strings.Join(narrators, ", ")}})
	}},
	{"libation", RunLibation, func(asin string, authors, narrators []string) string {
		return mustJSON([]map[string]any{{"AudibleProductId": asin, "Locale": "us", "Title": "A Cyrillic Book",
			"AuthorNames": strings.Join(authors, ", "), "NarratorNames": strings.Join(narrators, ", "),
			"LengthInMinutes": 300, "ContentType": "Product", "Language": "English"}})
	}},
	{"audiosilo-books", RunAudiosiloBooks, func(asin string, authors, narrators []string) string {
		return mustJSON(map[string]any{"format": "audiosilo-books", "version": 1, "books": []map[string]any{{
			"title": "A Cyrillic Book", "authors": authors, "narrators": narrators, "asin": asin, "language": "en"}}})
	}},
}

// requireNoCatchAll asserts the tree validates (checkUnslugFallback refuses the
// catch-all anywhere) and holds no catch-all person record.
func requireNoCatchAll(t *testing.T, dataDir string) {
	t.Helper()
	if entryExists(t, dataDir, personAddr(model.UnslugPersonID)) {
		t.Error("the catch-all person was created")
	}
	if res := check.Load(dataDir); !res.OK() {
		t.Errorf("tree failed validation: %v", res.Problems)
	}
}

// A user-library book crediting an author and a narrator whose names slug to
// nothing lands with those credits left off - never on the shared catch-all
// person - and each is reported as a credit not recorded, reason unsluggable
// (meta#2778).
func TestLibraryImportLeavesUnsluggableCreditsOff(t *testing.T) {
	for _, src := range librarySources {
		t.Run(src.name, func(t *testing.T) {
			dataDir := t.TempDir()
			export := src.export("B0UNSLUG01",
				[]string{"Ada Author", "Йордан Йовков", "Иван Петров - translator"},
				[]string{"Bea Reader", "김영하"})
			sum := runInto(t, src.run, dataDir, export)
			if sum.NewWorks != 1 || sum.NewRecordings != 1 {
				t.Fatalf("works/recordings = %d/%d, want 1/1\nwarnings: %v", sum.NewWorks, sum.NewRecordings, sum.Warnings)
			}
			var work struct {
				Authors []string       `json:"authors"`
				Credits []model.Credit `json:"credits"`
			}
			readEntity(t, dataDir, workAddr("a-cyrillic-book"), &work)
			if !reflect.DeepEqual(work.Authors, []string{"ada-author"}) || len(work.Credits) != 0 {
				t.Errorf("authors/credits = %v/%v, want [ada-author] and none", work.Authors, work.Credits)
			}
			if got := recSlugsOf(t, dataDir, "a-cyrillic-book"); !reflect.DeepEqual(got, []string{"bea-reader"}) {
				t.Errorf("recordings = %v, want [bea-reader]", got)
			}
			var rec struct {
				Narrators []string `json:"narrators"`
			}
			readEntity(t, dataDir, recAddr("a-cyrillic-book", "bea-reader"), &rec)
			if !reflect.DeepEqual(rec.Narrators, []string{"bea-reader"}) {
				t.Errorf("narrators = %v, want [bea-reader]", rec.Narrators)
			}
			want := []UnrecordedCredit{
				{ASIN: "B0UNSLUG01", Name: "Йордан Йовков", Work: "a-cyrillic-book", Reason: unsluggableReason},
				{ASIN: "B0UNSLUG01", Name: "Иван Петров", Roles: []string{model.RoleTranslator}, Work: "a-cyrillic-book", Reason: unsluggableReason},
				{ASIN: "B0UNSLUG01", Name: "김영하", Roles: []string{narratorRole}, Work: "a-cyrillic-book", Reason: unsluggableReason},
			}
			if !reflect.DeepEqual(sum.UnrecordedCredits, want) {
				t.Fatalf("unrecorded = %+v\nwant %+v", sum.UnrecordedCredits, want)
			}
			if got, want := sum.UnrecordedCredits[2].String(), `B0UNSLUG01 "김영하" (narrator) -> a-cyrillic-book, unsluggable`; got != want {
				t.Errorf("line = %q, want %q", got, want)
			}
			for _, w := range sum.Warnings {
				if strings.Contains(w, "empty slug") {
					t.Errorf("a credit reached the catch-all: %s", w)
				}
			}
			requireNoCatchAll(t, dataDir)
		})
	}
}

// A user-library book whose every author slugs to nothing has no identity left
// (a work needs an author, and one credited to nobody would merge two authors'
// same-titled books): it is skipped as missing-author, with a warning naming the
// names, and creates nothing. So is one left with only a translator.
func TestLibraryImportSkipsARowWithNoSluggableAuthor(t *testing.T) {
	for _, src := range librarySources {
		for _, authors := range [][]string{
			{"Йордан Йовков"},
			{"Йордан Йовков", "김영하"},
			{"Йордан Йовков", "Tina Translator - translator"},
		} {
			t.Run(src.name+"/"+strings.Join(authors, "+"), func(t *testing.T) {
				dataDir := t.TempDir()
				sum := runInto(t, src.run, dataDir, src.export("B0UNSLUG02", authors, []string{"Bea Reader"}))
				if sum.NewWorks != 0 || sum.NewRecordings != 0 || sum.NewPeople != 0 {
					t.Fatalf("works/recordings/people = %d/%d/%d, want nothing\nwarnings: %v",
						sum.NewWorks, sum.NewRecordings, sum.NewPeople, sum.Warnings)
				}
				if want := []RowSkip{{ASIN: "B0UNSLUG02", Reason: RefusalMissingAuthor}}; !reflect.DeepEqual(sum.Skips, want) {
					t.Errorf("skips = %+v, want %+v", sum.Skips, want)
				}
				if sum.SkippedUnsluggable != 1 {
					t.Errorf("SkippedUnsluggable = %d, want 1", sum.SkippedUnsluggable)
				}
				if !slices.ContainsFunc(sum.Warnings, func(w string) bool {
					return strings.Contains(w, "no individual author is left") && strings.Contains(w, `"Йордан Йовков"`)
				}) {
					t.Errorf("no warning names the unsluggable author: %v", sum.Warnings)
				}
				if len(sum.UnrecordedCredits) != 0 {
					t.Errorf("unrecorded = %+v, want none for a row that did not land", sum.UnrecordedCredits)
				}
				requireNoCatchAll(t, dataDir)
			})
		}
	}
}

// A user-library book whose every narrator slugs to nothing has no recording
// identity left (a recording needs a narrator, and is keyed by its first): it is
// skipped as missing-narrator, with a warning naming the names.
func TestLibraryImportSkipsARowWithNoSluggableNarrator(t *testing.T) {
	for _, src := range librarySources {
		t.Run(src.name, func(t *testing.T) {
			dataDir := t.TempDir()
			sum := runInto(t, src.run, dataDir, src.export("B0UNSLUG03", []string{"Ada Author"}, []string{"김영하"}))
			if sum.NewWorks != 0 || sum.NewRecordings != 0 || sum.NewPeople != 0 {
				t.Fatalf("works/recordings/people = %d/%d/%d, want nothing\nwarnings: %v",
					sum.NewWorks, sum.NewRecordings, sum.NewPeople, sum.Warnings)
			}
			if want := []RowSkip{{ASIN: "B0UNSLUG03", Reason: RefusalMissingNarrator}}; !reflect.DeepEqual(sum.Skips, want) {
				t.Errorf("skips = %+v, want %+v", sum.Skips, want)
			}
			if sum.SkippedUnsluggable != 1 {
				t.Errorf("SkippedUnsluggable = %d, want 1", sum.SkippedUnsluggable)
			}
			if !slices.ContainsFunc(sum.Warnings, func(w string) bool {
				return strings.Contains(w, "no narrator is left") && strings.Contains(w, `"김영하"`)
			}) {
				t.Errorf("no warning names the unsluggable narrator: %v", sum.Warnings)
			}
			requireNoCatchAll(t, dataDir)
		})
	}
}

// A book attaching to a catalogued work reports a narrator it leaves off as on a
// create: the recording lands under the narrator who resolves.
func TestLibraryAttachReportsAnUnsluggableNarrator(t *testing.T) {
	for _, src := range attachSources {
		if src.name == "libex" {
			continue // its parse refuses the row outright (credit-not-a-person)
		}
		t.Run(src.name, func(t *testing.T) {
			dataDir := t.TempDir()
			seedKafka(t, dataDir, "franz-kafka", "Franz Kafka")
			sum := runSourceInto(t, src, dataDir, strings.Replace(src.row("B0UNSLUG04"), "Bea Reader", "Bea Reader, 김영하", 1))
			if sum.NewWorks != 0 || sum.NewRecordings != 1 {
				t.Fatalf("works/recordings = %d/%d, want 0/1\nwarnings: %v", sum.NewWorks, sum.NewRecordings, sum.Warnings)
			}
			var lines []string
			for _, u := range sum.UnrecordedCredits {
				lines = append(lines, u.String())
			}
			if !slices.Contains(lines, `B0UNSLUG04 "김영하" (narrator) -> metamorphosis, unsluggable`) {
				t.Errorf("unrecorded = %v, want the narrator reported", lines)
			}
			requireNoCatchAll(t, dataDir)
		})
	}
}

// A co-author whose name slugs to nothing is left off before ANY reader of the
// row's credits sees them (rowAuthorCreditSplitIn), so the row is keyed and
// matched exactly as the clean row it lands as: the same-title serial pre-pass
// groups it with its sibling volume (serialPositionSuffixes), giving the two
// volumes the works the clean pair gets.
func TestUnsluggableCoAuthorKeysLikeTheCleanRow(t *testing.T) {
	serial := func(asin, pos, author string) map[string]any {
		return map[string]any{"asin": asin, "title_short": "Serial Book", "region": "US", "language": "english",
			"seconds": 18000, "author": author, "narrated_by": "Bea Reader", "series_name": "Serial Saga", "series_sequence": pos}
	}
	works := func(author2 string) []string {
		sum, dataDir := runImport(t, mustJSON([]map[string]any{
			serial("B0SERIAL01", "1", "Ada Author"), serial("B0SERIAL02", "2", author2),
		}), false)
		if sum.NewWorks != 2 {
			t.Fatalf("author %q: NewWorks = %d, want 2\nwarnings: %v", author2, sum.NewWorks, sum.Warnings)
		}
		requireNoCatchAll(t, dataDir)
		return listWorks(t, dataDir)
	}
	clean, coAuthor := works("Ada Author"), works("Ada Author, Йордан Йовков")
	if !reflect.DeepEqual(clean, coAuthor) {
		t.Errorf("works with an unsluggable co-author = %v, want the clean pair's %v", coAuthor, clean)
	}
}

// The recordings-only work match reads the same split: a row crediting the
// catalogued author and an unsluggable co-author is an alternate narration of
// that author's work, not a book the catalogue lacks.
func TestRecordingsOnlyMatchesPastAnUnsluggableCoAuthor(t *testing.T) {
	dataDir := t.TempDir()
	seedKafka(t, dataDir, "franz-kafka", "Franz Kafka")
	row := mustJSON([]map[string]any{{"asin": "B0UNSLUG05", "title_short": "Metamorphosis", "author": "Franz Kafka, Йордан Йовков",
		"narrated_by": "Bea Reader, 김영하", "language": "english", "region": "US", "seconds": 6000}})
	sum, err := Run(writeBooks(t, row), Options{DataDir: dataDir, ImportDate: testImportDate, Mode: ModeRecordingsOnly})
	if err != nil {
		t.Fatal(err)
	}
	if sum.SkippedNoWork != 0 || sum.NewRecordings != 1 {
		t.Fatalf("no-work/new recordings = %d/%d, want 0/1\nwarnings: %v", sum.SkippedNoWork, sum.NewRecordings, sum.Warnings)
	}
	if got := recSlugsOf(t, dataDir, "metamorphosis"); !reflect.DeepEqual(got, []string{"bea-reader", "se"}) {
		t.Errorf("recordings = %v, want bea-reader beside se", got)
	}
	want := []UnrecordedCredit{{ASIN: "B0UNSLUG05", Name: "김영하", Roles: []string{narratorRole}, Work: "metamorphosis", Reason: unsluggableReason}}
	if !reflect.DeepEqual(sum.UnrecordedCredits, want) {
		t.Errorf("unrecorded = %+v, want %+v", sum.UnrecordedCredits, want)
	}
	requireNoCatchAll(t, dataDir)
}

// The recordings-only match asks the create path's no-individual-author rule
// too: a row whose credits leave only "Anonymous" once the names that slug to
// nothing are left off identifies no work, so it does not attach a recording to
// a catalogued "Anonymous" book that merely shares its title.
func TestRecordingsOnlyRefusesARowLeftWithNoIndividualAuthor(t *testing.T) {
	dataDir := t.TempDir()
	seedKafka(t, dataDir, "anonymous", "Anonymous")
	row := mustJSON([]map[string]any{{"asin": "B0UNSLUG06", "title_short": "Metamorphosis", "author": "Anonymous, Йордан Йовков",
		"narrated_by": "Bea Reader", "language": "english", "region": "US", "seconds": 6000}})
	sum, err := Run(writeBooks(t, row), Options{DataDir: dataDir, ImportDate: testImportDate, Mode: ModeRecordingsOnly})
	if err != nil {
		t.Fatal(err)
	}
	if sum.SkippedNoWork != 1 || sum.NewRecordings != 0 {
		t.Fatalf("no-work/new recordings = %d/%d, want 1/0\nwarnings: %v", sum.SkippedNoWork, sum.NewRecordings, sum.Warnings)
	}
	if got := recSlugsOf(t, dataDir, "metamorphosis"); !reflect.DeepEqual(got, []string{"se"}) {
		t.Errorf("recordings = %v, want the seeded one alone", got)
	}
}

// An enrichment row lands nothing, so a role credit whose name slugs to nothing
// cannot be a credit not recorded: it is counted into the run's aggregate
// warning instead of being dropped silently.
func TestEnrichReportsAnUnsluggableRoleCredit(t *testing.T) {
	dataDir := seedEnrichTree(t, nil)
	books := `[{"asin":"B0LIBEX001","title_short":"The Lost Cartographer","author":"Ada Mapmaker, Иван Петров - translator",` +
		`"narrated_by":"Bea Reader","language":"english","region":"uk","publisher":"Lost Press"}]`
	sum, err := Run(writeBooks(t, books), Options{DataDir: dataDir, ImportDate: testImportDate, Mode: ModeEnrich})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(sum.Warnings, func(w string) bool {
		return strings.Contains(w, "1 role-qualified credits dropped") && strings.Contains(w, "Иван Петров")
	}) {
		t.Errorf("no aggregate warning names the dropped translator: %v", sum.Warnings)
	}
	requireNoCatchAll(t, dataDir)
}
