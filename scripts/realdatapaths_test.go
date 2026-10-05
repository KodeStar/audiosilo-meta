package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// real-data.yml's push trigger carries a `paths:` filter that MIRRORS
// check.yml's Go gate - the `go` answer of .github/scripts/pr-changes.sh. Two
// spellings of one rule drift, so this drives both over the same paths: the
// filter through GitHub's documented matching (patterns checked in order, a
// later `!` pattern excluding and a later positive one re-including), the
// script as check.yml runs it, against a stub `gh` listing a one-file pull
// request. A path the two answer differently for fails here.

// realDataPushPaths reads the `paths:` list of real-data.yml's `push:` trigger.
// A deliberately narrow reader of the file's own layout (two-space YAML, one
// quoted pattern per line): a layout it does not recognise is a failure, never
// an empty list.
func realDataPushPaths(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", ".github", "workflows", "real-data.yml"))
	if err != nil {
		t.Fatal(err)
	}
	item := regexp.MustCompile(`^      - "([^"]+)"$`)
	var pats []string
	inPush, inPaths := false, false
	for line := range strings.Lines(string(raw)) {
		line = strings.TrimSuffix(line, "\n")
		switch {
		case line == "  push:":
			inPush = true
		case inPush && line == "    paths:":
			inPaths = true
		case inPaths:
			m := item.FindStringSubmatch(line)
			if m == nil {
				inPush, inPaths = false, false
				continue
			}
			pats = append(pats, m[1])
		case inPush && !strings.HasPrefix(line, "    ") && !strings.HasPrefix(line, "  #"):
			inPush = false
		}
	}
	if len(pats) == 0 {
		t.Fatal("found no `paths:` list under real-data.yml's push trigger")
	}
	return pats
}

// globRE translates one GitHub path pattern into an anchored regexp: `**`
// matches any run of characters including `/`, `*` any run without one. Every
// other special character - and a `**/`, which GitHub also lets match zero
// directories - is REFUSED rather than approximated: the filter does not use
// them, and a matcher that guessed would make this guard agree with a filter it
// misreads.
func globRE(t *testing.T, pat string) *regexp.Regexp {
	t.Helper()
	if strings.ContainsAny(pat, "?+[]") || strings.Contains(pat, "**/") {
		t.Fatalf("pattern %q uses glob syntax this test does not model; extend globRE", pat)
	}
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pat); i++ {
		switch {
		case strings.HasPrefix(pat[i:], "**"):
			b.WriteString(".*")
			i++
		case pat[i] == '*':
			b.WriteString("[^/]*")
		default:
			b.WriteString(regexp.QuoteMeta(pat[i : i+1]))
		}
	}
	b.WriteString("$")
	return regexp.MustCompile(b.String())
}

// filterMatches is GitHub's sequential evaluation of a paths list.
func filterMatches(t *testing.T, pats []string, path string) bool {
	t.Helper()
	in := false
	for _, p := range pats {
		neg := strings.HasPrefix(p, "!")
		if globRE(t, strings.TrimPrefix(p, "!")).MatchString(path) {
			in = !neg
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
	switch {
	case strings.Contains(string(raw), "go=true\n"):
		return true
	case strings.Contains(string(raw), "go=false\n"):
		return false
	}
	t.Fatalf("pr-changes.sh wrote no go= answer for %q:\n%s", path, raw)
	return false
}

func TestRealDataPushFilterMirrorsTheGoGate(t *testing.T) {
	t.Parallel()
	pats := realDataPushPaths(t)
	for _, tc := range []struct {
		path string
		want bool // whether a push changing only this path runs real-data.yml
	}{
		{"data/works/0/0.json", false},
		{"data/people/0.json", false},
		{"data/redirects.json", false},
		{"data/go.mod", true},
		{"data/README.md", false},
		{"README.md", false},
		{"CLAUDE.md", false},
		{"scripts/README.md", false},
		{"site/src/content/guide.md", false},
		{"site/src/pages/index.astro", true},
		{"pkg/check/check.go", true},
		{"internal/importer/realdata_slug_test.go", true},
		{"cmd/metaserve/main.go", true},
		{"go.mod", true},
		{"go.sum", true},
		{".go-version", true},
		{"schema/work.schema.json", true},
		{"internal/serve/openapi.json", true},
		{".github/workflows/real-data.yml", true},
		{".github/scripts/real-data-report.sh", true},
		{"Dockerfile", true},
		{"notes.mdx", true},
		{"database.go", true},
	} {
		t.Run(tc.path, func(t *testing.T) {
			t.Parallel()
			filter := filterMatches(t, pats, tc.path)
			gate := prChangesGo(t, tc.path)
			if filter != gate {
				t.Errorf("real-data.yml's push filter says %v, pr-changes.sh's go gate says %v", filter, gate)
			}
			if filter != tc.want {
				t.Errorf("real-data.yml's push filter says %v, want %v", filter, tc.want)
			}
		})
	}
}
