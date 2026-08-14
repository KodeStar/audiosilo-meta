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
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kodestar/audiosilo-meta/pkg/model"
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

	// retired counts the artifact files of snapshots that have been swapped out
	// but not yet closed, so the cache prune spares every one of them (see
	// swap/pruneCacheLocked). Refcounted, because two retired snapshots can share
	// a path. Guarded by mu.
	retired map[string]int

	// nextRetry is the poll loop's CURRENT wait while no artifact has loaded, in
	// nanoseconds - what the 503s advertise as Retry-After. Written by the poll
	// loop, read by handlers, hence atomic.
	nextRetry atomic.Int64

	webhookRefreshing atomic.Bool // coalesces webhook-triggered refreshes to one in flight
}

// New builds a Server. When DBPath is set it is loaded immediately; otherwise
// (with Poll) the newest data release is fetched synchronously so the server
// never starts empty.
//
// A poll-only boot whose first fetch FAILS is not fatal: the image ships no
// baked artifact, so a GitHub outage at boot time must not turn into a container
// crash-loop. New logs the failure and falls back to the newest artifact on the
// cache volume - the one the previous container was serving - flagged as stale
// (see adoptStaleCache). Only when there is nothing there either does it return
// a Server with no snapshot; it then listens, serves the static site, answers
// /healthz with "starting" and every API route with 503, and the poll loop
// retries from cfg.bootRetry (backing off) until an artifact lands (see
// pollLoop).
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
	s := &Server{cfg: cfg, log: cfg.Logger, retired: map[string]int{}}
	s.nextRetry.Store(int64(cfg.bootRetry))
	if cfg.Poll {
		s.gh = newGHClient(cfg.Repo, cfg.Token, cfg.apiBase)
	}

	if cfg.DBPath != "" {
		snap, err := openSnapshot(cfg.DBPath, "")
		if err != nil {
			return nil, err
		}
		snap.log = s.log
		s.cur.Store(snap)
	} else if cfg.Poll {
		if err := s.refresh(context.Background()); err != nil {
			s.log.Printf("serve: no artifact at boot: %v", err)
			// A cache volume that outlived the last container usually still holds
			// its artifact. Serving that, loudly flagged as stale, beats answering
			// 503 with usable data on disk.
			if !s.adoptStaleCache() {
				s.log.Printf("serve: starting WITHOUT data; the API answers 503 until a release loads (retrying in %s, backing off to %s)",
					cfg.bootRetry, cfg.Interval)
			}
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
//
// The old snapshot's FILE is registered as retired for exactly as long as the
// handle lives, because the handle is not enough to keep it usable: SQLite opens
// pooled connections lazily, so a request still on the old snapshot can need to
// open that path after the swap. Several snapshots can be in grace at once, and
// the prune must spare all of them (see pruneCacheLocked).
//
// The caller must hold s.mu (adopt does): s.retired is refresh-owned state.
func (s *Server) swap(next *snapshot) {
	old := s.cur.Swap(next)
	if old == nil || old == next {
		return
	}
	s.retired[old.path]++
	time.AfterFunc(s.cfg.swapGrace, func() {
		old.close()
		s.mu.Lock()
		defer s.mu.Unlock()
		s.retired[old.path]--
		if s.retired[old.path] <= 0 {
			delete(s.retired, old.path)
		}
		s.pruneCacheLocked()
	})
}

// ---- routing ----------------------------------------------------------------

// route is one mux registration: the ServeMux pattern and the handler behind it.
type route struct {
	pattern string
	handler http.Handler
}

// specPath is the route's OpenAPI path key - its pattern minus the method.
// ServeMux spells a wildcard "{id}", exactly as OpenAPI templates one, so the
// two path vocabularies are already the same.
func (r route) specPath() string {
	_, path, _ := strings.Cut(r.pattern, " ")
	return path
}

// routes is the API surface, in registration order. It exists as DATA because it
// has two consumers: buildMux registers it, and TestOpenAPICoversEveryRoute
// diffs the embedded spec's path set against it - so the spec is pinned to what
// the server actually serves rather than to a third hand-written copy of the
// list.
//
// The static site at "/" is deliberately not here: it is not part of the API and
// has no spec entry (see buildMux).
func (s *Server) routes() []route {
	rs := []route{
		{"GET /healthz", http.HandlerFunc(s.handleHealthz)},
	}
	// Present only when a webhook secret is configured, because that is exactly
	// when it is registered: an unconfigured deployment does not serve the hook.
	if s.cfg.WebhookSecret != "" {
		rs = append(rs, route{"POST " + githubReleaseWebhookPath, http.HandlerFunc(s.handleGitHubReleaseWebhook)})
	}
	return append(rs,
		// The machine-readable description of everything below. It is STATIC, so
		// it deliberately skips the loaded-artifact gate: a client discovering the
		// API must get the spec even on a boot that has no data yet.
		route{"GET /api/v1/openapi.json", s.public(http.HandlerFunc(handleOpenAPI))},
		route{"GET /api/v1/stats", s.api(s.handleStats)},
		route{"GET /api/v1/search", s.api(s.searchHandler(kindAny))},
		// Type-scoped searches. A literal segment beats "{id}" in ServeMux's
		// precedence rules, so each coexists with its family's detail route
		// exactly as works/latest already does.
		route{"GET /api/v1/works/search", s.api(s.searchHandler(kindWork))},
		route{"GET /api/v1/people/search", s.api(s.searchHandler(kindPerson))},
		route{"GET /api/v1/series/search", s.api(s.searchHandler(kindSeries))},
		route{"GET /api/v1/works/latest", s.api(s.handleLatest)},
		route{"GET /api/v1/works/{id}", s.api(s.handleWork)},
		route{"GET /api/v1/works/{id}/recordings/{rid}/chapters", s.api(s.handleChapters)},
		route{"GET /api/v1/people/{id}", s.api(s.handlePerson)},
		route{"GET /api/v1/series/{id}", s.api(s.handleSeries)},
		route{"GET /api/v1/lookup", s.api(s.handleLookup)},
		route{"GET /api/v1/coverage", s.api(s.handleCoverage)},
		route{"GET /api/v1/coverage/works", s.api(s.handleCoverageWorks)},
		route{"GET /api/v1/coverage/series-gaps", s.api(s.handleCoverageSeriesGaps)},
		// Audiobookshelf custom metadata provider (ABS appends /search to the
		// configured base URL). Outside /api/v1; the specific pattern wins over "/".
		route{"GET /abs/search", s.api(s.handleABSSearch)},
	)
}

// idWildcard is the wildcard every family's detail route addresses its record
// by. A route carrying it is a route a retired slug can arrive at.
const idWildcard = "id"

// redirectNamespaces says which id namespace a route's {id} names. It sits beside
// routes() because it is the same knowledge: a route that addresses a record by
// slug can be reached by a slug a merge retired, and the namespace is what
// resolves it (and, being the route segment too, what rebuilds the Location - see
// model.RedirectKind).
//
// It is keyed by the ServeMux pattern, so it is checkable against the route table
// rather than believed: TestEveryIDRouteResolvesRetiredSlugs requires every
// registered pattern containing {id} to appear here, which is how a fifth family
// route cannot ship without redirect support.
var redirectNamespaces = map[string]model.RedirectKind{
	"GET /api/v1/works/{id}":                           model.RedirectWorks,
	"GET /api/v1/works/{id}/recordings/{rid}/chapters": model.RedirectWorks,
	"GET /api/v1/people/{id}":                          model.RedirectPeople,
	"GET /api/v1/series/{id}":                          model.RedirectSeries,
}

func (s *Server) buildMux() http.Handler {
	mux := http.NewServeMux()
	for _, r := range s.routes() {
		mux.Handle(r.pattern, r.handler)
	}
	// The static site catches everything the API surface did not claim.
	if s.site != nil {
		mux.Handle("/", s.site)
	}
	return mux
}

// public is the middleware stack every publicly reachable handler wears: CORS,
// because browsers call this API directly, and gzip. One spelling of it, so no
// route can pick up half the stack.
func (s *Server) public(h http.Handler) http.Handler {
	return gzipMW(corsMW(h))
}

// api is public plus the loaded-artifact gate: what every handler that reads a
// snapshot needs.
func (s *Server) api(h http.HandlerFunc) http.Handler {
	return s.public(s.requireSnapshot(h))
}

// requireSnapshot answers 503 while no artifact has loaded, so every data
// handler can assume s.current() is non-nil. Only a poll-only boot that could
// not reach GitHub is in that state (see New); it is temporary by construction,
// hence Retry-After.
func (s *Server) requireSnapshot(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.current() == nil {
			w.Header().Set("Retry-After", s.retryAfter())
			writeErr(w, http.StatusServiceUnavailable, "no data loaded yet: the server is fetching the latest release")
			return
		}
		next(w, r)
	}
}

// retryAfter is the Retry-After value for the 503s a server with no artifact
// answers: the poll loop's CURRENT wait, not the initial bootRetry. After
// several failed attempts the loop has backed off (up to --interval), and
// advertising 30s then would send every client back long before anything can
// have changed. Whole seconds, rounded up, never below 1 - a "0" would read as
// "retry immediately".
func (s *Server) retryAfter() string {
	secs := int(math.Ceil(time.Duration(s.nextRetry.Load()).Seconds()))
	return strconv.Itoa(max(secs, 1))
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
		w.Header().Set("Retry-After", s.retryAfter())
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
	detail, err := snap.workDetail(r.PathValue(idWildcard))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if detail == nil {
		if redirected(w, r, snap) {
			return
		}
		writeErr(w, http.StatusNotFound, "work not found")
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

// handleChapters answers an unknown (work, recording) pair with an empty list
// rather than a 404 - a recording legitimately has no chapters - so the retired
// slug is consulted when the list comes back EMPTY. It can only ever hit on a
// slug the catalogue does not hold: a live work is never a redirect source
// (pkg/check's checkRedirects), so a real recording with no chapters still gets
// its empty list.
func (s *Server) handleChapters(w http.ResponseWriter, r *http.Request) {
	snap := s.current()
	chs, err := snap.chapters(r.PathValue(idWildcard), r.PathValue("rid"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if len(chs) == 0 && redirected(w, r, snap) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"chapters": chs})
}

// redirected answers a request whose {id} names a RETIRED slug with a 301 at the
// same route under the slug that replaced it, and reports whether it did.
//
// Every id route calls it where it would otherwise 404, so a slug a merge retired
// keeps resolving (see model.Redirects for why that matters). The BODY carries the
// new slug too, so a client that does not follow redirects can heal what it stored
// instead of only learning that the id is gone.
//
// It is composed entirely from ROUTE DATA rather than per-route arguments: the
// namespace comes from redirectNamespaces keyed by the pattern that matched, and
// the Location is that same pattern with its wildcards filled in - the new slug for
// {id}, the request's own values for the rest - so no route can be handed a
// Location belonging to another one, and a route that gains a wildcard needs no
// change here. The request's query string travels along, because a ?limit/?offset
// window describes the request rather than the id.
//
// It reads the snapshot the handler already answered from rather than loading the
// pointer again, so a hot-swap mid-request cannot make the 404 and the redirect
// decision come from two different artifacts. A lookup FAILURE degrades to "no
// redirect": the caller then answers its own 404, which is what the request looked
// like anyway, rather than turning a missing record into a 500.
func redirected(w http.ResponseWriter, r *http.Request, snap *snapshot) bool {
	kind, ok := redirectNamespaces[r.Pattern]
	if !ok {
		return false // not a route a retired slug can arrive at
	}
	id := r.PathValue(idWildcard)
	to, err := snap.redirectTarget(kind, id)
	if err != nil {
		snap.logf("serve: redirect lookup for %s %q failed: %v", kind, id, err)
		return false
	}
	if to == "" {
		return false
	}
	u := &url.URL{Path: redirectLocation(r, to), RawQuery: r.URL.RawQuery}
	w.Header().Set("Location", u.String())
	writeJSON(w, http.StatusMovedPermanently, map[string]string{"redirect": to})
	return true
}

// redirectLocation rebuilds the matched route's path with to standing in for the
// {id} the request arrived with, every other wildcard keeping the value it
// matched, and every segment escaped - the ids in the artifact are plain slugs,
// but a header built by string concatenation is exactly the kind of thing that
// stops being true later.
func redirectLocation(r *http.Request, to string) string {
	_, pattern, _ := strings.Cut(r.Pattern, " ")
	var b strings.Builder
	for _, seg := range strings.Split(strings.TrimPrefix(pattern, "/"), "/") {
		b.WriteByte('/')
		name, wild := strings.CutPrefix(seg, "{")
		if !wild {
			b.WriteString(seg)
			continue
		}
		value := to
		if name = strings.TrimSuffix(name, "}"); name != idWildcard {
			value = r.PathValue(name)
		}
		b.WriteString(url.PathEscape(value))
	}
	return b.String()
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
	p, err := snap.person(r.PathValue(idWildcard), limit, clampOffset(q.Get("offset")))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if p == nil {
		if redirected(w, r, snap) {
			return
		}
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
	ser, err := snap.series(r.PathValue(idWildcard), clampLimit(q.Get("limit"), 0, seriesPageMax), clampOffset(q.Get("offset")))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if ser == nil {
		if redirected(w, r, snap) {
			return
		}
		writeErr(w, http.StatusNotFound, "series not found")
		return
	}
	writeJSON(w, http.StatusOK, ser)
}

// searchPageDefault / searchPageMax bound one page of search results. Every
// search endpoint shares them: a client that learns the window on /search knows
// it on the type-scoped ones too.
const (
	searchPageDefault = 20
	searchPageMax     = 50
)

// searchHandler builds the search endpoint for one kind - kindAny for the
// combined search, kindWork/kindPerson/kindSeries for the type-scoped ones. The
// four differ only in that value, so the q/limit parsing, the empty-q 400 and
// the {"results": [...]} envelope live here once rather than in four
// near-identical handlers.
func (s *Server) searchHandler(kind searchKind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := strings.TrimSpace(r.URL.Query().Get("q"))
		if q == "" {
			writeErr(w, http.StatusBadRequest, "q is required")
			return
		}
		limit := clampLimit(r.URL.Query().Get("limit"), searchPageDefault, searchPageMax)
		results, err := s.current().search(kind, q, limit)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"results": results})
	}
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
// (missing|has_characters|has_recaps|has_recap_summary), ?q is a full-text query
// over title/authors/narrators/series (word prefixes), ?limit/?offset paginate. It always returns 200 and degrades to an
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
