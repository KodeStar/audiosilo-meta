package importer

import (
	"reflect"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// A person LibriVox lists under two ids keeps the second as an alias, and that
// decision is what makes a fold stick: the next import resolves the alias to the
// record instead of re-deciding it and minting the twin again (meta#2863). No run
// writes the list (the rule is on model.Person).

func TestLibrivoxAuthorAliasResolvesToTheRecord(t *testing.T) {
	// Under another spelling too: the id, not the name, is the person. LibriVox
	// states years under the alias, which the run fills, keeping the list: the
	// one write a run makes onto a record it already holds, as a raw map.
	for _, first := range []string{"F. P.", "Frederick Paul"} {
		dataDir := t.TempDir()
		seedTree(t, dataDir, map[string]string{personAddr("f-p-walter"): personFiles(t, "f-p-walter", "F. P. Walter", map[string]any{
			"librivox_author_id": 6492, "librivox_author_aliases": []int{6858},
		})})
		snap := writeSnapshot(t, byAuthors(t, 99040, "Notes on Verne", lvDated(6858, first, "Walter", "1943", "2024")))
		sum := runLibrivox(t, snap, dataDir, allLibrivox)
		if got := workAuthorsOf(t, dataDir, "notes-on-verne"); !reflect.DeepEqual(got, []string{"f-p-walter"}) {
			t.Fatalf("%s: authors = %v\nnotes: %v", first, got, sum.Notes)
		}
		if len(sum.LibrivoxAuthorSplits) != 0 || len(sum.LibrivoxWorklist) != 0 {
			t.Errorf("%s: splits %+v, worklist %+v; want none", first, sum.LibrivoxAuthorSplits, sum.LibrivoxWorklist)
		}
		p := personOf(t, dataDir, "f-p-walter")
		if p.AuthorID != 6492 || p.Name != "F. P. Walter" || !reflect.DeepEqual(p.AuthorAliases, []int{6858}) || p.BirthYear != 1943 || p.DeathYear != 2024 {
			t.Errorf("%s: f-p-walter = %+v", first, p)
		}
		if entryExists(t, dataDir, personAddr("f-p-walter-librivox-author-6858")) {
			t.Errorf("%s: the twin was minted", first)
		}
		mustValidate(t, dataDir)
		rerunIsANoOp(t, snap, dataDir)
	}
}

func TestLibrivoxOneProjectCreditingBothAuthorIDs(t *testing.T) {
	// LibriVox credits one person twice on one project, under each id (Livingston
	// 1456 and 15071 on Sevenfold Trouble, spelled apart): one identity author, and
	// one translator credit on the recording.
	for _, second := range []string{"F. P.", "Frederick Paul"} {
		dataDir := t.TempDir()
		seedTree(t, dataDir, map[string]string{personAddr("f-p-walter"): personFiles(t, "f-p-walter", "F. P. Walter", map[string]any{
			"librivox_author_id": 6492, "librivox_author_aliases": []int{6858},
		})})
		pr := byAuthors(t, 99042, "Two Names One Hand", lvDated(6492, "F. P.", "Walter", "", ""), lvDated(6858, second, "Walter", "", ""))
		pr.Translators = []LibrivoxPerson{lvDated(6858, second, "Walter", "", ""), lvDated(6492, "F. P.", "Walter", "", "")}
		snap := writeSnapshot(t, pr)
		runLibrivox(t, snap, dataDir, allLibrivox)
		if got := workAuthorsOf(t, dataDir, "two-names-one-hand"); !reflect.DeepEqual(got, []string{"f-p-walter"}) {
			t.Errorf("%s: authors = %v, want one credit", second, got)
		}
		_, _, f := lvRecordingOf(t, dataDir, 99042)
		if !reflect.DeepEqual(f.Credits, []model.Credit{{Person: "f-p-walter", Role: model.RoleTranslator}}) {
			t.Errorf("%s: recording credits = %v, want one", second, f.Credits)
		}
		mustValidate(t, dataDir)
		rerunIsANoOp(t, snap, dataDir)
	}
}

func TestLibrivoxLifeYearsAreReadFromEveryIDOfAPerson(t *testing.T) {
	// Two ids need not state one thing, so the first row read must not keep the
	// other id's years out: the alias states a birth year alone, the own id both.
	dataDir := t.TempDir()
	seedTree(t, dataDir, map[string]string{personAddr("f-p-walter"): personFiles(t, "f-p-walter", "F. P. Walter", map[string]any{
		"librivox_author_id": 6492, "librivox_author_aliases": []int{6858},
	})})
	snap := writeSnapshot(t,
		byAuthors(t, 99040, "Notes on Verne", lvDated(6858, "F. P.", "Walter", "1943", "")),
		byAuthors(t, 99041, "More Notes on Verne", lvDated(6492, "F. P.", "Walter", "1943", "2024")))
	runLibrivox(t, snap, dataDir, allLibrivox)
	if p := personOf(t, dataDir, "f-p-walter"); p.BirthYear != 1943 || p.DeathYear != 2024 {
		t.Errorf("years = %d-%d, want 1943-2024", p.BirthYear, p.DeathYear)
	}
	mustValidate(t, dataDir)
	rerunIsANoOp(t, snap, dataDir)
}

func TestLibrivoxLifeYearsAnAliasContradictsAreKeptAndWarned(t *testing.T) {
	// Imbert de Saint-Amand: LibriVox's 1824 under the alias against the record's
	// 1834 is reported whichever id's row the run reads first, and written over
	// nothing.
	for name, pair := range map[string][2]LibrivoxPerson{
		"own id first": {lvDated(6041, "Imbert", "de Saint-Amand", "1834", "1900"), lvDated(17695, "Imbert", "de Saint-Amand", "1824", "1900")},
		"alias first":  {lvDated(17695, "Imbert", "de Saint-Amand", "1824", "1900"), lvDated(6041, "Imbert", "de Saint-Amand", "1834", "1900")},
	} {
		dataDir := t.TempDir()
		seedTree(t, dataDir, map[string]string{personAddr("imbert-de-saint-amand"): personFiles(t, "imbert-de-saint-amand", "Imbert de Saint-Amand", map[string]any{
			"birth_year": 1834, "death_year": 1900, "librivox_author_id": 6041, "librivox_author_aliases": []int{17695},
		})})
		snap := writeSnapshot(t, byAuthors(t, 99043, "Court Notes", pair[0]), byAuthors(t, 99044, "Further Court Notes", pair[1]))
		sum := runLibrivox(t, snap, dataDir, allLibrivox)
		if sum.LibrivoxRestated.LifeYearConflicts != 1 {
			t.Errorf("%s: conflicts = %d, want the alias's one", name, sum.LibrivoxRestated.LifeYearConflicts)
		}
		if p := personOf(t, dataDir, "imbert-de-saint-amand"); p.BirthYear != 1834 {
			t.Errorf("%s: birth year = %d, want the record's 1834", name, p.BirthYear)
		}
	}
}

func TestLibrivoxWithoutTheAliasTheTwinIsMinted(t *testing.T) {
	// The other half of the decision: with no alias recorded the two ids stay
	// two people and are listed, and no run writes the list.
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
	for _, slug := range []string{"f-p-walter", twin} {
		if got := personOf(t, dataDir, slug).AuthorAliases; got != nil {
			t.Errorf("a run wrote aliases %v on %s", got, slug)
		}
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
	rerunIsANoOp(t, snap, dataDir)
}

func TestLibrivoxVariantAttachUnderAnAlias(t *testing.T) {
	// The work credits the record under a fuller spelling of the name: the alias
	// pins the row's credit to the record, so no spelling is paired at all and the
	// row attaches.
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

func TestHeldByOtherAuthorIDReadsAnAliasAsHeld(t *testing.T) {
	// No row reaches this with an alias today (the alias pins the credit first, the
	// test above), so the rule is asked directly: an alias is not ANOTHER id.
	p := &planner{lv: newLibrivoxState()}
	person := &model.Person{ID: "f-p-walter", LibrivoxAuthorID: 6492, LibrivoxAuthorAliases: []int{6858}}
	p.loadLibrivoxPerson(person)
	for id, want := range map[int]bool{6492: false, 6858: false, 7000: true} {
		if got := p.heldByOtherAuthorID(person.ID, id); got != want {
			t.Errorf("heldByOtherAuthorID(%d) = %v, want %v", id, got, want)
		}
	}
}

func TestLibrivoxReaderAliasResolvesToTheRecord(t *testing.T) {
	// A reader id held as an alias reads under the record, under its own spelling
	// or another, never minted at -librivox-<id>; a recording read by both ids of
	// one person credits them once.
	aliased := personFiles(t, "branko-collin", "Branko Collin", map[string]any{"librivox_reader_id": 778, "librivox_reader_aliases": []int{1775}})
	for name, readers := range map[string][]LibrivoxReader{
		"under the record's spelling":   {{ReaderID: 1775, DisplayName: "Branko Collin"}},
		"under another spelling":        {{ReaderID: 1775, DisplayName: "Branko"}},
		"under both ids":                {{ReaderID: 778, DisplayName: "Branko Collin"}, {ReaderID: 1775, DisplayName: "Branko Collin"}},
		"under both ids, spelled apart": {{ReaderID: 778, DisplayName: "Branko Collin"}, {ReaderID: 1775, DisplayName: "Branko"}},
	} {
		dataDir := t.TempDir()
		seedTree(t, dataDir, map[string]string{personAddr("branko-collin"): aliased})
		pr := readProject(t, 99050, readers...)
		if len(pr.Sections) < len(readers) {
			t.Fatalf("the fixture has %d sections, %d readers", len(pr.Sections), len(readers))
		}
		snap := writeSnapshot(t, pr)
		sum := runLibrivox(t, snap, dataDir, allLibrivox)
		_, _, f := lvRecordingOf(t, dataDir, 99050)
		if !reflect.DeepEqual(f.Narrators, []string{"branko-collin"}) {
			t.Fatalf("%s: narrators = %v\nwarnings: %v", name, f.Narrators, sum.Warnings)
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
}

func TestLibrivoxRetiredIDFormResolvesThroughTheAlias(t *testing.T) {
	// A retired librivox-reader-<id> slot points at the record answering to the id
	// (pkg/check holds that), so the reader lands there rather than being refused
	// for a slot they cannot take.
	dataDir := seedTombstoneTree(t, map[string]string{
		personAddr("ada-mapmaker"): personFiles(t, "ada-mapmaker", "Ada Mapmaker", map[string]any{"librivox_reader_id": 5, "librivox_reader_aliases": []int{11294}}),
	}, model.Redirects{model.RedirectPeople: {"librivox-reader-11294": "ada-mapmaker"}})
	snap := writeSnapshot(t, greekRow(t, 91001, 11294, "Ἑλένη Κεμικτσή"))
	sum := runLibrivox(t, snap, dataDir, allLibrivox)
	if len(sum.Skips) != 0 {
		t.Fatalf("skips = %+v\nwarnings: %v", sum.Skips, sum.Warnings)
	}
	_, _, f := lvRecordingOf(t, dataDir, 91001)
	if !reflect.DeepEqual(f.Narrators, []string{"ada-mapmaker"}) {
		t.Errorf("narrators = %v", f.Narrators)
	}
	mustValidate(t, dataDir)
	rerunIsANoOp(t, snap, dataDir)
}

func TestLibrivoxRetiredAuthorIDFormResolvesThroughTheAlias(t *testing.T) {
	dataDir := seedTombstoneTree(t, map[string]string{
		personAddr("ada-mapmaker"): personFiles(t, "ada-mapmaker", "Ada Mapmaker", map[string]any{"librivox_author_id": 5, "librivox_author_aliases": []int{17538}}),
	}, model.Redirects{model.RedirectPeople: {"librivox-author-17538": "ada-mapmaker"}})
	snap := writeSnapshot(t, byAuthors(t, 91002, "Stikhi", LibrivoxPerson{ID: 17538, FirstName: "Зинаида", LastName: "Гиппиус"}))
	sum := runLibrivox(t, snap, dataDir, allLibrivox)
	if got := workAuthorsOf(t, dataDir, "stikhi"); !reflect.DeepEqual(got, []string{"ada-mapmaker"}) {
		t.Errorf("authors = %v\nwarnings: %v", got, sum.Warnings)
	}
	mustValidate(t, dataDir)
	rerunIsANoOp(t, snap, dataDir)
}

func TestLibrivoxHeldRowKeepsAReaderCreditedUnderAnAlias(t *testing.T) {
	// A held project read by the person AND by a third reader id of the same name:
	// the third is split off, and the narrator credit stays (rowCreditsHolder,
	// answersTo), whichever of the person's ids the row names and however it spells
	// them. Comparing the record's own id, or asking the name as well, dropped the
	// credit, and no rerun restored it.
	for name, holder := range map[string]LibrivoxReader{
		"an alias":                  {ReaderID: 1775, DisplayName: "Branko Collin"},
		"an alias, spelled apart":   {ReaderID: 1775, DisplayName: "Branko"},
		"the own id, spelled apart": {ReaderID: 778, DisplayName: "Branko"},
	} {
		t.Run(name, func(t *testing.T) {
			dataDir := t.TempDir()
			first := readProject(t, 99060, LibrivoxReader{ReaderID: 778, DisplayName: "Branko Collin"})
			runLibrivox(t, writeSnapshot(t, first), dataDir, allLibrivox)
			seedTree(t, dataDir, map[string]string{personAddr("branko-collin"): personFiles(t, "branko-collin", "Branko Collin", map[string]any{"librivox_reader_id": 778, "librivox_reader_aliases": []int{1775}})})
			held := readProject(t, 99060, holder, LibrivoxReader{ReaderID: 9999, DisplayName: "Branko Collin"})
			snap := writeSnapshot(t, held)
			sum := runLibrivox(t, snap, dataDir, allLibrivox)
			if sum.LibrivoxReadersMoved != 1 {
				t.Errorf("moved = %d, want the third reader", sum.LibrivoxReadersMoved)
			}
			_, _, f := lvRecordingOf(t, dataDir, 99060)
			if want := []string{"branko-collin", "branko-collin-librivox-9999"}; !reflect.DeepEqual(f.Narrators, want) {
				t.Errorf("narrators = %v, want %v", f.Narrators, want)
			}
			mustValidate(t, dataDir)
			rerunIsANoOp(t, snap, dataDir)
		})
	}
}
