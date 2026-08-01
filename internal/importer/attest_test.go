package importer

import (
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/check"
)

// The trust-tier tests run against a catalogue seeded the way the 142k libex
// tranche will seed it: every record carries libex-import provenance and
// nothing else, so every one of them is bulk-mirror-only. Each test overrides
// the parts of the seed it is about.
//
// A user-library import (RunOpenAudible here; libation and audiosilo-books are
// the same tier) is then run against it, which is the moment the policy in
// LICENSING.md's "Trust tiers and the user-overwrite rule" applies.
const (
	tierPersonAuthor   = `{"id":"ada-mapmaker","license":"CC0-1.0","name":"Ada Mapmaker","sources":[{"imported_at":"2026-01-05","ref":"B0LIBEX001","type":"libex-import"}]}`
	tierPersonNarrator = `{"id":"bea-reader","license":"CC0-1.0","name":"Bea Reader","sources":[{"imported_at":"2026-01-05","ref":"B0LIBEX001","type":"libex-import"}]}`
	// The work and recording under attestation: libex-only, and carrying the
	// facts a user's own export will disagree with.
	tierWork = `{"added_at":"2026-01-05","authors":["ada-mapmaker"],"id":"the-lost-cartographer","language":"en","license":"CC0-1.0","sources":[{"imported_at":"2026-01-05","ref":"B0LIBEX001","type":"libex-import"}],"title":"The Lost Cartographer"}`
	//nolint:lll // one record per line reads as the record it is
	tierRecording = `{"added_at":"2026-01-05","asin":[{"asin":"B0LIBEX001","region":"us"}],"chapters":[{"length_ms":60000,"start_ms":0,"title":"Mirror Chapter"}],"cover_url":"https://m.media-amazon.com/images/I/mirror.jpg","id":"bea-reader-2019","language":"en","license":"CC0-1.0","narrators":["bea-reader"],"publisher":"Mirror Press","release_date":"2019","runtime_min":600,"sources":[{"imported_at":"2026-01-05","ref":"B0LIBEX001","type":"libex-import"}],"work":"the-lost-cartographer"}`
	// The same recording once a user HAS attested it: their facts won and their
	// stamp rode along, so the record is no longer bulk-mirror-only and no later
	// run - mirror or user - may overwrite it again.
	//nolint:lll // one record per line reads as the record it is
	tierRecordingAttested = `{"added_at":"2026-01-05","asin":[{"asin":"B0LIBEX001","region":"us"}],"cover_url":"https://m.media-amazon.com/images/I/user.jpg","id":"bea-reader-2019","language":"en","license":"CC0-1.0","narrators":["bea-reader"],"publisher":"Lost Press","release_date":"2019-05-04","runtime_min":605,"sources":[{"imported_at":"2026-01-05","ref":"B0LIBEX001","type":"libex-import"},{"imported_at":"2026-07-11","ref":"B0LIBEX001","type":"openaudible-import"}],"work":"the-lost-cartographer"}`
)

const (
	tierWorkRel = "works/th/the-lost-cartographer/work.json"
	tierRecRel  = "works/th/the-lost-cartographer/recordings/bea-reader-2019.json"
)

// seedTierTree writes the bulk-mirror-only catalogue (plus overrides) into a
// fresh temp data dir.
func seedTierTree(t *testing.T, overrides map[string]string) string {
	t.Helper()
	files := map[string]string{
		"people/ad/ada-mapmaker.json": tierPersonAuthor,
		"people/be/bea-reader.json":   tierPersonNarrator,
		tierWorkRel:                   tierWork,
		tierRecRel:                    tierRecording,
	}
	for rel, content := range overrides {
		files[rel] = content
	}
	dataDir := t.TempDir()
	seedTree(t, dataDir, files)
	return dataDir
}

// userRowFull is an OpenAudible export row for the seeded book, stating every
// overwritable fact differently from the mirror: a more precise release date, a
// slightly longer runtime (within the 10% same-production window), its own
// publisher spelling, cover and chapter table.
const userRowFull = `[{
  "asin": "B0LIBEX001",
  "title_short": "The Lost Cartographer",
  "author": "Ada Mapmaker",
  "narrated_by": "Bea Reader",
  "language": "english",
  "region": "us",
  "publisher": "Lost Press",
  "release_date": "2019-05-04",
  "seconds": 36300,
  "image_url": "https://m.media-amazon.com/images/I/user.jpg",
  "chapters": [
    { "title": "Opening", "start_offset_ms": 0, "length_ms": 90000 },
    { "title": "The Map", "start_offset_ms": 90000, "length_ms": 120000 }
  ]
}]`

