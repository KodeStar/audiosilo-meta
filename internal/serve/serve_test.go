package serve

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kodestar/audiosilo-meta/internal/build"
	"github.com/kodestar/audiosilo-meta/internal/model"
)

// fixtureCatalog is a small but representative dataset: two fully-fleshed works
// (one with a cover + chapters + ASIN, one with an ISBN + dual narrators) plus
// two thin works that only exist to exercise numeric series ordering.
func fixtureCatalog() *model.Catalog {
	andy := &model.Person{ID: "andy-weir", Name: "Andy Weir", License: "CC0-1.0"}
	porter := &model.Person{ID: "ray-porter", Name: "Ray Porter", License: "CC0-1.0"}
	sando := &model.Person{ID: "brandon-sanderson", Name: "Brandon Sanderson", License: "CC0-1.0"}
	kramer := &model.Person{ID: "michael-kramer", Name: "Michael Kramer", License: "CC0-1.0"}
	reading := &model.Person{ID: "kate-reading", Name: "Kate Reading", License: "CC0-1.0"}

	phm := &model.Work{
		ID: "project-hail-mary", Title: "Project Hail Mary", Language: "en",
		Authors: []string{"andy-weir"}, License: "CC0-1.0",
		Recordings: []*model.Recording{{
			ID: "ray-porter-2021", Work: "project-hail-mary", Language: "en",
			RuntimeMin: 970, Publisher: "Audible Studios", ReleaseDate: "2021-05-04",
			CoverURL: "https://example.test/phm.jpg", License: "CC0-1.0",
			Narrators: []string{"ray-porter"},
			ASIN:      []model.ASIN{{Region: "us", ASIN: "B08G9PRS1K"}},
			Chapters: []model.Chapter{
				{Title: "Opening Credits", StartMS: 0, LengthMS: 5000},
				{Title: "Chapter 1", StartMS: 5000, LengthMS: 600000},
				{Title: "Chapter 2", StartMS: 605000, LengthMS: 600000},
			},
		}},
	}
	wok := &model.Work{
		ID: "the-way-of-kings", Title: "The Way of Kings", Language: "en",
		Authors: []string{"brandon-sanderson"}, License: "CC0-1.0",
		Recordings: []*model.Recording{{
			ID: "kramer-reading-2010", Work: "the-way-of-kings", Language: "en",
			Narrators: []string{"michael-kramer", "kate-reading"}, License: "CC0-1.0",
			ISBN: []string{"9781427209269"},
		}},
	}
	wor := &model.Work{
		ID: "words-of-radiance", Title: "Words of Radiance", Language: "en",
		Authors: []string{"brandon-sanderson"}, License: "CC0-1.0",
	}
	edge := &model.Work{
		ID: "edgedancer", Title: "Edgedancer", Language: "en",
		Authors: []string{"brandon-sanderson"}, License: "CC0-1.0",
	}

	series := &model.Series{
		ID: "the-stormlight-archive", Name: "The Stormlight Archive", License: "CC0-1.0",
		Authors: []string{"brandon-sanderson"},
		Works: []model.SeriesWork{
			{Work: "the-way-of-kings", Position: "1"},
			{Work: "words-of-radiance", Position: "2"},
			{Work: "edgedancer", Position: "10"}, // "10" < "2" as a string; must sort last numerically
		},
	}
	chars := &model.Characters{
		Work: "project-hail-mary", License: "CC-BY-SA-3.0",
		Sources: []model.Source{{Type: "community"}},
		Characters: []model.Character{
			{
				ID: "ryland-grace", Name: "Ryland Grace", Role: "protagonist",
				Aliases: []string{"Dr. Grace"}, Reveal: model.Position{Chapter: 1},
				Description: "A science teacher who wakes aboard the ship with amnesia.",
				Xref:        &model.CharacterXref{Wikidata: "Q110001"},
			},
			{ID: "rocky", Name: "Rocky", Role: "supporting", Reveal: model.Position{Chapter: 8}},
		},
	}
	recaps := &model.Recaps{
		Work: "project-hail-mary", License: "CC-BY-SA-3.0",
		Sources: []model.Source{{Type: "community"}},
		InShort: "A lone amnesiac wakes aboard a ship, befriends an alien, and saves both worlds.",
		Ending:  "Grace stays on Erid while the cure flies home.",
		Recaps: []model.Recap{
			{Through: model.Position{Chapter: 9}, Scope: "book", Text: "First contact is made."},
			{Through: model.Position{Chapter: 2}, Scope: "book", Text: "Grace wakes with amnesia."},
		},
	}
	return &model.Catalog{
		Works:      []*model.Work{phm, wok, wor, edge},
		People:     []*model.Person{andy, porter, sando, kramer, reading},
		Series:     []*model.Series{series},
		Characters: []*model.Characters{chars},
		Recaps:     []*model.Recaps{recaps},
	}
}

