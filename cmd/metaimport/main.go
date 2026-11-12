// Command metaimport ingests an external audiobook-library export into the
// data/ tree as work/recording/person/series records, deduplicating against the
// existing catalog so a contributor's upload becomes a reviewable diff.
//
// Usage:
//
//	metaimport openaudible <books.json>  [--data data] [--dry-run] [--date YYYY-MM-DD]
//	metaimport libation    <export.json> [--data data] [--dry-run] [--date YYYY-MM-DD]
//	metaimport libex       <export.json> [--data data] [--dry-run] [--date YYYY-MM-DD] [--enrich | --recordings-only [--copies-of <path>] | --relocate | --regenerate-genres --rows-as-of YYYY-MM-DD [--genre-changes <path>]] [--existing-series-only] [--attach-editions] [--skipped <path>]
//	metaimport libex-select <export.ndjson> -o <subset.ndjson> [--data data] [--max-per-series N] [--attach-editions] [--reported] [--refusals <path>] [--attachments <path>] [--date YYYY-MM-DD]
//	metaimport librivox fetch  --out <snapshot> [--max-pages N]
//	metaimport librivox import --from <snapshot> --ids <range> [--data data] [--dry-run] [--date YYYY-MM-DD] [--conflicts <path>] [--skipped <path>] [--worklist <path>]
//	metaimport librivox import --from <snapshot> --ids <range> --regenerate-genres --rows-as-of YYYY-MM-DD [--genre-changes <path>] [--data data] [--dry-run]
//	metaimport store       <listings.ndjson> [--data data] [--dry-run] [--date YYYY-MM-DD] [--attach-editions] [--existing-series-only] [--conflicts <path>]
//
// store lands a maintainer's reviewed file of store listings (Audiobook Guild,
// GraphicAudio; LICENSING.md "Store listings"): the libex row shape with a
// `store` and its page `url` in place of an ASIN, facts only, through the
// create path's identity, duplicate and person rules (store.go and
// internal/importer/store.go).
//
// librivox (LICENSING.md's public-domain exception) is two steps: fetch pages
// LibriVox's audiobooks feed into a local snapshot - the one command here that
// calls LibriVox, politely and resumably (see librivox.go) - and import lands the
// snapshot projects --ids selects, offline, deduplicating on the project id.
// import --regenerate-genres is the LibriVox twin of libex's genre regeneration
// below, matched by project id: how a LibriVox genre table change reaches the
// works already imported.
//
// libex-select writes no records: it reduces a full libex export to the
// bounded, series-completing subset LICENSING.md's import posture allows, and
// prints a report for review; the subset is then imported with the normal
// `metaimport libex` create path.
//
// --reported (libex-select) also keeps a row stating NO series, under every
// refusal that does not read one, unless a held work by one of its authors has
// the same title, or the same title before or after a subtitle, which refuses it
// as identity-duplicate. It is for the sync bot's reported-ASIN lane
// (GOVERNANCE.md) and never for a full export; internal/importer/libexselect.go
// and reportedguard.go have the reasoning.
//
// --attach-editions (libex-select, and libex's create mode) turns on the attach
// rule (internal/importer/attach.go): a row claiming a series position the
// catalogue already fills is kept, and imported as another recording of the
// work there (or another ASIN on one), when the importer's identity machinery
// resolves it to exactly that work; every other row at an occupied position is
// refused - by the selector as position-claimed, and by the import too, which
// then never plans a second work at an occupied position. Off, both behave as
// they always have. The series-completion sync bot passes it to both.
//
// Three NDJSON worklists are a contract with the sync bot, all with the stable
// reason codes internal/importer/refusalcodes.go defines:
// libex-select --refusals <path> (one {"asin","reason"} line per refused row),
// libex-select --attachments <path> (one {"asin","work","series","position"}
// line per row kept for attachment - how a reader tells those rows from
// completions in the subset) and libex --skipped <path> (the import's own
// refusals in the --refusals shape - a malformed ASIN, an unmapped region or
// language, a refused credit, and position-claimed for a row --attach-editions
// turned away or attached without anything being written; the bot memoizes them,
// so a row the selector kept and the import refused is not selected again every
// cycle). Each is written atomically (internal/atomicfile): libex-select commits
// the subset last, and --skipped is staged before the import runs, so a bad path
// fails before the tree is touched.
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
// --series-lookup (user-library sources) fills a series position the export did
// not state. A personal library export names the series a file is tagged with
// and very often no part number, so the row warns and the work is left out of
// its series; with this flag the row's ASIN is looked up on the live libex
// service and its position used when libex names the SAME series. Off by
// default because it reaches the network: the flag is what an operator (or the
// intake bot) opts in with. --series-lookup-limit caps the lookups per run,
// counting only the rows that need one.
//
// --existing-series-only (every source) forbids the run from FOUNDING a series:
// a series claim that would create one - a name the catalogue does not hold, or
// a same-named series closed to the row's authors (the step to `<slug>-2`) - is
// dropped instead, counted and named in one aggregated warning. The row itself
// still imports and is placed in every claim that lands in a catalogued series,
// so the run's "new series" count is 0 by construction. The series-completion
// sync bot (audiosilo-meta-sync) passes it: its contract is that it never adds
// a series, and a row libex-select kept for one catalogued claim can carry a
// second, uncatalogued one (a translated edition's own series name).
//
// --enrich (libex only) switches from creating records to ENRICHING the ones
// already here: a row whose ASIN the catalogue does not hold is counted and
// ignored, and a matched row only fills facts the existing work/recording does
// not have. Nothing is ever created, so the run is bounded by this catalogue
// rather than by the source's. What it READS is not bounded, though: the parser
// streams the file, but planning holds every accepted row at once (the batch
// censuses read them all), so the raw dump still needs a machine sized for it
// (scripts/README.md step 7 has the figures) and the recommended input is a
// pre-filtered row set: the libex-select output, or rows filtered to catalogued
// ASINs at export time. The one replacement it makes is the POST-RELEASE
// REFRESH: a recording catalogued from a preorder listing, met by a released row
// of its own ASIN, takes that row's runtime, cover, chapters and release date
// (internal/importer/refresh.go).
//
// libex-fill selects the rows itself and fetches them from the libex service:
// by default the user-library recordings missing a cover or chapters;
// --released-preorders instead selects every preorder-era recording whose
// release date has passed (by --date) and refreshes it. With --dry-run it is the
// count of what a refresh would change, per field, without writing.
//
// --recordings-only (libex only) switches to adding ALTERNATE NARRATIONS to
// works the catalogue already holds: a row is resolved to an existing work by
// title and author set, and lands as a new recording under it (or its ASIN
// merges into a matching sibling recording). A row whose work is not here is
// counted and dropped - the mode never creates a work and never touches a series
// file. It is what closes the gap the other modes leave: a second narration of a
// catalogued book matches no ASIN (so --enrich ignores it), fills no free series
// position (so libex-select excludes it unless it can prove the row is the work at
// that position), and would mint a duplicate work on the
// create path. Mutually exclusive with --enrich.
//
// --copies-of <path> (valid with --recordings-only alone) attaches the rows
// it lists DIRECTLY: one {"asin":"<copy>","anchor":"<catalogued ASIN>"} JSON
// line per row, the rows themselves read from the run's usual export input. A
// listed row is another marketplace's copy of the production its anchor ASIN
// sits on, as the caller proved it (the sync bot: one libex skuGroup), so its
// ASIN is merged onto that recording without its credits or series ever being
// read - no person, work or series can be minted for it. The physical guards
// stay: an unknown anchor, another language, a runtime mismatch, an abridged
// edition beside an unabridged one and a region the recording already holds
// with another ASIN refuse the row, under copy-anchor-unknown, copy-language,
// copy-runtime-mismatch, copy-abridged and copy-region-held, which --skipped
// writes (internal/importer/copies.go). An unlisted row in the same run takes
// the ordinary recordings-only path. Of the parse layer's credit refusals
// only the AI one (ai-narrator) still applies to a listed row: an AI-narrated
// listing is another production. The others (a placeholder, list, platform
// account or unidentifiable credit, kana and kanji among them) guard against
// minting a person, which a listed row never does, so it skips them; an
// unlisted row keeps them all.
//
// --regenerate-genres (libex; and `librivox import`, librivox.go) re-derives the GENRES of catalogued works from
// the libex rows of their own recordings, under today's mapping table and the
// recording vote: a work no user-library source contributed to, whose every
// ASIN-carrying recording met a row, takes the vote as its set; every other work
// only gains what the vote adds. It touches no other field, creates nothing and
// stamps no source (the genres are a derivation of rows the work already
// cites); a second identical run is a no-op. --rows-as-of YYYY-MM-DD, the
// rows' snapshot date, is required: a work whose newest provenance is later was
// written from newer rows and is not judged. --genre-changes <path> writes one
// NDJSON line per changed work ({"work","removed","added","mode"}). Mutually
// exclusive with the other modes; the input is the rows of every catalogued
// ASIN (scripts/README.md).
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/kodestar/audiosilo-meta/internal/atomicfile"
	"github.com/kodestar/audiosilo-meta/internal/audit"
	"github.com/kodestar/audiosilo-meta/internal/importer"
)

