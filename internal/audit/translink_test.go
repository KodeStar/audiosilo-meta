package audit

import (
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/testpack"
)

// translink_test.go pins T-LINK: every rule with a fixture it proposes on and one it
// must not.

var (
	withCreditsOpt  = testpack.WithCredits
	withTranslation = testpack.WithTranslationOf
	withSubtitleOpt = testpack.WithSubtitle
	withFields      = testpack.WithFields
)

// linkPeople are the people every T-LINK fixture credits.
func linkPeople(t testing.TB) map[string]string {
	t.Helper()
	return map[string]string{
		"people/ja/jane-doe.json":         personJSON(t, "jane-doe", "Jane Doe"),
		"people/jo/john-roe.json":         personJSON(t, "john-roe", "John Roe"),
		"people/ti/tina-translator.json":  personJSON(t, "tina-translator", "Tina Translator"),
		"people/na/nate-narrator.json":    personJSON(t, "nate-narrator", "Nate Narrator"),
		"people/ja/jane-l-doe.json":       personJSON(t, "jane-l-doe", "Jane L Doe"),
		"people/ot/other-translator.json": personJSON(t, "other-translator", "Other Translator"),
	}
}

// sagaTree is the series-edition calibration: an English series and its German edition
// named "<name> [German Edition]", one author across both.
func sagaTree(t testing.TB) map[string]string {
	t.Helper()
	files := linkPeople(t)
	for k, v := range map[string]string{
		"works/da/dawn/work.json":        workJSON(t, "dawn", "Dawn"),
		"works/du/dusk/work.json":        workJSON(t, "dusk", "Dusk"),
		"works/mo/morgen/work.json":      workJSON(t, "morgen", "Morgen", withLanguage("de")),
		"works/ab/abend/work.json":       workJSON(t, "abend", "Abend", withLanguage("de")),
		"series/sa/the-saga.json":        seriesJSON(t, "the-saga", "The Saga", "dawn@1", "dusk@2"),
		"series/sa/the-saga-german.json": seriesJSON(t, "the-saga-german", "The Saga [German Edition]", "morgen@1", "abend@2"),
	} {
		files[k] = v
	}
	return files
}

// linkFinding is the T-LINK record keyed key, or nil.
func linkFinding(t testing.TB, rep *Report, key string) *Finding {
	t.Helper()
	for _, fd := range classOf(t, rep, ClassTransLink) {
		if fd.Key == key {
			return &fd
		}
	}
	return nil
}

func TestSeriesEditionProposesTheOneSameAuthorSeries(t *testing.T) {
	rep := runFixture(t, sagaTree(t))
	fd := linkFinding(t, rep, "the-saga-german")
	if fd == nil {
		t.Fatalf("no proposal for the German edition: %+v", classOf(t, rep, ClassTransLink))
	}
	p := fd.Propose
	if fd.Subclass != tLinkSeries || p.Op != OpAddSeriesLink || p.Target != "the-saga-german" ||
		p.To != "the-saga" || p.Field != "translation_of" || p.From != "" || p.Advisory {
		t.Errorf("proposal = %+v (subclass %s), want a mechanical series add-link onto the-saga", p, fd.Subclass)
	}
	if !strings.Contains(fd.Action, "state series the-saga-german translation_of as including the-saga") {
		t.Errorf("action = %q", fd.Action)
	}
	assertProposalsConsistent(t, rep)
}

func TestSeriesEditionNeedsASharedAuthor(t *testing.T) {
	files := sagaTree(t)
	files["works/mo/morgen/work.json"] = workJSON(t, "morgen", "Morgen", withLanguage("de"), withAuthors("john-roe"))
	files["works/ab/abend/work.json"] = workJSON(t, "abend", "Abend", withLanguage("de"), withAuthors("john-roe"))
	rep := runFixture(t, files)
	if fd := linkFinding(t, rep, "the-saga-german"); fd != nil {
		t.Fatalf("proposed %+v over two author-disjoint series", fd.Propose)
	}
	if rep.Links.SeriesNoCandidate != 1 {
		t.Errorf("no-candidate tally = %d, want 1", rep.Links.SeriesNoCandidate)
	}
}

