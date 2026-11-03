package issueform

import (
	"maps"
	"os"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/testpack"
	"github.com/kodestar/audiosilo-meta/pkg/check"
	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/redirects"
)

// A title correction records the retitle ALIAS metarepair's retitle-work records
// (importer.WorkTitleAlias, aliasRetitle): a work's slug never follows its title, so a
// correction leaving the record off the chain its new title composes would let the
// next import of that title mint a duplicate beside it.

const redirectsFile = "data/redirects.json"

// correctTitle files a correct-data title correction on the seed's existing-work.
func correctTitle(t *testing.T, dir, title string) Result {
	t.Helper()
	return Process(Options{DataDir: dir, Template: "correct-data",
		Body: correctBody("https://meta.audiosilo.app/works/existing-work", "title", title, "the cover", true)})
}

// workRedirects reads the tree's works tombstones; an absent table reads as none.
func workRedirects(t *testing.T, dir string) map[string]string {
	t.Helper()
	reds, err := redirects.Load(dir)
	if err != nil {
		t.Fatalf("load redirects: %v", err)
	}
	return reds[model.RedirectWorks]
}

func mustValidate(t *testing.T, dir string) {
	t.Helper()
	if res := check.Load(dir); !res.OK() {
		t.Fatalf("the composed tree does not validate: %v", res.Problems)
	}
}

// TestCorrectTitleRecordsRetitleAlias is the passing fixture: "Existing Work"
// retitled "A Better Title" leaves existing-work off the new chain, so the slug an
// import of the new title would mint at is retired onto it, the table rides in the
// bot's commit (Result.Files) and the verdict says so.
func TestCorrectTitleRecordsRetitleAlias(t *testing.T) {
	dir := seedTree(t)
	res := correctTitle(t, dir, "A Better Title")
	if res.Status != StatusOK {
		t.Fatalf("status = %q, messages = %v", res.Status, res.Messages)
	}
	if got := workRedirects(t, dir); !maps.Equal(got, map[string]string{"a-better-title": "existing-work"}) {
		t.Errorf("works redirects = %v, want the alias a-better-title -> existing-work", got)
	}
	if !hasFile(res.Files, worksPack) || !hasFile(res.Files, redirectsFile) {
		t.Errorf("files = %v, want the works pack and %s", res.Files, redirectsFile)
	}
	if !anyContains(res.Messages, "aliased a-better-title -> existing-work") {
		t.Errorf("the verdict does not report the alias: %v", res.Messages)
	}
	mustValidate(t, dir)
}

// TestCorrectTitleOnItsChainRecordsNoAlias: a correction whose new title still slugs
// to the record's own id needs no alias, and the table is not written at all.
func TestCorrectTitleOnItsChainRecordsNoAlias(t *testing.T) {
	dir := seedTree(t)
	res := correctTitle(t, dir, "Existing work")
	if res.Status != StatusOK {
		t.Fatalf("status = %q, messages = %v", res.Status, res.Messages)
	}
	if hasFile(res.Files, redirectsFile) {
		t.Errorf("files = %v: a title still on its chain wrote the tombstone table", res.Files)
	}
	if _, err := os.Stat(redirects.Path(dir)); !os.IsNotExist(err) {
		t.Errorf("a title still on its chain created %s (stat: %v)", redirectsFile, err)
	}
}

// TestCorrectTitleAliasStepsOverALiveWork: the alias is the first FREE candidate - a
// live work at the bare title is another record (a merge decision, not an alias), so
// the alias lands on the next candidate the import would mint at.
func TestCorrectTitleAliasStepsOverALiveWork(t *testing.T) {
	files := seedFiles()
	files["works/ab/a-better-title/work.json"] = `{
  "authors": ["john-smith"],
  "id": "a-better-title",
  "language": "en",
  "license": "CC0-1.0",
  "sources": [{"type": "user", "imported_at": "2026-07-01"}],
  "title": "A Better Title"
}`
	dir := t.TempDir()
	testpack.Seed(t, dir, files)
	res := correctTitle(t, dir, "A Better Title")
	if res.Status != StatusOK {
		t.Fatalf("status = %q, messages = %v", res.Status, res.Messages)
	}
	if got := workRedirects(t, dir); !maps.Equal(got, map[string]string{"a-better-title-jane-doe": "existing-work"}) {
		t.Errorf("works redirects = %v, want only a-better-title-jane-doe -> existing-work", got)
	}
	mustValidate(t, dir)
}

// TestCorrectTitleAlreadyAliasedWritesNothingMore: a chain candidate already retired
// onto the record IS the record to the importer, so no second alias is added and the
// table is left as it was.
func TestCorrectTitleAlreadyAliasedWritesNothingMore(t *testing.T) {
	dir := seedTree(t)
	if err := redirects.Write(dir, model.Redirects{model.RedirectWorks: {"a-better-title": "existing-work"}}); err != nil {
		t.Fatal(err)
	}
	res := correctTitle(t, dir, "A Better Title")
	if res.Status != StatusOK {
		t.Fatalf("status = %q, messages = %v", res.Status, res.Messages)
	}
	if hasFile(res.Files, redirectsFile) || anyContains(res.Messages, "aliased") {
		t.Errorf("an already-aliased title was aliased again: files = %v, messages = %v", res.Files, res.Messages)
	}
}

// TestCorrectTitleOfASerialVolumeRecordsNoAlias: a work at "<title>-book-<n>" is a
// serial volume reached by its position probes, and aliasing the bare title onto it
// would pull every claim-free row of that title into that one volume - the exception
// WorkTitleAlias states, which the intake door inherits rather than restates.
func TestCorrectTitleOfASerialVolumeRecordsNoAlias(t *testing.T) {
	files := seedFiles()
	files["works/th/the-saga-book-2/work.json"] = `{
  "authors": ["jane-doe"],
  "id": "the-saga-book-2",
  "language": "en",
  "license": "CC0-1.0",
  "sources": [{"type": "user", "imported_at": "2026-07-01"}],
  "title": "The Saga, Book Two"
}`
	dir := t.TempDir()
	testpack.Seed(t, dir, files)
	res := Process(Options{DataDir: dir, Template: "correct-data",
		Body: correctBody("https://meta.audiosilo.app/works/the-saga-book-2", "title", "The Saga", "the cover", true)})
	if res.Status != StatusOK {
		t.Fatalf("status = %q, messages = %v", res.Status, res.Messages)
	}
	if hasFile(res.Files, redirectsFile) || len(workRedirects(t, dir)) != 0 {
		t.Errorf("a serial volume was aliased: files = %v, redirects = %v", res.Files, workRedirects(t, dir))
	}
}

// TestBatchTitleRecordsRetitleAlias: the correction-batch form applies a title
// through the same path, so its alias rides in the same pull request.
func TestBatchTitleRecordsRetitleAlias(t *testing.T) {
	dir := seedTree(t)
	res := runBatch(t, dir, changesJSON(change(batchWork, "title", "Existing Work", "A Better Title")))
	if res.Status != StatusOK {
		t.Fatalf("status = %q, messages = %v", res.Status, res.Messages)
	}
	if got := workRedirects(t, dir); got["a-better-title"] != "existing-work" {
		t.Errorf("works redirects = %v, want the alias a-better-title -> existing-work", got)
	}
	if !hasFile(res.Files, redirectsFile) {
		t.Errorf("files = %v, want %s", res.Files, redirectsFile)
	}
	mustValidate(t, dir)
}
