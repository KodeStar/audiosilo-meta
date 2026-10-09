package serve

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestPrimaryOrderingWinsThePageSeries is TestPrimaryOrderingWinsTheSeriesChoice
// (pkg/query) on the work PAGE: its JSON-LD isPartOf names the primary ordering,
// not the variant whose id sorts first.
func TestPrimaryOrderingWinsThePageSeries(t *testing.T) {
	ts := newPageServer(t, languagesCatalog(), markedShells)
	code, page := getPage(t, ts.URL, "/works/book-one")
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	var graph struct {
		Graph []map[string]any `json:"@graph"`
	}
	if err := json.Unmarshal([]byte(between(t, page, `<script type="application/ld+json">`, "</script>")), &graph); err != nil {
		t.Fatal(err)
	}
	part, _ := graph.Graph[0]["isPartOf"].(map[string]any)
	if part["url"] != testSiteURL+"/series/the-saga" {
		t.Errorf("JSON-LD isPartOf = %v, want the primary", part)
	}
}

// TestLanguagePagesGolden renders the pages the languages layer changes over the
// franchise catalogue: an original's work page (workTranslation, and isPartOf on
// the primary), a translated omnibus (translationOfWork naming two originals)
// and a primary series (inLanguage and workTranslation). Regenerate with
// -update-golden, as TestEntityPagesGolden.
func TestLanguagePagesGolden(t *testing.T) {
	ts := newPageServer(t, languagesCatalog(), markedShells)
	for _, tc := range []struct{ name, path string }{
		{"work-translations", "/works/book-one"},
		{"work-translation-of", "/works/sammelband"},
		{"series-orderings", "/series/the-saga"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, page := getPage(t, ts.URL, tc.path)
			if code != http.StatusOK {
				t.Fatalf("status = %d", code)
			}
			assertGolden(t, tc.name+".html", []byte(page))
		})
	}
}
