package repair

import (
	"slices"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/audit"
	"github.com/kodestar/audiosilo-meta/internal/testpack"
	"github.com/kodestar/audiosilo-meta/pkg/check"
	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
	"github.com/kodestar/audiosilo-meta/pkg/redirects"
)

// membership_test.go covers the write half of the audit's L-MIX class: the three
// mechanical membership ops applied through the real detectors (and the wave's
// idempotency), the split's slug chain, set-work-language planned directly (the audit
// never proposes it mechanically), and every refusal branch.

var mixOps = []string{audit.OpDropMembership, audit.OpMoveMembership, audit.OpSplitSeries}

func mixPeopleFiles(t testing.TB) map[string]string {
	t.Helper()
	return map[string]string{
		"people/ja/jane-doe.json":      personJSON(t, "jane-doe", "Jane Doe"),
		"people/na/nate-narrator.json": personJSON(t, "nate-narrator", "Nate Narrator"),
		"people/an/anna-sprecher.json": personJSON(t, "anna-sprecher", "Anna Sprecher"),
	}
}

// mixWorkFiles seeds a work in lang with one recording in the same language.
func mixWorkFiles(t testing.TB, id, lang string, opts ...workOpt) map[string]string {
	t.Helper()
	narrator := "nate-narrator"
	if lang != "en" {
		narrator = "anna-sprecher"
	}
	return testpack.WorkFiles(t, id, lang, narrator, opts...)
}

func mergeMaps(parts ...map[string]string) map[string]string {
	out := map[string]string{}
	for _, p := range parts {
		for k, v := range p {
			out[k] = v
		}
	}
	return out
}

// languageMixTree holds one proposal of each mechanical subclass:
//
//   - the-saga (en) holds two German volumes and no German series of the name exists:
//     a split;
//   - chronicle (en) holds chronik, which also sits in die-chronik (de): a drop;
//   - fate (en) holds schicksal-3, and "Fate [German Edition]" (de) has slot 3 free: a
//     move.
func languageMixTree(t testing.TB) map[string]string {
	t.Helper()
	files := mixPeopleFiles(t)
	for id, lang := range map[string]string{
		"dawn": "en", "dusk": "en", "noon": "en", "morgen": "de", "abend": "de",
		"c1": "en", "c2": "en", "chronik": "de", "chronik-zwei": "de",
		"f1": "en", "f2": "en", "schicksal-3": "de", "s1": "de", "s2": "de",
	} {
		files = mergeMaps(files, mixWorkFiles(t, id, lang))
	}
	files["series/sa/the-saga.json"] = seriesJSON(t, "the-saga", "The Saga", "dawn@1", "dusk@2", "noon@3", "morgen@4", "abend@5")
	files["series/ch/chronicle.json"] = seriesJSON(t, "chronicle", "The Chronicle", "c1@1", "c2@2", "chronik@3")
	files["series/di/die-chronik.json"] = seriesJSON(t, "die-chronik", "Die Chronik", "chronik@1", "chronik-zwei@2")
	files["series/fa/fate.json"] = seriesJSON(t, "fate", "Fate", "f1@1", "f2@2", "schicksal-3@3")
	files["series/fa/fate-german.json"] = seriesJSON(t, "fate-german", "Fate [German Edition]", "s1@1", "s2@2")
	return files
}

func seriesMembers(t testing.TB, data, slug string) []model.SeriesWork {
	t.Helper()
	return readEntry(t, data, pack.FamilySeries, slug).SeriesWorks()
}

func memberList(ws []model.SeriesWork) []string {
	out := make([]string, 0, len(ws))
	for _, sw := range ws {
		out = append(out, sw.Work+"@"+sw.Position)
	}
	slices.Sort(out)
	return out
}

