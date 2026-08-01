package issueform

import (
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/testpack"
	"github.com/kodestar/audiosilo-meta/pkg/check"
)

// libexOnlyRecording is the seed catalogue's recording with libex-import as its
// ONLY provenance - the shape ~135k records will have after the tranche lands.
const libexOnlyRecording = `{
  "abridged": false,
  "asin": [{"asin": "B000000001", "region": "us"}],
  "id": "john-smith-2020",
  "language": "en",
  "license": "CC0-1.0",
  "narrators": ["john-smith"],
  "runtime_min": 400,
  "sources": [{"type": "libex-import", "ref": "B000000001", "imported_at": "2026-07-01"}],
  "work": "existing-work"
}`

// seedTierTree writes the ordinary seed catalogue with the incumbent recording
// replaced by a bulk-mirror-only one.
func seedTierTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := seedFiles()
	files["works/ex/existing-work/recordings/john-smith-2020.json"] = libexOnlyRecording
	testpack.Seed(t, dir, files)
	if res := check.Load(dir); !res.OK() {
		t.Fatalf("seed tree does not validate: %v", res.Problems)
	}
	return dir
}

// TestAddWorkDuplicateOfLibexOnlyRecordNeedsHuman is the intake side of the
// user-overwrite rule: the submitter is the first person to attest a book only
// the mirror has ever stated, so their data should replace what is recorded.
// The bot only composes NEW records, so it hands the takeover to a maintainer
// instead of closing the submission as a duplicate.
func TestAddWorkDuplicateOfLibexOnlyRecordNeedsHuman(t *testing.T) {
	dir := seedTierTree(t)
	body := addWorkBody("Whatever Title", "Some Author", "en", "Some Narrator", "US: B000000001", "web", true)
	res := Process(Options{DataDir: dir, Template: "add-work", Body: body})
	if res.Status != StatusNeedsHuman {
		t.Fatalf("status = %q, want needs-human; messages = %v", res.Status, res.Messages)
	}
	if !anyContains(res.Messages, "seeded from the libex mirror") {
		t.Errorf("the message must say why a maintainer is needed: %v", res.Messages)
	}
	// It still locates the incumbent so the maintainer knows what to rewrite.
	if !anyContains(res.Messages, worksPack+": entry existing-work: recording john-smith-2020") {
		t.Errorf("the message must locate the record: %v", res.Messages)
	}
}

// TestAddWorkDuplicateOfAttestedRecordStaysDuplicate is the same submission
// against a record a user has already attested: nothing has changed, and the
// verdict is the long-standing duplicate.
func TestAddWorkDuplicateOfAttestedRecordStaysDuplicate(t *testing.T) {
	dir := seedTree(t) // the ordinary seed: user-sourced records
	body := addWorkBody("Whatever Title", "Some Author", "en", "Some Narrator", "US: B000000001", "web", true)
	res := Process(Options{DataDir: dir, Template: "add-work", Body: body})
	if res.Status != StatusDuplicate {
		t.Fatalf("status = %q, want duplicate; messages = %v", res.Status, res.Messages)
	}
	if anyContains(res.Messages, "libex mirror") {
		t.Errorf("an attested record must not be reported as a mirror seed: %v", res.Messages)
	}
}

// TestAddWorkDuplicateOfMixedRecordStaysDuplicate pins the "iff EVERY entry is
// libex-typed" half of the tier test: one user attestation is enough to leave
// the mirror tier for good.
func TestAddWorkDuplicateOfMixedRecordStaysDuplicate(t *testing.T) {
	dir := t.TempDir()
	files := seedFiles()
	files["works/ex/existing-work/recordings/john-smith-2020.json"] = strings.Replace(
		libexOnlyRecording,
		`"sources": [{"type": "libex-import", "ref": "B000000001", "imported_at": "2026-07-01"}]`,
		`"sources": [{"type": "libex-import", "ref": "B000000001", "imported_at": "2026-07-01"},`+
			`{"type": "user", "ref": "the publisher's page", "imported_at": "2026-07-02"}]`,
		1)
	testpack.Seed(t, dir, files)

	body := addWorkBody("Whatever Title", "Some Author", "en", "Some Narrator", "US: B000000001", "web", true)
	res := Process(Options{DataDir: dir, Template: "add-work", Body: body})
	if res.Status != StatusDuplicate {
		t.Fatalf("status = %q, want duplicate; messages = %v", res.Status, res.Messages)
	}
}

