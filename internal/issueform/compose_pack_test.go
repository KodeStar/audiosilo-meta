package issueform

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/testpack"
	"github.com/kodestar/audiosilo-meta/pkg/check"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// validRecapsJSON is a minimal, schema-valid recaps sidecar for existing-work.
const validRecapsJSON = `{"work":"existing-work","recaps":[{"through":{"chapter":1},"text":"Alice sets out, and by the end of the first chapter she has left home."}],"license":"CC-BY-SA-4.0","sources":[{"type":"community"}]}`

// recapsBody builds an add-recaps submission body.
func recapsBody(workRef, attachment string, license bool) string {
	b := field(fWorkRef, workRef) +
		field(fSidecarRecapsFile, attachment) +
		"### Own words\n\n- [x] own words\n\n" +
		"### Neutral voice\n\n- [x] neutral\n\n" +
		"### " + fSidecarLicense + "\n\n"
	if license {
		return b + checkedBox()
	}
	return b + uncheckedBox()
}

// TestSidecarsShareOneEntry pins the works-community read-modify-write: both
// sidecars for a work live in ONE entry keyed by the work slug, so adding recaps
// to a work that already has characters must preserve the characters member
// rather than replace the entry.
func TestSidecarsShareOneEntry(t *testing.T) {
	dir := seedTree(t)

	first := Process(Options{DataDir: dir, Template: "characters",
		Body: charactersBody("existing-work", validCharactersJSON, true)})
	if first.Status != StatusOK {
		t.Fatalf("characters status = %q, messages = %v", first.Status, first.Messages)
	}

	second := Process(Options{DataDir: dir, Template: "recaps",
		Body: recapsBody("existing-work", validRecapsJSON, true)})
	if second.Status != StatusOK {
		t.Fatalf("recaps status = %q, messages = %v", second.Status, second.Messages)
	}

	// Both members are present, and the characters one is the record the first
	// submission wrote.
	var chars struct {
		Work       string `json:"work"`
		Characters []struct {
			ID string `json:"id"`
		} `json:"characters"`
	}
	if err := json.Unmarshal([]byte(readFile(t, dir, "works/ex/existing-work/characters.json")), &chars); err != nil {
		t.Fatalf("unmarshal characters: %v", err)
	}
	if chars.Work != "existing-work" || len(chars.Characters) != 1 || chars.Characters[0].ID != "alice" {
		t.Errorf("the characters member did not survive the recaps write: %+v", chars)
	}
	if !recordExists(t, dir, "works/ex/existing-work/recaps.json") {
		t.Error("the recaps member was not written")
	}
}

// TestSidecarOverwriteRefusedPerMember is the counterpart: only the member being
// placed blocks. A work that already has recaps still accepts characters.
func TestSidecarOverwriteRefusedPerMember(t *testing.T) {
	dir := seedTree(t)
	res := Process(Options{DataDir: dir, Template: "recaps",
		Body: recapsBody("existing-work", validRecapsJSON, true)})
	if res.Status != StatusOK {
		t.Fatalf("recaps status = %q, messages = %v", res.Status, res.Messages)
	}
	// The same member again is a maintainer's call.
	again := Process(Options{DataDir: dir, Template: "recaps",
		Body: recapsBody("existing-work", validRecapsJSON, true)})
	if again.Status != StatusNeedsHuman {
		t.Fatalf("re-placing recaps status = %q, want needs-human; messages = %v", again.Status, again.Messages)
	}
	// The other member is not blocked by it.
	chars := Process(Options{DataDir: dir, Template: "characters",
		Body: charactersBody("existing-work", validCharactersJSON, true)})
	if chars.Status != StatusOK {
		t.Fatalf("characters status = %q, messages = %v", chars.Status, chars.Messages)
	}
}

