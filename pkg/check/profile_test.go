package check

import (
	"strings"
	"testing"

	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// profile_test.go covers the TREE PROFILE (pack.Profile) end to end through the
// loader: what a subset root validates, what it stops validating, and what it
// now refuses. The passing/violating pair for each rule, as every metacheck rule
// carries.
//
// The three facts under test, in the order LoadProfile's doc states them:
//
//   - a root holding only its profile's families loads clean;
//   - a file under a family the profile does not name is an unrecognized
//     location, which is what makes a family left behind by the split a red
//     check rather than a directory nobody reads;
//   - a CROSS-FAMILY rule whose other side is not in the tree is skipped, not
//     failed - the one that matters being "this sidecar's parent work exists".

// communityOnly is a works-community root standing on its own: one sidecar
// entry, keyed by a work slug NO tree here holds. Under ProfileCommunity that is
// the normal case (the works live in the other repository); under ProfileAll it
// is a dangling reference.
func communityOnly() map[string]string {
	return map[string]string{
		"works-community/0/0.json": packOf(map[string]string{
			"book-one": `{"characters":` + validCharacters("book-one") + `,"recaps":` + validRecaps("book-one") + `}`,
		}),
	}
}

// coreOnly is the CC0 half on its own: works, people and series, plus the
// tombstone table's family roots but no works-community.
func coreOnly() map[string]string {
	files := packValid()
	delete(files, "works-community/0/0.json")
	return files
}

// redirectsOf renders a tombstone table holding the given works entries. All
// three namespaces are required by the schema, so the two that are empty are
// spelled out rather than left off.
func redirectsOf(works string) string {
	return `{"people":{},"series":{},"works":{` + works + `}}`
}

// TestCommunityProfileTreeValidatesStandalone is the PASSING fixture: the
// community half of the split, checked on its own, is green - schema, placement,
// caps and the member rules all still run, and the one thing it cannot answer
// (does the parent work exist) is not asked.
func TestCommunityProfileTreeValidatesStandalone(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, communityOnly())

	res := LoadProfile(dir, pack.ProfileCommunity)
	if !res.OK() {
		t.Fatalf("community-profile tree reported problems:\n%s", joinProblems(res.Problems))
	}
	if len(res.Warnings) != 0 {
		t.Errorf("community-profile tree reported advisories:\n%s", joinProblems(res.Warnings))
	}
	cat := res.Catalog
	if len(cat.Characters) != 1 || len(cat.Recaps) != 1 {
		t.Fatalf("sidecars not loaded: %d characters, %d recaps", len(cat.Characters), len(cat.Recaps))
	}
	if len(cat.Works) != 0 || len(cat.People) != 0 || len(cat.Series) != 0 {
		t.Errorf("community-profile load read a core family: %d works, %d people, %d series",
			len(cat.Works), len(cat.People), len(cat.Series))
	}
}

// TestCommunityProfileSkipIsTheProfilesDoing is its VIOLATING twin, and the
// proof that the clean load above comes from the profile rather than from the
// fixture: the identical tree under the default profile reports the dangling
// parent work, once per sidecar.
func TestCommunityProfileSkipIsTheProfilesDoing(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, communityOnly())

	res := Load(dir)
	if res.OK() {
		t.Fatal("a dangling sidecar parent went unreported under the default profile")
	}
	if !hasProblem(res.Problems, `parent work "book-one" does not exist`) {
		t.Errorf("problems do not name the dangling parent work:\n%s", joinProblems(res.Problems))
	}
}

// TestCommunityProfileStillEnforcesItsOwnRules pins that only the CROSS-FAMILY
// question is skipped: a within-family rule (two recaps at one through-position)
// fails exactly as it does under the default profile.
func TestCommunityProfileStillEnforcesItsOwnRules(t *testing.T) {
	dir := t.TempDir()
	files := communityOnly()
	dupRecaps := strings.Replace(validRecaps("book-one"),
		`"through":{"chapter":3}`, `"through":{"chapter":0}`, 1)
	files["works-community/0/0.json"] = packOf(map[string]string{
		"book-one": `{"recaps":` + dupRecaps + `}`,
	})
	writeTree(t, dir, files)

	res := LoadProfile(dir, pack.ProfileCommunity)
	if !hasProblem(res.Problems, "duplicate recap through chapter 0") {
		t.Errorf("community profile stopped enforcing its own member rules:\n%s", joinProblems(res.Problems))
	}
}

// TestCoreProfileTreeValidatesStandalone is the other half's passing fixture:
// works, people and series alone are green, and the sidecar rules simply have
// nothing to run over.
func TestCoreProfileTreeValidatesStandalone(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, coreOnly())

	res := LoadProfile(dir, pack.ProfileCore)
	if !res.OK() {
		t.Fatalf("core-profile tree reported problems:\n%s", joinProblems(res.Problems))
	}
	if len(res.Warnings) != 0 {
		t.Errorf("core-profile tree reported advisories:\n%s", joinProblems(res.Warnings))
	}
}

