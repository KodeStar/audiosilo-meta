package query

import (
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/artifacttest"
	"github.com/kodestar/audiosilo-meta/internal/testpack"
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

// TestSeriesListeningGuideRecordingOrder pins the guide's stated recording
// order (listeningCandidatesSQL): narrator continuity with the series' OTHER
// volumes first, then the earliest release date with undated recordings last,
// then the recording id. The narrators' other credits outside the series and a
// second recording of the same work must not count as continuity.
func TestSeriesListeningGuideRecordingOrder(t *testing.T) {
	cat := &model.Catalog{Series: []*model.Series{{ID: "saga", Name: "Saga", License: "CC0-1.0"}}}
	for _, id := range []string{"voice", "other", "other4", "solo", "solo2", "solo3", "twice", "solo-c"} {
		cat.People = append(cat.People, &model.Person{ID: id, Name: id, License: "CC0-1.0"})
	}
	type rec struct {
		id, date  string
		narrators []string
	}
	add := func(work, position string, recs ...rec) {
		w := &model.Work{ID: work, Title: work, License: "CC0-1.0"}
		for _, r := range recs {
			w.Recordings = append(w.Recordings, &model.Recording{ID: r.id, Work: work, ReleaseDate: r.date, Narrators: r.narrators, License: "CC0-1.0"})
		}
		cat.Works = append(cat.Works, w)
		if position != "" {
			cat.Series[0].Works = append(cat.Series[0].Works, model.SeriesWork{Work: work, Position: position})
		}
	}
	add("vol-a", "1",
		rec{"a-1", "2001", []string{"solo"}},        // no continuity
		rec{"a-2", "2020-01-01", []string{"voice"}}, // narrates b and c too: 2
		rec{"a-3", "2005-06", []string{"other"}},    // narrates b: 1
		rec{"a-4", "1999-02-03", []string{"other"}}, // 1, earlier
		rec{"a-5", "", []string{"other"}},           // 1, undated
		rec{"a-6", "1990", []string{"solo2"}},       // none, earliest
		rec{"a-7", "", []string{"solo3"}},           // none, undated
		rec{"a-0", "2005-06", []string{"other4"}},   // narrates c: 1, ties a-3 on date
	)
	add("vol-b", "2",
		rec{"b-1", "", []string{"voice"}},
		rec{"b-2", "", []string{"other"}},
		rec{"b-3", "1950", []string{"twice"}}, // two recordings of ONE work are no continuity
		rec{"b-4", "1950", []string{"twice"}},
	)
	add("vol-c", "3",
		rec{"c-1", "", []string{"solo-c", "voice"}}, // the best of its narrators counts
		rec{"c-2", "", []string{"other4"}},
	)
	// A volume of ANOTHER series: its narrators are no continuity in this one.
	add("elsewhere", "", rec{"x-1", "", []string{"solo", "solo2", "solo3", "twice"}})
	cat.Series = append(cat.Series, &model.Series{ID: "other-saga", Name: "Other Saga", License: "CC0-1.0",
		Works: []model.SeriesWork{{Work: "elsewhere", Position: "1"}}})
	snap := openTestDB(t, artifacttest.Build(t, cat))
	guide, err := snap.SeriesListeningGuide(t.Context(), "saga", 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{
		"vol-a": {"a-2", "a-4", "a-0", "a-3", "a-5"},
		"vol-b": {"b-1", "b-2", "b-3", "b-4"},
		"vol-c": {"c-1", "c-2"},
	}
	if len(guide.Entries) != 3 {
		t.Fatalf("entries = %+v", guide.Entries)
	}
	for _, entry := range guide.Entries {
		var got []string
		for _, r := range entry.Recordings {
			got = append(got, r.ID)
		}
		if !slices.Equal(got, want[entry.Work.ID]) {
			t.Errorf("%s recordings = %v, want %v", entry.Work.ID, got, want[entry.Work.ID])
		}
	}
	if total := guide.Entries[0].RecordingsTotal; total != 8 {
		t.Errorf("vol-a recordings_total = %d, want 8", total)
	}
}

// TestSeriesOrderOverThePositionGrammar pins THE series member order
// (seriesMemberOrderSQL: CAST(position AS REAL), position, work_id) over every
// shape series.schema.json's position pattern admits, through both readers of
// it: the listening guide and DB.Series. SQLite's CAST reads the longest numeric
// prefix ("1-15" -> 1, "2.10-3" -> 2.1), which is the range's start only while
// the grammar stays numeric; ranges, a decimal whose digits sort differently as
// text ("2.10" vs "2.9"), a leading zero, 0 and a fraction below 1 are all here.
// The expected order is the one the Go sort DB.Series used before the order
// moved into SQL, so the move changed nothing over schema-valid positions.
func TestSeriesOrderOverThePositionGrammar(t *testing.T) {
	grammar := regexp.MustCompile(testpack.SeriesPositionPattern(t))
	// Listed out of order, with work ids that disagree with the position order.
	members := []struct{ work, position string }{
		{"w-01", "10.0"}, {"w-02", "2.9"}, {"w-03", "1-3.5"}, {"w-04", "0.5"},
		{"w-05", "2.10-3"}, {"w-06", "1"}, {"w-07", "9"}, {"w-08", "02"},
		{"w-09", "1-15"}, {"w-10", "0"}, {"w-11", "2.10"}, {"w-12", "10"},
		{"w-14", "1.5"}, {"w-13", "1.5"}, {"w-15", "2"},
	}
	want := []string{
		"w-10",                 // 0
		"w-04",                 // 0.5
		"w-06", "w-09", "w-03", // 1, 1-15, 1-3.5: one start, then the string
		"w-13", "w-14", // 1.5 twice: the work id breaks the tie
		"w-08", "w-15", // 02, 2
		"w-11", "w-05", // 2.10, 2.10-3: 2.1, not 2.10 > 2.9
		"w-02",         // 2.9
		"w-07",         // 9
		"w-12", "w-01", // 10, 10.0
	}
	cat := &model.Catalog{Series: []*model.Series{{ID: "grammar", Name: "Grammar", License: "CC0-1.0"}}}
	for _, m := range members {
		if !grammar.MatchString(m.position) {
			t.Fatalf("fixture position %q is outside the schema grammar", m.position)
		}
		cat.Works = append(cat.Works, &model.Work{ID: m.work, Title: m.work, License: "CC0-1.0"})
		cat.Series[0].Works = append(cat.Series[0].Works, model.SeriesWork{Work: m.work, Position: m.position})
	}
	snap := openTestDB(t, artifacttest.Build(t, cat))

	guide, err := snap.SeriesListeningGuide(t.Context(), "grammar", len(members), 0)
	if err != nil {
		t.Fatal(err)
	}
	var fromSQL []string
	for _, entry := range guide.Entries {
		fromSQL = append(fromSQL, entry.Work.ID)
	}

	series, err := snap.Series("grammar", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	var fromSeries []string
	for _, w := range series.Works {
		fromSeries = append(fromSeries, w.Work.ID)
	}
	if !slices.Equal(fromSQL, want) {
		t.Errorf("guide order = %v\nwant          %v", fromSQL, want)
	}
	if !slices.Equal(fromSeries, want) {
		t.Errorf("Series order = %v\nwant           %v", fromSeries, want)
	}
}

func TestSeriesListeningGuideMatchesAPITiesWithoutPositionIndex(t *testing.T) {
	cat := &model.Catalog{Series: []*model.Series{{ID: "ties", Name: "Ties", License: "CC0-1.0"}}}
	// Work-ID order deliberately places a range before its equal numeric start.
	for i, position := range []string{"1-3", "1", "1.5-2", "1.5", "1.5", "2"} {
		id := fmt.Sprintf("work-%d", i)
		cat.Works = append(cat.Works, &model.Work{ID: id, Title: id, License: "CC0-1.0"})
		cat.Series[0].Works = append(cat.Series[0].Works, model.SeriesWork{Work: id, Position: position})
	}
	for _, indexed := range []bool{false, true} {
		t.Run(fmt.Sprintf("indexed=%t", indexed), func(t *testing.T) {
			path := artifacttest.Build(t, cat)
			if !indexed {
				path = artifacttest.Altered(t, cat, 11, "ALTER TABLE recordings DROP COLUMN abridgement", "DROP INDEX idx_series_works_position", "DROP TABLE work_alternate_titles")
			}
			snap := openTestDB(t, path)
			want := []string{"work-1", "work-0", "work-3", "work-4", "work-2", "work-5"}
			for offset := 0; offset < len(want); offset += 2 {
				api, err := snap.Series("ties", 2, offset)
				if err != nil {
					t.Fatal(err)
				}
				guide, err := snap.SeriesListeningGuide(t.Context(), "ties", 2, offset)
				if err != nil {
					t.Fatal(err)
				}
				if len(api.Works) != 2 || len(guide.Entries) != 2 {
					t.Fatalf("offset %d: API %d, guide %d entries", offset, len(api.Works), len(guide.Entries))
				}
				for i, entry := range guide.Entries {
					if entry.Work.ID != want[offset+i] || api.Works[i].Work.ID != entry.Work.ID || api.Works[i].Position != entry.Position {
						t.Errorf("offset %d entry %d: API %+v, guide %+v, want %s", offset, i, api.Works[i], entry, want[offset+i])
					}
				}
			}
		})
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

// TestPersonRecordingsOrderTitlesWithoutCase pins every person list - the
// recording credits and the person detail's authored, narrated and translated
// lists - to the browse directories' NOCASE collation: binary order would put
// "Banana" and "Zebra" before "apple", and the work id breaks a tie between
// titles that differ only in case.
func TestPersonRecordingsOrderTitlesWithoutCase(t *testing.T) {
	cat := &model.Catalog{People: []*model.Person{{ID: "reader", Name: "Reader", License: "CC0-1.0"}}}
	for _, w := range []struct{ id, title string }{
		{"w-zebra", "Zebra"}, {"w-apple", "apple"}, {"w-banana", "Banana"},
		{"w-cherry-b", "cherry"}, {"w-cherry-a", "Cherry"},
	} {
		translator := []model.Credit{{Person: "reader", Role: model.RoleTranslator}}
		cat.Works = append(cat.Works, &model.Work{ID: w.id, Title: w.title, Authors: []string{"reader"}, License: "CC0-1.0", Recordings: []*model.Recording{
			{ID: "r-2", Work: w.id, Narrators: []string{"reader"}, Credits: translator, License: "CC0-1.0"},
			{ID: "r-1", Work: w.id, Narrators: []string{"reader"}, Credits: translator, License: "CC0-1.0"},
		}})
	}
	snap := openTestDB(t, artifacttest.Build(t, cat))
	page, err := snap.PersonRecordings(t.Context(), "reader", 50, 0, "", "")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, entry := range page.Recordings {
		got = append(got, entry.Work.ID+"/"+entry.Recording.ID)
	}
	want := []string{
		"w-apple/r-1", "w-apple/r-2", "w-banana/r-1", "w-banana/r-2",
		"w-cherry-a/r-1", "w-cherry-a/r-2", "w-cherry-b/r-1", "w-cherry-b/r-2",
		"w-zebra/r-1", "w-zebra/r-2",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("credit order = %v\nwant           %v", got, want)
	}

	person, err := snap.Person("reader", 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	var authored []string
	for _, card := range person.Authored {
		authored = append(authored, card.ID)
	}
	if wantWorks := []string{"w-apple", "w-banana", "w-cherry-a", "w-cherry-b", "w-zebra"}; !slices.Equal(authored, wantWorks) {
		t.Errorf("authored order = %v\nwant           %v", authored, wantWorks)
	}
	for name, list := range map[string][]NarratedEntry{"narrated": person.Narrated, "translated": person.Translated} {
		var got []string
		for _, entry := range list {
			got = append(got, entry.Work.ID+"/"+entry.RecordingID)
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s order = %v\nwant %v", name, got, want)
		}
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

// TestAbridgementLegacyArtifacts: the abridgement column arrived in schema 13.
// A schema 12 artifact (alternate titles, no abridgement column) and the older
// ones still serve every catalogue read, with a legacy false read as unknown;
// the 12 one keeps its alternate titles.
func TestAbridgementLegacyArtifacts(t *testing.T) {
	for _, version := range []int{7, 11, 12, 13} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			t.Parallel() // each subtest builds the 1,000-recording fixture
			cat := catalogueFixture()
			cat.Works[0].AlternateTitles = []string{"Book Zero"} // book-000
			var stmts []string
			if version < 13 {
				stmts = append(stmts, "ALTER TABLE recordings DROP COLUMN abridgement", "DROP INDEX idx_series_works_position", "DROP INDEX idx_recording_narrators_person_recording",
					"DROP INDEX idx_works_title_nocase", "DROP INDEX idx_people_name_nocase", "DROP INDEX idx_series_name_nocase")
			}
			if version < 12 {
				stmts = append(stmts, "DROP TABLE work_alternate_titles")
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
			if version >= 13 {
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
			var wantAlt []string
			if version >= 12 {
				wantAlt = []string{"Book Zero"}
			}
			if !slices.Equal(work.AlternateTitles, wantAlt) {
				t.Fatalf("v%d alternate titles = %v, want %v", version, work.AlternateTitles, wantAlt)
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
	path := artifacttest.Altered(t, artifacttest.Fixture(), 13, "ALTER TABLE recordings DROP COLUMN abridgement")
	if db, err := Open(path, ""); err == nil {
		_ = db.Close()
		t.Fatal("accepted v13 without abridgement")
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
		{listeningCandidatesSQL("?,?"), []any{"long-series", "book-000", "book-001", "book-000", "book-001", 5}},
		{recordingSummariesSQL(selected, true), args},
		{recordingSummaryNarratorsSQL(selected), args},
		{`SELECT DISTINCT rn.work_id, rn.recording_id` + base + personRecordingOrderSQL + ` LIMIT ? OFFSET ?`, append(append([]any{"reader"}, filterArgs...), 100, 100)},
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
