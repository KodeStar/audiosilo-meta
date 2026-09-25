package importer

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/check"
)

// fakeSeriesLookup is the offline stand-in for the live libex service: a map
// from ASIN to what the service would say about it. It counts its calls, which
// is how the cap is pinned - "how many lookups did this run spend" is not
// visible anywhere else.
type fakeSeriesLookup struct {
	refs  map[string][]SeriesPosition
	err   error
	calls int
}

func (f *fakeSeriesLookup) SeriesRefs(_ context.Context, asin string) ([]SeriesPosition, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.refs[asin], nil
}

// runSeriesBooks imports an audiosilo-books envelope with the given options
// against a fresh data dir, filling in the two every test shares.
func runSeriesBooks(t *testing.T, envelope string, opts Options) (Summary, string) {
	t.Helper()
	dataDir := t.TempDir()
	opts.DataDir = dataDir
	opts.ImportDate = testImportDate
	sum, err := RunAudiosiloBooks(writeBooks(t, envelope), opts)
	if err != nil {
		t.Fatalf("import run: %v", err)
	}
	if res := check.Load(dataDir); !res.OK() {
		t.Fatalf("imported tree failed validation:\n%v", res.Problems)
	}
	return sum, dataDir
}

// seriesSlots reads a series record's memberships as work -> position.
func seriesSlots(t *testing.T, dataDir, address string) map[string]string {
	t.Helper()
	var series struct {
		Works []struct {
			Work     string `json:"work"`
			Position string `json:"position"`
		} `json:"works"`
	}
	readEntity(t, dataDir, address, &series)
	slots := map[string]string{}
	for _, sw := range series.Works {
		slots[sw.Work] = sw.Position
	}
	return slots
}

// warningWith returns the run's first warning containing substr.
func warningWith(sum Summary, substr string) (string, bool) {
	for _, w := range sum.Warnings {
		if strings.Contains(w, substr) {
			return w, true
		}
	}
	return "", false
}

// missingPositionWarning is the line a claim with no usable position has always
// produced, quoted here so a test can assert it is UNCHANGED rather than merely
// present.
const missingPositionWarning = `B0WH000001: series "Warhammer 40,000": missing or invalid position ""; not placed in series`

// warhammerExport is the measured shape: a personal library export naming the
// series a file is tagged with and no part number at all.
const warhammerExport = `{
  "format": "audiosilo-books",
  "version": 1,
  "books": [
    {
      "title": "Thorn Wishes Talon",
      "authors": ["Gav Thorpe"],
      "narrators": ["A Narrator"],
      "series": "Warhammer 40,000",
      "asin": "B0WH000001",
      "language": "en"
    }
  ]
}`

const warhammerSeriesFile = "series/wa/warhammer-40-000.json"

// warhammerLookup answers for the one ASIN the export carries.
func warhammerLookup(refs ...SeriesPosition) *fakeSeriesLookup {
	return &fakeSeriesLookup{refs: map[string][]SeriesPosition{"B0WH000001": refs}}
}

// ---------------------------------------------------------------------------
// Part 1: a missing position filled from the lookup.

// The whole point: libex names the same series and a usable position, so the
// work is PLACED instead of warned about, and the run says so in one line.
func TestSeriesPositionFilledFromLookup(t *testing.T) {
	lookup := warhammerLookup(SeriesPosition{Name: "Warhammer 40,000", Position: "3.0"})
	sum, dataDir := runSeriesBooks(t, warhammerExport, Options{SeriesLookup: lookup})

	if lookup.calls != 1 {
		t.Fatalf("lookups = %d, want 1", lookup.calls)
	}
	if _, found := warningWith(sum, "missing or invalid position"); found {
		t.Errorf("the row was placed, so the missing-position warning must be gone; warnings = %v", sum.Warnings)
	}
	slots := seriesSlots(t, dataDir, warhammerSeriesFile)
	// "3.0" and "3" are one position: the looked-up value goes through
	// makeSeriesRef exactly as the row's own would have.
	if got := slots["thorn-wishes-talon"]; got != "3" {
		t.Errorf("placed at %q, want 3 (slots = %v)", got, slots)
	}
	want := `1 series position(s) taken from libex (for example: "Thorn Wishes Talon" in "Warhammer 40,000" at 3)`
	if line, found := warningWith(sum, "taken from libex"); !found || line != want {
		t.Errorf("aggregated note = %q, want %q", line, want)
	}
}

