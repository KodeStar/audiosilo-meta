package importer

import (
	"testing"
)

// TestMarkedKeyGroupsInitialsSpellings pins the three spellings of one initials
// group onto one key, and - the load-bearing half - keeps a mixed-case short
// word off it. Every "must not" pair below is a real pair of DIFFERENT people in
// the dump that the naive slug-level collapse merged.
func TestMarkedKeyGroupsInitialsSpellings(t *testing.T) {
	same := [][2]string{
		{"A.B. Kovacs", "AB Kovacs"},
		{"A. B. Kovacs", "AB Kovacs"},
		{"A B Kovacs", "A.B. Kovacs"},
		{"J. R. R. Tolkien", "JRR Tolkien"},
		{"J.R.R. Tolkien", "JRR Tolkien"},
		{"H. G. Wells", "HG Wells"},
		{"E. M. Kaplan", "EM Kaplan"},
		{"L. E. Barbant", "LE Barbant"},
		{"Deepak Chopra M.D.", "Deepak Chopra MD"},
		{"J C H Rigby", "JCH Rigby"},
		{"Lawrence W. Gold M.D.", "Lawrence W. Gold MD"},
	}
	for _, pair := range same {
		if a, b := markedKey(pair[0]), markedKey(pair[1]); a != b {
			t.Errorf("markedKey(%q) = %q, markedKey(%q) = %q; want the same key", pair[0], a, pair[1], b)
		}
	}

	differ := [][2]string{
		{"E. M. Brown", "Em Brown"},
		{"E. D. Baker", "Ed Baker"},
		{"E. D. Martin", "Ed Martin"},
		{"A.L. Brooks", "Al Brooks"},
		{"A.L. Smith", "Al Smith"},
		{"A.L. Gordon", "Al Gordon"},
		{"A. B. Jackson", "Ab Jackson"},
		{"X E Sands", "Xe Sands"},
		{"M. R. James", "Mr James"},
		// An entirely-uppercase name carries no case evidence, so nothing in it
		// is read as an initials cluster.
		{"AB KOVACS", "A.B. Kovacs"},
	}
	for _, pair := range differ {
		if a, b := markedKey(pair[0]), markedKey(pair[1]); a == b {
			t.Errorf("markedKey(%q) == markedKey(%q) == %q; a short mixed-case word is not an initials group", pair[0], pair[1], a)
		}
	}
}

// TestInitialsVariantSlugs pins the probe's addresses: the OTHER spellings'
// slugs, never the name's own, and nothing at all for a name with no initials.
func TestInitialsVariantSlugs(t *testing.T) {
	cases := []struct {
		name string
		want []string
	}{
		{"A.B. Kovacs", []string{"ab-kovacs"}},
		{"AB Kovacs", []string{"a-b-kovacs"}},
		{"J. R. R. Tolkien", []string{"jrr-tolkien"}},
		{"Em Brown", nil},
		{"Ramon de Ocampo", nil},
	}
	for _, tc := range cases {
		got := initialsVariantSlugs(tc.name)
		if len(got) != len(tc.want) {
			t.Errorf("initialsVariantSlugs(%q) = %v, want %v", tc.name, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("initialsVariantSlugs(%q) = %v, want %v", tc.name, got, tc.want)
				break
			}
		}
	}
}

