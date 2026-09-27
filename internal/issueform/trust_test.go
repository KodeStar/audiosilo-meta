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

// setField replaces one field's value in a rendered form body, whatever it held
// (withField only fills an empty one).
func setField(t *testing.T, body, label, value string) string {
	t.Helper()
	head := "### " + label + "\n\n"
	i := strings.Index(body, head)
	if i < 0 {
		t.Fatalf("the body has no %q field", label)
	}
	rest := body[i+len(head):]
	j := strings.Index(rest, "\n\n")
	return body[:i] + field(label, value) + rest[j+2:]
}

// TestAddWorkASINOfLibexOnlyRecordTakesItOver is the intake side of the
// user-overwrite rule: the submitter names, BY ASIN, a recording only the
// mirror has ever stated, so their stated facts replace the mirror's and their
// submission takes over its provenance - an ordinary bot pull request carrying
// a MODIFIED record, applied through the importer's own attestation.
func TestAddWorkASINOfLibexOnlyRecordTakesItOver(t *testing.T) {
	dir := seedTierTree(t)
	body := addWorkBody("Whatever Title", "Some Author", "en", "Some Narrator", "US: B000000001", "my own copy", true)
	body = setField(t, body, fRecRuntime, "410") // within 10% of the recorded 400
	res := Process(Options{DataDir: dir, Template: "add-work", Body: body})
	if res.Status != StatusOK {
		t.Fatalf("status = %q, want ok; messages = %v", res.Status, res.Messages)
	}
	if !hasFile(res.Files, worksPack) {
		t.Errorf("files = %v, want the works pack the takeover rewrote", res.Files)
	}
	if !anyContains(res.Messages, "replaced the mirror's") {
		t.Errorf("the takeover must be reported: %v", res.Messages)
	}
	rec := readFile(t, dir, "works/ex/existing-work/recordings/john-smith-2020.json")
	for _, want := range []string{
		`"runtime_min": 410`, `"release_date": "1999-11-01"`, `"publisher": "Acme Audio"`,
		`"cover_url": "https://example.com/cover.jpg"`, `"ref": "my own copy"`, `"type": "user"`,
	} {
		if !strings.Contains(rec, want) {
			t.Errorf("the taken-over recording must carry %s:\n%s", want, rec)
		}
	}
	// Identity is never rewritten, and nothing is composed beside the record.
	if !strings.Contains(rec, `"narrators": [`+"\n"+`    "john-smith"`) {
		t.Errorf("narrators must be untouched:\n%s", rec)
	}
	if recordExists(t, dir, "works/wh/whatever-title/work.json") {
		t.Error("a takeover must not compose a new work")
	}
	// What the takeover left behind is named, not dropped silently.
	if !anyContains(res.Messages, "not applied") || !anyContains(res.Messages, fWorkFirstPublished) {
		t.Errorf("the unapplied fields must be named: %v", res.Messages)
	}
	if !anyContains(res.Messages, "never rewrites identity") {
		t.Errorf("the differing identity must be named: %v", res.Messages)
	}
}

// TestAddWorkTakeoverAttestsAMirrorWorkAndAddsGenres: when the WORK is a mirror
// seed too it is taken over with the recording, and the form's genres are added
// to its set rather than replacing it (LICENSING.md rule 5).
func TestAddWorkTakeoverAttestsAMirrorWorkAndAddsGenres(t *testing.T) {
	dir := t.TempDir()
	files := seedFiles()
	files["works/ex/existing-work/work.json"] = `{
  "authors": ["jane-doe"],
  "genres": ["fantasy"],
  "id": "existing-work",
  "language": "en",
  "license": "CC0-1.0",
  "sources": [{"type": "libex-import", "ref": "B000000001", "imported_at": "2026-07-01"}],
  "title": "Existing Work"
}`
	files["works/ex/existing-work/recordings/john-smith-2020.json"] = libexOnlyRecording
	testpack.Seed(t, dir, files)

	body := addWorkBody("Existing Work", "Jane Doe", "en", "John Smith", "US: B000000001", "my own copy", true)
	body = setField(t, body, fRecRuntime, "400")
	body = setField(t, body, fWorkGenres, "horror")
	res := Process(Options{DataDir: dir, Template: "add-work", Body: body})
	if res.Status != StatusOK {
		t.Fatalf("status = %q, want ok; messages = %v", res.Status, res.Messages)
	}
	work := readFile(t, dir, "works/ex/existing-work/work.json")
	if !strings.Contains(work, `"fantasy",`+"\n"+`    "horror"`) {
		t.Errorf("the form's genre must be ADDED to the mirror's:\n%s", work)
	}
	if !strings.Contains(work, `"type": "user"`) {
		t.Errorf("the mirror-seed work must be attested too:\n%s", work)
	}
	if anyContains(res.Messages, "never rewrites identity") {
		t.Errorf("matching identity must not be reported as differing: %v", res.Messages)
	}
}

