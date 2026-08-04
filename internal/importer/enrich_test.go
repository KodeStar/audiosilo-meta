package importer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/check"
)

// The enrichment tests all run against one seeded catalogue: two works by one
// author, the first carrying a recording whose ASIN the export rows match, plus
// a series the first work is NOT yet a member of. Each test overrides the parts
// of the seed it is about.
const (
	seedPersonAuthor   = `{"id":"ada-mapmaker","license":"CC0-1.0","name":"Ada Mapmaker","sources":[{"type":"user"}]}`
	seedPersonNarrator = `{"id":"bea-reader","license":"CC0-1.0","name":"Bea Reader","sources":[{"type":"user"}]}`
	// The work under enrichment: no genres, so a row's genres can fill them.
	seedWork = `{"authors":["ada-mapmaker"],"id":"the-lost-cartographer","language":"en","license":"CC0-1.0","sources":[{"type":"user"}],"title":"The Lost Cartographer"}`
	// The recording under enrichment: identity only, every enrichable fact absent.
	seedRecording = `{"asin":[{"asin":"B0LIBEX001","region":"uk"}],"id":"bea-reader","language":"en","license":"CC0-1.0","narrators":["bea-reader"],"sources":[{"type":"user"}],"work":"the-lost-cartographer"}`
	// A sibling work, only so the seeded series can satisfy its minItems.
	seedSiblingWork = `{"authors":["ada-mapmaker"],"id":"the-second-map","language":"en","license":"CC0-1.0","sources":[{"type":"user"}],"title":"The Second Map"}`
	seedSeries      = `{"id":"cartographer-chronicles","license":"CC0-1.0","name":"Cartographer Chronicles","sources":[{"type":"user"}],"works":[{"position":"2","work":"the-second-map"}]}`
)

const (
	workRel   = "works/th/the-lost-cartographer/work.json"
	recRel    = "works/th/the-lost-cartographer/recordings/bea-reader.json"
	seriesRel = "series/ca/cartographer-chronicles.json"
)

// enrichSeedFiles is the base catalogue, with any overrides applied.
func enrichSeedFiles(overrides map[string]string) map[string]string {
	files := map[string]string{
		"people/ad/ada-mapmaker.json":       seedPersonAuthor,
		"people/be/bea-reader.json":         seedPersonNarrator,
		workRel:                             seedWork,
		recRel:                              seedRecording,
		"works/th/the-second-map/work.json": seedSiblingWork,
		seriesRel:                           seedSeries,
	}
	for rel, content := range overrides {
		files[rel] = content
	}
	return files
}

// seedEnrichTree writes the base catalogue (plus overrides) into a fresh temp
// data dir.
func seedEnrichTree(t *testing.T, overrides map[string]string) string {
	t.Helper()
	dataDir := t.TempDir()
	seedTree(t, dataDir, enrichSeedFiles(overrides))
	return dataDir
}

// runEnrich runs the libex importer in enrichment mode against dataDir.
func runEnrich(t *testing.T, dataDir, exportJSON string, dryRun bool) Summary {
	t.Helper()
	sum, err := RunLibex(writeBooks(t, exportJSON), Options{
		DataDir: dataDir, ImportDate: testImportDate, DryRun: dryRun, Mode: ModeEnrich,
	})
	if err != nil {
		t.Fatalf("enrich run: %v", err)
	}
	return sum
}

// fullRow is an export row stating every enrichable fact, matching the seeded
// recording's ASIN.
const fullRow = `[{
  "asin": "B0LIBEX001",
  "title": "The Lost Cartographer",
  "region": "gb",
  "publisher": "Lost Press",
  "isbn": "9781234567897",
  "language": "english",
  "bookFormat": "unabridged",
  "releaseDate": "2024-03-01T00:00:00Z",
  "imageUrl": "https://m.media-amazon.com/images/I/51libex0001.jpg",
  "lengthMinutes": 600,
  "authors": [{ "name": "Ada Mapmaker" }],
  "narrators": [{ "name": "Bea Reader" }],
  "genres": [{ "name": "Epic Fantasy" }, { "name": "Adventure" }],
  "series": [{ "name": "Cartographer Chronicles", "position": 1 }],
  "chapters": [
    { "title": "Prologue", "startOffsetMs": 0, "lengthMs": 60000 },
    { "title": "Chapter One", "startOffsetMs": 60000, "lengthMs": 120000 }
  ],
  "description": "A publisher blurb that must never be imported.",
  "rating": 4.87
}]`

// recordingFile is the recording fields the mode tests assert on - shared by
// the enrichment and recordings-only suites, which read the same file shape for
// different reasons. Description is here so a test can prove publisher copy
// never lands, not because the importer ever writes it.
type recordingFile struct {
	ID          string   `json:"id"`
	Work        string   `json:"work"`
	Narrators   []string `json:"narrators"`
	Abridged    *bool    `json:"abridged"`
	Language    string   `json:"language"`
	RuntimeMin  int      `json:"runtime_min"`
	ReleaseDate string   `json:"release_date"`
	Publisher   string   `json:"publisher"`
	CoverURL    string   `json:"cover_url"`
	ISBN        []string `json:"isbn"`
	ASIN        []struct {
		Region string `json:"region"`
		ASIN   string `json:"asin"`
	} `json:"asin"`
	Chapters []struct {
		Title    string `json:"title"`
		StartMS  int64  `json:"start_ms"`
		LengthMS int64  `json:"length_ms"`
	} `json:"chapters"`
	License string `json:"license"`
	Sources []struct {
		Type       string `json:"type"`
		Ref        string `json:"ref"`
		ImportedAt string `json:"imported_at"`
	} `json:"sources"`
	Description string `json:"description"`
}

