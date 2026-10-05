package serve

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// languagesCatalog is a small franchise carrying every schema_version 7 fact.
//
//   - "The Saga" (the-saga) is the PRIMARY ordering, in publication order, with
//     two VARIANT orderings of it: saga-chronological, whose id sorts BEFORE the
//     primary's (the case firstSeriesByWork's primary preference exists for), and
//     the-saga-recommended, whose id sorts after it.
//   - book-one sits in all three, so a card, a work page's series[0] and its
//     JSON-LD isPartOf have three series to choose from.
//   - buch-eins and livre-un translate book-one; sammelband is a translated
//     omnibus of book-one AND book-two; die-saga translates the-saga.
func languagesCatalog() *model.Catalog {
	jane := &model.Person{ID: "jane-doe", Name: "Jane Doe", License: "CC0-1.0"}
	work := func(id, title, lang string, of ...string) *model.Work {
		return &model.Work{
			ID: id, Title: title, Language: lang, Authors: []string{"jane-doe"},
			License: "CC0-1.0", TranslationOf: of,
		}
	}
	members := func(pairs ...string) []model.SeriesWork {
		var out []model.SeriesWork
		for _, p := range pairs {
			w, pos, _ := strings.Cut(p, "@")
			out = append(out, model.SeriesWork{Work: w, Position: pos})
		}
		return out
	}
	return &model.Catalog{
		People: []*model.Person{jane},
		Works: []*model.Work{
			work("book-one", "Book One", "en"),
			work("book-two", "Book Two", "en"),
			work("the-prequel", "The Prequel", "en"),
			work("buch-eins", "Buch Eins", "de", "book-one"),
			work("livre-un", "Livre Un", "fr", "book-one"),
			work("sammelband", "Sammelband", "de", "book-one", "book-two"),
		},
		Series: []*model.Series{
			{
				ID: "the-saga", Name: "The Saga", License: "CC0-1.0", Authors: []string{"jane-doe"},
				Works: members("book-one@1", "book-two@2"), Ordering: model.OrderingPublication,
			},
			{
				ID: "saga-chronological", Name: "The Saga (Chronological Order)", License: "CC0-1.0",
				Works:    members("the-prequel@1", "book-one@2", "book-two@3"),
				Ordering: model.OrderingChronological, OrderingOf: "the-saga",
			},
			{
				ID: "the-saga-recommended", Name: "The Saga (Recommended Order)", License: "CC0-1.0",
				Works:    members("book-one@1", "the-prequel@2", "book-two@3"),
				Ordering: model.OrderingRecommended, OrderingOf: "the-saga",
			},
			{
				ID: "die-saga", Name: "Die Saga", License: "CC0-1.0",
				Works: members("buch-eins@1", "sammelband@2"), TranslationOf: []string{"the-saga"},
			},
		},
	}
}

// ids reads the "id" of every object in a decoded JSON list.
func ids(t *testing.T, v any) []string {
	t.Helper()
	list, ok := v.([]any)
	if !ok {
		t.Fatalf("not a list: %v", v)
	}
	var out []string
	for _, item := range list {
		out = append(out, item.(map[string]any)["id"].(string))
	}
	return out
}

func sameIDs(t *testing.T, what string, got any, want ...string) {
	t.Helper()
	if g := ids(t, got); strings.Join(g, ",") != strings.Join(want, ",") {
		t.Errorf("%s = %v, want %v", what, g, want)
	}
}

// TestWorkDetailTranslations covers both directions of a work's translation
// links on GET /works/{id}: an original lists every work translating it, a
// translation (and a translated omnibus) lists what it translates, each in id
// order and carrying the other work's title and language - and a work with
// neither link omits both keys.
func TestWorkDetailTranslations(t *testing.T) {
	_, ts := newTestServerForCatalog(t, languagesCatalog())

	_, one := getJSON(t, ts.URL, "/api/v1/works/book-one")
	sameIDs(t, "book-one translations", one["translations"], "buch-eins", "livre-un", "sammelband")
	if _, has := one["translation_of"]; has {
		t.Errorf("an original carries translation_of: %v", one["translation_of"])
	}
	first := one["translations"].([]any)[0].(map[string]any)
	if first["title"] != "Buch Eins" || first["language"] != "de" {
		t.Errorf("translations[0] = %v, want Buch Eins in de", first)
	}

	_, omni := getJSON(t, ts.URL, "/api/v1/works/sammelband")
	sameIDs(t, "sammelband translation_of", omni["translation_of"], "book-one", "book-two")
	if _, has := omni["translations"]; has {
		t.Errorf("a translation nothing translates carries translations: %v", omni["translations"])
	}

	_, plain := getJSON(t, ts.URL, "/api/v1/works/the-prequel")
	for _, key := range []string{"translation_of", "translations"} {
		if _, has := plain[key]; has {
			t.Errorf("a work with no links carries %s: %v", key, plain[key])
		}
	}
}

