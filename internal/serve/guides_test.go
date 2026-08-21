package serve

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// The community guide pages (guides.go) and their sitemap families. The fixture
// catalogue already carries both sidecars on project-hail-mary and none on the
// other three works, which is exactly the pair of cases these pages turn on.

const (
	phmRecapPath      = "/works/project-hail-mary" + recapSuffix
	phmCharactersPath = "/works/project-hail-mary" + charactersSuffix
)

// ---- the pages --------------------------------------------------------------

// TestRecapPageCarriesTheRankingSubstance is the whole point of the page in one
// test: the query words are in the title, the H1 and the summary lines; the
// recap TEXT is in the markup (so it is indexable) but inside a closed
// <details> and a data-nosnippet block (so it is neither shown to a reader who
// did not ask nor quoted into a result snippet); and the share-alike terms the
// text travels under are stated with a rel="license" link.
func TestRecapPageCarriesTheRankingSubstance(t *testing.T) {
	ts := newPageServer(t, fixtureCatalog(), markedShells)
	code, page := getPage(t, ts.URL, phmRecapPath)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}

	for _, want := range []string{
		"<title>Project Hail Mary recap - story so far and chapter summaries - AudioSilo Meta</title>",
		`<link rel="canonical" href="https://meta.test/works/project-hail-mary/recap">`,
		`<meta property="og:type" content="article">`,
		// The cover rides along, so a shared link is not a bare icon.
		`<meta property="og:image" content="https://example.test/phm.jpg">`,
		`<h1 class="text-hi">Project Hail Mary recap</h1>`,
		"<summary>In short",
		"<summary>Story so far - through chapter 2",
		"<summary>Story so far - through chapter 9",
		"<summary>How does it end?",
		`<div data-nosnippet>`,
		`<a rel="license" href="https://creativecommons.org/licenses/by-sa/4.0/">CC BY-SA 4.0</a>`,
		`href="/build?work=project-hail-mary&amp;kind=recaps"`,
		// The crawl mesh: back to the work, across to the sibling guide.
		`<a href="/works/project-hail-mary">Full details for Project Hail Mary</a>`,
		`<a href="/works/project-hail-mary/characters">Project Hail Mary character guide</a>`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("recap page does not carry %q:\n%s", want, page)
		}
	}

	// The recap TEXT is present, and every paragraph of it sits behind a
	// data-nosnippet block inside a details element.
	const text = "First contact is made."
	if !strings.Contains(page, text) {
		t.Errorf("the recap text is not in the markup, so nothing indexes it:\n%s", page)
	}
	body := between(t, page, `<div id="ssr-entity">`, `</div><script type="application/json"`)
	if n := strings.Count(body, "<details>"); n != 4 {
		t.Errorf("fact sheet carries %d details elements, want 4 (in short, two recaps, ending)", n)
	}
	if strings.Count(body, "<details>") != strings.Count(body, `<div data-nosnippet>`) {
		t.Errorf("not every details body sits inside a data-nosnippet block:\n%s", body)
	}
	if strings.Contains(body, "<details open") {
		t.Error("a spoiler row is open by default")
	}

	// And no spoiler reaches the head, which is what a search result quotes.
	head := between(t, page, headOpenMarker, headCloseMarker)
	for _, spoiler := range []string{text, "Grace stays on Erid", "befriends an alien"} {
		if strings.Contains(head, spoiler) {
			t.Errorf("recap text %q reached the head, where a snippet quotes it:\n%s", spoiler, head)
		}
	}
}

