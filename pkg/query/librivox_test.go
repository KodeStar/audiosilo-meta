package query

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/artifacttest"
	"github.com/kodestar/audiosilo-meta/internal/importer"
	"github.com/kodestar/audiosilo-meta/pkg/check"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// The LibriVox layer end to end: the importer's fixture snapshot (four REAL
// LibriVox responses, internal/importer/testdata/librivox) imported into an empty
// tree, validated, built by the real builder and served - so every assertion
// below is about what a LibriVox import actually lands in the artifact, not a
// hand-written imitation of it.

const (
	lvFixtureDir   = "../../internal/importer/testdata/librivox/snapshot"
	lvImportDate   = "2026-10-27"
	lvControlWork  = "the-trial"
	lvControlRec   = "someone-else"
	lvControlASIN  = "B0TRIAL001"
	lvKafkaWork    = "metamorphosis"
	lvKafkaRec     = "david-barnes"
	lvSince2026    = "1796083200" // 2026-12-01T00:00:00Z: after the import day
	lvSinceImport  = "1761523200" // 2025-10-27T00:00:00Z: before it
	lvSinceSameDay = "1793091600" // 2026-10-27T09:00:00Z: later the same UTC day
)

// librivoxCatalog imports the fixture and adds a NON-LibriVox control: "The
// Trial", by the same Franz Kafka the import created, read by someone else from
// a user's library, with an ASIN - the record every source filter must exclude.
func librivoxCatalog(t *testing.T) *model.Catalog {
	t.Helper()
	dataDir := t.TempDir()
	ids, err := importer.ParseLibrivoxIDs("1-")
	if err != nil {
		t.Fatal(err)
	}
	if sum, err := importer.RunLibrivox(lvFixtureDir, ids, importer.Options{DataDir: dataDir, ImportDate: lvImportDate}); err != nil {
		t.Fatalf("librivox import: %v (%v)", err, sum.Warnings)
	}
	res := check.Load(dataDir)
	if !res.OK() {
		t.Fatalf("imported tree failed validation:\n%v", res.Problems)
	}
	cat := res.Catalog
	cat.People = append(cat.People, &model.Person{ID: "someone-else", Name: "Someone Else", License: "CC0-1.0", Sources: []model.Source{{Type: "user"}}})
	cat.Works = append(cat.Works, &model.Work{
		ID: lvControlWork, Title: "The Trial", Authors: []string{"franz-kafka"}, Language: "en", License: "CC0-1.0",
		AddedAt: lvImportDate, Sources: []model.Source{{Type: "user"}},
		Recordings: []*model.Recording{{
			ID: lvControlRec, Work: lvControlWork, Narrators: []string{"someone-else"}, Language: "en", License: "CC0-1.0",
			ASIN: []model.ASIN{{Region: "us", ASIN: lvControlASIN}}, AddedAt: lvImportDate,
			Chapters: []model.Chapter{{Title: "Arrest", StartMS: 0, LengthMS: 60000}},
			Sources:  []model.Source{{Type: "user"}},
		}},
	})
	return cat
}

// librivoxServer serves the fixture catalogue's artifact.
func librivoxServer(t *testing.T) *httptest.Server {
	t.Helper()
	_, ts := serveDB(t, artifacttest.Build(t, librivoxCatalog(t)), HandlerOptions{})
	return ts
}

// workIDsOf collects the work ids a search or latest page names, in order.
func workIDsOf(t *testing.T, body map[string]any, key string) []string {
	t.Helper()
	var out []string
	items, _ := body[key].([]any)
	for _, it := range items {
		m := it.(map[string]any)
		if k, ok := m["kind"]; ok && k != "work" {
			out = append(out, k.(string)+":"+m["id"].(string))
			continue
		}
		out = append(out, m["id"].(string))
	}
	return out
}

