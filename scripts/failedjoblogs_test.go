package scripts_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Tests of .github/scripts/failed-job-logs.sh, the step of check.yml's `check`
// verdict that fetches the failing jobs' logs through the API, driven against a
// stub `gh` that serves a job listing (filtered by the script's own --jq,
// through the real jq) and one log file per job id.

// failedJobsStub answers `gh api --paginate <listing> --jq <expr>` by running
// the expression over $GH_STUB_JOBS (after recording the listing URL), and `gh
// api repos/.../actions/jobs/<id>/logs` with $GH_STUB_DIR/<id>.log, failing
// when there is none.
const failedJobsStub = `#!/usr/bin/env bash
set -eu
printf '%s\n' "$*" >> "$GH_STUB_DIR/calls"
url="" expr=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    api|--paginate) ;;
    --jq) expr="$2"; shift ;;
    *) url="$1" ;;
  esac
  shift
done
case "$url" in
  */jobs/*/logs)
    id="${url%/logs}"; id="${id##*/}"
    [ -f "$GH_STUB_DIR/$id.log" ] || { echo "HTTP 404" >&2; exit 1; }
    cat "$GH_STUB_DIR/$id.log" ;;
  */jobs\?*)
    [ -n "${GH_STUB_LIST_FAILS:-}" ] && { echo "HTTP 500" >&2; exit 1; }
    jq -r "$expr" "$GH_STUB_JOBS" ;;
  *) echo "unexpected call: $url" >&2; exit 1 ;;
esac
`

type stubJob struct {
	ID         int64   `json:"id"`
	Name       string  `json:"name"`
	Conclusion *string `json:"conclusion"`
}

func conclusion(s string) *string { return &s }

