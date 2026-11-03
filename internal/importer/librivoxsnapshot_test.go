package importer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// writeRawSnapshot writes pages (file name -> body) verbatim under pages/, and
// the manifest beside them when m is not nil, through the fetcher's own
// writeManifest.
func writeRawSnapshot(t *testing.T, m *librivoxManifest, pages map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, librivoxPagesDir), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range pages {
		if err := os.WriteFile(filepath.Join(dir, librivoxPagesDir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if m != nil {
		if err := writeManifest(dir, *m); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// withPageIDs is a complete fetch's manifest recording the given page ranges.
func withPageIDs(ranges map[string]librivoxPageIDs) *librivoxManifest {
	return &librivoxManifest{Feed: LibrivoxFeedURL, Query: librivoxQuery, Limit: librivoxPageLimit, Complete: true, PageIDs: ranges}
}

func snapshotIDs(t *testing.T, dir, sel string) []int {
	t.Helper()
	ids := mustLibrivoxIDs(sel)
	projects, _, err := readLibrivoxSnapshot(dir, &ids)
	if err != nil {
		t.Fatalf("--ids %s: %v", sel, err)
	}
	out := []int{}
	for _, p := range projects {
		out = append(out, int(p.ID))
	}
	return out
}

// threePages is a snapshot whose middle page is not JSON past its first
// project, so any read that decoded it fails.
var threePages = map[string]string{
	librivoxPageName(0):   `{"books":[{"id":"100","title":"A"},{"id":"150","title":"B"}]}`,
	librivoxPageName(100): `{"books":[{"id":"300","title":"C"}, NOT JSON`,
	librivoxPageName(200): `{"books":[{"id":"400","title":"D"},{"id":"450","title":"E"}]}`,
}

var threeRanges = map[string]librivoxPageIDs{
	librivoxPageName(0):   {First: 100, Last: 150},
	librivoxPageName(100): {First: 300, Last: 300},
	librivoxPageName(200): {First: 400, Last: 450},
}

// A tranche decodes only the pages whose recorded id range meets it.
func TestLibrivoxTrancheDecodesOnlyItsPages(t *testing.T) {
	dir := writeRawSnapshot(t, withPageIDs(threeRanges), threePages)
	for sel, want := range map[string][]int{
		"100-299":     {100, 150},
		"400-":        {400, 450},
		"150,450":     {150, 450},
		"1-99":        {},
		"151-299":     {},
		"100-150,401": {100, 150},
	} {
		if got := snapshotIDs(t, dir, sel); !reflect.DeepEqual(got, want) {
			t.Errorf("--ids %s = %v, want %v", sel, got, want)
		}
	}
	// The pages that can hold the selection ARE decoded.
	ids := mustLibrivoxIDs("300")
	if _, _, err := readLibrivoxSnapshot(dir, &ids); err == nil {
		t.Error("--ids 300 read past the broken page")
	}
}

// Without a range for every page - no manifest, a manifest from before the
// ranges were recorded, or a page the manifest does not name - the snapshot is
// read whole, so the broken page fails the run whatever it selects.
func TestLibrivoxSnapshotWithoutRangesIsReadWhole(t *testing.T) {
	partial := map[string]librivoxPageIDs{}
	for k, v := range threeRanges {
		if k != librivoxPageName(100) {
			partial[k] = v
		}
	}
	for name, m := range map[string]*librivoxManifest{
		"no manifest":     nil,
		"no ranges":       withPageIDs(nil),
		"a page left out": withPageIDs(partial),
	} {
		ids := mustLibrivoxIDs("100")
		if _, _, err := readLibrivoxSnapshot(writeRawSnapshot(t, m, threePages), &ids); err == nil {
			t.Errorf("%s: the snapshot was read by page ranges", name)
		}
	}
}

// A decoded page holding an id outside its recorded range means the manifest no
// longer describes it: the snapshot is read whole, so an id another page holds
// outside ITS range is still found.
func TestLibrivoxPageOutsideItsRangeIsReadWhole(t *testing.T) {
	dir := writeRawSnapshot(t, withPageIDs(map[string]librivoxPageIDs{
		librivoxPageName(0):   {First: 100, Last: 150},
		librivoxPageName(100): {First: 200, Last: 250},
	}), map[string]string{
		librivoxPageName(0):   `{"books":[{"id":"100","title":"A"},{"id":"170","title":"B"}]}`,
		librivoxPageName(100): `{"books":[{"id":"200","title":"C"},{"id":"120","title":"D"}]}`,
	})
	if got, want := snapshotIDs(t, dir, "100-150"), []int{100, 120}; !reflect.DeepEqual(got, want) {
		t.Errorf("--ids 100-150 = %v, want %v", got, want)
	}
}

func TestLibrivoxIDsOverlaps(t *testing.T) {
	ids := mustLibrivoxIDs("10-20,50-")
	for _, tc := range []struct {
		lo, hi int
		want   bool
	}{
		{1, 9, false}, {1, 10, true}, {20, 30, true}, {21, 49, false}, {49, 0, true}, {60, 70, true}, {0, 0, true},
	} {
		if got := ids.overlaps(tc.lo, tc.hi); got != tc.want {
			t.Errorf("overlaps(%d, %d) = %v, want %v", tc.lo, tc.hi, got, tc.want)
		}
	}
}

// A project whose id is missing or unreadable is warned about and skipped,
// never imported as project 0; the rest of its page imports as usual.
func TestLibrivoxUnreadableProjectIDIsWarnedAndSkipped(t *testing.T) {
	good, err := json.Marshal(fixtureProject(t, 527))
	if err != nil {
		t.Fatal(err)
	}
	snap := writeRawSnapshot(t, nil, map[string]string{
		"page.json": `{"books":[{"id":"abc","title":"Bad Id"},{"title":"No Id"},{"id":"-3","title":"Negative Id"},` + string(good) + `]}`,
	})
	sum := runLibrivox(t, snap, t.TempDir(), allLibrivox)
	if sum.LibrivoxProjects != 1 || sum.NewRecordings != 1 {
		t.Errorf("projects/recordings = %d/%d, want only 527", sum.LibrivoxProjects, sum.NewRecordings)
	}
	page := filepath.Join(snap, librivoxPagesDir, "page.json")
	var want []string
	for _, title := range []string{"Bad Id", "No Id", "Negative Id"} {
		want = append(want, "librivox snapshot "+page+": project \""+title+"\" has no readable id; skipped (no --ids can select it)")
	}
	if !reflect.DeepEqual(sum.Warnings[:min(3, len(sum.Warnings))], want) || sum.RunLevelWarnings < 3 {
		t.Errorf("warnings = %q (run-level %d), want them to lead with %q", sum.Warnings, sum.RunLevelWarnings, want)
	}
	// The whole-snapshot reader leaves them out too.
	projects, err := ReadLibrivoxSnapshot(snap)
	if err != nil || len(projects) != 1 || projects[0].ID != 527 {
		t.Errorf("ReadLibrivoxSnapshot = %d projects, %v", len(projects), err)
	}
}
