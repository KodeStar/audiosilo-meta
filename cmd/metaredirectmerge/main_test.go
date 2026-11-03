package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/redirects"
)

// Every case here is a REAL git rebase - a base commit, a commit on main, a commit
// on the branch - with the repository's own .gitattributes, the way the intake
// sweep meets the file (scripts/packmerge_test.go does the same for the pack
// driver). The driver git runs is this test binary: with runMainEnv set, TestMain
// hands the arguments to run, so no separate build is needed and git sees exactly
// the code under test.

const runMainEnv = "METAREDIRECTMERGE_RUN_MAIN"

func TestMain(m *testing.M) {
	if os.Getenv(runMainEnv) == "1" {
		os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
	}
	os.Exit(m.Run())
}

const redirectsPath = "data/redirects.json"

// driverMergedMarker is what .github/workflows/intake.yml greps a rebase's output
// for to know a clean rebase still merged something and must be re-checked.
const driverMergedMarker = "merged both sides"

// table renders a works-only tombstone table from "old=new" pairs, in the bytes
// the tree stores (canonical, one tombstone per line - the shape the line merge's
// behaviour depends on).
func table(t *testing.T, pairs ...string) string {
	t.Helper()
	r := model.NewRedirects()
	for _, p := range pairs {
		from, to, _ := strings.Cut(p, "=")
		r[model.RedirectWorks][from] = to
	}
	out, err := redirects.Render(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// TestDriver runs the driver the way the sweep does. The merge RULES are
// pkg/redirects' TestMerge; this pins the git seam: a clean merge's bytes and
// marker line, a collapse, and a refusal left as a git conflict.
func TestDriver(t *testing.T) {
	requireGit(t)
	cases := []struct {
		name               string
		base, main, branch string
		// want is the merged file, byte for byte; empty means a refusal.
		want string
		// clash is a fragment the refusal must carry.
		clash string
	}{
		{
			name:   "two branches adding adjacent aliases merge",
			base:   table(t, "aaa-old=aaa"),
			main:   table(t, "aaa-old=aaa", "bbb-old=bbb"),
			branch: table(t, "aaa-old=aaa", "ccc-old=ccc"),
			want:   table(t, "aaa-old=aaa", "bbb-old=bbb", "ccc-old=ccc"),
		},
		{
			name:   "a chain across the two sides collapses",
			base:   table(t),
			main:   table(t, "aaa=bbb"),
			branch: table(t, "bbb=ccc"),
			want:   table(t, "aaa=ccc", "bbb=ccc"),
		},
		{
			name:   "one slug sent to two different targets is refused",
			base:   table(t, "aaa-old=aaa"),
			main:   table(t, "aaa-old=aaa", "bbb-old=bbb-one"),
			branch: table(t, "aaa-old=aaa", "bbb-old=bbb-two"),
			clash:  "works bbb-old: both sides added it, to different targets",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo, out, err := rebaseFixture(t, tc.base, tc.main, tc.branch, true)
			got := readFile(t, filepath.Join(repo, redirectsPath))
			if tc.want != "" {
				if err != nil {
					t.Fatalf("the driver did not resolve the merge: %v\n%s", err, out)
				}
				if !strings.Contains(out, driverMergedMarker) {
					t.Errorf("the rebase output does not carry %q, which .github/workflows/intake.yml greps for:\n%s", driverMergedMarker, out)
				}
				// The pack driver's line, which makes the sweep run a tree-wide
				// metafmt --write this file never needs.
				if strings.Contains(out, "merged both sides' records") {
					t.Errorf("the output carries the pack driver's marker:\n%s", out)
				}
				if got != tc.want {
					t.Errorf("merged file:\n%s\nwant (pkg/redirects.Write's bytes):\n%s", got, tc.want)
				}
				return
			}
			if err == nil {
				t.Fatalf("the rebase reported success on a conflict:\n%s\nfile:\n%s", out, got)
			}
			if !strings.Contains(out, tc.clash) {
				t.Errorf("the refusal does not name %q:\n%s", tc.clash, out)
			}
			// Left the way git's own merge leaves a conflict: unmerged in the
			// index, with markers in the file, so it cannot be committed by
			// accident as a valid table.
			if u := gitOut(t, repo, "diff", "--name-only", "--diff-filter=U"); strings.TrimSpace(u) != redirectsPath {
				t.Errorf("unmerged paths = %q, want %s", u, redirectsPath)
			}
			if !strings.Contains(got, "<<<<<<< ") || !strings.Contains(got, ">>>>>>> ") {
				t.Errorf("the conflicted file carries no conflict markers:\n%s", got)
			}
			// And by hand on what the rebase left: the same refusal, exit 5.
			out, code := runMain(t, repo, redirectsPath)
			if code != exitConflict {
				t.Errorf("by hand: exit %d, want %d: %s", code, exitConflict, out)
			}
		})
	}
}

// TestByHand: a checkout without the driver configured stops on the line merge's
// conflict, and the by-hand mode resolves it from the staged versions.
func TestByHand(t *testing.T) {
	requireGit(t)
	want := table(t, "aaa-old=aaa", "bbb-old=bbb", "ccc-old=ccc")
	repo, out, err := rebaseFixture(t,
		table(t, "aaa-old=aaa"),
		table(t, "aaa-old=aaa", "bbb-old=bbb"),
		table(t, "aaa-old=aaa", "ccc-old=ccc"),
		false)
	if err == nil {
		t.Skipf("git merged the adjacent insertions itself, so there is no conflict to resolve:\n%s", out)
	}
	out, code := runMain(t, repo, redirectsPath)
	if code != exitMerged {
		t.Fatalf("exit %d, want 0: %s", code, out)
	}
	if !strings.Contains(out, driverMergedMarker) {
		t.Errorf("output does not carry %q: %s", driverMergedMarker, out)
	}
	if got := readFile(t, filepath.Join(repo, redirectsPath)); got != want {
		t.Errorf("merged file:\n%s\nwant:\n%s", got, want)
	}
	if u := gitOut(t, repo, "diff", "--name-only", "--diff-filter=U"); strings.TrimSpace(u) != "" {
		t.Errorf("still unmerged after the by-hand merge: %q", u)
	}
	if out, err := gitIn(repo, nil, "-c", "core.editor=true", "rebase", "--continue"); err != nil {
		t.Errorf("rebase --continue: %v\n%s", err, out)
	}
}

// TestByHandFromASubdirectory: the path names one file for the stage reads, the
// write and the `git add` alike. Read root-relative and written cwd-relative, a
// run from data/ on redirects.json found no stages, and one on
// data/redirects.json read the real stages and aimed the write at
// data/data/redirects.json.
func TestByHandFromASubdirectory(t *testing.T) {
	requireGit(t)
	repo, out, err := rebaseFixture(t,
		table(t, "aaa-old=aaa"),
		table(t, "aaa-old=aaa", "bbb-old=bbb"),
		table(t, "aaa-old=aaa", "ccc-old=ccc"),
		false)
	if err == nil {
		t.Skipf("git merged the adjacent insertions itself, so there is no conflict to resolve:\n%s", out)
	}
	sub := filepath.Join(repo, "data")
	if out, code := runMain(t, sub, redirectsPath); code == exitMerged {
		t.Errorf("a root-relative path run from data/ merged: %s", out)
	}
	if _, err := os.Stat(filepath.Join(sub, redirectsPath)); !os.IsNotExist(err) {
		t.Errorf("a stray %s was written under data/ (stat: %v)", redirectsPath, err)
	}
	if out, code := runMain(t, sub, filepath.Base(redirectsPath)); code != exitMerged {
		t.Fatalf("by hand from data/: exit %d, want 0: %s", code, out)
	}
	want := table(t, "aaa-old=aaa", "bbb-old=bbb", "ccc-old=ccc")
	if got := readFile(t, filepath.Join(repo, redirectsPath)); got != want {
		t.Errorf("merged file:\n%s\nwant:\n%s", got, want)
	}
	if u := gitOut(t, repo, "diff", "--name-only", "--diff-filter=U"); strings.TrimSpace(u) != "" {
		t.Errorf("still unmerged after the by-hand merge: %q", u)
	}
}

func TestUsage(t *testing.T) {
	var stdout, stderr strings.Builder
	if code := run(nil, &stdout, &stderr); code != exitUsage {
		t.Errorf("no arguments: exit %d, want %d", code, exitUsage)
	}
}

// TestDriverRefusesANonTable: a version that does not parse is "cannot merge
// this way" (exit 1), never an empty table merged over - and the file git keeps
// carries conflict markers, never ours' valid table alone, which would read as
// resolved and lose theirs' side on a `git add`.
func TestDriverRefusesANonTable(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	ok := table(t, "aaa-old=aaa")
	for name, theirs := range map[string]string{
		"a pack":   `{"entries":{}}` + "\n",
		"empty":    "",
		"not JSON": "<<<<<<<\n",
	} {
		t.Run(name, func(t *testing.T) {
			ours := write("ours", ok)
			var stdout, stderr strings.Builder
			code := run([]string{write("base", ok), ours, write("theirs", theirs), redirectsPath}, &stdout, &stderr)
			if code != exitCannot {
				t.Errorf("exit %d, want %d: %s", code, exitCannot, stderr.String())
			}
			got := readFile(t, ours)
			if !strings.Contains(got, "<<<<<<< ") || !strings.Contains(got, ">>>>>>> ") {
				t.Errorf("the refused file carries no conflict markers:\n%s", got)
			}
			if !strings.Contains(got, `"aaa-old": "aaa"`) {
				t.Errorf("our side is missing from the conflicted file:\n%s", got)
			}
			if _, err := redirects.Parse([]byte(got)); err == nil {
				t.Errorf("the refused file still parses as a tombstone table:\n%s", got)
			}
		})
	}
}

// TestAttributeRoutesTheTable: the driver is only reached through .gitattributes,
// and the pack pattern above its line also matches this path.
func TestAttributeRoutesTheTable(t *testing.T) {
	requireGit(t)
	repo, _, _ := rebaseFixture(t, table(t), table(t, "a=b"), table(t, "c=d"), false)
	for path, want := range map[string]string{
		redirectsPath:         "merge: redirectsjson",
		"data/works/0/0.json": "merge: packjson",
		"data/people/0.json":  "merge: packjson",
	} {
		if out := gitOut(t, repo, "check-attr", "merge", "--", path); !strings.Contains(out, want) {
			t.Errorf("%s: %s, want %q", path, strings.TrimSpace(out), want)
		}
	}
}

// rebaseFixture builds a repository holding the three versions of the table and
// rebases the branch onto main, returning the directory, the output and the error
// (nil when the rebase completed). With driver set, the redirectsjson driver is
// configured as the intake sweep configures it; without, the attribute has
// nothing behind it and git falls back to its line merge.
func rebaseFixture(t *testing.T, base, main, branch string, driver bool) (string, string, error) {
	t.Helper()
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		if out, err := gitIn(repo, nil, args...); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	write := func(rel, body string) {
		t.Helper()
		full := filepath.Join(repo, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q", "-b", "main")
	git("config", "user.name", "Fixture")
	git("config", "user.email", "fixture@example.com")
	git("config", "commit.gpgsign", "false")
	if driver {
		self, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		git("config", "merge.redirectsjson.name", "three-way merge of the slug tombstone table")
		git("config", "merge.redirectsjson.driver", "'"+self+"' %O %A %B %P")
	}
	write(".gitattributes", readFile(t, filepath.Join("..", "..", ".gitattributes")))
	write(redirectsPath, base)
	git("add", "-A")
	git("commit", "-qm", "base")
	git("checkout", "-qb", "branch")
	write(redirectsPath, branch)
	git("commit", "-qam", "branch")
	git("checkout", "-q", "main")
	write(redirectsPath, main)
	git("commit", "-qam", "main")
	git("checkout", "-q", "branch")

	out, err := gitIn(repo, []string{runMainEnv + "=1"}, "rebase", "main")
	return repo, out, err
}

// runMain runs this binary as the command, in dir, returning its combined output
// and exit status.
func runMain(t *testing.T, dir string, args ...string) (string, int) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(self, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), runMainEnv+"=1")
	out, err := cmd.CombinedOutput()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return string(out), ee.ExitCode()
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(out), 0
}

func gitIn(dir string, env []string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(append(os.Environ(),
		"GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.com",
		"GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.com"), env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitIn(dir, nil, args...)
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}
}
