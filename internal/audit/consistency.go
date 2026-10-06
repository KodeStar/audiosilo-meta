package audit

import (
	"fmt"
	"slices"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// proposalConflictState indexes the mechanical set. Checking a promotion touches
// only the records it names; failed promotions leave the indexes unchanged.
type proposalConflictState struct {
	conflicts []string

	mergeTarget, isTarget     map[string]string   // op/id -> survivor or finding
	slot                      map[string]string   // series@position -> finding
	linkedTo, original        map[string]string   // op/id -> original or finding
	leaves, joins, restated   map[string]string   // series@work -> finding
	added                     map[string]string   // series@work an add-series-member adds -> finding
	mixWorks, mixSeries       map[string]string   // work or series -> finding
	mergedWorks, mergedSeries map[string]string   // work or series -> finding
	languages                 map[string]string   // work -> finding
	splitKeeper, splitBy      map[string]string   // series -> the language a split keeps it for, and that split
	mixLeftLang               map[string]string   // series/language -> an L-MIX drop or move taking a member of it out
	homes                     map[string][]string // series a drop relies on as the work's home -> every such drop
	retitled                  map[string]string   // work an ASSERTED retitle-work rewrites -> finding
}

// proposalConflicts is the shared set invariant used by reviewed acceptances and
// assertProposalsConsistent. Build once, then extend in decision order. Sorted
// diagnostics make refusals independent of map order.
func proposalConflicts(rep *Report) *proposalConflictState {
	s := &proposalConflictState{
		mergeTarget: map[string]string{}, isTarget: map[string]string{},
		slot: map[string]string{}, linkedTo: map[string]string{}, original: map[string]string{},
		leaves: map[string]string{}, joins: map[string]string{}, restated: map[string]string{}, added: map[string]string{},
		mixWorks: map[string]string{}, mixSeries: map[string]string{},
		mergedWorks: map[string]string{}, mergedSeries: map[string]string{}, languages: map[string]string{},
		splitKeeper: map[string]string{}, splitBy: map[string]string{}, mixLeftLang: map[string]string{},
		homes: map[string][]string{}, retitled: map[string]string{},
	}
	for _, class := range classOrder {
		for _, r := range rep.class(class).rows {
			if !r.Propose.Advisory {
				s.conflicts = append(s.conflicts, s.add(r, true)...)
			}
		}
	}
	slices.Sort(s.conflicts)
	return s
}

// promote adds a candidate only when the resulting set is consistent; an already
// inconsistent mechanical set promotes nothing (fail safe). Ops add does not index
// (fill-field, a detector's retitle-work) are re-checked by the repair itself
// (stale-value).
func (s *proposalConflictState) promote(r Finding) []string {
	if len(s.conflicts) > 0 {
		return s.conflicts
	}
	return s.add(r, false)
}

// add checks both sides of every constraint as each claim arrives, so report
// order and acceptance order enforce the same invariant. Initial construction
// keeps conflicting claims to diagnose the whole set; a promotion rolls them back.
func (s *proposalConflictState) add(r Finding, keepConflicts bool) []string {
	var conflicts []string
	report := func(format string, args ...any) { conflicts = append(conflicts, fmt.Sprintf(format, args...)) }
	var undo []func()
	put := func(index map[string]string, key, value string) {
		if !keepConflicts {
			prev, ok := index[key]
			undo = append(undo, func() {
				if ok {
					index[key] = prev
				} else {
					delete(index, key)
				}
			})
		}
		index[key] = value
	}
	// A drop READS its homes (Others): a home a series merge retires is no home once
	// that merge lands, so the drop would apply or go stale by run order (L-MIX's
	// emitDrop veto, held here for accepted drops too).
	homeMerged := func(drop, home, merger string) {
		report("%s relies on %s as its work's home, which %s merges", drop, home, merger)
	}
	claimSlot := func(key string) {
		if prev, dup := s.slot[key]; dup {
			report("%s and %s both claim series slot %s", prev, r.Key, key)
		}
		put(s.slot, key, r.Key)
	}
	touchSeries := func(series string) {
		if m, both := s.mergedSeries[series]; both {
			report("%s changes series %s, which %s merges", r.Key, series, m)
		}
		put(s.mixSeries, series, r.Key)
	}
	leave := func(series, work string) {
		membership := series + "@" + work
		if prev, dup := s.leaves[membership]; dup {
			report("%s and %s both move %s out of %s", prev, r.Key, work, series)
		}
		if other, both := s.restated[membership]; both {
			report("%s moves membership %s, which %s restates", r.Key, membership, other)
		}
		// A membership one proposal takes out and another puts in (an addition, or a move
		// into the series) would apply or go stale by run order.
		if other, both := s.added[membership]; both {
			report("%s moves %s out of %s, which %s adds", r.Key, work, series, other)
		}
		if other, both := s.joins[membership]; both {
			report("%s moves %s out of %s, which %s moves into it", r.Key, work, series, other)
		}
		if m, both := s.mergedWorks[work]; both {
			report("%s moves %s, which %s merges", r.Key, work, m)
		}
		if by, both := s.languages[work]; both {
			report("%s sets the language of %s, which %s moves", by, work, r.Key)
		}
		put(s.leaves, membership, r.Key)
		put(s.mixWorks, work, r.Key)
		touchSeries(series)
	}
	// mixOrientation: an L-MIX drop or move takes a member out of a series because its
	// language is NOT the one the series is kept for, so it contradicts a split keeping
	// the series for that very language (a tie's drop beside its other orientation would
	// empty the series between them).
	mixOrientation := func(series string) {
		if r.Class != ClassLangMix || len(r.Works) == 0 {
			return
		}
		lang := model.PrimarySubtag(r.Works[0].Language)
		if keeper, split := s.splitKeeper[series]; split && keeper == lang {
			report("%s takes a %s member out of %s, which %s keeps for %s", r.Key, lang, series, s.splitBy[series], keeper)
		}
		put(s.mixLeftLang, series+"/"+lang, r.Key)
	}

	p := r.Propose
	switch p.Op {
	case OpMergeWorks, OpMergeSeries:
		merged, moved := s.mergedWorks, s.mixWorks
		if p.Op == OpMergeSeries {
			merged, moved = s.mergedSeries, s.mixSeries
		}
		for _, id := range Cluster(p.Target, p.Others) {
			if by, both := moved[id]; both {
				if p.Op == OpMergeSeries {
					report("%s changes series %s, which %s merges", by, id, r.Key)
				} else {
					report("%s moves %s, which %s merges", by, id, r.Key)
				}
			}
			if p.Op == OpMergeWorks {
				if by, both := s.languages[id]; both {
					report("%s sets the language of %s, which %s merges", by, id, r.Key)
				}
			} else {
				for _, by := range s.homes[id] {
					homeMerged(by, id, r.Key)
				}
			}
			// A loser belongs in ONE proposal: folded twice, the later merge goes stale
			// against the earlier. A survivor may absorb several. Folding it onto another
			// target, or a target elsewhere, is reported below; here is the same target.
			// s.mergeTarget holds only OTHER proposals' losers here: this one records its
			// own in the Others loop below, after this cluster loop.
			if by, both := merged[id]; both && id != p.Target && s.mergeTarget[p.Op+"/"+id] == p.Target {
				report("%s merges %s, which %s already merges", r.Key, id, by)
			}
			put(merged, id, r.Key)
		}
		if p.Target != "" {
			key := p.Op + "/" + p.Target
			if by, both := s.mergeTarget[key]; both {
				report("%s is a merge target in %s and a loser in the proposal that folds it onto %s", p.Target, r.Key, by)
			}
			put(s.isTarget, key, r.Key)
		}
		for _, o := range p.Others {
			if by, both := s.retitled[o]; both && p.Op == OpMergeWorks {
				report("%s retitles %s, which %s folds onto %s", by, o, r.Key, p.Target)
			}
			key := p.Op + "/" + o
			if prev, dup := s.mergeTarget[key]; dup && prev != p.Target {
				report("%s: %s is told to fold onto both %s and %s", r.Key, o, prev, p.Target)
			}
			if other, both := s.isTarget[key]; both {
				report("%s is a merge target in %s and a loser in the proposal that folds it onto %s", o, other, p.Target)
			}
			put(s.mergeTarget, key, p.Target)
		}
	case OpAddSeriesMember:
		if p.Series != "" && p.To != "" {
			claimSlot(p.Series + "@" + p.To)
		}
		if by, dup := s.added[p.Series+"@"+p.Target]; dup {
			report("%s and %s both add %s to %s", by, r.Key, p.Target, p.Series)
		}
		if by, both := s.leaves[p.Series+"@"+p.Target]; both {
			report("%s moves %s out of %s, which %s adds", by, p.Target, p.Series, r.Key)
		}
		put(s.added, p.Series+"@"+p.Target, r.Key)
	case OpAddWorkLink, OpAddSeriesLink:
		tr := p.Op + "/" + p.Target
		if prev, dup := s.linkedTo[tr]; dup && prev != p.To {
			report("%s is linked to both %s and %s", r.Key, prev, p.To)
		}
		if by, both := s.original[tr]; both {
			report("%s is a translation in one link proposal and the original %s names in another", tr, by)
		}
		put(s.linkedTo, tr, p.To)
		orig := p.Op + "/" + p.To
		if _, both := s.linkedTo[orig]; both {
			report("%s is a translation in one link proposal and the original %s names in another", orig, r.Key)
		}
		put(s.original, orig, r.Key)
	case OpRestatePosition:
		for _, w := range Cluster(p.Target, p.Others) {
			if w != "" {
				membership := p.Series + "@" + w
				if by, both := s.leaves[membership]; both {
					report("%s moves membership %s, which %s restates", by, membership, r.Key)
				}
				put(s.restated, membership, r.Key)
			}
		}
	case OpRetitle:
		// A detector's retitle of a merge loser is harmless (either order leaves one
		// catalogue, the loser retired whichever runs first), and the real tree holds such
		// pairs. An ASSERTED retitle is a reviewer's title for THAT record, which a fold of
		// it would discard: assert it on the survivor. A survivor's retitle is no conflict.
		if r.Subclass != SubclassAsserted {
			break
		}
		if prev, dup := s.retitled[p.Target]; dup {
			report("%s and %s both retitle %s", prev, r.Key, p.Target)
		}
		if survivor, loser := s.mergeTarget[OpMergeWorks+"/"+p.Target]; loser {
			report("%s retitles %s, which %s folds onto %s", r.Key, p.Target, s.mergedWorks[p.Target], survivor)
		}
		put(s.retitled, p.Target, r.Key)
	case OpSetWorkLanguage:
		if prev, dup := s.languages[p.Target]; dup {
			report("%s and %s both set the language of %s", prev, r.Key, p.Target)
		}
		if other, both := s.mergedWorks[p.Target]; both {
			report("%s sets the language of %s, which %s merges", r.Key, p.Target, other)
		}
		if other, both := s.mixWorks[p.Target]; both {
			report("%s sets the language of %s, which %s moves", r.Key, p.Target, other)
		}
		put(s.languages, p.Target, r.Key)
	case OpDropMembership:
		leave(p.Series, p.Target)
		mixOrientation(p.Series)
		for _, h := range p.Others {
			if m, both := s.mergedSeries[h]; both {
				homeMerged(r.Key, h, m)
			}
			prev, had := s.homes[h]
			if !keepConflicts {
				undo = append(undo, func() {
					if had {
						s.homes[h] = prev
					} else {
						delete(s.homes, h)
					}
				})
			}
			s.homes[h] = append(slices.Clone(prev), r.Key)
		}
	case OpMoveMembership:
		leave(p.Series, p.Target)
		mixOrientation(p.Series)
		dest := p.Others[0]
		if prev, dup := s.joins[dest+"@"+p.Target]; dup {
			report("%s and %s both move %s into %s", prev, r.Key, p.Target, dest)
		}
		if by, both := s.leaves[dest+"@"+p.Target]; both {
			report("%s moves %s out of %s, which %s moves into it", by, p.Target, dest, r.Key)
		}
		put(s.joins, dest+"@"+p.Target, r.Key)
		touchSeries(dest)
		claimSlot(dest + "@" + p.To)
	case OpSplitSeries:
		// Every split of one series must keep it for the SAME language: a contested
		// series is proposed in each orientation, and only one of them can be the series.
		if prev, dup := s.splitKeeper[p.Target]; dup && prev != p.From {
			report("%s and %s split %s in two orientations (%s keeps it, and %s keeps it)", s.splitBy[p.Target], r.Key,
				p.Target, orDash(prev), orDash(p.From))
		}
		if by, out := s.mixLeftLang[p.Target+"/"+p.From]; out {
			report("%s takes a %s member out of %s, which %s keeps for %s", by, p.From, p.Target, r.Key, p.From)
		}
		put(s.splitKeeper, p.Target, p.From)
		put(s.splitBy, p.Target, r.Key)
		for _, w := range p.Others {
			leave(p.Target, w)
		}
	}
	if len(conflicts) > 0 && !keepConflicts {
		for _, restore := range slices.Backward(undo) {
			restore()
		}
	}
	slices.Sort(conflicts)
	return slices.Compact(conflicts)
}
