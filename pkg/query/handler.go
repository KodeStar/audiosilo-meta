package query

import (
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/kodestar/audiosilo-meta/internal/httpx"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// HandlerOptions configures NewHandler. The zero value is what an in-process
// consumer wants - a mirror-mode AudioSilo server answering its own lookups - and
// metaserve, whose routes are public, sets the fields that differ.
type HandlerOptions struct {
	// Logger receives the detail of every 500 (whose body is fixed - see
	// httpx.Fail). Nil means log.Default().
	Logger *log.Logger

	// RetryAfter is the wait the 503s advertise (as Retry-After, in whole
	// seconds) while the current func returns nil. Nil means defaultRetryAfter.
	// metaserve passes its poll loop's CURRENT wait, which backs off.
	RetryAfter func() time.Duration

	// SiteURL is the public origin the watch feeds link their items and
	// themselves to, without a trailing slash: the entity pages those links name
	// are metaserve's. Empty means model.SiteURL.
	SiteURL string

	// Now is the watch feeds' clock (their window boundary and day-granular
	// validator). Nil means time.Now; a test pins it for date-boundary cases.
	Now func() time.Time

	// MatchBudget bounds one works/match request, queueing included; past it the
	// request is a 503 with Retry-After: 1. Zero means defaultMatchBudget, a
	// runaway guard an in-process consumer (one server's own admin) never meets in
	// practice. metaserve, whose route is public and CORS-open, passes
	// PublicMatchBudget so a flood cannot price itself.
	MatchBudget time.Duration

	// MatchConcurrency bounds how many works/match requests run at once (the
	// heaviest read). Zero means matchConcurrency.
	MatchConcurrency int
}

// defaultRetryAfter is the Retry-After a handler with no RetryAfter func
// advertises while it has no database: the wait metaserve itself starts from
// (its boot retry) before it backs off.
const defaultRetryAfter = 30 * time.Second

// defaultMatchBudget is HandlerOptions.MatchBudget's zero value. The match is
// bounded by construction (see match.go's header), so this is a ceiling on a
// pathological request rather than a service level.
const defaultMatchBudget = 30 * time.Second

// NewHandler serves metaserve's JSON API - every route Routes lists, with paths,
// status codes, headers and bodies byte-identical to meta.audiosilo.app's - over
// whichever database current returns AT REQUEST TIME, so the caller can swap the
// database underneath it (metaserve's hot swap, a mirror's daily update) without
// rebuilding the handler. Each request loads the pointer once and answers from
// that one database. While current returns nil every route answers 503 with a
// Retry-After (/healthz with {"status":"starting"}).
//
// It carries no CORS and no compression: those are the caller's middleware.
// metaserve wraps its own around every route but /healthz; an in-process
// consumer needs neither.
func NewHandler(current func() *DB, opts HandlerOptions) http.Handler {
	return newHandler(current, opts).serveMux()
}

// newHandler is NewHandler's state with the option defaults filled in, apart
// from the mux, so a test can reach the match slots.
func newHandler(current func() *DB, opts HandlerOptions) *handler {
	if opts.Logger == nil {
		opts.Logger = log.Default()
	}
	if opts.RetryAfter == nil {
		opts.RetryAfter = func() time.Duration { return defaultRetryAfter }
	}
	if opts.SiteURL == "" {
		opts.SiteURL = model.SiteURL
	}
	// Stripped once, here, so every feed URL is one join away from being right.
	opts.SiteURL = strings.TrimRight(opts.SiteURL, "/")
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.MatchBudget <= 0 {
		opts.MatchBudget = defaultMatchBudget
	}
	if opts.MatchConcurrency <= 0 {
		opts.MatchConcurrency = matchConcurrency
	}
	s := &handler{load: current, cfg: opts, log: opts.Logger,
		matchSlots: make(chan struct{}, opts.MatchConcurrency), namespaces: map[string]model.RedirectKind{}}
	for _, r := range s.routes() {
		if r.namespace != "" {
			s.namespaces[r.pattern] = r.namespace
		}
	}
	return s
}

// serveMux registers the route table on a fresh mux.
func (s *handler) serveMux() http.Handler {
	mux := http.NewServeMux()
	for _, r := range s.routes() {
		mux.Handle(r.pattern, r.handler)
	}
	return mux
}

// handler is NewHandler's state: the database source, the options with their
// defaults filled in, and the works/match slots.
type handler struct {
	load func() *DB
	cfg  HandlerOptions
	log  *log.Logger

	// matchSlots bounds how many works/match requests run at once (see
	// match.go): the heaviest read, so a flood queues behind
	// MatchConcurrency slots and answers 503 past its budget.
	matchSlots chan struct{}

	// namespaces is the route table's redirect namespaces keyed by pattern -
	// what redirected looks the matched pattern up in.
	namespaces map[string]model.RedirectKind
}

// current returns the database this request answers from, or nil when none is
// loaded. Handlers call it ONCE and keep the result, so a swap mid-request cannot
// split one answer across two artifacts.
func (s *handler) current() *DB { return s.load() }

// fail answers with the fixed 500 body and logs the error (see httpx.Fail).
func (s *handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	httpx.Fail(w, r, s.log, err)
}

// retryAfter is the Retry-After the no-database 503s carry.
func (s *handler) retryAfter() string { return httpx.RetryAfter(s.cfg.RetryAfter()) }

// ---- routing ----------------------------------------------------------------

// HealthzPattern is the readiness probe's route. It is the one route a public
// deployment serves WITHOUT the API's CORS and compression (metaserve wraps
// every other pattern Routes lists), because it is a probe for a container
// health check or a load balancer rather than an endpoint a browser calls.
const HealthzPattern = "GET /healthz"

// Route is one route NewHandler serves.
type Route struct {
	// Pattern is the http.ServeMux pattern, method included.
	Pattern string
	// Namespace is the tombstone namespace the route's record wildcard resolves a
	// RETIRED slug in (the route answers 301 for one - see redirected), or "" for
	// a route that addresses no record.
	Namespace model.RedirectKind
}

// Routes lists every route NewHandler serves, in registration order. metaserve
// registers each pattern on its own mux in front of the handler (with its CORS
// and gzip around them), and its OpenAPI and redirect drift guards read the
// list, so the API surface is written down once - here.
func Routes() []Route {
	rs := (&handler{}).routes()
	out := make([]Route, len(rs))
	for i, r := range rs {
		out[i] = Route{Pattern: r.pattern, Namespace: r.namespace}
	}
	return out
}

// route is one registration: the pattern, the namespace a retired slug in its
// record wildcard resolves in ("" for none) and the handler behind it.
type route struct {
	pattern   string
	namespace model.RedirectKind
	handler   http.Handler
}

// routes is the API surface, in registration order. It exists as DATA because it
// has several consumers: NewHandler registers it, Routes exports it, and the
// redirect machinery and its guard read the namespaces from it.
func (s *handler) routes() []route {
	return []route{
		{pattern: HealthzPattern, handler: http.HandlerFunc(s.handleHealthz)},
		{pattern: "GET /api/v1/stats", handler: s.gate(s.handleStats)},
		{pattern: "GET /api/v1/search", handler: s.gate(s.searchHandler(kindAny))},
		// Type-scoped searches. A literal segment beats "{id}" in ServeMux's
		// precedence rules, so each coexists with its family's detail route
		// exactly as works/latest already does.
		{pattern: "GET /api/v1/works/search", handler: s.gate(s.searchHandler(kindWork))},
		{pattern: "GET /api/v1/people/search", handler: s.gate(s.searchHandler(kindPerson))},
		{pattern: "GET /api/v1/series/search", handler: s.gate(s.searchHandler(kindSeries))},
		{pattern: "GET /api/v1/works/latest", handler: s.gate(s.handleLatest)},
		// Structured matching of a file against the catalogue (match.go). A
		// literal segment like search and latest, so "match" is a reserved slug.
		{pattern: "GET /api/v1/works/match", handler: s.gate(s.handleMatch)},
		{pattern: "GET /api/v1/watch/feed.atom", handler: s.gate(s.handleWatchAtom)},
		{pattern: "GET /api/v1/watch/feed.json", handler: s.gate(s.handleWatchJSON)},
		{pattern: "GET /api/v1/watch/releases.ics", handler: s.gate(s.handleWatchICS)},
		{pattern: "GET /api/v1/works/{id}", namespace: model.RedirectWorks, handler: s.gate(s.handleWork)},
		{pattern: "GET /api/v1/works/{id}/recordings/{rid}/chapters", namespace: model.RedirectWorks, handler: s.gate(s.handleChapters)},
		{pattern: "GET /api/v1/people/{id}", namespace: model.RedirectPeople, handler: s.gate(s.handlePerson)},
		{pattern: "GET /api/v1/series/{id}", namespace: model.RedirectSeries, handler: s.gate(s.handleSeries)},
		{pattern: "GET /api/v1/lookup", handler: s.gate(s.handleLookup)},
		{pattern: "GET /api/v1/coverage", handler: s.gate(s.handleCoverage)},
		{pattern: "GET /api/v1/coverage/works", handler: s.gate(s.handleCoverageWorks)},
		{pattern: "GET /api/v1/coverage/series-gaps", handler: s.gate(s.handleCoverageSeriesGaps)},
		// Audiobookshelf custom metadata provider (ABS appends /search to the
		// configured base URL). Outside /api/v1.
		{pattern: "GET /abs/search", handler: s.gate(s.handleABSSearch)},
		// The same provider with a language preference: configured as /abs/de, ABS
		// calls /abs/de/search. It ranks rather than filters - see abs.go.
		{pattern: "GET /abs/{" + absLangWildcard + "}/search", handler: s.gate(s.handleABSLangSearch)},
	}
}

// redirectExemptRoutes are this table's wildcard routes that deliberately
// resolve no retired slug: the Audiobookshelf provider's language segment is a
// FILTER (de, or de,en), not a record - there is nothing it could have been
// retired from. TestEveryRecordRouteNamesANamespace requires every other
// wildcard route to name a namespace.
var redirectExemptRoutes = map[string]bool{
	"GET /abs/{" + absLangWildcard + "}/search": true,
}

// idWildcard is what the four current detail routes spell their record wildcard.
// Their handlers read it directly; the redirect machinery does NOT - it derives
// the name from the pattern (see idWildcardOf), so nothing depends on the
// spelling being this one.
const idWildcard = "id"

// idWildcardOf returns the name of the wildcard a pattern addresses its record
// by: the FIRST single-segment wildcard in the path. It is derived rather than
// assumed so a route spelling it {work} or {pid} still redirects, and so the
// coverage guard does not rest on a naming convention nothing enforces.
//
// "" means the pattern has no such wildcard. ServeMux's two other forms are
// deliberately not it: {$} is an anchor rather than a value, and {rest...} spans
// segments, so neither can name one record.
func idWildcardOf(pattern string) string {
	_, path, _ := strings.Cut(pattern, " ")
	for _, seg := range strings.Split(path, "/") {
		if name, ok := wildcardName(seg); ok {
			return name
		}
	}
	return ""
}

// wildcardName reads one pattern segment as a single-segment wildcard.
func wildcardName(seg string) (string, bool) {
	if !strings.HasPrefix(seg, "{") || !strings.HasSuffix(seg, "}") {
		return "", false
	}
	name := seg[1 : len(seg)-1]
	if name == "$" || strings.HasSuffix(name, "...") {
		return "", false
	}
	return name, true
}

// gate answers 503 while there is no database, so every data handler can assume
// s.current() is non-nil. For metaserve that is a poll-only boot that could not
// reach GitHub (temporary by construction, hence Retry-After); for an
// in-process consumer it is a copy that has not loaded.
func (s *handler) gate(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.current() == nil {
			w.Header().Set("Retry-After", s.retryAfter())
			httpx.WriteErr(w, http.StatusServiceUnavailable, httpx.NoArtifactMsg)
			return
		}
		next(w, r)
	}
}

