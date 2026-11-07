package query

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/artifacttest"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

func catalogueFixture() *model.Catalog {
	cat := &model.Catalog{People: []*model.Person{{ID: "reader", Name: "Reader", License: "CC0-1.0"}}}
	series := &model.Series{ID: "long-series", Name: "Long Series", License: "CC0-1.0"}
	for i := range 125 {
		id := fmt.Sprintf("book-%03d", i)
		work := &model.Work{ID: id, Title: fmt.Sprintf("Book %03d", i), Language: "en", Authors: []string{"reader"}, License: "CC0-1.0"}
		pos := fmt.Sprint(i + 1)
		if i == 1 {
			pos = "2.5"
		}
		if i == 2 {
			pos = "3-3.5"
		}
		series.Works = append(series.Works, model.SeriesWork{Work: id, Position: pos})
		// The final member has no recording; the other works have more versions
		// than the guide may return. Repeated narrator credits must not duplicate
		// either the summary's people or a person's recording list.
		if i < 124 {
			for j := range 8 {
				rec := &model.Recording{ID: fmt.Sprintf("reading-%02d", j), Work: id, Language: "en", Narrators: []string{"reader", "reader"}, License: "CC0-1.0"}
				if j == 0 {
					rec.AbridgedKnown = true
					rec.RuntimeMin = 60
				}
				if j == 1 {
					rec.Abridged = true
				}
				if j == 7 {
					rec.Language = "de-at"
				}
				if i == 0 && j == 0 {
					for k := range 203 {
						rec.Chapters = append(rec.Chapters, model.Chapter{Title: fmt.Sprintf("Chapter %d", k+1), StartMS: int64(k) * 1000, LengthMS: 1000})
					}
				}
				work.Recordings = append(work.Recordings, rec)
			}
		}
		cat.Works = append(cat.Works, work)
	}
	cat.Series = []*model.Series{series, {ID: "empty-series", Name: "Empty Series", License: "CC0-1.0"}}
	cat.Redirects = model.Redirects{model.RedirectSeries: {"old-series": "long-series"}, model.RedirectPeople: {"old-reader": "reader"}}
	return cat
}

