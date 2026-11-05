package serve

// metaserve's refresh orchestration over pkg/release: which data release to
// adopt (release.Client.LatestData, conditional on the ETag this server keeps),
// the CACHE VOLUME (adopting an artifact a previous container left, verified;
// pruning superseded ones), the zstd --patch-from DELTA against the loaded
// artifact, and the full verified download (release.Client.DownloadData) every
// other path falls back to. The asset contract, the host allowlist, the stall
// watchdog and the verified install are pkg/release's, shared with mirror-mode
// AudioSilo servers.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/kodestar/audiosilo-meta/pkg/query"
	"github.com/kodestar/audiosilo-meta/pkg/release"
)

// patchWindowLog is the log2 of the zstd window the release patches are
// compressed with (--long=31 in .github/workflows/release.yml - a bump is a
// deliberate two-file edit, there and here). The decoder options below must
// admit a window this large or the frame is rejected.
const patchWindowLog = 31

// patchAssetName is the release-asset naming convention for the binary delta
// based on fromTag's artifact.
func patchAssetName(fromTag string) string {
	return "meta.sqlite.patch.from-" + fromTag + ".zst"
}

// currentArtifactBytes is the on-disk size of the artifact this server is
// serving, or 0 when there is none or it cannot be stat'd (a boot that has
// loaded nothing yet, the shape tryPatch reads the same way).
func (s *Server) currentArtifactBytes() int64 {
	cur := s.current()
	if cur == nil || cur.Path() == "" {
		return 0
	}
	info, err := os.Stat(cur.Path())
	if err != nil {
		return 0
	}
	return info.Size()
}

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
// maxBytes is the COMPUTED bound on the reconstructed artifact, the shape the full
// download takes (release.DecompressBound): the patch declares no output size,
// and a frame that expanded without limit would fill the cache volume long before
// the sha256 gate could reject it. The caller measures it against the BASE
// artifact's size (the bound's current-artifact arm), the one size this path
// knows exactly.
//
// The patch itself and the reconstructed output both stream (file in, file out),
// but the PREVIOUS artifact is unavoidably held in memory: a raw zstd dictionary
// must be one contiguous byte slice, and the decoder's history window over it
// costs about as much again, so peak transient is roughly twice the base
// artifact. That is what defaultMaxPatchBase caps - past that size tryPatch
// refuses to start and the full download (which streams end to end) runs instead.
func applyPatchFile(patchPath, prevPath, dstPath, wantHexDigest string, maxBytes int64) (int64, error) {
	prev, err := os.ReadFile(prevPath) //nolint:gosec // prevPath is our own cache file, not user input
	if err != nil {
		return 0, err
	}
	patch, err := os.Open(patchPath) //nolint:gosec // patchPath is our own cache file, not user input
	if err != nil {
		return 0, err
	}
	defer func() { _ = patch.Close() }()
	reader, err := zstd.NewReader(bufio.NewReaderSize(patch, patchBufferBytes),
		zstd.WithDecoderDictRaw(0, prev),
		zstd.WithDecoderMaxWindow(1<<patchWindowLog),
		zstd.WithDecoderMaxMemory(1<<patchWindowLog),
		zstd.WithDecoderConcurrency(1), // single sequential decode; lowest memory
	)
	if err != nil {
		return 0, err
	}
	defer reader.Close()
	return release.InstallVerified(reader, dstPath, wantHexDigest, maxBytes)
}

// patchBufferBytes is the patch's read buffer - large enough that a
// hundreds-of-MB delta is not read in 4KB syscalls.
const patchBufferBytes = 1 << 20