// Each op lands as the audit proposed it, the report's applied records carry the
// proposals verbatim, the tree validates (Run fails the run otherwise), and a second
// run proposes none of it again.
func TestLanguageMixOpsApplyAndAreIdempotent(t *testing.T) {
	data := gitRepo(t, languageMixTree(t))
	res := check.LoadProfile(data, pack.ProfileCore)
	proposed := map[string]audit.Proposal{}
	for _, fd := range audit.Analyze(res).Findings(audit.ClassLangMix) {
		if !fd.Propose.Advisory {
			proposed[fd.Key] = fd.Propose
		}
	}
	if len(proposed) != 3 {
		t.Fatalf("the audit proposes %d mechanical L-MIX records, want 3: %+v", len(proposed), proposed)
	}

	rep := run(t, Options{DataDir: data, Ops: mixOps, Profile: pack.ProfileCore, Write: true})
	if len(rep.Applied) != 3 || len(rep.Refused) != 0 {
		t.Fatalf("applied %+v, refused %+v", rep.Applied, rep.Refused)
	}
	// The report and the audit agree: each applied record IS the proposal.
	for _, a := range rep.Applied {
		p, ok := proposed[a.Key]
		if !ok || a.Op != p.Op || a.Target != p.Target || a.Series != p.Series || a.Field != p.Field ||
			a.From != p.From || a.To != p.To || !slices.Equal(a.Others, p.Others) {
			t.Errorf("applied %+v does not match the proposal %+v", a, p)
		}
	}

	if got := memberList(seriesMembers(t, data, "chronicle")); !slices.Equal(got, []string{"c1@1", "c2@2"}) {
		t.Errorf("chronicle = %v, want chronik dropped", got)
	}
	if got := memberList(seriesMembers(t, data, "die-chronik")); !slices.Equal(got, []string{"chronik-zwei@2", "chronik@1"}) {
		t.Errorf("die-chronik = %v, untouched", got)
	}
	if got := memberList(seriesMembers(t, data, "fate")); !slices.Equal(got, []string{"f1@1", "f2@2"}) {
		t.Errorf("fate = %v", got)
	}
	if got := memberList(seriesMembers(t, data, "fate-german")); !slices.Equal(got, []string{"s1@1", "s2@2", "schicksal-3@3"}) {
		t.Errorf("fate-german = %v, want schicksal-3 at 3", got)
	}
	if got := memberList(seriesMembers(t, data, "the-saga")); !slices.Equal(got, []string{"dawn@1", "dusk@2", "noon@3"}) {
		t.Errorf("the-saga = %v", got)
	}
	split := readEntry(t, data, pack.FamilySeries, "the-saga-2")
	if got := memberList(split.SeriesWorks()); !slices.Equal(got, []string{"abend@5", "morgen@4"}) {
		t.Errorf("the-saga-2 = %v, want the German volumes at their positions", got)
	}
	orig := readEntry(t, data, pack.FamilySeries, "the-saga")
	if split.Str("name") != "The Saga" || split.Str("license") != "CC0-1.0" || string(split["sources"]) != string(orig["sources"]) {
		t.Errorf("the new series = %s", split.MustRaw())
	}
	for _, k := range []string{"xref", "ordering", "ordering_of", "translation_of", "authors"} {
		if split.Has(k) {
			t.Errorf("the new series states %s", k)
		}
	}
	if loadRedirects(t, data).Len() != 0 {
		t.Error("a membership op recorded a tombstone; nothing was retired")
	}

	again := run(t, Options{DataDir: data, Ops: mixOps, Profile: pack.ProfileCore})
	if again.Considered != 0 || len(again.Applied) != 0 {
		t.Fatalf("a second run considered %d and applied %+v", again.Considered, again.Applied)
	}
	for _, fd := range audit.Analyze(check.LoadProfile(data, pack.ProfileCore)).Findings(audit.ClassLangMix) {
		t.Errorf("the fresh audit still reports %s %s", fd.Subclass, fd.Key)
	}
}

// A reviewed worklist narrows the wave, and the same report over the repaired tree is
// stale throughout.
func TestLanguageMixFollowsTheWorklist(t *testing.T) {
	data := gitRepo(t, languageMixTree(t))
	report := auditReport(t, data)
	rep := run(t, Options{DataDir: data, ReportDir: report, Ops: mixOps, Write: true})
	if len(rep.Applied) != 3 || len(rep.Refused) != 0 {
		t.Fatalf("applied %+v, refused %+v", rep.Applied, rep.Refused)
	}
	again := run(t, Options{DataDir: data, ReportDir: report, Ops: mixOps})
	if len(again.Applied) != 0 || len(again.Refused) != 3 {
		t.Fatalf("applied %+v, refused %+v; want all three stale", again.Applied, again.Refused)
	}
	for _, r := range again.Refused {
		if r.Category != CatStaleProposal {
			t.Errorf("refusal %+v, want %s", r, CatStaleProposal)
		}
	}
}

