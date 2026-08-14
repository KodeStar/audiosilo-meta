package check

import (
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// The rule convention of this repo: every rule ships with a passing fixture and a
// violating one. The fixture syntax is check_test.go's - an address writeEntities
// does not recognize is written verbatim, which is exactly what the redirects
// file needs (it is not a pack and sits under no family).

// redirectTree returns baseValid plus a redirects file with the given body.
func redirectTree(body string) map[string]string {
	files := baseValid()
	files["redirects.json"] = body
	return files
}

// loadRedirectFixture writes the tree and returns the problems it reports about
// the redirects file only, so an unrelated rule cannot make a case pass.
func loadRedirectFixture(t *testing.T, files map[string]string) []Problem {
	t.Helper()
	dir := t.TempDir()
	writeEntities(t, dir, files)
	res := Load(dir)
	var out []Problem
	for _, p := range res.Problems {
		if strings.HasPrefix(p.Path, pack.RedirectsFile) {
			out = append(out, p)
		}
	}
	return out
}

// TestRedirectsValid is the passing fixture, and it is the state the repository
// ships in: an empty table for every namespace, plus one real tombstone. It also
// pins the accounting - a file under no family root would otherwise be reported
// as an unrecognized location on every run.
func TestRedirectsValid(t *testing.T) {
	dir := t.TempDir()
	files := baseValid()
	// A retired duplicate of book-one, and a person nobody holds any more.
	files["redirects.json"] = `{"people":{"author-uno":"author-one"},"series":{},"works":{"book-uno":"book-one"}}`
	writeEntities(t, dir, files)
	res := Load(dir)
	if !res.OK() {
		t.Fatalf("valid redirects reported problems: %v", res.Problems)
	}
	if got := res.Catalog.Redirects[model.RedirectWorks]["book-uno"]; got != "book-one" {
		t.Errorf("catalog redirect works/book-uno = %q, want book-one", got)
	}
	if got := res.Catalog.Redirects[model.RedirectPeople]["author-uno"]; got != "author-one" {
		t.Errorf("catalog redirect people/author-uno = %q, want author-one", got)
	}
}

// TestRedirectsAbsentIsFine: every tree that has retired nothing - which is every
// tree before this mechanism existed, and every fixture in this package - stays
// green, and the catalog simply carries no table.
func TestRedirectsAbsentIsFine(t *testing.T) {
	dir := t.TempDir()
	writeEntities(t, dir, baseValid())
	res := Load(dir)
	if !res.OK() {
		t.Fatalf("a tree with no redirects file reported problems: %v", res.Problems)
	}
	if res.Catalog.Redirects != nil {
		t.Errorf("Redirects = %v, want nil", res.Catalog.Redirects)
	}
}

// TestRedirectsEmptyTableIsFine is the file the repository commits before
// anything has been retired.
func TestRedirectsEmptyTableIsFine(t *testing.T) {
	if probs := loadRedirectFixture(t, redirectTree(`{"people":{},"series":{},"works":{}}`)); len(probs) != 0 {
		t.Errorf("an empty table reported problems: %v", probs)
	}
}

// TestRedirectRules is the violating half: each case is one way the table could
// stop resolving, and each must be reported.
func TestRedirectRules(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "target does not exist",
			body: `{"people":{},"series":{},"works":{"book-uno":"book-nine"}}`,
			want: "no live works id",
		},
		{
			name: "source is a live record",
			body: `{"people":{},"series":{},"works":{"book-one":"book-one-2"}}`,
			want: "source is a live works id",
		},
		{
			name: "self redirect",
			body: `{"people":{},"series":{},"works":{"book-uno":"book-uno"}}`,
			want: "points at itself",
		},
		{
			name: "chain",
			body: `{"people":{},"series":{},"works":{"book-uno":"book-dos","book-dos":"book-one"}}`,
			want: "is itself redirected to",
		},
		{
			name: "reserved source",
			body: `{"people":{},"series":{},"works":{"search":"book-one"}}`,
			want: "source \"search\" is a reserved slug",
		},
		{
			name: "reserved target",
			body: `{"people":{},"series":{},"works":{"book-uno":"latest"}}`,
			want: "target \"latest\" is a reserved slug",
		},
		{
			name: "person namespace is checked too",
			body: `{"people":{"author-uno":"author-nine"},"series":{},"works":{}}`,
			want: "no live people id",
		},
		{
			name: "series namespace is checked too",
			body: `{"people":{},"series":{"series-uno":"series-nine"},"works":{}}`,
			want: "no live series id",
		},
		{
			// A slug that exists in ANOTHER namespace is no target: the id spaces
			// are separate, and the route the redirect names is this one.
			name: "target from another namespace",
			body: `{"people":{"author-uno":"book-one"},"series":{},"works":{}}`,
			want: "no live people id",
		},
		{
			name: "key is not a slug",
			body: `{"people":{},"series":{},"works":{"Book Uno":"book-one"}}`,
			want: "does not match pattern",
		},
		{
			// A duplicate key is invisible to encoding/json (last wins) and to the
			// schema validator, so without the scan this dropped a redirect with the
			// gate green - the failure pkg/pack refuses for a pack, one file over.
			name: "duplicate retired slug",
			body: `{"people":{},"series":{},"works":{"book-uno":"book-one","book-uno":"book-one"}}`,
			want: `duplicate key "book-uno"`,
		},
		{
			name: "duplicate namespace",
			body: `{"people":{},"series":{},"works":{"book-uno":"book-one"},"works":{}}`,
			want: `duplicate key "works"`,
		},
		{
			name: "unknown namespace",
			body: `{"people":{},"recordings":{"a":"b"},"series":{},"works":{}}`,
			want: "additional properties 'recordings' not allowed",
		},
		{
			name: "not an object",
			body: `{"people":{},"series":{},"works":[]}`,
			want: "got array, want object",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			probs := loadRedirectFixture(t, redirectTree(tc.body))
			if !hasProblem(probs, tc.want) {
				t.Errorf("problems %v, none containing %q", probs, tc.want)
			}
		})
	}
}

// TestRedirectProblemNamesTheEntry pins the report's address: a Problem names the
// smallest thing that can be wrong, which here is the namespace and the retired
// slug rather than the whole file.
func TestRedirectProblemNamesTheEntry(t *testing.T) {
	probs := loadRedirectFixture(t, redirectTree(`{"people":{},"series":{},"works":{"book-uno":"book-nine"}}`))
	if len(probs) != 1 {
		t.Fatalf("problems = %v, want exactly one", probs)
	}
	if probs[0].Path != "redirects.json: works book-uno" {
		t.Errorf("path = %q", probs[0].Path)
	}
}

// TestRedirectSchemaFailureSkipsTheRules: a document whose SHAPE is the problem
// is reported once, not once per rule that cannot mean anything yet.
func TestRedirectSchemaFailureSkipsTheRules(t *testing.T) {
	probs := loadRedirectFixture(t, redirectTree(`{"people":{},"series":{},"works":{"Book Uno":"book-nine"}}`))
	if hasProblem(probs, "no live works id") {
		t.Errorf("a cross-record rule ran on a schema-rejected document: %v", probs)
	}
}

// TestRedirectsUnreadableIsReported: a broken file must fail the check rather
// than read as "no redirects".
func TestRedirectsUnreadableIsReported(t *testing.T) {
	probs := loadRedirectFixture(t, redirectTree(`{"works":`))
	if !hasProblem(probs, "invalid JSON") {
		t.Errorf("problems %v, want an invalid-JSON report", probs)
	}
}
