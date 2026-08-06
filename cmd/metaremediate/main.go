// Command metaremediate folds GraphicAudio's multi-part dramatized-adaptation
// products back into one work per book and gives the series slots those parts
// took back to the plain text editions.
//
// It is a one-off repair, metamigrate's sibling: the catalogue was seeded with
// Audible's part PRODUCTS as separate works ("The Blood Mirror (2 of 2)
// [Dramatized Adaptation]", "Oathbringer (3 of 6)"), which are not books.
//
// The default is a DRY RUN that writes nothing and prints the whole plan:
//
//	go run ./cmd/metaremediate                       # the plan, summarized
//	go run ./cmd/metaremediate -v                    # every merge and series change
//	go run ./cmd/metaremediate --complete-sets rows.ndjson -v
//	go run ./cmd/metaremediate --complete-sets rows.ndjson --write
//	go run ./cmd/metafmt --write && go run ./cmd/metacheck
//
// --complete-sets takes libex dump rows for the whole-book products, in the
// shape scripts/libex-export-rows.sql emits; see scripts/README.md for the
// operator flow. Without it the merge composes a derived title and carries the
// parts' identifiers only, which is a complete and valid outcome.
//
// Business logic lives in internal/remediate; this is flag wiring.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/kodestar/audiosilo-meta/internal/remediate"
)

func main() {
	dataDir := flag.String("data", "data", "path to the data directory")
	sets := flag.String("complete-sets", "", "NDJSON of libex rows for the whole-book products (optional)")
	write := flag.Bool("write", false, "apply the plan (default: report it and write nothing)")
	verbose := flag.Bool("v", false, "print every merge and every series change")
	flag.Parse()

	rep, err := remediate.Run(remediate.Options{
		Dir:          *dataDir,
		CompleteSets: *sets,
		Write:        *write,
	})
	if rep != nil {
		if werr := rep.Write(os.Stdout, *verbose); werr != nil {
			fmt.Fprintln(os.Stderr, "metaremediate:", werr)
			os.Exit(1)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "metaremediate:", err)
		os.Exit(1)
	}
}
