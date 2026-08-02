// Package scripts_test verifies the operator scripts that CI and contributors
// run by hand. They are shell, so they are exercised the way they are used - by
// driving a real git conflict and running the script on it - rather than by
// re-implementing their logic in Go.
package scripts_test

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The three-way merge behind the pack-conflict recipe. Every case here is a real
// rebase: a base commit, a commit on main, a commit on the branch, and the
// script run on the conflict git leaves behind.
//
// The rules it has to get right are all about DELETION. A plain union of the two
// sides cannot express one - the side that removed a record gets it handed back
// by the side that still has it, silently - and this repository deletes records
// (a retracted source, a work that turned out to be a duplicate).

func work(id, title string) string {
	return `"` + id + `":{"id":"` + id + `","title":"` + title + `"}`
}

func workWithRecs(id, title, recs string) string {
	return `"` + id + `":{"id":"` + id + `","title":"` + title + `","recordings":{` + recs + `}}`
}

func rec(id, publisher string) string {
	if publisher == "" {
		return `"` + id + `":{"id":"` + id + `","work":"aaa"}`
	}
	return `"` + id + `":{"id":"` + id + `","work":"aaa","publisher":"` + publisher + `"}`
}

func pack(entries ...string) string {
	return `{"entries":{` + strings.Join(entries, ",") + `}}` + "\n"
}

// A works-community entry has no own fields: it is the map of its members, one
// per sidecar, each a licensed document of its own.
func community(id string, members ...string) string {
	return `"` + id + `":{` + strings.Join(members, ",") + `}`
}

func charsMember(work, name string) string {
	return `"characters":{"work":"` + work + `","license":"CC-BY-SA-3.0",` +
		`"characters":[{"id":"c1","name":"` + name + `"}]}`
}

func recapsMember(work, text string) string {
	return `"recaps":{"work":"` + work + `","license":"CC-BY-SA-3.0",` +
		`"recaps":[{"through":{"chapter":1},"text":"` + text + `"}]}`
}

// The two families that have a one-level-deeper exception, since the script
// reads the family off the path.
const (
	worksPath     = "data/works/0/0.json"
	communityPath = "data/works-community/0/0.json"
)

