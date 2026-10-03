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
// so no detector veto is asked; only the records must exist, and metarepair's plan-time
// refusals and post-write validation still apply. A detector already proposing the same
// identity turns it into an acceptance (REDUNDANT), and once applied its records are
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
	return Proposal{Op: r.Op, Target: r.Target, Series: r.Series, Field: r.Field, From: r.From, To: r.To, Others: r.Others}
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
		if r.Target == "" || r.Series == "" || r.Field != "series" || r.From != "" || len(r.Others) > 0 {
			return fmt.Errorf("an asserted %s names target, series, field \"series\" and to, nothing else", r.Op)
		}
		if pos, ok := importer.NormalizeSequence(r.To); !ok || pos != r.To {
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

type decisionOutcome struct {
	Entry       reviewedDecision
	Status, Why string
}
type reviewedTally struct {
	// Stale are the decisions no fresh proposal matched (Status "stale"); for an
	// assertion, Why names the record that is gone or says it has been applied.
	Stale    []decisionOutcome
	Outcomes []decisionOutcome
}

func (t reviewedTally) Entries() int { return len(t.Outcomes) + len(t.Stale) }

// applyReviewed runs ONCE over all classes, after detection and before rendering.
// Rejections run first to release conflicts. Acceptances and assertions run in file
// order and may only extend a consistent mechanical set. Conflicting promotions stay
// advisory and report why. Repair's plan-time rules still apply. ix is the catalogue
// an assertion's records are looked up in (nil holds none).
func applyReviewed(rep *Report, rs []reviewedDecision, reds model.Redirects, ix *index) reviewedTally {
	// Only OpReview depends on the finding's class. Resolve all other decisions
	// once, retaining file order when redirects make several keys converge.
	byKey := make(map[proposalKey][]int, len(rs))
	byClass := make(map[string]map[proposalKey][]int)
	for j, r := range rs {
		if r.Op != OpReview {
			key := keyOf(resolvedProposal(r.proposal(), "", reds))
			byKey[key] = append(byKey[key], j)
		}
	}
	matches := make([][]*Finding, len(rs))
	byFinding := map[*Finding][]int{}
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
			key := keyOf(resolvedProposal(fd.Propose, c.class, reds))
			js := index[key]
			for _, j := range js {
				matches[j] = append(matches[j], fd)
			}
			if len(js) > 0 {
				byFinding[fd] = js
			}
		}
	}
	var t reviewedTally
	statuses := make([]string, len(rs))
	whys := make([]string, len(rs))
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
			status := "rejected"
			if fd.Propose.Advisory {
				status = "no-op"
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
	// One decision can meet several findings: the tally reports a refusal on any
	// of them, then an acceptance, rather than whichever finding came last.
	setStatus := func(j int, status string) {
		if statuses[j] != "refused" && (statuses[j] != "accepted" || status == "refused") {
			statuses[j] = status
		}
	}
	var conflictState *proposalConflictState
	state := func() *proposalConflictState {
		if conflictState == nil {
			conflictState = proposalConflicts(rep)
		}
		return conflictState
	}
	// Sourced findings join their classes only after this loop: appending while it runs
	// could move a class's rows out from under the pointers matches holds.
	as := &assertions{rep: rep, ix: ix, reds: reds, state: state, by: map[proposalKey]string{}}
	for j, r := range rs {
		if r.Decision == "assert" && len(matches[j]) == 0 {
			key := keyOf(resolvedProposal(r.proposal(), "", reds))
			rejected := slices.ContainsFunc(byKey[key], func(o int) bool { return rs[o].Decision == "reject" })
			statuses[j], whys[j] = as.source(r, key, rejected)
			continue
		}
		if r.Decision != "accept" && r.Decision != "assert" {
			continue
		}
		// A detector already makes the asserted proposal: the assertion is an acceptance
		// of it, and SUMMARY.md says so, so the entry can be rewritten as one.
		accepted, noop, verb := "accepted", "no-op", "reviewed and accepted: "
		if r.Decision == "assert" {
			accepted, noop, verb = "redundant", "redundant", "reviewed assertion, already proposed, taken as an acceptance: "
		}
		for _, fd := range matches[j] {
			why := ""
			for _, other := range byFinding[fd] {
				if rs[other].Decision == "reject" {
					why = "a reviewed rejection resolves to the same proposal"
				}
			}
			if why == "" && fd.Propose.Advisory {
				if conflicts := state().promote(*fd); len(conflicts) > 0 {
					why = strings.Join(conflicts, "; ")
				} else {
					fd.Propose.Advisory = false
					setStatus(j, accepted)
				}
			} else if why == "" {
				setStatus(j, noop)
			}
			note := verb + r.Reason
			if why != "" {
				statuses[j] = "refused"
				whys[j] = why
				note = "reviewed acceptance refused: " + why + "; review: " + r.Reason
			} else if r.Decision == "assert" && whys[j] == "" {
				whys[j] = "already proposed as " + fd.Class + " " + fd.Key
			}
			fd.Notes = append(fd.Notes, note)
			if fd.Propose.Reason != "" {
				fd.Propose.Reason += "; "
			}
			fd.Propose.Reason += note
		}
	}
	for _, fd := range as.sourced {
		c := rep.class(fd.Class)
		if !slices.Contains(rep.classes, c) {
			rep.classes = append(rep.classes, c)
		}
		c.rows = append(c.rows, fd)
	}
	for j, r := range rs {
		o := decisionOutcome{r, statuses[j], whys[j]}
		if o.Status == "stale" || (o.Status == "" && len(matches[j]) == 0) {
			o.Status = "stale"
			t.Stale = append(t.Stale, o)
		} else {
			t.Outcomes = append(t.Outcomes, o)
		}
	}
	return t
}

