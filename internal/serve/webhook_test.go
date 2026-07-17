package serve

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testWebhookSecret = "0123456789abcdef0123456789abcdef"

func webhookRequest(body, signature, event string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, githubReleaseWebhookPath, strings.NewReader(body))
	req.Header.Set("X-GitHub-Event", event)
	if signature != "" {
		req.Header.Set("X-Hub-Signature-256", signature)
	}
	return req
}

func signWebhook(body, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(body))
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func newWebhookServer(t *testing.T, seed string, fake *fakeGitHub) *Server {
	t.Helper()
	srv, err := New(Config{
		DBPath:        seed,
		Poll:          true,
		Repo:          "owner/name",
		CacheDir:      t.TempDir(),
		WebhookSecret: testWebhookSecret,
		swapGrace:     time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	srv.gh = newGHClient("owner/name", "", fake.srv.URL)
	return srv
}

func TestWebhookConfigValidation(t *testing.T) {
	if _, err := New(Config{WebhookSecret: testWebhookSecret}); err == nil || !strings.Contains(err.Error(), "requires --poll") {
		t.Fatalf("webhook without polling error = %v, want requires --poll", err)
	}
	if _, err := New(Config{Poll: true, WebhookSecret: "too-short"}); err == nil || !strings.Contains(err.Error(), "at least 32 bytes") {
		t.Fatalf("short webhook secret error = %v, want minimum-length error", err)
	}
}

func TestWebhookDisabledWithoutSecret(t *testing.T) {
	seed := buildFixtureDB(t, fixtureCatalog(), nil)
	srv, err := New(Config{DBPath: seed, swapGrace: time.Minute})
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, webhookRequest(`{}`, "", "release"))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("disabled webhook status = %d, want 404", rec.Code)
	}
}

func TestWebhookRejectsUnauthenticatedAndOversizedRequests(t *testing.T) {
	seed := buildFixtureDB(t, fixtureCatalog(), nil)
	fake := newFakeGitHub(t, tagR1, makeAssets(t, readDB(t, seed), "", nil))
	srv := newWebhookServer(t, seed, fake)
	body := `{"action":"published","repository":{"full_name":"owner/name"}}`

	tests := []struct {
		name      string
		body      string
		signature string
		want      int
	}{
		{name: "missing signature", body: body, want: http.StatusUnauthorized},
		{name: "wrong signature", body: body, signature: signWebhook(body, testWebhookSecret+"x"), want: http.StatusUnauthorized},
		{name: "oversized payload", body: strings.Repeat("x", maxWebhookBodyBytes+1), signature: "irrelevant", want: http.StatusRequestEntityTooLarge},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, webhookRequest(tc.body, tc.signature, "release"))
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}
		})
	}
	if got := fake.fullFetch.Load(); got != 0 {
		t.Fatalf("rejected webhooks fetched releases %d times, want 0", got)
	}
}

func TestWebhookIgnoresUnrelatedSignedEvents(t *testing.T) {
	seed := buildFixtureDB(t, fixtureCatalog(), nil)
	fake := newFakeGitHub(t, tagR1, makeAssets(t, readDB(t, seed), "", nil))
	srv := newWebhookServer(t, seed, fake)

	tests := []struct {
		name  string
		body  string
		event string
	}{
		{name: "different event", body: `{}`, event: "push"},
		{name: "different action", body: `{"action":"created","repository":{"full_name":"owner/name"}}`, event: "release"},
		{name: "different repository", body: `{"action":"published","repository":{"full_name":"other/repo"}}`, event: "release"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, webhookRequest(tc.body, signWebhook(tc.body, testWebhookSecret), tc.event))
			if rec.Code != http.StatusAccepted {
				t.Fatalf("status = %d, want 202", rec.Code)
			}
		})
	}
	if got := fake.fullFetch.Load(); got != 0 {
		t.Fatalf("ignored webhooks fetched releases %d times, want 0", got)
	}
}

func TestWebhookRefreshesPublishedRelease(t *testing.T) {
	v1Path, _, _, v2 := buildV1V2(t)
	fake := newFakeGitHub(t, tagR2, makeAssets(t, v2, "", nil))
	srv := newWebhookServer(t, v1Path, fake)
	body := `{"action":"published","repository":{"full_name":"owner/name"}}`

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, webhookRequest(body, signWebhook(body, testWebhookSecret), "release"))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", rec.Code)
	}

	deadline := time.Now().Add(10 * time.Second)
	for srv.current().tag != tagR2 {
		if time.Now().After(deadline) {
			t.Fatalf("webhook did not refresh to %q within the deadline", tagR2)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := srv.current().stats.Works; got != 5 {
		t.Fatalf("works = %d, want 5 after webhook refresh", got)
	}
}

func TestWebhookRejectsMalformedSignedJSON(t *testing.T) {
	seed := buildFixtureDB(t, fixtureCatalog(), nil)
	fake := newFakeGitHub(t, tagR1, makeAssets(t, readDB(t, seed), "", nil))
	srv := newWebhookServer(t, seed, fake)
	body := `{"action":`

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, webhookRequest(body, signWebhook(body, testWebhookSecret), "release"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if got := fake.fullFetch.Load(); got != 0 {
		t.Fatalf("malformed webhook fetched releases %d times, want 0", got)
	}
}

func TestValidWebhookSignature(t *testing.T) {
	body := []byte("payload")
	sig := signWebhook(string(body), testWebhookSecret)
	if !validWebhookSignature(body, sig, testWebhookSecret) {
		t.Fatal("valid signature rejected")
	}
	if validWebhookSignature(bytes.Clone(body), sig, testWebhookSecret+"x") {
		t.Fatal("signature made with a different secret accepted")
	}
}
