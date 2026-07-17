package serve

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	githubReleaseWebhookPath = "/hooks/github/release"
	minWebhookSecretBytes    = 32
	maxWebhookBodyBytes      = 1 << 20
	webhookRefreshTimeout    = 5 * time.Minute
)

type githubReleaseWebhook struct {
	Action     string `json:"action"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
}

// handleGitHubReleaseWebhook accepts a GitHub-compatible release notification
// signed with WebhookSecret. The release workflow calls it only after all data
// assets have uploaded, avoiding the race in a repository "release published"
// hook where the event can arrive before its assets are ready. The payload is
// only a trigger: refresh still queries GitHub, verifies the published checksums,
// and uses the same atomic hot-swap path as the fallback poller.
func (s *Server) handleGitHubReleaseWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWebhookBodyBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			http.Error(w, "webhook payload too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "invalid webhook payload", http.StatusBadRequest)
		return
	}
	if !validWebhookSignature(body, r.Header.Get("X-Hub-Signature-256"), s.cfg.WebhookSecret) {
		http.Error(w, "invalid webhook signature", http.StatusUnauthorized)
		return
	}
	if r.Header.Get("X-GitHub-Event") != "release" {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	var event githubReleaseWebhook
	if err := json.Unmarshal(body, &event); err != nil {
		http.Error(w, "invalid webhook payload", http.StatusBadRequest)
		return
	}
	if event.Action != "published" || !strings.EqualFold(event.Repository.FullName, s.cfg.Repo) {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	w.WriteHeader(http.StatusAccepted)
	go s.refreshFromWebhook()
}

func validWebhookSignature(body []byte, signature, secret string) bool {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(signature), []byte(want))
}

// refreshFromWebhook runs a single refresh on behalf of a delivered webhook.
// Deliveries coalesce: a refresh always targets the newest release, so while one
// is in flight (or waiting on refresh's mutex behind the fallback poller) further
// deliveries are dropped rather than piling up serialized goroutines, each of
// which would only re-query GitHub and 304.
func (s *Server) refreshFromWebhook() {
	if !s.webhookRefreshing.CompareAndSwap(false, true) {
		return
	}
	defer s.webhookRefreshing.Store(false)
	ctx, cancel := context.WithTimeout(context.Background(), webhookRefreshTimeout)
	defer cancel()
	if err := s.refresh(ctx); err != nil {
		s.log.Printf("serve: webhook refresh failed: %v", err)
	}
}
