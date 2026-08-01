package importer

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/check"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// runLibex runs the libex importer against a fresh empty data dir.
func runLibex(t *testing.T, exportJSON string, dryRun bool) (Summary, string) {
	t.Helper()
	return runWith(t, RunLibex, exportJSON, dryRun)
}

// libexFixture reads the export fixture, first asserting the invariant every
// test that uses it depends on (see requireFixtureNodesUnmapped).
func libexFixture(t *testing.T) string {
	t.Helper()
	requireFixtureNodesUnmapped(t)
	raw, err := os.ReadFile("testdata/libex_export.json")
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// fixtureGenreNodes are the browse-node ids the export fixture uses. They are
// deliberately NOT in the mapping table, so the fixture exercises the by_name
// half of the lookup (by-node precedence is unit-tested against a synthetic
// table in audiblegenres_test.go). This guard fails loudly if a table
// regeneration ever claims one of them, which would silently change what the
// fixture proves.
var fixtureGenreNodes = []string{"99900000001", "99900000002", "99900000003"}

func requireFixtureNodesUnmapped(t *testing.T) {
	t.Helper()
	for _, node := range fixtureGenreNodes {
		if g, ok := audibleGenreTable().ByASIN[node]; ok {
			t.Fatalf("fixture node id %s now maps to %q in audiblegenres.json; pick another for testdata/libex_export.json", node, g)
		}
	}
}

func TestLibexImportBasic(t *testing.T) {
	sum, dataDir := runLibex(t, libexFixture(t), false)

	if sum.NewWorks != 2 || sum.NewRecordings != 2 {
		t.Errorf("NewWorks/NewRecordings = %d/%d, want 2/2", sum.NewWorks, sum.NewRecordings)
	}
	// Ada Mapmaker + Bea Reader (shared by both books) + the first book's
	// role-qualified translator credit.
	if sum.NewPeople != 3 {
		t.Errorf("NewPeople = %d, want 3", sum.NewPeople)
	}
	if sum.NewSeries != 1 {
		t.Errorf("NewSeries = %d, want 1", sum.NewSeries)
	}

	if res := check.Load(dataDir); !res.OK() {
		t.Fatalf("imported tree failed validation:\n%v", res.Problems)
	}

	var work struct {
		Title    string         `json:"title"`
		Subtitle string         `json:"subtitle"`
		Authors  []string       `json:"authors"`
		Credits  []model.Credit `json:"credits"`
		Language string         `json:"language"`
		Genres   []string       `json:"genres"`
		Sources  []struct {
			Type       string `json:"type"`
			Ref        string `json:"ref"`
			ImportedAt string `json:"imported_at"`
		} `json:"sources"`
	}
	readEntity(t, dataDir, "works/th/the-lost-cartographer/work.json", &work)

	// The subtitle is never a stored fact of its own. It composes the full title
	// that distinguishes same-titled volumes, so it CAN end up inside work.title
	// on a work that disambiguation renamed (TestLibexSubtitleDisambiguates) -
	// but this book's title is unambiguous, so the work keeps the short title.
	if work.Title != "The Lost Cartographer" {
		t.Errorf("work title = %q, want %q (subtitle must not be folded in)", work.Title, "The Lost Cartographer")
	}
	if work.Subtitle != "" {
		t.Errorf("subtitle leaked onto the work: %q", work.Subtitle)
	}
	if work.Language != "en" {
		t.Errorf("language = %q, want en", work.Language)
	}
	// End-to-end credit hygiene: the export's doubled role qualifier ("Dan
	// Veksler - Translator - translator") is stripped down to the bare name
	// before the person slug is derived, so the tranche cannot mint a
	// "dan-veksler-translator-translator" person.
	if !reflect.DeepEqual(work.Authors, []string{"ada-mapmaker", "dan-veksler"}) {
		t.Errorf("authors = %v, want [ada-mapmaker dan-veksler]", work.Authors)
	}
	var translator struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	readEntity(t, dataDir, "people/da/dan-veksler.json", &translator)
	if translator.ID != "dan-veksler" || translator.Name != "Dan Veksler" {
		t.Errorf("translator person = %+v, want dan-veksler / %q", translator, "Dan Veksler")
	}
	// The stripped qualifier is no longer discarded: it is recorded as the work's
	// contributor credit, while the person keeps their place in authors above.
	wantCredits := []model.Credit{{Person: "dan-veksler", Role: model.RoleTranslator}}
	if !reflect.DeepEqual(work.Credits, wantCredits) {
		t.Errorf("credits = %+v, want %+v", work.Credits, wantCredits)
	}
	if sum.Credits != 1 {
		t.Errorf("Summary.Credits = %d, want 1", sum.Credits)
	}
	// Mapped onto the project vocabulary and SORTED ("Epic Fantasy" came first in
	// the export, action-adventure sorts before it); the unmapped string is gone.
	if !reflect.DeepEqual(work.Genres, []string{"action-adventure", "epic-fantasy"}) {
		t.Errorf("genres = %v, want [action-adventure epic-fantasy]", work.Genres)
	}
	if len(work.Sources) != 1 || work.Sources[0].Type != "libex-import" ||
		work.Sources[0].Ref != "B0LIBEX001" || work.Sources[0].ImportedAt != testImportDate {
		t.Errorf("work sources = %+v", work.Sources)
	}

	var rec struct {
		Narrators   []string `json:"narrators"`
		Abridged    *bool    `json:"abridged"`
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
	}
	readEntity(t, dataDir, "works/th/the-lost-cartographer/recordings/bea-reader-2024.json", &rec)

	if !reflect.DeepEqual(rec.Narrators, []string{"bea-reader"}) {
		t.Errorf("narrators = %v", rec.Narrators)
	}
	if rec.Abridged == nil || *rec.Abridged != false {
		t.Errorf("abridged = %v, want explicit false from bookFormat unabridged", rec.Abridged)
	}
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
	// The gb alias resolves to the uk marketplace rather than dropping the ASIN.
	if len(rec.ASIN) != 1 || rec.ASIN[0].Region != "uk" || rec.ASIN[0].ASIN != "B0LIBEX001" {
		t.Errorf("asin = %+v, want uk/B0LIBEX001", rec.ASIN)
	}
	if !reflect.DeepEqual(rec.ISBN, []string{"9781234567897"}) {
		t.Errorf("isbn = %v, want [9781234567897]", rec.ISBN)
	}
	if len(rec.Chapters) != 2 || rec.Chapters[0].Title != "Prologue" ||
		rec.Chapters[1].StartMS != 60000 || rec.Chapters[1].LengthMS != 120000 {
		t.Errorf("chapters = %+v", rec.Chapters)
	}

	// bookFormat "abridged" is a statement of fact on the second book.
	var rec2 struct {
		Abridged *bool    `json:"abridged"`
		ISBN     []string `json:"isbn"`
	}
	readEntity(t, dataDir, "works/th/the-second-map/recordings/bea-reader-2025.json", &rec2)
	if rec2.Abridged == nil || *rec2.Abridged != true {
		t.Errorf("second book abridged = %v, want explicit true", rec2.Abridged)
	}
	// Its ISBN duplicates the first book's, so it is dropped (globally unique).
	if len(rec2.ISBN) != 0 {
		t.Errorf("duplicate ISBN was emitted: %v", rec2.ISBN)
	}

	// A numeric position and a string position both place the volume.
	var series struct {
		Name  string `json:"name"`
		Works []struct {
			Work     string `json:"work"`
			Position string `json:"position"`
		} `json:"works"`
	}
	readEntity(t, dataDir, "series/ca/cartographer-chronicles.json", &series)
	if series.Name != "Cartographer Chronicles" || len(series.Works) != 2 {
		t.Fatalf("series = %+v", series)
	}
	got := map[string]string{}
	for _, sw := range series.Works {
		got[sw.Work] = sw.Position
	}
	if got["the-lost-cartographer"] != "1" || got["the-second-map"] != "2" {
		t.Errorf("series placements = %v", got)
	}
}

func TestLibexWarnings(t *testing.T) {
	sum, _ := runLibex(t, libexFixture(t), false)

	// Exactly three: the ASIN-less row, the duplicate ISBN, and ONE aggregated
	// unmapped-genre line (the same unmapped string appears on two books).
	if len(sum.Warnings) != 3 {
		t.Fatalf("warnings = %v, want 3", sum.Warnings)
	}
	if !hasWarning(sum.Warnings, "No Identity") || !hasWarning(sum.Warnings, "no well-formed ASIN") {
		t.Errorf("expected a malformed-ASIN warning naming the row, got %v", sum.Warnings)
	}
	if !hasWarning(sum.Warnings, "ISBN 9781234567897 is already recorded") {
		t.Errorf("expected a duplicate-ISBN warning, got %v", sum.Warnings)
	}
	unmapped := 0
	for _, w := range sum.Warnings {
		if strings.Contains(w, "unmapped genre strings:") {
			unmapped++
			if strings.Count(w, "Totally Made Up Category") != 1 {
				t.Errorf("unmapped string reported more than once: %q", w)
			}
		}
	}
	if unmapped != 1 {
		t.Errorf("expected exactly one unmapped-genre warning, got %v", sum.Warnings)
	}
}

func TestLibexDropsNonFactualFields(t *testing.T) {
	_, dataDir := runLibex(t, libexFixture(t), false)

	// Nothing copyrighted, editorial, or unmappable may reach the tree: the
	// publisher blurb/summary, the rating, the copyright line, the retailer's raw
	// genre strings, and the ASIN-less row's title.
	forbidden := []string{
		"spoilery", "Another blurb", "4.87", "(P) 2024 Mapmaker Media",
		"Epic Fantasy", "Adventure", "Totally Made Up Category", "No Identity",
		"A Tale of Maps",
	}
	var leaked []string
	_ = filepath.Walk(dataDir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		raw, _ := os.ReadFile(p)
		for _, bad := range forbidden {
			if strings.Contains(string(raw), bad) {
				leaked = append(leaked, bad+" in "+p)
			}
		}
		return nil
	})
	if len(leaked) != 0 {
		t.Errorf("non-factual fields leaked into the tree: %v", leaked)
	}
}

