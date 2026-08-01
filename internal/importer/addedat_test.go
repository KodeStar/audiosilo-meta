package importer

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/testpack"
	"github.com/kodestar/audiosilo-meta/pkg/check"
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
	// One file-per-entity person record is enough: layout is detected per family
	// from the shape of a file under its root.
	seeded := testpack.SeedLegacyPerson(t, dataDir, "some-author", "Some Author")

	books := `[{"asin":"B0LEGACY01","title_short":"Legacy Book","author":"Some Author","narrated_by":"A Reader","language":"english","region":"US","seconds":600}]`
	_, err := Run(writeBooks(t, books), Options{DataDir: dataDir, ImportDate: testImportDate})
	if err == nil {
		t.Fatal("an import against a legacy tree must fail")
	}
	if !errors.Is(err, pack.ErrLegacyLayout) {
		t.Errorf("error = %v, want it to wrap pack.ErrLegacyLayout", err)
	}
	if !strings.Contains(err.Error(), pack.FamilyPeople.Root()) {
		t.Errorf("error = %v, want it to name the family that is still legacy", err)
	}

	// Refused safely: the run wrote nothing at all.
	testpack.AssertUntouched(t, dataDir, "some-author", seeded)
}

// TestCreateNeverOverwritesAnUndecodableEntry is the guard against silent
// recording loss. check.Load is best-effort: an entry it cannot decode is
// reported as a problem but is ABSENT from the Catalog the planner seeds its
// identity maps from, so the create branch believes the slug is free. A plain
// upsert there replaces the whole composite entry - every recording with it -
// and the run exits 0, because the replacement is itself valid.
func TestCreateNeverOverwritesAnUndecodableEntry(t *testing.T) {
	dataDir := t.TempDir()
	const workAddress = "works/br/broken-book/work.json"
	// "authors" is a string, not an array: valid JSON, valid pack, but the work
	// does not decode into model.Work, so it never reaches the Catalog.
	seedTree(t, dataDir, map[string]string{
		"people/so/some-author.json": `{"id":"some-author","license":"CC0-1.0","name":"Some Author","sources":[{"type":"user"}]}`,
		workAddress:                  `{"authors":"some-author","id":"broken-book","language":"en","license":"CC0-1.0","sources":[{"type":"user"}],"title":"Broken Book"}`,
		"works/br/broken-book/recordings/a-reader-2019.json": `{"asin":[{"asin":"B0KEEPME01","region":"us"}],"id":"a-reader-2019","language":"en","license":"CC0-1.0","narrators":["a-reader"],"sources":[{"type":"user"}],"work":"broken-book"}`,
	})
	// The premise: the loader really does drop this work.
	if cat := check.Load(dataDir).Catalog; cat != nil {
		for _, w := range cat.Works {
			if w.ID == "broken-book" {
				t.Fatalf("fixture no longer defeats the loader; the work reached the Catalog")
			}
		}
	}

	books := `[{"asin":"B0BROKEN01","title_short":"Broken Book","author":"Some Author","narrated_by":"A Reader","language":"english","region":"US","release_date":"2019-01-01","seconds":36000}]`
	_, err := Run(writeBooks(t, books), Options{DataDir: dataDir, ImportDate: testImportDate})
	if err == nil {
		t.Fatal("creating over an entry the loader dropped must fail, not overwrite it")
	}
	if !strings.Contains(err.Error(), "broken-book") || !strings.Contains(err.Error(), "works/0/0.json") {
		t.Errorf("error = %v, want it to name the slug and the pack holding it", err)
	}
	// The recording the entry held is still there.
	if recs := recSlugsOf(t, dataDir, "broken-book"); len(recs) != 1 || recs[0] != "a-reader-2019" {
		t.Errorf("recordings = %v, want the seeded a-reader-2019 intact", recs)
	}
	var work struct {
		Authors any `json:"authors"`
	}
	readEntity(t, dataDir, workAddress, &work)
	if work.Authors != "some-author" {
		t.Errorf("the undecodable work was rewritten: authors = %v", work.Authors)
	}
}

// The layout refusal gets a clause naming who refused it; an I/O failure must
// not. Opening the store now READS the tree, so it can fail for reasons that
// have nothing to do with the layout - and telling an operator whose data root
// is a stray file that "metaimport writes the pack layout only" would be a false
// explanation of it. (The failure also lands BEFORE anything is written, where a
// per-file writer used to write first and fail validation afterwards.)
func TestImportIOFailureIsNotReportedAsALayoutRefusal(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	if err := os.WriteFile(root, []byte("not a tree"), 0o644); err != nil {
		t.Fatal(err)
	}
	books := `[{"asin":"B0NOTREE01","title_short":"No Tree","author":"Some Author","narrated_by":"A Reader","language":"english","region":"US","seconds":600}]`
	_, err := Run(writeBooks(t, books), Options{DataDir: root, ImportDate: testImportDate})
	if err == nil {
		t.Fatal("an import against a data root that is a regular file must fail")
	}
	if errors.Is(err, pack.ErrLegacyLayout) {
		t.Errorf("an I/O failure was reported as a legacy-layout refusal: %v", err)
	}
	if strings.Contains(err.Error(), "pack layout only") {
		t.Errorf("error = %v, want no layout clause on an I/O failure", err)
	}
	if !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("error = %v, want it to say what actually went wrong", err)
	}
}
