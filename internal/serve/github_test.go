package serve

import (
	"bytes"
	"context"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/kodestar/audiosilo-meta/internal/artifacttest"
	"github.com/kodestar/audiosilo-meta/internal/releasetest"
	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/release"
)

// writeFile writes data to a fresh file under dir and returns its path.
func writeFile(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestNextBootBackoff pins the boot-retry schedule: start at bootRetry, double
// per consecutive failure, never exceed the steady-state interval, and start
// over once an artifact loads (prev 0).
func TestNextBootBackoff(t *testing.T) {
	const base, limit = 30 * time.Second, 8 * time.Minute
	want := []time.Duration{30 * time.Second, time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 8 * time.Minute}
	got := time.Duration(0)
	for i, w := range want {
		got = nextBootBackoff(got, base, limit)
		if got != w {
			t.Fatalf("attempt %d: backoff = %s, want %s", i+1, got, w)
		}
	}
	// A success resets to the base, and a base above the limit is clamped.
	if got := nextBootBackoff(0, base, limit); got != base {
		t.Errorf("after reset = %s, want %s", got, base)
	}
	if got := nextBootBackoff(0, time.Hour, limit); got != limit {
		t.Errorf("base over the limit = %s, want %s", got, limit)
	}
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
	assets := releasetest.DataAssets(t, db)
	if patchFrom != "" && prev != nil {
		assets[patchAssetName(patchFrom)] = makePatch(t, prev, db)
	}
	return assets
}

// v2Catalog is artifacttest.Fixture plus one extra work, so its artifact's stats.Works
// differs from the base fixture (4 -> 5) - the observable signal that a patched
// refresh actually adopted the newer artifact.
func v2Catalog() *model.Catalog {
	cat := artifacttest.Fixture()
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
	v1Path = artifacttest.Build(t, artifacttest.Fixture())
	v2Path = artifacttest.Build(t, v2Catalog())
	return v1Path, readDB(t, v1Path), v2Path, readDB(t, v2Path)
}

// newPollServer seeds a server from a local artifact (so New() doesn't poll) and
// points its GitHub client at the fake.
func newPollServer(t *testing.T, seed string, fake *releasetest.GitHub) *Server {
	t.Helper()
	return newPollServerIn(t, seed, t.TempDir(), fake, time.Minute)
}

// newPollServerIn is newPollServer with the cache directory and the swap grace
// chosen by the test.
func newPollServerIn(t *testing.T, seed, cache string, fake *releasetest.GitHub, grace time.Duration) *Server {
	t.Helper()
	srv, err := New(Config{DBPath: seed, Repo: releasetest.Repo, CacheDir: cache, swapGrace: grace})
	if err != nil {
		t.Fatal(err)
	}
	srv.gh = srv.newGHClient(releasetest.Repo, fake.URL)
	return srv
}

const (
	tagR1 = "data-v2026.07.11-r1"
	tagR2 = "data-v2026.07.12-r2"
)

// codeOnlyRel mimics a code/image release (v*): newest in the list, no data
// assets - release selection must skip it.
var codeOnlyRel = releasetest.Rel{Tag: "v0.2.0", Assets: map[string][]byte{
	"metaserve-linux-amd64.tar.gz": []byte("a binary, not data"),
}}

// preRelDecoy mimics a prerelease DATA release: it advertises the data asset
// (selection only looks at name presence; nothing ever downloads it), but the
// prerelease flag must exclude it - adopting it would flip loaded to its tag.
var preRelDecoy = releasetest.Rel{
	Tag:        "data-v2026.07.13-pre",
	Prerelease: true,
	Assets:     map[string][]byte{release.DataAsset: nil},
}

// draftDecoy is preRelDecoy's sibling for the Draft branch of the filter: a
// draft release carrying the data asset name that must likewise be skipped.
var draftDecoy = releasetest.Rel{
	Tag:    "data-vdraft",
	Draft:  true,
	Assets: map[string][]byte{release.DataAsset: nil},
}

// setupR1 arranges the standard patch-test starting point from prebuilt v1
// artifacts: a fake publishing v1 under tagR1, a server seeded from v1Path, and
// one full refresh so the server is loaded on R1 (its snapshot path pointing at
// the cached R1 file, ready to be a patch base).
func setupR1(t *testing.T, v1Path string, v1 []byte) (*Server, *releasetest.GitHub) {
	t.Helper()
	fake := releasetest.NewGitHub(t, releasetest.Rel{Tag: tagR1, Assets: makeAssets(t, v1, "", nil)})
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
	seed := artifacttest.Build(t, artifacttest.Fixture())
	fake := releasetest.NewGitHub(t, releasetest.Rel{Tag: tagR1, Assets: makeAssets(t, readDB(t, seed), "", nil)})
	srv := newPollServer(t, seed, fake)

	// First refresh: no loaded tag yet, so the full path downloads and swaps.
	if err := srv.refresh(context.Background()); err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	if fake.Lists() != 1 {
		t.Errorf("expected 1 full release fetch, got %d", fake.Lists())
	}
	if srv.loaded != tagR1 {
		t.Errorf("loaded tag = %q", srv.loaded)
	}

	// Second refresh: the stored ETag yields a 304, so no re-download or swap.
	if err := srv.refresh(context.Background()); err != nil {
		t.Fatalf("second refresh: %v", err)
	}
	if fake.NotModified() != 1 {
		t.Errorf("expected a 304 on the second poll, got %d", fake.NotModified())
	}
	if fake.Hits(release.DataAsset) != 1 {
		t.Errorf("gz downloaded %d times, want exactly 1", fake.Hits(release.DataAsset))
	}
}

func TestRefreshRejectsCorruptDownload(t *testing.T) {
	seed := artifacttest.Build(t, artifacttest.Fixture())
	assets := makeAssets(t, readDB(t, seed), "", nil)
	// A checksum that does not match the gz payload.
	assets[release.DataDigestAsset] = []byte("deadbeef  " + release.DataAsset + "\n")
	fake := releasetest.NewGitHub(t, releasetest.Rel{Tag: tagR1, Assets: assets})
	srv := newPollServer(t, seed, fake)

	if err := srv.refresh(context.Background()); err == nil {
		t.Fatalf("expected refresh to reject a checksum mismatch")
	}
	// The bad release must not have been adopted.
	if srv.loaded != "" {
		t.Errorf("loaded tag = %q, want empty after rejected refresh", srv.loaded)
	}
	if srv.current().Stats().Works != 4 {
		t.Errorf("serving snapshot changed after a rejected refresh")
	}
}

func TestRefreshPatchHappyPath(t *testing.T) {
	v1Path, v1, _, v2 := buildV1V2(t)
	srv, fake := setupR1(t, v1Path, v1)

	// R2 ships a delta from R1; the poller should patch, not full-download.
	fake.Publish(tagR2, makeAssets(t, v2, tagR1, v1))
	if err := srv.refresh(context.Background()); err != nil {
		t.Fatalf("patch refresh: %v", err)
	}
	if srv.loaded != tagR2 {
		t.Errorf("loaded = %q, want %q", srv.loaded, tagR2)
	}
	if got := srv.current().Stats().Works; got != 5 {
		t.Errorf("works = %d, want 5 (v2 adopted)", got)
	}
	// The reconstructed artifact must be byte-identical to the real v2.
	patched := readDB(t, srv.current().Path())
	if !bytes.Equal(patched, v2) {
		t.Errorf("patched artifact (%d bytes) differs from v2 (%d bytes)", len(patched), len(v2))
	}
	// The gz anchor must never have been fetched for R2 (patch path only).
	if got := fake.Hits(release.DataAsset); got != 0 {
		t.Errorf("gz fetched %d times during patch refresh, want 0", got)
	}
	if got := fake.Hits(patchAssetName(tagR1)); got != 1 {
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
				a[release.RawDigestAsset] = []byte("deadbeef  meta.sqlite\n")
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
				delete(a, release.RawDigestAsset)
				return a
			},
			wantPatchHits: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, fake := setupR1(t, v1Path, v1)

			fake.Publish(tagR2, tc.build(t))
			if err := srv.refresh(context.Background()); err != nil {
				t.Fatalf("refresh should succeed via fallback: %v", err)
			}
			if srv.loaded != tagR2 {
				t.Errorf("loaded = %q, want %q", srv.loaded, tagR2)
			}
			if got := srv.current().Stats().Works; got != 5 {
				t.Errorf("works = %d, want 5 (v2 adopted via full download)", got)
			}
			if got := fake.Hits(release.DataAsset); got != 1 {
				t.Errorf("gz fetched %d times, want 1 (full fallback used)", got)
			}
			if got := fake.Hits(patchAssetName(tagR1)); got != tc.wantPatchHits {
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
	fake.Publish(tagR2, makeAssets(t, v2, "", nil))
	fake.SetAssetFailure(release.DataAsset, true)
	if err := srv.refresh(context.Background()); err == nil {
		t.Fatal("expected refresh to fail while the asset download 500s")
	}
	if srv.loaded != tagR1 {
		t.Fatalf("loaded = %q, want still %q after a failed refresh", srv.loaded, tagR1)
	}

	// The asset heals (same release, same ETag). The next refresh must retry
	// and succeed - before the etag-forget fix it 304-ed and no-oped forever.
	fake.SetAssetFailure(release.DataAsset, false)
	if err := srv.refresh(context.Background()); err != nil {
		t.Fatalf("refresh after the asset healed: %v", err)
	}
	if srv.loaded != tagR2 {
		t.Errorf("loaded = %q, want %q (retry must not be 304-blocked)", srv.loaded, tagR2)
	}
	if got := srv.current().Stats().Works; got != 5 {
		t.Errorf("works = %d, want 5 (v2 adopted on retry)", got)
	}
}

// TestRefreshSkipsNonDataReleases pins the selection criterion: the repo also
// cuts code/image releases (v*, no data assets) and GitHub's newest release can
// be one of them, so the poller must adopt the newest non-draft, non-prerelease
// release CARRYING the data asset - on both the patch and the full path.
func TestRefreshSkipsNonDataReleases(t *testing.T) {
	// One pair of artifacts serves both subtests; a code release plus a draft
	// and a prerelease data decoy sit in front of the real R2 in every case,
	// pinning each branch of the filter.
	v1Path, v1, _, v2 := buildV1V2(t)

	cases := []struct {
		name      string
		patchFrom string // "" = R2 ships no delta (full path)
		wantGz    int
		wantPatch int
	}{
		{name: "patch path", patchFrom: tagR1, wantGz: 0, wantPatch: 1},
		{name: "full path", patchFrom: "", wantGz: 1, wantPatch: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, fake := setupR1(t, v1Path, v1)

			fake.SetReleases(codeOnlyRel, draftDecoy, preRelDecoy, releasetest.Rel{Tag: tagR2, Assets: makeAssets(t, v2, tc.patchFrom, v1)})
			if err := srv.refresh(context.Background()); err != nil {
				t.Fatalf("refresh: %v", err)
			}
			if srv.loaded != tagR2 {
				t.Errorf("loaded = %q, want %q (code/draft/prerelease entries must be skipped)", srv.loaded, tagR2)
			}
			if got := srv.current().Stats().Works; got != 5 {
				t.Errorf("works = %d, want 5 (v2 adopted)", got)
			}
			if got := fake.Hits(release.DataAsset); got != tc.wantGz {
				t.Errorf("gz fetched %d times, want %d", got, tc.wantGz)
			}
			if got := fake.Hits(patchAssetName(tagR1)); got != tc.wantPatch {
				t.Errorf("patch fetched %d times, want %d", got, tc.wantPatch)
			}
		})
	}
}

// TestRefreshSelectsByPublishedAt is the regression test for the stale-artifact
// bug: GitHub's release list order is NOT publish-chronological (see the
// release.Client.LatestData doc comment for the observed order), so the first-listed
// data release is not necessarily the newest. The list here mimics that order -
// a leading code release, then a STALE data release (older published_at,
// lexicographically-larger tag) BEFORE the real newest - and the poller must
// adopt the newer-published one, not the first-listed.
func TestRefreshSelectsByPublishedAt(t *testing.T) {
	v1Path, v1, _, v2 := buildV1V2(t)

	// Same-day tags mirroring the production incident: the stale tag sorts
	// lexicographically ABOVE the newer one, but was published earlier.
	const (
		staleTag = "data-v2026.07.12-ddea3bc" // published 17:09, lexicographic max
		newerTag = "data-v2026.07.12-6f10608" // published 23:33, the actual newest
	)
	day := time.Date(2026, 7, 12, 0, 0, 0, 0, time.UTC)
	staleRel := releasetest.Rel{Tag: staleTag, Published: day.Add(17*time.Hour + 9*time.Minute), Assets: makeAssets(t, v1, "", nil)}
	newerRel := releasetest.Rel{Tag: newerTag, Published: day.Add(23*time.Hour + 33*time.Minute), Assets: makeAssets(t, v2, "", nil)}

	fake := releasetest.NewGitHub(t, releasetest.Rel{Tag: staleTag})
	// List order = observed GitHub order: code release first, then stale before
	// newer (reverse-lexicographic within the day).
	fake.SetReleases(codeOnlyRel, staleRel, newerRel)

	srv := newPollServer(t, v1Path, fake)
	if err := srv.refresh(context.Background()); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if srv.loaded != newerTag {
		t.Errorf("loaded = %q, want %q (must select max published_at, not the first-listed data release)", srv.loaded, newerTag)
	}
	if got := srv.current().Stats().Works; got != 5 {
		t.Errorf("works = %d, want 5 (the newer-published v2 artifact adopted)", got)
	}
	// The stale release's gz must never have been downloaded.
	if got := fake.Hits(release.DataAsset); got != 1 {
		t.Errorf("gz fetched %d times, want 1 (only the newer release)", got)
	}
}

// TestRefreshTieBreaksLikeJq pins the tie-break on an exact published_at tie:
// the LATER-in-list release wins, matching release.yml's jq (max_by is a stable
// sort, so it returns the last of a tied pair). If the two selectors disagreed
// on ties, the workflow could base its patch delta on a release no running
// server has loaded, silently defeating patch refresh for that release.
func TestRefreshTieBreaksLikeJq(t *testing.T) {
	v1Path, v1, _, v2 := buildV1V2(t)

	tie := time.Date(2026, 7, 12, 17, 9, 32, 0, time.UTC)
	first := releasetest.Rel{Tag: "data-v2026.07.12-aaaaaaa", Published: tie, Assets: makeAssets(t, v1, "", nil)}
	second := releasetest.Rel{Tag: "data-v2026.07.12-bbbbbbb", Published: tie, Assets: makeAssets(t, v2, "", nil)}

	fake := releasetest.NewGitHub(t, releasetest.Rel{Tag: first.Tag})
	fake.SetReleases(first, second)

	srv := newPollServer(t, v1Path, fake)
	if err := srv.refresh(context.Background()); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if srv.loaded != second.Tag {
		t.Errorf("loaded = %q, want %q (later-in-list must win a published_at tie, matching jq max_by)", srv.loaded, second.Tag)
	}
	if got := srv.current().Stats().Works; got != 5 {
		t.Errorf("works = %d, want 5 (the later-listed v2 artifact adopted)", got)
	}
}

// TestRefreshErrorsWithoutDataRelease pins the none-found behavior: a release
// list holding only code releases yields a descriptive error and the serving
// snapshot is untouched.
func TestRefreshErrorsWithoutDataRelease(t *testing.T) {
	seed := artifacttest.Build(t, artifacttest.Fixture())
	srv, fake := setupR1(t, seed, readDB(t, seed))

	fake.SetReleases(codeOnlyRel)
	err := srv.refresh(context.Background())
	if err == nil {
		t.Fatal("expected refresh to error when no data release exists")
	}
	if !strings.Contains(err.Error(), "no data release") {
		t.Errorf("error = %v, want a descriptive no-data-release error", err)
	}
	if srv.loaded != tagR1 {
		t.Errorf("loaded = %q, want still %q", srv.loaded, tagR1)
	}
	if got := srv.current().Stats().Works; got != 4 {
		t.Errorf("works = %d, want 4 (snapshot unchanged)", got)
	}
}

// TestWorkflowMatchesGoConstants pins the bash<->Go contract on the PATCH side:
// the release workflow's zstd window and patch-asset filename must match
// patchWindowLog and patchAssetName. A bump/rename on one side now fails tests
// instead of silently killing the delta feature (the poller would never find a
// matching asset). The prev-release SELECTION the patch is based on must match
// pkg/release's LatestData, which is that package's
// TestWorkflowMatchesTheSelection.
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

// TestRefreshPrunesCache covers the cache-volume leak: every adopted release
// used to leave its predecessor's full artifact on disk forever. After the swap
// grace elapses only the live artifact may remain - and the streamed download's
// transient .gz must be gone as soon as the refresh that created it finished.
func TestRefreshPrunesCache(t *testing.T) {
	v1Path, v1, _, v2 := buildV1V2(t)
	cache := t.TempDir()
	fake := releasetest.NewGitHub(t, releasetest.Rel{Tag: tagR1, Assets: makeAssets(t, v1, "", nil)})
	srv, err := New(Config{DBPath: v1Path, Repo: "owner/name", CacheDir: cache, swapGrace: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	srv.gh = srv.newGHClient("owner/name", fake.URL)

	if err := srv.refresh(context.Background()); err != nil {
		t.Fatalf("R1 refresh: %v", err)
	}
	r1File := filepath.Base(srv.dbCachePath(tagR1))
	// The gz the artifact was streamed through is removed by the refresh itself.
	if got := releasetest.DirEntries(t, cache); len(got) != 1 || got[0] != r1File {
		t.Errorf("cache after R1 = %v, want just [%s]", got, r1File)
	}

	fake.Publish(tagR2, makeAssets(t, v2, "", nil))
	if err := srv.refresh(context.Background()); err != nil {
		t.Fatalf("R2 refresh: %v", err)
	}
	r2File := filepath.Base(srv.dbCachePath(tagR2))

	// The prune runs on the swap-grace timer, so wait for it rather than
	// assuming it has already fired.
	deadline := time.Now().Add(10 * time.Second)
	for {
		got := releasetest.DirEntries(t, cache)
		if len(got) == 1 && got[0] == r2File {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("cache = %v, want just the live artifact [%s]", got, r2File)
		}
		time.Sleep(10 * time.Millisecond)
	}
	// The superseded artifact is gone but the live one still opens.
	if got := srv.current().Stats().Works; got != 5 {
		t.Errorf("works = %d, want 5 after the prune", got)
	}
}

// TestPatchSkippedOverBaseCap pins the memory guard on the patch path: the base
// artifact has to be held in memory as a raw zstd dictionary, so past the cap
// the refresh must decline the patch and fall back to the (fully streamed) gz
// download rather than allocate it.
func TestPatchSkippedOverBaseCap(t *testing.T) {
	v1Path, v1, _, v2 := buildV1V2(t)
	fake := releasetest.NewGitHub(t, releasetest.Rel{Tag: tagR1, Assets: makeAssets(t, v1, "", nil)})
	srv, err := New(Config{
		DBPath: v1Path, Repo: "owner/name", CacheDir: t.TempDir(),
		swapGrace: time.Minute, maxPatchBase: 1, // every artifact is over a 1-byte cap
	})
	if err != nil {
		t.Fatal(err)
	}
	srv.gh = srv.newGHClient("owner/name", fake.URL)
	if err := srv.refresh(context.Background()); err != nil {
		t.Fatalf("R1 refresh: %v", err)
	}

	// R2 ships a usable delta from R1; the cap must make us ignore it.
	fake.Publish(tagR2, makeAssets(t, v2, tagR1, v1))
	if err := srv.refresh(context.Background()); err != nil {
		t.Fatalf("R2 refresh: %v", err)
	}
	if srv.loaded != tagR2 {
		t.Errorf("loaded = %q, want %q (full download fallback)", srv.loaded, tagR2)
	}
	if got := fake.Hits(patchAssetName(tagR1)); got != 0 {
		t.Errorf("patch fetched %d times, want 0 (over the base cap)", got)
	}
	if got := fake.Hits(release.DataAsset); got != 1 {
		t.Errorf("gz fetched %d times, want 1", got)
	}
	if got := srv.current().Stats().Works; got != 5 {
		t.Errorf("works = %d, want 5 (v2 adopted via full download)", got)
	}
}

// TestBootWithoutDataServesDegraded is the D3 failure mode: the production image
// ships no baked artifact, so a boot that cannot reach GitHub must come up
// degraded (site served, API 503, /healthz "starting") instead of failing to
// construct - which in a container would be a crash loop. It recovers as soon as
// a release becomes reachable.
func TestBootWithoutDataServesDegraded(t *testing.T) {
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "GitHub is having a day", http.StatusInternalServerError)
	}))
	t.Cleanup(down.Close)

	srv, err := New(Config{
		Poll: true, Repo: "owner/name", CacheDir: t.TempDir(), Site: writeSiteFixture(t),
		apiBase: down.URL, swapGrace: time.Minute, bootRetry: time.Hour, // no background retry in this test
	})
	if err != nil {
		t.Fatalf("New must not fail when the first fetch fails: %v", err)
	}
	if srv.current() != nil {
		t.Fatal("expected no snapshot after a failed boot fetch")
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	code, body := artifacttest.GetJSON(t, ts.URL, "/healthz")
	if code != http.StatusServiceUnavailable {
		t.Errorf("healthz status = %d, want 503", code)
	}
	if body["status"] != "starting" {
		t.Errorf("healthz body = %v, want status starting", body)
	}
	code, body = artifacttest.GetJSON(t, ts.URL, "/api/v1/stats")
	if code != http.StatusServiceUnavailable {
		t.Errorf("stats status = %d, want 503", code)
	}
	if body["error"] == nil {
		t.Errorf("503 body = %v, want an error message", body)
	}
	// The site is catalogue-independent and must still be served.
	if code, page := getBody(t, ts.URL+"/"); code != 200 || !strings.Contains(page, "LANDING") {
		t.Errorf("site while degraded = %d %q, want the landing page", code, page)
	}

	// Once a release is reachable, the same process becomes healthy.
	_, v1, _, _ := buildV1V2(t)
	fake := releasetest.NewGitHub(t, releasetest.Rel{Tag: tagR1, Assets: makeAssets(t, v1, "", nil)})
	srv.gh = srv.newGHClient("owner/name", fake.URL)
	if err := srv.refresh(context.Background()); err != nil {
		t.Fatalf("recovery refresh: %v", err)
	}
	if code, body := artifacttest.GetJSON(t, ts.URL, "/healthz"); code != 200 || body["status"] != "ok" {
		t.Errorf("healthz after recovery = %d %v, want 200 ok", code, body)
	}
}

