package scripts_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// Tests of the intake rebase sweep (.github/workflows/intake.yml, the step
// "Rebase open intake pull requests"). The step holds a write token while it
// checks intake branches out and force-pushes them, so these drive the step's
// own script, extracted from the workflow, against a local bare origin with a
// stub gh (the pull request listing and the comments) and a stub go (main's
// tools, which only a conflicted rebase runs). It runs under `bash -e`, the
// shell GitHub gives a `run:` with no `shell:`.

// ghSweepStub answers the gh calls the sweep makes from files in $GH_STUB_DIR:
// prs.json is the listing (list-fails makes it fail), and a comment is kept as
// one JSON line per pull request, which `pr view` reads back.
const ghSweepStub = `#!/usr/bin/env bash
set -u
d="$GH_STUB_DIR"
case "$1 $2" in
  "pr list")
    if [ -f "$d/list-fails" ]; then echo "gh: HTTP 502" >&2; exit 1; fi
    cat "$d/prs.json" ;;
  "api graphql") echo "intake-bot" ;;
  "pr view")
    if [ -f "$d/comments-$3.jsonl" ]; then jq -s '{comments: .}' "$d/comments-$3.jsonl"; else echo '{"comments":[]}'; fi ;;
  "pr comment")
    [ "$4" = "--body" ] || exit 2
    jq -nc --arg b "$5" '{author: {login: "intake-bot"}, body: $b}' >> "$d/comments-$3.jsonl" ;;
  *) echo "unexpected: gh $*" >&2; exit 2 ;;
esac
`

// goSweepStub stands in for `go build -o DIR/ ./cmd/...`: tools that accept
// anything, since a clean rebase never runs them.
const goSweepStub = `#!/usr/bin/env bash
[ "$1" = build ] && [ "$2" = -o ] || { echo "unexpected: go $*" >&2; exit 2; }
for t in metafmt metacheck metaredirectmerge; do printf '#!/bin/sh\nexit 0\n' > "$3/$t"; chmod +x "$3/$t"; done
`

// sweepScript is the step's run: block, dedented the way YAML reads it.
func sweepScript(t *testing.T) string {
	t.Helper()
	lines := strings.Split(readFile(t, filepath.Join("..", ".github", "workflows", "intake.yml")), "\n")
	start := -1
	for i, l := range lines {
		if strings.TrimSpace(l) == "- name: Rebase open intake pull requests" {
			start = i
			break
		}
	}
	if start < 0 {
		t.Fatal("intake.yml: no step named \"Rebase open intake pull requests\"")
	}
	for start < len(lines) && strings.TrimSpace(lines[start]) != "run: |" {
		start++
	}
	var body []string
	indent := ""
	for _, l := range lines[start+1:] {
		if strings.TrimSpace(l) == "" {
			body = append(body, "")
			continue
		}
		if indent == "" {
			indent = l[:len(l)-len(strings.TrimLeft(l, " "))]
		}
		if !strings.HasPrefix(l, indent) {
			break
		}
		body = append(body, strings.TrimPrefix(l, indent))
	}
	if len(body) == 0 {
		t.Fatal("intake.yml: the sweep step has no run: block")
	}
	return strings.Join(body, "\n") + "\n"
}

type sweepFixture struct {
	t                *testing.T
	origin, seed, gh string
	git              func(args ...string) string
	write            func(rel, body string)
	prs              []map[string]any
	// listing, when set, is what gh pr list prints instead of prs;
	// listFails makes gh pr list fail instead.
	listing             string
	listFails           bool
	stubBin, runnerTemp string
}

