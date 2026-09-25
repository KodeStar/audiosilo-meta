package importer

import (
	"slices"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// TestRoleCreditsResolveAsTheCreatePathDoes pins workCredits to the same
// resolution: a role credit names the record the create path would give that
// name, never a record reached only by the slug-level initials decision.
//
// "Ab Kovacs" slugs to ab-kovacs, a slug the batch decided onto a-b-kovacs - but
// "Ab" is a word, not initials, so the create path keeps it apart
// (initialsMerge). Its translator credit must therefore not be written onto the
// A.B. Kovacs record; with no ab-kovacs record to name, it is dropped, which is
// what enrichment does for any credit naming a person the catalogue lacks. The
// genuine initials spelling "AB Kovacs" still lands on the decided record.
func TestRoleCreditsResolveAsTheCreatePathDoes(t *testing.T) {
	p := probePlanner(t, "A.B. Kovacs", "A.B. Kovacs", "AB Kovacs", "Ab Kovacs")
	if got := p.getOrCreatePerson("A.B. Kovacs", func(string, ...any) {}); got != "a-b-kovacs" {
		t.Fatalf("A.B. Kovacs resolved to %q, want a-b-kovacs", got)
	}

	got := p.workCredits([]credit{
		{name: "Ab Kovacs", roles: []string{"translator"}},
		{name: "AB Kovacs", roles: []string{"editor"}},
	})
	want := []model.Credit{{Person: "a-b-kovacs", Role: "editor"}}
	if !slices.Equal(got, want) {
		t.Errorf("workCredits = %v, want %v: Ab Kovacs is not the decided A.B. Kovacs", got, want)
	}

	// Once the create path has created Ab Kovacs, the credit names that record.
	if slug := p.getOrCreatePerson("Ab Kovacs", func(string, ...any) {}); slug != "ab-kovacs" {
		t.Fatalf("Ab Kovacs created at %q, want ab-kovacs", slug)
	}
	got = p.workCredits([]credit{{name: "Ab Kovacs", roles: []string{"translator"}}})
	if want := []model.Credit{{Person: "ab-kovacs", Role: "translator"}}; !slices.Equal(got, want) {
		t.Errorf("workCredits = %v, want %v", got, want)
	}
}

// TestReadOnlyAuthorsMatchTheCreatedOnes pins the premise the duplicate-identity
// guard and the create path share: for every row, the author set resolved
// READ-ONLY before the row's people exist (rowWorkAuthorsRO, which the guard and
// the pre-passes read) is exactly the set the create path then mints
// (rowWorkAuthors). Any gap re-opens the guard/create disagreement issue #2337
// was: the guard would judge one author set and the create path act on another.
//
// The rows run in sequence over ONE planner, so later rows meet people earlier
// rows minted, and the batch carries an initials decision - including the name
// that used to split the two ("Ab Kovacs": its slug is a decided variant, but
// its own initials groups do not merge, so the create path mints ab-kovacs while
// the old slug-only lookup read a-b-kovacs).
func TestReadOnlyAuthorsMatchTheCreatedOnes(t *testing.T) {
	rows := [][]string{
		{"A.B. Kovacs"},
		{"AB Kovacs", "Jane Doe"},
		{"Ab Kovacs"},
		{"A. B. Kovacs", "Jane Doe"},
		{"Jane Doe", "E. M. Brown"},
		{"Em Brown"},
		{"EM Brown", "J. R. R. Tolkien"},
		{"JRR Tolkien"},
	}
	var batch []string
	for _, r := range rows {
		batch = append(batch, r...)
	}
	// The majority spelling for the Kovacs group is the separated one, so the
	// decision maps ab-kovacs onto a-b-kovacs.
	batch = append(batch, "A.B. Kovacs", "A.B. Kovacs")
	p := probePlanner(t, batch...)

	for i, names := range rows {
		credits := make([]credit, len(names))
		for j, n := range names {
			credits[j] = credit{name: n}
		}
		ro := p.rowWorkAuthorsRO(credits)
		created := p.rowWorkAuthors(credits, func(string, ...any) {})
		if !slices.Equal(ro.all, created.all) || !slices.Equal(ro.identity, created.identity) {
			t.Errorf("row %d %q: read-only resolved %v/%v, the create path minted %v/%v",
				i, names, ro.all, ro.identity, created.all, created.identity)
		}
		// And once minted, the read-only view still names the same people.
		if again := p.rowWorkAuthorsRO(credits); !slices.Equal(again.all, created.all) {
			t.Errorf("row %d %q: after minting, read-only resolved %v, want %v", i, names, again.all, created.all)
		}
	}
}
