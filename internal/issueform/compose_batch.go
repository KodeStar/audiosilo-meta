package issueform

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// Field labels for correction-batch.yml. Record and Evidence share the
// correct-data form's labels (fCorrectRecord, fCorrectEvidence) and the CC0
// checkbox is fCC0, so only the Changes block is new.
const fBatchChanges = "Changes"

// The batch form's bounds. A batch is filed by a program (an AudioSilo Bridge
// mapping an admin's edits onto meta records), so a value outside them is far
// more likely a mapping bug than a real correction, and is refused loudly.
const (
	// maxBatchChanges caps the changes one issue may carry: one book's worth of
	// edits, and a pull request a maintainer can still read in one sitting.
	maxBatchChanges = 25
	// maxBatchValueRunes bounds each record reference, current and corrected
	// value (no correctable field comes near it).
	maxBatchValueRunes = 500
	// maxBatchEvidenceRunes bounds the shared evidence text.
	maxBatchEvidenceRunes = 2000
	// batchVersion is the one Changes document version this composer reads.
	batchVersion = 1
)

// batchMode is what the batch form does with a field it accepts.
type batchMode int

const (
	// batchAuto fields are applied by the correction path (correctOne), exactly
	// as the single-field form applies them.
	batchAuto batchMode = iota + 1
	// batchMaintainer fields are packaged for a maintainer with the evidence
	// attached and never written by the bot: a credit list or a series placement
	// is a judgement over records the batch does not name.
	batchMaintainer
)

// batchFields is the batch form's closed field vocabulary per record kind - the
// fields a server-side edit maps onto (CORRECTIONS-SPIKE.md section 5). Only
// three auto-apply: a work's title, and a recording's release_date and isbn (the
// additive op). Any field outside this table is a mapping bug and refuses the
// whole issue.
var batchFields = map[model.Kind]map[string]batchMode{
	model.KindWork: {
		"title":        batchAuto,
		"authors":      batchMaintainer,
		"series":       batchMaintainer,
		"series_index": batchMaintainer,
		"more_series":  batchMaintainer,
	},
	model.KindRecording: {
		"release_date": batchAuto,
		"isbn":         batchAuto,
		"narrators":    batchMaintainer,
	},
}

// Outcome is what happened to one change of a correction batch.
type Outcome string

const (
	// OutcomeApplied: the change is in the pull request.
	OutcomeApplied Outcome = "applied"
	// OutcomeUnchanged: the record already says this; nothing to write.
	OutcomeUnchanged Outcome = "unchanged"
	// OutcomeNeedsHuman: a maintainer applies it (a maintainer-only field, an
	// unresolvable record, a disagreement with a recorded fact).
	OutcomeNeedsHuman Outcome = "needs-human"
	// OutcomeInvalid: the change cannot be applied by anyone as filed, which
	// refuses the whole issue.
	OutcomeInvalid Outcome = "invalid"
)

// ChangeResult is one change's outcome in a correction batch (Result.Changes).
type ChangeResult struct {
	Record   string   `json:"record"`
	Field    string   `json:"field"`
	Outcome  Outcome  `json:"outcome"`
	Messages []string `json:"messages,omitempty"`
}

// batchDoc is the fenced JSON the Changes field carries, version 1:
//
//	{"v":1,"source":"audiosilo-server/2.4.0","changes":[
//	  {"record":"https://meta.audiosilo.app/works/<work>","field":"title","current":"...","corrected":"..."},
//	  {"record":"data/works/<work>/recordings/<rec>","field":"release_date","current":"2015","corrected":"1999-11-01"}]}
//
// record is optional per change and defaults to the form's Record field.
// Unknown keys are refused: the document is written by a program, and a key this
// composer does not read is one whose meaning it would silently drop.
type batchDoc struct {
	V       int           `json:"v"`
	Source  string        `json:"source"`
	Changes []batchChange `json:"changes"`
}

