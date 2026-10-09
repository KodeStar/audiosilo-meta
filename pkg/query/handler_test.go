package query

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/artifacttest"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

func TestHealthz(t *testing.T) {
	_, ts := newTestServer(t)
	code, body := artifacttest.GetJSON(t, ts.URL, "/healthz")
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
	code, body := artifacttest.GetJSON(t, ts.URL, "/api/v1/stats")
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

func TestLatestOrdering(t *testing.T) {
	_, ts := newTestServer(t)
	code, body := artifacttest.GetJSON(t, ts.URL, "/api/v1/works/latest")
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
	code, body := artifacttest.GetJSON(t, ts.URL, "/api/v1/works/latest?limit=999")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	// Clamped to 50 (max); the fixture yields 3 after the series cap.
	if n := len(body["works"].([]any)); n != 3 {
		t.Errorf("got %d works", n)
	}
	code, body = artifacttest.GetJSON(t, ts.URL, "/api/v1/works/latest?limit=1")
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
	dbPath := artifacttest.Build(t, cat)
	_, ts := serveDB(t, dbPath, HandlerOptions{})

	code, body := artifacttest.GetJSON(t, ts.URL, "/api/v1/works/latest?limit=8")
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
	code, body = artifacttest.GetJSON(t, ts.URL, "/api/v1/works/latest?limit=3")
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
	code, body := artifacttest.GetJSON(t, ts.URL, "/api/v1/works/project-hail-mary")
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

func TestWorkDetailDerivesPurchaseLinksFromIdentifiers(t *testing.T) {
	catalog := &model.Catalog{
		People: []*model.Person{
			{ID: "madeline-miller", Name: "Madeline Miller", License: "CC0-1.0"},
			{ID: "perdita-weeks", Name: "Perdita Weeks", License: "CC0-1.0"},
		},
		Works: []*model.Work{{
			ID: "circe", Title: "Circe", Authors: []string{"madeline-miller"}, Language: "en", License: "CC0-1.0",
			Recordings: []*model.Recording{{
				ID: "perdita-weeks-2018", Work: "circe", Narrators: []string{"perdita-weeks"}, Language: "en", License: "CC0-1.0",
				ASIN: []model.ASIN{{Region: "us", ASIN: "B0794BXZBF"}}, ISBN: []model.ISBNRef{{ISBN: "9781478975311"}},
			}},
		}},
	}
	_, ts := newTestServerForCatalog(t, catalog)

	code, body := artifacttest.GetJSON(t, ts.URL, "/api/v1/works/circe")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	// Field-level truth is pinned by TestDerivedPurchaseLinks; here only what
	// the handler level adds: the links reach the wire, in order, and the
	// unscoped ISBN link omits region on the wire.
	links := body["recordings"].([]any)[0].(map[string]any)["purchase_links"].([]any)
	if len(links) != 2 {
		t.Fatalf("purchase_links = %v", links)
	}
	if url := links[0].(map[string]any)["url"]; url != "https://www.audible.com/pd/B0794BXZBF" {
		t.Errorf("Audible purchase link url = %v", url)
	}
	libro := links[1].(map[string]any)
	if libro["url"] != "https://libro.fm/audiobooks/9781478975311" {
		t.Errorf("Libro.fm purchase link url = %v", libro["url"])
	}
	if _, has := libro["region"]; has {
		t.Errorf("unscoped ISBN link should omit region: %v", libro)
	}
}

func TestWorkDetailCharactersRecaps(t *testing.T) {
	_, ts := newTestServer(t)
	code, body := artifacttest.GetJSON(t, ts.URL, "/api/v1/works/project-hail-mary")
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
	_, wbody := artifacttest.GetJSON(t, ts.URL, "/api/v1/works/the-way-of-kings")
	if _, has := wbody["characters"]; has {
		t.Errorf("work without characters should omit the key")
	}
	if _, has := wbody["recaps"]; has {
		t.Errorf("work without recaps should omit the key")
	}
}

func TestWorkDetailRecapSummary(t *testing.T) {
	_, ts := newTestServer(t)
	code, body := artifacttest.GetJSON(t, ts.URL, "/api/v1/works/project-hail-mary")
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
	_, wbody := artifacttest.GetJSON(t, ts.URL, "/api/v1/works/the-way-of-kings")
	if _, has := wbody["recap_summary"]; has {
		t.Errorf("work without a recap summary should omit the key")
	}
}

func TestWorkDetailGenres(t *testing.T) {
	_, ts := newTestServer(t)
	code, body := artifacttest.GetJSON(t, ts.URL, "/api/v1/works/project-hail-mary")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	genres, ok := body["genres"].([]any)
	if !ok {
		t.Fatalf("genres = %v", body["genres"])
	}
	if len(genres) != 2 || genres[0] != "hard-science-fiction" || genres[1] != "science-fiction" {
		t.Errorf("genres = %v", genres)
	}

	// A work with no genres omits the key entirely (omitempty).
	_, wbody := artifacttest.GetJSON(t, ts.URL, "/api/v1/works/the-way-of-kings")
	if _, has := wbody["genres"]; has {
		t.Errorf("work without genres should omit the key, got %v", wbody["genres"])
	}
}

// TestGenresToleratesV3Artifact serves a schema_version 3 artifact that predates
// the work_genres table: the genre query no-ops on the version, so the work still
// serves without genres while its v3 payload keeps working.
func TestGenresToleratesV3Artifact(t *testing.T) {
	ts := downgradedServer(t, artifacttest.Downgraded(t, artifacttest.Fixture(), 3, "work_genres"))
	code, body := artifacttest.GetJSON(t, ts.URL, "/api/v1/works/project-hail-mary")
	if code != 200 {
		t.Fatalf("status %d, body %v", code, body)
	}
	if body["error"] != nil {
		t.Errorf("expected no error, got %v", body["error"])
	}
	if _, has := body["genres"]; has {
		t.Errorf("missing work_genres table should yield no genres key, got %v", body["genres"])
	}
	// The v3 payload (recaps + the recap summary) is still served.
	if _, has := body["recap_summary"]; !has {
		t.Errorf("v3 artifact should still serve recap_summary")
	}
}

// TestGenresToleratesV3ArtifactABS covers the same downgrade on the ABS facade,
// whose batched genre lookup gates on the version separately from workGenres.
func TestGenresToleratesV3ArtifactABS(t *testing.T) {
	ts := downgradedServer(t, artifacttest.Downgraded(t, artifacttest.Fixture(), 3, "work_genres"))
	code, matches := absMatches(t, ts.URL, "/abs/search?query=hail")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	if len(matches) == 0 {
		t.Fatalf("expected a match on a v3 artifact")
	}
	m := matches[0].(map[string]any)
	if _, has := m["genres"]; has {
		t.Errorf("missing work_genres table should yield no genres key, got %v", m["genres"])
	}
}

// TestRecapSummaryToleratesV2Artifact serves a schema_version 2 artifact that has
// the characters/recaps tables but not recap_summaries: the summary query no-ops
// on the version, so the work still serves its characters/recaps.
func TestRecapSummaryToleratesV2Artifact(t *testing.T) {
	ts := downgradedServer(t, artifacttest.Downgraded(t, artifacttest.Fixture(), 2, "work_genres", "recap_summaries"))
	code, body := artifacttest.GetJSON(t, ts.URL, "/api/v1/works/project-hail-mary")
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

// TestCommunityDescription covers the works-community description member end to
// end: the artifact's work_descriptions row reaches GET /works/{id} as an OBJECT
// under its own key, carrying the share-alike license the row states - it is
// deliberately NOT folded into the CC0 `description` string, which stays the work
// record's own field.
func TestCommunityDescription(t *testing.T) {
	_, ts := newTestServer(t)
	_, body := artifacttest.GetJSON(t, ts.URL, "/api/v1/works/project-hail-mary")
	cd, ok := body["community_description"].(map[string]any)
	if !ok {
		t.Fatalf("community_description = %v, want an object", body["community_description"])
	}
	if text, _ := cd["text"].(string); !strings.Contains(text, "wakes alone aboard a ship") {
		t.Errorf("community_description.text = %q", cd["text"])
	}
	if cd["license"] != "CC-BY-SA-4.0" {
		t.Errorf("community_description.license = %v, want CC-BY-SA-4.0", cd["license"])
	}
	if _, has := body["description"]; has {
		t.Errorf("the CC0 description key must stay absent for a work whose record states none, got %v", body["description"])
	}

	// A work with no description member omits the key entirely.
	_, plain := artifacttest.GetJSON(t, ts.URL, "/api/v1/works/the-way-of-kings")
	if _, has := plain["community_description"]; has {
		t.Errorf("a work with no description sidecar should omit the key, got %v", plain["community_description"])
	}
}

// TestCommunityDescriptionToleratesV5Artifact serves a schema_version 5 artifact
// that predates work_descriptions: the query no-ops on the version, so the work
// still serves and its pages fall back to the composed fact sentence.
func TestCommunityDescriptionToleratesV5Artifact(t *testing.T) {
	ts := downgradedServer(t, artifacttest.Downgraded(t, artifacttest.Fixture(), 5, "work_descriptions"))
	code, body := artifacttest.GetJSON(t, ts.URL, "/api/v1/works/project-hail-mary")
	if code != 200 {
		t.Fatalf("status %d, body %v", code, body)
	}
	if body["error"] != nil {
		t.Errorf("expected no error, got %v", body["error"])
	}
	if _, has := body["community_description"]; has {
		t.Errorf("missing work_descriptions table should yield no community_description key")
	}
	// The v5 payload (the sidecars that DO exist) is still served.
	if _, has := body["recap_summary"]; !has {
		t.Errorf("v5 artifact should still serve recap_summary")
	}
}

// TestCommunityDescriptionRefusesAVersion6ArtifactWithoutTheTable is the OTHER
// side of the version gate, and the reason it is asked at LOAD rather than per
// request: an artifact that CLAIMS version 6 without work_descriptions is a
// corrupt file, not an old one - the builder writes the version and the table
// together - and per-request it would 500 every works/{id} and every
// /abs/search candidate while the boot itself looked fine. The redirects table
// is refused the same way, and the error names the claim being enforced.
func TestCommunityDescriptionRefusesAVersion6ArtifactWithoutTheTable(t *testing.T) {
	dbPath := artifacttest.Downgraded(t, artifacttest.Fixture(), 6, "work_descriptions")
	_, err := Open(dbPath, "")
	if err == nil {
		t.Fatal("a version 6 artifact with no work_descriptions table opened cleanly")
	}
	for _, want := range []string{"schema_version 6", "work_descriptions"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

// TestDescriptionMemoSkipsTheQueryWhenTheTableIsEmpty pins the load-time memo the
// per-request gate reads: with no description in the catalogue - which is every
// release until the layer has data - the work page, the guide pages and the ABS
// candidates must not touch work_descriptions at all.
func TestDescriptionMemoSkipsTheQueryWhenTheTableIsEmpty(t *testing.T) {
	cat := artifacttest.Fixture()
	cat.Descriptions = nil
	empty := openTestDB(t, artifacttest.Build(t, cat))
	if empty.hasDescriptions {
		t.Error("hasDescriptions is true for a catalogue holding no descriptions")
	}
	got, err := empty.communityDescriptionOf("project-hail-mary")
	if err != nil || got != nil {
		t.Errorf("communityDescriptionOf = %v, %v; want nil, nil", got, err)
	}
	if full := openTestDB(t, artifacttest.Build(t, artifacttest.Fixture())); !full.hasDescriptions {
		t.Error("hasDescriptions is false for a catalogue that holds one")
	}
}

// TestWorkDetailToleratesOlderArtifact serves a schema_version 1 artifact that
// predates the characters/recaps tables: every sidecar query no-ops on the
// version, so the work still serves, just without them.
func TestWorkDetailToleratesOlderArtifact(t *testing.T) {
	ts := downgradedServer(t, artifacttest.Downgraded(t, artifacttest.Fixture(), 1,
		"work_genres", "characters", "character_aliases", "recaps", "recap_summaries", "work_descriptions"))
	code, body := artifacttest.GetJSON(t, ts.URL, "/api/v1/works/project-hail-mary")
	if code != 200 {
		t.Fatalf("status %d, body %v", code, body)
	}
	if body["error"] != nil {
		t.Errorf("expected no error, got %v", body["error"])
	}
	for _, key := range []string{"genres", "characters", "recaps", "recap_summary", "community_description"} {
		if _, has := body[key]; has {
			t.Errorf("missing table should yield no %s key, got %v", key, body[key])
		}
	}
}

func TestWorkNotFound(t *testing.T) {
	_, ts := newTestServer(t)
	code, body := artifacttest.GetJSON(t, ts.URL, "/api/v1/works/nope")
	if code != 404 {
		t.Fatalf("status %d", code)
	}
	if body["error"] == nil {
		t.Errorf("expected error body, got %v", body)
	}
}

func TestChapters(t *testing.T) {
	_, ts := newTestServer(t)
	code, body := artifacttest.GetJSON(t, ts.URL, "/api/v1/works/project-hail-mary/recordings/ray-porter-2021/chapters")
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
	code, body := artifacttest.GetJSON(t, ts.URL, "/api/v1/people/brandon-sanderson")
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
	code, body = artifacttest.GetJSON(t, ts.URL, "/api/v1/people/ray-porter")
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

// TestPersonPagination pins the additive window on a person's credit lists: the
// default returns everything a small person has (with the unpaged totals
// alongside), and ?limit/?offset slice the title-ordered list without changing
// the totals.
func TestPersonPagination(t *testing.T) {
	_, ts := newTestServer(t)

	code, body := artifacttest.GetJSON(t, ts.URL, "/api/v1/people/brandon-sanderson")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	if got := body["authored_total"].(float64); got != 3 {
		t.Errorf("authored_total = %v, want 3", got)
	}
	if got := body["narrated_total"].(float64); got != 0 {
		t.Errorf("narrated_total = %v, want 0", got)
	}
	if got := body["limit"].(float64); got != personPageDefault {
		t.Errorf("limit = %v, want the default %d", got, personPageDefault)
	}

	// The full title order, to slice against.
	var all []string
	for _, w := range body["authored"].([]any) {
		all = append(all, w.(map[string]any)["id"].(string))
	}
	if len(all) != 3 {
		t.Fatalf("authored = %v, want 3 works", all)
	}

	code, body = artifacttest.GetJSON(t, ts.URL, "/api/v1/people/brandon-sanderson?limit=1&offset=1")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	page := body["authored"].([]any)
	if len(page) != 1 || page[0].(map[string]any)["id"] != all[1] {
		t.Errorf("page = %v, want just %q", page, all[1])
	}
	if got := body["authored_total"].(float64); got != 3 {
		t.Errorf("authored_total under a window = %v, want the unpaged 3", got)
	}
	if got := body["offset"].(float64); got != 1 {
		t.Errorf("offset = %v, want 1", got)
	}

	// An over-large limit is clamped, not honoured verbatim.
	_, body = artifacttest.GetJSON(t, ts.URL, "/api/v1/people/brandon-sanderson?limit=999999")
	if got := body["limit"].(float64); got != PersonPageMax {
		t.Errorf("limit = %v, want the clamp %d", got, PersonPageMax)
	}
}

// TestSeriesPagination pins the series window: absent ?limit means the WHOLE
// series (audiosilo-server composes the player's series rail from it), and an
// explicit window slices the position-ordered list.
func TestSeriesPagination(t *testing.T) {
	_, ts := newTestServer(t)

	code, body := artifacttest.GetJSON(t, ts.URL, "/api/v1/series/the-stormlight-archive")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	if got := len(body["works"].([]any)); got != 3 {
		t.Errorf("default works = %d, want all 3", got)
	}
	if got := body["works_total"].(float64); got != 3 {
		t.Errorf("works_total = %v, want 3", got)
	}
	if got := body["limit"].(float64); got != 0 {
		t.Errorf("limit = %v, want 0 (no window)", got)
	}

	_, body = artifacttest.GetJSON(t, ts.URL, "/api/v1/series/the-stormlight-archive?limit=1&offset=1")
	works := body["works"].([]any)
	if len(works) != 1 {
		t.Fatalf("windowed works = %d, want 1", len(works))
	}
	if got := works[0].(map[string]any)["position"]; got != "2" {
		t.Errorf("windowed position = %v, want \"2\" (position order, offset 1)", got)
	}
	if got := body["works_total"].(float64); got != 3 {
		t.Errorf("works_total under a window = %v, want the unpaged 3", got)
	}

	// An offset past the end is an empty page, not an error.
	code, body = artifacttest.GetJSON(t, ts.URL, "/api/v1/series/the-stormlight-archive?offset=99")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	if got := len(body["works"].([]any)); got != 0 {
		t.Errorf("works past the end = %d, want 0", got)
	}
}

func TestPersonNotFound(t *testing.T) {
	_, ts := newTestServer(t)
	code, _ := artifacttest.GetJSON(t, ts.URL, "/api/v1/people/nobody")
	if code != 404 {
		t.Fatalf("status %d", code)
	}
}

func TestSeriesNumericOrder(t *testing.T) {
	_, ts := newTestServer(t)
	code, body := artifacttest.GetJSON(t, ts.URL, "/api/v1/series/the-stormlight-archive")
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
	code, body := artifacttest.GetJSON(t, ts.URL, "/api/v1/lookup?asin=B08G9PRS1K")
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
	code, body := artifacttest.GetJSON(t, ts.URL, "/api/v1/lookup?isbn=9781427209269")
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
	code, _ := artifacttest.GetJSON(t, ts.URL, "/api/v1/lookup")
	if code != 400 {
		t.Fatalf("status %d, want 400", code)
	}
	code, _ = artifacttest.GetJSON(t, ts.URL, "/api/v1/lookup?asin=ZZZNOPE")
	if code != 404 {
		t.Fatalf("status %d, want 404", code)
	}
}

func TestSearch(t *testing.T) {
	_, ts := newTestServer(t)
	code, body := artifacttest.GetJSON(t, ts.URL, "/api/v1/search?q=hail")
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
	code, body := artifacttest.GetJSON(t, ts.URL, "/api/v1/search?q=sand")
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
	code, body = artifacttest.GetJSON(t, ts.URL, "/api/v1/search?q=stormlight")
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
	code, _ := artifacttest.GetJSON(t, ts.URL, `/api/v1/search?q=%22%22%22`)
	if code != 200 {
		t.Fatalf("quote query status = %d, want 200", code)
	}
	code, _ = artifacttest.GetJSON(t, ts.URL, "/api/v1/search?q=%20%20")
	if code != 400 {
		t.Fatalf("empty q status = %d, want 400", code)
	}
}

// searchResultKinds fetches a search page and counts its results by kind. A
// type-scoped page must carry exactly one key.
func searchResultKinds(t *testing.T, base, path string) map[string]int {
	t.Helper()
	code, body := artifacttest.GetJSON(t, base, path)
	if code != 200 {
		t.Fatalf("GET %s: status %d", path, code)
	}
	out := map[string]int{}
	for _, r := range body["results"].([]any) {
		out[r.(map[string]any)["kind"].(string)]++
	}
	return out
}

// TestTypedSearchScopesToOneKind is the point of the type-scoped endpoints: a
// query the combined search answers with several kinds answers with exactly one
// on each scoped route.
//
// It takes two queries because no single fixture term reaches all three kinds:
// "sanderson" is a person's name and rides along in every one of his works' FTS
// names column, while "stormlight" is a series name and rides along in the same
// works. Between them the combined page covers work, person and series.
func TestTypedSearchScopesToOneKind(t *testing.T) {
	_, ts := newTestServer(t)

	byPerson := searchResultKinds(t, ts.URL, "/api/v1/search?q=sanderson")
	if byPerson["work"] == 0 || byPerson["person"] == 0 {
		t.Fatalf("combined search for 'sanderson' = %v, want works and a person", byPerson)
	}
	bySeries := searchResultKinds(t, ts.URL, "/api/v1/search?q=stormlight")
	if bySeries["work"] == 0 || bySeries["series"] == 0 {
		t.Fatalf("combined search for 'stormlight' = %v, want works and a series", bySeries)
	}

	cases := []struct{ path, kind string }{
		{"/api/v1/works/search?q=sanderson", "work"},
		{"/api/v1/people/search?q=sanderson", "person"},
		{"/api/v1/works/search?q=stormlight", "work"},
		{"/api/v1/series/search?q=stormlight", "series"},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			kinds := searchResultKinds(t, ts.URL, tc.path)
			if kinds[tc.kind] == 0 {
				t.Errorf("no %s results: %v", tc.kind, kinds)
			}
			if len(kinds) != 1 {
				t.Errorf("page carries %v, want %s only", kinds, tc.kind)
			}
		})
	}
}

// TestTypedSearchResultShapesMatchTheCombinedSearch: both search paths compose
// their page through one assembly, so a work hit carries its narrators and a
// series hit its member count on the scoped routes too.
func TestTypedSearchResultShapesMatchTheCombinedSearch(t *testing.T) {
	_, ts := newTestServer(t)

	code, body := artifacttest.GetJSON(t, ts.URL, "/api/v1/works/search?q=hail")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	results := body["results"].([]any)
	if len(results) == 0 {
		t.Fatal("works/search 'hail' found nothing")
	}
	work := results[0].(map[string]any)
	if work["id"] != "project-hail-mary" {
		t.Errorf("first result = %v", work["id"])
	}
	narrators, ok := work["narrators"].([]any)
	if !ok || len(narrators) == 0 {
		t.Fatalf("work result has no narrators: %v", work)
	}
	if narrators[0].(map[string]any)["id"] != "ray-porter" {
		t.Errorf("narrator = %v", narrators[0])
	}
	// The nullable card fields are always PRESENT on a work result, so a client
	// reads them without a key check.
	for _, key := range []string{"authors", "series", "cover_url", "added_at"} {
		if _, present := work[key]; !present {
			t.Errorf("work result is missing the %q key", key)
		}
	}

	code, body = artifacttest.GetJSON(t, ts.URL, "/api/v1/series/search?q=stormlight")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	series := body["results"].([]any)[0].(map[string]any)
	if series["id"] != "the-stormlight-archive" || series["works"].(float64) != 3 {
		t.Errorf("series result = %v", series)
	}
}

// TestSearchRequiresAQuery: an absent, empty or whitespace-only q is a 400 with
// the same message on every search endpoint - they share one handler.
func TestSearchRequiresAQuery(t *testing.T) {
	_, ts := newTestServer(t)
	paths := []string{
		"/api/v1/search",
		"/api/v1/works/search",
		"/api/v1/people/search",
		"/api/v1/series/search",
	}
	for _, p := range paths {
		for _, query := range []string{"", "?q=", "?q=%20%20"} {
			code, body := artifacttest.GetJSON(t, ts.URL, p+query)
			if code != 400 {
				t.Errorf("GET %s%s = %d, want 400", p, query, code)
			}
			if body["error"] != "q is required" {
				t.Errorf("GET %s%s error = %v", p, query, body["error"])
			}
		}
	}
}

// TestTypedSearchLimitClamp: the scoped endpoints inherit clampLimit, so a page
// size is honoured and an unparseable one falls back to the default rather than
// erroring.
func TestTypedSearchLimitClamp(t *testing.T) {
	_, ts := newTestServer(t)
	const path = "/api/v1/works/search?q=sanderson"

	if kinds := searchResultKinds(t, ts.URL, path+"&limit=1"); kinds["work"] != 1 {
		t.Errorf("limit=1 returned %d works, want 1", kinds["work"])
	}
	// The fixture holds three Sanderson works, under both the default and the cap.
	if kinds := searchResultKinds(t, ts.URL, path+"&limit=abc"); kinds["work"] != 3 {
		t.Errorf("limit=abc returned %d works, want the default page of 3", kinds["work"])
	}
	if kinds := searchResultKinds(t, ts.URL, path+"&limit=9999"); kinds["work"] != 3 {
		t.Errorf("limit=9999 returned %d works, want 3 (clamped, not widened)", kinds["work"])
	}
}

func TestPositionStart(t *testing.T) {
	// The cases site/src/lib/watch-flat.test.ts pins its twin against - see the
	// note on positionStart in queries.go.
	cases := map[string]float64{
		"1": 1, "2.5": 2.5, "1-3.5": 1, "10": 10, "": 1e18, "abc": 1e18,
		// A padded or padded-out value reads as the number it spells.
		"03": 3, " 4 ": 4,
		// A malformed range still sorts by its parseable prefix.
		"1-": 1, "1-garbage": 1, "1-2-3": 1,
	}
	for in, want := range cases {
		if got := positionStart(in); got != want {
			t.Errorf("positionStart(%q) = %v, want %v", in, got, want)
		}
	}
}

// TestGateHandsTheHandlerTheDatabaseItChecked pins that a request loads the
// database pointer ONCE: an in-process consumer's current can go back to nil
// (its copy unloaded) at any moment, and a handler that loaded it a second time
// after the gate's nil check would dereference that nil.
func TestGateHandsTheHandlerTheDatabaseItChecked(t *testing.T) {
	db := openTestDB(t, artifacttest.Build(t, artifacttest.Fixture()))
	var calls atomic.Int32
	current := func() *DB {
		if calls.Add(1) == 1 {
			return db
		}
		return nil // unloaded straight after the gate looked
	}
	h := NewHandler(current, HandlerOptions{Logger: artifacttest.QuietLogger()})
	for _, path := range []string{"/api/v1/stats", "/api/v1/works/match?q=hail+mary", "/api/v1/search?q=hail"} {
		calls.Store(0)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200 from the database the gate checked", path, rec.Code)
		}
		if n := calls.Load(); n != 1 {
			t.Errorf("GET %s loaded the database %d times, want once", path, n)
		}
	}
}
