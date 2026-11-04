package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"

	"github.com/kodestar/audiosilo-meta/internal/atomicfile"
	"github.com/kodestar/audiosilo-meta/internal/audit"
	"github.com/kodestar/audiosilo-meta/internal/importer"
)

// runLibrivox dispatches `metaimport librivox fetch|import` (LICENSING.md's
// public-domain exception; internal/importer/librivox.go and librivoxfetch.go).
//
//	metaimport librivox fetch  --out <snapshot> [--max-pages N]
//	metaimport librivox import --from <snapshot> --ids <range> [--data data] [--dry-run] [--date YYYY-MM-DD] [--conflicts <path>] [--skipped <path>]
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
// snapshot.
func runLibrivoxImport(args []string, run func(string, importer.LibrivoxIDs, importer.Options) (importer.Summary, error)) int {
	fs := flag.NewFlagSet("librivox import", flag.ContinueOnError)
	from := fs.String("from", "", "the snapshot directory `metaimport librivox fetch` wrote")
	idsFlag := fs.String("ids", "", "the project ids to import: 527, 1-2000, 5000- or a comma-separated union (required)")
	data := fs.String("data", "data", "path to the data directory")
	dryRun := fs.Bool("dry-run", false, "print the plan without writing any files")
	date := fs.String("date", "", "imported_at stamp (YYYY-MM-DD); defaults to today (UTC)")
	conflicts := fs.String("conflicts", "", "append one NDJSON row per refused contradiction to this file")
	skipped := fs.String("skipped", "", "write one NDJSON line per project the run refused for a reason with a refusal code ({\"librivox_id\",\"reason\"}) to this file")
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
	// Staged before the run, so a path that cannot be written fails before the
	// import touches the tree, and renamed into place only once it completed
	// (the libex --skipped writer, one RowSkip per line).
	skippedLog, err := atomicfile.StageIf(*skipped)
	if err != nil {
		fmt.Fprintln(os.Stderr, "metaimport librivox import: --skipped:", err)
		return 2
	}
	defer skippedLog.Discard() // a no-op once committed
	sum, err := run(*from, ids, importer.Options{
		DataDir: *data, ImportDate: stamp, DryRun: *dryRun, Conflicts: conflictLog, Distinct: audit.ReviewedDistinct(),
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "metaimport librivox import:", err)
		return 1
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
	if r := sum.LibrivoxRestated; r != (importer.LibrivoxRestated{}) {
		fmt.Printf("librivox: %d %s restated (the project's own title, genres, translators, section numbers and readers); "+
			"%d without the project's sections as chapters, %d with an unresolved section reader, %d %s created, %d unresolved translator %s\n",
			r.Recordings, plural(r.Recordings, "recording", "recordings"), r.Chapterless, r.ReadersUnresolved,
			r.TranslatorsCreated, plural(r.TranslatorsCreated, "translator", "translators"),
			r.TranslatorsUnresolved, plural(r.TranslatorsUnresolved, "credit", "credits"))
		if r.TranslatorCreditsDropped > 0 {
			fmt.Printf("librivox: %d recording translator %s dropped (the row names other translators, every one resolved)\n",
				r.TranslatorCreditsDropped, plural(r.TranslatorCreditsDropped, "credit", "credits"))
		}
		if r.StaleReadersDropped > 0 {
			fmt.Printf("librivox: %d chapter %s dropped from recordings whose row lists no sections (no longer narrators)\n",
				r.StaleReadersDropped, plural(r.StaleReadersDropped, "reader", "readers"))
		}
		fmt.Printf("librivox: life years filled on %d %s; %d %s a year LibriVox contradicts (kept, warned)\n",
			r.LifeYearsFilled, plural(r.LifeYearsFilled, "person", "people"),
			r.LifeYearConflicts, plural(r.LifeYearConflicts, "person states", "people state"))
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
	printLibrivoxAuthorPairs(sum.LibrivoxAuthorPairs)
	printUnrecordedCredits(sum.UnrecordedCredits)
	for _, s := range sum.Skips {
		skippedLog.Encode(s)
	}
	if err := atomicfile.CommitInOrder(skippedLog); err != nil {
		fmt.Fprintln(os.Stderr, "metaimport librivox import: --skipped:", err)
		return 1
	}
	return 0
}

// printLibrivoxAuthorPairs prints the author id pairs the run kept as two
// people for a person to merge or confirm, one line each
// (importer.LibrivoxAuthorPair.String). Nothing when there are none.
func printLibrivoxAuthorPairs(pairs []importer.LibrivoxAuthorPair) {
	if len(pairs) == 0 {
		return
	}
	fmt.Printf("librivox worklist: %d author id %s one courtesy title apart, kept as two people for a person to merge or confirm:\n",
		len(pairs), plural(len(pairs), "pair", "pairs"))
	for _, pr := range pairs {
		fmt.Println("  " + pr.String())
	}
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
