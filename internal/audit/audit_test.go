package audit

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/reportdir"
	"github.com/kodestar/audiosilo-meta/internal/testpack"
	"github.com/kodestar/audiosilo-meta/pkg/check"
)

// The fixture builders live in internal/testpack, beside the Seed that writes them: the
// suite that PROPOSES a repair and the suite that APPLIES it (internal/repair) have to
// seed the same records, and while each had its own copy they had already drifted on two
// details that matter (which separator a "<work>@<position>" member is cut at, and
// whether a recording's globally-unique ASIN is derived from its id alone or from its
// work too). What is local here is only the vocabulary - short names for this suite's
// prose - and the three REWRITERS below, which edit an already-rendered record.
var (
	workJSON           = testpack.WorkJSON
	recJSON            = testpack.RecJSON
	personJSON         = testpack.PersonJSON
	seriesJSON         = testpack.SeriesJSON
	charactersJSON     = testpack.CharactersJSON
	withLanguage       = testpack.WithLanguage
	withAuthors        = testpack.WithAuthors
	withRuntime        = testpack.WithRuntime
	withoutIdentifiers = testpack.WithoutIdentifiers
	withoutField       = testpack.WithoutField
)

// withAddedAt rewrites a rendered record's added_at, so a fixture can carry the RFC 3339
// spelling the storage migration's backfill wrote alongside the plain date everything
// stamped since carries.
func withAddedAt(t testing.TB, record, value string) string {
	t.Helper()
	return testpack.WithField(t, record, "added_at", value)
}

// withCredits adds a role credit to a rendered work, which is what makes two works' raw
// author lists differ while the identity rule still reduces them to one set.
func withCredits(t testing.TB, record, person, role string) string {
	t.Helper()
	return testpack.WithField(t, record, "credits", []map[string]string{{"person": person, "role": role}})
}

// runFixture seeds a tree, loads it and runs every detector. The load must be
// PROBLEM-FREE: a detector fixture that does not validate would be testing the
// audit against data the project refuses.
func runFixture(t testing.TB, files map[string]string) *Report {
	t.Helper()
	rep, res := runFixtureAllowingProblems(t, files)
	if len(res.Problems) > 0 {
		t.Fatalf("fixture does not validate: %v", res.Problems)
	}
	return rep
}

// runFixtureAllowingProblems is runFixture for the fixtures that deliberately
// violate a metacheck rule - S-INTEGRITY's duplicate position, REF-SIDECAR's
// orphan sidecar - which the audit must still report on rather than crash over.
func runFixtureAllowingProblems(t testing.TB, files map[string]string) (*Report, check.Result) {
	t.Helper()
	data := filepath.Join(t.TempDir(), "data")
	testpack.Seed(t, data, files)
	res := check.Load(data)
	if res.Catalog == nil {
		t.Fatalf("fixture produced no catalog: %v", res.Problems)
	}
	return analyze(res), res
}

// classOf returns one class's records from a report, in report order.
func classOf(t testing.TB, rep *Report, class string) []Finding {
	t.Helper()
	return rep.class(class).rows
}

// subclassOf returns the records of one class filed under one subclass.
func subclassOf(t testing.TB, rep *Report, class, subclass string) []Finding {
	t.Helper()
	var out []Finding
	for _, r := range classOf(t, rep, class) {
		if r.Subclass == subclass {
			out = append(out, r)
		}
	}
	return out
}

// workIDs is the sorted work ids a finding cites.
func workIDs(f Finding) []string {
	out := make([]string, 0, len(f.Works))
	for _, w := range f.Works {
		out = append(out, w.ID)
	}
	sort.Strings(out)
	return out
}

func TestReportIsByteIdenticalAcrossRuns(t *testing.T) {
	files := map[string]string{
		"works/ha/hammered/work.json":                    workJSON(t, "hammered", "Hammered"),
		"works/ha/hammered/recordings/luke-2011.json":    recJSON(t, "luke-2011", "hammered"),
		"works/ha/hammered-book-3/work.json":             workJSON(t, "hammered-book-3", "Hammered: The Druid Tales, Book 3"),
		"works/ha/hammered-book-3/recordings/chris.json": recJSON(t, "chris", "hammered-book-3"),
		"works/tr/tricked/work.json":                     workJSON(t, "tricked", "Tricked (Unabridged)"),
		"works/tr/tricked/recordings/luke-2012.json":     recJSON(t, "luke-2012", "tricked"),
		"people/ja/jane-doe.json":                        personJSON(t, "jane-doe", "Jane Doe"),
		"people/na/nate-narrator.json":                   personJSON(t, "nate-narrator", "Nate Narrator"),
		"series/dr/druid-tales.json":                     seriesJSON(t, "druid-tales", "The Druid Tales", "hammered@3", "tricked@4"),
	}
	data := filepath.Join(t.TempDir(), "data")
	testpack.Seed(t, data, files)

	var renders []string
	var perClass []map[string]string
	for i := 0; i < 3; i++ {
		out := filepath.Join(t.TempDir(), "out")
		rep, err := Run(Options{DataDir: data, OutDir: out})
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		_ = rep
		got := map[string]string{}
		for _, class := range classOrder {
			b, err := os.ReadFile(filepath.Join(out, classFile(class)))
			if err != nil {
				t.Fatalf("run %d: %v", i, err)
			}
			got[class] = string(b)
		}
		perClass = append(perClass, got)
		b, err := os.ReadFile(filepath.Join(out, "SUMMARY.md"))
		if err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		renders = append(renders, string(b))
	}
	for i := 1; i < len(renders); i++ {
		if renders[i] != renders[0] {
			t.Errorf("SUMMARY.md differs between run 0 and run %d", i)
		}
		for _, class := range classOrder {
			if perClass[i][class] != perClass[0][class] {
				t.Errorf("%s differs between run 0 and run %d", classFile(class), i)
			}
		}
	}
	// No timestamp may leak into a report: it would make every run a diff.
	for _, marker := range []string{"20:", "generated", "T00:00:00"} {
		if strings.Contains(renders[0], marker) {
			t.Errorf("SUMMARY.md carries what looks like a timestamp (%q)", marker)
		}
	}
}