// runFailedJobLogs runs the script against the stub with the given listing and
// per-id logs, returning the files it wrote, its stdout, its exit status and
// the gh calls it made.
func runFailedJobLogs(t *testing.T, jobs []stubJob, logs map[int64]string, env []string, args ...string) (map[string]string, string, int, []string) {
	t.Helper()
	requireTools(t, "bash", "jq", "sed", "awk")
	stub := t.TempDir()
	if err := os.WriteFile(filepath.Join(stub, "gh"), []byte(failedJobsStub), 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]any{"total_count": len(jobs), "jobs": jobs})
	if err != nil {
		t.Fatal(err)
	}
	listing := filepath.Join(stub, "jobs.json")
	if err := os.WriteFile(listing, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	for id, body := range logs {
		if err := os.WriteFile(filepath.Join(stub, jsonID(id)+".log"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out := filepath.Join(t.TempDir(), "logs")
	base := []string{
		"PATH=" + stub + string(os.PathListSeparator) + os.Getenv("PATH"),
		"GH_STUB_DIR=" + stub,
		"GH_STUB_JOBS=" + listing,
		"REPO=owner/repo", "RUN_ID=42", "RUN_ATTEMPT=2", "RETRY_SLEEP=0",
	}
	stdout, code := runScript(t, "", "failed-job-logs.sh",
		[]string{"GH_TOKEN", "REPO", "RUN_ID", "RUN_ATTEMPT", "RETRY_SLEEP", "GH_STUB_LIST_FAILS"},
		append(base, env...), append([]string{out}, args...)...)
	files := map[string]string{}
	entries, _ := os.ReadDir(out)
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(out, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		files[e.Name()] = string(b)
	}
	var calls []string
	if b, err := os.ReadFile(filepath.Join(stub, "calls")); err == nil {
		calls = strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	}
	return files, stdout, code, calls
}

func jsonID(id int64) string {
	b, _ := json.Marshal(id)
	return string(b)
}

// jobLog renders a log the way the API serves one: a BOM, a timestamp on every
// line, the failing step's output, then the post-job steps.
func jobLog(lines ...string) string {
	var b strings.Builder
	b.WriteString("\ufeff")
	for _, l := range append([]string{"Current runner version: '2.338.2'", "##[group]Run go test"}, lines...) {
		b.WriteString("2026-11-03T18:31:48.6464898Z " + l + "\n")
	}
	for _, l := range []string{"##[error]Process completed with exit code 1.", "Post job cleanup.",
		"[command]/usr/bin/git version", "Cleaning up orphan processes"} {
		b.WriteString("2026-11-03T18:33:13.6660981Z " + l + "\n")
	}
	return b.String()
}

var verdictJobs = []string{"changes", "build", "test", "metacheck", "metafmt", "advisories"}

// Only the jobs failing in the listed attempt, only the ones the verdict waits
// on, each under the file name check-verdict.sh reads.
func TestFailedJobLogsFetchesTheCurrentAttemptsFailures(t *testing.T) {
	t.Parallel()
	jobs := []stubJob{
		{1, "changes", conclusion("success")},
		{2, "build", conclusion("failure")},
		// Failed in attempt 1, passed in this one: the stale log the artifacts
		// used to carry.
		{3, "test (1)", conclusion("success")},
		{4, "test (2)", conclusion("failure")},
		{5, "test (3)", conclusion("cancelled")},
		{6, "metacheck", conclusion("timed_out")},
		{7, "metafmt", conclusion("skipped")},
		// Not a job the verdict waits on.
		{8, "lint", conclusion("failure")},
		// The verdict itself, still running.
		{9, "check", nil},
	}
	logs := map[int64]string{}
	for _, j := range jobs {
		logs[j.ID] = jobLog("output of " + j.Name)
	}
	files, _, code, calls := runFailedJobLogs(t, jobs, logs, nil, verdictJobs...)
	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	var names []string
	for n := range files {
		names = append(names, n)
	}
	slices.Sort(names)
	if want := []string{"build.log", "metacheck.log", "test-2.log", "test-3.log"}; !slices.Equal(names, want) {
		t.Errorf("wrote %v, want %v", names, want)
	}
	if len(calls) == 0 || !strings.Contains(calls[0], "repos/owner/repo/actions/runs/42/attempts/2/jobs?per_page=100") {
		t.Errorf("the listing is not this run's current attempt: %v", calls)
	}
	if got := files["test-2.log"]; !strings.Contains(got, "output of test (2)") {
		t.Errorf("test-2.log holds %q", got)
	}
}

// A log is the job's output with the timestamps and the BOM removed, ending
// with the failing step: the post-job steps would otherwise fill the tail the
// verdict prints.
func TestFailedJobLogsTrimsTheLog(t *testing.T) {
	t.Parallel()
	files, _, code, _ := runFailedJobLogs(t,
		[]stubJob{{2, "build", conclusion("failure")}},
		map[int64]string{2: jobLog("pkg/x/x.go:3:1: undefined: y")}, nil, verdictJobs...)
	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	want := "Current runner version: '2.338.2'\n##[group]Run go test\npkg/x/x.go:3:1: undefined: y\n" +
		"##[error]Process completed with exit code 1.\n"
	if got := files["build.log"]; got != want {
		t.Errorf("build.log =\n%q\nwant\n%q", got, want)
	}
}

// A log that cannot be fetched still leaves a file saying so, so the verdict
// prints something under the job's name; a listing that cannot be read is a
// failure of the step (which the workflow does not let decide the verdict).
func TestFailedJobLogsWhenTheAPIFails(t *testing.T) {
	t.Parallel()
	jobs := []stubJob{{2, "build", conclusion("failure")}, {4, "test (2)", conclusion("failure")}}
	files, _, code, calls := runFailedJobLogs(t, jobs, map[int64]string{4: jobLog("--- FAIL: TestX")}, nil, verdictJobs...)
	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	if got := files["build.log"]; !strings.Contains(got, "could not be fetched") {
		t.Errorf("build.log = %q, want the note that it could not be fetched", got)
	}
	if !strings.Contains(files["test-2.log"], "--- FAIL: TestX") {
		t.Errorf("one failed fetch lost the next job's log: %q", files["test-2.log"])
	}
	tries := 0
	for _, c := range calls {
		if strings.Contains(c, "actions/jobs/2/logs") {
			tries++
		}
	}
	if tries != 3 {
		t.Errorf("fetched the missing log %d times, want 3 tries", tries)
	}

	if _, _, code, _ := runFailedJobLogs(t, jobs, nil, []string{"GH_STUB_LIST_FAILS=1"}, verdictJobs...); code != 1 {
		t.Errorf("a failed listing exited %d, want 1", code)
	}
}

func TestFailedJobLogsUsage(t *testing.T) {
	t.Parallel()
	if _, _, code, _ := runFailedJobLogs(t, nil, nil, nil); code != 2 {
		t.Errorf("no job names exited %d, want 2", code)
	}
	if _, _, code, _ := runFailedJobLogs(t, nil, nil, []string{"RUN_ATTEMPT="}, "build"); code != 2 {
		t.Errorf("no RUN_ATTEMPT exited %d, want 2", code)
	}
}

// End to end: what the fetch writes is what the verdict prints last, the
// failing step's line inside the tail and none of the post-job cleanup.
func TestFailedJobLogsFeedTheVerdict(t *testing.T) {
	t.Parallel()
	jobs := []stubJob{{4, "test (2)", conclusion("failure")}, {3, "test (1)", conclusion("success")}}
	logs := map[int64]string{4: jobLog("--- FAIL: TestNow"), 3: jobLog("--- FAIL: TestEarlierAttempt")}
	files, _, code, _ := runFailedJobLogs(t, jobs, logs, nil, verdictJobs...)
	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	dir := writeLogs(t, files)
	out, vcode := verdict(t, needsOf(gate(map[string]string{"test": "failure"})), dir, "advisories")
	if vcode != 1 {
		t.Fatalf("verdict exit %d, want 1:\n%s", vcode, out)
	}
	if !strings.Contains(out, "--- FAIL: TestNow") {
		t.Errorf("the failing shard's output is not in the verdict:\n%s", out)
	}
	for _, stale := range []string{"TestEarlierAttempt", "Post job cleanup.", "orphan processes"} {
		if strings.Contains(out, stale) {
			t.Errorf("the verdict printed %q:\n%s", stale, out)
		}
	}
}
