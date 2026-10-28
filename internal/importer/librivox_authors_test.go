package importer

import (
	"reflect"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/testpack"
)

// seedAuthoredWorks seeds authoredWorkFiles.
func seedAuthoredWorks(t *testing.T, dataDir string, works map[string][]string) {
	t.Helper()
	seedTree(t, dataDir, authoredWorkFiles(t, works))
}

// authoredWorkFiles is a catalogue of works by slug -> (title, then author
// slug and name pairs), each with one recording read by someone-else.
func authoredWorkFiles(t *testing.T, works map[string][]string) map[string]string {
	t.Helper()
	files := map[string]string{
		personAddr("someone-else"): testpack.PersonJSON(t, "someone-else", "Someone Else"),
	}
	for slug, spec := range works {
		var authors []string
		for i := 1; i+1 < len(spec); i += 2 {
			authors = append(authors, spec[i])
			files[personAddr(spec[i])] = testpack.PersonJSON(t, spec[i], spec[i+1])
		}
		files[workAddr(slug)] = testpack.WorkJSON(t, slug, spec[0], testpack.WithAuthors(authors...))
		files[recAddr(slug, "se")] = testpack.RecJSON(t, "se", slug, testpack.WithNarrators("someone-else"))
	}
	return files
}

// lvAuthor is a LibriVox author credit named first + last.
func lvAuthor(id int, first, last string) LibrivoxPerson {
	return LibrivoxPerson{ID: lvInt(id), FirstName: first, LastName: last}
}

