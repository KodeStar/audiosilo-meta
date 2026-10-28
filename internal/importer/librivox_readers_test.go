package importer

import (
	"reflect"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/testpack"
	"github.com/kodestar/audiosilo-meta/pkg/check"
)

// readProject is fixture project 527 (Metamorphosis, one reader over every
// section) re-numbered as project id and read by readers, one per section in
// turn, so several projects of one text land as recordings of one work.
func readProject(t *testing.T, id int, readers ...LibrivoxReader) LibrivoxProject {
	t.Helper()
	pr := fixtureProject(t, 527)
	pr.ID = lvInt(id)
	pr.Sections = append([]LibrivoxSection(nil), pr.Sections...)
	for i := range pr.Sections {
		pr.Sections[i].Readers = []LibrivoxReader{readers[i%len(readers)]}
	}
	return pr
}

// martinaRow is project id read by the reader "Martina" with reader id reader.
func martinaRow(t *testing.T, id, reader int) LibrivoxProject {
	return readProject(t, id, LibrivoxReader{ReaderID: lvInt(reader), DisplayName: "Martina"})
}

// lvNarrators reads one recording's narrators.
func lvNarrators(t *testing.T, dataDir, work, rec string) []string {
	t.Helper()
	var r lvRecording
	readEntity(t, dataDir, recAddr(work, rec), &r)
	return r.Narrators
}

// readerIDOf reads one person's librivox_reader_id.
func readerIDOf(t *testing.T, dataDir, slug string) int {
	t.Helper()
	var p lvPerson
	readEntity(t, dataDir, personAddr(slug), &p)
	return p.ReaderID
}

func TestLibrivoxThreeSameNameReadersAreThreePeople(t *testing.T) {
	// LibriVox's real tranche-1 shape: three accounts called "Martina". The
	// first (in row order) keeps the plain slug, the other two are people of
	// their own, and every recording credits its own reader.
	snap := writeSnapshot(t, martinaRow(t, 91001, 844), martinaRow(t, 91002, 634), martinaRow(t, 91003, 2089))
	dataDir := t.TempDir()
	sum := runLibrivox(t, snap, dataDir, allLibrivox)
	if sum.NewRecordings != 3 || sum.LibrivoxReadersSplit != 2 {
		t.Fatalf("recordings/split = %d/%d, want 3/2\nwarnings: %v", sum.NewRecordings, sum.LibrivoxReadersSplit, sum.Warnings)
	}
	for slug, want := range map[string]int{"martina": 844, "martina-librivox-634": 634, "martina-librivox-2089": 2089} {
		if got := readerIDOf(t, dataDir, slug); got != want {
			t.Errorf("%s carries reader %d, want %d", slug, got, want)
		}
	}
	for rec, want := range map[string]string{"martina": "martina", "martina-librivox-634": "martina-librivox-634", "martina-librivox-2089": "martina-librivox-2089"} {
		if got := lvNarrators(t, dataDir, "metamorphosis", rec); !reflect.DeepEqual(got, []string{want}) {
			t.Errorf("recording %s narrators = %v, want [%s]", rec, got, want)
		}
	}
	if strings.Contains(strings.Join(sum.Warnings, "\n"), "librivox_reader_id") {
		t.Errorf("no reader id may be dropped: %v", sum.Warnings)
	}
	if res := check.Load(dataDir); !res.OK() {
		t.Fatalf("tree failed validation:\n%v", res.Problems)
	}

	// A rerun resolves every reader by id: nothing minted, nothing rewritten.
	before := testpack.Snapshot(t, dataDir)
	again := runLibrivox(t, snap, dataDir, allLibrivox)
	if again.NewPeople+again.NewRecordings+again.LibrivoxReadersSplit+again.LibrivoxReadersMoved+again.LibrivoxIDsStamped != 0 || len(again.Files) != 0 {
		t.Errorf("rerun = %+v, want a no-op", again)
	}
	if after := testpack.Snapshot(t, dataDir); !reflect.DeepEqual(before, after) {
		t.Error("rerun changed the tree")
	}
}

