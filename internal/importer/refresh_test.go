package importer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/testpack"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// The refresh fixture is the estimate fixture (estimate_test.go) with a
// placeholder cover: a recording the mirror catalogued on 2026-08-02 from a
// preorder listing released 2026-09-08 at an estimated 840 minutes, met on
// 2026-10-07 by rows libex last wrote on 2026-10-05.
const (
	refreshOldCover = "https://m.media-amazon.com/images/I/placeholder.jpg"
	refreshNewCover = "https://m.media-amazon.com/images/I/released.jpg"
	refreshCaptured = "2026-10-05T10:00:00.123456+00:00"
)

// refreshSeed seeds the preorder-era recording with its placeholder cover;
// chapters is a recorded chapter list ("" for none), sources its provenance.
func refreshSeed(t *testing.T, sources, chapters string) string {
	t.Helper()
	return seedEstimateTree(t, testpack.WithField(t, estRecording(t, "2026-08-02", sources, chapters), "cover_url", refreshOldCover))
}

// refreshRow is a RELEASED libex row of the fixture's own ASIN - the released
// runtime, a new cover, a chapter list agreeing with the runtime, productState
// AVAILABLE, written after the release - with fields overridden (a nil value
// drops the field).
func refreshRow(t *testing.T, overrides map[string]any) string {
	t.Helper()
	row := map[string]any{
		"asin": estUSASIN, "title": "A Bird Among Wolves", "region": "us", "language": "english",
		"bookFormat": "unabridged", "publisher": "Wolf Press",
		"releaseDate": estRelease + "T00:00:00+00:00", "lengthMinutes": estReleasedLen,
		"authors":   []any{map[string]any{"name": "Ada Mapmaker"}},
		"narrators": []any{map[string]any{"name": "Bea Reader"}},
		"imageUrl":  refreshNewCover, "updatedAt": refreshCaptured, "productState": "AVAILABLE",
		"chapters": json.RawMessage(estChapters(estReleasedLen, true)),
	}
	for k, v := range overrides {
		if v == nil {
			delete(row, k)
		} else {
			row[k] = v
		}
	}
	out, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func refreshEnrich(t *testing.T, dataDir string, conflicts *bytes.Buffer, rows ...string) Summary {
	t.Helper()
	opts := Options{ImportDate: estRunDate, Mode: ModeEnrich}
	if conflicts != nil {
		opts.Conflicts = conflicts
	}
	return runLibexWith(t, dataDir, opts, rows...)
}

func chapterTotal(rec recordingFile) int {
	var ms int64
	for _, c := range rec.Chapters {
		ms += c.LengthMS
	}
	return msToMinutes(ms)
}

// A released row of the recording's own ASIN replaces the runtime, the cover,
// the chapters and a moved release date, and nothing else; its stamp ends the
// preorder era, so the same row again is a byte-level no-op and a later row is
// ordinary fill-absent enrichment.
func TestARefreshReplacesTheReleasedFacts(t *testing.T) {
	dataDir := refreshSeed(t, estMirrorSource("2026-08-02"), "")
	var conflicts bytes.Buffer
	row := refreshRow(t, map[string]any{"releaseDate": "2026-09-15T00:00:00+00:00", "publisher": "Another Imprint"})
	sum := refreshEnrich(t, dataDir, &conflicts, row)

	if sum.RefreshedRecordings != 1 || sum.RefreshedRuntimes != 1 || sum.RefreshedCovers != 1 ||
		sum.RefreshedChapters != 1 || sum.RefreshedReleaseDates != 1 || sum.EnrichedRecordings != 1 || sum.EstimatesReplaced != 0 {
		t.Fatalf("summary = %+v, want one refresh replacing all four fields", sum)
	}
	if conflicts.Len() != 0 || hasWarning(sum.Warnings, "conflicts with the recorded") {
		t.Errorf("a refresh was reported as a contradiction: %v / %s", sum.Warnings, conflicts.String())
	}
	if !hasNote(sum.Notes, "refreshed 1 released preorder recording") {
		t.Errorf("notes = %v, want the refresh note", sum.Notes)
	}
	rec := readEstRecording(t, dataDir)
	if rec.RuntimeMin != estReleasedLen || rec.CoverURL != refreshNewCover || chapterTotal(rec) != estReleasedLen || rec.ReleaseDate != "2026-09-15" {
		t.Errorf("recording = runtime %d, cover %s, chapters %d min, released %s; want the row's", rec.RuntimeMin, rec.CoverURL, chapterTotal(rec), rec.ReleaseDate)
	}
	// Everything else stays as enrichment leaves it: the recorded publisher wins.
	if rec.Publisher != "Wolf Press" || len(rec.Narrators) != 1 || rec.Narrators[0] != "bea-reader" || len(rec.ASIN) != 1 {
		t.Errorf("recording = publisher %q, narrators %v, asins %v; want them untouched", rec.Publisher, rec.Narrators, rec.ASIN)
	}
	if _, raw := readRecordingRaw(t, dataDir); rawRuntimeEstimated(raw) {
		t.Fatalf("a refreshed record is still preorder-era: %v", raw["sources"])
	}

	before := snapshotTree(t, dataDir)
	again := refreshEnrich(t, dataDir, nil, row)
	if again.RefreshedRecordings != 0 || again.EnrichedRecordings != 0 {
		t.Errorf("re-run = %+v, want nothing refreshed a second time", again)
	}
	assertTreeUnchanged(t, dataDir, before)

	// A later row is ordinary enrichment: a recorded cover is never replaced.
	later := refreshEnrich(t, dataDir, nil, refreshRow(t, map[string]any{
		"imageUrl": "https://m.media-amazon.com/images/I/later.jpg", "releaseDate": "2026-09-15T00:00:00+00:00"}))
	if later.RefreshedRecordings != 0 {
		t.Errorf("summary = %+v, want no second refresh", later)
	}
	if got := readEstRecording(t, dataDir).CoverURL; got != refreshNewCover {
		t.Errorf("cover = %s, want the refreshed one kept", got)
	}
}

// A released row confirming every recorded value still refreshes: its stamp is
// the post-release statement, so the record leaves the preorder era instead of
// being selected again on every run.
func TestARefreshConfirmingEveryValueEndsThePreorderEra(t *testing.T) {
	dataDir := refreshSeed(t, estMirrorSource("2026-08-02"), estChapters(estEstimate, false))
	sum := refreshEnrich(t, dataDir, nil, refreshRow(t, map[string]any{
		"lengthMinutes": estEstimate, "imageUrl": refreshOldCover, "chapters": json.RawMessage(estChapters(estEstimate, true)),
	}))
	if sum.RefreshedRecordings != 1 || sum.EnrichedRecordings != 1 ||
		sum.RefreshedRuntimes+sum.RefreshedCovers+sum.RefreshedChapters+sum.RefreshedReleaseDates != 0 {
		t.Fatalf("summary = %+v, want one refresh replacing nothing", sum)
	}
	if _, raw := readRecordingRaw(t, dataDir); rawRuntimeEstimated(raw) {
		t.Errorf("a confirming refresh left the record preorder-era: %v", raw["sources"])
	}
}

// Each field, allowed and denied.
func TestARefreshReplacesEachFieldOnlyOnItsOwnRule(t *testing.T) {
	malformed := json.RawMessage(`[{"title":"One","startOffsetMs":5000,"lengthMs":60000}]`)
	for _, tc := range []struct {
		name      string
		recorded  string // the seeded chapter list, "" for none
		row       map[string]any
		refreshed bool
		conflict  bool
		runtime   int
		cover     string
		chapters  int // the chapter total the recording ends with, 0 for none
		release   string
	}{
		{"runtime: the own ASIN's runtime replaces beyond the merge's estimate bound",
			"", map[string]any{"lengthMinutes": 1900, "chapters": nil, "imageUrl": refreshOldCover},
			true, false, 1900, refreshOldCover, 0, estRelease},
		{"runtime: under a fifth of the estimate is broken, and refused as a contradiction",
			"", map[string]any{"lengthMinutes": 150, "chapters": nil},
			false, true, estEstimate, refreshOldCover, 0, estRelease},
		{"runtime: a recorded chapter list refuting the stated runtime withholds the refresh",
			estChapters(estEstimate, false), map[string]any{"chapters": nil},
			false, true, estEstimate, refreshOldCover, estEstimate, estRelease},
		{"cover: a new https cover replaces the placeholder",
			"", map[string]any{"lengthMinutes": estEstimate, "chapters": nil},
			true, false, estEstimate, refreshNewCover, 0, estRelease},
		{"cover: an http cover is no cover, and the placeholder stays",
			"", map[string]any{"imageUrl": "http://m.media-amazon.com/images/I/released.jpg"},
			true, false, estReleasedLen, refreshOldCover, estReleasedLen, estRelease},
		{"chapters: the row's list replaces a recorded one",
			estChapters(estEstimate, false), map[string]any{"lengthMinutes": 845, "chapters": json.RawMessage(estChapters(845, true))},
			true, false, 845, refreshNewCover, 845, estRelease},
		{"chapters: a malformed list replaces nothing",
			estChapters(estEstimate, false), map[string]any{"lengthMinutes": 845, "chapters": malformed},
			true, false, 845, refreshNewCover, estEstimate, estRelease},
		{"chapters: a list contradicting its own runtime withholds the refresh",
			"", map[string]any{"chapters": json.RawMessage(estChapters(estEstimate, true))},
			false, false, estEstimate, refreshOldCover, estEstimate, estRelease},
		{"release date: a moved full date replaces the recorded one",
			"", map[string]any{"releaseDate": "2026-09-01T00:00:00+00:00"},
			true, false, estReleasedLen, refreshNewCover, estReleasedLen, "2026-09-01"},
		{"release date: a year-only row says too little to refresh, and fills only what is absent",
			"", map[string]any{"releaseDate": "2026"},
			false, false, estEstimate, refreshOldCover, estReleasedLen, estRelease},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dataDir := refreshSeed(t, estMirrorSource("2026-08-02"), tc.recorded)
			var conflicts bytes.Buffer
			sum := refreshEnrich(t, dataDir, &conflicts, refreshRow(t, tc.row))
			if got := sum.RefreshedRecordings == 1; got != tc.refreshed {
				t.Errorf("refreshed = %v, want %v (summary %+v)", got, tc.refreshed, sum)
			}
			if got := conflicts.Len() > 0; got != tc.conflict {
				t.Errorf("conflict = %v, want %v (%s)", got, tc.conflict, conflicts.String())
			}
			rec := readEstRecording(t, dataDir)
			if rec.RuntimeMin != tc.runtime || rec.CoverURL != tc.cover || chapterTotal(rec) != tc.chapters || rec.ReleaseDate != tc.release {
				t.Errorf("recording = runtime %d, cover %s, chapters %d min, released %s; want %d, %s, %d, %s",
					rec.RuntimeMin, rec.CoverURL, chapterTotal(rec), rec.ReleaseDate, tc.runtime, tc.cover, tc.chapters, tc.release)
			}
		})
	}
}

