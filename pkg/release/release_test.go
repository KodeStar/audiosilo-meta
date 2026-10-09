package release

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
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

func dirEntries(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// TestInstallVerified covers the checksum gate every streamed asset now goes
// through: matching bytes install, tampered bytes do not (and leave nothing
// behind), and an unusable checksum file is an error rather than a panic.
func TestInstallVerified(t *testing.T) {
	data := []byte("the compressed artifact bytes")
	good := sumFile("meta.sqlite.gz", data)
	want, err := ExpectedDigest(good)
	if err != nil {
		t.Fatalf("expectedDigest: %v", err)
	}

	dir := t.TempDir()
	dst := filepath.Join(dir, "asset.bin")
	if _, err := InstallVerified(bytes.NewReader(data), dst, want, 0); err != nil {
		t.Errorf("good checksum rejected: %v", err)
	}
	if got, _ := os.ReadFile(dst); !bytes.Equal(got, data) {
		t.Errorf("installed bytes differ from the source")
	}

	// A corrupted download must be rejected and must not replace the file.
	tampered := filepath.Join(dir, "tampered.bin")
	if _, err := InstallVerified(bytes.NewReader([]byte("tampered")), tampered, want, 0); err == nil {
		t.Errorf("corrupted download accepted")
	}
	if _, err := os.Stat(tampered); !os.IsNotExist(err) {
		t.Errorf("destination created despite a checksum mismatch")
	}

	// An empty checksum file is an error, not a panic.
	if _, err := ExpectedDigest([]byte("")); err == nil {
		t.Errorf("empty checksum file accepted")
	}
}

// TestGunzipStreamTo covers the one-pass download path: the COMPRESSED bytes are
// what the published checksum covers, so the digest is taken over the gz while
// the artifact is decompressed straight to disk. A gz whose digest does not match
// must leave nothing behind, even though the decompression itself succeeded.
func TestGunzipStreamTo(t *testing.T) {
	payload := []byte("hello sqlite")
	gz := gzOf(t, payload)
	dir := t.TempDir()

	dst := filepath.Join(dir, "nested", "out.bin")
	if _, err := gunzipStreamTo(bytes.NewReader(gz), dst, hexDigest(gz), decompressFloor); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("gunzip = %q, want %q", got, payload)
	}

	// A valid gz carrying the wrong bytes: decompression succeeds, the digest
	// gate does not, and no file is installed.
	bad := filepath.Join(dir, "bad.bin")
	_, err = gunzipStreamTo(bytes.NewReader(gzOf(t, []byte("tampered"))), bad, hexDigest(gz), decompressFloor)
	if err == nil {
		t.Errorf("gz with a mismatched digest accepted")
	}
	if _, err := os.Stat(bad); !os.IsNotExist(err) {
		t.Errorf("destination created despite a digest mismatch")
	}
}

// TestDecompressBound pins the arithmetic both refresh paths write under. The
// property that matters is that it is a CEILING and never a trap: too low is
// not a caught attack, it is every refresh failing forever while the server
// serves stale data.
func TestDecompressBound(t *testing.T) {
	const big = 3 << 30 // an artifact bigger than the floor
	cases := []struct {
		name     string
		declared int64
		current  int64
		want     int64
	}{
		{"nothing known is the floor", 0, 0, decompressFloor},
		{"a negative size is nothing known", -1, 0, decompressFloor},
		{"a small declared size stays on the floor", 1 << 20, 0, decompressFloor},
		{"a large declared size takes the ratio", 1 << 30, 0, decompressRatio << 30},
		// THE FINDING: an undeclared size used to collapse onto a 1 GiB floor,
		// which is BELOW the ~1.6 GB artifact it was bounding, so every full
		// refresh would have failed forever. The loaded artifact is what keeps
		// the bound tracking the data.
		{"an undeclared size clears the loaded artifact", 0, big, big * currentArtifactRatio},
		{"the loaded artifact never lowers the floor", 0, 1 << 20, decompressFloor},
		{"the largest arm wins", 1 << 30, big, max(int64(decompressRatio)<<30, big*currentArtifactRatio)},
		{"an absurd declared size cannot overflow", 1 << 62, 0, decompressFloor},
		{"an absurd loaded size cannot overflow", 0, 1 << 62, decompressFloor},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DecompressBound(tc.declared, tc.current); got != tc.want {
				t.Errorf("DecompressBound(%d, %d) = %d, want %d", tc.declared, tc.current, got, tc.want)
			}
		})
	}

	// The floor alone has to clear the real artifact, since that is where an
	// undeclared size lands on a server with nothing loaded yet.
	const realArtifactBytes = 1_600_000_000
	if decompressFloor <= realArtifactBytes {
		t.Errorf("the floor is %d bytes, at or below the ~%d-byte artifact it must bound",
			int64(decompressFloor), int64(realArtifactBytes))
	}
}

