package serve

import (
	"fmt"
	"math"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/kodestar/audiosilo-meta/internal/httpx"
)

// ratelimit.go is metaserve's per-client token bucket (LIBRIVOX-PLAN.md decision
// 7): the API is public, unauthenticated and CORS-open, and the LibriVox-shaped
// feed invites apps that used to load a volunteer server onto this one, so one
// client gets Config.RateLimit requests a second with Config.RateBurst of slack
// and is told to wait (429 + Retry-After) past that. It wears the API stack
// (Server.public), so /healthz - a probe, not a browser's call - is exempt.
//
// A CLIENT is its IP address: the connection's remote address, or - only when
// that address is one of Config.TrustedProxies - the rightmost X-Forwarded-For
// entry that is not itself a trusted proxy. A forwarded header from anywhere
// else is a value the client chose, so it is never read: trusting it would let
// every request name a fresh address and never be limited. An IPv6 client's
// bucket is its /64 (bucketKey): one subscriber is routinely handed a whole /64,
// so keying the full address would hand every one of its addresses a fresh
// bucket - the same "name a fresh address" escape, by a different route.

// Defaults for metaserve's flags (cmd/metaserve), sized so that a shipped
// AudioSilo server never trips them. Its community client (audiosilo-server
// internal/meta) is the heaviest legitimate caller, does not pace itself and
// treats a 429 as a hard error (five in a row stop a bulk match), so the limit
// must sit above what it does at full speed:
//
//   - In flight at once, at most about a dozen: a bulk match's 2 workers each
//     with one works/match, work and series reads sharing a bound of 4
//     (maxConcurrentWorkFetches), the console's identifier lookups another 4
//     (maxConcurrentLookups), and a player's one /meta per opened book.
//   - Sustained, a bulk match is the long one: per book one works/match, then
//     its best 2 works together, then their series facts - about 4 requests in
//     3 round trips per worker. With a round trip (network plus answer) as
//     short as 30 ms that is 2 x 4 / 0.09 s, about 90 requests a second, for
//     as long as the library lasts; at a more usual 60 ms+ it is under 50.
//   - Bursty, a console Series page asks about every matched book of a few
//     dozen cards at once, a few hundred lookups 4 at a time.
//
// So 100 a second holds the bulk match's fast case with headroom, and a burst
// of 400 absorbs a Series page or a match run starting while a page loads. A
// scraper looping flat out on one address wants far more than that; past it
// the catalogue's bulk route is the release artifact. A server sharing the
// host or LAN with a private metaserve (round trips of a millisecond or two)
// can outrun any sane limit: that deployment turns it off (--rate-limit 0).
const (
	DefaultRateLimit = 100
	DefaultRateBurst = 400
)

// maxRefillSeconds is the longest refill time (burst/rate) a time.Duration holds.
const maxRefillSeconds = float64(math.MaxInt64) / float64(time.Second)

// validateRateLimit is Config's rule for the limit (see Config.RateLimit): 0
// turns it off, whatever the burst; a limit needs a finite positive rate and a
// burst of at least 1. A burst of 0 is refused, not defaulted: it reads as "no
// slack" and would otherwise quietly become some other number.
func validateRateLimit(rate float64, burst int) error {
	switch {
	case rate == 0:
		return nil
	case math.IsNaN(rate) || math.IsInf(rate, 0) || rate < 0:
		return fmt.Errorf("serve: rate limit %g: want a positive number of requests a second, or 0 to turn the limit off", rate)
	case burst < 1:
		return fmt.Errorf("serve: rate burst %d: want at least 1 while the rate limit is on (rate 0 turns it off)", burst)
	case float64(burst)/rate >= maxRefillSeconds:
		// The sweep's refill time (burst/rate) and Retry-After are Durations; past
		// this they overflow, and a wrapped refill time sweeps every bucket.
		return fmt.Errorf("serve: rate limit %g with burst %d: a bucket would take longer than %g seconds to refill", rate, burst, maxRefillSeconds)
	}
	return nil
}

// rateLimiter is a set of token buckets keyed by client address.
type rateLimiter struct {
	rate    float64 // tokens per second
	burst   float64
	trusted []netip.Prefix
	now     func() time.Time

	mu        sync.Mutex
	buckets   map[netip.Addr]*bucket
	lastSweep time.Time
}

// bucket is one client's tokens as of last.
type bucket struct {
	tokens float64
	last   time.Time
}

// sweepEvery is how often idle buckets are dropped. A bucket idle for longer
// than it takes to refill is indistinguishable from a fresh one, so dropping it
// changes no answer and keeps the map to the clients of the last minutes.
const sweepEvery = time.Minute