// The predicate, denied: a record a non-mirror source attested, a row that is
// not released (captured before its release date, or still for preorder), a row
// of an ASIN the recording does not carry, and a record catalogued after its
// release. Each is ordinary enrichment, so the placeholder cover - which
// fill-absent never replaces - is the witness that no refresh ran.
func TestARefreshNeedsAPreorderEraRecordAndAReleasedRowOfItsOwnASIN(t *testing.T) {
	attested := `[{"imported_at":"2026-08-02","ref":"B0EST00001","type":"libex-import"},{"imported_at":"2026-08-05","ref":"library.json","type":"user"}]`
	for _, tc := range []struct {
		name    string
		sources string
		row     map[string]any
	}{
		{"a user-attested record", attested, map[string]any{"lengthMinutes": 870, "chapters": nil}},
		{"a row captured before its release", estMirrorSource("2026-08-02"), map[string]any{"updatedAt": "2026-09-01T10:00:00+00:00", "chapters": nil}},
		{"a row still for preorder past its date", estMirrorSource("2026-08-02"), map[string]any{"productState": "AVAILABLE_FOR_PREORDER", "chapters": nil}},
		{"a row of an ASIN the catalogue does not hold", estMirrorSource("2026-08-02"), map[string]any{"asin": "B0EST00009"}},
		{"a record catalogued after its release", estMirrorSource("2026-09-20"), map[string]any{"lengthMinutes": 870, "chapters": nil}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dataDir := refreshSeed(t, tc.sources, "")
			sum := refreshEnrich(t, dataDir, nil, refreshRow(t, tc.row))
			if sum.RefreshedRecordings != 0 {
				t.Errorf("summary = %+v, want no refresh", sum)
			}
			if rec := readEstRecording(t, dataDir); rec.CoverURL != refreshOldCover || rec.RuntimeMin != estEstimate {
				t.Errorf("recording = cover %s, runtime %d; want the recorded %s and %d", rec.CoverURL, rec.RuntimeMin, refreshOldCover, estEstimate)
			}
		})
	}
}

