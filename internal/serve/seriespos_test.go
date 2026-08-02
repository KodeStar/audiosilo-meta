package serve

import (
	"reflect"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// seriesPositionCatalog is the fixture for series-position search. It carries
// the motivating case (a numbered series), the adversarial cases (titles that
// ARE numbers, and a query that is both a title fragment and a position), a
// decimal and a range position, and a series whose own name ends in a volume
// word.
func seriesPositionCatalog() *model.Catalog {
	person := func(id, name string) *model.Person {
		return &model.Person{ID: id, Name: name, License: "CC0-1.0"}
	}
	work := func(id, title, author string) *model.Work {
		return &model.Work{
			ID: id, Title: title, Language: "en", License: "CC0-1.0",
			Authors: []string{author},
		}
	}
	return &model.Catalog{
		People: []*model.Person{
			person("lee-child", "Lee Child"),
			person("mick-herron", "Mick Herron"),
			person("ray-bradbury", "Ray Bradbury"),
			person("george-orwell", "George Orwell"),
			person("rudyard-kipling", "Rudyard Kipling"),
			person("eric-nylund", "Eric Nylund"),
		},
		Works: []*model.Work{
			work("killing-floor", "Killing Floor", "lee-child"),
			work("die-trying", "Die Trying", "lee-child"),
			work("deep-down", "Deep Down", "lee-child"),
			work("tripwire", "Tripwire", "lee-child"),
			work("reacher-omnibus", "Reacher Omnibus", "lee-child"),
			work("slow-horses", "Slow Horses", "mick-herron"),
			work("dead-lions", "Dead Lions", "mick-herron"),
			work("slow-horses-2-companion", "Slow Horses 2: The Companion", "mick-herron"),
			work("fahrenheit-451", "Fahrenheit 451", "ray-bradbury"),
			work("nineteen-eighty-four", "1984", "george-orwell"),
			work("the-jungle-book", "The Jungle Book", "rudyard-kipling"),
			work("the-second-jungle-book", "The Second Jungle Book", "rudyard-kipling"),
			work("the-jungle", "The Jungle", "rudyard-kipling"),
			work("the-jungle-returns", "The Jungle Returns", "rudyard-kipling"),
			work("halo-combat-evolved", "Halo: Combat Evolved", "eric-nylund"),
			work("halo-2", "Halo 2", "eric-nylund"),
		},
		Series: []*model.Series{
			{
				ID: "jack-reacher", Name: "Jack Reacher", License: "CC0-1.0",
				Authors: []string{"lee-child"},
				Works: []model.SeriesWork{
					{Work: "killing-floor", Position: "1"},
					{Work: "die-trying", Position: "2"},
					{Work: "deep-down", Position: "2.5"},
					{Work: "tripwire", Position: "3"},
					{Work: "reacher-omnibus", Position: "1-3.5"},
				},
			},
			{
				ID: "slow-horses", Name: "Slow Horses", License: "CC0-1.0",
				Authors: []string{"mick-herron"},
				Works: []model.SeriesWork{
					{Work: "slow-horses", Position: "1"},
					{Work: "dead-lions", Position: "2"},
				},
			},
			// Two overlapping names: a probe for "the jungle book" must reach the
			// series that IS "The Jungle Book", not the one called "The Jungle".
			{
				ID: "the-jungle-book", Name: "The Jungle Book", License: "CC0-1.0",
				Works: []model.SeriesWork{
					{Work: "the-jungle-book", Position: "1"},
					{Work: "the-second-jungle-book", Position: "2"},
				},
			},
			{
				ID: "the-jungle", Name: "The Jungle", License: "CC0-1.0",
				Works: []model.SeriesWork{
					{Work: "the-jungle", Position: "1"},
					{Work: "the-jungle-returns", Position: "2"},
				},
			},
			// The volume whose TITLE is the position query: "halo 2" is both an
			// FTS title hit and a series-position hit for the same work.
			{
				ID: "halo", Name: "Halo", License: "CC0-1.0",
				Works: []model.SeriesWork{
					{Work: "halo-combat-evolved", Position: "1"},
					{Work: "halo-2", Position: "2"},
				},
			},
		},
	}
}

// resultIDs renders a search page as "<kind>:<id>" strings, in rank order.
func resultIDs(t *testing.T, results []any) []string {
	t.Helper()
	out := make([]string, 0, len(results))
	for _, r := range results {
		switch v := r.(type) {
		case workResult:
			out = append(out, "work:"+v.ID)
		case personResult:
			out = append(out, "person:"+v.ID)
		case seriesResult:
			out = append(out, "series:"+v.ID)
		default:
			t.Fatalf("unexpected result type %T", r)
		}
	}
	return out
}

func searchIDs(t *testing.T, snap *snapshot, q string) []string {
	t.Helper()
	res, err := snap.search(q, 20)
	if err != nil {
		t.Fatalf("search(%q): %v", q, err)
	}
	return resultIDs(t, res)
}

func first(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	return ids[0]
}

func contains(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

// TestSearchSeriesPosition is the feature: a user who knows the series and the
// number finds the volume, in every spelling of the number, ranked first.
func TestSearchSeriesPosition(t *testing.T) {
	snap := snapshotFor(t, seriesPositionCatalog())

	cases := []struct {
		query string
		want  string
	}{
		{"jack reacher 2", "work:die-trying"},
		{"Jack Reacher 2", "work:die-trying"},
		{"jack reacher 02", "work:die-trying"},
		{"jack reacher book 2", "work:die-trying"},
		{"jack reacher band 2", "work:die-trying"},
		{"jack reacher vol. 2", "work:die-trying"},
		{"jack reacher #2", "work:die-trying"},
		// A prefix of the series name still resolves (the probe goes through
		// ftsQuery, which prefixes the last token).
		{"jack reach 2", "work:die-trying"},
		{"jack reacher 3", "work:tripwire"},
		// Decimal and range positions match on the stated string.
		{"jack reacher 2.5", "work:deep-down"},
		{"jack reacher 1-3.5", "work:reacher-omnibus"},
	}
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			ids := searchIDs(t, snap, tc.query)
			if first(ids) != tc.want {
				t.Errorf("search(%q) first = %q, want %q (page: %v)", tc.query, first(ids), tc.want, ids)
			}
		})
	}
}