// A forked person record is not a missing author: the audit's own samePersonSpelling
// (a middle-name insertion here) reads "Jane L Doe" as Jane Doe.
func TestSeriesEditionReadsAForkedAuthorAsOnePerson(t *testing.T) {
	files := sagaTree(t)
	files["works/mo/morgen/work.json"] = workJSON(t, "morgen", "Morgen", withLanguage("de"), withAuthors("jane-l-doe"))
	files["works/ab/abend/work.json"] = workJSON(t, "abend", "Abend", withLanguage("de"), withAuthors("jane-l-doe"))
	rep := runFixture(t, files)
	if fd := linkFinding(t, rep, "the-saga-german"); fd == nil || fd.Propose.Advisory {
		t.Fatalf("a forked author record cost the proposal: %+v", fd)
	}
}

func TestSeriesEditionTwoCandidatesIsNoProposal(t *testing.T) {
	files := sagaTree(t)
	files["works/ev/eve/work.json"] = workJSON(t, "eve", "Eve")
	files["series/sa/the-saga-2.json"] = seriesJSON(t, "the-saga-2", "The Saga", "eve@1")
	rep := runFixture(t, files)
	if fd := linkFinding(t, rep, "the-saga-german"); fd != nil {
		t.Fatalf("proposed %+v with two same-named same-author candidates", fd.Propose)
	}
	if rep.Links.SeriesAmbiguous != 1 {
		t.Errorf("ambiguous tally = %d, want 1", rep.Links.SeriesAmbiguous)
	}
}

// The decoration must state the language the series' members are in: a "[French
// Edition]" over German works states nothing the data agrees with.
func TestSeriesEditionDecorationMustMatchTheDerivedLanguage(t *testing.T) {
	files := sagaTree(t)
	files["series/sa/the-saga-german.json"] = seriesJSON(t, "the-saga-german", "The Saga [French Edition]", "morgen@1", "abend@2")
	rep := runFixture(t, files)
	if fd := linkFinding(t, rep, "the-saga-german"); fd != nil {
		t.Fatalf("proposed %+v on a decoration its members contradict", fd.Propose)
	}
	if rep.Links.SeriesLanguageSkips != 1 {
		t.Errorf("language-skip tally = %d, want 1", rep.Links.SeriesLanguageSkips)
	}
}

func TestSeriesEditionOntoANonEnglishBaseIsAdvisory(t *testing.T) {
	files := sagaTree(t)
	files["works/da/dawn/work.json"] = workJSON(t, "dawn", "Dawn", withLanguage("fr"))
	files["works/du/dusk/work.json"] = workJSON(t, "dusk", "Dusk", withLanguage("fr"))
	rep := runFixture(t, files)
	fd := linkFinding(t, rep, "the-saga-german")
	if fd == nil || !fd.Propose.Advisory || !strings.Contains(fd.Propose.Reason, "derives fr, not en") {
		t.Fatalf("proposal = %+v, want an advisory naming the base's language", fd)
	}
}

func TestSeriesEditionOntoATranslatedBaseIsAdvisory(t *testing.T) {
	files := sagaTree(t)
	files["works/da/dawn/work.json"] = workJSON(t, "dawn", "Dawn",
		withAuthors("jane-doe", "tina-translator"), withCreditsOpt("tina-translator", "translator"))
	rep := runFixture(t, files)
	fd := linkFinding(t, rep, "the-saga-german")
	if fd == nil || !fd.Propose.Advisory || !strings.Contains(fd.Propose.Reason, "credited to a translator") {
		t.Fatalf("proposal = %+v, want an advisory naming the translator credit", fd)
	}
}

