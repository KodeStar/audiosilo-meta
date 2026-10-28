package scripts_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Tests of .github/scripts/check-verdict.sh, the verdict of check.yml's
// aggregate `check` job - the check run branch protection and the sync bot's
// merge gate read. Both directions matter equally: a red job must never be
// reported green, and the advisory job must never turn the verdict red.

func verdict(t *testing.T, needs, logdir string, advisory ...string) (string, int) {
	t.Helper()
	requireTools(t, "bash", "jq", "find", "tail")
	return runScript(t, "", "check-verdict.sh", []string{"NEEDS", "TAIL_LINES"},
		[]string{"NEEDS=" + needs}, append([]string{logdir}, advisory...)...)
}

// needsOf renders a needs context with the given results, in the shape
// `toJSON(needs)` gives it.
func needsOf(results map[string]string) string {
	var parts []string
	for job, result := range results {
		parts = append(parts, fmt.Sprintf(`%q: {"result": %q, "outputs": {}}`, job, result))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func gate(overrides map[string]string) map[string]string {
	r := map[string]string{
		"changes": "success", "build": "success", "test": "success",
		"metacheck": "success", "metafmt": "success", "advisories": "success",
	}
	for k, v := range overrides {
		r[k] = v
	}
	return r
}

func TestCheckVerdictPasses(t *testing.T) {
	t.Parallel()
	for name, results := range map[string]map[string]string{
		"every job succeeded": gate(nil),
		// The advisory job is never the verdict, whatever happened to it.
		"the advisory job failed":  gate(map[string]string{"advisories": "failure"}),
		"the advisory job skipped": gate(map[string]string{"advisories": "skipped"}),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			out, code := verdict(t, needsOf(results), t.TempDir(), "advisories")
			if code != 0 {
				t.Fatalf("exit %d, want 0:\n%s", code, out)
			}
			if !strings.Contains(out, "every required job passed") {
				t.Errorf("no pass line:\n%s", out)
			}
		})
	}
}

func TestCheckVerdictFails(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		needs    string
		advisory []string
	}{
		"a job failed":        {needs: needsOf(gate(map[string]string{"metafmt": "failure"})), advisory: []string{"advisories"}},
		"a job was skipped":   {needs: needsOf(gate(map[string]string{"build": "skipped"})), advisory: []string{"advisories"}},
		"a job was cancelled": {needs: needsOf(gate(map[string]string{"test": "cancelled"})), advisory: []string{"advisories"}},
		// Not naming the advisory job makes it count like any other.
		"an unnamed advisory job failed":   {needs: needsOf(gate(map[string]string{"advisories": "failure"}))},
		"an empty needs context":           {needs: "", advisory: []string{"advisories"}},
		"a needs context with no job":      {needs: "{}", advisory: []string{"advisories"}},
		"a needs context that is not JSON": {needs: "{not json", advisory: []string{"advisories"}},
		"a result that is not a word":      {needs: `{"check": {"result": null}}`},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			out, code := verdict(t, tc.needs, t.TempDir(), tc.advisory...)
			if code != 1 {
				t.Fatalf("exit %d, want 1:\n%s", code, out)
			}
			if strings.Contains(out, "every required job passed") {
				t.Errorf("a failing verdict printed the pass line:\n%s", out)
			}
		})
	}
}

