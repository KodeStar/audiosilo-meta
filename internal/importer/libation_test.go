package importer

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/check"
)

// runLibation runs the Libation importer against a fresh empty data dir.
func runLibation(t *testing.T, exportJSON string, dryRun bool) (Summary, string) {
	t.Helper()
	return runWith(t, RunLibation, exportJSON, dryRun)
}

func TestLibationImportBasic(t *testing.T) {
	fixture, err := os.ReadFile("testdata/libation_export.json")
	if err != nil {
		t.Fatal(err)
	}
	sum, dataDir := runLibation(t, string(fixture), false)

	if sum.NewWorks != 3 {
		t.Errorf("NewWorks = %d, want 3", sum.NewWorks)
	}
	if sum.NewRecordings != 3 {
		t.Errorf("NewRecordings = %d, want 3", sum.NewRecordings)
	}
	// Primal Hunter: Zogarth + Travis Baldree (2); Wind and Truth: Brandon
	// Sanderson + Kate Reading + Michael Kramer (3); Dragon Heart: Kirill
	// Klevanski + Valeria Kornosenko + Kevin T. Collins (3) = 8 distinct.
	if sum.NewPeople != 8 {
		t.Errorf("NewPeople = %d, want 8", sum.NewPeople)
	}
	// The Primal Hunter, The Stormlight Archive, Dragon Heart Series. The Cosmere
	// is NOT created: its position is unknown, so the book is not placed in it.
	if sum.NewSeries != 3 {
		t.Errorf("NewSeries = %d, want 3", sum.NewSeries)
	}
	if exists(filepath.Join(dataDir, "series/th/the-cosmere.json")) {
		t.Errorf("The Cosmere series should not be created (unknown position)")
	}
	// The only warning is the Cosmere unknown-position note.
	if len(sum.Warnings) != 1 || !hasWarning(sum.Warnings, "The Cosmere") {
		t.Errorf("expected one Cosmere position warning, got %v", sum.Warnings)
	}

	if res := check.Load(dataDir); !res.OK() {
		t.Fatalf("imported tree failed validation:\n%v", res.Problems)
	}

	// Work: clean title from Title (not "Title: Subtitle"), CC0, libation source.
	var work struct {
		Title       string   `json:"title"`
		Authors     []string `json:"authors"`
		Language    string   `json:"language"`
		Description string   `json:"description"`
		Sources     []struct {
			Type       string `json:"type"`
			Ref        string `json:"ref"`
			ImportedAt string `json:"imported_at"`
		} `json:"sources"`
	}
	readJSON(t, filepath.Join(dataDir, "works/wi/wind-and-truth/work.json"), &work)
	if work.Title != "Wind and Truth" {
		t.Errorf("work title = %q, want %q", work.Title, "Wind and Truth")
	}
	if work.Language != "en" {
		t.Errorf("work language = %q, want en", work.Language)
	}
	if work.Description != "" {
		t.Errorf("publisher description leaked into work: %q", work.Description)
	}
	if len(work.Sources) != 1 || work.Sources[0].Type != "libation-import" ||
		work.Sources[0].Ref != "B0CQDJ3PND" || work.Sources[0].ImportedAt != "2026-07-11" {
		t.Errorf("work sources = %+v", work.Sources)
	}

	// Recording: two narrators, minutes->runtime, date, publisher, cover, uk ASIN.
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
	}
	readJSON(t, filepath.Join(dataDir, "works/wi/wind-and-truth/recordings/kate-reading-2024.json"), &rec)
	if !reflect.DeepEqual(rec.Narrators, []string{"kate-reading", "michael-kramer"}) {
		t.Errorf("narrators = %v", rec.Narrators)
	}
	if rec.Abridged == nil || *rec.Abridged != false {
		t.Errorf("abridged = %v, want explicit false", rec.Abridged)
	}
	if rec.RuntimeMin != 3768 { // 3768 min -> 226080s -> round back to 3768
		t.Errorf("runtime_min = %d, want 3768", rec.RuntimeMin)
	}
	if rec.ReleaseDate != "2024-12-06" {
		t.Errorf("release_date = %q, want 2024-12-06", rec.ReleaseDate)
	}
	if rec.Publisher != "Gollancz" {
		t.Errorf("publisher = %q", rec.Publisher)
	}
	if rec.CoverURL != "https://m.media-amazon.com/images/I/51ZFAWrapyL._SL500_.jpg" {
		t.Errorf("cover_url = %q", rec.CoverURL)
	}
	if len(rec.ASIN) != 1 || rec.ASIN[0].Region != "uk" || rec.ASIN[0].ASIN != "B0CQDJ3PND" {
		t.Errorf("asin = %+v", rec.ASIN)
	}

	// Multi-series: placed in The Stormlight Archive at 5, never in The Cosmere.
	var series struct {
		Works []struct {
			Work     string `json:"work"`
			Position string `json:"position"`
		} `json:"works"`
	}
	readJSON(t, filepath.Join(dataDir, "series/th/the-stormlight-archive.json"), &series)
	found := false
	for _, sw := range series.Works {
		if sw.Work == "wind-and-truth" && sw.Position == "5" {
			found = true
		}
	}
	if !found {
		t.Errorf("wind-and-truth not placed at position 5: %+v", series.Works)
	}

	// The single-series book carries its position.
	readJSON(t, filepath.Join(dataDir, "series/th/the-primal-hunter.json"), &series)
	if len(series.Works) != 1 || series.Works[0].Work != "the-primal-hunter-14" || series.Works[0].Position != "14" {
		t.Errorf("the-primal-hunter series = %+v", series.Works)
	}
}

