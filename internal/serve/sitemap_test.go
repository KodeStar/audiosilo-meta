package serve

import (
	"compress/gzip"
	"encoding/xml"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// sitemapSite is the page-server config plus the Astro build's own static
// sitemap, which is what the index's first entry is conditioned on.
func sitemapSite(t *testing.T, cat *model.Catalog) *httptest.Server {
	t.Helper()
	cfg := quietConfig(t, cat, markedShells)
	writeSiteFile(t, cfg.Site, staticSitemapFile, `<urlset></urlset>`)
	_, ts := newPageServerFrom(t, cfg)
	return ts
}

// sitemapServerWithoutStatic is a deployment whose dist carries no static
// sitemap - and, in the second case, no dist at all.
func sitemapServerWithoutStatic(t *testing.T, cat *model.Catalog) *httptest.Server {
	t.Helper()
	_, ts := newPageServerFrom(t, quietConfig(t, cat, markedShells))
	return ts
}

// getSitemap fetches an XML document and returns its status, body and headers.
func getSitemap(t *testing.T, base, path string) (int, string, http.Header) {
	t.Helper()
	resp, err := http.Get(base + path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(body), resp.Header
}

// ---- route tables -----------------------------------------------------------

// TestSitemapRoutesAreDisjointFromEverythingElse extends the Phase A separation
// to the third table: the sitemaps are neither API (no openapi.json entry) nor
// pages (no shell), so no pattern may appear under /api/ or in either of the
// other two tables.
func TestSitemapRoutesAreDisjointFromEverythingElse(t *testing.T) {
	srv := &Server{cfg: Config{WebhookSecret: strings.Repeat("s", minWebhookSecretBytes)}}

	taken := map[string]string{}
	for _, r := range srv.routes() {
		taken[r.pattern] = "routes()"
	}
	for _, r := range srv.htmlRoutes() {
		taken[r.pattern] = "htmlRoutes()"
	}
	var got []string
	for _, r := range srv.sitemapRoutes() {
		got = append(got, r.pattern)
		if strings.Contains(r.specPath(), "/api/") {
			t.Errorf("sitemap route %s sits under /api/: the surfaces must stay disjoint", r.pattern)
		}
		if where, dup := taken[r.pattern]; dup {
			t.Errorf("pattern %s is registered by BOTH sitemapRoutes() and %s", r.pattern, where)
		}
	}
	want := []string{"GET /sitemap-index.xml", "GET /sitemaps/{file}"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("sitemapRoutes = %v, want %v", got, want)
	}
}

// TestSitemapsNeedNoSiteDirectory is the registration contract that differs from
// the entity pages': a sitemap is rendered from the artifact alone, so an
// API-only deployment serves it (with no static entry to list).
func TestSitemapsNeedNoSiteDirectory(t *testing.T) {
	_, ts := newTestServer(t) // no Site
	code, body, _ := getSitemap(t, ts.URL, sitemapIndexPath)
	if code != http.StatusOK {
		t.Fatalf("index without a site dir = %d, want 200\n%s", code, body)
	}
	if strings.Contains(body, staticSitemapFile) {
		t.Error("index lists a static sitemap that this deployment does not serve")
	}
	if code, _, _ := getSitemap(t, ts.URL, "/sitemaps/works-0.xml"); code != http.StatusOK {
		t.Errorf("shard without a site dir = %d, want 200", code)
	}
}

// ---- the index --------------------------------------------------------------

// TestSitemapIndexOrderAndContents pins the crawl-budget ordering the campaign
// rests on - the static pages first, then the two guide families, then series,
// people and works - along with the absolute locs and the built_at lastmod.
func TestSitemapIndexOrderAndContents(t *testing.T) {
	ts := sitemapSite(t, fixtureCatalog())
	code, body, hdr := getSitemap(t, ts.URL, sitemapIndexPath)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200\n%s", code, body)
	}
	if ct := hdr.Get("Content-Type"); ct != "application/xml; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
	if cc := hdr.Get("Cache-Control"); cc != sitemapMaxAge {
		t.Errorf("Cache-Control = %q, want %q", cc, sitemapMaxAge)
	}
	if hdr.Get("ETag") == "" {
		t.Error("no ETag on a sitemap index")
	}
	if hdr.Get("Access-Control-Allow-Origin") != "" {
		t.Error("a sitemap carries CORS headers: these are crawler documents, not API")
	}

	var doc struct {
		XMLName  xml.Name `xml:"sitemapindex"`
		NS       string   `xml:"xmlns,attr"`
		Sitemaps []struct {
			Loc     string `xml:"loc"`
			LastMod string `xml:"lastmod"`
		} `xml:"sitemap"`
	}
	if err := xml.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("index does not parse: %v\n%s", err, body)
	}
	if doc.NS != sitemapNS {
		t.Errorf("xmlns = %q, want %q", doc.NS, sitemapNS)
	}
	var locs []string
	for _, sm := range doc.Sitemaps {
		locs = append(locs, sm.Loc)
	}
	want := []string{
		testSiteURL + "/sitemap-0.xml",
		testSiteURL + "/sitemaps/recaps-0.xml",
		testSiteURL + "/sitemaps/characters-0.xml",
		testSiteURL + "/sitemaps/series-0.xml",
		testSiteURL + "/sitemaps/people-0.xml",
		testSiteURL + "/sitemaps/works-0.xml",
	}
	if strings.Join(locs, "\n") != strings.Join(want, "\n") {
		t.Errorf("index locs =\n%s\nwant\n%s", strings.Join(locs, "\n"), strings.Join(want, "\n"))
	}
	// The static entry states no date of its own (it belongs to the dist); every
	// shard states the artifact's build time, which IS what it was rendered from.
	if doc.Sitemaps[0].LastMod != "" {
		t.Errorf("static entry lastmod = %q, want none", doc.Sitemaps[0].LastMod)
	}
	for _, sm := range doc.Sitemaps[1:] {
		if sm.LastMod != "2026-07-11T00:00:00Z" {
			t.Errorf("%s lastmod = %q, want the artifact's built_at", sm.Loc, sm.LastMod)
		}
	}
}

