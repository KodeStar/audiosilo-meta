package importer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/canonical"
	"github.com/kodestar/audiosilo-meta/internal/check"
)

// jsonInto decodes s into v with UseNumber so coercion helpers see json.Number.
func jsonInto(s string, v any) error {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	return dec.Decode(v)
}

// writeBooks writes a books.json into a temp dir and returns its path.
func writeBooks(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "books.json")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// runImport runs the importer against a fresh empty data dir and returns the
// summary and the data dir.
func runImport(t *testing.T, booksJSON string, dryRun bool) (Summary, string) {
	t.Helper()
	dataDir := t.TempDir()
	books := writeBooks(t, booksJSON)
	sum, err := Run(books, Options{DataDir: dataDir, ImportDate: "2026-07-11", DryRun: dryRun})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return sum, dataDir
}

func readJSON(t *testing.T, path string, v any) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if ok, ferr := canonical.IsCanonical(raw); ferr != nil || !ok {
		t.Errorf("%s is not canonical (err=%v)", path, ferr)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestImportBasic(t *testing.T) {
	fixture, err := os.ReadFile("testdata/books_basic.json")
	if err != nil {
		t.Fatal(err)
	}
	sum, dataDir := runImport(t, string(fixture), false)

	if sum.NewWorks != 3 {
		t.Errorf("NewWorks = %d, want 3", sum.NewWorks)
	}
	if sum.NewRecordings != 3 {
		t.Errorf("NewRecordings = %d, want 3", sum.NewRecordings)
	}
	// 2 authors + 2 narrators (shared between the two Ledger books) + 1 author + 1 narrator (Grenzland) = 6
	if sum.NewPeople != 6 {
		t.Errorf("NewPeople = %d, want 6", sum.NewPeople)
	}
	if sum.NewSeries != 1 {
		t.Errorf("NewSeries = %d, want 1", sum.NewSeries)
	}

	// The whole tree must validate.
	if res := check.Load(dataDir); !res.OK() {
		t.Fatalf("imported tree failed validation:\n%v", res.Problems)
	}

	// Work: title from title_short, authors as slugs, no first_published, no description.
	var work struct {
		Title          string   `json:"title"`
		Authors        []string `json:"authors"`
		Language       string   `json:"language"`
		FirstPublished string   `json:"first_published"`
		Description    string   `json:"description"`
		Sources        []struct {
			Type       string `json:"type"`
			Ref        string `json:"ref"`
			ImportedAt string `json:"imported_at"`
		} `json:"sources"`
	}
	readJSON(t, filepath.Join(dataDir, "works/th/the-iron-ledger/work.json"), &work)
	if work.Title != "The Iron Ledger" {
		t.Errorf("work title = %q, want title_short", work.Title)
	}
	if len(work.Authors) != 2 || work.Authors[0] != "mara-quill" || work.Authors[1] != "devon-ashe" {
		t.Errorf("work authors = %v", work.Authors)
	}
	if work.Language != "en" {
		t.Errorf("work language = %q", work.Language)
	}
	if work.FirstPublished != "" {
		t.Errorf("first_published must be omitted, got %q", work.FirstPublished)
	}
	if work.Description != "" {
		t.Errorf("publisher description leaked into work: %q", work.Description)
	}
	if len(work.Sources) != 1 || work.Sources[0].Type != "openaudible-import" ||
		work.Sources[0].Ref != "B0SYNTH001" || work.Sources[0].ImportedAt != "2026-07-11" {
		t.Errorf("work sources = %+v", work.Sources)
	}

	// Recording: chapters trimmed, runtime rounded, cover https, region-scoped ASIN, abridged false.
	var rec struct {
		Narrators   []string `json:"narrators"`
		Abridged    *bool    `json:"abridged"`
		RuntimeMin  int      `json:"runtime_min"`
		ReleaseDate string   `json:"release_date"`
		Publisher   string   `json:"publisher"`
		CoverURL    string   `json:"cover_url"`
		ASIN        []struct {
			Region string `json:"region"`
			ASIN   string `json:"asin"`
		} `json:"asin"`
		Chapters []struct {
			Title    string `json:"title"`
			StartMS  int64  `json:"start_ms"`
			LengthMS int64  `json:"length_ms"`
		} `json:"chapters"`
	}
	readJSON(t, filepath.Join(dataDir, "works/th/the-iron-ledger/recordings/priya-lund-2025.json"), &rec)
	if len(rec.Narrators) != 2 || rec.Narrators[0] != "priya-lund" {
		t.Errorf("narrators = %v", rec.Narrators)
	}
	if rec.Abridged == nil || *rec.Abridged != false {
		t.Errorf("abridged = %v, want explicit false", rec.Abridged)
	}
	if rec.RuntimeMin != 724 { // round(43420/60) = 724
		t.Errorf("runtime_min = %d, want 724", rec.RuntimeMin)
	}
	if rec.CoverURL != "https://covers.example.com/iron-ledger.jpg" {
		t.Errorf("cover_url = %q", rec.CoverURL)
	}
	if len(rec.ASIN) != 1 || rec.ASIN[0].Region != "us" || rec.ASIN[0].ASIN != "B0SYNTH001" {
		t.Errorf("asin = %+v", rec.ASIN)
	}
	if len(rec.Chapters) != 3 {
		t.Fatalf("chapters = %d, want 3", len(rec.Chapters))
	}
	if rec.Chapters[0].Title != "Prologue" || rec.Chapters[1].Title != "Chapter One" {
		t.Errorf("chapter titles not trimmed: %q, %q", rec.Chapters[0].Title, rec.Chapters[1].Title)
	}
	if rec.Chapters[0].StartMS != 0 || rec.Chapters[1].StartMS != 60000 {
		t.Errorf("chapter offsets wrong: %+v", rec.Chapters)
	}

	// Second Ledger recording: abridged null -> field omitted entirely.
	raw, _ := os.ReadFile(filepath.Join(dataDir, "works/th/the-bronze-ledger/recordings/priya-lund-2025.json"))
	if strings.Contains(string(raw), "abridged") {
		t.Errorf("abridged should be omitted for a null source value: %s", raw)
	}

	// Series: three works, one at the omnibus range position.
	var series struct {
		Works []struct {
			Work     string `json:"work"`
			Position string `json:"position"`
		} `json:"works"`
	}
	readJSON(t, filepath.Join(dataDir, "series/th/the-ledger-wars.json"), &series)
	if len(series.Works) != 3 {
		t.Fatalf("series works = %d, want 3", len(series.Works))
	}
	foundRange := false
	for _, sw := range series.Works {
		if sw.Position == "1-3.5" && sw.Work == "grenzland" {
			foundRange = true
		}
	}
	if !foundRange {
		t.Errorf("omnibus range position missing: %+v", series.Works)
	}
}

func TestDryRunWritesNothing(t *testing.T) {
	fixture, _ := os.ReadFile("testdata/books_basic.json")
	sum, dataDir := runImport(t, string(fixture), true)
	if sum.NewWorks != 3 || sum.NewRecordings != 3 {
		t.Errorf("dry run should still compute the plan: %+v", sum)
	}
	entries, _ := os.ReadDir(dataDir)
	if len(entries) != 0 {
		t.Errorf("dry run wrote files: %v", entries)
	}
}

func TestDedupByASIN(t *testing.T) {
	// Seed a data tree that already contains a recording with B0SYNTH001.
	dataDir := t.TempDir()
	seed := map[string]string{
		"people/ma/mara-quill.json":                         `{"id":"mara-quill","license":"CC0-1.0","name":"Mara Quill","sources":[{"type":"user"}]}`,
		"people/pr/priya-lund.json":                         `{"id":"priya-lund","license":"CC0-1.0","name":"Priya Lund","sources":[{"type":"user"}]}`,
		"works/th/the-iron-ledger/work.json":                `{"authors":["mara-quill"],"id":"the-iron-ledger","language":"en","license":"CC0-1.0","sources":[{"type":"user"}],"title":"The Iron Ledger"}`,
		"works/th/the-iron-ledger/recordings/existing.json": `{"asin":[{"asin":"B0SYNTH001","region":"us"}],"id":"existing","language":"en","license":"CC0-1.0","narrators":["priya-lund"],"sources":[{"type":"user"}],"work":"the-iron-ledger"}`,
	}
	for rel, content := range seed {
		full := filepath.Join(dataDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	books := `[{"asin":"B0SYNTH001","title_short":"The Iron Ledger","author":"Mara Quill","narrated_by":"Priya Lund","language":"english","region":"US","seconds":1000}]`
	sum, err := Run(writeBooks(t, books), Options{DataDir: dataDir, ImportDate: "2026-07-11"})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1 (ASIN already present)", sum.Skipped)
	}
	if sum.NewWorks != 0 || sum.NewRecordings != 0 || sum.NewPeople != 0 {
		t.Errorf("dedup should create nothing new: %+v", sum)
	}
}

func TestSkipMissingNarrator(t *testing.T) {
	books := `[{"asin":"B0NONARR01","title_short":"No Voice","author":"Someone","language":"english"}]`
	sum, dataDir := runImport(t, books, false)
	if sum.NewRecordings != 0 || sum.NewWorks != 0 {
		t.Errorf("book without narrator must be skipped: %+v", sum)
	}
	if len(sum.Warnings) != 1 || !strings.Contains(sum.Warnings[0], "no narrator") {
		t.Errorf("expected a no-narrator warning, got %v", sum.Warnings)
	}
	if exists(filepath.Join(dataDir, "works")) {
		t.Errorf("no work should be written")
	}
}

func TestSkipUnknownLanguage(t *testing.T) {
	books := `[{"asin":"B0BADLANG1","title_short":"Mystery","author":"A","narrated_by":"N","language":"klingon"}]`
	sum, _ := runImport(t, books, false)
	if sum.NewWorks != 0 {
		t.Errorf("unknown-language book must be skipped: %+v", sum)
	}
	if len(sum.Warnings) != 1 || !strings.Contains(sum.Warnings[0], "unknown language") {
		t.Errorf("expected unknown-language warning, got %v", sum.Warnings)
	}
}

func TestChapterMonotonicFallback(t *testing.T) {
	// Chapters that do not start at 0 -> chapters omitted, book still imported.
	books := `[{"asin":"B0CHAPBAD1","title_short":"Bad Chapters","author":"A","narrated_by":"Nadia Vox","language":"english","region":"US","release_date":"2023-05-01","seconds":600,
		"chapters":[{"start_offset_ms":500,"length_ms":1000,"title":"One"},{"start_offset_ms":1500,"length_ms":1000,"title":"Two"}]}]`
	sum, dataDir := runImport(t, books, false)
	if sum.NewRecordings != 1 {
		t.Fatalf("book should still import: %+v", sum)
	}
	if len(sum.Warnings) != 1 || !strings.Contains(sum.Warnings[0], "chapters") {
		t.Errorf("expected a chapters warning, got %v", sum.Warnings)
	}
	raw, _ := os.ReadFile(filepath.Join(dataDir, "works/ba/bad-chapters/recordings/nadia-vox-2023.json"))
	if strings.Contains(string(raw), `"chapters"`) {
		t.Errorf("invalid chapters should be omitted, got: %s", raw)
	}
}

func TestWorkSlugCollisionAppendsAuthor(t *testing.T) {
	// Two different books share a title but have different authors -> the second
	// gets its slug disambiguated by the author.
	books := `[
		{"asin":"B0SAMETL01","title_short":"The Gathering","author":"Alice North","narrated_by":"V One","language":"english","seconds":600},
		{"asin":"B0SAMETL02","title_short":"The Gathering","author":"Bob South","narrated_by":"V Two","language":"english","seconds":600}
	]`
	sum, dataDir := runImport(t, books, false)
	if sum.NewWorks != 2 {
		t.Fatalf("expected 2 distinct works, got %d", sum.NewWorks)
	}
	if !exists(filepath.Join(dataDir, "works/th/the-gathering/work.json")) {
		t.Errorf("first work should own the bare slug")
	}
	if !exists(filepath.Join(dataDir, "works/th/the-gathering-bob-south/work.json")) {
		t.Errorf("second work should be disambiguated by author: %v", listWorks(t, dataDir))
	}
	if !hasWarning(sum.Warnings, "taken by a different book") {
		t.Errorf("expected a slug-collision warning, got %v", sum.Warnings)
	}
}

func TestSameWorkMergesRecordings(t *testing.T) {
	// Two books, same title AND authors, different narrations -> one work, two recordings.
	books := `[
		{"asin":"B0MERGE001","title_short":"Twin Tale","author":"Same Author","narrated_by":"Reader A","language":"english","seconds":600,"release_date":"2020-01-01"},
		{"asin":"B0MERGE002","title_short":"Twin Tale","author":"Same Author","narrated_by":"Reader B","language":"english","seconds":600,"release_date":"2021-01-01"}
	]`
	sum, dataDir := runImport(t, books, false)
	if sum.NewWorks != 1 {
		t.Errorf("same title+author should be one work, got %d", sum.NewWorks)
	}
	if sum.NewRecordings != 2 {
		t.Errorf("expected 2 recordings under the shared work, got %d", sum.NewRecordings)
	}
	recsDir := filepath.Join(dataDir, "works/tw/twin-tale/recordings")
	entries, _ := os.ReadDir(recsDir)
	if len(entries) != 2 {
		t.Errorf("expected 2 recording files, got %v", entries)
	}
}

func TestExtendExistingSeries(t *testing.T) {
	// Seed a series, then import a book that adds a new work to it. The existing
	// series' non-managed fields (authors) must survive.
	dataDir := t.TempDir()
	seed := map[string]string{
		"people/ex/existing-author.json": `{"id":"existing-author","license":"CC0-1.0","name":"Existing Author","sources":[{"type":"user"}]}`,
		"works/bo/book-alpha/work.json":  `{"authors":["existing-author"],"id":"book-alpha","language":"en","license":"CC0-1.0","sources":[{"type":"user"}],"title":"Book Alpha"}`,
		"series/my/my-series.json":       `{"authors":["existing-author"],"id":"my-series","license":"CC0-1.0","name":"My Series","sources":[{"type":"user"}],"works":[{"position":"1","work":"book-alpha"}]}`,
	}
	for rel, content := range seed {
		full := filepath.Join(dataDir, filepath.FromSlash(rel))
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		_ = os.WriteFile(full, []byte(content), 0o644)
	}
	books := `[{"asin":"B0EXTEND01","title_short":"Book Beta","author":"Existing Author","narrated_by":"Voice","series_name":"My Series","series_sequence":"2","language":"english","seconds":600}]`
	sum, err := Run(writeBooks(t, books), Options{DataDir: dataDir, ImportDate: "2026-07-11"})
	if err != nil {
		t.Fatal(err)
	}
	if sum.NewSeries != 0 {
		t.Errorf("existing series should be extended, not recreated: %+v", sum)
	}
	var series struct {
		Authors []string `json:"authors"`
		Works   []struct {
			Work     string `json:"work"`
			Position string `json:"position"`
		} `json:"works"`
	}
	readJSON(t, filepath.Join(dataDir, "series/my/my-series.json"), &series)
	if len(series.Authors) != 1 || series.Authors[0] != "existing-author" {
		t.Errorf("existing series authors were lost: %+v", series.Authors)
	}
	if len(series.Works) != 2 {
		t.Fatalf("series should now hold 2 works, got %d", len(series.Works))
	}
	if res := check.Load(dataDir); !res.OK() {
		t.Fatalf("extended tree failed validation: %v", res.Problems)
	}
}

func listWorks(t *testing.T, dataDir string) []string {
	t.Helper()
	var out []string
	_ = filepath.Walk(filepath.Join(dataDir, "works"), func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			out = append(out, p)
		}
		return nil
	})
	return out
}

