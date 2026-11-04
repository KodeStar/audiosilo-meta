package audit

import (
	"fmt"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/testpack"
	"github.com/kodestar/audiosilo-meta/pkg/canonical"
)

// repackage_test.go pins R-REPACKAGE: each signal with a fixture it admits and one it
// refuses, the one-work scope, and the reviewed decision as the record of the link.

// lvProject makes a fixture recording a LibriVox project of id, read in sections
// sections (each a minute, contiguous).
func lvProject(id, sections int) testpack.RecOpt {
	return func(m map[string]any) {
		m["librivox_id"] = id
		m["publisher"] = "LibriVox"
		delete(m, "asin")
		delete(m, "release_date")
		var chs []map[string]any
		for i := range sections {
			chs = append(chs, map[string]any{"title": fmt.Sprintf("Section %d", i+1), "start_ms": i * 60000, "length_ms": 60000})
		}
		if len(chs) > 0 {
			m["chapters"] = chs
		}
	}
}

// repackPeople are the people every fixture below credits.
func repackPeople(t testing.TB) map[string]string {
	return map[string]string{
		"people/ja/jane-doe.json":     personJSON(t, "jane-doe", "Jane Doe"),
		"people/da/david-barnes.json": personJSON(t, "david-barnes", "David Barnes"),
		"people/ot/other-reader.json": personJSON(t, "other-reader", "Other Reader"),
	}
}

// repackWork adds work id holding LibriVox project lvID (recording "david-barnes",
// lvRuntime minutes in sections sections) and the retail recording "david-barnes-2016"
// built by retail.
func repackWork(t testing.TB, files map[string]string, id string, lvID, lvRuntime, sections int, retail ...testpack.RecOpt) {
	t.Helper()
	files["works/xx/"+id+"/work.json"] = workJSON(t, id, "Book "+id)
	files["works/xx/"+id+"/recordings/david-barnes.json"] = recJSON(t, "david-barnes", id,
		withNarrators("david-barnes"), withRuntime(lvRuntime), lvProject(lvID, sections))
	files["works/xx/"+id+"/recordings/david-barnes-2016.json"] = recJSON(t, "david-barnes-2016", id,
		append([]testpack.RecOpt{withNarrators("david-barnes"), testpack.WithPublisher("Paperless")}, retail...)...)
}

// metamorphosis is the worked example: David Barnes's LibriVox project 527 (154 min,
// 3 sections) beside Paperless's 152-minute edition of it. Its work is not at the real
// slug: the audit reads the embedded reviewed.json, whose accepted link on the real
// pair would withdraw the fixture's proposal.
func metamorphosis(t testing.TB, retail ...testpack.RecOpt) map[string]string {
	t.Helper()
	files := repackPeople(t)
	repackWork(t, files, "repack-metamorphosis", 527, 154, 3, append([]testpack.RecOpt{withRuntime(152)}, retail...)...)
	return files
}

func repackFindings(t testing.TB, rep *Report) []Finding { return classOf(t, rep, ClassRepackage) }

func TestRepackageProposesTheWorkedExample(t *testing.T) {
	rep := runFixture(t, metamorphosis(t))
	got := repackFindings(t, rep)
	if len(got) != 1 {
		t.Fatalf("want one proposal, got %+v", got)
	}
	fd := got[0]
	want := Proposal{Op: OpLinkLibrivoxRepackage, Target: "repack-metamorphosis", From: "david-barnes-2016", To: "david-barnes", Advisory: true}
	if p := fd.Propose; p.Op != want.Op || p.Target != want.Target || p.From != want.From || p.To != want.To || !p.Advisory {
		t.Fatalf("proposal = %+v, want %+v", p, want)
	}
	if fd.Subclass != repackRuntime || fd.Key != "repack-metamorphosis" || fd.Recording != "david-barnes-2016" {
		t.Errorf("subclass %q key %q recording %q", fd.Subclass, fd.Key, fd.Recording)
	}
	for _, s := range []string{"LibriVox project 527", "same readers david-barnes", "runtime 152 min against the project's 154, 2 shorter", `publisher "Paperless"`} {
		if !strings.Contains(fd.Propose.Reason, s) {
			t.Errorf("reason %q lacks %q", fd.Propose.Reason, s)
		}
	}
	if !strings.Contains(strings.Join(fd.Notes, "\n"), "LibriVox project 527 is recording david-barnes") {
		t.Errorf("notes lack the project: %q", fd.Notes)
	}
	if strings.Contains(fd.Action, "do NOT apply") || !strings.Contains(fd.Action, "never a merge") {
		t.Errorf("action = %q", fd.Action)
	}
	if rep.Repackage.Pairs != 1 {
		t.Errorf("pairs = %d, want 1", rep.Repackage.Pairs)
	}
}

