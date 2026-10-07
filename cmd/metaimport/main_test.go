package main

import (
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/importer"
	"github.com/kodestar/audiosilo-meta/internal/testpack"
)

// captureStdout runs fn with os.Stdout redirected to a pipe and returns what it
// printed.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	fn()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stdout = old
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// seedSelectFixture writes a minimal catalogue (one series holding volume 1)
// plus a libex export whose one row completes it, and returns the data dir, the
// export path, and the export's exact bytes.
func seedSelectFixture(t *testing.T) (dataDir, exportPath, exportBody string) {
	t.Helper()
	dir := t.TempDir()
	dataDir = filepath.Join(dir, "data")
	testpack.Seed(t, dataDir, map[string]string{
		"people/ad/ada-mapmaker.json": `{"id":"ada-mapmaker","license":"CC0-1.0","name":"Ada Mapmaker","sources":[{"type":"user"}]}`,
		"people/be/bea-reader.json":   `{"id":"bea-reader","license":"CC0-1.0","name":"Bea Reader","sources":[{"type":"user"}]}`,
		"works/vo/volume-one/work.json": `{"authors":["ada-mapmaker"],"id":"volume-one","language":"en","license":"CC0-1.0",` +
			`"sources":[{"type":"user"}],"title":"Volume One"}`,
		"works/vo/volume-one/recordings/bea-reader-2024.json": `{"asin":[{"asin":"B0PRESENT1","region":"us"}],"id":"bea-reader-2024",` +
			`"language":"en","license":"CC0-1.0","narrators":["bea-reader"],"sources":[{"type":"user"}],"work":"volume-one"}`,
		"series/ca/cartographer-chronicles.json": `{"id":"cartographer-chronicles","license":"CC0-1.0","name":"Cartographer Chronicles",` +
			`"sources":[{"type":"user"}],"works":[{"position":"1","work":"volume-one"}]}`,
	})

	exportBody = `{"asin":"B0SELECT02","title":"Volume Two","region":"us","language":"english",` +
		`"authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":"Bea Reader"}],` +
		`"series":[{"name":"Cartographer Chronicles","position":"2"}]}` + "\n"
	exportPath = filepath.Join(dir, "full.ndjson")
	if err := os.WriteFile(exportPath, []byte(exportBody), 0o644); err != nil {
		t.Fatal(err)
	}
	return dataDir, exportPath, exportBody
}

// TestLibexSelectArgumentOrders is the regression guard for the destructive
// argument mis-parse: with the flags BEFORE the positional, a "first argument
// that does not start with -" split reads the -o VALUE as the input export and
// writes the subset over the real one - truncating an operator's multi-GB dump
// to nothing and exiting 0. Both orders must name the same input and the same
// output, and the input must come out untouched.
func TestLibexSelectArgumentOrders(t *testing.T) {
	for _, tc := range []struct {
		name string
		args func(export, out, data string) []string
	}{
		{"positional first", func(export, out, data string) []string {
			return []string{export, "--data", data, "-o", out}
		}},
		{"flags first", func(export, out, data string) []string {
			return []string{"-o", out, "--data", data, export}
		}},
		{"positional between flags", func(export, out, data string) []string {
			return []string{"-o", out, export, "--data", data}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dataDir, export, body := seedSelectFixture(t)
			out := filepath.Join(t.TempDir(), "subset.ndjson")

			var code int
			stdout := captureStdout(t, func() { code = runLibexSelect(tc.args(export, out, dataDir)) })
			if code != 0 {
				t.Fatalf("exit code = %d, want 0 (%s)", code, stdout)
			}

			// The input export is never a write target.
			got, err := os.ReadFile(export)
			if err != nil {
				t.Fatalf("the input export is gone: %v", err)
			}
			if string(got) != body {
				t.Errorf("the input export was rewritten:\n got %q\nwant %q", got, body)
			}
			// The subset went where -o said, and holds the completing row.
			subset, err := os.ReadFile(out)
			if err != nil {
				t.Fatalf("read subset: %v", err)
			}
			if !strings.Contains(string(subset), "B0SELECT02") {
				t.Errorf("subset = %q, want the selected row", subset)
			}
			if !strings.Contains(stdout, "selected 1 of 1 rows") || !strings.Contains(stdout, "wrote "+out) {
				t.Errorf("report does not describe the run:\n%s", stdout)
			}
		})
	}
}

// TestLibexSelectRefusesOutputOverInput is the belt-and-braces half: whatever
// the arguments meant, -o naming the input export is refused rather than acted
// on, and the export survives.
func TestLibexSelectRefusesOutputOverInput(t *testing.T) {
	dataDir, export, body := seedSelectFixture(t)

	var code int
	stdout := captureStdout(t, func() {
		code = runLibexSelect([]string{"-o", export, export, "--data", dataDir})
	})
	if code != 1 {
		t.Errorf("exit code = %d, want 1 (%s)", code, stdout)
	}
	if got, err := os.ReadFile(export); err != nil || string(got) != body {
		t.Errorf("the input export was written over: %q (err %v)", got, err)
	}
}