// libex knows the ASIN and places it in a DIFFERENT series. That says nothing
// about the series the row claims, so the claim is left as it was found.
func TestSeriesPositionLookupOtherSeriesIsIgnored(t *testing.T) {
	lookup := warhammerLookup(SeriesPosition{Name: "Horus Heresy", Position: "3"})
	sum, dataDir := runSeriesBooks(t, warhammerExport, Options{SeriesLookup: lookup})

	if line, found := warningWith(sum, "missing or invalid position"); !found || line != missingPositionWarning {
		t.Errorf("warning = %q, want the unchanged %q", line, missingPositionWarning)
	}
	if _, found := warningWith(sum, "taken from libex"); found {
		t.Errorf("nothing was filled, so no note is due; warnings = %v", sum.Warnings)
	}
	if entryExists(t, dataDir, warhammerSeriesFile) {
		t.Errorf("no series may be created for a claim that was never placed")
	}
}

// libex knows the series and states no usable position either - the same gap,
// one source further out.
func TestSeriesPositionLookupWithoutAPositionIsIgnored(t *testing.T) {
	lookup := warhammerLookup(SeriesPosition{Name: "Warhammer 40,000", Position: ""})
	sum, _ := runSeriesBooks(t, warhammerExport, Options{SeriesLookup: lookup})

	if line, found := warningWith(sum, "missing or invalid position"); !found || line != missingPositionWarning {
		t.Errorf("warning = %q, want the unchanged %q", line, missingPositionWarning)
	}
}

// The service is free, public and external: a lookup that fails is reported and
// the run carries on with exactly the records and warnings it would have had.
func TestSeriesPositionLookupErrorDoesNotFailTheRun(t *testing.T) {
	lookup := warhammerLookup()
	lookup.err = errors.New("libex: 503 Service Unavailable")
	sum, dataDir := runSeriesBooks(t, warhammerExport, Options{SeriesLookup: lookup})

	if sum.NewWorks != 1 {
		t.Errorf("NewWorks = %d, want 1 (the row still imports)", sum.NewWorks)
	}
	if line, found := warningWith(sum, "missing or invalid position"); !found || line != missingPositionWarning {
		t.Errorf("warning = %q, want the unchanged %q", line, missingPositionWarning)
	}
	if _, found := warningWith(sum, "lookup(s) failed"); !found {
		t.Errorf("a failed lookup must be reported, not swallowed; warnings = %v", sum.Warnings)
	}
	if entryExists(t, dataDir, warhammerSeriesFile) {
		t.Errorf("no series may be created for a claim that was never placed")
	}
}

// The cap counts rows that NEED a lookup, and stops the pass when it runs out.
func TestSeriesPositionLookupRespectsTheCap(t *testing.T) {
	const export = `{
  "format": "audiosilo-books",
  "version": 1,
  "books": [
    {"title": "Alpha", "authors": ["A Writer"], "narrators": ["A Narrator"],
     "series": "Gap Series", "asin": "B0GAP00001", "language": "en"},
    {"title": "Beta", "authors": ["A Writer"], "narrators": ["A Narrator"],
     "series": "Gap Series", "asin": "B0GAP00002", "language": "en"},
    {"title": "Gamma", "authors": ["A Writer"], "narrators": ["A Narrator"],
     "series": "Gap Series", "series_position": "9", "asin": "B0GAP00003", "language": "en"}
  ]
}`
	lookup := &fakeSeriesLookup{refs: map[string][]SeriesPosition{
		"B0GAP00001": {{Name: "Gap Series", Position: "1"}},
		"B0GAP00002": {{Name: "Gap Series", Position: "2"}},
		"B0GAP00003": {{Name: "Gap Series", Position: "3"}},
	}}
	sum, dataDir := runSeriesBooks(t, export, Options{SeriesLookup: lookup, SeriesLookupLimit: 1})

	// Two rows need a position and the third does not, so the cap of 1 is spent
	// on the first of the two and the row that states its own is never a lookup.
	if lookup.calls != 1 {
		t.Fatalf("lookups = %d, want 1 (the cap, counting only the rows that need one)", lookup.calls)
	}
	if line, found := warningWith(sum, "taken from libex"); !found || !strings.HasPrefix(line, "1 series position(s)") {
		t.Errorf("aggregated note = %q, want exactly one filled position", line)
	}
	slots := seriesSlots(t, dataDir, "series/ga/gap-series.json")
	if slots["alpha"] != "1" || slots["gamma"] != "9" {
		t.Errorf("slots = %v, want alpha at 1 (filled) and gamma at 9 (its own)", slots)
	}
	if _, placed := slots["beta"]; placed {
		t.Errorf("beta was past the cap and must not be placed; slots = %v", slots)
	}
}