// TestComposedRecordsCarryAddedAt pins the intake bot's half of the added_at
// contract: the work and recording a submission CREATES carry the run's date,
// and a correction to a record that entered earlier never adds the field.
func TestComposedRecordsCarryAddedAt(t *testing.T) {
	dir := seedTree(t)
	res := Process(Options{DataDir: dir, Template: "add-work",
		Body: addWorkBody("Dated Book", "Dora Author", "en", "Dev Voice", "US: B444444441", "web", true),
		Date: "2026-07-31"})
	if res.Status != StatusOK {
		t.Fatalf("status = %q, messages = %v", res.Status, res.Messages)
	}
	if got := addedAtOf(t, dir, "works/da/dated-book/work.json"); got != "2026-07-31" {
		t.Errorf("work added_at = %q, want the submission date", got)
	}
	recSlug := onlyRecordingOf(t, dir, "dated-book")
	if got := addedAtOf(t, dir, "works/da/dated-book/recordings/"+recSlug+".json"); got != "2026-07-31" {
		t.Errorf("recording added_at = %q, want the submission date", got)
	}

	// A correction edits a record that entered earlier, so it stamps nothing.
	fix := Process(Options{DataDir: dir, Template: "correct-data",
		Body: field(fCorrectRecord, "works/ex/existing-work/work.json") +
			field(fCorrectField, "title") +
			field(fCorrectCorrected, "Existing Work, Corrected") +
			field(fCorrectEvidence, "the publisher's page") +
			"### " + fCC0 + "\n\n" + checkedBox(),
		Date: "2026-07-31"})
	if fix.Status != StatusOK {
		t.Fatalf("correction status = %q, messages = %v", fix.Status, fix.Messages)
	}
	if got := addedAtOf(t, dir, "works/ex/existing-work/work.json"); got != "" {
		t.Errorf("a correction stamped added_at = %q", got)
	}
}

// addedAtOf reads a composed record's added_at ("" when the field is absent).
func addedAtOf(t *testing.T, dir, address string) string {
	t.Helper()
	var rec struct {
		AddedAt string `json:"added_at"`
	}
	if err := json.Unmarshal([]byte(readFile(t, dir, address)), &rec); err != nil {
		t.Fatalf("unmarshal %s: %v", address, err)
	}
	return rec.AddedAt
}

// TestIntakeRefusesLegacyLayout pins the dual-layout window's guard on the
// intake side. The verdict is needs-human, not invalid: the submission is fine
// and the repository is mid-migration, and invalid is the contributor-fault
// verdict the intake workflow comments back at the submitter.
func TestIntakeRefusesLegacyLayout(t *testing.T) {
	dir := t.TempDir()
	seeded := testpack.SeedLegacyPerson(t, dir, "jane-doe", "Jane Doe")

	res := Process(Options{DataDir: dir, Template: "add-work",
		Body: addWorkBody("Legacy Book", "Leo Author", "en", "Lia Voice", "", "web", true)})
	if res.Status != StatusNeedsHuman {
		t.Fatalf("status = %q, want needs-human; messages = %v", res.Status, res.Messages)
	}
	if !anyContains(res.Messages, pack.ErrLegacyLayout.Error()) {
		t.Errorf("messages must name the legacy layout: %v", res.Messages)
	}
	if !anyContains(res.Messages, pack.FamilyPeople.Root()) {
		t.Errorf("messages must name the family that is still legacy: %v", res.Messages)
	}
	testpack.AssertUntouched(t, dir, "jane-doe", seeded)
	// Guard the errors.Is contract the message is derived from.
	if _, err := openStore(dir, "add-work"); !errors.Is(err, pack.ErrLegacyLayout) {
		t.Errorf("openStore error = %v, want it to wrap pack.ErrLegacyLayout", err)
	}
}