func TestLibexDryRunWritesNothing(t *testing.T) {
	sum, dataDir := runLibex(t, libexFixture(t), true)
	if sum.NewWorks != 2 || sum.NewRecordings != 2 {
		t.Errorf("dry run should still compute the plan: %+v", sum)
	}
	entries, _ := os.ReadDir(dataDir)
	if len(entries) != 0 {
		t.Errorf("dry run wrote files: %v", entries)
	}
}

// TestLibexGenresOnlyOnNewWorks pins the create-only posture: a work already in
// the catalogue is never modified by a normal import, so its file is unchanged
// even when the incoming row carries mappable genres. (Backfilling absent facts
// onto catalogued records is the separate enrichment mode's job.)
func TestLibexGenresOnlyOnNewWorks(t *testing.T) {
	dataDir := t.TempDir()
	const workAddress = "works/th/the-lost-cartographer/work.json"
	seed := map[string]string{
		"people/ad/ada-mapmaker.json": `{"id":"ada-mapmaker","license":"CC0-1.0","name":"Ada Mapmaker","sources":[{"type":"user"}]}`,
		workAddress:                   `{"authors":["ada-mapmaker"],"id":"the-lost-cartographer","language":"en","license":"CC0-1.0","sources":[{"type":"user"}],"title":"The Lost Cartographer"}`,
		"works/th/the-lost-cartographer/recordings/existing.json": `{"asin":[{"asin":"B0OLDREC01","region":"us"}],"id":"existing","language":"en","license":"CC0-1.0","narrators":["ada-mapmaker"],"sources":[{"type":"user"}],"work":"the-lost-cartographer"}`,
	}
	seedTree(t, dataDir, seed)

	export := `[{"asin":"B0LIBEX001","title":"The Lost Cartographer","region":"us","language":"english",` +
		`"authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":"Bea Reader"}],"lengthMinutes":600,` +
		`"genres":[{"name":"Epic Fantasy"}]}]`
	sum, err := RunLibex(writeBooks(t, export), Options{DataDir: dataDir, ImportDate: testImportDate})
	if err != nil {
		t.Fatal(err)
	}
	if sum.NewWorks != 0 || sum.NewRecordings != 1 {
		t.Errorf("expected a new recording under the existing work: %+v", sum)
	}
	assertEntryUnchanged(t, dataDir, workAddress, seed)
}