// TestCharactersPageCarriesTheRankingSubstance is its sibling. A character's
// ALIASES sit inside the data-nosnippet block with the description: "also known
// as" is as much a spoiler as the description itself.
func TestCharactersPageCarriesTheRankingSubstance(t *testing.T) {
	ts := newPageServer(t, fixtureCatalog(), markedShells)
	code, page := getPage(t, ts.URL, phmCharactersPath)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}

	for _, want := range []string{
		"<title>Project Hail Mary characters - a spoiler-safe character guide - AudioSilo Meta</title>",
		`<link rel="canonical" href="https://meta.test/works/project-hail-mary/characters">`,
		`<h1 class="text-hi">Project Hail Mary characters</h1>`,
		"<summary>Ryland Grace <span class=\"text-dim\">(Protagonist)</span></summary>",
		"<summary>Rocky <span class=\"text-dim\">(Supporting)</span></summary>",
		"Also known as Dr. Grace",
		"Appears from the start",
		"First appears in chapter 8",
		`<a rel="license" href="https://creativecommons.org/licenses/by-sa/4.0/">CC BY-SA 4.0</a>`,
		`href="/build?work=project-hail-mary&amp;kind=characters"`,
		`<a href="/works/project-hail-mary/recap">Project Hail Mary recap</a>`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("characters page does not carry %q:\n%s", want, page)
		}
	}
	head := between(t, page, headOpenMarker, headCloseMarker)
	if strings.Contains(head, "wakes aboard the ship with amnesia") {
		t.Errorf("a character description reached the head:\n%s", head)
	}
	if !strings.Contains(head, `content="Character guide to Project Hail Mary, by Andy Weir, 2 characters`) {
		t.Errorf("the description does not state the facts it is composed from:\n%s", head)
	}
}

// TestGuidePageNeedsItsMember: the page EXISTS iff the sidecar does. A work the
// catalogue holds but with no recaps has no recap page - it is the site's 404,
// not a 200 with an empty body, which is what a crawler would otherwise index
// for the very query this feature targets.
func TestGuidePageNeedsItsMember(t *testing.T) {
	ts := newPageServer(t, fixtureCatalog(), markedShells)
	for _, path := range []string{
		"/works/the-way-of-kings" + recapSuffix,      // a live work, no recaps
		"/works/the-way-of-kings" + charactersSuffix, // a live work, no characters
		"/works/no-such-work" + recapSuffix,          // no such work
		"/works/no-such-work" + charactersSuffix,
		"/works/search" + recapSuffix, // a reserved slug: no record can hold one
	} {
		code, page := getPage(t, ts.URL, path)
		if code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, code)
			continue
		}
		if !strings.Contains(page, "CUSTOM 404") {
			t.Errorf("GET %s body = %q, want the site's 404 page", path, page)
		}
	}
}

// TestRecapPageServesASummaryOnlyWork pins the other half of hasRecapGuide: a
// work with no chaptered recaps but a whole-book summary still has a page, and
// its title promises a story summary rather than chapter summaries it does not
// have.
func TestRecapPageServesASummaryOnlyWork(t *testing.T) {
	ts := newPageServer(t, guideCatalog(), markedShells)
	code, page := getPage(t, ts.URL, "/works/only-summary"+recapSuffix)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if !strings.Contains(page, "<title>Only Summary recap - story summary - AudioSilo Meta</title>") {
		t.Errorf("summary-only recap page promises chapter summaries it does not have:\n%s", page)
	}
	if n := strings.Count(page, "<details>"); n != 1 {
		t.Errorf("page carries %d rows, want the one whole-book summary", n)
	}
	// A work with only characters has no recap page, and vice versa.
	if code, _ := getPage(t, ts.URL, "/works/only-characters"+recapSuffix); code != http.StatusNotFound {
		t.Errorf("recap page of a characters-only work = %d, want 404", code)
	}
	if code, _ := getPage(t, ts.URL, "/works/only-summary"+charactersSuffix); code != http.StatusNotFound {
		t.Errorf("characters page of a recaps-only work = %d, want 404", code)
	}
}

// TestGuidePayloadIsTheWorkAPIResponse is the hydration contract: the payload a
// guide page embeds is the WORK's own API document, byte for byte, so the island
// hydrates from it with no second fetch and cannot be handed a shape the API
// would not have returned.
func TestGuidePayloadIsTheWorkAPIResponse(t *testing.T) {
	ts := newPageServer(t, fixtureCatalog(), markedShells)
	_, apiBody := getBody(t, ts.URL+"/api/v1/works/project-hail-mary")
	want := strings.TrimSuffix(apiBody, "\n")
	for _, path := range []string{phmRecapPath, phmCharactersPath} {
		_, page := getPage(t, ts.URL, path)
		if got := entityPayload(t, page); got != want {
			t.Errorf("%s payload differs from the work API response\npage: %s\napi:  %s", path, got, want)
		}
	}
}