// TestSearchSeriesPositionMisses proves the boost stays quiet when it cannot
// resolve: an unknown position, an unknown series, and a query with no number
// all return exactly the FTS page they always did.
func TestSearchSeriesPositionMisses(t *testing.T) {
	snap := snapshotFor(t, seriesPositionCatalog())

	// A position the series does not hold boosts nothing (and the FTS page for
	// this query is empty, as it was before the feature).
	if ids := searchIDs(t, snap, "jack reacher 99"); len(ids) != 0 {
		t.Errorf("search('jack reacher 99') = %v, want no results", ids)
	}
	// A series that does not exist boosts nothing.
	if hits, err := snap.seriesPositionHits("fahrenheit 451"); err != nil || hits != nil {
		t.Errorf("seriesPositionHits('fahrenheit 451') = %v, %v; want nil, nil", hits, err)
	}
	// No trailing number: the plain series query is untouched, still returning
	// the series record and its works.
	if hits, err := snap.seriesPositionHits("jack reacher"); err != nil || hits != nil {
		t.Errorf("seriesPositionHits('jack reacher') = %v, %v; want nil, nil", hits, err)
	}
	ids := searchIDs(t, snap, "jack reacher")
	if !contains(ids, "series:jack-reacher") {
		t.Errorf("search('jack reacher') lost the series result: %v", ids)
	}
	for _, want := range []string{"work:killing-floor", "work:die-trying", "work:tripwire"} {
		if !contains(ids, want) {
			t.Errorf("search('jack reacher') missing %s: %v", want, ids)
		}
	}
}