// TestSitemapIndexOmitsAnAbsentStaticSitemap is the other half of that
// condition: nothing is listed that the deployment cannot serve.
func TestSitemapIndexOmitsAnAbsentStaticSitemap(t *testing.T) {
	ts := sitemapServerWithoutStatic(t, fixtureCatalog())
	_, body, _ := getSitemap(t, ts.URL, sitemapIndexPath)
	if strings.Contains(body, staticSitemapFile) {
		t.Errorf("index lists %s, which the dist does not carry:\n%s", staticSitemapFile, body)
	}
	if !strings.Contains(body, "/sitemaps/works-0.xml") {
		t.Errorf("index lost its entity shards:\n%s", body)
	}
}

// TestSitemapIndexSkipsAnEmptyFamily: a family with no records has no shard, so
// it contributes no entry (and its shard 0 is a 404 - see the 404 table).
func TestSitemapIndexSkipsAnEmptyFamily(t *testing.T) {
	ts := sitemapServerWithoutStatic(t, seriesLessCatalog())
	_, body, _ := getSitemap(t, ts.URL, sitemapIndexPath)
	if strings.Contains(body, "/sitemaps/series-") {
		t.Errorf("index lists a shard for an empty family:\n%s", body)
	}
	for _, want := range []string{"/sitemaps/people-0.xml", "/sitemaps/works-0.xml"} {
		if !strings.Contains(body, want) {
			t.Errorf("index is missing %s:\n%s", want, body)
		}
	}
}

// seriesLessCatalog is the minimum catalogue with an EMPTY family.
func seriesLessCatalog() *model.Catalog {
	return &model.Catalog{
		Works: []*model.Work{{
			ID: "lone-work", Title: "Lone Work", Language: "en",
			Authors: []string{"lone-author"}, License: "CC0-1.0",
		}},
		People: []*model.Person{{ID: "lone-author", Name: "Lone Author", License: "CC0-1.0"}},
	}
}

// ---- the shards -------------------------------------------------------------

// urlSet is a parsed shard.
type urlSet struct {
	XMLName xml.Name `xml:"urlset"`
	NS      string   `xml:"xmlns,attr"`
	URLs    []struct {
		Loc     string `xml:"loc"`
		LastMod string `xml:"lastmod"`
	} `xml:"url"`
}