type enrichedWork struct {
	Title    string   `json:"title"`
	Genres   []string `json:"genres"`
	Subtitle string   `json:"subtitle"`
	Sources  []struct {
		Type       string `json:"type"`
		Ref        string `json:"ref"`
		ImportedAt string `json:"imported_at"`
	} `json:"sources"`
}

// readRaw returns a record's exact JSON, compacted. It is the pack-layout
// answer to reading a record's file: the seed literals it is compared against
// are compact and key-sorted, which is the form a pack stores an entry in, so an
// unchanged record compares equal to the literal that seeded it.
func readRaw(t *testing.T, dataDir, address string) string {
	t.Helper()
	var buf bytes.Buffer
	if err := json.Compact(&buf, rawEntity(t, dataDir, address)); err != nil {
		t.Fatalf("compact %s: %v", address, err)
	}
	return buf.String()
}

func TestEnrichFillsAbsentFacts(t *testing.T) {
	dataDir := seedEnrichTree(t, nil)
	sum := runEnrich(t, dataDir, fullRow, false)

	if sum.EnrichedWorks != 1 || sum.EnrichedRecordings != 1 {
		t.Errorf("EnrichedWorks/EnrichedRecordings = %d/%d, want 1/1", sum.EnrichedWorks, sum.EnrichedRecordings)
	}
	if sum.SeriesPlacements != 1 {
		t.Errorf("SeriesPlacements = %d, want 1", sum.SeriesPlacements)
	}
	if sum.NotInCatalog != 0 {
		t.Errorf("NotInCatalog = %d, want 0", sum.NotInCatalog)
	}
	// Enrichment creates nothing, ever.
	if sum.NewWorks+sum.NewRecordings+sum.NewPeople+sum.NewSeries != 0 {
		t.Errorf("enrichment created records: %+v", sum)
	}
	if res := check.Load(dataDir); !res.OK() {
		t.Fatalf("enriched tree failed validation:\n%v", res.Problems)
	}

	var rec recordingFile
	readEntity(t, dataDir, recRel, &rec)
	if rec.RuntimeMin != 600 {
		t.Errorf("runtime_min = %d, want 600", rec.RuntimeMin)
	}
	if rec.ReleaseDate != "2024-03-01" {
		t.Errorf("release_date = %q, want 2024-03-01", rec.ReleaseDate)
	}
	if rec.Publisher != "Lost Press" {
		t.Errorf("publisher = %q", rec.Publisher)
	}
	if rec.CoverURL != "https://m.media-amazon.com/images/I/51libex0001.jpg" {
		t.Errorf("cover_url = %q", rec.CoverURL)
	}
	if !reflect.DeepEqual(rec.ISBN, []string{"9781234567897"}) {
		t.Errorf("isbn = %v", rec.ISBN)
	}
	if len(rec.Chapters) != 2 || rec.Chapters[0].StartMS != 0 || rec.Chapters[1].StartMS != 60000 {
		t.Errorf("chapters = %+v", rec.Chapters)
	}
	// Identity and the never-touched fields survive untouched.
	if len(rec.ASIN) != 1 || rec.ASIN[0].ASIN != "B0LIBEX001" || rec.ASIN[0].Region != "uk" {
		t.Errorf("asin[] must be untouched: %+v", rec.ASIN)
	}
	if rec.Abridged != nil {
		t.Errorf("abridged must never be filled by enrichment, got %v", *rec.Abridged)
	}
	if !reflect.DeepEqual(rec.Narrators, []string{"bea-reader"}) {
		t.Errorf("narrators must be untouched: %v", rec.Narrators)
	}
	if rec.Description != "" {
		t.Errorf("a publisher blurb leaked in: %q", rec.Description)
	}
	// Provenance: the original stamp kept, one enrichment stamp appended.
	if len(rec.Sources) != 2 || rec.Sources[0].Type != "user" ||
		rec.Sources[1].Type != "libex-import" || rec.Sources[1].Ref != "B0LIBEX001" ||
		rec.Sources[1].ImportedAt != testImportDate {
		t.Errorf("recording sources = %+v", rec.Sources)
	}

	var work enrichedWork
	readEntity(t, dataDir, workRel, &work)
	if !reflect.DeepEqual(work.Genres, []string{"action-adventure", "epic-fantasy"}) {
		t.Errorf("genres = %v, want [action-adventure epic-fantasy]", work.Genres)
	}
	if work.Title != "The Lost Cartographer" || work.Subtitle != "" {
		t.Errorf("work identity must be untouched: %+v", work)
	}
	if len(work.Sources) != 2 || work.Sources[1].Type != "libex-import" || work.Sources[1].Ref != "B0LIBEX001" {
		t.Errorf("work sources = %+v", work.Sources)
	}

	// The series gained the work at the row's position, with provenance.
	var series struct {
		Works []struct {
			Work     string `json:"work"`
			Position string `json:"position"`
		} `json:"works"`
		Sources []struct {
			Type string `json:"type"`
			Ref  string `json:"ref"`
		} `json:"sources"`
	}
	readEntity(t, dataDir, seriesRel, &series)
	if len(series.Works) != 2 {
		t.Fatalf("series works = %+v, want 2", series.Works)
	}
	found := false
	for _, sw := range series.Works {
		if sw.Work == "the-lost-cartographer" && sw.Position == "1" {
			found = true
		}
	}
	if !found {
		t.Errorf("work not placed at position 1: %+v", series.Works)
	}
	if len(series.Sources) != 2 || series.Sources[1].Type != "libex-import" {
		t.Errorf("series sources = %+v", series.Sources)
	}
}

