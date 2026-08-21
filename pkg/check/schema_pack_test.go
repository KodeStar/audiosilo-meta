package check

import (
	"bytes"
	"strings"
	"testing"

	meta "github.com/kodestar/audiosilo-meta"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// compileAll compiles every embedded schema file on its own, which is the guard
// that a new or edited schema file is still a valid, resolvable document.
// compileSchemas carries all of them too, so this differs from the real thing
// only in reaching each schema by name; it must match it in every other way,
// AssertFormat included, or these tests would prove something the tool does not
// do.
func compileAll(t *testing.T) map[string]*jsonschema.Schema {
	t.Helper()
	ents, err := meta.SchemaFS.ReadDir("schema")
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	c.AssertFormat()
	var names []string
	for _, e := range ents {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		names = append(names, e.Name())
		data, err := meta.SchemaFS.ReadFile("schema/" + e.Name())
		if err != nil {
			t.Fatal(err)
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("%s: %v", e.Name(), err)
		}
		if err := c.AddResource(schemaBase+e.Name(), doc); err != nil {
			t.Fatalf("%s: %v", e.Name(), err)
		}
	}
	out := map[string]*jsonschema.Schema{}
	for _, n := range names {
		sch, err := c.Compile(schemaBase + n)
		if err != nil {
			t.Fatalf("compile %s: %v", n, err)
		}
		out[n] = sch
	}
	return out
}

func TestEverySchemaCompiles(t *testing.T) {
	set := compileAll(t)
	for _, want := range []string{
		"common.schema.json", "work.schema.json", "recording.schema.json",
		"person.schema.json", "series.schema.json", "characters.schema.json",
		"recaps.schema.json", "pack-works.schema.json",
		"pack-works-community.schema.json", "pack-people.schema.json",
		"pack-series.schema.json", redirectsSchema,
	} {
		if set[want] == nil {
			t.Errorf("schema %s did not compile", want)
		}
	}
}

// TestEveryEntitySchemaCompiles is the drift guard that used to sit on the load
// path: every per-record schema compiles on its own. Load itself no longer does
// this - it compiles the four pack wrappers, and every entity schema is $ref'd
// from one of them - so a defect in a schema would otherwise only surface
// through whichever wrapper happened to reach the broken part of it.
//
// It reads entitySchemas rather than the directory, so it also fails when that
// map stops naming a real file.
func TestEveryEntitySchemaCompiles(t *testing.T) {
	set := compileAll(t)
	for kind, file := range entitySchemas {
		if set[file] == nil {
			t.Errorf("the schema for kind %q (%s) did not compile", kind, file)
		}
	}
}

func validateJSON(t *testing.T, sch *jsonschema.Schema, raw string) error {
	t.Helper()
	inst, err := jsonschema.UnmarshalJSON(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("invalid test JSON: %v", err)
	}
	return sch.Validate(inst)
}

const (
	validWork = `{
		"id": "dune", "title": "Dune", "authors": ["frank-herbert"],
		"language": "en", "added_at": "2026-07-31",
		"license": "CC0-1.0", "sources": [{"type": "user"}]
	}`
	validRecording = `{
		"id": "rec-one", "work": "dune", "narrators": ["ann-doe"],
		"language": "en", "added_at": "2026-07-31",
		"license": "CC0-1.0", "sources": [{"type": "user"}]
	}`
	packValidCharacters = `{
		"work": "dune", "license": "CC-BY-SA-4.0", "sources": [{"type": "community"}],
		"characters": [{"id": "paul", "name": "Paul", "reveal": {"chapter": 1}}]
	}`
	packValidRecaps = `{
		"work": "dune", "license": "CC-BY-SA-4.0", "sources": [{"type": "community"}],
		"recaps": [{"through": {"chapter": 1}, "text": "A start."}]
	}`
)

// added_at is optional and, when present, either a plain calendar date (what
// the importer and the intake bot stamp) or a full RFC 3339 timestamp with an
// offset (what the one-time git-history backfill writes verbatim, so the
// release artifact's bytes survive the storage migration unchanged).
func TestAddedAtField(t *testing.T) {
	set := compileAll(t)
	good := []string{
		"2026-07-31",
		"2026-07-12T18:23:45+01:00",
		"2026-07-12T17:23:45-05:00",
		"2026-07-12T17:23:45Z",
		"2026-07-12T17:23:45.123456Z",
	}
	bad := []string{
		"2026-07",                    // a partial date
		"2026-07-12T18:23:45",        // a local time is not a point in time
		"2026-07-12 18:23:45+01:00",  // no T separator
		"2026-07-12T18:23:45+0100",   // an unseparated offset
		"2026-07-31T",                // truncated
		"yesterday",                  // junk
		"2026-07-12T18:23:45+01:00Z", // two offsets
		// The pattern says the SHAPE; only the asserted "format" says the
		// value is a real point in time. Each of these matches the regex.
		"2026-02-30",           // February has no 30th
		"2026-13-01",           // no 13th month
		"2026-00-10",           // no zeroth month
		"2025-02-29",           // 2025 is not a leap year
		"2026-07-12T25:61:61Z", // no such time of day
	}
	for name, valid := range map[string]string{
		"work.schema.json":      validWork,
		"recording.schema.json": validRecording,
	} {
		without := strings.Replace(valid, `"added_at": "2026-07-31",`, "", 1)
		if err := validateJSON(t, set[name], without); err != nil {
			t.Errorf("%s rejected a record without added_at: %v", name, err)
		}
		for _, v := range good {
			raw := strings.Replace(valid, `"2026-07-31"`, `"`+v+`"`, 1)
			if err := validateJSON(t, set[name], raw); err != nil {
				t.Errorf("%s rejected added_at %q: %v", name, v, err)
			}
		}
		for _, v := range bad {
			raw := strings.Replace(valid, `"2026-07-31"`, `"`+v+`"`, 1)
			if err := validateJSON(t, set[name], raw); err == nil {
				t.Errorf("%s accepted added_at %q", name, v)
			}
		}
	}
}

// The composite entry nests recordings under the work; a standalone work is
// still valid without them, and the map keys are slugs.
func TestWorkRecordingsMap(t *testing.T) {
	set := compileAll(t)
	work := set["work.schema.json"]
	composite := strings.TrimSuffix(strings.TrimSpace(validWork), "}") +
		`, "recordings": {"rec-one": ` + validRecording + `}}`
	if err := validateJSON(t, work, composite); err != nil {
		t.Errorf("work schema rejected the composite entry: %v", err)
	}
	badKey := strings.TrimSuffix(strings.TrimSpace(validWork), "}") +
		`, "recordings": {"Rec One": ` + validRecording + `}}`
	if err := validateJSON(t, work, badKey); err == nil {
		t.Error("work schema accepted a non-slug recording key")
	}
	badRec := strings.TrimSuffix(strings.TrimSpace(validWork), "}") +
		`, "recordings": {"rec-one": {"id": "rec-one"}}}`
	if err := validateJSON(t, work, badRec); err == nil {
		t.Error("work schema accepted an invalid nested recording")
	}
}

func TestPackWrapperSchemas(t *testing.T) {
	set := compileAll(t)
	cases := []struct {
		schema string
		valid  string
		bad    map[string]string
	}{
		{
			schema: "pack-works.schema.json",
			valid:  `{"entries": {"dune": ` + validWork + `}}`,
			bad: map[string]string{
				"extra wrapper member": `{"entries": {}, "version": 1}`,
				"missing entries":      `{}`,
				"non-slug key":         `{"entries": {"Dune!": ` + validWork + `}}`,
				"invalid entry":        `{"entries": {"dune": {"id": "dune"}}}`,
				"share-alike license": `{"entries": {"dune": ` +
					strings.Replace(validWork, `"CC0-1.0"`, `"CC-BY-SA-4.0"`, 1) + `}}`,
			},
		},
		{
			schema: "pack-people.schema.json",
			valid: `{"entries": {"ann-doe": {"id": "ann-doe", "name": "Ann Doe",` +
				` "license": "CC0-1.0", "sources": [{"type": "user"}]}}}`,
			bad: map[string]string{
				"invalid entry": `{"entries": {"ann-doe": {"id": "ann-doe"}}}`,
			},
		},
		{
			schema: "pack-series.schema.json",
			valid: `{"entries": {"dune": {"id": "dune", "name": "Dune",` +
				` "works": [{"work": "dune", "position": "1"}],` +
				` "license": "CC0-1.0", "sources": [{"type": "user"}]}}}`,
			bad: map[string]string{
				"invalid entry": `{"entries": {"dune": {"id": "dune"}}}`,
			},
		},
		{
			schema: "pack-works-community.schema.json",
			valid:  `{"entries": {"dune": {"characters": ` + packValidCharacters + `, "recaps": ` + packValidRecaps + `}}}`,
			bad: map[string]string{
				"neither member":   `{"entries": {"dune": {}}}`,
				"unknown member":   `{"entries": {"dune": {"notes": {}}}}`,
				"invalid sidecar":  `{"entries": {"dune": {"characters": {"work": "dune"}}}}`,
				"CC0 on a sidecar": `{"entries": {"dune": {"characters": ` + strings.Replace(packValidCharacters, `"CC-BY-SA-4.0"`, `"CC0-1.0"`, 1) + `}}}`,
			},
		},
	}
	for _, c := range cases {
		sch := set[c.schema]
		if sch == nil {
			t.Fatalf("%s did not compile", c.schema)
		}
		if err := validateJSON(t, sch, c.valid); err != nil {
			t.Errorf("%s rejected a valid pack: %v", c.schema, err)
		}
		if err := validateJSON(t, sch, `{"entries": {}}`); err != nil {
			t.Errorf("%s rejected an empty pack (emptiness is a metacheck rule, not a schema one): %v", c.schema, err)
		}
		for name, raw := range c.bad {
			if err := validateJSON(t, sch, raw); err == nil {
				t.Errorf("%s accepted %s", c.schema, name)
			}
		}
	}
}

// Either sidecar alone is a valid works-community entry.
func TestCommunityPackSingleSidecar(t *testing.T) {
	sch := compileAll(t)["pack-works-community.schema.json"]
	for name, raw := range map[string]string{
		"characters only": `{"entries": {"dune": {"characters": ` + packValidCharacters + `}}}`,
		"recaps only":     `{"entries": {"dune": {"recaps": ` + packValidRecaps + `}}}`,
	} {
		if err := validateJSON(t, sch, raw); err != nil {
			t.Errorf("%s rejected: %v", name, err)
		}
	}
}