func TestLibexDedupByASIN(t *testing.T) {
	dataDir := t.TempDir()
	seedTree(t, dataDir, map[string]string{
		"people/ad/ada-mapmaker.json":                             `{"id":"ada-mapmaker","license":"CC0-1.0","name":"Ada Mapmaker","sources":[{"type":"user"}]}`,
		"people/be/bea-reader.json":                               `{"id":"bea-reader","license":"CC0-1.0","name":"Bea Reader","sources":[{"type":"user"}]}`,
		"works/th/the-lost-cartographer/work.json":                `{"authors":["ada-mapmaker"],"id":"the-lost-cartographer","language":"en","license":"CC0-1.0","sources":[{"type":"user"}],"title":"The Lost Cartographer"}`,
		"works/th/the-lost-cartographer/recordings/existing.json": `{"asin":[{"asin":"B0LIBEX001","region":"uk"}],"id":"existing","isbn":["9781234567897"],"language":"en","license":"CC0-1.0","narrators":["bea-reader"],"sources":[{"type":"user"}],"work":"the-lost-cartographer"}`,
	})

	export := `[{"asin":"B0LIBEX001","title":"The Lost Cartographer","region":"gb","language":"english",` +
		`"authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":"Bea Reader"}],"lengthMinutes":600}]`
	sum, err := RunLibex(writeBooks(t, export), Options{DataDir: dataDir, ImportDate: testImportDate})
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

// TestLibexDiskISBNBlocksDuplicate covers the loadExisting ISBN seed: an ISBN
// already on a recording ON DISK must not be re-emitted (checkUniqueness would
// fail the whole tree).
func TestLibexDiskISBNBlocksDuplicate(t *testing.T) {
	dataDir := t.TempDir()
	seedTree(t, dataDir, map[string]string{
		"people/ad/ada-mapmaker.json":              `{"id":"ada-mapmaker","license":"CC0-1.0","name":"Ada Mapmaker","sources":[{"type":"user"}]}`,
		"people/ca/carl-voice.json":                `{"id":"carl-voice","license":"CC0-1.0","name":"Carl Voice","sources":[{"type":"user"}]}`,
		"works/ol/older-book/work.json":            `{"authors":["ada-mapmaker"],"id":"older-book","language":"en","license":"CC0-1.0","sources":[{"type":"user"}],"title":"Older Book"}`,
		"works/ol/older-book/recordings/only.json": `{"id":"only","isbn":["9781234567897"],"language":"en","license":"CC0-1.0","narrators":["carl-voice"],"sources":[{"type":"user"}],"work":"older-book"}`,
	})

	export := `[{"asin":"B0LIBEX009","title":"Brand New Book","region":"us","language":"english","isbn":"9781234567897",` +
		`"authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":"Bea Reader"}],"lengthMinutes":600}]`
	sum, err := RunLibex(writeBooks(t, export), Options{DataDir: dataDir, ImportDate: testImportDate})
	if err != nil {
		t.Fatalf("import must still succeed (and validate): %v", err)
	}
	if !hasWarning(sum.Warnings, "ISBN 9781234567897 is already recorded") {
		t.Errorf("expected a duplicate-ISBN warning, got %v", sum.Warnings)
	}
	var rec struct {
		ISBN []string `json:"isbn"`
	}
	readEntity(t, dataDir, "works/br/brand-new-book/recordings/bea-reader.json", &rec)
	if len(rec.ISBN) != 0 {
		t.Errorf("isbn = %v, want none (already on disk)", rec.ISBN)
	}
}

func TestLibexMalformedISBNDropped(t *testing.T) {
	export := `[{"asin":"B0LIBEX010","title":"Bad ISBN","region":"us","language":"english","isbn":["123","9781234567897"],` +
		`"authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":"Bea Reader"}],"lengthMinutes":600}]`
	sum, dataDir := runLibex(t, export, false)
	if !hasWarning(sum.Warnings, `ISBN "123" is malformed`) {
		t.Errorf("expected a malformed-ISBN warning, got %v", sum.Warnings)
	}
	var rec struct {
		ISBN []string `json:"isbn"`
	}
	readEntity(t, dataDir, "works/ba/bad-isbn/recordings/bea-reader.json", &rec)
	if !reflect.DeepEqual(rec.ISBN, []string{"9781234567897"}) {
		t.Errorf("isbn = %v, want only the well-formed one", rec.ISBN)
	}
}

// TestLibexParserShapes covers the file shapes parseLibex sniffs: a top-level
// array, NDJSON (one row per line), and a wrapper object - each also with a
// UTF-8 BOM, which a Windows-side export tool prepends and which used to make
// the shape sniff fail on the first byte.
func TestLibexParserShapes(t *testing.T) {
	row1 := `{"asin":"B0LIBEX011","title":"Row One","region":"us","language":"english","authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":"Bea Reader"}],"lengthMinutes":600}`
	row2 := `{"asin":"B0LIBEX012","title":"Row Two","region":"us","language":"english","authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":"Bea Reader"}],"lengthMinutes":600}`
	const bom = "\xEF\xBB\xBF"
	cases := []struct {
		name  string
		input string
		want  int
	}{
		{"array", "[" + row1 + "," + row2 + "]", 2},
		{"ndjson", row1 + "\n" + row2 + "\n", 2},
		{"ndjson single row", row1, 1},
		{"wrapper object", `{"books":[` + row1 + "," + row2 + `]}`, 2},
		{"bom + array", bom + "[" + row1 + "," + row2 + "]", 2},
		{"bom + ndjson", bom + row1 + "\n" + row2 + "\n", 2},
		{"bom + wrapper object", bom + `{"books":[` + row1 + "," + row2 + `]}`, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parsed, err := parseLibex([]byte(tc.input))
			if err != nil {
				t.Fatalf("parseLibex: %v", err)
			}
			if len(parsed.warnings) != 0 || parsed.skipped != 0 {
				t.Errorf("unexpected warnings/skips: %+v", parsed.warnings)
			}
			if len(parsed.books) != tc.want {
				t.Fatalf("parsed %d books, want %d", len(parsed.books), tc.want)
			}
			if parsed.books[0].str("title_short") != "Row One" {
				t.Errorf("first book title_short = %q", parsed.books[0].str("title_short"))
			}
		})
	}

	if _, err := parseLibex([]byte("   ")); err == nil {
		t.Error("empty input should fail loud")
	}
	if _, err := parseLibex([]byte(`"not a book"`)); err == nil {
		t.Error("a non-object/array root should fail loud")
	}
}

