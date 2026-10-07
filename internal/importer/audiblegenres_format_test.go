package importer

import (
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/testpack"
)

// formatPaths is the FORMAT rule's node list stated by PATH, per marketplace:
// every Arts & Entertainment node that names how a book was produced rather than
// what it is about - the audio-performance/dramatization node and its children,
// and the Film & TV and Radio leaves of the performing-arts node - in each
// marketplace's own spelling. It is the fixture TestFormatNodesArePinned holds the
// table's hand-curated "format" list to, through the verification file's own
// path -> node map, so a node id typed wrong, a marketplace forgotten, or a node
// Audible re-ids fails naming the path. Derived from libex's /categories
// taxonomies when the rule landed (2026-10).
var formatPaths = map[string][]string{
	"us": {
		"arts & entertainment:audio performances & dramatizations",
		"arts & entertainment:audio performances & dramatizations:dramatizations",
		"arts & entertainment:audio performances & dramatizations:storytelling",
		"arts & entertainment:entertainment & performing arts:film & tv",
		"arts & entertainment:entertainment & performing arts:radio",
	},
	"uk": {
		"arts & entertainment:audio performances & dramatisations",
		"arts & entertainment:audio performances & dramatisations:dramatisations",
		"arts & entertainment:entertainment & performing arts:film & tv",
	},
	"ca": {
		"arts & entertainment:audio performances & dramatizations",
		"arts & entertainment:audio performances & dramatizations:dramatizations",
		"arts & entertainment:audio performances & dramatizations:storytelling",
		"arts & entertainment:entertainment & performing arts:film & tv",
		"arts & entertainment:entertainment & performing arts:radio",
	},
	"au": {
		"arts & entertainment:audio performances & dramatisations",
		"arts & entertainment:audio performances & dramatisations:dramatisations",
		"arts & entertainment:entertainment & performing arts:film & tv",
	},
	"in": {
		"arts & entertainment:audio performances & dramatisations",
		"arts & entertainment:audio performances & dramatisations:dramatisations",
		"arts & entertainment:audio performances & dramatisations:storytelling",
		"arts & entertainment:entertainment & performing arts:film & tv",
	},
	"de": {
		"kunst & unterhaltung:hörspiele & dramatisierungen",
	},
	"fr": {
		"arts et divertissement:adaptations et performances audio",
		"arts et divertissement:divertissement et arts du spectacle:films et télévision",
	},
	"es": {
		"arte y entretenimiento:audiciones y dramatizaciones",
		"arte y entretenimiento:audiciones y dramatizaciones:dramatizaciones",
		"arte y entretenimiento:audiciones y dramatizaciones:narrativa",
		"arte y entretenimiento:entretenimiento y artes escénicas:cine y tv",
	},
	"it": {
		"arte e intrattenimento:performance audio e sceneggiature",
		"arte e intrattenimento:performance audio e sceneggiature:narrazione",
		"arte e intrattenimento:performance audio e sceneggiature:sceneggiature",
		"arte e intrattenimento:intrattenimento e arti dello spettacolo:film e tv",
		"arte e intrattenimento:intrattenimento e arti dello spettacolo:radio",
	},
	"jp": {
		"エンターテインメント・アート:ドラマ化・音声舞台",
		"エンターテインメント・アート:ドラマ化・音声舞台:ドラマ化",
		"エンターテインメント・アート:ドラマ化・音声舞台:物語",
		"エンターテインメント・アート:エンターテインメント・舞台芸術:映画・テレビ",
		"エンターテインメント・アート:エンターテインメント・舞台芸術:ラジオ",
	},
	"br": {
		"artes e entretenimento:dramatizações e apresentações de áudio",
	},
}

