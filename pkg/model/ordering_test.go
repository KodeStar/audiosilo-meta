package model

import (
	"encoding/json"
	"os"
	"slices"
	"testing"
)

// seriesOrderingConstants is every Ordering* constant this package declares,
// listed only for the drift guard, which compares it against SeriesOrderings()
// as well as against the schema - so a constant left out of the list every
// consumer reads fails here rather than being unreachable through a door.
var seriesOrderingConstants = []string{
	OrderingPublication,
	OrderingChronological,
	OrderingRecommended,
}

// TestSeriesOrderingsCoverSchemaEnum is the drift guard for the series
// ordering vocabulary, the way TestPersonKindsCoverSchemaEnum guards the
// person kinds: SeriesOrderings() must BE the schema enum, same values, same
// order, and every value must be a declared constant.
func TestSeriesOrderingsCoverSchemaEnum(t *testing.T) {
	raw, err := os.ReadFile("../../schema/common.schema.json")
	if err != nil {
		t.Fatalf("read common.schema.json: %v", err)
	}
	var doc struct {
		Defs struct {
			SeriesOrdering struct {
				Enum []string `json:"enum"`
			} `json:"series_ordering"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse common.schema.json: %v", err)
	}
	enum := doc.Defs.SeriesOrdering.Enum
	if len(enum) == 0 {
		t.Fatal("common.schema.json has no $defs/series_ordering enum")
	}
	if got := SeriesOrderings(); !slices.Equal(got, enum) {
		t.Errorf("SeriesOrderings() = %v, want the schema's %v", got, enum)
	}
	for _, v := range enum {
		if !slices.Contains(seriesOrderingConstants, v) {
			t.Errorf("schema series ordering %q has no Ordering* constant in pkg/model", v)
		}
	}
	for _, c := range seriesOrderingConstants {
		if !slices.Contains(enum, c) {
			t.Errorf("Ordering constant %q is not in the schema's series_ordering enum", c)
		}
	}
}
