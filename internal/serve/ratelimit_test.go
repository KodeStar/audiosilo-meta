package serve

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"testing"
	"time"

	"github.com/kodestar/audiosilo-meta/internal/artifacttest"
)

// fakeClock is a settable time source for the limiter's bucket arithmetic.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

func TestRateLimiterBucket(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)}
	l := newRateLimiter(1, 3, nil, clock.now)
	a := netip.MustParseAddr("203.0.113.7")
	b := netip.MustParseAddr("198.51.100.9")

	// The burst is allowed, the next is not, and the wait is the time to one
	// token.
	for i := range 3 {
		if ok, _ := l.allow(a); !ok {
			t.Fatalf("request %d of the burst refused", i+1)
		}
	}
	ok, wait := l.allow(a)
	if ok || wait != time.Second {
		t.Fatalf("past the burst = %v, wait %v; want refused, 1s", ok, wait)
	}
	// Another client has its own bucket.
	if ok, _ := l.allow(b); !ok {
		t.Error("a second client was refused by the first one's bucket")
	}
	// Half a second refills half a token: still refused, with the rest to wait.
	clock.t = clock.t.Add(500 * time.Millisecond)
	if ok, wait := l.allow(a); ok || wait != 500*time.Millisecond {
		t.Errorf("after 0.5s = %v, wait %v; want refused, 0.5s", ok, wait)
	}
	clock.t = clock.t.Add(500 * time.Millisecond)
	if ok, _ := l.allow(a); !ok {
		t.Error("a refilled token was refused")
	}
	// The bucket never holds more than the burst, however long the client waits.
	clock.t = clock.t.Add(time.Hour)
	for i := range 3 {
		if ok, _ := l.allow(a); !ok {
			t.Fatalf("request %d after a long wait refused", i+1)
		}
	}
	if ok, _ := l.allow(a); ok {
		t.Error("an idle hour banked more than the burst")
	}
}

func TestRateLimiterSweepsIdleBuckets(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)}
	l := newRateLimiter(10, 30, nil, clock.now)
	l.allow(netip.MustParseAddr("203.0.113.7"))
	clock.t = clock.t.Add(2 * sweepEvery)
	l.allow(netip.MustParseAddr("198.51.100.9"))
	if _, kept := l.buckets[netip.MustParseAddr("203.0.113.7")]; kept || len(l.buckets) != 1 {
		t.Errorf("buckets after a sweep = %v, want only the active client", l.buckets)
	}
}

func TestRateLimiterClientAddr(t *testing.T) {
	trusted, err := ParseTrustedProxies("10.0.0.0/8, 172.17.0.1")
	if err != nil {
		t.Fatal(err)
	}
	l := newRateLimiter(10, 30, trusted, nil)
	for _, tc := range []struct {
		name, remote string
		xff          []string
		want         string
	}{
		{"direct client", "203.0.113.7:5000", nil, "203.0.113.7"},
		// Denied: a header from a client that is not a trusted proxy is the
		// client's own claim, so it is never read.
		{"untrusted remote's header is ignored", "203.0.113.7:5000", []string{"198.51.100.1"}, "203.0.113.7"},
		{"trusted proxy names the client", "172.17.0.1:5000", []string{"198.51.100.1"}, "198.51.100.1"},
		{"rightmost untrusted hop wins", "10.1.2.3:5000", []string{"192.0.2.66, 198.51.100.1, 10.0.0.2"}, "198.51.100.1"},
		{"a spoofed leftmost hop is not the client", "10.1.2.3:5000", []string{"1.1.1.1", "198.51.100.1"}, "198.51.100.1"},
		{"all hops trusted: the proxy itself", "10.1.2.3:5000", []string{"10.0.0.9"}, "10.1.2.3"},
		{"trusted proxy with no header", "10.1.2.3:5000", nil, "10.1.2.3"},
		{"a garbage hop ends the chain", "10.1.2.3:5000", []string{"198.51.100.1, nonsense"}, "10.1.2.3"},
		{"ipv4-mapped proxy", "[::ffff:172.17.0.1]:5000", []string{"198.51.100.1"}, "198.51.100.1"},
		{"ipv6 client", "[2001:db8::1]:5000", nil, "2001:db8::1"},
		// A proxy that writes the client's port, or brackets an IPv6 hop, still
		// names the client rather than ending the chain at the proxy.
		{"hop with a port", "10.1.2.3:5000", []string{"198.51.100.1:51234"}, "198.51.100.1"},
		{"bracketed ipv6 hop with a port", "10.1.2.3:5000", []string{"[2001:db8::7]:443"}, "2001:db8::7"},
		{"bracketed ipv6 hop", "10.1.2.3:5000", []string{"[2001:db8::7]"}, "2001:db8::7"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/api/v1/stats", nil)
			r.RemoteAddr = tc.remote
			for _, v := range tc.xff {
				r.Header.Add("X-Forwarded-For", v)
			}
			got, ok := l.clientAddr(r)
			if !ok || got.String() != tc.want {
				t.Errorf("clientAddr = %v %v, want %s", got, ok, tc.want)
			}
		})
	}
}

