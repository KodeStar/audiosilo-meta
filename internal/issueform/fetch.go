package issueform

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// maxAttachmentBytes caps a fetched attachment. Sidecar JSON is small (a work's
// cast/recaps); anything larger is rejected rather than read into memory.
const maxAttachmentBytes = 1 << 20 // 1 MiB

// attachmentHTTPTimeout bounds a single attachment fetch.
const attachmentHTTPTimeout = 20 * time.Second

// maxAttachmentRedirects bounds the redirect chain. Setting CheckRedirect
// REPLACES net/http's own ten-hop default, so the limit has to be restated here
// or a redirect loop would run until the timeout.
const maxAttachmentRedirects = 5

// attachmentPolicy decides whether one URL may be fetched. It is a function
// rather than a bare call so the SAME rule can be applied to the URL a
// submission names and to every hop a redirect takes it to, and so a test can
// substitute a policy that points the fetcher at a local server (the rule below
// names public hosts, which a test cannot be).
type attachmentPolicy func(*url.URL) error

// githubAttachmentPolicy is the production rule: HTTPS, and one of GitHub's
// user-attachment hosts.
func githubAttachmentPolicy(u *url.URL) error {
	if u.Scheme != "https" {
		return fmt.Errorf("attachment url must be https, got %q", u.Scheme)
	}
	if !allowedAttachmentHost(u.Hostname()) {
		return fmt.Errorf("attachment host %q is not an allowed GitHub attachment host", u.Hostname())
	}
	return nil
}

// defaultFetch fetches an issue-form attachment. SECURITY: it is HTTPS-only and
// pinned to GitHub's user-attachment hosts - at every hop, not only the first,
// since a redirect is a URL the submission did not name and an allowlist checked
// once is an allowlist a 302 walks straight past - so a submission can never
// point the workflow at an arbitrary internal or third-party URL, and the
// response is size-capped so a hostile link cannot exhaust memory. The bytes are
// only ever JSON-decoded by callers; nothing fetched is executed.
func defaultFetch(raw string) ([]byte, error) {
	return fetchAttachment(raw, &http.Client{Timeout: attachmentHTTPTimeout}, githubAttachmentPolicy)
}

// fetchAttachment is defaultFetch with the client and the policy supplied. The
// policy is applied to the initial URL BEFORE the request and, through
// CheckRedirect, to every hop after it.
func fetchAttachment(raw string, client *http.Client, policy attachmentPolicy) ([]byte, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("parse attachment url: %w", err)
	}
	if err := policy(u); err != nil {
		return nil, err
	}

	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxAttachmentRedirects {
			return fmt.Errorf("attachment url redirected more than %d times", maxAttachmentRedirects)
		}
		if err := policy(req.URL); err != nil {
			return fmt.Errorf("attachment url redirected to a refused location: %w", err)
		}
		return nil
	}
	resp, err := client.Get(u.String())
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

// allowedAttachmentHost pins attachment fetches to GitHub's user-content hosts.
func allowedAttachmentHost(host string) bool {
	host = strings.ToLower(host)
	return host == "github.com" || strings.HasSuffix(host, ".githubusercontent.com")
}
