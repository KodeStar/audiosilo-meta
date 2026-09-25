package importer

import (
	"encoding/json"
	"os"
	"reflect"
	"sort"
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
	pathEntries := 0
	for region, paths := range table.ByPath {
		if !marketplaces[region] {
			t.Errorf("by_path region %q is not a marketplace", region)
		}
		for key, value := range paths {
			pathEntries++
			// "" is a suppression: the marketplace's own node maps to nothing, so
			// the lookup must stop there rather than fall back to the US answer or
			// the leaf name.
			if value != "" && !enum[value] {
				t.Errorf("by_path[%q][%q] = %q is not in the schema genre enum", region, key, value)
			}
			if key != genrePathKey(key) {
				t.Errorf("by_path[%q] key %q must be in genrePathKey form (lookup normalizes that way)", region, key)
			}
		}
	}
	if pathEntries < 400 || len(table.ByPath["us"]) < 200 {
		t.Errorf("by_path has %d entries (%d us), want at least 400 (200 us)", pathEntries, len(table.ByPath["us"]))
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
		// Romance > Contemporary in every marketplace the table covers (issue
		// #2337: the US node was unmapped, so 71k books lost the label). Its
		// display name "Contemporary" is ambiguous - it is also a Fantasy and a
		// Teen Fantasy node - so only the node can say it.
		"18580522011": "contemporary-romance", // us Romance > Contemporary
		"18581007011": "contemporary-romance", // us Teen & Young Adult > Romance > Contemporary
		"19378423031": "contemporary-romance", // uk
		"21073595011": "contemporary-romance", // ca
		"8171261051":  "contemporary-romance", // au
		"21882010031": "contemporary-romance", // in
		"16245163031": "contemporary-romance", // de Liebesromane > Zeitgenössische Liebesromane
		"18059979031": "contemporary-romance", // es Romántica > Contemporánea
		"21838164031": "contemporary-romance", // it Romanzo d'amore > Contemporaneo
		"8191869051":  "contemporary-romance", // jp
		"41939661011": "contemporary-romance", // br Romance > Contemporâneo
		// Nodes a subtree's genre is true of, pinned because their display names
		// ("Literature & Fiction", "Americas", "Europe") are ambiguous on their own.
		"18573352011": "erotica",            // Erotica > Literature & Fiction
		"18573754011": "lgbtq",              // LGBTQ+ > Literature & Fiction
		"18580894011": "young-adult",        // Teen & Young Adult > Literature & Fiction
		"18580625011": "urban-fantasy",      // ... Fantasy > Paranormal & Urban > Urban
		"18573526011": "history",            // History > Americas
		"18581104011": "travel",             // Travel & Tourism > Europe
		"18580525011": "historical-romance", // Romance > Historical > 20th Century
	}
	for node, want := range byNode {
		// A deliberately wrong display name proves the node id wins.
		got, ok := table.lookup(genreClaim{node: node, name: "Totally Made Up Category"})
		if !ok || got != want {
			t.Errorf("lookup(node %q) = %q,%v; want %q,true", node, got, ok, want)
		}
	}

	// A PATH resolves the way its node does IN THAT MARKETPLACE, whatever the
	// leaf name says alone (the OpenAudible case: a ladder of names, no node
	// ids). The claims are built the way the parser builds them, so each row
	// also proves the path outranks the leaf name.
	for _, tc := range []struct {
		region, ladder, want string
	}{
		{"us", "Romance:Contemporary", "contemporary-romance"},
		{"au", "Romance:Contemporary", "contemporary-romance"},
		{"us", "Teen & Young Adult:Romance:Contemporary", "contemporary-romance"},
		{"us", "Romance:Military", "romance"},
		{"us", "Science Fiction & Fantasy:Science Fiction:Military", "military-science-fiction"},
		{"us", "History:Military", "military-history"},
		{"us", "Literature & Fiction:Historical Fiction:20th Century", "historical-fiction"},
		{"us", "Children's Audiobooks:Education & Learning:Social Studies:Careers", "childrens"},
		// Marketplace-aware: the US node for Romance > Historical is pinned to
		// historical-romance, while the uk/ca/au nodes libex states for the same
		// books answer historical-fiction - a path-stating row lands where a
		// node-stating one does for ITS marketplace. (That the marketplaces
		// disagree at all is a table inconsistency tracked separately.)
		{"us", "Romance:Historical", "historical-romance"},
		{"uk", "Romance:Historical", "historical-fiction"},
		{"au", "Romance:Historical", "historical-fiction"},
		// A marketplace we know nothing about falls back to the US table.
		{"", "Romance:Historical", "historical-romance"},
	} {
		claims := pathGenreClaims(tc.ladder, tc.region)
		got, ok := table.lookup(claims[len(claims)-1])
		if !ok || got != tc.want {
			t.Errorf("lookup(%s path %q) = %q,%v; want %q,true", tc.region, tc.ladder, got, ok, tc.want)
		}
	}
	// A path that needs no disambiguation has no entry and falls through to its
	// leaf name.
	claims := pathGenreClaims("Science Fiction & Fantasy:Fantasy:Epic", "us")
	if got, ok := table.lookup(claims[2]); !ok || got != "epic-fantasy" {
		t.Errorf("lookup(path Science Fiction & Fantasy:Fantasy:Epic) = %q,%v; want epic-fantasy,true", got, ok)
	}
}

