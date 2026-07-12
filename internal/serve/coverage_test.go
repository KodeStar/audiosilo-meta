package serve

import (
	"database/sql"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/kodestar/audiosilo-meta/internal/model"
)

// coverageCatalog exercises the missing-list logic: a fully-covered work, a
// partially-covered work (characters only), two bare standalone works, and a
// bare work that belongs to two series (so the "first series by id" pick is
// tested). Series membership drives the deterministic ordering.
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

func TestCoverageMissingAndTotals(t *testing.T) {
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

	missing := body["missing"].([]any)
	var ids []string
	for _, m := range missing {
		ids = append(ids, m.(map[string]any)["id"].(string))
	}
	// Series works first (grouped by series name: Aaa < Zeta), then standalone
	// works by title (Delta < Gamma). alpha-covered is fully covered => absent.
	want := []string{"multi", "beta-partial", "delta-standalone", "gamma-bare"}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("missing order = %v, want %v", ids, want)
	}

	beta := missing[1].(map[string]any)
	betaMissing := toStrings(beta["missing"].([]any))
	if !reflect.DeepEqual(betaMissing, []string{"recaps", "recap_summary"}) {
		t.Errorf("beta missing = %v, want [recaps recap_summary]", betaMissing)
	}

	m := missing[0].(map[string]any) // multi
	series := m["series"].(map[string]any)
	if series["id"] != "aaa-series" {
		t.Errorf("multi series = %v, want aaa-series (first by id)", series["id"])
	}
	authors := toStrings(m["authors"].([]any))
	if !reflect.DeepEqual(authors, []string{"A Author"}) {
		t.Errorf("multi authors = %v", authors)
	}
	if mm := toStrings(m["missing"].([]any)); !reflect.DeepEqual(mm, []string{"characters", "recaps", "recap_summary"}) {
		t.Errorf("multi missing = %v", mm)
	}

	// A standalone missing work omits the series key entirely.
	gamma := missing[3].(map[string]any)
	if _, has := gamma["series"]; has {
		t.Errorf("standalone work should omit series, got %v", gamma["series"])
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

func TestCoverageSeriesGaps(t *testing.T) {
	ts := serverFor(t, gapCatalog())
	code, body := getJSON(t, ts.URL, "/api/v1/coverage")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	gaps := body["series_gaps"].([]any)

	type want struct {
		id      string
		present []string
		missing []int
	}
	// Sorted by series id; sg-single and sg-nogap are omitted (no interior gap).
	wants := []want{
		{"sg-decimal", []string{"1", "2.5", "3"}, []int{2}},
		{"sg-int", []string{"1", "2", "5"}, []int{3, 4}},
		{"sg-range", []string{"1-3", "5"}, []int{4}},
	}
	if len(gaps) != len(wants) {
		t.Fatalf("series_gaps = %d entries, want %d: %v", len(gaps), len(wants), gaps)
	}
	for i, w := range wants {
		g := gaps[i].(map[string]any)
		if g["id"] != w.id {
			t.Fatalf("gap[%d] id = %v, want %v", i, g["id"], w.id)
		}
		if got := toStrings(g["present"].([]any)); !reflect.DeepEqual(got, w.present) {
			t.Errorf("%s present = %v, want %v", w.id, got, w.present)
		}
		if got := toInts(g["missing_positions"].([]any)); !reflect.DeepEqual(got, w.missing) {
			t.Errorf("%s missing_positions = %v, want %v", w.id, got, w.missing)
		}
	}
}

// TestCoverageDegradesV1 simulates a newer binary serving a pre-sidecar (schema
// version 1) artifact: sidecar totals are 0, the missing list is omitted
// entirely (coverage unknowable), but series_gaps is still computed.
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
	if _, has := body["missing"]; has {
		t.Errorf("v1 artifact must omit the missing list, got %v", body["missing"])
	}
	totals := body["totals"].(map[string]any)
	for _, k := range []string{"with_characters", "with_recaps", "with_recap_summary"} {
		if got, _ := totals[k].(float64); got != 0 {
			t.Errorf("v1 totals[%s] = %v, want 0", k, got)
		}
	}
	if got, _ := totals["works"].(float64); got != 4 {
		t.Errorf("v1 totals[works] = %v, want 4", got)
	}
	// series_gaps still computed from v1 data: stormlight has 1,2,10 => 3..9.
	gaps := body["series_gaps"].([]any)
	if len(gaps) != 1 {
		t.Fatalf("v1 series_gaps = %v", gaps)
	}
	g := gaps[0].(map[string]any)
	if g["id"] != "the-stormlight-archive" {
		t.Errorf("gap id = %v", g["id"])
	}
	if got := toInts(g["missing_positions"].([]any)); !reflect.DeepEqual(got, []int{3, 4, 5, 6, 7, 8, 9}) {
		t.Errorf("stormlight gaps = %v", got)
	}
}

// TestCoverageDegradesV2 simulates serving a schema-version-2 artifact (has
// characters/recaps, lacks recap_summaries): recap_summary is neither counted
// nor treated as a missing dimension, so a work with characters + recaps (but no
// summary) is fully covered and absent from the missing list.
func TestCoverageDegradesV2(t *testing.T) {
	dbPath := buildFixtureDB(t, fixtureCatalog(), nil)
	rollbackSchema(t, dbPath, 2, "DROP TABLE recap_summaries")

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
	if got, _ := totals["with_characters"].(float64); got != 1 {
		t.Errorf("v2 with_characters = %v, want 1", got)
	}
	if got, _ := totals["with_recaps"].(float64); got != 1 {
		t.Errorf("v2 with_recaps = %v, want 1", got)
	}
	if got, _ := totals["with_recap_summary"].(float64); got != 0 {
		t.Errorf("v2 with_recap_summary = %v, want 0 (table absent)", got)
	}

	missing := body["missing"].([]any)
	for _, m := range missing {
		row := m.(map[string]any)
		if row["id"] == "project-hail-mary" {
			t.Errorf("v2: work with characters+recaps must not be missing")
		}
		// No row lists recap_summary as missing at v2.
		for _, dim := range toStrings(row["missing"].([]any)) {
			if dim == "recap_summary" {
				t.Errorf("v2 must not report recap_summary as missing: %v", row)
			}
		}
	}
	// The three bare fixture works are missing exactly characters+recaps.
	if len(missing) != 3 {
		t.Errorf("v2 missing = %d works, want 3", len(missing))
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
