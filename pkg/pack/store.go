package pack

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// ErrLegacyLayout is returned for a family still in the file-per-entity layout.
// pkg/pack only reads and writes pack layout; during the dual-layout window a
// caller checks Store.Layout first and falls back to the legacy reader.
var ErrLegacyLayout = errors.New("family is in the legacy file-per-entity layout")

// Store is a read-through, write-behind view over a data tree's pack files: it
// answers reads from the queue first and from disk second, and writes nothing
// until Flush.
//
// Queued-write-first reads are load-bearing for the importer, which composes a
// record over several steps within one run: a Get after an Upsert of the same
// slug sees the queued entry, exactly as a per-file writer would have seen the
// file it just wrote.
//
// A store is layout-aware per family, so it is safe on a mixed tree: a family
// in pack layout, or absent entirely, is readable and writable; a family still
// in legacy layout fails every operation with ErrLegacyLayout and Flush never
// touches it. Without that a legacy people/ or series/ root would parse as a
// pack tree, since its records also sit one directory deep.
//
// A Store is not safe for concurrent use.
type Store struct {
	dir     string
	layouts map[Family]Layout
	trees   map[Family]*Tree
	cache   map[string]*File // pack path -> loaded pack
	ops     map[Family]map[string]op
	// pulls are targeted removals used by relocation: an entry has to leave the
	// wrong pack, which is not the pack its slug looks up to.
	pulls map[Family]map[string]map[string]bool // family -> pack path -> slugs
	// remove holds whole files Heal set aside: a file that is not a bound is
	// deleted once its entries have been re-queued elsewhere.
	remove map[Family]map[string]bool // family -> pack path
	// touch marks packs no queued write reached that must still be reshaped on
	// Flush, which is how an entry-count split reaches a pack nobody wrote to.
	touch map[Family]map[string]PackRef
	refs  map[string]PackRef // pack path -> ref, for packs the queue created
}

type op struct {
	entry json.RawMessage
	del   bool
}

// Open detects each family's layout under dataDir and reads the pack listing of
// the ones in pack layout. A family that is absent is still writable: its first
// Upsert creates the reserved "0" pack.
func Open(dataDir string) (*Store, error) {
	s := &Store{
		dir:     dataDir,
		layouts: map[Family]Layout{},
		trees:   map[Family]*Tree{},
		cache:   map[string]*File{},
		ops:     map[Family]map[string]op{},
		pulls:   map[Family]map[string]map[string]bool{},
		remove:  map[Family]map[string]bool{},
		touch:   map[Family]map[string]PackRef{},
		refs:    map[string]PackRef{},
	}
	for _, d := range Families() {
		l, err := DetectLayout(dataDir, d.Family)
		if err != nil {
			return nil, err
		}
		s.layouts[d.Family] = l
		if l == LayoutLegacy {
			s.trees[d.Family] = NewTree(d.Family, nil)
			continue
		}
		t, err := ReadTree(dataDir, d.Family)
		if err != nil {
			return nil, err
		}
		s.trees[d.Family] = t
	}
	return s, nil
}

// OpenFor opens dataDir and refuses it unless every named family is one this
// store may write: a family still in the file-per-entity layout fails with an
// ErrLegacyLayout-wrapped error naming it.
//
// It is what a writer wants. The migration is a flag day (PACK-SPEC.md), so a
// legacy family is a run to refuse rather than a second write path to maintain,
// and refusing at Open - before anything is planned - means a refused run has
// written nothing.
func OpenFor(dataDir string, families ...Family) (*Store, error) {
	s, err := Open(dataDir)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", dataDir, err)
	}
	for _, f := range families {
		if _, ok := Def(f); !ok {
			return nil, fmt.Errorf("unknown pack family %q", f)
		}
		if s.Layout(f) == LayoutLegacy {
			return nil, fmt.Errorf("%s/%s: %w; convert the tree to the pack layout first",
				dataDir, f.Root(), ErrLegacyLayout)
		}
	}
	return s, nil
}

// Dir returns the data root the store was opened on.
func (s *Store) Dir() string { return s.dir }

// Layout returns the layout family f was detected in at Open time.
func (s *Store) Layout(f Family) Layout { return s.layouts[f] }

// Tree returns family f's current pack listing. A legacy family has none.
func (s *Store) Tree(f Family) *Tree { return s.trees[f] }

