package release

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeRel is one release as the fake publishes it: a tag, its named assets, the
// draft/prerelease flags selection must respect, and a published time (zero
// unless a test pins it - selection is by max published_at).
type fakeRel struct {
	tag        string
	draft      bool
	prerelease bool
	published  time.Time
	assets     map[string][]byte
}

// fakeGitHub serves the releases LIST endpoint (in the order given, like GitHub's
// non-chronological list) with ETag/304 support, per-release named assets under
// /dl/<tag>/<name>, and a /redirect route a test aims at a hop target. It counts
// the full (200) list responses and per-asset downloads.
type fakeGitHub struct {
	srv *httptest.Server

	mu         sync.Mutex
	rels       []fakeRel
	etag       string
	hits       map[string]int
	lists      int
	redirectTo string
}

func newFakeGitHub(t *testing.T, rels ...fakeRel) *fakeGitHub {
	t.Helper()
	f := &fakeGitHub{}
	f.setReleases(rels...)
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/owner/name/releases", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Header.Get("If-None-Match") == f.etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		list := make([]apiRelease, 0, len(f.rels))
		for _, fr := range f.rels {
			rel := apiRelease{TagName: fr.tag, Draft: fr.draft, Prerelease: fr.prerelease, PublishedAt: fr.published}
			names := make([]string, 0, len(fr.assets))
			for name := range fr.assets {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				rel.Assets = append(rel.Assets, apiAsset{
					Name: name, Size: int64(len(fr.assets[name])),
					DownloadURL: f.srv.URL + "/dl/" + fr.tag + "/" + name,
				})
			}
			list = append(list, rel)
		}
		f.lists++
		w.Header().Set("ETag", f.etag)
		_ = json.NewEncoder(w).Encode(list)
	})
	mux.HandleFunc("/dl/", func(w http.ResponseWriter, r *http.Request) {
		tag, name, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/dl/"), "/")
		f.mu.Lock()
		var data []byte
		ok := false
		for _, fr := range f.rels {
			if fr.tag == tag {
				data, ok = fr.assets[name]
				break
			}
		}
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
	// A real browser_download_url always 302s to a CDN host, so the hop policy
	// is not an edge case here - this route is how a test aims one.
	mux.HandleFunc("/redirect", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		target := f.redirectTo
		f.mu.Unlock()
		http.Redirect(w, r, target, http.StatusFound)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// setReleases replaces the published list, changing the ETag (so the next
// conditional request is a 200, not a 304) and resetting the hit counts.
func (f *fakeGitHub) setReleases(rels ...fakeRel) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rels = rels
	tags := make([]string, len(rels))
	for i, r := range rels {
		tags[i] = r.tag
	}
	f.etag = `"` + strings.Join(tags, "+") + `"`
	f.hits = map[string]int{}
}

// setRedirect points the fake's /redirect route at target.
func (f *fakeGitHub) setRedirect(target string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.redirectTo = target
}

func (f *fakeGitHub) hitCount(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits[name]
}

func (f *fakeGitHub) listCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lists
}

// client is a Client pointed at the fake, with its notices discarded.
func (f *fakeGitHub) client(opts ...Option) *Client {
	return New("owner/name", "", append([]Option{WithAPIBase(f.srv.URL), WithLogger(quietLogger())}, opts...)...)
}