func TestEnrichNeverOverwritesPresentFacts(t *testing.T) {
	// Every fact the row states is already recorded (with different values), so
	// nothing may change - not even the provenance.
	const presentRec = `{"asin":[{"asin":"B0LIBEX001","region":"uk"}],"chapters":[{"length_ms":90000,"start_ms":0,"title":"Only Chapter"}],"cover_url":"https://example.com/existing.jpg","id":"bea-reader","isbn":["9781234567897"],"language":"en","license":"CC0-1.0","narrators":["bea-reader"],"publisher":"Existing Press","release_date":"2024-03-01","runtime_min":590,"sources":[{"type":"user"}],"work":"the-lost-cartographer"}`
	const presentWork = `{"authors":["ada-mapmaker"],"genres":["science-fiction"],"id":"the-lost-cartographer","language":"en","license":"CC0-1.0","sources":[{"type":"user"}],"title":"The Lost Cartographer"}`
	// The work is already a member of the series it claims (at another position,
	// which enrichment must leave exactly as it is).
	const presentSeries = `{"id":"cartographer-chronicles","license":"CC0-1.0","name":"Cartographer Chronicles","sources":[{"type":"user"}],"works":[{"position":"2","work":"the-second-map"},{"position":"5","work":"the-lost-cartographer"}]}`
	dataDir := seedEnrichTree(t, map[string]string{recRel: presentRec, workRel: presentWork, seriesRel: presentSeries})

	sum := runEnrich(t, dataDir, fullRow, false)
	if sum.EnrichedWorks != 0 || sum.EnrichedRecordings != 0 || sum.SeriesPlacements != 0 {
		t.Errorf("present facts must not be rewritten: %+v", sum)
	}
	if got := readRaw(t, dataDir, recRel); got != presentRec {
		t.Errorf("recording changed:\n got %s\nwant %s", got, presentRec)
	}
	if got := readRaw(t, dataDir, workRel); got != presentWork {
		t.Errorf("work changed:\n got %s\nwant %s", got, presentWork)
	}
	if got := readRaw(t, dataDir, seriesRel); got != presentSeries {
		t.Errorf("series changed:\n got %s\nwant %s", got, presentSeries)
	}
	// 590 vs 600 is within the 10 percent tolerance, and the release date
	// matches, so neither conflict warning fires here.
	if len(sum.Warnings) != 0 {
		t.Errorf("unexpected warnings: %v", sum.Warnings)
	}
}

// TestEnrichSkipsAContradictedRowEntirely pins the load-bearing consequence of a
// runtime or release-date conflict: those two facts are how this code recognizes
// a DIFFERENT production, so the conflict is the run's own evidence that the
// ASIN sits on the wrong record - and the whole row is then dropped. Filling
// "the absent facts anyway" is not a smaller mistake: chapters are
// all-or-nothing and an ISBN is claimed globally, so existing-value-wins makes
// either fill permanently unrepairable by a later, correct row.
func TestEnrichSkipsAContradictedRowEntirely(t *testing.T) {
	cases := []struct {
		name    string
		rec     string
		warning string
	}{
		{
			name:    "runtime",
			rec:     `{"asin":[{"asin":"B0LIBEX001","region":"uk"}],"id":"bea-reader","language":"en","license":"CC0-1.0","narrators":["bea-reader"],"runtime_min":300,"sources":[{"type":"user"}],"work":"the-lost-cartographer"}`,
			warning: "runtime 600 min conflicts with the recorded 300 min; the row was not used for enrichment",
		},
		{
			name:    "release date",
			rec:     `{"asin":[{"asin":"B0LIBEX001","region":"uk"}],"id":"bea-reader","language":"en","license":"CC0-1.0","narrators":["bea-reader"],"release_date":"2019-11-05","sources":[{"type":"user"}],"work":"the-lost-cartographer"}`,
			warning: "release date 2024-03-01 conflicts with the recorded 2019-11-05; the row was not used for enrichment",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dataDir := seedEnrichTree(t, map[string]string{recRel: tc.rec})
			before := snapshotTree(t, dataDir)

			sum := runEnrich(t, dataDir, fullRow, false)
			if !hasWarning(sum.Warnings, tc.warning) {
				t.Errorf("expected %q, got %v", tc.warning, sum.Warnings)
			}
			if len(sum.Warnings) != 1 {
				t.Errorf("a contradicted row warns exactly once: %v", sum.Warnings)
			}
			// The row matched - it is just unusable, which is a different thing
			// from not being in the catalogue.
			if sum.Matched != 1 || sum.NotInCatalog != 0 {
				t.Errorf("Matched/NotInCatalog = %d/%d, want 1/0", sum.Matched, sum.NotInCatalog)
			}
			if sum.EnrichedRecordings != 0 || sum.EnrichedWorks != 0 || sum.SeriesPlacements != 0 {
				t.Errorf("a contradicted row must change nothing: %+v", sum)
			}
			// Nothing from the row landed anywhere: not the recording's absent
			// publisher/cover/chapters/ISBN, not the work's genres, not the
			// series placement the row claims. (The files are still the
			// hand-written seed, so they are read raw - readJSON would fail them
			// on canonical form.)
			assertTreeUnchanged(t, dataDir, before)
			gotRec := readRaw(t, dataDir, recRel)
			for _, field := range []string{"chapters", "isbn", "publisher", "cover_url"} {
				if strings.Contains(gotRec, `"`+field+`"`) {
					t.Errorf("a contradicted row filled %s: %s", field, gotRec)
				}
			}
			if got := readRaw(t, dataDir, workRel); strings.Contains(got, `"genres"`) {
				t.Errorf("a contradicted row filled work genres: %s", got)
			}
		})
	}
}