// No lookup is the default, and the default is exactly what the importer did
// before this rule existed: one warning, no series, no note.
func TestNilSeriesLookupIsTodaysBehaviour(t *testing.T) {
	sum, dataDir := runSeriesBooks(t, warhammerExport, Options{})

	if line, found := warningWith(sum, "missing or invalid position"); !found || line != missingPositionWarning {
		t.Errorf("warning = %q, want the unchanged %q", line, missingPositionWarning)
	}
	for _, w := range sum.Warnings {
		if strings.Contains(w, "libex") {
			t.Errorf("a run with no lookup must say nothing about one: %q", w)
		}
	}
	if entryExists(t, dataDir, warhammerSeriesFile) {
		t.Errorf("no series may be created for a claim that was never placed")
	}
	if sum.NewSeries != 0 {
		t.Errorf("NewSeries = %d, want 0", sum.NewSeries)
	}
}

// ---------------------------------------------------------------------------
// Part 2: a title's stated volume against the source's position.

// towerboundExport is the measured case: Audible's series index counts the side
// stories this catalogue numbers 3.1 and 5.1, so the retailer's 8 is the
// title's 6.
func towerboundExport(title, position string) string {
	return fmt.Sprintf(`{
  "format": "audiosilo-books",
  "version": 1,
  "books": [
    {
      "title": %q,
      "authors": ["A Writer"],
      "narrators": ["A Narrator"],
      "series": "Towerbound",
      "series_position": %q,
      "asin": "B0TWR00001",
      "language": "en"
    }
  ]
}`, title, position)
}

const towerboundSeriesFile = "series/to/towerbound.json"

func TestTitleVolumeBeatsADisagreeingSourcePosition(t *testing.T) {
	sum, dataDir := runSeriesBooks(t, towerboundExport("Towerbound, Book 6", "8"), Options{})

	want := `series "Towerbound": source position "8" disagrees with the title's Book 6; placed at 6`
	if line, found := warningWith(sum, "disagrees with the title"); !found || !strings.HasSuffix(line, want) {
		t.Errorf("warning = %q, want one ending %q", line, want)
	}
	slots := seriesSlots(t, dataDir, towerboundSeriesFile)
	if got := slots["towerbound-book-6"]; got != "6" {
		t.Errorf("placed at %q, want 6 (slots = %v)", got, slots)
	}
}

// WHICH marker a title states as its volume is titlerule.StatedVolume's TIER order
// (issue #2258), and placement reads it: a volume marker outranks a division marker
// in any spelling. Two pins, both deliberate:
//
//   - "Season 1 - Ep. 3" is volume 3 - the nested serial shape the tiers were kept
//     for (the flat "earliest marker wins" read 1 and would have moved a correctly
//     placed episode 3 to slot 1);
//   - "Book Two, Season 3" is volume 2 - a reading the tier order CHANGED from main's
//     arm order, which tried the division digits before the word arm and read 3. No
//     title on the tree carries that combination; this pins where such a row lands.
func TestTitleVolumeTierDecidesPlacement(t *testing.T) {
	sum, dataDir := runSeriesBooks(t, towerboundExport("Towerbound: Season 1 - Ep. 3", "3"), Options{})
	if line, found := warningWith(sum, "disagrees with the title"); found {
		t.Errorf("the episode agrees with the source, so nothing is arbitrated: %q", line)
	}
	if got := seriesSlots(t, dataDir, towerboundSeriesFile)["towerbound-season-1-ep-3"]; got != "3" {
		t.Errorf("episode placed at %q, want 3", got)
	}

	sum, dataDir = runSeriesBooks(t, towerboundExport("Towerbound, Book Two, Season 3", "3"), Options{})
	want := `series "Towerbound": source position "3" disagrees with the title's Book 2; placed at 2`
	if line, found := warningWith(sum, "disagrees with the title"); !found || !strings.HasSuffix(line, want) {
		t.Errorf("warning = %q, want one ending %q", line, want)
	}
	if got := seriesSlots(t, dataDir, towerboundSeriesFile)["towerbound-book-two-season-3"]; got != "2" {
		t.Errorf("placed at %q, want 2", got)
	}
}

// Agreement is not news: the two say the same thing, so nothing is warned about
// and the position is the one it always was.
func TestTitleVolumeAgreeingWithTheSourceIsSilent(t *testing.T) {
	sum, dataDir := runSeriesBooks(t, towerboundExport("Towerbound, Book 6", "6"), Options{})

	if line, found := warningWith(sum, "disagrees with the title"); found {
		t.Errorf("agreement must be silent, got %q", line)
	}
	if got := seriesSlots(t, dataDir, towerboundSeriesFile)["towerbound-book-6"]; got != "6" {
		t.Errorf("placed at %q, want 6", got)
	}
}

