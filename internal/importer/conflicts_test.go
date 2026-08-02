package importer

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The conflict-worklist tests run against the enrichment suite's seeded
// catalogue (enrich_test.go), with the recordings overridden to carry the fact
// each test's export row disagrees with. The trust-tier suite's catalogue
// (attest_test.go) is reused for the two user-library modes, because a
// contradiction there needs a bulk-mirror-only record to attest.
const (
	// The matched recording, recording a runtime the export row contradicts.
	conflictRecShortRuntime = `{"asin":[{"asin":"B0LIBEX001","region":"uk"}],"id":"bea-reader","language":"en","license":"CC0-1.0","narrators":["bea-reader"],"runtime_min":300,"sources":[{"type":"user"}],"work":"the-lost-cartographer"}`
	// The same recording, recording a release date the row contradicts instead.
	conflictRecOtherDate = `{"asin":[{"asin":"B0LIBEX001","region":"uk"}],"id":"bea-reader","language":"en","license":"CC0-1.0","narrators":["bea-reader"],"release_date":"2019-11-05","sources":[{"type":"user"}],"work":"the-lost-cartographer"}`
	// A SECOND recording of the same work, so one run can refuse one row and
	// enrich another - which is what makes "the worklist changes nothing" a claim
	// about a run that actually writes.
	conflictPersonSecond   = `{"id":"cy-speaker","license":"CC0-1.0","name":"Cy Speaker","sources":[{"type":"user"}]}`
	conflictRecSecond      = `{"asin":[{"asin":"B0LIBEX002","region":"uk"}],"id":"cy-speaker","language":"en","license":"CC0-1.0","narrators":["cy-speaker"],"sources":[{"type":"user"}],"work":"the-lost-cartographer"}`
	conflictRecSecondShort = `{"asin":[{"asin":"B0LIBEX002","region":"uk"}],"id":"cy-speaker","language":"en","license":"CC0-1.0","narrators":["cy-speaker"],"runtime_min":300,"sources":[{"type":"user"}],"work":"the-lost-cartographer"}`
)

const (
	secondPersonRel = "people/cy/cy-speaker.json"
	secondRecRel    = "works/th/the-lost-cartographer/recordings/cy-speaker.json"
)

// conflictRow renders one libex export row for the seeded work, stating a
// 600-minute runtime (which a seeded 300-minute recording contradicts, and an
// unstated one is filled from).
func conflictRow(asin, narrator string) string {
	return fmt.Sprintf(`{"asin":%q,"title":"The Lost Cartographer","region":"gb","language":"english",`+
		`"publisher":"Lost Press","imageUrl":"https://m.media-amazon.com/images/I/51libex0001.jpg",`+
		`"lengthMinutes":600,"authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":%q}]}`, asin, narrator)
}

// conflictExport wraps rows into one export document.
func conflictExport(rows ...string) string { return "[" + strings.Join(rows, ",") + "]" }

// runEnrichWithWorklist runs a libex enrichment with a conflict worklist
// attached and returns the summary plus the worklist's rows.
func runEnrichWithWorklist(t *testing.T, dataDir, export string) (Summary, []string) {
	t.Helper()
	var buf bytes.Buffer
	sum, err := RunLibex(writeBooks(t, export), Options{
		DataDir: dataDir, ImportDate: testImportDate, Mode: ModeEnrich, Conflicts: &buf,
	})
	if err != nil {
		t.Fatalf("enrich run: %v", err)
	}
	return sum, worklistLines(t, buf.String())
}

// worklistLines splits an NDJSON worklist into its rows, proving as it goes that
// every row is terminated - a half-written last line is exactly what a consumer
// must never have to tolerate.
func worklistLines(t *testing.T, raw string) []string {
	t.Helper()
	if raw == "" {
		return nil
	}
	if !strings.HasSuffix(raw, "\n") {
		t.Fatalf("worklist does not end in a newline: %q", raw)
	}
	return strings.Split(strings.TrimSuffix(raw, "\n"), "\n")
}

