// Command metafmt enforces canonical JSON formatting for data/**/*.json: keys
// sorted alphabetically (recursively), 2-space indent, LF, a single trailing
// newline, UTF-8 with no HTML escaping. --check lists non-canonical files and
// exits 1; --write rewrites them in place.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/kodestar/audiosilo-meta/internal/canonical"
)

func main() {
	dataDir := flag.String("data", "data", "path to the data directory")
	checkMode := flag.Bool("check", false, "list non-canonical files and exit 1 if any")
	writeMode := flag.Bool("write", false, "rewrite non-canonical files in place")
	flag.Parse()

	switch {
	case *checkMode == *writeMode:
		fmt.Fprintln(os.Stderr, "metafmt: pass exactly one of --check or --write")
		os.Exit(2)
	case *checkMode:
		bad, err := canonical.CheckTree(*dataDir)
		if err != nil {
			fmt.Fprintln(os.Stderr, "metafmt:", err)
			os.Exit(2)
		}
		for _, f := range bad {
			fmt.Println(f)
		}
		if len(bad) > 0 {
			fmt.Fprintf(os.Stderr, "%d file(s) not canonical (run metafmt --write)\n", len(bad))
			os.Exit(1)
		}
	case *writeMode:
		changed, failed, err := canonical.WriteTree(*dataDir)
		if err != nil {
			fmt.Fprintln(os.Stderr, "metafmt:", err)
			os.Exit(2)
		}
		for _, f := range changed {
			fmt.Println("formatted", f)
		}
		for _, f := range failed {
			fmt.Fprintln(os.Stderr, "invalid JSON, left unchanged:", f)
		}
		if len(failed) > 0 {
			os.Exit(1)
		}
	}
}
