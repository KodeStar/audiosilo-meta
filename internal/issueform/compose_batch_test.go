package issueform

import (
	"encoding/json"
	"fmt"
	"maps"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/testpack"
)

// batchBody renders a correction-batch submission the way GitHub renders the
// form: the Changes textarea is `render: json`, so its value arrives fenced.
func batchBody(record, changes, evidence string, cc0 bool) string {
	b := field(fCorrectRecord, record) +
		field(fBatchChanges, "```json\n"+changes+"\n```") +
		field(fCorrectEvidence, evidence) +
		"### " + fCC0 + "\n\n"
	if cc0 {
		return b + checkedBox()
	}
	return b + uncheckedBox()
}

// changesJSON builds a version-1 Changes document from change objects.
func changesJSON(changes ...string) string {
	return `{"v":1,"source":"audiosilo-server/2.4.0","changes":[` + strings.Join(changes, ",") + `]}`
}

func change(record, field, current, corrected string) string {
	b, _ := json.Marshal(map[string]string{"record": record, "field": field, "current": current, "corrected": corrected})
	return string(b)
}

const (
	batchWork = "https://meta.audiosilo.app/works/existing-work"
)

var batchRec = recordingRef("existing-work", "john-smith-2020")

func runBatch(t *testing.T, dir, changes string) Result {
	t.Helper()
	return Process(Options{DataDir: dir, Template: "correction-batch", Body: batchBody(batchWork, changes, "Audible listing", true), Date: "2026-10-19"})
}

func outcomes(res Result) []Outcome {
	out := make([]Outcome, 0, len(res.Changes))
	for _, c := range res.Changes {
		out = append(out, c.Outcome)
	}
	return out
}

