package check

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// compose_test.go covers LoadComposed: the load over the TWO roots the
// community-repo split produces. The passing/violating pair for each of its
// cross-tree rules, as every metacheck rule carries - plus the property the
// cutover rests on, that splitting a tree changes nothing about what loads out of
// it while every key is live.

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

// redCore is composeCore with one problem of its OWN: a second work crediting a
// person no record names. Nothing about it touches the sidecars, so it is the
// fixture for "a red side does not suppress the cross-tree pass".
func redCore() map[string]string {
	files := composeCore()
	files["works/0/0.json"] = packOf(map[string]string{
		"book-one": composite(pkWorkOne, map[string]string{"rec-one": pkRecOne}),
		"book-two": strings.Replace(pkWorkTitled("book-two", "Book Two"),
			`"author-one"`, `"ghost-author"`, 1),
	})
	return files
}

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
// they load to as one tree. It holds for a pair whose keys are all LIVE, which is
// the state the cutover produces and the sweep maintains; the tombstone window is
// deliberately more permissive (see TestComposeResolvesRetiredKey). It compares
// what the artifact is built FROM; the artifact-level twin lives in
// internal/build.
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

	// Whole-catalogue equality: records, order, keys and the redirects map alike
	// (the Catalog carries no paths, so the split cannot hide in a field the
	// loops above would have skipped).
	if !reflect.DeepEqual(single.Catalog, composed.Catalog) {
		t.Fatalf("the composed catalogue differs from the single tree's:\nsingle=%+v\ncomposed=%+v",
			single.Catalog, composed.Catalog)
	}
}

// TestComposeSurvivesABrokenCommunityLoad pins an ordering the review round
// fixed: a community load that fails before producing a catalogue also returns
// a NIL path index, so the path-attribution pass must sit behind the
// nothing-to-compose guard - prefixing first was a latent panic. The fixture is
// an unreadable community pack: the result must be red with the load's own
// problem, never a panic.
func TestComposeSurvivesABrokenCommunityLoad(t *testing.T) {
	coreDir, comDir := composeDirs(t, composeCore(), composeCommunity(map[string]string{
		"book-one": bothSidecars("book-one"),
	}))
	var packPath string
	if err := filepath.WalkDir(comDir, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(path, ".json") && packPath == "" {
			packPath = path
		}
		return err
	}); err != nil || packPath == "" {
		t.Fatalf("no community pack found to break: %v", err)
	}
	if err := os.Chmod(packPath, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(packPath, 0o644) })

	res := LoadComposed(coreDir, comDir)
	if res.OK() {
		t.Fatal("an unreadable community pack must fail the composed load")
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
	// The problem locates itself in the community CHECKOUT, down to the member.
	if !hasProblem(res.Problems, "community: works-community/0/0.json: entry book-two: characters") {
		t.Errorf("problem did not name the pack entry it came from: %v", res.Problems)
	}
}

// TestComposeBothDoorsAgreeOnADanglingKey pins that the two spellings of the
// existence rule - checkIntegrity's sidecar arm in a whole-database load, and
// resolveSidecarKeys at compose time - cannot diverge on the VERDICT, however
// differently they word it. One set of records, loaded both ways, red both ways.
func TestComposeBothDoorsAgreeOnADanglingKey(t *testing.T) {
	community := composeCommunity(map[string]string{"book-two": bothSidecars("book-two")})

	whole := t.TempDir()
	files := composeCore()
	for rel, content := range community {
		files[rel] = content
	}
	writeTree(t, whole, files)
	if single := Load(whole); single.OK() {
		t.Error("one tree accepted a sidecar for a work it does not hold")
	}

	coreDir, comDir := composeDirs(t, composeCore(), community)
	if composed := LoadComposed(coreDir, comDir); composed.OK() {
		t.Error("the composed pair accepted a sidecar for a work the core does not hold")
	}
}

