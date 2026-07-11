package serve

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ghClient talks to the GitHub Releases API, remembering the last ETag so a
// poll that finds nothing new costs one conditional request.
type ghClient struct {
	base  string // API base, "https://api.github.com" (overridable for tests)
	repo  string // "owner/name"
	token string // optional
	http  *http.Client
	etag  string
}

func newGHClient(repo, token, base string) *ghClient {
	if base == "" {
		base = "https://api.github.com"
	}
	return &ghClient{
		base:  strings.TrimRight(base, "/"),
		repo:  repo,
		token: token,
		http:  &http.Client{Timeout: 60 * time.Second},
	}
}

type ghAsset struct {
	Name        string `json:"name"`
	DownloadURL string `json:"browser_download_url"`
}

type ghRelease struct {
	TagName string    `json:"tag_name"`
	Assets  []ghAsset `json:"assets"`
}

// latestRelease fetches /releases/latest conditionally. notModified is true when
// GitHub answers 304 (nothing changed since the last successful fetch).
func (c *ghClient) latestRelease(ctx context.Context) (rel *ghRelease, notModified bool, err error) {
	url := fmt.Sprintf("%s/repos/%s/releases/latest", c.base, c.repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if c.etag != "" {
		req.Header.Set("If-None-Match", c.etag)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotModified {
		return nil, true, nil
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, false, fmt.Errorf("releases/latest: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var r ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, false, err
	}
	if et := resp.Header.Get("ETag"); et != "" {
		c.etag = et
	}
	return &r, false, nil
}

// download GETs url with the client's auth and returns the body.
func (c *ghClient) download(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/octet-stream")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

func findAsset(rel *ghRelease, name string) (string, bool) {
	for _, a := range rel.Assets {
		if a.Name == name {
			return a.DownloadURL, true
		}
	}
	return "", false
}

// verifyChecksum checks gzData against a `sha256sum`-format checksum file (the
// first whitespace-separated field is the expected lowercase hex digest).
func verifyChecksum(gzData, checksumFile []byte) error {
	fields := strings.Fields(string(checksumFile))
	if len(fields) == 0 {
		return fmt.Errorf("checksum file is empty")
	}
	want := strings.ToLower(fields[0])
	sum := sha256.Sum256(gzData)
	got := hex.EncodeToString(sum[:])
	if got != want {
		return fmt.Errorf("sha256 mismatch: got %s, want %s", got, want)
	}
	return nil
}

// gunzipTo decompresses gzData into a new file at path (atomically via a temp
// file + rename).
func gunzipTo(gzData []byte, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	zr, err := gzip.NewReader(strings.NewReader(string(gzData)))
	if err != nil {
		return err
	}
	defer func() { _ = zr.Close() }()

	tmp, err := os.CreateTemp(filepath.Dir(path), ".meta-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := io.Copy(tmp, zr); err != nil { //nolint:gosec // trusted release artifact, sha256-verified before this call
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// refresh fetches the latest release; if it is newer than the loaded one, it
// downloads, verifies, gunzips, and hot-swaps the snapshot. It is a no-op on 304
// or when the loaded tag already matches. Serialized by s.mu.
func (s *Server) refresh(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	rel, notModified, err := s.gh.latestRelease(ctx)
	if err != nil {
		return err
	}
	if notModified {
		return nil
	}
	if rel.TagName != "" && rel.TagName == s.loaded {
		return nil
	}

	gzURL, ok := findAsset(rel, "meta.sqlite.gz")
	if !ok {
		return fmt.Errorf("release %s has no meta.sqlite.gz asset", rel.TagName)
	}
	sumURL, ok := findAsset(rel, "meta.sqlite.gz.sha256")
	if !ok {
		return fmt.Errorf("release %s has no meta.sqlite.gz.sha256 asset", rel.TagName)
	}

	gzData, err := s.gh.download(ctx, gzURL)
	if err != nil {
		return err
	}
	sumData, err := s.gh.download(ctx, sumURL)
	if err != nil {
		return err
	}
	if err := verifyChecksum(gzData, sumData); err != nil {
		return err
	}

	cache := s.cfg.CacheDir
	if cache == "" {
		cache = "./cache"
	}
	safeTag := strings.NewReplacer("/", "-", " ", "-").Replace(rel.TagName)
	dbPath := filepath.Join(cache, "meta-"+safeTag+".sqlite")
	if err := gunzipTo(gzData, dbPath); err != nil {
		return err
	}

	snap, err := openSnapshot(dbPath, rel.TagName)
	if err != nil {
		return err
	}
	s.swap(snap)
	s.loaded = rel.TagName
	s.log.Printf("serve: loaded release %s (%d works, built %s)", rel.TagName, snap.stats.Works, snap.stats.BuiltAt)
	return nil
}

// pollLoop refreshes on cfg.Interval until ctx is cancelled. Failures are logged
// and retried on the next tick; a poll never crashes the serving process.
func (s *Server) pollLoop(ctx context.Context) {
	t := time.NewTicker(s.cfg.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := s.refresh(ctx); err != nil {
				s.log.Printf("serve: poll refresh failed: %v", err)
			}
		}
	}
}
