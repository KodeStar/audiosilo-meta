package check

import (
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// identity_test.go covers the index the two WRITERS probe (internal/importer's
// create guard and internal/issueform's intake gate) rather than the tree census
// that shares it: what they ask is "would this incoming record duplicate one we
// hold", and the answer has to be the same on both sides of the seam.

// identityCatalog is a two-work catalogue: a series volume under its plain title,
// and a work whose author list carries a role-credited translator.
func identityCatalog() *model.Catalog {
	return &model.Catalog{
		Works: []*model.Work{
			{ID: "hammered", Title: "Hammered", Language: "en", Authors: []string{"kevin-hearne"}},
			{
				ID: "the-blood-of-elves", Title: "The Blood of Elves", Language: "en",
				Authors: []string{"andrzej-sapkowski", "danuta-stok"},
				Credits: []model.Credit{{Person: "danuta-stok", Role: "translator"}},
			},
		},
		Series: []*model.Series{{
			ID: "the-iron-druid-chronicles", Name: "The Iron Druid Chronicles",
			Works: []model.SeriesWork{{Work: "hammered", Position: "3"}},
		}},
	}
}

func set(ids ...string) map[string]bool {
	out := map[string]bool{}
	for _, id := range ids {
		out[id] = true
	}
	return out
}

func TestWorkIdentityMatch(t *testing.T) {
	ix := NewWorkIdentity(identityCatalog())

	// The decorated spelling of a catalogued book, cleaned against the series it
	// names, meets the record stored under the plain title.
	got := ix.Match("Hammered: The Iron Druid Chronicles, Book 3", "The Iron Druid Chronicles", "en",
		set("kevin-hearne"), set("kevin-hearne"))
	if len(got) != 1 || got[0].Work.ID != "hammered" {
		t.Fatalf("Match = %v, want the hammered record", got)
	}

	// The author-SUPERSET shape: the submission names only the novelist and the
	// record also lists a role-credited translator, so only the nesting rule sees it.
	got = ix.Match("Blood of Elves", "", "en", set("andrzej-sapkowski"), set("andrzej-sapkowski"))
	if len(got) != 1 || got[0].Work.ID != "the-blood-of-elves" {
		t.Fatalf("Match = %v, want the blood-of-elves record", got)
	}

	// Another author's book of the same name is another work.
	if got := ix.Match("Hammered", "", "en", set("elizabeth-bear"), set("elizabeth-bear")); len(got) != 0 {
		t.Errorf("Match on another author = %v, want none", got)
	}
	// A translation is a different work.
	if got := ix.Match("Hammered", "", "de", set("kevin-hearne"), set("kevin-hearne")); len(got) != 0 {
		t.Errorf("Match across languages = %v, want none", got)
	}
	// A pair whose titles BOTH state a volume, differently, is a serial's siblings.
	if got := ix.Match("Bravelands, Book 4", "", "en", set("kevin-hearne"), set("kevin-hearne")); len(got) != 0 {
		t.Errorf("Match on another stated volume = %v, want none", got)
	}
	// But a volume stated on ONE side only still matches, deliberately: "Hammered"
	// beside "Hammered, Book 4" is the duplicate shape the gates exist for, and which
	// volume a plain title is can only be answered by the series record - so the
	// POSITION veto belongs to the caller (internal/importer's seriesClaim.compatible,
	// internal/issueform's series-volume gate), not to a title comparison.
	if got := ix.Match("Hammered: The Iron Druid Chronicles, Book 4", "The Iron Druid Chronicles", "en",
		set("kevin-hearne"), set("kevin-hearne")); len(got) != 1 {
		t.Errorf("Match with a volume stated on one side = %v, want the hammered record", got)
	}
	// An unknown language never separates - the same posture every other rule takes.
	if got := ix.Match("Hammered", "", "", set("kevin-hearne"), set("kevin-hearne")); len(got) != 1 {
		t.Errorf("Match with no stated language = %v, want the hammered record", got)
	}
}

// The derivations the index exposes: which series a work's title was read against,
// and which catalogued series a free-text title names.
func TestWorkIdentityDerivations(t *testing.T) {
	ix := NewWorkIdentity(identityCatalog())

	if got := ix.SeriesNameOf("hammered"); got != "The Iron Druid Chronicles" {
		t.Errorf("SeriesNameOf(hammered) = %q", got)
	}
	if got := ix.SeriesNameOf("the-blood-of-elves"); got != "" {
		t.Errorf("SeriesNameOf on a series-less work = %q, want empty", got)
	}
	if got := ix.KeyOf("hammered"); got == "" || got != ix.Key("Hammered", "The Iron Druid Chronicles") {
		t.Errorf("KeyOf(hammered) = %q, which is not the key rule's answer", got)
	}
	name, id, ok := ix.SeriesNameIn("Hammered: The Iron Druid Chronicles, Book 3")
	if !ok || name != "The Iron Druid Chronicles" || id != "the-iron-druid-chronicles" {
		t.Errorf("SeriesNameIn = (%q, %q, %v)", name, id, ok)
	}
	if _, _, ok := ix.SeriesNameIn("A Book About Nothing In Particular"); ok {
		t.Error("SeriesNameIn matched a title naming no catalogued series")
	}
}

// IdentityEqualWorks must stay IdentityAuthorsMatch with one side read off a work:
// the nesting rule has one implementation, and a writer holding an incoming row must
// get the same answer as a reader holding two records.
func TestIdentityEqualWorksIsTheAuthorsMatch(t *testing.T) {
	a := &model.Work{ID: "a", Authors: []string{"one"}}
	b := &model.Work{
		ID: "b", Authors: []string{"one", "two"},
		Credits: []model.Credit{{Person: "two", Role: "translator"}},
	}
	if !IdentityEqualWorks(a, b) {
		t.Fatal("the role-credit fork must be identity-equal")
	}
	if !IdentityAuthorsMatch(b, set("one"), set("one")) {
		t.Error("the same pair judged from the row side disagreed")
	}
	// A mutual translation stays two works: the identities are disjoint.
	x := &model.Work{ID: "x", Authors: []string{"ada", "bea"}, Credits: []model.Credit{{Person: "bea", Role: "translator"}}}
	y := &model.Work{ID: "y", Authors: []string{"ada", "bea"}, Credits: []model.Credit{{Person: "ada", Role: "translator"}}}
	if IdentityEqualWorks(x, y) {
		t.Error("a mutual translation must not be identity-equal")
	}
	if IdentityAuthorsMatch(y, set("ada", "bea"), set("ada")) {
		t.Error("the mutual translation disagreed from the row side")
	}
}

// A nil catalogue yields a usable empty index rather than a nil pointer: the
// importer builds it off a best-effort load that can find nothing.
func TestWorkIdentityToleratesAnEmptyCatalog(t *testing.T) {
	ix := NewWorkIdentity(nil)
	if len(ix.Keys()) != 0 {
		t.Errorf("Keys on an empty index = %v", ix.Keys())
	}
	if got := ix.Match("Hammered", "", "en", set("a"), set("a")); len(got) != 0 {
		t.Errorf("Match on an empty index = %v", got)
	}
}