// fictionOnlyPaths is the subset of formatPaths that is a format ONLY in a row
// mapping a fiction genre: the Radio and Film & TV leaves of each marketplace's
// performing-arts node (see audiblegenres.go). TestFictionOnlyFormatNodesArePinned
// holds format_fiction_only to it.
var fictionOnlyPaths = map[string][]string{
	"us": {"arts & entertainment:entertainment & performing arts:film & tv", "arts & entertainment:entertainment & performing arts:radio"},
	"uk": {"arts & entertainment:entertainment & performing arts:film & tv"},
	"ca": {"arts & entertainment:entertainment & performing arts:film & tv", "arts & entertainment:entertainment & performing arts:radio"},
	"au": {"arts & entertainment:entertainment & performing arts:film & tv"},
	"in": {"arts & entertainment:entertainment & performing arts:film & tv"},
	"fr": {"arts et divertissement:divertissement et arts du spectacle:films et télévision"},
	"es": {"arte y entretenimiento:entretenimiento y artes escénicas:cine y tv"},
	"it": {
		"arte e intrattenimento:intrattenimento e arti dello spettacolo:film e tv",
		"arte e intrattenimento:intrattenimento e arti dello spettacolo:radio",
	},
	"jp": {"エンターテインメント・アート:エンターテインメント・舞台芸術:映画・テレビ", "エンターテインメント・アート:エンターテインメント・舞台芸術:ラジオ"},
}

func TestFictionOnlyFormatNodesArePinned(t *testing.T) {
	table := audibleGenreTable()
	paths := loadGenrePaths(t)
	want := map[string]bool{}
	for region, keys := range fictionOnlyPaths {
		for _, key := range keys {
			if !slices.Contains(formatPaths[region], key) {
				t.Errorf("%s %q is fiction-only but not a pinned format path", region, key)
			}
			want[paths[region][key]] = true
			if node := table.FormatTree[paths[region][key]]; !node.Format || !node.FictionOnly {
				t.Errorf("format_tree %s (%s %q) = %+v, want a fiction-only format node", paths[region][key], region, key, node)
			}
		}
	}
	got := map[string]bool{}
	for _, n := range table.FormatFictionOnly {
		got[n] = true
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("format_fiction_only = %v\nwant (from the pinned paths) %v", slices.Sorted(maps.Keys(got)), slices.Sorted(maps.Keys(want)))
	}
	if !slices.IsSorted(table.FormatFictionOnly) {
		t.Errorf("format_fiction_only is not sorted")
	}
}

// TestGenreKindsCoverTheVocabulary is genre_kinds' drift guard: every value of
// the schema's genre vocabulary is classified, as fiction, nonfiction or
// neither, and nothing else is - so a genre added to the vocabulary must be
// classified before the format rule can read it.
func TestGenreKindsCoverTheVocabulary(t *testing.T) {
	table := audibleGenreTable()
	enum := testpack.SchemaDefEnum(t, "genre")
	for g := range enum {
		switch table.GenreKinds[g] {
		case genreFiction, "nonfiction", "neither":
		default:
			t.Errorf("genre %q is classified %q; want fiction, nonfiction or neither", g, table.GenreKinds[g])
		}
	}
	for g := range table.GenreKinds {
		if !enum[g] {
			t.Errorf("genre_kinds classifies %q, which is not in the vocabulary", g)
		}
	}
	// The judgement calls the format rule depends on (audiblegenres.go).
	for g, want := range map[string]string{"comedy-humor": "neither", "drama-plays": "neither", "short-stories": genreFiction, "history": "nonfiction", "mystery": genreFiction} {
		if got := table.GenreKinds[g]; got != want {
			t.Errorf("genre_kinds[%s] = %q, want %q", g, got, want)
		}
	}
}

