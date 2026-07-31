package check

import (
	"bytes"
	"strings"
	"testing"

	meta "github.com/kodestar/audiosilo-meta"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// compileAll compiles every embedded schema file, including the pack wrappers
// that compileSchemas does not yet carry. It is the guard that a new or edited
// schema file is still a valid, resolvable document.
func compileAll(t *testing.T) map[string]*jsonschema.Schema {
	t.Helper()
	ents, err := meta.SchemaFS.ReadDir("schema")
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
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
		"pack-series.schema.json",
	} {
		if set[want] == nil {
			t.Errorf("schema %s did not compile", want)
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
		"work": "dune", "license": "CC-BY-SA-3.0", "sources": [{"type": "community"}],
		"characters": [{"id": "paul", "name": "Paul", "reveal": {"chapter": 1}}]
	}`
	packValidRecaps = `{
		"work": "dune", "license": "CC-BY-SA-3.0", "sources": [{"type": "community"}],
		"recaps": [{"through": {"chapter": 1}, "text": "A start."}]
	}`
)

// added_at is optional and, when present, a full calendar date.
func TestAddedAtField(t *testing.T) {
	set := compileAll(t)
	for name, valid := range map[string]string{
		"work.schema.json":      validWork,
		"recording.schema.json": validRecording,
	} {
		if err := validateJSON(t, set[name], valid); err != nil {
			t.Errorf("%s rejected a valid added_at: %v", name, err)
		}
		without := strings.Replace(valid, `"added_at": "2026-07-31",`, "", 1)
		if err := validateJSON(t, set[name], without); err != nil {
			t.Errorf("%s rejected a record without added_at: %v", name, err)
		}
		bad := strings.Replace(valid, `"2026-07-31"`, `"2026-07"`, 1)
		if err := validateJSON(t, set[name], bad); err == nil {
			t.Errorf("%s accepted a partial added_at date", name)
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
					strings.Replace(validWork, `"CC0-1.0"`, `"CC-BY-SA-3.0"`, 1) + `}}`,
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
				"CC0 on a sidecar": `{"entries": {"dune": {"characters": ` + strings.Replace(packValidCharacters, `"CC-BY-SA-3.0"`, `"CC0-1.0"`, 1) + `}}}`,
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
