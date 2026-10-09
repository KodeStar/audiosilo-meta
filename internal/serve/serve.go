// Package serve is metaserve: the read-only HTTP server over the compiled
// metadata artifact. The JSON API itself (search, work/person/series detail,
// ASIN/ISBN lookup, match, stats, coverage, watch feeds, the Audiobookshelf
// provider) is pkg/query's handler, served here behind CORS and gzip; this
// package adds what is metaserve's own - the hot swap of a newer GitHub Release
// artifact on a signed webhook or fallback poll without a restart, the
// server-rendered entity and guide pages, the sitemaps and the OpenAPI document.
// All content is public, so there is no auth; CORS is wide open on the API
// surface. Business logic lives here and in pkg/query; cmd/metaserve is a thin
// wrapper.
package serve

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kodestar/audiosilo-meta/internal/httpx"
	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/query"
)

// Config configures a Server.
type Config struct {
	Addr   string // listen address, e.g. ":8080"
	DBPath string // local artifact to serve (dev); empty => must poll
	Site   string // optional static site directory served at "/"
	// SiteURL is the server's PUBLIC origin, without a trailing slash. metaserve
	// cannot discover it (it sits behind a proxy and answers whatever Host it is
	// given), and the entity pages have to emit absolute canonical, og: and
	// JSON-LD URLs, so it is configuration. New defaults it to defaultSiteURL.
	SiteURL       string
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
	// talks to api.github.com. Setting it also admits that origin as an asset
	// origin (ghClient.allowOrigin), since an httptest server is plain HTTP on a
	// loopback IP with a port - three things the production asset rule refuses.
	apiBase string

	// matchBudget overrides how long one works/match may run
	// (query.PublicMatchBudget). Test-only, so a budget test does not have to
	// wait seconds.
	matchBudget time.Duration

	// now supplies the watch feed's window boundary (query.HandlerOptions.Now).
	// Test-only; production uses time.Now. Keeping it on the server makes
	// date-boundary tests deterministic without a package-global clock that would
	// race parallel tests.
	now func() time.Time
}