// TestFormatNodesArePinned is the format list's drift guard: the table's
// hand-curated "format" ids are EXACTLY the nodes the verification file gives the
// pinned paths, every marketplace the table covers is stated (none silently
// missing), and each format node sits in the derived tree at that path.
func TestFormatNodesArePinned(t *testing.T) {
	table := audibleGenreTable()
	paths := loadGenrePaths(t)
	want := map[string]bool{}
	for _, region := range Marketplaces() {
		keys, ok := formatPaths[region]
		if !ok {
			t.Errorf("formatPaths states nothing for marketplace %s: find its format nodes by path and pin them", region)
			continue
		}
		for _, key := range keys {
			node, ok := paths[region][key]
			if !ok {
				t.Errorf("%s format path %q is not in %s (regenerate with scripts/genrepaths)", region, key, genrePathsFile)
				continue
			}
			if got := table.FormatPaths[region][key]; got != node {
				t.Errorf("format_paths %s %q = %q, want %q", region, key, got, node)
			}
			if !table.FormatTree[node].Format {
				t.Errorf("format_tree does not mark %s (%s %q) as a format node", node, region, key)
			}
			want[node] = true
		}
	}
	got := map[string]bool{}
	for _, n := range table.Format {
		if got[n] {
			t.Errorf("format lists %s twice", n)
		}
		got[n] = true
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("format = %v\nwant (from the pinned paths) %v", slices.Sorted(maps.Keys(got)), slices.Sorted(maps.Keys(want)))
	}
	if !slices.IsSorted(table.Format) {
		t.Errorf("format is not sorted")
	}
}

// TestFormatTreeMatchesGenrePaths pins the DERIVED format_tree and format_paths
// against the verification file: DeriveFormatTree (the generator's own
// derivation) over its paths must give exactly what the table carries. A hand
// edit, or a format list changed without regenerating, fails here.
func TestFormatTreeMatchesGenrePaths(t *testing.T) {
	table := audibleGenreTable()
	tree, paths, err := DeriveFormatTree(table.Format, table.FormatFictionOnly, loadGenrePaths(t))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(tree, table.FormatTree) {
		t.Errorf("format_tree differs from its derivation over %s - regenerate with scripts/genrepaths", genrePathsFile)
	}
	if !reflect.DeepEqual(paths, table.FormatPaths) {
		t.Errorf("format_paths differs from its derivation over %s - regenerate with scripts/genrepaths", genrePathsFile)
	}
}

// nodeClaims builds node-stating claims (the libex shape) from "id|name" pairs.
func nodeClaims(pairs ...string) []genreClaim {
	out := make([]genreClaim, 0, len(pairs))
	for _, p := range pairs {
		id, name, _ := strings.Cut(p, "|")
		out = append(out, genreClaim{node: id, name: name})
	}
	return out
}

func mapRow(claims []genreClaim) []string {
	return audibleGenreTable().mapGenres(claims, map[string]bool{})
}

// TestFormatRuleFiveLittlePigs replays the exact claims libex holds for the two
// BBC Five Little Pigs productions the rule was written for. The full-cast
// dramatization states only format ladders under Arts & Entertainment beside its
// mystery ladders, so it is a mystery and not an arts book; the John Moffatt
// production also states Arts & Entertainment > Art, a SUBJECT node, so it keeps
// arts-entertainment through it.
func TestFormatRuleFiveLittlePigs(t *testing.T) {
	fullCast := mapRow(nodeClaims( // B076TNM4YZ
		"18571910011|Arts & Entertainment", "18571919011|Audio Performances & Dramatizations",
		"18571920011|Dramatizations", "18574597011|Mystery, Thriller & Suspense",
		"18574606011|Mystery", "18574621011|Thriller & Suspense", "18574623011|Crime Thrillers"))
	if slices.Contains(fullCast, "arts-entertainment") || !slices.Contains(fullCast, "mystery") {
		t.Errorf("B076TNM4YZ = %v; want mystery and no arts-entertainment", fullCast)
	}
	moffatt := mapRow(nodeClaims( // B0042G3KPA
		"18571910011|Arts & Entertainment", "18571913011|Art", "18571923011|Entertainment & Performing Arts",
		"18571933011|Film & TV", "18571937011|Radio", "18574426011|Literature & Fiction",
		"18574456011|Genre Fiction", "18574481011|Westerns", "18574597011|Mystery, Thriller & Suspense",
		"18574598011|Crime Fiction", "18574606011|Mystery", "18574619011|Traditional Detectives",
		"18574621011|Thriller & Suspense"))
	if !slices.Contains(moffatt, "arts-entertainment") || !slices.Contains(moffatt, "mystery") {
		t.Errorf("B0042G3KPA = %v; want arts-entertainment (through Art) beside mystery", moffatt)
	}
}

