package repair

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/audit"
	"github.com/kodestar/audiosilo-meta/internal/testpack"
	"github.com/kodestar/audiosilo-meta/pkg/check"
	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// links_test.go pins the translation_of / ordering_of half of a merge (links.go): no
// link is left naming a retired slug, every value the merge could not keep is named,
// and the shapes no mechanical rule can settle are refused under their own category.

// setField sets a member on an already-rendered fixture record.
func setField(t testing.TB, files map[string]string, address, field string, v any) {
	t.Helper()
	body, ok := files[address]
	if !ok {
		t.Fatalf("fixture has no %s", address)
	}
	files[address] = testpack.WithField(t, body, field, v)
}

// translationCluster is the calibration cluster plus translations of its halves: a
// French edition naming the LOSER, and a German one naming BOTH halves (which the
// re-point collapses onto one survivor).
func translationCluster(t testing.TB) map[string]string {
	t.Helper()
	files := hammeredCluster(t)
	addWork(t, files, "marteau", "fr", withTranslationOf("hammered-book-3"))
	addWork(t, files, "gehammert", "de", withTranslationOf("hammered", "hammered-book-3"))
	return files
}

// A merge-works re-points every translation naming a loser onto the survivor - on
// records that are not part of the merge at all - dedupes the set that then names the
// survivor twice, and leaves the tree green. The dry run plans exactly what the write
// applies.
func TestMergeWorksRepointsTranslationsOfTheLoser(t *testing.T) {
	data := seedTree(t, translationCluster(t))
	dry := run(t, Options{DataDir: data, Ops: []string{audit.OpMergeWorks}})
	rep := run(t, Options{DataDir: data, Ops: []string{audit.OpMergeWorks}, Write: true})
	if len(rep.Applied) != 1 || len(rep.Refused) != 0 {
		t.Fatalf("applied %+v, refused %+v", rep.Applied, rep.Refused)
	}
	if !reflect.DeepEqual(dry.Applied, rep.Applied) {
		t.Errorf("the dry run planned something else than the write applied:\ndry:   %+v\nwrite: %+v", dry.Applied, rep.Applied)
	}
	for slug, want := range map[string][]string{"marteau": {"hammered"}, "gehammert": {"hammered"}} {
		if got := workEntry(t, data, slug).Strs("translation_of"); !slices.Equal(got, want) {
			t.Errorf("%s translation_of = %v, want %v", slug, got, want)
		}
	}
	if workEntry(t, data, "hammered").Has("translation_of") {
		t.Error("the survivor gained a translation_of it never stated")
	}
	for _, want := range []string{"re-pointed translation_of of work marteau onto hammered",
		"re-pointed translation_of of work gehammert onto hammered"} {
		if !noteMentions(rep.Applied[0].Notes, want) {
			t.Errorf("notes do not say %q: %v", want, rep.Applied[0].Notes)
		}
	}
	if len(rep.PostProblems) != 0 {
		t.Errorf("post-write problems: %v", rep.PostProblems)
	}
}

// The loser's own translation_of is a set-valued fact, so it is UNIONED onto the
// survivor rather than chosen away, and the union is in ascending order.
func TestMergeWorksUnionsTheLosersTranslationOf(t *testing.T) {
	files := hammeredCluster(t)
	files["works/ha/hammered/work.json"] = workJSON(t, "hammered", "Hammered",
		withGenres("fantasy"), withTranslationOf("zertrummert"))
	files["works/ha/hammered-book-3/work.json"] = workJSON(t, "hammered-book-3", "Hammered: The Druid Tales, Book 3",
		withGenres("action-adventure"), withSubtitle("An Iron Druid Adventure"), withTranslationOf("gehammert"))
	for _, slug := range []string{"gehammert", "zertrummert"} {
		addWork(t, files, slug, "de")
	}
	data := seedTree(t, files)
	rep := run(t, Options{DataDir: data, Ops: []string{audit.OpMergeWorks}, Write: true})
	if len(rep.Applied) != 1 || len(rep.Refused) != 0 {
		t.Fatalf("applied %+v, refused %+v", rep.Applied, rep.Refused)
	}
	if got, want := workEntry(t, data, "hammered").Strs("translation_of"), []string{"gehammert", "zertrummert"}; !slices.Equal(got, want) {
		t.Errorf("survivor translation_of = %v, want the sorted union %v", got, want)
	}
	if noteMentions(rep.Applied[0].Notes, "translation_of:") {
		t.Errorf("a union dropped nothing, yet the notes report a loss: %v", rep.Applied[0].Notes)
	}
}