// writeLogs writes the logs the verdict's merged download holds: one flat
// directory, a file per job (check.yml's download-artifact merge-multiple).
func writeLogs(t *testing.T, logs map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range logs {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// A failing verdict ends with the failing jobs' own output, the part of the
// log the sync bot reads as its evidence; a passing one prints none of it.
func TestCheckVerdictEndsWithTheFailedLogs(t *testing.T) {
	t.Parallel()
	logs := writeLogs(t, map[string]string{
		"metacheck.log": "loading\nerror: works/0/a.json: entry a: the problem\n",
		"test-2.log":    "--- FAIL: TestX\nFAIL\tgithub.com/kodestar/audiosilo-meta/pkg/x\n",
	})
	out, code := verdict(t, needsOf(gate(map[string]string{"metacheck": "failure", "test": "failure"})), logs, "advisories")
	if code != 1 {
		t.Fatalf("exit %d, want 1:\n%s", code, out)
	}
	mc := strings.Index(out, "error: works/0/a.json: entry a: the problem")
	ts := strings.Index(out, "--- FAIL: TestX")
	if mc < 0 || ts < 0 {
		t.Fatalf("the failed logs are not in the output:\n%s", out)
	}
	if res := strings.Index(out, "metacheck: failure"); res < 0 || res > mc {
		t.Errorf("the results should come first and the logs last:\n%s", out)
	}
	if !strings.Contains(out, "===== metacheck.log,") || !strings.Contains(out, "===== test-2.log,") {
		t.Errorf("a log is printed without naming it:\n%s", out)
	}

	out, code = verdict(t, needsOf(gate(nil)), logs, "advisories")
	if code != 0 || strings.Contains(out, "the problem") {
		t.Errorf("a passing verdict printed logs (exit %d):\n%s", code, out)
	}
}

// Artifacts outlive the attempt that uploaded them, so after a re-run of the
// failed jobs the download still holds the log of a job that has passed since.
// Only the jobs failing NOW are evidence: a stale log printed last would be the
// tail the sync bot hands its resolver.
func TestCheckVerdictSkipsThePassedJobsLogs(t *testing.T) {
	t.Parallel()
	logs := writeLogs(t, map[string]string{
		"metacheck.log": "error: works/0/a.json: entry a: the real problem\n",
		"metafmt.log":   "works/0/b.json is not canonical (attempt 1, since fixed)\n",
		"test-3.log":    "--- FAIL: TestFlaky (attempt 1)\n",
		"build.log":     "",
	})
	out, code := verdict(t, needsOf(gate(map[string]string{"metacheck": "failure", "build": "failure"})), logs, "advisories")
	if code != 1 {
		t.Fatalf("exit %d, want 1:\n%s", code, out)
	}
	if !strings.Contains(out, "the real problem") {
		t.Errorf("the failing job's log is missing:\n%s", out)
	}
	for _, stale := range []string{"since fixed", "TestFlaky", "===== build.log"} {
		if strings.Contains(out, stale) {
			t.Errorf("printed %q, from a job that passed or a log that is empty:\n%s", stale, out)
		}
	}
}

// TAIL_LINES is shared between the logs, so two failing jobs both land in the
// sync bot's 200-line window, each cut to its share with a floor of 40.
func TestCheckVerdictSharesTheTailBudget(t *testing.T) {
	t.Parallel()
	var long strings.Builder
	for i := range 300 {
		fmt.Fprintf(&long, "line %03d\n", i)
	}
	logs := writeLogs(t, map[string]string{"metacheck.log": long.String(), "metafmt.log": long.String()})
	out, code := verdict(t, needsOf(gate(map[string]string{"metacheck": "failure", "metafmt": "failure"})), logs, "advisories")
	if code != 1 {
		t.Fatalf("exit %d, want 1:\n%s", code, out)
	}
	if got := strings.Count(out, "line 299"); got != 2 {
		t.Errorf("each log's last line should appear once per log, got %d", got)
	}
	if strings.Contains(out, "line 224") || !strings.Contains(out, "line 225") {
		t.Errorf("each of two logs should get 75 of the default 150 lines:\n%s", out)
	}
	if !strings.Contains(out, "last 75 lines") {
		t.Errorf("the header does not state the share:\n%s", out)
	}
}

// check.yml's verdict job: it waits for every job that uploads a log for it,
// names only `advisories` as advisory, and runs under always() - a cancelled
// run under !cancelled() would SKIP it, and a skipped required check reads as
// a pass to branch protection and to audiosilo-meta-sync.
func TestCheckWorkflowVerdictWiring(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("..", ".github", "workflows", "check.yml"))
	if err != nil {
		t.Fatal(err)
	}
	job := regexp.MustCompile(`(?ms)^  check:\n(.*?)\n\n  [a-z#]`).FindSubmatch(raw)
	if job == nil {
		t.Fatal("found no `check:` job in check.yml")
	}
	body := string(job[1]) + "\n"
	m := regexp.MustCompile(`(?m)^    needs: \[([^\]]*)\]$`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("the check job has no one-line `needs: [...]`")
	}
	needs := map[string]bool{}
	for _, n := range strings.Split(m[1], ",") {
		needs[strings.TrimSpace(n)] = true
	}
	uploaders := regexp.MustCompile(`(?m)^          name: check-log-([a-z]+)`).FindAllStringSubmatch(string(raw), -1)
	if len(uploaders) == 0 {
		t.Fatal("found no `check-log-*` artifact in check.yml")
	}
	for _, u := range append(uploaders, []string{"", "changes"}, []string{"", "advisories"}) {
		if !needs[u[1]] {
			t.Errorf("the check job does not need %q, so the verdict cannot see it fail", u[1])
		}
	}
	if !strings.Contains(body, "    if: ${{ always() }}\n") {
		t.Error("the check job must run under `if: ${{ always() }}`")
	}
	if !strings.Contains(body, `check-verdict.sh "$RUNNER_TEMP/logs" advisories`+"\n") {
		t.Error("the verdict must name `advisories`, and only it, as advisory")
	}
}

func TestCheckVerdictUsage(t *testing.T) {
	t.Parallel()
	requireTools(t, "bash", "jq")
	if _, code := runScript(t, "", "check-verdict.sh", []string{"NEEDS"}, []string{"NEEDS=" + needsOf(gate(nil))}); code != 2 {
		t.Errorf("no log-dir argument exited %d, want 2", code)
	}
}
