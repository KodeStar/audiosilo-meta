package serve

import (
	"context"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"runtime/debug"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kodestar/audiosilo-meta/internal/artifacttest"
	"github.com/kodestar/audiosilo-meta/internal/revision"
	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/query"
)

// The page revision dates on the three surfaces that state them: a sitemap URL's
// <lastmod>, the page's JSON-LD dateModified (Book, Article, BookSeries - never a
// Person) and its Last-Modified header.

var (
	revT1 = time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	revT2 = time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
)

// stampedArtifact is the fixture dated in two releases: every page at revT1, then
// a narrator rename stamping the pages showing him at revT2 - so the surfaces are
// checked against two different dates, not one constant.
func stampedArtifact(t *testing.T) string {
	t.Helper()
	stampAt := func(cat *model.Catalog, prev string, at time.Time) string {
		out := artifacttest.Build(t, cat)
		if _, err := revision.Stamp(context.Background(), out, revision.Options{Previous: prev, At: at, Workers: 2}); err != nil {
			t.Fatal(err)
		}
		return out
	}
	base := stampAt(artifacttest.Fixture(), artifacttest.Build(t, &model.Catalog{}), revT1)
	next := artifacttest.Fixture()
	for _, p := range next.People {
		if p.ID == "ray-porter" {
			p.Name = "Raymond Porter"
		}
	}
	return stampAt(next, base, revT2)
}

func revisionServer(t *testing.T, dbPath string) string {
	t.Helper()
	cfg := quietConfig(t, artifacttest.Fixture(), markedShells)
	cfg.DBPath = dbPath
	_, ts := newPageServerFrom(t, cfg)
	return ts.URL
}

// getPageWithHeaders fetches a page and returns its status, body and headers.
func getPageWithHeaders(t *testing.T, url string, hdr map[string]string) (int, string, http.Header) {
	t.Helper()
	resp := artifacttest.GetNoFollowWith(t, url, hdr)
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(body), resp.Header
}

// dateModified reads the page's JSON-LD dateModified: on the document itself, or
// on the first @graph node (a work page's Book).
func dateModified(t *testing.T, page string) (string, bool) {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(between(t, page, `<script type="application/ld+json">`, "</script>")), &doc); err != nil {
		t.Fatalf("JSON-LD does not parse: %v", err)
	}
	if graph, ok := doc["@graph"].([]any); ok && len(graph) > 0 {
		doc, _ = graph[0].(map[string]any)
	}
	v, ok := doc["dateModified"]
	if !ok {
		return "", false
	}
	s, _ := v.(string)
	return s, true
}

// TestRevisionDatesAgreeAcrossSurfaces: for every URL of every sitemap family,
// the <lastmod>, the page's Last-Modified and (where the page type carries one)
// its JSON-LD dateModified state the same instant.
func TestRevisionDatesAgreeAcrossSurfaces(t *testing.T) {
	base := revisionServer(t, stampedArtifact(t))
	seen := map[string]int{}
	for _, fam := range sitemapFamilies {
		_, body, _ := getSitemap(t, base, sitemapShardPrefix+shardFile(fam, 0))
		for _, u := range parseURLSet(t, body).URLs {
			if u.LastMod == "" {
				t.Errorf("%s: no <lastmod> on a fully dated artifact", u.Loc)
				continue
			}
			seen[u.LastMod]++
			at, err := time.Parse(time.RFC3339, u.LastMod)
			if err != nil {
				t.Fatalf("%s: <lastmod> %q: %v", u.Loc, u.LastMod, err)
			}
			status, page, hdr := getPageWithHeaders(t, base+strings.TrimPrefix(u.Loc, testSiteURL), nil)
			if status != http.StatusOK {
				t.Fatalf("GET %s = %d", u.Loc, status)
			}
			if got, want := hdr.Get("Last-Modified"), at.UTC().Format(http.TimeFormat); got != want {
				t.Errorf("%s: Last-Modified %q, want %q", u.Loc, got, want)
			}
			got, has := dateModified(t, page)
			if fam.pages == query.PersonPages {
				if has {
					t.Errorf("%s: a Person states dateModified %q", u.Loc, got)
				}
				continue
			}
			if got != u.LastMod {
				t.Errorf("%s: JSON-LD dateModified %q, <lastmod> %q", u.Loc, got, u.LastMod)
			}
		}
	}
	// Both releases' dates reached the surfaces: the rename moved ray-porter and
	// the work he narrates, and nothing else.
	if seen[revT2.Format(time.RFC3339)] != 2 || seen[revT1.Format(time.RFC3339)] == 0 {
		t.Errorf("lastmod dates seen %v, want two pages at %s and the rest at %s", seen, revT2.Format(time.RFC3339), revT1.Format(time.RFC3339))
	}

	// A chapters page carries its work's date.
	_, _, hdr := getPageWithHeaders(t, base+chaptersPath(artifacttest.HailMary, artifacttest.HailMaryRecording), nil)
	if got, want := hdr.Get("Last-Modified"), revT2.Format(http.TimeFormat); got != want {
		t.Errorf("chapters page Last-Modified %q, want the work's %q", got, want)
	}
}

