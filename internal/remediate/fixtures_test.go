package remediate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/testpack"
)

// fixtures_test.go builds the small trees the rules are exercised against.
// Addresses use internal/testpack's per-entity reference syntax; the records
// land in their family's first pack, which is where a writer would put them at
// this scale.

const testToday = "2026-08-06"

func workAddr(slug string) string     { return "works/xx/" + slug + "/work.json" }
func recAddr(slug, rec string) string { return "works/xx/" + slug + "/recordings/" + rec + ".json" }
func seriesAddr(slug string) string   { return "series/xx/" + slug + ".json" }
func personAddr(slug string) string   { return "people/xx/" + slug + ".json" }

// recSpec is a recording as a test states it. A zero field is a field the
// record does not carry, which is the whole point: the merge's job is to keep
// "unstated" and "stated" apart.
type recSpec struct {
	Key       string
	Narrators []string
	// ASINs are "region:value" pairs.
	ASINs     []string
	Runtime   int
	Release   string
	Publisher string
	Cover     string
	Chapters  bool
	Abridged  *bool
	AddedAt   string
}

// workSpec is a work entry as a test states it.
type workSpec struct {
	Slug    string
	Title   string
	Authors []string
	Genres  []string
	AddedAt string
	Rec     recSpec
	// Extra is a SECOND recording on the same work, the shape 14 live works
	// carry (a duplicate credited only to the collective record).
	Extra *recSpec
}

// seedWorks writes work entries (each with its one recording) into dataDir.
func seedWorks(t testing.TB, dataDir string, works ...workSpec) {
	t.Helper()
	files := map[string]string{}
	for _, w := range works {
		files[workAddr(w.Slug)] = workJSON(t, w)
		files[recAddr(w.Slug, recKey(w))] = recJSON(t, w, w.Rec)
		if w.Extra != nil {
			files[recAddr(w.Slug, w.Extra.Key)] = recJSON(t, w, *w.Extra)
		}
	}
	testpack.Seed(t, dataDir, files)
}

func recKey(w workSpec) string {
	if w.Rec.Key != "" {
		return w.Rec.Key
	}
	return "full-cast-2020"
}

func workJSON(t testing.TB, w workSpec) string {
	t.Helper()
	entry := map[string]any{
		"id":       w.Slug,
		"title":    w.Title,
		"authors":  w.Authors,
		"language": "en",
		"license":  "CC0-1.0",
		"sources":  []any{source(w.Slug)},
	}
	if len(w.Genres) > 0 {
		entry["genres"] = w.Genres
	}
	if w.AddedAt != "" {
		entry["added_at"] = w.AddedAt
	}
	return mustJSON(t, entry)
}

func recJSON(t testing.TB, w workSpec, r recSpec) string {
	t.Helper()
	entry := map[string]any{
		"id":        r.Key,
		"work":      w.Slug,
		"narrators": defaultTo(r.Narrators, []string{"full-cast"}),
		"language":  "en",
		"license":   "CC0-1.0",
		"sources":   []any{source(w.Slug)},
		"publisher": defaultStr(r.Publisher, "GraphicAudio"),
	}
	if len(r.ASINs) > 0 {
		var asins []any
		for _, a := range r.ASINs {
			region, value := splitPair(a)
			asins = append(asins, map[string]any{"region": region, "asin": value})
		}
		entry["asin"] = asins
	}
	if r.Runtime > 0 {
		entry["runtime_min"] = r.Runtime
	}
	if r.Release != "" {
		entry["release_date"] = r.Release
	}
	if r.Cover != "" {
		entry["cover_url"] = r.Cover
	}
	if r.AddedAt != "" {
		entry["added_at"] = r.AddedAt
	}
	if r.Abridged != nil {
		entry["abridged"] = *r.Abridged
	}
	if r.Chapters {
		entry["chapters"] = []any{
			map[string]any{"title": "Chapter 1", "start_ms": 0, "length_ms": 60000},
		}
	}
	return mustJSON(t, entry)
}

func source(ref string) map[string]any {
	return map[string]any{"type": "libex-import", "ref": ref, "imported_at": "2026-07-30"}
}

// seedSeries writes one series entry. positions is work slug -> position.
func seedSeries(t testing.TB, dataDir, slug, name string, works [][2]string) {
	t.Helper()
	var list []any
	for _, w := range works {
		list = append(list, map[string]any{"work": w[0], "position": w[1]})
	}
	entry := map[string]any{
		"id":      slug,
		"name":    name,
		"works":   list,
		"license": "CC0-1.0",
		"sources": []any{source(slug)},
	}
	testpack.Seed(t, dataDir, map[string]string{seriesAddr(slug): mustJSON(t, entry)})
}