type batchChange struct {
	Record    string `json:"record"`
	Field     string `json:"field"`
	Current   string `json:"current"`
	Corrected string `json:"corrected"`
}

// plannedChange is a structurally valid change, ready to apply.
type plannedChange struct {
	batchChange
	ref  recordRef
	mode batchMode
	// resolved is false when the record reference names nothing this form can
	// address; the change is then a maintainer's.
	resolved bool
}

// correctBatch applies a correction-batch submission: up to maxBatchChanges
// field corrections sharing one evidence text, landing in ONE pull request.
//
// Every change is checked before any is applied, and one structurally invalid
// change (an unknown field, an over-long value, a field on the wrong kind of
// record) refuses the whole issue, naming every bad change: a batch is
// machine-written, so a bad change is most likely a mapping bug that would make
// its siblings suspect too. The valid ones then run one at a time through
// applyCorrection, the single-field form's own path, and the verdict is:
//
//   - ok when at least one change applied, with every other change listed - the
//     needs-human ones in Result.NeedsHuman, which the workflow comments on the
//     issue (and labels it data:needs-human) once the pull request is open;
//   - needs-human when nothing applied and something needs a maintainer;
//   - duplicate when every change already matches the record;
//   - invalid when any change is invalid, including one only applying it could
//     find (a malformed date), and then nothing is written.
func (c *composer) correctBatch(s sections) {
	if !s.checked(fCC0) {
		c.fail(StatusInvalid, "the CC0 public-domain dedication checkbox is not ticked")
		return
	}
	evidence := s.get(fCorrectEvidence)
	if evidence == "" {
		c.fail(StatusInvalid, "Evidence / source is required")
		return
	}
	if n := utf8.RuneCountInString(evidence); n > maxBatchEvidenceRunes {
		c.fail(StatusInvalid, "Evidence / source is %d characters long; the limit is %d", n, maxBatchEvidenceRunes)
		return
	}
	doc, err := parseBatchDoc(s.raw(fBatchChanges))
	if err != nil {
		c.fail(StatusInvalid, "the Changes block could not be read: %v", err)
		return
	}
	plan, problems := c.planBatch(doc, s.get(fCorrectRecord))
	if len(problems) > 0 {
		c.refuseBatch(len(problems), problems)
		return
	}

	var applied, human, invalid int
	var invalidLines []string
	for i, ch := range plan {
		outcome, msgs := c.applyBatchChange(ch, evidence)
		label := fmt.Sprintf("change %d (%s on %s)", i+1, ch.Field, ch.Record)
		lines := make([]string, 0, len(msgs))
		for _, m := range msgs {
			lines = append(lines, label+": "+m)
		}
		c.changes = append(c.changes, ChangeResult{Record: ch.Record, Field: ch.Field, Outcome: outcome, Messages: msgs})
		switch outcome {
		case OutcomeApplied:
			applied++
		case OutcomeNeedsHuman:
			human++
			c.needsHuman = append(c.needsHuman, lines...)
		case OutcomeInvalid:
			invalid++
			invalidLines = append(invalidLines, lines...)
		}
		c.messages = append(c.messages, lines...)
	}

	switch {
	case invalid > 0:
		// Only the invalid lines: nothing else was written, so an "applied" line
		// (or outcome) beside them would be untrue.
		c.messages, c.needsHuman, c.changes = nil, nil, nil
		c.refuseBatch(invalid, invalidLines)
	case applied > 0:
		// ok: the composer's status stays unset, and process flushes and
		// validates what was queued.
	case human > 0:
		c.needsHuman = nil
		c.failLead(StatusNeedsHuman, "none of the %d changes could be applied automatically - a maintainer will apply %s", len(plan), countNoun(human, "change", "changes"))
	default:
		c.failLead(StatusDuplicate, "every change already matches the record - nothing to change")
	}
}