// newSweepFixture is an origin whose main holds a data pack, data/go.mod and
// main's own merge-driver script, with a seed clone to build branches in.
func newSweepFixture(t *testing.T) *sweepFixture {
	t.Helper()
	requireTools(t, "bash", "git", "jq", "sha256sum")
	f := &sweepFixture{t: t, origin: filepath.Join(t.TempDir(), "origin.git")}
	if out, err := runIn("", "git", "init", "-q", "--bare", "-b", "main", f.origin); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}
	f.seed, f.git, f.write = gitFixture(t)
	f.git("remote", "add", "origin", f.origin)
	f.write("data/works/a.json", "{\"a\":1}\n")
	f.write("data/go.mod", "module data\n")
	f.write("scripts/pack-union-merge.sh", "#!/bin/sh\nexit 1\n")
	f.git("add", "-A")
	f.git("commit", "-q", "-m", "base")
	f.git("push", "-q", "origin", "main")
	f.gh = filepath.Join(t.TempDir(), "gh")
	f.stubBin = filepath.Join(t.TempDir(), "bin")
	f.runnerTemp = t.TempDir()
	for _, d := range []string{f.gh, f.stubBin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range map[string]string{"gh": ghSweepStub, "go": goSweepStub} {
		if err := os.WriteFile(filepath.Join(f.stubBin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

// change is one file a commit writes; an empty body removes it, and the
// bodies below make it a symbolic link, a submodule entry or an executable
// file instead.
type change struct{ path, body string }

const (
	asSymlink    = "\x00symlink"
	asGitlink    = "\x00gitlink"
	asExecutable = "\x00executable"
)

// branch commits each set of changes in turn on top of base, pushes the result
// to refs/heads/<name> and lists it as intake pull request number.
func (f *sweepFixture) branch(number int, name, base string, commits ...[]change) string {
	f.t.Helper()
	f.git("checkout", "-q", "-B", "fixture-work", base)
	for i, c := range commits {
		for _, ch := range c {
			switch ch.body {
			case "":
				f.git("rm", "-q", "--", ch.path)
			case asSymlink:
				full := filepath.Join(f.seed, filepath.FromSlash(ch.path))
				if err := os.Symlink("../../.git/config", full); err != nil {
					f.t.Fatal(err)
				}
				f.git("add", "--", ch.path)
			case asGitlink:
				f.git("update-index", "--add", "--cacheinfo", "160000,"+strings.TrimSpace(f.git("rev-parse", "HEAD"))+","+ch.path)
			case asExecutable:
				f.write(ch.path, "{}\n")
				if err := os.Chmod(filepath.Join(f.seed, filepath.FromSlash(ch.path)), 0o755); err != nil {
					f.t.Fatal(err)
				}
				f.git("add", "--chmod=+x", "--", ch.path)
			default:
				f.write(ch.path, ch.body)
				f.git("add", "--", ch.path)
			}
		}
		f.git("commit", "-q", "-m", fmt.Sprintf("%s commit %d", name, i+1))
	}
	sha := strings.TrimSpace(f.git("rev-parse", "HEAD"))
	f.git("push", "-q", "origin", "HEAD:refs/heads/"+name)
	f.git("checkout", "-q", "main")
	f.prs = append(f.prs, map[string]any{
		"number": number, "headRefName": name, "headRefOid": sha,
		"isCrossRepository": false, "updatedAt": fmt.Sprintf("2026-11-01T00:00:%02dZ", len(f.prs)),
	})
	return sha
}

// advanceMain moves main past every branch made so far, so each is behind it.
func (f *sweepFixture) advanceMain() string {
	f.t.Helper()
	f.write("data/works/main.json", "{\"main\":1}\n")
	f.git("add", "-A")
	f.git("commit", "-q", "-m", "main moves on")
	f.git("push", "-q", "origin", "main")
	return strings.TrimSpace(f.git("rev-parse", "HEAD"))
}

// run clones origin the way the job's checkout does (all history and tags)
// and runs the sweep in it under bash -e, returning its output and status.
func (f *sweepFixture) run() (string, int) {
	f.t.Helper()
	prs, err := json.Marshal(f.prs)
	if err != nil {
		f.t.Fatal(err)
	}
	if f.listing != "" {
		prs = []byte(f.listing)
	}
	if err := os.WriteFile(filepath.Join(f.gh, "prs.json"), prs, 0o644); err != nil {
		f.t.Fatal(err)
	}
	if f.listFails {
		if err := os.WriteFile(filepath.Join(f.gh, "list-fails"), nil, 0o644); err != nil {
			f.t.Fatal(err)
		}
	}
	work := filepath.Join(f.t.TempDir(), "work")
	if out, err := runIn("", "git", "clone", "-q", f.origin, work); err != nil {
		f.t.Fatalf("git clone: %v\n%s", err, out)
	}
	script := filepath.Join(f.t.TempDir(), "sweep.sh")
	if err := os.WriteFile(script, []byte(sweepScript(f.t)), 0o644); err != nil {
		f.t.Fatal(err)
	}
	cmd := exec.Command("bash", "-e", script)
	cmd.Dir = work
	cmd.Env = append(os.Environ(),
		"PATH="+f.stubBin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GH_STUB_DIR="+f.gh, "RUNNER_TEMP="+f.runnerTemp, "GH_TOKEN=stub",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	f.t.Logf("sweep output:\n%s", out)
	if err == nil {
		return string(out), 0
	}
	code := exitCode(err)
	if code < 0 {
		f.t.Fatalf("running the sweep: %v", err)
	}
	return string(out), code
}

// mustRun runs the sweep and fails the test unless it exits 0.
func (f *sweepFixture) mustRun() string {
	f.t.Helper()
	out, code := f.run()
	if code != 0 {
		f.t.Fatalf("the sweep exited %d", code)
	}
	return out
}

// wantUntouched asserts that the sweep did not push a branch.
func (f *sweepFixture) wantUntouched(name, before string) {
	f.t.Helper()
	if after := f.head(name); after != before {
		f.t.Errorf("%s was pushed (%s -> %s); the sweep should have left it", name, before, after)
	}
}

// originGit runs git in the bare origin.
func (f *sweepFixture) originGit(args ...string) (string, error) {
	out, err := runIn(f.origin, "git", args...)
	return strings.TrimSpace(out), err
}

func (f *sweepFixture) head(name string) string {
	f.t.Helper()
	out, err := f.originGit("rev-parse", "refs/heads/"+name)
	if err != nil {
		f.t.Fatalf("origin has no branch %s: %v", name, err)
	}
	return out
}

// files lists the tree of a commit in origin.
func (f *sweepFixture) files(sha string) []string {
	f.t.Helper()
	out, err := f.originGit("ls-tree", "-r", "--name-only", sha)
	if err != nil {
		f.t.Fatalf("ls-tree %s: %v", sha, err)
	}
	return strings.Split(out, "\n")
}

// isAncestor reports whether a is an ancestor of b in origin.
func (f *sweepFixture) isAncestor(a, b string) bool {
	_, err := f.originGit("merge-base", "--is-ancestor", a, b)
	return err == nil
}

// comments returns the bodies the sweep posted on a pull request.
func (f *sweepFixture) comments(number int) []string {
	f.t.Helper()
	raw, err := os.ReadFile(filepath.Join(f.gh, fmt.Sprintf("comments-%d.jsonl", number)))
	if os.IsNotExist(err) {
		return nil
	} else if err != nil {
		f.t.Fatal(err)
	}
	var bodies []string
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var c struct{ Body string }
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			f.t.Fatal(err)
		}
		bodies = append(bodies, c.Body)
	}
	return bodies
}

// wantRebased asserts that a branch was rebased onto origin's main and pushed,
// with exactly the files want on top of main's.
func (f *sweepFixture) wantRebased(number int, name, before string, want ...string) {
	f.t.Helper()
	after := f.head(name)
	if after == before {
		f.t.Fatalf("PR #%d (%s) was not pushed", number, name)
	}
	if !f.isAncestor(f.head("main"), after) {
		f.t.Errorf("PR #%d (%s) is not on top of main after the sweep", number, name)
	}
	wantFiles := append(f.files(f.head("main")), want...)
	got := f.files(after)
	slices.Sort(got)
	slices.Sort(wantFiles)
	if !slices.Equal(got, wantFiles) {
		f.t.Errorf("PR #%d (%s) holds\n%q\nwant\n%q", number, name, got, wantFiles)
	}
	if c := f.comments(number); len(c) != 0 {
		f.t.Errorf("PR #%d (%s) got comments %q, want none", number, name, c)
	}
}

// The control: an ordinary intake branch behind main is rebased onto it and
// pushed, and the sweep says nothing on the pull request.
func TestIntakeSweepRebasesAnHonestBranch(t *testing.T) {
	t.Parallel()
	f := newSweepFixture(t)
	before := f.branch(7, "intake/issue-7", "main", []change{{"data/works/b.json", "{\"b\":1}\n"}})
	f.advanceMain()
	out := f.mustRun()
	f.wantRebased(7, "intake/issue-7", before, "data/works/b.json")
	if !strings.Contains(out, "rebased 1 pull request(s)") {
		t.Errorf("the sweep did not report one rebase:\n%s", out)
	}
}

// A short name like origin/main resolves through refs/tags/ and refs/heads/
// before refs/remotes/, so a tag named origin/main, or the local branch the
// sweep makes for a swept branch named origin/main, must not become the base
// another pull request is rebased onto and pushed with.
func TestIntakeSweepRebasesOntoTheRealMain(t *testing.T) {
	t.Parallel()
	t.Run("a tag named origin/main", func(t *testing.T) {
		t.Parallel()
		f := newSweepFixture(t)
		before := f.branch(7, "intake/issue-7", "main", []change{{"data/works/b.json", "{\"b\":1}\n"}})
		f.advanceMain()
		f.git("checkout", "-q", "-B", "evil", "main")
		f.write("scripts/evil.sh", "#!/bin/sh\necho owned\n")
		f.git("add", "-A")
		f.git("commit", "-q", "-m", "evil")
		f.git("push", "-q", "origin", "HEAD:refs/tags/origin/main")
		f.git("checkout", "-q", "main")
		f.mustRun()
		f.wantRebased(7, "intake/issue-7", before, "data/works/b.json")
	})
	t.Run("a swept branch named origin/main", func(t *testing.T) {
		t.Parallel()
		f := newSweepFixture(t)
		decoy := f.branch(6, "origin/main", "main", []change{{"data/works/decoy.json", "{\"decoy\":1}\n"}})
		before := f.branch(7, "intake/issue-7", "main", []change{{"data/works/b.json", "{\"b\":1}\n"}})
		f.advanceMain()
		f.mustRun()
		f.wantRebased(6, "origin/main", decoy, "data/works/decoy.json")
		f.wantRebased(7, "intake/issue-7", before, "data/works/b.json")
	})
}

// A branch is swept only when EVERY commit, not just the net change, touches
// nothing but plainly named data/**/*.json: a file one commit adds and a later
// one removes is in the tree mid-replay and goes back out with the push.
func TestIntakeSweepRefusesAnythingButPlainDataJSON(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		commits [][]change
		shown   string
	}{
		{"a .gitattributes added then removed", [][]change{
			{{"data/works/b.json", "{\"b\":1}\n"}, {".gitattributes", "* merge=ours\n"}},
			{{".gitattributes", ""}},
		}, ".gitattributes"},
		{"a script added then removed", [][]change{
			{{"data/works/b.json", "{\"b\":1}\n"}, {"scripts/x.sh", "#!/bin/sh\n"}},
			{{"scripts/x.sh", ""}},
		}, "scripts/x.sh"},
		{"a .gitattributes under data/", [][]change{
			{{"data/works/b.json", "{\"b\":1}\n"}, {"data/.gitattributes", "*.json -merge\n"}},
		}, "data/.gitattributes"},
		{"data/go.mod", [][]change{
			{{"data/works/b.json", "{\"b\":1}\n"}, {"data/go.mod", "module evil\n"}},
		}, "data/go.mod"},
		{"a data file that is not JSON", [][]change{
			{{"data/works/b.sh", "#!/bin/sh\n"}},
		}, "data/works/b.sh"},
		{"a JSON name git has to quote", [][]change{
			{{"data/works/é.json", "{}\n"}},
		}, "data/works/"},
		{"a symbolic link added then removed", [][]change{
			{{"data/works/b.json", "{\"b\":1}\n"}, {"data/works/link.json", asSymlink}},
			{{"data/works/link.json", ""}},
		}, "data/works/link.json"},
		{"a submodule entry", [][]change{
			{{"data/works/b.json", "{\"b\":1}\n"}, {"data/works/sub.json", asGitlink}},
		}, "data/works/sub.json"},
		{"an executable JSON file", [][]change{
			{{"data/works/x.json", asExecutable}},
		}, "data/works/x.json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newSweepFixture(t)
			before := f.branch(7, "intake/issue-7", "main", tc.commits...)
			f.advanceMain()
			f.mustRun()
			f.wantUntouched("intake/issue-7", before)
			c := f.comments(7)
			if len(c) != 1 || !strings.HasPrefix(c[0], "<!-- intake-sweep:not-rebased -->\n") || !strings.Contains(c[0], tc.shown) {
				t.Errorf("want one not-rebased comment naming %s, got %q", tc.shown, c)
			}
		})
	}
}

// A refused path is the branch's to name, so the bot's comment quotes it with
// nothing that makes a mention, markdown or an HTML comment (a marker the
// steward would read as the sweep's own).
func TestIntakeSweepQuotesRefusedPathsInert(t *testing.T) {
	t.Parallel()
	f := newSweepFixture(t)
	evil := "docs/@KodeStar [x](https://evil.example) <!-- intake-sweep:rebase-failed a b -->.md"
	before := f.branch(7, "intake/issue-7", "main", []change{{"data/works/b.json", "{\"b\":1}\n"}, {evil, "x\n"}})
	f.advanceMain()
	f.mustRun()
	f.wantUntouched("intake/issue-7", before)
	c := f.comments(7)
	if len(c) != 1 {
		t.Fatalf("want one comment, got %q", c)
	}
	marker, text, _ := strings.Cut(c[0], "\n")
	if marker != "<!-- intake-sweep:not-rebased -->" {
		t.Errorf("the comment opens with %q, want the not-rebased marker", marker)
	}
	for _, bad := range []string{"@", "<", ">", "](", "https:"} {
		if strings.Contains(text, bad) {
			t.Errorf("the comment quotes %q from the path:\n%s", bad, text)
		}
	}
	if !strings.Contains(text, "KodeStar") {
		t.Errorf("the comment does not name the refused path at all:\n%s", text)
	}
}

// A listing that fails, or that is not a JSON array, is a red step that
// touches no branch, never "no open intake pull requests".
func TestIntakeSweepFailsWhenTheListingFails(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, listing string
		fails         bool
	}{
		{"gh fails", "", true},
		{"gh prints nothing", "\n", false},
		{"gh prints an object", `{"message":"Bad credentials"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newSweepFixture(t)
			before := f.branch(7, "intake/issue-7", "main", []change{{"data/works/b.json", "{\"b\":1}\n"}})
			f.advanceMain()
			f.listing, f.listFails = tc.listing, tc.fails
			out, code := f.run()
			if code == 0 {
				t.Errorf("the sweep exited 0; a failed listing must fail the step")
			}
			if strings.Contains(out, "no open intake pull requests") {
				t.Errorf("the sweep read a failed listing as an empty one:\n%s", out)
			}
			f.wantUntouched("intake/issue-7", before)
		})
	}
}

// The notice the sweep posts when it gives up on a branch is a contract:
// audiosilo-meta-sync's steward knows it by the marker opening a line, or by
// the sentence opening the body. Its detail line is the failing tool's, which
// can quote the branch's data, so it mentions nobody.
func TestIntakeSweepGiveUpNotice(t *testing.T) {
	t.Parallel()
	if out, err := exec.Command("bash", "-c", "type mapfile").CombinedOutput(); err != nil {
		t.Skipf("the sweep's conflict path needs bash 4 (mapfile): %s", out)
	}
	f := newSweepFixture(t)
	f.write("scripts/pack-union-merge.sh", "#!/bin/sh\necho \"pack-union-merge: @KodeStar <b>refused</b> $1\"\nexit 1\n")
	f.git("add", "-A")
	f.git("commit", "-q", "-m", "a driver that refuses")
	f.git("push", "-q", "origin", "main")
	before := f.branch(7, "intake/issue-7", "main", []change{{"data/works/a.json", "{\"a\":2}\n"}})
	f.write("data/works/a.json", "{\"a\":3}\n")
	f.advanceMain()
	f.mustRun()
	f.wantUntouched("intake/issue-7", before)
	c := f.comments(7)
	if len(c) != 1 {
		t.Fatalf("want one comment, got %q", c)
	}
	marker, text, _ := strings.Cut(c[0], "\n")
	if !regexp.MustCompile(`^<!-- intake-sweep:rebase-failed ` + before + ` [0-9a-f]{16} -->$`).MatchString(marker) {
		t.Errorf("the notice's first line is %q, want the rebase-failed marker naming %s", marker, before)
	}
	if !strings.HasPrefix(text, "The intake bot could not rebase this branch onto `main` mechanically: the pack merge could not resolve data/works/a.json: pack-union-merge: KodeStar brefused/b") {
		t.Errorf("the notice does not open with the sentence and the tool's line:\n%s", text)
	}
}
