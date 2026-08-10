package importer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// libexFill stands in for the libex service and its two families of endpoint:
// the book RECORD at /db/book/<asin> and /book/<asin>, and the dedicated
// CHAPTERS payload at those same two paths with /chapters appended. Bodies are
// keyed by ASIN; anything not held answers with libex's own 404 error body.
//
// missRecord / missChapters name the ASINs whose MIRROR (/db/...) half 404s -
// per resource, because the mirror can hold the record and not the chapters -
// which is the case the live fallback exists for. Every served path is recorded,
// so a test can assert on what was NOT requested.
type libexFill struct {
	records      map[string]string
	chapters     map[string]string
	missRecord   []string
	missChapters []string
	// chaptersStatus, when set, is what EVERY chapters request answers with: the
	// endpoint being broken rather than empty.
	chaptersStatus int

	mu   sync.Mutex
	seen []string
}

func libexFillServer(t *testing.T, f *libexFill) *httptest.Server {
	t.Helper()
	missRec, missCh := map[string]bool{}, map[string]bool{}
	for _, a := range f.missRecord {
		missRec[a] = true
	}
	for _, a := range f.missChapters {
		missCh[a] = true
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.seen = append(f.seen, r.URL.Path)
		f.mu.Unlock()

		path := strings.TrimPrefix(r.URL.Path, "/db")
		mirrored := path != r.URL.Path
		bodies, miss := f.records, missRec
		if strings.HasSuffix(path, "/chapters") {
			path = strings.TrimSuffix(path, "/chapters")
			bodies, miss = f.chapters, missCh
			if f.chaptersStatus != 0 {
				w.WriteHeader(f.chaptersStatus)
				return
			}
		}
		asin := strings.TrimPrefix(path, "/book/")
		body, ok := bodies[asin]
		if !ok || (mirrored && miss[asin]) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"Book not found in local database","status_code":404}`))
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// paths returns the paths the service was asked for, in order.
func (f *libexFill) paths() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.seen...)
}

// saw reports whether the service was asked for a path.
func (f *libexFill) saw(path string) bool {
	for _, p := range f.paths() {
		if p == path {
			return true
		}
	}
	return false
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
	srv := libexFillServer(t, &libexFill{records: map[string]string{"B0LIVE": liveRecord}})

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
	srv := libexFillServer(t, &libexFill{
		records:    map[string]string{"B0LIVE": liveRecord},
		missRecord: []string{"B0LIVE"},
	})

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
	srv := libexFillServer(t, &libexFill{})

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

// chapterPayload is what the dedicated endpoints answer with: a chapter list,
// the production's own runtime, and libex's confidence in the offsets. runtime
// is in MINUTES here for legibility; the payload states milliseconds.
func chapterPayload(runtime float64, accurate bool) string {
	return fmt.Sprintf(`{"asin":"B0LIVE","isAccurate":%t,"runtimeLengthMs":%d,"chapters":[
{"title":"Opening Credits","lengthMs":21000,"startOffsetMs":0,"startOffsetSec":0},
{"title":"Chapter One","lengthMs":600000,"startOffsetMs":21000,"startOffsetSec":21}]}`,
		accurate, int64(runtime*60000))
}

// fetchOneRow runs a single target and returns the row it wrote, which must be
// exactly one.
func fetchOneRow(t *testing.T, srv *httptest.Server, target FillTarget) (map[string]any, FillReport) {
	t.Helper()
	var buf bytes.Buffer
	rep, err := fillClient(t, srv).FetchRows(context.Background(), []FillTarget{target}, &buf)
	if err != nil {
		t.Fatalf("FetchRows: %v", err)
	}
	if rep.Fetched != 1 {
		t.Fatalf("report = %+v, want one fetched row", rep)
	}
	var row map[string]any
	if err := json.Unmarshal(buf.Bytes(), &row); err != nil {
		t.Fatalf("row is not JSON: %v", err)
	}
	return row, rep
}

// rowChapterTitles reads back the chapter titles a row carries.
func rowChapterTitles(t *testing.T, row map[string]any) []string {
	t.Helper()
	list, _ := row["chapters"].([]any)
	var out []string
	for _, e := range list {
		m, ok := e.(map[string]any)
		if !ok {
			t.Fatalf("chapter entry is not an object: %v", e)
		}
		title, _ := m["title"].(string)
		out = append(out, title)
	}
	return out
}

// TestFetchRowsFetchesTheChaptersEndpoint is the defect this endpoint pair
// exists for: the book RECORD carries no chapter list, so a fill run that only
// fetched the record filled 19 of 13,387 chapter gaps. A target that needs
// chapters asks the dedicated endpoint for them.
func TestFetchRowsFetchesTheChaptersEndpoint(t *testing.T) {
	f := &libexFill{
		records:  map[string]string{"B0LIVE": liveRecord},
		chapters: map[string]string{"B0LIVE": chapterPayload(300, true)},
	}
	srv := libexFillServer(t, f)

	row, rep := fetchOneRow(t, srv, FillTarget{ASIN: "B0LIVE", NeedChapters: true})
	if rep.ChaptersFetched != 1 || rep.ChaptersRejected != 0 {
		t.Errorf("report = %+v, want one chapter list attached", rep)
	}
	if got := rowChapterTitles(t, row); len(got) != 2 || got[1] != "Chapter One" {
		t.Errorf("row chapters = %v, want the payload's two", got)
	}
	// The mirror is asked first, exactly as it is for the record.
	if !f.saw("/db/book/B0LIVE/chapters") {
		t.Errorf("never asked the mirror for chapters; saw %v", f.paths())
	}
	// The entries keep the payload's own keys - the shape the parse layer reads
	// off a dump row - so nothing re-boxes them on the way through.
	list, _ := row["chapters"].([]any)
	first, _ := list[0].(map[string]any)
	for _, k := range []string{"title", "lengthMs", "startOffsetMs", "startOffsetSec"} {
		if _, ok := first[k]; !ok {
			t.Errorf("chapter entry is missing %q: %v", k, first)
		}
	}
}

func TestFetchRowsFallsBackToTheLiveChaptersEndpoint(t *testing.T) {
	f := &libexFill{
		records:      map[string]string{"B0LIVE": liveRecord},
		chapters:     map[string]string{"B0LIVE": chapterPayload(300, true)},
		missChapters: []string{"B0LIVE"},
	}
	srv := libexFillServer(t, f)

	row, rep := fetchOneRow(t, srv, FillTarget{ASIN: "B0LIVE", NeedChapters: true})
	if rep.ChaptersFetched != 1 {
		t.Fatalf("report = %+v, want the live chapters endpoint to have served it", rep)
	}
	if got := rowChapterTitles(t, row); len(got) != 2 {
		t.Errorf("row chapters = %v, want the payload's two", got)
	}
	if !f.saw("/db/book/B0LIVE/chapters") || !f.saw("/book/B0LIVE/chapters") {
		t.Errorf("wrong fallback order; saw %v", f.paths())
	}
}

// TestFetchRowsRefusesInaccurateChapters: libex states its own confidence, and
// an explicit false means the offsets do not describe this production.
func TestFetchRowsRefusesInaccurateChapters(t *testing.T) {
	srv := libexFillServer(t, &libexFill{
		records:  map[string]string{"B0LIVE": liveRecord},
		chapters: map[string]string{"B0LIVE": chapterPayload(300, false)},
	})

	row, rep := fetchOneRow(t, srv, FillTarget{ASIN: "B0LIVE", NeedChapters: true})
	if rep.ChaptersFetched != 0 || rep.ChaptersRejected != 1 {
		t.Errorf("report = %+v, want the payload rejected", rep)
	}
	if got := rowChapterTitles(t, row); len(got) != 0 {
		t.Errorf("row carries chapters libex says are inaccurate: %v", got)
	}
}

// TestChaptersRuntimeToleranceHasAnAbsoluteFloor pins BOTH halves of the runtime
// guard: a payload describing a different edition is refused, and a short book
// is not. The catalogue stores floored integer minutes, so on a 5-minute book
// 10% is smaller than the rounding itself - a relative-only guard falsely
// refused about 1,300 short children's books.
func TestChaptersRuntimeToleranceHasAnAbsoluteFloor(t *testing.T) {
	shortRecord := `{"asin":"B0LIVE","title":"A Short Book","language":"english",
"lengthMinutes":5,"region":"us","authors":[{"name":"Author One"}],"narrators":[{"name":"Narrator One"}]}`

	tests := []struct {
		name    string
		record  string
		runtime float64
		attach  bool
	}{
		// 300 recorded against 200 stated: a different edition.
		{"a different edition is refused", liveRecord, 200, false},
		// Within 10% of 300 minutes.
		{"within the relative tolerance", liveRecord, 310, true},
		// 0.9 minutes over a 5-minute record: past 10% (0.5) but inside the floor.
		{"inside the absolute floor", shortRecord, 5.9, true},
		// 2 minutes over the same record: past the floor too.
		{"past the absolute floor", shortRecord, 7, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := libexFillServer(t, &libexFill{
				records:  map[string]string{"B0LIVE": tc.record},
				chapters: map[string]string{"B0LIVE": chapterPayload(tc.runtime, true)},
			})
			row, rep := fetchOneRow(t, srv, FillTarget{ASIN: "B0LIVE", NeedChapters: true})
			attached := len(rowChapterTitles(t, row)) > 0
			if attached != tc.attach {
				t.Fatalf("attached = %t, want %t (report %+v)", attached, tc.attach, rep)
			}
			if tc.attach && rep.ChaptersFetched != 1 {
				t.Errorf("report = %+v, want one attached", rep)
			}
			if !tc.attach && rep.ChaptersRejected != 1 {
				t.Errorf("report = %+v, want one rejected", rep)
			}
		})
	}
}

// TestFetchRowsSkipsChaptersForACoverOnlyTarget: the chapters endpoint is an
// extra request against a free public service, so it is asked for only when the
// recording actually lacks chapters.
func TestFetchRowsSkipsChaptersForACoverOnlyTarget(t *testing.T) {
	f := &libexFill{
		records:  map[string]string{"B0LIVE": liveRecord},
		chapters: map[string]string{"B0LIVE": chapterPayload(300, true)},
	}
	srv := libexFillServer(t, f)

	_, rep := fetchOneRow(t, srv, FillTarget{ASIN: "B0LIVE", NeedCover: true})
	if rep.ChaptersFetched != 0 || rep.ChaptersRejected != 0 {
		t.Errorf("report = %+v, want no chapter work at all", rep)
	}
	for _, p := range f.paths() {
		if strings.HasSuffix(p, "/chapters") {
			t.Errorf("asked for chapters a target did not need: %v", f.paths())
			break
		}
	}
}

// TestFetchRowsSurvivesAChaptersFailure: chapters are best-effort WITHIN an
// otherwise fine row - the cover and every other fact still fill.
func TestFetchRowsSurvivesAChaptersFailure(t *testing.T) {
	srv := libexFillServer(t, &libexFill{
		records:        map[string]string{"B0LIVE": liveRecord},
		chapters:       map[string]string{"B0LIVE": chapterPayload(300, true)},
		chaptersStatus: http.StatusInternalServerError,
	})

	row, rep := fetchOneRow(t, srv, FillTarget{ASIN: "B0LIVE", NeedCover: true, NeedChapters: true})
	if rep.Failed != 0 {
		t.Errorf("report = %+v, want the row itself to have succeeded", rep)
	}
	if rep.ChaptersFetched != 0 || rep.ChaptersRejected != 0 {
		t.Errorf("report = %+v, want nothing attached and nothing judged", rep)
	}
	if len(rep.Errors) != 1 || !strings.Contains(rep.Errors[0], "chapters") {
		t.Errorf("errors = %v, want one naming the chapters fetch", rep.Errors)
	}
	if row["imageUrl"] == nil || row["imageUrl"] == "" {
		t.Errorf("the row lost its cover to a chapters failure: %v", row["imageUrl"])
	}
	if got := rowChapterTitles(t, row); len(got) != 0 {
		t.Errorf("row carries chapters it never fetched: %v", got)
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
