package check

import (
	"slices"
	"strings"
	"testing"
)

// The Languages Phase 2 contract fields: translation_of on a work and a series,
// and ordering / ordering_of on a series. These tests pin the SCHEMA half (the
// shapes and the one structural pairing); the cross-record rules over them are
// pkg/check's own and carry their own fixtures.

const validSeries = `{
	"id": "dune-chronicles", "name": "Dune Chronicles",
	"works": [{"work": "dune", "position": "1"}],
	"license": "CC0-1.0", "sources": [{"type": "user"}]
}`

// withMembers splices extra top-level members into a one-object JSON document.
func withMembers(doc, members string) string {
	return strings.TrimSuffix(strings.TrimSpace(doc), "}") + ", " + members + "}"
}

func TestLanguageFieldsValid(t *testing.T) {
	set := compileAll(t)
	for name, c := range map[string]struct{ schema, raw string }{
		"work translation_of": {"work.schema.json",
			withMembers(validWork, `"translation_of": ["der-wustenplanet"]`)},
		"work translation_of of two originals": {"work.schema.json",
			withMembers(validWork, `"translation_of": ["dune", "dune-messiah"]`)},
		"series translation_of": {"series.schema.json",
			withMembers(validSeries, `"translation_of": ["der-wustenplanet-zyklus"]`)},
		"series ordering alone": {"series.schema.json",
			withMembers(validSeries, `"ordering": "publication"`)},
		"series variant": {"series.schema.json",
			withMembers(validSeries, `"ordering": "chronological", "ordering_of": "dune-publication"`)},
		"series recommended ordering": {"series.schema.json",
			withMembers(validSeries, `"ordering": "recommended", "ordering_of": "dune-publication"`)},
		"series every new field": {"series.schema.json",
			withMembers(validSeries, `"translation_of": ["a", "b"], "ordering": "chronological", "ordering_of": "c"`)},
	} {
		if err := validateJSON(t, set[c.schema], c.raw); err != nil {
			t.Errorf("%s: %s rejected it: %v", name, c.schema, err)
		}
	}

	// The fields are reached through the pack wrappers, which is what a load
	// actually validates an entry against.
	for name, c := range map[string]struct{ schema, raw string }{
		"works pack": {"pack-works.schema.json",
			`{"entries": {"dune": ` + withMembers(validWork, `"translation_of": ["der-wustenplanet"]`) + `}}`},
		"series pack": {"pack-series.schema.json",
			`{"entries": {"dune-chronicles": ` + withMembers(validSeries,
				`"translation_of": ["x"], "ordering": "chronological", "ordering_of": "y"`) + `}}`},
	} {
		if err := validateJSON(t, set[c.schema], c.raw); err != nil {
			t.Errorf("%s: %s rejected it: %v", name, c.schema, err)
		}
	}
}

func TestLanguageFieldsInvalid(t *testing.T) {
	set := compileAll(t)
	for name, c := range map[string]struct{ schema, raw string }{
		"work empty translation_of": {"work.schema.json",
			withMembers(validWork, `"translation_of": []`)},
		"series empty translation_of": {"series.schema.json",
			withMembers(validSeries, `"translation_of": []`)},
		"work translation_of repeating a slug": {"work.schema.json",
			withMembers(validWork, `"translation_of": ["dune", "dune"]`)},
		"work translation_of a non-slug": {"work.schema.json",
			withMembers(validWork, `"translation_of": ["Der Wüstenplanet"]`)},
		"work translation_of as a string": {"work.schema.json",
			withMembers(validWork, `"translation_of": "der-wustenplanet"`)},
		// A variant must say what kind of order it is (dependentRequired).
		"series ordering_of without ordering": {"series.schema.json",
			withMembers(validSeries, `"ordering_of": "dune-publication"`)},
		"series ordering outside the enum": {"series.schema.json",
			withMembers(validSeries, `"ordering": "authors-preferred"`)},
		"series ordering in another case": {"series.schema.json",
			withMembers(validSeries, `"ordering": "Chronological"`)},
		"series ordering_of a non-slug": {"series.schema.json",
			withMembers(validSeries, `"ordering": "chronological", "ordering_of": "Dune Publication"`)},
		// ordering and ordering_of are series-level: a work never carries them.
		"work ordering": {"work.schema.json",
			withMembers(validWork, `"ordering": "publication"`)},
		"series pack with a bad ordering": {"pack-series.schema.json",
			`{"entries": {"dune-chronicles": ` + withMembers(validSeries, `"ordering_of": "y"`) + `}}`},
	} {
		if err := validateJSON(t, set[c.schema], c.raw); err == nil {
			t.Errorf("%s: %s accepted it", name, c.schema)
		}
	}
}

// TestLanguageFieldsReachTheCatalog loads a tree whose records carry every new
// field and checks each decodes onto its model struct. The fixture is shaped
// to satisfy every cross-record rule over the fields as well (a live target in
// another language, no chain, sorted sets, one hop, distinct orderings), so it
// stays the passing fixture once those rules land.
func TestLanguageFieldsReachTheCatalog(t *testing.T) {
	dir := t.TempDir()
	files := baseValid()
	files["works/bo/book-un/work.json"] = `{"authors":["author-one"],"id":"book-un","language":"fr","license":"CC0-1.0","sources":[{"type":"user"}],"title":"Livre Un","translation_of":["book-one"]}`
	files["works/bo/book-un/recordings/rec-un.json"] = `{"id":"rec-un","language":"fr","license":"CC0-1.0","narrators":["narrator-one"],"sources":[{"type":"user"}],"work":"book-un"}`
	files["series/se/series-one.json"] = `{"id":"series-one","license":"CC0-1.0","name":"Series One","ordering":"publication","sources":[{"type":"user"}],"works":[{"position":"1","work":"book-one"}]}`
	files["series/se/series-one-chronological.json"] = `{"id":"series-one-chronological","license":"CC0-1.0","name":"Series One (Chronological Order)","ordering":"chronological","ordering_of":"series-one","sources":[{"type":"user"}],"works":[{"position":"1","work":"book-one"}]}`
	files["series/se/serie-un.json"] = `{"id":"serie-un","license":"CC0-1.0","name":"Serie Un","sources":[{"type":"user"}],"translation_of":["series-one"],"works":[{"position":"1","work":"book-un"}]}`
	writeEntities(t, dir, files)
	res := Load(dir)
	if !res.OK() {
		t.Fatalf("a tree carrying the language fields should validate, got: %v", res.Problems)
	}
	for _, w := range res.Catalog.Works {
		want := []string(nil)
		if w.ID == "book-un" {
			want = []string{"book-one"}
		}
		if !slices.Equal(w.TranslationOf, want) {
			t.Errorf("work %s translation_of = %v, want %v", w.ID, w.TranslationOf, want)
		}
	}
	got := map[string]string{}
	for _, s := range res.Catalog.Series {
		got[s.ID] = strings.Join(s.TranslationOf, ",") + "|" + s.Ordering + "|" + s.OrderingOf
	}
	for id, want := range map[string]string{
		"series-one":               "|publication|",
		"series-one-chronological": "|chronological|series-one",
		"serie-un":                 "series-one||",
	} {
		if got[id] != want {
			t.Errorf("series %s decoded as %q, want %q", id, got[id], want)
		}
	}
}
