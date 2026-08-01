package migrate

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// TestPlannedSizesMatchPackRender pins the planner's arithmetic against the real
// renderer. The planner sizes a candidate pack once per entry, so it adds up
// per-entry byte counts instead of re-rendering; if that sum ever drifts from
// what pkg/pack actually writes, the migration would fill packs to the wrong
// size - quietly, since the result would still be a valid tree.
func TestPlannedSizesMatchPackRender(t *testing.T) {
	entries := make([]entry, 0, 8)
	file := pack.NewFile()
	for i := 0; i < 8; i++ {
		slug := "book-" + strconv.Itoa(1000+i)
		raw := json.RawMessage(`{"id":"` + slug + `","title":"` + strings.Repeat("T", i*17) + `"}`)
		e, err := newEntry(slug, raw)
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, e)
		file.Set(slug, raw)

		want, err := file.Size()
		if err != nil {
			t.Fatal(err)
		}
		if got := renderedSize(entries); got != want {
			t.Errorf("with %d entries: planned %d bytes, pkg/pack renders %d", len(entries), got, want)
		}
	}
}

// planned returns the packs the planner produces for slugs of the given entry
// size, and their entry counts.
func plannedCounts(t *testing.T, def pack.FamilyDef, n, pad int) []plannedPack {
	t.Helper()
	entries := make([]entry, 0, n)
	for i := 0; i < n; i++ {
		slug := "book-" + strconv.Itoa(1000+i)
		raw := json.RawMessage(`{"id":"` + slug + `","pad":"` + strings.Repeat("p", pad) + `"}`)
		e, err := newEntry(slug, raw)
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, e)
	}
	return planFamily(def, entries)
}

// The fill rule: pack until the NEXT entry would cross half the size target.
func TestPlanFillsToHalfTheTarget(t *testing.T) {
	def, _ := pack.Def(pack.FamilyWorks)
	packs := plannedCounts(t, def, 60, 8000) // ~8KB each: about 16 to a half-target pack
	if len(packs) < 3 {
		t.Fatalf("60 x 8KB entries planned into %d packs, want several", len(packs))
	}
	budget := def.Caps.TargetSize / fillDivisor
	for i, p := range packs {
		size := renderedSize(p.entries)
		if size > budget && len(p.entries) > 1 {
			t.Errorf("pack %d is %d bytes with %d entries, over the %d-byte budget",
				i, size, len(p.entries), budget)
		}
		// Only the last pack may be under-filled; the rest were closed because
		// one more entry would not fit.
		if i < len(packs)-1 && size+p.entries[0].size < budget {
			t.Errorf("pack %d closed early at %d bytes", i, size)
		}
	}
}

// The entry cap binds for tiny records long before the size cap does, and the
// migration leaves the same headroom on it: half the family's entry cap.
func TestPlanRespectsHalfTheEntryCap(t *testing.T) {
	def, _ := pack.Def(pack.FamilyWorksCommunity) // 200 entries, the tightest cap
	packs := plannedCounts(t, def, 250, 0)
	if len(packs) != 3 {
		t.Fatalf("250 tiny entries planned into %d packs, want 3 at half the 200-entry cap", len(packs))
	}
	for i, p := range packs {
		if len(p.entries) > def.Caps.Entries/fillDivisor {
			t.Errorf("pack %d holds %d entries, over the %d budget", i, len(p.entries), def.Caps.Entries/fillDivisor)
		}
	}
}

// Bounds and directories: the family's first pack and directory carry the
// reserved minimum bound, every other pack is named by its own first slug, and
// a flat family stays flat until it outgrows the per-directory cap.
func TestPlanNamesBoundsAndDirectories(t *testing.T) {
	def, _ := pack.Def(pack.FamilyWorks)
	packs := plannedCounts(t, def, 60, 8000)
	if packs[0].bound != pack.MinBound || packs[0].dir != pack.MinBound {
		t.Errorf("first pack = %s/%s, want %q/%q", packs[0].dir, packs[0].bound, pack.MinBound, pack.MinBound)
	}
	for i, p := range packs[1:] {
		if p.bound != p.entries[0].slug {
			t.Errorf("pack %d bound = %q, want its first slug %q", i+1, p.bound, p.entries[0].slug)
		}
		if p.dir != pack.MinBound {
			t.Errorf("pack %d landed in directory %q; %d packs fit in one", i+1, p.dir, len(packs))
		}
	}

	flat, _ := pack.Def(pack.FamilyPeople)
	people := plannedCounts(t, flat, 40, 8000)
	for i, p := range people {
		if p.dir != "" {
			t.Errorf("flat-family pack %d gained directory %q before the cap", i, p.dir)
		}
	}
}

// Past the per-directory pack cap, a directory-carrying family opens a new
// directory named by its first pack's bound.
func TestPlanSplitsDirectoriesAtTheCap(t *testing.T) {
	def, _ := pack.Def(pack.FamilyWorks)
	// One entry per pack (each over the fill budget on its own), so the pack
	// count is the entry count and the directory cap is reachable in a test.
	packs := plannedCounts(t, def, pack.DirPackCap+3, 200*1024)
	if len(packs) != pack.DirPackCap+3 {
		t.Fatalf("planned %d packs, want one per entry", len(packs))
	}
	first, second := packs[pack.DirPackCap-1], packs[pack.DirPackCap]
	if first.dir != pack.MinBound {
		t.Errorf("pack %d is in %q, want the first directory", pack.DirPackCap-1, first.dir)
	}
	if second.dir != second.bound {
		t.Errorf("the second directory is %q, want its first pack's bound %q", second.dir, second.bound)
	}
	for _, p := range packs[pack.DirPackCap:] {
		if p.dir != second.dir {
			t.Errorf("pack %q landed in %q, want %q", p.bound, p.dir, second.dir)
		}
	}
}
