package serve

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
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
// this is the same key the request path uses.
func TestEveryIDRouteResolvesRetiredSlugs(t *testing.T) {
	srv := &Server{cfg: Config{WebhookSecret: strings.Repeat("s", minWebhookSecretBytes)}}
	registered := map[string]bool{}
	for _, r := range srv.routes() {
		registered[r.pattern] = true
		if !strings.Contains(r.pattern, "{"+idWildcard+"}") {
			continue
		}
		if _, ok := redirectNamespaces[r.pattern]; !ok {
			t.Errorf("route %s addresses a record by {%s} but names no redirect namespace: "+
				"a retired slug would 404 there", r.pattern, idWildcard)
		}
	}
	for pattern := range redirectNamespaces {
		if !registered[pattern] {
			t.Errorf("redirectNamespaces names %s, which Server.routes does not register", pattern)
		}
	}
}