// TestSidecarTemplateIgnoresACoreFamilysLayout is the per-template half of the
// gate: works-community converts on its own schedule during the dual-layout
// window, so the families a template does NOT write must not be able to refuse
// it - and the family it does write still must.
func TestSidecarTemplateGatesOnlyItsOwnFamily(t *testing.T) {
	// The community family is absent (so writable) while people is legacy: a
	// characters submission writes only works-community, so the store opens.
	legacyCore := t.TempDir()
	testpack.SeedLegacyPerson(t, legacyCore, "jane-doe", "Jane Doe")
	if _, err := openStore(legacyCore, "characters"); err != nil {
		t.Errorf("a sidecar submission was refused for a family it never writes: %v", err)
	}
	if _, err := openStore(legacyCore, "add-work"); !errors.Is(err, pack.ErrLegacyLayout) {
		t.Errorf("add-work writes people; it must still be refused: %v", err)
	}

	// The mirror image: works-community legacy, the core families untouched.
	legacySidecar := t.TempDir()
	writeLegacySidecar(t, legacySidecar, "some-work")
	if _, err := openStore(legacySidecar, "characters"); !errors.Is(err, pack.ErrLegacyLayout) {
		t.Errorf("a sidecar submission must be refused when its own family is legacy: %v", err)
	}
	for _, tmpl := range []string{"add-work", "add-recording", "correct-data"} {
		if _, err := openStore(legacySidecar, tmpl); err != nil {
			t.Errorf("%s never writes works-community; it must not be refused: %v", tmpl, err)
		}
	}
}

// legacyShard is the directory a slug used to live under before the pack
// migration: its first two characters. Only the legacy-refusal fixtures below
// still need it, so it is spelled out here rather than exported from anywhere.
func legacyShard(slug string) string {
	if len(slug) < 2 {
		return slug
	}
	return slug[:2]
}

