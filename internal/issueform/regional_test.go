package issueform

import (
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/testpack"
	"github.com/kodestar/audiosilo-meta/pkg/check"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// This file covers the regional-release intake: an ISBN a submitter can scope to
// a marketplace, the regional-publisher field, and the two ADDITIVE corrections
// that let a contributor state a regional fact about a recording that already
// exists. See recording.schema.json's isbn[]/publishers[] and CONTRIBUTING.md.

// recAddress is the incumbent recording every correction test here addresses,
// in the per-entity reference form a submitter types.
const recAddress = "data/works/ex/existing-work/recordings/john-smith-2020.json"

// recRel is the same recording as a test-fixture address (readFile's form).
const recRel = "works/ex/existing-work/recordings/john-smith-2020.json"

// seedRecordingTree writes the ordinary seed catalogue with the incumbent
// recording replaced, so a test states only the record shape it is about.
func seedRecordingTree(t *testing.T, recording string) string {
	t.Helper()
	dir := t.TempDir()
	files := seedFiles()
	files[recRel] = recording
	testpack.Seed(t, dir, files)
	if res := check.Load(dir); !res.OK() {
		t.Fatalf("seed tree does not validate: %v", res.Problems)
	}
	return dir
}

// withField sets a form field's value, replacing the "_No response_" placeholder
// the body helpers emit or appending the field when the body has none. parseBody
// keys on the "### <label>" headings and is order-independent, so appending is
// as faithful as substituting.
func withField(body, label, value string) string {
	if out := strings.Replace(body, field(label, ""), field(label, value), 1); out != body {
		return out
	}
	return body + field(label, value)
}

// TestParseISBNLineSpellings covers the two on-disk spellings a submitter may
// type, and the two ways a line is unusable.
func TestParseISBNLineSpellings(t *testing.T) {
	cases := []struct {
		name         string
		line         string
		wantISBN     string
		wantRegion   string
		wantOK       bool
		wantReasonIn string
	}{
		{name: "bare ISBN leaves the region unstated", line: "9781473647633", wantISBN: "9781473647633", wantOK: true},
		{name: "region prefix resolves through the alias table", line: "GB: 9781473647633", wantISBN: "9781473647633", wantRegion: "uk", wantOK: true},
		{name: "printed hyphens are stripped", line: "uk: 978-1-4736-4763-3", wantISBN: "9781473647633", wantRegion: "uk", wantOK: true},
		{name: "unknown region", line: "NZ: 9781473647633", wantReasonIn: "not a known marketplace"},
		{name: "region with a bad ISBN", line: "uk: not-an-isbn", wantReasonIn: "not a valid ISBN"},
		{name: "bare bad ISBN", line: "12345", wantReasonIn: "not a valid ISBN"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, reason, ok := parseISBNLine(tc.line)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v (reason %q), want %v", ok, reason, tc.wantOK)
			}
			if !ok {
				if !strings.Contains(reason, tc.wantReasonIn) {
					t.Errorf("reason = %q, want it to mention %q", reason, tc.wantReasonIn)
				}
				return
			}
			if got.ISBN != tc.wantISBN || got.Region != tc.wantRegion {
				t.Errorf("= %+v, want isbn %q region %q", got, tc.wantISBN, tc.wantRegion)
			}
		})
	}
}

// TestBareISBNIsUnstatedWhileBareASINDefaultsToUS pins the deliberate asymmetry
// between the two identifier parsers, which is the one thing about this feature
// most likely to be "tidied" into consistency later.
//
// An ASIN only exists inside a marketplace, so a region-less one is a missing
// prefix and defaulting it to us loses nothing. An ISBN is not marketplace-
// scoped: "region unknown" is an ordinary true state the schema spells as the
// bare string, and inventing "us" for it would be a fabricated fact.
func TestBareISBNIsUnstatedWhileBareASINDefaultsToUS(t *testing.T) {
	c := &composer{}

	isbns := c.parseISBNs("9781473647633")
	if len(isbns) != 1 || isbns[0].ISBN != "9781473647633" || isbns[0].Region != "" {
		t.Fatalf("bare ISBN = %+v, want the value with NO region", isbns)
	}

	asins := c.parseASINs("B017V4IMVG")
	if len(asins) != 1 || asins[0].ASIN != "B017V4IMVG" || asins[0].Region != "us" {
		t.Fatalf("bare ASIN = %+v, want region us", asins)
	}
	if !anyContains(c.messages, "had no region prefix - defaulted to us") {
		t.Errorf("the ASIN default must be reported: %v", c.messages)
	}
}

