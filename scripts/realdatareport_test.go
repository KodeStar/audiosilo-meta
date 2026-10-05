package scripts_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Tests of .github/scripts/real-data-report.sh, driven as the workflow drives
// it with a stub `gh` on PATH. They live here because the go tool ignores
// dot-directories.

// realDataReport runs the script with args and extra environment, returning
// stdout and the exit status. The environment is the test's own minus every
// variable the script reads, so a CI runner's GITHUB_REPOSITORY cannot leak in.
func realDataReport(t *testing.T, env []string, args ...string) (string, int) {
	t.Helper()
	requireTools(t, "bash")
	script := abs(t, filepath.Join("..", ".github", "scripts", "real-data-report.sh"))
	cmd := exec.Command("bash", append([]string{script}, args...)...)
	for _, kv := range os.Environ() {
		switch strings.SplitN(kv, "=", 2)[0] {
		case "GH_TOKEN", "GITHUB_REPOSITORY", "RESULT", "RUN_URL", "SHA", "FAILURES", "EVENT":
			continue
		}
		cmd.Env = append(cmd.Env, kv)
	}
	cmd.Env = append(cmd.Env, env...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if stderr.Len() > 0 {
		t.Logf("stderr: %s", stderr.String())
	}
	if err == nil {
		return string(out), 0
	}
	code := exitCode(err)
	if code < 0 {
		t.Fatalf("running the report script: %v", err)
	}
	return string(out), code
}

// runSummarize writes log (when non-nil) to a temp file and runs `summarize` on it.
func runSummarize(t *testing.T, log *string) (string, int) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.log")
	if log != nil {
		if err := os.WriteFile(path, []byte(*log), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return realDataReport(t, nil, "summarize", path)
}

func reportLog(s string) *string { return &s }

// The script's bounds, restated: a test of a cap has to know the cap.
const (
	reportMaxLines = 50
	reportMaxWidth = 300
)

func TestRealDataSummarize(t *testing.T) {
	t.Parallel()
	wide := "--- FAIL: TestWide/" + strings.Repeat("x", 3*reportMaxWidth) + " (0.00s)"
	for _, tc := range []struct {
		name string
		log  *string
		want string
	}{
		{
			name: "a wide line is cut",
			log:  reportLog(wide + "\nFAIL\tgithub.com/x/pkg\t0.1s\n"),
			want: wide[:reportMaxWidth] + "\nFAIL\tgithub.com/x/pkg\t0.1s\n",
		},
		{
			name: "missing log",
			log:  nil,
			want: "(no test log was written: the job failed before or around the test step)\n",
		},
		{
			name: "no FAIL line",
			log: reportLog("=== RUN   TestA\n--- PASS: TestA (0.00s)\nPASS\n" +
				"ok  \tgithub.com/x/pkg\t0.1s\n"),
			want: "(no --- FAIL: line in the test log: the failure is outside the tests - " +
				"a build error, a checkout or setup step, or a timeout; see the run)\n",
		},
		{
			// Indented subtest markers are kept, package FAIL lines are kept,
			// the bare FAIL, the test's own output and the ok lines are not.
			name: "failing tests",
			log: reportLog("=== RUN   TestA\n=== RUN   TestA/sub\n" +
				"    a_test.go:10: boom\n" +
				"--- FAIL: TestA (0.00s)\n" +
				"    --- FAIL: TestA/sub (0.00s)\n" +
				"FAIL\n" +
				"FAIL\tgithub.com/x/pkg\t0.1s\n" +
				"ok  \tgithub.com/x/other\t0.2s\n" +
				"FAIL\tgithub.com/x/broken [build failed]\n"),
			want: "--- FAIL: TestA (0.00s)\n" +
				"    --- FAIL: TestA/sub (0.00s)\n" +
				"FAIL\tgithub.com/x/pkg\t0.1s\n" +
				"FAIL\tgithub.com/x/broken [build failed]\n",
		},
		{
			// A timed-out binary prints no --- FAIL: line; the FIRST panic and
			// its four following lines (the hung tests' names) are what say
			// which test it was.
			name: "test timed out",
			log: reportLog("=== RUN   TestRealDataTree\n" +
				"panic: test timed out after 20m0s\n" +
				"\trunning tests:\n" +
				"\t\tTestRealDataTree (20m0s)\n" +
				"\n" +
				"goroutine 1 [running]:\n" +
				"testing.(*M).startAlarm.func1()\n" +
				"panic: a second one\n" +
				"FAIL\tgithub.com/x/pkg\t1200.1s\n"),
			want: "FAIL\tgithub.com/x/pkg\t1200.1s\n" +
				"panic: test timed out after 20m0s\n" +
				"\trunning tests:\n" +
				"\t\tTestRealDataTree (20m0s)\n" +
				"\n" +
				"goroutine 1 [running]:\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, code := runSummarize(t, tc.log)
			if code != 0 {
				t.Fatalf("summarize exited %d, want 0", code)
			}
			if got != tc.want {
				t.Errorf("summarize wrote\n%q\nwant\n%q", got, tc.want)
			}
		})
	}
}

// TestRealDataSummarizeBoundsALongList is the SIGPIPE regression: a failure
// list far past a pipe buffer, cut to MAX_LINES. Piped through `head`, the
// writer died of SIGPIPE, pipefail failed the script, and the workflow's
// $GITHUB_OUTPUT block was left unterminated.
func TestRealDataSummarizeBoundsALongList(t *testing.T) {
	t.Parallel()
	const total = 3000
	var log strings.Builder
	for i := range total {
		fmt.Fprintf(&log, "--- FAIL: TestSomethingWithAReasonablyLongName%04d/and_a_subtest_name (0.00s)\n", i)
	}
	if log.Len() < 2*65536 {
		t.Fatalf("the log is %d bytes, too short to outrun a pipe buffer", log.Len())
	}
	got, code := runSummarize(t, reportLog(log.String()))
	if code != 0 {
		t.Fatalf("summarize exited %d on a %d-line failure list, want 0", code, total)
	}
	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	if len(lines) != reportMaxLines+1 {
		t.Fatalf("summarize wrote %d lines, want %d plus the count line", len(lines), reportMaxLines)
	}
	for i, line := range lines[:reportMaxLines] {
		want := fmt.Sprintf("--- FAIL: TestSomethingWithAReasonablyLongName%04d/and_a_subtest_name (0.00s)", i)
		if line != want {
			t.Fatalf("line %d is %q, want %q", i, line, want)
		}
	}
	if want := fmt.Sprintf("... and %d more", total-reportMaxLines); lines[reportMaxLines] != want {
		t.Errorf("the last line is %q, want %q", lines[reportMaxLines], want)
	}
}

// ghStub is a fake `gh` on PATH: it logs every call's argv and, for a
// --body-file call, its stdin, and answers `issue list` with GH_STUB_OPEN.
const ghStub = `#!/usr/bin/env bash
set -eu
{ echo "--- call"; printf '%s\n' "$@"; } >> "$GH_STUB_LOG"
for a in "$@"; do
  if [ "$a" = "--body-file" ]; then
    echo "--- stdin" >> "$GH_STUB_LOG"
    cat >> "$GH_STUB_LOG"
  fi
done
if [ "${1:-} ${2:-}" = "issue list" ] && [ -n "${GH_STUB_OPEN:-}" ]; then
  echo "$GH_STUB_OPEN"
fi
`

type ghCall struct {
	args  []string
	stdin string
}

// runReport runs `report` against the stub and returns its stdout and the gh
// calls it made, in order. extra is appended to the environment (EVENT).
func runReport(t *testing.T, open, result, failures string, extra ...string) (string, []ghCall) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(ghStub), 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "gh.log")
	env := []string{
		"PATH=" + dir + string(os.PathListSeparator) + os.Getenv("PATH"),
		"GH_STUB_LOG=" + logPath,
		"GH_STUB_OPEN=" + open,
		"GITHUB_REPOSITORY=owner/repo",
		"RESULT=" + result,
		"RUN_URL=https://github.com/owner/repo/actions/runs/42",
		"SHA=abc123",
		"FAILURES=" + failures,
	}
	env = append(env, extra...)
	out, code := realDataReport(t, env, "report")
	if code != 0 {
		t.Fatalf("report exited %d, want 0 (stdout %q)", code, out)
	}
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("the stub gh was never called: %v", err)
	}
	var calls []ghCall
	for _, chunk := range strings.Split(string(raw), "--- call\n")[1:] {
		argv, stdin, _ := strings.Cut(chunk, "--- stdin\n")
		calls = append(calls, ghCall{args: strings.Split(strings.TrimSuffix(argv, "\n"), "\n"), stdin: stdin})
	}
	return out, calls
}

