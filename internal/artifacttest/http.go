package artifacttest

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"testing"
)

// The HTTP side of the suites that serve these artifacts - pkg/query's handler
// tests and metaserve's - so a request helper is written once rather than once
// per package.

// QuietLogger is the logger a handler, database or server built in a test gets,
// so a degradation notice does not land in the test output.
func QuietLogger() *log.Logger { return log.New(io.Discard, "", 0) }

// GetJSON fetches base+path and decodes the body into a generic map (nil for an
// empty body).
func GetJSON(tb testing.TB, base, path string) (int, map[string]any) {
	tb.Helper()
	resp, err := http.Get(base + path) //nolint:noctx // test client
	if err != nil {
		tb.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if len(body) > 0 {
		if err := json.Unmarshal(body, &out); err != nil {
			tb.Fatalf("GET %s: decode %q: %v", path, body, err)
		}
	}
	return resp.StatusCode, out
}

// GetNoFollow issues the request WITHOUT following redirects, which is the whole
// point wherever a redirect is the thing under test: http.DefaultClient would
// follow it and every assertion would be about the destination instead. The
// body is closed at cleanup.
func GetNoFollow(tb testing.TB, base, path string) *http.Response {
	tb.Helper()
	return GetNoFollowWith(tb, base+path, nil)
}

// GetNoFollowWith is GetNoFollow carrying request headers. A header named here
// is sent as given, so an explicit Accept-Encoding stops the transport adding
// its own and transparently stripping the Content-Encoding a test is asserting
// on. The body is closed at cleanup.
func GetNoFollowWith(tb testing.TB, url string, hdr map[string]string) *http.Response {
	tb.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil) //nolint:noctx // test client
	if err != nil {
		tb.Fatal(err)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}
