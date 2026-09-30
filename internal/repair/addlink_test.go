package repair

import (
	"slices"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/audit"
	"github.com/kodestar/audiosilo-meta/pkg/check"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// addlink_test.go covers add-work-link and add-series-link, the write half of the
// audit's T-LINK class: both subclasses applied through the real detectors, the wave's
// idempotency, and every refusal branch.

// editionTree holds one proposal of each T-LINK subclass: "The Saga [German Edition]"
// beside "The Saga", and "A Game of Fate (French Edition)" beside "A Game of Fate".
func editionTree(t testing.TB) map[string]string {
	t.Helper()
	return map[string]string{
		"people/ja/jane-doe.json":           personJSON(t, "jane-doe", "Jane Doe"),
		"people/na/nate-narrator.json":      personJSON(t, "nate-narrator", "Nate Narrator"),
		"works/da/dawn/work.json":           workJSON(t, "dawn", "Dawn"),
		"works/du/dusk/work.json":           workJSON(t, "dusk", "Dusk"),
		"works/mo/morgen/work.json":         workJSON(t, "morgen", "Morgen", withLanguage("de")),
		"works/ab/abend/work.json":          workJSON(t, "abend", "Abend", withLanguage("de")),
		"series/sa/the-saga.json":           seriesJSON(t, "the-saga", "The Saga", "dawn@1", "dusk@2"),
		"series/sa/the-saga-german.json":    seriesJSON(t, "the-saga-german", "The Saga [German Edition]", "morgen@1", "abend@2"),
		"works/ag/a-game-of-fate/work.json": workJSON(t, "a-game-of-fate", "A Game of Fate"),
		"works/ag/a-game-of-fate-fr/work.json": workJSON(t, "a-game-of-fate-fr", "A Game of Fate (French Edition)",
			withLanguage("fr")),
	}
}

// Both subclasses land as one sorted translation_of member each, over a CORE-profile
// tree with no --community: a link never reads the works-community layer, so the
// sidecarUnknown refusal (merge-works only) must not reach it. A second run proposes
// nothing, because a linked pair is not proposed again.
func TestAddLinkAppliesBothSubclassesAndIsIdempotent(t *testing.T) {
	data := seedTree(t, editionTree(t))

	linkOps := []string{audit.OpAddWorkLink, audit.OpAddSeriesLink}
	rep := run(t, Options{DataDir: data, Ops: linkOps, Profile: pack.ProfileCore, Write: true})
	if len(rep.Applied) != 2 || len(rep.Refused) != 0 {
		t.Fatalf("applied %+v, refused %+v; want both links", rep.Applied, rep.Refused)
	}
	for _, a := range rep.Applied {
		if _, ok := linkOpFamily[a.Op]; !ok || a.Field != check.FieldTranslationOf {
			t.Errorf("applied record %+v does not name a link op and the field", a)
		}
	}
	if got := workEntry(t, data, "a-game-of-fate-fr").Strs("translation_of"); !slices.Equal(got, []string{"a-game-of-fate"}) {
		t.Errorf("work translation_of = %v", got)
	}
	if got := readEntry(t, data, pack.FamilySeries, "the-saga-german").Strs("translation_of"); !slices.Equal(got, []string{"the-saga"}) {
		t.Errorf("series translation_of = %v", got)
	}
	if loadRedirects(t, data).Len() != 0 {
		t.Error("a link recorded a tombstone; nothing was retired")
	}

	again := run(t, Options{DataDir: data, Ops: linkOps, Profile: pack.ProfileCore})
	if again.Considered != 0 || len(again.Applied) != 0 {
		t.Fatalf("a second run over the linked tree considered %d and applied %+v", again.Considered, again.Applied)
	}
	res := check.LoadProfile(data, pack.ProfileCore)
	for _, fd := range audit.Analyze(res).Findings(audit.ClassTransLink) {
		t.Errorf("the fresh audit still proposes %s -> %s", fd.Propose.Target, fd.Propose.To)
	}
}

// --op selects a wave: the op names the family, so the series edition alone.
func TestAddLinkOpSelectsTheWave(t *testing.T) {
	data := seedTree(t, editionTree(t))
	rep := run(t, Options{DataDir: data, Ops: []string{audit.OpAddSeriesLink}})
	if len(rep.Applied) != 1 || rep.Applied[0].Target != "the-saga-german" || rep.Applied[0].Op != audit.OpAddSeriesLink {
		t.Fatalf("applied %+v, want the series edition alone", rep.Applied)
	}
}

// A reviewed worklist narrows the wave like any other op.
func TestAddLinkFollowsTheWorklist(t *testing.T) {
	data := seedTree(t, editionTree(t))
	report := auditReport(t, data)
	rep := run(t, Options{DataDir: data, ReportDir: report, Ops: []string{audit.OpAddWorkLink, audit.OpAddSeriesLink}, Write: true})
	if len(rep.Applied) != 2 || len(rep.Refused) != 0 {
		t.Fatalf("applied %+v, refused %+v", rep.Applied, rep.Refused)
	}
	// The same reviewed report over the linked tree: both records are now stale.
	again := run(t, Options{DataDir: data, ReportDir: report, Ops: []string{audit.OpAddWorkLink, audit.OpAddSeriesLink}})
	if len(again.Applied) != 0 || len(again.Refused) != 2 {
		t.Fatalf("applied %+v, refused %+v; want both refused as stale", again.Applied, again.Refused)
	}
	for _, r := range again.Refused {
		if r.Category != CatStaleProposal {
			t.Errorf("refusal %+v, want %s", r, CatStaleProposal)
		}
	}
}

// linkFinding is a link op's proposal as a literal, for the refusal branches.
func linkFinding(op, target, from, to string) audit.Finding {
	return audit.Finding{
		Class: audit.ClassTransLink, Subclass: "work-edition", Key: target,
		Propose: audit.Proposal{Op: op, Target: target,
			Field: check.FieldTranslationOf, From: from, To: to},
	}
}

// The record moved since the proposal: it now states an original the proposal did not
// see, so the union is not the reviewed decision.
func TestAddLinkRefusesAStaleFrom(t *testing.T) {
	files := editionTree(t)
	files["works/ot/other/work.json"] = workJSON(t, "other", "Other")
	files["works/ag/a-game-of-fate-fr/work.json"] = workJSON(t, "a-game-of-fate-fr", "A Game of Fate (French Edition)",
		withLanguage("fr"), withTranslationOf("other"))
	data := seedTree(t, files)
	rn, tx := planFixture(t, data)
	err := rn.addLink(tx, linkFinding(audit.OpAddWorkLink, "a-game-of-fate-fr", "", "a-game-of-fate"))
	assertRefusal(t, err, CatStaleValue, "now states translation_of [other]")
}

func TestAddLinkRefusesWhatItCannotRead(t *testing.T) {
	data := seedTree(t, editionTree(t))
	for _, tc := range []struct {
		name     string
		fd       audit.Finding
		category Category
		mentions string
	}{
		{"missing original", linkFinding(audit.OpAddWorkLink, "a-game-of-fate-fr", "", "no-such-work"), CatMissing, "no-such-work"},
		{"missing translation", linkFinding(audit.OpAddWorkLink, "no-such-work", "", "a-game-of-fate"), CatMissing, "no-such-work"},
		{"not a link op", linkFinding("add-person-link", "a-game-of-fate-fr", "", "a-game-of-fate"), CatMalformed, "not a link op"},
		{"self link", linkFinding(audit.OpAddWorkLink, "a-game-of-fate", "", "a-game-of-fate"), CatMalformed, "to itself"},
		{"no original", linkFinding(audit.OpAddWorkLink, "a-game-of-fate-fr", "", ""), CatNoValue, "names no original"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rn, tx := planFixture(t, data)
			assertRefusal(t, rn.addLink(tx, tc.fd), tc.category, tc.mentions)
		})
	}
	rn, tx := planFixture(t, data)
	fd := linkFinding(audit.OpAddWorkLink, "a-game-of-fate-fr", "", "a-game-of-fate")
	fd.Propose.Field = "ordering_of"
	assertRefusal(t, rn.addLink(tx, fd), CatMalformed, "translation_of links only")
}

