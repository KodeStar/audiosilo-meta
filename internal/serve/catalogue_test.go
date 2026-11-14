package serve

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/artifacttest"
	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/query"
)

func catalogueFixture() *model.Catalog {
	cat := artifacttest.Fixture()
	rec := cat.Works[0].Recordings[0]
	rec.Chapters = nil
	for i := 0; i < 205; i++ {
		rec.Chapters = append(rec.Chapters, model.Chapter{Title: fmt.Sprintf("Chapter %03d", i+1), StartMS: int64(i) * 60000, LengthMS: 60000})
	}
	alt := *rec
	alt.ID = "alternate"
	alt.ASIN = nil
	alt.ISBN = nil
	cat.Works[0].Recordings = append(cat.Works[0].Recordings, &alt)
	for i := 0; i < 103; i++ {
		id := fmt.Sprintf("volume-%03d", i)
		cat.Works = append(cat.Works, &model.Work{ID: id, Title: fmt.Sprintf("Volume %03d", i), Language: "en", Authors: []string{"andy-weir"}, License: "CC0-1.0", Recordings: []*model.Recording{{ID: "reading", Work: id, Narrators: []string{"ray-porter"}, Language: "en", License: "CC0-1.0"}}})
		cat.Series[0].Works = append(cat.Series[0].Works, model.SeriesWork{Work: id, Position: fmt.Sprint(i + 10)})
	}
	return cat
}

func TestBrowseDirectoryAndPagination(t *testing.T) {
	ts := newPageServer(t, catalogueFixture(), markedShells)
	code, index := getPage(t, ts.URL, "/browse")
	if code != http.StatusOK {
		t.Fatal(code)
	}
	for _, family := range []string{"works", "series", "people", "recaps", "characters"} {
		if !strings.Contains(index, `href="/browse/`+family+`"`) {
			t.Errorf("missing directory link %s", family)
		}
	}
	code, first := getPage(t, ts.URL, "/browse/works")
	if code != 200 || !strings.Contains(first, `rel="next" href="/browse/works?page=2"`) {
		t.Fatalf("first page status %d, no next link", code)
	}
	code, second := getPage(t, ts.URL, "/browse/works?page=2")
	if code != 200 || !strings.Contains(second, `rel="prev" href="/browse/works"`) || !strings.Contains(second, `href="https://meta.test/browse/works?page=2"`) {
		t.Errorf("second page status %d or wrong navigation/canonical", code)
	}
	// Disjoint bounded windows leave later entries for real links to page three.
	if strings.Contains(first, `href="/works/volume-080"`) || !strings.Contains(second, `href="/works/volume-080"`) {
		t.Error("browse windows overlap or omit the second page")
	}
	for _, path := range []string{"/browse/unknown", "/browse/works?page=1000", "/browse?page=2"} {
		if code, _ := getPage(t, ts.URL, path); code != 404 {
			t.Errorf("%s=%d", path, code)
		}
	}
	for _, path := range []string{"/browse/works?page=0", "/browse/works?page=-1", "/browse/works?page=two", "/browse/works?page=2&page=3", "/browse/works?page=1000001", "/browse/works?page="} {
		if code, _ := getPage(t, ts.URL, path); code != 400 {
			t.Errorf("%s=%d", path, code)
		}
	}
	empty := newPageServer(t, &model.Catalog{}, markedShells)
	if code, body := getPage(t, empty.URL, "/browse/recaps"); code != 200 || !strings.Contains(body, "No entries are catalogued") {
		t.Errorf("empty directory %d %s", code, body)
	}
}