func TestSourceFilter(t *testing.T) {
	ts := librivoxServer(t)
	for _, tc := range []struct {
		path      string
		key       string
		want      []string // the work ids, sorted
		unfilterd string   // the same request without the filter must hold the control
	}{
		{"/api/v1/search?q=kafka&source=librivox", "results", []string{lvKafkaWork}, "/api/v1/search?q=kafka"},
		{"/api/v1/works/search?q=kafka&source=librivox", "results", []string{lvKafkaWork}, "/api/v1/works/search?q=kafka"},
		{"/api/v1/works/latest?source=librivox&limit=50", "works",
			[]string{"candide", "candide-ou-loptimisme", lvKafkaWork, "short-story-collection-vol-001"}, "/api/v1/works/latest?limit=50"},
		// The exact-title boost resolves "The Trial" outright; the filter still
		// drops it, because it has no LibriVox recording.
		{"/api/v1/works/search?q=the+trial&source=librivox", "results", nil, "/api/v1/works/search?q=the+trial"},
		{"/api/v1/works/search?q=kafka&source=user", "results", []string{lvControlWork}, ""},
		// A known type no recording carries filters to nothing (not a 400).
		{"/api/v1/works/search?q=kafka&source=wikidata", "results", nil, ""},
	} {
		t.Run(tc.path, func(t *testing.T) {
			code, body := artifacttest.GetJSON(t, ts.URL, tc.path)
			if code != http.StatusOK {
				t.Fatalf("status %d: %v", code, body)
			}
			got := workIDsOf(t, body, tc.key)
			sort.Strings(got)
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("works = %v, want %v", got, tc.want)
			}
			if tc.unfilterd == "" {
				return
			}
			_, plain := artifacttest.GetJSON(t, ts.URL, tc.unfilterd)
			if ids := workIDsOf(t, plain, tc.key); !containsString(ids, lvControlWork) {
				t.Errorf("control: %s does not hold %s (%v), so the filter proved nothing", tc.unfilterd, lvControlWork, ids)
			}
		})
	}

	// The combined search, filtered, is works only: Franz Kafka the person
	// matches "kafka" unfiltered.
	_, plain := artifacttest.GetJSON(t, ts.URL, "/api/v1/search?q=kafka")
	if !containsString(workIDsOf(t, plain, "results"), "person:franz-kafka") {
		t.Errorf("control: the unfiltered search holds no person: %v", workIDsOf(t, plain, "results"))
	}

	for _, path := range []string{
		"/api/v1/search?q=kafka&source=LibriVox",
		"/api/v1/works/search?q=kafka&source=librivox-import",
		"/api/v1/works/latest?source=audible",
	} {
		if code, body := artifacttest.GetJSON(t, ts.URL, path); code != http.StatusBadRequest || !strings.Contains(body["error"].(string), "source") {
			t.Errorf("GET %s = %d %v, want a 400 naming source", path, code, body)
		}
	}
	// The people and series scopes take no source; they leave it unread.
	if code, _ := artifacttest.GetJSON(t, ts.URL, "/api/v1/people/search?q=kafka&source=nonsense"); code != http.StatusOK {
		t.Errorf("people/search with a source = %d, want 200 (unread)", code)
	}
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func TestLookupLibrivoxID(t *testing.T) {
	ts := librivoxServer(t)
	code, body := artifacttest.GetJSON(t, ts.URL, "/api/v1/lookup?librivox_id=527")
	if code != http.StatusOK || body["recording_id"] != lvKafkaRec || body["work"].(map[string]any)["id"] != lvKafkaWork {
		t.Errorf("lookup 527 = %d %v", code, body)
	}
	if code, _ := artifacttest.GetJSON(t, ts.URL, "/api/v1/lookup?librivox_id=99999"); code != http.StatusNotFound {
		t.Errorf("lookup of an unknown project = %d, want 404", code)
	}
	for _, bad := range []string{"abc", "0", "-5", "5.5"} {
		if code, _ := artifacttest.GetJSON(t, ts.URL, "/api/v1/lookup?librivox_id="+bad); code != http.StatusBadRequest {
			t.Errorf("lookup?librivox_id=%s = %d, want 400", bad, code)
		}
	}
	// An ASIN beside it keeps the lookup it always was.
	if code, body := artifacttest.GetJSON(t, ts.URL, "/api/v1/lookup?asin="+lvControlASIN+"&librivox_id=527"); code != http.StatusOK || body["recording_id"] != lvControlRec {
		t.Errorf("asin + librivox_id = %d %v, want the ASIN's recording", code, body)
	}
	if code, body := artifacttest.GetJSON(t, ts.URL, "/api/v1/lookup"); code != http.StatusBadRequest || !strings.Contains(body["error"].(string), "librivox_id") {
		t.Errorf("bare lookup = %d %v", code, body)
	}
}

