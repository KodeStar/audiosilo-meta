package serve

import (
	"net/http"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/artifacttest"
)

func TestAPICacheHeaders(t *testing.T) {
	_, ts := newTestServer(t)
	for _, tc := range []struct {
		path, want string
	}{
		{"/api/v1/stats", apiMaxAge},
		{"/api/v1/works/" + artifacttest.HailMary, apiMaxAge},
		{"/api/v1/works/latest", apiMaxAge},
		{"/api/v1/lookup?asin=" + artifacttest.HailMaryASIN, apiMaxAge},
		{"/api/v1/search?q=hail", searchMaxAge},
		{"/api/v1/works/search?q=hail", searchMaxAge},
		{"/api/v1/people/search?q=weir", searchMaxAge},
		{"/abs/search?mediaType=book&query=hail", searchMaxAge},
		// Not a 200: never cacheable by default.
		{"/api/v1/works/no-such-work", ""},
		{"/api/v1/search", ""},
		{"/api/v1/librivox/audiobooks", ""}, // the fixture holds no LibriVox book: 404
		{"/api/v1/librivox/audiobooks?limit=501", ""},
		// The readiness probe is not an API answer.
		{"/healthz", ""},
	} {
		resp := get(t, ts.URL+tc.path, nil)
		if got := resp.Header.Get("Cache-Control"); got != tc.want {
			t.Errorf("GET %s (%d) Cache-Control = %q, want %q", tc.path, resp.StatusCode, got, tc.want)
		}
	}
	// A route that states its own keeps it.
	if got := get(t, ts.URL+"/api/v1/openapi.json", nil).Header.Get("Cache-Control"); got != specMaxAge {
		t.Errorf("openapi Cache-Control = %q, want its own %q", got, specMaxAge)
	}
	resp := get(t, ts.URL+"/api/v1/watch/feed.json?s="+artifacttest.Stormlight, nil)
	if got := resp.Header.Get("Cache-Control"); resp.StatusCode != http.StatusOK || got == apiMaxAge || got == "" {
		t.Errorf("watch feed (%d) Cache-Control = %q, want its own", resp.StatusCode, got)
	}
}