// A link that the merge turns into a link to ITSELF is dropped and named. From a
// valid tree it takes a CROSS-LANGUAGE pair: the loser, a German record, says it
// translates the English survivor - the two are merged directly here (the audit's
// language veto keeps such a cluster out of a real run), and the link that would
// then name the survivor itself goes.
func TestMergeWorksDropsATranslationThatWouldNameItself(t *testing.T) {
	files := hammeredCluster(t)
	setField(t, files, "works/ha/hammered-book-3/work.json", "language", "de")
	setField(t, files, "works/ha/hammered-book-3/work.json", "translation_of", []string{"hammered"})
	rn, tx := planFixture(t, seedTree(t, files))
	if err := rn.mergeWorks(tx, mergeFinding("hammered", "hammered-book-3")); err != nil {
		t.Fatal(err)
	}
	if tx.works.puts["hammered"].Has("translation_of") {
		t.Errorf("the self-link survived: %v", tx.works.puts["hammered"].Strs("translation_of"))
	}
	if want := `translation_of: kept "", dropped "hammered" from hammered-book-3`; !noteMentions(tx.notes, want) {
		t.Errorf("notes do not name the dropped self-link %q: %v", want, tx.notes)
	}
}

// addWork seeds a work in a stated language with one recording, for the link
// fixtures that need partners in other languages.
func addWork(t testing.TB, files map[string]string, slug, lang string, opts ...workOpt) {
	t.Helper()
	files["works/"+slug[:2]+"/"+slug+"/work.json"] = workJSON(t, slug, slug, append([]workOpt{withLanguage(lang)}, opts...)...)
	files["works/"+slug[:2]+"/"+slug+"/recordings/luke-daniels-2020.json"] = recJSON(t, "luke-daniels-2020", slug,
		withNarrators("luke-daniels"))
}

// A translation re-point the merge cannot settle: the survivor is a translation
// (of the German zorn) and the loser is an original (the French marteau translates
// it), so folding the loser in would make the survivor both - a two-hop chain the
// merge itself creates, from a tree that is valid before it.
func TestMergeWorksRefusesATranslationChain(t *testing.T) {
	files := hammeredCluster(t)
	addWork(t, files, "zorn", "de")
	addWork(t, files, "marteau", "fr", withTranslationOf("hammered-book-3"))
	setField(t, files, "works/ha/hammered/work.json", "translation_of", []string{"zorn"})
	rn, tx := planFixture(t, seedTree(t, files))
	err := rn.mergeWorks(tx, mergeFinding("hammered", "hammered-book-3"))
	assertRefusal(t, err, CatTranslationLink, "would state translation_of [zorn] while marteau names it as its original")
}

// The planner's OTHER chain arm - the survivor would name an original that is itself
// a translation - cannot arise from a VALID tree: every original the merged set names
// was already some cluster member's original, and pkg/check holds an original to
// carrying no translation_of of its own (and an earlier proposal that made one a
// carrier would itself have been refused by the arm above). The arm stays because a
// DRY run carries on over a tree that does not validate, and there it is what names
// the chain; a --write refuses such a tree outright, before planning anything.
func TestMergeWorksOverATranslationChainRefusesToWrite(t *testing.T) {
	files := hammeredCluster(t)
	addWork(t, files, "zorn", "de", withTranslationOf("rage"))
	addWork(t, files, "rage", "fr")
	setField(t, files, "works/ha/hammered-book-3/work.json", "translation_of", []string{"zorn"})
	data := seedTreeAllowingProblems(t, files)
	if res := check.Load(data); len(res.Problems) == 0 {
		t.Fatal("the chained fixture validates: this test needs pkg/check's chain rule to refuse it")
	}
	before := treeBytes(t, data)
	if _, err := Run(Options{DataDir: data, Write: true}); err == nil || !strings.Contains(err.Error(), "metacheck") {
		t.Fatalf("a --write over a chained tree: err = %v, want the refusal naming metacheck", err)
	}
	if !equalTrees(before, treeBytes(t, data)) {
		t.Error("a refused --write changed the tree")
	}
	rn, tx := planFixture(t, data)
	err := rn.mergeWorks(tx, mergeFinding("hammered", "hammered-book-3"))
	assertRefusal(t, err, CatTranslationLink, "zorn is itself a translation of [rage]")
}

