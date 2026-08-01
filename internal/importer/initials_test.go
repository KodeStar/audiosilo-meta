package importer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
)

// The report's validation corpus, shared by the key-level test and the
// probe-level test below so the two can never disagree about which pairs are
// which. Every samePair is one person spelling their initials two ways; every
// differentPair is a real pair of DIFFERENT people that a lazier rule merged.
var (
	initialsSamePairs = [][2]string{
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
		// A hyphenated surname is the same person spelled two ways too - see
		// TestInitialsVariantSlugs for the address half of that.
		{"A.B. Hyde-White", "AB Hyde-White"},
	}

	// initialsDifferentPairs is the adversarial half. The first nine are the
	// naive slug-level collapse's victims; the last five are the DOTTED
	// honorifics, which have the exact shape of a dotted initials cluster
	// (letters and dots, nothing else) and are separated from it by case
	// evidence alone. Without that gate a catalogue "Mr. James" absorbs the real
	// M. R. James - 246 credits in the dump - and 61 committed records sit on
	// one of these five spellings.
	initialsDifferentPairs = [][2]string{
		{"E. M. Brown", "Em Brown"},
		{"E. D. Baker", "Ed Baker"},
		{"E. D. Martin", "Ed Martin"},
		{"A.L. Brooks", "Al Brooks"},
		{"A.L. Smith", "Al Smith"},
		{"A.L. Gordon", "Al Gordon"},
		{"A. B. Jackson", "Ab Jackson"},
		{"X E Sands", "Xe Sands"},
		{"M. R. James", "Mr James"},
		{"M. R. James", "Mr. James"},
		{"D. R. Ford", "Dr. Ford"},
		{"S. T. James", "St. James"},
		{"J. R. Tolkien", "Jr. Tolkien"},
		{"M. S. Read", "Ms. Read"},
	}
)