func TestSeriesListeningGuideBoundsAndFacts(t *testing.T) {
	snap := openTestDB(t, artifacttest.Build(t, catalogueFixture()))
	guide, err := snap.SeriesListeningGuide(t.Context(), "long-series", 1000, 0)
	if err != nil {
		t.Fatal(err)
	}
	if guide.WorksTotal != 125 || guide.Limit != 100 || len(guide.Entries) != 100 {
		t.Fatalf("window = total %d limit %d entries %d", guide.WorksTotal, guide.Limit, len(guide.Entries))
	}
	for i, entry := range guide.Entries {
		if entry.Work.ID != fmt.Sprintf("book-%03d", i) || len(entry.Recordings) != 5 || entry.RecordingsTotal != 8 || entry.RecordingsLimit != 5 {
			t.Fatalf("entry %d = %+v", i, entry)
		}
		for j, r := range entry.Recordings {
			if r.ID != fmt.Sprintf("reading-%02d", j) || len(r.Narrators) != 1 || r.Narrators[0].ID != "reader" {
				t.Fatalf("recording %d = %+v", j, r)
			}
		}
	}
	first := guide.Entries[0].Recordings
	if first[0].Abridgement != "unabridged" || first[0].RuntimeMin != 60 || first[0].ChapterCount != 203 || first[1].Abridgement != "abridged" || first[2].Abridgement != "" || first[2].ReleaseDate != "" || first[2].Publisher != "" {
		t.Fatalf("recording facts = %+v", first)
	}
	page, err := snap.SeriesListeningGuide(t.Context(), "long-series", 100, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != 25 || page.Entries[0].Work.ID != "book-100" {
		t.Fatalf("page two = %+v", page)
	}
	last := page.Entries[24]
	if last.RecordingsTotal != 0 || last.Recordings == nil || len(last.Recordings) != 0 {
		t.Fatalf("zero recordings = %+v", last)
	}
	for _, tt := range []struct {
		id     string
		offset int
		absent bool
	}{{"long-series", 10000, false}, {"empty-series", 0, false}, {"missing", 0, true}} {
		page, err := snap.SeriesListeningGuide(t.Context(), tt.id, 50, tt.offset)
		if err != nil {
			t.Fatal(err)
		}
		if (page == nil) != tt.absent {
			t.Fatalf("%s: page=%+v", tt.id, page)
		}
		if page != nil && len(page.Entries) != 0 {
			t.Fatalf("%s: nonempty page %+v", tt.id, page)
		}
	}
}

func TestPersonRecordingsPreservesVersionsAndFilters(t *testing.T) {
	snap := openTestDB(t, artifacttest.Build(t, catalogueFixture()))
	page, err := snap.PersonRecordings(t.Context(), "reader", 1000, 0, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 124*8 || len(page.Recordings) != 100 || page.Limit != 100 {
		t.Fatalf("window = %+v", page)
	}
	for i := range 8 {
		entry := page.Recordings[i]
		if entry.Work.ID != "book-000" || entry.Recording.ID != fmt.Sprintf("reading-%02d", i) || len(entry.Series) != 1 || entry.Series[0].ID != "long-series" || len(entry.Recording.Narrators) != 1 {
			t.Fatalf("credit %d = %+v", i, entry)
		}
	}
	page, err = snap.PersonRecordings(t.Context(), "reader", 50, 100, "long-series", "de")
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 124 || len(page.Recordings) != 24 || page.Recordings[0].Work.ID != "book-100" || page.Recordings[0].Recording.Language != "de-at" {
		t.Fatalf("filtered page = %+v", page)
	}
	for _, tt := range []struct{ series, lang string }{{"empty-series", ""}, {"missing-series", ""}, {"", "fr"}} {
		page, err := snap.PersonRecordings(t.Context(), "reader", 50, 0, tt.series, tt.lang)
		if err != nil || page.Total != 0 || page.Recordings == nil || len(page.Recordings) != 0 {
			t.Fatalf("empty %+v = %+v, %v", tt, page, err)
		}
	}
	page, err = snap.PersonRecordings(t.Context(), "reader", 50, 10000, "", "")
	if err != nil || page.Total != 992 || len(page.Recordings) != 0 {
		t.Fatalf("out of range = %+v, %v", page, err)
	}
	if page, err := snap.PersonRecordings(t.Context(), "missing", 50, 0, "", ""); err != nil || page != nil {
		t.Fatalf("missing = %+v, %v", page, err)
	}
}

func TestBrowseAndRecordingChapterWindows(t *testing.T) {
	snap := openTestDB(t, artifacttest.Build(t, catalogueFixture()))
	page, err := snap.Browse(t.Context(), WorkPages, 1000, 100)
	if err != nil || page.Total != 125 || page.Limit != 100 || len(page.Entries) != 25 || page.Entries[0].ID != "book-100" || page.Entries[0].Title != "Book 100" {
		t.Fatalf("browse = %+v, %v", page, err)
	}
	for _, family := range []PageFamily{PersonPages, SeriesPages, RecapPages, CharacterPages} {
		page, err := snap.Browse(t.Context(), family, 100, 0)
		if err != nil || page == nil || page.Entries == nil {
			t.Fatalf("%s: %+v, %v", family, page, err)
		}
	}
	if page, err := snap.Browse(t.Context(), "unknown", 100, 0); err != nil || page != nil {
		t.Fatalf("unknown = %+v, %v", page, err)
	}
	chapters, err := snap.RecordingChapters(t.Context(), "book-000", "reading-00", 1000, 100)
	if err != nil || chapters.Total != 203 || chapters.Limit != 100 || len(chapters.Chapters) != 100 || chapters.Chapters[0].Title != "Chapter 101" || chapters.Chapters[0].StartMS != 100000 || chapters.Chapters[0].LengthMS != 1000 {
		t.Fatalf("chapters = %+v, %v", chapters, err)
	}
	chapters, err = snap.RecordingChapters(t.Context(), "book-000", "reading-00", 100, 200)
	if err != nil || len(chapters.Chapters) != 3 {
		t.Fatalf("last page = %+v, %v", chapters, err)
	}
	chapters, err = snap.RecordingChapters(t.Context(), "book-000", "reading-00", 100, 10000)
	if err != nil || chapters.Total != 203 || len(chapters.Chapters) != 0 {
		t.Fatalf("past end = %+v, %v", chapters, err)
	}
	chapters, err = snap.RecordingChapters(t.Context(), "book-000", "reading-01", 100, 0)
	if err != nil || chapters == nil || chapters.Chapters == nil || chapters.Total != 0 {
		t.Fatalf("no chapters = %+v, %v", chapters, err)
	}
	for _, pair := range [][2]string{{"book-000", "missing"}, {"missing", "reading-00"}} {
		if page, err := snap.RecordingChapters(t.Context(), pair[0], pair[1], 100, 0); err != nil || page != nil {
			t.Fatalf("missing recording %v = %+v, %v", pair, page, err)
		}
	}
}

func TestAbridgementLegacyArtifacts(t *testing.T) {
	cat := catalogueFixture()
	for _, version := range []int{7, 11, 12} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			var stmts []string
			if version < 12 {
				stmts = append(stmts, "ALTER TABLE recordings DROP COLUMN abridgement", "DROP INDEX idx_series_works_position", "DROP INDEX idx_recording_narrators_person_recording")
			}
			if version < 8 {
				stmts = append(stmts, "ALTER TABLE chapters DROP COLUMN audio_url")
			}
			snap := openTestDB(t, artifacttest.Altered(t, cat, version, stmts...))
			page, err := snap.SeriesListeningGuide(t.Context(), "long-series", 1, 0)
			if err != nil {
				t.Fatal(err)
			}
			want := ""
			if version == 12 {
				want = "unabridged"
			}
			if page.Entries[0].Recordings[0].Abridgement != want || page.Entries[0].Recordings[1].Abridgement != "abridged" {
				t.Fatalf("v%d: %+v", version, page.Entries[0].Recordings)
			}
			work, err := snap.WorkDetail("book-000")
			if err != nil {
				t.Fatal(err)
			}
			if work.Recordings[0].Abridgement != want || work.Recordings[0].Language != "en" || work.Recordings[1].Abridgement != "abridged" || !work.Recordings[1].Abridged {
				t.Fatalf("legacy work = %+v", work.Recordings)
			}
			chapters, err := snap.RecordingChapters(t.Context(), "book-000", "reading-00", 100, 100)
			if err != nil || len(chapters.Chapters) != 100 {
				t.Fatalf("old chapters = %+v, %v", chapters, err)
			}
			credits, err := snap.PersonRecordings(t.Context(), "reader", 1, 0, "long-series", "de")
			if err != nil || credits.Total != 124 {
				t.Fatalf("old credits = %+v, %v", credits, err)
			}
		})
	}
	path := artifacttest.Altered(t, cat, 12, "ALTER TABLE recordings DROP COLUMN abridgement")
	if db, err := Open(path, ""); err == nil {
		_ = db.Close()
		t.Fatal("accepted v12 without abridgement")
	} else if !strings.Contains(err.Error(), "requires the abridgement column") {
		t.Fatal(err)
	}
}