func parseURLSet(t *testing.T, body string) urlSet {
	t.Helper()
	var doc urlSet
	if err := xml.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("shard does not parse: %v\n%s", err, body)
	}
	return doc
}

// TestSitemapShardsCarryEveryEntityURL walks all three families: the locs are the
// Phase A page URLs in id order, and a lastmod appears exactly where the record
// states an added_at.
func TestSitemapShardsCarryEveryEntityURL(t *testing.T) {
	ts := sitemapSite(t, fixtureCatalog())

	code, body, hdr := getSitemap(t, ts.URL, "/sitemaps/works-0.xml")
	if code != http.StatusOK {
		t.Fatalf("works shard = %d, want 200\n%s", code, body)
	}
	if ct := hdr.Get("Content-Type"); ct != "application/xml; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
	works := parseURLSet(t, body)
	if works.NS != sitemapNS {
		t.Errorf("xmlns = %q", works.NS)
	}
	var locs []string
	for _, u := range works.URLs {
		locs = append(locs, u.Loc)
	}
	want := []string{
		testSiteURL + "/works/edgedancer",
		testSiteURL + "/works/project-hail-mary",
		testSiteURL + "/works/the-way-of-kings",
		testSiteURL + "/works/words-of-radiance",
	}
	if strings.Join(locs, "\n") != strings.Join(want, "\n") {
		t.Errorf("works locs =\n%s\nwant\n%s", strings.Join(locs, "\n"), strings.Join(want, "\n"))
	}
	// project-hail-mary is the one fixture work with an added_at; the other three
	// carry none and must state none rather than a fabricated date.
	for _, u := range works.URLs {
		switch {
		case strings.HasSuffix(u.Loc, "/project-hail-mary"):
			if u.LastMod != "2026-07-10T00:00:00Z" {
				t.Errorf("phm lastmod = %q, want its added_at", u.LastMod)
			}
		case u.LastMod != "":
			t.Errorf("%s states lastmod %q for a record with no added_at", u.Loc, u.LastMod)
		}
	}

	// people and series have no added_at column in the artifact at all, so every
	// URL there omits lastmod - and nothing about the artifact changed to say so.
	for _, tc := range []struct {
		path, prefix string
		want         int
	}{
		{"/sitemaps/people-0.xml", testSiteURL + "/people/", 5},
		{"/sitemaps/series-0.xml", testSiteURL + "/series/", 1},
	} {
		_, body, _ := getSitemap(t, ts.URL, tc.path)
		doc := parseURLSet(t, body)
		if len(doc.URLs) != tc.want {
			t.Errorf("%s holds %d URLs, want %d", tc.path, len(doc.URLs), tc.want)
		}
		for _, u := range doc.URLs {
			if !strings.HasPrefix(u.Loc, tc.prefix) {
				t.Errorf("%s: loc %q does not address the entity page", tc.path, u.Loc)
			}
			if u.LastMod != "" {
				t.Errorf("%s: loc %q states a lastmod the artifact does not carry", tc.path, u.Loc)
			}
		}
	}
}

// TestShardCountPagination is the protocol's cap arithmetic at the boundary: a
// family of exactly 50,000 is one file, 50,001 is two, and an empty family has
// none at all.
func TestShardCountPagination(t *testing.T) {
	cases := []struct{ n, want int }{
		{0, 0}, {-1, 0}, {1, 1},
		{sitemapShardURLs - 1, 1}, {sitemapShardURLs, 1}, {sitemapShardURLs + 1, 2},
		{2 * sitemapShardURLs, 2}, {2*sitemapShardURLs + 1, 3},
		{279_000, 6},
	}
	for _, tc := range cases {
		if got := shardCount(tc.n); got != tc.want {
			t.Errorf("shardCount(%d) = %d, want %d", tc.n, got, tc.want)
		}
	}
}

