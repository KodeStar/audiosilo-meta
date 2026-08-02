package serve

import (
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/klauspost/compress/zstd"
)

// The request deadlines. They are deliberately NOT one http.Client.Timeout:
// that is a deadline on the whole request, body included, so a single number
// has to cover both a tiny JSON document and an artifact download that is
// hundreds of MB post-seed. Sized for the JSON it aborts healthy long
// downloads - and since the image bakes no artifact, an aborted download is a
// container that never becomes ready, forever. Sized for the artifact it lets a
// hung metadata request sit for the same long time. So the two are split, and
// the artifact download is policed by PROGRESS rather than by total duration.
const (
	// metadataTimeout bounds the release-metadata request end to end. It is a
	// small JSON document: a slow one is a broken one.
	metadataTimeout = 30 * time.Second

	// responseHeaderTimeout bounds the wait for response HEADERS on every
	// request, asset downloads included. It is what catches a connection that is
	// accepted and then never answered, which is otherwise the one failure the
	// stall watchdog below cannot see (no body has started).
	responseHeaderTimeout = 30 * time.Second

	// assetDeadline is the absolute ceiling on ONE asset download. It is not a
	// service-level expectation but a runaway guard: a slow-but-healthy link may
	// legitimately need many minutes for the artifact, and aborting that is worse
	// than waiting for it.
	assetDeadline = 60 * time.Minute

	// assetStallTimeout is how long a download may go without delivering a
	// single byte before it is abandoned. This - not a whole-request deadline -
	// is what tells a dead transfer from a slow one, so a healthy download of any
	// size finishes while a stalled one fails in a minute.
	assetStallTimeout = 60 * time.Second
)

// ghClient talks to the GitHub Releases API, remembering the last ETag so a
// poll that finds nothing new costs one conditional request.
type ghClient struct {
	base  string // API base, "https://api.github.com" (overridable for tests)
	repo  string // "owner/name"
	token string // optional
	http  *http.Client
	etag  string

	// The deadlines above, as fields so tests can scale them down.
	metaTimeout   time.Duration // whole-request deadline for the metadata call
	assetDeadline time.Duration // whole-request ceiling for an asset download
	stallTimeout  time.Duration // no-progress watchdog for an asset download
}

func newGHClient(repo, token, base string) *ghClient {
	if base == "" {
		base = "https://api.github.com"
	}
	// The client carries NO Timeout (see the deadline constants); the per-request
	// contexts do that job. The transport still bounds the pre-body phase.
	tr, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		tr = &http.Transport{}
	}
	tr = tr.Clone()
	tr.ResponseHeaderTimeout = responseHeaderTimeout
	return &ghClient{
		base:          strings.TrimRight(base, "/"),
		repo:          repo,
		token:         token,
		http:          &http.Client{Transport: tr},
		metaTimeout:   metadataTimeout,
		assetDeadline: assetDeadline,
		stallTimeout:  assetStallTimeout,
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
// latestDataRelease).
//
// The window is a BOOT-SURVIVAL concern, not just a freshness one: the image
// bakes no artifact, so if enough code/image releases ever pushed every data
// release out of these 15, a fresh container would find nothing to load. It
// would not crash - it comes up degraded, falls back to whatever the cache
// volume holds and otherwise answers 503 (see New) - but it would stay there,
// because every retry scans the same exhausted window. A wider window (or
// pagination) is the fix if the cadence of code releases ever approaches this.
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
	ctx, cancel := context.WithTimeout(ctx, c.metaTimeout)
	defer cancel()
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

// get issues the authenticated asset GET. The caller owns (and must close) the
// response body - closing it is also what releases the request context, so a
// caller that forgets leaks a context until assetDeadline.
//
// The download is bounded by assetDeadline (a runaway guard) and, far more
// tightly, by a no-progress watchdog: see stallGuard.
func (c *ghClient) get(ctx context.Context, url string) (*http.Response, error) {
	ctx, cancel := context.WithTimeout(ctx, c.assetDeadline)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		cancel()
		return nil, err
	}
	req.Header.Set("Accept", "application/octet-stream")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		cancel()
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		cancel()
		return nil, fmt.Errorf("download %s: %s", url, resp.Status)
	}
	resp.Body = newStallGuard(resp.Body, c.stallTimeout, cancel)
	return resp, nil
}