// The reader signal: exactly the LibriVox recording's readers, in any order. Another
// reader, or a reader set that merely overlaps (a group project's volunteer), is not.
func TestRepackageReaderSignal(t *testing.T) {
	t.Run("another reader is refused", func(t *testing.T) {
		rep := runFixture(t, metamorphosis(t, withNarrators("other-reader")))
		if got := repackFindings(t, rep); len(got) != 0 {
			t.Fatalf("proposed another reader's recording: %+v", got)
		}
	})
	t.Run("an overlapping reader set is refused and counted", func(t *testing.T) {
		rep := runFixture(t, metamorphosis(t, withNarrators("david-barnes", "other-reader")))
		if got := repackFindings(t, rep); len(got) != 0 {
			t.Fatalf("proposed an overlapping reader set: %+v", got)
		}
		if rep.Repackage.SharedReader != 1 {
			t.Errorf("shared-reader = %d, want 1", rep.Repackage.SharedReader)
		}
	})
	t.Run("the same readers in another order are admitted", func(t *testing.T) {
		files := repackPeople(t)
		files["works/xx/duet/work.json"] = workJSON(t, "duet", "Duet")
		files["works/xx/duet/recordings/david-barnes.json"] = recJSON(t, "david-barnes", "duet",
			withNarrators("david-barnes", "other-reader"), withRuntime(100), lvProject(900, 0))
		files["works/xx/duet/recordings/retail.json"] = recJSON(t, "retail", "duet",
			withNarrators("other-reader", "david-barnes"), withRuntime(99))
		rep := runFixture(t, files)
		if got := repackFindings(t, rep); len(got) != 1 || got[0].Propose.From != "retail" {
			t.Fatalf("want the retail recording proposed, got %+v", got)
		}
	})
}

// The runtime signal: never longer than the project beyond rounding, at most a minute
// a section (five at least) shorter.
func TestRepackageRuntimeSignal(t *testing.T) {
	for _, tc := range []struct {
		name               string
		lv, sections, cand int
		want               bool
	}{
		{"the same length", 154, 3, 154, true},
		{"two longer is rounding", 154, 3, 156, true},
		{"three longer is another recording", 154, 3, 157, false},
		{"one percent longer on a long project", 1000, 3, 1010, true},
		{"past one percent longer on a long project", 1000, 3, 1011, false},
		{"five shorter is the floor", 154, 3, 149, true},
		{"six shorter with three sections", 154, 3, 148, false},
		{"a short project's floor is a fifth of it", 10, 1, 8, true},
		{"past a fifth of a short project", 10, 1, 7, false},
		{"a one-minute track is not a six-minute poem", 6, 1, 1, false},
		{"one minute of rounding on a tiny project", 3, 0, 2, true},
		{"a minute a section shorter", 300, 10, 290, true},
		{"past a minute a section shorter", 300, 10, 289, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := repackPeople(t)
			repackWork(t, files, "book", 527, tc.lv, tc.sections, withRuntime(tc.cand))
			rep := runFixture(t, files)
			got := repackFindings(t, rep)
			if (len(got) == 1) != tc.want {
				t.Fatalf("proposed = %v, want %v: %+v", len(got) == 1, tc.want, got)
			}
			if !tc.want && rep.Repackage.OutsideWindow != 1 {
				t.Errorf("outside-window = %d, want 1", rep.Repackage.OutsideWindow)
			}
		})
	}
}

