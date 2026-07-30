package importer

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/check"
)

// The recordings-only tests run against a seeded catalogue modeled on the real
// one: two Harry Potter works, the second book carrying ONLY the Stephen Fry
// narration (so the Jim Dale one is genuinely missing) and the fourth book
// carrying its own, so an excerpt of it has a work to wrongly match if the
// title matching is too loose.
const (
	hpAuthor        = `{"id":"j-k-rowling","license":"CC0-1.0","name":"J.K. Rowling","sources":[{"type":"user"}]}`
	hpFryPerson     = `{"id":"stephen-fry","license":"CC0-1.0","name":"Stephen Fry","sources":[{"type":"user"}]}`
	hpDalePerson    = `{"id":"jim-dale","license":"CC0-1.0","name":"Jim Dale","sources":[{"type":"user"}]}`
	hpChamberWork   = `{"authors":["j-k-rowling"],"id":"harry-potter-and-the-chamber-of-secrets","language":"en","license":"CC0-1.0","sources":[{"type":"user"}],"title":"Harry Potter and the Chamber of Secrets"}`
	hpChamberFry    = `{"asin":[{"asin":"B017V6627U","region":"us"}],"id":"stephen-fry-2015","language":"en","license":"CC0-1.0","narrators":["stephen-fry"],"runtime_min":583,"sources":[{"type":"user"}],"work":"harry-potter-and-the-chamber-of-secrets"}`
	hpGobletWork    = `{"authors":["j-k-rowling"],"id":"harry-potter-and-the-goblet-of-fire","language":"en","license":"CC0-1.0","sources":[{"type":"user"}],"title":"Harry Potter and the Goblet of Fire"}`
	hpGobletDale    = `{"asin":[{"asin":"B017V4NUPO","region":"us"}],"id":"jim-dale-2015","language":"en","license":"CC0-1.0","narrators":["jim-dale"],"runtime_min":1257,"sources":[{"type":"user"}],"work":"harry-potter-and-the-goblet-of-fire"}`
	hpChamberSeries = `{"id":"harry-potter","license":"CC0-1.0","name":"Harry Potter","sources":[{"type":"user"}],"works":[{"position":"2","work":"harry-potter-and-the-chamber-of-secrets"},{"position":"4","work":"harry-potter-and-the-goblet-of-fire"}]}`
)

const (
	hpChamberWorkRel = "works/ha/harry-potter-and-the-chamber-of-secrets/work.json"
	hpChamberFryRel  = "works/ha/harry-potter-and-the-chamber-of-secrets/recordings/stephen-fry-2015.json"
	hpChamberDaleRel = "works/ha/harry-potter-and-the-chamber-of-secrets/recordings/jim-dale-2015.json"
	hpChamberCastRel = "works/ha/harry-potter-and-the-chamber-of-secrets/recordings/alex-hassell-2025.json"
	hpGobletWorkRel  = "works/ha/harry-potter-and-the-goblet-of-fire/work.json"
	hpGobletDaleRel  = "works/ha/harry-potter-and-the-goblet-of-fire/recordings/jim-dale-2015.json"
	hpSeriesRel      = "series/ha/harry-potter.json"
)

// recordingsSeedFiles is the base catalogue every test in this file runs
// against.
func recordingsSeedFiles() map[string]string {
	return map[string]string{
		"people/j-/j-k-rowling.json": hpAuthor,
		"people/st/stephen-fry.json": hpFryPerson,
		"people/ji/jim-dale.json":    hpDalePerson,
		hpChamberWorkRel:             hpChamberWork,
		hpChamberFryRel:              hpChamberFry,
		hpGobletWorkRel:              hpGobletWork,
		hpGobletDaleRel:              hpGobletDale,
		hpSeriesRel:                  hpChamberSeries,
	}
}

// seedRecordingsTree writes the base catalogue into a fresh temp data dir.
func seedRecordingsTree(t *testing.T) string {
	t.Helper()
	dataDir := t.TempDir()
	seedTree(t, dataDir, recordingsSeedFiles())
	return dataDir
}