// buildFixtureDB writes a fixture artifact and returns its path. added is the
// file-derived added_at map passed to the builder.
func buildFixtureDB(t *testing.T, cat *model.Catalog, added map[string]string) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "meta.sqlite")
	if err := build.Build(cat, out, time.Date(2026, 7, 11, 0, 0, 0, 0, time.UTC), added); err != nil {
		t.Fatal(err)
	}
	return out
}

func newTestServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	added := map[string]string{"project-hail-mary": "2026-07-10T00:00:00Z"}
	dbPath := buildFixtureDB(t, fixtureCatalog(), added)
	srv, err := New(Config{DBPath: dbPath, swapGrace: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return srv, ts
}

// getJSON fetches path and decodes the body into a generic map.
func getJSON(t *testing.T, base, path string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Get(base + path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if len(body) > 0 {
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatalf("GET %s: decode %q: %v", path, body, err)
		}
	}
	return resp.StatusCode, out
}

func TestHealthz(t *testing.T) {
	_, ts := newTestServer(t)
	code, body := getJSON(t, ts.URL, "/healthz")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	if body["status"] != "ok" {
		t.Errorf("status = %v", body["status"])
	}
	if body["built_at"] != "2026-07-11T00:00:00Z" {
		t.Errorf("built_at = %v", body["built_at"])
	}
	if body["works"].(float64) != 4 {
		t.Errorf("works = %v", body["works"])
	}
}

func TestStats(t *testing.T) {
	_, ts := newTestServer(t)
	code, body := getJSON(t, ts.URL, "/api/v1/stats")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	want := map[string]float64{
		"works": 4, "recordings": 2, "people": 5, "series": 1,
		"total_runtime_min": 970, "total_chapters": 3,
	}
	for k, v := range want {
		if got, _ := body[k].(float64); got != v {
			t.Errorf("stats[%s] = %v, want %v", k, body[k], v)
		}
	}
	if body["built_at"] != "2026-07-11T00:00:00Z" {
		t.Errorf("built_at = %v", body["built_at"])
	}
}

func TestCORSHeader(t *testing.T) {
	_, ts := newTestServer(t)
	resp, err := http.Get(ts.URL + "/api/v1/stats")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("ACAO = %q", got)
	}
	if got := resp.Header.Get("Vary"); got == "" {
		t.Errorf("Vary header missing")
	}
}

func TestLatestOrdering(t *testing.T) {
	_, ts := newTestServer(t)
	code, body := getJSON(t, ts.URL, "/api/v1/works/latest")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	works := body["works"].([]any)
	// The fixture has 4 works, but 3 share the stormlight series and NULL
	// added_at: the per-series diversity cap (2) drops the third, so 3 remain.
	if len(works) != 3 {
		t.Fatalf("latest returned %d works, want 3 (series cap)", len(works))
	}
	// project-hail-mary has an added_at; the others are NULL and sort by
	// title, so PHM must be first.
	first := works[0].(map[string]any)
	if first["id"] != "project-hail-mary" {
		t.Errorf("latest[0] = %v, want project-hail-mary", first["id"])
	}
	if first["added_at"] != "2026-07-10T00:00:00Z" {
		t.Errorf("added_at = %v", first["added_at"])
	}
	// A null-added work still serializes added_at as null (not omitted).
	last := works[2].(map[string]any)
	if v, ok := last["added_at"]; !ok || v != nil {
		t.Errorf("null-added work added_at = %v (present=%v)", v, ok)
	}
}

