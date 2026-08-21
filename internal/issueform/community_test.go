package issueform

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kodestar/audiosilo-meta/internal/build"
	"github.com/kodestar/audiosilo-meta/internal/testpack"
	"github.com/kodestar/audiosilo-meta/pkg/check"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
)

// community_test.go covers intake against the COMMUNITY repository's tree: one
// family (works-community), no works to check a sidecar's key against, and a
// built meta.sqlite release artifact standing in for the core catalogue.
//
// The two halves it pins are the two things that break on such a root: the store
// and the post-write validation have to run under the profile, and the
// WORK-EXISTENCE question has to be answered from the artifact - never skipped,
// because a sidecar keyed by a work nothing holds is what the release build
// stops on.

// communityTree is a root holding the community family alone. It starts empty:
// pack.ListProfile tolerates an absent family root, and a submission is the
// first thing to write one.
func communityTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if res := check.LoadProfile(dir, pack.ProfileCommunity); !res.OK() {
		t.Fatalf("empty community tree does not validate: %v", res.Problems)
	}
	return dir
}

// worksArtifact builds a real meta.sqlite from a small CORE tree and returns its
// path. It is the same builder the release cuts with (internal/build over
// check.LoadProfile), so the tables and columns the lookups read are the
// artifact's own rather than a hand-rolled fixture schema.
//
// The tree holds one work, existing-work, and one tombstone retiring old-work
// onto it - the two states a submitted slug can be in besides "unknown".
func worksArtifact(t *testing.T) string {
	t.Helper()
	core := t.TempDir()
	testpack.Seed(t, core, map[string]string{
		"people/ja/jane-doe.json":          testpack.PersonJSON(t, "jane-doe", "Jane Doe"),
		"works/ex/existing-work/work.json": testpack.WorkJSON(t, "existing-work", "Existing Work"),
	})
	// The tombstone table is core glue and has no per-record address, so it is
	// written straight to the root pkg/pack accounts for it at.
	redirects := `{
  "people": {},
  "series": {},
  "works": {
    "old-work": "existing-work"
  }
}
`
	if err := os.WriteFile(filepath.Join(core, pack.RedirectsFile), []byte(redirects), 0o644); err != nil {
		t.Fatalf("write redirects: %v", err)
	}

	res := check.LoadProfile(core, pack.ProfileCore)
	if !res.OK() {
		t.Fatalf("core fixture tree does not validate: %v", res.Problems)
	}
	out := filepath.Join(t.TempDir(), "meta.sqlite")
	if err := build.Build(res.Catalog, out, time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("build artifact: %v", err)
	}
	return out
}

// communityOptions is the shape a community intake run takes: the community
// root, its profile, and the artifact the work key is verified against.
func communityOptions(dir, worksDB, tmpl, body string) Options {
	return Options{DataDir: dir, Profile: pack.ProfileCommunity, WorksDB: worksDB, Template: tmpl, Body: body}
}

// TestCommunityProfileComposesASidecar is the green path both sidecar forms take
// on the community repository: one family in the tree, the work verified against
// the artifact, and a pack file written under works-community.
func TestCommunityProfileComposesASidecar(t *testing.T) {
	db := worksArtifact(t)
	for _, tc := range []struct {
		tmpl, address string
		body          string
	}{
		{"characters", "works/ex/existing-work/characters.json", charactersBody("existing-work", validCharactersJSON, true)},
		{"recaps", "works/ex/existing-work/recaps.json", recapsBody("existing-work", testpack.RecapsJSON(t, "existing-work", 3), true)},
	} {
		t.Run(tc.tmpl, func(t *testing.T) {
			dir := communityTree(t)
			res := Process(communityOptions(dir, db, tc.tmpl, tc.body))
			if res.Status != StatusOK {
				t.Fatalf("status = %q, want ok; messages = %v", res.Status, res.Messages)
			}
			if len(res.Files) != 1 || !strings.HasPrefix(res.Files[0], "data/"+pack.FamilyWorksCommunity.Root()+"/") {
				t.Fatalf("files = %v, want one under the community family", res.Files)
			}
			if !testpack.Exists(t, dir, tc.address) {
				t.Errorf("no %s member was written", tc.tmpl)
			}
			// The composed tree has to validate under the profile it was written
			// under - which is also what the community repository's CI runs.
			if r := check.LoadProfile(dir, pack.ProfileCommunity); !r.OK() {
				t.Errorf("composed community tree does not validate: %v", r.Problems)
			}
		})
	}
}

// TestCommunityProfileSendsACoreFormToCore: the community repository holds no
// core family, so an add-work (or any other core) submission cannot be composed
// there. The verdict is needs-human - the submission is fine, it is on the wrong
// repository - and it names the one it belongs on.
func TestCommunityProfileSendsACoreFormToCore(t *testing.T) {
	dir := communityTree(t)
	db := worksArtifact(t)
	for _, tmpl := range []string{"add-work", "add-recording", "correct-data", "import"} {
		res := Process(communityOptions(dir, db, tmpl, ""))
		if res.Status != StatusNeedsHuman {
			t.Errorf("%s: status = %q, want needs-human; messages = %v", tmpl, res.Status, res.Messages)
		}
		if !anyContains(res.Messages, coreRepoIssues) {
			t.Errorf("%s: the verdict does not name the core repository: %v", tmpl, res.Messages)
		}
		if len(res.Files) != 0 {
			t.Errorf("%s: a refused template wrote files: %v", tmpl, res.Files)
		}
	}
}

