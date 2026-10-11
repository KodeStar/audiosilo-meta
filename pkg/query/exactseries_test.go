package query

import (
	"context"
	"net/http"
	"reflect"
	"slices"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/artifacttest"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

const hpPrimary = "the-harry-potter-series"

// The same-named "Harry Potter" franchise in the order the exact-series rule
// ranks it: the primary, the two translations by id, the variant, then the
// partial match in its bm25 place.
var hpRanked = []string{
	hpPrimary, "harry-potter-german-edition", "harry-potter-spanish-edition",
	"harry-potter-chronological", "harry-potter-full-cast",
}

// seriesHitOptionalKeys are the omitempty languages fields of a series hit.
var seriesHitOptionalKeys = []string{"language", "ordering", "ordering_of", "ordering_of_name", "translation_of"}

// rowsOfKind returns the ids of a search page's rows of one kind, in page order.
func rowsOfKind(t *testing.T, base, path string, kind searchKind) []string {
	t.Helper()
	code, body := artifacttest.GetJSON(t, base, path)
	if code != http.StatusOK {
		t.Fatalf("GET %s: status %d, body %v", path, code, body)
	}
	var out []string
	for _, r := range body["results"].([]any) {
		m := r.(map[string]any)
		if m["kind"] == string(kind) {
			out = append(out, m["id"].(string))
		}
	}
	return out
}

// seriesRows returns the ids of the series rows of a search page, in page order.
func seriesRows(t *testing.T, base, path string) []string {
	t.Helper()
	return rowsOfKind(t, base, path, kindSeries)
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
	if got := rowsOfKind(t, ts.URL, "/api/v1/search?q=harry+potter", kindWork); !slices.Equal(got, want) {
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

// hpGroup is the same-named "Harry Potter" set in the order the group rule ranks
// it: the primary, the two translations by id, then the variant.
var hpGroup = hpRanked[:4]

// TestSameNamedSeriesLeadWithThePrimaryWhileTyping is the half-typed query: no
// whole-query lift fires on "harry pot" or "potter", but the four series named
// "Harry Potter" still tie in bm25, so the group is ranked inside its own slots -
// the primary first - on both surfaces, while the partial match and the works
// keep the slots they held on the unranked page.
func TestSameNamedSeriesLeadWithThePrimaryWhileTyping(t *testing.T) {
	db, ts := newTestServerForCatalog(t, artifacttest.SameNamedSeries())
	ctx := context.Background()
	for _, q := range []string{"harry", "potter", "harry pot", "Harry Pott"} {
		for _, kind := range []searchKind{kindAny, kindSeries} {
			plain, err := db.searchPage(ctx, kind, q, 20, nil)
			if err != nil {
				t.Fatal(err)
			}
			// The premise: unranked, the primary does not lead the group.
			if first := firstSeries(plain); first == hpPrimary {
				t.Fatalf("plain %q %q page leads with the primary: the fixture no longer shows the bug", kind, q)
			}
			got, err := db.search(ctx, kind, q, 20, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(plain) {
				t.Fatalf("search(%q, %q) has %d rows, the plain page %d", kind, q, len(got), len(plain))
			}
			var group []string
			for i := range got {
				sr, ok := got[i].(seriesResult)
				if ok && nameKey(sr.Name) == "harry potter" {
					group = append(group, sr.ID)
					if _, was := plain[i].(seriesResult); !was || nameKey(plain[i].(seriesResult).Name) != "harry potter" {
						t.Errorf("search(%q, %q) row %d: group row %s took a slot the group did not hold", kind, q, i, sr.ID)
					}
					continue
				}
				if !reflect.DeepEqual(got[i], plain[i]) {
					t.Errorf("search(%q, %q) row %d = %v, want the plain page's %v", kind, q, i, got[i], plain[i])
				}
			}
			if !slices.Equal(group, hpGroup) {
				t.Errorf("search(%q, %q) group = %v, want %v", kind, q, group, hpGroup)
			}
		}
	}
	for _, path := range []string{"/api/v1/series/search?q=harry+pot", "/api/v1/search?q=potter"} {
		if got := seriesRows(t, ts.URL, path); !slices.Equal(got, hpRanked) {
			t.Errorf("%s series rows = %v, want %v", path, got, hpRanked)
		}
	}
}

// firstSeries is the id of a page's first series row, "" when it holds none.
func firstSeries(page []any) string {
	for _, r := range page {
		if sr, ok := r.(seriesResult); ok {
			return sr.ID
		}
	}
	return ""
}

// TestSeriesRankingLeavesAPageWithoutAGroupAlone: a page naming no series outright
// and holding no two series of one name is the unranked page byte for byte -
// over the same-named fixture's queries that reach at most one of its names and
// over the main fixture, whose series names are all distinct.
func TestSeriesRankingLeavesAPageWithoutAGroupAlone(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		cat     func() *model.Catalog
		queries []string
	}{
		{artifacttest.SameNamedSeries, []string{"harry potter full", "full cast", "the", "a", "rowling", "stein"}},
		{artifacttest.Fixture, []string{"a", "the", "s", "har", "reacher", "jack", "saga", "book"}},
		{artifacttest.Languages, []string{"saga", "the", "die", "book", "buch"}},
	} {
		db := openTestDB(t, artifacttest.Build(t, tc.cat()))
		for _, q := range tc.queries {
			for _, kind := range []searchKind{kindAny, kindSeries} {
				want, err := db.searchPage(ctx, kind, q, 20, nil)
				if err != nil {
					t.Fatal(err)
				}
				if hasSameNamedGroup(want) {
					t.Fatalf("searchPage(%q, %q) holds a same-named group: not a no-group page", kind, q)
				}
				got, err := db.search(ctx, kind, q, 20, nil)
				if err != nil {
					t.Fatal(err)
				}
				if exactName(want, q) {
					continue // the whole-query lift may move it; that rule is pinned above
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("search(%q, %q) = %v, want the plain page %v", kind, q, got, want)
				}
			}
		}
	}
}

// hasSameNamedGroup reports whether two series rows of a page share a nameKey.
func hasSameNamedGroup(page []any) bool {
	seen := map[string]bool{}
	for _, r := range page {
		if sr, ok := r.(seriesResult); ok {
			k := nameKey(sr.Name)
			if seen[k] {
				return true
			}
			seen[k] = true
		}
	}
	return false
}

// exactName reports whether a page holds a series named q outright.
func exactName(page []any, q string) bool {
	for _, r := range page {
		if sr, ok := r.(seriesResult); ok && nameKey(sr.Name) == nameKey(q) {
			return true
		}
	}
	return false
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
	for id, want := range cases {
		got, ok := byID[id]
		if !ok {
			t.Errorf("no hit for %s", id)
			continue
		}
		for _, key := range seriesHitOptionalKeys {
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
		for _, key := range seriesHitOptionalKeys {
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
		{"no exact name, no group", []any{w("a"), partial, chrono}, "harry pot", []any{w("a"), partial, chrono}},
		{
			"a same-named group is ranked inside its own slots",
			[]any{chrono, w("a"), partial, de, p("p"), primary, w("b")},
			"harry pot",
			[]any{primary, w("a"), partial, de, p("p"), chrono, w("b")},
		},
		{
			"two groups are each ranked in their own slots",
			[]any{chrono, s("b-x", "X"), de, s("a-x", "x!"), primary},
			"harry",
			[]any{primary, s("a-x", "x!"), de, s("b-x", "X"), chrono},
		},
		{
			"a group beside the whole-query lift is ranked too",
			[]any{w("a"), s("b-x", "X"), partial, s("a-x", "X")},
			"harry potter full cast editions",
			[]any{w("a"), partial, s("a-x", "X"), s("b-x", "X")},
		},
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
