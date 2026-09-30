package audit

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/testpack"
)

// langmix_test.go covers L-MIX: one fixture per subclass, one per veto, the
// set-work-language review in both of its shapes, and the class's place in the
// report (it replaced S-INTEGRITY's minority-language record).

// mixPeople are the credits every L-MIX fixture resolves: the default author and
// narrator, a German narrator and a second author.
func mixPeople(t testing.TB) map[string]string {
	t.Helper()
	return map[string]string{
		"people/ja/jane-doe.json":      personJSON(t, "jane-doe", "Jane Doe"),
		"people/na/nate-narrator.json": personJSON(t, "nate-narrator", "Nate Narrator"),
		"people/an/anna-sprecher.json": personJSON(t, "anna-sprecher", "Anna Sprecher"),
		"people/ot/otto-autor.json":    personJSON(t, "otto-autor", "Otto Autor"),
	}
}

// mixWork seeds a work in lang with one recording in the same language, by narrator.
func mixWork(t testing.TB, id, lang, narrator string, opts ...testpack.WorkOpt) map[string]string {
	t.Helper()
	return map[string]string{
		"works/xx/" + id + "/work.json": workJSON(t, id, strings.ToUpper(id[:1])+id[1:],
			append([]testpack.WorkOpt{withLanguage(lang)}, opts...)...),
		"works/xx/" + id + "/recordings/r-" + id + ".json": recJSON(t, "r-"+id, id,
			withNarrators(narrator), testpack.WithRecLanguage(lang)),
	}
}

// sagaTree is the split shape: an English series holding two German volumes, with no
// German series of the name anywhere.
func mixSagaTree(t testing.TB) map[string]string {
	t.Helper()
	return mergeFiles(mixPeople(t),
		mixWork(t, "dawn", "en", "nate-narrator"), mixWork(t, "dusk", "en", "nate-narrator"),
		mixWork(t, "noon", "en", "nate-narrator"),
		mixWork(t, "morgen", "de", "anna-sprecher"), mixWork(t, "abend", "de", "anna-sprecher"),
		map[string]string{"series/sa/the-saga.json": seriesJSON(t, "the-saga", "The Saga",
			"dawn@1", "dusk@2", "noon@3", "morgen@4", "abend@5")},
	)
}

func onlyMix(t testing.TB, rep *Report, subclass string) Finding {
	t.Helper()
	got := subclassOf(t, rep, ClassLangMix, subclass)
	if len(got) != 1 {
		t.Fatalf("want one %s record, got %d: %+v", subclass, len(got), classOf(t, rep, ClassLangMix))
	}
	return got[0]
}

func TestLangMixSplitsTheMinorityLanguage(t *testing.T) {
	rep := runFixture(t, mixSagaTree(t))
	fd := onlyMix(t, rep, lMixSplit)
	want := Proposal{Op: OpSplitSeries, Target: "the-saga", Others: []string{"abend", "morgen"},
		Field: fieldLanguage, From: "en", To: "de"}
	got := fd.Propose
	got.Reason = ""
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("proposal = %+v, want %+v", fd.Propose, want)
	}
	if fd.Key != "the-saga/de" {
		t.Errorf("key = %q", fd.Key)
	}
	if !strings.Contains(strings.Join(fd.Notes, " "), "at the-saga-2") {
		t.Errorf("notes = %v, want the slug the split would mint named", fd.Notes)
	}
	// The S-INTEGRITY record it replaced is gone.
	for _, r := range classOf(t, rep, ClassSeriesInteg) {
		if r.Propose.Field == fieldLanguage {
			t.Errorf("S-INTEGRITY still files a language record: %+v", r)
		}
	}
	assertProposalsConsistent(t, rep)
}

// A de+en+fr series yields one split per minority language.
func TestLangMixSplitsEachMinorityLanguageApart(t *testing.T) {
	files := mixSagaTree(t)
	files = mergeFiles(files, mixWork(t, "aube", "fr", "nate-narrator"), map[string]string{
		"series/sa/the-saga.json": seriesJSON(t, "the-saga", "The Saga",
			"dawn@1", "dusk@2", "noon@3", "morgen@4", "abend@5", "aube@6"),
	})
	got := subclassOf(t, runFixture(t, files), ClassLangMix, lMixSplit)
	if len(got) != 2 || got[0].Key != "the-saga/de" || got[1].Key != "the-saga/fr" {
		t.Fatalf("want the de and fr splits, got %+v", got)
	}
}

