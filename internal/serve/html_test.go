package serve

import (
	"encoding/json"
	"flag"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// updateGolden regenerates the committed rendered pages. The goldens are the
// review artifact of this feature - what a crawler is actually served - so they
// live in the repo rather than being asserted piecemeal, and are regenerated
// with `go test ./internal/serve -run Golden -update-golden` after a deliberate
// rendering change.
var updateGolden = flag.Bool("update-golden", false, "rewrite the golden entity pages in testdata/golden")

// testSiteURL is the origin the fixture server is told it serves, so every
// canonical/og/JSON-LD URL in the goldens is stable and obviously not
// production.
const testSiteURL = "https://meta.test"

// markedShells is the ordinary case: every entity shell carries the markers.
// The guide pages' shells are here too - they are ordinary entity shells as far
// as this package is concerned, and the dist really does build one per page.
var markedShells = map[string]string{
	"work":       "shell-marked.html",
	"person":     "shell-marked.html",
	"series":     "shell-marked.html",
	"recap":      "shell-marked.html",
	"characters": "shell-marked.html",
}

// entitySite lays out a site directory whose entity shells are the named
// testdata fixtures, beside the landing page and the site's own 404.
func entitySite(t *testing.T, shells map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	writeSiteFile(t, dir, "index.html", "<html>LANDING</html>")
	writeSiteFile(t, dir, "404.html", "<html>CUSTOM 404</html>")
	for page, fixture := range shells {
		raw, err := os.ReadFile(filepath.Join("testdata", fixture))
		if err != nil {
			t.Fatal(err)
		}
		writeSiteFile(t, dir, filepath.Join(page, "index.html"), string(raw))
	}
	return dir
}

func writeSiteFile(t *testing.T, dir, name, content string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// quietConfig is the shared server config for the page tests: a fixed public
// origin (so the goldens are stable) and a silent logger (the missing-shell
// notices are the point of some of these cases, not noise the run should carry).
func quietConfig(t *testing.T, cat *model.Catalog, shells map[string]string) Config {
	t.Helper()
	return Config{
		DBPath:    buildFixtureDB(t, cat),
		Site:      entitySite(t, shells),
		SiteURL:   testSiteURL,
		Logger:    log.New(io.Discard, "", 0),
		swapGrace: time.Minute,
	}
}

func newPageServer(t *testing.T, cat *model.Catalog, shells map[string]string) *httptest.Server {
	t.Helper()
	_, ts := newPageServerFrom(t, quietConfig(t, cat, shells))
	return ts
}

// newPageServerFrom is newPageServer with the config supplied and the *Server
// handed back: a test that has to derive what the server itself computed (an
// ETag over its own snapshot and shell) needs both, and a test about the DIST
// needs to construct two servers over one config.
func newPageServerFrom(t *testing.T, cfg Config) (*Server, *httptest.Server) {
	t.Helper()
	srv, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return srv, ts
}

// getPage fetches an HTML page and returns its status and body.
func getPage(t *testing.T, base, path string) (int, string) {
	t.Helper()
	return getBody(t, base+path)
}

// ---- route tables -----------------------------------------------------------

// TestHTMLRoutesAreDisjointFromTheAPI pins the separation the OpenAPI guard
// depends on: openapi.json describes the API, and the entity pages are not API,
// so they live in a second table. If a page pattern ever appeared under /api/ or
// in routes(), TestOpenAPICoversEveryRoute would start demanding a spec entry for
// a page - and the honest fix would be a spec that lies.
func TestHTMLRoutesAreDisjointFromTheAPI(t *testing.T) {
	srv := &Server{cfg: Config{WebhookSecret: strings.Repeat("s", minWebhookSecretBytes)}}

	api := map[string]bool{}
	for _, r := range srv.routes() {
		api[r.pattern] = true
	}
	for _, r := range srv.htmlRoutes() {
		if strings.Contains(r.specPath(), "/api/") {
			t.Errorf("html route %s sits under /api/: the two surfaces must stay disjoint", r.pattern)
		}
		if api[r.pattern] {
			t.Errorf("pattern %s is registered by BOTH routes() and htmlRoutes()", r.pattern)
		}
	}

	// And every page is registered, derived from the one table: the three entity
	// families with both of their routes, and the two guide pages with only the
	// path route (they replaced no query-param URL, so htmlRoutes registers no
	// legacy twin for them - see htmlEntityRoute.legacy).
	var got []string
	for _, r := range srv.htmlRoutes() {
		got = append(got, r.pattern)
	}
	sort.Strings(got)
	want := []string{
		"GET /people/{id}", "GET /person", "GET /series", "GET /series/{id}",
		"GET /work", "GET /works/{id}", "GET /works/{id}/characters", "GET /works/{id}/recap",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("htmlRoutes = %v, want %v", got, want)
	}
}

// TestHTMLRoutesNeedASiteDirectory pins the registration condition: an API-only
// deployment serves no pages, because there is no shell to inject into.
func TestHTMLRoutesNeedASiteDirectory(t *testing.T) {
	_, ts := newTestServer(t) // no Site
	if code, _ := getPage(t, ts.URL, "/works/project-hail-mary"); code != http.StatusNotFound {
		t.Errorf("entity page without a site dir = %d, want 404 (the route is not registered)", code)
	}
}

// ---- shell injection --------------------------------------------------------

// entityPayload pulls the embedded API payload out of a rendered page.
func entityPayload(t *testing.T, page string) string {
	t.Helper()
	const open = `<script type="application/json" id="entity-data">`
	_, rest, ok := strings.Cut(page, open)
	if !ok {
		t.Fatalf("page carries no %s block", open)
	}
	payload, _, ok := strings.Cut(rest, "</script>")
	if !ok {
		t.Fatal("entity-data block is never closed")
	}
	return payload
}

// TestEntityPageInjectsIntoTheShell is the contract with the site build: the
// marked head section is REPLACED (once), the body marker becomes the fact sheet
// plus the payload, and everything outside the markers survives untouched.
func TestEntityPageInjectsIntoTheShell(t *testing.T) {
	ts := newPageServer(t, fixtureCatalog(), markedShells)
	code, page := getPage(t, ts.URL, "/works/project-hail-mary")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}

	// Outside the markers: untouched.
	for _, want := range []string{`<meta charset="utf-8">`, "SHELL HEADER", "SHELL FOOTER", `<div id="island-root">`} {
		if !strings.Contains(page, want) {
			t.Errorf("page lost the shell's %q", want)
		}
	}
	// Inside them: replaced, exactly once.
	if strings.Contains(page, "<title>AudioSilo Meta</title>") {
		t.Error("the shell's placeholder title survived the injection")
	}
	if n := strings.Count(page, "<title>"); n != 1 {
		t.Errorf("page carries %d title tags, want 1", n)
	}
	if strings.Contains(page, "The open audiobook database.") {
		t.Error("the shell's placeholder description survived the injection")
	}
	if !strings.Contains(page, "<title>Project Hail Mary by Andy Weir (audiobook) - AudioSilo Meta</title>") {
		t.Errorf("composed title missing from head:\n%s", page)
	}
	if !strings.Contains(page, `<link rel="canonical" href="https://meta.test/works/project-hail-mary">`) {
		t.Error("canonical link missing or wrong")
	}
	// The body marker is consumed by what replaces it.
	if strings.Contains(page, bodyMarker) {
		t.Error("the body marker survived the injection")
	}
	if !strings.Contains(page, `<div id="ssr-entity">`) {
		t.Error("no server-rendered fact sheet")
	}
	// The fact sheet's internal links are PATH routes.
	if !strings.Contains(page, `href="/people/andy-weir"`) {
		t.Error("fact sheet does not link the author at its path route")
	}
	if strings.Contains(page, `href="/person?id=`) {
		t.Error("fact sheet emitted a legacy query-param link")
	}
	// The JSON-LD parses.
	ld := between(t, page, `<script type="application/ld+json">`, "</script>")
	var graph map[string]any
	if err := json.Unmarshal([]byte(ld), &graph); err != nil {
		t.Fatalf("JSON-LD does not parse: %v\n%s", err, ld)
	}
	if graph["@context"] != ldContext {
		t.Errorf("JSON-LD @context = %v", graph["@context"])
	}
}

func between(t *testing.T, s, open, close string) string {
	t.Helper()
	_, rest, ok := strings.Cut(s, open)
	if !ok {
		t.Fatalf("missing %q", open)
	}
	got, _, ok := strings.Cut(rest, close)
	if !ok {
		t.Fatalf("missing %q after %q", close, open)
	}
	return got
}

// TestEntityPayloadIsTheAPIResponse is the hydration contract: the embedded
// payload is byte-for-byte what the corresponding API route returns for the same
// id, so the island can use it as initial data and skip the fetch entirely.
//
// The API URL each case names is the fetch the island would have MADE, not the
// route's bare default: the person page asks for the maximum window (see
// site/src/lib/api.ts PERSON_PAGE_MAX and composePersonPage), so a payload
// composed at the default would be a smaller page than the fetch it replaces.
func TestEntityPayloadIsTheAPIResponse(t *testing.T) {
	ts := newPageServer(t, fixtureCatalog(), markedShells)
	cases := []struct{ page, api string }{
		{"/works/project-hail-mary", "/api/v1/works/project-hail-mary"},
		{"/people/brandon-sanderson", "/api/v1/people/brandon-sanderson?limit=" + strconv.Itoa(personPageMax)},
		{"/series/the-stormlight-archive", "/api/v1/series/the-stormlight-archive"},
	}
	for _, tc := range cases {
		t.Run(tc.page, func(t *testing.T) {
			_, page := getPage(t, ts.URL, tc.page)
			_, apiBody := getBody(t, ts.URL+tc.api)
			// The API writer appends a newline (json.Encoder); the payload is the
			// same document without it.
			if got, want := entityPayload(t, page), strings.TrimSuffix(apiBody, "\n"); got != want {
				t.Errorf("payload differs from the API response\npage: %s\napi:  %s", got, want)
			}
			// And the id the island matches on is present.
			var doc map[string]any
			if err := json.Unmarshal([]byte(entityPayload(t, page)), &doc); err != nil {
				t.Fatalf("payload does not parse: %v", err)
			}
			if doc["id"] == nil {
				t.Error("payload carries no id for the island to match the slug against")
			}
		})
	}
}

// TestPersonPageComposesTheMaximumWindow pins the window the person page is
// composed at. The fixture catalogue is far too small to show the defect - it
// would take a person with more than personPageDefault credits - so the constant
// is asserted instead, through the payload the page actually embeds.
//
// The window matters because the payload REPLACES a fetch: the site asks for
// PERSON_PAGE_MAX (site/src/lib/api.ts), and a page composed at the default
// would silently drop credits 101-500 on a hydrated page that has no pagination
// UI to reach them with.
func TestPersonPageComposesTheMaximumWindow(t *testing.T) {
	ts := newPageServer(t, fixtureCatalog(), markedShells)
	_, page := getPage(t, ts.URL, "/people/brandon-sanderson")
	var doc struct {
		Limit int `json:"limit"`
	}
	if err := json.Unmarshal([]byte(entityPayload(t, page)), &doc); err != nil {
		t.Fatalf("payload does not parse: %v", err)
	}
	if doc.Limit != personPageMax {
		t.Errorf("person page composed at limit %d, want personPageMax (%d)", doc.Limit, personPageMax)
	}
}

// TestShellWithoutMarkersIsServedUntouched: a dist that predates the markers (or
// a Base.astro refactor that dropped one) turns the feature off rather than
// breaking the page. Both halves of the contract are required, so a shell with
// only one is as inert as one with neither.
func TestShellWithoutMarkersIsServedUntouched(t *testing.T) {
	cases := []struct {
		name    string
		fixture string
	}{
		{"no head markers", "shell-nohead.html"},
		{"no body marker", "shell-nobody.html"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts := newPageServer(t, fixtureCatalog(), map[string]string{"work": tc.fixture})
			code, page := getPage(t, ts.URL, "/works/project-hail-mary")
			if code != http.StatusOK {
				t.Fatalf("status = %d, want 200", code)
			}
			if !strings.Contains(page, "<title>AudioSilo Meta</title>") {
				t.Error("the shell's own head was replaced despite the missing marker")
			}
			if strings.Contains(page, `id="ssr-entity"`) {
				t.Error("content was injected into an unmarked shell")
			}
		})
	}
}

