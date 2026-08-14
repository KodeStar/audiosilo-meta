package serve

import (
	"compress/gzip"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// gzipResponseWriter compresses the response body. Content-Length is dropped
// (it no longer matches) and Content-Encoding is announced lazily on first
// write, so handlers that only set headers (e.g. an error) still work.
//
// A status that forbids a body is passed through untouched - see ensureHeader.
type gzipResponseWriter struct {
	http.ResponseWriter
	gz          *gzip.Writer
	wroteHeader bool
	// raw records that the status announced no encoding. Anything a handler
	// writes after such a status then goes through verbatim, rather than into a
	// compressed stream the response never claimed to carry.
	raw bool
}

// bodylessStatus reports whether status forbids a response body: 1xx, 204 and
// 304 (RFC 9110 15.4.5 / 15.3.5).
func bodylessStatus(status int) bool {
	return status == http.StatusNoContent || status == http.StatusNotModified ||
		(status >= 100 && status < 200)
}

// ensureHeader writes the status once, announcing gzip only for a response that
// can carry a body.
//
// The exception is not cosmetic. A 304 answers a conditional request by telling
// the client its CACHED copy is still good, and the client applies the 304's
// content headers to that copy - so `Content-Encoding: gzip` on a 304 tells a
// client holding an identity-encoded copy to gunzip it. The spec route is the
// live case: it revalidates on an ETag, and the middleware wraps it. The same
// reasoning covers the CORS preflight's 204.
func (g *gzipResponseWriter) ensureHeader(status int) {
	if g.wroteHeader {
		return
	}
	g.wroteHeader = true
	if bodylessStatus(status) {
		g.raw = true
		g.ResponseWriter.WriteHeader(status)
		return
	}
	h := g.Header()
	h.Del("Content-Length")
	h.Set("Content-Encoding", "gzip")
	h.Add("Vary", "Accept-Encoding")
	g.ResponseWriter.WriteHeader(status)
}

func (g *gzipResponseWriter) WriteHeader(status int) { g.ensureHeader(status) }

func (g *gzipResponseWriter) Write(b []byte) (int, error) {
	g.ensureHeader(http.StatusOK)
	if g.raw {
		return g.ResponseWriter.Write(b)
	}
	if g.gz == nil {
		g.gz = gzip.NewWriter(g.ResponseWriter)
	}
	return g.gz.Write(b)
}

// close flushes the compressor, if one was ever needed. Creating it lazily in
// Write is what keeps a bodyless response from emitting a gzip header and
// footer for a body that does not exist.
func (g *gzipResponseWriter) close() {
	if g.gz != nil {
		_ = g.gz.Close()
	}
}

// gzipMW compresses responses when the client accepts gzip.
func gzipMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			next.ServeHTTP(w, r)
			return
		}
		gzw := &gzipResponseWriter{ResponseWriter: w}
		defer gzw.close()
		next.ServeHTTP(gzw, r)
	})
}

// siteHandler serves a static site directory. Astro emits real .html pages, so
// there is no SPA fallback; an extension-less path is resolved to its
// index.html, and a genuine miss returns the site's 404.html when present.
//
// It is returned as its CONCRETE type rather than as an http.Handler because the
// entity pages need two things from it beyond ServeHTTP: the untouched shell
// (their degrade path) and notFound, so an unknown slug renders the site's own
// 404 page with a 404 status rather than net/http's plain text.
type siteHandler struct {
	dir string
	fs  http.Handler
}

func newSiteHandler(dir string) *siteHandler {
	return &siteHandler{dir: dir, fs: http.FileServer(http.Dir(dir))}
}

func (h *siteHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	upath := r.URL.Path
	if strings.Contains(upath, "..") {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	clean := filepath.Clean(strings.TrimPrefix(upath, "/"))
	full := filepath.Join(h.dir, clean)

	if info, err := os.Stat(full); err == nil && !info.IsDir() {
		h.fs.ServeHTTP(w, r)
		return
	}
	// Directory or extension-less route: try <path>/index.html. The root ("/")
	// cleans to "." whose Ext is "." (not ""), so it needs its own case - it is
	// the site's landing page, not a miss.
	if clean == "." || filepath.Ext(clean) == "" {
		idx := filepath.Join(full, "index.html")
		if info, err := os.Stat(idx); err == nil && !info.IsDir() {
			http.ServeFile(w, r, idx)
			return
		}
	}
	h.notFound(w, r)
}

func (h *siteHandler) notFound(w http.ResponseWriter, r *http.Request) {
	if f, err := os.Open(filepath.Join(h.dir, "404.html")); err == nil {
		defer func() { _ = f.Close() }()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.Copy(w, f)
		return
	}
	http.NotFound(w, r)
}