var reportIssueList = []string{"issue", "list", "--repo", "owner/repo", "--label", "ci-real-data",
	"--state", "open", "--json", "number", "--jq", "sort_by(.number) | .[0].number // empty"}

func wantGHCalls(t *testing.T, got []ghCall, want ...[]string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("gh was called %d times, want %d: %q", len(got), len(want), got)
	}
	for i := range want {
		if !slices.Equal(got[i].args, want[i]) {
			t.Errorf("gh call %d was\n%q\nwant\n%q", i, got[i].args, want[i])
		}
	}
}

func TestRealDataReportSuccessWithNoOpenIssue(t *testing.T) {
	t.Parallel()
	out, calls := runReport(t, "", "success", "")
	wantGHCalls(t, calls, reportIssueList)
	if !strings.Contains(out, "no open ci-real-data issue to close") {
		t.Errorf("stdout %q does not say there was nothing to close", out)
	}
}

func TestRealDataReportSuccessClosesTheOpenIssue(t *testing.T) {
	t.Parallel()
	_, calls := runReport(t, "7", "success", "")
	wantGHCalls(t, calls, reportIssueList,
		[]string{"issue", "close", "7", "--repo", "owner/repo", "--comment",
			"The real-data tests passed at abc123: https://github.com/owner/repo/actions/runs/42"})
}