func TestRecordingDetailCarriesTheLibrivoxLayer(t *testing.T) {
	ts := librivoxServer(t)
	_, work := artifacttest.GetJSON(t, ts.URL, "/api/v1/works/"+lvKafkaWork)
	recs := work["recordings"].([]any)
	if len(recs) != 1 {
		t.Fatalf("recordings = %v", recs)
	}
	rec := recs[0].(map[string]any)
	if rec["librivox_id"] != float64(527) || rec["archive_id"] != "metamorphosis_librivox" || rec["copyright_year"] != float64(1915) {
		t.Errorf("recording = %v", rec)
	}
	links := rec["links"].(map[string]any)
	want := map[string]any{
		"librivox": "https://librivox.org/the-metamorphosis-by-franz-kafka/",
		"zip":      "https://archive.org/compress/metamorphosis_librivox/formats=64KBPS MP3&file=/metamorphosis_librivox.zip",
		"rss":      "https://librivox.org/rss/527",
		"text":     "https://www.kafka.org/index.php?id=162,170,0,0,1,0",
	}
	for k, v := range want {
		if links[k] != v {
			t.Errorf("links.%s = %v, want %v", k, links[k], v)
		}
	}
	pl := rec["purchase_links"].([]any)
	first := pl[0].(map[string]any)
	if len(pl) != 1 || first["retailer"] != "librivox" || first["id"] != "527" || first["free"] != true ||
		first["url"] != want["librivox"] || first["availability"] != "unknown" {
		t.Errorf("purchase_links = %v, want the one free LibriVox link", pl)
	}

	// The control states none of it, and its Audible link carries no "free".
	_, trial := artifacttest.GetJSON(t, ts.URL, "/api/v1/works/"+lvControlWork)
	ctl := trial["recordings"].([]any)[0].(map[string]any)
	for _, k := range []string{"librivox_id", "archive_id", "links", "copyright_year"} {
		if _, ok := ctl[k]; ok {
			t.Errorf("control recording carries %s: %v", k, ctl)
		}
	}
	for _, l := range ctl["purchase_links"].([]any) {
		if lm := l.(map[string]any); lm["retailer"] == "librivox" || lm["free"] != nil {
			t.Errorf("control purchase link = %v", lm)
		}
	}

	// Chapters carry each section's file; the control's carry none.
	_, chs := artifacttest.GetJSON(t, ts.URL, "/api/v1/works/"+lvKafkaWork+"/recordings/"+lvKafkaRec+"/chapters")
	ch := chs["chapters"].([]any)
	if len(ch) != 3 || ch[1].(map[string]any)["audio_url"] != "https://www.archive.org/download/metamorphosis_librivox/metamorphosis_2_kafka_64kb.mp3" {
		t.Errorf("chapters = %v", ch)
	}
	_, chs = artifacttest.GetJSON(t, ts.URL, "/api/v1/works/"+lvControlWork+"/recordings/"+lvControlRec+"/chapters")
	if _, ok := chs["chapters"].([]any)[0].(map[string]any)["audio_url"]; ok {
		t.Errorf("control chapter carries an audio_url: %v", chs)
	}
}