// cachePrefix names every file this server writes into the cache directory
// itself: the artifacts (meta-<tag>.sqlite) and their transient siblings (.gz,
// .patch.zst). The verified installs' temp files are pkg/release's
// (release.TempFile). pruneCacheLocked only ever removes files that are one or
// the other, so a cache directory shared with anything else is left alone.
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
		keep[cur.Path()] = true
	}
	for p := range s.retired {
		keep[p] = true
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, cachePrefix) && !release.TempFile(name) {
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
func (s *Server) adopt(dbPath, tag string) (*query.DB, error) {
	snap, err := s.open(dbPath, tag)
	if err != nil {
		return nil, err
	}
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
// s.loaded / the snapshot path, so tryPatch may read s.current().Path() safely).
func (s *Server) refresh(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	rel, etag, notModified, err := s.gh.LatestData(ctx, s.etag)
	// Stored even beside an error: a 200 that lists no data release makes the
	// next poll a cheap 304 until the list changes (see LatestData).
	s.etag = etag
	if err != nil {
		return err
	}
	if notModified {
		return nil
	}
	if rel.Tag != "" && rel.Tag == s.loaded {
		return nil
	}

	// Before spending a download: the cache volume may already hold this exact
	// artifact. A restarted container is the common case (the volume outlives
	// it), and a degraded boot that adopted a stale cached file is the other -
	// that one is confirmed here, which is why it does not set s.loaded.
	if err := s.cachedRefresh(ctx, rel); err == nil {
		return nil
	} else if !errors.Is(err, errNoCachedArtifact) && ctx.Err() == nil {
		s.log.Printf("serve: cached artifact for %s unusable (%v); downloading", rel.Tag, err)
	}

	if err := s.tryPatch(ctx, rel); err != nil {
		// A dead context means shutdown, not a patch problem - don't log a
		// misleading fallback or attempt a doomed full download.
		if ctx.Err() != nil {
			s.forgetETag()
			return err
		}
		s.log.Printf("serve: patch refresh unavailable (%v); falling back to full download", err)
		if err := s.fullRefresh(ctx, rel); err != nil {
			// The 200 above already stored the new ETag; forget it so the next
			// poll retries this release instead of 304-ing until the one after.
			s.forgetETag()
			return err
		}
	}
	return nil
}

// forgetETag drops the remembered ETag so the next poll refetches the release
// metadata unconditionally. Called when a refresh fails AFTER a successful
// (200) metadata fetch - the ETag was already stored, so without this the next
// poll would 304 and never retry the failed download until another release is
// cut. The caller holds s.mu.
func (s *Server) forgetETag() { s.etag = "" }

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
func (s *Server) cachedRefresh(ctx context.Context, rel *release.Release) error {
	dbPath := s.dbCachePath(rel.Tag)
	if _, err := os.Stat(dbPath); err != nil {
		return errNoCachedArtifact
	}
	want, err := s.gh.ReadDigest(ctx, rel, release.RawDigestAsset)
	if err != nil {
		return err
	}
	if err := release.VerifyFile(dbPath, want); err != nil {
		return err
	}
	snap, err := s.adopt(dbPath, rel.Tag)
	if err != nil {
		return err
	}
	st := snap.Stats()
	s.log.Printf("serve: adopted cached artifact for %s without downloading (%d works, built %s)",
		rel.Tag, st.Works, st.BuiltAt)
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
	snap, err := s.open(path, tag)
	if err != nil {
		s.log.Printf("serve: cached artifact %s is not usable: %v", path, err)
		return false
	}
	s.cur.Store(snap)
	st := snap.Stats()
	s.log.Printf("serve: serving the STALE cached artifact %s (%s, %d works, built %s) until a release loads",
		path, tag, st.Works, st.BuiltAt)
	return true
}

// fullRefresh downloads the release's artifact (release.Client.DownloadData:
// meta.sqlite.gz streamed through gunzip into the cache in ONE pass, verified
// against meta.sqlite.gz.sha256 before it is installed, so no .gz ever lands on
// the cache volume and neither form is held in memory), then hot-swaps the
// snapshot. This is the universal path: it works for the first refresh and
// whenever a patch is unavailable - including when meta.sqlite.sha256, which
// gates only the cache and patch paths, is the broken asset.
func (s *Server) fullRefresh(ctx context.Context, rel *release.Release) error {
	dbPath := s.dbCachePath(rel.Tag)
	if _, err := s.gh.DownloadData(ctx, rel, dbPath, nil); err != nil {
		return err
	}
	snap, err := s.adopt(dbPath, rel.Tag)
	if err != nil {
		return err
	}
	st := snap.Stats()
	s.log.Printf("serve: loaded release %s (%d works, built %s)", rel.Tag, st.Works, st.BuiltAt)
	return nil
}

// tryPatch attempts an incremental refresh: apply the release's zstd
// --patch-from delta (based on the currently-loaded artifact) to reconstruct the
// new sqlite, verified byte-for-byte against meta.sqlite.sha256. It returns a
// descriptive error on every bail-out so refresh() can log the reason and fall
// back to a full download; it never swaps a snapshot unless the patched artifact
// verifies. metabuild is deterministic, so the patched file is bit-identical to
// what a full download would produce.
func (s *Server) tryPatch(ctx context.Context, rel *release.Release) error {
	// The loaded snapshot is the single source of truth for the patch base: its
	// tag names the asset to request and its path is the dictionary, so the two
	// can never diverge. cur is nil on a poll-only boot's first refresh; an
	// empty tag is a local --db artifact - both always take the full path.
	cur := s.current()
	var curTag string
	if cur != nil {
		curTag = cur.Info().Tag
	}
	if curTag == "" {
		return fmt.Errorf("no loaded release tag (first refresh is always full)")
	}
	info, err := os.Stat(cur.Path())
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
	patchName := patchAssetName(curTag)
	if _, ok := rel.Asset(patchName); !ok {
		return fmt.Errorf("release %s has no %s asset", rel.Tag, patchName)
	}

	// Fetch and parse the tiny raw-file checksum first, so a release that can't
	// verify a patch fails fast without spending the patch download.
	want, err := s.gh.ReadDigest(ctx, rel, release.RawDigestAsset)
	if err != nil {
		return err
	}

	dstPath := s.dbCachePath(rel.Tag)
	patchPath := dstPath + ".patch.zst"
	defer func() { _ = os.Remove(patchPath) }()
	// Bounded by the patch asset's DECLARED size: nothing is decompressed on the
	// way to disk, so a body past its own declaration is refused.
	patchBytes, err := s.gh.DownloadAsset(ctx, rel, patchName, patchPath)
	if err != nil {
		return err
	}

	// The base artifact's size, stat'd above, is what the reconstruction is
	// measured against - the one size this path knows exactly. The patch asset's
	// own declared size says nothing about the OUTPUT, so it is not the declared
	// arm here; the base goes in as the current-artifact arm instead.
	artifactBytes, err := applyPatchFile(patchPath, cur.Path(), dstPath, want,
		release.DecompressBound(0, info.Size()))
	if err != nil {
		return err
	}
	snap, err := s.adopt(dstPath, rel.Tag)
	if err != nil {
		return err
	}
	st := snap.Stats()
	s.log.Printf("serve: patched %s -> %s (patch %d bytes, artifact %d bytes; %d works, built %s)",
		curTag, rel.Tag, patchBytes, artifactBytes, st.Works, st.BuiltAt)
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
