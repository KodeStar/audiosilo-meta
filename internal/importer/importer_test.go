package importer

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/testpack"
	"github.com/kodestar/audiosilo-meta/pkg/check"
	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// testImportDate is the imported_at stamp every test run uses.
const testImportDate = "2026-07-11"

// jsonInto decodes s into v with UseNumber so coercion helpers see json.Number.
func jsonInto(s string, v any) error {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	return dec.Decode(v)
}

// writeBooks writes an export file into a temp dir and returns its path.
func writeBooks(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "books.json")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// runWith runs the given importer entrypoint (Run or RunLibation) against a
// fresh empty data dir and returns the summary and the data dir.
func runWith(t *testing.T, run func(string, Options) (Summary, error), input string, dryRun bool) (Summary, string) {
	t.Helper()
	dataDir := t.TempDir()
	sum, err := run(writeBooks(t, input), Options{DataDir: dataDir, ImportDate: testImportDate, DryRun: dryRun})
	if err != nil {
		t.Fatalf("import run: %v", err)
	}
	return sum, dataDir
}

// runImport runs the OpenAudible importer against a fresh empty data dir.
func runImport(t *testing.T, booksJSON string, dryRun bool) (Summary, string) {
	t.Helper()
	return runWith(t, Run, booksJSON, dryRun)
}

// seedTree writes a catalogue into dataDir's pack tree. Keys are per-entity
// addresses ("works/th/the-thing/work.json"), which is how a seed literal reads
// as the record it is; testpack resolves each to its pack family and entry key.
func seedTree(t *testing.T, dataDir string, files map[string]string) {
	t.Helper()
	testpack.Seed(t, dataDir, files)
}

// readEntity decodes the record at a per-entity address out of the pack tree,
// asserting the pack holding it is canonical.
func readEntity(t *testing.T, dataDir, address string, v any) {
	t.Helper()
	testpack.Read(t, dataDir, address, v)
}

// entryExists reports whether the pack tree holds a record at the address.
func entryExists(t *testing.T, dataDir, address string) bool {
	t.Helper()
	return testpack.Exists(t, dataDir, address)
}

// rawEntity returns the record's raw JSON, for the few assertions that inspect
// bytes rather than a decoded shape.
func rawEntity(t *testing.T, dataDir, address string) []byte {
	t.Helper()
	raw, ok := testpack.Raw(t, dataDir, address)
	if !ok {
		t.Fatalf("no record at %s", address)
	}
	return raw
}

// shard is the directory a slug used to live under, before the pack migration.
// The addresses below still spell it because that is testpack's fixture syntax;
// nothing in the tooling computes a shard any more.
func shard(slug string) string {
	if len(slug) < 2 {
		return slug
	}
	return slug[:2]
}

// workAddr / recAddr / personAddr / seriesAddr compose the per-entity addresses
// the helpers above take, for the tests that build one from a computed slug.
func workAddr(slug string) string {
	return path.Join("works", shard(slug), slug, "work.json")
}

func recAddr(workSlug, recSlug string) string {
	return path.Join("works", shard(workSlug), workSlug, "recordings", recSlug+".json")
}

func seriesAddr(slug string) string {
	return path.Join("series", shard(slug), slug+".json")
}