// TestEntityPageWithoutAShellFallsThrough: no built shell for the family at all
// means the request is the static site's, exactly as before these routes
// existed - here, the site's own 404.
func TestEntityPageWithoutAShellFallsThrough(t *testing.T) {
	ts := newPageServer(t, fixtureCatalog(), nil)
	code, page := getPage(t, ts.URL, "/works/project-hail-mary")
	if code != http.StatusNotFound {
		t.Fatalf("status = %d, want the static site's 404", code)
	}
	if !strings.Contains(page, "CUSTOM 404") {
		t.Errorf("body = %q, want the site's 404 page", page)
	}
}

// TestEntityPageWithoutASnapshot: a poll-only boot that has not loaded an
// artifact yet serves the untouched shell rather than the API's 503. The page is
// still a page, and a crawler that gets an error keeps the error.
func TestEntityPageWithoutASnapshot(t *testing.T) {
	srv := &Server{
		cfg:     Config{Site: entitySite(t, markedShells), SiteURL: testSiteURL},
		log:     log.New(io.Discard, "", 0),
		retired: map[string]int{},
	}
	srv.site = newSiteHandler(srv.cfg.Site)
	srv.shells = loadShells(srv.cfg.Site, srv.cfg.SiteURL, srv.log)
	srv.mux = srv.buildMux()
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	if srv.current() != nil {
		t.Fatal("test server unexpectedly has a snapshot")
	}
	code, page := getPage(t, ts.URL, "/works/project-hail-mary")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (degrade, never error)", code)
	}
	if !strings.Contains(page, "<title>AudioSilo Meta</title>") {
		t.Error("expected the untouched shell")
	}
	if strings.Contains(page, `id="ssr-entity"`) {
		t.Error("content was injected with no snapshot to read")
	}
}