// TestPollLoopRefreshesAtStartup pins the production Docker boot: New() loads a
// baked --db artifact (tag "", so it does NOT run the poll-only synchronous
// first refresh), then Run() starts pollLoop. pollLoop must refresh IMMEDIATELY,
// well before the first Interval tick, or a recreated container serves
// build-time data for one full interval. The server here is seeded from v1, the
// fake publishes a newer R2 release, and Interval defaults to an hour - so ONLY
// the startup refresh can adopt R2 within the test's seconds-long deadline.
func TestPollLoopRefreshesAtStartup(t *testing.T) {
	v1Path, _, _, v2 := buildV1V2(t)
	fake := releasetest.NewGitHub(t, releasetest.Rel{Tag: tagR2, Assets: makeAssets(t, v2, "", nil)})
	srv := newPollServer(t, v1Path, fake) // Interval defaults to time.Hour

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		srv.pollLoop(ctx)
		close(done)
	}()

	// The startup refresh must adopt R2 within seconds (never the 1h tick). Read
	// through the atomic snapshot pointer (tag/stats are immutable per snapshot),
	// so this poll is race-safe against pollLoop's concurrent swap. The deadline
	// is only a cap (the loop exits as soon as R2 lands) - keep it generous so a
	// loaded -race CI runner can't flake it.
	deadline := time.Now().Add(10 * time.Second)
	for srv.current().Info().Tag != tagR2 {
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatalf("pollLoop did not refresh to %q at startup within the deadline", tagR2)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := srv.current().Stats().Works; got != 5 {
		t.Errorf("works = %d, want 5 (v2 adopted at startup)", got)
	}

	// Cancelling ctx must make pollLoop return promptly (no goroutine leak).
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("pollLoop did not return after ctx cancel")
	}
}