// TestLibexParserRefusesAmbiguousFiles pins the two ways a valid-LOOKING file
// used to import silently wrong: a wrapper under a key we do not recognize
// (decoded as one row with no ASIN - "0 books imported", exit 0), and two
// concatenated arrays (only the first was read).
func TestLibexParserRefusesAmbiguousFiles(t *testing.T) {
	row1 := `{"asin":"B0LIBEX011","title":"Row One","region":"us","language":"english","authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":"Bea Reader"}],"lengthMinutes":600}`
	row2 := `{"asin":"B0LIBEX012","title":"Row Two","region":"us","language":"english","authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":"Bea Reader"}],"lengthMinutes":600}`

	if _, err := parseLibex([]byte(`{"data":[` + row1 + `]}`)); err == nil {
		t.Error("a wrapper under an unrecognized key must fail loud, not import 0 books")
	}
	if _, err := parseLibex([]byte("[" + row1 + "][" + row2 + "]")); err == nil {
		t.Error("trailing content after the array must fail loud, not drop the rest")
	}
	// The inverse: a lone row that happens to carry a wrapper-ish key is still a
	// row, because it has an "asin".
	parsed, err := parseLibex([]byte(`{"asin":"B0LIBEX011","title":"Row One","region":"us","language":"english","library":"my shelf","authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":"Bea Reader"}]}`))
	if err != nil || len(parsed.books) != 1 {
		t.Errorf("a lone row with a wrapper-ish key: %d books, err %v", len(parsed.books), err)
	}
}

