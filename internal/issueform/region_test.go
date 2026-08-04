package issueform

import (
	"encoding/json"
	"reflect"
	"testing"

	meta "github.com/kodestar/audiosilo-meta"
)

// TestRegionAliasesTargetTheSchemaEnum is the drift guard between the alias
// table a submitter's typed region is resolved through and the one region
// vocabulary (common.schema.json #/$defs/region).
//
// Only the VALUE range is checked, in both directions. The keys are meant to be
// wider than the enum ("usa", "ger", "bra"), but a value outside it would
// compose a region metacheck rejects, and an enum member no value reaches is a
// marketplace no submitter can name however they spell it.
func TestRegionAliasesTargetTheSchemaEnum(t *testing.T) {
	want := schemaDefEnum(t, "region")
	got := make(map[string]bool, len(want))
	for _, region := range regionAliases {
		got[region] = true
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("regionAliases values = %v, schema $defs/region = %v", got, want)
	}
}

// schemaDefEnum reads a controlled vocabulary straight out of the embedded
// schema's $defs, so a drift guard compares its table against the CONTRACT
// rather than a copy of it.
func schemaDefEnum(t *testing.T, def string) map[string]bool {
	t.Helper()
	raw, err := meta.SchemaFS.ReadFile("schema/common.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Defs map[string]struct {
			Enum []string `json:"enum"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	enum := doc.Defs[def].Enum
	if len(enum) == 0 {
		t.Fatalf("schema $defs/%s enum is empty; the drift guard would pass vacuously", def)
	}
	set := make(map[string]bool, len(enum))
	for _, v := range enum {
		set[v] = true
	}
	return set
}
