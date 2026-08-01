package migrate

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The refusals, all in one place.
//
// Every one of them exists because the alternative is silent data loss: this
// tool deletes the tree it read, so a run that proceeds on a wrong assumption
// cannot be undone from anything but git. Each case here reproduces the
// assumption and asserts the run stops with a message that names the fix.

// gitRepo initializes a repository at dir with a committed data tree, and
// returns the data directory. Signing is pinned off: an inherited ~/.gitconfig
// with commit.gpgsign would make these commits prompt or stall.
func gitRepo(t *testing.T, files map[string]string) (repo, dataDir string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}
	repo = t.TempDir()
	dataDir = filepath.Join(repo, "data")
	writeTree(t, dataDir, files)
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.name", "Fixture"},
		{"config", "user.email", "fixture@example.com"},
		{"config", "commit.gpgsign", "false"},
		{"config", "tag.gpgsign", "false"},
		{"add", "-A"},
		{"commit", "-qm", "seed"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.com",
			"GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	return repo, dataDir
}

// A shallow clone answers the history walk with every path added by its single
// commit, so the run would "succeed" having dated the entire database on the day
// of the migration - uniform, plausible, and wrong. It has to be refused before
// a single file is touched.
func TestShallowCloneIsRefused(t *testing.T) {
	repo, _ := gitRepo(t, legacyFixture())
	shallow := filepath.Join(t.TempDir(), "shallow")
	clone := exec.Command("git", "clone", "-q", "--depth", "1", "file://"+repo, shallow)
	if out, err := clone.CombinedOutput(); err != nil {
		t.Skipf("could not make a shallow clone here: %v: %s", err, out)
	}
	shallowData := filepath.Join(shallow, "data")

	before := treeSnapshot(t, shallowData)
	_, err := Run(Options{DataDir: shallowData, Backfill: true})
	if err == nil {
		t.Fatal("a shallow clone was converted; every added_at would be the tip commit's date")
	}
	if !strings.Contains(err.Error(), "shallow") || !strings.Contains(err.Error(), "unshallow") {
		t.Errorf("error = %v, want it to name the shallow clone and the fix", err)
	}
	if len(treeSnapshot(t, shallowData)) != len(before) {
		t.Error("the refused run changed the tree")
	}
}

// The conversion is not atomic; a committed tree is what makes it recoverable.
// An uncommitted tree is refused - which is also what stops the worst re-run,
// since a half-deleted tree from an interrupted run is a dirty tree.
func TestUncommittedTreeIsRefusedInPlace(t *testing.T) {
	_, dataDir := gitRepo(t, legacyFixture())
	// The shape an interrupted run leaves: a whole work gone, nothing committed.
	// It still reads as a coherent (smaller) tree, which is exactly the problem -
	// only git can tell that records are missing.
	if err := os.RemoveAll(filepath.Join(dataDir, "works", "em")); err != nil {
		t.Fatal(err)
	}

	_, err := Run(Options{DataDir: dataDir})
	if err == nil {
		t.Fatal("an uncommitted (half-deleted) tree was converted")
	}
	for _, want := range []string{"uncommitted", "works/em/emma/work.json", "git checkout"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to mention %q", err, want)
		}
	}

	// Out of place there is nothing to recover, so the same tree converts.
	out := filepath.Join(t.TempDir(), "converted")
	if _, err := Run(Options{DataDir: dataDir, OutDir: out}); err != nil {
		t.Errorf("an --out run was refused for an uncommitted source: %v", err)
	}
}

// A committed tree converts, and the refusal is not just "is this a repository".
func TestCommittedTreeConverts(t *testing.T) {
	_, dataDir := gitRepo(t, legacyFixture())
	if _, err := Run(Options{DataDir: dataDir}); err != nil {
		t.Fatalf("a clean committed tree was refused: %v", err)
	}
}

// Finding no works at all is the signature of a wrong --data and of an
// interrupted run's leftovers. Converting it would report success over a
// database that is no longer there.
func TestEmptyTreeIsRefused(t *testing.T) {
	cases := map[string]map[string]string{
		"nothing at all": {},
		"people and series but no works": {
			"people/an/ann-doe.json": `{"id":"ann-doe","license":"CC0-1.0","name":"Ann Doe","sources":[{"type":"user"}]}`,
			"series/du/dune.json":    `{"id":"dune","license":"CC0-1.0","name":"Dune","sources":[{"type":"user"}],"works":[]}`,
		},
	}
	for name, files := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if len(files) > 0 {
				writeTree(t, dir, files)
			}
			if _, err := Run(Options{DataDir: dir}); err == nil {
				t.Fatal("a tree with no works was converted")
			} else if !strings.Contains(err.Error(), "no works") {
				t.Errorf("error = %v, want it to say the tree holds no works", err)
			}
		})
	}
}

