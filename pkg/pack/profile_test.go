package pack

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// TestProfileTable pins what each profile IS, because that table is the whole
// contract: which families the root holds and whether the tombstone table is one
// of its files. Everything else in this file is a consequence of it.
func TestProfileTable(t *testing.T) {
	cases := []struct {
		profile   Profile
		families  []Family
		redirects bool
	}{
		{ProfileAll, []Family{FamilyPeople, FamilySeries, FamilyWorks, FamilyWorksCommunity}, true},
		{ProfileCore, []Family{FamilyPeople, FamilySeries, FamilyWorks}, true},
		{ProfileCommunity, []Family{FamilyWorksCommunity}, false},
		// The zero value IS the default: a profile travels in struct fields, and
		// "unset" has to keep every existing caller reading the whole tree.
		{Profile(""), []Family{FamilyPeople, FamilySeries, FamilyWorks, FamilyWorksCommunity}, true},
	}
	for _, c := range cases {
		got := make([]Family, 0, 4)
		for _, d := range c.profile.Families() {
			got = append(got, d.Family)
		}
		if !reflect.DeepEqual(got, c.families) {
			t.Errorf("%s families = %v, want %v", c.profile, got, c.families)
		}
		for _, f := range c.families {
			if !c.profile.Has(f) {
				t.Errorf("%s does not hold %s", c.profile, f)
			}
		}
		if got := c.profile.Redirects(); got != c.redirects {
			t.Errorf("%s redirects = %v, want %v", c.profile, got, c.redirects)
		}
		if !c.profile.Valid() {
			t.Errorf("%s is not valid", c.profile)
		}
	}
	// ProfileAll is the full family table, so nothing can be added to pkg/pack
	// and quietly reach no profile at all.
	all := make([]Family, 0, 4)
	for _, d := range Families() {
		all = append(all, d.Family)
	}
	got := make([]Family, 0, 4)
	for _, d := range ProfileAll.Families() {
		got = append(got, d.Family)
	}
	if !reflect.DeepEqual(got, all) {
		t.Errorf("ProfileAll families = %v, want the whole table %v", got, all)
	}
	// Core and community PARTITION it: every family is in exactly one of the two
	// halves the split produces, so nothing is lost or duplicated by it.
	half := make([]Family, 0, 4)
	for _, p := range []Profile{ProfileCore, ProfileCommunity} {
		for _, d := range p.Families() {
			half = append(half, d.Family)
		}
	}
	sort.Slice(half, func(i, j int) bool { return half[i] < half[j] })
	if !reflect.DeepEqual(half, all) {
		t.Errorf("core+community = %v, want a partition of %v", half, all)
	}
}

// TestParseProfile: the name comes off a flag, so a typo is an error naming the
// alternatives rather than a silently narrowed tree.
func TestParseProfile(t *testing.T) {
	for _, name := range Profiles() {
		p, err := ParseProfile(name)
		if err != nil {
			t.Fatalf("ParseProfile(%q): %v", name, err)
		}
		if p.String() != name {
			t.Errorf("ParseProfile(%q) = %q", name, p)
		}
	}
	_, err := ParseProfile("kore")
	if err == nil {
		t.Fatal("ParseProfile accepted an unknown name")
	}
	for _, want := range append([]string{"kore"}, Profiles()...) {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// TestListProfilePartitionsOutOfProfileFilesAsStray is the mechanism the whole
// boundary rests on: a family root the profile does not name is no family root,
// so its files land in Stray - which pkg/check reports as an unrecognized
// location. Nothing is ignored anywhere.
func TestListProfilePartitionsOutOfProfileFilesAsStray(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"works/0/0.json":           packOf1("book-one"),
		"works-community/0/0.json": packOf1("book-one"),
		"people/0.json":            packOf1("ann-doe"),
		"series/0.json":            packOf1("series-one"),
		RedirectsFile:              `{"people":{},"series":{},"works":{}}`,
	})

	l, err := ListProfile(dir, ProfileCore)
	if err != nil {
		t.Fatal(err)
	}
	if got := l.Profile(); got != ProfileCore {
		t.Errorf("listing profile = %v", got)
	}
	if want := []string{"works-community/0/0.json"}; !reflect.DeepEqual(l.Stray(), want) {
		t.Errorf("stray = %v, want %v", l.Stray(), want)
	}
	if got := l.Files(FamilyWorksCommunity); len(got) != 0 {
		t.Errorf("out-of-profile family files = %v, want none", got)
	}
	if got := l.Tree(FamilyWorksCommunity); got.Len() != 0 {
		t.Errorf("out-of-profile family tree = %v, want empty", got.Packs())
	}
	if want := []string{RedirectsFile}; !reflect.DeepEqual(l.Aux(), want) {
		t.Errorf("core aux = %v, want %v", l.Aux(), want)
	}
	layouts, err := l.Layouts()
	if err != nil {
		t.Fatal(err)
	}
	if _, listed := layouts[FamilyWorksCommunity]; listed {
		t.Error("Layouts reported a family outside the profile")
	}

	// The community half is the mirror image, and the tombstone table falls to
	// Stray there: it retires core slugs, which are not this tree's.
	c, err := ListProfile(dir, ProfileCommunity)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Aux()) != 0 {
		t.Errorf("community aux = %v, want none", c.Aux())
	}
	want := []string{"people/0.json", RedirectsFile, "series/0.json", "works/0/0.json"}
	sort.Strings(want)
	if !reflect.DeepEqual(c.Stray(), want) {
		t.Errorf("community stray = %v, want %v", c.Stray(), want)
	}
	if got := c.Files(FamilyWorksCommunity); len(got) != 1 {
		t.Errorf("community files = %v, want the one works-community pack", got)
	}
}