// TestShardWindowsThePageItPromises checks the OFFSET arithmetic against the
// database rather than only the count: with the cap forced down to 2, shard 0
// and shard 1 partition the works family in id order with no gap and no repeat.
func TestShardWindowsThePageItPromises(t *testing.T) {
	snap := snapshotFor(t, fixtureCatalog())
	var got []string
	for shard := range 2 {
		rows, err := snap.db.Query(worksSitemapSQL, 2, shard*2)
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for rows.Next() {
			var id string
			var added *string
			if err := rows.Scan(&id, &added); err != nil {
				t.Fatal(err)
			}
			ids = append(ids, id)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		if len(ids) != 2 {
			t.Fatalf("shard %d returned %d ids, want 2", shard, len(ids))
		}
		got = append(got, ids...)
	}
	want := "edgedancer,project-hail-mary,the-way-of-kings,words-of-radiance"
	if strings.Join(got, ",") != want {
		t.Errorf("windowed ids = %v, want %s", got, want)
	}
}

// withShardCap lowers the protocol cap for one test. The package's tests do not
// run in parallel, and the original is restored on cleanup.
func withShardCap(t *testing.T, n int) {
	t.Helper()
	old := sitemapShardURLs
	sitemapShardURLs = n
	t.Cleanup(func() { sitemapShardURLs = old })
}

// TestShardRoutePartitionsThroughTheMux drives shard > 0 over HTTP, which 50,000
// fixture records cannot: with the cap at 2, the two works shards must partition
// the family exactly as the index promises - the boundary ids in the right files,
// no id in both, and the shard past the end a 404.
func TestShardRoutePartitionsThroughTheMux(t *testing.T) {
	withShardCap(t, 2)
	ts := sitemapSite(t, fixtureCatalog())

	_, index, _ := getSitemap(t, ts.URL, sitemapIndexPath)
	for _, want := range []string{"/sitemaps/works-0.xml", "/sitemaps/works-1.xml"} {
		if !strings.Contains(index, want) {
			t.Errorf("index does not promise %s:\n%s", want, index)
		}
	}
	if strings.Contains(index, "/sitemaps/works-2.xml") {
		t.Errorf("index promises a third shard for 4 works:\n%s", index)
	}

	// The promise is kept: shard 0 is the first two ids in id order, shard 1 the
	// rest, and nothing appears twice.
	want := [][]string{
		{testSiteURL + "/works/edgedancer", testSiteURL + "/works/project-hail-mary"},
		{testSiteURL + "/works/the-way-of-kings", testSiteURL + "/works/words-of-radiance"},
	}
	seen := map[string]int{}
	for shard, wantLocs := range want {
		path := "/sitemaps/works-" + strconv.Itoa(shard) + ".xml"
		code, body, _ := getSitemap(t, ts.URL, path)
		if code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200\n%s", path, code, body)
		}
		var locs []string
		for _, u := range parseURLSet(t, body).URLs {
			locs = append(locs, u.Loc)
			seen[u.Loc]++
		}
		if strings.Join(locs, "\n") != strings.Join(wantLocs, "\n") {
			t.Errorf("%s holds\n%s\nwant\n%s", path, strings.Join(locs, "\n"), strings.Join(wantLocs, "\n"))
		}
	}
	for loc, n := range seen {
		if n != 1 {
			t.Errorf("%s appears in %d shards, want exactly 1", loc, n)
		}
	}
	if code, _, _ := getSitemap(t, ts.URL, "/sitemaps/works-2.xml"); code != http.StatusNotFound {
		t.Errorf("shard past the end = %d, want 404", code)
	}
}

// TestSitemapShard404s is the whole refusal surface of the shard route: an
// unknown family, a non-canonical number, junk, a traversal attempt, a shard past
// the family's end and every shard of an empty family.
func TestSitemapShard404s(t *testing.T) {
	ts := sitemapSite(t, fixtureCatalog())
	cases := []string{
		"/sitemaps/works.xml",          // no shard number
		"/sitemaps/works-.xml",         // empty number
		"/sitemaps/works-00.xml",       // a second spelling of shard 0
		"/sitemaps/works-01.xml",       // ... and of shard 1
		"/sitemaps/works-0.XML",        // wrong case
		"/sitemaps/works-0.xml.gz",     // wrong extension
		"/sitemaps/recordings-0.xml",   // not a family
		"/sitemaps/Works-0.xml",        // not the family's spelling
		"/sitemaps/works-0.xml/",       // trailing slash
		"/sitemaps/works-1.xml",        // past the end (4 works = 1 shard)
		"/sitemaps/works-99.xml",       // far past the end
		"/sitemaps/..%2Fsitemap-0.xml", // traversal, escaped
		"/sitemaps/..%2F..%2Fetc%2Fpasswd",
		"/sitemaps/", // the directory itself
	}
	for _, path := range cases {
		t.Run(path, func(t *testing.T) {
			code, body, _ := getSitemap(t, ts.URL, path)
			if code != http.StatusNotFound {
				t.Errorf("GET %s = %d, want 404\n%s", path, code, body)
			}
			if strings.Contains(body, "<urlset") {
				t.Errorf("GET %s served a sitemap", path)
			}
		})
	}

	// An empty family 404s at its own shard 0 too.
	empty := sitemapServerWithoutStatic(t, seriesLessCatalog())
	if code, _, _ := getSitemap(t, empty.URL, "/sitemaps/series-0.xml"); code != http.StatusNotFound {
		t.Errorf("shard 0 of an empty family = %d, want 404", code)
	}
}

