package query

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/artifacttest"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// langFilterCatalog is artifacttest.Languages plus the shapes the ?lang= filter has to
// judge: a work carrying a REGIONAL tag (buch-zwei, de-at), a series whose members
// TIE between two languages (mixed-saga, which therefore derives none), and four
// "Saga Tales" editions by two authors in three languages for the Audiobookshelf
// ranking (author dominant, then language).
func langFilterCatalog() *model.Catalog {
	cat := artifacttest.Languages()
	cat.People = append(cat.People, &model.Person{ID: "max-muster", Name: "Max Muster", License: "CC0-1.0"})
	work := func(id, title, lang, author string) *model.Work {
		return &model.Work{ID: id, Title: title, Language: lang, Authors: []string{author}, License: "CC0-1.0"}
	}
	cat.Works = append(cat.Works,
		work("buch-zwei", "Buch Zwei", "de-at", "jane-doe"),
		work("saga-tales", "Saga Tales", "en", "jane-doe"),
		work("saga-tales-de", "Saga Tales", "de", "max-muster"),
		work("saga-tales-fr", "Saga Tales", "fr", "max-muster"),
		work("saga-tales-jane-de", "Saga Tales", "de", "jane-doe"),
	)
	cat.Series = append(cat.Series, &model.Series{
		ID: "mixed-saga", Name: "Mixed Saga", License: "CC0-1.0",
		Works: []model.SeriesWork{{Work: "book-two", Position: "1"}, {Work: "buch-zwei", Position: "2"}},
	})
	return cat
}

// getRaw fetches path and returns the status and the body exactly as sent.
func getRaw(t *testing.T, base, path string) (int, []byte) {
	t.Helper()
	resp, err := http.Get(base + path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, body
}

// TestUnscopedABSSearchIsUnchanged pins the unscoped /abs/search bytes to goldens
// captured BEFORE the per-language route existed: the language ranking is the
// new route's alone, and an Audiobookshelf server configured with the plain base
// URL must see exactly what it always saw.
func TestUnscopedABSSearchIsUnchanged(t *testing.T) {
	_, ts := newTestServerForCatalog(t, langFilterCatalog())
	for _, tc := range []struct{ name, path string }{
		{"abs-unscoped-author.json", "/abs/search?mediaType=book&query=saga+tales&author=Jane+Doe"},
		{"abs-unscoped-plain.json", "/abs/search?mediaType=book&query=saga"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, body := getRaw(t, ts.URL, tc.path)
			if code != http.StatusOK {
				t.Fatalf("status = %d, body %s", code, body)
			}
			assertGolden(t, tc.name, body)
		})
	}
}

// TestParseLangFilter pins the one parser: lowercased, trimmed, reduced to the
// primary subtag, deduplicated and sorted, empty items skipped; a non-tag item is
// an error naming it, and so is a ninth language.
func TestParseLangFilter(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want langFilter
	}{
		{"", nil},
		{" , ,", nil},
		{"de", langFilter{"de"}},
		{"DE", langFilter{"de"}},
		{" en , de ", langFilter{"de", "en"}},
		{"de-AT", langFilter{"de"}},
		{"de,de-at,de-ch", langFilter{"de"}},
		{"pt-br,en", langFilter{"en", "pt"}},
		{"fil", langFilter{"fil"}},
		{"aa,ab,ac,ad,ae,af,ag,ah", langFilter{"aa", "ab", "ac", "ad", "ae", "af", "ag", "ah"}},
		// Nine spellings of eight languages is still eight.
		{"aa,ab,ac,ad,ae,af,ag,ah,ah-x1", langFilter{"aa", "ab", "ac", "ad", "ae", "af", "ag", "ah"}},
	} {
		got, err := parseLangFilter(tc.raw)
		if err != nil {
			t.Errorf("parseLangFilter(%q): %v", tc.raw, err)
			continue
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("parseLangFilter(%q) = %v, want %v", tc.raw, got, tc.want)
		}
	}
	for _, tc := range []struct{ raw, names string }{
		{"english", `"english"`},
		{"de,e", `"e"`},
		{"de_AT", `"de_AT"`},
		{"de-", `"de-"`},
		{"'de'", `"'de'"`},
		{"aa,ab,ac,ad,ae,af,ag,ah,ai", "at most 8"},
		// The raw item count is bounded BEFORE validation, so a value repeating
		// one language past maxLangItems is refused without matching every item.
		{strings.Repeat("de,", maxLangItems), "at most 8"},
	} {
		_, err := parseLangFilter(tc.raw)
		if err == nil || !strings.Contains(err.Error(), tc.names) {
			t.Errorf("parseLangFilter(%q) error = %v, want one naming %s", tc.raw, err, tc.names)
		}
	}
}