// Two splits of one name in one run take two slugs off the chain, and a retired
// candidate is stepped past: the-saga-2 is held, the-saga-3 is a tombstone.
func TestSplitSeriesRederivesTheSlugAgainstThePlan(t *testing.T) {
	files := languageMixTree(t)
	for id, lang := range map[string]string{"ember": "en", "flame": "en", "glut": "de"} {
		files = mergeMaps(files, mixWorkFiles(t, id, lang))
	}
	files["series/sa/the-saga-2.json"] = seriesJSON(t, "the-saga-2", "The Saga", "ember@1", "flame@2", "glut@3")
	data := seedTree(t, files)
	table := model.NewRedirects()
	table[model.RedirectSeries]["the-saga-3"] = "the-saga"
	if err := redirects.Write(data, table); err != nil {
		t.Fatal(err)
	}
	rep := run(t, Options{DataDir: data, Ops: []string{audit.OpSplitSeries}})
	if len(rep.Applied) != 2 {
		t.Fatalf("applied %+v, refused %+v", rep.Applied, rep.Refused)
	}
	var minted []string
	for _, a := range rep.Applied {
		for _, n := range a.Notes {
			if i := strings.Index(n, "to the new series "); i >= 0 {
				minted = append(minted, strings.Fields(n[i+len("to the new series "):])[0])
			}
		}
	}
	if !slices.Equal(minted, []string{"the-saga-4", "the-saga-5"}) {
		t.Errorf("minted %v, want the-saga-4 then the-saga-5", minted)
	}
}

// ---- refusals ----------------------------------------------------------------

func mixFinding(op, key string, p audit.Proposal) audit.Finding {
	p.Op = op
	return audit.Finding{Class: audit.ClassLangMix, Key: key, Propose: p}
}

