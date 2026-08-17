package importer

import (
	"reflect"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/testpack"
)

// TestAudibleGenreTable is the drift guard between the embedded mapping table and
// the schema's genre enum: the table parses, and every value it can produce is a
// member of the controlled vocabulary. A schema rename that misses the table
// would otherwise only surface as a failed post-import validation.
func TestAudibleGenreTable(t *testing.T) {
	table, err := loadGenreTable()
	if err != nil {
		t.Fatalf("embedded audiblegenres.json does not parse: %v", err)
	}

	// Size floors, so a regeneration that truncates the table (or writes an
	// empty half) fails here rather than silently importing books with no
	// genres. They are floors, not exact counts - the table is expected to grow.
	if len(table.ByName) < 1000 {
		t.Errorf("by_name has %d entries, want at least 1000", len(table.ByName))
	}
	if len(table.ByASIN) < 2000 {
		t.Errorf("by_asin has %d entries, want at least 2000", len(table.ByASIN))
	}

	enum := testpack.SchemaDefEnum(t, "genre")
	for key, value := range table.ByName {
		if !enum[value] {
			t.Errorf("by_name[%q] = %q is not in the schema genre enum", key, value)
		}
		if key != strings.ToLower(strings.TrimSpace(key)) {
			t.Errorf("by_name key %q must be lowercased and trimmed (lookup normalizes that way)", key)
		}
	}
	for key, value := range table.ByASIN {
		if !enum[value] {
			t.Errorf("by_asin[%q] = %q is not in the schema genre enum", key, value)
		}
		if key != strings.TrimSpace(key) {
			t.Errorf("by_asin key %q must be trimmed (lookup normalizes that way)", key)
		}
	}

	// The accessor used by the pipeline returns the same table (and does not panic).
	if len(audibleGenreTable().ByName) != len(table.ByName) {
		t.Error("audibleGenreTable() disagrees with loadGenreTable()")
	}
}

// TestAudibleGenreTableAnchors pins a sample of the embedded table end to end
// (through lookup, so normalization and the by-node precedence are exercised).
// The drift guard above only proves every value is SOME vocabulary member; these
// anchors prove the table still says what it is supposed to say - including the
// localized names that let a German or Spanish row map without a node id, and
// the hierarchy-disambiguated nodes whose display NAME is ambiguous ("Military"
// is military-history under History and military-science-fiction under Science
// Fiction, so those two can only resolve by node).
func TestAudibleGenreTableAnchors(t *testing.T) {
	byName := map[string]string{
		// English.
		"science fiction":       "science-fiction",
		"epic fantasy":          "epic-fantasy",
		"space opera":           "space-opera",
		"historical fiction":    "historical-fiction",
		"true crime":            "true-crime",
		"biographies & memoirs": "biography-memoir",
		"business & careers":    "business",
		"fantasy":               "fantasy",
		"romance":               "romance",
		"history":               "history",
		// German.
		"biografien & erinnerungen": "biography-memoir",
		"geschichte":                "history",
		"krimis":                    "mystery",
		"wirtschaft":                "business",
		"wissenschaft":              "science",
		// Spanish.
		"biografías y memorias": "biography-memoir",
		"ciencia ficción":       "science-fiction",
		"fantasía":              "fantasy",
		"historia":              "history",
		"misterio y suspense":   "mystery",
		"romántica":             "romance",
	}
	table := audibleGenreTable()
	for name, want := range byName {
		got, ok := table.lookup(genreClaim{name: name})
		if !ok || got != want {
			t.Errorf("lookup(name %q) = %q,%v; want %q,true", name, got, ok, want)
		}
	}

	byNode := map[string]string{
		"18580641011": "military-science-fiction",
		"18573647011": "military-history",
		"16206689031": "history",
		"16209737031": "fantasy",
		"16209735031": "romance",
		"16215169031": "science-fiction",
		"16209803031": "horror",
		"16206636031": "biography-memoir",
	}
	for node, want := range byNode {
		// A deliberately wrong display name proves the node id wins.
		got, ok := table.lookup(genreClaim{node: node, name: "Totally Made Up Category"})
		if !ok || got != want {
			t.Errorf("lookup(node %q) = %q,%v; want %q,true", node, got, ok, want)
		}
	}
}

