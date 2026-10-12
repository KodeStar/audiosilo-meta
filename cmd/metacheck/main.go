// Command metacheck validates the entire data/ tree: schema, key/id agreement,
// pack placement, caps and bounds, referential integrity, uniqueness, chapter
// ordering, and series positions. It prints one line per problem
// ("path: message") and exits 1 if any are found.
//
// Advisories are printed to stderr with an "advisory:" prefix and never affect
// the exit status: they name something worth a look (an entry too large to ever
// be split out of its pack, say) rather than a rule violation.
//
// The duplicate advisories honour the REVIEWED decisions in
// internal/audit/reviewed.json: a pair a maintainer rejected as a merge
// (audit.ReviewedDistinct) is neither printed nor counted, and the census line says
// how many reviewed pairs that withheld.
//
// --advisory-keys <file> additionally writes every advisory's stable identity
// (check.AdvisoryKey), sorted, one per line, so two runs - a pull request's base and
// its head - can be compared for the advisories a change introduces
// (.github/scripts/advisory-delta.sh). It is written whether or not the tree has
// problems, and NOT written when the load produced no catalogue at all: an empty
// list would read as "the base had no advisories" and make every one of the head's
// look new. A write that fails is reported and leaves NO file - a partial list
// would make the keys it lost read as new or as gone - and never changes the exit
// status, which stays the tree's: CI's required metacheck step passes the flag, and
// the delta it feeds is never a gate.
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
	"slices"
	"strings"

	"github.com/kodestar/audiosilo-meta/internal/audit"
	"github.com/kodestar/audiosilo-meta/pkg/check"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

func main() {
	dataDir := flag.String("data", "data", "path to the data directory")
	profileName := flag.String("profile", pack.ProfileAll.String(),
		pack.ProfileFlagUsage)
	keysFile := flag.String("advisory-keys", "",
		"also write every advisory's stable identity, sorted, one per line, to this file")
	flag.Parse()

	profile, err := pack.ParseProfile(*profileName)
	if err != nil {
		fmt.Fprintln(os.Stderr, "metacheck:", err)
		os.Exit(2)
	}

	res := check.LoadProfileWith(*dataDir, profile, check.LoadOptions{Distinct: audit.ReviewedDistinct()})
	for _, p := range res.Problems {
		fmt.Println(p.String())
	}
	// Advisory lines + the census under them (the classes an adversarial review
	// found a bulk import producing at scale, counted so one wave can be compared
	// with the last without diffing thousands of lines) - one printer, shared
	// with metabuild.
	check.PrintAdvisories(os.Stderr, res)
	if *keysFile != "" && res.Catalog != nil {
		if err := writeAdvisoryKeys(*keysFile, res.Warnings); err != nil {
			fmt.Fprintln(os.Stderr, "metacheck: advisory keys not written:", err)
		}
	}
	if !res.OK() {
		fmt.Fprintf(os.Stderr, "%d problem(s) found\n", len(res.Problems))
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "ok: %d works, %d people, %d series (%d advisory)\n",
		len(res.Catalog.Works), len(res.Catalog.People), len(res.Catalog.Series), len(res.Warnings))
}

// writeAdvisoryKeys writes the sorted, de-duplicated stable identity of every
// advisory to path, one per line. On failure it removes whatever it wrote, so a
// consumer that takes the file's existence as success never reads half a list.
func writeAdvisoryKeys(path string, warnings []check.Problem) error {
	keys := make([]string, 0, len(warnings))
	for _, w := range warnings {
		keys = append(keys, check.AdvisoryKey(w))
	}
	slices.Sort(keys)
	keys = slices.Compact(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}
