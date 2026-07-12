// Package serve is the read-only HTTP API over the compiled metadata artifact.
// It opens the SQLite database produced by internal/build, exposes a small JSON
// API (search, work/person/series detail, ASIN/ISBN lookup, stats, coverage), and can
// optionally poll GitHub Releases to hot-swap in a newer artifact without a
// restart. All content is public, so there is no auth; CORS is wide open on the
// API surface. Business logic lives here; cmd/metaserve is a thin wrapper.
package serve

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Config configures a Server.
type Config struct {
	Addr     string        // listen address, e.g. ":8080"
	DBPath   string        // local artifact to serve (dev); empty => must poll
	Site     string        // optional static site directory served at "/"
	Poll     bool          // fetch/refresh the artifact from GitHub Releases
	Repo     string        // owner/name, e.g. "KodeStar/audiosilo-meta"
	Interval time.Duration // poll interval
	CacheDir string        // where downloaded artifacts are gunzipped
	Token    string        // optional GITHUB_TOKEN for a higher rate limit
	Logger   *log.Logger   // nil => log.Default()

	// swapGrace is how long an old snapshot is kept open after a swap so that
	// in-flight requests finish on it. Overridable for tests; default 60s.
	swapGrace time.Duration
}

// Server holds the current snapshot and serves the API. The snapshot is swapped
// atomically; readers load the pointer once per request.
type Server struct {
	cfg Config
	log *log.Logger

	cur atomic.Pointer[snapshot]

	site http.Handler
	mux  http.Handler

	gh *ghClient

	mu     sync.Mutex // guards refresh() so two polls never race
	loaded string     // tag of the currently-loaded release ("" for local db)
}

// New builds a Server. When DBPath is set it is loaded immediately; otherwise
// (with Poll) the newest data release is fetched synchronously so the server
// never starts empty.
func New(cfg Config) (*Server, error) {
	if cfg.Logger == nil {
		cfg.Logger = log.Default()
	}
	if cfg.Interval <= 0 {
		cfg.Interval = time.Hour
	}
	if cfg.Repo == "" {
		cfg.Repo = "KodeStar/audiosilo-meta"
	}
	if cfg.swapGrace <= 0 {
		cfg.swapGrace = 60 * time.Second
	}
	s := &Server{cfg: cfg, log: cfg.Logger}
	if cfg.Poll {
		s.gh = newGHClient(cfg.Repo, cfg.Token, "")
	}

	if cfg.DBPath != "" {
		snap, err := openSnapshot(cfg.DBPath, "")
		if err != nil {
			return nil, err
		}
		s.cur.Store(snap)
	} else if cfg.Poll {
		if err := s.refresh(context.Background()); err != nil {
			return nil, err
		}
	} else {
		return nil, errors.New("serve: no --db and --poll not set: nothing to serve")
	}

	if cfg.Site != "" {
		s.site = newSiteHandler(cfg.Site)
	}
	s.mux = s.buildMux()
	return s, nil
}

// Handler returns the http.Handler for the server (exposed for tests).
func (s *Server) Handler() http.Handler { return s.mux }