func TestMembershipOpsRefuse(t *testing.T) {
	data := seedTree(t, languageMixTree(t))
	for name, tc := range map[string]struct {
		fd       audit.Finding
		category Category
		mentions string
	}{
		"drop at a moved position": {mixFinding(audit.OpDropMembership, "k", audit.Proposal{Target: "chronik", Series: "chronicle",
			Field: fieldPosition, From: "9", Others: []string{"die-chronik"}}), CatStaleValue, `at position "9"`},
		"drop with no home": {mixFinding(audit.OpDropMembership, "k", audit.Proposal{Target: "chronik", Series: "chronicle",
			Field: fieldPosition, From: "3", Others: []string{"fate-german"}}), CatStaleValue, "no longer sits in a series of its language"},
		"drop of a majority member": {mixFinding(audit.OpDropMembership, "k", audit.Proposal{Target: "c1", Series: "chronicle",
			Field: fieldPosition, From: "1", Others: []string{"die-chronik"}}), CatStaleValue, "now derives en"},
		"drop naming no position": {mixFinding(audit.OpDropMembership, "k", audit.Proposal{Series: "chronicle",
			Field: "work", From: "chronik"}), CatMalformed, "no work"},
		"move into a taken slot": {mixFinding(audit.OpMoveMembership, "k", audit.Proposal{Target: "schicksal-3", Series: "fate",
			Field: fieldPosition, From: "3", To: "2", Others: []string{"fate-german"}}), CatPositionConflict, "held by s2"},
		"move into another language": {mixFinding(audit.OpMoveMembership, "k", audit.Proposal{Target: "schicksal-3", Series: "fate",
			Field: fieldPosition, From: "3", To: "3", Others: []string{"the-saga"}}), CatStaleValue, "now derives \"en\""},
		"move to two series": {mixFinding(audit.OpMoveMembership, "k", audit.Proposal{Target: "schicksal-3", Series: "fate",
			Field: fieldPosition, From: "3", To: "3", Others: []string{"fate-german", "die-chronik"}}), CatMalformed, "ONE series"},
		"move to a non-canonical position": {mixFinding(audit.OpMoveMembership, "k", audit.Proposal{Target: "schicksal-3", Series: "fate",
			Field: fieldPosition, From: "3", To: "3.0", Others: []string{"fate-german"}}), CatMalformed, "canonical"},
		"split of a member it no longer lists": {mixFinding(audit.OpSplitSeries, "k", audit.Proposal{Target: "the-saga",
			Others: []string{"chronik"}, Field: fieldLanguage, From: "en", To: "de"}), CatStaleValue, "no longer lists chronik"},
		"split of a member in another language": {mixFinding(audit.OpSplitSeries, "k", audit.Proposal{Target: "the-saga",
			Others: []string{"dawn"}, Field: fieldLanguage, From: "en", To: "de"}), CatStaleValue, "now states \"en\""},
		"split of every member": {mixFinding(audit.OpSplitSeries, "k", audit.Proposal{Target: "fate-german",
			Others: []string{"s1", "s2"}, Field: fieldLanguage, From: "en", To: "de"}), CatStaleValue, "left with no members"},
		"split naming no language": {mixFinding(audit.OpSplitSeries, "k", audit.Proposal{Target: "the-saga",
			Others: []string{"morgen"}, Field: fieldLanguage}), CatNoValue, "no language"},
		"split of a retired series": {mixFinding(audit.OpSplitSeries, "k", audit.Proposal{Target: "gone",
			Others: []string{"morgen"}, Field: fieldLanguage, To: "de"}), CatMissing, "no series"},
	} {
		t.Run(name, func(t *testing.T) {
			rn, tx := planFixture(t, data)
			var err error
			switch tc.fd.Propose.Op {
			case audit.OpDropMembership:
				err = rn.dropMembership(tx, tc.fd)
			case audit.OpMoveMembership:
				err = rn.moveMembership(tx, tc.fd)
			case audit.OpSplitSeries:
				err = rn.splitSeries(tx, tc.fd)
			}
			assertRefusal(t, err, tc.category, tc.mentions)
		})
	}
}

// ---- a drop naming no home ----------------------------------------------------

// homelessDrop is a reviewed assertion's drop: no home in Others.
func homelessDrop(target, series, from string) audit.Finding {
	return audit.Finding{Class: audit.ClassSeriesInteg, Subclass: audit.SubclassAsserted, Key: "asserted/" + target + "@" + series,
		Propose: audit.Proposal{Op: audit.OpDropMembership, Target: target, Series: series, Field: fieldPosition, From: from,
			Reason: "asserted by review: an omnibus is not a volume"}}
}

// A drop naming no home applies only as a reviewed assertion: no language is judged,
// so a MAJORITY member leaves its series on the review's word, with the review's own
// reason in the note, and the work itself is not written. The same shape in any other
// subclass fails closed as malformed, and a drop naming homes keeps L-MIX's check: a
// home that no longer lists the work refuses it.
func TestHomelessDropAppliesOnlyAsAnAssertion(t *testing.T) {
	data := seedTree(t, languageMixTree(t))
	rn, tx := planFixture(t, data)
	if err := rn.dropMembership(tx, homelessDrop("c1", "chronicle", "1")); err != nil {
		t.Fatal(err)
	}
	if got := memberList(tx.series.puts["chronicle"].SeriesWorks()); !slices.Equal(got, []string{"c2@2", "chronik@3"}) {
		t.Errorf("chronicle = %v", got)
	}
	if want := `dropped c1 (position "1") from series chronicle: asserted by review: an omnibus is not a volume`; !slices.Equal(tx.notes, []string{want}) {
		t.Errorf("notes = %v, want %q", tx.notes, want)
	}
	if len(tx.works.puts) != 0 {
		t.Errorf("a drop wrote works: %v", tx.works.puts)
	}
	rn, tx = planFixture(t, data)
	homeless := mixFinding(audit.OpDropMembership, "k", audit.Proposal{Target: "c1", Series: "chronicle", Field: fieldPosition, From: "1"})
	assertRefusal(t, rn.dropMembership(tx, homeless), CatMalformed, "only ever a reviewed assertion")
	rn, tx = planFixture(t, data)
	lmix := mixFinding(audit.OpDropMembership, "k", audit.Proposal{Target: "chronik", Series: "chronicle", Field: fieldPosition,
		From: "3", Others: []string{"fate-german"}})
	assertRefusal(t, rn.dropMembership(tx, lmix), CatStaleValue, "no longer sits in a series of its language")
}