// TestBucketKey: an IPv6 client is limited as its /64, so cycling the addresses
// of one subscriber's prefix buys no fresh bucket; IPv4 stays per address.
func TestBucketKey(t *testing.T) {
	for in, want := range map[string]string{
		"203.0.113.7":           "203.0.113.7",
		"2001:db8:1:2:aaaa::1":  "2001:db8:1:2::",
		"2001:db8:1:2:bbbb::ff": "2001:db8:1:2::",
		"2001:db8:1:3::1":       "2001:db8:1:3::",
		"::ffff:198.51.100.9":   "::ffff:198.51.100.9",
		"fe80::1:2:3:4%eth0":    "fe80::",
	} {
		if got := bucketKey(netip.MustParseAddr(in)); got.String() != want {
			t.Errorf("bucketKey(%s) = %s, want %s", in, got, want)
		}
	}
	clock := &fakeClock{t: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)}
	l := newRateLimiter(1, 1, nil, clock.now)
	if ok, _ := l.allow(bucketKey(netip.MustParseAddr("2001:db8:1:2::1"))); !ok {
		t.Fatal("first request refused")
	}
	// Denied: another address in the same /64 is the same client.
	if ok, _ := l.allow(bucketKey(netip.MustParseAddr("2001:db8:1:2::2"))); ok {
		t.Error("a second address of one /64 bought a fresh bucket")
	}
}

func TestParseTrustedProxies(t *testing.T) {
	got, err := ParseTrustedProxies(" 10.0.0.0/8 ,172.17.0.1,, 2001:db8::/32, 10.1.2.3/8")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"10.0.0.0/8", "172.17.0.1/32", "2001:db8::/32", "10.0.0.0/8"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i].String() != want[i] {
			t.Errorf("[%d] = %v, want %s", i, got[i], want[i])
		}
	}
	if got, err := ParseTrustedProxies(""); err != nil || got != nil {
		t.Errorf("empty = %v %v", got, err)
	}
	for _, bad := range []string{"10.0.0.0/33", "proxy.local", "10.0.0"} {
		if _, err := ParseTrustedProxies(bad); err == nil {
			t.Errorf("ParseTrustedProxies(%q) accepted", bad)
		}
	}
}

