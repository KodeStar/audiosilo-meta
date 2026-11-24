package serve

import (
	"bytes"
	"context"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kodestar/audiosilo-meta/internal/artifacttest"
	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/release"
	"github.com/kodestar/audiosilo-meta/pkg/release/releasetest"
)

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

// v2Catalog is artifacttest.Fixture plus one extra work, so its artifact's stats.Works
// differs from the base fixture (4 -> 5) - the observable signal that a
// refresh actually adopted the newer artifact.
func v2Catalog() *model.Catalog {
	cat := artifacttest.Fixture()
	cat.Works = append(cat.Works, &model.Work{
		ID: "the-martian", Title: "The Martian", Language: "en",
		Authors: []string{"andy-weir"}, License: "CC0-1.0",
	})
	return cat
}

// buildV1V2 builds the two artifacts the refresh tests need, ONCE each: v1 (the
// base fixture, 4 works) and v2 (one extra work, 5). v1 comes back as (path,
// bytes) - the path seeds a poll server, the bytes are a release asset - and v2
// as bytes only.
func buildV1V2(t *testing.T) (v1Path string, v1, v2 []byte) {
	t.Helper()
	v1Path = artifacttest.Build(t, artifacttest.Fixture())
	return v1Path, readDB(t, v1Path), readDB(t, artifacttest.Build(t, v2Catalog()))
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

// setupR1 arranges the standard refresh-test starting point from prebuilt v1
// artifacts: a fake publishing v1 under tagR1, a server seeded from v1Path, and
// one full refresh so the server is loaded on R1 (its snapshot path pointing at
// the cached R1 file).
func setupR1(t *testing.T, v1Path string, v1 []byte) (*Server, *releasetest.GitHub) {
	t.Helper()
	fake := releasetest.NewGitHub(t, releasetest.Rel{Tag: tagR1, Assets: releasetest.DataAssets(t, v1)})
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
	fake := releasetest.NewGitHub(t, releasetest.Rel{Tag: tagR1, Assets: releasetest.DataAssets(t, readDB(t, seed))})
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
	assets := releasetest.DataAssets(t, readDB(t, seed))
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

// TestRefreshRetriesAfterFailedDownload pins the ETag-forget behavior: a 200
// release fetch stores the new ETag before the assets are secured, so a failed
// download must clear it - otherwise every subsequent poll 304s and the server
// is stranded on the old artifact until the NEXT release is cut.
func TestRefreshRetriesAfterFailedDownload(t *testing.T) {
	v1Path, v1, v2 := buildV1V2(t)
	srv, fake := setupR1(t, v1Path, v1)

	// R2 is published, but its gz download 500s: the refresh must error and
	// nothing may be adopted.
	fake.Publish(tagR2, releasetest.DataAssets(t, v2))
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
// release CARRYING the data asset. A code release plus a draft and a prerelease
// data decoy sit in front of the real R2, pinning each branch of the filter.
func TestRefreshSkipsNonDataReleases(t *testing.T) {
	v1Path, v1, v2 := buildV1V2(t)
	srv, fake := setupR1(t, v1Path, v1)

	fake.SetReleases(codeOnlyRel, draftDecoy, preRelDecoy, releasetest.Rel{Tag: tagR2, Assets: releasetest.DataAssets(t, v2)})
	if err := srv.refresh(context.Background()); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if srv.loaded != tagR2 {
		t.Errorf("loaded = %q, want %q (code/draft/prerelease entries must be skipped)", srv.loaded, tagR2)
	}
	if got := srv.current().Stats().Works; got != 5 {
		t.Errorf("works = %d, want 5 (v2 adopted)", got)
	}
	if got := fake.Hits(release.DataAsset); got != 1 {
		t.Errorf("gz fetched %d times, want 1", got)
	}
}

// TestRefreshIgnoresALegacyPatchAsset: data releases up to 2026-11-19 also
// carried a zstd --patch-from delta (meta.sqlite.patch.from-<prev tag>.zst),
// which metaserve no longer reads. A release still carrying one, even a delta
// based on exactly the loaded tag, is adopted through the gz anchor and the
// delta is never fetched.
func TestRefreshIgnoresALegacyPatchAsset(t *testing.T) {
	v1Path, v1, v2 := buildV1V2(t)
	srv, fake := setupR1(t, v1Path, v1)

	legacy := "meta.sqlite.patch.from-" + tagR1 + ".zst"
	assets := releasetest.DataAssets(t, v2)
	assets[legacy] = []byte("a delta nobody reads")
	fake.Publish(tagR2, assets)
	if err := srv.refresh(context.Background()); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if srv.loaded != tagR2 {
		t.Errorf("loaded = %q, want %q", srv.loaded, tagR2)
	}
	if got := srv.current().Stats().Works; got != 5 {
		t.Errorf("works = %d, want 5 (v2 adopted)", got)
	}
	if got := fake.Hits(release.DataAsset); got != 1 {
		t.Errorf("gz fetched %d times, want 1", got)
	}
	if got := fake.Hits(legacy); got != 0 {
		t.Errorf("the legacy delta was fetched %d times, want 0", got)
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
	v1Path, v1, v2 := buildV1V2(t)

	// Same-day tags mirroring the production incident: the stale tag sorts
	// lexicographically ABOVE the newer one, but was published earlier.
	const (
		staleTag = "data-v2026.07.12-ddea3bc" // published 17:09, lexicographic max
		newerTag = "data-v2026.07.12-6f10608" // published 23:33, the actual newest
	)
	day := time.Date(2026, 7, 12, 0, 0, 0, 0, time.UTC)
	staleRel := releasetest.Rel{Tag: staleTag, Published: day.Add(17*time.Hour + 9*time.Minute), Assets: releasetest.DataAssets(t, v1)}
	newerRel := releasetest.Rel{Tag: newerTag, Published: day.Add(23*time.Hour + 33*time.Minute), Assets: releasetest.DataAssets(t, v2)}

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
// the LATER-in-list release wins, matching jq's max_by (a stable sort, so it
// returns the last of a tied pair) - the selector a shell consumer of the
// release list writes, so the two agree even on a same-second tie.
func TestRefreshTieBreaksLikeJq(t *testing.T) {
	v1Path, v1, v2 := buildV1V2(t)

	tie := time.Date(2026, 7, 12, 17, 9, 32, 0, time.UTC)
	first := releasetest.Rel{Tag: "data-v2026.07.12-aaaaaaa", Published: tie, Assets: releasetest.DataAssets(t, v1)}
	second := releasetest.Rel{Tag: "data-v2026.07.12-bbbbbbb", Published: tie, Assets: releasetest.DataAssets(t, v2)}

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

// TestRefreshPrunesCache covers the cache-volume leak: every adopted release
// used to leave its predecessor's full artifact on disk forever. After the swap
// grace elapses only the live artifact may remain - and the streamed download's
// transient .gz must be gone as soon as the refresh that created it finished.
func TestRefreshPrunesCache(t *testing.T) {
	v1Path, v1, v2 := buildV1V2(t)
	cache := t.TempDir()
	fake := releasetest.NewGitHub(t, releasetest.Rel{Tag: tagR1, Assets: releasetest.DataAssets(t, v1)})
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

	fake.Publish(tagR2, releasetest.DataAssets(t, v2))
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
	v1 := readDB(t, artifacttest.Build(t, artifacttest.Fixture()))
	fake := releasetest.NewGitHub(t, releasetest.Rel{Tag: tagR1, Assets: releasetest.DataAssets(t, v1)})
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
	v1Path, _, v2 := buildV1V2(t)
	fake := releasetest.NewGitHub(t, releasetest.Rel{Tag: tagR2, Assets: releasetest.DataAssets(t, v2)})
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

// TestFullRefreshSurvivesAnUndeclaredAssetSize is the finding's teeth. The bound
// used to collapse onto a 1 GiB floor whenever the release declared no size -
// below the ~1.6 GB artifact it was bounding - so every full refresh would have
// failed forever, re-downloading the whole asset each poll and writing
// maxBytes+1 bytes to the cache volume each time.
func TestFullRefreshSurvivesAnUndeclaredAssetSize(t *testing.T) {
	var logged bytes.Buffer
	v1Path := artifacttest.Build(t, artifacttest.Fixture())
	fake := releasetest.NewGitHub(t, releasetest.Rel{Tag: tagR1, Assets: releasetest.DataAssets(t, readDB(t, v1Path))})
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