// ---- rendering --------------------------------------------------------------

// TestEntityPagesAreDeterministic pins the property every cached, crawled page
// needs: one snapshot renders one page, byte for byte, however many times it is
// asked. Nothing here may depend on a timestamp or on map iteration order.
func TestEntityPagesAreDeterministic(t *testing.T) {
	ts := newPageServer(t, fixtureCatalog(), markedShells)
	for _, path := range []string{
		"/works/project-hail-mary", "/people/brandon-sanderson", "/series/the-stormlight-archive",
	} {
		_, first := getPage(t, ts.URL, path)
		_, second := getPage(t, ts.URL, path)
		if first != second {
			t.Errorf("GET %s rendered differently on the second request", path)
		}
	}
}

// TestEntityPagesGolden renders every page kind over the shared fixture
// catalogue and diffs them against the committed output. It is the review
// artifact for this feature: a change to the head, the fact sheet or the JSON-LD
// shows up as a readable diff rather than as a passing test.
func TestEntityPagesGolden(t *testing.T) {
	ts := newPageServer(t, fixtureCatalog(), markedShells)
	cases := []struct{ name, path string }{
		{"work", "/works/project-hail-mary"},
		{"person", "/people/brandon-sanderson"},
		{"series", "/series/the-stormlight-archive"},
		// The community guide pages: what a crawler is served for "<title> recap"
		// and "<title> characters" - the whole point of Phase F, so the whole page
		// is reviewable as a diff.
		{"recap", "/works/project-hail-mary" + recapSuffix},
		{"characters", "/works/project-hail-mary" + charactersSuffix},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, page := getPage(t, ts.URL, tc.path)
			if code != http.StatusOK {
				t.Fatalf("status = %d", code)
			}
			golden := filepath.Join("testdata", "golden", tc.name+".html")
			if *updateGolden {
				if err := os.WriteFile(golden, []byte(page), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("%v (regenerate with -update-golden)", err)
			}
			if page != string(want) {
				t.Errorf("rendered %s page differs from %s (regenerate with -update-golden)", tc.name, golden)
			}
		})
	}
}