// TestLibrivoxLayerOnAnOlderArtifact: a newer binary serving a version 7 release
// (no LibriVox columns or tables) degrades to "no data" - every work page still
// answers, a filtered list is empty rather than unfiltered, the lookup and the
// feed find nothing.
func TestLibrivoxLayerOnAnOlderArtifact(t *testing.T) {
	path := artifacttest.Altered(t, librivoxCatalog(t), 7,
		"DROP TABLE recording_sources", "DROP TABLE work_credits",
		"DROP INDEX idx_recordings_librivox",
		"ALTER TABLE recordings DROP COLUMN librivox_id", "ALTER TABLE recordings DROP COLUMN archive_id",
		"ALTER TABLE recordings DROP COLUMN link_librivox", "ALTER TABLE recordings DROP COLUMN link_zip",
		"ALTER TABLE recordings DROP COLUMN link_rss", "ALTER TABLE recordings DROP COLUMN link_text",
		"ALTER TABLE recordings DROP COLUMN copyright_year", "ALTER TABLE recordings DROP COLUMN added_at",
		"ALTER TABLE chapters DROP COLUMN audio_url",
		"ALTER TABLE people DROP COLUMN librivox_reader_id", "ALTER TABLE people DROP COLUMN librivox_author_id",
		"ALTER TABLE people DROP COLUMN birth_year", "ALTER TABLE people DROP COLUMN death_year",
	)
	ts := downgradedServer(t, path)
	code, work := artifacttest.GetJSON(t, ts.URL, "/api/v1/works/"+lvKafkaWork)
	if code != http.StatusOK {
		t.Fatalf("work page on a v7 artifact = %d", code)
	}
	if rec := work["recordings"].([]any)[0].(map[string]any); rec["librivox_id"] != nil || rec["purchase_links"] != nil {
		t.Errorf("v7 recording = %v", rec)
	}
	if code, _ := artifacttest.GetJSON(t, ts.URL, "/api/v1/works/"+lvKafkaWork+"/recordings/"+lvKafkaRec+"/chapters"); code != http.StatusOK {
		t.Errorf("chapters on a v7 artifact = %d", code)
	}
	for _, path := range []string{"/api/v1/search?q=kafka&source=librivox", "/api/v1/works/latest?source=librivox"} {
		code, body := artifacttest.GetJSON(t, ts.URL, path)
		key := "results"
		if strings.Contains(path, "latest") {
			key = "works"
		}
		if code != http.StatusOK || len(body[key].([]any)) != 0 {
			t.Errorf("GET %s on a v7 artifact = %d %v, want an empty list", path, code, body)
		}
	}
	if code, _ := artifacttest.GetJSON(t, ts.URL, "/api/v1/lookup?librivox_id=527"); code != http.StatusNotFound {
		t.Errorf("lookup on a v7 artifact = %d, want 404", code)
	}
	if code, body := artifacttest.GetJSON(t, ts.URL, LibrivoxFeedPath+"?id=527"); code != http.StatusNotFound || body["error"] != librivoxNotFound {
		t.Errorf("feed on a v7 artifact = %d %v", code, body)
	}
}

// TestOpenRefusesAFalseLibrivoxClaim: a version 8 claim without the layer is a
// corrupt file, refused at open naming the claim.
func TestOpenRefusesAFalseLibrivoxClaim(t *testing.T) {
	path := artifacttest.Altered(t, librivoxCatalog(t), 8, "DROP TABLE recording_sources")
	db, err := Open(path, "")
	if err == nil {
		_ = db.Close()
		t.Fatal("Open accepted a version 8 artifact without recording_sources")
	}
	if !strings.Contains(err.Error(), "LibriVox layer") {
		t.Errorf("error = %v, want it to name the LibriVox claim", err)
	}
}

// ---- the LibriVox-shaped feed ------------------------------------------------

// lvFixtureBooks reads the fixture's real LibriVox answers, keyed by project id.
func lvFixtureBooks(t *testing.T) map[string]map[string]any {
	t.Helper()
	dir := filepath.Join(lvFixtureDir, "pages")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]map[string]any{}
	for _, e := range entries {
		body, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		var page struct {
			Books []map[string]any `json:"books"`
		}
		if err := json.Unmarshal(body, &page); err != nil {
			t.Fatal(err)
		}
		for _, b := range page.Books {
			out[b["id"].(string)] = b
		}
	}
	return out
}

// feedBooks fetches a feed URL and returns its books (failing on a non-200).
func feedBooks(t *testing.T, base, path string) []map[string]any {
	t.Helper()
	code, body := artifacttest.GetJSON(t, base, path)
	if code != http.StatusOK {
		t.Fatalf("GET %s = %d %v", path, code, body)
	}
	var out []map[string]any
	for _, b := range body["books"].([]any) {
		out = append(out, b.(map[string]any))
	}
	return out
}

func idsOfBooks(books []map[string]any) []string {
	out := make([]string, 0, len(books))
	for _, b := range books {
		out = append(out, b["id"].(string))
	}
	return out
}

