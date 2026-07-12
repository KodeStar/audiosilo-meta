package serve

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/kodestar/audiosilo-meta/internal/model"
)

// hexDigest is data's sha256 as lowercase hex.
func hexDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// sumFile builds a `sha256sum`-format checksum file over data for name.
func sumFile(name string, data []byte) []byte {
	return []byte(hexDigest(data) + "  " + name + "\n")
}

func TestVerifyChecksum(t *testing.T) {
	data := []byte("the compressed artifact bytes")
	good := sumFile("meta.sqlite.gz", data)
	if err := verifyChecksum(data, good); err != nil {
		t.Errorf("good checksum rejected: %v", err)
	}

	// A corrupted download must be rejected.
	if err := verifyChecksum([]byte("tampered"), good); err == nil {
		t.Errorf("corrupted download accepted")
	}
	// An empty checksum file is an error, not a panic.
	if err := verifyChecksum(data, []byte("")); err == nil {
		t.Errorf("empty checksum file accepted")
	}
}

// gzOf gzips b.
func gzOf(t *testing.T, b []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(b); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestGunzipTo(t *testing.T) {
	payload := []byte("hello sqlite")
	dst := filepath.Join(t.TempDir(), "nested", "out.bin")
	if err := gunzipTo(gzOf(t, payload), dst); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("gunzip = %q, want %q", got, payload)
	}
}

// fakeGitHub serves a releases/latest endpoint with ETag/304 support plus a
// per-release set of named assets. It counts full (200) release responses, 304s,
// and per-asset downloads. setRelease advances the release (new tag => new ETag)
// and resets the per-asset hit counts, so a test can assert exactly which assets
// the *current* refresh fetched; setAssetFailure makes one asset 500 on demand.
type fakeGitHub struct {
	srv *httptest.Server

	mu      sync.Mutex
	tag     string
	etag    string
	assets  map[string][]byte
	hits    map[string]int
	failing map[string]bool

	fullFetch atomic.Int32
	notMod    atomic.Int32
}

