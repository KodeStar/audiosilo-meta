package check

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// The rule suite in this file is written in PER-ENTITY addresses
// ("works/bo/book-one/recordings/rec-one.json"), the shape the tree had before
// the pack migration. That is a fixture syntax and nothing more: writeEntities
// resolves each address onto the pack entry that holds the record now, so a
// case reads as one record at a time while the tree it validates is the real
// layout. Storage rules (placement, bounds, caps) are packcheck_test.go's; this
// file is about what the records SAY and how they relate.

// baseValid returns a minimal, fully valid catalogue (address -> content).
func baseValid() map[string]string {
	return map[string]string{
		"people/au/author-one.json":                 `{"id":"author-one","license":"CC0-1.0","name":"Author One","sources":[{"type":"user"}]}`,
		"people/na/narrator-one.json":               `{"id":"narrator-one","license":"CC0-1.0","name":"Narrator One","sources":[{"type":"user"}]}`,
		"works/bo/book-one/work.json":               `{"authors":["author-one"],"id":"book-one","language":"en","license":"CC0-1.0","sources":[{"type":"user"}],"title":"Book One"}`,
		"works/bo/book-one/recordings/rec-one.json": `{"abridged":false,"id":"rec-one","language":"en","license":"CC0-1.0","narrators":["narrator-one"],"sources":[{"type":"user"}],"work":"book-one"}`,
		"series/se/series-one.json":                 `{"id":"series-one","license":"CC0-1.0","name":"Series One","sources":[{"type":"user"}],"works":[{"position":"1","work":"book-one"}]}`,
	}
}

// writeEntities materializes a per-entity fixture map as a pack tree: one works
// pack holding each work with its recordings nested, one works-community pack
// pairing each work's sidecars, and a flat pack for people and for series. An
// address it does not recognize is written VERBATIM at that path, which is how a
// case puts a stray file in the tree.
func writeEntities(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	works := map[string]string{}
	recs := map[string]map[string]string{}
	community := map[string]map[string]string{}
	people := map[string]string{}
	series := map[string]string{}
	raw := map[string]string{}

	for _, address := range sortedAddresses(files) {
		body := files[address]
		parts := strings.Split(address, "/")
		switch {
		case len(parts) == 4 && parts[0] == "works" && parts[3] == "work.json":
			works[parts[2]] = body
		case len(parts) == 4 && parts[0] == "works" && parts[3] == "characters.json":
			member(community, parts[2])["characters"] = body
		case len(parts) == 4 && parts[0] == "works" && parts[3] == "recaps.json":
			member(community, parts[2])["recaps"] = body
		case len(parts) == 5 && parts[0] == "works" && parts[3] == "recordings":
			member(recs, parts[2])[strings.TrimSuffix(parts[4], ".json")] = body
		case len(parts) == 3 && parts[0] == "people":
			people[strings.TrimSuffix(parts[2], ".json")] = body
		case len(parts) == 3 && parts[0] == "series":
			series[strings.TrimSuffix(parts[2], ".json")] = body
		default:
			raw[address] = body
		}
	}

	out := map[string]string{}
	if len(works) > 0 || len(recs) > 0 {
		entries := map[string]string{}
		for slug, work := range works {
			entries[slug] = composite(work, recs[slug])
		}
		for slug := range recs {
			if _, ok := works[slug]; !ok {
				t.Fatalf("check_test: recordings for %q with no work record: a pack composite is the work", slug)
			}
		}
		out["works/0/0.json"] = packOf(entries)
	}
	if len(community) > 0 {
		entries := map[string]string{}
		for slug, members := range community {
			entries[slug] = jsonObject(members)
		}
		out["works-community/0/0.json"] = packOf(entries)
	}
	if len(people) > 0 {
		out["people/0.json"] = packOf(people)
	}
	if len(series) > 0 {
		out["series/0.json"] = packOf(series)
	}
	for rel, body := range raw {
		out[rel] = body
	}
	writeTree(t, dir, out)
}

// member returns m[key], creating it on first use.
func member(m map[string]map[string]string, key string) map[string]string {
	if m[key] == nil {
		m[key] = map[string]string{}
	}
	return m[key]
}

