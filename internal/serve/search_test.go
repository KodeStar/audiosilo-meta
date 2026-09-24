package serve

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// The MATCH builder's tests: ftsMatch is the one place a query becomes an FTS5
// expression, so every query shape is pinned here and the surfaces that share it
// are proved end-to-end below.

// TestFTSQueryBuilder pins the MATCH expression for every query shape:
// punctuation as a word boundary, an initialism kept as ONE adjacent phrase, the
// prefix-star on the last phrase, and the empty-phrase sentinel for a query
// holding no term at all.
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
		{"underscores", "the_way_of_kings", `"the" AND "way" AND "of" AND ("kings"* OR "king s")`},
		{"slash", "Crime/Punishment", `"Crime" "Punishment"*`},

		// Punctuation that stands alone, or trails, is simply gone.
		{"ampersand", "Harry Potter & the Goblet", `"Harry" "Potter" "the" "Goblet"*`},
		{"parenthesised", "The Bell (Whitechapel)", `"The" "Bell" "Whitechapel"*`},
		{"trailing bang", "Watchers: Culloden!", `("Watchers" OR "Watcher s") AND "Culloden"*`},
		{"trailing question mark", "Do Androids Dream?", `"Do" AND ("Androids" OR "Android s") AND "Dream"*`},
		{"lone dash", "Age of Trinity - Die Stunde", `"Age" "of" "Trinity" "Die" "Stunde"*`},

		// Apostrophes and hyphens split too: unicode61 tokenized them apart on
		// the way INTO the index, so the terms are what the row holds.
		{"apostrophe", "Don't Look Back", `"Don" "t" "Look" "Back"*`},
		// (A written possessive is the mirror group - see TestFTSQueryPossessives.)
		{"typographic apostrophe", "The Ring’s Secret", `"The" AND ("Ring s" OR "Rings") AND "Secret"*`},
		{"hyphenated name", "Spider-Man", `"Spider" "Man"*`},

		// Numeric titles are one term and keep exactly the expression they had.
		{"numeric title", "1984", `"1984"*`},
		{"word plus number", "Fahrenheit 451", `"Fahrenheit" "451"*`},

		// An INITIALISM - every term one rune - is punctuation inside ONE word,
		// so it stays one adjacent phrase and keeps the selectivity a
		// conjunction of the commonest tokens would throw away ("Q" AND anything
		// starting with "A" matches "Alpha Q" too). This is byte-for-byte what
		// the whitespace-only predecessor emitted for these tokens.
		{"ampersand initialism", "Q&A", `"Q A"*`},
		{"starred initialism", "M*A*S*H", `"M A S H"*`},
		{"dotted initialism", "N.E.R.D.S.", `"N E R D S"*`},
		{"dotted initialism plus volume", "N.E.R.D.S. 1", `"N E R D S" "1"*`},
		{"two-letter abbreviation", "A.D.", `"A D"*`},
		{"slashed initialism", "Y/N", `"Y N"*`},
		{"lowercase abbreviation", "3 a.m.", `"3" "a m"*`},
		// A decimal and an omnibus range are the same shape, which is what keeps
		// "2.5" a distinct volume rather than the tokens 2 and 5 anywhere.
		{"decimal", "Reacher 2.5", `"Reacher" "2 5"*`},
		{"omnibus range", "Reacher 1-3.5", `"Reacher" "1 3 5"*`},
		// Mixed tokens split: one term is longer than a rune, so the token is
		// punctuation BETWEEN words.
		{"mixed initialism and word", "M*A*S*H Reunion", `"M A S H" "Reunion"*`},

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
		{"vulgar fraction", "Half ½ Measures", `"Half" AND "½" AND ("Measures"* OR "Measure s")`},
		{"superscript digit", "Super² Nova", `"Super²" "Nova"*`},

		// No term at all: the harmless empty-phrase match, never a syntax error.
		{"whitespace only", "   ", `""`},
		{"punctuation only", "!!", `""`},
		{"quotes only", `"""`, `""`},
		{"empty", "", `""`},

		// A quote is a boundary like any other punctuation, so it can never
		// reach the inside of a phrase (here two one-rune terms, so the
		// initialism rule keeps them adjacent - as the predecessor did).
		{"embedded quote", `a"b`, `"a b"*`},
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

	// The initialism cohort. "Q&A" is the selectivity case and "Alpha Q" is its
	// adversary: it holds a "q" token and a token STARTING with "a", so it
	// matches the conjunction a split would build and not the adjacent phrase.
	// Both carry a recording, so /abs/search sees the same distinction.
	rec := func(id, work string) []*model.Recording {
		return []*model.Recording{{
			ID: id, Work: work, Language: "en", License: "CC0-1.0",
			Narrators: []string{"timothy-dadabo"}, RuntimeMin: 300,
		}}
	}
	qanda := &model.Work{
		ID: "q-and-a", Title: "Q&A", Language: "en",
		Authors: []string{"isla-morley"}, License: "CC0-1.0",
		Recordings: rec("qanda-2019", "q-and-a"),
	}
	alphaQ := &model.Work{
		ID: "alpha-q", Title: "Alpha Q", Language: "en",
		Authors: []string{"isla-morley"}, License: "CC0-1.0",
		Recordings: rec("alpha-q-2019", "alpha-q"),
	}
	mash := &model.Work{
		ID: "mash", Title: "M*A*S*H", Language: "en",
		Authors: []string{"isla-morley"}, License: "CC0-1.0",
	}
	// An all-initials SERIES: its volumes' FTS rows say nothing about a position,
	// so "N.E.R.D.S. 2" has an EMPTY plain FTS page and the boost is the only
	// thing that can answer it - which is exactly what the reworked cost gate
	// took away for this whole class.
	nerds := &model.Work{
		ID: "nerds", Title: "N.E.R.D.S.", Language: "en",
		Authors: []string{"isla-morley"}, License: "CC0-1.0",
	}
	mamasBoy := &model.Work{
		ID: "m-is-for-mamas-boy", Title: "M Is for Mama's Boy", Language: "en",
		Authors: []string{"isla-morley"}, License: "CC0-1.0",
	}

	return &model.Catalog{
		Works: []*model.Work{
			primordium, cryptum, dont, goblet, f451, nineteen, madchen, henry,
			qanda, alphaQ, mash, nerds, mamasBoy,
		},
		People: []*model.Person{bear, dadabo, morley, rowling, bradbury, orwell},
		Series: []*model.Series{
			{
				ID: "halo", Name: "Halo", License: "CC0-1.0",
				Works: []model.SeriesWork{
					{Work: "halo-cryptum", Position: "7"},
					{Work: "halo-primordium", Position: "8"},
				},
			},
			{
				ID: "nerds", Name: "N.E.R.D.S.", License: "CC0-1.0",
				Works: []model.SeriesWork{
					{Work: "nerds", Position: "1"},
					{Work: "m-is-for-mamas-boy", Position: "2"},
				},
			},
		},
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

// TestSearchInitialisms is the other half of the punctuation rule: a title whose
// every fragment is one letter must NOT be split, because the conjunction that
// would replace its phrase is made of the commonest tokens in the index. On the
// 279k-work artifact splitting cost 12 of the 21 all-initials series an EMPTY
// result page, dropped the right book off /abs/search's ten matches, and ran
// 30-45% slower for the class; here the adversary is "Alpha Q", which matches
// the conjunction and not the phrase.
func TestSearchInitialisms(t *testing.T) {
	snap := snapshotFor(t, punctuatedCatalog())

	// Selectivity: the phrase resolves the initialism and nothing else.
	ids := searchIDs(t, snap, "Q&A")
	if first(ids) != "work:q-and-a" {
		t.Errorf(`search("Q&A") first = %q, want work:q-and-a (page: %v)`, first(ids), ids)
	}
	if contains(ids, "work:alpha-q") {
		t.Errorf(`search("Q&A") returned work:alpha-q: %v - the initialism was split into a conjunction`, ids)
	}

	// The exact-title boost still reaches an initialism title (the cost gate has
	// to admit it, and nameKey has to fold it).
	if got := first(searchIDs(t, snap, "M*A*S*H")); got != "work:mash" {
		t.Errorf(`search("M*A*S*H") first = %q, want work:mash`, got)
	}

	// The series-position boost too - and here the plain FTS page is empty, so
	// the boost is the ONLY source of the answer.
	hits, err := snap.ftsHits(kindAny, ftsQuery("N.E.R.D.S. 2"), 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("the fixture no longer has an empty FTS page for 'N.E.R.D.S. 2': %v", hits)
	}
	// (An undotted "nerds 2" resolves nothing, before this change and after: the
	// row's tokens are n/e/r/d/s, so "nerds" is a different word to unicode61.)
	for query, want := range map[string]string{
		"N.E.R.D.S. 1": "work:nerds",
		"N.E.R.D.S. 2": "work:m-is-for-mamas-boy",
	} {
		if got := first(searchIDs(t, snap, query)); got != want {
			t.Errorf("search(%q) first = %q, want %q", query, got, want)
		}
	}
}

// TestABSSearchInitialism: the facade has no boost to fall back on, so the
// builder's selectivity is all it has. ABS caps at ten matches, and on the real
// artifact a split initialism pushed the right book off that list entirely.
func TestABSSearchInitialism(t *testing.T) {
	base := absServer(t, punctuatedCatalog())
	const path = "/abs/search?mediaType=book&query=Q%26A"
	code, matches := absMatches(t, base, path)
	if code != 200 {
		t.Fatalf("GET %s = %d", path, code)
	}
	if len(matches) != 1 {
		t.Fatalf("GET %s returned %d matches, want 1 (the conjunction also matches Alpha Q)", path, len(matches))
	}
	if first, _ := matches[0].(map[string]any); first["title"] != "Q&A" {
		t.Errorf("GET %s match = %v, want Q&A", path, first["title"])
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

// TestFTSQueryIsBounded pins the two caps on one query. The search surfaces are
// unauthenticated, CORS-open and hit per keystroke, and every phrase in a MATCH
// expression is a posting-list walk - so the length of the walk list must be the
// server's to decide, not the caller's. Both bounds TRUNCATE (this is a
// per-keystroke UI), and what survives is the START of what was typed, with the
// prefix star still on the last phrase KEPT.
func TestFTSQueryIsBounded(t *testing.T) {
	t.Run("phrase count", func(t *testing.T) {
		// Distinct TWO-character tokens, so the phrase cap is what binds here
		// rather than the byte cap (see the constants: a query long enough to
		// hold maxQueryPhrases ordinary words is past maxQueryBytes already).
		words := make([]string, 0, maxQueryPhrases+8)
		for i := range cap(words) {
			words = append(words, capWord(i))
		}
		query := strings.Join(words, " ")
		if len(query) > maxQueryBytes {
			t.Fatalf("the fixture query is %d bytes, past the byte cap - it no longer tests the phrase cap", len(query))
		}
		got := ftsQuery(query)
		phrases := strings.Count(got, `"`) / 2
		if phrases != maxQueryPhrases {
			t.Errorf("phrases = %d, want the cap %d: %s", phrases, maxQueryPhrases, got)
		}
		if !strings.HasPrefix(got, `"`+words[0]+`" "`+words[1]+`"`) {
			t.Errorf("the kept phrases are not the leading ones: %s", got)
		}
		if !strings.HasSuffix(got, `"`+words[maxQueryPhrases-1]+`"*`) {
			t.Errorf("the last kept phrase lost its prefix star: %s", got)
		}
		// Punctuation splits into phrases too, so the cap has to count what the
		// expression actually holds rather than whitespace tokens. (The terms are
		// two runes each: a token whose terms are ALL single runes is an
		// initialism, which is deliberately ONE phrase - see tokenPhrases.)
		dense := strings.TrimSuffix(strings.Repeat("aa.bb.cc.", maxQueryPhrases/3+2), ".")
		if len(dense) > maxQueryBytes {
			t.Fatalf("the dense fixture is %d bytes, past the byte cap", len(dense))
		}
		if n := strings.Count(ftsQuery(dense), `"`) / 2; n != maxQueryPhrases {
			t.Errorf("punctuation-split phrases = %d, want the cap %d", n, maxQueryPhrases)
		}
	})

	t.Run("byte length", func(t *testing.T) {
		// One enormous token: the phrase cap cannot bound this one, the byte cap
		// must.
		long := strings.Repeat("a", maxQueryBytes*4)
		got := ftsQuery(long)
		if len(got) > maxQueryBytes+3 { // the two quotes and the star
			t.Errorf("a %d-byte token produced a %d-byte expression", len(long), len(got))
		}
		// A multi-byte rune must not be cut in half: every term FTS5 indexed is
		// whole runes, so half of one is a term no row can hold.
		wide := strings.Repeat("é", maxQueryBytes) // two bytes each
		if got := ftsQuery(wide); !utf8.ValidString(got) {
			t.Errorf("the cut split a rune: %q", got)
		}
	})

	t.Run("the search routes apply it", func(t *testing.T) {
		// End to end: an over-long query is answered, not refused, and the
		// answer is the one its bounded prefix earns.
		_, ts := newWatchFeedServer(t, fixtureCatalog())
		long := "hail " + strings.Repeat("x", maxQueryBytes*2)
		for _, path := range []string{
			"/api/v1/search?q=", "/api/v1/works/search?q=",
			"/api/v1/people/search?q=", "/api/v1/series/search?q=",
			"/abs/search?query=",
		} {
			resp, body := watchFeedResponse(t, ts, path+url.QueryEscape(long))
			if resp.StatusCode != http.StatusOK {
				t.Errorf("GET %s = %d, want 200; body %s", path, resp.StatusCode, body)
			}
		}
	})
}

// TestFTSQueryPossessives pins the possessive group: unicode61 indexed
// "Ender's" as `ender` + `s` and "Finnegans" as one word, so a possessive typed
// in EITHER spelling matches both - and a term that cannot be one keeps exactly
// the expression it always had.
func TestFTSQueryPossessives(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"possessive then word", "enders game", `("enders" OR "ender s") AND "game"*`},
		// The star goes on the literal branch alone: the literal may still grow
		// ("endersby"), the possessive reading is already a whole `s` token.
		{"possessive last", "enders", `("enders"* OR "ender s")`},
		{"keystroke before the s", "ender", `"ender"*`},
		{"several", "hitchhikers guides", `("hitchhikers" OR "hitchhiker s") AND ("guides"* OR "guide s")`},
		{"case is kept", "ENDERS", `("ENDERS"* OR "ENDER S")`},
		{"decade", "1980s", `("1980s"* OR "1980 s")`},
		{"non-ASCII base", "Mädchens", `("Mädchens"* OR "Mädchen s")`},
		// Written WITH the apostrophe: the mirror group, the two terms as one
		// adjacent phrase or the word a title stored without it holds.
		{"apostrophe kept", "Ender's Game", `("Ender s" OR "Enders") AND "Game"*`},
		{"typographic apostrophe", "Finnegan’s Wake", `("Finnegan s" OR "Finnegans") AND "Wake"*`},
		{"apostrophe last", "ender's", `("ender s"* OR "enders")`},
		// Only an apostrophe makes a lone s a possessive, and only when the
		// joined word would qualify on its own.
		{"free-standing S", "Model S", `"Model" "S"*`},
		{"hyphenated S", "Model-S", `"Model" "S"*`},
		{"joined word too short", "it's", `"it" "s"*`},
		// "jamess" doubles the s, so the written s stays its own phrase ("James"
		// itself is a word ending in s, and gets the ordinary group).
		{"joined word doubles the s", "James's", `("James" OR "Jame s") AND "s"*`},
		// The bounds: three runes or fewer (the articles das/des/los and his,
		// was), and a doubled s (darkness, princess) are never possessives.
		{"three runes", "his das its", `"his" "das" "its"*`},
		{"doubled s", "princess darkness", `"princess" "darkness"*`},
		{"lone s", "s", `"s"*`},
		// An initialism's fragments are single runes: never a group.
		{"initialism", "N.E.R.D.S.", `"N E R D S"*`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ftsQuery(tc.in); got != tc.want {
				t.Errorf("ftsQuery(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
	if got, want := ftsPhrase("enders game"), `("enders" OR "ender s") AND "game"`; got != want {
		t.Errorf("ftsPhrase = %q, want %q", got, want)
	}
}

// capWord is the i-th distinct two-character token the cap tests build their
// queries from: short enough that the phrase cap binds before the byte cap, and
// never ending in s, so it is always one plain phrase.
func capWord(i int) string { return string(rune('a'+i/10)) + strconv.Itoa(i%10) }

// TestFTSQueryPossessiveCap: a group holds two phrases and counts as two
// against maxQueryPhrases, and a group that would cross the cap is rendered as
// its plain phrase rather than dropped - an expansion never costs a term.
func TestFTSQueryPossessiveCap(t *testing.T) {
	t.Run("groups count two", func(t *testing.T) {
		// Two more words than the groups the cap has room for.
		words := make([]string, 0, maxQueryPhrases/2+2)
		for i := range cap(words) {
			words = append(words, capWord(i)+"xs") // four runes ending in s
		}
		query := strings.Join(words, " ")
		if len(query) > maxQueryBytes {
			t.Fatalf("the fixture query is %d bytes, past the byte cap", len(query))
		}
		got := ftsQuery(query)
		if n := strings.Count(got, `"`) / 2; n != maxQueryPhrases {
			t.Errorf("phrases = %d, want the cap %d: %s", n, maxQueryPhrases, got)
		}
		last := words[maxQueryPhrases/2-1]
		if want := `("` + last + `"* OR "` + strings.TrimSuffix(last, "s") + ` s")`; !strings.HasSuffix(got, want) {
			t.Errorf("the last kept group is not %s: %s", want, got)
		}
	})

	t.Run("a group past the cap is cut like any term", func(t *testing.T) {
		words := make([]string, 0, maxQueryPhrases)
		for i := range maxQueryPhrases - 1 {
			words = append(words, capWord(i))
		}
		words = append(words, "zzxs")
		got := ftsQuery(strings.Join(words, " "))
		// One slot is left and the group needs two: it is not half-rendered as
		// a phrase that may match nothing, it is cut, and the leading phrases
		// are the expression.
		if n := strings.Count(got, `"`) / 2; n != maxQueryPhrases-1 {
			t.Errorf("phrases = %d, want %d: %s", n, maxQueryPhrases-1, got)
		}
		if !strings.HasSuffix(got, `"`+capWord(maxQueryPhrases-2)+`"*`) || strings.Contains(got, "zzxs") {
			t.Errorf("the group past the cap was not cut: %s", got)
		}
	})
}

// TestNameKeyFoldsPossessives: the whole-name comparison folds a possessive
// exactly where retrieval expands one, so the exact-title and series boosts
// accept the title the query just matched.
func TestNameKeyFoldsPossessives(t *testing.T) {
	cases := map[string]string{
		"Ender's Game":     "enders game",
		"enders game":      "enders game",
		"Ender’s Game":     "enders game",
		"The Hitchhiker's": "the hitchhikers",
		"N.E.R.D.S.":       "n e r d s", // one-rune fragments stay apart
		"It's":             "it s",      // "its" is never expanded, so never folded
		"James's Journey":  "james s journey",
		"Ender´s Game":     "enders game", // the accent a keyboard reaches for
		// Only an apostrophe folds: a free-standing S is a middle initial or a
		// model letter, and "models" must not boost a book titled "Model S".
		"Harry S. Truman":   "harry s truman",
		"Model S":           "model s",
		"Model-S":           "model s",
		"ender s game":      "ender s game",
		"Hitchhikers Guide": "hitchhikers guide",
	}
	for in, want := range cases {
		if got := nameKey(in); got != want {
			t.Errorf("nameKey(%q) = %q, want %q", in, got, want)
		}
	}
}

// possessiveCatalog holds three titles carrying a possessive, each with a
// recording so the ABS facade has a BookMetadata to emit, plus distractors that
// share their other words - so a target ranking first is the fix, not an
// accident of a one-row index.
func possessiveCatalog() *model.Catalog {
	person := func(id, name string) *model.Person {
		return &model.Person{ID: id, Name: name, License: "CC0-1.0"}
	}
	work := func(id, title, author string) *model.Work {
		return &model.Work{
			ID: id, Title: title, Language: "en", Authors: []string{author}, License: "CC0-1.0",
			Recordings: []*model.Recording{{
				ID: id + "-rec", Work: id, Language: "en", License: "CC0-1.0",
				Narrators: []string{"stefan-rudnicki"}, RuntimeMin: 600,
			}},
		}
	}
	return &model.Catalog{
		Works: []*model.Work{
			work("enders-game", "Ender's Game", "orson-scott-card"),
			work("speaker-for-the-dead", "Speaker for the Dead", "orson-scott-card"),
			work("the-hitchhikers-guide-to-the-galaxy", "The Hitchhiker's Guide to the Galaxy", "douglas-adams"),
			work("harry-potter-and-the-philosophers-stone", "Harry Potter and the Philosopher's Stone", "j-k-rowling"),
			// Stored WITHOUT the apostrophe, as the book itself spells it: the
			// written-possessive query has to reach it the other way round.
			work("finnegans-wake", "Finnegans Wake", "james-joyce"),
			// Distractors: every other word of the targets, none of them the book.
			work("game-on", "Game On", "jane-doe"),
			work("the-game", "The Game", "jane-doe"),
			work("a-guide-to-the-stars", "A Guide to the Stars", "jane-doe"),
			work("harry-potter-and-the-stone-age", "Harry Potter and the Stone Age", "jane-doe"),
		},
		People: []*model.Person{
			person("orson-scott-card", "Orson Scott Card"),
			person("douglas-adams", "Douglas Adams"),
			person("james-joyce", "James Joyce"),
			person("j-k-rowling", "J.K. Rowling"),
			person("jane-doe", "Jane Doe"),
			person("stefan-rudnicki", "Stefan Rudnicki"),
		},
		Series: []*model.Series{{
			ID: "enders-game", Name: "Ender's Game", License: "CC0-1.0",
			Works: []model.SeriesWork{
				{Work: "enders-game", Position: "1"},
				{Work: "speaker-for-the-dead", Position: "2"},
			},
		}},
	}
}

// TestSearchPossessivesWithoutApostrophe is the end-to-end fix, on both the
// combined search and the work scope: each query drops a possessive's
// apostrophe, which before this returned NOTHING - the row holds `ender` and
// `s`, never `enders`.
func TestSearchPossessivesWithoutApostrophe(t *testing.T) {
	snap := snapshotFor(t, possessiveCatalog())

	// The bug, pinned on the fixture: the expression the builder used to emit
	// matches nothing at all.
	if hits, err := snap.ftsHits(kindAny, `"enders" "game"*`, 20); err != nil || len(hits) != 0 {
		t.Fatalf("the fixture no longer reproduces the bug: %v %v", hits, err)
	}

	cases := []struct {
		query, want string
		first       bool // the exact-title or series boost puts it first
	}{
		{"enders game", "work:enders-game", true},
		{"Enders Game", "work:enders-game", true},
		{"hitchhikers guide", "work:the-hitchhikers-guide-to-the-galaxy", false},
		{"the hitchhikers guide to the galaxy", "work:the-hitchhikers-guide-to-the-galaxy", true},
		{"harry potter and the philosophers stone", "work:harry-potter-and-the-philosophers-stone", true},
		// The series boost reads the same words: "enders game 2" is volume 2
		// of the series named "Ender's Game".
		{"enders game 2", "work:speaker-for-the-dead", true},
		// And WITH the apostrophe, in both directions: the written form still
		// finds the apostrophe title, and reaches the one stored without it.
		{"ender's game", "work:enders-game", true},
		{"the hitchhiker’s guide to the galaxy", "work:the-hitchhikers-guide-to-the-galaxy", true},
		{"finnegan's wake", "work:finnegans-wake", true},
		{"finnegans wake", "work:finnegans-wake", true},
		{"ender's game 2", "work:speaker-for-the-dead", true},
	}
	for _, tc := range cases {
		for _, kind := range []searchKind{kindAny, kindWork} {
			ids := scopedIDs(t, snap, kind, tc.query)
			if tc.first && first(ids) != tc.want {
				t.Errorf("search(%s, %q) first = %q, want %q (page %v)", kind, tc.query, first(ids), tc.want, ids)
			}
			if !contains(ids, tc.want) {
				t.Errorf("search(%s, %q) = %v, want it to hold %q", kind, tc.query, ids, tc.want)
			}
		}
	}

	// And on the wire, once.
	ts := serverFor(t, possessiveCatalog())
	const path = "/api/v1/works/search?q=enders+game"
	code, body := getJSON(t, ts.URL, path)
	results, _ := body["results"].([]any)
	if code != http.StatusOK || len(results) == 0 {
		t.Fatalf("GET %s = %d with %d results", path, code, len(results))
	}
	if m, _ := results[0].(map[string]any); m["id"] != "enders-game" {
		t.Errorf("GET %s first result = %v, want enders-game", path, m["id"])
	}
}

// TestABSSearchPossessivesWithoutApostrophe: Audiobookshelf searches with the
// title it read off a folder or file name, which is where an apostrophe is most
// often dropped - and the facade has no boost to fall back on.
func TestABSSearchPossessivesWithoutApostrophe(t *testing.T) {
	base := absServer(t, possessiveCatalog())
	cases := []struct{ query, author, want string }{
		{"Harry Potter and the Philosophers Stone", "J.K. Rowling", "Harry Potter and the Philosopher's Stone"},
		{"Enders Game", "Orson Scott Card", "Ender's Game"},
		{"Hitchhikers Guide to the Galaxy", "Douglas Adams", "The Hitchhiker's Guide to the Galaxy"},
		{"Enders Game", "", "Ender's Game"},
		{"Finnegan's Wake", "James Joyce", "Finnegans Wake"},
	}
	for _, tc := range cases {
		path := "/abs/search?mediaType=book&query=" + url.QueryEscape(tc.query)
		if tc.author != "" {
			path += "&author=" + url.QueryEscape(tc.author)
		}
		code, matches := absMatches(t, base, path)
		if code != http.StatusOK {
			t.Fatalf("GET %s = %d", path, code)
		}
		if len(matches) == 0 {
			t.Errorf("GET %s returned no matches", path)
			continue
		}
		if m, _ := matches[0].(map[string]any); m["title"] != tc.want {
			t.Errorf("GET %s first match = %v, want %q", path, m["title"], tc.want)
		}
	}
}

// TestExactTitleIgnoresAFreeStandingS: retrieval lets "models" reach a row
// holding "model" + "s" whatever separated them, but only an apostrophe makes
// that a possessive - so the exact-title boost must not put "Model S" first for
// a query that says "models".
func TestExactTitleIgnoresAFreeStandingS(t *testing.T) {
	cat := possessiveCatalog()
	cat.Works = append(cat.Works, &model.Work{
		ID: "model-s", Title: "Model S", Language: "en", Authors: []string{"jane-doe"}, License: "CC0-1.0",
	})
	snap := snapshotFor(t, cat)
	for q, want := range map[string]string{"models": "", "Model S": "model-s", "enders game": "enders-game"} {
		ids, err := snap.exactTitleHits(q)
		if err != nil {
			t.Fatalf("exactTitleHits(%q): %v", q, err)
		}
		if got := first(ids); got != want || len(ids) > 1 {
			t.Errorf("exactTitleHits(%q) = %v, want [%s]", q, ids, want)
		}
	}
}