// The publisher signal stands in for an unstated runtime only when the publisher
// carries repackPublisherMin runtime-confirmed proposals elsewhere.
func TestRepackagePublisherSignal(t *testing.T) {
	tree := func(confirmed int) map[string]string {
		files := repackPeople(t)
		for i := range confirmed {
			repackWork(t, files, fmt.Sprintf("confirmed-%d", i), 600+i, 154, 3, withRuntime(152))
		}
		repackWork(t, files, "unstated", 527, 154, 3, testpack.WithoutRuntime())
		return files
	}
	t.Run("a repeat repackager is admitted", func(t *testing.T) {
		rep := runFixture(t, tree(repackPublisherMin))
		got := subclassOf(t, rep, ClassRepackage, repackPublisher)
		if len(got) != 1 || got[0].Key != "unstated" {
			t.Fatalf("want the unstated runtime proposed on its publisher, got %+v", got)
		}
		if !strings.Contains(strings.Join(got[0].Notes, "\n"), "the publisher stands in") {
			t.Errorf("notes = %q", got[0].Notes)
		}
	})
	t.Run("a publisher with too few is refused", func(t *testing.T) {
		rep := runFixture(t, tree(repackPublisherMin-1))
		if got := subclassOf(t, rep, ClassRepackage, repackPublisher); len(got) != 0 {
			t.Fatalf("proposed on too thin a publisher: %+v", got)
		}
		if rep.Repackage.NoRuntimeNoPublisher != 1 {
			t.Errorf("no-runtime-no-publisher = %d, want 1", rep.Repackage.NoRuntimeNoPublisher)
		}
	})
	t.Run("one recording matching two projects counts once", func(t *testing.T) {
		files := tree(repackPublisherMin - 1)
		// confirmed-0's retail edition also sits inside the window of a second project
		// by the same reader: still one Paperless edition, so still too few.
		files["works/xx/confirmed-0/recordings/david-barnes-v2.json"] = recJSON(t, "david-barnes-v2", "confirmed-0",
			withNarrators("david-barnes"), withRuntime(153), lvProject(800, 3))
		rep := runFixture(t, files)
		if got := subclassOf(t, rep, ClassRepackage, repackPublisher); len(got) != 0 {
			t.Fatalf("one edition counted twice toward its publisher: %+v", got)
		}
		if got := len(subclassOf(t, rep, ClassRepackage, repackRuntime)); got != repackPublisherMin {
			t.Errorf("runtime proposals = %d, want %d (both projects of confirmed-0 still proposed)", got, repackPublisherMin)
		}
	})
	t.Run("a reviewed rejection is no evidence for its publisher", func(t *testing.T) {
		reject := linkDecision("reject", "confirmed-0", "david-barnes-2016", "david-barnes")
		rep := runFixtureRejecting(t, tree(repackPublisherMin), reject)
		if got := subclassOf(t, rep, ClassRepackage, repackPublisher); len(got) != 0 {
			t.Fatalf("a refuted pair stood in for an unstated runtime: %+v", got)
		}
		if rep.Repackage.NoRuntimeNoPublisher != 1 || rep.Repackage.Suppressed != 1 {
			t.Errorf("no-runtime-no-publisher %d, suppressed %d, want 1 and 1",
				rep.Repackage.NoRuntimeNoPublisher, rep.Repackage.Suppressed)
		}
	})
	t.Run("a stated runtime outside the window is refused whatever the publisher", func(t *testing.T) {
		files := tree(repackPublisherMin)
		repackWork(t, files, "rerecorded", 700, 154, 3, withRuntime(200))
		rep := runFixture(t, files)
		for _, fd := range repackFindings(t, rep) {
			if fd.Key == "rerecorded" {
				t.Fatalf("proposed a re-recording on its publisher: %+v", fd)
			}
		}
	})
}

// The language signal: the two recordings state one language.
func TestRepackageLanguageSignal(t *testing.T) {
	rep := runFixture(t, metamorphosis(t, testpack.WithRecLanguage("de")))
	if got := repackFindings(t, rep); len(got) != 0 {
		t.Fatalf("proposed across languages: %+v", got)
	}
	if rep.Repackage.LanguageDiffers != 1 {
		t.Errorf("language-differs = %d, want 1", rep.Repackage.LanguageDiffers)
	}
	if got := repackFindings(t, runFixture(t, metamorphosis(t, testpack.WithRecLanguage("en-gb")))); len(got) != 1 {
		t.Errorf("a regional tag of one language was refused: %+v", got)
	}
}

