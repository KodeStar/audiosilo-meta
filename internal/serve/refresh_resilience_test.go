package serve

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// knobGitHub is a releases endpoint with the knobs these tests need: it can go
// down, count per-asset downloads, deliver an asset in timed chunks (slow but
// perfectly healthy) and stop dead in the middle of one (a stalled transfer).
// fakeGitHub in github_test.go covers the well-behaved cases; this one exists
// for the failure shapes, so neither fake has to grow the other's knobs.
type knobGitHub struct {
	srv *httptest.Server

	mu      sync.Mutex
	tag     string
	assets  map[string][]byte
	hits    map[string]int
	down    bool
	chunk   int           // >0: deliver in chunks of this many bytes
	pace    time.Duration // delay between chunks
	hang    string        // asset that delivers half and then goes quiet
	release chan struct{} // closed at cleanup so a hung handler can return
}

func newKnobGitHub(t *testing.T, tag string, assets map[string][]byte) *knobGitHub {
	t.Helper()
	f := &knobGitHub{tag: tag, assets: assets, hits: map[string]int{}, release: make(chan struct{})}
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/owner/name/releases", func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.down {
			http.Error(w, "github is down", http.StatusInternalServerError)
			return
		}
		rel := ghRelease{TagName: f.tag, PublishedAt: time.Now()}
		for name := range f.assets {
			rel.Assets = append(rel.Assets, ghAsset{Name: name, DownloadURL: f.srv.URL + "/dl/" + f.tag + "/" + name})
		}
		_ = json.NewEncoder(w).Encode([]ghRelease{rel})
	})
	mux.HandleFunc("/dl/", func(w http.ResponseWriter, r *http.Request) {
		_, name, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/dl/"), "/")
		f.mu.Lock()
		data, ok := f.assets[name]
		chunk, pace, hang := f.chunk, f.pace, f.hang == name
		if ok {
			f.hits[name]++
		}
		f.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		flush := func() {
			if fl, ok := w.(http.Flusher); ok {
				fl.Flush()
			}
		}
		if hang {
			_, _ = w.Write(data[:len(data)/2])
			flush()
			select { // a connection that is up and simply never delivers again
			case <-r.Context().Done():
			case <-f.release:
			}
			return
		}
		if chunk > 0 {
			for off := 0; off < len(data); off += chunk {
				end := min(off+chunk, len(data))
				if _, err := w.Write(data[off:end]); err != nil {
					return
				}
				flush()
				time.Sleep(pace)
			}
			return
		}
		_, _ = w.Write(data)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(func() { close(f.release); f.srv.Close() })
	return f
}

func (f *knobGitHub) publish(tag string, assets map[string][]byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tag, f.assets, f.hits = tag, assets, map[string]int{}
}

func (f *knobGitHub) setDown(v bool)     { f.mu.Lock(); f.down = v; f.mu.Unlock() }
func (f *knobGitHub) hangOn(name string) { f.mu.Lock(); f.hang = name; f.mu.Unlock() }

func (f *knobGitHub) throttle(chunk int, pace time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.chunk, f.pace = chunk, pace
}

func (f *knobGitHub) hitCount(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits[name]
}

// knobServer seeds a server from a local artifact (so New does not poll) and
// points it at the knob fake.
func knobServer(t *testing.T, seed, cache string, f *knobGitHub, grace time.Duration) *Server {
	t.Helper()
	srv, err := New(Config{DBPath: seed, Repo: "owner/name", CacheDir: cache, swapGrace: grace})
	if err != nil {
		t.Fatal(err)
	}
	srv.gh = newGHClient("owner/name", "", f.srv.URL)
	return srv
}

// cacheNames lists the cache directory.
func cacheNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func hasTempFile(names []string) bool {
	for _, n := range names {
		if strings.HasSuffix(n, ".tmp") {
			return true
		}
	}
	return false
}

// ---- (1) the prune must spare every snapshot still inside its grace ---------

// v3Catalog is a third distinct artifact, so three consecutive adopts each land
// on their own cache file.
func v3Catalog() *model.Catalog {
	cat := v2Catalog()
	cat.Works = append(cat.Works, &model.Work{
		ID: "artemis", Title: "Artemis", Language: "en",
		Authors: []string{"andy-weir"}, License: "CC0-1.0",
	})
	return cat
}

const tagR3 = "data-v2026.07.13-r3"

// TestPruneSparesEveryArtifactStillInGrace drives THREE adopts inside one swap
// grace window. Sparing only the immediately-superseded snapshot unlinks the
// first one's artifact while its handle is still open and, by the grace
// contract, still serving in-flight requests - and because SQLite opens pooled
// connections lazily, the very next concurrent query on it fails with "unable to
// open database file".
func TestPruneSparesEveryArtifactStillInGrace(t *testing.T) {
	v1Path, v1, _, v2 := buildV1V2(t)
	v3 := readDB(t, buildFixtureDB(t, v3Catalog()))
	cache := t.TempDir()

	f := newKnobGitHub(t, tagR1, makeAssets(t, v1, "", nil))
	// A grace long enough that nothing is collected by a timer during the test:
	// every file that disappears here disappeared because a prune chose to.
	srv := knobServer(t, v1Path, cache, f, 10*time.Minute)

	if err := srv.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	first := srv.current() // the handle an in-flight request would be holding

	f.publish(tagR2, makeAssets(t, v2, "", nil))
	if err := srv.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	second := srv.current()

	f.publish(tagR3, makeAssets(t, v3, "", nil))
	if err := srv.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}

	for _, snap := range []*snapshot{first, second} {
		if _, err := os.Stat(snap.path); err != nil {
			t.Fatalf("artifact %s of a snapshot still in grace was pruned: %v", snap.path, err)
		}
	}

	// The real symptom, not just the file check: concurrent queries on the
	// oldest snapshot force the pool to open connections against its path.
	var wg sync.WaitGroup
	errs := make([]error, 16)
	start := make(chan struct{})
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			var n int
			errs[i] = first.db.QueryRow(`SELECT COUNT(*) FROM works`).Scan(&n)
		}(i)
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("query %d on a superseded-but-draining snapshot failed: %v", i, err)
		}
	}
}

