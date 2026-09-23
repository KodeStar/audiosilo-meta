package ghhost

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// TestAllowed pins the shared rule from both sides, including the near-misses a
// suffix comparison written the obvious way lets through.
func TestAllowed(t *testing.T) {
	for _, host := range []string{
		"github.com", "GitHub.com",
		"objects.githubusercontent.com",
		"release-assets.githubusercontent.com",
		"user-images.githubusercontent.com",
	} {
		if !Allowed(host) {
			t.Errorf("%q was refused", host)
		}
	}
	for _, host := range []string{
		"", "evil.example",
		"github.com.evil.example",
		"notgithubusercontent.com",
		"api.github.com", // an addition of serve's, never of the shared rule
	} {
		if Allowed(host) {
			t.Errorf("%q was allowed", host)
		}
	}

	// The caller's own additions ride in as extra, matched case-insensitively,
	// and an empty one can never admit an empty host.
	if !Allowed("api.github.com", "api.github.com") {
		t.Error("an extra host was refused")
	}
	if !Allowed("127.0.0.1:53217", "", "127.0.0.1:53217") {
		t.Error("an extra host beside an empty one was refused")
	}
	if Allowed("", "") {
		t.Error("an empty extra admitted an empty host")
	}
}

// TestHopPolicy pins the rule a REDIRECT is judged by, which is deliberately
// not the host allowlist. GitHub has moved release assets between CDNs before
// (*.s3.amazonaws.com), and net/http already strips Authorization on a
// cross-host hop, so refusing an unlisted https host here would buy nothing and
// cost every refresh on the day GitHub moves them again.
func TestHopPolicy(t *testing.T) {
	for _, raw := range []string{
		"https://objects.githubusercontent.com/x",
		"https://github-production-release-asset.s3.amazonaws.com/x", // the shape the allowlist would refuse
		"https://some-cdn.example/x",
	} {
		if err := HopPolicy(mustParse(t, raw)); err != nil {
			t.Errorf("%s was refused: %v", raw, err)
		}
	}
	for _, raw := range []string{
		"http://objects.githubusercontent.com/x", // a downgrade the redirect chose
		"https://127.0.0.1/x",
		"https://[::1]/x",
		"https://169.254.169.254/latest/meta-data/", // the cloud metadata service
		"https://10.0.0.5/x",
		"https://192.168.1.1:8443/x",
	} {
		if err := HopPolicy(mustParse(t, raw)); err == nil {
			t.Errorf("%s was allowed", raw)
		}
	}
}

func mustParse(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return u
}

// TestCheckRedirect pins the three jobs of the hop policy: HopPolicy runs again
// on every hop, the chain is bounded - which it has to be, because setting
// CheckRedirect replaces net/http's own ten-hop default - and a caller-supplied
// exemption is consulted first (how a test points a client at a local server).
func TestCheckRedirect(t *testing.T) {
	check := CheckRedirect(3, nil)

	hop := func(raw string, n int) error {
		req := &http.Request{URL: mustParse(t, raw)}
		return check(req, make([]*http.Request, n))
	}
	if err := hop("https://cdn.example/x", 2); err != nil {
		t.Errorf("an ordinary https hop inside the limit was refused: %v", err)
	}
	err := hop("http://cdn.example/x", 0)
	if err == nil || !strings.Contains(err.Error(), "redirected to a refused location") {
		t.Errorf("error = %v, want the policy refusal", err)
	}
	if !strings.Contains(err.Error(), "is not https") {
		t.Errorf("the policy's own reason was not wrapped: %v", err)
	}
	if err := hop("https://cdn.example/x", 3); err == nil || !strings.Contains(err.Error(), "redirected more than 3 times") {
		t.Errorf("error = %v, want the hop limit", err)
	}

	// 0 is the DEFAULT bound, never "unbounded".
	def := CheckRedirect(0, nil)
	req := &http.Request{URL: mustParse(t, "https://cdn.example/x")}
	if err := def(req, make([]*http.Request, DefaultMaxRedirects-1)); err != nil {
		t.Errorf("a hop inside the default bound was refused: %v", err)
	}
	err = def(req, make([]*http.Request, DefaultMaxRedirects))
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("more than %d times", DefaultMaxRedirects)) {
		t.Errorf("error = %v, want the default hop limit", err)
	}

	// The exemption admits what HopPolicy refuses, and nothing else.
	local := CheckRedirect(3, func(u *url.URL) bool { return u.Host == "127.0.0.1:8080" })
	if err := local(&http.Request{URL: mustParse(t, "http://127.0.0.1:8080/x")}, nil); err != nil {
		t.Errorf("the exempt origin was refused: %v", err)
	}
	if err := local(&http.Request{URL: mustParse(t, "http://127.0.0.1:9090/x")}, nil); err == nil {
		t.Error("a different local origin rode in on the exemption")
	}
}