func newFakeGitHub(t *testing.T, tag string, assets map[string][]byte) *fakeGitHub {
	f := &fakeGitHub{}
	f.setRelease(tag, assets)
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/owner/name/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		etag, tag := f.etag, f.tag
		names := make([]string, 0, len(f.assets))
		for name := range f.assets {
			names = append(names, name)
		}
		f.mu.Unlock()

		if r.Header.Get("If-None-Match") == etag {
			f.notMod.Add(1)
			w.WriteHeader(http.StatusNotModified)
			return
		}
		f.fullFetch.Add(1)
		w.Header().Set("ETag", etag)
		sort.Strings(names)
		rel := ghRelease{TagName: tag}
		for _, name := range names {
			rel.Assets = append(rel.Assets, ghAsset{Name: name, DownloadURL: f.srv.URL + "/dl/" + name})
		}
		_ = json.NewEncoder(w).Encode(rel)
	})
	mux.HandleFunc("/dl/", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/dl/")
		f.mu.Lock()
		if f.failing[name] {
			f.mu.Unlock()
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		data, ok := f.assets[name]
		if ok {
			f.hits[name]++
		}
		f.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// setRelease advances the fake to a new release, changing the ETag (so the next
// poll is a 200, not a 304) and resetting per-asset hit counts and failures.
func (f *fakeGitHub) setRelease(tag string, assets map[string][]byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tag = tag
	f.etag = `"` + tag + `"`
	f.assets = assets
	f.hits = map[string]int{}
	f.failing = map[string]bool{}
}

// setAssetFailure makes downloads of the named asset return 500 (or heals it).
// The release metadata (and its ETag) is unchanged.
func (f *fakeGitHub) setAssetFailure(name string, fail bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failing[name] = fail
}

func (f *fakeGitHub) hitCount(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits[name]
}

// readDB reads a fixture artifact file into memory.
func readDB(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// makePatch builds a zstd --patch-from delta from prev to next using the
// klauspost encoder, registering prev as a raw content dictionary under id 0 -
// exactly the dictionary id the zstd CLI stamps on a --patch-from frame, which
// is what applyPatch's decoder assumes.
func makePatch(t *testing.T, prev, next []byte) []byte {
	t.Helper()
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderDictRaw(0, prev))
	if err != nil {
		t.Fatal(err)
	}
	patch := enc.EncodeAll(next, nil)
	_ = enc.Close()
	return patch
}

// makeAssets builds the standard four release assets for a db artifact: the gz
// anchor + its checksum, the raw-sqlite checksum, and (when patchFrom is set) a
// zstd delta named for that from-tag.
func makeAssets(t *testing.T, db []byte, patchFrom string, prev []byte) map[string][]byte {
	t.Helper()
	gz := gzOf(t, db)
	assets := map[string][]byte{
		"meta.sqlite.gz":        gz,
		"meta.sqlite.gz.sha256": sumFile("meta.sqlite.gz", gz),
		"meta.sqlite.sha256":    sumFile("meta.sqlite", db),
	}
	if patchFrom != "" && prev != nil {
		assets[patchAssetName(patchFrom)] = makePatch(t, prev, db)
	}
	return assets
}

// v2Catalog is fixtureCatalog plus one extra work, so its artifact's stats.Works
// differs from the base fixture (4 -> 5) - the observable signal that a patched
// refresh actually adopted the newer artifact.
func v2Catalog() *model.Catalog {
	cat := fixtureCatalog()
	cat.Works = append(cat.Works, &model.Work{
		ID: "the-martian", Title: "The Martian", Language: "en",
		Authors: []string{"andy-weir"}, License: "CC0-1.0",
	})
	return cat
}

// buildV1V2 builds the two artifacts the delta tests need, ONCE each: v1 (the
// base fixture, 4 works) and v2 (one extra work, 5). Both come back as (path,
// bytes) - the paths double as poll-server seeds, patch bases, and CLI inputs;
// the bytes as release assets.
func buildV1V2(t *testing.T) (v1Path string, v1 []byte, v2Path string, v2 []byte) {
	t.Helper()
	v1Path = buildFixtureDB(t, fixtureCatalog(), nil)
	v2Path = buildFixtureDB(t, v2Catalog(), nil)
	return v1Path, readDB(t, v1Path), v2Path, readDB(t, v2Path)
}

// newPollServer seeds a server from a local artifact (so New() doesn't poll) and
// points its GitHub client at the fake.
func newPollServer(t *testing.T, seed string, fake *fakeGitHub) *Server {
	t.Helper()
	srv, err := New(Config{DBPath: seed, Repo: "owner/name", CacheDir: t.TempDir(), swapGrace: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	srv.gh = newGHClient("owner/name", "", fake.srv.URL)
	return srv
}

const (
	tagR1 = "data-v2026.07.11-r1"
	tagR2 = "data-v2026.07.12-r2"
)

// setupR1 arranges the standard patch-test starting point from prebuilt v1
// artifacts: a fake publishing v1 under tagR1, a server seeded from v1Path, and
// one full refresh so the server is loaded on R1 (its snapshot path pointing at
// the cached R1 file, ready to be a patch base).
func setupR1(t *testing.T, v1Path string, v1 []byte) (*Server, *fakeGitHub) {
	t.Helper()
	fake := newFakeGitHub(t, tagR1, makeAssets(t, v1, "", nil))
	srv := newPollServer(t, v1Path, fake)
	if err := srv.refresh(context.Background()); err != nil {
		t.Fatalf("R1 refresh: %v", err)
	}
	if srv.loaded != tagR1 {
		t.Fatalf("after R1 refresh loaded = %q, want %q", srv.loaded, tagR1)
	}
	return srv, fake
}

func TestRefreshETagAndSwap(t *testing.T) {
	seed := buildFixtureDB(t, fixtureCatalog(), nil)
	fake := newFakeGitHub(t, tagR1, makeAssets(t, readDB(t, seed), "", nil))
	srv := newPollServer(t, seed, fake)

	// First refresh: no loaded tag yet, so the full path downloads and swaps.
	if err := srv.refresh(context.Background()); err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	if fake.fullFetch.Load() != 1 {
		t.Errorf("expected 1 full release fetch, got %d", fake.fullFetch.Load())
	}
	if srv.loaded != tagR1 {
		t.Errorf("loaded tag = %q", srv.loaded)
	}

	// Second refresh: the stored ETag yields a 304, so no re-download or swap.
	if err := srv.refresh(context.Background()); err != nil {
		t.Fatalf("second refresh: %v", err)
	}
	if fake.notMod.Load() != 1 {
		t.Errorf("expected a 304 on the second poll, got %d", fake.notMod.Load())
	}
	if fake.hitCount("meta.sqlite.gz") != 1 {
		t.Errorf("gz downloaded %d times, want exactly 1", fake.hitCount("meta.sqlite.gz"))
	}
}

func TestRefreshRejectsCorruptDownload(t *testing.T) {
	seed := buildFixtureDB(t, fixtureCatalog(), nil)
	assets := makeAssets(t, readDB(t, seed), "", nil)
	// A checksum that does not match the gz payload.
	assets["meta.sqlite.gz.sha256"] = []byte("deadbeef  meta.sqlite.gz\n")
	fake := newFakeGitHub(t, tagR1, assets)
	srv := newPollServer(t, seed, fake)

	if err := srv.refresh(context.Background()); err == nil {
		t.Fatalf("expected refresh to reject a checksum mismatch")
	}
	// The bad release must not have been adopted.
	if srv.loaded != "" {
		t.Errorf("loaded tag = %q, want empty after rejected refresh", srv.loaded)
	}
	if srv.current().stats.Works != 4 {
		t.Errorf("serving snapshot changed after a rejected refresh")
	}
}

func TestRefreshPatchHappyPath(t *testing.T) {
	v1Path, v1, _, v2 := buildV1V2(t)
	srv, fake := setupR1(t, v1Path, v1)

	// R2 ships a delta from R1; the poller should patch, not full-download.
	fake.setRelease(tagR2, makeAssets(t, v2, tagR1, v1))
	if err := srv.refresh(context.Background()); err != nil {
		t.Fatalf("patch refresh: %v", err)
	}
	if srv.loaded != tagR2 {
		t.Errorf("loaded = %q, want %q", srv.loaded, tagR2)
	}
	if got := srv.current().stats.Works; got != 5 {
		t.Errorf("works = %d, want 5 (v2 adopted)", got)
	}
	// The reconstructed artifact must be byte-identical to the real v2.
	patched := readDB(t, srv.current().path)
	if !bytes.Equal(patched, v2) {
		t.Errorf("patched artifact (%d bytes) differs from v2 (%d bytes)", len(patched), len(v2))
	}
	// The gz anchor must never have been fetched for R2 (patch path only).
	if got := fake.hitCount("meta.sqlite.gz"); got != 0 {
		t.Errorf("gz fetched %d times during patch refresh, want 0", got)
	}
	if got := fake.hitCount(patchAssetName(tagR1)); got != 1 {
		t.Errorf("patch fetched %d times, want 1", got)
	}
}

func TestRefreshPatchFallsBack(t *testing.T) {
	// One pair of artifacts serves every subtest; only the fake + server state
	// is per-case.
	v1Path, v1, _, v2 := buildV1V2(t)

	cases := []struct {
		name string
		// build assembles R2's assets (v1/v2 captured from the enclosing test).
		build func(t *testing.T) map[string][]byte
		// wantPatchHits pins whether tryPatch actually attempted the patch
		// download before falling back (1) or bailed without spending it (0).
		wantPatchHits int
	}{
		{
			name: "corrupt patch bytes",
			build: func(t *testing.T) map[string][]byte {
				a := makeAssets(t, v2, tagR1, v1)
				a[patchAssetName(tagR1)] = []byte("not a zstd frame")
				return a
			},
			wantPatchHits: 1,
		},
		{
			name: "wrong raw checksum",
			build: func(t *testing.T) map[string][]byte {
				a := makeAssets(t, v2, tagR1, v1)
				a["meta.sqlite.sha256"] = []byte("deadbeef  meta.sqlite\n")
				return a
			},
			wantPatchHits: 1,
		},
		{
			name: "patch from a different tag",
			build: func(t *testing.T) map[string][]byte {
				// Server is loaded on R1 but the only delta is from some older R0;
				// tryPatch must bail on the in-memory asset list, zero downloads.
				return makeAssets(t, v2, "data-v2026.07.01-r0", v1)
			},
			wantPatchHits: 0,
		},
		{
			name: "raw checksum missing",
			build: func(t *testing.T) map[string][]byte {
				a := makeAssets(t, v2, tagR1, v1)
				delete(a, "meta.sqlite.sha256")
				return a
			},
			wantPatchHits: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, fake := setupR1(t, v1Path, v1)

			fake.setRelease(tagR2, tc.build(t))
			if err := srv.refresh(context.Background()); err != nil {
				t.Fatalf("refresh should succeed via fallback: %v", err)
			}
			if srv.loaded != tagR2 {
				t.Errorf("loaded = %q, want %q", srv.loaded, tagR2)
			}
			if got := srv.current().stats.Works; got != 5 {
				t.Errorf("works = %d, want 5 (v2 adopted via full download)", got)
			}
			if got := fake.hitCount("meta.sqlite.gz"); got != 1 {
				t.Errorf("gz fetched %d times, want 1 (full fallback used)", got)
			}
			if got := fake.hitCount(patchAssetName(tagR1)); got != tc.wantPatchHits {
				t.Errorf("patch fetched %d times, want %d", got, tc.wantPatchHits)
			}
		})
	}
}

// TestRefreshRetriesAfterFailedDownload pins the ETag-forget behavior: a 200
// release fetch stores the new ETag before the assets are secured, so a failed
// download must clear it - otherwise every subsequent poll 304s and the server
// is stranded on the old artifact until the NEXT release is cut.
func TestRefreshRetriesAfterFailedDownload(t *testing.T) {
	v1Path, v1, _, v2 := buildV1V2(t)
	srv, fake := setupR1(t, v1Path, v1)

	// R2 is published (no patch asset, so the full path runs), but its gz
	// download 500s: the refresh must error and nothing may be adopted.
	fake.setRelease(tagR2, makeAssets(t, v2, "", nil))
	fake.setAssetFailure("meta.sqlite.gz", true)
	if err := srv.refresh(context.Background()); err == nil {
		t.Fatal("expected refresh to fail while the asset download 500s")
	}
	if srv.loaded != tagR1 {
		t.Fatalf("loaded = %q, want still %q after a failed refresh", srv.loaded, tagR1)
	}

	// The asset heals (same release, same ETag). The next refresh must retry
	// and succeed - before the etag-forget fix it 304-ed and no-oped forever.
	fake.setAssetFailure("meta.sqlite.gz", false)
	if err := srv.refresh(context.Background()); err != nil {
		t.Fatalf("refresh after the asset healed: %v", err)
	}
	if srv.loaded != tagR2 {
		t.Errorf("loaded = %q, want %q (retry must not be 304-blocked)", srv.loaded, tagR2)
	}
	if got := srv.current().stats.Works; got != 5 {
		t.Errorf("works = %d, want 5 (v2 adopted on retry)", got)
	}
}

// TestWorkflowMatchesGoConstants pins the bash<->Go contract: the release
// workflow's zstd window and patch-asset filename must match patchWindowLog and
// patchAssetName. A bump/rename on one side now fails tests instead of silently
// killing the delta feature (the poller would never find a matching asset).
func TestWorkflowMatchesGoConstants(t *testing.T) {
	wf, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(wf)
	if want := "--long=" + strconv.Itoa(patchWindowLog); !strings.Contains(s, want) {
		t.Errorf("release.yml does not contain %q - bump the workflow window and patchWindowLog together", want)
	}
	if want := patchAssetName("${PREV_TAG}"); !strings.Contains(s, want) {
		t.Errorf("release.yml does not contain %q - the patch asset naming convention drifted", want)
	}
}

func TestApplyPatch(t *testing.T) {
	v1Path, v1, _, v2 := buildV1V2(t)
	patch := makePatch(t, v1, v2)

	t.Run("happy", func(t *testing.T) {
		dst := filepath.Join(t.TempDir(), "out", "meta.sqlite")
		n, err := applyPatch(patch, v1Path, dst, hexDigest(v2))
		if err != nil {
			t.Fatalf("applyPatch: %v", err)
		}
		if n != int64(len(v2)) {
			t.Errorf("reported %d bytes written, want %d", n, len(v2))
		}
		if got := readDB(t, dst); !bytes.Equal(got, v2) {
			t.Errorf("patched result differs from v2")
		}
	})

	t.Run("hash mismatch", func(t *testing.T) {
		dir := t.TempDir()
		dst := filepath.Join(dir, "meta.sqlite")
		if _, err := applyPatch(patch, v1Path, dst, "deadbeef"); err == nil {
			t.Fatal("expected a hash mismatch error")
		}
		if _, err := os.Stat(dst); !os.IsNotExist(err) {
			t.Errorf("dst was created despite a hash mismatch")
		}
		// No leftover temp files in the dst directory.
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), ".meta-") {
				t.Errorf("leftover temp file %q after failed apply", e.Name())
			}
		}
	})
}

