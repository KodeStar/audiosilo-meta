package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"github.com/kodestar/audiosilo-meta/internal/atomicfile"
	"github.com/kodestar/audiosilo-meta/internal/audit"
	"github.com/kodestar/audiosilo-meta/internal/importer"
)

// runLibrivox dispatches `metaimport librivox fetch|import` (LICENSING.md's
// public-domain exception; internal/importer/librivox.go and librivoxfetch.go).
//
//	metaimport librivox fetch  --out <snapshot> [--max-pages N]
//	metaimport librivox import --from <snapshot> --ids <range> [--data data] [--dry-run] [--date YYYY-MM-DD] [--conflicts <path>]
//	metaimport librivox import --from <snapshot> --ids <range> --regenerate-genres --rows-as-of YYYY-MM-DD [--genre-changes <path>] [--data data] [--dry-run]
//
// fetch is the ONLY command in this repository that calls LibriVox, and it does
// so politely: 100 projects a page, five seconds between requests, a User-Agent
// naming meta.audiosilo.app and kode@audiosilo.app, Retry-After honoured on 429
// and 5xx, resumable from the snapshot's manifest. The snapshot is local working
// state and is never committed. import reads a snapshot offline and lands the
// projects whose ids --ids selects (a backfill tranche is a range, about 2,000
// projects a pull request).
func runLibrivox(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "metaimport librivox: name a step: fetch or import")
		usage()
		return 2
	}
	switch args[0] {
	case "fetch":
		return runLibrivoxFetch(args[1:], importer.NewLibrivoxFetcher())
	case "import":
		return runLibrivoxImport(args[1:], importer.RunLibrivox)
	default:
		fmt.Fprintf(os.Stderr, "metaimport librivox: unknown step %q (fetch or import)\n", args[0])
		return 2
	}
}