// stallGuard wraps a download body with a no-progress watchdog and owns the
// request context's cancel func (released on Close, so the guard is also what
// makes the deadline collectable).
//
// It is the piece that lets the artifact download have a generous deadline
// without also tolerating a dead connection for that long: every byte delivered
// resets the timer, so a slow-but-steady transfer of any size runs to
// completion, while one that goes quiet for stallTimeout is cancelled. The
// cancellation surfaces to the reader as a context error, which says nothing
// about why, so Read rewrites it into the real reason.
type stallGuard struct {
	body    io.ReadCloser
	timeout time.Duration
	timer   *time.Timer
	cancel  context.CancelFunc
	stalled atomic.Bool
}

func newStallGuard(body io.ReadCloser, timeout time.Duration, cancel context.CancelFunc) *stallGuard {
	g := &stallGuard{body: body, timeout: timeout, cancel: cancel}
	g.timer = time.AfterFunc(timeout, func() {
		g.stalled.Store(true)
		cancel() // kills the in-flight Read
	})
	return g
}

func (g *stallGuard) Read(p []byte) (int, error) {
	n, err := g.body.Read(p)
	if n > 0 {
		g.timer.Reset(g.timeout)
	}
	if err != nil && g.stalled.Load() {
		return n, fmt.Errorf("download stalled: no data for %s", g.timeout)
	}
	return n, err
}

func (g *stallGuard) Close() error {
	g.timer.Stop()
	err := g.body.Close()
	g.cancel()
	return err
}

// maxSmallAssetBytes bounds the assets that are read into memory. Only the
// `sha256sum`-format checksum files take that path (about 80 bytes each); the
// artifact and the patch are streamed to disk, so nothing unbounded is ever
// buffered.
const maxSmallAssetBytes = 4 << 10

// downloadSmall GETs url and returns the body, refusing anything larger than
// maxSmallAssetBytes.
func (c *ghClient) downloadSmall(ctx context.Context, url string) ([]byte, error) {
	resp, err := c.get(ctx, url)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSmallAssetBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxSmallAssetBytes {
		return nil, fmt.Errorf("download %s: larger than %d bytes, expected a checksum file", url, maxSmallAssetBytes)
	}
	return body, nil
}

// downloadTo streams url straight into dstPath (temp file + rename, never
// through memory) and, when wantHexDigest is non-empty, refuses to install it
// unless the streamed bytes hash to it. This is the path every artifact-sized
// asset takes: post-seed the gz is hundreds of MB and the raw artifact ~10x
// today's, so buffering a whole asset would put that on the heap of a serving
// process.
func (c *ghClient) downloadTo(ctx context.Context, url, dstPath, wantHexDigest string) (int64, error) {
	resp, err := c.get(ctx, url)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	return installVerified(resp.Body, dstPath, wantHexDigest)
}

func findAsset(rel *ghRelease, name string) (string, bool) {
	for _, a := range rel.Assets {
		if a.Name == name {
			return a.DownloadURL, true
		}
	}
	return "", false
}

// smallAsset finds the named asset on rel and reads it into memory (checksum
// files only - see maxSmallAssetBytes).
func (s *Server) smallAsset(ctx context.Context, rel *ghRelease, name string) ([]byte, error) {
	url, ok := findAsset(rel, name)
	if !ok {
		return nil, fmt.Errorf("release %s has no %s asset", rel.TagName, name)
	}
	return s.gh.downloadSmall(ctx, url)
}

// assetTo finds the named asset on rel and streams it to dstPath, verified
// against wantHexDigest when one is given.
func (s *Server) assetTo(ctx context.Context, rel *ghRelease, name, dstPath, wantHexDigest string) (int64, error) {
	url, ok := findAsset(rel, name)
	if !ok {
		return 0, fmt.Errorf("release %s has no %s asset", rel.TagName, name)
	}
	return s.gh.downloadTo(ctx, url, dstPath, wantHexDigest)
}

