package repair

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/audit"
	"github.com/kodestar/audiosilo-meta/internal/rawentry"
	"github.com/kodestar/audiosilo-meta/internal/testpack"
	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// ops_test.go covers the four single-field ops and merge-series: one happy path each
// through the real detectors, plus every refusal branch.

// retitle-work: a retailer's decoration comes off the TITLE and the slug is left
// exactly as it was, because a slug is identity.
func TestRetitleWorkChangesTheTitleAndNotTheSlug(t *testing.T) {
	data := seedTree(t, map[string]string{
		"works/th/the-hobbit-unabridged/work.json":                          workJSON(t, "the-hobbit-unabridged", "The Hobbit (Unabridged)"),
		"works/th/the-hobbit-unabridged/recordings/nate-narrator-2020.json": recJSON(t, "nate-narrator-2020", "the-hobbit-unabridged"),
		"people/ja/jane-doe.json":                                           personJSON(t, "jane-doe", "Jane Doe"),
		"people/na/nate-narrator.json":                                      personJSON(t, "nate-narrator", "Nate Narrator"),
	})

	rep := run(t, Options{DataDir: data, Ops: []string{audit.OpRetitle}, Write: true})
	if len(rep.Applied) != 1 {
		t.Fatalf("applied %+v, refused %+v", rep.Applied, rep.Refused)
	}
	if got := workEntry(t, data, "the-hobbit-unabridged").Str("title"); got != "The Hobbit" {
		t.Errorf("title = %q, want the edition marker removed", got)
	}
	if !entryExists(t, data, pack.FamilyWorks, "the-hobbit-unabridged") {
		t.Error("the slug moved; a retitle must never rename a record")
	}
	if loadRedirects(t, data).Len() != 0 {
		t.Error("a retitle recorded a tombstone; nothing was retired")
	}
}

// A retitle whose From no longer matches the record is refused: the proposal was
// written against a title the tree no longer states.
func TestRetitleRefusesAChangedTitle(t *testing.T) {
	data := seedTree(t, hammeredCluster(t))
	rn, tx := planFixture(t, data)
	err := rn.retitleWork(tx, audit.Finding{
		Key: "hammered", Propose: audit.Proposal{
			Op: audit.OpRetitle, Target: "hammered", Field: "title",
			From: "Hammered (Unabridged)", To: "Hammered",
		},
	})
	assertRefusal(t, err, CatStaleValue, "now states the title")
}

// add-series-member: the title names a series and a volume, the catalogue models
// neither, so the membership is added at the position the title states.
func TestAddSeriesMemberModelsTheMembership(t *testing.T) {
	data := seedTree(t, map[string]string{
		"works/ho/hounded/work.json":                                          workJSON(t, "hounded", "Hounded"),
		"works/ho/hounded/recordings/luke-daniels-2011.json":                  recJSON(t, "luke-daniels-2011", "hounded", withNarrators("luke-daniels")),
		"works/he/hexed-druid-tales-book-2/work.json":                         workJSON(t, "hexed-druid-tales-book-2", "Hexed: The Druid Tales, Book 2"),
		"works/he/hexed-druid-tales-book-2/recordings/luke-daniels-2011.json": recJSON(t, "luke-daniels-2011", "hexed-druid-tales-book-2", withNarrators("luke-daniels")),
		"people/ja/jane-doe.json":                                             personJSON(t, "jane-doe", "Jane Doe"),
		"people/lu/luke-daniels.json":                                         personJSON(t, "luke-daniels", "Luke Daniels"),
		"series/dr/druid-tales.json":                                          seriesJSON(t, "druid-tales", "The Druid Tales", "hounded@1"),
	})

	rep := run(t, Options{DataDir: data, Ops: []string{audit.OpAddSeriesMember}, Write: true})
	if len(rep.Applied) != 1 {
		t.Fatalf("applied %+v, refused %+v", rep.Applied, rep.Refused)
	}
	want := []model.SeriesWork{{Work: "hounded", Position: "1"}, {Work: "hexed-druid-tales-book-2", Position: "2"}}
	if got := readEntry(t, data, pack.FamilySeries, "druid-tales").SeriesWorks(); !reflect.DeepEqual(got, want) {
		t.Errorf("memberships = %+v, want %+v (appended, in the order the importer would)", got, want)
	}
}