// def resolves a family, rejecting an unknown one and one the store may not
// touch because it is still in legacy layout.
func (s *Store) def(f Family) (FamilyDef, error) {
	d, ok := Def(f)
	if !ok {
		return FamilyDef{}, fmt.Errorf("unknown pack family %q", f)
	}
	if s.layouts[f] == LayoutLegacy {
		return FamilyDef{}, fmt.Errorf("%s: %w", f.Root(), ErrLegacyLayout)
	}
	return d, nil
}

// Locate returns the pack that holds slug, or would hold it. A family with no
// packs yet locates to its reserved first pack (MinBound, under directory
// MinBound for a family that carries a directory level).
func (s *Store) Locate(f Family, slug string) (PackRef, error) {
	def, err := s.def(f)
	if err != nil {
		return PackRef{}, err
	}
	t := s.trees[f]
	if t != nil && t.Len() > 0 {
		ref, _ := t.Lookup(slug)
		return ref, nil
	}
	ref := PackRef{Family: f, Bound: MinBound}
	if def.Dirs {
		ref.Dir = MinBound
	}
	return ref, nil
}

// Get returns the raw entry stored under slug. A queued Upsert of that slug
// wins over disk and a queued Delete reads as absent, so a run sees its own
// writes. The lookup only consults the pack the slug's bound points at: an
// entry sitting in the wrong pack is invisible until Heal relocates it.
func (s *Store) Get(f Family, slug string) (json.RawMessage, bool, error) {
	ref, err := s.Locate(f, slug)
	if err != nil {
		return nil, false, err
	}
	if o, ok := s.ops[f][slug]; ok {
		if o.del {
			return nil, false, nil
		}
		return o.entry, true, nil
	}
	file, err := s.load(ref)
	if err != nil {
		return nil, false, err
	}
	e, ok := file.Get(slug)
	return e, ok, nil
}

// Has reports whether slug has an entry, queued or on disk.
func (s *Store) Has(f Family, slug string) (bool, error) {
	_, ok, err := s.Get(f, slug)
	return ok, err
}

// Upsert queues entry under slug. Nothing is written until Flush.
func (s *Store) Upsert(f Family, slug string, entry json.RawMessage) error {
	if _, err := s.def(f); err != nil {
		return err
	}
	cp := make(json.RawMessage, len(entry))
	copy(cp, entry)
	s.queue(f, slug, op{entry: cp})
	return nil
}

// Delete queues the removal of slug's entry. Nothing is written until Flush.
func (s *Store) Delete(f Family, slug string) error {
	if _, err := s.def(f); err != nil {
		return err
	}
	s.queue(f, slug, op{del: true})
	return nil
}

func (s *Store) queue(f Family, slug string, o op) {
	if s.ops[f] == nil {
		s.ops[f] = map[string]op{}
	}
	s.ops[f][slug] = o
}

// Pack returns an independent copy of a pack as it is ON DISK. Queued writes
// are deliberately not folded in - a pack is the unit Flush rewrites, so a
// half-applied view of one would invite a caller to write it back and lose the
// rest of the queue. Use Get for a single entry read queued-write-first.
//
// The ref must name a pack the family's tree holds; a pack that does not exist
// is an error rather than an empty file, so a typo does not read as "no data".
func (s *Store) Pack(ref PackRef) (*File, error) {
	if _, err := s.def(ref.Family); err != nil {
		return nil, err
	}
	want := ref.Path()
	t := s.trees[ref.Family]
	if i := t.Index(ref.Bound); i < 0 || t.Packs()[i].Path() != want {
		return nil, fmt.Errorf("no such pack %s", want)
	}
	f, err := s.load(ref)
	if err != nil {
		return nil, err
	}
	return f.Clone(), nil
}

// Touch marks a pack for reshaping on the next Flush without changing any of
// its entries. It is what carries a split that no write triggered: Flush judges
// packs the queue never reached by their on-disk size alone, so an entry-count
// violation (1,000 people is well under the 512KB size cap) would otherwise
// survive untouched. Heal calls it for every pack Pending reports, so the
// Heal-then-Flush pair always leaves a family Pending-clean.
func (s *Store) Touch(ref PackRef) error {
	if _, err := s.def(ref.Family); err != nil {
		return err
	}
	p := ref.Path()
	if s.touch[ref.Family] == nil {
		s.touch[ref.Family] = map[string]PackRef{}
	}
	s.touch[ref.Family][p] = ref
	s.refs[p] = ref
	return nil
}