// The sibling note names only the other projects the candidate is PROPOSED against: a
// pair left out (here, a runtime unstated on a one-off imprint) has nothing to accept.
func TestRepackageSiblingNoteNamesProposedPairsOnly(t *testing.T) {
	files := metamorphosis(t)
	files["works/xx/repack-metamorphosis/recordings/david-barnes-v2.json"] = recJSON(t, "david-barnes-v2", "repack-metamorphosis",
		withNarrators("david-barnes"), lvProject(528, 3), testpack.WithoutRuntime())
	rep := runFixture(t, files)
	got := repackFindings(t, rep)
	if len(got) != 1 || got[0].Propose.To != "david-barnes" {
		t.Fatalf("want the one runtime-confirmed pair, got %+v", got)
	}
	if notes := strings.Join(got[0].Notes, "\n"); strings.Contains(notes, "david-barnes-v2") {
		t.Errorf("the note names a pair that is not proposed: %q", notes)
	}
	files["works/xx/repack-metamorphosis/recordings/david-barnes-v2.json"] = recJSON(t, "david-barnes-v2", "repack-metamorphosis",
		withNarrators("david-barnes"), lvProject(528, 3), withRuntime(153))
	for _, fd := range repackFindings(t, runFixture(t, files)) {
		if !strings.Contains(strings.Join(fd.Notes, "\n"), "also read LibriVox recording") {
			t.Errorf("two proposed pairs of one candidate, and %s's note does not name the other: %q", fd.Propose.To, fd.Notes)
		}
	}
}

// Never across works, and never a LibriVox recording as the candidate.
func TestRepackageStaysInsideOneWork(t *testing.T) {
	files := repackPeople(t)
	files["works/xx/a/work.json"] = workJSON(t, "a", "A")
	files["works/xx/a/recordings/david-barnes.json"] = recJSON(t, "david-barnes", "a",
		withNarrators("david-barnes"), withRuntime(154), lvProject(527, 3))
	files["works/xx/b/work.json"] = workJSON(t, "b", "B")
	files["works/xx/b/recordings/david-barnes-2016.json"] = recJSON(t, "david-barnes-2016", "b",
		withNarrators("david-barnes"), withRuntime(152))
	// Two versions of one project by one reader are two projects, not a repackage.
	files["works/xx/c/work.json"] = workJSON(t, "c", "C")
	files["works/xx/c/recordings/v1.json"] = recJSON(t, "v1", "c", withNarrators("david-barnes"), withRuntime(154), lvProject(601, 3))
	files["works/xx/c/recordings/v2.json"] = recJSON(t, "v2", "c", withNarrators("david-barnes"), withRuntime(153), lvProject(602, 3))
	if got := repackFindings(t, runFixture(t, files)); len(got) != 0 {
		t.Fatalf("proposed across works or between two projects: %+v", got)
	}
}

func linkDecision(decision, target, from, to string) reviewedDecision {
	return reviewedDecision{Op: OpLinkLibrivoxRepackage, Target: target, From: from, To: to, Decision: decision, Reason: "reviewed"}
}

// An ACCEPT is the link recorded: the pair stops being proposed and the decision reads
// linked - whether or not the detector still proposes it, while both recordings stand.
func TestRepackageReviewedAcceptRecordsTheLink(t *testing.T) {
	accept := linkDecision("accept", "repack-metamorphosis", "david-barnes-2016", "david-barnes")
	rep := runFixtureRejecting(t, metamorphosis(t), accept)
	if got := repackFindings(t, rep); len(got) != 0 {
		t.Fatalf("an accepted pair is still proposed: %+v", got)
	}
	if o := rep.Reviewed.All[0]; o.Status != statusLinked {
		t.Fatalf("status = %q (%s), want %q", o.Status, o.Why, statusLinked)
	}
	if rep.Repackage.Linked != 1 {
		t.Errorf("linked = %d, want 1", rep.Repackage.Linked)
	}
	if s := summary(rep); !strings.Contains(s, "LibriVox repackage links recorded (linked) | 1") {
		t.Errorf("SUMMARY.md does not count the recorded link")
	}
	// No longer proposed (the runtime moved), still the record.
	rep = runFixtureRejecting(t, metamorphosis(t, withRuntime(120)), accept)
	if o := rep.Reviewed.All[0]; o.Status != statusLinked {
		t.Errorf("an accepted link the detector no longer proposes reads %q, want %q", o.Status, statusLinked)
	}
	// A mechanical set never gains it: metarepair reads nothing to apply.
	for _, c := range rep.classes {
		for _, fd := range c.rows {
			if fd.Propose.Op == OpLinkLibrivoxRepackage && !fd.Propose.Advisory {
				t.Errorf("a link became mechanical: %+v", fd)
			}
		}
	}
}

