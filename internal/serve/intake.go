package serve

// intake.go is the unknown-ASIN intake queue: a server that opted in names the
// Audible ASINs the catalogue does not hold, and the sync bot reads them back as a
// list of listings worth looking up. It is the one place metaserve takes anything
// from an anonymous caller, so what it keeps is as small as the job allows
// (intake_queue.go has the schema) and it is off unless a deployment configures it.
//
// The rules a change here must keep:
//
//   - Off means off. With Config.IntakeDB empty, or a file that will not open,
//     neither route is registered, the spec does not name them, no file is created
//     and no goroutine runs. The kill switch is unsetting the variable.
//   - Its own middleware, never Server.public. That stack is CORS-open, and a write
//     route must not be callable from a script on someone else's page: requiring
//     application/json forces a browser's preflight, and with no OPTIONS route and
//     no CORS header the preflight fails. Everything answers no-store.
//   - A client's address reaches the limiter and stops there. The limiter holds it
//     in memory (the bucket's key, an IPv6 address as its /64), and intakeLoop's
//     sweep drops it about a day after the client's last request whether or not
//     anybody else ever calls. Nothing on this path logs an address, a user agent,
//     an ASIN or a batch, and nothing of the kind goes in the file, whose layout
//     forgets the arrival order on the same tick (intakeQueue.compact).
//   - Every snapshot this server serves filters the queue: a row leaves when the
//     artifact knows it, or when it expires. There is no ack route. The housekeeping
//     goroutine notices a new snapshot on its tick, however it was installed, and
//     purges outside s.mu, so a slow or failing queue never holds up a swap.

import (
	"cmp"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"mime"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/kodestar/audiosilo-meta/internal/httpx"
	"github.com/kodestar/audiosilo-meta/pkg/query"
)

const (
	intakePath = "/api/v1/intake/unknown-asins"

	// maxIntakeBodyBytes and maxIntakeItems bound one report. 200 ASINs at their
	// 13 bytes each fit well inside the body limit.
	maxIntakeBodyBytes = 8 << 10
	maxIntakeItems     = 200
	// maxIntakeRows is the queue's row cap (intakeQueue.record has the eviction).
	maxIntakeRows = 20000
	// intakeExpiryDays is how long a row outlives its last report.
	intakeExpiryDays = 90

	// intakeReportsPerDay is the POST limiter's rate and burst: six reports at once,
	// then one every four hours as the bucket refills.
	intakeReportsPerDay = 6
	// intakeRefusal is its 429 body, saying what the limit is in the limit's units.
	intakeRefusal = "rate limit exceeded: at most 6 reports a day per client; retry after the Retry-After seconds"
	// maxIntakeClients bounds the POST limiter's map, about 12 MiB of buckets. They
	// live a day where the global limiter's live seconds, so a sender rotating
	// through a routed IPv6 prefix could otherwise grow it until the process, and
	// the catalogue API with it, runs out of memory.
	maxIntakeClients = 100_000

	minIntakeTokenBytes = 32

	// intakePurgeTimeout bounds one purge: it reads the snapshot it pinned, and past
	// the swap grace that snapshot may be closed anyway.
	intakePurgeTimeout = time.Minute
	// intakeCloseTimeout bounds the last compact. One that does not finish is the
	// next boot's to do.
	intakeCloseTimeout = 2 * time.Second
)

// intakeASIN is the ASIN's format, the catalogue's own (schema/common.schema.json's
// asin, which TestIntakeASINIsTheSchemasPattern pins this to, and the importer's
// asinPattern). It is deliberately not normalized: a lower-case spelling is an
// invalid item, so one ASIN has one row.
var intakeASIN = regexp.MustCompile(`^[A-Z0-9]{10}$`)

// intakeState is the queue and what runs around it, present only while the routes
// are registered.
type intakeState struct {
	queue *intakeQueue
	// limiter is the POST route's own bucket set. The API's global limiter is
	// sized for a bulk match and would not notice this route's abuse.
	limiter *rateLimiter
	// token is the read route's bearer token; empty leaves that route unregistered.
	token string
	// stop ends intakeLoop, which closes done on its way out.
	stop func()
	done chan struct{}
	once sync.Once
}

