package importer

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/testpack"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// The QUALIFIER reach (seriesqualified.go): a claim reaches a held series by the
// base and the facets the two names and the series' own fields state, not only by
// the exact spelling of its name on the chain. Every test pairs the reach with the
// case that must NOT reach, and the end-to-end ones validate the tree they wrote.

// hpTree is J. K. Rowling's English "Harry Potter" (volumes 1-2) and its German
// edition series, linked by translation_of, under the German series' stored name:
// "Harry Potter [German Edition]" as the catalogue holds it today, or the plain
// base the stored-name cleanup (languages Phase 6, decision 2) renames it to. The
// slug is `harry-potter-german-edition` either way.
func hpTree(t *testing.T, germanName string) string {
	t.Helper()
	files := map[string]string{
		"people/jk/j-k-rowling.json": testpack.PersonJSON(t, "j-k-rowling", "J. K. Rowling"),
		"series/ha/harry-potter.json": testpack.SeriesJSON(t, "harry-potter", "Harry Potter",
			"philosophers-stone@1", "chamber-of-secrets@2"),
		"series/ha/harry-potter-german-edition.json": testpack.WithField(t,
			testpack.SeriesJSON(t, "harry-potter-german-edition", germanName, "stein-der-weisen@1", "kammer-des-schreckens@2"),
			"translation_of", []string{"harry-potter"}),
	}
	for w, lang := range map[string]string{"philosophers-stone": "en", "chamber-of-secrets": "en",
		"stein-der-weisen": "de", "kammer-des-schreckens": "de"} {
		files["works/"+shard(w)+"/"+w+"/work.json"] = testpack.WorkJSON(t, w, w,
			testpack.WithAuthors("j-k-rowling"), testpack.WithLanguage(lang))
		files["works/"+shard(w)+"/"+w+"/recordings/r1.json"] = testpack.RecJSON(t, "r1", w, testpack.WithNarrators("bea-reader"))
	}
	return seedTombstoneTree(t, files, nil)
}

// (b) A held edition series renamed to its plain base is still found by a claim
// that spells the decoration - which is why the rename must wait for this reach -
// and the decorated tree resolves the same row to the same series through the
// chain. A claim in ANOTHER language than the decoration states still founds its
// own series, and says which series it stepped past.
func TestADecoratedClaimFindsTheRenamedEditionSeries(t *testing.T) {
	for _, held := range []string{"Harry Potter [German Edition]", "Harry Potter"} {
		dataDir := hpTree(t, held)
		sum := runLibexOver(t, dataDir,
			langRow("B0HPDE0003", "Gefangene von Askaban", "J. K. Rowling", "german", "Harry Potter [German Edition]", "3"))
		if got := seriesWorks(t, dataDir, "harry-potter-german-edition")["gefangene-von-askaban"]; got != "3" || sum.NewSeries != 0 {
			t.Errorf("held %q: German row placed at %q, NewSeries %d; want harry-potter-german-edition #3 and no new series", held, got, sum.NewSeries)
		}
		assertTreeValid(t, dataDir)

		dataDir = hpTree(t, held)
		sum = runLibexOver(t, dataDir,
			langRow("B0HPEN0003", "Prisoner of Azkaban", "J. K. Rowling", "english", "Harry Potter [German Edition]", "3"))
		if got := seriesWorks(t, dataDir, "harry-potter-german-edition-2"); !reflect.DeepEqual(got, map[string]string{"prisoner-of-azkaban": "3"}) || sum.NewSeries != 1 {
			t.Errorf("held %q: English row founded %v (NewSeries %d), want harry-potter-german-edition-2 alone", held, got, sum.NewSeries)
		}
		if !hasWarning(sum.Warnings, `harry-potter-german-edition is in another language (de); created "harry-potter-german-edition-2"`) {
			t.Errorf("held %q: the founding does not name the German series it stepped past: %v", held, sum.Warnings)
		}
		assertTreeValid(t, dataDir)
	}
}

