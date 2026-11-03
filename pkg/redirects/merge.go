package redirects

import (
	"fmt"
	"slices"
	"strings"

	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// MergeConflict is Merge's refusal: the two sides disagree about a tombstone, or
// together they describe a table no Add could have built. Clashes names each one,
// in namespace then slug order, so the refusal is the same on every run.
type MergeConflict struct {
	Clashes []string
}

func (c *MergeConflict) Error() string {
	return "the two sides disagree about the slug tombstones: " + strings.Join(c.Clashes, "; ")
}

// Merge is the THREE-WAY merge of two edits of the tombstone table against their
// common base - what a git merge driver for data/redirects.json has to decide
// (cmd/metaredirectmerge is that driver). base may be nil or empty: a file both
// sides created has no base.
//
// Each namespace is merged key by key, with the base deciding what a difference
// means, exactly as the pack driver's entry rules do (scripts/pack-union-merge.sh):
//
//	on both sides, same target            keep it
//	on both sides, one side equal to base only the other side changed it: take that
//	on both sides, otherwise              a conflict (both changed it, or both
//	                                      ADDED it, to different targets)
//	on one side, absent from base         that side added it: keep it
//	on one side, in base unchanged        the other side deleted it: delete it
//	on one side, in base changed          delete versus modify: a conflict
//	on neither side                       deleted (or never there)
//
// Two sides adding different slugs - the common case, two pull requests each
// recording one alias, which git's line merge sees as overlapping hunks because
// the sorted lines are adjacent - is the first-but-one row on each side.
//
// THEN THE CHAINS ARE COLLAPSED, through Add, because the union of two valid
// tables need not be one. One side retiring a -> b while the other retires b -> c
// is two individually correct edits whose union is the chain a -> b -> c, which
// pkg/check refuses. Collapsing it (a -> c, b -> c) is what Add would have done had
// the two edits landed one after the other in either order, so the merged table is
// the table that sequence would have produced, and the collapse has one
// implementation rather than a second one here. Refusing instead would stall
// exactly the case a sequence of repair waves produces. What Add refuses stays
// refused - a pair of edits whose union is a cycle (a -> b on one side, b -> a on
// the other) is two people disagreeing about which record survived, and a slug
// the table may not hold is reported rather than carried - so either is returned
// as a MergeConflict too.
//
// Whether every target still names a live record is a question about the
// catalogue (the other side may have deleted the record a new tombstone points
// at), which this package does not read: pkg/check answers it over the merged
// tree, which is why the intake sweep runs metacheck after any driver merge.
func Merge(base, ours, theirs model.Redirects) (model.Redirects, error) {
	b, err := normalize(base)
	if err != nil {
		return nil, fmt.Errorf("base: %w", err)
	}
	o, err := normalize(ours)
	if err != nil {
		return nil, fmt.Errorf("ours: %w", err)
	}
	t, err := normalize(theirs)
	if err != nil {
		return nil, fmt.Errorf("theirs: %w", err)
	}

	kept := model.NewRedirects()
	var clashes []string
	for _, kind := range model.RedirectKinds() {
		for _, slug := range unionKeys(b[kind], o[kind], t[kind]) {
			bv, inB := b[kind][slug]
			ov, inO := o[kind][slug]
			tv, inT := t[kind][slug]
			switch {
			case inO && inT:
				switch {
				case ov == tv, inB && bv == tv:
					kept[kind][slug] = ov
				case inB && bv == ov:
					kept[kind][slug] = tv
				case inB:
					clashes = append(clashes, fmt.Sprintf("%s %s: both sides retargeted it (base %s, ours %s, theirs %s)",
						kind, slug, bv, ov, tv))
				default:
					clashes = append(clashes, fmt.Sprintf("%s %s: both sides added it, to different targets (ours %s, theirs %s)",
						kind, slug, ov, tv))
				}
			case inO:
				if !inB {
					kept[kind][slug] = ov
				} else if bv != ov {
					clashes = append(clashes, fmt.Sprintf("%s %s: theirs deleted it, ours retargeted it (base %s, ours %s)",
						kind, slug, bv, ov))
				}
			case inT:
				if !inB {
					kept[kind][slug] = tv
				} else if bv != tv {
					clashes = append(clashes, fmt.Sprintf("%s %s: ours deleted it, theirs retargeted it (base %s, theirs %s)",
						kind, slug, bv, tv))
				}
			}
		}
	}
	if len(clashes) > 0 {
		return nil, &MergeConflict{Clashes: clashes}
	}

	// Rebuild through Add in slug order. The order cannot change the result of a
	// table that collapses at all (Add repoints in both directions), but a fixed
	// one keeps the reported refusal stable.
	out := model.NewRedirects()
	for _, kind := range model.RedirectKinds() {
		for _, slug := range unionKeys(kept[kind]) {
			if err := Add(out, kind, slug, kept[kind][slug]); err != nil {
				return nil, &MergeConflict{Clashes: []string{"the merged table cannot be collapsed: " + err.Error()}}
			}
		}
	}
	return out, nil
}

// unionKeys returns every key of the maps, sorted and deduplicated.
func unionKeys(maps ...map[string]string) []string {
	var keys []string
	for _, m := range maps {
		for k := range m {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	return slices.Compact(keys)
}