// TestLibexSelectSuppressesReportOnAbort pins fix 6: a run that failed
// mid-stream must not print its report. The counts it holds cover only the rows
// it managed to read, so the breakdown would describe a fraction of the export
// as though it were the whole thing - and this report is the artifact a
// reviewer signs a tranche off from.
func TestLibexSelectSuppressesReportOnAbort(t *testing.T) {
	dataDir, _, _ := seedSelectFixture(t)
	dir := t.TempDir()
	truncated := filepath.Join(dir, "truncated.ndjson")
	body := `[{"asin":"B0SELECT02","title":"Volume Two","region":"us","language":"english",` +
		`"series":[{"name":"Cartographer Chronicles","position":"2"}]}`
	if err := os.WriteFile(truncated, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "subset.ndjson")

	var code int
	stdout := captureStdout(t, func() {
		code = runLibexSelect([]string{truncated, "--data", dataDir, "-o", out})
	})
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if strings.Contains(stdout, "selected ") || strings.Contains(stdout, "excluded ") {
		t.Errorf("an aborted run printed its report:\n%s", stdout)
	}
	if !strings.Contains(stdout, "aborted after 1 rows; no output written") {
		t.Errorf("stdout does not say how far the run got:\n%s", stdout)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Errorf("an aborted run left an output file (stat err = %v)", err)
	}
}

// TestParsePositionalRejectsAmbiguousArgs pins the two argument shapes that
// must not be guessed at: none, and more than one.
func TestParsePositionalRejectsAmbiguousArgs(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"none", []string{"--data", "data"}, "missing <export.json> path"},
		{"two", []string{"a.json", "--data", "data", "b.json"}, "expected one <export.json> path, got 2: a.json b.json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fs := flag.NewFlagSet("t", flag.ContinueOnError)
			fs.SetOutput(io.Discard)
			fs.String("data", "data", "")
			got, err := parsePositional(fs, tc.args, "<export.json>")
			if err == nil {
				t.Fatalf("parsePositional accepted %v as %q", tc.args, got)
			}
			if err.Error() != tc.want {
				t.Errorf("error = %q, want %q", err, tc.want)
			}
		})
	}
}

func TestPrintSummaryIncludesMergedASINs(t *testing.T) {
	// Fix 7: the merged-ASIN count must be surfaced in the summary line so a
	// maintainer sees re-releases folded into existing recordings.
	out := captureStdout(t, func() {
		printSummary(importer.Summary{NewWorks: 1, NewRecordings: 2, MergedASINs: 3}, false, importer.ModeCreate)
	})
	if !strings.Contains(out, "3 asins merged into existing recordings") {
		t.Errorf("summary line missing the merged-ASIN count: %q", out)
	}
}

func TestPrintSummaryEnrichMode(t *testing.T) {
	// Enrichment creates nothing, so its line reports the enrichment counters
	// rather than the create ones - and prints the row accounting as an identity
	// (rows read = matched + not in the catalogue + skipped at parse) so no row
	// can go missing without the line failing to add up.
	out := captureStdout(t, func() {
		printSummary(importer.Summary{
			EnrichedWorks: 1, EnrichedRecordings: 2, SeriesPlacements: 3,
			Matched: 4, NotInCatalog: 5, SkippedRows: 6,
		}, false, importer.ModeEnrich)
	})
	for _, want := range []string{
		"enriched:", "1 works", "2 recordings", "3 works placed in a series",
		"15 rows read = 4 matched + 5 not in the catalogue + 6 skipped at parse",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("enrich summary line missing %q: %q", want, out)
		}
	}
	if strings.Contains(out, "new works") {
		t.Errorf("enrich summary must not report create counters: %q", out)
	}
}

// TestPrintSummaryRecordingsOnlyMode pins the recordings-only line. The mode
// creates no work and no series, so reporting those counters would be noise -
// but the two skip buckets are exactly what an operator reads the run from, so
// both must be on the line.
func TestPrintSummaryRecordingsOnlyMode(t *testing.T) {
	out := captureStdout(t, func() {
		printSummary(importer.Summary{
			NewRecordings: 2, NewPeople: 15, Skipped: 3, SkippedNoWork: 4, MergedASINs: 1,
		}, false, importer.ModeRecordingsOnly)
	})
	for _, want := range []string{
		"added:", "2 new recordings", "15 new people", "3 skipped (already present)",
		"4 skipped (work not in the catalogue)", "1 asins merged into existing recordings",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("recordings-only summary line missing %q: %q", want, out)
		}
	}
	if strings.Contains(out, "new works") || strings.Contains(out, "new series") {
		t.Errorf("recordings-only summary must not report work/series counters: %q", out)
	}
}