// childrensClaims is the children's side of the taxonomy that used to leak adult
// ADVICE genres: every leaf whose display name reads like an adult self-help,
// parenting or careers category, with the browse-node ids libex's /categories
// endpoint gives it in each marketplace the table covers. It is the fixture both
// children's tests read, so a regenerated table is judged against the same list.
var childrensClaims = []struct {
	claim genreClaim
	where string // the node's taxonomy path, for the failure message
	// shared marks a display name an ADULT node also carries ("Careers" is also
	// Business & Careers/Personal Success/Careers), where the name keeps its
	// adult mapping and only the children's node ids are pinned.
	shared bool
}{
	{genreClaim{node: "18572393011", name: "Social & Life Skills"}, "us Children's Audiobooks/Growing Up & Facts of Life/Social & Life Skills", false},
	{genreClaim{node: "21073675011", name: "Social & Life Skills"}, "ca Children's Audiobooks/Growing Up & Facts of Life/Social & Life Skills", false},
	{genreClaim{node: "21882090031", name: "Social & Life Skills"}, "in Children's Audiobooks/Growing Up & Facts of Life/Social & Life Skills", false},
	{genreClaim{node: "18572324011", name: "Difficult Discussions"}, "us Children's Audiobooks/Growing Up & Facts of Life/Difficult Discussions", false},
	{genreClaim{node: "21073678011", name: "Difficult Discussions"}, "ca Children's Audiobooks/Growing Up & Facts of Life/Difficult Discussions", false},
	{genreClaim{node: "18572505011", name: "Family Life"}, "us Children's Audiobooks/Literature & Fiction/Family Life", false},
	{genreClaim{node: "21073812011", name: "Family Life"}, "ca Children's Audiobooks/Literature & Fiction/Family Life", false},
	{genreClaim{node: "21882089031", name: "Family Life"}, "in Children's Audiobooks/Growing Up & Facts of Life/Family Life", false},
	{genreClaim{node: "18572205011", name: "Activities & Hobbies"}, "us Children's Audiobooks/Activities & Hobbies", false},
	{genreClaim{node: "8169599051", name: "Activities & Hobbies"}, "au Children's Audiobooks/Activities & Hobbies", false},
	{genreClaim{node: "16214901031", name: "Schwierige Gespräche"}, "de Kinder-Hörbücher/Auf- & Heranwachsen/Schwierige Gespräche", false},
	{genreClaim{node: "16214969031", name: "Soziale Kompetenz"}, "de Kinder-Hörbücher/Auf- & Heranwachsen/Soziale Kompetenz", false},
	{genreClaim{node: "16214929031", name: "Familienleben"}, "de Kinder-Hörbücher/Auf- & Heranwachsen/Familienleben", false},
	{genreClaim{node: "16215081031", name: "Familienleben"}, "de Kinder-Hörbücher/Literatur & Belletristik/Familienleben", false},
	{genreClaim{node: "19126062031", name: "Habilidades sociales y para la vida"}, "es Audiolibros infantiles/Crecer y cosas de la vida/Habilidades sociales y para la vida", false},
	{genreClaim{node: "19126117031", name: "Vida en familia"}, "es Audiolibros infantiles/Crecer y cosas de la vida/Vida en familia", false},
	{genreClaim{node: "18572251011", name: "Careers"}, "us Children's Audiobooks/Education & Learning/Social Studies/Careers", true},
	{genreClaim{node: "21883278031", name: "Careers"}, "in Children's Audiobooks/Education & Learning/Social Studies/Careers", true},
}