// openIntake brings the queue up when the deployment configured one. A failure
// here is logged and leaves it off: the catalogue API must not depend on a
// disposable side queue.
func (s *Server) openIntake() {
	// Trimmed as a request's token is, so a stray newline in the deployment's value
	// is not a route that refuses every caller.
	path, token := s.cfg.IntakeDB, strings.TrimSpace(s.cfg.IntakeToken)
	if path == "" {
		if token != "" {
			s.log.Printf("serve: METASERVE_INTAKE_TOKEN is set without METASERVE_INTAKE_DB; the intake routes stay off")
		}
		return
	}
	q, err := openIntakeQueue(path)
	if err != nil {
		s.log.Printf("serve: intake queue unavailable, the intake routes stay off: %v", err)
		return
	}
	if token != "" && len(token) < minIntakeTokenBytes {
		s.log.Printf("serve: METASERVE_INTAKE_TOKEN is shorter than %d bytes; the intake read route stays off", minIntakeTokenBytes)
		token = ""
	}
	limiter := newRateLimiter(intakeReportsPerDay/(24*time.Hour).Seconds(), intakeReportsPerDay, s.cfg.TrustedProxies, s.cfg.now)
	limiter.refusal = intakeRefusal
	limiter.maxBuckets = maxIntakeClients
	ctx, cancel := context.WithCancel(context.Background())
	s.intake = &intakeState{
		queue: q, limiter: limiter, token: token,
		stop: cancel, done: make(chan struct{}),
	}
	go s.intakeLoop(ctx)
	s.log.Printf("serve: intake queue on at %s", path)
}

// closeIntake stops the housekeeping, compacts what the last tick did not reach and
// closes the queue. It is a no-op with the queue off, and safe to call twice.
func (s *Server) closeIntake() {
	in := s.intake
	if in == nil {
		return
	}
	in.once.Do(func() {
		in.stop()
		<-in.done
		ctx, cancel := context.WithTimeout(context.Background(), intakeCloseTimeout)
		defer cancel()
		s.compactIntake(ctx)
		if err := in.queue.close(); err != nil {
			s.log.Printf("serve: closing the intake queue: %v", err)
		}
	})
}

// intakeLoop is the queue's housekeeping, for as long as the server lives (Run ends
// it through closeIntake). The limiter's sweep is the point of the ticker: allow
// only sweeps when a request arrives, so without this the last client before a
// quiet spell would stay in memory until the next one, and the promise is about a
// day. The tick is sweepEvery, so the address is gone within a minute or two of
// its full bucket's 24 hours.
func (s *Server) intakeLoop(ctx context.Context) {
	defer close(s.intake.done)
	tick := time.NewTicker(cmp.Or(s.cfg.intakeTick, sweepEvery))
	defer tick.Stop()
	expiredOn := s.expireIntakeRows(ctx, "")
	purged := s.purgeKnownIntakeRows(ctx, nil)
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			s.intake.limiter.sweepIdle()
			expiredOn = s.expireIntakeRows(ctx, expiredOn)
			purged = s.purgeKnownIntakeRows(ctx, purged)
			s.compactIntake(ctx)
		}
	}
}

// compactIntake runs intakeQueue.compact. A failure is logged and the next call
// tries again.
func (s *Server) compactIntake(ctx context.Context) {
	if err := s.intake.queue.compact(ctx); err != nil && ctx.Err() == nil {
		s.log.Printf("serve: intake queue: %v", err)
	}
}

