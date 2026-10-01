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
// sagaTree is the split shape: an English series holding two German volumes, with no
// German series of the name anywhere.
func mixSagaTree(t testing.TB) map[string]string {
	t.Helper()
	return mergeFiles(mixPeople(t),
		testpack.WorkFiles(t, "dawn", "en", "nate-narrator"), testpack.WorkFiles(t, "dusk", "en", "nate-narrator"),
		testpack.WorkFiles(t, "noon", "en", "nate-narrator"),
		testpack.WorkFiles(t, "morgen", "de", "anna-sprecher"), testpack.WorkFiles(t, "abend", "de", "anna-sprecher"),
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
	files = mergeFiles(files, testpack.WorkFiles(t, "aube", "fr", "nate-narrator"), map[string]string{
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
		testpack.WorkFiles(t, "c1", "en", "nate-narrator"), testpack.WorkFiles(t, "c2", "en", "nate-narrator"),
		testpack.WorkFiles(t, "chronik", "de", "anna-sprecher"), testpack.WorkFiles(t, "chronik-zwei", "de", "anna-sprecher"),
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
// name over the edition decoration's base. The author's three standalone English books
// keep her catalogue English, so the HOME signal stays quiet.
func moveTree(t testing.TB, germanMembers ...string) map[string]string {
	t.Helper()
	return mergeFiles(mixPeople(t),
		testpack.WorkFiles(t, "solo-1", "en", "nate-narrator"), testpack.WorkFiles(t, "solo-2", "en", "nate-narrator"),
		testpack.WorkFiles(t, "solo-3", "en", "nate-narrator"),
		testpack.WorkFiles(t, "f1", "en", "nate-narrator"), testpack.WorkFiles(t, "f2", "en", "nate-narrator"),
		testpack.WorkFiles(t, "schicksal-3", "de", "anna-sprecher"),
		testpack.WorkFiles(t, "s1", "de", "anna-sprecher"), testpack.WorkFiles(t, "s2", "de", "anna-sprecher"),
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
	files = mergeFiles(files, testpack.WorkFiles(t, "g1", "de", "anna-sprecher"), map[string]string{
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
		testpack.WorkFiles(t, "r1", "de", "anna-sprecher"), testpack.WorkFiles(t, "r2", "de", "anna-sprecher"),
		testpack.WorkFiles(t, "r3", "de", "anna-sprecher"), testpack.WorkFiles(t, "imperium", "en", "anna-sprecher"),
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
	files := mergeFiles(mixPeople(t), testpack.WorkFiles(t, "r1", "de", "anna-sprecher"), testpack.WorkFiles(t, "r2", "de", "anna-sprecher"),
		map[string]string{
			"works/xx/fixture-gem-red/work.json": workJSON(t, "fixture-gem-red", "Rubinrot"),
			"works/xx/fixture-gem-red/recordings/r.json": recJSON(t, "r", "fixture-gem-red", withNarrators("anna-sprecher"),
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
		testpack.WorkFiles(t, "t-en", "en", "nate-narrator", testpack.WithAddedAt("2026-02-01")),
		testpack.WorkFiles(t, "t-de", "de", "anna-sprecher", testpack.WithAddedAt("2026-01-01")),
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
		testpack.WorkFiles(t, "t-en", "en", "nate-narrator", testpack.WithAddedAt("2025-01-01")),
		testpack.WorkFiles(t, "t-de", "de", "anna-sprecher"),
		map[string]string{"series/ti/tie.json": seriesJSON(t, "tie", "Tie [German Edition]", "t-en@1", "t-de@2")})
	split := onlyMix(t, runFixture(t, base), lMixSplit)
	if split.Propose.From != "de" || !split.Propose.Advisory || !strings.Contains(split.Propose.Reason, "would be named") {
		t.Fatalf("split = %+v", split.Propose)
	}
	homed := mergeFiles(base, testpack.WorkFiles(t, "t-en-2", "en", "nate-narrator"), map[string]string{
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
	files = mergeFiles(files, testpack.WorkFiles(t, "prequel", "en", "nate-narrator"), map[string]string{
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
	files = mergeFiles(files, testpack.WorkFiles(t, "other", "en", "nate-narrator"), map[string]string{
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
	files := mergeFiles(moveTree(t, "s1@1", "s2@2"), testpack.WorkFiles(t, "g1", "en", "nate-narrator"),
		testpack.WorkFiles(t, "g2", "en", "nate-narrator"), testpack.WorkFiles(t, "anderes-3", "de", "anna-sprecher"),
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

// Membership order must not choose a different position or narration explanation,
// even in an invalid series that lists a work more than once.
func TestLangMixMemberOrderDoesNotChangeFindings(t *testing.T) {
	_, res := runFixtureAllowingProblems(t, mergeFiles(mixSagaTree(t), narratedTree(t)))
	for _, s := range res.Catalog.Series {
		if s.ID == "the-saga" {
			duplicate := s.Works[len(s.Works)-1]
			duplicate.Position = "99"
			s.Works = append(s.Works, duplicate)
		}
	}
	a := analyze(res)
	for _, s := range res.Catalog.Series {
		slices.Reverse(s.Works)
	}
	b := analyze(res)
	if !reflect.DeepEqual(a.Findings(ClassLangMix), b.Findings(ClassLangMix)) || a.LangMix != b.LangMix {
		t.Fatal("reversing membership order changed L-MIX findings or tallies")
	}
}

// homeTree is mixSagaTree under an author whose catalogue is mostly German: four
// standalone German books beside the saga's three English and two German volumes.
func homeTree(t testing.TB, author string) map[string]string {
	t.Helper()
	files := mixSagaTree(t)
	for _, id := range []string{"buch-1", "buch-2", "buch-3", "buch-4"} {
		files = mergeFiles(files, testpack.WorkFiles(t, id, "de", "anna-sprecher", withAuthors(author)))
	}
	return files
}

// Each CONTEST signal makes the majority split advisory, names itself in the reason and
// the notes, and adds the other orientation: German keeping the slug, the English
// members moving out.
func TestLangMixContestSignals(t *testing.T) {
	for name, tc := range map[string]struct {
		files  func(t testing.TB) map[string]string
		signal string
	}{
		"stated by a translator credit": {func(t testing.TB) map[string]string {
			f := mixSagaTree(t)
			f["works/xx/dawn/work.json"] = workJSON(t, "dawn", "Dawn", testpack.WithCredits("otto-autor", "translator"))
			return f
		}, signalStated},
		"stated by an edition decoration": {func(t testing.TB) map[string]string {
			f := mixSagaTree(t)
			f["works/xx/dawn/work.json"] = workJSON(t, "dawn", "Dawn (English Edition)")
			return f
		}, signalStated},
		"stated by a translation link": {func(t testing.TB) map[string]string {
			f := mixSagaTree(t)
			f["works/xx/dawn/work.json"] = testpack.WithFields(t, workJSON(t, "dawn", "Dawn"),
				map[string]any{"translation_of": []string{"morgen"}})
			return f
		}, signalStated},
		"collision": {func(t testing.TB) map[string]string {
			f := mixSagaTree(t)
			for _, id := range []string{"morgen", "abend"} {
				f["works/xx/"+id+"/work.json"] = workJSON(t, id, id, withLanguage("de"), withAuthors("otto-autor"))
			}
			return f
		}, signalCollision},
		"collision through a shared collective credit": {func(t testing.TB) map[string]string {
			f := mixSagaTree(t)
			f["people/va/various.json"] = personJSON(t, "various", "Various")
			for _, id := range []string{"dawn", "dusk", "noon"} {
				f["works/xx/"+id+"/work.json"] = workJSON(t, id, id, withAuthors("jane-doe", "various"))
			}
			for _, id := range []string{"morgen", "abend"} {
				f["works/xx/"+id+"/work.json"] = workJSON(t, id, id, withLanguage("de"), withAuthors("otto-autor", "various"))
			}
			return f
		}, signalCollision},
		"home": {func(t testing.TB) map[string]string { return homeTree(t, "jane-doe") }, signalHome},
	} {
		t.Run(name, func(t *testing.T) {
			rep := runFixture(t, tc.files(t))
			split := onlyMix(t, rep, lMixSplit)
			if !split.Propose.Advisory || !strings.Contains(split.Propose.Reason, "contested ("+tc.signal+")") {
				t.Fatalf("split = %+v, want advisory on the %s signal alone", split.Propose, tc.signal)
			}
			if !strings.Contains(strings.Join(split.Notes, " "), "contested, "+tc.signal+":") {
				t.Errorf("notes = %v, want the %s evidence", split.Notes, tc.signal)
			}
			alt := onlyMix(t, rep, lMixOtherKeeper)
			want := Proposal{Op: OpSplitSeries, Target: "the-saga", Others: []string{"dawn", "dusk", "noon"},
				Field: fieldLanguage, From: "de", To: "en", Advisory: true}
			got := alt.Propose
			got.Reason = ""
			if !reflect.DeepEqual(got, want) || alt.Key != "the-saga/en/keep-de" {
				t.Fatalf("other orientation = %s %+v, want %+v", alt.Key, alt.Propose, want)
			}
			if !strings.HasPrefix(alt.Propose.Reason, "the other orientation, for review: ") {
				t.Errorf("reason = %q", alt.Propose.Reason)
			}
			if rep.LangMix.Contested != 1 || rep.LangMix.OtherKeeperSplits != 1 {
				t.Errorf("tally = %+v", rep.LangMix)
			}
			assertProposalsConsistent(t, rep)
		})
	}
}

// The passing side of each signal: nothing contests the majority, so the split stays
// mechanical and no other orientation is proposed.
func TestLangMixUncontestedMajorityIsMechanical(t *testing.T) {
	for name, files := range map[string]func(t testing.TB) map[string]string{
		"one franchise": mixSagaTree,
		// A MOVING member stating a translation is what a translation looks like.
		"the minority states a translation": func(t testing.TB) map[string]string {
			f := mixSagaTree(t)
			f["works/xx/morgen/work.json"] = workJSON(t, "morgen", "Morgen", withLanguage("de"),
				testpack.WithCredits("otto-autor", "translator"))
			return f
		},
		// A translator credited as an author on one volume, whose own catalogue is all
		// German, does not outvote the author credited on every volume.
		"a translator credited as an author": func(t testing.TB) map[string]string {
			f := homeTree(t, "otto-autor")
			f["works/xx/morgen/work.json"] = workJSON(t, "morgen", "Morgen", withLanguage("de"), withAuthors("jane-doe", "otto-autor"))
			return f
		},
		// A collective credit's books say nothing about where an author writes.
		"a collective's German books": func(t testing.TB) map[string]string {
			f := mergeFiles(homeTree(t, "various"), map[string]string{"people/va/various.json": personJSON(t, "various", "Various")})
			for _, id := range []string{"morgen", "abend"} {
				f["works/xx/"+id+"/work.json"] = workJSON(t, id, id, withLanguage("de"), withAuthors("jane-doe", "various"))
			}
			return f
		},
	} {
		t.Run(name, func(t *testing.T) {
			rep := runFixture(t, files(t))
			if fd := onlyMix(t, rep, lMixSplit); fd.Propose.Advisory {
				t.Fatalf("split = %+v, want mechanical", fd.Propose)
			}
			if got := subclassOf(t, rep, ClassLangMix, lMixOtherKeeper); len(got) != 0 || rep.LangMix.Contested != 0 {
				t.Fatalf("an uncontested series proposed another orientation: %+v, tally %+v", got, rep.LangMix)
			}
		})
	}
}

// A contested series withholds its drops and the set-work-language review says so; only
// the split is proposed in another orientation.
func TestLangMixContestWithholdsDropsAndNotesTheLanguageReview(t *testing.T) {
	files := mergeFiles(mixPeople(t),
		testpack.WorkFiles(t, "c1", "en", "nate-narrator", withAuthors("otto-autor")),
		testpack.WorkFiles(t, "c2", "en", "nate-narrator", withAuthors("otto-autor")),
		testpack.WorkFiles(t, "chronik", "de", "anna-sprecher"), testpack.WorkFiles(t, "chronik-zwei", "de", "anna-sprecher"),
		map[string]string{
			"series/ch/chronicle.json":   seriesJSON(t, "chronicle", "The Chronicle", "c1@1", "c2@2", "chronik@3"),
			"series/di/die-chronik.json": seriesJSON(t, "die-chronik", "Die Chronik", "chronik@1", "chronik-zwei@2"),
		})
	rep := runFixture(t, files)
	drop := onlyMix(t, rep, lMixHomed)
	if !drop.Propose.Advisory || !strings.Contains(drop.Propose.Reason, "contested (collision)") {
		t.Fatalf("drop = %+v, want advisory on the contest", drop.Propose)
	}
	// Only the split is proposed in another orientation: German keeping the slug moves
	// the English members out, and the drop is not restated against that keeper.
	alt := onlyMix(t, rep, lMixOtherKeeper)
	if p := alt.Propose; p.From != "de" || p.To != "en" || !slices.Equal(p.Others, []string{"c1", "c2"}) {
		t.Errorf("other orientation = %+v", p)
	}

	narrated := narratedTree(t)
	narrated["works/xx/imperium/work.json"] = workJSON(t, "imperium", "Imperium", withAuthors("otto-autor"))
	lang := onlyMix(t, runFixture(t, narrated), lMixNarration)
	if !strings.Contains(strings.Join(lang.Notes, " "), "keeper language is contested: reihe (collision") {
		t.Errorf("notes = %v, want the contested series named", lang.Notes)
	}
}

// A tie decided by incumbency is proposed in the other orientation too.
func TestLangMixTieProposesTheOtherOrientation(t *testing.T) {
	files := mergeFiles(mixPeople(t),
		testpack.WorkFiles(t, "t-en", "en", "nate-narrator", testpack.WithAddedAt("2026-02-01")),
		testpack.WorkFiles(t, "t-de", "de", "anna-sprecher", testpack.WithAddedAt("2026-01-01")),
		map[string]string{"series/ti/tie.json": seriesJSON(t, "tie", "Tie", "t-en@1", "t-de@2")})
	rep := runFixture(t, files)
	alt := onlyMix(t, rep, lMixOtherKeeper)
	if p := alt.Propose; p.From != "en" || p.To != "de" || !slices.Equal(p.Others, []string{"t-de"}) || !p.Advisory ||
		!strings.Contains(p.Reason, "tie") {
		t.Fatalf("other orientation = %+v", p)
	}
	if rep.LangMix.Contested != 0 {
		t.Errorf("a tie is not a contested majority: %+v", rep.LangMix)
	}
}

// Accepting BOTH orientations of one series is refused: only one can be the series.
func TestReviewedRefusesTwoOrientationsOfOneSeries(t *testing.T) {
	files := mixSagaTree(t)
	for _, id := range []string{"morgen", "abend"} {
		files["works/xx/"+id+"/work.json"] = workJSON(t, id, id, withLanguage("de"), withAuthors("otto-autor"))
	}
	fresh := runFixture(t, files)
	split, alt := onlyMix(t, fresh, lMixSplit), onlyMix(t, fresh, lMixOtherKeeper)
	rep := runFixtureRejectingWith(t, files, "", review(alt.Propose, "accept"), review(split.Propose, "accept"))
	out := rep.Reviewed.Outcomes
	if len(out) != 2 {
		t.Fatalf("outcomes = %+v", out)
	}
	byFrom := map[string]decisionOutcome{}
	for _, o := range out {
		byFrom[o.Entry.From] = o
	}
	if byFrom["de"].Status != "accepted" || byFrom["en"].Status != "refused" || !strings.Contains(byFrom["en"].Why, "two orientations") {
		t.Fatalf("outcomes = %+v", out)
	}
	if fd := onlyMix(t, rep, lMixOtherKeeper); fd.Propose.Advisory {
		t.Errorf("the accepted orientation stayed advisory: %+v", fd.Propose)
	}
	assertProposalsConsistent(t, rep)
}

// Accepting a tie's drop (the incumbent orientation) beside the split that keeps the
// series for the dropped member's language (the other orientation) is refused: between
// them the two would empty the series (the Antonia Scott shape).
func TestReviewedRefusesADropAgainstTheOrientationItContradicts(t *testing.T) {
	files := mergeFiles(mixPeople(t),
		testpack.WorkFiles(t, "x-en", "en", "nate-narrator", testpack.WithAddedAt("2026-01-01")),
		testpack.WorkFiles(t, "x-es", "es", "anna-sprecher", testpack.WithAddedAt("2026-02-01")),
		testpack.WorkFiles(t, "y-es", "es", "anna-sprecher"),
		map[string]string{
			"series/ti/tie.json":      seriesJSON(t, "tie", "Tie", "x-en@1", "x-es@2"),
			"series/se/serie-es.json": seriesJSON(t, "serie-es", "Serie", "x-es@1", "y-es@2"),
		})
	fresh := runFixture(t, files)
	drop, alt := onlyMix(t, fresh, lMixHomed), onlyMix(t, fresh, lMixOtherKeeper)
	if alt.Propose.From != "es" || !slices.Equal(alt.Propose.Others, []string{"x-en"}) {
		t.Fatalf("other orientation = %+v", alt.Propose)
	}
	rep := runFixtureRejectingWith(t, files, "", review(alt.Propose, "accept"), review(drop.Propose, "accept"))
	byOp := map[string]decisionOutcome{}
	for _, o := range rep.Reviewed.Outcomes {
		byOp[o.Entry.Op] = o
	}
	if byOp[OpSplitSeries].Status != "accepted" || byOp[OpDropMembership].Status != "refused" ||
		!strings.Contains(byOp[OpDropMembership].Why, "keeps for es") {
		t.Fatalf("outcomes = %+v", rep.Reviewed.Outcomes)
	}
	assertProposalsConsistent(t, rep)
}

// A same-name series in the language an alternate would move out does not withhold
// that orientation: under a contest the halves may be two franchises, so the split is
// offered and the existing series is named (the zodiac-academy shape).
func TestLangMixOtherKeeperIsOfferedBesideATarget(t *testing.T) {
	f := mixSagaTree(t)
	for _, id := range []string{"morgen", "abend"} {
		f["works/xx/"+id+"/work.json"] = workJSON(t, id, id, withLanguage("de"), withAuthors("otto-autor"))
	}
	f = mergeFiles(f, testpack.WorkFiles(t, "eve", "en", "nate-narrator"),
		map[string]string{"series/sa/the-saga-english-edition.json": seriesJSON(t, "the-saga-english-edition", "The Saga [English Edition]", "eve@9")})
	rep := runFixture(t, f)
	alt := onlyMix(t, rep, lMixOtherKeeper)
	if alt.Key != "the-saga/en/keep-de" || !alt.Propose.Advisory {
		t.Fatalf("other orientation = %s %+v", alt.Key, alt.Propose)
	}
	if !strings.Contains(strings.Join(alt.Notes, " "), "the-saga-english-edition") {
		t.Errorf("notes = %v, want the existing en series named", alt.Notes)
	}
	assertProposalsConsistent(t, rep)
}