// TestMarkedKeyGroupsInitialsSpellings pins the three spellings of one initials
// group onto one key, and - the load-bearing half - keeps a mixed-case short
// word or a dotted honorific off it.
func TestMarkedKeyGroupsInitialsSpellings(t *testing.T) {
	for _, pair := range initialsSamePairs {
		if a, b := markedKey(pair[0]), markedKey(pair[1]); a != b {
			t.Errorf("markedKey(%q) = %q, markedKey(%q) = %q; want the same key", pair[0], a, pair[1], b)
		}
	}

	differ := append(slices.Clone(initialsDifferentPairs),
		// An entirely-uppercase name carries no case evidence, so nothing in it
		// is read as an initials cluster. This one is key-level only: the probe
		// test drives a planner, where the two spellings ALSO stay apart, but
		// the reason is worth pinning where the key is computed.
		[2]string{"AB KOVACS", "A.B. Kovacs"})
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
		// The word tokens are rendered from the RAW spelling, so the hyphen
		// Slugify keeps survives into the variant. Rendering them from the
		// comparison letters probed "ab-hydewhite", which nothing ever mints.
		{"A.B. Hyde-White", []string{"ab-hyde-white"}},
		{"AB Hyde-White", []string{"a-b-hyde-white"}},
		{"Em Brown", nil},
		{"Ramon de Ocampo", nil},
		// A dotted honorific is a word, so the name has no group to respell and
		// no address to probe.
		{"Mr. James", nil},
		{"Dr. Ford", nil},
	}
	for _, tc := range cases {
		if got := initialsVariantSlugs(tc.name); !slices.Equal(got, tc.want) {
			t.Errorf("initialsVariantSlugs(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestInitialsProbeMergesSpellings is the rule at the point it acts: a run whose
// batch carries both spellings of one person must resolve them onto ONE record,
// in either arrival order, and must never do so across the initials/short-word
// boundary.
func TestInitialsProbeMergesSpellings(t *testing.T) {
	// The report's validation set: ten pairs that are one person and must merge
	// onto whichever spelling the pre-pass decided, and the adversarial pairs
	// that are two people and must not. Both are driven through the same loop,
	// in both orders, so "merges" and "stays apart" are the SAME assertion with
	// the expectation flipped.
	mergePairs := [][2]string{
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
		{"AB Hyde-White", "A.B. Hyde-White"},
	}

	for _, group := range []struct {
		pairs     [][2]string
		wantMerge bool
	}{
		{mergePairs, true},
		{initialsDifferentPairs, false},
	} {
		for _, pair := range group.pairs {
			for _, order := range [][2]string{{pair[0], pair[1]}, {pair[1], pair[0]}} {
				first, second := order[0], order[1]
				t.Run(first+" then "+second, func(t *testing.T) {
					p := probePlanner(t, first, second)
					kept := p.getOrCreatePerson(first, func(string, ...any) {})
					got := p.getOrCreatePerson(second, func(string, ...any) {})

					if merged := got == kept; merged != group.wantMerge {
						if group.wantMerge {
							t.Errorf("%q resolved to %q, want the record %q resolved to (%q)", second, got, first, kept)
						} else {
							t.Errorf("%q merged into %q; these are two different people", second, first)
						}
					}
					wantPeople := 2
					if group.wantMerge {
						wantPeople = 1
					}
					if p.summary.NewPeople != wantPeople {
						t.Errorf("NewPeople = %d, want %d", p.summary.NewPeople, wantPeople)
					}
				})
			}
		}
	}
}

// TestInitialsSurvivorIsDecidedNotFirstSeen pins WHICH spelling survives, which
// is the half that used to depend on the order the rows arrived in (and so on
// where a `split -l` cut the input).
func TestInitialsSurvivorIsDecidedNotFirstSeen(t *testing.T) {
	cases := []struct {
		name      string
		catalogue map[string]string
		batch     []string
		want      string
		wantName  string
	}{{
		name:     "the batch majority wins",
		batch:    []string{"AB Kovacs", "AB Kovacs", "A.B. Kovacs"},
		want:     "ab-kovacs",
		wantName: "AB Kovacs",
	}, {
		name:  "the majority wins from the other side too",
		batch: []string{"AB Kovacs", "A.B. Kovacs", "A. B. Kovacs"},
		want:  "a-b-kovacs",
		// Two rows spell the winning SLUG, one each way, so the record's name is
		// the lexicographically first of the two - decided, not first-seen.
		wantName: "A. B. Kovacs",
	}, {
		name: "a catalogue spelling beats the batch majority",
		// Three rows spell it "AB Kovacs" and one "A.B. Kovacs", but the
		// catalogue already holds the separated form: an existing id never moves.
		catalogue: map[string]string{"a-b-kovacs": "A.B. Kovacs"},
		batch:     []string{"AB Kovacs", "AB Kovacs", "AB Kovacs", "A.B. Kovacs"},
		want:      "a-b-kovacs",
		wantName:  "A.B. Kovacs",
	}, {
		name:     "a tie is broken by slug order, never by arrival",
		batch:    []string{"A.B. Kovacs", "AB Kovacs"},
		want:     "a-b-kovacs",
		wantName: "A.B. Kovacs",
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newInitialsCensus()
			for slug, name := range tc.catalogue {
				c.addCatalogue(slug, name)
			}
			for _, name := range tc.batch {
				c.addBatch(name)
			}
			survivors := c.decide()
			got, decided := survivors[Slugify(tc.batch[0])]
			if !decided {
				t.Fatalf("no decision for %q", tc.batch[0])
			}
			if got.slug != tc.want || got.name != tc.wantName {
				t.Errorf("survivor = %q/%q, want %q/%q", got.slug, got.name, tc.want, tc.wantName)
			}
		})
	}
}

// TestInitialsSingleSpellingIsNotDecided pins the absence half: a group written
// only one way has nothing to decide, so nothing is recorded and the name is
// minted exactly as it always was.
func TestInitialsSingleSpellingIsNotDecided(t *testing.T) {
	c := newInitialsCensus()
	c.addBatch("AB Kovacs")
	c.addBatch("AB Kovacs")
	c.addBatch("Mr. James")
	if got := c.decide(); len(got) != 0 {
		t.Errorf("decide() = %v, want no decisions", got)
	}
}

// probePlanner is the smallest planner getOrCreatePerson needs: an identity map,
// a write layer over an empty tree, and the batch pre-pass's decision over the
// names the test is about to import. Nothing is flushed - what the probe test
// observes is which slug each spelling resolves to.
func probePlanner(t *testing.T, batch ...string) *planner {
	t.Helper()
	dir := t.TempDir()
	store, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	c := newInitialsCensus()
	for _, name := range batch {
		c.addBatch(name)
	}
	return &planner{
		dataDir:           dir,
		people:            map[string]string{},
		store:             store,
		initialsSurvivors: c.decide(),
		curSource:         OutSource{Type: sourceLibex, Ref: "B0TEST0000", ImportedAt: testImportDate},
	}
}

// TestLibexImportMergesInitialsSpellings is the end-to-end proof: two rows
// spelling one narrator's initials differently produce ONE person record, and
// the row order does not decide anything at all.
func TestLibexImportMergesInitialsSpellings(t *testing.T) {
	const rows = `{"asin":"B0TEST1001","title":"Alpha","region":"us","language":"english","bookFormat":"unabridged","releaseDate":"2024-01-01 00:00:00+00","lengthMinutes":100,"authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":"AB Kovacs"}],"genres":[],"series":[]}
{"asin":"B0TEST1002","title":"Beta","region":"us","language":"english","bookFormat":"unabridged","releaseDate":"2024-01-02 00:00:00+00","lengthMinutes":100,"authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":"A.B. Kovacs"}],"genres":[],"series":[]}
{"asin":"B0TEST1003","title":"Gamma","region":"us","language":"english","bookFormat":"unabridged","releaseDate":"2024-01-03 00:00:00+00","lengthMinutes":100,"authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":"Em Brown"}],"genres":[],"series":[]}
{"asin":"B0TEST1004","title":"Delta","region":"us","language":"english","bookFormat":"unabridged","releaseDate":"2024-01-04 00:00:00+00","lengthMinutes":100,"authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":"E. M. Brown"}],"genres":[],"series":[]}
{"asin":"B0TEST1005","title":"Epsilon","region":"us","language":"english","bookFormat":"unabridged","releaseDate":"2024-01-05 00:00:00+00","lengthMinutes":100,"authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":"Mr. James"}],"genres":[],"series":[]}
{"asin":"B0TEST1006","title":"Zeta","region":"us","language":"english","bookFormat":"unabridged","releaseDate":"2024-01-06 00:00:00+00","lengthMinutes":100,"authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":"M. R. James"}],"genres":[],"series":[]}`

	_, dataDir := runLibex(t, rows, false)

	// A tie, so the decision is the lower slug: the separated spelling.
	if !entryExists(t, dataDir, "people/a-/a-b-kovacs.json") {
		t.Error("the decided spelling has no person record")
	}
	if entryExists(t, dataDir, "people/ab/ab-kovacs.json") {
		t.Error("the other initials spelling minted a duplicate person record")
	}
	// The adversarial pairs stay two people: "Em" is a given name and "Mr." is
	// an honorific, neither of them initials.
	if !entryExists(t, dataDir, "people/em/em-brown.json") || !entryExists(t, dataDir, "people/e-/e-m-brown.json") {
		t.Error("Em Brown and E. M. Brown were not both kept; they are different people")
	}
	if !entryExists(t, dataDir, "people/mr/mr-james.json") || !entryExists(t, dataDir, "people/m-/m-r-james.json") {
		t.Error("Mr. James and M. R. James were not both kept; an honorific is not an initials group")
	}
}

// TestLibexImportKeepsTheRoleCreditAcrossAMerge is the finding that the merge
// dropped the credit it resolved: getOrCreatePerson returned the merged slug,
// but workCredits re-derived the person from the name and looked for a record
// under the OTHER spelling, which nothing had created. The role credit was
// silently skipped.
//
// Both catalogue spellings are driven through the same row, so the assertion is
// that the credit survives whichever spelling the identity lives under.
func TestLibexImportKeepsTheRoleCreditAcrossAMerge(t *testing.T) {
	for _, catalogue := range []struct{ slug, name string }{
		{"ab-kovacs", "AB Kovacs"},
		{"a-b-kovacs", "A.B. Kovacs"},
	} {
		t.Run(catalogue.slug, func(t *testing.T) {
			dataDir := t.TempDir()
			seedTree(t, dataDir, map[string]string{
				"people/" + shard(catalogue.slug) + "/" + catalogue.slug + ".json": fmt.Sprintf(
					`{"id":%q,"license":"CC0-1.0","name":%q,"sources":[{"type":"user"}]}`, catalogue.slug, catalogue.name),
			})

			// The row spells the translator the OTHER way round from the
			// catalogue, so the credit only lands if the merge is resolved.
			other := "A.B. Kovacs"
			if catalogue.slug == "a-b-kovacs" {
				other = "AB Kovacs"
			}
			rows := fmt.Sprintf(`{"asin":"B0TEST2001","title":"Alpha","region":"us","language":"english","bookFormat":"unabridged","releaseDate":"2024-01-01 00:00:00+00","lengthMinutes":100,"authors":[{"name":"Ada Mapmaker"},{"name":%q}],"narrators":[{"name":"Bea Reader"}],"genres":[],"series":[]}`,
				other+" - translator")

			if _, err := RunLibex(writeBooks(t, rows), Options{DataDir: dataDir, ImportDate: testImportDate}); err != nil {
				t.Fatal(err)
			}

			var work outWork
			readEntity(t, dataDir, workAddr("alpha"), &work)
			if !slices.Contains(work.Authors, catalogue.slug) {
				t.Errorf("authors = %v, want the catalogue record %q", work.Authors, catalogue.slug)
			}
			if len(work.Credits) != 1 || work.Credits[0].Person != catalogue.slug || work.Credits[0].Role != "translator" {
				t.Errorf("credits = %+v, want one translator credit on %q", work.Credits, catalogue.slug)
			}
		})
	}
}

// TestInitialsDecisionIsOrderIndependent is the order-dependence finding at run
// scale: the SAME rows in three different orders must produce the same tree.
//
// The rows are the shape the defect lived in - several initials groups, each
// spelled more than one way, with an uneven majority - and the comparison is the
// whole data tree, not just the person ids. Only sources[].ref is normalized
// away: it stamps the ASIN of the row that happened to create a record, which is
// a pre-existing property of every import and not what this is about.
func TestInitialsDecisionIsOrderIndependent(t *testing.T) {
	names := []string{
		"AB Kovacs", "AB Kovacs", "A.B. Kovacs",
		"J. R. R. Tolkien", "JRR Tolkien", "J. R. R. Tolkien",
		"PJ Roscoe", "P.J. Roscoe",
		"Em Brown", "E. M. Brown",
		"Mr. James", "M. R. James",
		"AB Hyde-White", "A.B. Hyde-White", "A.B. Hyde-White",
	}
	rows := make([]string, 0, len(names))
	for i, name := range names {
		rows = append(rows, fmt.Sprintf(`{"asin":"B0ORDER%04d","title":"Book %02d","region":"us","language":"english","bookFormat":"unabridged","releaseDate":"2024-01-01 00:00:00+00","lengthMinutes":100,"authors":[{"name":"Ada Mapmaker"}],"narrators":[{"name":%q}],"genres":[],"series":[]}`, i, i, name))
	}

	orders := [][]string{
		slices.Clone(rows),
		reversed(rows),
		// A deterministic shuffle: the same rows, interleaved from both ends,
		// which is what a `split -l` boundary does to a group's spellings.
		interleaved(rows),
	}
	var want string
	for i, order := range orders {
		_, dataDir := runLibex(t, strings.Join(order, "\n"), false)
		got := normalizedTree(t, dataDir)
		if i == 0 {
			want = got
			continue
		}
		if got != want {
			t.Errorf("row order %d produced a different tree:\n%s", i, firstDiff(want, got))
		}
	}
}

func reversed(rows []string) []string {
	out := slices.Clone(rows)
	slices.Reverse(out)
	return out
}

// interleaved takes rows from the front and back alternately, which separates
// the spellings of one group as far as an input split can.
func interleaved(rows []string) []string {
	out := make([]string, 0, len(rows))
	for i, j := 0, len(rows)-1; i <= j; i, j = i+1, j-1 {
		out = append(out, rows[i])
		if i != j {
			out = append(out, rows[j])
		}
	}
	return out
}

// normalizedTree renders every pack file in a data tree as sorted text, with
// each sources[].ref blanked - see TestInitialsDecisionIsOrderIndependent.
func normalizedTree(t *testing.T, dataDir string) string {
	t.Helper()
	var lines []string
	err := filepath.WalkDir(dataDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".json") {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var pack struct {
			Entries map[string]json.RawMessage `json:"entries"`
		}
		if err := json.Unmarshal(raw, &pack); err != nil {
			return err
		}
		for slug, entry := range pack.Entries {
			var v any
			if err := json.Unmarshal(entry, &v); err != nil {
				return err
			}
			blankSourceRefs(v)
			out, err := json.Marshal(v)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(dataDir, path)
			lines = append(lines, filepath.Dir(rel)+" "+slug+" "+string(out))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// blankSourceRefs clears every sources[].ref in a decoded record, at any depth
// (a work's recordings carry their own).
func blankSourceRefs(v any) {
	switch t := v.(type) {
	case map[string]any:
		for key, child := range t {
			if key == "sources" {
				if arr, ok := child.([]any); ok {
					for _, src := range arr {
						if m, ok := src.(map[string]any); ok {
							delete(m, "ref")
						}
					}
				}
				continue
			}
			blankSourceRefs(child)
		}
	case []any:
		for _, child := range t {
			blankSourceRefs(child)
		}
	}
}

// firstDiff names the first line two rendered trees disagree on.
func firstDiff(want, got string) string {
	a, b := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return "want: " + a[i] + "\n got: " + b[i]
		}
	}
	return fmt.Sprintf("line counts differ: %d vs %d", len(a), len(b))
}
