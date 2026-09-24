package issueform

import (
	"slices"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// TestRecordFieldsReadTheSchemas pins the schema reader recordFields is built on:
// it parses (a failure there panics every correction), it resolves a shared
// $ref down to its enum, and it places each field on the kind that owns it.
func TestRecordFieldsReadTheSchemas(t *testing.T) {
	fields, err := loadRecordFields()
	if err != nil {
		t.Fatalf("loadRecordFields: %v", err)
	}
	for _, k := range []model.Kind{model.KindWork, model.KindRecording, model.KindPerson, model.KindSeries} {
		if got := fields[k]["license"].enum; !slices.Equal(got, []string{licenseCC0}) {
			t.Errorf("%s license enum = %v, want [%s] (through common.schema.json#/$defs/license)", k, got, licenseCC0)
		}
	}
	kinds := slices.Sorted(slices.Values(fields[model.KindPerson]["kind"].enum))
	if want := slices.Sorted(slices.Values(model.PersonKinds())); !slices.Equal(kinds, want) {
		t.Errorf("person kind enum = %v, want model.PersonKinds() %v", kinds, want)
	}
	if got := len(fields[model.KindWork]["genres"].itemEnum); got != len(genreVocabulary()) {
		t.Errorf("work genres item enum has %d values, want the genre vocabulary's %d", got, len(genreVocabulary()))
	}
	if _, ok := fields[model.KindRecording]["runtime_min"]; !ok {
		t.Error("runtime_min is not read as a recording field")
	}
	if _, ok := fields[model.KindWork]["runtime_min"]; ok {
		t.Error("runtime_min is read as a work field")
	}
	if fs, ok := fields[model.KindWork]["isbn"]; !ok || !fs.nested {
		t.Errorf("a work's xref.isbn is not read as a nested name: %+v, %v", fs, ok)
	}
}

// TestCorrectableFieldsAreSchemaFields is the drift guard between the
// correction allowlist and the schemas it now consults: every field a
// correction may write must be a top-level property of its kind, or the
// misaddressed-field and enum checks would be reading a different record than
// the one the allowlist writes to.
func TestCorrectableFieldsAreSchemaFields(t *testing.T) {
	fields := recordFields()
	for kind, ops := range correctableFields {
		for name := range ops {
			fs, ok := fields[kind][name]
			if !ok || fs.nested {
				t.Errorf("correctableFields lists %s.%s, which is no top-level field of the %s schema", kind, name, kind)
			}
		}
	}
}

// TestRuntimeOnAWorkNamesTheRecording is the finding: runtime_min submitted
// against a WORK was told "only simple scalar fields are" auto-corrected, which
// is false (runtime_min is one) and sends nobody anywhere. The field lives on a
// recording, so the verdict says so, names the recording reference form and
// lists the work's recordings - each of which must resolve back to that
// recording if pasted into Record.
func TestRuntimeOnAWorkNamesTheRecording(t *testing.T) {
	dir := seedTree(t)
	for _, ref := range []string{
		"https://meta.audiosilo.app/works/existing-work",
		"data/works/ex/existing-work/work.json",
	} {
		t.Run(ref, func(t *testing.T) {
			body := correctBody(ref, "runtime", "499", "Audible listing", true)
			res := Process(Options{DataDir: dir, Template: "correct-data", Body: body})
			if res.Status != StatusInvalid {
				t.Fatalf("status = %q, want invalid; messages = %v", res.Status, res.Messages)
			}
			want := recordingRefPath("existing-work", "john-smith-2020")
			if !anyContains(res.Messages, `"runtime_min" is a recording field`) || !anyContains(res.Messages, want) {
				t.Errorf("the verdict does not name the recording (%s): %v", want, res.Messages)
			}
			if anyContains(res.Messages, "only simple scalar fields") {
				t.Errorf("the misleading message survived: %v", res.Messages)
			}
			if len(res.Files) != 0 {
				t.Errorf("a refused correction wrote: %v", res.Files)
			}
		})
	}

	rr, ok := resolveRecordRef(recordingRefPath("existing-work", "john-smith-2020"))
	if !ok || rr.kind != model.KindRecording || rr.workSlug != "existing-work" || rr.slug != "john-smith-2020" {
		t.Errorf("the reference the verdict offers does not resolve to the recording: %+v, %v", rr, ok)
	}
}

// TestRuntimeOnAWorkFollowsTheCorrectionOnceRefiled: the reference the verdict
// hands out is the one that works - resubmitted against it, the same correction
// applies.
func TestRuntimeOnAWorkFollowsTheCorrectionOnceRefiled(t *testing.T) {
	dir := seedTree(t)
	body := correctBody(recordingRefPath("existing-work", "john-smith-2020"), "runtime_min", "499", "Audible listing", true)
	if res := Process(Options{DataDir: dir, Template: "correct-data", Body: body}); res.Status != StatusOK {
		t.Fatalf("status = %q, messages = %v", res.Status, res.Messages)
	}
}

// TestWorkFieldOnARecordingNamesTheWork is the mirror case: a title submitted
// against a recording path names the WORK page, which resolves back to it.
func TestWorkFieldOnARecordingNamesTheWork(t *testing.T) {
	dir := seedTree(t)
	body := correctBody("data/works/ex/existing-work/recordings/john-smith-2020.json", "title", "A Better Title", "the cover", true)
	res := Process(Options{DataDir: dir, Template: "correct-data", Body: body})
	if res.Status != StatusInvalid {
		t.Fatalf("status = %q, want invalid; messages = %v", res.Status, res.Messages)
	}
	url := workPageURL("existing-work")
	if !anyContains(res.Messages, `"title" is a work field`) || !anyContains(res.Messages, url) {
		t.Errorf("the verdict does not name the work (%s): %v", url, res.Messages)
	}
	rr, ok := resolveRecordRef(url)
	if !ok || rr.kind != model.KindWork || rr.slug != "existing-work" {
		t.Errorf("the work URL the verdict offers does not resolve to the work: %+v, %v", rr, ok)
	}
}

// TestSharedAndNestedNamesAreNotMisaddressed: a field both kinds carry is a
// correction to the record addressed, and a name the addressed record carries
// NESTED (a work's xref.isbn) may well mean this record - neither is redirected.
func TestSharedAndNestedNamesAreNotMisaddressed(t *testing.T) {
	for _, c := range []struct{ ref, field, value string }{
		{"data/works/ex/existing-work/work.json", "language", "de"},
		{"data/works/ex/existing-work/recordings/john-smith-2020.json", "language", "de"},
		{"data/works/ex/existing-work/work.json", "isbn", "9781473647633"},
	} {
		body := correctBody(c.ref, c.field, c.value, "web", true)
		res := Process(Options{DataDir: seedTree(t), Template: "correct-data", Body: body})
		if anyContains(res.Messages, "is a recording field") || anyContains(res.Messages, "is a work field") {
			t.Errorf("%s on %s was redirected: %v", c.field, c.ref, res.Messages)
		}
	}
}

// TestClosedVocabularyValuesAreRefusedAsInvalid is the second finding: license
// "MIT" was parked for a maintainer who could never apply it, since the schema
// admits CC0-1.0 alone in the core. A value outside ANY enum a correction can
// name is the submitter's to fix, and the verdict names what is allowed.
func TestClosedVocabularyValuesAreRefusedAsInvalid(t *testing.T) {
	cases := []struct {
		ref, field, value, want string
	}{
		{"data/works/ex/existing-work/work.json", "license", "MIT", `"CC0-1.0"`},
		{"data/works/ex/existing-work/recordings/john-smith-2020.json", "license", "CC-BY-SA-4.0", `"CC0-1.0"`},
		{"data/people/jo/john-smith.json", "license", "MIT", `"CC0-1.0"`},
		{"data/series/ex/existing-series.json", "license", "MIT", `"CC0-1.0"`},
		{"data/people/jo/john-smith.json", "kind", "corporation", `"publisher"`},
		{"data/works/ex/existing-work/work.json", "genres", "fantasy, space opera noir", "values the schema lists"},
	}
	for _, c := range cases {
		t.Run(c.field+"="+c.value, func(t *testing.T) {
			body := correctBody(c.ref, c.field, c.value, "web", true)
			res := Process(Options{DataDir: seedTree(t), Template: "correct-data", Body: body})
			if res.Status != StatusInvalid {
				t.Fatalf("status = %q, want invalid; messages = %v", res.Status, res.Messages)
			}
			if !anyContains(res.Messages, "is not an allowed value") || !anyContains(res.Messages, c.want) {
				t.Errorf("the verdict does not name the allowed values (%s): %v", c.want, res.Messages)
			}
			if anyContains(res.Messages, "maintainer will apply") {
				t.Errorf("a value no one can apply was promised to a maintainer: %v", res.Messages)
			}
		})
	}
}

// TestClosedVocabularyKeepsTheGenuineVerdicts: an in-vocabulary value keeps the
// verdict it always had. A license already CC0-1.0 is a no-op rather than a
// maintainer's job, valid genres (which a correction cannot write) still go to a
// maintainer, and an in-enum person kind still applies.
func TestClosedVocabularyKeepsTheGenuineVerdicts(t *testing.T) {
	cases := []struct {
		ref, field, value string
		want              Status
	}{
		{"data/works/ex/existing-work/work.json", "license", "cc0-1.0", StatusDuplicate},
		{"data/works/ex/existing-work/work.json", "genres", "fantasy, classics", StatusNeedsHuman},
		{"data/people/jo/john-smith.json", "kind", "Group", StatusOK},
	}
	for _, c := range cases {
		t.Run(c.field+"="+c.value, func(t *testing.T) {
			body := correctBody(c.ref, c.field, c.value, "web", true)
			res := Process(Options{DataDir: seedTree(t), Template: "correct-data", Body: body})
			if res.Status != c.want {
				t.Fatalf("status = %q, want %q; messages = %v", res.Status, c.want, res.Messages)
			}
			if c.want == StatusDuplicate && !strings.Contains(strings.Join(res.Messages, "\n"), "nothing to change") {
				t.Errorf("a no-op license correction was not reported as one: %v", res.Messages)
			}
		})
	}
}