// TestSeriesDetailLanguagesAndOrderings covers the series fields: the derived
// language, the stated ordering and ordering_of, both translation directions,
// and the ordering FAMILY - identical on the primary and on each variant, the
// primary first even where a variant's id sorts before it.
func TestSeriesDetailLanguagesAndOrderings(t *testing.T) {
	_, ts := newTestServerForCatalog(t, languagesCatalog())

	_, primary := getJSON(t, ts.URL, "/api/v1/series/the-saga")
	if primary["language"] != "en" || primary["ordering"] != model.OrderingPublication {
		t.Errorf("the-saga language/ordering = %v/%v", primary["language"], primary["ordering"])
	}
	if _, has := primary["ordering_of"]; has {
		t.Errorf("a primary carries ordering_of: %v", primary["ordering_of"])
	}
	sameIDs(t, "the-saga translations", primary["translations"], "die-saga")
	sameIDs(t, "the-saga orderings", primary["orderings"], "the-saga", "saga-chronological", "the-saga-recommended")
	fam := primary["orderings"].([]any)
	if got := fam[1].(map[string]any); got["ordering"] != model.OrderingChronological || got["name"] != "The Saga (Chronological Order)" {
		t.Errorf("orderings[1] = %v", got)
	}

	for _, variant := range []string{"saga-chronological", "the-saga-recommended"} {
		_, v := getJSON(t, ts.URL, "/api/v1/series/"+variant)
		if v["ordering_of"] != "the-saga" {
			t.Errorf("%s ordering_of = %v, want the-saga", variant, v["ordering_of"])
		}
		sameIDs(t, variant+" orderings", v["orderings"], "the-saga", "saga-chronological", "the-saga-recommended")
	}

	_, de := getJSON(t, ts.URL, "/api/v1/series/die-saga")
	if de["language"] != "de" {
		t.Errorf("die-saga language = %v, want de", de["language"])
	}
	sameIDs(t, "die-saga translation_of", de["translation_of"], "the-saga")
	for _, key := range []string{"orderings", "ordering", "ordering_of", "translations"} {
		if _, has := de[key]; has {
			t.Errorf("die-saga carries %s: %v", key, de[key])
		}
	}
}

// TestStatsLanguages pins the census: most works first, ties by tag.
func TestStatsLanguages(t *testing.T) {
	_, ts := newTestServerForCatalog(t, languagesCatalog())
	_, body := getJSON(t, ts.URL, "/api/v1/stats")
	raw, _ := json.Marshal(body["languages"])
	const want = `[{"language":"en","works":3},{"language":"de","works":2},{"language":"fr","works":1}]`
	if string(raw) != want {
		t.Errorf("stats languages = %s, want %s", raw, want)
	}
}

// TestPrimaryOrderingWinsTheSeriesChoice is the firstSeriesByWork preference: a
// work in a primary and in variants of it - one of whose ids sorts FIRST - is
// presented under the PRIMARY by every surface that picks one series: the card,
// the work detail's series[0] (with the variants after it, each naming its
// primary) and the work page's JSON-LD isPartOf.
func TestPrimaryOrderingWinsTheSeriesChoice(t *testing.T) {
	cat := languagesCatalog()
	snap := snapshotFor(t, cat)
	card, err := snap.workCard(t.Context(), "book-one")
	if err != nil {
		t.Fatal(err)
	}
	if card.Series == nil || card.Series.ID != "the-saga" {
		t.Fatalf("card series = %+v, want the-saga", card.Series)
	}

	ts := newPageServer(t, cat, markedShells)
	_, detail := getJSON(t, ts.URL, "/api/v1/works/book-one")
	sameIDs(t, "book-one series", detail["series"], "the-saga", "saga-chronological", "the-saga-recommended")
	refs := detail["series"].([]any)
	if _, has := refs[0].(map[string]any)["ordering_of"]; has {
		t.Errorf("the primary's series ref carries ordering_of: %v", refs[0])
	}
	if got := refs[1].(map[string]any)["ordering_of"]; got != "the-saga" {
		t.Errorf("a variant's series ref ordering_of = %v, want the-saga", got)
	}

	// works/latest's series cap keys on the same choice, so its cards agree.
	_, latest := getJSON(t, ts.URL, "/api/v1/works/latest")
	for _, w := range latest["works"].([]any) {
		c := w.(map[string]any)
		if c["id"] == "book-one" && c["series"].(map[string]any)["id"] != "the-saga" {
			t.Errorf("latest card series = %v, want the-saga", c["series"])
		}
	}

	code, page := getPage(t, ts.URL, "/works/book-one")
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	var graph struct {
		Graph []map[string]any `json:"@graph"`
	}
	if err := json.Unmarshal([]byte(between(t, page, `<script type="application/ld+json">`, "</script>")), &graph); err != nil {
		t.Fatal(err)
	}
	part, _ := graph.Graph[0]["isPartOf"].(map[string]any)
	if part["url"] != testSiteURL+"/series/the-saga" {
		t.Errorf("JSON-LD isPartOf = %v, want the primary", part)
	}
}