// TestPruneCollectsArtifactsOnceGraceElapses is the other half: sparing every
// retired path must not mean keeping them forever - once a snapshot's grace
// fires, its file is collectable again.
func TestPruneCollectsArtifactsOnceGraceElapses(t *testing.T) {
	v1Path, v1, _, v2 := buildV1V2(t)
	cache := t.TempDir()
	f := newKnobGitHub(t, tagR1, makeAssets(t, v1, "", nil))
	srv := knobServer(t, v1Path, cache, f, 20*time.Millisecond)

	if err := srv.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	superseded := srv.current().path
	f.publish(tagR2, makeAssets(t, v2, "", nil))
	if err := srv.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(superseded); os.IsNotExist(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("superseded artifact %s was never collected after its grace elapsed", superseded)
		}
		time.Sleep(5 * time.Millisecond)
	}
	srv.mu.Lock()
	retired := len(srv.retired)
	srv.mu.Unlock()
	if retired != 0 {
		t.Errorf("retired set still holds %d paths after every grace elapsed", retired)
	}
}

// ---- (2) download deadlines: progress, not total duration -------------------

// TestAssetDownloadsHaveNoWholeRequestTimeout pins the shape of the fix: a
// single http.Client.Timeout cannot serve both a small JSON document and a
// hundreds-of-MB artifact, and with no baked data in the image, aborting a
// healthy download is a container that never becomes ready.
func TestAssetDownloadsHaveNoWholeRequestTimeout(t *testing.T) {
	c := newGHClient("owner/name", "", "")
	if c.http.Timeout != 0 {
		t.Errorf("client carries a whole-request timeout of %s; asset downloads must be bounded by progress instead", c.http.Timeout)
	}
	if c.metaTimeout <= 0 || c.stallTimeout <= 0 {
		t.Errorf("metadata timeout = %s, stall timeout = %s; both must be set", c.metaTimeout, c.stallTimeout)
	}
	if c.assetDeadline <= c.metaTimeout {
		t.Errorf("asset deadline %s is no more generous than the metadata timeout %s", c.assetDeadline, c.metaTimeout)
	}
}

