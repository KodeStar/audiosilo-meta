package importer

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/testpack"
	"github.com/kodestar/audiosilo-meta/pkg/check"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// The preorder-estimate fixture is A Bird Among Wolves' shape: a recording the
// bulk mirror catalogued on 2026-08-02 from a preorder listing (release date
// 2026-09-08, estimated at 840 minutes), and rows arriving after release stating
// the corrected 970.
const (
	estRunDate     = "2026-10-07" // after the release
	estPreRelease  = "2026-09-01" // before it
	estWork        = "a-bird-among-wolves"
	estRec         = "bea-reader-2026"
	estUSASIN      = "B0EST00001"
	estUKASIN      = "B0EST00002"
	estRelease     = "2026-09-08"
	estEstimate    = 840
	estReleasedLen = 970
)

var estRecAddr = recAddr(estWork, estRec)

// estChapters renders a chapter list totalling minutes (one chapter per 60
// minutes, the last one taking the remainder), as either the seeded record's
// snake_case or a libex row's camelCase.
func estChapters(minutes int, row bool) string {
	var parts []string
	start, n := 0, 0
	for left := minutes; left > 0; n++ {
		chunk := min(left, 60)
		if row {
			parts = append(parts, fmt.Sprintf(`{"title":"Chapter %d","startOffsetMs":%d,"lengthMs":%d}`, n+1, start*60000, chunk*60000))
		} else {
			parts = append(parts, fmt.Sprintf(`{"length_ms":%d,"start_ms":%d,"title":"Chapter %d"}`, chunk*60000, start*60000, n+1))
		}
		start += chunk
		left -= chunk
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// estRecording renders the seeded recording; sources and addedAt vary the
// estimate test, chapters (a JSON array or "") the chapter rule.
func estRecording(addedAt, sources, chapters string) string {
	extra := ""
	if chapters != "" {
		extra = `"chapters":` + chapters + `,`
	}
	return fmt.Sprintf(`{"added_at":%q,"asin":[{"asin":%q,"region":"us"}],%s"id":%q,"language":"en","license":"CC0-1.0","narrators":["bea-reader"],"publisher":"Wolf Press","release_date":%q,"runtime_min":%d,"sources":%s,"work":%q}`,
		addedAt, estUSASIN, extra, estRec, estRelease, estEstimate, sources, estWork)
}

func estMirrorSource(day string) string {
	return fmt.Sprintf(`[{"imported_at":%q,"ref":%q,"type":"libex-import"}]`, day, estUSASIN)
}

func seedEstimateTree(t *testing.T, recording string) string {
	t.Helper()
	dataDir := t.TempDir()
	seedTree(t, dataDir, map[string]string{
		"people/ad/ada-mapmaker.json": seedPersonAuthor,
		"people/be/bea-reader.json":   seedPersonNarrator,
		workAddr(estWork):             `{"added_at":"2026-08-02","authors":["ada-mapmaker"],"id":"a-bird-among-wolves","language":"en","license":"CC0-1.0","sources":[{"imported_at":"2026-08-02","ref":"B0EST00001","type":"libex-import"}],"title":"A Bird Among Wolves"}`,
		estRecAddr:                    recording,
	})
	return dataDir
}

// estRow is a libex row for the fixture's production.
func estRow(asin, region string, minutes int, format, chapters string) string {
	extra := ""
	if chapters != "" {
		extra = `,"chapters":` + chapters
	}
	return fmt.Sprintf(`{"asin":%q,"title":"A Bird Among Wolves","region":%q,"language":"english","bookFormat":%q,"publisher":"Wolf Press","releaseDate":"%sT00:00:00Z","lengthMinutes":%d,"authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":"Bea Reader"}]%s}`,
		asin, region, format, estRelease, minutes, extra)
}

func runEstimate(t *testing.T, dataDir, date string, mode Mode, conflicts *bytes.Buffer, rows ...string) Summary {
	t.Helper()
	opts := Options{DataDir: dataDir, ImportDate: date, Mode: mode}
	if conflicts != nil {
		opts.Conflicts = conflicts
	}
	sum, err := RunLibex(writeBooks(t, strings.Join(rows, "\n")+"\n"), opts)
	if err != nil {
		t.Fatalf("libex run: %v", err)
	}
	if res := check.Load(dataDir); !res.OK() {
		t.Fatalf("tree failed validation: %v", res.Problems)
	}
	return sum
}

func readEstRecording(t *testing.T, dataDir string) recordingFile {
	t.Helper()
	var rec recordingFile
	readEntity(t, dataDir, estRecAddr, &rec)
	return rec
}

func TestReleasedAfterComparesAtTheReleasePrecision(t *testing.T) {
	for _, tc := range []struct {
		release, day string
		want         bool
	}{
		{"2026-09-08", "2026-08-02", true},
		{"2026-09-08", "2026-09-08", false}, // released that day
		{"2026-09-08", "2026-10-07", false},
		{"2026-09", "2026-08-31", true},
		{"2026-09", "2026-09-30", false},
		{"2026", "2026-01-01", false}, // a year names no later day of itself
		{"2027", "2026-12-31", true},
		{"2026-09-08", "2026-08-02T10:00:00+02:00", true}, // a backfilled timestamp is read by its day
		{"", "2026-08-02", false},
		{"2026-09-08", "", false},
		{"soon", "2026-08-02", false},
	} {
		if got := releasedAfter(tc.release, tc.day); got != tc.want {
			t.Errorf("releasedAfter(%q, %q) = %v, want %v", tc.release, tc.day, got, tc.want)
		}
	}
}

func TestRuntimeEstimatedReadsProvenanceAndDates(t *testing.T) {
	mirror := func(days ...string) []model.Source {
		var out []model.Source
		for _, d := range days {
			out = append(out, model.Source{Type: model.SourceLibexImport, ImportedAt: d})
		}
		return out
	}
	for _, tc := range []struct {
		name    string
		release string
		added   string
		sources []model.Source
		want    bool
	}{
		{"catalogued from a preorder listing", "2026-09-08", "2026-08-02", mirror("2026-08-02"), true},
		{"no added_at: the source day decides", "2026-09-08", "", mirror("2026-08-02"), true},
		{"catalogued after release", "2026-09-08", "2026-09-20", mirror("2026-09-20"), false},
		{"a post-release statement ends the estimate", "2026-09-08", "2026-08-02", mirror("2026-08-02", "2026-10-04"), false},
		{"a user-attested record is never an estimate", "2026-09-08", "2026-08-02",
			append(mirror("2026-08-02"), model.Source{Type: "openaudible-import", ImportedAt: "2026-08-03"}), false},
		{"a year-only release is never later", "2026", "2026-08-02", mirror("2026-08-02"), false},
		{"no release date", "", "2026-08-02", mirror("2026-08-02"), false},
		{"no dated statement at all", "2026-09-08", "", []model.Source{{Type: model.SourceLibexImport}}, false},
	} {
		if got := runtimeEstimated(tc.release, tc.added, tc.sources); got != tc.want {
			t.Errorf("%s: runtimeEstimated = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// The defect: a released regional row of a preorder-catalogued production is
// the same recording, and its runtime replaces the estimate.
func TestPreorderEstimateMergesAReleasedRegionalRow(t *testing.T) {
	dataDir := seedEstimateTree(t, estRecording("2026-08-02", estMirrorSource("2026-08-02"), ""))
	var conflicts bytes.Buffer
	sum := runEstimate(t, dataDir, estRunDate, ModeCreate, &conflicts,
		estRow(estUKASIN, "uk", estReleasedLen, "unabridged", ""))

	if sum.NewWorks != 0 || sum.NewRecordings != 0 || sum.MergedASINs != 1 || sum.EstimatesReplaced != 1 {
		t.Fatalf("summary = %+v, want 0 works / 0 recordings / 1 merged / 1 estimate replaced", sum)
	}
	if recs := recSlugsOf(t, dataDir, estWork); len(recs) != 1 {
		t.Errorf("a twin recording was minted: %v", recs)
	}
	rec := readEstRecording(t, dataDir)
	if rec.RuntimeMin != estReleasedLen || len(rec.ASIN) != 2 {
		t.Errorf("recording = runtime %d, asins %v; want %d and both ASINs", rec.RuntimeMin, rec.ASIN, estReleasedLen)
	}
	if !hasNote(sum.Notes, "replaced 1 preorder-estimate runtime") || !hasNote(sum.Notes, "840 -> 970") {
		t.Errorf("the replacement is not reported: %v", sum.Notes)
	}
	if conflicts.Len() != 0 {
		t.Errorf("a corrected estimate is not a conflict: %s", conflicts.String())
	}
	// The merge's post-release stamp ends the estimate state: the runtime is a
	// post-release statement now and fully guarded again.
	_, raw := readRecordingRaw(t, dataDir)
	if rawRuntimeEstimated(raw) {
		t.Errorf("the record is still an estimate after a post-release merge: %v", raw["sources"])
	}
}

// Before release the row is a preorder too: the merge happens (both runtimes
// are unknown to it) but nothing replaces the estimate.
func TestPreorderEstimateMergesButKeepsTheRuntimeBeforeRelease(t *testing.T) {
	dataDir := seedEstimateTree(t, estRecording("2026-08-02", estMirrorSource("2026-08-02"), ""))
	sum := runEstimate(t, dataDir, estPreRelease, ModeCreate, nil,
		estRow(estUKASIN, "uk", estReleasedLen, "unabridged", ""))

	if sum.NewRecordings != 0 || sum.MergedASINs != 1 || sum.EstimatesReplaced != 0 {
		t.Fatalf("summary = %+v, want a merge and no replacement", sum)
	}
	if rec := readEstRecording(t, dataDir); rec.RuntimeMin != estEstimate {
		t.Errorf("runtime = %d, want the recorded %d kept before release", rec.RuntimeMin, estEstimate)
	}
}

// The incoming row may be the preorder: a released recording meets a bulk
// row listing a regional edition not out yet, at an estimate.
func TestAPreorderRowMergesIntoAReleasedRecording(t *testing.T) {
	released := strings.Replace(estRecording("2026-09-20", estMirrorSource("2026-09-20"), ""),
		fmt.Sprintf(`"runtime_min":%d`, estEstimate), fmt.Sprintf(`"runtime_min":%d`, estReleasedLen), 1)
	dataDir := seedEstimateTree(t, released)
	// Run on 2026-09-01, so the row's 2026-09-08 release is still ahead of it.
	sum := runEstimate(t, dataDir, estPreRelease, ModeCreate, nil,
		estRow(estUKASIN, "uk", estEstimate, "unabridged", ""))

	if sum.NewRecordings != 0 || sum.MergedASINs != 1 || sum.EstimatesReplaced != 0 {
		t.Fatalf("summary = %+v, want the preorder row merged and nothing replaced", sum)
	}
	if rec := readEstRecording(t, dataDir); rec.RuntimeMin != estReleasedLen {
		t.Errorf("runtime = %d, want the released %d kept", rec.RuntimeMin, estReleasedLen)
	}
}

// A preorder row's own chapter list stands in for its estimate, exactly as a
// recorded one does: a timeline that disagrees with the released recording is a
// different production.
func TestAPreorderRowsTimelineKeepsTheGuard(t *testing.T) {
	released := strings.Replace(estRecording("2026-09-20", estMirrorSource("2026-09-20"), ""),
		fmt.Sprintf(`"runtime_min":%d`, estEstimate), fmt.Sprintf(`"runtime_min":%d`, estReleasedLen), 1)
	dataDir := seedEstimateTree(t, released)
	sum := runEstimate(t, dataDir, estPreRelease, ModeCreate, nil,
		estRow(estUKASIN, "uk", 600, "unabridged", estChapters(600, true)))
	if sum.MergedASINs != 0 || sum.NewRecordings != 1 {
		t.Fatalf("summary = %+v, want a distinct recording", sum)
	}
}

// Every guard the rule does not lift still decides.
func TestThePreorderEstimateRuleRelaxesNothingElse(t *testing.T) {
	for _, tc := range []struct {
		name      string
		recording string
		row       string
	}{
		{"a user-attested recording keeps the runtime guard",
			estRecording("2026-08-02", fmt.Sprintf(`[{"imported_at":"2026-08-02","ref":%q,"type":"libex-import"},{"imported_at":"2026-08-03","type":"openaudible-import"}]`, estUSASIN), ""),
			estRow(estUKASIN, "uk", estReleasedLen, "unabridged", "")},
		{"a recording catalogued after release keeps the runtime guard",
			estRecording("2026-09-20", estMirrorSource("2026-09-20"), ""),
			estRow(estUKASIN, "uk", estReleasedLen, "unabridged", "")},
		{"the abridged guard still applies to an estimate",
			estRecording("2026-08-02", estMirrorSource("2026-08-02"), ""),
			estRow(estUKASIN, "uk", estReleasedLen, "abridged", "")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dataDir := seedEstimateTree(t, tc.recording)
			sum := runEstimate(t, dataDir, estRunDate, ModeCreate, nil, tc.row)
			if sum.MergedASINs != 0 || sum.NewRecordings != 1 || sum.EstimatesReplaced != 0 {
				t.Fatalf("summary = %+v, want a distinct recording and no merge", sum)
			}
			if rec := readEstRecording(t, dataDir); rec.RuntimeMin != estEstimate || len(rec.ASIN) != 1 {
				t.Errorf("the guarded recording changed: runtime %d, asins %v", rec.RuntimeMin, rec.ASIN)
			}
		})
	}
}

// --enrich over the SAME ASIN: the released runtime replaces the estimate
// instead of disqualifying the row as a contradiction.
func TestEnrichReplacesAPreorderEstimate(t *testing.T) {
	dataDir := seedEstimateTree(t, estRecording("2026-08-02", estMirrorSource("2026-08-02"), ""))
	var conflicts bytes.Buffer
	sum := runEstimate(t, dataDir, estRunDate, ModeEnrich, &conflicts,
		estRow(estUSASIN, "us", estReleasedLen, "unabridged", estChapters(estReleasedLen, true)))

	if sum.EnrichedRecordings != 1 || sum.EstimatesReplaced != 1 {
		t.Fatalf("summary = %+v, want 1 enriched recording / 1 estimate replaced", sum)
	}
	if hasWarning(sum.Warnings, "conflicts with the recorded") || conflicts.Len() != 0 {
		t.Errorf("a corrected estimate was reported as a contradiction: %v / %s", sum.Warnings, conflicts.String())
	}
	rec := readEstRecording(t, dataDir)
	if rec.RuntimeMin != estReleasedLen || len(rec.Chapters) != 17 {
		t.Errorf("recording = runtime %d, %d chapters; want %d and the row's 17", rec.RuntimeMin, len(rec.Chapters), estReleasedLen)
	}

	// A second identical run is a byte-level no-op: the value is already there.
	before := snapshotTree(t, dataDir)
	again := runEstimate(t, dataDir, estRunDate, ModeEnrich, nil,
		estRow(estUSASIN, "us", estReleasedLen, "unabridged", estChapters(estReleasedLen, true)))
	if again.EstimatesReplaced != 0 || again.EnrichedRecordings != 0 {
		t.Errorf("re-run changed something: %+v", again)
	}
	assertTreeUnchanged(t, dataDir, before)
}

// Outside the rule the contradiction guard is unchanged: the same rows against
// a recording catalogued after release are refused and logged.
func TestEnrichStillRefusesAContradictionOfAReleasedRecording(t *testing.T) {
	dataDir := seedEstimateTree(t, estRecording("2026-09-20", estMirrorSource("2026-09-20"), ""))
	var conflicts bytes.Buffer
	sum := runEstimate(t, dataDir, estRunDate, ModeEnrich, &conflicts,
		estRow(estUSASIN, "us", estReleasedLen, "unabridged", ""))
	if sum.EnrichedRecordings != 0 || sum.EstimatesReplaced != 0 || conflicts.Len() == 0 {
		t.Errorf("summary = %+v, conflicts %q; want the row refused and logged", sum, conflicts.String())
	}
}

// The chapter timeline is the measured audio, so it stands in for an estimate
// in the merge comparison and is the one fact that can withhold a replacement.
func TestTheChapterTimelineDecidesAnEstimate(t *testing.T) {
	for _, tc := range []struct {
		name         string
		recorded     string // the seeded chapter list, "" for none
		rowMinutes   int
		rowChapters  string
		wantMerged   bool
		wantRuntime  int
		wantTimeline int // the chapter total the recording ends with, 0 for none
		wantNote     string
	}{
		{"recorded chapters agreeing with the released runtime stay",
			estChapters(estReleasedLen, false), estReleasedLen, "", true, estReleasedLen, estReleasedLen, "replaced 1 preorder-estimate"},
		{"recorded chapters agreeing with the estimate keep the guard",
			estChapters(estEstimate, false), estReleasedLen, "", false, estEstimate, estEstimate, ""},
		{"the row's own chapters replace the recorded ones",
			estChapters(estReleasedLen, false), estReleasedLen, estChapters(estReleasedLen+5, true), true, estReleasedLen, estReleasedLen + 5, "replaced 1 preorder-estimate"},
		{"a row whose chapters contradict its runtime replaces nothing",
			"", estReleasedLen, estChapters(estEstimate, true), true, estEstimate, 0, "kept 1 preorder-estimate"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dataDir := seedEstimateTree(t, estRecording("2026-08-02", estMirrorSource("2026-08-02"), tc.recorded))
			// Merged through the create path, so the row is another ASIN: the
			// enrich path's fill would otherwise write the row's chapters into a
			// recording that has none.
			sum := runEstimate(t, dataDir, estRunDate, ModeCreate, nil,
				estRow(estUKASIN, "uk", tc.rowMinutes, "unabridged", tc.rowChapters))
			if merged := sum.MergedASINs == 1 && sum.NewRecordings == 0; merged != tc.wantMerged {
				t.Fatalf("summary = %+v, want merged %v", sum, tc.wantMerged)
			}
			rec := readEstRecording(t, dataDir)
			var ms int64
			for _, c := range rec.Chapters {
				ms += c.LengthMS
			}
			if rec.RuntimeMin != tc.wantRuntime || int(ms/60000) != tc.wantTimeline {
				t.Errorf("recording = runtime %d, chapters %d min; want %d, %d", rec.RuntimeMin, ms/60000, tc.wantRuntime, tc.wantTimeline)
			}
			if tc.wantNote != "" && !hasNote(sum.Notes, tc.wantNote) {
				t.Errorf("notes = %v, want one saying %q", sum.Notes, tc.wantNote)
			}
		})
	}
}

// The same input writes the same bytes, and the order two regional rows arrive
// in changes no decision: both merge, the estimate is replaced once, by the
// first, and the second meets a post-release record it agrees with.
func TestPreorderEstimateCorrectionIsDeterministic(t *testing.T) {
	rows := []string{
		estRow(estUKASIN, "uk", estReleasedLen, "unabridged", ""),
		estRow("B0EST00003", "au", estReleasedLen+5, "unabridged", ""),
	}
	run := func(order ...string) (map[string]string, recordingFile) {
		dataDir := seedEstimateTree(t, estRecording("2026-08-02", estMirrorSource("2026-08-02"), ""))
		sum := runEstimate(t, dataDir, estRunDate, ModeCreate, nil, order...)
		if sum.MergedASINs != 2 || sum.NewRecordings != 0 || sum.EstimatesReplaced != 1 {
			t.Fatalf("summary = %+v, want both rows merged and one replacement", sum)
		}
		return testpack.Snapshot(t, dataDir), readEstRecording(t, dataDir)
	}
	first, rec := run(rows...)
	again, _ := run(rows...)
	if !reflect.DeepEqual(first, again) {
		t.Errorf("two runs of one input wrote different trees")
	}
	_, reversed := run(rows[1], rows[0])
	if rec.RuntimeMin != estReleasedLen || reversed.RuntimeMin != estReleasedLen+5 {
		t.Errorf("runtimes = %d / %d, want the first released row's in each order", rec.RuntimeMin, reversed.RuntimeMin)
	}
	if len(rec.ASIN) != 3 || len(reversed.ASIN) != 3 {
		t.Errorf("asins = %v / %v, want all three in either order", rec.ASIN, reversed.ASIN)
	}
}

// readRecordingRaw returns the decoded raw recording of the fixture.
func readRecordingRaw(t *testing.T, dataDir string) (string, map[string]any) {
	t.Helper()
	var raw map[string]any
	readEntity(t, dataDir, estRecAddr, &raw)
	return estRecAddr, raw
}
