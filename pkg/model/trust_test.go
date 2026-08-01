package model

import (
	"encoding/json"
	"os"
	"testing"
)

func TestTierOfSource(t *testing.T) {
	cases := []struct {
		sourceType string
		want       SourceTier
	}{
		{"libex-import", TierBulkMirror},
		{"user", TierUserLibrary},
		{"openaudible-import", TierUserLibrary},
		{"libation-import", TierUserLibrary},
		{"audiosilo-books-import", TierUserLibrary},
		{"openlibrary", TierReference},
		{"wikidata", TierReference},
		{"audible-lookup", TierReference},
		{"community", TierReference},
		// Not in the schema enum at all: unknown must never rank as the
		// overwritable bulk-mirror tier.
		{"some-future-source", TierReference},
		{"", TierReference},
	}
	for _, c := range cases {
		if got := TierOfSource(c.sourceType); got != c.want {
			t.Errorf("TierOfSource(%q) = %d, want %d", c.sourceType, got, c.want)
		}
	}
}

// TestBulkMirrorOnly covers the three record shapes the overwrite rule branches
// on: bulk-mirror-only, mixed, and user-only.
func TestBulkMirrorOnly(t *testing.T) {
	cases := []struct {
		name    string
		sources []Source
		want    bool
	}{
		{"libex only", []Source{{Type: "libex-import", Ref: "B0AAA"}}, true},
		{
			"libex only, several entries",
			[]Source{{Type: "libex-import", Ref: "B0AAA"}, {Type: "libex-import", Ref: "B0BBB"}},
			true,
		},
		{
			"mixed libex and user",
			[]Source{{Type: "libex-import", Ref: "B0AAA"}, {Type: "openaudible-import", Ref: "B0AAA"}},
			false,
		},
		{
			"mixed libex and reference",
			[]Source{{Type: "libex-import", Ref: "B0AAA"}, {Type: "wikidata", Ref: "Q1"}},
			false,
		},
		{"user only", []Source{{Type: "user", Ref: "the publisher's page"}}, false},
		{"reference only", []Source{{Type: "openlibrary", Ref: "OL1W"}}, false},
		{"no sources at all", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := BulkMirrorOnly(c.sources); got != c.want {
				t.Errorf("BulkMirrorOnly = %v, want %v", got, c.want)
			}
			types := make([]string, 0, len(c.sources))
			for _, s := range c.sources {
				types = append(types, s.Type)
			}
			if got := BulkMirrorOnlyTypes(types); got != c.want {
				t.Errorf("BulkMirrorOnlyTypes = %v, want %v", got, c.want)
			}
		})
	}
}

// TestSourceTiersCoverSchemaEnum keeps the trust tables and the schema's
// source-type enum in step: a type added to the schema without a decision here
// would silently fall into TierReference, which is safe but is a decision
// someone should make on purpose.
func TestSourceTiersCoverSchemaEnum(t *testing.T) {
	raw, err := os.ReadFile("../../schema/common.schema.json")
	if err != nil {
		t.Fatalf("read common.schema.json: %v", err)
	}
	var doc struct {
		Defs struct {
			Source struct {
				Properties struct {
					Type struct {
						Enum []string `json:"enum"`
					} `json:"type"`
				} `json:"properties"`
			} `json:"source"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse common.schema.json: %v", err)
	}
	enum := doc.Defs.Source.Properties.Type.Enum
	if len(enum) == 0 {
		t.Fatal("no source-type enum found in the schema")
	}
	// The reference tier is the default, so "classified" means the type is
	// named in one of the two policy tables OR is a known reference source.
	reference := map[string]bool{
		"audible-lookup": true, "openlibrary": true, "wikidata": true,
		"inventaire": true, "community": true,
	}
	for _, tp := range enum {
		if userLibrarySources[tp] || bulkMirrorSources[tp] || reference[tp] {
			continue
		}
		t.Errorf("schema source type %q is not classified in pkg/model/trust.go - "+
			"add it to userLibrarySources, bulkMirrorSources, or this test's reference set", tp)
	}
}