// TestLibrivoxFeedGolden compares the feed, field by field, with LibriVox's own
// answer for the same project (the fixture's real responses), for every field
// the feed claims to mirror. The fields it does not mirror are asserted to be
// the documented placeholder instead, so a change to either is a decision.
func TestLibrivoxFeedGolden(t *testing.T) {
	ts := librivoxServer(t)
	want := lvFixtureBooks(t)
	for _, id := range []string{"527", "296", "801", "1870"} {
		t.Run(id, func(t *testing.T) {
			books := feedBooks(t, ts.URL, LibrivoxFeedPath+"?id="+id+"&extended=1&coverart=1&format=json")
			if len(books) != 1 {
				t.Fatalf("books = %d", len(books))
			}
			got, lv := books[0], want[id]
			same := func(field string) {
				t.Helper()
				if !jsonEqual(got[field], lv[field]) {
					t.Errorf("%s = %#v, LibriVox says %#v", field, got[field], lv[field])
				}
			}
			for _, f := range []string{"id", "url_text_source", "language", "copyright_year", "num_sections",
				"url_rss", "url_zip_file", "url_librivox", "url_other", "url_iarchive", "coverart_jpg"} {
				same(f)
			}
			// The title: LibriVox's own, except the "(version N)" project marker,
			// which the mirror does not store.
			if wantTitle := strings.TrimSuffix(lv["title"].(string), " (version 2)"); got["title"] != wantTitle {
				t.Errorf("title = %v, want %q", got["title"], wantTitle)
			}
			// Runtime: the sum of the section playtimes. LibriVox's own total is
			// its stored clock string, which can sit a second off that sum (296).
			gotSecs, lvSecs := int(got["totaltimesecs"].(float64)), int(lv["totaltimesecs"].(float64))
			if d := gotSecs - lvSecs; d < -1 || d > 1 {
				t.Errorf("totaltimesecs = %d, LibriVox says %d", gotSecs, lvSecs)
			}
			if d := gotSecs - lvSecs; d == 0 {
				same("totaltime")
			} else if got["totaltime"] != clockTime(gotSecs) {
				t.Errorf("totaltime = %v for %d seconds", got["totaltime"], gotSecs)
			}

			comparePeople(t, "authors", got["authors"], lv["authors"])
			comparePeople(t, "translators", got["translators"], lv["translators"])

			gs, ls := got["sections"].([]any), lv["sections"].([]any)
			if len(gs) != len(ls) {
				t.Fatalf("sections = %d, LibriVox has %d", len(gs), len(ls))
			}
			soloReader := map[string]bool{}
			for _, s := range ls {
				for _, r := range s.(map[string]any)["readers"].([]any) {
					soloReader[r.(map[string]any)["reader_id"].(string)] = true
				}
			}
			for i := range gs {
				g, l := gs[i].(map[string]any), ls[i].(map[string]any)
				for _, f := range []string{"title", "listen_url", "playtime", "file_name"} {
					if !jsonEqual(g[f], l[f]) {
						t.Errorf("section %d %s = %#v, LibriVox says %#v", i, f, g[f], l[f])
					}
				}
				// One reader for the whole project: named on every section, as
				// LibriVox names them. Several: none (not stored per section).
				if len(soloReader) == 1 {
					if !jsonEqual(g["readers"], l["readers"]) {
						t.Errorf("section %d readers = %v, LibriVox says %v", i, g["readers"], l["readers"])
					}
				} else if len(g["readers"].([]any)) != 0 {
					t.Errorf("section %d of a multi-reader project names readers: %v", i, g["readers"])
				}
				if g["section_number"] != itoa(i+1) || g["id"] != "" || g["language"] != got["language"] {
					t.Errorf("section %d placeholders = number %v id %v language %v", i, g["section_number"], g["id"], g["language"])
				}
			}

			// The fields the mirror does not keep, by design.
			if got["description"] != "" || got["url_project"] != "" || got["coverart_pdf"] != nil || got["coverart_thumbnail"] != nil {
				t.Errorf("unmirrored fields = description %q url_project %q pdf %v thumb %v",
					got["description"], got["url_project"], got["coverart_pdf"], got["coverart_thumbnail"])
			}
			for _, g := range got["genres"].([]any) {
				if gm := g.(map[string]any); gm["id"] != "" || gm["name"] != strings.ToLower(gm["name"].(string)) {
					t.Errorf("genre = %v, want a vocabulary value with an empty id", gm)
				}
			}
		})
	}
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// comparePeople compares authors or translators: the id, the name split, and the
// years - except that LibriVox's non-year dod/dob ("alive", "") come back "".
func comparePeople(t *testing.T, what string, got, lv any) {
	t.Helper()
	gl, ll := got.([]any), lv.([]any)
	if len(gl) != len(ll) {
		t.Fatalf("%s = %v, LibriVox says %v", what, gl, ll)
	}
	for i := range gl {
		g, l := gl[i].(map[string]any), ll[i].(map[string]any)
		for _, f := range []string{"id", "first_name", "last_name"} {
			if g[f] != l[f] {
				t.Errorf("%s[%d].%s = %v, LibriVox says %v", what, i, f, g[f], l[f])
			}
		}
		for _, f := range []string{"dob", "dod"} {
			wantYear := l[f].(string)
			if wantYear == "alive" {
				wantYear = ""
			}
			if g[f] != wantYear {
				t.Errorf("%s[%d].%s = %v, LibriVox says %v", what, i, f, g[f], l[f])
			}
		}
	}
}

// jsonEqual compares two decoded JSON values.
func jsonEqual(a, b any) bool {
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb)
}

