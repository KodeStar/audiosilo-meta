package importer

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// libexFillServer stands in for the libex service. Records keyed by ASIN are
// served from /book/; /db/book/ 404s for any ASIN in mirrorMisses, which is the
// case the live fallback exists for.
func libexFillServer(t *testing.T, records map[string]string, mirrorMisses ...string) *httptest.Server {
	t.Helper()
	miss := map[string]bool{}
	for _, a := range mirrorMisses {
		miss[a] = true
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mirrored := strings.HasPrefix(r.URL.Path, "/db/book/")
		asin := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		body, ok := records[asin]
		if !ok || (mirrored && miss[asin]) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"Book not found in local database","status_code":404}`))
			return
		}
		_, _ = w.Write([]byte(body))
	}))
}

func fillClient(t *testing.T, srv *httptest.Server) *LibexClient {
	t.Helper()
	c := NewLibexClient()
	c.BaseURL = srv.URL
	c.Pause = 0
	return c
}

func TestSelectFillTargetsPicksOnlyTheGaps(t *testing.T) {
	cat := &model.Catalog{Works: []*model.Work{{
		ID: "book-one",
		Recordings: []*model.Recording{
			{ID: "complete", CoverURL: "https://x/c.jpg", Chapters: []model.Chapter{{Title: "One"}}, ASIN: []model.ASIN{{Region: "us", ASIN: "B1"}}},
			{ID: "no-cover", Chapters: []model.Chapter{{Title: "One"}}, ASIN: []model.ASIN{{Region: "us", ASIN: "B2"}}},
			{ID: "no-chapters", CoverURL: "https://x/c.jpg", ASIN: []model.ASIN{{Region: "us", ASIN: "B3"}}},
			{ID: "no-asin"},
		},
	}}}
	got := SelectFillTargets(cat)
	if len(got) != 2 {
		t.Fatalf("selected %d targets, want 2 (no-cover + no-chapters): %+v", len(got), got)
	}
	// Sorted by ASIN, so the order is deterministic.
	if got[0].ASIN != "B2" || !got[0].NeedCover {
		t.Errorf("first target = %+v, want B2 needing a cover", got[0])
	}
	if got[1].ASIN != "B3" || !got[1].NeedChapters {
		t.Errorf("second target = %+v, want B3 needing chapters", got[1])
	}
	// A recording with neither gap, and one with no ASIN to look up by, are both
	// left alone: the first needs nothing and the second cannot be identified.
	for _, g := range got {
		if g.ASIN == "B1" || g.Recording == "no-asin" {
			t.Errorf("selected a recording it should not have: %+v", g)
		}
	}
}

func TestSelectFillTargetsInRestrictsToTheGivenWorks(t *testing.T) {
	cat := &model.Catalog{Works: []*model.Work{
		{ID: "wanted", Recordings: []*model.Recording{
			{ID: "r", ASIN: []model.ASIN{{Region: "us", ASIN: "B1"}}}}},
		{ID: "other", Recordings: []*model.Recording{
			{ID: "r", ASIN: []model.ASIN{{Region: "us", ASIN: "B2"}}}}},
	}}
	got := SelectFillTargetsIn(cat, map[string]bool{"wanted": true})
	if len(got) != 1 || got[0].Work != "wanted" {
		t.Fatalf("restricted select = %+v, want only the wanted work", got)
	}
	if n := len(SelectFillTargetsIn(cat, nil)); n != 0 {
		t.Errorf("an empty work set selected %d targets, want 0", n)
	}
}

const liveRecord = `{"asin":"B0LIVE","title":"A Book","subtitle":"Sub","language":"english",
"bookFormat":"unabridged","lengthMinutes":300,"publisher":"Pub","region":"us",
"releaseDate":"2020-01-01T00:00:00+00:00","imageUrl":"https://m.media-amazon.com/images/I/x.jpg",
"authors":[{"name":"Author One","updatedAt":null}],"narrators":[{"name":"Narrator One","updatedAt":null}],
"series":[],"genres":[],"description":"dropped","rating":4.5,"isVvab":false}`

func TestFetchRowsProjectsToTheExportRowShape(t *testing.T) {
	srv := libexFillServer(t, map[string]string{"B0LIVE": liveRecord})
	defer srv.Close()

	var buf bytes.Buffer
	rep, err := fillClient(t, srv).FetchRows(context.Background(),
		[]FillTarget{{ASIN: "B0LIVE", Work: "w", Recording: "r", NeedCover: true}}, &buf)
	if err != nil {
		t.Fatalf("FetchRows: %v", err)
	}
	if rep.Fetched != 1 || rep.NotFound != 0 || rep.Failed != 0 {
		t.Fatalf("report = %+v, want 1 fetched", rep)
	}
	var row map[string]any
	if err := json.Unmarshal(buf.Bytes(), &row); err != nil {
		t.Fatalf("row is not JSON: %v", err)
	}
	// Exactly the export's keys: the row must be indistinguishable from a dump
	// row, because that is what lets it go through the ordinary parse layer.
	for _, k := range libexFillRowKeys {
		if _, ok := row[k]; !ok {
			t.Errorf("row is missing export key %q", k)
		}
	}
	for _, dropped := range []string{"description", "rating", "isVvab"} {
		if _, ok := row[dropped]; ok {
			t.Errorf("row carries %q, which the export shape does not have", dropped)
		}
	}
	// Credits keep only the name, as the export emits them.
	nar, _ := row["narrators"].([]any)
	if len(nar) != 1 {
		t.Fatalf("narrators = %v, want one", row["narrators"])
	}
	if m, _ := nar[0].(map[string]any); len(m) != 1 || m["name"] != "Narrator One" {
		t.Errorf("narrator entry = %v, want just a name", nar[0])
	}
}

func TestFetchRowsFallsBackToTheLiveEndpoint(t *testing.T) {
	// The mirror 404s, exactly as it did for the three ASINs our dump lacked.
	srv := libexFillServer(t, map[string]string{"B0LIVE": liveRecord}, "B0LIVE")
	defer srv.Close()

	var buf bytes.Buffer
	rep, err := fillClient(t, srv).FetchRows(context.Background(),
		[]FillTarget{{ASIN: "B0LIVE"}}, &buf)
	if err != nil {
		t.Fatalf("FetchRows: %v", err)
	}
	if rep.Fetched != 1 {
		t.Fatalf("report = %+v, want the live endpoint to have served it", rep)
	}
}

func TestFetchRowsCountsAMissWithoutFailing(t *testing.T) {
	srv := libexFillServer(t, map[string]string{})
	defer srv.Close()

	var buf bytes.Buffer
	rep, err := fillClient(t, srv).FetchRows(context.Background(),
		[]FillTarget{{ASIN: "B0GONE"}}, &buf)
	if err != nil {
		t.Fatalf("an ASIN libex does not have must not fail the run: %v", err)
	}
	if rep.NotFound != 1 || rep.Fetched != 0 || rep.Failed != 0 {
		t.Errorf("report = %+v, want one not-found", rep)
	}
	if buf.Len() != 0 {
		t.Errorf("a miss wrote a row: %q", buf.String())
	}
}

// TestUserLibraryOnlyBoundsTheAutomaticPass pins the bound the automatic pass
// runs under: a personal-library record is filled, a bulk-mirror seed is not.
// Without it a fill run would be ~136,000 lookups against a free public service
// to fix the ~130 mirror records that actually lack a cover.
func TestUserLibraryOnlyBoundsTheAutomaticPass(t *testing.T) {
	cat := &model.Catalog{Works: []*model.Work{
		{ID: "mine", Recordings: []*model.Recording{{
			ID: "r", ASIN: []model.ASIN{{Region: "us", ASIN: "B1"}},
			Sources: []model.Source{{Type: model.SourceAudiosiloBooksImport}},
		}}},
		{ID: "mirror", Recordings: []*model.Recording{{
			ID: "r", ASIN: []model.ASIN{{Region: "us", ASIN: "B2"}},
			Sources: []model.Source{{Type: model.SourceLibexImport}},
		}}},
	}}
	got := UserLibraryOnly(SelectFillTargets(cat), cat)
	if len(got) != 1 || got[0].Work != "mine" {
		t.Fatalf("bounded select = %+v, want only the user-library record", got)
	}
	// Unbounded, both are candidates - the bound is a policy choice, not a
	// limitation of the selector.
	if n := len(SelectFillTargets(cat)); n != 2 {
		t.Errorf("unbounded select = %d, want both", n)
	}
}