func TestEnrichDoesNotWarnOnALessPreciseRecordedDate(t *testing.T) {
	// The catalogue records release dates at whatever precision their source
	// stated. A recorded "2024" and a row's "2024-03-01" are the same date, so
	// the recorded value still wins but nothing is reported.
	const yearOnlyRec = `{"asin":[{"asin":"B0LIBEX001","region":"uk"}],"id":"bea-reader","language":"en","license":"CC0-1.0","narrators":["bea-reader"],"release_date":"2024","sources":[{"type":"user"}],"work":"the-lost-cartographer"}`
	dataDir := seedEnrichTree(t, map[string]string{recRel: yearOnlyRec})

	sum := runEnrich(t, dataDir, fullRow, false)
	if hasWarning(sum.Warnings, "release date") {
		t.Errorf("a precision difference is not a conflict: %v", sum.Warnings)
	}
	var rec recordingFile
	readEntity(t, dataDir, recRel, &rec)
	if rec.ReleaseDate != "2024" {
		t.Errorf("release_date = %q, want the recorded value kept", rec.ReleaseDate)
	}
}

func TestEnrichNeverReplacesChapters(t *testing.T) {
	const chapteredRec = `{"asin":[{"asin":"B0LIBEX001","region":"uk"}],"chapters":[{"length_ms":90000,"start_ms":0,"title":"Only Chapter"}],"id":"bea-reader","language":"en","license":"CC0-1.0","narrators":["bea-reader"],"sources":[{"type":"user"}],"work":"the-lost-cartographer"}`
	dataDir := seedEnrichTree(t, map[string]string{recRel: chapteredRec})

	runEnrich(t, dataDir, fullRow, false)
	var rec recordingFile
	readEntity(t, dataDir, recRel, &rec)
	if len(rec.Chapters) != 1 || rec.Chapters[0].Title != "Only Chapter" {
		t.Errorf("existing chapters must never be merged or replaced: %+v", rec.Chapters)
	}
}

// TestEnrichSeesARegionScopedISBNAsPresent is the dedup rule for the ISBN
// entry's second spelling. No importer ever WRITES it: it reaches the tree from
// a hand-authored pull request, or from the issue-form correction path, which
// contributes one entry at a time. Either way enrichISBNs reads the RAW entry,
// where such an ISBN is an object rather than a string, and a record somebody
// scoped that way is enriched by every later run.
// Without model.ISBNRefOf the object reads as absent and the row's
// identical ISBN looks absent, so the record's own identifier is offered to
// claimISBNs and comes back refused as "already recorded on another recording" -
// a collision reported against the very record being read. (The global claim set
// is what keeps the duplicate off disk; the false warning is the visible defect,
// and it is what this test pins.)
func TestEnrichSeesARegionScopedISBNAsPresent(t *testing.T) {
	const scopedRec = `{"asin":[{"asin":"B0LIBEX001","region":"uk"}],"id":"bea-reader","isbn":[{"isbn":"9781234567897","region":"uk"}],"language":"en","license":"CC0-1.0","narrators":["bea-reader"],"sources":[{"type":"user"}],"work":"the-lost-cartographer"}`
	dataDir := seedEnrichTree(t, map[string]string{recRel: scopedRec})

	sum := runEnrich(t, dataDir, fullRow, false)
	if hasWarning(sum.Warnings, "is already recorded on another recording") {
		t.Errorf("the record's OWN ISBN must not read as another recording's: %v", sum.Warnings)
	}

	var rec struct {
		ISBN []json.RawMessage `json:"isbn"`
	}
	readEntity(t, dataDir, recRel, &rec)
	if len(rec.ISBN) != 1 {
		t.Fatalf("isbn = %s, want the single region-scoped entry untouched", rec.ISBN)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, rec.ISBN[0]); err != nil {
		t.Fatalf("compact isbn[0]: %v", err)
	}
	if got := compact.String(); got != `{"isbn":"9781234567897","region":"uk"}` {
		t.Errorf("isbn[0] = %s, want the recorded object form unchanged", got)
	}
	if res := check.Load(dataDir); !res.OK() {
		t.Fatalf("enriched tree failed validation:\n%v", res.Problems)
	}
}

