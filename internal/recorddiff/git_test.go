package recorddiff

import (
	"os/exec"
	"strings"
	"testing"
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
	if _, err := RevParse(repo, "HEAD~1"); err != nil {
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
