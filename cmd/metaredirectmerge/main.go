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
// On a conflict - and on a refusal of a version it cannot read as a table (exit 1)
// - the driver leaves the file the way git's own merge would: the line merge's
// conflict markers (git merge-file), or, if the lines happen to merge cleanly where
// the tombstones do not, the two whole sides between markers. So a person resolving
// it sees the disagreement, and a file left unresolved can never pass metacheck: left
// as OURS' valid table, a `git add` would drop every tombstone theirs carried
// without a word. By hand, the file already holds git's markers and is left alone.
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
	"path/filepath"
	"strings"

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
	switch {
	case len(args) == 1 && (args[0] == "-h" || args[0] == "-help" || args[0] == "--help"):
		// Asked for, not mistyped: a caller probing that the binary runs (the
		// sync service's preflight) must not reach git with "--help" as a path.
	case len(args) == 1:
		return byHand(args[0], stdout, stderr)
	case len(args) == 4:
		return driver(args[0], args[1], args[2], args[3], stdout, stderr)
	}
	say(stderr, "usage: metaredirectmerge <conflicted data/redirects.json>\n")
	say(stderr, "       metaredirectmerge <base> <ours> <theirs> <path>   (git merge driver: %%O %%A %%B %%P)\n")
	return exitUsage
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
	default:
		// A conflict, or a version that is no table: either way the file must
		// not be left as ours' valid table, which reads as resolved.
		markConflict(oursFile, baseFile, theirsFile, ours, theirs, path, stderr)
	}
	return code
}

// byHand merges the three versions a stopped rebase or merge staged for path.
func byHand(path string, stdout, stderr io.Writer) int {
	stages, err := listStages(path)
	if err != nil {
		say(stderr, "%s: listing the staged versions: %v\n", path, err)
		return exitCannot
	}
	if len(stages) == 0 {
		say(stderr, "%s: not conflicted; nothing to merge\n", path)
		return exitCannot
	}
	// A missing side is a modify/delete conflict, which is a person's decision:
	// one side removed the whole table.
	for _, side := range []struct {
		stage int
		name  string
	}{{2, "our"}, {3, "their"}} {
		if !stages[side.stage] {
			say(stderr, "%s: %s version (stage %d) is not staged - that side deleted the file; resolve this one by hand\n",
				path, side.name, side.stage)
			return exitCannot
		}
	}
	var versions [4][]byte
	for stage := 1; stage <= 3; stage++ {
		// No base stage is a file both sides created: an empty table. A base
		// stage that IS listed but cannot be read is a failure, never an empty
		// base - read as one, every tombstone the base held would look added by
		// both sides, and one side's deletion would be handed back.
		if stage == 1 && !stages[1] {
			continue
		}
		raw, err := showStage(stage, path)
		if err != nil {
			say(stderr, "%s: reading stage %d: %v\n", path, stage, err)
			return exitCannot
		}
		versions[stage] = raw
	}
	base, ours, theirs := versions[1], versions[2], versions[3]
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

// staged reads one index stage of path. A bare path in git's :<stage>:<path> is
// REPOSITORY-ROOT relative while the write and the `git add` below read path from
// the current directory, so a relative path is given its ./ form, which git
// resolves from the current directory too. With the bare form, a run from data/
// on redirects.json found no stages at all, and one on data/redirects.json read
// the real stages and then wrote (and, where that directory existed, staged)
// data/data/redirects.json, leaving the conflict where it was.
// showStage reads one staged version; a variable so a test can make a listed
// stage unreadable, which a real repository does not produce on demand.
var showStage = func(stage int, path string) ([]byte, error) {
	p := filepath.ToSlash(path)
	if !filepath.IsAbs(path) && !strings.HasPrefix(p, "./") && !strings.HasPrefix(p, "../") {
		p = "./" + p
	}
	return exec.Command("git", "show", fmt.Sprintf(":%d:%s", stage, p)).Output()
}

// listStages reports which of the stages 1-3 the index holds for path (resolved
// against the working directory, as showStage's ./ form is).
func listStages(path string) (map[int]bool, error) {
	out, err := exec.Command("git", "--literal-pathspecs", "ls-files", "--unmerged", "-z", "--", path).Output()
	if err != nil {
		return nil, err
	}
	stages := map[int]bool{}
	for _, rec := range strings.Split(string(out), "\x00") {
		// "<mode> <object> <stage>\t<path>"
		meta, _, ok := strings.Cut(rec, "\t")
		if !ok {
			continue
		}
		fields := strings.Fields(meta)
		if len(fields) != 3 {
			return nil, fmt.Errorf("unexpected ls-files record %q", rec)
		}
		switch fields[2] {
		case "1", "2", "3":
			stages[int(fields[2][0]-'0')] = true
		default:
			return nil, fmt.Errorf("unexpected stage in ls-files record %q", rec)
		}
	}
	return stages, nil
}

// merge parses the three versions and merges them, returning the canonical bytes
// and an exit code; a refusal is reported on stderr.
func merge(path string, base, ours, theirs []byte, stderr io.Writer) ([]byte, int) {
	b, okB := parse(path, "the base", base, true, stderr)
	o, okO := parse(path, "our", ours, false, stderr)
	t, okT := parse(path, "their", theirs, false, stderr)
	if !okB || !okO || !okT {
		return nil, exitCannot
	}
	merged, err := redirects.Merge(b, o, t)
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

// parse reads one version, reporting why it cannot be merged. Only the base may be
// empty: a file both sides created has none, which is an empty table.
func parse(path, name string, raw []byte, emptyOK bool, stderr io.Writer) (model.Redirects, bool) {
	if len(raw) == 0 {
		if emptyOK {
			return nil, true
		}
		say(stderr, "%s: %s version is empty; resolve this one by hand\n", path, name)
		return nil, false
	}
	r, err := redirects.Parse(raw)
	if err != nil {
		say(stderr, "%s: %s version is not a tombstone table: %v\n", path, name, err)
		return nil, false
	}
	return r, true
}

// markConflict leaves oursFile holding conflict markers, as git's own merge would
// have: git merge-file's, or - when the line merge is clean although the
// tombstones conflict (or a version is no table), so its result would read as
// resolved - the two whole sides. Exit 1..127 from merge-file is the number of
// conflicts it marked.
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
