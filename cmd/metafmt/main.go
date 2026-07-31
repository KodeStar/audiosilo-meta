// Command metafmt enforces the on-disk form of data/**/*.json: canonical JSON
// (keys sorted alphabetically recursively, 2-space indent, LF, a single
// trailing newline, UTF-8 with no HTML escaping) and, for families already in
// pack layout, entry placement - every entry in its bound-correct pack, no pack
// or directory over its split caps. --check lists the outstanding work and
// exits 1; --write formats, relocates and splits in place.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/kodestar/audiosilo-meta/internal/format"
)

func main() {
	dataDir := flag.String("data", "data", "path to the data directory")
	checkMode := flag.Bool("check", false, "list outstanding formatting and placement work and exit 1 if any")
	writeMode := flag.Bool("write", false, "format, relocate and split in place")
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
		for _, line := range rep.CheckLines() {
			fmt.Println(line)
		}
		if !rep.Clean() {
			fmt.Fprintf(os.Stderr, "%s (run metafmt --write to fix)\n", rep.Summary())
			os.Exit(1)
		}
	case *writeMode:
		rep, err := format.Write(*dataDir)
		if err != nil {
			fmt.Fprintln(os.Stderr, "metafmt:", err)
			os.Exit(2)
		}
		for _, line := range rep.WriteLines() {
			fmt.Println(line)
		}
		for _, f := range rep.Invalid {
			fmt.Fprintln(os.Stderr, "invalid JSON, left unchanged:", f)
		}
		if len(rep.Invalid) > 0 {
			os.Exit(1)
		}
	}
}
