package migrate

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/kodestar/audiosilo-meta/internal/build"
	"github.com/kodestar/audiosilo-meta/pkg/check"
	"github.com/kodestar/audiosilo-meta/pkg/model"
)

// The equivalence proof, at fixture scale.
//
// The migration is only safe if the release artifact does not change: the data
// is the same data, and added_at - the one value that moved, from a git-history
// walk at release time into the records themselves - has to come out holding the
// same bytes. This test is the dress rehearsal for the proof the migration PR
// runs against the real repository, and it is the part of that proof which can
// be kept honest forever:
//
//	PRE  = the pre-migration path. A legacy tree, read by a reader written HERE
//	       (loadLegacyCatalog - deliberately not the migrator's own code), dated
//	       by release.yml's literal added.tsv shell pipeline, built.
//	POST = the post-migration path. The same tree converted by metamigrate, read
//	       by check.Load, built.
//
// Byte-identical artifacts mean the conversion preserved every record and every
// field, and put every git-derived date exactly where the retired --added map
// used to supply it.
//
// The PRE side stamps the tsv dates onto Work.AddedAt because that is precisely
// what the retired build.Build(added map) parameter did with them: added[id]
// first, the sources[].imported_at fallback second. The precedence itself is
// pinned by internal/build's own tests.
func TestArtifactEquivalenceAcrossTheMigration(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available; the added.tsv walk cannot be run")
	}
	repo := t.TempDir()
	dataDir := filepath.Join(repo, "data")
	seedRepoHistory(t, repo)

	// PRE: the retired release.yml step, verbatim, then the retired --added
	// precedence over a legacy load.
	added := parseAddedTSV(t, addedTSV(t, repo))
	if len(added) != 2 {
		t.Fatalf("added.tsv dated %d works, want 2: %v", len(added), added)
	}
	cat := loadLegacyCatalog(t, dataDir)
	for _, w := range cat.Works {
		if d := added[w.ID]; d != "" {
			w.AddedAt = d
		}
	}
	builtAt := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	pre := filepath.Join(t.TempDir(), "pre.sqlite")
	if err := build.Build(cat, pre, builtAt); err != nil {
		t.Fatal(err)
	}

	// POST: convert in place, load the packs, build again.
	if _, err := Run(Options{DataDir: dataDir, Backfill: true}); err != nil {
		t.Fatal(err)
	}
	res := check.Load(dataDir)
	if !res.OK() {
		t.Fatalf("the converted tree does not validate:\n%s", problems(res))
	}
	post := filepath.Join(t.TempDir(), "post.sqlite")
	if err := build.Build(res.Catalog, post, builtAt); err != nil {
		t.Fatal(err)
	}

	preBytes, postBytes := readFile(t, pre), readFile(t, post)
	if !bytes.Equal(preBytes, postBytes) {
		t.Fatalf("the artifact changed across the migration: %d bytes before, %d after",
			len(preBytes), len(postBytes))
	}

	// And the dates really did travel: the artifact holds the commit timestamps
	// verbatim, not a normalized or re-derived form of them.
	for _, w := range res.Catalog.Works {
		if got, want := w.AddedAt, added[w.ID]; got != want {
			t.Errorf("work %q added_at = %q, want the history's %q", w.ID, got, want)
		}
	}
}

// seedRepoHistory builds a git repository whose history adds the fixture's
// records in three commits, at three distinct author dates with three distinct
// UTC offsets - so a date that was normalized on the way through would not
// survive the comparison.
func seedRepoHistory(t *testing.T, repo string) {
	t.Helper()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.com",
			"GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	commit := func(date string, files map[string]string) {
		t.Helper()
		writeTree(t, filepath.Join(repo, "data"), files)
		git("add", "-A")
		cmd := exec.Command("git", "commit", "-m", "seed", "--date", date)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.com",
			"GIT_AUTHOR_DATE="+date,
			"GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.com",
			"GIT_COMMITTER_DATE="+date)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git commit: %v: %s", err, out)
		}
	}

	git("init", "-q")
	git("config", "user.name", "Fixture")
	git("config", "user.email", "fixture@example.com")
	// A developer's ~/.gitconfig is inherited by a fresh repository, and a
	// signing setup there (commit.gpgsign with an agent behind it) makes these
	// commits prompt, stall, or fail. The fixture's commits are throwaway; pin
	// signing off so the test depends on git alone.
	git("config", "commit.gpgsign", "false")
	git("config", "tag.gpgsign", "false")

	all := legacyFixture()
	pick := func(keys ...string) map[string]string {
		out := map[string]string{}
		for _, k := range keys {
			out[k] = all[k]
		}
		return out
	}
	commit("2026-01-05T10:00:00+01:00", pick(
		"people/an/ann-doe.json", "people/bo/bob-roe.json",
		"works/du/dune/work.json", "works/du/dune/recordings/ann-doe-2021.json"))
	commit("2026-02-06T09:30:00Z", pick(
		"series/du/dune.json", "works/du/dune/recordings/bob-roe-2020.json",
		"works/em/emma/work.json", "works/em/emma/recordings/ann-doe-2019.json"))
	commit("2026-03-07T12:00:00-05:00", pick(
		"works/du/dune/characters.json", "works/du/dune/recaps.json"))
}

