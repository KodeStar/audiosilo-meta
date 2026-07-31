package pack

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// entryOf returns an entry whose canonical bytes are padded to roughly n bytes.
func entryOf(n int) json.RawMessage {
	if n < 20 {
		n = 20
	}
	return json.RawMessage(`{"pad":"` + strings.Repeat("x", n-14) + `"}`)
}

func packOf(sizes map[string]int) *File {
	f := NewFile()
	for slug, n := range sizes {
		f.Set(slug, entryOf(n))
	}
	return f
}

func testCaps(hard, entries int) Caps {
	return Caps{TargetSize: hard / 2, HardSize: hard, Entries: entries, DirPacks: 4}
}

func bounds(parts []Bounded) []string {
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = p.Bound
	}
	return out
}

func TestSplitLeavesAnUndersizedPackAlone(t *testing.T) {
	f := packOf(map[string]int{"aa": 100, "bb": 100})
	parts, err := Split(testCaps(4096, 1000), MinBound, f)
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 1 || parts[0].Bound != MinBound {
		t.Fatalf("parts = %v, want the pack unchanged", bounds(parts))
	}
}

// The cut lands on the entry boundary nearest the median byte, and the upper
// half is named by its own first slug.
func TestSplitAtMedianByte(t *testing.T) {
	f := packOf(map[string]int{"aa": 100, "bb": 100, "cc": 100, "dd": 100})
	parts, err := Split(testCaps(300, 1000), MinBound, f)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := bounds(parts), []string{MinBound, "cc"}; !equalStrings(got, want) {
		t.Fatalf("bounds = %v, want %v", got, want)
	}
	if got := parts[0].File.Slugs(); !equalStrings(got, []string{"aa", "bb"}) {
		t.Errorf("lower half = %v, want [aa bb]", got)
	}
	if got := parts[1].File.Slugs(); !equalStrings(got, []string{"cc", "dd"}) {
		t.Errorf("upper half = %v, want [cc dd]", got)
	}
}

// One outsized entry drags the median to it, which is the point: the cut is by
// bytes, not by entry count.
func TestSplitMedianFollowsBytesNotCount(t *testing.T) {
	f := packOf(map[string]int{"aa": 4000, "bb": 40, "cc": 40, "dd": 40})
	parts, err := Split(testCaps(1000, 1000), MinBound, f)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := bounds(parts), []string{MinBound, "bb"}; !equalStrings(got, want) {
		t.Fatalf("bounds = %v, want %v", got, want)
	}
	if got := parts[0].File.Slugs(); !equalStrings(got, []string{"aa"}) {
		t.Errorf("lower half = %v, want the one oversized entry", got)
	}
}

// A pack several times over its cap splits all the way down in one call.
func TestSplitRepeatsUntilEveryPartFits(t *testing.T) {
	sizes := map[string]int{}
	for i := 0; i < 16; i++ {
		sizes[fmt.Sprintf("s%02d", i)] = 100
	}
	parts, err := Split(testCaps(500, 1000), MinBound, packOf(sizes))
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) < 4 {
		t.Fatalf("parts = %v, want at least 4", bounds(parts))
	}
	for _, p := range parts {
		size, err := p.File.Size()
		if err != nil {
			t.Fatal(err)
		}
		if over, why := testCaps(500, 1000).Exceeds(p.File.Len(), size); over {
			t.Errorf("pack %s still over cap (%s)", p.Bound, why)
		}
	}
	assertPartitions(t, parts, sizes)
}

// A pack holding a single entry is exempt from the hard size cap: an entry
// larger than the cap cannot be split, so enforcing it would fail on unfixable
// data.
func TestSplitSingleEntryExemptFromSizeCap(t *testing.T) {
	f := packOf(map[string]int{"huge": 5000})
	parts, err := Split(testCaps(1000, 1000), MinBound, f)
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 1 {
		t.Fatalf("parts = %v, want the single entry left alone", bounds(parts))
	}
	over, _ := testCaps(1000, 1000).Exceeds(1, 5000)
	if over {
		t.Error("Exceeds reported a single-entry pack over the size cap")
	}
	over, why := testCaps(1000, 1000).Exceeds(2, 5000)
	if !over || why != "size" {
		t.Errorf("Exceeds(2, 5000) = %v/%q, want true/size", over, why)
	}
}

// The entry cap splits a pack that is nowhere near the size cap.
func TestSplitOnEntryCap(t *testing.T) {
	sizes := map[string]int{}
	for i := 0; i < 5; i++ {
		sizes[fmt.Sprintf("s%d", i)] = 30
	}
	caps := testCaps(1<<20, 2)
	parts, err := Split(caps, MinBound, packOf(sizes))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range parts {
		if p.File.Len() > 2 {
			t.Errorf("pack %s holds %d entries, over the cap of 2", p.Bound, p.File.Len())
		}
	}
	assertPartitions(t, parts, sizes)
}