// Run starts the HTTP server and, when configured, the background poller. It
// blocks until ctx is cancelled or the listener fails.
func (s *Server) Run(ctx context.Context) error {
	if s.cfg.Poll {
		go s.pollLoop(ctx)
	}
	srv := &http.Server{
		Addr:              s.cfg.Addr,
		Handler:           s.mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()
	err := srv.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// current returns the live snapshot.
func (s *Server) current() *snapshot { return s.cur.Load() }

// swap installs a new snapshot and schedules the old one's close after the grace
// period, so requests that already grabbed the old handle finish cleanly.
func (s *Server) swap(next *snapshot) {
	old := s.cur.Swap(next)
	if old != nil && old != next {
		grace := s.cfg.swapGrace
		time.AfterFunc(grace, old.close)
	}
}

// ---- routing ----------------------------------------------------------------

func (s *Server) buildMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.Handle("GET /api/v1/stats", s.api(s.handleStats))
	mux.Handle("GET /api/v1/search", s.api(s.handleSearch))
	mux.Handle("GET /api/v1/works/latest", s.api(s.handleLatest))
	mux.Handle("GET /api/v1/works/{id}", s.api(s.handleWork))
	mux.Handle("GET /api/v1/works/{id}/recordings/{rid}/chapters", s.api(s.handleChapters))
	mux.Handle("GET /api/v1/people/{id}", s.api(s.handlePerson))
	mux.Handle("GET /api/v1/series/{id}", s.api(s.handleSeries))
	mux.Handle("GET /api/v1/lookup", s.api(s.handleLookup))
	mux.Handle("GET /api/v1/coverage", s.api(s.handleCoverage))
	if s.site != nil {
		mux.Handle("/", s.site)
	}
	return mux
}

// api wraps a JSON API handler with CORS and gzip.
func (s *Server) api(h http.HandlerFunc) http.Handler {
	return gzipMW(corsMW(h))
}

// ---- middleware -------------------------------------------------------------

func corsMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", "*")
		h.Add("Vary", "Origin")
		if r.Method == http.MethodOptions {
			h.Set("Access-Control-Allow-Methods", "GET, OPTIONS")
			h.Set("Access-Control-Allow-Headers", "*")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ---- JSON helpers -----------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	_ = enc.Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// clampLimit parses the ?limit= param and clamps it to [1, max], defaulting to
// def when absent or invalid.
func clampLimit(raw string, def, max int) int {
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return def
	}
	if n > max {
		return max
	}
	return n
}

// ---- handlers ---------------------------------------------------------------

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	snap := s.current()
	writeJSON(w, http.StatusOK, map[string]any{
		"status":   "ok",
		"built_at": snap.stats.BuiltAt,
		"works":    snap.stats.Works,
	})
}

func (s *Server) handleStats(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.current().stats)
}

func (s *Server) handleLatest(w http.ResponseWriter, r *http.Request) {
	limit := clampLimit(r.URL.Query().Get("limit"), 12, 50)
	cards, err := s.current().latestWorks(limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"works": cards})
}

func (s *Server) handleWork(w http.ResponseWriter, r *http.Request) {
	snap := s.current()
	detail, err := snap.workDetail(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if detail == nil {
		writeErr(w, http.StatusNotFound, "work not found")
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (s *Server) handleChapters(w http.ResponseWriter, r *http.Request) {
	snap := s.current()
	chs, err := snap.chapters(r.PathValue("id"), r.PathValue("rid"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"chapters": chs})
}

func (s *Server) handlePerson(w http.ResponseWriter, r *http.Request) {
	snap := s.current()
	p, err := snap.person(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if p == nil {
		writeErr(w, http.StatusNotFound, "person not found")
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) handleSeries(w http.ResponseWriter, r *http.Request) {
	snap := s.current()
	ser, err := snap.series(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if ser == nil {
		writeErr(w, http.StatusNotFound, "series not found")
		return
	}
	writeJSON(w, http.StatusOK, ser)
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeErr(w, http.StatusBadRequest, "q is required")
		return
	}
	limit := clampLimit(r.URL.Query().Get("limit"), 20, 50)
	results, err := s.current().search(q, limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

// handleCoverage reports expressive-layer coverage (characters/recaps/recap
// summaries) plus series position gaps. It always returns 200 and degrades on
// older artifacts (see snapshot.coverage) rather than reporting everything as
// missing.
func (s *Server) handleCoverage(w http.ResponseWriter, _ *http.Request) {
	res, err := s.current().coverage()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleLookup(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	asin := strings.TrimSpace(q.Get("asin"))
	isbn := strings.TrimSpace(q.Get("isbn"))
	if asin == "" && isbn == "" {
		writeErr(w, http.StatusBadRequest, "asin or isbn is required")
		return
	}
	snap := s.current()
	res, err := snap.lookup(asin, isbn)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if res == nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	writeJSON(w, http.StatusOK, res)
}
