// Command metamigrate converts a file-per-entity data tree into the
// range-packed layout (PACK-SPEC.md) and backfills each work's and recording's
// added_at from git history.
//
// It is the flag-day tool: the repository's own tree is converted once, in one
// commit, after which every writer speaks packs only. Run it against the live
// tree with no flags:
//
//	go run ./cmd/metamigrate
//	go run ./cmd/metacheck && go run ./cmd/metafmt --check
//
// To rehearse without touching anything, write the converted tree somewhere
// else - the source tree is then left exactly as it is:
//
//	go run ./cmd/metamigrate --out /tmp/converted
//
// The added_at backfill walks the history of the repository the data directory
// sits in, so it needs a FULL clone (release.yml's retired fetch-depth: 0). Pass
// --backfill=false only for a tree with no history to read.
//
// Business logic lives in internal/migrate; this is flag wiring.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/kodestar/audiosilo-meta/internal/migrate"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

func main() {
	dataDir := flag.String("data", "data", "path to the legacy data directory")
	out := flag.String("out", "", "write the pack tree here instead of converting in place")
	repo := flag.String("repo", "", "repository to walk for added_at dates (default: the one --data sits in)")
	backfill := flag.Bool("backfill", true, "backfill added_at from git history")
	flag.Parse()

	sum, err := migrate.Run(migrate.Options{
		DataDir:  *dataDir,
		OutDir:   *out,
		RepoDir:  *repo,
		Backfill: *backfill,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "metamigrate:", err)
		os.Exit(1)
	}

	where := *dataDir
	if !sum.InPlace {
		where = *out
	}
	fmt.Fprintf(os.Stderr, "converted %d works (%d recordings), %d people, %d series, %d community sidecar sets\n",
		sum.Works, sum.Recordings, sum.People, sum.Series, sum.Community)
	fmt.Fprintf(os.Stderr, "backfilled added_at on %d works and %d recordings from git history\n",
		sum.DatedWorks, sum.DatedRecordings)
	for _, def := range pack.Families() {
		n := sum.Packs[def.Family]
		if n == 0 {
			continue
		}
		if d := sum.Dirs[def.Family]; d > 0 {
			fmt.Fprintf(os.Stderr, "  %-16s %4d packs in %d directories\n", def.Family.Root(), n, d)
			continue
		}
		fmt.Fprintf(os.Stderr, "  %-16s %4d packs\n", def.Family.Root(), n)
	}
	fmt.Fprintf(os.Stderr, "wrote %d pack files to %s", sum.TotalPacks(), where)
	if sum.InPlace {
		fmt.Fprintf(os.Stderr, ", removed %d legacy files", sum.Removed)
	}
	fmt.Fprintln(os.Stderr)
}