// addedTSV runs release.yml's retired added.tsv step, verbatim. It is quoted
// here exactly as the workflow spelled it, because THAT is the contract the
// backfill has to reproduce - not this package's reading of it.
func addedTSV(t *testing.T, repo string) string {
	t.Helper()
	const script = `git log --reverse --diff-filter=A --date-order ` +
		`--format='COMMIT%x09%aI' --name-only -- 'data/works' ` +
		`| awk -F'\t' '/^COMMIT\t/{d=$2; next} /work\.json$/{if (!($0 in seen)) {seen[$0]=1; print d "\t" $0}}'`
	cmd := exec.Command("sh", "-c", script)
	cmd.Dir = repo
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("added.tsv pipeline: %v", err)
	}
	return string(out)
}

// parseAddedTSV is the retired cmd/metabuild parseAdded: a tab-separated
// "<date>\t<path>" file keyed onto the work slug, first line per path winning.
func parseAddedTSV(t *testing.T, body string) map[string]string {
	t.Helper()
	out := map[string]string{}
	seen := map[string]bool{}
	for _, line := range strings.Split(body, "\n") {
		if line == "" {
			continue
		}
		date, p, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		p = path.Clean(strings.TrimSpace(p))
		if path.Base(p) != "work.json" || seen[p] {
			continue
		}
		seen[p] = true
		slug := path.Base(path.Dir(p))
		if _, dup := out[slug]; !dup {
			out[slug] = strings.TrimSpace(date)
		}
	}
	return out
}

// loadLegacyCatalog reads a file-per-entity tree into a Catalog.
//
// It is written here rather than reused from anywhere: the point of the proof is
// that the PRE side is an INDEPENDENT reading of the old tree, so that "the two
// artifacts match" means the migrator preserved the data rather than that two
// callers agreed with each other. It is deliberately unforgiving - anything it
// cannot read fails the test.
func loadLegacyCatalog(t *testing.T, dataDir string) *model.Catalog {
	t.Helper()
	cat := &model.Catalog{}
	byWork := map[string]*model.Work{}
	recsByWork := map[string][]*model.Recording{}

	var rels []string
	err := filepath.Walk(dataDir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(p, ".json") {
			return err
		}
		rel, rerr := filepath.Rel(dataDir, p)
		if rerr != nil {
			return rerr
		}
		rels = append(rels, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(rels)

	decode := func(rel string, v any) {
		t.Helper()
		raw, rerr := os.ReadFile(filepath.Join(dataDir, filepath.FromSlash(rel)))
		if rerr != nil {
			t.Fatal(rerr)
		}
		if jerr := json.Unmarshal(raw, v); jerr != nil {
			t.Fatalf("%s: %v", rel, jerr)
		}
	}
	for _, rel := range rels {
		parts := strings.Split(rel, "/")
		switch {
		case len(parts) == 4 && parts[0] == "works" && parts[3] == "work.json":
			var w model.Work
			decode(rel, &w)
			cat.Works = append(cat.Works, &w)
			byWork[w.ID] = &w
		case len(parts) == 5 && parts[0] == "works" && parts[3] == "recordings":
			var r model.Recording
			decode(rel, &r)
			recsByWork[parts[2]] = append(recsByWork[parts[2]], &r)
		case len(parts) == 4 && parts[0] == "works" && parts[3] == "characters.json":
			var c model.Characters
			decode(rel, &c)
			cat.Characters = append(cat.Characters, &c)
		case len(parts) == 4 && parts[0] == "works" && parts[3] == "recaps.json":
			var rc model.Recaps
			decode(rel, &rc)
			cat.Recaps = append(cat.Recaps, &rc)
		case len(parts) == 3 && parts[0] == "people":
			var p model.Person
			decode(rel, &p)
			cat.People = append(cat.People, &p)
		case len(parts) == 3 && parts[0] == "series":
			var s model.Series
			decode(rel, &s)
			cat.Series = append(cat.Series, &s)
		default:
			t.Fatalf("legacy loader: unrecognized %s", rel)
		}
	}
	// Recordings hang off their work in slug order, which is the order both the
	// old per-file walk (sorted paths) and the pack composite (sorted map keys)
	// produce.
	for slug, recs := range recsByWork {
		w := byWork[slug]
		if w == nil {
			t.Fatalf("legacy loader: recordings for %q with no work", slug)
		}
		sort.Slice(recs, func(i, j int) bool { return recs[i].ID < recs[j].ID })
		w.Recordings = recs
	}
	return cat
}

func readFile(t *testing.T, p string) []byte {
	t.Helper()
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
