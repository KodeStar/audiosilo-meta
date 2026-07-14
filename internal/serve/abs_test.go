package serve

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kodestar/audiosilo-meta/internal/model"
)

// absServer builds a server over cat and returns its base URL.
func absServer(t *testing.T, cat *model.Catalog) string {
	t.Helper()
	dbPath := buildFixtureDB(t, cat, nil)
	srv, err := New(Config{DBPath: dbPath, swapGrace: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts.URL
}

// absMatches fetches /abs/search and returns the status plus the matches array
// (as []any). It fails the test if "matches" is absent or not an array (ABS
// hard-fails on that), so a passing call proves the array invariant.
func absMatches(t *testing.T, base, path string) (int, []any) {
	t.Helper()
	code, body := getJSON(t, base, path)
	if code == 200 {
		raw, ok := body["matches"]
		if !ok || raw == nil {
			t.Fatalf("GET %s: matches missing/null: %v", path, body)
		}
		arr, ok := raw.([]any)
		if !ok {
			t.Fatalf("GET %s: matches not an array: %v", path, raw)
		}
		return code, arr
	}
	return code, nil
}

// absFixture is the shared catalog for the endpoint tests: a work with TWO
// recordings (one with a us+uk ASIN and an ISBN, one uk-only) so one-per-
// recording, us-ASIN preference, and preferred-first ordering are all
// exercised; a single-recording ISBN work with dual narrators + a series
// membership; plus a distinct-author work sharing a title token for author
// ranking.
func absFixture() *model.Catalog {
	weir := &model.Person{ID: "andy-weir", Name: "Andy Weir", License: "CC0-1.0"}
	porter := &model.Person{ID: "ray-porter", Name: "Ray Porter", License: "CC0-1.0"}
	bray := &model.Person{ID: "rosario-dawson", Name: "Rosario Dawson", License: "CC0-1.0"}
	sando := &model.Person{ID: "brandon-sanderson", Name: "Brandon Sanderson", License: "CC0-1.0"}
	kramer := &model.Person{ID: "michael-kramer", Name: "Michael Kramer", License: "CC0-1.0"}
	reading := &model.Person{ID: "kate-reading", Name: "Kate Reading", License: "CC0-1.0"}
	king := &model.Person{ID: "stephen-king", Name: "Stephen King", License: "CC0-1.0"}

	phm := &model.Work{
		ID: "project-hail-mary", Title: "Project Hail Mary", Subtitle: "A Novel",
		Language: "en", Authors: []string{"andy-weir"}, FirstPublished: "2021",
		Description: "A lone astronaut must save the earth.", License: "CC0-1.0",
		Recordings: []*model.Recording{
			{
				ID: "ray-porter-2021", Work: "project-hail-mary", Language: "en",
				RuntimeMin: 970, Publisher: "Audible Studios", ReleaseDate: "2021-05-04",
				CoverURL: "https://example.test/phm.jpg", License: "CC0-1.0",
				Narrators: []string{"ray-porter"},
				// uk listed before us to prove region preference is by value, not order.
				ASIN: []model.ASIN{{Region: "uk", ASIN: "B08UKPRS1K"}, {Region: "us", ASIN: "B08G9PRS1K"}},
				ISBN: []string{"9780593135204"},
			},
			{
				ID: "dawson-2021", Work: "project-hail-mary", Language: "en",
				RuntimeMin: 965, Publisher: "Penguin Audio", License: "CC0-1.0",
				Narrators: []string{"rosario-dawson"},
				ASIN:      []model.ASIN{{Region: "uk", ASIN: "B09UKONLY0"}},
			},
		},
	}
	wok := &model.Work{
		ID: "the-way-of-kings", Title: "The Way of Kings", Language: "en",
		Authors: []string{"brandon-sanderson"}, FirstPublished: "2010", License: "CC0-1.0",
		Recordings: []*model.Recording{{
			ID: "kramer-reading-2010", Work: "the-way-of-kings", Language: "en",
			Narrators: []string{"michael-kramer", "kate-reading"}, License: "CC0-1.0",
			RuntimeMin: 2735, Publisher: "Macmillan Audio",
			ISBN: []string{"9781427209269"},
		}},
	}
	// A second work that shares the token "kings" with wok but a different author,
	// so an author= param must rank the matching author's work first.
	kings := &model.Work{
		ID: "the-kings-jest", Title: "The Kings Jest", Language: "en",
		Authors: []string{"stephen-king"}, License: "CC0-1.0",
		Recordings: []*model.Recording{{
			ID: "king-2020", Work: "the-kings-jest", Language: "en",
			Narrators: []string{"stephen-king"}, License: "CC0-1.0",
		}},
	}
	series := &model.Series{
		ID: "the-stormlight-archive", Name: "The Stormlight Archive", License: "CC0-1.0",
		Authors: []string{"brandon-sanderson"},
		Works:   []model.SeriesWork{{Work: "the-way-of-kings", Position: "1"}},
	}
	return &model.Catalog{
		Works:  []*model.Work{phm, wok, kings},
		People: []*model.Person{weir, porter, bray, sando, kramer, reading, king},
		Series: []*model.Series{series},
	}
}

func TestABSEmptyQuery400(t *testing.T) {
	base := absServer(t, absFixture())
	if code, _ := getJSON(t, base, "/abs/search"); code != 400 {
		t.Errorf("missing query: status %d, want 400", code)
	}
	if code, _ := getJSON(t, base, "/abs/search?query=%20%20"); code != 400 {
		t.Errorf("blank query: status %d, want 400", code)
	}
	// mediaType is accepted/ignored; a real query still succeeds.
	if code, _ := absMatches(t, base, "/abs/search?mediaType=book&query=hail"); code != 200 {
		t.Errorf("valid query: status %d, want 200", code)
	}
}

func TestABSNoMatch(t *testing.T) {
	base := absServer(t, absFixture())
	code, matches := absMatches(t, base, "/abs/search?query=zzznotathing")
	if code != 200 {
		t.Fatalf("status %d, want 200", code)
	}
	if len(matches) != 0 {
		t.Errorf("expected empty matches, got %v", matches)
	}
}

func TestABSTitleSearch(t *testing.T) {
	base := absServer(t, absFixture())
	code, matches := absMatches(t, base, "/abs/search?query=hail+mary")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	// Two recordings of Project Hail Mary => two matches (one per recording,
	// ordered by recording id). Locate each by narrator rather than assuming an
	// order, then assert per-recording fields.
	if len(matches) != 2 {
		t.Fatalf("matches = %d, want 2 (one per recording)", len(matches))
	}
	byNarrator := map[string]map[string]any{}
	for _, mm := range matches {
		m := mm.(map[string]any)
		byNarrator[m["narrator"].(string)] = m
	}
	m := byNarrator["Ray Porter"]
	if m == nil {
		t.Fatalf("no Ray Porter recording in %v", matches)
	}
	// Shared work-level fields.
	if m["title"] != "Project Hail Mary" {
		t.Errorf("title = %v", m["title"])
	}
	if m["subtitle"] != "A Novel" {
		t.Errorf("subtitle = %v", m["subtitle"])
	}
	if m["author"] != "Andy Weir" {
		t.Errorf("author = %v", m["author"])
	}
	// publishedYear is a STRING, not a number.
	if m["publishedYear"] != "2021" {
		t.Errorf("publishedYear = %v (type %T)", m["publishedYear"], m["publishedYear"])
	}
	if m["language"] != "en" {
		t.Errorf("language = %v", m["language"])
	}
	if m["description"] != "A lone astronaut must save the earth." {
		t.Errorf("description = %v", m["description"])
	}
	// The ray-porter recording: duration in MINUTES, us-preferred ASIN, cover, publisher, isbn.
	if m["duration"].(float64) != 970 {
		t.Errorf("duration = %v, want 970 (minutes)", m["duration"])
	}
	if m["asin"] != "B08G9PRS1K" {
		t.Errorf("asin = %v, want us-region B08G9PRS1K", m["asin"])
	}
	if m["cover"] != "https://example.test/phm.jpg" {
		t.Errorf("cover = %v", m["cover"])
	}
	if m["publisher"] != "Audible Studios" {
		t.Errorf("publisher = %v", m["publisher"])
	}
	if m["isbn"] != "9780593135204" {
		t.Errorf("isbn = %v", m["isbn"])
	}
	// The uk-only recording falls back to the uk ASIN and has no cover/isbn.
	m2 := byNarrator["Rosario Dawson"]
	if m2 == nil {
		t.Fatalf("no Rosario Dawson recording in %v", matches)
	}
	if m2["asin"] != "B09UKONLY0" {
		t.Errorf("uk-only recording asin = %v, want uk fallback", m2["asin"])
	}
	if m2["duration"].(float64) != 965 {
		t.Errorf("uk-only recording duration = %v", m2["duration"])
	}
	if _, has := m2["cover"]; has {
		t.Errorf("uk-only recording should omit empty cover")
	}
	if _, has := m2["isbn"]; has {
		t.Errorf("uk-only recording should omit empty isbn")
	}
	// genres/tags are never emitted (omitted, not null).
	if _, has := m["genres"]; has {
		t.Errorf("genres should be omitted")
	}
	if _, has := m["tags"]; has {
		t.Errorf("tags should be omitted")
	}
}

func TestABSCommaJoinedAndSeries(t *testing.T) {
	base := absServer(t, absFixture())
	code, matches := absMatches(t, base, "/abs/search?query=way+of+kings")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	// Find the way-of-kings match (both "way of kings" and "kings jest" may hit).
	var wok map[string]any
	for _, mm := range matches {
		m := mm.(map[string]any)
		if m["title"] == "The Way of Kings" {
			wok = m
		}
	}
	if wok == nil {
		t.Fatalf("The Way of Kings not in matches: %v", matches)
	}
	// Dual narrators are comma-joined in order.
	if wok["narrator"] != "Michael Kramer, Kate Reading" {
		t.Errorf("narrator = %v", wok["narrator"])
	}
	// Series maps to [{series, sequence}] with a STRING sequence.
	ser := wok["series"].([]any)
	if len(ser) != 1 {
		t.Fatalf("series = %v", ser)
	}
	s0 := ser[0].(map[string]any)
	if s0["series"] != "The Stormlight Archive" || s0["sequence"] != "1" {
		t.Errorf("series entry = %v", s0)
	}
	// A work with no series omits the key.
	code, matches = absMatches(t, base, "/abs/search?query=hail")
	if code != 200 {
		t.Fatal(code)
	}
	if _, has := matches[0].(map[string]any)["series"]; has {
		t.Errorf("seriesless work should omit series")
	}
}

func TestABSISBNExactHit(t *testing.T) {
	base := absServer(t, absFixture())
	// ISBN belongs to The Way of Kings; a mismatched query must not override the
	// exact identifier hit.
	code, matches := absMatches(t, base, "/abs/search?isbn=9781427209269&query=completely+unrelated")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	if len(matches) != 1 {
		t.Fatalf("matches = %d, want 1", len(matches))
	}
	m := matches[0].(map[string]any)
	if m["title"] != "The Way of Kings" {
		t.Errorf("isbn hit title = %v", m["title"])
	}
	if m["isbn"] != "9781427209269" {
		t.Errorf("isbn = %v", m["isbn"])
	}
}

// TestABSISBNPreferredFirst: an ISBN that belongs to the FIRST recording of a
// two-recording work returns both, that recording first.
func TestABSISBNPreferredFirst(t *testing.T) {
	base := absServer(t, absFixture())
	// 9780593135204 is on ray-porter-2021 (the first recording already), so also
	// verify the multi-recording work returns both recordings on an ISBN hit.
	code, matches := absMatches(t, base, "/abs/search?isbn=9780593135204&query=x")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	if len(matches) != 2 {
		t.Fatalf("matches = %d, want 2 recordings of the work", len(matches))
	}
	if matches[0].(map[string]any)["asin"] != "B08G9PRS1K" {
		t.Errorf("preferred (isbn-bearing) recording not first: %v", matches[0])
	}
}

func TestABSISBNMissFallsBackToTitle(t *testing.T) {
	base := absServer(t, absFixture())
	// An unknown ISBN plus a real title must still find the book via FTS.
	code, matches := absMatches(t, base, "/abs/search?isbn=0000000000000&query=hail")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	if len(matches) == 0 || matches[0].(map[string]any)["title"] != "Project Hail Mary" {
		t.Errorf("isbn miss did not fall back to title search: %v", matches)
	}
}

func TestABSAuthorRanking(t *testing.T) {
	base := absServer(t, absFixture())
	// "kings" matches both The Way of Kings (Sanderson) and The Kings Jest (King).
	// author=Sanderson must rank the Sanderson work's recording first.
	code, matches := absMatches(t, base, "/abs/search?query=kings&author=Brandon+Sanderson")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	if len(matches) < 2 {
		t.Fatalf("expected both king-titled works, got %v", matches)
	}
	if matches[0].(map[string]any)["title"] != "The Way of Kings" {
		t.Errorf("author ranking failed, first = %v", matches[0].(map[string]any)["title"])
	}

	// The other author boosts the other work.
	_, matches = absMatches(t, base, "/abs/search?query=kings&author=Stephen+King")
	if matches[0].(map[string]any)["title"] != "The Kings Jest" {
		t.Errorf("author=King should rank Kings Jest first, got %v", matches[0].(map[string]any)["title"])
	}
}

// ---- pure-function unit tests ----------------------------------------------

func TestAuthorMatches(t *testing.T) {
	cases := []struct {
		names  []string
		author string
		want   bool
	}{
		{[]string{"Brandon Sanderson"}, "Sanderson", true},         // substring
		{[]string{"Brandon Sanderson"}, "brandon sanderson", true}, // full, case-insensitive
		{[]string{"Brandon Sanderson"}, "Sando Brandon", true},     // token overlap ("brandon")
		{[]string{"Andy Weir"}, "Stephen King", false},             // no overlap
		{[]string{"Andy Weir"}, "", false},                         // empty author
		{nil, "Weir", false},                                       // no names
		{[]string{"J. K. Rowling"}, "rowling", true},               // substring within
	}
	for _, c := range cases {
		if got := authorMatches(c.names, c.author); got != c.want {
			t.Errorf("authorMatches(%v, %q) = %v, want %v", c.names, c.author, got, c.want)
		}
	}
}

func TestPickASIN(t *testing.T) {
	if got := pickASIN(nil); got != "" {
		t.Errorf("empty = %q", got)
	}
	if got := pickASIN([]asinRef{{Region: "uk", ASIN: "UK1"}, {Region: "us", ASIN: "US1"}}); got != "US1" {
		t.Errorf("us-preferred = %q, want US1", got)
	}
	if got := pickASIN([]asinRef{{Region: "de", ASIN: "DE1"}, {Region: "uk", ASIN: "UK1"}}); got != "DE1" {
		t.Errorf("no-us fallback = %q, want first DE1", got)
	}
}

// TestAbsBooksForMapping exercises the pure mapper directly: work-only fallback,
// work-ISBN fallback when a recording has none, and comma-joining.
func TestAbsBooksForMapping(t *testing.T) {
	// Work with no recordings falls back to a single work-only book carrying the
	// work print ISBN.
	d := &workDetail{
		ID: "w", Title: "Solo", Language: "en", FirstPublished: "1999",
		Authors: []personRef{{ID: "a", Name: "A One"}, {ID: "b", Name: "B Two"}},
		Xref:    &workXref{ISBN: []string{"111"}},
	}
	books := absBooksFor(d, "")
	if len(books) != 1 {
		t.Fatalf("work-only books = %d, want 1", len(books))
	}
	if books[0].Author != "A One, B Two" {
		t.Errorf("author join = %q", books[0].Author)
	}
	if books[0].ISBN != "111" {
		t.Errorf("work-only isbn fallback = %q", books[0].ISBN)
	}
	if books[0].Duration != 0 {
		t.Errorf("work-only duration = %d", books[0].Duration)
	}

	// A recording with no ISBN of its own inherits the work print ISBN.
	d.Recordings = []recordingDetail{
		{ID: "r1", RuntimeMin: 60, Narrators: []personRef{{Name: "Nar One"}}},
	}
	books = absBooksFor(d, "")
	if books[0].ISBN != "111" {
		t.Errorf("recording isbn fallback = %q, want work isbn", books[0].ISBN)
	}
	if books[0].Duration != 60 {
		t.Errorf("duration = %d", books[0].Duration)
	}
	if books[0].Narrator != "Nar One" {
		t.Errorf("narrator = %q", books[0].Narrator)
	}
}
