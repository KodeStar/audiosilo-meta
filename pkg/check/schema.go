package check

import (
	"bytes"
	"fmt"
	"strings"

	meta "github.com/kodestar/audiosilo-meta"
	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

const schemaBase = "https://meta.audiosilo.app/schema/"

// entitySchemas are the per-record schemas, which the legacy file-per-entity
// reader validates against directly.
var entitySchemas = map[model.Kind]string{
	model.KindWork:       "work.schema.json",
	model.KindRecording:  "recording.schema.json",
	model.KindPerson:     "person.schema.json",
	model.KindSeries:     "series.schema.json",
	model.KindCharacters: "characters.schema.json",
	model.KindRecaps:     "recaps.schema.json",
}

// wrapperSchemas are the per-family pack wrappers. The pack walker reaches an
// entry THROUGH its family's wrapper (see schemaSet.entry), so these files are
// load-bearing rather than documentation: change what a wrapper says an entry
// is, and the walker validates the new thing.
var wrapperSchemas = map[pack.Family]string{
	pack.FamilyWorks:          "pack-works.schema.json",
	pack.FamilyWorksCommunity: "pack-works-community.schema.json",
	pack.FamilyPeople:         "pack-people.schema.json",
	pack.FamilySeries:         "pack-series.schema.json",
}

// The two fragments of a wrapper that describe its entries map. Compiling them
// individually is what lets an entry (and an entry key) be validated on its
// own, so a problem is reported against the entry rather than the whole pack -
// a propertyNames violation locates itself at the object it was found in, which
// for a whole-pack validation would be "somewhere in these 1,000 entries".
const (
	entryFragment = "#/properties/entries/additionalProperties"
	keyFragment   = "#/properties/entries/propertyNames"
)

// schemaSet holds every compiled schema one load needs.
type schemaSet struct {
	// kinds holds the per-record schemas, keyed by entity kind.
	kinds map[model.Kind]*jsonschema.Schema
	// entries holds the shape of one pack entry, keyed by family.
	entries map[pack.Family]*jsonschema.Schema
	// keys holds the shape of one pack entry's key, keyed by family.
	keys map[pack.Family]*jsonschema.Schema
}

// compileSchemas compiles the embedded JSON Schema files. It is the single
// place the authoritative schema/*.json files enter the validator, and it
// compiles ALL of them - a schema no tool loads is a contract free to drift.
func compileSchemas() (schemaSet, error) {
	c := jsonschema.NewCompiler()
	// Assert "format" rather than treating it as an annotation, which is
	// draft 2020-12's default. Without this a date's regex is the only gate and
	// "2026-02-30" validates: the pattern says the shape, the format says the
	// value is a real point in time.
	c.AssertFormat()

	files := []string{"common.schema.json"}
	for _, f := range entitySchemas {
		files = append(files, f)
	}
	for _, f := range wrapperSchemas {
		files = append(files, f)
	}
	for _, f := range files {
		data, err := meta.SchemaFS.ReadFile("schema/" + f)
		if err != nil {
			return schemaSet{}, fmt.Errorf("read embedded schema %s: %w", f, err)
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if err != nil {
			return schemaSet{}, fmt.Errorf("parse schema %s: %w", f, err)
		}
		if err := c.AddResource(schemaBase+f, doc); err != nil {
			return schemaSet{}, fmt.Errorf("add schema %s: %w", f, err)
		}
	}

	set := schemaSet{
		kinds:   make(map[model.Kind]*jsonschema.Schema, len(entitySchemas)),
		entries: make(map[pack.Family]*jsonschema.Schema, len(wrapperSchemas)),
		keys:    make(map[pack.Family]*jsonschema.Schema, len(wrapperSchemas)),
	}
	compile := func(ref string) (*jsonschema.Schema, error) {
		sch, err := c.Compile(schemaBase + ref)
		if err != nil {
			return nil, fmt.Errorf("compile schema %s: %w", ref, err)
		}
		return sch, nil
	}
	for kind, f := range entitySchemas {
		sch, err := compile(f)
		if err != nil {
			return schemaSet{}, err
		}
		set.kinds[kind] = sch
	}
	for family, f := range wrapperSchemas {
		entry, err := compile(f + entryFragment)
		if err != nil {
			return schemaSet{}, err
		}
		key, err := compile(f + keyFragment)
		if err != nil {
			return schemaSet{}, err
		}
		set.entries[family], set.keys[family] = entry, key
	}
	return set, nil
}

// violation is one schema failure kept apart from its rendering: Loc is the
// JSON-pointer instance location inside the validated value ("" is the value
// itself), Msg the reason. The pack walker needs the two separately - a
// violation inside a composite's recordings map is re-reported against that
// recording, with its location rebased - so validate() is a thin renderer over
// violations().
type violation struct {
	Loc string
	Msg string
	// noLoc marks a failure that describes the whole validation rather than a
	// place inside it, so it renders without a location.
	noLoc bool
}

// String renders the violation the way a problem message reads.
func (v violation) String() string {
	switch {
	case v.noLoc:
		return v.Msg
	case v.Loc == "":
		return "(root): " + v.Msg
	default:
		return v.Loc + ": " + v.Msg
	}
}

// rebase re-points a violation that landed inside a sub-value at that
// sub-value: the pointer prefix is dropped, so the location reads relative to
// the thing the problem is now reported against. ok is false when the violation
// is not inside prefix.
func (v violation) rebase(prefix string) (violation, bool) {
	if v.noLoc || !strings.HasPrefix(v.Loc, prefix) {
		return v, false
	}
	rest := v.Loc[len(prefix):]
	if rest != "" && !strings.HasPrefix(rest, "/") {
		return v, false // prefix landed mid-token, e.g. "/recordings-extra"
	}
	return violation{Loc: rest, Msg: v.Msg}, true
}

// entryViolations checks one pack entry against its family's entry shape, taken
// from the family wrapper schema.
func (s schemaSet) entryViolations(family pack.Family, inst any) []violation {
	return validateWith(s.entries[family], fmt.Sprintf("no pack schema for family %q", family), inst)
}

// keyViolations checks one pack entry's key against its family's key shape.
func (s schemaSet) keyViolations(family pack.Family, key string) []violation {
	return validateWith(s.keys[family], fmt.Sprintf("no pack schema for family %q", family), key)
}

// violations checks inst against the per-record schema for kind.
func (s schemaSet) violations(kind model.Kind, inst any) []violation {
	return validateWith(s.kinds[kind], fmt.Sprintf("no schema for kind %q", kind), inst)
}

// validate checks inst against the per-record schema for kind, returning
// human-readable messages (one per underlying violation). An empty slice means
// it validated.
func (s schemaSet) validate(kind model.Kind, inst any) []string {
	return renderViolations(s.violations(kind, inst))
}

func renderViolations(vs []violation) []string {
	msgs := make([]string, 0, len(vs))
	for _, v := range vs {
		msgs = append(msgs, v.String())
	}
	return msgs
}

// validateWith runs one compiled schema, flattening the failure into
// violations. missing is the message for a schema that was never compiled,
// which is a programming error rather than a data one.
func validateWith(sch *jsonschema.Schema, missing string, inst any) []violation {
	if sch == nil {
		return []violation{{Msg: missing, noLoc: true}}
	}
	err := sch.Validate(inst)
	if err == nil {
		return nil
	}
	verr, ok := err.(*jsonschema.ValidationError)
	if !ok {
		return []violation{{Msg: collapse(err.Error()), noLoc: true}}
	}
	var out []violation
	collectLeaves(verr.DetailedOutput(), &out)
	if len(out) == 0 {
		out = append(out, violation{Msg: collapse(verr.Error()), noLoc: true})
	}
	return out
}

// collectLeaves walks an output tree, emitting one violation per leaf error.
//
// It walks the DETAILED output, not the basic one: the basic output collapses
// every failure behind a $ref to the bare string "validation failed", which
// costs the reason for anything a schema reached through a $ref - a nested
// recording, an added_at date, a license enum. The detailed tree keeps the
// reason at the same instance location, so a message is the same whether the
// value was validated directly or through two levels of $ref, which is what
// makes the legacy and pack walkers report a defect identically.
func collectLeaves(u *jsonschema.OutputUnit, out *[]violation) {
	if u == nil {
		return
	}
	if u.Error != nil && len(u.Errors) == 0 {
		*out = append(*out, violation{Loc: u.InstanceLocation, Msg: collapse(u.Error.String())})
	}
	for i := range u.Errors {
		collectLeaves(&u.Errors[i], out)
	}
}

// collapse flattens whitespace so a message is a single line.
func collapse(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
