package querytest

import (
	"bytes"
	"os"
	"testing"
)

// TestBuildIsDeterministicAndSmall: two builds are byte-identical (so a
// consumer's ETag or golden naming the artifact is stable), and the artifact is
// small enough to build in every test that wants one.
func TestBuildIsDeterministicAndSmall(t *testing.T) {
	a, err := os.ReadFile(Build(t, t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(Build(t, t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Error("two builds of the fixture differ")
	}
	if len(a) >= 1<<20 {
		t.Errorf("the fixture artifact is %d bytes, want well under 1 MiB", len(a))
	}
}