// TestFTSSearchQueryWithoutAFilterIsUnchanged pins that a request naming no
// language issues EXACTLY the SQL it always did - the two constants, with their
// arguments - so the filter costs an unfiltered search nothing.
func TestFTSSearchQueryWithoutAFilterIsUnchanged(t *testing.T) {
	q, args := ftsSearchQuery(kindAny, `"x"*`, 20, nil)
	if q != searchSQL || len(args) != 2 {
		t.Errorf("unscoped, unfiltered = %q %v", q, args)
	}
	q, args = ftsSearchQuery(kindWork, `"x"*`, 20, nil)
	if q != searchKindSQL || len(args) != 3 {
		t.Errorf("scoped, unfiltered = %q %v", q, args)
	}
	if q, _ := (&DB{}).latestCandidatesQuery(nil, 200); q != latestCandidatesSQL {
		t.Errorf("latest, unfiltered = %q", q)
	}
	// And a filtered query binds exactly as many arguments as it has markers.
	for _, kind := range []searchKind{kindAny, kindWork} {
		q, args := ftsSearchQuery(kind, `"x"*`, 20, langFilter{"de", "en"})
		if n := strings.Count(q, "?"); n != len(args) {
			t.Errorf("%q filtered: %d markers, %d args", kind, n, len(args))
		}
	}
}

// pageIDs is the "id" of every search result, in order.
func pageIDs(t *testing.T, base, path string) []string {
	t.Helper()
	code, body := artifacttest.GetJSON(t, base, path)
	if code != http.StatusOK {
		t.Fatalf("GET %s: status %d, body %v", path, code, body)
	}
	return ids(t, body["results"])
}

func sameSet(got []string, want ...string) bool {
	g, w := slices.Clone(got), slices.Clone(want)
	slices.Sort(g)
	slices.Sort(w)
	return slices.Equal(g, w)
}

