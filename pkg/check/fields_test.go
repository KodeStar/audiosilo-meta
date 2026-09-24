package check

import (
	"slices"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// TestFieldsOfReadsTheCompiledSchemas pins FieldsOf over the real embedded
// schemas: a shared $ref is resolved down to its enum, an array's item enum is
// read through two refs, each field sits on the kind that owns it, and an
// object's own properties are named as nested.
func TestFieldsOfReadsTheCompiledSchemas(t *testing.T) {
	for _, k := range []model.Kind{model.KindWork, model.KindRecording, model.KindPerson, model.KindSeries} {
		fields, err := FieldsOf(k)
		if err != nil {
			t.Fatalf("FieldsOf(%s): %v", k, err)
		}
		if got := fields["license"].Enum; !slices.Equal(got, []string{"CC0-1.0"}) {
			t.Errorf("%s license enum = %v, want [CC0-1.0] (through common.schema.json#/$defs/license)", k, got)
		}
	}
	work, _ := FieldsOf(model.KindWork)
	rec, _ := FieldsOf(model.KindRecording)
	person, _ := FieldsOf(model.KindPerson)

	if want := slices.Sorted(slices.Values(model.PersonKinds())); !slices.Equal(person["kind"].Enum, want) {
		t.Errorf("person kind enum = %v, want model.PersonKinds() %v", person["kind"].Enum, want)
	}
	if genres := work["genres"].ItemEnum; len(genres) < 50 || !slices.Contains(genres, "epic-fantasy") {
		t.Errorf("work genres item enum = %d values, want the controlled vocabulary", len(genres))
	}
	if _, ok := rec["runtime_min"]; !ok {
		t.Error("runtime_min is not a recording field")
	}
	if _, ok := work["runtime_min"]; ok {
		t.Error("runtime_min is read as a work field")
	}
	if f, ok := work["isbn"]; !ok || !f.Nested {
		t.Errorf("a work's xref.isbn is not named as nested: %+v, %v", f, ok)
	}
	if f := work["title"]; f.Nested || f.Enum != nil {
		t.Errorf("title reads as %+v, want a plain top-level field", f)
	}
	if _, err := FieldsOf(model.Kind("nope")); err == nil {
		t.Error("a kind with no schema returned fields")
	}
}
