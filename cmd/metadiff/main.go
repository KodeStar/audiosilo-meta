// Command metadiff renders an ENTRY-LEVEL summary of the data changes between
// two revisions of this repository: what records were added, removed and
// modified, rather than what bytes moved.
//
// It exists because the data tree is range-packed. A write re-renders its whole
// pack and may split it, so a textual `git diff` of a hundred-work tranche is
// several megabytes of storage churn wrapped around a few hundred lines of
// actual facts - and anything reading it under a size cap (the ai-verify
// reviewer, a maintainer skimming a pull request) sees an arbitrary tenth of it.
// This command keys every changed pack's entries by family and slug across the
// whole tranche at once, so an entry that merely moved between packs cancels out
// and what is left is the change.
//
//	metadiff                                   # HEAD~1..HEAD, text, 180000 bytes
//	metadiff --base <sha> --head <sha>
//	metadiff --format json                     # the same comparison, structured
//	metadiff --max-bytes 0                     # no truncation at all
//
// It is READ-ONLY: it reads git objects and writes to stdout. The logic is in
// internal/recorddiff; this is flag wiring.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/kodestar/audiosilo-meta/internal/recorddiff"
)

func main() {
	repoDir := flag.String("repo", ".", "path to the git checkout to compare inside")
	dataDir := flag.String("data", "data", "the data directory, relative to the repository root")
	base := flag.String("base", "HEAD~1", "the revision to compare from")
	head := flag.String("head", "HEAD", "the revision to compare to")
	format := flag.String("format", "text", "output format: text or json")
	maxBytes := flag.Int("max-bytes", recorddiff.DefaultMaxBytes,
		"cap the text render at this many bytes, dropping whole entries and saying how many (0 for no cap)")
	flag.Parse()

	if *format != "text" && *format != "json" {
		fail("--format must be text or json")
	}

	diff, err := run(*repoDir, *dataDir, *base, *head)
	if err != nil {
		fail(err.Error())
	}

	if *format == "json" {
		raw, err := diff.JSON()
		if err != nil {
			fail(err.Error())
		}
		if _, err := os.Stdout.Write(raw); err != nil {
			fail(err.Error())
		}
		return
	}
	fmt.Print(diff.Text(*maxBytes))
}

// run opens both revisions and computes the diff, closing the two git readers
// whatever happens.
func run(repoDir, dataDir, base, head string) (diff *recorddiff.Diff, err error) {
	// The changed-path listing is the three-dot comparison (the merge base
	// against head), so the base side's CONTENT is read at the merge base too.
	// Reading it at the given revision instead - a pull request's base sha is the
	// base BRANCH's tip, which moves while the branch sits open - would report
	// every record main gained in the meantime as removed by this change.
	mergeBase, err := recorddiff.MergeBase(repoDir, base, head)
	if err != nil {
		return nil, err
	}

	paths, err := recorddiff.GitChangedPaths(repoDir, dataDir, mergeBase, head)
	if err != nil {
		return nil, err
	}

	baseSrc, err := recorddiff.OpenGitSource(repoDir, dataDir, mergeBase)
	if err != nil {
		return nil, err
	}
	defer closing(baseSrc, &err)
	headSrc, err := recorddiff.OpenGitSource(repoDir, dataDir, head)
	if err != nil {
		return nil, err
	}
	defer closing(headSrc, &err)

	diff, err = recorddiff.Compute(paths, baseSrc, headSrc)
	if err != nil {
		return nil, err
	}
	diff.Base, diff.Head = baseSrc.Rev(), headSrc.Rev()
	return diff, nil
}

// closing shuts a git reader down, reporting its failure only when the run had
// not already failed for a better reason.
func closing(s *recorddiff.GitSource, err *error) {
	if cerr := s.Close(); cerr != nil && *err == nil {
		*err = cerr
	}
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, "metadiff:", msg)
	os.Exit(1)
}