// failLead sets a terminal status with its reason as the FIRST message, ahead of
// the per-change lines already collected: the workflow's comment and every
// reader see the verdict before its detail.
func (c *composer) failLead(status Status, format string, args ...any) {
	rest := c.messages
	c.messages = nil
	c.fail(status, format, args...)
	c.messages = append(c.messages, rest...)
}

// refuseBatch fails the whole batch as invalid because n of its changes are,
// with the reason first and then lines, the detail naming each bad change.
func (c *composer) refuseBatch(n int, lines []string) {
	c.fail(StatusInvalid, "the batch is refused as a whole, because %s invalid - nothing was applied", countNoun(n, "change is", "changes are"))
	c.messages = append(c.messages, lines...)
}

// countNoun spells "1 change is" / "3 changes are".
func countNoun(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// parseBatchDoc reads the Changes field: one JSON document, optionally fenced,
// of the version this composer reads, with 1 to maxBatchChanges changes.
func parseBatchDoc(block string) (batchDoc, error) {
	raw := unfence(block)
	if raw == "" {
		return batchDoc{}, fmt.Errorf("it is empty")
	}
	// DisallowUnknownFields still matches a key in any case and keeps the last of
	// two, so "corrected" twice, or beside "Corrected", would drop a value the
	// issue page shows: both are refused as an unknown key is.
	if err := pack.CheckNoDuplicateKeys([]byte(raw)); err != nil {
		return batchDoc{}, err
	}
	if err := checkBatchKeySpelling(raw); err != nil {
		return batchDoc{}, err
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	var doc batchDoc
	if err := dec.Decode(&doc); err != nil {
		return batchDoc{}, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return batchDoc{}, fmt.Errorf("it holds more than one JSON value")
	}
	if doc.V != batchVersion {
		return batchDoc{}, fmt.Errorf("version %d is not one this bot reads (it reads %d)", doc.V, batchVersion)
	}
	if len(doc.Changes) == 0 {
		return batchDoc{}, fmt.Errorf("it lists no changes")
	}
	if len(doc.Changes) > maxBatchChanges {
		return batchDoc{}, fmt.Errorf("it lists %d changes; one issue takes at most %d", len(doc.Changes), maxBatchChanges)
	}
	return doc, nil
}

// batchKeys are the keys of a version-1 document and of each of its changes,
// spelled exactly.
var batchKeys = [2][]string{{"v", "source", "changes"}, {"record", "field", "current", "corrected"}}

// checkBatchKeySpelling refuses a key not spelled exactly as batchKeys has it. A
// document that is not an object holding a list of change objects is left to
// the decoder, whose error says why.
func checkBatchKeySpelling(raw string) error {
	var top map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &top) != nil {
		return nil
	}
	var changes []map[string]json.RawMessage
	_ = json.Unmarshal(top["changes"], &changes)
	for i, obj := range append([]map[string]json.RawMessage{top}, changes...) {
		for _, k := range slices.Sorted(maps.Keys(obj)) {
			if !slices.Contains(batchKeys[min(i, 1)], k) {
				return fmt.Errorf("unknown field %q", k)
			}
		}
	}
	return nil
}

// planBatch checks every change's STRUCTURE - known field, bounded values, a
// field the addressed kind of record carries, no change stated twice - and
// returns the plan, or every problem found. It reads no record: what a change
// does to the catalogue is applyBatchChange's question.
func (c *composer) planBatch(doc batchDoc, defaultRecord string) ([]plannedChange, []string) {
	var plan []plannedChange
	var problems []string
	seen := map[string]bool{}
	for i, ch := range doc.Changes {
		bad := func(format string, args ...any) {
			problems = append(problems, fmt.Sprintf("change %d: ", i+1)+fmt.Sprintf(format, args...))
		}
		// Values are read as every other form field is (formValue): a title
		// escaped as "Rizzoli &amp; Isles" is the title "Rizzoli & Isles".
		ch.Record = formValue(ch.Record)
		if ch.Record == "" {
			ch.Record = defaultRecord
		}
		ch.Field = strings.TrimSpace(ch.Field)
		ch.Corrected = formValue(ch.Corrected)
		ch.Current = formValue(ch.Current)

		if ch.Record == "" {
			bad("names no record, and the form's Record field is empty")
			continue
		}
		if ch.Corrected == "" {
			bad("%q has no corrected value", ch.Field)
			continue
		}
		if msg := boundedValues([]namedValue{{"record", ch.Record}, {"current", ch.Current}, {"corrected", ch.Corrected}}); msg != "" {
			bad("%s", msg)
			continue
		}
		mode, known := batchMode(0), false
		for _, fields := range batchFields {
			if m, ok := fields[ch.Field]; ok {
				mode, known = m, true
			}
		}
		if !known {
			bad("field %q is not one this form takes (%s)", ch.Field, batchFieldList())
			continue
		}

		p := plannedChange{batchChange: ch, mode: mode}
		// target names the record the change lands on, so "stated twice" sees one
		// record however it is spelled - a page URL, a data path, a retired slug
		// liveRecord follows to its survivor - and falls back to the reference
		// itself when it resolves to nothing this form addresses.
		target := ch.Record
		ref, ok := resolveRecordRef(ch.Record)
		if ok && (ref.kind == model.KindWork || ref.kind == model.KindRecording) {
			p.ref, p.resolved = ref, true
			_, live := c.liveWorkOf(ref)
			target = string(ref.kind) + "\x00" + live
			if ref.kind == model.KindRecording {
				target += "\x00" + ref.slug
			}
			if _, own := batchFields[ref.kind][ch.Field]; !own {
				if ref.kind == model.KindRecording {
					bad("%q is a work field, but %s is a recording - address the work instead (%s)", ch.Field, ch.Record, workPageURL(live))
					continue
				}
				// A recording field against a WORK is not a mapping bug: the filer
				// may simply not know which recording the book is. It goes to a
				// maintainer, who can tell.
				p.resolved = false
			}
		}

		// The same field of the same record twice is two answers to one
		// question - except the additive isbn, where two different ISBNs are two
		// facts.
		key := target + "\x00" + ch.Field
		if ch.Field == "isbn" {
			key += "\x00" + ch.Corrected
		}
		if seen[key] {
			bad("%q on %s is stated twice", ch.Field, ch.Record)
			continue
		}
		seen[key] = true
		plan = append(plan, p)
	}
	return plan, problems
}

// namedValue is one value of a change, named for a refusal message.
type namedValue struct{ name, v string }

// boundedValues reports the first value that breaks the batch's bounds: over
// maxBatchValueRunes, or carrying a control character (a newline in a title is
// a mapping bug, and every value is echoed into a comment).
func boundedValues(values []namedValue) string {
	for _, nv := range values {
		if n := utf8.RuneCountInString(nv.v); n > maxBatchValueRunes {
			return fmt.Sprintf("%s is %d characters long; the limit is %d", nv.name, n, maxBatchValueRunes)
		}
		if strings.IndexFunc(nv.v, unicode.IsControl) >= 0 {
			return fmt.Sprintf("%s contains a control character", nv.name)
		}
	}
	return ""
}

// batchFieldList names the accepted fields, for the refusal message.
func batchFieldList() string {
	var out []string
	for _, kind := range []model.Kind{model.KindWork, model.KindRecording} {
		names := make([]string, 0, len(batchFields[kind]))
		for f := range batchFields[kind] {
			names = append(names, f)
		}
		slices.Sort(names)
		out = append(out, fmt.Sprintf("a %s's %s", kind, strings.Join(names, ", ")))
	}
	return strings.Join(out, "; ")
}

// applyBatchChange decides one planned change.
func (c *composer) applyBatchChange(p plannedChange, evidence string) (Outcome, []string) {
	if !p.resolved {
		if p.ref.kind == model.KindWork {
			return OutcomeNeedsHuman, []string{fmt.Sprintf("%q describes a recording, and this change names only the work - a maintainer will find the recording and apply %s",
				p.Field, describeChange(p))}
		}
		return OutcomeNeedsHuman, []string{fmt.Sprintf("could not resolve %q to a work or recording - a maintainer will locate it and apply %s", p.Record, describeChange(p))}
	}
	ref, notes := c.liveRecord(p)
	if p.mode == batchMaintainer {
		return OutcomeNeedsHuman, append(notes, fmt.Sprintf("%q is not applied automatically - a maintainer will apply %s", p.Field, describeChange(p)))
	}
	if p.Field == "title" && c.titleSpellsSubtitle(p.ref, p.Corrected) {
		return OutcomeUnchanged, append(notes, fmt.Sprintf("the record already says %q as a title and subtitle - nothing to change", p.Corrected))
	}
	outcome, msgs := c.applyCorrection(ref, p.Field, p.Corrected, evidence)
	return outcome, append(notes, msgs...)
}

// describeChange is the human sentence for a change a maintainer applies.
func describeChange(p plannedChange) string {
	if p.Current == "" {
		return fmt.Sprintf("%q", p.Corrected)
	}
	return fmt.Sprintf("%q (it was %q)", p.Corrected, p.Current)
}

// liveRecord is the change's record with a RETIRED work slug followed to its
// survivor, plus the note saying so. A server matched the book before a core
// merge retired its slug and still holds the old id; the record it means lives
// under the survivor now, and the bot is choosing where to write, so it writes
// there (resolveWorkKey's rule for a sidecar key).
func (c *composer) liveRecord(p plannedChange) (recordRef, []string) {
	work, live := c.liveWorkOf(p.ref)
	if live == work {
		return p.ref, nil
	}
	ref := p.ref
	if ref.kind == model.KindRecording {
		ref.workSlug = live
	} else {
		ref.slug = live
	}
	return ref, []string{fmt.Sprintf("%q has been merged into %q, so the change goes there", work, live)}
}

// titleSpellsSubtitle reports whether a corrected title is the work's own title
// and subtitle joined with ": " - how a player or a tag shows a book whose record
// splits them. Writing it to title would duplicate the subtitle into it.
func (c *composer) titleSpellsSubtitle(ref recordRef, corrected string) bool {
	w := c.works[c.liveWorkSlug(ref.slug)]
	return w != nil && w.Subtitle != "" && corrected == w.Title+": "+w.Subtitle
}

// applyCorrection applies ONE field correction through the single-field form's
// path (correctOne) and reports its outcome instead of the composer's verdict.
//
// correctOne and every helper it calls report through c.fail, which sets the
// whole submission's status. Here that verdict is scoped to the one change: the
// composer's status, messages and no-op flag are set aside, the change runs, its
// verdict and messages are read off, and the composer is put back - so a batch
// collects one outcome per change and decides its own verdict from them. A
// change that fails writes nothing (correctOne queues its entry last), and one
// that applies leaves its entry queued for the batch's one flush.
func (c *composer) applyCorrection(ref recordRef, field, corrected, evidence string) (Outcome, []string) {
	status, n, noop := c.status, len(c.messages), c.noop
	c.status, c.noop = "", false
	c.correctOne(ref, field, corrected, evidence)
	got, gotNoop := c.status, c.noop
	msgs := slices.Clone(c.messages[n:])
	c.status, c.messages, c.noop = status, c.messages[:n], noop

	switch {
	case got == "" || got == StatusOK:
		return OutcomeApplied, msgs
	case got == StatusDuplicate && gotNoop:
		return OutcomeUnchanged, msgs
	case got == StatusInvalid:
		return OutcomeInvalid, msgs
	default:
		// needs-human, or a duplicate that is NOT a no-op - an identifier already
		// recorded on another record, which is a disagreement for a maintainer.
		return OutcomeNeedsHuman, msgs
	}
}