// (c) A German row naming the PLAIN "Harry Potter" is closed to the English series
// by its language and joins the German edition series the catalogue holds, rather
// than founding `harry-potter-2` beside it - under either stored name. A row in a
// language the catalogue holds no edition of still founds, and the German series,
// which its name never spelled, is not reported as stepped past.
func TestAPlainClaimFindsItsLanguagesEditionSeries(t *testing.T) {
	for _, held := range []string{"Harry Potter [German Edition]", "Harry Potter"} {
		dataDir := hpTree(t, held)
		sum := runLibexOver(t, dataDir,
			langRow("B0HPDE0003", "Gefangene von Askaban", "J. K. Rowling", "german", "Harry Potter", "3"))
		if got := seriesWorks(t, dataDir, "harry-potter-german-edition")["gefangene-von-askaban"]; got != "3" ||
			sum.NewSeries != 0 || entryExists(t, dataDir, seriesAddr("harry-potter-2")) {
			t.Errorf("held %q: German row placed at %q, NewSeries %d; want harry-potter-german-edition #3", held, got, sum.NewSeries)
		}
		assertTreeValid(t, dataDir)

		dataDir = hpTree(t, held)
		sum = runLibexOver(t, dataDir,
			langRow("B0HPFR0003", "Le prisonnier d'Azkaban", "J. K. Rowling", "french", "Harry Potter", "3"))
		if got := seriesWorks(t, dataDir, "harry-potter-2"); len(got) != 1 || sum.NewSeries != 1 {
			t.Errorf("held %q: French row founded %v (NewSeries %d), want harry-potter-2", held, got, sum.NewSeries)
		}
		if !hasWarning(sum.Warnings, `series "Harry Potter": harry-potter is in another language (en); created "harry-potter-2"`) {
			t.Errorf("held %q: the founding names more than the English series: %v", held, sum.Warnings)
		}
		assertTreeValid(t, dataDir)
	}
}

// The rename is invisible to the ANCHOR step too: a decorated claim whose chain
// holds nothing anchors on the series its decoration reaches exactly as it
// anchored on the chain before the rename. Rowling's own volume and a volume she
// co-wrote with Ada anchor first, and Ada's rows then fit through that co-credit -
// where, judged as one cluster instead, Ada's ten rows would dominate the merged
// evidence and the cluster, Rowling's own volumes with it, would found
// harry-potter-german-edition-2 in the renamed tree alone.
func TestARenamedEditionSeriesAnchorsAsTheChainDid(t *testing.T) {
	rows := []string{
		langRow("B0HPDE0003", "Gefangene", "J. K. Rowling", "german", "Harry Potter [German Edition]", "3"),
		strings.Replace(langRow("B0CO000004", "Gemeinsam", "J. K. Rowling", "german", "Harry Potter [German Edition]", "4"),
			`"authors":[{"name":"J. K. Rowling"}]`, `"authors":[{"name":"J. K. Rowling"},{"name":"Ada Mapmaker"}]`, 1),
	}
	for i := range 10 {
		rows = append(rows, langRow(fmt.Sprintf("B0ADA000%02d", i), fmt.Sprintf("Ada Buch %d", i), "Ada Mapmaker", "german",
			"Harry Potter [German Edition]", fmt.Sprint(5+i)))
	}
	outcome := func(held string) (map[string]string, int) {
		dataDir := hpTree(t, held)
		sum := runLibexOver(t, dataDir, rows...)
		assertTreeValid(t, dataDir)
		return seriesWorks(t, dataDir, "harry-potter-german-edition"), sum.NewSeries
	}
	decorated, decoratedNew := outcome("Harry Potter [German Edition]")
	renamed, renamedNew := outcome("Harry Potter")
	if !reflect.DeepEqual(renamed, decorated) || renamedNew != decoratedNew {
		t.Errorf("renamed tree: %v (NewSeries %d); decorated tree: %v (NewSeries %d) - the rename changed the outcome",
			renamed, renamedNew, decorated, decoratedNew)
	}
}

// The reach is only ever a candidate: the author fit still decides. A German row by
// another author does not join Rowling's German series however it is reached.
func TestAReachedSeriesStillAsksTheAuthorFit(t *testing.T) {
	dataDir := hpTree(t, "Harry Potter")
	sum := runLibexOver(t, dataDir,
		langRow("B0OTHER003", "Ein anderes Buch", "Ada Mapmaker", "german", "Harry Potter", "3"))
	if got := seriesWorks(t, dataDir, "harry-potter-german-edition"); len(got) != 2 || sum.NewSeries != 1 {
		t.Errorf("harry-potter-german-edition = %v (NewSeries %d), want it untouched and the row's own series founded", got, sum.NewSeries)
	}
	assertTreeValid(t, dataDir)
}

