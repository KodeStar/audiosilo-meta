package pack

import (
	"bytes"
	"fmt"
	"path"
	"sort"

	"github.com/kodestar/audiosilo-meta/pkg/canonical"
)

// under renders a data-relative path beneath a reporting prefix. An empty prefix
// leaves it data-relative, which is what a message about the tree itself wants.
func under(prefix, rel string) string {
	if prefix == "" {
		return rel
	}
	return path.Join(prefix, rel)
}

// Misplaced is an entry sitting outside its bound-correct pack. A contributor
// may add an entry to roughly the right pack, or the wrong one; this is what
// names the canonical location.
type Misplaced struct {
	Family Family
	Slug   string
	From   PackRef
	To     PackRef
}

func (m Misplaced) String() string { return m.StringUnder("") }

// StringUnder renders the message with every path beneath prefix, so a caller
// reporting against a data directory does not restate the sentence itself.
func (m Misplaced) StringUnder(prefix string) string {
	return fmt.Sprintf("entry %q belongs in %s, not %s",
		m.Slug, under(prefix, m.To.Path()), under(prefix, m.From.Path()))
}

// Conflict is one slug held by two files at once. The copy in the bound-correct
// pack is authoritative and survives; the misfiled duplicate is dropped, never
// merged into it, and never silently - this is the shape a merge-conflict union
// produces, and taking the misfiled copy would quietly revert whatever the
// correctly-placed one said.
type Conflict struct {
	Family Family
	Slug   string
	// Kept is the path of the pack whose copy survives.
	Kept string
	// Dropped is the path the duplicate is dropped from.
	Dropped string
	// Identical reports that the two copies said the same thing, so the drop
	// lost nothing.
	Identical bool
}

func (c Conflict) String() string { return c.StringUnder("") }

func (c Conflict) StringUnder(prefix string) string {
	tail := "the copies differ, and the misfiled one is discarded"
	if c.Identical {
		tail = "the copies are identical"
	}
	return fmt.Sprintf("entry %q is in both %s and %s: the correctly-placed copy is kept, %s",
		c.Slug, under(prefix, c.Kept), under(prefix, c.Dropped), tail)
}

// Salvage is a file under a family root that must not exist where or as it is:
// its name is not a bound, its directory does not cover it, it is nested too
// deep, or it holds nothing. Healing moves whatever entries it holds into their
// bound-correct packs and deletes the file.
//
// One category on purpose. Every one of these means the same thing - the file
// does not name a real bound, so nothing may treat it as one - and the answer is
// always the same: keep the entries, drop the file.
type Salvage struct {
	Family Family
	// Path is the file's data-relative path. A salvaged file need not sit at a
	// pack location at all, so it is a path rather than a PackRef.
	Path    string
	Reason  string
	Entries int
}

func (s Salvage) String() string { return s.StringUnder("") }

func (s Salvage) StringUnder(prefix string) string {
	if s.Entries == 0 {
		return fmt.Sprintf("%s is not a pack: %s; it will be deleted",
			under(prefix, s.Path), s.Reason)
	}
	return fmt.Sprintf("%s is not a pack: %s; its %d entries move to their bound-correct packs and the file is deleted",
		under(prefix, s.Path), s.Reason, s.Entries)
}

// Unreadable is a file under a family root that nothing here can interpret - it
// is not JSON, or not a pack wrapper. It is reported and left exactly as it is:
// deleting a file whose contents the tooling does not understand is never the
// right heal, so this is the one category Heal cannot clear.
type Unreadable struct {
	Family Family
	Path   string
	Err    string
}

func (u Unreadable) String() string { return u.StringUnder("") }

func (u Unreadable) StringUnder(prefix string) string {
	return fmt.Sprintf("%s cannot be read as a pack (%s); it needs a human, the tooling will not touch it",
		under(prefix, u.Path), u.Err)
}

// DueSplit is a pack over its family's hard caps, which Flush would split.
type DueSplit struct {
	Pack    PackRef
	Reason  string // "size" or "entry count"
	Size    int
	Entries int
}

func (d DueSplit) String() string { return d.StringUnder("") }

func (d DueSplit) StringUnder(prefix string) string {
	return fmt.Sprintf("pack %s is over its hard %s cap (%d entries, %d bytes), split due",
		under(prefix, d.Pack.Path()), d.Reason, d.Entries, d.Size)
}

// DueDirSplit is a directory over its family's pack cap, which Flush would
// split. An empty Dir is a flat family that has to gain a directory level.
type DueDirSplit struct {
	Family Family
	Dir    string
	Packs  int
}

func (d DueDirSplit) String() string { return d.StringUnder("") }

