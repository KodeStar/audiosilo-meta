package issueform

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// validRecapsJSON is a minimal, schema-valid recaps sidecar for existing-work.
const validRecapsJSON = `{"work":"existing-work","recaps":[{"through":{"chapter":1},"text":"Alice sets out, and by the end of the first chapter she has left home."}],"license":"CC-BY-SA-3.0","sources":[{"type":"community"}]}`

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
// intake side: the composer speaks pack only, so a tree still in the
// file-per-entity layout is an invalid verdict rather than a second write path.
func TestIntakeRefusesLegacyLayout(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "people", "ja", "jane-doe.json")
	if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"id":"jane-doe","license":"CC0-1.0","name":"Jane Doe","sources":[{"type":"user"}]}` + "\n"
	if err := os.WriteFile(legacy, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	res := Process(Options{DataDir: dir, Template: "add-work",
		Body: addWorkBody("Legacy Book", "Leo Author", "en", "Lia Voice", "", "web", true)})
	if res.Status != StatusInvalid {
		t.Fatalf("status = %q, want invalid; messages = %v", res.Status, res.Messages)
	}
	if !anyContains(res.Messages, pack.ErrLegacyLayout.Error()) {
		t.Errorf("messages must name the legacy layout: %v", res.Messages)
	}
	if !anyContains(res.Messages, "data/people") {
		t.Errorf("messages must name the family that is still legacy: %v", res.Messages)
	}
	if got, rerr := os.ReadFile(legacy); rerr != nil || string(got) != body {
		t.Errorf("the legacy tree was touched (err=%v): %s", rerr, got)
	}
	if _, serr := os.Stat(filepath.Join(dir, "works")); !os.IsNotExist(serr) {
		t.Errorf("the refused submission created works/ (stat err = %v)", serr)
	}
	// Guard the errors.Is contract the message is derived from.
	if _, err := openStore(dir); !errors.Is(err, pack.ErrLegacyLayout) {
		t.Errorf("openStore error = %v, want it to wrap pack.ErrLegacyLayout", err)
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