// load reads the pack at ref, remembering the ref so Flush can place a pack the
// queue created.
func (s *Store) load(ref PackRef) (*File, error) {
	s.refs[ref.Path()] = ref
	return s.loadPath(ref.Path())
}

// loadPath reads a file by its data-relative path, caching it. A file that does
// not exist loads as an empty pack, so a first Upsert into a new family works
// without ceremony. Paths rather than refs, because a file Heal has to salvage
// need not sit at a pack location at all.
func (s *Store) loadPath(rel string) (*File, error) {
	if f, ok := s.cache[rel]; ok {
		return f, nil
	}
	raw, err := os.ReadFile(filepath.Join(s.dir, filepath.FromSlash(rel)))
	if err != nil {
		if os.IsNotExist(err) {
			f := NewFile()
			s.cache[rel] = f
			return f, nil
		}
		return nil, err
	}
	f, err := Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", rel, err)
	}
	s.cache[rel] = f
	return f, nil
}

// Written reports the files a Flush touched, data-relative and sorted.
type Written struct {
	// Wrote holds the packs created or rewritten.
	Wrote []string
	// Deleted holds the packs removed, either emptied or moved elsewhere.
	Deleted []string
}

// planPack is one pack's state while Flush works out the family's final shape.
type planPack struct {
	// origRef is where the pack sits on disk now. Its zero value (an empty
	// bound, which no pack may carry) marks a pack the queue created.
	origRef PackRef
	dir     string
	bound   string
	file    *File // nil while the pack is unread and unchanged
	size    int
	dirty   bool
}

// orig returns the pack's current path on disk, empty for one that does not
// exist yet.
func (p planPack) orig() string {
	if p.origRef.Bound == "" {
		return ""
	}
	return p.origRef.Path()
}

type packGroup struct {
	dir   string
	packs []planPack
}

// Flush applies the queued writes and leaves every family well-formed: due
// splits performed, directories within their pack cap, emptied packs removed,
// and the lowest pack of a family (and of each directory) carrying its
// container's bound. Output is deterministic - the same store state always
// produces the same files - and the store stays usable afterwards, with its
// queue cleared and its trees reloaded.
//
// Packs the queue did not touch are judged by their on-disk size alone, which
// is exact for a canonically formatted tree. Their entry counts are not
// re-read: an entry count only grows through this store, and that path loads
// the pack.
func (s *Store) Flush() (Written, error) {
	var w Written
	for _, d := range Families() {
		if s.layouts[d.Family] == LayoutLegacy {
			continue
		}
		fw, err := s.flushFamily(d)
		if err != nil {
			return Written{}, err
		}
		w.Wrote = append(w.Wrote, fw.Wrote...)
		w.Deleted = append(w.Deleted, fw.Deleted...)
	}
	sort.Strings(w.Wrote)
	sort.Strings(w.Deleted)
	s.ops = map[Family]map[string]op{}
	s.pulls = map[Family]map[string]map[string]bool{}
	s.remove = map[Family]map[string]bool{}
	s.touch = map[Family]map[string]PackRef{}
	s.cache = map[string]*File{}
	s.refs = map[string]PackRef{}
	return w, nil
}

func (s *Store) flushFamily(def FamilyDef) (Written, error) {
	f := def.Family
	if len(s.ops[f]) == 0 && len(s.pulls[f]) == 0 && len(s.remove[f]) == 0 &&
		len(s.touch[f]) == 0 && s.trees[f].Len() == 0 {
		return Written{}, nil
	}

	touched := map[string]bool{}
	if err := s.applyTouches(f, touched); err != nil {
		return Written{}, err
	}
	if err := s.applyPulls(f, touched); err != nil {
		return Written{}, err
	}
	if err := s.applyOps(f, touched); err != nil {
		return Written{}, err
	}

	packs, err := s.planFamily(def, touched)
	if err != nil {
		return Written{}, err
	}
	final, err := s.reshape(def, packs)
	if err != nil {
		return Written{}, err
	}
	return s.commit(def, packs, final)
}

// applyTouches loads the packs Touch marked so reshape sees their real entry
// counts. It changes no entry; a pack that reshape leaves alone is not rewritten
// either, because commit compares the rendered bytes with what is on disk.
func (s *Store) applyTouches(f Family, touched map[string]bool) error {
	paths := make([]string, 0, len(s.touch[f]))
	for p := range s.touch[f] {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		if _, err := s.load(s.touch[f][p]); err != nil {
			return err
		}
		touched[p] = true
	}
	return nil
}

