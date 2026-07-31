package pack_test

// The keystone property. pkg/pack's model of "what is wrong with this family"
// has to be a superset of what metacheck rejects, and healing has to converge in
// ONE pass without ever losing an entry. If those three hold together, a
// contributor can put a record approximately anywhere and the tooling makes the
// tree correct; if any one of them slips, metafmt reports a broken tree as clean
// and then rewrites it.
//
// It lives in an external test package so it can call pkg/check, which imports
// pkg/pack.

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/check"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

func person(slug string) string {
	return `{"id":"` + slug + `","name":"Name ` + slug +
		`","license":"CC0-1.0","sources":[{"type":"user"}]}`
}

func work(slug string) string {
	return `{"id":"` + slug + `","title":"Title ` + slug +
		`","authors":["p-aa"],"language":"en","license":"CC0-1.0","sources":[{"type":"user"}]}`
}

func packFile(entries map[string]string) string {
	keys := make([]string, 0, len(entries))
	for k := range entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := `{"entries":{`
	for i, k := range keys {
		if i > 0 {
			out += ","
		}
		out += `"` + k + `":` + entries[k]
	}
	return out + `}}`
}

func write(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// seedTree writes a well-formed multi-pack, multi-directory catalogue: enough
// shape that a corruption has somewhere wrong to be.
func seedTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write(t, dir, "people/0.json", packFile(map[string]string{
		"p-aa": person("p-aa"), "p-bb": person("p-bb"), "p-cc": person("p-cc"),
	}))
	write(t, dir, "people/p-mm.json", packFile(map[string]string{
		"p-mm": person("p-mm"), "p-nn": person("p-nn"),
	}))
	write(t, dir, "people/p-zz.json", packFile(map[string]string{"p-zz": person("p-zz")}))

	write(t, dir, "works/0/0.json", packFile(map[string]string{
		"w-aa": work("w-aa"), "w-bb": work("w-bb"),
	}))
	write(t, dir, "works/0/w-mm.json", packFile(map[string]string{
		"w-mm": work("w-mm"), "w-nn": work("w-nn"),
	}))
	write(t, dir, "works/w-zz/w-zz.json", packFile(map[string]string{"w-zz": work("w-zz")}))

	write(t, dir, "series/0.json", packFile(map[string]string{
		"s-one": `{"id":"s-one","name":"One","works":[{"work":"w-aa","position":"1"},` +
			`{"work":"w-bb","position":"2"}],"license":"CC0-1.0","sources":[{"type":"user"}]}`,
	}))
	canonicalize(t, dir)
	return dir
}

// canonicalize rewrites every pack in canonical form, which is what a tree
// arrives in after metafmt's formatting pass.
func canonicalize(t *testing.T, dir string) {
	t.Helper()
	for _, rel := range allJSON(t, dir) {
		raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		f, err := pack.Parse(raw)
		if err != nil {
			continue
		}
		out, err := f.Bytes()
		if err != nil {
			t.Fatal(err)
		}
		write(t, dir, rel, string(out))
	}
}

// rm deletes a file the corruption is replacing.
func rm(t *testing.T, dir string, parts ...string) {
	t.Helper()
	if err := os.Remove(filepath.Join(append([]string{dir}, parts...)...)); err != nil {
		t.Fatal(err)
	}
}

