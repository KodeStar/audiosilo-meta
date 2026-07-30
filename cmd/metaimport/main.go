// Command metaimport ingests an external audiobook-library export into the
// data/ tree as work/recording/person/series records, deduplicating against the
// existing catalog so a contributor's upload becomes a reviewable diff.
//
// Usage:
//
//	metaimport openaudible <books.json>  [--data data] [--dry-run] [--date YYYY-MM-DD]
//	metaimport libation    <export.json> [--data data] [--dry-run] [--date YYYY-MM-DD]
//	metaimport libex       <export.json> [--data data] [--dry-run] [--date YYYY-MM-DD] [--enrich]
//
// --dry-run prints the plan without writing. A real run writes the new/changed
// files, then validates the whole tree and exits non-zero if that fails. Import
// warnings (a book skipped for a missing narrator, an odd field) are
// informational and never fail the run.
//
// --enrich (libex only) switches from creating records to ENRICHING the ones
// already here: a row whose ASIN the catalogue does not hold is counted and
// ignored, and a matched row only fills facts the existing work/recording does
// not have. Nothing is ever created, so the run is bounded by this catalogue
// rather than by the source's. What it READS is not bounded, though - the parser
// slurps the whole file before the ASIN match discards the rows that do not
// apply (roughly 6GB of live heap for libex's 1.06M-row dump), so the
// recommended input is still a pre-filtered row set: the libex-select output, or
// rows filtered to catalogued ASINs at export time. Feeding the raw dump needs a
// machine sized for it.
package main

import (
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/kodestar/audiosilo-meta/internal/importer"
)

var dateRE = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "openaudible":
		os.Exit(runSource("openaudible", os.Args[2:], importer.Run))
	case "libation":
		os.Exit(runSource("libation", os.Args[2:], importer.RunLibation))
	case "audiosilo-books":
		os.Exit(runSource("audiosilo-books", os.Args[2:], importer.RunAudiosiloBooks))
	case "libex":
		os.Exit(runSource("libex", os.Args[2:], importer.RunLibex))
	case "-h", "--help", "help":
		usage()
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "metaimport: unknown source %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

// enrichSource is the one source whose operator permits the ASIN-matched
// enrichment pass, so --enrich is accepted for it alone. The importer core
// supports the mode for any source; restricting it here keeps the licensing
// posture a deliberate, per-source decision rather than a flag anyone can point
// at any export.
const enrichSource = "libex"

// runSource parses the shared flags for a source subcommand and runs its
// importer (Run for openaudible, RunLibation for libation, RunLibex for libex).
func runSource(name string, args []string, run func(string, importer.Options) (importer.Summary, error)) int {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	data := fs.String("data", "data", "path to the data directory")
	dryRun := fs.Bool("dry-run", false, "print the plan without writing any files")
	date := fs.String("date", "", "imported_at stamp (YYYY-MM-DD); defaults to today (UTC)")
	// Registered for every source so pointing it at the wrong one produces a
	// clear refusal instead of flag's bare "not defined" line.
	enrich := fs.Bool("enrich", false, "fill absent facts on ASIN-matched existing records instead of creating any (libex only)")

	// Accept the positional export path either before or after the flags.
	exportPath, flagArgs := splitPositional(args)
	if err := fs.Parse(flagArgs); err != nil {
		return 2
	}
	if exportPath == "" {
		fmt.Fprintf(os.Stderr, "metaimport: missing <export.json> path\n")
		usage()
		return 2
	}
	if *enrich && name != enrichSource {
		fmt.Fprintf(os.Stderr, "metaimport: --enrich is only supported for the %s source, not %q\n", enrichSource, name)
		return 2
	}

	stamp := *date
	if stamp == "" {
		stamp = time.Now().UTC().Format("2006-01-02")
	} else if !dateRE.MatchString(stamp) {
		fmt.Fprintf(os.Stderr, "metaimport: --date %q must be YYYY-MM-DD\n", stamp)
		return 2
	}

	sum, err := run(exportPath, importer.Options{
		DataDir:    *data,
		ImportDate: stamp,
		DryRun:     *dryRun,
		Enrich:     *enrich,
	})

	printSummary(sum, *dryRun, *enrich)
	if err != nil {
		fmt.Fprintln(os.Stderr, "metaimport:", err)
		return 1
	}
	return 0
}

// splitPositional pulls the first non-flag argument out as the books path,
// leaving the rest for the flag parser.
func splitPositional(args []string) (positional string, rest []string) {
	for _, a := range args {
		if positional == "" && !strings.HasPrefix(a, "-") {
			positional = a
			continue
		}
		rest = append(rest, a)
	}
	return positional, rest
}

// printSummary renders the run's outcome. Enrichment reports its own counters
// (it creates nothing, so the create counters are all zero and would only be
// noise) while sharing the warning list. Its row accounting is deliberately
// printed as an identity - rows read = matched + not in the catalogue + skipped
// at parse - so a row can never go missing without the line failing to add up.
func printSummary(s importer.Summary, dryRun, enrich bool) {
	var head string
	switch {
	case enrich && dryRun:
		head = "enrichment plan (dry run, no files written)"
	case enrich:
		head = "enriched"
	case dryRun:
		head = "plan (dry run, no files written)"
	default:
		head = "imported"
	}
	if enrich {
		rows := s.Matched + s.NotInCatalog + s.SkippedRows
		fmt.Printf("%s: %d works, %d recordings; %d works placed in a series; %d rows read = %d matched + %d not in the catalogue + %d skipped at parse; %d warnings\n",
			head, s.EnrichedWorks, s.EnrichedRecordings, s.SeriesPlacements,
			rows, s.Matched, s.NotInCatalog, s.SkippedRows, len(s.Warnings))
	} else {
		fmt.Printf("%s: %d new works, %d new recordings, %d new people, %d new series; %d skipped (already present); %d asins merged into existing recordings; %d warnings\n",
			head, s.NewWorks, s.NewRecordings, s.NewPeople, s.NewSeries, s.Skipped, s.MergedASINs, len(s.Warnings))
	}
	for _, w := range s.Warnings {
		fmt.Println("  warning:", w)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage:")
	fmt.Fprintln(os.Stderr, "  metaimport openaudible <books.json>  [--data data] [--dry-run] [--date YYYY-MM-DD]")
	fmt.Fprintln(os.Stderr, "  metaimport libation    <export.json> [--data data] [--dry-run] [--date YYYY-MM-DD]")
	fmt.Fprintln(os.Stderr, "  metaimport audiosilo-books <export.json> [--data data] [--dry-run] [--date YYYY-MM-DD]")
	fmt.Fprintln(os.Stderr, "  metaimport libex       <export.json> [--data data] [--dry-run] [--date YYYY-MM-DD] [--enrich]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "  --enrich (libex only) fills absent facts on ASIN-matched existing records; it never creates.")
}