func TestEnrichISBNGlobalDuplicateGuard(t *testing.T) {
	// The row's ISBN is already recorded on ANOTHER recording, so adding it here
	// would break the global ISBN uniqueness rule.
	const otherRec = `{"id":"other","isbn":["9781234567897"],"language":"en","license":"CC0-1.0","narrators":["bea-reader"],"sources":[{"type":"user"}],"work":"the-second-map"}`
	dataDir := seedEnrichTree(t, map[string]string{
		"works/th/the-second-map/recordings/other.json": otherRec,
	})

	sum := runEnrich(t, dataDir, fullRow, false)
	if !hasWarning(sum.Warnings, "ISBN 9781234567897 is already recorded on another recording") {
		t.Errorf("expected an ISBN collision warning, got %v", sum.Warnings)
	}
	var rec recordingFile
	readEntity(t, dataDir, recRel, &rec)
	if len(rec.ISBN) != 0 {
		t.Errorf("a globally-claimed ISBN must not be added: %v", rec.ISBN)
	}
	if res := check.Load(dataDir); !res.OK() {
		t.Fatalf("tree failed validation:\n%v", res.Problems)
	}
}

// TestEnrichFindsSuffixedLongNameSeries is findSeries' own lock: enrichment is
// the only caller that MUST walk past candidate 0 to do anything (it never
// creates a series, so a walk that stops at the bare slug silently places
// nothing). The two series here have long names colliding on one bounded base,
// so the target only exists on the numeric candidate - and its slug is written
// out literally, independent of the production formula, so a chain that stops
// early or lands elsewhere fails rather than agrees with itself.
func TestEnrichFindsSuffixedLongNameSeries(t *testing.T) {
	prefix := strings.Repeat("Long ", 25)
	nameA, nameB := prefix+"Alpha", prefix+"Beta"
	const (
		slugA = "long-long-long-long-long-long-long-long-long-long-long-long-long-long-long-long-long-long-long-long" // 99 chars: the shared truncated base
		slugB = "long-long-long-long-long-long-long-long-long-long-long-long-long-long-long-long-long-long-long-2"    // the bounded numeric candidate
		relA  = "series/lo/" + slugA + ".json"
		relB  = "series/lo/" + slugB + ".json"
	)
	dataDir := seedEnrichTree(t, map[string]string{
		relA: fmt.Sprintf(`{"id":%q,"license":"CC0-1.0","name":%q,"sources":[{"type":"user"}],"works":[{"position":"1","work":"the-second-map"}]}`, slugA, nameA),
		relB: fmt.Sprintf(`{"id":%q,"license":"CC0-1.0","name":%q,"sources":[{"type":"user"}],"works":[{"position":"1","work":"the-second-map"}]}`, slugB, nameB),
	})
	row := fmt.Sprintf(`[{"asin":"B0LIBEX001","title":"The Lost Cartographer","region":"gb","language":"english",
	  "authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":"Bea Reader"}],
	  "series":[{"name":%q,"position":"2"}]}]`, nameB)

	sum := runEnrich(t, dataDir, row, false)
	if sum.SeriesPlacements != 1 || sum.NewSeries != 0 {
		t.Fatalf("SeriesPlacements/NewSeries = %d/%d, want 1/0 (the suffixed series must be found, not skipped): %v",
			sum.SeriesPlacements, sum.NewSeries, sum.Warnings)
	}
	var series struct {
		Works []struct{ Work, Position string } `json:"works"`
	}
	readEntity(t, dataDir, relB, &series)
	if len(series.Works) != 2 {
		t.Fatalf("the suffixed series holds %+v, want the enriched work added", series.Works)
	}
	if before := readRaw(t, dataDir, relA); !strings.Contains(before, `"position":"1"`) || strings.Contains(before, "the-lost-cartographer") {
		t.Errorf("the same-base series A was touched: %s", before)
	}
}

func TestEnrichSeriesOnlyExistingAndOnlyOnce(t *testing.T) {
	// Two claims: the seeded series (placeable) and one the catalogue does not
	// have (skipped silently - enrichment never creates a series).
	const row = `[{"asin":"B0LIBEX001","title":"The Lost Cartographer","region":"gb","language":"english",
	  "authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":"Bea Reader"}],
	  "series":[{"name":"Cartographer Chronicles","position":"1"},{"name":"Nonexistent Saga","position":"4"}]}]`
	dataDir := seedEnrichTree(t, nil)

	sum := runEnrich(t, dataDir, row, false)
	if sum.SeriesPlacements != 1 || sum.NewSeries != 0 {
		t.Errorf("SeriesPlacements/NewSeries = %d/%d, want 1/0", sum.SeriesPlacements, sum.NewSeries)
	}
	if entryExists(t, dataDir, "series/no/nonexistent-saga.json") {
		t.Error("enrichment created a series")
	}
	if len(sum.Warnings) != 0 {
		t.Errorf("an unknown series must be skipped silently: %v", sum.Warnings)
	}

	// Re-running with a DIFFERENT position leaves the existing membership alone:
	// re-positioning a catalogued work is a correction, not an enrichment.
	const moved = `[{"asin":"B0LIBEX001","title":"The Lost Cartographer","region":"gb","language":"english",
	  "authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":"Bea Reader"}],
	  "series":[{"name":"Cartographer Chronicles","position":"3"}]}]`
	before := readRaw(t, dataDir, seriesRel)
	sum2 := runEnrich(t, dataDir, moved, false)
	if sum2.SeriesPlacements != 0 {
		t.Errorf("an existing membership must be a no-op: %+v", sum2)
	}
	if after := readRaw(t, dataDir, seriesRel); after != before {
		t.Errorf("series rewritten:\n got %s\nwant %s", after, before)
	}
}