// Determinism is the whole contract: metafmt --write on two machines has to
// produce the same tree.
func TestSplitIsDeterministic(t *testing.T) {
	sizes := map[string]int{}
	for i := 0; i < 40; i++ {
		sizes[fmt.Sprintf("slug-%02d", i)] = 50 + (i*37)%400
	}
	caps := testCaps(2000, 1000)
	first, err := Split(caps, MinBound, packOf(sizes))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		again, err := Split(caps, MinBound, packOf(sizes))
		if err != nil {
			t.Fatal(err)
		}
		if !equalStrings(bounds(first), bounds(again)) {
			t.Fatalf("run %d split differently: %v vs %v", i, bounds(first), bounds(again))
		}
		for j := range first {
			if !equalStrings(first[j].File.Slugs(), again[j].File.Slugs()) {
				t.Fatalf("run %d: pack %s holds different entries", i, first[j].Bound)
			}
		}
	}
}

// assertPartitions checks the split neither lost, duplicated, nor reordered an
// entry, and that every part is named by a bound its entries fall in.
func assertPartitions(t *testing.T, parts []Bounded, sizes map[string]int) {
	t.Helper()
	seen := map[string]bool{}
	for i, p := range parts {
		if p.File.Len() == 0 {
			t.Errorf("pack %s is empty", p.Bound)
		}
		for _, s := range p.File.Slugs() {
			if seen[s] {
				t.Errorf("entry %s appears twice", s)
			}
			seen[s] = true
			if s < p.Bound {
				t.Errorf("entry %s is below its pack bound %s", s, p.Bound)
			}
			if i+1 < len(parts) && s >= parts[i+1].Bound {
				t.Errorf("entry %s is at or past the next bound %s", s, parts[i+1].Bound)
			}
		}
		if i > 0 && p.Bound != p.File.Slugs()[0] {
			t.Errorf("pack %s is not named by its first slug %s", p.Bound, p.File.Slugs()[0])
		}
	}
	if len(seen) != len(sizes) {
		t.Errorf("split holds %d entries, want %d", len(seen), len(sizes))
	}
}

func TestPlanDirsKeepsAFlatFamilyFlat(t *testing.T) {
	def, _ := Def(FamilyPeople)
	def.Caps.DirPacks = 4
	packs := []planPack{{bound: MinBound, size: 10}, {bound: "mm", size: 10}}
	groups := planDirs(def, packs)
	if len(groups) != 1 || groups[0].dir != "" {
		t.Fatalf("groups = %v, want one flat group", groups)
	}
}

// The flat family gains a directory level in the same step it splits.
func TestPlanDirsFlatToDirTransition(t *testing.T) {
	def, _ := Def(FamilySeries)
	def.Caps.DirPacks = 2
	packs := []planPack{
		{bound: MinBound, size: 10}, {bound: "bb", size: 10},
		{bound: "cc", size: 10}, {bound: "dd", size: 10},
	}
	groups := planDirs(def, packs)
	if len(groups) != 2 {
		t.Fatalf("groups = %d, want 2", len(groups))
	}
	if groups[0].dir != MinBound || groups[1].dir != "cc" {
		t.Fatalf("dirs = %q,%q want 0,cc", groups[0].dir, groups[1].dir)
	}
	for _, g := range groups {
		if g.packs[0].bound != g.dir {
			t.Errorf("directory %s is not named by its first pack %s", g.dir, g.packs[0].bound)
		}
	}
}

// A directory over the pack cap splits; directories under it are left exactly
// where they are, so an insert never shuffles unrelated packs.
func TestPlanDirsSplitsOnlyTheOverfullDirectory(t *testing.T) {
	def, _ := Def(FamilyWorks)
	def.Caps.DirPacks = 2
	packs := []planPack{
		{dir: MinBound, bound: MinBound, size: 10},
		{dir: MinBound, bound: "bb", size: 10},
		{dir: MinBound, bound: "cc", size: 10},
		{dir: MinBound, bound: "dd", size: 10},
		{dir: "mm", bound: "mm", size: 10},
	}
	groups := planDirs(def, packs)
	var dirs []string
	for _, g := range groups {
		dirs = append(dirs, g.dir)
	}
	if !equalStrings(dirs, []string{MinBound, "cc", "mm"}) {
		t.Fatalf("dirs = %v, want [0 cc mm]", dirs)
	}
	if len(groups[2].packs) != 1 {
		t.Errorf("the untouched directory changed: %v", groups[2].packs)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
