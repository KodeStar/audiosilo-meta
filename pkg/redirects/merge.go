package redirects

import (
	"fmt"
	"maps"
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
// the sorted lines are adjacent - is the "on one side, absent from base" row, once
// for each side's slug.
//
// THE CHAINS ARE COLLAPSED, through Add, because the union of two valid
// tables need not be one. One side retiring a -> b while the other retires b -> c
// is two individually correct edits whose union is the chain a -> b -> c, which
// pkg/check refuses. Collapsing it (a -> c, b -> c) is what Add would have done had
// the two edits landed one after the other in either order, so the merged table is
// the table that sequence would have produced, and the collapse has one
// implementation rather than a second one here. Refusing instead would stall
// exactly the case a sequence of repair waves produces. What Add refuses stays
// refused - a pair of edits whose union is a cycle (a -> b on one side, b -> a on
// the other) is two people disagreeing about which record survived, and an
// incoming slug the table may not hold is reported rather than carried - so
// either is returned as a MergeConflict too. Only THEIR changes go through Add
// (see below); ours is taken as it stands, which metacheck has already judged.
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

	// The result starts as OURS and takes only what theirs changed, so the cost is
	// one copy plus one Add per incoming change rather than an Add (each a walk of
	// the table) per tombstone. Starting from ours is the same table as building
	// every kept entry through Add whenever ours is a table Add could have built
	// (no chain), which pkg/check holds every committed version to.
	out := model.NewRedirects()
	for kind, table := range o {
		maps.Copy(out[kind], table)
	}
	var clashes []string
	type change struct {
		kind     model.RedirectKind
		from, to string
	}
	var incoming []change
	for _, kind := range model.RedirectKinds() {
		for _, slug := range unionKeys(b[kind], o[kind], t[kind]) {
			bv, ov, tv := lookup(b[kind], slug), lookup(o[kind], slug), lookup(t[kind], slug)
			switch {
			case ov == tv, tv == bv:
				// Ours already holds the result.
			case ov == bv:
				// Only theirs changed it: drop ours' copy, and record theirs' target
				// (if it kept one) once every removal is done.
				delete(out[kind], slug)
				if tv.ok {
					incoming = append(incoming, change{kind, slug, tv.v})
				}
			default:
				clashes = append(clashes, clash(kind, slug, bv, ov, tv))
			}
		}
	}
	if len(clashes) > 0 {
		return nil, &MergeConflict{Clashes: clashes}
	}
	// The additions wait for the removals: an Add collapses through whatever the
	// table holds, so one made before theirs' deletion of the tombstone it walks
	// through would collapse onto a target theirs no longer states.
	for _, c := range incoming {
		if err := Add(out, c.kind, c.from, c.to); err != nil {
			// A cycle across the two sides, or a slug the table may not hold:
			// Add's own error says which, so it is not re-worded as either.
			return nil, &MergeConflict{Clashes: []string{fmt.Sprintf(
				"theirs' %s %s -> %s cannot be added to ours: %v", c.kind, c.from, c.to, err)}}
		}
	}
	return out, nil
}

// target is one side's view of one slug: the target it states, or absent. Being
// comparable is the point - absence is a value, so the base rules are three
// equalities.
type target struct {
	v  string
	ok bool
}

func lookup(table map[string]string, slug string) target {
	v, ok := table[slug]
	return target{v, ok}
}

// clash words a refusal by which of the three versions hold the slug. The only
// shapes that reach it are both sides holding it (added or retargeted) and one
// side deleting what the other retargeted: every other one has a side equal to
// the base.
func clash(kind model.RedirectKind, slug string, b, o, t target) string {
	switch {
	case !o.ok:
		return fmt.Sprintf("%s %s: ours deleted it, theirs retargeted it (base %s, theirs %s)", kind, slug, b.v, t.v)
	case !t.ok:
		return fmt.Sprintf("%s %s: theirs deleted it, ours retargeted it (base %s, ours %s)", kind, slug, b.v, o.v)
	case b.ok:
		return fmt.Sprintf("%s %s: both sides retargeted it (base %s, ours %s, theirs %s)", kind, slug, b.v, o.v, t.v)
	default:
		return fmt.Sprintf("%s %s: both sides added it, to different targets (ours %s, theirs %s)", kind, slug, o.v, t.v)
	}
}

// unionKeys returns every key of the tables, sorted and deduplicated.
func unionKeys(tables ...map[string]string) []string {
	var keys []string
	for _, m := range tables {
		for k := range m {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	return slices.Compact(keys)
}
