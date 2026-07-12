package extract

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"testing"

	meta "github.com/kodestar/audiosilo-meta"
)

// factualCaps lists capped schema fields that are deliberately NOT ngram-
// scanned: defensive length caps on FACTUAL fields (identifiers, names, refs),
// not own-words prose. Empty today - every capped string in the sidecar
// schemas is expressive. Add a field here only after confirming it carries no
// own-words text.
var factualCaps = map[string]bool{}

// TestCheckedFieldsMatchSchemas is the drift guard for the ngram check: it
// walks the embedded sidecar schemas (the same schema/*.json the rest of the
// tooling validates against, via meta.SchemaFS) for string fields carrying a
// maxLength cap and asserts that set equals the set collectExprs scans
// (expressiveFields, the real source of truth - not a copy) plus the explicit
// factualCaps allowlist above.
//
// The load-bearing assumption, verified against both schemas: within
// characters.schema.json and recaps.schema.json, an INLINE string property with
// maxLength is exactly an own-words expressive field (description, text,
// in_short, ending - each capped "for the copyright reference-guide tier").
// Identifier caps (like the slug's maxLength) live behind $refs into
// common.schema.json, which this walk deliberately does not resolve: refs in
// the sidecar schemas point at structural defs (slug, position, license,
// sources), never at own-words prose.
//
// If this test fails on the discovered side, a new capped field was added to a
// schema and the ngram check would silently never scan it - the exact failure
// the tool exists to prevent.
func TestCheckedFieldsMatchSchemas(t *testing.T) {
	for _, kind := range sidecarKinds() {
		fields := expressiveFields[kind]
		file := kind + ".schema.json"

		data, err := meta.SchemaFS.ReadFile("schema/" + file)
		if err != nil {
			t.Fatalf("read embedded schema %s: %v", file, err)
		}
		var doc map[string]any
		if err := json.Unmarshal(data, &doc); err != nil {
			t.Fatalf("parse schema %s: %v", file, err)
		}

		discovered := map[string]bool{}
		cappedStringFields(t, doc, "", discovered)

		// The set collectExprs scans for this kind, in the same path notation.
		checked := map[string]bool{fmt.Sprintf("%s[].%s", kind, fields.itemField): true}
		for _, tl := range fields.topLevel {
			checked[tl] = true
		}

		for f := range discovered {
			if factualCaps[f] {
				continue
			}
			if !checked[f] {
				t.Errorf("%s: %s: new capped own-words field in the schema - add it to expressiveFields (own-words prose, ngram.go) OR to factualCaps (defensive cap on a factual field, drift_test.go)",
					file, f)
			}
		}
		for f := range checked {
			if !discovered[f] {
				t.Errorf("%s: ngram checks %s but the schema has no such capped string field - stale entry in expressiveFields?",
					file, f)
			}
		}
		if t.Failed() {
			t.Logf("%s discovered=%v checked=%v", file, sortedKeys(discovered), sortedKeys(checked))
		}
	}
}

// TestCappedStringFieldsDescendsApplicators pins the walker's coverage of the
// structural applicator keywords: a capped string hidden under allOf/if/then
// or under anyOf inside array items must still be discovered (a base schema
// can declare a field uncapped and a conditional branch apply the cap).
func TestCappedStringFieldsDescendsApplicators(t *testing.T) {
	const frag = `{
	  "type": "object",
	  "allOf": [
	    {
	      "if": {"properties": {"kind": {"const": "a"}}},
	      "then": {"properties": {"note": {"type": "string", "maxLength": 100}}}
	    }
	  ],
	  "properties": {
	    "entries": {
	      "type": "array",
	      "items": {
	        "anyOf": [
	          {"properties": {"blurb": {"type": "string", "maxLength": 50}}}
	        ]
	      }
	    }
	  }
	}`
	var doc map[string]any
	if err := json.Unmarshal([]byte(frag), &doc); err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	cappedStringFields(t, doc, "", out)
	want := []string{"entries[].blurb", "note"}
	if got := sortedKeys(out); !slices.Equal(got, want) {
		t.Errorf("discovered %v, want %v", got, want)
	}
}

// Subschema-bearing keywords the walk traverses, and recognized ones it does
// not. A keyword in the second set appearing in a walked schema fails the test
// loudly (fail closed): a capped string could hide inside it, so a human must
// extend the walk and re-confirm the inventory rather than let it pass
// silently.
var untraversedSubschemaKeywords = []string{
	"$defs", "definitions", "contains", "contentSchema", "dependencies",
	"dependentSchemas", "patternProperties", "propertyNames",
	"unevaluatedItems", "unevaluatedProperties",
}

// cappedStringFields walks a parsed JSON Schema document, collecting the paths
// of inline string-typed schemas that carry maxLength. Array items add "[]" to
// the path; the in-place applicators (allOf/anyOf/oneOf/if/then/else/not)
// constrain the same location, so they keep the prefix; $refs are not resolved
// (see TestCheckedFieldsMatchSchemas). It fails the test on any recognized
// subschema-bearing keyword it does not traverse.
func cappedStringFields(t *testing.T, node map[string]any, prefix string, out map[string]bool) {
	t.Helper()
	for _, kw := range untraversedSubschemaKeywords {
		if _, present := node[kw]; present {
			t.Fatalf("schema walk: node at %q carries %q, which cappedStringFields does not traverse - extend the walk and re-confirm the capped-field inventory", prefix, kw)
		}
	}

	// A capped string schema at this location. A missing "type" is treated as
	// a string (fail closed - maxLength only ever applies to strings).
	if _, capped := node["maxLength"]; capped {
		if typ, hasType := node["type"].(string); !hasType || typ == "string" {
			out[prefix] = true
		}
	}

	if props, ok := node["properties"].(map[string]any); ok {
		for name, raw := range props {
			if child, ok := raw.(map[string]any); ok {
				path := name
				if prefix != "" {
					path = prefix + "." + name
				}
				cappedStringFields(t, child, path, out)
			}
		}
	}
	if items, ok := node["items"].(map[string]any); ok {
		cappedStringFields(t, items, prefix+"[]", out)
	}
	if pi, ok := node["prefixItems"].([]any); ok {
		for _, raw := range pi {
			if sub, ok := raw.(map[string]any); ok {
				cappedStringFields(t, sub, prefix+"[]", out)
			}
		}
	}
	// additionalProperties in schema form (the common `false` bool is ignored).
	if ap, ok := node["additionalProperties"].(map[string]any); ok {
		cappedStringFields(t, ap, prefix+".*", out)
	}
	for _, kw := range []string{"allOf", "anyOf", "oneOf"} {
		if arr, ok := node[kw].([]any); ok {
			for _, raw := range arr {
				if sub, ok := raw.(map[string]any); ok {
					cappedStringFields(t, sub, prefix, out)
				}
			}
		}
	}
	for _, kw := range []string{"if", "then", "else", "not"} {
		if sub, ok := node[kw].(map[string]any); ok {
			cappedStringFields(t, sub, prefix, out)
		}
	}
}

func sortedKeys(m map[string]bool) []string {
	return slices.Sorted(maps.Keys(m))
}
