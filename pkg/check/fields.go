package check

import (
	"fmt"
	"slices"
	"sync"

	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Field is what a record kind's schema says about one of its field names: that
// the record has it, and the closed vocabulary its value is drawn from, if any.
// It is read off the COMPILED schema, so every $ref is the library's to resolve
// and a reader of a kind's fields never keeps a second schema parser.
type Field struct {
	// Enum is the closed set of values a scalar field accepts (a record's
	// license, a person's kind); nil when the schema leaves the value open.
	Enum []string
	// ItemEnum is the same for each entry of an array field (a work's genres).
	ItemEnum []string
	// Nested marks a name that is not a top-level field but a property of one
	// of the record's objects (a work's xref.isbn): the record carries the name,
	// just not as a field of its own.
	Nested bool
}

// FieldsOf returns the fields a record kind's schema declares, keyed by name:
// every top-level property, plus the properties of its object-valued ones
// (marked Nested, and only where no top-level field has the name). The schemas
// are embedded, so the answer is computed once per process; a kind with no
// schema is an error.
func FieldsOf(k model.Kind) (map[string]Field, error) {
	all, err := entityFields()
	if err != nil {
		return nil, err
	}
	fields, ok := all[k]
	if !ok {
		return nil, fmt.Errorf("no schema describes the %q kind", k)
	}
	return fields, nil
}

// entityFields compiles every entity schema once and reads its fields.
var entityFields = sync.OnceValues(func() (map[model.Kind]map[string]Field, error) {
	c, err := newSchemaCompiler()
	if err != nil {
		return nil, err
	}
	out := make(map[model.Kind]map[string]Field, len(entitySchemas))
	for kind, file := range entitySchemas {
		sch, err := c.Compile(schemaBase + file)
		if err != nil {
			return nil, fmt.Errorf("compile schema %s: %w", file, err)
		}
		out[kind] = fieldsOf(sch)
	}
	return out, nil
})

// fieldsOf reads one compiled entity schema's field table.
func fieldsOf(sch *jsonschema.Schema) map[string]Field {
	fields := make(map[string]Field, len(sch.Properties))
	var nested []string
	for name, prop := range sch.Properties {
		f := Field{Enum: enumValues(prop)}
		if items := inChain(prop, func(s *jsonschema.Schema) bool { return s.Items2020 != nil }); items != nil {
			f.ItemEnum = enumValues(items.Items2020)
		}
		fields[name] = f
		if obj := inChain(prop, func(s *jsonschema.Schema) bool { return len(s.Properties) > 0 }); obj != nil {
			for sub := range obj.Properties {
				nested = append(nested, sub)
			}
		}
	}
	for _, sub := range nested {
		if _, top := fields[sub]; !top {
			fields[sub] = Field{Nested: true}
		}
	}
	return fields
}

// inChain returns the first schema along s's $ref chain - s itself, then what
// each $ref names - for which has holds, or nil. Draft 2020-12 lets a $ref
// carry sibling keywords, so the constraint may sit on the referring schema
// rather than at the end of the chain; stopping only at the chain's end would
// drop a sibling enum or items silently.
func inChain(s *jsonschema.Schema, has func(*jsonschema.Schema) bool) *jsonschema.Schema {
	for ; s != nil; s = s.Ref {
		if has(s) {
			return s
		}
	}
	return nil
}

// enumValues is the string enum found along a schema's $ref chain, sorted, or
// nil when it has none. An enum holding anything but strings is nil too, rather
// than an empty vocabulary that would refuse every value.
func enumValues(s *jsonschema.Schema) []string {
	e := inChain(s, func(s *jsonschema.Schema) bool { return s.Enum != nil })
	if e == nil {
		return nil
	}
	out := make([]string, 0, len(e.Enum.Values))
	for _, v := range e.Enum.Values {
		str, ok := v.(string)
		if !ok {
			return nil
		}
		out = append(out, str)
	}
	slices.Sort(out)
	return out
}