func TestLibrivoxFeedParameters(t *testing.T) {
	ts := librivoxServer(t)
	all := []string{"296", "527", "801", "1870"}
	for _, tc := range []struct {
		query string
		want  []string
	}{
		{"", all},
		{"?format=json", all},
		{"?format=JSON", all},
		{"?sort_order=desc", []string{"1870", "801", "527", "296"}},
		{"?sort_order=sideways", all},
		{"?limit=2", []string{"296", "527"}},
		{"?limit=2&offset=2", []string{"801", "1870"}},
		{"?limit=0", all}, // empty in PHP: the default
		{"?limit=500", all},
		{"?id=801", []string{"801"}},
		{"?title=Metamorphosis", []string{"527"}},
		{"?title=metamorphosis", []string{"527"}},
		{"?title=^cand", []string{"801", "1870"}},
		{"?title=Candide", []string{"801"}},
		{"?title=^100%25", nil},
		{"?author=Kafka", []string{"527"}},
		{"?author=^vol", []string{"801", "1870"}},
		{"?author=Franz", nil},    // a first name is not a last name
		{"?author=Johnston", nil}, // a translator is not an author
		{"?author=various", []string{"296"}},
		{"?genre=Short%20Stories", []string{"296"}},
		{"?genre=short-stories", []string{"296"}},
		{"?genre=^short", []string{"296"}},
		{"?genre=General%20Fiction", nil}, // not mapped: no genre stored
		{"?since=" + lvSinceImport, all},
		{"?since=" + lvSinceSameDay, nil}, // the day starts at midnight UTC, as LibriVox compares
		{"?since=" + lvSince2026, nil},
		{"?since=1793059200", all}, // exactly midnight of the import day
	} {
		path := LibrivoxFeedPath + tc.query
		code, body := artifacttest.GetJSON(t, ts.URL, path)
		if tc.want == nil {
			if code != http.StatusNotFound || body["error"] != librivoxNotFound {
				t.Errorf("GET %s = %d %v, want LibriVox's 404", path, code, body)
			}
			continue
		}
		if code != http.StatusOK {
			t.Errorf("GET %s = %d %v", path, code, body)
			continue
		}
		var books []map[string]any
		for _, b := range body["books"].([]any) {
			books = append(books, b.(map[string]any))
		}
		if got := idsOfBooks(books); strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("GET %s = %v, want %v", path, got, tc.want)
		}
	}

	// The control (a user's recording) is never in the feed, by any parameter.
	if code, body := artifacttest.GetJSON(t, ts.URL, LibrivoxFeedPath+"?title=The%20Trial&author=Kafka"); code != http.StatusNotFound {
		t.Errorf("the feed holds the non-LibriVox control: %d %v", code, body)
	}

	// The trailing-slash spelling LibriVox documents.
	if books := feedBooks(t, ts.URL, LibrivoxFeedPath+"/?id=527&format=json"); len(books) != 1 || books[0]["id"] != "527" {
		t.Errorf("trailing-slash feed = %v", books)
	}

	for _, bad := range []string{"?limit=501", "?limit=100000", "?format=xml", "?format=jsonp", "?format=php",
		"?limit=-1", "?limit=ten", "?offset=-1", "?id=abc", "?since=yesterday"} {
		if code, body := artifacttest.GetJSON(t, ts.URL, LibrivoxFeedPath+bad); code != http.StatusBadRequest || body["error"] == nil {
			t.Errorf("GET %s = %d %v, want a 400", bad, code, body)
		}
	}
	if _, body := artifacttest.GetJSON(t, ts.URL, LibrivoxFeedPath+"?limit=501"); !strings.Contains(body["error"].(string), "500") {
		t.Errorf("the cap's 400 does not name the cap: %v", body)
	}
}