// clampLimit parses the ?limit= param and clamps it to [1, max], defaulting to
// def when absent or invalid. A def of 0 is how an endpoint spells "no window at
// all by default" (DB.series), since 0 is never reachable from a supplied
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
func (s *handler) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	snap := s.current()
	if snap == nil {
		w.Header().Set("Retry-After", s.retryAfter())
		httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "starting"})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"status":   "ok",
		"built_at": snap.stats.BuiltAt,
		"works":    snap.stats.Works,
	})
}

func (s *handler) handleStats(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, s.current().stats)
}

// langParam reads a request's language filter from its already-parsed query
// values, as snap can apply it to kind (DB.langFilterFor - the one version
// gate), answering the 400 itself when an item is not a language tag. Repeated
// parameters are one list, so `lang=de&lang=en` reads as `lang=de,en`. Every
// handler a filter narrows reads it here, so the spelling of the parameter and
// its error have one home; the handler must then query snap itself, the snapshot
// the gate was asked of.
func langParam(w http.ResponseWriter, snap *DB, q url.Values, kind searchKind) (langFilter, bool) {
	f, err := snap.langFilterFor(strings.Join(q["lang"], ","), kind)
	if err != nil {
		httpx.WriteErr(w, http.StatusBadRequest, err.Error())
		return nil, false
	}
	return f, true
}

