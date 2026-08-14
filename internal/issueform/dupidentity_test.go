package issueform

import (
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/testpack"
	"github.com/kodestar/audiosilo-meta/internal/titlerule"
	"github.com/kodestar/audiosilo-meta/pkg/check"
)

// dupidentity_test.go pins the intake-time duplicate and decoration prevention on
// the CALIBRATION SHAPES the data-quality audit measured, one fixture per shape,
// each with the pair it must and must not fire on:
//
//   - a decorated title stating a series volume the catalogue already holds (the
//     "hammered-the-iron-druid-chronicles-book-3" record beside the plain
//     "hammered" that IS volume 3);
//   - a normalized-identity duplicate whose catalogued twin lists a SUPERSET of the
//     submitted authors, the extra person being role-credited (the fork shape that
//     split the French Witcher run in two);
//   - a decorated title that cleans safely (it is composed under the clean title);
//   - a decorated title that does NOT clean safely (it is a maintainer's, not a
//     mechanical rewrite).

// dupSeedFiles is the calibration catalogue: the Iron Druid volume 3, the Witcher
// fork's surviving record, and the people they credit.
//
// It is deliberately SEPARATE from seedFiles(): the shared seed is what every
// existing verdict fixture is written against, and adding a series with an occupied
// position to it would change what those submissions collide with.
func dupSeedFiles() map[string]string {
	return map[string]string{
		"people/ke/kevin-hearne.json": `{
  "id": "kevin-hearne",
  "license": "CC0-1.0",
  "name": "Kevin Hearne",
  "sources": [{"type": "user", "imported_at": "2026-07-01"}]
}`,
		"people/lu/luke-daniels.json": `{
  "id": "luke-daniels",
  "license": "CC0-1.0",
  "name": "Luke Daniels",
  "sources": [{"type": "user", "imported_at": "2026-07-01"}]
}`,
		"people/an/andrzej-sapkowski.json": `{
  "id": "andrzej-sapkowski",
  "license": "CC0-1.0",
  "name": "Andrzej Sapkowski",
  "sources": [{"type": "user", "imported_at": "2026-07-01"}]
}`,
		"people/da/danuta-stok.json": `{
  "id": "danuta-stok",
  "license": "CC0-1.0",
  "name": "Danuta Stok",
  "sources": [{"type": "user", "imported_at": "2026-07-01"}]
}`,
		// Volume 3 of the Iron Druid Chronicles, under its own plain title.
		"works/ha/hammered/work.json": `{
  "authors": ["kevin-hearne"],
  "id": "hammered",
  "language": "en",
  "license": "CC0-1.0",
  "sources": [{"type": "user", "imported_at": "2026-07-01"}],
  "title": "Hammered"
}`,
		"works/ha/hammered/recordings/luke-daniels-2011.json": `{
  "asin": [{"asin": "B005HAMMER", "region": "us"}],
  "id": "luke-daniels-2011",
  "language": "en",
  "license": "CC0-1.0",
  "narrators": ["luke-daniels"],
  "runtime_min": 480,
  "sources": [{"type": "user", "imported_at": "2026-07-01"}],
  "work": "hammered"
}`,
		// The role-credit shape: the record lists the translator among its authors AND
		// states the role, so its IDENTITY authors are the novelist alone.
		"works/th/the-blood-of-elves/work.json": `{
  "authors": ["andrzej-sapkowski", "danuta-stok"],
  "credits": [{"person": "danuta-stok", "role": "translator"}],
  "id": "the-blood-of-elves",
  "language": "en",
  "license": "CC0-1.0",
  "sources": [{"type": "user", "imported_at": "2026-07-01"}],
  "title": "The Blood of Elves"
}`,
		"works/th/the-blood-of-elves/recordings/luke-daniels-2008.json": `{
  "asin": [{"asin": "B005ELVES1", "region": "us"}],
  "id": "luke-daniels-2008",
  "language": "en",
  "license": "CC0-1.0",
  "narrators": ["luke-daniels"],
  "runtime_min": 600,
  "sources": [{"type": "user", "imported_at": "2026-07-01"}],
  "work": "the-blood-of-elves"
}`,
		"series/th/the-iron-druid-chronicles.json": `{
  "id": "the-iron-druid-chronicles",
  "license": "CC0-1.0",
  "name": "The Iron Druid Chronicles",
  "sources": [{"type": "user", "imported_at": "2026-07-01"}],
  "works": [{"position": "3", "work": "hammered"}]
}`,
	}
}

