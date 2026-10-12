package check

import (
	"slices"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// reviewed.go is how the advisory census honours a maintainer's REVIEW. The duplicate
// classes are deliberately broad - a maintainer's reading list (checkIdentityEqualWorks,
// differentVolumes) - so most of what they name is NOT one record, and a census that
// keeps naming a pair somebody has already read and ruled on is a census nobody reads.
// The rulings live in internal/audit/reviewed.json, which this package cannot import
// (audit imports check), so they arrive as plain data on LoadOptions and the caller that
// may import the audit - cmd/metacheck - supplies them.

// ReviewedDistinct is one reviewed decision that the records it names are NOT one
// record: internal/audit/reviewed.json's `reject` of a `merge-works` (Kind
// model.RedirectWorks) or `merge-series` (model.RedirectSeries) proposal, its target
// and others together. Every PAIR of its ids is read as distinct, so a reviewer who
// finds two of a group's members really are one record rejects only the others (and
// asserts that merge), rather than rejecting the whole group.
//
// The ids are the ones the review named. They are resolved through the tree's own
// slug tombstone table at load, so a decision taken before a repair wave retired one
// of its members still names that member's survivor.
type ReviewedDistinct struct {
	Kind model.RedirectKind
	IDs  []string
}

// LoadOptions are what a caller may add to a load beyond the tree itself. The zero
// value is a plain load, byte-identical to LoadProfile.
type LoadOptions struct {
	// Distinct are the reviewed "not one record" decisions the duplicate advisories
	// honour: a pair one of them covers is neither reported nor counted by
	// identity-equal-works, normalized-duplicate-works (works) or duplicate-series
	// (series), and Result.Suppressed counts every finding so withheld. Problems are
	// never touched - a review is about a census, not about what is valid.
	Distinct []ReviewedDistinct
}

// reviewedPairs is the load's resolved form of LoadOptions.Distinct: every pair of
// live ids a decision covers, per namespace, plus the count of advisory findings it
// has withheld. Empty for a load given no decisions.
type reviewedPairs struct {
	pairs      map[model.RedirectKind]map[[2]string]bool
	suppressed int
}

// newReviewedPairs resolves ds through the tombstone table reds.
func newReviewedPairs(ds []ReviewedDistinct, reds model.Redirects) *reviewedPairs {
	r := &reviewedPairs{pairs: map[model.RedirectKind]map[[2]string]bool{}}
	for _, d := range ds {
		ids := make([]string, 0, len(d.IDs))
		for _, id := range d.IDs {
			if to, ok := reds.Survivor(d.Kind, id); ok {
				id = to
			}
			ids = append(ids, id)
		}
		slices.Sort(ids)
		ids = slices.Compact(ids) // two members merged since are one record, not a pair
		set := r.pairs[d.Kind]
		if set == nil {
			set = map[[2]string]bool{}
			r.pairs[d.Kind] = set
		}
		for i := range ids {
			for j := i + 1; j < len(ids); j++ {
				set[[2]string{ids[i], ids[j]}] = true
			}
		}
	}
	return r
}

// withheld reports whether a reviewed decision recorded a and b, of namespace kind,
// as distinct, counting it when it did. A rule asks it LAST, after every rule of its
// own has passed the pair, so the count is exactly the findings a review took out of
// the census rather than every pair it happens to name.
func (r *reviewedPairs) withheld(kind model.RedirectKind, a, b string) bool {
	if b < a {
		a, b = b, a
	}
	if !r.pairs[kind][[2]string{a, b}] {
		return false
	}
	r.suppressed++
	return true
}