// The plan's link index is kept current as proposals commit: a later merge retiring
// the survivor of an earlier one re-points the link the earlier one moved.
func TestALaterMergeSeesTheLinksAnEarlierOneRepointed(t *testing.T) {
	files := translationCluster(t)
	files["works/ha/hammered-2/work.json"] = workJSON(t, "hammered-2", "Hammered")
	files["works/ha/hammered-2/recordings/luke-daniels-2019.json"] = recJSON(t, "luke-daniels-2019", "hammered-2",
		withNarrators("luke-daniels"))
	rn, tx := planFixture(t, seedTree(t, files))
	if err := rn.mergeWorks(tx, mergeFinding("hammered", "hammered-book-3")); err != nil {
		t.Fatal(err)
	}
	if err := tx.commit("first"); err != nil {
		t.Fatal(err)
	}
	if got := rn.plan.workLinks.translatedBy["hammered"]; !got["marteau"] || !got["gehammert"] {
		t.Fatalf("the index does not see the re-pointed links: %v", got)
	}
	tx = rn.plan.begin()
	if err := rn.mergeWorks(tx, mergeFinding("hammered-2", "hammered")); err != nil {
		t.Fatal(err)
	}
	if got := tx.works.puts["marteau"].Strs("translation_of"); !slices.Equal(got, []string{"hammered-2"}) {
		t.Errorf("marteau translation_of = %v, want it re-pointed again onto hammered-2", got)
	}
}

// orderingFamily is seriesPair plus a THIRD series, a chronological variant of the
// loser: the MaddAddam shape a merge has to keep pointing somewhere live.
func orderingFamily(t testing.TB) map[string]string {
	t.Helper()
	files := seriesPair(t, "hounded@1", "hexed@2")
	setField(t, files, "series/ir/iron-druid-chronicles.json", "ordering", model.OrderingPublication)
	setField(t, files, "series/ir/iron-druid-chronicles-2.json", "ordering", model.OrderingPublication)
	files["series/dr/druid-reading-order.json"] = testpack.WithFields(t,
		seriesJSON(t, "druid-reading-order", "Druid Reading Order", "hexed@1", "hounded@2"),
		map[string]any{"ordering": model.OrderingChronological, "ordering_of": "iron-druid-chronicles-2"})
	return files
}

const druidTarget, druidLoser = "iron-druid-chronicles", "iron-druid-chronicles-2"

// (b) a variant OUTSIDE the cluster naming the loser is re-pointed onto the survivor,
// end to end, and the dry run plans what the write applies.
func TestMergeSeriesRepointsAVariantOfTheLoser(t *testing.T) {
	data := seedTree(t, orderingFamily(t))
	dry := run(t, Options{DataDir: data, Ops: []string{audit.OpMergeSeries}})
	rep := run(t, Options{DataDir: data, Ops: []string{audit.OpMergeSeries}, Write: true})
	if len(rep.Applied) != 1 || len(rep.Refused) != 0 {
		t.Fatalf("applied %+v, refused %+v", rep.Applied, rep.Refused)
	}
	if rep.Applied[0].Target != druidTarget {
		t.Fatalf("target = %s, want %s", rep.Applied[0].Target, druidTarget)
	}
	if !reflect.DeepEqual(dry.Applied, rep.Applied) {
		t.Errorf("the dry run planned something else than the write applied:\ndry:   %+v\nwrite: %+v", dry.Applied, rep.Applied)
	}
	if got := readEntry(t, data, pack.FamilySeries, "druid-reading-order").Str("ordering_of"); got != druidTarget {
		t.Errorf("variant ordering_of = %q, want it re-pointed onto %s", got, druidTarget)
	}
	want := "re-pointed ordering_of of series druid-reading-order from " + druidLoser + " onto " + druidTarget
	if !noteMentions(rep.Applied[0].Notes, want) {
		t.Errorf("notes do not say %q: %v", want, rep.Applied[0].Notes)
	}
	if res := check.Load(data); len(res.Problems) > 0 {
		t.Errorf("the tree does not validate after the merge: %v", res.Problems)
	}
}