func TestCatalogueRoutes(t *testing.T) {
	_, ts := newTestServerForCatalog(t, catalogueFixture())
	for _, path := range []string{"/api/v1/series/long-series/listening-guide?limit=1&offset=2", "/api/v1/people/reader/recordings?limit=1&lang=de&series=long-series"} {
		code, body := artifacttest.GetJSON(t, ts.URL, path)
		if code != http.StatusOK || body["limit"] != float64(1) {
			t.Fatalf("%s = %d %+v", path, code, body)
		}
	}
	for _, path := range []string{"/api/v1/series/missing/listening-guide", "/api/v1/people/missing/recordings"} {
		code, _ := artifacttest.GetJSON(t, ts.URL, path)
		if code != http.StatusNotFound {
			t.Fatalf("%s = %d", path, code)
		}
	}
	for _, path := range []string{"/api/v1/people/reader/recordings?lang=not_a_tag", "/api/v1/people/reader/recordings?series=bad%2Fslug"} {
		code, _ := artifacttest.GetJSON(t, ts.URL, path)
		if code != http.StatusBadRequest {
			t.Fatalf("%s = %d", path, code)
		}
	}
	for _, tt := range []struct{ from, to string }{{"/api/v1/series/old-series/listening-guide?limit=1&offset=2", "/api/v1/series/long-series/listening-guide?limit=1&offset=2"}, {"/api/v1/people/old-reader/recordings?lang=de&offset=50", "/api/v1/people/reader/recordings?lang=de&offset=50"}} {
		resp := artifacttest.GetNoFollow(t, ts.URL, tt.from)
		if resp.StatusCode != http.StatusMovedPermanently || resp.Header.Get("Location") != tt.to {
			t.Fatalf("%s -> %d %s", tt.from, resp.StatusCode, resp.Header.Get("Location"))
		}
	}
}