// expireIntakeRows deletes the rows past their expiry, once a UTC day, and returns
// the day it last ran. A failure is logged and waits for the next day.
func (s *Server) expireIntakeRows(ctx context.Context, expiredOn string) string {
	now := s.cfg.now().UTC()
	today := now.Format(time.DateOnly)
	if today == expiredOn {
		return expiredOn
	}
	cutoff := now.AddDate(0, 0, -intakeExpiryDays).Format(time.DateOnly)
	if err := s.intake.queue.expire(ctx, cutoff); err != nil && ctx.Err() == nil {
		s.log.Printf("serve: intake queue: %v", err)
	}
	return today
}

// purgeKnownIntakeRows deletes the rows the live snapshot knows, once per snapshot,
// and returns the snapshot it last read. Comparing the pointer is what reaches every
// way of installing one, and several swaps between two ticks are one purge against
// the newest. The snapshot is loaded once and used throughout, as a request does: a
// swap meanwhile leaves this one open for its grace, and the next tick takes its
// successor. A failure is only logged. The rows stay, and the next adopt tries again.
func (s *Server) purgeKnownIntakeRows(ctx context.Context, purged *query.DB) *query.DB {
	snap := s.current()
	if snap == nil || snap == purged {
		return purged
	}
	ctx, cancel := context.WithTimeout(ctx, intakePurgeTimeout)
	defer cancel()
	queue := s.intake.queue
	err := func() error {
		asins, err := queue.asins(ctx)
		if err != nil {
			return err
		}
		known, err := snap.KnownASINs(ctx, asins)
		if err != nil || len(known) == 0 {
			return err
		}
		if err := queue.remove(ctx, slices.Collect(maps.Keys(known))); err != nil {
			return err
		}
		s.log.Printf("serve: intake queue: %d rows are now known to the catalogue and were dropped", len(known))
		return nil
	}()
	// A cancelled context is shutdown, not a failure. A purge past its time bound is
	// logged like any other.
	if err != nil && !errors.Is(context.Cause(ctx), context.Canceled) {
		s.log.Printf("serve: intake queue purge failed: %v", err)
	}
	return snap
}

// intakeRoutes is the intake surface, empty when the deployment does not offer it.
// The POST wears only its own limiter and not the global one, whose buckets are
// not on the intake limiter's sweep: a report's address has one place to be. The
// read route is the token holder's, so it has the same limit as the rest of the
// API instead, which also bounds refused guesses.
func (s *Server) intakeRoutes() []route {
	if s.intake == nil {
		return nil
	}
	rs := []route{{"POST " + intakePath, noStoreMW(s.intake.limiter.middleware(http.HandlerFunc(s.handleIntakeReport)))}}
	if s.intake.token != "" {
		var read http.Handler = http.HandlerFunc(s.handleIntakeRead)
		if s.limiter != nil {
			read = s.limiter.middleware(read)
		}
		rs = append(rs, route{"GET " + intakePath, noStoreMW(read)})
	}
	return rs
}

// unofferedSpecMembers names what this deployment's OpenAPI document leaves out of
// the embedded file (see servedSpec).
func (s *Server) unofferedSpecMembers() [][]string {
	schema := func(name string) []string { return []string{"components", "schemas", name} }
	read := [][]string{{"components", "securitySchemes"}, schema("IntakeQueue"), schema("IntakeRow")}
	switch {
	case s.intake == nil:
		return append(read, []string{"paths", intakePath}, schema("IntakeReport"), schema("IntakeAccepted"))
	case s.intake.token == "":
		return append(read, []string{"paths", intakePath, "get"})
	}
	return nil
}

