// Package serve is the read-only HTTP API over the compiled metadata artifact.
// It opens the SQLite database produced by internal/build, exposes a small JSON
// API (search, work/person/series detail, ASIN/ISBN lookup, stats, coverage), and can
// hot-swap a newer GitHub Release artifact on a signed webhook or fallback poll
// without a restart. All content is public, so there is no auth; CORS is wide
// open on the API surface. Business logic lives here; cmd/metaserve is a thin
// wrapper.
package serve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	Addr          string        // listen address, e.g. ":8080"
	DBPath        string        // local artifact to serve (dev); empty => must poll
	Site          string        // optional static site directory served at "/"
	Poll          bool          // fetch/refresh the artifact from GitHub Releases
	Repo          string        // owner/name, e.g. "KodeStar/audiosilo-meta"
	Interval      time.Duration // fallback poll interval
	CacheDir      string        // where downloaded artifacts are gunzipped
	Token         string        // optional GITHUB_TOKEN for a higher rate limit
	WebhookSecret string        // optional HMAC secret for release refresh webhooks
	Logger        *log.Logger   // nil => log.Default()

	// swapGrace is how long an old snapshot is kept open after a swap so that
	// in-flight requests finish on it. Overridable for tests; default 60s.
	// Superseded cache files are pruned once it elapses.
	swapGrace time.Duration

	// maxPatchBase caps the artifact size for which a binary-delta refresh is
	// attempted (see applyPatchFile). Overridable for tests; New defaults it to
	// defaultMaxPatchBase.
	maxPatchBase int64

	// bootRetry is the FIRST wait between poll attempts while no artifact has
	// loaded at all (it then backs off - see pollLoop). Overridable for tests;
	// New defaults it to defaultBootRetry.
	bootRetry time.Duration

	// apiBase overrides the GitHub API base URL. Test-only: production always
	// talks to api.github.com.
	apiBase string
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

	webhookRefreshing atomic.Bool // coalesces webhook-triggered refreshes to one in flight
}

