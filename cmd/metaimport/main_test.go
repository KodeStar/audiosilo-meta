package main

import (
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/importer"
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
	for rel, body := range map[string]string{
		"people/ad/ada-mapmaker.json": `{"id":"ada-mapmaker","license":"CC0-1.0","name":"Ada Mapmaker","sources":[{"type":"user"}]}`,
		"people/be/bea-reader.json":   `{"id":"bea-reader","license":"CC0-1.0","name":"Bea Reader","sources":[{"type":"user"}]}`,
		"works/vo/volume-one/work.json": `{"authors":["ada-mapmaker"],"id":"volume-one","language":"en","license":"CC0-1.0",` +
			`"sources":[{"type":"user"}],"title":"Volume One"}`,
		"works/vo/volume-one/recordings/bea-reader-2024.json": `{"asin":[{"asin":"B0PRESENT1","region":"us"}],"id":"bea-reader-2024",` +
			`"language":"en","license":"CC0-1.0","narrators":["bea-reader"],"sources":[{"type":"user"}],"work":"volume-one"}`,
		"series/ca/cartographer-chronicles.json": `{"id":"cartographer-chronicles","license":"CC0-1.0","name":"Cartographer Chronicles",` +
			`"sources":[{"type":"user"}],"works":[{"position":"1","work":"volume-one"}]}`,
	} {
		path := filepath.Join(dataDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

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