// TestImportOfLibexOnlyRecordAttestsRatherThanDuplicating is the bulk path's
// intake seam. The same export that reads as a plain duplicate against an
// attested record CHANGES the tree when the record is a mirror seed, so the
// verdict must be ok (a pull request opens) rather than duplicate (the
// submission is closed and the takeover silently discarded).
func TestImportOfLibexOnlyRecordAttestsRatherThanDuplicating(t *testing.T) {
	dir := seedTierTree(t)
	export := `[{"asin":"B000000001","title":"Existing Work","author":"Jane Doe","narrated_by":"John Smith",` +
		`"language":"english","region":"us","publisher":"The Owner's Copy"}]`
	res := Process(Options{DataDir: dir, Template: "import", Body: importBody("OpenAudible (books.json)", export)})
	if res.Status != StatusOK {
		t.Fatalf("status = %q, want ok; messages = %v", res.Status, res.Messages)
	}
	if !anyContains(res.Messages, "previously seeded from the libex mirror") {
		t.Errorf("the attestation must be reported: %v", res.Messages)
	}
	rec := readFile(t, dir, "works/ex/existing-work/recordings/john-smith-2020.json")
	if !strings.Contains(rec, `"The Owner's Copy"`) {
		t.Errorf("the submitter's publisher must have replaced the mirror's:\n%s", rec)
	}
	if !strings.Contains(rec, `"openaudible-import"`) {
		t.Errorf("the record must now be user-attested:\n%s", rec)
	}
}

// TestImportConflictIsFlaggedNotRejected is the "flag for review" half: a row
// that disagrees with an attested record is refused individually, the recorded
// value stands, and the note tells the maintainer to adjudicate - but the
// import as a whole still lands as a reviewable pull request.
func TestImportConflictIsFlaggedNotRejected(t *testing.T) {
	dir := seedTree(t) // attested records; the seed recording is 400 minutes
	export := `[{"asin":"B000000001","title":"Existing Work","author":"Jane Doe","narrated_by":"John Smith",` +
		`"language":"english","region":"us","seconds":72000},` +
		`{"asin":"B0NEWBOOK1","title":"A Book Of My Own","author":"Jane Doe","narrated_by":"John Smith",` +
		`"language":"english","region":"us"}]`
	res := Process(Options{DataDir: dir, Template: "import", Body: importBody("OpenAudible (books.json)", export)})
	if res.Status != StatusOK {
		t.Fatalf("status = %q, want ok; messages = %v", res.Status, res.Messages)
	}
	if !anyContains(res.Messages, "disagreed with a value already recorded") {
		t.Errorf("the conflict must be flagged for review: %v", res.Messages)
	}
	rec := readFile(t, dir, "works/ex/existing-work/recordings/john-smith-2020.json")
	if !strings.Contains(rec, `"runtime_min": 400`) {
		t.Errorf("the recorded value must stand - first writer wins:\n%s", rec)
	}
}

// TestAddRecordingDuplicateNarratorOnLibexOnlyRecordNeedsHuman covers the other
// duplicate gate: the narrator-set match on the add-recording form.
func TestAddRecordingDuplicateNarratorOnLibexOnlyRecordNeedsHuman(t *testing.T) {
	dir := seedTierTree(t)
	body := addRecordingBody("existing-work", "John Smith", "", true)
	res := Process(Options{DataDir: dir, Template: "add-recording", Body: body})
	if res.Status != StatusNeedsHuman {
		t.Fatalf("status = %q, want needs-human; messages = %v", res.Status, res.Messages)
	}
	if !anyContains(res.Messages, "seeded from the libex mirror") {
		t.Errorf("the message must say why a maintainer is needed: %v", res.Messages)
	}
}