// TestSearchFiltersByLanguage covers the four searches: a work outside the filter
// is excluded, a REGIONAL tag (buch-zwei, de-at) is matched by its primary subtag
// whichever side spells the region, a person always passes the combined search,
// a series is matched by its derived language and a TIED series passes every
// filter, and people/search ignores the parameter.
func TestSearchFiltersByLanguage(t *testing.T) {
	_, ts := newTestServerForCatalog(t, langFilterCatalog())

	for _, lang := range []string{"de", "de-at", "DE-CH"} {
		if got := pageIDs(t, ts.URL, "/api/v1/works/search?q=buch&lang="+lang); !sameSet(got, "buch-eins", "buch-zwei") {
			t.Errorf("works/search?q=buch&lang=%s = %v, want buch-eins and the de-at buch-zwei", lang, got)
		}
	}
	if got := pageIDs(t, ts.URL, "/api/v1/works/search?q=buch&lang=en"); len(got) != 0 {
		t.Errorf("works/search?q=buch&lang=en = %v, want nothing", got)
	}
	// A language the catalogue does not hold filters to nothing, not to a 400.
	if got := pageIDs(t, ts.URL, "/api/v1/works/search?q=buch&lang=ja"); len(got) != 0 {
		t.Errorf("works/search?q=buch&lang=ja = %v, want nothing", got)
	}
	// Two languages are a union.
	if got := pageIDs(t, ts.URL, "/api/v1/works/search?q=saga+tales&lang=fr,en"); !sameSet(got, "saga-tales", "saga-tales-fr") {
		t.Errorf("works/search?q=saga+tales&lang=fr,en = %v", got)
	}
	// A repeated parameter is the same list.
	if got := pageIDs(t, ts.URL, "/api/v1/works/search?q=saga+tales&lang=fr&lang=en"); !sameSet(got, "saga-tales", "saga-tales-fr") {
		t.Errorf("works/search?q=saga+tales&lang=fr&lang=en = %v", got)
	}

	// The combined search: Jane Doe has no language and passes; of her works only
	// the French one is left.
	if got := pageIDs(t, ts.URL, "/api/v1/search?q=jane&lang=fr"); !sameSet(got, "jane-doe", "livre-un") {
		t.Errorf("search?q=jane&lang=fr = %v, want the person and livre-un", got)
	}
	all := pageIDs(t, ts.URL, "/api/v1/search?q=jane")
	if len(all) <= 2 {
		t.Fatalf("search?q=jane unfiltered = %v, want more than the filtered page", all)
	}

	// Series: die-saga derives de, mixed-saga ties (en 1, de 1) and passes both.
	if got := pageIDs(t, ts.URL, "/api/v1/series/search?q=saga&lang=de"); !sameSet(got, "die-saga", "mixed-saga") {
		t.Errorf("series/search?q=saga&lang=de = %v, want die-saga and the tied mixed-saga", got)
	}
	if got := pageIDs(t, ts.URL, "/api/v1/series/search?q=saga&lang=en"); !sameSet(got,
		"the-saga", "saga-chronological", "the-saga-recommended", "mixed-saga") {
		t.Errorf("series/search?q=saga&lang=en = %v", got)
	}

	// people/search ignores it.
	if got := pageIDs(t, ts.URL, "/api/v1/people/search?q=jane&lang=fr"); !sameSet(got, "jane-doe") {
		t.Errorf("people/search?q=jane&lang=fr = %v, want jane-doe", got)
	}
}

// TestSeriesResultCarriesItsLanguage pins the additive series hit field: the
// derived language where there is one, omitted on a tie.
func TestSeriesResultCarriesItsLanguage(t *testing.T) {
	_, ts := newTestServerForCatalog(t, langFilterCatalog())
	_, body := artifacttest.GetJSON(t, ts.URL, "/api/v1/series/search?q=saga")
	langs := map[string]any{}
	for _, r := range body["results"].([]any) {
		m := r.(map[string]any)
		lang, has := m["language"]
		if !has {
			lang = nil
		}
		langs[m["id"].(string)] = lang
	}
	want := map[string]any{
		"the-saga": "en", "saga-chronological": "en", "the-saga-recommended": "en",
		"die-saga": "de", "mixed-saga": nil,
	}
	for id, lang := range want {
		if got, ok := langs[id]; !ok || got != lang {
			t.Errorf("series hit %s language = %v (present %v), want %v", id, got, ok, lang)
		}
	}
}

// TestSearchBoostsObeyTheLanguageFilter pins the leak the filter must close: the
// two boosts resolve works OUTSIDE the FTS query, so an exact title or a series
// volume in another language would otherwise lead a page that excludes it.
func TestSearchBoostsObeyTheLanguageFilter(t *testing.T) {
	_, ts := newTestServerForCatalog(t, langFilterCatalog())

	// Unfiltered, both boosts put book-one first.
	for _, path := range []string{"/api/v1/works/search?q=book+one", "/api/v1/search?q=the+saga+1"} {
		if got := pageIDs(t, ts.URL, path); len(got) == 0 || got[0] != "book-one" {
			t.Fatalf("%s = %v, want book-one first (the boost under test)", path, got)
		}
	}
	for _, path := range []string{
		"/api/v1/works/search?q=book+one&lang=de",
		"/api/v1/search?q=book+one&lang=de",
		"/api/v1/search?q=the+saga+1&lang=de",
		"/api/v1/works/search?q=the+saga+1&lang=fr",
	} {
		if got := pageIDs(t, ts.URL, path); slices.Contains(got, "book-one") {
			t.Errorf("%s = %v: a boosted work outside the filter leaked onto the page", path, got)
		}
	}
	// A boosted work INSIDE the filter still leads.
	if got := pageIDs(t, ts.URL, "/api/v1/works/search?q=buch+zwei&lang=de"); len(got) == 0 || got[0] != "buch-zwei" {
		t.Errorf("works/search?q=buch+zwei&lang=de = %v, want the de-at buch-zwei first", got)
	}
	if got := pageIDs(t, ts.URL, "/api/v1/search?q=the+saga+1&lang=en"); len(got) == 0 || got[0] != "book-one" {
		t.Errorf("search?q=the+saga+1&lang=en = %v, want book-one first", got)
	}
}