var dateRE = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// runDate resolves a --date flag: today (UTC) when absent, else the value when
// it is YYYY-MM-DD. ok is false (with the error printed) otherwise. One helper,
// because the import and libex-select must read the same run day.
func runDate(flag string) (string, bool) {
	if flag == "" {
		return time.Now().UTC().Format("2006-01-02"), true
	}
	if !dateRE.MatchString(flag) {
		fmt.Fprintf(os.Stderr, "metaimport: --date %q must be YYYY-MM-DD\n", flag)
		return "", false
	}
	return flag, true
}

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
	case "libex-fill":
		os.Exit(runLibexFill(os.Args[2:]))
	case "narrations":
		os.Exit(runNarrations(os.Args[2:]))
	case "librivox":
		os.Exit(runLibrivox(os.Args[2:]))
	case "store":
		os.Exit(runStore(os.Args[2:]))
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
	relocate := fs.Bool("relocate", false, "move cross-language recordings to their stated-language work (libex only)")
	recordingsOnly := fs.Bool("recordings-only", false, "add alternate narrations to works already in the catalogue; never create a work or touch a series (libex only)")
	regenerateGenres := fs.Bool("regenerate-genres", false, "re-derive catalogued works' genres from their recordings' rows (the recording vote; a trim only where no user-library source contributed and every recording met a row); touches nothing else (libex only)")
	rowsAsOf := fs.String("rows-as-of", "", "with --regenerate-genres (REQUIRED there): the YYYY-MM-DD snapshot date of the rows; a work whose newest provenance is later is not judged")
	genreChanges := fs.String("genre-changes", "", "with --regenerate-genres: write one NDJSON line per changed work ({\"work\",\"removed\",\"added\",\"mode\"}) to this file")
	conflicts := fs.String("conflicts", "", "append one NDJSON row per refused contradiction to this file (a durable worklist; the run is unchanged)")
	// Registered for every source, like --enrich, so pointing it at the wrong one
	// says why. It only ever DOES anything on a user-library create run - the
	// importer's own gate (seriespos.go) - so a libex run ignores it silently
	// rather than being refused: the flag asks for a gap to be filled, and a libex
	// row has no such gap.
	seriesLookup := fs.Bool("series-lookup", false, "fill a missing series position by looking the row's ASIN up on the live libex service (user-library sources; off by default because it reaches the network)")
	seriesLookupLimit := fs.Int("series-lookup-limit", 0, "cap the series-position lookups per run, counting only the rows that need one (0 = the default cap, negative = no cap)")
	libexBase := fs.String("libex", importer.LibexBase, "libex base URL for --series-lookup")
	// Registered for every source because the rule is not a property of one: any
	// create run can be told it may only complete series the catalogue holds.
	// The catalogue-bounded modes never found a series, so it is inert there.
	existingSeriesOnly := fs.Bool("existing-series-only", false, "never found a series: drop (and report) a series claim that would create one; the row still imports")
	// Registered for every source so the wrong one is refused with a reason.
	skipped := fs.String("skipped", "", "write one NDJSON line per row the run refused for a reason with a libex-select refusal code ({\"asin\",\"reason\"}) to this file (libex only)")
	attachEditions := fs.Bool("attach-editions", false, "attach a row at a series position the catalogue already fills to the work there when it is another edition of it, and refuse it otherwise (libex only)")
	copiesOf := fs.String("copies-of", "", "with --recordings-only: attach each row this NDJSON file lists ({\"asin\",\"anchor\"} per line) to the recording its catalogued anchor ASIN sits on, its credits and series never read; refusals go to --skipped (libex only)")

	// Accept the positional export path either before or after the flags.
	exportPath, err := parsePositional(fs, args, "<export.json>")
	if err != nil {
		fmt.Fprintln(os.Stderr, "metaimport:", err)
		usage()
		return 2
	}
	mode, err := selectMode(name, *enrich, *recordingsOnly, *relocate, *regenerateGenres)
	if err != nil {
		fmt.Fprintln(os.Stderr, "metaimport:", err)
		return 2
	}
	// The flags that belong to one source or one mode, validated in one place.
	// A libex-only flag is refused for every other source; a mode-scoped one
	// outside the modes it does something in, so a flag that would be silently
	// inert is a refusal instead: --regenerate-genres writes nothing but genres
	// and stamps no source, so a date, a conflict worklist, a series rule or the
	// series lookup would all be ignored there.
	rules := append([]flagRule{
		{"--skipped", true, notRegen, "cannot report a --regenerate-genres run's refusals: a refused row whose ASIN the catalogue holds is not listed, and every other row matched nothing"},
		{"--attach-editions", true, func(m importer.Mode) bool { return m == importer.ModeCreate }, "attaches rows the CREATE path would plan; it is valid in that mode only"},
		{"--copies-of", true, func(m importer.Mode) bool { return m == importer.ModeRecordingsOnly }, "attaches copies in the recordings-only pass; it requires --recordings-only"},
		{"--existing-series-only", false, notRegen, inertInRegen},
		{"--series-lookup", false, notRegen, inertInRegen},
		{"--series-lookup-limit", false, notRegen, inertInRegen},
		{"--libex", false, notRegen, inertInRegen},
	}, regenFlagRules...)
	if refuseFlags("metaimport", fs, mode, name, rules) {
		return 2
	}

	// The regeneration never judges a record with evidence older than the
	// record, so it has to be told how old its rows are.
	if err := importer.ValidateRowsAsOf(mode, *rowsAsOf, time.Now().UTC()); err != nil {
		fmt.Fprintln(os.Stderr, "metaimport: --rows-as-of:", err)
		return 2
	}

	stamp, ok := runDate(*date)
	if !ok {
		return 2
	}

	var copies map[string]string
	if *copiesOf != "" {
		if copies, err = importer.ReadCopiesOf(*copiesOf); err != nil {
			fmt.Fprintln(os.Stderr, "metaimport: --copies-of:", err)
			return 2
		}
	}

	conflictLog, closeLog, err := openAppendLog("--conflicts", *conflicts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "metaimport:", err)
		return 2
	}
	defer closeLog()

	opts := importer.Options{
		DataDir:            *data,
		ImportDate:         stamp,
		DryRun:             *dryRun,
		Mode:               mode,
		Conflicts:          conflictLog,
		ExistingSeriesOnly: *existingSeriesOnly,
		AttachEditions:     *attachEditions,
		CopiesOf:           copies,
		RowsAsOf:           *rowsAsOf,
		Distinct:           audit.ReviewedDistinct(),
	}
	if *seriesLookup {
		client := importer.NewLibexClient()
		client.BaseURL = *libexBase
		opts.SeriesLookup = client
		opts.SeriesLookupLimit = *seriesLookupLimit
	}

	// The worklists are staged BEFORE the run, so a path that cannot be written
	// fails here, before the import touches the tree; they are committed together
	// (CommitInOrder: renamed into place only once both were written) once the run
	// completed. --skipped is one {"asin","reason"} line per refused row with a
	// refusal code - the --refusals shape and writer, so the sync bot reads both
	// with one reader; --genre-changes one line per work the genre regeneration
	// changed.
	skippedLog, err := atomicfile.StageIf(*skipped)
	if err != nil {
		fmt.Fprintln(os.Stderr, "metaimport: --skipped:", err)
		return 2
	}
	defer skippedLog.Discard() // a no-op once committed
	changesLog, err := atomicfile.StageIf(*genreChanges)
	if err != nil {
		fmt.Fprintln(os.Stderr, "metaimport: --genre-changes:", err)
		return 2
	}
	defer changesLog.Discard() // a no-op once committed

	sum, err := run(exportPath, opts)

	// The summary prints only on success. A run can fail BEFORE it plans anything
	// - a data tree still in the file-per-entity layout is refused at open - and
	// a "0 new works, 0 new recordings" plan line is fiction there, read as a
	// result rather than as a run that never happened.
	if err != nil {
		fmt.Fprintln(os.Stderr, "metaimport:", err)
		return 1
	}
	// The import completed, so its summary prints whatever happens to the
	// worklist after it: the tree is written, and the summary is the record of
	// what was.
	printSummary(sum, *dryRun, mode)
	printUnrecordedCredits(sum.UnrecordedCredits)
	for _, rows := range [][]importer.RowSkip{sum.Skips, sum.RelocationSkips} {
		for _, s := range rows {
			skippedLog.Encode(s)
		}
	}
	encodeGenreChanges(changesLog, sum)
	if err := atomicfile.CommitInOrder(skippedLog, changesLog); err != nil {
		fmt.Fprintln(os.Stderr, "metaimport: worklists:", err)
		return 1
	}
	return 0
}