func TestChapterPagesValidateIdentityAndPagination(t *testing.T) {
	ts := newPageServer(t, catalogueFixture(), markedShells)
	path := "/works/project-hail-mary/recordings/ray-porter-2021/chapters"
	code, body := getPage(t, ts.URL, path+"?page=2")
	for _, want := range []string{"Chapter 101", "1:40:00", "0:01:00", `href="` + path + `"`, `href="` + path + `?page=3"`, `href="https://meta.test` + path + `?page=2"`} {
		if !strings.Contains(body, want) {
			t.Errorf("chapter page missing %q", want)
		}
	}
	if code != 200 || strings.Contains(body, "Chapter 100</th>") || strings.Contains(body, "Chapter 201</th>") {
		t.Errorf("invalid chapter window, status %d", code)
	}
	for _, invalid := range []string{path + "?page=4", "/works/nope/recordings/ray-porter-2021/chapters", "/works/project-hail-mary/recordings/nope/chapters"} {
		if code, _ := getPage(t, ts.URL, invalid); code != 404 {
			t.Errorf("%s=%d", invalid, code)
		}
	}
	for _, pair := range []struct{ from, to string }{{path + "?page=01&tracking=1", path}, {path + "?page=02&tracking=1", path + "?page=2"}, {"/works/project-hail-mary-audiobook/recordings/ray-porter-2021/chapters?page=2", path + "?page=2"}, {"/works/project-hail-mary-audiobook/recordings/caf%C3%A9-2021/chapters?page=2", "/works/project-hail-mary/recordings/caf%C3%A9-2021/chapters?page=2"}} {
		resp := artifacttest.GetNoFollow(t, ts.URL, pair.from)
		_ = resp.Body.Close()
		if resp.StatusCode != 301 || resp.Header.Get("Location") != pair.to {
			t.Errorf("%s=%d location %q", pair.from, resp.StatusCode, resp.Header.Get("Location"))
		}
	}
	_, work := getPage(t, ts.URL, "/works/project-hail-mary")
	for _, want := range []string{`href="` + path + `"`, `id="recording-ray-porter-2021"`, `href="#recording-ray-porter-2021"`, "Compare recordings"} {
		if !strings.Contains(work, want) {
			t.Errorf("work missing %q", want)
		}
	}
	_, noChapters := getPage(t, ts.URL, "/works/the-way-of-kings/recordings/kramer-reading-2010/chapters")
	if !strings.Contains(noChapters, "No chapter navigation is catalogued") {
		t.Error("zero chapters has no meaningful empty state")
	}
}

func TestPaginatedPageValidatorsAreDistinct(t *testing.T) {
	ts := newPageServer(t, catalogueFixture(), markedShells)
	paths := []string{"/browse/works", "/browse/works?page=2", "/series/the-stormlight-archive", "/series/the-stormlight-archive?page=2", "/people/ray-porter", "/people/ray-porter?page=2", "/works/project-hail-mary/recordings/ray-porter-2021/chapters", "/works/project-hail-mary/recordings/ray-porter-2021/chapters?page=2", "/works/project-hail-mary/recordings/alternate/chapters"}
	seen := map[string]bool{}
	for _, path := range paths {
		resp := artifacttest.GetNoFollow(t, ts.URL, path)
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		etag := resp.Header.Get("ETag")
		if resp.StatusCode != 200 || etag == "" || seen[etag] {
			t.Fatalf("%s status %d duplicate/empty ETag %q", path, resp.StatusCode, etag)
		}
		seen[etag] = true
		conditional := artifacttest.GetNoFollowWith(t, ts.URL+path, map[string]string{"If-None-Match": etag})
		_ = conditional.Body.Close()
		if conditional.StatusCode != 304 {
			t.Errorf("%s conditional status %d", path, conditional.StatusCode)
		}
	}
}

func TestPagedEntitySectionsMatchVisibleAPIWindow(t *testing.T) {
	ts := newPageServer(t, catalogueFixture(), markedShells)
	for _, test := range []struct{ path, key string }{{"/series/the-stormlight-archive?page=2", "listening_guide"}, {"/people/ray-porter?page=2", "recording_credits"}} {
		code, body := getPage(t, ts.URL, test.path)
		if code != 200 {
			t.Fatal(code)
		}
		var payload map[string]json.RawMessage
		if err := json.Unmarshal([]byte(entityPayload(t, body)), &payload); err != nil {
			t.Fatal(err)
		}
		var section struct{ Limit, Offset int }
		if err := json.Unmarshal(payload[test.key], &section); err != nil {
			t.Fatal(err)
		}
		if section.Limit != 50 || section.Offset != 50 {
			t.Errorf("%s window %+v", test.key, section)
		}
		if !strings.Contains(body, "Recording reading") || !strings.Contains(body, `href="/works/volume-`) {
			t.Errorf("%s lacks recording summaries", test.path)
		}
	}
}