func TestRealDataReportFailureOpensAnIssue(t *testing.T) {
	t.Parallel()
	failures := "--- FAIL: TestRealDataTree (12.00s)\nFAIL\tgithub.com/x/pkg\t12.1s"
	_, calls := runReport(t, "", "failure", failures, "EVENT=push")
	wantGHCalls(t, calls, reportIssueList,
		[]string{"label", "create", "ci-real-data", "--repo", "owner/repo", "--color", "B60205",
			"--description", "The real-data test run (real-data.yml) is failing", "--force"},
		[]string{"issue", "create", "--repo", "owner/repo", "--title", "Real-data tests are failing on main",
			"--label", "ci-real-data", "--body-file", "-"})
	body := calls[2].stdin
	for _, want := range []string{
		"triggered by `push`) ended `failure` at abc123",
		"Run: https://github.com/owner/repo/actions/runs/42",
		"```\n" + failures + "\n```\n",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the issue body does not contain %q:\n%s", want, body)
		}
	}
}

func TestRealDataReportFailureCommentsOnTheOpenIssue(t *testing.T) {
	t.Parallel()
	_, calls := runReport(t, "7", "cancelled", "")
	wantGHCalls(t, calls, reportIssueList,
		[]string{"issue", "comment", "7", "--repo", "owner/repo", "--body-file", "-"})
	body := calls[1].stdin
	for _, want := range []string{
		// No EVENT in the environment: the body says so rather than guessing.
		"triggered by `unknown`) ended `cancelled` at abc123",
		"```\n(no summary was produced; see the run)\n```\n",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the comment body does not contain %q:\n%s", want, body)
		}
	}
}

// A panic message can quote a fence of its own; the body's fence must be longer
// than any backtick run in it, or the content closes the block early.
func TestRealDataReportFenceOutlastsTheContent(t *testing.T) {
	t.Parallel()
	failures := "panic: unexpected\n```\nquoted\n````\n--- FAIL: TestX (0.00s)"
	_, calls := runReport(t, "7", "failure", failures)
	body := calls[1].stdin
	want := "`````\n" + failures + "\n`````\n"
	if !strings.Contains(body, want) {
		t.Errorf("the body does not wrap the failures in a five-backtick fence:\n%s", body)
	}
}
