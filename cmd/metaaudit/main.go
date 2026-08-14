// Command metaaudit runs a read-only data-quality audit over the data/ tree and
// writes a deterministic report: one NDJSON file per detector class plus a
// human-readable SUMMARY.md.
//
//	go run ./cmd/metaaudit -data data -o audit-report
//
// It never writes to data/ - the logic reaches the tree through pkg/check's
// loader and has no write path at all. Acting on the report is a separate pass.
// Flag wiring only; the logic lives in internal/audit.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/kodestar/audiosilo-meta/internal/audit"
)

func main() {
	dataDir := flag.String("data", "data", "path to the data root to audit (read-only)")
	out := flag.String("o", "", "directory to write the report into (required)")
	flag.Parse()

	if *out == "" {
		fmt.Fprintln(os.Stderr, "metaaudit: -o <reportdir> is required")
		flag.Usage()
		os.Exit(2)
	}

	rep, err := audit.Run(audit.Options{DataDir: *dataDir, OutDir: *out})
	if err != nil {
		fmt.Fprintln(os.Stderr, "metaaudit:", err)
		os.Exit(1)
	}

	if err := rep.Write(os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "metaaudit:", err)
		os.Exit(1)
	}
	fmt.Printf("report written to %s\n", *out)
	if rep.LoaderProblems > 0 {
		fmt.Fprintf(os.Stderr, "metaaudit: warning: pkg/check reported %d problem(s) on this tree - fix them before acting on the report\n",
			rep.LoaderProblems)
	}
}