// TestLatestAndCoverageFilterByLanguage covers the two works-table surfaces: the
// filter narrows works/latest and the coverage browser (count and page alike),
// the regional tag counts as its primary subtag, and a coverage row carries the
// work's language.
func TestLatestAndCoverageFilterByLanguage(t *testing.T) {
	_, ts := newTestServerForCatalog(t, langFilterCatalog())

	_, latest := artifacttest.GetJSON(t, ts.URL, "/api/v1/works/latest?lang=de")
	if got := ids(t, latest["works"]); !sameSet(got, "buch-eins", "sammelband", "buch-zwei", "saga-tales-de", "saga-tales-jane-de") {
		t.Errorf("works/latest?lang=de = %v", got)
	}
	_, latest = artifacttest.GetJSON(t, ts.URL, "/api/v1/works/latest?lang=fr")
	if got := ids(t, latest["works"]); !sameSet(got, "livre-un", "saga-tales-fr") {
		t.Errorf("works/latest?lang=fr = %v", got)
	}

	_, cov := artifacttest.GetJSON(t, ts.URL, "/api/v1/coverage/works?lang=de")
	if cov["total"] != float64(5) {
		t.Errorf("coverage/works?lang=de total = %v, want 5", cov["total"])
	}
	langs := map[string]any{}
	for _, w := range cov["works"].([]any) {
		m := w.(map[string]any)
		langs[m["id"].(string)] = m["language"]
	}
	if langs["buch-zwei"] != "de-at" || langs["buch-eins"] != "de" || len(langs) != 5 {
		t.Errorf("coverage/works?lang=de rows = %v", langs)
	}
	// A filter selecting most of the catalogue takes the scan form
	// (worksPredicate) and must answer the same way, the regional tag included.
	_, cov = artifacttest.GetJSON(t, ts.URL, "/api/v1/coverage/works?lang=de,en&limit=100")
	if cov["total"] != float64(9) {
		t.Errorf("coverage/works?lang=de,en total = %v, want 9", cov["total"])
	}
	_, latest = artifacttest.GetJSON(t, ts.URL, "/api/v1/works/latest?lang=en,de")
	if got := ids(t, latest["works"]); !slices.Contains(got, "buch-zwei") || slices.Contains(got, "livre-un") {
		t.Errorf("works/latest?lang=en,de = %v, want the de-at buch-zwei and no French work", got)
	}
	// q and lang narrow together.
	_, cov = artifacttest.GetJSON(t, ts.URL, "/api/v1/coverage/works?lang=de&q=saga+tales")
	if cov["total"] != float64(2) {
		t.Errorf("coverage/works?lang=de&q=saga+tales total = %v, want 2", cov["total"])
	}
	// Unfiltered rows carry the language too.
	_, cov = artifacttest.GetJSON(t, ts.URL, "/api/v1/coverage/works")
	if cov["total"] != float64(11) {
		t.Errorf("coverage/works total = %v, want 11", cov["total"])
	}
	for _, w := range cov["works"].([]any) {
		if m := w.(map[string]any); m["language"] == nil || m["language"] == "" {
			t.Errorf("coverage row %v carries no language", m["id"])
		}
	}
}