func hasWarning(warnings []string, sub string) bool {
	for _, w := range warnings {
		if strings.Contains(w, sub) {
			return true
		}
	}
	return false
}

func TestPersonSpellingVariantsMerge(t *testing.T) {
	// Spelling variants of the same name in one batch must resolve to ONE person
	// record: the slug is the normalized identity.
	books := `[
		{"asin":"B0VARIANT1","title_short":"Steel World","author":"B.V. Larson","narrated_by":"Ramón De Ocampo","language":"english","region":"US","release_date":"2013-01-01","seconds":600},
		{"asin":"B0VARIANT2","title_short":"Dust World","author":"B. V. Larson","narrated_by":"Ramon de Ocampo","language":"english","region":"US","release_date":"2014-01-01","seconds":600}
	]`
	sum, dataDir := runImport(t, books, false)
	if sum.NewPeople != 2 { // one author + one narrator, shared across both books
		t.Errorf("NewPeople = %d, want 2 (variants must merge)", sum.NewPeople)
	}
	if len(sum.Warnings) != 0 {
		t.Errorf("variant merge must not warn: %v", sum.Warnings)
	}
	if exists(filepath.Join(dataDir, "people/b-/b-v-larson-2.json")) {
		t.Errorf("numbered duplicate person was created")
	}
	// First occurrence's name wins.
	var person struct {
		Name string `json:"name"`
	}
	readJSON(t, filepath.Join(dataDir, "people/ra/ramon-de-ocampo.json"), &person)
	if person.Name != "Ramón De Ocampo" {
		t.Errorf("first-seen name should win, got %q", person.Name)
	}
	// Both works reference the same author slug.
	for _, w := range []string{"works/st/steel-world/work.json", "works/du/dust-world/work.json"} {
		var work struct {
			Authors []string `json:"authors"`
		}
		readJSON(t, filepath.Join(dataDir, w), &work)
		if len(work.Authors) != 1 || work.Authors[0] != "b-v-larson" {
			t.Errorf("%s authors = %v, want [b-v-larson]", w, work.Authors)
		}
	}
}

