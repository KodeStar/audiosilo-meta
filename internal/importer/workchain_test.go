package importer

import (
	"strings"
	"testing"

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
