package importer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/testpack"
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

// estRecording renders the seeded recording at the estimated runtime; sources
// and addedAt vary the estimate test, chapters (a JSON array or "") the chapter
// rule, and opts anything else.
func estRecording(t *testing.T, addedAt, sources, chapters string, opts ...testpack.RecOpt) string {
	t.Helper()
	fields := map[string]any{"publisher": "Wolf Press", "release_date": estRelease, "sources": json.RawMessage(sources)}
	if chapters != "" {
		fields["chapters"] = json.RawMessage(chapters)
	}
	return testpack.WithFields(t, testpack.RecJSON(t, estRec, estWork,
		append([]testpack.RecOpt{testpack.WithASIN(estUSASIN), testpack.WithNarrators("bea-reader"),
			testpack.WithRuntime(estEstimate), testpack.WithRecAddedAt(addedAt)}, opts...)...), fields)
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
		// ... its UTC day: 01:00 at +02:00 on the release day is 23:00 UTC the day before.
		{"2026-09-08", "2026-09-08T01:00:00+02:00", true},
		{"2026-09-08", "2026-09-07T23:30:00-02:00", false}, // 01:30 UTC on the release day
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
		{"an offset stamp read by its UTC day", "2026-09-08", "2026-09-08T01:00:00+02:00", mirror("2026-09-08T01:00:00+02:00"), true},
		// The statements decide, not the creation stamp: a recording created after
		// release from a row captured before it (setSource dates the row).
		{"created after release from a pre-release capture", "2026-09-08", "2026-10-07", mirror("2026-09-01"), true},
		{"no dated source: added_at stands in", "2026-09-08", "2026-08-02", []model.Source{{Type: model.SourceLibexImport}}, true},
	} {
		if got := runtimeEstimated(tc.release, tc.added, tc.sources); got != tc.want {
			t.Errorf("%s: runtimeEstimated = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// The defect: a released regional row of a preorder-catalogued production is
// the same recording, and its runtime replaces the estimate.
func TestPreorderEstimateMergesAReleasedRegionalRow(t *testing.T) {
	dataDir := seedEstimateTree(t, estRecording(t, "2026-08-02", estMirrorSource("2026-08-02"), ""))
	var conflicts bytes.Buffer
	sum := runLibexWith(t, dataDir, Options{ImportDate: estRunDate, Mode: ModeCreate, Conflicts: &conflicts},
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
	dataDir := seedEstimateTree(t, estRecording(t, "2026-08-02", estMirrorSource("2026-08-02"), ""))
	sum := runLibexWith(t, dataDir, Options{ImportDate: estPreRelease, Mode: ModeCreate},
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
	released := estRecording(t, "2026-09-20", estMirrorSource("2026-09-20"), "", testpack.WithRuntime(estReleasedLen))
	dataDir := seedEstimateTree(t, released)
	// Run on 2026-09-01, so the row's 2026-09-08 release is still ahead of it.
	sum := runLibexWith(t, dataDir, Options{ImportDate: estPreRelease, Mode: ModeCreate},
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
	released := estRecording(t, "2026-09-20", estMirrorSource("2026-09-20"), "", testpack.WithRuntime(estReleasedLen))
	dataDir := seedEstimateTree(t, released)
	sum := runLibexWith(t, dataDir, Options{ImportDate: estPreRelease, Mode: ModeCreate},
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
			estRecording(t, "2026-08-02", fmt.Sprintf(`[{"imported_at":"2026-08-02","ref":%q,"type":"libex-import"},{"imported_at":"2026-08-03","type":"openaudible-import"}]`, estUSASIN), ""),
			estRow(estUKASIN, "uk", estReleasedLen, "unabridged", "")},
		{"a recording catalogued after release keeps the runtime guard",
			estRecording(t, "2026-09-20", estMirrorSource("2026-09-20"), ""),
			estRow(estUKASIN, "uk", estReleasedLen, "unabridged", "")},
		{"the abridged guard still applies to an estimate",
			estRecording(t, "2026-08-02", estMirrorSource("2026-08-02"), ""),
			estRow(estUKASIN, "uk", estReleasedLen, "abridged", "")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dataDir := seedEstimateTree(t, tc.recording)
			sum := runLibexWith(t, dataDir, Options{ImportDate: estRunDate, Mode: ModeCreate}, tc.row)
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
	dataDir := seedEstimateTree(t, estRecording(t, "2026-08-02", estMirrorSource("2026-08-02"), ""))
	var conflicts bytes.Buffer
	sum := runLibexWith(t, dataDir, Options{ImportDate: estRunDate, Mode: ModeEnrich, Conflicts: &conflicts},
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

	// The enrichment's stamp ends the estimate state although its ref (the
	// record's own ASIN) is already recorded: the corrected runtime is a
	// post-release statement now, fully guarded again.
	if _, raw := readRecordingRaw(t, dataDir); rawRuntimeEstimated(raw) {
		t.Errorf("the record is still an estimate after a post-release enrichment: %v", raw["sources"])
	}

	// A second identical run is a byte-level no-op: the value is already there.
	before := snapshotTree(t, dataDir)
	again := runLibexWith(t, dataDir, Options{ImportDate: estRunDate, Mode: ModeEnrich},
		estRow(estUSASIN, "us", estReleasedLen, "unabridged", estChapters(estReleasedLen, true)))
	if again.EstimatesReplaced != 0 || again.EnrichedRecordings != 0 {
		t.Errorf("re-run changed something: %+v", again)
	}
	assertTreeUnchanged(t, dataDir, before)

	// A later row contradicting the corrected runtime is a contradiction again,
	// not a second "correction".
	var conflicts2 bytes.Buffer
	later := runLibexWith(t, dataDir, Options{ImportDate: "2026-10-08", Mode: ModeEnrich, Conflicts: &conflicts2},
		estRow(estUSASIN, "us", 300, "unabridged", ""))
	if later.EstimatesReplaced != 0 || conflicts2.Len() == 0 {
		t.Errorf("summary = %+v, conflicts %q; want the contradicting row refused", later, conflicts2.String())
	}
	if rec := readEstRecording(t, dataDir); rec.RuntimeMin != estReleasedLen {
		t.Errorf("runtime = %d, want the corrected %d kept", rec.RuntimeMin, estReleasedLen)
	}
}

// A recorded estimate with no runtime at all is nothing to replace: a merged
// regional row never fills a runtime, estimate or not.
func TestAMergeFillsNoAbsentRuntime(t *testing.T) {
	rec := estRecording(t, "2026-08-02", estMirrorSource("2026-08-02"), "")
	var m map[string]any
	if err := json.Unmarshal([]byte(rec), &m); err != nil {
		t.Fatal(err)
	}
	delete(m, "runtime_min")
	stripped, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	dataDir := seedEstimateTree(t, string(stripped))
	sum := runLibexWith(t, dataDir, Options{ImportDate: estRunDate, Mode: ModeCreate},
		estRow(estUKASIN, "uk", estReleasedLen, "unabridged", ""))
	if sum.MergedASINs != 1 || sum.EstimatesReplaced != 0 {
		t.Fatalf("summary = %+v, want a merge and no replacement", sum)
	}
	if got := readEstRecording(t, dataDir); got.RuntimeMin != 0 {
		t.Errorf("runtime = %d, want none filled by a merge", got.RuntimeMin)
	}
}

// Outside the rule the contradiction guard is unchanged: the same rows against
// a recording catalogued after release are refused and logged.
func TestEnrichStillRefusesAContradictionOfAReleasedRecording(t *testing.T) {
	dataDir := seedEstimateTree(t, estRecording(t, "2026-09-20", estMirrorSource("2026-09-20"), ""))
	var conflicts bytes.Buffer
	sum := runLibexWith(t, dataDir, Options{ImportDate: estRunDate, Mode: ModeEnrich, Conflicts: &conflicts},
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
			dataDir := seedEstimateTree(t, estRecording(t, "2026-08-02", estMirrorSource("2026-08-02"), tc.recorded))
			// Merged through the create path, so the row is another ASIN: the
			// enrich path's fill would otherwise write the row's chapters into a
			// recording that has none.
			sum := runLibexWith(t, dataDir, Options{ImportDate: estRunDate, Mode: ModeCreate},
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
		dataDir := seedEstimateTree(t, estRecording(t, "2026-08-02", estMirrorSource("2026-08-02"), ""))
		sum := runLibexWith(t, dataDir, Options{ImportDate: estRunDate, Mode: ModeCreate}, order...)
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

// estRowCaptured is estRow as libex last wrote it on day (its updatedAt).
func estRowCaptured(asin, region string, minutes int, day string) string {
	return strings.Replace(estRow(asin, region, minutes, "unabridged", ""), `"region":`,
		fmt.Sprintf(`"updatedAt":"%sT10:00:00.123456+00:00","region":`, day), 1)
}

func TestEstimateRuntimesHaveTheirOwnBound(t *testing.T) {
	est := func(m int) runtimeEvidence { return runtimeEvidence{min: m, estimate: true} }
	known := func(m int) runtimeEvidence { return runtimeEvidence{min: m} }
	for _, tc := range []struct {
		a, b runtimeEvidence
		want bool
	}{
		{known(840), known(970), false}, // the ordinary 10% rule is unchanged
		{known(900), known(970), true},
		{est(840), known(970), true}, // A Bird Among Wolves
		{known(970), est(840), true},
		{est(840), known(1680), true}, // exactly double
		{est(840), known(1690), false},
		{est(840), known(420), true}, // exactly half
		{est(840), known(300), false},
		{est(600), est(1300), false},
		{est(840), known(0), true}, // unknown is compatible with anything
	} {
		if got := sameRuntime(tc.a, tc.b); got != tc.want {
			t.Errorf("sameRuntime(%+v, %+v) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

// A chapterless estimate keeps a runtime bound: a released row of a clearly
// different length is another production, on the merge and on enrichment.
func TestAChapterlessEstimateKeepsARuntimeBound(t *testing.T) {
	dataDir := seedEstimateTree(t, estRecording(t, "2026-08-02", estMirrorSource("2026-08-02"), ""))
	sum := runLibexWith(t, dataDir, Options{ImportDate: estRunDate, Mode: ModeCreate},
		estRow(estUKASIN, "uk", 300, "unabridged", ""))
	if sum.MergedASINs != 0 || sum.NewRecordings != 1 || sum.EstimatesReplaced != 0 {
		t.Fatalf("summary = %+v, want a distinct recording and no replacement", sum)
	}
	if rec := readEstRecording(t, dataDir); rec.RuntimeMin != estEstimate || len(rec.ASIN) != 1 {
		t.Errorf("the estimate changed: runtime %d, asins %v", rec.RuntimeMin, rec.ASIN)
	}

	dataDir = seedEstimateTree(t, estRecording(t, "2026-08-02", estMirrorSource("2026-08-02"), ""))
	var conflicts bytes.Buffer
	sum = runLibexWith(t, dataDir, Options{ImportDate: estRunDate, Mode: ModeEnrich, Conflicts: &conflicts},
		estRow(estUSASIN, "us", 300, "unabridged", ""))
	if sum.EnrichedRecordings != 0 || sum.EstimatesReplaced != 0 || conflicts.Len() == 0 {
		t.Errorf("summary = %+v, conflicts %q; want the row refused as a contradiction", sum, conflicts.String())
	}
	if rec := readEstRecording(t, dataDir); rec.RuntimeMin != estEstimate {
		t.Errorf("runtime = %d, want the estimate kept", rec.RuntimeMin)
	}
}

// "Released" is judged by the day the row was CAPTURED: a preorder row libex
// has not written since before the release, imported after it, is an estimate
// itself. It merges (both sides are estimates) but replaces nothing, and its
// stamp is dated its capture day, so the record stays an estimate for the
// released row that follows.
func TestAStalePreorderRowCorrectsNothing(t *testing.T) {
	dataDir := seedEstimateTree(t, estRecording(t, "2026-08-02", estMirrorSource("2026-08-02"), ""))
	sum := runLibexWith(t, dataDir, Options{ImportDate: estRunDate, Mode: ModeCreate},
		estRowCaptured(estUKASIN, "uk", 900, "2026-09-01"))
	if sum.MergedASINs != 1 || sum.NewRecordings != 0 || sum.EstimatesReplaced != 0 {
		t.Fatalf("summary = %+v, want a merge and no replacement", sum)
	}
	if rec := readEstRecording(t, dataDir); rec.RuntimeMin != estEstimate {
		t.Errorf("runtime = %d, want the recorded %d kept", rec.RuntimeMin, estEstimate)
	}
	_, raw := readRecordingRaw(t, dataDir)
	if !rawRuntimeEstimated(raw) {
		t.Fatalf("a stale preorder row ended the estimate: %v", raw["sources"])
	}
	if !strings.Contains(fmt.Sprint(raw["sources"]), "2026-09-01") {
		t.Errorf("the stale row's stamp is not dated its capture day: %v", raw["sources"])
	}

	// The same row on enrichment: nothing replaced, still an estimate.
	sum = runLibexWith(t, dataDir, Options{ImportDate: estRunDate, Mode: ModeEnrich},
		estRowCaptured(estUSASIN, "us", 900, "2026-09-01"))
	if sum.EstimatesReplaced != 0 {
		t.Errorf("summary = %+v, want nothing replaced", sum)
	}
	if _, raw := readRecordingRaw(t, dataDir); !rawRuntimeEstimated(raw) {
		t.Errorf("a stale enrichment row ended the estimate: %v", raw["sources"])
	}

	// A row captured after the release corrects it.
	sum = runLibexWith(t, dataDir, Options{ImportDate: estRunDate, Mode: ModeEnrich},
		estRowCaptured(estUSASIN, "us", estReleasedLen, "2026-10-05"))
	if sum.EstimatesReplaced != 1 {
		t.Errorf("summary = %+v, want the released row to replace the estimate", sum)
	}
	if rec := readEstRecording(t, dataDir); rec.RuntimeMin != estReleasedLen {
		t.Errorf("runtime = %d, want %d", rec.RuntimeMin, estReleasedLen)
	}
}

// The row arm reads the capture day too: a stale preorder row is held to the
// estimate bound against a released recording (it merges), where the same row
// captured after release is a measurement that differs (a distinct recording).
func TestAStalePreorderRowIsAnEstimateToTheMerge(t *testing.T) {
	for _, tc := range []struct {
		name       string
		row        string
		wantMerged bool
	}{
		{"captured before release", estRowCaptured(estUKASIN, "uk", 600, "2026-09-01"), true},
		{"captured after release", estRowCaptured(estUKASIN, "uk", 600, "2026-10-01"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			released := estRecording(t, "2026-09-20", estMirrorSource("2026-09-20"), "", testpack.WithRuntime(estReleasedLen))
			dataDir := seedEstimateTree(t, released)
			sum := runLibexWith(t, dataDir, Options{ImportDate: estRunDate, Mode: ModeCreate}, tc.row)
			if merged := sum.MergedASINs == 1 && sum.NewRecordings == 0; merged != tc.wantMerged {
				t.Errorf("summary = %+v, want merged %v", sum, tc.wantMerged)
			}
			if rec := readEstRecording(t, dataDir); rec.RuntimeMin != estReleasedLen {
				t.Errorf("runtime = %d, want the released %d kept", rec.RuntimeMin, estReleasedLen)
			}
		})
	}
}

// A recording CREATED after release from a stale preorder row is an estimate:
// its statement is dated the row's capture day, so a released row corrects it.
func TestARecordingCreatedFromAStaleRowIsAnEstimate(t *testing.T) {
	dataDir := seedEstimateTree(t, estRecording(t, "2026-08-02", estMirrorSource("2026-08-02"), ""))
	// A different narrator's production: a new recording under the work.
	stale := strings.Replace(estRowCaptured("B0EST00009", "us", 500, "2026-09-01"),
		`"narrators":[{"name":"Bea Reader"}]`, `"narrators":[{"name":"Cal Voice"}]`, 1)
	if sum := runLibexWith(t, dataDir, Options{ImportDate: estRunDate, Mode: ModeCreate}, stale); sum.NewRecordings != 1 {
		t.Fatalf("summary = %+v, want one new recording", sum)
	}
	var raw map[string]any
	readEntity(t, dataDir, recAddr(estWork, "cal-voice-2026"), &raw)
	if !rawRuntimeEstimated(raw) || coerceStr(raw["added_at"]) != estRunDate {
		t.Errorf("recording = added_at %v, sources %v; want an estimate created on the run day", raw["added_at"], raw["sources"])
	}
}

// The title corroboration reads an estimate with no timeline at the ordinary
// 10% rule, never at the estimate bound: volumes of one series by one narrator
// sit inside that bound routinely.
func TestTitleCorroborationReadsAnEstimateStrictly(t *testing.T) {
	narr := map[string]bool{"bea-reader": true}
	ws := &workState{recs: map[string]*recInfo{"r": {narrators: narr, known: runtimeEvidence{min: 840, estimate: true}}}}
	for _, tc := range []struct {
		row  runtimeEvidence
		want bool
	}{
		{runtimeEvidence{min: 870}, true},                 // within 10% of the estimate as stated
		{runtimeEvidence{min: estReleasedLen}, false},     // within the estimate bound only
		{runtimeEvidence{min: 870, estimate: true}, true}, // an estimate row, the same
		{runtimeEvidence{}, false},                        // unknown corroborates nothing
	} {
		if got := titleCorroborated(ws, &rowProduction{runtime: tc.row, narrators: narr}); got != tc.want {
			t.Errorf("row %+v: titleCorroborated = %v, want %v", tc.row, got, tc.want)
		}
	}
}

// Witch Myth as preorders: three books whose rows all list a release still
// ahead and carry no chapters, so every runtime is an estimate. The second and
// third (196 and 218 minutes) sit within the estimate bound of the first's 169,
// but the title arm is corroborated only at the 10% rule, so they stay three.
func TestPreorderVolumesStayApart(t *testing.T) {
	dataDir := witchMythTree(t)
	rows := make([]string, 0, 3)
	for _, n := range []int{1, 2, 3} {
		rows = append(rows, strings.Replace(witchMythRows[n], `"releaseDate":"2020-01-01 00:00:00+00"`, `"releaseDate":"2027-01-01 00:00:00+00"`, 1))
	}
	sum := runLibexOver(t, dataDir, rows...)
	if sum.NewWorks != 3 || sum.SkippedDuplicateIdentity != 0 || sum.MergedASINs != 0 {
		t.Errorf("works = %d, skipped = %d, merged = %d; want three books: %v",
			sum.NewWorks, sum.SkippedDuplicateIdentity, sum.MergedASINs, sum.Warnings)
	}
}

// Every write the estimate rule makes into a decoded record is stored in the
// decoded form, so a reader later in the same edit (enrichment's "chapters
// absent?" test, the chapter total, coerceInt) sees what a re-read would.
func TestEstimateWritesStoreTheDecodedForm(t *testing.T) {
	p := &planner{importDate: estRunDate, mirrorTier: true}
	raw := map[string]any{}
	if err := json.Unmarshal([]byte(estRecording(t, "2026-08-02", estMirrorSource("2026-08-02"), "")), &raw); err != nil {
		t.Fatal(err)
	}
	var row map[string]any
	if err := json.Unmarshal([]byte(estRow(estUKASIN, "uk", estReleasedLen, "unabridged", estChapters(estReleasedLen, true))), &row); err != nil {
		t.Fatal(err)
	}
	b := libexToBook(rawBook(row), estUKASIN, "uk", []string{"Ada Mapmaker"}, []string{"Bea Reader"}, &libexParse{})
	p.setEvidenceRuntime(&b)
	if !p.correctEstimate(raw, RecRef{Work: estWork, Rec: estRec}, b) {
		t.Fatal("the estimate was not replaced")
	}
	if _, ok := raw["runtime_min"].(float64); !ok {
		t.Errorf("runtime_min is %T, want the decoded float64", raw["runtime_min"])
	}
	chs, ok := raw["chapters"].([]any)
	if !ok || len(chs) != 17 {
		t.Fatalf("chapters is %T (%d), want the decoded []any of 17", raw["chapters"], len(chs))
	}
	if n, known := coerceInt(raw["runtime_min"]); !known || n != estReleasedLen {
		t.Errorf("coerceInt(runtime_min) = %d, %v", n, known)
	}
	if got := rawKnownRuntime(raw); got != (runtimeEvidence{min: estReleasedLen}) {
		t.Errorf("rawKnownRuntime = %+v, want the replaced runtime read through the decoded chapters", got)
	}
}

// estRowWithISBN is estRow carrying a recording ISBN, so an enrichment has a
// fact to fill and stamps the record.
func estRowWithISBN(asin, region string, minutes int) string {
	return strings.Replace(estRow(asin, region, minutes, "unabridged", ""), `"region":`, `"isbn":"9781250411396","region":`, 1)
}

// A released row stating NO runtime said nothing about the estimate: an
// enrichment of the record's own ASIN fills what it states, its stamp is
// deduplicated as it always was, and the estimate stays correctable by the next
// row that states the released runtime.
func TestAnEnrichmentStatingNoRuntimeKeepsTheEstimate(t *testing.T) {
	dataDir := seedEstimateTree(t, estRecording(t, "2026-08-02", estMirrorSource("2026-08-02"), ""))
	sum := runLibexWith(t, dataDir, Options{ImportDate: estRunDate, Mode: ModeEnrich},
		estRowWithISBN(estUSASIN, "us", 0))
	if sum.EnrichedRecordings != 1 || sum.EstimatesReplaced != 0 {
		t.Fatalf("summary = %+v, want the ISBN filled and nothing replaced", sum)
	}
	_, raw := readRecordingRaw(t, dataDir)
	if !rawRuntimeEstimated(raw) {
		t.Fatalf("a row stating no runtime ended the estimate: %v", raw["sources"])
	}
	if srcs, _ := raw["sources"].([]any); len(srcs) != 1 {
		t.Errorf("sources = %v, want the one deduplicated stamp", raw["sources"])
	}

	var conflicts bytes.Buffer
	sum = runLibexWith(t, dataDir, Options{ImportDate: estRunDate, Mode: ModeEnrich, Conflicts: &conflicts},
		estRow(estUSASIN, "us", estReleasedLen, "unabridged", ""))
	if sum.EstimatesReplaced != 1 || conflicts.Len() != 0 {
		t.Errorf("summary = %+v, conflicts %q; want the released runtime to replace the estimate", sum, conflicts.String())
	}
	if rec := readEstRecording(t, dataDir); rec.RuntimeMin != estReleasedLen {
		t.Errorf("runtime = %d, want %d", rec.RuntimeMin, estReleasedLen)
	}
}

// The gap estimate.go's header records: a merged regional row stating no
// runtime appends a NEW stamp dated after the release, so the recording reads
// as measured and the estimate is guarded from then on - a later row stating
// the released runtime is a contradiction. sources[] records when a statement
// was made, not what it stated, so nothing short of a schema change tells this
// stamp from a measurement. If this test starts failing because the estimate
// survives, the gap has been closed: update the header and CLAUDE.md.
func TestAMergeStatingNoRuntimeEndsTheEstimate(t *testing.T) {
	dataDir := seedEstimateTree(t, estRecording(t, "2026-08-02", estMirrorSource("2026-08-02"), ""))
	sum := runLibexWith(t, dataDir, Options{ImportDate: estRunDate, Mode: ModeCreate},
		estRow(estUKASIN, "uk", 0, "unabridged", ""))
	if sum.MergedASINs != 1 || sum.NewRecordings != 0 || sum.EstimatesReplaced != 0 {
		t.Fatalf("summary = %+v, want a merge and no replacement", sum)
	}
	_, raw := readRecordingRaw(t, dataDir)
	if rawRuntimeEstimated(raw) {
		t.Fatalf("the merged stamp no longer ends the estimate - the gap is closed: %v", raw["sources"])
	}

	var conflicts bytes.Buffer
	sum = runLibexWith(t, dataDir, Options{ImportDate: estRunDate, Mode: ModeEnrich, Conflicts: &conflicts},
		estRow(estUSASIN, "us", estReleasedLen, "unabridged", ""))
	if sum.EstimatesReplaced != 0 || conflicts.Len() == 0 {
		t.Errorf("summary = %+v, conflicts %q; want the released row refused against the locked estimate", sum, conflicts.String())
	}
	if rec := readEstRecording(t, dataDir); rec.RuntimeMin != estEstimate {
		t.Errorf("runtime = %d, want the estimate %d kept", rec.RuntimeMin, estEstimate)
	}
}

// A preorder ROW is the uncertain side, so it is held to the estimate bound
// against any incumbent, a user-attested one included: within the bound its
// ASIN merges into the user's recording, whose own facts never change (the
// correction runs only on a bulk-mirror-only estimate); outside it, it is a
// distinct recording.
func TestAPreorderRowAgainstAUserAttestedRecording(t *testing.T) {
	attested := estRecording(t, "2026-09-20",
		fmt.Sprintf(`[{"imported_at":"2026-09-20","ref":%q,"type":"libex-import"},{"imported_at":"2026-09-21","type":"openaudible-import"}]`, estUSASIN),
		"", testpack.WithRuntime(estReleasedLen))
	for _, tc := range []struct {
		name       string
		minutes    int
		wantMerged bool
	}{
		{"within the estimate bound", estEstimate, true},
		{"outside the estimate bound", 400, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dataDir := seedEstimateTree(t, attested)
			// Run before the 2026-09-08 release, so the row is a preorder.
			sum := runLibexWith(t, dataDir, Options{ImportDate: estPreRelease, Mode: ModeCreate},
				estRow(estUKASIN, "uk", tc.minutes, "unabridged", ""))
			if merged := sum.MergedASINs == 1 && sum.NewRecordings == 0; merged != tc.wantMerged {
				t.Fatalf("summary = %+v, want merged %v", sum, tc.wantMerged)
			}
			if sum.EstimatesReplaced != 0 || sum.AttestedRecordings != 0 {
				t.Errorf("summary = %+v, want nothing replaced or attested", sum)
			}
			rec := readEstRecording(t, dataDir)
			wantASINs := 1
			if tc.wantMerged {
				wantASINs = 2
			}
			if rec.RuntimeMin != estReleasedLen || rec.ReleaseDate != estRelease || len(rec.ASIN) != wantASINs {
				t.Errorf("recording = runtime %d, release %q, asins %v; want %d, %q and %d ASINs",
					rec.RuntimeMin, rec.ReleaseDate, rec.ASIN, estReleasedLen, estRelease, wantASINs)
			}
			if _, raw := readRecordingRaw(t, dataDir); model.BulkMirrorOnly(rawSources(raw)) {
				t.Errorf("the record lost its user attestation: %v", raw["sources"])
			}
		})
	}
}