// A file that is not JSON is neither converted nor deleted, so it would survive
// into the packed tree as debris metacheck then reports forever.
func TestNonJSONFileRefusesTheRun(t *testing.T) {
	files := legacyFixture()
	dir := seedLegacy(t, files)
	writeTree(t, dir, map[string]string{"works/du/dune/NOTES.md": "not a record"})

	_, err := Run(Options{DataDir: dir})
	if err == nil {
		t.Fatal("a tree with a non-JSON file was converted")
	}
	if !strings.Contains(err.Error(), "works/du/dune/NOTES.md") || !strings.Contains(err.Error(), "not JSON") {
		t.Errorf("error = %v, want it to name the file and say it is not JSON", err)
	}
	if len(treeSnapshot(t, dir)) != len(files)+1 {
		t.Error("the refused run changed the tree")
	}
}

// An output directory that already holds a family is refused: writing into it
// would leave one conversion's packs interleaved with another's.
func TestOccupiedOutputIsRefused(t *testing.T) {
	dir := seedLegacy(t, legacyFixture())
	out := t.TempDir()
	writeTree(t, out, map[string]string{"works/0/0.json": `{"entries":{}}`})

	_, err := Run(Options{DataDir: dir, OutDir: out})
	if err == nil {
		t.Fatal("an occupied output directory was written into")
	}
	if !strings.Contains(err.Error(), "not empty") {
		t.Errorf("error = %v, want it to say the output is not empty", err)
	}
}

// The scan's own refusals: a record that cannot be placed, and any second record
// for one identity. The old layout allowed the same slug under two shard
// directories, and one of the two would silently win the entry key.
func TestScanRefusals(t *testing.T) {
	cases := map[string]struct {
		mutate func(map[string]string)
		want   string
	}{
		"recordings with no work record": {
			mutate: func(f map[string]string) { delete(f, "works/du/dune/work.json") },
			want:   "has recordings or sidecars but no work.json",
		},
		"sidecars with no work record": {
			mutate: func(f map[string]string) {
				delete(f, "works/em/emma/work.json")
				delete(f, "works/em/emma/recordings/ann-doe-2019.json")
				f["works/em/emma/characters.json"] = `{"characters":[{"id":"x","name":"X","reveal":{"chapter":1}}],` +
					`"license":"CC-BY-SA-3.0","sources":[{"type":"community"}],"work":"emma"}`
			},
			want: "has recordings or sidecars but no work.json",
		},
		"one work slug under two shard directories": {
			mutate: func(f map[string]string) { f["works/xx/dune/work.json"] = f["works/du/dune/work.json"] },
			want:   "a second work record",
		},
		"one person slug under two shard directories": {
			mutate: func(f map[string]string) { f["people/xx/ann-doe.json"] = f["people/an/ann-doe.json"] },
			want:   "a second person record",
		},
		"one series slug under two shard directories": {
			mutate: func(f map[string]string) { f["series/xx/dune.json"] = f["series/du/dune.json"] },
			want:   "a second series record",
		},
		"one recording slug in two places": {
			mutate: func(f map[string]string) {
				f["works/xx/dune/recordings/bob-roe-2020.json"] = f["works/du/dune/recordings/bob-roe-2020.json"]
				f["works/xx/dune/work.json"] = f["works/du/dune/work.json"]
			},
			// The duplicate work is seen first; either refusal is the same answer.
			want: "a second",
		},
		"a second characters sidecar": {
			mutate: func(f map[string]string) {
				f["works/xx/dune/characters.json"] = f["works/du/dune/characters.json"]
			},
			want: "a second characters sidecar",
		},
		"a second recaps sidecar": {
			mutate: func(f map[string]string) {
				f["works/xx/dune/recaps.json"] = f["works/du/dune/recaps.json"]
			},
			want: "a second recaps sidecar",
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			files := legacyFixture()
			c.mutate(files)
			dir := seedLegacy(t, files)

			_, err := Run(Options{DataDir: dir})
			if err == nil {
				t.Fatal("the run was not refused")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error = %v, want it to contain %q", err, c.want)
			}
			if got := len(treeSnapshot(t, dir)); got != len(files) {
				t.Errorf("the refused run changed the tree: %d files, want %d", got, len(files))
			}
		})
	}
}
