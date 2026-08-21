package check

import (
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// compose_test.go covers LoadComposed: the load over the TWO roots the
// community-repo split produces. The passing/violating pair for each of its four
// cross-tree rules, as every metacheck rule carries - plus the property the
// cutover rests on, that splitting a tree changes nothing about what loads out
// of it.

// composeCore is the CC0 half of packValid: works, people, series, no sidecars.
func composeCore() map[string]string {
	files := packValid()
	delete(files, "works-community/0/0.json")
	return files
}

// composeCommunity is the CC BY-SA half of packValid: the sidecars alone, keyed
// by the core tree's work.
func composeCommunity(entries map[string]string) map[string]string {
	return map[string]string{"works-community/0/0.json": packOf(entries)}
}

// bothSidecars is packValid's works-community entry: one work carrying both
// members.
func bothSidecars(work string) string {
	return `{"characters":` + validCharacters(work) + `,"recaps":` + validRecaps(work) + `}`
}

func charactersOnly(work string) string { return `{"characters":` + validCharacters(work) + `}` }
func recapsOnly(work string) string     { return `{"recaps":` + validRecaps(work) + `}` }

// problemLines renders a problem list for advisoryMatching, which compares the
// one-line form.
func problemLines(ps []Problem) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.String())
	}
	return out
}

// composeDirs writes the two roots and returns their paths.
func composeDirs(t *testing.T, core, community map[string]string) (string, string) {
	t.Helper()
	coreDir, comDir := t.TempDir(), t.TempDir()
	writeTree(t, coreDir, core)
	writeTree(t, comDir, community)
	return coreDir, comDir
}

// TestComposeValidPair is the PASSING fixture: the two halves of a split tree
// load as one catalogue, with the sidecars attached to the core's works.
func TestComposeValidPair(t *testing.T) {
	coreDir, comDir := composeDirs(t, composeCore(), composeCommunity(map[string]string{
		"book-one": bothSidecars("book-one"),
	}))

	res := LoadComposed(coreDir, comDir)
	if !res.OK() {
		t.Fatalf("a valid pair reported problems: %v", res.Problems)
	}
	if len(res.Catalog.Works) != 1 || len(res.Catalog.People) != 2 || len(res.Catalog.Series) != 1 {
		t.Errorf("unexpected core counts: %+v", res.Catalog)
	}
	if len(res.Catalog.Characters) != 1 || len(res.Catalog.Recaps) != 1 {
		t.Fatalf("sidecars did not compose in: characters=%d recaps=%d",
			len(res.Catalog.Characters), len(res.Catalog.Recaps))
	}
	if got := res.Catalog.Characters[0].Work; got != "book-one" {
		t.Errorf("characters keyed %q, want book-one", got)
	}
}

// TestComposeEqualsSingleTree is the EQUIVALENCE property the repository split
// rests on: the same records, split across two roots, load to the same catalogue
// they load to as one tree. It compares what the artifact is built FROM; the
// artifact-level twin lives in internal/build.
func TestComposeEqualsSingleTree(t *testing.T) {
	whole := t.TempDir()
	writeTree(t, whole, packValid())
	single := Load(whole)
	if !single.OK() {
		t.Fatalf("the whole tree reported problems: %v", single.Problems)
	}

	coreDir, comDir := composeDirs(t, composeCore(), composeCommunity(map[string]string{
		"book-one": bothSidecars("book-one"),
	}))
	composed := LoadComposed(coreDir, comDir)
	if !composed.OK() {
		t.Fatalf("the split pair reported problems: %v", composed.Problems)
	}

	a, b := single.Catalog, composed.Catalog
	if len(a.Works) != len(b.Works) || len(a.People) != len(b.People) || len(a.Series) != len(b.Series) ||
		len(a.Characters) != len(b.Characters) || len(a.Recaps) != len(b.Recaps) {
		t.Fatalf("catalogue counts differ: single=%+v composed=%+v", a, b)
	}
	for i := range a.Characters {
		if a.Characters[i].Work != b.Characters[i].Work {
			t.Errorf("characters[%d] keyed %q as one tree, %q composed", i, a.Characters[i].Work, b.Characters[i].Work)
		}
	}
	for i := range a.Recaps {
		if a.Recaps[i].Work != b.Recaps[i].Work {
			t.Errorf("recaps[%d] keyed %q as one tree, %q composed", i, a.Recaps[i].Work, b.Recaps[i].Work)
		}
	}
}

