package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/kodestar/audiosilo-meta/internal/audit"
	"github.com/kodestar/audiosilo-meta/internal/importer"
)

// runStore is `metaimport store <listings.ndjson>`: a maintainer's reviewed
// file of a store's public listings (Audiobook Guild, GraphicAudio), landed
// through the create path under source type store-listing
// (internal/importer/store.go has the row shape; LICENSING.md "Store
// listings" the posture). It only creates: the catalogue-bounded modes key on
// an ASIN, which a store row never carries.
//
//	metaimport store <listings.ndjson> [--data data] [--dry-run] [--date YYYY-MM-DD] [--attach-editions] [--existing-series-only] [--conflicts <path>]
func runStore(args []string) int {
	fs := flag.NewFlagSet("store", flag.ContinueOnError)
	data := fs.String("data", "data", "path to the data directory")
	dryRun := fs.Bool("dry-run", false, "print the plan without writing any files")
	date := fs.String("date", "", "imported_at stamp (YYYY-MM-DD); defaults to today (UTC)")
	attachEditions := fs.Bool("attach-editions", false, "attach a row at a series position the catalogue already fills to the work there when it is another edition of it, and refuse it otherwise")
	existingSeriesOnly := fs.Bool("existing-series-only", false, "never found a series: drop (and report) a series claim that would create one; the row still imports")
	conflicts := fs.String("conflicts", "", "append one NDJSON row per refused contradiction to this file")
	path, err := parsePositional(fs, args, "<listings.ndjson>")
	if err != nil {
		fmt.Fprintln(os.Stderr, "metaimport:", err)
		usage()
		return 2
	}
	stamp, ok := runDate(*date)
	if !ok {
		return 2
	}
	conflictLog, closeLog, err := openAppendLog("--conflicts", *conflicts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "metaimport:", err)
		return 2
	}
	defer closeLog()
	sum, err := importer.RunStore(path, importer.Options{
		DataDir:            *data,
		ImportDate:         stamp,
		DryRun:             *dryRun,
		Mode:               importer.ModeCreate,
		Conflicts:          conflictLog,
		ExistingSeriesOnly: *existingSeriesOnly,
		AttachEditions:     *attachEditions,
		Distinct:           audit.ReviewedDistinct(),
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "metaimport:", err)
		return 1
	}
	printSummary(sum, *dryRun, importer.ModeCreate)
	if sum.SkippedRows > 0 {
		fmt.Printf("  refused %d rows at parse (see the warnings)\n", sum.SkippedRows)
	}
	printUnrecordedCredits(sum.UnrecordedCredits)
	return 0
}