// The same op, refused three ways: an occupied position, a work already listed, and a
// position the data model does not accept.
func TestAddSeriesMemberRefusals(t *testing.T) {
	data := seedTree(t, hammeredCluster(t))
	base := func(work, pos string) audit.Finding {
		return audit.Finding{Key: work, Propose: audit.Proposal{
			Op: audit.OpAddSeriesMember, Target: work, Series: "druid-tales", Field: "series", To: pos,
		}}
	}
	t.Run("position taken", func(t *testing.T) {
		rn, tx := planFixture(t, data)
		assertRefusal(t, rn.addSeriesMember(tx, base("hammered-book-3", "3")), CatPositionConflict, "is held by hammered")
	})
	t.Run("position taken under another spelling", func(t *testing.T) {
		rn, tx := planFixture(t, data)
		assertRefusal(t, rn.addSeriesMember(tx, base("hammered-book-3", "03")), CatPositionConflict, "is held by hammered")
	})
	t.Run("already a member", func(t *testing.T) {
		rn, tx := planFixture(t, data)
		assertRefusal(t, rn.addSeriesMember(tx, base("hammered", "9")), CatStaleValue, "already lists")
	})
	t.Run("not a position", func(t *testing.T) {
		rn, tx := planFixture(t, data)
		assertRefusal(t, rn.addSeriesMember(tx, base("hammered-book-3", "the third")), CatMalformed, "not a position")
	})
	t.Run("no position stated", func(t *testing.T) {
		rn, tx := planFixture(t, data)
		assertRefusal(t, rn.addSeriesMember(tx, base("hammered-book-3", "")), CatNoValue, "no position")
	})
	t.Run("no such work", func(t *testing.T) {
		rn, tx := planFixture(t, data)
		assertRefusal(t, rn.addSeriesMember(tx, base("no-such-work", "9")), CatMissing, "no work")
	})
}

// restate-position: a position is a STRING, so "02" and "2" are different positions to
// every rule that compares them. The canonical spelling is written in place.
func TestRestatePositionRewritesTheSpelling(t *testing.T) {
	data := seedTree(t, map[string]string{
		"works/ho/hounded/work.json":                         workJSON(t, "hounded", "Hounded"),
		"works/ho/hounded/recordings/luke-daniels-2011.json": recJSON(t, "luke-daniels-2011", "hounded", withNarrators("luke-daniels")),
		"people/ja/jane-doe.json":                            personJSON(t, "jane-doe", "Jane Doe"),
		"people/lu/luke-daniels.json":                        personJSON(t, "luke-daniels", "Luke Daniels"),
		"series/dr/druid-tales.json":                         seriesJSON(t, "druid-tales", "The Druid Tales", "hounded@2.50"),
	})

	rep := run(t, Options{DataDir: data, Ops: []string{audit.OpRestatePosition}, Write: true})
	if len(rep.Applied) != 1 {
		t.Fatalf("applied %+v, refused %+v", rep.Applied, rep.Refused)
	}
	want := []model.SeriesWork{{Work: "hounded", Position: "2.5"}}
	if got := readEntry(t, data, pack.FamilySeries, "druid-tales").SeriesWorks(); !reflect.DeepEqual(got, want) {
		t.Errorf("memberships = %+v, want %+v", got, want)
	}
}

func TestRestatePositionRefusals(t *testing.T) {
	data := seedTree(t, hammeredCluster(t))
	base := func(from, to string) audit.Finding {
		return audit.Finding{Key: "druid-tales", Propose: audit.Proposal{
			Op: audit.OpRestatePosition, Target: "hammered", Series: "druid-tales", Field: "position", From: from, To: to,
		}}
	}
	t.Run("membership gone", func(t *testing.T) {
		rn, tx := planFixture(t, data)
		assertRefusal(t, rn.restatePosition(tx, base("7", "8")), CatStaleValue, "no longer lists")
	})
	t.Run("no value", func(t *testing.T) {
		rn, tx := planFixture(t, data)
		assertRefusal(t, rn.restatePosition(tx, base("3", "")), CatNoValue, "no replacement position")
	})
	t.Run("non-canonical target spelling", func(t *testing.T) {
		rn, tx := planFixture(t, data)
		assertRefusal(t, rn.restatePosition(tx, base("3", "2.50")), CatMalformed, "canonical spelling")
	})
	t.Run("no series named", func(t *testing.T) {
		rn, tx := planFixture(t, data)
		fd := base("3", "4")
		fd.Propose.Series = ""
		assertRefusal(t, rn.restatePosition(tx, fd), CatMalformed, "no work or no series")
	})
}