// throneTree is Sarah J. Maas's German "Throne of Glass [German Edition]", vols 1-2.
func throneTree(t *testing.T) string {
	t.Helper()
	files := map[string]string{
		"people/sa/sarah-j-maas.json": testpack.PersonJSON(t, "sarah-j-maas", "Sarah J. Maas"),
		"series/th/throne-of-glass-german-edition.json": testpack.SeriesJSON(t, "throne-of-glass-german-edition",
			"Throne of Glass [German Edition]", "die-erwahlte@1", "kriegerin-im-schatten@2"),
	}
	for _, w := range []string{"die-erwahlte", "kriegerin-im-schatten"} {
		files["works/"+shard(w)+"/"+w+"/work.json"] = testpack.WorkJSON(t, w, w,
			testpack.WithAuthors("sarah-j-maas"), testpack.WithLanguage("de"))
		files["works/"+shard(w)+"/"+w+"/recordings/r1.json"] = testpack.RecJSON(t, "r1", w, testpack.WithNarrators("bea-reader"))
	}
	return seedTombstoneTree(t, files, nil)
}

// (a) A RESPELLED qualifier ("(Deutsche Ausgabe)" beside the held "[German
// Edition]") has another slug, so the chain never sees the held series; the reach
// joins it. An English row of the same spelling is closed by its language and
// founds its own series at its own chain slug.
func TestARespelledQualifierFindsTheHeldSeries(t *testing.T) {
	dataDir := throneTree(t)
	sum := runLibexOver(t, dataDir,
		langRow("B0TOGDE003", "Erbin des Feuers", "Sarah J. Maas", "german", "Throne of Glass (Deutsche Ausgabe)", "3"))
	if got := seriesWorks(t, dataDir, "throne-of-glass-german-edition")["erbin-des-feuers"]; got != "3" || sum.NewSeries != 0 {
		t.Errorf("respelled German row placed at %q, NewSeries %d; want throne-of-glass-german-edition #3", got, sum.NewSeries)
	}
	assertTreeValid(t, dataDir)

	dataDir = throneTree(t)
	sum = runLibexOver(t, dataDir,
		langRow("B0TOGEN003", "Heir of Fire", "Sarah J. Maas", "english", "Throne of Glass (Deutsche Ausgabe)", "3"))
	if got := seriesWorks(t, dataDir, "throne-of-glass-deutsche-ausgabe"); len(got) != 1 || sum.NewSeries != 1 {
		t.Errorf("English row founded %v (NewSeries %d), want throne-of-glass-deutsche-ausgabe", got, sum.NewSeries)
	}
	if len(seriesWorks(t, dataDir, "throne-of-glass-german-edition")) != 2 {
		t.Error("the German series took an English volume")
	}
	assertTreeValid(t, dataDir)
}

// --existing-series-only keeps its meaning - nothing is founded - and a reached
// series is a JOIN, so the plain German claim is placed rather than dropped.
func TestExistingSeriesOnlyJoinsAReachedSeries(t *testing.T) {
	dataDir := hpTree(t, "Harry Potter [German Edition]")
	sum := runLibexWith(t, dataDir, Options{ExistingSeriesOnly: true},
		langRow("B0HPDE0003", "Gefangene von Askaban", "J. K. Rowling", "german", "Harry Potter", "3"),
		langRow("B0HPFR0003", "Le prisonnier d'Azkaban", "J. K. Rowling", "french", "Harry Potter", "3"))
	if sum.NewSeries != 0 || sum.SeriesClaimsDropped != 1 ||
		seriesWorks(t, dataDir, "harry-potter-german-edition")["gefangene-von-askaban"] != "3" {
		t.Errorf("NewSeries/SeriesClaimsDropped = %d/%d, German series %v; want 0/1 with the German volume joined",
			sum.NewSeries, sum.SeriesClaimsDropped, seriesWorks(t, dataDir, "harry-potter-german-edition"))
	}
	assertTreeValid(t, dataDir)
}