// TestCoreProfileSendsASidecarFormToTheCommunityRepo is the mirror image, which
// is what the core repository will run once the cutover lands: the sidecar forms
// write a family a core root does not hold.
func TestCoreProfileSendsASidecarFormToTheCommunityRepo(t *testing.T) {
	dir := t.TempDir()
	for _, tmpl := range []string{"characters", "recaps"} {
		res := Process(Options{DataDir: dir, Profile: pack.ProfileCore, Template: tmpl})
		if res.Status != StatusNeedsHuman {
			t.Errorf("%s: status = %q, want needs-human; messages = %v", tmpl, res.Status, res.Messages)
		}
		if !anyContains(res.Messages, communityRepoIssues) {
			t.Errorf("%s: the verdict does not name the community repository: %v", tmpl, res.Messages)
		}
	}
}

// TestCommunityProfileRefusesAnUnknownWork: the entry key IS a core work slug, so
// a submission naming a book the catalogue does not hold is refused rather than
// composed - the pull request would fail the community repository's own key
// check, and the release build would stop on it.
func TestCommunityProfileRefusesAnUnknownWork(t *testing.T) {
	dir := communityTree(t)
	body := charactersBody("no-such-book", validCharactersJSON, true)
	res := Process(communityOptions(dir, worksArtifact(t), "characters", body))
	if res.Status != StatusInvalid {
		t.Fatalf("status = %q, want invalid; messages = %v", res.Status, res.Messages)
	}
	if !anyContains(res.Messages, `"no-such-book"`) {
		t.Errorf("the verdict does not name the slug it refused: %v", res.Messages)
	}
	if len(res.Files) != 0 {
		t.Errorf("an unverified sidecar was written: %v", res.Files)
	}
}

// TestCommunityProfileRekeysARetiredWork: a core merge retires a slug and
// tombstones it. A submitter is reading a page under the OLD slug (metaserve
// 301s it), so the form arrives naming it - and the bot composes under the
// SURVIVOR, because writing the tombstoned key would open a pull request whose
// own CI refuses it with "re-key to ...".
func TestCommunityProfileRekeysARetiredWork(t *testing.T) {
	dir := communityTree(t)
	body := charactersBody("old-work", validCharactersJSON, true)
	res := Process(communityOptions(dir, worksArtifact(t), "characters", body))
	if res.Status != StatusOK {
		t.Fatalf("status = %q, want ok; messages = %v", res.Status, res.Messages)
	}
	if !testpack.Exists(t, dir, "works/ex/existing-work/characters.json") {
		t.Error("the sidecar was not composed under the surviving slug")
	}
	if testpack.Exists(t, dir, "works/ol/old-work/characters.json") {
		t.Error("the sidecar was composed under the RETIRED slug")
	}
	// The re-key is a fact about the contribution, so it is reported rather than
	// applied silently: a maintainer reading the pull request has to be able to
	// see the key is not the one the form said.
	if !anyContains(res.Messages, "old-work") || !anyContains(res.Messages, "existing-work") {
		t.Errorf("the verdict does not report the re-key: %v", res.Messages)
	}
}

// TestCommunityProfileRefusesWithoutTheArtifact: with no works family and no
// artifact there is NOTHING that can answer whether the key names a live work,
// and the bot never composes a key it could not verify. needs-human, because the
// missing input is the bot's own and not the submitter's.
func TestCommunityProfileRefusesWithoutTheArtifact(t *testing.T) {
	dir := communityTree(t)
	body := charactersBody("existing-work", validCharactersJSON, true)
	res := Process(Options{DataDir: dir, Profile: pack.ProfileCommunity, Template: "characters", Body: body})
	if res.Status != StatusNeedsHuman {
		t.Fatalf("status = %q, want needs-human; messages = %v", res.Status, res.Messages)
	}
	if !anyContains(res.Messages, "--works-db") {
		t.Errorf("the verdict does not name the missing input: %v", res.Messages)
	}
	if len(res.Files) != 0 {
		t.Errorf("a sidecar was composed with nothing to verify it: %v", res.Files)
	}
}

// TestCommunityProfileRefusesANonArtifact: --works-db is an operator-supplied
// path, so a file that is not a release artifact is named rather than read as an
// empty catalogue - which would refuse every submission as "unknown work".
func TestCommunityProfileRefusesANonArtifact(t *testing.T) {
	dir := communityTree(t)
	notAnArtifact := filepath.Join(t.TempDir(), "meta.sqlite")
	if err := os.WriteFile(notAnArtifact, []byte("not a database"), 0o644); err != nil {
		t.Fatal(err)
	}
	body := charactersBody("existing-work", validCharactersJSON, true)
	res := Process(communityOptions(dir, notAnArtifact, "characters", body))
	if res.Status != StatusNeedsHuman {
		t.Fatalf("status = %q, want needs-human; messages = %v", res.Status, res.Messages)
	}
	if !anyContains(res.Messages, notAnArtifact) {
		t.Errorf("the verdict does not name the file it could not read: %v", res.Messages)
	}
}

// TestCoreProfileIgnoresTheArtifact pins the other half of the contract: where
// the works family IS in the tree, the catalogue answers and the artifact is
// never consulted. A core run handed a path that is not a database at all still
// behaves exactly as it always did.
func TestCoreProfileIgnoresTheArtifact(t *testing.T) {
	dir := seedTree(t)
	body := charactersBody("existing-work", validCharactersJSON, true)
	res := Process(Options{DataDir: dir, Template: "characters", Body: body, WorksDB: "/no/such/artifact.sqlite"})
	if res.Status != StatusOK {
		t.Fatalf("status = %q, want ok; messages = %v", res.Status, res.Messages)
	}
}
