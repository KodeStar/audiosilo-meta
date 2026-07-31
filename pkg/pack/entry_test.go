package pack

import (
	"encoding/json"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

const compositeEntry = `{
  "added_at": "2026-01-02",
  "authors": ["ann-doe"],
  "id": "dune",
  "language": "en",
  "license": "CC0-1.0",
  "recordings": {
    "rec-one": {
      "added_at": "2026-01-03",
      "id": "rec-one",
      "language": "en",
      "license": "CC0-1.0",
      "narrators": ["bob-roe"],
      "sources": [{"type": "user"}],
      "work": "dune"
    }
  },
  "sources": [{"type": "user"}],
  "title": "Dune"
}`

func TestWorkEntryRoundTrip(t *testing.T) {
	var e WorkEntry
	if err := json.Unmarshal([]byte(compositeEntry), &e); err != nil {
		t.Fatal(err)
	}
	if e.Work.ID != "dune" || e.Work.AddedAt != "2026-01-02" {
		t.Fatalf("work = %+v", e.Work)
	}
	if len(e.Recordings) != 1 || e.Recordings["rec-one"].Work != "dune" {
		t.Fatalf("recordings = %+v", e.Recordings)
	}
	if e.Recordings["rec-one"].AddedAt != "2026-01-03" {
		t.Errorf("recording added_at = %q", e.Recordings["rec-one"].AddedAt)
	}
	// The work's loader slice is not part of the wire shape.
	if e.Work.Recordings != nil {
		t.Error("unmarshal populated the loader slice")
	}

	out, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	var back WorkEntry
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatal(err)
	}
	if back.Work.Title != "Dune" || back.Recordings["rec-one"].ID != "rec-one" {
		t.Errorf("second round trip lost fields: %s", out)
	}
}

// A work with no recordings must not emit an empty map: the key is omitted, so
// a composite entry and a standalone work record are byte-identical.
func TestWorkEntryOmitsEmptyRecordings(t *testing.T) {
	e := WorkEntry{Work: &model.Work{ID: "dune", Title: "Dune", License: "CC0-1.0"}}
	out, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(out, &obj); err != nil {
		t.Fatal(err)
	}
	if _, ok := obj["recordings"]; ok {
		t.Errorf("empty recordings map emitted: %s", out)
	}
	// The work's own loader slice never reaches the wire either.
	e.Work.Recordings = []*model.Recording{{ID: "ghost"}}
	out, err = json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(out, &obj); err != nil {
		t.Fatal(err)
	}
	if _, ok := obj["recordings"]; ok {
		t.Errorf("the loader slice leaked onto the wire: %s", out)
	}
}

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

func TestCommunityEntry(t *testing.T) {
	raw := `{"characters":{"work":"dune","characters":[],"license":"CC-BY-SA-3.0","sources":[{"type":"community"}]}}`
	var e CommunityEntry
	if err := json.Unmarshal([]byte(raw), &e); err != nil {
		t.Fatal(err)
	}
	if e.Characters == nil || e.Characters.Work != "dune" {
		t.Fatalf("characters = %+v", e.Characters)
	}
	if e.Recaps != nil {
		t.Error("recaps decoded from an entry that has none")
	}
	out, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(out, &obj); err != nil {
		t.Fatal(err)
	}
	if _, ok := obj["recaps"]; ok {
		t.Errorf("absent sidecar emitted: %s", out)
	}
}
