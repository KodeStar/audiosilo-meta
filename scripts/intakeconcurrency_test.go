package scripts_test

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// Tests that intake.yml's concurrency group carries a verbatim copy of the intake
// job's `if`. GitHub keeps one PENDING run per group, so a skipped event must land
// in the `-noop` group and a real one in `run`: the group decides that by the
// same condition the job does, and a copy that drifts sends a real run into the
// group where a skipped event can replace it while it waits. Nothing else checks
// that the two agree.

// intakeGroupHead and intakeGroupTail are the group's text around the copied
// condition, whitespace removed.
const (
	intakeGroupHead = `${{github.event_name=='push'&&'intake-rebase'||format('intake-{0}-{1}',github.event.issue.number,(`
	intakeGroupTail = `)&&'run'||'noop')}}`
)

// foldedBlock returns the `>-` block scalar that follows the line equal to
// header (indentation included), with every whitespace character removed: the
// two copies are wrapped differently, and no label or event name holds a space.
func foldedBlock(lines []string, header string) (string, bool) {
	for i, l := range lines {
		if l != header {
			continue
		}
		indent := len(header) - len(strings.TrimLeft(header, " "))
		var b strings.Builder
		for _, body := range lines[i+1:] {
			if strings.TrimSpace(body) != "" && len(body)-len(strings.TrimLeft(body, " ")) <= indent {
				break
			}
			b.WriteString(strings.Join(strings.Fields(body), ""))
		}
		return b.String(), true
	}
	return "", false
}

// intakeConcurrencyCopiesIf reports how intake.yml's concurrency group and the
// intake job's `if` disagree, or nil when the group carries the condition whole.
func intakeConcurrencyCopiesIf(yml string) error {
	lines := strings.Split(yml, "\n")
	group, ok := foldedBlock(lines, "  group: >-")
	if !ok {
		return fmt.Errorf("no `  group: >-` block under concurrency")
	}
	cond, ok := foldedBlock(lines, "    if: >-")
	if !ok {
		return fmt.Errorf("no `    if: >-` block on the intake job")
	}
	if want := intakeGroupHead + cond + intakeGroupTail; group != want {
		return fmt.Errorf("the concurrency group is not the job's `if` copied whole:\n group %s\n want  %s", group, want)
	}
	return nil
}

func TestIntakeConcurrencyCopiesTheJobIf(t *testing.T) {
	yml := readFile(t, filepath.Join("..", ".github", "workflows", "intake.yml"))
	if err := intakeConcurrencyCopiesIf(yml); err != nil {
		t.Fatal(err)
	}
	// The check itself: a label changed in only one of the two copies is caught
	// either way round, and so is a missing block.
	const label = `github.event.label.name != 'data:duplicate'`
	if strings.Count(yml, label) != 2 {
		t.Fatalf("intake.yml names %q %d times, want 2 (the group and the job)", label, strings.Count(yml, label))
	}
	first := strings.Replace(yml, label, `github.event.label.name != 'data:other'`, 1)
	second := yml[:strings.LastIndex(yml, label)] + `github.event.label.name != 'data:other'` + yml[strings.LastIndex(yml, label)+len(label):]
	for name, mutated := range map[string]string{
		"the group alone changed": first,
		"the if alone changed":    second,
		"no group":                strings.Replace(yml, "  group: >-", "  group: x", 1),
	} {
		if intakeConcurrencyCopiesIf(mutated) == nil {
			t.Errorf("%s: the check passed", name)
		}
	}
}
