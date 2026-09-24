package issueform

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	meta "github.com/kodestar/audiosilo-meta"
	"github.com/kodestar/audiosilo-meta/pkg/check"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// schemaDirURL is where a contributor reads the schemas a refused value is
// checked against, named when a vocabulary is too long to quote in a verdict.
const schemaDirURL = "https://github.com/kodestar/audiosilo-meta/tree/main/schema"

// fieldSchema is what a correction needs to know about one top-level field of a
// record kind, read from the EMBEDDED schema rather than restated: that the
// field exists at all, and the closed vocabulary its value is drawn from, if
// any.
type fieldSchema struct {
	// enum is the closed set of values a scalar field accepts (a record's
	// license, a person's kind); nil when the schema leaves the value open.
	enum []string
	// itemEnum is the same for each entry of an array field (a work's genres).
	itemEnum []string
	// nested marks a name that is not a top-level field but a property of one
	// of the record's objects (a work's xref.isbn). A correction naming it is
	// about THIS record, just not about a field the form can write, so it is
	// never read as a field of the sibling kind (misaddressedField).
	nested bool
}

// recordFields maps every kind a correction can address to its schema's
// top-level properties, plus the names of its objects' own properties (marked
// nested). The kinds are correctableFields' own keys, so the two
// cannot disagree about which records a correction reaches. A parse failure is a
// programming error in an embedded file (TestRecordFieldsReadTheSchemas), so it
// panics rather than silently treating every field as unknown.
var recordFields = sync.OnceValue(func() map[model.Kind]map[string]fieldSchema {
	m, err := loadRecordFields()
	if err != nil {
		panic(fmt.Sprintf("issueform: embedded record schemas are unusable: %v", err))
	}
	return m
})

// loadRecordFields reads each correctable kind's schema out of meta.SchemaFS,
// resolving every property through its $ref chain so a field typed by a shared
// definition (license -> common.schema.json#/$defs/license) reports that
// definition's enum.
func loadRecordFields() (map[model.Kind]map[string]fieldSchema, error) {
	docs := map[string]map[string]any{}
	read := func(file string) (map[string]any, error) {
		if d, ok := docs[file]; ok {
			return d, nil
		}
		raw, err := meta.SchemaFS.ReadFile("schema/" + file)
		if err != nil {
			return nil, err
		}
		var d map[string]any
		if err := json.Unmarshal(raw, &d); err != nil {
			return nil, fmt.Errorf("%s: %w", file, err)
		}
		docs[file] = d
		return d, nil
	}

	out := make(map[model.Kind]map[string]fieldSchema, len(correctableFields))
	for kind := range correctableFields {
		file, ok := check.EntitySchemaFile(kind)
		if !ok {
			return nil, fmt.Errorf("no schema names the %s kind", kind)
		}
		doc, err := read(file)
		if err != nil {
			return nil, err
		}
		props, _ := doc["properties"].(map[string]any)
		if len(props) == 0 {
			return nil, fmt.Errorf("%s declares no properties", file)
		}
		fields := make(map[string]fieldSchema, len(props))
		nestedNames := map[string]bool{}
		for name, raw := range props {
			node, at, err := resolveSchemaRef(read, file, raw)
			if err != nil {
				return nil, fmt.Errorf("%s: property %q: %w", file, name, err)
			}
			fs := fieldSchema{enum: enumOf(node)}
			if node["type"] == "array" {
				items, _, err := resolveSchemaRef(read, at, node["items"])
				if err != nil {
					return nil, fmt.Errorf("%s: property %q items: %w", file, name, err)
				}
				fs.itemEnum = enumOf(items)
			}
			fields[name] = fs
			if node["type"] != "object" {
				continue
			}
			sub, _ := node["properties"].(map[string]any)
			for n := range sub {
				nestedNames[n] = true
			}
		}
		for n := range nestedNames {
			if _, top := fields[n]; !top {
				fields[n] = fieldSchema{nested: true}
			}
		}
		out[kind] = fields
	}
	return out, nil
}

// resolveSchemaRef follows a schema node's $ref chain to the node that carries
// the constraints, returning it with the file it sits in (a later relative ref
// resolves against that file). Only the one ref shape these schemas use is
// understood - "<file>#/$defs/<name>", the file part optional - and anything else
// is an error, so a schema edit that outgrows this reader fails its test rather
// than reading as an unconstrained field.
func resolveSchemaRef(read func(string) (map[string]any, error), file string, v any) (map[string]any, string, error) {
	for range 16 {
		node, ok := v.(map[string]any)
		if !ok {
			return nil, "", fmt.Errorf("schema node is not an object")
		}
		ref, ok := node["$ref"].(string)
		if !ok {
			return node, file, nil
		}
		target, frag, _ := strings.Cut(ref, "#")
		name, ok := strings.CutPrefix(frag, "/$defs/")
		if !ok || strings.Contains(name, "/") {
			return nil, "", fmt.Errorf("unsupported $ref %q", ref)
		}
		if target != "" {
			file = target
		}
		doc, err := read(file)
		if err != nil {
			return nil, "", err
		}
		defs, _ := doc["$defs"].(map[string]any)
		if v, ok = defs[name]; !ok {
			return nil, "", fmt.Errorf("$ref %q names no definition", ref)
		}
	}
	return nil, "", fmt.Errorf("$ref chain too deep")
}

// enumOf returns a node's string enum, or nil when it has none.
func enumOf(node map[string]any) []string {
	vals, _ := node["enum"].([]any)
	if len(vals) == 0 {
		return nil
	}
	out := make([]string, 0, len(vals))
	for _, v := range vals {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// enumViolation names why corrected cannot be written to field on a record of
// this kind - a value outside the schema's closed vocabulary - or returns "".
// Such a correction can never be applied, by a maintainer or anyone else, so it
// is refused as the submitter's to fix rather than parked for a human.
//
// The comparison ignores case: "Publisher" names the enum's "publisher" (the
// person-kind coercion lowercases it on the way in), and a value that differs
// only in case is a spelling question, not an unknown value.
func enumViolation(kind model.Kind, field, corrected string) string {
	fs := recordFields()[kind][field]
	switch {
	case fs.enum != nil:
		if !containsFold(fs.enum, corrected) {
			return fmt.Sprintf("%q is not an allowed value for %q - the schema allows only %s", corrected, field, allowedValues(fs.enum))
		}
	case fs.itemEnum != nil:
		for _, v := range splitList(corrected) {
			if !containsFold(fs.itemEnum, v) {
				return fmt.Sprintf("%q is not an allowed value for %q - each entry must be %s", v, field, allowedValues(fs.itemEnum))
			}
		}
	}
	return ""
}

// allowedValues renders a vocabulary for a verdict: quoted in full when it is
// short enough to read, pointed at when it is not (the genre list runs to
// dozens of values).
func allowedValues(vals []string) string {
	if len(vals) > 12 {
		return fmt.Sprintf("one of the %d values the schema lists (%s)", len(vals), schemaDirURL)
	}
	quoted := make([]string, len(vals))
	for i, v := range vals {
		quoted[i] = fmt.Sprintf("%q", v)
	}
	return strings.Join(quoted, ", ")
}

func containsFold(vals []string, s string) bool {
	s = strings.TrimSpace(s)
	for _, v := range vals {
		if strings.EqualFold(v, s) {
			return true
		}
	}
	return false
}
