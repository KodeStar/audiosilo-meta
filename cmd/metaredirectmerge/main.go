// Command metaredirectmerge is the three-way merge of data/redirects.json, the
// slug tombstone table - the file's own git merge driver, as
// scripts/pack-union-merge.sh is the pack files'. The rules (and the chain
// collapse) are pkg/redirects.Merge; this is only the git plumbing around it.
//
// As a git merge driver, which is how the intake sweep runs it (.gitattributes
// names it for this one path):
//
//	git config merge.redirectsjson.name "three-way merge of the slug tombstone table"
//	git config merge.redirectsjson.driver "go run ./cmd/metaredirectmerge %O %A %B %P"
//
// (git runs a driver from the working tree's root, so mid-rebase `go run` builds
// the REPLAYED branch's copy - which is why the intake sweep builds main's copy
// outside the tree and configures the driver by that absolute path instead.)
//
// By hand, from inside a rebase or merge that stopped on a conflict in the file,
// it reads the three staged versions and, when they merge, writes and stages the
// result:
//
//	go run ./cmd/metaredirectmerge data/redirects.json
//
// The result is the exact bytes pkg/redirects.Write produces, so metafmt has
// nothing to re-render. It says "merged both sides" on success, which the intake
// sweep greps a rebase's output for to know it must run metacheck: a merged
// tombstone can name a record the other side deleted, and only the catalogue can
// tell.
//
// On a conflict the driver leaves the file the way git's own merge would - the
// line merge's conflict markers (git merge-file), or, if the lines happen to merge
// cleanly where the tombstones do not, the two whole sides between markers - so a
// person resolving it sees the disagreement and a file left unresolved can never
// pass metacheck. By hand, the file already holds git's markers and is left alone.
//
// Exit codes: 0 merged, 1 the file cannot be merged this way (a version is
// missing, empty or not a tombstone table), 2 usage, 5 a real conflict that needs
// a person (as a merge driver, any non-zero exit tells git the file is
// conflicted).
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/redirects"
)

const (
	exitMerged   = 0
	exitCannot   = 1
	exitUsage    = 2
	exitConflict = 5
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	switch len(args) {
	case 1:
		return byHand(args[0], stdout, stderr)
	case 4:
		return driver(args[0], args[1], args[2], args[3], stdout, stderr)
	default:
		say(stderr, "usage: metaredirectmerge <conflicted data/redirects.json>\n")
		say(stderr, "       metaredirectmerge <base> <ours> <theirs> <path>   (git merge driver: %%O %%A %%B %%P)\n")
		return exitUsage
	}
}

// driver merges the three versions git hands a merge driver and leaves the result
// in oursFile, which is where git takes it from.
func driver(baseFile, oursFile, theirsFile, path string, stdout, stderr io.Writer) int {
	base, err := os.ReadFile(baseFile)
	if err != nil {
		say(stderr, "%s: reading the base version: %v\n", path, err)
		return exitCannot
	}
	ours, err := os.ReadFile(oursFile)
	if err != nil {
		say(stderr, "%s: reading our version: %v\n", path, err)
		return exitCannot
	}
	theirs, err := os.ReadFile(theirsFile)
	if err != nil {
		say(stderr, "%s: reading their version: %v\n", path, err)
		return exitCannot
	}
	out, code := merge(path, base, ours, theirs, stderr)
	switch code {
	case exitMerged:
		if err := os.WriteFile(oursFile, out, 0o644); err != nil {
			say(stderr, "%s: writing the merge: %v\n", path, err)
			return exitCannot
		}
		say(stdout, "%s: merged both sides' redirects\n", path)
	case exitConflict:
		markConflict(oursFile, baseFile, theirsFile, ours, theirs, path, stderr)
	}
	return code
}