// writeLegacySidecar puts the works-community family in the legacy layout with
// one per-entity characters file.
func writeLegacySidecar(t *testing.T, dir, workSlug string) {
	t.Helper()
	full := filepath.Join(dir, pack.FamilyWorksCommunity.Root(), legacyShard(workSlug), workSlug, "characters.json")
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(validCharactersJSON+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestComposeNeverOverwritesAnUndecodableEntry is the intake side of the guard
// against silent recording loss: the dedup maps come from check.Load's Catalog,
// which drops an entry it cannot decode, so the duplicate check passes and the
// create path would replace the whole composite entry - recordings and all.
func TestComposeNeverOverwritesAnUndecodableEntry(t *testing.T) {
	dir := t.TempDir()
	const workAddress = "works/br/broken-book/work.json"
	// "authors" is a string, not an array: valid JSON, valid pack, but the work
	// does not decode into model.Work, so it never reaches the Catalog.
	testpack.Seed(t, dir, map[string]string{
		workAddress: `{"authors":"jane-doe","id":"broken-book","language":"en","license":"CC0-1.0","sources":[{"type":"user"}],"title":"Broken Book"}`,
		"works/br/broken-book/recordings/john-smith-2020.json": `{"asin":[{"asin":"B0KEEPME01","region":"us"}],"id":"john-smith-2020","language":"en","license":"CC0-1.0","narrators":["john-smith"],"sources":[{"type":"user"}],"work":"broken-book"}`,
	})
	if cat := check.Load(dir).Catalog; cat != nil {
		for _, w := range cat.Works {
			if w.ID == "broken-book" {
				t.Fatalf("fixture no longer defeats the loader; the work reached the Catalog")
			}
		}
	}

	res := Process(Options{DataDir: dir, Template: "add-work",
		Body: addWorkBody("Broken Book", "Jane Doe", "en", "Nina Voice", "", "web", true)})
	if res.Status != StatusNeedsHuman {
		t.Fatalf("status = %q, want needs-human; messages = %v", res.Status, res.Messages)
	}
	if !anyContains(res.Messages, "broken-book") || !anyContains(res.Messages, worksPack) {
		t.Errorf("messages must name the slug and the pack holding it: %v", res.Messages)
	}
	if recs := testpack.Recordings(t, dir, "broken-book"); len(recs) != 1 || recs[0] != "john-smith-2020" {
		t.Errorf("recordings = %v, want the seeded john-smith-2020 intact", recs)
	}
	var work struct {
		Authors any `json:"authors"`
	}
	if err := json.Unmarshal([]byte(readFile(t, dir, workAddress)), &work); err != nil {
		t.Fatal(err)
	}
	if work.Authors != "jane-doe" {
		t.Errorf("the undecodable work was rewritten: authors = %v", work.Authors)
	}
}

// TestStrayFieldsSurviveACorrection pins the composite read-modify-write on the
// correction path: a recording correction rewrites its work's whole entry, so
// the work's own fields and its SIBLING recordings must come through untouched.
func TestStrayFieldsSurviveACorrection(t *testing.T) {
	dir := seedTree(t)
	res := Process(Options{DataDir: dir, Template: "correct-data",
		Body: field(fCorrectRecord, "works/ex/existing-work/recordings/john-smith-2020.json") +
			field(fCorrectField, "publisher") +
			field(fCorrectCorrected, "Corrected Audio") +
			field(fCorrectEvidence, "the publisher's page") +
			"### " + fCC0 + "\n\n" + checkedBox(),
		Date: "2026-07-31"})
	if res.Status != StatusOK {
		t.Fatalf("status = %q, messages = %v", res.Status, res.Messages)
	}
	rec := readFile(t, dir, "works/ex/existing-work/recordings/john-smith-2020.json")
	if !strings.Contains(rec, `"publisher": "Corrected Audio"`) {
		t.Errorf("the correction was not applied:\n%s", rec)
	}
	if !strings.Contains(rec, `"runtime_min": 400`) {
		t.Errorf("an untouched recording field was lost:\n%s", rec)
	}
	work := readFile(t, dir, "works/ex/existing-work/work.json")
	if !strings.Contains(work, `"title": "Existing Work"`) {
		t.Errorf("the parent work was rewritten by a recording correction:\n%s", work)
	}
}

// TestWorkCreditsSurviveIntake pins the intake side of contributor credits.
// The forms deliberately do NOT gain a credits field this round (the surface
// stays minimal pre-seed), so the only thing intake owes the field is that its
// read-modify-writes preserve it: a work seeded with credits must still carry
// them after a submission splices a recording into its composite entry, and
// after a correction rewrites one of its other fields.
//
// It is a real risk rather than a theoretical one - both paths rewrite the
// WHOLE works entry, so a composer that rebuilt the work from typed fields
// instead of editing the decoded record would drop every field it does not know
// about, credits included.
func TestWorkCreditsSurviveIntake(t *testing.T) {
	files := seedFiles()
	files["works/ex/existing-work/work.json"] = `{
  "authors": ["jane-doe"],
  "credits": [{"person": "john-smith", "role": "translator"}],
  "id": "existing-work",
  "language": "en",
  "license": "CC0-1.0",
  "sources": [{"type": "user", "imported_at": "2026-07-01"}],
  "title": "Existing Work"
}`
	dir := t.TempDir()
	testpack.Seed(t, dir, files)
	if res := check.Load(dir); !res.OK() {
		t.Fatalf("seed tree with credits does not validate: %v", res.Problems)
	}

	// A second narration is spliced into the same works entry.
	res := Process(Options{DataDir: dir, Template: "add-recording",
		Body: addRecordingBody("existing-work", "Nora Voice", "US: B444444442", true),
		Date: "2026-07-31"})
	if res.Status != StatusOK {
		t.Fatalf("add-recording status = %q, messages = %v", res.Status, res.Messages)
	}
	if work := readFile(t, dir, "works/ex/existing-work/work.json"); !strings.Contains(work, `"role": "translator"`) {
		t.Errorf("adding a recording dropped the work's credits:\n%s", work)
	}

	// A correction rewrites another field of the same record.
	fix := Process(Options{DataDir: dir, Template: "correct-data",
		Body: field(fCorrectRecord, "works/ex/existing-work/work.json") +
			field(fCorrectField, "title") +
			field(fCorrectCorrected, "Existing Work, Corrected") +
			field(fCorrectEvidence, "the publisher's page") +
			"### " + fCC0 + "\n\n" + checkedBox(),
		Date: "2026-07-31"})
	if fix.Status != StatusOK {
		t.Fatalf("correction status = %q, messages = %v", fix.Status, fix.Messages)
	}
	work := readFile(t, dir, "works/ex/existing-work/work.json")
	if !strings.Contains(work, `"role": "translator"`) {
		t.Errorf("a correction dropped the work's credits:\n%s", work)
	}
	if !strings.Contains(work, `"title": "Existing Work, Corrected"`) {
		t.Errorf("the correction was not applied:\n%s", work)
	}
	if res := check.Load(dir); !res.OK() {
		t.Fatalf("tree failed validation after intake:\n%v", res.Problems)
	}
}