// The collision rule is untouched: a slot another work holds is not free to move
// into, so the source's position stands.
func TestTitleVolumeFallsBackWhenItsSlotIsTaken(t *testing.T) {
	const export = `{
  "format": "audiosilo-books",
  "version": 1,
  "books": [
    {"title": "Prelude at the Gate", "authors": ["A Writer"], "narrators": ["A Narrator"],
     "series": "Towerbound", "series_position": "6", "asin": "B0TWR00002", "language": "en"},
    {"title": "Towerbound, Book 6", "authors": ["A Writer"], "narrators": ["A Narrator"],
     "series": "Towerbound", "series_position": "8", "asin": "B0TWR00001", "language": "en"}
  ]
}`
	sum, dataDir := runSeriesBooks(t, export, Options{})

	if line, found := warningWith(sum, "disagrees with the title"); found {
		t.Errorf("the title's slot is taken, so nothing is moved and nothing is claimed: %q", line)
	}
	slots := seriesSlots(t, dataDir, towerboundSeriesFile)
	if slots["prelude-at-the-gate"] != "6" || slots["towerbound-book-6"] != "8" {
		t.Errorf("slots = %v, want the incumbent at 6 and the row at its source position 8", slots)
	}
}

// A title that states no volume states nothing to arbitrate with.
func TestTitleWithNoStatedVolumeIsUntouched(t *testing.T) {
	sum, dataDir := runSeriesBooks(t, towerboundExport("The Gate Opens", "8"), Options{})

	if line, found := warningWith(sum, "disagrees with the title"); found {
		t.Errorf("no volume is stated, so nothing disagrees: %q", line)
	}
	if got := seriesSlots(t, dataDir, towerboundSeriesFile)["the-gate-opens"]; got != "8" {
		t.Errorf("placed at %q, want the source's 8", got)
	}
}

// The word-number vocabulary stops where wordVolumeMarker's does: "Book
// Thirteen" keeps its words through the title rules and states no volume this
// importer can read, so the source's position stands rather than being
// second-guessed.
func TestUnreadableWordVolumeIsUntouched(t *testing.T) {
	sum, dataDir := runSeriesBooks(t, towerboundExport("Towerbound, Book Thirteen", "8"), Options{})

	if line, found := warningWith(sum, "disagrees with the title"); found {
		t.Errorf("the volume word is outside the vocabulary, so nothing is read back: %q", line)
	}
	slots := seriesSlots(t, dataDir, towerboundSeriesFile)
	if got := slots["towerbound-book-thirteen"]; got != "8" {
		t.Errorf("placed at %q, want the source's 8 (slots = %v)", got, slots)
	}
}

// A title that IS a number is not a title stating its volume. bareSeq reads a
// residual of nothing but digits, which is right for the duplicate gates and
// would place "1984" at position 1984 here, so the arbitration requires a
// volume MARKER.
func TestANumericTitleIsNotAStatedVolume(t *testing.T) {
	const export = `{
  "format": "audiosilo-books",
  "version": 1,
  "books": [
    {"title": "1984", "authors": ["A Writer"], "narrators": ["A Narrator"],
     "series": "Dystopias", "series_position": "2", "asin": "B0DYS00001", "language": "en"}
  ]
}`
	sum, dataDir := runSeriesBooks(t, export, Options{})

	if line, found := warningWith(sum, "disagrees with the title"); found {
		t.Errorf("a year is not a series position: %q", line)
	}
	if got := seriesSlots(t, dataDir, "series/dy/dystopias.json")["1984"]; got != "2" {
		t.Errorf("placed at %q, want the source's 2", got)
	}
}

// An omnibus RANGE is a statement about several volumes; a title's single number
// does not contradict it.
func TestARangePositionIsNeverArbitrated(t *testing.T) {
	sum, dataDir := runSeriesBooks(t, towerboundExport("Towerbound, Book 6", "1-3"), Options{})

	if line, found := warningWith(sum, "disagrees with the title"); found {
		t.Errorf("a range is not a single slot to contradict: %q", line)
	}
	if got := seriesSlots(t, dataDir, towerboundSeriesFile)["towerbound-book-6"]; got != "1-3" {
		t.Errorf("placed at %q, want the source's 1-3", got)
	}
}

// towerboundRows is an envelope of "Towerbound, Book 6" rows at the retailer's
// position 8, one per ASIN - re-releases of the one production (same narrator,
// the same stated runtime, which is what corroborates the title's volume when
// nothing else does - titleCorroborated).
func towerboundRows(asins ...string) string {
	rows := make([]string, len(asins))
	for i, a := range asins {
		rows[i] = fmt.Sprintf(`{"title": "Towerbound, Book 6", "authors": ["A Writer"], "narrators": ["A Narrator"],`+
			` "series": "Towerbound", "series_position": "8", "asin": %q, "language": "en", "runtime_min": 300}`, a)
	}
	return `{"format": "audiosilo-books", "version": 1, "books": [` + strings.Join(rows, ",") + `]}`
}

