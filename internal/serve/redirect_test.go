package serve

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// getNoFollow issues the request WITHOUT following redirects, which is the whole
// point wherever a redirect is the thing under test: http.DefaultClient would
// follow it and every assertion would be about the destination instead. Shared
// with site_test.go, whose 301 comes from http.FileServer rather than from here.
func getNoFollow(t *testing.T, base, path string) *http.Response {
	t.Helper()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(base + path)
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
	if got := resp.Header.Get("Cache-Control"); got != redirectMaxAge {
		t.Errorf("Cache-Control = %q, want %q", got, redirectMaxAge)
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
	ts := downgradedServer(t, 4, "redirects")
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

// TestRedirectLookupIsIndexed pins the redirect probe to the artifact's primary
// key. It sits on the miss path of every id route, so a full scan here would be a
// table scan per 404 - and 404s are what a crawler and a stale client produce
// most of.
func TestRedirectLookupIsIndexed(t *testing.T) {
	snap := snapshotFor(t, fixtureCatalog())
	assertNoFullScan(t, queryPlan(t, snap, redirectTargetSQL, "works", "project-hail-mary-audiobook"))
}

// TestRedirectTargetResolves covers the query layer on its own: a hit, a miss,
// and that the namespaces do not leak into one another. (The version gate is
// covered end to end by TestRedirectsTolerateOlderArtifact, and the empty-table
// short circuit by TestRedirectTargetSkipsAnEmptyTable.)
func TestRedirectTargetResolves(t *testing.T) {
	snap := snapshotFor(t, fixtureCatalog())
	got, err := snap.redirectTarget("works", "project-hail-mary-audiobook")
	if err != nil || got != "project-hail-mary" {
		t.Errorf("redirectTarget(works) = %q, %v", got, err)
	}
	// The namespaces do not leak into one another.
	if got, err := snap.redirectTarget("people", "project-hail-mary-audiobook"); err != nil || got != "" {
		t.Errorf("redirectTarget(people) = %q, %v, want no hit", got, err)
	}
	if got, err := snap.redirectTarget("works", "project-hail-mary"); err != nil || got != "" {
		t.Errorf("a live slug resolved to %q, %v, want no hit", got, err)
	}
}

// TestRedirectTargetSkipsAnEmptyTable pins the memo: a catalogue that has retired
// nothing answers without touching the table, which matters because the chapters
// route consults the redirects on an ordinary 200 (an empty chapter list).
func TestRedirectTargetSkipsAnEmptyTable(t *testing.T) {
	cat := fixtureCatalog()
	cat.Redirects = nil
	snap := snapshotFor(t, cat)
	if snap.hasRedirects {
		t.Error("hasRedirects is true for a catalogue with no redirects")
	}
	if got, err := snap.redirectTarget("works", "project-hail-mary-audiobook"); err != nil || got != "" {
		t.Errorf("empty table = %q, %v, want no hit and no error", got, err)
	}
	// And with redirects present the memo says so, so the query does run.
	if full := snapshotFor(t, fixtureCatalog()); !full.hasRedirects {
		t.Error("hasRedirects is false for a catalogue that holds redirects")
	}
}

// TestEveryIDRouteResolvesRetiredSlugs is the drift guard, in the shape of
// TestOpenAPICoversEveryRoute: it diffs redirectNamespaces against the server's
// own route table, so a fifth route that addresses a record by slug cannot ship
// without redirect support (and an entry naming a route that no longer exists
// cannot linger). The pattern is what redirected() looks the namespace up by, so
// this is the same key the request path uses, and the candidate set is derived
// from the pattern's SHAPE rather than from the wildcard being spelled {id} - see
// TestRedirectCoverageIgnoresTheWildcardsName.
func TestEveryIDRouteResolvesRetiredSlugs(t *testing.T) {
	srv := &Server{cfg: Config{WebhookSecret: strings.Repeat("s", minWebhookSecretBytes)}}
	patterns := make([]string, 0, len(srv.routes()))
	registered := map[string]bool{}
	for _, r := range srv.routes() {
		patterns = append(patterns, r.pattern)
		registered[r.pattern] = true
	}
	for _, gap := range redirectCoverageGaps(patterns) {
		t.Errorf("route %s addresses a record by a wildcard but neither names a redirect "+
			"namespace nor appears in redirectExemptRoutes: a retired slug would 404 there", gap)
	}
	for pattern := range redirectNamespaces {
		if !registered[pattern] {
			t.Errorf("redirectNamespaces names %s, which Server.routes does not register", pattern)
		}
	}
	for pattern := range redirectExemptRoutes {
		if !registered[pattern] {
			t.Errorf("redirectExemptRoutes names %s, which Server.routes does not register", pattern)
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

// TestSelfRedirectDoesNotLoop covers the resolver's own guarantee against a loop.
// pkg/check refuses a self-row, so this artifact is hand-built to carry one: the
// request must fall through to the 404 it was already heading for rather than
// 301-ing a following client back to the same URL forever.
func TestSelfRedirectDoesNotLoop(t *testing.T) {
	cat := fixtureCatalog()
	cat.Redirects = model.Redirects{model.RedirectWorks: {"ghost-work": "ghost-work"}}
	ts := serverFor(t, cat)

	resp := getNoFollow(t, ts.URL, "/api/v1/works/ghost-work")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404: a self-redirect must not be served", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "" {
		t.Errorf("Location = %q, want none", loc)
	}
}

// TestSchemaVersion5RequiresTheRedirectsTable pins the load-time claim, and that
// the failure SAYS which claim it is: an artifact that reports version 5 without
// the table is corrupt, and "no such table: redirects" alone reads as a bug in the
// server rather than as a broken file.
func TestSchemaVersion5RequiresTheRedirectsTable(t *testing.T) {
	path := buildFixtureDB(t, fixtureCatalog())
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DROP TABLE redirects`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	_, err = openSnapshot(path, "")
	if err == nil {
		t.Fatal("openSnapshot accepted a version 5 artifact with no redirects table")
	}
	for _, want := range []string{"schema_version 5", "requires the redirects table", path} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
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
		{name: "a route that names its namespace", pattern: "GET /api/v1/works/{id}", gap: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gaps := redirectCoverageGaps([]string{tc.pattern})
			if got := len(gaps) == 1; got != tc.gap {
				t.Errorf("redirectCoverageGaps(%q) = %v, want a gap: %v", tc.pattern, gaps, tc.gap)
			}
		})
	}
	// And the id wildcard is read by POSITION, whatever it is called.
	if got := idWildcardOf("GET /api/v1/publishers/{pid}/imprints/{iid}"); got != "pid" {
		t.Errorf("idWildcardOf = %q, want pid", got)
	}
	if got := idWildcardOf("GET /api/v1/stats"); got != "" {
		t.Errorf("idWildcardOf of a literal route = %q, want empty", got)
	}
}
