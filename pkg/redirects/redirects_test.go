package redirects

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

func read(t *testing.T, dir string) string {
	t.Helper()
	raw, err := os.ReadFile(Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// TestLoadAbsentIsEmpty covers the tree that has never retired a slug: the flow
// a writer follows must not depend on the file already being there.
func TestLoadAbsentIsEmpty(t *testing.T) {
	got, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load of a tree with no redirects file: %v", err)
	}
	if got.Len() != 0 {
		t.Errorf("Len = %d, want 0", got.Len())
	}
	for _, kind := range model.RedirectKinds() {
		if got[kind] == nil {
			t.Errorf("namespace %q is nil: a caller must be able to index the result", kind)
		}
	}
}

// TestWriteThenLoadRoundTrips also pins the BYTES: the file is canonical (sorted
// keys, 2-space indent, one trailing newline), because metafmt --check judges it
// by exactly that rule.
func TestWriteThenLoadRoundTrips(t *testing.T) {
	dir := t.TempDir()
	r := model.NewRedirects()
	if err := Add(r, model.RedirectWorks, "old-book", "new-book"); err != nil {
		t.Fatal(err)
	}
	if err := Write(dir, r); err != nil {
		t.Fatal(err)
	}
	const want = "{\n  \"people\": {},\n  \"series\": {},\n  \"works\": {\n    \"old-book\": \"new-book\"\n  }\n}\n"
	if got := read(t, dir); got != want {
		t.Errorf("file =\n%q\nwant\n%q", got, want)
	}
	back, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := back.Target(model.RedirectWorks, "old-book"); got != "new-book" {
		t.Errorf("reloaded target = %q", got)
	}
}

// TestWriteAlwaysCarriesEveryNamespace: the schema requires all three keys, so a
// table built by hand with one namespace still writes a complete file - the shape
// does not change the first time a namespace gains an entry.
func TestWriteAlwaysCarriesEveryNamespace(t *testing.T) {
	dir := t.TempDir()
	if err := Write(dir, model.Redirects{model.RedirectSeries: {"old-series": "new-series"}}); err != nil {
		t.Fatal(err)
	}
	const want = "{\n  \"people\": {},\n  \"series\": {\n    \"old-series\": \"new-series\"\n  },\n  \"works\": {}\n}\n"
	if got := read(t, dir); got != want {
		t.Errorf("file =\n%q\nwant\n%q", got, want)
	}
}

// TestAddCollapsesForward is the first half of chain collapsing: the repair pass
// retires b for c, then later retires a for b. a must land on c, because a
// resolver does ONE lookup (and pkg/check refuses the chain outright).
func TestAddCollapsesForward(t *testing.T) {
	r := model.NewRedirects()
	if err := Add(r, model.RedirectWorks, "b", "c"); err != nil {
		t.Fatal(err)
	}
	if err := Add(r, model.RedirectWorks, "a", "b"); err != nil {
		t.Fatal(err)
	}
	if got := r.Target(model.RedirectWorks, "a"); got != "c" {
		t.Errorf("a -> %q, want c (collapsed through b)", got)
	}
	if got := r.Target(model.RedirectWorks, "b"); got != "c" {
		t.Errorf("b -> %q, want c", got)
	}
}

// TestAddRepointsWhatPointedAtTheSource is the other half: something already
// redirected to a, and a is now retired too. Yesterday's tombstone must be
// repointed, or it becomes a chain today.
func TestAddRepointsWhatPointedAtTheSource(t *testing.T) {
	r := model.NewRedirects()
	for _, hop := range [][2]string{{"older", "a"}, {"other", "a"}} {
		if err := Add(r, model.RedirectPeople, hop[0], hop[1]); err != nil {
			t.Fatal(err)
		}
	}
	if err := Add(r, model.RedirectPeople, "a", "survivor"); err != nil {
		t.Fatal(err)
	}
	for _, from := range []string{"older", "other", "a"} {
		if got := r.Target(model.RedirectPeople, from); got != "survivor" {
			t.Errorf("%s -> %q, want survivor", from, got)
		}
	}
}

// TestAddIsIdempotent: a repair pass re-run over the same merges must be a
// no-op, not a refusal.
func TestAddIsIdempotent(t *testing.T) {
	r := model.NewRedirects()
	for i := 0; i < 3; i++ {
		if err := Add(r, model.RedirectSeries, "old", "new"); err != nil {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	if r.Len() != 1 {
		t.Errorf("Len = %d, want 1", r.Len())
	}
}

// TestAddRefusals covers everything Add must decline rather than guess about.
// Each one would otherwise put a table on disk that pkg/check refuses, or that a
// resolver could loop on.
func TestAddRefusals(t *testing.T) {
	cases := []struct {
		name  string
		setup func(model.Redirects)
		kind  model.RedirectKind
		from  string
		to    string
	}{
		{name: "unknown namespace", kind: "recordings", from: "a", to: "b"},
		{name: "self redirect", kind: model.RedirectWorks, from: "a", to: "a"},
		{name: "source is not a slug", kind: model.RedirectWorks, from: "Not A Slug", to: "b"},
		{name: "target is not a slug", kind: model.RedirectWorks, from: "a", to: "b--c"},
		{name: "reserved source", kind: model.RedirectWorks, from: "search", to: "b"},
		{name: "reserved target", kind: model.RedirectWorks, from: "a", to: "latest"},
		{
			name:  "retargeting an existing tombstone",
			setup: func(r model.Redirects) { r[model.RedirectWorks]["a"] = "b" },
			kind:  model.RedirectWorks, from: "a", to: "c",
		},
		{
			// b already redirects to a, so a -> b collapses back onto a: a cycle.
			name:  "target redirects back to the source",
			setup: func(r model.Redirects) { r[model.RedirectWorks]["b"] = "a" },
			kind:  model.RedirectWorks, from: "a", to: "b",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := model.NewRedirects()
			if tc.setup != nil {
				tc.setup(r)
			}
			if err := Add(r, tc.kind, tc.from, tc.to); err == nil {
				t.Fatalf("Add(%s, %s -> %s) was accepted; table is now %v", tc.kind, tc.from, tc.to, r)
			}
		})
	}
	if err := Add(nil, model.RedirectWorks, "a", "b"); err == nil {
		t.Error("Add into a nil table was accepted")
	}
}

// TestLoadRefusesAnUnknownNamespace: a writer must never rewrite a file it did
// not fully understand, and Write would drop the key it could not name.
func TestLoadRefusesAnUnknownNamespace(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "redirects.json"), []byte(`{"recordings":{"a":"b"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Error("Load accepted a file naming a namespace that does not exist")
	}
	if err := Write(dir, model.Redirects{"recordings": {"a": "b"}}); err == nil {
		t.Error("Write accepted a namespace that does not exist")
	}
}

// TestLoadRefusesBrokenJSON keeps a hand-edited file from being silently
// replaced by an empty table.
func TestLoadRefusesBrokenJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "redirects.json"), []byte(`{"works":`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Error("Load accepted a file that is not JSON")
	}
}