// New builds a Server. When DBPath is set it is loaded immediately; otherwise
// (with Poll) the newest data release is fetched synchronously so the server
// never starts empty.
//
// A poll-only boot whose first fetch FAILS is not fatal: the image ships no
// baked artifact, so a GitHub outage at boot time must not turn into a container
// crash-loop. New logs the failure and returns a Server with no snapshot; it
// listens, serves the static site, answers /healthz with "starting" and every
// API route with 503, and the poll loop retries every cfg.bootRetry until an
// artifact lands (see pollLoop). Nothing serves stale or partial data in the
// meantime, because there is nothing to serve.
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
	if cfg.bootRetry <= 0 {
		cfg.bootRetry = defaultBootRetry
	}
	if cfg.maxPatchBase <= 0 {
		cfg.maxPatchBase = defaultMaxPatchBase
	}
	if cfg.WebhookSecret != "" {
		if !cfg.Poll {
			return nil, errors.New("serve: METASERVE_WEBHOOK_SECRET requires --poll")
		}
		if len(cfg.WebhookSecret) < minWebhookSecretBytes {
			return nil, fmt.Errorf("serve: METASERVE_WEBHOOK_SECRET must be at least %d bytes", minWebhookSecretBytes)
		}
	}
	s := &Server{cfg: cfg, log: cfg.Logger}
	if cfg.Poll {
		s.gh = newGHClient(cfg.Repo, cfg.Token, cfg.apiBase)
	}

	if cfg.DBPath != "" {
		snap, err := openSnapshot(cfg.DBPath, "")
		if err != nil {
			return nil, err
		}
		s.cur.Store(snap)
	} else if cfg.Poll {
		if err := s.refresh(context.Background()); err != nil {
			s.log.Printf("serve: no artifact at boot: %v", err)
			s.log.Printf("serve: starting WITHOUT data; the API answers 503 until a release loads (retrying in %s, backing off to %s)",
				cfg.bootRetry, cfg.Interval)
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

// current returns the live snapshot, or nil when none has loaded yet (a
// poll-only boot whose first fetch failed - see New).
func (s *Server) current() *snapshot { return s.cur.Load() }

// defaultBootRetry is the first retry wait for a server with NO artifact. It is
// much shorter than the steady-state poll interval: a boot that could not reach
// GitHub is an outage to recover from in seconds, not in an hour. Consecutive
// failures back off from here up to cfg.Interval (see pollLoop).
const defaultBootRetry = 30 * time.Second

// swap installs a new snapshot and schedules the old one's close after the grace
// period, so requests that already grabbed the old handle finish cleanly. Once
// the grace elapses the old artifact's file is prunable, so a prune runs then
// too - adopt already pruned everything else at swap time.
func (s *Server) swap(next *snapshot) {
	old := s.cur.Swap(next)
	if old != nil && old != next {
		grace := s.cfg.swapGrace
		time.AfterFunc(grace, func() {
			old.close()
			s.pruneCache()
		})
	}
}

// ---- routing ----------------------------------------------------------------

func (s *Server) buildMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	if s.cfg.WebhookSecret != "" {
		mux.HandleFunc("POST "+githubReleaseWebhookPath, s.handleGitHubReleaseWebhook)
	}
	mux.Handle("GET /api/v1/stats", s.api(s.handleStats))
	mux.Handle("GET /api/v1/search", s.api(s.handleSearch))
	mux.Handle("GET /api/v1/works/latest", s.api(s.handleLatest))
	mux.Handle("GET /api/v1/works/{id}", s.api(s.handleWork))
	mux.Handle("GET /api/v1/works/{id}/recordings/{rid}/chapters", s.api(s.handleChapters))
	mux.Handle("GET /api/v1/people/{id}", s.api(s.handlePerson))
	mux.Handle("GET /api/v1/series/{id}", s.api(s.handleSeries))
	mux.Handle("GET /api/v1/lookup", s.api(s.handleLookup))
	mux.Handle("GET /api/v1/coverage", s.api(s.handleCoverage))
	mux.Handle("GET /api/v1/coverage/works", s.api(s.handleCoverageWorks))
	mux.Handle("GET /api/v1/coverage/series-gaps", s.api(s.handleCoverageSeriesGaps))
	// Audiobookshelf custom metadata provider (ABS appends /search to the
	// configured base URL). Outside /api/v1; the specific pattern wins over "/".
	mux.Handle("GET /abs/search", s.api(s.handleABSSearch))
	if s.site != nil {
		mux.Handle("/", s.site)
	}
	return mux
}

// api wraps a JSON API handler with CORS, gzip, and the loaded-artifact gate.
func (s *Server) api(h http.HandlerFunc) http.Handler {
	return gzipMW(corsMW(s.requireSnapshot(h)))
}

// requireSnapshot answers 503 while no artifact has loaded, so every data
// handler can assume s.current() is non-nil. Only a poll-only boot that could
// not reach GitHub is in that state (see New); it is temporary by construction,
// hence Retry-After.
func (s *Server) requireSnapshot(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.current() == nil {
			w.Header().Set("Retry-After", strconv.Itoa(int(s.cfg.bootRetry.Seconds())))
			writeErr(w, http.StatusServiceUnavailable, "no data loaded yet: the server is fetching the latest release")
			return
		}
		next(w, r)
	}
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
// def when absent or invalid. A def of 0 is how an endpoint spells "no window at
// all by default" (snapshot.series), since 0 is never reachable from a supplied
// value.
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

// clampOffset parses the ?offset= param into a non-negative row offset,
// defaulting to 0 when absent, invalid, or negative.
func clampOffset(raw string) int {
	if raw == "" {
		return 0
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// ---- handlers ---------------------------------------------------------------

// handleHealthz reports readiness, not liveness: a server that has not loaded an
// artifact yet answers 503 with status "starting", so a container health check
// or a load balancer keeps it out of rotation until it can actually answer.
func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	snap := s.current()
	if snap == nil {
		w.Header().Set("Retry-After", strconv.Itoa(int(s.cfg.bootRetry.Seconds())))
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "starting"})
		return
	}
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

// personPageDefault / personPageMax bound one page of a person's credit lists.
// The default is a page, not the whole list: a corporate credit ("Full Cast")
// already narrates ~2,000 works, and that count grows with the catalogue. The
// unpaged totals travel in the response so a client always knows what it is
// missing.
const (
	personPageDefault = 100
	personPageMax     = 500
)

func (s *Server) handlePerson(w http.ResponseWriter, r *http.Request) {
	snap := s.current()
	q := r.URL.Query()
	limit := clampLimit(q.Get("limit"), personPageDefault, personPageMax)
	p, err := snap.person(r.PathValue("id"), limit, clampOffset(q.Get("offset")))
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

// seriesPageMax bounds an explicitly requested series page. There is no
// default: ?limit absent means the whole series (see snapshot.series - the
// player's series rail is composed from the full list).
const seriesPageMax = 500

func (s *Server) handleSeries(w http.ResponseWriter, r *http.Request) {
	snap := s.current()
	q := r.URL.Query()
	ser, err := snap.series(r.PathValue("id"), clampLimit(q.Get("limit"), 0, seriesPageMax), clampOffset(q.Get("offset")))
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

// handleCoverage reports the top-line expressive-layer totals
// (characters/recaps/recap summaries). The per-work list and series gaps are
// their own paginated endpoints. It always returns 200 and degrades on older
// artifacts (see snapshot.coverage) rather than reporting everything as missing.
func (s *Server) handleCoverage(w http.ResponseWriter, _ *http.Request) {
	res, err := s.current().coverage()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// handleCoverageWorks serves one filtered, searchable, paginated page of works
// for the contribute-page coverage browser. ?filter selects the dimension
// (missing|has_characters|has_recaps|has_recap_summary), ?q is a title/author
// substring, ?limit/?offset paginate. It always returns 200 and degrades to an
// empty page with available:false when the filter's dimension is unevaluable at
// the current artifact schema_version (see snapshot.coverageWorks).
func (s *Server) handleCoverageWorks(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filter, ok := validCoverageFilter(q.Get("filter"))
	if !ok {
		writeErr(w, http.StatusBadRequest, "unknown filter")
		return
	}
	limit := clampLimit(q.Get("limit"), 25, 100)
	offset := clampOffset(q.Get("offset"))
	res, err := s.current().coverageWorks(filter, strings.TrimSpace(q.Get("q")), limit, offset)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// handleCoverageSeriesGaps serves one searchable, paginated page of series with
// interior position gaps. ?q is a series-name substring; ?limit/?offset
// paginate. series_gaps has no schema_version dependency, so it is always
// available.
func (s *Server) handleCoverageSeriesGaps(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := clampLimit(q.Get("limit"), 25, 100)
	offset := clampOffset(q.Get("offset"))
	res, err := s.current().seriesGapsPage(strings.TrimSpace(q.Get("q")), limit, offset)
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
