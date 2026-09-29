package issueform

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/testpack"
	"github.com/kodestar/audiosilo-meta/pkg/check"
	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/redirects"
)

// links_test.go pins the correct-data form's translation_of / ordering / ordering_of
// corrections (compose_links.go): every verdict the compose-time rules can give, and
// the tombstone resolution of a retired reference.

// linkTree is the shared seed plus a small multilingual catalogue:
//
//   - works: existing-work (en, the seed's), das-werk (de), other-en (en) and
//     l-oeuvre (fr, a translation of existing-work);
//   - series: existing-series (en), die-serie (de), la-serie (fr, a translation of
//     existing-series), tie-series (one en and one de member, so its language is a
//     tie), and an ordering family - saga-pub (publication, the primary) and
//     saga-chrono (chronological, its variant) - beside saga-plain (no ordering)
//     and saga-rec (recommended, no family yet);
//   - tombstones: old-existing -> existing-work, old-existing-series ->
//     existing-series, old-saga-pub -> saga-pub.
func linkTree(t *testing.T) string {
	t.Helper()
	files := seedFiles()
	work := func(id, title, lang string, opts ...testpack.WorkOpt) {
		files["works/"+id[:2]+"/"+id+"/work.json"] = testpack.WorkJSON(t, id, title, append(opts, testpack.WithLanguage(lang))...)
	}
	work("das-werk", "Das Werk", "de")
	work("other-en", "Other Work", "en")
	work("l-oeuvre", "L'Oeuvre", "fr", testpack.WithTranslationOf("existing-work"))
	series := func(id string, fields map[string]any, members ...string) {
		files["series/"+id[:2]+"/"+id+".json"] = testpack.WithFields(t, testpack.SeriesJSON(t, id, id, members...), fields)
	}
	series("die-serie", nil, "das-werk@1")
	series("la-serie", map[string]any{"translation_of": []string{"existing-series"}}, "l-oeuvre@1")
	series("tie-series", nil, "existing-work@1", "das-werk@2")
	series("saga-pub", map[string]any{"ordering": model.OrderingPublication}, "existing-work@1")
	series("saga-chrono", map[string]any{"ordering": model.OrderingChronological, "ordering_of": "saga-pub"}, "existing-work@1")
	series("saga-plain", nil, "existing-work@1")
	series("saga-rec", map[string]any{"ordering": model.OrderingRecommended}, "existing-work@1")
	series("saga-chrono-2", map[string]any{"ordering": model.OrderingChronological}, "other-en@1")

	dir := t.TempDir()
	testpack.Seed(t, dir, files)
	if err := redirects.Write(dir, model.Redirects{
		model.RedirectWorks:  {"old-existing": "existing-work"},
		model.RedirectSeries: {"old-existing-series": "existing-series", "old-saga-pub": "saga-pub"},
	}); err != nil {
		t.Fatal(err)
	}
	if res := check.Load(dir); !res.OK() {
		t.Fatalf("link tree does not validate: %v", res.Problems)
	}
	return dir
}

// linkField reads one member off a composed record.
func linkField(t *testing.T, dir, address, field string) any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(readFile(t, dir, address)), &m); err != nil {
		t.Fatal(err)
	}
	return m[field]
}

func correctLink(t *testing.T, dir, record, fieldName, value string) Result {
	t.Helper()
	return Process(Options{DataDir: dir, Template: "correct-data", Body: correctBody(record, fieldName, value, "the publisher's catalogue", true)})
}