// TestSlowButHealthyDownloadIsNotAborted: a transfer that keeps delivering but
// takes many times the stall timeout in total must complete. Under a
// whole-request deadline of the same size it would be killed every attempt.
func TestSlowButHealthyDownloadIsNotAborted(t *testing.T) {
	v1Path, _, _, v2 := buildV1V2(t)
	cache := t.TempDir()
	assets := makeAssets(t, v2, "", nil)
	f := newKnobGitHub(t, tagR2, assets)
	srv := knobServer(t, v1Path, cache, f, time.Minute)
	srv.gh.stallTimeout = 300 * time.Millisecond
	srv.gh.assetDeadline = 30 * time.Second

	// Ten chunks, 50ms apart: no gap is anywhere near the stall timeout, but the
	// download as a whole takes well over it.
	gz := assets[dataAssetName]
	f.throttle(len(gz)/10+1, 50*time.Millisecond)

	started := time.Now()
	if err := srv.refresh(context.Background()); err != nil {
		t.Fatalf("a slow but healthy download was aborted: %v", err)
	}
	if elapsed := time.Since(started); elapsed < srv.gh.stallTimeout {
		t.Fatalf("the download finished in %s, faster than the %s stall timeout - the test proved nothing", elapsed, srv.gh.stallTimeout)
	}
	if srv.loaded != tagR2 || srv.current().stats.Works != 5 {
		t.Errorf("loaded = %q with %d works, want %q / 5", srv.loaded, srv.current().stats.Works, tagR2)
	}
}

// TestStalledDownloadIsAbandoned is the guard the generous deadline needs: a
// connection that is up and simply stops delivering is abandoned after the stall
// timeout, with the usual invariants (no swap, no artifact, no temp file) and
// with the ETag forgotten so the next poll really retries.
func TestStalledDownloadIsAbandoned(t *testing.T) {
	v1Path, _, _, v2 := buildV1V2(t)
	cache := t.TempDir()
	f := newKnobGitHub(t, tagR2, makeAssets(t, v2, "", nil))
	f.hangOn(dataAssetName)
	srv := knobServer(t, v1Path, cache, f, time.Minute)
	srv.gh.stallTimeout = 150 * time.Millisecond
	srv.gh.assetDeadline = 30 * time.Second
	before := srv.current()

	err := srv.refresh(context.Background())
	if err == nil {
		t.Fatal("a stalled download was accepted")
	}
	if !strings.Contains(err.Error(), "stalled") {
		t.Errorf("error = %v, want it to name the stall (a bare context error says nothing)", err)
	}
	if srv.current() != before {
		t.Errorf("snapshot swapped despite an abandoned download")
	}
	if _, err := os.Stat(srv.dbCachePath(tagR2)); !os.IsNotExist(err) {
		t.Errorf("partial artifact installed")
	}
	if names := cacheNames(t, cache); hasTempFile(names) {
		t.Errorf("temp file leaked: %v", names)
	}
	if srv.gh.etag != "" {
		t.Errorf("etag retained after a failed refresh: %q", srv.gh.etag)
	}
}

// ---- (3) the cache volume buys boot resilience ------------------------------

