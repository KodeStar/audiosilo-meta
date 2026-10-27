package importer

import (
	"reflect"
	"slices"
	"strings"
	"testing"

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
func ianJohnston(work string, created bool) LibrivoxUnrecordedCredit {
	return LibrivoxUnrecordedCredit{Project: 527, AuthorID: 1019, Name: "Ian Johnston",
		Roles: []string{model.RoleTranslator}, Work: work, Created: created}
}

func TestLibrivoxAttachCreatesNoUncreditedTranslator(t *testing.T) {
	// The catalogue holds Metamorphosis by Kafka with no translator: the project
	// attaches (rule 1), its translator is credited nowhere, so no record is made
	// for him and the run reports the credit instead.
	dataDir := t.TempDir()
	seedKafka(t, dataDir, "franz-kafka", "Franz Kafka")
	sum := runLibrivox(t, librivoxFixture, dataDir, mustLibrivoxIDs("527"))
	if sum.NewWorks != 0 || sum.NewRecordings != 1 || sum.NewPeople != 1 {
		t.Fatalf("works/recordings/people = %d/%d/%d, want 0/1/1 (the reader only)\nwarnings: %v",
			sum.NewWorks, sum.NewRecordings, sum.NewPeople, sum.Warnings)
	}
	if entryExists(t, dataDir, personAddr("ian-johnston")) {
		t.Error("the uncredited translator was created")
	}
	if want := []LibrivoxUnrecordedCredit{ianJohnston("metamorphosis", false)}; !reflect.DeepEqual(sum.LibrivoxUnrecordedCredits, want) {
		t.Errorf("unrecorded = %+v, want %+v", sum.LibrivoxUnrecordedCredits, want)
	}
	if got := orphanPeople(t, dataDir); len(got) != 0 {
		t.Errorf("orphans: %v", got)
	}
}

func TestLibrivoxSpellingVariantAttachCreatesNoUncreditedTranslator(t *testing.T) {
	// The same through rule 2 (a spelling variant), and the reported line.
	dataDir := t.TempDir()
	seedWorks(t, dataDir, map[string][]string{"kafkas-book": {"The Metamorphosis", "franz-kafka"}})
	sum := runLibrivox(t, writeSnapshot(t, fixtureProject(t, 527)), dataDir, allLibrivox)
	if sum.LibrivoxVariantsAttached != 1 || entryExists(t, dataDir, personAddr("ian-johnston")) {
		t.Fatalf("attached = %d, ian-johnston created = %v", sum.LibrivoxVariantsAttached,
			entryExists(t, dataDir, personAddr("ian-johnston")))
	}
	if len(sum.LibrivoxUnrecordedCredits) != 1 {
		t.Fatalf("unrecorded = %+v", sum.LibrivoxUnrecordedCredits)
	}
	if got, want := sum.LibrivoxUnrecordedCredits[0].String(),
		`527 "Ian Johnston" (translator, librivox_author_id 1019) -> kafkas-book, person not created`; got != want {
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
	if want := []LibrivoxUnrecordedCredit{ianJohnston("metamorphosis", true)}; !reflect.DeepEqual(sum.LibrivoxUnrecordedCredits, want) {
		t.Errorf("unrecorded = %+v, want %+v", sum.LibrivoxUnrecordedCredits, want)
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
	if sum.NewWorks != 0 || sum.NewRecordings != 1 || len(sum.LibrivoxUnrecordedCredits) != 0 {
		t.Fatalf("works/recordings/unrecorded = %d/%d/%+v\nwarnings: %v",
			sum.NewWorks, sum.NewRecordings, sum.LibrivoxUnrecordedCredits, sum.Warnings)
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
	if sum.NewWorks != 1 || sum.NewPeople != 3 || len(sum.LibrivoxUnrecordedCredits) != 0 {
		t.Fatalf("works/people/unrecorded = %d/%d/%+v", sum.NewWorks, sum.NewPeople, sum.LibrivoxUnrecordedCredits)
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
	if len(sum.LibrivoxUnrecordedCredits) != 0 || !entryExists(t, dataDir, personAddr("willa-muir")) {
		t.Fatalf("unrecorded = %+v, willa-muir created = %v", sum.LibrivoxUnrecordedCredits,
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
	if sum.NewPeople != 0 || sum.NewRecordings != 0 || len(sum.LibrivoxUnrecordedCredits) != 0 || len(sum.Files) != 0 {
		t.Errorf("rerun people/recordings/unrecorded/files = %d/%d/%+v/%d",
			sum.NewPeople, sum.NewRecordings, sum.LibrivoxUnrecordedCredits, len(sum.Files))
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
	// is still attaches (rule 1 first).
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
			seedWorks(t, dataDir, tc.works)
			sum := runLibrivox(t, writeSnapshot(t, retitled(t, 99602, tc.title)), dataDir, allLibrivox)
			if sum.NewWorks != tc.newWorks || sum.NewRecordings != 1 || len(sum.LibrivoxWorklist) != 0 {
				t.Fatalf("works/recordings = %d/%d (want %d/1), worklist = %+v\nwarnings: %v",
					sum.NewWorks, sum.NewRecordings, tc.newWorks, sum.LibrivoxWorklist, sum.Warnings)
			}
			mustValidate(t, dataDir)
		})
	}
}

func TestLibrivoxQualifierRE(t *testing.T) {
	for in, want := range map[string]string{
		"Frankenstein; or The Modern Prometheus (1818)": "Frankenstein; or The Modern Prometheus",
		"Man of Property (Forsyte Saga Vol. 1)":         "Man of Property",
		"Black Beauty [The Autobiography of a Horse]":   "Black Beauty",
		"King in Yellow (part 1) ":                      "King in Yellow",
		"Anna Karenina, Book 1":                         "Anna Karenina, Book 1",
		"(1818)":                                        "",
	} {
		if got := strings.TrimSpace(librivoxQualifierRE.ReplaceAllString(in, "")); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
}