// (b) refused: the survivor is itself a variant, so re-pointing a variant onto it
// would make a two-hop chain - including the survivor being a variant of the LOSER,
// where the fold would promote the variant to primary.
func TestMergeSeriesRefusesAVariantChain(t *testing.T) {
	t.Run("survivor is a variant of another primary", func(t *testing.T) {
		files := orderingFamily(t)
		files["series/pr/primary-druid.json"] = testpack.WithField(t,
			seriesJSON(t, "primary-druid", "Primary Druid", "hounded@1"), "ordering", model.OrderingRecommended)
		setField(t, files, "series/ir/"+druidTarget+".json", "ordering_of", "primary-druid")
		rn, tx := planFixture(t, seedTree(t, files))
		err := rn.mergeSeries(tx, seriesFinding(druidTarget, druidLoser))
		assertRefusal(t, err, CatOrderingLink, "would make a chain")
	})
	t.Run("survivor is a variant of the loser", func(t *testing.T) {
		files := seriesPair(t, "hounded@1", "hexed@2")
		setField(t, files, "series/ir/"+druidLoser+".json", "ordering", model.OrderingPublication)
		setField(t, files, "series/ir/"+druidTarget+".json", "ordering", model.OrderingChronological)
		setField(t, files, "series/ir/"+druidTarget+".json", "ordering_of", druidLoser)
		rn, tx := planFixture(t, seedTree(t, files))
		err := rn.mergeSeries(tx, seriesFinding(druidTarget, druidLoser))
		assertRefusal(t, err, CatOrderingLink, "would promote the variant")
	})
}

// (c) a variant folded into its own primary: its link goes with it, and the notes
// name it - and its differing `ordering` - as chosen away.
func TestMergeSeriesDropsTheLinkOfAVariantFoldedIntoItsPrimary(t *testing.T) {
	files := seriesPair(t, "hounded@1", "hexed@2")
	setField(t, files, "series/ir/"+druidTarget+".json", "ordering", model.OrderingPublication)
	setField(t, files, "series/ir/"+druidLoser+".json", "ordering", model.OrderingChronological)
	setField(t, files, "series/ir/"+druidLoser+".json", "ordering_of", druidTarget)
	rn, tx := planFixture(t, seedTree(t, files))
	if err := rn.mergeSeries(tx, seriesFinding(druidTarget, druidLoser)); err != nil {
		t.Fatal(err)
	}
	merged := tx.series.puts[druidTarget]
	if merged.Has("ordering_of") || merged.Str("ordering") != model.OrderingPublication {
		t.Errorf("survivor ordering = %q, ordering_of = %q; want its own publication and no link",
			merged.Str("ordering"), merged.Str("ordering_of"))
	}
	for _, want := range []string{
		`ordering_of: kept "", dropped "` + druidTarget + `" from ` + druidLoser,
		`ordering: kept "publication", dropped "chronological" from ` + druidLoser,
	} {
		if !noteMentions(tx.notes, want) {
			t.Errorf("notes do not report %q: %v", want, tx.notes)
		}
	}
}

// (d) two variants of one primary: the survivor keeps its link, which is also the
// loser's, so nothing is chosen away but the differing ordering.
func TestMergeSeriesKeepsTheSharedPrimaryOfTwoVariants(t *testing.T) {
	files := seriesPair(t, "hounded@1", "hexed@2")
	files["series/pr/primary-druid.json"] = testpack.WithField(t,
		seriesJSON(t, "primary-druid", "Primary Druid", "hounded@1", "hexed@2"), "ordering", model.OrderingPublication)
	for slug, o := range map[string]string{druidTarget: model.OrderingChronological, druidLoser: model.OrderingRecommended} {
		setField(t, files, "series/ir/"+slug+".json", "ordering", o)
		setField(t, files, "series/ir/"+slug+".json", "ordering_of", "primary-druid")
	}
	rn, tx := planFixture(t, seedTree(t, files))
	if err := rn.mergeSeries(tx, seriesFinding(druidTarget, druidLoser)); err != nil {
		t.Fatal(err)
	}
	if got := tx.series.puts[druidTarget].Str("ordering_of"); got != "primary-druid" {
		t.Errorf("survivor ordering_of = %q, want primary-druid kept", got)
	}
	if noteMentions(tx.notes, "ordering_of:") {
		t.Errorf("a shared primary is not a loss: %v", tx.notes)
	}
	if !noteMentions(tx.notes, `ordering: kept "chronological", dropped "recommended" from `+druidLoser) {
		t.Errorf("the differing ordering is not noted: %v", tx.notes)
	}
}

