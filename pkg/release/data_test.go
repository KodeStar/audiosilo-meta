package release

import (
	"bytes"
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func quietLogger() *log.Logger { return log.New(io.Discard, "", 0) }

// dataAssets is a data release's three contract assets over raw (the
// decompressed artifact bytes).
func dataAssets(t *testing.T, raw []byte) map[string][]byte {
	t.Helper()
	gz := gzOf(t, raw)
	return map[string][]byte{
		DataAsset:       gz,
		DataDigestAsset: sumFile(DataAsset, gz),
		RawDigestAsset:  sumFile("meta.sqlite", raw),
	}
}

// codeRel is a code/image release (v*): newer than every data release and
// carrying no data asset, so selection must skip it however it is listed.
func codeRel(tag string, published time.Time) fakeRel {
	return fakeRel{tag: tag, published: published, assets: map[string][]byte{
		"metaserve-linux-amd64.tar.gz": []byte("a binary, not data"),
	}}
}

// TestLatestDataPicksTheNewestDataRelease: code releases interleaved with data
// releases (and listed first, as GitHub's "latest" would be), a draft and a
// prerelease that carry the data asset name, and a list order that is NOT
// publish order - the selection is the non-draft, non-prerelease release
// carrying meta.sqlite.gz at the maximum published_at, whatever its position.
func TestLatestDataPicksTheNewestDataRelease(t *testing.T) {
	day := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)
	data := map[string][]byte{DataAsset: []byte("gz")}
	fake := newFakeGitHub(t,
		codeRel("v0.21.0", day.Add(10*time.Hour)),
		fakeRel{tag: "data-v2026.10.09-aaa-bbb", published: day.Add(2 * time.Hour), assets: data},
		codeRel("v0.20.9", day.Add(9*time.Hour)),
		fakeRel{tag: "data-v2026.10.09-ccc-ddd", published: day.Add(8 * time.Hour), assets: data},
		fakeRel{tag: "data-vdraft", draft: true, published: day.Add(11 * time.Hour), assets: data},
		fakeRel{tag: "data-v2026.10.09-pre", prerelease: true, published: day.Add(12 * time.Hour), assets: data},
		fakeRel{tag: "data-v2026.10.08-eee-fff", published: day.Add(-time.Hour), assets: data},
	)
	rel, etag, notModified, err := fake.client().LatestData(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if notModified || rel == nil || rel.Tag != "data-v2026.10.09-ccc-ddd" {
		t.Fatalf("rel = %+v, notModified %v, want data-v2026.10.09-ccc-ddd", rel, notModified)
	}
	if !rel.PublishedAt.Equal(day.Add(8 * time.Hour)) {
		t.Errorf("PublishedAt = %v", rel.PublishedAt)
	}
	if a, ok := rel.Asset(DataAsset); !ok || a.Size != 2 || !strings.HasSuffix(a.URL, "/dl/data-v2026.10.09-ccc-ddd/"+DataAsset) {
		t.Errorf("data asset = %+v, %v", a, ok)
	}
	if etag == "" {
		t.Error("no ETag returned to store")
	}
}

// TestLatestDataTieBreaksLikeJq: on equal published_at the LATER-listed release
// wins, as release.yml's max_by does - the two selectors must agree even on a
// same-second tie.
func TestLatestDataTieBreaksLikeJq(t *testing.T) {
	at := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	data := map[string][]byte{DataAsset: []byte("gz")}
	fake := newFakeGitHub(t,
		fakeRel{tag: "data-first", published: at, assets: data},
		fakeRel{tag: "data-second", published: at, assets: data},
	)
	rel, _, _, err := fake.client().LatestData(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if rel.Tag != "data-second" {
		t.Errorf("tie went to %s, want data-second (the later-listed, as jq's max_by)", rel.Tag)
	}
}

// TestLatestDataIsConditional: the ETag a 200 returns makes the next request a
// 304 (notModified, no release, the same ETag to keep), and a changed list is a
// 200 again with a new ETag.
func TestLatestDataIsConditional(t *testing.T) {
	data := map[string][]byte{DataAsset: []byte("gz")}
	fake := newFakeGitHub(t, fakeRel{tag: "data-1", assets: data})
	c := fake.client()

	_, etag, _, err := c.LatestData(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	rel, again, notModified, err := c.LatestData(context.Background(), etag)
	if err != nil || !notModified || rel != nil || again != etag {
		t.Fatalf("second call = %+v, %q, notModified %v, %v; want a 304 keeping %q", rel, again, notModified, err, etag)
	}
	if fake.listCount() != 1 {
		t.Errorf("the list was served in full %d times, want 1", fake.listCount())
	}

	fake.setReleases(fakeRel{tag: "data-2", assets: data})
	rel, next, notModified, err := c.LatestData(context.Background(), etag)
	if err != nil || notModified || rel == nil || rel.Tag != "data-2" {
		t.Fatalf("after a new release = %+v, notModified %v, %v", rel, notModified, err)
	}
	if next == etag || next == "" {
		t.Errorf("ETag after a change = %q (was %q), want a new one", next, etag)
	}
}

// TestLatestDataKeepsTheETagOfAListWithNoDataRelease: a 200 listing only code
// releases is an error, but its ETag still comes back to be stored - so the next
// poll is a cheap 304 until the list changes (retrying an unchanged list cannot
// find a data release either).
func TestLatestDataKeepsTheETagOfAListWithNoDataRelease(t *testing.T) {
	fake := newFakeGitHub(t, codeRel("v0.21.0", time.Now()))
	rel, etag, _, err := fake.client().LatestData(context.Background(), "")
	if err == nil || rel != nil {
		t.Fatalf("rel = %+v, err = %v; want the no-data-release error", rel, err)
	}
	if !strings.Contains(err.Error(), "no data release") {
		t.Errorf("error = %v", err)
	}
	if etag == "" {
		t.Error("the list's ETag was not returned beside the error")
	}
}

// TestRequestsCarryTheUserAgent: GitHub asks API clients to name themselves.
func TestRequestsCarryTheUserAgent(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Header.Get("User-Agent"))
		w.WriteHeader(http.StatusNotModified)
	}))
	t.Cleanup(srv.Close)
	c := New("owner/name", "", WithAPIBase(srv.URL), WithUserAgent("AudioSilo/2.1.0"))
	if _, _, _, err := c.LatestData(context.Background(), `"x"`); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "AudioSilo/2.1.0" {
		t.Errorf("User-Agent = %v", got)
	}
}

