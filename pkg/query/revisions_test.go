package query

import (
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/artifacttest"
)

// revisedFixture is the fixture with a hand-written page_revisions: one work,
// one person and the recap page dated, everything else unknown.
func revisedFixture(t *testing.T) *DB {
	t.Helper()
	return openTestDB(t, artifacttest.Altered(t, artifacttest.Fixture(), revisionsSchemaVersion,
		`INSERT INTO page_revisions(kind, id, revised_at) VALUES
			('works', 'project-hail-mary', '2026-11-02T10:00:00Z'),
			('people', 'andy-weir', '2026-11-01T10:00:00Z'),
			('recaps', 'project-hail-mary', '2026-11-03T10:00:00Z'),
			-- A row for another family's id must not leak into this one.
			('series', 'project-hail-mary', '2026-01-01T00:00:00Z')`))
}

func TestPagesCarryTheirRevision(t *testing.T) {
	snap := revisedFixture(t)
	want := map[PageFamily]map[string]string{
		WorkPages:      {"project-hail-mary": "2026-11-02T10:00:00Z"},
		PersonPages:    {"andy-weir": "2026-11-01T10:00:00Z"},
		RecapPages:     {"project-hail-mary": "2026-11-03T10:00:00Z"},
		CharacterPages: {},
		SeriesPages:    {},
	}
	for family, dated := range want {
		entries, err := snap.Pages(family, 100, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != snap.PageCount(family) {
			t.Errorf("%s: %d entries, PageCount %d", family, len(entries), snap.PageCount(family))
		}
		for _, e := range entries {
			got := ""
			if e.RevisedAt != nil {
				got = *e.RevisedAt
			}
			if got != dated[e.ID] {
				t.Errorf("%s/%s: RevisedAt %q, want %q", family, e.ID, got, dated[e.ID])
			}
			at, err := snap.PageRevision(family, e.ID)
			if err != nil {
				t.Fatal(err)
			}
			if at != got {
				t.Errorf("%s/%s: PageRevision %q, Pages %q", family, e.ID, at, got)
			}
		}
	}
	// The works family keeps its added_at beside the revision.
	entries, err := snap.Pages(WorkPages, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.ID == "project-hail-mary" && (e.AddedAt == nil || *e.AddedAt != "2026-07-10T00:00:00Z") {
			t.Errorf("added_at = %v", e.AddedAt)
		}
	}
}

func TestPageRevisionUnknown(t *testing.T) {
	for name, path := range map[string]string{
		// An empty table (an artifact built without a previous release) asks nothing.
		"unstamped": artifacttest.Build(t, artifacttest.Fixture()),
		// An older artifact has no table to ask.
		"schema 13": artifacttest.Downgraded(t, artifacttest.Fixture(), 13, "page_revisions"),
	} {
		t.Run(name, func(t *testing.T) {
			snap := openTestDB(t, path)
			if snap.hasRevisions {
				t.Error("hasRevisions on an artifact with no revision")
			}
			at, err := snap.PageRevision(WorkPages, "project-hail-mary")
			if err != nil || at != "" {
				t.Errorf("PageRevision = %q, %v", at, err)
			}
			for _, family := range []PageFamily{WorkPages, PersonPages, SeriesPages, RecapPages, CharacterPages} {
				entries, err := snap.Pages(family, 100, 0)
				if err != nil {
					t.Fatal(err)
				}
				for _, e := range entries {
					if e.RevisedAt != nil {
						t.Errorf("%s/%s: RevisedAt %q", family, e.ID, *e.RevisedAt)
					}
				}
			}
		})
	}
	snap := revisedFixture(t)
	if at, err := snap.PageRevision(WorkPages, "no-such-work"); err != nil || at != "" {
		t.Errorf("PageRevision of an absent page = %q, %v", at, err)
	}
}

func TestRevisionClaimWithoutItsTableFails(t *testing.T) {
	path := artifacttest.Altered(t, artifacttest.Fixture(), revisionsSchemaVersion, "DROP TABLE page_revisions")
	if db, err := Open(path, ""); err == nil {
		_ = db.Close()
		t.Fatal("accepted v14 without page_revisions")
	} else if !strings.Contains(err.Error(), "requires the page_revisions table") {
		t.Fatal(err)
	}
}

// TestRevisionReadsAreIndexed puts the revision layer's reads through the index
// guard: the point query and every family's revised shard spelling, which must
// still walk its family's index with a primary-key probe per row.
func TestRevisionReadsAreIndexed(t *testing.T) {
	snap := revisedFixture(t)
	cases := []struct {
		name  string
		query string
		args  []any
	}{
		{"page revision", pageRevisionSQL, []any{string(WorkPages), "project-hail-mary"}},
		{"works shard, revised", worksRevisedSitemapSQL, []any{sitemapShardURLs, 0}},
		{"people shard, revised", peopleRevisedSitemapSQL, []any{sitemapShardURLs, 0}},
		{"series shard, revised", seriesRevisedSitemapSQL, []any{sitemapShardURLs, 0}},
		{"characters shard, revised", charactersRevisedSitemapSQL, []any{sitemapShardURLs, 0}},
		{"recaps shard, revised", recapsRevisedSitemapSQL, []any{sitemapShardURLs, 0}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan := queryPlan(t, snap, tc.query, tc.args...)
			assertNoFullScan(t, plan)
			if tc.query == pageRevisionSQL {
				return
			}
			if joined := strings.Join(plan, " | "); !strings.Contains(joined, "SEARCH pr USING PRIMARY KEY") {
				t.Errorf("the revision is not a primary-key probe: %s", joined)
			}
		})
	}
}
