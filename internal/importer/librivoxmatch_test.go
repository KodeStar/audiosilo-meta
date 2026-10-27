package importer

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/testpack"
	"github.com/kodestar/audiosilo-meta/pkg/check"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// retitled is fixture project 527 (Metamorphosis, Kafka tr. Ian Johnston, read
// by David Barnes) under another id and title.
func retitled(t *testing.T, id int, title string) LibrivoxProject {
	t.Helper()
	p := fixtureProject(t, 527)
	p.ID, p.Title = lvInt(id), title
	return p
}

// seedWorks seeds works by slug -> (title, authors...), each with one recording
// read by someone-else, plus a person record for every author named.
func seedWorks(t *testing.T, dataDir string, works map[string][]string) {
	t.Helper()
	files := map[string]string{
		personAddr("someone-else"): testpack.PersonJSON(t, "someone-else", "Someone Else"),
	}
	for slug, spec := range works {
		for _, a := range spec[1:] {
			files[personAddr(a)] = testpack.PersonJSON(t, a, a)
		}
		files[workAddr(slug)] = testpack.WorkJSON(t, slug, spec[0], testpack.WithAuthors(spec[1:]...))
		files[recAddr(slug, "se")] = testpack.RecJSON(t, "se", slug, testpack.WithNarrators("someone-else"))
	}
	seedTree(t, dataDir, files)
}

func mustValidate(t *testing.T, dataDir string) {
	t.Helper()
	if res := check.Load(dataDir); !res.OK() {
		t.Fatalf("tree failed validation:\n%v", res.Problems)
	}
}

func TestLibrivoxAttachesASpellingVariant(t *testing.T) {
	// Both directions: the catalogue's title carries the article, or the
	// project's does. Either way the project is a recording of that work.
	for name, tc := range map[string]struct{ catalogued, project string }{
		"catalogue has the article": {"The Metamorphosis", "Metamorphosis"},
		"project has the article":   {"Metamorphosis", "The Metamorphosis"},
		"punctuation apart":         {"Metamorphosis!", "The Metamorphosis."},
	} {
		t.Run(name, func(t *testing.T) {
			dataDir := t.TempDir()
			seedWorks(t, dataDir, map[string][]string{"kafkas-book": {tc.catalogued, "franz-kafka"}})
			sum := runLibrivox(t, writeSnapshot(t, retitled(t, 99500, tc.project)), dataDir, allLibrivox)
			if sum.NewWorks != 0 || sum.NewRecordings != 1 || sum.LibrivoxVariantsAttached != 1 || len(sum.LibrivoxWorklist) != 0 {
				t.Fatalf("works/recordings/attached/worklist = %d/%d/%d/%v\nwarnings: %v",
					sum.NewWorks, sum.NewRecordings, sum.LibrivoxVariantsAttached, sum.LibrivoxWorklist, sum.Warnings)
			}
			if got := recSlugsOf(t, dataDir, "kafkas-book"); !reflect.DeepEqual(got, []string{"david-barnes", "se"}) {
				t.Errorf("recordings = %v", got)
			}
			if !strings.Contains(strings.Join(sum.Notes, "\n"), `99500 "`+tc.project+`" -> "kafkas-book"`) {
				t.Errorf("no note naming the attachment: %v", sum.Notes)
			}
			var p lvPerson
			readEntity(t, dataDir, personAddr("franz-kafka"), &p)
			if p.AuthorID != 100 {
				t.Errorf("the catalogued author was not keyed: %+v", p)
			}
			mustValidate(t, dataDir)
		})
	}
}

func TestLibrivoxSpellingVariantNeedsTheSameAuthor(t *testing.T) {
	// "The Metamorphosis" by Ovid is another book: a new work beside it, and no
	// worklist entry (a different author is not ambiguous).
	dataDir := t.TempDir()
	seedWorks(t, dataDir, map[string][]string{"metamorphosis": {"Metamorphosis", "ovid"}})
	sum := runLibrivox(t, writeSnapshot(t, retitled(t, 99501, "The Metamorphosis")), dataDir, allLibrivox)
	if sum.NewWorks != 1 || sum.NewRecordings != 1 || sum.LibrivoxVariantsAttached != 0 || len(sum.LibrivoxWorklist) != 0 {
		t.Fatalf("works/recordings/attached/worklist = %d/%d/%d/%v", sum.NewWorks, sum.NewRecordings, sum.LibrivoxVariantsAttached, sum.LibrivoxWorklist)
	}
	if got := recSlugsOf(t, dataDir, "metamorphosis"); !reflect.DeepEqual(got, []string{"se"}) {
		t.Errorf("Ovid's work gained %v", got)
	}
	if !entryExists(t, dataDir, workAddr("the-metamorphosis")) {
		t.Error("no new work for the project")
	}
	mustValidate(t, dataDir)
}

