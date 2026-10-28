package scripts_test

import (
	"fmt"
	"os"
	"path/filepath"
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

// A failing verdict ends with the failing jobs' own output, the part of the
// log the sync bot reads as its evidence; a passing one prints none of it.
func TestCheckVerdictEndsWithTheFailedLogs(t *testing.T) {
	t.Parallel()
	logs := t.TempDir()
	for rel, body := range map[string]string{
		"check-log-metacheck/metacheck.log": "loading\nerror: works/0/a.json: entry a: the problem\n",
		"check-log-test-2/test.log":         "--- FAIL: TestX\nFAIL\tgithub.com/kodestar/audiosilo-meta/pkg/x\n",
	} {
		path := filepath.Join(logs, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
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
	if !strings.Contains(out, "check-log-metacheck/metacheck.log") {
		t.Errorf("a log is printed without naming it:\n%s", out)
	}

	out, code = verdict(t, needsOf(gate(nil)), logs, "advisories")
	if code != 0 || strings.Contains(out, "the problem") {
		t.Errorf("a passing verdict printed logs (exit %d):\n%s", code, out)
	}
}

func TestCheckVerdictUsage(t *testing.T) {
	t.Parallel()
	requireTools(t, "bash", "jq")
	if _, code := runScript(t, "", "check-verdict.sh", []string{"NEEDS"}, []string{"NEEDS=" + needsOf(gate(nil))}); code != 2 {
		t.Errorf("no log-dir argument exited %d, want 2", code)
	}
}
