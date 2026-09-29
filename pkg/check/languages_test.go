package check

import (
	"slices"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// languages_test.go carries the passing and violating fixtures for the
// translation_of and ordering rules (languages.go). The passing half of the
// whole set is TestLanguageFieldsReachTheCatalog's tree, which carries every
// field at once; the cases here each break exactly one rule on top of baseValid.

// langWork renders a work by author-one in lang, with extra top-level members
// spliced in ("" for none).
func langWork(id, lang, extra string) string {
	doc := `{"authors":["author-one"],"id":"` + id + `","language":"` + lang +
		`","license":"CC0-1.0","sources":[{"type":"user"}],"title":"` + id + `"}`
	if extra == "" {
		return doc
	}
	return withMembers(doc, extra)
}

// langSeries renders a series listing works at positions 1, 2, ... with extra
// top-level members spliced in.
func langSeries(id string, works []string, extra string) string {
	var members []string
	for i, w := range works {
		members = append(members, `{"position":"`+string(rune('1'+i))+`","work":"`+w+`"}`)
	}
	doc := `{"id":"` + id + `","license":"CC0-1.0","name":"` + id + `","sources":[{"type":"user"}],"works":[` +
		strings.Join(members, ",") + `]}`
	if extra == "" {
		return doc
	}
	return withMembers(doc, extra)
}

// langTree is baseValid (book-one in English, series-one holding it) plus a
// French work book-un, a German work book-drei and an English work book-two, so
// a case only has to state the link it is about.
func langTree() map[string]string {
	files := baseValid()
	files["works/bo/book-un/work.json"] = langWork("book-un", "fr", "")
	files["works/bo/book-drei/work.json"] = langWork("book-drei", "de", "")
	files["works/bo/book-two/work.json"] = langWork("book-two", "en", "")
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
	files := langTree()
	files["works/bo/book-un/work.json"] = langWork("book-un", "fr", `"translation_of":["book-one","book-two"]`)
	files["series/se/serie-mixte.json"] = langSeries("serie-mixte", []string{"book-un", "book-two"},
		`"translation_of":["series-one"]`)
	files["series/se/series-one.json"] = langSeries("series-one", []string{"book-one", "book-two"},
		`"ordering":"publication"`)
	files["series/se/series-one-chrono.json"] = langSeries("series-one-chrono", []string{"book-two", "book-one"},
		`"ordering":"chronological","ordering_of":"series-one"`)
	files["series/se/series-one-rec.json"] = langSeries("series-one-rec", []string{"book-two"},
		`"ordering":"recommended","ordering_of":"series-one"`)
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
				f["works/bo/book-un/work.json"] = langWork("book-un", "fr", `"translation_of":["book-nine"]`)
			},
			path: "works/0/0.json: entry book-un",
			want: `translation_of "book-nine" is no live work id`,
		},
		{
			// A slug of ANOTHER family is no target: the namespaces are separate.
			name: "work target is a series slug",
			mutate: func(f map[string]string) {
				f["works/bo/book-un/work.json"] = langWork("book-un", "fr", `"translation_of":["series-one"]`)
			},
			path: "works/0/0.json: entry book-un",
			want: `translation_of "series-one" is no live work id`,
		},
		{
			name: "work target is retired",
			mutate: func(f map[string]string) {
				f["redirects.json"] = `{"people":{},"series":{},"works":{"book-uno":"book-one"}}`
				f["works/bo/book-un/work.json"] = langWork("book-un", "fr", `"translation_of":["book-uno"]`)
			},
			path: "works/0/0.json: entry book-un",
			want: `translation_of "book-uno" is retired - point at "book-one"`,
		},
		{
			name: "work links to itself",
			mutate: func(f map[string]string) {
				f["works/bo/book-un/work.json"] = langWork("book-un", "fr", `"translation_of":["book-un"]`)
			},
			path: "works/0/0.json: entry book-un",
			want: "translation_of names the record itself",
		},
		{
			// Primary subtags are what is compared, so a regional tag is the same
			// language as its bare one.
			name: "work in the same language as its original",
			mutate: func(f map[string]string) {
				f["works/bo/book-un/work.json"] = langWork("book-un", "en-GB", `"translation_of":["book-one"]`)
			},
			path: "works/0/0.json: entry book-un",
			want: `translation_of "book-one" is in the same language ("en") as this work`,
		},
		{
			name: "work translation chain",
			mutate: func(f map[string]string) {
				f["works/bo/book-un/work.json"] = langWork("book-un", "fr", `"translation_of":["book-one"]`)
				f["works/bo/book-drei/work.json"] = langWork("book-drei", "de", `"translation_of":["book-un"]`)
			},
			path: "works/0/0.json: entry book-drei",
			want: `translation_of "book-un" is itself a translation (of "book-one")`,
		},
		{
			name: "work targets out of order",
			mutate: func(f map[string]string) {
				f["works/bo/book-un/work.json"] = langWork("book-un", "fr", `"translation_of":["book-two","book-one"]`)
			},
			path: "works/0/0.json: entry book-un",
			want: `translation_of must be sorted: "book-one" comes after "book-two"`,
		},
		// translation_of on a series.
		{
			name: "series target does not exist",
			mutate: func(f map[string]string) {
				f["series/se/serie-un.json"] = langSeries("serie-un", []string{"book-un"}, `"translation_of":["series-nine"]`)
			},
			path: "series/0.json: entry serie-un",
			want: `translation_of "series-nine" is no live series id`,
		},
		{
			name: "series target is retired",
			mutate: func(f map[string]string) {
				f["redirects.json"] = `{"people":{},"series":{"series-uno":"series-one"},"works":{}}`
				f["series/se/serie-un.json"] = langSeries("serie-un", []string{"book-un"}, `"translation_of":["series-uno"]`)
			},
			path: "series/0.json: entry serie-un",
			want: `translation_of "series-uno" is retired - point at "series-one"`,
		},
		{
			name: "series links to itself",
			mutate: func(f map[string]string) {
				f["series/se/serie-un.json"] = langSeries("serie-un", []string{"book-un"}, `"translation_of":["serie-un"]`)
			},
			path: "series/0.json: entry serie-un",
			want: "translation_of names the record itself",
		},
		{
			name: "series in the same derived language as its original",
			mutate: func(f map[string]string) {
				f["series/se/serie-en.json"] = langSeries("serie-en", []string{"book-two"}, `"translation_of":["series-one"]`)
			},
			path: "series/0.json: entry serie-en",
			want: `translation_of "series-one" is in the same language ("en") as this series`,
		},
		{
			name: "series translation chain",
			mutate: func(f map[string]string) {
				f["series/se/serie-un.json"] = langSeries("serie-un", []string{"book-un"}, `"translation_of":["series-one"]`)
				f["series/se/reihe-drei.json"] = langSeries("reihe-drei", []string{"book-drei"}, `"translation_of":["serie-un"]`)
			},
			path: "series/0.json: entry reihe-drei",
			want: `translation_of "serie-un" is itself a translation (of "series-one")`,
		},
		{
			name: "series targets out of order",
			mutate: func(f map[string]string) {
				f["series/se/series-two.json"] = langSeries("series-two", []string{"book-two"}, "")
				f["series/se/serie-un.json"] = langSeries("serie-un", []string{"book-un"},
					`"translation_of":["series-two","series-one"]`)
			},
			path: "series/0.json: entry serie-un",
			want: `translation_of must be sorted: "series-one" comes after "series-two"`,
		},
		// ordering / ordering_of.
		{
			name: "ordering_of target does not exist",
			mutate: func(f map[string]string) {
				f["series/se/series-one-chrono.json"] = langSeries("series-one-chrono", []string{"book-one"},
					`"ordering":"chronological","ordering_of":"series-nine"`)
			},
			path: "series/0.json: entry series-one-chrono",
			want: `ordering_of "series-nine" is no live series id`,
		},
		{
			name: "ordering_of target is retired",
			mutate: func(f map[string]string) {
				f["redirects.json"] = `{"people":{},"series":{"series-uno":"series-one"},"works":{}}`
				f["series/se/series-one-chrono.json"] = langSeries("series-one-chrono", []string{"book-one"},
					`"ordering":"chronological","ordering_of":"series-uno"`)
			},
			path: "series/0.json: entry series-one-chrono",
			want: `ordering_of "series-uno" is retired - point at "series-one"`,
		},
		{
			name: "ordering_of names the series itself",
			mutate: func(f map[string]string) {
				f["series/se/series-one-chrono.json"] = langSeries("series-one-chrono", []string{"book-one"},
					`"ordering":"chronological","ordering_of":"series-one-chrono"`)
			},
			path: "series/0.json: entry series-one-chrono",
			want: "ordering_of names the series itself",
		},
		{
			name: "ordering_of two hops",
			mutate: func(f map[string]string) {
				f["series/se/series-one-chrono.json"] = langSeries("series-one-chrono", []string{"book-one"},
					`"ordering":"chronological","ordering_of":"series-one"`)
				f["series/se/series-one-rec.json"] = langSeries("series-one-rec", []string{"book-one"},
					`"ordering":"recommended","ordering_of":"series-one-chrono"`)
			},
			path: "series/0.json: entry series-one-rec",
			want: `ordering_of "series-one-chrono" is itself a variant (of "series-one")`,
		},
		{
			name: "variant restates the primary's ordering",
			mutate: func(f map[string]string) {
				f["series/se/series-one.json"] = langSeries("series-one", []string{"book-one"}, `"ordering":"publication"`)
				f["series/se/series-one-too.json"] = langSeries("series-one-too", []string{"book-one"},
					`"ordering":"publication","ordering_of":"series-one"`)
			},
			path: "series/0.json: entry series-one-too",
			want: `series "series-one" and "series-one-too" both state the "publication" ordering of the family whose primary is "series-one"`,
		},
		{
			// Two VARIANTS in one order are the same duplicate, with no ordering on
			// the primary at all; the later id is reported against the earlier.
			name: "two variants in one ordering",
			mutate: func(f map[string]string) {
				f["series/se/series-one-b.json"] = langSeries("series-one-b", []string{"book-one"},
					`"ordering":"chronological","ordering_of":"series-one"`)
				f["series/se/series-one-a.json"] = langSeries("series-one-a", []string{"book-one"},
					`"ordering":"chronological","ordering_of":"series-one"`)
			},
			path: "series/0.json: entry series-one-b",
			want: `series "series-one-a" and "series-one-b" both state the "chronological" ordering`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			files := langTree()
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

// TestSeriesTranslationLanguageTieStandsDown: a series whose members split
// evenly between two languages has no derived language, so the same-language
// rule cannot be asked of it - on EITHER side of the link. A sync-bot volume
// that tips a linked series into a tie must not turn the link red.
func TestSeriesTranslationLanguageTieStandsDown(t *testing.T) {
	for name, series := range map[string]map[string]string{
		"tie on the translation": {
			"series/se/serie-mixte.json": langSeries("serie-mixte", []string{"book-un", "book-two"},
				`"translation_of":["series-one"]`),
		},
		"tie on the original": {
			"series/se/series-one.json": langSeries("series-one", []string{"book-one", "book-un"}, ""),
			"series/se/serie-en.json":   langSeries("serie-en", []string{"book-two"}, `"translation_of":["series-one"]`),
		},
	} {
		t.Run(name, func(t *testing.T) {
			files := langTree()
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
	files := langTree()
	files["series/se/series-one-chrono.json"] = langSeries("series-one-chrono",
		[]string{"book-two", "book-one", "book-two"}, `"ordering":"chronological","ordering_of":"series-one"`)
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
	files := langTree()
	files["works/bo/book-un/work.json"] = langWork("book-un", "fr", `"translation_of":["book-nine"]`)
	files["series/se/series-one-chrono.json"] = langSeries("series-one-chrono", []string{"book-one"},
		`"ordering":"chronological","ordering_of":"series-nine"`)
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
	files := langTree()
	files["works/bo/book-un/work.json"] = langWork("book-un", "fr", `"translation_of":["book-un"]`)
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