// flagRule is one flag that belongs to one source or one mode (refuseFlags):
// libexOnly says it is for the catalogue-bounded source alone, valid (nil: every
// mode) which modes it does something in, and what is the refusal's wording.
type flagRule struct {
	name      string
	libexOnly bool
	valid     func(importer.Mode) bool
	what      string
}

// notRegen is the mode test of a flag the genre regeneration would ignore.
func notRegen(m importer.Mode) bool { return m != importer.ModeRegenerateGenres }

// regenOnly is the mode test of the genre regeneration's own flags.
func regenOnly(m importer.Mode) bool { return m == importer.ModeRegenerateGenres }

const inertInRegen = "does nothing under --regenerate-genres, which writes genres only and stamps no source"

// regenFlagRules are the genre regeneration's flag rules, shared by the libex
// command and `librivox import` (both run the regeneration): its own two flags,
// valid in that mode only, and the two create flags it would silently ignore.
// None is libexOnly: the mode test alone scopes them, since only those two
// commands can select the mode (selectMode refuses it for every other source).
var regenFlagRules = []flagRule{
	{"--genre-changes", false, regenOnly, "is the --regenerate-genres worklist; it is valid in that mode only"},
	{"--rows-as-of", false, regenOnly, "is the --regenerate-genres rows' snapshot date; it is valid in that mode only"},
	{"--date", false, notRegen, inertInRegen},
	{"--conflicts", false, notRegen, inertInRegen},
}

