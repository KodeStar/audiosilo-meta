// Command metaimport ingests an external audiobook-library export into the
// data/ tree as work/recording/person/series records, deduplicating against the
// existing catalog so a contributor's upload becomes a reviewable diff.
//
// Usage:
//
//	metaimport openaudible <books.json>  [--data data] [--dry-run] [--date YYYY-MM-DD]
//	metaimport libation    <export.json> [--data data] [--dry-run] [--date YYYY-MM-DD]
//	metaimport libex       <export.json> [--data data] [--dry-run] [--date YYYY-MM-DD] [--enrich | --recordings-only]
//	metaimport libex-select <export.ndjson> -o <subset.ndjson> [--data data] [--max-per-series N]
//
// libex-select writes no records: it reduces a full libex export to the
// bounded, series-completing subset LICENSING.md's import posture allows, and
// prints a report for review; the subset is then imported with the normal
// `metaimport libex` create path.
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
//
// --recordings-only (libex only) switches to adding ALTERNATE NARRATIONS to
// works the catalogue already holds: a row is resolved to an existing work by
// title and author set, and lands as a new recording under it (or its ASIN
// merges into a matching sibling recording). A row whose work is not here is
// counted and dropped - the mode never creates a work and never touches a series
// file. It is what closes the gap the other modes leave: a second narration of a
// catalogued book matches no ASIN (so --enrich ignores it), fills no free series
// position (so libex-select excludes it), and would mint a duplicate work on the
// create path. Mutually exclusive with --enrich.
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
	case "libex-select":
		os.Exit(runLibexSelect(os.Args[2:]))
	case "-h", "--help", "help":
		usage()
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "metaimport: unknown source %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

// boundedSource is the one source whose operator permits the two
// catalogue-bounded planning modes (--enrich and --recordings-only), so both
// flags are accepted for it alone. The importer core supports the modes for any
// source; restricting them here keeps the licensing posture a deliberate,
// per-source decision rather than a flag anyone can point at any export.
const boundedSource = "libex"

// runSource parses the shared flags for a source subcommand and runs its
// importer (Run for openaudible, RunLibation for libation, RunLibex for libex).
func runSource(name string, args []string, run func(string, importer.Options) (importer.Summary, error)) int {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	data := fs.String("data", "data", "path to the data directory")
	dryRun := fs.Bool("dry-run", false, "print the plan without writing any files")
	date := fs.String("date", "", "imported_at stamp (YYYY-MM-DD); defaults to today (UTC)")
	// Registered for every source so pointing one at the wrong one produces a
	// clear refusal instead of flag's bare "not defined" line.
	enrich := fs.Bool("enrich", false, "fill absent facts on ASIN-matched existing records instead of creating any (libex only)")
	recordingsOnly := fs.Bool("recordings-only", false, "add alternate narrations to works already in the catalogue; never create a work or touch a series (libex only)")

	// Accept the positional export path either before or after the flags.
	exportPath, err := parsePositional(fs, args, "<export.json>")
	if err != nil {
		fmt.Fprintln(os.Stderr, "metaimport:", err)
		usage()
		return 2
	}
	for _, mode := range []struct {
		flag string
		on   bool
	}{{"--enrich", *enrich}, {"--recordings-only", *recordingsOnly}} {
		if mode.on && name != boundedSource {
			fmt.Fprintf(os.Stderr, "metaimport: %s is only supported for the %s source, not %q\n", mode.flag, boundedSource, name)
			return 2
		}
	}
	if *enrich && *recordingsOnly {
		fmt.Fprintln(os.Stderr, "metaimport: --enrich and --recordings-only are different modes; pass one or the other")
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
		DataDir:        *data,
		ImportDate:     stamp,
		DryRun:         *dryRun,
		Enrich:         *enrich,
		RecordingsOnly: *recordingsOnly,
	})

	printSummary(sum, *dryRun, *enrich, *recordingsOnly)
	if err != nil {
		fmt.Fprintln(os.Stderr, "metaimport:", err)
		return 1
	}
	return 0
}

