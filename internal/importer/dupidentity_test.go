package importer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// dupidentity_test.go pins the CREATE path's duplicate-identity guard on the shape
// the data-quality audit measured most of its 4,596 near-duplicate clusters in: a
// bulk row whose title carries retailer decoration, naming a book the catalogue
// already holds under the plain spelling.
//
// Every test comes in a pair - the row that must be refused, and the neighbouring
// row that must NOT be, since the whole risk of a guard like this is refusing a book
// we do not have.

// seedPlainWork imports one undecorated row, so the catalogue holds "Hammered" by
// Kevin Hearne as volume 3 of its series.
func seedPlainWork(t *testing.T) string {
	t.Helper()
	dataDir := t.TempDir()
	sum := runLibexInto(t, dataDir, rows(
		libexRow{asin: "B0PLAIN001", title: "Hammered", authors: `{"name":"Kevin Hearne"}`,
			narrators: `{"name":"Luke Daniels"}`, minutes: 480,
			series: `{"name":"The Iron Druid Chronicles","position":"3"}`},
	))
	if sum.NewWorks != 1 {
		t.Fatalf("seed run: NewWorks = %d, want 1", sum.NewWorks)
	}
	if !entryExists(t, dataDir, workAddr("hammered")) {
		t.Fatal("seed run left no work at hammered")
	}
	return dataDir
}

// The refusal: a second listing of the same book, whose title spells out the series
// and the volume, mints NOTHING - not the work, not its people - and is counted and
// named instead.
func TestCreateRefusesADecoratedDuplicateWork(t *testing.T) {
	dataDir := seedPlainWork(t)

	sum := runLibexInto(t, dataDir, rows(
		libexRow{asin: "B0DECOR001", title: "Hammered: The Iron Druid Chronicles, Book 3",
			authors: `{"name":"Kevin Hearne"}`, narrators: `{"name":"Christopher Ragland"}`, minutes: 470,
			series: `{"name":"The Iron Druid Chronicles","position":"3"}`},
	))

	if sum.NewWorks != 0 {
		t.Errorf("NewWorks = %d, want 0: the book is already in the catalogue", sum.NewWorks)
	}
	if sum.SkippedDuplicateIdentity != 1 {
		t.Errorf("SkippedDuplicateIdentity = %d, want 1", sum.SkippedDuplicateIdentity)
	}
	if sum.NewRecordings != 0 {
		t.Errorf("NewRecordings = %d, want 0: a refused row writes nothing", sum.NewRecordings)
	}
	// No orphan person record either: the guard runs before the row's people are
	// resolved, which is why it sits where it does in addBook.
	if entryExists(t, dataDir, personAddr("christopher-ragland")) {
		t.Error("a refused row left a person record behind")
	}
	if entryExists(t, dataDir, workAddr("hammered-the-iron-druid-chronicles-book-3")) {
		t.Error("the decorated row minted a second work")
	}
	if !hasWarning(sum.Warnings, "differently-spelled title") {
		t.Errorf("the run must report the refusals in one aggregated warning: %v", sum.Warnings)
	}
	if !hasWarning(sum.Warnings, "hammered") {
		t.Errorf("the warning must name an example: %v", sum.Warnings)
	}
}

// The row it must not refuse: a DIFFERENT volume of the same series, whose title
// normalizes to the same residual only because the volume marker comes off. The
// stated volume numbers disagree, so these are siblings, not duplicates.
func TestCreateAcceptsAnotherVolumeOfTheSameSeries(t *testing.T) {
	dataDir := seedPlainWork(t)

	sum := runLibexInto(t, dataDir, rows(
		libexRow{asin: "B0VOLUME04", title: "Hammered: The Iron Druid Chronicles, Book 4",
			authors: `{"name":"Kevin Hearne"}`, narrators: `{"name":"Luke Daniels"}`, minutes: 500,
			series: `{"name":"The Iron Druid Chronicles","position":"4"}`},
	))

	if sum.SkippedDuplicateIdentity != 0 {
		t.Errorf("SkippedDuplicateIdentity = %d, want 0: volume 4 is a different book", sum.SkippedDuplicateIdentity)
	}
	if sum.NewWorks != 1 {
		t.Errorf("NewWorks = %d, want 1; warnings = %v", sum.NewWorks, sum.Warnings)
	}
}

