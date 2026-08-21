// Package pack implements the range-packed storage layout: many entities per
// JSON file, addressed by slug range. It is the one shared implementation of
// bound math, lookup, split, relocation, and pack read/write that every reader
// and writer in the project builds on.
//
// This package is PUBLIC API: like pkg/model and pkg/canonical it is consumed
// by the sibling audiosilo-sidecars tool as an ordinary module dependency, so
// its exported surface is a contract.
//
// The layout, in one paragraph. Four families live under the data root:
// works/ and works-community/ carry a directory level from day one, people/
// and series/ start flat and gain one only when they exceed the per-directory
// pack cap. A pack file's name minus ".json" is its inclusive lower bound and
// it covers [own bound, next sibling's bound); a directory's bound is its first
// pack's bound; only a family's very first pack and directory carry the
// reserved minimum bound "0". Bounds change only on split (plus the narrow
// rebind a deletion of the lowest pack forces, see Store.Flush), so the sorted
// tree listing IS the index and lookup is a binary search over it.
//
// Reading a tree costs ONE walk and ONE parse per pack, however many things ask
// for it. The walk is a Listing (which family every file sits under, each
// family's layout, each family's tree) and the parse is a Reader (a pack path to
// its parsed contents). A Store keeps both and hands them out, so a run that
// validates the catalogue and then writes into it - every importer and intake
// run - neither walks the tree twice nor parses a pack twice; see
// check.LoadStore.
package pack

import "sort"

// Family is one pack family. Its value is also its directory name under the
// data root.
type Family string

const (
	// FamilyWorks holds the CC0 work composites, keyed by work slug.
	FamilyWorks Family = "works"
	// FamilyWorksCommunity holds the CC BY-SA characters/recaps sidecars,
	// keyed by work slug. It is a separate family so the license boundary is
	// directory-structural as well as schema-structural.
	FamilyWorksCommunity Family = "works-community"
	// FamilyPeople holds person records, keyed by person slug.
	FamilyPeople Family = "people"
	// FamilySeries holds series records, keyed by series slug.
	FamilySeries Family = "series"
)

// Root returns the family's directory name, relative to the data root.
func (f Family) Root() string { return string(f) }

// MinBound is the reserved bound of a family's very first pack and directory.
// It sorts at or before every valid slug ("0" is the lowest first character a
// slug may start with) and the bound is inclusive, so a literal slug "0" still
// lands in it.
const MinBound = "0"

// Byte-size caps shared by every family.
const (
	// TargetSize is the fill target packs are built to. It is advisory: only
	// HardSize forces a split.
	TargetSize = 256 << 10
	// HardSize is the size a pack may not exceed, except when it holds a
	// single entry (see Caps.Exceeds).
	HardSize = 512 << 10
	// DirPackCap is the number of packs a directory may hold before it splits.
	// It keeps a directory under GitHub's 1,000-entry render limit.
	DirPackCap = 512
)

// Caps are the limits that force a pack or directory to split.
type Caps struct {
	// TargetSize is the advisory fill target in bytes.
	TargetSize int
	// HardSize is the maximum canonical byte length of a pack file.
	HardSize int
	// Entries is the maximum number of entries in one pack.
	Entries int
	// DirPacks is the maximum number of packs in one directory.
	DirPacks int
}

// Exceeds reports whether a pack holding entries of the given canonical byte
// size is over its hard caps, and why. A pack holding exactly one entry is
// exempt from the size cap: an entry larger than the cap cannot be split, so
// enforcing it would fail on unfixable data.
func (c Caps) Exceeds(entries, size int) (bool, string) {
	if entries > c.Entries {
		return true, "entry count"
	}
	if entries > 1 && size > c.HardSize {
		return true, "size"
	}
	return false, ""
}

// FamilyDef is a family's static definition.
type FamilyDef struct {
	// Family is the family this defines.
	Family Family
	// Dirs reports whether the family carries a directory level from day one.
	// A family without one is flat until it exceeds Caps.DirPacks packs.
	Dirs bool
	// Caps are the family's split limits.
	Caps Caps
}

var defs = map[Family]FamilyDef{
	FamilyWorks: {
		Family: FamilyWorks,
		Dirs:   true,
		Caps:   Caps{TargetSize: TargetSize, HardSize: HardSize, Entries: 1000, DirPacks: DirPackCap},
	},
	FamilyWorksCommunity: {
		Family: FamilyWorksCommunity,
		Dirs:   true,
		Caps:   Caps{TargetSize: TargetSize, HardSize: HardSize, Entries: 200, DirPacks: DirPackCap},
	},
	FamilyPeople: {
		Family: FamilyPeople,
		Dirs:   false,
		Caps:   Caps{TargetSize: TargetSize, HardSize: HardSize, Entries: 1000, DirPacks: DirPackCap},
	},
	FamilySeries: {
		Family: FamilySeries,
		Dirs:   false,
		Caps:   Caps{TargetSize: TargetSize, HardSize: HardSize, Entries: 1000, DirPacks: DirPackCap},
	},
}

// Families returns every family definition, in a stable order.
//
// It is the FULL table - the four families this package defines - and stays so.
// A data root need not hold all of them (see Profile, and the community-repo
// split it exists for), so a caller iterating the families of a TREE asks that
// tree's profile instead; this is what a profile is selected from.
func Families() []FamilyDef {
	out := make([]FamilyDef, 0, len(defs))
	for _, d := range defs {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Family < out[j].Family })
	return out
}

// Def returns the definition of family f. ok is false for an unknown family.
func Def(f Family) (FamilyDef, bool) {
	d, ok := defs[f]
	return d, ok
}