func TestCorrectTranslationOfVerdicts(t *testing.T) {
	const deWork, frWork = "https://meta.audiosilo.app/works/das-werk", "data/works/l-/l-oeuvre/work.json"
	for _, tc := range []struct {
		name, record, value string
		status              Status
		mention             string
	}{
		{"unknown target", deWork, "no-such-work", StatusInvalid, `work "no-such-work" is not in the catalogue`},
		{"self", deWork, "das-werk", StatusInvalid, "cannot be a translation of itself"},
		{"same language", "https://meta.audiosilo.app/works/other-en", "existing-work", StatusInvalid, `are both in "en"`},
		{"target is itself a translation", deWork, "l-oeuvre", StatusInvalid, `work "l-oeuvre" is itself a translation (of existing-work)`},
		{"record is an original", "https://meta.audiosilo.app/works/existing-work", "das-werk", StatusInvalid, "is the original that l-oeuvre translate"},
		{"already present", frWork, "https://meta.audiosilo.app/works/existing-work", StatusDuplicate, `already names "existing-work" in translation_of`},
		{"not a work reference", deWork, "https://meta.audiosilo.app/series/existing-series", StatusInvalid, "is not a work reference"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := linkTree(t)
			res := correctLink(t, dir, tc.record, "translation_of", tc.value)
			if res.Status != tc.status || !anyContains(res.Messages, tc.mention) {
				t.Errorf("status = %q, messages = %v; want %q mentioning %q", res.Status, res.Messages, tc.status, tc.mention)
			}
			if len(res.Files) != 0 {
				t.Errorf("a refused correction wrote: %v", res.Files)
			}
		})
	}
}

// The ADD op: an original is contributed to the set, which is re-sorted, and the
// evidence is stamped.
func TestCorrectTranslationOfAddsToTheSet(t *testing.T) {
	dir := linkTree(t)
	res := correctLink(t, dir, "https://meta.audiosilo.app/works/l-oeuvre", "translated from", "das-werk")
	if res.Status != StatusOK {
		t.Fatalf("status = %q, messages = %v", res.Status, res.Messages)
	}
	if got := linkSet(t, dir, "works/l-/l-oeuvre/work.json"); !slices.Equal(got, []string{"das-werk", "existing-work"}) {
		t.Errorf("translation_of = %v, want the sorted set [das-werk existing-work]", got)
	}
	if !anyContains(res.Messages, `added translation_of "das-werk"`) {
		t.Errorf("the verdict does not say what was added: %v", res.Messages)
	}
	if res := check.Load(dir); !res.OK() {
		t.Errorf("the corrected tree does not validate: %v", res.Problems)
	}
}

// A retired reference composes under its survivor and says so.
func TestCorrectTranslationOfResolvesARetiredSlug(t *testing.T) {
	dir := linkTree(t)
	res := correctLink(t, dir, "https://meta.audiosilo.app/works/das-werk", "translation_of", "https://meta.audiosilo.app/works/old-existing")
	if res.Status != StatusOK {
		t.Fatalf("status = %q, messages = %v", res.Status, res.Messages)
	}
	if got := linkSet(t, dir, "works/da/das-werk/work.json"); !slices.Equal(got, []string{"existing-work"}) {
		t.Errorf("translation_of = %v, want the survivor existing-work", got)
	}
	if !anyContains(res.Messages, `works slug "old-existing" was retired by a merge onto "existing-work"`) {
		t.Errorf("the verdict does not report the retired slug: %v", res.Messages)
	}
}

func TestCorrectSeriesTranslationOf(t *testing.T) {
	t.Run("ok, through a retired series URL", func(t *testing.T) {
		dir := linkTree(t)
		res := correctLink(t, dir, "https://meta.audiosilo.app/series/die-serie", "translation_of",
			"https://meta.audiosilo.app/series/old-existing-series")
		if res.Status != StatusOK {
			t.Fatalf("status = %q, messages = %v", res.Status, res.Messages)
		}
		if got := linkSet(t, dir, "series/di/die-serie.json"); !slices.Equal(got, []string{"existing-series"}) {
			t.Errorf("translation_of = %v, want [existing-series]", got)
		}
		if !anyContains(res.Messages, `series slug "old-existing-series" was retired`) {
			t.Errorf("the retired slug is not reported: %v", res.Messages)
		}
	})
	t.Run("a tie cannot be judged, so it is not", func(t *testing.T) {
		dir := linkTree(t)
		res := correctLink(t, dir, "https://meta.audiosilo.app/series/tie-series", "translation_of", "die-serie")
		if res.Status != StatusOK {
			t.Fatalf("status = %q, messages = %v", res.Status, res.Messages)
		}
	})
	for _, tc := range []struct {
		name, record, value, mention string
	}{
		{"same derived language", "saga-pub", "existing-series", `are both in "en"`},
		{"unknown", "die-serie", "no-such-series", `series "no-such-series" is not in the catalogue`},
		{"target is a translation", "die-serie", "la-serie", `series "la-serie" is itself a translation (of existing-series)`},
		{"record is an original", "existing-series", "die-serie", "is the original that la-serie translate"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := linkTree(t)
			res := correctLink(t, dir, "https://meta.audiosilo.app/series/"+tc.record, "translation_of", tc.value)
			if res.Status != StatusInvalid || !anyContains(res.Messages, tc.mention) {
				t.Errorf("status = %q, messages = %v; want invalid mentioning %q", res.Status, res.Messages, tc.mention)
			}
		})
	}
}

