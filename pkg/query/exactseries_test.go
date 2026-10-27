package query

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
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
	plain, err := db.ftsHits(context.Background(), kindSeries, ftsQuery("harry potter"), 20, nil, "")
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
	plain, err := db.ftsHits(context.Background(), kindAny, ftsQuery("harry potter"), 20, nil, "")
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
// it: the primary, the two translations (in the page's order, which on this
// fixture is id order), then the variant.
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
			plain, err := db.searchPage(ctx, kind, q, 20, nil, "")
			if err != nil {
				t.Fatal(err)
			}
			// The premise: unranked, the primary does not lead the group.
			if first := firstSeries(plain); first == hpPrimary {
				t.Fatalf("plain %q %q page leads with the primary: the fixture no longer shows the bug", kind, q)
			}
			got, err := db.search(ctx, kind, q, 20, nil, "")
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
				want, err := db.searchPage(ctx, kind, q, 20, nil, "")
				if err != nil {
					t.Fatal(err)
				}
				if hasSameNamedGroup(want) {
					t.Fatalf("searchPage(%q, %q) holds a same-named group: not a no-group page", kind, q)
				}
				got, err := db.search(ctx, kind, q, 20, nil, "")
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
		// A link to a series outside the group demotes nothing.
		{seriesResult{TranslationOf: []SeriesTranslation{{ID: "absent"}}}, tierPrimary},
		{seriesResult{TranslationOf: []SeriesTranslation{{ID: "absent"}, {ID: "x"}}}, tierTranslation},
		{seriesResult{OrderingOf: "absent"}, tierPrimary},
		{seriesResult{OrderingOf: "absent", TranslationOf: of}, tierTranslation},
	}
	for _, tc := range cases {
		if got := tierOf(tc.r, map[string]bool{"x": true}); got != tc.want {
			t.Errorf("tierOf(%+v) = %d, want %d", tc.r, got, tc.want)
		}
	}
}