// refuseFlags validates the flags fs was given explicitly against rules, for a
// run of source in mode, printing the first refusal under the command's prefix:
// a flag that would be silently inert is a refusal instead. A libexOnly flag is
// refused for every source but the catalogue-bounded one (boundedSource).
func refuseFlags(prefix string, fs *flag.FlagSet, mode importer.Mode, source string, rules []flagRule) bool {
	explicit := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { explicit["--"+f.Name] = true })
	for _, f := range rules {
		switch {
		case !explicit[f.name]:
		case f.valid != nil && !f.valid(mode):
			fmt.Fprintf(os.Stderr, "%s: %s %s\n", prefix, f.name, f.what)
			return true
		case f.libexOnly && source != boundedSource:
			fmt.Fprintf(os.Stderr, "%s: %s is only supported for the %s source, not %q\n", prefix, f.name, boundedSource, source)
			return true
		}
	}
	return false
}

// encodeGenreChanges writes a genre regeneration's worklist (one line per changed
// work) to its staged file; a no-op for a run that changed none or a nil file.
func encodeGenreChanges(log *atomicfile.File, sum importer.Summary) {
	for _, c := range sum.GenreChanges {
		log.Encode(c)
	}
}

// openAppendLog opens a worklist flag's file (--conflicts, librivox import's
// --worklist) for APPEND, returning the sink
// to hand the importer and the close to defer. An empty path is the no-flag
// case: a nil io.Writer, which is what makes the run byte-for-byte what it was.
//
// The returned writer is the *os.File itself, deliberately unbuffered: the
// importer writes one row per conflict, so a wave killed after six hours keeps
// every conflict it had already found. Append (rather than truncate) is what
// lets several runs accumulate ONE worklist.
//
// The nil is returned as an untyped io.Writer rather than a nil *os.File, which
// would be a non-nil interface holding a nil pointer - the importer's "was I
// given a worklist" check reads the interface.
func openAppendLog(flag, path string) (io.Writer, func(), error) {
	if path == "" {
		return nil, func() {}, nil
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, func() {}, fmt.Errorf("%s: %w", flag, err)
	}
	return f, func() { _ = f.Close() }, nil
}

