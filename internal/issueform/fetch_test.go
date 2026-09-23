package issueform

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// hostPolicy is the production policy's shape over a fixed host:port set, so a
// test can allow ONE local server and refuse another. The real rule names public
// hosts and both httptest servers are 127.0.0.1, so the port is what tells them
// apart; the scheme arm is the production one, applied to the same URL.
func hostPolicy(allowed string) attachmentPolicy {
	return func(u *url.URL) error {
		if u.Scheme != "https" {
			return fmt.Errorf("attachment url must be https, got %q", u.Scheme)
		}
		if u.Host != allowed {
			return fmt.Errorf("attachment host %q is not an allowed GitHub attachment host", u.Host)
		}
		return nil
	}
}

// TestAttachmentRedirectsAreRechecked is the finding: the allowlist was applied
// to the URL the SUBMISSION names and to nothing after it, so an allowed
// attachment host answering 302 sent the workflow's client - inside CI, on a
// runner with network reach a contributor does not have - to any URL the
// redirect chose. The policy now runs on every hop.
func TestAttachmentRedirectsAreRechecked(t *testing.T) {
	elsewhere := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"secret":"should never be read"}`))
	}))
	defer elsewhere.Close()

	var target string
	allowed := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/redirect":
			http.Redirect(w, r, target, http.StatusFound)
		case "/loop":
			http.Redirect(w, r, "/loop", http.StatusFound)
		default:
			_, _ = w.Write([]byte(`{"ok":true}`))
		}
	}))
	defer allowed.Close()

	policy := hostPolicy(strings.TrimPrefix(allowed.URL, "https://"))

	t.Run("a hop to a refused host is refused", func(t *testing.T) {
		target = elsewhere.URL + "/x.json"
		_, err := fetchAttachment(allowed.URL+"/redirect", allowed.Client(), policy)
		if err == nil {
			t.Fatal("a redirect to a host outside the allowlist was followed")
		}
		if !strings.Contains(err.Error(), "redirected to a refused location") {
			t.Errorf("error = %v, want the redirect refusal", err)
		}
	})

	t.Run("a hop that downgrades the scheme is refused", func(t *testing.T) {
		// Same host, plain HTTP: an allowlisted host can still answer with a
		// redirect that takes the fetch off TLS.
		target = "http://" + strings.TrimPrefix(allowed.URL, "https://") + "/x.json"
		if _, err := fetchAttachment(allowed.URL+"/redirect", allowed.Client(), policy); err == nil {
			t.Fatal("a redirect that downgraded https to http was followed")
		}
	})

	t.Run("a hop inside the allowlist is followed", func(t *testing.T) {
		target = allowed.URL + "/x.json"
		body, err := fetchAttachment(allowed.URL+"/redirect", allowed.Client(), policy)
		if err != nil {
			t.Fatalf("an allowed redirect was refused: %v", err)
		}
		if string(body) != `{"ok":true}` {
			t.Errorf("body = %s", body)
		}
	})

	t.Run("a redirect loop is bounded", func(t *testing.T) {
		// Setting CheckRedirect replaces net/http's own ten-hop default, so the
		// bound has to be ours.
		_, err := fetchAttachment(allowed.URL+"/loop", allowed.Client(), policy)
		if err == nil || !strings.Contains(err.Error(), "redirected more than") {
			t.Errorf("error = %v, want the hop limit", err)
		}
	})
}

// TestGitHubAttachmentPolicy pins the production rule the hops are re-checked
// against - the same two refusals defaultFetch has always made of the initial
// URL, now reachable from one place.
func TestGitHubAttachmentPolicy(t *testing.T) {
	for _, raw := range []string{
		"https://github.com/user-attachments/files/1/characters.json",
		"https://user-images.githubusercontent.com/1/x.json",
	} {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if err := githubAttachmentPolicy(u); err != nil {
			t.Errorf("%s was refused: %v", raw, err)
		}
	}
	for _, raw := range []string{
		"http://github.com/x.json",
		"https://evil.example.com/x.json",
		"https://github.com.evil.example/x.json",
		"https://notgithubusercontent.com/x.json",
	} {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if err := githubAttachmentPolicy(u); err == nil {
			t.Errorf("%s was allowed", raw)
		}
	}
}
