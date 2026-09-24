package serve

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestServerDeadlines pins the listener Run serves on to the named deadlines.
// An unset field is not a default here, it is NO bound: a client that stops
// reading would hold its connection and goroutine forever.
func TestServerDeadlines(t *testing.T) {
	srv, _ := newTestServer(t)
	hs := srv.httpServer()
	for _, tc := range []struct {
		name      string
		got, want time.Duration
	}{
		{"ReadHeaderTimeout", hs.ReadHeaderTimeout, readHeaderTimeout},
		{"ReadTimeout", hs.ReadTimeout, readTimeout},
		{"WriteTimeout", hs.WriteTimeout, writeTimeout},
		{"IdleTimeout", hs.IdleTimeout, idleTimeout},
	} {
		if tc.got <= 0 || tc.got != tc.want {
			t.Errorf("%s = %s, want %s", tc.name, tc.got, tc.want)
		}
	}
	if hs.Handler == nil {
		t.Error("the listener has no handler, so it would serve http.DefaultServeMux")
	}
	// The one ordering the comment on idleTimeout argues for: the proxy in front
	// must give up on an idle connection before this side does.
	if idleTimeout <= 2*time.Minute {
		t.Errorf("idleTimeout = %s, want longer than the common proxy idle timeouts (up to 2m)", idleTimeout)
	}
}

// fetch is getNoFollowWith with gzip accepted explicitly, so the transport
// neither adds Accept-Encoding nor strips the Content-Encoding the rows assert.
func fetch(t *testing.T, url string, hdr map[string]string) *http.Response {
	t.Helper()
	h := map[string]string{"Accept-Encoding": "gzip"}
	for k, v := range hdr {
		h[k] = v
	}
	return getNoFollowWith(t, url, h)
}

// TestSecurityHeaders: nosniff on every response, the two document headers on
// every response that is (or answers for) an HTML document and on nothing else.
// The rows are chosen to cross every shape the middleware could get wrong - the
// bodyless 304 and the 301 on both surfaces, the gzip'd sitemap, the static
// site's own HTML, its assets and its 404 page - and each row re-asserts the one
// header its surface already promised (CORS, gzip, the redirect's Location), so
// adding the headers is shown not to have displaced anything.
func TestSecurityHeaders(t *testing.T) {
	srv, ts := newPageServerFrom(t, quietConfig(t, fixtureCatalog(), markedShells))
	writeSiteFile(t, srv.cfg.Site, "styles.css", "body{}")
	writeSiteFile(t, srv.cfg.Site, "about/index.html", "<html>ABOUT</html>")

	page := fetch(t, ts.URL+"/works/project-hail-mary", nil)
	spec := fetch(t, ts.URL+"/api/v1/openapi.json", nil)
	landing := fetch(t, ts.URL+"/", nil)

	cases := []struct {
		name     string
		path     string
		hdr      map[string]string
		status   int
		document bool
		also     map[string]string // a header the surface already carried, still there
	}{
		{"JSON route", "/api/v1/works/project-hail-mary", nil, http.StatusOK, false,
			map[string]string{"Access-Control-Allow-Origin": "*", "Content-Encoding": "gzip"}},
		{"ABS provider", "/abs/search?query=hail", nil, http.StatusOK, false,
			map[string]string{"Access-Control-Allow-Origin": "*"}},
		{"API 404", "/api/v1/works/no-such-work", nil, http.StatusNotFound, false, nil},
		// With a site configured, "/" claims every path nothing else did, so an
		// unrouted URL is the site's own 404 PAGE - a document.
		{"unrouted 404", "/api/v1/nothing-here", nil, http.StatusNotFound, true, nil},
		{"sitemap shard", "/sitemaps/works-0.xml", nil, http.StatusOK, false,
			map[string]string{"Content-Encoding": "gzip"}},
		{"API 301", "/api/v1/works/project-hail-mary-audiobook", nil, http.StatusMovedPermanently, false,
			map[string]string{"Location": "/api/v1/works/project-hail-mary"}},
		{"API 304", "/api/v1/openapi.json", map[string]string{"If-None-Match": spec.Header.Get("ETag")},
			http.StatusNotModified, false, map[string]string{"ETag": spec.Header.Get("ETag")}},
		{"sitemap", "/sitemap-index.xml", nil, http.StatusOK, false,
			map[string]string{"Content-Encoding": "gzip"}},
		{"entity page", "/works/project-hail-mary", nil, http.StatusOK, true,
			map[string]string{"Content-Encoding": "gzip"}},
		{"entity 304", "/works/project-hail-mary", map[string]string{"If-None-Match": page.Header.Get("ETag")},
			http.StatusNotModified, true, map[string]string{"ETag": page.Header.Get("ETag")}},
		{"entity 301", "/works/project-hail-mary-audiobook", nil, http.StatusMovedPermanently, true,
			map[string]string{"Location": "/works/project-hail-mary"}},
		{"legacy 301", "/work?id=project-hail-mary", nil, http.StatusMovedPermanently, true,
			map[string]string{"Location": "/works/project-hail-mary"}},
		{"entity 404", "/works/no-such-work", nil, http.StatusNotFound, true, nil},
		{"static landing", "/", nil, http.StatusOK, true, nil},
		{"static page", "/about", nil, http.StatusOK, true, nil},
		{"static .html file", "/404.html", nil, http.StatusOK, true, nil},
		{"static 304", "/", map[string]string{"If-Modified-Since": landing.Header.Get("Last-Modified")},
			http.StatusNotModified, true, nil},
		{"static 404", "/nope", nil, http.StatusNotFound, true, nil},
		{"static asset", "/styles.css", nil, http.StatusOK, false, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := fetch(t, ts.URL+tc.path, tc.hdr)
			if resp.StatusCode != tc.status {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.status)
			}
			if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
				t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
			}
			for name, want := range map[string]string{
				"Referrer-Policy":         "strict-origin-when-cross-origin",
				"Content-Security-Policy": "frame-ancestors 'none'",
			} {
				got := resp.Header.Get(name)
				switch {
				case tc.document && got != want:
					t.Errorf("%s = %q, want %q", name, got, want)
				case !tc.document && got != "":
					t.Errorf("%s = %q on a non-document response", name, got)
				}
			}
			for name, want := range tc.also {
				if got := resp.Header.Get(name); got != want {
					t.Errorf("%s = %q, want %q", name, got, want)
				}
			}
			if tc.status == http.StatusNotModified && resp.Header.Get("Content-Encoding") != "" {
				t.Error("a 304 announced a Content-Encoding")
			}
		})
	}
}