func (d DueDirSplit) StringUnder(prefix string) string {
	if d.Dir == "" {
		return fmt.Sprintf("family %s holds %d packs and has to gain a directory level, split due",
			under(prefix, d.Family.Root()), d.Packs)
	}
	return fmt.Sprintf("directory %s holds %d packs, over the pack cap, split due",
		under(prefix, path.Join(d.Family.Root(), d.Dir)), d.Packs)
}

// DueRebind is a pack whose name or directory has to change for the family's
// bounds to keep covering every slug: the lowest pack of a family, or of a
// directory, takes its container's bound, and the first directory takes the
// reserved minimum. It is the only bound change that is not a split, it only
// ever widens a pack's range DOWNWARD (always safe - nothing covered the range
// below it), and a deletion of the previous lowest pack is what forces it.
type DueRebind struct {
	Family Family
	From   PackRef
	To     PackRef
}

func (d DueRebind) String() string { return d.StringUnder("") }

func (d DueRebind) StringUnder(prefix string) string {
	return fmt.Sprintf("pack %s has to become %s so the lowest bound still covers every slug",
		under(prefix, d.From.Path()), under(prefix, d.To.Path()))
}

// Pending is everything a family needs to become well-formed. It is what metafmt
// --check reports and, apart from Unreadable, what Heal + Flush performs.
//
// The categories together cover every structural invariant metacheck's pack
// walker enforces. That parity is the contract: a tree metacheck rejects is
// never a tree this reports as clean, because anything invisible here would be
// processed by Flush as though it were valid.
type Pending struct {
	// Salvage holds files that are not bounds; their entries are relocated and
	// the files deleted. It is listed first because everything else is computed
	// against the packs that remain once these are set aside.
	Salvage []Salvage
	// Unreadable holds files Heal cannot interpret. It is the only category a
	// Heal + Flush pass does not clear.
	Unreadable []Unreadable
	// Misplaced holds entries sitting outside their bound-correct pack.
	Misplaced []Misplaced
	// Conflicts holds slugs held twice over, and which copy survives.
	Conflicts []Conflict
	// Rebinds holds the packs whose bound or directory has to widen downward.
	Rebinds []DueRebind
	// Packs holds the packs over a hard cap, Dirs the directories over the
	// per-directory pack cap.
	Packs []DueSplit
	Dirs  []DueDirSplit
}

// Empty reports whether the family is already well-formed.
func (p Pending) Empty() bool {
	return len(p.Salvage) == 0 && len(p.Unreadable) == 0 && len(p.Misplaced) == 0 &&
		len(p.Conflicts) == 0 && len(p.Rebinds) == 0 && len(p.Packs) == 0 && len(p.Dirs) == 0
}

// Healable reports whether everything outstanding is something Heal + Flush can
// fix. Only Unreadable is not.
func (p Pending) Healable() bool { return len(p.Unreadable) == 0 }

// Lines renders every outstanding item, in the order Heal performs them.
func (p Pending) Lines() []string { return p.LinesUnder("") }

// LinesUnder renders every outstanding item with paths beneath prefix, so a
// caller reporting against a data directory does not duplicate the phrasing.
func (p Pending) LinesUnder(prefix string) []string {
	var out []string
	for _, x := range p.Unreadable {
		out = append(out, x.StringUnder(prefix))
	}
	for _, x := range p.Salvage {
		out = append(out, x.StringUnder(prefix))
	}
	for _, x := range p.Conflicts {
		out = append(out, x.StringUnder(prefix))
	}
	for _, x := range p.Misplaced {
		out = append(out, x.StringUnder(prefix))
	}
	for _, x := range p.Rebinds {
		out = append(out, x.StringUnder(prefix))
	}
	for _, x := range p.Packs {
		out = append(out, x.StringUnder(prefix))
	}
	for _, x := range p.Dirs {
		out = append(out, x.StringUnder(prefix))
	}
	return out
}

// Pending reports family f's outstanding structural work without queueing or
// writing anything. It reads every file under the family root, so unlike Flush
// it also sees violations in packs no writer touched.
func (s *Store) Pending(f Family) (Pending, error) {
	def, err := s.def(f)
	if err != nil {
		return Pending{}, err
	}
	fs, err := s.survey(def)
	if err != nil {
		return Pending{}, err
	}
	return s.pendingFrom(fs)
}

