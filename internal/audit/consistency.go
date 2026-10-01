package audit

import (
	"fmt"
	"slices"
	"strings"
)

// proposalConflicts is the shared set invariant used by reviewed acceptances and
// assertProposalsConsistent. Sorted diagnostics make refusals independent of map order.
func proposalConflicts(rep *Report) []string {
	var conflicts []string
	report := func(format string, args ...any) { conflicts = append(conflicts, fmt.Sprintf(format, args...)) }
	mergeTarget := map[string]string{} // op/id -> the target it was told to fold onto
	isTarget := map[string]string{}    // op/id -> the record naming it as a target
	slot := map[string]string{}        // "<series>@<pos>" -> the record claiming it
	linkedTo := map[string]string{}    // "<op>/<translation>" -> the original it is linked to
	original := map[string]string{}    // "<op>/<original>" -> the record naming it
	// The L-MIX membership ops: a membership leaves its series once, a work joins a series
	// once, and neither the works moved nor the series touched are in a merge - a merge
	// retires what the move was checked against, so the two would not commute.
	leaves := map[string]string{}       // "<series>@<work>" -> the record moving it out
	joins := map[string]string{}        // "<series>@<work>" -> the record moving it in
	mixWorks := map[string]string{}     // work -> an L-MIX record moving it
	mixSeries := map[string]string{}    // series -> an L-MIX record touching it
	mergedWorks := map[string]string{}  // work -> the merge-works record naming it
	mergedSeries := map[string]string{} // series -> the merge-series record naming it
	restated := map[string]string{}     // membership -> position rewrite
	languages := map[string]string{}    // work -> language rewrite
	leave := func(series, work, key string) {
		if prev, dup := leaves[series+"@"+work]; dup {
			report("%s and %s both move %s out of %s", prev, key, work, series)
		}
		leaves[series+"@"+work] = key
		mixWorks[work] = key
		mixSeries[series] = key
	}

	for _, class := range classOrder {
		for _, r := range rep.class(class).rows {
			p := r.Propose
			if p.Advisory {
				continue // an advisory proposal is a reading list, not an instruction
			}
			switch p.Op {
			case OpMergeWorks, OpMergeSeries:
				merged := mergedWorks
				if p.Op == OpMergeSeries {
					merged = mergedSeries
				}
				for _, id := range Cluster(p.Target, p.Others) {
					merged[id] = r.Key
				}
				if p.Target != "" {
					isTarget[p.Op+"/"+p.Target] = r.Key
				}
				for _, o := range p.Others {
					if prev, dup := mergeTarget[p.Op+"/"+o]; dup && prev != p.Target {
						report("%s: %s is told to fold onto both %s and %s", r.Key, o, prev, p.Target)
					}
					mergeTarget[p.Op+"/"+o] = p.Target
				}
			case OpAddSeriesMember:
				if p.Series == "" || p.To == "" {
					continue
				}
				key := p.Series + "@" + p.To
				if prev, dup := slot[key]; dup {
					report("%s and %s both claim series slot %s", prev, r.Key, key)
				}
				slot[key] = r.Key
			case OpAddWorkLink, OpAddSeriesLink:
				// A translation gains ONE original per run, and no record is both a
				// translation and an original across the set: applied in either order,
				// the second would chain onto the first.
				tr := p.Op + "/" + p.Target
				if prev, dup := linkedTo[tr]; dup && prev != p.To {
					report("%s is linked to both %s and %s", r.Key, prev, p.To)
				}
				linkedTo[tr] = p.To
				original[p.Op+"/"+p.To] = r.Key
			case OpRestatePosition:
				for _, w := range Cluster(p.Target, p.Others) {
					if w != "" {
						restated[p.Series+"@"+w] = r.Key
					}
				}
			case OpSetWorkLanguage:
				if prev, dup := languages[p.Target]; dup {
					report("%s and %s both set the language of %s", prev, r.Key, p.Target)
				}
				languages[p.Target] = r.Key
			case OpDropMembership:
				leave(p.Series, p.Target, r.Key)
			case OpMoveMembership:
				leave(p.Series, p.Target, r.Key)
				dest := p.Others[0]
				if prev, dup := joins[dest+"@"+p.Target]; dup {
					report("%s and %s both move %s into %s", prev, r.Key, p.Target, dest)
				}
				joins[dest+"@"+p.Target] = r.Key
				mixSeries[dest] = r.Key
				key := dest + "@" + p.To
				if prev, dup := slot[key]; dup {
					report("%s and %s both claim series slot %s", prev, r.Key, key)
				}
				slot[key] = r.Key
			case OpSplitSeries:
				for _, w := range p.Others {
					leave(p.Target, w, r.Key)
				}
			}
		}
	}
	for membership, by := range leaves {
		if other, both := restated[membership]; both {
			report("%s moves membership %s, which %s restates", by, membership, other)
		}
	}
	for work, by := range languages {
		if other, both := mergedWorks[work]; both {
			report("%s sets the language of %s, which %s merges", by, work, other)
		}
		if other, both := mixWorks[work]; both {
			report("%s sets the language of %s, which %s moves", by, work, other)
		}
	}
	for w, by := range mixWorks {
		if m, both := mergedWorks[w]; both {
			report("%s moves %s, which %s merges", by, w, m)
		}
	}
	for s, by := range mixSeries {
		if m, both := mergedSeries[s]; both {
			report("%s changes series %s, which %s merges", by, s, m)
		}
	}
	for tr := range linkedTo {
		if by, both := original[tr]; both {
			report("%s is a translation in one link proposal and the original %s names in another", tr, by)
		}
	}
	for w, by := range mergeTarget {
		if other, both := isTarget[w]; both {
			report("%s is a merge target in %s and a loser in the proposal that folds it onto %s", strings.SplitN(w, "/", 2)[1], other, by)
		}
	}
	slices.Sort(conflicts)
	return conflicts
}