func TestFormatRule(t *testing.T) {
	cases := []struct {
		name    string
		claims  []genreClaim
		want    []string // exactly, when non-nil
		keep    []string
		without []string
	}{
		{
			name:   "a radio show whose row maps nothing else keeps its format genre",
			claims: nodeClaims("18571910011|Arts & Entertainment", "18571923011|Entertainment & Performing Arts", "18571937011|Radio"),
			want:   []string{"arts-entertainment"},
		},
		{
			name: "a format ladder beside a subject drops the format genre and its ancestors'",
			claims: nodeClaims("18571910011|Arts & Entertainment", "18571923011|Entertainment & Performing Arts",
				"18571937011|Radio", "18574597011|Mystery, Thriller & Suspense", "18574606011|Mystery"),
			keep: []string{"mystery"}, without: []string{"arts-entertainment"},
		},
		{
			name: "Storytelling is a format node: a storytelling performance of a mystery is not about writing",
			claims: nodeClaims("18571910011|Arts & Entertainment", "18571919011|Audio Performances & Dramatizations",
				"18571922011|Storytelling", "18574606011|Mystery"),
			keep: []string{"mystery"}, without: []string{"writing-publishing", "arts-entertainment"},
		},
		{
			name: "an ancestor reached through a subject descendant (Music) keeps its genre",
			claims: nodeClaims("18571910011|Arts & Entertainment", "18571942011|Music",
				"18571923011|Entertainment & Performing Arts", "18571937011|Radio", "18574606011|Mystery"),
			keep: []string{"arts-entertainment", "music", "mystery"},
		},
		{
			name: "a format node with a subject child stated is justified by it (Film & TV > Screenwriting)",
			claims: nodeClaims("18571910011|Arts & Entertainment", "18571923011|Entertainment & Performing Arts",
				"18571933011|Film & TV", "18571936011|Screenwriting"),
			keep: []string{"arts-entertainment"},
		},
		{
			name:   "a row with no format node is untouched",
			claims: nodeClaims("18571910011|Arts & Entertainment", "18571913011|Art", "18574606011|Mystery"),
			want:   []string{"arts-entertainment", "mystery"},
		},
		{
			name: "two LADDERS (path claims) follow the same rule",
			claims: append(pathGenreClaims("Arts & Entertainment:Entertainment & Performing Arts:Radio", "us"),
				pathGenreClaims("Mystery, Thriller & Suspense:Mystery", "us")...),
			keep: []string{"mystery"}, without: []string{"arts-entertainment"},
		},
		{
			name:   "one format ladder alone keeps its genre (OpenAudible's one ladder per book)",
			claims: pathGenreClaims("Arts & Entertainment:Entertainment & Performing Arts:Radio", "uk"),
			want:   []string{"arts-entertainment"},
		},
		{
			name: "a BBC fiction radio row drops arts-entertainment (Radio and Film & TV are formats for fiction)",
			claims: nodeClaims("18571910011|Arts & Entertainment", "18571923011|Entertainment & Performing Arts",
				"18571937011|Radio", "18571933011|Film & TV", "18574606011|Mystery"),
			want: []string{"mystery"},
		},
		{
			name: "a nonfiction row about film keeps arts-entertainment (Film & TV is its subject)",
			claims: nodeClaims("18571910011|Arts & Entertainment", "18571923011|Entertainment & Performing Arts",
				"18571933011|Film & TV", "18573518011|History"),
			want: []string{"arts-entertainment", "history"},
		},
		{
			name: "a radio comedy panel show keeps arts-entertainment (comedy-humor is not fiction)",
			claims: nodeClaims("18571910011|Arts & Entertainment", "18571923011|Entertainment & Performing Arts",
				"18571937011|Radio", "24427740011|Comedy & Humor"),
			want: []string{"arts-entertainment", "comedy-humor"},
		},
		{
			name: "the dramatizations subtree is a format beside nonfiction too (a dramatized Gospel)",
			claims: nodeClaims("18571910011|Arts & Entertainment", "18571919011|Audio Performances & Dramatizations",
				"18571920011|Dramatizations", "18574839011|Religion & Spirituality"),
			want: []string{"religion-spirituality"},
		},
		{
			name: "a German Hörspiel of a thriller is a thriller",
			claims: nodeClaims("16206595031|Kunst & Unterhaltung", "16206604031|Hörspiele & Dramatisierungen",
				"18574621011|Thriller & Suspense"),
			without: []string{"arts-entertainment"}, keep: []string{"thriller-suspense"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mapRow(tc.claims)
			if tc.want != nil && !reflect.DeepEqual(got, tc.want) {
				t.Errorf("mapGenres = %v, want %v", got, tc.want)
			}
			for _, g := range tc.keep {
				if !slices.Contains(got, g) {
					t.Errorf("mapGenres = %v, want it to keep %s", got, g)
				}
			}
			for _, g := range tc.without {
				if slices.Contains(got, g) {
					t.Errorf("mapGenres = %v, want no %s", got, g)
				}
			}
		})
	}
}

