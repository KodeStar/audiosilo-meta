package serve

import (
	"bytes"
	"log"
	"reflect"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// exactTitleCatalog is the fixture for exact-title search. It reproduces issue
// #1838 in miniature - a short common title on a heavily credited work, sharing
// its word with a family of longer titles - and adds the adversarial cases: a
// second work with the SAME title, a person and a series carrying the title as
// their own name, titles that differ from the query only in punctuation, a work
// whose title is a junk-guard word, and a series-position query that also names
// a work outright.
func exactTitleCatalog() *model.Catalog {
	person := func(id, name string) *model.Person {
		return &model.Person{ID: id, Name: name, License: "CC0-1.0"}
	}
	// A work whose FTS row is long: many narrators, which is exactly what sinks
	// the real "Spare" below its shorter-titled neighbours under bm25.
	work := func(id, title string, authors []string, narrators ...string) *model.Work {
		w := &model.Work{
			ID: id, Title: title, Language: "en", License: "CC0-1.0", Authors: authors,
		}
		if len(narrators) > 0 {
			w.Recordings = []*model.Recording{{
				ID: "r1", Work: id, Language: "en", License: "CC0-1.0", Narrators: narrators,
			}}
		}
		return w
	}
	return &model.Catalog{
		People: []*model.Person{
			person("prince-harry", "Prince Harry The Duke of Sussex"),
			person("violet-fox", "Violet Fox"),
			person("sally-rogers-davidson", "Sally Rogers-Davidson"),
			person("andy-weir", "Andy Weir"),
			person("eric-nylund", "Eric Nylund"),
			person("kate-reading", "Kate Reading"),
			person("michael-kramer", "Michael Kramer"),
			person("ray-porter", "Ray Porter"),
			// A person whose NAME is the query: the probe is kind-scoped to works,
			// so this must never be boosted onto a page.
			person("spare", "Spare"),
		},
		Works: []*model.Work{
			work("spare", "Spare", []string{"prince-harry"},
				"kate-reading", "michael-kramer", "ray-porter"),
			work("spare-violet-fox", "Spare", []string{"violet-fox"}),
			work("spare-us", "Spare Us!", []string{"violet-fox"}),
			work("spare-parts", "Spare Parts", []string{"sally-rogers-davidson"}),
			work("spare-parts-andy-weir", "Spare Parts", []string{"andy-weir"}),
			work("spare-room", "Spare Room", []string{"violet-fox"}),
			work("the-spare-man", "The Spare Man", []string{"violet-fox"}),
			// Punctuation and case are not identity (nameKey).
			work("the-martian", "The Martian", []string{"andy-weir"}),
			work("the-martian-chronicles", "The Martian Chronicles", []string{"andy-weir"}),
			// The junk guard's fixtures: real works whose whole title is a word the
			// guard refuses to probe, so a nil answer there is the SKIP.
			work("the", "The", []string{"violet-fox"}),
			work("a", "A", []string{"violet-fox"}),
			work("s", "S", []string{"violet-fox"}),
			// The series-position interaction: a work TITLED "Halo 2" that is not
			// the volume at position 2 of the Halo series.
			work("halo-2", "Halo 2", []string{"violet-fox"}),
			work("halo-combat-evolved", "Halo: Combat Evolved", []string{"eric-nylund"}),
			work("halo-fall-of-reach", "Halo: The Fall of Reach", []string{"eric-nylund"}),
		},
		Series: []*model.Series{
			{
				ID: "halo", Name: "Halo", License: "CC0-1.0",
				Works: []model.SeriesWork{
					{Work: "halo-combat-evolved", Position: "1"},
					{Work: "halo-fall-of-reach", Position: "2"},
				},
			},
			// A series whose NAME is the query, for the same reason as the person.
			{
				ID: "spare-series", Name: "Spare", License: "CC0-1.0",
				Works: []model.SeriesWork{{Work: "spare-room", Position: "1"}},
			},
		},
	}
}

// TestExactTitleBoost is the feature: the work whose title the user typed leads
// the page, on the combined search and on the works scope alike, in every
// spelling nameKey folds together.
func TestExactTitleBoost(t *testing.T) {
	snap := snapshotFor(t, exactTitleCatalog())

	cases := []struct {
		query string
		want  []string
	}{
		// Issue #1838: both works titled "Spare", ahead of "Spare Us!" and friends.
		{"Spare", []string{"work:spare", "work:spare-violet-fox"}},
		{"spare", []string{"work:spare", "work:spare-violet-fox"}},
		{"  SPARE  ", []string{"work:spare", "work:spare-violet-fox"}},
		// The longer title is its own exact match, and only its own.
		{"Spare Parts", []string{"work:spare-parts", "work:spare-parts-andy-weir"}},
		{"The Martian", []string{"work:the-martian"}},
		{"the martian!", []string{"work:the-martian"}},
		// nameKey reads the same terms the MATCH is built from, so punctuation
		// standing in for a space still names the title outright.
		{"Halo:Combat Evolved", []string{"work:halo-combat-evolved"}},
		{"halo, combat evolved", []string{"work:halo-combat-evolved"}},
	}
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			for _, kind := range []searchKind{kindAny, kindWork} {
				ids := scopedIDs(t, snap, kind, tc.query)
				if len(ids) < len(tc.want) || !reflect.DeepEqual(ids[:len(tc.want)], tc.want) {
					t.Errorf("search(%s, %q) leads with %v, want %v", kind, tc.query, ids, tc.want)
				}
			}
		})
	}
}