func TestCorrectSeriesOrdering(t *testing.T) {
	t.Run("ok, in the schema's spelling", func(t *testing.T) {
		dir := linkTree(t)
		res := correctLink(t, dir, "https://meta.audiosilo.app/series/saga-plain", "ordering", "Recommended")
		if res.Status != StatusOK {
			t.Fatalf("status = %q, messages = %v", res.Status, res.Messages)
		}
		if got := linkField(t, dir, "series/sa/saga-plain.json", "ordering"); got != model.OrderingRecommended {
			t.Errorf("ordering = %v, want %q", got, model.OrderingRecommended)
		}
	})
	t.Run("outside the vocabulary", func(t *testing.T) {
		dir := linkTree(t)
		res := correctLink(t, dir, "https://meta.audiosilo.app/series/saga-plain", "ordering", "alphabetical")
		if res.Status != StatusInvalid || !anyContains(res.Messages, `"chronological"`) {
			t.Errorf("status = %q, messages = %v; want invalid listing the vocabulary", res.Status, res.Messages)
		}
	})
	t.Run("a second series in one order of a family", func(t *testing.T) {
		dir := linkTree(t)
		res := correctLink(t, dir, "https://meta.audiosilo.app/series/saga-chrono", "ordering", "publication")
		if res.Status != StatusInvalid || !anyContains(res.Messages, `series "saga-pub" already states the "publication" ordering of saga-pub's family`) {
			t.Errorf("status = %q, messages = %v; want invalid naming saga-pub", res.Status, res.Messages)
		}
	})
}