// TestGunzipStreamToIsBounded is the finding: the sha256 gate only fires once the
// bytes are on disk, and a gzip stream declares no output size - so an asset that
// expands without limit fills the cache volume before anything rejects it. The
// bound stops the COPY, so the outcome is a failed refresh with nothing
// installed, exactly as a digest mismatch is.
func TestGunzipStreamToIsBounded(t *testing.T) {
	payload := bytes.Repeat([]byte("a"), 1<<20) // compresses to ~1KB
	gz := gzOf(t, payload)
	dir := t.TempDir()

	dst := filepath.Join(dir, "bounded.bin")
	_, err := gunzipStreamTo(bytes.NewReader(gz), dst, hexDigest(gz), 4096)
	if err == nil {
		t.Fatal("an asset expanding far past its bound was installed")
	}
	if !strings.Contains(err.Error(), "bound") {
		t.Errorf("error = %v, want the bound named", err)
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Errorf("destination created despite the bound")
	}
	for _, e := range dirEntries(t, dir) {
		if strings.HasPrefix(e, ".meta-") {
			t.Errorf("leftover temp file %q after a bounded refusal", e)
		}
	}

	// The bound is not tight: the real ratio (about 4x) must pass, and an
	// artifact exactly AT the bound is legitimate rather than suspicious - while
	// ONE byte past it is not, which is the whole boundary in two lines.
	exact := filepath.Join(dir, "exact.bin")
	if _, err := gunzipStreamTo(bytes.NewReader(gz), exact, hexDigest(gz), int64(len(payload))); err != nil {
		t.Errorf("a payload exactly at the bound was refused: %v", err)
	}
	over := filepath.Join(dir, "over.bin")
	if _, err := gunzipStreamTo(bytes.NewReader(gz), over, hexDigest(gz), int64(len(payload))-1); err == nil {
		t.Error("a payload one byte past the bound was installed")
	}
	if _, err := os.Stat(over); !os.IsNotExist(err) {
		t.Error("destination created despite the bound")
	}
}

// TestAssetHostsAreAllowlisted is the finding: an asset URL comes out of the
// release JSON and get() attaches this server's token to whatever host it names.
// A release that pointed an asset elsewhere would hand that host the credential,
// so the host is checked BEFORE the request is built.
func TestAssetHostsAreAllowlisted(t *testing.T) {
	prod := New("owner/name", "token")
	for _, url := range []string{
		"https://github.com/owner/name/releases/download/v1/meta.sqlite.gz",
		"https://objects.githubusercontent.com/github-production-release-asset/1/2",
		"https://release-assets.githubusercontent.com/x",
		"https://api.github.com/repos/owner/name/releases/assets/1",
	} {
		if err := prod.checkAssetURL(url); err != nil {
			t.Errorf("%s was refused: %v", url, err)
		}
	}
	for _, tc := range []struct{ url, reason string }{
		{"https://evil.example/meta.sqlite.gz", "not a GitHub release-asset host"},
		{"https://github.com.evil.example/meta.sqlite.gz", "not a GitHub release-asset host"},
		{"https://notgithubusercontent.com/x", "not a GitHub release-asset host"},
		// A token is never sent in clear.
		{"http://github.com/owner/name/releases/download/v1/meta.sqlite.gz", "is not https"},
		// THE PORT: the host arm reads u.Hostname(), which strips it, so an
		// allowlisted NAME with a port of the release JSON's choosing used to be
		// dialled with the bearer token attached.
		{"https://objects.githubusercontent.com:8443/x", "explicit port"},
		{"https://github.com:1337/owner/name/releases/download/v1/meta.sqlite.gz", "explicit port"},
		{"https://api.github.com:8443/repos/owner/name/releases/assets/1", "explicit port"},
		{"://nonsense", "download"},
	} {
		err := prod.checkAssetURL(tc.url)
		if err == nil {
			t.Errorf("%s was allowed", tc.url)
			continue
		}
		if !strings.Contains(err.Error(), tc.reason) {
			t.Errorf("%s was refused as %q, want the reason to name %q", tc.url, err, tc.reason)
		}
	}

	// The origins WithAPIBase/WithAllowedOrigin were handed are the one exception, and they are
	// whole origins: the port is part of what must match, which is how a test
	// server is reached without the rule above gaining a clause.
	local := New("owner/name", "token", WithAPIBase("http://127.0.0.1:1/"))
	if err := local.checkAssetURL("http://127.0.0.1:1/dl/tag/meta.sqlite.gz"); err != nil {
		t.Errorf("the configured base's own origin was refused: %v", err)
	}
	for _, url := range []string{
		"http://127.0.0.2:1/dl/tag/meta.sqlite.gz",  // another host
		"http://127.0.0.1:2/dl/tag/meta.sqlite.gz",  // another port
		"https://127.0.0.1:1/dl/tag/meta.sqlite.gz", // another scheme
	} {
		if err := local.checkAssetURL(url); err == nil {
			t.Errorf("%s rode in on the exempt origin", url)
		}
	}
}

