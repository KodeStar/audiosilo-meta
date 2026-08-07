package issueform

import (
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/check"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// TestAddWorkStepsOffAReservedSlug is the intake half of the reserved-slug rule.
// The composer shares the bulk importer's bounded slug helpers but derives the
// work's base with a plain Slugify, so the guard has to be here too: a book
// titled "Search" would otherwise be written at /api/v1/works/search's own
// segment, and the very next metacheck run would fail the bot's own pull
// request.
//
// It covers all three families at once - the work title, the series name and a
// PERSON named Search, whose id comes from model.PersonSlug so intake and the
// importer mint the same variant. The tree is validated at the end, which is the
// only assertion that proves composing and checking agree.
func TestAddWorkStepsOffAReservedSlug(t *testing.T) {
	dir := seedTree(t)
	body := field(fWorkTitle, "Search") +
		field(fWorkSubtitle, "") +
		field(fWorkAuthors, "Search") +
		field(fWorkLanguage, "en") +
		field(fWorkFirstPublished, "2015") +
		field(fWorkGenres, "") +
		field(fWorkSeriesName, "Latest") +
		field(fWorkSeriesPosition, "2") +
		field(fWorkISBN, "") +
		field(fWorkWikidata, "") +
		field(fWorkOpenLibrary, "") +
		field(fRecNarrators, "Bob Reader") +
		field(fRecAbridged, "Unabridged") +
		field(fRecRuntime, "400") +
		field(fRecRelease, "2015-09-08") +
		field(fRecPublisher, "Acme Audio") +
		field(fRecASINs, "US: B222222222") +
		field(fRecISBNs, "") +
		field(fRecCoverURL, "") +
		field(fSources, "Audible product page") +
		"### Factual data\n\n- [x] factual\n\n" +
		"### " + fCC0 + "\n\n" + checkedBox()

	res := Process(Options{DataDir: dir, Template: "add-work", Body: body, Date: "2026-07-14"})
	if res.Status != StatusOK {
		t.Fatalf("status = %q, messages = %v", res.Status, res.Messages)
	}

	// The work: the author-suffixed slug the bulk importer's chain would mint.
	// The author is named "Search" too, so the suffix is the person's stepped-off
	// slug - which is exactly the point: one derivation, used everywhere.
	authorSlug, _ := model.PersonSlug("Search")
	wantWork := unreservedWorkSlug("search", "Search")
	if recordExists(t, dir, "works/se/search/work.json") {
		t.Error("a work was written at the reserved slug \"search\"")
	}
	if !recordExists(t, dir, "works/"+wantWork[:2]+"/"+wantWork+"/work.json") {
		t.Errorf("no work at %q: the reserved title slug must step onto the author suffix", wantWork)
	}
	if !strings.HasSuffix(wantWork, authorSlug) {
		t.Errorf("work slug %q does not end in the author's slug %q", wantWork, authorSlug)
	}

	// The person: the canonical reserved variant, never the bare word.
	if recordExists(t, dir, "people/se/search.json") {
		t.Error("a person was written at the reserved slug \"search\"")
	}
	if !recordExists(t, dir, "people/"+authorSlug[:2]+"/"+authorSlug+".json") {
		t.Errorf("no person at %q", authorSlug)
	}

	// The series: the numeric candidate, matching SeriesSlugAt.
	if recordExists(t, dir, "series/la/latest.json") {
		t.Error("a series was written at the reserved slug \"latest\"")
	}
	if !recordExists(t, dir, "series/la/latest-2.json") {
		t.Errorf("no series at latest-2: %v", res.Messages)
	}

	// Every step is reported, so a submitter reading the bot's comment learns
	// where their record actually went.
	joined := strings.Join(res.Messages, "\n")
	for _, want := range []string{`work slug "search" is reserved`, `series slug "latest" is reserved`} {
		if !strings.Contains(joined, want) {
			t.Errorf("messages do not mention %q: %v", want, res.Messages)
		}
	}

	if r := check.Load(dir); !r.OK() {
		t.Errorf("the composed tree does not validate: %v", r.Problems)
	}
}