func TestHomelessDropRefuses(t *testing.T) {
	data := seedTree(t, mergeMaps(languageMixTree(t), map[string]string{
		"series/so/solo.json": seriesJSON(t, "solo", "Solo", "dawn@1"),
	}))
	for name, tc := range map[string]struct {
		fd       audit.Finding
		category Category
		mentions string
	}{
		"at a moved position":   {homelessDrop("chronik", "chronicle", "2"), CatStaleValue, `no longer lists chronik at position "2"`},
		"of a work not listed":  {homelessDrop("dawn", "chronicle", "1"), CatStaleValue, "no longer lists dawn"},
		"of the last member":    {homelessDrop("dawn", "solo", "1"), CatStaleValue, "with no members"},
		"from a retired series": {homelessDrop("dawn", "gone", "1"), CatMissing, "no series"},
		"naming no position":    {func() audit.Finding { f := homelessDrop("dawn", "solo", "1"); f.Propose.Field = ""; return f }(), CatMalformed, "no position"},
		// S-INTEGRITY's dangling-member drop names no work and is advisory; if it ever
		// reached the pass it would still be malformed, never an unconditional drop.
		"a dangling member": {audit.Finding{Class: audit.ClassSeriesInteg, Key: "chronicle", Propose: audit.Proposal{
			Op: audit.OpDropMembership, Series: "chronicle", Field: "work", From: "chronik"}}, CatMalformed, "no work"},
	} {
		t.Run(name, func(t *testing.T) {
			rn, tx := planFixture(t, data)
			assertRefusal(t, rn.dropMembership(tx, tc.fd), tc.category, tc.mentions)
		})
	}
}

// A membership is found by its SLOT (importer.SameSlot), the plan-time rule everywhere
// else: a proposal at "3" finds a work the series lists at "03", for a drop and a move.
func TestMembershipOpsFindTheSlotNotTheSpelling(t *testing.T) {
	data := seedTree(t, mergeMaps(languageMixTree(t), map[string]string{
		"series/ch/chronicle.json": seriesJSON(t, "chronicle", "The Chronicle", "c1@1", "c2@2", "chronik@03"),
		"series/fa/fate.json":      seriesJSON(t, "fate", "Fate", "f1@1", "f2@2", "schicksal-3@03"),
	}))
	rn, tx := planFixture(t, data)
	drop := mixFinding(audit.OpDropMembership, "k", audit.Proposal{Target: "chronik", Series: "chronicle", Field: fieldPosition,
		From: "3", Others: []string{"die-chronik"}})
	if err := rn.dropMembership(tx, drop); err != nil {
		t.Fatal(err)
	}
	if got := memberList(tx.series.puts["chronicle"].SeriesWorks()); !slices.Equal(got, []string{"c1@1", "c2@2"}) {
		t.Errorf("chronicle = %v", got)
	}
	rn, tx = planFixture(t, data)
	move := mixFinding(audit.OpMoveMembership, "k", audit.Proposal{Target: "schicksal-3", Series: "fate", Field: fieldPosition,
		From: "3", To: "3", Others: []string{"fate-german"}})
	if err := rn.moveMembership(tx, move); err != nil {
		t.Fatal(err)
	}
	if got := memberList(tx.series.puts["fate"].SeriesWorks()); !slices.Equal(got, []string{"f1@1", "f2@2"}) {
		t.Errorf("fate = %v", got)
	}
}

// ---- set-work-language --------------------------------------------------------

