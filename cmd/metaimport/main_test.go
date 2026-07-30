package main

import (
	"io"
	"os"
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

func TestPrintSummaryIncludesMergedASINs(t *testing.T) {
	// Fix 7: the merged-ASIN count must be surfaced in the summary line so a
	// maintainer sees re-releases folded into existing recordings.
	out := captureStdout(t, func() {
		printSummary(importer.Summary{NewWorks: 1, NewRecordings: 2, MergedASINs: 3}, false, false)
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
		}, false, true)
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

// TestPrintSummaryDryRunHeadings pins each mode's dry-run wording. The create
// heading is long-standing output a user (and any log-reading habit) recognizes,
// so adding enrichment must not quietly reword it; enrichment gets its own
// heading rather than borrowing "imported", which it never does.
func TestPrintSummaryDryRunHeadings(t *testing.T) {
	cases := []struct {
		name           string
		dryRun, enrich bool
		want           string
	}{
		{name: "create", want: "imported:"},
		{name: "create dry run", dryRun: true, want: "plan (dry run, no files written):"},
		{name: "enrich", enrich: true, want: "enriched:"},
		{name: "enrich dry run", dryRun: true, enrich: true, want: "enrichment plan (dry run, no files written):"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := captureStdout(t, func() { printSummary(importer.Summary{}, tc.dryRun, tc.enrich) })
			if !strings.HasPrefix(out, tc.want) {
				t.Errorf("heading = %q, want it to start with %q", out, tc.want)
			}
		})
	}
}

// TestEnrichFlagReachesTheImporter is the ACCEPT half of the --enrich flag's
// pair: the refusal test below proves the wrong source is rejected, but without
// this one, deleting the flag's threading into Options would leave the suite
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
		if code := runSource(enrichSource, []string{"export.json", "--enrich"}, run); code != 0 {
			t.Errorf("exit code = %d, want 0", code)
		}
	})
	if !got.Enrich {
		t.Error("--enrich did not reach Options.Enrich")
	}

	// And the flag is opt-in: the same invocation without it must not enrich.
	got = importer.Options{}
	captureStdout(t, func() {
		if code := runSource(enrichSource, []string{"export.json"}, run); code != 0 {
			t.Errorf("exit code = %d, want 0", code)
		}
	})
	if got.Enrich {
		t.Error("Options.Enrich set without the flag")
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
