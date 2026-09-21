package recorddiff

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kodestar/audiosilo-meta/internal/testpack"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// git_test.go is the SMOKE test: the fixture suites prove what the comparison
// says, and this proves it survives the real thing - a checkout of this
// repository, its git object store, and whatever the last commit happened to
// touch.
//
// It deliberately asserts almost nothing about the output. What it is here to
// catch is the class of failure a fixture cannot reach: a cat-file protocol
// mistake, a path the walk does not recognize, an entry in the live tree that
// the summary renderer panics on. The CONTENT of HEAD~1 is not this suite's to
// know.

func TestAgainstTheRealCheckout(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	repo, err := repoRoot()
	if err != nil {
		t.Skipf("not inside a git checkout: %v", err)
	}
	if _, err := revParse(repo, "HEAD~1"); err != nil {
		t.Skipf("HEAD~1 is not reachable (a shallow checkout): %v", err)
	}

	paths, err := GitChangedPaths(repo, "data", "HEAD~1", "HEAD")
	if err != nil {
		t.Fatalf("GitChangedPaths: %v", err)
	}

	base, err := OpenGitSource(repo, "data", "HEAD~1")
	if err != nil {
		t.Fatalf("OpenGitSource(base): %v", err)
	}
	defer func() { _ = base.Close() }()
	head, err := OpenGitSource(repo, "data", "HEAD")
	if err != nil {
		t.Fatalf("OpenGitSource(head): %v", err)
	}
	defer func() { _ = head.Close() }()

	d, err := Compute(paths, base, head)
	if err != nil {
		t.Fatalf("Compute over HEAD~1..HEAD: %v", err)
	}
	d.Base, d.Head = base.Rev(), head.Rev()

	// The render must not blow the budget it was given, whatever the last commit
	// was, and must still carry its header.
	const budget = 20000
	text := d.Text(budget)
	if len(text) > budget {
		t.Errorf("render is %d bytes, over the %d-byte budget", len(text), budget)
	}
	if !strings.Contains(text, "ENTRY-LEVEL SUMMARY") {
		t.Errorf("the render lost its header:\n%s", text)
	}
	if _, err := d.JSON(); err != nil {
		t.Errorf("JSON: %v", err)
	}
}

// repoRoot finds the checkout the test is running in.
func repoRoot() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// fixtureRepo builds a throwaway checkout with two commits and returns its path.
// seed is the data tree of the first commit, change of the second; a path mapped
// to "" in change is deleted by it.
func fixtureRepo(t *testing.T, seed, change map[string]string) string {
	t.Helper()
	dir := newRepo(t)
	writeData(t, dir, seed)
	commitAll(t, dir, "seed")
	writeData(t, dir, change)
	commitAll(t, dir, "change")
	return dir
}

// gitIn runs one git command in a fixture repository.
func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	// A fixture repository must not read the machine's identity, hooks or
	// signing config: the commits here are scratch objects, not history.
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
}

// newRepo initializes an empty fixture repository on a `main` branch.
func newRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "main")
	return dir
}