// Nor may it refuse a different AUTHOR's book of the same name: the author-nesting
// rule is what separates two books that share a title.
func TestCreateAcceptsTheSameTitleByAnotherAuthor(t *testing.T) {
	dataDir := seedPlainWork(t)

	sum := runLibexInto(t, dataDir, rows(
		libexRow{asin: "B0OTHERAU1", title: "Hammered (Unabridged)",
			authors: `{"name":"Elizabeth Bear"}`, narrators: `{"name":"Ann Reader"}`, minutes: 600},
	))

	if sum.SkippedDuplicateIdentity != 0 {
		t.Errorf("SkippedDuplicateIdentity = %d, want 0: another author's book is another work", sum.SkippedDuplicateIdentity)
	}
	if sum.NewWorks != 1 {
		t.Errorf("NewWorks = %d, want 1; warnings = %v", sum.NewWorks, sum.Warnings)
	}
}

// The guard sees the run's OWN output, not only the tree: two decorated spellings of
// one title inside ONE batch must not both create, which is the shape a chunked wave
// produces constantly.
func TestCreateRefusesADuplicateWithinOneBatch(t *testing.T) {
	dataDir := t.TempDir()

	sum := runLibexInto(t, dataDir, rows(
		libexRow{asin: "B0BATCH001", title: "Two Ravens", authors: `{"name":"Kevin Hearne"}`,
			narrators: `{"name":"Luke Daniels"}`, minutes: 300},
		libexRow{asin: "B0BATCH002", title: "Two Ravens (Unabridged)", authors: `{"name":"Kevin Hearne"}`,
			narrators: `{"name":"Christopher Ragland"}`, minutes: 305},
	))

	// The edition marker is stripped for identity anyway (cleanWorkTitle), so this
	// pair meets on the slug chain and MERGES - the run creates one work with two
	// recordings and refuses nothing. It is here as the boundary of the batch case:
	// what the guard must add is the pair the slug chain cannot see, below.
	if sum.NewWorks != 1 {
		t.Errorf("NewWorks = %d, want 1; warnings = %v", sum.NewWorks, sum.Warnings)
	}

	sum = runLibexInto(t, dataDir, rows(
		libexRow{asin: "B0BATCH011", title: "Broken Pride", authors: `{"name":"Erin Hunter"}`,
			narrators: `{"name":"Luke Daniels"}`, minutes: 300},
		libexRow{asin: "B0BATCH012", title: "Broken Pride: A Dark Fantasy Adventure",
			authors: `{"name":"Erin Hunter"}`, narrators: `{"name":"Ann Reader"}`, minutes: 310},
	))
	if sum.NewWorks != 1 {
		t.Errorf("NewWorks = %d, want 1: the second spelling must not mint a work; warnings = %v",
			sum.NewWorks, sum.Warnings)
	}
	if sum.SkippedDuplicateIdentity != 1 {
		t.Errorf("SkippedDuplicateIdentity = %d, want 1", sum.SkippedDuplicateIdentity)
	}
	if entryExists(t, dataDir, workAddr("broken-pride-a-dark-fantasy-adventure")) {
		t.Error("the genre-subtitled spelling minted its own work")
	}
}