func TestGradeAuthorSpelling(t *testing.T) {
	// The pairs LibriVox tranches 1 and 2 minted duplicate works for (the
	// row's spelling, then the catalogue's), and the different authors that
	// share those titles.
	for _, tc := range []struct {
		a, b string
		want authorSpelling
	}{
		{"Henrietta Elizabeth Marshall", "Henrietta Marshall", spellingVariant},
		{"Joseph Sheridan Le Fanu", "Sheridan Le Fanu", spellingVariant},
		{"Mary White Rowlandson", "Mary Rowlandson", spellingVariant},
		{"Baroness Emma Orczy", "Baroness Orczy", spellingVariant},
		{"Hendrik van Loon", "Hendrik Willem van Loon", spellingVariant},
		{"Ignatius Loyola Donnelly", "Ignatius Donnelly", spellingVariant},
		{"Poul William Anderson", "Poul Anderson", spellingVariant},
		{"Henry Brooks Adams", "Henry Adams", spellingVariant},
		{"Saint Justin Martyr", "Justin Martyr", spellingVariant},
		{"Francis Parkman Jr.", "Francis Parkman", spellingVariant},
		{"Horatio Alger Jr.", "Horatio Alger", spellingVariant},
		{"Elizabeth Cleghorn Gaskell", "Elizabeth Gaskell", spellingVariant},
		{"Pierre Choderlos de Laclos", "Pierre-Ambroise-François Choderlos de Laclos", spellingVariant},
		{"Harriet Jacobs", "Harriet Ann Jacobs", spellingVariant},
		{"Amelia Ann Blanford Edwards", "Amelia Blanford Edwards", spellingVariant},
		// Looser: an initial, another given name, a one-word name, Saint and St,
		// father and son.
		{"Charles Monroe Sheldon", "Charles M. Sheldon", spellingLoose},
		{"E. Nesbit", "Edith Nesbit", spellingLoose},
		{"Abraham Merritt", "A. Merritt", spellingLoose},
		{"William Henry Hudson", "W. H. Hudson", spellingLoose},
		{"Fanny Burney", "Frances Burney", spellingLoose},
		{"Lev Tolstoy", "Leo Tolstoy", spellingLoose},
		{"Baroness Emma Orczy", "Emmuska Orczy", spellingLoose},
		{"Saint Teresa of Avila", "St Teresa of Ávila", spellingLoose},
		{"Jacob & Wilhelm Grimm", "The Brothers Grimm", spellingLoose},
		{"Homer", "Winslow Homer", spellingLoose},
		{"Alexander Dumas Jr.", "Alexander Dumas Sr.", spellingLoose},
		// Apart: another surname.
		{"James H. Schmitz", "Marie James", spellingApart},
		{"James H. Schmitz", "James Kerr", spellingApart},
		{"James Fenimore Cooper", "James Phelan", spellingApart},
		{"Publius (Ovid) Ovidius Naso", "Ovid", spellingApart},
		{"Gaius Suetonius Tranquillus", "Suetonius", spellingApart},
		{"Saint Augustine of Hippo", "Saint Augustine", spellingApart},
		{"Henrietta Elizabeth Marshall", "Henrietta Marhall", spellingApart},
		{"Epictetus", "Epictetus Arrian", spellingApart},
		{"Йордан Йовков", "Yordan Yovkov", spellingApart},
	} {
		if got := gradeAuthorSpelling(tc.a, tc.b); got != tc.want {
			t.Errorf("gradeAuthorSpelling(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
		if got := gradeAuthorSpelling(tc.b, tc.a); got != tc.want {
			t.Errorf("gradeAuthorSpelling(%q, %q) = %d, want %d (reversed)", tc.b, tc.a, got, tc.want)
		}
	}
}

func TestLibrivoxAuthorVariantAttaches(t *testing.T) {
	// The project's author is the catalogued one spelled with more (or fewer)
	// words: its recording joins that work, the catalogued person is credited
	// and keyed by the project's author id, and no second person is made.
	for name, tc := range map[string]struct {
		title, catalogued    string
		person, personName   string
		first, last, newSlug string
	}{
		"more words":         {"This Country of Ours, Part 2", "This Country of Ours, Part 2", "henrietta-marshall", "Henrietta Marshall", "Henrietta Elizabeth", "Marshall", "henrietta-elizabeth-marshall"},
		"fewer words":        {"Uncle Silas", "Uncle Silas", "joseph-sheridan-le-fanu", "Joseph Sheridan Le Fanu", "Sheridan", "Le Fanu", "sheridan-le-fanu"},
		"suffix and article": {"Oregon Trail", "The Oregon Trail", "francis-parkman", "Francis Parkman", "Francis", "Parkman Jr.", "francis-parkman-jr"},
	} {
		t.Run(name, func(t *testing.T) {
			dataDir := t.TempDir()
			seedAuthoredWorks(t, dataDir, map[string][]string{"the-work": {tc.catalogued, tc.person, tc.personName}})
			snap := writeSnapshot(t, byAuthor(t, 99800, tc.title, "English", lvAuthor(7000, tc.first, tc.last)))
			sum := runLibrivox(t, snap, dataDir, allLibrivox)
			if sum.NewWorks != 0 || sum.NewRecordings != 1 || sum.LibrivoxAuthorVariants != 1 || len(sum.LibrivoxWorklist) != 0 || len(sum.LibrivoxUnrecordedCredits) != 0 {
				t.Fatalf("works/recordings/variants/worklist/unrecorded = %d/%d/%d/%v/%v\nwarnings: %v\nnotes: %v",
					sum.NewWorks, sum.NewRecordings, sum.LibrivoxAuthorVariants, sum.LibrivoxWorklist, sum.LibrivoxUnrecordedCredits, sum.Warnings, sum.Notes)
			}
			if got := recSlugsOf(t, dataDir, "the-work"); !reflect.DeepEqual(got, []string{"david-barnes", "se"}) {
				t.Errorf("recordings = %v", got)
			}
			if entryExists(t, dataDir, personAddr(tc.newSlug)) {
				t.Errorf("a second person %q was created", tc.newSlug)
			}
			var w lvWork
			readEntity(t, dataDir, workAddr("the-work"), &w)
			if !reflect.DeepEqual(w.Authors, []string{tc.person}) {
				t.Errorf("authors = %v", w.Authors)
			}
			var p lvPerson
			readEntity(t, dataDir, personAddr(tc.person), &p)
			if p.AuthorID != 7000 || p.Name != tc.personName {
				t.Errorf("person = %+v, want %q keyed 7000", p, tc.personName)
			}
			spelled := strings.TrimSpace(tc.first + " " + tc.last)
			if note := strings.Join(sum.Notes, "\n"); !strings.Contains(note, `99800 "`+tc.title+`": "`+spelled+`" credited as "`+tc.personName+`"`) {
				t.Errorf("no note naming the respelling: %s", note)
			}
			mustValidate(t, dataDir)

			before := testpack.Snapshot(t, dataDir)
			again := runLibrivox(t, snap, dataDir, allLibrivox)
			if again.NewWorks+again.NewRecordings+again.NewPeople+again.LibrivoxIDsStamped != 0 || len(again.Files) != 0 || again.Skipped != 1 {
				t.Errorf("rerun = %+v", again)
			}
			if after := testpack.Snapshot(t, dataDir); !reflect.DeepEqual(before, after) {
				t.Error("rerun changed the tree")
			}
		})
	}
}

func TestLibrivoxAuthorVariantWithACoAuthor(t *testing.T) {
	// One author agrees outright, the other is a spelling: still the work.
	dataDir := t.TempDir()
	seedAuthoredWorks(t, dataDir, map[string][]string{"the-work": {"Letters", "ada-one", "Ada One", "bea-two", "Bea Two"}})
	pr := byAuthor(t, 99801, "Letters", "English", lvAuthor(7001, "Ada", "One"))
	pr.Authors = append(pr.Authors, lvAuthor(7002, "Bea Clara", "Two"))
	sum := runLibrivox(t, writeSnapshot(t, pr), dataDir, allLibrivox)
	if sum.NewWorks != 0 || sum.LibrivoxAuthorVariants != 1 || entryExists(t, dataDir, personAddr("bea-clara-two")) {
		t.Fatalf("works/variants = %d/%d; worklist %v", sum.NewWorks, sum.LibrivoxAuthorVariants, sum.LibrivoxWorklist)
	}
	mustValidate(t, dataDir)
}

func TestLibrivoxAuthorVariantKeepsAnotherIDsPerson(t *testing.T) {
	// The catalogued person already carries another LibriVox author id: the
	// project still attaches and credits them, the id is not written twice and
	// the run says so.
	dataDir := t.TempDir()
	files := authoredWorkFiles(t, map[string][]string{"the-work": {"Uncle Silas", "sheridan-le-fanu", "Sheridan Le Fanu"}})
	files[personAddr("sheridan-le-fanu")] = testpack.WithField(t, files[personAddr("sheridan-le-fanu")], "librivox_author_id", 42)
	seedTree(t, dataDir, files)
	sum := runLibrivox(t, writeSnapshot(t, byAuthor(t, 99802, "Uncle Silas", "English", lvAuthor(7003, "Joseph Sheridan", "Le Fanu"))), dataDir, allLibrivox)
	if sum.NewWorks != 0 || sum.LibrivoxAuthorVariants != 1 {
		t.Fatalf("works/variants = %d/%d", sum.NewWorks, sum.LibrivoxAuthorVariants)
	}
	var p lvPerson
	readEntity(t, dataDir, personAddr("sheridan-le-fanu"), &p)
	if p.AuthorID != 42 {
		t.Errorf("person = %+v", p)
	}
	if !strings.Contains(strings.Join(sum.Warnings, "\n"), `already carries librivox_author_id 42; librivox_author_id 7003`) {
		t.Errorf("no warning about the second id: %v", sum.Warnings)
	}
	mustValidate(t, dataDir)
}

func TestLibrivoxAuthorVariantDenied(t *testing.T) {
	// Another surname is another author (a new work, no worklist row); a looser
	// likeness - an initial, another given name, a one-word name, Saint and St -
	// is an author-spelling worklist row and writes nothing; two works the row's
	// author is a spelling of, or one beside a looser likeness, decide nothing.
	cases := map[string]struct {
		works    map[string][]string
		first    string
		last     string
		newWorks int
		reason   string
		want     []LibrivoxCandidate
	}{
		"another surname": {
			works: map[string][]string{"legacy": {"Legacy", "james-kerr", "James Kerr"}, "legacy-2": {"Legacy", "marie-james", "Marie James"}},
			first: "James H.", last: "Schmitz", newWorks: 1,
		},
		"an initial": {
			works: map[string][]string{"the-phoenix-and-the-carpet": {"The Phoenix and the Carpet", "edith-nesbit", "Edith Nesbit"}},
			first: "E.", last: "Nesbit", reason: LibrivoxAuthorSpelling,
			want: []LibrivoxCandidate{{"the-phoenix-and-the-carpet", "The Phoenix and the Carpet"}},
		},
		"another given name": {
			works: map[string][]string{"evelina": {"Evelina", "frances-burney", "Frances Burney"}},
			first: "Fanny", last: "Burney", reason: LibrivoxAuthorSpelling,
			want: []LibrivoxCandidate{{"evelina", "Evelina"}},
		},
		"a one-word name": {
			works: map[string][]string{"sketches": {"Sketches", "winslow-homer", "Winslow Homer"}},
			last:  "Homer", reason: LibrivoxAuthorSpelling,
			want: []LibrivoxCandidate{{"sketches", "Sketches"}},
		},
		"saint and st": {
			works: map[string][]string{"the-interior-castle": {"The Interior Castle", "st-teresa-of-avila", "St Teresa of Ávila"}},
			first: "Saint Teresa", last: "of Avila", reason: LibrivoxAuthorSpelling,
			want: []LibrivoxCandidate{{"the-interior-castle", "The Interior Castle"}},
		},
		"two variant works": {
			works: map[string][]string{
				"les-liaisons-dangereuses":   {"Les Liaisons Dangereuses", "choderlos-de-laclos", "Choderlos de Laclos"},
				"les-liaisons-dangereuses-2": {"Les liaisons dangereuses", "pierre-ambroise-francois-choderlos-de-laclos", "Pierre-Ambroise-François Choderlos de Laclos"},
			},
			first: "Pierre", last: "Choderlos de Laclos", reason: LibrivoxSeveralWorks,
			want: []LibrivoxCandidate{{"les-liaisons-dangereuses", "Les Liaisons Dangereuses"}, {"les-liaisons-dangereuses-2", "Les liaisons dangereuses"}},
		},
		"a variant beside a looser likeness": {
			works: map[string][]string{
				"the-scarlet-pimpernel":   {"The Scarlet Pimpernel", "baroness-orczy", "Baroness Orczy"},
				"the-scarlet-pimpernel-2": {"The Scarlet Pimpernel", "emmuska-orczy", "Emmuska Orczy"},
			},
			first: "Baroness Emma", last: "Orczy", reason: LibrivoxAuthorSpelling,
			want: []LibrivoxCandidate{{"the-scarlet-pimpernel", "The Scarlet Pimpernel"}, {"the-scarlet-pimpernel-2", "The Scarlet Pimpernel"}},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dataDir := t.TempDir()
			seedAuthoredWorks(t, dataDir, tc.works)
			title := tc.works[firstKey(tc.works)][0]
			pr := byAuthor(t, 99810, strings.TrimPrefix(strings.TrimPrefix(title, "The "), "Les "), "English", lvAuthor(7010, tc.first, tc.last))
			before := testpack.Snapshot(t, dataDir)
			sum := runLibrivox(t, writeSnapshot(t, pr), dataDir, allLibrivox)
			if sum.LibrivoxAuthorVariants != 0 || sum.NewWorks != tc.newWorks {
				t.Fatalf("variants/works = %d/%d, want 0/%d\nworklist: %v", sum.LibrivoxAuthorVariants, sum.NewWorks, tc.newWorks, sum.LibrivoxWorklist)
			}
			for slug := range tc.works {
				if got := recSlugsOf(t, dataDir, slug); !reflect.DeepEqual(got, []string{"se"}) {
					t.Errorf("%s gained %v", slug, got)
				}
			}
			if tc.reason == "" {
				if len(sum.LibrivoxWorklist) != 0 {
					t.Errorf("worklist = %+v", sum.LibrivoxWorklist)
				}
				mustValidate(t, dataDir)
				return
			}
			want := []LibrivoxWorklistEntry{{Project: 99810, Title: pr.Title, Authors: []string{pr.Authors[0].name()}, Reason: tc.reason, Candidates: tc.want}}
			if !reflect.DeepEqual(sum.LibrivoxWorklist, want) {
				t.Errorf("worklist = %+v, want %+v", sum.LibrivoxWorklist, want)
			}
			if after := testpack.Snapshot(t, dataDir); !reflect.DeepEqual(before, after) {
				t.Error("a worklist row changed the tree")
			}
		})
	}
}

// firstKey is m's smallest key.
func firstKey(m map[string][]string) string {
	var first string
	for k := range m {
		if first == "" || k < first {
			first = k
		}
	}
	return first
}

func TestLibrivoxAuthorVariantWithinOneRun(t *testing.T) {
	// Both spellings are new in one run: the first project creates the work and
	// its person, the second meets them and attaches. Its own author id stays
	// off the person (one id per person), with a warning.
	snap := writeSnapshot(t,
		byAuthor(t, 99820, "Narrative of the Captivity", "English", lvAuthor(7020, "Mary White", "Rowlandson")),
		byAuthor(t, 99821, "The Narrative of the Captivity", "English", lvAuthor(7021, "Mary", "Rowlandson")),
	)
	dataDir := t.TempDir()
	sum := runLibrivox(t, snap, dataDir, allLibrivox)
	if sum.NewWorks != 1 || sum.NewRecordings != 2 || sum.LibrivoxAuthorVariants != 1 || entryExists(t, dataDir, personAddr("mary-rowlandson")) {
		t.Fatalf("works/recordings/variants = %d/%d/%d; worklist %v", sum.NewWorks, sum.NewRecordings, sum.LibrivoxAuthorVariants, sum.LibrivoxWorklist)
	}
	if got := recSlugsOf(t, dataDir, "narrative-of-the-captivity"); len(got) != 2 {
		t.Errorf("recordings = %v", got)
	}
	var p lvPerson
	readEntity(t, dataDir, personAddr("mary-white-rowlandson"), &p)
	if p.AuthorID != 7020 {
		t.Errorf("person = %+v", p)
	}
	mustValidate(t, dataDir)

	before := testpack.Snapshot(t, dataDir)
	again := runLibrivox(t, snap, dataDir, allLibrivox)
	if again.NewWorks+again.NewRecordings+again.NewPeople != 0 || len(again.Files) != 0 || again.Skipped != 2 {
		t.Errorf("rerun = %+v", again)
	}
	if after := testpack.Snapshot(t, dataDir); !reflect.DeepEqual(before, after) {
		t.Error("rerun changed the tree")
	}
}

func TestLibrivoxAuthorVariantKeysTheIDForTheBatch(t *testing.T) {
	// The attaching project's author id is the catalogued person for the whole
	// batch: the id's other project, whose title is new (and which comes FIRST),
	// creates its work crediting that person too, not a second record, and the
	// person is keyed by the id.
	dataDir := t.TempDir()
	seedAuthoredWorks(t, dataDir, map[string][]string{"the-oregon-trail": {"The Oregon Trail", "francis-parkman", "Francis Parkman"}})
	parkman := lvAuthor(7030, "Francis", "Parkman, Jr.")
	snap := writeSnapshot(t,
		byAuthor(t, 99830, "Montcalm and Wolfe", "English", parkman),
		byAuthor(t, 99831, "Oregon Trail", "English", parkman),
	)
	sum := runLibrivox(t, snap, dataDir, allLibrivox)
	if sum.NewWorks != 1 || sum.NewRecordings != 2 || sum.LibrivoxAuthorVariants != 2 || entryExists(t, dataDir, personAddr("francis-parkman-jr")) {
		t.Fatalf("works/recordings/variants = %d/%d/%d\nnotes: %v\nwarnings: %v", sum.NewWorks, sum.NewRecordings, sum.LibrivoxAuthorVariants, sum.Notes, sum.Warnings)
	}
	var w lvWork
	readEntity(t, dataDir, workAddr("montcalm-and-wolfe"), &w)
	if !reflect.DeepEqual(w.Authors, []string{"francis-parkman"}) {
		t.Errorf("new work authors = %v", w.Authors)
	}
	if got := recSlugsOf(t, dataDir, "the-oregon-trail"); len(got) != 2 {
		t.Errorf("recordings = %v", got)
	}
	var p lvPerson
	readEntity(t, dataDir, personAddr("francis-parkman"), &p)
	if p.AuthorID != 7030 {
		t.Errorf("person = %+v", p)
	}
	if strings.Contains(strings.Join(sum.Warnings, "\n"), "not recorded") {
		t.Errorf("an id was refused: %v", sum.Warnings)
	}
	mustValidate(t, dataDir)

	before := testpack.Snapshot(t, dataDir)
	again := runLibrivox(t, snap, dataDir, allLibrivox)
	if again.NewWorks+again.NewRecordings+again.NewPeople+again.LibrivoxAuthorVariants != 0 || len(again.Files) != 0 || again.Skipped != 2 {
		t.Errorf("rerun = %+v", again)
	}
	if after := testpack.Snapshot(t, dataDir); !reflect.DeepEqual(before, after) {
		t.Error("rerun changed the tree")
	}
}

func TestLibrivoxAuthorVariantLeavesAnIDOffAKeyedPerson(t *testing.T) {
	// The catalogued person already carries another author id, so the batch
	// rule stands down: the attaching project still credits them, and the id's
	// other project keeps its own person, as before.
	dataDir := t.TempDir()
	files := authoredWorkFiles(t, map[string][]string{"the-oregon-trail": {"The Oregon Trail", "francis-parkman", "Francis Parkman"}})
	files[personAddr("francis-parkman")] = testpack.WithField(t, files[personAddr("francis-parkman")], "librivox_author_id", 42)
	seedTree(t, dataDir, files)
	parkman := lvAuthor(7031, "Francis", "Parkman, Jr.")
	sum := runLibrivox(t, writeSnapshot(t,
		byAuthor(t, 99840, "Montcalm and Wolfe", "English", parkman),
		byAuthor(t, 99841, "Oregon Trail", "English", parkman),
	), dataDir, allLibrivox)
	if sum.NewWorks != 1 || sum.NewRecordings != 2 || sum.LibrivoxAuthorVariants != 1 {
		t.Fatalf("works/recordings/variants = %d/%d/%d\nnotes: %v", sum.NewWorks, sum.NewRecordings, sum.LibrivoxAuthorVariants, sum.Notes)
	}
	var w lvWork
	readEntity(t, dataDir, workAddr("montcalm-and-wolfe"), &w)
	if !reflect.DeepEqual(w.Authors, []string{"francis-parkman-jr"}) {
		t.Errorf("new work authors = %v", w.Authors)
	}
	if got := recSlugsOf(t, dataDir, "the-oregon-trail"); len(got) != 2 {
		t.Errorf("recordings = %v", got)
	}
	mustValidate(t, dataDir)
}