// TestExactTitleBoostFixesTheRanking is the bug itself: without the boost the
// plain FTS page ranks a long-rowed exact title below its shorter neighbours.
// The fixture reproduces that, so the assertion above is not passing by accident
// on a page that was already right.
func TestExactTitleBoostFixesTheRanking(t *testing.T) {
	snap := snapshotFor(t, exactTitleCatalog())

	hits, err := snap.ftsHits(kindWork, ftsQuery("spare"), 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("no FTS hits for 'spare'")
	}
	if hits[0].id == "spare" {
		// Not a failure - bm25 is free to change - but the fixture has stopped
		// carrying the shape the boost exists for, and should be made heavier again.
		t.Log("note: plain FTS now leads with the exact title in this fixture; only the assertion below still has teeth")
	}
	if got := first(searchIDs(t, snap, "spare")); got != "work:spare" {
		t.Errorf("search('spare') first = %q, want work:spare (plain FTS led with %q)", got, hits[0].id)
	}
}

// TestExactTitleBoostIsDeterministic: two works share the title "Spare Parts",
// so the order they boost in may not depend on the row order the probe returned.
func TestExactTitleBoostIsDeterministic(t *testing.T) {
	snap := snapshotFor(t, exactTitleCatalog())
	want := []string{"spare-parts", "spare-parts-andy-weir"}
	for i := 0; i < 5; i++ {
		hits, err := snap.exactTitleHits("Spare Parts")
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(hits, want) {
			t.Fatalf("exactTitleHits('Spare Parts') = %v, want %v", hits, want)
		}
	}
}

// TestExactTitleMisses proves the boost stays quiet when the query is not a
// title: a prefix, a fragment, a title with extra words, and a query that names
// nothing all resolve nothing, so the page is the one FTS produced.
func TestExactTitleMisses(t *testing.T) {
	snap := snapshotFor(t, exactTitleCatalog())
	for _, q := range []string{
		"spar",              // half-typed: no prefix-star on the probe
		"parts",             // a fragment of a title is not the title
		"spare parts andy",  // more than the title
		"martian",           // ditto, the other way round
		"nobody wrote this", // nothing at all
	} {
		hits, err := snap.exactTitleHits(q)
		if err != nil {
			t.Fatalf("exactTitleHits(%q): %v", q, err)
		}
		if hits != nil {
			t.Errorf("exactTitleHits(%q) = %v, want no boost", q, hits)
		}
	}

	// And the page a miss produces is exactly the FTS page, hit for hit.
	const q = "spare room"
	hits, err := snap.ftsHits(kindAny, ftsQuery(q), 20)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := snap.results(hits)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := searchIDs(t, snap, q), resultIDs(t, plain); !reflect.DeepEqual(got, want) {
		t.Errorf("search(%q) = %v, want the unboosted FTS page %v", q, got, want)
	}
}

// TestExactTitleBoostsOnlyWorks: the ids the probe resolves are always works, so
// a people or series page is never prepended to - and the probe's own kind
// filter means a PERSON and a SERIES named "Spare" can never be boosted onto the
// combined page as works either.
func TestExactTitleBoostsOnlyWorks(t *testing.T) {
	snap := snapshotFor(t, exactTitleCatalog())

	hits, err := snap.exactTitleHits("Spare")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(hits, []string{"spare", "spare-violet-fox"}) {
		t.Errorf("exactTitleHits('Spare') = %v, want the two works only", hits)
	}

	for _, kind := range []searchKind{kindPerson, kindSeries} {
		ids := scopedIDs(t, snap, kind, "Spare")
		for _, id := range ids {
			if strings.HasPrefix(id, "work:") {
				t.Errorf("%s search for 'Spare' returned %s", kind, id)
			}
		}
		if len(ids) == 0 {
			t.Errorf("%s search for 'Spare' returned nothing; the fixture holds one", kind)
		}
	}
}

// TestExactTitleSkipsJunkQueries is the COST guard. Each of these matches a
// large share of the title index - "the" is in 88,458 of the real artifact's
// titles, and probing it measured 426ms against the plain page's 196ms - so the
// query is never probed at all. The fixture holds works actually titled "The",
// "A" and "S", so a nil answer here is the SKIP, not an absence of data.
func TestExactTitleSkipsJunkQueries(t *testing.T) {
	snap := snapshotFor(t, exactTitleCatalog())
	for _, q := range []string{"the", "The", "a", "s", "S", "the a of", "  ", ""} {
		hits, err := snap.exactTitleHits(q)
		if err != nil {
			t.Fatalf("exactTitleHits(%q): %v", q, err)
		}
		if hits != nil {
			t.Errorf("exactTitleHits(%q) = %v, want no boost", q, hits)
		}
	}
	// The gate is about junk, not about short titles: a two-rune title that is
	// not an article still resolves. ("Halo 2" is one, through the other branch.)
	if got := first(searchIDs(t, snap, "halo 2")); got != "work:halo-2" {
		t.Errorf("search('halo 2') first = %q, want work:halo-2", got)
	}
}