// TestAssetRedirectsAreRechecked is the finding the attachment fetcher had too,
// one repository over: policy ran on the URL the release JSON named and on
// nothing after it. A browser_download_url ALWAYS 302s to a CDN host, so
// redirects are this path's NORMAL shape.
//
// What a hop is judged by is ghhost.HopPolicy - https, no IP literal, bounded -
// and NOT the asset allowlist: GitHub chooses the CDN, has moved it before, and
// net/http strips the Authorization header on a cross-host hop. ghhost's own
// test pins that an ordinary unlisted https host is followed; a local test
// server can only be an IP literal, so it cannot be pinned from here.
func TestAssetRedirectsAreRechecked(t *testing.T) {
	var elsewhereHits atomic.Int32
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		elsewhereHits.Add(1)
		_, _ = w.Write([]byte("payload"))
	}))
	defer elsewhere.Close()

	asset := []byte("the artifact bytes")
	fake := newFakeGitHub(t, fakeRel{tag: "data-v1", assets: map[string][]byte{DataAsset: asset}})
	c := New("owner/name", "token", WithAPIBase(fake.srv.URL))

	t.Run("a hop to the exempt origin is followed", func(t *testing.T) {
		fake.setRedirect(fake.srv.URL + "/dl/data-v1/" + DataAsset)
		resp, err := c.get(context.Background(), fake.srv.URL+"/redirect")
		if err != nil {
			t.Fatalf("an allowed hop was refused: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		got, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, asset) {
			t.Errorf("body = %q, want the asset", got)
		}
	})

	t.Run("a hop to another local host is refused and never dialed", func(t *testing.T) {
		// Plain HTTP on a loopback IP: both arms of the hop rule, and the shape
		// an SSRF redirect takes in production (169.254.169.254, a service on the
		// container's own loopback).
		fake.setRedirect(elsewhere.URL + "/meta.sqlite.gz")
		resp, err := c.get(context.Background(), fake.srv.URL+"/redirect")
		if err == nil {
			_ = resp.Body.Close()
			t.Fatal("a redirect to a refused location was followed")
		}
		if !strings.Contains(err.Error(), "redirected to a refused location") {
			t.Errorf("error = %v, want the redirect refusal", err)
		}
		if n := elsewhereHits.Load(); n != 0 {
			t.Errorf("the refused host was dialed %d times", n)
		}
	})
}

// TestReleaseMetadataFollowsARedirect: the metadata call shares the client, so
// it shares the hop policy. A repository RENAME answers 301 on
// /repos/<old>/releases, and a policy that refused it would strand every poller
// on the old name.
func TestReleaseMetadataFollowsARedirect(t *testing.T) {
	fake := newFakeGitHub(t, fakeRel{tag: "data-v1", assets: map[string][]byte{DataAsset: []byte("x")}})
	moved := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, fake.srv.URL+r.URL.RequestURI(), http.StatusMovedPermanently)
	}))
	defer moved.Close()

	c := New("owner/name", "", WithAPIBase(moved.URL), WithAllowedOrigin(fake.srv.URL))
	rel, _, notModified, err := c.LatestData(context.Background(), "")
	if err != nil {
		t.Fatalf("LatestData through a 301: %v", err)
	}
	if notModified || rel == nil || rel.Tag != "data-v1" {
		t.Errorf("rel = %+v, notModified = %v, want the moved repository's release", rel, notModified)
	}
}