// The refusal is TRIAGEABLE: with --conflicts the run appends one NDJSON row per
// refused duplicate, naming the work whose identity was already recorded and both
// titles, in the same worklist the contradiction guards write to.
func TestRefusedDuplicateWritesAConflictRow(t *testing.T) {
	dataDir := seedPlainWork(t)

	path := filepath.Join(t.TempDir(), "conflicts.ndjson")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	sum, err := RunLibex(writeBooks(t, rows(
		libexRow{asin: "B0DECOR002", title: "Hammered: The Iron Druid Chronicles, Book 3",
			authors: `{"name":"Kevin Hearne"}`, narrators: `{"name":"Christopher Ragland"}`, minutes: 470,
			series: `{"name":"The Iron Druid Chronicles","position":"3"}`},
	)), Options{DataDir: dataDir, ImportDate: testImportDate, Conflicts: f})
	if err != nil {
		t.Fatalf("libex run: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if sum.SkippedDuplicateIdentity != 1 {
		t.Fatalf("SkippedDuplicateIdentity = %d, want 1", sum.SkippedDuplicateIdentity)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 1 {
		t.Fatalf("worklist holds %d rows, want 1:\n%s", len(lines), raw)
	}
	var got Conflict
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatalf("worklist row is not a Conflict: %v", err)
	}
	want := Conflict{
		Run: "create", ASIN: "B0DECOR002", Work: "hammered", Recording: "",
		Field: "work_identity", Recorded: "Hammered",
		Stated:     "Hammered: The Iron Druid Chronicles, Book 3",
		SourceType: "libex-import", DetectedAt: testImportDate,
	}
	if got != want {
		t.Errorf("conflict row =\n%+v\nwant\n%+v", got, want)
	}
}

// F1: a title whose residual names no book is no identity. "Cars 2" against the
// series "Cars" reduces to the bare "2", and so does every other sequel of a
// one-word series - 199 such keys covered 3,287 works in the tree. The guard must
// never refuse on one.
func TestCreateAcceptsSequelsOfOneWordSeries(t *testing.T) {
	dataDir := t.TempDir()
	sum := runLibexInto(t, dataDir, rows(
		libexRow{asin: "B0CARS0002", title: "Cars 2", authors: `{"name":"Ada One"}`,
			narrators: `{"name":"Ann Reader"}`, series: `{"name":"Cars","position":"2"}`},
	))
	if sum.NewWorks != 1 {
		t.Fatalf("seed run: NewWorks = %d, want 1", sum.NewWorks)
	}

	sum = runLibexInto(t, dataDir, rows(
		libexRow{asin: "B0HAWK0002", title: "Hawk 2", authors: `{"name":"Ada One"}`,
			narrators: `{"name":"Ann Reader"}`, series: `{"name":"Hawk","position":"2"}`},
	))
	if sum.SkippedDuplicateIdentity != 0 {
		t.Errorf("SkippedDuplicateIdentity = %d, want 0: two unrelated sequels are two books", sum.SkippedDuplicateIdentity)
	}
	if sum.NewWorks != 1 {
		t.Errorf("NewWorks = %d, want 1; warnings = %v", sum.NewWorks, sum.Warnings)
	}
}

// F2: a row whose TITLE states a volume is only a duplicate of a work the catalogue
// PLACES at that volume. Silence is a veto, in all three ways the catalogue can be
// silent - each one was a book we do not hold being refused.
func TestCreateAcceptsAStatedVolumeNothingPlaces(t *testing.T) {
	// (a) the series is not in the tree at all: the seed row states none, so nothing
	// records where either book sits.
	dataDir := t.TempDir()
	if sum := runLibexInto(t, dataDir, rows(
		libexRow{asin: "B0CIRCUS01", title: "Circus of the Dead", authors: `{"name":"Ada One"}`,
			narrators: `{"name":"Ann Reader"}`},
	)); sum.NewWorks != 1 {
		t.Fatalf("seed run: NewWorks = %d, want 1", sum.NewWorks)
	}
	sum := runLibexInto(t, dataDir, rows(
		libexRow{asin: "B0CIRCUS02", title: "Circus of the Dead, Book 2", authors: `{"name":"Ada One"}`,
			narrators: `{"name":"Bob Reader"}`},
	))
	if sum.SkippedDuplicateIdentity != 0 || sum.NewWorks != 1 {
		t.Errorf("no series in the tree: SkippedDuplicateIdentity = %d, NewWorks = %d, want 0 and 1; warnings = %v",
			sum.SkippedDuplicateIdentity, sum.NewWorks, sum.Warnings)
	}

	// (b) the series exists and the matched work has NO membership in it (its
	// placement was dropped, or it was never claimed).
	dataDir = t.TempDir()
	if sum := runLibexInto(t, dataDir, rows(
		libexRow{asin: "B0CIRCUS11", title: "Circus of the Dead", authors: `{"name":"Ada One"}`,
			narrators: `{"name":"Ann Reader"}`},
		libexRow{asin: "B0OTHER011", title: "Something Else Entirely", authors: `{"name":"Ada One"}`,
			narrators: `{"name":"Ann Reader"}`, series: `{"name":"Circus of the Dead","position":"5"}`},
	)); sum.NewWorks != 2 {
		t.Fatalf("seed run: NewWorks = %d, want 2", sum.NewWorks)
	}
	sum = runLibexInto(t, dataDir, rows(
		libexRow{asin: "B0CIRCUS12", title: "Circus of the Dead, Book 2", authors: `{"name":"Ada One"}`,
			narrators: `{"name":"Bob Reader"}`, series: `{"name":"Circus of the Dead","position":"2"}`},
	))
	if sum.SkippedDuplicateIdentity != 0 || sum.NewWorks != 1 {
		t.Errorf("unplaced match: SkippedDuplicateIdentity = %d, NewWorks = %d, want 0 and 1; warnings = %v",
			sum.SkippedDuplicateIdentity, sum.NewWorks, sum.Warnings)
	}

	// (c) the row states no series, so it carries no claim to confirm - only the
	// marker in its title.
	dataDir = seedPlainWork(t)
	sum = runLibexInto(t, dataDir, rows(
		libexRow{asin: "B0HAMMER07", title: "Hammered, Book 7", authors: `{"name":"Kevin Hearne"}`,
			narrators: `{"name":"Ann Reader"}`},
	))
	if sum.SkippedDuplicateIdentity != 0 || sum.NewWorks != 1 {
		t.Errorf("claim-less stated volume: SkippedDuplicateIdentity = %d, NewWorks = %d, want 0 and 1; warnings = %v",
			sum.SkippedDuplicateIdentity, sum.NewWorks, sum.Warnings)
	}
}

// F3: a COLLECTION is not the volume it collects. Both spellings the audit measured -
// a "Books 1-3" range and a "Complete Boxed Set" - reduce to the plain title once the
// packaging comes off.
func TestCreateAcceptsACollectionBesideItsVolume(t *testing.T) {
	dataDir := t.TempDir()
	if sum := runLibexInto(t, dataDir, rows(
		libexRow{asin: "B0BRAVE001", title: "Bravelands", authors: `{"name":"Erin Hunter"}`,
			narrators: `{"name":"Ann Reader"}`, minutes: 400},
		libexRow{asin: "B0REDRIS01", title: "Red Rising", authors: `{"name":"Pierce Brown"}`,
			narrators: `{"name":"Ann Reader"}`, minutes: 400},
	)); sum.NewWorks != 2 {
		t.Fatalf("seed run: NewWorks = %d, want 2", sum.NewWorks)
	}

	sum := runLibexInto(t, dataDir, rows(
		libexRow{asin: "B0BRAVEBOX", title: "Bravelands: Books 1-3", authors: `{"name":"Erin Hunter"}`,
			narrators: `{"name":"Ann Reader"}`, minutes: 1200},
		libexRow{asin: "B0REDRIBOX", title: "Red Rising: The Complete Boxed Set", authors: `{"name":"Pierce Brown"}`,
			narrators: `{"name":"Ann Reader"}`, minutes: 1200},
	))
	if sum.SkippedDuplicateIdentity != 0 {
		t.Errorf("SkippedDuplicateIdentity = %d, want 0: a boxed set is not the book it collects",
			sum.SkippedDuplicateIdentity)
	}
	if sum.NewWorks != 2 {
		t.Errorf("NewWorks = %d, want 2; warnings = %v", sum.NewWorks, sum.Warnings)
	}
}

// The other two planning modes never build the index and never refuse: enrichment
// matches by ASIN and creates nothing, and the recordings-only pass resolves a work
// the catalogue must already hold. A row that the create guard would refuse is, in
// that mode, exactly the alternate narration the mode exists for.
func TestOtherModesAreUntouchedByTheGuard(t *testing.T) {
	dataDir := seedPlainWork(t)

	sum, err := RunLibex(writeBooks(t, rows(
		libexRow{asin: "B0RECONLY1", title: "Hammered", authors: `{"name":"Kevin Hearne"}`,
			narrators: `{"name":"Christopher Ragland"}`, minutes: 470},
	)), Options{DataDir: dataDir, ImportDate: testImportDate, Mode: ModeRecordingsOnly})
	if err != nil {
		t.Fatalf("recordings-only run: %v", err)
	}
	if sum.SkippedDuplicateIdentity != 0 {
		t.Errorf("SkippedDuplicateIdentity = %d, want 0 outside the create mode", sum.SkippedDuplicateIdentity)
	}
	if sum.NewRecordings != 1 {
		t.Errorf("NewRecordings = %d, want 1: the alternate narration must land", sum.NewRecordings)
	}
}