// v6ShapedDB builds cat's artifact and rolls it back to the schema_version 6
// SHAPE, which is more than downgradedDB's dropped tables: the translations table
// goes, the three series columns and their index go, and idx_works_language goes.
// search_fts keeps its language column, because an FTS5 table cannot drop one -
// harmless, since no serve query reads it.
func v6ShapedDB(t *testing.T, cat *model.Catalog) string {
	t.Helper()
	return alteredDB(t, cat, 6,
		"DROP TABLE translations",
		"DROP INDEX idx_series_ordering_of",
		"DROP INDEX idx_works_language",
		"ALTER TABLE series DROP COLUMN ordering_of",
		"ALTER TABLE series DROP COLUMN ordering",
		"ALTER TABLE series DROP COLUMN language",
	)
}

// TestLanguagesTolerateAV6Artifact serves a v6-SHAPED artifact - no translations
// table and no series ordering columns - to the v7 binary. Every read the
// languages layer added gates on the version, so every route still answers 200
// with the v6 payload: no translation lists, no series language or family, no
// stats census, and a work's series in plain id order (the v6 ORDER BY, with NULL
// selected in place of ordering_of).
func TestLanguagesTolerateAV6Artifact(t *testing.T) {
	ts := downgradedServer(t, v6ShapedDB(t, languagesCatalog()))

	get := func(route string) map[string]any {
		t.Helper()
		code, body := getJSON(t, ts.URL, route)
		if code != http.StatusOK {
			t.Fatalf("GET %s on a v6 artifact: status %d, body %v", route, code, body)
		}
		return body
	}
	one := get("/api/v1/works/book-one")
	if _, has := one["translations"]; has {
		t.Errorf("v6 work carries translations: %v", one["translations"])
	}
	sameIDs(t, "v6 book-one series (id order)", one["series"], "saga-chronological", "the-saga", "the-saga-recommended")
	for _, ref := range one["series"].([]any) {
		if _, has := ref.(map[string]any)["ordering_of"]; has {
			t.Errorf("v6 series ref carries ordering_of: %v", ref)
		}
	}
	omni := get("/api/v1/works/sammelband")
	if _, has := omni["translation_of"]; has {
		t.Errorf("v6 work carries translation_of: %v", omni["translation_of"])
	}
	for _, id := range []string{"the-saga", "saga-chronological", "die-saga"} {
		s := get("/api/v1/series/" + id)
		for _, key := range []string{"language", "ordering", "ordering_of", "translation_of", "translations", "orderings"} {
			if _, has := s[key]; has {
				t.Errorf("v6 series %s carries %s: %v", id, key, s[key])
			}
		}
	}
	if st := get("/api/v1/stats"); st["languages"] != nil {
		t.Errorf("v6 stats carries languages: %v", st["languages"])
	}
	get("/api/v1/works/latest")
	get("/api/v1/search?q=book")
	get("/api/v1/series/search?q=saga")
}

// TestLanguagesRefuseAVersion7ArtifactWithoutTheTable is the other side of the
// gate: an artifact CLAIMING version 7 without the translations table, or
// without the series ordering columns, is a corrupt file - the builder writes the
// version and the shape together - so the load fails naming the claim rather
// than 500ing every work and series page.
func TestLanguagesRefuseAVersion7ArtifactWithoutTheTable(t *testing.T) {
	cases := []struct {
		name  string
		stmts []string
		want  string
	}{
		{"no translations table", []string{"DROP TABLE translations"}, "translations table"},
		{"no ordering_of column", []string{"DROP INDEX idx_series_ordering_of", "ALTER TABLE series DROP COLUMN ordering_of"}, "series ordering columns"},
		// The two columns only the series header reads: the load proves every
		// column a gated read names, not just the one the memos walk.
		{"no ordering column", []string{"ALTER TABLE series DROP COLUMN ordering"}, "series ordering columns"},
		{"no language column", []string{"ALTER TABLE series DROP COLUMN language"}, "series ordering columns"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := openSnapshot(alteredDB(t, languagesCatalog(), 7, tc.stmts...), "")
			if err == nil {
				t.Fatal("a version 7 artifact missing its languages shape opened cleanly")
			}
			for _, want := range []string{"schema_version 7", tc.want} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not name %q", err, want)
				}
			}
		})
	}
}