// TestLibexUnmappableRegionSkipped pins the parse-time refusal: a row whose
// marketplace does not map has no place to put its ASIN, so importing it would
// mint a work and a recording that no lookup can reach and no dedup can see -
// and would burn the ASIN for a sibling row that DOES state a marketplace.
func TestLibexUnmappableRegionSkipped(t *testing.T) {
	bad := `{"asin":"B0LIBEX021","title":"Nowhere Book","region":"narnia","language":"english","authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":"Bea Reader"}],"lengthMinutes":600}`
	good := `{"asin":"B0LIBEX021","title":"Nowhere Book","region":"us","language":"english","authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":"Bea Reader"}],"lengthMinutes":600}`
	noRegion := `{"asin":"B0LIBEX022","title":"Regionless","language":"english","authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":"Bea Reader"}],"lengthMinutes":600}`

	sum, dataDir := runLibex(t, "["+bad+","+good+","+noRegion+"]", false)
	if !hasWarning(sum.Warnings, `B0LIBEX021: region "narnia" is not a known marketplace`) ||
		!hasWarning(sum.Warnings, "B0LIBEX022") {
		t.Errorf("expected a skip warning for each unmappable row, got %v", sum.Warnings)
	}
	if sum.NewWorks != 1 || sum.NewRecordings != 1 || sum.Skipped != 0 {
		t.Errorf("the good sibling must still import: %+v", sum)
	}
	var rec struct {
		ASIN []struct {
			Region string `json:"region"`
			ASIN   string `json:"asin"`
		} `json:"asin"`
	}
	readEntity(t, dataDir, "works/no/nowhere-book/recordings/bea-reader.json", &rec)
	if len(rec.ASIN) != 1 || rec.ASIN[0].Region != "us" || rec.ASIN[0].ASIN != "B0LIBEX021" {
		t.Errorf("asin = %+v, want the good sibling's us/B0LIBEX021", rec.ASIN)
	}
}