// assetBody finds the named asset on rel and opens its body for streaming. The
// caller owns (and must close) the returned reader. Used by the path that
// transforms an asset while it downloads rather than landing it as a file.
func (s *Server) assetBody(ctx context.Context, rel *ghRelease, name string) (io.ReadCloser, error) {
	url, ok := findAsset(rel, name)
	if !ok {
		return nil, fmt.Errorf("release %s has no %s asset", rel.TagName, name)
	}
	resp, err := s.gh.get(ctx, url)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
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

// rawAssetName names the UNCOMPRESSED artifact. No such asset is published -
// only its checksum is (rawAssetName + ".sha256"), which is what the patch path
// and the cache-adoption path verify a reconstructed or already-present file
// against.
const rawAssetName = "meta.sqlite"

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

// installStream streams src into dstPath atomically (temp file + rename in
// dstPath's directory). When verify is non-nil it runs AFTER the copy and BEFORE
// the rename: an error from it means nothing is installed. Any failure removes
// the temp file and never creates dstPath. Returns the number of bytes written.
//
// The gate is a callback rather than a digest because the bytes that must be
// verified are not always the bytes being written: fullRefresh decompresses
// while it downloads, so what it can check against the published checksum is the
// COMPRESSED input, not the artifact landing on disk.
func installStream(src io.Reader, dstPath string, verify func() error) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
		return 0, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dstPath), ".meta-*.tmp")
	if err != nil {
		return 0, err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	n, err := io.Copy(tmp, src) //nolint:gosec // trusted release artifact: verify below gates the rename, so unverified bytes never become dstPath
	if err != nil {
		_ = tmp.Close()
		return 0, err
	}
	if err := tmp.Close(); err != nil {
		return 0, err
	}
	if verify != nil {
		if err := verify(); err != nil {
			return 0, err
		}
	}
	if err := os.Rename(tmpName, dstPath); err != nil {
		return 0, err
	}
	return n, nil
}

// checkDigest compares an accumulated hash against an expected lowercase hex
// digest.
func checkDigest(h hash.Hash, wantHexDigest string) error {
	got := hex.EncodeToString(h.Sum(nil))
	if got != wantHexDigest {
		return fmt.Errorf("sha256 mismatch: got %s, want %s", got, wantHexDigest)
	}
	return nil
}

// installVerified streams src into dstPath atomically, requiring the streamed
// bytes to hash to wantHexDigest (empty = no digest check) before anything is
// installed.
func installVerified(src io.Reader, dstPath, wantHexDigest string) (int64, error) {
	if wantHexDigest == "" {
		return installStream(src, dstPath, nil)
	}
	h := sha256.New()
	return installStream(io.TeeReader(src, h), dstPath, func() error {
		return checkDigest(h, wantHexDigest)
	})
}

// gunzipStreamTo decompresses src into dstPath in ONE pass, verifying the
// COMPRESSED bytes against wantGzDigest before the result is installed. This is
// what lets fullRefresh go from the network straight to the finished artifact:
// no intermediate .gz file, and neither the compressed nor the decompressed
// bytes are ever held in memory.
//
// The digest is taken at the tee, i.e. over everything read from src, and the
// reader is drained before the check so trailing bytes cannot be skipped by the
// gzip reader stopping at its trailer. installStream runs the check before the
// rename, so a corrupted download is discarded with the temp file - the same
// "verified before it counts" property the old download-then-compare had.
func gunzipStreamTo(src io.Reader, dstPath, wantGzDigest string) error {
	h := sha256.New()
	tee := io.TeeReader(src, h)
	zr, err := gzip.NewReader(bufio.NewReaderSize(tee, downloadBufferBytes))
	if err != nil {
		return err
	}
	defer func() { _ = zr.Close() }()
	_, err = installStream(zr, dstPath, func() error {
		if _, err := io.Copy(io.Discard, tee); err != nil {
			return err
		}
		return checkDigest(h, wantGzDigest)
	})
	return err
}

// downloadBufferBytes is the read buffer for the on-disk decompression paths -
// large enough that a multi-hundred-MB artifact is not read in 4KB syscalls,
// small enough to be irrelevant to the process's footprint.
const downloadBufferBytes = 1 << 20