// fill-field: every proposal a real report carries states no VALUE (a language nobody
// recorded is not derivable from the record), so the op refuses and says why rather
// than being silently skipped.
func TestFillFieldRefusesAProposalWithNoValue(t *testing.T) {
	data := seedTreeAllowingProblems(t, map[string]string{
		"works/no/no-language/work.json":                          workJSON(t, "no-language", "A Book With No Language", withoutLanguage()),
		"works/no/no-language/recordings/nate-narrator-2020.json": recJSON(t, "nate-narrator-2020", "no-language"),
		"people/ja/jane-doe.json":                                 personJSON(t, "jane-doe", "Jane Doe"),
		"people/na/nate-narrator.json":                            personJSON(t, "nate-narrator", "Nate Narrator"),
	})
	// The fixture is deliberately schema-INVALID (language is required), which is the
	// only way F-HYGIENE reports a missing language - so this is a dry run: --write
	// refuses a tree that does not validate.
	rep, err := Run(Options{DataDir: data, Ops: []string{audit.OpFillField}})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Applied) != 0 {
		t.Fatalf("a fill-field with no value was applied: %+v", rep.Applied)
	}
	if len(rep.Refused) == 0 || rep.Refused[0].Category != CatNoValue {
		t.Fatalf("refusals = %+v, want a %s", rep.Refused, CatNoValue)
	}
}

// The apply path exists for the day a detector does state a value, and refuses a field
// outside the narrow scalar set - so a proposal naming something else is never written.
func TestFillFieldWritesAStatedValueAndRefusesOtherFields(t *testing.T) {
	data := seedTree(t, hammeredCluster(t))
	fd := func(field, to string) audit.Finding {
		return audit.Finding{Key: "hammered", Propose: audit.Proposal{
			Op: audit.OpFillField, Target: "hammered", Field: field, To: to,
		}}
	}
	rn, tx := planFixture(t, data)
	if err := rn.fillField(tx, fd("first_published", "2011")); err != nil {
		t.Fatalf("a stated value was not written: %v", err)
	}
	e, _, err := tx.works.get("hammered")
	if err != nil {
		t.Fatal(err)
	}
	if got := e.Str("first_published"); got != "2011" {
		t.Errorf("first_published = %q, want it filled", got)
	}
	assertRefusal(t, rn.fillField(tx, fd("license", "CC0-1.0")), CatMalformed, "not one of the work scalars")
	assertRefusal(t, rn.fillField(tx, fd("language", "de")), CatStaleValue, "already states")
}

// merge-series: two spellings of one series fold into one, the memberships union, and
// the retired spelling keeps resolving.
func TestMergeSeriesUnionsTheMembershipsAndTombstonesTheSlug(t *testing.T) {
	data := seedTree(t, seriesPair(t, "hounded@1", "hexed@2"))

	rep := run(t, Options{DataDir: data, Ops: []string{audit.OpMergeSeries}, Write: true})
	if len(rep.Applied) != 1 || len(rep.Refused) != 0 {
		t.Fatalf("applied %+v, refused %+v", rep.Applied, rep.Refused)
	}
	target := rep.Applied[0].Target
	loser := rep.Applied[0].Others[0]
	got := readEntry(t, data, pack.FamilySeries, target).SeriesWorks()
	if len(got) != 2 {
		t.Errorf("memberships = %+v, want both series' works", got)
	}
	if entryExists(t, data, pack.FamilySeries, loser) {
		t.Error("the retired series entry is still there")
	}
	if to := loadRedirects(t, data)[model.RedirectSeries][loser]; to != target {
		t.Errorf("series redirect = %q, want %q", to, target)
	}
	// A merge-series rewrites nothing outside the series family: a work does not
	// reference its series.
	if w := workEntry(t, data, "hexed"); w.Has("series") {
		t.Error("a work gained a series member; works do not reference series")
	}
}