func TestLangMixDropsAMemberAlreadyHomed(t *testing.T) {
	files := mergeFiles(mixPeople(t),
		mixWork(t, "c1", "en", "nate-narrator"), mixWork(t, "c2", "en", "nate-narrator"),
		mixWork(t, "chronik", "de", "anna-sprecher"), mixWork(t, "chronik-zwei", "de", "anna-sprecher"),
		map[string]string{
			"series/ch/chronicle.json":   seriesJSON(t, "chronicle", "The Chronicle", "c1@1", "c2@2", "chronik@3"),
			"series/di/die-chronik.json": seriesJSON(t, "die-chronik", "Die Chronik", "chronik@1", "chronik-zwei@2"),
		})
	rep := runFixture(t, files)
	fd := onlyMix(t, rep, lMixHomed)
	p := fd.Propose
	if p.Op != OpDropMembership || p.Target != "chronik" || p.Series != "chronicle" || p.From != "3" ||
		!slices.Equal(p.Others, []string{"die-chronik"}) || p.Advisory {
		t.Fatalf("proposal = %+v", p)
	}
	if len(subclassOf(t, rep, ClassLangMix, lMixSplit)) != 0 {
		t.Error("a homed member was also proposed for a split")
	}
	assertProposalsConsistent(t, rep)
}

// moveTree: an English series holding a German volume, and a German series of the same
// name over the edition decoration's base.
func moveTree(t testing.TB, germanMembers ...string) map[string]string {
	t.Helper()
	return mergeFiles(mixPeople(t),
		mixWork(t, "f1", "en", "nate-narrator"), mixWork(t, "f2", "en", "nate-narrator"),
		mixWork(t, "schicksal-3", "de", "anna-sprecher"),
		mixWork(t, "s1", "de", "anna-sprecher"), mixWork(t, "s2", "de", "anna-sprecher"),
		map[string]string{
			"series/fa/fate.json":        seriesJSON(t, "fate", "Fate", "f1@1", "f2@2", "schicksal-3@3"),
			"series/fa/fate-german.json": seriesJSON(t, "fate-german", "Fate [German Edition]", germanMembers...),
		})
}

func TestLangMixMovesToTheSameNamedSeriesOfItsLanguage(t *testing.T) {
	rep := runFixture(t, moveTree(t, "s1@1", "s2@2"))
	fd := onlyMix(t, rep, lMixTarget)
	p := fd.Propose
	if p.Op != OpMoveMembership || p.Target != "schicksal-3" || p.Series != "fate" || p.From != "3" || p.To != "3" ||
		!slices.Equal(p.Others, []string{"fate-german"}) || p.Advisory {
		t.Fatalf("proposal = %+v", p)
	}
	assertProposalsConsistent(t, rep)
}

// The target's slot at the work's position is taken: the move is advisory.
func TestLangMixMoveIntoATakenSlotIsAdvisory(t *testing.T) {
	fd := onlyMix(t, runFixture(t, moveTree(t, "s1@1", "s2@3")), lMixTarget)
	if !fd.Propose.Advisory || !strings.Contains(fd.Propose.Reason, "held by s2") {
		t.Fatalf("proposal = %+v, want advisory naming the holder", fd.Propose)
	}
}

// A translation_of link reaches a target of another name; two targets are a review.
func TestLangMixTranslationLinkedTargetsAndSeveral(t *testing.T) {
	files := moveTree(t, "s1@1")
	files = mergeFiles(files, mixWork(t, "g1", "de", "anna-sprecher"), map[string]string{
		"series/sc/schicksal.json": testpack.WithFields(t, seriesJSON(t, "schicksal", "Schicksal", "g1@1"),
			map[string]any{"translation_of": []string{"fate"}}),
	})
	fd := onlyMix(t, runFixture(t, files), lMixTargets)
	if fd.Propose.Op != OpReview || !fd.Propose.Advisory || !slices.Equal(fd.Propose.Others, []string{"fate-german", "schicksal"}) {
		t.Fatalf("proposal = %+v", fd.Propose)
	}
	// With the same-name target gone, the link alone reaches schicksal.
	delete(files, "series/fa/fate-german.json")
	files["works/xx/s1/work.json"] = workJSON(t, "s1", "S1", withLanguage("de"))
	files["series/sc/schicksal.json"] = testpack.WithFields(t, seriesJSON(t, "schicksal", "Schicksal", "g1@1", "s1@2"),
		map[string]any{"translation_of": []string{"fate"}})
	fd = onlyMix(t, runFixture(t, files), lMixTarget)
	if !slices.Equal(fd.Propose.Others, []string{"schicksal"}) || fd.Propose.Advisory {
		t.Fatalf("proposal = %+v", fd.Propose)
	}
}

