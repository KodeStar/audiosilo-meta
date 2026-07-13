package serve

import (
	"bytes"
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

	"github.com/klauspost/compress/zstd"
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
	TagName     string    `json:"tag_name"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt time.Time `json:"published_at"`
	Assets      []ghAsset `json:"assets"`
}

// releaseListPageSize is how many recent releases the poller scans for a data
// release. The repo also cuts code/image releases (v*, no data assets) between
// data releases, so a window of the list is searched and the newest by
// published_at selected (the list order is not publish-chronological - see
// latestDataRelease). If non-data releases ever exhausted the window, a
// poll-only boot would fail New()'s synchronous first refresh and the process
// would exit - acceptable at this repo's release cadence.
const releaseListPageSize = 15

// latestDataRelease fetches the newest releases conditionally and returns the
// non-draft, non-prerelease release carrying a meta.sqlite.gz asset with the
// MAXIMUM published_at. GitHub's "latest" release can be a code/image release
// (v*) with no data assets, so selection is by asset presence, not recency
// alone. The list endpoint's order is NOT publish-chronological: it was
// observed live to be created_at date descending, then reverse-lexicographic
// tag order within a day - so with several same-day data releases the
// first-listed one is not the newest. Selection is therefore by max
// published_at, not list position. notModified is true when GitHub answers 304
// (nothing changed since the last successful fetch).
func (c *ghClient) latestDataRelease(ctx context.Context) (rel *ghRelease, notModified bool, err error) {
	url := fmt.Sprintf("%s/repos/%s/releases?per_page=%d", c.base, c.repo, releaseListPageSize)
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
		return nil, false, fmt.Errorf("releases: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var list []ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, false, err
	}
	if et := resp.Header.Get("ETag"); et != "" {
		c.etag = et
	}
	var best *ghRelease
	for i := range list {
		r := &list[i]
		if r.Draft || r.Prerelease {
			continue
		}
		if _, ok := findAsset(r, dataAssetName); !ok {
			continue
		}
		// Greater-or-equal keeps the LATER-in-list entry on equal timestamps,
		// matching release.yml's jq (max_by is a stable sort, so it returns the
		// last of a tied pair) - the two selectors must agree even on a
		// same-second tie, or the workflow could base its patch on a release no
		// running server has loaded.
		if best == nil || !r.PublishedAt.Before(best.PublishedAt) {
			best = r
		}
	}
	if best != nil {
		return best, false, nil
	}
	// The stored ETag makes the next poll 304 until the list changes - correct,
	// since retrying an unchanged list cannot find a data release either.
	return nil, false, fmt.Errorf("no data release with a %s asset among the latest %d", dataAssetName, len(list))
}

// forget drops the remembered ETag so the next poll refetches the release
// metadata unconditionally. Called when a refresh fails AFTER a successful
// (200) metadata fetch - the ETag was already stored, so without this the next
// poll would 304 and never retry the failed download until another release is
// cut.
func (c *ghClient) forget() { c.etag = "" }

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

// downloadAsset finds the named asset on rel and downloads it.
func (s *Server) downloadAsset(ctx context.Context, rel *ghRelease, name string) ([]byte, error) {
	url, ok := findAsset(rel, name)
	if !ok {
		return nil, fmt.Errorf("release %s has no %s asset", rel.TagName, name)
	}
	return s.gh.download(ctx, url)
}

// patchWindowLog is the log2 of the zstd window the release patches are
// compressed with (--long=31 in .github/workflows/release.yml - a bump is a
// deliberate two-file edit, there and here). The decoder options below must
// admit a window this large or the frame is rejected.
const patchWindowLog = 31

// dataAssetName is the release asset that makes a release a DATA release: the
// gzipped SQLite artifact. Release selection keys on its presence; its checksum
// sibling is dataAssetName + ".sha256".
const dataAssetName = "meta.sqlite.gz"

// patchAssetName is the release-asset naming convention for the binary delta
// based on fromTag's artifact.
func patchAssetName(fromTag string) string {
	return "meta.sqlite.patch.from-" + fromTag + ".zst"
}

// expectedDigest parses a `sha256sum`-format checksum file, returning its first
// whitespace-separated field as the expected lowercase hex digest.
func expectedDigest(checksumFile []byte) (string, error) {
	fields := strings.Fields(string(checksumFile))
	if len(fields) == 0 {
		return "", fmt.Errorf("checksum file is empty")
	}
	return strings.ToLower(fields[0]), nil
}

// verifyChecksum checks data against a `sha256sum`-format checksum file.
func verifyChecksum(data, checksumFile []byte) error {
	want, err := expectedDigest(checksumFile)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	if got != want {
		return fmt.Errorf("sha256 mismatch: got %s, want %s", got, want)
	}
	return nil
}

// installVerified streams src into dstPath atomically (temp file + rename in
// dstPath's directory). When wantHexDigest is non-empty, the streamed bytes'
// sha256 must equal it or nothing is installed. Any failure removes the temp
// file and never creates dstPath. Returns the number of bytes written.
func installVerified(src io.Reader, dstPath, wantHexDigest string) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
		return 0, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dstPath), ".meta-*.tmp")
	if err != nil {
		return 0, err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	// Only pay for hashing when a digest will actually be checked.
	hasher := sha256.New()
	var w io.Writer = tmp
	if wantHexDigest != "" {
		w = io.MultiWriter(tmp, hasher)
	}
	n, err := io.Copy(w, src) //nolint:gosec // trusted release artifact: the caller verified the compressed bytes, or the digest gate below rejects the output
	if err != nil {
		_ = tmp.Close()
		return 0, err
	}
	if err := tmp.Close(); err != nil {
		return 0, err
	}
	if wantHexDigest != "" {
		got := hex.EncodeToString(hasher.Sum(nil))
		if got != wantHexDigest {
			return 0, fmt.Errorf("sha256 mismatch: got %s, want %s", got, wantHexDigest)
		}
	}
	if err := os.Rename(tmpName, dstPath); err != nil {
		return 0, err
	}
	return n, nil
}

// gunzipTo decompresses gzData into a new file at path (atomically via a temp
// file + rename). No digest is enforced here: the caller verifies the gz bytes
// themselves (verifyChecksum) - existing releases publish no raw-file digest.
func gunzipTo(gzData []byte, path string) error {
	zr, err := gzip.NewReader(bytes.NewReader(gzData))
	if err != nil {
		return err
	}
	defer func() { _ = zr.Close() }()
	_, err = installVerified(zr, path, "")
	return err
}

// applyPatch reconstructs the new artifact by applying a zstd --patch-from delta
// to the previously-loaded artifact, verifies the result's sha256 against
// wantHexDigest (the published raw-sqlite digest), and only then atomically
// installs it at dstPath. Any failure removes the temp and never creates
// dstPath. Returns the reconstructed artifact's size in bytes.
//
// Two decoder details are load-bearing:
//   - The zstd CLI stamps NO dictionary id on a --patch-from frame, so the
//     previous artifact must be registered as a RAW content dictionary under id
//     0 (WithDecoderDictRaw(0, prev)) - that is the CLI's --patch-from
//     convention, matched here.
//   - The release step compresses with --long=31 (patchWindowLog), whose 2 GiB
//     window exceeds the decoder's defaults, so both the max window and max
//     memory are raised to match or the decode rejects the frame.
//
// The whole previous artifact is held in memory for the duration of one apply.
// That is bounded (one artifact) and happens only on a refresh, never per
// request.
func applyPatch(patch []byte, prevPath, dstPath, wantHexDigest string) (int64, error) {
	prev, err := os.ReadFile(prevPath) //nolint:gosec // prevPath is our own cache file, not user input
	if err != nil {
		return 0, err
	}
	reader, err := zstd.NewReader(bytes.NewReader(patch),
		zstd.WithDecoderDictRaw(0, prev),
		zstd.WithDecoderMaxWindow(1<<patchWindowLog),
		zstd.WithDecoderMaxMemory(1<<patchWindowLog),
		zstd.WithDecoderConcurrency(1), // single sequential decode; lowest memory
	)
	if err != nil {
		return 0, err
	}
	defer reader.Close()
	return installVerified(reader, dstPath, wantHexDigest)
}

// dbCachePath is where the artifact for a release tag is materialized on disk.
// Shared by the full and patch paths so their target filenames can never drift.
func (s *Server) dbCachePath(tag string) string {
	cache := s.cfg.CacheDir
	if cache == "" {
		cache = "./cache"
	}
	safeTag := strings.NewReplacer("/", "-", " ", "-").Replace(tag)
	return filepath.Join(cache, "meta-"+safeTag+".sqlite")
}

// adopt opens the artifact at dbPath as the snapshot for tag and hot-swaps it
// in. Shared tail of the full and patch refresh paths.
func (s *Server) adopt(dbPath, tag string) (*snapshot, error) {
	snap, err := openSnapshot(dbPath, tag)
	if err != nil {
		return nil, err
	}
	s.swap(snap)
	s.loaded = tag
	return snap, nil
}

// refresh fetches the newest data release; if it is newer than the loaded one,
// it adopts it, preferring a small binary delta against the currently-loaded
// artifact and falling back to a full download. It is a no-op on 304 or when the
// loaded tag already matches. Serialized by s.mu (also the only writer of
// s.loaded / the snapshot path, so tryPatch may read s.current().path safely).
func (s *Server) refresh(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	rel, notModified, err := s.gh.latestDataRelease(ctx)
	if err != nil {
		return err
	}
	if notModified {
		return nil
	}
	if rel.TagName != "" && rel.TagName == s.loaded {
		return nil
	}

	if err := s.tryPatch(ctx, rel); err != nil {
		// A dead context means shutdown, not a patch problem - don't log a
		// misleading fallback or attempt a doomed full download.
		if ctx.Err() != nil {
			s.gh.forget()
			return err
		}
		s.log.Printf("serve: patch refresh unavailable (%v); falling back to full download", err)
		if err := s.fullRefresh(ctx, rel); err != nil {
			// The 200 above already stored the new ETag; forget it so the next
			// poll retries this release instead of 304-ing until the one after.
			s.gh.forget()
			return err
		}
	}
	return nil
}

// fullRefresh downloads meta.sqlite.gz, verifies it against meta.sqlite.gz.sha256,
// gunzips it into the cache, and hot-swaps the snapshot. This is the universal
// path: it works for the first refresh and whenever a patch is unavailable.
func (s *Server) fullRefresh(ctx context.Context, rel *ghRelease) error {
	gzData, err := s.downloadAsset(ctx, rel, dataAssetName)
	if err != nil {
		return err
	}
	sumData, err := s.downloadAsset(ctx, rel, dataAssetName+".sha256")
	if err != nil {
		return err
	}
	if err := verifyChecksum(gzData, sumData); err != nil {
		return err
	}

	dbPath := s.dbCachePath(rel.TagName)
	if err := gunzipTo(gzData, dbPath); err != nil {
		return err
	}
	snap, err := s.adopt(dbPath, rel.TagName)
	if err != nil {
		return err
	}
	s.log.Printf("serve: loaded release %s (%d works, built %s)", rel.TagName, snap.stats.Works, snap.stats.BuiltAt)
	return nil
}

// tryPatch attempts an incremental refresh: apply the release's zstd
// --patch-from delta (based on the currently-loaded artifact) to reconstruct the
// new sqlite, verified byte-for-byte against meta.sqlite.sha256. It returns a
// descriptive error on every bail-out so refresh() can log the reason and fall
// back to a full download; it never swaps a snapshot unless the patched artifact
// verifies. metabuild is deterministic, so the patched file is bit-identical to
// what a full download would produce.
func (s *Server) tryPatch(ctx context.Context, rel *ghRelease) error {
	// The loaded snapshot is the single source of truth for the patch base: its
	// tag names the asset to request and its path is the dictionary, so the two
	// can never diverge. cur is nil on a poll-only boot's first refresh; an
	// empty tag is a local --db artifact - both always take the full path.
	cur := s.current()
	if cur == nil || cur.tag == "" {
		return fmt.Errorf("no loaded release tag (first refresh is always full)")
	}
	if _, err := os.Stat(cur.path); err != nil {
		return fmt.Errorf("loaded artifact unavailable: %w", err)
	}
	// The most common bail-out - the server is 2+ releases behind, so no delta
	// is based on our tag - must cost zero HTTP requests.
	patchName := patchAssetName(cur.tag)
	if _, ok := findAsset(rel, patchName); !ok {
		return fmt.Errorf("release %s has no %s asset", rel.TagName, patchName)
	}

	// Fetch and parse the tiny raw-file checksum first, so a release that can't
	// verify a patch fails fast without spending the patch download.
	sumData, err := s.downloadAsset(ctx, rel, "meta.sqlite.sha256")
	if err != nil {
		return err
	}
	want, err := expectedDigest(sumData)
	if err != nil {
		return err
	}
	patchData, err := s.downloadAsset(ctx, rel, patchName)
	if err != nil {
		return err
	}

	dstPath := s.dbCachePath(rel.TagName)
	artifactBytes, err := applyPatch(patchData, cur.path, dstPath, want)
	if err != nil {
		return err
	}
	snap, err := s.adopt(dstPath, rel.TagName)
	if err != nil {
		return err
	}
	s.log.Printf("serve: patched %s -> %s (patch %d bytes, artifact %d bytes; %d works, built %s)",
		cur.tag, rel.TagName, len(patchData), artifactBytes, snap.stats.Works, snap.stats.BuiltAt)
	return nil
}

// pollLoop refreshes immediately at startup and then every cfg.Interval until
// ctx is cancelled. Failures are logged and retried on the next poll; a poll
// never crashes the serving process.
//
// The immediate first refresh exists because the production boot is a baked
// --db artifact AND --poll: New() loads the image-build-time DB and, since
// DBPath != "", does NOT do the synchronous first refresh (that path is
// poll-only). Without an immediate poll a recreated container would serve
// stale, build-time data for one full Interval (an hour by default) - seen live
// on meta.audiosilo.app. On a poll-only boot New() already refreshed
// synchronously and stored the ETag, so this immediate refresh is a cheap
// conditional 304 - harmless. refresh() is ctx-bound, so a slow first download
// aborts on shutdown rather than delaying it. The wait uses time.After, not a
// Ticker, so the interval is measured from the END of the previous refresh - a
// refresh slower than a short Interval can never pend a tick and fire again
// back-to-back.
func (s *Server) pollLoop(ctx context.Context) {
	for {
		// A refresh cut short by shutdown is not a poll failure - stay silent.
		if err := s.refresh(ctx); err != nil && ctx.Err() == nil {
			s.log.Printf("serve: poll refresh failed: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(s.cfg.Interval):
		}
	}
}