func TestSeriesEditionAlreadyLinkedIsNotProposedAgain(t *testing.T) {
	files := sagaTree(t)
	files["series/sa/the-saga-german.json"] = withFields(t, files["series/sa/the-saga-german.json"],
		map[string]any{"translation_of": []string{"the-saga"}})
	rep := runFixture(t, files)
	if fd := linkFinding(t, rep, "the-saga-german"); fd != nil {
		t.Fatalf("re-proposed a link the series states: %+v", fd.Propose)
	}
}

// An original that is itself a translation would make a chain pkg/check refuses.
func TestSeriesEditionOntoATranslationIsAdvisory(t *testing.T) {
	files := sagaTree(t)
	files["works/or/orig/work.json"] = workJSON(t, "orig", "Orig", withLanguage("es"))
	files["series/or/el-origen.json"] = seriesJSON(t, "el-origen", "El Origen", "orig@1")
	files["series/sa/the-saga.json"] = withFields(t, files["series/sa/the-saga.json"],
		map[string]any{"translation_of": []string{"el-origen"}})
	rep := runFixture(t, files)
	fd := linkFinding(t, rep, "the-saga-german")
	if fd == nil || !fd.Propose.Advisory || !strings.Contains(fd.Propose.Reason, "chain rule") {
		t.Fatalf("proposal = %+v, want an advisory naming the chain", fd)
	}
}

// fateTree is the work-edition calibration: a French work whose title is the English
// original's title left untranslated plus "(French Edition)".
func fateTree(t testing.TB) map[string]string {
	t.Helper()
	files := linkPeople(t)
	files["works/ag/a-game-of-fate/work.json"] = workJSON(t, "a-game-of-fate", "A Game of Fate")
	files["works/ag/a-game-of-fate-fr/work.json"] = workJSON(t, "a-game-of-fate-fr", "A Game of Fate (French Edition)",
		withLanguage("fr"), withAuthors("jane-doe", "tina-translator"), withCreditsOpt("tina-translator", "translator"))
	return files
}

func TestWorkEditionProposesTheOneSameBookInAnotherLanguage(t *testing.T) {
	rep := runFixture(t, fateTree(t))
	fd := linkFinding(t, rep, "a-game-of-fate-fr")
	if fd == nil {
		t.Fatalf("no proposal: %+v", classOf(t, rep, ClassTransLink))
	}
	p := fd.Propose
	if fd.Subclass != tLinkWork || p.Op != OpAddWorkLink || p.To != "a-game-of-fate" || p.Advisory {
		t.Errorf("proposal = %+v (subclass %s), want a mechanical works add-link onto a-game-of-fate", p, fd.Subclass)
	}
	assertProposalsConsistent(t, rep)
}

// A decoration in the subtitle states the edition as well as one in the title.
func TestWorkEditionReadsTheSubtitle(t *testing.T) {
	files := fateTree(t)
	files["works/ag/a-game-of-fate-fr/work.json"] = workJSON(t, "a-game-of-fate-fr", "A Game of Fate",
		withLanguage("fr"), withSubtitleOpt("(French Edition)"))
	rep := runFixture(t, files)
	if fd := linkFinding(t, rep, "a-game-of-fate-fr"); fd == nil || fd.Propose.Advisory {
		t.Fatalf("proposal = %+v, want a mechanical one off the subtitle", fd)
	}
}

// The identity key and its volume statement read the title alone, so a subtitle
// stating a volume - on either side - keeps the link advisory.
func TestWorkEditionSubtitleVolumeIsAdvisory(t *testing.T) {
	files := fateTree(t)
	files["works/ag/a-game-of-fate-fr/work.json"] = workJSON(t, "a-game-of-fate-fr", "A Game of Fate",
		withLanguage("fr"), withSubtitleOpt("Volume 2 (French Edition)"))
	if fd := linkFinding(t, runFixture(t, files), "a-game-of-fate-fr"); fd == nil || !fd.Propose.Advisory {
		t.Errorf("translation subtitle states a volume: proposal = %+v, want an advisory one", fd)
	}
	files = fateTree(t)
	files["works/ag/a-game-of-fate/work.json"] = workJSON(t, "a-game-of-fate", "A Game of Fate", withSubtitleOpt("Book 2"))
	if fd := linkFinding(t, runFixture(t, files), "a-game-of-fate-fr"); fd == nil || !fd.Propose.Advisory {
		t.Errorf("original subtitle states a volume: proposal = %+v, want an advisory one", fd)
	}
}

