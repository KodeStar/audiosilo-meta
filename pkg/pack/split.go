package pack

// Bounded is a pack together with the bound it is named by.
type Bounded struct {
	Bound string
	File  *File
}

// Split divides an over-cap pack into successive bound-named packs. The first
// result keeps bound (its range only ever widens downward, never upward); each
// later one is named by its own first slug. It repeats until every result is
// within caps or holds a single entry, so a pack several times over its cap
// splits in one call.
//
// The cut is the entry boundary at the median byte position: the first boundary
// at or past half the pack's canonical size. It is a pure function of the
// pack's contents and the caps, so the same overfull pack always splits the
// same way.
func Split(c Caps, bound string, f *File) ([]Bounded, error) {
	var out []Bounded
	if err := splitInto(c, bound, f, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func splitInto(c Caps, bound string, f *File, out *[]Bounded) error {
	size, per, err := f.Sizes()
	if err != nil {
		return err
	}
	over, _ := c.Exceeds(f.Len(), size)
	if !over || f.Len() < 2 {
		*out = append(*out, Bounded{Bound: bound, File: f})
		return nil
	}
	slugs := f.Slugs()
	cut := medianCut(slugs, per)

	lower, upper := NewFile(), NewFile()
	for i, s := range slugs {
		e, _ := f.Get(s)
		if i < cut {
			lower.Set(s, e)
		} else {
			upper.Set(s, e)
		}
	}
	if err := splitInto(c, bound, lower, out); err != nil {
		return err
	}
	return splitInto(c, slugs[cut], upper, out)
}

// medianCut returns the index the sorted keys split at: the lowest index whose
// preceding entries already cover half the total size, clamped so both halves
// hold at least one entry. keys must hold at least two entries.
func medianCut(keys []string, sizes map[string]int) int {
	total := 0
	for _, k := range keys {
		total += sizes[k]
	}
	half := total / 2
	cum := 0
	for i := 0; i < len(keys)-1; i++ {
		cum += sizes[keys[i]]
		if cum >= half {
			return i + 1
		}
	}
	return len(keys) - 1
}

// planDirs assigns a family's packs, given in bound order with their byte
// sizes, to directories. Existing directory boundaries are preserved so an
// insert never shuffles unrelated packs; a directory over the pack cap splits
// at the median byte boundary, and a flat family over the cap gains the
// directory level in the same step. Each new directory is named by its first
// pack's bound.
//
// The returned groups are in bound order and every pack appears exactly once.
func planDirs(def FamilyDef, packs []planPack) []packGroup {
	if len(packs) == 0 {
		return nil
	}
	usesDirs := def.Dirs || len(packs) > def.Caps.DirPacks
	for _, p := range packs {
		if p.dir != "" {
			usesDirs = true
			break
		}
	}
	if !usesDirs {
		return []packGroup{{dir: "", packs: packs}}
	}

	var groups []packGroup
	for _, p := range packs {
		if n := len(groups); n > 0 && groups[n-1].dir == p.dir {
			groups[n-1].packs = append(groups[n-1].packs, p)
			continue
		}
		groups = append(groups, packGroup{dir: p.dir, packs: []planPack{p}})
	}

	var out []packGroup
	for _, g := range groups {
		splitDirGroup(def.Caps, g, &out)
	}
	// A group inherited from a flat family has no directory bound yet.
	for i := range out {
		if out[i].dir == "" {
			out[i].dir = out[i].packs[0].bound
		}
	}
	return out
}

// splitDirGroup halves an over-cap directory at the median byte boundary,
// repeating until every part fits. The first part keeps the directory's bound;
// each later part is named by its own first pack's bound.
func splitDirGroup(c Caps, g packGroup, out *[]packGroup) {
	if len(g.packs) <= c.DirPacks || len(g.packs) < 2 {
		*out = append(*out, g)
		return
	}
	keys := make([]string, len(g.packs))
	sizes := make(map[string]int, len(g.packs))
	for i, p := range g.packs {
		keys[i] = p.bound
		sizes[p.bound] = p.size
	}
	cut := medianCut(keys, sizes)
	splitDirGroup(c, packGroup{dir: g.dir, packs: g.packs[:cut]}, out)
	splitDirGroup(c, packGroup{dir: g.packs[cut].bound, packs: g.packs[cut:]}, out)
}