// runBooksOver runs one audiosilo-books import over an existing tree.
func runBooksOver(t *testing.T, dataDir, envelope string) Summary {
	t.Helper()
	sum, err := RunAudiosiloBooks(writeBooks(t, envelope), Options{DataDir: dataDir, ImportDate: testImportDate})
	if err != nil {
		t.Fatalf("import run: %v", err)
	}
	if res := check.Load(dataDir); !res.OK() {
		t.Fatalf("imported tree failed validation:\n%v", res.Problems)
	}
	return sum
}

// The serial guard reads the position a row would be PLACED at, not the raw one
// its source stated: run 1 places "Towerbound, Book 6" at the title's 6, so run
// 2's re-release of it - still stating the retailer's 8 - is the same volume and
// its ASIN merges, exactly as the two rows do within one run.
func TestSerialGuardReadsTheArbitratedPosition(t *testing.T) {
	// The recording guard on its own: the alternate-narration pass resolves the
	// work by title and author alone, so nothing but the guard stands between the
	// re-release and a duplicate sibling recording.
	t.Run("recordings-only second run", func(t *testing.T) {
		dataDir := t.TempDir()
		runBooksOver(t, dataDir, towerboundRows("B0TWR00001"))
		sum := runRecordingsOnly(t, dataDir,
			tombRow("B0TWR00009", "Towerbound, Book 6", "A Writer", "A Narrator", 300, "Towerbound", "8")+"\n", false)
		if sum.MergedASINs != 1 || sum.NewRecordings != 0 {
			t.Errorf("MergedASINs = %d, NewRecordings = %d; want the re-release merged: %v",
				sum.MergedASINs, sum.NewRecordings, sum.Warnings)
		}
	})
	// The same shape through the create path, where the work-level series claim
	// (seriesClaim.compatible) reads the row's position first.
	t.Run("two runs", func(t *testing.T) {
		dataDir := t.TempDir()
		runBooksOver(t, dataDir, towerboundRows("B0TWR00001"))
		sum := runBooksOver(t, dataDir, towerboundRows("B0TWR00009"))
		if sum.MergedASINs != 1 || sum.NewRecordings != 0 {
			t.Errorf("MergedASINs = %d, NewRecordings = %d; want the re-release merged: %v",
				sum.MergedASINs, sum.NewRecordings, sum.Warnings)
		}
	})
	t.Run("one run", func(t *testing.T) {
		sum := runBooksOver(t, t.TempDir(), towerboundRows("B0TWR00001", "B0TWR00009"))
		if sum.MergedASINs != 1 || sum.NewRecordings != 1 {
			t.Errorf("MergedASINs = %d, NewRecordings = %d; want the re-release merged: %v",
				sum.MergedASINs, sum.NewRecordings, sum.Warnings)
		}
	})
}

// When the title's slot was TAKEN, placement kept the source's position (8), so
// the work sits at 8 on disk. A re-release stating that same 8 is the same
// volume too: the row's claim carries both the title's and the source's
// position, and either matching the recording's is agreement.
func TestSerialGuardMatchesTheSourcePositionPlacementKept(t *testing.T) {
	dataDir := t.TempDir()
	runBooksOver(t, dataDir, `{"format": "audiosilo-books", "version": 1, "books": [
    {"title": "Prelude at the Gate", "authors": ["A Writer"], "narrators": ["A Narrator"],
     "series": "Towerbound", "series_position": "6", "asin": "B0TWR00002", "language": "en"}]}`)
	runBooksOver(t, dataDir, towerboundRows("B0TWR00001"))
	if got := seriesSlots(t, dataDir, towerboundSeriesFile)["towerbound-book-6"]; got != "8" {
		t.Fatalf("placed at %q, want the source's 8 (the title's 6 is taken)", got)
	}
	sum := runBooksOver(t, dataDir, towerboundRows("B0TWR00009"))
	if sum.MergedASINs != 1 || sum.NewRecordings != 0 {
		t.Errorf("MergedASINs = %d, NewRecordings = %d; want the re-release merged: %v",
			sum.MergedASINs, sum.NewRecordings, sum.Warnings)
	}
}

