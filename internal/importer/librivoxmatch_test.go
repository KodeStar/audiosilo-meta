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
	// any spelling variant. (A Cyrillic-named AUTHOR is
	// TestLibrivoxUnsluggableSoleAuthorGoesToTheWorklist's.)
	project := func(id int, title, lang, first, last string) LibrivoxProject {
		return byAuthor(t, id, title, lang, LibrivoxPerson{ID: lvInt(6000 + id%100), FirstName: first, LastName: last})
	}
	snap := writeSnapshot(t,
		project(99600, "Война и мир", "Russian", "Leo", "Tolstoy"),
		project(99601, "Ὀδύσσεια", "Ancient Greek", "", "Homer"),
		project(99602, "論語", "Chinese", "", "Confucius"),
		project(99603, "Война и мир (version 2)", "Russian", "Leo", "Tolstoy"),
	)
	dataDir := t.TempDir()
	sum := runLibrivox(t, snap, dataDir, allLibrivox)
	if sum.NewWorks != 3 || sum.NewRecordings != 4 || sum.LibrivoxFallbackSlugs != 3 || len(sum.LibrivoxWorklist) != 0 {
		t.Fatalf("works/recordings/fallback = %d/%d/%d, want 3/4/3\nworklist: %v\nwarnings: %v",
			sum.NewWorks, sum.NewRecordings, sum.LibrivoxFallbackSlugs, sum.LibrivoxWorklist, sum.Warnings)
	}
	for slug, title := range map[string]string{
		"librivox-99600": "Война и мир",
		"librivox-99601": "Ὀδύσσεια",
		"librivox-99602": "論語",
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
	mustValidate(t, dataDir)

	// The rerun writes nothing: every landed id dedups first.
	before := testpack.Snapshot(t, dataDir)
	again := runLibrivox(t, snap, dataDir, allLibrivox)
	if again.NewWorks+again.NewRecordings+again.NewPeople+again.LibrivoxFallbackSlugs != 0 || len(again.Files) != 0 || again.Skipped != 4 {
		t.Errorf("rerun = %+v", again)
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
		{"Re-Creation", "Recreation"},
		{"Vol. 1-2", "Vol 1 2"},
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
		{"Poems Vol. 1, 2", "Poems Vol. 12"}, // two numbers are not one
		{"Том 1, 2", "Том 12"},               // in a non-Latin title too
		{"कमल", "कमला"},                      // a vowel sign spells another word
		{"こころ", "ごころ"},                       // and so does the kana voicing mark
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

// byAuthor is fixture project 527 under another id and title, by one author
// (translators as given) and read by David Barnes.
func byAuthor(t *testing.T, id int, title, lang string, author LibrivoxPerson, translators ...LibrivoxPerson) LibrivoxProject {
	t.Helper()
	p := retitled(t, id, title)
	p.Language = lang
	p.Authors, p.Translators = []LibrivoxPerson{author}, translators
	return p
}

func TestLibrivoxUnsluggableCoAuthorIsNotRecorded(t *testing.T) {
	// A credit whose name slugs to nothing is dropped before the work is
	// decided and reported unsluggable; the row lands on its other author.
	// Project 2607's shape: "Йордан Йовков" beside "Yordan Yovkov" creates a
	// work by Yovkov alone, and two "Letters" by Cyrillic-named Tolstoy and
	// Aylmer Maude are one work by Maude, attached by the ordinary walk. The
	// catch-all person is never created.
	cyrillic := LibrivoxPerson{ID: 6100, FirstName: "Йордан", LastName: "Йовков"}
	latin := LibrivoxPerson{ID: 6101, FirstName: "Yordan", LastName: "Yovkov"}
	tolstoy := LibrivoxPerson{ID: 6102, FirstName: "Лев", LastName: "Толстой"}
	maude := LibrivoxPerson{ID: 6103, FirstName: "Aylmer", LastName: "Maude"}
	coauthored := func(id int, title string, first, second LibrivoxPerson) LibrivoxProject {
		p := byAuthor(t, id, title, "English", first)
		p.Authors = append(p.Authors, second)
		return p
	}
	snap := writeSnapshot(t,
		coauthored(99900, "Short Stories", cyrillic, latin),
		coauthored(99901, "Letters", tolstoy, maude),
		coauthored(99902, "Letters (version 2)", tolstoy, maude),
	)
	dataDir := t.TempDir()
	sum := runLibrivox(t, snap, dataDir, allLibrivox)
	if sum.NewWorks != 2 || sum.NewRecordings != 3 || len(sum.LibrivoxWorklist) != 0 {
		t.Fatalf("works/recordings = %d/%d, want 2/3\nworklist: %v\nwarnings: %v", sum.NewWorks, sum.NewRecordings, sum.LibrivoxWorklist, sum.Warnings)
	}
	for slug, author := range map[string]string{"short-stories": "yordan-yovkov", "letters": "aylmer-maude"} {
		var w lvWork
		readEntity(t, dataDir, workAddr(slug), &w)
		if !reflect.DeepEqual(w.Authors, []string{author}) || len(w.Credits) != 0 {
			t.Errorf("%s authors/credits = %v/%v, want [%s]", slug, w.Authors, w.Credits, author)
		}
	}
	unsluggable := func(project, id int, name, work string) LibrivoxUnrecordedCredit {
		return LibrivoxUnrecordedCredit{Project: project, AuthorID: id, Name: name, Work: work, Reason: librivoxUnsluggable}
	}
	want := []LibrivoxUnrecordedCredit{
		unsluggable(99900, 6100, "Йордан Йовков", "short-stories"),
		unsluggable(99901, 6102, "Лев Толстой", "letters"),
		unsluggable(99902, 6102, "Лев Толстой", "letters"),
	}
	if !reflect.DeepEqual(sum.LibrivoxUnrecordedCredits, want) {
		t.Errorf("unrecorded = %+v, want %+v", sum.LibrivoxUnrecordedCredits, want)
	}
	if got, line := sum.LibrivoxUnrecordedCredits[0].String(), `99900 "Йордан Йовков" (author, librivox_author_id 6100) -> short-stories, unsluggable`; got != line {
		t.Errorf("line = %q, want %q", got, line)
	}
	if entryExists(t, dataDir, personAddr(model.UnslugPersonID)) {
		t.Error("the catch-all person was created")
	}
	for _, w := range sum.Warnings {
		if strings.Contains(w, "empty slug") {
			t.Errorf("a credit reached the catch-all: %s", w)
		}
	}
	mustValidate(t, dataDir)

	before := testpack.Snapshot(t, dataDir)
	again := runLibrivox(t, snap, dataDir, allLibrivox)
	if again.NewWorks+again.NewRecordings+again.NewPeople != 0 || len(again.Files) != 0 || again.Skipped != 3 {
		t.Errorf("rerun = %+v", again)
	}
	if after := testpack.Snapshot(t, dataDir); !reflect.DeepEqual(before, after) {
		t.Error("rerun changed the tree")
	}
}

func TestLibrivoxUnsluggableSoleAuthorGoesToTheWorklist(t *testing.T) {
	// A row left with no individual author once its unsluggable credits are
	// dropped lands nowhere: alone, beside only a translator (Gorky's and
	// Tolstoy's "Childhood"s, both translated by Leo Wiener, would otherwise be
	// one work by Wiener), or beside only a collective (two "Folk Tales" by two
	// Cyrillic-named authors would be one work by Anonymous). Nothing is
	// written, not even the readers.
	tolstoy := LibrivoxPerson{ID: 6110, FirstName: "Лев", LastName: "Толстой"}
	gorky := LibrivoxPerson{ID: 6111, FirstName: "Максим", LastName: "Горький"}
	wiener := LibrivoxPerson{ID: 6112, FirstName: "Leo", LastName: "Wiener"}
	anon := LibrivoxPerson{ID: 6113, LastName: "Anonymous"}
	withAnon := func(id int, author LibrivoxPerson) LibrivoxProject {
		p := byAuthor(t, id, "Folk Tales", "English", author)
		p.Authors = append(p.Authors, anon)
		return p
	}
	snap := writeSnapshot(t,
		byAuthor(t, 99920, "Детство", "Russian", tolstoy),
		byAuthor(t, 99921, "Childhood", "English", tolstoy, wiener),
		byAuthor(t, 99922, "Childhood", "English", gorky, wiener),
		withAnon(99923, tolstoy),
		withAnon(99924, gorky),
	)
	dataDir := t.TempDir()
	sum := runLibrivox(t, snap, dataDir, allLibrivox)
	if sum.NewWorks+sum.NewRecordings+sum.NewPeople != 0 || len(sum.Files) != 0 {
		t.Fatalf("works/recordings/people = %d/%d/%d, want nothing\nwarnings: %v", sum.NewWorks, sum.NewRecordings, sum.NewPeople, sum.Warnings)
	}
	entry := func(project int, title string, authors ...string) LibrivoxWorklistEntry {
		return LibrivoxWorklistEntry{Project: project, Title: title, Authors: authors, Reason: LibrivoxAuthorUnsluggable, Candidates: []LibrivoxCandidate{}}
	}
	want := []LibrivoxWorklistEntry{
		entry(99920, "Детство", "Лев Толстой"),
		entry(99921, "Childhood", "Лев Толстой", "Leo Wiener (Translator)"),
		entry(99922, "Childhood", "Максим Горький", "Leo Wiener (Translator)"),
		entry(99923, "Folk Tales", "Лев Толстой", "Anonymous"),
		entry(99924, "Folk Tales", "Максим Горький", "Anonymous"),
	}
	if !reflect.DeepEqual(sum.LibrivoxWorklist, want) {
		t.Errorf("worklist = %+v", sum.LibrivoxWorklist)
	}
	if got, line := sum.LibrivoxWorklist[0].String(), `99920 "Детство" by Лев Толстой [author-unsluggable]`; got != line {
		t.Errorf("line = %q, want %q", got, line)
	}
	again := runLibrivox(t, snap, dataDir, allLibrivox)
	if !reflect.DeepEqual(again.LibrivoxWorklist, want) || len(again.Files) != 0 {
		t.Errorf("rerun = %+v", again)
	}
}

func TestLibrivoxMostlyNonLatinTitlesKeyOnTheirOwnScript(t *testing.T) {
	// "Сборник стихов 001" slugs to "001" and "The Война и мир" to "the": read
	// through the slug rule they would join the catalogued "001" and "The" by
	// the same author. Read by their own letters they get librivox-<id>, and
	// "The Война и мир" is "Война и мир" once its article is dropped. A title
	// whose letters are half Latin stays on the slug rule.
	tolstoy := LibrivoxPerson{ID: 6200, FirstName: "Leo", LastName: "Tolstoy"}
	dataDir := t.TempDir()
	seedWorks(t, dataDir, map[string][]string{
		"001": {"001", "leo-tolstoy"},
		"the": {"The", "leo-tolstoy"},
	})
	snap := writeSnapshot(t,
		byAuthor(t, 99910, "Сборник стихов 001", "Russian", tolstoy),
		byAuthor(t, 99911, "Война и мир", "Russian", tolstoy),
		byAuthor(t, 99912, "The Война и мир", "Russian", tolstoy),
		byAuthor(t, 99913, "Anna Karenina (Анна Каренина)", "Russian", tolstoy),
	)
	sum := runLibrivox(t, snap, dataDir, allLibrivox)
	if sum.NewWorks != 3 || sum.NewRecordings != 4 || sum.LibrivoxFallbackSlugs != 2 || len(sum.LibrivoxWorklist) != 0 {
		t.Fatalf("works/recordings/fallback = %d/%d/%d\nworklist: %v\nwarnings: %v",
			sum.NewWorks, sum.NewRecordings, sum.LibrivoxFallbackSlugs, sum.LibrivoxWorklist, sum.Warnings)
	}
	for _, slug := range []string{"001", "the"} {
		if got := recSlugsOf(t, dataDir, slug); !reflect.DeepEqual(got, []string{"se"}) {
			t.Errorf("%s gained %v", slug, got)
		}
	}
	var w lvWork
	readEntity(t, dataDir, workAddr("librivox-99910"), &w)
	if w.Title != "Сборник стихов 001" {
		t.Errorf("librivox-99910 = %+v", w)
	}
	if got := recSlugsOf(t, dataDir, "librivox-99911"); len(got) != 2 {
		t.Errorf("Война и мир recordings = %v, want both projects", got)
	}
	if entryExists(t, dataDir, workAddr("librivox-99913")) || !entryExists(t, dataDir, workAddr("anna-karenina")) {
		t.Error("a half-Latin title left the slug rule")
	}
	mustValidate(t, dataDir)

	before := testpack.Snapshot(t, dataDir)
	again := runLibrivox(t, snap, dataDir, allLibrivox)
	if again.NewWorks+again.NewRecordings+again.NewPeople+again.LibrivoxFallbackSlugs != 0 || len(again.Files) != 0 || again.Skipped != 4 {
		t.Errorf("rerun = %+v", again)
	}
	if after := testpack.Snapshot(t, dataDir); !reflect.DeepEqual(before, after) {
		t.Error("rerun changed the tree")
	}
}

func TestLibrivoxNonLatinTitle(t *testing.T) {
	for title, want := range map[string]bool{
		"Война и мир":                   true,
		"論語":                            true,
		"Сборник стихов 001":            true,
		"The Война и мир":               true,  // judged once the article is dropped
		"Anna Karenina (Анна Каренина)": false, // exactly half: not MORE than half
		"Metamorphosis":                 false,
		"Les Misérables":                false, // accented Latin is Latin
		"1984":                          false,
		"Ab Вой":                        true,
		"Abc Во":                        false,
	} {
		if got := librivoxNonLatinTitle(title); got != want {
			t.Errorf("librivoxNonLatinTitle(%q) = %v, want %v", title, got, want)
		}
	}
}

func TestLibrivoxRepeatMarkerAttachesToThePlainWork(t *testing.T) {
	// A dramatic reading or a numbered version of a catalogued text is another
	// recording of that work: the marker is stripped before the walk, so the
	// row neither creates a work nor waits on the worklist as a qualified twin.
	for name, title := range map[string]string{
		"dramatic reading": "Metamorphosis (Dramatic Reading)",
		"short version":    "Metamorphosis (ver. 2)",
		"stacked":          "Metamorphosis (version 3) (Dramatic Reading)",
		"combined":         "Metamorphosis (Version 3, Dramatic Reading)",
		"bare version":     "Metamorphosis, Version 2",
		"separator before": "Metamorphosis - (version 2)",
	} {
		t.Run(name, func(t *testing.T) {
			dataDir := t.TempDir()
			seedWorks(t, dataDir, map[string][]string{"metamorphosis": {"Metamorphosis", "franz-kafka"}})
			sum := runLibrivox(t, writeSnapshot(t, retitled(t, 99604, title)), dataDir, allLibrivox)
			if sum.NewWorks != 0 || sum.NewRecordings != 1 || len(sum.LibrivoxWorklist) != 0 {
				t.Fatalf("works/recordings = %d/%d, worklist = %+v\nwarnings: %v",
					sum.NewWorks, sum.NewRecordings, sum.LibrivoxWorklist, sum.Warnings)
			}
			if got := recSlugsOf(t, dataDir, "metamorphosis"); !reflect.DeepEqual(got, []string{"david-barnes", "se"}) {
				t.Fatalf("recordings of metamorphosis = %v", got)
			}
			var r lvRecording
			readEntity(t, dataDir, recAddr("metamorphosis", "david-barnes"), &r)
			if r.LibrivoxID != 99604 {
				t.Errorf("attached recording librivox_id = %d", r.LibrivoxID)
			}
			var w lvWork
			readEntity(t, dataDir, workAddr("metamorphosis"), &w)
			if w.Title != "Metamorphosis" {
				t.Errorf("work title = %q", w.Title)
			}
			mustValidate(t, dataDir)
		})
	}
}