// TestAddWorkASINTakeoverDisagreementNeedsHuman: a runtime more than 10% apart
// is the disagreement GOVERNANCE.md routes to a maintainer. Nothing is written -
// not even the stamp that would end the record's mirror-only status.
func TestAddWorkASINTakeoverDisagreementNeedsHuman(t *testing.T) {
	dir := seedTierTree(t)
	body := addWorkBody("Whatever Title", "Some Author", "en", "Some Narrator", "US: B000000001", "web", true)
	res := Process(Options{DataDir: dir, Template: "add-work", Body: body}) // 500 min against 400
	if res.Status != StatusNeedsHuman {
		t.Fatalf("status = %q, want needs-human; messages = %v", res.Status, res.Messages)
	}
	if !anyContains(res.Messages, "a maintainer adjudicates") ||
		!anyContains(res.Messages, "runtime 500 min conflicts with the recorded 400 min") {
		t.Errorf("the message must name the disagreement and both values: %v", res.Messages)
	}
	if !anyContains(res.Messages, worksPack+": entry existing-work: recording john-smith-2020") {
		t.Errorf("the message must locate the record: %v", res.Messages)
	}
	rec := readFile(t, dir, "works/ex/existing-work/recordings/john-smith-2020.json")
	if !strings.Contains(rec, `"runtime_min": 400`) || strings.Contains(rec, `"type": "user"`) {
		t.Errorf("a refused takeover must write nothing:\n%s", rec)
	}
}

// TestAddRecordingASINTakeoverDateDisagreementNeedsHuman: a release date that is
// not the same date at another precision disagrees too.
func TestAddRecordingASINTakeoverDateDisagreementNeedsHuman(t *testing.T) {
	dir := t.TempDir()
	files := seedFiles()
	files["works/ex/existing-work/recordings/john-smith-2020.json"] = strings.Replace(
		libexOnlyRecording, `"runtime_min": 400,`, `"release_date": "2020", "runtime_min": 400,`, 1)
	testpack.Seed(t, dir, files)
	res := Process(Options{DataDir: dir, Template: "add-recording",
		Body: addRecordingBody("existing-work", "John Smith", "US: B000000001", true)}) // states 2021-01-01
	if res.Status != StatusNeedsHuman {
		t.Fatalf("status = %q, want needs-human; messages = %v", res.Status, res.Messages)
	}
	if !anyContains(res.Messages, "release date 2021-01-01 conflicts with the recorded 2020") {
		t.Errorf("the message must name both dates: %v", res.Messages)
	}
}

// TestAddRecordingASINOfLibexOnlyRecordTakesItOver: the add-recording form takes
// a mirror seed over the same way, and a date at another precision agrees.
func TestAddRecordingASINOfLibexOnlyRecordTakesItOver(t *testing.T) {
	dir := t.TempDir()
	files := seedFiles()
	files["works/ex/existing-work/recordings/john-smith-2020.json"] = strings.Replace(
		libexOnlyRecording, `"runtime_min": 400,`, `"release_date": "2021", "runtime_min": 400,`, 1)
	testpack.Seed(t, dir, files)
	res := Process(Options{DataDir: dir, Template: "add-recording",
		Body: addRecordingBody("existing-work", "John Smith", "US: B000000001", true)})
	if res.Status != StatusOK {
		t.Fatalf("status = %q, want ok; messages = %v", res.Status, res.Messages)
	}
	rec := readFile(t, dir, "works/ex/existing-work/recordings/john-smith-2020.json")
	for _, want := range []string{`"release_date": "2021-01-01"`, `"publisher": "Other Audio"`, `"runtime_min": 410`} {
		if !strings.Contains(rec, want) {
			t.Errorf("the taken-over recording must carry %s:\n%s", want, rec)
		}
	}
}

// TestAddRecordingASINUnderAnotherWorkNeedsHuman: the form names one work and the
// ASIN is recorded under another - a disagreement about what the book is, which
// no takeover settles.
func TestAddRecordingASINUnderAnotherWorkNeedsHuman(t *testing.T) {
	dir := t.TempDir()
	files := seedFiles()
	files["works/ex/existing-work/recordings/john-smith-2020.json"] = libexOnlyRecording
	files["works/ot/other-work/work.json"] = `{"authors": ["jane-doe"], "id": "other-work", "language": "en", ` +
		`"license": "CC0-1.0", "sources": [{"type": "user", "imported_at": "2026-07-01"}], "title": "Other Work"}`
	testpack.Seed(t, dir, files)
	res := Process(Options{DataDir: dir, Template: "add-recording",
		Body: addRecordingBody("other-work", "John Smith", "US: B000000001", true)})
	if res.Status != StatusNeedsHuman {
		t.Fatalf("status = %q, want needs-human; messages = %v", res.Status, res.Messages)
	}
	if !anyContains(res.Messages, `not of the work this submission names ("other-work")`) {
		t.Errorf("the message must say the ASIN belongs to another work: %v", res.Messages)
	}
}

