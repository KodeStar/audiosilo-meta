package audit

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kodestar/audiosilo-meta/pkg/canonical"
	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// tlinkrejected.go is T-LINK's REVIEWED-REJECTION list: the link proposals a
// maintainer has reviewed and rejected, so a fresh audit stops re-proposing them as
// mechanical.
//
// WHY IT EXISTS: T-LINK proposes from stated evidence, and the evidence it cannot see
// is exactly what a review finds - an English "original" that is itself a translation
// (Coelho, Glukhovsky, Dicker), a link pointing the wrong way (Mahanenko's Russian
// originals, Allende's Paula), a direction nothing establishes (same-day bilingual
// releases), a two-book pack as the target, an adaptation risk, a base series mixing
// other authors' franchises. Languages waves 2 and 3 excluded 30 such proposals through
// a -report worklist, and a worklist records nothing: every later audit proposed the
// same 30 as mechanical again, and a wave run without that worklist would have applied
// them. This file is where the review's decision is written down.
//
// WHERE IT LIVES, and why there: an EMBEDDED file of this package, not data/. It is a
// maintainer's decision about the AUDIT, not a fact about a book - no pack accounting,
// no schema, nothing in the artifact. Embedded, metarepair (which re-runs these
// detectors in process through Analyze) inherits it with no flag to forget, and
// changing it is a code-reviewed change like any other rule here.
//
// THE KEY is one specific link: (op, id, target), where op is the proposal's own op
// (propose.op in T-LINK.ndjson, the value linkOps names and metarepair's --op takes),
// id is the TRANSLATION - the record the op writes, propose.target - and target is the
// proposed ORIGINAL, propose.to. id/target is the artifact's own vocabulary for a link
// (translations(kind, id, target)); the op rather than the family noun because the op is
// what a reviewer copies out of the report and what the match compares. A proposal for
// the same record naming a DIFFERENT original is not suppressed: the rejection is of
// one link, and a changed candidate deserves a fresh judgement.
//
// THE EFFECT is advisory, never absence: a matching proposal stays in T-LINK.ndjson,
// turned advisory, its reason saying it was reviewed and rejected and why. An entry
// matching no proposal is STALE and SUMMARY.md lists it by name for a cleanup - it
// fails nothing, since an entry goes stale legitimately when the tree moves (the link
// applied by hand, a record merged, the evidence changed).

//go:embed tlink_rejected.json
var linkRejectionsFile []byte

// linkRejectionsPath is the file's name as a report quotes it.
const linkRejectionsPath = "internal/audit/tlink_rejected.json"

// linkRejection is one reviewed-and-rejected link.
type linkRejection struct {
	Op     string `json:"op"`
	ID     string `json:"id"`
	Target string `json:"target"`
	Reason string `json:"reason"`
}

func (r linkRejection) key() linkKey { return linkKey{r.Op, r.ID, r.Target} }

// linkKey is what a rejection and a proposal are matched on.
type linkKey struct{ op, id, target string }

// linkRejections is the embedded list, parsed once. A file that does not parse is a
// build defect (TestLinkRejectionsFileIsValid names it), so it stops the process
// rather than auditing as though nothing had been reviewed.
var linkRejections = mustParseLinkRejections(linkRejectionsFile)

func mustParseLinkRejections(raw []byte) []linkRejection {
	rs, err := parseLinkRejections(raw)
	if err != nil {
		panic(fmt.Sprintf("audit: %s: %v", linkRejectionsPath, err))
	}
	return rs
}

// parseLinkRejections decodes and validates the list: no repeated key, canonical JSON,
// no unknown field, a known op, slug-shaped id and target that differ, a one-line
// reason in hyphens, and entries in strictly increasing (op, id, target) order - which
// is also what makes them unique.
func parseLinkRejections(raw []byte) ([]linkRejection, error) {
	// Duplicate keys first: canonical form keeps the last of a repeated key, so the
	// canonical test would name the wrong defect.
	if err := pack.CheckNoDuplicateKeys(raw); err != nil {
		return nil, err
	}
	if ok, err := canonical.IsCanonical(raw); err != nil {
		return nil, err
	} else if !ok {
		return nil, fmt.Errorf("not in canonical form (metafmt's rules: sorted keys, 2-space indent, trailing newline)")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var rs []linkRejection
	if err := dec.Decode(&rs); err != nil {
		return nil, err
	}
	known := map[string]bool{}
	for _, op := range linkOps {
		known[op] = true
	}
	for i, r := range rs {
		switch {
		case !known[r.Op]:
			return nil, fmt.Errorf("entry %d: op %q is not a link op", i, r.Op)
		case !model.ValidSlug(r.ID):
			return nil, fmt.Errorf("entry %d: id %q is not a slug", i, r.ID)
		case !model.ValidSlug(r.Target):
			return nil, fmt.Errorf("entry %d: target %q is not a slug", i, r.Target)
		case r.ID == r.Target:
			return nil, fmt.Errorf("entry %d: id and target are both %q", i, r.ID)
		case strings.TrimSpace(r.Reason) != r.Reason || r.Reason == "":
			return nil, fmt.Errorf("entry %d (%s): the reason is empty or padded", i, r.ID)
		case strings.ContainsAny(r.Reason, "\n\r"):
			return nil, fmt.Errorf("entry %d (%s): the reason is more than one line", i, r.ID)
		case strings.ContainsAny(r.Reason, "\u2013\u2014"):
			return nil, fmt.Errorf("entry %d (%s): the reason uses an en or em dash: hyphens only", i, r.ID)
		}
		if i > 0 && !linkKeyLess(rs[i-1].key(), r.key()) {
			return nil, fmt.Errorf("entry %d (%s %s -> %s): not after the entry before it: the list is sorted by "+
				"(op, id, target) with no duplicates", i, r.Op, r.ID, r.Target)
		}
	}
	return rs, nil
}

func linkKeyLess(a, b linkKey) bool {
	if a.op != b.op {
		return a.op < b.op
	}
	if a.id != b.id {
		return a.id < b.id
	}
	return a.target < b.target
}

// linkRejectionTally is what SUMMARY.md reports of the list: how many entries matched
// a proposal, and the entries that matched none, in the file's order.
type linkRejectionTally struct {
	Entries int
	Matched int
	Stale   []linkRejection
}

// applyLinkRejections turns every T-LINK proposal a rejection names advisory, with the
// rejection's reason in front of whatever the proposal already said, and tallies the
// entries. It runs before finalize, so the rendered action carries the new reason.
func applyLinkRejections(f *findings, rejections []linkRejection) linkRejectionTally {
	byKey := make(map[linkKey]int, len(rejections))
	for i, r := range rejections {
		byKey[r.key()] = i
	}
	matched := make([]bool, len(rejections))
	for i := range f.rows {
		p := &f.rows[i].Propose
		j, ok := byKey[linkKey{p.Op, p.Target, p.To}]
		if !ok {
			continue
		}
		matched[j] = true
		reason := "reviewed and rejected (" + linkRejectionsPath + "): " + rejections[j].Reason
		if p.Advisory && p.Reason != "" {
			reason += "; " + p.Reason
		}
		p.Advisory = true
		p.Reason = reason
	}
	t := linkRejectionTally{Entries: len(rejections)}
	for i, m := range matched {
		if m {
			t.Matched++
		} else {
			t.Stale = append(t.Stale, rejections[i])
		}
	}
	return t
}
