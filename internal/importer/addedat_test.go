package importer

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// added_at records when a record entered the database, so only the branches that
// CREATE one may stamp it. These tests pin both halves: a created work and
// recording carry the run's import date, and a record the run merely touched
// (an ASIN merge, an enrichment backfill) never gains one.

// addedAtOf reads a record's added_at ("" when the field is absent).
func addedAtOf(t *testing.T, dataDir, address string) string {
	t.Helper()
	var rec struct {
		AddedAt string `json:"added_at"`
	}
	readEntity(t, dataDir, address, &rec)
	return rec.AddedAt
}

func TestCreatedRecordsCarryAddedAt(t *testing.T) {
	books := `[{"asin":"B0ADDEDAT1","title_short":"Fresh Book","author":"Some Author","narrated_by":"A Reader","language":"english","region":"US","release_date":"2021-01-01","seconds":36000}]`
	sum, dataDir := runImport(t, books, false)
	if sum.NewWorks != 1 || sum.NewRecordings != 1 {
		t.Fatalf("summary = %+v, want 1 work / 1 recording", sum)
	}
	if got := addedAtOf(t, dataDir, "works/fr/fresh-book/work.json"); got != testImportDate {
		t.Errorf("work added_at = %q, want the run's import date %q", got, testImportDate)
	}
	if got := addedAtOf(t, dataDir, recAddr("fresh-book", "a-reader-2021")); got != testImportDate {
		t.Errorf("recording added_at = %q, want the run's import date %q", got, testImportDate)
	}
}

func TestASINMergeDoesNotStampAddedAt(t *testing.T) {
	// A catalogued work and recording with no added_at (the pre-field state the
	// migration backfill leaves behind for anything it could not date). A
	// re-release row merges its ASIN into that recording, which is not a
	// creation, so neither record may gain the field.
	dataDir := t.TempDir()
	const (
		workAddress = "works/on/one-production/work.json"
		recAddress  = "works/on/one-production/recordings/a-reader-2019.json"
	)
	seedTree(t, dataDir, map[string]string{
		"people/so/some-author.json": `{"id":"some-author","license":"CC0-1.0","name":"Some Author","sources":[{"type":"user"}]}`,
		"people/a-/a-reader.json":    `{"id":"a-reader","license":"CC0-1.0","name":"A Reader","sources":[{"type":"user"}]}`,
		workAddress:                  `{"authors":["some-author"],"id":"one-production","language":"en","license":"CC0-1.0","sources":[{"type":"user"}],"title":"One Production"}`,
		recAddress:                   `{"asin":[{"asin":"B0DEC20190","region":"us"}],"id":"a-reader-2019","language":"en","license":"CC0-1.0","narrators":["a-reader"],"runtime_min":600,"sources":[{"type":"user"}],"work":"one-production"}`,
	})

	books := `[{"asin":"B0JAN20200","title_short":"One Production","author":"Some Author","narrated_by":"A Reader","language":"english","region":"UK","release_date":"2020-01-05","seconds":36000}]`
	sum, err := Run(writeBooks(t, books), Options{DataDir: dataDir, ImportDate: testImportDate})
	if err != nil {
		t.Fatal(err)
	}
	if sum.MergedASINs != 1 || sum.NewRecordings != 0 || sum.NewWorks != 0 {
		t.Fatalf("summary = %+v, want a pure ASIN merge", sum)
	}
	if got := addedAtOf(t, dataDir, recAddress); got != "" {
		t.Errorf("merged recording gained added_at = %q; a merge is not a creation", got)
	}
	if got := addedAtOf(t, dataDir, workAddress); got != "" {
		t.Errorf("work gained added_at = %q on a merge", got)
	}
}

func TestEnrichDoesNotStampAddedAt(t *testing.T) {
	// Enrichment fills absent FACTS on records that entered earlier; added_at is
	// not one of them.
	dataDir := seedEnrichTree(t, nil)
	sum := runEnrich(t, dataDir, fullRow, false)
	if sum.EnrichedWorks != 1 || sum.EnrichedRecordings != 1 {
		t.Fatalf("EnrichedWorks/EnrichedRecordings = %d/%d, want 1/1", sum.EnrichedWorks, sum.EnrichedRecordings)
	}
	if got := addedAtOf(t, dataDir, recRel); got != "" {
		t.Errorf("enriched recording gained added_at = %q", got)
	}
	if got := addedAtOf(t, dataDir, workRel); got != "" {
		t.Errorf("enriched work gained added_at = %q", got)
	}
}

// TestImportRefusesLegacyLayout pins the dual-layout window's guard: the writer
// speaks pack only, so a tree still in the file-per-entity layout is refused
// loudly - before anything is planned - rather than written through a second
// path.
func TestImportRefusesLegacyLayout(t *testing.T) {
	dataDir := t.TempDir()
	// A legacy per-entity person record: no pack wrapper, so DetectLayout reads
	// the people family as legacy.
	legacy := filepath.Join(dataDir, "people", "so", "some-author.json")
	if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"id":"some-author","license":"CC0-1.0","name":"Some Author","sources":[{"type":"user"}]}` + "\n"
	if err := os.WriteFile(legacy, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	books := `[{"asin":"B0LEGACY01","title_short":"Legacy Book","author":"Some Author","narrated_by":"A Reader","language":"english","region":"US","seconds":600}]`
	_, err := Run(writeBooks(t, books), Options{DataDir: dataDir, ImportDate: testImportDate})
	if err == nil {
		t.Fatal("an import against a legacy tree must fail")
	}
	if !errors.Is(err, pack.ErrLegacyLayout) {
		t.Errorf("error = %v, want it to wrap pack.ErrLegacyLayout", err)
	}
	if !strings.Contains(err.Error(), "data/people") {
		t.Errorf("error = %v, want it to name the family that is still legacy", err)
	}

	// Refused safely: the run wrote nothing at all.
	if got, rerr := os.ReadFile(legacy); rerr != nil || string(got) != body {
		t.Errorf("the legacy tree was touched (err=%v): %s", rerr, got)
	}
	if _, serr := os.Stat(filepath.Join(dataDir, "works")); !os.IsNotExist(serr) {
		t.Errorf("the refused run created works/ (stat err = %v)", serr)
	}
}