// A coupled member - it carries a recording in the series' language - is W4.1's first.
func TestLangMixCoupledMemberIsAdvisory(t *testing.T) {
	files := mixSagaTree(t)
	files["works/xx/morgen/recordings/r-morgen-en.json"] = recJSON(t, "r-morgen-en", "morgen", withNarrators("nate-narrator"))
	fd := onlyMix(t, runFixture(t, files), lMixSplit)
	if !fd.Propose.Advisory || !strings.Contains(fd.Propose.Reason, "carries a recording in en") {
		t.Fatalf("proposal = %+v", fd.Propose)
	}
}

// narratedTree: a German series holding an English-stated work whose narrator records
// two other German works.
func narratedTree(t testing.TB) map[string]string {
	t.Helper()
	return mergeFiles(mixPeople(t),
		mixWork(t, "r1", "de", "anna-sprecher"), mixWork(t, "r2", "de", "anna-sprecher"),
		mixWork(t, "r3", "de", "anna-sprecher"), mixWork(t, "imperium", "en", "anna-sprecher"),
		map[string]string{"series/re/reihe.json": seriesJSON(t, "reihe", "Reihe", "r1@1", "r2@2", "r3@3", "imperium@4")})
}

func TestLangMixNarratorContradictionWithholdsTheSplitAndProposesTheLanguage(t *testing.T) {
	rep := runFixture(t, narratedTree(t))
	split := onlyMix(t, rep, lMixSplit)
	if !split.Propose.Advisory || !strings.Contains(split.Propose.Reason, "record in de") {
		t.Fatalf("split = %+v, want advisory on the narrator evidence", split.Propose)
	}
	lang := onlyMix(t, rep, lMixNarration)
	want := Proposal{Op: OpSetWorkLanguage, Target: "imperium", Field: fieldLanguage, From: "en", To: "de", Advisory: true}
	got := lang.Propose
	got.Reason = ""
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("language proposal = %+v, want %+v", lang.Propose, want)
	}
	if rep.LangMix.NarratorContradicted != 1 {
		t.Errorf("tally = %+v", rep.LangMix)
	}
}

// The Rubinrot shape: a work whose every recording states another language, in no
// mixed series at all.
func TestLangMixProposesTheRecordingsLanguageForTheRubinrotShape(t *testing.T) {
	files := mergeFiles(mixPeople(t), mixWork(t, "r1", "de", "anna-sprecher"), mixWork(t, "r2", "de", "anna-sprecher"),
		map[string]string{
			"works/xx/rubinrot/work.json": workJSON(t, "rubinrot", "Rubinrot"),
			"works/xx/rubinrot/recordings/r.json": recJSON(t, "r", "rubinrot", withNarrators("anna-sprecher"),
				testpack.WithRecLanguage("de")),
		})
	rep := runFixture(t, files)
	fd := onlyMix(t, rep, lMixNarration)
	if fd.Propose.Op != OpSetWorkLanguage || fd.Propose.From != "en" || fd.Propose.To != "de" || !fd.Propose.Advisory {
		t.Fatalf("proposal = %+v", fd.Propose)
	}
	if rep.LangMix.AllOther != 1 || rep.LangMix.AllOtherContradicted != 1 || rep.LangMix.CrossRecordings != 1 {
		t.Errorf("tally = %+v", rep.LangMix)
	}
	// Without the narrator evidence the shape is counted and proposes nothing.
	delete(files, "works/xx/r2/work.json")
	delete(files, "works/xx/r2/recordings/r-r2.json")
	rep = runFixture(t, files)
	if got := subclassOf(t, rep, ClassLangMix, lMixNarration); len(got) != 0 || rep.LangMix.AllOther != 1 {
		t.Errorf("one other recording is no evidence: %+v, tally %+v", got, rep.LangMix)
	}
}

// A tie is decided by the incumbent - the member catalogued first - and is advisory.
func TestLangMixTieByIncumbencyIsAdvisory(t *testing.T) {
	files := mergeFiles(mixPeople(t),
		mixWork(t, "t-en", "en", "nate-narrator", testpack.WithAddedAt("2026-02-01")),
		mixWork(t, "t-de", "de", "anna-sprecher", testpack.WithAddedAt("2026-01-01")),
		map[string]string{"series/ti/tie.json": seriesJSON(t, "tie", "Tie", "t-en@1", "t-de@2")})
	rep := runFixture(t, files)
	fd := onlyMix(t, rep, lMixSplit)
	if fd.Propose.From != "de" || fd.Propose.To != "en" || !fd.Propose.Advisory ||
		!strings.Contains(fd.Propose.Reason, "incumbency") {
		t.Fatalf("proposal = %+v, want the de incumbent keeping the slug, advisory", fd.Propose)
	}
	if rep.LangMix.TieSeries != 1 {
		t.Errorf("tally = %+v", rep.LangMix)
	}
}