func TestPageCompositionFailuresAreTemporary(t *testing.T) {
	srv, ts := newPageServerFrom(t, quietConfig(t, artifacttest.Fixture(), markedShells))
	if err := srv.current().Close(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/works/project-hail-mary", "/people/ray-porter", "/series/the-stormlight-archive", "/browse/works", "/works/project-hail-mary/recordings/ray-porter-2021/chapters"} {
		resp := artifacttest.GetNoFollow(t, ts.URL, path)
		_ = resp.Body.Close()
		if resp.StatusCode != 503 || resp.Header.Get("Retry-After") == "" || resp.Header.Get("Cache-Control") != "no-store" || resp.Header.Get("ETag") != "" {
			t.Errorf("%s: %d %v", path, resp.StatusCode, resp.Header)
		}
	}
}

func TestExplicitAbridgementMatchesJSONLD(t *testing.T) {
	cat := artifacttest.Fixture()
	rec := cat.Works[0].Recordings[0]
	rec.AbridgedKnown = true
	ts := newPageServer(t, cat, markedShells)
	_, page := getPage(t, ts.URL, "/works/project-hail-mary")
	if !strings.Contains(page, `"abridged":false`) || !strings.Contains(page, "Unabridged") {
		t.Error("explicit unabridgement absent from visible page or JSON-LD")
	}
	legacy := *rec
	legacy.ID = "unknown-reading"
	legacy.ASIN = nil
	legacy.ISBN = nil
	legacy.AbridgedKnown = false
	cat.Works[0].Recordings = append(cat.Works[0].Recordings, &legacy)
	ts = newPageServer(t, cat, markedShells)
	_, page = getPage(t, ts.URL, "/works/project-hail-mary")
	var graph struct {
		Graph []map[string]any `json:"@graph"`
	}
	if err := json.Unmarshal([]byte(between(t, page, `<script type="application/ld+json">`, `</script>`)), &graph); err != nil {
		t.Fatal(err)
	}
	for _, node := range graph.Graph {
		if node["@id"] == testSiteURL+"/works/project-hail-mary#recording-unknown-reading" {
			if _, ok := node["abridged"]; ok {
				t.Error("unknown abridgement falsely stated")
			}
			return
		}
	}
	t.Error("recording JSON-LD node missing")
}

func TestChapterHTMLSupportsOlderArtifacts(t *testing.T) {
	cfg := quietConfig(t, artifacttest.Fixture(), markedShells)
	cfg.DBPath = artifacttest.Altered(t, artifacttest.Fixture(), 7, "ALTER TABLE recordings DROP COLUMN abridgement", "ALTER TABLE chapters DROP COLUMN audio_url")
	_, ts := newPageServerFrom(t, cfg)
	code, page := getPage(t, ts.URL, "/works/project-hail-mary/recordings/ray-porter-2021/chapters")
	if code != 200 || !strings.Contains(page, "Chapter 1") || !strings.Contains(page, "0:00:05") || !strings.Contains(page, "0:10:00") {
		t.Errorf("old artifact chapters %d %s", code, page)
	}
	_, page = getPage(t, ts.URL, "/works/project-hail-mary")
	if strings.Contains(page, `"abridged":false`) || strings.Contains(page, "Unabridged") {
		t.Error("legacy false incorrectly establishes unabridgement")
	}
}

func TestChapterTimePreservesMilliseconds(t *testing.T) {
	for _, tc := range []struct {
		ms   int64
		want string
	}{{1501, "0:00:01.501"}, {0, "0:00:00"}, {3600001, "1:00:00.001"}, {-1, "Unknown"}} {
		if got := formatChapterTime(tc.ms); got != tc.want {
			t.Errorf("%d = %q want %q", tc.ms, got, tc.want)
		}
	}
}

