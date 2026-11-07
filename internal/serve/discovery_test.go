package serve

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/artifacttest"
	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/query"
)

func TestDirectorySearchFiltersAndNavigation(t *testing.T) {
	cat := catalogueFixture()
	for i := 0; i < 153; i++ {
		cat.Series = append(cat.Series, &model.Series{ID: fmt.Sprintf("mixed-%03d", i), Name: fmt.Sprintf("Mixed series %03d", i), License: "CC0-1.0"})
	}
	ts := newPageServer(t, cat, markedShells)
	path := "/browse/series?letter=M&page=2&q=Mixed"
	code, body := getPage(t, ts.URL, path)
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	for _, want := range []string{`<form method="get" action="/browse/series" class="directory-search">`, `name="q" type="search" value="Mixed"`, `name="letter" value="M"`, `name="page" type="number" min="1" max="4" value="2"`, `href="/browse/series?letter=M&amp;q=Mixed">First`, `href="/browse/series?letter=M&amp;page=4&amp;q=Mixed">Last`, `href="/browse/series?letter=M&amp;page=3&amp;q=Mixed"`, `href="/browse/series?letter=N&amp;q=Mixed"`, `href="/browse/works?letter=M&amp;q=Mixed"`, `href="https://meta.test/browse/series?letter=M&amp;page=2&amp;q=Mixed"`, `name="robots" content="noindex,follow"`, `href="/browse/series">Clear filters`, `Showing 50 of 153 entries`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Count(body, `class="directory-jump"`) != 2 || strings.Count(body, `class="directory-card"`) != 50 {
		t.Error("expected bounded cards and top/bottom jumps")
	}
	_, letters := getPage(t, ts.URL, "/browse/series?letter=M")
	if strings.Contains(letters, `name="robots"`) {
		t.Error("letter pages must remain indexable")
	}
	_, empty := getPage(t, ts.URL, "/browse/series?letter=M&q=NoSuchSeries")
	if !strings.Contains(empty, "No entries match these filters") || strings.Contains(empty, `class="directory-jump"`) {
		t.Error("empty filtered navigation")
	}
	for _, pair := range []struct{ from, to string }{{"/browse/series?q=+Mixed++&page=01&letter=m&tracking=1", "/browse/series?letter=M&q=Mixed"}, {"/browse/series?q=&letter=all", "/browse/series"}} {
		resp := artifacttest.GetNoFollow(t, ts.URL, pair.from)
		_ = resp.Body.Close()
		if resp.StatusCode != 301 || resp.Header.Get("Location") != pair.to {
			t.Errorf("%s: %d %s", pair.from, resp.StatusCode, resp.Header.Get("Location"))
		}
	}
	for _, suffix := range []string{"q=x&q=y", "letter=A&letter=B", "letter=AB", "letter=%C3%89", "q=" + strings.Repeat("x", 257), "q=%FF", "q=%zz", "q=x;letter=M"} {
		resp := artifacttest.GetNoFollow(t, ts.URL, "/browse/series?"+suffix)
		_ = resp.Body.Close()
		if resp.StatusCode != 400 {
			t.Errorf("accepted %s: %d", suffix, resp.StatusCode)
		}
	}
	for _, family := range []string{"works", "people", "series", "recaps", "characters"} {
		if code, body := getPage(t, ts.URL, "/browse/"+family); code != 200 || !strings.Contains(body, "Search this directory") || !strings.Contains(body, "Filter by first letter") {
			t.Errorf("controls absent from %s", family)
		}
	}
}

func TestDirectoryQuotedUnicodeAndValidators(t *testing.T) {
	cat := artifacttest.Fixture()
	cat.Series = append(cat.Series, &model.Series{ID: "quoted", Name: `Étoile "quoted" & <name>`, License: "CC0-1.0"})
	ts := newPageServer(t, cat, markedShells)
	path := "/browse/series?letter=Other&q=" + url.QueryEscape(`Étoile "quoted"`)
	code, body := getPage(t, ts.URL, path)
	if code != 200 || !strings.Contains(body, `Étoile &#34;quoted&#34; &amp; &lt;name&gt;`) {
		t.Errorf("unsafe/missing quoted name, status %d", code)
	}
	var previous string
	for _, suffix := range []string{"", "?letter=M", "?letter=T", "?q=Stormlight", "?letter=T&q=Stormlight"} {
		resp := artifacttest.GetNoFollow(t, ts.URL, "/browse/series"+suffix)
		_ = resp.Body.Close()
		tag := resp.Header.Get("ETag")
		if tag == "" || tag == previous {
			t.Errorf("shared validator for %s: %s", suffix, tag)
		}
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/browse/series"+suffix, nil)
		req.Header.Set("If-None-Match", tag)
		cached, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = cached.Body.Close()
		if cached.StatusCode != 304 {
			t.Errorf("revalidate %s: %d", suffix, cached.StatusCode)
		}
		previous = tag
	}
}

func TestDirectoryPaginationStartMiddleEndAndEmpty(t *testing.T) {
	for _, tc := range []struct {
		page, total, links int
		first, last        bool
	}{{1, 45386, 5, false, true}, {454, 45386, 9, true, true}, {908, 45386, 5, true, false}, {1, 0, 1, false, false}} {
		v := newDirectoryPagination("/browse/series", query.BrowseFilters{Query: "Mixed", Letter: "M"}, tc.page, tc.total)
		if len(v.Links) != tc.links || (v.First != "") != tc.first || (v.Last != "") != tc.last {
			t.Errorf("%+v: %+v", tc, v)
		}
		for _, link := range v.Links {
			if !link.Gap && (!strings.Contains(link.URL, "letter=M") || !strings.Contains(link.URL, "q=Mixed")) {
				t.Error(link.URL)
			}
		}
	}
}
