package check

import (
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// narrationCatalog is a catalogue of works each carrying one recording: id -> (language,
// narrators).
func narrationCatalog(people []*model.Person, recs map[string][2]any) *model.Catalog {
	cat := &model.Catalog{People: people}
	for id, spec := range recs {
		lang, narrators := spec[0].(string), spec[1].([]string)
		cat.Works = append(cat.Works, &model.Work{ID: id, Language: lang, Recordings: []*model.Recording{
			{ID: "r", Work: id, Language: lang, Narrators: narrators},
		}})
	}
	return cat
}

func TestNarrationProfileCountsTheOtherRecordingsOnly(t *testing.T) {
	prof := NewNarrationProfile(narrationCatalog(nil, map[string][2]any{
		"a": {"de", []string{"anna"}},
		"b": {"de-DE", []string{"anna"}},
		"c": {"de", []string{"anna"}},
		"d": {"en", []string{"anna"}}, // the work in question
	}))
	ev := prof.Of([]string{"anna"}, "d")
	if ev.Total != 3 || ev.Counts["de"] != 3 {
		t.Fatalf("evidence = %+v, want 3 de recordings (the work's own excluded)", ev)
	}
	if !ev.Contradicts("en") || ev.Contradicts("de") || ev.Contradicts("") {
		t.Errorf("Contradicts: en %v, de %v, unknown %v; want true, false, false",
			ev.Contradicts("en"), ev.Contradicts("de"), ev.Contradicts(""))
	}
	// Excluding nothing counts the questioned recording too: 3 de of 4 is under 80%.
	if d := prof.Of([]string{"anna"}, "").Dominant(); d != "" {
		t.Errorf("with the work's own recording counted, dominant = %q, want none (75%%)", d)
	}
}

// The floor and the share: one other recording is no evidence, and a narrator whose
// other recordings are split below 80% contradicts nothing.
func TestNarrationProfileNeedsTwoRecordingsAndAnEightyPercentShare(t *testing.T) {
	prof := NewNarrationProfile(narrationCatalog(nil, map[string][2]any{
		"a":  {"de", []string{"once"}},
		"x":  {"en", []string{"once", "split"}},
		"b1": {"de", []string{"split"}},
		"b2": {"de", []string{"split"}},
		"b3": {"de", []string{"split"}},
		"b4": {"fr", []string{"split"}},
		"b5": {"fr", []string{"split"}},
	}))
	if ev := prof.Of([]string{"once"}, "x"); ev.Total != 1 || ev.Contradicts("en") {
		t.Errorf("one other recording contradicted: %+v", ev)
	}
	if ev := prof.Of([]string{"split"}, "x"); ev.Contradicts("en") || ev.Dominant() != "" {
		t.Errorf("a 3/5 split named a language: %+v", ev)
	}
	// Exactly 80% is enough: 4 of 5.
	prof = NewNarrationProfile(narrationCatalog(nil, map[string][2]any{
		"b1": {"de", []string{"n"}}, "b2": {"de", []string{"n"}}, "b3": {"de", []string{"n"}},
		"b4": {"de", []string{"n"}}, "b5": {"fr", []string{"n"}}, "x": {"en", []string{"n"}},
	}))
	if d := prof.Of([]string{"n"}, "x").Dominant(); d != "de" {
		t.Errorf("4 of 5 = %q, want de", d)
	}
}

// A recording two of the narrators share is ONE recording, and a record that is not an
// individual (a group, the shared catch-all) is not counted at all.
func TestNarrationProfileDedupesSharedRecordingsAndSkipsNonIndividuals(t *testing.T) {
	people := []*model.Person{
		{ID: "full-cast", Name: "Full Cast", Kind: model.KindEntityGroup},
		{ID: "anna", Name: "Anna"},
		{ID: "ben", Name: "Ben", Kind: model.KindEntityPerson},
	}
	prof := NewNarrationProfile(narrationCatalog(people, map[string][2]any{
		"shared": {"de", []string{"anna", "ben"}},
		"cast1":  {"de", []string{"full-cast"}},
		"cast2":  {"de", []string{"full-cast"}},
		"blob1":  {"ko", []string{model.UnslugPersonID}},
		"blob2":  {"ko", []string{model.UnslugPersonID}},
		"x":      {"en", []string{"anna", "ben", "full-cast", model.UnslugPersonID}},
	}))
	ev := prof.Of([]string{"anna", "ben", "full-cast", model.UnslugPersonID}, "x")
	if ev.Total != 1 {
		t.Errorf("evidence = %+v, want the one shared recording counted once", ev)
	}
}

// OfWork is Of over every narrator of the work's recordings, excluding the work.
func TestNarrationProfileOfWork(t *testing.T) {
	cat := narrationCatalog(nil, map[string][2]any{
		"a": {"pl", []string{"anna"}},
		"b": {"pl", []string{"ben"}},
	})
	w := &model.Work{ID: "w", Language: "en", Recordings: []*model.Recording{
		{ID: "r1", Work: "w", Language: "en", Narrators: []string{"anna"}},
		{ID: "r2", Work: "w", Language: "en", Narrators: []string{"ben", "anna"}},
	}}
	cat.Works = append(cat.Works, w)
	ev := NewNarrationProfile(cat).OfWork(w)
	if ev.Total != 2 || !ev.Contradicts("en") {
		t.Errorf("evidence = %+v, want 2 pl recordings contradicting en", ev)
	}
}
