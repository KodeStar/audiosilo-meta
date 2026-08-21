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
// --profile names which families the root holds (pack.Profile), defaulting to
// "all" as metacheck's and metafmt's do. Pass `core` for THIS repository's tree
// since the community-repo split: the store then never addresses works-community
// (which now lives in KodeStar/audiosilo-meta-community), and the post-write
// format pass and validation judge the same tree the CI check does. Over a core
// tree the two readings agree - the family is simply absent either way - so this
// is a statement of what the root is, not a change of what a wave does.
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
	profileName := flag.String("profile", pack.ProfileAll.String(), pack.ProfileFlagUsage)
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