// (e) the loser is a variant of a primary the survivor does not share: the fold would
// move it out of its family (the survivor a primary, or a variant of another).
func TestMergeSeriesRefusesMovingAVariantOutOfItsFamily(t *testing.T) {
	primary := func(t testing.TB, files map[string]string, slug string) {
		files["series/pr/"+slug+".json"] = testpack.WithField(t,
			seriesJSON(t, slug, slug, "hounded@1"), "ordering", model.OrderingPublication)
	}
	t.Run("survivor is a primary", func(t *testing.T) {
		files := seriesPair(t, "hounded@1", "hexed@2")
		primary(t, files, "primary-druid")
		setField(t, files, "series/ir/"+druidLoser+".json", "ordering", model.OrderingChronological)
		setField(t, files, "series/ir/"+druidLoser+".json", "ordering_of", "primary-druid")
		rn, tx := planFixture(t, seedTree(t, files))
		err := rn.mergeSeries(tx, seriesFinding(druidTarget, druidLoser))
		assertRefusal(t, err, CatOrderingLink, "is a variant ordering of primary-druid but "+druidTarget+" is not")
	})
	t.Run("survivor is a variant of another primary", func(t *testing.T) {
		files := seriesPair(t, "hounded@1", "hexed@2")
		primary(t, files, "primary-druid")
		primary(t, files, "primary-other")
		setField(t, files, "series/ir/"+druidLoser+".json", "ordering", model.OrderingChronological)
		setField(t, files, "series/ir/"+druidLoser+".json", "ordering_of", "primary-druid")
		setField(t, files, "series/ir/"+druidTarget+".json", "ordering", model.OrderingChronological)
		setField(t, files, "series/ir/"+druidTarget+".json", "ordering_of", "primary-other")
		rn, tx := planFixture(t, seedTree(t, files))
		err := rn.mergeSeries(tx, seriesFinding(druidTarget, druidLoser))
		assertRefusal(t, err, CatOrderingLink, "would move a variant out of its ordering family")
	})
}

// Folding two primaries that each head a chronological variant would leave one family
// holding two series in one order - a duplicate to fold by hand, not a view.
func TestMergeSeriesRefusesTwoVariantsOfOneOrderingInAFamily(t *testing.T) {
	files := orderingFamily(t)
	files["series/dr/druid-timeline.json"] = testpack.WithFields(t,
		seriesJSON(t, "druid-timeline", "Druid Timeline", "hounded@1"),
		map[string]any{"ordering": model.OrderingChronological, "ordering_of": druidTarget})
	rn, tx := planFixture(t, seedTree(t, files))
	err := rn.mergeSeries(tx, seriesFinding(druidTarget, druidLoser))
	assertRefusal(t, err, CatOrderingLink, "would both state the chronological ordering")
}