// assertions sources the proposals reviewed assertions state and no detector makes.
type assertions struct {
	rep     *Report
	ix      *index
	reds    model.Redirects
	state   func() *proposalConflictState
	sourced []Finding
	by      map[proposalKey]string // resolved identity -> the sourced finding's key
}

// source emits one assertion as a non-advisory finding of its op's class, or says why
// it cannot: STALE when a record it names is gone or it has been applied (a merge's
// others all resolve to its target, a membership is already listed), REFUSED when it
// would break the mechanical set's consistency. key is its resolved identity.
func (a *assertions) source(r reviewedDecision, key proposalKey, rejected bool) (status, why string) {
	if rejected {
		return "refused", "a reviewed rejection resolves to the same proposal"
	}
	if by, dup := a.by[key]; dup {
		return "redundant", "another assertion resolves to the same proposal (" + by + ")"
	}
	p := resolvedProposal(r.proposal(), "", a.reds)
	p.Others = slices.DeleteFunc(p.Others, func(o string) bool { return o == p.Target })
	if p.Op != OpAddSeriesMember && len(p.Others) == 0 {
		return "stale", "applied: every record it folds now resolves to " + p.Target
	}
	class := assertClass[p.Op]
	fd := Finding{Class: class, Subclass: subclassAsserted,
		Notes: []string{"no detector proposes this: a reviewed assertion in " + reviewedPath + " sources it, and no detector veto was asked"}}
	var missing []string
	work := func(id string) {
		if w := a.workByID(id); w != nil {
			fd.Works = append(fd.Works, a.ix.workBrief(w))
		} else {
			missing = append(missing, "work "+id)
		}
	}
	series := func(id string) {
		if s := a.seriesByID(id); s != nil {
			fd.Series = append(fd.Series, a.ix.seriesRef(s))
		} else {
			missing = append(missing, "series "+id)
		}
	}
	switch p.Op {
	case OpMergeWorks:
		for _, id := range Cluster(p.Target, p.Others) {
			work(id)
		}
	case OpMergeSeries:
		for _, id := range Cluster(p.Target, p.Others) {
			series(id)
		}
	case OpAddSeriesMember:
		work(p.Target)
		series(p.Series)
	}
	if len(missing) > 0 {
		return "stale", "no live " + strings.Join(missing, ", ")
	}
	if p.Op == OpAddSeriesMember {
		for _, m := range a.ix.memberships[p.Target] {
			if m.series == p.Series {
				return "stale", fmt.Sprintf("applied: series %s lists %s at position %q", p.Series, p.Target, m.position)
			}
		}
	}
	base := "asserted/" + p.Target
	if p.Op == OpAddSeriesMember {
		base += "@" + p.Series
	}
	fd.Key = base
	for n := 2; a.keyTaken(class, fd.Key); n++ {
		fd.Key = fmt.Sprintf("%s/%d", base, n)
	}
	p.Advisory = false
	p.Reason = "asserted by review: " + r.Reason
	fd.Propose = p
	s := a.state()
	conflicts := s.overlaps(fd)
	if len(conflicts) == 0 {
		conflicts = s.promote(fd)
	}
	if len(conflicts) > 0 {
		return "refused", strings.Join(conflicts, "; ")
	}
	a.sourced = append(a.sourced, fd)
	a.by[key] = class + " " + fd.Key
	return "asserted", ""
}

func (a *assertions) workByID(id string) *model.Work {
	if a.ix == nil {
		return nil
	}
	return a.ix.workByID[id]
}

func (a *assertions) seriesByID(id string) *model.Series {
	if a.ix == nil {
		return nil
	}
	return a.ix.seriesByID[id]
}

// keyTaken reports whether a class already holds a finding under key, so a sourced
// finding never shares a detector's or another assertion's identity in a worklist.
func (a *assertions) keyTaken(class, key string) bool {
	return slices.ContainsFunc(a.rep.class(class).rows, func(fd Finding) bool { return fd.Key == key }) ||
		slices.ContainsFunc(a.sourced, func(fd Finding) bool { return fd.Class == class && fd.Key == key })
}