// dupSeedTree writes the calibration catalogue and asserts it validates, so a
// schema drift fails here rather than masking a gate bug.
func dupSeedTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	testpack.Seed(t, dir, dupSeedFiles())
	if res := check.Load(dir); !res.OK() {
		t.Fatalf("seed tree does not validate: %v", res.Problems)
	}
	return dir
}

// dupWorkBody is addWorkBody with the series fields fillable, since two of the
// gates read them.
func dupWorkBody(title, authors, narrators, seriesName, seriesPos string) string {
	return field(fWorkTitle, title) +
		field(fWorkSubtitle, "") +
		field(fWorkAuthors, authors) +
		field(fWorkLanguage, "en") +
		field(fWorkFirstPublished, "2011") +
		field(fWorkGenres, "") +
		field(fWorkSeriesName, seriesName) +
		field(fWorkSeriesPosition, seriesPos) +
		field(fWorkISBN, "") +
		field(fWorkWikidata, "") +
		field(fWorkOpenLibrary, "") +
		field(fRecNarrators, narrators) +
		field(fRecAbridged, "Unabridged") +
		field(fRecRuntime, "480") +
		field(fRecRelease, "2011-07-05") +
		field(fRecPublisher, "Del Rey") +
		field(fRecASINs, "US: B0FRESH001") +
		field(fRecISBNs, "") +
		field(fRecCoverURL, "") +
		field(fSources, "Audible product page") +
		"### Factual data\n\n- [x] factual\n\n" +
		"### " + fCC0 + "\n\n" + checkedBox()
}

func processAddWork(t *testing.T, dir, body string) Result {
	t.Helper()
	return Process(Options{DataDir: dir, Template: "add-work", Body: body, Date: "2026-08-14"})
}

// The decorated series-volume shape: the submitted title states the series and the
// volume number, the catalogue already holds a work at that position, and the
// verdict names THAT member - the whole point of the gate, since the submitter
// cannot tell from a slug that "Hammered" is the book they are describing.
func TestAddWorkRefusesADecoratedSeriesVolume(t *testing.T) {
	dir := dupSeedTree(t)
	res := processAddWork(t, dir, dupWorkBody(
		"Hammered: The Iron Druid Chronicles, Book 3", "Kevin Hearne", "Christopher Ragland", "", ""))

	if res.Status != StatusDuplicate {
		t.Fatalf("status = %q, want %q; messages = %v", res.Status, StatusDuplicate, res.Messages)
	}
	if !anyContains(res.Messages, "hammered") || !anyContains(res.Messages, "volume 3") {
		t.Errorf("the verdict must name the member and the position: %v", res.Messages)
	}
	if recordExists(t, dir, "works/ha/hammered-the-iron-druid-chronicles-book-3/work.json") {
		t.Error("a second record of volume 3 was composed")
	}
}

// The same shape with the series stated in the FORM rather than only in the title:
// the gate reads the catalogue's positions either way.
func TestAddWorkRefusesAStatedSeriesVolumeAlreadyFilled(t *testing.T) {
	dir := dupSeedTree(t)
	res := processAddWork(t, dir, dupWorkBody(
		"Hammered, Book 3", "Kevin Hearne", "Christopher Ragland", "The Iron Druid Chronicles", "3"))

	if res.Status != StatusDuplicate {
		t.Fatalf("status = %q, want %q; messages = %v", res.Status, StatusDuplicate, res.Messages)
	}
}

// The gate must NOT fire for a volume the series does not hold: volume 4 is a book
// we do not have, and refusing it would be a lost contribution.
func TestAddWorkAcceptsAFreeSeriesPosition(t *testing.T) {
	dir := dupSeedTree(t)
	res := processAddWork(t, dir, dupWorkBody(
		"Tricked: The Iron Druid Chronicles, Book 4", "Kevin Hearne", "Luke Daniels", "", ""))

	if res.Status != StatusOK {
		t.Fatalf("status = %q, want ok; messages = %v", res.Status, res.Messages)
	}
	// Composed under the CLEAN title, not the retailer's: the strip fired.
	work := readFile(t, dir, "works/tr/tricked/work.json")
	if !strings.Contains(work, `"title": "Tricked"`) {
		t.Errorf("the decorated title was not cleaned:\n%s", work)
	}
}