// runLibexSelect parses the flags for the libex-select subcommand and runs the
// subset selector. It writes no records: it reads a full libex export, picks
// the rows that complete series the catalogue already tracks, and re-emits
// them as NDJSON for a later `metaimport libex` run. A completed run prints the
// report a maintainer reviews the tranche from; an aborted one prints how far
// it got instead, since a partial report is not a tranche.
func runLibexSelect(args []string) int {
	fs := flag.NewFlagSet("libex-select", flag.ContinueOnError)
	data := fs.String("data", "data", "path to the data directory")
	out := fs.String("o", "", "path to write the selected rows to (NDJSON)")
	maxPerSeries := fs.Int("max-per-series", 0, "cap the new works selected per catalogue series (0 = unlimited)")

	exportPath, err := parsePositional(fs, args, "<export.ndjson>")
	if err != nil {
		fmt.Fprintln(os.Stderr, "metaimport:", err)
		usage()
		return 2
	}
	if *out == "" {
		fmt.Fprintf(os.Stderr, "metaimport: libex-select needs -o <subset.ndjson>\n")
		usage()
		return 2
	}
	if *maxPerSeries < 0 {
		fmt.Fprintf(os.Stderr, "metaimport: --max-per-series must not be negative\n")
		return 2
	}

	res, err := importer.SelectLibex(exportPath, *out, importer.SelectOptions{
		DataDir:      *data,
		MaxPerSeries: *maxPerSeries,
	})
	if err != nil {
		// The report is deliberately NOT printed here. A run that aborted
		// mid-stream has counted only the rows it reached, so printing it
		// would render a confident, all-but-empty breakdown of an export that
		// was never fully read - the one output an operator must not trust.
		fmt.Fprintln(os.Stderr, "metaimport:", err)
		fmt.Printf("aborted after %d rows; no output written\n", res.RowsRead)
		return 1
	}
	fmt.Print(res.Report())
	fmt.Printf("wrote %s\n", *out)
	return 0
}

// parsePositional parses a subcommand's flags and returns its single positional
// argument, accepting the flags before it, after it, or both. label names the
// argument in the error.
//
// Go's flag package stops parsing at the first non-flag argument, so neither
// plain fs.Parse nor a "first argument not starting with -" split can read both
// orders: the split reads a FLAG'S VALUE as the positional, which turned
// `libex-select -o subset.ndjson full.ndjson` into "read subset.ndjson, write
// full.ndjson" and truncated the operator's export. Parsing in rounds hands the
// FlagSet - the only thing that knows which flags take a value - every
// remaining argument.
func parsePositional(fs *flag.FlagSet, args []string, label string) (string, error) {
	var positional []string
	rest := args
	for {
		if err := fs.Parse(rest); err != nil {
			return "", err
		}
		rest = fs.Args()
		if len(rest) == 0 {
			break
		}
		positional = append(positional, rest[0])
		rest = rest[1:]
	}
	switch {
	case len(positional) == 0:
		return "", fmt.Errorf("missing %s path", label)
	case len(positional) > 1:
		return "", fmt.Errorf("expected one %s path, got %d: %s", label, len(positional), strings.Join(positional, " "))
	}
	return positional[0], nil
}

// printSummary renders the run's outcome. Each mode reports its own counters -
// the ones it structurally cannot move are all zero and would only be noise -
// while sharing the warning list. Enrichment's row accounting is deliberately
// printed as an identity - rows read = matched + not in the catalogue + skipped
// at parse - so a row can never go missing without the line failing to add up.
func printSummary(s importer.Summary, dryRun, enrich, recordingsOnly bool) {
	var head string
	switch {
	case enrich && dryRun:
		head = "enrichment plan (dry run, no files written)"
	case enrich:
		head = "enriched"
	case recordingsOnly && dryRun:
		head = "recordings-only plan (dry run, no files written)"
	case recordingsOnly:
		head = "added"
	case dryRun:
		head = "plan (dry run, no files written)"
	default:
		head = "imported"
	}
	switch {
	case enrich:
		rows := s.Matched + s.NotInCatalog + s.SkippedRows
		fmt.Printf("%s: %d works, %d recordings; %d works placed in a series; %d rows read = %d matched + %d not in the catalogue + %d skipped at parse; %d warnings\n",
			head, s.EnrichedWorks, s.EnrichedRecordings, s.SeriesPlacements,
			rows, s.Matched, s.NotInCatalog, s.SkippedRows, len(s.Warnings))
	case recordingsOnly:
		fmt.Printf("%s: %d new recordings, %d new people; %d skipped (already present); %d skipped (work not in the catalogue); %d asins merged into existing recordings; %d warnings\n",
			head, s.NewRecordings, s.NewPeople, s.Skipped, s.SkippedNoWork, s.MergedASINs, len(s.Warnings))
	default:
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
	fmt.Fprintln(os.Stderr, "  metaimport libex       <export.json> [--data data] [--dry-run] [--date YYYY-MM-DD] [--enrich | --recordings-only]")
	fmt.Fprintln(os.Stderr, "  metaimport libex-select <export.ndjson> -o <subset.ndjson> [--data data] [--max-per-series N]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "  --enrich (libex only) fills absent facts on ASIN-matched existing records; it never creates.")
	fmt.Fprintln(os.Stderr, "  --recordings-only (libex only) adds alternate narrations to works already in the catalogue;")
	fmt.Fprintln(os.Stderr, "    it never creates a work and never touches a series.")
}
