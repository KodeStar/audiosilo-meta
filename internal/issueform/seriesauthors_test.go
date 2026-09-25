package issueform

import (
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/testpack"
	"github.com/kodestar/audiosilo-meta/pkg/check"
)

// The intake form resolves a series name by the importer's own AUTHOR rule
// (internal/importer seriesauthors.go), so a form and an import of one book
// agree which series it is: another author's same-named series is never
// extended, and the submitting author's own series is found down the chain.

// formLostFleetTree is Sarah Hawke's three-volume `lost-fleet`, plus extra files.
func formLostFleetTree(t *testing.T, extra map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"people/sa/sarah-hawke.json":   testpack.PersonJSON(t, "sarah-hawke", "Sarah Hawke"),
		"people/ja/jack-campbell.json": testpack.PersonJSON(t, "jack-campbell", "Jack Campbell"),
		"people/na/nate-narrator.json": testpack.PersonJSON(t, "nate-narrator", "Nate Narrator"),
		"series/lo/lost-fleet.json":    testpack.SeriesJSON(t, "lost-fleet", "Lost Fleet", "incursion@1", "insurrection@2", "invasion@3"),
	}
	for _, w := range []string{"incursion", "insurrection", "invasion"} {
		files["works/in/"+w+"/work.json"] = testpack.WorkJSON(t, w, w, testpack.WithAuthors("sarah-hawke"))
		files["works/in/"+w+"/recordings/r1.json"] = testpack.RecJSON(t, "r1", w)
	}
	for k, v := range extra {
		files[k] = v
	}
	testpack.Seed(t, dir, files)
	if res := check.Load(dir); !res.OK() {
		t.Fatalf("seed tree does not validate: %v", res.Problems)
	}
	return dir
}

// A Campbell submission naming "Lost Fleet" does not extend Hawke's series: it
// composes Campbell's own at the next free chain slug, exactly where the bulk
// importer would mint it, and says so.
func TestAddWorkDoesNotExtendAnotherAuthorsSeries(t *testing.T) {
	dir := formLostFleetTree(t, nil)
	res := processAddWork(t, dir, dupWorkBody("Valiant", "Jack Campbell", "Christian Rummel", "Lost Fleet", "4"))

	if res.Status != StatusOK {
		t.Fatalf("status = %q, want ok; messages = %v", res.Status, res.Messages)
	}
	if strings.Contains(readFile(t, dir, "series/lo/lost-fleet.json"), "valiant") {
		t.Error("Hawke's lost-fleet was extended with Campbell's book")
	}
	mint := readFile(t, dir, "series/lo/lost-fleet-2.json")
	if !strings.Contains(mint, `"work": "valiant"`) || !strings.Contains(mint, `"name": "Lost Fleet"`) {
		t.Errorf("Campbell's series was not composed at lost-fleet-2:\n%s", mint)
	}
	if !anyContains(res.Messages, "belongs to other authors") {
		t.Errorf("the verdict does not say why a new series was composed: %v", res.Messages)
	}
}

// ...while Hawke's own next volume extends hers, and a Campbell submission finds
// Campbell's series down the chain once it exists.
func TestAddWorkFindsTheAuthorsOwnSeries(t *testing.T) {
	dir := formLostFleetTree(t, map[string]string{
		"works/da/dauntless/work.json":          testpack.WorkJSON(t, "dauntless", "Dauntless", testpack.WithAuthors("jack-campbell")),
		"works/da/dauntless/recordings/r1.json": testpack.RecJSON(t, "r1", "dauntless"),
		"series/lo/lost-fleet-2.json":           testpack.SeriesJSON(t, "lost-fleet-2", "Lost Fleet", "dauntless@1"),
	})
	res := processAddWork(t, dir, dupWorkBody("Fearless", "Jack Campbell", "Christian Rummel", "Lost Fleet", "2"))
	if res.Status != StatusOK {
		t.Fatalf("status = %q, want ok; messages = %v", res.Status, res.Messages)
	}
	if !strings.Contains(readFile(t, dir, "series/lo/lost-fleet-2.json"), `"work": "fearless"`) {
		t.Error("Campbell's lost-fleet-2 was not extended")
	}
	if strings.Contains(readFile(t, dir, "series/lo/lost-fleet.json"), "fearless") {
		t.Error("Hawke's lost-fleet was extended with Campbell's book")
	}

	dir = formLostFleetTree(t, nil)
	res = processAddWork(t, dir, dupWorkBody("Renegade", "Sarah Hawke", "Nate Narrator", "Lost Fleet", "4"))
	if res.Status != StatusOK {
		t.Fatalf("status = %q, want ok; messages = %v", res.Status, res.Messages)
	}
	if !strings.Contains(readFile(t, dir, "series/lo/lost-fleet.json"), `"work": "renegade"`) {
		t.Error("Hawke's own volume did not extend her series")
	}
}