func TestPersonVariantReusesExistingRecord(t *testing.T) {
	// A diacritic variant of a person already in the catalog reuses the existing
	// record; its committed name is kept and no new file is emitted.
	dataDir := t.TempDir()
	seedFile := `{"id":"ramon-de-ocampo","license":"CC0-1.0","name":"Ramón De Ocampo","sources":[{"type":"user"}]}`
	full := filepath.Join(dataDir, "people/ra/ramon-de-ocampo.json")
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(seedFile), 0o644); err != nil {
		t.Fatal(err)
	}

	books := `[{"asin":"B0REUSE001","title_short":"Wimpy Tales","author":"Ramon de Ocampo","narrated_by":"Fresh Voice","language":"english","region":"US","seconds":600}]`
	sum, err := Run(writeBooks(t, books), Options{DataDir: dataDir, ImportDate: "2026-07-11"})
	if err != nil {
		t.Fatal(err)
	}
	if sum.NewPeople != 1 { // only the narrator is new
		t.Errorf("NewPeople = %d, want 1 (author variant must reuse existing)", sum.NewPeople)
	}
	raw, _ := os.ReadFile(full)
	if string(raw) != seedFile {
		t.Errorf("existing person record was rewritten: %s", raw)
	}
	var work struct {
		Authors []string `json:"authors"`
	}
	readJSON(t, filepath.Join(dataDir, "works/wi/wimpy-tales/work.json"), &work)
	if len(work.Authors) != 1 || work.Authors[0] != "ramon-de-ocampo" {
		t.Errorf("work should reference the existing person, got %v", work.Authors)
	}
}

