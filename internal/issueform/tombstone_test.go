package issueform

import (
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/check"
	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/redirects"
)

// The intake door follows the slug tombstone table exactly as the bulk importer
// does (internal/importer/tombstone.go): a form never composes a person, work or
// series at a slug a merge retired, because the next metacheck would refuse the
// bot's own pull request for re-creating the merged duplicate (issue #2320's
// failure, through the other door).

// tombstoneFormTree is the shared seed plus a tombstone in every namespace, each
// retired onto a record the seed holds.
func tombstoneFormTree(t *testing.T) string {
	t.Helper()
	dir := seedTree(t)
	if err := redirects.Write(dir, model.Redirects{
		model.RedirectPeople: {"janet-q-doe": "jane-doe"},
		model.RedirectSeries: {"old-series": "existing-series"},
		model.RedirectWorks:  {"old-work": "existing-work"},
	}); err != nil {
		t.Fatalf("write redirects: %v", err)
	}
	if res := check.Load(dir); !res.OK() {
		t.Fatalf("seed tree with tombstones does not validate: %v", res.Problems)
	}
	return dir
}

// tombstoneWorkBody is an add-work body naming a title, an author and an
// optional series placement - the three names a tombstone can retire.
func tombstoneWorkBody(title, author, series, pos string) string {
	return field(fWorkTitle, title) +
		field(fWorkSubtitle, "") +
		field(fWorkAuthors, author) +
		field(fWorkLanguage, "en") +
		field(fWorkFirstPublished, "") +
		field(fWorkGenres, "") +
		field(fWorkSeriesName, series) +
		field(fWorkSeriesPosition, pos) +
		field(fWorkISBN, "") +
		field(fWorkWikidata, "") +
		field(fWorkOpenLibrary, "") +
		field(fRecNarrators, "John Smith") +
		field(fRecAbridged, "Unabridged") +
		field(fRecRuntime, "400") +
		field(fRecRelease, "2021-03-04") +
		field(fRecPublisher, "Acme Audio") +
		field(fRecASINs, "US: B0TOMBF001") +
		field(fRecISBNs, "") +
		field(fRecCoverURL, "") +
		field(fSources, "Audible product page") +
		"### Factual data\n\n- [x] factual\n\n" +
		"### " + fCC0 + "\n\n" + checkedBox()
}

// TestAddWorkResolvesRetiredPersonAndSeries: an author whose name slugs onto a
// retired person is credited as the survivor, and a series whose name slugs onto
// a retired series extends the survivor - nothing is composed at either retired
// address, the verdict says so, and the tree the bot writes validates.
func TestAddWorkResolvesRetiredPersonAndSeries(t *testing.T) {
	dir := tombstoneFormTree(t)
	res := Process(Options{DataDir: dir, Template: "add-work",
		Body: tombstoneWorkBody("Brand New Book", "Janet Q. Doe", "Old Series", "2"), Date: "2026-09-24"})
	if res.Status != StatusOK {
		t.Fatalf("status = %q, messages = %v", res.Status, res.Messages)
	}
	if recordExists(t, dir, "people/ja/janet-q-doe.json") {
		t.Error("a person was composed at the retired slug janet-q-doe")
	}
	if recordExists(t, dir, "series/ol/old-series.json") {
		t.Error("a series was composed at the retired slug old-series")
	}
	if work := readFile(t, dir, "works/br/brand-new-book/work.json"); !strings.Contains(work, `"jane-doe"`) {
		t.Errorf("the work does not credit the survivor jane-doe:\n%s", work)
	}
	if ser := readFile(t, dir, "series/ex/existing-series.json"); !strings.Contains(ser, `"brand-new-book"`) {
		t.Errorf("the survivor series was not extended:\n%s", ser)
	}
	for _, want := range []string{`people slug "janet-q-doe"`, `series slug "old-series"`} {
		if !anyContains(res.Messages, want) {
			t.Errorf("the verdict does not report %s: %v", want, res.Messages)
		}
	}
	if res := check.Load(dir); !res.OK() {
		t.Errorf("the composed tree does not validate: %v", res.Problems)
	}
}

// TestAddWorkOnARetiredWorkSlugIsTheSurvivorsDuplicate: the work-slug gate meets
// a retired slug as its survivor - the gate that has always met a live record at
// that slug - so the verdict is the survivor's duplicate, naming it, and nothing
// is written.
func TestAddWorkOnARetiredWorkSlugIsTheSurvivorsDuplicate(t *testing.T) {
	dir := tombstoneFormTree(t)
	res := Process(Options{DataDir: dir, Template: "add-work",
		Body: tombstoneWorkBody("Old Work", "Jane Doe", "", ""), Date: "2026-09-24"})
	if res.Status != StatusDuplicate {
		t.Fatalf("status = %q, want duplicate; messages = %v", res.Status, res.Messages)
	}
	if !anyContains(res.Messages, "old-work") || !anyContains(res.Messages, "existing-work") {
		t.Errorf("the verdict does not name the retired slug and its survivor: %v", res.Messages)
	}
	if recordExists(t, dir, "works/ol/old-work/work.json") {
		t.Error("a work was composed at the retired slug old-work")
	}
}