// noStoreMW keeps every answer out of every cache: neither a report's outcome nor
// the queue is anybody else's to be served.
func noStoreMW(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// handleIntakeReport is POST /api/v1/intake/unknown-asins. The limiter has already
// spent a token on it, so a refused report counts toward the six as well.
func (s *Server) handleIntakeReport(w http.ResponseWriter, r *http.Request) {
	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
		httpx.WriteErr(w, http.StatusUnsupportedMediaType, "the body must be application/json")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxIntakeBodyBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			httpx.WriteErr(w, http.StatusRequestEntityTooLarge, "the body is over the limit of 8 KiB")
			return
		}
		httpx.WriteErr(w, http.StatusBadRequest, "the body could not be read")
		return
	}
	items, err := parseIntakeBody(body)
	if err != nil {
		httpx.WriteErr(w, http.StatusBadRequest, "the body must be a JSON object with an array member asins")
		return
	}
	if len(items) > maxIntakeItems {
		httpx.WriteErr(w, http.StatusBadRequest, "asins holds more than 200 items")
		return
	}
	valid, invalid := sortIntakeItems(items)

	// One load, as a request on the API makes: a swap during this request leaves
	// this snapshot open for its grace. With no snapshot yet nothing can be called
	// known, so the report is taken and the first adopt filters it.
	var known map[string]bool
	if snap := s.current(); snap != nil {
		if known, err = snap.KnownASINs(r.Context(), valid); err != nil {
			s.failIntake(w, r, err)
			return
		}
	}
	unknown := slices.DeleteFunc(valid, func(a string) bool { return known[a] })
	if err := s.intake.queue.record(r.Context(), unknown, s.cfg.now().UTC().Format(time.DateOnly)); err != nil {
		s.failIntake(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, map[string]int{
		"accepted": len(unknown), "known": len(known), "invalid": invalid,
	})
}

// parseIntakeBody reads the items of a report's asins array, each still raw. Any
// other member is ignored and goes no further. A missing asins, a null one and
// anything that is not an array are the same error; [] is an empty report.
func parseIntakeBody(body []byte) ([]json.RawMessage, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, err
	}
	var items []json.RawMessage
	if err := json.Unmarshal(doc["asins"], &items); err != nil {
		return nil, err
	}
	if items == nil {
		return nil, errors.New("asins is null")
	}
	return items, nil
}

// sortIntakeItems splits a report's items into its distinct valid ASINs and the
// count of the rest. An item that is not a string (null included) or not an ASIN
// is invalid, once per occurrence; an ASIN named twice is one ASIN.
func sortIntakeItems(items []json.RawMessage) (valid []string, invalid int) {
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		var asin string
		if err := json.Unmarshal(item, &asin); err != nil || !intakeASIN.MatchString(asin) {
			invalid++
			continue
		}
		if !seen[asin] {
			seen[asin] = true
			valid = append(valid, asin)
		}
	}
	return valid, invalid
}

// failIntake answers a storage failure. The error goes to the log, whose text never
// holds a bound value, so no ASIN does; a client that hung up gets no log line.
func (s *Server) failIntake(w http.ResponseWriter, r *http.Request, err error) {
	if r.Context().Err() == nil {
		s.log.Printf("serve: intake queue: %v", err)
	}
	httpx.WriteErr(w, http.StatusServiceUnavailable, "the intake queue is unavailable; nothing was recorded")
}

// handleIntakeRead is GET /api/v1/intake/unknown-asins, the whole queue for the
// token holder. The rows are read before the first byte is written, so a slow
// reader never holds the queue's one connection.
func (s *Server) handleIntakeRead(w http.ResponseWriter, r *http.Request) {
	if !s.intakeTokenMatches(r.Header.Get("Authorization")) {
		w.Header().Set("WWW-Authenticate", "Bearer")
		httpx.WriteErr(w, http.StatusUnauthorized, "a valid bearer token is required")
		return
	}
	rows, err := s.intake.queue.list(r.Context())
	if err != nil {
		s.failIntake(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, struct {
		Total int         `json:"total"`
		ASINs []intakeRow `json:"asins"`
	}{len(rows), rows})
}

// intakeTokenMatches compares an Authorization header against the token in constant
// time. Both sides are hashed first, because the comparison would otherwise answer
// early on a difference in length.
func (s *Server) intakeTokenMatches(header string) bool {
	scheme, token, ok := strings.Cut(header, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return false
	}
	got := sha256.Sum256([]byte(strings.TrimSpace(token)))
	want := sha256.Sum256([]byte(s.intake.token))
	return subtle.ConstantTimeCompare(got[:], want[:]) == 1
}