// A REJECT suppresses the pair, and reads suppressed while both recordings stand.
func TestRepackageReviewedRejectSuppresses(t *testing.T) {
	reject := linkDecision("reject", "repack-metamorphosis", "david-barnes-2016", "david-barnes")
	rep := runFixtureRejecting(t, metamorphosis(t), reject)
	if got := repackFindings(t, rep); len(got) != 0 {
		t.Fatalf("a rejected pair is still proposed: %+v", got)
	}
	if o := rep.Reviewed.All[0]; o.Status != statusSuppressed {
		t.Fatalf("status = %q (%s), want %q", o.Status, o.Why, statusSuppressed)
	}
	if rep.Repackage.Suppressed != 1 {
		t.Errorf("suppressed = %d, want 1", rep.Repackage.Suppressed)
	}
}

// An acceptance and a rejection resolving to one pair: the rejection wins and the
// acceptance is refused, as for every other op.
func TestRepackageConvergingDecisionsRefuseTheAcceptance(t *testing.T) {
	accept := linkDecision("accept", "metamorphosis-old", "david-barnes-2016", "david-barnes")
	reject := linkDecision("reject", "repack-metamorphosis", "david-barnes-2016", "david-barnes")
	rep := runFixtureRejectingWith(t, metamorphosis(t),
		`{"people":{},"series":{},"works":{"metamorphosis-old":"repack-metamorphosis"}}`, reject, accept)
	if got := repackFindings(t, rep); len(got) != 0 {
		t.Fatalf("a rejected pair is still proposed: %+v", got)
	}
	want := map[string]outcomeStatus{"reject": statusSuppressed, "accept": statusRefused}
	for _, o := range rep.Reviewed.All {
		if o.Status != want[o.Entry.Decision] {
			t.Errorf("%s reads %q (%s), want %q", o.Entry.Decision, o.Status, o.Why, want[o.Entry.Decision])
		}
	}
	if rep.Repackage.Suppressed != 1 || rep.Repackage.Linked != 0 {
		t.Errorf("suppressed %d linked %d, want 1 and 0", rep.Repackage.Suppressed, rep.Repackage.Linked)
	}
}

// A decision resolves through the works tombstones (a merge carries both recordings to
// the survivor) and reads STALE once a recording it names has left the work.
func TestRepackageReviewedResolvesAndGoesStale(t *testing.T) {
	retired := linkDecision("accept", "metamorphosis-old", "david-barnes-2016", "david-barnes")
	rep := runFixtureRejectingWith(t, metamorphosis(t),
		`{"people":{},"series":{},"works":{"metamorphosis-old":"repack-metamorphosis"}}`, retired)
	if o := rep.Reviewed.All[0]; o.Status != statusLinked || len(repackFindings(t, rep)) != 0 {
		t.Fatalf("a decision under a retired slug: status %q, findings %+v", o.Status, repackFindings(t, rep))
	}
	for _, tc := range []struct {
		name string
		d    reviewedDecision
		why  string
	}{
		{"a recording gone", linkDecision("reject", "repack-metamorphosis", "gone-2010", "david-barnes"), "no longer holds recording gone-2010"},
		{"the sides swapped", linkDecision("accept", "repack-metamorphosis", "david-barnes", "david-barnes-2016"), "is itself LibriVox project 527"},
		{"no such work", linkDecision("accept", "nowhere", "david-barnes-2016", "david-barnes"), "no live work nowhere"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rep := runFixtureRejecting(t, metamorphosis(t), tc.d)
			o := rep.Reviewed.All[0]
			if o.Status != statusStale || !strings.Contains(o.Why, tc.why) {
				t.Fatalf("status %q why %q, want STALE naming %q", o.Status, o.Why, tc.why)
			}
			if len(repackFindings(t, rep)) != 1 {
				t.Errorf("a stale decision withdrew the proposal")
			}
		})
	}
}

