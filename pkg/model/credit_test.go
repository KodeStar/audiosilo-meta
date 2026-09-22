package model

import (
	"encoding/json"
	"os"
	"testing"
)

// creditRoleConstants is every Role* constant this package declares, in enum
// order. It exists only so the drift guard below has something to compare the
// schema against; the constants themselves are what callers use.
var creditRoleConstants = []string{
	RoleAdaptation,
	RoleAfterword,
	RoleContributor,
	RoleEditor,
	RoleForeword,
	RoleIllustrator,
	RoleIntroduction,
	RolePreface,
	RoleTranslator,
}

// TestCreditRolesCoverSchemaEnum keeps the Role* constants and the schema's
// credit_role enum in step, in BOTH directions: a role added to the enum with no
// constant is a role the importer's qualifier table can never emit, and a
// constant with no enum entry would emit data that fails schema validation on
// the very next metacheck. Either way the drift is a failing test here rather
// than a surprise in a 142k-record seed.
func TestCreditRolesCoverSchemaEnum(t *testing.T) {
	raw, err := os.ReadFile("../../schema/common.schema.json")
	if err != nil {
		t.Fatalf("read common.schema.json: %v", err)
	}
	var doc struct {
		Defs struct {
			CreditRole struct {
				Enum []string `json:"enum"`
			} `json:"credit_role"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse common.schema.json: %v", err)
	}
	enum := doc.Defs.CreditRole.Enum
	if len(enum) == 0 {
		t.Fatal("no credit_role enum found in the schema")
	}

	consts := make(map[string]bool, len(creditRoleConstants))
	for _, r := range creditRoleConstants {
		consts[r] = true
	}
	inEnum := make(map[string]bool, len(enum))
	for _, r := range enum {
		inEnum[r] = true
		if !consts[r] {
			t.Errorf("schema credit_role %q has no Role* constant in pkg/model", r)
		}
	}
	for _, r := range creditRoleConstants {
		if !inEnum[r] {
			t.Errorf("Role constant %q is not in the schema's credit_role enum", r)
		}
	}

	// The enum is stored sorted so the vocabulary reads as a list rather than an
	// accretion order, and so a new role lands in an obvious place.
	for i := 1; i < len(enum); i++ {
		if enum[i] <= enum[i-1] {
			t.Errorf("credit_role enum is not strictly ascending: %q follows %q", enum[i], enum[i-1])
		}
	}
}

// personKindConstants is every KindEntity* constant this package declares. Like
// creditRoleConstants it exists only for the drift guard, and it is compared
// against PersonKinds() as well as against the schema - so a constant added
// without being listed in PersonKinds() (the list every consumer reads) fails
// here rather than being silently unreachable through the issue form.
var personKindConstants = []string{
	KindEntityPerson,
	KindEntityGroup,
	KindEntityPublisher,
	KindEntitySynthetic,
}

// TestPersonKindsCoverSchemaEnum is the same drift guard for the person kind
// vocabulary.
func TestPersonKindsCoverSchemaEnum(t *testing.T) {
	raw, err := os.ReadFile("../../schema/person.schema.json")
	if err != nil {
		t.Fatalf("read person.schema.json: %v", err)
	}
	var doc struct {
		Properties struct {
			Kind struct {
				Enum []string `json:"enum"`
			} `json:"kind"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse person.schema.json: %v", err)
	}
	want := make(map[string]bool, len(personKindConstants))
	for _, k := range personKindConstants {
		want[k] = true
	}
	if len(doc.Properties.Kind.Enum) != len(want) {
		t.Fatalf("person kind enum = %v, want %d values", doc.Properties.Kind.Enum, len(want))
	}
	for _, k := range doc.Properties.Kind.Enum {
		if !want[k] {
			t.Errorf("schema person kind %q has no KindEntity* constant in pkg/model", k)
		}
	}

	// PersonKinds() is what every consumer reads, so it has to BE the enum -
	// same values, same order. A constant that is declared but left out of the
	// list is a kind the issue form would reject and the schema would accept.
	kinds := PersonKinds()
	if len(kinds) != len(doc.Properties.Kind.Enum) {
		t.Fatalf("PersonKinds() = %v, want the schema's %v", kinds, doc.Properties.Kind.Enum)
	}
	for i, k := range kinds {
		if k != doc.Properties.Kind.Enum[i] {
			t.Errorf("PersonKinds()[%d] = %q, want %q", i, k, doc.Properties.Kind.Enum[i])
		}
		if !want[k] {
			t.Errorf("PersonKinds() lists %q, which is no KindEntity* constant", k)
		}
	}
}