func TestFactualPageMetadataDoesNotClaimCommunityLicensing(t *testing.T) {
	ts := newPageServer(t, artifacttest.Fixture(), markedShells)
	for _, tc := range []struct{ path, kind string }{{"/browse", "CollectionPage"}, {"/browse/works", "CollectionPage"}, {"/works/project-hail-mary/recordings/ray-porter-2021/chapters", "WebPage"}} {
		_, page := getPage(t, ts.URL, tc.path)
		var node map[string]any
		if err := json.Unmarshal([]byte(between(t, page, `<script type="application/ld+json">`, `</script>`)), &node); err != nil {
			t.Fatal(err)
		}
		if _, ok := node["license"]; ok {
			t.Errorf("%s incorrectly claims community licence", tc.path)
		}
		if node["@type"] != tc.kind {
			t.Errorf("%s type %v", tc.path, node["@type"])
		}
		if tc.kind == "CollectionPage" {
			if _, ok := node["about"]; ok {
				t.Errorf("%s points at an invented book node", tc.path)
			}
		}
	}
	_, work := getPage(t, ts.URL, "/works/the-way-of-kings")
	if !strings.Contains(work, "None catalogued") {
		t.Error("known zero chapter count labelled unknown")
	}
	for _, path := range []string{"/series/the-stormlight-archive", "/people/michael-kramer"} {
		code, body := getPage(t, ts.URL, path)
		if code != http.StatusOK || !strings.Contains(body, "<dt>Chapters</dt><dd>None catalogued</dd>") {
			t.Errorf("%s: zero chapter summary is not labelled consistently (status %d)", path, code)
		}
	}
}

func TestSeriesHTMLPageTwoKeepsFractionalAndRangePositions(t *testing.T) {
	cat := artifacttest.Fixture()
	cat.Series[0].Works = nil
	for i := 0; i < 54; i++ {
		id := fmt.Sprintf("ordered-%02d", i)
		position := fmt.Sprint(i + 1)
		if i == 49 {
			position = "50-52.5"
		}
		if i == 50 {
			position = "50"
		}
		if i == 51 || i == 52 {
			position = "50.5"
		}
		if i == 53 {
			position = "51-52.5"
		}
		cat.Works = append(cat.Works, &model.Work{ID: id, Title: fmt.Sprintf("Ordered %02d", i), Language: "en", Authors: []string{"brandon-sanderson"}, License: "CC0-1.0"})
		cat.Series[0].Works = append(cat.Series[0].Works, model.SeriesWork{Work: id, Position: position})
	}
	cfg := quietConfig(t, cat, markedShells)
	// A supported older artifact has no numeric-position expression index.
	cfg.DBPath = artifacttest.Altered(t, cat, 11, "ALTER TABLE recordings DROP COLUMN abridgement", "DROP INDEX idx_series_works_position")
	_, ts := newPageServerFrom(t, cfg)
	code, page := getPage(t, ts.URL, "/series/the-stormlight-archive?page=2")
	if code != 200 {
		t.Fatal(code)
	}
	var payload struct {
		Works []query.SeriesEntry `json:"works"`
		Guide struct {
			Entries       []query.ListeningGuideEntry
			WorksTotal    int `json:"works_total"`
			Limit, Offset int
		} `json:"listening_guide"`
	}
	if err := json.Unmarshal([]byte(entityPayload(t, page)), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Works) != 4 || payload.Works[0].Position != "50-52.5" || payload.Works[1].Position != "50.5" || payload.Works[2].Position != "50.5" || payload.Works[3].Position != "51-52.5" {
		t.Fatalf("base membership page %+v", payload.Works)
	}
	if len(payload.Guide.Entries) != 4 || payload.Guide.WorksTotal != 54 || payload.Guide.Limit != 50 || payload.Guide.Offset != 50 {
		t.Fatalf("guide page %+v", payload.Guide)
	}
	apiResp := artifacttest.GetNoFollow(t, ts.URL, "/api/v1/series/the-stormlight-archive?limit=50&offset=50")
	var api query.SeriesDetail
	if err := json.NewDecoder(apiResp.Body).Decode(&api); err != nil {
		t.Fatal(err)
	}
	if apiResp.StatusCode != http.StatusOK || len(api.Works) != len(payload.Works) {
		t.Fatalf("API status %d, %d members; HTML has %d", apiResp.StatusCode, len(api.Works), len(payload.Works))
	}
	for i, entry := range payload.Guide.Entries {
		if entry.Work.ID != payload.Works[i].Work.ID || entry.Work.ID != api.Works[i].Work.ID || entry.Position != api.Works[i].Position {
			t.Errorf("entry %d differs between guide, HTML membership and API: %+v, %+v, %+v", i, entry, payload.Works[i], api.Works[i])
		}
	}
	if !strings.Contains(page, "Showing 4 of 54 titles") || !strings.Contains(page, "No recording catalogued") || strings.Contains(page, "Ordered 50") {
		t.Error("page does not describe its visible bounded membership")
	}
}
