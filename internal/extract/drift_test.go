package extract

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"testing"

	meta "github.com/kodestar/audiosilo-meta"
)

// TestCheckedFieldsMatchSchemas is the drift guard for the ngram check: it
// walks the embedded sidecar schemas (the same schema/*.json the rest of the
// tooling validates against, via meta.SchemaFS) for string fields carrying a
// maxLength cap and asserts that set equals the set collectExprs scans
// (expressiveFields, the real source of truth - not a copy).
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
// If this test fails on the discovered side, a new capped own-words field was
// added to a schema and the ngram check would silently never scan it - the
// exact failure the tool exists to prevent.
func TestCheckedFieldsMatchSchemas(t *testing.T) {
	schemaForKind := map[string]string{
		"characters": "characters.schema.json",
		"recaps":     "recaps.schema.json",
	}
	if len(schemaForKind) != len(expressiveFields) {
		t.Fatalf("expressiveFields covers %d sidecar kinds, test maps %d - keep them in step",
			len(expressiveFields), len(schemaForKind))
	}

	for kind, file := range schemaForKind {
		fields, ok := expressiveFields[kind]
		if !ok {
			t.Fatalf("expressiveFields has no entry for sidecar kind %q", kind)
		}

		data, err := meta.SchemaFS.ReadFile("schema/" + file)
		if err != nil {
			t.Fatalf("read embedded schema %s: %v", file, err)
		}
		var doc map[string]any
		if err := json.Unmarshal(data, &doc); err != nil {
			t.Fatalf("parse schema %s: %v", file, err)
		}

		discovered := map[string]bool{}
		cappedStringFields(doc, "", discovered)

		// The set collectExprs scans for this kind, in the same path notation.
		checked := map[string]bool{fmt.Sprintf("%s[].%s", kind, fields.itemField): true}
		for _, tl := range fields.topLevel {
			checked[tl] = true
		}

		for f := range discovered {
			if !checked[f] {
				t.Errorf("%s: %s: new capped own-words field in the schema - add it to the ngram check (expressiveFields in ngram.go)",
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

// cappedStringFields walks a parsed JSON Schema document, collecting the paths
// of inline string-typed properties that carry maxLength. Array items add "[]"
// to the path; $refs are not resolved (see the test comment).
func cappedStringFields(node map[string]any, prefix string, out map[string]bool) {
	if props, ok := node["properties"].(map[string]any); ok {
		for name, raw := range props {
			child, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			path := name
			if prefix != "" {
				path = prefix + "." + name
			}
			if child["type"] == "string" {
				if _, capped := child["maxLength"]; capped {
					out[path] = true
				}
				continue
			}
			cappedStringFields(child, path, out)
		}
	}
	if items, ok := node["items"].(map[string]any); ok {
		if items["type"] == "string" {
			if _, capped := items["maxLength"]; capped {
				out[prefix+"[]"] = true
			}
		} else {
			cappedStringFields(items, prefix+"[]", out)
		}
	}
}

func sortedKeys(m map[string]bool) []string {
	return slices.Sorted(maps.Keys(m))
}
