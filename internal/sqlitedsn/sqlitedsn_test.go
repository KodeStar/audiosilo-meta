package sqlitedsn

import (
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite" // the driver whose own URI parser is what this rule is about
)

// TestReadOnlyEscapesThePath pins the rule from BOTH sides: the DSN this package
// builds names the file the operator named and keeps mode=ro, and the naive
// splice it replaced does neither. The three characters are SQLite's documented
// URI syntax, not a driver quirk - '?' starts the query, '#' a fragment, and a
// literal '%NN' is decoded - so a path carrying one is silently a different
// file, opened read-WRITE (and created if absent).
func TestReadOnlyEscapesThePath(t *testing.T) {
	base := t.TempDir()
	for _, dirName := range awkwardDirs {
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

// awkwardDirs are the directory names the rule is about: the three characters
// SQLite's URI parser treats as syntax, plus a space for the ordinary
// percent-encoding the escaping also has to get right.
var awkwardDirs = []string{"plain", "a?b", "c#d", "e%2Ff", "g h"}

// TestReadOnlyOpensAnAwkwardPath takes the rule to the DRIVER, which is the only
// place it is really settled: TestReadOnlyEscapesThePath above reasons about the
// DSN through net/url, and a URI is only wrong if the thing parsing it disagrees
// - so this opens a real database through modernc's own URI parser at every one
// of the shapes, and reads a row back out of it.
//
// It lives here rather than in either caller because both of them (internal/serve's
// snapshot loader, internal/issueform's works-artifact stand-in) inherit exactly
// this; each keeps ONE behavioural open of its own, over the '?' shape, to pin
// that it really goes through this package.
func TestReadOnlyOpensAnAwkwardPath(t *testing.T) {
	for _, dirName := range awkwardDirs {
		t.Run(dirName, func(t *testing.T) {
			base := t.TempDir()
			// Built at an ordinary path and MOVED: a plain DSN is split on '?'
			// by the driver too, so a database written straight to the path under
			// test would leave no file there at all.
			path := filepath.Join(base, dirName, "meta.sqlite")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(seedDB(t, filepath.Join(base, "seed.sqlite")), path); err != nil {
				t.Fatal(err)
			}

			dsn, err := ReadOnly(path)
			if err != nil {
				t.Fatalf("ReadOnly: %v", err)
			}
			db, err := sql.Open("sqlite", dsn)
			if err != nil {
				t.Fatalf("open %q: %v", dsn, err)
			}
			defer func() { _ = db.Close() }()

			var got string
			if err := db.QueryRow(`SELECT v FROM marker`).Scan(&got); err != nil {
				t.Fatalf("the DSN did not open the database that was there: %v", err)
			}
			if got != "the real artifact" {
				t.Errorf("read %q, want the seeded marker - a different file was opened", got)
			}
			// mode=ro survived the escaping, so the handle cannot write.
			if _, err := db.Exec(`INSERT INTO marker(v) VALUES ('written')`); err == nil {
				t.Error("the handle is writable: mode=ro was lost")
			}
			// And nothing was CREATED at the truncated spelling the naive splice
			// would have named, in THIS directory.
			// ('e' is what a DECODED %2F truncates to, the third shape's own.)
			for _, stray := range []string{"a", "c", "e", "g"} {
				if _, err := os.Stat(filepath.Join(base, stray)); err == nil {
					t.Errorf("a truncated artifact path %q was created", stray)
				}
			}
		})
	}
}

// seedDB writes a one-row database at an ORDINARY path and returns it.
func seedDB(t *testing.T, path string) string {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`CREATE TABLE marker (v TEXT); INSERT INTO marker(v) VALUES ('the real artifact')`); err != nil {
		t.Fatal(err)
	}
	return path
}