// TestTakeoverRefusesAnASINTheRecordDoesNotCarry: an attestation never adds an
// identifier, so a second, unrecorded ASIN goes to a maintainer rather than
// being dropped silently.
func TestTakeoverRefusesAnASINTheRecordDoesNotCarry(t *testing.T) {
	dir := seedTierTree(t)
	res := Process(Options{DataDir: dir, Template: "add-recording",
		Body: addRecordingBody("existing-work", "John Smith", "US: B000000001\nUK: B0NEWASIN1", true)})
	if res.Status != StatusNeedsHuman {
		t.Fatalf("status = %q, want needs-human; messages = %v", res.Status, res.Messages)
	}
	if !anyContains(res.Messages, "B0NEWASIN1, which that recording does not carry") {
		t.Errorf("the message must name the unrecorded ASIN: %v", res.Messages)
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
// duplicate gate: the narrator-set match on the add-recording form. A narrator
// set is not the ASIN match the takeover is keyed on (issue #2406's shape), so a
// maintainer decides - and the message names the record's ASIN, the one edit
// that would make it a takeover the bot applies.
func TestAddRecordingDuplicateNarratorOnLibexOnlyRecordNeedsHuman(t *testing.T) {
	dir := seedTierTree(t)
	body := addRecordingBody("existing-work", "John Smith", "", true)
	res := Process(Options{DataDir: dir, Template: "add-recording", Body: body})
	if res.Status != StatusNeedsHuman {
		t.Fatalf("status = %q, want needs-human; messages = %v", res.Status, res.Messages)
	}
	if !anyContains(res.Messages, "matched it by its narrators, not by an ASIN it carries") {
		t.Errorf("the message must say why it is not a takeover: %v", res.Messages)
	}
	if !anyContains(res.Messages, "add its ASIN (B000000001 (us))") {
		t.Errorf("the message must name the ASIN that would make it one: %v", res.Messages)
	}
	rec := readFile(t, dir, "works/ex/existing-work/recordings/john-smith-2020.json")
	if strings.Contains(rec, `"type": "user"`) {
		t.Errorf("nothing may be applied:\n%s", rec)
	}
}

// TestAddISBNOnlyMatchOfLibexOnlyRecordNeedsHuman: an ISBN is not an ASIN either.
func TestAddISBNOnlyMatchOfLibexOnlyRecordNeedsHuman(t *testing.T) {
	dir := t.TempDir()
	files := seedFiles()
	files["works/ex/existing-work/recordings/john-smith-2020.json"] = strings.Replace(
		libexOnlyRecording, `"id": "john-smith-2020",`, `"id": "john-smith-2020", "isbn": ["9781473647633"],`, 1)
	testpack.Seed(t, dir, files)
	body := setField(t, addRecordingBody("existing-work", "Somebody New", "", true), fRecISBNs, "9781473647633")
	res := Process(Options{DataDir: dir, Template: "add-recording", Body: body})
	if res.Status != StatusNeedsHuman {
		t.Fatalf("status = %q, want needs-human; messages = %v", res.Status, res.Messages)
	}
	if !anyContains(res.Messages, "matched it by ISBN 9781473647633, not by an ASIN it carries") {
		t.Errorf("the message must say what it matched by: %v", res.Messages)
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
	if !anyContains(res.Messages, "matched it by its title, not by an ASIN it carries") {
		t.Errorf("the message must say why it is not a takeover: %v", res.Messages)
	}
	// It locates the WORK entry - what the maintainer has to look at here.
	if !anyContains(res.Messages, worksPack+": entry existing-work") {
		t.Errorf("the message must locate the work: %v", res.Messages)
	}
}

// TestAddWorkSlugHeldByAnotherAuthorsMirrorSeedIsNotATakeover is issue #2408's
// shape: the title's slug is held by a mirror seed of ANOTHER author's book of
// the same name. That is neither a duplicate nor a takeover - telling the
// submitter their data should replace that record would put one book's facts on
// another's - so the verdict says it is a different book needing its own slug.
func TestAddWorkSlugHeldByAnotherAuthorsMirrorSeedIsNotATakeover(t *testing.T) {
	dir := t.TempDir()
	files := seedFiles()
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

	body := addWorkBody("Existing Work", "Somebody Else", "en", "Different Narrator", "US: B0FRESH001", "web", true)
	res := Process(Options{DataDir: dir, Template: "add-work", Body: body})
	if res.Status != StatusNeedsHuman {
		t.Fatalf("status = %q, want needs-human; messages = %v", res.Status, res.Messages)
	}
	if !anyContains(res.Messages, `held by a different book - "Existing Work" by Jane Doe`) {
		t.Errorf("the message must say it is another author's book: %v", res.Messages)
	}
	if anyContains(res.Messages, "should replace") || anyContains(res.Messages, "edit the issue to add its ASIN") {
		t.Errorf("another book must not be offered as a takeover: %v", res.Messages)
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