// Server holds the current snapshot and serves the API. The snapshot is swapped
// atomically; readers load the pointer once per request.
type Server struct {
	cfg Config
	log *log.Logger

	cur atomic.Pointer[query.DB]

	// query is the JSON API: pkg/query's handler over the live snapshot, the same
	// one a mirror-mode AudioSilo server mounts in-process. buildMux builds it and
	// routes() registers its patterns in front of it.
	query http.Handler

	site *siteHandler
	mux  http.Handler

	// shells is the marker split of the built entity shells, done once at
	// construction (see loadShells). Empty when no site directory is configured,
	// or when the dist carries no injectable shells - the pages then degrade to
	// the untouched static file.
	shells shells

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
	if cfg.SiteURL == "" {
		cfg.SiteURL = defaultSiteURL
	}
	if cfg.now == nil {
		cfg.now = time.Now
	}
	// Stripped once, here, so every canonical/og/JSON-LD URL is one join away
	// from being right rather than each having to defend against a "//".
	cfg.SiteURL = strings.TrimRight(cfg.SiteURL, "/")
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
		if cfg.apiBase != "" {
			s.gh.allowOrigin(cfg.apiBase) // test-only; apiBase is unexported
		}
	}

	if cfg.DBPath != "" {
		snap, err := query.Open(cfg.DBPath, "")
		if err != nil {
			return nil, err
		}
		snap.SetLogger(s.log)
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
		s.shells = loadShells(cfg.Site, cfg.SiteURL, s.log)
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
	srv := s.httpServer()
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

// The listener's four deadlines. Every one is a bound on how long a CLIENT can
// hold a connection and a goroutine, not on how long the server may work: nothing
// artifact-sized happens inside a request (the release webhook answers 202 and
// refreshes on a goroutine of its own - see handleGitHubReleaseWebhook), so the
// only slow thing a deadline can meet is the far end of the socket.
const (
	// readHeaderTimeout bounds the request line and headers - the slowloris guard.
	readHeaderTimeout = 10 * time.Second
	// readTimeout bounds the whole request READ, headers included. Every route is
	// a GET with no body except the webhook, whose body is capped at 1 MiB
	// (maxWebhookBodyBytes): 30s is 1 MiB at ~35 KB/s, and GitHub's own delivery
	// is a single small JSON document. It does NOT bound the handler: once the
	// request is read, net/http clears the read deadline before its background
	// read (connReader.startBackgroundRead), so r.Context() is not cancelled when
	// readTimeout elapses mid-handler - checked on Go 1.25. Only a body read still
	// in progress at that point (the webhook's) is cut off.
	readTimeout = 30 * time.Second
	// writeTimeout runs from the end of the request headers to the end of the
	// response, so it bounds the handler AND the transfer. Measured over the
	// 278,607-work artifact (2026-09-24): the slowest handler, cold, was ~3.4s (a
	// 200-series watch feed, and /abs/search on a stopword); the largest body is
	// a 50,000-URL sitemap shard, 6.2 MB identity / 0.5 MB gzip; the largest
	// static asset is ~360 KB. Two minutes carries the gzip shard at ~4.5 KB/s and
	// even the identity shard at ~52 KB/s, which is a slow client rather than a
	// dead one, while still releasing a connection that has stopped reading.
	writeTimeout = 2 * time.Minute
	// idleTimeout is how long a keep-alive connection waits for its next request.
	// Production sits behind nginx, whose upstream keepalive_timeout defaults to
	// 60s, and the proxy's idle timeout MUST stay below this one: when the
	// upstream closes an idle connection at the moment the proxy reuses it, the
	// proxy answers 502, so the proxy has to be the side that gives up first.
	// Left unset it would default to readTimeout.
	idleTimeout = 3 * time.Minute
)

// httpServer is the listener Run serves on, split out so the deadlines above
// are testable without binding a port.
func (s *Server) httpServer() *http.Server {
	return &http.Server{
		Addr:              s.cfg.Addr,
		Handler:           s.mux,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}
}

// current returns the live snapshot, or nil when none has loaded yet (a
// poll-only boot whose first fetch failed - see New).
func (s *Server) current() *query.DB { return s.cur.Load() }

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
func (s *Server) swap(next *query.DB) {
	old := s.cur.Swap(next)
	if old == nil || old == next {
		return
	}
	s.retired[old.Path()]++
	time.AfterFunc(s.cfg.swapGrace, func() {
		_ = old.Close()
		s.mu.Lock()
		defer s.mu.Unlock()
		s.retired[old.Path()]--
		if s.retired[old.Path()] <= 0 {
			delete(s.retired, old.Path())
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
// The JSON API itself is pkg/query's (query.Routes): every pattern it serves is
// registered here in front of s.query, wearing the public stack - except the
// readiness probe, which a health check calls rather than a browser. Only the
// routes that are metaserve's own (the release webhook and the spec) have
// handlers here.
//
// The static site at "/" is deliberately not here: it is not part of the API and
// has no spec entry (see buildMux).
func (s *Server) routes() []route {
	var rs []route
	for _, qr := range query.Routes() {
		h := s.public(s.query)
		if qr.Pattern == query.HealthzPattern {
			h = s.query
		}
		rs = append(rs, route{qr.Pattern, h})
	}
	// Present only when a webhook secret is configured, because that is exactly
	// when it is registered: an unconfigured deployment does not serve the hook.
	if s.cfg.WebhookSecret != "" {
		rs = append(rs, route{"POST " + githubReleaseWebhookPath, http.HandlerFunc(s.handleGitHubReleaseWebhook)})
	}
	// The machine-readable description of all of it. It is STATIC, so it
	// deliberately skips the loaded-artifact gate: a client discovering the API
	// must get the spec even on a boot that has no data yet.
	return append(rs, route{"GET /api/v1/openapi.json", s.public(http.HandlerFunc(handleOpenAPI))})
}

// idWildcard is what the entity page routes spell their record wildcard. Their
// handler reads it directly; the redirect machinery does NOT - it derives the
// name from the pattern (query's RedirectRetired), so nothing depends on the
// spelling being this one.
const idWildcard = "id"

// redirectNamespaces says which id namespace a route's record wildcard names,
// over EVERY route this server registers. A route that addresses a record by
// slug can be reached by a slug a merge retired, and the namespace is what
// resolves it (and, being the route segment too, what rebuilds the Location -
// see model.RedirectKind).
//
// It is keyed by the ServeMux pattern, so it is checkable against the route
// tables rather than believed: TestEveryIDRouteResolvesRetiredSlugs requires
// every registered pattern that HAS a wildcard to appear here or in
// redirectExemptRoutes, which is how a fifth family route cannot ship without
// redirect support. The API routes' namespaces are pkg/query's (query.Routes,
// which its handler resolves by); the HTML entity pages address the same records
// by the same slugs, so their patterns are folded in from the ONE pattern-keyed
// index of the table that defines them (htmlEntityRouteByPattern, which the page
// redirect reads too) rather than restated here - a page family cannot be added
// without its redirect, and the tables cannot disagree about which namespace a
// family resolves in.
var redirectNamespaces = func() map[string]model.RedirectKind {
	m := map[string]model.RedirectKind{}
	for _, r := range query.Routes() {
		if r.Namespace != "" {
			m[r.Pattern] = r.Namespace
		}
	}
	for pattern, e := range htmlEntityRouteByPattern {
		m[pattern] = e.namespace
	}
	return m
}()

// redirectExemptRoutes are the wildcard routes that deliberately resolve no
// retired slug. It exists so that the guard above can be answered in the only two
// ways that are honest - name the namespace, or say out loud that this wildcard is
// not a record - rather than by a route quietly not appearing in either list. A
// multi-segment wildcard ({rest...}) belongs here: it is a path, not an id.
//
// The sitemap shard's wildcard is a FILE NAME (works-3.xml), not a slug: it names
// a window over a family, so there is no retired id for it to resolve and an
// unknown one is the 404 parseShardFile already gives it.
//
// pkg/query's wildcard routes that name no namespace (the Audiobookshelf
// provider's language segment, a FILTER rather than a record) were decided about
// by that package's own guard, TestEveryRecordRouteNamesANamespace, so they are
// taken as it states them.
var redirectExemptRoutes = func() map[string]bool {
	m := map[string]bool{
		"GET " + sitemapShardPrefix + "{" + sitemapFileWildcard + "}": true,
	}
	for _, r := range query.Routes() {
		if r.Namespace == "" && hasAnyWildcard(r.Pattern) {
			m[r.Pattern] = true
		}
	}
	return m
}()

// redirectCoverageGaps returns the patterns that address a record by a wildcard
// and neither name a namespace nor say they are exempt. It is the guard's
// derivation, exported to the test rather than written there, so what the test
// checks is what the request path reads: the SHAPE of the pattern decides, not the
// wildcard's name, so "GET /api/v1/publishers/{pid}" is as much a gap as a
// {id} route would be.
func redirectCoverageGaps(patterns []string) []string {
	var gaps []string
	for _, pattern := range patterns {
		if !hasAnyWildcard(pattern) {
			continue // a fully literal route addresses no record
		}
		if _, named := redirectNamespaces[pattern]; named || redirectExemptRoutes[pattern] {
			continue
		}
		gaps = append(gaps, pattern)
	}
	return gaps
}

// hasAnyWildcard reports whether a pattern carries a wildcard of ANY form - a
// record's {id} as much as a {rest...} path or a filter segment, everything but
// the {$} anchor. A route with one has to be decided about, out loud, rather
// than skipped for having no {id} segment.
func hasAnyWildcard(pattern string) bool {
	_, path, _ := strings.Cut(pattern, " ")
	for _, seg := range strings.Split(path, "/") {
		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") && seg != "{$}" {
			return true
		}
	}
	return false
}

func (s *Server) buildMux() http.Handler {
	// The JSON API over the live snapshot. It reads the pointer per request, so
	// it is built once and survives every swap.
	s.query = query.NewHandler(s.current, query.HandlerOptions{
		Logger:      s.log,
		RetryAfter:  s.retryWait,
		SiteURL:     s.cfg.SiteURL,
		Now:         s.cfg.now,
		MatchBudget: cmp.Or(s.cfg.matchBudget, query.PublicMatchBudget),
	})
	mux := http.NewServeMux()
	for _, r := range s.routes() {
		mux.Handle(r.pattern, r.handler)
	}
	// The sitemaps are registered unconditionally: they are rendered from the
	// snapshot and the configured origin, with no shell to inject into, so an
	// API-only deployment serves them too (it simply has no static-pages sitemap
	// to list). The index pattern SHADOWS the dist's own sitemap-index.xml, which
	// is the point - see sitemap.go.
	for _, r := range s.sitemapRoutes() {
		mux.Handle(r.pattern, r.handler)
	}
	// The HTML entity pages sit between the API and the static fallback, and only
	// when a site directory is configured: with no dist there is no shell to
	// inject into, and an API-only deployment serves no pages at all.
	if s.site != nil {
		for _, r := range s.htmlRoutes() {
			mux.Handle(r.pattern, r.handler)
		}
		// The static site catches everything the API and the pages did not claim.
		mux.Handle("/", s.site)
	}
	return nosniffMW(mux)
}

// public is the middleware stack every publicly reachable handler wears: CORS,
// because browsers call this API directly, and gzip. One spelling of it, so no
// route can pick up half the stack.
func (s *Server) public(h http.Handler) http.Handler {
	return gzipMW(corsMW(h))
}

// compressed is the stack a crawler DOCUMENT wears (the sitemaps): gzip alone.
// No CORS, as for a page - it is fetched by a crawler, not by a script on
// another origin - and none of a page's document headers either (see
// Server.html): an XML document is never framed or navigated from.
func (s *Server) compressed(h http.HandlerFunc) http.Handler { return gzipMW(h) }

// retryWait is the wait the 503s a server with no artifact advertise as
// Retry-After: the poll loop's CURRENT wait, not the initial bootRetry. After
// several failed attempts the loop has backed off (up to --interval), and
// advertising 30s then would send every client back long before anything can
// have changed. The API's gate reads it through query.HandlerOptions.RetryAfter
// and the sitemaps through retryAfter, so the two cannot advertise different
// waits.
func (s *Server) retryWait() time.Duration { return time.Duration(s.nextRetry.Load()) }

// retryAfter is retryWait as a header value.
func (s *Server) retryAfter() string { return httpx.RetryAfter(s.retryWait()) }

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

// fail answers with the fixed 500 body and logs the error through this server's
// logger (httpx.Fail: the error's own text never reaches the body).
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	httpx.Fail(w, r, s.log, err)
}