func (s *handler) handleLatest(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := clampLimit(q.Get("limit"), 12, 50)
	snap := s.current()
	lang, ok := langParam(w, snap, q, kindWork)
	if !ok {
		return
	}
	cards, err := snap.latestWorks(limit, lang)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"works": cards})
}

func (s *handler) handleWork(w http.ResponseWriter, r *http.Request) {
	snap := s.current()
	detail, err := snap.WorkDetail(r.PathValue(idWildcard))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if detail == nil {
		if s.redirected(w, r, snap) {
			return
		}
		httpx.WriteErr(w, http.StatusNotFound, "work not found")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, detail)
}

// handleChapters answers an unknown (work, recording) pair with an empty list
// rather than a 404 - a recording legitimately has no chapters - so the retired
// slug is consulted when the list comes back EMPTY. It can only ever hit on a
// slug the catalogue does not hold: a live work is never a redirect source
// (pkg/check's checkRedirects), so a real recording with no chapters still gets
// its empty list.
func (s *handler) handleChapters(w http.ResponseWriter, r *http.Request) {
	snap := s.current()
	chs, err := snap.chapters(r.PathValue(idWildcard), r.PathValue("rid"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if len(chs) == 0 && s.redirected(w, r, snap) {
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"chapters": chs})
}

// redirected answers a request whose {id} names a RETIRED slug with a 301 at the
// same route under the slug that replaced it, and reports whether it did.
//
// Every id route calls it where it would otherwise 404, so a slug a merge retired
// keeps resolving (see model.Redirects for why that matters). The BODY carries the
// new slug too, so a client that does not follow redirects can heal what it stored
// instead of only learning that the id is gone.
//
// The namespace comes from the route table keyed by the pattern that matched;
// everything else is DB.RedirectRetired's, which metaserve's entity pages share
// with a body of their own.
func (s *handler) redirected(w http.ResponseWriter, r *http.Request, snap *DB) bool {
	kind, named := s.namespaces[r.Pattern]
	if !named {
		return false // not a route a retired slug can arrive at
	}
	return snap.RedirectRetired(w, r, kind, func(w http.ResponseWriter, _, to string) {
		httpx.WriteJSON(w, http.StatusMovedPermanently, map[string]string{"redirect": to})
	})
}

// RedirectRetired answers a request whose record wildcard names a slug this
// artifact RETIRED (in namespace kind) with a 301 at the same route under the
// slug that replaced it, and reports whether it did. writeBody writes the 301's
// body (it must call WriteHeader(http.StatusMovedPermanently)); the API's is
// {"redirect": to}, metaserve's entity pages write a minimal HTML page linking
// the Location. The HEADERS are set here, so the two writers cannot drift on the
// part that matters.
//
// It is composed entirely from ROUTE DATA rather than per-route arguments: the
// record wildcard is the first single-segment wildcard of the pattern that
// matched (r.Pattern), and the Location is that same pattern with its wildcards
// filled in - the new slug for the record, the request's own values for the
// rest - so no route can be handed a Location belonging to another one, and a
// route that gains a wildcard needs no change here. The request's query string
// travels along, because a ?limit/?offset window describes the request rather
// than the id.
//
// The caller passes the database it already answered from rather than loading
// the pointer again, so a hot-swap mid-request cannot make the 404 and the
// redirect decision come from two different artifacts. A lookup FAILURE degrades
// to "no redirect": the caller then answers its own 404, which is what the
// request looked like anyway, rather than turning a missing record into a 500.
func (d *DB) RedirectRetired(w http.ResponseWriter, r *http.Request, kind model.RedirectKind,
	writeBody func(w http.ResponseWriter, location, to string)) bool {
	location, to, ok := d.resolveRedirect(r, kind)
	if !ok {
		return false
	}
	w.Header().Set("Location", location)
	// A tombstone is not permanent the way 301 invites a client to assume: a bad
	// merge can be reversed, and the redirect then has to stop being served. An
	// hour is long enough to spare the origin a stale client's repeated misses and
	// short enough that reversing one is not a support problem.
	w.Header().Set("Cache-Control", RedirectMaxAge)
	writeBody(w, location, to)
	return true
}

// resolveRedirect is the decision half of RedirectRetired: it reports the
// Location to send and the slug that replaced the requested one, or ok=false
// when this request is not a retired slug.
func (d *DB) resolveRedirect(r *http.Request, kind model.RedirectKind) (location, to string, ok bool) {
	idName := idWildcardOf(r.Pattern)
	if idName == "" {
		return "", "", false // the pattern addresses no record
	}
	id := r.PathValue(idName)
	to, err := d.redirectTarget(kind, id)
	if err != nil {
		d.logf("serve: redirect lookup for %s %q failed: %v", kind, id, err)
		return "", "", false
	}
	if to == "" {
		return "", "", false
	}
	// A row pointing a slug at ITSELF would send a following client back here
	// forever. pkg/check refuses one, so no sanctioned artifact carries it, but
	// "cannot loop" should be true of the resolver rather than only of the data it
	// is usually given - a hand-built or corrupted artifact must degrade to the
	// 404 the request was already heading for.
	if to == id {
		d.logf("serve: ignoring self-redirect for %s %q (the artifact's redirect table is corrupt)", kind, id)
		return "", "", false
	}
	return redirectLocation(r, idName, to), to, true
}

// RedirectMaxAge is the Cache-Control every retired-slug 301 carries. See
// RedirectRetired.
const RedirectMaxAge = "public, max-age=3600"

// redirectLocation rebuilds the matched route's path with to standing in for the
// record wildcard idName, every other wildcard keeping the value it matched, and
// the request's own query string appended.
//
// It returns the WIRE form and is used as the header value directly, escaping each
// segment exactly once. Handing it to url.URL would escape it a second time (Path
// is the decoded form, so a % becomes %25), and setting Path alone would not
// escape at all - url.URL leaves a "/" inside a path segment untouched. The ids in
// the artifact are plain slugs today, so either mistake is latent rather than
// visible, which is precisely why it is written down here.
func redirectLocation(r *http.Request, idName, to string) string {
	_, pattern, _ := strings.Cut(r.Pattern, " ")
	var b strings.Builder
	for _, seg := range strings.Split(strings.TrimPrefix(pattern, "/"), "/") {
		b.WriteByte('/')
		name, wild := wildcardName(seg)
		if !wild {
			b.WriteString(seg)
			continue
		}
		value := to
		if name != idName {
			value = r.PathValue(name)
		}
		b.WriteString(url.PathEscape(value))
	}
	if r.URL.RawQuery != "" {
		b.WriteByte('?')
		b.WriteString(r.URL.RawQuery)
	}
	return b.String()
}

// personPageDefault / PersonPageMax bound one page of a person's credit lists.
// The default is a page, not the whole list: a corporate credit ("Full Cast")
// already narrates ~2,000 works, and that count grows with the catalogue. The
// unpaged totals travel in the response so a client always knows what it is
// missing.
const (
	personPageDefault = 100
	PersonPageMax     = 500
)

func (s *handler) handlePerson(w http.ResponseWriter, r *http.Request) {
	snap := s.current()
	q := r.URL.Query()
	limit := clampLimit(q.Get("limit"), personPageDefault, PersonPageMax)
	p, err := snap.Person(r.PathValue(idWildcard), limit, clampOffset(q.Get("offset")))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if p == nil {
		if s.redirected(w, r, snap) {
			return
		}
		httpx.WriteErr(w, http.StatusNotFound, "person not found")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, p)
}

// seriesPageMax bounds an explicitly requested series page. There is no
// default: ?limit absent means the whole series (see DB.series - the
// player's series rail is composed from the full list).
const seriesPageMax = 500

func (s *handler) handleSeries(w http.ResponseWriter, r *http.Request) {
	snap := s.current()
	q := r.URL.Query()
	ser, err := snap.Series(r.PathValue(idWildcard), clampLimit(q.Get("limit"), 0, seriesPageMax), clampOffset(q.Get("offset")))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if ser == nil {
		if s.redirected(w, r, snap) {
			return
		}
		httpx.WriteErr(w, http.StatusNotFound, "series not found")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, ser)
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
func (s *handler) searchHandler(kind searchKind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		params := r.URL.Query()
		q := strings.TrimSpace(params.Get("q"))
		if q == "" {
			httpx.WriteErr(w, http.StatusBadRequest, "q is required")
			return
		}
		limit := clampLimit(params.Get("limit"), searchPageDefault, searchPageMax)
		// Validated on every scope, people/search included: the parameter means one
		// thing on all four, and the people scope ignoring it (a person has no
		// language) is the gate's decision, not a reason to accept garbage.
		snap := s.current()
		lang, ok := langParam(w, snap, params, kind)
		if !ok {
			return
		}
		results, err := snap.search(r.Context(), kind, q, limit, lang)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"results": results})
	}
}