// A merge-series names every series-level value it chose away, in merge-works' note
// format: the retired spelling of the NAME (the importer joins a series by name, so a
// source still spelling it that way stops meeting it) and a differing xref member.
// Values that agree, or that only the loser states (filled onto the survivor), are not
// a choice and add no line.
func TestMergeSeriesNotesTheValuesItChoseAway(t *testing.T) {
	const keeper, loser = "iron-druid-chronicles", "iron-druid-chronicles-2"
	set := func(files map[string]string, slug, field string, v any) {
		path := "series/ir/" + slug + ".json"
		files[path] = testpack.WithField(t, files[path], field, v)
	}
	apply := func(t *testing.T, data string) (target, notes string) {
		t.Helper()
		rep := run(t, Options{DataDir: data, Ops: []string{audit.OpMergeSeries}, Write: true})
		if len(rep.Applied) != 1 || len(rep.Refused) != 0 {
			t.Fatalf("applied %+v, refused %+v", rep.Applied, rep.Refused)
		}
		return rep.Applied[0].Target, strings.Join(rep.Applied[0].Notes, "\n")
	}

	t.Run("a differing name and QID are named", func(t *testing.T) {
		// seriesPair spells the two "The Iron Druid Chronicles" and "Iron Druid Chronicles".
		files := seriesPair(t, "hounded@1", "hexed@2")
		set(files, keeper, "xref", map[string]any{"wikidata": "Q111"})
		set(files, loser, "xref", map[string]any{"wikidata": "Q222"})
		target, notes := apply(t, seedTree(t, files))
		if target != keeper {
			t.Fatalf("target = %s, want %s", target, keeper)
		}
		for _, want := range []string{
			`name: kept "The Iron Druid Chronicles", dropped "Iron Druid Chronicles" from ` + loser,
			`xref.wikidata: kept "Q111", dropped "Q222" from ` + loser,
		} {
			if !strings.Contains(notes, want) {
				t.Errorf("notes do not report %q:\n%s", want, notes)
			}
		}
	})
	t.Run("an identical name and a filled QID add no line", func(t *testing.T) {
		// Two series cannot share a QID (pkg/check), so the xref side of "nothing chosen
		// away" is a QID only the loser states: it is FILLED onto the survivor.
		files := seriesPair(t, "hounded@1", "hexed@2")
		set(files, loser, "name", "The Iron Druid Chronicles")
		set(files, loser, "xref", map[string]any{"wikidata": "Q222"})
		data := seedTree(t, files)
		target, notes := apply(t, data)
		if strings.Contains(notes, "dropped") {
			t.Errorf("a fold that chose nothing away reports a dropped value:\n%s", notes)
		}
		xref, err := rawentry.Decode(readEntry(t, data, pack.FamilySeries, target)["xref"])
		if err != nil || xref.Str("wikidata") != "Q222" {
			t.Errorf("survivor xref = %v (%v), want the loser's QID filled in", xref, err)
		}
	})
}

// A work the two spellings place at different positions is two orderings, not one
// series, and a position two different works would share is what pkg/check refuses.
func TestMergeSeriesRefusesPositionContradictions(t *testing.T) {
	t.Run("one work at two positions", func(t *testing.T) {
		data := seedTree(t, seriesPair(t, "hounded@1", "hounded@2"))
		rn, tx := planFixture(t, data)
		err := rn.mergeSeries(tx, seriesFinding("iron-druid-chronicles", "iron-druid-chronicles-2"))
		assertRefusal(t, err, CatPositionConflict, "two orderings are not one series")
	})
	t.Run("two works at one position", func(t *testing.T) {
		data := seedTree(t, seriesPair(t, "hounded@1", "hexed@1"))
		rn, tx := planFixture(t, data)
		err := rn.mergeSeries(tx, seriesFinding("iron-druid-chronicles", "iron-druid-chronicles-2"))
		assertRefusal(t, err, CatPositionConflict, "two works at one position")
	})
}