// userRowBare states only what identifies the book - no publisher, cover,
// runtime, date or chapters - so it proves the absent-field half of the rule.
const userRowBare = `[{
  "asin": "B0LIBEX001",
  "title_short": "The Lost Cartographer",
  "author": "Ada Mapmaker",
  "narrated_by": "Bea Reader",
  "language": "english",
  "region": "us"
}]`

// userRowConflicting is a SECOND user's row for the same book, disagreeing on
// the runtime: 54000s = 900 minutes against the 605 the first user recorded, far
// outside the 10% window that means "the same production". It also states a
// publisher of its own, so a run that applied anything at all would be visible.
const userRowConflicting = `[{
  "asin": "B0LIBEX001",
  "title_short": "The Lost Cartographer",
  "author": "Ada Mapmaker",
  "narrated_by": "Bea Reader",
  "language": "english",
  "region": "us",
  "seconds": 54000,
  "publisher": "Another Imprint"
}]`

// runUserImport runs a user-library (OpenAudible) import against dataDir.
func runUserImport(t *testing.T, dataDir, books string) Summary {
	t.Helper()
	sum, err := Run(writeBooks(t, books), Options{DataDir: dataDir, ImportDate: testImportDate})
	if err != nil {
		t.Fatalf("user import: %v", err)
	}
	return sum
}

// sourceTypes lists a record's provenance types in order.
func sourceTypes(rec recordingFile) []string {
	out := make([]string, 0, len(rec.Sources))
	for _, s := range rec.Sources {
		out = append(out, s.Type)
	}
	return out
}

func TestUserImportOverwritesLibexOnlyRecord(t *testing.T) {
	dataDir := seedTierTree(t, nil)
	sum := runUserImport(t, dataDir, userRowFull)

	// The book was not created (its ASIN is catalogued) but it WAS attested.
	if sum.NewWorks+sum.NewRecordings+sum.NewPeople != 0 {
		t.Errorf("attestation must create nothing: %+v", sum)
	}
	if sum.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1", sum.Skipped)
	}
	if sum.AttestedRecordings != 1 || sum.AttestedWorks != 1 {
		t.Errorf("AttestedWorks/AttestedRecordings = %d/%d, want 1/1", sum.AttestedWorks, sum.AttestedRecordings)
	}
	if sum.Conflicts != 0 {
		t.Errorf("Conflicts = %d, want 0", sum.Conflicts)
	}

	var rec recordingFile
	readEntity(t, dataDir, tierRecRel, &rec)
	if rec.Publisher != "Lost Press" {
		t.Errorf("publisher = %q, want the user's %q", rec.Publisher, "Lost Press")
	}
	if rec.ReleaseDate != "2019-05-04" {
		t.Errorf("release_date = %q, want the user's %q", rec.ReleaseDate, "2019-05-04")
	}
	if rec.RuntimeMin != 605 {
		t.Errorf("runtime_min = %d, want the user's 605", rec.RuntimeMin)
	}
	if rec.CoverURL != "https://m.media-amazon.com/images/I/user.jpg" {
		t.Errorf("cover_url = %q, want the user's", rec.CoverURL)
	}
	if len(rec.Chapters) != 2 || rec.Chapters[0].Title != "Opening" {
		t.Errorf("chapters = %+v, want the user's two-chapter table", rec.Chapters)
	}
	// Identity is never rewritten by an import, in any tier.
	if len(rec.ASIN) != 1 || rec.ASIN[0].ASIN != "B0LIBEX001" || rec.ASIN[0].Region != "us" {
		t.Errorf("asin = %+v, want the recorded one untouched", rec.ASIN)
	}
	if len(rec.Narrators) != 1 || rec.Narrators[0] != "bea-reader" {
		t.Errorf("narrators = %v, want untouched", rec.Narrators)
	}
	// The record is now user-attested: the mirror's stamp stays (retraction
	// still needs it), the user's is appended.
	if got := sourceTypes(rec); len(got) != 2 || got[0] != "libex-import" || got[1] != "openaudible-import" {
		t.Errorf("sources = %v, want [libex-import openaudible-import]", got)
	}

	// added_at is a creation stamp, never a fact from the export.
	if raw := readRaw(t, dataDir, tierRecRel); !strings.Contains(raw, `"added_at":"2026-01-05"`) {
		t.Errorf("recording added_at was rewritten: %s", raw)
	}
	if raw := readRaw(t, dataDir, tierWorkRel); !strings.Contains(raw, `"added_at":"2026-01-05"`) {
		t.Errorf("work added_at was rewritten: %s", raw)
	}
	var work enrichedWork
	readEntity(t, dataDir, tierWorkRel, &work)
	if work.Title != "The Lost Cartographer" {
		t.Errorf("work title = %q, want untouched (a title change is a correction)", work.Title)
	}
	if len(work.Sources) != 2 || work.Sources[1].Type != "openaudible-import" {
		t.Errorf("work sources = %+v, want the user stamp appended", work.Sources)
	}

	if res := check.Load(dataDir); !res.OK() {
		t.Fatalf("attested tree failed validation:\n%v", res.Problems)
	}
}