// TestLibexIdempotent pins that a re-run of the same export changes nothing: every
// row dedupes on its ASIN and no file is rewritten.
func TestLibexIdempotent(t *testing.T) {
	export := libexFixture(t)
	_, dataDir := runLibex(t, export, false)
	before := snapshotTree(t, dataDir)

	sum, err := RunLibex(writeBooks(t, export), Options{DataDir: dataDir, ImportDate: testImportDate})
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if sum.Skipped != 2 || sum.NewWorks != 0 || sum.NewRecordings != 0 || sum.NewPeople != 0 || sum.NewSeries != 0 || sum.MergedASINs != 0 {
		t.Errorf("second run should be all skips: %+v", sum)
	}
	if after := snapshotTree(t, dataDir); !reflect.DeepEqual(before, after) {
		t.Errorf("second run rewrote the tree:\nbefore %v\nafter  %v", keysOf(before), keysOf(after))
	}
}

// TestLibexRegionFallback covers what libexRegion adds on top of mapRegion (which
// owns the alias/marketplace cases in its own test): the regions[] fallback and
// a row that states no marketplace at all.
func TestLibexRegionFallback(t *testing.T) {
	cases := []struct {
		name   string
		row    string
		want   string
		wantOK bool
	}{
		{"regions fallback", `{"regions":["au","us"]}`, "au", true},
		{"regions fallback is aliased too", `{"regions":["gb"]}`, "uk", true},
		{"region wins over regions", `{"region":"de","regions":["us"]}`, "de", true},
		{"unknown", `{"region":"narnia"}`, "", false},
		{"absent", `{}`, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var e rawBook
			if err := jsonInto(tc.row, &e); err != nil {
				t.Fatal(err)
			}
			got, _, ok := libexRegion(e)
			if got != tc.want || ok != tc.wantOK {
				t.Errorf("libexRegion = %q,%v; want %q,%v", got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func TestLibexAbridged(t *testing.T) {
	cases := []struct {
		in   string
		want *bool
	}{
		{"unabridged", boolPtr(false)},
		{"Abridged", boolPtr(true)},
		{"", nil},
		{"audiobook", nil},
	}
	for _, tc := range cases {
		got := libexAbridged(tc.in)
		switch {
		case tc.want == nil && got != nil:
			t.Errorf("libexAbridged(%q) = %v, want nil", tc.in, *got)
		case tc.want != nil && (got == nil || *got != *tc.want):
			t.Errorf("libexAbridged(%q) = %v, want %v", tc.in, got, *tc.want)
		}
	}
}

func boolPtr(v bool) *bool { return &v }

func TestLibexNamesDedupe(t *testing.T) {
	var e rawBook
	if err := jsonInto(`{"authors":[{"name":"Ada Mapmaker"},{"name":"Ada Mapmaker"},{"name":"Bo Writer"}]}`, &e); err != nil {
		t.Fatal(err)
	}
	if got := libexNames(e["authors"]); !reflect.DeepEqual(got, []string{"Ada Mapmaker", "Bo Writer"}) {
		t.Errorf("libexNames = %v", got)
	}
	// A plain string array (a looser export) still works.
	if err := jsonInto(`{"narrators":["Bea Reader"]}`, &e); err != nil {
		t.Fatal(err)
	}
	if got := libexNames(e["narrators"]); !reflect.DeepEqual(got, []string{"Bea Reader"}) {
		t.Errorf("libexNames from strings = %v", got)
	}
	// A bare string is the retailer's own comma-joined list, so it is split.
	if err := jsonInto(`{"authors":"Ada Mapmaker, Bo Writer"}`, &e); err != nil {
		t.Fatal(err)
	}
	if got := libexNames(e["authors"]); !reflect.DeepEqual(got, []string{"Ada Mapmaker", "Bo Writer"}) {
		t.Errorf("libexNames from a joined string = %v", got)
	}
}

// TestLibexNameWithComma pins why structured credits are carried as a typed list
// rather than joined: a name containing a comma must stay ONE person.
func TestLibexNameWithComma(t *testing.T) {
	export := `[{"asin":"B0LIBEX030","title":"The Comma","region":"us","language":"english",` +
		`"authors":[{"name":"Alexandre Dumas, père"}],"narrators":[{"name":"Bea Reader"}],"lengthMinutes":600}]`
	sum, dataDir := runLibex(t, export, false)
	if sum.NewPeople != 2 {
		t.Errorf("NewPeople = %d, want 2 (the author is one person)", sum.NewPeople)
	}
	var work struct {
		Authors []string `json:"authors"`
	}
	readEntity(t, dataDir, "works/th/the-comma/work.json", &work)
	if !reflect.DeepEqual(work.Authors, []string{"alexandre-dumas-pere"}) {
		t.Errorf("authors = %v, want one person", work.Authors)
	}
}

// TestLibexCreditsDedupeBySlug pins that identity is the SLUG: two spellings of
// one name on the same row are one credit, not a repeated entry.
func TestLibexCreditsDedupeBySlug(t *testing.T) {
	export := `[{"asin":"B0LIBEX031","title":"Two Spellings","region":"us","language":"english",` +
		`"authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":"Ramón De Ocampo"},{"name":"Ramon de Ocampo"}],"lengthMinutes":600}]`
	sum, dataDir := runLibex(t, export, false)
	if sum.NewPeople != 2 {
		t.Errorf("NewPeople = %d, want 2 (one author, one narrator)", sum.NewPeople)
	}
	var rec struct {
		Narrators []string `json:"narrators"`
	}
	readEntity(t, dataDir, "works/tw/two-spellings/recordings/ramon-de-ocampo.json", &rec)
	if !reflect.DeepEqual(rec.Narrators, []string{"ramon-de-ocampo"}) {
		t.Errorf("narrators = %v, want the slug listed once", rec.Narrators)
	}
}

// TestLibexSubtitleDisambiguates pins the subtitle's real role: it is never a
// stored fact, but it composes the FULL title that tells two same-titled volumes
// of one series apart - and so it does appear inside work.title when that
// disambiguation triggers.
func TestLibexSubtitleDisambiguates(t *testing.T) {
	export := `[` +
		`{"asin":"B0LIBEX040","title":"Dragon Heart","subtitle":"Book 1: First Flight","region":"us","language":"english",` +
		`"authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":"Bea Reader"}],"lengthMinutes":600,` +
		`"series":[{"name":"Dragon Heart","position":1}]},` +
		`{"asin":"B0LIBEX041","title":"Dragon Heart","subtitle":"Book 2: Land of War","region":"us","language":"english",` +
		`"authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":"Bea Reader"}],"lengthMinutes":600,` +
		`"series":[{"name":"Dragon Heart","position":2}]}]`
	sum, dataDir := runLibex(t, export, false)
	if sum.NewWorks != 2 {
		t.Fatalf("NewWorks = %d, want 2 (the volumes must not collapse): %+v", sum.NewWorks, sum)
	}
	var work struct {
		Title    string `json:"title"`
		Subtitle string `json:"subtitle"`
	}
	readEntity(t, dataDir, "works/dr/dragon-heart-book-2-land-of-war/work.json", &work)
	if work.Title != "Dragon Heart: Book 2: Land of War" {
		t.Errorf("title = %q, want the composed full title", work.Title)
	}
	if work.Subtitle != "" {
		t.Errorf("subtitle leaked onto the work as its own field: %q", work.Subtitle)
	}
}

// TestLibexChapters pins that libex hands its own rows to buildChapters: the
// camelCase offset spelling is read without re-boxing the rows into
// OpenAudible's key names.
func TestLibexChapters(t *testing.T) {
	var e rawBook
	if err := jsonInto(`{"chapters":[{"title":"One","startOffsetMs":0,"lengthMs":1000},{"title":" ","startOffsetMs":1000,"lengthMs":500}]}`, &e); err != nil {
		t.Fatal(err)
	}
	var lp libexParse
	warn := func(string, ...any) { t.Error("no warning expected") }
	sb := libexToBook(e, "B0LIBEX050", "us", nil, &lp)
	if len(lp.warnings) != 0 {
		t.Errorf("no parse warning expected: %+v", lp.warnings)
	}
	chs := buildChapters(sb.chapterRows(), warn)
	if len(chs) != 2 || chs[0].Title != "One" || chs[0].LengthMS != 1000 || chs[1].Title != "Chapter 2" {
		t.Errorf("chapters = %+v", chs)
	}
}

// TestLibexHTTPCoverWarns pins that an unusable (non-https) cover URL is dropped
// LOUDLY - the schema cannot store it, but silently discarding a stated fact
// hides that the row had a cover at all.
func TestLibexHTTPCoverWarns(t *testing.T) {
	export := `[{"asin":"B0LIBEX060","title":"Plain Cover","region":"us","language":"english","imageUrl":"http://example.com/c.jpg",` +
		`"authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":"Bea Reader"}],"lengthMinutes":600}]`
	sum, dataDir := runLibex(t, export, false)
	if !hasWarning(sum.Warnings, "is not https") {
		t.Errorf("expected a dropped-cover warning, got %v", sum.Warnings)
	}
	var rec struct {
		CoverURL string `json:"cover_url"`
	}
	readEntity(t, dataDir, "works/pl/plain-cover/recordings/bea-reader.json", &rec)
	if rec.CoverURL != "" {
		t.Errorf("cover_url = %q, want none", rec.CoverURL)
	}
}

// TestLibexISBNAcceptsHyphens pins the shared NormalizeISBN behaviour: an ISBN
// printed with hyphens is accepted and stored in its canonical hyphenless form
// (the issue form has always accepted them; a bulk import used to reject them).
func TestLibexISBNAcceptsHyphens(t *testing.T) {
	export := `[{"asin":"B0LIBEX070","title":"Hyphenated","region":"us","language":"english","isbn":"978-1-234-56789-7",` +
		`"authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":"Bea Reader"}],"lengthMinutes":600}]`
	sum, dataDir := runLibex(t, export, false)
	if len(sum.Warnings) != 0 {
		t.Errorf("unexpected warnings: %v", sum.Warnings)
	}
	var rec struct {
		ISBN []string `json:"isbn"`
	}
	readEntity(t, dataDir, "works/hy/hyphenated/recordings/bea-reader.json", &rec)
	if !reflect.DeepEqual(rec.ISBN, []string{"9781234567897"}) {
		t.Errorf("isbn = %v, want the normalized form", rec.ISBN)
	}
}