func TestEveryClassGetsAFileEvenWhenEmpty(t *testing.T) {
	files := map[string]string{
		"works/pl/plain/work.json":            workJSON(t, "plain", "Plain"),
		"works/pl/plain/recordings/only.json": recJSON(t, "only", "plain"),
		"people/ja/jane-doe.json":             personJSON(t, "jane-doe", "Jane Doe"),
		"people/na/nate-narrator.json":        personJSON(t, "nate-narrator", "Nate Narrator"),
	}
	data := filepath.Join(t.TempDir(), "data")
	testpack.Seed(t, data, files)
	out := filepath.Join(t.TempDir(), "out")
	if _, err := Run(Options{DataDir: data, OutDir: out}); err != nil {
		t.Fatal(err)
	}
	for _, class := range classOrder {
		if _, err := os.Stat(filepath.Join(out, classFile(class))); err != nil {
			t.Errorf("%s: %v", classFile(class), err)
		}
	}
	if _, err := os.Stat(filepath.Join(out, "SUMMARY.md")); err != nil {
		t.Errorf("SUMMARY.md: %v", err)
	}
}

func TestRunLeavesTheDataTreeUntouched(t *testing.T) {
	files := map[string]string{
		"works/pl/plain/work.json":            workJSON(t, "plain", "Plain (Unabridged)"),
		"works/pl/plain/recordings/only.json": recJSON(t, "only", "plain"),
		"people/ja/jane-doe.json":             personJSON(t, "jane-doe", "Jane Doe"),
		"people/na/nate-narrator.json":        personJSON(t, "nate-narrator", "Nate Narrator"),
	}
	data := filepath.Join(t.TempDir(), "data")
	testpack.Seed(t, data, files)
	before := treeSnapshot(t, data)

	out := filepath.Join(t.TempDir(), "out")
	if _, err := Run(Options{DataDir: data, OutDir: out}); err != nil {
		t.Fatal(err)
	}
	if after := treeSnapshot(t, data); after != before {
		t.Errorf("the audit modified data/:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// treeSnapshot renders every file under root with its bytes, so any write at all
// shows up as a difference.
func treeSnapshot(t testing.TB, root string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		rel, _ := filepath.Rel(root, path)
		b.WriteString(rel + "\n" + string(data) + "\n")
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot %s: %v", root, err)
	}
	return b.String()
}

// The read-only claim is not only "the logic has no write path": pointing -o at the
// data root would land the report IN the tree, where metafmt and metacheck would
// then judge files that are not records.
func TestRunRefusesAnOutputDirectoryInsideTheDataRoot(t *testing.T) {
	data := filepath.Join(t.TempDir(), "data")
	testpack.Seed(t, data, map[string]string{
		"works/pl/plain/work.json": workJSON(t, "plain", "Plain"),
		"people/ja/jane-doe.json":  personJSON(t, "jane-doe", "Jane Doe"),
	})
	for _, out := range []string{data, filepath.Join(data, "report"), filepath.Join(data, "works", "r")} {
		if _, err := Run(Options{DataDir: data, OutDir: out}); err == nil {
			t.Errorf("-o %s was accepted; it is inside the data root", out)
		} else if !strings.Contains(err.Error(), "inside the data root") {
			t.Errorf("-o %s failed with the wrong error: %v", out, err)
		}
	}
	// A sibling directory is fine.
	if _, err := Run(Options{DataDir: data, OutDir: filepath.Join(t.TempDir(), "out")}); err != nil {
		t.Errorf("a sibling output directory was refused: %v", err)
	}
	// And nothing was written into the tree by the refused runs.
	for _, name := range []string{"report", "SUMMARY.md", classFile(ClassWorkDup)} {
		if _, err := os.Stat(filepath.Join(data, name)); !os.IsNotExist(err) {
			t.Errorf("a refused run created data/%s (stat err = %v)", name, err)
		}
	}
}

func TestRunRequiresAnOutputDirectory(t *testing.T) {
	data := filepath.Join(t.TempDir(), "data")
	testpack.Seed(t, data, map[string]string{
		"works/pl/plain/work.json": workJSON(t, "plain", "Plain"),
		"people/ja/jane-doe.json":  personJSON(t, "jane-doe", "Jane Doe"),
	})
	if _, err := Run(Options{DataDir: data}); err == nil {
		t.Error("a run with no output directory should fail")
	}
}

func TestRunReportsAnUnloadableTree(t *testing.T) {
	if _, err := Run(Options{DataDir: filepath.Join(t.TempDir(), "nope"), OutDir: t.TempDir()}); err == nil {
		t.Error("a run over a missing data root should fail")
	}
}

func TestLoaderAdvisoriesAreCarriedThrough(t *testing.T) {
	// An uncredited person is one of pkg/check's own advisories.
	rep := runFixture(t, map[string]string{
		"works/pl/plain/work.json":            workJSON(t, "plain", "Plain"),
		"works/pl/plain/recordings/only.json": recJSON(t, "only", "plain"),
		"people/ja/jane-doe.json":             personJSON(t, "jane-doe", "Jane Doe"),
		"people/na/nate-narrator.json":        personJSON(t, "nate-narrator", "Nate Narrator"),
		"people/or/orphan-person.json":        personJSON(t, "orphan-person", "Orphan Person"),
	})
	// The subclass is the LOADER's own advisory class name, not one "warning"
	// bucket: pkg/check already distinguishes five classes and flattening them
	// would make this class's counts useless for triage.
	got := subclassOf(t, rep, ClassLoader, check.AdvisoryOrphanPerson)
	if len(got) == 0 {
		t.Fatalf("pkg/check's orphan-person advisory was not carried into the report: %+v",
			classOf(t, rep, ClassLoader))
	}
	if !got[0].Propose.Advisory {
		t.Error("a carried-through advisory must be marked advisory")
	}
	if rep.LoaderWarnings != len(got) {
		t.Errorf("LoaderWarnings = %d, records = %d", rep.LoaderWarnings, len(got))
	}
	if rep.LoaderProblems != 0 {
		t.Errorf("LoaderProblems = %d, want 0", rep.LoaderProblems)
	}
}

func TestSummaryCountsMatchTheRecordCounts(t *testing.T) {
	rep := runFixture(t, map[string]string{
		"works/ha/hammered/work.json":                    workJSON(t, "hammered", "Hammered"),
		"works/ha/hammered/recordings/luke.json":         recJSON(t, "luke", "hammered"),
		"works/ha/hammered-book-3/work.json":             workJSON(t, "hammered-book-3", "Hammered: The Druid Tales, Book 3"),
		"works/ha/hammered-book-3/recordings/chris.json": recJSON(t, "chris", "hammered-book-3"),
		"people/ja/jane-doe.json":                        personJSON(t, "jane-doe", "Jane Doe"),
		"people/na/nate-narrator.json":                   personJSON(t, "nate-narrator", "Nate Narrator"),
		"series/dr/druid-tales.json":                     seriesJSON(t, "druid-tales", "The Druid Tales", "hammered@3"),
	})
	md := summary(rep)
	for _, class := range classOrder {
		c := rep.class(class)
		if !strings.Contains(md, "| "+class+" | "+reportdir.Comma(c.total())+" |") {
			t.Errorf("SUMMARY.md is missing the count row for %s (%d)", class, c.total())
		}
	}
	if !strings.Contains(md, "| works | 2 |") {
		t.Error("SUMMARY.md is missing the catalogue totals")
	}
}

func TestReportWriteCoversEveryClass(t *testing.T) {
	rep := runFixture(t, map[string]string{
		"works/pl/plain/work.json":            workJSON(t, "plain", "Plain"),
		"works/pl/plain/recordings/only.json": recJSON(t, "only", "plain"),
		"people/ja/jane-doe.json":             personJSON(t, "jane-doe", "Jane Doe"),
		"people/na/nate-narrator.json":        personJSON(t, "nate-narrator", "Nate Narrator"),
	})
	var b strings.Builder
	if err := rep.Write(&b); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(b.String(), "\n"), "\n")
	// One header line plus one per class.
	if len(lines) != len(classOrder)+1 {
		t.Fatalf("Write produced %d lines for %d classes", len(lines), len(classOrder))
	}
	if !strings.Contains(lines[0], "audited 1 works") {
		t.Errorf("header = %q", lines[0])
	}
	for i, class := range classOrder {
		if !strings.Contains(lines[i+1], class) {
			t.Errorf("line %d = %q, want it to name %s", i+1, lines[i+1], class)
		}
	}
}

func TestClassDocCoversEveryClass(t *testing.T) {
	for _, class := range classOrder {
		if strings.TrimSpace(classDoc[class]) == "" {
			t.Errorf("class %s has no one-line description in classDoc", class)
		}
	}
	if len(classDoc) != len(classOrder) {
		t.Errorf("classDoc has %d entries for %d classes - a stale one, or a missing one", len(classDoc), len(classOrder))
	}
}
