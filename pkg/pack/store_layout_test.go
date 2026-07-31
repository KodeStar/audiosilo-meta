package pack

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// During the dual-layout window the live tree is legacy while fixtures are
// packed, so a store has to survive a tree that is both. A legacy family's
// records sit one directory deep exactly like a packed people/ family, so
// without per-family detection they would parse as packs and Flush would try to
// rewrite them.
func TestStoreIsLayoutAwarePerFamily(t *testing.T) {
	dir := t.TempDir()
	// people and series: legacy file-per-entity.
	writeFile(t, filepath.Join(dir, "people", "an", "ann-doe.json"), `{"id":"ann-doe","name":"Ann Doe"}`)
	writeFile(t, filepath.Join(dir, "series", "du", "dune.json"), `{"id":"dune","name":"Dune"}`)
	// works: already packed. works-community: absent.
	writeFile(t, filepath.Join(dir, "works", "0", "0.json"), `{"entries":{"dune":{"id":"dune"}}}`)

	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := map[Family]Layout{
		FamilyPeople:         LayoutLegacy,
		FamilySeries:         LayoutLegacy,
		FamilyWorks:          LayoutPack,
		FamilyWorksCommunity: LayoutAbsent,
	}
	for f, l := range want {
		if got := s.Layout(f); got != l {
			t.Errorf("Layout(%s) = %s, want %s", f, got, l)
		}
	}

	// A legacy family is refused, not silently misread.
	if err := s.Upsert(FamilyPeople, "ann-doe", json.RawMessage(`{"id":"ann-doe"}`)); !errors.Is(err, ErrLegacyLayout) {
		t.Errorf("Upsert on a legacy family = %v, want ErrLegacyLayout", err)
	}
	if err := s.Delete(FamilySeries, "dune"); !errors.Is(err, ErrLegacyLayout) {
		t.Errorf("Delete on a legacy family = %v, want ErrLegacyLayout", err)
	}
	if _, _, err := s.Get(FamilyPeople, "ann-doe"); !errors.Is(err, ErrLegacyLayout) {
		t.Errorf("Get on a legacy family = %v, want ErrLegacyLayout", err)
	}
	if _, err := s.Pending(FamilyPeople); !errors.Is(err, ErrLegacyLayout) {
		t.Errorf("Pending on a legacy family = %v, want ErrLegacyLayout", err)
	}
	if _, err := s.Heal(FamilySeries); !errors.Is(err, ErrLegacyLayout) {
		t.Errorf("Heal on a legacy family = %v, want ErrLegacyLayout", err)
	}
	if err := s.Touch(PackRef{Family: FamilyPeople, Bound: MinBound}); !errors.Is(err, ErrLegacyLayout) {
		t.Errorf("Touch on a legacy family = %v, want ErrLegacyLayout", err)
	}
	if s.Tree(FamilyPeople).Len() != 0 {
		t.Error("a legacy family reported packs")
	}

	// The packed family works, and the absent one is still writable: its first
	// write creates the reserved 0 pack.
	if _, ok, err := s.Get(FamilyWorks, "dune"); err != nil || !ok {
		t.Fatalf("Get on the packed family = %v/%v", ok, err)
	}
	if err := s.Upsert(FamilyWorks, "eden", json.RawMessage(`{"id":"eden"}`)); err != nil {
		t.Fatal(err)
	}
	if err := s.Upsert(FamilyWorksCommunity, "dune", json.RawMessage(`{"characters":{"work":"dune"}}`)); err != nil {
		t.Fatal(err)
	}
	w := mustFlush(t, s)
	if !equalStrings(w.Wrote, []string{"works-community/0/0.json", "works/0/0.json"}) {
		t.Fatalf("wrote %v, want only the pack and absent families", w.Wrote)
	}
	if len(w.Deleted) != 0 {
		t.Errorf("deleted %v, want nothing", w.Deleted)
	}
	// The legacy families are left exactly as they were.
	for _, rel := range []string{"people/an/ann-doe.json", "series/du/dune.json"} {
		raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		if strings.Contains(string(raw), "entries") {
			t.Errorf("%s was rewritten as a pack: %s", rel, raw)
		}
	}
	if got := packPaths(t, dir, FamilyPeople); !equalStrings(got, []string{"people/an/ann-doe.json"}) {
		t.Errorf("people/ = %v, want the legacy record alone", got)
	}
}