// selectMode maps the mode flags onto the importer's single Mode, which is
// where their exclusivity stops being a rule and becomes a type: past this
// point there is one mode, so no layer downstream has a combination to police.
// These flags are catalogue-bounded passes permitted for boundedSource alone, so
// pointing either at another source is refused here rather than silently
// honoured.
func selectMode(source string, enrich, recordingsOnly, relocate, regenerateGenres bool) (importer.Mode, error) {
	modes := []struct {
		flag string
		set  bool
		mode importer.Mode
	}{
		{"--enrich", enrich, importer.ModeEnrich},
		{"--recordings-only", recordingsOnly, importer.ModeRecordingsOnly},
		{"--relocate", relocate, importer.ModeRelocate},
		{"--regenerate-genres", regenerateGenres, importer.ModeRegenerateGenres},
	}
	var names, picked []string
	mode := importer.ModeCreate
	for _, m := range modes {
		names = append(names, m.flag)
		if m.set {
			picked, mode = append(picked, m.flag), m.mode
		}
	}
	switch {
	case len(picked) > 1:
		return 0, fmt.Errorf("%s and %s are mutually exclusive; pass one mode at most",
			strings.Join(names[:len(names)-1], ", "), names[len(names)-1])
	case len(picked) == 1 && source != boundedSource:
		return 0, fmt.Errorf("%s is only supported for the %s source, not %q", picked[0], boundedSource, source)
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
	attachEditions := fs.Bool("attach-editions", false, "keep a row at a series position the catalogue already fills when it is another edition of the work there, for `metaimport libex --attach-editions` to attach")
	reported := fs.Bool("reported", false, "also keep a row stating no series, under every refusal that does not read one, refusing it as identity-duplicate when a held work by one of its authors has the same title, with or without a subtitle, an \"&\" or a German \"und\" read as \"and\" (the sync bot's reported-ASIN lane; never over a full export, GOVERNANCE.md)")
	refusals := fs.String("refusals", "", "write one NDJSON line per refused row ({\"asin\",\"reason\"}, stable reason codes) to this file")
	attachments := fs.String("attachments", "", "write one NDJSON line per row selected for ATTACHMENT ({\"asin\",\"work\",\"series\",\"position\"}) to this file")
	date := fs.String("date", "", "the run day (YYYY-MM-DD) the preorder-estimate rule reads for a row carrying no updatedAt, as the import's --date does; defaults to today (UTC)")
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
	day, ok := runDate(*date)
	if !ok {
		return 2
	}

	res, err := importer.SelectLibex(exportPath, *out, importer.SelectOptions{
		DataDir:         *data,
		MaxPerSeries:    *maxPerSeries,
		AttachEditions:  *attachEditions,
		Reported:        *reported,
		RefusalsPath:    *refusals,
		AttachmentsPath: *attachments,
		ImportDate:      day,
		Distinct:        audit.ReviewedDistinct(),
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

// printUnrecordedCredits prints the author, translator and other role credits
// of landed rows that their work does not record (importer.UnrecordedCredit):
// the row attached to a work that does not name them, so the work gained no
// credit (a LibriVox translator who resolves is credited on its recording instead,
// meta#2752, which may create the person). A credit whose name slugs to nothing
// - an author's, a contributor's or a narrator's, any source - is listed too
// (reason unsluggable): it was left off rather than credited to the shared
// catch-all.
// Nothing when there are none, so every other run's report reads as before.
func printUnrecordedCredits(credits []importer.UnrecordedCredit) {
	if len(credits) == 0 {
		return
	}
	fmt.Printf("credits not recorded: %d (the row attached to a work that does not name them, or the name slugs to nothing and was left off; a LibriVox translator who resolves is credited on its recording instead):\n",
		len(credits))
	for _, c := range credits {
		fmt.Println("  " + c.String())
	}
}

// printSummary renders the run's outcome for the selected mode. Each mode
// reports its own counters - the ones it structurally cannot move are all zero
// and would only be noise - while sharing the warning list and the dry-run
// heading rule (summaryHead). Enrichment's row accounting is deliberately
// printed as an identity - rows read = matched + not in the catalogue + skipped
// at parse - so a row can never go missing without the line failing to add up.
func printSummary(s importer.Summary, dryRun bool, mode importer.Mode) {
	switch mode {
	case importer.ModeRelocate:
		fmt.Printf("%s: %d relocated-to-existing, %d relocated-to-new-work, %d merged-into-sibling; %d memberships re-pointed\n", summaryHead(mode, dryRun), s.RelocatedToExisting, s.RelocatedToNewWork, s.MergedIntoSibling, s.MembershipsRepointed)
		for _, code := range importer.RefusalCodes() {
			if n := s.RelocationRefusals[code]; n > 0 {
				fmt.Printf("  %s: %d recordings refused\n", code, n)
			}
		}
	case importer.ModeEnrich:
		rows := s.Matched + s.NotInCatalog + s.SkippedRows
		fmt.Printf("%s: %d works, %d recordings; %d works placed in a series; %d rows read = %d matched + %d not in the catalogue + %d skipped at parse; %d warnings\n",
			summaryHead(mode, dryRun), s.EnrichedWorks, s.EnrichedRecordings, s.SeriesPlacements,
			rows, s.Matched, s.NotInCatalog, s.SkippedRows, len(s.Warnings))
	case importer.ModeRegenerateGenres:
		rows := s.Matched + s.NotInCatalog + s.SkippedRows
		set, addedTo, added, removed := s.GenreTally()
		fmt.Printf("%s: %d works set to the recording vote, %d works added to, %d unchanged, %d held back as newer than the rows, %d not reached by any row; %d genres added, %d removed; %d rows read = %d matched + %d not in the catalogue + %d skipped at parse; %d warnings\n",
			summaryHead(mode, dryRun), set, addedTo, s.GenreWorksUnchanged, s.GenreWorksNewer, s.GenreWorksNoRow,
			added, removed, rows, s.Matched, s.NotInCatalog, s.SkippedRows, len(s.Warnings))
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
	if s.GenreWorks > 0 {
		fmt.Printf("  added genres to %d already-attested works\n", s.GenreWorks)
	}
	if s.ISBNRecordings > 0 {
		fmt.Printf("  added ISBNs to %d recordings from rows with nothing else to write there\n", s.ISBNRecordings)
	}
	// A store-listing run only (`metaimport store`); zero for every other source.
	if s.StoreLinksAdded > 0 {
		fmt.Printf("  added a store page to %d recordings the catalogue already held as that production\n", s.StoreLinksAdded)
	}
	if s.ISBNsMirrorOnly > 0 {
		fmt.Printf("  isbn-not-attached: mirror-only: %d %s without an ASIN met a bulk-mirror-only recording\n",
			s.ISBNsMirrorOnly, plural(s.ISBNsMirrorOnly, "row", "rows"))
	}
	// Printed only when a run refused one (--attach-editions), and on a
	// line of its own: the create summary line's shape is parsed by the sync bot.
	if s.SkippedOccupied > 0 {
		fmt.Printf("  skipped %d %s claiming a series position the catalogue already holds with another work (--attach-editions)\n",
			s.SkippedOccupied, plural(s.SkippedOccupied, "row", "rows"))
	}
	// Printed only when a run attached something (--attach-editions, the
	// attach rule in internal/importer/attach.go), so every other summary reads
	// exactly as it did. The attached rows are already inside the summary line's
	// "new recordings" and "asins merged" counts; this says which of those were
	// attachments.
	if s.Attached > 0 {
		fmt.Printf("  attached %d %s to the catalogued work already at %s series position (counted above as new recordings or merged asins)\n",
			s.Attached, plural(s.Attached, "row", "rows"), plural(s.Attached, "its", "their"))
	}
	// Printed only when a --copies-of run attached one; they are already inside
	// the summary line's "asins merged" count.
	if s.CopiesAttached > 0 {
		fmt.Printf("  attached %d listed %s to %s anchor's recording (--copies-of; counted above as merged asins)\n",
			s.CopiesAttached, plural(s.CopiesAttached, "copy", "copies"), plural(s.CopiesAttached, "its", "their"))
	}
	// The name merges are listed in FULL rather than counted. Every line is
	// the run deciding that two spellings are one human, which is the least
	// reversible thing an import does and the one an operator should read before
	// the wave is committed; there are a handful per wave, never a page.
	if len(s.HonorificMerges) > 0 {
		fmt.Printf("  resolved %d courtesy-title credit(s) onto a bare twin already credited on the same side:\n", len(s.HonorificMerges))
		for _, m := range s.HonorificMerges {
			fmt.Println("    honorific:", m)
		}
	}
	if len(s.CredentialMerges) > 0 {
		fmt.Printf("  resolved %d credentialed credit(s) onto a bare twin already credited on the same side:\n", len(s.CredentialMerges))
		for _, m := range s.CredentialMerges {
			fmt.Println("    credential:", m)
		}
	}
	// Notes come first: they say what the run DID (today, the AI-narration fold),
	// and a reader scanning past a warning list should not have to reach the end
	// of it to find out that a synthetic narration was admitted rather than
	// refused.
	for _, n := range s.Notes {
		fmt.Println("  note:", n)
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
	case importer.ModeRelocate:
		if dryRun {
			return "relocation plan (dry run, no files written)"
		}
		return "relocated"
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
	case importer.ModeRegenerateGenres:
		if dryRun {
			return "genre regeneration plan (dry run, no files written)"
		}
		return "regenerated genres"
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
	fmt.Fprintln(os.Stderr, "  metaimport libex       <export.json> [--data data] [--dry-run] [--date YYYY-MM-DD] [--enrich | --recordings-only [--copies-of <path>] | --relocate | --regenerate-genres --rows-as-of YYYY-MM-DD [--genre-changes <path>]] [--existing-series-only] [--attach-editions] [--skipped <path>]")
	fmt.Fprintln(os.Stderr, "  metaimport libex-select <export.ndjson> -o <subset.ndjson> [--data data] [--max-per-series N] [--attach-editions] [--reported] [--refusals <path>] [--attachments <path>] [--date YYYY-MM-DD]")
	fmt.Fprintln(os.Stderr, "  metaimport libex-fill  [--data data] [--works a,b] [--limit N] [--all-tiers | --released-preorders] [--dry-run] [--date YYYY-MM-DD]")
	fmt.Fprintln(os.Stderr, "  metaimport narrations  -o <worklist.ndjson> [--data data] [--works a,b] [--user-library] [--limit N] [--rows <rows.ndjson>]")
	fmt.Fprintln(os.Stderr, "  metaimport librivox fetch  --out <snapshot> [--max-pages N]")
	fmt.Fprintln(os.Stderr, "  metaimport librivox import --from <snapshot> --ids <range> [--data data] [--dry-run] [--date YYYY-MM-DD] [--conflicts <path>] [--skipped <path>] [--worklist <path>]")
	fmt.Fprintln(os.Stderr, "  metaimport librivox import --from <snapshot> --ids <range> --regenerate-genres --rows-as-of YYYY-MM-DD [--genre-changes <path>] [--data data] [--dry-run]")
	fmt.Fprintln(os.Stderr, "  metaimport store       <listings.ndjson> [--data data] [--dry-run] [--date YYYY-MM-DD] [--attach-editions] [--existing-series-only] [--conflicts <path>]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "  --conflicts <path> appends one NDJSON row per refused contradiction (a durable worklist).")
	fmt.Fprintln(os.Stderr, "  --series-lookup (user-library sources) fills a series position the export did not state by")
	fmt.Fprintln(os.Stderr, "    looking the row's ASIN up on the live libex service; it is used only when libex names the")
	fmt.Fprintln(os.Stderr, "    same series. --series-lookup-limit N caps the lookups per run (0 = the default cap of 100).")
	fmt.Fprintln(os.Stderr, "  --existing-series-only (every source) never founds a series: a claim that would create one is")
	fmt.Fprintln(os.Stderr, "    dropped and reported, and the row still imports into every catalogued series it claims.")
	fmt.Fprintln(os.Stderr, "  --attach-editions (libex-select; libex create) attaches a row at a filled series position to the")
	fmt.Fprintln(os.Stderr, "    work there when it is another edition of it, and refuses it otherwise; never a second work.")
	fmt.Fprintln(os.Stderr, "  --reported (libex-select) also keeps a row stating no series, under every refusal that does not read")
	fmt.Fprintln(os.Stderr, "    one, and refuses it as identity-duplicate when a held work by one of its authors has the same title,")
	fmt.Fprintln(os.Stderr, "    with or without a subtitle, an \"&\" or a German \"und\" read as \"and\". For the sync bot's reported-ASIN")
	fmt.Fprintln(os.Stderr, "    lane, which caps its own records; never over a full export.")
	fmt.Fprintln(os.Stderr, "  --refusals / --attachments (libex-select) and --skipped (libex, librivox import) write NDJSON worklists with")
	fmt.Fprintln(os.Stderr, "    stable reason codes, atomically.")
	fmt.Fprintln(os.Stderr, "  --enrich (libex only) fills absent facts on ASIN-matched existing records; it never creates.")
	fmt.Fprintln(os.Stderr, "  --recordings-only (libex only) adds alternate narrations to works already in the catalogue;")
	fmt.Fprintln(os.Stderr, "    it never creates a work and never touches a series. --copies-of <path> (with it) attaches each")
	fmt.Fprintln(os.Stderr, "    listed {\"asin\",\"anchor\"} row to the recording its catalogued anchor ASIN sits on, reading")
	fmt.Fprintln(os.Stderr, "    no credit or series; the physical guards refuse it under the copy-* codes --skipped writes.")
	fmt.Fprintln(os.Stderr, "  --relocate (libex only) moves cross-language recordings using all their source rows;")
	fmt.Fprintln(os.Stderr, "    it may create a work, never a series; --skipped records relocation refusals.")
	fmt.Fprintln(os.Stderr, "  --regenerate-genres (libex; librivox import) re-derives catalogued works' genres from their recordings'")
	fmt.Fprintln(os.Stderr, "    rows (the recording vote); --rows-as-of <the rows' snapshot date> is required, and a work")
	fmt.Fprintln(os.Stderr, "    with newer provenance is not judged; --genre-changes <path> writes one NDJSON line per changed work.")
	fmt.Fprintln(os.Stderr, "  librivox fetch pages LibriVox's audiobooks feed into a local snapshot, politely (100 a page,")
	fmt.Fprintln(os.Stderr, "    5 s apart, Retry-After honoured, resumable); the snapshot is never committed. librivox import")
	fmt.Fprintln(os.Stderr, "    lands the --ids it selects (527, 1-2000, 5000-), deduplicating on the LibriVox project id;")
	fmt.Fprintln(os.Stderr, "    with --regenerate-genres it re-derives the genres of the works holding them instead.")
	fmt.Fprintln(os.Stderr, "  libex-fill looks up the recordings that carry an ASIN but no cover (or no chapters) and")
	fmt.Fprintln(os.Stderr, "    enriches them from the live libex service. It covers USER-LIBRARY imports only unless")
	fmt.Fprintln(os.Stderr, "    --all-tiers is given; --works limits it further to those work ids. --released-preorders instead")
	fmt.Fprintln(os.Stderr, "    selects the preorder-era recordings whose release date has passed (by --date, default today)")
	fmt.Fprintln(os.Stderr, "    and refreshes their runtime, cover, chapters and release date from the released listing.")
	fmt.Fprintln(os.Stderr, "  narrations is READ-ONLY: it looks up on libex every ASIN of each recording holding two or more")
	fmt.Fprintln(os.Stderr, "    ASINs in one marketplace (or every recording of --works) and writes one NDJSON line per")
	fmt.Fprintln(os.Stderr, "    recording whose ASINs name disjoint narrators, or whose narrators none of its ASINs credit;")
	fmt.Fprintln(os.Stderr, "    --rows writes those ASINs' libex rows (the input libex --recordings-only reads).")
}

// runLibexFill parses the flags for the libex-fill subcommand and runs it.
//
// It is the automatic counterpart to `libex --enrich`: instead of being handed
// rows, it works out which recordings are missing a cover or a chapter list,
// fetches just those ASINs from the live libex service, and feeds the rows
// through the ordinary enrichment pass. Every guard that pass applies still
// applies - a runtime contradiction refuses its row here exactly as it would
// from a dump.
func runLibexFill(args []string) int {
	fs := flag.NewFlagSet("libex-fill", flag.ContinueOnError)
	dataDir := fs.String("data", "data", "data root")
	works := fs.String("works", "", "comma-separated work ids to limit the fill to (default: the whole catalogue)")
	limit := fs.Int("limit", 0, "stop after this many lookups (0 = no limit)")
	dryRun := fs.Bool("dry-run", false, "plan without writing files")
	date := fs.String("date", "", "YYYY-MM-DD stamp for source.imported_at (default: today)")
	conflicts := fs.String("conflicts", "", "append one NDJSON row per refused contradiction")
	base := fs.String("libex", importer.LibexBase, "libex base URL")
	allTiers := fs.Bool("all-tiers", false, "also fill bulk-mirror-seeded records (default: user-library imports only)")
	released := fs.Bool("released-preorders", false, "refresh the preorder-era recordings whose release date has passed, instead of filling gaps")
	if err := fs.Parse(args); err != nil {
		usage()
		return 2
	}
	day, ok := runDate(*date)
	if !ok {
		return 2
	}

	cat, err := importer.LoadCatalogForFill(*dataDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "metaimport: %v\n", err)
		return 1
	}

	var targets []importer.FillTarget
	switch {
	case *released:
		// A preorder-era record is bulk-mirror-only by definition, so the
		// user-library bound would select nothing: --all-tiers is implied.
		targets = importer.SelectRefreshTargets(cat, day)
	case !*allTiers:
		targets = importer.UserLibraryOnly(importer.SelectFillTargets(cat), cat)
	default:
		targets = importer.SelectFillTargets(cat)
	}
	if strings.TrimSpace(*works) != "" {
		targets = importer.RestrictToWorks(targets, parseIDSet(*works))
	}
	if *released {
		fmt.Printf("libex-fill: %d released preorder-era %s to refresh\n", len(targets), plural(len(targets), "recording", "recordings"))
	}
	if *limit > 0 && len(targets) > *limit {
		fmt.Fprintf(os.Stderr, "metaimport: %d recordings selected; --limit stops after %d\n", len(targets), *limit)
		targets = targets[:*limit]
	}
	if len(targets) == 0 {
		if *released {
			fmt.Println("libex-fill: nothing to refresh (no preorder-era recording has reached its release date)")
		} else {
			fmt.Println("libex-fill: nothing to fill (every ASIN-bearing recording already has a cover and chapters)")
		}
		return 0
	}

	rows, err := os.CreateTemp("", "libex-fill-*.ndjson")
	if err != nil {
		fmt.Fprintf(os.Stderr, "metaimport: %v\n", err)
		return 1
	}
	defer func() { _ = os.Remove(rows.Name()) }()

	client := importer.NewLibexClient()
	client.BaseURL = *base
	client.Day = day
	rep, err := client.FetchRows(context.Background(), targets, rows)
	if cerr := rows.Close(); err == nil && cerr != nil {
		err = cerr
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "metaimport: libex fetch: %v\n", err)
		return 1
	}
	fmt.Printf("libex-fill: looked up %d ASIN(s) = %d fetched + %d not in libex + %d failed; chapters: %d attached + %d rejected\n",
		rep.Requested, rep.Fetched, rep.NotFound, rep.Failed, rep.ChaptersFetched, rep.ChaptersRejected)
	for _, e := range rep.Errors {
		fmt.Fprintf(os.Stderr, "  warning: %s\n", e)
	}
	if rep.Fetched == 0 {
		return 0
	}

	opts := importer.Options{DataDir: *dataDir, ImportDate: day, DryRun: *dryRun, Mode: importer.ModeEnrich, Distinct: audit.ReviewedDistinct()}
	if *conflicts != "" {
		f, err := os.OpenFile(*conflicts, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "metaimport: %v\n", err)
			return 1
		}
		defer func() { _ = f.Close() }()
		opts.Conflicts = f
	}
	sum, err := importer.RunLibex(rows.Name(), opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "metaimport: %v\n", err)
		return 1
	}
	printSummary(sum, *dryRun, importer.ModeEnrich)
	return 0
}

// plural picks one or many by n, for the summary's additive lines.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// runNarrations parses the flags for the narrations subcommand and runs it: the
// merged-narration check (internal/importer/narrationcheck.go). It reads the tree
// and the libex service and writes only its worklist (and, with --rows, the libex
// rows of the ASINs it reports); nothing under --data is touched.
func runNarrations(args []string) int {
	fs := flag.NewFlagSet("narrations", flag.ContinueOnError)
	dataDir := fs.String("data", "data", "data root")
	works := fs.String("works", "", "comma-separated work ids: check every ASIN-bearing recording of these instead of the stored-facts suspects")
	limit := fs.Int("limit", 0, "check at most this many recordings (0 = no limit)")
	out := fs.String("o", "", "the NDJSON worklist to write (required)")
	rowsPath := fs.String("rows", "", "also write the libex rows of every ASIN in a finding to this NDJSON file")
	base := fs.String("libex", importer.LibexBase, "libex base URL")
	userLibrary := fs.Bool("user-library", false, "check only recordings carrying a user-library source (where library imports merge a second production)")
	if err := fs.Parse(args); err != nil {
		usage()
		return 2
	}
	if *out == "" {
		fmt.Fprintln(os.Stderr, "metaimport: narrations needs -o <worklist.ndjson>")
		return 2
	}
	worklist, err := atomicfile.Stage(*out)
	if err != nil {
		fmt.Fprintln(os.Stderr, "metaimport: -o:", err)
		return 2
	}
	defer worklist.Discard()
	rowsLog, err := atomicfile.StageIf(*rowsPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "metaimport: --rows:", err)
		return 2
	}
	defer rowsLog.Discard()

	cat, err := importer.LoadCatalogForFill(*dataDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "metaimport: %v\n", err)
		return 1
	}
	suspects := importer.SelectNarrationSuspects(cat, parseIDSet(*works))
	if *userLibrary {
		suspects = importer.UserLibraryNarrations(suspects, cat)
	}
	if *limit > 0 && len(suspects) > *limit {
		fmt.Fprintf(os.Stderr, "metaimport: %d recordings selected; --limit stops after %d\n", len(suspects), *limit)
		suspects = suspects[:*limit]
	}
	client := importer.NewLibexClient()
	client.BaseURL = *base
	// The rows are buffered only when --rows asked for them.
	var rows bytes.Buffer
	var rowsOut io.Writer
	if rowsLog != nil {
		rowsOut = &rows
	}
	findings, rep, err := client.CheckNarrations(context.Background(), suspects, rowsOut)
	if err != nil {
		fmt.Fprintf(os.Stderr, "metaimport: libex: %v\n", err)
		return 1
	}
	for _, f := range findings {
		worklist.Encode(f)
	}
	rowsLog.Write(rows.Bytes())
	if err := atomicfile.CommitInOrder(rowsLog, worklist); err != nil {
		fmt.Fprintln(os.Stderr, "metaimport: worklists:", err)
		return 1
	}
	fmt.Printf("narrations: %d %s checked, %d ASIN lookups = %d answered + %d not in libex + %d failed; %d %s\n",
		rep.Recordings, plural(rep.Recordings, "recording", "recordings"), rep.Lookups,
		rep.Lookups-rep.NotFound-rep.Failed, rep.NotFound, rep.Failed,
		rep.Findings, plural(rep.Findings, "finding", "findings"))
	for _, e := range rep.Errors {
		fmt.Fprintf(os.Stderr, "  warning: %s\n", e)
	}
	return 0
}

// parseIDSet reads a comma-separated id list flag into a set, blanks dropped.
func parseIDSet(list string) map[string]bool {
	set := map[string]bool{}
	for _, id := range strings.Split(list, ",") {
		if id = strings.TrimSpace(id); id != "" {
			set[id] = true
		}
	}
	return set
}