// newRateLimiter wants the rate and burst New has validated (validateRateLimit).
func newRateLimiter(rate float64, burst int, trusted []netip.Prefix, now func() time.Time) *rateLimiter {
	if now == nil {
		now = time.Now
	}
	return &rateLimiter{
		rate: rate, burst: float64(burst), trusted: trusted, now: now,
		buckets: map[netip.Addr]*bucket{}, lastSweep: now(),
	}
}

// allow takes one token from addr's bucket, or reports how long until one is
// available.
func (l *rateLimiter) allow(addr netip.Addr) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.sweep(now)
	b, ok := l.buckets[addr]
	if !ok {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[addr] = b
	}
	if elapsed := now.Sub(b.last).Seconds(); elapsed > 0 {
		b.tokens = min(l.burst, b.tokens+elapsed*l.rate)
	}
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	return false, time.Duration((1 - b.tokens) / l.rate * float64(time.Second))
}

// sweep drops the buckets that have refilled completely. Called under mu.
func (l *rateLimiter) sweep(now time.Time) {
	if now.Sub(l.lastSweep) < sweepEvery {
		return
	}
	l.lastSweep = now
	full := time.Duration(l.burst / l.rate * float64(time.Second))
	for addr, b := range l.buckets {
		if now.Sub(b.last) > full {
			delete(l.buckets, addr)
		}
	}
}

// isTrusted reports whether addr is one of the configured proxies.
func (l *rateLimiter) isTrusted(addr netip.Addr) bool {
	for _, p := range l.trusted {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// clientAddr is the request's client as the limiter keys it (see the file
// comment). ok is false only for a remote address that does not parse, which
// net/http never produces for a real connection.
func (l *rateLimiter) clientAddr(r *http.Request) (netip.Addr, bool) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	remote, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, false
	}
	remote = remote.Unmap()
	if !l.isTrusted(remote) {
		return remote, true
	}
	// Every X-Forwarded-For header, in order, as one list: the proxies append,
	// so the client is the rightmost entry no trusted proxy wrote.
	var hops []string
	for _, v := range r.Header.Values("X-Forwarded-For") {
		hops = append(hops, strings.Split(v, ",")...)
	}
	for i := len(hops) - 1; i >= 0; i-- {
		hop, err := parseHop(hops[i])
		if err != nil {
			// A hop that is not an address ends what can be read: the entries to
			// its left were written by whoever wrote it.
			break
		}
		hop = hop.Unmap()
		if !l.isTrusted(hop) {
			return hop, true
		}
	}
	return remote, true
}

// parseHop reads one X-Forwarded-For entry: a bare address, or one a proxy wrote
// with its port or in brackets ("203.0.113.7:51234", "[2001:db8::1]:443",
// "[2001:db8::1]"). Refusing those would end the chain at the first such hop and
// key every client behind that proxy on the proxy's own address.
func parseHop(s string) (netip.Addr, error) {
	s = strings.TrimSpace(s)
	if a, err := netip.ParseAddr(strings.TrimSuffix(strings.TrimPrefix(s, "["), "]")); err == nil {
		return a, nil
	}
	ap, err := netip.ParseAddrPort(s)
	if err != nil {
		return netip.Addr{}, err
	}
	return ap.Addr(), nil
}

// bucketKey is the address a client's bucket is keyed on: an IPv4 address as
// itself, an IPv6 address as its /64 (see the file comment).
func bucketKey(addr netip.Addr) netip.Addr {
	if addr.Is6() && !addr.Is4In6() {
		if p, err := addr.Prefix(64); err == nil {
			return p.Addr()
		}
	}
	return addr
}

// middleware answers 429 with Retry-After to a client past its bucket. The body
// is the API's error envelope, and Cache-Control: no-store keeps a shared cache
// from serving one client's refusal to another.
func (l *rateLimiter) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		addr, ok := l.clientAddr(r)
		if ok {
			if allowed, wait := l.allow(bucketKey(addr)); !allowed {
				h := w.Header()
				h.Set("Retry-After", httpx.RetryAfter(wait))
				h.Set("Cache-Control", "no-store")
				httpx.WriteErr(w, http.StatusTooManyRequests,
					fmt.Sprintf("rate limit exceeded: at most %g requests a second per client; retry after the Retry-After seconds", l.rate))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// ParseTrustedProxies reads a comma-separated list of proxy addresses and CIDR
// prefixes ("10.0.0.0/8, 172.17.0.1") as the limiter's trusted set. Empty is
// none.
func ParseTrustedProxies(raw string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if strings.Contains(item, "/") {
			p, err := netip.ParsePrefix(item)
			if err != nil {
				return nil, fmt.Errorf("trusted proxy %q: %w", item, err)
			}
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(item)
		if err != nil {
			return nil, fmt.Errorf("trusted proxy %q: %w", item, err)
		}
		a = a.Unmap()
		out = append(out, netip.PrefixFrom(a, a.BitLen()))
	}
	return out, nil
}