// Two works at one SLOT spelled two ways ("3" and "03") is two works at one place in the
// order, and pkg/check - which compares the stored strings - would have accepted it. The
// slot lookup is what refuses it.
func TestMergeSeriesRefusesOneSlotSpelledTwoWays(t *testing.T) {
	data := seedTree(t, seriesPair(t, "hounded@3", "hexed@03"))
	rn, tx := planFixture(t, data)
	err := rn.mergeSeries(tx, seriesFinding("iron-druid-chronicles", "iron-druid-chronicles-2"))
	assertRefusal(t, err, CatPositionConflict, "two works at one position")
}

// seriesPair is two series spellings over the same two works, for the merge-series
// refusal cases.
func seriesPair(t testing.TB, first, second string) map[string]string {
	t.Helper()
	return map[string]string{
		"works/ho/hounded/work.json":                         workJSON(t, "hounded", "Hounded"),
		"works/ho/hounded/recordings/luke-daniels-2011.json": recJSON(t, "luke-daniels-2011", "hounded", withNarrators("luke-daniels")),
		"works/ha/hexed/work.json":                           workJSON(t, "hexed", "Hexed"),
		"works/ha/hexed/recordings/luke-daniels-2012.json":   recJSON(t, "luke-daniels-2012", "hexed", withNarrators("luke-daniels")),
		"people/ja/jane-doe.json":                            personJSON(t, "jane-doe", "Jane Doe"),
		"people/lu/luke-daniels.json":                        personJSON(t, "luke-daniels", "Luke Daniels"),
		"series/ir/iron-druid-chronicles.json":               seriesJSON(t, "iron-druid-chronicles", "The Iron Druid Chronicles", first),
		"series/ir/iron-druid-chronicles-2.json":             seriesJSON(t, "iron-druid-chronicles-2", "Iron Druid Chronicles", second),
	}
}

func seriesFinding(target string, losers ...string) audit.Finding {
	return audit.Finding{
		Class: audit.ClassSeriesDup, Key: target, Subclass: "name-key",
		Propose: audit.Proposal{Op: audit.OpMergeSeries, Target: target, Others: losers},
	}
}

// familyTree is an ordering family over the two seriesPair works plus a third: a
// publication-order primary and its chronological variant, beside a plain spelling the
// caller lists.
func familyTree(t testing.TB, plain ...string) map[string]string {
	t.Helper()
	files := seriesPair(t, "hounded@1", "hexed@2")
	delete(files, "series/ir/iron-druid-chronicles-2.json")
	files["works/tr/tricked/work.json"] = workJSON(t, "tricked", "Tricked")
	files["works/tr/tricked/recordings/luke-daniels-2013.json"] = recJSON(t, "luke-daniels-2013", "tricked", withNarrators("luke-daniels"))
	files["series/ir/iron-druid-chronicles.json"] = testpack.WithField(t,
		seriesJSON(t, "iron-druid-chronicles", "The Iron Druid Chronicles", "hounded@1", "hexed@2", "tricked@4"),
		"ordering", model.OrderingPublication)
	files["series/ir/iron-druid-chronological.json"] = testpack.WithFields(t,
		seriesJSON(t, "iron-druid-chronological", "The Iron Druid Chronicles (Chronological Order)", "hexed@1", "hounded@2", "tricked@3"),
		map[string]any{"ordering": model.OrderingChronological, "ordering_of": "iron-druid-chronicles"})
	files["series/ir/iron-druid-plain.json"] = seriesJSON(t, "iron-druid-plain", "Iron Druid Chronicles", plain...)
	return files
}