// TestComposeDanglingSidecarKey is the VIOLATING fixture for the existence rule:
// a sidecar keyed by a work the core tree does not hold is a red release, not a
// silently dropped contribution. It is exactly the question a community root
// standing alone declines to answer (see TestCommunityProfileTreeSkipsParentWork
// in profile_test.go), asked where it can be.
func TestComposeDanglingSidecarKey(t *testing.T) {
	coreDir, comDir := composeDirs(t, composeCore(), composeCommunity(map[string]string{
		"book-two": bothSidecars("book-two"),
	}))

	res := LoadComposed(coreDir, comDir)
	if res.OK() {
		t.Fatal("a sidecar keyed by an unknown work loaded clean")
	}
	if !hasProblem(res.Problems, `characters sidecar is keyed by work "book-two", which the core tree does not hold`) {
		t.Errorf("problems did not name the dangling characters key: %v", res.Problems)
	}
	if !hasProblem(res.Problems, `recaps sidecar is keyed by work "book-two"`) {
		t.Errorf("problems did not name the dangling recaps key: %v", res.Problems)
	}
	// The problem locates itself in the community tree, down to the member.
	if !hasProblem(res.Problems, "works-community/0/0.json: entry book-two: characters") {
		t.Errorf("problem did not name the pack entry it came from: %v", res.Problems)
	}
}

// TestComposeResolvesRetiredKey covers the redirect hop: a core merge retires a
// work slug in one repository and the community re-key sweep lands in the other,
// so the build resolves the tombstone rather than losing the sidecar in the
// window between them.
func TestComposeResolvesRetiredKey(t *testing.T) {
	core := composeCore()
	core["redirects.json"] = redirectsOf(`"old-book":"book-one"`)
	coreDir, comDir := composeDirs(t, core, composeCommunity(map[string]string{
		"old-book": bothSidecars("old-book"),
	}))

	res := LoadComposed(coreDir, comDir)
	if !res.OK() {
		t.Fatalf("a retired sidecar key did not resolve: %v", res.Problems)
	}
	if got := res.Catalog.Characters[0].Work; got != "book-one" {
		t.Errorf("characters still keyed %q, want the surviving slug book-one", got)
	}
	if got := res.Catalog.Recaps[0].Work; got != "book-one" {
		t.Errorf("recaps still keyed %q, want the surviving slug book-one", got)
	}
}

// TestComposeResolvedKeyMergesDisjointMembers pins the other half of the
// resolution: a characters sidecar at the retired slug and a recaps sidecar at
// the survivor simply meet on one work, which is what a composed entry is. Only
// a member both sides carry is a refusal.
func TestComposeResolvedKeyMergesDisjointMembers(t *testing.T) {
	core := composeCore()
	core["redirects.json"] = redirectsOf(`"old-book":"book-one"`)
	coreDir, comDir := composeDirs(t, core, composeCommunity(map[string]string{
		"book-one": recapsOnly("book-one"),
		"old-book": charactersOnly("old-book"),
	}))

	res := LoadComposed(coreDir, comDir)
	if !res.OK() {
		t.Fatalf("disjoint members across a redirect were refused: %v", res.Problems)
	}
	if got := res.Catalog.Characters[0].Work; got != "book-one" {
		t.Errorf("characters keyed %q, want book-one", got)
	}
	if got := res.Catalog.Recaps[0].Work; got != "book-one" {
		t.Errorf("recaps keyed %q, want book-one", got)
	}
}

