package importer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/testpack"
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

// Issue #2258's review: a title word is not a stated volume. "Level One Dropout"
// begins with a division word and a word number, and had the stated-volume rule read
// it as volume 1, the positive test above would have fired on a decorated second
// listing of it (nothing places a series-less work at volume 1) and MINTED the
// duplicate as a sibling work - exactly what main refused. The division word's word
// number is read only in marker position, so this row is refused as it always was.
func TestCreateRefusesADecoratedDuplicateWhoseTitleBeginsWithADivisionWord(t *testing.T) {
	dataDir := t.TempDir()
	if sum := runLibexInto(t, dataDir, rows(
		libexRow{asin: "B0LEVEL001", title: "Level One Dropout", authors: `{"name":"Ada One"}`,
			narrators: `{"name":"Ann Reader"}`, minutes: 600},
	)); sum.NewWorks != 1 {
		t.Fatalf("seed run: NewWorks = %d, want 1", sum.NewWorks)
	}
	sum := runLibexInto(t, dataDir, rows(
		libexRow{asin: "B0LEVEL002", title: "Level One Dropout: A LitRPG Adventure", authors: `{"name":"Ada One"}`,
			narrators: `{"name":"Bob Reader"}`, minutes: 610},
	))
	if sum.SkippedDuplicateIdentity != 1 || sum.NewWorks != 0 {
		t.Errorf("SkippedDuplicateIdentity = %d, NewWorks = %d, want 1 and 0: the decorated listing is the catalogued book; warnings = %v",
			sum.SkippedDuplicateIdentity, sum.NewWorks, sum.Warnings)
	}
	// The edition-marker spelling never reaches the guard at all - cleanWorkTitle
	// strips it and the row's own slug chain lands on the catalogued work - but it
	// must not mint a work either.
	sum = runLibexInto(t, dataDir, rows(
		libexRow{asin: "B0LEVEL003", title: "Level One Dropout (Unabridged)", authors: `{"name":"Ada One"}`,
			narrators: `{"name":"Cy Reader"}`, minutes: 605},
	))
	if sum.NewWorks != 0 {
		t.Errorf("(Unabridged): NewWorks = %d, want 0; warnings = %v", sum.NewWorks, sum.Warnings)
	}
}

// The same guard for a word-numbered division IN marker position, which the audit's
// reading does count ("Locked In: Season One" states season 1): the positive test
// reads titlerule.WriterPolicy, which leaves that reading out, so a decorated second
// listing is refused exactly as main refused it rather than minted beside the work
// no series places.
func TestCreateRefusesADecoratedDuplicateOfAWordNumberedSeason(t *testing.T) {
	dataDir := t.TempDir()
	if sum := runLibexInto(t, dataDir, rows(
		libexRow{asin: "B0LOCKED01", title: "Locked In: Season One", authors: `{"name":"Ada One"}`,
			narrators: `{"name":"Ann Reader"}`, minutes: 600},
	)); sum.NewWorks != 1 {
		t.Fatalf("seed run: NewWorks = %d, want 1", sum.NewWorks)
	}
	sum := runLibexInto(t, dataDir, rows(
		libexRow{asin: "B0LOCKED02", title: "Locked In: Season One: A LitRPG Adventure", authors: `{"name":"Ada One"}`,
			narrators: `{"name":"Bob Reader"}`, minutes: 610},
	))
	if sum.SkippedDuplicateIdentity != 1 || sum.NewWorks != 0 {
		t.Errorf("SkippedDuplicateIdentity = %d, NewWorks = %d, want 1 and 0; warnings = %v",
			sum.SkippedDuplicateIdentity, sum.NewWorks, sum.Warnings)
	}
}

