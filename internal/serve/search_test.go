package serve

import (
	"net/url"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// The MATCH builder's tests: ftsMatch is the one place a query becomes an FTS5
// expression, so every query shape is pinned here and the surfaces that share it
// are proved end-to-end below.

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

		// Non-ASCII letters and the NUMBER classes beyond the ASCII digits are
		// term runes, because unicode61 indexes them: a German or Spanish title
		// is one term per word (composed and decomposed alike - a combining mark
		// stays inside its term), a Roman numeral is a term, and a superscript
		// digit stays welded to the word it decorates exactly as in the index.
		{"german umlaut", "Mädchen", `"Mädchen"*`},
		{"german umlaut decomposed", "Ma\u0308dchen", "\"Ma\u0308dchen\"*"},
		{"spanish accent", "Corazón Salvaje", `"Corazón" "Salvaje"*`},
		{"german sharp s", "Die Straße", `"Die" "Straße"*`},
		{"roman numeral", "Henry Ⅶ", `"Henry" "Ⅶ"*`},
		{"vulgar fraction", "Half ½ Measures", `"Half" "½" "Measures"*`},
		{"superscript digit", "Super² Nova", `"Super²" "Nova"*`},

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

	// The no-prefix-star twin differs in exactly that, which is what keeps the
	// boosts' keystroke-hot probes bounded to whole words (see seriespos.go).
	for in, want := range map[string]string{"Halo: Primordium": `"Halo" "Primordium"`, "!!": `""`} {
		if got := ftsPhrase(in); got != want {
			t.Errorf("ftsPhrase(%q) = %q, want %q", in, got, want)
		}
	}
}

// punctuatedCatalog is the end-to-end fixture: titles carrying the punctuation
// shapes above, one of them (Halo: Primordium) with a recording so the ABS
// facade has a BookMetadata to emit, and a series so the series-position boost
// is exercised over the same rows. The umlaut and Roman-numeral titles are the
// SQLite oracle for isTermRune: they only resolve if the Go term rule agrees
// with what unicode61 actually indexed.
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
	// A Roman numeral (Nl) is a token to unicode61, so it has to be one to
	// ftsTerms as well - with IsDigit alone the term vanished from the query.
	henry := &model.Work{
		ID: "henry-vii", Title: "Henry Ⅶ", Language: "en",
		Authors: []string{"isla-morley"}, License: "CC0-1.0",
	}

	return &model.Catalog{
		Works:  []*model.Work{primordium, cryptum, dont, goblet, f451, nineteen, madchen, henry},
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
		// The oracle cases: a Roman numeral must survive as a term, and a query
		// written DECOMPOSED must still find a composed title (unicode61 strips
		// the diacritic, so the mark has to stay inside the term rather than
		// splitting it in two).
		{"roman numeral", "Henry Ⅶ", "work:henry-vii"},
		// The numeral ALONE is where the oracle bites: if the term rule stopped
		// admitting Nl/No the query would hold no term at all and answer nothing,
		// while "Henry Ⅶ" would still pass on its first word.
		{"roman numeral alone", "Ⅶ", "work:henry-vii"},
		{"umlaut", "Mädchen", "work:die-wut-die-bleibt"},
		{"umlaut decomposed query", "Ma\u0308dchen", "work:die-wut-die-bleibt"},
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
	for _, q := range []string{"!!", `"""`} {
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

// TestSearchPunctuatedQueryOverHTTP proves the fix reaches the wire (the table
// above drives the snapshot directly), and that the exact-title boost puts the
// punctuated title FIRST: nameKey reads the same terms, so a query spelling the
// title without its space still names it outright.
func TestSearchPunctuatedQueryOverHTTP(t *testing.T) {
	ts := serverFor(t, punctuatedCatalog())
	const path = "/api/v1/search?q=Halo%3APrimordium"
	code, body := getJSON(t, ts.URL, path)
	if code != 200 {
		t.Fatalf("GET %s = %d", path, code)
	}
	results, _ := body["results"].([]any)
	if len(results) == 0 {
		t.Fatalf("GET %s returned no results", path)
	}
	if first, _ := results[0].(map[string]any); first["id"] != "halo-primordium" {
		t.Errorf("GET %s first result = %v, want halo-primordium", path, first["id"])
	}
}

// TestABSSearchPunctuatedQuery is the facade's proof: ABS sends the title it
// scraped off the shelf, punctuation and all, and it goes through the same
// ftsQuery - so the provider healed with the API.
func TestABSSearchPunctuatedQuery(t *testing.T) {
	base := absServer(t, punctuatedCatalog())
	const path = "/abs/search?mediaType=book&query=Greg.Bear-Halo.Primordium"
	code, matches := absMatches(t, base, path)
	if code != 200 {
		t.Fatalf("GET %s = %d", path, code)
	}
	if len(matches) == 0 {
		t.Fatalf("GET %s returned no matches", path)
	}
	if first, _ := matches[0].(map[string]any); first["title"] != "Halo: Primordium" {
		t.Errorf("GET %s first match = %v, want Halo: Primordium", path, first["title"])
	}
}

// TestSeriesPositionBoostSurvivesPunctuation: the series probe reads the same
// terms, so a punctuated series name resolves its volume too - while the
// position itself is deliberately NOT read through ftsTerms, which would shred
// "2.5" into two digits. "halo:7" is the pin on that boundary: the position
// parse splits on whitespace, so a welded number is not a position query at all
// and the page stays the plain FTS one.
func TestSeriesPositionBoostSurvivesPunctuation(t *testing.T) {
	snap := snapshotFor(t, punctuatedCatalog())
	// Halo #7 is Cryptum, which the plain FTS page cannot rank first (nothing in
	// its row says "7").
	for _, q := range []string{"halo 7", "halo: 7"} {
		if got := first(searchIDs(t, snap, q)); got != "work:halo-cryptum" {
			t.Errorf("search(%q) first = %q, want work:halo-cryptum", q, got)
		}
	}
	if _, ok := parseSeriesPositionQuery("halo:7"); ok {
		t.Error(`parseSeriesPositionQuery("halo:7") read a welded token as a position`)
	}
	if got := first(searchIDs(t, snap, "halo:7")); got == "work:halo-cryptum" {
		t.Error(`search("halo:7") boosted the volume; the position parse must stay whitespace-split`)
	}
}
