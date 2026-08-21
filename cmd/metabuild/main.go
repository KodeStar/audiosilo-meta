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
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/kodestar/audiosilo-meta/internal/build"
	"github.com/kodestar/audiosilo-meta/pkg/check"
)

func main() {
	dataDir := flag.String("data", "data", "path to the data directory")
	community := flag.String("community", "",
		"path to a second data root holding the works-community family alone; "+
			"-data is then read as the CC0 core and the two are composed into one artifact")
	out := flag.String("o", "meta.sqlite", "output SQLite file")
	builtAt := flag.String("built-at", "", "build timestamp (RFC3339); defaults to now (UTC)")
	flag.Parse()

	res := build.Load(build.Sources{Data: *dataDir, Community: *community})
	for _, w := range res.Warnings {
		fmt.Fprintf(os.Stderr, "advisory: %s\n", w.String())
	}
	if census := check.AdvisoryCensus(res.Warnings); census != "" {
		fmt.Fprintf(os.Stderr, "%s\n", census)
	}
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
}