// TestSearchNumericTitlesNotRegressed is the adversarial half: a title that IS a
// number, a title that ENDS in a number, and a query that is both a title
// fragment and a position query.
func TestSearchNumericTitlesNotRegressed(t *testing.T) {
	snap := snapshotFor(t, seriesPositionCatalog())

	// "1984" is one token, so it is never read as a position at all.
	if _, ok := parseSeriesPositionQuery("1984"); ok {
		t.Error("parseSeriesPositionQuery('1984') read a bare number as a position")
	}
	if got := first(searchIDs(t, snap, "1984")); got != "work:nineteen-eighty-four" {
		t.Errorf("search('1984') first = %q, want work:nineteen-eighty-four", got)
	}

	// "Fahrenheit 451" parses as a position query but resolves no series, so the
	// title hit keeps the top slot.
	if got := first(searchIDs(t, snap, "fahrenheit 451")); got != "work:fahrenheit-451" {
		t.Errorf("search('fahrenheit 451') first = %q, want work:fahrenheit-451", got)
	}

	// "slow horses 2" is BOTH: the position hit leads, and the title hit that FTS
	// found ("Slow Horses 2: The Companion") is still on the page.
	ids := searchIDs(t, snap, "slow horses 2")
	if first(ids) != "work:dead-lions" {
		t.Errorf("search('slow horses 2') first = %q, want work:dead-lions (page: %v)", first(ids), ids)
	}
	if !contains(ids, "work:slow-horses-2-companion") {
		t.Errorf("search('slow horses 2') dropped the title hit: %v", ids)
	}
	// Without the number, the title search is exactly what it was.
	if ids := searchIDs(t, snap, "slow horses"); !contains(ids, "work:slow-horses") || !contains(ids, "series:slow-horses") {
		t.Errorf("search('slow horses') = %v, want the work and the series", ids)
	}
}

// TestSearchSeriesPositionDedupes covers the work that is both the boost and an
// FTS hit ("Halo 2" at position 2 of Halo): it must appear once, first.
func TestSearchSeriesPositionDedupes(t *testing.T) {
	snap := snapshotFor(t, seriesPositionCatalog())
	ids := searchIDs(t, snap, "halo 2")
	if first(ids) != "work:halo-2" {
		t.Errorf("search('halo 2') first = %q, want work:halo-2 (page: %v)", first(ids), ids)
	}
	n := 0
	for _, id := range ids {
		if id == "work:halo-2" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("search('halo 2') returned work:halo-2 %d times: %v", n, ids)
	}
}

// TestSearchSeriesPositionPrefersTheWholeName proves the volume-word handling
// tries the whole residual first: "the jungle book 2" is volume 2 of "The Jungle
// Book", not volume 2 of "The Jungle".
func TestSearchSeriesPositionPrefersTheWholeName(t *testing.T) {
	snap := snapshotFor(t, seriesPositionCatalog())
	if got := first(searchIDs(t, snap, "the jungle book 2")); got != "work:the-second-jungle-book" {
		t.Errorf("search('the jungle book 2') first = %q, want work:the-second-jungle-book", got)
	}
	if got := first(searchIDs(t, snap, "the jungle 2")); got != "work:the-jungle-returns" {
		t.Errorf("search('the jungle 2') first = %q, want work:the-jungle-returns", got)
	}
}

