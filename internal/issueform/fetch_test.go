package issueform

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
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
		_, err := fetchAttachment(allowed.URL+"/redirect", maxAttachmentBytes, allowed.Client(), policy, exempt)
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
		if _, err := fetchAttachment(allowed.URL+"/redirect", maxAttachmentBytes, allowed.Client(), policy, exempt); err == nil {
			t.Fatal("a redirect that downgraded https to http was followed")
		}
	})

	t.Run("a hop inside the allowlist is followed", func(t *testing.T) {
		target = allowed.URL + "/x.json"
		body, err := fetchAttachment(allowed.URL+"/redirect", maxAttachmentBytes, allowed.Client(), policy, exempt)
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
		_, err := fetchAttachment(allowed.URL+"/loop", maxAttachmentBytes, allowed.Client(), policy, exempt)
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
	if _, err := fetchAttachment(srv.URL+"/x.json", maxAttachmentBytes, client, hostPolicy(host),
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
			if _, err := fetchAttachment(srv.URL+"/x.json", maxAttachmentBytes, client, hostPolicy(host),
				func(u *url.URL) bool { return localOrigin(u, host) }); err != nil {
				t.Errorf("concurrent fetch: %v", err)
			}
		}()
	}
	wg.Wait()
}

// TestAttachmentCapsBoundTheBody pins the cap at its edge through the real
// fetcher: a body of exactly the cap is read whole, one byte more is refused
// (and refused as TOO LARGE, not truncated into a parse error downstream). The
// cap is a parameter, so a small one proves the rule; which cap each form passes
// is TestEachTemplateFetchesUnderItsOwnCap's.
func TestAttachmentCapsBoundTheBody(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, err := strconv.ParseInt(r.URL.Query().Get("n"), 10, 64)
		if err != nil {
			http.Error(w, "bad n", http.StatusBadRequest)
			return
		}
		_, _ = w.Write(bytes.Repeat([]byte{'x'}, int(n)))
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "https://")
	exempt := func(u *url.URL) bool { return localOrigin(u, host) }

	const maxBytes = 1024
	body, err := fetchAttachment(fmt.Sprintf("%s/x.json?n=%d", srv.URL, maxBytes), maxBytes, srv.Client(), hostPolicy(host), exempt)
	if err != nil {
		t.Fatalf("a body of exactly the cap was refused: %v", err)
	}
	if len(body) != maxBytes {
		t.Errorf("read %d bytes, want %d", len(body), maxBytes)
	}
	_, err = fetchAttachment(fmt.Sprintf("%s/x.json?n=%d", srv.URL, maxBytes+1), maxBytes, srv.Client(), hostPolicy(host), exempt)
	if err == nil || !strings.Contains(err.Error(), "exceeds the "+sizeLabel(maxBytes)+" limit") {
		t.Errorf("error = %v, want the cap refusal", err)
	}
}

// TestAttachmentTimeoutFollowsTheCap: the fetch deadline grows with the cap, so
// the import form's export gets the time a 25 MiB download needs while a
// sidecar fetch keeps the short deadline it always had.
func TestAttachmentTimeoutFollowsTheCap(t *testing.T) {
	small, large := attachmentTimeout(maxAttachmentBytes), attachmentTimeout(maxImportAttachmentBytes)
	if small > 30*time.Second {
		t.Errorf("sidecar timeout = %v, want it to stay short", small)
	}
	if large < time.Minute {
		t.Errorf("import timeout = %v, want room for a 25 MiB download", large)
	}
}

// TestEachTemplateFetchesUnderItsOwnCap: the cap is chosen by the FORM the file
// came from, so the import template must hand the fetcher its own cap and a
// community sidecar form the small one. Asserted through Process, with an
// injected fetcher recording what it was asked for.
func TestEachTemplateFetchesUnderItsOwnCap(t *testing.T) {
	const at = "https://github.com/user-attachments/files/1/export.json"
	cases := []struct {
		template string
		body     string
		payload  string
		want     int64
	}{
		{"import", importBody("OpenAudible (books.json)", "[books.json]("+at+")"), openAudibleExport, maxImportAttachmentBytes},
		{"characters", charactersBody("existing-work", "[characters.json]("+at+")", true), validCharactersJSON, maxAttachmentBytes},
	}
	for _, c := range cases {
		t.Run(c.template, func(t *testing.T) {
			var asked int64
			fetch := func(u string, maxBytes int64) ([]byte, error) {
				asked = maxBytes
				return []byte(c.payload), nil
			}
			res := Process(Options{DataDir: seedTree(t), Template: c.template, Body: c.body, Fetch: fetch})
			if res.Status != StatusOK {
				t.Fatalf("status = %q, messages = %v", res.Status, res.Messages)
			}
			if asked != c.want {
				t.Errorf("fetched under a %s cap, want %s", sizeLabel(asked), sizeLabel(c.want))
			}
		})
	}
}
