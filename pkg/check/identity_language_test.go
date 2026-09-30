package check

import (
	"slices"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// SameBookInAnyLanguage is matches with the language rule removed, and nothing else:
// the other three rules still hold across languages, and the language rule still holds
// for Match (so the census and the writers are untouched).
func TestSameBookInAnyLanguage(t *testing.T) {
	cat := &model.Catalog{Works: []*model.Work{
		{ID: "fate", Title: "A Game of Fate", Language: "en", Authors: []string{"jane-doe"}},
		{ID: "fate-fr", Title: "A Game of Fate (French Edition)", Language: "fr",
			Authors: []string{"jane-doe", "tina"}, Credits: []model.Credit{{Person: "tina", Role: "translator"}}},
		{ID: "fate-2-de", Title: "A Game of Fate, Book 2 (German Edition)", Language: "de", Authors: []string{"jane-doe"}},
		{ID: "fate-3-de", Title: "A Game of Fate, Book 3 (German Edition)", Language: "de", Authors: []string{"jane-doe"}},
		{ID: "fate-box", Title: "A Game of Fate: The Complete Boxed Set", Language: "es", Authors: []string{"jane-doe"}},
		{ID: "fate-other", Title: "A Game of Fate", Language: "it", Authors: []string{"john-roe"}},
	}}
	ix := NewWorkIdentity(cat)
	byID := map[string]*model.Work{}
	for _, w := range cat.Works {
		byID[w.ID] = w
	}
	ids := func(ms []IdentityMatch) []string {
		out := make([]string, 0, len(ms))
		for _, m := range ms {
			out = append(out, m.Work.ID)
		}
		return out
	}

	// The French edition meets the English record across the language line, and the
	// volume-stating German records (whose one-sided silence agrees with it) too; the
	// boxed set (a collection) and another author's book do not.
	if got, want := ids(ix.SameBookInAnyLanguage(byID["fate-fr"])), []string{"fate", "fate-2-de", "fate-3-de"}; !slices.Equal(got, want) {
		t.Errorf("SameBookInAnyLanguage(fate-fr) = %v, want %v", got, want)
	}
	// Two stated volumes that differ are two books, in any language.
	if got := ids(ix.SameBookInAnyLanguage(byID["fate-2-de"])); slices.Contains(got, "fate-3-de") {
		t.Errorf("volume 2 met volume 3: %v", got)
	}
	// The language-bound predicate is unchanged: a Portuguese record of the book is a
	// duplicate of nothing the catalogue holds, although every language's record meets it
	// above.
	if got := ix.Match("A Game of Fate (Portuguese Edition)", "", "pt", set("jane-doe"), set("jane-doe")); len(got) != 0 {
		t.Errorf("Match across languages = %v, want nothing", got)
	}
	// A work the index was not built over has no derivation to ask.
	if got := ix.SameBookInAnyLanguage(&model.Work{ID: "stranger", Title: "A Game of Fate"}); got != nil {
		t.Errorf("an uncatalogued work matched %v", got)
	}
}
