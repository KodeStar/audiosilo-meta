package importer

import (
	"reflect"
	"slices"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/testpack"
	"github.com/kodestar/audiosilo-meta/pkg/check"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// orphanPeople is every person the tree's orphan-person advisory names.
func orphanPeople(t *testing.T, dataDir string) []string {
	t.Helper()
	res := check.Load(dataDir)
	if !res.OK() {
		t.Fatalf("tree failed validation:\n%v", res.Problems)
	}
	var out []string
	for _, w := range res.Warnings {
		if check.AdvisoryClass(w) == check.AdvisoryOrphanPerson {
			out = append(out, w.String())
		}
	}
	return out
}

// ianJohnston is fixture 527's translator credit as the run reports it when the
// project attaches to a catalogued Metamorphosis that does not name him.
func ianJohnston(work string, created bool) UnrecordedCredit {
	return UnrecordedCredit{Project: 527, AuthorID: 1019, Name: "Ian Johnston",
		Roles: []string{model.RoleTranslator}, Work: work, PersonExists: created}
}

func TestLibrivoxAttachCreditsItsTranslatorOnTheRecording(t *testing.T) {
	// The catalogue holds Metamorphosis by Kafka with no translator: the project
	// attaches (rule 1) and records no WORK credit (reported as not recorded),
	// but the translation is the project's own fact, so the recording credits
	// its translator - created for it, carrying his LibriVox id and years - and
	// he is no orphan.
	dataDir := t.TempDir()
	seedKafka(t, dataDir, "franz-kafka", "Franz Kafka")
	sum := runLibrivox(t, librivoxFixture, dataDir, mustLibrivoxIDs("527"))
	if sum.NewWorks != 0 || sum.NewRecordings != 1 || sum.NewPeople != 2 || sum.LibrivoxRestated.TranslatorsCreated != 1 {
		t.Fatalf("works/recordings/people/translators = %d/%d/%d/%d, want 0/1/2/1 (the reader and the translator)\nwarnings: %v",
			sum.NewWorks, sum.NewRecordings, sum.NewPeople, sum.LibrivoxRestated.TranslatorsCreated, sum.Warnings)
	}
	if p := personOf(t, dataDir, "ian-johnston"); p.AuthorID != 1019 || p.BirthYear != 1938 {
		t.Errorf("ian-johnston = %+v, want author id 1019 and born 1938", p)
	}
	if _, _, f := lvRecordingOf(t, dataDir, 527); !reflect.DeepEqual(f.Credits, []model.Credit{{Person: "ian-johnston", Role: model.RoleTranslator}}) {
		t.Errorf("recording credits = %v", f.Credits)
	}
	if w := workCreditsOf(t, dataDir, "metamorphosis"); len(w) != 0 {
		t.Errorf("work credits = %v, want none (an attach records no work credit)", w)
	}
	if got := orphanPeople(t, dataDir); len(got) != 0 {
		t.Errorf("orphans: %v", got)
	}
	if want := []UnrecordedCredit{ianJohnston("metamorphosis", true)}; !reflect.DeepEqual(sum.UnrecordedCredits, want) {
		t.Errorf("unrecorded = %+v, want %+v", sum.UnrecordedCredits, want)
	}
}

// workCreditsOf reads one work's credits.
func workCreditsOf(t *testing.T, dataDir, slug string) []model.Credit {
	t.Helper()
	var w lvWork
	readEntity(t, dataDir, workAddr(slug), &w)
	return w.Credits
}

func TestLibrivoxSpellingVariantAttachCreditsItsTranslatorOnTheRecording(t *testing.T) {
	// The same through rule 2 (a spelling variant), and the reported line.
	dataDir := t.TempDir()
	seedWorks(t, dataDir, map[string][]string{"kafkas-book": {"The Metamorphosis", "franz-kafka"}})
	sum := runLibrivox(t, writeSnapshot(t, fixtureProject(t, 527)), dataDir, allLibrivox)
	if sum.LibrivoxVariantsAttached != 1 || !entryExists(t, dataDir, personAddr("ian-johnston")) {
		t.Fatalf("attached = %d, ian-johnston created = %v", sum.LibrivoxVariantsAttached,
			entryExists(t, dataDir, personAddr("ian-johnston")))
	}
	if len(sum.UnrecordedCredits) != 1 {
		t.Fatalf("unrecorded = %+v", sum.UnrecordedCredits)
	}
	if got, want := sum.UnrecordedCredits[0].String(),
		`527 "Ian Johnston" (translator, librivox_author_id 1019) -> kafkas-book`; got != want {
		t.Errorf("line = %q, want %q", got, want)
	}
	if got := orphanPeople(t, dataDir); len(got) != 0 {
		t.Errorf("orphans: %v", got)
	}
}

func TestLibrivoxAttachKeepsACataloguedTranslatorUnstamped(t *testing.T) {
	// The translator is already a person here (credited on another work): the
	// record stays, the credit is still not recorded on this work and is
	// reported as such, and his LibriVox id is not stamped from this project.
	dataDir := t.TempDir()
	seedKafka(t, dataDir, "franz-kafka", "Franz Kafka")
	seedWorks(t, dataDir, map[string][]string{"faust": {"Faust", "goethe", "ian-johnston"}})
	sum := runLibrivox(t, librivoxFixture, dataDir, mustLibrivoxIDs("527"))
	if sum.NewPeople != 1 {
		t.Errorf("NewPeople = %d, want the reader only", sum.NewPeople)
	}
	if want := []UnrecordedCredit{ianJohnston("metamorphosis", true)}; !reflect.DeepEqual(sum.UnrecordedCredits, want) {
		t.Errorf("unrecorded = %+v, want %+v", sum.UnrecordedCredits, want)
	}
	var p lvPerson
	readEntity(t, dataDir, personAddr("ian-johnston"), &p)
	if p.AuthorID != 0 {
		t.Errorf("ian-johnston stamped %d from a project that does not credit him", p.AuthorID)
	}
}

func TestLibrivoxAttachToAWorkNamingTheTranslator(t *testing.T) {
	// A catalogued work that already names the translator: nothing is
	// unrecorded, and the existing record is keyed by its LibriVox id.
	dataDir := t.TempDir()
	seedWorks(t, dataDir, map[string][]string{"metamorphosis": {"Metamorphosis", "franz-kafka", "ian-johnston"}})
	sum := runLibrivox(t, writeSnapshot(t, fixtureProject(t, 527)), dataDir, allLibrivox)
	if sum.NewWorks != 0 || sum.NewRecordings != 1 || len(sum.UnrecordedCredits) != 0 {
		t.Fatalf("works/recordings/unrecorded = %d/%d/%+v\nwarnings: %v",
			sum.NewWorks, sum.NewRecordings, sum.UnrecordedCredits, sum.Warnings)
	}
	var p lvPerson
	readEntity(t, dataDir, personAddr("ian-johnston"), &p)
	if p.AuthorID != 1019 {
		t.Errorf("ian-johnston = %+v; want librivox_author_id 1019", p)
	}
}

func TestLibrivoxNewWorkStillCreatesItsTranslator(t *testing.T) {
	// A project that creates its work keeps every credit: author and translator
	// are created, credited and keyed, and nothing is reported unrecorded.
	dataDir := t.TempDir()
	sum := runLibrivox(t, librivoxFixture, dataDir, mustLibrivoxIDs("527"))
	if sum.NewWorks != 1 || sum.NewPeople != 3 || len(sum.UnrecordedCredits) != 0 {
		t.Fatalf("works/people/unrecorded = %d/%d/%+v", sum.NewWorks, sum.NewPeople, sum.UnrecordedCredits)
	}
	var w lvWork
	readEntity(t, dataDir, workAddr("metamorphosis"), &w)
	if !slices.Contains(w.Credits, model.Credit{Person: "ian-johnston", Role: model.RoleTranslator}) {
		t.Errorf("credits = %v; want the translator credited", w.Credits)
	}
	if got := orphanPeople(t, dataDir); len(got) != 0 {
		t.Errorf("orphans: %v", got)
	}
}

func TestLibrivoxSecondTranslationInOneRunIsNotUnrecorded(t *testing.T) {
	// The run-created work takes a later row's translator as a role credit
	// (TestLibrivoxStampsASecondTranslationsTranslator), so it is not reported.
	first := fixtureProject(t, 527)
	second := fixtureProject(t, 527)
	second.ID, second.Title = 99200, "Metamorphosis (version 2)"
	second.Translators = []LibrivoxPerson{{ID: 2000, FirstName: "Willa", LastName: "Muir"}}
	dataDir := t.TempDir()
	sum := runLibrivox(t, writeSnapshot(t, first, second), dataDir, allLibrivox)
	if len(sum.UnrecordedCredits) != 0 || !entryExists(t, dataDir, personAddr("willa-muir")) {
		t.Fatalf("unrecorded = %+v, willa-muir created = %v", sum.UnrecordedCredits,
			entryExists(t, dataDir, personAddr("willa-muir")))
	}
	if got := orphanPeople(t, dataDir); len(got) != 0 {
		t.Errorf("orphans: %v", got)
	}
}

func TestLibrivoxRerunAfterAnAttachIsANoOp(t *testing.T) {
	// The withheld translator is not created by a rerun either.
	dataDir := t.TempDir()
	seedKafka(t, dataDir, "franz-kafka", "Franz Kafka")
	runLibrivox(t, librivoxFixture, dataDir, mustLibrivoxIDs("527"))
	sum := runLibrivox(t, librivoxFixture, dataDir, mustLibrivoxIDs("527"))
	if sum.NewPeople != 0 || sum.NewRecordings != 0 || len(sum.UnrecordedCredits) != 0 || len(sum.Files) != 0 {
		t.Errorf("rerun people/recordings/unrecorded/files = %d/%d/%+v/%d",
			sum.NewPeople, sum.NewRecordings, sum.UnrecordedCredits, len(sum.Files))
	}
}

func TestLibrivoxTrailingQualifierGoesToTheWorklist(t *testing.T) {
	// A title that is a catalogued work's (same author) once a trailing
	// parenthetical or bracketed qualifier is removed is listed
	// differently-spelled-title and creates nothing.
	for name, title := range map[string]string{
		"edition year":         "Metamorphosis (1915)",
		"subtitle":             "Metamorphosis (The Story of Gregor Samsa)",
		"series note":          "Metamorphosis (Kafka Stories Vol. 1)",
		"article and brackets": "The Metamorphosis [Johnston Translation]",
		"two qualifiers":       "Metamorphosis (Dramatic Reading) (1915)",
	} {
		t.Run(name, func(t *testing.T) {
			dataDir := t.TempDir()
			seedWorks(t, dataDir, map[string][]string{"metamorphosis": {"Metamorphosis", "franz-kafka"}})
			sum := runLibrivox(t, writeSnapshot(t, retitled(t, 99600, title)), dataDir, allLibrivox)
			want := []LibrivoxWorklistEntry{{
				Project: 99600, Title: title, Authors: []string{"Franz Kafka", "Ian Johnston (Translator)"},
				Reason: LibrivoxDifferentlySpelled, Candidates: []LibrivoxCandidate{{"metamorphosis", "Metamorphosis"}},
			}}
			if sum.NewWorks != 0 || sum.NewRecordings != 0 || sum.NewPeople != 0 || !reflect.DeepEqual(sum.LibrivoxWorklist, want) {
				t.Fatalf("works/recordings/people = %d/%d/%d, worklist = %+v", sum.NewWorks, sum.NewRecordings, sum.NewPeople, sum.LibrivoxWorklist)
			}
			if sum.SkippedDuplicateIdentity != 1 {
				t.Errorf("SkippedDuplicateIdentity = %d, want the run's warning to count it", sum.SkippedDuplicateIdentity)
			}
			if want := []RowSkip{{LibrivoxID: 99600, Reason: RefusalIdentityDuplicate}}; !reflect.DeepEqual(sum.Skips, want) {
				t.Errorf("Skips = %+v, want the refusal keyed by project id", sum.Skips)
			}
		})
	}
}

func TestLibrivoxTrailingQualifierWithSeveralWorks(t *testing.T) {
	dataDir := t.TempDir()
	seedWorks(t, dataDir, map[string][]string{
		"metamorphosis":   {"Metamorphosis", "franz-kafka"},
		"metamorphosis-2": {"The Metamorphosis", "franz-kafka"},
	})
	sum := runLibrivox(t, writeSnapshot(t, retitled(t, 99601, "Metamorphosis (1915)")), dataDir, allLibrivox)
	if sum.NewWorks != 0 || len(sum.LibrivoxWorklist) != 1 || sum.LibrivoxWorklist[0].Reason != LibrivoxSeveralWorks {
		t.Fatalf("works = %d, worklist = %+v", sum.NewWorks, sum.LibrivoxWorklist)
	}
}

func TestLibrivoxTrailingQualifierLeavesOtherTitlesAlone(t *testing.T) {
	// A new work, no worklist: the stripped title is another author's, the
	// stripped title is no catalogued work, the qualifier is not trailing, or
	// the volume is stated outside brackets. A title the ordinary walk finds as
	// is still attaches (rule 1 first). Kafka is dated, so the person rules
	// read the credit as the catalogued person whatever the titles.
	cases := map[string]struct {
		works    map[string][]string
		title    string
		newWorks int
	}{
		"another author's title":  {map[string][]string{"metamorphosis": {"Metamorphosis", "ovid"}}, "Metamorphosis (1915)", 1},
		"no such work":            {map[string][]string{"the-trial": {"The Trial", "franz-kafka"}}, "Metamorphosis (1915)", 1},
		"qualifier not trailing":  {map[string][]string{"metamorphosis": {"Metamorphosis", "franz-kafka"}}, "Metamorphosis (1915) and Other Stories", 1},
		"volume outside brackets": {map[string][]string{"metamorphosis": {"Metamorphosis", "franz-kafka"}}, "Metamorphosis, Part 1", 1},
		"catalogued as titled":    {map[string][]string{"metamorphosis-1915": {"Metamorphosis (1915)", "franz-kafka"}, "metamorphosis": {"Metamorphosis", "franz-kafka"}}, "Metamorphosis (1915)", 0},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dataDir := t.TempDir()
			seedDatedWorks(t, dataDir, tc.works, map[string][2]int{"franz-kafka": {1883, 1924}})
			sum := runLibrivox(t, writeSnapshot(t, retitled(t, 99602, tc.title)), dataDir, allLibrivox)
			if sum.NewWorks != tc.newWorks || sum.NewRecordings != 1 || len(sum.LibrivoxWorklist) != 0 {
				t.Fatalf("works/recordings = %d/%d (want %d/1), worklist = %+v\nwarnings: %v",
					sum.NewWorks, sum.NewRecordings, tc.newWorks, sum.LibrivoxWorklist, sum.Warnings)
			}
			mustValidate(t, dataDir)
		})
	}
}