func TestLibrivoxSplitReaderIsFoundByIDInALaterRun(t *testing.T) {
	// A later tranche meeting reader 634 again lands on martina-librivox-634,
	// never on the plain holder and never on a second minted record.
	dataDir := t.TempDir()
	runLibrivox(t, writeSnapshot(t, martinaRow(t, 91001, 844), martinaRow(t, 91002, 634)), dataDir, allLibrivox)
	again := readProject(t, 91004, LibrivoxReader{ReaderID: 634, DisplayName: "Martina"})
	again.Title = "Metamorphosis (version 3)"
	sum := runLibrivox(t, writeSnapshot(t, again), dataDir, allLibrivox)
	if sum.NewPeople != 0 || sum.NewRecordings != 1 || sum.LibrivoxReadersSplit != 0 {
		t.Fatalf("people/recordings/split = %d/%d/%d, want 0/1/0\nwarnings: %v", sum.NewPeople, sum.NewRecordings, sum.LibrivoxReadersSplit, sum.Warnings)
	}
	if got := lvNarrators(t, dataDir, "metamorphosis", "martina-librivox-634-2"); !reflect.DeepEqual(got, []string{"martina-librivox-634"}) {
		t.Errorf("narrators = %v", got)
	}
	if res := check.Load(dataDir); !res.OK() {
		t.Fatalf("tree failed validation:\n%v", res.Problems)
	}
}

func TestLibrivoxTwoSameNameReadersOnOneProject(t *testing.T) {
	// Two "Martina" accounts reading one project: two narrators, not one.
	pr := readProject(t, 91001, LibrivoxReader{ReaderID: 844, DisplayName: "Martina"}, LibrivoxReader{ReaderID: 634, DisplayName: "Martina"})
	dataDir := t.TempDir()
	runLibrivox(t, writeSnapshot(t, pr), dataDir, allLibrivox)
	if got := lvNarrators(t, dataDir, "metamorphosis", "martina"); !reflect.DeepEqual(got, []string{"martina", "martina-librivox-634"}) {
		t.Errorf("narrators = %v", got)
	}
	if res := check.Load(dataDir); !res.OK() {
		t.Fatalf("tree failed validation:\n%v", res.Problems)
	}
}

func TestLibrivoxReaderWithoutAnIDIsNotSplit(t *testing.T) {
	// A person at the plain slug who carries no reader id (a narrator another
	// source catalogued) is not "another reader": the first LibriVox reader of
	// that name is stamped onto them, as before.
	dataDir := t.TempDir()
	seedTree(t, dataDir, map[string]string{personAddr("martina"): testpack.PersonJSON(t, "martina", "Martina")})
	sum := runLibrivox(t, writeSnapshot(t, martinaRow(t, 91001, 844)), dataDir, allLibrivox)
	if sum.LibrivoxReadersSplit != 0 || readerIDOf(t, dataDir, "martina") != 844 {
		t.Errorf("split %d, martina carries %d; want 0 and 844", sum.LibrivoxReadersSplit, readerIDOf(t, dataDir, "martina"))
	}
}

func TestLibrivoxSplitNeverWritesOverAnotherPerson(t *testing.T) {
	// The disambiguated id is somebody else's (a person whose own name slugs
	// to it): the run warns and credits the name as it always did, never
	// writing over that record.
	dataDir := t.TempDir()
	seedTree(t, dataDir, map[string]string{
		personAddr("martina"):              testpack.WithField(t, testpack.PersonJSON(t, "martina", "Martina"), "librivox_reader_id", 844),
		personAddr("martina-librivox-634"): testpack.PersonJSON(t, "martina-librivox-634", "Martina Librivox 634"),
	})
	sum := runLibrivox(t, writeSnapshot(t, martinaRow(t, 91002, 634)), dataDir, allLibrivox)
	if !strings.Contains(strings.Join(sum.Warnings, "\n"), `"martina-librivox-634" is another person's id`) {
		t.Errorf("no occupant warning: %v", sum.Warnings)
	}
	var p lvPerson
	readEntity(t, dataDir, personAddr("martina-librivox-634"), &p)
	if p.Name != "Martina Librivox 634" || p.ReaderID != 0 {
		t.Errorf("the occupant was written over: %+v", p)
	}
	if got := lvNarrators(t, dataDir, "metamorphosis", "martina"); !reflect.DeepEqual(got, []string{"martina"}) {
		t.Errorf("narrators = %v", got)
	}
	// The name's person keeps the id it carries, and the run says the reader's
	// id was not recorded (the one way a reader id is still dropped).
	if !strings.Contains(strings.Join(sum.Warnings, "\n"), "already carries librivox_reader_id 844") {
		t.Errorf("no warning about the kept id: %v", sum.Warnings)
	}
	if res := check.Load(dataDir); !res.OK() {
		t.Fatalf("tree failed validation:\n%v", res.Problems)
	}
}