func TestRankSeriesRows(t *testing.T) {
	of := []SeriesTranslation{{ID: "z-hp", Name: "Harry Potter"}}
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
			"two groups, each ranked by tier in their own slots",
			[]any{chrono, s("b-x", "X"), es, s("a-x", "x!"), de, primary},
			"harry",
			// es before de: one tier keeps the page's order, not id order.
			[]any{primary, s("b-x", "X"), es, s("a-x", "x!"), de, chrono},
		},
		{
			"an all-primary group is left as it came",
			[]any{w("a"), s("b-x", "Reapers MC"), partial, s("a-x", "Reaper's MC")},
			"reaper",
			[]any{w("a"), s("b-x", "Reapers MC"), partial, s("a-x", "Reaper's MC")},
		},
		{
			"the lift breaks ties by id, a group beside it by page order",
			[]any{w("a"), s("b-x", "X"), es, s("a-x", "X"), de},
			"x",
			[]any{w("a"), s("a-x", "X"), s("b-x", "X"), es, de},
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

// A link only justifies demotion when its target shares the group's name and
// is on this page. Check both the exact-name lift and the half-typed query.
func TestSeriesRankingRequiresATargetInTheGroup(t *testing.T) {
	primary := seriesResult{ID: "z-primary", Name: "Reapers MC"}
	unrelated := seriesResult{ID: "unrelated", Name: "Reapers MC"}
	otherName := seriesResult{ID: primary.ID, Name: "Other Series"}
	work := workResult{Kind: kindWork, WorkCard: &WorkCard{ID: "work"}}
	for _, link := range []string{"translation", "ordering"} {
		related := seriesResult{ID: "a-related", Name: "Reaper's MC"}
		if link == "translation" {
			related.TranslationOf = []SeriesTranslation{{ID: "absent"}, {ID: primary.ID}}
		} else {
			related.OrderingOf = primary.ID
		}
		for _, q := range []string{"reaper", "reapers mc"} {
			for _, tc := range []struct {
				name string
				page []any
				want []any
			}{
				{"target in group", []any{related, primary}, []any{primary, related}},
				{"target absent", []any{related, unrelated}, []any{related, unrelated}},
				{"target has another name", []any{related, unrelated, otherName}, []any{related, unrelated, otherName}},
				{"non-series target", []any{related, unrelated, personResult{ID: primary.ID}}, []any{related, unrelated, personResult{ID: primary.ID}}},
			} {
				t.Run(link+"/"+q+"/"+tc.name, func(t *testing.T) {
					page := append([]any{work}, tc.page...)
					before := slices.Clone(page)
					want := append([]any{work}, tc.want...)
					if got := rankSeriesRows(page, q); !reflect.DeepEqual(got, want) {
						t.Errorf("rankSeriesRows = %v, want %v", got, want)
					}
					if !reflect.DeepEqual(page, before) {
						t.Error("ranking mutated the input page")
					}
				})
			}
		}
	}
}

// TestSmallWindowPullsTheSameNamedPrimary is the gap the ranking alone left: a
// window smaller than the tie holds translations and the variant of "Harry
// Potter" and not the primary, and with their target off the page the
// group-relative tiers rank those rows as primaries, so a translation led. The
// page's rows name the primary (translation_of, ordering_of) under the same
// name, so it is read in one batch, joins the group ahead of them and the last
// row drops so the limit holds - on both surfaces, for the whole name and while
// it is typed.
func TestSmallWindowPullsTheSameNamedPrimary(t *testing.T) {
	db, ts := newTestServerForCatalog(t, artifacttest.SameNamedSeries())
	ctx := context.Background()
	for limit, want := range map[int][]string{
		1: {hpPrimary},
		2: {hpPrimary, "harry-potter-german-edition"},
		3: {hpPrimary, "harry-potter-german-edition", "harry-potter-spanish-edition"},
	} {
		for _, kind := range []searchKind{kindAny, kindSeries} {
			// The premise: the window holds no primary before the pull.
			plain, err := db.searchPage(ctx, kind, "harry potter", limit, nil, "")
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range plain {
				if sr, ok := r.(seriesResult); ok && sr.ID == hpPrimary {
					t.Fatalf("plain %q page at limit %d holds the primary: the fixture no longer shows the gap", kind, limit)
				}
			}
		}
		for _, path := range []string{
			fmt.Sprintf("/api/v1/series/search?q=harry+potter&limit=%d", limit),
			fmt.Sprintf("/api/v1/search?q=harry+potter&limit=%d", limit),
		} {
			_, body := artifacttest.GetJSON(t, ts.URL, path)
			if n := len(body["results"].([]any)); n != limit {
				t.Errorf("%s holds %d rows, want the limit %d", path, n, limit)
			}
			if got := seriesRows(t, ts.URL, path); !slices.Equal(got, want) {
				t.Errorf("%s series rows = %v, want %v", path, got, want)
			}
		}
	}
	// A half-typed name pulls it too, from a lone row (the variant alone at
	// limit=1) as from a set of them.
	for path, want := range map[string][]string{
		"/api/v1/series/search?q=harry+pot&limit=1": {hpPrimary},
		"/api/v1/search?q=harry+pot&limit=1":        {hpPrimary},
		"/api/v1/series/search?q=potter&limit=1":    {hpPrimary},
		"/api/v1/series/search?q=harry+pot&limit=2": {hpPrimary, "harry-potter-german-edition"},
		"/api/v1/search?q=potter&limit=2":           {hpPrimary, "harry-potter-german-edition"},
	} {
		if got := seriesRows(t, ts.URL, path); !slices.Equal(got, want) {
			t.Errorf("%s series rows = %v, want %v", path, got, want)
		}
	}
}

// TestSmallWindowPullObeysTheLanguageFilter: under lang=de the English primary
// is outside the filter, so it is neither pulled nor even read - the German
// edition, the only exact match the reader asked for, leads; under lang=en the
// variant's primary is English and is pulled.
func TestSmallWindowPullObeysTheLanguageFilter(t *testing.T) {
	db, ts := newTestServerForCatalog(t, artifacttest.SameNamedSeries())
	ctx := context.Background()
	for limit := 1; limit <= 3; limit++ {
		for _, kind := range []searchKind{kindAny, kindSeries} {
			page, err := db.searchPage(ctx, kind, "harry potter", limit, langFilter{"de"}, "")
			if err != nil {
				t.Fatal(err)
			}
			if pulls := planPrimaryPulls(rankSeriesRows(page, "harry potter"), langFilter{"de"}); pulls != nil {
				t.Errorf("lang=de %q page at limit %d plans a read: %+v", kind, limit, pulls)
			}
		}
		for _, path := range []string{
			fmt.Sprintf("/api/v1/series/search?q=harry+potter&lang=de&limit=%d", limit),
			fmt.Sprintf("/api/v1/search?q=harry+potter&lang=de&limit=%d", limit),
		} {
			if got, want := seriesRows(t, ts.URL, path), []string{"harry-potter-german-edition"}; !slices.Equal(got, want) {
				t.Errorf("%s series rows = %v, want %v", path, got, want)
			}
		}
	}
	for _, path := range []string{
		"/api/v1/series/search?q=harry+potter&lang=en&limit=1",
		"/api/v1/search?q=harry+potter&lang=en&limit=1",
	} {
		if got, want := seriesRows(t, ts.URL, path), []string{hpPrimary}; !slices.Equal(got, want) {
			t.Errorf("%s series rows = %v, want %v", path, got, want)
		}
	}
}

// TestSmallWindowDoesNotPullADifferentlyNamedPrimary: "Die Saga" translates "The
// Saga" and "The Saga (Chronological Order)" is a variant of it - a link, but to
// another NAME, which is another answer to another query. Neither is pulled, and
// nothing is read.
func TestSmallWindowDoesNotPullADifferentlyNamedPrimary(t *testing.T) {
	db, ts := newTestServerForCatalog(t, artifacttest.Languages())
	ctx := context.Background()
	for q, want := range map[string]string{
		"die saga":                       "die-saga",
		"the saga (chronological order)": "saga-chronological",
	} {
		for _, kind := range []searchKind{kindAny, kindSeries} {
			page, err := db.searchPage(ctx, kind, q, 1, nil, "")
			if err != nil {
				t.Fatal(err)
			}
			if pulls := planPrimaryPulls(rankSeriesRows(page, q), nil); pulls != nil {
				t.Errorf("%q %q page plans a read: %+v", kind, q, pulls)
			}
		}
		path := "/api/v1/series/search?limit=1&q=" + url.QueryEscape(q)
		if got := seriesRows(t, ts.URL, path); !slices.Equal(got, []string{want}) {
			t.Errorf("%s series rows = %v, want [%s]", path, got, want)
		}
	}
}

// TestOrdinaryPagesPlanNoPrimaryRead pins the cost: every default-window page of
// the fixtures' queries - the Harry Potter pages, whose primary the window holds,
// included - plans no pull, so the search issues no read beyond the page's own.
func TestOrdinaryPagesPlanNoPrimaryRead(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		cat     func() *model.Catalog
		queries []string
	}{
		{artifacttest.SameNamedSeries, []string{"harry potter", "harry pot", "potter", "full cast", "the", "rowling"}},
		{artifacttest.Fixture, []string{"a", "the", "s", "har", "reacher", "jack", "saga", "book"}},
		{artifacttest.Languages, []string{"saga", "the saga", "die saga", "the", "die", "book", "buch"}},
	} {
		db := openTestDB(t, artifacttest.Build(t, tc.cat()))
		for _, q := range tc.queries {
			for _, kind := range []searchKind{kindAny, kindSeries} {
				for _, lang := range []langFilter{nil, {"de"}, {"en"}} {
					page, err := db.searchPage(ctx, kind, q, 20, lang, "")
					if err != nil {
						t.Fatal(err)
					}
					if pulls := planPrimaryPulls(rankSeriesRows(page, q), lang); pulls != nil {
						t.Errorf("%q %q lang=%v plans a read: %+v", kind, q, lang, pulls)
					}
				}
			}
		}
	}
}

