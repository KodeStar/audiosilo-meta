package serve

import (
	"strings"
	"testing"
)

// queryPlan returns the EXPLAIN QUERY PLAN steps for query.
func queryPlan(t *testing.T, snap *snapshot, query string, args ...any) []string {
	t.Helper()
	rows, err := snap.db.Query("EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatalf("EXPLAIN QUERY PLAN %s: %v", query, err)
	}
	defer func() { _ = rows.Close() }()
	var plan []string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return plan
}

// assertNoFullScan fails when any step of the plan is a bare full table scan.
// Two shapes are NOT full scans and are allowed:
//   - "SCAN ... USING INDEX" walks an index, which is what an ORDER BY over a
//     covering index looks like.
//   - "SCAN <fts> VIRTUAL TABLE INDEX <n>:M..." is FTS5 being driven BY the
//     MATCH constraint (the "M" plan). Every FTS query reports as a SCAN of the
//     virtual table; that is the index lookup, not a scan of the catalogue.
func assertNoFullScan(t *testing.T, plan []string) {
	t.Helper()
	joined := strings.Join(plan, " | ")
	for _, step := range plan {
		if !strings.HasPrefix(step, "SCAN ") {
			continue
		}
		if strings.Contains(step, "USING") || strings.Contains(step, "VIRTUAL TABLE INDEX") {
			continue
		}
		t.Errorf("full table scan in plan: %s\nfull plan: %s", step, joined)
	}
	t.Logf("plan -> %s", joined)
}

// TestServeLookupsAreIndexed is the regression guard for the serving indexes. It
// lives HERE, in the package that issues the queries, and runs each one from the
// very constant the request path uses - so a query that is edited without its
// index is caught, which a hand-copied lookalike in the builder's tests could
// not do.
//
// Every one of these was a measured full scan before the covering indexes were
// added, on the hottest paths in the API: GET /works/{id} runs the per-recording
// three once per recording, so the cost was multiplied by the catalogue size on
// every hit.
func TestServeLookupsAreIndexed(t *testing.T) {
	snap := snapshotFor(t, fixtureCatalog())

	const (
		work      = "project-hail-mary"
		recording = "ray-porter-2021"
		wok       = "the-way-of-kings"
		series    = "the-stormlight-archive"
		person    = "brandon-sanderson"
	)

	cases := []struct {
		name  string
		query string
		args  []any
	}{
		// Per-recording reads, once per recording of every work detail.
		{"narrators of a recording", narratorsOfSQL, []any{work, recording}},
		{"asins of a recording", asinsOfSQL, []any{work, recording}},
		{"isbns of a recording", recordingISBNsSQL, []any{wok, "kramer-reading-2010"}},
		{"chapter count of a recording", recordingCountSQL, []any{work, recording}},
		// Per-work reads.
		{"authors of a work", authorsOfSQL, []any{work}},
		{"narrators of a work", workNarratorsSQL, []any{wok}},
		{"character aliases of a work", characterAliasSQL, []any{work}},
		// Series detail.
		{"works of a series", seriesWorksSQL, []any{series}},
		{"authors of a series", seriesAuthorsSQL, []any{series}},
		{"work count of a series", seriesWorksCountSQL, []any{series}},
		// Person detail: the paged lists AND the totals that must agree with them.
		{"authored total", authoredTotalSQL, []any{person}},
		{"narrated total", narratedTotalSQL, []any{person}},
		{"authored page", authoredPageSQL, []any{person, 100, 0}},
		{"narrated page", narratedPageSQL, []any{person, 100, 0}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertNoFullScan(t, queryPlan(t, snap, tc.query, tc.args...))
		})
	}
}

// TestBatchLookupsAreIndexed covers the IN-batch queries cardsByID issues, plus
// the series-position boost's batched membership read. They carry a rendered
// placeholder list rather than a fixed argument count, so the guard drives them
// through the same eachChunk helper the real code uses.
func TestBatchLookupsAreIndexed(t *testing.T) {
	snap := snapshotFor(t, fixtureCatalog())
	ids := []string{"project-hail-mary", "the-way-of-kings"}
	seriesIDs := []string{"the-stormlight-archive"}

	cases := []struct {
		name string
		ids  []string
		sql  func(ph string) string
	}{
		{"works by id", ids, worksByIDSQL},
		{"authors by work", ids, authorsByWorkSQL},
		{"first series by work", ids, firstSeriesByWorkSQL},
		{"covers by work", ids, coversByWorkSQL},
		{"members of the probed series", seriesIDs, seriesMembersSQL},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := eachChunk(tc.ids, func(ph string, args []any) error {
				assertNoFullScan(t, queryPlan(t, snap, tc.sql(ph), args...))
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

// NOT guarded here: seriesMatchSQL, the series-position boost's name probe.
// This test checks INDEX SELECTION, and an FTS5 MATCH selects no index a plan
// can name - every FTS query reports as "SCAN <fts> VIRTUAL TABLE INDEX n:M...",
// which assertNoFullScan deliberately allows, and the bm25 ORDER BY legitimately
// sorts in a temp b-tree. A case here would pass no matter how expensive the
// probe became, and a vacuous guard is worse than none: it reads as coverage.
// What actually bounds the probe's cost is the SIZE of its match set, and that
// is guarded behaviourally instead - TestSeriesPositionSkipsJunkResiduals pins
// the residuals that are never probed, and ftsPhrase (no prefix-star) pins the
// match to whole tokens. Both are measured in the A/B on the real artifact.