// A recording created THIS run still carries its row's raw claim (the retailer's
// 8, the title's 6), while the work it belongs to sits at 6. A later row of the
// same run stating that 6 - runtime unstated, so nothing else corroborates a
// title - is the volume the work sits at, and its ASIN merges exactly as it does
// when the first recording is read back from disk in a second run.
func TestSerialGuardAgreesWithTheWorksPlacementInOneRun(t *testing.T) {
	seed := `{"format": "audiosilo-books", "version": 1, "books": [{"title": "Towerbound, Book 6",` +
		` "authors": ["A Writer"], "narrators": ["Other Voice"], "series": "Towerbound", "series_position": "6",` +
		` "asin": "B0TWR00000", "language": "en", "runtime_min": 300}]}`
	first := tombRow("B0TWR00001", "Towerbound, Book 6", "A Writer", "A Narrator", 300, "Towerbound", "8")
	rerelease := strings.Replace(tombRow("B0TWR00009", "Towerbound, Book 6", "A Writer", "A Narrator", 0, "Towerbound", "6"),
		`"lengthMinutes":0,`, "", 1)
	for _, tc := range []struct {
		name string
		runs []string
	}{
		{"one run", []string{first + "\n" + rerelease + "\n"}},
		{"two runs", []string{first + "\n", rerelease + "\n"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dataDir := t.TempDir()
			runBooksOver(t, dataDir, seed)
			var sum Summary
			for _, run := range tc.runs {
				sum = runRecordingsOnly(t, dataDir, run, false)
			}
			if sum.MergedASINs != 1 {
				t.Errorf("MergedASINs = %d, want the re-release merged: %v", sum.MergedASINs, sum.Warnings)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// The title's position counts only with corroboration (titleCorroborated): the
// row must be the SAME PRODUCTION as one of the work's recordings. The fixtures
// are the real-data measurement's rows, verbatim where it matters.

// witchMythTree is the Yew Hollow series as the catalogue holds it: only the
// 1-3 boxset.
func witchMythTree(t *testing.T) string {
	t.Helper()
	dataDir := t.TempDir()
	seedTree(t, dataDir, map[string]string{
		"people/al/alexandria-clarke.json":  personRec("alexandria-clarke", "Alexandria Clarke"),
		"people/jo/jo-nelson.json":          personRec("jo-nelson", "Jo Nelson"),
		"people/el/elisabeth-langelee.json": personRec("elisabeth-langelee", "Elisabeth Langelee"),
		"works/wi/witch-myth-super-boxset-a-yew-hollow-cozy-mystery/work.json": workRec(
			"witch-myth-super-boxset-a-yew-hollow-cozy-mystery", "Witch Myth Super Boxset: A Yew Hollow Cozy Mystery",
			"en", `"alexandria-clarke"`, ""),
		"works/wi/witch-myth-super-boxset-a-yew-hollow-cozy-mystery/recordings/elisabeth-langelee-2017.json": recRec(
			"witch-myth-super-boxset-a-yew-hollow-cozy-mystery", "elisabeth-langelee-2017", "en", "elisabeth-langelee", "B0WMBOXSET", 1580),
		"series/ye/yew-hollow-cozy-mysteries.json": `{"id":"yew-hollow-cozy-mysteries","license":"CC0-1.0",` +
			`"name":"Yew Hollow Cozy Mysteries","sources":[{"type":"user"}],` +
			`"works":[{"position":"1-3","work":"witch-myth-super-boxset-a-yew-hollow-cozy-mystery"}]}`,
	})
	return dataDir
}

// witchMythRows are the three real rows, by the retailer's number: three
// different books by one narrator whose second and third titles say "Book 1"
// and "Book 2".
var witchMythRows = func() map[int]string {
	const author, narrator = `{"name":"Alexandria Clarke"}`, `{"name":"Jo Nelson"}`
	return map[int]string{
		1: libexRow{asin: "B01M1Z0PE0", title: "Witch Myth", subtitle: "A Yew Hollow Cozy Mystery", authors: author,
			narrators: narrator, minutes: 169,
			series: `{"name":"Yew Hollow Cozy Mysteries","position":"1"},{"name":"Witch Myth","position":"1"}`}.render(),
		2: libexRow{asin: "B01N3SY0AS", title: "Witch Myth", subtitle: "A Yew Hollow Cozy Mystery, Book 1", authors: author,
			narrators: narrator, minutes: 196,
			series: `{"name":"Yew Hollow Cozy Mysteries","position":"2"},{"name":"Witch Myth","position":"2"},` +
				`{"name":"A Witch Myth Cozy Mystery","position":"1"}`}.render(),
		3: libexRow{asin: "B01MXW1559", title: "Witch Myth", subtitle: "A Yew Hollow Cozy Mystery, Book 2", authors: author,
			narrators: narrator, minutes: 218,
			series: `{"name":"Yew Hollow Cozy Mysteries","position":"3"},{"name":"Witch Myth","position":"3"}`}.render(),
	}
}()

// Witch Myth is three books whatever order - or runs - its rows arrive in.
// Uncorroborated, "Book 1" (the retailer's 2) matched the 169-minute book 1 by
// its title and was skipped as its duplicate. The first corroboration rule made
// it order-dependent instead: in the order 1, 3, 2, "Book 2" was placed into
// the free slot 2, after which "Book 1"'s source slot counted as "held by
// another work" and corroborated the same wrong match. Neither 196 nor 218
// minutes is the 169-minute production, so nothing corroborates the title.
func TestTitlePositionNeedsCorroboration(t *testing.T) {
	for _, tc := range []struct {
		name string
		runs [][]int
	}{
		{"order 1 2 3", [][]int{{1, 2, 3}}},
		{"order 1 3 2", [][]int{{1, 3, 2}}},
		{"runs 1+3 then 2", [][]int{{1, 3}, {2}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dataDir := witchMythTree(t)
			works, skipped, merged := 0, 0, 0
			for _, run := range tc.runs {
				rows := make([]string, len(run))
				for i, n := range run {
					rows[i] = witchMythRows[n]
				}
				sum := runLibexOver(t, dataDir, rows...)
				works += sum.NewWorks
				skipped += sum.SkippedDuplicateIdentity
				merged += sum.MergedASINs
			}
			if works != 3 || skipped != 0 || merged != 0 {
				t.Errorf("works = %d, skipped = %d, merged = %d; want three books and nothing skipped", works, skipped, merged)
			}
			assertTreeValid(t, dataDir)
		})
	}
}

// The title's position corroborated by the SAME PRODUCTION: "Geronimo Stilton,
// Book 6" at the retailer's 3 is Edward Herrmann's 70-minute reading, and the
// catalogued #6 is Edward Herrmann's 71-minute reading - so the row is the
// catalogued #6 rather than a second record of it.
func TestTitlePositionCorroboratedByTheSameProduction(t *testing.T) {
	dataDir := t.TempDir()
	seedTree(t, dataDir, map[string]string{
		"people/ge/geronimo-stilton.json": personRec("geronimo-stilton", "Geronimo Stilton"),
		"people/ed/edward-herrmann.json":  personRec("edward-herrmann", "Edward Herrmann"),
		"works/ge/geronimo-stilton-book-3-cat-and-mouse-in-a-haunted-house/work.json": workRec(
			"geronimo-stilton-book-3-cat-and-mouse-in-a-haunted-house",
			"Geronimo Stilton Book 3: Cat and Mouse in a Haunted House", "en", `"geronimo-stilton"`, ""),
		"works/ge/geronimo-stilton-book-3-cat-and-mouse-in-a-haunted-house/recordings/geronimo-stilton-2009.json": recRec(
			"geronimo-stilton-book-3-cat-and-mouse-in-a-haunted-house", "geronimo-stilton-2009", "en",
			"geronimo-stilton", "B0GERON003", 69),
		"works/ge/geronimo-stilton-6-paws-off-cheddarface/work.json": workRec(
			"geronimo-stilton-6-paws-off-cheddarface", "Geronimo Stilton #6: Paws Off, Cheddarface!", "en",
			`"geronimo-stilton"`, ""),
		"works/ge/geronimo-stilton-6-paws-off-cheddarface/recordings/edward-herrmann-2006.json": recRec(
			"geronimo-stilton-6-paws-off-cheddarface", "edward-herrmann-2006", "en", "edward-herrmann", "B0GERON006", 71),
		"series/ge/geronimo-stilton.json": `{"id":"geronimo-stilton","license":"CC0-1.0","name":"Geronimo Stilton",` +
			`"sources":[{"type":"user"}],"works":[` +
			`{"position":"3","work":"geronimo-stilton-book-3-cat-and-mouse-in-a-haunted-house"},` +
			`{"position":"6","work":"geronimo-stilton-6-paws-off-cheddarface"}]}`,
	})
	sum := runLibexOver(t, dataDir, libexRow{asin: "B008D5E7C6", title: "Geronimo Stilton, Book 6: Paws Off, Cheddarface!",
		authors: `{"name":"Geronimo Stilton"}`, narrators: `{"name":"Edward Herrmann"}`, minutes: 70,
		series: `{"name":"Geronimo Stilton","position":"3"}`}.render())

	if sum.NewWorks != 0 {
		t.Errorf("NewWorks = %d, want 0: the row is the catalogued #6: %v", sum.NewWorks, sum.Warnings)
	}
	assertTreeValid(t, dataDir)
}

// Trial by Fire (Newpointe 911 #4) arrives as three narrations in one run. The
// second states the retailer's 5 and "Book 4" in its title, but a different
// narrator is not the same production, so nothing corroborates the title and the
// row falls back to what origin/main does with it: the source position alone, a
// second work beside #4 (and the third row, a plain 4 whose slot #4 now holds, a
// third). A duplicate work is the recoverable outcome - a wrong merge is not.
func TestTitlePositionUncorroboratedByAnotherNarration(t *testing.T) {
	dataDir := t.TempDir()
	seedTree(t, dataDir, map[string]string{
		"people/te/terri-blackstock.json": personRec("terri-blackstock", "Terri Blackstock"),
		"people/jc/j-c-howe.json":         personRec("j-c-howe", "J. C. Howe"),
		"works/li/line-of-duty/work.json": workRec("line-of-duty", "Line of Duty", "en", `"terri-blackstock"`, ""),
		"works/li/line-of-duty/recordings/j-c-howe-2010.json": recRec(
			"line-of-duty", "j-c-howe-2010", "en", "j-c-howe", "B0LINEDUTY", 631),
		"series/ne/newpointe-911.json": `{"id":"newpointe-911","license":"CC0-1.0","name":"Newpointe 911",` +
			`"sources":[{"type":"user"}],"works":[{"position":"5","work":"line-of-duty"}]}`,
	})
	const author = `{"name":"Terri Blackstock"}`
	sum := runLibexOver(t, dataDir,
		libexRow{asin: "B002V5BSGM", title: "Trial by Fire", subtitle: "Newpointe 911 Series #4", authors: author,
			narrators: `{"name":"Kris Faulkner"}`, minutes: 566, series: `{"name":"Newpointe 911","position":"4"}`}.render(),
		libexRow{asin: "B002V19RIW", title: "Trial by Fire", subtitle: "Newpointe 911 Series, Book 4", authors: author,
			narrators: `{"name":"John McDonough"}`, minutes: 628, series: `{"name":"Newpointe 911","position":"5"}`}.render(),
		libexRow{asin: "B0032CLAPC", title: "Trial by Fire", authors: author,
			narrators: `{"name":"Jay Charles"}`, minutes: 589, series: `{"name":"Newpointe 911","position":"4"}`}.render(),
	)

	if sum.NewWorks != 3 || sum.MergedASINs != 0 || sum.SkippedDuplicateIdentity != 0 {
		t.Errorf("NewWorks = %d, MergedASINs = %d, SkippedDuplicateIdentity = %d; want origin/main's three works: %v",
			sum.NewWorks, sum.MergedASINs, sum.SkippedDuplicateIdentity, sum.Warnings)
	}
	assertTreeValid(t, dataDir)
}

// Two Towerbound rows with NO runtime: nothing states a length, so nothing can
// corroborate the title, and the re-release falls back to what origin/main does -
// the first row is placed at the title's 6, the second (still at the retailer's
// 8) reads as a different volume and gets an author-suffixed work of its own.
func TestTitlePositionWithoutRuntimesFallsBack(t *testing.T) {
	row := func(asin string) string {
		return fmt.Sprintf(`{"title": "Towerbound, Book 6", "authors": ["A Writer"], "narrators": ["A Narrator"],`+
			` "series": "Towerbound", "series_position": "8", "asin": %q, "language": "en"}`, asin)
	}
	sum := runBooksOver(t, t.TempDir(),
		`{"format": "audiosilo-books", "version": 1, "books": [`+row("B0TWR00001")+","+row("B0TWR00009")+`]}`)

	if sum.NewWorks != 2 || sum.MergedASINs != 0 {
		t.Errorf("NewWorks = %d, MergedASINs = %d; want origin/main's two works: %v", sum.NewWorks, sum.MergedASINs, sum.Warnings)
	}
}

// The live client's adapter reads the record's own `series` array and nothing
// else, and reports an ASIN libex does not hold as "nothing to fill" rather than
// as an error - plenty of a personal library is not on Audible at all.
func TestLibexClientSeriesRefs(t *testing.T) {
	srv := libexFillServer(t, &libexFill{records: map[string]string{
		"B0WH000001": `{"asin":"B0WH000001","title":"Thorn Wishes Talon",
			"series":[{"name":"Warhammer 40,000","position":"3"}]}`,
	}})
	c := NewLibexClient()
	c.BaseURL = srv.URL
	c.Pause = 0

	refs, err := c.SeriesRefs(context.Background(), "B0WH000001")
	if err != nil {
		t.Fatalf("SeriesRefs: %v", err)
	}
	if len(refs) != 1 || refs[0].Name != "Warhammer 40,000" || refs[0].Position != "3" {
		t.Errorf("refs = %+v", refs)
	}

	refs, err = c.SeriesRefs(context.Background(), "B0NOTHING1")
	if err != nil || refs != nil {
		t.Errorf("an ASIN libex does not hold = (%+v, %v), want (nil, nil)", refs, err)
	}
}

// sameSeriesName is the importer's own test, not a looser one: the fill may only
// use a position libex stated for the series the row is actually claiming.
func TestSameSeriesName(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		same bool
	}{
		{a: "Warhammer 40,000", b: "warhammer 40,000", same: true},
		{a: "Mémoires", b: "mémoires", same: true},
		{a: "Warhammer 40,000", b: "Horus Heresy"},
		{a: "Warhammer 40,000", b: "Warhammer 40K"},
		// A name with no addressable slug is no series at all.
		{a: "。。。", b: "。。。"},
	} {
		if got := sameSeriesName(tc.a, tc.b); got != tc.same {
			t.Errorf("sameSeriesName(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.same)
		}
	}
}
