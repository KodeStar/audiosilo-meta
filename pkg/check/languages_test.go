package check

import (
	"slices"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/testpack"
	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// languages_test.go carries the passing and violating fixtures for the
// translation_of and ordering rules (languages.go). The passing half of the
// whole set is TestLanguageFieldsReachTheCatalog's tree, which carries every
// field at once; the cases here each break exactly one rule on top of baseValid.

// langWork renders a work by author-one in lang, with extra top-level members
// injected (nil for none) through testpack.WithFields, the injector every suite's
// link fixtures share.
func langWork(t testing.TB, id, lang string, fields map[string]any) string {
	doc := `{"authors":["author-one"],"id":"` + id + `","language":"` + lang +
		`","license":"CC0-1.0","sources":[{"type":"user"}],"title":"` + id + `"}`
	return testpack.WithFields(t, doc, fields)
}

// langSeries renders a series listing works at positions 1, 2, ... with extra
// top-level members injected (nil for none).
func langSeries(t testing.TB, id string, works []string, fields map[string]any) string {
	var members []string
	for i, w := range works {
		members = append(members, `{"position":"`+string(rune('1'+i))+`","work":"`+w+`"}`)
	}
	doc := `{"id":"` + id + `","license":"CC0-1.0","name":"` + id + `","sources":[{"type":"user"}],"works":[` +
		strings.Join(members, ",") + `]}`
	return testpack.WithFields(t, doc, fields)
}

// langTree is baseValid (book-one in English, series-one holding it) plus a
// French work book-un, a German work book-drei and an English work book-two, so
// a case only has to state the link it is about.
func langTree(t testing.TB) map[string]string {
	t.Helper()
	files := baseValid()
	files["works/bo/book-un/work.json"] = langWork(t, "book-un", "fr", nil)
	files["works/bo/book-drei/work.json"] = langWork(t, "book-drei", "de", nil)
	files["works/bo/book-two/work.json"] = langWork(t, "book-two", "en", nil)
	return files
}

func loadLangTree(t *testing.T, files map[string]string) Result {
	t.Helper()
	dir := t.TempDir()
	writeEntities(t, dir, files)
	return Load(dir)
}

// TestLanguageLinksValid is the passing fixture beyond the catalogue test's: a
// work translating two originals (sorted, both in another language), a series
// translation whose derived language is a TIE (the language rule stands down
// rather than guessing), and an ordering family of three distinct orders whose
// variants list only works the primary lists.
func TestLanguageLinksValid(t *testing.T) {
	files := langTree(t)
	files["works/bo/book-un/work.json"] = langWork(t, "book-un", "fr", map[string]any{"translation_of": []string{"book-one", "book-two"}})
	files["series/se/serie-mixte.json"] = langSeries(t, "serie-mixte", []string{"book-un", "book-two"},
		map[string]any{"translation_of": []string{"series-one"}})
	files["series/se/series-one.json"] = langSeries(t, "series-one", []string{"book-one", "book-two"},
		map[string]any{"ordering": "publication"})
	files["series/se/series-one-chrono.json"] = langSeries(t, "series-one-chrono", []string{"book-two", "book-one"},
		map[string]any{"ordering": "chronological", "ordering_of": "series-one"})
	files["series/se/series-one-rec.json"] = langSeries(t, "series-one-rec", []string{"book-two"},
		map[string]any{"ordering": "recommended", "ordering_of": "series-one"})
	res := loadLangTree(t, files)
	if !res.OK() {
		t.Fatalf("valid language links reported problems:\n%s", joinProblems(res.Problems))
	}
	for _, w := range res.Warnings {
		if AdvisoryClass(w) == AdvisoryOrderingNotSubset {
			t.Errorf("a variant listing only its primary's works was reported: %v", w)
		}
	}
}

// TestLanguageLinkRules is the violating half: each case breaks one rule, and
// the problem must be reported against the linking record's own pack entry.
func TestLanguageLinkRules(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(map[string]string)
		path   string // the pack entry the problem is reported against
		want   string
	}{
		// translation_of on a work.
		{
			name: "work target does not exist",
			mutate: func(f map[string]string) {
				f["works/bo/book-un/work.json"] = langWork(t, "book-un", "fr", map[string]any{"translation_of": []string{"book-nine"}})
			},
			path: "works/0/0.json: entry book-un",
			want: `translation_of "book-nine" is no live work id`,
		},
		{
			// A slug of ANOTHER family is no target: the namespaces are separate.
			name: "work target is a series slug",
			mutate: func(f map[string]string) {
				f["works/bo/book-un/work.json"] = langWork(t, "book-un", "fr", map[string]any{"translation_of": []string{"series-one"}})
			},
			path: "works/0/0.json: entry book-un",
			want: `translation_of "series-one" is no live work id`,
		},
		{
			name: "work target is retired",
			mutate: func(f map[string]string) {
				f["redirects.json"] = `{"people":{},"series":{},"works":{"book-uno":"book-one"}}`
				f["works/bo/book-un/work.json"] = langWork(t, "book-un", "fr", map[string]any{"translation_of": []string{"book-uno"}})
			},
			path: "works/0/0.json: entry book-un",
			want: `translation_of "book-uno" is retired - point at "book-one"`,
		},
		{
			name: "work links to itself",
			mutate: func(f map[string]string) {
				f["works/bo/book-un/work.json"] = langWork(t, "book-un", "fr", map[string]any{"translation_of": []string{"book-un"}})
			},
			path: "works/0/0.json: entry book-un",
			want: "translation_of names the record itself",
		},
		{
			// Primary subtags are what is compared, so a regional tag is the same
			// language as its bare one.
			name: "work in the same language as its original",
			mutate: func(f map[string]string) {
				f["works/bo/book-un/work.json"] = langWork(t, "book-un", "en-GB", map[string]any{"translation_of": []string{"book-one"}})
			},
			path: "works/0/0.json: entry book-un",
			want: `translation_of "book-one" is in the same language ("en") as this work`,
		},
		{
			name: "work translation chain",
			mutate: func(f map[string]string) {
				f["works/bo/book-un/work.json"] = langWork(t, "book-un", "fr", map[string]any{"translation_of": []string{"book-one"}})
				f["works/bo/book-drei/work.json"] = langWork(t, "book-drei", "de", map[string]any{"translation_of": []string{"book-un"}})
			},
			path: "works/0/0.json: entry book-drei",
			want: `translation_of "book-un" is itself a translation (of "book-one")`,
		},
		{
			name: "work targets out of order",
			mutate: func(f map[string]string) {
				f["works/bo/book-un/work.json"] = langWork(t, "book-un", "fr", map[string]any{"translation_of": []string{"book-two", "book-one"}})
			},
			path: "works/0/0.json: entry book-un",
			want: `translation_of must be sorted: "book-one" comes after "book-two"`,
		},
		// translation_of on a series.
		{
			name: "series target does not exist",
			mutate: func(f map[string]string) {
				f["series/se/serie-un.json"] = langSeries(t, "serie-un", []string{"book-un"}, map[string]any{"translation_of": []string{"series-nine"}})
			},
			path: "series/0.json: entry serie-un",
			want: `translation_of "series-nine" is no live series id`,
		},
		{
			name: "series target is retired",
			mutate: func(f map[string]string) {
				f["redirects.json"] = `{"people":{},"series":{"series-uno":"series-one"},"works":{}}`
				f["series/se/serie-un.json"] = langSeries(t, "serie-un", []string{"book-un"}, map[string]any{"translation_of": []string{"series-uno"}})
			},
			path: "series/0.json: entry serie-un",
			want: `translation_of "series-uno" is retired - point at "series-one"`,
		},
		{
			name: "series links to itself",
			mutate: func(f map[string]string) {
				f["series/se/serie-un.json"] = langSeries(t, "serie-un", []string{"book-un"}, map[string]any{"translation_of": []string{"serie-un"}})
			},
			path: "series/0.json: entry serie-un",
			want: "translation_of names the record itself",
		},
		{
			name: "series translation chain",
			mutate: func(f map[string]string) {
				f["series/se/serie-un.json"] = langSeries(t, "serie-un", []string{"book-un"}, map[string]any{"translation_of": []string{"series-one"}})
				f["series/se/reihe-drei.json"] = langSeries(t, "reihe-drei", []string{"book-drei"}, map[string]any{"translation_of": []string{"serie-un"}})
			},
			path: "series/0.json: entry reihe-drei",
			want: `translation_of "serie-un" is itself a translation (of "series-one")`,
		},
		{
			name: "series targets out of order",
			mutate: func(f map[string]string) {
				f["series/se/series-two.json"] = langSeries(t, "series-two", []string{"book-two"}, nil)
				f["series/se/serie-un.json"] = langSeries(t, "serie-un", []string{"book-un"},
					map[string]any{"translation_of": []string{"series-two", "series-one"}})
			},
			path: "series/0.json: entry serie-un",
			want: `translation_of must be sorted: "series-one" comes after "series-two"`,
		},
		// ordering / ordering_of.
		{
			name: "ordering_of target does not exist",
			mutate: func(f map[string]string) {
				f["series/se/series-one-chrono.json"] = langSeries(t, "series-one-chrono", []string{"book-one"},
					map[string]any{"ordering": "chronological", "ordering_of": "series-nine"})
			},
			path: "series/0.json: entry series-one-chrono",
			want: `ordering_of "series-nine" is no live series id`,
		},
		{
			name: "ordering_of target is retired",
			mutate: func(f map[string]string) {
				f["redirects.json"] = `{"people":{},"series":{"series-uno":"series-one"},"works":{}}`
				f["series/se/series-one-chrono.json"] = langSeries(t, "series-one-chrono", []string{"book-one"},
					map[string]any{"ordering": "chronological", "ordering_of": "series-uno"})
			},
			path: "series/0.json: entry series-one-chrono",
			want: `ordering_of "series-uno" is retired - point at "series-one"`,
		},
		{
			name: "ordering_of names the series itself",
			mutate: func(f map[string]string) {
				f["series/se/series-one-chrono.json"] = langSeries(t, "series-one-chrono", []string{"book-one"},
					map[string]any{"ordering": "chronological", "ordering_of": "series-one-chrono"})
			},
			path: "series/0.json: entry series-one-chrono",
			want: "ordering_of names the series itself",
		},
		{
			name: "ordering_of two hops",
			mutate: func(f map[string]string) {
				f["series/se/series-one-chrono.json"] = langSeries(t, "series-one-chrono", []string{"book-one"},
					map[string]any{"ordering": "chronological", "ordering_of": "series-one"})
				f["series/se/series-one-rec.json"] = langSeries(t, "series-one-rec", []string{"book-one"},
					map[string]any{"ordering": "recommended", "ordering_of": "series-one-chrono"})
			},
			path: "series/0.json: entry series-one-rec",
			want: `ordering_of "series-one-chrono" is itself a variant (of "series-one")`,
		},
		{
			name: "variant restates the primary's ordering",
			mutate: func(f map[string]string) {
				f["series/se/series-one.json"] = langSeries(t, "series-one", []string{"book-one"}, map[string]any{"ordering": "publication"})
				f["series/se/series-one-too.json"] = langSeries(t, "series-one-too", []string{"book-one"},
					map[string]any{"ordering": "publication", "ordering_of": "series-one"})
			},
			path: "series/0.json: entry series-one-too",
			want: `series "series-one" and "series-one-too" both state the "publication" ordering of the family whose primary is "series-one"`,
		},
		{
			// Two VARIANTS in one order are the same duplicate, with no ordering on
			// the primary at all; the later id is reported against the earlier.
			name: "two variants in one ordering",
			mutate: func(f map[string]string) {
				f["series/se/series-one-b.json"] = langSeries(t, "series-one-b", []string{"book-one"},
					map[string]any{"ordering": "chronological", "ordering_of": "series-one"})
				f["series/se/series-one-a.json"] = langSeries(t, "series-one-a", []string{"book-one"},
					map[string]any{"ordering": "chronological", "ordering_of": "series-one"})
			},
			path: "series/0.json: entry series-one-b",
			want: `series "series-one-a" and "series-one-b" both state the "chronological" ordering`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			files := langTree(t)
			c.mutate(files)
			res := loadLangTree(t, files)
			found := false
			for _, p := range res.Problems {
				if strings.Contains(p.Msg, c.want) {
					found = true
					if p.Path != c.path {
						t.Errorf("reported at %q, want %q", p.Path, c.path)
					}
				}
			}
			if !found {
				t.Errorf("no problem contained %q; problems:\n%s", c.want, joinProblems(res.Problems))
			}
		})
	}
}