// Two proposals in one run are judged against the STAGED plan: once the first links the
// French edition onto the English record, a second making that English record a
// translation too would chain, and pkg/check's rule of record refuses it before a byte
// is written.
func TestAddLinkSecondProposalSeesTheFirst(t *testing.T) {
	files := editionTree(t)
	files["works/ag/a-game-of-fate-es/work.json"] = workJSON(t, "a-game-of-fate-es", "Un juego del destino", withLanguage("es"))
	data := seedTree(t, files)
	rn, tx := planFixture(t, data)
	if err := rn.addLink(tx, linkFinding(audit.OpAddWorkLink, "a-game-of-fate-fr", "", "a-game-of-fate")); err != nil {
		t.Fatalf("the first link: %v", err)
	}
	if err := tx.commit("first"); err != nil {
		t.Fatal(err)
	}
	second := rn.plan.begin()
	err := rn.addLink(second, linkFinding(audit.OpAddWorkLink, "a-game-of-fate", "", "a-game-of-fate-es"))
	assertRefusal(t, err, CatTranslationLink, "one hop from its original")
}

// A link whose two sides share a language is pkg/check's same-language problem.
func TestAddLinkRefusesASameLanguageLink(t *testing.T) {
	files := editionTree(t)
	files["works/ag/a-game-of-fate-fr/work.json"] = workJSON(t, "a-game-of-fate-fr", "A Game of Fate (French Edition)")
	data := seedTree(t, files)
	rn, tx := planFixture(t, data)
	err := rn.addLink(tx, linkFinding(audit.OpAddWorkLink, "a-game-of-fate-fr", "", "a-game-of-fate"))
	assertRefusal(t, err, CatTranslationLink, "same language")
}

// Every work's language is indexed off the load, so a series link - on any series,
// named by any op - judges a derived language without parsing a pack.
func TestWorkLanguagesIndexesEveryWork(t *testing.T) {
	data := seedTree(t, editionTree(t))
	rn, _ := planFixture(t, data)
	for id, want := range map[string]string{"dawn": "en", "morgen": "de", "a-game-of-fate-fr": "fr"} {
		if got, ok := rn.plan.workLang[id]; !ok || got != want {
			t.Errorf("workLang[%s] = %q, %v; want %q", id, got, ok, want)
		}
	}
}