func TestLibrivoxFeedFieldsAndFlags(t *testing.T) {
	ts := librivoxServer(t)
	base := feedBooks(t, ts.URL, LibrivoxFeedPath+"?id=527")[0]
	for _, k := range []string{"sections", "genres", "translators", "url_iarchive", "coverart_jpg", "coverart_pdf", "coverart_thumbnail"} {
		if _, ok := base[k]; ok {
			t.Errorf("a plain request carries %s, which only extended/coverart add", k)
		}
	}
	if base["authors"] == nil || base["totaltimesecs"] != float64(9265) {
		t.Errorf("a plain request = %v", base)
	}
	if b := feedBooks(t, ts.URL, LibrivoxFeedPath+"?id=527&extended=0&coverart=0")[0]; b["sections"] != nil || b["coverart_jpg"] != nil {
		t.Errorf("extended=0/coverart=0 read as on: %v", b)
	}
	if b := feedBooks(t, ts.URL, LibrivoxFeedPath+"?id=527&extended=yes")[0]; b["sections"] == nil {
		t.Errorf("extended=yes read as off")
	}
	for _, q := range []string{"fields={id,title,authors}", "fields=id,title,authors", "fields[]=id&fields[]=title&fields[]=authors"} {
		b := feedBooks(t, ts.URL, LibrivoxFeedPath+"?id=527&extended=1&"+q)[0]
		keys := make([]string, 0, len(b))
		for k := range b {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		if strings.Join(keys, ",") != "authors,id,title" {
			t.Errorf("%s kept %v", q, keys)
		}
	}
}

// TestLibrivoxFeedQueries pins the unfiltered page query's shape and the two
// renderings the golden test leans on: the clock string and the name split.
func TestLibrivoxFeedQueries(t *testing.T) {
	q, args := librivoxFeedQuery(librivoxFeedParams{limit: 50})
	if !strings.HasPrefix(q, librivoxFeedSelect) || !strings.HasSuffix(q, "ORDER BY r.librivox_id ASC LIMIT ? OFFSET ?") || len(args) != 2 {
		t.Errorf("unfiltered page = %s %v", q, args)
	}
	if got := clockTime(9265); got != "2:34:25" {
		t.Errorf("clockTime(9265) = %q", got)
	}
	if got := clockTime(36000 + 61); got != "10:01:01" {
		t.Errorf("clockTime = %q", got)
	}
	for name, want := range map[string][2]string{
		"Franz Kafka":        {"Franz", "Kafka"},
		"Voltaire":           {"", "Voltaire"},
		"Arthur Conan Doyle": {"Arthur Conan", "Doyle"},
		" Ian  Johnston ":    {"Ian", "Johnston"},
	} {
		if f, l := splitLastWord(name); f != want[0] || l != want[1] {
			t.Errorf("splitLastWord(%q) = %q %q", name, f, l)
		}
	}
}

// TestSinceDay pins the since cutoff: the first UTC day starting at or after the
// time, clamped beyond the four-digit years.
func TestSinceDay(t *testing.T) {
	for _, tc := range []struct {
		since int64
		want  string
	}{
		{1793059200, "2026-10-27"}, // exactly midnight
		{1793059201, "2026-10-28"}, // a second later: the next day
		{-1, "1970-01-01"},
		{-1 << 62, "0000-00-00"},
		{1 << 62, "9999-99-99"},
		{253402300799, "9999-99-99"}, // 9999-12-31T23:59:59Z rounds past the last day
	} {
		if got := sinceDay(tc.since); got != tc.want {
			t.Errorf("sinceDay(%d) = %q, want %q", tc.since, got, tc.want)
		}
	}
}