// A serial published under ONE title: volume 4 of a series whose volume 3 is stored
// at the slug the strip would produce. The marker is the only thing that tells the
// two apart, so the title is kept as submitted rather than cleaned onto its
// sibling's slug - the strip must never turn a book we do not hold into a duplicate
// verdict.
func TestAddWorkKeepsTheMarkerOfANewVolume(t *testing.T) {
	dir := dupSeedTree(t)
	res := processAddWork(t, dir, dupWorkBody(
		"Hammered, Book 4", "Kevin Hearne", "Luke Daniels", "The Iron Druid Chronicles", "4"))

	if res.Status != StatusOK {
		t.Fatalf("status = %q, want ok; messages = %v", res.Status, res.Messages)
	}
	if !recordExists(t, dir, "works/ha/hammered-book-4/work.json") {
		t.Fatalf("volume 4 was not composed under its own slug; messages = %v", res.Messages)
	}
	if !anyContains(res.Messages, "kept as submitted") {
		t.Errorf("the verdict must say why the marker survived: %v", res.Messages)
	}
}

// The normalized-identity gate on the author-SUPERSET shape: the catalogued record
// lists the translator among its authors and role-credits them, the submission names
// only the novelist, and the two titles differ by a leading article - so no
// identifier, narrator or slug gate can see the collision.
func TestAddWorkRefusesANormalizedIdentityDuplicate(t *testing.T) {
	dir := dupSeedTree(t)
	res := processAddWork(t, dir, dupWorkBody(
		"Blood of Elves", "Andrzej Sapkowski", "Peter Kenny", "", ""))

	if res.Status != StatusDuplicate {
		t.Fatalf("status = %q, want %q; messages = %v", res.Status, StatusDuplicate, res.Messages)
	}
	if !anyContains(res.Messages, "the-blood-of-elves") {
		t.Errorf("the verdict must name the catalogued work: %v", res.Messages)
	}
	if recordExists(t, dir, "works/bl/blood-of-elves/work.json") {
		t.Error("a second record of the work was composed")
	}
}

// The identity gate must not fire on a DIFFERENT book: the same normalized title by
// another author is another work, which is the whole reason the gate asks the
// author-nesting rule rather than the title alone.
func TestAddWorkAcceptsTheSameTitleByAnotherAuthor(t *testing.T) {
	dir := dupSeedTree(t)
	res := processAddWork(t, dir, dupWorkBody(
		"Blood of Elves", "Someone Else", "Peter Kenny", "", ""))

	if res.Status != StatusOK {
		t.Fatalf("status = %q, want ok; messages = %v", res.Status, res.Messages)
	}
	if !recordExists(t, dir, "works/bl/blood-of-elves/work.json") {
		t.Error("the different author's book was not composed")
	}
}

// A decorated title that cleans SAFELY is composed under the clean title, and the
// verdict says what was removed - the record a contributor gets is the one a
// maintainer would have written by hand.
func TestAddWorkStripsSafeDecoration(t *testing.T) {
	dir := dupSeedTree(t)
	res := processAddWork(t, dir, dupWorkBody(
		"Two Ravens (Unabridged)", "Kevin Hearne", "Luke Daniels", "", ""))

	if res.Status != StatusOK {
		t.Fatalf("status = %q, want ok; messages = %v", res.Status, res.Messages)
	}
	if recordExists(t, dir, "works/tw/two-ravens-unabridged/work.json") {
		t.Error("the work was composed under the decorated slug")
	}
	work := readFile(t, dir, "works/tw/two-ravens/work.json")
	if !strings.Contains(work, `"title": "Two Ravens"`) {
		t.Errorf("the edition marker was not stripped from the title:\n%s", work)
	}
	if !anyContains(res.Messages, "edition-marker") {
		t.Errorf("the verdict must say what was cleaned: %v", res.Messages)
	}
}

// A stripped edition marker makes the EXISTING work-slug gate reachable, which is
// half the point of stripping at all: "Hammered (Unabridged)" is the book stored at
// `hammered`, and before the strip it composed a second record beside it.
func TestAddWorkStrippedTitleMeetsTheSlugGate(t *testing.T) {
	dir := dupSeedTree(t)
	res := processAddWork(t, dir, dupWorkBody(
		"Hammered (Unabridged)", "Kevin Hearne", "Christopher Ragland", "", ""))

	if res.Status != StatusDuplicate {
		t.Fatalf("status = %q, want %q; messages = %v", res.Status, StatusDuplicate, res.Messages)
	}
	if recordExists(t, dir, "works/ha/hammered-unabridged/work.json") {
		t.Error("the decorated spelling was composed as a second work")
	}
}