// A released row of ANOTHER ASIN merged into the recording is the ASIN merge,
// not a refresh: its runtime corrects the estimate within the estimate bound
// (correctEstimate), and the cover, the chapters and the release date are not
// its to replace.
func TestAMergedRegionalRowRefreshesNothing(t *testing.T) {
	dataDir := refreshSeed(t, estMirrorSource("2026-08-02"), "")
	sum := runLibexWith(t, dataDir, Options{ImportDate: estRunDate, Mode: ModeCreate}, refreshRow(t, map[string]any{
		"asin": estUKASIN, "region": "uk", "releaseDate": "2026-09-15T00:00:00+00:00", "chapters": nil}))
	if sum.MergedASINs != 1 || sum.EstimatesReplaced != 1 || sum.RefreshedRecordings != 0 {
		t.Fatalf("summary = %+v, want a merge correcting the estimate and no refresh", sum)
	}
	rec := readEstRecording(t, dataDir)
	if rec.RuntimeMin != estReleasedLen || rec.CoverURL != refreshOldCover || rec.ReleaseDate != estRelease {
		t.Errorf("recording = runtime %d, cover %s, released %s; want the runtime corrected and the rest kept", rec.RuntimeMin, rec.CoverURL, rec.ReleaseDate)
	}
}

func TestSelectRefreshTargetsPicksReleasedPreorderEraRecordings(t *testing.T) {
	mirror := func(day string) []model.Source {
		return []model.Source{{Type: model.SourceLibexImport, ImportedAt: day}}
	}
	rec := func(id, asin, release string, sources []model.Source) *model.Recording {
		r := &model.Recording{ID: id, ReleaseDate: release, Sources: sources}
		if asin != "" {
			r.ASIN = []model.ASIN{{Region: "us", ASIN: asin}, {Region: "uk", ASIN: "B9" + asin}}
		}
		return r
	}
	cat := &model.Catalog{Works: []*model.Work{{ID: "w", Recordings: []*model.Recording{
		rec("released", "B2", "2026-09-08", mirror("2026-08-02")),
		rec("released-today", "B1", "2026-10-07", mirror("2026-08-02")),
		rec("still-preorder", "B3", "2026-11-01", mirror("2026-08-02")),
		rec("catalogued-after-release", "B4", "2026-09-08", mirror("2026-09-20")),
		rec("attested", "B5", "2026-09-08", append(mirror("2026-08-02"), model.Source{Type: model.SourceUser, ImportedAt: "2026-08-05"})),
		rec("no-asin", "", "2026-09-08", mirror("2026-08-02")),
	}}}}
	got := SelectRefreshTargets(cat, "2026-10-07")
	if len(got) != 2 || got[0].Recording != "released-today" || got[1].Recording != "released" {
		t.Fatalf("targets = %+v, want released-today then released (by ASIN)", got)
	}
	if got[1].ASIN != "B2" || !got[1].NeedChapters {
		t.Errorf("target = %+v, want its first ASIN, asking for chapters", got[1])
	}
}

