package issueform

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kodestar/audiosilo-meta/internal/build"
	"github.com/kodestar/audiosilo-meta/internal/testpack"
	"github.com/kodestar/audiosilo-meta/pkg/check"
	"github.com/kodestar/audiosilo-meta/pkg/model"
	"github.com/kodestar/audiosilo-meta/pkg/pack"
	"github.com/kodestar/audiosilo-meta/pkg/redirects"
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
	return buildArtifactAt(t, filepath.Join(t.TempDir(), "meta.sqlite"))
}

// buildArtifactAt is worksArtifact with the output path named, for the DSN test
// (which needs the artifact to sit under a directory whose name is hostile to a
// file: URI). One builder for both, so the two cannot disagree about what an
// artifact holds.
func buildArtifactAt(t *testing.T, out string) string {
	t.Helper()
	core := t.TempDir()
	testpack.Seed(t, core, map[string]string{
		"people/ja/jane-doe.json":          testpack.PersonJSON(t, "jane-doe", "Jane Doe"),
		"works/ex/existing-work/work.json": testpack.WorkJSON(t, "existing-work", "Existing Work"),
	})
	// The tombstone table is core glue and has no per-record address, so it is
	// written straight to the root pkg/pack accounts for it at.
	writeRedirects(t, core, map[string]string{"old-work": "existing-work"})

	res := check.LoadProfile(core, pack.ProfileCore)
	if !res.OK() {
		t.Fatalf("core fixture tree does not validate: %v", res.Problems)
	}
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

// TestUnknownTemplateIsNamedRatherThanRouted: writeFamilies answers for an
// unknown template with the core families (its default), so a profile gate asked
// first would tell a submitter their nonexistent form belongs on the core
// repository. It is judged as unknown before the gate, under every profile.
func TestUnknownTemplateIsNamedRatherThanRouted(t *testing.T) {
	for _, p := range []pack.Profile{pack.ProfileAll, pack.ProfileCore, pack.ProfileCommunity} {
		res := Process(Options{DataDir: t.TempDir(), Profile: p, Template: "add-quotes"})
		if res.Status != StatusInvalid {
			t.Errorf("%s: status = %q, want invalid; messages = %v", p, res.Status, res.Messages)
		}
		if !anyContains(res.Messages, `unknown template "add-quotes"`) {
			t.Errorf("%s: the verdict does not name the template: %v", p, res.Messages)
		}
		if anyContains(res.Messages, coreRepoIssues) || anyContains(res.Messages, communityRepoIssues) {
			t.Errorf("%s: an unknown template was routed to a repository: %v", p, res.Messages)
		}
	}
}

// TestEveryRoutingTemplateNormalizesToAKnownOne is the drift guard on the
// derivation, observed BEHAVIORALLY: a label that routes must reach a switch
// case in process, never the routed-then-unknown outcome. Membership in
// normalizedTemplates cannot be the assertion - that set is built from the very
// expression under test - so each template is Processed (an empty body is fine:
// any verdict is acceptable except the unknown-template one).
func TestEveryRoutingTemplateNormalizesToAKnownOne(t *testing.T) {
	dir := seedTree(t)
	for tmpl := range normalizedTemplates {
		res := Process(Options{DataDir: dir, Template: tmpl, Body: ""})
		if anyContains(res.Messages, "unknown template") {
			t.Errorf("template %q routes but the process switch does not handle it: %v", tmpl, res.Messages)
		}
	}
	if got, want := len(normalizedTemplates), len(routingTemplates); got != want {
		t.Errorf("normalizedTemplates has %d entries, routingTemplates %d - two labels normalized onto one template", got, want)
	}
}

// TestCommunityProfileRefusesAnUnknownWork: the entry key IS a core work slug, so
// a submission naming a book the catalogue does not hold is refused rather than
// composed - the pull request would fail the community repository's own key
// check, and the release build would stop on it.
//
// NEEDS-HUMAN, not invalid: the artifact is stale in one direction by design, so
// "nothing holds this slug" can be the bot's gap (a work added to core since the
// release) rather than the submitter's mistake - and it is the verdict a
// works-holding root gives for the same state, which one submission should not
// change by which repository it was sent to.
func TestCommunityProfileRefusesAnUnknownWork(t *testing.T) {
	dir := communityTree(t)
	body := charactersBody("no-such-book", validCharactersJSON, true)
	res := Process(communityOptions(dir, worksArtifact(t), "characters", body))
	if res.Status != StatusNeedsHuman {
		t.Fatalf("status = %q, want needs-human; messages = %v", res.Status, res.Messages)
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

// TestUnknownWorkIsTheSameVerdictOnEitherRoot pins the symmetry directly: one
// state, one verdict class, whichever repository the form was opened on. The
// works-holding side has always answered needs-human, and the artifact side now
// agrees rather than blaming the submitter for a release that has not been cut.
func TestUnknownWorkIsTheSameVerdictOnEitherRoot(t *testing.T) {
	body := charactersBody("no-such-book", validCharactersJSON, true)

	core := Process(Options{DataDir: seedTree(t), Template: "characters", Body: body})
	community := Process(communityOptions(communityTree(t), worksArtifact(t), "characters", body))

	if core.Status != StatusNeedsHuman || community.Status != StatusNeedsHuman {
		t.Fatalf("verdicts differ or are not needs-human: works-holding = %q, community = %q", core.Status, community.Status)
	}
}

// TestCommunityProfileRekeyRefusesAMemberStillAtTheRetiredKey is the state a core
// merge actually leaves behind, and the one a naive probe cannot see: the work
// slug is retired in core while the community entry keeps its OLD key until the
// re-key sweep lands. A submission naming the retired slug resolves to the
// survivor, finds nothing there, and would compose a SECOND characters member for
// the same book - a collision the release build refuses and a maintainer then has
// to unpick. So the guard looks under both keys.
func TestCommunityProfileRekeyRefusesAMemberStillAtTheRetiredKey(t *testing.T) {
	db := worksArtifact(t)
	for _, tc := range []struct{ name, ref string }{
		{"submitted under the retired slug", "old-work"},
		{"submitted under the survivor", "existing-work"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			// The sidecar is stored where the merge left it: the RETIRED key.
			testpack.Seed(t, dir, map[string]string{
				"works/ol/old-work/characters.json": testpack.CharactersJSON(t, "old-work", "alice"),
			})
			res := Process(communityOptions(dir, db, "characters",
				charactersBody(tc.ref, validCharactersJSON, true)))
			if res.Status != StatusNeedsHuman {
				t.Fatalf("status = %q, want needs-human; messages = %v", res.Status, res.Messages)
			}
			if !anyContains(res.Messages, "already exists") {
				t.Errorf("the verdict does not name the existing sidecar: %v", res.Messages)
			}
			if len(res.Files) != 0 {
				t.Errorf("a competing member was composed: %v", res.Files)
			}
		})
	}
}

// TestCommunityProfileRekeyStillComposesTheSiblingKind is the other half of the
// same probe: the retired-key entry blocks only the member KIND being submitted.
// A recaps submission for a work whose retired entry carries characters is
// ordinary work and must still compose - under the survivor, since that is the
// key every new record takes.
func TestCommunityProfileRekeyStillComposesTheSiblingKind(t *testing.T) {
	dir := t.TempDir()
	testpack.Seed(t, dir, map[string]string{
		"works/ol/old-work/characters.json": testpack.CharactersJSON(t, "old-work", "alice"),
	})
	res := Process(communityOptions(dir, worksArtifact(t), "recaps",
		recapsBody("old-work", testpack.RecapsJSON(t, "existing-work", 3), true)))
	if res.Status != StatusOK {
		t.Fatalf("status = %q, want ok; messages = %v", res.Status, res.Messages)
	}
	if !testpack.Exists(t, dir, "works/ex/existing-work/recaps.json") {
		t.Error("the recaps member was not composed under the surviving slug")
	}
}

// TestWorksHoldingRootResolvesItsOwnTombstones: a works-holding root carries
// data/redirects.json, so a submitter who pasted a work-page URL for a retired
// slug (metaserve 301s it, so they never learn the slug changed) must get the
// same re-key the artifact branch gives - not "not found" from a bot holding the
// table that names the survivor.
func TestWorksHoldingRootResolvesItsOwnTombstones(t *testing.T) {
	dir := seedTree(t)
	writeRedirects(t, dir, map[string]string{"retired-work": "existing-work"})
	if res := check.Load(dir); !res.OK() {
		t.Fatalf("seed tree with a tombstone does not validate: %v", res.Problems)
	}

	res := Process(Options{DataDir: dir, Template: "characters",
		Body: charactersBody("retired-work", validCharactersJSON, true)})
	if res.Status != StatusOK {
		t.Fatalf("status = %q, want ok; messages = %v", res.Status, res.Messages)
	}
	if !testpack.Exists(t, dir, "works/ex/existing-work/characters.json") {
		t.Error("the sidecar was not composed under the surviving slug")
	}
	if !anyContains(res.Messages, "retired-work") || !anyContains(res.Messages, "existing-work") {
		t.Errorf("the verdict does not report the re-key: %v", res.Messages)
	}
}

// TestWorksHoldingRootRefusesAMemberStillAtTheRetiredKey is finding 1 on the
// works-holding side: the two branches resolve the same way, so they must guard
// the same way too.
func TestWorksHoldingRootRefusesAMemberStillAtTheRetiredKey(t *testing.T) {
	dir := seedTree(t)
	writeRedirects(t, dir, map[string]string{"retired-work": "existing-work"})
	testpack.Seed(t, dir, map[string]string{
		"works/re/retired-work/characters.json": testpack.CharactersJSON(t, "retired-work", "alice"),
	})

	res := Process(Options{DataDir: dir, Template: "characters",
		Body: charactersBody("retired-work", validCharactersJSON, true)})
	if res.Status != StatusNeedsHuman {
		t.Fatalf("status = %q, want needs-human; messages = %v", res.Status, res.Messages)
	}
	if !anyContains(res.Messages, "already exists") {
		t.Errorf("the verdict does not name the existing sidecar: %v", res.Messages)
	}
}

// writeRedirects puts a tombstone table at the one path pkg/pack accounts for
// it at, through the canonical writer - the fixture then cannot drift from the
// file shape pkg/redirects produces. It has no per-record address, so
// testpack.Seed cannot place it.
func writeRedirects(t *testing.T, dir string, works map[string]string) {
	t.Helper()
	if err := redirects.Write(dir, model.Redirects{model.RedirectWorks: works}); err != nil {
		t.Fatalf("write redirects: %v", err)
	}
}

// TestWorksDBDSNEscapesThePath: --works-db is an operator-supplied path spliced
// into a file: URI, where '?' starts the query (dropping mode=ro and naming a
// different file), '#' truncates, and a literal %NN is decoded to other bytes.
// The check that matters is behavioural - the artifact at such a path really does
// open - rather than the exact spelling of the DSN.
func TestWorksDBDSNEscapesThePath(t *testing.T) {
	for _, dirName := range []string{"a?b", "c#d", "e%2Ff", "g h"} {
		t.Run(dirName, func(t *testing.T) {
			// The artifact is BUILT at an ordinary path and MOVED, because the
			// builder opens its output with a plain (non-URI) DSN and the driver
			// splits that on '?' too - a fixture written the other way round would
			// leave no file at the path under test and prove nothing.
			built := buildArtifactAt(t, filepath.Join(t.TempDir(), "meta.sqlite"))
			base := filepath.Join(t.TempDir(), dirName)
			if err := os.MkdirAll(base, 0o755); err != nil {
				t.Fatal(err)
			}
			artifact := filepath.Join(base, "meta.sqlite")
			if err := os.Rename(built, artifact); err != nil {
				t.Fatal(err)
			}

			dsn, err := worksDBDSN(artifact)
			if err != nil {
				t.Fatalf("worksDBDSN: %v", err)
			}
			// The delimiters must not survive into the URI as themselves, or the
			// parser reads them as syntax rather than as the path they are.
			if i := strings.IndexAny(dsn, "?#"); i >= 0 && dsn[i:] != "?mode=ro" {
				t.Errorf("DSN carries an unescaped delimiter: %s", dsn)
			}
			if !strings.HasSuffix(dsn, "?mode=ro") {
				t.Errorf("DSN lost its read-only parameter: %s", dsn)
			}

			w, err := openWorksDB(artifact)
			if err != nil {
				t.Fatalf("openWorksDB on a %q path: %v", dirName, err)
			}
			defer func() { _ = w.Close() }()
			if _, verdict, err := w.resolve("existing-work"); err != nil || verdict != workLive {
				t.Errorf("resolve = %v, %v; want workLive - the wrong file was opened", verdict, err)
			}
		})
	}
}

// TestWorksDBDSNSpliceWouldOpenTheWrongFile is the finding's teeth: without the
// escaping, a '?' in the path ends the file name and the rest becomes URI
// parameters, so SQLite opens (and, since mode=ro never parsed, CREATES) a
// different file and reports success. The bug is silent by nature, which is why
// it is pinned from the failing side rather than only from the fixed one.
func TestWorksDBDSNSpliceWouldOpenTheWrongFile(t *testing.T) {
	built := buildArtifactAt(t, filepath.Join(t.TempDir(), "meta.sqlite"))
	base := filepath.Join(t.TempDir(), "a?b")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(base, "meta.sqlite")
	if err := os.Rename(built, artifact); err != nil {
		t.Fatal(err)
	}

	spliced, err := sql.Open("sqlite", "file:"+artifact+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = spliced.Close() }()
	var n int
	if err := spliced.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name = 'works'`).Scan(&n); err != nil {
		// Some other file, or none - either way not the artifact.
		t.Logf("spliced DSN failed outright: %v", err)
		return
	}
	if n != 0 {
		t.Fatal("the spliced DSN opened the real artifact - this test no longer pins anything")
	}
	// The real proof: the same path through the escaped DSN DOES see it.
	w, err := openWorksDB(artifact)
	if err != nil {
		t.Fatalf("openWorksDB: %v", err)
	}
	defer func() { _ = w.Close() }()
	if _, verdict, err := w.resolve("existing-work"); err != nil || verdict != workLive {
		t.Errorf("resolve = %v, %v; want workLive", verdict, err)
	}
}

// TestImportTemplateIsJudgedUnderItsRootsProfile: the import template is the one
// form that hands the tree to ANOTHER writer (internal/importer), which opens its
// own store and runs its own post-write validation - so without the profile
// travelling with the root, a scoped tree would be opened and validated as
// ProfileAll. That is not a hypothetical difference: ProfileAll accounts for
// data/works-community/ as a family root, so a core tree still holding a leftover
// community family reads CLEAN under it and as an unrecognized location under
// ProfileCore, which is precisely the state the profile mechanism exists to catch.
//
// The fixture is that state: a root holding a leftover community family. Under
// ProfileAll the import succeeds (that root really does hold every family);
// under ProfileCore the same import must be refused, because the tree it wrote
// into does not validate as the tree it claims to be.
func TestImportTemplateIsJudgedUnderItsRootsProfile(t *testing.T) {
	leftover := map[string]string{
		"works/ex/existing-work/characters.json": testpack.CharactersJSON(t, "existing-work", "alice"),
	}
	body := importBody("OpenAudible (books.json)", openAudibleExport)

	// The fixture's premise, asserted rather than assumed: the two profiles really
	// do disagree about this tree, which is the whole reason the threading matters.
	premise := seedTree(t)
	testpack.Seed(t, premise, leftover)
	if res := check.LoadProfile(premise, pack.ProfileAll); !res.OK() {
		t.Fatalf("fixture is not clean under ProfileAll: %v", res.Problems)
	}
	if res := check.LoadProfile(premise, pack.ProfileCore); res.OK() {
		t.Fatal("fixture is clean under ProfileCore too - it no longer pins the difference")
	}

	all := seedTree(t)
	testpack.Seed(t, all, leftover)
	if res := Process(Options{DataDir: all, Profile: pack.ProfileAll, Template: "import", Body: body}); res.Status != StatusOK {
		t.Fatalf("ProfileAll import status = %q, want ok; messages = %v", res.Status, res.Messages)
	}

	core := seedTree(t)
	testpack.Seed(t, core, leftover)
	res := Process(Options{DataDir: core, Profile: pack.ProfileCore, Template: "import", Body: body})
	if res.Status == StatusOK {
		t.Fatalf("ProfileCore import reported ok over a root holding a family it does not: %v", res.Messages)
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
