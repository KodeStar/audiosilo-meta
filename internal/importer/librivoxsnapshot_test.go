package importer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// writeRawSnapshot writes pages (file name -> body) verbatim under pages/, with
// a fetcher's manifest beside them when manifest is set.
func writeRawSnapshot(t *testing.T, manifest bool, pages map[string]string) string {
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
	if manifest {
		m := `{"feed":"` + LibrivoxFeedURL + `","query":"` + librivoxQuery + `","limit":100,"complete":true}`
		if err := os.WriteFile(filepath.Join(dir, librivoxManifestFile), []byte(m), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
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

// A tranche of a fetcher-written snapshot decodes only the pages that can hold
// its ids: the middle page here is not JSON past its first project, so any run
// that decoded it would fail.
func TestLibrivoxTrancheDecodesOnlyItsPages(t *testing.T) {
	dir := writeRawSnapshot(t, true, map[string]string{
		"offset-0000000.json": `{"books":[{"id":"100","title":"A"},{"id":"150","title":"B"}]}`,
		"offset-0000002.json": `{"books":[{"id":"300","title":"C"}, NOT JSON`,
		"offset-0000003.json": `{"books":[{"id":"400","title":"D"},{"id":"450","title":"E"}]}`,
	})
	for sel, want := range map[string][]int{
		"100-299":     {100, 150},
		"400-":        {400, 450},
		"150,450":     {150, 450},
		"1-99":        {},
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

// The page index needs a fetcher-written snapshot: without a manifest, or with
// a page not named by its offset, every page is read (the broken middle page
// fails the run whatever it selects).
func TestLibrivoxHandAssembledSnapshotIsReadWhole(t *testing.T) {
	pages := map[string]string{
		"offset-0000000.json": `{"books":[{"id":"100","title":"A"}]}`,
		"offset-0000001.json": `{"books":[{"id":"300","title":"C"}, NOT JSON`,
	}
	ids := mustLibrivoxIDs("100")
	if _, _, err := readLibrivoxSnapshot(writeRawSnapshot(t, false, pages), &ids); err == nil {
		t.Error("a snapshot without a manifest was read by its page index")
	}
	pages["extra.json"] = `{"books":[{"id":"500","title":"E"}]}`
	if _, _, err := readLibrivoxSnapshot(writeRawSnapshot(t, true, pages), &ids); err == nil {
		t.Error("a snapshot with a page not named by its offset was read by its page index")
	}
}

// A decoded page holding an id outside its span means the order did not hold:
// the snapshot is read whole, so a selected id on another page is still found.
func TestLibrivoxOutOfOrderSnapshotIsReadWhole(t *testing.T) {
	dir := writeRawSnapshot(t, true, map[string]string{
		"offset-0000000.json": `{"books":[{"id":"100","title":"A"},{"id":"250","title":"B"}]}`,
		"offset-0000002.json": `{"books":[{"id":"200","title":"C"},{"id":"120","title":"D"}]}`,
	})
	if got, want := snapshotIDs(t, dir, "100-150"), []int{100, 120}; !reflect.DeepEqual(got, want) {
		t.Errorf("--ids 100-150 = %v, want %v", got, want)
	}
	// First ids that do not ascend are never indexed either.
	dir = writeRawSnapshot(t, true, map[string]string{
		"offset-0000000.json": `{"books":[{"id":"300","title":"A"}]}`,
		"offset-0000001.json": `{"books":[{"id":"100","title":"B"}]}`,
	})
	if got, want := snapshotIDs(t, dir, "300-"), []int{300}; !reflect.DeepEqual(got, want) {
		t.Errorf("--ids 300- = %v, want %v", got, want)
	}
}

func TestLibrivoxFirstID(t *testing.T) {
	dir := t.TempDir()
	for body, want := range map[string]int{
		`{"books":[{"id":"527","title":"Metamorphosis"}]}`:         527,
		`{"books":[{"title":"x","authors":[{"id":"9"}],"id":42}]}`: 42,
		`{"meta":{"n":1},"books":[{"id":"7"}]}`:                    7,
		`{"books":[]}`:                                             0,
		`{"books":[{"id":"abc"}]}`:                                 0,
		`not json`:                                                 0,
	} {
		path := filepath.Join(dir, "page.json")
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		got, ok := librivoxFirstID(path)
		if got != want || ok != (want > 0) {
			t.Errorf("librivoxFirstID(%s) = %d, %v; want %d", body, got, ok, want)
		}
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
	snap := writeRawSnapshot(t, false, map[string]string{
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