// The CONTRADICTION half of the same rule: a word-numbered division states nothing to a
// writer, so it cannot contradict a catalogued volume either. "Nameless (Season One)"
// against "Nameless (Volume II)" is 1 against 2 for the audit, and a match the create
// guard vetoed on that reading would mint the row as a sibling work. Both halves of the
// probe - the catalogued work, and a work this run wrote - read the writer policy.
func TestCreateGuardReadsNoContradictionFromAWordNumberedSeason(t *testing.T) {
	seed := libexRow{asin: "B0NAMELES1", title: "Nameless (Volume II)", authors: `{"name":"Ada One"}`,
		narrators: `{"name":"Ann Reader"}`, minutes: 600}
	row := libexRow{asin: "B0NAMELES2", title: "Nameless (Season One)", authors: `{"name":"Ada One"}`,
		narrators: `{"name":"Bob Reader"}`, minutes: 610}

	dataDir := t.TempDir()
	if sum := runLibexInto(t, dataDir, rows(seed)); sum.NewWorks != 1 {
		t.Fatalf("seed run: NewWorks = %d, want 1", sum.NewWorks)
	}
	if sum := runLibexInto(t, dataDir, rows(row)); sum.SkippedDuplicateIdentity != 1 || sum.NewWorks != 0 {
		t.Errorf("catalogued: SkippedDuplicateIdentity = %d, NewWorks = %d, want 1 and 0; warnings = %v",
			sum.SkippedDuplicateIdentity, sum.NewWorks, sum.Warnings)
	}

	dataDir = t.TempDir()
	if sum := runLibexInto(t, dataDir, rows(seed, row)); sum.SkippedDuplicateIdentity != 1 || sum.NewWorks != 1 {
		t.Errorf("one run: SkippedDuplicateIdentity = %d, NewWorks = %d, want 1 and 1; warnings = %v",
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

// The two title QUALIFIERS (titlerule's qualifiers.go) are stripped before work
// identity (cleanWorkTitle, through titlerule.StripTitleQualifiers), so a
// marketplace-edition or narrator-qualified listing of a catalogued book RESOLVES to
// it and is judged by the ordinary recording rules - attached as a new narration, or
// merged by ASIN into the same production - rather than refused by the duplicate
// guard or minted beside it (48 of the 51 tree titles carrying "International Edition" have such a twin). The
// brand possessive is a comparison rule only, so a brand respelling still meets the
// duplicate guard.
func TestQualifiedListingsAttachToTheCataloguedWork(t *testing.T) {
	dataDir := t.TempDir()
	if sum := runLibexInto(t, dataDir, rows(
		libexRow{asin: "B0QUALIF01", title: "The Search", authors: `{"name":"Ada One"}`,
			narrators: `{"name":"Ann Reader"}`, minutes: 568},
		libexRow{asin: "B0QUALIF02", title: "Tom Clancy's Oath of Office", authors: `{"name":"Bo Two"}`,
			narrators: `{"name":"Ann Reader"}`, minutes: 900},
	)); sum.NewWorks != 2 {
		t.Fatalf("seed run: NewWorks = %d, want 2", sum.NewWorks)
	}
	for _, c := range []struct {
		row  libexRow
		want string // "merge": the ASIN joins the same production; "recording": a new narration
	}{
		// The same narrator within the runtime tolerance is the same production.
		{libexRow{asin: "B0QUALIF03", title: "The Search: International Edition", authors: `{"name":"Ada One"}`,
			narrators: `{"name":"Ann Reader"}`, minutes: 527}, "merge"},
		{libexRow{asin: "B0QUALIF04", title: "The Search “International Edition”", authors: `{"name":"Ada One"}`,
			narrators: `{"name":"Cy Reader"}`, minutes: 530}, "recording"},
		{libexRow{asin: "B0QUALIF05", title: "The Search, Read by Dee Reader", authors: `{"name":"Ada One"}`,
			narrators: `{"name":"Dee Reader"}`, minutes: 560}, "recording"},
	} {
		before := len(recSlugsOf(t, dataDir, "the-search"))
		sum := runLibexInto(t, dataDir, rows(c.row))
		if sum.NewWorks != 0 || sum.SkippedDuplicateIdentity != 0 {
			t.Errorf("%q: NewWorks = %d, SkippedDuplicateIdentity = %d, want 0 and 0; warnings = %v",
				c.row.title, sum.NewWorks, sum.SkippedDuplicateIdentity, sum.Warnings)
		}
		if c.want == "recording" && (sum.NewRecordings != 1 || len(recSlugsOf(t, dataDir, "the-search")) != before+1) {
			t.Errorf("%q: NewRecordings = %d, want a new recording under the-search", c.row.title, sum.NewRecordings)
		}
		if c.want == "merge" && sum.MergedASINs != 1 {
			t.Errorf("%q: MergedASINs = %d, want the ASIN merged into the same production", c.row.title, sum.MergedASINs)
		}
	}
	// The brand fold is the identity KEY's, not the work title's: refused, not attached.
	sum := runLibexInto(t, dataDir, rows(
		libexRow{asin: "B0QUALIF06", title: "Tom Clancy Oath of Office", authors: `{"name":"Bo Two"}`,
			narrators: `{"name":"Eve Reader"}`, minutes: 905},
	))
	if sum.SkippedDuplicateIdentity != 1 || sum.NewWorks != 0 {
		t.Errorf("brand respelling: SkippedDuplicateIdentity = %d, NewWorks = %d, want 1 and 0; warnings = %v",
			sum.SkippedDuplicateIdentity, sum.NewWorks, sum.Warnings)
	}
	// A title that merely USES the words is no qualifier, and is its own book.
	sum = runLibexInto(t, dataDir, rows(
		libexRow{asin: "B0QUALIF07", title: "Narrated by the Author: The Search", authors: `{"name":"Ada One"}`,
			narrators: `{"name":"Ann Reader"}`, minutes: 300},
	))
	if sum.SkippedDuplicateIdentity != 0 || sum.NewWorks != 1 {
		t.Errorf("a lead-in opening a title: SkippedDuplicateIdentity = %d, NewWorks = %d, want 0 and 1; warnings = %v",
			sum.SkippedDuplicateIdentity, sum.NewWorks, sum.Warnings)
	}
}

// A title that is NOTHING but a qualifier keeps itself: stripping would leave no title.
func TestABareQualifierTitleKeepsItsTitle(t *testing.T) {
	for _, title := range []string{`"International Edition"`, "(Narrated by Jane Doe)"} {
		if got := cleanWorkTitle(title); got != title {
			t.Errorf("cleanWorkTitle(%q) = %q, want it unchanged", title, got)
		}
	}
	for title, want := range map[string]string{
		"The Search: International Edition (Unabridged)": "The Search",
		"ESV Audio Bible, Read by Ray Ortlund":           "ESV Audio Bible",
		"Murder: Read by Candlelight":                    "Murder: Read by Candlelight",
		// A marker standing BEFORE the qualifier comes off once the qualifier has.
		"The Search (Unabridged), Read by Dee Reader": "The Search",
		// A volume riding after the credit keeps the credit: it is no name.
		"The Search, Read by Dee Reader, Book Two": "The Search, Read by Dee Reader, Book Two",
	} {
		if got := cleanWorkTitle(title); got != want {
			t.Errorf("cleanWorkTitle(%q) = %q, want %q", title, got, want)
		}
	}
}

// lonelyBookTree seeds a work catalogued under its QUALIFIED title alone - the shape
// every import before the qualifier strip created - plus, when withTwin is set, the
// plain twin a cleaned title reaches first.
func lonelyBookTree(t *testing.T, withTwin bool) string {
	t.Helper()
	files := map[string]string{}
	add := func(id, title string) {
		files["works/"+shard(id)+"/"+id+"/work.json"] = testpack.WorkJSON(t, id, title, testpack.WithAuthors("ada-mapmaker"))
		files["works/"+shard(id)+"/"+id+"/recordings/r1.json"] = testpack.RecJSON(t, "r1", id,
			testpack.WithNarrators("bea-reader"), testpack.WithRuntime(500))
	}
	add("lonely-book-international-edition", "Lonely Book: International Edition")
	if withTwin {
		add("lonely-book", "Lonely Book")
	}
	return seedTombstoneTree(t, files, nil)
}

// A work catalogued under its qualified title ALONE stays reachable: the cleaned
// title's chain holds nothing, so resolveWork walks the qualified title as a merge
// target (sourceBook.qualifiedTitle) and the row takes the ordinary ASIN-merge or
// new-recording path - in create mode and in the recordings-only pass alike - rather
// than being refused by the duplicate-identity guard its key still trips.
func TestAQualifiedOnlyWorkStaysReachable(t *testing.T) {
	const title = "Lonely Book: International Edition"
	dataDir := lonelyBookTree(t, false)
	sum := runLibexInto(t, dataDir, rows(libexRow{asin: "B0LONELY01", title: title,
		authors: `{"name":"Ada Mapmaker"}`, narrators: `{"name":"Bea Reader"}`, minutes: 505}))
	if sum.MergedASINs != 1 || sum.NewWorks != 0 || sum.SkippedDuplicateIdentity != 0 {
		t.Errorf("same production: MergedASINs = %d, NewWorks = %d, SkippedDuplicateIdentity = %d, want 1, 0, 0; warnings = %v",
			sum.MergedASINs, sum.NewWorks, sum.SkippedDuplicateIdentity, sum.Warnings)
	}
	sum = runLibexInto(t, dataDir, rows(libexRow{asin: "B0LONELY02", title: title,
		authors: `{"name":"Ada Mapmaker"}`, narrators: `{"name":"Cy Reader"}`, minutes: 480}))
	if sum.NewRecordings != 1 || sum.NewWorks != 0 || len(recSlugsOf(t, dataDir, "lonely-book-international-edition")) != 2 {
		t.Errorf("another narration: NewRecordings = %d, NewWorks = %d, want a new recording under the qualified work; warnings = %v",
			sum.NewRecordings, sum.NewWorks, sum.Warnings)
	}
	sum = runLibexWith(t, dataDir, Options{Mode: ModeRecordingsOnly}, libexRow{asin: "B0LONELY03", title: title,
		authors: `{"name":"Ada Mapmaker"}`, narrators: `{"name":"Dee Reader"}`, minutes: 470}.render())
	if sum.NewRecordings != 1 || sum.SkippedNoWork != 0 {
		t.Errorf("recordings-only: NewRecordings = %d, SkippedNoWork = %d, want 1 and 0; warnings = %v",
			sum.NewRecordings, sum.SkippedNoWork, sum.Warnings)
	}
}

// With a plain twin catalogued too, the CLEANED title's walk wins: the qualified
// chain is only ever a fallback.
func TestAPlainTwinWinsOverTheQualifiedWork(t *testing.T) {
	dataDir := lonelyBookTree(t, true)
	sum := runLibexInto(t, dataDir, rows(libexRow{asin: "B0LONELY04", title: "Lonely Book: International Edition",
		authors: `{"name":"Ada Mapmaker"}`, narrators: `{"name":"Cy Reader"}`, minutes: 480}))
	if sum.NewRecordings != 1 || len(recSlugsOf(t, dataDir, "lonely-book")) != 2 ||
		len(recSlugsOf(t, dataDir, "lonely-book-international-edition")) != 1 {
		t.Errorf("NewRecordings = %d; want the new narration under the plain twin; warnings = %v", sum.NewRecordings, sum.Warnings)
	}
}

// A row naming ANOTHER narrator than the qualified-only work's title does spells a
// different qualified slug, so its own pre-qualifier title cannot reach the work; the
// catalogued work whose title cleans to exactly the row's is walked instead.
func TestAnotherNarratorReachesANarratorQualifiedWork(t *testing.T) {
	const id = "jesus-listens-narrated-by-bea-reader"
	dataDir := seedTombstoneTree(t, map[string]string{
		"works/" + shard(id) + "/" + id + "/work.json": testpack.WorkJSON(t, id, "Jesus Listens (Narrated by Bea Reader)",
			testpack.WithAuthors("ada-mapmaker")),
		"works/" + shard(id) + "/" + id + "/recordings/r1.json": testpack.RecJSON(t, "r1", id,
			testpack.WithNarrators("bea-reader"), testpack.WithRuntime(300)),
	}, nil)
	sum := runLibexInto(t, dataDir, rows(libexRow{asin: "B0JESUS001", title: "Jesus Listens (Narrated by Cy Reader)",
		authors: `{"name":"Ada Mapmaker"}`, narrators: `{"name":"Cy Reader"}`, minutes: 310}))
	if sum.NewRecordings != 1 || sum.NewWorks != 0 || sum.SkippedDuplicateIdentity != 0 || len(recSlugsOf(t, dataDir, id)) != 2 {
		t.Errorf("NewRecordings = %d, NewWorks = %d, SkippedDuplicateIdentity = %d, want a new recording under %s; warnings = %v",
			sum.NewRecordings, sum.NewWorks, sum.SkippedDuplicateIdentity, id, sum.Warnings)
	}
}

// Two qualified-only works cleaning to one title decide nothing: neither is offered as
// a merge target, so nothing is attached to either and the row meets the duplicate
// guard as it did before the qualifier strip - whose own ambiguity rule lets it
// through to found a work of its own.
func TestSeveralQualifiedWorksDecideNothing(t *testing.T) {
	files := map[string]string{}
	for _, w := range [][2]string{
		{"jesus-listens-narrated-by-bea-reader", "Jesus Listens (Narrated by Bea Reader)"},
		{"jesus-listens-narrated-by-cy-reader", "Jesus Listens (Narrated by Cy Reader)"},
	} {
		id := w[0]
		files["works/"+shard(id)+"/"+id+"/work.json"] = testpack.WorkJSON(t, id, w[1], testpack.WithAuthors("ada-mapmaker"))
		files["works/"+shard(id)+"/"+id+"/recordings/r1.json"] = testpack.RecJSON(t, "r1", id,
			testpack.WithNarrators("bea-reader"), testpack.WithRuntime(300))
	}
	dataDir := seedTombstoneTree(t, files, nil)
	sum := runLibexInto(t, dataDir, rows(libexRow{asin: "B0JESUS002", title: "Jesus Listens (Narrated by Dee Reader)",
		authors: `{"name":"Ada Mapmaker"}`, narrators: `{"name":"Dee Reader"}`, minutes: 310}))
	for _, id := range []string{"jesus-listens-narrated-by-bea-reader", "jesus-listens-narrated-by-cy-reader"} {
		if recs := recSlugsOf(t, dataDir, id); len(recs) != 1 {
			t.Errorf("%s holds recordings %v: the row was attached to one of two candidates", id, recs)
		}
	}
	// The guard's ambiguity rule lets a row whose key names several works through, so
	// it founds its own work - exactly what it did before the qualifier strip.
	if sum.MergedASINs != 0 || sum.SkippedDuplicateIdentity != 0 || sum.NewWorks != 1 {
		t.Errorf("MergedASINs = %d, SkippedDuplicateIdentity = %d, NewWorks = %d, want 0, 0, 1; warnings = %v",
			sum.MergedASINs, sum.SkippedDuplicateIdentity, sum.NewWorks, sum.Warnings)
	}
}

// Two qualified-only works cleaning to one title but by DIFFERENT authors are not
// ambiguous to a row by one of them: only the works that clear the walk (authors,
// language) count, as the duplicate guard counts only MATCHING works - so the row
// reaches its own author's work rather than being refused as its duplicate.
func TestQualifiedWorksOfAnotherAuthorAreNoAmbiguity(t *testing.T) {
	files := map[string]string{}
	for _, w := range [][3]string{
		{"jesus-listens-narrated-by-bea-reader", "Jesus Listens (Narrated by Bea Reader)", "ada-mapmaker"},
		{"jesus-listens-narrated-by-cy-reader", "Jesus Listens (Narrated by Cy Reader)", "zed-other"},
	} {
		id := w[0]
		files["works/"+shard(id)+"/"+id+"/work.json"] = testpack.WorkJSON(t, id, w[1], testpack.WithAuthors(w[2]))
		files["works/"+shard(id)+"/"+id+"/recordings/r1.json"] = testpack.RecJSON(t, "r1", id,
			testpack.WithNarrators("bea-reader"), testpack.WithRuntime(300))
	}
	files["people/ze/zed-other.json"] = testpack.PersonJSON(t, "zed-other", "Zed Other")
	dataDir := seedTombstoneTree(t, files, nil)
	sum := runLibexInto(t, dataDir, rows(libexRow{asin: "B0JESUS003", title: "Jesus Listens (Narrated by Dee Reader)",
		authors: `{"name":"Ada Mapmaker"}`, narrators: `{"name":"Dee Reader"}`, minutes: 310}))
	if sum.NewRecordings != 1 || sum.NewWorks != 0 || sum.SkippedDuplicateIdentity != 0 ||
		len(recSlugsOf(t, dataDir, "jesus-listens-narrated-by-bea-reader")) != 2 {
		t.Errorf("NewRecordings = %d, NewWorks = %d, SkippedDuplicateIdentity = %d, want a new recording under the author's work; warnings = %v",
			sum.NewRecordings, sum.NewWorks, sum.SkippedDuplicateIdentity, sum.Warnings)
	}
}

// The recordings-only pass - the alternate-narration pass - reaches a work catalogued
// only under ANOTHER narrator's qualified title, as the create path does.
func TestRecordingsOnlyReachesANarratorQualifiedWork(t *testing.T) {
	const id = "jesus-listens-narrated-by-bea-reader"
	dataDir := seedTombstoneTree(t, map[string]string{
		"works/" + shard(id) + "/" + id + "/work.json": testpack.WorkJSON(t, id, "Jesus Listens (Narrated by Bea Reader)",
			testpack.WithAuthors("ada-mapmaker")),
		"works/" + shard(id) + "/" + id + "/recordings/r1.json": testpack.RecJSON(t, "r1", id,
			testpack.WithNarrators("bea-reader"), testpack.WithRuntime(300)),
	}, nil)
	sum := runLibexWith(t, dataDir, Options{Mode: ModeRecordingsOnly}, libexRow{asin: "B0JESUS004",
		title: "Jesus Listens (Narrated by Cy Reader)", authors: `{"name":"Ada Mapmaker"}`,
		narrators: `{"name":"Cy Reader"}`, minutes: 310}.render())
	if sum.NewRecordings != 1 || sum.SkippedNoWork != 0 || len(recSlugsOf(t, dataDir, id)) != 2 {
		t.Errorf("NewRecordings = %d, SkippedNoWork = %d, want a new recording under %s; warnings = %v",
			sum.NewRecordings, sum.SkippedNoWork, id, sum.Warnings)
	}
}

// A qualified-only work whose title opens with its series' name is keyed by the
// identity index under the SERIES-stripped key, so a lookup by the row's bare title
// key never met it; the catalogued arm is an exact cleaned-title lookup instead, and
// a row naming another narrator reaches the work rather than being refused as its
// duplicate.
func TestASeriesTitledQualifiedWorkStaysReachable(t *testing.T) {
	const id = "dragon-saga-ember-narrated-by-bea-reader"
	dataDir := seedTombstoneTree(t, map[string]string{
		"works/" + shard(id) + "/" + id + "/work.json": testpack.WorkJSON(t, id, "Dragon Saga: Ember (Narrated by Bea Reader)",
			testpack.WithAuthors("ada-mapmaker")),
		"works/" + shard(id) + "/" + id + "/recordings/r1.json": testpack.RecJSON(t, "r1", id,
			testpack.WithNarrators("bea-reader"), testpack.WithRuntime(300)),
		"series/dr/dragon-saga.json": testpack.SeriesJSON(t, "dragon-saga", "Dragon Saga", id+"@1"),
	}, nil)
	sum := runLibexInto(t, dataDir, rows(libexRow{asin: "B0DRAGON01", title: "Dragon Saga: Ember (Narrated by Cy Reader)",
		authors: `{"name":"Ada Mapmaker"}`, narrators: `{"name":"Cy Reader"}`, minutes: 310,
		series: `{"name":"Dragon Saga","position":"1"}`}))
	if sum.NewRecordings != 1 || sum.NewWorks != 0 || sum.SkippedDuplicateIdentity != 0 || len(recSlugsOf(t, dataDir, id)) != 2 {
		t.Errorf("NewRecordings = %d, NewWorks = %d, SkippedDuplicateIdentity = %d, want a new recording under %s; warnings = %v",
			sum.NewRecordings, sum.NewWorks, sum.SkippedDuplicateIdentity, id, sum.Warnings)
	}
}
