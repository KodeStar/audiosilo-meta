package pack

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
// A store is also PROFILE-aware (see Profile): it carries the profile of the
// root it was opened on, plans and flushes only that profile's families, and
// refuses every operation addressed to a family outside it. A write that fell
// through to a family this root does not hold would be a silent drop - the entry
// would be planned into a directory nothing else in the tree accounts for - so it
// is an error at the door, beside the legacy-layout refusal it reads like.
//
// A Store is not safe for concurrent use.
type Store struct {
	dir     string
	profile Profile
	layouts map[Family]Layout
	trees   map[Family]*Tree
	// reader holds every pack the store has parsed. It is shareable, which is
	// how a validating writer reads each pack once (see check.LoadStore).
	reader *Reader
	// listing is the walk the store was opened on, kept so a caller that reads
	// the same tree need not walk it again. Flush clears it: the files on disk
	// are no longer the ones that were walked.
	listing *Listing
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
// Upsert creates the reserved "0" pack, and a data root that does not exist yet
// is not an error either.
//
// It takes ONE walk of the tree and answers both questions from it, and it keeps
// that walk (Listing) and its parse cache (Reader) so a caller reading the same
// tree - pkg/check, validating what the writer is about to write into - can
// share both instead of repeating them.
func Open(dataDir string) (*Store, error) { return OpenProfile(dataDir, ProfileAll) }

// OpenProfile is Open over a root holding only profile p's families (see
// Profile). The store then plans, flushes and heals exactly those families, and
// every operation naming another one fails - see def.
func OpenProfile(dataDir string, p Profile) (*Store, error) {
	l, err := listFor(dataDir, p)
	if err != nil {
		return nil, err
	}
	return openListing(l, NewReader(dataDir))
}

// openListing builds a store over an already-walked tree and a parse cache. The
// profile comes off the listing, so the store's families are exactly the ones the
// walk partitioned into.
func openListing(l *Listing, r *Reader) (*Store, error) {
	s := &Store{
		dir:     l.Dir(),
		profile: l.Profile(),
		layouts: map[Family]Layout{},
		trees:   map[Family]*Tree{},
		reader:  r,
		listing: l,
		ops:     map[Family]map[string]op{},
		pulls:   map[Family]map[string]map[string]bool{},
		remove:  map[Family]map[string]bool{},
		touch:   map[Family]map[string]PackRef{},
		refs:    map[string]PackRef{},
	}
	layouts, err := l.Layouts()
	if err != nil {
		return nil, err
	}
	for _, d := range s.profile.Families() {
		s.layouts[d.Family] = layouts[d.Family]
		if layouts[d.Family] == LayoutLegacy {
			s.trees[d.Family] = NewTree(d.Family, nil)
			continue
		}
		s.trees[d.Family] = l.Tree(d.Family)
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
	return OpenForProfile(dataDir, ProfileAll, families...)
}

// OpenForProfile is OpenFor over a root holding only profile p's families (see
// Profile). A named family the profile does not hold is refused here, for the
// same reason a legacy one is: a writer must learn at the door that this root is
// not the one its records belong in, before it has planned anything.
func OpenForProfile(dataDir string, p Profile, families ...Family) (*Store, error) {
	s, err := OpenProfile(dataDir, p)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", dataDir, err)
	}
	// ONE derivation: def already answers all three refusals (unknown family,
	// out of profile, legacy layout), so the classification is its error rather
	// than a second reading of the same state. Only the legacy one is re-worded,
	// because a WRITER can act on it - it names the full path and the conversion.
	for _, f := range families {
		_, err := s.def(f)
		switch {
		case err == nil:
		case errors.Is(err, ErrLegacyLayout):
			return nil, fmt.Errorf("%s/%s: %w; convert the tree to the pack layout first",
				dataDir, f.Root(), ErrLegacyLayout)
		default:
			return nil, err
		}
	}
	return s, nil
}

// Dir returns the data root the store was opened on.
func (s *Store) Dir() string { return s.dir }

// Profile returns the tree profile the store was opened under: the families it
// may read and write, and whether the root carries the tombstone table. A caller
// that validates the same tree reads it from here (check.LoadStore) rather than
// being told it twice.
func (s *Store) Profile() Profile { return s.profile }

// Layout returns the layout family f was detected in at Open time.
//
// It PANICS for a family outside the store's profile - see mustHold, which owns
// that decision and its message.
func (s *Store) Layout(f Family) Layout {
	s.mustHold(f, "Layout")
	return s.layouts[f]
}

// mustHold refuses a family this store's root does not hold, for the two
// accessors that answer with a VALUE rather than an error.
//
// Store.def is the door every read and write goes through, and it returns the
// refusal as an error. Tree and Layout cannot: their signatures are public API
// (pkg/pack is consumed by audiosilo-sidecars as an ordinary dependency) and
// their zero values are both LIES about an out-of-profile family - Tree hands
// back a nil *Tree, so internal/remediate's `store.Tree(FamilyWorks).Packs()`
// would nil-panic with no explanation, and Layout hands back LayoutAbsent for a
// family whose packs are sitting on disk, which is worse: a wrong answer a caller
// acts on. So the refusal is a PANIC carrying exactly the message def produces.
//
// That is the right severity, not a shortcut. Asking a store for a family its
// root does not hold is a programming error - the caller chose both the profile
// and the family - where every error these methods' neighbours return describes
// a state of the DATA (a legacy family, a missing pack). The out-of-profile arm
// can never fire under ProfileAll, which holds every family; the unknown-family
// arm CAN, under every profile, and is a deliberate hardening - these methods
// used to answer a bogus family with a silent nil/LayoutAbsent. A caller that
// wants to ASK rather than assert has Profile().Has(f).
func (s *Store) mustHold(f Family, method string) {
	if s.profile.Has(f) {
		return
	}
	if _, ok := Def(f); !ok {
		panic(fmt.Sprintf("pack: Store.%s: %s", method, unknownFamily(f)))
	}
	panic(fmt.Sprintf("pack: Store.%s: %s", method, s.outOfProfile(f)))
}

// unknownFamily is the ONE sentence a family the package does not define is
// refused with - outOfProfile's sibling, shared by def's error and mustHold's
// panic for the same reason.
func unknownFamily(f Family) string {
	return fmt.Sprintf("unknown pack family %q", f)
}

// outOfProfile is the ONE sentence a family this root does not hold is refused
// with, so def's error and mustHold's panic cannot describe it differently.
func (s *Store) outOfProfile(f Family) string {
	return fmt.Sprintf("family %q is not in the %s tree profile (this root holds %s)",
		f.Root(), s.Profile(), strings.Join(s.profile.Roots(), ", "))
}

// Reader returns the store's parse cache, so a caller reading the same tree can
// answer from a pack the store has already parsed instead of parsing it again
// (see check.LoadStore). What it holds is what the store has read, as of when it
// read it - a borrower validating the tree must not fill it with packs the store
// never asked for, or the whole tree stays resident for the rest of the run.
func (s *Store) Reader() *Reader { return s.reader }

// Listing returns the walk the store was opened on, or nil once a Flush has
// made it stale - including a FAILED flush, which has at the very least composed
// the queue into the packs it holds, and if it failed while writing has changed
// the tree as well. A caller that needs a listing either way takes a fresh one
// when this returns nil.
//
// It is the tree as of Open. Anything deciding what to write - survey, and
// therefore healing and relocation - re-scans instead, because those act on the
// files that are there now.
func (s *Store) Listing() *Listing { return s.listing }

// Tree returns family f's current pack listing. A legacy family has none.
//
// It PANICS for a family outside the store's profile - see mustHold.
func (s *Store) Tree(f Family) *Tree {
	s.mustHold(f, "Tree")
	return s.trees[f]
}

// def resolves a family, rejecting an unknown one, one this root does not hold
// under its profile, and one the store may not touch because it is still in
// legacy layout.
//
// The profile refusal is LOUD on purpose. Every read and write goes through here,
// so a store opened on a community root that is handed a works entry fails the
// run instead of queueing an entry into a family whose directory nothing else in
// this tree accounts for - the exact silent-drop shape the file accounting exists
// to prevent, one layer up.
func (s *Store) def(f Family) (FamilyDef, error) {
	d, ok := Def(f)
	if !ok {
		return FamilyDef{}, errors.New(unknownFamily(f))
	}
	if !s.profile.Has(f) {
		return FamilyDef{}, errors.New(s.outOfProfile(f))
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

// loadPath reads a file by its data-relative path through the store's reader,
// so it is parsed at most once however many times it is asked for - by this
// store, or by anything sharing the reader. A file that does not exist loads as
// an empty pack, so a first Upsert into a new family works without ceremony.
// Paths rather than refs, because a file Heal has to salvage need not sit at a
// pack location at all.
func (s *Store) loadPath(rel string) (*File, error) { return s.reader.Read(rel) }

// cached returns the pack the store's reader holds for rel, or nil. It peeks
// rather than asking Cached, because a pack this store is CREATING is held as
// the empty stand-in for a file that is not there yet, with the queued entries
// already composed into it - which is precisely the file a flush has to render.
func (s *Store) cached(rel string) *File {
	f, _ := s.reader.peek(rel)
	return f
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
	// src names the pack this plan came from, kept for diagnostics only: a
	// split's later parts have no origRef, so without it a refusal could not
	// say which two packs it was about to write onto one path.
	src   string
	dir   string
	bound string
	file  *File // nil while the pack is unread and unchanged
	size  int
	dirty bool
}

// from names the pack a plan derives from, for a message about it.
func (p planPack) from() string {
	if p.src == "" {
		return "a new pack"
	}
	return p.src
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

// familyPlan is one family's flush, worked out and validated but not yet
// written: the packs as the plan found them (before), the shape they are to be
// written in (after), and the paths that shape occupies - the refs the family's
// tree becomes, and the set commit tells a replaced file from a surviving one
// by. Planning resolves the paths once; nothing recomputes them.
type familyPlan struct {
	def    FamilyDef
	before []planPack
	after  []planPack
	refs   []PackRef
	seen   map[string]bool
}

// Flush applies the queued writes and leaves every family well-formed: due
// splits performed, directories within their pack cap, emptied packs removed,
// and the lowest pack of a family (and of each directory) carrying its
// container's bound. Output is deterministic - the same store state always
// produces the same files - and the store stays usable afterwards, with its
// queue cleared and its trees reloaded.
//
// EVERY family is planned and validated before the first byte is written. A
// plan is refused for a tree the writer must not act on - entries reaching into
// a later pack's range, and the two-packs-on-one-path backstop that follows
// from them - and refusing it after half the tree had been rewritten left an
// operator with a partially written catalogue to heal on top of the refusal.
// Planning is cheap next to writing (the packs are already parsed and
// rendered), so the whole plan set is worked out first and a refused flush
// writes nothing.
//
// Packs the queue did not touch are judged by their on-disk size alone, which
// is exact for a canonically formatted tree. Their entry counts are not
// re-read: an entry count only grows through this store, and that path loads
// the pack.
func (s *Store) Flush() (Written, error) {
	// Whatever happens from here, the Open-time walk and the parsed packs no
	// longer describe the tree: planning applies the queue to the packs it
	// holds, and a flush that FAILS part-way through writing has already
	// committed the families it got through. A defer, so no early return can
	// leave the store certifying bytes it has overwritten - or composed.
	defer func() {
		s.reader.Drop()
		s.listing = nil
	}()

	fams := s.profile.Families()
	plans := make([]familyPlan, 0, len(fams))
	for _, d := range fams {
		if s.layouts[d.Family] == LayoutLegacy {
			continue
		}
		p, ok, err := s.planFlush(d)
		if err != nil {
			return Written{}, err
		}
		if ok {
			plans = append(plans, p)
		}
	}
	var w Written
	for _, p := range plans {
		fw, err := s.commit(p)
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
	s.refs = map[string]PackRef{}
	return w, nil
}

// planFlush works out one family's final shape, and validates it, without
// writing anything. ok is false for a family with nothing queued and no packs.
func (s *Store) planFlush(def FamilyDef) (familyPlan, bool, error) {
	f := def.Family
	if len(s.ops[f]) == 0 && len(s.pulls[f]) == 0 && len(s.remove[f]) == 0 &&
		len(s.touch[f]) == 0 && s.trees[f].Len() == 0 {
		return familyPlan{}, false, nil
	}

	touched := map[string]bool{}
	if err := s.applyTouches(f, touched); err != nil {
		return familyPlan{}, false, err
	}
	if err := s.applyPulls(f, touched); err != nil {
		return familyPlan{}, false, err
	}
	if err := s.applyOps(f, touched); err != nil {
		return familyPlan{}, false, err
	}

	packs, err := s.planFamily(def, touched)
	if err != nil {
		return familyPlan{}, false, err
	}
	final, err := s.reshape(def, packs)
	if err != nil {
		return familyPlan{}, false, err
	}
	refs, seen, err := plannedPaths(def, final)
	if err != nil {
		return familyPlan{}, false, err
	}
	return familyPlan{def: def, before: packs, after: final, refs: refs, seen: seen}, true, nil
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
		byPath[ref.Path()] = planPack{origRef: ref, src: ref.Path(), dir: ref.Dir, bound: ref.Bound}
	}
	for p := range touched {
		ref := s.refs[p]
		if pp, ok := byPath[p]; ok {
			pp.dirty = true
			pp.file = s.cached(p)
			byPath[p] = pp
			continue
		}
		byPath[p] = planPack{dir: ref.Dir, bound: ref.Bound, file: s.cached(p), dirty: true}
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
	for i, pp := range packs {
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
		var next PackRef
		if i+1 < len(packs) {
			n := packs[i+1]
			next = PackRef{Family: def.Family, Dir: n.dir, Bound: n.bound}
		}
		if err := checkInRange(def, pp, next); err != nil {
			return nil, err
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
			np := planPack{src: pp.src, dir: pp.dir, bound: part.Bound, file: part.File, size: size, dirty: true}
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

// ErrMisplacedEntries is returned for a pack holding entries that belong in a
// later pack. Its range no longer describes what it holds, so splitting it
// would mint a bound another pack already carries; the entries have to be
// relocated first, which is metafmt --write's job.
var ErrMisplacedEntries = errors.New("pack holds entries outside its range")

// checkInRange refuses a pack whose entries reach into the next pack's range.
// next is the zero PackRef for the last pack, which is unbounded above.
//
// This is the root cause the two-packs-on-one-path backstop used to catch, one
// step too late: a pack named by its own first slug that also holds a LATER
// pack's first slug splits into a bound that pack already carries. The entries
// are equally lost either way - the writer that never sees them (Locate reads
// the later pack) and the split that would overwrite it - so the tree has to be
// healed before anything is written into it, and this says so precisely, naming
// the entry rather than the path collision it would have produced.
//
// Only packs the flush has read are checked, which is every pack it may rewrite:
// an untouched, in-cap pack mints no bound and is not read at all.
func checkInRange(def FamilyDef, pp planPack, next PackRef) error {
	if next.Bound == "" || pp.file == nil {
		return nil
	}
	slugs := pp.file.Slugs()
	i := sort.SearchStrings(slugs, next.Bound)
	if i == len(slugs) {
		return nil // every entry is below the next pack's bound
	}
	here := PackRef{Family: def.Family, Dir: pp.dir, Bound: pp.bound}
	return fmt.Errorf("%s: %w: entry %q belongs in %s; heal the tree with metafmt --write before writing to it",
		here.Path(), ErrMisplacedEntries, slugs[i], next.Path())
}

// plannedPaths resolves the plan's pack refs and the set of paths it writes to.
//
// Two planned packs on one path would make the second write silently replace
// the first, losing every entry the first held. checkInRange refuses the tree
// shape that is known to produce it; this is the backstop that turns any other
// route to it into a failed run instead of missing data. It runs while a family
// is being planned, so every family is resolved before the flush writes its
// first byte.
func plannedPaths(def FamilyDef, after []planPack) ([]PackRef, map[string]bool, error) {
	refs := make([]PackRef, 0, len(after))
	seen := make(map[string]bool, len(after))
	first := make(map[string]planPack, len(after))
	for _, pp := range after {
		ref := PackRef{Family: def.Family, Dir: pp.dir, Bound: pp.bound}
		p := ref.Path()
		if seen[p] {
			return nil, nil, fmt.Errorf("%s: two packs planned onto one path (%s), refusing to write",
				p, collisionSource(first[p], pp))
		}
		seen[p] = true
		first[p] = pp
		refs = append(refs, ref)
	}
	return refs, seen, nil
}

// collisionSource says where two plans on one path came from. Two parts of one
// pack's split report the pack once: naming it twice reads like a typo rather
// than like the split it is.
func collisionSource(a, b planPack) string {
	if a.src != "" && a.src == b.src {
		return fmt.Sprintf("two parts of %s's split", a.src)
	}
	return fmt.Sprintf("from %s and %s", a.from(), b.from())
}

// commit writes a planned family's final shape and removes the files it
// replaced. The plan is already resolved and validated (see planFlush).
func (s *Store) commit(plan familyPlan) (Written, error) {
	def, before, after, seen := plan.def, plan.before, plan.after, plan.seen
	var w Written
	for _, pp := range after {
		p := PackRef{Family: def.Family, Dir: pp.dir, Bound: pp.bound}.Path()
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

	s.trees[def.Family] = NewTree(def.Family, plan.refs)
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