func TestLibrivoxQualifierStrip(t *testing.T) {
	for in, want := range map[string]string{
		"Frankenstein; or The Modern Prometheus (1818)": "Frankenstein; or The Modern Prometheus",
		"Man of Property (Forsyte Saga Vol. 1)":         "Man of Property",
		"Black Beauty [The Autobiography of a Horse]":   "Black Beauty",
		"King in Yellow (part 1) ":                      "King in Yellow",
		"Anna Karenina, Book 1":                         "Anna Karenina, Book 1",
		"(1818)":                                        "",
	} {
		if got := stripTrailingQualifier(in); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
}

func TestLibrivoxAttachToAWorkCreditingTheTranslator(t *testing.T) {
	// A catalogued work that names the translator in its stored credits only
	// (not its authors): the credit is the work's already, so nothing is
	// reported, and the person is keyed by the project's LibriVox id.
	dataDir := t.TempDir()
	seedKafka(t, dataDir, "franz-kafka", "Franz Kafka")
	seedTree(t, dataDir, map[string]string{
		personAddr("ian-johnston"): testpack.PersonJSON(t, "ian-johnston", "Ian Johnston"),
		workAddr("metamorphosis"): testpack.WithField(t,
			testpack.WorkJSON(t, "metamorphosis", "Metamorphosis", testpack.WithAuthors("franz-kafka")),
			"credits", []model.Credit{{Person: "ian-johnston", Role: model.RoleTranslator}}),
	})
	sum := runLibrivox(t, librivoxFixture, dataDir, mustLibrivoxIDs("527"))
	if sum.NewWorks != 0 || sum.NewRecordings != 1 || len(sum.UnrecordedCredits) != 0 {
		t.Fatalf("works/recordings/unrecorded = %d/%d/%+v\nwarnings: %v",
			sum.NewWorks, sum.NewRecordings, sum.UnrecordedCredits, sum.Warnings)
	}
	var p lvPerson
	readEntity(t, dataDir, personAddr("ian-johnston"), &p)
	if p.AuthorID != 1019 {
		t.Errorf("ian-johnston = %+v; want librivox_author_id 1019", p)
	}
}

func TestLibrivoxUnrecordedUnsluggableTranslatorsKeepTheirOwnIDs(t *testing.T) {
	// Two translators whose names slug away to nothing: each is reported
	// unsluggable with its own LibriVox id, matched by name, and neither is said
	// to exist (the catch-all record is never theirs).
	dataDir := t.TempDir()
	seedKafka(t, dataDir, "franz-kafka", "Franz Kafka")
	pr := fixtureProject(t, 527)
	pr.Translators = []LibrivoxPerson{{ID: 5001, FirstName: "Иван", LastName: "Петров"}, {ID: 5002, FirstName: "Пётр", LastName: "Иванов"}}
	sum := runLibrivox(t, writeSnapshot(t, pr), dataDir, allLibrivox)
	want := []UnrecordedCredit{
		{Project: 527, AuthorID: 5001, Name: "Иван Петров", Roles: []string{model.RoleTranslator}, Work: "metamorphosis", Reason: librivoxUnsluggable},
		{Project: 527, AuthorID: 5002, Name: "Пётр Иванов", Roles: []string{model.RoleTranslator}, Work: "metamorphosis", Reason: librivoxUnsluggable},
	}
	if !reflect.DeepEqual(sum.UnrecordedCredits, want) {
		t.Errorf("unrecorded = %+v, want %+v", sum.UnrecordedCredits, want)
	}
}

func TestLibrivoxUnrecordedTranslatorWhoReadsExists(t *testing.T) {
	// The translator also reads the project: the reader credit creates the
	// record, so the withheld translator credit names a person that exists.
	dataDir := t.TempDir()
	seedKafka(t, dataDir, "franz-kafka", "Franz Kafka")
	pr := fixtureProject(t, 527)
	for i := range pr.Sections {
		pr.Sections[i].Readers = []LibrivoxReader{{ReaderID: 77, DisplayName: "Ian Johnston"}}
	}
	sum := runLibrivox(t, writeSnapshot(t, pr), dataDir, allLibrivox)
	if !entryExists(t, dataDir, personAddr("ian-johnston")) {
		t.Fatal("the reader was not created")
	}
	if want := []UnrecordedCredit{ianJohnston("metamorphosis", true)}; !reflect.DeepEqual(sum.UnrecordedCredits, want) {
		t.Errorf("unrecorded = %+v, want %+v", sum.UnrecordedCredits, want)
	}
}
