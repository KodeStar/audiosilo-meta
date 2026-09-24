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
		p := deref(prop)
		f := Field{Enum: enumValues(p)}
		if p.Items2020 != nil {
			f.ItemEnum = enumValues(deref(p.Items2020))
		}
		fields[name] = f
		for sub := range p.Properties {
			nested = append(nested, sub)
		}
	}
	for _, sub := range nested {
		if _, top := fields[sub]; !top {
			fields[sub] = Field{Nested: true}
		}
	}
	return fields
}

// deref follows a schema's $ref chain to the schema carrying its constraints.
func deref(s *jsonschema.Schema) *jsonschema.Schema {
	for s.Ref != nil {
		s = s.Ref
	}
	return s
}

// enumValues is a schema's string enum, sorted, or nil when it has none.
func enumValues(s *jsonschema.Schema) []string {
	if s.Enum == nil {
		return nil
	}
	out := make([]string, 0, len(s.Enum.Values))
	for _, v := range s.Enum.Values {
		if str, ok := v.(string); ok {
			out = append(out, str)
		}
	}
	slices.Sort(out)
	return out
}