// runLibrivoxFetch parses the fetch flags and pages the feed into the snapshot.
// An interrupt stops it between requests; the snapshot resumes from there.
func runLibrivoxFetch(args []string, f *importer.LibrivoxFetcher) int {
	fs := flag.NewFlagSet("librivox fetch", flag.ContinueOnError)
	out := fs.String("out", "", "the snapshot directory to write (created if absent; an existing one is resumed)")
	maxPages := fs.Int("max-pages", 0, "stop after this many pages (0 = run to the end of the catalogue)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *out == "" || fs.NArg() > 0 {
		fmt.Fprintln(os.Stderr, "metaimport librivox fetch: --out <snapshot> is required and no positional argument is taken")
		return 2
	}
	if *maxPages < 0 {
		fmt.Fprintln(os.Stderr, "metaimport librivox fetch: --max-pages must be 0 or more")
		return 2
	}
	if f.Log == nil {
		f.Log = os.Stderr
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	res, err := f.Fetch(ctx, *out, *maxPages)
	state := "not complete (rerun to resume)"
	if res.Complete {
		state = "complete"
	}
	fmt.Printf("librivox fetch: %d pages, %d projects this run (%d requests); snapshot holds %d projects, next offset %d, %s\n",
		res.PagesFetched, res.Projects, res.Requests, res.TotalProjects, res.NextOffset, state)
	if err != nil {
		fmt.Fprintln(os.Stderr, "metaimport librivox fetch:", err)
		return 1
	}
	return 0
}

// runLibrivoxImport parses the import flags and runs the importer over the
// snapshot. --regenerate-genres switches it from creating records to the genre
// regeneration (internal/importer's regenerate.go) over the works holding the
// selected projects: how a change to the LibriVox genre table
// (model.LibrivoxGenres) reaches the tranches already written. It takes the
// libex regeneration's --rows-as-of and --genre-changes, and refuses the
// create-only --date and --conflicts, which it would silently ignore (it stamps
// no source and refuses no row).
func runLibrivoxImport(args []string, run func(string, importer.LibrivoxIDs, importer.Options) (importer.Summary, error)) int {
	fs := flag.NewFlagSet("librivox import", flag.ContinueOnError)
	from := fs.String("from", "", "the snapshot directory `metaimport librivox fetch` wrote")
	idsFlag := fs.String("ids", "", "the project ids to import: 527, 1-2000, 5000- or a comma-separated union (required)")
	data := fs.String("data", "data", "path to the data directory")
	dryRun := fs.Bool("dry-run", false, "print the plan without writing any files")
	date := fs.String("date", "", "imported_at stamp (YYYY-MM-DD); defaults to today (UTC)")
	conflicts := fs.String("conflicts", "", "append one NDJSON row per refused contradiction to this file")
	regenerate := fs.Bool("regenerate-genres", false, "re-derive the genres of the works holding the selected projects under today's LibriVox genre table (the recording vote; a trim only where every recording met a row); touches nothing else")
	rowsAsOf := fs.String("rows-as-of", "", "with --regenerate-genres (REQUIRED there): the YYYY-MM-DD day of the snapshot's rows; a work whose newest provenance is later is not judged")
	genreChanges := fs.String("genre-changes", "", "with --regenerate-genres: write one NDJSON line per changed work ({\"work\",\"removed\",\"added\",\"mode\"}) to this file")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *from == "" || fs.NArg() > 0 {
		fmt.Fprintln(os.Stderr, "metaimport librivox import: --from <snapshot> is required and no positional argument is taken")
		return 2
	}
	ids, err := importer.ParseLibrivoxIDs(*idsFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, "metaimport librivox import:", err)
		return 2
	}
	mode := importer.ModeCreate
	if *regenerate {
		mode = importer.ModeRegenerateGenres
	}
	explicit := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { explicit["--"+f.Name] = true })
	for _, f := range []struct {
		name  string
		regen bool // valid only under --regenerate-genres (else: only without it)
	}{{"--date", false}, {"--conflicts", false}, {"--rows-as-of", true}, {"--genre-changes", true}} {
		if explicit[f.name] && f.regen != *regenerate {
			if f.regen {
				fmt.Fprintf(os.Stderr, "metaimport librivox import: %s is valid with --regenerate-genres only\n", f.name)
			} else {
				fmt.Fprintf(os.Stderr, "metaimport librivox import: %s does nothing under --regenerate-genres, which writes genres only and stamps no source\n", f.name)
			}
			return 2
		}
	}
	if err := importer.ValidateRowsAsOf(mode, *rowsAsOf, time.Now().UTC()); err != nil {
		fmt.Fprintln(os.Stderr, "metaimport librivox import: --rows-as-of:", err)
		return 2
	}
	stamp, ok := runDate(*date)
	if !ok {
		return 2
	}
	conflictLog, closeLog, err := openConflictLog(*conflicts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "metaimport librivox import:", err)
		return 2
	}
	defer closeLog()
	// Staged before the run, so an unwritable path fails before the tree is
	// touched (the libex regeneration's rule, main.go).
	changesLog, err := atomicfile.StageIf(*genreChanges)
	if err != nil {
		fmt.Fprintln(os.Stderr, "metaimport librivox import: --genre-changes:", err)
		return 2
	}
	defer changesLog.Discard() // a no-op once committed
	sum, err := run(*from, ids, importer.Options{
		DataDir: *data, ImportDate: stamp, DryRun: *dryRun, Mode: mode, RowsAsOf: *rowsAsOf,
		Conflicts: conflictLog, Distinct: audit.ReviewedDistinct(),
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "metaimport librivox import:", err)
		return 1
	}
	if mode == importer.ModeRegenerateGenres {
		fmt.Printf("librivox: %d projects selected by --ids %s\n", sum.LibrivoxProjects, strings.TrimSpace(*idsFlag))
		printSummary(sum, *dryRun, mode)
		if !*dryRun {
			fmt.Printf("  %d pack files written\n", len(sum.Files))
		}
		for _, c := range sum.GenreChanges {
			changesLog.Encode(c)
		}
		if err := atomicfile.CommitInOrder(changesLog); err != nil {
			fmt.Fprintln(os.Stderr, "metaimport librivox import: --genre-changes:", err)
			return 1
		}
		return 0
	}
	fmt.Printf("librivox: %d projects selected by --ids %s; %d LibriVox ids recorded on people\n",
		sum.LibrivoxProjects, strings.TrimSpace(*idsFlag), sum.LibrivoxIDsStamped)
	if sum.LibrivoxReadersSplit > 0 || sum.LibrivoxReadersMoved > 0 {
		fmt.Printf("librivox: %d %s whose name another reader id holds (a person of their own); %d narrator %s moved onto them on held recordings\n",
			sum.LibrivoxReadersSplit, plural(sum.LibrivoxReadersSplit, "reader", "readers"),
			sum.LibrivoxReadersMoved, plural(sum.LibrivoxReadersMoved, "credit", "credits"))
	}
	if n := sum.LibrivoxReadersUnslugged; n > 0 {
		fmt.Printf("librivox: %d %s whose name slugs to nothing (a person at librivox-reader-<reader id>)\n", n, plural(n, "reader", "readers"))
	}
	printLibrivoxInProgress(sum.LibrivoxInProgress)
	// The spelling-variant attachments are a run note (printSummary prints it).
	if n := sum.LibrivoxFallbackSlugs; n > 0 {
		fmt.Printf("librivox: %d new %s at librivox-<id> (non-Latin or unsluggable title)\n", n, plural(n, "work", "works"))
	}
	printSummary(sum, *dryRun, importer.ModeCreate)
	if !*dryRun {
		fmt.Printf("  %d pack files written\n", len(sum.Files))
	}
	printLibrivoxWorklist(sum.LibrivoxWorklist)
	printUnrecordedCredits(sum.UnrecordedCredits)
	return 0
}

// printLibrivoxInProgress prints the projects the run set aside as still in
// progress (importer.Summary.LibrivoxInProgress), their ids on one line.
// Nothing when there are none.
func printLibrivoxInProgress(ids []int) {
	if len(ids) == 0 {
		return
	}
	s := make([]string, len(ids))
	for i, id := range ids {
		s[i] = strconv.Itoa(id)
	}
	fmt.Printf("librivox: skipped %d in-progress %s (no runtime and no archive.org item; nothing written, re-import once complete): %s\n",
		len(ids), plural(len(ids), "project", "projects"), strings.Join(s, ", "))
}

// printLibrivoxWorklist prints the projects the run skipped for a person to
// resolve, one line each (importer.LibrivoxWorklistEntry.String): the project
// id, its title and authors, the reason code and the catalogued works it names.
// Nothing when the run skipped none.
func printLibrivoxWorklist(entries []importer.LibrivoxWorklistEntry) {
	if len(entries) == 0 {
		return
	}
	fmt.Printf("librivox worklist: %d %s skipped for a person to resolve (re-import the ids once settled):\n",
		len(entries), plural(len(entries), "project", "projects"))
	for _, e := range entries {
		fmt.Println("  " + e.String())
	}
}
