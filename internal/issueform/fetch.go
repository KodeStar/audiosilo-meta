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

// The attachment caps are PER TEMPLATE, because the files the forms carry are
// different kinds of thing. Anything over its cap is rejected rather than read
// into memory, and each caller passes the cap for the file it expects.
const (
	// maxAttachmentBytes caps a community sidecar (characters.json, recaps.json:
	// one work's cast or recaps, which are small). Those forms live on the
	// community repository, which runs this same code under its own profile.
	maxAttachmentBytes int64 = 1 << 20 // 1 MiB
	// maxImportAttachmentBytes caps a library export on the import form, which is
	// the core repository's main attachment and a far larger file: an OpenAudible
	// or Libation export of a few hundred books is already past 1 MiB. It is
	// GitHub's own ceiling for a non-image issue attachment ("25MB for all other
	// files", docs.github.com/en/get-started/writing-on-github/
	// working-with-advanced-formatting/attaching-files, checked 2026-09-24), taken
	// as MiB so it is never the tighter of the two: a file GitHub accepted is a
	// file we read. The body is held once, next to a catalogue load measured in
	// GB, so it is not what bounds the run's memory.
	maxImportAttachmentBytes int64 = 25 << 20 // 25 MiB
)

// attachmentTimeout bounds a single attachment fetch, body included, and is
// DERIVED from the cap rather than sized for the largest one: a fixed floor for
// the round trip plus 2s per MiB the cap allows (a 512 KiB/s floor rate). A
// sidecar keeps the ~20s it always had and a 25 MiB export gets 70s - a slow
// link, not a hung one - without the small forms waiting that long on a dead one.
func attachmentTimeout(maxBytes int64) time.Duration {
	return 20*time.Second + time.Duration(maxBytes>>20)*2*time.Second
}

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
func defaultFetch(raw string, maxBytes int64) ([]byte, error) {
	return fetchAttachment(raw, maxBytes, &http.Client{Timeout: attachmentTimeout(maxBytes)}, githubAttachmentPolicy, nil)
}

// fetchAttachment is defaultFetch with the client and the policies supplied.
// maxBytes is the caller's cap (see maxAttachmentBytes); policy judges the initial URL BEFORE the request is built, and hopExempt is
// ghhost.CheckRedirect's escape (nil in production; a test uses it to admit its
// own local server, which the hop rule refuses by nature).
func fetchAttachment(raw string, maxBytes int64, client *http.Client, policy attachmentPolicy, hopExempt func(*url.URL) bool) ([]byte, error) {
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
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read attachment: %w", err)
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("attachment exceeds the %s limit for this form", sizeLabel(maxBytes))
	}
	return data, nil
}

// sizeLabel renders a byte cap the way a submitter reads one: whole MiB where it
// is one, bytes otherwise.
func sizeLabel(n int64) string {
	if n >= 1<<20 && n%(1<<20) == 0 {
		return fmt.Sprintf("%d MiB", n>>20)
	}
	return fmt.Sprintf("%d-byte", n)
}
