package issueform

// The form's same-person gates honour internal/audit/reviewed.json's
// merge-people rejections as the importer does (importer.PeopleApart): the
// real list records Dr. Alex George the TV doctor and Alex George the novelist
// as two people, and David Clarke the narrator and Dr. David Clarke the
// counsellor, so the tests below read those decisions as shipped.

import (
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/audit"
	"github.com/kodestar/audiosilo-meta/internal/importer"
	"github.com/kodestar/audiosilo-meta/internal/testpack"
	"github.com/kodestar/audiosilo-meta/pkg/check"
)

// alexGeorgeTree is the novelist Alex George's two-volume "Mind Matters".
func alexGeorgeTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"people/al/alex-george.json":   testpack.PersonJSON(t, "alex-george", "Alex George"),
		"people/na/nate-narrator.json": testpack.PersonJSON(t, "nate-narrator", "Nate Narrator"),
		"series/mi/mind-matters.json":  testpack.SeriesJSON(t, "mind-matters", "Mind Matters", "a-good-american@1", "the-paris-hours@2"),
	}
	for _, w := range []string{"a-good-american", "the-paris-hours"} {
		files["works/"+w[:2]+"/"+w+"/work.json"] = testpack.WorkJSON(t, w, w, testpack.WithAuthors("alex-george"))
		files["works/"+w[:2]+"/"+w+"/recordings/r1.json"] = testpack.RecJSON(t, "r1", w, testpack.WithNarrators("nate-narrator"))
	}
	testpack.Seed(t, dir, files)
	if res := check.Load(dir); !res.OK() {
		t.Fatalf("seed tree does not validate: %v", res.Problems)
	}
	return dir
}

// The doctor's book titled like the novelist's is another author's book: it
// steps to the author-suffixed slug, as a clearly different author's does,
// rather than closing as a duplicate of the novelist's or asking a maintainer
// a question the review already answered.
func TestAddWorkReviewedPairIsAnotherAuthor(t *testing.T) {
	dir := alexGeorgeTree(t)
	res := processAddWork(t, dir, dupWorkBody("a-good-american", "Dr. Alex George", "Nate Narrator", "", ""))
	if res.Status != StatusOK {
		t.Fatalf("status = %q, want ok; messages = %v", res.Status, res.Messages)
	}
	if want := importer.AuthorSuffixedWorkSlug("a-good-american", "dr-alex-george"); !recordExists(t, dir, "works/"+want[:2]+"/"+want+"/work.json") {
		t.Fatalf("the book was not composed at %q; messages = %v", want, res.Messages)
	}
	if !anyContains(res.Messages, "another author's book of the same title") {
		t.Errorf("the step must be reported: %v", res.Messages)
	}
}

// ... and his book naming "Mind Matters" does not extend the novelist's series:
// it composes his own at the next chain slug, where the importer would mint it.
func TestAddWorkReviewedPairDoesNotExtendTheSeries(t *testing.T) {
	dir := alexGeorgeTree(t)
	res := processAddWork(t, dir, dupWorkBody("The Mind Manual", "Dr. Alex George", "Nate Narrator", "Mind Matters", "3"))
	if res.Status != StatusOK {
		t.Fatalf("status = %q, want ok; messages = %v", res.Status, res.Messages)
	}
	if strings.Contains(readFile(t, dir, "series/mi/mind-matters.json"), "the-mind-manual") {
		t.Error("the novelist's mind-matters was extended with the doctor's book")
	}
	if mint := readFile(t, dir, "series/mi/mind-matters-2.json"); !strings.Contains(mint, `"work": "the-mind-manual"`) {
		t.Errorf("the doctor's series was not composed at mind-matters-2:\n%s", mint)
	}
}

// The takeover's identity gate reads a narrator by the same rule: a submission
// crediting "Dr. David Clarke" does not describe a recording David Clarke
// narrates, while the bare spelling and an unreviewed courtesy title still do.
func TestSamePeopleHonoursAReviewedRejection(t *testing.T) {
	c := &composer{personName: map[string]string{"david-clarke": "David Clarke", "samuel-li": "Samuel Li"},
		apart: importer.NewPeopleApart(audit.ReviewedDistinct(), nil)}
	for _, tc := range []struct {
		names []string
		ids   []string
		want  bool
	}{
		{[]string{"David Clarke"}, []string{"david-clarke"}, true},
		{[]string{"Dr. David Clarke"}, []string{"david-clarke"}, false},
		{[]string{"Dr. Samuel Li"}, []string{"samuel-li"}, true},
	} {
		if got := c.samePeople(tc.names, tc.ids); got != tc.want {
			t.Errorf("samePeople(%v, %v) = %v, want %v", tc.names, tc.ids, got, tc.want)
		}
	}
}