func TestUserImportKeepsLibexValuesItDoesNotState(t *testing.T) {
	dataDir := seedTierTree(t, nil)
	sum := runUserImport(t, dataDir, userRowBare)

	if sum.AttestedRecordings != 1 || sum.AttestedWorks != 1 {
		t.Errorf("AttestedWorks/AttestedRecordings = %d/%d, want 1/1", sum.AttestedWorks, sum.AttestedRecordings)
	}
	var rec recordingFile
	readEntity(t, dataDir, tierRecRel, &rec)
	if rec.Publisher != "Mirror Press" || rec.ReleaseDate != "2019" || rec.RuntimeMin != 600 {
		t.Errorf("a fact the row does not state must keep the mirror's value: %+v", rec)
	}
	if rec.CoverURL != "https://m.media-amazon.com/images/I/mirror.jpg" {
		t.Errorf("cover_url = %q, want the mirror's", rec.CoverURL)
	}
	if len(rec.Chapters) != 1 || rec.Chapters[0].Title != "Mirror Chapter" {
		t.Errorf("chapters = %+v, want the mirror's", rec.Chapters)
	}
	// Silence is not an assertion, but the record is attested all the same: the
	// user's provenance entry IS the attestation.
	if got := sourceTypes(rec); len(got) != 2 || got[1] != "openaudible-import" {
		t.Errorf("sources = %v, want the user stamp appended", got)
	}
}

// TestUserImportDoesNotOverwriteAnAttestedRecord is rule 3: once a record has
// been attested the ordinary posture resumes. A second user's differing (but
// compatible) values do not win, and the create path's skip is a skip again -
// nothing is written at all.
func TestUserImportDoesNotOverwriteAnAttestedRecord(t *testing.T) {
	dataDir := seedTierTree(t, nil)
	runUserImport(t, dataDir, userRowFull)
	before := snapshotTree(t, dataDir)

	second := `[{
	  "asin": "B0LIBEX001",
	  "title_short": "The Lost Cartographer",
	  "author": "Ada Mapmaker",
	  "narrated_by": "Bea Reader",
	  "language": "english",
	  "region": "us",
	  "publisher": "Another Imprint",
	  "image_url": "https://m.media-amazon.com/images/I/second.jpg"
	}]`
	sum := runUserImport(t, dataDir, second)
	if sum.AttestedRecordings != 0 || sum.AttestedWorks != 0 {
		t.Errorf("an attested record must not be attested twice: %+v", sum)
	}
	if sum.EnrichedRecordings != 0 || sum.EnrichedWorks != 0 {
		t.Errorf("a create-mode skip must stay a skip: %+v", sum)
	}
	if sum.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1", sum.Skipped)
	}
	assertTreeUnchanged(t, dataDir, before)
}