// seedFoldedMartina is the tree a pre-fix import left: reader 844 holds
// `martina`, and project 91002 - read by reader 634, also "Martina" - was
// folded onto that record, its id dropped. narrators is the folded recording's
// narrator list.
func seedFoldedMartina(t *testing.T, dataDir string, narrators ...string) {
	t.Helper()
	seedTree(t, dataDir, map[string]string{
		personAddr("martina"):                 testpack.WithField(t, testpack.PersonJSON(t, "martina", "Martina"), "librivox_reader_id", 844),
		personAddr("franz-kafka"):             testpack.PersonJSON(t, "franz-kafka", "Franz Kafka"),
		workAddr("metamorphosis"):             testpack.WorkJSON(t, "metamorphosis", "Metamorphosis", testpack.WithAuthors("franz-kafka")),
		recAddr("metamorphosis", "martina"):   testpack.WithField(t, testpack.RecJSON(t, "martina", "metamorphosis", testpack.WithNarrators("martina")), "librivox_id", 91001),
		recAddr("metamorphosis", "martina-2"): testpack.WithField(t, testpack.RecJSON(t, "martina-2", "metamorphosis", testpack.WithNarrators(narrators...)), "librivox_id", 91002),
	})
}

func TestLibrivoxRerunSplitsAFoldedReader(t *testing.T) {
	// Re-importing the ids of a pre-fix tranche puts the fold right: the held
	// recording's credit moves onto reader 634's own person, reader 844's
	// recording is untouched, and a second rerun changes nothing.
	dataDir := t.TempDir()
	seedFoldedMartina(t, dataDir, "martina")
	snap := writeSnapshot(t, martinaRow(t, 91001, 844), martinaRow(t, 91002, 634))
	sum := runLibrivox(t, snap, dataDir, allLibrivox)
	if sum.NewRecordings != 0 || sum.Skipped != 2 || sum.LibrivoxReadersSplit != 1 || sum.LibrivoxReadersMoved != 1 {
		t.Fatalf("recordings/skipped/split/moved = %d/%d/%d/%d, want 0/2/1/1\nwarnings: %v",
			sum.NewRecordings, sum.Skipped, sum.LibrivoxReadersSplit, sum.LibrivoxReadersMoved, sum.Warnings)
	}
	if got := lvNarrators(t, dataDir, "metamorphosis", "martina-2"); !reflect.DeepEqual(got, []string{"martina-librivox-634"}) {
		t.Errorf("folded recording narrators = %v", got)
	}
	if got := lvNarrators(t, dataDir, "metamorphosis", "martina"); !reflect.DeepEqual(got, []string{"martina"}) {
		t.Errorf("844's recording narrators = %v", got)
	}
	if got := readerIDOf(t, dataDir, "martina-librivox-634"); got != 634 {
		t.Errorf("split reader carries %d, want 634", got)
	}
	if res := check.Load(dataDir); !res.OK() {
		t.Fatalf("tree failed validation:\n%v", res.Problems)
	}

	before := testpack.Snapshot(t, dataDir)
	again := runLibrivox(t, snap, dataDir, allLibrivox)
	if again.NewPeople+again.LibrivoxReadersSplit+again.LibrivoxReadersMoved+again.LibrivoxIDsStamped != 0 || len(again.Files) != 0 {
		t.Errorf("second rerun = %+v, want a no-op", again)
	}
	if after := testpack.Snapshot(t, dataDir); !reflect.DeepEqual(before, after) {
		t.Error("second rerun changed the tree")
	}
}

func TestLibrivoxRerunKeepsTheHoldersOwnCredit(t *testing.T) {
	// The folded project was read by BOTH Martinas: the holder's credit stays
	// and reader 634 is added beside it.
	dataDir := t.TempDir()
	seedFoldedMartina(t, dataDir, "martina")
	both := readProject(t, 91002, LibrivoxReader{ReaderID: 844, DisplayName: "Martina"}, LibrivoxReader{ReaderID: 634, DisplayName: "Martina"})
	snap := writeSnapshot(t, both)
	if sum := runLibrivox(t, snap, dataDir, allLibrivox); sum.LibrivoxReadersMoved != 1 {
		t.Errorf("moved %d, want 1", sum.LibrivoxReadersMoved)
	}
	if got := lvNarrators(t, dataDir, "metamorphosis", "martina-2"); !reflect.DeepEqual(got, []string{"martina", "martina-librivox-634"}) {
		t.Errorf("narrators = %v", got)
	}
	if res := check.Load(dataDir); !res.OK() {
		t.Fatalf("tree failed validation:\n%v", res.Problems)
	}

	// Both readers are credited now, so a second rerun moves nothing.
	before := testpack.Snapshot(t, dataDir)
	if again := runLibrivox(t, snap, dataDir, allLibrivox); again.LibrivoxReadersMoved != 0 || len(again.Files) != 0 {
		t.Errorf("second rerun moved %d, wrote %v; want a no-op", again.LibrivoxReadersMoved, again.Files)
	}
	if after := testpack.Snapshot(t, dataDir); !reflect.DeepEqual(before, after) {
		t.Error("second rerun changed the tree")
	}
}

