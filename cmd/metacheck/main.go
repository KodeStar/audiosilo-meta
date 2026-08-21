// Command metacheck validates the entire data/ tree: schema, key/id agreement,
// pack placement, caps and bounds, referential integrity, uniqueness, chapter
// ordering, and series positions. It prints one line per problem
// ("path: message") and exits 1 if any are found.
//
// Advisories are printed to stderr with an "advisory:" prefix and never affect
// the exit status: they name something worth a look (an entry too large to ever
// be split out of its pack, say) rather than a rule violation.
//
// --profile names which families the root is meant to hold (pack.Profile). It
// defaults to "all", the whole database in one tree, which is this repository;
// "core" and "community" are the two halves the community-repo split produces,
// and under either of them a file belonging to the other half is reported as an
// unrecognized location.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/kodestar/audiosilo-meta/pkg/check"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

func main() {
	dataDir := flag.String("data", "data", "path to the data directory")
	profileName := flag.String("profile", pack.ProfileAll.String(),
		pack.ProfileFlagUsage)
	flag.Parse()

	profile, err := pack.ParseProfile(*profileName)
	if err != nil {
		fmt.Fprintln(os.Stderr, "metacheck:", err)
		os.Exit(2)
	}

	res := check.LoadProfile(*dataDir, profile)
	for _, p := range res.Problems {
		fmt.Println(p.String())
	}
	// Advisory lines + the census under them (the classes an adversarial review
	// found a bulk import producing at scale, counted so one wave can be compared
	// with the last without diffing thousands of lines) - one printer, shared
	// with metabuild.
	check.PrintAdvisories(os.Stderr, res.Warnings)
	if !res.OK() {
		fmt.Fprintf(os.Stderr, "%d problem(s) found\n", len(res.Problems))
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "ok: %d works, %d people, %d series (%d advisory)\n",
		len(res.Catalog.Works), len(res.Catalog.People), len(res.Catalog.Series), len(res.Warnings))
}