// TestEntityPageEscapesHostileText is the XSS test. A title is free text from a
// contributor, so it can carry markup: it must be escaped in the head and in the
// fact sheet, and the embedded payload must still parse - which it does because
// encoding/json escapes "<", making a "</script>" inside the JSON impossible.
func TestEntityPageEscapesHostileText(t *testing.T) {
	const hostile = `<script>alert(1)</script> & "quotes"`
	cat := &model.Catalog{
		People: []*model.Person{{ID: "evil-author", Name: `Mallory <b>Bold</b>`, License: "CC0-1.0"}},
		Works: []*model.Work{{
			ID: "hostile-work", Title: hostile, Language: "en",
			Authors: []string{"evil-author"}, License: "CC0-1.0",
		}},
	}
	ts := newPageServer(t, cat, markedShells)
	code, page := getPage(t, ts.URL, "/works/hostile-work")
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if strings.Contains(page, "<script>alert(1)</script>") {
		t.Error("the hostile title was emitted as live markup")
	}
	if !strings.Contains(page, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Errorf("the hostile title is not HTML-escaped in the page:\n%s", page)
	}
	// Only the two script blocks this package writes may exist.
	if n := strings.Count(page, "<script"); n != 2 {
		t.Errorf("page carries %d script tags, want exactly the ld+json and the payload", n)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(entityPayload(t, page)), &doc); err != nil {
		t.Fatalf("payload does not parse: %v", err)
	}
	if doc["title"] != hostile {
		t.Errorf("payload title = %v, want the raw title", doc["title"])
	}
	var graph map[string]any
	if err := json.Unmarshal([]byte(between(t, page, `<script type="application/ld+json">`, "</script>")), &graph); err != nil {
		t.Fatalf("JSON-LD does not parse: %v", err)
	}
}