func TestLibrivoxRerunLeavesAnUnkeyedNarrator(t *testing.T) {
	// A held recording crediting a person who carries no reader id is not a
	// fold this can prove: nothing moves.
	dataDir := t.TempDir()
	seedTree(t, dataDir, map[string]string{
		personAddr("martina"):               testpack.PersonJSON(t, "martina", "Martina"),
		personAddr("franz-kafka"):           testpack.PersonJSON(t, "franz-kafka", "Franz Kafka"),
		workAddr("metamorphosis"):           testpack.WorkJSON(t, "metamorphosis", "Metamorphosis", testpack.WithAuthors("franz-kafka")),
		recAddr("metamorphosis", "martina"): testpack.WithField(t, testpack.RecJSON(t, "martina", "metamorphosis", testpack.WithNarrators("martina")), "librivox_id", 91002),
	})
	before := testpack.Snapshot(t, dataDir)
	sum := runLibrivox(t, writeSnapshot(t, martinaRow(t, 91002, 634)), dataDir, allLibrivox)
	if sum.LibrivoxReadersMoved != 0 || len(sum.Files) != 0 {
		t.Errorf("moved %d, wrote %v; want nothing", sum.LibrivoxReadersMoved, sum.Files)
	}
	if after := testpack.Snapshot(t, dataDir); !reflect.DeepEqual(before, after) {
		t.Error("the tree changed")
	}
}

func TestLibrivoxRerunSplitsTwoReadersFoldedOntoOne(t *testing.T) {
	// The folded project was read by two Martinas, NEITHER of them the holder:
	// both are moved off the holder, in row order, and neither is lost.
	dataDir := t.TempDir()
	seedFoldedMartina(t, dataDir, "martina")
	pr := readProject(t, 91002, LibrivoxReader{ReaderID: 634, DisplayName: "Martina"}, LibrivoxReader{ReaderID: 2089, DisplayName: "Martina"})
	snap := writeSnapshot(t, pr)
	sum := runLibrivox(t, snap, dataDir, allLibrivox)
	if sum.LibrivoxReadersSplit != 2 || sum.LibrivoxReadersMoved != 2 {
		t.Errorf("split/moved = %d/%d, want 2/2\nwarnings: %v", sum.LibrivoxReadersSplit, sum.LibrivoxReadersMoved, sum.Warnings)
	}
	if got := lvNarrators(t, dataDir, "metamorphosis", "martina-2"); !reflect.DeepEqual(got, []string{"martina-librivox-634", "martina-librivox-2089"}) {
		t.Errorf("narrators = %v", got)
	}
	if res := check.Load(dataDir); !res.OK() {
		t.Fatalf("tree failed validation:\n%v", res.Problems)
	}

	before := testpack.Snapshot(t, dataDir)
	if again := runLibrivox(t, snap, dataDir, allLibrivox); again.LibrivoxReadersMoved+again.LibrivoxReadersSplit != 0 || len(again.Files) != 0 {
		t.Errorf("second rerun = %+v, want a no-op", again)
	}
	if after := testpack.Snapshot(t, dataDir); !reflect.DeepEqual(before, after) {
		t.Error("second rerun changed the tree")
	}
}