// wantOneRow asserts the worklist holds exactly the given row, byte for byte.
// The whole line is compared rather than field by field because the line IS the
// contract: a consumer parses these keys, and a renamed field or a stringified
// number is a breaking change that a field-by-field check would wave through.
func wantOneRow(t *testing.T, got []string, want string) {
	t.Helper()
	if len(got) != 1 {
		t.Fatalf("worklist rows = %d, want 1:\n%s", len(got), strings.Join(got, "\n"))
	}
	if got[0] != want {
		t.Errorf("worklist row:\n got %s\nwant %s", got[0], want)
	}
}

// TestEnrichConflictWorklistRecordsBothRefusedFields is the motivating case: a
// libex enrichment run refuses a row for contradicting the record its ASIN
// matched, and today that is a warning line and nothing else. The worklist is
// what makes the disagreement findable afterwards - the spider-man recording
// (stored at 81 minutes, dumped and chaptered at 492) was only ever going to be
// found by a human tripping over it.
//
// Both guarded fields are covered, because they carry different value types: a
// runtime must land as a JSON number and a date as a string, so a consumer can
// compare runtimes without parsing prose.
func TestEnrichConflictWorklistRecordsBothRefusedFields(t *testing.T) {
	for _, tc := range []struct{ name, rec, want string }{
		{
			name: "runtime",
			rec:  conflictRecShortRuntime,
			want: `{"run":"enrich","asin":"B0LIBEX001","work":"the-lost-cartographer","recording":"bea-reader",` +
				`"field":"runtime_min","recorded":300,"stated":600,"source_type":"libex-import","detected_at":"` + testImportDate + `"}`,
		},
		{
			name: "release date",
			rec:  conflictRecOtherDate,
			want: `{"run":"enrich","asin":"B0LIBEX001","work":"the-lost-cartographer","recording":"bea-reader",` +
				`"field":"release_date","recorded":"2019-11-05","stated":"2024-03-01","source_type":"libex-import","detected_at":"` + testImportDate + `"}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dataDir := seedEnrichTree(t, map[string]string{recRel: tc.rec})
			before := snapshotTree(t, dataDir)

			sum, rows := runEnrichWithWorklist(t, dataDir, fullRow)
			wantOneRow(t, rows, tc.want)
			// The run is otherwise exactly what it was: one warning, no writes.
			if len(sum.Warnings) != 1 || !strings.Contains(sum.Warnings[0], "conflicts with the recorded") {
				t.Errorf("warnings = %v, want the single contradiction line", sum.Warnings)
			}
			assertTreeUnchanged(t, dataDir, before)
		})
	}
}

// TestUserCreateConflictWorklistRecordsTheAttestScopeContradiction covers the
// other emission scope: a user-library import meeting a mirror seed it disagrees
// with. The row is refused without stamping (first writer wins), so the
// disagreement leaves no trace in the tree at all - the worklist is the only
// place it is durably recorded, and the run field names the mode that found it.
func TestUserCreateConflictWorklistRecordsTheAttestScopeContradiction(t *testing.T) {
	dataDir := seedTierTree(t, nil) // the mirror seed records "2019"
	conflicting := `[{
	  "asin": "B0LIBEX001",
	  "title_short": "The Lost Cartographer",
	  "author": "Ada Mapmaker",
	  "narrated_by": "Bea Reader",
	  "language": "english",
	  "region": "us",
	  "publisher": "Another Imprint",
	  "release_date": "2021-07-02"
	}]`

	var buf bytes.Buffer
	sum, err := Run(writeBooks(t, conflicting), Options{
		DataDir: dataDir, ImportDate: testImportDate, Conflicts: &buf,
	})
	if err != nil {
		t.Fatalf("user import: %v", err)
	}
	if sum.Conflicts != 1 {
		t.Fatalf("Conflicts = %d, want 1", sum.Conflicts)
	}
	wantOneRow(t, worklistLines(t, buf.String()),
		`{"run":"create","asin":"B0LIBEX001","work":"the-lost-cartographer","recording":"bea-reader-2019",`+
			`"field":"release_date","recorded":"2019","stated":"2021-07-02","source_type":"openaudible-import","detected_at":"`+testImportDate+`"}`)
}

// TestRecordingsOnlyConflictWorklistRecordsTheAttestScopeContradiction is the
// third mode. The guard reaches it through the same ASIN-dedup attestation the
// create path uses, so what this pins is the run LABEL: a worklist accumulated
// across a wave's several passes has to say which pass found each row, or the
// operator cannot reproduce it.
func TestRecordingsOnlyConflictWorklistRecordsTheAttestScopeContradiction(t *testing.T) {
	dataDir := seedTierTree(t, nil) // the mirror seed records 600 minutes

	var buf bytes.Buffer
	sum, err := Run(writeBooks(t, userRowConflicting), Options{
		DataDir: dataDir, ImportDate: testImportDate, Mode: ModeRecordingsOnly, Conflicts: &buf,
	})
	if err != nil {
		t.Fatalf("user import: %v", err)
	}
	if sum.Conflicts != 1 {
		t.Fatalf("Conflicts = %d, want 1", sum.Conflicts)
	}
	wantOneRow(t, worklistLines(t, buf.String()),
		`{"run":"recordings-only","asin":"B0LIBEX001","work":"the-lost-cartographer","recording":"bea-reader-2019",`+
			`"field":"runtime_min","recorded":600,"stated":900,"source_type":"openaudible-import","detected_at":"`+testImportDate+`"}`)
}

// seedTwoRecordingTree seeds the enrichment catalogue with a SECOND recording
// under the same work, so a run can refuse one row and enrich another.
func seedTwoRecordingTree(t *testing.T, first, second string) string {
	t.Helper()
	return seedEnrichTree(t, map[string]string{
		recRel:          first,
		secondPersonRel: conflictPersonSecond,
		secondRecRel:    second,
	})
}

// treeContents is snapshotTree without the modification times, so two runs in
// two different temp directories can be compared for having produced the same
// tree.
func treeContents(t *testing.T, dataDir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for rel, st := range snapshotTree(t, dataDir) {
		out[rel] = st.content
	}
	return out
}

// TestConflictWorklistChangesNothingAboutTheRun is the additive-observability
// claim, and the reason the flag is safe to add to a real wave: the same export
// against the same catalogue must produce the same summary (warnings and
// conflict counter included) and the same tree whether or not a worklist was
// attached.
//
// The export deliberately mixes a refused row with an enriching one, so the
// comparison is over a run that actually writes rather than one the guard
// silenced entirely.
func TestConflictWorklistChangesNothingAboutTheRun(t *testing.T) {
	export := conflictExport(
		conflictRow("B0LIBEX001", "Bea Reader"), // contradicts the seeded 300 minutes
		conflictRow("B0LIBEX002", "Cy Speaker"), // fills the second recording's absent facts
	)
	withDir := seedTwoRecordingTree(t, conflictRecShortRuntime, conflictRecSecond)
	withoutDir := seedTwoRecordingTree(t, conflictRecShortRuntime, conflictRecSecond)

	var buf bytes.Buffer
	with, err := RunLibex(writeBooks(t, export), Options{
		DataDir: withDir, ImportDate: testImportDate, Mode: ModeEnrich, Conflicts: &buf,
	})
	if err != nil {
		t.Fatalf("run with a worklist: %v", err)
	}
	without, err := RunLibex(writeBooks(t, export), Options{
		DataDir: withoutDir, ImportDate: testImportDate, Mode: ModeEnrich,
	})
	if err != nil {
		t.Fatalf("run without a worklist: %v", err)
	}

	// The run that was watched really did both things, or the comparison proves
	// nothing.
	if with.EnrichedRecordings != 1 || len(worklistLines(t, buf.String())) != 1 {
		t.Fatalf("the fixture must enrich one recording and refuse one row: %+v / %q", with, buf.String())
	}
	if !reflect.DeepEqual(with, without) {
		t.Errorf("summary differs:\n with %+v\nwithout %+v", with, without)
	}
	if got, want := treeContents(t, withDir), treeContents(t, withoutDir); !reflect.DeepEqual(got, want) {
		t.Errorf("the tree differs with a worklist attached:\n got %v\nwant %v", got, want)
	}
}

// TestConflictWorklistAppendsAcrossRuns pins the accumulation contract the flag
// exists for: a dump too big for one process is imported in chunks (scripts/
// README.md step 7), and every chunk's run has to add to ONE worklist rather
// than truncating the previous chunk's findings. The file is opened exactly as
// cmd/metaimport opens it.
func TestConflictWorklistAppendsAcrossRuns(t *testing.T) {
	dataDir := seedEnrichTree(t, map[string]string{recRel: conflictRecShortRuntime})
	path := filepath.Join(t.TempDir(), "conflicts.ndjson")

	for run := 1; run <= 2; run++ {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := RunLibex(writeBooks(t, fullRow), Options{
			DataDir: dataDir, ImportDate: testImportDate, Mode: ModeEnrich, Conflicts: f,
		}); err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	rows := worklistLines(t, string(raw))
	if len(rows) != 2 {
		t.Fatalf("worklist rows = %d after two runs, want 2:\n%s", len(rows), strings.Join(rows, "\n"))
	}
	if rows[0] != rows[1] {
		t.Errorf("the same refusal twice must record the same row:\n%s\n%s", rows[0], rows[1])
	}
}

// failingWriter refuses every write, counting the attempts.
type failingWriter struct{ writes int }

func (w *failingWriter) Write([]byte) (int, error) {
	w.writes++
	return 0, errors.New("no space left on device")
}

// TestConflictWorklistWriteFailureWarnsOnceAndKeepsImporting pins what a broken
// worklist costs: one warning, and nothing else. The import is complete and
// validated work, so discarding a landed tranche because its side channel filled
// up would be the worse outcome - but a silently truncated worklist reads as "no
// more conflicts", so the loss has to be visible. The sink is given up after the
// first failure, or a wave that fills a disk pays one warning per remaining
// conflict.
func TestConflictWorklistWriteFailureWarnsOnceAndKeepsImporting(t *testing.T) {
	dataDir := seedTwoRecordingTree(t, conflictRecShortRuntime, conflictRecSecondShort)
	export := conflictExport(
		conflictRow("B0LIBEX001", "Bea Reader"),
		conflictRow("B0LIBEX002", "Cy Speaker"),
	)

	sink := &failingWriter{}
	sum, err := RunLibex(writeBooks(t, export), Options{
		DataDir: dataDir, ImportDate: testImportDate, Mode: ModeEnrich, Conflicts: sink,
	})
	if err != nil {
		t.Fatalf("a failing worklist must not fail the run: %v", err)
	}
	if sink.writes != 1 {
		t.Errorf("write attempts = %d, want 1 - the sink is given up after the first failure", sink.writes)
	}
	var reported int
	for _, w := range sum.Warnings {
		if strings.HasPrefix(w, "conflict worklist:") {
			reported++
		}
	}
	if reported != 1 {
		t.Errorf("worklist warnings = %d, want exactly 1: %v", reported, sum.Warnings)
	}
	// Both rows were still refused on their own terms, and both said so.
	if got := len(sum.Warnings); got != 3 {
		t.Errorf("warnings = %d, want the two contradictions plus the worklist failure: %v", got, sum.Warnings)
	}
}