// TestEnrichSeriesInvalidPositionWarnOrder pins WHERE the invalid-position
// warning sits among enrichSeries' guards, which is the whole of its meaning: it
// fires only for a claim this run could otherwise have acted on. A claim on a
// series the catalogue does not hold is nothing to do with the position (nothing
// would be placed either way), and a work already in the series is settled, so
// reporting either would be noise on rows the run correctly ignored - and at
// export scale that noise is the report.
func TestEnrichSeriesInvalidPositionWarnOrder(t *testing.T) {
	row := func(series string) string {
		return `[{"asin":"B0LIBEX001","title":"The Lost Cartographer","region":"gb","language":"english",
		  "authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":"Bea Reader"}],
		  "series":[` + series + `]}]`
	}
	cases := []struct {
		name     string
		series   string
		override map[string]string
		wantWarn bool
	}{
		{
			// The series exists and the work is not a member: the position is the
			// only reason nothing happened, so say so.
			name:     "known series, not a member",
			series:   `{"name":"Cartographer Chronicles","position":"not a number"}`,
			wantWarn: true,
		},
		{
			name:   "unknown series",
			series: `{"name":"Nonexistent Saga","position":"not a number"}`,
		},
		{
			name:     "already a member",
			series:   `{"name":"Cartographer Chronicles","position":"not a number"}`,
			override: map[string]string{seriesRel: `{"id":"cartographer-chronicles","license":"CC0-1.0","name":"Cartographer Chronicles","sources":[{"type":"user"}],"works":[{"position":"2","work":"the-second-map"},{"position":"5","work":"the-lost-cartographer"}]}`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dataDir := seedEnrichTree(t, tc.override)
			sum := runEnrich(t, dataDir, row(tc.series), false)
			got := hasWarning(sum.Warnings, "missing or invalid position")
			if got != tc.wantWarn {
				t.Errorf("invalid-position warning = %v, want %v: %v", got, tc.wantWarn, sum.Warnings)
			}
			if sum.SeriesPlacements != 0 {
				t.Errorf("an invalid position must never place a work: %+v", sum)
			}
		})
	}
}