// TestLanguageMemosSkipTheQueries pins the two load-time memos: over a catalogue
// with no translation and no variant ordering - every release until the first
// link lands - the work and series pages do not touch the translations table or
// the family query at all, and over one that HAS a family only its members pay
// for the family query.
func TestLanguageMemosSkipTheQueries(t *testing.T) {
	empty := snapshotFor(t, fixtureCatalog())
	if empty.hasTranslations || empty.hasOrderings() {
		t.Errorf("memos = %v/%v for a catalogue with no links", empty.hasTranslations, empty.hasOrderings())
	}
	// Close the handle: a query would now fail, so a nil error proves none ran.
	empty.close()
	if of, by, err := empty.workTranslations("project-hail-mary"); err != nil || of != nil || by != nil {
		t.Errorf("workTranslations = %v, %v, %v; want no query", of, by, err)
	}
	if fam, err := empty.orderingFamily(&seriesDetail{ID: "the-stormlight-archive"}); err != nil || fam != nil {
		t.Errorf("orderingFamily = %v, %v; want no query", fam, err)
	}

	full := snapshotFor(t, languagesCatalog())
	if !full.hasTranslations || !full.hasOrderings() {
		t.Errorf("memos = %v/%v for a catalogue holding both", full.hasTranslations, full.hasOrderings())
	}
	if len(full.orderingPrimaries) != 1 || !full.orderingPrimaries["the-saga"] {
		t.Errorf("orderingPrimaries = %v, want exactly the-saga", full.orderingPrimaries)
	}
	full.close()
	if fam, err := full.orderingFamily(&seriesDetail{ID: "die-saga"}); err != nil || fam != nil {
		t.Errorf("orderingFamily(die-saga) = %v, %v; want no query for a series outside every family", fam, err)
	}
}

// TestLanguagePagesGolden renders the pages the languages layer changes over the
// franchise catalogue: an original's work page (workTranslation, and isPartOf on
// the primary), a translated omnibus (translationOfWork naming two originals)
// and a primary series (inLanguage and workTranslation). Regenerate with
// -update-golden, as TestEntityPagesGolden.
func TestLanguagePagesGolden(t *testing.T) {
	ts := newPageServer(t, languagesCatalog(), markedShells)
	for _, tc := range []struct{ name, path string }{
		{"work-translations", "/works/book-one"},
		{"work-translation-of", "/works/sammelband"},
		{"series-orderings", "/series/the-saga"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, page := getPage(t, ts.URL, tc.path)
			if code != http.StatusOK {
				t.Fatalf("status = %d", code)
			}
			assertGolden(t, tc.name+".html", []byte(page))
		})
	}
}

// TestLatestCapsAnOrderingFamilyAsOneSeries pins works/latest's per-series cap
// on the ordering FAMILY: three works catalogued on one date, two carded under
// the primary and a prequel that only the variant orderings list (so its card's
// series is a variant), are one franchise and share one cap of latestSeriesCap.
// Keyed on the card's series id, the prequel opened a bucket of its own and all
// three filled the grid.
func TestLatestCapsAnOrderingFamilyAsOneSeries(t *testing.T) {
	cat := languagesCatalog()
	for _, w := range cat.Works {
		switch w.ID {
		case "book-one", "book-two", "the-prequel":
			w.AddedAt = "2026-09-30"
		}
	}
	snap := snapshotFor(t, cat)
	cards, err := snap.latestWorks(12, nil)
	if err != nil {
		t.Fatal(err)
	}
	var family []string
	for _, c := range cards {
		switch c.ID {
		case "book-one", "book-two", "the-prequel":
			family = append(family, c.ID+"@"+c.Series.ID)
		}
	}
	if len(family) > latestSeriesCap {
		t.Errorf("works/latest carries %d works of one ordering family (%v), want at most %d",
			len(family), family, latestSeriesCap)
	}
	if len(family) == 0 {
		t.Error("works/latest carries none of the family's works")
	}
}
