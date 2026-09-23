package sqlitedsn

import (
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadOnlyEscapesThePath pins the rule from BOTH sides: the DSN this package
// builds names the file the operator named and keeps mode=ro, and the naive
// splice it replaced does neither. The three characters are SQLite's documented
// URI syntax, not a driver quirk - '?' starts the query, '#' a fragment, and a
// literal '%NN' is decoded - so a path carrying one is silently a different
// file, opened read-WRITE (and created if absent).
func TestReadOnlyEscapesThePath(t *testing.T) {
	base := t.TempDir()
	for _, dirName := range []string{"plain", "a?b", "c#d", "e%2Ff", "g h"} {
		t.Run(dirName, func(t *testing.T) {
			path := filepath.Join(base, dirName, "meta.sqlite")

			dsn, err := ReadOnly(path)
			if err != nil {
				t.Fatalf("ReadOnly: %v", err)
			}
			u, err := url.Parse(dsn)
			if err != nil {
				t.Fatalf("parse %q: %v", dsn, err)
			}
			if u.Scheme != "file" || u.Host != "" {
				t.Errorf("DSN %q is not a rooted file URI", dsn)
			}
			if u.Path != path {
				t.Errorf("DSN names %q, want the path %q", u.Path, path)
			}
			if u.Query().Get("mode") != "ro" {
				t.Errorf("DSN %q lost mode=ro", dsn)
			}
			// Nothing but the read-only parameter may reach the URI as syntax.
			if i := strings.IndexAny(dsn, "?#"); i >= 0 && dsn[i:] != "?mode=ro" {
				t.Errorf("DSN carries an unescaped delimiter: %s", dsn)
			}
		})
	}
}

// TestSplicedDSNLosesThePathAndTheReadOnlyFlag is the finding's teeth: the
// spelling this package replaced reads a '?' in the path as the start of the
// query, so SQLite opens a DIFFERENT file with NO mode=ro. It is pinned from the
// failing side because the bug is silent by nature - the open succeeds.
func TestSplicedDSNLosesThePathAndTheReadOnlyFlag(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a?b", "meta.sqlite")
	spliced, err := url.Parse("file:" + path + "?mode=ro")
	if err != nil {
		t.Fatalf("parse the spliced DSN: %v", err)
	}
	if spliced.Path == path {
		t.Fatal("the spliced DSN named the right file - this test no longer pins anything")
	}
	if spliced.Query().Get("mode") == "ro" {
		t.Fatal("the spliced DSN kept mode=ro - this test no longer pins anything")
	}
}