// TestLangIsValidated is the 400 on every surface that reads ?lang=, people/search
// included - the parameter means one thing on all of them - naming the bad item.
func TestLangIsValidated(t *testing.T) {
	_, ts := newTestServerForCatalog(t, langFilterCatalog())
	for _, path := range []string{
		"/api/v1/search?q=saga&lang=de,english",
		"/api/v1/works/search?q=saga&lang=english",
		"/api/v1/series/search?q=saga&lang=english",
		"/api/v1/people/search?q=jane&lang=english",
		"/api/v1/works/latest?lang=english",
		"/api/v1/coverage/works?lang=english",
		"/api/v1/works/latest?lang=aa,ab,ac,ad,ae,af,ag,ah,ai",
	} {
		code, body := artifacttest.GetJSON(t, ts.URL, path)
		if code != http.StatusBadRequest {
			t.Errorf("GET %s: status %d, want 400", path, code)
			continue
		}
		msg, _ := body["error"].(string)
		if !strings.Contains(msg, "english") && !strings.Contains(msg, "at most 8") {
			t.Errorf("GET %s: error %q names neither the item nor the bound", path, msg)
		}
	}
}

// TestLangIsIgnoredBelowTheLanguagesLayer proves the GATE is the version, not
// the shape: a v6 STAMP over an artifact that still carries search_fts.language
// (and every other v7 column) still answers unfiltered, while garbage is still a
// 400 - the parameter is parsed, then ignored.
func TestLangIsIgnoredBelowTheLanguagesLayer(t *testing.T) {
	ts := downgradedServer(t, artifacttest.Altered(t, langFilterCatalog(), 6))

	if got := pageIDs(t, ts.URL, "/api/v1/works/search?q=buch&lang=en"); !sameSet(got, "buch-eins", "buch-zwei") {
		t.Errorf("v6 works/search?q=buch&lang=en = %v, want the unfiltered page", got)
	}
	if got := pageIDs(t, ts.URL, "/api/v1/series/search?q=saga&lang=fr"); len(got) != 5 {
		t.Errorf("v6 series/search?q=saga&lang=fr = %v, want all five", got)
	}
	_, body := artifacttest.GetJSON(t, ts.URL, "/api/v1/series/search?q=saga")
	for _, r := range body["results"].([]any) {
		if lang, has := r.(map[string]any)["language"]; has {
			t.Errorf("v6 series hit carries language %v", lang)
		}
	}
	_, latest := artifacttest.GetJSON(t, ts.URL, "/api/v1/works/latest?lang=fr")
	if n := len(ids(t, latest["works"])); n <= 2 {
		t.Errorf("v6 works/latest?lang=fr = %d works, want the unfiltered list", n)
	}
	_, cov := artifacttest.GetJSON(t, ts.URL, "/api/v1/coverage/works?lang=fr")
	if cov["total"] != float64(11) {
		t.Errorf("v6 coverage/works?lang=fr total = %v, want 11", cov["total"])
	}
	if code, _ := artifacttest.GetJSON(t, ts.URL, "/api/v1/works/search?q=buch&lang=english"); code != http.StatusBadRequest {
		t.Errorf("v6 garbage lang: status %d, want 400", code)
	}
	// The ABS language route ranks as the unscoped one does.
	_, plain := absMatches(t, ts.URL, "/abs/search?query=saga+tales&author=Max+Muster")
	_, scoped := absMatches(t, ts.URL, "/abs/fr/search?query=saga+tales&author=Max+Muster")
	if !reflect.DeepEqual(plain, scoped) {
		t.Errorf("v6 /abs/fr/search differs from /abs/search:\n%v\n%v", scoped, plain)
	}
}

// absOrder is the (author, language) of every match, in order.
func absOrder(t *testing.T, base, path string) []string {
	t.Helper()
	code, matches := absMatches(t, base, path)
	if code != http.StatusOK {
		t.Fatalf("GET %s: status %d", path, code)
	}
	out := make([]string, len(matches))
	for i, m := range matches {
		book := m.(map[string]any)
		out[i] = book["author"].(string) + "/" + book["language"].(string)
	}
	return out
}

