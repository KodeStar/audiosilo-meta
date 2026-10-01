package audit

import (
	"bytes"
	"cmp"
	_ "embed"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

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
// Class, subclass, finding key and notes are evidence, not proposal identity.
//
// Slug-valued fields resolve through the appropriate tombstone namespace; literal
// titles, positions and languages never do. Entries retain the reviewed spelling.
// Reviews converging after redirects keep all distinct reasons in file order. If
// opposite decisions converge, rejection wins and the acceptance is refused.
// STALE means no fresh proposal matches, not an error or permission to apply it.
// Ambiguity can make a decision temporarily stale: remove it only after review.
//
//go:embed reviewed.json
var reviewedFile []byte

const reviewedPath = "internal/audit/reviewed.json"

// Kept only as a report-format compatibility label for migrated link rejections.
// There is no second file, loader or decision list at this path.
const legacyRejectionsPath = "internal/audit/tlink_rejected.json"

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
	// JSON encoding is unambiguous even if an identity string contains delimiters.
	others, _ := json.Marshal(p.Others)
	if len(p.Others) == 0 {
		others = nil
	}
	return proposalKey{p.Op, p.Target, p.Series, p.Field, p.From, p.To, string(others)}
}

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
	switch op {
	case OpMergeWorks, OpMergeSeries, OpRetitle, OpAddSeriesMember, OpRestatePosition,
		OpDropMembership, OpFillField, OpRenameCandidate, OpRepointSidecar, OpAddWorkLink,
		OpAddSeriesLink, OpMoveMembership, OpSplitSeries, OpSetWorkLanguage, OpReview:
		return true
	}
	return false
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
		if r.Decision != "accept" && r.Decision != "reject" {
			return nil, fmt.Errorf("entry %d: bad decision %q", i, r.Decision)
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
	return rs, nil
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
	case OpMergeSeries, OpAddSeriesLink, OpSplitSeries:
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
	Matched  int
	Stale    []reviewedDecision
	Outcomes []decisionOutcome
	Legacy   bool
}

func (t reviewedTally) Entries() int { return t.Matched + len(t.Stale) }

func legacyRejection(r reviewedDecision) bool {
	return r.Decision == "reject" && (r.Op == OpAddWorkLink || r.Op == OpAddSeriesLink) &&
		r.Field == "translation_of" && r.From == "" && r.Series == "" && len(r.Others) == 0
}

// applyReviewed runs ONCE over all classes, after detection and before rendering.
// Rejections run first to release conflicts. Acceptances run in file order and may
// only extend a consistent mechanical set. Each promotion is tentative: if it adds
// a conflict, restore advisory and report why. Repair's plan-time rules still apply.
func applyReviewed(rep *Report, rs []reviewedDecision, reds model.Redirects) reviewedTally {
	matches := make([][]*Finding, len(rs))
	byFinding := map[*Finding][]int{}
	for _, c := range rep.classes {
		for i := range c.rows {
			fd := &c.rows[i]
			key := keyOf(resolvedProposal(fd.Propose, c.class, reds))
			for j, r := range rs {
				if r.Op == fd.Propose.Op && keyOf(resolvedProposal(r.proposal(), c.class, reds)) == key {
					matches[j] = append(matches[j], fd)
					byFinding[fd] = append(byFinding[fd], j)
				}
			}
		}
	}
	t := reviewedTally{Legacy: true}
	statuses := make([]string, len(rs))
	whys := make([]string, len(rs))
	// Iterate findings in report order, not pointer-map order.
	for _, c := range rep.classes {
		for i := range c.rows {
			fd := &c.rows[i]
			js := byFinding[fd]
			var reasons []string
			var rejects []int
			legacy := true
			for _, j := range js {
				if rs[j].Decision == "reject" {
					rejects = append(rejects, j)
					legacy = legacy && legacyRejection(rs[j])
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
			if legacy {
				note = "reviewed and rejected (" + legacyRejectionsPath + "): " + strings.Join(reasons, "; ")
			} else {
				fd.Notes = append(fd.Notes, note)
			}
			if fd.Propose.Advisory && fd.Propose.Reason != "" {
				note += "; " + fd.Propose.Reason
			}
			fd.Propose.Advisory = true
			fd.Propose.Reason = note
		}
	}
	for j, r := range rs {
		if r.Decision != "accept" {
			continue
		}
		for _, fd := range matches[j] {
			why := ""
			for _, other := range byFinding[fd] {
				if rs[other].Decision == "reject" {
					why = "a reviewed rejection resolves to the same proposal"
				}
			}
			if why == "" && fd.Propose.Advisory {
				fd.Propose.Advisory = false
				if conflicts := proposalConflicts(rep); len(conflicts) > 0 {
					why = strings.Join(conflicts, "; ")
					fd.Propose.Advisory = true
				}
				if why == "" {
					statuses[j] = "accepted"
				}
			} else if why == "" {
				statuses[j] = "no-op"
			}
			note := "reviewed and accepted: " + r.Reason
			if why != "" {
				statuses[j] = "refused"
				whys[j] = why
				note = "reviewed acceptance refused: " + why + "; review: " + r.Reason
			}
			fd.Notes = append(fd.Notes, note)
			if fd.Propose.Reason != "" {
				fd.Propose.Reason += "; "
			}
			fd.Propose.Reason += note
		}
	}
	for j, r := range rs {
		t.Legacy = t.Legacy && legacyRejection(r) && (statuses[j] == "rejected" || len(matches[j]) == 0)
		if len(matches[j]) == 0 {
			t.Stale = append(t.Stale, r)
		} else {
			t.Matched++
			t.Outcomes = append(t.Outcomes, decisionOutcome{r, statuses[j], whys[j]})
		}
	}
	return t
}