// TestSeriesTranslationSameLanguageIsAdvisory: a series has no language of its
// own, so two sides DERIVING one language says a member is misfiled rather than
// that the link is false - an ADVISORY, classified and counted, never a problem
// (DESIGN section 11: a sync-bot volume must not turn red a link it did not
// write). The WORK-level rule, over two stated languages, stays a problem.
func TestSeriesTranslationSameLanguageIsAdvisory(t *testing.T) {
	files := langTree(t)
	files["series/se/serie-en.json"] = langSeries(t, "serie-en", []string{"book-two"}, map[string]any{"translation_of": []string{"series-one"}})
	res := loadLangTree(t, files)
	if !res.OK() {
		t.Fatalf("a same-language SERIES link failed the load:\n%s", joinProblems(res.Problems))
	}
	var got []Problem
	for _, w := range res.Warnings {
		if AdvisoryClass(w) == AdvisorySeriesTranslationSameLanguage {
			got = append(got, w)
		}
	}
	if len(got) != 1 {
		t.Fatalf("want one series-translation-same-language advisory, got %v (all: %v)", got, res.Warnings)
	}
	if got[0].Path != "series/0.json: entry serie-en" ||
		!strings.Contains(got[0].Msg, `translation_of "series-one" derives the same language ("en") as this series`) {
		t.Errorf("advisory = %s: %s", got[0].Path, got[0].Msg)
	}
	if census := AdvisoryCensus(res.Warnings); !strings.Contains(census,
		"1 series translation links whose two sides derive the same language") {
		t.Errorf("census %q does not count the advisory", census)
	}
}