// TestPullDegradesToTheRankedPage: a read that fails (here a cancelled context)
// serves the ranked page as it came rather than failing the search.
func TestPullDegradesToTheRankedPage(t *testing.T) {
	db := openTestDB(t, artifacttest.Build(t, artifacttest.SameNamedSeries()))
	page, err := db.searchPage(context.Background(), kindSeries, "harry potter", 1, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	ranked := rankSeriesRows(page, "harry potter")
	// The premise: this page plans a read, so the failure path is the one taken
	// rather than the no-plan return, which would pass this test unread.
	if planPrimaryPulls(ranked, nil) == nil {
		t.Fatal("the limit=1 page plans no pull: the fixture no longer reaches the read")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := db.pullPrimaries(ctx, ranked, "harry potter", 1, nil); !reflect.DeepEqual(got, ranked) {
		t.Errorf("pullPrimaries on a failed read = %v, want the ranked page %v", got, ranked)
	}
}

func TestPlanAndPlacePrimaries(t *testing.T) {
	w := func(id string) any { return workResult{Kind: kindWork, WorkCard: &WorkCard{ID: id}} }
	s := func(id, name, lang string) seriesResult {
		return seriesResult{Kind: kindSeries, ID: id, Name: name, Language: lang}
	}
	of := func(r seriesResult, id, name, lang string) seriesResult {
		r.TranslationOf = append(r.TranslationOf, SeriesTranslation{ID: id, Name: name, Language: lang})
		return r
	}
	variant := func(r seriesResult, id, name string) seriesResult {
		r.OrderingOf, r.OrderingOfName = id, name
		return r
	}
	hp := s("hp", "Harry Potter", "en")
	de := of(s("hp-de", "Harry Potter", "de"), "hp", "Harry Potter", "en")
	es := of(s("hp-es", "Harry Potter", "es"), "hp", "Harry Potter", "en")
	chrono := variant(s("hp-chrono", "Harry Potter", "en"), "hp", "Harry Potter")
	x := s("x", "X", "en")
	xde := of(s("x-de", "X", "de"), "x", "X", "en")
	xvar := variant(s("x-var", "X", "en"), "x", "X")
	diesaga := of(s("die-saga", "Die Saga", "de"), "the-saga", "The Saga", "en")
	// A translation of a differently-named series ranks as a primary of its own
	// name (tierOf judges a link against the same-named rows), and the pull reads
	// it the same way.
	hpOther := of(s("hp-other", "Harry Potter", "de"), "hpc", "Harry Potter Collection", "en")
	// An unrelated series of the name, stating no link: a primary of the name,
	// whose id sorts after the German edition's.
	fan := s("potter-fan", "Harry Potter", "en")
	// A reading-order variant of the German edition, whose id sorts ahead of
	// the original's: a translation's variant, two hops from the primary.
	chronoDe := variant(s("chrono-de", "Harry Potter", "de"), "hp-de", "Harry Potter")
	// Both reads' answers: the first returns hp-de for chronoDe, the second hp.
	fetched := map[string]seriesResult{"hp": hp, "x": x, "hp-de": de}

	cases := []struct {
		name  string
		page  []any
		q     string
		lang  langFilter
		limit int
		plan  []primaryPull
		want  []any
	}{
		{
			"the exact block of one row pulls its primary in its place",
			[]any{de}, "harry potter", nil, 1,
			[]primaryPull{{slot: 0, key: "harry potter", ids: []string{"hp"}}},
			[]any{hp},
		},
		{
			"the set shifts down and the page's last row drops",
			[]any{w("a"), de, chrono, w("b")}, "harry", nil, 4,
			[]primaryPull{{slot: 1, key: "harry potter", ids: []string{"hp"}}},
			[]any{w("a"), hp, de, chrono},
		},
		{
			"a short page grows by the primary",
			[]any{de, es}, "harry", nil, 20,
			[]primaryPull{{slot: 0, key: "harry potter", ids: []string{"hp"}}},
			[]any{hp, de, es},
		},
		{
			"two sets each pull their own",
			[]any{de, chrono, xde, xvar}, "potter x", nil, 4,
			[]primaryPull{
				{slot: 0, key: "harry potter", ids: []string{"hp"}},
				{slot: 2, key: "x", ids: []string{"x"}},
			},
			[]any{hp, de, chrono, x},
		},
		{"a set holding the primary its rows name pulls nothing", []any{de, hp}, "harry", nil, 2, nil, []any{de, hp}},
		{
			// Ranked alone the two are tier 0 (hp is off the page), so by id the
			// German edition led; another primary of the name says nothing about
			// the series it translates.
			"an unrelated primary of the name does not block the pull",
			[]any{de, fan}, "harry potter", nil, 2,
			[]primaryPull{{slot: 0, key: "harry potter", ids: []string{"hp"}}},
			[]any{hp, fan},
		},
		{
			"a translation of another name does not block the pull either",
			[]any{de, hpOther}, "harry", nil, 2,
			[]primaryPull{{slot: 0, key: "harry potter", ids: []string{"hp"}}},
			[]any{hp, hpOther},
		},
		{
			// The first read returns hp-de, no primary of the name (it translates
			// hp); the second returns hp. Both join, so the variant ranks behind
			// the translation it is a view of and the translation behind hp,
			// whatever the ids say - placed alone, hp would tie the variant at
			// tier 0 and lose to its id.
			"a translation's variant pulls the original in two hops",
			[]any{chronoDe}, "harry potter", nil, 3,
			[]primaryPull{{slot: 0, key: "harry potter", ids: []string{"hp-de"}}},
			[]any{hp, de, chronoDe},
		},
		{
			"a translation's variant at the limit leaves the original alone",
			[]any{chronoDe}, "harry pot", nil, 1,
			[]primaryPull{{slot: 0, key: "harry potter", ids: []string{"hp-de"}}},
			[]any{hp},
		},
		{
			// Under lang=de the English original is outside the filter, so hp-de's
			// link to it is no same-named link: hp-de is the primary of its name,
			// pulled for its variant, and leads.
			"under a filter the translation is the primary its variant pulls",
			[]any{chronoDe}, "harry potter", langFilter{"de"}, 2,
			[]primaryPull{{slot: 0, key: "harry potter", ids: []string{"hp-de"}}},
			[]any{de, chronoDe},
		},
		{
			// The original is on the page, the translation between them is not:
			// ranked alone the variant and the original tie at tier 0 and the
			// variant's id leads. The translation joins on the first read alone.
			"a translation joins between its variant and an original on the page",
			[]any{chronoDe, hp}, "harry potter", nil, 3,
			[]primaryPull{{slot: 0, key: "harry potter", ids: []string{"hp-de"}}},
			[]any{hp, de, chronoDe},
		},
		{
			// Before the pull the exact block ranks both rows tier 0 (their target
			// is off the page), so by id the variant leads; with the primary in the
			// group the translation is tier 1 and the variant tier 2.
			"the pulled primary joins the group, so its translation leads its variant",
			[]any{chrono, de}, "harry potter", nil, 2,
			[]primaryPull{{slot: 0, key: "harry potter", ids: []string{"hp"}}},
			[]any{hp, de},
		},
		{
			"a half-typed set is ranked by tier once its primary joins",
			[]any{chrono, w("a"), de}, "harry", nil, 3,
			[]primaryPull{{slot: 0, key: "harry potter", ids: []string{"hp"}}},
			[]any{hp, de, w("a")},
		},
		{
			"a lone row of a typed-at name pulls its primary",
			[]any{chrono}, "harry pot", nil, 1,
			[]primaryPull{{slot: 0, key: "harry potter", ids: []string{"hp"}}},
			[]any{hp},
		},
		{"a primary already on the page is not read again", []any{de, w("a"), es, hp}, "harry", nil, 4, nil, []any{de, w("a"), es, hp}},
		{"a differently named original is not pulled", []any{diesaga}, "die saga", nil, 1, nil, []any{diesaga}},
		{"an original outside the filter is not even planned", []any{de}, "harry potter", langFilter{"de"}, 1, nil, []any{de}},
		{
			"a variant's primary outside the filter is planned, read and refused",
			[]any{chrono}, "harry potter", langFilter{"de"}, 1,
			[]primaryPull{{slot: 0, key: "harry potter", ids: []string{"hp"}}},
			[]any{chrono},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan := planPrimaryPulls(tc.page, tc.lang)
			if !reflect.DeepEqual(plan, tc.plan) {
				t.Fatalf("planPrimaryPulls = %+v, want %+v", plan, tc.plan)
			}
			if got := placePrimaries(tc.page, plan, fetched, tc.q, tc.limit, tc.lang); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("placePrimaries = %v, want %v", got, tc.want)
			}
		})
	}

	// A translation whose own original did not come back as a primary of the
	// name - a third hop, never read - is not placed, nor is anything for it.
	for _, orig := range []seriesResult{
		of(s("hp", "Harry Potter", "en"), "older", "Harry Potter", "en"),
		s("hp", "Harry Potter", "fr"),
	} {
		got := placePrimaries([]any{chronoDe}, []primaryPull{{slot: 0, key: "harry potter", ids: []string{"hp-de"}}},
			map[string]seriesResult{"hp-de": de, "hp": orig}, "harry potter", 2, langFilter{"de", "en"})
		if !reflect.DeepEqual(got, []any{chronoDe}) {
			t.Errorf("placePrimaries with the original %+v = %v, want the page unchanged", orig, got)
		}
	}

	// A fetched row that is no longer a primary of the set's name, no longer of
	// that name, or outside the filter is not placed.
	pull := []primaryPull{{slot: 0, key: "harry potter", ids: []string{"hp"}}}
	for _, stale := range []seriesResult{
		of(s("hp", "Harry Potter", "en"), "other", "Harry Potter", "en"),
		s("hp", "Harry Potter Collection", "en"),
		variant(s("hp", "Harry Potter", "en"), "other", "Harry Potter"),
	} {
		if got := placePrimaries([]any{de}, pull, map[string]seriesResult{"hp": stale}, "harry potter", 1, nil); !reflect.DeepEqual(got, []any{de}) {
			t.Errorf("placePrimaries with %+v = %v, want the page unchanged", stale, got)
		}
	}
}