func TestLibrivoxAmbiguousVariantsGoToTheWorklist(t *testing.T) {
	// Two works by Kafka carry the title (several-works), and a work whose
	// authors only partly agree with the project's (author-unmatched): neither
	// is guessed. Nothing is written - not even the project's people.
	cases := map[string]struct {
		works  map[string][]string
		reason string
		want   []LibrivoxCandidate
	}{
		"two candidate works": {
			works: map[string][]string{
				"metamorphosis":        {"Metamorphosis", "franz-kafka"},
				"metamorphosis-reread": {"Metamorphosis", "franz-kafka"},
			},
			reason: LibrivoxSeveralWorks,
			want:   []LibrivoxCandidate{{"metamorphosis", "Metamorphosis"}, {"metamorphosis-reread", "Metamorphosis"}},
		},
		"author only partly agrees": {
			works:  map[string][]string{"metamorphosis": {"Metamorphosis", "franz-kafka", "max-brod"}},
			reason: LibrivoxAuthorUnmatched,
			want:   []LibrivoxCandidate{{"metamorphosis", "Metamorphosis"}},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dataDir := t.TempDir()
			seedWorks(t, dataDir, tc.works)
			before := testpack.Snapshot(t, dataDir)
			sum := runLibrivox(t, writeSnapshot(t, retitled(t, 99502, "The Metamorphosis")), dataDir, allLibrivox)
			want := []LibrivoxWorklistEntry{{
				Project: 99502, Title: "The Metamorphosis",
				Authors: []string{"Franz Kafka", "Ian Johnston (Translator)"},
				Reason:  tc.reason, Candidates: tc.want,
			}}
			if !reflect.DeepEqual(sum.LibrivoxWorklist, want) {
				t.Errorf("worklist = %+v\nwant %+v", sum.LibrivoxWorklist, want)
			}
			if sum.NewWorks+sum.NewRecordings+sum.NewPeople != 0 || len(sum.Files) != 0 {
				t.Errorf("a worklist row wrote something: %+v", sum)
			}
			if after := testpack.Snapshot(t, dataDir); !reflect.DeepEqual(before, after) {
				t.Error("the tree changed")
			}
		})
	}
}

func TestLibrivoxGuardRefusalIsOnTheWorklist(t *testing.T) {
	// "Metamorphosis: A Novella" is the catalogued Metamorphosis to the
	// duplicate-identity guard's coarser key, not to the spelling-variant rule:
	// still a skip, now listed by project id - and the --conflicts row carries
	// the id too, since the row has no ASIN.
	dataDir := t.TempDir()
	seedWorks(t, dataDir, map[string][]string{"metamorphosis": {"Metamorphosis", "franz-kafka"}})
	var conflicts bytes.Buffer
	sum, err := RunLibrivox(writeSnapshot(t, retitled(t, 99503, "Metamorphosis: A Novella")), allLibrivox,
		Options{DataDir: dataDir, ImportDate: testImportDate, Conflicts: &conflicts})
	if err != nil {
		t.Fatal(err)
	}
	want := []LibrivoxWorklistEntry{{
		Project: 99503, Title: "Metamorphosis: A Novella",
		Authors: []string{"Franz Kafka", "Ian Johnston (Translator)"},
		Reason:  LibrivoxDifferentlySpelled, Candidates: []LibrivoxCandidate{{"metamorphosis", "Metamorphosis"}},
	}}
	if !reflect.DeepEqual(sum.LibrivoxWorklist, want) || sum.SkippedDuplicateIdentity != 1 || sum.NewRecordings != 0 {
		t.Fatalf("worklist = %+v, refused %d, recordings %d\nwarnings: %v", sum.LibrivoxWorklist, sum.SkippedDuplicateIdentity, sum.NewRecordings, sum.Warnings)
	}
	if !strings.Contains(conflicts.String(), `"asin":"","librivox_id":99503,"work":"metamorphosis"`) {
		t.Errorf("conflict row = %s", conflicts.String())
	}
}