// TestListProfileRefusesAnUnknownProfile: the walk validates before it walks, so
// nothing downstream has to cope with a profile that names no family set.
func TestListProfileRefusesAnUnknownProfile(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"people/0.json": packOf1("ann-doe")})
	if _, err := ListProfile(dir, Profile("kore")); err == nil {
		t.Fatal("ListProfile accepted an unknown profile")
	}
	if _, err := OpenProfile(dir, Profile("kore")); err == nil {
		t.Fatal("OpenProfile accepted an unknown profile")
	}
	// Even for a root that does not exist: a bad profile is a bad profile, not an
	// absent tree to be created.
	missing := filepath.Join(t.TempDir(), "nope")
	if _, err := OpenProfile(missing, Profile("kore")); err == nil {
		t.Fatal("OpenProfile accepted an unknown profile on a missing root")
	}
}

// TestStoreRefusesAWriteOutsideItsProfile: every read and write goes through
// Store.def, so a family this root does not hold fails at the door instead of
// queueing an entry into a directory nothing else in the tree accounts for.
func TestStoreRefusesAWriteOutsideItsProfile(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"works-community/0/0.json": packOf1("book-one")})

	s, err := OpenProfile(dir, ProfileCommunity)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Profile(); got != ProfileCommunity {
		t.Errorf("store profile = %v", got)
	}

	assertRefused := func(what string, err error) {
		t.Helper()
		if err == nil {
			t.Fatalf("%s: a works operation was accepted on a community root", what)
		}
		for _, want := range []string{"works", "community"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: error %q does not mention %q", what, err, want)
			}
		}
	}
	assertRefused("Upsert", s.Upsert(FamilyWorks, "book-one", []byte(`{"id":"book-one"}`)))
	assertRefused("Delete", s.Delete(FamilyWorks, "book-one"))
	_, _, gerr := s.Get(FamilyWorks, "book-one")
	assertRefused("Get", gerr)
	_, lerr := s.Locate(FamilyWorks, "book-one")
	assertRefused("Locate", lerr)
	_, perr := s.Pending(FamilyWorks)
	assertRefused("Pending", perr)
	_, herr := s.HealPending(FamilyWorks)
	assertRefused("HealPending", herr)
	assertRefused("Touch", s.Touch(PackRef{Family: FamilyWorks, Dir: MinBound, Bound: MinBound}))

	// The refusals wrote nothing: a flush of the refused store leaves the root
	// with exactly the family it started with.
	if _, err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var roots []string
	for _, e := range ents {
		roots = append(roots, e.Name())
	}
	if want := []string{FamilyWorksCommunity.Root()}; !reflect.DeepEqual(roots, want) {
		t.Errorf("root holds %v, want %v", roots, want)
	}
}

// TestOpenForProfileRefusesAFamilyTheRootDoesNotHold is the writer's door: a run
// whose target family is not this root's learns it before it has planned
// anything, exactly as it does for a legacy family.
func TestOpenForProfileRefusesAFamilyTheRootDoesNotHold(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"works-community/0/0.json": packOf1("book-one")})

	if _, err := OpenForProfile(dir, ProfileCommunity, FamilyWorksCommunity); err != nil {
		t.Fatalf("OpenForProfile refused its own family: %v", err)
	}
	_, err := OpenForProfile(dir, ProfileCommunity, FamilyWorks)
	if err == nil {
		t.Fatal("OpenForProfile accepted a family outside the profile")
	}
	if errors.Is(err, ErrLegacyLayout) {
		t.Errorf("an out-of-profile family was reported as a legacy layout: %v", err)
	}
	// The unknown-family message is untouched by the profile check in front of it.
	if _, err := OpenForProfile(dir, ProfileAll, Family("nope")); err == nil ||
		!strings.Contains(err.Error(), `unknown pack family "nope"`) {
		t.Errorf("unknown family error = %v", err)
	}
}

// TestStoreFlushesOnlyItsProfilesFamilies: a store opened on a subset root plans
// and writes exactly that subset, so a family sitting in the tree it does not
// hold is never rewritten, split or rebound by it.
func TestStoreFlushesOnlyItsProfilesFamilies(t *testing.T) {
	dir := t.TempDir()
	// works-community's pack is deliberately NOT canonically shaped for its
	// family: a misnamed bound, which a flush of that family would rebind.
	writeFiles(t, dir, map[string]string{
		"works/0/0.json":                 packOf1("book-one"),
		"works-community/zz/zz-off.json": packOf1("zz-book"),
	})
	before, err := os.ReadFile(filepath.Join(dir, "works-community", "zz", "zz-off.json"))
	if err != nil {
		t.Fatal(err)
	}

	s, err := OpenProfile(dir, ProfileCore)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Upsert(FamilyWorks, "book-two", []byte(`{"id":"book-two"}`)); err != nil {
		t.Fatal(err)
	}
	w, err := s.Flush()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range append(append([]string{}, w.Wrote...), w.Deleted...) {
		if strings.HasPrefix(p, FamilyWorksCommunity.Root()+"/") {
			t.Errorf("flush touched %s, a family outside its profile", p)
		}
	}
	after, err := os.ReadFile(filepath.Join(dir, "works-community", "zz", "zz-off.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Error("flush rewrote a pack of a family outside its profile")
	}
}