// TestGuidePagesAreDeterministic: one snapshot renders one page, byte for byte.
func TestGuidePagesAreDeterministic(t *testing.T) {
	ts := newPageServer(t, fixtureCatalog(), markedShells)
	for _, path := range []string{phmRecapPath, phmCharactersPath} {
		_, first := getPage(t, ts.URL, path)
		_, second := getPage(t, ts.URL, path)
		if first != second {
			t.Errorf("GET %s rendered differently on the second request", path)
		}
	}
}

// TestGuidePagesValidateSeparately is why the page's own name is part of its
// identity (see pageIdentity): a work and its two guide pages are three
// representations of ONE slug, and the fixture builds all three from identical
// shell bytes. Without the name they would share a validator and a client would
// be handed a 304 for a page it never asked for.
func TestGuidePagesValidateSeparately(t *testing.T) {
	ts := newPageServer(t, fixtureCatalog(), markedShells)
	etags := map[string]string{}
	for _, path := range []string{"/works/project-hail-mary", phmRecapPath, phmCharactersPath} {
		resp := getNoFollow(t, ts.URL, path)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s = %d", path, resp.StatusCode)
		}
		if _, err := io.Copy(io.Discard, resp.Body); err != nil {
			t.Fatal(err)
		}
		etag := resp.Header.Get("ETag")
		if etag == "" {
			t.Fatalf("GET %s issued no ETag", path)
		}
		if cc := resp.Header.Get("Cache-Control"); cc != entityMaxAge {
			t.Errorf("GET %s Cache-Control = %q, want %q", path, cc, entityMaxAge)
		}
		if where, dup := etags[etag]; dup {
			t.Errorf("%s and %s share the validator %q", path, where, etag)
		}
		etags[etag] = path

		// And the page's own validator revalidates to a bodyless 304.
		cond := conditionalGet(t, ts.URL+path, etag)
		if cond.StatusCode != http.StatusNotModified {
			t.Errorf("conditional GET %s = %d, want 304", path, cond.StatusCode)
			continue
		}
		wantBodyless(t, cond)
	}
}

// TestRetiredSlugRedirectsOnGuidePages: the tombstone mechanism reaches these
// pages for free, because they are rows in htmlEntityRoutes and the 301's
// Location is rebuilt from the matched pattern - so the sub-path survives with
// no special case. TestEveryIDRouteResolvesRetiredSlugs is what makes that a
// rule rather than a coincidence; this is the behaviour it promises.
func TestRetiredSlugRedirectsOnGuidePages(t *testing.T) {
	ts := newPageServer(t, fixtureCatalog(), markedShells)
	cases := []struct{ path, location string }{
		{"/works/project-hail-mary-audiobook" + recapSuffix, phmRecapPath},
		{"/works/project-hail-mary-audiobook" + charactersSuffix, phmCharactersPath},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			resp := getNoFollow(t, ts.URL, tc.path)
			if resp.StatusCode != http.StatusMovedPermanently {
				t.Fatalf("status = %d, want 301", resp.StatusCode)
			}
			if got := resp.Header.Get("Location"); got != tc.location {
				t.Errorf("Location = %q, want %q", got, tc.location)
			}
			if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
				t.Errorf("Content-Type = %q, want html", ct)
			}
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(body), `<a href="`+tc.location+`">`) {
				t.Errorf("301 body does not link the new URL: %s", body)
			}
		})
	}
}

