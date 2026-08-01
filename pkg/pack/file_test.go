package pack

import (
	"bytes"
	"encoding/json"
	"maps"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/canonical"
)

const samplePack = `{
  "entries": {
    "a-work": {
      "id": "a-work",
      "license": "CC0-1.0",
      "recordings": {
        "rec-one": {
          "id": "rec-one",
          "work": "a-work"
        }
      },
      "title": "A Work"
    },
    "b-work": {
      "id": "b-work",
      "nested": {
        "deep": [
          1,
          2,
          3
        ]
      }
    }
  }
}
`

func TestParseRoundTripIsByteIdentical(t *testing.T) {
	f, err := Parse([]byte(samplePack))
	if err != nil {
		t.Fatal(err)
	}
	got, err := f.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != samplePack {
		t.Errorf("round trip changed the bytes:\n--- got ---\n%s\n--- want ---\n%s", got, samplePack)
	}
}

// The pack renderer reproduces pkg/canonical's form exactly; this is the guard
// against the two drifting apart.
func TestRenderMatchesCanonical(t *testing.T) {
	cases := []string{
		samplePack,
		`{"entries":{}}`,
		`{"entries":{"only":{"a":1}}}`,
		`{"entries":{"esc":{"s":"a<b>c&d","n":1.5000000000000000001,"u":"caf\u00e9"}}}`,
	}
	for _, raw := range cases {
		f, err := Parse([]byte(raw))
		if err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		got, err := f.Bytes()
		if err != nil {
			t.Fatal(err)
		}
		want, err := canonical.Format([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Errorf("%s:\n--- pack ---\n%s\n--- canonical ---\n%s", raw, got, want)
		}
		ok, err := canonical.IsCanonical(got)
		if err != nil || !ok {
			t.Errorf("%s: rendered pack is not canonical (%v)", raw, err)
		}
	}
}

// Unknown fields are kept verbatim: the tooling must never silently drop data
// it does not model.
func TestParsePreservesUnknownEntryFields(t *testing.T) {
	f, err := Parse([]byte(`{"entries":{"x":{"id":"x","future_field":{"k":[1,2]}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	out, err := f.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "future_field") {
		t.Errorf("unknown field dropped: %s", out)
	}
}

func TestParseRejectsBadWrappers(t *testing.T) {
	cases := map[string]string{
		"extra member":  `{"entries":{},"version":1}`,
		"no entries":    `{}`,
		"entries array": `{"entries":[]}`,
		"entries null":  `{"entries":null}`,
		"trailing":      `{"entries":{}}{"entries":{}}`,
		"not json":      `{`,
	}
	for name, raw := range cases {
		if _, err := Parse([]byte(raw)); err == nil {
			t.Errorf("%s: Parse accepted %s", name, raw)
		}
	}
}

func TestFileMutation(t *testing.T) {
	f := NewFile()
	f.Set("b", json.RawMessage(`{"id":"b"}`))
	f.Set("a", json.RawMessage(`{"id":"a"}`))
	if got := f.Slugs(); got[0] != "a" || got[1] != "b" {
		t.Errorf("Slugs() = %v, want sorted", got)
	}
	if _, ok := f.Get("a"); !ok {
		t.Error("Get(a) missing")
	}
	f.Remove("a")
	if _, ok := f.Get("a"); ok {
		t.Error("Get(a) after Remove still present")
	}
	if f.Len() != 1 {
		t.Errorf("Len() = %d, want 1", f.Len())
	}
	clone := f.Clone()
	clone.Remove("b")
	if f.Len() != 1 {
		t.Error("Clone is not independent")
	}
}

// Set copies the entry bytes, so a caller reusing its buffer cannot corrupt the
// pack.
func TestSetCopiesEntry(t *testing.T) {
	buf := []byte(`{"id":"a"}`)
	f := NewFile()
	f.Set("a", buf)
	copy(buf, []byte(`{"id":"Z"}`))
	got, _ := f.Get("a")
	if string(got) != `{"id":"a"}` {
		t.Errorf("entry = %s, want the value as it was at Set time", got)
	}
}

func TestSizesSumToTheFile(t *testing.T) {
	f, err := Parse([]byte(samplePack))
	if err != nil {
		t.Fatal(err)
	}
	total, per, err := f.Sizes()
	if err != nil {
		t.Fatal(err)
	}
	sum := 0
	for _, n := range per {
		sum += n
	}
	overhead := len(packPrefix) + len("\n") + len(packSuffix)
	if sum+overhead != total {
		t.Errorf("entry sizes %d + wrapper %d != file %d", sum, overhead, total)
	}
	if total != len(samplePack) {
		t.Errorf("Sizes total = %d, want %d", total, len(samplePack))
	}
}

// The memo is a pure function of the entries, so releasing it may change nothing
// but the memory: the same bytes and the same sizes come back afterwards. That
// is what lets a pass that has finished with the sizes hand back the copy of the
// pack the render left behind - on a whole tree, more memory than the entries
// themselves (see check.readPack).
func TestReleaseMemoKeepsTheAnswersAndDropsTheCopy(t *testing.T) {
	f := NewFile()
	for _, slug := range []string{"ann-doe", "bob-roe", "cal-loe"} {
		f.Set(slug, []byte(`{"id":"`+slug+`","name":"`+strings.Repeat("x", 4096)+`"}`))
	}
	wantBytes, err := f.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	wantTotal, wantPer, err := f.Sizes()
	if err != nil {
		t.Fatal(err)
	}
	if f.rendered == nil || f.sizes == nil {
		t.Fatal("Sizes left no memo, so there is nothing to release")
	}

	f.ReleaseMemo()
	if f.rendered != nil || f.sizes != nil {
		t.Error("ReleaseMemo kept the memo")
	}

	gotBytes, err := f.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotBytes, wantBytes) {
		t.Error("the pack renders differently after ReleaseMemo")
	}
	gotTotal, gotPer, err := f.Sizes()
	if err != nil {
		t.Fatal(err)
	}
	if gotTotal != wantTotal || !maps.Equal(gotPer, wantPer) {
		t.Errorf("sizes after ReleaseMemo = %d %v, want %d %v", gotTotal, gotPer, wantTotal, wantPer)
	}
	if f.Len() != 3 {
		t.Errorf("entries = %d, want 3: ReleaseMemo changed what the pack holds", f.Len())
	}
}