// TestLinkFaultsSeesBothEnds: the writers' door asks about ONE record, so it must
// see a fault from either end of a link - on the links the record states and on
// the links naming it - while the load, judging every record in turn, reports
// each fault once, against the record stating the link.
func TestLinkFaultsSeesBothEnds(t *testing.T) {
	works := map[string]*model.Work{
		"orig":  {ID: "orig", Language: "en"},
		"trans": {ID: "trans", Language: "fr", TranslationOf: []string{"orig"}},
		"re":    {ID: "re", Language: "de", TranslationOf: []string{"trans"}},
	}
	v := NewLinkView(works, nil, nil)
	chain := func(fs []LinkFault) []string {
		var out []string
		for _, f := range fs {
			if f.Code == LinkChain {
				out = append(out, f.From+">"+f.To)
			}
		}
		return out
	}
	if got := chain(LinkFaults(v, model.RedirectWorks, "trans")); !slices.Equal(got, []string{"re>trans"}) {
		t.Errorf("faults involving the middle of a chain = %v, want the one on re's link naming it", got)
	}
	if got := chain(LinkFaults(v, model.RedirectWorks, "re")); !slices.Equal(got, []string{"re>trans"}) {
		t.Errorf("faults of the link's own record = %v", got)
	}
	if got := LinkFaults(v, model.RedirectWorks, "orig"); len(got) != 0 {
		t.Errorf("the original is in no fault, got %+v", got)
	}

	series := map[string]*model.Series{
		"pub":    {ID: "pub", Ordering: model.OrderingPublication},
		"chrono": {ID: "chrono", Ordering: model.OrderingPublication, OrderingOf: "pub"},
	}
	v = NewLinkView(nil, series, nil)
	for _, id := range []string{"pub", "chrono"} {
		fs := LinkFaults(v, model.RedirectSeries, id)
		if len(fs) != 1 || fs[0].Code != LinkDuplicateOrdering || fs[0].From != "chrono" || fs[0].Others[0] != "pub" {
			t.Errorf("LinkFaults(%s) = %+v, want the one duplicate ordering, on chrono naming pub", id, fs)
		}
	}
}