// ---- determinism, caching, transport ----------------------------------------

// TestSitemapsAreDeterministic: one snapshot renders one document, byte for
// byte. Nothing here reads a clock, and every list is ordered by id.
func TestSitemapsAreDeterministic(t *testing.T) {
	ts := sitemapSite(t, fixtureCatalog())
	for _, path := range []string{sitemapIndexPath, "/sitemaps/works-0.xml", "/sitemaps/people-0.xml"} {
		_, first, _ := getSitemap(t, ts.URL, path)
		_, second, _ := getSitemap(t, ts.URL, path)
		if first != second {
			t.Errorf("%s differs between renders:\n%s\n---\n%s", path, first, second)
		}
		if !strings.HasPrefix(first, xml.Header) {
			t.Errorf("%s carries no XML declaration:\n%s", path, first)
		}
	}
}

// TestSitemapEscapesTheOrigin: a slug cannot need escaping, but the configured
// origin is not a slug - and a raw "&" would make the document unparseable. The
// escaping is encoding/xml's, so this pins that nothing bypasses it.
func TestSitemapEscapesTheOrigin(t *testing.T) {
	cfg := quietConfig(t, fixtureCatalog(), markedShells)
	cfg.SiteURL = "https://meta.test/?a=1&b=2"
	_, ts := newPageServerFrom(t, cfg)

	_, body, _ := getSitemap(t, ts.URL, "/sitemaps/works-0.xml")
	if strings.Contains(body, "&b=2") {
		t.Errorf("an unescaped & reached the document:\n%s", body)
	}
	doc := parseURLSet(t, body)
	if len(doc.URLs) == 0 || !strings.Contains(doc.URLs[0].Loc, "&b=2") {
		t.Errorf("the origin did not survive a round trip: %+v", doc.URLs)
	}
}

// TestSitemapConditionalRequests covers the validator on both documents: a
// matching If-None-Match and the "*" wildcard are 304 with no body, and a
// validator for another document is not.
func TestSitemapConditionalRequests(t *testing.T) {
	ts := sitemapSite(t, fixtureCatalog())
	for _, path := range []string{sitemapIndexPath, "/sitemaps/works-0.xml"} {
		_, _, hdr := getSitemap(t, ts.URL, path)
		etag := hdr.Get("ETag")
		if etag == "" {
			t.Fatalf("%s issued no ETag", path)
		}
		for _, inm := range []string{etag, "*", `"other", ` + etag} {
			resp := conditionalGet(t, ts.URL+path, inm)
			if resp.StatusCode != http.StatusNotModified {
				t.Errorf("GET %s with If-None-Match %q = %d, want 304", path, inm, resp.StatusCode)
				continue
			}
			wantBodyless(t, resp)
		}
		if resp := conditionalGet(t, ts.URL+path, `W/"some-other-release/x"`); resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s with a stale validator = %d, want 200", path, resp.StatusCode)
		}
	}

	// The two documents are different representations, so one's validator must
	// not satisfy the other.
	_, _, idx := getSitemap(t, ts.URL, sitemapIndexPath)
	if resp := conditionalGet(t, ts.URL+"/sitemaps/works-0.xml", idx.Get("ETag")); resp.StatusCode != http.StatusOK {
		t.Errorf("the index's validator satisfied a shard = %d, want 200", resp.StatusCode)
	}
}

