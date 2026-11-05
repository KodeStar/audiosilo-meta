package model

import (
	"encoding/json"
	"os"
	"slices"
	"testing"
)

// TestPositionScaleUnitsAreTheSchemaEnum is the drift guard for the unit vocabulary,
// the way TestSeriesOrderingsCoverSchemaEnum guards the orderings: same values, same
// order.
func TestPositionScaleUnitsAreTheSchemaEnum(t *testing.T) {
	raw, err := os.ReadFile("../../schema/position-scale.schema.json")
	if err != nil {
		t.Fatalf("read position-scale.schema.json: %v", err)
	}
	var doc struct {
		Properties struct {
			Unit struct {
				Enum []string `json:"enum"`
			} `json:"unit"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse position-scale.schema.json: %v", err)
	}
	if got := PositionScaleUnits(); len(got) == 0 || !slices.Equal(got, doc.Properties.Unit.Enum) {
		t.Errorf("PositionScaleUnits() = %v, want the schema's %v", got, doc.Properties.Unit.Enum)
	}
}