// applyPatchFile reconstructs the new artifact by applying the zstd
// --patch-from delta at patchPath to the previously-loaded artifact, verifies
// the result's sha256 against wantHexDigest (the published raw-sqlite digest),
// and only then atomically installs it at dstPath. Any failure removes the temp
// and never creates dstPath. Returns the reconstructed artifact's size in bytes.
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
// The patch itself and the reconstructed output both stream (file in, file out),
// but the PREVIOUS artifact is unavoidably held in memory: a raw zstd dictionary
// must be one contiguous byte slice, and the decoder's history window over it
// costs about as much again, so peak transient is roughly twice the base
// artifact. That is what defaultMaxPatchBase caps - past that size tryPatch
// refuses to start and the full download (which streams end to end) runs instead.
func applyPatchFile(patchPath, prevPath, dstPath, wantHexDigest string) (int64, error) {
	prev, err := os.ReadFile(prevPath) //nolint:gosec // prevPath is our own cache file, not user input
	if err != nil {
		return 0, err
	}
	patch, err := os.Open(patchPath) //nolint:gosec // patchPath is our own cache file, not user input
	if err != nil {
		return 0, err
	}
	defer func() { _ = patch.Close() }()
	reader, err := zstd.NewReader(bufio.NewReaderSize(patch, downloadBufferBytes),
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

// cachePrefix names every file this server writes into the cache directory: the
// artifacts (meta-<tag>.sqlite), their transient siblings (.gz, .patch.zst) and
// installVerified's temp files (.meta-*.tmp). pruneCache only ever removes files
// matching it, so a cache directory shared with anything else is left alone.
const cachePrefix = "meta-"

// artifactSuffix completes a cached artifact's file name (cachePrefix + tag +
// artifactSuffix). Only the finished artifacts carry it: the transient siblings
// end in .gz / .patch.zst and the temp files in .tmp, so it is what tells an
// adoptable file from work in progress.
const artifactSuffix = ".sqlite"

// cacheDir is where downloaded artifacts are materialized.
func (s *Server) cacheDir() string {
	if s.cfg.CacheDir == "" {
		return "./cache"
	}
	return s.cfg.CacheDir
}

// dbCachePath is where the artifact for a release tag is materialized on disk.
// Shared by the full and patch paths so their target filenames can never drift.
func (s *Server) dbCachePath(tag string) string {
	return filepath.Join(s.cacheDir(), cachePrefix+cacheTagName(tag)+artifactSuffix)
}

// cacheTagName renders a release tag as a file-name component.
var cacheTagName = strings.NewReplacer("/", "-", " ", "-").Replace

// tagFromCacheName reads a tag back out of a cache file name, or returns ""
// when the name is not a finished artifact of ours. It is the inverse of
// dbCachePath for every tag this repo cuts (`data-vYYYY.MM.DD-<sha>` contains
// neither a slash nor a space); a tag that did contain one would come back
// substituted, which costs nothing but a missed patch base - the next refresh
// then takes the full path.
func tagFromCacheName(name string) string {
	if !strings.HasPrefix(name, cachePrefix) || !strings.HasSuffix(name, artifactSuffix) {
		return ""
	}
	return strings.TrimSuffix(strings.TrimPrefix(name, cachePrefix), artifactSuffix)
}

// newestCachedArtifact returns the most recently modified finished artifact on
// the cache volume and the tag its name encodes. This is what a boot that
// cannot reach GitHub falls back to (see Server.adoptStaleCache): the volume
// outlives the container, so it usually holds the artifact the previous one was
// serving.
func (s *Server) newestCachedArtifact() (path, tag string, ok bool) {
	entries, err := os.ReadDir(s.cacheDir())
	if err != nil {
		return "", "", false
	}
	var newest time.Time
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		t := tagFromCacheName(e.Name())
		if t == "" {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if path == "" || info.ModTime().After(newest) {
			path, tag, newest = filepath.Join(s.cacheDir(), e.Name()), t, info.ModTime()
		}
	}
	return path, tag, path != ""
}

// verifyFileDigest hashes an existing file and compares it with an expected
// lowercase hex digest. The download paths verify bytes as they stream; this is
// for the one case where the bytes are already on disk (see cachedRefresh).
func verifyFileDigest(path, wantHexDigest string) error {
	f, err := os.Open(path) //nolint:gosec // our own cache file, not user input
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, bufio.NewReaderSize(f, downloadBufferBytes)); err != nil {
		return err
	}
	return checkDigest(h, wantHexDigest)
}

// defaultMaxPatchBase caps the artifact size at which the patch path is still
// worth taking (see applyPatchFile: the base artifact must be one contiguous
// slice in memory). Peak transient is roughly TWICE the base, not once: the
// contiguous dictionary copy plus the decoder's history window over it. So this
// 1 GiB cap bounds the patch path at about 2 GiB of transient - it leaves the
// 142k-work artifact comfortably inside the patch path while refusing the
// multi-GB shapes where that transient would dominate the serving process.
const defaultMaxPatchBase = 1 << 30

// pruneCacheLocked deletes every cache file this server owns except the live
// artifact, s.cfg.DBPath, and every RETIRED artifact - one superseded by a swap
// whose grace has not elapsed, so requests are still reading it. What it
// collects is superseded releases (one full artifact per adopted release, which
// otherwise accumulate forever on the cache volume) and any .patch.zst/tmp
// leftovers from a refresh that died mid-flight.
//
// It runs at two moments, which together cover the whole lifecycle: from adopt,
// which is the pass that also cleans up after a cold boot (whose first adopt
// supersedes nothing at all); and again once each swap grace elapses, by which
// point that snapshot's file is closed and prunable too.
//
// The keep-set is the whole retired SET rather than the one just-superseded
// snapshot: closes ride a grace timer, so several snapshots can be open at once
// (three adopts inside one grace window), and sparing only the newest of them
// unlinks a file an older, still-serving snapshot needs. SQLite reopens pooled
// connections lazily, so an unlinked path is not harmless: a concurrent query on
// that snapshot fails with "unable to open database file".
//
// The caller must hold s.mu: a refresh in flight owns the transient files it is
// writing, s.retired is refresh-owned state, and the live snapshot's path must
// not move underneath the check.
func (s *Server) pruneCacheLocked() {
	dir := s.cacheDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !os.IsNotExist(err) {
			s.log.Printf("serve: cache prune skipped: %v", err)
		}
		return
	}
	keep := map[string]bool{s.cfg.DBPath: true}
	if cur := s.current(); cur != nil {
		keep[cur.path] = true
	}
	for p := range s.retired {
		keep[p] = true
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, cachePrefix) && !strings.HasPrefix(name, "."+cachePrefix) {
			continue
		}
		// Never delete what we are serving, what a request may still be reading,
		// or a --db artifact a deployment happens to keep in the cache directory.
		path := filepath.Join(dir, name)
		if keep[path] {
			continue
		}
		if err := os.Remove(path); err != nil {
			s.log.Printf("serve: cache prune could not remove %s: %v", path, err)
			continue
		}
		s.log.Printf("serve: pruned superseded cache file %s", path)
	}
}