func TestWorthTitleProbing(t *testing.T) {
	worth := []string{"spare", "it", "us", "1984", "the martian", "halo 2", "book", "part one"}
	for _, q := range worth {
		if !worthTitleProbing(q) {
			t.Errorf("worthTitleProbing(%q) = false, want true", q)
		}
	}
	// "the-a" is the punctuation case: one five-rune whitespace token, but two
	// stopword terms in the MATCH - so the gate has to judge the terms.
	junk := []string{"", "   ", "the", "a", "an", "of", "and", "s", "t", "i", "the a of", "j r", "the-a", "!!"}
	for _, q := range junk {
		if worthTitleProbing(q) {
			t.Errorf("worthTitleProbing(%q) = true, want false", q)
		}
	}
	// The one deliberate difference from the series probe: a bare volume word
	// names no series but can be a work's whole title.
	if worthProbing("book") || !worthTitleProbing("book") {
		t.Error("the volume-word vocabulary must gate the series probe and not the title probe")
	}
}

// TestTitleMatchFiltersEveryPhrase pins the parenthesized column filter. Without
// the parens FTS5 binds the filter to the FIRST phrase only, and the rest of the
// query matches the names column - which boosted Prince Harry's "Spare" for the
// query "spare harry", a string that is nobody's title.
func TestTitleMatchFiltersEveryPhrase(t *testing.T) {
	snap := snapshotFor(t, exactTitleCatalog())

	cases := map[string]string{
		"spare harry": `title : ("spare" "harry")`,
		// Punctuation is a boundary inside the filter too, so a punctuated title
		// query is a phrase list rather than one welded adjacency claim.
		"Halo: Primordium": `title : ("Halo" "Primordium")`,
		// A query with no term at all still composes a filter FTS5 parses (it
		// matches nothing); exactTitleHits gates this case out before it is run.
		"!!": `title : ("")`,
	}
	for in, want := range cases {
		if got := titleMatch(in); got != want {
			t.Errorf("titleMatch(%q) = %q, want %q", in, got, want)
		}
	}

	cands, err := snap.titleCandidates("spare harry")
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 0 {
		t.Errorf("the title probe for 'spare harry' matched %v; the filter leaked into the names column", cands)
	}
}

// TestExactTitleLeadsTheSeriesPositionBoost pins the ORDER the two boosts fire
// in when one query resolves both. "halo 2" is a work's whole title AND the way
// a reader asks for volume 2 of Halo; the title is an equality on a stated fact
// and the position is an inference, so the title leads and the volume follows -
// both ahead of the FTS page.
func TestExactTitleLeadsTheSeriesPositionBoost(t *testing.T) {
	snap := snapshotFor(t, exactTitleCatalog())

	// Both probes really do fire for this query.
	title, err := snap.exactTitleHits("halo 2")
	if err != nil {
		t.Fatal(err)
	}
	position, err := snap.seriesPositionHits("halo 2")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(title, []string{"halo-2"}) {
		t.Fatalf("exactTitleHits('halo 2') = %v, want [halo-2]", title)
	}
	if !reflect.DeepEqual(position, []string{"halo-fall-of-reach"}) {
		t.Fatalf("seriesPositionHits('halo 2') = %v, want [halo-fall-of-reach]", position)
	}

	want := []string{"halo-2", "halo-fall-of-reach"}
	if got := snap.boostedWorks("halo 2"); !reflect.DeepEqual(got, want) {
		t.Errorf("boostedWorks('halo 2') = %v, want %v", got, want)
	}
	ids := scopedIDs(t, snap, kindWork, "halo 2")
	if len(ids) < 2 || ids[0] != "work:halo-2" || ids[1] != "work:halo-fall-of-reach" {
		t.Errorf("works/search('halo 2') = %v, want the titled work then the volume", ids)
	}
}

// TestBoostProbeErrorDegrades: a probe that fails serves the plain page rather
// than a 500, and says so in the log. Both probes run against the same handle,
// so a closed one exercises both degradation paths at once.
func TestBoostProbeErrorDegrades(t *testing.T) {
	snap := snapshotFor(t, exactTitleCatalog())
	var logged bytes.Buffer
	snap.log = log.New(&logged, "", 0)
	snap.close()

	if got := snap.boostedWorks("halo 2"); got != nil {
		t.Errorf("boostedWorks on a closed handle = %v, want nil", got)
	}
	for _, want := range []string{"exact-title probe", "series-position probe"} {
		if !strings.Contains(logged.String(), want) {
			t.Errorf("log %q does not mention the %s failure", logged.String(), want)
		}
	}
}