// genrePathsFile is scripts/genrepaths' verification output: for each
// marketplace, the category paths the generator checked (every path whose node
// the table pins, every path some marketplace's by_path carries, every root and
// every path under a children's root) and the browse-node id each one names.
const genrePathsFile = "testdata/genrepaths.json"

func loadGenrePaths(t *testing.T) map[string]map[string]string {
	t.Helper()
	raw, err := os.ReadFile(genrePathsFile)
	if err != nil {
		t.Fatalf("%s: %v (regenerate with scripts/genrepaths)", genrePathsFile, err)
	}
	var v map[string]map[string]string
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("%s: %v", genrePathsFile, err)
	}
	return v
}

// nodeAnswer is what a node-stating source gets for a node: the by_asin pin,
// else the node's own (leaf) name through by_name, else nothing.
func nodeAnswer(table genreTable, node, leaf string) string {
	if g, ok := table.ByASIN[node]; ok {
		return g
	}
	return table.ByName[leaf]
}

// TestGenrePathsMatchTheirNodes is by_path's drift guard, and the whole contract
// the generator implements: for every checked path of every marketplace, the
// path lookup (the marketplace's table, then the US table, then the leaf name)
// answers exactly what the path's NODE answers. A hand edit to by_path, or a
// by_asin pin changed without regenerating, fails here naming the path.
func TestGenrePathsMatchTheirNodes(t *testing.T) {
	table := audibleGenreTable()
	checked := 0
	for region, paths := range loadGenrePaths(t) {
		for key, node := range paths {
			checked++
			leaf := key[strings.LastIndex(key, ":")+1:]
			want := nodeAnswer(table, node, leaf)
			got, _ := table.lookup(genreClaim{path: key, region: region, name: leaf})
			if got != want {
				t.Errorf("%s path %q (node %s): path lookup = %q, node answers %q - regenerate by_path with scripts/genrepaths",
					region, key, node, got, want)
			}
		}
	}
	if checked < 3000 {
		t.Errorf("verification file checks %d paths, want at least 3000 (truncated?)", checked)
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
// adultAdviceGenres is the vocabulary a children's category must never produce.
var adultAdviceGenres = map[string]bool{
	"self-help":               true,
	"parenting-relationships": true,
	"business":                true,
	"finance":                 true,
	"home-garden":             true,
}

func TestChildrensClaimsAvoidAdultAdviceGenres(t *testing.T) {
	advice := adultAdviceGenres
	table := audibleGenreTable()
	for _, c := range childrensClaims {
		if got, ok := table.lookup(c.claim); ok && advice[got] {
			t.Errorf("lookup(node %q, name %q) = %q; a children's category (%s) must not map to an adult advice genre",
				c.claim.node, c.claim.name, got, c.where)
		}
		// The ladder, as OpenAudible states it ("<region> A/B/C" -> "A:B:C"),
		// at EVERY level - shared entries included, since the path (unlike the
		// bare name) is the children's node's own.
		region, where, _ := strings.Cut(c.where, " ")
		for _, level := range pathGenreClaims(strings.ReplaceAll(where, "/", ":"), region) {
			if got, ok := table.lookup(level); ok && advice[got] {
				t.Errorf("lookup(%s path %q) = %q; a children's category path must not map to an adult advice genre",
					region, level.path, got)
			}
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

// TestChildrensPathsAvoidAdultAdviceGenres extends the children's rule to EVERY
// children's path of every marketplace the generator saw - it, jp and br
// included - rather than to a hand-kept list of localized roots: a children's
// root is a root the table itself answers "childrens" for, and every path under
// one, at every level, must stay clear of adult advice vocabulary both through
// the path lookup and through its node.
func TestChildrensPathsAvoidAdultAdviceGenres(t *testing.T) {
	table := audibleGenreTable()
	all := loadGenrePaths(t)
	regions := make([]string, 0, len(all))
	for r := range all {
		regions = append(regions, r)
	}
	sort.Strings(regions)
	rootsSeen := map[string]bool{}
	for _, region := range regions {
		paths := all[region]
		roots := map[string]bool{}
		for key, node := range paths {
			if !strings.Contains(key, ":") && nodeAnswer(table, node, key) == "childrens" {
				roots[key] = true
			}
		}
		if len(roots) == 0 {
			t.Errorf("%s: no children's root found in %s", region, genrePathsFile)
		}
		for key, node := range paths {
			root, _, _ := strings.Cut(key, ":")
			if !roots[root] {
				continue
			}
			rootsSeen[region] = true
			leaf := key[strings.LastIndex(key, ":")+1:]
			if g := nodeAnswer(table, node, leaf); adultAdviceGenres[g] {
				t.Errorf("%s node %s (%q) = %q; a children's category must not map to an adult advice genre", region, node, key, g)
			}
			for _, level := range pathGenreClaims(key, region) {
				if got, ok := table.lookup(level); ok && adultAdviceGenres[got] {
					t.Errorf("lookup(%s path %q) = %q; a children's category must not map to an adult advice genre",
						region, level.path, got)
				}
			}
		}
	}
	for _, r := range []string{"us", "uk", "de", "es", "it", "jp", "br"} {
		if !rootsSeen[r] {
			t.Errorf("no children's paths checked for %s", r)
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
