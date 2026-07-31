package check

import (
	"bytes"
	"fmt"
	"strings"

	meta "github.com/kodestar/audiosilo-meta"
	"github.com/kodestar/audiosilo-meta/pkg/model"
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

// violation is one schema failure kept apart from its rendering: Loc is the
// JSON-pointer instance location inside the validated value (or "(root)"), Msg
// the reason. The pack walker needs the two separately - a violation inside a
// composite's recordings map is re-reported against that recording, with its
// location rebased - so validate() is a thin renderer over violations().
type violation struct {
	Loc string
	Msg string
}

// String renders the violation the way a problem message reads. An empty Loc
// carries no location at all (a whole-value failure), so it renders bare.
func (v violation) String() string {
	if v.Loc == "" {
		return v.Msg
	}
	return v.Loc + ": " + v.Msg
}

// violations checks inst against the schema for kind. An empty slice means it
// validated.
func (s schemaSet) violations(kind model.Kind, inst any) []violation {
	sch, ok := s[kind]
	if !ok {
		return []violation{{Msg: fmt.Sprintf("no schema for kind %q", kind)}}
	}
	err := sch.Validate(inst)
	if err == nil {
		return nil
	}
	verr, ok := err.(*jsonschema.ValidationError)
	if !ok {
		return []violation{{Msg: collapse(err.Error())}}
	}
	var out []violation
	collectLeaves(verr.BasicOutput(), &out)
	if len(out) == 0 {
		out = append(out, violation{Msg: collapse(verr.Error())})
	}
	return out
}

// validate checks inst against the schema for kind, returning human-readable
// messages (one per underlying violation). An empty slice means it validated.
func (s schemaSet) validate(kind model.Kind, inst any) []string {
	vs := s.violations(kind, inst)
	msgs := make([]string, 0, len(vs))
	for _, v := range vs {
		msgs = append(msgs, v.String())
	}
	return msgs
}

// collectLeaves walks a BasicOutput tree, emitting one violation per leaf error.
func collectLeaves(u *jsonschema.OutputUnit, out *[]violation) {
	if u == nil {
		return
	}
	if u.Error != nil && len(u.Errors) == 0 {
		loc := u.InstanceLocation
		if loc == "" {
			loc = "(root)"
		}
		*out = append(*out, violation{Loc: loc, Msg: collapse(u.Error.String())})
	}
	for i := range u.Errors {
		collectLeaves(&u.Errors[i], out)
	}
}

// collapse flattens whitespace so a message is a single line.
func collapse(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
