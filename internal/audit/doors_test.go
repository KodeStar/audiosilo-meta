package audit

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/testpack"
)

// doors_test.go pins the two ways OUT of this package against each other: the report
// files a run writes, and the in-process Analyze/Findings pair the repair pass reads.
//
// The repair pass treats a report directory as a FILTER over its own fresh Analyze run,
// matching the two by finding identity - so if the file and the in-memory record ever
// stopped being the same thing, every worklist record would read as stale and a wave
// would refuse itself wholesale.
func TestFindingsMatchTheWrittenReportFiles(t *testing.T) {
	data := filepath.Join(t.TempDir(), "data")
	out := filepath.Join(t.TempDir(), "report")
	seedDoorFixture(t, data)

	rep, err := Run(Options{DataDir: data, OutDir: out})
	if err != nil {
		t.Fatal(err)
	}
	written := 0
	for _, class := range Classes() {
		path := filepath.Join(out, ClassFile(class))
		fromFile := readClassFile(t, path)
		fromMemory := rep.Findings(class)
		if len(fromFile) != len(fromMemory) {
			t.Fatalf("%s: file holds %d records, Findings holds %d", class, len(fromFile), len(fromMemory))
		}
		written += len(fromFile)
		for i := range fromFile {
			if !reflect.DeepEqual(fromFile[i], fromMemory[i]) {
				t.Errorf("%s record %d differs between the file and Findings:\n file:   %+v\n memory: %+v",
					class, i, fromFile[i], fromMemory[i])
			}
		}
	}
	if written == 0 {
		t.Fatal("the fixture produced no findings at all, so the comparison proves nothing")
	}
}

// Classes covers every file a report holds: a class the writer emits and Classes omits
// would be a file the repair pass never reads.
func TestClassesCoversTheReportDirectory(t *testing.T) {
	data := filepath.Join(t.TempDir(), "data")
	out := filepath.Join(t.TempDir(), "report")
	seedDoorFixture(t, data)
	if _, err := Run(Options{DataDir: data, OutDir: out}); err != nil {
		t.Fatal(err)
	}
	ents, err := os.ReadDir(out)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"SUMMARY.md": true}
	for _, class := range Classes() {
		want[ClassFile(class)] = true
	}
	for _, e := range ents {
		if !want[e.Name()] {
			t.Errorf("the report holds %s, which Classes()/ClassFile() do not account for", e.Name())
		}
		delete(want, e.Name())
	}
	for name := range want {
		t.Errorf("Classes() names %s, which the report does not hold", name)
	}
}

// seedDoorFixture writes a tree that produces findings in several classes: a duplicate
// pair with a decorated title, a series, and a work missing a field.
func seedDoorFixture(t testing.TB, data string) {
	t.Helper()
	if err := os.MkdirAll(data, 0o755); err != nil {
		t.Fatal(err)
	}
	testpack.Seed(t, data, map[string]string{
		"works/ha/hammered/work.json":                    workJSON(t, "hammered", "Hammered"),
		"works/ha/hammered/recordings/luke-2011.json":    recJSON(t, "luke-2011", "hammered", withRuntime(576)),
		"works/ha/hammered-book-3/work.json":             workJSON(t, "hammered-book-3", "Hammered: The Druid Tales, Book 3"),
		"works/ha/hammered-book-3/recordings/chris.json": recJSON(t, "chris", "hammered-book-3", withRuntime(626)),
		"people/ja/jane-doe.json":                        personJSON(t, "jane-doe", "Jane Doe"),
		"people/na/nate-narrator.json":                   personJSON(t, "nate-narrator", "Nate Narrator"),
		"series/dr/druid-tales.json":                     seriesJSON(t, "druid-tales", "The Druid Tales", "hammered@3"),
	})
}

func readClassFile(t testing.TB, path string) []Finding {
	t.Helper()
	fh, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer fh.Close() //nolint:errcheck // read-only
	var out []Finding
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 0, 1<<20), 8<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var fd Finding
		if err := json.Unmarshal([]byte(line), &fd); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		out = append(out, fd)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}
