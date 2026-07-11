package serve

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeSiteFixture lays out a minimal Astro-like static site:
//
//	index.html          the landing page
//	work/index.html     an extension-less route
//	styles.css          a plain asset
//	404.html            the custom not-found page
func writeSiteFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"index.html":      "<html>LANDING</html>",
		"work/index.html": "<html>WORK PAGE</html>",
		"styles.css":      "body{}",
		"404.html":        "<html>CUSTOM 404</html>",
	}
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func newSiteTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	dbPath := buildFixtureDB(t, fixtureCatalog(), nil)
	srv, err := New(Config{DBPath: dbPath, Site: writeSiteFixture(t), swapGrace: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func getBody(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(body)
}

// TestSiteRoot is the regression test for the "/" 404: the root path cleans to
// "." (whose filepath.Ext is ".", not ""), which used to skip the index.html
// branch and fall through to notFound.
func TestSiteRoot(t *testing.T) {
	ts := newSiteTestServer(t)
	code, body := getBody(t, ts.URL+"/")
	if code != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200", code)
	}
	if !strings.Contains(body, "LANDING") {
		t.Errorf("GET / body = %q, want the landing page", body)
	}
}

func TestSiteExtensionlessRoute(t *testing.T) {
	ts := newSiteTestServer(t)
	code, body := getBody(t, ts.URL+"/work")
	if code != http.StatusOK {
		t.Fatalf("GET /work status = %d, want 200", code)
	}
	if !strings.Contains(body, "WORK PAGE") {
		t.Errorf("GET /work body = %q, want work/index.html", body)
	}
}

func TestSitePlainAsset(t *testing.T) {
	ts := newSiteTestServer(t)
	code, body := getBody(t, ts.URL+"/styles.css")
	if code != http.StatusOK || body != "body{}" {
		t.Errorf("GET /styles.css = %d %q", code, body)
	}
}

func TestSiteCustom404(t *testing.T) {
	ts := newSiteTestServer(t)
	code, body := getBody(t, ts.URL+"/nope")
	if code != http.StatusNotFound {
		t.Fatalf("GET /nope status = %d, want 404", code)
	}
	if !strings.Contains(body, "CUSTOM 404") {
		t.Errorf("GET /nope body = %q, want the site's 404.html", body)
	}
}

// TestSiteIndexHTMLRedirect: http.FileServer 301-redirects /index.html to ./ ;
// following that redirect must land on the (now working) root, not a 404.
func TestSiteIndexHTMLRedirect(t *testing.T) {
	ts := newSiteTestServer(t)

	// First observe the 301 itself.
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := noFollow.Get(ts.URL + "/index.html")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusMovedPermanently {
		t.Fatalf("GET /index.html status = %d, want 301", resp.StatusCode)
	}

	// Then follow it (default client) and require the landing page.
	code, body := getBody(t, ts.URL+"/index.html")
	if code != http.StatusOK {
		t.Fatalf("GET /index.html (followed) status = %d, want 200", code)
	}
	if !strings.Contains(body, "LANDING") {
		t.Errorf("GET /index.html (followed) body = %q, want the landing page", body)
	}
}
