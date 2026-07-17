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
		printSummary(importer.Summary{NewWorks: 1, NewRecordings: 2, MergedASINs: 3}, false)
	})
	if !strings.Contains(out, "3 asins merged into existing recordings") {
		t.Errorf("summary line missing the merged-ASIN count: %q", out)
	}
}