func TestPackUnionMerge(t *testing.T) {
	requireTools(t, "git", "jq")
	script := abs(t, "pack-union-merge.sh")

	cases := []struct {
		name string
		// path is the pack file, which names the FAMILY and so decides which
		// one-level-deeper exception the script may apply. Empty means works.
		path string
		// base, main and branch are the three versions of the pack file.
		base, main, branch string
		// wantRefusal expects exit 5: a conflict only a person can settle.
		wantRefusal bool
		// present and absent are entry paths (jq-style, "aaa" or
		// "aaa.recordings.r1") the merged file must and must not hold.
		present, absent []string
	}{
		{
			name:    "each side adds its own record",
			base:    pack(work("aaa", "A")),
			main:    pack(work("aaa", "A"), work("mmm", "M")),
			branch:  pack(work("aaa", "A"), work("zzz", "Z")),
			present: []string{"aaa", "mmm", "zzz"},
		},
		{
			// The bug a two-sided union has: the branch deleted ddd, main still
			// has it, and a union would hand it straight back.
			name:    "a record the branch deleted stays deleted",
			base:    pack(work("aaa", "A"), work("ddd", "D")),
			main:    pack(work("aaa", "A"), work("ddd", "D"), work("mmm", "M")),
			branch:  pack(work("aaa", "A")),
			present: []string{"aaa", "mmm"},
			absent:  []string{"ddd"},
		},
		{
			name:    "a record main deleted stays deleted",
			base:    pack(work("aaa", "A"), work("ddd", "D")),
			main:    pack(work("aaa", "A")),
			branch:  pack(work("aaa", "A"), work("ddd", "D"), work("zzz", "Z")),
			present: []string{"aaa", "zzz"},
			absent:  []string{"ddd"},
		},
		{
			name:    "a record both sides deleted stays deleted",
			base:    pack(work("aaa", "A"), work("ddd", "D")),
			main:    pack(work("aaa", "A"), work("mmm", "M")),
			branch:  pack(work("aaa", "A"), work("zzz", "Z")),
			present: []string{"mmm", "zzz"},
			absent:  []string{"ddd"},
		},
		{
			name:        "one side deleted what the other edited",
			base:        pack(work("aaa", "A"), work("ddd", "D")),
			main:        pack(work("aaa", "A"), work("ddd", "D edited")),
			branch:      pack(work("aaa", "A")),
			wantRefusal: true,
		},
		{
			name:        "both sides edited one record",
			base:        pack(work("aaa", "A")),
			main:        pack(work("aaa", "A from main")),
			branch:      pack(work("aaa", "A from the branch")),
			wantRefusal: true,
		},
		{
			// The common intake collision: two narrations of one book.
			name:    "each side adds a recording to one work",
			base:    pack(workWithRecs("aaa", "A", rec("r1", ""))),
			main:    pack(workWithRecs("aaa", "A", rec("r1", "")+","+rec("r2", ""))),
			branch:  pack(workWithRecs("aaa", "A", rec("r1", "")+","+rec("r3", ""))),
			present: []string{"aaa.recordings.r1", "aaa.recordings.r2", "aaa.recordings.r3"},
		},
		{
			name:    "a recording the branch deleted stays deleted",
			base:    pack(workWithRecs("aaa", "A", rec("r1", "")+","+rec("r2", ""))),
			main:    pack(workWithRecs("aaa", "A", rec("r1", "")+","+rec("r2", "")+","+rec("r9", ""))),
			branch:  pack(workWithRecs("aaa", "A", rec("r1", ""))),
			present: []string{"aaa.recordings.r1", "aaa.recordings.r9"},
			absent:  []string{"aaa.recordings.r2"},
		},
		{
			name:        "both sides edited one recording",
			base:        pack(workWithRecs("aaa", "A", rec("r1", "old"))),
			main:        pack(workWithRecs("aaa", "A", rec("r1", "main"))),
			branch:      pack(workWithRecs("aaa", "A", rec("r1", "branch"))),
			wantRefusal: true,
		},
		{
			// Only the recordings map may differ: a work's own fields differing
			// is two people disagreeing about the book.
			name:        "the work's own fields differ too",
			base:        pack(workWithRecs("aaa", "A", rec("r1", ""))),
			main:        pack(workWithRecs("aaa", "A from main", rec("r1", "")+","+rec("r2", ""))),
			branch:      pack(workWithRecs("aaa", "A from the branch", rec("r1", "")+","+rec("r3", ""))),
			wantRefusal: true,
		},
		{
			// The works-community twin of the narrations collision, and the one
			// the live intake stream hits: a characters pull request and a recaps
			// pull request for the same book each CREATE the same entry, carrying
			// one disjoint member apiece.
			name:   "each side adds its own community member for one work",
			path:   communityPath,
			base:   pack(community("aaa", charsMember("aaa", "Ann"))),
			main:   pack(community("aaa", charsMember("aaa", "Ann")), community("bbb", charsMember("bbb", "Bob"))),
			branch: pack(community("aaa", charsMember("aaa", "Ann")), community("bbb", recapsMember("bbb", "So far"))),
			present: []string{
				"aaa.characters", "bbb.characters", "bbb.recaps",
			},
		},
		{
			// Two people describing the same characters is a disagreement about
			// one text, exactly as two people editing one recording is.
			name:        "both sides wrote the same community member",
			path:        communityPath,
			base:        pack(community("aaa", charsMember("aaa", "Ann"))),
			main:        pack(community("aaa", charsMember("aaa", "Ann from main"))),
			branch:      pack(community("aaa", charsMember("aaa", "Ann from the branch"))),
			wantRefusal: true,
		},
		{
			// The base decides per MEMBER: the branch removed the recaps, main
			// left them exactly as the base had them, so they are deleted rather
			// than handed back.
			name:    "a community member the branch deleted stays deleted",
			path:    communityPath,
			base:    pack(community("aaa", charsMember("aaa", "Ann")+","+recapsMember("aaa", "So far"))),
			main:    pack(community("aaa", charsMember("aaa", "Ann")+","+recapsMember("aaa", "So far")), community("mmm", charsMember("mmm", "Em"))),
			branch:  pack(community("aaa", charsMember("aaa", "Ann"))),
			present: []string{"aaa.characters", "mmm.characters"},
			absent:  []string{"aaa.recaps"},
		},
		{
			// Both deletions are honoured, and an entry with no members left is
			// nothing - it goes rather than staying behind as an empty record.
			name:    "both sides deleted a different community member",
			path:    communityPath,
			base:    pack(community("aaa", charsMember("aaa", "Ann")+","+recapsMember("aaa", "So far"))),
			main:    pack(community("aaa", charsMember("aaa", "Ann")), community("mmm", charsMember("mmm", "Em"))),
			branch:  pack(community("aaa", recapsMember("aaa", "So far"))),
			present: []string{"mmm.characters"},
			absent:  []string{"aaa"},
		},
		{
			// Delete versus modify at the member level is a person's call, the
			// same way it is at the entry level.
			name:        "one side deleted the community member the other rewrote",
			path:        communityPath,
			base:        pack(community("aaa", charsMember("aaa", "Ann")+","+recapsMember("aaa", "So far"))),
			main:        pack(community("aaa", charsMember("aaa", "Ann")+","+recapsMember("aaa", "So far, rewritten"))),
			branch:      pack(community("aaa", charsMember("aaa", "Ann"))),
			wantRefusal: true,
		},
		{
			// A family with no exception gets none: a person entry both sides
			// rewrote is a refusal whatever its shape.
			name:        "both sides edited one person record",
			path:        "data/people/0.json",
			base:        pack(work("aaa", "A")),
			main:        pack(work("aaa", "A from main")),
			branch:      pack(work("aaa", "A from the branch")),
			wantRefusal: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			packPath := c.path
			if packPath == "" {
				packPath = worksPath
			}
			repo := conflictedRebase(t, packPath, c.base, c.main, c.branch)

			out, err := runIn(repo, script, packPath)
			if c.wantRefusal {
				if err == nil {
					t.Fatalf("the merge went ahead; it must refuse this one:\n%s", out)
				}
				if code := exitCode(err); code != 5 {
					t.Errorf("exit code = %d, want 5 (a conflict for a person): %s", code, out)
				}
				return
			}
			if err != nil {
				t.Fatalf("merge failed: %v\n%s", err, out)
			}
			merged := readEntries(t, filepath.Join(repo, packPath))
			for _, p := range c.present {
				if !has(merged, p) {
					t.Errorf("%s is missing from the merged pack: %v", p, merged)
				}
			}
			for _, p := range c.absent {
				if has(merged, p) {
					t.Errorf("%s came back from the dead: %v", p, merged)
				}
			}
		})
	}
}

