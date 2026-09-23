package issueform

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
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

// localOrigin is the hop exemption a test needs: a local httptest server is
// plain-or-TLS on an IP LITERAL, which is exactly the shape ghhost.HopPolicy
// refuses. Matching the SCHEME too is what keeps the downgrade case below real.
func localOrigin(u *url.URL, allowed string) bool {
	return u.Scheme == "https" && u.Host == allowed
}

// TestAttachmentRedirectsAreRechecked is the finding: the URL the SUBMISSION
// names was judged and nothing after it, so an allowed attachment host
// answering 302 sent the workflow's client - inside CI, on a runner with
// network reach a contributor does not have - to any URL the redirect chose.
// Every hop is judged now. The rule a hop is judged by is ghhost.HopPolicy, not
// the host allowlist (see ghhost's package doc), and that an ordinary unlisted
// https host IS followed is pinned there - a local test server can only be an
// IP literal, so it cannot be pinned from here.
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

	host := strings.TrimPrefix(allowed.URL, "https://")
	policy := hostPolicy(host)
	exempt := func(u *url.URL) bool { return localOrigin(u, host) }

	t.Run("a hop to a refused host is refused", func(t *testing.T) {
		target = elsewhere.URL + "/x.json"
		_, err := fetchAttachment(allowed.URL+"/redirect", allowed.Client(), policy, exempt)
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
		if _, err := fetchAttachment(allowed.URL+"/redirect", allowed.Client(), policy, exempt); err == nil {
			t.Fatal("a redirect that downgraded https to http was followed")
		}
	})

	t.Run("a hop inside the allowlist is followed", func(t *testing.T) {
		target = allowed.URL + "/x.json"
		body, err := fetchAttachment(allowed.URL+"/redirect", allowed.Client(), policy, exempt)
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
		_, err := fetchAttachment(allowed.URL+"/loop", allowed.Client(), policy, exempt)
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

// TestFetchAttachmentLeavesTheCallersClientAlone: the redirect policy used to be
// assigned through the caller's *http.Client, which reconfigures a client the
// caller may still be using and is a data race between two concurrent fetches.
// The policy goes on a shallow COPY instead.
func TestFetchAttachmentLeavesTheCallersClientAlone(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	client := srv.Client()
	host := strings.TrimPrefix(srv.URL, "https://")
	if _, err := fetchAttachment(srv.URL+"/x.json", client, hostPolicy(host),
		func(u *url.URL) bool { return localOrigin(u, host) }); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if client.CheckRedirect != nil {
		t.Error("the caller's client had its CheckRedirect assigned")
	}

	// And two fetches through one client race on nothing (run under -race).
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := fetchAttachment(srv.URL+"/x.json", client, hostPolicy(host),
				func(u *url.URL) bool { return localOrigin(u, host) }); err != nil {
				t.Errorf("concurrent fetch: %v", err)
			}
		}()
	}
	wg.Wait()
}