// The reach keeps resolution ORDER-INDEPENDENT: German rows spelling the series
// three ways, English and French rows, in any order, land in the same series.
func TestQualifierReachIsOrderIndependent(t *testing.T) {
	rows := []string{
		langRow("B0HPDE0003", "Gefangene von Askaban", "J. K. Rowling", "german", "Harry Potter", "3"),
		langRow("B0HPDE0004", "Feuerkelch", "J. K. Rowling", "german", "Harry Potter [German Edition]", "4"),
		langRow("B0HPDE0005", "Orden des Phoenix", "J. K. Rowling", "german", "Harry Potter (Deutsche Ausgabe)", "5"),
		langRow("B0HPEN0003", "Prisoner of Azkaban", "J. K. Rowling", "english", "Harry Potter", "3"),
		langRow("B0HPFR0003", "Le prisonnier", "J. K. Rowling", "french", "Harry Potter", "3"),
		langRow("B0HPFR0004", "La coupe de feu", "J. K. Rowling", "french", "Harry Potter", "4"),
	}
	membership := func(order []int) map[string]map[string]string {
		dataDir := hpTree(t, "Harry Potter")
		var in []string
		for _, i := range order {
			in = append(in, rows[i])
		}
		runLibexOver(t, dataDir, in...)
		assertTreeValid(t, dataDir)
		out := map[string]map[string]string{}
		for _, slug := range []string{"harry-potter", "harry-potter-2", "harry-potter-3",
			"harry-potter-german-edition", "harry-potter-german-edition-2", "harry-potter-deutsche-ausgabe"} {
			if entryExists(t, dataDir, seriesAddr(slug)) {
				out[slug] = seriesWorks(t, dataDir, slug)
			}
		}
		return out
	}
	forward := membership([]int{0, 1, 2, 3, 4, 5})
	want := map[string]map[string]string{
		"harry-potter": {"philosophers-stone": "1", "chamber-of-secrets": "2", "prisoner-of-azkaban": "3"},
		"harry-potter-german-edition": {"stein-der-weisen": "1", "kammer-des-schreckens": "2",
			"gefangene-von-askaban": "3", "feuerkelch": "4", "orden-des-phoenix": "5"},
		"harry-potter-2": {"le-prisonnier": "3", "la-coupe-de-feu": "4"},
	}
	if !reflect.DeepEqual(forward, want) {
		t.Fatalf("forward order: %v, want %v", forward, want)
	}
	for name, order := range map[string][]int{"reverse": {5, 4, 3, 2, 1, 0}, "interleaved": {4, 2, 0, 5, 1, 3}} {
		if got := membership(order); !reflect.DeepEqual(got, forward) {
			t.Errorf("%s order: %v, want %v", name, got, forward)
		}
	}
}

// resolveIn resolves one claim against a hand-built catalogue, as the intake form
// does (SeriesAuthorIndex.Resolve).
func resolveIn(cat *model.Catalog, name, lang string) SeriesMatch {
	names := map[string]string{}
	for _, s := range cat.Series {
		names[s.ID] = s.Name
	}
	stored := func(slug string) (string, bool) { n, ok := names[slug]; return n, ok }
	row := SeriesRowFor([]string{"David Gemmell"}, []string{"A New Book"}, "", lang, nil)
	return NewSeriesAuthorIndex(cat).Resolve(name, nil, stored, row)
}

// gemmell is a catalogue of English works by David Gemmell, and series over them.
func gemmell(series ...*model.Series) *model.Catalog {
	cat := &model.Catalog{People: []*model.Person{{ID: "david-gemmell", Name: "David Gemmell"}}, Series: series}
	for _, w := range []string{"legend", "the-king-beyond-the-gate", "waylander"} {
		cat.Works = append(cat.Works, &model.Work{ID: w, Title: w, Language: "en", Authors: []string{"david-gemmell"}})
	}
	return cat
}

func members() []model.SeriesWork {
	return []model.SeriesWork{{Work: "legend", Position: "1"}, {Work: "the-king-beyond-the-gate", Position: "2"}}
}