func TestLibrivoxRerunLeavesARecordingAlreadyCreditingTheReader(t *testing.T) {
	// The held recording already credits reader 634's own person beside the
	// holder, though the row no longer states the holder: that is not a fold, so
	// the holder's credit is not removed.
	dataDir := t.TempDir()
	seedTree(t, dataDir, map[string]string{
		personAddr("martina"):               testpack.WithField(t, testpack.PersonJSON(t, "martina", "Martina"), "librivox_reader_id", 844),
		personAddr("martina-librivox-634"):  testpack.WithField(t, testpack.PersonJSON(t, "martina-librivox-634", "Martina"), "librivox_reader_id", 634),
		personAddr("franz-kafka"):           testpack.PersonJSON(t, "franz-kafka", "Franz Kafka"),
		workAddr("metamorphosis"):           testpack.WorkJSON(t, "metamorphosis", "Metamorphosis", testpack.WithAuthors("franz-kafka")),
		recAddr("metamorphosis", "martina"): testpack.WithField(t, testpack.RecJSON(t, "martina", "metamorphosis", testpack.WithNarrators("martina", "martina-librivox-634")), "librivox_id", 91002),
	})
	before := testpack.Snapshot(t, dataDir)
	sum := runLibrivox(t, writeSnapshot(t, martinaRow(t, 91002, 634)), dataDir, allLibrivox)
	if sum.LibrivoxReadersMoved != 0 || len(sum.Files) != 0 {
		t.Errorf("moved %d, wrote %v; want nothing", sum.LibrivoxReadersMoved, sum.Files)
	}
	if after := testpack.Snapshot(t, dataDir); !reflect.DeepEqual(before, after) {
		t.Error("the tree changed")
	}
}

func TestLibrivoxSameNameReadersOfACollectiveAreNotSplit(t *testing.T) {
	// A collective statement is no one's name: a second reader id spelled
	// "Various" is not minted a "various-librivox-<id>" person of its own.
	pr1 := readProject(t, 91001, LibrivoxReader{ReaderID: 11, DisplayName: "Various"})
	pr2 := readProject(t, 91002, LibrivoxReader{ReaderID: 12, DisplayName: "Various"})
	pr2.Title = "Metamorphosis (version 2)"
	dataDir := t.TempDir()
	sum := runLibrivox(t, writeSnapshot(t, pr1, pr2), dataDir, allLibrivox)
	if sum.LibrivoxReadersSplit != 0 {
		t.Errorf("split %d readers off a collective, want 0", sum.LibrivoxReadersSplit)
	}
	if res := check.Load(dataDir); !res.OK() {
		t.Fatalf("tree failed validation:\n%v", res.Problems)
	}
}

func TestLibrivoxHeldReaderKeepsTheirNameAgainstANewRow(t *testing.T) {
	// A held project's reader credited to a person carrying no reader id holds
	// that name for this run: a NEW row's other reader of the name is a person of
	// their own at once, so the first rerun is final and the second a no-op
	// (rather than the new reader taking the name and the next rerun splitting
	// the held one off it).
	dataDir := t.TempDir()
	seedTree(t, dataDir, map[string]string{
		personAddr("martina"):               testpack.PersonJSON(t, "martina", "Martina"),
		personAddr("franz-kafka"):           testpack.PersonJSON(t, "franz-kafka", "Franz Kafka"),
		workAddr("metamorphosis"):           testpack.WorkJSON(t, "metamorphosis", "Metamorphosis", testpack.WithAuthors("franz-kafka")),
		recAddr("metamorphosis", "martina"): testpack.WithField(t, testpack.RecJSON(t, "martina", "metamorphosis", testpack.WithNarrators("martina")), "librivox_id", 91001),
	})
	later := martinaRow(t, 91002, 634)
	later.Title = "Metamorphosis (version 2)"
	snap := writeSnapshot(t, martinaRow(t, 91001, 844), later)
	sum := runLibrivox(t, snap, dataDir, allLibrivox)
	if sum.LibrivoxReadersSplit != 1 || readerIDOf(t, dataDir, "martina-librivox-634") != 634 || readerIDOf(t, dataDir, "martina") != 0 {
		t.Errorf("split %d; martina carries %d; want reader 634 split off and martina left unkeyed\nwarnings: %v",
			sum.LibrivoxReadersSplit, readerIDOf(t, dataDir, "martina"), sum.Warnings)
	}
	before := testpack.Snapshot(t, dataDir)
	if again := runLibrivox(t, snap, dataDir, allLibrivox); again.LibrivoxReadersMoved+again.LibrivoxReadersSplit+again.NewPeople+again.LibrivoxIDsStamped != 0 || len(again.Files) != 0 {
		t.Errorf("second rerun = %+v, want a no-op", again)
	}
	if after := testpack.Snapshot(t, dataDir); !reflect.DeepEqual(before, after) {
		t.Error("second rerun changed the tree")
	}
}
