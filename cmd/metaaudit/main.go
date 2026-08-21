// Command metaaudit runs a read-only data-quality audit over the data/ tree and
// writes a deterministic report: one NDJSON file per detector class plus a
// human-readable SUMMARY.md.
//
//	go run ./cmd/metaaudit -data data -o audit-report
//
// It never writes to data/ - the logic reaches the tree through pkg/check's
// loader and has no write path at all. Acting on the report is a separate pass.
//
// --profile names which families the root holds (pack.Profile), defaulting to
// `core` - this repository's tree since the community-repo split, and the same
// default metarepair takes. The two MUST agree: metarepair re-runs these very
// detectors before it applies anything, so a report taken under one reading of the
// tree and applied under another is the drift the fresh-audit gate exists to
// prevent. (metacheck and metafmt still default to `all`; they are read by CI with
// the flag spelled out, where a wrong reading is a red check rather than a wave of
// deletions.)
//
// Flag wiring only; the logic lives in internal/audit.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/kodestar/audiosilo-meta/internal/audit"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

func main() {
	dataDir := flag.String("data", "data", "path to the data root to audit (read-only)")
	out := flag.String("o", "", "directory to write the report into (required)")
	profileName := flag.String("profile", pack.ProfileCore.String(), pack.ProfileFlagUsage+
		" (default core: this repository's tree, matching metarepair)")
	flag.Parse()

	if *out == "" {
		fmt.Fprintln(os.Stderr, "metaaudit: -o <reportdir> is required")
		flag.Usage()
		os.Exit(2)
	}

	profile, perr := pack.ParseProfile(*profileName)
	if perr != nil {
		fmt.Fprintln(os.Stderr, "metaaudit:", perr)
		os.Exit(2)
	}

	rep, err := audit.Run(audit.Options{DataDir: *dataDir, OutDir: *out, Profile: profile})
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