func TestLibrivoxNonLatinTitlesGetTheProjectSlug(t *testing.T) {
	// Cyrillic, Greek and CJK titles by Latin-named authors: each is its own
	// work at librivox-<id>, titled as LibriVox writes it, never "untitled". A
	// second project of one of them attaches to its work by the same rule as
	// any spelling variant. A Cyrillic-named author falls back to the catch-all
	// person, so a title match resting on that person decides nothing.
	project := func(id int, title, lang, first, last string) LibrivoxProject {
		p := fixtureProject(t, 527)
		p.ID, p.Title, p.Language, p.Translators = lvInt(id), title, lang, nil
		p.Authors = []LibrivoxPerson{{ID: lvInt(6000 + id%100), FirstName: first, LastName: last}}
		return p
	}
	unslugAuthor := func(id int, title string) LibrivoxProject {
		p := project(id, title, "Russian", "Лев", "Толстой")
		p.Sections = append([]LibrivoxSection(nil), p.Sections...)
		for i := range p.Sections {
			p.Sections[i].Readers = []LibrivoxReader{{ReaderID: 5000, DisplayName: "Иван"}}
		}
		return p
	}
	snap := writeSnapshot(t,
		project(99600, "Война и мир", "Russian", "Leo", "Tolstoy"),
		project(99601, "Ὀδύσσεια", "Ancient Greek", "", "Homer"),
		project(99602, "論語", "Chinese", "", "Confucius"),
		project(99603, "Война и мир (version 2)", "Russian", "Leo", "Tolstoy"),
		unslugAuthor(99604, "Анна Каренина"),
		unslugAuthor(99605, "Анна Каренина (version 2)"),
	)
	dataDir := t.TempDir()
	sum := runLibrivox(t, snap, dataDir, allLibrivox)
	if sum.NewWorks != 4 || sum.NewRecordings != 5 || sum.LibrivoxFallbackSlugs != 4 {
		t.Fatalf("works/recordings/fallback = %d/%d/%d, want 4/5/4\nworklist: %v\nwarnings: %v",
			sum.NewWorks, sum.NewRecordings, sum.LibrivoxFallbackSlugs, sum.LibrivoxWorklist, sum.Warnings)
	}
	for slug, title := range map[string]string{
		"librivox-99600": "Война и мир",
		"librivox-99601": "Ὀδύσσεια",
		"librivox-99602": "論語",
		"librivox-99604": "Анна Каренина",
	} {
		var w lvWork
		readEntity(t, dataDir, workAddr(slug), &w)
		if w.Title != title {
			t.Errorf("%s title = %q, want %q", slug, w.Title, title)
		}
	}
	if entryExists(t, dataDir, workAddr("untitled")) || entryExists(t, dataDir, workAddr("librivox-99603")) {
		t.Error("a non-Latin title was filed under untitled, or the second War and Peace made its own work")
	}
	var r lvRecording
	readEntity(t, dataDir, recAddr("librivox-99600", "david-barnes-2"), &r)
	if r.LibrivoxID != 99603 {
		t.Errorf("the second War and Peace = %+v", r)
	}
	want := []LibrivoxWorklistEntry{{
		Project: 99605, Title: "Анна Каренина", Authors: []string{"Лев Толстой"},
		Reason: LibrivoxAuthorUnmatched, Candidates: []LibrivoxCandidate{{"librivox-99604", "Анна Каренина"}},
	}}
	if !reflect.DeepEqual(sum.LibrivoxWorklist, want) {
		t.Errorf("worklist = %+v", sum.LibrivoxWorklist)
	}
	var p lvPerson
	readEntity(t, dataDir, personAddr(model.UnslugPersonID), &p)
	if p.ReaderID != 0 || p.AuthorID != 0 {
		t.Errorf("the catch-all person was stamped: %+v", p)
	}
	mustValidate(t, dataDir)

	// The rerun writes nothing: every landed id dedups first; the worklist row
	// is listed again, unchanged.
	before := testpack.Snapshot(t, dataDir)
	again := runLibrivox(t, snap, dataDir, allLibrivox)
	if again.NewWorks+again.NewRecordings+again.NewPeople+again.LibrivoxFallbackSlugs != 0 || len(again.Files) != 0 || again.Skipped != 5 {
		t.Errorf("rerun = %+v", again)
	}
	if !reflect.DeepEqual(again.LibrivoxWorklist, want) {
		t.Errorf("rerun worklist = %+v", again.LibrivoxWorklist)
	}
	if after := testpack.Snapshot(t, dataDir); !reflect.DeepEqual(before, after) {
		t.Error("rerun changed the tree")
	}
}