// The binding case for Touch: at the measured 268B per person, 1,000 entries is
// ~268KB, far under the 512KB size cap, so an entry-count violation on a pack no
// writer touched is invisible to Flush's on-disk size check. Heal has to carry
// it, or self-healing is incomplete.
func TestHealThenFlushLeavesTheFamilyPendingClean(t *testing.T) {
	withCaps(t, FamilyPeople, Caps{TargetSize: 1 << 20, HardSize: 1 << 20, Entries: 2, DirPacks: 512})
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "people", "0.json"),
		`{"entries":{"aa":{"id":"aa"},"bb":{"id":"bb"},"cc":{"id":"cc"},"dd":{"id":"dd"}}}`)

	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.Pending(FamilyPeople)
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Packs) != 1 || before.Packs[0].Reason != "entry count" {
		t.Fatalf("Pending = %+v, want one entry-count split", before.Packs)
	}
	if len(before.Misplaced) != 0 {
		t.Fatalf("Pending misplaced = %+v, want none (the split is the only work)", before.Misplaced)
	}

	if _, err := s.Heal(FamilyPeople); err != nil {
		t.Fatal(err)
	}
	mustFlush(t, s)

	if got := packPaths(t, dir, FamilyPeople); len(got) < 2 {
		t.Fatalf("packs = %v, want the overfull pack split", got)
	}
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	after, err := s2.Pending(FamilyPeople)
	if err != nil {
		t.Fatal(err)
	}
	if !after.Empty() {
		t.Errorf("Heal + Flush left work pending: %+v", after)
	}
}

// Touch reshapes a pack without changing an entry, and leaves a pack that needs
// nothing exactly as it was.
func TestTouch(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "people", "0.json"), `{"entries":{"aa":{"id":"aa"}}}`)
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	ref := PackRef{Family: FamilyPeople, Bound: MinBound}
	if err := s.Touch(ref); err != nil {
		t.Fatal(err)
	}
	w := mustFlush(t, s)
	// The fixture is not canonical, so touching it rewrites it canonically.
	if !equalStrings(w.Wrote, []string{"people/0.json"}) {
		t.Fatalf("wrote %v, want the touched pack normalized", w.Wrote)
	}
	if got := readPack(t, dir, "people/0.json").Slugs(); !equalStrings(got, []string{"aa"}) {
		t.Errorf("entries = %v, want [aa] unchanged", got)
	}
	// A second touch of a now-canonical, in-cap pack writes nothing.
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s2.Touch(ref); err != nil {
		t.Fatal(err)
	}
	if w := mustFlush(t, s2); len(w.Wrote) != 0 || len(w.Deleted) != 0 {
		t.Errorf("re-touching a well-formed pack touched %v / %v", w.Wrote, w.Deleted)
	}
}

// Pack is the on-disk view, and an independent copy: a caller cannot reach into
// the store's cache through it, and a queued write does not half-apply to it.
func TestStorePackView(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "people", "0.json"), `{"entries":{"aa":{"id":"aa"}}}`)
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	ref := PackRef{Family: FamilyPeople, Bound: MinBound}
	f, err := s.Pack(ref)
	if err != nil {
		t.Fatal(err)
	}
	if !equalStrings(f.Slugs(), []string{"aa"}) {
		t.Fatalf("Pack = %v, want [aa]", f.Slugs())
	}

	// Mutating the copy cannot reach the store.
	f.Remove("aa")
	f.Set("ghost", json.RawMessage(`{"id":"ghost"}`))
	again, err := s.Pack(ref)
	if err != nil {
		t.Fatal(err)
	}
	if !equalStrings(again.Slugs(), []string{"aa"}) {
		t.Errorf("Pack after mutating the copy = %v, want [aa]", again.Slugs())
	}

	// Queued writes are deliberately not folded into the pack view.
	if err := s.Upsert(FamilyPeople, "bb", json.RawMessage(`{"id":"bb"}`)); err != nil {
		t.Fatal(err)
	}
	queued, err := s.Pack(ref)
	if err != nil {
		t.Fatal(err)
	}
	if !equalStrings(queued.Slugs(), []string{"aa"}) {
		t.Errorf("Pack = %v, want the on-disk view without the queued entry", queued.Slugs())
	}
	if _, ok, err := s.Get(FamilyPeople, "bb"); err != nil || !ok {
		t.Error("Get lost the queued entry")
	}

	// A pack the tree does not hold is an error, not an empty file.
	if _, err := s.Pack(PackRef{Family: FamilyPeople, Bound: "zz"}); err == nil {
		t.Error("Pack accepted a pack that does not exist")
	}
	// So is a family the store may not touch.
	writeFile(t, filepath.Join(dir, "series", "du", "dune.json"), `{"id":"dune"}`)
	legacy, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Pack(PackRef{Family: FamilySeries, Bound: MinBound}); !errors.Is(err, ErrLegacyLayout) {
		t.Errorf("Pack on a legacy family = %v, want ErrLegacyLayout", err)
	}
}