// A decoration naming ANOTHER language than the work's own states nothing about it.
func TestWorkEditionDecorationMustBeTheWorksOwnLanguage(t *testing.T) {
	files := fateTree(t)
	files["works/ag/a-game-of-fate-fr/work.json"] = workJSON(t, "a-game-of-fate-fr", "A Game of Fate (German Edition)",
		withLanguage("fr"))
	rep := runFixture(t, files)
	if fd := linkFinding(t, rep, "a-game-of-fate-fr"); fd != nil {
		t.Fatalf("proposed %+v off a decoration in another language than the work's", fd.Propose)
	}
}

// A translator credit alone is not direction evidence: madame-bovary (en) carries one
// because it is itself a translation from French.
func TestWorkEditionTranslatorCreditAloneProposesNothing(t *testing.T) {
	files := linkPeople(t)
	files["works/ma/madame-bovary/work.json"] = workJSON(t, "madame-bovary", "Madame Bovary",
		withAuthors("jane-doe", "tina-translator"), withCreditsOpt("tina-translator", "translator"))
	files["works/ma/madame-bovary-fr/work.json"] = workJSON(t, "madame-bovary-fr", "Madame Bovary", withLanguage("fr"))
	rep := runFixture(t, files)
	if got := classOf(t, rep, ClassTransLink); len(got) != 0 {
		t.Fatalf("proposed on a translator credit alone: %+v", got)
	}
}

// A candidate that STATES it is a translation - a translator credit - is not an original.
func TestWorkEditionNeverTakesATranslationAsTheOriginal(t *testing.T) {
	files := fateTree(t)
	files["works/ag/a-game-of-fate/work.json"] = workJSON(t, "a-game-of-fate", "A Game of Fate",
		withAuthors("jane-doe", "other-translator"), withCreditsOpt("other-translator", "translator"))
	rep := runFixture(t, files)
	if fd := linkFinding(t, rep, "a-game-of-fate-fr"); fd != nil {
		t.Fatalf("proposed %+v onto a translator-credited candidate", fd.Propose)
	}
	if rep.Links.WorksNoCandidate != 1 {
		t.Errorf("no-candidate tally = %d, want 1", rep.Links.WorksNoCandidate)
	}
}

func TestWorkEditionTwoOriginalsIsNoProposal(t *testing.T) {
	files := fateTree(t)
	files["works/ag/a-game-of-fate-es/work.json"] = workJSON(t, "a-game-of-fate-es", "A Game of Fate", withLanguage("es"))
	rep := runFixture(t, files)
	if fd := linkFinding(t, rep, "a-game-of-fate-fr"); fd != nil {
		t.Fatalf("proposed %+v with an English and a Spanish candidate", fd.Propose)
	}
	if rep.Links.WorksAmbiguous != 1 {
		t.Errorf("ambiguous tally = %d, want 1", rep.Links.WorksAmbiguous)
	}
}