// TestPrintSummaryDryRunHeadings pins each mode's dry-run wording. The create
// heading is long-standing output a user (and any log-reading habit) recognizes,
// so adding a mode must not quietly reword it; each new mode gets its own
// heading rather than borrowing "imported", which it never does.
func TestPrintSummaryDryRunHeadings(t *testing.T) {
	cases := []struct {
		name   string
		mode   importer.Mode
		dryRun bool
		want   string
	}{
		{name: "create", mode: importer.ModeCreate, want: "imported:"},
		{name: "create dry run", mode: importer.ModeCreate, dryRun: true, want: "plan (dry run, no files written):"},
		{name: "enrich", mode: importer.ModeEnrich, want: "enriched:"},
		{name: "enrich dry run", mode: importer.ModeEnrich, dryRun: true, want: "enrichment plan (dry run, no files written):"},
		{name: "recordings only", mode: importer.ModeRecordingsOnly, want: "added:"},
		{name: "recordings only dry run", mode: importer.ModeRecordingsOnly, dryRun: true, want: "recordings-only plan (dry run, no files written):"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := captureStdout(t, func() {
				printSummary(importer.Summary{}, tc.dryRun, tc.mode)
			})
			if !strings.HasPrefix(out, tc.want) {
				t.Errorf("heading = %q, want it to start with %q", out, tc.want)
			}
		})
	}
}

// TestEnrichFlagReachesTheImporter is the ACCEPT half of the --enrich flag's
// pair: the refusal test below proves the wrong source is rejected, but without
// this one, deleting the flag's mapping onto Options.Mode would leave the suite
// green while --enrich silently ran a CREATE import over the whole export.
func TestEnrichFlagReachesTheImporter(t *testing.T) {
	var got importer.Options
	run := func(path string, opts importer.Options) (importer.Summary, error) {
		got = opts
		if path != "export.json" {
			t.Errorf("export path = %q", path)
		}
		return importer.Summary{}, nil
	}
	captureStdout(t, func() {
		if code := runSource(boundedSource, []string{"export.json", "--enrich"}, run); code != 0 {
			t.Errorf("exit code = %d, want 0", code)
		}
	})
	if got.Mode != importer.ModeEnrich {
		t.Errorf("--enrich reached Options.Mode as %v, want ModeEnrich", got.Mode)
	}

	// And the flag is opt-in: the same invocation without it must not enrich.
	got = importer.Options{}
	captureStdout(t, func() {
		if code := runSource(boundedSource, []string{"export.json"}, run); code != 0 {
			t.Errorf("exit code = %d, want 0", code)
		}
	})
	if got.Mode != importer.ModeCreate {
		t.Errorf("Options.Mode = %v without the flag, want ModeCreate", got.Mode)
	}
}

func TestEnrichFlagRejectedForOtherSources(t *testing.T) {
	// --enrich is a per-source licensing decision, so pointing it at a source
	// other than libex must refuse clearly rather than silently enrich.
	called := false
	run := func(string, importer.Options) (importer.Summary, error) {
		called = true
		return importer.Summary{}, nil
	}
	if code := runSource("openaudible", []string{"books.json", "--enrich"}, run); code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if called {
		t.Error("the importer ran despite the refused --enrich flag")
	}
}

// TestRecordingsOnlyFlagReachesTheImporter is the ACCEPT half of the
// --recordings-only flag's pair (see TestEnrichFlagReachesTheImporter): without
// it, deleting the flag's mapping onto Options.Mode would leave the suite green
// while --recordings-only silently ran a CREATE import and minted duplicate
// works.
func TestRecordingsOnlyFlagReachesTheImporter(t *testing.T) {
	var got importer.Options
	run := func(path string, opts importer.Options) (importer.Summary, error) {
		got = opts
		if path != "export.json" {
			t.Errorf("export path = %q", path)
		}
		return importer.Summary{}, nil
	}
	captureStdout(t, func() {
		if code := runSource(boundedSource, []string{"export.json", "--recordings-only"}, run); code != 0 {
			t.Errorf("exit code = %d, want 0", code)
		}
	})
	if got.Mode != importer.ModeRecordingsOnly {
		t.Errorf("--recordings-only reached Options.Mode as %v, want ModeRecordingsOnly", got.Mode)
	}

	// And the flag is opt-in: the same invocation without it must not switch mode.
	got = importer.Options{}
	captureStdout(t, func() {
		if code := runSource(boundedSource, []string{"export.json"}, run); code != 0 {
			t.Errorf("exit code = %d, want 0", code)
		}
	})
	if got.Mode != importer.ModeCreate {
		t.Errorf("Options.Mode = %v without the flag, want ModeCreate", got.Mode)
	}
}