// TestGuidePageJSONLD pins the document these pages carry: an Article ABOUT the
// work's Book node, licensed under the share-alike deed. The `about` @id has to
// be the very node the WORK page defines, or it references nothing.
func TestGuidePageJSONLD(t *testing.T) {
	ts := newPageServer(t, fixtureCatalog(), markedShells)
	_, page := getPage(t, ts.URL, phmRecapPath)
	var doc map[string]any
	if err := json.Unmarshal([]byte(between(t, page, `<script type="application/ld+json">`, "</script>")), &doc); err != nil {
		t.Fatalf("JSON-LD does not parse: %v", err)
	}
	if doc["@type"] != "Article" {
		t.Errorf("@type = %v, want Article", doc["@type"])
	}
	if doc["headline"] != "Project Hail Mary recap" {
		t.Errorf("headline = %v", doc["headline"])
	}
	if doc["license"] != ccBySAURL {
		t.Errorf("license = %v, want the CC BY-SA deed", doc["license"])
	}
	about, _ := doc["about"].(map[string]any)
	if about == nil || about["@id"] != workNodeID(testSiteURL+"/works/project-hail-mary") {
		t.Errorf("about = %v, want the work page's Book node", doc["about"])
	}
	// The same @id the work page's own graph defines - asserted from that page
	// rather than from a second spelling of the string.
	_, workPage := getPage(t, ts.URL, "/works/project-hail-mary")
	if !strings.Contains(workPage, `"@id":"`+about["@id"].(string)+`"`) {
		t.Errorf("the work page defines no node with @id %v", about["@id"])
	}
	// No fabricated authorship or dates for community-written text.
	for _, key := range []string{"author", "datePublished", "dateModified", "aggregateRating"} {
		if _, has := doc[key]; has {
			t.Errorf("JSON-LD states %s, which the artifact carries nothing for", key)
		}
	}
}

// ---- the work page's Guides section -----------------------------------------

// TestWorkPageLinksItsGuides is the internal discovery path (and the branded
// query's landing spot): the work page names its guides, with descriptive anchor
// text, and carries none of their text.
func TestWorkPageLinksItsGuides(t *testing.T) {
	ts := newPageServer(t, fixtureCatalog(), markedShells)
	_, page := getPage(t, ts.URL, "/works/project-hail-mary")
	for _, want := range []string{
		"<h2>Guides</h2>",
		`<a href="/works/project-hail-mary/recap">Chapter-by-chapter recap of Project Hail Mary</a>`,
		`<a href="/works/project-hail-mary/characters">Project Hail Mary character guide</a>`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("work page does not carry %q:\n%s", want, page)
		}
	}
	// The sidecar TEXT stays off the work page: the guide pages own it, and two
	// URLs carrying the same paragraphs split the crawl weight for one query.
	sheet := between(t, page, `<div id="ssr-entity">`, `</div><script type="application/json"`)
	for _, spoiler := range []string{"First contact is made.", "wakes aboard the ship with amnesia"} {
		if strings.Contains(sheet, spoiler) {
			t.Errorf("the work fact sheet carries sidecar text %q", spoiler)
		}
	}

	// A work with no sidecars has no section at all - and no link to a 404.
	_, plain := getPage(t, ts.URL, "/works/the-way-of-kings")
	if strings.Contains(plain, "<h2>Guides</h2>") {
		t.Errorf("a work with no sidecars advertises a Guides section:\n%s", plain)
	}
	// A summary-only work gets the anchor its data supports.
	guides := newPageServer(t, guideCatalog(), markedShells)
	_, summaryOnly := getPage(t, guides.URL, "/works/only-summary")
	if !strings.Contains(summaryOnly, `<a href="/works/only-summary/recap">Story summary of Only Summary</a>`) {
		t.Errorf("summary-only work does not link its guide with the anchor its data supports:\n%s", summaryOnly)
	}
}

// ---- the sitemap families ---------------------------------------------------

