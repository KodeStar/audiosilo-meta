package serve

import (
	"net/http"
	"strings"
)

// The Cache-Control a successful API answer carries when its handler set none:
// a release changes the data at most a few times a day, so a shared cache (or a
// browser) may reuse an answer for a while. The searches - typed a keystroke at a
// time, and the one family whose answer a reader expects to track the box - get
// the shorter window. A handler that states its own (the watch feeds, the spec, a
// retired slug's 301) keeps it.
const (
	apiMaxAge    = "public, max-age=600"
	searchMaxAge = "public, max-age=60"
)

// cacheMaxAgeFor is the default Cache-Control of a route's 200s.
func cacheMaxAgeFor(pattern string) string {
	if strings.HasSuffix(pattern, "/search") || strings.HasSuffix(pattern, "/match") {
		return searchMaxAge
	}
	return apiMaxAge
}

// cacheMW stamps value as Cache-Control on a 200 whose handler set none. Every
// other status - an error, a 429, a 503 while no data is loaded - is left
// uncacheable.
func cacheMW(value string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(&cacheWriter{ResponseWriter: w, value: value}, r)
	})
}

type cacheWriter struct {
	http.ResponseWriter
	value   string
	written bool
}

func (c *cacheWriter) WriteHeader(status int) {
	if !c.written {
		c.written = true
		if h := c.Header(); status == http.StatusOK && h.Get("Cache-Control") == "" {
			h.Set("Cache-Control", c.value)
		}
	}
	c.ResponseWriter.WriteHeader(status)
}

func (c *cacheWriter) Write(b []byte) (int, error) {
	if !c.written {
		c.WriteHeader(http.StatusOK)
	}
	return c.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the writer underneath.
func (c *cacheWriter) Unwrap() http.ResponseWriter { return c.ResponseWriter }
