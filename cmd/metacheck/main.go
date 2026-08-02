// Command metacheck validates the entire data/ tree: schema, key/id agreement,
// pack placement, caps and bounds, referential integrity, uniqueness, chapter
// ordering, and series positions. It prints one line per problem
// ("path: message") and exits 1 if any are found.
//
// Advisories are printed to stderr with an "advisory:" prefix and never affect
// the exit status: they name something worth a look (an entry too large to ever
// be split out of its pack, say) rather than a rule violation.
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
	for _, w := range res.Warnings {
		fmt.Fprintf(os.Stderr, "advisory: %s\n", w.String())
	}
	// The census under the advisory lines: the three classes an adversarial
	// review found a bulk import producing at scale are counted so one wave can
	// be compared with the last without diffing thousands of lines.
	if census := check.AdvisoryCensus(res.Warnings); census != "" {
		fmt.Fprintf(os.Stderr, "%s\n", census)
	}
	if !res.OK() {
		fmt.Fprintf(os.Stderr, "%d problem(s) found\n", len(res.Problems))
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "ok: %d works, %d people, %d series (%d advisory)\n",
		len(res.Catalog.Works), len(res.Catalog.People), len(res.Catalog.Series), len(res.Warnings))
}
