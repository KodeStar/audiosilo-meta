package serve

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/query"
)

// getNoFollow issues the request WITHOUT following redirects, which is the whole
// point wherever a redirect is the thing under test: http.DefaultClient would
// follow it and every assertion would be about the destination instead. Shared
// with site_test.go, whose 301 comes from http.FileServer rather than from here.
func getNoFollow(t *testing.T, base, path string) *http.Response {
	t.Helper()
	return getNoFollowWith(t, base+path, nil)
}

// getNoFollowWith is getNoFollow carrying request headers - the one request
// builder conditionalGet and the header tests share. A header named here is sent
// as given, so an explicit Accept-Encoding stops the transport adding its own and
// transparently stripping the Content-Encoding a test is asserting on. The caller
// does not close the body.
func getNoFollowWith(t *testing.T, url string, hdr map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// wantRedirect asserts the response is the 301 contract: the status, the
// Location, and the body naming the new slug so a client that does not follow
// redirects can heal the id it stored.
func wantRedirect(t *testing.T, resp *http.Response, wantLocation, wantSlug string) {
	t.Helper()
	if resp.StatusCode != http.StatusMovedPermanently {
		t.Fatalf("status = %d, want 301", resp.StatusCode)
	}
	if got := resp.Header.Get("Location"); got != wantLocation {
		t.Errorf("Location = %q, want %q", got, wantLocation)
	}
	// A tombstone has to be revocable: an unbounded 301 lets a client or a CDN
	// keep serving it after a bad merge is reversed.
	if got := resp.Header.Get("Cache-Control"); got != query.RedirectMaxAge {
		t.Errorf("Cache-Control = %q, want %q", got, query.RedirectMaxAge)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]string
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("body %q: %v", body, err)
	}
	if out["redirect"] != wantSlug {
		t.Errorf("body redirect = %q, want %q", out["redirect"], wantSlug)
	}
}

// TestRetiredSlugRedirects is the behavioural contract of the tombstone
// mechanism, on every id route: a retired slug answers 301 at the same route
// under the slug that replaced it.
func TestRetiredSlugRedirects(t *testing.T) {
	_, ts := newTestServer(t)

	cases := []struct {
		name     string
		path     string
		location string
		slug     string
	}{
		{
			name:     "work",
			path:     "/api/v1/works/project-hail-mary-audiobook",
			location: "/api/v1/works/project-hail-mary",
			slug:     "project-hail-mary",
		},
		{
			// The chapters route answers an unknown pair with an empty list, so
			// this is the one case where the redirect is consulted on a 200 path.
			name:     "recording chapters",
			path:     "/api/v1/works/project-hail-mary-audiobook/recordings/ray-porter-2021/chapters",
			location: "/api/v1/works/project-hail-mary/recordings/ray-porter-2021/chapters",
			slug:     "project-hail-mary",
		},
		{
			name:     "person",
			path:     "/api/v1/people/andy-weir-author",
			location: "/api/v1/people/andy-weir",
			slug:     "andy-weir",
		},
		{
			name:     "series",
			path:     "/api/v1/series/stormlight-archive",
			location: "/api/v1/series/the-stormlight-archive",
			slug:     "the-stormlight-archive",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wantRedirect(t, getNoFollow(t, ts.URL, tc.path), tc.location, tc.slug)
		})
	}
}

// TestRetiredSlugRedirectKeepsTheQuery pins that the window travels with the
// redirect: ?limit/?offset describe the request, not the id, so dropping them
// would silently hand a follower a different page than it asked for.
func TestRetiredSlugRedirectKeepsTheQuery(t *testing.T) {
	_, ts := newTestServer(t)
	resp := getNoFollow(t, ts.URL, "/api/v1/people/andy-weir-author?limit=5&offset=10")
	wantRedirect(t, resp, "/api/v1/people/andy-weir?limit=5&offset=10", "andy-weir")
}

// TestRetiredSlugRedirectIsFollowable checks the end-to-end effect, with an
// ordinary client: the caller ends up holding the surviving record, which is why
// 301 is transparent to the site's fetches and to audiosilo-server's
// community-metadata seam.
func TestRetiredSlugRedirectIsFollowable(t *testing.T) {
	_, ts := newTestServer(t)
	code, body := getJSON(t, ts.URL, "/api/v1/works/project-hail-mary-audiobook")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200 after following the redirect", code)
	}
	if body["id"] != "project-hail-mary" {
		t.Errorf("id = %v, want project-hail-mary", body["id"])
	}
}