// TestABSLanguageRouteRanksAuthorThenLanguage pins /abs/{lang}/search's order:
// the author dominates and the language comes second - (author + language,
// author, language, the rest) - and nothing is filtered away.
func TestABSLanguageRouteRanksAuthorThenLanguage(t *testing.T) {
	_, ts := newTestServerForCatalog(t, langFilterCatalog())

	got := absOrder(t, ts.URL, "/abs/de/search?mediaType=book&query=saga+tales&author=Jane+Doe")
	want := []string{"Jane Doe/de", "Jane Doe/en", "Max Muster/de", "Max Muster/fr"}
	if !slices.Equal(got, want) {
		t.Errorf("/abs/de/search with an author = %v, want %v", got, want)
	}
	got = absOrder(t, ts.URL, "/abs/fr/search?mediaType=book&query=saga+tales&author=Jane+Doe")
	want = []string{"Jane Doe/en", "Jane Doe/de", "Max Muster/fr", "Max Muster/de"}
	if !slices.Equal(got, want) {
		t.Errorf("/abs/fr/search with an author = %v, want %v", got, want)
	}
	// With no author the language leads and nothing is lost.
	got = absOrder(t, ts.URL, "/abs/fr/search?mediaType=book&query=saga+tales")
	if len(got) != 4 || got[0] != "Max Muster/fr" {
		t.Errorf("/abs/fr/search without an author = %v, want the French edition first of four", got)
	}
	// A regional segment ranks by its primary subtag, and a list is a union.
	got = absOrder(t, ts.URL, "/abs/de-at,fr/search?mediaType=book&query=saga+tales&author=Max+Muster")
	want = []string{"Max Muster/de", "Max Muster/fr", "Jane Doe/de", "Jane Doe/en"}
	if !slices.Equal(got, want) {
		t.Errorf("/abs/de-at,fr/search = %v, want %v", got, want)
	}
}

// TestABSLanguageRouteEdges covers the two URLs an admin's configuration can
// produce besides the plain one: a base with a trailing slash arrives as
// /abs/de//search and must still answer (ServeMux's path cleaning, a 307 to the
// cleaned path with the query string kept, which Audiobookshelf's axios follows
// for a GET), and a segment that is not a
// language list names no provider and is a 404.
func TestABSLanguageRouteEdges(t *testing.T) {
	_, ts := newTestServerForCatalog(t, langFilterCatalog())

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(ts.URL + "/abs/de//search?query=saga+tales&author=Jane+Doe")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusTemporaryRedirect {
		t.Fatalf("/abs/de//search: status %d, want 307", resp.StatusCode)
	}
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil || loc.Path != "/abs/de/search" || loc.Query().Get("query") != "saga tales" || loc.Query().Get("author") != "Jane Doe" {
		t.Errorf("/abs/de//search Location = %q, want /abs/de/search with the query kept", resp.Header.Get("Location"))
	}
	// Followed, it is the ranked answer.
	if got := absOrder(t, ts.URL, "/abs/de//search?query=saga+tales&author=Jane+Doe"); len(got) != 4 || got[0] != "Jane Doe/de" {
		t.Errorf("/abs/de//search followed = %v", got)
	}

	for _, seg := range []string{"english", "de_at", "de,english", "%20"} {
		if code, _ := artifacttest.GetJSON(t, ts.URL, "/abs/"+seg+"/search?query=saga"); code != http.StatusNotFound {
			t.Errorf("/abs/%s/search: status %d, want 404", seg, code)
		}
	}
	if code, _ := artifacttest.GetJSON(t, ts.URL, "/abs/de/search"); code != http.StatusBadRequest {
		t.Errorf("/abs/de/search with no query: status %d, want 400", code)
	}
}

