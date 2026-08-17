package testpack

import (
	"encoding/json"
	"testing"

	meta "github.com/kodestar/audiosilo-meta"
)

// SchemaDefEnum reads a controlled vocabulary straight out of the embedded
// schema's $defs, so a drift guard compares its table against the CONTRACT
// rather than a copy of it. It is the one implementation behind the region,
// genre and credit-role mirror guards (internal/importer, internal/issueform,
// internal/serve) - each package used to carry a byte-identical copy.
func SchemaDefEnum(t *testing.T, def string) map[string]bool {
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