// TestComposeResolvedKeyCollision is the VIOLATING fixture for the collision
// rule: a retired key resolving onto a work that already carries the same member
// kind is refused, never folded. Which of the two describes the surviving work
// is a human decision - internal/repair refuses the same shape for the same
// reason.
func TestComposeResolvedKeyCollision(t *testing.T) {
	core := composeCore()
	core["redirects.json"] = redirectsOf(`"old-book":"book-one"`)
	coreDir, comDir := composeDirs(t, core, composeCommunity(map[string]string{
		"book-one": charactersOnly("book-one"),
		"old-book": charactersOnly("old-book"),
	}))

	res := LoadComposed(coreDir, comDir)
	if res.OK() {
		t.Fatal("two characters sidecars resolving onto one work loaded clean")
	}
	if !hasProblem(res.Problems, `characters sidecar keyed by work "old-book" resolves onto "book-one", which already has one`) {
		t.Errorf("problems did not name the collision: %v", res.Problems)
	}
	if !hasProblem(res.Problems, "which of the two describes the surviving work is a human decision") {
		t.Errorf("problem did not say why it is not folded: %v", res.Problems)
	}
}

// TestComposeRunsThePositionScaleAdvisory pins the cross-tree ADVISORY: the rule
// measures a sidecar's positions against its work's chapter list, which is
// vacuous over a community root alone and real here. It stays a warning - a
// release must not turn on it.
func TestComposeRunsThePositionScaleAdvisory(t *testing.T) {
	core := composeCore()
	core["works/0/0.json"] = packOf(map[string]string{
		"book-one": composite(pkWorkOne, map[string]string{
			"rec-one": strings.TrimSuffix(pkRec("rec-one", "book-one", "en"), "}") +
				`,"chapters":` + chapterList(80) + `}`,
		}),
	})
	chars, recaps := sidecarSpread(3, 7, 11)
	coreDir, comDir := composeDirs(t, core, composeCommunity(map[string]string{
		"book-one": `{"characters":` + chars + `,"recaps":` + recaps + `}`,
	}))

	res := LoadComposed(coreDir, comDir)
	if !res.OK() {
		t.Fatalf("the scale fixture reported problems: %v", res.Problems)
	}
	if got := advisoryMatching(problemLines(res.Warnings), scaleMarker); len(got) != 2 {
		t.Errorf("a sidecar stopping at chapter 11 of 80 reported %d advisories, want 2: %v", len(got), got)
	}

	// The same community root alone says nothing: it cannot see the chapters.
	alone := LoadProfile(comDir, pack.ProfileCommunity)
	if got := advisoryMatching(problemLines(alone.Warnings), scaleMarker); len(got) != 0 {
		t.Errorf("the community root alone reported %v", got)
	}
}

// TestComposeRefusesAMisdirectedRoot covers the two operator errors --community
// makes possible, both answered by the profile machinery's own accounting rather
// than by a message of this file's own: a root that is not there, and the two
// checkouts handed over the wrong way round.
func TestComposeRefusesAMisdirectedRoot(t *testing.T) {
	coreDir, comDir := composeDirs(t, composeCore(), composeCommunity(map[string]string{
		"book-one": bothSidecars("book-one"),
	}))

	if res := LoadComposed(coreDir, coreDir+"/does-not-exist"); res.OK() {
		t.Error("a community root that is not there loaded clean")
	}

	// Swapped: every file of each root belongs to a family its profile does not
	// name, so each one is an unrecognized location.
	res := LoadComposed(comDir, coreDir)
	if res.OK() {
		t.Fatal("the two roots swapped loaded clean")
	}
	if !hasProblem(res.Problems, "unrecognized location") {
		t.Errorf("problems did not report the misplaced families: %v", res.Problems)
	}
}