func TestParseSeriesPositionQuery(t *testing.T) {
	cases := []struct {
		query     string
		wantPos   string
		wantResid []string
	}{
		{"jack reacher 2", "2", []string{"jack reacher"}},
		{"jack reacher 02", "02", []string{"jack reacher"}},
		{"jack reacher #2", "2", []string{"jack reacher"}},
		{"jack reacher 2.", "2", []string{"jack reacher"}},
		{"jack reacher 2.5", "2.5", []string{"jack reacher"}},
		{"jack reacher 1-3.5", "1-3.5", []string{"jack reacher"}},
		{"jack reacher book 2", "2", []string{"jack reacher book", "jack reacher"}},
		{"Jack Reacher Vol. 2", "2", []string{"Jack Reacher Vol.", "Jack Reacher"}},
		{"die tribute von panem band 2", "2", []string{"die tribute von panem band", "die tribute von panem"}},
		// A one-token residual that IS a volume word keeps the word: stripping it
		// would leave nothing to resolve.
		{"buch 2", "2", []string{"buch"}},
	}
	for _, tc := range cases {
		got, ok := parseSeriesPositionQuery(tc.query)
		if !ok {
			t.Errorf("parseSeriesPositionQuery(%q) = not a position query", tc.query)
			continue
		}
		if got.position != tc.wantPos {
			t.Errorf("parseSeriesPositionQuery(%q).position = %q, want %q", tc.query, got.position, tc.wantPos)
		}
		if !reflect.DeepEqual(got.residuals, tc.wantResid) {
			t.Errorf("parseSeriesPositionQuery(%q).residuals = %v, want %v", tc.query, got.residuals, tc.wantResid)
		}
	}

	notPositions := []string{
		"1984",                       // one token: a title, not a series + number
		"2",                          // ditto
		"",                           // no tokens
		"   ",                        // no tokens
		"jack reacher",               // no number
		"the way of kings",           // no number
		"way of kings 9781427209269", // too long to be a position (an identifier)
		"halo v2",                    // the number does not start the token
		"catch 22a",                  // ditto, trailing letter
	}
	for _, q := range notPositions {
		if got, ok := parseSeriesPositionQuery(q); ok {
			t.Errorf("parseSeriesPositionQuery(%q) = %+v, want not a position query", q, got)
		}
	}
}

func TestPositionKey(t *testing.T) {
	cases := map[string]string{
		"2": "2", "02": "2", "002": "2", "0": "0", "00": "0",
		"2.5": "2.5", "02.5": "2.5", "1-3.5": "1-3.5", "01-03.5": "1-3.5",
		"10": "10", "": "", " 2 ": "2",
	}
	for in, want := range cases {
		if got := positionKey(in); got != want {
			t.Errorf("positionKey(%q) = %q, want %q", in, got, want)
		}
	}
	// Distinct positions stay distinct: the FTS tokenizer would fold these
	// together, which is exactly why the comparison is done here instead.
	for _, pair := range [][2]string{{"2", "2.5"}, {"2", "1-3.5"}, {"2.5", "2.50"}} {
		if positionKey(pair[0]) == positionKey(pair[1]) {
			t.Errorf("positionKey collapsed %q and %q", pair[0], pair[1])
		}
	}
}

func TestMergeHits(t *testing.T) {
	fts := []searchHit{
		{kind: "series", id: "jack-reacher"},
		{kind: "work", id: "die-trying"},
		{kind: "person", id: "lee-child"},
	}

	// No boost: the FTS page is handed back untouched.
	if got := mergeHits(nil, fts, 20); !reflect.DeepEqual(got, fts) {
		t.Errorf("mergeHits(nil, ...) = %v, want %v", got, fts)
	}

	// A boost leads and the duplicate FTS hit is dropped.
	got := mergeHits([]string{"die-trying"}, fts, 20)
	want := []searchHit{
		{kind: "work", id: "die-trying"},
		{kind: "series", id: "jack-reacher"},
		{kind: "person", id: "lee-child"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("mergeHits = %v, want %v", got, want)
	}

	// Repeated boost ids collapse, and the page never grows past the limit.
	got = mergeHits([]string{"deep-down", "deep-down", "tripwire"}, fts, 4)
	want = []searchHit{
		{kind: "work", id: "deep-down"},
		{kind: "work", id: "tripwire"},
		{kind: "series", id: "jack-reacher"},
		{kind: "work", id: "die-trying"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("mergeHits (capped) = %v, want %v", got, want)
	}
}