// TestUnknownSlugStillNotFound is the other half of the rule: only a RETIRED
// slug redirects. An id nothing ever held is still a 404, and a live work with a
// recording that has no chapters is still an empty list.
func TestUnknownSlugStillNotFound(t *testing.T) {
	_, ts := newTestServer(t)
	for _, path := range []string{
		"/api/v1/works/no-such-work",
		"/api/v1/people/no-such-person",
		"/api/v1/series/no-such-series",
	} {
		if code, _ := getJSON(t, ts.URL, path); code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, code)
		}
	}
	code, body := getJSON(t, ts.URL, "/api/v1/works/words-of-radiance/recordings/nope/chapters")
	if code != http.StatusOK {
		t.Fatalf("chapters of a live work = %d, want 200", code)
	}
	if chs, ok := body["chapters"].([]any); !ok || len(chs) != 0 {
		t.Errorf("chapters = %v, want an empty list", body["chapters"])
	}
}

// TestRedirectsTolerateOlderArtifact serves a schema_version 4 release with the
// redirects table dropped - a newer binary briefly serving an older release. The
// retired slug must 404 as it did before the mechanism existed, never 500.
func TestRedirectsTolerateOlderArtifact(t *testing.T) {
	ts := downgradedServer(t, downgradedDB(t, fixtureCatalog(), 4, "redirects"))
	if code, _ := getJSON(t, ts.URL, "/api/v1/works/project-hail-mary-audiobook"); code != http.StatusNotFound {
		t.Errorf("retired work slug on a v4 artifact = %d, want 404", code)
	}
	if code, _ := getJSON(t, ts.URL, "/api/v1/people/andy-weir-author"); code != http.StatusNotFound {
		t.Errorf("retired person slug on a v4 artifact = %d, want 404", code)
	}
	// And the route it gates still serves the live record.
	if code, _ := getJSON(t, ts.URL, "/api/v1/works/project-hail-mary"); code != http.StatusOK {
		t.Errorf("live work on a v4 artifact = %d, want 200", code)
	}
}

// redirectNamespaces says which id namespace a route's record wildcard names,
// over the routes that are metaserve's OWN (the pages, the sitemaps, the webhook
// and the spec). A route that addresses a record by slug can be reached by a slug
// a merge retired, and the namespace is what resolves it.
//
// The pages are the only own routes that address a record, and they are folded
// in from the ONE table that defines them (htmlEntityRoutes, whose namespace the
// page handler resolves by), so a page family cannot be added without its
// redirect. The API routes are pkg/query's, guarded by its own
// TestEveryRecordRouteNamesANamespace.
var redirectNamespaces = func() map[string]model.RedirectKind {
	m := map[string]model.RedirectKind{}
	for _, e := range htmlEntityRoutes {
		m[e.pattern] = e.namespace
	}
	return m
}()

// redirectExemptRoutes are the own wildcard routes that deliberately resolve no
// retired slug. It exists so that the guard can be answered in the only two ways
// that are honest - name the namespace, or say out loud that this wildcard is not
// a record - rather than by a route quietly not appearing in either list. A
// multi-segment wildcard ({rest...}) belongs here: it is a path, not an id.
//
// The sitemap shard's wildcard is a FILE NAME (works-3.xml), not a slug: it names
// a window over a family, so there is no retired id for it to resolve and an
// unknown one is the 404 parseShardFile already gives it.
var redirectExemptRoutes = map[string]bool{
	"GET " + sitemapShardPrefix + "{" + sitemapFileWildcard + "}": true,
}

// redirectCoverageGaps returns the patterns that address a record by a wildcard
// and neither name a namespace nor say they are exempt. The SHAPE of the pattern
// decides, not the wildcard's name, so "GET /api/v1/publishers/{pid}" is as much
// a gap as a {id} route would be.
func redirectCoverageGaps(patterns []string) []string {
	var gaps []string
	for _, pattern := range patterns {
		if !hasAnyWildcard(pattern) {
			continue // a fully literal route addresses no record
		}
		if _, named := redirectNamespaces[pattern]; named || redirectExemptRoutes[pattern] {
			continue
		}
		gaps = append(gaps, pattern)
	}
	return gaps
}

// hasAnyWildcard reports whether a pattern carries a wildcard of ANY form - a
// record's {id} as much as a {rest...} path or a filter segment, everything but
// the {$} anchor. A route with one has to be decided about, out loud, rather
// than skipped for having no {id} segment.
func hasAnyWildcard(pattern string) bool {
	_, path, _ := strings.Cut(pattern, " ")
	for _, seg := range strings.Split(path, "/") {
		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") && seg != "{$}" {
			return true
		}
	}
	return false
}

// ownRoutes is every route metaserve registers that is NOT pkg/query's: the
// pages, the sitemaps, and the webhook and spec routes() appends after the API.
func ownRoutes(srv *Server) []route {
	api := map[string]bool{}
	for _, r := range query.Routes() {
		api[r.Pattern] = true
	}
	own := append(srv.htmlRoutes(), srv.sitemapRoutes()...)
	for _, r := range srv.routes() {
		if !api[r.pattern] {
			own = append(own, r)
		}
	}
	return own
}

