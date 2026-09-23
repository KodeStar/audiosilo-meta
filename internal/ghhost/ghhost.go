// Package ghhost is the host allowlist applied to a URL that arrives as DATA
// before this project's HTTP clients will send a request to it. It is a LEAF -
// net/http, net/url and nothing else here - because two unrelated packages need
// the identical rule (internal/serve downloading a release asset whose URL it
// read out of GitHub's release JSON, internal/issueform fetching an attachment
// whose URL a submitter typed into an issue form) and a second spelling of an
// allowlist is a second chance to leave one of them behind when the rule moves.
//
// Both callers send something they would not want a third party to have - serve
// attaches its API token to an asset GET, issueform runs inside CI on a runner
// with network reach a contributor does not have - so the rule is applied
// BEFORE the request is built and, through CheckRedirect, to every hop after
// it: an allowlist checked once is an allowlist a 302 walks straight past.
package ghhost

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Allowed reports whether host is a host GitHub serves content from: github.com
// itself (where a browser_download_url and a user-attachment URL point) and the
// *.githubusercontent.com family (where both redirect to).
//
// Each caller's own additions ride in as extra rather than joining the shared
// rule, so this stays the one rule both of them share and neither inherits the
// other's exceptions: serve passes api.github.com, the API's own asset route,
// which is no part of what an attachment fetch may name.
func Allowed(host string, extra ...string) bool {
	host = strings.ToLower(host)
	if host == "github.com" || strings.HasSuffix(host, ".githubusercontent.com") {
		return true
	}
	for _, e := range extra {
		if e != "" && host == strings.ToLower(e) {
			return true
		}
	}
	return false
}

// CheckRedirect builds an http.Client.CheckRedirect that re-applies policy to
// every hop and bounds the chain at maxHops.
//
// The hop limit is not optional book-keeping: setting CheckRedirect at all
// REPLACES net/http's own ten-hop default, so without restating a bound a
// redirect loop would run until the request's deadline.
func CheckRedirect(maxHops int, policy func(*url.URL) error) func(*http.Request, []*http.Request) error {
	return func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxHops {
			return fmt.Errorf("redirected more than %d times", maxHops)
		}
		if err := policy(req.URL); err != nil {
			return fmt.Errorf("redirected to a refused location: %w", err)
		}
		return nil
	}
}