// TestUnknownRevisionsStateNothing: an artifact older than the revision layer,
// and one built without a previous release, state no date anywhere.
func TestUnknownRevisionsStateNothing(t *testing.T) {
	for name, path := range map[string]string{
		"schema 13": artifacttest.Downgraded(t, artifacttest.Fixture(), 13, "page_revisions"),
		"unstamped": artifacttest.Build(t, artifacttest.Fixture()),
	} {
		t.Run(name, func(t *testing.T) {
			base := revisionServer(t, path)
			for _, fam := range sitemapFamilies {
				_, body, _ := getSitemap(t, base, sitemapShardPrefix+shardFile(fam, 0))
				for _, u := range parseURLSet(t, body).URLs {
					if u.LastMod != "" {
						t.Errorf("%s: <lastmod> %q", u.Loc, u.LastMod)
					}
					_, page, hdr := getPageWithHeaders(t, base+strings.TrimPrefix(u.Loc, testSiteURL), nil)
					if lm := hdr.Get("Last-Modified"); lm != "" {
						t.Errorf("%s: Last-Modified %q", u.Loc, lm)
					}
					if got, has := dateModified(t, page); has {
						t.Errorf("%s: dateModified %q", u.Loc, got)
					}
				}
			}
		})
	}
}

// TestIfModifiedSinceNeverRenews: Last-Modified is informational. A date-based
// conditional request is answered with the page, however late the date it
// names, because the date covers the content and not the dist or the renderer
// (writePage) - so an old validator can never renew a changed representation.
func TestIfModifiedSinceNeverRenews(t *testing.T) {
	base := revisionServer(t, stampedArtifact(t))
	future := time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC).Format(http.TimeFormat)
	for _, path := range []string{"/works/" + artifacttest.HailMary, "/people/ray-porter", "/series/" + artifacttest.Stormlight,
		"/works/" + artifacttest.HailMary + recapSuffix} {
		status, body, hdr := getPageWithHeaders(t, base+path, map[string]string{"If-Modified-Since": future})
		if status != http.StatusOK || body == "" {
			t.Errorf("GET %s with If-Modified-Since = %d, want the page", path, status)
		}
		if hdr.Get("Last-Modified") == "" {
			t.Errorf("GET %s: no Last-Modified", path)
		}
	}
}

func TestStampedRevisionWins(t *testing.T) {
	info := &debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "vcs-sha"}}}
	cases := []struct {
		stamped string
		info    *debug.BuildInfo
		want    string
	}{
		{"stamped-sha", info, "stamped-sha"},
		{"", info, "vcs-sha"},
		{"", &debug.BuildInfo{}, ""},
		{"", nil, ""},
		{"stamped-sha", nil, "stamped-sha"},
	}
	for _, tc := range cases {
		if got := resolveRevision(tc.stamped, tc.info); got != tc.want {
			t.Errorf("resolveRevision(%q, %v) = %q, want %q", tc.stamped, tc.info, got, tc.want)
		}
	}
}

func TestHTTPDate(t *testing.T) {
	cases := map[string]string{
		"":                          "",
		"2026-11-02T10:00:00Z":      "Mon, 02 Nov 2026 10:00:00 GMT",
		"2026-11-02T12:00:00+02:00": "Mon, 02 Nov 2026 10:00:00 GMT",
		"2026-11-02":                "",
		"garbage":                   "",
	}
	for in, want := range cases {
		if got := httpDate(in); got != want {
			t.Errorf("httpDate(%q) = %q, want %q", in, got, want)
		}
	}
}

// ---- the digests against the rendered pages ----------------------------------

