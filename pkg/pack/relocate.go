package pack

import (
	"fmt"
	"sort"
)

// Misplaced is an entry sitting outside its bound-correct pack. A contributor
// may add an entry to roughly the right pack, or the wrong one; this is what
// names the canonical location.
type Misplaced struct {
	Family Family
	Slug   string
	From   PackRef
	To     PackRef
}

func (m Misplaced) String() string {
	return fmt.Sprintf("entry %q belongs in %s, not %s", m.Slug, m.To.Path(), m.From.Path())
}

// DueSplit is a pack over its family's hard caps, which Flush would split.
type DueSplit struct {
	Pack    PackRef
	Reason  string // "size" or "entry count"
	Size    int
	Entries int
}

func (d DueSplit) String() string {
	return fmt.Sprintf("pack %s is over its hard %s cap (%d entries, %d bytes), split due",
		d.Pack.Path(), d.Reason, d.Entries, d.Size)
}

// DueDirSplit is a directory over its family's pack cap, which Flush would
// split. An empty Dir is a flat family that has to gain a directory level.
type DueDirSplit struct {
	Family Family
	Dir    string
	Packs  int
}

func (d DueDirSplit) String() string {
	if d.Dir == "" {
		return fmt.Sprintf("family %s holds %d packs and has to gain a directory level, split due",
			d.Family.Root(), d.Packs)
	}
	return fmt.Sprintf("directory %s/%s holds %d packs, over the pack cap, split due",
		d.Family.Root(), d.Dir, d.Packs)
}

// Pending is the self-healing work a family needs. It is what metafmt --check
// reports and metafmt --write performs.
type Pending struct {
	Misplaced []Misplaced
	Packs     []DueSplit
	Dirs      []DueDirSplit
}

// Empty reports whether the family is already well-formed.
func (p Pending) Empty() bool {
	return len(p.Misplaced) == 0 && len(p.Packs) == 0 && len(p.Dirs) == 0
}

// Pending reports family f's outstanding placement and split work without
// queueing or writing anything. Unlike Flush it reads every pack, so entry-count
// violations in packs no writer touched are found too.
func (s *Store) Pending(f Family) (Pending, error) {
	def, ok := Def(f)
	if !ok {
		return Pending{}, fmt.Errorf("unknown pack family %q", f)
	}
	var p Pending
	t := s.trees[f]
	byDir := map[string]int{}
	for i, ref := range t.Packs() {
		file, err := s.load(ref)
		if err != nil {
			return Pending{}, err
		}
		byDir[ref.Dir]++
		lo, hi, _ := t.Range(i)
		for _, slug := range file.Slugs() {
			if Covers(lo, hi, slug) {
				continue
			}
			to, _ := t.Lookup(slug)
			p.Misplaced = append(p.Misplaced, Misplaced{Family: f, Slug: slug, From: ref, To: to})
		}
		size, err := file.Size()
		if err != nil {
			return Pending{}, err
		}
		if over, reason := def.Caps.Exceeds(file.Len(), size); over {
			p.Packs = append(p.Packs, DueSplit{Pack: ref, Reason: reason, Size: size, Entries: file.Len()})
		}
	}
	dirs := make([]string, 0, len(byDir))
	for d := range byDir {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	for _, d := range dirs {
		if byDir[d] > def.Caps.DirPacks {
			p.Dirs = append(p.Dirs, DueDirSplit{Family: f, Dir: d, Packs: byDir[d]})
		}
	}
	return p, nil
}

// Heal queues the moves that put every entry of family f in its bound-correct
// pack and returns what will move. Nothing is written until Flush, which also
// performs the due pack and directory splits Pending reports.
func (s *Store) Heal(f Family) ([]Misplaced, error) {
	p, err := s.Pending(f)
	if err != nil {
		return nil, err
	}
	for _, m := range p.Misplaced {
		file, err := s.load(m.From)
		if err != nil {
			return nil, err
		}
		entry, ok := file.Get(m.Slug)
		if !ok {
			return nil, fmt.Errorf("%s: entry %q vanished", m.From.Path(), m.Slug)
		}
		if err := s.Upsert(f, m.Slug, entry); err != nil {
			return nil, err
		}
		s.pull(f, m.From, m.Slug)
	}
	return p.Misplaced, nil
}

// pull records that slug must leave a specific pack, which relocation needs
// because the entry is not in the pack its bound looks up to.
func (s *Store) pull(f Family, ref PackRef, slug string) {
	if s.pulls[f] == nil {
		s.pulls[f] = map[string]map[string]bool{}
	}
	p := ref.Path()
	if s.pulls[f][p] == nil {
		s.pulls[f][p] = map[string]bool{}
	}
	s.pulls[f][p][slug] = true
	s.refs[p] = ref
}