func TestLatestLimitClamp(t *testing.T) {
	_, ts := newTestServer(t)
	code, body := getJSON(t, ts.URL, "/api/v1/works/latest?limit=999")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	// Clamped to 50 (max); the fixture yields 3 after the series cap.
	if n := len(body["works"].([]any)); n != 3 {
		t.Errorf("got %d works", n)
	}
	code, body = getJSON(t, ts.URL, "/api/v1/works/latest?limit=1")
	if code != 200 || len(body["works"].([]any)) != 1 {
		t.Errorf("limit=1 -> %d works", len(body["works"].([]any)))
	}
}

// TestLatestSeriesDiversityCap: 5 works in one series interleaved (by title)
// with 3 standalone works, all sharing NULL added_at, so the title tie-break
// governs. The cap must keep only the first 2 series volumes, never suppress a
// standalone work, and preserve the ordering of everything it keeps.
func TestLatestSeriesDiversityCap(t *testing.T) {
	author := &model.Person{ID: "prolific-author", Name: "Prolific Author", License: "CC0-1.0"}
	mkWork := func(id, title string) *model.Work {
		return &model.Work{
			ID: id, Title: title, Language: "en",
			Authors: []string{"prolific-author"}, License: "CC0-1.0",
		}
	}
	cat := &model.Catalog{
		People: []*model.Person{author},
		Works: []*model.Work{
			mkWork("saga-one", "A Saga One"),
			mkWork("alone-one", "B Alone One"),
			mkWork("saga-two", "C Saga Two"),
			mkWork("saga-three", "D Saga Three"),
			mkWork("alone-two", "E Alone Two"),
			mkWork("saga-four", "F Saga Four"),
			mkWork("saga-five", "G Saga Five"),
			mkWork("alone-three", "H Alone Three"),
		},
		Series: []*model.Series{{
			ID: "the-saga", Name: "The Saga", License: "CC0-1.0",
			Authors: []string{"prolific-author"},
			Works: []model.SeriesWork{
				{Work: "saga-one", Position: "1"},
				{Work: "saga-two", Position: "2"},
				{Work: "saga-three", Position: "3"},
				{Work: "saga-four", Position: "4"},
				{Work: "saga-five", Position: "5"},
			},
		}},
	}
	dbPath := buildFixtureDB(t, cat, nil)
	srv, err := New(Config{DBPath: dbPath, swapGrace: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	code, body := getJSON(t, ts.URL, "/api/v1/works/latest?limit=8")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	works := body["works"].([]any)
	var got []string
	saga := 0
	for _, w := range works {
		m := w.(map[string]any)
		got = append(got, m["id"].(string))
		if m["series"] != nil {
			saga++
		}
	}
	// Title-order walk with the cap: saga-one, alone-one, saga-two kept; the
	// remaining saga volumes skipped; every standalone work kept, in order.
	want := []string{"saga-one", "alone-one", "saga-two", "alone-two", "alone-three"}
	if len(got) != len(want) {
		t.Fatalf("latest = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("latest = %v, want %v", got, want)
		}
	}
	if saga != 2 {
		t.Errorf("series works in latest = %d, want exactly 2", saga)
	}

	// The limit still binds after capping.
	code, body = getJSON(t, ts.URL, "/api/v1/works/latest?limit=3")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	works = body["works"].([]any)
	if len(works) != 3 || works[2].(map[string]any)["id"] != "saga-two" {
		t.Errorf("limit=3 latest = %v", works)
	}
}

func TestWorkDetail(t *testing.T) {
	_, ts := newTestServer(t)
	code, body := getJSON(t, ts.URL, "/api/v1/works/project-hail-mary")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	if body["title"] != "Project Hail Mary" {
		t.Errorf("title = %v", body["title"])
	}
	authors := body["authors"].([]any)
	if len(authors) != 1 || authors[0].(map[string]any)["id"] != "andy-weir" {
		t.Errorf("authors = %v", authors)
	}
	recs := body["recordings"].([]any)
	if len(recs) != 1 {
		t.Fatalf("recordings = %d", len(recs))
	}
	r := recs[0].(map[string]any)
	if r["id"] != "ray-porter-2021" {
		t.Errorf("recording id = %v", r["id"])
	}
	if r["chapter_count"].(float64) != 3 {
		t.Errorf("chapter_count = %v", r["chapter_count"])
	}
	asin := r["asin"].([]any)
	if len(asin) != 1 || asin[0].(map[string]any)["asin"] != "B08G9PRS1K" {
		t.Errorf("asin = %v", asin)
	}
}

func TestWorkDetailCharactersRecaps(t *testing.T) {
	_, ts := newTestServer(t)
	code, body := getJSON(t, ts.URL, "/api/v1/works/project-hail-mary")
	if code != 200 {
		t.Fatalf("status %d", code)
	}

	chars, ok := body["characters"].([]any)
	if !ok || len(chars) != 2 {
		t.Fatalf("characters = %v", body["characters"])
	}
	// Authored order preserved: protagonist first.
	c0 := chars[0].(map[string]any)
	if c0["id"] != "ryland-grace" || c0["role"] != "protagonist" {
		t.Errorf("character[0] = %v", c0)
	}
	if c0["reveal"].(map[string]any)["chapter"].(float64) != 1 {
		t.Errorf("reveal = %v", c0["reveal"])
	}
	aliases := c0["aliases"].([]any)
	if len(aliases) != 1 || aliases[0] != "Dr. Grace" {
		t.Errorf("aliases = %v", aliases)
	}
	if c0["xref"].(map[string]any)["wikidata"] != "Q110001" {
		t.Errorf("xref = %v", c0["xref"])
	}
	// The character with no aliases/xref omits those keys.
	c1 := chars[1].(map[string]any)
	if c1["id"] != "rocky" {
		t.Errorf("character[1] = %v", c1)
	}
	if _, has := c1["aliases"]; has {
		t.Errorf("rocky should omit empty aliases")
	}

	recaps, ok := body["recaps"].([]any)
	if !ok || len(recaps) != 2 {
		t.Fatalf("recaps = %v", body["recaps"])
	}
	// Served in position order (chapter 2 before 9).
	if recaps[0].(map[string]any)["through"].(map[string]any)["chapter"].(float64) != 2 {
		t.Errorf("recap[0] through = %v", recaps[0])
	}
	if recaps[1].(map[string]any)["through"].(map[string]any)["chapter"].(float64) != 9 {
		t.Errorf("recap[1] through = %v", recaps[1])
	}

	// A work with no sidecars omits the keys entirely (omitempty).
	_, wbody := getJSON(t, ts.URL, "/api/v1/works/the-way-of-kings")
	if _, has := wbody["characters"]; has {
		t.Errorf("work without characters should omit the key")
	}
	if _, has := wbody["recaps"]; has {
		t.Errorf("work without recaps should omit the key")
	}
}

func TestWorkDetailRecapSummary(t *testing.T) {
	_, ts := newTestServer(t)
	code, body := getJSON(t, ts.URL, "/api/v1/works/project-hail-mary")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	sum, ok := body["recap_summary"].(map[string]any)
	if !ok {
		t.Fatalf("recap_summary = %v", body["recap_summary"])
	}
	if sum["in_short"] == "" || sum["in_short"] == nil {
		t.Errorf("in_short = %v", sum["in_short"])
	}
	if sum["ending"] == "" || sum["ending"] == nil {
		t.Errorf("ending = %v", sum["ending"])
	}

	// A work whose recaps sidecar has no summary fields omits the key entirely.
	_, wbody := getJSON(t, ts.URL, "/api/v1/works/the-way-of-kings")
	if _, has := wbody["recap_summary"]; has {
		t.Errorf("work without a recap summary should omit the key")
	}
}

// TestRecapSummaryToleratesV2Artifact simulates a newer metaserve binary
// serving an older (schema_version 2) artifact that has the characters/recaps
// tables but not recap_summaries: the summary query no-ops on the version, so
// the work still serves with its characters/recaps but no recap_summary.
func TestRecapSummaryToleratesV2Artifact(t *testing.T) {
	added := map[string]string{"project-hail-mary": "2026-07-10T00:00:00Z"}
	dbPath := buildFixtureDB(t, fixtureCatalog(), added)

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	stmts := []string{
		"DROP TABLE recap_summaries",
		"UPDATE meta SET value='2' WHERE key='schema_version'",
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	srv, err := New(Config{DBPath: dbPath, swapGrace: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	code, body := getJSON(t, ts.URL, "/api/v1/works/project-hail-mary")
	if code != 200 {
		t.Fatalf("status %d, body %v", code, body)
	}
	if body["error"] != nil {
		t.Errorf("expected no error, got %v", body["error"])
	}
	if _, has := body["recap_summary"]; has {
		t.Errorf("missing recap_summaries table should yield no recap_summary key")
	}
	// The v2 sidecars (characters/recaps) are still served.
	if _, has := body["recaps"]; !has {
		t.Errorf("v2 artifact should still serve recaps")
	}
}

// TestWorkDetailToleratesOlderArtifact simulates a newer metaserve binary
// briefly serving an older (schema_version 1) artifact that predates the
// characters/recaps tables: the sidecar queries no-op on the version, so the
// work still serves, just without them.
func TestWorkDetailToleratesOlderArtifact(t *testing.T) {
	added := map[string]string{"project-hail-mary": "2026-07-10T00:00:00Z"}
	dbPath := buildFixtureDB(t, fixtureCatalog(), added)

	// Roll the artifact back to a v1 shape: the tables are gone and the stamped
	// version says 1, exactly as a pre-sidecar release would look.
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	stmts := []string{
		"DROP TABLE characters",
		"DROP TABLE character_aliases",
		"DROP TABLE recaps",
		"DROP TABLE recap_summaries",
		"UPDATE meta SET value='1' WHERE key='schema_version'",
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	srv, err := New(Config{DBPath: dbPath, swapGrace: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	code, body := getJSON(t, ts.URL, "/api/v1/works/project-hail-mary")
	if code != 200 {
		t.Fatalf("status %d, body %v", code, body)
	}
	if body["error"] != nil {
		t.Errorf("expected no error, got %v", body["error"])
	}
	if _, has := body["characters"]; has {
		t.Errorf("missing table should yield no characters key, got %v", body["characters"])
	}
	if _, has := body["recaps"]; has {
		t.Errorf("missing table should yield no recaps key")
	}
	if _, has := body["recap_summary"]; has {
		t.Errorf("missing table should yield no recap_summary key")
	}
}

func TestWorkNotFound(t *testing.T) {
	_, ts := newTestServer(t)
	code, body := getJSON(t, ts.URL, "/api/v1/works/nope")
	if code != 404 {
		t.Fatalf("status %d", code)
	}
	if body["error"] == nil {
		t.Errorf("expected error body, got %v", body)
	}
}

func TestChapters(t *testing.T) {
	_, ts := newTestServer(t)
	code, body := getJSON(t, ts.URL, "/api/v1/works/project-hail-mary/recordings/ray-porter-2021/chapters")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	chs := body["chapters"].([]any)
	if len(chs) != 3 {
		t.Fatalf("chapters = %d", len(chs))
	}
	if chs[0].(map[string]any)["title"] != "Opening Credits" {
		t.Errorf("first chapter = %v", chs[0])
	}
}

func TestPerson(t *testing.T) {
	_, ts := newTestServer(t)
	code, body := getJSON(t, ts.URL, "/api/v1/people/brandon-sanderson")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	if body["name"] != "Brandon Sanderson" {
		t.Errorf("name = %v", body["name"])
	}
	authored := body["authored"].([]any)
	if len(authored) != 3 { // way-of-kings, words-of-radiance, edgedancer
		t.Errorf("authored = %d", len(authored))
	}

	// A narrator has narrated entries carrying the recording id.
	code, body = getJSON(t, ts.URL, "/api/v1/people/ray-porter")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	nar := body["narrated"].([]any)
	if len(nar) != 1 {
		t.Fatalf("narrated = %d", len(nar))
	}
	e := nar[0].(map[string]any)
	if e["recording_id"] != "ray-porter-2021" {
		t.Errorf("narrated recording_id = %v", e["recording_id"])
	}
	if e["work"].(map[string]any)["id"] != "project-hail-mary" {
		t.Errorf("narrated work = %v", e["work"])
	}
}

func TestPersonNotFound(t *testing.T) {
	_, ts := newTestServer(t)
	code, _ := getJSON(t, ts.URL, "/api/v1/people/nobody")
	if code != 404 {
		t.Fatalf("status %d", code)
	}
}

func TestSeriesNumericOrder(t *testing.T) {
	_, ts := newTestServer(t)
	code, body := getJSON(t, ts.URL, "/api/v1/series/the-stormlight-archive")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	works := body["works"].([]any)
	var order []string
	for _, w := range works {
		order = append(order, w.(map[string]any)["position"].(string))
	}
	want := []string{"1", "2", "10"} // numeric, not lexical ("10" would precede "2")
	if len(order) != 3 || order[0] != want[0] || order[1] != want[1] || order[2] != want[2] {
		t.Errorf("series positions = %v, want %v", order, want)
	}
}

func TestLookupASIN(t *testing.T) {
	_, ts := newTestServer(t)
	code, body := getJSON(t, ts.URL, "/api/v1/lookup?asin=B08G9PRS1K")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	if body["recording_id"] != "ray-porter-2021" {
		t.Errorf("recording_id = %v", body["recording_id"])
	}
	if body["work"].(map[string]any)["id"] != "project-hail-mary" {
		t.Errorf("work = %v", body["work"])
	}
}

func TestLookupISBN(t *testing.T) {
	_, ts := newTestServer(t)
	code, body := getJSON(t, ts.URL, "/api/v1/lookup?isbn=9781427209269")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	if body["work"].(map[string]any)["id"] != "the-way-of-kings" {
		t.Errorf("work = %v", body["work"])
	}
	if body["recording_id"] != "kramer-reading-2010" {
		t.Errorf("recording_id = %v", body["recording_id"])
	}
}

func TestLookupMissingParam(t *testing.T) {
	_, ts := newTestServer(t)
	code, _ := getJSON(t, ts.URL, "/api/v1/lookup")
	if code != 400 {
		t.Fatalf("status %d, want 400", code)
	}
	code, _ = getJSON(t, ts.URL, "/api/v1/lookup?asin=ZZZNOPE")
	if code != 404 {
		t.Fatalf("status %d, want 404", code)
	}
}

func TestSearch(t *testing.T) {
	_, ts := newTestServer(t)
	code, body := getJSON(t, ts.URL, "/api/v1/search?q=hail")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	results := body["results"].([]any)
	found := false
	for _, r := range results {
		m := r.(map[string]any)
		if m["kind"] == "work" && m["id"] == "project-hail-mary" {
			found = true
			if _, ok := m["narrators"]; !ok {
				t.Errorf("work result missing narrators")
			}
		}
	}
	if !found {
		t.Errorf("search 'hail' did not find project-hail-mary: %v", results)
	}
}

func TestSearchPrefixAndKinds(t *testing.T) {
	_, ts := newTestServer(t)
	// "sand" is a prefix of Sanderson (person) and matches works via author name.
	code, body := getJSON(t, ts.URL, "/api/v1/search?q=sand")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	kinds := map[string]bool{}
	for _, r := range body["results"].([]any) {
		kinds[r.(map[string]any)["kind"].(string)] = true
	}
	if !kinds["person"] {
		t.Errorf("prefix 'sand' did not return a person result: %v", kinds)
	}

	// A series query returns a series result carrying a works count.
	code, body = getJSON(t, ts.URL, "/api/v1/search?q=stormlight")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	sawSeries := false
	for _, r := range body["results"].([]any) {
		m := r.(map[string]any)
		if m["kind"] == "series" {
			sawSeries = true
			if m["works"].(float64) != 3 {
				t.Errorf("series works count = %v", m["works"])
			}
		}
	}
	if !sawSeries {
		t.Errorf("no series result for 'stormlight'")
	}
}

func TestSearchQuoteEscaping(t *testing.T) {
	_, ts := newTestServer(t)
	// A query full of double quotes must not 500 (FTS escaping), and empty q is 400.
	code, _ := getJSON(t, ts.URL, `/api/v1/search?q=%22%22%22`)
	if code != 200 {
		t.Fatalf("quote query status = %d, want 200", code)
	}
	code, _ = getJSON(t, ts.URL, "/api/v1/search?q=%20%20")
	if code != 400 {
		t.Fatalf("empty q status = %d, want 400", code)
	}
}

func TestFTSQueryBuilder(t *testing.T) {
	cases := map[string]string{
		"hail mary": `"hail" "mary"*`,
		"dragon":    `"dragon"*`,
		`a"b`:       `"a""b"*`,
		`"""`:       `""""""""*`, // 3 quotes -> 6 escaped -> wrapped in a pair = 8, then '*'
		"   ":       `""`,
	}
	for in, want := range cases {
		if got := ftsQuery(in); got != want {
			t.Errorf("ftsQuery(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPositionStart(t *testing.T) {
	cases := map[string]float64{
		"1": 1, "2.5": 2.5, "1-3.5": 1, "10": 10, "": 1e18, "abc": 1e18,
	}
	for in, want := range cases {
		if got := positionStart(in); got != want {
			t.Errorf("positionStart(%q) = %v, want %v", in, got, want)
		}
	}
}

// TestHotSwap builds two artifacts with different work counts, hammers /stats
// concurrently, swaps mid-flight, and asserts the stat flips atomically without
// a race (run under -race).
func TestHotSwap(t *testing.T) {
	added := map[string]string{"project-hail-mary": "2026-07-10T00:00:00Z"}
	db1 := buildFixtureDB(t, fixtureCatalog(), added)

	cat2 := fixtureCatalog()
	cat2.Works = append(cat2.Works, &model.Work{
		ID: "artemis", Title: "Artemis", Language: "en",
		Authors: []string{"andy-weir"}, License: "CC0-1.0",
	})
	db2 := buildFixtureDB(t, cat2, added)

	srv, err := New(Config{DBPath: db1, swapGrace: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	handler := srv.Handler()

	snap2, err := openSnapshot(db2, "v2")
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	var bad atomic.Int32
	stop := make(chan struct{})
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				rec := httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodGet, "/api/v1/stats", nil)
				handler.ServeHTTP(rec, req)
				var st Stats
				if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
					bad.Add(1)
					return
				}
				if st.Works != 4 && st.Works != 5 {
					bad.Add(1)
					return
				}
			}
		}()
	}
	time.Sleep(10 * time.Millisecond)
	srv.swap(snap2)
	time.Sleep(20 * time.Millisecond)
	close(stop)
	wg.Wait()

	if bad.Load() != 0 {
		t.Fatalf("%d requests observed an inconsistent state", bad.Load())
	}
	if got := srv.current().stats.Works; got != 5 {
		t.Errorf("after swap works = %d, want 5", got)
	}
}
