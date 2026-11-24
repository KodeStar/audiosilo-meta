package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSameFile pins the guard that refuses a --previous naming the output: Build
// deletes -o first, so the comparison would be with the new artifact itself.
func TestSameFile(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.sqlite")
	b := filepath.Join(dir, "b.sqlite")
	for _, p := range []string{a, b} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(dir, "link.sqlite")
	if err := os.Symlink(a, link); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	cases := []struct {
		name string
		x, y string
		want bool
	}{
		{"one path", a, a, true},
		{"relative and absolute", "a.sqlite", a, true},
		{"through a link", link, a, true},
		{"two files", a, b, false},
		{"an output not yet written", a, filepath.Join(dir, "new.sqlite"), false},
	}
	for _, tc := range cases {
		got, err := sameFile(tc.x, tc.y)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got != tc.want {
			t.Errorf("%s: sameFile(%q, %q) = %v, want %v", tc.name, tc.x, tc.y, got, tc.want)
		}
	}
}
