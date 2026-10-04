package audit

import (
	"bytes"
	"cmp"
	_ "embed"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/kodestar/audiosilo-meta/internal/importer"
	"github.com/kodestar/audiosilo-meta/pkg/canonical"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// The ONE reviewed-decision list is audit policy, not catalogue data. Embedding it
// makes metarepair's fresh Analyze inherit every review without an optional flag.
//
// Identity is (op, target, series, field, from, to, others), the minimal complete
// set of Proposal's identity fields: op selects the action; target and series its
// records; field distinguishes edits on one record; from/to distinguish both the
// precondition and replacement; others distinguishes merge clusters, split members
// and membership destinations/evidence. Advisory and Reason are judgements, not
// identity. Empty fields are omitted, never wildcards. Others is the sorted set the
// detectors emit, re-sorted after redirects.
// Class, subclass, finding key and notes are evidence, not proposal identity - so a
// `review` rejection meets every class's review finding of the same identity (the
// class only picks the slug namespace). That is harmless: a review is advisory already,
// the match only adds the note, and a review can never be accepted.
//
// Slug-valued fields resolve through the appropriate tombstone namespace; literal
// titles, positions and languages never do. Entries retain the reviewed spelling.
// Reviews converging after redirects keep all distinct reasons in file order. If
// opposite decisions converge, rejection wins and the acceptance is refused.
// STALE means no fresh proposal matches, not an error or permission to apply it.
// Ambiguity can make a decision temporarily stale: remove it only after review.
//
// A third decision, ASSERT, SOURCES a proposal no detector can see (an alternate title,
// a reissue, a work stating no series): the entry's identity IS the proposal, emitted
// non-advisory in its op's class under the subclass `asserted`. It is a human decision,
// so no detector veto is asked; only the records must exist, and a membership's slot be
// free (the one plan-time refusal metarepair would make every run), while metarepair's
// other plan-time refusals and post-write validation still apply. A detector already proposing the same
// identity turns it into an acceptance (REDUNDANT): like an accept, it promotes that
// proposal even where the detector made it advisory, overriding the detector's veto. Once applied its records are
// retired or joined, so it reads STALE and a re-run proposes nothing.
//
//go:embed reviewed.json
var reviewedFile []byte

const reviewedPath = "internal/audit/reviewed.json"

type reviewedDecision struct {
	Op       string   `json:"op"`
	Target   string   `json:"target,omitempty"`
	Series   string   `json:"series,omitempty"`
	Field    string   `json:"field,omitempty"`
	From     string   `json:"from,omitempty"`
	To       string   `json:"to,omitempty"`
	Others   []string `json:"others,omitempty"`
	Decision string   `json:"decision"`
	Reason   string   `json:"reason"`
}

type proposalKey struct{ op, target, series, field, from, to, others string }

func keyOf(p Proposal) proposalKey {
	// Others contains slugs, which cannot contain commas.
	return proposalKey{p.Op, p.Target, p.Series, p.Field, p.From, p.To, strings.Join(p.Others, ",")}
}

// assertClass is the class an asserted proposal is emitted in: the class whose detector
// makes proposals of that op, so a reader and metarepair's --op/--subclass meet it where
// they already look. Its keys are the only ops an assertion may source.
var assertClass = map[string]string{
	OpMergeWorks:      ClassWorkDup,
	OpMergeSeries:     ClassSeriesDup,
	OpAddSeriesMember: ClassWorkNoSeries,
}

// subclassAsserted is the subclass an asserted proposal is emitted under.
const subclassAsserted = "asserted"

func (r reviewedDecision) proposal() Proposal {
	p := Proposal{Op: r.Op, Target: r.Target, Series: r.Series, Field: r.Field, From: r.From, To: r.To, Others: r.Others}
	// W-NOSERIES states every membership it adds with field "series"; an assertion may
	// omit it, and is read with it, so its identity meets the detector's. (An accept or
	// reject copies a proposal, so its field is taken as written.)
	if r.Decision == "assert" && p.Op == OpAddSeriesMember && p.Field == "" {
		p.Field = "series"
	}
	return p
}

func decisionCmp(a, b reviewedDecision) int {
	return cmp.Or(cmp.Compare(a.Op, b.Op), cmp.Compare(a.Target, b.Target), cmp.Compare(a.Series, b.Series),
		cmp.Compare(a.Field, b.Field), cmp.Compare(a.From, b.From), cmp.Compare(a.To, b.To), slices.Compare(a.Others, b.Others))
}

var reviewedDecisions = mustParseReviewed(reviewedFile)

func mustParseReviewed(raw []byte) []reviewedDecision {
	rs, err := parseReviewed(raw)
	if err != nil {
		panic(fmt.Sprintf("audit: %s: %v", reviewedPath, err))
	}
	return rs
}

func knownReviewOp(op string) bool {
	_, ok := opPhrase[op]
	return op != OpNone && ok
}

// unappliableOps are the ops the audit emits that no repair carries out (a review, a
// rename candidate, a sidecar re-point: each names a decision a rule may not make). An
// acceptance of one would be counted as made mechanical while nothing applies it, so
// it is refused at parse. internal/repair's TestAcceptableOpsAreAppliable pins this set
// against the ops the repair can apply.
var unappliableOps = map[string]bool{OpReview: true, OpRenameCandidate: true, OpRepointSidecar: true}

// AcceptableOp reports whether a reviewed decision may ACCEPT a proposal of op.
// Ops lists every op the audit emits, so a test can hold this to the repair's appliers.
func AcceptableOp(op string) bool { return knownReviewOp(op) && !unappliableOps[op] }

// Ops returns every op the audit emits, sorted. opPhrase renders every one of them,
// review included, so it is the list.
func Ops() []string {
	var out []string
	for op := range opPhrase {
		if op != OpNone {
			out = append(out, op)
		}
	}
	slices.Sort(out)
	return out
}

func parseReviewed(raw []byte) ([]reviewedDecision, error) {
	if ok, err := canonical.IsCanonical(raw); err != nil {
		return nil, err
	} else if !ok {
		return nil, fmt.Errorf("not in canonical form (including no repeated key): re-render it canonically")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var rs []reviewedDecision
	if err := dec.Decode(&rs); err != nil {
		return nil, err
	}
	if rs == nil {
		return nil, fmt.Errorf("expected an array of decisions")
	}
	for i, r := range rs {
		if !knownReviewOp(r.Op) {
			return nil, fmt.Errorf("entry %d: unknown op %q", i, r.Op)
		}
		if r.Decision != "accept" && r.Decision != "reject" && r.Decision != "assert" {
			return nil, fmt.Errorf("entry %d: bad decision %q", i, r.Decision)
		}
		if r.Decision == "assert" {
			if err := validAssertion(r); err != nil {
				return nil, fmt.Errorf("entry %d: %w", i, err)
			}
		}
		// A review (or any op no repair carries out) has no mechanical action, so
		// promoting one would be counted as made mechanical while nothing applies it.
		if r.Decision == "accept" && !AcceptableOp(r.Op) {
			return nil, fmt.Errorf("entry %d: no repair applies a %q proposal, so it cannot be accepted", i, r.Op)
		}
		if r.Reason == "" || strings.TrimSpace(r.Reason) != r.Reason || strings.ContainsAny(r.Reason, "\n\r\u2013\u2014") {
			return nil, fmt.Errorf("entry %d: the reason must be a one-line, trimmed reason in hyphens", i)
		}
		if !slices.Equal(r.Others, sortedUnique(r.Others)) {
			return nil, fmt.Errorf("entry %d: others must be sorted, unique slugs", i)
		}
		for _, slug := range append([]string{r.Target, r.Series}, r.Others...) {
			if slug != "" && !model.ValidSlug(slug) {
				return nil, fmt.Errorf("entry %d: %q is not a slug", i, slug)
			}
		}
		if r.Op == OpAddWorkLink || r.Op == OpAddSeriesLink {
			if !model.ValidSlug(r.Target) || !model.ValidSlug(r.To) {
				return nil, fmt.Errorf("entry %d: link side is not a slug", i)
			}
			if r.Target == r.To {
				return nil, fmt.Errorf("entry %d: both link sides are %q", i, r.Target)
			}
		}
		if i > 0 && decisionCmp(rs[i-1], r) >= 0 {
			return nil, fmt.Errorf("entry %d: list must be sorted by (op, target, series, field, from, to, others) with no duplicates", i)
		}
	}
	back, err := json.Marshal(rs)
	if err != nil {
		return nil, err
	}
	back, err = canonical.Format(back)
	if err != nil {
		return nil, err
	}
	// encoding/json otherwise silently accepts case-variant field names.
	if !bytes.Equal(raw, back) {
		return nil, fmt.Errorf("entries must carry exactly the keys declared in reviewed.go, in lower case, omitting empty optional fields")
	}
	// An assertion may omit a membership's field, so two entries the sort tells apart
	// can still be ONE proposal: refuse that as the duplicate it is.
	seen := make(map[proposalKey]int, len(rs))
	for i, r := range rs {
		k := keyOf(r.proposal())
		if prev, dup := seen[k]; dup {
			return nil, fmt.Errorf("entry %d: the same proposal as entry %d", i, prev)
		}
		seen[k] = i
	}
	// A decision naming no record matches EVERY proposal of its op that names none
	// either (W-DUP's volume-conflict reviews carry no identity at all), so it can
	// never mean the one finding that was reviewed.
	for i, r := range rs {
		if r.Target == "" && r.Series == "" && len(r.Others) == 0 {
			return nil, fmt.Errorf("entry %d: names no record (target, series or others): it would match every such %q proposal", i, r.Op)
		}
	}
	return rs, nil
}

// validAssertion is the structural rule an assertion must meet, since nothing a
// detector checks stands behind it: a merge names a target and the distinct records
// folding onto it, and a membership a canonical position in a series. Every other
// identity field must be empty, so the entry spells the proposal exactly as a
// detector would and a detector making the same proposal is found as redundant.
func validAssertion(r reviewedDecision) error {
	if _, ok := assertClass[r.Op]; !ok {
		return fmt.Errorf("a %q proposal cannot be asserted (want %s, %s or %s)", r.Op, OpAddSeriesMember, OpMergeSeries, OpMergeWorks)
	}
	if r.Op == OpAddSeriesMember {
		if r.Target == "" || r.Series == "" || (r.Field != "" && r.Field != "series") || r.From != "" || len(r.Others) > 0 {
			return fmt.Errorf("an asserted %s names target, series and to (field, if stated, \"series\"), nothing else", r.Op)
		}
		if !canonicalPosition(r.To) {
			return fmt.Errorf("asserted position %q is not a canonical series position", r.To)
		}
		return nil
	}
	if r.Target == "" || len(r.Others) == 0 || r.Series != "" || r.Field != "" || r.From != "" || r.To != "" {
		return fmt.Errorf("an asserted %s names a target and others, nothing else", r.Op)
	}
	if slices.Contains(r.Others, r.Target) {
		return fmt.Errorf("an asserted %s folds %q onto itself", r.Op, r.Target)
	}
	return nil
}

// retiredElsewhere names the losers of a merge assertion an earlier wave retired onto a
// record OTHER than its (resolved) target. Only a loser resolving to the target itself
// has been applied; one resolving elsewhere would silently widen the merge to fold
// that other survivor too.
func retiredElsewhere(r reviewedDecision, target string, reds model.Redirects) []string {
	if r.Op != OpMergeWorks && r.Op != OpMergeSeries {
		return nil
	}
	kind := model.RedirectWorks
	if r.Op == OpMergeSeries {
		kind = model.RedirectSeries
	}
	var out []string
	for _, o := range r.Others {
		if to, ok := reds.Survivor(kind, o); ok && to != target {
			out = append(out, o+" -> "+to)
		}
	}
	return out
}

func resolvedProposal(p Proposal, class string, reds model.Redirects) Proposal {
	live := func(kind model.RedirectKind, slug string) string {
		if to, ok := reds.Survivor(kind, slug); ok {
			return to
		}
		return slug
	}
	kind := model.RedirectWorks
	switch p.Op {
	case OpMergeSeries, OpSplitSeries:
		kind = model.RedirectSeries
	case OpReview:
		switch class {
		case ClassPersonDup:
			kind = model.RedirectPeople
		case ClassSeriesDup, ClassSeriesParen:
			kind = model.RedirectSeries
		case ClassSeriesInteg:
			if p.Field != "position" {
				kind = model.RedirectSeries
			}
		}
	}
	if linkKind, ok := linkOpKind(p.Op); ok {
		kind = linkKind
	}
	// Recording IDs are not work slugs and have no tombstone namespace.
	if p.Op != OpFillField || (p.Field != "narrators" && p.Field != "asin") {
		p.Target = live(kind, p.Target)
	}
	p.Series = live(model.RedirectSeries, p.Series)
	othersKind := kind
	switch p.Op {
	case OpMoveMembership, OpDropMembership:
		othersKind = model.RedirectSeries
	case OpSplitSeries, OpRestatePosition:
		othersKind = model.RedirectWorks
	case OpReview:
		if class == ClassLangMix && p.Series != "" {
			othersKind = model.RedirectSeries
		}
	}
	p.Others = slices.Clone(p.Others)
	for i, s := range p.Others {
		p.Others[i] = live(othersKind, s)
	}
	p.Others = sortedUnique(p.Others)
	switch p.Op {
	case OpAddWorkLink, OpAddSeriesLink:
		p.To = live(kind, p.To)
		if p.From != "" {
			parts := strings.Split(p.From, ",")
			for i, s := range parts {
				parts[i] = live(kind, s)
			}
			p.From = strings.Join(sortedUnique(parts), ",")
		}
	case OpRepointSidecar, OpRenameCandidate:
		p.From = live(model.RedirectWorks, p.From)
		p.To = live(model.RedirectWorks, p.To)
	case OpDropMembership:
		if p.Field == "work" {
			p.From = live(model.RedirectWorks, p.From)
		}
	}
	return p
}

// outcomeStatus is what one decision did to the fresh audit.
type outcomeStatus string

const (
	statusAccepted  outcomeStatus = "accepted"  // an advisory proposal made mechanical
	statusRejected  outcomeStatus = "rejected"  // a mechanical proposal made advisory
	statusAsserted  outcomeStatus = "asserted"  // a proposal sourced, mechanical
	statusRedundant outcomeStatus = "redundant" // an assertion a detector already makes, taken as an acceptance
	statusNoOp      outcomeStatus = "no-op"     // the proposal was already in the state asked for
	statusRefused   outcomeStatus = "refused"   // an acceptance or assertion that would break consistency
	statusStale     outcomeStatus = "stale"     // matching no fresh proposal
	// statusWithholds is a rejection whose only match is an assertion of the same
	// proposal: it is what keeps the assertion out, so it is never stale.
	statusWithholds outcomeStatus = "rejected-an-assertion"
)

// listed reports whether SUMMARY.md names the decision one by one: the outcomes a
// reviewer has to act on (STALE ones are listed in their own section).
func (s outcomeStatus) listed() bool {
	return slices.Contains([]outcomeStatus{statusNoOp, statusRefused, statusRedundant, statusWithholds}, s)
}

type decisionOutcome struct {
	Entry  reviewedDecision
	Status outcomeStatus
	// Why names a refusal's conflict, a redundant assertion's detector proposal, or a
	// stale assertion's gone record (or that it has been applied).
	Why string
}

// reviewedTally is every decision's outcome, in file order.
type reviewedTally struct{ All []decisionOutcome }

// Outcomes are the decisions that met a fresh proposal; Stale the ones that did not.
func (t reviewedTally) Outcomes() []decisionOutcome { return t.filter(false) }
func (t reviewedTally) Stale() []decisionOutcome    { return t.filter(true) }
func (t reviewedTally) Entries() int                { return len(t.All) }

func (t reviewedTally) filter(stale bool) []decisionOutcome {
	var out []decisionOutcome
	for _, o := range t.All {
		if (o.Status == statusStale) == stale {
			out = append(out, o)
		}
	}
	return out
}

// applyReviewed runs ONCE over all classes, after detection and before rendering.
// Assertions no detector meets are sourced first, as advisory findings of their op's
// class, so every later step treats them as it treats a detector's. Rejections then
// run, to release conflicts. Acceptances and assertions run in file order and may only
// extend a consistent mechanical set; conflicting promotions stay advisory and report
// why, and a sourced finding left advisory is withdrawn. Repair's plan-time rules
// still apply. ix is the catalogue an assertion's records are looked up in.
func applyReviewed(rep *Report, rs []reviewedDecision, reds model.Redirects, ix *index) reviewedTally {
	// Only OpReview depends on the finding's class. Resolve all other decisions
	// once, retaining file order when redirects make several keys converge.
	byKey := make(map[proposalKey][]int, len(rs))
	byClass := make(map[string]map[proposalKey][]int)
	resolved := make([]Proposal, len(rs))
	statuses := make([]outcomeStatus, len(rs))
	whys := make([]string, len(rs))
	for j, r := range rs {
		if r.Op != OpReview {
			resolved[j] = resolvedProposal(r.proposal(), "", reds)
			if r.Decision == "assert" {
				if elsewhere := retiredElsewhere(r, resolved[j].Target, reds); len(elsewhere) > 0 {
					// Never widened: folding the other survivor onto the target is a decision
					// nobody reviewed. It matches nothing and sources nothing.
					statuses[j] = statusRefused
					whys[j] = "a record it folds was merged into another survivor since: " + strings.Join(elsewhere, ", ")
					continue
				}
				// A loser an earlier wave retired onto the target is no longer folded: the
				// assertion's proposal is the rest of its cluster, which is what a detector,
				// a converging assertion and the sourced finding all spell.
				resolved[j].Others = slices.DeleteFunc(resolved[j].Others, func(o string) bool { return o == resolved[j].Target })
			}
			key := keyOf(resolved[j])
			byKey[key] = append(byKey[key], j)
		}
	}
	// Matches are row positions, not pointers, until sourcing has appended its rows.
	type row struct {
		c *findings
		i int
	}
	matched := make([][]row, len(rs))
	for _, c := range rep.classes {
		for i := range c.rows {
			fd := &c.rows[i]
			index := byKey
			if fd.Propose.Op == OpReview {
				index = byClass[c.class]
				if index == nil {
					index = make(map[proposalKey][]int)
					for j, r := range rs {
						if r.Op == OpReview {
							key := keyOf(resolvedProposal(r.proposal(), c.class, reds))
							index[key] = append(index[key], j)
						}
					}
					byClass[c.class] = index
				}
			}
			for _, j := range index[keyOf(resolvedProposal(fd.Propose, c.class, reds))] {
				matched[j] = append(matched[j], row{c, i})
			}
		}
	}
	src := sourcing{rep: rep, ix: ix}
	for j, r := range rs {
		if r.Decision != "assert" || len(matched[j]) > 0 || statuses[j] != "" {
			continue
		}
		key := keyOf(resolved[j])
		if slices.ContainsFunc(byKey[key], func(o int) bool { return rs[o].Decision == "reject" }) {
			statuses[j], whys[j] = statusRefused, "a reviewed rejection resolves to the same proposal"
			for _, o := range byKey[key] {
				if rs[o].Decision == "reject" {
					statuses[o], whys[o] = statusWithholds, "withholds the reviewed assertion of the same proposal"
				}
			}
			continue
		}
		c, i, status, why := src.source(r, resolved[j])
		if c == nil {
			statuses[j], whys[j] = status, why
			continue
		}
		// Every assertion converging on this identity meets the one sourced finding: the
		// first in file order promotes it, the rest find it made already.
		for _, o := range byKey[key] {
			if rs[o].Decision == "assert" {
				matched[o] = append(matched[o], row{c, i})
			}
		}
	}
	matches := make([][]*Finding, len(rs))
	byFinding := map[*Finding][]int{}
	for j, rows := range matched {
		for _, m := range rows {
			fd := &m.c.rows[m.i]
			matches[j] = append(matches[j], fd)
			byFinding[fd] = append(byFinding[fd], j)
		}
	}
	// Iterate findings in report order, not pointer-map order.
	for _, c := range rep.classes {
		for i := range c.rows {
			fd := &c.rows[i]
			js := byFinding[fd]
			var reasons []string
			var rejects []int
			for _, j := range js {
				if rs[j].Decision == "reject" {
					rejects = append(rejects, j)
					if !slices.Contains(reasons, rs[j].Reason) {
						reasons = append(reasons, rs[j].Reason)
					}
				}
			}
			if len(rejects) == 0 {
				continue
			}
			status := statusRejected
			if fd.Propose.Advisory {
				status = statusNoOp
			}
			for _, j := range rejects {
				statuses[j] = status
			}
			note := "reviewed and rejected: " + strings.Join(reasons, "; ")
			fd.Notes = append(fd.Notes, note)
			if fd.Propose.Advisory && fd.Propose.Reason != "" {
				note += "; " + fd.Propose.Reason
			}
			fd.Propose.Advisory = true
			fd.Propose.Reason = note
		}
	}
	// One decision can meet several findings: the tally reports a refusal on any of
	// them, then a promotion (accepted or asserted), then a decision that changed
	// nothing (no-op or redundant), whatever order the findings came in.
	rank := map[outcomeStatus]int{statusNoOp: 1, statusRedundant: 1, statusAccepted: 2, statusAsserted: 2, statusRefused: 3}
	setStatus := func(j int, status outcomeStatus) {
		if rank[status] >= rank[statuses[j]] {
			statuses[j] = status
		}
	}
	var conflictState *proposalConflictState
	for j, r := range rs {
		if r.Decision == "reject" {
			continue
		}
		for _, fd := range matches[j] {
			why := ""
			for _, other := range byFinding[fd] {
				if rs[other].Decision == "reject" {
					why = "a reviewed rejection resolves to the same proposal"
				}
			}
			// Only the label differs between an acceptance, an assertion sourcing its own
			// finding, and one a detector (or a converging assertion) already made.
			sourced := fd.Subclass == subclassAsserted
			var promoted, unchanged outcomeStatus
			switch {
			case r.Decision == "accept":
				promoted, unchanged = statusAccepted, statusNoOp
			case sourced:
				promoted, unchanged = statusAsserted, statusRedundant
			default:
				promoted, unchanged = statusRedundant, statusRedundant
			}
			if why == "" && fd.Propose.Advisory {
				if conflictState == nil {
					conflictState = proposalConflicts(rep)
				}
				if conflicts := conflictState.promote(*fd); len(conflicts) > 0 {
					why = strings.Join(conflicts, "; ")
				} else {
					fd.Propose.Advisory = false
					setStatus(j, promoted)
				}
			} else if why == "" {
				setStatus(j, unchanged)
			}
			note := "reviewed and accepted: " + r.Reason
			switch {
			case why != "":
				statuses[j] = statusRefused
				whys[j] = why
				note = "reviewed acceptance refused: " + why + "; review: " + r.Reason
			case statuses[j] == statusAsserted:
				continue // the sourced finding's own reason and notes already say so
			case r.Decision == "assert":
				// SUMMARY.md says where and what to do. An acceptance meets only a
				// detector's proposal, so a duplicate of another assertion is dropped.
				if whys[j] == "" && sourced {
					whys[j] = "another assertion already sources it as " + fd.Class + " " + fd.Key + ": drop one of them"
				} else if whys[j] == "" {
					whys[j] = "already proposed as " + fd.Class + " " + fd.Key + ": rewrite it as an accept"
				}
				note = "reviewed assertion, already proposed, taken as an acceptance: " + r.Reason
			}
			fd.Notes = append(fd.Notes, note)
			if fd.Propose.Reason != "" {
				fd.Propose.Reason += "; "
			}
			fd.Propose.Reason += note
		}
	}
	// A sourced finding no assertion could make mechanical stays out of the report:
	// it exists only as the assertion, whose refusal SUMMARY.md names.
	for _, c := range rep.classes {
		c.rows = slices.DeleteFunc(c.rows, func(fd Finding) bool { return fd.Subclass == subclassAsserted && fd.Propose.Advisory })
	}
	var t reviewedTally
	for j, r := range rs {
		if statuses[j] == "" {
			statuses[j] = statusStale
		}
		t.All = append(t.All, decisionOutcome{r, statuses[j], whys[j]})
	}
	return t
}

// sourcing adds the findings reviewed assertions state and no detector makes.
type sourcing struct {
	rep  *Report
	ix   *index
	keys map[string]bool // class + key of every finding, so a sourced key is unique
}

// source appends one assertion's proposal p (resolved, its target out of its others)
// to its op's class as an ADVISORY finding the acceptance loop then promotes,
// returning where it sits; or, with a nil class, the status and why: STALE when a
// record it names is gone or it has been applied (a merge's others all resolve to its
// target, a membership is listed), REFUSED when the series holds its position already.
func (s *sourcing) source(r reviewedDecision, p Proposal) (c *findings, i int, status outcomeStatus, why string) {
	if s.ix == nil {
		return nil, 0, statusStale, "no catalogue to find its records in"
	}
	if p.Op != OpAddSeriesMember && len(p.Others) == 0 {
		return nil, 0, statusStale, "applied: every record it folds now resolves to " + p.Target
	}
	var works, series []string
	switch p.Op {
	case OpMergeWorks:
		works = Cluster(p.Target, p.Others)
	case OpMergeSeries:
		series = Cluster(p.Target, p.Others)
	case OpAddSeriesMember:
		works, series = []string{p.Target}, []string{p.Series}
	}
	fd := Finding{Subclass: subclassAsserted,
		Notes: []string{"no detector proposes this: a reviewed assertion in " + reviewedPath + " sources it, and no detector veto was asked"}}
	var missing []string
	for _, id := range works {
		if w := s.ix.workByID[id]; w != nil {
			fd.Works = append(fd.Works, s.ix.workBrief(w))
		} else {
			missing = append(missing, "work "+id)
		}
	}
	for _, id := range series {
		if se := s.ix.seriesByID[id]; se != nil {
			fd.Series = append(fd.Series, s.ix.seriesRef(se))
		} else {
			missing = append(missing, "series "+id)
		}
	}
	if len(missing) > 0 {
		return nil, 0, statusStale, "no live " + strings.Join(missing, ", ")
	}
	if p.Op == OpAddSeriesMember {
		for _, m := range s.ix.memberships[p.Target] {
			if m.series != p.Series {
				continue
			}
			if importer.SameSlot(m.position, p.To) {
				return nil, 0, statusStale, fmt.Sprintf("applied: series %s lists %s at position %q", p.Series, p.Target, m.position)
			}
			return nil, 0, statusRefused, fmt.Sprintf("series %s lists %s at position %q, not %q: moving it is a restate, not an addition",
				p.Series, p.Target, m.position, p.To)
		}
		// metarepair refuses a held slot on every run, so the audit says so once here.
		for _, sw := range s.ix.seriesByID[p.Series].Works {
			if importer.SameSlot(sw.Position, p.To) {
				return nil, 0, statusRefused, fmt.Sprintf("position %q of series %s is held by %s", p.To, p.Series, sw.Work)
			}
		}
	}
	class := assertClass[p.Op]
	if s.keys == nil {
		s.keys = map[string]bool{}
		for _, c := range s.rep.classes {
			for _, fd := range c.rows {
				s.keys[c.class+" "+fd.Key] = true
			}
		}
	}
	base := "asserted/" + p.Target
	if p.Op == OpAddSeriesMember {
		base += "@" + p.Series
	}
	fd.Key = base
	for n := 2; s.keys[class+" "+fd.Key]; n++ {
		fd.Key = fmt.Sprintf("%s/%d", base, n)
	}
	s.keys[class+" "+fd.Key] = true
	p.Advisory = true
	p.Reason = "asserted by review: " + r.Reason
	fd.Propose = p
	c = s.rep.class(class)
	if !slices.Contains(s.rep.classes, c) {
		s.rep.classes = append(s.rep.classes, c)
	}
	c.add(fd)
	return c, len(c.rows) - 1, "", ""
}
