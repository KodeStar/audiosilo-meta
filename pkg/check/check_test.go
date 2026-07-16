package check

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// baseValid returns a minimal, fully valid data tree (relpath -> content).
func baseValid() map[string]string {
	return map[string]string{
		"people/au/author-one.json":                 `{"id":"author-one","license":"CC0-1.0","name":"Author One","sources":[{"type":"user"}]}`,
		"people/na/narrator-one.json":               `{"id":"narrator-one","license":"CC0-1.0","name":"Narrator One","sources":[{"type":"user"}]}`,
		"works/bo/book-one/work.json":               `{"authors":["author-one"],"id":"book-one","language":"en","license":"CC0-1.0","sources":[{"type":"user"}],"title":"Book One"}`,
		"works/bo/book-one/recordings/rec-one.json": `{"abridged":false,"id":"rec-one","language":"en","license":"CC0-1.0","narrators":["narrator-one"],"sources":[{"type":"user"}],"work":"book-one"}`,
		"series/se/series-one.json":                 `{"id":"series-one","license":"CC0-1.0","name":"Series One","sources":[{"type":"user"}],"works":[{"position":"1","work":"book-one"}]}`,
	}
}

// writeTree materializes files into dir.
func writeTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLoadValid(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, baseValid())
	res := Load(dir)
	if !res.OK() {
		t.Fatalf("valid tree reported problems: %v", res.Problems)
	}
	if len(res.Catalog.Works) != 1 || len(res.Catalog.People) != 2 || len(res.Catalog.Series) != 1 {
		t.Errorf("unexpected catalog counts: %+v", res.Catalog)
	}
	if len(res.Catalog.Works[0].Recordings) != 1 {
		t.Errorf("recording not attached to work")
	}
}

// TestOmnibusSeriesPosition covers the schema change allowing a range position
// (e.g. "1-3.5") for an omnibus edition, while still forbidding duplicates.
func TestOmnibusSeriesPosition(t *testing.T) {
	dir := t.TempDir()
	files := baseValid()
	files["works/bo/book-two/work.json"] = `{"authors":["author-one"],"id":"book-two","language":"en","license":"CC0-1.0","sources":[{"type":"user"}],"title":"Book Two"}`
	files["series/se/series-one.json"] = `{"id":"series-one","license":"CC0-1.0","name":"Series One","sources":[{"type":"user"}],"works":[{"position":"1","work":"book-one"},{"position":"1-3.5","work":"book-two"}]}`
	writeTree(t, dir, files)
	res := Load(dir)
	if !res.OK() {
		t.Fatalf("omnibus range position should validate, got: %v", res.Problems)
	}
}

// TestRecordingAbridgedOptional covers the schema change making abridged
// optional: a recording that omits it must still validate.
func TestRecordingAbridgedOptional(t *testing.T) {
	dir := t.TempDir()
	files := baseValid()
	files["works/bo/book-one/recordings/rec-one.json"] = `{"id":"rec-one","language":"en","license":"CC0-1.0","narrators":["narrator-one"],"sources":[{"type":"user"}],"work":"book-one"}`
	writeTree(t, dir, files)
	res := Load(dir)
	if !res.OK() {
		t.Fatalf("recording without abridged should validate, got: %v", res.Problems)
	}
}

// validCharacters / validRecaps are minimal, valid per-work sidecars for the
// given work, in canonical (sorted-key) form.
func validCharacters(work string) string {
	return `{"characters":[{"aliases":["The Kid"],"description":"A brave hero.","id":"hero","name":"Hero","reveal":{"chapter":1},"role":"protagonist","xref":{"wikidata":"Q42"}}],"license":"CC-BY-SA-3.0","sources":[{"type":"community"}],"work":"` + work + `"}`
}

func validRecaps(work string) string {
	return `{"license":"CC-BY-SA-3.0","recaps":[{"scope":"series","text":"Previously, in earlier books.","through":{"chapter":0}},{"scope":"book","text":"So far, the hero set out.","through":{"chapter":3}}],"sources":[{"type":"community"}],"work":"` + work + `"}`
}

// TestCharactersRecapsValid covers the CC BY-SA per-work sidecars: a valid
// characters.json and recaps.json load cleanly and land in the Catalog.
func TestCharactersRecapsValid(t *testing.T) {
	dir := t.TempDir()
	files := baseValid()
	files["works/bo/book-one/characters.json"] = validCharacters("book-one")
	files["works/bo/book-one/recaps.json"] = validRecaps("book-one")
	writeTree(t, dir, files)
	res := Load(dir)
	if !res.OK() {
		t.Fatalf("valid characters/recaps reported problems: %v", res.Problems)
	}
	if len(res.Catalog.Characters) != 1 || len(res.Catalog.Recaps) != 1 {
		t.Errorf("unexpected sidecar counts: characters=%d recaps=%d", len(res.Catalog.Characters), len(res.Catalog.Recaps))
	}
}