func TestRecordingsOnlyFlagRejectedForOtherSources(t *testing.T) {
	// Same per-source licensing decision as --enrich: the mode is bounded by
	// this catalogue and permitted for libex alone.
	called := false
	run := func(string, importer.Options) (importer.Summary, error) {
		called = true
		return importer.Summary{}, nil
	}
	if code := runSource("openaudible", []string{"books.json", "--recordings-only"}, run); code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if called {
		t.Error("the importer ran despite the refused --recordings-only flag")
	}
}

// TestModeFlagsAreMutuallyExclusive pins the refusal of the one combination
// that has no meaning: enrichment fills absent facts on ASIN-matched records
// while recordings-only adds narrations the catalogue has never seen, so a run
// asked for both would have to silently pick one. The CLI is the only place the
// combination can even be expressed - Options carries a single Mode - so this is
// where it has to be refused.
func TestModeFlagsAreMutuallyExclusive(t *testing.T) {
	called := false
	run := func(string, importer.Options) (importer.Summary, error) {
		called = true
		return importer.Summary{}, nil
	}
	if code := runSource(boundedSource, []string{"export.json", "--enrich", "--recordings-only"}, run); code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if called {
		t.Error("the importer ran despite two conflicting mode flags")
	}
}

// TestConflictsFlagReachesTheImporterAndAppends is the --conflicts flag's accept
// half. Two things have to hold and neither is visible from the importer's own
// tests: the flag arrives as a WRITABLE sink (so a worklist row written during
// planning is on disk when the process exits), and the file is opened for
// APPEND, because a dump imported in chunks has to accumulate one worklist
// across every chunk's run rather than each run truncating the last.
func TestConflictsFlagReachesTheImporterAndAppends(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conflicts.ndjson")
	if err := os.WriteFile(path, []byte("{\"run\":\"earlier chunk\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(_ string, opts importer.Options) (importer.Summary, error) {
		if opts.Conflicts == nil {
			t.Fatal("--conflicts did not reach Options.Conflicts")
		}
		if _, err := opts.Conflicts.Write([]byte("{\"run\":\"this chunk\"}\n")); err != nil {
			t.Errorf("write to the worklist: %v", err)
		}
		return importer.Summary{}, nil
	}
	captureStdout(t, func() {
		if code := runSource("openaudible", []string{"books.json", "--conflicts", path}, run); code != 0 {
			t.Errorf("exit code = %d, want 0", code)
		}
	})
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := "{\"run\":\"earlier chunk\"}\n{\"run\":\"this chunk\"}\n"; string(raw) != want {
		t.Errorf("worklist =\n%q\nwant\n%q", raw, want)
	}
}

// TestConflictsFlagAbsentLeavesTheSinkNil is the opt-in half, and it pins the
// one way this flag could break every run that does not use it: a nil *os.File
// assigned to the io.Writer field is a NON-nil interface, which the importer
// would treat as a worklist and write into on the first conflict.
func TestConflictsFlagAbsentLeavesTheSinkNil(t *testing.T) {
	var got importer.Options
	run := func(_ string, opts importer.Options) (importer.Summary, error) {
		got = opts
		return importer.Summary{}, nil
	}
	captureStdout(t, func() {
		if code := runSource("openaudible", []string{"books.json"}, run); code != 0 {
			t.Errorf("exit code = %d, want 0", code)
		}
	})
	if got.Conflicts != nil {
		t.Errorf("Options.Conflicts = %#v without the flag, want nil", got.Conflicts)
	}
}

// TestConflictsFlagRejectedByLibexSelect keeps the flag's story consistent with
// --enrich's: a flag pointed at a subcommand that cannot honour it says why.
// Selection writes no records, so no row of it can contradict one - and the
// refusal has to arrive before the export is read, or a mistyped invocation
// costs a full pass over the dump first.
func TestConflictsFlagRejectedByLibexSelect(t *testing.T) {
	var code int
	stderr := captureStderr(t, func() {
		code = runLibexSelect([]string{"export.ndjson", "-o", "subset.ndjson", "--conflicts", "conflicts.ndjson"})
	})
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr, "--conflicts") || !strings.Contains(stderr, "libex-select") {
		t.Errorf("stderr does not explain the refusal:\n%s", stderr)
	}
	if _, err := os.Stat("subset.ndjson"); err == nil {
		t.Error("libex-select wrote its output despite the refused flag")
	}
}