// TestBatchGoldenBody pins the contract an AudioSilo Bridge mirrors by hand: this
// exact body - the form's four headings, a fenced version-1 document, the CC0
// box ticked with an upper-case X - composes, applies the three auto fields in
// one pull request and lists the maintainer field as the remainder.
func TestBatchGoldenBody(t *testing.T) {
	const body = "### Record\n\nhttps://meta.audiosilo.app/works/existing-work\n\n" +
		"### Changes\n\n```json\n" +
		`{"v":1,"source":"audiosilo-server/2.4.0","changes":[` +
		`{"record":"https://meta.audiosilo.app/works/existing-work","field":"title","current":"Existing Work","corrected":"The Existing Work"},` +
		`{"record":"data/works/existing-work/recordings/john-smith-2020","field":"release_date","current":"","corrected":"2020-03-04"},` +
		`{"record":"data/works/existing-work/recordings/john-smith-2020","field":"isbn","current":"","corrected":"9781473647633"},` +
		`{"field":"authors","current":"Jane Doe","corrected":"Jane Doe, John Smith"}]}` +
		"\n```\n\n" +
		"### Evidence / source\n\nEdited by the server admin; the publisher's page lists the narrator as co-author.\n\n" +
		"### Public domain dedication\n\n- [X] I dedicate these corrections to the public domain under CC0-1.0, and I have the right to do so.\n"
	dir := seedTree(t)
	res := Process(Options{DataDir: dir, Template: "data:correction-batch", Body: body, Date: "2026-10-19"})
	if res.Status != StatusOK {
		t.Fatalf("status = %q, messages = %v", res.Status, res.Messages)
	}
	if res.Template != "correction-batch" || !strings.HasPrefix(res.License, "CC0-1.0") {
		t.Errorf("template/license = %q/%q", res.Template, res.License)
	}
	want := []Outcome{OutcomeApplied, OutcomeApplied, OutcomeApplied, OutcomeNeedsHuman}
	if got := outcomes(res); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("outcomes = %v, want %v", got, want)
	}
	if len(res.NeedsHuman) != 1 || !strings.Contains(res.NeedsHuman[0], "authors") || !strings.Contains(res.NeedsHuman[0], "Jane Doe, John Smith") {
		t.Errorf("needs_human = %v, want the authors change with its value", res.NeedsHuman)
	}
	if len(res.Files) != 1 || res.Files[0] != worksPack {
		t.Errorf("files = %v, want the one works pack", res.Files)
	}
	work := readFile(t, dir, "works/ex/existing-work/work.json")
	if !strings.Contains(work, `"title": "The Existing Work"`) {
		t.Errorf("title not applied:\n%s", work)
	}
	rec := readFile(t, dir, "works/ex/existing-work/recordings/john-smith-2020.json")
	for _, want := range []string{`"release_date": "2020-03-04"`, `"9781473647633"`, "publisher's page lists"} {
		if !strings.Contains(rec, want) {
			t.Errorf("recording lacks %s:\n%s", want, rec)
		}
	}

	// The wire carries the remainder and the per-change outcomes.
	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		NeedsHuman []string `json:"needs_human"`
		Changes    []struct {
			Outcome string `json:"outcome"`
		} `json:"changes"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	if len(wire.NeedsHuman) != 1 || len(wire.Changes) != 4 || wire.Changes[3].Outcome != "needs-human" {
		t.Errorf("wire = %s", raw)
	}
}

// TestBatchAllAppliedHasNoRemainder: a batch the bot applies in full carries no
// needs_human list, so the workflow neither comments nor labels.
func TestBatchAllAppliedHasNoRemainder(t *testing.T) {
	res := runBatch(t, seedTree(t), changesJSON(change(batchWork, "title", "Existing Work", "A Better Title")))
	if res.Status != StatusOK || len(res.NeedsHuman) != 0 {
		t.Fatalf("status = %q, needs_human = %v, messages = %v", res.Status, res.NeedsHuman, res.Messages)
	}
	raw, _ := json.Marshal(res)
	if strings.Contains(string(raw), "needs_human") {
		t.Errorf("an empty remainder reached the wire: %s", raw)
	}
}

// TestBatchOnlyMaintainerFieldsIsNeedsHuman: nothing applied, something for a
// maintainer - the existing needs-human verdict, and nothing written.
func TestBatchOnlyMaintainerFieldsIsNeedsHuman(t *testing.T) {
	dir := seedTree(t)
	before := testpack.Snapshot(t, dir)
	res := runBatch(t, dir, changesJSON(
		change(batchRec, "narrators", "John Smith", "Jane Doe"),
		change(batchWork, "series", "Existing Series", "Another Series"),
	))
	if res.Status != StatusNeedsHuman {
		t.Fatalf("status = %q, messages = %v", res.Status, res.Messages)
	}
	if !strings.Contains(res.Messages[0], "none of the 2 changes") {
		t.Errorf("the verdict does not lead the messages: %v", res.Messages)
	}
	if len(res.NeedsHuman) != 0 {
		t.Errorf("a needs-human verdict also carried a remainder: %v", res.NeedsHuman)
	}
	if !maps.Equal(before, testpack.Snapshot(t, dir)) {
		t.Error("a needs-human batch changed the tree")
	}
}

// TestBatchAllUnchangedIsADuplicate: every change states what the record already
// says, so there is nothing to do.
func TestBatchAllUnchangedIsADuplicate(t *testing.T) {
	res := runBatch(t, seedTree(t), changesJSON(change(batchWork, "title", "", "Existing Work")))
	if res.Status != StatusDuplicate || !anyContains(res.Messages, "nothing to change") {
		t.Fatalf("status = %q, messages = %v", res.Status, res.Messages)
	}
	if got := outcomes(res); len(got) != 1 || got[0] != OutcomeUnchanged {
		t.Errorf("outcomes = %v", got)
	}
}

// TestBatchUnchangedBesideAppliedStaysOK: a no-op change does not hold back its
// siblings.
func TestBatchUnchangedBesideAppliedStaysOK(t *testing.T) {
	res := runBatch(t, seedTree(t), changesJSON(
		change(batchWork, "title", "", "Existing Work"),
		change(batchRec, "release_date", "", "2020"),
	))
	if res.Status != StatusOK || fmt.Sprint(outcomes(res)) != fmt.Sprint([]Outcome{OutcomeUnchanged, OutcomeApplied}) {
		t.Fatalf("status = %q, outcomes = %v, messages = %v", res.Status, outcomes(res), res.Messages)
	}
}

// TestBatchStructuralProblemsRefuseTheWhole: a change nobody could apply as
// filed refuses the issue, names every bad change, and writes nothing - even
// when its siblings were fine.
func TestBatchStructuralProblemsRefuseTheWhole(t *testing.T) {
	long := strings.Repeat("x", maxBatchValueRunes+1)
	cases := map[string]struct {
		changes string
		want    string
	}{
		"unknown field":       {changesJSON(change(batchWork, "title", "", "Fine"), change(batchWork, "genres", "", "fantasy")), `field "genres" is not one this form takes`},
		"value too long":      {changesJSON(change(batchWork, "title", "", long)), "corrected is 501 characters long"},
		"control character":   {changesJSON(change(batchWork, "title", "", "Two\nLines")), "control character"},
		"work field on a rec": {changesJSON(change(batchRec, "title", "", "Fine")), `"title" is a work field`},
		"stated twice":        {changesJSON(change(batchWork, "title", "", "A"), change(batchWork, "title", "", "B")), "stated twice"},
		"no corrected value":  {changesJSON(change(batchWork, "title", "x", "")), "has no corrected value"},
		"unknown key":         {`{"v":1,"source":"x","changes":[{"field":"title","corrected":"A","why":"x"}]}`, "unknown field"},
		"wrong version":       {`{"v":2,"source":"x","changes":[{"field":"title","corrected":"A"}]}`, "version 2"},
		"no changes":          {`{"v":1,"source":"x","changes":[]}`, "lists no changes"},
		"two documents":       {`{"v":1,"changes":[{"field":"title","corrected":"A"}]} {}`, "more than one JSON value"},
		"not json":            {`title = A`, "could not be read"},
		"apply-time bad date": {changesJSON(change(batchWork, "title", "", "Fine"), change(batchRec, "release_date", "", "last spring")), "refused as a whole"},
		"apply-time bad isbn": {changesJSON(change(batchRec, "isbn", "", "12345")), "not a usable ISBN"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			dir := seedTree(t)
			before := testpack.Snapshot(t, dir)
			res := runBatch(t, dir, c.changes)
			if res.Status != StatusInvalid {
				t.Fatalf("status = %q, want invalid; messages = %v", res.Status, res.Messages)
			}
			if !anyContains(res.Messages, c.want) {
				t.Errorf("messages do not say %q: %v", c.want, res.Messages)
			}
			if anyContains(res.Messages, "applied ") {
				t.Errorf("a refused batch reports a change as applied: %v", res.Messages)
			}
			if len(res.Files) != 0 || len(res.NeedsHuman) != 0 || !maps.Equal(before, testpack.Snapshot(t, dir)) {
				t.Errorf("a refused batch wrote or listed a remainder: files %v, needs_human %v", res.Files, res.NeedsHuman)
			}
		})
	}
}

// TestBatchCapsTheChangeCount: 25 is the most one issue takes.
func TestBatchCapsTheChangeCount(t *testing.T) {
	var many []string
	for i := range maxBatchChanges + 1 {
		many = append(many, change(batchRec, "isbn", "", fmt.Sprintf("97814736476%02d", i)))
	}
	res := runBatch(t, seedTree(t), changesJSON(many...))
	if res.Status != StatusInvalid || !anyContains(res.Messages, "at most 25") {
		t.Fatalf("status = %q, messages = %v", res.Status, res.Messages)
	}
}

// TestBatchRefusesWithoutConsentOrEvidence: the CC0 box and the evidence are
// required, and the evidence is bounded.
func TestBatchRefusesWithoutConsentOrEvidence(t *testing.T) {
	changes := changesJSON(change(batchWork, "title", "", "A Better Title"))
	for name, body := range map[string]string{
		"no cc0":        batchBody(batchWork, changes, "web", false),
		"no evidence":   batchBody(batchWork, changes, "", true),
		"long evidence": batchBody(batchWork, changes, strings.Repeat("e", maxBatchEvidenceRunes+1), true),
	} {
		res := Process(Options{DataDir: seedTree(t), Template: "correction-batch", Body: body})
		if res.Status != StatusInvalid {
			t.Errorf("%s: status = %q, messages = %v", name, res.Status, res.Messages)
		}
	}
}

// TestBatchRecordingFieldOnAWorkGoesToAMaintainer: a filer that knows the book
// but not which recording it is sends the work; that is a maintainer's lookup,
// not a mapping bug.
func TestBatchRecordingFieldOnAWorkGoesToAMaintainer(t *testing.T) {
	res := runBatch(t, seedTree(t), changesJSON(
		change(batchWork, "title", "", "A Better Title"),
		change(batchWork, "release_date", "", "2020"),
	))
	if res.Status != StatusOK || len(res.NeedsHuman) != 1 || !strings.Contains(res.NeedsHuman[0], "names only the work") {
		t.Fatalf("status = %q, needs_human = %v, messages = %v", res.Status, res.NeedsHuman, res.Messages)
	}
}

// TestBatchUnresolvableRecordGoesToAMaintainer: a reference naming no work or
// recording is the single form's needs-human, per change.
func TestBatchUnresolvableRecordGoesToAMaintainer(t *testing.T) {
	res := runBatch(t, seedTree(t), changesJSON(
		change("https://meta.audiosilo.app/people/jane-doe", "title", "", "A"),
		change("https://meta.audiosilo.app/works/no-such-work", "title", "", "B"),
	))
	if res.Status != StatusNeedsHuman || fmt.Sprint(outcomes(res)) != fmt.Sprint([]Outcome{OutcomeNeedsHuman, OutcomeNeedsHuman}) {
		t.Fatalf("status = %q, outcomes = %v, messages = %v", res.Status, outcomes(res), res.Messages)
	}
}

// TestBatchTitleSpelledWithItsSubtitleIsUnchanged: a player shows "Title:
// Subtitle" for a record that splits them, and writing that into title would
// duplicate the subtitle.
func TestBatchTitleSpelledWithItsSubtitleIsUnchanged(t *testing.T) {
	dir := t.TempDir()
	seed := seedFiles()
	seed["works/ex/existing-work/work.json"] = strings.Replace(seed["works/ex/existing-work/work.json"],
		`"title": "Existing Work"`, `"subtitle": "A Novel",
  "title": "Existing Work"`, 1)
	testpack.Seed(t, dir, seed)
	res := runBatch(t, dir, changesJSON(change(batchWork, "title", "", "Existing Work: A Novel")))
	if res.Status != StatusDuplicate || fmt.Sprint(outcomes(res)) != fmt.Sprint([]Outcome{OutcomeUnchanged}) {
		t.Fatalf("status = %q, outcomes = %v, messages = %v", res.Status, outcomes(res), res.Messages)
	}
}

// TestBatchFollowsARetiredWorkSlug: a server matched the book before a core
// merge and still holds the old id; the change lands on the survivor, saying so.
func TestBatchFollowsARetiredWorkSlug(t *testing.T) {
	dir := seedTree(t)
	writeRedirects(t, dir, map[string]string{"retired-work": "existing-work"})
	res := runBatch(t, dir, changesJSON(
		change("https://meta.audiosilo.app/works/retired-work", "title", "", "A Better Title"),
		change(recordingRef("retired-work", "john-smith-2020"), "release_date", "", "2020"),
	))
	if res.Status != StatusOK || fmt.Sprint(outcomes(res)) != fmt.Sprint([]Outcome{OutcomeApplied, OutcomeApplied}) {
		t.Fatalf("status = %q, outcomes = %v, messages = %v", res.Status, outcomes(res), res.Messages)
	}
	if !anyContains(res.Messages, `"retired-work" has been merged into "existing-work"`) {
		t.Errorf("the merge is not noted: %v", res.Messages)
	}
	if work := readFile(t, dir, "works/ex/existing-work/work.json"); !strings.Contains(work, "A Better Title") {
		t.Errorf("the survivor was not corrected:\n%s", work)
	}
}

// TestApplyCorrectionScopesTheVerdict: one change's failure never becomes the
// composer's, and its messages leave with it.
func TestApplyCorrectionScopesTheVerdict(t *testing.T) {
	c := &composer{status: "", messages: []string{"before"}}
	c.fail(StatusNeedsHuman, "outer")
	status, msgs := c.status, len(c.messages)
	outcome, got := c.applyCorrection("not a reference at all", "title", "x", "web")
	if outcome != OutcomeNeedsHuman || len(got) != 1 {
		t.Errorf("outcome = %q, messages = %v", outcome, got)
	}
	if c.status != status || len(c.messages) != msgs {
		t.Errorf("the composer's verdict changed: %q %v", c.status, c.messages)
	}
}

// TestBatchRoutesFromItsLabel: the label the route workflow applies selects the
// batch composer, under both spellings it accepts.
func TestBatchRoutesFromItsLabel(t *testing.T) {
	if got := TemplateFromLabels([]string{"data", "data:correction-batch"}); got != "correction-batch" {
		t.Errorf("TemplateFromLabels = %q", got)
	}
	for _, in := range []string{"correction-batch", "data:correction-batch", "corrections"} {
		if got := normalizeTemplate(in); got != "correction-batch" {
			t.Errorf("normalizeTemplate(%q) = %q", in, got)
		}
	}
	if got := normalizeTemplate("correction"); got != "correct-data" {
		t.Errorf("the single-field label moved: %q", got)
	}
}

// TestRecapsFillASummaryOnlyMember: a recaps member holding only a whole-book
// summary is filled by a submitted recap list rather than refused as taken, and
// the community's summary and its sources survive (an AudioSilo Bridge drops its
// own summary in that case; a submission that still carries one is told).
func TestRecapsFillASummaryOnlyMember(t *testing.T) {
	dir := t.TempDir()
	seed := seedFiles()
	// Not a valid member on its own today (the schema requires a list), which is
	// why it is seeded without the validity check: the merge must still turn it
	// into one.
	seed["works/ex/existing-work/recaps.json"] = `{"work":"existing-work","in_short":"It all works out in the end.","license":"CC-BY-SA-4.0","sources":[{"type":"community","ref":"summary-author"}]}`
	testpack.Seed(t, dir, seed)

	submitted := `{"work":"existing-work","recaps":[{"through":{"chapter":1},"text":"Alice sets out, and by the end of the first chapter she has left home."}],"in_short":"A different summary.","license":"CC-BY-SA-4.0","sources":[{"type":"community"}]}`
	res := Process(Options{DataDir: dir, Template: "recaps", Body: recapsBody("existing-work", submitted, true)})
	if res.Status != StatusOK {
		t.Fatalf("status = %q, messages = %v", res.Status, res.Messages)
	}
	if !anyContains(res.Messages, "kept the community's existing in_short") {
		t.Errorf("the dropped summary is not reported: %v", res.Messages)
	}
	var got struct {
		Recaps  []map[string]any `json:"recaps"`
		InShort string           `json:"in_short"`
		Sources []map[string]any `json:"sources"`
	}
	if err := json.Unmarshal([]byte(readFile(t, dir, "works/ex/existing-work/recaps.json")), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Recaps) != 1 || got.InShort != "It all works out in the end." || len(got.Sources) != 2 {
		t.Errorf("merged member = %+v", got)
	}
}

// TestRecapsStillRefuseAFullMember: a member that already has a recap list is
// replaced by nobody but a maintainer, summary or not.
func TestRecapsStillRefuseAFullMember(t *testing.T) {
	dir := t.TempDir()
	seed := seedFiles()
	seed["works/ex/existing-work/recaps.json"] = `{"work":"existing-work","recaps":[{"through":{"chapter":2},"text":"Earlier text."}],"in_short":"Summary.","license":"CC-BY-SA-4.0","sources":[{"type":"community"}]}`
	testpack.Seed(t, dir, seed)
	res := Process(Options{DataDir: dir, Template: "recaps", Body: recapsBody("existing-work", validRecapsJSON, true)})
	if res.Status != StatusNeedsHuman || !anyContains(res.Messages, "already exists") {
		t.Fatalf("status = %q, messages = %v", res.Status, res.Messages)
	}
}