// TestComposeResolvesRetiredKey covers the redirect hop: a core merge retires a
// work slug in one repository and the community re-key sweep lands in the other,
// so the build resolves the tombstone rather than losing the sidecar in the
// window between them - and WARNS, so that window is datable from a build log.
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

	// A ridden redirect is never silent, and its advisory has a class - the census
	// count IS the size of the open re-key sweep.
	rides := advisoryMatching(problemLines(res.Warnings), "the community re-key sweep is pending")
	if len(rides) != 2 {
		t.Fatalf("a resolved pair reported %d ride advisories, want 2 (characters + recaps): %v", len(rides), rides)
	}
	for _, w := range res.Warnings {
		if got := AdvisoryClass(w); got == AdvisoryUnclassified {
			t.Errorf("no advisory class claims %q", w)
		}
	}
	if !hasProblem(res.Warnings, `keyed by the retired work slug "old-book", resolved onto "book-one"`) {
		t.Errorf("the ride advisory did not name both slugs: %v", res.Warnings)
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
// kind is refused, never folded. Which of the two describes the surviving work is
// a human decision - internal/repair refuses the same shape for the same reason.
//
// It also pins the REWRITE CONTRACT: a red pass hands the catalogue back with
// its keys AS WRITTEN. The resolution has been computed and reported, but a
// result that will not be built from must not carry keys that are neither what
// the tree says nor what a green build would have produced.
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

	keys := map[string]bool{}
	for _, c := range res.Catalog.Characters {
		keys[c.Work] = true
	}
	if !keys["old-book"] || !keys["book-one"] {
		t.Errorf("a red pass rewrote the sidecar keys: got %v, want both as written", keys)
	}
}

// TestComposeCollisionKeepsTheIncumbent pins WHICH of a colliding pair is named.
// The entry whose key was already the live slug is the keeper, whatever the pack
// order; only the redirect RIDER is surplus. Chosen by path alone, the rider in
// the earlier-sorting pack became the keeper and the message told the operator to
// fold away the entry that was correctly keyed all along.
func TestComposeCollisionKeepsTheIncumbent(t *testing.T) {
	core := composeCore()
	core["redirects.json"] = redirectsOf(`"aa-old-book":"book-one"`)
	// Two packs, so the RIDER sits in the earlier-sorting path and the incumbent
	// in the later one.
	community := map[string]string{
		"works-community/0/0.json":               packOf(map[string]string{"aa-old-book": charactersOnly("aa-old-book")}),
		"works-community/book-one/book-one.json": packOf(map[string]string{"book-one": charactersOnly("book-one")}),
	}
	coreDir, comDir := composeDirs(t, core, community)

	res := LoadComposed(coreDir, comDir)
	if res.OK() {
		t.Fatal("two characters sidecars resolving onto one work loaded clean")
	}
	if !hasProblem(res.Problems, `characters sidecar keyed by work "aa-old-book" resolves onto "book-one", `+
		`which already has one (community: works-community/book-one/book-one.json: entry book-one: characters)`) {
		t.Errorf("the rider was not the one reported against the incumbent: %v", res.Problems)
	}
	if hasProblem(res.Problems, `sidecar keyed by work "book-one" resolves onto`) {
		t.Errorf("the incumbent was reported as the surplus entry: %v", res.Problems)
	}
}

// TestComposeRunsTheCrossTreeRulesOnARedSide pins that a problem in either root
// does NOT suppress the cross-tree pass. A whole-database load has never let one
// malformed pack silence checkIntegrity's dangling-sidecar reports, and the
// composed load reports everything one run can see for the same reason.
func TestComposeRunsTheCrossTreeRulesOnARedSide(t *testing.T) {
	coreDir, comDir := composeDirs(t, redCore(), composeCommunity(map[string]string{
		"book-nine": charactersOnly("book-nine"),
	}))

	res := LoadComposed(coreDir, comDir)
	if res.OK() {
		t.Fatal("a red core loaded clean")
	}
	if !hasProblem(res.Problems, `author "ghost-author" does not exist as a person`) {
		t.Errorf("the core's own problem is missing: %v", res.Problems)
	}
	if !hasProblem(res.Problems, `characters sidecar is keyed by work "book-nine"`) {
		t.Errorf("the cross-tree rule was suppressed by the red core: %v", res.Problems)
	}
}

// TestComposeNamesADeadRedirectTarget covers the arm of the existence message a
// green core cannot reach: a key that IS retired, onto a target that is not a
// live work either. checkRedirects has gone red about that table, and because the
// cross-tree pass runs red or not, the sidecar's own report says what it found
// rather than pretending the slug was never retired.
func TestComposeNamesADeadRedirectTarget(t *testing.T) {
	core := composeCore()
	core["redirects.json"] = redirectsOf(`"old-book":"ghost-book"`)
	coreDir, comDir := composeDirs(t, core, composeCommunity(map[string]string{
		"old-book": charactersOnly("old-book"),
	}))

	res := LoadComposed(coreDir, comDir)
	if res.OK() {
		t.Fatal("a redirect onto a work that does not exist loaded clean")
	}
	if !hasProblem(res.Problems, `characters sidecar is keyed by work "old-book", which the core tree does not hold `+
		`(the slug redirects to "ghost-book", which is not there either)`) {
		t.Errorf("the dead-target arm did not report itself: %v", res.Problems)
	}
}

