package serve

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The SENDER of the release webhook is .github/scripts/notify-release.sh, and
// the two tests below are what keep it pinned to the receiver in webhook.go.
//
// They exist because the envelope broke once and nothing noticed: the payload
// was composed with `printf` inside single quotes, so its backslashes were
// literal, the body was not JSON, every delivery came back 400, and the hourly
// poller carried the release anyway. Nothing in either repo compared what the
// workflow sent with what the handler accepts - one was YAML, the other Go.
//
// So the script is exercised the way it is used rather than re-implemented here
// (scripts/packmerge_test.go's precedent): run it in --print mode, take the
// signature and the payload bytes it would have sent, and POST exactly those to
// the real handler through httptest.

// notifyReleaseScript runs the sender in --print mode and returns the signature
// and payload it would have put on the wire. It skips the test rather than
// failing where the script's own dependencies are absent - a Go-only machine is
// a legitimate place to run the suite; CI has all three.
func notifyReleaseScript(t *testing.T, repo, secret string) (signature, payload string) {
	t.Helper()
	for _, tool := range []string{"bash", "jq", "python3"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed, so the sender script cannot be driven here", tool)
		}
	}
	script, err := filepath.Abs(filepath.Join("..", "..", ".github", "scripts", "notify-release.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(script); err != nil {
		t.Fatalf("the release sender script is not where this test expects it: %v", err)
	}

	cmd := exec.Command("bash", script, "--print")
	cmd.Env = append(os.Environ(),
		"GITHUB_REPOSITORY="+repo,
		"WEBHOOK_SECRET="+secret,
		// Deliberately unset: --print must never reach the network, and an
		// inherited URL from the environment would hide it if it did.
		"WEBHOOK_URL=",
	)
	out, err := cmd.Output()
	if err != nil {
		var stderr string
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = string(ee.Stderr)
		}
		t.Fatalf("notify-release.sh --print failed: %v\n%s", err, stderr)
	}
	signature, payload, ok := strings.Cut(string(out), "\n")
	if !ok {
		t.Fatalf("notify-release.sh --print wrote %q, want \"<signature>\\n<payload>\"", out)
	}
	return signature, payload
}

func TestReleaseNotifyScriptIsAcceptedByTheHandler(t *testing.T) {
	signature, payload := notifyReleaseScript(t, "owner/name", testWebhookSecret)

	v1Path, _, _, v2 := buildV1V2(t)
	fake := newFakeGitHub(t, tagR2, makeAssets(t, v2, "", nil))
	srv := newWebhookServer(t, v1Path, fake)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, webhookRequest(payload, signature, "release"))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("the handler answered the script's own delivery with %d (%s), want 202",
			rec.Code, strings.TrimSpace(rec.Body.String()))
	}

	// 202 alone only says the signature verified. Waiting for the swap proves
	// the payload PARSED and named this repository - the two things a malformed
	// or mis-addressed body would fail silently at, since both answer 202 too -
	// and it settles the refresh goroutine before the test's temp dirs go away.
	deadline := time.Now().Add(10 * time.Second)
	for srv.current().tag != tagR2 {
		if time.Now().After(deadline) {
			t.Fatalf("the script's delivery did not drive a refresh to %q within the deadline", tagR2)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestReleaseNotifyScriptSignatureCoversThePayload(t *testing.T) {
	signature, payload := notifyReleaseScript(t, "owner/name", testWebhookSecret)

	// One byte of the body changed: "published" -> "publishee". The signature is
	// the script's own, over the bytes it composed, so this is the tamper case
	// the HMAC exists for rather than a hand-written wrong signature.
	tampered := strings.Replace(payload, `"published"`, `"publishee"`, 1)
	if tampered == payload {
		t.Fatalf("the payload no longer contains the action this test flips: %q", payload)
	}

	seed := buildFixtureDB(t, fixtureCatalog())
	fake := newFakeGitHub(t, tagR1, makeAssets(t, readDB(t, seed), "", nil))
	srv := newWebhookServer(t, seed, fake)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, webhookRequest(tampered, signature, "release"))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("a tampered body under the script's signature = %d, want 401", rec.Code)
	}
	if got := fake.fullFetch.Load(); got != 0 {
		t.Fatalf("the rejected delivery fetched releases %d times, want 0", got)
	}
}