// TestSitemapErrorCarriesNoCacheHeaders is the ordering rule: the validator and
// the hour of public caching belong to a document that was actually composed. A
// 500 - here a hot swap closing the db out from under the request - must carry
// neither, or a shared cache would store the error and then 304-renew it under
// the document's own ETag for the life of the release.
func TestSitemapErrorCarriesNoCacheHeaders(t *testing.T) {
	srv, ts := newPageServerFrom(t, quietConfig(t, fixtureCatalog(), markedShells))
	// The stats are already loaded, so the shard still EXISTS - the failure lands
	// where the document is rendered, which is the path under test.
	srv.current().close()

	code, body, hdr := getSitemap(t, ts.URL, "/sitemaps/works-0.xml")
	if code != http.StatusInternalServerError {
		t.Fatalf("shard over a closed db = %d, want 500\n%s", code, body)
	}
	if etag := hdr.Get("ETag"); etag != "" {
		t.Errorf("the 500 carries validator %q: a cache could revalidate the error into a 304", etag)
	}
	if cc := hdr.Get("Cache-Control"); cc != "" {
		t.Errorf("the 500 carries Cache-Control %q, want none", cc)
	}
}

// TestSitemapIndexETagTracksTheStaticEntry: the index lists the dist's own
// sitemap when the dist carries one, so that bit is part of the document and
// must be part of its validator - otherwise a UI-only redeploy under an
// unchanged data release 304-renews the wrong index forever. The SHARDS have no
// such dependency and must be unmoved by it.
func TestSitemapIndexETagTracksTheStaticEntry(t *testing.T) {
	// One artifact, two dists: same snapshot identity, same origin.
	bare := quietConfig(t, fixtureCatalog(), markedShells)
	withStatic := bare
	withStatic.Site = entitySite(t, markedShells)
	writeSiteFile(t, withStatic.Site, staticSitemapFile, `<urlset></urlset>`)

	_, without := newPageServerFrom(t, bare)
	_, with := newPageServerFrom(t, withStatic)

	_, body, hdrWithout := getSitemap(t, without.URL, sitemapIndexPath)
	if strings.Contains(body, staticSitemapFile) {
		t.Fatalf("the bare dist's index lists a static sitemap:\n%s", body)
	}
	_, body, hdrWith := getSitemap(t, with.URL, sitemapIndexPath)
	if !strings.Contains(body, staticSitemapFile) {
		t.Fatalf("the static sitemap did not reach the index:\n%s", body)
	}
	if hdrWithout.Get("ETag") == hdrWith.Get("ETag") {
		t.Errorf("both indexes validate as %q, so the static entry can change unseen", hdrWith.Get("ETag"))
	}

	_, _, shardWithout := getSitemap(t, without.URL, "/sitemaps/works-0.xml")
	_, _, shardWith := getSitemap(t, with.URL, "/sitemaps/works-0.xml")
	if shardWithout.Get("ETag") != shardWith.Get("ETag") {
		t.Errorf("shard validators differ across dists (%q vs %q): no byte of the dist reaches a shard",
			shardWithout.Get("ETag"), shardWith.Get("ETag"))
	}
}

// TestSitemapGzip: the documents wear the shared gzip middleware, and a 304
// still announces no encoding (a client applies a 304's content headers to its
// CACHED copy - see gzipResponseWriter.ensureHeader).
func TestSitemapGzip(t *testing.T) {
	ts := sitemapSite(t, fixtureCatalog())
	req, err := http.NewRequest(http.MethodGet, ts.URL+"/sitemaps/works-0.xml", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept-Encoding", "gzip")
	resp, err := http.DefaultTransport.RoundTrip(req) // no transparent decompression
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if enc := resp.Header.Get("Content-Encoding"); enc != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", enc)
	}
	zr, err := gzip.NewReader(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "/works/project-hail-mary") {
		t.Errorf("gzipped shard did not decompress to the document:\n%s", body)
	}

	// A 304 must announce no encoding: the client applies its content headers to
	// the copy it already holds, which is not gzipped.
	cond := conditionalGet(t, ts.URL+"/sitemaps/works-0.xml", resp.Header.Get("ETag"))
	if cond.StatusCode != http.StatusNotModified {
		t.Errorf("conditional gzip request = %d, want 304", cond.StatusCode)
	}
	if enc := cond.Header.Get("Content-Encoding"); enc != "" {
		t.Errorf("304 announced Content-Encoding %q", enc)
	}
}

