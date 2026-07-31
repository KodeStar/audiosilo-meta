// Command metafmt enforces the on-disk form of data/**/*.json: canonical JSON
// (keys sorted alphabetically recursively, 2-space indent, LF, a single
// trailing newline, UTF-8 with no HTML escaping) and, for families already in
// pack layout, the structure - every entry in its bound-correct pack, every
// file a real pack, no duplicate entry, no pack or directory over its caps.
// --check lists the outstanding work, naming each fix, and exits 1; --write
// performs all of it in one converging pass. A file that cannot be read as a
// pack is only ever named: it needs a human, and --write exits 1 saying so.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/kodestar/audiosilo-meta/internal/format"
)

func main() {
	dataDir := flag.String("data", "data", "path to the data directory")
	checkMode := flag.Bool("check", false, "list the outstanding formatting and structural work and exit 1 if any")
	writeMode := flag.Bool("write", false, "format, relocate, salvage and split in place")
	flag.Parse()

	switch {
	case *checkMode == *writeMode:
		fmt.Fprintln(os.Stderr, "metafmt: pass exactly one of --check or --write")
		os.Exit(2)
	case *checkMode:
		rep, err := format.Check(*dataDir)
		if err != nil {
			fmt.Fprintln(os.Stderr, "metafmt:", err)
			os.Exit(2)
		}
		report(rep)
		if !rep.Clean() {
			fmt.Fprintf(os.Stderr, "%s (%s)\n", rep.Summary(), rep.Advice())
			os.Exit(1)
		}
	case *writeMode:
		rep, err := format.Write(*dataDir)
		if err != nil {
			fmt.Fprintln(os.Stderr, "metafmt:", err)
			os.Exit(2)
		}
		report(rep)
		if rep.NeedsHuman() {
			fmt.Fprintln(os.Stderr, "metafmt: some files could not be read and were left unchanged; they need a human")
			os.Exit(1)
		}
	}
}

func report(rep format.Report) {
	for _, line := range rep.Lines() {
		fmt.Println(line)
	}
	for _, f := range rep.Invalid {
		fmt.Fprintln(os.Stderr, "invalid JSON, left unchanged:", f)
	}
}
