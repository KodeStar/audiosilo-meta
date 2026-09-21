package recorddiff

import (
	"fmt"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/internal/testpack"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// bigTranche builds a head tree holding n works, one person and one removal, the
// shape a bulk import has: far more added records than any budget can hold.
func bigTranche(t *testing.T, n int) *Diff {
	t.Helper()
	baseDir, headDir := trees(t)

	base := map[string]string{"gone-work": testpack.WorkJSON(t, "gone-work", "Gone")}
	head := map[string]string{}
	for i := range n {
		slug := fmt.Sprintf("work-%03d", i)
		head[slug] = testpack.WorkJSON(t, slug, fmt.Sprintf("Work Number %03d", i))
	}
	writePack(t, baseDir, "works/0/0.json", base)
	writePack(t, headDir, "works/0/0.json", head)
	writePack(t, baseDir, "people/0.json", map[string]string{
		"jane-doe": testpack.PersonJSON(t, "jane-doe", "Jane Doe"),
	})
	writePack(t, headDir, "people/0.json", map[string]string{
		"jane-doe":      testpack.PersonJSON(t, "jane-doe", "Jane Doe"),
		"nate-narrator": testpack.PersonJSON(t, "nate-narrator", "Nate Narrator"),
	})
	return diffTrees(t, baseDir, headDir)
}

func TestTextFitsTheBudgetWithoutCuttingALine(t *testing.T) {
	d := bigTranche(t, 400)
	const budget = 8000

	got := d.Text(budget)
	if len(got) > budget {
		t.Fatalf("render is %d bytes, over the %d-byte budget", len(got), budget)
	}
	if !strings.HasSuffix(got, "\n") {
		t.Errorf("the render does not end on a line boundary")
	}
	// Every line the budget kept is a WHOLE line: no added entry is half printed.
	for _, line := range strings.Split(strings.TrimRight(got, "\n"), "\n") {
		if strings.HasPrefix(line, "+ work ") && !strings.Contains(line, "recordings: ") {
			t.Fatalf("an added-work line was cut mid-line: %q", line)
		}
	}
}

func TestTruncationKeepsEveryHeaderAndCount(t *testing.T) {
	d := bigTranche(t, 400)
	got := d.Text(4000)

	for _, want := range []string{
		"ENTRY-LEVEL SUMMARY",
		"works: 400 added, 1 removed, 0 modified, 0 moved-only",
		"people: 1 added, 0 removed, 0 modified, 0 moved-only",
		"redirects: 0 added, 0 removed",
		"ADDED (401)",
		"REMOVED (1)",
		// The small sections are served their budget FIRST, so a tranche of 400
		// additions can never push the one removal out of the render.
		`- work gone-work: "Gone"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the truncated render is missing %q:\n%s", want, got)
		}
	}
}

func TestTruncationSaysHowMuchItLeftOut(t *testing.T) {
	d := bigTranche(t, 400)
	got := d.Text(8000)

	var omission string
	for _, line := range strings.Split(got, "\n") {
		if strings.HasPrefix(line, "... ") && strings.Contains(line, "more added") {
			omission = line
		}
	}
	if omission == "" {
		t.Fatalf("a truncated render carries no omission line for the added entries:\n%s", got)
	}
	if !strings.Contains(omission, "omitted") || !strings.Contains(omission, "larger than this summary's budget") {
		t.Errorf("omission line = %q", omission)
	}

	shown := strings.Count(got, "\n+ work ")
	if shown == 0 {
		t.Fatalf("nothing was shown at all:\n%s", got)
	}
	// The count in the omission line plus the lines actually printed must be the
	// whole tranche, or the reader cannot tell how much was dropped.
	var dropped int
	if _, err := fmt.Sscanf(omission, "... %d more added", &dropped); err != nil {
		t.Fatalf("omission line %q is not parseable: %v", omission, err)
	}
	if shown+dropped != 401 {
		t.Errorf("shown %d + dropped %d != 401 added entries", shown, dropped)
	}
}

func TestAnUnlimitedRenderPrintsEverything(t *testing.T) {
	d := bigTranche(t, 50)
	got := d.Text(0)
	if strings.Contains(got, "omitted") {
		t.Errorf("an unlimited render truncated:\n%s", got)
	}
	if n := strings.Count(got, "\n+ work "); n != 50 {
		t.Errorf("printed %d added works, want 50", n)
	}
}

func TestABudgetSmallerThanTheHeaderStillYieldsTheCounts(t *testing.T) {
	d := bigTranche(t, 400)
	got := d.Text(10)
	if !strings.Contains(got, "works: 400 added") {
		t.Fatalf("a tiny budget dropped the counts:\n%s", got)
	}
	if strings.Contains(got, "+ work ") {
		t.Errorf("a tiny budget printed entries:\n%s", got)
	}
}

// TestASmallTrancheIsPrintedWhole is the regression for the budget's ORDINARY
// case, which every other test here walks past: allocate caps a section at its
// own size, so a tranche that fits is handed a budget exactly equal to what
// printing it costs. Demanding the omission line's reserve on top of that made
// the render drop the tail of every small section - and, where the blocks are
// short (a person line is a few dozen bytes), the whole of one - while printing
// an omission line claiming the tranche was over budget. That is the one thing
// this renderer may never do: say something was left out when nothing was.
func TestASmallTrancheIsPrintedWhole(t *testing.T) {
	baseDir, headDir := trees(t)
	writePack(t, baseDir, "people/0.json", map[string]string{
		"jane-doe": testpack.PersonJSON(t, "jane-doe", "Jane Doe"),
	})
	writePack(t, headDir, "people/0.json", map[string]string{
		"jane-doe":      testpack.PersonJSON(t, "jane-doe", "Jane Doe"),
		"nate-narrator": testpack.PersonJSON(t, "nate-narrator", "Nate Narrator"),
		"olly-other":    testpack.PersonJSON(t, "olly-other", "Olly Other"),
		"pat-person":    testpack.PersonJSON(t, "pat-person", "Pat Person"),
	})

	d := diffTrees(t, baseDir, headDir)
	got := d.Text(DefaultMaxBytes)

	if strings.Contains(got, "omitted") {
		t.Fatalf("a %d-byte render under a %d-byte budget claims entries were omitted:\n%s",
			len(got), DefaultMaxBytes, got)
	}
	for _, slug := range []string{"nate-narrator", "olly-other", "pat-person"} {
		if !strings.Contains(got, "+ person "+slug) {
			t.Errorf("the render is missing the added person %q:\n%s", slug, got)
		}
	}
}

func TestRenderIsDeterministic(t *testing.T) {
	baseDir, headDir := trees(t)
	testpack.Seed(t, baseDir, map[string]string{
		"works/th/the-thing/work.json": testpack.WorkJSON(t, "the-thing", "The Thing"),
	})
	testpack.Seed(t, headDir, map[string]string{
		"works/th/the-thing/work.json": testpack.WorkJSON(t, "the-thing", "The Thing Renamed"),
		"works/zz/zulu/work.json":      testpack.WorkJSON(t, "zulu", "Zulu"),
		"works/al/alpha/work.json":     testpack.WorkJSON(t, "alpha", "Alpha"),
		"people/ja/jane-doe.json":      testpack.PersonJSON(t, "jane-doe", "Jane Doe"),
	})

	first := diffTrees(t, baseDir, headDir).Text(0)
	for range 5 {
		if got := diffTrees(t, baseDir, headDir).Text(0); got != first {
			t.Fatalf("the render is not deterministic:\n--- first ---\n%s\n--- again ---\n%s", first, got)
		}
	}
	// Added entries are sorted within their family.
	alpha := strings.Index(first, "+ work alpha")
	zulu := strings.Index(first, "+ work zulu")
	if alpha < 0 || zulu < 0 || alpha > zulu {
		t.Errorf("added works are not slug-sorted:\n%s", first)
	}
	// And the family with a rank of its own leads.
	if c := diffTrees(t, baseDir, headDir).Counts[pack.FamilyPeople]; c.Added != 1 {
		t.Errorf("people counts = %+v", c)
	}
}