// TestSeriesTranslationLanguageTieStandsDown: a series whose members split
// evenly between two languages has no derived language, so the same-language
// rule cannot be asked of it - on EITHER side of the link. A sync-bot volume
// that tips a linked series into a tie must not turn the link red.
func TestSeriesTranslationLanguageTieStandsDown(t *testing.T) {
	for name, series := range map[string]map[string]string{
		"tie on the translation": {
			"series/se/serie-mixte.json": langSeries(t, "serie-mixte", []string{"book-un", "book-two"},
				map[string]any{"translation_of": []string{"series-one"}}),
		},
		"tie on the original": {
			"series/se/series-one.json": langSeries(t, "series-one", []string{"book-one", "book-un"}, nil),
			"series/se/serie-en.json":   langSeries(t, "serie-en", []string{"book-two"}, map[string]any{"translation_of": []string{"series-one"}}),
		},
	} {
		t.Run(name, func(t *testing.T) {
			files := langTree(t)
			for k, v := range series {
				files[k] = v
			}
			if res := loadLangTree(t, files); !res.OK() {
				t.Errorf("a tied language was judged:\n%s", joinProblems(res.Problems))
			}
		})
	}
}

// TestOrderingVariantNotSubsetAdvisory: a variant listing a work its primary
// does not is legitimate (a chronological order holding a prequel novella), so
// it is an ADVISORY - classified, counted by the census, never a problem.
func TestOrderingVariantNotSubsetAdvisory(t *testing.T) {
	files := langTree(t)
	files["series/se/series-one-chrono.json"] = langSeries(t, "series-one-chrono", []string{"book-two", "book-one", "book-two"}, map[string]any{"ordering": "chronological", "ordering_of": "series-one"})
	res := loadLangTree(t, files)
	if !res.OK() {
		t.Fatalf("the advisory fixture must validate:\n%s", joinProblems(res.Problems))
	}
	var got []Problem
	for _, w := range res.Warnings {
		if AdvisoryClass(w) == AdvisoryOrderingNotSubset {
			got = append(got, w)
		}
	}
	if len(got) != 1 {
		t.Fatalf("want one ordering-variant-not-subset advisory, got %v (all: %v)", got, res.Warnings)
	}
	w := got[0]
	if w.Path != "series/0.json: entry series-one-chrono" {
		t.Errorf("advisory reported at %q", w.Path)
	}
	// Named once however often the variant lists it, with the primary beside it.
	if !strings.Contains(w.Msg, `lists "book-two", which its primary ordering "series-one" does not`) {
		t.Errorf("advisory message = %q", w.Msg)
	}
	if census := AdvisoryCensus(res.Warnings); !strings.Contains(census,
		"1 ordering variants listing works their primary does not") {
		t.Errorf("census %q does not count the advisory", census)
	}
}

