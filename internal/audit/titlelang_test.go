package audit

import (
	"slices"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/testpack"
)

// titlelang_test.go covers the title-language subclass: each signal with a passing and
// a violating fixture, the narrator verdict, and the fold into narration-contradicts.

// titledWork seeds a work with a title of its own and one recording in its language.
func titledWork(t testing.TB, id, title, lang, narrator string, opts ...testpack.WorkOpt) map[string]string {
	t.Helper()
	return map[string]string{
		"works/xx/" + id + "/work.json": workJSON(t, id, title, append([]testpack.WorkOpt{withLanguage(lang)}, opts...)...),
		"works/xx/" + id + "/recordings/r-" + id + ".json": recJSON(t, "r-"+id, id, withNarrators(narrator),
			testpack.WithRecLanguage(lang)),
	}
}

// titleProposals is the title-language records, keyed by work.
func titleProposals(t testing.TB, rep *Report) map[string]Proposal {
	t.Helper()
	out := map[string]Proposal{}
	for _, fd := range subclassOf(t, rep, ClassLangMix, lMixTitle) {
		out[fd.Key] = fd.Propose
	}
	return out
}

func wantTitleProposal(t testing.TB, got map[string]Proposal, work, from, to string) Proposal {
	t.Helper()
	p, ok := got[work]
	if !ok {
		t.Fatalf("no title-language proposal for %s: %+v", work, got)
	}
	if p.Op != OpSetWorkLanguage || p.Target != work || p.Field != fieldLanguage || p.From != from || p.To != to || !p.Advisory {
		t.Fatalf("%s proposal = %+v, want an advisory set-work-language %s -> %s", work, p, from, to)
	}
	return p
}

// An own-language edition decoration of another language than the work's tag proposes
// that language, over the whole catalogue - and a course naming the language it
// teaches, or a decoration agreeing with the tag, proposes nothing.
func TestTitleLanguageReadsAnEditionStatement(t *testing.T) {
	rep := runFixture(t, mergeFiles(mixPeople(t),
		titledWork(t, "the-gambler", "The Gambler [Persian Edition]", "en", "nate-narrator"),
		titledWork(t, "learn-german", "Learn German: By Reading Fantasy (German Edition)", "en", "nate-narrator"),
		titledWork(t, "steelheart", "Steelheart [German Edition]", "de", "anna-sprecher"),
	))
	got := titleProposals(t, rep)
	p := wantTitleProposal(t, got, "the-gambler", "en", "fa")
	if !strings.Contains(p.Reason, `states the fa edition`) || !strings.Contains(p.Reason, "settle nothing") {
		t.Errorf("reason = %q, want the statement and the narrator verdict", p.Reason)
	}
	if len(got) != 1 {
		t.Errorf("the course and the agreeing decoration proposed: %+v", got)
	}
	if rep.LangMix.TitleEdition != 1 || rep.LangMix.TitleCourse != 1 || rep.LangMix.TitleProposals != 1 {
		t.Errorf("tally = %+v", rep.LangMix)
	}
}

// Narrator evidence only withholds: narrators who record in another language are named
// in the reason, and the proposal is still made, advisory.
func TestTitleLanguageNamesANarratorContradiction(t *testing.T) {
	rep := runFixture(t, mergeFiles(mixPeople(t),
		testpack.WorkFiles(t, "r1", "de", "anna-sprecher"), testpack.WorkFiles(t, "r2", "de", "anna-sprecher"),
		titledWork(t, "the-gambler", "The Gambler [Persian Edition]", "en", "anna-sprecher"),
	))
	p := wantTitleProposal(t, titleProposals(t, rep), "the-gambler", "en", "fa")
	if !strings.Contains(p.Reason, "record in de") || !strings.Contains(p.Reason, "contradicts fa") {
		t.Errorf("reason = %q, want the narrators' contradiction named", p.Reason)
	}
}

// A bracket gloss on an en-tagged member of a non-English series proposes the series'
// language - for a tie, the one language every other member states. The other member,
// tagged es, is never read: English words are no evidence against a non-English tag.
func TestTitleLanguageReadsASeriesGloss(t *testing.T) {
	files := mergeFiles(mixPeople(t),
		// Catalogued first, so the incumbency rule keeps the tie for en: the language
		// read is still the other member's.
		titledWork(t, "la-odisea", "La Odisea [The Odyssey]", "en", "nate-narrator", testpack.WithAddedAt("2025-01-01")),
		titledWork(t, "iliada", "Ilíada [The Iliad]", "es", "anna-sprecher"),
		map[string]string{"series/il/iliada-odisea.json": seriesJSON(t, "iliada-odisea", "Ilíada & Odisea", "la-odisea@1", "iliada@2")},
	)
	rep := runFixture(t, files)
	got := titleProposals(t, rep)
	p := wantTitleProposal(t, got, "la-odisea", "en", "es")
	if !strings.Contains(p.Reason, `glossed [The Odyssey]`) {
		t.Errorf("reason = %q, want the gloss named", p.Reason)
	}
	if len(got) != 1 || rep.LangMix.TitleSeries != 1 {
		t.Errorf("proposals = %+v, tally %+v", got, rep.LangMix)
	}
}