func TestLibationDropsPersonalFields(t *testing.T) {
	fixture, _ := os.ReadFile("testdata/libation_export.json")
	_, dataDir := runLibation(t, string(fixture), false)

	// No personal/marketing field value should appear anywhere in the tree.
	var leaked []string
	_ = filepath.Walk(dataDir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		raw, _ := os.ReadFile(p)
		for _, bad := range []string{"user@example.com", "blurb", "favourite", "LitRPG", "Liberated", "2020-01-01"} {
			if strings.Contains(string(raw), bad) {
				leaked = append(leaked, bad+" in "+p)
			}
		}
		return nil
	})
	if len(leaked) != 0 {
		t.Errorf("personal/marketing fields leaked into the tree: %v", leaked)
	}
}

func TestLibationStripsRoleQualifierAndAbridged(t *testing.T) {
	fixture, _ := os.ReadFile("testdata/libation_export.json")
	_, dataDir := runLibation(t, string(fixture), false)

	// Dragon Heart: author role qualifier stripped, all-digit ASIN kept, uk region.
	var work struct {
		Authors []string `json:"authors"`
	}
	readJSON(t, filepath.Join(dataDir, "works/dr/dragon-heart/work.json"), &work)
	if !reflect.DeepEqual(work.Authors, []string{"kirill-klevanski", "valeria-kornosenko"}) {
		t.Errorf("authors = %v, want role qualifier stripped", work.Authors)
	}
	if exists(filepath.Join(dataDir, "people/va/valeria-kornosenko-introduction.json")) {
		t.Errorf("qualifier-suffixed person record was created")
	}
	var person struct {
		Name string `json:"name"`
	}
	readJSON(t, filepath.Join(dataDir, "people/va/valeria-kornosenko.json"), &person)
	if person.Name != "Valeria Kornosenko" {
		t.Errorf("person name = %q, want qualifier stripped", person.Name)
	}

	var rec struct {
		Abridged *bool `json:"abridged"`
		ASIN     []struct {
			Region string `json:"region"`
			ASIN   string `json:"asin"`
		} `json:"asin"`
	}
	readJSON(t, filepath.Join(dataDir, "works/dr/dragon-heart/recordings/kevin-t-collins-2020.json"), &rec)
	if rec.Abridged == nil || *rec.Abridged != true {
		t.Errorf("abridged = %v, want explicit true", rec.Abridged)
	}
	if len(rec.ASIN) != 1 || rec.ASIN[0].Region != "uk" || rec.ASIN[0].ASIN != "1705204724" {
		t.Errorf("asin = %+v, want uk/1705204724", rec.ASIN)
	}
}

func TestLibationDryRunWritesNothing(t *testing.T) {
	fixture, _ := os.ReadFile("testdata/libation_export.json")
	sum, dataDir := runLibation(t, string(fixture), true)
	if sum.NewWorks != 3 || sum.NewRecordings != 3 {
		t.Errorf("dry run should still compute the plan: %+v", sum)
	}
	entries, _ := os.ReadDir(dataDir)
	if len(entries) != 0 {
		t.Errorf("dry run wrote files: %v", entries)
	}
}