// TestChildrensClaimsAvoidAdultAdviceGenres is the rule stated in
// audiblegenres.go: a claim rooted under Children's Audiobooks may never resolve
// to an adult ADVICE genre. Both halves of a claim are checked, because a leaf
// with no node override falls through to its display NAME - which is how a
// children's tag reached self-help in the first place.
func TestChildrensClaimsAvoidAdultAdviceGenres(t *testing.T) {
	advice := map[string]bool{
		"self-help":               true,
		"parenting-relationships": true,
		"business":                true,
		"finance":                 true,
		"home-garden":             true,
	}
	table := audibleGenreTable()
	for _, c := range childrensClaims {
		if got, ok := table.lookup(c.claim); ok && advice[got] {
			t.Errorf("lookup(node %q, name %q) = %q; a children's category (%s) must not map to an adult advice genre",
				c.claim.node, c.claim.name, got, c.where)
		}
		if c.shared {
			continue // the bare name belongs to the adult node; only the ids are ours
		}
		// The name alone, as a row with no browse-node id resolves it.
		if got, ok := table.lookup(genreClaim{name: c.claim.name}); ok && advice[got] {
			t.Errorf("lookup(name %q) = %q; a children's category name (%s) must not map to an adult advice genre",
				c.claim.name, got, c.where)
		}
	}
}

// TestChildrensClaimAnchors pins what each of those claims resolves to now, so
// the rule above cannot be satisfied by a table that has simply gone empty, and
// so the two deliberate outcomes stay visible: a leaf nothing true can be said
// about maps to NOTHING, and a leaf whose name an adult node legitimately shares
// ("Careers" is also Business & Careers/Personal Success/Careers) is pinned to
// childrens by node id, since an override can redirect a claim but not suppress
// one.
func TestChildrensClaimAnchors(t *testing.T) {
	want := map[string]string{
		"18572393011": "", "21073675011": "", "21882090031": "",
		"18572324011": "", "21073678011": "",
		"18572505011": "", "21073812011": "", "21882089031": "",
		"18572205011": "", "8169599051": "",
		"16214901031": "", "16214969031": "", "16214929031": "", "16215081031": "",
		"19126062031": "", "19126117031": "",
		"18572251011": "childrens", "21883278031": "childrens",
	}
	table := audibleGenreTable()
	for _, c := range childrensClaims {
		got, ok := table.lookup(c.claim)
		if !ok {
			got = ""
		}
		if got != want[c.claim.node] {
			t.Errorf("lookup(node %q, name %q) = %q; want %q (%s)", c.claim.node, c.claim.name, got, want[c.claim.node], c.where)
		}
	}
	// The children's leaves that DO say something true still say it: the parent
	// of the growing-up leaves is a real coming-of-age signal, and the sibling
	// topic leaves keep the topic an adult book would get.
	for _, tc := range []struct {
		claim genreClaim
		want  string
	}{
		{genreClaim{node: "18572323011", name: "Growing Up & Facts of Life"}, "coming-of-age"},
		{genreClaim{node: "18572091011", name: "Children's Audiobooks"}, "childrens"},
		{genreClaim{node: "18572252011", name: "Economics"}, "economics"},
		{genreClaim{node: "18572253011", name: "Government"}, "politics"},
		{genreClaim{node: "18572206011", name: "Cooking & Food"}, "food-cooking"},
		// Not a children's node at all: Teen & Young Adult/Health, Lifestyle &
		// Relationships/Life Skills really is teen advice, so it keeps self-help.
		{genreClaim{node: "18580810011", name: "Life Skills"}, "self-help"},
	} {
		if got, ok := table.lookup(tc.claim); !ok || got != tc.want {
			t.Errorf("lookup(%+v) = %q,%v; want %q,true", tc.claim, got, ok, tc.want)
		}
	}
}