// applyPulls performs relocation's targeted removals: an entry leaves the pack
// it is actually in, which is not the pack its bound points at.
func (s *Store) applyPulls(f Family, touched map[string]bool) error {
	paths := make([]string, 0, len(s.pulls[f]))
	for p := range s.pulls[f] {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		file, err := s.loadPath(p)
		if err != nil {
			return err
		}
		for slug := range s.pulls[f][p] {
			file.Remove(slug)
		}
		// A file already set aside for deletion is not part of the plan; its
		// entries were re-queued elsewhere and its bytes are never rewritten.
		if !s.removed(f, p) {
			touched[p] = true
		}
	}
	return nil
}

func (s *Store) applyOps(f Family, touched map[string]bool) error {
	slugs := make([]string, 0, len(s.ops[f]))
	for slug := range s.ops[f] {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)
	for _, slug := range slugs {
		o := s.ops[f][slug]
		ref, err := s.Locate(f, slug)
		if err != nil {
			return err
		}
		file, err := s.load(ref)
		if err != nil {
			return err
		}
		if o.del {
			file.Remove(slug)
		} else {
			file.Set(slug, o.entry)
		}
		touched[ref.Path()] = true
	}
	return nil
}

// planFamily builds the family's pack list in bound order: every pack in the
// tree plus any pack the queue created, with sizes taken from the rendered
// bytes for touched packs and from disk for the rest.
func (s *Store) planFamily(def FamilyDef, touched map[string]bool) ([]planPack, error) {
	f := def.Family
	byPath := map[string]planPack{}
	for _, ref := range s.trees[f].Packs() {
		if s.removed(f, ref.Path()) {
			continue // set aside by Heal: its entries have already moved
		}
		byPath[ref.Path()] = planPack{origRef: ref, dir: ref.Dir, bound: ref.Bound}
	}
	for p := range touched {
		ref := s.refs[p]
		if pp, ok := byPath[p]; ok {
			pp.dirty = true
			pp.file = s.cache[p]
			byPath[p] = pp
			continue
		}
		byPath[p] = planPack{dir: ref.Dir, bound: ref.Bound, file: s.cache[p], dirty: true}
	}

	out := make([]planPack, 0, len(byPath))
	for _, pp := range byPath {
		if pp.file != nil {
			if pp.file.Len() == 0 {
				continue // emptied: its file goes away
			}
			size, err := pp.file.Size()
			if err != nil {
				return nil, fmt.Errorf("%s: %w", pp.bound, err)
			}
			pp.size = size
		} else {
			info, err := os.Stat(filepath.Join(s.dir, filepath.FromSlash(pp.orig())))
			if err != nil {
				return nil, err
			}
			pp.size = int(info.Size())
		}
		out = append(out, pp)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].bound != out[j].bound {
			return out[i].bound < out[j].bound
		}
		return out[i].dir < out[j].dir
	})
	return out, nil
}

// reshape turns the applied pack list into the family's final shape: the lowest
// pack rebound to the family minimum, over-cap packs split, directories planned
// and split, and each directory's first pack rebound to the directory's bound.
func (s *Store) reshape(def FamilyDef, packs []planPack) ([]planPack, error) {
	if len(packs) == 0 {
		return nil, nil
	}
	if packs[0].bound != MinBound {
		if err := s.rebind(&packs[0], MinBound); err != nil {
			return nil, err
		}
	}

	var split []planPack
	for _, pp := range packs {
		if pp.file == nil {
			// An untouched pack is judged by its on-disk size; only one that
			// is already over the hard cap is worth reading back to split.
			if pp.size <= def.Caps.HardSize {
				split = append(split, pp)
				continue
			}
			file, err := s.load(pp.origRef)
			if err != nil {
				return nil, err
			}
			pp.file = file
		}
		parts, err := Split(def.Caps, pp.bound, pp.file)
		if err != nil {
			return nil, err
		}
		if len(parts) == 1 && !pp.dirty {
			split = append(split, pp)
			continue
		}
		for i, part := range parts {
			size, err := part.File.Size()
			if err != nil {
				return nil, err
			}
			np := planPack{dir: pp.dir, bound: part.Bound, file: part.File, size: size, dirty: true}
			if i == 0 {
				np.origRef = pp.origRef
			}
			split = append(split, np)
		}
	}
	sort.Slice(split, func(i, j int) bool { return split[i].bound < split[j].bound })

	groups := planDirs(def, split)
	if len(groups) == 0 {
		return nil, nil
	}
	usesDirs := groups[0].dir != ""
	if usesDirs {
		groups[0].dir = MinBound
	}
	var out []planPack
	for _, g := range groups {
		for i := range g.packs {
			g.packs[i].dir = g.dir
		}
		if usesDirs && g.packs[0].bound != g.dir {
			if err := s.rebind(&g.packs[0], g.dir); err != nil {
				return nil, err
			}
		}
		out = append(out, g.packs...)
	}
	return out, nil
}

