package pack

import "testing"

// The cap table is the signed-off spec's, not an implementation detail: a
// change here changes what every contributor's tree looks like.
func TestFamilyCapTable(t *testing.T) {
	want := map[Family]struct {
		dirs    bool
		entries int
	}{
		FamilyWorks:          {dirs: true, entries: 1000},
		FamilyWorksCommunity: {dirs: true, entries: 200},
		FamilyPeople:         {dirs: false, entries: 1000},
		FamilySeries:         {dirs: false, entries: 1000},
	}
	fams := Families()
	if len(fams) != len(want) {
		t.Fatalf("Families() = %d, want %d", len(fams), len(want))
	}
	for _, d := range fams {
		w, ok := want[d.Family]
		if !ok {
			t.Fatalf("unexpected family %q", d.Family)
		}
		if d.Dirs != w.dirs {
			t.Errorf("%s dirs = %v, want %v", d.Family, d.Dirs, w.dirs)
		}
		if d.Caps.Entries != w.entries {
			t.Errorf("%s entry cap = %d, want %d", d.Family, d.Caps.Entries, w.entries)
		}
		if d.Caps.TargetSize != 256<<10 || d.Caps.HardSize != 512<<10 || d.Caps.DirPacks != 512 {
			t.Errorf("%s caps = %+v, want 256KB/512KB/512", d.Family, d.Caps)
		}
		if d.Family.Root() != string(d.Family) {
			t.Errorf("%s root = %q", d.Family, d.Family.Root())
		}
	}
}

func TestDefUnknownFamily(t *testing.T) {
	if _, ok := Def("nope"); ok {
		t.Error("Def accepted an unknown family")
	}
}

func TestCapsExceeds(t *testing.T) {
	c := Caps{TargetSize: 100, HardSize: 200, Entries: 3, DirPacks: 4}
	cases := []struct {
		entries, size int
		want          bool
		reason        string
	}{
		{2, 150, false, ""},
		{4, 10, true, "entry count"},
		{2, 201, true, "size"},
		{1, 100000, false, ""}, // the single-entry exemption
		{3, 200, false, ""},    // both caps are inclusive
	}
	for _, c2 := range cases {
		got, reason := c.Exceeds(c2.entries, c2.size)
		if got != c2.want || reason != c2.reason {
			t.Errorf("Exceeds(%d,%d) = %v/%q, want %v/%q",
				c2.entries, c2.size, got, reason, c2.want, c2.reason)
		}
	}
}