// TestDownloadDataInstallsAVerifiedArtifact: the happy path decompresses into
// dstPath, reports the raw size and digest, and reports progress up to the
// declared compressed size.
func TestDownloadDataInstallsAVerifiedArtifact(t *testing.T) {
	raw := bytes.Repeat([]byte("the artifact "), 4096)
	fake := newFakeGitHub(t, fakeRel{tag: "data-1", assets: dataAssets(t, raw)})
	c := fake.client()
	rel, _, _, err := c.LatestData(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "mirror", "meta-data-1.sqlite")
	var done, total int64
	res, err := c.DownloadData(context.Background(), rel, dst, func(d, tot int64) { done, total = d, tot })
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, raw) {
		t.Error("installed bytes differ from the artifact")
	}
	if res.Bytes != int64(len(raw)) || res.SHA256 != hexDigest(raw) {
		t.Errorf("Result = %+v, want %d bytes, %s", res, len(raw), hexDigest(raw))
	}
	gz, _ := rel.Asset(DataAsset)
	if total != gz.Size || done != gz.Size {
		t.Errorf("progress ended at %d / %d, want %d / %d", done, total, gz.Size, gz.Size)
	}
	if names := dirEntries(t, filepath.Dir(dst)); len(names) != 1 {
		t.Errorf("mirror folder holds %v, want only the artifact", names)
	}
}

// TestDownloadDataRefusesABadDigest: a compressed asset whose bytes do not match
// meta.sqlite.gz.sha256 is refused - and leaves neither the destination nor a
// temp file behind.
func TestDownloadDataRefusesABadDigest(t *testing.T) {
	raw := []byte("the real artifact")
	for _, tc := range []struct {
		name   string
		mangle func(map[string][]byte)
	}{
		{"the checksum", func(a map[string][]byte) { a[DataDigestAsset] = sumFile(DataAsset, []byte("other")) }},
		{"the bytes", func(a map[string][]byte) { a[DataAsset] = gzOf(t, []byte("tampered")) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assets := dataAssets(t, raw)
			tc.mangle(assets)
			fake := newFakeGitHub(t, fakeRel{tag: "data-1", assets: assets})
			c := fake.client()
			rel, _, _, err := c.LatestData(context.Background(), "")
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			dst := filepath.Join(dir, "meta-data-1.sqlite")
			if _, err := c.DownloadData(context.Background(), rel, dst, nil); err == nil || !strings.Contains(err.Error(), "sha256 mismatch") {
				t.Fatalf("err = %v, want the digest mismatch", err)
			}
			if names := dirEntries(t, dir); len(names) != 0 {
				t.Errorf("left behind %v after a refused download", names)
			}
		})
	}
}

// TestDownloadDataIsAnchoredOnTheGzDigest: the raw-file checksum is not a gate
// on a download (see DownloadData) - neither its absence nor a wrong one blocks
// an install the gz digest proves, and it is not even fetched; the
// decompressed digest still comes back in the Result.
func TestDownloadDataIsAnchoredOnTheGzDigest(t *testing.T) {
	raw := []byte("an artifact")
	for _, tc := range []struct {
		name   string
		mangle func(map[string][]byte)
	}{
		{"absent", func(a map[string][]byte) { delete(a, RawDigestAsset) }},
		{"wrong", func(a map[string][]byte) { a[RawDigestAsset] = sumFile("meta.sqlite", []byte("other")) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assets := dataAssets(t, raw)
			tc.mangle(assets)
			fake := newFakeGitHub(t, fakeRel{tag: "data-1", assets: assets})
			c := fake.client()
			rel, _, _, err := c.LatestData(context.Background(), "")
			if err != nil {
				t.Fatal(err)
			}
			res, err := c.DownloadData(context.Background(), rel, filepath.Join(t.TempDir(), "meta.sqlite"), nil)
			if err != nil || res.SHA256 != hexDigest(raw) {
				t.Fatalf("Result = %+v, %v", res, err)
			}
			if n := fake.hitCount(RawDigestAsset); n != 0 {
				t.Errorf("the raw checksum was fetched %d times, want 0", n)
			}
		})
	}
}