// ---- degradation ------------------------------------------------------------

// TestSitemapsWithoutAnArtifact is the cold-boot posture: the index degrades to
// the dist's own static sitemap-index.xml (the file this route shadows), and the
// shards - which have no static equivalent - answer the API's 503 with the poll
// loop's Retry-After.
func TestSitemapsWithoutAnArtifact(t *testing.T) {
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "GitHub is having a day", http.StatusInternalServerError)
	}))
	t.Cleanup(down.Close)

	dir := writeSiteFixture(t)
	writeSiteFile(t, dir, "sitemap-index.xml", `<sitemapindex>STATIC</sitemapindex>`)
	srv, err := New(Config{
		Poll: true, Repo: "owner/name", CacheDir: t.TempDir(), Site: dir,
		Logger:  log.New(io.Discard, "", 0),
		apiBase: down.URL, swapGrace: time.Minute, bootRetry: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	if srv.current() != nil {
		t.Fatal("expected no snapshot after a failed boot fetch")
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	code, body, degraded := getSitemap(t, ts.URL, sitemapIndexPath)
	if code != http.StatusOK || !strings.Contains(body, "STATIC") {
		t.Errorf("index while degraded = %d %q, want the dist's static file", code, body)
	}
	// The stand-in describes the boot window, not the site. Without this a
	// crawler that arrived during the window would keep the twelve-URL static
	// index for a tenth of the file's age - hours after the real one came live.
	if cc := degraded.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("degraded index Cache-Control = %q, want no-store", cc)
	}
	code, _, hdr := getSitemap(t, ts.URL, "/sitemaps/works-0.xml")
	if code != http.StatusServiceUnavailable {
		t.Errorf("shard while degraded = %d, want 503", code)
	}
	if hdr.Get("Retry-After") == "" {
		t.Error("503 carries no Retry-After")
	}
}

// TestSitemapIndexWithoutAnArtifactOrADist is the last resort: nothing to render
// and nothing to fall back to is the API's 503, not a 200 with an empty index (a
// crawler would take that as "this site has no pages").
func TestSitemapIndexWithoutAnArtifactOrADist(t *testing.T) {
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "GitHub is having a day", http.StatusInternalServerError)
	}))
	t.Cleanup(down.Close)

	srv, err := New(Config{
		Poll: true, Repo: "owner/name", CacheDir: t.TempDir(),
		Logger:  log.New(io.Discard, "", 0),
		apiBase: down.URL, swapGrace: time.Minute, bootRetry: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	code, _, hdr := getSitemap(t, ts.URL, sitemapIndexPath)
	if code != http.StatusServiceUnavailable {
		t.Errorf("index with neither data nor dist = %d, want 503", code)
	}
	if hdr.Get("Retry-After") == "" {
		t.Error("503 carries no Retry-After")
	}
}

// ---- lastmod ----------------------------------------------------------------

// TestW3CDate covers the two spellings the artifact carries and the refusal for
// anything else: a lastmod a crawler cannot parse is worse than none, and a date
// nobody stated is never invented.
func TestW3CDate(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"2026-07-10", "2026-07-10"},
		{" 2026-07-10 ", "2026-07-10"},
		{"2026-07-10T00:00:00Z", "2026-07-10T00:00:00Z"},
		{"2026-07-10T12:30:00+02:00", "2026-07-10T10:30:00Z"}, // normalized to UTC
		{"2026-07", ""},
		{"10/07/2026", ""},
		{"not a date", ""},
	}
	for _, tc := range cases {
		if got := w3cDate(tc.in); got != tc.want {
			t.Errorf("w3cDate(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestSitemapNormalizesADateOnlyAddedAt: an added_at stamped by the importer is
// a bare date, and it travels into the document as one.
func TestSitemapNormalizesADateOnlyAddedAt(t *testing.T) {
	cat := seriesLessCatalog()
	cat.Works[0].AddedAt = "2026-08-01"
	ts := sitemapServerWithoutStatic(t, cat)
	_, body, _ := getSitemap(t, ts.URL, "/sitemaps/works-0.xml")
	doc := parseURLSet(t, body)
	if len(doc.URLs) != 1 || doc.URLs[0].LastMod != "2026-08-01" {
		t.Errorf("date-only added_at rendered as %+v", doc.URLs)
	}
}