// TestEnrichAggregatesParseWarningsAndReconciles pins the two halves of a run's
// honesty at export scale. (1) The parse layer's per-row lines are folded into
// one line per CLASS: an enrichment input is overwhelmingly rows this catalogue
// will never match, so per-row warnings about them would bury the run's real
// output under six figures of noise. (2) Every row read is still accounted for -
// matched + not-in-catalogue + parse-skipped - so aggregating can never turn
// into losing count of them.
func TestEnrichAggregatesParseWarningsAndReconciles(t *testing.T) {
	const rows = `[
	  {"asin":"B0LIBEX001","title":"The Lost Cartographer","region":"gb","language":"english",
	   "authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":"Bea Reader"}],
	   "publisher":"Lost Press","imageUrl":"http://insecure.example.com/a.jpg","isbn":"not-an-isbn"},
	  {"asin":"B0UNKNOWN1","title":"Unmatched One","region":"us","language":"english",
	   "imageUrl":"http://insecure.example.com/b.jpg","isbn":"also-bad"},
	  {"asin":"B0UNKNOWN2","title":"Unmatched Two","region":"us","language":"english"},
	  {"asin":"not-an-asin","title":"No ASIN One","region":"us"},
	  {"asin":"","title":"No ASIN Two","region":"us"},
	  {"asin":"B0LIBEX0ZZ","title":"Nowhere","region":"atlantis"}
	]`
	dataDir := seedEnrichTree(t, nil)
	sum := runEnrich(t, dataDir, rows, false)

	if sum.Matched != 1 || sum.NotInCatalog != 2 || sum.SkippedRows != 3 {
		t.Errorf("Matched/NotInCatalog/SkippedRows = %d/%d/%d, want 1/2/3", sum.Matched, sum.NotInCatalog, sum.SkippedRows)
	}
	if total := sum.Matched + sum.NotInCatalog + sum.SkippedRows; total != 6 {
		t.Errorf("the six rows read must all be accounted for, got %d", total)
	}
	// One line per class, with counts - not one per row.
	for _, want := range []string{
		"libex: 2 rows skipped: no well-formed ASIN",
		"libex: 1 rows skipped: region is not a known marketplace",
		"libex: 2 cover URLs were not https; dropped",
		"libex: 2 ISBNs were malformed; dropped",
	} {
		if !hasWarning(sum.Warnings, want) {
			t.Errorf("missing aggregated warning %q: %v", want, sum.Warnings)
		}
	}
	// The examples name concrete rows, so a maintainer can go and look.
	if !hasWarning(sum.Warnings, "(for example: B0LIBEX001, B0UNKNOWN1)") {
		t.Errorf("aggregated lines should carry examples: %v", sum.Warnings)
	}
	// Create mode is the opposite trade: a curated tranche is small and its
	// contributor acts on the per-row detail.
	created, err := RunLibex(writeBooks(t, rows), Options{DataDir: seedEnrichTree(t, nil), ImportDate: testImportDate, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if !hasWarning(created.Warnings, `row "No ASIN One" has no well-formed ASIN`) {
		t.Errorf("create mode must keep its per-row warnings: %v", created.Warnings)
	}
	if hasWarning(created.Warnings, "rows skipped: no well-formed ASIN") {
		t.Errorf("create mode must not aggregate: %v", created.Warnings)
	}
	if created.SkippedRows != 3 {
		t.Errorf("SkippedRows is counted in both modes, got %d", created.SkippedRows)
	}
}

// TestEnrichReportsAnASINOnTwoRecordings pins the visible failure mode of
// asinLoc's bare-ASIN key. Uniqueness upstream is (region, ASIN), so two
// recordings could legally carry the same ASIN string in different marketplaces
// while an export row states only the bare one. No such pair exists today; if
// one appears, the first recording keeps the match deterministically and the
// collision is REPORTED rather than silently decided by load order.
func TestEnrichReportsAnASINOnTwoRecordings(t *testing.T) {
	// The seeded recording holds B0LIBEX001 in uk; this one holds it in us.
	const twinRec = `{"asin":[{"asin":"B0LIBEX001","region":"us"}],"id":"twin","language":"en","license":"CC0-1.0","narrators":["bea-reader"],"sources":[{"type":"user"}],"work":"the-second-map"}`
	dataDir := seedEnrichTree(t, map[string]string{"works/th/the-second-map/recordings/twin.json": twinRec})

	sum := runEnrich(t, dataDir, fullRow, false)
	if !hasWarning(sum.Warnings, "ASIN B0LIBEX001 is recorded on both") {
		t.Errorf("expected a duplicate-ASIN report, got %v", sum.Warnings)
	}
	// Exactly one recording was enriched - the first, not both and not the last
	// one loaded.
	if sum.EnrichedRecordings != 1 {
		t.Errorf("EnrichedRecordings = %d, want 1", sum.EnrichedRecordings)
	}
	if got := readRaw(t, dataDir, "works/th/the-second-map/recordings/twin.json"); got != twinRec {
		t.Errorf("the second claimant must be untouched:\n got %s\nwant %s", got, twinRec)
	}
	var rec recordingFile
	readEntity(t, dataDir, recRel, &rec)
	if rec.Publisher != "Lost Press" {
		t.Errorf("the first claimant should have been enriched: %+v", rec)
	}
}

// TestEnrichUnknownASINIsCountedAndIgnored pins that enrichment matches by
// IDENTIFIER only: a row the catalogue has no ASIN for is counted and ignored,
// and no other resemblance can substitute for the match.
func TestEnrichUnknownASINIsCountedAndIgnored(t *testing.T) {
	cases := []struct {
		name string
		row  string
	}{
		{
			// Nothing about the row relates to the catalogue.
			name: "unrelated book",
			row: `[{"asin":"B0UNKNOWN1","title":"Some Other Book","region":"us","language":"english",
			  "publisher":"Lost Press","lengthMinutes":123,
			  "authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":"Bea Reader"}]}]`,
		},
		{
			// The ASIN is unknown but the title/author/narrator match a catalogued
			// work exactly. Create mode would mint a recording (and a work on a
			// slug collision); enrichment still does nothing.
			name: "exact title match",
			row: `[{"asin":"B0UNKNOWN2","title":"The Lost Cartographer","region":"gb","language":"english",
			  "lengthMinutes":600,"publisher":"Lost Press",
			  "authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":"Bea Reader"}],
			  "genres":[{"name":"Epic Fantasy"}],
			  "series":[{"name":"Cartographer Chronicles","position":"1"}]}]`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dataDir := seedEnrichTree(t, nil)
			before := snapshotTree(t, dataDir)

			sum := runEnrich(t, dataDir, tc.row, false)
			if sum.NotInCatalog != 1 || sum.Matched != 0 {
				t.Errorf("NotInCatalog/Matched = %d/%d, want 1/0", sum.NotInCatalog, sum.Matched)
			}
			if sum.EnrichedWorks+sum.EnrichedRecordings+sum.SeriesPlacements != 0 {
				t.Errorf("an unmatched row must change nothing: %+v", sum)
			}
			if sum.NewWorks+sum.NewRecordings+sum.NewPeople+sum.NewSeries != 0 {
				t.Errorf("enrichment created records: %+v", sum)
			}
			assertTreeUnchanged(t, dataDir, before)
		})
	}
}

func TestEnrichIsIdempotent(t *testing.T) {
	dataDir := seedEnrichTree(t, nil)
	if sum := runEnrich(t, dataDir, fullRow, false); sum.EnrichedRecordings != 1 {
		t.Fatalf("first run did not enrich: %+v", sum)
	}
	after := snapshotTree(t, dataDir)

	sum := runEnrich(t, dataDir, fullRow, false)
	if sum.EnrichedWorks+sum.EnrichedRecordings+sum.SeriesPlacements != 0 {
		t.Errorf("second identical run changed records: %+v", sum)
	}
	// Byte-identical AND untouched on disk: an unchanged input queues no writes.
	assertTreeUnchanged(t, dataDir, after)

	// And provenance was stamped exactly once per record.
	var rec recordingFile
	readEntity(t, dataDir, recRel, &rec)
	libexStamps := 0
	for _, s := range rec.Sources {
		if s.Type == "libex-import" {
			libexStamps++
		}
	}
	if libexStamps != 1 {
		t.Errorf("libex-import stamps = %d, want exactly 1: %+v", libexStamps, rec.Sources)
	}
}

func TestEnrichDryRunWritesNothing(t *testing.T) {
	dataDir := seedEnrichTree(t, nil)
	before := snapshotTree(t, dataDir)

	sum := runEnrich(t, dataDir, fullRow, true)
	if sum.EnrichedWorks != 1 || sum.EnrichedRecordings != 1 || sum.SeriesPlacements != 1 {
		t.Errorf("dry run should still compute the plan: %+v", sum)
	}
	assertTreeUnchanged(t, dataDir, before)
}

func TestEnrichComposesRowsTouchingOneRecording(t *testing.T) {
	// One recording, two marketplace ASINs, two rows - each stating a different
	// absent fact. The second row must read the first row's QUEUED write, not the
	// unmodified file on disk, or it would silently drop the first row's edit.
	const twoASINRec = `{"asin":[{"asin":"B0LIBEX001","region":"uk"},{"asin":"B0LIBEX00A","region":"us"}],"id":"bea-reader","language":"en","license":"CC0-1.0","narrators":["bea-reader"],"sources":[{"type":"user"}],"work":"the-lost-cartographer"}`
	dataDir := seedEnrichTree(t, map[string]string{recRel: twoASINRec})

	const rows = `[
	  {"asin":"B0LIBEX001","title":"The Lost Cartographer","region":"gb","language":"english",
	   "authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":"Bea Reader"}],
	   "publisher":"Lost Press","lengthMinutes":600},
	  {"asin":"B0LIBEX00A","title":"The Lost Cartographer","region":"us","language":"english",
	   "authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":"Bea Reader"}],
	   "imageUrl":"https://m.media-amazon.com/images/I/51libex0001.jpg","isbn":"9781234567897"}
	]`
	sum := runEnrich(t, dataDir, rows, false)
	if sum.EnrichedRecordings != 2 {
		t.Errorf("EnrichedRecordings = %d, want 2 (one per changing row)", sum.EnrichedRecordings)
	}

	var rec recordingFile
	readEntity(t, dataDir, recRel, &rec)
	if rec.Publisher != "Lost Press" || rec.RuntimeMin != 600 {
		t.Errorf("the first row's facts were lost: %+v", rec)
	}
	if rec.CoverURL == "" || len(rec.ISBN) != 1 {
		t.Errorf("the second row's facts were lost: %+v", rec)
	}
	if len(rec.ASIN) != 2 {
		t.Errorf("asin[] must be untouched: %+v", rec.ASIN)
	}
	// One stamp per contributing row, both distinct refs.
	if len(rec.Sources) != 3 || rec.Sources[1].Ref != "B0LIBEX001" || rec.Sources[2].Ref != "B0LIBEX00A" {
		t.Errorf("recording sources = %+v", rec.Sources)
	}
	if res := check.Load(dataDir); !res.OK() {
		t.Fatalf("enriched tree failed validation:\n%v", res.Problems)
	}
}

func TestEnrichIsSourceAgnosticInTheCore(t *testing.T) {
	// The mode lives in the shared core, so it is source-agnostic: an
	// OpenAudible-shaped export enriches the same way (the CLI is what limits
	// --enrich to the source whose operator permits the pass).
	dataDir := seedEnrichTree(t, nil)
	books := `[{"asin":"B0LIBEX001","title_short":"The Lost Cartographer","author":"Ada Mapmaker","narrated_by":"Bea Reader","language":"english","region":"uk","publisher":"Lost Press"}]`
	sum, err := Run(writeBooks(t, books), Options{DataDir: dataDir, ImportDate: testImportDate, Mode: ModeEnrich})
	if err != nil {
		t.Fatal(err)
	}
	if sum.EnrichedRecordings != 1 || sum.NewRecordings != 0 {
		t.Errorf("summary = %+v", sum)
	}
}

// assertTreeUnchanged proves a run touched nothing: the same file set, the same
// bytes, and the same modification times (so a queued write that happened to
// produce identical bytes is still a failure - an unchanged input must queue no
// write at all).
func assertTreeUnchanged(t *testing.T, dataDir string, before treeSnapshot) {
	t.Helper()
	after := snapshotTree(t, dataDir)
	if len(after) != len(before) {
		t.Fatalf("file set changed: %d files, want %d", len(after), len(before))
	}
	for rel, want := range before {
		got, present := after[rel]
		switch {
		case !present:
			t.Errorf("%s disappeared", rel)
		case got.content != want.content:
			t.Errorf("%s changed:\n got %s\nwant %s", rel, got.content, want.content)
		case got.modTime != want.modTime:
			t.Errorf("%s was rewritten with identical bytes (a queued write that should not exist)", rel)
		}
	}
}

// TestEnrichCreateModeStillSkipsAKnownASIN pins the two modes as disjoint: the
// very row enrichment fills facts from is a plain SKIP in create mode, so a
// normal import never backfills an existing record as a side effect.
func TestEnrichCreateModeStillSkipsAKnownASIN(t *testing.T) {
	dataDir := seedEnrichTree(t, nil)
	before := snapshotTree(t, dataDir)

	sum, err := RunLibex(writeBooks(t, fullRow), Options{DataDir: dataDir, ImportDate: testImportDate})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Skipped != 1 {
		t.Errorf("create mode must skip a known ASIN: %+v", sum)
	}
	assertTreeUnchanged(t, dataDir, before)
}
