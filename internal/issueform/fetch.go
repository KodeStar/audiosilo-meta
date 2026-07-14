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

// defaultFetch fetches an issue-form attachment. SECURITY: it is HTTPS-only and
// pinned to GitHub's user-attachment hosts, so a submission can never point the
// workflow at an arbitrary internal or third-party URL, and the response is
// size-capped so a hostile link cannot exhaust memory. The bytes are only ever
// JSON-decoded by callers; nothing fetched is executed.
func defaultFetch(raw string) ([]byte, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("parse attachment url: %w", err)
	}
	if u.Scheme != "https" {
		return nil, fmt.Errorf("attachment url must be https, got %q", u.Scheme)
	}
	if !allowedAttachmentHost(u.Hostname()) {
		return nil, fmt.Errorf("attachment host %q is not an allowed GitHub attachment host", u.Hostname())
	}

	client := &http.Client{Timeout: attachmentHTTPTimeout}
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