// ORDERING: a VARIANT is reached only by a claim stating its ordering, in any
// spelling the reader knows; an unqualified claim reaches the primary instead, and
// an ordering no series of the base states reaches nothing.
func TestOrderingReachesAVariantOnlyByItsOrdering(t *testing.T) {
	cat := gemmell(
		&model.Series{ID: "drenai-publication-order", Name: "Drenai [publication order]", Ordering: model.OrderingPublication, Works: members()},
		&model.Series{ID: "drenai-chronological-order", Name: "Drenai [chronological order]", Ordering: model.OrderingChronological,
			OrderingOf: "drenai-publication-order", Works: members()},
	)
	for name, want := range map[string]string{
		"Drenai (Chronological)":                     "drenai-chronological-order",
		"Drenai (in chronologischer Reihenfolge)":    "drenai-chronological-order",
		"Drenai (Published Order)":                   "drenai-publication-order",
		"Drenai":                                     "drenai-publication-order",
		"Drenai (Recommended Listening Order)":       "",
		"Drenai [Chronological Order] [Light Novel]": "",
	} {
		m := resolveIn(cat, name, "en")
		if want == "" {
			if m.Found {
				t.Errorf("%q reached %s, want nothing", name, m.Slug)
			}
			continue
		}
		if !m.Found || m.Slug != want {
			t.Errorf("%q resolved to %+v, want %s", name, m, want)
		}
	}
	// A variant whose primary lives under another base is still unreachable to an
	// unqualified claim of the variant's own base.
	cat = gemmell(
		&model.Series{ID: "drenai-saga", Name: "Drenai Saga", Ordering: model.OrderingPublication, Works: members()},
		&model.Series{ID: "drenai-chronological-order", Name: "Drenai (Chronological Order)", Ordering: model.OrderingChronological,
			OrderingOf: "drenai-saga", Works: members()},
	)
	if m := resolveIn(cat, "Drenai", "en"); m.Found {
		t.Errorf("an unqualified claim reached the variant %s", m.Slug)
	}
}

// A non-variant is reached by a claim stating its OWN ordering - "MaddAddam
// (Published Order)" joins the plain primary that absorbed it - but not by an
// ordering it does not state.
func TestOrderingReachesAPrimaryByItsOwnOrdering(t *testing.T) {
	stated := gemmell(&model.Series{ID: "drenai", Name: "Drenai", Ordering: model.OrderingPublication, Works: members()})
	if m := resolveIn(stated, "Drenai (Published Order)", "en"); !m.Found || m.Slug != "drenai" {
		t.Errorf("a claim stating the primary's ordering resolved to %+v, want drenai", m)
	}
	unstated := gemmell(&model.Series{ID: "drenai", Name: "Drenai", Works: members()})
	if m := resolveIn(unstated, "Drenai (Published Order)", "en"); m.Found {
		t.Errorf("a claim reached %s, which states no ordering", m.Slug)
	}
	// A non-variant in a chronological or recommended order - by its field or by
	// its name alone - is a reading order: an unqualified claim's position is not
	// one of its slots, so only a claim stating that order reaches it. (The ids sit
	// off every claim's chain, so the index alone decides.)
	for claim, s := range map[string]*model.Series{
		"Drenai (Chronological Order)": {ID: "drenai-ro", Name: "Drenai (chronological)", Ordering: model.OrderingChronological, Works: members()},
		"Drenai [Chronological]":       {ID: "drenai-ro", Name: "Drenai (chronological)", Works: members()},
		"Drenai (Reading Order)":       {ID: "drenai-ro", Name: "Drenai (Recommended Listening Order)", Works: members()},
	} {
		cat := gemmell(s)
		if m := resolveIn(cat, "Drenai", "en"); m.Found {
			t.Errorf("an unqualified claim reached the reading order %q", s.Name)
		}
		if m := resolveIn(cat, claim, "en"); !m.Found || m.Slug != s.ID {
			t.Errorf("%q, stating %q's own order, resolved to %+v", claim, s.Name, m)
		}
	}
}

// A TIED series has no language facet: it is reached by its name alone, as before.
func TestATiedSeriesIsNotIndexed(t *testing.T) {
	cat := gemmell(&model.Series{ID: "drenai-saga-2", Name: "Drenai Saga", Works: members()})
	cat.Works[1].Language = "de" // one en, one de: a tie
	ix := NewSeriesAuthorIndex(cat)
	if got := ix.qualifiedIndex(); len(got) != 0 {
		t.Errorf("a tied series is indexed: %v", got)
	}
	if m := resolveIn(cat, "Drenai Saga", "en"); m.Found {
		t.Errorf("a claim reached the tied series off its chain: %+v", m)
	}
}