func TestLibationDedupByASIN(t *testing.T) {
	// Seed a data tree already containing the Primal Hunter recording (its ASIN).
	dataDir := t.TempDir()
	seed := map[string]string{
		"people/zo/zogarth.json":                                 `{"id":"zogarth","license":"CC0-1.0","name":"Zogarth","sources":[{"type":"user"}]}`,
		"people/tr/travis-baldree.json":                          `{"id":"travis-baldree","license":"CC0-1.0","name":"Travis Baldree","sources":[{"type":"user"}]}`,
		"works/th/the-primal-hunter-14/work.json":                `{"authors":["zogarth"],"id":"the-primal-hunter-14","language":"en","license":"CC0-1.0","sources":[{"type":"user"}],"title":"The Primal Hunter 14"}`,
		"works/th/the-primal-hunter-14/recordings/existing.json": `{"asin":[{"asin":"B0H1NBT6RF","region":"uk"}],"id":"existing","language":"en","license":"CC0-1.0","narrators":["travis-baldree"],"sources":[{"type":"user"}],"work":"the-primal-hunter-14"}`,
	}
	seedTree(t, dataDir, seed)

	// Only the Primal Hunter entry (its ASIN is already present).
	export := `[{"AudibleProductId":"B0H1NBT6RF","Locale":"uk","Title":"The Primal Hunter 14","AuthorNames":"Zogarth","NarratorNames":"Travis Baldree","LengthInMinutes":1118,"Language":"English","IsAbridged":false}]`
	sum, err := RunLibation(writeBooks(t, export), Options{DataDir: dataDir, ImportDate: "2026-07-12"})
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

func TestParseLibationSeries(t *testing.T) {
	cases := []struct {
		name      string
		order     string
		names     string
		wantNames []string
		wantSeq   []string // "" for an unknown position
		wantSeqOK []bool
	}{
		{
			name:      "single",
			order:     "14 : The Primal Hunter",
			names:     "The Primal Hunter",
			wantNames: []string{"The Primal Hunter"},
			wantSeq:   []string{"14"},
			wantSeqOK: []bool{true},
		},
		{
			name:      "multi with empty leading order",
			order:     " : The Cosmere, 5 : The Stormlight Archive",
			names:     "The Cosmere, The Stormlight Archive",
			wantNames: []string{"The Cosmere", "The Stormlight Archive"},
			wantSeq:   []string{"", "5"},
			wantSeqOK: []bool{false, true},
		},
		{
			name:      "colon in series name",
			order:     "1 : Discworld, 1 : Discworld: Rincewind",
			names:     "Discworld, Discworld: Rincewind",
			wantNames: []string{"Discworld", "Discworld: Rincewind"},
			wantSeq:   []string{"1", "1"},
			wantSeqOK: []bool{true, true},
		},
		{
			name:      "omnibus range",
			order:     "1-3.5 : Omnibus Series",
			names:     "Omnibus Series",
			wantNames: []string{"Omnibus Series"},
			wantSeq:   []string{"1-3.5"},
			wantSeqOK: []bool{true},
		},
		{
			name:      "unsorted sentinel becomes unknown",
			order:     "999999999 : Some Podcast",
			names:     "Some Podcast",
			wantNames: []string{"Some Podcast"},
			wantSeq:   []string{""},
			wantSeqOK: []bool{false},
		},
		{
			name:      "names-only fallback (no order)",
			order:     "",
			names:     "Lonely Series",
			wantNames: []string{"Lonely Series"},
			wantSeq:   []string{""},
			wantSeqOK: []bool{false},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			refs := parseLibationSeries(tc.order, tc.names)
			if len(refs) != len(tc.wantNames) {
				t.Fatalf("got %d refs, want %d: %+v", len(refs), len(tc.wantNames), refs)
			}
			for i, r := range refs {
				if r.name != tc.wantNames[i] || r.seq != tc.wantSeq[i] || r.seqOK != tc.wantSeqOK[i] {
					t.Errorf("ref %d = %+v, want name=%q seq=%q ok=%v", i, r, tc.wantNames[i], tc.wantSeq[i], tc.wantSeqOK[i])
				}
			}
		})
	}
}

func TestLibationCover(t *testing.T) {
	if got := libationCover(""); got != "" {
		t.Errorf("empty PictureId should yield no cover, got %q", got)
	}
	if got := libationCover("51cfid7m8gL"); got != "https://m.media-amazon.com/images/I/51cfid7m8gL._SL500_.jpg" {
		t.Errorf("cover = %q", got)
	}
	// A '+' in the id is percent-encoded so the URL is unambiguous.
	if got := libationCover("51zVN+Q+LcL"); got != "https://m.media-amazon.com/images/I/51zVN%2BQ%2BLcL._SL500_.jpg" {
		t.Errorf("cover with + = %q", got)
	}
}

func TestLibationDate(t *testing.T) {
	if got := libationDate("2024-12-06T03:00:00"); got != "2024-12-06" {
		t.Errorf("date = %q, want 2024-12-06", got)
	}
	if got := libationDate("2024-12-06"); got != "2024-12-06" {
		t.Errorf("date = %q, want 2024-12-06", got)
	}
	if got := libationDate(""); got != "" {
		t.Errorf("empty date = %q", got)
	}
}