// Series translation_of follows the works' rules: a series translating the loser is
// re-pointed, the loser's own set is unioned onto the survivor, and a fold that would
// make the survivor both a translation and an original is refused. Every partner is
// in another language than the English pair (a series' language is its members'), so
// each tree is valid before the merge and the merge is what re-points or chains.
func TestMergeSeriesRepointsAndUnionsTranslations(t *testing.T) {
	// chroniques-du-druide, a French series, translates the LOSER.
	chroniques := func(t testing.TB, files map[string]string) {
		addWork(t, files, "limier", "fr")
		files["series/ch/chroniques-du-druide.json"] = testpack.WithField(t,
			seriesJSON(t, "chroniques-du-druide", "Chroniques du druide", "limier@1"), "translation_of", []string{druidLoser})
	}
	// chroniken-des-druiden is a German original, translated by the series named.
	chroniken := func(t testing.TB, files map[string]string, translator string) {
		addWork(t, files, "gehetzt", "de")
		files["series/ch/chroniken-des-druiden.json"] = seriesJSON(t, "chroniken-des-druiden", "Chroniken des Druiden", "gehetzt@1")
		setField(t, files, "series/ir/"+translator+".json", "translation_of", []string{"chroniken-des-druiden"})
	}
	t.Run("a translation of the loser is re-pointed", func(t *testing.T) {
		files := seriesPair(t, "hounded@1", "hexed@2")
		chroniques(t, files)
		rn, tx := planFixture(t, seedTree(t, files))
		if err := rn.mergeSeries(tx, seriesFinding(druidTarget, druidLoser)); err != nil {
			t.Fatal(err)
		}
		if got := tx.series.puts["chroniques-du-druide"].Strs("translation_of"); !slices.Equal(got, []string{druidTarget}) {
			t.Errorf("chroniques translation_of = %v, want it re-pointed onto %s", got, druidTarget)
		}
	})
	t.Run("the loser's own set is unioned", func(t *testing.T) {
		files := seriesPair(t, "hounded@1", "hexed@2")
		chroniken(t, files, druidLoser)
		rn, tx := planFixture(t, seedTree(t, files))
		if err := rn.mergeSeries(tx, seriesFinding(druidTarget, druidLoser)); err != nil {
			t.Fatal(err)
		}
		if got := tx.series.puts[druidTarget].Strs("translation_of"); !slices.Equal(got, []string{"chroniken-des-druiden"}) {
			t.Errorf("survivor translation_of = %v, want the loser's", got)
		}
	})
	t.Run("a survivor translation folding in an original is a chain", func(t *testing.T) {
		files := seriesPair(t, "hounded@1", "hexed@2")
		chroniques(t, files)
		chroniken(t, files, druidTarget)
		rn, tx := planFixture(t, seedTree(t, files))
		err := rn.mergeSeries(tx, seriesFinding(druidTarget, druidLoser))
		assertRefusal(t, err, CatTranslationLink, "chroniques-du-druide names it as its original")
	})
}

// The two new categories are part of the published triage list.
func TestLinkConflictCategoriesArePublished(t *testing.T) {
	for _, c := range []Category{CatTranslationLink, CatOrderingLink} {
		if !slices.Contains(Categories(), c) {
			t.Errorf("Categories() lacks %q", c)
		}
	}
}

// A tree carrying no link at all - every tree today - plans a merge byte for byte as
// it did before links.go existed: nothing is re-set on the survivor, nothing extra is
// staged, and no note mentions a link.
func TestAMergeOverATreeWithoutLinksIsUnchanged(t *testing.T) {
	data := seedTree(t, hammeredCluster(t))
	one := filepath.Join(t.TempDir(), "out")
	rep := run(t, Options{DataDir: data, OutDir: one})
	for _, a := range rep.Applied {
		for _, n := range a.Notes {
			if strings.Contains(n, "translation_of") || strings.Contains(n, "ordering") {
				t.Errorf("a link note on a tree without links: %q", n)
			}
		}
	}
	if _, err := os.Stat(filepath.Join(one, appliedFile)); err != nil {
		t.Fatal(err)
	}
}

// languageFold is a merge-series whose survivor is a translation: the Iron Druid
// Chronicles (two English members, one German) name the German original
// druiden-chroniken. The loser holds only German members, so the fold decides the
// survivor's DERIVED language - which is what the translation rule judges.
func languageFold(t testing.TB, loserMembers ...string) map[string]string {
	t.Helper()
	files := seriesPair(t, "hounded@1", "hexed@2")
	for _, slug := range []string{"gehetzt", "verhext", "entfesselt"} {
		addWork(t, files, slug, "de")
	}
	files["series/dr/druiden-chroniken.json"] = seriesJSON(t, "druiden-chroniken", "Die Chroniken des Eisernen Druiden",
		"gehetzt@1", "verhext@2", "entfesselt@3")
	files["series/ir/"+druidTarget+".json"] = testpack.WithField(t,
		seriesJSON(t, druidTarget, "The Iron Druid Chronicles", "hounded@1", "hexed@2", "gehetzt@3"),
		"translation_of", []string{"druiden-chroniken"})
	files["series/ir/"+druidLoser+".json"] = seriesJSON(t, druidLoser, "Iron Druid Chronicles", loserMembers...)
	return files
}