// TestLanguageLinksRunUnderTheCoreProfile: works and series live in the core
// tree, so a core root checked on its own runs every rule - and a community
// root, which holds neither family, has nothing for them to see.
func TestLanguageLinksRunUnderTheCoreProfile(t *testing.T) {
	files := langTree(t)
	files["works/bo/book-un/work.json"] = langWork(t, "book-un", "fr", map[string]any{"translation_of": []string{"book-nine"}})
	files["series/se/series-one-chrono.json"] = langSeries(t, "series-one-chrono", []string{"book-one"},
		map[string]any{"ordering": "chronological", "ordering_of": "series-nine"})
	dir := t.TempDir()
	writeEntities(t, dir, files)
	res := LoadProfile(dir, pack.ProfileCore)
	for _, want := range []string{`translation_of "book-nine"`, `ordering_of "series-nine"`} {
		if !hasProblem(res.Problems, want) {
			t.Errorf("core profile did not report %q:\n%s", want, joinProblems(res.Problems))
		}
	}

	comDir := t.TempDir()
	writeTree(t, comDir, communityOnly())
	if res := LoadProfile(comDir, pack.ProfileCommunity); !res.OK() {
		t.Errorf("a community root reported problems:\n%s", joinProblems(res.Problems))
	}
}

// TestComposeRunsLanguageLinksOverTheCore: the composed load reads the core root
// as ProfileCore, so the rules run there and report against the CORE path (no
// community attribution), exactly as a single-root load of it would.
func TestComposeRunsLanguageLinksOverTheCore(t *testing.T) {
	files := langTree(t)
	files["works/bo/book-un/work.json"] = langWork(t, "book-un", "fr", map[string]any{"translation_of": []string{"book-un"}})
	coreDir := t.TempDir()
	writeEntities(t, coreDir, files)
	comDir := t.TempDir()
	writeTree(t, comDir, composeCommunity(map[string]string{"book-one": bothSidecars("book-one")}))

	res := LoadComposed(coreDir, comDir)
	idx := slices.IndexFunc(res.Problems, func(p Problem) bool {
		return strings.Contains(p.Msg, "translation_of names the record itself")
	})
	if idx < 0 {
		t.Fatalf("the composed load did not run the rule:\n%s", joinProblems(res.Problems))
	}
	if got := res.Problems[idx].Path; got != "works/0/0.json: entry book-un" {
		t.Errorf("reported at %q, want the core path", got)
	}
}