// ---- redirects and 404s -----------------------------------------------------

// TestRetiredSlugRedirectsOnPages: the tombstone mechanism reaches the pages
// too, and answers in HTML there - a browser following a dead link gets a page
// linking the survivor, not the API's JSON envelope.
func TestRetiredSlugRedirectsOnPages(t *testing.T) {
	ts := newPageServer(t, fixtureCatalog(), markedShells)
	cases := []struct{ path, location string }{
		{"/works/project-hail-mary-audiobook", "/works/project-hail-mary"},
		{"/people/andy-weir-author", "/people/andy-weir"},
		{"/series/stormlight-archive", "/series/the-stormlight-archive"},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			resp := getNoFollow(t, ts.URL, tc.path)
			if resp.StatusCode != http.StatusMovedPermanently {
				t.Fatalf("status = %d, want 301", resp.StatusCode)
			}
			if got := resp.Header.Get("Location"); got != tc.location {
				t.Errorf("Location = %q, want %q", got, tc.location)
			}
			if got := resp.Header.Get("Cache-Control"); got != redirectMaxAge {
				t.Errorf("Cache-Control = %q, want %q", got, redirectMaxAge)
			}
			if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
				t.Errorf("Content-Type = %q, want html", ct)
			}
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(body), `<a href="`+tc.location+`">`) {
				t.Errorf("301 body does not link the new URL: %s", body)
			}
		})
	}
}

// TestRetiredSlugStillAnswersJSONOnTheAPI is the other half: adding the HTML
// writer must not change what an API client is served.
func TestRetiredSlugStillAnswersJSONOnTheAPI(t *testing.T) {
	ts := newPageServer(t, fixtureCatalog(), markedShells)
	wantRedirect(t, getNoFollow(t, ts.URL, "/api/v1/works/project-hail-mary-audiobook"),
		"/api/v1/works/project-hail-mary", "project-hail-mary")
}

// TestLegacyQueryRedirects covers the old ?id= URLs: they 301 to the path route
// forever, keeping the rest of the query and escaping the id exactly once. With
// no id at all the request is the family's landing page, served from the shell.
func TestLegacyQueryRedirects(t *testing.T) {
	ts := newPageServer(t, fixtureCatalog(), markedShells)
	cases := []struct{ name, path, location string }{
		{"work", "/work?id=project-hail-mary", "/works/project-hail-mary"},
		{"person", "/person?id=andy-weir", "/people/andy-weir"},
		{"series", "/series?id=the-stormlight-archive", "/series/the-stormlight-archive"},
		{"other params survive", "/work?id=phm&tab=chapters", "/works/phm?tab=chapters"},
		{"the id is escaped once", "/work?id=caf%C3%A9", "/works/caf%C3%A9"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := getNoFollow(t, ts.URL, tc.path)
			if resp.StatusCode != http.StatusMovedPermanently {
				t.Fatalf("status = %d, want 301", resp.StatusCode)
			}
			if got := resp.Header.Get("Location"); got != tc.location {
				t.Errorf("Location = %q, want %q", got, tc.location)
			}
		})
	}

	// No id: the shell, untouched (there is no entity to render).
	code, page := getPage(t, ts.URL, "/work")
	if code != http.StatusOK {
		t.Fatalf("GET /work = %d, want 200", code)
	}
	if !strings.Contains(page, "<title>AudioSilo Meta</title>") {
		t.Error("GET /work should serve the untouched shell")
	}
}

// TestEntityPageNotFound: an id nothing holds renders the SITE's 404 page with a
// 404 status, so a crawler gets the designed page and the right code. The
// reserved slugs need no special case - no record can hold one, so they land
// here like any other unknown id.
func TestEntityPageNotFound(t *testing.T) {
	ts := newPageServer(t, fixtureCatalog(), markedShells)
	for _, path := range []string{
		"/works/no-such-work", "/people/nobody", "/series/nothing",
		"/works/search", "/works/latest", "/people/search", "/series/search",
	} {
		code, page := getPage(t, ts.URL, path)
		if code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, code)
			continue
		}
		if !strings.Contains(page, "CUSTOM 404") {
			t.Errorf("GET %s body = %q, want the site's 404 page", path, page)
		}
	}
}

// ---- caching ----------------------------------------------------------------