func TestLibrivoxFallbackSlugTaken(t *testing.T) {
	dataDir := t.TempDir()
	seedWorks(t, dataDir, map[string][]string{"librivox-99700": {"Something Else", "jane-doe"}})
	p := fixtureProject(t, 527)
	p.ID, p.Title = 99700, "Превращение"
	sum := runLibrivox(t, writeSnapshot(t, p), dataDir, allLibrivox)
	want := []LibrivoxWorklistEntry{{
		Project: 99700, Title: "Превращение", Authors: []string{"Franz Kafka", "Ian Johnston (Translator)"},
		Reason: LibrivoxFallbackTaken, Candidates: []LibrivoxCandidate{{"librivox-99700", "Something Else"}},
	}}
	if !reflect.DeepEqual(sum.LibrivoxWorklist, want) || sum.NewRecordings != 0 {
		t.Errorf("worklist = %+v, recordings %d", sum.LibrivoxWorklist, sum.NewRecordings)
	}
}

func TestLibrivoxTitleKey(t *testing.T) {
	same := [][2]string{
		{"The Metamorphosis", "Metamorphosis"},
		{"A Christmas Carol", "Christmas Carol"},
		{"An Ideal Husband", "Ideal Husband"},
		{"Le Comte de Monte-Cristo", "Comte de Monte Cristo"},
		{"La Peau de chagrin", "Peau de Chagrin"},
		{"Les Misérables", "Miserables"},
		{"L'Étranger", "Étranger"},
		{"L’Avare", "Avare"},
		{"Der Prozess", "Prozess"},
		{"Die Verwandlung", "Verwandlung"},
		{"Das Schloss", "Schloss"},
		{"El Lazarillo", "Lazarillo"},
		{"Los Pazos de Ulloa", "Pazos de Ulloa"},
		{"Las Moradas", "Moradas"},
		{"Il Principe", "Principe"},
		{"Lo Stagno", "Stagno"},
		{"De Avonden", "Avonden"},
		{"Het Achterhuis", "Achterhuis"},
		{"Een Vlucht", "Vlucht"},
		{"Candide, ou l'Optimisme", "Candide ou L’optimisme"},
		{"ВОЙНА И МИР", "война и мир"},
		{"Ὀδύσσεια", "Οδυσσεια"},
	}
	for _, p := range same {
		if a, b := librivoxTitleKey(p[0]), librivoxTitleKey(p[1]); a == "" || a != b {
			t.Errorf("key(%q) = %q, key(%q) = %q; want one key", p[0], a, p[1], b)
		}
	}
	apart := [][2]string{
		{"The", "A"},                      // an article alone is the title
		{"Thermodynamics", "Rmodynamics"}, // "the" inside a word is not an article
		{"The Metamorphosis", "Metamorphoses"},
		{"Short Story Collection Vol. 001", "Short Story Collection Vol. 002"},
		{"Une Vie", "Vie"}, // only the listed articles
		{"Война и мир", "Анна Каренина"},
	}
	for _, p := range apart {
		if a, b := librivoxTitleKey(p[0]), librivoxTitleKey(p[1]); a == b {
			t.Errorf("key(%q) = key(%q) = %q; want them apart", p[0], p[1], a)
		}
	}
	if k := librivoxTitleKey(" ?! "); k != "" {
		t.Errorf("a title with no letter keyed %q", k)
	}
}

func TestLibrivoxWorklistEntryString(t *testing.T) {
	e := LibrivoxWorklistEntry{
		Project: 527, Title: "The Metamorphosis", Authors: []string{"Franz Kafka"}, Reason: LibrivoxSeveralWorks,
		Candidates: []LibrivoxCandidate{{"metamorphosis", "Metamorphosis"}, {"metamorphosis-2", "Metamorphosis"}},
	}
	want := `527 "The Metamorphosis" by Franz Kafka [several-works]: metamorphosis "Metamorphosis"; metamorphosis-2 "Metamorphosis"`
	if got := e.String(); got != want {
		t.Errorf("String() =\n%s\nwant\n%s", got, want)
	}
	if got := (LibrivoxWorklistEntry{Project: 1, Title: "X", Reason: LibrivoxFallbackTaken}).String(); got != `1 "X" [fallback-slug-taken]` {
		t.Errorf("bare entry = %s", got)
	}
}