// guideCatalog is the sidecar partition the guide sitemaps have to get right:
// one work with chaptered recaps only, one with a whole-book summary only, one
// with characters only, and one with nothing. The recap family is the UNION of
// the first two, so a catalogue where the two tables name DIFFERENT works is
// what tells a union apart from either table alone.
func guideCatalog() *model.Catalog {
	work := func(id, title string) *model.Work {
		return &model.Work{
			ID: id, Title: title, Language: "en",
			Authors: []string{"lone-author"}, License: "CC0-1.0",
		}
	}
	return &model.Catalog{
		People: []*model.Person{{ID: "lone-author", Name: "Lone Author", License: "CC0-1.0"}},
		Works: []*model.Work{
			work("only-chapters", "Only Chapters"),
			work("only-characters", "Only Characters"),
			work("only-summary", "Only Summary"),
			work("plain-work", "Plain Work"),
		},
		Characters: []*model.Characters{{
			Work: "only-characters", License: "CC-BY-SA-4.0",
			Characters: []model.Character{{ID: "someone", Name: "Someone", Reveal: model.Position{Chapter: 3}}},
		}},
		Recaps: []*model.Recaps{
			{
				Work: "only-chapters", License: "CC-BY-SA-4.0",
				Recaps: []model.Recap{{Through: model.Position{Chapter: 4}, Scope: "book", Text: "Things happen."}},
			},
			{Work: "only-summary", License: "CC-BY-SA-4.0", InShort: "It all works out."},
		},
	}
}

// TestGuideSitemapShardsListEveryGuidePage walks both new families: the locs are
// the guide page URLs in id order, the recap family is the union of the two
// tables a recap page can be built from, and a work with no sidecar appears in
// neither.
func TestGuideSitemapShardsListEveryGuidePage(t *testing.T) {
	_, ts := newPageServerFrom(t, quietConfig(t, guideCatalog(), markedShells))
	cases := []struct {
		path string
		want []string
	}{
		{"/sitemaps/recaps-0.xml", []string{
			testSiteURL + "/works/only-chapters/recap",
			testSiteURL + "/works/only-summary/recap",
		}},
		{"/sitemaps/characters-0.xml", []string{
			testSiteURL + "/works/only-characters/characters",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			code, body, _ := getSitemap(t, ts.URL, tc.path)
			if code != http.StatusOK {
				t.Fatalf("status = %d, want 200\n%s", code, body)
			}
			var locs []string
			for _, u := range parseURLSet(t, body).URLs {
				locs = append(locs, u.Loc)
				// The artifact carries no date for a sidecar, so nothing is stated.
				if u.LastMod != "" {
					t.Errorf("%s states a lastmod %q the artifact does not carry", u.Loc, u.LastMod)
				}
			}
			if strings.Join(locs, "\n") != strings.Join(tc.want, "\n") {
				t.Errorf("locs =\n%s\nwant\n%s", strings.Join(locs, "\n"), strings.Join(tc.want, "\n"))
			}
			// And every URL the sitemap promises is actually served. This is what
			// ties the family's suffix to the route's literal: a sitemap that
			// advertises a 404 is worse than one that advertises nothing.
			for _, loc := range locs {
				if code, _ := getPage(t, ts.URL, strings.TrimPrefix(loc, testSiteURL)); code != http.StatusOK {
					t.Errorf("the sitemap promises %s, which answers %d", loc, code)
				}
			}
		})
	}
}

// TestGuideSitemapsSkipACatalogueWithNoSidecars: the families are empty, so they
// contribute no index entry and 404 at every shard - the empty-family rule, which
// matters more here than for the record families because most works carry no
// sidecar at all.
func TestGuideSitemapsSkipACatalogueWithNoSidecars(t *testing.T) {
	ts := sitemapServerWithoutStatic(t, seriesLessCatalog())
	_, index, _ := getSitemap(t, ts.URL, sitemapIndexPath)
	for _, absent := range []string{"/sitemaps/recaps-", "/sitemaps/characters-"} {
		if strings.Contains(index, absent) {
			t.Errorf("index lists %s for a catalogue with no sidecars:\n%s", absent, index)
		}
	}
	for _, path := range []string{"/sitemaps/recaps-0.xml", "/sitemaps/characters-0.xml"} {
		if code, _, _ := getSitemap(t, ts.URL, path); code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, code)
		}
	}
}