// pendingFrom computes the report from an already-classified family.
func (s *Store) pendingFrom(fs *familySurvey) (Pending, error) {
	def := fs.def
	p := Pending{Salvage: fs.salvage, Unreadable: fs.unreadable}
	t := fs.tree

	// claimed records which pack keeps each slug. An entry already sitting in
	// its bound-correct pack claims it before any relocation is considered, so
	// a misfiled duplicate can never displace the copy readers are seeing.
	claimed := map[string]string{}
	for i, ref := range t.Packs() {
		lo, hi, _ := t.Range(i)
		for _, slug := range fs.files[ref.Path()].Slugs() {
			if Covers(lo, hi, slug) {
				claimed[slug] = ref.Path()
			}
		}
	}

	for _, sv := range fs.salvage {
		file := s.cached(sv.Path)
		if file == nil {
			continue
		}
		for _, slug := range file.Slugs() {
			s.noteMove(&p, def, fs, claimed, sv.Path, slug, nil)
		}
	}

	for i, ref := range t.Packs() {
		file := fs.files[ref.Path()]
		lo, hi, _ := t.Range(i)
		for _, slug := range file.Slugs() {
			if Covers(lo, hi, slug) {
				continue
			}
			from := ref
			s.noteMove(&p, def, fs, claimed, ref.Path(), slug, &from)
		}
		size, err := file.Size()
		if err != nil {
			return Pending{}, err
		}
		if over, reason := def.Caps.Exceeds(file.Len(), size); over {
			p.Packs = append(p.Packs, DueSplit{Pack: ref, Reason: reason, Size: size, Entries: file.Len()})
		}
	}

	p.Rebinds = plannedRebinds(def, t)
	p.Dirs = dueDirSplits(def, t)
	return p, nil
}

// noteMove records what happens to one entry that has to leave where it is: it
// relocates to its bound-correct pack, unless that pack already holds the slug,
// in which case the correctly-placed copy wins and this one is a dropped
// duplicate. from is nil for an entry coming out of a salvaged file, which has
// no pack identity to report a relocation against.
func (s *Store) noteMove(p *Pending, def FamilyDef, fs *familySurvey, claimed map[string]string, fromPath, slug string, from *PackRef) {
	to := targetFor(def, fs.tree, slug)
	if kept, taken := claimed[slug]; taken && kept != fromPath {
		p.Conflicts = append(p.Conflicts, Conflict{
			Family:    def.Family,
			Slug:      slug,
			Kept:      kept,
			Dropped:   fromPath,
			Identical: sameEntry(s.cached(fromPath), s.cached(kept), slug),
		})
		return
	}
	claimed[slug] = to.Path()
	if from == nil || *from == to {
		// A slug below the family's lowest bound looks up to the very pack it
		// already sits in. Nothing relocates - the pack's bound widens down to
		// the reserved minimum instead, which plannedRebinds reports. Calling
		// that a move would print "belongs in X, not X".
		return
	}
	p.Misplaced = append(p.Misplaced, Misplaced{Family: def.Family, Slug: slug, From: *from, To: to})
}

// targetFor returns the pack an entry belongs in once the tree holds only
// authoritative packs. A family whose every pack was salvaged targets the
// reserved first pack.
func targetFor(def FamilyDef, t *Tree, slug string) PackRef {
	if t.Len() > 0 {
		ref, _ := t.Lookup(slug)
		return ref
	}
	ref := PackRef{Family: def.Family, Bound: MinBound}
	if def.Dirs {
		ref.Dir = MinBound
	}
	return ref
}

// sameEntry reports whether two packs hold the same canonical bytes for a slug.
func sameEntry(a, b *File, slug string) bool {
	if a == nil || b == nil {
		return false
	}
	ra, oka := a.Get(slug)
	rb, okb := b.Get(slug)
	if !oka || !okb {
		return false
	}
	ca, err := canonical.FormatIndent(ra, "")
	if err != nil {
		return false
	}
	cb, err := canonical.FormatIndent(rb, "")
	if err != nil {
		return false
	}
	return bytes.Equal(ca, cb)
}

// plannedRebinds names the downward bound and directory changes Flush performs
// so the family's lowest bound keeps covering every slug. It mirrors
// Store.reshape's normalization: the first pack takes the reserved minimum, the
// first directory takes it too, and every other directory's first pack takes its
// directory's bound.
func plannedRebinds(def FamilyDef, t *Tree) []DueRebind {
	packs := t.Packs()
	if len(packs) == 0 {
		return nil
	}
	want := make([]PackRef, len(packs))
	copy(want, packs)
	if want[0].Dir != "" && want[0].Dir != MinBound {
		firstDir := want[0].Dir
		for i := range want {
			if want[i].Dir != firstDir {
				break
			}
			want[i].Dir = MinBound
		}
	}
	want[0].Bound = MinBound
	seen := map[string]bool{}
	for i := range want {
		d := want[i].Dir
		if d == "" || seen[d] {
			continue
		}
		seen[d] = true
		want[i].Bound = d
	}
	var out []DueRebind
	for i := range want {
		if want[i] != packs[i] {
			out = append(out, DueRebind{Family: def.Family, From: packs[i], To: want[i]})
		}
	}
	return out
}

