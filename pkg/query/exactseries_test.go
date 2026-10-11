package query

import (
	"context"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/artifacttest"
)

const hpPrimary = "the-harry-potter-series"

// The same-named "Harry Potter" franchise in the order the exact-series rule
// ranks it: the primary, the two translations by id, the variant, then the
// partial match in its bm25 place.
var hpRanked = []string{
	hpPrimary, "harry-potter-german-edition", "harry-potter-spanish-edition",
	"harry-potter-chronological", "harry-potter-full-cast",
}

// seriesRows returns the ids of the series rows of a search page, in page order.
func seriesRows(t *testing.T, base, path string) []string {
	t.Helper()
	code, body := artifacttest.GetJSON(t, base, path)
	if code != http.StatusOK {
		t.Fatalf("GET %s: status %d, body %v", path, code, body)
	}
	var out []string
	for _, r := range body["results"].([]any) {
		m := r.(map[string]any)
		if m["kind"] == "series" {
			out = append(out, m["id"].(string))
		}
	}
	return out
}

// TestExactSeriesNameRanksThePrimaryFirst is the reported bug: three series all
// named "Harry Potter" - the English original and its German and Spanish
// editions - plus a same-named reading-order variant. The primary leads the
// series rows on both pages that hold them, then the translations, then the
// variant, then the partial match.
func TestExactSeriesNameRanksThePrimaryFirst(t *testing.T) {
	db, ts := newTestServerForCatalog(t, artifacttest.SameNamedSeries())

	// The fixture's premise: WITHOUT the rule the primary is not first, because
	// identical names tie in bm25. A fixture where it already was would make the
	// assertions below vacuous.
	plain, err := db.ftsHits(context.Background(), kindSeries, ftsQuery("harry potter"), 20, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(plain) == 0 || plain[0].id == hpPrimary {
		t.Fatalf("plain FTS series page = %v: the fixture no longer shows the bug", plain)
	}

	for _, path := range []string{
		"/api/v1/series/search?q=harry+potter",
		"/api/v1/series/search?q=Harry+Potter!",
		"/api/v1/search?q=harry+potter",
	} {
		if got := seriesRows(t, ts.URL, path); !slices.Equal(got, hpRanked) {
			t.Errorf("%s series rows = %v, want %v", path, got, hpRanked)
		}
	}
}

// TestExactSeriesNameLeavesWorksAlone pins that the combined page's WORK rows are
// exactly what they were: the lift reorders series rows and nothing else.
func TestExactSeriesNameLeavesWorksAlone(t *testing.T) {
	db, ts := newTestServerForCatalog(t, artifacttest.SameNamedSeries())
	plain, err := db.ftsHits(context.Background(), kindAny, ftsQuery("harry potter"), 20, nil)
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, h := range plain {
		if h.kind == kindWork {
			want = append(want, h.id)
		}
	}
	var got []string
	for _, id := range pageIDs(t, ts.URL, "/api/v1/search?q=harry+potter") {
		if !strings.HasPrefix(id, "harry-potter") && id != hpPrimary {
			got = append(got, id)
		}
	}
	if !slices.Equal(got, want) {
		t.Errorf("work rows = %v, want the plain page's %v", got, want)
	}
}

// TestExactSeriesNameObeysTheLanguageFilter: the page is filtered at the source,
// so under lang=de the English primary is not on it and the German edition - the
// only exact match left - leads; nothing outside the filter reaches the page.
func TestExactSeriesNameObeysTheLanguageFilter(t *testing.T) {
	_, ts := newTestServerForCatalog(t, artifacttest.SameNamedSeries())
	for lang, want := range map[string][]string{
		"de": {"harry-potter-german-edition"},
		"es": {"harry-potter-spanish-edition"},
		"en": {hpPrimary, "harry-potter-chronological", "harry-potter-full-cast"},
	} {
		for _, path := range []string{
			"/api/v1/series/search?q=harry+potter&lang=" + lang,
			"/api/v1/search?q=harry+potter&lang=" + lang,
		} {
			if got := seriesRows(t, ts.URL, path); !slices.Equal(got, want) {
				t.Errorf("%s series rows = %v, want %v", path, got, want)
			}
		}
	}
}

// TestExactSeriesNameOnlyFiresOnAWholeName: a query that names no series outright
// moves nothing, so its page is the plain merged page byte for byte.
func TestExactSeriesNameOnlyFiresOnAWholeName(t *testing.T) {
	db := openTestDB(t, artifacttest.Build(t, artifacttest.SameNamedSeries()))
	ctx := context.Background()
	for _, q := range []string{"harry", "potter", "harry pot", "harry potter full", "the", "a"} {
		for _, kind := range []searchKind{kindAny, kindSeries} {
			got, err := db.search(ctx, kind, q, 20, nil)
			if err != nil {
				t.Fatal(err)
			}
			hits, err := db.ftsHits(ctx, kind, ftsQuery(q), 20, nil)
			if err != nil {
				t.Fatal(err)
			}
			boosted := db.boostedWorks(ctx, q, nil)
			if kind == kindSeries {
				boosted = nil
			}
			want, err := db.results(ctx, mergeHits(boosted, hits, 20))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("search(%q, %q) = %v, want the plain page %v", kind, q, got, want)
			}
		}
	}
}