// TestGuideSitemapsTolerateOlderArtifacts is the version gate, on both sides of
// it. A v2 artifact has recaps but no recap_summaries, so the family is that one
// table - counted AND listed against it, which is the pairing recapSitemapSQL
// derives together. A v1 artifact predates the sidecars entirely and honestly
// advertises no guide shards at all.
func TestGuideSitemapsTolerateOlderArtifacts(t *testing.T) {
	t.Run("v2, before recap summaries", func(t *testing.T) {
		ts := downgradedServer(t, guideCatalog(), 2, "work_genres", "recap_summaries", "redirects")
		code, body, _ := getSitemap(t, ts.URL, "/sitemaps/recaps-0.xml")
		if code != http.StatusOK {
			t.Fatalf("recaps shard on a v2 artifact = %d, want 200\n%s", code, body)
		}
		var locs []string
		for _, u := range parseURLSet(t, body).URLs {
			locs = append(locs, u.Loc)
		}
		want := []string{testSiteURL + "/works/only-chapters/recap"}
		if strings.Join(locs, "\n") != strings.Join(want, "\n") {
			t.Errorf("locs = %v, want %v (the summary-only work has no page on this artifact)", locs, want)
		}
		// The count agrees with the listing: one URL is one shard, and there is no
		// second one promising the rest.
		if code, _, _ := getSitemap(t, ts.URL, "/sitemaps/recaps-1.xml"); code != http.StatusNotFound {
			t.Errorf("a second recaps shard = %d, want 404", code)
		}
	})

	t.Run("v1, before the sidecars", func(t *testing.T) {
		ts := downgradedServer(t, guideCatalog(), 1,
			"work_genres", "characters", "character_aliases", "recaps", "recap_summaries", "redirects")
		_, index, _ := getSitemap(t, ts.URL, sitemapIndexPath)
		for _, absent := range []string{"/sitemaps/recaps-", "/sitemaps/characters-"} {
			if strings.Contains(index, absent) {
				t.Errorf("a v1 artifact advertises %s:\n%s", absent, index)
			}
		}
		for _, path := range []string{"/sitemaps/recaps-0.xml", "/sitemaps/characters-0.xml"} {
			if code, _, _ := getSitemap(t, ts.URL, path); code != http.StatusNotFound {
				t.Errorf("GET %s on a v1 artifact = %d, want 404", path, code)
			}
		}
		// And the rest of the sitemap surface is untouched by their absence.
		if !strings.Contains(index, "/sitemaps/works-0.xml") {
			t.Errorf("the works shard went missing with the sidecars:\n%s", index)
		}
	})
}

// ---- the presence probe -----------------------------------------------------