func TestCorrectSeriesOrderingOf(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		dir := linkTree(t)
		res := correctLink(t, dir, "https://meta.audiosilo.app/series/saga-rec", "ordering_of", "https://meta.audiosilo.app/series/saga-pub")
		if res.Status != StatusOK {
			t.Fatalf("status = %q, messages = %v", res.Status, res.Messages)
		}
		if got := linkField(t, dir, "series/sa/saga-rec.json", "ordering_of"); got != "saga-pub" {
			t.Errorf("ordering_of = %v, want saga-pub", got)
		}
		if res := check.Load(dir); !res.OK() {
			t.Errorf("the corrected tree does not validate: %v", res.Problems)
		}
	})
	t.Run("a retired primary composes under its survivor", func(t *testing.T) {
		dir := linkTree(t)
		res := correctLink(t, dir, "https://meta.audiosilo.app/series/saga-rec", "ordering_of", "old-saga-pub")
		if res.Status != StatusOK {
			t.Fatalf("status = %q, messages = %v", res.Status, res.Messages)
		}
		if got := linkField(t, dir, "series/sa/saga-rec.json", "ordering_of"); got != "saga-pub" {
			t.Errorf("ordering_of = %v, want the survivor saga-pub", got)
		}
		if !anyContains(res.Messages, `series slug "old-saga-pub" was retired by a merge onto "saga-pub"`) {
			t.Errorf("the retired slug is not reported: %v", res.Messages)
		}
	})
	t.Run("already the primary", func(t *testing.T) {
		dir := linkTree(t)
		res := correctLink(t, dir, "https://meta.audiosilo.app/series/saga-chrono", "ordering_of", "saga-pub")
		if res.Status != StatusDuplicate {
			t.Errorf("status = %q, messages = %v; want the no-op duplicate", res.Status, res.Messages)
		}
	})
	for _, tc := range []struct {
		name, record, value, mention string
	}{
		{"no ordering stated", "saga-plain", "saga-pub", "state this series' ordering first"},
		{"self", "saga-rec", "saga-rec", "cannot be a variant ordering of itself"},
		{"unknown", "saga-rec", "no-such-series", `series "no-such-series" is not in the catalogue`},
		{"target is a variant", "saga-rec", "saga-chrono", `series "saga-chrono" is itself a variant ordering of "saga-pub"`},
		{"record is a primary", "saga-pub", "saga-rec", "is the primary ordering that saga-chrono name"},
		{"duplicate ordering in the family", "saga-chrono-2", "saga-pub", `series "saga-chrono" already states the "chronological" ordering of saga-pub's family`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := linkTree(t)
			res := correctLink(t, dir, "https://meta.audiosilo.app/series/"+tc.record, "ordering_of", tc.value)
			if res.Status != StatusInvalid || !anyContains(res.Messages, tc.mention) {
				t.Errorf("status = %q, messages = %v; want invalid mentioning %q", res.Status, res.Messages, tc.mention)
			}
			if len(res.Files) != 0 {
				t.Errorf("a refused correction wrote: %v", res.Files)
			}
		})
	}
}

// A work's LANGUAGE can break a translation link from a distance: a correction that
// lands a work on the primary language of the work it translates, or of a work
// translating it, is asked over the resulting state and goes to a maintainer (which
// of the two statements is wrong is not the bot's call). A language that clashes with
// nothing is applied as ever.
func TestCorrectWorkLanguageAgainstItsTranslationLinks(t *testing.T) {
	for _, tc := range []struct {
		name, record, value string
		status              Status
		mention             string
	}{
		{"the translation lands on its original's language", "l-oeuvre", "en", StatusNeedsHuman,
			`would then be in "en", the same language as work "existing-work", which it names in translation_of`},
		{"the original lands on its translation's language", "existing-work", "fr", StatusNeedsHuman,
			`would then be in "fr", the same language as work "l-oeuvre", which names it as its original`},
		{"a region of the same language is the same language", "l-oeuvre", "en-GB", StatusNeedsHuman,
			`the same language as work "existing-work"`},
		{"a language clashing with nothing", "l-oeuvre", "de", StatusOK, "applied language = de"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := linkTree(t)
			res := correctLink(t, dir, "https://meta.audiosilo.app/works/"+tc.record, "language", tc.value)
			if res.Status != tc.status || !anyContains(res.Messages, tc.mention) {
				t.Errorf("status = %q, messages = %v; want %q mentioning %q", res.Status, res.Messages, tc.status, tc.mention)
			}
			if tc.status != StatusOK && len(res.Files) != 0 {
				t.Errorf("a refused correction wrote: %v", res.Files)
			}
		})
	}
}

// translation_of against a RECORDING is the misaddressed-field verdict: the field is
// the work's.
func TestTranslationOfOnARecordingNamesTheWork(t *testing.T) {
	dir := linkTree(t)
	res := correctLink(t, dir, recordingRef("existing-work", "john-smith-2020"), "translation_of", "das-werk")
	if res.Status != StatusInvalid || !anyContains(res.Messages, `"translation_of" is a work field`) {
		t.Errorf("status = %q, messages = %v; want the misaddressed-field verdict", res.Status, res.Messages)
	}
}

// linkSet reads a composed record's link set as the string list it must be.
func linkSet(t *testing.T, dir, address string) []string {
	t.Helper()
	got, ok := stringsOf(linkField(t, dir, address, "translation_of"))
	if !ok {
		t.Fatalf("%s translation_of is not a string array", address)
	}
	return got
}
