package issueform

import (
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/check"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// A hand submission is user-library tier, the same tier whose library exports
// the bulk importer admits an AI-narrated book from. So the intake forms make
// the same two decisions the importer's shared gate makes
// (internal/importer/synthetic.go): a synthetic NARRATION folds onto the one
// canonical record, and an AI AUTHOR credit refuses the submission.

// TestAddWorkFoldsSyntheticNarration is the admission: an AI-narrated book
// submitted by hand is composed, and its narrator is the canonical record rather
// than a person minted for the TTS persona.
func TestAddWorkFoldsSyntheticNarration(t *testing.T) {
	dir := seedTree(t)
	body := addWorkBody("Machine Read Book", "Alice Author", "en", "AI Voice Nina", "US: B333333331", "Audible product page", true)
	res := Process(Options{DataDir: dir, Template: "add-work", Body: body, Date: "2026-07-14"})
	if res.Status != StatusOK {
		t.Fatalf("status = %q, messages = %v", res.Status, res.Messages)
	}
	if recordExists(t, dir, "people/ai/ai-voice-nina.json") {
		t.Error("the TTS persona was minted as a person")
	}
	person := readFile(t, dir, "people/vi/virtual-voice.json")
	if !strings.Contains(person, `"kind": "synthetic"`) {
		t.Errorf("the canonical record is not marked synthetic:\n%s", person)
	}
	if !strings.Contains(person, `"name": "Virtual Voice"`) {
		t.Errorf("the canonical record is not named canonically:\n%s", person)
	}
	rec := readFile(t, dir, "works/ma/machine-read-book/recordings/virtual-voice-1999.json")
	if !strings.Contains(rec, `"virtual-voice"`) {
		t.Errorf("the recording does not credit the canonical record:\n%s", rec)
	}
	if res := check.Load(dir); !res.OK() {
		t.Fatalf("tree failed validation after the submission:\n%v", res.Problems)
	}
}

// TestAddRecordingFoldsSyntheticNarration is the same admission on the other
// form, and it also pins the REUSE: the record this submission names was minted
// by the add-work submission's own composer run, so a second AI narration never
// forks a second synthetic identity.
func TestAddRecordingFoldsSyntheticNarration(t *testing.T) {
	dir := seedTree(t)
	body := addRecordingBody("https://meta.audiosilo.app/work?id=existing-work", "Steve Stewart's Voice Replica", "US: B333333332", true)
	res := Process(Options{DataDir: dir, Template: "add-recording", Body: body})
	if res.Status != StatusOK {
		t.Fatalf("status = %q, messages = %v", res.Status, res.Messages)
	}
	if recordExists(t, dir, "people/st/steve-stewarts-voice-replica.json") {
		t.Error("the cloned narrator's voice replica was minted as a person")
	}
	if person := readFile(t, dir, "people/vi/virtual-voice.json"); !strings.Contains(person, `"kind": "synthetic"`) {
		t.Errorf("the canonical record is not marked synthetic:\n%s", person)
	}
	if res := check.Load(dir); !res.OK() {
		t.Fatalf("tree failed validation after the submission:\n%v", res.Problems)
	}
}

// TestAddWorkRefusesAIAuthors is the half the decision did NOT widen, and it is
// also what RESERVES the canonical slug on this path: the vocabulary that folds
// a narrator credit spelled "Virtual Voice" refuses an AUTHOR credit spelled the
// same way, so no form can write that address for anybody real.
func TestAddWorkRefusesAIAuthors(t *testing.T) {
	for _, tc := range []struct{ name, author, why string }{
		{"generative system", "Sparky From ChatGPT", "an AI system, not a person"},
		{"synthetic voice", "Virtual Voice", "an AI voice"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := seedTree(t)
			body := addWorkBody("Model Authored Book", tc.author, "en", "Bob Reader", "US: B333333333", "Audible product page", true)
			res := Process(Options{DataDir: dir, Template: "add-work", Body: body})
			if res.Status != StatusInvalid {
				t.Fatalf("status = %q, want invalid; messages = %v", res.Status, res.Messages)
			}
			if !strings.Contains(strings.Join(res.Messages, "\n"), tc.why) {
				t.Errorf("messages = %v, want one naming %q", res.Messages, tc.why)
			}
			if recordExists(t, dir, "works/mo/model-authored-book/work.json") {
				t.Error("the refused submission still composed a work")
			}
			if recordExists(t, dir, "people/vi/virtual-voice.json") {
				t.Error("a refused author credit reached the canonical synthetic address")
			}
		})
	}
}

// TestCorrectDataAcceptsSyntheticKind pins the correction allowlist against the
// widened enum: personKinds is built from model.PersonKinds, so a kind added to
// the schema is accepted here with no second table to remember.
func TestCorrectDataAcceptsSyntheticKind(t *testing.T) {
	if !personKinds[model.KindEntitySynthetic] {
		t.Fatal("the correction allowlist does not accept the synthetic kind")
	}
	if len(personKinds) != len(model.PersonKinds()) {
		t.Errorf("personKinds has %d values, want model.PersonKinds()'s %d", len(personKinds), len(model.PersonKinds()))
	}
	dir := seedTree(t)
	body := correctBody("data/people/jo/john-smith.json", "kind", "Synthetic", "the listing credits a virtual voice", true)
	res := Process(Options{DataDir: dir, Template: "correct-data", Body: body})
	if res.Status != StatusOK {
		t.Fatalf("status = %q, messages = %v", res.Status, res.Messages)
	}
	if person := readFile(t, dir, "people/jo/john-smith.json"); !strings.Contains(person, `"kind": "synthetic"`) {
		t.Errorf("kind not corrected:\n%s", person)
	}
	if res := check.Load(dir); !res.OK() {
		t.Fatalf("tree failed validation after the correction:\n%v", res.Problems)
	}
}
