package repair

import (
	"slices"
	"strings"

	"github.com/kodestar/audiosilo-meta/internal/audit"
	"github.com/kodestar/audiosilo-meta/internal/rawentry"
	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// addlink.go applies add-link, the audit's T-LINK proposals: a translation_of link a
// record's own-language edition decoration states. It is the smallest write this pass
// makes - one member of one entry, no slug touched, nothing retired - and it is held
// to the same two rules as every other op here.
//
// The RECORD is re-read and its translation_of compared with the proposal's FROM (what
// the record stated when the audit proposed the link): a record that has moved since
// is refused as stale-value rather than unioned into, since the audit's decision was
// made against the old set - which is also what makes two proposals naming one
// translation fail safe: the second finds the first's link and refuses.
//
// The LINK is then held to pkg/check's rule of record over the STAGED plan
// (txn.refuseLinkFaults, the check every merge ends in), so a proposal that would chain
// onto a link an earlier proposal in the same run added, or put a translation in its
// original's language, is refused as translation-link-conflict with the tree untouched.
// Nothing about it needs --community: a link lives on a core record and never reads the
// works-community layer.

// addLink states Target's translation_of as including To.
func (rn *runner) addLink(t *txn, fd audit.Finding) error {
	p := fd.Propose
	if p.Field != fieldTranslationOf {
		return refusef(CatMalformed, "add-link names field %q; this pass adds translation_of links only", p.Field)
	}
	if p.Target == "" {
		return refusef(CatMalformed, "add-link names no record to link")
	}
	if p.To == "" {
		return refusef(CatNoValue, "the proposal names no original to link %s to", p.Target)
	}
	if p.To == p.Target {
		return refusef(CatMalformed, "add-link would link %s to itself", p.Target)
	}
	var f pack.Family
	switch model.RedirectKind(p.Kind) {
	case model.RedirectWorks:
		f = pack.FamilyWorks
	case model.RedirectSeries:
		f = pack.FamilySeries
	default:
		return refusef(CatMalformed, "add-link names the id namespace %q; want works or series", p.Kind)
	}
	e, err := rn.liveLinkRecord(t, f, p.Target)
	if err != nil {
		return err
	}
	if _, err := rn.liveLinkRecord(t, f, p.To); err != nil {
		return err
	}
	have := e.Strs(fieldTranslationOf)
	if got := strings.Join(have, ","); got != p.From {
		return refusef(CatStaleValue, "%s %s now states translation_of [%s], not the [%s] the proposal was written against",
			p.Kind, p.Target, strings.Join(have, ", "), strings.ReplaceAll(p.From, ",", ", "))
	}
	next := e.Clone()
	rawentry.SetListOrDrop(next, fieldTranslationOf, slices.Compact(slices.Sorted(slices.Values(append(slices.Clone(have), p.To)))))
	t.stageFor(f).put(p.Target, next)
	_, noun := linkFamily(f)
	t.note("linked %s %s as a translation of %s", noun, p.Target, p.To)
	return t.refuseLinkFaults(p.Target)
}

// liveLinkRecord reads a work or a series the plan still holds - liveWork and
// liveSeries by family.
func (rn *runner) liveLinkRecord(t *txn, f pack.Family, slug string) (entry, error) {
	if f == pack.FamilySeries {
		e, _, err := rn.liveSeries(t, slug)
		return e, err
	}
	return rn.liveWork(t, slug)
}

// pendingLinkedSeries is every series id a selected SERIES add-link names, at either
// end, so the plan indexes their members' languages before the first link is judged
// (see linkedMemberLanguages). nil when there is none.
func pendingLinkedSeries(selected []candidate) map[string]bool {
	var out map[string]bool
	for _, c := range selected {
		p := c.fd.Propose
		if p.Op != audit.OpAddLink || p.Kind != string(model.RedirectSeries) {
			continue
		}
		if out == nil {
			out = map[string]bool{}
		}
		out[p.Target], out[p.To] = true, true
	}
	return out
}