// CTEs/window functions scan their bounded ephemeral relations. Every actual
// catalogue table must still be reached through an index; inspect the exact
// SQL constructors used above rather than lookalike query strings.
func TestCatalogueReadsAreIndexed(t *testing.T) {
	snap := openTestDB(t, artifacttest.Build(t, catalogueFixture()))
	selected, args := selectedRecordingsSQL([]narratedCredit{{"book-000", "reading-00"}, {"book-001", "reading-01"}})
	base, filterArgs := personRecordingCandidatesSQL("long-series", langFilter{"de"})
	queries := []struct {
		query string
		args  []any
	}{
		{seriesListeningMembersSQL, []any{"long-series", 100, 100}},
		{seriesListeningTotalSQL, []any{"long-series"}},
		{listeningCandidatesSQL("?,?"), []any{"book-000", "book-001", 5}},
		{recordingSummariesSQL(selected, true), args},
		{recordingSummaryNarratorsSQL(selected), args},
		{`SELECT DISTINCT rn.work_id, rn.recording_id` + base + ` ORDER BY w.title, rn.work_id, rn.recording_id LIMIT ? OFFSET ?`, append(append([]any{"reader"}, filterArgs...), 100, 100)},
		{pagedChaptersSQL(true), []any{"book-000", "reading-00", 100, 100}},
		{browseEntriesSQL(WorkPages, "?,?"), []any{"book-000", "book-001"}},
		{browseEntriesSQL(PersonPages, "?"), []any{"reader"}},
		{browseEntriesSQL(SeriesPages, "?"), []any{"long-series"}},
	}
	for _, tt := range queries {
		plan := queryPlan(t, snap, tt.query, tt.args...)
		if tt.query == seriesListeningMembersSQL {
			if !strings.Contains(strings.Join(plan, " "), "idx_series_works_position") || strings.Contains(strings.Join(plan, " "), "TEMP B-TREE") {
				t.Errorf("series window must walk the numeric position index: %v", plan)
			}
		}
		for _, step := range plan {
			if !strings.HasPrefix(step, "SCAN ") || strings.Contains(step, "USING") {
				continue
			}
			for _, table := range []string{"recordings", "recording_narrators", "series_works", "chapters", "works", "people", "series", "rn", "r", "sw", "c", "w", "p"} {
				if step == "SCAN "+table {
					t.Errorf("full catalogue scan %q: %v", step, plan)
				}
			}
		}
	}
}