// A tie whose name states an edition: that language keeps the slug, so a drop there is
// mechanical - while a split, whose new series would carry the decorated name onto
// works of another language, is not.
func TestLangMixTieByDecoration(t *testing.T) {
	base := mergeFiles(mixPeople(t),
		mixWork(t, "t-en", "en", "nate-narrator", testpack.WithAddedAt("2025-01-01")),
		mixWork(t, "t-de", "de", "anna-sprecher"),
		map[string]string{"series/ti/tie.json": seriesJSON(t, "tie", "Tie [German Edition]", "t-en@1", "t-de@2")})
	split := onlyMix(t, runFixture(t, base), lMixSplit)
	if split.Propose.From != "de" || !split.Propose.Advisory || !strings.Contains(split.Propose.Reason, "would be named") {
		t.Fatalf("split = %+v", split.Propose)
	}
	homed := mergeFiles(base, mixWork(t, "t-en-2", "en", "nate-narrator"), map[string]string{
		"series/ti/tie-en.json": seriesJSON(t, "tie-en", "Tie", "t-en@1", "t-en-2@2"),
	})
	rep := runFixture(t, homed)
	drop := onlyMix(t, rep, lMixHomed)
	if drop.Propose.Advisory || drop.Propose.Target != "t-en" {
		t.Fatalf("drop = %+v, want mechanical", drop.Propose)
	}
	if rep.LangMix.TieByDecoration != 1 {
		t.Errorf("tally = %+v", rep.LangMix)
	}
}

// The three reasons the majority keeping the slug is in doubt.
func TestLangMixSlugVetoes(t *testing.T) {
	for name, tc := range map[string]struct {
		files  func(t testing.TB) map[string]string
		reason string
	}{
		"no shared author": {func(t testing.TB) map[string]string {
			f := mixSagaTree(t)
			for _, id := range []string{"morgen", "abend"} {
				f["works/xx/"+id+"/work.json"] = workJSON(t, id, id, withLanguage("de"), withAuthors("otto-autor"))
			}
			return f
		}, "share no author"},
		"the moving half is two series": {func(t testing.TB) map[string]string {
			f := mixSagaTree(t)
			f["works/xx/morgen/work.json"] = workJSON(t, "morgen", "Morgen", withLanguage("de"), withAuthors("otto-autor"))
			return f
		}, "2 groups that share no author"},
		"the keeper states a translation": {func(t testing.TB) map[string]string {
			f := mixSagaTree(t)
			f["works/xx/dawn/work.json"] = workJSON(t, "dawn", "Dawn", testpack.WithCredits("otto-autor", "translator"))
			return f
		}, "state a translation"},
		"the moving half arrived first": {func(t testing.TB) map[string]string {
			f := mixSagaTree(t)
			f["works/xx/morgen/work.json"] = workJSON(t, "morgen", "Morgen", withLanguage("de"), testpack.WithAddedAt("2025-06-01"))
			return f
		}, "catalogued first"},
	} {
		t.Run(name, func(t *testing.T) {
			fd := onlyMix(t, runFixture(t, tc.files(t)), lMixSplit)
			if !fd.Propose.Advisory || !strings.Contains(fd.Propose.Reason, tc.reason) {
				t.Fatalf("proposal = %+v, want advisory naming %q", fd.Propose, tc.reason)
			}
		})
	}
}

// A series in an ordering family: the new series would state no ordering.
func TestLangMixOrderingFamilyIsAdvisory(t *testing.T) {
	files := mixSagaTree(t)
	files = mergeFiles(files, mixWork(t, "prequel", "en", "nate-narrator"), map[string]string{
		"series/sa/the-saga.json": testpack.WithFields(t, seriesJSON(t, "the-saga", "The Saga",
			"dawn@1", "dusk@2", "noon@3", "morgen@4", "abend@5"),
			map[string]any{"ordering": "chronological", "ordering_of": "the-saga-pub"}),
		"series/sa/the-saga-pub.json": testpack.WithFields(t, seriesJSON(t, "the-saga-pub", "The Saga (Publication)",
			"dawn@1", "prequel@2"), map[string]any{"ordering": "publication"}),
	})
	fd := onlyMix(t, runFixture(t, files), lMixSplit)
	if !fd.Propose.Advisory || !strings.Contains(fd.Propose.Reason, "ordering family") {
		t.Fatalf("proposal = %+v", fd.Propose)
	}
}

