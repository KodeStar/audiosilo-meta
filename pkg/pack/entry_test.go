package pack

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

func TestWorkEntryCompose(t *testing.T) {
	e := WorkEntry{
		Work: &model.Work{ID: "dune"},
		Recordings: map[string]*model.Recording{
			"zed": {ID: "zed"},
			"abe": {ID: "abe"},
		},
	}
	w := e.Compose()
	if len(w.Recordings) != 2 || w.Recordings[0].ID != "abe" || w.Recordings[1].ID != "zed" {
		t.Fatalf("Compose = %+v, want recordings in slug order", w.Recordings)
	}
	// Compose is idempotent: a second call must not double the slice.
	if got := len(e.Compose().Recordings); got != 2 {
		t.Errorf("second Compose = %d recordings, want 2", got)
	}
	if (WorkEntry{}).Compose() != nil {
		t.Error("Compose on an empty entry returned a work")
	}
}

// A writer edits the decoded map, never a typed struct: marshalling
// model.Recording back out would state an "abridged": false the source never
// gave, and a fabricated fact is worse than a missing one.
func TestSetRecordingEditsTheDecodedComposite(t *testing.T) {
	raw := json.RawMessage(`{
		"id": "dune", "title": "Dune", "authors": ["frank-herbert"],
		"language": "en", "license": "CC0-1.0", "sources": [{"type": "user"}],
		"recordings": {"one": {"id": "one", "work": "dune", "runtime_min": 1234}}
	}`)
	entry, err := DecodeEntry(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := SetRecording(entry, "two", map[string]any{"id": "two", "work": "dune"}); err != nil {
		t.Fatal(err)
	}
	recs, _ := entry["recordings"].(map[string]any)
	if len(recs) != 2 || recs["one"] == nil || recs["two"] == nil {
		t.Fatalf("recordings = %v, want both", recs)
	}
	// The untouched recording keeps its number exactly, so a re-write is a
	// byte-level no-op for everything the edit did not reach.
	one, _ := recs["one"].(map[string]any)
	if n, ok := one["runtime_min"].(json.Number); !ok || n.String() != "1234" {
		t.Errorf("runtime_min = %#v, want the exact json.Number", one["runtime_min"])
	}
	out, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "abridged") {
		t.Errorf("a fact nobody stated was invented: %s", out)
	}
}

// A work with no recordings gains the map on the first splice.
func TestSetRecordingCreatesTheMap(t *testing.T) {
	entry, err := DecodeEntry(json.RawMessage(`{"id":"dune"}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := SetRecording(entry, "one", map[string]any{"id": "one"}); err != nil {
		t.Fatal(err)
	}
	recs, ok := entry["recordings"].(map[string]any)
	if !ok || recs["one"] == nil {
		t.Fatalf("recordings = %v", entry["recordings"])
	}
	if err := SetRecording(nil, "one", nil); err == nil {
		t.Error("SetRecording accepted a nil entry")
	}
	bad, err := DecodeEntry(json.RawMessage(`{"id":"dune","recordings":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := SetRecording(bad, "one", nil); err == nil {
		t.Error("SetRecording accepted a recordings member that is not an object")
	}
}

func TestDecodeEntry(t *testing.T) {
	if _, err := DecodeEntry(json.RawMessage(`[1,2]`)); err == nil {
		t.Error("DecodeEntry accepted a non-object")
	}
	if _, err := DecodeEntry(json.RawMessage(`null`)); err == nil {
		t.Error("DecodeEntry accepted null")
	}
	m, err := DecodeEntry(json.RawMessage(`{"n":1e400000}`))
	if err != nil {
		t.Fatalf("DecodeEntry rejected a number no float can hold: %v", err)
	}
	if n, _ := m["n"].(json.Number); n.String() != "1e400000" {
		t.Errorf("n = %#v, want the exact token", m["n"])
	}
}