// TestSecurityHeadersOnAnAPIOnlyServer covers the responses a deployment with no
// site answers: the 503 a poll-only boot gives before its first release lands
// (still carrying nosniff and still saying when to retry), and the mux's own 404,
// which no route handler writes and the outermost middleware still reaches.
func TestSecurityHeadersOnAnAPIOnlyServer(t *testing.T) {
	srv, _ := newTestServer(t)
	if rec := serveRecorded(srv, "/nothing-here"); rec.StatusCode != http.StatusNotFound ||
		rec.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("mux 404 = %d with X-Content-Type-Options %q", rec.StatusCode, rec.Header.Get("X-Content-Type-Options"))
	}
	// The state a poll-only boot is in before its first release lands.
	t.Cleanup(srv.cur.Swap(nil).close)
	for _, path := range []string{"/healthz", "/api/v1/stats"} {
		rec := serveRecorded(srv, path)
		if rec.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("GET %s = %d, want 503", path, rec.StatusCode)
		}
		if got := rec.Header.Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("GET %s X-Content-Type-Options = %q, want nosniff", path, got)
		}
		if rec.Header.Get("Retry-After") == "" {
			t.Errorf("GET %s lost its Retry-After", path)
		}
		if rec.Header.Get("Content-Security-Policy") != "" {
			t.Errorf("GET %s carries a document header", path)
		}
	}
}

// TestIsHTMLFile pins the extension rule to the one http.FileServer types a
// file by.
func TestIsHTMLFile(t *testing.T) {
	for name, want := range map[string]bool{
		"index.html": true, "a/b/page.htm": true, "UPPER.HTML": true,
		"styles.css": false, "app.js": false, "sitemap-0.xml": false,
		"robots.txt": false, "hero.webp": false, "noext": false,
	} {
		if got := isHTMLFile(name); got != want {
			t.Errorf("isHTMLFile(%q) = %v, want %v", name, got, want)
		}
	}
}

// serveRecorded runs one GET through the server's handler in process.
func serveRecorded(srv *Server, path string) *http.Response {
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec.Result()
}