func TestSeriesVolumesSharingShortTitle(t *testing.T) {
	// Dragon-Heart shape: every volume shares title_short but claims a different
	// series position. The pre-pass must give EVERY volume a full-title-derived
	// work (the incumbent does not squat the short slug), each placed in the
	// series at its own position, with no merge warnings.
	books := `[
		{"asin":"B0DRAGONH1","title":"Dragon Heart - Book 1: Iron Will","title_short":"Dragon Heart","author":"Kirill Klevanski","narrated_by":"Zach Villa","series_name":"Dragon Heart","series_sequence":"1","language":"english","region":"US","release_date":"2019-01-01","seconds":60000},
		{"asin":"B0DRAGONH2","title":"Dragon Heart - Book 5: Sea of Sand","title_short":"Dragon Heart","author":"Kirill Klevanski","narrated_by":"Zach Villa","series_name":"Dragon Heart","series_sequence":"5","language":"english","region":"US","release_date":"2020-01-01","seconds":60000},
		{"asin":"B0DRAGONH3","title":"Dragon Heart - Book 10: Land of War","title_short":"Dragon Heart","author":"Kirill Klevanski","narrated_by":"Zach Villa","series_name":"Dragon Heart","series_sequence":"10","language":"english","region":"US","release_date":"2021-01-01","seconds":60000}
	]`
	sum, dataDir := runImport(t, books, false)
	if sum.NewWorks != 3 {
		t.Fatalf("NewWorks = %d, want 3 distinct volumes", sum.NewWorks)
	}
	if sum.NewRecordings != 3 || sum.NewSeries != 1 {
		t.Errorf("recordings/series = %d/%d, want 3/1", sum.NewRecordings, sum.NewSeries)
	}
	if len(sum.Warnings) != 0 {
		t.Errorf("no merge warnings expected, got %v", sum.Warnings)
	}
	if exists(filepath.Join(dataDir, "works/dr/dragon-heart/work.json")) {
		t.Errorf("no volume may squat the ambiguous short-title slug")
	}
	wantWorks := map[string]string{
		"dragon-heart-book-1-iron-will":    "1",
		"dragon-heart-book-5-sea-of-sand":  "5",
		"dragon-heart-book-10-land-of-war": "10",
	}
	for slug := range wantWorks {
		if !exists(filepath.Join(dataDir, "works", slug[:2], slug, "work.json")) {
			t.Errorf("missing full-title work %q; works: %v", slug, listWorks(t, dataDir))
		}
	}
	var series struct {
		Works []struct {
			Work     string `json:"work"`
			Position string `json:"position"`
		} `json:"works"`
	}
	readJSON(t, filepath.Join(dataDir, "series/dr/dragon-heart.json"), &series)
	if len(series.Works) != 3 {
		t.Fatalf("series should hold 3 works, got %d", len(series.Works))
	}
	for _, sw := range series.Works {
		if wantWorks[sw.Work] != sw.Position {
			t.Errorf("series entry %q at %q, want %q", sw.Work, sw.Position, wantWorks[sw.Work])
		}
	}
	if res := check.Load(dataDir); !res.OK() {
		t.Fatalf("tree failed validation: %v", res.Problems)
	}
}

