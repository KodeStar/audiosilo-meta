package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// real-data.yml's push `paths:` filter MIRRORS check.yml's Go gate - the `go`
// answer of .github/scripts/pr-changes.sh - with ONE deliberate exception:
// site/** (the Astro site) runs the Go gate but not the real-data suite. This
// drives both over the same paths, the filter through GitHub's documented
// matching (patterns in order, a later `!` excluding and a later positive one
// re-including), the script against a stub `gh` listing a one-file PR.

// pushPathsRE captures real-data.yml's `push:` block's paths list: the
// `branches: [main]` line, then `paths:` and its quoted items. No match is a
// failure, so a reshaped file can never read as an empty filter.
var pushPathsRE = regexp.MustCompile(`(?m)^  push:\n    branches: \[main\]\n    paths:\n((?:      - "[^"]+"\n)+)`)

var pathItemRE = regexp.MustCompile(`"([^"]+)"`)

func realDataPushPaths(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", ".github", "workflows", "real-data.yml"))
	if err != nil {
		t.Fatal(err)
	}
	m := pushPathsRE.FindSubmatch(raw)
	if m == nil {
		t.Fatal("found no `push: branches: [main] paths:` list in real-data.yml")
	}
	var pats []string
	for _, item := range pathItemRE.FindAllSubmatch(m[1], -1) {
		pats = append(pats, string(item[1]))
	}
	return pats
}

// globRE translates one path pattern. Only `**` (any characters, `/`
// included) is modelled; any other glob syntax left after translating it
// FAILS rather than being guessed at, so the guard cannot agree with a filter
// it misreads.
func globRE(t *testing.T, pat string) *regexp.Regexp {
	t.Helper()
	body := strings.ReplaceAll(regexp.QuoteMeta(pat), `\*\*`, ".*")
	if strings.Contains(body, `\*`) || strings.ContainsAny(pat, "?+[]") {
		t.Fatalf("pattern %q uses glob syntax this test does not model; extend globRE", pat)
	}
	return regexp.MustCompile("^" + body + "$")
}

type pathRule struct {
	re  *regexp.Regexp
	neg bool
}

func compileFilter(t *testing.T, pats []string) []pathRule {
	t.Helper()
	rules := make([]pathRule, len(pats))
	for i, p := range pats {
		rules[i] = pathRule{globRE(t, strings.TrimPrefix(p, "!")), strings.HasPrefix(p, "!")}
	}
	return rules
}

// filterMatches is GitHub's sequential evaluation of a paths list.
func filterMatches(rules []pathRule, path string) bool {
	in := false
	for _, r := range rules {
		if r.re.MatchString(path) {
			in = !r.neg
		}
	}
	return in
}

// prChangesGo runs pr-changes.sh over a one-file pull request and returns its
// `go` answer.
func prChangesGo(t *testing.T, path string) bool {
	t.Helper()
	requireTools(t, "bash", "grep", "wc", "tr")
	dir := t.TempDir()
	stub := "#!/bin/sh\nprintf '%s\\n' \"$GH_STUB_FILE\"\n"
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "output")
	cmd := exec.Command("bash", abs(t, filepath.Join("..", ".github", "scripts", "pr-changes.sh")))
	cmd.Env = append(os.Environ(),
		"PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GH_STUB_FILE="+path,
		"GITHUB_OUTPUT="+out,
		"GITHUB_EVENT_NAME=pull_request",
		"GITHUB_REPOSITORY=owner/repo",
		"PR=1",
	)
	if msg, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("pr-changes.sh over %q: %v\n%s", path, err, msg)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	if !strings.Contains(got, "go=true\n") && !strings.Contains(got, "go=false\n") {
		t.Fatalf("pr-changes.sh wrote no go= answer for %q:\n%s", path, got)
	}
	return strings.Contains(got, "go=true\n")
}

func TestRealDataPushFilterMirrorsTheGoGate(t *testing.T) {
	t.Parallel()
	rules := compileFilter(t, realDataPushPaths(t))
	for _, tc := range []struct {
		path string
		want bool // whether a push changing only this path runs real-data.yml
	}{
		{"pkg/check/check.go", true},
		{"go.mod", true},
		{"go.sum", true},
		{"data/go.mod", true},
		{"data/works/0/0.json", false},
		{"data/redirects.json", false},
		{"README.md", false},
		{"scripts/README.md", false},
		{"notes.mdx", true},
		{".github/workflows/real-data.yml", true},
		{".github/scripts/pr-changes.sh", true},
		{"schema/work.schema.json", true},
		{"cmd/metacheck/main.go", true},
		{"site/src/pages/index.astro", false},
		{"site/README.md", false},
	} {
		t.Run(tc.path, func(t *testing.T) {
			t.Parallel()
			filter := filterMatches(rules, tc.path)
			if filter != tc.want {
				t.Errorf("real-data.yml's push filter says %v, want %v", filter, tc.want)
			}
			gate := prChangesGo(t, tc.path)
			// The one documented divergence: site/** may reach the Go gate
			// but never the real-data suite. Everywhere else the two agree.
			if mirrored := gate && !strings.HasPrefix(tc.path, "site/"); filter != mirrored {
				t.Errorf("real-data.yml's push filter says %v, pr-changes.sh's go gate says %v (site/** excepted)", filter, gate)
			}
		})
	}
}