// TestLanguageFilteredQueriesUseTheLanguageIndex pins the works-table predicate
// POSITIVELY, both ways round. A filter selecting a MINORITY of the catalogue must
// reach works through idx_works_language, one SEARCH per arm, so it reads its own
// rows rather than the catalogue (assertNoFullScan alone would pass a plan that
// read works through some other index); one selecting MOST of it must not, since
// walking the index over most of the table and then reading every row costs more
// than one read of the table (worksPredicate). The fixture is 4 en, 5 de (one
// tagged de-at) and 2 fr of 11.
func TestLanguageFilteredQueriesUseTheLanguageIndex(t *testing.T) {
	snap := openTestDB(t, artifacttest.Build(t, langFilterCatalog()))

	// The boosts' filter reads the ids it was handed by primary key.
	pred, predArgs := langFilter{"de"}.predicate("language", false)
	plan := queryPlan(t, snap, worksInLanguagesSQL("?,?", pred), append([]any{"book-one", "buch-zwei"}, predArgs...)...)
	assertNoFullScan(t, plan)
	if joined := strings.Join(plan, " | "); !strings.Contains(joined, "INDEX sqlite_autoindex_works_1 (id=?)") {
		t.Errorf("worksInLanguages does not read works by id: %s", joined)
	}

	for _, tc := range []struct {
		f     langFilter
		index bool
	}{
		{langFilter{"de"}, true},        // 5 of 11
		{langFilter{"en"}, true},        // 4 of 11
		{langFilter{"fr", "ja"}, true},  // 2 of 11, two languages
		{langFilter{"de", "en"}, false}, // 9 of 11
		{langFilter{"en", "fr"}, false}, // 6 of 11
	} {
		q, args := snap.latestCandidatesQuery(tc.f, 200)
		where, wargs, _ := snap.coverageWhere(filterMissing, "", tc.f)
		for name, plan := range map[string][]string{
			"works/latest":   queryPlan(t, snap, q, args...),
			"coverage/works": queryPlan(t, snap, `SELECT COUNT(*) FROM works w WHERE `+where, wargs...),
			"coverage/works page": queryPlan(t, snap,
				`SELECT w.id FROM works w WHERE `+where+` ORDER BY w.title, w.id LIMIT ? OFFSET ?`, append(wargs, 25, 0)...),
		} {
			n := strings.Count(strings.Join(plan, " | "), "USING INDEX idx_works_language")
			switch {
			case tc.index && n != 2*len(tc.f):
				t.Errorf("%s %v: %d idx_works_language searches, want %d: %v", name, tc.f, n, 2*len(tc.f), plan)
			case tc.index:
				assertNoFullScan(t, plan)
			case n != 0:
				t.Errorf("%s %v selects most of the catalogue but walks idx_works_language: %v", name, tc.f, plan)
			}
		}
	}
}

// TestLangFilterForIsTheOneGate pins the request-level gate every surface goes
// through: garbage is an error on every artifact and every scope, a live filter
// comes back as parsed, and it is dropped below languagesSchemaVersion and on the
// people scope (a person has no language).
func TestLangFilterForIsTheOneGate(t *testing.T) {
	v7 := &DB{schemaVersion: languagesSchemaVersion}
	v6 := &DB{schemaVersion: languagesSchemaVersion - 1}
	for _, snap := range []*DB{v6, v7} {
		for _, kind := range []searchKind{kindAny, kindWork, kindPerson, kindSeries} {
			if _, err := snap.langFilterFor("english", kind); err == nil {
				t.Errorf("v%d %q: garbage accepted", snap.schemaVersion, kind)
			}
			got, err := snap.langFilterFor("de,EN", kind)
			if err != nil {
				t.Fatal(err)
			}
			live := snap == v7 && kind != kindPerson
			if want := (langFilter{"de", "en"}); live && !slices.Equal(got, want) {
				t.Errorf("v%d %q = %v, want %v", snap.schemaVersion, kind, got, want)
			} else if !live && got != nil {
				t.Errorf("v%d %q = %v, want the filter dropped", snap.schemaVersion, kind, got)
			}
		}
	}
}

