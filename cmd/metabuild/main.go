// Command metabuild compiles the data/ tree into a SQLite artifact. It runs the
// full validation first and refuses to build invalid data.
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/kodestar/audiosilo-meta/internal/build"
	"github.com/kodestar/audiosilo-meta/internal/check"
)

func main() {
	dataDir := flag.String("data", "data", "path to the data directory")
	out := flag.String("o", "meta.sqlite", "output SQLite file")
	builtAt := flag.String("built-at", "", "build timestamp (RFC3339); defaults to now (UTC)")
	flag.Parse()

	res := check.Load(*dataDir)
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
