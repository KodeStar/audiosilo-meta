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
// --conflicts <path> additionally APPENDS a machine-readable worklist: one
// NDJSON row per row the contradiction guards refused (a runtime or release date
// disagreeing with the record its ASIN matched), naming both values, the record
// they disagree about and the run that found them. The run itself is unchanged -
// the same warnings, the same counters, the same writes - so the flag is safe to
// add to a real wave, and it is what turns "a warning scrolled past six hours
// ago" into a list a maintainer can sort. It works with --dry-run, which is the
// cheapest way to survey a dump's disagreements without touching data.
//
// A USER-library source (openaudible, libation, audiosilo-books) additionally
// ATTESTS what it matches: a row whose ASIN is already in the catalogue on a
// record seeded only from the libex mirror overwrites that record's facts with
// the ones the export states, and stamps the run's provenance on it, so real
// users become the provenance over time. Once a record carries any user
// attestation the ordinary rules resume - a recorded value wins, a
// contradicting row is refused and counted for review. See LICENSING.md's trust
// tiers; libex runs are unaffected.
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
	"io"
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
	conflicts := fs.String("conflicts", "", "append one NDJSON row per refused contradiction to this file (a durable worklist; the run is unchanged)")

	// Accept the positional export path either before or after the flags.
	exportPath, err := parsePositional(fs, args, "<export.json>")
	if err != nil {
		fmt.Fprintln(os.Stderr, "metaimport:", err)
		usage()
		return 2
	}
	mode, err := selectMode(name, *enrich, *recordingsOnly)
	if err != nil {
		fmt.Fprintln(os.Stderr, "metaimport:", err)
		return 2
	}

	stamp := *date
	if stamp == "" {
		stamp = time.Now().UTC().Format("2006-01-02")
	} else if !dateRE.MatchString(stamp) {
		fmt.Fprintf(os.Stderr, "metaimport: --date %q must be YYYY-MM-DD\n", stamp)
		return 2
	}

	conflictLog, closeLog, err := openConflictLog(*conflicts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "metaimport:", err)
		return 2
	}
	defer closeLog()

	sum, err := run(exportPath, importer.Options{
		DataDir:    *data,
		ImportDate: stamp,
		DryRun:     *dryRun,
		Mode:       mode,
		Conflicts:  conflictLog,
	})

	// The summary prints only on success. A run can fail BEFORE it plans anything
	// - a data tree still in the file-per-entity layout is refused at open - and
	// a "0 new works, 0 new recordings" plan line is fiction there, read as a
	// result rather than as a run that never happened.
	if err != nil {
		fmt.Fprintln(os.Stderr, "metaimport:", err)
		return 1
	}
	printSummary(sum, *dryRun, mode)
	return 0
}

// openConflictLog opens the --conflicts worklist for APPEND, returning the sink
// to hand the importer and the close to defer. An empty path is the no-flag
// case: a nil io.Writer, which is what makes the run byte-for-byte what it was.
//
// The returned writer is the *os.File itself, deliberately unbuffered: the
// importer writes one row per conflict, so a wave killed after six hours keeps
// every conflict it had already found. Append (rather than truncate) is what
// lets a dump split into chunks with `split -l` accumulate ONE worklist across
// every chunk's run.
//
// The nil is returned as an untyped io.Writer rather than a nil *os.File, which
// would be a non-nil interface holding a nil pointer - the importer's "was I
// given a worklist" check reads the interface.
func openConflictLog(path string) (io.Writer, func(), error) {
	if path == "" {
		return nil, func() {}, nil
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, func() {}, fmt.Errorf("--conflicts: %w", err)
	}
	return f, func() { _ = f.Close() }, nil
}