func languageFinding(target, from, to string) audit.Finding {
	return mixFinding(audit.OpSetWorkLanguage, target, audit.Proposal{Target: target, Field: fieldLanguage, From: from, To: to})
}

// The minority shape (work and recordings en, reset to de) and the Rubinrot shape (work
// en, recording de already) both land; nothing else about the work changes.
func TestSetWorkLanguageAppliesWithItsRecordings(t *testing.T) {
	files := mergeMaps(mixPeopleFiles(t), mixWorkFiles(t, "imperium", "en"), map[string]string{
		"works/xx/rubinrot/work.json": workJSON(t, "rubinrot", "Rubinrot"),
		"works/xx/rubinrot/recordings/r.json": recJSON(t, "r", "rubinrot", withNarrators("anna-sprecher"),
			testpack.WithRecLanguage("de")),
	})
	data := seedTree(t, files)
	for _, tc := range []struct{ work, rec string }{{"imperium", "r-imperium"}, {"rubinrot", "r"}} {
		rn, tx := planFixture(t, data)
		if err := rn.setWorkLanguage(tx, languageFinding(tc.work, "en", "de")); err != nil {
			t.Fatalf("%s: %v", tc.work, err)
		}
		e := tx.works.puts[tc.work]
		if e.Str("language") != "de" {
			t.Errorf("%s language = %q", tc.work, e.Str("language"))
		}
		recs, err := e.Recordings()
		if err != nil {
			t.Fatal(err)
		}
		if got := recs[tc.rec].Str("language"); got != "de" {
			t.Errorf("%s recording language = %q", tc.work, got)
		}
		if e.Str("title") == "" || !e.Has("sources") {
			t.Errorf("%s lost its other members: %s", tc.work, e.MustRaw())
		}
	}
}

func TestSetWorkLanguageRefuses(t *testing.T) {
	files := mergeMaps(mixPeopleFiles(t), mixWorkFiles(t, "imperium", "en"), mixWorkFiles(t, "original", "de"), map[string]string{
		"works/xx/mixed/work.json":              workJSON(t, "mixed", "Mixed"),
		"works/xx/mixed/recordings/r-en.json":   recJSON(t, "r-en", "mixed"),
		"works/xx/mixed/recordings/r-fr.json":   recJSON(t, "r-fr", "mixed", testpack.WithRecLanguage("fr")),
		"works/xx/translated/work.json":         workJSON(t, "translated", "Translated", withTranslationOf("original")),
		"works/xx/translated/recordings/r.json": recJSON(t, "r", "translated"),
	})
	data := seedTree(t, files)
	for name, tc := range map[string]struct {
		fd       audit.Finding
		category Category
		mentions string
	}{
		"a stale From":             {languageFinding("imperium", "fr", "de"), CatStaleValue, `now states the language "en"`},
		"a third-language record":  {languageFinding("mixed", "en", "de"), CatStaleValue, `states "fr"`},
		"no value":                 {languageFinding("imperium", "en", ""), CatNoValue, "no language"},
		"the same language":        {languageFinding("imperium", "en", "en"), CatMalformed, "the one it states"},
		"a same-language original": {languageFinding("translated", "en", "de"), CatTranslationLink, "same language"},
		"a missing work":           {languageFinding("nobody", "en", "de"), CatMissing, "no work"},
	} {
		t.Run(name, func(t *testing.T) {
			rn, tx := planFixture(t, data)
			assertRefusal(t, rn.setWorkLanguage(tx, tc.fd), tc.category, tc.mentions)
		})
	}
}

// A reviewed decision may ACCEPT exactly the ops this pass can apply: an accepted op
// with no planner would be counted as made mechanical while nothing carries it out.
func TestAcceptableOpsAreAppliable(t *testing.T) {
	for _, op := range audit.Ops() {
		if appliableOps[op] != audit.AcceptableOp(op) {
			t.Errorf("%s: appliable=%v, acceptable=%v; want them equal", op, appliableOps[op], audit.AcceptableOp(op))
		}
	}
	for _, op := range AppliableOps() {
		if !slices.Contains(audit.Ops(), op) {
			t.Errorf("%s is appliable but the audit never emits it", op)
		}
	}
}
