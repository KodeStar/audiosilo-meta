package issueform

import (
	"reflect"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/testpack"
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
	want := testpack.SchemaDefEnum(t, "region")
	got := make(map[string]bool, len(want))
	for _, region := range regionAliases {
		got[region] = true
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("regionAliases values = %v, schema $defs/region = %v", got, want)
	}
}
