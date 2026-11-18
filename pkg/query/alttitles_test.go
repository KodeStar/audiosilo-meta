package query

import (
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/artifacttest"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// altTitleCatalog is one work that states an alternate title (the retitled
// re-release's earlier title) and one that states none.
func altTitleCatalog() *model.Catalog {
	cat := storeCatalog()
	cat.Works[0].AlternateTitles = []string{"Lawless Seventeen"}
	return cat
}

// TestAlternateTitlesOnWorkDetail: the work document states its alternate titles,
// and a search for one finds the work.
func TestAlternateTitlesOnWorkDetail(t *testing.T) {
	_, ts := serveDB(t, artifacttest.Build(t, altTitleCatalog()), HandlerOptions{})
	code, work := artifacttest.GetJSON(t, ts.URL, "/api/v1/works/without-law-17")
	if code != http.StatusOK {
		t.Fatalf("GET work = %d %v", code, work)
	}
	if want := []any{"Lawless Seventeen"}; !reflect.DeepEqual(work["alternate_titles"], want) {
		t.Errorf("alternate_titles = %v, want %v", work["alternate_titles"], want)
	}
	code, page := artifacttest.GetJSON(t, ts.URL, "/api/v1/search?q=lawless+seventeen")
	if code != http.StatusOK || !strings.Contains(fmt.Sprint(page), "without-law-17") {
		t.Errorf("search for the alternate title = %d %v, want the work", code, page)
	}
}

// TestAlternateTitlesOnAnOlderArtifact: a newer binary serving a version 11 release
// (no work_alternate_titles table) answers the work with none rather than a 500.
func TestAlternateTitlesOnAnOlderArtifact(t *testing.T) {
	path := artifacttest.Altered(t, altTitleCatalog(), 11, "DROP TABLE work_alternate_titles")
	code, work := artifacttest.GetJSON(t, downgradedServer(t, path).URL, "/api/v1/works/without-law-17")
	if code != http.StatusOK || work["alternate_titles"] != nil {
		t.Errorf("GET work on a v11 artifact = %d, alternate_titles %v; want 200 and none", code, work["alternate_titles"])
	}
}

// TestOpenRefusesAFalseAlternateTitlesClaim: a version 12 claim without the table
// is a corrupt file, refused at open naming the claim.
func TestOpenRefusesAFalseAlternateTitlesClaim(t *testing.T) {
	path := artifacttest.Altered(t, altTitleCatalog(), 12, "DROP TABLE work_alternate_titles")
	db, err := Open(path, "")
	if err == nil {
		_ = db.Close()
		t.Fatal("Open accepted a version 12 artifact without the work_alternate_titles table")
	}
	if !strings.Contains(err.Error(), "work_alternate_titles") {
		t.Errorf("error = %v, want it to name the claim", err)
	}
}
