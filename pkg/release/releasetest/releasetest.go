// Package releasetest is test support for the data-release fetcher: ONE fake
// of GitHub's releases API, and the checksum and gzip helpers its assets are
// built with. pkg/release's own tests and metaserve's refresh tests
// (internal/serve) both run against it, so the two suites cannot drift apart
// on what a release looks like on the wire. It is exported for the same reason
// downstream: audiosilo-server's mirror mode downloads through pkg/release, and
// its tests publish their releases here rather than in a fake of their own.
//
// It deliberately does NOT import pkg/release: that package's internal tests
// import this one, and a cycle would follow. The asset names are therefore
// spelled as literals here, and pkg/release's TestReleasetestNamesTheContract
// pins them to its constants.
package releasetest

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// Repo is the repository the fake serves ("owner/name"): a client is pointed at
// it with this repo and the fake's URL as its API base.
const Repo = "owner/name"

// Digest is data's sha256 as lowercase hex.
func Digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// SumFile builds a `sha256sum`-format checksum file over data for name.
func SumFile(name string, data []byte) []byte {
	return []byte(Digest(data) + "  " + name + "\n")
}

// Gzip gzips b.
func Gzip(tb testing.TB, b []byte) []byte {
	tb.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(b); err != nil {
		tb.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		tb.Fatal(err)
	}
	return buf.Bytes()
}

// DataAssets is a data release's three contract assets over raw (the
// decompressed artifact bytes): meta.sqlite.gz, its checksum, and the checksum
// of raw itself.
func DataAssets(tb testing.TB, raw []byte) map[string][]byte {
	tb.Helper()
	gz := Gzip(tb, raw)
	return map[string][]byte{
		"meta.sqlite.gz":        gz,
		"meta.sqlite.gz.sha256": SumFile("meta.sqlite.gz", gz),
		"meta.sqlite.sha256":    SumFile("meta.sqlite", raw),
	}
}

// DirEntries lists dir's entry names, sorted (os.ReadDir's order).
func DirEntries(tb testing.TB, dir string) []string {
	tb.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		tb.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// apiAsset and apiRelease are the GitHub releases API's JSON, as much of it as
// the fake publishes (pkg/release decodes the same fields).
type apiAsset struct {
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	DownloadURL string `json:"browser_download_url"`
}

type apiRelease struct {
	TagName     string     `json:"tag_name"`
	Draft       bool       `json:"draft"`
	Prerelease  bool       `json:"prerelease"`
	PublishedAt time.Time  `json:"published_at"`
	Assets      []apiAsset `json:"assets"`
}

// Rel is one release as the fake publishes it: a tag, its named assets, the
// draft/prerelease flags selection must respect, and a published time (zero
// unless a test pins it - selection is by max published_at).
type Rel struct {
	Tag        string
	Draft      bool
	Prerelease bool
	Published  time.Time
	Assets     map[string][]byte
}

// GitHub is the fake. It serves the releases LIST endpoint (in the order given,
// like GitHub's non-chronological list) with ETag/304 support, each release's
// assets under /dl/<tag>/<name> with their sizes declared, and a /redirect route
// a test aims at a hop target. It counts full (200) list responses, 304s and
// per-asset downloads, and has the knobs the failure tests need: the list can go
// down, an asset can 500, deliver in timed chunks (slow but healthy) or deliver
// half and go quiet (a stalled transfer).
type GitHub struct {
	// URL is the fake's base URL - the API base a client is pointed at.
	URL string

	mu         sync.Mutex
	rels       []Rel
	etag       string
	hits       map[string]int
	failing    map[string]bool
	lists      int
	notMod     int
	down       bool
	chunk      int           // >0: deliver assets in chunks of this many bytes
	pace       time.Duration // delay between chunks
	hang       string        // asset that delivers half and then goes quiet
	redirectTo string
	agents     []string      // every list and asset request's User-Agent, in order
	unhang     chan struct{} // closed at cleanup so a hung handler can return
}

// NewGitHub starts a fake publishing rels, stopped at the test's cleanup.
func NewGitHub(tb testing.TB, rels ...Rel) *GitHub {
	tb.Helper()
	f := &GitHub{unhang: make(chan struct{})}
	f.SetReleases(rels...)
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/"+Repo+"/releases", f.serveList)
	mux.HandleFunc("/dl/", f.serveAsset)
	// A real browser_download_url always 302s to a CDN host, so the hop policy
	// is not an edge case - this route is how a test aims one.
	mux.HandleFunc("/redirect", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		target := f.redirectTo
		f.mu.Unlock()
		http.Redirect(w, r, target, http.StatusFound)
	})
	srv := httptest.NewServer(mux)
	f.URL = srv.URL
	tb.Cleanup(func() { close(f.unhang); srv.Close() })
	return f
}