// TestComposeRefusesAnEmptyCommunityRoot: --community says "there is a community
// layer here". A directory that holds none of it composes clean and ships an
// artifact with the whole CC BY-SA layer dropped - the quiet omission the
// existence rule exists to prevent, reachable by pointing the flag at the
// community repository's root instead of its data/ subdirectory. pack.ListProfile
// tolerates a missing family root by design, so the refusal belongs here.
func TestComposeRefusesAnEmptyCommunityRoot(t *testing.T) {
	coreDir, comDir := composeDirs(t, composeCore(), map[string]string{})

	res := LoadComposed(coreDir, comDir)
	if res.OK() {
		t.Fatal("a community root holding no sidecars composed clean")
	}
	if !hasProblem(res.Problems, "holds no loadable works-community entries") {
		t.Errorf("problems did not name the empty community root: %v", res.Problems)
	}
	if !hasProblem(res.Problems, "omit the flag to build the core alone") {
		t.Errorf("problem did not name the fix: %v", res.Problems)
	}
	if !hasProblem(res.Problems, comDir+":") {
		t.Errorf("problem did not name the directory: %v", res.Problems)
	}
}

// TestComposeAttributesProblemsToTheirRoot: the two roots' paths are both
// root-relative and two of them are spellable by either root, so a composed list
// says which checkout every line belongs to. Composed mode ONLY - a single-root
// load's paths are untouched.
func TestComposeAttributesProblemsToTheirRoot(t *testing.T) {
	community := composeCommunity(map[string]string{"book-nine": charactersOnly("book-nine")})
	coreDir, comDir := composeDirs(t, redCore(), community)

	res := LoadComposed(coreDir, comDir)
	for _, p := range res.Problems {
		com := strings.HasPrefix(p.Path, "community: ")
		if strings.Contains(p.Path, "works-community/") != com {
			t.Errorf("problem %q is attributed to the wrong root", p)
		}
	}

	// The same community tree loaded on its own is unmarked, byte for byte.
	alone := LoadProfile(comDir, pack.ProfileCommunity)
	for _, p := range alone.Problems {
		if strings.HasPrefix(p.Path, "community: ") {
			t.Errorf("a single-root load marked %q", p)
		}
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

// TestSidecarRefsCoverEverySidecarKind is the DRIFT GUARD on the one hand-written
// enumeration of the works-community member kinds (check.go's sidecarRefs, beside
// the pathIndex maps the same list is declared in). A sidecar kind is derived here
// from the Catalog TYPE - a slice of records carrying a `Work` slug - so a third
// member added to the model and missed in that list fails this test instead of
// silently escaping the compose's existence rule.
func TestSidecarRefsCoverEverySidecarKind(t *testing.T) {
	cat := &model.Catalog{}
	v := reflect.ValueOf(cat).Elem()
	var kinds []string
	for i := range v.NumField() {
		f := v.Type().Field(i)
		if f.Type.Kind() != reflect.Slice {
			continue
		}
		el := f.Type.Elem()
		if el.Kind() != reflect.Pointer || el.Elem().Kind() != reflect.Struct {
			continue
		}
		if wf, ok := el.Elem().FieldByName("Work"); !ok || wf.Type.Kind() != reflect.String {
			continue
		}
		kinds = append(kinds, f.Name)
		rec := reflect.New(el.Elem())
		rec.Elem().FieldByName("Work").SetString("book-one")
		v.Field(i).Set(reflect.Append(v.Field(i), rec))
	}

	if len(kinds) < 2 {
		t.Fatalf("the derivation found %v, which cannot be the sidecar kinds - it has drifted from the model", kinds)
	}
	// Compare the KIND NAMES, not just the counts: a third kind wired into
	// sidecarRefs under the wrong kind string would pass a length check while
	// every message it composes names the wrong member.
	var got []string
	for _, ref := range sidecarRefs(cat, newPathIndex()) {
		got = append(got, ref.kind)
	}
	slices.Sort(got)
	want := make([]string, len(kinds))
	for i, k := range kinds {
		want[i] = strings.ToLower(k)
	}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("sidecarRefs enumerated %v; the model's sidecar kinds are %v: "+
			"a kind added to the model must be added to sidecarRefs too, under its own name", got, want)
	}
}
