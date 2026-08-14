package model

import (
	"encoding/json"
	"reflect"
	"testing"
)

// TestRedirectKindsAreTheThreeNamespaces pins the closed set and its order: the
// order is the canonical file's key order, and the set is what every reader,
// writer and rule iterates.
func TestRedirectKindsAreTheThreeNamespaces(t *testing.T) {
	want := []RedirectKind{RedirectPeople, RedirectSeries, RedirectWorks}
	if got := RedirectKinds(); !reflect.DeepEqual(got, want) {
		t.Errorf("RedirectKinds() = %v, want %v", got, want)
	}
	for _, k := range want {
		if !ValidRedirectKind(k) {
			t.Errorf("%q is not reported valid", k)
		}
	}
	for _, k := range []RedirectKind{"", "work", "recordings", "Works"} {
		if ValidRedirectKind(k) {
			t.Errorf("%q is reported valid", k)
		}
	}
	// The kind IS the family directory and the API path segment, which is what
	// lets the server build a Location out of it.
	if string(RedirectWorks) != "works" || string(RedirectPeople) != "people" || string(RedirectSeries) != "series" {
		t.Error("a redirect kind is no longer spelled as its route segment")
	}
}

// TestRedirectsJSONIsTheFileShape pins that the type IS the contract: no
// marshalling code stands between it and schema/redirects.schema.json, so the
// file's keys are the kinds and nothing has to be kept in step.
func TestRedirectsJSONIsTheFileShape(t *testing.T) {
	r := NewRedirects()
	r[RedirectWorks]["old-book"] = "new-book"
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"people":{},"series":{},"works":{"old-book":"new-book"}}`
	if string(raw) != want {
		t.Errorf("marshalled = %s, want %s", raw, want)
	}
	var back Redirects
	if err := json.Unmarshal([]byte(want), &back); err != nil {
		t.Fatal(err)
	}
	if got := back.Target(RedirectWorks, "old-book"); got != "new-book" {
		t.Errorf("round-tripped target = %q", got)
	}
}

func TestRedirectsTargetAndLen(t *testing.T) {
	var empty Redirects
	if got := empty.Target(RedirectWorks, "anything"); got != "" {
		t.Errorf("nil table target = %q, want empty", got)
	}
	if got := empty.Len(); got != 0 {
		t.Errorf("nil table Len = %d", got)
	}
	r := NewRedirects()
	r[RedirectWorks]["a"] = "b"
	r[RedirectPeople]["c"] = "d"
	if got := r.Len(); got != 2 {
		t.Errorf("Len = %d, want 2", got)
	}
	if got := r.Target(RedirectPeople, "a"); got != "" {
		t.Errorf("namespaces leaked: people/a = %q", got)
	}
}
