package check

import (
	"slices"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

func faultCodes(fs []LinkFault) []string {
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		out = append(out, string(f.Code)+":"+f.From+">"+f.To)
	}
	return out
}

// TestLinkOverlayIntroducedFaults: the overlay answers every question about ONE record
// from what the change would write, the inverse included, and IntroducedFaults is the
// faults that state has and the base does not.
func TestLinkOverlayIntroducedFaults(t *testing.T) {
	works := map[string]*model.Work{
		"orig":   {ID: "orig", Language: "en"},
		"trans":  {ID: "trans", Language: "fr", TranslationOf: []string{"orig"}},
		"de":     {ID: "de", Language: "de"},
		"en-dup": {ID: "en-dup", Language: "en"},
		// A fault the catalogue already carries: a same-language link on "stale".
		"stale": {ID: "stale", Language: "en", TranslationOf: []string{"orig"}},
	}
	reds := model.NewRedirects()
	reds[model.RedirectWorks] = map[string]string{"gone": "orig"}
	base := NewLinkView(works, nil, reds)
	overlay := func(id string, to ...string) *LinkOverlay {
		rec := LinkRecordOf(base, model.RedirectWorks, id)
		rec.TranslationOf = slices.Sorted(slices.Values(append(slices.Clone(rec.TranslationOf), to...)))
		return NewLinkOverlay(base, model.RedirectWorks, id, rec)
	}

	// Passing: a clean link introduces nothing, and the inverse sees it.
	o := overlay("de", "orig")
	if got := o.IntroducedFaults(); len(got) != 0 {
		t.Errorf("a clean link introduced %v", faultCodes(got))
	}
	if got := o.TranslatedBy(model.RedirectWorks, "orig"); !slices.Equal(got, []string{"de", "stale", "trans"}) {
		t.Errorf("TranslatedBy(orig) over the overlay = %v", got)
	}

	// Violating: each rule, from the overlaid record's end.
	for name, tc := range map[string]struct {
		id, to string
		want   []string
	}{
		"chain":         {"de", "trans", []string{"chain:de>trans"}},
		"same language": {"en-dup", "orig", []string{"same-language:en-dup>orig"}},
		"self":          {"de", "de", []string{"self:de>de"}},
		"retired":       {"de", "gone", []string{"retired:de>gone"}},
		"dead":          {"de", "nothing", []string{"dead:de>nothing"}},
		// The record is already an original: linking it makes trans a chain, a fault
		// on ANOTHER record's link that the change introduces.
		"original becomes a translation": {"orig", "de", []string{"chain:trans>orig", "chain:stale>orig"}},
	} {
		got := faultCodes(overlay(tc.id, tc.to).IntroducedFaults())
		slices.Sort(got)
		want := slices.Sorted(slices.Values(tc.want))
		if !slices.Equal(got, want) {
			t.Errorf("%s: introduced %v, want %v", name, got, want)
		}
	}

	// A fault the base already carries is not introduced by a change elsewhere on the
	// record: stale's own same-language link survives its unrelated second link.
	if got := faultCodes(overlay("stale", "de").IntroducedFaults()); len(got) != 0 {
		t.Errorf("a pre-existing fault was reported as introduced: %v", got)
	}
}

// TestLinkOverlayRederivesASeriesLanguage: a work's language moves the derived
// language of every series listing it, so an overlaid work language is read by the
// series side too - and an unchanged one reads the base's memo.
func TestLinkOverlayRederivesASeriesLanguage(t *testing.T) {
	works := map[string]*model.Work{
		"a": {ID: "a", Language: "fr"},
		"b": {ID: "b", Language: "fr"},
		"c": {ID: "c", Language: "en"},
	}
	series := map[string]*model.Series{
		"s": {ID: "s", Works: []model.SeriesWork{{Work: "a"}, {Work: "b"}, {Work: "c"}}},
	}
	base := NewLinkView(works, series, nil)
	rec := LinkRecordOf(base, model.RedirectWorks, "a")
	o := NewLinkOverlay(base, model.RedirectWorks, "a", rec)
	if got := o.Language(model.RedirectSeries, "s"); got != "fr" {
		t.Errorf("unchanged overlay: series language = %q, want fr", got)
	}
	o.Record.Language = "en"
	if got := o.Language(model.RedirectSeries, "s"); got != "en" {
		t.Errorf("a moved to en: series language = %q, want en", got)
	}
}