// The parse: accept and reject are both valid (accept is what records the link, so the
// no-repair-applies refusal does not reach it), and nothing else of the shape is.
func TestParseReviewedRepackageDecisions(t *testing.T) {
	parse := func(entries ...string) error {
		raw, err := canonical.Format([]byte("[" + strings.Join(entries, ",") + "]"))
		if err != nil {
			t.Fatal(err)
		}
		_, err = parseReviewed(raw)
		return err
	}
	// entry spells a decision of from -> david-barnes, an empty from omitting the key.
	entry := func(decision, from, extra string) string {
		if from != "" {
			extra += `,"from":"` + from + `"`
		}
		return `{"decision":"` + decision + `","op":"link-librivox-repackage","reason":"why",` +
			`"target":"repack-metamorphosis","to":"david-barnes"` + extra + `}`
	}
	const retail = "david-barnes-2016"
	if err := parse(entry("accept", retail, "")); err != nil {
		t.Errorf("refused an accept: %v", err)
	}
	if err := parse(entry("reject", retail, "")); err != nil {
		t.Errorf("refused a reject: %v", err)
	}
	if AcceptableOp(OpLinkLibrivoxRepackage) {
		t.Errorf("AcceptableOp(%s) = true: no repair applies it", OpLinkLibrivoxRepackage)
	}
	for name, raw := range map[string]string{
		"an assertion":                   entry("assert", retail, ""),
		"a series":                       entry("accept", retail, `,"series":"s"`),
		"a field":                        entry("accept", retail, `,"field":"recording"`),
		"others":                         entry("accept", retail, `,"others":["x"]`),
		"no from":                        entry("accept", "", ""),
		"one recording twice":            entry("accept", "david-barnes", ""),
		"a recording id that is no slug": entry("accept", "David Barnes", ""),
	} {
		t.Run(name, func(t *testing.T) {
			if err := parse(raw); err == nil {
				t.Fatalf("parsed %s", raw)
			}
		})
	}
}

// twoProjects is metamorphosis with a second LibriVox project read by the same reader
// (528, recording david-barnes-v2), so the retail edition pairs with both.
func twoProjects(t testing.TB) map[string]string {
	t.Helper()
	files := metamorphosis(t)
	files["works/xx/repack-metamorphosis/recordings/david-barnes-v2.json"] = recJSON(t, "david-barnes-v2", "repack-metamorphosis",
		withNarrators("david-barnes"), withRuntime(153), lvProject(528, 3))
	return files
}

// One retail recording repackages one project: an accepted link withdraws every pair
// of its retail recording, not only the accepted one.
func TestRepackageAcceptWithdrawsEveryPairOfTheRetailRecording(t *testing.T) {
	files := twoProjects(t)
	if got := repackFindings(t, runFixture(t, files)); len(got) != 2 {
		t.Fatalf("want the retail edition paired with both projects, got %+v", got)
	}
	rep := runFixtureRejecting(t, files, linkDecision("accept", "repack-metamorphosis", "david-barnes-2016", "david-barnes"))
	if got := repackFindings(t, rep); len(got) != 0 {
		t.Fatalf("a pair of an accepted retail recording is still proposed: %+v", got)
	}
	if rep.Repackage.Linked != 2 {
		t.Errorf("linked = %d, want 2", rep.Repackage.Linked)
	}
	if o := rep.Reviewed.All[0]; o.Status != statusLinked {
		t.Errorf("status = %q (%s), want %q", o.Status, o.Why, statusLinked)
	}
}

// A second acceptance naming the same retail recording with another LibriVox
// recording is refused, naming the link already recorded.
func TestRepackageSecondAcceptOfOneRetailRecordingIsRefused(t *testing.T) {
	rep := runFixtureRejecting(t, twoProjects(t),
		linkDecision("accept", "repack-metamorphosis", "david-barnes-2016", "david-barnes"),
		linkDecision("accept", "repack-metamorphosis", "david-barnes-2016", "david-barnes-v2"))
	first, second := rep.Reviewed.All[0], rep.Reviewed.All[1]
	if first.Status != statusLinked {
		t.Errorf("first acceptance = %q (%s), want %q", first.Status, first.Why, statusLinked)
	}
	if second.Status != statusRefused || !strings.Contains(second.Why,
		"already recorded as a repackage of LibriVox recording david-barnes: one retail recording repackages one project") {
		t.Errorf("second acceptance = %q (%s), want refused naming the recorded link", second.Status, second.Why)
	}
	if got := repackFindings(t, rep); len(got) != 0 {
		t.Errorf("pairs still proposed beside a recorded link: %+v", got)
	}
	// Two acceptances of the very same pair are one record, not a conflict.
	rep = runFixtureRejectingWith(t, twoProjects(t), `{"people":{},"series":{},"works":{"metamorphosis-old":"repack-metamorphosis"}}`,
		linkDecision("accept", "repack-metamorphosis", "david-barnes-2016", "david-barnes"),
		linkDecision("accept", "metamorphosis-old", "david-barnes-2016", "david-barnes"))
	if s := rep.Reviewed.All[1].Status; s != statusLinked {
		t.Errorf("a converging acceptance of the same pair = %q, want %q", s, statusLinked)
	}
}