// A record another class proposes to change mechanically in the same audit withholds
// every L-MIX proposal touching it: the two would not commute.
func TestLangMixRespectsTheOtherClassesLocks(t *testing.T) {
	_, res := runFixtureAllowingProblems(t, mixSagaTree(t))
	ix := newIndex(res.Catalog)
	for name, locks := range map[string]mixLocks{
		"a merged work":       {works: map[string]string{"morgen": "W-DUP x"}},
		"a merged series":     {series: map[string]string{"the-saga": "SER-DUP y"}},
		"a restated position": {memberships: map[string]string{"the-saga@abend": "S-INTEGRITY z"}},
	} {
		t.Run(name, func(t *testing.T) {
			f, _ := detectLanguageMix(ix, locks)
			if len(f.rows) != 1 || !f.rows[0].Propose.Advisory || !strings.Contains(f.rows[0].Propose.Reason, "in this audit") {
				t.Fatalf("rows = %+v", f.rows)
			}
		})
	}
	// And the locks are read off the classes themselves.
	locks := newMixLocks(&findings{class: ClassWorkDup, rows: []Finding{{Key: "k",
		Propose: Proposal{Op: OpMergeWorks, Target: "a", Others: []string{"b"}}}}},
		&findings{class: ClassWorkNoSeries, rows: []Finding{{Key: "n",
			Propose: Proposal{Op: OpAddSeriesMember, Target: "w", Series: "s", To: "02"}}}},
		&findings{class: ClassSeriesDup, rows: []Finding{{Key: "adv",
			Propose: Proposal{Op: OpMergeSeries, Target: "x", Others: []string{"y"}, Advisory: true}}}})
	if locks.works["b"] == "" || locks.slots["s@2"] == "" || len(locks.series) != 0 {
		t.Errorf("locks = %+v", locks)
	}
}

// Two runs over one tree produce the same class, and SUMMARY.md carries the tallies.
func TestLangMixIsDeterministicAndTallied(t *testing.T) {
	files := mergeFiles(mixSagaTree(t), narratedTree(t))
	a, b := runFixture(t, files), runFixture(t, files)
	if !reflect.DeepEqual(a.class(ClassLangMix).rows, b.class(ClassLangMix).rows) || a.LangMix != b.LangMix {
		t.Fatal("two runs over one tree disagree")
	}
	if a.LangMix.MixedSeries != 2 || a.LangMix.Minority != 3 {
		t.Errorf("tally = %+v", a.LangMix)
	}
	s := summary(a)
	if !strings.Contains(s, "## L-MIX") || !strings.Contains(s, "series whose members state two or more languages | 2") {
		t.Errorf("SUMMARY.md lacks the L-MIX section or its tally")
	}
	assertProposalsConsistent(t, a)
}

// The new series a split would mint steps past a held and a retired slug, as the
// importer's chain does.
func TestLangMixNamesTheSlugTheImporterChainGives(t *testing.T) {
	files := mixSagaTree(t)
	files = mergeFiles(files, mixWork(t, "other", "en", "nate-narrator"), map[string]string{
		"series/sa/the-saga-2.json": seriesJSON(t, "the-saga-2", "The Saga", "other@1"),
	})
	rep := runFixtureRejectingWith(t, files, `{"people":{},"series":{"the-saga-3":"the-saga"},"works":{}}`)
	fd := onlyMix(t, rep, lMixSplit)
	if !strings.Contains(strings.Join(fd.Notes, " "), "at the-saga-4") {
		t.Errorf("notes = %v, want the-saga-4", fd.Notes)
	}
}

// Two series moving works into one slot of one target contend: both are withheld.
func TestLangMixContestedMovesAreAdvisory(t *testing.T) {
	files := mergeFiles(moveTree(t, "s1@1", "s2@2"), mixWork(t, "g1", "en", "nate-narrator"),
		mixWork(t, "g2", "en", "nate-narrator"), mixWork(t, "anderes-3", "de", "anna-sprecher"),
		map[string]string{
			"series/fa/fate-2.json": seriesJSON(t, "fate-2", "Fate", "g1@1", "g2@2", "anderes-3@3"),
		})
	rep := runFixture(t, files)
	got := subclassOf(t, rep, ClassLangMix, lMixTarget)
	if len(got) != 2 {
		t.Fatalf("want two moves, got %+v", got)
	}
	for _, fd := range got {
		if !fd.Propose.Advisory || !strings.Contains(fd.Propose.Reason, "claims the same place") {
			t.Errorf("%s: %+v, want advisory on the contest", fd.Key, fd.Propose)
		}
	}
	assertProposalsConsistent(t, rep)
}