// selectMode maps the two mode flags onto the importer's single Mode, which is
// where their exclusivity stops being a rule and becomes a type: past this
// point there is one mode, so no layer downstream has a combination to police.
// Both flags are catalogue-bounded passes permitted for boundedSource alone, so
// pointing either at another source is refused here rather than silently
// honoured.
func selectMode(source string, enrich, recordingsOnly bool) (importer.Mode, error) {
	if enrich && recordingsOnly {
		return 0, fmt.Errorf("--enrich and --recordings-only are different modes; pass one or the other")
	}
	var (
		flagName string
		mode     importer.Mode
	)
	switch {
	case enrich:
		flagName, mode = "--enrich", importer.ModeEnrich
	case recordingsOnly:
		flagName, mode = "--recordings-only", importer.ModeRecordingsOnly
	default:
		return importer.ModeCreate, nil
	}
	if source != boundedSource {
		return 0, fmt.Errorf("%s is only supported for the %s source, not %q", flagName, boundedSource, source)
	}
	return mode, nil
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
	// Registered here for the same reason --enrich is registered for every
	// source: a flag pointed at the wrong subcommand should say why, not produce
	// flag's bare "not defined" line. Selection imports nothing, so there is no
	// record for a row to contradict and no worklist to write.
	conflicts := fs.String("conflicts", "", "not supported by libex-select (selection imports nothing, so it refuses no row)")

	exportPath, err := parsePositional(fs, args, "<export.ndjson>")
	if err != nil {
		fmt.Fprintln(os.Stderr, "metaimport:", err)
		usage()
		return 2
	}
	if *conflicts != "" {
		fmt.Fprintln(os.Stderr, "metaimport: --conflicts is only supported by the import subcommands, not libex-select: "+
			"selection writes no records, so no row can contradict one")
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

// printSummary renders the run's outcome for the selected mode. Each mode
// reports its own counters - the ones it structurally cannot move are all zero
// and would only be noise - while sharing the warning list and the dry-run
// heading rule (summaryHead). Enrichment's row accounting is deliberately
// printed as an identity - rows read = matched + not in the catalogue + skipped
// at parse - so a row can never go missing without the line failing to add up.
func printSummary(s importer.Summary, dryRun bool, mode importer.Mode) {
	switch mode {
	case importer.ModeEnrich:
		rows := s.Matched + s.NotInCatalog + s.SkippedRows
		fmt.Printf("%s: %d works, %d recordings; %d works placed in a series; %d rows read = %d matched + %d not in the catalogue + %d skipped at parse; %d warnings\n",
			summaryHead(mode, dryRun), s.EnrichedWorks, s.EnrichedRecordings, s.SeriesPlacements,
			rows, s.Matched, s.NotInCatalog, s.SkippedRows, len(s.Warnings))
	case importer.ModeRecordingsOnly:
		fmt.Printf("%s: %d new recordings, %d new people; %d skipped (already present); %d skipped (work not in the catalogue); %d asins merged into existing recordings; %d warnings\n",
			summaryHead(mode, dryRun), s.NewRecordings, s.NewPeople, s.Skipped, s.SkippedNoWork, s.MergedASINs, len(s.Warnings))
	case importer.ModeCreate:
		fmt.Printf("%s: %d new works, %d new recordings, %d new people, %d new series; %d skipped (already present); %d asins merged into existing recordings; %d warnings\n",
			summaryHead(mode, dryRun), s.NewWorks, s.NewRecordings, s.NewPeople, s.NewSeries, s.Skipped, s.MergedASINs, len(s.Warnings))
	}
	// Same rule as the trust-tier line below: printed only when the run actually
	// recorded a role, so every summary that predates contributor credits reads
	// exactly as it did. A seed wave needs this visible - the qualifiers used to
	// be stripped and discarded, and silently losing them again would look
	// identical to capturing them.
	if s.Credits > 0 {
		fmt.Printf("  recorded %d contributor credits from source-stated role qualifiers\n", s.Credits)
	}
	// The trust-tier line is printed only when a run moved something, so the
	// long-standing summary wording above is untouched for every run that did
	// not (every libex run, and any user import that met no mirror seed).
	if s.AttestedWorks+s.AttestedRecordings+s.Conflicts > 0 {
		fmt.Printf("  attested %d works and %d recordings that were previously libex-only; %d rows conflicted with a recorded value and were not applied\n",
			s.AttestedWorks, s.AttestedRecordings, s.Conflicts)
	}
	// The honorific merges are listed in FULL rather than counted. Every line is
	// the run deciding that two spellings are one human, which is the least
	// reversible thing an import does and the one an operator should read before
	// the wave is committed; there are a handful per wave, never a page.
	if len(s.HonorificMerges) > 0 {
		fmt.Printf("  resolved %d courtesy-title credit(s) onto a bare twin already credited on the same side:\n", len(s.HonorificMerges))
		for _, m := range s.HonorificMerges {
			fmt.Println("    honorific:", m)
		}
	}
	for _, w := range s.Warnings {
		fmt.Println("  warning:", w)
	}
}

// summaryHead is the summary line's leading verb. Each mode gets its own rather
// than borrowing "imported", which only the create mode ever does - and the
// create wording is long-standing output a user (and any log-reading habit)
// recognizes, so it stays exactly as it was.
func summaryHead(mode importer.Mode, dryRun bool) string {
	switch mode {
	case importer.ModeEnrich:
		if dryRun {
			return "enrichment plan (dry run, no files written)"
		}
		return "enriched"
	case importer.ModeRecordingsOnly:
		if dryRun {
			return "recordings-only plan (dry run, no files written)"
		}
		return "added"
	default:
		if dryRun {
			return "plan (dry run, no files written)"
		}
		return "imported"
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
	fmt.Fprintln(os.Stderr, "  --conflicts <path> appends one NDJSON row per refused contradiction (a durable worklist).")
	fmt.Fprintln(os.Stderr, "  --enrich (libex only) fills absent facts on ASIN-matched existing records; it never creates.")
	fmt.Fprintln(os.Stderr, "  --recordings-only (libex only) adds alternate narrations to works already in the catalogue;")
	fmt.Fprintln(os.Stderr, "    it never creates a work and never touches a series.")
}