// TestImportConflictOnlySubmissionNeedsHuman is the verdict gap the "flag for
// review" promise had: an export whose ONLY effect was a disagreement produces
// nothing, so Produced() == 0 and Skipped > 0, and the submission was closed as
// a plain duplicate - the adjudication note and the importer warnings never
// reached anyone. (TestImportConflictIsFlaggedNotRejected passes either way,
// because its export also carries a brand-new book.)
func TestImportConflictOnlySubmissionNeedsHuman(t *testing.T) {
	dir := seedTree(t) // attested records; the seed recording is 400 minutes
	// One row, and it is the same book with a runtime far outside the 10% window.
	export := `[{"asin":"B000000001","title":"Existing Work","author":"Jane Doe","narrated_by":"John Smith",` +
		`"language":"english","region":"us","seconds":72000}]`
	res := Process(Options{DataDir: dir, Template: "import", Body: importBody("OpenAudible (books.json)", export)})

	if res.Status != StatusNeedsHuman {
		t.Fatalf("status = %q, want needs-human; messages = %v", res.Status, res.Messages)
	}
	if !anyContains(res.Messages, "disagreed with a value already recorded") {
		t.Errorf("the conflict must be surfaced for adjudication: %v", res.Messages)
	}
	// The warning naming the record is what a maintainer adjudicates from.
	if !anyContains(res.Messages, "conflicts with the recorded") {
		t.Errorf("the importer warning must ride along: %v", res.Messages)
	}
}

// TestImportPlainDuplicateStaysDuplicate is the boundary of the fix above: an
// export that only re-states what is already recorded, with nothing to
// adjudicate, is still the long-standing duplicate.
func TestImportPlainDuplicateStaysDuplicate(t *testing.T) {
	dir := seedTree(t)
	export := `[{"asin":"B000000001","title":"Existing Work","author":"Jane Doe","narrated_by":"John Smith",` +
		`"language":"english","region":"us"}]`
	res := Process(Options{DataDir: dir, Template: "import", Body: importBody("OpenAudible (books.json)", export)})
	if res.Status != StatusDuplicate {
		t.Fatalf("status = %q, want duplicate; messages = %v", res.Status, res.Messages)
	}
}

// TestAddWorkSlugDuplicateOfMirrorOnlyWorkNeedsHuman covers the THIRD duplicate
// gate - the work-slug collision - which routed to a plain duplicate while the
// ASIN/ISBN and narrator-set gates already consulted the trust tier. A
// submission naming a work only the mirror has ever stated is the first person
// to attest it, so it goes to a maintainer like the other two.
func TestAddWorkSlugDuplicateOfMirrorOnlyWorkNeedsHuman(t *testing.T) {
	dir := t.TempDir()
	files := seedFiles()
	// BOTH the work and its recording are mirror seeds.
	files["works/ex/existing-work/work.json"] = `{
  "authors": ["jane-doe"],
  "id": "existing-work",
  "language": "en",
  "license": "CC0-1.0",
  "sources": [{"type": "libex-import", "ref": "B000000001", "imported_at": "2026-07-01"}],
  "title": "Existing Work"
}`
	files["works/ex/existing-work/recordings/john-smith-2020.json"] = libexOnlyRecording
	testpack.Seed(t, dir, files)

	// A fresh ASIN and a different narrator, so the identifier and narrator gates
	// both pass and only the work-slug gate can fire.
	body := addWorkBody("Existing Work", "Jane Doe", "en", "Different Narrator", "US: B0FRESH001", "web", true)
	res := Process(Options{DataDir: dir, Template: "add-work", Body: body})

	if res.Status != StatusNeedsHuman {
		t.Fatalf("status = %q, want needs-human; messages = %v", res.Status, res.Messages)
	}
	if !anyContains(res.Messages, "seeded from the libex mirror") {
		t.Errorf("the message must say why a maintainer is needed: %v", res.Messages)
	}
	// It locates the WORK entry - what the maintainer has to rewrite here.
	if !anyContains(res.Messages, worksPack+": entry existing-work") {
		t.Errorf("the message must locate the work: %v", res.Messages)
	}
}

// TestAddWorkSlugDuplicateOfAttestedWorkStaysDuplicate is that gate's other
// side: a work someone has attested is an ordinary duplicate, as it always was.
func TestAddWorkSlugDuplicateOfAttestedWorkStaysDuplicate(t *testing.T) {
	dir := seedTree(t) // the ordinary seed: user-sourced records
	body := addWorkBody("Existing Work", "Jane Doe", "en", "Different Narrator", "US: B0FRESH001", "web", true)
	res := Process(Options{DataDir: dir, Template: "add-work", Body: body})
	if res.Status != StatusDuplicate {
		t.Fatalf("status = %q, want duplicate; messages = %v", res.Status, res.Messages)
	}
	if anyContains(res.Messages, "libex mirror") {
		t.Errorf("an attested work must not be reported as a mirror seed: %v", res.Messages)
	}
}