// TestConflictsFlagRejectsAnUnopenablePath refuses before the import runs: a
// worklist that cannot be created is a mistyped invocation, and discovering it
// six hours into a wave (or, worse, not discovering it) is the outcome this
// avoids.
func TestConflictsFlagRejectsAnUnopenablePath(t *testing.T) {
	called := false
	run := func(string, importer.Options) (importer.Summary, error) {
		called = true
		return importer.Summary{}, nil
	}
	path := filepath.Join(t.TempDir(), "no-such-dir", "conflicts.ndjson")
	var code int
	stderr := captureStderr(t, func() {
		code = runSource("openaudible", []string{"books.json", "--conflicts", path}, run)
	})
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if called {
		t.Error("the importer ran despite an unopenable worklist path")
	}
	if !strings.Contains(stderr, "--conflicts") {
		t.Errorf("stderr does not name the flag:\n%s", stderr)
	}
}

// TestFailedRunPrintsNoSummary pins the ordering of the two things a finished
// run reports. A run can fail BEFORE it plans anything - a data tree still in
// the file-per-entity layout is refused when the store is opened - and printing
// the Summary regardless produced a "plan (dry run): 0 new works, 0 new
// recordings ..." line that is fiction: nothing was planned, so nothing was
// planned to be zero.
func TestFailedRunPrintsNoSummary(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	testpack.SeedLegacyPerson(t, dataDir, "some-author", "Some Author")

	books := filepath.Join(dir, "books.json")
	body := `[{"asin":"B0LEGACY01","title_short":"Legacy Book","author":"Some Author",` +
		`"narrated_by":"A Reader","language":"english","region":"US","seconds":600}]`
	if err := os.WriteFile(books, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	var (
		code   int
		stderr string
	)
	stdout := captureStdout(t, func() {
		stderr = captureStderr(t, func() {
			code = runSource("openaudible", []string{books, "--data", dataDir, "--dry-run"}, importer.Run)
		})
	})
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if stdout != "" {
		t.Errorf("a failed run printed a summary:\n%s", stdout)
	}
	if !strings.Contains(stderr, "legacy file-per-entity layout") {
		t.Errorf("stderr does not explain the refusal:\n%s", stderr)
	}
}

// captureStderr runs fn with os.Stderr redirected to a pipe and returns what it
// printed, so a test asserting on a refusal does not spill the message into the
// suite's own output.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	fn()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stderr = old
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// TestPrintSummaryNotesComeBeforeWarnings pins the run's NOTES on the summary.
// They say what the run DID - today, that an AI-narrated book was admitted
// under the canonical synthetic record rather than refused - and a reader
// scanning a long warning list should not have to reach its end to find that
// out, so they print first.
func TestPrintSummaryNotesComeBeforeWarnings(t *testing.T) {
	out := captureStdout(t, func() {
		printSummary(importer.Summary{
			NewWorks: 1,
			Notes:    []string{"2 recordings credited to Virtual Voice (synthetic narration)"},
			Warnings: []string{"something else"},
		}, false, importer.ModeCreate)
	})
	note := strings.Index(out, "note: 2 recordings credited to Virtual Voice (synthetic narration)")
	warn := strings.Index(out, "warning: something else")
	if note < 0 || warn < 0 {
		t.Fatalf("summary missing the note or the warning: %q", out)
	}
	if note > warn {
		t.Errorf("the note printed after the warning: %q", out)
	}
}

// TestExistingSeriesOnlyFlagReachesTheImporter: the flag the series-completion
// sync bot passes must land on Options.ExistingSeriesOnly for every source, and
// be off without it - a dropped mapping would let the bot found series again.
func TestExistingSeriesOnlyFlagReachesTheImporter(t *testing.T) {
	var got importer.Options
	run := func(_ string, opts importer.Options) (importer.Summary, error) {
		got = opts
		return importer.Summary{}, nil
	}
	for _, source := range []string{boundedSource, "openaudible"} {
		got = importer.Options{}
		captureStdout(t, func() {
			if code := runSource(source, []string{"export.json", "--existing-series-only"}, run); code != 0 {
				t.Errorf("%s: exit code = %d, want 0", source, code)
			}
		})
		if !got.ExistingSeriesOnly {
			t.Errorf("%s: --existing-series-only did not reach Options.ExistingSeriesOnly", source)
		}
		got = importer.Options{ExistingSeriesOnly: true}
		captureStdout(t, func() {
			if code := runSource(source, []string{"export.json"}, run); code != 0 {
				t.Errorf("%s: exit code = %d, want 0", source, code)
			}
		})
		if got.ExistingSeriesOnly {
			t.Errorf("%s: Options.ExistingSeriesOnly is set without the flag", source)
		}
	}
}

// --refusals reaches the selector: the worklist lands where the flag says, one
// line per refused row in the contract's shape.
func TestLibexSelectRefusalsFlag(t *testing.T) {
	dataDir, export, _ := seedSelectFixture(t)
	body := `{"asin":"B0OTHER001","title":"Unrelated","region":"us","language":"english",` +
		`"authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":"Bea Reader"}],"series":[{"name":"Elsewhere","position":"1"}]}` + "\n"
	f, err := os.OpenFile(export, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(body); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	dir := t.TempDir()
	out, refusals := filepath.Join(dir, "subset.ndjson"), filepath.Join(dir, "refusals.ndjson")
	attachments := filepath.Join(dir, "attachments.ndjson")
	var code int
	stdout := captureStdout(t, func() {
		code = runLibexSelect([]string{export, "--data", dataDir, "-o", out, "--refusals", refusals, "--attachments", attachments})
	})
	if code != 0 {
		t.Fatalf("exit code = %d (%s)", code, stdout)
	}
	got, err := os.ReadFile(refusals)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"asin":"B0OTHER001","reason":"no-catalogue-series"}`+"\n" {
		t.Errorf("refusals = %q", got)
	}
	// Asked for, the attachment worklist is written even when it is empty, so a
	// reader can tell "none" from "not produced".
	if got, err := os.ReadFile(attachments); err != nil || len(got) != 0 {
		t.Errorf("attachments = %q, %v; want an empty file", got, err)
	}
}

// The attach line is printed only when a run attached something, so every other
// create summary - and the line the sync bot parses - reads exactly as before.
func TestPrintSummaryAttachLine(t *testing.T) {
	const head = "imported: 0 new works, 1 new recordings, 0 new people, 0 new series; 0 skipped (already present); 1 asins merged into existing recordings; 0 warnings\n"
	out := captureStdout(t, func() {
		printSummary(importer.Summary{NewRecordings: 1, MergedASINs: 1}, false, importer.ModeCreate)
	})
	if out != head {
		t.Errorf("summary without attachments = %q, want %q", out, head)
	}
	out = captureStdout(t, func() {
		printSummary(importer.Summary{NewRecordings: 1, MergedASINs: 1, Attached: 2}, false, importer.ModeCreate)
	})
	if !strings.HasPrefix(out, head) || !strings.Contains(out, "  attached 2 rows to the catalogued work already at their series position") {
		t.Errorf("summary with attachments = %q", out)
	}
	out = captureStdout(t, func() {
		printSummary(importer.Summary{NewRecordings: 1, MergedASINs: 1, Attached: 1}, false, importer.ModeCreate)
	})
	if !strings.Contains(out, "  attached 1 row to the catalogued work already at its series position") {
		t.Errorf("summary with one attachment = %q", out)
	}
}

// --skipped writes the run's Skips as {"asin","reason"} lines, atomically and
// only on success, and is refused for any source but libex.
func TestSkippedFlagWritesTheWorklist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "skipped.ndjson")
	run := func(string, importer.Options) (importer.Summary, error) {
		return importer.Summary{SkippedOccupied: 1, Skips: []importer.RowSkip{
			{ASIN: "B0DENY0001", Reason: importer.RefusalPositionClaimed},
			{ASIN: "B0REGION04", Reason: importer.RefusalUnmappedRegion},
		}}, nil
	}
	var code int
	out := captureStdout(t, func() { code = runSource(boundedSource, []string{"export.json", "--skipped", path}, run) })
	if code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"asin":"B0DENY0001","reason":"position-claimed"}` + "\n" + `{"asin":"B0REGION04","reason":"unmapped-region"}` + "\n"
	if string(got) != want {
		t.Errorf("skipped = %q, want %q", got, want)
	}
	if !strings.Contains(out, "  skipped 1 row claiming a series position the catalogue already holds with another work") {
		t.Errorf("summary does not report the refused row on its own line:\n%s", out)
	}
	if !strings.HasPrefix(out, "imported: 0 new works, 0 new recordings, 0 new people, 0 new series; 0 skipped (already present); 0 asins merged into existing recordings; 0 warnings\n") {
		t.Errorf("the create summary line changed:\n%s", out)
	}

	failing := func(string, importer.Options) (importer.Summary, error) {
		return importer.Summary{}, errors.New("boom")
	}
	failed := filepath.Join(t.TempDir(), "skipped.ndjson")
	captureStdout(t, func() { _ = runSource(boundedSource, []string{"export.json", "--skipped", failed}, failing) })
	if _, err := os.Stat(failed); !os.IsNotExist(err) {
		t.Errorf("a failed run wrote the worklist: %v", err)
	}
	if code := runSource("openaudible", []string{"books.json", "--skipped", path}, run); code != 2 {
		t.Errorf("--skipped on openaudible: exit %d, want 2", code)
	}
}

// A --skipped path that cannot be written fails BEFORE the import runs; a
// worklist that cannot be committed after a completed import still leaves the
// summary printed, since the tree was written.
func TestSkippedFlagIsStagedBeforeTheRun(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "file")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	ran := false
	run := func(string, importer.Options) (importer.Summary, error) {
		ran = true
		return importer.Summary{}, nil
	}
	var code int
	captureStdout(t, func() {
		code = runSource(boundedSource, []string{"export.json", "--skipped", filepath.Join(blocker, "skipped.ndjson")}, run)
	})
	if code == 0 || ran {
		t.Errorf("exit %d, ran %v: a bad --skipped path must fail before the import", code, ran)
	}

	// A directory where the worklist goes: staging works, the commit cannot.
	dest := filepath.Join(dir, "skipped.ndjson")
	if err := os.MkdirAll(filepath.Join(dest, "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() { code = runSource(boundedSource, []string{"export.json", "--skipped", dest}, run) })
	if code != 1 || !strings.HasPrefix(out, "imported: ") {
		t.Errorf("exit %d, stdout %q: a completed import prints its summary even when the worklist fails", code, out)
	}
}

// --attach-editions is a create-path option: with --enrich or --recordings-only
// it is refused before anything runs.
func TestAttachEditionsRejectsTheBoundedModes(t *testing.T) {
	run := func(string, importer.Options) (importer.Summary, error) {
		t.Error("the import must not run")
		return importer.Summary{}, nil
	}
	for _, mode := range []string{"--enrich", "--recordings-only"} {
		if code := runSource(boundedSource, []string{"export.json", "--attach-editions", mode}, run); code != 2 {
			t.Errorf("--attach-editions %s: exit %d, want 2", mode, code)
		}
	}
}

func TestRelocateModeFlags(t *testing.T) {
	for _, tc := range []struct {
		source                       string
		enrich, recordings, relocate bool
		want                         importer.Mode
		bad                          bool
	}{
		{"libex", false, false, true, importer.ModeRelocate, false},
		{"libex", true, false, true, 0, true},
		{"libex", false, true, true, 0, true},
		{"openaudible", false, false, true, 0, true},
	} {
		mode, err := selectMode(tc.source, tc.enrich, tc.recordings, tc.relocate, false)
		if (err != nil) != tc.bad || !tc.bad && mode != tc.want {
			t.Fatalf("selectMode(%+v) = %v, %v", tc, mode, err)
		}
	}
}

func TestRelocateCLIRefusalsAreWrittenAtomically(t *testing.T) {
	dir, export, _ := seedSelectFixture(t)
	work, _ := testpack.Raw(t, dir, "works/vo/volume-one/work.json")
	recording, _ := testpack.Raw(t, dir, "works/vo/volume-one/recordings/bea-reader-2024.json")
	testpack.Seed(t, dir, map[string]string{
		"works/vo/volume-one/work.json":                       string(work),
		"works/vo/volume-one/recordings/bea-reader-2024.json": string(recording),
		"works/vo/volume-one/recordings/cross.json":           `{"id":"cross","work":"volume-one","language":"de","narrators":["bea-reader"],"asin":[{"region":"de","asin":"B0SELECT02"}],"license":"CC0-1.0","sources":[{"type":"user"}]}`,
	})
	skipped := filepath.Join(t.TempDir(), "skipped.ndjson")
	var code int
	out := captureStdout(t, func() {
		code = runSource("libex", []string{export, "--relocate", "--data", dir, "--skipped", skipped}, importer.RunLibex)
	})
	if code != 0 {
		t.Fatal(code, out)
	}
	raw, err := os.ReadFile(skipped)
	if err != nil {
		t.Fatal(err)
	}
	want := "{\"asin\":\"B0SELECT02\",\"reason\":\"relocate-recording-language\"}\n"
	if string(raw) != want {
		t.Fatalf("worklist = %q", raw)
	}
	if !strings.Contains(out, "relocate-recording-language: 1 recordings refused") {
		t.Fatal(out)
	}
}

// TestRegenerateGenresFlags is --regenerate-genres' accept and refuse halves:
// the flag reaches Options.Mode, it is exclusive with every other mode, it is
// libex-only, and --genre-changes is refused without it.
func TestRegenerateGenresFlags(t *testing.T) {
	var got importer.Options
	run := func(path string, opts importer.Options) (importer.Summary, error) {
		got = opts
		return importer.Summary{}, nil
	}
	captureStdout(t, func() {
		if code := runSource(boundedSource, []string{"export.json", "--regenerate-genres", "--rows-as-of", "2026-07-29"}, run); code != 0 {
			t.Errorf("exit code = %d, want 0", code)
		}
	})
	if got.Mode != importer.ModeRegenerateGenres || got.RowsAsOf != "2026-07-29" {
		t.Errorf("--regenerate-genres reached Options as mode %v, rows as of %q", got.Mode, got.RowsAsOf)
	}
	refused := func(string, importer.Options) (importer.Summary, error) {
		t.Error("the import must not run")
		return importer.Summary{}, nil
	}
	for _, args := range [][]string{
		{"export.json", "--regenerate-genres", "--enrich"},
		{"export.json", "--regenerate-genres", "--recordings-only"},
		{"export.json", "--regenerate-genres", "--relocate"},
		{"export.json", "--regenerate-genres", "--attach-editions"},
		{"export.json", "--genre-changes", "x.ndjson"},
		{"export.json", "--regenerate-genres"},                               // no --rows-as-of
		{"export.json", "--regenerate-genres", "--rows-as-of", "2026-07"},    // not a day
		{"export.json", "--regenerate-genres", "--rows-as-of", "2062-07-29"}, // after today
		{"export.json", "--enrich", "--rows-as-of", "2026-07-29"},            // another mode
	} {
		if code := runSource(boundedSource, args, refused); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
	if code := runSource("openaudible", []string{"books.json", "--regenerate-genres"}, refused); code != 2 {
		t.Errorf("--regenerate-genres on openaudible: exit %d, want 2", code)
	}
}

// TestGenreChangesWorklist runs the real mode end to end through the CLI: a
// libex-only work whose one recording's row maps a genre the record lacks is
// set to it, the worklist names the change, and the summary line says so.
func TestGenreChangesWorklist(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")
	testpack.Seed(t, dataDir, map[string]string{
		"people/ad/ada-mapmaker.json": `{"id":"ada-mapmaker","license":"CC0-1.0","name":"Ada Mapmaker","sources":[{"type":"libex-import"}]}`,
		"people/be/bea-reader.json":   `{"id":"bea-reader","license":"CC0-1.0","name":"Bea Reader","sources":[{"type":"libex-import"}]}`,
		"works/vo/volume-one/work.json": `{"authors":["ada-mapmaker"],"genres":["westerns"],"id":"volume-one","language":"en","license":"CC0-1.0",` +
			`"sources":[{"type":"libex-import"}],"title":"Volume One"}`,
		"works/vo/volume-one/recordings/bea-reader-2024.json": `{"asin":[{"asin":"B0PRESENT1","region":"us"}],"id":"bea-reader-2024",` +
			`"language":"en","license":"CC0-1.0","narrators":["bea-reader"],"sources":[{"type":"libex-import"}],"work":"volume-one"}`,
	})
	export := filepath.Join(dir, "rows.ndjson")
	row := `{"asin":"B0PRESENT1","title":"Volume One","region":"us","language":"english","authors":[{"name":"Ada Mapmaker"}],` +
		`"narrators":[{"name":"Bea Reader"}],"genres":[{"name":"Mystery"}]}` + "\n"
	if err := os.WriteFile(export, []byte(row), 0o644); err != nil {
		t.Fatal(err)
	}
	changes := filepath.Join(dir, "changes.ndjson")
	var code int
	out := captureStdout(t, func() {
		code = runSource("libex", []string{export, "--regenerate-genres", "--rows-as-of", "2026-10-06", "--data", dataDir, "--genre-changes", changes}, importer.RunLibex)
	})
	if code != 0 {
		t.Fatal(code, out)
	}
	if !strings.Contains(out, "regenerated genres: 1 works set to the recording vote") {
		t.Errorf("summary = %q", out)
	}
	raw, err := os.ReadFile(changes)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"work":"volume-one","removed":["westerns"],"added":["mystery"],"mode":"trim"}` + "\n"; string(raw) != want {
		t.Errorf("worklist = %q, want %q", raw, want)
	}
}

// TestRegenerateGenresRefusesInertFlags: every flag that would do nothing under
// --regenerate-genres (it writes genres only and stamps no source) is refused
// through the one flag table, rather than silently ignored.
func TestRegenerateGenresRefusesInertFlags(t *testing.T) {
	refused := func(string, importer.Options) (importer.Summary, error) {
		t.Error("the import must not run")
		return importer.Summary{}, nil
	}
	for _, extra := range [][]string{
		{"--date", "2026-10-06"},
		{"--conflicts", "c.ndjson"},
		{"--existing-series-only"},
		{"--series-lookup"},
		{"--series-lookup-limit", "5"},
		{"--libex", "https://example.invalid"},
		{"--attach-editions"},
		{"--skipped", "s.ndjson"},
	} {
		args := append([]string{"export.json", "--regenerate-genres", "--rows-as-of", "2026-07-29"}, extra...)
		if code := runSource(boundedSource, args, refused); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
	// The same flags stay valid in the other libex modes.
	ran := false
	ok := func(string, importer.Options) (importer.Summary, error) { ran = true; return importer.Summary{}, nil }
	captureStdout(t, func() {
		if code := runSource(boundedSource, []string{"export.json", "--enrich", "--date", "2026-10-06", "--existing-series-only"}, ok); code != 0 || !ran {
			t.Errorf("--enrich with --date: exit %d, ran %v", code, ran)
		}
	})
}
