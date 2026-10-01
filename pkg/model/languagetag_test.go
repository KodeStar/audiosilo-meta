package model

import (
	"encoding/json"
	"testing"

	meta "github.com/kodestar/audiosilo-meta"
)

// TestLanguageTagPatternIsTheSchemas is the drift guard between ValidLanguageTag
// and the contract: its pattern must be common.schema.json's #/$defs/language
// pattern byte for byte, or a tag one door accepts is a tag metacheck rejects.
// The schema is reached from this test only (the module root embeds it and
// imports nothing of ours), so pkg/model itself stays a leaf.
func TestLanguageTagPatternIsTheSchemas(t *testing.T) {
	raw, err := meta.SchemaFS.ReadFile("schema/common.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Defs map[string]struct {
			Pattern string `json:"pattern"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	want := doc.Defs["language"].Pattern
	if want == "" {
		t.Fatal("schema $defs/language states no pattern; the drift guard would pass vacuously")
	}
	if got := languageTagRE.String(); got != want {
		t.Errorf("ValidLanguageTag's pattern = %q, the schema's = %q", got, want)
	}
}

func TestValidLanguageTag(t *testing.T) {
	for tag, want := range map[string]bool{
		"en": true, "fil": true, "pt-br": true, "zh-hant-tw": true, "de-at": true,
		"": false, "e": false, "english": false, "EN": false, "de-": false, "de_at": false, "pt-BR": false,
	} {
		if got := ValidLanguageTag(tag); got != want {
			t.Errorf("ValidLanguageTag(%q) = %v, want %v", tag, got, want)
		}
	}
}