// byHand merges the three versions a stopped rebase or merge staged for path.
func byHand(path string, stdout, stderr io.Writer) int {
	// A file both sides created has no base stage, which is an empty table.
	base, _ := staged(1, path)
	ours, err := staged(2, path)
	if err != nil {
		say(stderr, "%s: no common-side version staged; resolve this one by hand\n", path)
		return exitCannot
	}
	theirs, err := staged(3, path)
	if err != nil {
		say(stderr, "%s: no incoming version staged; resolve this one by hand\n", path)
		return exitCannot
	}
	out, code := merge(path, base, ours, theirs, stderr)
	if code != exitMerged {
		return code
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		say(stderr, "%s: writing the merge: %v\n", path, err)
		return exitCannot
	}
	if msg, err := exec.Command("git", "add", "--", path).CombinedOutput(); err != nil {
		say(stderr, "%s: staging the merge: %v\n%s", path, err, msg)
		return exitCannot
	}
	say(stdout, "%s: merged both sides' redirects\n", path)
	return exitMerged
}

func staged(stage int, path string) ([]byte, error) {
	return exec.Command("git", "show", fmt.Sprintf(":%d:%s", stage, path)).Output()
}

// merge parses the three versions and merges them, returning the canonical bytes
// and an exit code; a refusal is reported on stderr.
func merge(path string, base, ours, theirs []byte, stderr io.Writer) ([]byte, int) {
	var b model.Redirects
	if len(base) > 0 {
		var err error
		if b, err = redirects.Parse(base); err != nil {
			say(stderr, "%s: the base version is not a tombstone table: %v\n", path, err)
			return nil, exitCannot
		}
	}
	sides := [2]model.Redirects{}
	for i, side := range [2]struct {
		name string
		raw  []byte
	}{{"our", ours}, {"their", theirs}} {
		if len(side.raw) == 0 {
			say(stderr, "%s: %s version is empty; resolve this one by hand\n", path, side.name)
			return nil, exitCannot
		}
		r, err := redirects.Parse(side.raw)
		if err != nil {
			say(stderr, "%s: %s version is not a tombstone table: %v\n", path, side.name, err)
			return nil, exitCannot
		}
		sides[i] = r
	}
	merged, err := redirects.Merge(b, sides[0], sides[1])
	if err != nil {
		say(stderr, "%s: %v\n", path, err)
		var mc *redirects.MergeConflict
		if errors.As(err, &mc) {
			return nil, exitConflict
		}
		return nil, exitCannot
	}
	out, err := redirects.Render(merged)
	if err != nil {
		say(stderr, "%s: rendering the merge: %v\n", path, err)
		return nil, exitCannot
	}
	return out, exitMerged
}

// markConflict leaves oursFile holding conflict markers, as git's own merge would
// have: git merge-file's, or - when the line merge is clean although the
// tombstones conflict, so its result would read as resolved - the two whole
// sides. Exit 1..127 from merge-file is the number of conflicts it marked.
func markConflict(oursFile, baseFile, theirsFile string, ours, theirs []byte, path string, stderr io.Writer) {
	cmd := exec.Command("git", "merge-file", "-L", "ours", "-L", "base", "-L", "theirs",
		oursFile, baseFile, theirsFile)
	err := cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() > 0 && ee.ExitCode() < 128 {
		return
	}
	whole := append([]byte("<<<<<<< ours\n"), withNewline(ours)...)
	whole = append(whole, "=======\n"...)
	whole = append(whole, withNewline(theirs)...)
	whole = append(whole, ">>>>>>> theirs\n"...)
	if werr := os.WriteFile(oursFile, whole, 0o644); werr != nil {
		say(stderr, "%s: writing the conflict markers: %v\n", path, werr)
	}
}

// withNewline returns b ending in a newline, so a marker never shares a line.
func withNewline(b []byte) []byte {
	if len(b) > 0 && b[len(b)-1] != '\n' {
		return append(append([]byte(nil), b...), '\n')
	}
	return b
}

// say writes one diagnostic; a failed write to stdout or stderr has nowhere left
// to be reported, and the exit code carries the verdict either way.
func say(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}