// rebind lowers a pack's bound to its container's, which a deletion of the
// lowest pack forces. Widening downward is always safe: nothing covered the
// range below it. RAISING a bound is not, and is refused - every entry below the
// new bound would be orphaned, and the pack's own first entry would fall outside
// it. The survey rejects the misplacements that used to reach here, so this is
// the enforcement of a documented invariant rather than a path anyone takes.
func (s *Store) rebind(pp *planPack, bound string) error {
	if bound > pp.bound {
		return fmt.Errorf("%s: refusing to raise the pack bound to %q, which would orphan every entry below it",
			pp.orig(), bound)
	}
	if pp.file == nil {
		file, err := s.load(pp.origRef)
		if err != nil {
			return err
		}
		pp.file = file
	}
	pp.bound = bound
	pp.dirty = true
	return nil
}

// commit writes the final shape and removes the files it replaced.
func (s *Store) commit(def FamilyDef, before, after []planPack) (Written, error) {
	var w Written
	seen := map[string]bool{}
	refs := make([]PackRef, 0, len(after))
	for _, pp := range after {
		ref := PackRef{Family: def.Family, Dir: pp.dir, Bound: pp.bound}
		refs = append(refs, ref)
		p := ref.Path()
		// Two planned packs on one path would make the second write silently
		// replace the first, losing every entry the first held. Nothing should
		// be able to produce it once the survey has classified the family, so
		// this is the backstop that turns a future bug into a failed run
		// instead of missing data.
		if seen[p] {
			return Written{}, fmt.Errorf("%s: two packs planned onto one path, refusing to write", p)
		}
		seen[p] = true
		if !pp.dirty && p == pp.orig() {
			continue
		}
		file := pp.file
		if file == nil {
			var err error
			if file, err = s.load(pp.origRef); err != nil {
				return Written{}, err
			}
		}
		data, err := file.Bytes()
		if err != nil {
			return Written{}, fmt.Errorf("%s: %w", p, err)
		}
		abs := filepath.Join(s.dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return Written{}, err
		}
		if same, err := sameOnDisk(abs, data); err != nil {
			return Written{}, err
		} else if same {
			continue
		}
		if err := os.WriteFile(abs, data, 0o644); err != nil {
			return Written{}, err
		}
		w.Wrote = append(w.Wrote, p)
	}

	old := map[string]bool{}
	for _, ref := range s.trees[def.Family].Packs() {
		old[ref.Path()] = true
	}
	for _, pp := range before {
		if o := pp.orig(); o != "" {
			old[o] = true
		}
	}
	for p := range s.remove[def.Family] {
		old[p] = true
	}
	gone := make([]string, 0, len(old))
	for p := range old {
		if !seen[p] {
			gone = append(gone, p)
		}
	}
	sort.Strings(gone)
	for _, p := range gone {
		if err := os.Remove(filepath.Join(s.dir, filepath.FromSlash(p))); err != nil && !os.IsNotExist(err) {
			return Written{}, err
		}
		w.Deleted = append(w.Deleted, p)
	}
	if err := s.pruneDirs(def.Family); err != nil {
		return Written{}, err
	}

	s.trees[def.Family] = NewTree(def.Family, refs)
	sort.Strings(w.Wrote)
	sort.Strings(w.Deleted)
	return w, nil
}

// sameOnDisk reports whether the file already holds exactly data.
func sameOnDisk(abs string, data []byte) (bool, error) {
	cur, err := os.ReadFile(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return bytes.Equal(cur, data), nil
}

// pruneDirs removes directories a move or delete left empty.
func (s *Store) pruneDirs(f Family) error {
	root := filepath.Join(s.dir, f.Root())
	ents, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		sub := filepath.Join(root, e.Name())
		inner, err := os.ReadDir(sub)
		if err != nil {
			return err
		}
		if len(inner) == 0 {
			if err := os.Remove(sub); err != nil {
				return err
			}
		}
	}
	return nil
}
