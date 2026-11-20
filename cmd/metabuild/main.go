// Command metabuild compiles the data/ tree into a SQLite artifact. It runs the
// full validation first and refuses to build invalid data.
//
// A work's added_at comes from the record itself. The pack layout moved that
// value into the data - a pack's add-date is not its entries' add-dates, so the
// git-history derivation a file-per-work tree allowed is gone - which is why
// there is no date file to pass in and no --added flag.
//
// --community names a SECOND data root holding the works-community family alone,
// for the release build over two checkouts once the CC BY-SA layer lives in its
// own repository. With it, -data is read as the CC0 core and the two catalogues
// are composed into one artifact, cross-tree rules included (build.Sources ->
// check.LoadComposed). Without it nothing changes: one root holding the whole
// database, which is what this repository is today.
//
// ADVISORIES go to stderr with metacheck's own "advisory:" prefix and census
// line, and never affect the exit status. That matters most in composed mode: the
// position-scale rule and the retired-sidecar-key rule fire for real only where
// both trees are present, so the release build is the ONE place they are
// observable once the split lands - a binary that discarded them would make them
// unobservable anywhere.
//
// --previous and --revised-at (each requires the other) date the artifact's
// pages after it is built: --previous names the previous data release's
// artifact, and every page whose content differs from it, or is new, is stamped
// --revised-at while every unchanged page keeps the previous release's date
// (internal/revision). --previous-tag records that release's tag in
// meta(revisions_base). Without them the page_revisions table is left empty and
// the artifact is exactly what the data alone builds.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/kodestar/audiosilo-meta/internal/build"
	"github.com/kodestar/audiosilo-meta/internal/revision"
	"github.com/kodestar/audiosilo-meta/pkg/check"
	"github.com/kodestar/audiosilo-meta/pkg/query"
)

// digestWorkers caps how many pages the revision pass digests at once. More is
// SLOWER, not faster: the reads contend inside the pure-Go SQLite driver, and on
// the real catalogue (500k pages, 20 cores) one artifact took 1m52s at 4 workers,
// 2m50s at 8 and longer at 16.
const digestWorkers = 4

func main() {
	dataDir := flag.String("data", "data", "path to the data directory")
	community := flag.String("community", "",
		"path to a second data root holding the works-community family alone; "+
			"-data is then read as the CC0 core and the two are composed into one artifact")
	out := flag.String("o", "meta.sqlite", "output SQLite file")
	builtAt := flag.String("built-at", "", "build timestamp (RFC3339); defaults to now (UTC)")
	previous := flag.String("previous", "",
		"the previous data release's artifact; dates every page by comparing with it (requires --revised-at)")
	previousTag := flag.String("previous-tag", "", "the previous release's tag, recorded as meta(revisions_base)")
	revisedAt := flag.String("revised-at", "",
		"RFC3339 stamp for a page that changed since --previous (requires --previous)")
	flag.Parse()

	if (*previous == "") != (*revisedAt == "") {
		fmt.Fprintln(os.Stderr, "metabuild: --previous and --revised-at must be given together")
		os.Exit(2)
	}
	if *previousTag != "" && *previous == "" {
		fmt.Fprintln(os.Stderr, "metabuild: --previous-tag needs --previous")
		os.Exit(2)
	}
	var revised time.Time
	if *revisedAt != "" {
		parsed, err := time.Parse(time.RFC3339, *revisedAt)
		if err != nil {
			fmt.Fprintln(os.Stderr, "metabuild: invalid --revised-at:", err)
			os.Exit(2)
		}
		revised = parsed
	}
	if *previous != "" {
		// Build deletes -o before writing it, so a --previous naming the same file
		// would be compared with the NEW artifact: every page "unchanged", every
		// date silently gone. And an unreadable predecessor is refused before the
		// build rather than after it.
		same, err := sameFile(*previous, *out)
		if err != nil {
			fmt.Fprintln(os.Stderr, "metabuild:", err)
			os.Exit(2)
		}
		if same {
			fmt.Fprintln(os.Stderr, "metabuild: --previous names the output file; give the previous release's artifact its own path")
			os.Exit(2)
		}
		prev, err := query.Open(*previous, "")
		if err != nil {
			fmt.Fprintln(os.Stderr, "metabuild: --previous:", err)
			os.Exit(1)
		}
		_ = prev.Close()
	}

	res := build.Load(build.Sources{Data: *dataDir, Community: *community})
	check.PrintAdvisories(os.Stderr, res)
	if !res.OK() {
		for _, p := range res.Problems {
			fmt.Fprintln(os.Stderr, p.String())
		}
		fmt.Fprintf(os.Stderr, "refusing to build: %d validation problem(s)\n", len(res.Problems))
		os.Exit(1)
	}

	var ts time.Time
	if *builtAt != "" {
		parsed, err := time.Parse(time.RFC3339, *builtAt)
		if err != nil {
			fmt.Fprintln(os.Stderr, "metabuild: invalid --built-at:", err)
			os.Exit(2)
		}
		ts = parsed
	}

	if err := build.Build(res.Catalog, *out, ts); err != nil {
		fmt.Fprintln(os.Stderr, "metabuild:", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "built %s: %d works, %d people, %d series\n",
		*out, len(res.Catalog.Works), len(res.Catalog.People), len(res.Catalog.Series))

	if *previous == "" {
		return
	}
	start := time.Now()
	sum, err := revision.Stamp(context.Background(), *out, revision.Options{
		Previous: *previous, Base: *previousTag, At: revised, Workers: min(runtime.GOMAXPROCS(0), digestWorkers),
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "metabuild:", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "revisions against %s: %d pages, %d changed (stamped %s), %d carried, %d unknown, in %s\n",
		*previous, sum.Pages(), sum.Changed, sum.At, sum.Carried, sum.Unknown, time.Since(start).Round(time.Second))
}

// sameFile reports whether a and b name one file: the same absolute path, or,
// when both exist, the same file reached through a link.
func sameFile(a, b string) (bool, error) {
	absA, err := filepath.Abs(a)
	if err != nil {
		return false, err
	}
	absB, err := filepath.Abs(b)
	if err != nil {
		return false, err
	}
	if absA == absB {
		return true, nil
	}
	infoA, errA := os.Stat(a)
	infoB, errB := os.Stat(b)
	if errA != nil || errB != nil {
		return false, nil
	}
	return os.SameFile(infoA, infoB), nil
}