// TestDownloadDataKeepsTheCurrentFileOnFailure: a failed download into an
// existing destination leaves the existing file exactly as it was.
func TestDownloadDataKeepsTheCurrentFileOnFailure(t *testing.T) {
	assets := dataAssets(t, []byte("new"))
	assets[DataDigestAsset] = sumFile(DataAsset, []byte("not it"))
	fake := newFakeGitHub(t, fakeRel{tag: "data-2", assets: assets})
	c := fake.client()
	rel, _, _, err := c.LatestData(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "meta.sqlite")
	if err := os.WriteFile(dst, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := c.DownloadData(context.Background(), rel, dst, nil); err == nil {
		t.Fatal("a mismatched download was accepted")
	}
	if got, _ := os.ReadFile(dst); string(got) != "old" {
		t.Errorf("destination = %q, want the old file untouched", got)
	}
}

// TestExpansionBoundReadsTheBaseSize: the bound a client computes takes the
// artifact its caller holds into account (WithBaseSize), so a catalogue that
// outgrows the floor can never trip it - and an asset that declares no size says
// so in the log rather than only in a refresh that starts failing.
func TestExpansionBoundReadsTheBaseSize(t *testing.T) {
	var logged bytes.Buffer
	const size = 5 << 30 // past the floor, so the arm is visible
	c := New("owner/name", "", WithLogger(log.New(&logged, "", 0)), WithBaseSize(func() int64 { return size }))
	if got, want := c.expansionBound(DataAsset, 0), DecompressBound(0, size); got != want || want != size*currentArtifactRatio {
		t.Errorf("expansionBound with nothing declared = %d, want %d", got, want)
	}
	if !strings.Contains(logged.String(), "declares no size") {
		t.Errorf("log %q does not report the undeclared size", logged.String())
	}
	logged.Reset()
	if got, want := c.expansionBound(DataAsset, 1<<30), DecompressBound(1<<30, size); got != want {
		t.Errorf("expansionBound with a declared size = %d, want %d", got, want)
	}
	if logged.Len() != 0 {
		t.Errorf("a declared size logged %q", logged.String())
	}
}

// TestTempFile recognizes exactly the temp files an install writes.
func TestTempFile(t *testing.T) {
	dir := t.TempDir()
	// Leave one behind the way a killed process would: a failed install.
	if _, err := InstallVerified(bytes.NewReader([]byte("x")), filepath.Join(dir, "meta.sqlite"), "00", 0); err == nil {
		t.Fatal("a wrong digest installed")
	}
	f, err := os.CreateTemp(dir, tempPattern)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if !TempFile(filepath.Base(f.Name())) {
		t.Errorf("TempFile(%q) = false", filepath.Base(f.Name()))
	}
	for _, name := range []string{"meta.sqlite", "meta-data-1.sqlite", "state.json", ".meta-x.sqlite"} {
		if TempFile(name) {
			t.Errorf("TempFile(%q) = true", name)
		}
	}
}

// TestWorkflowMatchesTheSelection: release.yml's prev-release selection (what a
// patch is based on) must pick the release LatestData picks - by data-asset
// presence, drafts and prereleases excluded, by max published_at, over the same
// window - or the workflow bases its delta on a release no running server has
// loaded. Expected substrings are derived from the Go constants where one exists;
// the draft/prerelease jq predicate is pinned verbatim (an equivalent
// reformatting requires updating this test). metaserve's own half (the zstd
// window and the patch asset name) is internal/serve's TestWorkflowMatchesGoConstants.
func TestWorkflowMatchesTheSelection(t *testing.T) {
	wf, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(wf)
	if want := `.name == "` + DataAsset + `"`; !strings.Contains(s, want) {
		t.Errorf("release.yml prev selection does not filter releases by %q - it must pick the newest release carrying the data asset, not GitHub's latest", want)
	}
	if want := `select((.draft or .prerelease) | not)`; !strings.Contains(s, want) {
		t.Errorf("release.yml prev selection does not contain %q - drafts/prereleases must be excluded like LatestData does", want)
	}
	if want := `max_by(.published_at)`; !strings.Contains(s, want) {
		t.Errorf("release.yml prev selection does not contain %q - it must pick by publish time, not list position, like LatestData does", want)
	}
	if want := "per_page=" + strconv.Itoa(releaseListPageSize); !strings.Contains(s, want) {
		t.Errorf("release.yml prev selection does not contain %q - keep the scan window in sync with releaseListPageSize", want)
	}
	for _, name := range []string{DataDigestAsset, RawDigestAsset} {
		if !strings.Contains(s, name) {
			t.Errorf("release.yml does not publish %s", name)
		}
	}
}