// recSlugsOf returns a work's recording slugs, sorted - the pack-layout answer
// to listing its recordings directory.
func recSlugsOf(t *testing.T, dataDir, workSlug string) []string {
	t.Helper()
	return testpack.Recordings(t, dataDir, workSlug)
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
	readEntity(t, dataDir, "works/th/the-iron-ledger/work.json", &work)
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
		work.Sources[0].Ref != "B0SYNTH001" || work.Sources[0].ImportedAt != testImportDate {
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
	readEntity(t, dataDir, "works/th/the-iron-ledger/recordings/priya-lund-2025.json", &rec)
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
	raw := rawEntity(t, dataDir, "works/th/the-bronze-ledger/recordings/priya-lund-2025.json")
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
	readEntity(t, dataDir, "series/th/the-ledger-wars.json", &series)
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
	seedTree(t, dataDir, seed)

	books := `[{"asin":"B0SYNTH001","title_short":"The Iron Ledger","author":"Mara Quill","narrated_by":"Priya Lund","language":"english","region":"US","seconds":1000}]`
	sum, err := Run(writeBooks(t, books), Options{DataDir: dataDir, ImportDate: testImportDate})
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
	if len(listWorks(t, dataDir)) != 0 {
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
	raw := rawEntity(t, dataDir, "works/ba/bad-chapters/recordings/nadia-vox-2023.json")
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
	if !entryExists(t, dataDir, "works/th/the-gathering/work.json") {
		t.Errorf("first work should own the bare slug")
	}
	if !entryExists(t, dataDir, "works/th/the-gathering-bob-south/work.json") {
		t.Errorf("second work should be disambiguated by author: %v", listWorks(t, dataDir))
	}
	if !hasWarning(sum.Warnings, "taken by a different book") {
		t.Errorf("expected a slug-collision warning, got %v", sum.Warnings)
	}
}

// spikeTitleSlug is a real Slugify output from the 142k-book validation spike:
// a German title truncated to exactly MaxSlugLen. Appending an author slug to
// it used to mint a 115-char work id that failed model.ValidSlug and cascaded
// into recording and series reference failures.
const spikeTitleSlug = "die-ideale-welt-fur-den-soziopathen-ein-apokalyptisches-litrpg-abenteuer-die-ideale-welt-fur-den-soz"

// assertCandidateChain checks the invariants every workCandidates result must
// hold: every candidate is a valid slug, every NUMBERED candidate still carries
// its own "-<i>", and the numbered candidates are therefore pairwise distinct.
//
// Global distinctness is deliberately NOT asserted: candidates 0 and 1 carry no
// number, so a base or author slug ending in digits can make one of them equal a
// later candidate. That costs the walk one wasted probe of a slug it has already
// tested and nothing more (see workSlugAt).
// candidateSlugs is the identity-author chain as a plain slug list: the two
// author roots are the same in these tests, so the legacy probe collapses onto
// the identity one and the chain is exactly the 51 the formula defines.
func candidateSlugs(t *testing.T, base, author string) []string {
	t.Helper()
	cands, primary := workCandidates(base, workAuthors{all: []string{author}, identity: []string{author}})
	if primary != 2 {
		t.Fatalf("primary candidates = %d, want 2 for a single-author row", primary)
	}
	out := make([]string, 0, len(cands))
	for _, c := range cands {
		if c.probeOnly {
			t.Fatalf("candidate %q is a probe, but the row credits one author", c.slug)
		}
		out = append(out, c.slug)
	}
	return out
}

func assertCandidateChain(t *testing.T, got []string) {
	t.Helper()
	if len(got) != 51 {
		t.Fatalf("candidate chain has %d entries, want 51", len(got))
	}
	seen := map[string]bool{}
	for i, slug := range got {
		if !model.ValidSlug(slug) {
			t.Errorf("candidate %d = %q (%d chars) is not a valid slug", i, slug, len(slug))
		}
		if i < 2 {
			continue
		}
		if !strings.HasSuffix(slug, fmt.Sprintf("-%d", i)) {
			t.Errorf("candidate %d = %q lost its numeric suffix", i, slug)
		}
		if seen[slug] {
			t.Errorf("numbered candidate %d = %q duplicates an earlier numbered candidate", i, slug)
		}
		seen[slug] = true
	}
}

func TestWorkCandidatesShortBaseUnchanged(t *testing.T) {
	got := candidateSlugs(t, "the-gathering", "bob-south")
	assertCandidateChain(t, got)
	want := []string{"the-gathering", "the-gathering-bob-south", "the-gathering-bob-south-2", "the-gathering-bob-south-3"}
	if !reflect.DeepEqual(got[:len(want)], want) {
		t.Errorf("candidate chain = %v, want prefix %v", got[:len(want)], want)
	}
	if got[50] != "the-gathering-bob-south-50" {
		t.Errorf("last candidate = %q", got[50])
	}
}

func TestWorkCandidatesBoundedToMaxSlugLen(t *testing.T) {
	if len(spikeTitleSlug) != model.MaxSlugLen {
		t.Fatalf("fixture base is %d chars, want %d", len(spikeTitleSlug), model.MaxSlugLen)
	}
	const author = "oleg-sapphire"
	got := candidateSlugs(t, spikeTitleSlug, author)
	assertCandidateChain(t, got)
	if got[0] != spikeTitleSlug {
		t.Errorf("first candidate = %q, want the bare base untouched", got[0])
	}
	head := strings.TrimSuffix(got[1], "-"+author)
	if head == got[1] {
		t.Fatalf("candidate %q does not end in -%s", got[1], author)
	}
	if !strings.HasPrefix(spikeTitleSlug, head) || spikeTitleSlug[len(head)] != '-' {
		t.Errorf("head %q is not the base cut at a word boundary", head)
	}
}

func TestWorkCandidatesFallbackWithoutWordBoundary(t *testing.T) {
	// Neither base can be cut at a hyphen and still leave room for the tail: the
	// first has no hyphen at all, the second's author slug alone fills the cap.
	cases := []struct{ name, base, author string }{
		{"single-word title", strings.Repeat("a", model.MaxSlugLen), "oleg-sapphire"},
		{"author fills the cap", "a-long-enough-title-to-cut", strings.Repeat("b", model.MaxSlugLen)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assertCandidateChain(t, candidateSlugs(t, c.base, c.author))
		})
	}
}

func TestOverlongTitleCollisionProducesValidSlugs(t *testing.T) {
	// Same over-long title, two authors: the second book walks onto the
	// author-suffixed candidate, which must still validate end to end.
	const title = "Die ideale Welt fur den Soziopathen: Ein apokalyptisches LitRPG Abenteuer, die ideale Welt fur den Soziopathen Band Zwei"
	books := fmt.Sprintf(`[
		{"asin":"B0LONGTTL1","title_short":%[1]q,"author":"Oleg Sapphire","narrated_by":"V One","language":"german","seconds":600},
		{"asin":"B0LONGTTL2","title_short":%[1]q,"author":"Other Author","narrated_by":"V Two","language":"german","seconds":600}
	]`, title)
	sum, dataDir := runImport(t, books, false)
	if sum.NewWorks != 2 {
		t.Fatalf("expected 2 distinct works, got %d (%v)", sum.NewWorks, sum.Warnings)
	}
	if res := check.Load(dataDir); !res.OK() {
		t.Fatalf("imported tree failed validation:\n%v", res.Problems)
	}
	for _, slug := range listWorks(t, dataDir) {
		var work struct {
			ID string `json:"id"`
		}
		readEntity(t, dataDir, workAddr(slug), &work)
		if !model.ValidSlug(work.ID) {
			t.Errorf("work id %q (%d chars) is not a valid slug", work.ID, len(work.ID))
		}
	}
}

// TestOverlongNarratorRecordingSlugs pins the recording chain's bound: a
// full-cast credit slugifying to the cap plus the release year already overran
// MaxSlugLen before the collision chain appended a single suffix.
func TestOverlongNarratorRecordingSlugs(t *testing.T) {
	narrator := strings.Repeat("Narrator ", 12) + "Voice"
	if len(Slugify(narrator)) != model.MaxSlugLen {
		t.Fatalf("fixture narrator slug is %d chars, want %d", len(Slugify(narrator)), model.MaxSlugLen)
	}
	// Same work, same narrator, same year, runtimes far enough apart to be two
	// productions - so the second lands on the chain's numeric candidate.
	books := fmt.Sprintf(`[
		{"asin":"B0LONGNAR1","title_short":"Cast Recording","author":"Some Author","narrated_by":%[1]q,"language":"english","release_date":"2020-03-01","seconds":600},
		{"asin":"B0LONGNAR2","title_short":"Cast Recording","author":"Some Author","narrated_by":%[1]q,"language":"english","release_date":"2020-09-01","seconds":7200}
	]`, narrator)
	sum, dataDir := runImport(t, books, false)
	if sum.NewWorks != 1 || sum.NewRecordings != 2 {
		t.Fatalf("expected 1 work with 2 recordings, got %+v", sum)
	}
	if res := check.Load(dataDir); !res.OK() {
		t.Fatalf("imported tree failed validation:\n%v", res.Problems)
	}
	// Recording ids are the entry keys of the work's recordings map, so
	// uniqueness is structural; validity is what this pins.
	recs := recSlugsOf(t, dataDir, "cast-recording")
	for _, id := range recs {
		if !model.ValidSlug(id) {
			t.Errorf("recording id %q (%d chars) is not a valid slug", id, len(id))
		}
	}
	if len(recs) != 2 {
		t.Errorf("expected 2 recordings, got %v", recs)
	}
}

// TestOverlongTitleMergeWarnsOnConflation pins the one behaviour the bound
// changes rather than fixes: two DIFFERENT long titles by one author that agree
// up to the truncation point land on the same shortened candidate and merge as a
// single work. The unbounded formula "reported" this by minting an invalid slug
// for metacheck to reject, so the merge must not be silent.
func TestOverlongTitleMergeWarnsOnConflation(t *testing.T) {
	// Two 90-char bases sharing everything up to the cut at 84.
	prefix := strings.Repeat("Saga ", 17)
	titleA, titleO := prefix+"Alpha", prefix+"Omega"
	// The bare bases are claimed by other authors first, so the shared-author
	// books fall through to the author-suffixed (and therefore shortened)
	// candidate.
	books := fmt.Sprintf(`[
		{"asin":"B0CONFL001","title_short":%[1]q,"author":"Yuri Vale","narrated_by":"V One","language":"english","seconds":600},
		{"asin":"B0CONFL002","title_short":%[2]q,"author":"Zara Nile","narrated_by":"V Two","language":"english","seconds":600},
		{"asin":"B0CONFL003","title_short":%[1]q,"author":"Xavier Poe","narrated_by":"V Three","language":"english","seconds":600},
		{"asin":"B0CONFL004","title_short":%[2]q,"author":"Xavier Poe","narrated_by":"V Four","language":"english","seconds":600}
	]`, titleA, titleO)
	sum, dataDir := runImport(t, books, false)
	if sum.NewWorks != 3 {
		t.Fatalf("expected 3 works (the two squatters plus one merged), got %d: %v", sum.NewWorks, listWorks(t, dataDir))
	}
	if !hasWarning(sum.Warnings, "was shortened to fit") {
		t.Errorf("a merge onto a truncated slug must warn, got %v", sum.Warnings)
	}
	if !hasWarning(sum.Warnings, titleO) {
		t.Errorf("the warning must name the incoming title, got %v", sum.Warnings)
	}
	if res := check.Load(dataDir); !res.OK() {
		t.Fatalf("imported tree failed validation:\n%v", res.Problems)
	}
}

// TestReimportOverlongSlugsIsNoop pins idempotency for the bounded slugs
// specifically: the existing idempotency tests all use short names, so nothing
// would catch a bound that resolved differently on the second pass (which would
// re-create every truncated work, recording and series as a sibling).
func TestReimportOverlongSlugsIsNoop(t *testing.T) {
	narrator := strings.Repeat("Narrator ", 12) + "Voice"
	title := "Die ideale Welt fur den Soziopathen: Ein apokalyptisches LitRPG Abenteuer, die ideale Welt fur den Soziopathen Band Zwei"
	seriesA, seriesB := strings.Repeat("Long ", 25)+"Alpha", strings.Repeat("Long ", 25)+"Beta"
	books := fmt.Sprintf(`[
		{"asin":"B0REIMP001","title_short":%[1]q,"author":"Oleg Sapphire","narrated_by":%[2]q,"language":"german","region":"US","release_date":"2020-03-01","seconds":600,"series_name":%[3]q,"series_sequence":"1"},
		{"asin":"B0REIMP002","title_short":%[1]q,"author":"Other Author","narrated_by":%[2]q,"language":"german","region":"US","release_date":"2020-09-01","seconds":600,"series_name":%[4]q,"series_sequence":"1"}
	]`, title, narrator, seriesA, seriesB)

	sum, dataDir := runImport(t, books, false)
	if sum.NewWorks != 2 || sum.NewSeries != 2 {
		t.Fatalf("setup: NewWorks/NewSeries = %d/%d, want 2/2", sum.NewWorks, sum.NewSeries)
	}
	before := snapshotTree(t, dataDir)

	sum2, err := Run(writeBooks(t, books), Options{DataDir: dataDir, ImportDate: testImportDate})
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if sum2.Skipped != 2 || sum2.NewWorks != 0 || sum2.NewRecordings != 0 || sum2.NewSeries != 0 {
		t.Errorf("second run should be all skips: %+v", sum2)
	}
	if after := snapshotTree(t, dataDir); !reflect.DeepEqual(before, after) {
		t.Errorf("second run rewrote the tree:\nbefore %v\nafter  %v", keysOf(before), keysOf(after))
	}
}

// TestOverlongSeriesNameCollision pins the series chain's bound AND that all
// three walkers of it agree: getOrCreateSeries places the second series on the
// suffixed slug, findSeries puts a later volume in that same series, and
// libexselect's seriesIndex.find resolves the name to the slug on disk.
func TestOverlongSeriesNameCollision(t *testing.T) {
	// Two different names whose slugs truncate to the same MaxSlugLen-bounded
	// base: the second series can only exist on a numeric candidate.
	prefix := strings.Repeat("Long ", 25)
	seriesA, seriesB := prefix+"Alpha", prefix+"Beta"
	if Slugify(seriesA) != Slugify(seriesB) {
		t.Fatalf("fixture names do not collide: %q vs %q", Slugify(seriesA), Slugify(seriesB))
	}
	if len(Slugify(seriesB))+len("-2") <= model.MaxSlugLen {
		t.Fatalf("fixture base is only %d chars; a numeric suffix must overflow the cap", len(Slugify(seriesB)))
	}
	books := fmt.Sprintf(`[
		{"asin":"B0LONGSER1","title_short":"Alpha One","author":"Series Author","narrated_by":"Voice","series_name":%[1]q,"series_sequence":"1","language":"english","seconds":600},
		{"asin":"B0LONGSER2","title_short":"Beta One","author":"Series Author","narrated_by":"Voice","series_name":%[2]q,"series_sequence":"1","language":"english","seconds":600},
		{"asin":"B0LONGSER3","title_short":"Beta Two","author":"Series Author","narrated_by":"Voice","series_name":%[2]q,"series_sequence":"2","language":"english","seconds":600}
	]`, seriesA, seriesB)
	sum, dataDir := runImport(t, books, false)
	if sum.NewSeries != 2 {
		t.Fatalf("expected 2 distinct series, got %d (%v)", sum.NewSeries, sum.Warnings)
	}
	if res := check.Load(dataDir); !res.OK() {
		t.Fatalf("imported tree failed validation:\n%v", res.Problems)
	}

	idx, _ := loadSeriesIndex(dataDir)
	slugB, found := idx.find(seriesB)
	if !found {
		t.Fatalf("seriesIndex.find does not resolve the suffixed series; index holds %v", idx.bySlug)
	}
	if !model.ValidSlug(slugB) {
		t.Errorf("series id %q (%d chars) is not a valid slug", slugB, len(slugB))
	}
	if slugA, _ := idx.find(seriesA); slugA == slugB {
		t.Errorf("both names resolved to %q", slugB)
	}
	// Both Beta volumes must have landed in the series find resolved to: that is
	// getOrCreateSeries and findSeries agreeing with the selector's chain.
	var series struct {
		Works []struct{ Work string } `json:"works"`
	}
	readEntity(t, dataDir, seriesAddr(slugB), &series)
	if len(series.Works) != 2 {
		t.Errorf("series %q holds %d works, want the 2 Beta volumes", slugB, len(series.Works))
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
	if recs := recSlugsOf(t, dataDir, "twin-tale"); len(recs) != 2 {
		t.Errorf("expected 2 recordings, got %v", recs)
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
	seedTree(t, dataDir, seed)
	books := `[{"asin":"B0EXTEND01","title_short":"Book Beta","author":"Existing Author","narrated_by":"Voice","series_name":"My Series","series_sequence":"2","language":"english","seconds":600}]`
	sum, err := Run(writeBooks(t, books), Options{DataDir: dataDir, ImportDate: testImportDate})
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
	readEntity(t, dataDir, "series/my/my-series.json", &series)
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

// listWorks returns every work slug in the tree, sorted - the pack-layout
// answer to walking works/ for work.json files.
func listWorks(t *testing.T, dataDir string) []string {
	t.Helper()
	return testpack.Slugs(t, dataDir, pack.FamilyWorks)
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
	if entryExists(t, dataDir, "people/b-/b-v-larson-2.json") {
		t.Errorf("numbered duplicate person was created")
	}
	// First occurrence's name wins.
	var person struct {
		Name string `json:"name"`
	}
	readEntity(t, dataDir, "people/ra/ramon-de-ocampo.json", &person)
	if person.Name != "Ramón De Ocampo" {
		t.Errorf("first-seen name should win, got %q", person.Name)
	}
	// Both works reference the same author slug.
	for _, w := range []string{"works/st/steel-world/work.json", "works/du/dust-world/work.json"} {
		var work struct {
			Authors []string `json:"authors"`
		}
		readEntity(t, dataDir, w, &work)
		if len(work.Authors) != 1 || work.Authors[0] != "b-v-larson" {
			t.Errorf("%s authors = %v, want [b-v-larson]", w, work.Authors)
		}
	}
}

func TestPersonVariantReusesExistingRecord(t *testing.T) {
	// A diacritic variant of a person already in the catalog reuses the existing
	// record; its committed name is kept and no new file is emitted.
	dataDir := t.TempDir()
	const personAddress = "people/ra/ramon-de-ocampo.json"
	seed := map[string]string{
		personAddress: `{"id":"ramon-de-ocampo","license":"CC0-1.0","name":"Ramón De Ocampo","sources":[{"type":"user"}]}`,
	}
	seedTree(t, dataDir, seed)

	books := `[{"asin":"B0REUSE001","title_short":"Wimpy Tales","author":"Ramon de Ocampo","narrated_by":"Fresh Voice","language":"english","region":"US","seconds":600}]`
	sum, err := Run(writeBooks(t, books), Options{DataDir: dataDir, ImportDate: testImportDate})
	if err != nil {
		t.Fatal(err)
	}
	if sum.NewPeople != 1 { // only the narrator is new
		t.Errorf("NewPeople = %d, want 1 (author variant must reuse existing)", sum.NewPeople)
	}
	assertEntryUnchanged(t, dataDir, personAddress, seed)
	var work struct {
		Authors []string `json:"authors"`
	}
	readEntity(t, dataDir, "works/wi/wimpy-tales/work.json", &work)
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
	if entryExists(t, dataDir, "works/dr/dragon-heart/work.json") {
		t.Errorf("no volume may squat the ambiguous short-title slug")
	}
	wantWorks := map[string]string{
		"dragon-heart-book-1-iron-will":    "1",
		"dragon-heart-book-5-sea-of-sand":  "5",
		"dragon-heart-book-10-land-of-war": "10",
	}
	for slug := range wantWorks {
		if !entryExists(t, dataDir, workAddr(slug)) {
			t.Errorf("missing full-title work %q; works: %v", slug, listWorks(t, dataDir))
		}
	}
	var series struct {
		Works []struct {
			Work     string `json:"work"`
			Position string `json:"position"`
		} `json:"works"`
	}
	readEntity(t, dataDir, "series/dr/dragon-heart.json", &series)
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
	seedTree(t, dataDir, seed)

	books := `[{"asin":"B0DRAGONH5","title":"Dragon Heart - Book 5: Sea of Sand","title_short":"Dragon Heart","author":"Kirill Klevanski","narrated_by":"Zach Villa","series_name":"Dragon Heart","series_sequence":"5","language":"english","region":"US","release_date":"2020-01-01","seconds":60000}]`
	sum, err := Run(writeBooks(t, books), Options{DataDir: dataDir, ImportDate: testImportDate})
	if err != nil {
		t.Fatal(err)
	}
	if sum.NewWorks != 1 {
		t.Fatalf("NewWorks = %d, want 1 (a new volume, not a merge)", sum.NewWorks)
	}
	if !entryExists(t, dataDir, "works/dr/dragon-heart-book-5-sea-of-sand/work.json") {
		t.Errorf("full-title work missing; works: %v", listWorks(t, dataDir))
	}
	// The existing volume kept its slug and its lone recording-less state.
	assertEntryUnchanged(t, dataDir, "works/dr/dragon-heart/work.json", seed)
	var series struct {
		Works []struct {
			Work     string `json:"work"`
			Position string `json:"position"`
		} `json:"works"`
	}
	readEntity(t, dataDir, "series/dr/dragon-heart.json", &series)
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
	readEntity(t, dataDir, "series/ha/halo.json", &series)
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
	if entryExists(t, dataDir, "works/dr/dragon-heart/work.json") {
		t.Errorf("volume 1 squatted the bare slug despite extra credits; works: %v", listWorks(t, dataDir))
	}
	for _, slug := range []string{
		"dragon-heart-book-1-iron-will",
		"dragon-heart-book-5-sea-of-sand",
		"dragon-heart-book-10-land-of-war",
	} {
		if !entryExists(t, dataDir, workAddr(slug)) {
			t.Errorf("missing full-title work %q", slug)
		}
	}
	// Role qualifiers were stripped from the extra credits: clean person
	// records, no qualifier-suffixed slugs.
	var work struct {
		Authors []string `json:"authors"`
	}
	readEntity(t, dataDir, "works/dr/dragon-heart-book-1-iron-will/work.json", &work)
	wantAuthors := []string{"kirill-klevanski", "valeria-kornosenko", "j-kharkova"}
	if !reflect.DeepEqual(work.Authors, wantAuthors) {
		t.Errorf("volume 1 authors = %v, want %v", work.Authors, wantAuthors)
	}
	var person struct {
		Name string `json:"name"`
	}
	readEntity(t, dataDir, "people/va/valeria-kornosenko.json", &person)
	if person.Name != "Valeria Kornosenko" {
		t.Errorf("credited person name = %q, want qualifier stripped", person.Name)
	}
	if entryExists(t, dataDir, "people/va/valeria-kornosenko-introduction.json") {
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
	readEntity(t, dataDir, "series/dr/dragon-heart.json", &series)
	if len(series.Works) != 3 {
		t.Errorf("series should hold 3 works, got %+v", series.Works)
	}
}

func TestCleanWorkTitle(t *testing.T) {
	cases := map[string]string{
		"System Collapse (Unabridged)":  "System Collapse",
		"Mageling (Unabridged)":         "Mageling",
		"Rogue Protocol [Abridged]":     "Rogue Protocol",
		"Twice (Unabridged) [Abridged]": "Twice", // repeated until stable
		"Plain Title":                   "Plain Title",
		"(Unabridged)":                  "(Unabridged)", // only a marker: unchanged
		"  Spaced (Unabridged)  ":       "Spaced",
	}
	for in, want := range cases {
		if got := cleanWorkTitle(in); got != want {
			t.Errorf("cleanWorkTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestImportStripsEditionMarker(t *testing.T) {
	// An OpenAudible entry whose title carries a trailing (Unabridged) marker
	// resolves to the undecorated work slug and stores the clean title.
	books := `[{"asin":"B0SYSCOL01","title_short":"System Collapse (Unabridged)","author":"Martha Wells","narrated_by":"Kevin Free","language":"english","region":"US","seconds":36000}]`
	sum, dataDir := runImport(t, books, false)
	if sum.NewWorks != 1 {
		t.Fatalf("NewWorks = %d, want 1", sum.NewWorks)
	}
	if !entryExists(t, dataDir, "works/sy/system-collapse/work.json") {
		t.Fatalf("edition marker not stripped from work slug; works: %v", listWorks(t, dataDir))
	}
	var work struct {
		Title string `json:"title"`
	}
	readEntity(t, dataDir, "works/sy/system-collapse/work.json", &work)
	if work.Title != "System Collapse" {
		t.Errorf("stored title = %q, want %q", work.Title, "System Collapse")
	}
}

func TestAudiosiloBooksSubtitlePrefixTitle(t *testing.T) {
	// The ABS "Title: Subtitle" concatenation derives the work title from the
	// prefix, not the whole concatenated string.
	env := `{"format":"audiosilo-books","version":1,"books":[
		{"title":"Fugitive Telemetry: Murderbot Diaries, Book 6","subtitle":"Murderbot Diaries, Book 6","authors":["Martha Wells"],"narrators":["Kevin Free"],"language":"en","asin":"B0FUGITEL1","runtime_min":180}
	]}`
	_, dataDir := runWith(t, RunAudiosiloBooks, env, false)
	if !entryExists(t, dataDir, "works/fu/fugitive-telemetry/work.json") {
		t.Fatalf("subtitle-derived work slug missing; works: %v", listWorks(t, dataDir))
	}
	var work struct {
		Title string `json:"title"`
	}
	readEntity(t, dataDir, "works/fu/fugitive-telemetry/work.json", &work)
	if work.Title != "Fugitive Telemetry" {
		t.Errorf("stored title = %q, want %q", work.Title, "Fugitive Telemetry")
	}
}

// assertEntryUnchanged checks the record at address still decodes to exactly
// what the seed put there. It compares decoded values rather than bytes: an
// entry's bytes now carry the indentation of the pack it sits in, which the seed
// literal does not.
func assertEntryUnchanged(t *testing.T, dataDir, address string, seed map[string]string) {
	t.Helper()
	var got, want any
	if err := json.Unmarshal(rawEntity(t, dataDir, address), &got); err != nil {
		t.Fatalf("decode %s: %v", address, err)
	}
	if err := json.Unmarshal([]byte(seed[address]), &want); err != nil {
		t.Fatalf("decode seed %s: %v", address, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s was rewritten:\n got %v\nwant %v", address, got, want)
	}
}

// asinsOf reads a recording's ASIN values as a set.
func asinsOf(t *testing.T, dataDir, address string) map[string]string {
	t.Helper()
	var rec struct {
		ASIN []struct {
			Region string `json:"region"`
			ASIN   string `json:"asin"`
		} `json:"asin"`
	}
	readEntity(t, dataDir, address, &rec)
	out := map[string]string{}
	for _, a := range rec.ASIN {
		out[a.ASIN] = a.Region
	}
	return out
}

func TestSameEditionASINMergesOneRun(t *testing.T) {
	// "Mageling" and "Mageling (Unabridged)": same author/narrator/year, similar
	// runtime, different ASINs -> ONE work, ONE recording carrying BOTH ASINs,
	// the series position claimed once, no collision warning.
	books := `[
		{"asin":"B0MAGELING","title_short":"Mageling","author":"Some Author","narrated_by":"A Reader","series_name":"Mage Series","series_sequence":"1","language":"english","region":"US","release_date":"2021-01-01","seconds":36000},
		{"asin":"B0MAGELUNA","title_short":"Mageling (Unabridged)","author":"Some Author","narrated_by":"A Reader","series_name":"Mage Series","series_sequence":"1","language":"english","region":"US","release_date":"2021-06-01","seconds":36000}
	]`
	sum, dataDir := runImport(t, books, false)
	if sum.NewWorks != 1 || sum.NewRecordings != 1 || sum.MergedASINs != 1 {
		t.Fatalf("summary = %+v, want 1 work / 1 recording / 1 merged ASIN", sum)
	}
	if len(sum.Warnings) != 0 {
		t.Errorf("no warnings expected (esp. no position collision), got %v", sum.Warnings)
	}
	if entryExists(t, dataDir, "works/ma/mageling-unabridged/work.json") {
		t.Errorf("edition variant minted a sibling work; works: %v", listWorks(t, dataDir))
	}
	recs := recSlugsOf(t, dataDir, "mageling")
	if len(recs) != 1 {
		t.Fatalf("expected 1 recording, got %v", recs)
	}
	got := asinsOf(t, dataDir, recAddr("mageling", recs[0]))
	if len(got) != 2 || got["B0MAGELING"] == "" || got["B0MAGELUNA"] == "" {
		t.Errorf("recording ASINs = %v, want both B0MAGELING and B0MAGELUNA", got)
	}
	var series struct {
		Works []struct {
			Work     string `json:"work"`
			Position string `json:"position"`
		} `json:"works"`
	}
	readEntity(t, dataDir, "series/ma/mage-series.json", &series)
	if len(series.Works) != 1 || series.Works[0].Work != "mageling" || series.Works[0].Position != "1" {
		t.Errorf("series should list mageling once at 1, got %+v", series.Works)
	}
	if res := check.Load(dataDir); !res.OK() {
		t.Fatalf("merged tree failed validation: %v", res.Problems)
	}
}

func TestSameEditionASINMergesIntoExisting(t *testing.T) {
	// The re-release arrives against an on-disk recording: its file gains the
	// ASIN, no new work directory, every other field preserved.
	dataDir := t.TempDir()
	seed := map[string]string{
		"people/so/some-author.json":                      `{"id":"some-author","license":"CC0-1.0","name":"Some Author","sources":[{"type":"user"}]}`,
		"people/a-/a-reader.json":                         `{"id":"a-reader","license":"CC0-1.0","name":"A Reader","sources":[{"type":"user"}]}`,
		"works/ma/mageling/work.json":                     `{"authors":["some-author"],"id":"mageling","language":"en","license":"CC0-1.0","sources":[{"type":"user"}],"title":"Mageling"}`,
		"works/ma/mageling/recordings/a-reader-2021.json": `{"asin":[{"asin":"B0MAGELING","region":"us"}],"id":"a-reader-2021","language":"en","license":"CC0-1.0","narrators":["a-reader"],"runtime_min":600,"sources":[{"type":"user"}],"work":"mageling"}`,
	}
	seedTree(t, dataDir, seed)

	books := `[{"asin":"B0MAGELUNA","title_short":"Mageling (Unabridged)","author":"Some Author","narrated_by":"A Reader","language":"english","region":"US","release_date":"2021-06-01","seconds":36000}]`
	sum, err := Run(writeBooks(t, books), Options{DataDir: dataDir, ImportDate: testImportDate})
	if err != nil {
		t.Fatal(err)
	}
	if sum.NewWorks != 0 || sum.NewRecordings != 0 || sum.MergedASINs != 1 {
		t.Fatalf("summary = %+v, want 0 work / 0 recording / 1 merged ASIN", sum)
	}
	if entryExists(t, dataDir, "works/ma/mageling-unabridged/work.json") {
		t.Errorf("a sibling work directory was created; works: %v", listWorks(t, dataDir))
	}
	const recAddress = "works/ma/mageling/recordings/a-reader-2021.json"
	got := asinsOf(t, dataDir, recAddress)
	if len(got) != 2 || got["B0MAGELING"] == "" || got["B0MAGELUNA"] == "" {
		t.Errorf("recording ASINs = %v, want both", got)
	}
	var rec struct {
		Narrators  []string `json:"narrators"`
		RuntimeMin int      `json:"runtime_min"`
		Work       string   `json:"work"`
	}
	readEntity(t, dataDir, recAddress, &rec)
	if rec.RuntimeMin != 600 || rec.Work != "mageling" || len(rec.Narrators) != 1 || rec.Narrators[0] != "a-reader" {
		t.Errorf("unmanaged fields changed on merge: %+v", rec)
	}
	if res := check.Load(dataDir); !res.OK() {
		t.Fatalf("merged tree failed validation: %v", res.Problems)
	}
}

func TestDifferentRuntimeMakesDistinctRecording(t *testing.T) {
	// Same work/narrator/year but runtimes 300 vs 500 min: a genuinely different
	// production -> two recordings under ONE work, no merge, no sibling work.
	books := `[
		{"asin":"B0DIVERGE1","title_short":"Divergent Tale","author":"Some Author","narrated_by":"A Reader","language":"english","region":"US","release_date":"2021-01-01","seconds":18000},
		{"asin":"B0DIVERGE2","title_short":"Divergent Tale (Unabridged)","author":"Some Author","narrated_by":"A Reader","language":"english","region":"US","release_date":"2021-06-01","seconds":30000}
	]`
	sum, dataDir := runImport(t, books, false)
	if sum.NewWorks != 1 || sum.NewRecordings != 2 || sum.MergedASINs != 0 {
		t.Fatalf("summary = %+v, want 1 work / 2 recordings / 0 merged", sum)
	}
	if recs := recSlugsOf(t, dataDir, "divergent-tale"); len(recs) != 2 {
		t.Fatalf("expected 2 recordings, got %v", recs)
	}
	if len(listWorks(t, dataDir)) == 0 {
		t.Fatalf("no works written")
	}
	for _, w := range listWorks(t, dataDir) {
		if strings.Contains(w, "divergent-tale-unabridged") {
			t.Errorf("edition variant minted a sibling work: %s", w)
		}
	}
	if res := check.Load(dataDir); !res.OK() {
		t.Fatalf("tree failed validation: %v", res.Problems)
	}
}

func TestSameEditionASINMergeIdempotent(t *testing.T) {
	// Re-running the same import is a no-op: both ASINs already present skip.
	books := `[
		{"asin":"B0MAGELING","title_short":"Mageling","author":"Some Author","narrated_by":"A Reader","language":"english","region":"US","release_date":"2021-01-01","seconds":36000},
		{"asin":"B0MAGELUNA","title_short":"Mageling (Unabridged)","author":"Some Author","narrated_by":"A Reader","language":"english","region":"US","release_date":"2021-06-01","seconds":36000}
	]`
	_, dataDir := runImport(t, books, false)
	sum2, err := Run(writeBooks(t, books), Options{DataDir: dataDir, ImportDate: testImportDate})
	if err != nil {
		t.Fatal(err)
	}
	if sum2.NewWorks != 0 || sum2.NewRecordings != 0 || sum2.MergedASINs != 0 {
		t.Errorf("re-run should be a no-op, got %+v", sum2)
	}
	if sum2.Skipped != 2 {
		t.Errorf("both ASINs should skip on re-run, Skipped = %d", sum2.Skipped)
	}
}

func TestReReleaseMergesIntoMatchingRuntimeSibling(t *testing.T) {
	// Fix 1: a work already has TWO same-narrator recordings (600 and 300 min).
	// A re-release with runtime 300 and a new ASIN must merge into the 300-min
	// sibling (the merge scans ALL same-narrator siblings, not just the first),
	// with no third recording minted.
	dataDir := t.TempDir()
	seed := map[string]string{
		"people/so/some-author.json":                         `{"id":"some-author","license":"CC0-1.0","name":"Some Author","sources":[{"type":"user"}]}`,
		"people/a-/a-reader.json":                            `{"id":"a-reader","license":"CC0-1.0","name":"A Reader","sources":[{"type":"user"}]}`,
		"works/tw/two-takes/work.json":                       `{"authors":["some-author"],"id":"two-takes","language":"en","license":"CC0-1.0","sources":[{"type":"user"}],"title":"Two Takes"}`,
		"works/tw/two-takes/recordings/a-reader-2021.json":   `{"asin":[{"asin":"B0FIRST001","region":"us"}],"id":"a-reader-2021","language":"en","license":"CC0-1.0","narrators":["a-reader"],"runtime_min":600,"sources":[{"type":"user"}],"work":"two-takes"}`,
		"works/tw/two-takes/recordings/a-reader-2021-2.json": `{"asin":[{"asin":"B0SECOND02","region":"us"}],"id":"a-reader-2021-2","language":"en","license":"CC0-1.0","narrators":["a-reader"],"runtime_min":300,"sources":[{"type":"user"}],"work":"two-takes"}`,
	}
	seedTree(t, dataDir, seed)

	// seconds 18000 = 300 min, matching the SECOND recording, not the first.
	books := `[{"asin":"B0RERELES1","title_short":"Two Takes","author":"Some Author","narrated_by":"A Reader","language":"english","region":"US","release_date":"2021-09-01","seconds":18000}]`
	sum, err := Run(writeBooks(t, books), Options{DataDir: dataDir, ImportDate: testImportDate})
	if err != nil {
		t.Fatal(err)
	}
	if sum.NewRecordings != 0 || sum.MergedASINs != 1 {
		t.Fatalf("summary = %+v, want 0 new recordings / 1 merged ASIN", sum)
	}
	if entryExists(t, dataDir, "works/tw/two-takes/recordings/a-reader-2021-3.json") {
		t.Errorf("a third recording was minted instead of merging into the runtime match")
	}
	// The 600-min recording is untouched; it must not have gained the re-release
	// ASIN.
	firstRaw := rawEntity(t, dataDir, "works/tw/two-takes/recordings/a-reader-2021.json")
	if !strings.Contains(string(firstRaw), "B0FIRST001") || strings.Contains(string(firstRaw), "B0RERELES1") {
		t.Errorf("600-min recording changed: %s", firstRaw)
	}
	second := asinsOf(t, dataDir, "works/tw/two-takes/recordings/a-reader-2021-2.json")
	if len(second) != 2 || second["B0SECOND02"] == "" || second["B0RERELES1"] == "" {
		t.Errorf("300-min recording ASINs = %v, want both B0SECOND02 and B0RERELES1", second)
	}
	if res := check.Load(dataDir); !res.OK() {
		t.Fatalf("merged tree failed validation: %v", res.Problems)
	}
}

func TestAbridgedConflictBlocksMerge(t *testing.T) {
	// Fix 2: "Foo" (no abridged field, runtime unknown) and "Foo (Abridged)"
	// (same narrator/year, new ASIN, runtime unknown) in one run must NOT merge -
	// an explicitly-abridged edition is a distinct production. Two recordings
	// result, and the abridged edition's recording carries abridged:true (derived
	// from the title marker).
	books := `[
		{"asin":"B0FOO00001","title_short":"Foo","author":"Some Author","narrated_by":"A Reader","language":"english","region":"US","release_date":"2021-01-01"},
		{"asin":"B0FOOABRD1","title_short":"Foo (Abridged)","author":"Some Author","narrated_by":"A Reader","language":"english","region":"US","release_date":"2021-01-01"}
	]`
	sum, dataDir := runImport(t, books, false)
	if sum.NewWorks != 1 || sum.NewRecordings != 2 || sum.MergedASINs != 0 {
		t.Fatalf("summary = %+v, want 1 work / 2 recordings / 0 merged", sum)
	}
	if recs := recSlugsOf(t, dataDir, "foo"); len(recs) != 2 {
		t.Fatalf("expected 2 recordings, got %v", recs)
	}
	// The abridged edition landed on the numeric-suffixed slug and must carry the
	// derived abridged:true; the plain "Foo" recording must have no abridged flag.
	abridged := readAbridged(t, dataDir, recAddr("foo", "a-reader-2021-2"))
	if abridged == nil || *abridged != true {
		t.Errorf("abridged edition recording abridged = %v, want true", abridged)
	}
	plain := readAbridged(t, dataDir, recAddr("foo", "a-reader-2021"))
	if plain != nil {
		t.Errorf("plain recording abridged = %v, want omitted (nil)", plain)
	}
	if res := check.Load(dataDir); !res.OK() {
		t.Fatalf("tree failed validation: %v", res.Problems)
	}
}

// readAbridged reads a recording's abridged tri-state (nil when the field is
// absent).
func readAbridged(t *testing.T, dataDir, address string) *bool {
	t.Helper()
	var rec struct {
		Abridged *bool `json:"abridged"`
	}
	readEntity(t, dataDir, address, &rec)
	return rec.Abridged
}

func TestAudiosiloBooksStripsEditionMarkerBeforeSplit(t *testing.T) {
	// Fix 3: an edition marker on the concatenated ABS title must be stripped
	// BEFORE the subtitle split and full-title concatenation, so it neither
	// defeats the split nor leaks into the work title / full title.
	cases := []struct {
		name       string
		title, sub string
		wantShort  string
		wantFull   string
	}{
		{
			name:      "marker on concatenated title",
			title:     "Fugitive Telemetry: Murderbot Diaries, Book 6 (Unabridged)",
			sub:       "Murderbot Diaries, Book 6",
			wantShort: "Fugitive Telemetry",
			wantFull:  "Fugitive Telemetry: Murderbot Diaries, Book 6",
		},
		{
			name:      "marker on bare title with distinct subtitle",
			title:     "Fugitive Telemetry (Unabridged)",
			sub:       "Murderbot Diaries, Book 6",
			wantShort: "Fugitive Telemetry",
			wantFull:  "Fugitive Telemetry: Murderbot Diaries, Book 6",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sb := audiosiloBookToBook(rawBook{"title": tc.title, "subtitle": tc.sub})
			if got := sb.str("title_short"); got != tc.wantShort {
				t.Errorf("title_short = %q, want %q", got, tc.wantShort)
			}
			full := sb.str("title")
			if full != tc.wantFull {
				t.Errorf("title = %q, want %q", full, tc.wantFull)
			}
			if strings.Contains(strings.ToLower(full), "abridged") {
				t.Errorf("full title still carries an edition marker: %q", full)
			}
		})
	}

	// End-to-end: the marker-on-concatenated-title case resolves to the clean
	// work slug.
	env := `{"format":"audiosilo-books","version":1,"books":[
		{"title":"Fugitive Telemetry: Murderbot Diaries, Book 6 (Unabridged)","subtitle":"Murderbot Diaries, Book 6","authors":["Martha Wells"],"narrators":["Kevin Free"],"language":"en","asin":"B0FUGITEL2","runtime_min":180}
	]}`
	_, dataDir := runWith(t, RunAudiosiloBooks, env, false)
	if !entryExists(t, dataDir, "works/fu/fugitive-telemetry/work.json") {
		t.Fatalf("marker not stripped from work slug; works: %v", listWorks(t, dataDir))
	}
}

func TestMergedASINGetsProvenance(t *testing.T) {
	// Fix 4: a merged re-release ASIN appends a provenance entry to the
	// recording's sources[] (auditable/retractable), referencing the merged ASIN.
	dataDir := t.TempDir()
	seed := map[string]string{
		"people/so/some-author.json":                      `{"id":"some-author","license":"CC0-1.0","name":"Some Author","sources":[{"type":"user"}]}`,
		"people/a-/a-reader.json":                         `{"id":"a-reader","license":"CC0-1.0","name":"A Reader","sources":[{"type":"user"}]}`,
		"works/ma/mageling/work.json":                     `{"authors":["some-author"],"id":"mageling","language":"en","license":"CC0-1.0","sources":[{"type":"user"}],"title":"Mageling"}`,
		"works/ma/mageling/recordings/a-reader-2021.json": `{"asin":[{"asin":"B0MAGELING","region":"us"}],"id":"a-reader-2021","language":"en","license":"CC0-1.0","narrators":["a-reader"],"runtime_min":600,"sources":[{"type":"user"}],"work":"mageling"}`,
	}
	seedTree(t, dataDir, seed)

	books := `[{"asin":"B0MAGELUNA","title_short":"Mageling (Unabridged)","author":"Some Author","narrated_by":"A Reader","language":"english","region":"US","release_date":"2021-06-01","seconds":36000}]`
	sum, err := Run(writeBooks(t, books), Options{DataDir: dataDir, ImportDate: testImportDate})
	if err != nil {
		t.Fatal(err)
	}
	if sum.MergedASINs != 1 {
		t.Fatalf("summary = %+v, want 1 merged ASIN", sum)
	}
	var rec struct {
		Sources []struct {
			Type string `json:"type"`
			Ref  string `json:"ref"`
		} `json:"sources"`
	}
	readEntity(t, dataDir, "works/ma/mageling/recordings/a-reader-2021.json", &rec)
	if len(rec.Sources) != 2 {
		t.Fatalf("sources = %+v, want 2 entries after merge", rec.Sources)
	}
	if rec.Sources[1].Ref != "B0MAGELUNA" {
		t.Errorf("merged source ref = %q, want the merged ASIN B0MAGELUNA", rec.Sources[1].Ref)
	}
	if res := check.Load(dataDir); !res.OK() {
		t.Fatalf("merged tree failed validation: %v", res.Problems)
	}
}

func TestAppendSourceUnique(t *testing.T) {
	// Re-stamping an identical type+ref (a re-import or a second backfill pass
	// over the same ASIN) is a no-op, so sources[] stays a set of distinct
	// auditable refs. A new type or a new ref is appended.
	existing := func() []any {
		return []any{
			map[string]any{"type": "audiosilo-books-import", "ref": "B0AAA", "imported_at": "2026-07-17"},
			map[string]any{"type": "audible-lookup", "ref": "B0AAA"},
		}
	}
	cases := []struct {
		name string
		src  OutSource
		want int
	}{
		{"exact dup type+ref", OutSource{Type: "audible-lookup", Ref: "B0AAA"}, 2},
		{"dup ignoring imported_at", OutSource{Type: "audiosilo-books-import", Ref: "B0AAA", ImportedAt: "2026-08-01"}, 2},
		{"new ref same type", OutSource{Type: "audible-lookup", Ref: "B0BBB"}, 3},
		{"new type same ref", OutSource{Type: "openlibrary", Ref: "B0AAA"}, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := appendSourceUnique(existing(), tc.src)
			if len(got) != tc.want {
				t.Errorf("len = %d, want %d (%v)", len(got), tc.want, got)
			}
		})
	}
}

func TestAddToSeriesRejectsEmptyName(t *testing.T) {
	// Defense in depth below the parsers' non-empty-name invariant: a direct
	// caller must never mint a nameless series (slug "series").
	p := &planner{series: map[string]*seriesState{}}
	var warned []string
	warn := func(format string, args ...any) { warned = append(warned, fmt.Sprintf(format, args...)) }
	p.addToSeries("", "some-work", "1", warn)
	if len(p.series) != 0 || p.summary.NewSeries != 0 {
		t.Errorf("empty series name minted a series: %+v", p.summary)
	}
	if len(warned) != 1 || !strings.Contains(warned[0], "empty series name") {
		t.Errorf("expected one empty-name warning, got %v", warned)
	}
}

// fileState is one file's content and modification time. The mod time is what
// lets a test prove a run wrote NOTHING at all, rather than only that it wrote
// the same bytes back.
type fileState struct {
	content string
	modTime int64
}

// treeSnapshot maps each data-relative path to its state.
type treeSnapshot map[string]fileState

// snapshotTree reads every file under dataDir into a snapshot, so a test can
// assert that a second run changed nothing at all.
func snapshotTree(t *testing.T, dataDir string) treeSnapshot {
	t.Helper()
	out := treeSnapshot{}
	err := filepath.Walk(dataDir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		raw, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		rel, rerr := filepath.Rel(dataDir, p)
		if rerr != nil {
			return rerr
		}
		out[filepath.ToSlash(rel)] = fileState{content: string(raw), modTime: info.ModTime().UnixNano()}
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot %s: %v", dataDir, err)
	}
	return out
}

// keysOf renders a snapshot's paths for a failure message.
func keysOf(snapshot treeSnapshot) []string {
	out := make([]string, 0, len(snapshot))
	for k := range snapshot {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestRejectedASINStaysAvailable(t *testing.T) {
	// An ASIN whose region did not map is recorded NOWHERE, so it must not be
	// claimed in the run's ASIN registry either: a later, well-formed entry for
	// the same book would otherwise dedupe against a recording that does not
	// carry the ASIN, and the run would lose it entirely.
	books := `[
		{"asin":"B0LOSTASIN","title_short":"Wandering Book","author":"Some Author","narrated_by":"A Reader","language":"english","region":"narnia","release_date":"2021-01-01"},
		{"asin":"B0LOSTASIN","title_short":"Wandering Book","author":"Some Author","narrated_by":"A Reader","language":"english","region":"us","release_date":"2021-01-01"}
	]`
	sum, dataDir := runImport(t, books, false)
	if !hasWarning(sum.Warnings, "not a known marketplace") {
		t.Errorf("expected a region warning, got %v", sum.Warnings)
	}
	if sum.Skipped != 0 {
		t.Errorf("Skipped = %d; the second entry must not dedupe against an unrecorded ASIN", sum.Skipped)
	}
	if sum.MergedASINs != 1 {
		t.Errorf("MergedASINs = %d, want the good entry's ASIN merged into the recording", sum.MergedASINs)
	}
	asins := asinsOf(t, dataDir, "works/wa/wandering-book/recordings/a-reader-2021.json")
	if asins["B0LOSTASIN"] != "us" {
		t.Errorf("asins = %v, want B0LOSTASIN recorded under us", asins)
	}
	if res := check.Load(dataDir); !res.OK() {
		t.Fatalf("tree failed validation: %v", res.Problems)
	}
}

func TestCrossYearSiblingMerges(t *testing.T) {
	// The recording slug embeds the release YEAR, so a production released in the
	// US in December and in the UK the following January lands on two unrelated
	// slug chains. The same-narrator scan therefore looks at ALL of the work's
	// recordings: the UK re-release must merge its ASIN into the existing
	// recording, not mint a second one for the same production.
	books := `[
		{"asin":"B0DEC20190","title_short":"One Production","author":"Some Author","narrated_by":"A Reader","language":"english","region":"US","release_date":"2019-12-15","seconds":36000},
		{"asin":"B0JAN20200","title_short":"One Production","author":"Some Author","narrated_by":"A Reader","language":"english","region":"UK","release_date":"2020-01-05","seconds":36000}
	]`
	sum, dataDir := runImport(t, books, false)
	if sum.NewWorks != 1 || sum.NewRecordings != 1 || sum.MergedASINs != 1 {
		t.Fatalf("summary = %+v, want 1 work / 1 recording / 1 merged ASIN", sum)
	}
	if entryExists(t, dataDir, "works/on/one-production/recordings/a-reader-2020.json") {
		t.Errorf("a second recording was minted for the same production")
	}
	asins := asinsOf(t, dataDir, "works/on/one-production/recordings/a-reader-2019.json")
	if asins["B0DEC20190"] != "us" || asins["B0JAN20200"] != "uk" {
		t.Errorf("asins = %v, want both releases on one recording", asins)
	}
	if res := check.Load(dataDir); !res.OK() {
		t.Fatalf("tree failed validation: %v", res.Problems)
	}
}

func TestCrossYearDifferentRuntimeStaysDistinct(t *testing.T) {
	// The runtime guard is still the correctness gate for the widened scan: a
	// genuinely different production of the same book by the same narrator gets
	// its own recording even though the scan now considers it a candidate.
	books := `[
		{"asin":"B0LONG2019","title_short":"Two Productions","author":"Some Author","narrated_by":"A Reader","language":"english","region":"US","release_date":"2019-12-15","seconds":36000},
		{"asin":"B0SHRT2020","title_short":"Two Productions","author":"Some Author","narrated_by":"A Reader","language":"english","region":"UK","release_date":"2020-01-05","seconds":18000}
	]`
	sum, dataDir := runImport(t, books, false)
	if sum.NewRecordings != 2 || sum.MergedASINs != 0 {
		t.Fatalf("summary = %+v, want 2 distinct recordings", sum)
	}
	if !entryExists(t, dataDir, "works/tw/two-productions/recordings/a-reader-2020.json") {
		t.Errorf("the second production did not get its own recording")
	}
}

func TestMergedRecordingCarriesISBN(t *testing.T) {
	// A merged re-release brings its ISBN along: it identifies the same edition,
	// and no later run would restore a silently dropped one. A duplicate ISBN
	// (already recorded elsewhere) is still refused, with the warning.
	dataDir := t.TempDir()
	seedTree(t, dataDir, map[string]string{
		"people/so/some-author.json":                       `{"id":"some-author","license":"CC0-1.0","name":"Some Author","sources":[{"type":"user"}]}`,
		"people/a-/a-reader.json":                          `{"id":"a-reader","license":"CC0-1.0","name":"A Reader","sources":[{"type":"user"}]}`,
		"works/re/rerelease/work.json":                     `{"authors":["some-author"],"id":"rerelease","language":"en","license":"CC0-1.0","sources":[{"type":"user"}],"title":"Rerelease"}`,
		"works/re/rerelease/recordings/a-reader-2021.json": `{"asin":[{"asin":"B0FIRST001","region":"us"}],"id":"a-reader-2021","isbn":["9780306406157"],"language":"en","license":"CC0-1.0","narrators":["a-reader"],"sources":[{"type":"user"}],"work":"rerelease"}`,
		"works/ol/older/work.json":                         `{"authors":["some-author"],"id":"older","language":"en","license":"CC0-1.0","sources":[{"type":"user"}],"title":"Older"}`,
		"works/ol/older/recordings/only.json":              `{"id":"only","isbn":["9781234567897"],"language":"en","license":"CC0-1.0","narrators":["a-reader"],"sources":[{"type":"user"}],"work":"older"}`,
	})

	export := `[{"asin":"B0LIBEX080","title":"Rerelease","region":"uk","language":"english","isbn":["9780007560776","9781234567897"],` +
		`"authors":[{"name":"Some Author"}],"narrators":[{"name":"A Reader"}],"releaseDate":"2021-05-01T00:00:00Z"}]`
	sum, err := RunLibex(writeBooks(t, export), Options{DataDir: dataDir, ImportDate: testImportDate})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if sum.MergedASINs != 1 || sum.NewRecordings != 0 {
		t.Fatalf("summary = %+v, want a merge", sum)
	}
	if !hasWarning(sum.Warnings, "ISBN 9781234567897 is already recorded") {
		t.Errorf("expected the duplicate ISBN to be refused, got %v", sum.Warnings)
	}
	var rec struct {
		ISBN []string `json:"isbn"`
	}
	readEntity(t, dataDir, "works/re/rerelease/recordings/a-reader-2021.json", &rec)
	if !reflect.DeepEqual(rec.ISBN, []string{"9780306406157", "9780007560776"}) {
		t.Errorf("isbn = %v, want the incumbent's plus the merged one", rec.ISBN)
	}
	if res := check.Load(dataDir); !res.OK() {
		t.Fatalf("merged tree failed validation: %v", res.Problems)
	}
}