// writeData lays files into the repository's data root. A path mapped to "" is
// deleted.
func writeData(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		full := filepath.Join(dir, "data", filepath.FromSlash(rel))
		if body == "" {
			if err := os.Remove(full); err != nil {
				t.Fatalf("remove %s: %v", rel, err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
}

// commitAll stages everything and commits it.
func commitAll(t *testing.T, dir, msg string) {
	t.Helper()
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", msg)
}

// diffLikeTheCommand is the resolution cmd/metadiff performs, run end to end, so
// a test asserts what the tool actually does rather than what one of its helpers
// returns.
func diffLikeTheCommand(t *testing.T, repo, base, head string) *Diff {
	t.Helper()
	mergeBase, err := MergeBase(repo, base, head)
	if err != nil {
		t.Fatalf("MergeBase: %v", err)
	}
	paths, err := GitChangedPaths(repo, "data", mergeBase, head)
	if err != nil {
		t.Fatalf("GitChangedPaths: %v", err)
	}
	baseSrc, err := OpenGitSource(repo, "data", mergeBase)
	if err != nil {
		t.Fatalf("OpenGitSource(base): %v", err)
	}
	defer func() { _ = baseSrc.Close() }()
	headSrc, err := OpenGitSource(repo, "data", head)
	if err != nil {
		t.Fatalf("OpenGitSource(head): %v", err)
	}
	defer func() { _ = headSrc.Close() }()
	d, err := Compute(paths, baseSrc, headSrc)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	return d
}

// onePack renders a pack file holding the given entries.
func onePack(t *testing.T, entries map[string]string) string {
	t.Helper()
	f := pack.NewFile()
	for slug, raw := range entries {
		f.Set(slug, []byte(raw))
	}
	out, err := f.Bytes()
	if err != nil {
		t.Fatalf("render pack: %v", err)
	}
	return string(out)
}

// TestGitSourceHandlesAPathOneSideDoesNotHold is the cat-file PROTOCOL test.
//
// A file the change created is absent on the base side, and git answers that
// with a one-line "<spec> missing" reply carrying no payload. Getting that wrong
// is not a wrong answer about one file: the batch stream is strictly
// request/response, so a reply half-consumed (or a payload skipped that was
// never sent) desynchronizes every read after it. The fixture therefore puts the
// missing path FIRST in sorted order and a real one after it, so a desync would
// show up as garbage for the second file rather than as nothing at all.
func TestGitSourceHandlesAPathOneSideDoesNotHold(t *testing.T) {
	works := onePack(t, map[string]string{"the-thing": testpack.WorkJSON(t, "the-thing", "The Thing")})
	repo := fixtureRepo(t,
		map[string]string{"works/0/0.json": works},
		map[string]string{
			// people/0.json sorts BEFORE works/0/0.json and exists only in head.
			"people/0.json":  onePack(t, map[string]string{"jane-doe": testpack.PersonJSON(t, "jane-doe", "Jane Doe")}),
			"works/0/0.json": onePack(t, map[string]string{"the-thing": testpack.WorkJSON(t, "the-thing", "The Thing Renamed")}),
		})

	paths, err := GitChangedPaths(repo, "data", "HEAD~1", "HEAD")
	if err != nil {
		t.Fatalf("GitChangedPaths: %v", err)
	}
	if len(paths) != 2 || paths[0] != "people/0.json" {
		t.Fatalf("changed paths = %v, want people/0.json first", paths)
	}

	base, err := OpenGitSource(repo, "data", "HEAD~1")
	if err != nil {
		t.Fatalf("OpenGitSource(base): %v", err)
	}
	defer func() { _ = base.Close() }()

	// The missing path reads as empty and NOT as an error...
	raw, found, err := base.Read("people/0.json")
	if err != nil || found || raw != nil {
		t.Fatalf("Read of an absent path = (%q, %v, %v), want (nil, false, nil)", raw, found, err)
	}
	// ...and the very next read is still in step with the stream.
	raw, found, err = base.Read("works/0/0.json")
	if err != nil || !found {
		t.Fatalf("Read after a missing path = (%v, %v)", found, err)
	}
	if string(raw) != works {
		t.Fatalf("Read after a missing path returned the wrong bytes:\n%s", raw)
	}

	head, err := OpenGitSource(repo, "data", "HEAD")
	if err != nil {
		t.Fatalf("OpenGitSource(head): %v", err)
	}
	defer func() { _ = head.Close() }()

	d, err := Compute(paths, base, head)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if len(d.Added) != 1 || d.Added[0].Slug != "jane-doe" {
		t.Errorf("added = %+v, want the person the change created", d.Added)
	}
	if len(d.Modified) != 1 || d.Modified[0].Slug != "the-thing" {
		t.Errorf("modified = %+v, want the work the change renamed", d.Modified)
	}
	if len(d.Warnings) != 0 {
		t.Errorf("warnings = %v, want none", d.Warnings)
	}
}

// TestARenamedPackIsNotReadAsAnAddition pins the one git behaviour that could
// quietly invert this whole package's purpose. A pack REBIND renames the file
// (its bound is its name), and `git diff --name-only` reports a rename as its
// DESTINATION path alone - so the source path never reached Compute, the
// entries were absent from the base side, and a pack nobody edited was reported
// as a tranche of brand-new records. Rename detection is therefore off, and the
// entries cancel out as moved-only, which is what they are.
func TestARenamedPackIsNotReadAsAnAddition(t *testing.T) {
	body := onePack(t, map[string]string{
		"aaa-work": testpack.WorkJSON(t, "aaa-work", "A"),
		"bbb-work": testpack.WorkJSON(t, "bbb-work", "B"),
	})
	repo := fixtureRepo(t,
		map[string]string{"works/0/aaa-work.json": body},
		// The very same bytes under a new bound: git scores this a 100% rename.
		map[string]string{"works/0/aaa-work.json": "", "works/0/aab-work.json": body})

	paths, err := GitChangedPaths(repo, "data", "HEAD~1", "HEAD")
	if err != nil {
		t.Fatalf("GitChangedPaths: %v", err)
	}
	if len(paths) != 2 {
		t.Fatalf("changed paths = %v, want BOTH halves of the rename", paths)
	}

	base, err := OpenGitSource(repo, "data", "HEAD~1")
	if err != nil {
		t.Fatalf("OpenGitSource(base): %v", err)
	}
	defer func() { _ = base.Close() }()
	head, err := OpenGitSource(repo, "data", "HEAD")
	if err != nil {
		t.Fatalf("OpenGitSource(head): %v", err)
	}
	defer func() { _ = head.Close() }()

	d, err := Compute(paths, base, head)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if !d.Empty() {
		t.Errorf("a pure pack rename is a RECORD change: added=%v removed=%v modified=%v",
			d.Added, d.Removed, d.Modified)
	}
	if c := d.Counts[pack.FamilyWorks]; c.Moved != 2 || c.Added != 0 || c.Removed != 0 {
		t.Errorf("works counts = %+v, want the two entries counted as moved-only", c)
	}
}

// TestMergeBaseIsWhatTheBaseSideIsReadAt pins the resolution cmd/metadiff does
// before it opens either side. A pull request's base sha is the base BRANCH's
// tip and moves while the branch sits open, so reading the base side's bytes
// there would report every record main gained meanwhile as removed by this
// change.
func TestMergeBaseIsWhatTheBaseSideIsReadAt(t *testing.T) {
	work := func(slug, title string) string {
		return onePack(t, map[string]string{slug: testpack.WorkJSON(t, slug, title)})
	}
	repo := newRepo(t)

	// M: the fork point, holding one work.
	writeData(t, repo, map[string]string{"works/0/0.json": work("a-work", "A")})
	commitAll(t, repo, "fork point")

	// The branch under review adds a work of its own...
	gitIn(t, repo, "checkout", "-q", "-b", "feature")
	writeData(t, repo, map[string]string{"works/0/0.json": onePack(t, map[string]string{
		"a-work": testpack.WorkJSON(t, "a-work", "A"),
		"b-work": testpack.WorkJSON(t, "b-work", "B"),
	})})
	commitAll(t, repo, "the branch's own change")

	// ...while main moves on underneath it, which is what a base sha names.
	gitIn(t, repo, "checkout", "-q", "main")
	writeData(t, repo, map[string]string{"works/0/0.json": onePack(t, map[string]string{
		"a-work": testpack.WorkJSON(t, "a-work", "A"),
		"c-work": testpack.WorkJSON(t, "c-work", "C"),
	})})
	commitAll(t, repo, "somebody else's merge")

	// The base sha the workflow passes is main's TIP, not the fork point.
	d := diffLikeTheCommand(t, repo, "main", "feature")

	if len(d.Removed) != 0 {
		t.Errorf("the branch is credited with removing %+v; main's own commits are not this branch's change", d.Removed)
	}
	if len(d.Added) != 1 || d.Added[0].Slug != "b-work" {
		t.Errorf("added = %+v, want only the branch's own b-work", d.Added)
	}
	if c := d.Counts[pack.FamilyWorks]; c.Added != 1 || c.Removed != 0 || c.Modified != 0 {
		t.Errorf("works counts = %+v, want 1 added and nothing else", c)
	}
}

// TestGitSourceCloseAlwaysReaps pins that the batch process is waited for, that
// a second Close is a no-op, and - the case that could hang - that a reader
// whose stream was abandoned mid-reply still closes rather than blocking forever
// on a git stuck writing into a full pipe.
func TestGitSourceCloseAlwaysReaps(t *testing.T) {
	big := strings.Repeat("x", 300<<10) // comfortably past a pipe buffer
	repo := fixtureRepo(t,
		map[string]string{"works/0/0.json": onePack(t, map[string]string{"a-work": testpack.WorkJSON(t, "a-work", "A")})},
		map[string]string{"works/0/0.json": onePack(t, map[string]string{"a-work": testpack.WorkJSON(t, "a-work", big)})})

	g, err := OpenGitSource(repo, "data", "HEAD")
	if err != nil {
		t.Fatalf("OpenGitSource: %v", err)
	}
	// Ask for the large object and then abandon the reply by never reading it:
	// this is the shape that leaves git blocked on a write.
	if _, err := g.in.Write([]byte(g.Rev() + ":data/works/0/0.json\n")); err != nil {
		t.Fatalf("write request: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- g.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Close hung on a reader whose reply was abandoned mid-stream")
	}
	if err := g.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

// TestGitSourceRefusesToReadOnABrokenStream pins the latch: once a read has
// failed, every later read reports the same failure instead of returning
// whatever the desynchronized stream happens to hold.
func TestGitSourceRefusesToReadOnABrokenStream(t *testing.T) {
	repo := fixtureRepo(t,
		map[string]string{"works/0/0.json": onePack(t, map[string]string{"a-work": testpack.WorkJSON(t, "a-work", "A")})},
		map[string]string{"works/0/0.json": onePack(t, map[string]string{"a-work": testpack.WorkJSON(t, "a-work", "B")})})

	g, err := OpenGitSource(repo, "data", "HEAD")
	if err != nil {
		t.Fatalf("OpenGitSource: %v", err)
	}
	defer func() { _ = g.Close() }()

	// Killing the process makes the next read fail; whatever the first error is,
	// the SECOND read must report one too rather than answering from a stream
	// that is no longer in step.
	if err := g.cmd.Process.Kill(); err != nil {
		t.Fatalf("kill: %v", err)
	}
	if _, _, err := g.Read("works/0/0.json"); err == nil {
		t.Skip("the killed process still answered the first read; nothing to latch")
	}
	if _, _, err := g.Read("works/0/0.json"); err == nil {
		t.Fatal("a read on a broken stream returned success")
	}
}