// snapshotOf opens an artifact path as a snapshot, for the probe tests below -
// they ask the query layer how it degrades, which is a question a route cannot
// put (the downgraded servers carry no site directory, so no page route exists
// on them at all).
func snapshotOf(t *testing.T, dbPath string) *snapshot {
	t.Helper()
	snap, err := openSnapshot(dbPath, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(snap.close)
	return snap
}

// TestGuideProbeAgreesWithTheGuideDefinition is what makes the probe an
// optimization rather than a second definition: for every work in the sidecar
// partition, the cheap point query and the composed document's own
// hasRecapGuide / hasCharacterGuide must give the same answer. A disagreement
// would be a page the sitemap promises and the route 404s, or the reverse.
func TestGuideProbeAgreesWithTheGuideDefinition(t *testing.T) {
	snap := snapshotFor(t, guideCatalog())
	for _, id := range []string{"only-chapters", "only-characters", "only-summary", "plain-work", "no-such-work"} {
		t.Run(id, func(t *testing.T) {
			d, err := snap.workDetail(id)
			if err != nil {
				t.Fatal(err)
			}
			probes := []struct {
				name  string
				probe func(string) (bool, error)
				want  bool
			}{
				{"recap", snap.hasRecapPage, d != nil && hasRecapGuide(d)},
				{"characters", snap.hasCharacterPage, d != nil && hasCharacterGuide(d)},
			}
			for _, p := range probes {
				got, err := p.probe(id)
				if err != nil {
					t.Fatalf("%s probe: %v", p.name, err)
				}
				if got != p.want {
					t.Errorf("%s probe = %v, but the composed document says %v", p.name, got, p.want)
				}
			}
		})
	}
}

// TestGuideProbesTolerateOlderArtifacts is the probe's own version gate, the
// half TestGuideSitemapsTolerateOlderArtifacts cannot reach through a route. At
// v2 the recap probe must not NAME recap_summaries - a query mentioning a table
// the artifact does not have would not even parse, so the page would be a 500
// where the artifact honestly supports a 404. At v1 neither probe may query at
// all: every sidecar table is gone, and the answer is "no page" for every work.
func TestGuideProbesTolerateOlderArtifacts(t *testing.T) {
	t.Run("v2, before recap summaries", func(t *testing.T) {
		snap := snapshotOf(t, downgradedDB(t, guideCatalog(), 2, "work_genres", "recap_summaries", "redirects"))
		want := map[string]bool{"only-chapters": true, "only-summary": false, "plain-work": false}
		for id, expect := range want {
			got, err := snap.hasRecapPage(id)
			if err != nil {
				t.Fatalf("recap probe on a v2 artifact: %v", err)
			}
			if got != expect {
				t.Errorf("hasRecapPage(%q) on a v2 artifact = %v, want %v", id, got, expect)
			}
		}
	})

	t.Run("v1, before the sidecars", func(t *testing.T) {
		snap := snapshotOf(t, downgradedDB(t, guideCatalog(), 1,
			"work_genres", "characters", "character_aliases", "recaps", "recap_summaries", "redirects"))
		for _, id := range []string{"only-chapters", "only-characters", "only-summary"} {
			recap, err := snap.hasRecapPage(id)
			if err != nil {
				t.Fatalf("recap probe on a v1 artifact: %v", err)
			}
			chars, err := snap.hasCharacterPage(id)
			if err != nil {
				t.Fatalf("character probe on a v1 artifact: %v", err)
			}
			if recap || chars {
				t.Errorf("%s: probes on a v1 artifact = (%v, %v), want no page either way", id, recap, chars)
			}
		}
	})
}

// ---- label helpers ----------------------------------------------------------

// TestRecapRowLabel pins the spoiler-safe summary line, including the two
// chapter-0 forms: 0 is front matter or prior-book knowledge (see the position
// model), so a series-scoped recap there is the "previously" catch-up and a
// book-scoped one is what you need before starting.
func TestRecapRowLabel(t *testing.T) {
	cases := []struct {
		in   recapOut
		want string
	}{
		{recapOut{Through: positionOut{Chapter: 0}, Scope: "series"}, "Previously, in earlier books"},
		{recapOut{Through: positionOut{Chapter: 0}, Scope: "book"}, "Before this book"},
		{recapOut{Through: positionOut{Chapter: 0}}, "Before this book"},
		{recapOut{Through: positionOut{Chapter: 1}, Scope: "book"}, "Story so far - through chapter 1"},
		{recapOut{Through: positionOut{Chapter: 12}}, "Story so far - through chapter 12"},
	}
	for _, tc := range cases {
		if got := recapRowLabel(tc.in); got != tc.want {
			t.Errorf("recapRowLabel(%+v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestRevealLabelAndRoleLabel: an unstated role states nothing (no badge), and
// chapters 0 and 1 are both "from the start" - neither is a reveal to gate.
func TestRevealLabelAndRoleLabel(t *testing.T) {
	reveals := map[int]string{
		0: "Appears from the start", 1: "Appears from the start",
		2: "First appears in chapter 2", 40: "First appears in chapter 40",
	}
	for in, want := range reveals {
		if got := revealLabel(in); got != want {
			t.Errorf("revealLabel(%d) = %q, want %q", in, got, want)
		}
	}
	roles := map[string]string{
		"protagonist": "Protagonist", "antagonist": "Antagonist",
		"supporting": "Supporting", "minor": "Minor",
		"": "", "narrator": "",
	}
	for in, want := range roles {
		if got := characterRoleLabel(in); got != want {
			t.Errorf("characterRoleLabel(%q) = %q, want %q", in, got, want)
		}
	}
	scopes := map[string]string{"series": "earlier books", "book": "this book", "": ""}
	for in, want := range scopes {
		if got := recapScopeLabel(in); got != want {
			t.Errorf("recapScopeLabel(%q) = %q, want %q", in, got, want)
		}
	}
}
