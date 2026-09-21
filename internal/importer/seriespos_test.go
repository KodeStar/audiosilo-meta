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
