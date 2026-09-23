// Package ghhost is the host allowlist applied to a URL that arrives as DATA
// before this project's HTTP clients will send a request to it. It is a LEAF -
// net, net/http, net/url and nothing else here - because two unrelated packages
// need the identical rule (internal/serve downloading a release asset whose URL
// it read out of GitHub's release JSON, internal/issueform fetching an
// attachment whose URL a submitter typed into an issue form) and a second
// spelling of an allowlist is a second chance to leave one of them behind when
// the rule moves.
//
// TWO RULES, DELIBERATELY DIFFERENT, because the two questions are:
//
//   - Allowed is the STRICT host allowlist, and it is asked of the INITIAL URL
//     only - the one the data named. That is where serve decides whether to
//     attach its token and where issueform decides whether a submitter may
//     point CI at a host at all.
//   - HopPolicy is what every REDIRECT hop is judged by, and it is deliberately
//     not the allowlist. A browser_download_url 302s to a CDN GitHub chooses:
//     today that is release-assets.githubusercontent.com, but assets have been
//     served from *.s3.amazonaws.com in the past and GitHub can move them
//     again. Under a hop allowlist that day is not a refused download, it is
//     EVERY refresh failing forever while the server keeps serving stale data -
//     a self-inflicted outage in exchange for a guarantee net/http already
//     gives: http.Client strips Authorization (and Cookie, and
//     WWW-Authenticate) on a redirect to a host that is neither the original
//     nor a subdomain of it, so a hop cannot carry serve's token off GitHub.
//
// What a hop IS judged by is the part net/http does not do for us: the scheme
// must stay https (a token or a CI runner's reach is not sent in clear, and a
// downgrade is something the redirect chose, not the caller), the host must not
// be an IP LITERAL (the SSRF guard - a 302 to 127.0.0.1, to a private range or
// to link-local 169.254.169.254 is how a redirect reaches a metadata service or
// a service bound to the runner's own loopback), and the chain is bounded.
package ghhost

import (
	"fmt"
	"net"
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

// DefaultMaxRedirects is the hop bound CheckRedirect applies when its caller
// passes 0. Two hops is the live shape of an asset download (github.com ->
// objects.githubusercontent.com); the rest is slack for a CDN that adds one.
//
// It is ONE constant rather than a matching pair in each caller: both wanted
// the same number and neither has a reason of its own for it, so a caller that
// states nothing gets this and a caller that states 0 gets this too - 0 is
// never "unbounded", which would be the one value worth refusing.
const DefaultMaxRedirects = 5

// HopPolicy is the rule every redirect hop is judged by - see the package doc
// for why it is not the host allowlist.
func HopPolicy(u *url.URL) error {
	if u.Scheme != "https" {
		return fmt.Errorf("scheme %q is not https", u.Scheme)
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil {
		return fmt.Errorf("host %q is an IP literal", u.Hostname())
	}
	return nil
}

// CheckRedirect builds an http.Client.CheckRedirect that applies HopPolicy to
// every hop and bounds the chain at maxHops (0, or anything below it, means
// DefaultMaxRedirects).
//
// The hop limit is not optional book-keeping: setting CheckRedirect at all
// REPLACES net/http's own ten-hop default, so without restating a bound a
// redirect loop would run until the request's deadline.
//
// exempt, when non-nil, is consulted FIRST and admits a hop HopPolicy would
// refuse. Production passes a func that admits nothing: its only use is a
// client pointed at a local test server, which is plain HTTP on an IP literal
// by nature and is therefore exactly what the hop rule exists to refuse.
func CheckRedirect(maxHops int, exempt func(*url.URL) bool) func(*http.Request, []*http.Request) error {
	if maxHops <= 0 {
		maxHops = DefaultMaxRedirects
	}
	return func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxHops {
			return fmt.Errorf("redirected more than %d times", maxHops)
		}
		if exempt != nil && exempt(req.URL) {
			return nil
		}
		if err := HopPolicy(req.URL); err != nil {
			return fmt.Errorf("redirected to a refused location: %w", err)
		}
		return nil
	}
}