// renderedPages composes every page internal/revision dates, keyed as it keys
// them, from one artifact: the whole document (head and body, payload included)
// and the part a reader or a crawler reads without running the island (the head
// and the fact sheet). A work page's entry also carries its chapters pages,
// whose date it is.
func renderedPages(t *testing.T, snap *query.DB) (whole, visible map[revision.Key]string) {
	t.Helper()
	whole, visible = map[revision.Key]string{}, map[revision.Key]string{}
	add := func(k revision.Key, p *entityPage) {
		if p == nil {
			return
		}
		whole[k] += p.renderHead() + p.renderBody()
		visible[k] += p.renderHead() + p.factSheet
	}
	compose := map[query.PageFamily]composeFunc{
		query.WorkPages: composeWorkPage, query.RecapPages: composeRecapPage, query.CharacterPages: composeCharactersPage,
		query.PersonPages: composePersonPage, query.SeriesPages: composeSeriesPage,
	}
	for _, family := range slices.Sorted(maps.Keys(compose)) {
		entries, err := snap.Pages(family, 1000, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			k := revision.Key{Family: family, ID: e.ID}
			p, err := compose[family](testSiteURL, snap, e.ID)
			if err != nil {
				t.Fatal(err)
			}
			add(k, p)
			if family != query.WorkPages {
				continue
			}
			d, err := snap.WorkDetail(e.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, rec := range d.Recordings {
				cp, err := composeChaptersPage(context.Background(), testSiteURL, snap, e.ID, rec.ID, 1)
				if err != nil {
					t.Fatal(err)
				}
				add(k, cp)
			}
		}
	}
	return whole, visible
}

// TestDigestsMoveWithTheRenderedPage bounds the digests by what metaserve
// actually renders, over a spread of fixture mutations, in both directions:
//
//   - a page whose digest moved must render differently (no date is ever moved
//     for a page whose bytes are unchanged), and
//   - a page whose visible part - the head and the fact sheet - renders
//     differently must have moved.
//
// Between the two bounds sits the embedded payload: it is the API document the
// island hydrates from, so a work page's payload carries the recap text the
// recap page owns, and a change to it alone moves only the recap page.
func TestDigestsMoveWithTheRenderedPage(t *testing.T) {
	rename := func(id, name string) func(*model.Catalog) {
		return func(c *model.Catalog) {
			for _, p := range c.People {
				if p.ID == id {
					p.Name = name
				}
			}
		}
	}
	hailMary := func(c *model.Catalog) *model.Work {
		for _, w := range c.Works {
			if w.ID == artifacttest.HailMary {
				return w
			}
		}
		panic("no Project Hail Mary")
	}
	mutations := map[string]func(*model.Catalog){
		"rename an author":     rename("andy-weir", "Andrew Weir"),
		"rename a narrator":    rename("kate-reading", "Katherine Reading"),
		"rename a series hand": rename("brandon-sanderson", "B. Sanderson"),
		"recap text":           func(c *model.Catalog) { c.Recaps[0].Recaps[1].Text = "Grace wakes up." },
		"recap summary":        func(c *model.Catalog) { c.Recaps[0].InShort = "A shorter summary." },
		"character":            func(c *model.Catalog) { c.Characters[0].Characters[0].Aliases = []string{"Ryland"} },
		"description":          func(c *model.Catalog) { c.Descriptions[0].Text += " More." },
		"position scale unit":  func(c *model.Catalog) { c.PositionScales[0].Unit = "part" },
		"position scale size":  func(c *model.Catalog) { c.PositionScales[0].Length = 13 },
		"chapter title":        func(c *model.Catalog) { hailMary(c).Recordings[0].Chapters[2].Title = "Two" },
		"cover":                func(c *model.Catalog) { hailMary(c).Recordings[0].CoverURL = "https://example.test/phm2.jpg" },
		"runtime":              func(c *model.Catalog) { hailMary(c).Recordings[0].RuntimeMin = 971 },
		"genre":                func(c *model.Catalog) { hailMary(c).Genres = append(hailMary(c).Genres, "thriller") },
		"title":                func(c *model.Catalog) { hailMary(c).Title = "Project Hail Mary!" },
		"added_at":             func(c *model.Catalog) { hailMary(c).AddedAt = "2026-11-01T00:00:00Z" },
		"series membership": func(c *model.Catalog) {
			c.Series[0].Works = append(c.Series[0].Works, model.SeriesWork{Work: artifacttest.HailMary, Position: "3"})
		},
		"series position": func(c *model.Catalog) { c.Series[0].Works[1].Position = "2.5" },
		"new work": func(c *model.Catalog) {
			c.Works = append(c.Works, &model.Work{ID: "the-martian", Title: "The Martian", Language: "en",
				Authors: []string{"andy-weir"}, License: "CC0-1.0"})
		},
	}
	open := func(cat *model.Catalog) (*query.DB, map[revision.Key]revision.Digest) {
		snap := snapshotFor(t, cat)
		d, err := revision.Digests(context.Background(), snap, 2)
		if err != nil {
			t.Fatal(err)
		}
		return snap, d
	}
	baseSnap, baseDigests := open(artifacttest.Fixture())
	baseWhole, baseVisible := renderedPages(t, baseSnap)
	if len(baseDigests) != len(baseWhole) {
		t.Fatalf("%d digests for %d rendered pages", len(baseDigests), len(baseWhole))
	}

	names := make([]string, 0, len(mutations))
	for n := range mutations {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			cat := artifacttest.Fixture()
			mutations[name](cat)
			snap, digests := open(cat)
			whole, visible := renderedPages(t, snap)
			moved := 0
			for k, d := range digests {
				old, existed := baseDigests[k]
				digestMoved := !existed || old != d
				if digestMoved {
					moved++
				}
				if digestMoved && existed && whole[k] == baseWhole[k] {
					t.Errorf("%v: digest moved, but the page renders the same bytes", k)
				}
				if !digestMoved && visible[k] != baseVisible[k] {
					t.Errorf("%v: the visible page changed, but the digest did not", k)
				}
			}
			if moved == 0 && name != "added_at" && name != "position scale size" {
				t.Errorf("no page moved")
			}
		})
	}
}