// A family-spelling fold onto a VARIANT: the plain loser has nothing to say about the
// family, so the survivor - cloned, never rebuilt - keeps its own ordering and ordering_of
// and the plain slug is tombstoned onto it. Nothing in the audit applies this on its own
// (it is advisory onto a variant); a reviewed acceptance does, and this is what it writes.
func TestMergeSeriesKeepsTheVariantSurvivorsOrderingForAPlainLoser(t *testing.T) {
	const variant = "iron-druid-chronological"
	rn, tx := planFixture(t, seedTree(t, familyTree(t, "hexed@1", "hounded@2")))
	if err := rn.mergeSeries(tx, seriesFinding(variant, "iron-druid-plain")); err != nil {
		t.Fatal(err)
	}
	e, ok, err := tx.series.get(variant)
	if err != nil || !ok {
		t.Fatalf("staged variant: %v %v", ok, err)
	}
	if e.Str("ordering") != model.OrderingChronological || e.Str("ordering_of") != "iron-druid-chronicles" {
		t.Errorf("survivor ordering = %q / ordering_of = %q, want the variant's own", e.Str("ordering"), e.Str("ordering_of"))
	}
	if got := len(e.SeriesWorks()); got != 3 {
		t.Errorf("memberships = %d, want the variant's three unchanged", got)
	}
	if !slices.Contains(tx.retires, retiredKey(pack.FamilySeries, "iron-druid-plain")) {
		t.Errorf("the plain spelling was not retired: %v", tx.retires)
	}
	if len(tx.tombs) != 1 || tx.tombs[0].from != "iron-druid-plain" || tx.tombs[0].to != variant {
		t.Errorf("tombstones = %+v, want iron-druid-plain -> %s", tx.tombs, variant)
	}
}

// renumberedFinding is a family-renumbered merge-series: Field "position".
func renumberedFinding(target, loser string) audit.Finding {
	fd := seriesFinding(target, loser)
	fd.Subclass = audit.SubclassFamilyRenumbered
	fd.Propose.Field = audit.FieldPosition
	return fd
}

// A family-renumbered fold keeps the TARGET's numbering: a loser membership the target
// lists at another slot is not a conflict, and its dropped number is named in the notes
// in the one loss format. A loser membership the target does not list at all still
// refuses - a renumbered fold only retires - and no other field names a merge-series.
func TestFamilyRenumberedKeepsTheTargetsNumbering(t *testing.T) {
	const primary = "iron-druid-chronicles"
	t.Run("the target's numbers stand", func(t *testing.T) {
		rn, tx := planFixture(t, seedTree(t, familyTree(t, "hounded@1", "hexed@2", "tricked@3")))
		if err := rn.mergeSeries(tx, renumberedFinding(primary, "iron-druid-plain")); err != nil {
			t.Fatal(err)
		}
		e, _, err := tx.series.get(primary)
		if err != nil {
			t.Fatal(err)
		}
		at := map[string]string{}
		for _, sw := range e.SeriesWorks() {
			at[sw.Work] = sw.Position
		}
		if want := map[string]string{"hounded": "1", "hexed": "2", "tricked": "4"}; !reflect.DeepEqual(at, want) {
			t.Errorf("memberships = %v, want the primary's numbering %v", at, want)
		}
		if want := `position of tricked: kept "4", dropped "3" from iron-druid-plain`; !strings.Contains(strings.Join(tx.notes, "\n"), want) {
			t.Errorf("notes = %v, want %q", tx.notes, want)
		}
	})
	t.Run("a membership the target lacks refuses", func(t *testing.T) {
		files := familyTree(t, "hounded@1", "extra@3")
		files["works/ex/extra/work.json"] = workJSON(t, "extra", "Extra")
		files["works/ex/extra/recordings/luke-daniels-2014.json"] = recJSON(t, "luke-daniels-2014", "extra", withNarrators("luke-daniels"))
		rn, tx := planFixture(t, seedTree(t, files))
		assertRefusal(t, rn.mergeSeries(tx, renumberedFinding(primary, "iron-druid-plain")), CatPositionConflict, "never adds a membership")
	})
	t.Run("an unknown field is malformed", func(t *testing.T) {
		rn, tx := planFixture(t, seedTree(t, familyTree(t, "hounded@1")))
		fd := seriesFinding(primary, "iron-druid-plain")
		fd.Propose.Field = "name"
		assertRefusal(t, rn.mergeSeries(tx, fd), CatMalformed, `names field "name"`)
	})
}
