package importer

import (
	"reflect"
	"slices"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/testpack"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// A person LibriVox lists under two author ids keeps the second in
// librivox_author_aliases, and that decision is what makes a fold stick: the
// next import resolves the alias to the record instead of re-deciding it and
// minting the twin again (meta#2863). No run writes the list.

// aliasedWalter is F. P. Walter as the fold leaves him: 6492 his own, 6858 the
// second record LibriVox holds for him.
func aliasedWalter(t *testing.T) string {
	t.Helper()
	return personFiles(t, "f-p-walter", "F. P. Walter", map[string]any{
		"librivox_author_id": 6492, "librivox_author_aliases": []int{6858},
		"birth_year": 1943, "death_year": 2024,
	})
}

// readBy restates each section as read by the next of readers, cycling.
func readBy(sections []LibrivoxSection, readers ...LibrivoxReader) []LibrivoxSection {
	out := slices.Clone(sections)
	for i := range out {
		out[i].Readers = []LibrivoxReader{readers[i%len(readers)]}
	}
	return out
}

// lvNarratorsOf reads the narrators of a project's recording.
func lvNarratorsOf(t *testing.T, dataDir string, project int) []string {
	t.Helper()
	work, rec, _ := lvRecordingOf(t, dataDir, project)
	var r struct {
		Narrators []string `json:"narrators"`
	}
	readEntity(t, dataDir, recAddr(work, rec), &r)
	return r.Narrators
}

// authorAliases reads a person's alias list off the tree.
func authorAliases(t *testing.T, dataDir, slug string) []int {
	t.Helper()
	var p struct {
		Aliases []int `json:"librivox_author_aliases"`
	}
	readEntity(t, dataDir, personAddr(slug), &p)
	return p.Aliases
}

func TestLibrivoxAuthorAliasResolvesToTheRecord(t *testing.T) {
	// Under another spelling too: the id, not the name, is the person.
	for _, first := range []string{"F. P.", "Frederick Paul"} {
		dataDir := t.TempDir()
		seedTree(t, dataDir, map[string]string{personAddr("f-p-walter"): aliasedWalter(t)})
		snap := writeSnapshot(t, byAuthors(t, 99040, "Notes on Verne", lvDated(6858, first, "Walter", "", "")))
		sum := runLibrivox(t, snap, dataDir, allLibrivox)
		if got := workAuthorsOf(t, dataDir, "notes-on-verne"); !reflect.DeepEqual(got, []string{"f-p-walter"}) {
			t.Fatalf("%s: authors = %v\nnotes: %v", first, got, sum.Notes)
		}
		if len(sum.LibrivoxAuthorSplits) != 0 || len(sum.LibrivoxWorklist) != 0 {
			t.Errorf("%s: splits %+v, worklist %+v; want none", first, sum.LibrivoxAuthorSplits, sum.LibrivoxWorklist)
		}
		if p := personOf(t, dataDir, "f-p-walter"); p.AuthorID != 6492 || p.Name != "F. P. Walter" {
			t.Errorf("%s: f-p-walter = %+v", first, p)
		}
		if got := authorAliases(t, dataDir, "f-p-walter"); !reflect.DeepEqual(got, []int{6858}) {
			t.Errorf("%s: aliases = %v", first, got)
		}
		if entryExists(t, dataDir, personAddr("f-p-walter-librivox-author-6858")) {
			t.Errorf("%s: the twin was minted", first)
		}
		mustValidate(t, dataDir)
		rerunIsANoOp(t, snap, dataDir)
	}
}

func TestLibrivoxWithoutTheAliasTheTwinIsMinted(t *testing.T) {
	// The other half of the decision: with no alias recorded the two ids stay
	// two people and are listed. Two ids never fold automatically, and no run
	// writes the list.
	dataDir := t.TempDir()
	seedTree(t, dataDir, map[string]string{
		personAddr("f-p-walter"): personFiles(t, "f-p-walter", "F. P. Walter", map[string]any{"librivox_author_id": 6492}),
	})
	sum := runLibrivox(t, writeSnapshot(t, byAuthors(t, 99040, "Notes on Verne", lvDated(6858, "F. P.", "Walter", "", ""))), dataDir, allLibrivox)
	const twin = "f-p-walter-librivox-author-6858"
	if got := workAuthorsOf(t, dataDir, "notes-on-verne"); !reflect.DeepEqual(got, []string{twin}) {
		t.Fatalf("authors = %v", got)
	}
	if len(sum.LibrivoxAuthorSplits) != 1 {
		t.Errorf("splits = %+v, want the one", sum.LibrivoxAuthorSplits)
	}
	if got := authorAliases(t, dataDir, "f-p-walter"); got != nil {
		t.Errorf("a run wrote aliases %v", got)
	}
	if got := authorAliases(t, dataDir, twin); got != nil {
		t.Errorf("a run wrote aliases %v on the twin", got)
	}
}

func TestLibrivoxHeldTranslatorUnderAnAliasIsCredited(t *testing.T) {
	// TestLibrivoxHeldTranslatorOnAnotherIDsRecordGoesToTheWorklist's row, once
	// the decision is recorded: H. P. Paull 6787 beside 7024 is credited on
	// the recording, off the worklist, and the record is not touched.
	files := map[string]string{
		personAddr("ian-johnston"): personFiles(t, "ian-johnston", "Ian Johnston",
			map[string]any{"birth_year": 1850, "death_year": 1920, "librivox_author_id": 5555, "librivox_author_aliases": []int{1019}}),
	}
	retailWork(t, files, "a-translated-book", "A Translated Book", "ian-johnston", "")
	dataDir := heldKafkaTree(t, files)
	pr := fixtureProject(t, 527)
	pr.Translators = []LibrivoxPerson{{ID: 1019, FirstName: "Ian", LastName: "Johnston", DOB: "1850", DOD: "1920"}}
	snap := writeSnapshot(t, pr)
	sum := runLibrivox(t, snap, dataDir, allLibrivox)
	if f := lvFidelityOf(t, dataDir, "metamorphosis", "se"); !reflect.DeepEqual(f.Credits, []model.Credit{{Person: "ian-johnston", Role: model.RoleTranslator}}) {
		t.Errorf("credits = %v, want ian-johnston", f.Credits)
	}
	if len(sum.LibrivoxWorklist) != 0 || sum.LibrivoxRestated.TranslatorsCreated != 0 || sum.LibrivoxRestated.TranslatorsUnresolved != 0 {
		t.Errorf("worklist %+v, restated %+v", sum.LibrivoxWorklist, sum.LibrivoxRestated)
	}
	if p := personOf(t, dataDir, "ian-johnston"); p.AuthorID != 5555 {
		t.Errorf("ian-johnston = %+v", p)
	}
	before := testpack.Snapshot(t, dataDir)
	if again := runLibrivox(t, snap, dataDir, allLibrivox); again.NewPeople+again.LibrivoxRestated.Recordings != 0 || len(again.Files) != 0 {
		t.Errorf("rerun = %+v, want a no-op", again.LibrivoxRestated)
	}
	if after := testpack.Snapshot(t, dataDir); !reflect.DeepEqual(before, after) {
		t.Error("rerun changed the tree")
	}
}

func TestLibrivoxVariantAttachUnderAnAlias(t *testing.T) {
	// The work credits the record under a fuller spelling of the name: a row
	// crediting the alias is the same person, so nothing refuses the pairing
	// as two ids (heldByOtherAuthorID, answersToAuthorID) and the row attaches.
	dataDir := t.TempDir()
	files := map[string]string{
		personAddr("henrietta-elizabeth-marshall"): personFiles(t, "henrietta-elizabeth-marshall", "Henrietta Elizabeth Marshall",
			map[string]any{"librivox_author_id": 100, "librivox_author_aliases": []int{200}}),
	}
	retailWork(t, files, "our-island-story", "Our Island Story", "henrietta-elizabeth-marshall", "")
	seedTree(t, dataDir, files)
	sum := runLibrivox(t, writeSnapshot(t, byAuthors(t, 99041, "Our Island Story", lvDated(200, "Henrietta", "Marshall", "", ""))), dataDir, allLibrivox)
	if sum.NewWorks != 0 || sum.NewRecordings != 1 || len(sum.LibrivoxWorklist) != 0 {
		t.Fatalf("summary: %d works, %d recordings, worklist %+v", sum.NewWorks, sum.NewRecordings, sum.LibrivoxWorklist)
	}
	if got := workAuthorsOf(t, dataDir, "our-island-story"); !reflect.DeepEqual(got, []string{"henrietta-elizabeth-marshall"}) {
		t.Errorf("authors = %v", got)
	}
	if got := orphanPeople(t, dataDir); len(got) != 0 {
		t.Errorf("orphans: %v", got)
	}
	mustValidate(t, dataDir)
}

func TestLibrivoxReaderAliasResolvesToTheRecord(t *testing.T) {
	// A reader id held as an alias reads under the record, under its own
	// spelling or another, never minted at -librivox-<id>; a recording read by
	// both ids of one person credits them once.
	aliased := func() string {
		return personFiles(t, "branko-collin", "Branko Collin", map[string]any{"librivox_reader_id": 778, "librivox_reader_aliases": []int{1775}})
	}
	for _, name := range []string{"Branko Collin", "Branko"} {
		dataDir := t.TempDir()
		seedTree(t, dataDir, map[string]string{personAddr("branko-collin"): aliased()})
		pr := byAuthor(t, 99050, "Notes on Reading", "English", lvDated(1, "Some", "Writer", "", ""))
		pr.Sections = readBy(pr.Sections, LibrivoxReader{ReaderID: 1775, DisplayName: name})
		snap := writeSnapshot(t, pr)
		sum := runLibrivox(t, snap, dataDir, allLibrivox)
		rec := lvNarratorsOf(t, dataDir, 99050)
		if !reflect.DeepEqual(rec, []string{"branko-collin"}) {
			t.Fatalf("%s: narrators = %v\nwarnings: %v", name, rec, sum.Warnings)
		}
		if entryExists(t, dataDir, personAddr("branko-collin-librivox-1775")) {
			t.Errorf("%s: the twin was minted", name)
		}
		if p := personOf(t, dataDir, "branko-collin"); p.ReaderID != 778 || p.Name != "Branko Collin" {
			t.Errorf("%s: branko-collin = %+v", name, p)
		}
		mustValidate(t, dataDir)
		rerunIsANoOp(t, snap, dataDir)
	}

	dataDir := t.TempDir()
	seedTree(t, dataDir, map[string]string{personAddr("branko-collin"): aliased()})
	pr := byAuthor(t, 99051, "Two Ids", "English", lvDated(1, "Some", "Writer", "", ""))
	if len(pr.Sections) < 2 {
		t.Fatalf("the fixture has %d sections, this needs two", len(pr.Sections))
	}
	pr.Sections = readBy(pr.Sections, LibrivoxReader{ReaderID: 778, DisplayName: "Branko Collin"}, LibrivoxReader{ReaderID: 1775, DisplayName: "Branko Collin"})
	snap := writeSnapshot(t, pr)
	runLibrivox(t, snap, dataDir, allLibrivox)
	if rec := lvNarratorsOf(t, dataDir, 99051); !reflect.DeepEqual(rec, []string{"branko-collin"}) {
		t.Errorf("narrators = %v, want one credit", rec)
	}
	mustValidate(t, dataDir)
	rerunIsANoOp(t, snap, dataDir)
}

func TestLibrivoxRunKeepsTheAliasesOnALifeYearFill(t *testing.T) {
	// Every run edits a person as a raw map, so the list rides through the
	// year fill, the one write a run makes onto a record it already holds.
	dataDir := t.TempDir()
	seedTree(t, dataDir, map[string]string{
		personAddr("f-p-walter"): personFiles(t, "f-p-walter", "F. P. Walter", map[string]any{"librivox_author_id": 6492, "librivox_author_aliases": []int{6858}}),
	})
	runLibrivox(t, writeSnapshot(t, byAuthors(t, 99040, "Notes on Verne", lvDated(6858, "F. P.", "Walter", "1943", "2024"))), dataDir, allLibrivox)
	p := personOf(t, dataDir, "f-p-walter")
	if p.BirthYear != 1943 || p.DeathYear != 2024 {
		t.Errorf("f-p-walter = %+v, want the years filled", p)
	}
	if got := authorAliases(t, dataDir, "f-p-walter"); !reflect.DeepEqual(got, []int{6858}) {
		t.Errorf("aliases = %v", got)
	}
}