// dueDirSplits names the directories over the per-directory pack cap, and the
// flat family that has to gain a directory level.
func dueDirSplits(def FamilyDef, t *Tree) []DueDirSplit {
	byDir := map[string]int{}
	for _, ref := range t.Packs() {
		byDir[ref.Dir]++
	}
	dirs := make([]string, 0, len(byDir))
	for d := range byDir {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	var out []DueDirSplit
	for _, d := range dirs {
		if byDir[d] > def.Caps.DirPacks {
			out = append(out, DueDirSplit{Family: def.Family, Dir: d, Packs: byDir[d]})
		}
	}
	return out
}

// Heal queues everything family f needs to become well-formed and returns the
// entries that move. Nothing is written until Flush; the pair is what converges,
// in ONE pass.
//
// It performs, in order: set the files that are not bounds aside (their entries
// are re-queued into their bound-correct packs and the files deleted), move the
// entries sitting outside their pack's range, resolve a slug held twice by
// keeping the correctly-placed copy, and mark the packs that owe a split. The
// bound rebinds and the directory splits are Flush's own normalization. After
// Heal + Flush, Pending on the family is empty apart from files nothing could
// read.
//
// It narrows the family's tree to the packs that survive, so no later Upsert or
// Flush can address a file that is on its way out.
func (s *Store) Heal(f Family) ([]Misplaced, error) {
	p, err := s.HealPending(f)
	if err != nil {
		return nil, err
	}
	return p.Misplaced, nil
}

// HealPending is Heal plus the report it computed on the way: exactly what
// Pending would have returned for the family, at the moment the healing was
// planned.
//
// A caller that wants both - metafmt reports what is outstanding and then fixes
// it - would otherwise survey the family twice, reading and classifying every
// file in it for a second answer identical to the first.
func (s *Store) HealPending(f Family) (Pending, error) {
	def, err := s.def(f)
	if err != nil {
		return Pending{}, err
	}
	fs, err := s.survey(def)
	if err != nil {
		return Pending{}, err
	}
	p, err := s.pendingFrom(fs)
	if err != nil {
		return Pending{}, err
	}

	// From here on only the surviving packs are addressable.
	s.trees[f] = fs.tree

	dropped := map[string]map[string]bool{}
	for _, c := range p.Conflicts {
		if dropped[c.Dropped] == nil {
			dropped[c.Dropped] = map[string]bool{}
		}
		dropped[c.Dropped][c.Slug] = true
	}

	for _, sv := range p.Salvage {
		file, lerr := s.loadPath(sv.Path)
		if lerr != nil {
			return Pending{}, lerr
		}
		for _, slug := range file.Slugs() {
			if dropped[sv.Path][slug] {
				continue
			}
			entry, _ := file.Get(slug)
			if uerr := s.Upsert(f, slug, entry); uerr != nil {
				return Pending{}, uerr
			}
		}
		s.markRemoved(f, sv.Path)
	}

	for _, m := range p.Misplaced {
		file, lerr := s.loadPath(m.From.Path())
		if lerr != nil {
			return Pending{}, lerr
		}
		entry, ok := file.Get(m.Slug)
		if !ok {
			continue
		}
		if uerr := s.Upsert(f, m.Slug, entry); uerr != nil {
			return Pending{}, uerr
		}
		s.pull(f, m.From.Path(), m.Slug)
	}

	paths := make([]string, 0, len(dropped))
	for dp := range dropped {
		paths = append(paths, dp)
	}
	sort.Strings(paths)
	for _, dropPath := range paths {
		if s.removed(f, dropPath) {
			continue // the whole file is going away anyway
		}
		for slug := range dropped[dropPath] {
			s.pull(f, dropPath, slug)
		}
	}

	for _, d := range p.Packs {
		if terr := s.Touch(d.Pack); terr != nil {
			return Pending{}, terr
		}
	}
	return p, nil
}

// pull records that slug must leave a specific pack, which relocation needs
// because the entry is not in the pack its bound looks up to.
func (s *Store) pull(f Family, packPath, slug string) {
	if s.pulls[f] == nil {
		s.pulls[f] = map[string]map[string]bool{}
	}
	if s.pulls[f][packPath] == nil {
		s.pulls[f][packPath] = map[string]bool{}
	}
	s.pulls[f][packPath][slug] = true
}

// markRemoved queues a whole file for deletion at the next Flush.
func (s *Store) markRemoved(f Family, packPath string) {
	if s.remove[f] == nil {
		s.remove[f] = map[string]bool{}
	}
	s.remove[f][packPath] = true
}

func (s *Store) removed(f Family, packPath string) bool { return s.remove[f][packPath] }