// End to end, as `libex-fill --released-preorders` runs it: the selection, the
// mirror record and its chapters payload, and the enrichment that refreshes.
func TestReleasedPreordersAreRefreshedFromTheMirror(t *testing.T) {
	dataDir := refreshSeed(t, estMirrorSource("2026-08-02"), "")
	record := refreshRow(t, map[string]any{"chapters": nil, "description": "dropped"})
	payload := `{"isAccurate":true,"runtimeLengthMs":58200000,"chapters":` + estChapters(estReleasedLen, true) + `}`
	f := &libexFill{records: map[string]string{estUSASIN: record}, chapters: map[string]string{estUSASIN: payload}}
	srv := libexFillServer(t, f)

	cat, err := LoadCatalogForFill(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	targets := SelectRefreshTargets(cat, estRunDate)
	if len(targets) != 1 {
		t.Fatalf("targets = %+v, want the one released preorder", targets)
	}
	var rows bytes.Buffer
	rep, err := fillClient(t, srv).FetchRows(context.Background(), targets, &rows)
	if err != nil || rep.Fetched != 1 || rep.ChaptersFetched != 1 {
		t.Fatalf("fetch = %+v, %v; want the record and its chapters", rep, err)
	}
	if !f.saw("/db/book/" + estUSASIN) {
		t.Errorf("the mirror was never asked; saw %v", f.paths())
	}
	rowsPath := filepath.Join(t.TempDir(), "rows.ndjson")
	if err := os.WriteFile(rowsPath, rows.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	sum, err := RunLibex(rowsPath, Options{DataDir: dataDir, ImportDate: estRunDate, Mode: ModeEnrich})
	if err != nil {
		t.Fatal(err)
	}
	if sum.RefreshedRecordings != 1 || sum.RefreshedCovers != 1 || sum.RefreshedChapters != 1 {
		t.Fatalf("summary = %+v, want the refresh", sum)
	}
	cat, err = LoadCatalogForFill(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(SelectRefreshTargets(cat, estRunDate)); n != 0 {
		t.Errorf("%d targets after the refresh, want none: a refreshed record is not selected again", n)
	}
}

func TestAwaitingRefreshReadsTheFirstStatementAndTheRestatement(t *testing.T) {
	src := func(typ, ref, day string) model.Source { return model.Source{Type: typ, Ref: ref, ImportedAt: day} }
	mirror := model.SourceLibexImport
	for _, tc := range []struct {
		name    string
		added   string
		sources []model.Source
		want    bool
	}{
		{"catalogued from the preorder listing", "", []model.Source{src(mirror, "B1", "2026-08-02")}, true},
		{"a regional ASIN merged after the release ends nothing", "",
			[]model.Source{src(mirror, "B1", "2026-08-02"), src(mirror, "B2", "2026-10-06")}, true},
		{"the first ASIN restated after the release", "",
			[]model.Source{src(mirror, "B1", "2026-08-02"), src(mirror, "B1", "2026-10-06")}, false},
		{"a merged ref restated after the release ends nothing either", "",
			[]model.Source{src(mirror, "B1", "2026-08-02"), src(mirror, "B2", "2026-10-06"), src(mirror, "B2", "2026-10-07")}, true},
		{"the first ASIN stated after the release under a later ref order", "",
			[]model.Source{src(mirror, "B2", "2026-08-02"), src(mirror, "B1", "2026-10-06")}, false},
		{"a ref recorded twice before the release is no restatement", "",
			[]model.Source{src(mirror, "B1", "2026-08-02"), src(mirror, "B1", "2026-09-01")}, true},
		{"first stated after the release", "", []model.Source{src(mirror, "B1", "2026-09-20")}, false},
		{"a user-library source", "", []model.Source{src(mirror, "B1", "2026-08-02"), src(model.SourceUser, "form", "2026-08-03")}, false},
		{"undated sources fall back to added_at", "2026-08-02", []model.Source{src(mirror, "B1", "")}, true},
		{"undated sources and a later added_at", "2026-09-20", []model.Source{src(mirror, "B1", "")}, false},
	} {
		if got := awaitingRefresh(estRelease, tc.added, "B1", tc.sources); got != tc.want {
			t.Errorf("%s: awaitingRefresh = %v, want %v", tc.name, got, tc.want)
		}
	}
	if awaitingRefresh(estRelease, "", "", []model.Source{src(mirror, "B1", "2026-08-02")}) {
		t.Errorf("a recording with no ASIN awaits a refresh nothing can deliver")
	}
}

// Only a row of the recording's FIRST ASIN refreshes it: a regional edition
// merged since is a separate listing (its own cover, and possibly a mis-merge
// a 1/5 floor would not catch), so its released row is ordinary enrichment and
// ends nothing; the first ASIN's released row then refreshes and ends the era.
func TestOnlyTheFirstASINRefreshes(t *testing.T) {
	sources := `[{"imported_at":"2026-08-02","ref":"B0EST00001","type":"libex-import"},{"imported_at":"2026-09-20","ref":"B0EST00002","type":"libex-import"}]`
	rec := testpack.WithField(t, estRecording(t, "2026-08-02", sources, ""), "asin",
		[]map[string]string{{"asin": estUSASIN, "region": "us"}, {"asin": estUKASIN, "region": "uk"}})
	dataDir := seedEstimateTree(t, testpack.WithField(t, rec, "cover_url", refreshOldCover))

	sum := refreshEnrich(t, dataDir, nil, refreshRow(t, map[string]any{"asin": estUKASIN, "region": "uk", "chapters": nil}))
	if sum.RefreshedRecordings != 0 {
		t.Fatalf("summary = %+v, want a merged ASIN's released row not to refresh", sum)
	}
	got := readEstRecording(t, dataDir)
	if got.CoverURL != refreshOldCover || got.RuntimeMin != estEstimate {
		t.Errorf("recording = cover %s, runtime %d; want neither replaced by the merged ASIN", got.CoverURL, got.RuntimeMin)
	}
	if _, raw := readRecordingRaw(t, dataDir); !rawAwaitingRefresh(raw) {
		t.Fatalf("a merged ASIN's row ended the preorder era: %v", raw["sources"])
	}

	sum = refreshEnrich(t, dataDir, nil, refreshRow(t, nil))
	if sum.RefreshedRecordings != 1 || sum.RefreshedCovers != 1 || sum.RefreshedRuntimes != 1 {
		t.Fatalf("summary = %+v, want the first ASIN's released row to refresh", sum)
	}
	if _, raw := readRecordingRaw(t, dataDir); rawAwaitingRefresh(raw) {
		t.Errorf("the refresh did not end the preorder era: %v", raw["sources"])
	}
}

// A refresh never trades a recorded chapter table for a degenerate one - a
// single chapter, or under half as many - while its other fields still refresh
// and the era still ends.
func TestARefreshKeepsARecordedTableOverAThinnerOne(t *testing.T) {
	one := json.RawMessage(`[{"title":"Whole Book","startOffsetMs":0,"lengthMs":` + fmt.Sprint(estReleasedLen*60000) + `}]`)
	for _, tc := range []struct {
		name     string
		recorded string
		row      json.RawMessage
		want     int // chapters the recording ends with
	}{
		{"one chapter over a table", estChapters(estReleasedLen, false), one, 17},
		{"under half the chapters", estChapters(estReleasedLen, false), json.RawMessage(estChaptersOf(estReleasedLen, 8)), 17},
		{"half the chapters or more replaces", estChapters(estReleasedLen, false), json.RawMessage(estChaptersOf(estReleasedLen, 9)), 9},
		{"one chapter over none fills", "", one, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dataDir := refreshSeed(t, estMirrorSource("2026-08-02"), tc.recorded)
			sum := refreshEnrich(t, dataDir, nil, refreshRow(t, map[string]any{"chapters": tc.row}))
			if sum.RefreshedRecordings != 1 || sum.RefreshedCovers != 1 {
				t.Fatalf("summary = %+v, want the row's other fields refreshed", sum)
			}
			if got := readEstRecording(t, dataDir); len(got.Chapters) != tc.want || got.CoverURL != refreshNewCover {
				t.Errorf("recording = %d chapters, cover %s; want %d and the new cover", len(got.Chapters), got.CoverURL, tc.want)
			}
			if _, raw := readRecordingRaw(t, dataDir); rawAwaitingRefresh(raw) {
				t.Errorf("the refresh did not end the preorder era: %v", raw["sources"])
			}
		})
	}
}

// estChaptersOf is a row chapter list of n equal chapters totalling minutes.
func estChaptersOf(minutes, n int) string {
	var parts []string
	each := minutes * 60000 / n
	for i := 0; i < n; i++ {
		length := each
		if i == n-1 {
			length = minutes*60000 - each*(n-1)
		}
		parts = append(parts, fmt.Sprintf(`{"title":"Part %d","startOffsetMs":%d,"lengthMs":%d}`, i+1, i*each, length))
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// A MOVED PREORDER DATE: an unreleased row of the recording's own ASIN listing a
// different full date moves release_date and nothing else, and the record stays
// preorder-era (its runtime an estimate) for the released row that follows.
func TestAnUnreleasedRowMovesAPreorderDate(t *testing.T) {
	postponed := map[string]any{"releaseDate": "2026-11-20T00:00:00+00:00", "productState": "AVAILABLE_FOR_PREORDER", "chapters": nil}
	dataDir := refreshSeed(t, estMirrorSource("2026-08-02"), "")
	var conflicts bytes.Buffer
	sum := refreshEnrich(t, dataDir, &conflicts, refreshRow(t, postponed))
	if sum.MovedPreorderDates != 1 || sum.RefreshedRecordings != 0 || sum.EnrichedRecordings != 1 || conflicts.Len() != 0 {
		t.Fatalf("summary = %+v, conflicts %q; want the date moved and nothing refreshed", sum, conflicts.String())
	}
	if !hasNote(sum.Notes, "moved 1 preorder release date") {
		t.Errorf("notes = %v, want the moved-date note", sum.Notes)
	}
	rec := readEstRecording(t, dataDir)
	if rec.ReleaseDate != "2026-11-20" || rec.RuntimeMin != estEstimate || rec.CoverURL != refreshOldCover || len(rec.Chapters) != 0 {
		t.Errorf("recording = released %s, runtime %d, cover %s, %d chapters; want only the date moved", rec.ReleaseDate, rec.RuntimeMin, rec.CoverURL, len(rec.Chapters))
	}
	_, raw := readRecordingRaw(t, dataDir)
	if !rawAwaitingRefresh(raw) || !rawRuntimeEstimated(raw) {
		t.Fatalf("a moved date ended the preorder era: %v", raw["sources"])
	}

	// The released row that follows refreshes it.
	sum = runLibexWith(t, dataDir, Options{ImportDate: "2026-11-25", Mode: ModeEnrich}, refreshRow(t, map[string]any{
		"releaseDate": "2026-11-20T00:00:00+00:00", "updatedAt": "2026-11-24T10:00:00+00:00"}))
	if sum.RefreshedRecordings != 1 || sum.RefreshedRuntimes != 1 || sum.RefreshedCovers != 1 {
		t.Errorf("summary = %+v, want the released row to refresh the postponed record", sum)
	}
}

// A row released by its date but still listed for preorder is no refresh, yet
// its full date is the own ASIN's and moves the preorder-era record's.
func TestAPreorderListingPastItsDateMovesTheDateOnly(t *testing.T) {
	dataDir := refreshSeed(t, estMirrorSource("2026-08-02"), "")
	var conflicts bytes.Buffer
	sum := refreshEnrich(t, dataDir, &conflicts, refreshRow(t, map[string]any{
		"releaseDate": "2026-09-15T00:00:00+00:00", "productState": "AVAILABLE_FOR_PREORDER", "chapters": nil}))
	if sum.MovedPreorderDates != 1 || sum.RefreshedRecordings != 0 || conflicts.Len() != 0 {
		t.Fatalf("summary = %+v, conflicts %q; want the date moved and no refresh", sum, conflicts.String())
	}
	rec := readEstRecording(t, dataDir)
	if rec.ReleaseDate != "2026-09-15" || rec.RuntimeMin != estEstimate || rec.CoverURL != refreshOldCover {
		t.Errorf("recording = released %s, runtime %d, cover %s; want only the date moved", rec.ReleaseDate, rec.RuntimeMin, rec.CoverURL)
	}
	if _, raw := readRecordingRaw(t, dataDir); !rawAwaitingRefresh(raw) {
		t.Errorf("a row that refreshed nothing ended the preorder era: %v", raw["sources"])
	}
}

func TestAMovedPreorderDateNeedsAPreorderEraRecordAndAFullDate(t *testing.T) {
	attested := `[{"imported_at":"2026-08-02","ref":"B0EST00001","type":"libex-import"},{"imported_at":"2026-08-05","ref":"library.json","type":"user"}]`
	for _, tc := range []struct {
		name, sources, release string
		minutes                int
		conflict               bool
	}{
		{"a user-attested record keeps its date and refuses the row", attested, "2026-11-20T00:00:00+00:00", estEstimate, true},
		// The date is estimate evidence, so no contradiction - and no full date to take.
		{"a coarser date keeps the extend-only rule", estMirrorSource("2026-08-02"), "2026-11", estEstimate, false},
		{"the runtime half of the guard still refuses the row", estMirrorSource("2026-08-02"), "2026-11-20T00:00:00+00:00", 2000, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dataDir := refreshSeed(t, tc.sources, "")
			var conflicts bytes.Buffer
			sum := refreshEnrich(t, dataDir, &conflicts, refreshRow(t, map[string]any{
				"releaseDate": tc.release, "lengthMinutes": tc.minutes, "chapters": nil}))
			if sum.MovedPreorderDates != 0 || (conflicts.Len() > 0) != tc.conflict {
				t.Errorf("summary = %+v, conflicts %q; want no date moved, conflict %v", sum, conflicts.String(), tc.conflict)
			}
			if rec := readEstRecording(t, dataDir); rec.ReleaseDate != estRelease {
				t.Errorf("release date = %s, want the recorded %s", rec.ReleaseDate, estRelease)
			}
		})
	}
}

// A released row whose stamp would be dated before its release (a run dated the
// day before the row was captured - a sync cycle crossing UTC midnight) is no
// refresh: its stamp would end nothing and be appended again on every run. Its
// date moves the record, and the run dated on or after it refreshes once.
func TestARefreshWaitsForAStampThatEndsTheEra(t *testing.T) {
	dataDir := refreshSeed(t, estMirrorSource("2026-08-02"), "")
	row := refreshRow(t, map[string]any{"releaseDate": "2026-10-08T00:00:00+00:00", "updatedAt": "2026-10-08T00:05:00+00:00"})
	for range 2 {
		sum := refreshEnrich(t, dataDir, nil, row) // dated estRunDate, 2026-10-07
		if sum.RefreshedRecordings != 0 {
			t.Fatalf("summary = %+v, want no refresh from a run dated before the release", sum)
		}
	}
	_, raw := readRecordingRaw(t, dataDir)
	if srcs, _ := raw["sources"].([]any); len(srcs) != 1 || coerceStr(raw["release_date"]) != "2026-10-08" {
		t.Fatalf("recording = released %v, sources %v; want the date moved and no stamp appended", raw["release_date"], raw["sources"])
	}

	sum := runLibexWith(t, dataDir, Options{ImportDate: "2026-10-08", Mode: ModeEnrich}, row)
	if sum.RefreshedRecordings != 1 {
		t.Fatalf("summary = %+v, want the refresh once the run reaches the release", sum)
	}
	before := snapshotTree(t, dataDir)
	if again := runLibexWith(t, dataDir, Options{ImportDate: "2026-10-09", Mode: ModeEnrich}, row); again.RefreshedRecordings != 0 {
		t.Errorf("re-run = %+v, want no second refresh", again)
	}
	assertTreeUnchanged(t, dataDir, before)
}

// The release date belongs to the listing the recording was catalogued under:
// a merged regional edition's row carries its own marketplace's date, so it
// never moves it: the guard reads the date against it, released or not, and a
// released one refreshes nothing either (TestOnlyTheFirstASINRefreshes).
func TestARegionalRowNeverMovesTheReleaseDate(t *testing.T) {
	sources := `[{"imported_at":"2026-08-02","ref":"B0EST00001","type":"libex-import"},{"imported_at":"2026-08-03","ref":"B0EST00002","type":"libex-import"}]`
	seed := func(t *testing.T) string {
		rec := testpack.WithField(t, estRecording(t, "2026-08-02", sources, ""), "asin",
			[]map[string]string{{"asin": estUSASIN, "region": "us"}, {"asin": estUKASIN, "region": "uk"}})
		return seedEstimateTree(t, testpack.WithField(t, rec, "cover_url", refreshOldCover))
	}

	dataDir := seed(t)
	var conflicts bytes.Buffer
	sum := runLibexWith(t, dataDir, Options{ImportDate: "2026-08-10", Mode: ModeEnrich, Conflicts: &conflicts}, refreshRow(t, map[string]any{
		"asin": estUKASIN, "region": "uk", "releaseDate": "2026-10-01T00:00:00+00:00", "productState": "AVAILABLE_FOR_PREORDER",
		"updatedAt": "2026-08-09T00:00:00+00:00", "chapters": nil}))
	if sum.MovedPreorderDates != 0 || conflicts.Len() == 0 {
		t.Errorf("summary = %+v, conflicts %q; want the regional date refused, not moved", sum, conflicts.String())
	}
	if got := readEstRecording(t, dataDir).ReleaseDate; got != estRelease {
		t.Errorf("release date = %s, want the recorded %s", got, estRelease)
	}

	dataDir = seed(t)
	conflicts.Reset()
	sum = refreshEnrich(t, dataDir, &conflicts, refreshRow(t, map[string]any{
		"asin": estUKASIN, "region": "uk", "releaseDate": "2026-09-15T00:00:00+00:00"}))
	if sum.RefreshedRecordings != 0 || sum.MovedPreorderDates != 0 || conflicts.Len() == 0 {
		t.Fatalf("summary = %+v, conflicts %q; want the released regional row refused on its date", sum, conflicts.String())
	}
	if rec := readEstRecording(t, dataDir); rec.ReleaseDate != estRelease || rec.RuntimeMin != estEstimate || rec.CoverURL != refreshOldCover {
		t.Errorf("recording = released %s, runtime %d, cover %s; want it untouched", rec.ReleaseDate, rec.RuntimeMin, rec.CoverURL)
	}
}

// A row of the first ASIN captured before the record's newest statement of it
// (an old dump fed to --enrich after the sync bot catalogued the preorder) is
// stale: it cannot roll the date back, and the guard reads the date against it.
func TestAStaleRowNeverMovesTheReleaseDate(t *testing.T) {
	dataDir := refreshSeed(t, estMirrorSource("2026-08-02"), "")
	var conflicts bytes.Buffer
	sum := runLibexWith(t, dataDir, Options{ImportDate: "2026-08-10", Mode: ModeEnrich, Conflicts: &conflicts}, refreshRow(t, map[string]any{
		"releaseDate": "2026-08-20T00:00:00+00:00", "productState": "AVAILABLE_FOR_PREORDER",
		"updatedAt": "2026-07-01T00:00:00+00:00", "chapters": nil}))
	if sum.MovedPreorderDates != 0 || conflicts.Len() == 0 {
		t.Errorf("summary = %+v, conflicts %q; want the stale date refused, not moved", sum, conflicts.String())
	}
	if got := readEstRecording(t, dataDir).ReleaseDate; got != estRelease {
		t.Errorf("release date = %s, want the recorded %s", got, estRelease)
	}
}