// limitedServer serves the fixture with a rate limit tight enough to exhaust in
// a test: burst requests, then a token every 1000 seconds.
func limitedServer(t *testing.T, burst int, trusted string) *httptest.Server {
	t.Helper()
	proxies, err := ParseTrustedProxies(trusted)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(Config{DBPath: artifacttest.Build(t, artifacttest.Fixture()), swapGrace: time.Minute,
		Logger: artifacttest.QuietLogger(), RateLimit: 0.001, RateBurst: burst, TrustedProxies: proxies})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func get(t *testing.T, url string, hdr map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func TestRateLimitAnswers429WithRetryAfter(t *testing.T) {
	ts := limitedServer(t, 2, "")
	// Allowed: the burst.
	for i := range 2 {
		if resp := get(t, ts.URL+"/api/v1/stats", nil); resp.StatusCode != http.StatusOK {
			t.Fatalf("request %d of the burst = %d", i+1, resp.StatusCode)
		}
	}
	// Denied: the next, on any API route, with the wait and the error envelope.
	for _, path := range []string{"/api/v1/stats", "/api/v1/works/latest", "/api/v1/librivox/audiobooks", "/api/v1/openapi.json"} {
		resp := get(t, ts.URL+path, nil)
		if resp.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("GET %s past the burst = %d, want 429", path, resp.StatusCode)
		}
		ra, err := strconv.Atoi(resp.Header.Get("Retry-After"))
		if err != nil || ra < 1 || ra > 1000 {
			t.Errorf("Retry-After = %q, want whole seconds up to a token's 1000", resp.Header.Get("Retry-After"))
		}
		if resp.Header.Get("Cache-Control") != "no-store" {
			t.Errorf("429 Cache-Control = %q, want no-store", resp.Header.Get("Cache-Control"))
		}
		// CORS wraps the limit, so a browser can read the 429.
		if resp.Header.Get("Access-Control-Allow-Origin") != "*" {
			t.Errorf("429 without CORS: %v", resp.Header)
		}
		// Retry-After is not CORS-safelisted, so it must be exposed explicitly.
		if resp.Header.Get("Access-Control-Expose-Headers") != "Retry-After" {
			t.Errorf("429 does not expose Retry-After to a cross-origin script: %v", resp.Header)
		}
		var body map[string]string
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil || body["error"] == "" {
			t.Errorf("429 body = %v %v", body, err)
		}
	}
	// Exempt: the readiness probe.
	if resp := get(t, ts.URL+"/healthz", nil); resp.StatusCode != http.StatusOK {
		t.Errorf("/healthz past the burst = %d, want 200 (exempt)", resp.StatusCode)
	}
	// Denied: a client that is not a trusted proxy cannot buy a fresh bucket by
	// naming another address.
	if resp := get(t, ts.URL+"/api/v1/stats", map[string]string{"X-Forwarded-For": "198.51.100.77"}); resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("a spoofed X-Forwarded-For = %d, want 429", resp.StatusCode)
	}
}

func TestRateLimitBehindATrustedProxy(t *testing.T) {
	// The test client connects from loopback, which is trusted here: each
	// forwarded address is its own client.
	ts := limitedServer(t, 1, "127.0.0.1, ::1")
	alice := map[string]string{"X-Forwarded-For": "198.51.100.1"}
	bob := map[string]string{"X-Forwarded-For": "198.51.100.2"}
	if resp := get(t, ts.URL+"/api/v1/stats", alice); resp.StatusCode != http.StatusOK {
		t.Fatalf("alice's first = %d", resp.StatusCode)
	}
	if resp := get(t, ts.URL+"/api/v1/stats", alice); resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("alice's second = %d, want 429", resp.StatusCode)
	}
	if resp := get(t, ts.URL+"/api/v1/stats", bob); resp.StatusCode != http.StatusOK {
		t.Errorf("bob behind the same proxy = %d, want 200 (his own bucket)", resp.StatusCode)
	}
}

func TestNoRateLimitWhenOff(t *testing.T) {
	_, ts := newTestServer(t) // the zero Config: no limit
	for i := range 60 {
		if resp := get(t, ts.URL+"/api/v1/stats", nil); resp.StatusCode != http.StatusOK {
			t.Fatalf("request %d with the limit off = %d", i+1, resp.StatusCode)
		}
	}
}
