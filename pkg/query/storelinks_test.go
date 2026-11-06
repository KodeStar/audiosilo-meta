package query

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/artifacttest"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

const (
	storeGuildURL   = "https://audiobookguild.com/products/without-law-17"
	storeGraphicURL = "https://www.graphicaudio.net/without-law-17.html"
)

// storeCatalog is one work with a recording carrying both store pages and an
// ASIN, so the purchase links' order (stated pages, then derived routes) shows.
func storeCatalog() *model.Catalog {
	return &model.Catalog{
		People: []*model.Person{
			{ID: "eric-vall", Name: "Eric Vall", License: "CC0-1.0", Sources: []model.Source{{Type: model.SourceStoreListing}}},
			{ID: "joshua-story", Name: "Joshua Story", License: "CC0-1.0", Sources: []model.Source{{Type: model.SourceStoreListing}}},
		},
		Works: []*model.Work{{
			ID: "without-law-17", Title: "Without Law 17", Authors: []string{"eric-vall"}, Language: "en", License: "CC0-1.0",
			Sources: []model.Source{{Type: model.SourceStoreListing, Ref: storeGuildURL}},
			Recordings: []*model.Recording{{
				ID: "joshua-story-2024", Work: "without-law-17", Narrators: []string{"joshua-story"}, Language: "en", License: "CC0-1.0",
				ASIN:    []model.ASIN{{Region: "us", ASIN: "B0STORE001"}},
				Links:   &model.RecordingLinks{AudiobookGuild: storeGuildURL, GraphicAudio: storeGraphicURL},
				Sources: []model.Source{{Type: model.SourceStoreListing, Ref: storeGuildURL}},
			}},
		}},
	}
}

func storeRecordingOf(t *testing.T, base string) map[string]any {
	t.Helper()
	code, work := artifacttest.GetJSON(t, base, "/api/v1/works/without-law-17")
	if code != http.StatusOK {
		t.Fatalf("GET work = %d %v", code, work)
	}
	return work["recordings"].([]any)[0].(map[string]any)
}

// TestStoreLinksOnWorkDetail: a recording's store pages are its `links` and a
// purchase link each, with unknown availability, ahead of the derived routes.
func TestStoreLinksOnWorkDetail(t *testing.T) {
	_, ts := serveDB(t, artifacttest.Build(t, storeCatalog()), HandlerOptions{})
	rec := storeRecordingOf(t, ts.URL)
	wantLinks := map[string]any{"audiobookguild": storeGuildURL, "graphicaudio": storeGraphicURL}
	if !reflect.DeepEqual(rec["links"], wantLinks) {
		t.Errorf("links = %v, want %v", rec["links"], wantLinks)
	}
	var got []string
	for _, l := range rec["purchase_links"].([]any) {
		m := l.(map[string]any)
		if m["availability"] != "unknown" || m["free"] != nil {
			t.Errorf("purchase link %v: want availability unknown and no free flag", m)
		}
		got = append(got, m["retailer"].(string)+" "+m["id"].(string)+" "+m["url"].(string))
	}
	want := []string{
		"audiobookguild " + storeGuildURL + " " + storeGuildURL,
		"graphicaudio " + storeGraphicURL + " " + storeGraphicURL,
		"audible B0STORE001 https://www.audible.com/pd/B0STORE001",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("purchase_links = %v, want %v", got, want)
	}
}

// TestStoreLinksOnAnOlderArtifact: a newer binary serving a version 10 release
// (no store columns) answers the work with no store link rather than a 500.
func TestStoreLinksOnAnOlderArtifact(t *testing.T) {
	path := artifacttest.Altered(t, storeCatalog(), 10,
		"ALTER TABLE recordings DROP COLUMN link_audiobookguild", "ALTER TABLE recordings DROP COLUMN link_graphicaudio")
	rec := storeRecordingOf(t, downgradedServer(t, path).URL)
	if rec["links"] != nil {
		t.Errorf("links on a v10 artifact = %v, want none", rec["links"])
	}
	pl := rec["purchase_links"].([]any)
	if len(pl) != 1 || pl[0].(map[string]any)["retailer"] != "audible" {
		t.Errorf("purchase_links on a v10 artifact = %v, want the Audible link alone", pl)
	}
}

// TestOpenRefusesAFalseStoreLinksClaim: a version 11 claim without the columns
// is a corrupt file, refused at open naming the claim.
func TestOpenRefusesAFalseStoreLinksClaim(t *testing.T) {
	path := artifacttest.Altered(t, storeCatalog(), 11, "ALTER TABLE recordings DROP COLUMN link_graphicaudio")
	db, err := Open(path, "")
	if err == nil {
		_ = db.Close()
		t.Fatal("Open accepted a version 11 artifact without the store link columns")
	}
	if !strings.Contains(err.Error(), "store link columns") {
		t.Errorf("error = %v, want it to name the store links claim", err)
	}
}

func TestStorePurchaseLinks(t *testing.T) {
	if got := storePurchaseLinks(nil); got != nil {
		t.Errorf("no links object = %v, want none", got)
	}
	if got := storePurchaseLinks(&model.RecordingLinks{Librivox: "https://librivox.org/x/"}); got != nil {
		t.Errorf("a LibriVox page alone = %v, want no store link", got)
	}
	got := storePurchaseLinks(&model.RecordingLinks{GraphicAudio: storeGraphicURL})
	want := []PurchaseLink{{Retailer: "graphicaudio", ID: storeGraphicURL, URL: storeGraphicURL, Availability: "unknown"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("storePurchaseLinks = %v, want %v", got, want)
	}
}
