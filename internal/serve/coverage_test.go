package serve

import (
	"database/sql"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/kodestar/audiosilo-meta/internal/model"
)

// coverageCatalog exercises the coverage browser: a fully-covered work, a
// partially-covered work (characters only), two bare standalone works, and a
// bare work that belongs to two series (so the "first series by id" pick is
// tested).
func coverageCatalog() *model.Catalog {
	auth := &model.Person{ID: "a-author", Name: "A Author", License: "CC0-1.0"}

	mkWork := func(id, title string) *model.Work {
		return &model.Work{
			ID: id, Title: title, Language: "en",
			Authors: []string{"a-author"}, License: "CC0-1.0",
		}
	}

	alpha := mkWork("alpha-covered", "Alpha Covered")
	beta := mkWork("beta-partial", "Beta Partial")
	gamma := mkWork("gamma-bare", "Gamma Bare")
	delta := mkWork("delta-standalone", "Delta Standalone")
	multi := mkWork("multi", "Multi")

	chars := func(work string) *model.Characters {
		return &model.Characters{
			Work: work, License: "CC-BY-SA-3.0",
			Sources: []model.Source{{Type: "community"}},
			Characters: []model.Character{
				{ID: "someone", Name: "Someone", Reveal: model.Position{Chapter: 1}},
			},
		}
	}
	// alpha is fully covered: characters + recaps + a whole-book summary.
	alphaRecaps := &model.Recaps{
		Work: "alpha-covered", License: "CC-BY-SA-3.0",
		Sources: []model.Source{{Type: "community"}},
		InShort: "The whole arc in one paragraph.",
		Ending:  "It ends well.",
		Recaps: []model.Recap{
			{Through: model.Position{Chapter: 3}, Text: "So far so good."},
		},
	}

	return &model.Catalog{
		People: []*model.Person{auth},
		Works:  []*model.Work{alpha, beta, gamma, delta, multi},
		Series: []*model.Series{
			{
				ID: "zeta-series", Name: "Zeta Series", License: "CC0-1.0",
				Authors: []string{"a-author"},
				Works: []model.SeriesWork{
					{Work: "alpha-covered", Position: "1"},
					{Work: "beta-partial", Position: "2"},
				},
			},
			{
				ID: "aaa-series", Name: "Aaa Series", License: "CC0-1.0",
				Authors: []string{"a-author"},
				Works:   []model.SeriesWork{{Work: "multi", Position: "3"}},
			},
			{
				ID: "mmm-series", Name: "Mmm Series", License: "CC0-1.0",
				Authors: []string{"a-author"},
				Works:   []model.SeriesWork{{Work: "multi", Position: "1"}},
			},
		},
		Characters: []*model.Characters{chars("alpha-covered"), chars("beta-partial")},
		Recaps:     []*model.Recaps{alphaRecaps},
	}
}

