package audit

import (
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/importer"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// peopleFixture seeds a catalogue whose only interesting content is its people.
// Every one of them is credited as an author of a work of their own, so the counts
// a P-DUP record cites are non-zero and pkg/check raises no orphan advisory.
func peopleFixture(t testing.TB, names ...string) map[string]string {
	t.Helper()
	out := map[string]string{
		"people/na/nate-narrator.json": personJSON(t, "nate-narrator", "Nate Narrator"),
	}
	for i, name := range names {
		slug, _ := model.PersonSlug(name)
		out["people/xx/"+slug+".json"] = personJSON(t, slug, name)
		id := "book-" + string(rune('a'+i))
		out["works/xx/"+id+"/work.json"] = workJSON(t, id, "Book "+strings.ToUpper(string(rune('A'+i))), withAuthors(slug))
		out["works/xx/"+id+"/recordings/r-"+id+".json"] = recJSON(t, "r-"+id, id)
	}
	return out
}

// The initials rule is the importer's own: two spellings meet only when their
// MARKED keys agree, which needs case evidence.
func TestPersonDupGroupsInitialsSpellings(t *testing.T) {
	rep := runFixture(t, peopleFixture(t, "C. J. Critt", "CJ Critt"))
	got := subclassOf(t, rep, ClassPersonDup, pDupInitials)
	if len(got) != 1 {
		t.Fatalf("want one initials-spelling record, got %d: %+v", len(got), classOf(t, rep, ClassPersonDup))
	}
	if len(got[0].People) != 2 {
		t.Fatalf("record cites %d people, want 2", len(got[0].People))
	}
	for _, p := range got[0].People {
		if p.AuthorOf != 1 {
			t.Errorf("%s: author_of = %d, want 1 - triage needs the counts", p.ID, p.AuthorOf)
		}
	}
	// Typed, not prose: the class is advisory and proposes no mechanical action.
	if !got[0].Propose.Advisory || got[0].Propose.Op != OpReview {
		t.Errorf("P-DUP must be advisory review, got op %q advisory %v", got[0].Propose.Op, got[0].Propose.Advisory)
	}
	if got[0].Propose.Target != "" {
		t.Errorf("P-DUP must propose no canonical member, got %q", got[0].Propose.Target)
	}
}

// The gate the importer's rule turns on: a mixed-case short token is a real given
// name, so "E. M. Brown" must not meet "Em Brown".
func TestPersonDupKeepsInitialsOffASameSpelledGivenName(t *testing.T) {
	rep := runFixture(t, peopleFixture(t, "E. M. Brown", "Em Brown"))
	if got := subclassOf(t, rep, ClassPersonDup, pDupInitials); len(got) != 0 {
		t.Errorf("initials met a given name: %+v", got)
	}
}

func TestPersonDupGroupsPunctuationOnlyVariants(t *testing.T) {
	rep := runFixture(t, peopleFixture(t, "Amber Lee Connors", "AmberLee Connors"))
	got := subclassOf(t, rep, ClassPersonDup, pDupTight)
	if len(got) != 1 {
		t.Fatalf("want one punctuation-only record, got %d: %+v", len(got), classOf(t, rep, ClassPersonDup))
	}
	if len(got[0].People) != 2 {
		t.Errorf("record cites %d people, want 2", len(got[0].People))
	}
}

// A pair already grouped by the marked key is not reported twice under the coarser
// punctuation key.
func TestPersonDupDoesNotReportOnePairTwice(t *testing.T) {
	rep := runFixture(t, peopleFixture(t, "C. J. Critt", "CJ Critt"))
	if got := subclassOf(t, rep, ClassPersonDup, pDupTight); len(got) != 0 {
		t.Errorf("an initials pair was reported again as punctuation-only: %+v", got)
	}
}

// The calibration shape: one edit apart, sharing a first token, long enough for a
// typo to be the likelier explanation than two people.
func TestPersonDupGroupsNamesOneEditApart(t *testing.T) {
	rep := runFixture(t, peopleFixture(t, "Rachel Rene Russell", "Rachel Renee Russell"))
	got := subclassOf(t, rep, ClassPersonDup, pDupEdit1)
	if len(got) != 1 {
		t.Fatalf("want one edit-distance-1 record, got %d: %+v", len(got), classOf(t, rep, ClassPersonDup))
	}
	if got[0].Propose.Target != "" {
		t.Errorf("an advisory record must propose nothing, got canonical %q", got[0].Propose.Target)
	}
	if !got[0].Propose.Advisory || !strings.Contains(got[0].Propose.Reason, "false-positive") {
		t.Errorf("propose = %+v, want an advisory stating the false-positive risk", got[0].Propose)
	}
}

func TestPersonDupNeedsASharedFirstToken(t *testing.T) {
	// One edit apart in the FIRST name, so the buckets differ and nothing meets.
	rep := runFixture(t, peopleFixture(t, "Rachel Renee Russell", "Rachil Renee Russell"))
	if got := subclassOf(t, rep, ClassPersonDup, pDupEdit1); len(got) != 0 {
		t.Errorf("two names with different first tokens were paired: %+v", got)
	}
}

func TestPersonDupIgnoresShortNames(t *testing.T) {
	// "Jo Ray" folds to "joray" (5 chars), under minEditDistanceLen - two real
	// people are far likelier than a typo at that length.
	rep := runFixture(t, peopleFixture(t, "Jo Ray", "Jo Roy"))
	if got := subclassOf(t, rep, ClassPersonDup, pDupEdit1); len(got) != 0 {
		t.Errorf("two short names were paired: %+v", got)
	}
}

func TestPersonDupIsSilentOnUnrelatedPeople(t *testing.T) {
	rep := runFixture(t, peopleFixture(t, "Jane Doe", "Bram Stoker", "Luke Daniels"))
	if got := classOf(t, rep, ClassPersonDup); len(got) != 0 {
		t.Errorf("unrelated people were grouped: %+v", got)
	}
}

// The audit and the importer must never disagree about which two spellings are one
// person, which is why the key the P-DUP index builds on is the importer's own
// (importer.MarkedNameKey -> markedKey, the rule the importer MERGES spellings on).
// This test lived in internal/titlerule while that package re-exported the key; it
// moved here with the re-export's removal, to the consumer that actually asks.
func TestMarkedNameKeyIsTheImportersRule(t *testing.T) {
	for _, c := range []struct {
		a, b string
		same bool
	}{
		{"C. J. Critt", "CJ Critt", true},
		{"J.D. Franx", "JD Franx", true},
		{"E. M. Brown", "Em Brown", false},
		{"Mr. James", "M. R. James", false},
	} {
		if got := importer.MarkedNameKey(c.a) == importer.MarkedNameKey(c.b); got != c.same {
			t.Errorf("MarkedNameKey(%q) == MarkedNameKey(%q) is %v, want %v", c.a, c.b, got, c.same)
		}
	}
}