// TestCoreProfileRefusesALeftoverCommunityFamily is THE enforcement of the
// boundary: the family that moved out is no family root any more, so every file
// under it is an unrecognized location - and the message names the roots this
// tree does hold, which is the fix.
func TestCoreProfileRefusesALeftoverCommunityFamily(t *testing.T) {
	dir := t.TempDir()
	files := packValid()
	// A second pack under the leftover root, so "every such file" is testable
	// rather than "the first one". Its work is real, so the fixture is valid
	// under the default profile and the only thing the core profile can be
	// reacting to is the family's absence from it.
	files["works/0/0.json"] = packOf(map[string]string{
		"book-one": composite(pkWorkOne, map[string]string{"rec-one": pkRecOne}),
		"zz-book":  pkWorkTitled("zz-book", "Zz Book"),
	})
	files["works-community/0/zz-book.json"] = packOf(map[string]string{
		"zz-book": `{"characters":` + strings.Replace(validCharacters("zz-book"), `"wikidata":"Q42"`, `"wikidata":"Q43"`, 1) + `}`,
	})
	writeTree(t, dir, files)

	// The same tree is clean under the default profile: the leftover is only a
	// leftover relative to a profile that does not hold that family.
	if res := Load(dir); !res.OK() {
		t.Fatalf("fixture is not valid under the default profile:\n%s", joinProblems(res.Problems))
	}

	res := LoadProfile(dir, pack.ProfileCore)
	if res.OK() {
		t.Fatal("core profile accepted a works-community family")
	}
	for _, rel := range []string{"works-community/0/0.json", "works-community/0/zz-book.json"} {
		if !hasProblem(res.Problems, rel+": unrecognized location") {
			t.Errorf("%s not reported as an unrecognized location:\n%s", rel, joinProblems(res.Problems))
		}
	}
	if !hasProblem(res.Problems, "not under any of the people/, series/, works/ roots") {
		t.Errorf("the message does not name the profile's own roots:\n%s", joinProblems(res.Problems))
	}
	// Nothing from the leftover family reached the catalogue - it was accounted
	// for, not read.
	if n := len(res.Catalog.Characters); n != 0 {
		t.Errorf("core profile loaded %d characters from a family it does not hold", n)
	}
}

// TestCommunityProfileRefusesTheRedirectsFile: the tombstone table is core glue -
// it retires core slugs - so under the community profile data/redirects.json is
// recognized by nothing and is reported like any other file that belongs nowhere.
func TestCommunityProfileRefusesTheRedirectsFile(t *testing.T) {
	dir := t.TempDir()
	files := communityOnly()
	files[pack.RedirectsFile] = redirectsOf(`"old-book":"book-one"`)
	writeTree(t, dir, files)

	res := LoadProfile(dir, pack.ProfileCommunity)
	if !hasProblem(res.Problems, pack.RedirectsFile+": unrecognized location") {
		t.Errorf("the tombstone table was accepted under the community profile:\n%s", joinProblems(res.Problems))
	}
	if res.Catalog.Redirects.Len() != 0 {
		t.Errorf("redirects were read under a profile that carries none: %v", res.Catalog.Redirects)
	}
	// And the redirect RULES stayed quiet: nothing was checked against an id set
	// this tree cannot have.
	if hasProblem(res.Problems, "redirect target") || hasProblem(res.Problems, "redirect source") {
		t.Errorf("redirect rules ran under a profile that carries no table:\n%s", joinProblems(res.Problems))
	}
}

// TestCoreProfileKeepsTheRedirectsFile is its counterpart: the core half DOES
// carry the tombstone table, so it is read and its rules run exactly as before.
func TestCoreProfileKeepsTheRedirectsFile(t *testing.T) {
	dir := t.TempDir()
	files := coreOnly()
	files[pack.RedirectsFile] = redirectsOf(`"old-book":"book-one"`)
	writeTree(t, dir, files)

	res := LoadProfile(dir, pack.ProfileCore)
	if !res.OK() {
		t.Fatalf("core-profile tree with a tombstone table reported problems:\n%s", joinProblems(res.Problems))
	}
	if got := res.Catalog.Redirects.Len(); got != 1 {
		t.Fatalf("redirects read = %d, want 1", got)
	}

	// The violating twin, so the rules are demonstrably live and not merely
	// silent: a target no record answers to.
	bad := t.TempDir()
	files[pack.RedirectsFile] = redirectsOf(`"old-book":"no-such-book"`)
	writeTree(t, bad, files)
	if res := LoadProfile(bad, pack.ProfileCore); !hasProblem(res.Problems, "is no live works id") {
		t.Errorf("redirect rules did not run under the core profile:\n%s", joinProblems(res.Problems))
	}
}

// TestLoadRefusesAnUnknownProfile: a profile is read from a flag, so a typo has
// to be an error rather than a silently narrowed tree.
func TestLoadRefusesAnUnknownProfile(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, packValid())
	res := LoadProfile(dir, pack.Profile("kore"))
	if res.OK() {
		t.Fatal("LoadProfile accepted an unknown profile")
	}
	if !hasProblem(res.Problems, `unknown tree profile "kore"`) {
		t.Errorf("problems do not name the bad profile:\n%s", joinProblems(res.Problems))
	}
}

// TestLoadStoreCarriesTheStoresProfile pins that the writers' door in needs no
// profile of its own: it takes the store's, so a validating writer can never
// check a tree under a different profile than the one it is writing under.
func TestLoadStoreCarriesTheStoresProfile(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, communityOnly())

	s, err := pack.OpenProfile(dir, pack.ProfileCommunity)
	if err != nil {
		t.Fatal(err)
	}
	if res := LoadStore(s); !res.OK() {
		t.Fatalf("LoadStore did not inherit the community profile:\n%s", joinProblems(res.Problems))
	}
	// ... and after a Flush has made the listing stale, the fresh load takes the
	// profile off the store rather than defaulting back to the whole tree.
	if _, err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	if s.Listing() != nil {
		t.Fatal("Flush left a listing behind, so the fresh-load path is untested")
	}
	if res := LoadStore(s); !res.OK() {
		t.Errorf("post-flush LoadStore lost the store's profile:\n%s", joinProblems(res.Problems))
	}
}
