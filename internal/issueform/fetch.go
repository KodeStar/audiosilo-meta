package issueform

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/kodestar/audiosilo-meta/internal/ghhost"
)

// maxAttachmentBytes caps a fetched attachment. Sidecar JSON is small (a work's
// cast/recaps); anything larger is rejected rather than read into memory.
const maxAttachmentBytes = 1 << 20 // 1 MiB

// attachmentHTTPTimeout bounds a single attachment fetch.
const attachmentHTTPTimeout = 20 * time.Second

// attachmentPolicy decides whether the URL a submission NAMES may be fetched.
// It is a function rather than a bare call so a test can substitute a policy
// that points the fetcher at a local server (the rule below names public hosts,
// which a test cannot be).
//
// It is deliberately not what a REDIRECT hop is judged by: that is
// ghhost.HopPolicy, which requires https and refuses an IP literal but does not
// pin the host (see ghhost's package doc).
type attachmentPolicy func(*url.URL) error

// githubAttachmentPolicy is the production rule: HTTPS, and one of GitHub's
// user-attachment hosts.
func githubAttachmentPolicy(u *url.URL) error {
	if u.Scheme != "https" {
		return fmt.Errorf("attachment url must be https, got %q", u.Scheme)
	}
	if !ghhost.Allowed(u.Hostname()) {
		return fmt.Errorf("attachment host %q is not an allowed GitHub attachment host", u.Hostname())
	}
	return nil
}

// defaultFetch fetches an issue-form attachment. SECURITY: the URL a submission
// names must be https on one of GitHub's user-attachment hosts, so a submission
// can never point the workflow at an arbitrary internal or third-party URL; a
// redirect is a URL the submission did not name, so every hop is re-judged too
// - https, and never an IP literal, which is what keeps a 302 from reaching the
// runner's own loopback or a cloud metadata service. The response is size-capped
// so a hostile link cannot exhaust memory, and the bytes are only ever
// JSON-decoded by callers; nothing fetched is executed.
func defaultFetch(raw string) ([]byte, error) {
	return fetchAttachment(raw, &http.Client{Timeout: attachmentHTTPTimeout}, githubAttachmentPolicy, nil)
}

// fetchAttachment is defaultFetch with the client and the policies supplied:
// policy judges the initial URL BEFORE the request is built, and hopExempt is
// ghhost.CheckRedirect's escape (nil in production; a test uses it to admit its
// own local server, which the hop rule refuses by nature).
func fetchAttachment(raw string, client *http.Client, policy attachmentPolicy, hopExempt func(*url.URL) bool) ([]byte, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("parse attachment url: %w", err)
	}
	if err := policy(u); err != nil {
		return nil, err
	}

	// The client belongs to the CALLER, so its CheckRedirect is not ours to
	// assign: writing through the pointer reconfigures a client that may be
	// serving other requests, and two concurrent fetches would be a data race on
	// one field. A shallow copy shares the Transport - the connection pool, which
	// is the part worth sharing - and nothing else.
	c := *client
	c.CheckRedirect = ghhost.CheckRedirect(0, hopExempt) // 0: ghhost.DefaultMaxRedirects
	resp, err := c.Get(u.String())
	if err != nil {
		return nil, fmt.Errorf("fetch attachment: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch attachment: HTTP %d", resp.StatusCode)
	}

	// Read one byte past the cap so an over-size body is detected, not silently
	// truncated.
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxAttachmentBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read attachment: %w", err)
	}
	if len(data) > maxAttachmentBytes {
		return nil, fmt.Errorf("attachment exceeds %d bytes", maxAttachmentBytes)
	}
	return data, nil
}