// TestChildrensBookGenresEndToEnd replays the exact claim ladders libex holds for
// the two books the defect was found on. Anne of Green Gables was a self-help
// book and a parenting guide; Pippi was self-help.
func TestChildrensBookGenresEndToEnd(t *testing.T) {
	table := audibleGenreTable()
	cases := []struct {
		book   string
		claims []genreClaim
		want   []string
	}{
		{
			book: "The Anne of Green Gables Collection (B0FMGT6FD6)",
			claims: []genreClaim{
				{node: "18572091011", name: "Children's Audiobooks"},
				{node: "18572224011", name: "Beginner Readers"},
				{node: "18572323011", name: "Growing Up & Facts of Life"},
				{node: "18572393011", name: "Social & Life Skills"},
				{node: "18572491011", name: "Literature & Fiction"},
				{node: "18572496011", name: "Chapter Books & Readers"},
				{node: "18572505011", name: "Family Life"},
			},
			want: []string{"childrens", "coming-of-age"},
		},
		{
			book: "Pippi Goes on Board (B0DS6G3CY7)",
			claims: []genreClaim{
				{node: "18572091011", name: "Children's Audiobooks"},
				{node: "18572323011", name: "Growing Up & Facts of Life"},
				{node: "18572393011", name: "Social & Life Skills"},
				{node: "18572491011", name: "Literature & Fiction"},
				{node: "18572503011", name: "Classics"},
				{node: "18572513011", name: "Humorous Fiction"},
			},
			want: []string{"childrens", "classics", "comedy-humor", "coming-of-age"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.book, func(t *testing.T) {
			got := table.withRunMemo().mapGenres(tc.claims, map[string]bool{})
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("mapGenres = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestGenreTableLookupPrecedence(t *testing.T) {
	table := genreTable{
		ByASIN: map[string]string{"18574784011": "epic-fantasy"},
		ByName: map[string]string{"adventure": "action-adventure", "epic fantasy": "fantasy"},
	}
	cases := []struct {
		name   string
		claim  genreClaim
		want   string
		wantOK bool
	}{
		{"node wins over name", genreClaim{node: "18574784011", name: "Epic Fantasy"}, "epic-fantasy", true},
		{"name when the node is unknown", genreClaim{node: "99999999999", name: "Epic Fantasy"}, "fantasy", true},
		{"name only", genreClaim{name: "  ADVENTURE "}, "action-adventure", true},
		{"unmapped", genreClaim{name: "Totally Made Up Category"}, "", false},
		{"empty", genreClaim{}, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := table.lookup(tc.claim)
			if got != tc.want || ok != tc.wantOK {
				t.Errorf("lookup(%+v) = %q,%v; want %q,%v", tc.claim, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

// TestGenreTableNameMemo pins the memo's contract: a hit and a MISS are both
// remembered, and both keep answering the same way (a memoized miss must never
// read as a mapped genre).
func TestGenreTableNameMemo(t *testing.T) {
	table := genreTable{
		ByName: map[string]string{"epic fantasy": "epic-fantasy"},
		memo:   map[string]string{},
	}
	for range 2 {
		if got, ok := table.lookup(genreClaim{name: " Epic Fantasy "}); got != "epic-fantasy" || !ok {
			t.Errorf("lookup = %q,%v; want epic-fantasy,true", got, ok)
		}
		if got, ok := table.lookup(genreClaim{name: "Nope"}); got != "" || ok {
			t.Errorf("lookup = %q,%v; want an unmapped miss", got, ok)
		}
	}
	if len(table.memo) != 2 {
		t.Errorf("memo = %v, want one entry per distinct raw name", table.memo)
	}
}

func TestGenreTableMapGenres(t *testing.T) {
	table := genreTable{
		ByName: map[string]string{"epic fantasy": "epic-fantasy", "adventure": "action-adventure", "sagas": "epic-fantasy"},
	}
	unmapped := map[string]bool{}
	got := table.mapGenres([]genreClaim{
		{name: "Epic Fantasy"},
		{name: "Adventure"},
		{name: "Sagas"},       // maps to a slug already collected: deduplicated
		{name: "Nonexistent"}, // unmapped: dropped and reported
		{node: "12345678901"}, // no name and no mapping: reported under its node id
	}, unmapped)

	if !reflect.DeepEqual(got, []string{"action-adventure", "epic-fantasy"}) {
		t.Errorf("mapGenres = %v, want sorted deduplicated [action-adventure epic-fantasy]", got)
	}
	if !unmapped["Nonexistent"] || !unmapped["12345678901"] || len(unmapped) != 2 {
		t.Errorf("unmapped = %v", unmapped)
	}
	// No claims at all leaves the field absent (genre_list has minItems 1).
	if got := table.mapGenres(nil, unmapped); len(got) != 0 {
		t.Errorf("mapGenres(nil) = %v, want empty", got)
	}
}