// TestRefusedAssetHostIsNeverDialed: the allowlist runs before the request
// exists, so the refused host sees no connection at all - which is the whole
// point, since making the request is what would disclose the token.
func TestRefusedAssetHostIsNeverDialed(t *testing.T) {
	var hits atomic.Int32
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte("payload"))
	}))
	defer elsewhere.Close()

	// A client whose exempt origin is a DIFFERENT local server, so `elsewhere` is
	// off the rule the way a third-party host is in production.
	other := httptest.NewServer(http.NotFoundHandler())
	defer other.Close()
	c := New("owner/name", "token", WithAPIBase(other.URL))

	resp, err := c.get(context.Background(), elsewhere.URL+"/meta.sqlite.gz")
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("an asset download to a refused host was made")
	}
	if !strings.Contains(err.Error(), "is not https") {
		t.Errorf("error = %v, want the allowlist refusal", err)
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("the refused host was dialed %d times", n)
	}
}

// TestAssetDownloadIsBoundedByItsDeclaredSize is the finding on the one
// artifact-sized writer that had no bound at all: the patch asset streamed to
// disk with maxBytes 0, although the release states its size right at the call
// site. Nothing is decompressed on that path, so the declared size is the EXACT
// bound rather than a multiple of it.
func TestAssetDownloadIsBoundedByItsDeclaredSize(t *testing.T) {
	payload := bytes.Repeat([]byte("p"), 4096)
	fake := newFakeGitHub(t, fakeRel{tag: "data-v1", assets: map[string][]byte{"blob.bin": payload}})
	c := New("owner/name", "", WithAPIBase(fake.srv.URL), WithLogger(log.New(io.Discard, "", 0)))
	dst := filepath.Join(t.TempDir(), "blob.bin")
	url := fake.srv.URL + "/dl/data-v1/blob.bin"

	over := &Release{Tag: "data-v1", Assets: []Asset{
		{Name: "blob.bin", Size: int64(len(payload)) - 1, URL: url},
	}}
	if _, err := c.DownloadAsset(context.Background(), over, "blob.bin", dst, ""); err == nil {
		t.Error("an asset one byte past its declared size was installed")
	} else if !strings.Contains(err.Error(), "bound") {
		t.Errorf("error = %v, want the bound named", err)
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Error("destination created despite the bound")
	}

	exact := &Release{Tag: "data-v1", Assets: []Asset{
		{Name: "blob.bin", Size: int64(len(payload)), URL: url},
	}}
	if n, err := c.DownloadAsset(context.Background(), exact, "blob.bin", dst, ""); err != nil || n != int64(len(payload)) {
		t.Errorf("an asset exactly at its declared size: %d bytes, %v", n, err)
	}

	// A release that declares no size falls back to the expansion bound rather
	// than to no bound at all.
	none := &Release{Tag: "data-v1", Assets: []Asset{{Name: "blob.bin", URL: url}}}
	if _, err := c.DownloadAsset(context.Background(), none, "blob.bin", dst, ""); err != nil {
		t.Errorf("an asset declaring no size was refused: %v", err)
	}
}

// TestAssetDownloadsHaveNoWholeRequestTimeout pins the shape of the fix: a
// single http.Client.Timeout cannot serve both a small JSON document and a
// hundreds-of-MB artifact, and with no baked data in the image, aborting a
// healthy download is a container that never becomes ready.
func TestAssetDownloadsHaveNoWholeRequestTimeout(t *testing.T) {
	c := New("owner/name", "")
	if c.http.Timeout != 0 {
		t.Errorf("client carries a whole-request timeout of %s; asset downloads must be bounded by progress instead", c.http.Timeout)
	}
	if c.timeouts.Metadata <= 0 || c.timeouts.Stall <= 0 {
		t.Errorf("metadata timeout = %s, stall timeout = %s; both must be set", c.timeouts.Metadata, c.timeouts.Stall)
	}
	if c.timeouts.Asset <= c.timeouts.Metadata {
		t.Errorf("asset deadline %s is no more generous than the metadata timeout %s", c.timeouts.Asset, c.timeouts.Metadata)
	}
}
