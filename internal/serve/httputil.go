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
type gzipResponseWriter struct {
	http.ResponseWriter
	gz          *gzip.Writer
	wroteHeader bool
}

func (g *gzipResponseWriter) ensureHeader(status int) {
	if g.wroteHeader {
		return
	}
	g.wroteHeader = true
	h := g.Header()
	h.Del("Content-Length")
	h.Set("Content-Encoding", "gzip")
	h.Add("Vary", "Accept-Encoding")
	g.ResponseWriter.WriteHeader(status)
}

func (g *gzipResponseWriter) WriteHeader(status int) { g.ensureHeader(status) }

func (g *gzipResponseWriter) Write(b []byte) (int, error) {
	g.ensureHeader(http.StatusOK)
	return g.gz.Write(b)
}

// gzipMW compresses responses when the client accepts gzip.
func gzipMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			next.ServeHTTP(w, r)
			return
		}
		gz := gzip.NewWriter(w)
		defer func() { _ = gz.Close() }()
		next.ServeHTTP(&gzipResponseWriter{ResponseWriter: w, gz: gz}, r)
	})
}

// siteHandler serves a static site directory. Astro emits real .html pages, so
// there is no SPA fallback; an extension-less path is resolved to its
// index.html, and a genuine miss returns the site's 404.html when present.
type siteHandler struct {
	dir string
	fs  http.Handler
}

func newSiteHandler(dir string) http.Handler {
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
