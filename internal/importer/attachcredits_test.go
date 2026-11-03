package importer

import (
	"reflect"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/testpack"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// The orphan-translator regression (meta#2760), for every source family that
// reaches addBook's ordinary walk: a row that attaches to a catalogued work
// records only its recording, so a translator that work does not name must not
// be minted as a person credited nowhere. The LibriVox twin is
// librivox_credits_test.go; these share its orphanPeople helper.

// attachSource is one source family's import entrypoint and its spelling of a
// row "Metamorphosis" by Franz Kafka, translated by Ian Johnston, read by Bea
// Reader, at the given ASIN.
type attachSource struct {
	name string
	run  func(string, Options) (Summary, error)
	row  func(asin string) string
}

var attachSources = []attachSource{
	{"openaudible", Run, func(asin string) string {
		return `{"asin":"` + asin + `","title_short":"Metamorphosis","author":"Franz Kafka, Ian Johnston - translator",` +
			`"narrated_by":"Bea Reader","language":"english","region":"US","seconds":6000}`
	}},
	{"libation", RunLibation, func(asin string) string {
		return `{"AudibleProductId":"` + asin + `","Locale":"us","Title":"Metamorphosis",` +
			`"AuthorNames":"Franz Kafka, Ian Johnston - translator","NarratorNames":"Bea Reader",` +
			`"LengthInMinutes":100,"ContentType":"Product","Language":"English"}`
	}},
	{"libex", RunLibex, func(asin string) string {
		return libexRow{asin: asin, title: "Metamorphosis", minutes: 100,
			authors:   `{"name":"Franz Kafka"},{"name":"Ian Johnston - Translator"}`,
			narrators: `{"name":"Bea Reader"}`}.render()
	}},
}

// runInto runs an import entrypoint (Run, RunLibation, RunLibex) in create
// mode against an existing, possibly seeded, data dir - runWith always starts
// from an empty tree.
func runInto(t *testing.T, run func(string, Options) (Summary, error), dataDir, export string) Summary {
	t.Helper()
	sum, err := run(writeBooks(t, export), Options{DataDir: dataDir, ImportDate: testImportDate})
	if err != nil {
		t.Fatalf("import run: %v", err)
	}
	return sum
}

// runSourceInto imports rows (src's spelling) into dataDir.
func runSourceInto(t *testing.T, src attachSource, dataDir string, rows ...string) Summary {
	t.Helper()
	return runInto(t, src.run, dataDir, "["+strings.Join(rows, ",")+"]")
}

// requireTranslatorWithheld asserts the attach made no record for the
// uncredited translator ian-johnston and left the tree with no orphan person.
func requireTranslatorWithheld(t *testing.T, dataDir string) {
	t.Helper()
	if entryExists(t, dataDir, personAddr("ian-johnston")) {
		t.Error("the uncredited translator was created")
	}
	if got := orphanPeople(t, dataDir); len(got) != 0 {
		t.Errorf("orphans: %v", got)
	}
}

func TestAttachCreatesNoUncreditedTranslator(t *testing.T) {
	for _, src := range attachSources {
		t.Run(src.name, func(t *testing.T) {
			dataDir := t.TempDir()
			seedKafka(t, dataDir, "franz-kafka", "Franz Kafka") // naming no translator
			sum := runSourceInto(t, src, dataDir, src.row("B0ORPHAN01"))
			if sum.NewWorks != 0 || sum.NewRecordings != 1 || sum.NewPeople != 1 {
				t.Fatalf("works/recordings/people = %d/%d/%d, want 0/1/1 (the narrator only)\nwarnings: %v",
					sum.NewWorks, sum.NewRecordings, sum.NewPeople, sum.Warnings)
			}
			requireTranslatorWithheld(t, dataDir)
			want := []UnrecordedCredit{{ASIN: "B0ORPHAN01", Name: "Ian Johnston",
				Roles: []string{model.RoleTranslator}, Work: "metamorphosis"}}
			if !reflect.DeepEqual(sum.UnrecordedCredits, want) {
				t.Errorf("unrecorded = %+v, want %+v", sum.UnrecordedCredits, want)
			}
			if got, want := sum.UnrecordedCredits[0].String(),
				`B0ORPHAN01 "Ian Johnston" (translator) -> metamorphosis, person not created`; got != want {
				t.Errorf("line = %q, want %q", got, want)
			}
		})
	}
}

func TestAttachKeepsACreditedTranslator(t *testing.T) {
	// The catalogued work already names the translator (in credits[], not
	// authors[]): the attach credits him there, so nothing is reported and no
	// second record is made.
	for _, src := range attachSources {
		t.Run(src.name, func(t *testing.T) {
			dataDir := t.TempDir()
			seedKafka(t, dataDir, "franz-kafka", "Franz Kafka")
			seedTree(t, dataDir, map[string]string{
				personAddr("ian-johnston"): testpack.PersonJSON(t, "ian-johnston", "Ian Johnston"),
				workAddr("metamorphosis"): testpack.WithField(t,
					testpack.WorkJSON(t, "metamorphosis", "Metamorphosis", testpack.WithAuthors("franz-kafka")),
					"credits", []model.Credit{{Person: "ian-johnston", Role: model.RoleTranslator}}),
				// The override replaces the whole work entry, so its recording is
				// seeded again (an orphan narrator otherwise).
				recAddr("metamorphosis", "se"): testpack.RecJSON(t, "se", "metamorphosis", testpack.WithNarrators("someone-else")),
			})
			sum := runSourceInto(t, src, dataDir, src.row("B0ORPHAN02"))
			if sum.NewWorks != 0 || sum.NewRecordings != 1 || sum.NewPeople != 1 || len(sum.UnrecordedCredits) != 0 {
				t.Fatalf("works/recordings/people = %d/%d/%d, unrecorded %+v; want 0/1/1 and none\nwarnings: %v",
					sum.NewWorks, sum.NewRecordings, sum.NewPeople, sum.UnrecordedCredits, sum.Warnings)
			}
			if got := orphanPeople(t, dataDir); len(got) != 0 {
				t.Errorf("orphans: %v", got)
			}
		})
	}
}

func TestCreateStillMintsTheTranslator(t *testing.T) {
	// The other side: a row that CREATES its work mints the translator and
	// credits him on it, and a second row of the run attaching to that work
	// (another narrator) reports nothing - its translator is the work's.
	for _, src := range attachSources {
		t.Run(src.name, func(t *testing.T) {
			dataDir := t.TempDir()
			second := strings.Replace(src.row("B0ORPHAN04"), "Bea Reader", "Cal Voice", 1)
			sum := runSourceInto(t, src, dataDir, src.row("B0ORPHAN03"), second)
			if sum.NewWorks != 1 || sum.NewRecordings != 2 || len(sum.UnrecordedCredits) != 0 {
				t.Fatalf("works/recordings = %d/%d, unrecorded %+v; want 1/2 and none\nwarnings: %v",
					sum.NewWorks, sum.NewRecordings, sum.UnrecordedCredits, sum.Warnings)
			}
			var work struct {
				Authors []string       `json:"authors"`
				Credits []model.Credit `json:"credits"`
			}
			readEntity(t, dataDir, workAddr("metamorphosis"), &work)
			if want := []model.Credit{{Person: "ian-johnston", Role: model.RoleTranslator}}; !reflect.DeepEqual(work.Credits, want) {
				t.Errorf("credits = %+v, want %+v", work.Credits, want)
			}
			if want := []string{"franz-kafka", "ian-johnston"}; !reflect.DeepEqual(work.Authors, want) {
				t.Errorf("authors = %v, want %v", work.Authors, want)
			}
			if !entryExists(t, dataDir, personAddr("ian-johnston")) {
				t.Error("the translator of a created work was not created")
			}
			if got := orphanPeople(t, dataDir); len(got) != 0 {
				t.Errorf("orphans: %v", got)
			}
		})
	}
}

func TestAttachReportsAnUnsluggableTranslator(t *testing.T) {
	// A translator whose name slugs to nothing could only land on the shared
	// catch-all, which is not them: the attach reports the credit as
	// unsluggable (not as a withheld person) and makes no record. libex is not
	// asked: its parse refuses such a row outright (credit-not-a-person).
	for _, src := range attachSources {
		if src.name == "libex" {
			continue
		}
		t.Run(src.name, func(t *testing.T) {
			dataDir := t.TempDir()
			seedKafka(t, dataDir, "franz-kafka", "Franz Kafka")
			sum := runSourceInto(t, src, dataDir, strings.Replace(src.row("B0ORPHAN05"), "Ian Johnston", "Иван Петров", 1))
			want := []UnrecordedCredit{{ASIN: "B0ORPHAN05", Name: "Иван Петров",
				Roles: []string{model.RoleTranslator}, Work: "metamorphosis", Reason: librivoxUnsluggable}}
			if !reflect.DeepEqual(sum.UnrecordedCredits, want) {
				t.Fatalf("unrecorded = %+v, want %+v\nwarnings: %v", sum.UnrecordedCredits, want, sum.Warnings)
			}
			if got, want := sum.UnrecordedCredits[0].String(),
				`B0ORPHAN05 "Иван Петров" (translator) -> metamorphosis, unsluggable`; got != want {
				t.Errorf("line = %q, want %q", got, want)
			}
			if entryExists(t, dataDir, personAddr(model.UnslugPersonID)) {
				t.Error("the catch-all person was created")
			}
			if got := orphanPeople(t, dataDir); len(got) != 0 {
				t.Errorf("orphans: %v", got)
			}
		})
	}
}

func TestAttachEditionsReportsAnUncreditedTranslator(t *testing.T) {
	// The --attach-editions rule (attach.go) attaches a row to the catalogued
	// occupant of its series position: it is an attach like any other, so a
	// translator the occupant does not name is reported and gets no record.
	row := `{"asin":"B0ATTACH09","title":"The Lost Coast","region":"us","language":"english",` +
		`"authors":[{"name":"Ada Mapmaker"},{"name":"Tina Translator - Translator"}],"narrators":[{"name":"Cal Voice"}],` +
		`"series":[{"name":"` + seriesName + `","position":"1"}]}`
	_, sum, dataDir := attachImport(t, nil, row)
	if sum.Attached != 1 || sum.NewRecordings != 1 {
		t.Fatalf("Attached/NewRecordings = %d/%d, want 1/1\nwarnings: %v", sum.Attached, sum.NewRecordings, sum.Warnings)
	}
	if entryExists(t, dataDir, personAddr("tina-translator")) {
		t.Error("the uncredited translator was created")
	}
	want := []UnrecordedCredit{{ASIN: "B0ATTACH09", Name: "Tina Translator",
		Roles: []string{model.RoleTranslator}, Work: "the-lost-coast"}}
	if !reflect.DeepEqual(sum.UnrecordedCredits, want) {
		t.Errorf("unrecorded = %+v, want %+v", sum.UnrecordedCredits, want)
	}
	if got := orphanPeople(t, dataDir); len(got) != 0 {
		t.Errorf("orphans: %v", got)
	}
}