// TestSecondHopReadsOnlyWhatTheFirstCannotPlace pins the second read's bound:
// it is asked only for a first-read row that is no primary of its name, only
// for that row's same-named links, and never for one the page or the first read
// already holds or the filter puts outside it.
func TestSecondHopReadsOnlyWhatTheFirstCannotPlace(t *testing.T) {
	s := func(id, lang string) seriesResult {
		return seriesResult{Kind: kindSeries, ID: id, Name: "Harry Potter", Language: lang}
	}
	hp := s("hp", "en")
	de := s("hp-de", "de")
	de.TranslationOf = []SeriesTranslation{{ID: "hp", Name: "Harry Potter", Language: "en"}}
	chronoDe := s("chrono-de", "de")
	chronoDe.OrderingOf, chronoDe.OrderingOfName = "hp-de", "Harry Potter"
	pullDe := []primaryPull{{slot: 0, key: "harry potter", ids: []string{"hp-de"}}}
	pullHp := []primaryPull{{slot: 0, key: "harry potter", ids: []string{"hp"}}}
	for _, tc := range []struct {
		name    string
		page    []any
		pulls   []primaryPull
		fetched map[string]seriesResult
		lang    langFilter
		want    []string
	}{
		{"a translation's original", []any{chronoDe}, pullDe, map[string]seriesResult{"hp-de": de}, nil, []string{"hp"}},
		{"an original outside the filter", []any{chronoDe}, pullDe, map[string]seriesResult{"hp-de": de}, langFilter{"de"}, nil},
		{"an original on the page", []any{chronoDe, hp}, pullDe, map[string]seriesResult{"hp-de": de}, nil, nil},
		{"an original the first read returned", []any{chronoDe}, pullDe, map[string]seriesResult{"hp-de": de, "hp": hp}, nil, nil},
		{"a primary", []any{de}, pullHp, map[string]seriesResult{"hp": hp}, nil, nil},
		{"a row that did not come back", []any{chronoDe}, pullDe, map[string]seriesResult{}, nil, nil},
	} {
		if got := secondHop(tc.page, tc.pulls, tc.fetched, tc.lang); !slices.Equal(got, tc.want) {
			t.Errorf("%s: secondHop = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestPullFollowsATranslationsVariantToItsOriginal runs both reads over a real
// artifact: a reading-order variant of the German edition, alone on a page,
// names the German edition, which translates the English original under the
// same name. With no filter the second read brings the original and it leads -
// its id sorts after the variant's, so only the translation joining between them
// puts it there; under lang=de the German edition is the primary of its name and
// leads its variant.
func TestPullFollowsATranslationsVariantToItsOriginal(t *testing.T) {
	cat := artifacttest.SameNamedSeries()
	const deChrono = "harry-potter-german-chronological"
	cat.Series = append(cat.Series, &model.Series{
		ID: deChrono, Name: "Harry Potter", License: "CC0-1.0", Authors: []string{"j-k-rowling"},
		Works:    []model.SeriesWork{{Work: "stein-der-weisen", Position: "1"}},
		Ordering: model.OrderingChronological, OrderingOf: "harry-potter-german-edition",
	})
	db := openTestDB(t, artifacttest.Build(t, cat))
	ctx := context.Background()
	rows, err := db.seriesResultsByID(ctx, []string{deChrono})
	if err != nil {
		t.Fatal(err)
	}
	page := []any{rows[deChrono]}
	for _, tc := range []struct {
		lang  langFilter
		limit int
		want  []string
	}{
		{nil, 1, []string{hpPrimary}},
		{nil, 3, []string{hpPrimary, "harry-potter-german-edition", deChrono}},
		{langFilter{"de"}, 1, []string{"harry-potter-german-edition"}},
		{langFilter{"de"}, 2, []string{"harry-potter-german-edition", deChrono}},
	} {
		var got []string
		for _, r := range db.pullPrimaries(ctx, page, "harry potter", tc.limit, tc.lang) {
			got = append(got, r.(seriesResult).ID)
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("lang=%v limit=%d: pulled page = %v, want %v", tc.lang, tc.limit, got, tc.want)
		}
	}
}