// What is not a gloss: a bracket naming the recording's publisher, a format bracket, a
// head carrying an English function word, a statement naming a language, and a gloss
// outside a mixed series (M. Robinson's English "El Diablo [The Devil]").
func TestTitleLanguageGlossVetoes(t *testing.T) {
	for _, tc := range []struct {
		name, title string
		series      bool
	}{
		{"publisher", "Faust [Fixture Audio]", true},
		{"tie-in", "Adrift [Movie Tie-in]", true},
		{"collection", "Faust [The Dragon Box Set]", true},
		{"bundle", "Small Talk [5-in-1]", true},
		{"english head", "A Christmas Carol [Una Novela]", true},
		{"language statement", "Faust [UK English]", true}, // the course rule: it names a language
		{"number", "Gravity [1980033501]", true},
		{"no mixed series", "El Diablo [The Devil]", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := mergeFiles(mixPeople(t),
				titledWork(t, "member", tc.title, "en", "nate-narrator"),
				titledWork(t, "band-eins", "Band Eins", "de", "anna-sprecher"),
				titledWork(t, "band-zwei", "Band Zwei", "de", "anna-sprecher"))
			if tc.series {
				files["series/re/reihe.json"] = seriesJSON(t, "reihe", "Reihe", "band-eins@1", "band-zwei@2", "member@3")
			}
			if got := titleProposals(t, runFixture(t, files)); len(got) != 0 {
				t.Errorf("%q proposed %+v", tc.title, got)
			}
		})
	}
}

// Function words of the series' language - two distinct ones, and no English one -
// propose it without a gloss (the Italian "Il Cuore Spezzato Di Arelium").
func TestTitleLanguageReadsSeriesFunctionWords(t *testing.T) {
	series := func(t testing.TB, title string) map[string]string {
		return mergeFiles(mixPeople(t),
			titledWork(t, "member", title, "en", "nate-narrator"),
			titledWork(t, "la-mano", "La Mano Cinerea Di Kessrin", "it", "anna-sprecher"),
			titledWork(t, "la-spada", "La Spada", "it", "anna-sprecher"),
			map[string]string{"series/la/la-guerra.json": seriesJSON(t, "la-guerra", "La Guerra Dei Dodici",
				"member@1", "la-mano@2", "la-spada@3")})
	}
	p := wantTitleProposal(t, titleProposals(t, runFixture(t, series(t, "Il Cuore Spezzato Di Arelium"))), "member", "en", "it")
	if !strings.Contains(p.Reason, "it function words (di, il)") {
		t.Errorf("reason = %q, want the words named", p.Reason)
	}
	for _, title := range []string{
		"Alex Cross Must Die",                 // no Italian word at all
		"La La Land",                          // one distinct word is not two
		"The Cuore of Il Spezzato Di Arelium", // English function words beside the Italian
		"Learn Italian: Il Cuore Di Roma",     // a course names its language
	} {
		if got := titleProposals(t, runFixture(t, series(t, title))); len(got) != 0 {
			t.Errorf("%q proposed %+v", title, got)
		}
	}
}

// Only an en tag is read. A non-English member keeping an English title is a
// translation's ordinary shape ("A Dance of Lies", French, in a tied English/French
// series), and a gloss on one ("Tagebuch Eines Ninja Kindes [Diary of a Ninja Kid]",
// German, in a Spanish series) glosses a title in its own tag's language.
func TestTitleLanguageReadsAnEnTagOnly(t *testing.T) {
	rep := runFixture(t, mergeFiles(mixPeople(t),
		titledWork(t, "a-dance-of-lies", "A Dance of Lies", "fr", "anna-sprecher"),
		titledWork(t, "to-dance-with-death", "To Dance with Death", "en", "nate-narrator"),
		map[string]string{"series/ed/edge.json": seriesJSON(t, "edge", "Edge Of Glass", "a-dance-of-lies@1", "to-dance-with-death@2")},
		titledWork(t, "tagebuch", "Tagebuch Eines Ninja Kindes [Diary of a Ninja Kid]", "de", "anna-sprecher"),
		titledWork(t, "diario-1", "Diario de un Niño Ninja", "es", "nate-narrator"),
		titledWork(t, "diario-2", "Una Invasión Extraterrestre", "es", "nate-narrator"),
		map[string]string{"series/di/diario.json": seriesJSON(t, "diario", "Diario de un Niño Ninja", "diario-1@1", "diario-2@2", "tagebuch@3")},
	))
	if got := titleProposals(t, rep); len(got) != 0 {
		t.Errorf("proposed %+v", got)
	}
}

// A work narration-contradicts already proposes keeps that one record, the title
// evidence folded into its notes: one finding per work.
func TestTitleLanguageFoldsIntoANarrationRecord(t *testing.T) {
	files := narratedTree(t)
	files["works/xx/imperium/work.json"] = workJSON(t, "imperium", "Das Imperium der Nacht", withLanguage("en"))
	rep := runFixture(t, files)
	if got := titleProposals(t, rep); len(got) != 0 {
		t.Fatalf("a second record for one work: %+v", got)
	}
	fd := onlyMix(t, rep, lMixNarration)
	if fd.Propose.Op != OpSetWorkLanguage || fd.Propose.To != "de" {
		t.Fatalf("narration proposal = %+v", fd.Propose)
	}
	if !slices.ContainsFunc(fd.Notes, func(n string) bool { return strings.HasPrefix(n, "-> de (title): ") }) {
		t.Errorf("notes = %q, want the title evidence folded in", fd.Notes)
	}
}
