package audit

import (
	"bytes"
	"cmp"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kodestar/audiosilo-meta/pkg/canonical"
	"github.com/kodestar/audiosilo-meta/pkg/model"
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
// THE KEY is one specific link, spelled with the proposal's OWN field names - (op,
// target, to), copied straight off a T-LINK.ndjson record's propose object: op is the
// value linkOps names and metarepair's --op takes, target the TRANSLATION (the record
// the op writes) and to the rejected ORIGINAL. A proposal for the same record naming a
// DIFFERENT original is not suppressed: the rejection is of one link, and a changed
// candidate deserves a fresh judgement.
//
// A RETIRED slug on either side is read through the tombstone table (model.Redirects,
// in the family the op names): a merge wave retiring one side must not bring the link
// back as mechanical under the survivor's slug. The entry keeps naming the slug it was
// reviewed under; the match is made against the survivor.
//
// THE EFFECT is advisory, never absence: a matching proposal stays in T-LINK.ndjson,
// turned advisory, its reason saying it was reviewed and rejected and why. An entry
// matching no proposal, even after resolving, is STALE and SUMMARY.md lists it by name
// for a cleanup - it fails nothing, since an entry goes stale legitimately when the tree
// moves (the link applied by hand, the evidence changed).

//go:embed tlink_rejected.json
var linkRejectionsFile []byte

// linkRejectionsPath is the file's name as a report quotes it.
const linkRejectionsPath = "internal/audit/tlink_rejected.json"

// linkRejection is one reviewed-and-rejected link, keyed as the proposal it rejects.
type linkRejection struct {
	Op     string `json:"op"`
	Target string `json:"target"`
	To     string `json:"to"`
	Reason string `json:"reason"`
}

// linkKey is what a rejection and a proposal are matched on.
type linkKey struct{ op, target, to string }

// linkRejections is the embedded list, parsed once. A file that does not parse is a
// build defect, so package initialization panics naming the file and the error rather
// than auditing as though nothing had been reviewed - every test in the package fails
// with it.
var linkRejections = mustParseLinkRejections(linkRejectionsFile)

func mustParseLinkRejections(raw []byte) []linkRejection {
	rs, err := parseLinkRejections(raw)
	if err != nil {
		panic(fmt.Sprintf("audit: %s: %v", linkRejectionsPath, err))
	}
	return rs
}

// linkOpKind is the family whose translation_of op writes - linkOps read backwards -
// and whether op is a link op at all.
func linkOpKind(op string) (model.RedirectKind, bool) {
	for kind, o := range linkOps {
		if o == op {
			return kind, true
		}
	}
	return "", false
}

// parseLinkRejections decodes and validates the list: canonical JSON (which also
// refuses a repeated key, since canonical form keeps only one), no unknown field, a
// link op, slug-shaped target and to that differ, a one-line trimmed reason in
// hyphens, and entries in strictly increasing (op, target, to) order - which is also
// what makes them unique.
func parseLinkRejections(raw []byte) ([]linkRejection, error) {
	if ok, err := canonical.IsCanonical(raw); err != nil {
		return nil, err
	} else if !ok {
		return nil, fmt.Errorf("not in canonical form (sorted keys, 2-space indent, trailing newline, no " +
			"repeated key): re-render it canonically")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var rs []linkRejection
	if err := dec.Decode(&rs); err != nil {
		return nil, err
	}
	for i, r := range rs {
		if _, ok := linkOpKind(r.Op); !ok {
			return nil, fmt.Errorf("entry %d: op %q is not a link op", i, r.Op)
		}
		switch {
		case !model.ValidSlug(r.Target):
			return nil, fmt.Errorf("entry %d: target %q is not a slug", i, r.Target)
		case !model.ValidSlug(r.To):
			return nil, fmt.Errorf("entry %d: to %q is not a slug", i, r.To)
		case r.Target == r.To:
			return nil, fmt.Errorf("entry %d: target and to are both %q", i, r.Target)
		case r.Reason == "" || strings.TrimSpace(r.Reason) != r.Reason ||
			strings.ContainsAny(r.Reason, "\n\r\u2013\u2014"):
			return nil, fmt.Errorf("entry %d (%s): the reason must be a one-line, trimmed reason in hyphens", i, r.Target)
		}
		if i > 0 && linkRejectionCmp(rs[i-1], r) >= 0 {
			return nil, fmt.Errorf("entry %d (%s %s -> %s): not after the entry before it: the list is sorted by "+
				"(op, target, to) with no duplicates", i, r.Op, r.Target, r.To)
		}
	}
	return rs, nil
}

func linkRejectionCmp(a, b linkRejection) int {
	return cmp.Or(cmp.Compare(a.Op, b.Op), cmp.Compare(a.Target, b.Target), cmp.Compare(a.To, b.To))
}

// linkRejectionTally is what SUMMARY.md reports of the list: how many entries matched
// a proposal, and the entries that matched none, in the file's order.
type linkRejectionTally struct {
	Matched int
	Stale   []linkRejection
}

// Entries is how many rejections the list holds.
func (t linkRejectionTally) Entries() int { return t.Matched + len(t.Stale) }

// applyLinkRejections turns every T-LINK proposal a rejection names advisory, with the
// rejection's reason in front of whatever the proposal already said, and tallies the
// entries. Each entry's sides are resolved through reds first, so a slug a merge has
// since retired still names the link under its survivor. It runs before finalize, so
// the rendered action carries the new reason.
func applyLinkRejections(f *findings, rejections []linkRejection, reds model.Redirects) linkRejectionTally {
	live := func(kind model.RedirectKind, slug string) string {
		if to, ok := reds.Survivor(kind, slug); ok {
			return to
		}
		return slug
	}
	byKey := make(map[linkKey][]int, len(rejections))
	for i, r := range rejections {
		kind, _ := linkOpKind(r.Op)
		k := linkKey{r.Op, live(kind, r.Target), live(kind, r.To)}
		byKey[k] = append(byKey[k], i)
	}
	matched := make([]bool, len(rejections))
	for i := range f.rows {
		p := &f.rows[i].Propose
		js, ok := byKey[linkKey{p.Op, p.Target, p.To}]
		if !ok {
			continue
		}
		for _, j := range js {
			matched[j] = true
		}
		reason := "reviewed and rejected (" + linkRejectionsPath + "): " + rejections[js[0]].Reason
		if p.Advisory {
			reason += "; " + p.Reason
		}
		p.Advisory = true
		p.Reason = reason
	}
	var t linkRejectionTally
	for i, m := range matched {
		if m {
			t.Matched++
		} else {
			t.Stale = append(t.Stale, rejections[i])
		}
	}
	return t
}