// A decorated title the rules CANNOT clean safely is a maintainer's: the residual
// names no book, so no mechanical rewrite can say what this work is called.
func TestAddWorkRefusesADecorationOnlyTitle(t *testing.T) {
	dir := dupSeedTree(t)
	res := processAddWork(t, dir, dupWorkBody(
		"Omnibus: A LitRPG Adventure", "Kevin Hearne", "Luke Daniels", "", ""))

	if res.Status != StatusNeedsHuman {
		t.Fatalf("status = %q, want %q; messages = %v", res.Status, StatusNeedsHuman, res.Messages)
	}
	if !anyContains(res.Messages, "nothing that names a book") {
		t.Errorf("the verdict must name the reason: %v", res.Messages)
	}
	if recordExists(t, dir, "works/om/omnibus/work.json") {
		t.Error("a work was composed from a title that names no book")
	}
}

// A work whose title IS its series' name is ordinary - a one-book series is named
// after its book, and the series here is a catalogued one the title spells out whole.
// The strip has nothing safe to propose (the whole title would go), so the submission
// is composed exactly as it was written and says nothing about decoration.
func TestAddWorkAcceptsATitleThatIsItsSeriesName(t *testing.T) {
	dir := dupSeedTree(t)
	res := processAddWork(t, dir, dupWorkBody(
		"The Iron Druid Chronicles", "Kevin Hearne", "Luke Daniels", "", ""))

	if res.Status != StatusOK {
		t.Fatalf("status = %q, want ok; messages = %v", res.Status, res.Messages)
	}
	work := readFile(t, dir, "works/th/the-iron-druid-chronicles/work.json")
	if !strings.Contains(work, `"title": "The Iron Druid Chronicles"`) {
		t.Errorf("the title was not composed as submitted:\n%s", work)
	}
	if anyContains(res.Messages, "retailer decoration") {
		t.Errorf("a title that is its series' name was reported as decorated: %v", res.Messages)
	}
}

// Every strip-refusal code the rule package can return must have a decision recorded
// here - proceed, or a maintainer's - so a code added to internal/titlerule cannot
// reach this gate and fall through a default branch nobody chose.
func TestEveryStripRefusalIsClassified(t *testing.T) {
	for _, code := range titlerule.RefusalCodes() {
		if _, listed := decorationRefusals[code]; !listed {
			t.Errorf("strip refusal %q has no intake decision in decorationRefusals", code)
		}
	}
	if len(decorationRefusals) != len(titlerule.RefusalCodes()) {
		t.Errorf("decorationRefusals holds %d codes, the rule package defines %d",
			len(decorationRefusals), len(titlerule.RefusalCodes()))
	}
}

// The tier discipline every duplicate gate shares: a collision with a record that is
// still nothing but a libex mirror seed is needs-human (the submitter's data should
// replace the seed and the bot only composes new records), not a closed duplicate.
func TestNormalizedIdentityDuplicateOfAMirrorSeedNeedsAHuman(t *testing.T) {
	dir := t.TempDir()
	files := dupSeedFiles()
	files["works/th/the-blood-of-elves/work.json"] = strings.Replace(
		files["works/th/the-blood-of-elves/work.json"],
		`"sources": [{"type": "user", "imported_at": "2026-07-01"}]`,
		`"sources": [{"type": "libex-import", "imported_at": "2026-07-01"}]`, 1)
	testpack.Seed(t, dir, files)
	if res := check.Load(dir); !res.OK() {
		t.Fatalf("seed tree does not validate: %v", res.Problems)
	}

	res := processAddWork(t, dir, dupWorkBody(
		"Blood of Elves", "Andrzej Sapkowski", "Peter Kenny", "", ""))
	if res.Status != StatusNeedsHuman {
		t.Fatalf("status = %q, want %q; messages = %v", res.Status, StatusNeedsHuman, res.Messages)
	}
	if !anyContains(res.Messages, "seeded from the libex mirror") {
		t.Errorf("the mirror-seed verdict must explain itself: %v", res.Messages)
	}
}

// An ordinary submission that collides with nothing must still be composed, with no
// note about decoration it does not carry - the gates are additive.
func TestAddWorkUntouchedByTheNewGates(t *testing.T) {
	dir := dupSeedTree(t)
	res := processAddWork(t, dir, dupWorkBody(
		"A Wholly Different Book", "New Author", "New Narrator", "", ""))

	if res.Status != StatusOK {
		t.Fatalf("status = %q, want ok; messages = %v", res.Status, res.Messages)
	}
	if !recordExists(t, dir, "works/a-/a-wholly-different-book/work.json") {
		t.Fatalf("the work was not composed; messages = %v", res.Messages)
	}
	for _, m := range res.Messages {
		if strings.Contains(m, "retailer decoration") {
			t.Errorf("an undecorated title was reported as decorated: %q", m)
		}
	}
}