// TestRefreshAdoptsVerifiedCachedArtifact: the artifact for the newest release
// is already on the volume (a restarted container), so the refresh must verify
// it against the published digest and adopt it WITHOUT downloading it again.
func TestRefreshAdoptsVerifiedCachedArtifact(t *testing.T) {
	v1Path, _, _, v2 := buildV1V2(t)
	cache := t.TempDir()
	f := newKnobGitHub(t, tagR2, makeAssets(t, v2, "", nil))
	srv := knobServer(t, v1Path, cache, f, time.Minute)
	// What the previous container left behind.
	if err := os.WriteFile(srv.dbCachePath(tagR2), v2, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := srv.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if srv.loaded != tagR2 || srv.current().stats.Works != 5 {
		t.Errorf("loaded = %q with %d works, want %q / 5", srv.loaded, srv.current().stats.Works, tagR2)
	}
	if got := f.hitCount(dataAssetName); got != 0 {
		t.Errorf("artifact downloaded %d times, want 0 (the cache already held it)", got)
	}
	if got := f.hitCount(rawAssetName + ".sha256"); got != 1 {
		t.Errorf("raw checksum fetched %d times, want 1 (the cached file must be verified, not trusted)", got)
	}
}

// TestRefreshRedownloadsAnUnverifiableCachedArtifact: a file under the right
// name whose bytes are NOT the release's must never be adopted - the cache is
// verified, not trusted.
func TestRefreshRedownloadsAnUnverifiableCachedArtifact(t *testing.T) {
	v1Path, v1, _, v2 := buildV1V2(t)
	cache := t.TempDir()
	f := newKnobGitHub(t, tagR2, makeAssets(t, v2, "", nil))
	srv := knobServer(t, v1Path, cache, f, time.Minute)
	// A perfectly loadable artifact - just not this release's.
	if err := os.WriteFile(srv.dbCachePath(tagR2), v1, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := srv.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := f.hitCount(dataAssetName); got != 1 {
		t.Errorf("artifact downloaded %d times, want 1 (the mismatched cache file must not be adopted)", got)
	}
	if srv.loaded != tagR2 || srv.current().stats.Works != 5 {
		t.Errorf("loaded = %q with %d works, want %q / 5 (the freshly downloaded artifact)", srv.loaded, srv.current().stats.Works, tagR2)
	}
}

// TestDegradedBootServesStaleCachedArtifact: GitHub is unreachable at boot and
// the image bakes no data, but the cache volume still holds what the previous
// container served. Answering 503 with that on disk is the finding; serving it
// as stale is the fix - and the first reachable poll then confirms it against
// the release without downloading anything.
func TestDegradedBootServesStaleCachedArtifact(t *testing.T) {
	_, v1, _, _ := buildV1V2(t)
	cache := t.TempDir()
	f := newKnobGitHub(t, tagR1, makeAssets(t, v1, "", nil))
	f.setDown(true)

	// Seed the volume the way a previous container would have.
	staleServer := &Server{cfg: Config{CacheDir: cache}}
	if err := os.WriteFile(staleServer.dbCachePath(tagR1), v1, 0o600); err != nil {
		t.Fatal(err)
	}

	srv, err := New(Config{Poll: true, Repo: "owner/name", CacheDir: cache, apiBase: f.srv.URL,
		swapGrace: time.Minute, bootRetry: time.Hour})
	if err != nil {
		t.Fatalf("New must not fail: %v", err)
	}
	snap := srv.current()
	if snap == nil {
		t.Fatal("boot answered 503 with a loadable artifact in the cache directory")
	}
	if snap.stats.Works != 4 {
		t.Errorf("works = %d, want 4", snap.stats.Works)
	}
	if snap.tag != tagR1 {
		t.Errorf("snapshot tag = %q, want %q (read back from the file name, so a patch can still be based on it)", snap.tag, tagR1)
	}
	if srv.loaded != "" {
		t.Errorf("loaded = %q, want empty: nothing has confirmed this artifact against a release yet", srv.loaded)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	if code, _ := getJSON(t, ts.URL, "/healthz"); code != http.StatusOK {
		t.Errorf("healthz = %d, want 200: the server is serving", code)
	}
	if code, _ := getJSON(t, ts.URL, "/api/v1/stats"); code != http.StatusOK {
		t.Errorf("stats = %d, want 200", code)
	}

	// GitHub comes back: the stale artifact is confirmed, not re-downloaded.
	f.setDown(false)
	if err := srv.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if srv.loaded != tagR1 {
		t.Errorf("loaded = %q, want %q once the release confirmed the cached artifact", srv.loaded, tagR1)
	}
	if got := f.hitCount(dataAssetName); got != 0 {
		t.Errorf("artifact downloaded %d times, want 0", got)
	}
}

// TestDegradedBootWithNothingCachedStillServes503 keeps the fallback honest: an
// empty (or unusable) cache is still the 503 boot, not a crash.
func TestDegradedBootWithNothingCachedStillServes503(t *testing.T) {
	cache := t.TempDir()
	// Not an artifact: a leftover temp file and a foreign file must not be adopted.
	if err := os.WriteFile(filepath.Join(cache, ".meta-abc.tmp"), []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, "someone-elses.sqlite"), []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := newKnobGitHub(t, tagR1, map[string][]byte{})
	f.setDown(true)
	srv, err := New(Config{Poll: true, Repo: "owner/name", CacheDir: cache, apiBase: f.srv.URL,
		swapGrace: time.Minute, bootRetry: time.Hour})
	if err != nil {
		t.Fatalf("New must not fail: %v", err)
	}
	if srv.current() != nil {
		t.Fatal("adopted something that is not one of our artifacts")
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	if code, _ := getJSON(t, ts.URL, "/healthz"); code != http.StatusServiceUnavailable {
		t.Errorf("healthz = %d, want 503", code)
	}
}

// TestTagFromCacheName pins the file-name round trip the stale fallback reads
// its tag back through, including what it must refuse.
func TestTagFromCacheName(t *testing.T) {
	srv := &Server{cfg: Config{CacheDir: "/cache"}}
	if got := tagFromCacheName(filepath.Base(srv.dbCachePath(tagR2))); got != tagR2 {
		t.Errorf("round trip = %q, want %q", got, tagR2)
	}
	for _, name := range []string{".meta-abc.tmp", "meta-x.sqlite.patch.zst", "someone-elses.sqlite", "meta-x.gz"} {
		if got := tagFromCacheName(name); got != "" {
			t.Errorf("tagFromCacheName(%q) = %q, want \"\" (not a finished artifact of ours)", name, got)
		}
	}
}

// ---- (4) Retry-After must report the wait that is actually in force ---------

// TestRetryAfterReportsTheCurrentWait: the 503s advertised bootRetry forever,
// even once the loop had backed off to the poll interval, sending clients back
// dozens of times before anything could have changed.
func TestRetryAfterReportsTheCurrentWait(t *testing.T) {
	f := newKnobGitHub(t, tagR1, map[string][]byte{})
	f.setDown(true)
	srv, err := New(Config{Poll: true, Repo: "owner/name", CacheDir: t.TempDir(), apiBase: f.srv.URL,
		swapGrace: time.Minute, bootRetry: 30 * time.Second, Interval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	header := func(path string) string {
		resp, err := http.Get(ts.URL + path) //nolint:noctx // test client
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		return resp.Header.Get("Retry-After")
	}
	for _, path := range []string{"/api/v1/stats", "/healthz"} {
		if got := header(path); got != "30" {
			t.Errorf("%s Retry-After = %q at boot, want \"30\"", path, got)
		}
	}
	// Once the loop has backed off to the interval, so must the header.
	srv.nextRetry.Store(int64(time.Hour))
	for _, path := range []string{"/api/v1/stats", "/healthz"} {
		if got := header(path); got != "3600" {
			t.Errorf("%s Retry-After = %q after backoff, want \"3600\"", path, got)
		}
	}
	// A sub-second wait still advertises a whole second: "0" reads as "at once".
	srv.nextRetry.Store(int64(10 * time.Millisecond))
	if got := header("/api/v1/stats"); got != "1" {
		t.Errorf("Retry-After = %q for a 10ms wait, want \"1\"", got)
	}
}

// TestPollLoopPublishesItsBackoff: the header is only right if the loop keeps it
// in step with the wait it is actually sleeping on.
func TestPollLoopPublishesItsBackoff(t *testing.T) {
	f := newKnobGitHub(t, tagR1, map[string][]byte{})
	f.setDown(true)
	srv, err := New(Config{Poll: true, Repo: "owner/name", CacheDir: t.TempDir(), apiBase: f.srv.URL,
		swapGrace: time.Minute, bootRetry: 5 * time.Millisecond, Interval: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.pollLoop(ctx)

	deadline := time.Now().Add(3 * time.Second)
	for {
		if time.Duration(srv.nextRetry.Load()) > srv.cfg.bootRetry {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("advertised retry stayed at %s while the loop backed off", time.Duration(srv.nextRetry.Load()))
		}
		time.Sleep(5 * time.Millisecond)
	}
}
