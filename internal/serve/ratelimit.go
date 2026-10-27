package serve

import (
	"fmt"
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
// every request name a fresh address and never be limited.

// Defaults for metaserve's flags (cmd/metaserve): ten requests a second per
// client with a burst of thirty is far beyond what a person browsing or an app
// syncing needs, and well inside what a scraper looping over the API wants.
const (
	DefaultRateLimit = 10
	DefaultRateBurst = 30
)

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

func newRateLimiter(rate float64, burst int, trusted []netip.Prefix, now func() time.Time) *rateLimiter {
	if now == nil {
		now = time.Now
	}
	return &rateLimiter{
		rate: rate, burst: float64(max(burst, 1)), trusted: trusted, now: now,
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
		hop, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
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

// middleware answers 429 with Retry-After to a client past its bucket. The body
// is the API's error envelope, and Cache-Control: no-store keeps a shared cache
// from serving one client's refusal to another.
func (l *rateLimiter) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		addr, ok := l.clientAddr(r)
		if ok {
			if allowed, wait := l.allow(addr); !allowed {
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
