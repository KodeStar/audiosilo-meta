package importer

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	meta "github.com/kodestar/audiosilo-meta"
)

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

// TestAudibleGenreTable is the drift guard between the embedded mapping table and
// the schema's genre enum: the table parses, and every value it can produce is a
// member of the controlled vocabulary. A schema rename that misses the table
// would otherwise only surface as a failed post-import validation.
func TestAudibleGenreTable(t *testing.T) {
	table, err := loadGenreTable()
	if err != nil {
		t.Fatalf("embedded audiblegenres.json does not parse: %v", err)
	}

	// Size floors, so a regeneration that truncates the table (or writes an
	// empty half) fails here rather than silently importing books with no
	// genres. They are floors, not exact counts - the table is expected to grow.
	if len(table.ByName) < 1000 {
		t.Errorf("by_name has %d entries, want at least 1000", len(table.ByName))
	}
	if len(table.ByASIN) < 2000 {
		t.Errorf("by_asin has %d entries, want at least 2000", len(table.ByASIN))
	}

	enum := schemaDefEnum(t, "genre")
	for key, value := range table.ByName {
		if !enum[value] {
			t.Errorf("by_name[%q] = %q is not in the schema genre enum", key, value)
		}
		if key != strings.ToLower(strings.TrimSpace(key)) {
			t.Errorf("by_name key %q must be lowercased and trimmed (lookup normalizes that way)", key)
		}
	}
	for key, value := range table.ByASIN {
		if !enum[value] {
			t.Errorf("by_asin[%q] = %q is not in the schema genre enum", key, value)
		}
		if key != strings.TrimSpace(key) {
			t.Errorf("by_asin key %q must be trimmed (lookup normalizes that way)", key)
		}
	}

	// The accessor used by the pipeline returns the same table (and does not panic).
	if len(audibleGenreTable().ByName) != len(table.ByName) {
		t.Error("audibleGenreTable() disagrees with loadGenreTable()")
	}
}

// TestAudibleGenreTableAnchors pins a sample of the embedded table end to end
// (through lookup, so normalization and the by-node precedence are exercised).
// The drift guard above only proves every value is SOME vocabulary member; these
// anchors prove the table still says what it is supposed to say - including the
// localized names that let a German or Spanish row map without a node id, and
// the hierarchy-disambiguated nodes whose display NAME is ambiguous ("Military"
// is military-history under History and military-science-fiction under Science
// Fiction, so those two can only resolve by node).
func TestAudibleGenreTableAnchors(t *testing.T) {
	byName := map[string]string{
		// English.
		"science fiction":       "science-fiction",
		"epic fantasy":          "epic-fantasy",
		"space opera":           "space-opera",
		"historical fiction":    "historical-fiction",
		"true crime":            "true-crime",
		"biographies & memoirs": "biography-memoir",
		"business & careers":    "business",
		"fantasy":               "fantasy",
		"romance":               "romance",
		"history":               "history",
		// German.
		"biografien & erinnerungen": "biography-memoir",
		"geschichte":                "history",
		"krimis":                    "mystery",
		"wirtschaft":                "business",
		"wissenschaft":              "science",
		// Spanish.
		"biografías y memorias": "biography-memoir",
		"ciencia ficción":       "science-fiction",
		"fantasía":              "fantasy",
		"historia":              "history",
		"misterio y suspense":   "mystery",
		"romántica":             "romance",
	}
	table := audibleGenreTable()
	for name, want := range byName {
		got, ok := table.lookup(genreClaim{name: name})
		if !ok || got != want {
			t.Errorf("lookup(name %q) = %q,%v; want %q,true", name, got, ok, want)
		}
	}

	byNode := map[string]string{
		"18580641011": "military-science-fiction",
		"18573647011": "military-history",
		"16206689031": "history",
		"16209737031": "fantasy",
		"16209735031": "romance",
		"16215169031": "science-fiction",
		"16209803031": "horror",
		"16206636031": "biography-memoir",
	}
	for node, want := range byNode {
		// A deliberately wrong display name proves the node id wins.
		got, ok := table.lookup(genreClaim{node: node, name: "Totally Made Up Category"})
		if !ok || got != want {
			t.Errorf("lookup(node %q) = %q,%v; want %q,true", node, got, ok, want)
		}
	}
}

func TestGenreTableLookupPrecedence(t *testing.T) {
	table := genreTable{
		ByASIN: map[string]string{"18574784011": "epic-fantasy"},
		ByName: map[string]string{"adventure": "action-adventure", "epic fantasy": "fantasy"},
	}
	cases := []struct {
		name   string
		claim  genreClaim
		want   string
		wantOK bool
	}{
		{"node wins over name", genreClaim{node: "18574784011", name: "Epic Fantasy"}, "epic-fantasy", true},
		{"name when the node is unknown", genreClaim{node: "99999999999", name: "Epic Fantasy"}, "fantasy", true},
		{"name only", genreClaim{name: "  ADVENTURE "}, "action-adventure", true},
		{"unmapped", genreClaim{name: "Totally Made Up Category"}, "", false},
		{"empty", genreClaim{}, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := table.lookup(tc.claim)
			if got != tc.want || ok != tc.wantOK {
				t.Errorf("lookup(%+v) = %q,%v; want %q,%v", tc.claim, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

// TestGenreTableNameMemo pins the memo's contract: a hit and a MISS are both
// remembered, and both keep answering the same way (a memoized miss must never
// read as a mapped genre).
func TestGenreTableNameMemo(t *testing.T) {
	table := genreTable{
		ByName: map[string]string{"epic fantasy": "epic-fantasy"},
		memo:   map[string]string{},
	}
	for range 2 {
		if got, ok := table.lookup(genreClaim{name: " Epic Fantasy "}); got != "epic-fantasy" || !ok {
			t.Errorf("lookup = %q,%v; want epic-fantasy,true", got, ok)
		}
		if got, ok := table.lookup(genreClaim{name: "Nope"}); got != "" || ok {
			t.Errorf("lookup = %q,%v; want an unmapped miss", got, ok)
		}
	}
	if len(table.memo) != 2 {
		t.Errorf("memo = %v, want one entry per distinct raw name", table.memo)
	}
}

func TestGenreTableMapGenres(t *testing.T) {
	table := genreTable{
		ByName: map[string]string{"epic fantasy": "epic-fantasy", "adventure": "action-adventure", "sagas": "epic-fantasy"},
	}
	unmapped := map[string]bool{}
	got := table.mapGenres([]genreClaim{
		{name: "Epic Fantasy"},
		{name: "Adventure"},
		{name: "Sagas"},       // maps to a slug already collected: deduplicated
		{name: "Nonexistent"}, // unmapped: dropped and reported
		{node: "12345678901"}, // no name and no mapping: reported under its node id
	}, unmapped)

	if !reflect.DeepEqual(got, []string{"action-adventure", "epic-fantasy"}) {
		t.Errorf("mapGenres = %v, want sorted deduplicated [action-adventure epic-fantasy]", got)
	}
	if !unmapped["Nonexistent"] || !unmapped["12345678901"] || len(unmapped) != 2 {
		t.Errorf("unmapped = %v", unmapped)
	}
	// No claims at all leaves the field absent (genre_list has minItems 1).
	if got := table.mapGenres(nil, unmapped); len(got) != 0 {
		t.Errorf("mapGenres(nil) = %v, want empty", got)
	}
}