func TestApplyPatchFile(t *testing.T) {
	v1Path, v1, _, v2 := buildV1V2(t)
	patch := makePatch(t, v1, v2)

	t.Run("happy", func(t *testing.T) {
		dir := t.TempDir()
		patchPath := writeFile(t, dir, "patch.zst", patch)
		dst := filepath.Join(dir, "out", "meta.sqlite")
		n, err := applyPatchFile(patchPath, v1Path, dst, releasetest.Digest(v2), patchBound(v1))
		if err != nil {
			t.Fatalf("applyPatchFile: %v", err)
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
		patchPath := writeFile(t, dir, "patch.zst", patch)
		dst := filepath.Join(dir, "meta.sqlite")
		if _, err := applyPatchFile(patchPath, v1Path, dst, "deadbeef", patchBound(v1)); err == nil {
			t.Fatal("expected a hash mismatch error")
		}
		if _, err := os.Stat(dst); !os.IsNotExist(err) {
			t.Errorf("dst was created despite a hash mismatch")
		}
		// No leftover temp files in the dst directory.
		for _, name := range releasetest.DirEntries(t, dir) {
			if strings.HasPrefix(name, ".meta-") {
				t.Errorf("leftover temp file %q after failed apply", name)
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
	info, err := os.Stat(patchPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("CLI patch size = %d bytes (v2 artifact = %d bytes)", info.Size(), len(v2))

	dst := filepath.Join(dir, "out.sqlite")
	if _, err := applyPatchFile(patchPath, v1Path, dst, releasetest.Digest(v2), release.DecompressBound(0, 0)); err != nil {
		t.Fatalf("applyPatchFile on CLI frame: %v", err)
	}
	if got := readDB(t, dst); !bytes.Equal(got, v2) {
		t.Errorf("CLI-frame patched result differs from v2")
	}
}

// TestCurrentArtifactBytesReadsTheLoadedArtifact: the size this server hands its
// release client as the expansion bound's base (release.WithBaseSize) is the
// artifact it is serving, so a catalogue that outgrows the floor can never trip
// the bound. The arithmetic and the "declares no size" notice are pkg/release's
// TestExpansionBoundReadsTheBaseSize; that the notice reaches THIS server's log
// is TestFullRefreshSurvivesAnUndeclaredAssetSize.
func TestCurrentArtifactBytesReadsTheLoadedArtifact(t *testing.T) {
	srv, err := New(Config{DBPath: artifacttest.Build(t, artifacttest.Fixture()), Logger: artifacttest.QuietLogger()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.current().Close() })

	if size := srv.currentArtifactBytes(); size <= 0 {
		t.Fatalf("the loaded artifact reports %d bytes", size)
	}
}

// TestApplyPatchFileIsBounded is the same bound on the patch path, where the
// output size is decided by a zstd frame rather than by a gzip one. The base
// artifact's size is what it is measured against - the one size this path knows
// exactly.
func TestApplyPatchFileIsBounded(t *testing.T) {
	dir := t.TempDir()
	base := bytes.Repeat([]byte("base"), 16) // 64 bytes
	basePath := writeFile(t, dir, "base.bin", base)
	next := bytes.Repeat([]byte("next"), 1<<16) // 256 KiB, far past 8x the base
	patchPath := writeFile(t, dir, "patch.zst", makePatch(t, base, next))

	dst := filepath.Join(dir, "out.bin")
	// Floor 1, so the bound really is the ratio over the base's 64 bytes - what
	// tryPatch computes from the base artifact it stat'd.
	if _, err := applyPatchFile(patchPath, basePath, dst, releasetest.Digest(next), int64(len(base))*2); err == nil {
		t.Fatal("a patch expanding far past its base was installed")
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Errorf("destination created despite the bound")
	}
	// An output exactly AT the bound is legitimate rather than suspicious.
	if _, err := applyPatchFile(patchPath, basePath, dst, releasetest.Digest(next), int64(len(next))); err != nil {
		t.Errorf("a patch landing exactly on the bound was refused: %v", err)
	}
	// With the production floor the same patch is an ordinary, tiny refresh.
	if _, err := applyPatchFile(patchPath, basePath, dst, releasetest.Digest(next), patchBound(base)); err != nil {
		t.Errorf("a patch well inside the floor was refused: %v", err)
	}
}

// patchBound is tryPatch's own bound for a base artifact, so a test never
// invents an arithmetic of its own.
func patchBound(base []byte) int64 {
	return release.DecompressBound(0, int64(len(base)))
}

// TestFullRefreshSurvivesAnUndeclaredAssetSize is the finding's teeth. The bound
// used to collapse onto a 1 GiB floor whenever the release declared no size -
// below the ~1.6 GB artifact it was bounding - so every full refresh would have
// failed forever, re-downloading the whole asset each poll and writing
// maxBytes+1 bytes to the cache volume each time.
func TestFullRefreshSurvivesAnUndeclaredAssetSize(t *testing.T) {
	var logged bytes.Buffer
	v1Path := artifacttest.Build(t, artifacttest.Fixture())
	fake := releasetest.NewGitHub(t, releasetest.Rel{Tag: tagR1, Assets: makeAssets(t, readDB(t, v1Path), "", nil)})
	srv, err := New(Config{DBPath: v1Path, Repo: "owner/name", CacheDir: t.TempDir(),
		swapGrace: time.Minute, Logger: log.New(&logged, "", 0)})
	if err != nil {
		t.Fatal(err)
	}
	srv.gh = srv.newGHClient("owner/name", fake.URL)

	rel, _, _, err := srv.gh.LatestData(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	// The same release as GitHub would report it if it declared no sizes.
	for i := range rel.Assets {
		rel.Assets[i].Size = 0
	}
	if err := srv.fullRefresh(context.Background(), rel); err != nil {
		t.Fatalf("a refresh whose assets declare no size failed: %v", err)
	}
	if got := srv.current().Info().Tag; got != tagR1 {
		t.Errorf("loaded tag = %q, want %q", got, tagR1)
	}
	if !strings.Contains(logged.String(), "declares no size") {
		t.Errorf("log %q does not report the undeclared size", logged.String())
	}
}