// TestSecondUserContradictionWarnsAndKeepsTheFirst is the other half of rule 3:
// where two users genuinely disagree, the first writer's value stands, the row
// is refused whole, and the disagreement is counted for review - never resolved
// by letting the later writer win.
func TestSecondUserContradictionWarnsAndKeepsTheFirst(t *testing.T) {
	dataDir := seedTierTree(t, nil)
	runUserImport(t, dataDir, userRowFull)
	before := snapshotTree(t, dataDir)

	sum := runUserImport(t, dataDir, userRowConflicting)
	if sum.Conflicts != 1 {
		t.Errorf("Conflicts = %d, want 1", sum.Conflicts)
	}
	if len(sum.Warnings) != 1 || !strings.Contains(sum.Warnings[0], "conflicts with the recorded") {
		t.Errorf("warnings = %v, want one contradiction line", sum.Warnings)
	}
	assertTreeUnchanged(t, dataDir, before)
}

// TestUserImportIsIdempotent pins the second-run promise: attestation is a
// one-way step, so re-importing the same library queues no write at all.
func TestUserImportIsIdempotent(t *testing.T) {
	dataDir := seedTierTree(t, nil)
	runUserImport(t, dataDir, userRowFull)
	before := snapshotTree(t, dataDir)

	sum := runUserImport(t, dataDir, userRowFull)
	if sum.AttestedRecordings != 0 || sum.AttestedWorks != 0 || sum.Conflicts != 0 {
		t.Errorf("a second identical run must do nothing: %+v", sum)
	}
	assertTreeUnchanged(t, dataDir, before)
}

// TestLibexImportNeverOverwritesAUserAttestedRecord is the ordering itself:
// trust runs one way. Neither libex planning mode may touch a record a user has
// attested, whatever the mirror states.
func TestLibexImportNeverOverwritesAUserAttestedRecord(t *testing.T) {
	// The mirror states a different publisher and cover for facts the record
	// already carries, so a run that honours the ordering writes nothing at all.
	row := `[{"asin":"B0LIBEX001","title":"The Lost Cartographer","region":"us","language":"english","publisher":"Mirror Press","releaseDate":"2019-05-04T00:00:00Z","lengthMinutes":605,"imageUrl":"https://m.media-amazon.com/images/I/mirror.jpg","authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":"Bea Reader"}]}]`

	for _, mode := range []struct {
		name string
		mode Mode
	}{{"create", ModeCreate}, {"enrich", ModeEnrich}} {
		t.Run(mode.name, func(t *testing.T) {
			dataDir := seedTierTree(t, map[string]string{tierRecRel: tierRecordingAttested})
			before := snapshotTree(t, dataDir)
			sum, err := RunLibex(writeBooks(t, row), Options{
				DataDir: dataDir, ImportDate: testImportDate, Mode: mode.mode,
			})
			if err != nil {
				t.Fatal(err)
			}
			if sum.AttestedRecordings != 0 || sum.AttestedWorks != 0 || sum.Conflicts != 0 {
				t.Errorf("libex must never attest or flag: %+v", sum)
			}
			assertTreeUnchanged(t, dataDir, before)
		})
	}
}

// TestUserImportAttestsOnAnASINMerge covers the other ASIN-matched flow: a
// user's regional edition folds its ASIN into the recording the mirror seeded.
// That merge stamps the user's provenance, which ENDS the record's
// bulk-mirror-only status - so the row's facts have to be applied in the same
// act or they are lost for good.
func TestUserImportAttestsOnAnASINMerge(t *testing.T) {
	dataDir := seedTierTree(t, nil)
	row := `[{
	  "asin": "B0USER0001",
	  "title_short": "The Lost Cartographer",
	  "author": "Ada Mapmaker",
	  "narrated_by": "Bea Reader",
	  "language": "english",
	  "region": "uk",
	  "publisher": "Lost Press",
	  "seconds": 36300
	}]`
	sum := runUserImport(t, dataDir, row)

	if sum.MergedASINs != 1 {
		t.Errorf("MergedASINs = %d, want 1", sum.MergedASINs)
	}
	if sum.AttestedRecordings != 1 {
		t.Errorf("AttestedRecordings = %d, want 1", sum.AttestedRecordings)
	}
	if sum.NewWorks != 0 || sum.NewRecordings != 0 {
		t.Errorf("a merge must create nothing: %+v", sum)
	}
	var rec recordingFile
	readEntity(t, dataDir, tierRecRel, &rec)
	if rec.Publisher != "Lost Press" || rec.RuntimeMin != 605 {
		t.Errorf("the merged row's facts must have overwritten the mirror's: %+v", rec)
	}
	if len(rec.ASIN) != 2 {
		t.Errorf("asin = %+v, want both editions", rec.ASIN)
	}
}

