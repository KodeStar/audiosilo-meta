package canonical

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestFormatCanonicalShape(t *testing.T) {
	in := []byte(`{"b":2,"a":1,"nested":{"z":1,"y":2}}`)
	got, err := Format(in)
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"a\": 1,\n  \"b\": 2,\n  \"nested\": {\n    \"y\": 2,\n    \"z\": 1\n  }\n}\n"
	if string(got) != want {
		t.Errorf("Format shape mismatch:\n got %q\nwant %q", got, want)
	}
}

func TestFormatIdempotent(t *testing.T) {
	in := []byte(`{"b":[3,1,2],"a":"x"}`)
	once, err := Format(in)
	if err != nil {
		t.Fatal(err)
	}
	twice, err := Format(once)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(once, twice) {
		t.Errorf("Format not idempotent:\n once %q\ntwice %q", once, twice)
	}
	if ok, _ := IsCanonical(once); !ok {
		t.Errorf("formatted output should be canonical")
	}
}

func TestFormatPreservesData(t *testing.T) {
	// Round-trip: array order, big integers, and no-HTML-escaping survive.
	in := []byte(`{"tags":["b","a"],"big":90071992547409919007199254740991,"html":"a<b>&c"}`)
	got, err := Format(in)
	if err != nil {
		t.Fatal(err)
	}
	// With HTML escaping off, <, > and & appear literally. If the encoder had
	// escaped them, this literal substring would instead be < etc.
	if !bytes.Contains(got, []byte(`"a<b>&c"`)) {
		t.Errorf("HTML characters should be unescaped, got: %s", got)
	}
	if !bytes.Contains(got, []byte("90071992547409919007199254740991")) {
		t.Errorf("large integer not preserved exactly: %s", got)
	}

	var a, b any
	if err := json.Unmarshal(in, &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(got, &b); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Errorf("data changed across canonicalization:\n in  %#v\n out %#v", a, b)
	}
}

func TestIsCanonicalDetectsNonCanonical(t *testing.T) {
	if ok, _ := IsCanonical([]byte(`{"b":1,"a":2}`)); ok {
		t.Errorf("unsorted keys should not be canonical")
	}
	if ok, _ := IsCanonical([]byte("{\n    \"a\": 1\n}\n")); ok {
		t.Errorf("4-space indent should not be canonical")
	}
	if ok, _ := IsCanonical([]byte(`{"a":1}`)); ok {
		t.Errorf("compact form should not be canonical")
	}
	if _, err := Format([]byte(`{"a":1} trailing`)); err == nil {
		t.Errorf("trailing content should error")
	}
	if _, err := Format([]byte(`not json`)); err == nil {
		t.Errorf("invalid JSON should error")
	}
}

func TestCheckAndWriteTree(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.json")
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(good, []byte("{\n  \"a\": 1\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bad, []byte(`{"b":2,"a":1}`), 0o644); err != nil {
		t.Fatal(err)
	}

	nonCanon, err := CheckTree(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(nonCanon) != 1 || nonCanon[0] != bad {
		t.Errorf("CheckTree = %v, want [%s]", nonCanon, bad)
	}

	changed, failed, err := WriteTree(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(failed) != 0 {
		t.Errorf("unexpected failed files: %v", failed)
	}
	if len(changed) != 1 || changed[0] != bad {
		t.Errorf("WriteTree changed = %v, want [%s]", changed, bad)
	}

	after, err := CheckTree(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 0 {
		t.Errorf("tree still non-canonical after write: %v", after)
	}
}