// sortedAddresses returns the fixture's addresses in a stable order, so a fixture
// always composes the same tree.
func sortedAddresses(files map[string]string) []string {
	out := make([]string, 0, len(files))
	for k := range files {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
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
	writeEntities(t, dir, baseValid())
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
	writeEntities(t, dir, files)
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
	writeEntities(t, dir, files)
	res := Load(dir)
	if !res.OK() {
		t.Fatalf("recording without abridged should validate, got: %v", res.Problems)
	}
}

// TestWorkGenresValid covers the optional normalized genres field: a work
// carrying sorted vocabulary values validates and the values reach the catalog.
func TestWorkGenresValid(t *testing.T) {
	dir := t.TempDir()
	files := baseValid()
	files["works/bo/book-one/work.json"] = `{"authors":["author-one"],"genres":["epic-fantasy","fantasy","science-fiction"],"id":"book-one","language":"en","license":"CC0-1.0","sources":[{"type":"user"}],"title":"Book One"}`
	writeEntities(t, dir, files)
	res := Load(dir)
	if !res.OK() {
		t.Fatalf("work with sorted genres should validate, got: %v", res.Problems)
	}
	got := res.Catalog.Works[0].Genres
	if len(got) != 3 || got[0] != "epic-fantasy" || got[2] != "science-fiction" {
		t.Errorf("genres did not load: %v", got)
	}
}

// TestLibexImportSourceType covers the source-type enum addition the libex
// importer produces: a record stamped "libex-import" validates, and the stamp
// survives into the catalog (so a whole source stays auditable and retractable).
func TestLibexImportSourceType(t *testing.T) {
	dir := t.TempDir()
	files := baseValid()
	files["works/bo/book-one/work.json"] = `{"authors":["author-one"],"genres":["epic-fantasy"],"id":"book-one","language":"en","license":"CC0-1.0","sources":[{"imported_at":"2026-07-29","ref":"B0LIBEX001","type":"libex-import"}],"title":"Book One"}`
	writeEntities(t, dir, files)
	res := Load(dir)
	if !res.OK() {
		t.Fatalf("a libex-import source should validate, got: %v", res.Problems)
	}
	srcs := res.Catalog.Works[0].Sources
	if len(srcs) != 1 || srcs[0].Type != "libex-import" || srcs[0].Ref != "B0LIBEX001" {
		t.Errorf("libex source did not load: %+v", srcs)
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
	writeEntities(t, dir, files)
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
	writeEntities(t, dir, files)
	res := Load(dir)
	if !res.OK() {
		t.Fatalf("recaps with in_short/ending and a 2500-char text should validate, got: %v", res.Problems)
	}
	rc := res.Catalog.Recaps
	if len(rc) != 1 || rc[0].InShort == "" || rc[0].Ending == "" {
		t.Errorf("summary fields did not load: %+v", rc)
	}
}

// TestCreditListsDistinctSlugs is the passing fixture for the credit-list
// uniqueItems rule (its violating fixtures live in TestLoadRuleViolations): a
// work and a recording crediting two DIFFERENT people still validate, so the
// rule only ever rejects a repeated slug.
func TestCreditListsDistinctSlugs(t *testing.T) {
	dir := t.TempDir()
	files := baseValid()
	files["people/au/author-two.json"] = `{"id":"author-two","license":"CC0-1.0","name":"Author Two","sources":[{"type":"user"}]}`
	files["people/na/narrator-two.json"] = `{"id":"narrator-two","license":"CC0-1.0","name":"Narrator Two","sources":[{"type":"user"}]}`
	files["works/bo/book-one/work.json"] = `{"authors":["author-one","author-two"],"id":"book-one","language":"en","license":"CC0-1.0","sources":[{"type":"user"}],"title":"Book One"}`
	files["works/bo/book-one/recordings/rec-one.json"] = `{"id":"rec-one","language":"en","license":"CC0-1.0","narrators":["narrator-one","narrator-two"],"sources":[{"type":"user"}],"work":"book-one"}`
	files["series/se/series-one.json"] = `{"authors":["author-one","author-two"],"id":"series-one","license":"CC0-1.0","name":"Series One","sources":[{"type":"user"}],"works":[{"position":"1","work":"book-one"}]}`
	writeEntities(t, dir, files)
	res := Load(dir)
	if !res.OK() {
		t.Fatalf("distinct credit slugs should validate, got: %v", res.Problems)
	}
}

// TestWorkCreditsValid is the passing fixture for the optional contributor
// credits: a work crediting existing people validates, ONE person may hold two
// different roles (the combined "editor and translator" qualifier the importer
// splits into two entries), and the values reach the catalog.
func TestWorkCreditsValid(t *testing.T) {
	dir := t.TempDir()
	files := baseValid()
	files["people/au/author-two.json"] = `{"id":"author-two","license":"CC0-1.0","name":"Author Two","sources":[{"type":"user"}]}`
	files["works/bo/book-one/work.json"] = `{"authors":["author-one"],"credits":[{"person":"author-two","role":"editor"},{"person":"author-two","role":"translator"}],"id":"book-one","language":"en","license":"CC0-1.0","sources":[{"type":"user"}],"title":"Book One"}`
	writeEntities(t, dir, files)
	res := Load(dir)
	if !res.OK() {
		t.Fatalf("a work with valid credits should validate, got: %v", res.Problems)
	}
	got := res.Catalog.Works[0].Credits
	if len(got) != 2 || got[0].Person != "author-two" || got[0].Role != "editor" || got[1].Role != "translator" {
		t.Errorf("credits did not load: %+v", got)
	}
}

// TestCreditPairReportedOnce pins that a repeated (person, role) credit is
// reported ONCE per work however many copies it has: the fix is one edit either
// way, so five copies must not push four identical lines into a report a
// contributor has to read.
func TestCreditPairReportedOnce(t *testing.T) {
	dir := t.TempDir()
	files := baseValid()
	dupe := strings.Repeat(`{"person":"author-one","role":"editor"},`, 5)
	files["works/bo/book-one/work.json"] = `{"authors":["author-one"],"credits":[` + strings.TrimSuffix(dupe, ",") +
		`],"id":"book-one","language":"en","license":"CC0-1.0","sources":[{"type":"user"}],"title":"Book One"}`
	writeEntities(t, dir, files)
	res := Load(dir)
	if res.OK() {
		t.Fatal("a work crediting one person five times in one role must fail")
	}
	n := 0
	for _, p := range res.Problems {
		if strings.Contains(p.Msg, `credit "author-one" is listed twice as "editor"`) {
			n++
		}
	}
	if n != 1 {
		t.Errorf("checkCreditPairs reported the same pair %d times, want 1; problems: %v", n, res.Problems)
	}
}

// TestPersonKindValid is the passing fixture for the optional person kind: the
// field is accepted, absence stays the norm, and the value reaches the catalog.
func TestPersonKindValid(t *testing.T) {
	dir := t.TempDir()
	files := baseValid()
	files["people/au/author-one.json"] = `{"id":"author-one","kind":"publisher","license":"CC0-1.0","name":"Author One","sources":[{"type":"user"}]}`
	writeEntities(t, dir, files)
	res := Load(dir)
	if !res.OK() {
		t.Fatalf("a person with a kind should validate, got: %v", res.Problems)
	}
	var kinds []string
	for _, p := range res.Catalog.People {
		kinds = append(kinds, p.ID+"="+p.Kind)
	}
	sort.Strings(kinds)
	// narrator-one carries no kind, which reads as an individual person.
	if len(kinds) != 2 || kinds[0] != "author-one=publisher" || kinds[1] != "narrator-one=" {
		t.Errorf("person kinds did not load: %v", kinds)
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
			name: "id does not match its entry key",
			mutate: func(f map[string]string) {
				f["people/au/author-one.json"] = strings.Replace(f["people/au/author-one.json"], `"id":"author-one"`, `"id":"someone-else"`, 1)
			},
			want: "does not match its entry key",
		},
		{
			// The composite is the only way a recording can lose its work: the
			// entry key is what its recordings hang off, so a work whose own id
			// disagrees with that key leaves them pointing at nothing.
			name: "recording orphaned by a work id that is not its entry key",
			mutate: func(f map[string]string) {
				f["works/bo/book-one/work.json"] = strings.Replace(
					f["works/bo/book-one/work.json"], `"id":"book-one"`, `"id":"other-book"`, 1)
			},
			want: `parent work "book-one" does not exist`,
		},
		{
			name: "recording work mismatches its parent entry",
			mutate: func(f map[string]string) {
				f["works/bo/book-one/recordings/rec-one.json"] = strings.Replace(f["works/bo/book-one/recordings/rec-one.json"], `"work":"book-one"`, `"work":"other-book"`, 1)
			},
			want: "must equal the parent entry key",
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
			name: "unknown genre value",
			mutate: func(f map[string]string) {
				f["works/bo/book-one/work.json"] = `{"authors":["author-one"],"genres":["fantasy","sci-fi-ish"],"id":"book-one","language":"en","license":"CC0-1.0","sources":[{"type":"user"}],"title":"Book One"}`
			},
			want: "/genres/1",
		},
		{
			name: "duplicate genre value",
			mutate: func(f map[string]string) {
				f["works/bo/book-one/work.json"] = `{"authors":["author-one"],"genres":["fantasy","fantasy"],"id":"book-one","language":"en","license":"CC0-1.0","sources":[{"type":"user"}],"title":"Book One"}`
			},
			want: "/genres",
		},
		{
			name: "genres not sorted",
			mutate: func(f map[string]string) {
				f["works/bo/book-one/work.json"] = `{"authors":["author-one"],"genres":["science-fiction","fantasy"],"id":"book-one","language":"en","license":"CC0-1.0","sources":[{"type":"user"}],"title":"Book One"}`
			},
			want: `genres must be sorted: "fantasy" comes after "science-fiction"`,
		},
		{
			name: "credit names a person who does not exist",
			mutate: func(f map[string]string) {
				f["works/bo/book-one/work.json"] = `{"authors":["author-one"],"credits":[{"person":"ghost-editor","role":"editor"}],"id":"book-one","language":"en","license":"CC0-1.0","sources":[{"type":"user"}],"title":"Book One"}`
			},
			want: `credit "ghost-editor" (editor) does not exist as a person`,
		},
		{
			name: "one person credited twice in the same role",
			mutate: func(f map[string]string) {
				f["works/bo/book-one/work.json"] = `{"authors":["author-one"],"credits":[{"person":"author-one","role":"editor"},{"person":"author-one","role":"editor"}],"id":"book-one","language":"en","license":"CC0-1.0","sources":[{"type":"user"}],"title":"Book One"}`
			},
			want: `credit "author-one" is listed twice as "editor"`,
		},
		{
			name: "unknown credit role value",
			mutate: func(f map[string]string) {
				f["works/bo/book-one/work.json"] = `{"authors":["author-one"],"credits":[{"person":"author-one","role":"narrator"}],"id":"book-one","language":"en","license":"CC0-1.0","sources":[{"type":"user"}],"title":"Book One"}`
			},
			want: "/credits/0/role",
		},
		{
			name: "credit with an extra property",
			mutate: func(f map[string]string) {
				f["works/bo/book-one/work.json"] = `{"authors":["author-one"],"credits":[{"note":"why","person":"author-one","role":"editor"}],"id":"book-one","language":"en","license":"CC0-1.0","sources":[{"type":"user"}],"title":"Book One"}`
			},
			want: "/credits/0",
		},
		{
			name: "empty credits list",
			mutate: func(f map[string]string) {
				f["works/bo/book-one/work.json"] = `{"authors":["author-one"],"credits":[],"id":"book-one","language":"en","license":"CC0-1.0","sources":[{"type":"user"}],"title":"Book One"}`
			},
			want: "/credits",
		},
		{
			name: "unknown person kind value",
			mutate: func(f map[string]string) {
				f["people/au/author-one.json"] = `{"id":"author-one","kind":"corporation","license":"CC0-1.0","name":"Author One","sources":[{"type":"user"}]}`
			},
			want: "/kind",
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
			name: "characters work backref mismatches its entry key",
			mutate: func(f map[string]string) {
				f["works/bo/book-one/characters.json"] = validCharacters("other-book")
			},
			want: "must equal the entry key",
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
		// A credit list may not repeat a person slug: the slug IS the identity,
		// so a doubled entry is a composition bug, never two credits. The
		// schema's uniqueItems is the mechanical backstop behind the
		// dedupe-by-slug in importer.creditSlugs / issueform.slugsFor.
		{
			name: "work authors repeat a slug",
			mutate: func(f map[string]string) {
				f["works/bo/book-one/work.json"] = `{"authors":["author-one","author-one"],"id":"book-one","language":"en","license":"CC0-1.0","sources":[{"type":"user"}],"title":"Book One"}`
			},
			want: "/authors: items at 0 and 1 are equal",
		},
		{
			name: "recording narrators repeat a slug",
			mutate: func(f map[string]string) {
				f["works/bo/book-one/recordings/rec-one.json"] = `{"id":"rec-one","language":"en","license":"CC0-1.0","narrators":["narrator-one","narrator-one"],"sources":[{"type":"user"}],"work":"book-one"}`
			},
			want: "/narrators: items at 0 and 1 are equal",
		},
		{
			name: "series authors repeat a slug",
			mutate: func(f map[string]string) {
				f["series/se/series-one.json"] = `{"authors":["author-one","author-one"],"id":"series-one","license":"CC0-1.0","name":"Series One","sources":[{"type":"user"}],"works":[{"position":"1","work":"book-one"}]}`
			},
			want: "/authors: items at 0 and 1 are equal",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			files := baseValid()
			c.mutate(files)
			writeEntities(t, dir, files)
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

// TestRealDataTree guards the committed data: it must validate so it can never
// silently drift.
//
// A tree that is not in the pack layout at all is the ONE thing it does not
// report, because that is not drift: it is a checkout from before the storage
// migration, where every one of the thousands of problems says the same thing.
// metacheck says it there, once per family, and says how to fix it.
func TestRealDataTree(t *testing.T) {
	const dataDir = "../../data"
	if _, err := os.Stat(dataDir); err != nil {
		t.Skipf("no data tree at %s: %v", dataDir, err)
	}
	layouts, err := pack.Detect(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, def := range pack.Families() {
		if layouts[def.Family] == pack.LayoutLegacy {
			t.Skipf("%s/%s predates the pack migration; convert it with `go run ./cmd/metamigrate`",
				dataDir, def.Family.Root())
		}
	}
	res := Load(dataDir)
	if !res.OK() {
		t.Fatalf("real data/ tree has validation problems:\n%s", joinProblems(res.Problems))
	}
}