// TestInitialsProbeMergesSpellings is the rule at the point it acts: a planner
// that already holds one spelling must resolve the other onto it, in BOTH
// directions (the probe is symmetric), and must never do so across the
// initials/short-word boundary.
func TestInitialsProbeMergesSpellings(t *testing.T) {
	// The twenty cases the report's validation names: ten pairs that are one
	// person and must merge onto whichever spelling is already recorded, and
	// nine adversarial pairs that are two people and must not.
	merge := [][2]string{
		{"AB Kovacs", "A.B. Kovacs"},
		{"AJ Newman", "A J Newman"},
		{"AK Alliss", "A.K. Alliss"},
		{"AJ Powers", "A.J. Powers"},
		{"A J Spencer", "AJ Spencer"},
		{"JRR Tolkien", "J. R. R. Tolkien"},
		{"HG Wells", "H. G. Wells"},
		{"EM Kaplan", "E. M. Kaplan"},
		{"LE Barbant", "L. E. Barbant"},
		{"Deepak Chopra MD", "Deepak Chopra M.D."},
	}
	for _, pair := range merge {
		for _, order := range [][2]string{{pair[0], pair[1]}, {pair[1], pair[0]}} {
			first, second := order[0], order[1]
			t.Run(first+" then "+second, func(t *testing.T) {
				p := probePlanner(t)
				kept := p.getOrCreatePerson(first, func(string, ...any) {})
				got := p.getOrCreatePerson(second, func(string, ...any) {})
				if got != kept {
					t.Errorf("%q resolved to %q, want the record %q already minted (%q)", second, got, first, kept)
				}
				if p.summary.NewPeople != 1 {
					t.Errorf("NewPeople = %d, want 1 - the second spelling minted a duplicate", p.summary.NewPeople)
				}
			})
		}
	}

	apart := [][2]string{
		{"E. M. Brown", "Em Brown"},
		{"E. D. Baker", "Ed Baker"},
		{"E. D. Martin", "Ed Martin"},
		{"A.L. Brooks", "Al Brooks"},
		{"A.L. Smith", "Al Smith"},
		{"A.L. Gordon", "Al Gordon"},
		{"A. B. Jackson", "Ab Jackson"},
		{"X E Sands", "Xe Sands"},
		{"M. R. James", "Mr James"},
	}
	for _, pair := range apart {
		for _, order := range [][2]string{{pair[0], pair[1]}, {pair[1], pair[0]}} {
			first, second := order[0], order[1]
			t.Run(first+" vs "+second, func(t *testing.T) {
				p := probePlanner(t)
				kept := p.getOrCreatePerson(first, func(string, ...any) {})
				got := p.getOrCreatePerson(second, func(string, ...any) {})
				if got == kept {
					t.Errorf("%q merged into %q; a short mixed-case given name is not an initials group", second, first)
				}
				if p.summary.NewPeople != 2 {
					t.Errorf("NewPeople = %d, want 2 - these are different people", p.summary.NewPeople)
				}
			})
		}
	}
}

// probePlanner is the smallest planner getOrCreatePerson needs: an identity map
// and a write layer over an empty tree. Nothing is flushed - what the probe test
// observes is which slug the second spelling resolves to.
func probePlanner(t *testing.T) *planner {
	t.Helper()
	dir := t.TempDir()
	store, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	return &planner{
		dataDir:   dir,
		people:    map[string]string{},
		store:     store,
		curSource: OutSource{Type: sourceLibex, Ref: "B0TEST0000", ImportedAt: testImportDate},
	}
}

// TestLibexImportMergesInitialsSpellings is the end-to-end proof: two rows
// spelling one narrator's initials differently produce ONE person record, and
// the row order does not decide anything but which spelling is recorded.
func TestLibexImportMergesInitialsSpellings(t *testing.T) {
	const rows = `{"asin":"B0TEST1001","title":"Alpha","region":"us","language":"english","bookFormat":"unabridged","releaseDate":"2024-01-01 00:00:00+00","lengthMinutes":100,"authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":"AB Kovacs"}],"genres":[],"series":[]}
{"asin":"B0TEST1002","title":"Beta","region":"us","language":"english","bookFormat":"unabridged","releaseDate":"2024-01-02 00:00:00+00","lengthMinutes":100,"authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":"A.B. Kovacs"}],"genres":[],"series":[]}
{"asin":"B0TEST1003","title":"Gamma","region":"us","language":"english","bookFormat":"unabridged","releaseDate":"2024-01-03 00:00:00+00","lengthMinutes":100,"authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":"Em Brown"}],"genres":[],"series":[]}
{"asin":"B0TEST1004","title":"Delta","region":"us","language":"english","bookFormat":"unabridged","releaseDate":"2024-01-04 00:00:00+00","lengthMinutes":100,"authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":"E. M. Brown"}],"genres":[],"series":[]}`

	_, dataDir := runLibex(t, rows, false)

	if !entryExists(t, dataDir, "people/ab/ab-kovacs.json") {
		t.Error("the first spelling has no person record")
	}
	if entryExists(t, dataDir, "people/a-/a-b-kovacs.json") {
		t.Error("the second initials spelling minted a duplicate person record")
	}
	// The adversarial pair stays two people: "Em" is a given name, not initials.
	if !entryExists(t, dataDir, "people/em/em-brown.json") || !entryExists(t, dataDir, "people/e-/e-m-brown.json") {
		t.Error("Em Brown and E. M. Brown were not both kept; they are different people")
	}
}
