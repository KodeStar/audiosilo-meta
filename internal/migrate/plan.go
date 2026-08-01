package migrate

import (
	"encoding/json"
	"fmt"

	"github.com/kodestar/audiosilo-meta/pkg/canonical"
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

// entry is one composed pack entry, with the exact number of bytes it
// contributes to a rendered pack.
type entry struct {
	slug string
	raw  json.RawMessage
	size int
}

// packOverhead is what a non-empty pack costs beyond its entries: the wrapper
// prefix, the newline after it, and the closing suffix. renderedSize is
// deliberately arithmetic rather than a trial render - the planner sizes a pack
// once per candidate entry, and re-rendering the pack each time would be
// quadratic in its bytes. TestPlannedSizesMatchPackRender pins the arithmetic
// against pkg/pack's real renderer.
const packOverhead = len("{\n  \"entries\": {") + len("\n") + len("  }\n}\n")

// entrySize is an entry's contribution to its pack's canonical bytes: the
// indent, the quoted key, ": ", the value rendered at entry indentation, a
// separating comma, and the newline. The last entry in a pack carries no comma,
// which renderedSize subtracts once.
func entrySize(slug string, raw json.RawMessage) (int, error) {
	key, err := canonical.Encode(slug, "")
	if err != nil {
		return 0, err
	}
	val, err := canonical.FormatIndent(raw, "    ")
	if err != nil {
		return 0, err
	}
	return len("    ") + len(key) + len(": ") + len(val) + len(",") + len("\n"), nil
}

// newEntry composes one entry and measures it.
func newEntry(slug string, raw json.RawMessage) (entry, error) {
	size, err := entrySize(slug, raw)
	if err != nil {
		return entry{}, fmt.Errorf("entry %q: %w", slug, err)
	}
	return entry{slug: slug, raw: raw, size: size}, nil
}

// renderedSize is the canonical byte length of a pack holding these entries.
func renderedSize(entries []entry) int {
	if len(entries) == 0 {
		return len("{\n  \"entries\": {}\n}\n")
	}
	total := packOverhead
	for _, e := range entries {
		total += e.size
	}
	return total - len(",") // the last entry carries no separator
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

// planFamily assigns entries, in slug order, to packs and directories.
//
// A pack is closed when the next entry would take it past half the size target
// or half the entry cap, whichever binds first - never on the first entry, so an
// entry larger than the whole budget gets a pack of its own rather than an
// empty one before it (PACK-SPEC.md's single-entry exemption is what keeps that
// legal). Directories fill to the per-directory pack cap in order.
func planFamily(def pack.FamilyDef, entries []entry) []plannedPack {
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
	size := packOverhead
	flush := func() {
		if len(cur) == 0 {
			return
		}
		packs = append(packs, plannedPack{bound: cur[0].slug, entries: cur})
		cur, size = nil, packOverhead
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