// conditionalGet issues a GET carrying an If-None-Match header, following no
// redirects. The caller closes the body.
func conditionalGet(t *testing.T, url, inm string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("If-None-Match", inm)
	req.Header.Set("Accept-Encoding", "gzip")
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// wantBodyless fails when a response carries any body, which is what a 304 must
// not.
func wantBodyless(t *testing.T, resp *http.Response) {
	t.Helper()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) != 0 {
		t.Errorf("%d carried a %d-byte body", resp.StatusCode, len(body))
	}
}

// TestEntityPageRevalidates pins the crawl-budget win: the page carries the ETag
// entityETag composes for it, and a conditional request is answered with a
// bodyless 304. The expected value is DERIVED from the helper rather than
// spelled out - the shape has three parts now (see entityETag) and a test that
// re-spells it would be a second definition of the validator.
func TestEntityPageRevalidates(t *testing.T) {
	srv, ts := newPageServerFrom(t, quietConfig(t, fixtureCatalog(), markedShells))
	resp := getNoFollow(t, ts.URL, "/works/project-hail-mary")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	etag := resp.Header.Get("ETag")
	want := entityETag(srv.current(), srv.shells.get("work/index.html"), "project-hail-mary")
	if etag != want {
		t.Errorf("ETag = %q, want %q", etag, want)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != entityMaxAge {
		t.Errorf("Cache-Control = %q, want %q", cc, entityMaxAge)
	}
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		t.Fatal(err)
	}

	second := conditionalGet(t, ts.URL+"/works/project-hail-mary", etag)
	if second.StatusCode != http.StatusNotModified {
		t.Fatalf("conditional GET = %d, want 304", second.StatusCode)
	}
	if enc := second.Header.Get("Content-Encoding"); enc != "" {
		t.Errorf("304 Content-Encoding = %q, want none", enc)
	}
	wantBodyless(t, second)

	// Two different records never share a validator.
	other := getNoFollow(t, ts.URL, "/works/the-way-of-kings")
	if got := other.Header.Get("ETag"); got == etag {
		t.Errorf("two works share the ETag %q", got)
	}
}

// TestEntityETagCoversTheDist is the reason the validator is not the artifact's
// identity alone: the body is the built shell plus the composed markup, so a
// UI-only deploy - a new dist against the SAME data release - has to invalidate
// it. Without that, a 304 would renew a client's copy of the old page forever,
// and that page asks for hashed assets the new dist no longer carries.
//
// One config, so both servers read one artifact; the dist changes between them,
// as a deploy changes it.
func TestEntityETagCoversTheDist(t *testing.T) {
	cfg := quietConfig(t, fixtureCatalog(), markedShells)
	_, first := newPageServerFrom(t, cfg)
	before := getNoFollow(t, first.URL, "/works/project-hail-mary").Header.Get("ETag")

	shellPath := filepath.Join(cfg.Site, "work", "index.html")
	raw, err := os.ReadFile(shellPath)
	if err != nil {
		t.Fatal(err)
	}
	// A new asset hash is what a UI deploy really changes; any byte outside the
	// markers proves the same point.
	if err := os.WriteFile(shellPath, append(raw, []byte("<!--rebuilt-->")...), 0o644); err != nil {
		t.Fatal(err)
	}
	_, second := newPageServerFrom(t, cfg)
	after := getNoFollow(t, second.URL, "/works/project-hail-mary").Header.Get("ETag")

	if before == "" || after == "" {
		t.Fatalf("missing ETag: %q, %q", before, after)
	}
	if before == after {
		t.Errorf("a rebuilt dist reissued the validator %q", after)
	}
}

// TestPageIdentityCoversEveryComponent pins the four inputs one page's identity
// is mixed from, each for a different way the rendered bytes can change while
// the artifact does not: WHICH page it is, the dist, the origin every canonical
// URL is built from, and the composer's own code.
func TestPageIdentityCoversEveryComponent(t *testing.T) {
	const (
		shellName = "work/index.html"
		shellHTML = "<html>SHELL</html>"
		siteURL   = "https://meta.test"
		revision  = "abc123"
	)
	base := pageIdentity(shellName, shellHTML, siteURL, revision)
	if again := pageIdentity(shellName, shellHTML, siteURL, revision); again != base {
		t.Errorf("pageIdentity is not deterministic: %q then %q", base, again)
	}
	cases := map[string]string{
		// Two pages a record can be addressed at (a work and its recap page) built
		// from IDENTICAL shell bytes: without the name they would share a
		// validator, and a client would be handed a 304 for the other page.
		"a different page":     pageIdentity("recap/index.html", shellHTML, siteURL, revision),
		"a rebuilt dist":       pageIdentity(shellName, shellHTML+" ", siteURL, revision),
		"a different origin":   pageIdentity(shellName, shellHTML, siteURL+"/staging", revision),
		"a different revision": pageIdentity(shellName, shellHTML, siteURL, revision+"0"),
		// Length-prefixed components: without that, moving a boundary would leave
		// the digest input identical.
		"a moved boundary": pageIdentity(shellName, shellHTML+siteURL, "", revision),
	}
	for name, got := range cases {
		if got == base {
			t.Errorf("%s left the page identity at %q", name, got)
		}
	}
	// An unstamped binary (go run, a test binary) is honest, not an error.
	if pageIdentity(shellName, shellHTML, siteURL, "") == "" {
		t.Error("an empty revision produced an empty identity")
	}
}

