// Command metacheck validates the entire data/ tree: schema, id/shard
// agreement, referential integrity, uniqueness, chapter ordering, and series
// positions. It prints one line per problem ("path: message") and exits 1 if
// any are found.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/kodestar/audiosilo-meta/pkg/check"
)

func main() {
	dataDir := flag.String("data", "data", "path to the data directory")
	flag.Parse()

	res := check.Load(*dataDir)
	for _, p := range res.Problems {
		fmt.Println(p.String())
	}
	if !res.OK() {
		fmt.Fprintf(os.Stderr, "%d problem(s) found\n", len(res.Problems))
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "ok: %d works, %d people, %d series\n",
		len(res.Catalog.Works), len(res.Catalog.People), len(res.Catalog.Series))
}