// TestEveryIDRouteResolvesRetiredSlugs is the drift guard, in the shape of
// TestOpenAPICoversEveryRoute: it diffs redirectNamespaces against the server's
// own route tables, so a further page that addresses a record by slug cannot
// ship without redirect support (and an entry naming a route that no longer
// exists cannot linger). The candidate set is derived from the pattern's SHAPE
// rather than from the wildcard being spelled {id} - see
// TestRedirectCoverageIgnoresTheWildcardsName.
//
// It covers metaserve's OWN routes: an entity PAGE addresses a record by the
// same slug an API route does, so a retired slug has to keep resolving there too
// (in HTML - see entityHandler), and the sitemap table has to answer the guard
// as well - which it does by naming its file wildcard exempt. The API routes are
// pkg/query's guard's (TestEveryRecordRouteNamesANamespace).
func TestEveryIDRouteResolvesRetiredSlugs(t *testing.T) {
	srv := &Server{cfg: Config{WebhookSecret: strings.Repeat("s", minWebhookSecretBytes)}, log: testLogger()}
	own := ownRoutes(srv)
	patterns := make([]string, 0, len(own))
	registered := map[string]bool{}
	for _, r := range own {
		patterns = append(patterns, r.pattern)
		registered[r.pattern] = true
	}
	for _, gap := range redirectCoverageGaps(patterns) {
		t.Errorf("route %s addresses a record by a wildcard but neither names a redirect "+
			"namespace nor appears in redirectExemptRoutes: a retired slug would 404 there", gap)
	}
	for pattern := range redirectNamespaces {
		if !registered[pattern] {
			t.Errorf("redirectNamespaces names %s, which the server does not register", pattern)
		}
	}
	for pattern := range redirectExemptRoutes {
		if !registered[pattern] {
			t.Errorf("redirectExemptRoutes names %s, which the server does not register", pattern)
		}
	}
}

// TestRedirectLocationEscapesExactlyOnce is the regression test for a double
// escape. The Location is the WIRE form, so a value that needs escaping must be
// escaped once: url.URL.String() over an already-escaped Path turned every % into
// %25 (caf%C3%A9-2021 -> caf%25C3%25A9-2021), which sends a following client to a
// recording id that does not exist. Latent while every id is an ASCII slug, and
// wrong by construction either way.
func TestRedirectLocationEscapesExactlyOnce(t *testing.T) {
	_, ts := newTestServer(t)
	resp := getNoFollow(t, ts.URL, "/api/v1/works/project-hail-mary-audiobook/recordings/caf%C3%A9-2021/chapters")
	wantRedirect(t, resp,
		"/api/v1/works/project-hail-mary/recordings/caf%C3%A9-2021/chapters", "project-hail-mary")
}

// TestRedirectCoverageIgnoresTheWildcardsName is the teeth of the coverage guard.
// Keying it on the literal "{id}" made it blind to exactly the route it exists to
// catch: a new family whose wildcard is spelled differently shipped silently.
func TestRedirectCoverageIgnoresTheWildcardsName(t *testing.T) {
	cases := []struct {
		name    string
		pattern string
		gap     bool
	}{
		{name: "a differently spelled id", pattern: "GET /api/v1/publishers/{pid}", gap: true},
		{name: "a nested wildcard route", pattern: "GET /api/v1/labels/{slug}/imprints/{iid}", gap: true},
		{name: "a multi-segment wildcard", pattern: "GET /files/{rest...}", gap: true},
		{name: "a literal route", pattern: "GET /api/v1/stats", gap: false},
		{name: "an anchored literal route", pattern: "GET /api/v1/coverage/{$}", gap: false},
		{name: "a route that names its namespace", pattern: "GET /series/{id}", gap: false},
		// A PAGE route is judged by the same rule: a fourth family's page that
		// addresses a record and names no namespace is a gap, and the pages that
		// exist are covered because htmlEntityRoutes folds them into the map.
		{name: "an unregistered page route", pattern: "GET /publishers/{id}", gap: true},
		{name: "a registered page route", pattern: "GET /works/{id}", gap: false},
		{name: "a legacy query-param page route", pattern: "GET /work", gap: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gaps := redirectCoverageGaps([]string{tc.pattern})
			if got := len(gaps) == 1; got != tc.gap {
				t.Errorf("redirectCoverageGaps(%q) = %v, want a gap: %v", tc.pattern, gaps, tc.gap)
			}
		})
	}
	// That the id wildcard is then read by POSITION, whatever it is called, is
	// pkg/query's TestRecordWildcardIsReadByPosition (the resolver is there).
}