// TestEntityPageRevalidatesWithoutComposing pins that the 304 is answered before
// the page is composed - the property the whole revalidation win rests on, since
// composing is a query cascade per request and revalidation is what a crawler
// mostly sends once a page is indexed.
//
// It is proved from the outside: an UNKNOWN slug carrying a matching validator
// is answered 304 rather than 404, which is only possible if the handler asked
// the snapshot nothing. That shape is the accepted cost of the ordering - an
// honest client can only hold a matching validator for a page we served a 200
// from this same snapshot, so a fabricated one buys its fabricator a bodyless
// 304 for a page that was never served and tells it nothing it did not invent.
func TestEntityPageRevalidatesWithoutComposing(t *testing.T) {
	ts := newPageServer(t, fixtureCatalog(), markedShells)
	live := getNoFollow(t, ts.URL, "/works/project-hail-mary")
	if _, err := io.Copy(io.Discard, live.Body); err != nil {
		t.Fatal(err)
	}
	etag := live.Header.Get("ETag")
	if etag == "" {
		t.Fatal("page carries no ETag")
	}
	// The same snapshot's validator for a slug the catalogue does not hold.
	fabricated := strings.Replace(etag, "project-hail-mary", "no-such-work", 1)
	if fabricated == etag {
		t.Fatalf("the ETag %q does not carry the slug, so the case cannot be built", etag)
	}

	// Without the conditional header the same URL is a 404, so the 304 below is
	// the ordering and not a record we accidentally hold.
	if code, _ := getPage(t, ts.URL, "/works/no-such-work"); code != http.StatusNotFound {
		t.Fatalf("unconditional GET of an unknown slug = %d, want 404", code)
	}

	resp := conditionalGet(t, ts.URL+"/works/no-such-work", fabricated)
	if resp.StatusCode != http.StatusNotModified {
		t.Fatalf("conditional GET of an unknown slug = %d, want 304 (the page must not be composed)", resp.StatusCode)
	}
	wantBodyless(t, resp)
}

// TestWildcardValidatorNeedsARepresentation is the ONE conditional request that
// does not take the fast path above. "*" names no validator, so it carries no
// proof that anything resolved, and RFC 9110 13.1.2 makes it match only where a
// current representation EXISTS - so the handler runs its whole cascade and the
// wildcard changes only what a SUCCESSFUL compose is written as. An unknown slug
// keeps its 404 and a retired one keeps its 301; treating "*" as an
// unconditional match had them answering 304 for records the server does not
// serve, which is how a crawler loses a page and a browser loses a redirect.
func TestWildcardValidatorNeedsARepresentation(t *testing.T) {
	ts := newPageServer(t, fixtureCatalog(), markedShells)
	cases := []struct {
		name, path string
		want       int
	}{
		{"a live slug", "/works/project-hail-mary", http.StatusNotModified},
		{"an unknown slug", "/works/no-such-work", http.StatusNotFound},
		{"a retired slug", "/works/project-hail-mary-audiobook", http.StatusMovedPermanently},
		// The wildcard is matched out of the list form too.
		{"in a list", "/works/no-such-work", http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			header := "*"
			if tc.name == "in a list" {
				header = `W/"nonsense", *`
			}
			resp := conditionalGet(t, ts.URL+tc.path, header)
			if resp.StatusCode != tc.want {
				t.Fatalf("GET %s with If-None-Match: %s = %d, want %d", tc.path, header, resp.StatusCode, tc.want)
			}
			switch tc.want {
			case http.StatusNotModified:
				wantBodyless(t, resp)
				if got := resp.Header.Get("ETag"); got == "" {
					t.Error("304 carries no ETag for the client to keep")
				}
			case http.StatusMovedPermanently:
				if got := resp.Header.Get("Location"); got != "/works/project-hail-mary" {
					t.Errorf("Location = %q", got)
				}
			}
		})
	}
}

// ---- unit-level helpers -----------------------------------------------------