// runRecordingsOnly runs the libex importer in recordings-only mode against
// dataDir.
func runRecordingsOnly(t *testing.T, dataDir, exportJSON string, dryRun bool) Summary {
	t.Helper()
	sum, err := RunLibex(writeBooks(t, exportJSON), Options{
		DataDir: dataDir, ImportDate: testImportDate, DryRun: dryRun, Mode: ModeRecordingsOnly,
	})
	if err != nil {
		t.Fatalf("recordings-only run: %v", err)
	}
	return sum
}

// recordingsFixture is the committed NDJSON fixture: four rows trimmed by hand
// from the real libex dump. Only the full-cast row's narrator list is shortened
// (the release credits fifteen); every other field is verbatim.
func recordingsFixture(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("testdata/libex_recordings_only.ndjson")
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// TestRecordingsOnlyAddsAlternateNarrations is the mode's acceptance test. Four
// real-shaped rows against a catalogue holding the Chamber of Secrets with only
// the Fry narration must produce exactly two new recordings under that work -
// the Jim Dale narration (whose ", Book 2" volume marker has to be seen through)
// and the full-cast one (whose "(Full-Cast Edition)" qualifier has to be) -
// while neither of the other two creates anything.
//
// The two rejected rows are refused at different layers, which is deliberate:
// the excerpt reaches this planner and lands in the no-work bucket (an excerpt
// is not the work), while the trivia title never gets that far - it is narrated
// by "Virtual Voice" in the real dump, so libex.go's AI-narrator rule refuses it
// at parse time and it is counted in SkippedRows. That a companion title also
// fails to resolve by TITLE is pinned by TestWorkTitleCandidates.
func TestRecordingsOnlyAddsAlternateNarrations(t *testing.T) {
	dataDir := seedRecordingsTree(t)
	before := snapshotTree(t, dataDir)

	sum := runRecordingsOnly(t, dataDir, recordingsFixture(t), false)

	if sum.NewRecordings != 2 {
		t.Errorf("NewRecordings = %d, want 2", sum.NewRecordings)
	}
	if sum.SkippedNoWork != 1 {
		t.Errorf("SkippedNoWork = %d, want 1 (the excerpt)", sum.SkippedNoWork)
	}
	if sum.SkippedRows != 1 {
		t.Errorf("SkippedRows = %d, want 1 (the AI-narrated trivia title)", sum.SkippedRows)
	}
	if sum.NewWorks != 0 || sum.NewSeries != 0 {
		t.Errorf("the mode created a work or a series: NewWorks=%d NewSeries=%d", sum.NewWorks, sum.NewSeries)
	}
	// The three full-cast narrators only; the excerpt's and the trivia row's
	// people are never created, because the work match runs first.
	if sum.NewPeople != 3 {
		t.Errorf("NewPeople = %d, want 3 (the full-cast narrators)", sum.NewPeople)
	}
	if sum.MergedASINs != 0 {
		t.Errorf("MergedASINs = %d, want 0", sum.MergedASINs)
	}

	if res := check.Load(dataDir); !res.OK() {
		t.Fatalf("tree failed validation after the run:\n%v", res.Problems)
	}

	// The Jim Dale narration is a NEW recording under the EXISTING work, not a
	// new work: ", Book 2" is a volume marker, not part of the title.
	var dale recordingFile
	readJSON(t, filepath.Join(dataDir, hpChamberDaleRel), &dale)
	if dale.Work != "harry-potter-and-the-chamber-of-secrets" {
		t.Errorf("jim dale recording work = %q", dale.Work)
	}
	if !reflect.DeepEqual(dale.Narrators, []string{"jim-dale"}) {
		t.Errorf("jim dale narrators = %v", dale.Narrators)
	}
	if dale.RuntimeMin != 542 || dale.ReleaseDate != "2015-11-20" || dale.Publisher != "Pottermore Publishing" {
		t.Errorf("jim dale facts = %+v", dale)
	}
	if dale.Abridged == nil || *dale.Abridged {
		t.Errorf("jim dale abridged = %v, want a stated false", dale.Abridged)
	}
	if len(dale.ASIN) != 1 || dale.ASIN[0].ASIN != "B017V4IWVG" || dale.ASIN[0].Region != "us" {
		t.Errorf("jim dale asin = %+v", dale.ASIN)
	}
	if dale.License != "CC0-1.0" {
		t.Errorf("jim dale license = %q", dale.License)
	}
	if len(dale.Sources) != 1 || dale.Sources[0].Type != "libex-import" ||
		dale.Sources[0].Ref != "B017V4IWVG" || dale.Sources[0].ImportedAt != testImportDate {
		t.Errorf("jim dale sources = %+v", dale.Sources)
	}

	// The full-cast edition is another recording under the same work.
	var cast recordingFile
	readJSON(t, filepath.Join(dataDir, hpChamberCastRel), &cast)
	if cast.Work != "harry-potter-and-the-chamber-of-secrets" {
		t.Errorf("full-cast recording work = %q", cast.Work)
	}
	if !reflect.DeepEqual(cast.Narrators, []string{"alex-hassell", "arabella-stanton", "cush-jumbo"}) {
		t.Errorf("full-cast narrators = %v", cast.Narrators)
	}
	if cast.RuntimeMin != 577 {
		t.Errorf("full-cast runtime = %d, want 577", cast.RuntimeMin)
	}

	// Nothing else moved: no new work directory, and the seeded files (the works,
	// the Fry recording, the series) are byte-for-byte what they were.
	for rel, was := range before {
		now, err := os.ReadFile(filepath.Join(dataDir, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("seeded file %s disappeared: %v", rel, err)
		}
		if string(now) != was.content {
			t.Errorf("seeded file %s was rewritten:\n%s", rel, now)
		}
	}
	for _, rel := range []string{
		"works/ha/harry-potter-and-the-chamber-of-secrets-book-2",
		"works/ha/harry-potter-and-the-goblet-of-fire-book-4-excerpt",
		"works/ha/harry-potter-and-the-chamber-of-secrets-ultimate-trivia-test",
	} {
		if exists(filepath.Join(dataDir, filepath.FromSlash(rel))) {
			t.Errorf("the run created work directory %s", rel)
		}
	}

	// A second identical run changes nothing: both new ASINs are catalogued now,
	// so they dedupe, and the two unmatched rows are unmatched again.
	after := snapshotTree(t, dataDir)
	sum2 := runRecordingsOnly(t, dataDir, recordingsFixture(t), false)
	if sum2.NewRecordings != 0 || sum2.NewPeople != 0 || sum2.MergedASINs != 0 {
		t.Errorf("second run was not a no-op: %+v", sum2)
	}
	if sum2.Skipped != 2 || sum2.SkippedNoWork != 1 {
		t.Errorf("second run counters = %d skipped / %d no-work, want 2/1", sum2.Skipped, sum2.SkippedNoWork)
	}
	if got := snapshotTree(t, dataDir); !reflect.DeepEqual(got, after) {
		t.Error("a second identical run changed the tree")
	}
}

// TestRecordingsOnlyMergesRegionalASIN pins the other half of the mode: a
// regional re-release of a narration the catalogue ALREADY has must fold its
// ASIN into that recording (the shared same-narrator merge, with its runtime
// guard) rather than mint a second recording of the same production.
func TestRecordingsOnlyMergesRegionalASIN(t *testing.T) {
	dataDir := seedRecordingsTree(t)
	// The real Canadian Jim Dale Goblet of Fire row: a different ASIN for the
	// production the catalogue holds at 1257 minutes.
	row := `{"asin":"B071S4H4GF","title":"Harry Potter and the Goblet of Fire, Book 4","region":"ca","publisher":"Pottermore Publishing","language":"english","bookFormat":"unabridged","releaseDate":"2015-11-20 00:00:00+00","lengthMinutes":1240,"authors":[{"name":"J.K. Rowling"}],"narrators":[{"name":"Jim Dale"}],"series":[{"name":"Harry Potter","position":"4"}]}`

	sum := runRecordingsOnly(t, dataDir, row, false)

	if sum.MergedASINs != 1 {
		t.Errorf("MergedASINs = %d, want 1", sum.MergedASINs)
	}
	if sum.NewRecordings != 0 || sum.NewWorks != 0 || sum.SkippedNoWork != 0 {
		t.Errorf("the merge created something: %+v", sum)
	}

	var rec recordingFile
	readJSON(t, filepath.Join(dataDir, hpGobletDaleRel), &rec)
	if len(rec.ASIN) != 2 {
		t.Fatalf("asin = %+v, want the seeded one plus the merged one", rec.ASIN)
	}
	if rec.ASIN[1].ASIN != "B071S4H4GF" || rec.ASIN[1].Region != "ca" {
		t.Errorf("merged asin = %+v", rec.ASIN[1])
	}
	// The merge is auditable: it appends this row's provenance rather than
	// silently editing the record.
	if len(rec.Sources) != 2 || rec.Sources[1].Type != "libex-import" || rec.Sources[1].Ref != "B071S4H4GF" {
		t.Errorf("merge sources = %+v", rec.Sources)
	}
	// The runtime the record already stated wins - a merge carries the ASIN, not
	// a re-statement of the production's facts.
	if rec.RuntimeMin != 1257 {
		t.Errorf("runtime = %d, want the recorded 1257", rec.RuntimeMin)
	}
}

// TestRecordingsOnlyNeverCreatesAWork is the mode's load-bearing refusal: a row
// for a book the catalogue simply does not hold must be counted and dropped, not
// created. Without this the mode would silently become the create path with a
// different name.
func TestRecordingsOnlyNeverCreatesAWork(t *testing.T) {
	dataDir := seedRecordingsTree(t)
	before := snapshotTree(t, dataDir)
	row := `{"asin":"B0NOTHERE1","title":"A Book Nobody Catalogued","region":"us","language":"english","bookFormat":"unabridged","releaseDate":"2024-01-01 00:00:00+00","lengthMinutes":300,"authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":"Bea Reader"}],"series":[{"name":"Cartographer Chronicles","position":"1"}]}`

	sum := runRecordingsOnly(t, dataDir, row, false)

	if sum.SkippedNoWork != 1 {
		t.Errorf("SkippedNoWork = %d, want 1", sum.SkippedNoWork)
	}
	if sum.NewWorks != 0 || sum.NewRecordings != 0 || sum.NewPeople != 0 || sum.NewSeries != 0 {
		t.Errorf("the mode created records for an uncatalogued book: %+v", sum)
	}
	if got := snapshotTree(t, dataDir); !reflect.DeepEqual(got, before) {
		t.Error("the run wrote to the tree despite matching no work")
	}
	// The rows it could not place are reported in aggregate, with examples.
	if len(sum.Warnings) == 0 || !strings.Contains(strings.Join(sum.Warnings, "\n"), "1 rows matched no catalogued work") {
		t.Errorf("warnings do not report the unmatched row: %v", sum.Warnings)
	}
	if !strings.Contains(strings.Join(sum.Warnings, "\n"), "B0NOTHERE1") {
		t.Errorf("the unmatched-row warning names no example: %v", sum.Warnings)
	}
}

// TestRecordingsOnlyRequiresTheSameAuthorSet pins the identity half of the
// match: a same-titled book by someone else is a different work, so it must not
// acquire a narration of ours.
func TestRecordingsOnlyRequiresTheSameAuthorSet(t *testing.T) {
	dataDir := seedRecordingsTree(t)
	row := `{"asin":"B0IMPOSTR1","title":"Harry Potter and the Chamber of Secrets, Book 2","region":"us","language":"english","bookFormat":"unabridged","releaseDate":"2024-01-01 00:00:00+00","lengthMinutes":500,"authors":[{"name":"Someone Else"}],"narrators":[{"name":"Jim Dale"}]}`

	sum := runRecordingsOnly(t, dataDir, row, false)

	if sum.SkippedNoWork != 1 || sum.NewRecordings != 0 {
		t.Errorf("a different author set matched the work: %+v", sum)
	}
	if exists(filepath.Join(dataDir, filepath.FromSlash(hpChamberDaleRel))) {
		t.Error("a differently-authored row added a recording to our work")
	}
}

// TestRecordingsOnlyDryRunWritesNothing pins that the plan is inspectable
// without touching the tree, the way every other mode's dry run is.
func TestRecordingsOnlyDryRunWritesNothing(t *testing.T) {
	dataDir := seedRecordingsTree(t)
	before := snapshotTree(t, dataDir)

	sum := runRecordingsOnly(t, dataDir, recordingsFixture(t), true)

	if sum.NewRecordings != 2 || sum.SkippedNoWork != 1 {
		t.Errorf("dry-run plan = %+v, want the same 2 recordings / 1 unmatched", sum)
	}
	if got := snapshotTree(t, dataDir); !reflect.DeepEqual(got, before) {
		t.Error("a dry run wrote to the tree")
	}
}

// TestRecordingsOnlyResolvesCollisionSuffixedWorks pins the slug-candidate
// walk. A work whose bare title slug was claimed by a different author's book is
// stored under "<title>-<author>", and probing only the bare slug would make
// that work - and every alternate narration of it - permanently invisible to
// this mode.
func TestRecordingsOnlyResolvesCollisionSuffixedWorks(t *testing.T) {
	// "Chamber of Secrets" is squatted by a different author's book, so ours
	// lives at the author-suffixed slug exactly as getOrCreateWork would mint it.
	const squatterWork = `{"authors":["someone-else"],"id":"chamber-of-secrets","language":"en","license":"CC0-1.0","sources":[{"type":"user"}],"title":"Chamber of Secrets"}`
	const suffixedWork = `{"authors":["j-k-rowling"],"id":"chamber-of-secrets-j-k-rowling","language":"en","license":"CC0-1.0","sources":[{"type":"user"}],"title":"Chamber of Secrets"}`
	dataDir := seedRecordingsTree(t)
	seedTree(t, dataDir, map[string]string{
		"people/so/someone-else.json":                       `{"id":"someone-else","license":"CC0-1.0","name":"Someone Else","sources":[{"type":"user"}]}`,
		"works/ch/chamber-of-secrets/work.json":             squatterWork,
		"works/ch/chamber-of-secrets-j-k-rowling/work.json": suffixedWork,
	})
	row := `{"asin":"B0SUFFIXD1","title":"Chamber of Secrets, Book 2","region":"us","language":"english","bookFormat":"unabridged","releaseDate":"2015-11-20 00:00:00+00","lengthMinutes":542,"authors":[{"name":"J.K. Rowling"}],"narrators":[{"name":"Jim Dale"}]}`

	sum := runRecordingsOnly(t, dataDir, row, false)

	if sum.SkippedNoWork != 0 || sum.NewRecordings != 1 {
		t.Fatalf("the author-suffixed work was not found: %+v", sum)
	}
	var rec recordingFile
	readJSON(t, filepath.Join(dataDir, "works/ch/chamber-of-secrets-j-k-rowling/recordings/jim-dale-2015.json"), &rec)
	if rec.Work != "chamber-of-secrets-j-k-rowling" {
		t.Errorf("recording landed under %q", rec.Work)
	}
	// And the squatter is untouched: the author set is still the identity.
	if exists(filepath.Join(dataDir, "works/ch/chamber-of-secrets/recordings/jim-dale-2015.json")) {
		t.Error("the recording landed under the different author's work")
	}
}

// TestRecordingsOnlyDoesNotWarnAboutRowsItNeverWanted pins the step ORDER. The
// mode's natural input is an unfiltered export, so the language and narrator
// checks must run only for rows that already matched a work - otherwise every
// Finnish edition and every narrator-less row in a million-row dump earns a
// per-row line about a book we were never importing.
func TestRecordingsOnlyDoesNotWarnAboutRowsItNeverWanted(t *testing.T) {
	dataDir := seedRecordingsTree(t)
	rows := strings.Join([]string{
		// Unmatched AND unusable: an unknown language and no narrator. Neither
		// may produce a per-row warning.
		`{"asin":"B0FINNISH1","title":"Jokin Kirja","region":"us","language":"finnish","lengthMinutes":300,"authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":"Bea Reader"}]}`,
		`{"asin":"B0NONARRA1","title":"A Book Nobody Catalogued","region":"us","language":"english","lengthMinutes":300,"authors":[{"name":"Ada Mapmaker"}],"narrators":[]}`,
	}, "\n")

	sum := runRecordingsOnly(t, dataDir, rows, false)

	if sum.SkippedNoWork != 2 {
		t.Errorf("SkippedNoWork = %d, want 2", sum.SkippedNoWork)
	}
	if len(sum.Warnings) != 1 {
		t.Fatalf("want exactly the one aggregated warning, got %v", sum.Warnings)
	}
	if !strings.Contains(sum.Warnings[0], "2 rows matched no catalogued work") {
		t.Errorf("warning = %q", sum.Warnings[0])
	}

	// The same two failures on a row that DOES match a work still warn per row -
	// there the operator asked for the book, so the reason it was dropped is
	// exactly what they need.
	matching := `{"asin":"B0MATCHBD1","title":"Harry Potter and the Chamber of Secrets, Book 2","region":"us","language":"finnish","lengthMinutes":542,"authors":[{"name":"J.K. Rowling"}],"narrators":[{"name":"Jim Dale"}]}`
	sum = runRecordingsOnly(t, dataDir, matching, false)
	if sum.SkippedNoWork != 0 || sum.NewRecordings != 0 {
		t.Errorf("summary = %+v", sum)
	}
	if len(sum.Warnings) != 1 || !strings.Contains(sum.Warnings[0], `unknown language "finnish"`) {
		t.Errorf("a matched row's rejection must be reported per row: %v", sum.Warnings)
	}
}

// TestRecordingsOnlyCapsUnmatchedExamples pins that the aggregate warning
// retains only the handful of labels it prints. The mode reads exports in which
// nearly every row is unmatched, so retaining one string per row would grow the
// slice to the size of the input for output nobody sees.
func TestRecordingsOnlyCapsUnmatchedExamples(t *testing.T) {
	var rows []string
	for i := 0; i < maxWarnExamples+3; i++ {
		rows = append(rows, fmt.Sprintf(
			`{"asin":"B0NOWRK%03d","title":"Uncatalogued Number %d","region":"us","language":"english","lengthMinutes":300,"authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":"Bea Reader"}]}`, i, i))
	}
	dataDir := seedRecordingsTree(t)

	sum := runRecordingsOnly(t, dataDir, strings.Join(rows, "\n"), false)

	if sum.SkippedNoWork != maxWarnExamples+3 {
		t.Errorf("SkippedNoWork = %d, want %d", sum.SkippedNoWork, maxWarnExamples+3)
	}
	if len(sum.Warnings) != 1 {
		t.Fatalf("warnings = %v", sum.Warnings)
	}
	// The COUNT is every unmatched row; the EXAMPLES are capped.
	if !strings.Contains(sum.Warnings[0], fmt.Sprintf("%d rows matched no catalogued work", maxWarnExamples+3)) {
		t.Errorf("the count is not the full tally: %q", sum.Warnings[0])
	}
	if named := strings.Count(sum.Warnings[0], "B0NOWRK"); named != maxWarnExamples {
		t.Errorf("named %d examples, want %d: %q", named, maxWarnExamples, sum.Warnings[0])
	}
}

// TestWorkTitleCandidates pins the title matching each of the three real Harry
// Potter shapes depends on, plus the ordering rule that keeps the undecorated
// form a fallback rather than a rewrite.
func TestWorkTitleCandidates(t *testing.T) {
	cases := []struct {
		name  string
		short string
		full  string
		want  []string
	}{
		{
			name:  "trailing volume marker",
			short: "Harry Potter and the Chamber of Secrets, Book 2",
			want: []string{
				"Harry Potter and the Chamber of Secrets, Book 2",
				"Harry Potter and the Chamber of Secrets",
			},
		},
		{
			name:  "production qualifier",
			short: "Harry Potter and the Chamber of Secrets (Full-Cast Edition)",
			want: []string{
				"Harry Potter and the Chamber of Secrets (Full-Cast Edition)",
				"Harry Potter and the Chamber of Secrets",
			},
		},
		{
			// An excerpt is not the work. "(Excerpt)" is not a production
			// qualifier, and it also blocks the volume marker from being trailing,
			// so the row can only match a work actually called this.
			name:  "excerpt keeps its whole title",
			short: "Harry Potter and the Goblet of Fire, Book 4 (Excerpt)",
			want:  []string{"Harry Potter and the Goblet of Fire, Book 4 (Excerpt)"},
		},
		{
			// A companion title is a different book that merely quotes ours; no
			// rule fires on it, so it can only match a work actually called this.
			name:  "companion title is not the work",
			short: "Harry Potter and The Chamber of Secrets Ultimate Trivia Test",
			want:  []string{"Harry Potter and The Chamber of Secrets Ultimate Trivia Test"},
		},
		{
			name:  "both decorations strip",
			short: "The Lost Cartographer, Book 3 (Full-Cast Edition)",
			want: []string{
				"The Lost Cartographer, Book 3 (Full-Cast Edition)",
				"The Lost Cartographer, Book 3",
				"The Lost Cartographer",
			},
		},
		{
			// A sequel whose own name ends in "Book <N>" prints no separator, so
			// nothing strips: "The Jungle Book" must never become a candidate, or
			// the sequel's narration would land on the base work.
			name:  "a sequel title is never stripped",
			short: "The Jungle Book 2",
			want:  []string{"The Jungle Book 2"},
		},
		{
			// Nothing marker-shaped at all.
			name:  "an ordinary title is left alone",
			short: "Ready Player One",
			want:  []string{"Ready Player One"},
		},
		{
			name:  "the fuller title seeds the chain too",
			short: "The Lost Cartographer",
			full:  "The Lost Cartographer: A Tale of Maps",
			want: []string{
				"The Lost Cartographer",
				"The Lost Cartographer: A Tale of Maps",
			},
		},
		{
			// Nothing to strip, and no duplicate candidate when the two title
			// fields agree.
			name:  "undecorated title",
			short: "Harry Potter and the Chamber of Secrets",
			full:  "Harry Potter and the Chamber of Secrets",
			want:  []string{"Harry Potter and the Chamber of Secrets"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := rawBook{"title_short": tc.short}
			if tc.full != "" {
				raw["title"] = tc.full
			}
			got := workTitleCandidates(sourceBook{raw: raw})
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("candidates = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestStripVolumeMarker pins the volume-marker rule's edges. The REFUSALS carry
// the weight: a marker missed only sends the row to SkippedNoWork, while a strip
// that eats part of a title's own name attaches a narration to the wrong work.
func TestStripVolumeMarker(t *testing.T) {
	cases := []struct{ in, want string }{
		// Every separator the dump prints, tight or spaced on either side.
		{"Title, Book 2", "Title"},
		{"Title - Book 2", "Title"},
		{"Title: Volume 3", "Title"},
		{"Title; Book 2", "Title"},
		{"Title – Book 2", "Title"},
		{"Title-Book 2", "Title"},
		{"Title , Book 2", "Title"},
		{"Title - Vol. 4", "Title"},
		{"Title, Book 2.5", "Title"},
		{"Title, BOOK 2", "Title"},
		// No separator: the number is part of the title's own name. "The Jungle
		// Book 2" is a sequel, and the dump's separator-less rows are mostly
		// anthology titles that would otherwise collapse onto a base title.
		{"The Jungle Book 2", "The Jungle Book 2"},
		{"33 Essays on Nondual Spirituality Volume 2", "33 Essays on Nondual Spirituality Volume 2"},
		{"Title Vol. 4", "Title Vol. 4"},
		// "part" is never stripped: a split release's half is not the whole work.
		{"Title, Part 2", "Title, Part 2"},
		// A marker with no number is just words.
		{"The Jungle Book", "The Jungle Book"},
		{"Ready Player One", "Ready Player One"},
		// Mid-title markers are untouched - only a TRAILING one decorates.
		{"Book 2 of the Series", "Book 2 of the Series"},
		// Stripping would leave nothing, so nothing is stripped.
		{"Book 2", "Book 2"},
		{", Book 2", ", Book 2"},
	}
	for _, tc := range cases {
		if got := stripVolumeMarker(tc.in); got != tc.want {
			t.Errorf("stripVolumeMarker(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestStripProductionQualifier pins the tiny, deliberately closed qualifier
// list. Widening it is what would let an excerpt or an adaptation impersonate
// the work, so the refusals matter more than the acceptances.
func TestStripProductionQualifier(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Title (Full-Cast Edition)", "Title"},
		{"Title (Full Cast Edition)", "Title"},
		{"Title [full-cast edition]", "Title"},
		{"Title  ( Full-Cast   Edition ) ", "Title"},
		// Not production qualifiers: these rows are not the work.
		{"Title (Excerpt)", "Title (Excerpt)"},
		{"Title (Dramatized Adaptation)", "Title (Dramatized Adaptation)"},
		{"Title (Narrated by Stephen Fry)", "Title (Narrated by Stephen Fry)"},
		{"Title", "Title"},
		{"(Full-Cast Edition)", "(Full-Cast Edition)"},
	}
	for _, tc := range cases {
		if got := stripProductionQualifier(tc.in); got != tc.want {
			t.Errorf("stripProductionQualifier(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestProductionQualifiersAreCanonical pins the table's key form. A key that is
// not already normQualifier's output is unreachable - the lookup normalizes the
// title's qualifier before comparing - so a hyphenated or capitalized entry
// would silently never match the release it was added for.
func TestProductionQualifiersAreCanonical(t *testing.T) {
	for qualifier := range productionQualifiers {
		if got := normQualifier(qualifier); got != qualifier {
			t.Errorf("key %q is not canonical (normQualifier gives %q); it can never match", qualifier, got)
		}
	}
}

// TestProductionQualifiersCarryNoAbridgedSignal is the guard the qualifier list
// needs to stay safe: abridgedFromMarker reads a title for the edition
// tri-state, and no listed qualifier may look like an abridged marker to it - a
// stripped qualifier must never be able to invent an edition fact.
func TestProductionQualifiersCarryNoAbridgedSignal(t *testing.T) {
	for qualifier := range productionQualifiers {
		title := "Some Title (" + qualifier + ")"
		if a := abridgedFromMarker(title); a != nil {
			t.Errorf("qualifier %q makes abridgedFromMarker(%q) state %v", qualifier, title, *a)
		}
		if cleaned := cleanWorkTitle(title); cleaned != title {
			t.Errorf("qualifier %q is eaten by cleanWorkTitle: %q", qualifier, cleaned)
		}
	}
}
