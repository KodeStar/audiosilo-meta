package check

import (
	"bytes"
	"fmt"
	"strings"

	meta "github.com/kodestar/audiosilo-meta"
	"github.com/kodestar/audiosilo-meta/internal/model"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

const schemaBase = "https://meta.audiosilo.app/schema/"

var schemaFiles = map[model.Kind]string{
	model.KindWork:       "work.schema.json",
	model.KindRecording:  "recording.schema.json",
	model.KindPerson:     "person.schema.json",
	model.KindSeries:     "series.schema.json",
	model.KindCharacters: "characters.schema.json",
	model.KindRecaps:     "recaps.schema.json",
}

// schemaSet holds the compiled schema for each entity kind.
type schemaSet map[model.Kind]*jsonschema.Schema

// compileSchemas compiles the embedded JSON Schema files. It is the single
// place the authoritative schema/*.json files enter the validator.
func compileSchemas() (schemaSet, error) {
	c := jsonschema.NewCompiler()

	all := []string{"common.schema.json"}
	for _, f := range schemaFiles {
		all = append(all, f)
	}
	for _, f := range all {
		data, err := meta.SchemaFS.ReadFile("schema/" + f)
		if err != nil {
			return nil, fmt.Errorf("read embedded schema %s: %w", f, err)
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("parse schema %s: %w", f, err)
		}
		if err := c.AddResource(schemaBase+f, doc); err != nil {
			return nil, fmt.Errorf("add schema %s: %w", f, err)
		}
	}

	set := make(schemaSet, len(schemaFiles))
	for kind, f := range schemaFiles {
		sch, err := c.Compile(schemaBase + f)
		if err != nil {
			return nil, fmt.Errorf("compile schema %s: %w", f, err)
		}
		set[kind] = sch
	}
	return set, nil
}

// validate checks inst against the schema for kind, returning human-readable
// messages (one per underlying violation). An empty slice means it validated.
func (s schemaSet) validate(kind model.Kind, inst any) []string {
	sch, ok := s[kind]
	if !ok {
		return []string{fmt.Sprintf("no schema for kind %q", kind)}
	}
	err := sch.Validate(inst)
	if err == nil {
		return nil
	}
	verr, ok := err.(*jsonschema.ValidationError)
	if !ok {
		return []string{collapse(err.Error())}
	}
	var msgs []string
	collectLeaves(verr.BasicOutput(), &msgs)
	if len(msgs) == 0 {
		msgs = append(msgs, collapse(verr.Error()))
	}
	return msgs
}

// collectLeaves walks a BasicOutput tree, emitting one message per leaf error.
func collectLeaves(u *jsonschema.OutputUnit, out *[]string) {
	if u == nil {
		return
	}
	if u.Error != nil && len(u.Errors) == 0 {
		loc := u.InstanceLocation
		if loc == "" {
			loc = "(root)"
		}
		*out = append(*out, fmt.Sprintf("%s: %s", loc, collapse(u.Error.String())))
	}
	for i := range u.Errors {
		collectLeaves(&u.Errors[i], out)
	}
}

// collapse flattens whitespace so a message is a single line.
func collapse(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