func TestSplitShell(t *testing.T) {
	cases := []struct {
		name string
		page string
		ok   bool
	}{
		{"all three markers", "A" + headOpenMarker + "B" + headCloseMarker + "C" + bodyMarker + "D", true},
		{"no head open", "A" + headCloseMarker + "C" + bodyMarker + "D", false},
		{"no head close", "A" + headOpenMarker + "C" + bodyMarker + "D", false},
		{"no body marker", "A" + headOpenMarker + "B" + headCloseMarker + "C", false},
		{"a doubled marker", "A" + headOpenMarker + "B" + headCloseMarker + "C" + bodyMarker + bodyMarker, false},
		{"out of order", "A" + headOpenMarker + "B" + bodyMarker + "C" + headCloseMarker, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sh, err := splitShell(tc.page)
			if (err == nil) != tc.ok {
				t.Fatalf("splitShell = %v, want ok=%v", err, tc.ok)
			}
			if tc.ok && sh.render("<HEAD>", "<BODY>") !=
				"A"+headOpenMarker+"<HEAD>"+headCloseMarker+"C"+"<BODY>"+"D" {
				t.Errorf("render = %q", sh.render("<HEAD>", "<BODY>"))
			}
		})
	}
}

func TestISODuration(t *testing.T) {
	cases := map[int]string{0: "", -5: "", 45: "PT45M", 60: "PT1H", 970: "PT16H10M", 120: "PT2H"}
	for in, want := range cases {
		if got := isoDuration(in); got != want {
			t.Errorf("isoDuration(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestFormatRuntime(t *testing.T) {
	cases := map[int]string{0: "", 45: "45 min", 60: "1 h", 970: "16 h 10 min"}
	for in, want := range cases {
		if got := formatRuntime(in); got != want {
			t.Errorf("formatRuntime(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestListPosition(t *testing.T) {
	cases := []struct {
		in   string
		want int
		ok   bool
	}{
		{"1", 1, true}, {"10", 10, true}, {"2.5", 0, false}, {"1-3.5", 0, false},
		{"", 0, false}, {"abc", 0, false},
	}
	for _, tc := range cases {
		got, ok := listPosition(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("listPosition(%q) = %d, %v, want %d, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

// TestTruncateDescription pins the two rules a truncated description has to
// keep: it ends on a word, and it does not end on a separator.
func TestTruncateDescription(t *testing.T) {
	short := "Project Hail Mary, by Andy Weir."
	if got := truncateDescription(short); got != short {
		t.Errorf("a short description was altered: %q", got)
	}
	long := truncateDescription(strings.Repeat("wordy ", 60) + "tail")
	if len(long) > descriptionMax {
		t.Errorf("len = %d, want <= %d", len(long), descriptionMax)
	}
	if strings.HasSuffix(long, " ") || strings.HasSuffix(long, ",") {
		t.Errorf("truncated description ends on a separator: %q", long)
	}
	if !strings.HasSuffix(long, "wordy") {
		t.Errorf("truncation did not land on a word boundary: %q", long)
	}
}

// TestOGImagePrefersAnHTTPSCover: a social crawler drops mixed content, so an
// http cover leaves the card imageless - the site icon is the better answer.
func TestOGImagePrefersAnHTTPSCover(t *testing.T) {
	cases := map[string]string{
		"https://example.test/c.jpg": "https://example.test/c.jpg",
		"http://example.test/c.jpg":  testSiteURL + defaultOGImage,
		"":                           testSiteURL + defaultOGImage,
	}
	for in, want := range cases {
		if got := ogImage(testSiteURL, in); got != want {
			t.Errorf("ogImage(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestSiteURLIsNormalized pins the two things New has to do with the flag: fill
// in the default, and strip a trailing slash so no URL is ever built with "//".
func TestSiteURLIsNormalized(t *testing.T) {
	cases := map[string]string{
		"":                          defaultSiteURL,
		"https://meta.test/":        "https://meta.test",
		"https://meta.test///":      "https://meta.test",
		"https://meta.test/sub/dir": "https://meta.test/sub/dir",
	}
	for in, want := range cases {
		srv, err := New(Config{
			DBPath: buildFixtureDB(t, fixtureCatalog()), SiteURL: in,
			Logger: log.New(io.Discard, "", 0), swapGrace: time.Minute,
		})
		if err != nil {
			t.Fatal(err)
		}
		if srv.cfg.SiteURL != want {
			t.Errorf("SiteURL %q normalized to %q, want %q", in, srv.cfg.SiteURL, want)
		}
	}
}