func allJSON(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(p) != ".json" {
			return nil
		}
		rel, rerr := filepath.Rel(dir, p)
		if rerr != nil {
			return rerr
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}

// entrySet collects every slug in the tree, family-qualified. It is what must
// never change across a heal.
func entrySet(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	for _, rel := range allJSON(t, dir) {
		raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		f, err := pack.Parse(raw)
		if err != nil {
			continue
		}
		fam := filepath.ToSlash(rel)
		fam = fam[:len(fam)-len(filepath.Base(fam))-1]
		if i := len(fam); i > 0 {
			if j := indexByte(fam, '/'); j >= 0 {
				fam = fam[:j]
			}
		}
		for _, s := range f.Slugs() {
			out = append(out, fam+"/"+s)
		}
	}
	sort.Strings(out)
	return out
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

// snapshot reads every file's bytes, so a second pass can be proved a no-op.
func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, rel := range allJSON(t, dir) {
		raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		out[rel] = string(raw)
	}
	return out
}

// healPass runs one Heal + Flush over every family in pack layout, which is
// exactly what metafmt --write does.
func healPass(t *testing.T, dir string) {
	t.Helper()
	s, err := pack.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range pack.Families() {
		if s.Layout(d.Family) != pack.LayoutPack {
			continue
		}
		if _, err := s.Heal(d.Family); err != nil {
			t.Fatalf("heal %s: %v", d.Family, err)
		}
	}
	if _, err := s.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
}

func assertPendingClean(t *testing.T, dir string) {
	t.Helper()
	s, err := pack.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range pack.Families() {
		if s.Layout(d.Family) != pack.LayoutPack {
			continue
		}
		p, err := s.Pending(d.Family)
		if err != nil {
			t.Fatalf("pending %s: %v", d.Family, err)
		}
		if !p.Empty() {
			t.Errorf("%s still pending:\n  %v", d.Family, p.LinesUnder(dir))
		}
	}
}

func assertMetacheckGreen(t *testing.T, dir string) {
	t.Helper()
	res := check.Load(dir)
	if !res.OK() {
		for _, p := range res.Problems {
			t.Errorf("metacheck: %s", p.String())
		}
	}
}

// TestHealConverges is the property: for every way a tree can be structurally
// wrong, ONE Heal + Flush leaves it metacheck-green, Pending-empty, holding
// exactly the entries it started with, and a second pass changes nothing.
func TestHealConverges(t *testing.T) {
	// A sanity check on the fixture itself: a well-formed tree needs no work.
	t.Run("baseline", func(t *testing.T) {
		dir := seedTree(t)
		assertMetacheckGreen(t, dir)
		assertPendingClean(t, dir)
		before := snapshot(t, dir)
		healPass(t, dir)
		if got := snapshot(t, dir); !sameSnapshot(got, before) {
			t.Error("healing a well-formed tree changed it")
		}
	})

	cases := map[string]func(t *testing.T, dir string){
		"entry in the wrong pack": func(t *testing.T, dir string) {
			// p-nn belongs in people/p-mm.json; put it in the first pack.
			write(t, dir, "people/0.json", packFile(map[string]string{
				"p-aa": person("p-aa"), "p-bb": person("p-bb"),
				"p-cc": person("p-cc"), "p-nn": person("p-nn"),
			}))
			write(t, dir, "people/p-mm.json", packFile(map[string]string{"p-mm": person("p-mm")}))
		},
		"pack outside its directory's range": func(t *testing.T, dir string) {
			rm(t, dir, "works", "0", "w-mm.json")
			write(t, dir, "works/w-zz/w-mm.json", packFile(map[string]string{
				"w-mm": work("w-mm"), "w-nn": work("w-nn"),
			}))
		},
		"pack name is not a bound": func(t *testing.T, dir string) {
			rm(t, dir, "people", "p-mm.json")
			write(t, dir, "people/Not A Bound.json", packFile(map[string]string{
				"p-mm": person("p-mm"), "p-nn": person("p-nn"),
			}))
		},
		"empty pack": func(t *testing.T, dir string) {
			write(t, dir, "people/p-ee.json", `{"entries":{}}`)
		},
		"file nested too deep": func(t *testing.T, dir string) {
			rm(t, dir, "works", "0", "w-mm.json")
			write(t, dir, "works/0/extra/note.json", packFile(map[string]string{
				"w-mm": work("w-mm"), "w-nn": work("w-nn"),
			}))
		},
		"subdirectory in a flat family": func(t *testing.T, dir string) {
			rm(t, dir, "people", "p-zz.json")
			write(t, dir, "people/sub/x.json", packFile(map[string]string{"p-zz": person("p-zz")}))
		},
		"lowest pack is not the reserved bound": func(t *testing.T, dir string) {
			rm(t, dir, "people", "0.json")
			write(t, dir, "people/p-aa.json", packFile(map[string]string{
				"p-aa": person("p-aa"), "p-bb": person("p-bb"), "p-cc": person("p-cc"),
			}))
		},
		"first directory is not the reserved bound": func(t *testing.T, dir string) {
			rm(t, dir, "works", "0", "0.json")
			rm(t, dir, "works", "0", "w-mm.json")
			write(t, dir, "works/w-aa/w-aa.json", packFile(map[string]string{
				"w-aa": work("w-aa"), "w-bb": work("w-bb"),
			}))
			write(t, dir, "works/w-aa/w-mm.json", packFile(map[string]string{
				"w-mm": work("w-mm"), "w-nn": work("w-nn"),
			}))
		},
		"duplicate entry in a misfiled copy": func(t *testing.T, dir string) {
			// p-aa is correctly in people/0.json; a stale copy sits elsewhere.
			write(t, dir, "people/p-zz.json", packFile(map[string]string{
				"p-aa": `{"id":"p-aa","name":"Stale","license":"CC0-1.0","sources":[{"type":"user"}]}`,
				"p-zz": person("p-zz"),
			}))
		},
		"everything at once": func(t *testing.T, dir string) {
			rm(t, dir, "people", "0.json")
			rm(t, dir, "people", "p-mm.json")
			write(t, dir, "people/p-aa.json", packFile(map[string]string{
				"p-aa": person("p-aa"), "p-bb": person("p-bb"),
				"p-cc": person("p-cc"), "p-zz": person("p-zz"),
			}))
			write(t, dir, "people/Not A Bound.json", packFile(map[string]string{"p-mm": person("p-mm")}))
			write(t, dir, "people/sub/x.json", packFile(map[string]string{"p-nn": person("p-nn")}))
			write(t, dir, "people/p-ee.json", `{"entries":{}}`)
			rm(t, dir, "works", "0", "w-mm.json")
			write(t, dir, "works/w-zz/w-mm.json", packFile(map[string]string{
				"w-mm": work("w-mm"), "w-nn": work("w-nn"),
			}))
			write(t, dir, "works/0/extra/note.json", packFile(map[string]string{}))
		},
	}

	for name, corrupt := range cases {
		t.Run(name, func(t *testing.T) {
			dir := seedTree(t)
			want := entrySet(t, dir)
			corrupt(t, dir)
			// The corruption is only interesting if it is visible.
			assertCorrupt(t, dir)

			healPass(t, dir)

			assertMetacheckGreen(t, dir)
			assertPendingClean(t, dir)
			if got := entrySet(t, dir); !sameStrings(got, want) {
				t.Fatalf("entry set changed:\n got %v\nwant %v", got, want)
			}
			before := snapshot(t, dir)
			healPass(t, dir)
			if got := snapshot(t, dir); !sameSnapshot(got, before) {
				t.Error("a second pass was not a no-op")
			}
		})
	}
}

// assertCorrupt proves the corruption is reported, so a case can never pass by
// being invisible to everything.
func assertCorrupt(t *testing.T, dir string) {
	t.Helper()
	s, err := pack.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range pack.Families() {
		if s.Layout(d.Family) != pack.LayoutPack {
			continue
		}
		p, perr := s.Pending(d.Family)
		if perr != nil {
			t.Fatal(perr)
		}
		if !p.Empty() {
			return
		}
	}
	t.Fatal("the corrupted tree reports no pending work, so metafmt would call it clean")
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sameSnapshot(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