// conflictedRebase builds a repository whose branch rebase stops on a conflict in
// path, and returns the repository directory mid-rebase.
func conflictedRebase(t *testing.T, path, base, main, branch string) string {
	t.Helper()
	repo := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		out, err := runIn(repo, "git", args...)
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return out
	}
	write := func(body string) {
		t.Helper()
		full := filepath.Join(repo, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	git("init", "-q", "-b", "main")
	git("config", "user.name", "Fixture")
	git("config", "user.email", "fixture@example.com")
	// An inherited ~/.gitconfig with commit signing would make these prompt.
	git("config", "commit.gpgsign", "false")
	git("config", "tag.gpgsign", "false")

	write(base)
	git("add", "-A")
	git("commit", "-qm", "base")
	git("checkout", "-qb", "branch")
	write(branch)
	git("commit", "-qam", "branch")
	git("checkout", "-q", "main")
	write(main)
	git("commit", "-qam", "main")
	git("checkout", "-q", "branch")

	if out, err := runIn(repo, "git", "rebase", "main"); err == nil {
		t.Fatalf("the rebase did not conflict, so there is nothing to merge:\n%s", out)
	}
	return repo
}

// readEntries returns the merged pack's entries.
func readEntries(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Entries map[string]any `json:"entries"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("the merged file is not valid JSON: %v\n%s", err, raw)
	}
	return doc.Entries
}

// has reports whether the entries map holds the dotted path ("aaa" or
// "aaa.recordings.r1").
func has(entries map[string]any, dotted string) bool {
	cur := any(entries)
	for _, part := range strings.Split(dotted, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return false
		}
		if cur, ok = m[part]; !ok {
			return false
		}
	}
	return true
}

func runIn(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.com",
		"GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.com")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func exitCode(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

func requireTools(t *testing.T, names ...string) {
	t.Helper()
	for _, n := range names {
		if _, err := exec.LookPath(n); err != nil {
			t.Skipf("%s is not available", n)
		}
	}
}

// abs resolves a script in this directory to an absolute path, since the tests
// run it from a temporary repository.
func abs(t *testing.T, name string) string {
	t.Helper()
	p, err := filepath.Abs(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatal(err)
	}
	return p
}