func TestExistingWorkDifferentSeriesPosition(t *testing.T) {
	// A book whose title_short slug maps onto an EXISTING on-disk work that sits
	// in the same series at a DIFFERENT position is a different volume: its work
	// derives from the full title; the existing work is untouched.
	dataDir := t.TempDir()
	seed := map[string]string{
		"people/ki/kirill-klevanski.json": `{"id":"kirill-klevanski","license":"CC0-1.0","name":"Kirill Klevanski","sources":[{"type":"user"}]}`,
		"works/dr/dragon-heart/work.json": `{"authors":["kirill-klevanski"],"id":"dragon-heart","language":"en","license":"CC0-1.0","sources":[{"type":"user"}],"title":"Dragon Heart"}`,
		"series/dr/dragon-heart.json":     `{"id":"dragon-heart","license":"CC0-1.0","name":"Dragon Heart","sources":[{"type":"user"}],"works":[{"position":"1","work":"dragon-heart"}]}`,
	}
	for rel, content := range seed {
		full := filepath.Join(dataDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	books := `[{"asin":"B0DRAGONH5","title":"Dragon Heart - Book 5: Sea of Sand","title_short":"Dragon Heart","author":"Kirill Klevanski","narrated_by":"Zach Villa","series_name":"Dragon Heart","series_sequence":"5","language":"english","region":"US","release_date":"2020-01-01","seconds":60000}]`
	sum, err := Run(writeBooks(t, books), Options{DataDir: dataDir, ImportDate: "2026-07-11"})
	if err != nil {
		t.Fatal(err)
	}
	if sum.NewWorks != 1 {
		t.Fatalf("NewWorks = %d, want 1 (a new volume, not a merge)", sum.NewWorks)
	}
	if !exists(filepath.Join(dataDir, "works/dr/dragon-heart-book-5-sea-of-sand/work.json")) {
		t.Errorf("full-title work missing; works: %v", listWorks(t, dataDir))
	}
	// The existing volume kept its slug, file, and lone recording-less state.
	raw, _ := os.ReadFile(filepath.Join(dataDir, "works/dr/dragon-heart/work.json"))
	if string(raw) != seed["works/dr/dragon-heart/work.json"] {
		t.Errorf("existing work was rewritten: %s", raw)
	}
	var series struct {
		Works []struct {
			Work     string `json:"work"`
			Position string `json:"position"`
		} `json:"works"`
	}
	readJSON(t, filepath.Join(dataDir, "series/dr/dragon-heart.json"), &series)
	if len(series.Works) != 2 {
		t.Fatalf("series should hold 2 works, got %+v", series.Works)
	}
	posByWork := map[string]string{}
	for _, sw := range series.Works {
		posByWork[sw.Work] = sw.Position
	}
	if posByWork["dragon-heart"] != "1" || posByWork["dragon-heart-book-5-sea-of-sand"] != "5" {
		t.Errorf("series membership wrong: %+v", posByWork)
	}
	if res := check.Load(dataDir); !res.OK() {
		t.Fatalf("tree failed validation: %v", res.Problems)
	}
}

func TestSeriesPositionConflictStillWarns(t *testing.T) {
	// Two genuinely different works claiming the SAME series position (the Halo
	// pos-4 shape) keep the warn-and-skip behavior.
	books := `[
		{"asin":"B0HALOPOS1","title_short":"First Strike","author":"Eric Nylund","narrated_by":"Todd McLaren","series_name":"Halo","series_sequence":"4","language":"english","region":"US","seconds":60000},
		{"asin":"B0HALOPOS2","title_short":"Some Other Book","author":"Different Writer","narrated_by":"Todd McLaren","series_name":"Halo","series_sequence":"4","language":"english","region":"US","seconds":60000}
	]`
	sum, dataDir := runImport(t, books, false)
	if sum.NewWorks != 2 {
		t.Errorf("NewWorks = %d, want 2", sum.NewWorks)
	}
	if !hasWarning(sum.Warnings, `position "4" already taken`) {
		t.Errorf("expected a position-conflict warning, got %v", sum.Warnings)
	}
	var series struct {
		Works []struct {
			Work string `json:"work"`
		} `json:"works"`
	}
	readJSON(t, filepath.Join(dataDir, "series/ha/halo.json"), &series)
	if len(series.Works) != 1 || series.Works[0].Work != "first-strike" {
		t.Errorf("only the first claimant should hold the position: %+v", series.Works)
	}
}

func TestPrePassGroupsByTitleSlugOnly(t *testing.T) {
	// Volume 1 carries extra credited people in the author field (real Audible
	// shape: "Kirill Klevanski, Valeria Kornosenko - introduction, ..."), so an
	// author-set group key would let it escape the group and squat the bare
	// slug. Grouping is by title slug only: ALL volumes get full-title works.
	books := `[
		{"asin":"B0DRAGONV1","title":"Dragon Heart - Book 1: Iron Will","title_short":"Dragon Heart","author":"Kirill Klevanski, Valeria Kornosenko - introduction, J. Kharkova - translator","narrated_by":"Zach Villa","series_name":"Dragon Heart","series_sequence":"1","language":"english","region":"US","release_date":"2019-01-01","seconds":60000},
		{"asin":"B0DRAGONV2","title":"Dragon Heart - Book 5: Sea of Sand","title_short":"Dragon Heart","author":"Kirill Klevanski","narrated_by":"Zach Villa","series_name":"Dragon Heart","series_sequence":"5","language":"english","region":"US","release_date":"2020-01-01","seconds":60000},
		{"asin":"B0DRAGONV3","title":"Dragon Heart - Book 10: Land of War","title_short":"Dragon Heart","author":"Kirill Klevanski","narrated_by":"Zach Villa","series_name":"Dragon Heart","series_sequence":"10","language":"english","region":"US","release_date":"2021-01-01","seconds":60000}
	]`
	sum, dataDir := runImport(t, books, false)
	if sum.NewWorks != 3 {
		t.Fatalf("NewWorks = %d, want 3", sum.NewWorks)
	}
	if len(sum.Warnings) != 0 {
		t.Errorf("no warnings expected, got %v", sum.Warnings)
	}
	if exists(filepath.Join(dataDir, "works/dr/dragon-heart/work.json")) {
		t.Errorf("volume 1 squatted the bare slug despite extra credits; works: %v", listWorks(t, dataDir))
	}
	for _, slug := range []string{
		"dragon-heart-book-1-iron-will",
		"dragon-heart-book-5-sea-of-sand",
		"dragon-heart-book-10-land-of-war",
	} {
		if !exists(filepath.Join(dataDir, "works", slug[:2], slug, "work.json")) {
			t.Errorf("missing full-title work %q", slug)
		}
	}
	// Role qualifiers were stripped from the extra credits: clean person
	// records, no qualifier-suffixed slugs.
	var work struct {
		Authors []string `json:"authors"`
	}
	readJSON(t, filepath.Join(dataDir, "works/dr/dragon-heart-book-1-iron-will/work.json"), &work)
	wantAuthors := []string{"kirill-klevanski", "valeria-kornosenko", "j-kharkova"}
	if !reflect.DeepEqual(work.Authors, wantAuthors) {
		t.Errorf("volume 1 authors = %v, want %v", work.Authors, wantAuthors)
	}
	var person struct {
		Name string `json:"name"`
	}
	readJSON(t, filepath.Join(dataDir, "people/va/valeria-kornosenko.json"), &person)
	if person.Name != "Valeria Kornosenko" {
		t.Errorf("credited person name = %q, want qualifier stripped", person.Name)
	}
	if exists(filepath.Join(dataDir, "people/va/valeria-kornosenko-introduction.json")) {
		t.Errorf("qualifier-suffixed person record was created")
	}
	if res := check.Load(dataDir); !res.OK() {
		t.Fatalf("tree failed validation: %v", res.Problems)
	}
	// The series holds all three volumes at their own positions.
	var series struct {
		Works []struct {
			Work     string `json:"work"`
			Position string `json:"position"`
		} `json:"works"`
	}
	readJSON(t, filepath.Join(dataDir, "series/dr/dragon-heart.json"), &series)
	if len(series.Works) != 3 {
		t.Errorf("series should hold 3 works, got %+v", series.Works)
	}
}