// libex-select reaches the same series the import will: a row whose name's chain
// holds nothing the catalogue does is still a completion when the qualifier index
// reaches a held series for it (the pre-filter, namesACatalogueChain, asks the
// index too), and a row it reaches nothing for is still out.
func TestLibexSelectReachesAQualifiedSeries(t *testing.T) {
	dataDir := hpTree(t, "Harry Potter")
	row := func(asin, title, lang, series string) string {
		return strings.Replace(langRow(asin, title, "J. K. Rowling", lang, series, "3"), `"region":"us"`, `"region":"de"`, 1)
	}
	res, lines := runSelect(t, dataDir, []string{
		row("B0HPDE0003", "Gefangene von Askaban", "german", "Harry Potter (Deutsche Ausgabe)"),
		row("B0HPFR0003", "Le prisonnier", "french", "Harry Potter (Edition Francaise)"),
	}, 0)
	if res.RowsSelected != 1 || len(lines) != 1 || !strings.Contains(lines[0], "B0HPDE0003") {
		t.Errorf("selected %d rows %v, want the German row alone", res.RowsSelected, lines)
	}
	if res.Excluded[reasonNoSeries.report] != 1 {
		t.Errorf("excluded = %v, want the French row as no catalogue series", res.Excluded)
	}
}

// The index is built lazily: an index nothing asks never builds it, and a lookup
// builds it once.
func TestQualifiedIndexIsBuiltOnFirstUse(t *testing.T) {
	ix := NewSeriesAuthorIndex(gemmell(&model.Series{ID: "drenai", Name: "Drenai", Works: members()}))
	if ix.qualified != nil || ix.qualifiedFrom == nil {
		t.Fatal("the qualifier index was built before anything asked for it")
	}
	if !ix.holdsQualifiedBase("Drenai (Published Order)", "drenai-published-order") || ix.qualifiedFrom != nil {
		t.Error("a lookup did not build the index from the catalogue's series")
	}
	if ix.holdsQualifiedBase("Waylander", "waylander") {
		t.Error("a base no series holds was reported held")
	}
}

// sagaTree is Ada Mapmaker's one-volume German "Saga [German Edition]": open to
// any one row, as a one-member series is.
func sagaTree(t *testing.T) string {
	t.Helper()
	return seedTombstoneTree(t, map[string]string{
		"works/ei/eins/work.json": testpack.WorkJSON(t, "eins", "Eins",
			testpack.WithAuthors("ada-mapmaker"), testpack.WithLanguage("de")),
		"works/ei/eins/recordings/r1.json":   testpack.RecJSON(t, "r1", "eins", testpack.WithNarrators("bea-reader")),
		"series/sa/saga-german-edition.json": testpack.SeriesJSON(t, "saga-german-edition", "Saga [German Edition]", "eins@1"),
	}, nil)
}

// sagaRow is a German row by author naming series at pos.
func sagaRow(asin, title, author, series, pos string) string {
	return langRow(asin, title, author, "german", series, pos)
}

// One author's rows SPLIT ACROSS SPELLINGS of a series are judged against ONE
// evidence for the series every spelling reaches, exactly as one spelling would
// be: three rows of another author do not take over a one-volume series, whether
// they spell it one way or three, and each spelling founds on its own chain.
func TestSpellingsDoNotSplitATakeover(t *testing.T) {
	for name, series := range map[string][3]string{
		"one spelling":    {"Saga [German Edition]", "Saga [German Edition]", "Saga [German Edition]"},
		"three spellings": {"Saga [German Edition]", "Saga (Deutsche Ausgabe)", "Saga"},
	} {
		dataDir := sagaTree(t)
		sum := runLibexOver(t, dataDir,
			sagaRow("B0CARL0002", "Zwei", "Carl Squatter", series[0], "2"),
			sagaRow("B0CARL0003", "Drei", "Carl Squatter", series[1], "3"),
			sagaRow("B0CARL0004", "Vier", "Carl Squatter", series[2], "4"))
		if got := seriesWorks(t, dataDir, "saga-german-edition"); !reflect.DeepEqual(got, map[string]string{"eins": "1"}) {
			t.Errorf("%s: saga-german-edition = %v, want Ada's volume alone", name, got)
		}
		if sum.NewSeries == 0 {
			t.Errorf("%s: no series founded for the refused rows", name)
		}
		assertTreeValid(t, dataDir)
	}
	// The violating side: one row of another author is still admitted to the
	// open one-volume series, under any spelling.
	for _, series := range []string{"Saga [German Edition]", "Saga (Deutsche Ausgabe)", "Saga"} {
		dataDir := sagaTree(t)
		runLibexOver(t, dataDir, sagaRow("B0CARL0002", "Zwei", "Carl Squatter", series, "2"))
		if seriesWorks(t, dataDir, "saga-german-edition")["zwei"] != "2" {
			t.Errorf("one row naming %q was not admitted to the open series", series)
		}
	}
}

