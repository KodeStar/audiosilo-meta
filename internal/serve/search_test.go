package serve

import (
	"net/url"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// The MATCH builder's tests. ftsMatch is the ONE place a query becomes an FTS5
// expression - every search endpoint, the ABS facade, the coverage browser and
// both query-side boosts go through it - so the expression it builds is pinned
// here for every shape a user (or a filename-derived client query) can write,
// and the two boosts' composition of it is pinned beside it.

// TestFTSQueryBuilder pins the MATCH expression for every query shape: one
// one-word phrase per term, punctuation as a boundary, the prefix-star on the
// last term, and the empty-phrase sentinel for a query holding no term at all.
func TestFTSQueryBuilder(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"plain two words", "hail mary", `"hail" "mary"*`},
		{"single word", "dragon", `"dragon"*`},

		// The bug: punctuation used INSTEAD of a space. Each of these was one
		// welded phrase before, and an adjacency constraint no row could meet.
		{"colon subtitle", "Halo: Primordium", `"Halo" "Primordium"*`},
		{"colon, no space", "Halo:Primordium", `"Halo" "Primordium"*`},
		{"filename dots and dashes", "Greg.Bear-Halo.Primordium", `"Greg" "Bear" "Halo" "Primordium"*`},
		{"comma, no space", "Primordium,Halo", `"Primordium" "Halo"*`},
		{"underscores", "the_way_of_kings", `"the" "way" "of" "kings"*`},
		{"slash", "Crime/Punishment", `"Crime" "Punishment"*`},

		// Punctuation that stands alone, or trails, is simply gone.
		{"ampersand", "Harry Potter & the Goblet", `"Harry" "Potter" "the" "Goblet"*`},
		{"parenthesised", "The Bell (Whitechapel)", `"The" "Bell" "Whitechapel"*`},
		{"trailing bang", "Watchers: Culloden!", `"Watchers" "Culloden"*`},
		{"trailing question mark", "Do Androids Dream?", `"Do" "Androids" "Dream"*`},
		{"lone dash", "Age of Trinity - Die Stunde", `"Age" "of" "Trinity" "Die" "Stunde"*`},

		// Apostrophes and hyphens split too: unicode61 tokenized them apart on
		// the way INTO the index, so the terms are what the row holds.
		{"apostrophe", "Don't Look Back", `"Don" "t" "Look" "Back"*`},
		{"typographic apostrophe", "The Ring’s Secret", `"The" "Ring" "s" "Secret"*`},
		{"hyphenated name", "Spider-Man", `"Spider" "Man"*`},

		// Numeric titles are one term and keep exactly the expression they had.
		{"numeric title", "1984", `"1984"*`},
		{"word plus number", "Fahrenheit 451", `"Fahrenheit" "451"*`},
		{"decimal", "Reacher 2.5", `"Reacher" "2" "5"*`},

		// Non-ASCII letters are term runes, so a German or Spanish title is one
		// term per word - composed and decomposed alike (a combining mark stays
		// inside its term, because unicode61 keeps it inside the token).
		{"german umlaut", "Mädchen", `"Mädchen"*`},
		{"german umlaut decomposed", "Ma\u0308dchen", "\"Ma\u0308dchen\"*"},
		{"spanish accent", "Corazón Salvaje", `"Corazón" "Salvaje"*`},
		{"german sharp s", "Die Straße", `"Die" "Straße"*`},

		// No term at all: the harmless empty-phrase match, never a syntax error.
		{"whitespace only", "   ", `""`},
		{"punctuation only", "!!", `""`},
		{"quotes only", `"""`, `""`},
		{"empty", "", `""`},

		// A quote is a boundary like any other punctuation, so it can never
		// reach the inside of a phrase.
		{"embedded quote", `a"b`, `"a" "b"*`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ftsQuery(tc.in); got != tc.want {
				t.Errorf("ftsQuery(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestFTSPhraseBuilder pins the no-prefix-star twin: the same terms, matched
// whole. It is what the two boosts probe with, so a prefix-star leaking into it
// would widen a keystroke-hot lookup the file headers argue must stay bounded.
func TestFTSPhraseBuilder(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Halo: Primordium", `"Halo" "Primordium"`},
		{"jack reacher", `"jack" "reacher"`},
		{"Don't Look Back", `"Don" "t" "Look" "Back"`},
		{"1984", `"1984"`},
		{"!!", `""`},
		{"", `""`},
	}
	for _, tc := range cases {
		if got := ftsPhrase(tc.in); got != tc.want {
			t.Errorf("ftsPhrase(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestBoostMatchComposition pins how the two boosts wrap ftsPhrase, because they
// are the callers a change to the shared builder could break silently: the
// exact-title probe's column filter must stay over a PARENTHESIZED phrase list
// (an unparenthesized filter binds to the first phrase only, which is the bug
// its file header records), and the series probe must still drop stopwords and
// keep every surviving term as its own phrase.
func TestBoostMatchComposition(t *testing.T) {
	titles := []struct{ in, want string }{
		{"Halo: Primordium", `title : ("Halo" "Primordium")`},
		{"spare", `title : ("spare")`},
		{"Don't Look Back", `title : ("Don" "t" "Look" "Back")`},
		// No term at all still composes a filter FTS5 parses (it matches
		// nothing); exactTitleHits gates this case out before it is reached.
		{"!!", `title : ("")`},
	}
	for _, tc := range titles {
		if got := titleMatch(tc.in); got != tc.want {
			t.Errorf("titleMatch(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}

	residuals := []struct{ in, want string }{
		{"jack reacher", `"jack" "reacher"`},
		{"the expanse", `"expanse"`},
		{"halo: the fall of reach", `"halo" "fall" "reach"`},
		// Every token is a stopword: the fallback keeps the residual whole
		// rather than composing an empty MATCH.
		{"the of", `"the" "of"`},
	}
	for _, tc := range residuals {
		if got := probeMatch(tc.in); got != tc.want {
			t.Errorf("probeMatch(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// punctuatedCatalog is the end-to-end fixture: titles carrying the punctuation
// shapes above, one of them (Halo: Primordium) with a recording so the ABS
// facade has a BookMetadata to emit, and a series so the series-position boost
// is exercised over the same rows.
func punctuatedCatalog() *model.Catalog {
	bear := &model.Person{ID: "greg-bear", Name: "Greg Bear", License: "CC0-1.0"}
	dadabo := &model.Person{ID: "timothy-dadabo", Name: "Timothy Dadabo", License: "CC0-1.0"}
	morley := &model.Person{ID: "isla-morley", Name: "Isla Morley", License: "CC0-1.0"}
	rowling := &model.Person{ID: "j-k-rowling", Name: "J.K. Rowling", License: "CC0-1.0"}
	bradbury := &model.Person{ID: "ray-bradbury", Name: "Ray Bradbury", License: "CC0-1.0"}
	orwell := &model.Person{ID: "george-orwell", Name: "George Orwell", License: "CC0-1.0"}

	primordium := &model.Work{
		ID: "halo-primordium", Title: "Halo: Primordium", Language: "en",
		Authors: []string{"greg-bear"}, License: "CC0-1.0",
		Recordings: []*model.Recording{{
			ID: "timothy-dadabo-2012", Work: "halo-primordium", Language: "en",
			Narrators: []string{"timothy-dadabo"}, License: "CC0-1.0",
			RuntimeMin: 570, Publisher: "Audible Studios", ReleaseDate: "2012-01-03",
			ISBN: []model.ISBNRef{{ISBN: "9781427215703"}},
		}},
	}
	cryptum := &model.Work{
		ID: "halo-cryptum", Title: "Halo: Cryptum", Language: "en",
		Authors: []string{"greg-bear"}, License: "CC0-1.0",
	}
	dont := &model.Work{
		ID: "dont-look-back", Title: "Don't Look Back", Language: "en",
		Authors: []string{"isla-morley"}, License: "CC0-1.0",
	}
	goblet := &model.Work{
		ID: "harry-potter-and-the-goblet-of-fire", Title: "Harry Potter & the Goblet of Fire",
		Language: "en", Authors: []string{"j-k-rowling"}, License: "CC0-1.0",
	}
	f451 := &model.Work{
		ID: "fahrenheit-451", Title: "Fahrenheit 451", Language: "en",
		Authors: []string{"ray-bradbury"}, License: "CC0-1.0",
	}
	nineteen := &model.Work{
		ID: "1984", Title: "1984", Language: "en",
		Authors: []string{"george-orwell"}, License: "CC0-1.0",
	}
	madchen := &model.Work{
		ID: "die-wut-die-bleibt", Title: "Die Wut, die bleibt: Mädchen", Language: "de",
		Authors: []string{"isla-morley"}, License: "CC0-1.0",
	}

	return &model.Catalog{
		Works:  []*model.Work{primordium, cryptum, dont, goblet, f451, nineteen, madchen},
		People: []*model.Person{bear, dadabo, morley, rowling, bradbury, orwell},
		Series: []*model.Series{{
			ID: "halo", Name: "Halo", License: "CC0-1.0",
			Works: []model.SeriesWork{
				{Work: "halo-cryptum", Position: "7"},
				{Work: "halo-primordium", Position: "8"},
			},
		}},
	}
}

// TestSearchPunctuatedQueries is the end-to-end fix: every one of these queries
// names a work the catalogue holds, and the ones with punctuation standing in
// for a space (or in a different order than the row) returned NOTHING before,
// because the welded phrase demanded the words be adjacent - across two FTS
// columns, in the query's order.
func TestSearchPunctuatedQueries(t *testing.T) {
	snap := snapshotFor(t, punctuatedCatalog())

	cases := []struct {
		name, query, want string
	}{
		{"colon title", "Halo: Primordium", "work:halo-primordium"},
		{"colon, no space", "Halo:Primordium", "work:halo-primordium"},
		{"filename separators", "Greg.Bear-Halo.Primordium", "work:halo-primordium"},
		{"reversed comma", "Primordium,Halo", "work:halo-primordium"},
		{"author comma title", "Bear,Primordium", "work:halo-primordium"},
		{"underscores", "halo_primordium", "work:halo-primordium"},
		{"apostrophe", "Don't Look Back", "work:dont-look-back"},
		{"apostrophe welded", "Don't.Look.Back", "work:dont-look-back"},
		{"ampersand", "Harry Potter & the Goblet of Fire", "work:harry-potter-and-the-goblet-of-fire"},
		{"trailing bang", "Fahrenheit 451!", "work:fahrenheit-451"},
		{"trailing question mark", "1984?", "work:1984"},
		{"umlaut", "Mädchen", "work:die-wut-die-bleibt"},
		// The non-regressions: bare numeric and word+number titles answer
		// exactly as they always did.
		{"numeric title", "1984", "work:1984"},
		{"word plus number", "Fahrenheit 451", "work:fahrenheit-451"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ids := searchIDs(t, snap, tc.query)
			if !contains(ids, tc.want) {
				t.Errorf("search(%q) = %v, want it to hold %q", tc.query, ids, tc.want)
			}
		})
	}
}

// TestSearchPunctuationDegradesQuietly: a query with no term at all is a
// well-formed MATCH that finds nothing, never an FTS5 syntax error surfacing as
// a 500. The handler's own guard rejects an empty q, so these arrive as
// punctuation.
func TestSearchPunctuationDegradesQuietly(t *testing.T) {
	ts := serverFor(t, punctuatedCatalog())
	for _, q := range []string{"!!", "&", "-", "...", `"""`, "?!", "(", "/"} {
		path := "/api/v1/search?q=" + url.QueryEscape(q)
		code, body := getJSON(t, ts.URL, path)
		if code != 200 {
			t.Errorf("GET %s = %d, want 200 (body %v)", path, code, body)
			continue
		}
		if results, _ := body["results"].([]any); len(results) != 0 {
			t.Errorf("GET %s returned %d results, want none", path, len(results))
		}
	}
}

// TestSearchPunctuatedQueryOverHTTP is the combined endpoint's own proof, since
// TestSearchPunctuatedQueries drives the snapshot directly: the fix reaches the
// wire, and the exact-title boost puts the punctuated title FIRST (nameKey
// compares titles with punctuation trimmed, so the query need not spell it).
func TestSearchPunctuatedQueryOverHTTP(t *testing.T) {
	ts := serverFor(t, punctuatedCatalog())
	for _, q := range []string{"Halo: Primordium", "halo primordium", "Greg.Bear-Halo.Primordium"} {
		path := "/api/v1/search?q=" + url.QueryEscape(q)
		code, body := getJSON(t, ts.URL, path)
		if code != 200 {
			t.Fatalf("GET %s = %d", path, code)
		}
		results, _ := body["results"].([]any)
		if len(results) == 0 {
			t.Fatalf("GET %s returned no results", path)
		}
		first, _ := results[0].(map[string]any)
		if first["id"] != "halo-primordium" {
			t.Errorf("GET %s first result = %v, want halo-primordium", path, first["id"])
		}
	}
	// The type-scoped route shares the handler and the builder, so it heals with
	// the combined one.
	code, body := getJSON(t, ts.URL, "/api/v1/works/search?q="+url.QueryEscape("Primordium,Halo"))
	if code != 200 {
		t.Fatalf("works/search: status %d", code)
	}
	if results, _ := body["results"].([]any); len(results) == 0 {
		t.Error("works/search for a reversed punctuated query returned nothing")
	}
}

// TestABSSearchPunctuatedQuery is the facade's proof. ABS sends the title it
// scraped off the shelf, punctuation and all, and it goes through the same
// ftsQuery - so the provider healed with the API.
func TestABSSearchPunctuatedQuery(t *testing.T) {
	base := absServer(t, punctuatedCatalog())
	for _, q := range []string{"Halo: Primordium", "Halo.Primordium", "Greg.Bear-Halo.Primordium"} {
		path := "/abs/search?mediaType=book&query=" + url.QueryEscape(q)
		code, matches := absMatches(t, base, path)
		if code != 200 {
			t.Fatalf("GET %s = %d", path, code)
		}
		if len(matches) == 0 {
			t.Fatalf("GET %s returned no matches", path)
		}
		first, _ := matches[0].(map[string]any)
		if first["title"] != "Halo: Primordium" {
			t.Errorf("GET %s first match = %v, want Halo: Primordium", path, first["title"])
		}
	}
}

// TestSeriesPositionBoostSurvivesPunctuation: the series probe reads the same
// builder, so a punctuated series name now resolves its volume too - while the
// existing seriespos suite pins that every unpunctuated spelling ("jack reacher
// book 2", "#2", "vol. 2") answers exactly as before.
func TestSeriesPositionBoostSurvivesPunctuation(t *testing.T) {
	snap := snapshotFor(t, punctuatedCatalog())
	// "halo 7" is a series-position query: Halo #7 is Cryptum, which the plain
	// FTS page cannot rank first (nothing in its row says "7").
	if got := first(searchIDs(t, snap, "halo 7")); got != "work:halo-cryptum" {
		t.Errorf(`search("halo 7") first = %q, want work:halo-cryptum`, got)
	}
	if got := first(searchIDs(t, snap, "halo: 7")); got != "work:halo-cryptum" {
		t.Errorf(`search("halo: 7") first = %q, want work:halo-cryptum`, got)
	}
}
