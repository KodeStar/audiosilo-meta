package serve

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestVerifyChecksum(t *testing.T) {
	data := []byte("the compressed artifact bytes")
	sum := sha256.Sum256(data)
	good := []byte(hex.EncodeToString(sum[:]) + "  meta.sqlite.gz\n")
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

// fakeGitHub serves a releases/latest endpoint with ETag/304 support plus the
// two release assets. It counts full (200) release responses.
type fakeGitHub struct {
	srv       *httptest.Server
	etag      string
	gzData    []byte
	sumData   []byte
	fullFetch atomic.Int32
	assetHits atomic.Int32
	notMod    atomic.Int32
}

func newFakeGitHub(t *testing.T, gzData, sumData []byte) *fakeGitHub {
	f := &fakeGitHub{etag: `"rel-1"`, gzData: gzData, sumData: sumData}
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/owner/name/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == f.etag {
			f.notMod.Add(1)
			w.WriteHeader(http.StatusNotModified)
			return
		}
		f.fullFetch.Add(1)
		w.Header().Set("ETag", f.etag)
		rel := ghRelease{
			TagName: "data-v2026.07.11-abc1234",
			Assets: []ghAsset{
				{Name: "meta.sqlite.gz", DownloadURL: f.srv.URL + "/dl/meta.sqlite.gz"},
				{Name: "meta.sqlite.gz.sha256", DownloadURL: f.srv.URL + "/dl/meta.sqlite.gz.sha256"},
			},
		}
		_ = json.NewEncoder(w).Encode(rel)
	})
	mux.HandleFunc("/dl/meta.sqlite.gz", func(w http.ResponseWriter, _ *http.Request) {
		f.assetHits.Add(1)
		_, _ = w.Write(f.gzData)
	})
	mux.HandleFunc("/dl/meta.sqlite.gz.sha256", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(f.sumData)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func TestRefreshETagAndSwap(t *testing.T) {
	// Build a real artifact, gzip it, and compute its checksum file.
	dbPath := buildFixtureDB(t, fixtureCatalog(), nil)
	raw, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	gzData := gzOf(t, raw)
	sum := sha256.Sum256(gzData)
	sumData := []byte(fmt.Sprintf("%s  meta.sqlite.gz\n", hex.EncodeToString(sum[:])))

	fake := newFakeGitHub(t, gzData, sumData)

	// Seed the server with a starting local artifact so New() doesn't need to poll.
	seed := buildFixtureDB(t, fixtureCatalog(), nil)
	srv, err := New(Config{
		DBPath:    seed,
		Repo:      "owner/name",
		CacheDir:  t.TempDir(),
		swapGrace: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Point the server's GitHub client at the fake.
	srv.gh = newGHClient("owner/name", "", fake.srv.URL)

	// First refresh: full fetch, download, verify, swap.
	if err := srv.refresh(context.Background()); err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	if fake.fullFetch.Load() != 1 {
		t.Errorf("expected 1 full release fetch, got %d", fake.fullFetch.Load())
	}
	if srv.loaded != "data-v2026.07.11-abc1234" {
		t.Errorf("loaded tag = %q", srv.loaded)
	}

	// Second refresh: the stored ETag yields a 304, so no re-download or swap.
	if err := srv.refresh(context.Background()); err != nil {
		t.Fatalf("second refresh: %v", err)
	}
	if fake.notMod.Load() != 1 {
		t.Errorf("expected a 304 on the second poll, got %d", fake.notMod.Load())
	}
	if fake.assetHits.Load() != 1 {
		t.Errorf("asset downloaded %d times, want exactly 1", fake.assetHits.Load())
	}
}

func TestRefreshRejectsCorruptDownload(t *testing.T) {
	dbPath := buildFixtureDB(t, fixtureCatalog(), nil)
	raw, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	gzData := gzOf(t, raw)
	// A checksum that does not match the gz payload.
	badSum := []byte("deadbeef  meta.sqlite.gz\n")
	fake := newFakeGitHub(t, gzData, badSum)

	seed := buildFixtureDB(t, fixtureCatalog(), nil)
	srv, err := New(Config{DBPath: seed, Repo: "owner/name", CacheDir: t.TempDir(), swapGrace: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	srv.gh = newGHClient("owner/name", "", fake.srv.URL)

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