// TestParseISBNsSkipsAnUnusableLine covers the list field's tolerance: one bad
// line is noted and dropped, the rest of the submission stands.
func TestParseISBNsSkipsAnUnusableLine(t *testing.T) {
	c := &composer{}
	got := c.parseISBNs("NZ: 9781473647633\n9780062898968")
	if len(got) != 1 || got[0].ISBN != "9780062898968" {
		t.Fatalf("= %+v, want only the usable line", got)
	}
	if !anyContains(c.messages, `region "NZ" is not a known marketplace`) {
		t.Errorf("the skipped line must be reported: %v", c.messages)
	}
}

// TestParseISBNsDedupesTheSubmissionsOwnList: the two spellings make it easy to
// state one identifier twice in one field, and composing both would write a
// record that is a duplicate of ITSELF - reported afterwards as a raw metacheck
// line rather than as anything the submitter could act on. The SCOPED spelling
// wins, which is correctISBN's upgrade rule one layer up.
func TestParseISBNsDedupesTheSubmissionsOwnList(t *testing.T) {
	cases := []struct {
		name       string
		block      string
		wantRegion string
		wantNoteIn string
	}{
		{
			name:       "bare then scoped keeps the region",
			block:      "9781473647633\nGB: 9781473647633",
			wantRegion: "uk",
			wantNoteIn: `keeping the one scoped to region "uk"`,
		},
		{
			name:       "scoped then bare keeps the region",
			block:      "GB: 9781473647633\n9781473647633",
			wantRegion: "uk",
			wantNoteIn: "listed more than once",
		},
		{
			name:       "identical spellings collapse",
			block:      "9781473647633\n9781473647633",
			wantRegion: "",
			wantNoteIn: "listed more than once",
		},
		{
			name:       "case-folded check digit is the same identifier",
			block:      "012345678X\n012345678x",
			wantRegion: "",
			wantNoteIn: "listed more than once",
		},
		{
			// The contradiction correctISBN escalates to a maintainer. A list
			// field has to resolve it somehow, so first wins - but the note
			// names the region that was dropped, which is the one thing the
			// submitter would otherwise never learn.
			name:       "two different stated regions names both and the winner",
			block:      "GB: 9781473647633\nCA: 9781473647633",
			wantRegion: "uk",
			wantNoteIn: `listed under both "uk" and "ca"; keeping "uk"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &composer{}
			got := c.parseISBNs(tc.block)
			if len(got) != 1 {
				t.Fatalf("= %+v, want one entry", got)
			}
			if got[0].Region != tc.wantRegion {
				t.Errorf("region = %q, want %q", got[0].Region, tc.wantRegion)
			}
			if !anyContains(c.messages, tc.wantNoteIn) {
				t.Errorf("messages = %v, want one mentioning %q", c.messages, tc.wantNoteIn)
			}
		})
	}
}

// TestAdditiveFieldNamesResolve covers the spellings a submitter may put in the
// correction form's Field box, including the two FORM LABELS - which are derived
// from the label constants rather than hand-spelled, so a label rename cannot
// strand the synonym silently.
func TestAdditiveFieldNamesResolve(t *testing.T) {
	cases := map[string]string{
		"isbn":               "isbn",
		"ISBNs":              "isbn",
		"audiobook isbn":     "isbn",
		fRecISBNs:            "isbn",
		"publishers":         "publishers",
		"regional-publisher": "publishers",
		fRecPublishers:       "publishers",
	}
	for in, want := range cases {
		if got := normalizeFieldName(in); got != want {
			t.Errorf("normalizeFieldName(%q) = %q, want %q", in, got, want)
		}
		if _, ok := correctableFields[model.KindRecording][want]; !ok {
			t.Errorf("%q is not a correctable recording field", want)
		}
	}
}

// TestAddWorkRegionalISBNAndPublisher is the passing fixture for the whole
// intake path: a submission stating a regional ISBN and a regional imprint
// composes both onto the recording.
func TestAddWorkRegionalISBNAndPublisher(t *testing.T) {
	dir := seedTree(t)
	body := addWorkBody("Regional Book", "Some Author", "en", "Some Narrator", "US: B000000009", "publisher page", true)
	body = withField(body, fRecISBNs, "9780062898968\nGB: 9781473647633")
	body = withField(body, fRecPublishers, "GB: Hodder & Stoughton")
	res := Process(Options{DataDir: dir, Template: "add-work", Body: body})
	if res.Status != StatusOK {
		t.Fatalf("status = %q, messages = %v", res.Status, res.Messages)
	}
	rec := readFile(t, dir, "works/re/regional-book/recordings/some-narrator-1999.json")
	// The bare ISBN keeps the bare spelling; the scoped one becomes an object.
	if !strings.Contains(rec, `"9780062898968"`) {
		t.Errorf("the unstated-region ISBN did not land as a bare string:\n%s", rec)
	}
	if !strings.Contains(rec, `"isbn": "9781473647633"`) || !strings.Contains(rec, `"region": "uk"`) {
		t.Errorf("the region-scoped ISBN did not land as an object:\n%s", rec)
	}
	if !strings.Contains(rec, `"publisher": "Hodder & Stoughton"`) {
		t.Errorf("the regional publisher did not land:\n%s", rec)
	}
	if !strings.Contains(rec, `"publisher": "Acme Audio"`) {
		t.Errorf("the publisher of record must survive:\n%s", rec)
	}
}

// TestAddWorkRegionalPublisherRestatingPublisherIsInvalid: publishers[] holds
// the OTHER regions, and pkg/check refuses a restatement - so the form refuses
// it at compose time, with a verdict that says why.
func TestAddWorkRegionalPublisherRestatingPublisherIsInvalid(t *testing.T) {
	dir := seedTree(t)
	body := addWorkBody("Restating Book", "Some Author", "en", "Some Narrator", "", "publisher page", true)
	body = withField(body, fRecPublishers, "GB: Acme Audio")
	res := Process(Options{DataDir: dir, Template: "add-work", Body: body})
	if res.Status != StatusInvalid {
		t.Fatalf("status = %q, want invalid; messages = %v", res.Status, res.Messages)
	}
	if !anyContains(res.Messages, "already the Publisher field") {
		t.Errorf("the message must say why: %v", res.Messages)
	}
}

// TestAddWorkRegionalPublisherWithoutPublisherIsInvalid covers the schema's
// dependentRequired: "the other regions" needs the one it is other than.
func TestAddWorkRegionalPublisherWithoutPublisherIsInvalid(t *testing.T) {
	dir := seedTree(t)
	body := addWorkBody("Orphan Imprint Book", "Some Author", "en", "Some Narrator", "", "publisher page", true)
	body = strings.Replace(body, field(fRecPublisher, "Acme Audio"), field(fRecPublisher, ""), 1)
	body = withField(body, fRecPublishers, "GB: Hodder & Stoughton")
	res := Process(Options{DataDir: dir, Template: "add-work", Body: body})
	if res.Status != StatusInvalid {
		t.Fatalf("status = %q, want invalid; messages = %v", res.Status, res.Messages)
	}
	if !anyContains(res.Messages, "needs a Publisher") {
		t.Errorf("the message must name the missing field: %v", res.Messages)
	}
}

// TestAddWorkDuplicateRegionalPublisherRegionIsInvalid: one region names one
// imprint (pkg/check's checkRegionalPublishers), asked at compose time.
func TestAddWorkDuplicateRegionalPublisherRegionIsInvalid(t *testing.T) {
	dir := seedTree(t)
	body := addWorkBody("Two Imprints Book", "Some Author", "en", "Some Narrator", "", "publisher page", true)
	body = withField(body, fRecPublishers, "GB: Hodder & Stoughton\nUK: Gollancz")
	res := Process(Options{DataDir: dir, Template: "add-work", Body: body})
	if res.Status != StatusInvalid {
		t.Fatalf("status = %q, want invalid; messages = %v", res.Status, res.Messages)
	}
	if !anyContains(res.Messages, `region "uk" is listed twice`) {
		t.Errorf("the message must name the region: %v", res.Messages)
	}
}

// TestRegionScopedISBNDedupsAgainstARecordedBareOne proves the dedup gate keys
// on the ISBN VALUE: scoping a submitted ISBN to a region does not make it a
// different identifier from the bare one already recorded.
func TestRegionScopedISBNDedupsAgainstARecordedBareOne(t *testing.T) {
	dir := seedRecordingTree(t, `{
  "abridged": false,
  "asin": [{"asin": "B000000001", "region": "us"}],
  "id": "john-smith-2020",
  "isbn": ["9781473647633"],
  "language": "en",
  "license": "CC0-1.0",
  "narrators": ["john-smith"],
  "sources": [{"type": "user", "imported_at": "2026-07-01"}],
  "work": "existing-work"
}`)
	body := addWorkBody("Some Other Book", "Some Author", "en", "Some Narrator", "", "web", true)
	body = withField(body, fRecISBNs, "GB: 9781473647633")
	res := Process(Options{DataDir: dir, Template: "add-work", Body: body})
	if res.Status != StatusDuplicate {
		t.Fatalf("status = %q, want duplicate; messages = %v", res.Status, res.Messages)
	}
	if !anyContains(res.Messages, "ISBN 9781473647633 already exists") {
		t.Errorf("the message must name the identifier: %v", res.Messages)
	}
}

// userRecording is the incumbent with a user source (so it is NOT a bulk-mirror
// seed) and a publisher of record, which is what the additive corrections need.
const userRecording = `{
  "abridged": false,
  "asin": [{"asin": "B000000001", "region": "us"}],
  "id": "john-smith-2020",
  "language": "en",
  "license": "CC0-1.0",
  "narrators": ["john-smith"],
  "publisher": "Harper Voyager",
  "runtime_min": 400,
  "sources": [{"type": "user", "imported_at": "2026-07-01"}],
  "work": "existing-work"
}`

// withISBN returns userRecording carrying the given raw isbn[] body.
func withISBN(isbnJSON string) string {
	return strings.Replace(userRecording, `"language": "en",`, `"isbn": `+isbnJSON+`,
  "language": "en",`, 1)
}

// withPublishers returns userRecording carrying the given raw publishers[] body,
// beside the publisher of record it already states.
func withPublishers(publishersJSON string) string {
	return strings.Replace(userRecording, `"publisher": "Harper Voyager",`,
		`"publisher": "Harper Voyager",
  "publishers": `+publishersJSON+`,`, 1)
}

// TestCorrectAppendsANewISBN is the additive op's base case: an identifier
// nobody had recorded is a new fact, so it is appended rather than refused.
func TestCorrectAppendsANewISBN(t *testing.T) {
	dir := seedRecordingTree(t, userRecording)
	body := correctBody(recAddress, "isbn", "GB: 9781473647633", "the UK publisher's page", true)
	res := Process(Options{DataDir: dir, Template: "correct-data", Body: body})
	if res.Status != StatusOK {
		t.Fatalf("status = %q, messages = %v", res.Status, res.Messages)
	}
	rec := readFile(t, dir, recRel)
	if !strings.Contains(rec, `"isbn": "9781473647633"`) || !strings.Contains(rec, `"region": "uk"`) {
		t.Errorf("the region-scoped ISBN was not appended:\n%s", rec)
	}
	if !strings.Contains(rec, "the UK publisher's page") {
		t.Errorf("the correction's evidence was not stamped:\n%s", rec)
	}
}

// TestCorrectUpgradesABareISBNToARegion is the case the additive design exists
// for: a bare entry states NO region, so naming one adds information without
// contradicting anything, and the entry is upgraded in place.
func TestCorrectUpgradesABareISBNToARegion(t *testing.T) {
	dir := seedRecordingTree(t, withISBN(`["9781473647633"]`))
	body := correctBody(recAddress, "isbn", "uk: 9781473647633", "the UK publisher's page", true)
	res := Process(Options{DataDir: dir, Template: "correct-data", Body: body})
	if res.Status != StatusOK {
		t.Fatalf("status = %q, messages = %v", res.Status, res.Messages)
	}
	rec := readFile(t, dir, recRel)
	if !strings.Contains(rec, `"isbn": "9781473647633"`) || !strings.Contains(rec, `"region": "uk"`) {
		t.Errorf("the bare entry was not upgraded:\n%s", rec)
	}
	if strings.Count(rec, "9781473647633") != 1 {
		t.Errorf("the upgrade must replace the entry, not add a second one:\n%s", rec)
	}
	if !anyContains(res.Messages, "scoped the recorded ISBN") {
		t.Errorf("the verdict must say what happened: %v", res.Messages)
	}
}

// TestCorrectISBNStatedRegionContradictionNeedsHuman: a region already STATED is
// a source's claim, and this correction is another one. Nothing mechanical can
// pick a winner.
func TestCorrectISBNStatedRegionContradictionNeedsHuman(t *testing.T) {
	dir := seedRecordingTree(t, withISBN(`[{"isbn": "9781473647633", "region": "uk"}]`))
	body := correctBody(recAddress, "isbn", "CA: 9781473647633", "a Canadian listing", true)
	res := Process(Options{DataDir: dir, Template: "correct-data", Body: body})
	if res.Status != StatusNeedsHuman {
		t.Fatalf("status = %q, want needs-human; messages = %v", res.Status, res.Messages)
	}
	if !anyContains(res.Messages, `"uk"`) || !anyContains(res.Messages, `"ca"`) {
		t.Errorf("the message must name both regions: %v", res.Messages)
	}
	// Nothing was written: the recorded region survives untouched.
	rec := readFile(t, dir, recRel)
	if strings.Contains(rec, `"ca"`) {
		t.Errorf("a refused correction must not write:\n%s", rec)
	}
}

// TestCorrectISBNAlreadyRecordedIsANoOp covers the "same value, same
// statedness" case, reported with the duplicate-style verdict every other
// already-recorded gate uses.
func TestCorrectISBNAlreadyRecordedIsANoOp(t *testing.T) {
	dir := seedRecordingTree(t, withISBN(`[{"isbn": "9781473647633", "region": "uk"}]`))
	body := correctBody(recAddress, "isbn", "GB: 9781473647633", "the UK publisher's page", true)
	res := Process(Options{DataDir: dir, Template: "correct-data", Body: body})
	if res.Status != StatusDuplicate {
		t.Fatalf("status = %q, want duplicate; messages = %v", res.Status, res.Messages)
	}
	if !anyContains(res.Messages, "nothing to change") {
		t.Errorf("the message must say it is a no-op: %v", res.Messages)
	}
}

// TestCorrectISBNOnAnotherRecordingKeepsItsDuplicateRouting proves the existing
// global gate still fires ahead of the additive op, and - because it goes
// through failDuplicate - that a bulk-mirror-only incumbent still routes to a
// maintainer rather than being closed as a duplicate.
func TestCorrectISBNOnAnotherRecordingKeepsItsDuplicateRouting(t *testing.T) {
	dir := t.TempDir()
	files := seedFiles()
	files[recRel] = userRecording
	// A second work whose recording is a bulk-mirror seed already carrying the
	// identifier this correction states.
	files["works/mi/mirror-work/work.json"] = `{"authors": ["jane-doe"], "id": "mirror-work", "language": "en", "license": "CC0-1.0", "sources": [{"type": "libex-import", "ref": "B000000077", "imported_at": "2026-07-01"}], "title": "Mirror Work"}`
	files["works/mi/mirror-work/recordings/john-smith-2021.json"] = `{"id": "john-smith-2021", "isbn": ["9781473647633"], "language": "en", "license": "CC0-1.0", "narrators": ["john-smith"], "sources": [{"type": "libex-import", "ref": "B000000077", "imported_at": "2026-07-01"}], "work": "mirror-work"}`
	testpack.Seed(t, dir, files)
	if res := check.Load(dir); !res.OK() {
		t.Fatalf("seed tree does not validate: %v", res.Problems)
	}

	body := correctBody(recAddress, "isbn", "GB: 9781473647633", "the UK publisher's page", true)
	res := Process(Options{DataDir: dir, Template: "correct-data", Body: body})
	if res.Status != StatusNeedsHuman {
		t.Fatalf("status = %q, want needs-human (the incumbent is a mirror seed); messages = %v", res.Status, res.Messages)
	}
	if !anyContains(res.Messages, "seeded from the libex mirror") {
		t.Errorf("the mirror-seed verdict must be preserved: %v", res.Messages)
	}
}

// TestCorrectAppendsARegionalPublisherSorted covers the publishers[] append and
// the deterministic byte-form: the list is rewritten sorted by region, because
// canonical formatting preserves array order rather than imposing one.
func TestCorrectAppendsARegionalPublisherSorted(t *testing.T) {
	dir := seedRecordingTree(t, withPublishers(`[{"publisher": "Gollancz", "region": "uk"}]`))
	body := correctBody(recAddress, "regional publisher", "CA: Doubleday Canada", "the Canadian listing", true)
	res := Process(Options{DataDir: dir, Template: "correct-data", Body: body})
	if res.Status != StatusOK {
		t.Fatalf("status = %q, messages = %v", res.Status, res.Messages)
	}
	rec := readFile(t, dir, recRel)
	ca, uk := strings.Index(rec, "Doubleday Canada"), strings.Index(rec, "Gollancz")
	if ca < 0 || uk < 0 {
		t.Fatalf("both imprints must be present:\n%s", rec)
	}
	if ca > uk {
		t.Errorf("publishers[] must be sorted by region (ca before uk):\n%s", rec)
	}
}

// TestCorrectRegionalPublisherContradictionNeedsHuman: one region names one
// imprint, and two sources disagreeing about which is a maintainer's call.
func TestCorrectRegionalPublisherContradictionNeedsHuman(t *testing.T) {
	dir := seedRecordingTree(t, withPublishers(`[{"publisher": "Gollancz", "region": "uk"}]`))
	body := correctBody(recAddress, "publishers", "GB: Hodder & Stoughton", "a UK listing", true)
	res := Process(Options{DataDir: dir, Template: "correct-data", Body: body})
	if res.Status != StatusNeedsHuman {
		t.Fatalf("status = %q, want needs-human; messages = %v", res.Status, res.Messages)
	}
	if !anyContains(res.Messages, "Gollancz") || !anyContains(res.Messages, "Hodder & Stoughton") {
		t.Errorf("the message must name both imprints: %v", res.Messages)
	}
}

// TestCorrectRegionalPublisherWithoutAPublisherOfRecordNeedsHuman covers the
// schema's dependentRequired on the correction path. It is needs-human rather
// than invalid because the submitter's fact is fine - the record simply is not
// ready for it, and the message names the scalar correction that makes it ready.
func TestCorrectRegionalPublisherWithoutAPublisherOfRecordNeedsHuman(t *testing.T) {
	dir := seedRecordingTree(t, strings.Replace(userRecording, `  "publisher": "Harper Voyager",
`, "", 1))
	body := correctBody(recAddress, "publishers", "GB: Hodder & Stoughton", "a UK listing", true)
	res := Process(Options{DataDir: dir, Template: "correct-data", Body: body})
	if res.Status != StatusNeedsHuman {
		t.Fatalf("status = %q, want needs-human; messages = %v", res.Status, res.Messages)
	}
	if !anyContains(res.Messages, `correct "publisher" first`) {
		t.Errorf("the message must name the way forward: %v", res.Messages)
	}
}

// TestCorrectRegionalPublisherRestatingPublisherIsInvalid is the correction-path
// twin of the add-form refusal.
func TestCorrectRegionalPublisherRestatingPublisherIsInvalid(t *testing.T) {
	dir := seedRecordingTree(t, userRecording)
	body := correctBody(recAddress, "publishers", "GB: Harper Voyager", "a UK listing", true)
	res := Process(Options{DataDir: dir, Template: "correct-data", Body: body})
	if res.Status != StatusInvalid {
		t.Fatalf("status = %q, want invalid; messages = %v", res.Status, res.Messages)
	}
	if !anyContains(res.Messages, "publishers[] holds the OTHER regions") {
		t.Errorf("the message must say why: %v", res.Messages)
	}
}

// TestCorrectRegionalPublisherAlreadyRecordedIsANoOp is the duplicate-style
// verdict for a correction that states what is already there.
func TestCorrectRegionalPublisherAlreadyRecordedIsANoOp(t *testing.T) {
	dir := seedRecordingTree(t, withPublishers(`[{"publisher": "Hodder & Stoughton", "region": "uk"}]`))
	body := correctBody(recAddress, "publishers", "GB: Hodder & Stoughton", "a UK listing", true)
	res := Process(Options{DataDir: dir, Template: "correct-data", Body: body})
	if res.Status != StatusDuplicate {
		t.Fatalf("status = %q, want duplicate; messages = %v", res.Status, res.Messages)
	}
	if !anyContains(res.Messages, "nothing to change") {
		t.Errorf("the message must say it is a no-op: %v", res.Messages)
	}
}

// TestAdditiveNoOpOnAMirrorSeedIsStillADuplicate is failNoop's reason for
// existing. A no-op verdict must not go through failDuplicate, whose tier branch
// says "your submission should REPLACE what is recorded - the intake bot only
// composes new records, it cannot rewrite one": both halves are false here.
// Nothing needs replacing (the fact is identical), and the correction path CAN
// rewrite a record - it is the path doing the rewriting. Since bulk-mirror-only
// is the dominant population after the seed, that wrong verdict would be the
// common case rather than an edge.
func TestAdditiveNoOpOnAMirrorSeedIsStillADuplicate(t *testing.T) {
	// The same record as the fixtures above, but sourced ONLY by the mirror.
	mirrorSeed := strings.Replace(
		withPublishers(`[{"publisher": "Hodder & Stoughton", "region": "uk"}]`),
		`"sources": [{"type": "user", "imported_at": "2026-07-01"}]`,
		`"sources": [{"type": "libex-import", "ref": "B000000001", "imported_at": "2026-07-01"}]`, 1)
	mirrorSeed = strings.Replace(mirrorSeed, `"language": "en",`,
		`"isbn": [{"isbn": "9781473647633", "region": "uk"}],
  "language": "en",`, 1)

	cases := []struct {
		name  string
		field string
		value string
	}{
		{"isbn", "isbn", "GB: 9781473647633"},
		{"publishers", "publishers", "GB: Hodder & Stoughton"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := seedRecordingTree(t, mirrorSeed)
			body := correctBody(recAddress, tc.field, tc.value, "a UK listing", true)
			res := Process(Options{DataDir: dir, Template: "correct-data", Body: body})
			if res.Status != StatusDuplicate {
				t.Fatalf("status = %q, want duplicate; messages = %v", res.Status, res.Messages)
			}
			if anyContains(res.Messages, "seeded from the libex mirror") {
				t.Errorf("a no-op must not be routed as a mirror takeover: %v", res.Messages)
			}
			if !anyContains(res.Messages, "nothing to change") {
				t.Errorf("the message must say it is a no-op: %v", res.Messages)
			}
		})
	}
}

// TestRefusedPublisherShapesAreRejectedBySchemaToo is the cross-layer guard for
// the three publisher refusals the composer phrases in its own words.
//
// Those messages are a UX copy of rules that live in recording.schema.json and
// pkg/check, and a status assertion alone does not pin the agreement: the
// composer could keep refusing something the schema had come to allow, or - far
// worse - keep ALLOWING something the schema still rejects, which is a submission
// written and then bounced by metacheck. So each case builds the record the
// composer would have written and asserts the validation layer rejects it too.
func TestRefusedPublisherShapesAreRejectedBySchemaToo(t *testing.T) {
	cases := []struct {
		name      string
		recording string
	}{
		{
			name:      "duplicate region",
			recording: withPublishers(`[{"publisher": "Hodder & Stoughton", "region": "uk"}, {"publisher": "Gollancz", "region": "uk"}]`),
		},
		{
			name:      "restates the publisher of record",
			recording: withPublishers(`[{"publisher": "Harper Voyager", "region": "uk"}]`),
		},
		{
			name: "publishers with no publisher of record",
			recording: strings.Replace(
				withPublishers(`[{"publisher": "Hodder & Stoughton", "region": "uk"}]`),
				"  \"publisher\": \"Harper Voyager\",\n", "", 1),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			files := seedFiles()
			files[recRel] = tc.recording
			testpack.Seed(t, dir, files)
			res := check.Load(dir)
			if res.OK() {
				t.Fatalf("the validation layer accepted a shape the composer refuses:\n%s", tc.recording)
			}
		})
	}
}

// TestCorrectPublishersRefusesAMultiLineValue: a publisher name is free text, so
// a correction value carrying two lines would weld them into one corrupt name
// ("Hodder\nCA: Doubleday") that the schema, pkg/check and canonical formatting
// all accept. The form widget is single-line, but the issue BODY is untrusted -
// intake also runs on edited bodies and on issues opened through the API.
func TestCorrectPublishersRefusesAMultiLineValue(t *testing.T) {
	dir := seedRecordingTree(t, userRecording)
	body := correctBody(recAddress, "publishers", "GB: Hodder\nCA: Doubleday", "two listings", true)
	res := Process(Options{DataDir: dir, Template: "correct-data", Body: body})
	if res.Status != StatusInvalid {
		t.Fatalf("status = %q, want invalid; messages = %v", res.Status, res.Messages)
	}
	if !anyContains(res.Messages, "states one") {
		t.Errorf("the message must say one value per correction: %v", res.Messages)
	}
	if strings.Contains(readFile(t, dir, recRel), "Doubleday") {
		t.Error("a refused correction must not write")
	}
}

// TestAddFormTakesSeveralPublisherLines is the other side of the refusal above:
// the LIST field splits on newlines before parsePublisherLine ever sees a value,
// so several imprints on the add form are unaffected.
func TestAddFormTakesSeveralPublisherLines(t *testing.T) {
	dir := seedTree(t)
	body := addWorkBody("Many Imprints Book", "Some Author", "en", "Some Narrator", "", "publisher pages", true)
	body = withField(body, fRecPublishers, "GB: Hodder & Stoughton\nCA: Doubleday Canada")
	res := Process(Options{DataDir: dir, Template: "add-work", Body: body})
	if res.Status != StatusOK {
		t.Fatalf("status = %q, messages = %v", res.Status, res.Messages)
	}
	rec := readFile(t, dir, "works/ma/many-imprints-book/recordings/some-narrator-1999.json")
	if !strings.Contains(rec, "Hodder & Stoughton") || !strings.Contains(rec, "Doubleday Canada") {
		t.Errorf("both regional imprints should land:\n%s", rec)
	}
}

// TestCorrectISBNEscalatesAMalformedISBNField: a non-array isbn value reads as
// nil through the type assertion, so an append would REPLACE it and report ok -
// a silent destructive write. Unreachable from a green tree, which is exactly
// why it must not be silent; readRegionPublishers handles its own version of
// this the same way.
func TestCorrectISBNEscalatesAMalformedISBNField(t *testing.T) {
	dir := t.TempDir()
	files := seedFiles()
	// Deliberately schema-invalid, so it is seeded WITHOUT the usual check.Load
	// gate - the point is what the composer does when it meets one.
	files[recRel] = withISBN(`"9781473647633"`)
	testpack.Seed(t, dir, files)

	body := correctBody(recAddress, "isbn", "GB: 9780062898968", "a UK listing", true)
	res := Process(Options{DataDir: dir, Template: "correct-data", Body: body})
	if res.Status != StatusNeedsHuman {
		t.Fatalf("status = %q, want needs-human; messages = %v", res.Status, res.Messages)
	}
	if !anyContains(res.Messages, "not in the expected shape") {
		t.Errorf("the message must name the problem: %v", res.Messages)
	}
	if !strings.Contains(readFile(t, dir, recRel), "9781473647633") {
		t.Error("the recorded value must survive a refused correction")
	}
}

// TestCorrectPublisherToARegionalImprintNeedsHuman is the publisher/publishers
// pairing guarded from the SCALAR side. Two individually-valid corrections - add
// "GB: Gollancz" to publishers[], then correct publisher to "Gollancz" - would
// otherwise be written and bounced by the post-write validation, handing the
// submitter the raw checkRegionalPublishers line that the compose-time refusals
// exist to replace.
func TestCorrectPublisherToARegionalImprintNeedsHuman(t *testing.T) {
	dir := seedRecordingTree(t, withPublishers(`[{"publisher": "Gollancz", "region": "uk"}]`))
	body := correctBody(recAddress, "publisher", "Gollancz", "the spine of the book", true)
	res := Process(Options{DataDir: dir, Template: "correct-data", Body: body})
	if res.Status != StatusNeedsHuman {
		t.Fatalf("status = %q, want needs-human; messages = %v", res.Status, res.Messages)
	}
	if !anyContains(res.Messages, `region "uk"`) {
		t.Errorf("the message must name the region holding it: %v", res.Messages)
	}
	if !strings.Contains(readFile(t, dir, recRel), `"publisher": "Harper Voyager"`) {
		t.Error("a refused correction must not write")
	}
}

// TestCorrectISBNOnAWorkKeepsItsExistingVerdict pins that the additive ops are
// recordings-only: a work's xref.isbn is a different field with a different
// shape, and its verdict is unchanged.
func TestCorrectISBNOnAWorkKeepsItsExistingVerdict(t *testing.T) {
	dir := seedTree(t)
	body := correctBody("data/works/ex/existing-work/work.json", "isbn", "9781473647633", "web", true)
	res := Process(Options{DataDir: dir, Template: "correct-data", Body: body})
	if res.Status != StatusNeedsHuman {
		t.Fatalf("status = %q, want needs-human; messages = %v", res.Status, res.Messages)
	}
	if !anyContains(res.Messages, "cannot be auto-corrected") {
		t.Errorf("the existing verdict must be preserved: %v", res.Messages)
	}
}

// TestIssue1414ShapedSubmissionComposesMechanically is the case this feature
// exists for, end to end.
//
// A recording is one production; its identifiers and publisher differ by region.
// The catalogue holds the US release (publisher of record, US ASIN); a submitter
// holds the UK release of the SAME production and knows its ISBN and imprint.
// Before this, both facts were unrepresentable through the forms and the UK
// publisher had to be dropped. Now each is one correction, applied mechanically.
func TestIssue1414ShapedSubmissionComposesMechanically(t *testing.T) {
	dir := seedRecordingTree(t, userRecording)

	isbn := correctBody(recAddress, "isbn", "GB: 9781473647633", "the Hodder & Stoughton listing", true)
	if res := Process(Options{DataDir: dir, Template: "correct-data", Body: isbn}); res.Status != StatusOK {
		t.Fatalf("the UK ISBN correction: status = %q, messages = %v", res.Status, res.Messages)
	}
	imprint := correctBody(recAddress, "regional publisher", "GB: Hodder & Stoughton", "the Hodder & Stoughton listing", true)
	if res := Process(Options{DataDir: dir, Template: "correct-data", Body: imprint}); res.Status != StatusOK {
		t.Fatalf("the UK publisher correction: status = %q, messages = %v", res.Status, res.Messages)
	}

	rec := readFile(t, dir, recRel)
	if !strings.Contains(rec, `"isbn": "9781473647633"`) {
		t.Errorf("the UK ISBN is missing:\n%s", rec)
	}
	if !strings.Contains(rec, `"publisher": "Hodder & Stoughton"`) {
		t.Errorf("the UK imprint is missing:\n%s", rec)
	}
	// One production, one recording: the US facts are untouched.
	if !strings.Contains(rec, `"publisher": "Harper Voyager"`) || !strings.Contains(rec, "B000000001") {
		t.Errorf("the US release's facts must survive:\n%s", rec)
	}
	if res := check.Load(dir); !res.OK() {
		t.Fatalf("the corrected tree failed validation:\n%v", res.Problems)
	}
}