func serverFor(t *testing.T, cat *model.Catalog) *httptest.Server {
	t.Helper()
	dbPath := buildFixtureDB(t, cat, nil)
	srv, err := New(Config{DBPath: dbPath, swapGrace: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

// workIDs pulls the "works" array from a /coverage/works body into an id slice.
func workIDs(body map[string]any) []string {
	works, _ := body["works"].([]any)
	ids := make([]string, 0, len(works))
	for _, w := range works {
		ids = append(ids, w.(map[string]any)["id"].(string))
	}
	return ids
}

func TestCoverageTotals(t *testing.T) {
	ts := serverFor(t, coverageCatalog())
	code, body := getJSON(t, ts.URL, "/api/v1/coverage")
	if code != 200 {
		t.Fatalf("status %d", code)
	}

	totals := body["totals"].(map[string]any)
	wantTotals := map[string]float64{
		"works": 5, "with_characters": 2, "with_recaps": 1, "with_recap_summary": 1,
	}
	for k, v := range wantTotals {
		if got, _ := totals[k].(float64); got != v {
			t.Errorf("totals[%s] = %v, want %v", k, totals[k], v)
		}
	}
	// The heavy lists moved to their own paginated endpoints; the band payload
	// must not carry them regardless of catalogue size.
	if _, has := body["missing"]; has {
		t.Errorf("/coverage must not embed the missing list, got %v", body["missing"])
	}
	if _, has := body["series_gaps"]; has {
		t.Errorf("/coverage must not embed series_gaps, got %v", body["series_gaps"])
	}
}

func TestCoverageWorksMissing(t *testing.T) {
	ts := serverFor(t, coverageCatalog())
	code, body := getJSON(t, ts.URL, "/api/v1/coverage/works?filter=missing")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	if body["available"] != true {
		t.Errorf("available = %v, want true", body["available"])
	}
	if got, _ := body["total"].(float64); got != 4 {
		t.Errorf("total = %v, want 4", got)
	}
	// alpha-covered is fully covered => absent. Flat order is title then id:
	// Beta Partial < Delta Standalone < Gamma Bare < Multi.
	want := []string{"beta-partial", "delta-standalone", "gamma-bare", "multi"}
	if got := workIDs(body); !reflect.DeepEqual(got, want) {
		t.Fatalf("missing order = %v, want %v", got, want)
	}

	works := body["works"].([]any)
	beta := works[0].(map[string]any)
	if m := toStrings(beta["missing"].([]any)); !reflect.DeepEqual(m, []string{"recaps", "recap_summary"}) {
		t.Errorf("beta missing = %v, want [recaps recap_summary]", m)
	}
	// beta-partial belongs to zeta-series; the row carries its series ref.
	if s, ok := beta["series"].(map[string]any); !ok || s["id"] != "zeta-series" {
		t.Errorf("beta series = %v, want zeta-series", beta["series"])
	}
	// Authors are {id,name} personRefs, the shape used everywhere else.
	a := beta["authors"].([]any)[0].(map[string]any)
	if a["id"] != "a-author" || a["name"] != "A Author" {
		t.Errorf("beta authors[0] = %v, want {a-author, A Author}", a)
	}

	multi := works[3].(map[string]any)
	if s := multi["series"].(map[string]any); s["id"] != "aaa-series" {
		t.Errorf("multi series = %v, want aaa-series (first by id)", s["id"])
	}
	// A standalone missing work omits the series key entirely.
	gamma := works[2].(map[string]any)
	if _, has := gamma["series"]; has {
		t.Errorf("standalone work should omit series, got %v", gamma["series"])
	}
}

func TestCoverageWorksHasFilters(t *testing.T) {
	ts := serverFor(t, coverageCatalog())

	// has_characters: alpha-covered + beta-partial (title order).
	_, body := getJSON(t, ts.URL, "/api/v1/coverage/works?filter=has_characters")
	if got := workIDs(body); !reflect.DeepEqual(got, []string{"alpha-covered", "beta-partial"}) {
		t.Errorf("has_characters = %v, want [alpha-covered beta-partial]", got)
	}
	// A fully-covered work in a "has" view lists no remaining gaps.
	alpha := body["works"].([]any)[0].(map[string]any)
	if m := toStrings(alpha["missing"].([]any)); len(m) != 0 {
		t.Errorf("alpha missing = %v, want []", m)
	}
	// A partially-covered work still advertises what it lacks.
	beta := body["works"].([]any)[1].(map[string]any)
	if m := toStrings(beta["missing"].([]any)); !reflect.DeepEqual(m, []string{"recaps", "recap_summary"}) {
		t.Errorf("beta missing = %v, want [recaps recap_summary]", m)
	}

	// has_recaps / has_recap_summary: only alpha-covered.
	for _, f := range []string{"has_recaps", "has_recap_summary"} {
		_, body := getJSON(t, ts.URL, "/api/v1/coverage/works?filter="+f)
		if got := workIDs(body); !reflect.DeepEqual(got, []string{"alpha-covered"}) {
			t.Errorf("%s = %v, want [alpha-covered]", f, got)
		}
	}
}

func TestCoverageWorksPagination(t *testing.T) {
	ts := serverFor(t, coverageCatalog())

	_, body := getJSON(t, ts.URL, "/api/v1/coverage/works?filter=missing&limit=2&offset=0")
	if got, _ := body["total"].(float64); got != 4 {
		t.Errorf("total = %v, want 4", got)
	}
	if got, _ := body["limit"].(float64); got != 2 {
		t.Errorf("limit = %v, want 2", got)
	}
	if got := workIDs(body); !reflect.DeepEqual(got, []string{"beta-partial", "delta-standalone"}) {
		t.Errorf("page 1 = %v", got)
	}

	_, body = getJSON(t, ts.URL, "/api/v1/coverage/works?filter=missing&limit=2&offset=2")
	if got := workIDs(body); !reflect.DeepEqual(got, []string{"gamma-bare", "multi"}) {
		t.Errorf("page 2 = %v", got)
	}
	if got, _ := body["offset"].(float64); got != 2 {
		t.Errorf("offset = %v, want 2", got)
	}

	// Offset past the end returns an empty page but the true total.
	_, body = getJSON(t, ts.URL, "/api/v1/coverage/works?filter=missing&offset=10")
	if got := workIDs(body); len(got) != 0 {
		t.Errorf("over-offset page = %v, want empty", got)
	}
	if got, _ := body["total"].(float64); got != 4 {
		t.Errorf("over-offset total = %v, want 4", got)
	}
}

func TestCoverageWorksSearch(t *testing.T) {
	ts := serverFor(t, coverageCatalog())

	// Title substring, case-insensitive.
	_, body := getJSON(t, ts.URL, "/api/v1/coverage/works?filter=missing&q=MULTI")
	if got := workIDs(body); !reflect.DeepEqual(got, []string{"multi"}) {
		t.Errorf("q=MULTI = %v, want [multi]", got)
	}
	if got, _ := body["total"].(float64); got != 1 {
		t.Errorf("q=MULTI total = %v, want 1", got)
	}

	// Author substring matches every work by that author.
	_, body = getJSON(t, ts.URL, "/api/v1/coverage/works?filter=missing&q=Author")
	if got, _ := body["total"].(float64); got != 4 {
		t.Errorf("q=Author total = %v, want 4", got)
	}

	// A LIKE metacharacter is matched literally, not as a wildcard.
	_, body = getJSON(t, ts.URL, "/api/v1/coverage/works?filter=missing&q=%25")
	if got, _ := body["total"].(float64); got != 0 {
		t.Errorf("q=%%%% total = %v, want 0 (literal match)", got)
	}
}

func TestCoverageWorksUnknownFilter(t *testing.T) {
	ts := serverFor(t, coverageCatalog())
	code, _ := getJSON(t, ts.URL, "/api/v1/coverage/works?filter=bogus")
	if code != 400 {
		t.Errorf("unknown filter status = %d, want 400", code)
	}
}

// gapCatalog builds series that exercise every gap-parsing branch: plain-integer
// gap, a decimal that does not fill a slot, an omnibus range that does fill its
// integers, a single-member series (no interior), and a contiguous no-gap series.
func gapCatalog() *model.Catalog {
	auth := &model.Person{ID: "gap-author", Name: "Gap Author", License: "CC0-1.0"}
	var works []*model.Work
	mk := func(id string) string {
		works = append(works, &model.Work{
			ID: id, Title: id, Language: "en",
			Authors: []string{"gap-author"}, License: "CC0-1.0",
		})
		return id
	}
	series := []*model.Series{
		{ID: "sg-int", Name: "SG Int", License: "CC0-1.0", Authors: []string{"gap-author"}, Works: []model.SeriesWork{
			{Work: mk("si1"), Position: "1"}, {Work: mk("si2"), Position: "2"}, {Work: mk("si5"), Position: "5"},
		}},
		{ID: "sg-decimal", Name: "SG Decimal", License: "CC0-1.0", Authors: []string{"gap-author"}, Works: []model.SeriesWork{
			{Work: mk("sd1"), Position: "1"}, {Work: mk("sd25"), Position: "2.5"}, {Work: mk("sd3"), Position: "3"},
		}},
		{ID: "sg-range", Name: "SG Range", License: "CC0-1.0", Authors: []string{"gap-author"}, Works: []model.SeriesWork{
			{Work: mk("sr13"), Position: "1-3"}, {Work: mk("sr5"), Position: "5"},
		}},
		{ID: "sg-single", Name: "SG Single", License: "CC0-1.0", Authors: []string{"gap-author"}, Works: []model.SeriesWork{
			{Work: mk("ss1"), Position: "1"},
		}},
		{ID: "sg-nogap", Name: "SG NoGap", License: "CC0-1.0", Authors: []string{"gap-author"}, Works: []model.SeriesWork{
			{Work: mk("sn1"), Position: "1"}, {Work: mk("sn2"), Position: "2"}, {Work: mk("sn3"), Position: "3"},
		}},
	}
	return &model.Catalog{People: []*model.Person{auth}, Works: works, Series: series}
}

func gapIDs(body map[string]any) []string {
	gaps, _ := body["gaps"].([]any)
	ids := make([]string, 0, len(gaps))
	for _, g := range gaps {
		ids = append(ids, g.(map[string]any)["id"].(string))
	}
	return ids
}

func TestCoverageSeriesGaps(t *testing.T) {
	ts := serverFor(t, gapCatalog())
	code, body := getJSON(t, ts.URL, "/api/v1/coverage/series-gaps")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	if got, _ := body["total"].(float64); got != 3 {
		t.Errorf("total = %v, want 3", got)
	}
	// Sorted by name; sg-single and sg-nogap are omitted (no interior gap).
	// Names: "SG Decimal" < "SG Int" < "SG Range".
	if got := gapIDs(body); !reflect.DeepEqual(got, []string{"sg-decimal", "sg-int", "sg-range"}) {
		t.Fatalf("gap order = %v", got)
	}

	gaps := body["gaps"].([]any)
	type want struct {
		id      string
		present []string
		missing []int
	}
	wants := []want{
		{"sg-decimal", []string{"1", "2.5", "3"}, []int{2}},
		{"sg-int", []string{"1", "2", "5"}, []int{3, 4}},
		{"sg-range", []string{"1-3", "5"}, []int{4}},
	}
	for i, w := range wants {
		g := gaps[i].(map[string]any)
		if got := toStrings(g["present"].([]any)); !reflect.DeepEqual(got, w.present) {
			t.Errorf("%s present = %v, want %v", w.id, got, w.present)
		}
		if got := toInts(g["missing_positions"].([]any)); !reflect.DeepEqual(got, w.missing) {
			t.Errorf("%s missing_positions = %v, want %v", w.id, got, w.missing)
		}
	}
}

func TestCoverageSeriesGapsPageAndSearch(t *testing.T) {
	ts := serverFor(t, gapCatalog())

	// Second page of one item.
	_, body := getJSON(t, ts.URL, "/api/v1/coverage/series-gaps?limit=1&offset=1")
	if got := gapIDs(body); !reflect.DeepEqual(got, []string{"sg-int"}) {
		t.Errorf("page = %v, want [sg-int]", got)
	}
	if got, _ := body["total"].(float64); got != 3 {
		t.Errorf("total = %v, want 3 (unpaged)", got)
	}

	// Name search, case-insensitive.
	_, body = getJSON(t, ts.URL, "/api/v1/coverage/series-gaps?q=RANGE")
	if got := gapIDs(body); !reflect.DeepEqual(got, []string{"sg-range"}) {
		t.Errorf("q=RANGE = %v, want [sg-range]", got)
	}
	if got, _ := body["total"].(float64); got != 1 {
		t.Errorf("q=RANGE total = %v, want 1", got)
	}
}

// TestCoverageDegradesV1 simulates a newer binary serving a pre-sidecar (schema
// version 1) artifact: the sidecar totals are omitted, list_available is false,
// and the works browser reports available:false with no rows - but series_gaps
// is still computed.
func TestCoverageDegradesV1(t *testing.T) {
	dbPath := buildFixtureDB(t, fixtureCatalog(), nil)
	rollbackSchema(t, dbPath, 1,
		"DROP TABLE characters", "DROP TABLE character_aliases",
		"DROP TABLE recaps", "DROP TABLE recap_summaries")

	srv, err := New(Config{DBPath: dbPath, swapGrace: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	code, body := getJSON(t, ts.URL, "/api/v1/coverage")
	if code != 200 {
		t.Fatalf("status %d, body %v", code, body)
	}
	totals := body["totals"].(map[string]any)
	for _, k := range []string{"with_characters", "with_recaps", "with_recap_summary"} {
		if v, has := totals[k]; has {
			t.Errorf("v1 totals[%s] must be omitted, got %v", k, v)
		}
	}
	if got, _ := totals["works"].(float64); got != 4 {
		t.Errorf("v1 totals[works] = %v, want 4", got)
	}

	// The works browser is unavailable at v1: an empty page, available:false.
	_, wb := getJSON(t, ts.URL, "/api/v1/coverage/works?filter=missing")
	if wb["available"] != false {
		t.Errorf("v1 works available = %v, want false", wb["available"])
	}
	if got := workIDs(wb); len(got) != 0 {
		t.Errorf("v1 works = %v, want empty", got)
	}

	// series_gaps still computed from v1 data: stormlight has 1,2,10 => 3..9.
	_, gaps := getJSON(t, ts.URL, "/api/v1/coverage/series-gaps")
	if got := gapIDs(gaps); !reflect.DeepEqual(got, []string{"the-stormlight-archive"}) {
		t.Fatalf("v1 gaps = %v", got)
	}
	g := gaps["gaps"].([]any)[0].(map[string]any)
	if got := toInts(g["missing_positions"].([]any)); !reflect.DeepEqual(got, []int{3, 4, 5, 6, 7, 8, 9}) {
		t.Errorf("stormlight gaps = %v", got)
	}
}

// TestCoverageDegradesV2 simulates serving a schema-version-2 artifact (has
// characters/recaps, lacks recap_summaries): the recap_summary total is omitted,
// it is not a missing dimension, and a "has_recap_summary" filter is
// unavailable - but characters/recaps filters work normally.
func TestCoverageDegradesV2(t *testing.T) {
	dbPath := buildFixtureDB(t, fixtureCatalog(), nil)
	rollbackSchema(t, dbPath, 2, "DROP TABLE recap_summaries")

	srv, err := New(Config{DBPath: dbPath, swapGrace: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	_, body := getJSON(t, ts.URL, "/api/v1/coverage")
	totals := body["totals"].(map[string]any)
	if got, _ := totals["with_characters"].(float64); got != 1 {
		t.Errorf("v2 with_characters = %v, want 1", got)
	}
	if got, _ := totals["with_recaps"].(float64); got != 1 {
		t.Errorf("v2 with_recaps = %v, want 1", got)
	}
	if v, has := totals["with_recap_summary"]; has {
		t.Errorf("v2 with_recap_summary must be omitted (table absent), got %v", v)
	}

	// The missing filter is evaluable; project-hail-mary (characters+recaps) is
	// covered, no row cites recap_summary, and the three bare works remain.
	_, wb := getJSON(t, ts.URL, "/api/v1/coverage/works?filter=missing")
	if got, _ := wb["total"].(float64); got != 3 {
		t.Errorf("v2 missing total = %v, want 3", got)
	}
	for _, w := range wb["works"].([]any) {
		row := w.(map[string]any)
		if row["id"] == "project-hail-mary" {
			t.Errorf("v2: work with characters+recaps must not be missing")
		}
		for _, dim := range toStrings(row["missing"].([]any)) {
			if dim == "recap_summary" {
				t.Errorf("v2 must not report recap_summary as missing: %v", row)
			}
		}
	}

	// The recap-summary filter is unavailable (table absent).
	_, hs := getJSON(t, ts.URL, "/api/v1/coverage/works?filter=has_recap_summary")
	if hs["available"] != false {
		t.Errorf("v2 has_recap_summary available = %v, want false", hs["available"])
	}
}

func TestCoveredIntegers(t *testing.T) {
	cases := []struct {
		in   string
		want []int
	}{
		{"2", []int{2}},
		{"10", []int{10}},
		{"1", []int{1}},
		{"1-3", []int{1, 2, 3}},
		{"1-3.5", []int{1, 2, 3}},
		{"2.5-4", []int{3, 4}},
		{"2.5", nil},     // bare decimal fills no integer slot
		{"2.5-2.9", nil}, // range spanning no integer
		{"", nil},        // empty
		{"abc", nil},     // unparseable
		{"3-1", nil},     // inverted range
	}
	for _, c := range cases {
		if got := coveredIntegers(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("coveredIntegers(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// rollbackSchema mutates an artifact in place to look like an older schema
// version: it runs the given DROP statements and stamps meta.schema_version.
func rollbackSchema(t *testing.T, dbPath string, version int, drops ...string) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range drops {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec("UPDATE meta SET value=? WHERE key='schema_version'", version); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func toStrings(a []any) []string {
	out := make([]string, len(a))
	for i, v := range a {
		out[i] = v.(string)
	}
	return out
}

func toInts(a []any) []int {
	out := make([]int, len(a))
	for i, v := range a {
		out[i] = int(v.(float64))
	}
	return out
}