// The two shapes the cruder first measurement paired wrongly: a later volume and a
// collection, both reaching the plain title through the normalized key. Neither may be
// mechanical.
func TestWorkEditionTitleMustBeTheOriginalsUntranslated(t *testing.T) {
	for _, tc := range []struct{ name, title string }{
		{"stated volume", "Families First, Volume 2 (German Edition)"},
		{"collection", "Families First (Die komplette Trilogie) [German Edition]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := linkPeople(t)
			files["works/fa/families-first/work.json"] = workJSON(t, "families-first", "Families First")
			files["works/fa/families-first-de/work.json"] = workJSON(t, "families-first-de", tc.title, withLanguage("de"))
			rep := runFixture(t, files)
			// The proposal must EXIST: both titles meet the original on the identity key,
			// and a detector that silently produced nothing would otherwise pass.
			fd := linkFinding(t, rep, "families-first-de")
			if fd == nil {
				t.Fatalf("no proposal for %q, want an advisory one", tc.title)
			}
			if !fd.Propose.Advisory {
				t.Fatalf("mechanical proposal %+v for %q", fd.Propose, tc.title)
			}
			if !strings.Contains(fd.Propose.Reason, "differ beyond the edition decoration") {
				t.Errorf("reason = %q", fd.Propose.Reason)
			}
		})
	}
}

func TestWorkEditionOntoADramatizationIsAdvisory(t *testing.T) {
	files := linkPeople(t)
	files["works/ci/city-of-thorns-da/work.json"] = workJSON(t, "city-of-thorns-da", "City of Thorns (Dramatized Adaptation)")
	files["works/ci/city-of-thorns-de/work.json"] = workJSON(t, "city-of-thorns-de", "City of Thorns (German edition)",
		withLanguage("de"))
	rep := runFixture(t, files)
	fd := linkFinding(t, rep, "city-of-thorns-de")
	if fd == nil || !fd.Propose.Advisory || !strings.Contains(fd.Propose.Reason, "dramatized or adapted") {
		t.Fatalf("proposal = %+v, want an advisory naming the dramatization", fd)
	}
}

func TestWorkEditionAlreadyLinkedIsNotProposedAgain(t *testing.T) {
	files := fateTree(t)
	files["works/ag/a-game-of-fate-fr/work.json"] = workJSON(t, "a-game-of-fate-fr", "A Game of Fate (French Edition)",
		withLanguage("fr"), withTranslation("a-game-of-fate"))
	rep := runFixture(t, files)
	if fd := linkFinding(t, rep, "a-game-of-fate-fr"); fd != nil {
		t.Fatalf("re-proposed a link the work states: %+v", fd.Propose)
	}
}

// A translation already naming a DIFFERENT original is a second-original (omnibus) claim
// a human makes.
func TestWorkEditionOntoARecordStatingAnotherOriginalIsAdvisory(t *testing.T) {
	files := fateTree(t)
	files["works/ot/other-book/work.json"] = workJSON(t, "other-book", "Other Book")
	files["works/ag/a-game-of-fate-fr/work.json"] = workJSON(t, "a-game-of-fate-fr", "A Game of Fate (French Edition)",
		withLanguage("fr"), withTranslation("other-book"))
	rep := runFixture(t, files)
	fd := linkFinding(t, rep, "a-game-of-fate-fr")
	if fd == nil || !fd.Propose.Advisory || fd.Propose.From != "other-book" ||
		!strings.Contains(fd.Propose.Reason, "already states translation_of") {
		t.Fatalf("proposal = %+v, want an advisory carrying the stated original as From", fd)
	}
}

// A translation that is itself somebody's original would, once linked, turn that other
// record's link into a chain - pkg/check's rule of record, asked over the catalogue with
// the proposed link overlaid, is what sees it.
func TestWorkEditionThatWouldMakeAChainIsAdvisory(t *testing.T) {
	files := fateTree(t)
	files["works/ag/a-game-of-fate-es/work.json"] = workJSON(t, "a-game-of-fate-es", "Un juego del destino",
		withLanguage("es"), withTranslation("a-game-of-fate-fr"))
	rep := runFixture(t, files)
	fd := linkFinding(t, rep, "a-game-of-fate-fr")
	if fd == nil || !fd.Propose.Advisory || !strings.Contains(fd.Propose.Reason, "pkg/check would refuse the link") {
		t.Fatalf("proposal = %+v, want an advisory naming the link rule", fd)
	}
}
