package migrate

import (
	"encoding/json"
	"fmt"

	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// Pack planning: which entries share a file, and what that file is called.
//
// The rule is PACK-SPEC.md's migration rule - greedy fill in slug order to
// ~50% of the pack target, so organic inserts have headroom before the first
// split - applied to entry counts as well as bytes, for the same reason. Bounds
// follow the layout's naming: a family's very first pack and directory carry the
// reserved minimum bound "0", every other pack is named by its own first slug
// and every other directory by its first pack's bound.
//
// Planning is a pure function of the sorted entries and the family's caps, so
// the same input tree always produces the same pack tree.

// entry is one composed pack entry, with the number of bytes it contributes to
// a rendered pack (filled in by measure).
type entry struct {
	slug string
	raw  json.RawMessage
	size int
}

// newEntry composes one entry. Its size is measured later, for the whole family
// at once.
func newEntry(slug string, raw json.RawMessage) (entry, error) {
	if len(raw) == 0 {
		return entry{}, fmt.Errorf("entry %q is empty", slug)
	}
	return entry{slug: slug, raw: raw}, nil
}

// measure fills in each entry's byte contribution and returns the pack wrapper's
// own overhead, by rendering the whole family through pkg/pack ONCE.
//
// The sizes are the real renderer's rather than arithmetic that reproduces its
// layout. A second spelling of "what a pack file looks like" would be a second
// thing to keep in step with pkg/canonical, and drift there fails silently:
// packs filled to the wrong size are still perfectly valid packs. File.Sizes
// reports exactly what each entry costs in the file it rendered, memoized, so
// one pass over the family answers every question the planner has.
//
// The one inexactness is the separating comma: the last entry of each PLANNED
// pack will not carry one, while all but the last of the family did. That is one
// byte per pack, against a fill target that is advisory by design.
func measure(entries []entry) (overhead int, err error) {
	if len(entries) == 0 {
		return 0, nil
	}
	f := pack.NewFile()
	for _, e := range entries {
		f.Set(e.slug, e.raw)
	}
	total, per, err := f.Sizes()
	if err != nil {
		return 0, err
	}
	sum := 0
	for i := range entries {
		entries[i].size = per[entries[i].slug]
		sum += entries[i].size
	}
	return total - sum, nil
}

// plannedPack is one pack file: where it goes, what it is called, and what it
// holds.
type plannedPack struct {
	dir     string
	bound   string
	entries []entry
}

// ref renders the pack's location.
func (p plannedPack) ref(f pack.Family) pack.PackRef {
	return pack.PackRef{Family: f, Dir: p.dir, Bound: p.bound}
}

// fillDivisor is the fraction of a family's caps the migration fills to: half,
// so every pack has room for organic inserts before the first split is due.
const fillDivisor = 2

// planFamily assigns entries, in slug order, to packs and directories. overhead
// is the pack wrapper's own byte cost, from measure.
//
// A pack is closed when the next entry would take it past half the size target
// or half the entry cap, whichever binds first - never on the first entry, so an
// entry larger than the whole budget gets a pack of its own rather than an
// empty one before it (PACK-SPEC.md's single-entry exemption is what keeps that
// legal). Directories fill to the per-directory pack cap in order.
//
// The directory chunking here is the migration's own, and simpler than
// pkg/pack's planDirs (which preserves existing directory boundaries and splits
// an over-cap directory at its median byte position): a conversion has no
// existing boundaries to preserve, and filling in order is what makes the result
// a pure function of the slug sequence. Deliberate duplication, not an oversight.
func planFamily(def pack.FamilyDef, entries []entry, overhead int) []plannedPack {
	if len(entries) == 0 {
		return nil
	}
	sizeBudget := def.Caps.TargetSize / fillDivisor
	entryBudget := def.Caps.Entries / fillDivisor
	if entryBudget < 1 {
		entryBudget = 1
	}

	var packs []plannedPack
	var cur []entry
	size := overhead
	flush := func() {
		if len(cur) == 0 {
			return
		}
		packs = append(packs, plannedPack{bound: cur[0].slug, entries: cur})
		cur, size = nil, overhead
	}
	for _, e := range entries {
		if len(cur) > 0 && (size+e.size > sizeBudget || len(cur)+1 > entryBudget) {
			flush()
		}
		cur = append(cur, e)
		size += e.size
	}
	flush()

	// The family's first pack carries the reserved minimum bound, so it covers
	// every slug below its own first entry.
	packs[0].bound = pack.MinBound

	if !def.Dirs && len(packs) <= def.Caps.DirPacks {
		return packs // flat family, still within the per-directory cap
	}
	dir := pack.MinBound
	for i := range packs {
		if i > 0 && i%def.Caps.DirPacks == 0 {
			dir = packs[i].bound
		}
		packs[i].dir = dir
	}
	return packs
}

// render turns a planned pack into its canonical on-disk bytes.
func render(p plannedPack) ([]byte, error) {
	f := pack.NewFile()
	for _, e := range p.entries {
		f.Set(e.slug, e.raw)
	}
	return f.Bytes()
}