// TestRecapsSummaryFields covers the optional whole-book summary fields
// (in_short / ending) and the raised per-entry text cap (2000 -> 3000): a
// recaps sidecar carrying all three still validates.
func TestRecapsSummaryFields(t *testing.T) {
	dir := t.TempDir()
	files := baseValid()
	longText := strings.Repeat("word ", 500) // 2500 chars, over the old 2000 cap
	files["works/bo/book-one/recaps.json"] = `{"ending":"The hero wins and goes home.","in_short":"A hero sets out, struggles, and prevails.","license":"CC-BY-SA-3.0","recaps":[{"scope":"book","text":"` + longText + `","through":{"chapter":3}}],"sources":[{"type":"community"}],"work":"book-one"}`
	writeTree(t, dir, files)
	res := Load(dir)
	if !res.OK() {
		t.Fatalf("recaps with in_short/ending and a 2500-char text should validate, got: %v", res.Problems)
	}
	rc := res.Catalog.Recaps
	if len(rc) != 1 || rc[0].InShort == "" || rc[0].Ending == "" {
		t.Errorf("summary fields did not load: %+v", rc)
	}
}

func TestLoadRuleViolations(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(map[string]string)
		want   string // substring expected in some problem message
	}{
		{
			name:   "unrecognized location",
			mutate: func(f map[string]string) { f["works/bo/book-one/notes.txt.json"] = `{}` },
			want:   "unrecognized location",
		},
		{
			name: "id does not match slug",
			mutate: func(f map[string]string) {
				f["people/au/author-one.json"] = strings.Replace(f["people/au/author-one.json"], `"id":"author-one"`, `"id":"someone-else"`, 1)
			},
			want: "does not match its file/dir slug",
		},
		{
			name: "wrong person shard",
			mutate: func(f map[string]string) {
				f["people/xx/author-one.json"] = f["people/au/author-one.json"]
				delete(f, "people/au/author-one.json")
			},
			want: "shard dir",
		},
		{
			name: "wrong recording shard uses work slug",
			mutate: func(f map[string]string) {
				f["works/xx/book-one/recordings/rec-one.json"] = f["works/bo/book-one/recordings/rec-one.json"]
				f["works/xx/book-one/work.json"] = f["works/bo/book-one/work.json"]
				delete(f, "works/bo/book-one/recordings/rec-one.json")
				delete(f, "works/bo/book-one/work.json")
			},
			want: "shard dir",
		},
		{
			name: "recording work mismatches parent dir",
			mutate: func(f map[string]string) {
				f["works/bo/book-one/recordings/rec-one.json"] = strings.Replace(f["works/bo/book-one/recordings/rec-one.json"], `"work":"book-one"`, `"work":"other-book"`, 1)
			},
			want: "must equal the parent work dir id",
		},
		{
			name:   "missing author",
			mutate: func(f map[string]string) { delete(f, "people/au/author-one.json") },
			want:   `author "author-one" does not exist`,
		},
		{
			name:   "missing narrator",
			mutate: func(f map[string]string) { delete(f, "people/na/narrator-one.json") },
			want:   `narrator "narrator-one" does not exist`,
		},
		{
			name: "missing series work",
			mutate: func(f map[string]string) {
				f["series/se/series-one.json"] = strings.Replace(f["series/se/series-one.json"], `"work":"book-one"`, `"work":"ghost-book"`, 1)
			},
			want: `series work "ghost-book" does not exist`,
		},
		{
			name:   "orphan recording (no parent work)",
			mutate: func(f map[string]string) { delete(f, "works/bo/book-one/work.json") },
			want:   `parent work "book-one" does not exist`,
		},
		{
			name: "duplicate region+asin",
			mutate: func(f map[string]string) {
				f["works/bo/book-one/recordings/rec-one.json"] = `{"abridged":false,"asin":[{"asin":"B000000001","region":"us"}],"id":"rec-one","language":"en","license":"CC0-1.0","narrators":["narrator-one"],"sources":[{"type":"user"}],"work":"book-one"}`
				f["works/bo/book-one/recordings/rec-two.json"] = `{"abridged":false,"asin":[{"asin":"B000000001","region":"us"}],"id":"rec-two","language":"en","license":"CC0-1.0","narrators":["narrator-one"],"sources":[{"type":"user"}],"work":"book-one"}`
			},
			want: "duplicate ASIN B000000001",
		},
		{
			name: "duplicate ISBN",
			mutate: func(f map[string]string) {
				f["works/bo/book-one/recordings/rec-one.json"] = `{"abridged":false,"id":"rec-one","isbn":["9780000000001"],"language":"en","license":"CC0-1.0","narrators":["narrator-one"],"sources":[{"type":"user"}],"work":"book-one"}`
				f["works/bo/book-one/recordings/rec-two.json"] = `{"abridged":false,"id":"rec-two","isbn":["9780000000001"],"language":"en","license":"CC0-1.0","narrators":["narrator-one"],"sources":[{"type":"user"}],"work":"book-one"}`
			},
			want: "duplicate ISBN 9780000000001",
		},
		{
			name: "duplicate person wikidata",
			mutate: func(f map[string]string) {
				f["people/au/author-one.json"] = `{"id":"author-one","license":"CC0-1.0","name":"Author One","sources":[{"type":"user"}],"xref":{"wikidata":"Q123"}}`
				f["people/na/narrator-one.json"] = `{"id":"narrator-one","license":"CC0-1.0","name":"Narrator One","sources":[{"type":"user"}],"xref":{"wikidata":"Q123"}}`
			},
			want: "duplicate person xref.wikidata Q123",
		},
		{
			name: "chapters do not start at 0",
			mutate: func(f map[string]string) {
				f["works/bo/book-one/recordings/rec-one.json"] = withChapters(`[{"title":"One","start_ms":500,"length_ms":1000}]`)
			},
			want: "first chapter must start at 0",
		},
		{
			name: "chapters not strictly increasing",
			mutate: func(f map[string]string) {
				f["works/bo/book-one/recordings/rec-one.json"] = withChapters(`[{"title":"One","start_ms":0,"length_ms":1000},{"title":"Two","start_ms":0,"length_ms":1000}]`)
			},
			want: "is not greater than previous",
		},
		{
			name: "duplicate series position",
			mutate: func(f map[string]string) {
				f["series/se/series-one.json"] = `{"id":"series-one","license":"CC0-1.0","name":"Series One","sources":[{"type":"user"}],"works":[{"position":"1","work":"book-one"},{"position":"1","work":"book-two"}]}`
			},
			want: `duplicate series position "1"`,
		},
		{
			name: "schema violation: missing required title",
			mutate: func(f map[string]string) {
				f["works/bo/book-one/work.json"] = `{"authors":["author-one"],"id":"book-one","language":"en","license":"CC0-1.0","sources":[{"type":"user"}]}`
			},
			want: "title",
		},
		{
			name: "schema violation: bad license",
			mutate: func(f map[string]string) {
				f["people/au/author-one.json"] = strings.Replace(f["people/au/author-one.json"], `"CC0-1.0"`, `"MIT"`, 1)
			},
			want: "license",
		},
		{
			name: "schema violation: additionalProperties",
			mutate: func(f map[string]string) {
				f["people/au/author-one.json"] = strings.Replace(f["people/au/author-one.json"], `"name":"Author One"`, `"name":"Author One","surprise":true`, 1)
			},
			want: "additional",
		},
		{
			name: "invalid slug in id/dir",
			mutate: func(f map[string]string) {
				f["people/Au/Author_One.json"] = `{"id":"Author_One","license":"CC0-1.0","name":"X","sources":[{"type":"user"}]}`
			},
			want: "not a valid slug",
		},
		{
			name: "characters with CC0 license rejected (must be CC BY-SA)",
			mutate: func(f map[string]string) {
				f["works/bo/book-one/characters.json"] = strings.Replace(validCharacters("book-one"), `"CC-BY-SA-3.0"`, `"CC0-1.0"`, 1)
			},
			want: "license",
		},
		{
			name: "duplicate character id within file",
			mutate: func(f map[string]string) {
				f["works/bo/book-one/characters.json"] = `{"characters":[{"id":"hero","name":"Hero","reveal":{"chapter":1}},{"id":"hero","name":"Hero Twin","reveal":{"chapter":2}}],"license":"CC-BY-SA-3.0","sources":[{"type":"community"}],"work":"book-one"}`
			},
			want: `duplicate character id "hero"`,
		},
		{
			name: "character description exceeds length cap",
			mutate: func(f map[string]string) {
				long := strings.Repeat("a", 1501)
				f["works/bo/book-one/characters.json"] = `{"characters":[{"description":"` + long + `","id":"hero","name":"Hero","reveal":{"chapter":1}}],"license":"CC-BY-SA-3.0","sources":[{"type":"community"}],"work":"book-one"}`
			},
			want: "/characters/0/description",
		},
		{
			name: "characters parent work missing",
			mutate: func(f map[string]string) {
				f["works/gh/ghost-book/characters.json"] = validCharacters("ghost-book")
			},
			want: `parent work "ghost-book" does not exist`,
		},
		{
			name: "characters work backref mismatches dir",
			mutate: func(f map[string]string) {
				f["works/bo/book-one/characters.json"] = validCharacters("other-book")
			},
			want: "must equal the parent work dir id",
		},
		{
			name: "characters wrong shard",
			mutate: func(f map[string]string) {
				f["works/xx/book-one/characters.json"] = validCharacters("book-one")
			},
			want: "shard dir",
		},
		{
			name: "duplicate recap through-position",
			mutate: func(f map[string]string) {
				f["works/bo/book-one/recaps.json"] = `{"license":"CC-BY-SA-3.0","recaps":[{"text":"A.","through":{"chapter":3}},{"text":"B.","through":{"chapter":3}}],"sources":[{"type":"community"}],"work":"book-one"}`
			},
			want: "duplicate recap through chapter 3",
		},
		{
			name: "recap bad scope enum",
			mutate: func(f map[string]string) {
				f["works/bo/book-one/recaps.json"] = strings.Replace(validRecaps("book-one"), `"scope":"book"`, `"scope":"midway"`, 1)
			},
			want: "scope",
		},
		{
			name: "recap negative chapter rejected",
			mutate: func(f map[string]string) {
				f["works/bo/book-one/recaps.json"] = `{"license":"CC-BY-SA-3.0","recaps":[{"text":"A.","through":{"chapter":-1}}],"sources":[{"type":"community"}],"work":"book-one"}`
			},
			want: "chapter",
		},
		{
			name: "recap text exceeds raised length cap",
			mutate: func(f map[string]string) {
				long := strings.Repeat("a", 3001)
				f["works/bo/book-one/recaps.json"] = `{"license":"CC-BY-SA-3.0","recaps":[{"text":"` + long + `","through":{"chapter":3}}],"sources":[{"type":"community"}],"work":"book-one"}`
			},
			want: "/recaps/0/text",
		},
		{
			name: "in_short exceeds length cap",
			mutate: func(f map[string]string) {
				long := strings.Repeat("a", 1501)
				f["works/bo/book-one/recaps.json"] = `{"in_short":"` + long + `","license":"CC-BY-SA-3.0","recaps":[{"text":"A.","through":{"chapter":3}}],"sources":[{"type":"community"}],"work":"book-one"}`
			},
			want: "/in_short",
		},
		{
			name: "in_short empty string rejected",
			mutate: func(f map[string]string) {
				f["works/bo/book-one/recaps.json"] = `{"in_short":"","license":"CC-BY-SA-3.0","recaps":[{"text":"A.","through":{"chapter":3}}],"sources":[{"type":"community"}],"work":"book-one"}`
			},
			want: "/in_short",
		},
		{
			name: "ending exceeds length cap",
			mutate: func(f map[string]string) {
				long := strings.Repeat("a", 2001)
				f["works/bo/book-one/recaps.json"] = `{"ending":"` + long + `","license":"CC-BY-SA-3.0","recaps":[{"text":"A.","through":{"chapter":3}}],"sources":[{"type":"community"}],"work":"book-one"}`
			},
			want: "/ending",
		},
		{
			name: "ending wrong type rejected",
			mutate: func(f map[string]string) {
				f["works/bo/book-one/recaps.json"] = `{"ending":42,"license":"CC-BY-SA-3.0","recaps":[{"text":"A.","through":{"chapter":3}}],"sources":[{"type":"community"}],"work":"book-one"}`
			},
			want: "/ending",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			files := baseValid()
			c.mutate(files)
			writeTree(t, dir, files)
			res := Load(dir)
			if res.OK() {
				t.Fatalf("expected a problem containing %q, got none", c.want)
			}
			found := false
			for _, p := range res.Problems {
				if strings.Contains(p.Msg, c.want) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("no problem contained %q; problems:\n%s", c.want, joinProblems(res.Problems))
			}
		})
	}
}

func withChapters(chaptersJSON string) string {
	return `{"abridged":false,"chapters":` + chaptersJSON + `,"id":"rec-one","language":"en","license":"CC0-1.0","narrators":["narrator-one"],"sources":[{"type":"user"}],"work":"book-one"}`
}

func joinProblems(ps []Problem) string {
	var b strings.Builder
	for _, p := range ps {
		b.WriteString("  " + p.String() + "\n")
	}
	return b.String()
}

// TestRealDataTree guards the committed seed data: it must validate and be
// canonically formatted so it can never silently drift.
func TestRealDataTree(t *testing.T) {
	const dataDir = "../../data"
	if _, err := os.Stat(dataDir); err != nil {
		t.Skipf("no data tree at %s: %v", dataDir, err)
	}
	res := Load(dataDir)
	if !res.OK() {
		t.Fatalf("real data/ tree has validation problems:\n%s", joinProblems(res.Problems))
	}
}