// TestApplyPatchCLIInterop proves the dictionary-id-0 assumption against real
// zstd CLI --patch-from output (not just the klauspost encoder). Skips when the
// zstd binary is unavailable.
func TestApplyPatchCLIInterop(t *testing.T) {
	zstdBin, err := exec.LookPath("zstd")
	if err != nil {
		t.Skip("zstd CLI not available")
	}
	v1Path, _, v2Path, v2 := buildV1V2(t)

	dir := t.TempDir()
	patchPath := filepath.Join(dir, "patch.zst")
	// The exact production flag: zstd clamps the frame window to the input
	// size, so --long=31 stays cheap on small fixtures.
	long := "--long=" + strconv.Itoa(patchWindowLog)
	cmd := exec.Command(zstdBin, "-f", "--patch-from="+v1Path, long, "-o", patchPath, v2Path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("zstd --patch-from: %v\n%s", err, out)
	}
	patch, err := os.ReadFile(patchPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("CLI patch size = %d bytes (v2 artifact = %d bytes)", len(patch), len(v2))

	dst := filepath.Join(dir, "out.sqlite")
	if _, err := applyPatch(patch, v1Path, dst, hexDigest(v2)); err != nil {
		t.Fatalf("applyPatch on CLI frame: %v", err)
	}
	if got := readDB(t, dst); !bytes.Equal(got, v2) {
		t.Errorf("CLI-frame patched result differs from v2")
	}
}