// adopt opens the artifact at dbPath as the snapshot for tag and hot-swaps it
// in, then prunes the cache. Shared tail of the full and patch refresh paths.
// Always called with s.mu held (refresh owns it), hence pruneCacheLocked.
func (s *Server) adopt(dbPath, tag string) (*snapshot, error) {
	snap, err := openSnapshot(dbPath, tag)
	if err != nil {
		return nil, err
	}
	snap.log = s.log
	s.swap(snap)
	s.loaded = tag
	// Prune on EVERY successful adopt, not only when the grace timer fires, so a
	// cold boot (which supersedes no snapshot) still clears what an earlier
	// container left on the volume. Snapshots still inside their grace are
	// serving in-flight requests, so swap has registered their files as retired
	// and this pass leaves them alone.
	s.pruneCacheLocked()
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

	// Before spending a download: the cache volume may already hold this exact
	// artifact. A restarted container is the common case (the volume outlives
	// it), and a degraded boot that adopted a stale cached file is the other -
	// that one is confirmed here, which is why it does not set s.loaded.
	if err := s.cachedRefresh(ctx, rel); err == nil {
		return nil
	} else if !errors.Is(err, errNoCachedArtifact) && ctx.Err() == nil {
		s.log.Printf("serve: cached artifact for %s unusable (%v); downloading", rel.TagName, err)
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

// errNoCachedArtifact means the cache volume holds no file for the release's
// tag - the ordinary case, and the one refresh() must not log about.
var errNoCachedArtifact = errors.New("no cached artifact for this release")

// cachedRefresh adopts the release's artifact straight off the cache volume,
// without downloading anything but the ~80-byte checksum that proves it is the
// right file. It is the difference between a restarted container re-downloading
// a hundreds-of-MB artifact it already has and it being ready in a second.
//
// Trust is not placed in the file's presence or its name: it is hashed and
// compared against the release's published raw-artifact digest, the same value
// the patch path verifies a reconstruction against. Anything else - absent,
// unreadable, wrong bytes - is an error, and refresh falls through to the patch
// and full paths, which overwrite the file atomically anyway.
func (s *Server) cachedRefresh(ctx context.Context, rel *ghRelease) error {
	dbPath := s.dbCachePath(rel.TagName)
	if _, err := os.Stat(dbPath); err != nil {
		return errNoCachedArtifact
	}
	sumData, err := s.smallAsset(ctx, rel, rawAssetName+".sha256")
	if err != nil {
		return err
	}
	want, err := expectedDigest(sumData)
	if err != nil {
		return err
	}
	if err := verifyFileDigest(dbPath, want); err != nil {
		return err
	}
	snap, err := s.adopt(dbPath, rel.TagName)
	if err != nil {
		return err
	}
	s.log.Printf("serve: adopted cached artifact for %s without downloading (%d works, built %s)",
		rel.TagName, snap.stats.Works, snap.stats.BuiltAt)
	return nil
}

// adoptStaleCache is the last resort of a poll-only boot whose first fetch
// failed: rather than answering 503 with a perfectly good artifact sitting on
// the cache volume, it serves the newest one the previous container left there.
// Reported true when something was adopted.
//
// The data is STALE by definition - nothing has confirmed it against a release -
// so it is logged as such, and s.loaded stays EMPTY on purpose. That is what
// makes the first reachable poll re-check this artifact instead of treating it
// as current; because the file is already on disk, that check costs one checksum
// request and no download (see cachedRefresh). The snapshot still carries the
// tag its file name encodes, so a patch can be based on it in the meantime.
func (s *Server) adoptStaleCache() bool {
	path, tag, ok := s.newestCachedArtifact()
	if !ok {
		return false
	}
	snap, err := openSnapshot(path, tag)
	if err != nil {
		s.log.Printf("serve: cached artifact %s is not usable: %v", path, err)
		return false
	}
	snap.log = s.log
	s.cur.Store(snap)
	s.log.Printf("serve: serving the STALE cached artifact %s (%s, %d works, built %s) until a release loads",
		path, tag, snap.stats.Works, snap.stats.BuiltAt)
	return true
}

// fullRefresh downloads meta.sqlite.gz, verifies it against meta.sqlite.gz.sha256,
// and decompresses it into the cache in ONE streaming pass, then hot-swaps the
// snapshot. This is the universal path: it works for the first refresh and
// whenever a patch is unavailable.
//
// The checksum is fetched FIRST so the download can be gated on it without ever
// materializing the compressed asset: the response body is hashed as it is
// decompressed straight into the artifact's temp file, and gunzipStreamTo checks
// the digest before the rename. So the network transfer, the decompression and
// the verification all happen once, over one pass, with no .gz file on the cache
// volume and neither form of the asset in memory.
func (s *Server) fullRefresh(ctx context.Context, rel *ghRelease) error {
	sumData, err := s.smallAsset(ctx, rel, dataAssetName+".sha256")
	if err != nil {
		return err
	}
	want, err := expectedDigest(sumData)
	if err != nil {
		return err
	}

	body, err := s.assetBody(ctx, rel, dataAssetName)
	if err != nil {
		return err
	}
	defer func() { _ = body.Close() }()

	dbPath := s.dbCachePath(rel.TagName)
	if err := gunzipStreamTo(body, dbPath, want); err != nil {
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
	info, err := os.Stat(cur.path)
	if err != nil {
		return fmt.Errorf("loaded artifact unavailable: %w", err)
	}
	// A raw zstd dictionary has to be one contiguous slice, so the patch base is
	// the one thing on this path that cannot stream. Past the cap the transient
	// costs more than it saves and the full download (which streams end to end)
	// is the better trade - the fallback is unconditional, so refusing here is
	// safe, not a failure.
	if maxBase := s.cfg.maxPatchBase; info.Size() > maxBase {
		return fmt.Errorf("loaded artifact is %d bytes, over the %d-byte patch-base cap", info.Size(), maxBase)
	}
	// The most common bail-out - the server is 2+ releases behind, so no delta
	// is based on our tag - must cost zero HTTP requests.
	patchName := patchAssetName(cur.tag)
	if _, ok := findAsset(rel, patchName); !ok {
		return fmt.Errorf("release %s has no %s asset", rel.TagName, patchName)
	}

	// Fetch and parse the tiny raw-file checksum first, so a release that can't
	// verify a patch fails fast without spending the patch download.
	sumData, err := s.smallAsset(ctx, rel, rawAssetName+".sha256")
	if err != nil {
		return err
	}
	want, err := expectedDigest(sumData)
	if err != nil {
		return err
	}

	dstPath := s.dbCachePath(rel.TagName)
	patchPath := dstPath + ".patch.zst"
	defer func() { _ = os.Remove(patchPath) }()
	patchBytes, err := s.assetTo(ctx, rel, patchName, patchPath, "")
	if err != nil {
		return err
	}

	artifactBytes, err := applyPatchFile(patchPath, cur.path, dstPath, want)
	if err != nil {
		return err
	}
	snap, err := s.adopt(dstPath, rel.TagName)
	if err != nil {
		return err
	}
	s.log.Printf("serve: patched %s -> %s (patch %d bytes, artifact %d bytes; %d works, built %s)",
		cur.tag, rel.TagName, patchBytes, artifactBytes, snap.stats.Works, snap.stats.BuiltAt)
	return nil
}

// pollLoop refreshes immediately at startup and then every cfg.Interval until
// ctx is cancelled. Failures are logged and retried on the next poll; a poll
// never crashes the serving process.
//
// The immediate first refresh matters for any boot that DID load a local
// artifact (a dev --db, or a deployment that keeps one): New() skips the
// poll-only synchronous refresh in that case, so without an immediate poll the
// process would serve that artifact for one full Interval (an hour by default) -
// seen live on meta.audiosilo.app back when the image baked its data. On a
// poll-only boot New() already refreshed successfully and stored the ETag, so
// this immediate refresh is a cheap conditional 304 - harmless.
//
// While NO artifact has loaded at all (a poll-only boot whose first fetch
// failed - the production image ships no baked data), the wait starts at the
// much shorter bootRetry instead of Interval: the server is answering 503 and
// must recover as soon as GitHub does, not an hour later. It then BACKS OFF,
// doubling up to Interval, because this is the normal boot path and a failure
// is not always cheap: a download that dies near the end of a hundreds-of-MB
// artifact costs real bandwidth, and retrying that at full price every 30s
// indefinitely would hammer both ends of an outage that may last hours. The
// backoff resets the moment an artifact loads.
//
// refresh() is ctx-bound, so a slow first download aborts on shutdown rather
// than delaying it. The wait uses time.After, not a Ticker, so the interval is
// measured from the END of the previous refresh - a refresh slower than a short
// Interval can never pend a tick and fire again back-to-back.
func (s *Server) pollLoop(ctx context.Context) {
	var backoff time.Duration
	for {
		// A refresh cut short by shutdown is not a poll failure - stay silent.
		if err := s.refresh(ctx); err != nil && ctx.Err() == nil {
			s.log.Printf("serve: poll refresh failed: %v", err)
		}
		wait := s.cfg.Interval
		if s.current() == nil {
			backoff = nextBootBackoff(backoff, s.cfg.bootRetry, s.cfg.Interval)
			wait = backoff
			// The 503s advertise this wait, so publish it before sleeping on it -
			// a client told to come back in 30s while the loop has backed off to an
			// hour retries pointlessly, 120 times.
			s.nextRetry.Store(int64(wait))
			s.log.Printf("serve: still no artifact; retrying in %s", wait)
		} else {
			backoff = 0
			s.nextRetry.Store(int64(s.cfg.bootRetry))
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// nextBootBackoff returns the next boot-retry wait: base on the first failure,
// then doubling, never exceeding limit (the steady-state poll interval). A base
// already larger than limit is clamped, so the wait is never longer than a
// normal poll.
func nextBootBackoff(prev, base, limit time.Duration) time.Duration {
	next := base
	if prev > 0 {
		next = prev * 2
	}
	if next > limit {
		return limit
	}
	return next
}
