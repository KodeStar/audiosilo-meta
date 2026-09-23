package ghhost

import (
	"errors"
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

// TestCheckRedirect pins the two jobs of the hop policy: the rule runs again on
// every hop, and the chain is bounded - which it has to be, because setting
// CheckRedirect replaces net/http's own ten-hop default.
func TestCheckRedirect(t *testing.T) {
	refuse := errors.New("nope")
	policy := func(u *url.URL) error {
		if u.Host == "bad.example" {
			return refuse
		}
		return nil
	}
	check := CheckRedirect(3, policy)

	hop := func(host string, n int) error {
		req := &http.Request{URL: &url.URL{Scheme: "https", Host: host}}
		return check(req, make([]*http.Request, n))
	}
	if err := hop("github.com", 2); err != nil {
		t.Errorf("an allowed hop inside the limit was refused: %v", err)
	}
	err := hop("bad.example", 0)
	if err == nil || !strings.Contains(err.Error(), "redirected to a refused location") {
		t.Errorf("error = %v, want the policy refusal", err)
	}
	if !errors.Is(err, refuse) {
		t.Errorf("the policy's own error was not wrapped: %v", err)
	}
	if err := hop("github.com", 3); err == nil || !strings.Contains(err.Error(), "redirected more than 3 times") {
		t.Errorf("error = %v, want the hop limit", err)
	}
}
