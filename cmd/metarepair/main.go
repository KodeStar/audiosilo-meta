// Command metarepair applies the NON-ADVISORY proposals of a metaaudit report to the
// data tree: it merges duplicate works and series, retitles decorated titles, models
// the series memberships a title states, and restates non-canonical positions.
//
// The default is a DRY RUN that writes nothing and reports the whole plan:
//
//	go run ./cmd/metaaudit -data data -o audit-report            # the worklist
//	go run ./cmd/metarepair -data data --op merge-works --limit 50
//	go run ./cmd/metarepair -data data -report audit-report --op merge-works --limit 50 -o repair-report
//	go run ./cmd/metarepair -data data -report audit-report --op merge-works --limit 50 -o repair-report --write
//	go run ./cmd/metacheck && go run ./cmd/metafmt --check
//
// A -report directory is a FILTER, never a source of instructions: every proposal
// applied comes from a fresh, in-process audit over the tree being modified, and a
// record in the file the fresh run no longer proposes the same way is refused and
// named. That is what makes a second run over repaired data a no-op.
//
// --write additionally refuses a data tree with uncommitted changes (git is the
// recovery story: this pass deletes records) and one that does not validate, and it
// fails the run if the tree does not validate afterwards.
//
// --profile names which families the root holds (pack.Profile). It defaults to
// `core`, which is what THIS repository's tree is since the community-repo split -
// deliberately NOT the `all` that metacheck and metafmt default to, and the
// difference is the cost of being wrong. Those two are read by CI with the flag
// spelled out, and a wrong reading there is a red check. This pass DELETES
// RECORDS, is run by a human typing a command, and under `all` over a core tree
// would see an empty works-community family and merge as though no book in the
// wave carried a sidecar. The safe reading is the default; `--profile all` is
// available for a genuine whole-database tree, of which there are now none.
//
// --community names that repository's data/ directory, opened READ-ONLY, and a
// core-profile merge wave NEEDS IT. The sidecar-collision refusal - both halves of
// a duplicate carrying the same characters or recaps member, which is a human
// decision - is a question about data this repository no longer holds, so without
// the flag every merge-works proposal is refused as `community-data-required`
// rather than merged blind. Nothing is written there: the members ride the slug
// tombstone to the surviving work until the community repository's re-key sweep
// lands them. A typical wave is therefore:
//
//	go run ./cmd/metarepair -data data --profile core \
//	  --community ../audiosilo-meta-community/data --op merge-works --limit 50
//
// Business logic lives in internal/repair; this is flag wiring.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/kodestar/audiosilo-meta/internal/repair"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// repeated is a flag that may be given several times, and may also take a
// comma-separated list, so `--op merge-works --op merge-series` and
// `--op merge-works,merge-series` mean the same thing.
//
// An EMPTY value is an ERROR, not an empty list. Absent, `--op` means "every op this pass
// can apply" - which is the right default for an interactive run - but that made
// `--op "$OP"` with an unset variable mean the same thing, so a runbook line meant to
// select one op would have selected all of them, against a database this tool deletes
// records from. `--op ”`, `--op ','` and `--op 'merge-works,'` are all refused; the
// "everything" reading is reachable only by leaving the flag off.
type repeated []string

func (r *repeated) String() string { return strings.Join(*r, ",") }

func (r *repeated) Set(v string) error {
	if strings.TrimSpace(v) == "" {
		return fmt.Errorf("is empty; leave the flag off to mean every value, or name one")
	}
	for _, part := range strings.Split(v, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return fmt.Errorf("%q holds an empty entry; name one value per entry", v)
		}
		*r = append(*r, part)
	}
	return nil
}

func main() {
	dataDir := flag.String("data", "data", "path to the data directory")
	report := flag.String("report", "", "metaaudit report directory to use as the worklist (optional: without it, whatever the fresh audit proposes)")
	out := flag.String("o", "", "directory to write the apply-report into (optional)")
	only := flag.String("only", "", "file of explicit keys to act on, one per line (a finding key, or a work/series slug it names)")
	limit := flag.Int("limit", 0, "consider at most N matching proposals, in report order; a refusal raised while planning "+
		"counts against it, and chunking a wave also bounds the packs a run holds resident (0 = no cap)")
	write := flag.Bool("write", false, "apply the plan (default: report it and write nothing)")
	verbose := flag.Bool("v", false, "print every applied change record by record")
	profileName := flag.String("profile", pack.ProfileCore.String(), pack.ProfileFlagUsage+
		" (default core: this repository's tree, and the safe reading for a pass that deletes records)")
	community := flag.String("community", "", "the community checkout's data/ directory, opened read-only so the "+
		"sidecar-collision check can see the CC BY-SA layer; REQUIRED for a merge wave under --profile core, which "+
		"otherwise refuses every merge as community-data-required")
	var ops, subclasses repeated
	flag.Var(&ops, "op", "restrict to this op, repeatable or comma-separated; LEAVE IT OFF for every op (one of "+
		strings.Join(repair.AppliableOps(), ", ")+")")
	flag.Var(&subclasses, "subclass", "restrict to this audit subclass, repeatable or comma-separated; leave it off for every subclass")
	flag.Parse()

	profile, perr := pack.ParseProfile(*profileName)
	if perr != nil {
		fmt.Fprintln(os.Stderr, "metarepair:", perr)
		os.Exit(2)
	}

	rep, err := repair.Run(repair.Options{
		DataDir:    *dataDir,
		ReportDir:  *report,
		OutDir:     *out,
		Ops:        ops,
		Subclasses: subclasses,
		OnlyFile:   *only,
		Limit:      *limit,
		Write:      *write,
		Profile:    profile,

		CommunityDir: *community,
	})
	if rep != nil {
		if werr := rep.Write(os.Stdout, *verbose); werr != nil {
			fmt.Fprintln(os.Stderr, "metarepair:", werr)
			os.Exit(1)
		}
		if *out != "" {
			fmt.Printf("apply-report written to %s\n", *out)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "metarepair:", err)
		os.Exit(1)
	}
}