// planAndApply plans one proposal through planOne - the path a real run takes, whose
// merge planners hold the staged change to pkg/check's link rules before it commits -
// and then runs the write phase, returning the tree's bytes before and after. The
// audit's own language veto keeps these folds out of a real run's proposals, which is
// why the proposal is planned directly: the rule under test is the plan-time backstop
// behind that veto.
func planAndApply(t *testing.T, data string, fd audit.Finding, class string) (*runner, map[string]string, map[string]string) {
	t.Helper()
	rn, _ := planFixture(t, data)
	rn.planOne(candidate{class: class, fd: fd})
	if rn.fatal != nil {
		t.Fatal(rn.fatal)
	}
	before := treeBytes(t, data)
	if err := rn.apply(rn.plan.series.write); err != nil {
		t.Fatalf("apply: %v (post-write problems %v)", err, rn.rep.PostProblems)
	}
	return rn, before, treeBytes(t, data)
}

// A fold that flips the survivor's majority onto its original's language (English
// 2 / German 1 becomes English 2 / German 3) leaves a SERIES translation whose two
// sides derive one language - which pkg/check reports as an ADVISORY (a misfiled
// member, not a false link; DESIGN section 11), so the merge is applied and the
// advisory is what the post-write load carries.
func TestMergeSeriesAppliesAFoldThatFlipsATranslationsDerivedLanguage(t *testing.T) {
	data := seedTree(t, languageFold(t, "verhext@4", "entfesselt@5"))
	rn, _, _ := planAndApply(t, data, seriesFinding(druidTarget, druidLoser), audit.ClassSeriesDup)
	if len(rn.rep.Applied) != 1 || len(rn.rep.Refused) != 0 {
		t.Fatalf("applied %+v, refused %+v; want the fold applied", rn.rep.Applied, rn.rep.Refused)
	}
	res := check.Load(data)
	if len(res.Problems) != 0 {
		t.Fatalf("the applied fold left problems: %v", res.Problems)
	}
	if !slices.ContainsFunc(res.Warnings, func(w check.Problem) bool {
		return check.AdvisoryClass(w) == check.AdvisorySeriesTranslationSameLanguage
	}) {
		t.Errorf("the flipped series link is not advised on: %v", res.Warnings)
	}
}

// A fold that only produces a TIE (English 2 / German 2) leaves the survivor's
// language unjudgeable, which pkg/check's rule skips - so it is applied.
func TestMergeSeriesAppliesAFoldThatOnlyTies(t *testing.T) {
	data := seedTree(t, languageFold(t, "verhext@4"))
	rn, _, _ := planAndApply(t, data, seriesFinding(druidTarget, druidLoser), audit.ClassSeriesDup)
	if len(rn.rep.Applied) != 1 || len(rn.rep.Refused) != 0 {
		t.Fatalf("applied %+v, refused %+v; want the fold applied", rn.rep.Applied, rn.rep.Refused)
	}
	if got := readEntry(t, data, pack.FamilySeries, druidTarget).Strs("translation_of"); !slices.Equal(got, []string{"druiden-chroniken"}) {
		t.Errorf("survivor translation_of = %v, want its link kept", got)
	}
	if entryExists(t, data, pack.FamilySeries, druidLoser) {
		t.Error("the loser is still there")
	}
}

// The work half of the same rule: a survivor whose own language is its new
// original's (a cross-language merge the audit vetoes, planned directly) is refused.
func TestMergeWorksRefusesATranslationInItsOriginalsLanguage(t *testing.T) {
	files := hammeredCluster(t)
	setField(t, files, "works/ha/hammered/work.json", "language", "de")
	setField(t, files, "works/ha/hammered-book-3/work.json", "translation_of", []string{"zorn"})
	addWork(t, files, "zorn", "de")
	data := seedTree(t, files)
	rn, before, after := planAndApply(t, data, mergeFinding("hammered", "hammered-book-3"), audit.ClassWorkDup)
	if len(rn.rep.Refused) != 1 || rn.rep.Refused[0].Category != CatTranslationLink ||
		!strings.Contains(rn.rep.Refused[0].Reason, `work hammered would be in "de", the same language as work zorn`) {
		t.Fatalf("refused %+v, applied %+v; want the language refusal", rn.rep.Refused, rn.rep.Applied)
	}
	if !equalTrees(before, after) {
		t.Error("a refused proposal changed the tree")
	}
}