// TestABSSkipsTheUnfilteredWindowWhenItCannotReachThePage pins absCandidates'
// short cut: with no author and a language window already holding limit works,
// the unfiltered window is never read - and the matches are exactly what running
// both windows gives. An author that matches nobody is how the comparison runs
// both: it keeps both windows, and with every work in its "author missed" half the
// order it ranks is (language, the rest), the no-author order.
func TestABSSkipsTheUnfilteredWindowWhenItCannotReachThePage(t *testing.T) {
	snap := openTestDB(t, artifacttest.Build(t, langFilterCatalog()))
	de := langFilter{"de"} // two German "Saga Tales" of four

	ids, _, err := snap.absCandidates(t.Context(), "saga tales", "", 2, de)
	if err != nil {
		t.Fatal(err)
	}
	if !sameSet(ids, "saga-tales-de", "saga-tales-jane-de") {
		t.Errorf("no author, full language window: candidates %v, want the two German works alone", ids)
	}
	both, _, err := snap.absCandidates(t.Context(), "saga tales", "Nobody At All", 2, de)
	if err != nil {
		t.Fatal(err)
	}
	if len(both) != 4 {
		t.Fatalf("with an author: candidates %v, want both windows (four works)", both)
	}
	// A language window SHORT of limit still reads the unfiltered one.
	if short, _, err := snap.absCandidates(t.Context(), "saga tales", "", 3, de); err != nil || len(short) != 4 {
		t.Errorf("no author, language window short of limit: candidates %v (%v), want four", short, err)
	}

	for _, limit := range []int{1, 2} {
		skipped, err := snap.absSearch(t.Context(), "saga tales", "", "", limit, de)
		if err != nil {
			t.Fatal(err)
		}
		full, err := snap.absSearch(t.Context(), "saga tales", "Nobody At All", "", limit, de)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(skipped, full) {
			t.Errorf("limit %d: skipping the unfiltered window changed the matches:\n%v\n%v", limit, skipped, full)
		}
	}
}

// TestBoostProbesFilterInsideTheirWindows pins that both boost probes apply the
// language filter INSIDE their bounded windows rather than to what the window
// returned: more same-titled (or same-named) records in other languages than the
// window holds must not crowd the reader's own language out of the boost.
func TestBoostProbesFilterInsideTheirWindows(t *testing.T) {
	cat := langFilterCatalog()
	work := func(id, title, lang string) *model.Work {
		return &model.Work{ID: id, Title: title, Language: lang, Authors: []string{"jane-doe"}, License: "CC0-1.0"}
	}
	// exactTitleProbeLimit English "Dune"s sort ahead of the German one by id.
	for i := 0; i < exactTitleProbeLimit; i++ {
		cat.Works = append(cat.Works, work(fmt.Sprintf("dune-en-%02d", i), "Dune", "en"))
	}
	cat.Works = append(cat.Works, work("zz-dune-de", "Dune", "de"))
	// seriesProbeLimit English series named "Saga Chronicles" beside a German one.
	for i := 0; i < seriesProbeLimit; i++ {
		id := fmt.Sprintf("saga-chronicles-en-%d", i)
		cat.Works = append(cat.Works, work(id+"-vol", "Volume "+id, "en"))
		cat.Series = append(cat.Series, &model.Series{ID: id, Name: "Saga Chronicles", License: "CC0-1.0",
			Works: []model.SeriesWork{{Work: id + "-vol", Position: "4"}}})
	}
	cat.Works = append(cat.Works, work("saga-chronicles-de-vol", "Band vier", "de"))
	cat.Series = append(cat.Series, &model.Series{ID: "zz-saga-chronicles-de", Name: "Saga Chronicles", License: "CC0-1.0",
		Works: []model.SeriesWork{{Work: "saga-chronicles-de-vol", Position: "4"}}})
	snap := openTestDB(t, artifacttest.Build(t, cat))
	de := langFilter{"de"}

	if q, args := exactTitleQuery("dune", nil); q != exactTitleSQL || len(args) != 2 {
		t.Errorf("unfiltered exact-title probe = %q %v, want exactTitleSQL unchanged", q, args)
	}
	if got, err := snap.exactTitleHits(t.Context(), "dune", nil); err != nil || slices.Contains(got, "zz-dune-de") {
		t.Fatalf("fixture: unfiltered exact-title window = %v (%v), want it full without the German Dune", got, err)
	}
	if got, err := snap.exactTitleHits(t.Context(), "dune", de); err != nil || !slices.Equal(got, []string{"zz-dune-de"}) {
		t.Errorf("exact-title probe under lang=de = %v (%v), want [zz-dune-de]", got, err)
	}
	if got, err := snap.seriesPositionHits(t.Context(), "saga chronicles 4", de); err != nil || !slices.Equal(got, []string{"saga-chronicles-de-vol"}) {
		t.Errorf("series probe under lang=de = %v (%v), want [saga-chronicles-de-vol]", got, err)
	}
}