// TestSeriesResultSaysWhatASeriesIs pins the additive series-hit fields: a
// translation names its original (the series detail's SeriesTranslation shape), a
// variant names its ordering and its primary, and the primary carries neither.
func TestSeriesResultSaysWhatASeriesIs(t *testing.T) {
	_, ts := newTestServerForCatalog(t, artifacttest.SameNamedSeries())
	_, body := artifacttest.GetJSON(t, ts.URL, "/api/v1/series/search?q=harry+potter")
	byID := map[string]map[string]any{}
	for _, r := range body["results"].([]any) {
		m := r.(map[string]any)
		byID[m["id"].(string)] = m
	}
	original := []any{map[string]any{"id": hpPrimary, "name": "Harry Potter", "language": "en"}}
	cases := map[string]map[string]any{
		hpPrimary:                      {"language": "en"},
		"harry-potter-german-edition":  {"language": "de", "translation_of": original},
		"harry-potter-spanish-edition": {"language": "es", "translation_of": original},
		"harry-potter-chronological": {
			"language": "en", "ordering": "chronological",
			"ordering_of": hpPrimary, "ordering_of_name": "Harry Potter",
		},
		"harry-potter-full-cast": {"language": "en"},
	}
	optional := []string{"language", "ordering", "ordering_of", "ordering_of_name", "translation_of"}
	for id, want := range cases {
		got, ok := byID[id]
		if !ok {
			t.Errorf("no hit for %s", id)
			continue
		}
		for _, key := range optional {
			if !reflect.DeepEqual(got[key], want[key]) {
				t.Errorf("%s.%s = %v, want %v", id, key, got[key], want[key])
			}
		}
	}
}

// TestExactSeriesNameOnAV6Artifact: an older artifact states no link, so every
// exact name is a primary and the lift is "exact names first, by id"; the hits
// carry none of the languages fields and nothing 500s.
func TestExactSeriesNameOnAV6Artifact(t *testing.T) {
	ts := downgradedServer(t, v6ShapedDB(t, artifacttest.SameNamedSeries()))
	want := []string{
		"harry-potter-chronological", "harry-potter-german-edition", "harry-potter-spanish-edition",
		hpPrimary, "harry-potter-full-cast",
	}
	if got := seriesRows(t, ts.URL, "/api/v1/series/search?q=harry+potter"); !slices.Equal(got, want) {
		t.Errorf("v6 series rows = %v, want %v", got, want)
	}
	_, body := artifacttest.GetJSON(t, ts.URL, "/api/v1/search?q=harry+potter")
	for _, r := range body["results"].([]any) {
		m := r.(map[string]any)
		if m["kind"] != "series" {
			continue
		}
		for _, key := range []string{"language", "ordering", "ordering_of", "ordering_of_name", "translation_of"} {
			if _, has := m[key]; has {
				t.Errorf("v6 series hit %s carries %s: %v", m["id"], key, m[key])
			}
		}
	}
}

func TestTierOf(t *testing.T) {
	of := []SeriesTranslation{{ID: "x", Name: "X"}}
	cases := []struct {
		r    seriesResult
		want seriesTier
	}{
		{seriesResult{}, tierPrimary},
		{seriesResult{Ordering: "publication"}, tierPrimary},
		{seriesResult{TranslationOf: of}, tierTranslation},
		{seriesResult{OrderingOf: "x"}, tierVariant},
		// A variant is a variant whatever else it states.
		{seriesResult{OrderingOf: "x", TranslationOf: of}, tierVariant},
	}
	for _, tc := range cases {
		if got := tierOf(tc.r); got != tc.want {
			t.Errorf("tierOf(%+v) = %d, want %d", tc.r, got, tc.want)
		}
	}
}

func TestRankSeriesRows(t *testing.T) {
	of := []SeriesTranslation{{ID: "hp", Name: "Harry Potter"}}
	w := func(id string) any { return workResult{Kind: kindWork, WorkCard: &WorkCard{ID: id}} }
	p := func(id string) any { return personResult{Kind: kindPerson, ID: id} }
	s := func(id, name string) seriesResult { return seriesResult{Kind: kindSeries, ID: id, Name: name} }
	primary := s("z-hp", "Harry Potter")
	de := s("hp-de", "Harry Potter")
	de.TranslationOf = of
	es := s("hp-es", "Harry Potter!")
	es.TranslationOf = of
	chrono := s("a-hp-chrono", "harry  potter")
	chrono.OrderingOf = "z-hp"
	partial := s("hp-full-cast", "Harry Potter (Full-Cast Editions)")

	cases := []struct {
		name string
		page []any
		q    string
		want []any
	}{
		{
			"the exact names take the first series slot, tiered",
			[]any{w("a"), partial, w("b"), chrono, de, p("p"), primary, es},
			"harry potter",
			[]any{w("a"), primary, de, es, chrono, partial, w("b"), p("p")},
		},
		{
			"already in order is unchanged",
			[]any{primary, de, es, chrono, partial},
			"Harry Potter",
			[]any{primary, de, es, chrono, partial},
		},
		{"no exact name", []any{w("a"), partial, chrono}, "harry pot", []any{w("a"), partial, chrono}},
		{"no series row", []any{w("a"), p("b")}, "harry potter", []any{w("a"), p("b")}},
		{"no term", []any{partial, primary}, "!!", []any{partial, primary}},
		{"empty page", nil, "harry potter", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := rankSeriesRows(tc.page, tc.q); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("rankSeriesRows = %v, want %v", got, tc.want)
			}
		})
	}
}