// TestUserImportMergeLeavesAnAttestedRecordAlone pins the merge path's
// long-standing narrowness for every record the tier does not open: the ASIN,
// the ISBN and the provenance stamp, and nothing else.
func TestUserImportMergeLeavesAnAttestedRecordAlone(t *testing.T) {
	dataDir := seedTierTree(t, map[string]string{tierRecRel: tierRecordingAttested})
	row := `[{
	  "asin": "B0USER0001",
	  "title_short": "The Lost Cartographer",
	  "author": "Ada Mapmaker",
	  "narrated_by": "Bea Reader",
	  "language": "english",
	  "region": "uk",
	  "publisher": "Another Imprint",
	  "release_date": "2020-01-09",
	  "seconds": 36300
	}]`
	sum := runUserImport(t, dataDir, row)

	if sum.MergedASINs != 1 || sum.AttestedRecordings != 0 {
		t.Errorf("summary = %+v, want a plain merge", sum)
	}
	// A re-release legitimately carries its own release date, so an inferred
	// match must not report one as a disagreement either.
	if sum.Conflicts != 0 {
		t.Errorf("Conflicts = %d, want 0 - a regional edition's own date is not a conflict", sum.Conflicts)
	}
	var rec recordingFile
	readEntity(t, dataDir, tierRecRel, &rec)
	if rec.Publisher != "Lost Press" || rec.ReleaseDate != "2019-05-04" {
		t.Errorf("an attested record must keep its facts through a merge: %+v", rec)
	}
	if len(rec.ASIN) != 2 {
		t.Errorf("asin = %+v, want both editions", rec.ASIN)
	}
}

// TestRecordWalksFromLibexSeedToUserAttestation is the end-to-end fixture: one
// record created by a libex import, taken over by the first user to import it,
// then defended against a second user who disagrees.
func TestRecordWalksFromLibexSeedToUserAttestation(t *testing.T) {
	dataDir := t.TempDir()
	seedTree(t, dataDir, map[string]string{})

	// 1. The libex tranche seeds the book.
	libexRow := `[{
	  "asin": "B0LIBEX001",
	  "title": "The Lost Cartographer",
	  "region": "us",
	  "language": "english",
	  "publisher": "Mirror Press",
	  "releaseDate": "2019-05-04T00:00:00Z",
	  "lengthMinutes": 600,
	  "authors": [{ "name": "Ada Mapmaker" }],
	  "narrators": [{ "name": "Bea Reader" }]
	}]`
	seed, err := RunLibex(writeBooks(t, libexRow), Options{DataDir: dataDir, ImportDate: "2026-01-05"})
	if err != nil {
		t.Fatal(err)
	}
	if seed.NewWorks != 1 || seed.NewRecordings != 1 {
		t.Fatalf("seed summary = %+v", seed)
	}
	var rec recordingFile
	readEntity(t, dataDir, tierRecRel, &rec)
	if got := sourceTypes(rec); len(got) != 1 || got[0] != "libex-import" {
		t.Fatalf("seeded sources = %v, want libex-only", got)
	}

	// 2. The first user to own the book imports their library: their facts win.
	first := runUserImport(t, dataDir, userRowFull)
	if first.AttestedRecordings != 1 || first.AttestedWorks != 1 {
		t.Errorf("first user summary = %+v", first)
	}
	readEntity(t, dataDir, tierRecRel, &rec)
	if rec.Publisher != "Lost Press" || rec.RuntimeMin != 605 || rec.ReleaseDate != "2019-05-04" {
		t.Errorf("the first user's facts must win over the seed: %+v", rec)
	}
	if got := sourceTypes(rec); len(got) != 2 || got[1] != "openaudible-import" {
		t.Errorf("sources = %v, want the user stamp appended", got)
	}

	// 3. A second user disagrees: the first writer's values stand and the
	// disagreement is flagged rather than applied.
	before := snapshotTree(t, dataDir)
	second := runUserImport(t, dataDir, userRowConflicting)
	if second.Conflicts != 1 || second.AttestedRecordings != 0 {
		t.Errorf("second user summary = %+v, want one flagged conflict and no takeover", second)
	}
	assertTreeUnchanged(t, dataDir, before)

	if res := check.Load(dataDir); !res.OK() {
		t.Fatalf("tree failed validation:\n%v", res.Problems)
	}
}