// The unit resolution is independent of ROW order: the owner's own volumes, a
// would-be squatter's and an English row, spread over the spellings, land in the
// same series whatever order they arrive in.
func TestSpellingUnitsAreOrderIndependent(t *testing.T) {
	rows := []string{
		sagaRow("B0ADA00002", "Zwei", "Ada Mapmaker", "Saga (Deutsche Ausgabe)", "2"),
		sagaRow("B0ADA00003", "Drei", "Ada Mapmaker", "Saga", "3"),
		sagaRow("B0CARL0004", "Vier", "Carl Squatter", "Saga [German Edition]", "4"),
		sagaRow("B0CARL0005", "Fuenf", "Carl Squatter", "Saga (Deutsche Ausgabe)", "5"),
		sagaRow("B0CARL0006", "Sechs", "Carl Squatter", "Saga", "6"),
		langRow("B0ADAEN001", "One", "Ada Mapmaker", "english", "Saga", "1"),
	}
	membership := func(order []int) map[string]map[string]string {
		dataDir := sagaTree(t)
		var in []string
		for _, i := range order {
			in = append(in, rows[i])
		}
		runLibexOver(t, dataDir, in...)
		assertTreeValid(t, dataDir)
		out := map[string]map[string]string{}
		for _, slug := range []string{"saga-german-edition", "saga-german-edition-2", "saga-deutsche-ausgabe",
			"saga-deutsche-ausgabe-2", "saga", "saga-2", "saga-3"} {
			if entryExists(t, dataDir, seriesAddr(slug)) {
				out[slug] = seriesWorks(t, dataDir, slug)
			}
		}
		return out
	}
	forward := membership([]int{0, 1, 2, 3, 4, 5})
	if got := forward["saga-german-edition"]; !reflect.DeepEqual(got, map[string]string{"eins": "1", "zwei": "2", "drei": "3"}) {
		t.Errorf("saga-german-edition = %v, want Ada's three German volumes alone", got)
	}
	for name, order := range map[string][]int{"reverse": {5, 4, 3, 2, 1, 0}, "interleaved": {3, 0, 5, 2, 4, 1}, "squatter first": {2, 3, 4, 0, 1, 5}} {
		if got := membership(order); !reflect.DeepEqual(got, forward) {
			t.Errorf("%s order: %v, want %v", name, got, forward)
		}
	}
}

// A join the name alone does not show is said once in the run's Notes - a note,
// not a warning - and a join of a series stored under the very name the claim
// gave says nothing.
func TestAJoinUnderAnotherStoredNameIsNoted(t *testing.T) {
	dataDir := throneTree(t)
	sum := runLibexOver(t, dataDir,
		langRow("B0TOGDE003", "Erbin des Feuers", "Sarah J. Maas", "german", "Throne of Glass (Deutsche Ausgabe)", "3"),
		langRow("B0TOGDE004", "Koenigin der Schatten", "Sarah J. Maas", "german", "Throne of Glass [German Edition]", "4"))
	want := `1 series claim(s) joined a catalogued series stored under another name: ` +
		`"Throne of Glass (Deutsche Ausgabe)" joined throne-of-glass-german-edition "Throne of Glass [German Edition]"`
	if !slices.Contains(sum.Notes, want) {
		t.Errorf("Notes = %v, want %q", sum.Notes, want)
	}
	if hasWarning(sum.Warnings, "joined") {
		t.Errorf("the join was warned about: %v", sum.Warnings)
	}
	assertTreeValid(t, dataDir)
}