// TestFormatRuleEveryMarketplace runs every pinned format node's whole ladder,
// node ids from the verification file, in two rows: beside a subject claim (the
// ladder contributes nothing) and alone (the ladder keeps what it maps without
// the rule). That is the rule holding for every marketplace's equivalents, not
// only the US nodes it was found on.
func TestFormatRuleEveryMarketplace(t *testing.T) {
	table := audibleGenreTable()
	plain := table
	plain.FormatTree = nil
	paths := loadGenrePaths(t)
	subject := genreClaim{node: "18574606011", name: "Mystery"}
	history := genreClaim{node: "18573518011", name: "History"}
	fictionOnly := map[string]bool{}
	for region, keys := range fictionOnlyPaths {
		for _, key := range keys {
			fictionOnly[region+" "+key] = true
		}
	}
	for region, keys := range formatPaths {
		for _, key := range keys {
			segs := strings.Split(key, ":")
			var ladder []genreClaim
			for i := range segs {
				prefix := strings.Join(segs[:i+1], ":")
				ladder = append(ladder, genreClaim{node: paths[region][prefix], name: segs[i]})
			}
			alone := table.mapGenres(ladder, map[string]bool{})
			if want := plain.mapGenres(ladder, map[string]bool{}); !reflect.DeepEqual(alone, want) {
				t.Errorf("%s %q alone = %v, want %v (a format-only row keeps its genre)", region, key, alone, want)
			}
			beside := table.mapGenres(append(ladder, subject), map[string]bool{})
			if !reflect.DeepEqual(beside, []string{"mystery"}) {
				t.Errorf("%s %q beside Mystery = %v, want [mystery]", region, key, beside)
			}
			// Beside a NONFICTION subject a fiction-only node (Radio, Film & TV)
			// is a subject itself and keeps what it maps; the dramatizations
			// subtree is a format whatever the row maps.
			nonfiction := table.mapGenres(append(slices.Clone(ladder), history), map[string]bool{})
			want := []string{"history"}
			if fictionOnly[region+" "+key] {
				want = UnionGenres(want, alone)
			}
			if !reflect.DeepEqual(nonfiction, want) {
				t.Errorf("%s %q beside History = %v, want %v", region, key, nonfiction, want)
			}
		}
	}
}

func TestDeriveFormatTreeRefusesAnIncompleteTree(t *testing.T) {
	if _, _, err := DeriveFormatTree([]string{"1"}, []string{"9"}, map[string]map[string]string{"us": {"a": "1"}}); err == nil {
		t.Errorf("a fiction-only node outside the format list was accepted")
	}
	if _, _, err := DeriveFormatTree([]string{"9"}, nil, map[string]map[string]string{"us": {"a": "1"}}); err == nil {
		t.Errorf("a format node no path names was accepted")
	}
	if _, _, err := DeriveFormatTree([]string{"2"}, nil, map[string]map[string]string{"us": {"a:b": "2"}}); err == nil {
		t.Errorf("a path with no parent path was accepted")
	}
}