// handleCoverage reports the top-line expressive-layer totals
// (characters/recaps/recap summaries). The per-work list and series gaps are
// their own paginated endpoints. It always returns 200 and degrades on older
// artifacts (see DB.coverage) rather than reporting everything as missing.
func (s *handler) handleCoverage(w http.ResponseWriter, r *http.Request) {
	res, err := s.current().coverage()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

// handleCoverageWorks serves one filtered, searchable, paginated page of works
// for the contribute-page coverage browser. ?filter selects the dimension
// (missing|has_characters|has_recaps|has_recap_summary), ?q is a full-text query
// over title/authors/narrators/series (word prefixes), ?lang narrows by language,
// ?limit/?offset paginate. It always returns 200 and degrades to an
// empty page with available:false when the filter's dimension is unevaluable at
// the current artifact schema_version (see DB.coverageWorks).
func (s *handler) handleCoverageWorks(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filter, ok := validCoverageFilter(q.Get("filter"))
	if !ok {
		httpx.WriteErr(w, http.StatusBadRequest, "unknown filter")
		return
	}
	snap := s.current()
	lang, ok := langParam(w, snap, q, kindWork)
	if !ok {
		return
	}
	limit := clampLimit(q.Get("limit"), 25, 100)
	offset := clampOffset(q.Get("offset"))
	res, err := snap.coverageWorks(filter, strings.TrimSpace(q.Get("q")), limit, offset, lang)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

// handleCoverageSeriesGaps serves one searchable, paginated page of series with
// interior position gaps. ?q is a series-name substring; ?limit/?offset
// paginate. series_gaps has no schema_version dependency, so it is always
// available.
func (s *handler) handleCoverageSeriesGaps(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := clampLimit(q.Get("limit"), 25, 100)
	offset := clampOffset(q.Get("offset"))
	res, err := s.current().seriesGapsPage(strings.TrimSpace(q.Get("q")), limit, offset)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

func (s *handler) handleLookup(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	asin := strings.TrimSpace(q.Get("asin"))
	isbn := strings.TrimSpace(q.Get("isbn"))
	if asin == "" && isbn == "" {
		httpx.WriteErr(w, http.StatusBadRequest, "asin or isbn is required")
		return
	}
	snap := s.current()
	res, err := snap.lookup(r.Context(), asin, isbn)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if res == nil {
		httpx.WriteErr(w, http.StatusNotFound, "not found")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}
