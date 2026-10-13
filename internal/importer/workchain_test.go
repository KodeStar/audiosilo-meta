package importer

import (
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/testpack"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// OnWorkSlugChain answers from the chain itself (workCandidates), so every slug the
// create path would probe or mint for a title is on it - the SHORTENED ones included -
// and a hand-made slug the chain never mints is not.
func TestOnWorkSlugChain(t *testing.T) {
	work := func(id, title string, authors ...string) *model.Work {
		return &model.Work{ID: id, Title: title, Authors: authors}
	}
	on := []*model.Work{
		work("emma", "Emma", "jane-austen"),
		work("emma-jane-austen", "Emma", "jane-austen"),
		work("emma-jane-austen-2", "Emma", "jane-austen"),
		work("emma-jane-austen-17", "Emma", "jane-austen"),
		// A second credited person is a probe-only candidate, but a record there is one
		// a re-import still finds.
		work("emma-fay-weldon", "Emma", "jane-austen", "fay-weldon"),
		// The edition marker is cleaned off before the slug is composed.
		work("mageling", "Mageling (Unabridged)", "jane-doe"),
	}
	for _, w := range on {
		if !OnWorkSlugChain(w) {
			t.Errorf("OnWorkSlugChain(%q, %q) = false, want true", w.ID, w.Title)
		}
	}
	// A title long enough that the author-suffixed candidate must be SHORTENED is
	// recognized at the slug the chain really mints, not at a restated formula.
	long := strings.Repeat("an extraordinarily long title word ", 6)
	base := Slugify(long)
	shortened := AuthorSuffixedWorkSlug(base, "jane-doe")
	if shortened == base+"-jane-doe" {
		t.Fatalf("fixture is not long enough to shorten: %q", shortened)
	}
	if !OnWorkSlugChain(work(shortened, long, "jane-doe")) {
		t.Errorf("the shortened chain slug %q is not recognized", shortened)
	}
	off := []*model.Work{
		work("emma-1996", "Emma", "jane-austen"),
		work("emma-0", "Emma", "jane-austen"),
		work("emma-1", "Emma", "jane-austen"),
		work("emma-2", "Emma", "jane-austen"),
		work("emma-classic", "Emma", "jane-austen"),
		work("emma-jane-austen", "Emma", "someone-else"),
		work("emma", "Emma"),
	}
	for _, w := range off {
		if OnWorkSlugChain(w) {
			t.Errorf("OnWorkSlugChain(%q, %q, %v) = true, want false", w.ID, w.Title, w.Authors)
		}
	}
}

// WorkTitleAlias is empty when the work's own title chain reaches it - its id, or a
// tombstone onto it - and otherwise names the slug an import of that title would
// mint at, stepping past what is held or retired exactly as walkWorkChain does.
func TestWorkTitleAlias(t *testing.T) {
	w := &model.Work{ID: "messengers-legacy-novella", Title: "Messenger's Legacy", Authors: []string{"peter-v-brett"}}
	held := map[string]bool{}
	live := func(s string) bool { return held[s] }
	reds := model.NewRedirects()

	if got := WorkTitleAlias(w, live, reds); got != "messengers-legacy" {
		t.Errorf("off-chain: alias = %q, want the bare slug the import would mint at", got)
	}
	reds[model.RedirectWorks]["messengers-legacy"] = w.ID
	if got := WorkTitleAlias(w, live, reds); got != "" {
		t.Errorf("reached through its own alias: alias = %q, want none", got)
	}
	reds[model.RedirectWorks]["messengers-legacy"] = "some-other-work"
	if got := WorkTitleAlias(w, live, reds); got != "messengers-legacy-peter-v-brett" {
		t.Errorf("bare slug retired onto another work: alias = %q, want the author-suffixed candidate", got)
	}
	reds = model.NewRedirects()
	held["messengers-legacy"] = true
	if got := WorkTitleAlias(w, live, reds); got != "messengers-legacy-peter-v-brett" {
		t.Errorf("bare slug held by another work: alias = %q, want the author-suffixed candidate", got)
	}
	if got := WorkTitleAlias(&model.Work{ID: "messengers-legacy", Title: "Messenger's Legacy", Authors: w.Authors}, live, reds); got != "" {
		t.Errorf("on its chain: alias = %q, want none", got)
	}
	if got := WorkTitleAlias(&model.Work{ID: "x", Title: "Messenger's Legacy"}, live, reds); got != "" {
		t.Errorf("no authors: alias = %q, want none (nothing is composed)", got)
	}
	// A serial volume is reached by its position probes, not by the claim-free chain.
	if got := WorkTitleAlias(&model.Work{ID: "bravelands-book-3", Title: "Bravelands", Authors: w.Authors}, live, reds); got != "" {
		t.Errorf("serial volume: alias = %q, want none", got)
	}
	// A decorated title's own slug is not one, whatever its tail reads like.
	horsemen := &model.Work{ID: "war-the-four-horsemen-book-2", Title: "War", Authors: []string{"laura-thalassa"}}
	if got := WorkTitleAlias(horsemen, live, reds); got != "war" {
		t.Errorf("retitled decorated work: alias = %q, want war", got)
	}
}

// The alias is what makes a retitle safe end to end. Without it the next import of
// the new title misses the off-chain work: the duplicate-identity guard happens to
// catch this row, but it can only DROP it (the edition is lost, and a row past the
// guard's vetoes mints the duplicate). The alias WorkTitleAlias names, recorded as a
// tombstone onto the work, makes that import merge into it instead.
func TestWorkTitleAliasMakesTheImportMerge(t *testing.T) {
	files := map[string]string{
		"works/me/messengers-legacy-novella/work.json": testpack.WorkJSON(t, "messengers-legacy-novella",
			"Messenger's Legacy", testpack.WithAuthors("ada-mapmaker")),
		"works/me/messengers-legacy-novella/recordings/r1.json": testpack.RecJSON(t, "r1", "messengers-legacy-novella",
			testpack.WithNarrators("bea-reader"), testpack.WithRuntime(300)),
	}
	row := tombRow("B0ALIAS001", "Messenger's Legacy", "Ada Mapmaker", "Bea Reader", 300, "", "")

	t.Run("without the alias", func(t *testing.T) {
		dataDir := seedTombstoneTree(t, files, model.NewRedirects())
		sum := runLibexOver(t, dataDir, row)
		if sum.NewWorks == 0 && sum.SkippedDuplicateIdentity == 0 {
			t.Fatal("the row merged into the off-chain work without an alias; this test no longer shows what the alias is for")
		}
	})
	t.Run("with the alias", func(t *testing.T) {
		w := &model.Work{ID: "messengers-legacy-novella", Title: "Messenger's Legacy", Authors: []string{"ada-mapmaker"}}
		alias := WorkTitleAlias(w, func(s string) bool { return s == w.ID }, model.NewRedirects())
		if alias == "" {
			t.Fatal("no alias for an off-chain work")
		}
		dataDir := seedTombstoneTree(t, files, model.Redirects{model.RedirectWorks: {alias: w.ID}})
		sum := runLibexOver(t, dataDir, row)
		if sum.NewWorks != 0 || sum.SkippedDuplicateIdentity != 0 {
			t.Errorf("NewWorks = %d, SkippedDuplicateIdentity = %d; want the row merged into %s",
				sum.NewWorks, sum.SkippedDuplicateIdentity, w.ID)
		}
		if entryExists(t, dataDir, workAddr(alias)) {
			t.Errorf("a work was minted at the alias %q", alias)
		}
		if !workHasASIN(t, dataDir, w.ID, "B0ALIAS001") {
			t.Error("the row's ASIN is not on any recording of the retitled work")
		}
		assertTreeValid(t, dataDir)
	})
}