func (f *GitHub) serveList(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.agents = append(f.agents, r.Header.Get("User-Agent"))
	if f.down {
		f.mu.Unlock()
		http.Error(w, "github is down", http.StatusInternalServerError)
		return
	}
	if r.Header.Get("If-None-Match") == f.etag {
		f.notMod++
		f.mu.Unlock()
		w.WriteHeader(http.StatusNotModified)
		return
	}
	etag := f.etag
	list := make([]apiRelease, 0, len(f.rels))
	for _, fr := range f.rels {
		rel := apiRelease{TagName: fr.Tag, Draft: fr.Draft, Prerelease: fr.Prerelease, PublishedAt: fr.Published}
		names := make([]string, 0, len(fr.Assets))
		for name := range fr.Assets {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			// Download URLs are namespaced per release (like real GitHub asset
			// URLs), so two releases advertising one asset name never shadow
			// each other.
			rel.Assets = append(rel.Assets, apiAsset{
				Name: name, Size: int64(len(fr.Assets[name])),
				DownloadURL: f.URL + "/dl/" + fr.Tag + "/" + name,
			})
		}
		list = append(list, rel)
	}
	f.lists++
	f.mu.Unlock()
	w.Header().Set("ETag", etag)
	_ = json.NewEncoder(w).Encode(list)
}

func (f *GitHub) serveAsset(w http.ResponseWriter, r *http.Request) {
	tag, name, found := strings.Cut(strings.TrimPrefix(r.URL.Path, "/dl/"), "/")
	if !found {
		http.NotFound(w, r)
		return
	}
	f.mu.Lock()
	f.agents = append(f.agents, r.Header.Get("User-Agent"))
	if f.failing[name] {
		f.mu.Unlock()
		http.Error(w, "boom", http.StatusInternalServerError)
		return
	}
	var data []byte
	ok := false
	for _, fr := range f.rels {
		if fr.Tag == tag {
			data, ok = fr.Assets[name]
			break
		}
	}
	if ok {
		f.hits[name]++
	}
	chunk, pace, hang := f.chunk, f.pace, f.hang == name
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
	switch {
	case hang:
		_, _ = w.Write(data[:len(data)/2])
		flush()
		select { // a connection that is up and simply never delivers again
		case <-r.Context().Done():
		case <-f.unhang:
		}
	case chunk > 0:
		for off := 0; off < len(data); off += chunk {
			if _, err := w.Write(data[off:min(off+chunk, len(data))]); err != nil {
				return
			}
			flush()
			time.Sleep(pace)
		}
	default:
		_, _ = w.Write(data)
	}
}

// SetReleases replaces the published list, changing the ETag (so the next
// conditional request is a 200, not a 304) and resetting the per-asset hit
// counts and failures, so a test can assert exactly which assets the CURRENT
// refresh fetched.
func (f *GitHub) SetReleases(rels ...Rel) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rels = rels
	tags := make([]string, len(rels))
	for i, r := range rels {
		tags[i] = r.Tag
	}
	f.etag = `"` + strings.Join(tags, "+") + `"`
	f.hits = map[string]int{}
	f.failing = map[string]bool{}
}

// Publish publishes a single release - the common case.
func (f *GitHub) Publish(tag string, assets map[string][]byte) {
	f.SetReleases(Rel{Tag: tag, Assets: assets})
}

// SetAssetFailure makes downloads of the named asset answer 500 (or heals it).
// The release list, and its ETag, are unchanged.
func (f *GitHub) SetAssetFailure(name string, fail bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failing[name] = fail
}

// SetDown makes the release list answer 500 (or brings it back).
func (f *GitHub) SetDown(down bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.down = down
}

// HangOn makes the named asset deliver half its bytes and then go quiet.
func (f *GitHub) HangOn(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hang = name
}

// Throttle delivers every asset in chunks of chunk bytes, pace apart.
func (f *GitHub) Throttle(chunk int, pace time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.chunk, f.pace = chunk, pace
}

// SetRedirect points the /redirect route at target.
func (f *GitHub) SetRedirect(target string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.redirectTo = target
}

// Hits is how many times the named asset was downloaded since the list last
// changed.
func (f *GitHub) Hits(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits[name]
}

// Lists is how many full (200) list responses the fake has served.
func (f *GitHub) Lists() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lists
}

// NotModified is how many 304s the list endpoint has answered.
func (f *GitHub) NotModified() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.notMod
}

// UserAgents lists the User-Agent of every list and asset request, in order.
func (f *GitHub) UserAgents() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.agents...)
}