// seedPeople writes the person records a valid tree needs for its credits.
func seedPeople(t testing.TB, dataDir string, names map[string]string) {
	t.Helper()
	files := map[string]string{}
	for _, slug := range sortedKeys(names) {
		files[personAddr(slug)] = mustJSON(t, map[string]any{
			"id": slug, "name": names[slug], "license": "CC0-1.0",
			"sources": []any{source(slug)},
		})
	}
	testpack.Seed(t, dataDir, files)
}

// writeCompleteSets writes an NDJSON complete-set export and returns its path.
func writeCompleteSets(t testing.TB, rows ...map[string]any) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sets.ndjson")
	var body []byte
	for _, r := range rows {
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatalf("marshal complete-set row: %v", err)
		}
		body = append(append(body, b...), '\n')
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write complete-set export: %v", err)
	}
	return path
}

// run plans and applies a remediation over dataDir.
func run(t testing.TB, dataDir string, write bool, sets string) *Report {
	t.Helper()
	rep, err := Run(Options{Dir: dataDir, Write: write, CompleteSets: sets, Today: testToday})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return rep
}

// readWork returns a work entry (without its recordings) as a decoded object.
func readWork(t testing.TB, dataDir, slug string) obj {
	t.Helper()
	raw, ok := testpack.Raw(t, dataDir, workAddr(slug))
	if !ok {
		t.Fatalf("work %s is not in the tree", slug)
	}
	o, err := decodeObj(raw)
	if err != nil {
		t.Fatalf("work %s: %v", slug, err)
	}
	return o
}

// readRecording returns a work's one recording as a decoded object.
func readRecording(t testing.TB, dataDir, slug string) (string, obj) {
	t.Helper()
	keys := testpack.Recordings(t, dataDir, slug)
	if len(keys) != 1 {
		t.Fatalf("work %s has %d recordings, want 1 (%v)", slug, len(keys), keys)
	}
	raw, ok := testpack.Raw(t, dataDir, recAddr(slug, keys[0]))
	if !ok {
		t.Fatalf("recording %s/%s is not in the tree", slug, keys[0])
	}
	o, err := decodeObj(raw)
	if err != nil {
		t.Fatalf("recording %s/%s: %v", slug, keys[0], err)
	}
	return keys[0], o
}

// readSeries returns a series' membership list as "work@position" strings,
// sorted, which is the shape an assertion reads best.
func readSeries(t testing.TB, dataDir, slug string) []string {
	t.Helper()
	raw, ok := testpack.Raw(t, dataDir, seriesAddr(slug))
	if !ok {
		t.Fatalf("series %s is not in the tree", slug)
	}
	o, err := decodeObj(raw)
	if err != nil {
		t.Fatalf("series %s: %v", slug, err)
	}
	works, err := seriesWorks(o)
	if err != nil {
		t.Fatalf("series %s: %v", slug, err)
	}
	out := make([]string, 0, len(works))
	for _, w := range works {
		out = append(out, w.Work+"@"+w.Position)
	}
	sort.Strings(out)
	return out
}

// asinValues returns a recording's identifiers in the order it lists them.
func asinValues(rec obj) []string {
	var out []string
	for _, a := range rec.asins() {
		out = append(out, a.ASIN)
	}
	return out
}

// refusalsIn returns the refusals of one category.
func refusalsIn(rep *Report, category string) []Refusal {
	var out []Refusal
	for _, r := range rep.Refusals {
		if r.Category == category {
			out = append(out, r)
		}
	}
	return out
}

// treeBytes reads every pack file under dataDir, so two runs can be compared
// byte for byte.
func treeBytes(t testing.TB, dataDir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(dataDir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		b, err := os.ReadFile(p) //nolint:gosec // a test tree
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dataDir, p)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = string(b)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dataDir, err)
	}
	return out
}

func mustJSON(t testing.TB, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	return string(b)
}

func defaultTo(v, fallback []string) []string {
	if len(v) == 0 {
		return fallback
	}
	return v
}

func defaultStr(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func splitPair(s string) (string, string) {
	for i := 0; i < len(s); i++ {
		if s[i] == ':' {
			return s[:i], s[i+1:]
		}
	}
	return "us", s
}

func boolPtr(v bool) *bool { return &v }
