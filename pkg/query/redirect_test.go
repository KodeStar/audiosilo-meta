package query

import (
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/build"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// TestRedirectLookupIsIndexed pins the redirect probe to the artifact's primary
// key. It sits on the miss path of every id route, so a full scan here would be a
// table scan per 404 - and 404s are what a crawler and a stale client produce
// most of.
func TestRedirectLookupIsIndexed(t *testing.T) {
	snap := snapshotFor(t, fixtureCatalog())
	assertNoFullScan(t, queryPlan(t, snap, redirectTargetSQL, "works", "project-hail-mary-audiobook"))
}

// TestRedirectTargetResolves covers the query layer on its own: a hit, a miss,
// and that the namespaces do not leak into one another. (The version gate is
// covered end to end by TestRedirectsTolerateOlderArtifact, and the empty-table
// short circuit by TestRedirectTargetSkipsAnEmptyTable.)
func TestRedirectTargetResolves(t *testing.T) {
	snap := snapshotFor(t, fixtureCatalog())
	got, err := snap.redirectTarget("works", "project-hail-mary-audiobook")
	if err != nil || got != "project-hail-mary" {
		t.Errorf("redirectTarget(works) = %q, %v", got, err)
	}
	// The namespaces do not leak into one another.
	if got, err := snap.redirectTarget("people", "project-hail-mary-audiobook"); err != nil || got != "" {
		t.Errorf("redirectTarget(people) = %q, %v, want no hit", got, err)
	}
	if got, err := snap.redirectTarget("works", "project-hail-mary"); err != nil || got != "" {
		t.Errorf("a live slug resolved to %q, %v, want no hit", got, err)
	}
}

// TestRedirectTargetSkipsAnEmptyTable pins the memo: a catalogue that has retired
// nothing answers without touching the table, which matters because the chapters
// route consults the redirects on an ordinary 200 (an empty chapter list).
func TestRedirectTargetSkipsAnEmptyTable(t *testing.T) {
	cat := fixtureCatalog()
	cat.Redirects = nil
	snap := snapshotFor(t, cat)
	if snap.hasRedirects {
		t.Error("hasRedirects is true for a catalogue with no redirects")
	}
	if got, err := snap.redirectTarget("works", "project-hail-mary-audiobook"); err != nil || got != "" {
		t.Errorf("empty table = %q, %v, want no hit and no error", got, err)
	}
	// And with redirects present the memo says so, so the query does run.
	if full := snapshotFor(t, fixtureCatalog()); !full.hasRedirects {
		t.Error("hasRedirects is false for a catalogue that holds redirects")
	}
}

// TestSelfRedirectDoesNotLoop covers the resolver's own guarantee against a loop.
// pkg/check refuses a self-row, so this artifact is hand-built to carry one: the
// request must fall through to the 404 it was already heading for rather than
// 301-ing a following client back to the same URL forever.
func TestSelfRedirectDoesNotLoop(t *testing.T) {
	cat := fixtureCatalog()
	cat.Redirects = model.Redirects{model.RedirectWorks: {"ghost-work": "ghost-work"}}
	ts := serverFor(t, cat)

	resp := getNoFollow(t, ts.URL, "/api/v1/works/ghost-work")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404: a self-redirect must not be served", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "" {
		t.Errorf("Location = %q, want none", loc)
	}
}

// TestRedirectVersionClaimRequiresTheTable pins the load-time claim, and that the
// failure SAYS which claim it is: an artifact reporting redirectSchemaVersion or
// above without the table is corrupt, and "no such table: redirects" alone reads
// as a bug in the server rather than as a broken file.
//
// The version it asserts is the BUILDER's, not the literal 5 the gate was born
// at: a later table bumps SchemaVersion past it, and the message names the
// version the artifact actually claims.
func TestRedirectVersionClaimRequiresTheTable(t *testing.T) {
	path := buildFixtureDB(t, fixtureCatalog())
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DROP TABLE redirects`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	_, err = Open(path, "")
	if err == nil {
		t.Fatal("openSnapshot accepted a redirect-claiming artifact with no redirects table")
	}
	claimed := fmt.Sprintf("schema_version %d", build.SchemaVersion)
	for _, want := range []string{claimed, "requires the redirects table", path} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

// TestRecordWildcardIsReadByPosition: the record wildcard RedirectRetired fills
// is the FIRST single-segment wildcard of the matched pattern, whatever it is
// called - so a route spelling it {pid} still redirects (metaserve's coverage
// guard, TestRedirectCoverageIgnoresTheWildcardsName, judges routes by the same
// shape).
func TestRecordWildcardIsReadByPosition(t *testing.T) {
	if got := idWildcardOf("GET /api/v1/publishers/{pid}/imprints/{iid}"); got != "pid" {
		t.Errorf("idWildcardOf = %q, want pid", got)
	}
	if got := idWildcardOf("GET /api/v1/stats"); got != "" {
		t.Errorf("idWildcardOf of a literal route = %q, want empty", got)
	}
}

// TestEveryRecordRouteNamesANamespace is this route table's redirect drift
// guard: a route whose pattern carries a wildcard either names the namespace a
// retired slug in it resolves in, or appears in redirectExemptRoutes - saying out
// loud that its wildcard is not a record - so a new family route cannot ship
// without redirect support. metaserve's TestEveryIDRouteResolvesRetiredSlugs
// takes this table's answers as given, across its own routes too.
func TestEveryRecordRouteNamesANamespace(t *testing.T) {
	registered := map[string]bool{}
	for _, r := range Routes() {
		registered[r.Pattern] = true
		if !strings.Contains(r.Pattern, "{") {
			if r.Namespace != "" {
				t.Errorf("route %s names namespace %q but has no wildcard to resolve", r.Pattern, r.Namespace)
			}
			continue
		}
		if (r.Namespace != "") == redirectExemptRoutes[r.Pattern] {
			t.Errorf("route %s must either name a redirect namespace or be exempt, not %s",
				r.Pattern, map[bool]string{true: "both", false: "neither"}[r.Namespace != ""])
		}
	}
	for pattern := range redirectExemptRoutes {
		if !registered[pattern] {
			t.Errorf("redirectExemptRoutes names %s, which the route table does not register", pattern)
		}
	}
}
