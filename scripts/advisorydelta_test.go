package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// Tests of .github/scripts/advisory-delta.sh (the advisories a pull request
// introduces, for check.yml's job summary and ai-verify's context) and of the
// ADVISORIES_FILE arm of .github/scripts/ai-verify.sh that consumes it. They live
// here because the go tool ignores dot-directories.

// advisoryDelta runs advisory-delta.sh in dir ("" for the test's own) with args
// and extra environment, returning stdout and the exit status.
func advisoryDelta(t *testing.T, dir string, env []string, args ...string) (string, int) {
	t.Helper()
	requireTools(t, "bash", "git", "tar", "iconv")
	// STATUS and NOKEYS steer TestAdvisoryDeltaKeys' stub metacheck, so an
	// inherited value of either must not reach it.
	return runScript(t, dir, "advisory-delta.sh", []string{"MAX_LINES", "MAX_WIDTH", "PROFILE", "STATUS", "NOKEYS"}, env, args...)
}

// keysFile writes lines (one key each) to a temp file and returns its path.
func keysFile(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "keys.txt")
	body := strings.Join(lines, "\n")
	if len(lines) > 0 {
		body += "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

const (
	orphanP  = "orphan-person\tpeople: entry p: person \"p\" is credited by no work, recording or series: an orphan record"
	orphanQ  = "orphan-person\tpeople: entry q: person \"q\" is credited by no work, recording or series: an orphan record"
	groupAB  = "normalized-duplicate-works\tworks: entry a: work \"a\" normalizes to the same title and authors as \"b\" (\"A\"): the same book under two spellings of its title"
	groupABC = "normalized-duplicate-works\tworks: entry a: work \"a\" normalizes to the same title and authors as \"b\", \"c\" (\"A\"): the same book under two spellings of its title"
	groupBC  = "normalized-duplicate-works\tworks: entry b: work \"b\" normalizes to the same title and authors as \"c\" (\"B: Decorated\"): the same book under two spellings of its title"
	seriesN2 = "duplicate-series\tseries: entry s-abridged: series \"s-abridged\" (\"S (abridged)\") lists 2 works, every one at the same position in \"s\": a duplicate series (a recording variant or a second spelling)"
	seriesN3 = "duplicate-series\tseries: entry s-abridged: series \"s-abridged\" (\"S (abridged)\") lists 3 works, every one at the same position in \"s\": a duplicate series (a recording variant or a second spelling)"
	oversize = "oversized-entry\tworks: entry big: entry is 300000 bytes, over the 262144-byte pack target: an oversized entry cannot be split and keeps its pack over target"
)

func TestAdvisoryDeltaDiff(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		base, head []string
		want       string
	}{
		{name: "nothing new is no output", base: []string{orphanP, groupAB}, head: []string{groupAB, orphanP}},
		{name: "an advisory the head lost is not news", base: []string{orphanP, groupAB}, head: []string{groupAB}},
		{
			name: "a new advisory is listed under a header counting its class",
			base: []string{groupAB}, head: []string{groupAB, orphanQ},
			want: "1 new advisory (orphan-person 1):\n- [orphan-person] " + strings.SplitN(orphanQ, "\t", 2)[1] + "\n",
		},
		{
			// A work joining an existing duplicate group changes the group's text
			// AND names a record the base group did not: it is introduced.
			name: "a work joining a group is introduced",
			base: []string{groupAB}, head: []string{groupABC},
			want: "1 new advisory (normalized-duplicate-works 1):\n- [normalized-duplicate-works] " + strings.SplitN(groupABC, "\t", 2)[1] + "\n",
		},
		{
			// A repair folding the old lead away re-leads the group under another
			// record and quotes another title, but names nothing the base did not.
			name: "a group a repair shrank and re-led is not",
			base: []string{groupABC}, head: []string{groupBC},
		},
		{name: "a duplicate series gaining a volume is not", base: []string{seriesN2}, head: []string{seriesN3}},
		{name: "the slugs must be covered within the same class", base: []string{orphanP}, head: []string{orphanP, "honorific-person-pair\tpeople: entry dr-p: person \"p\" differs from \"q\" only by a courtesy title"},
			want: "1 new advisory (honorific-person-pair 1):\n- [honorific-person-pair] people: entry dr-p: person \"p\" differs from \"q\" only by a courtesy title\n"},
		{name: "an oversized entry is never listed", base: nil, head: []string{oversize}},
		{
			// The ENTRY is a record the key names even when its message does not
			// quote it: a cross-language recording on another work is new.
			name: "a key's entry counts as a record it names",
			base: []string{"cross-language-recording\tworks: entry y: recording \"r\" is in \"pt\" but its work is in \"en\": a translation is a different work"},
			head: []string{"cross-language-recording\tworks: entry x: recording \"r\" is in \"pt\" but its work is in \"en\": a translation is a different work"},
			want: "1 new advisory (cross-language-recording 1):\n- [cross-language-recording] works: entry x: recording \"r\" is in \"pt\" but its work is in \"en\": a translation is a different work\n",
		},
		{
			// Quoted tokens that are not records (language codes) are one more name a
			// base advisory must hold: the same recording of the same work in a NEW
			// language pair is listed, not covered by the old pair.
			name: "a quoted language code makes a key harder to cover, never easier",
			base: []string{"cross-language-recording\tworks: entry x: recording \"r\" is in \"pt\" but its work is in \"en\": a translation is a different work"},
			head: []string{"cross-language-recording\tworks: entry x: recording \"r\" is in \"de\" but its work is in \"en\": a translation is a different work"},
			want: "1 new advisory (cross-language-recording 1):\n- [cross-language-recording] works: entry x: recording \"r\" is in \"de\" but its work is in \"en\": a translation is a different work\n",
		},
		{
			// A key naming no record (no entry locator, nothing slug-shaped quoted)
			// would be a subset of every same-class base advisory: it is listed.
			name: "a key naming no record is never covered",
			base: []string{orphanP},
			head: []string{orphanP, "orphan-person\tpeople: an orphan record with no locator"},
			want: "1 new advisory (orphan-person 1):\n- [orphan-person] people: an orphan record with no locator\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, code := advisoryDelta(t, "", nil, "diff", keysFile(t, tc.base...), keysFile(t, tc.head...))
			if code != 0 || got != tc.want {
				t.Errorf("exit %d, output:\n%s\nwant:\n%s", code, got, tc.want)
			}
		})
	}
}

// The list is bounded twice - lines, then bytes per line on a UTF-8 boundary - and
// the header still counts everything, so a cut list says how large the delta was.
func TestAdvisoryDeltaDiffIsBounded(t *testing.T) {
	t.Parallel()
	var head []string
	for _, id := range []string{"p1", "p2", "p3", "p4", "p5"} {
		head = append(head, "orphan-person\tpeople: entry "+id+": person \""+id+"\" called "+strings.Repeat("é", 40)+": an orphan record")
	}
	got, code := advisoryDelta(t, "", []string{"MAX_LINES=2", "MAX_WIDTH=61"}, "diff", keysFile(t), keysFile(t, head...))
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	if len(lines) != 4 || lines[0] != "5 new advisories (orphan-person 5):" || lines[3] != "- ... and 3 more not shown" {
		t.Fatalf("bounded output:\n%s", got)
	}
	for _, l := range lines[1:3] {
		if !utf8.ValidString(l) || !strings.HasSuffix(l, "...") || len(l) > 61+len("...") {
			t.Errorf("line %q: want a valid UTF-8 cut at 61 bytes plus an ellipsis", l)
		}
	}
}

func TestAdvisoryDeltaDiffNeedsBothFiles(t *testing.T) {
	t.Parallel()
	if _, code := advisoryDelta(t, "", nil, "diff", keysFile(t), filepath.Join(t.TempDir(), "absent")); code != 2 {
		t.Errorf("a missing head file: exit %d, want 2", code)
	}
	if _, code := advisoryDelta(t, "", nil, "nonsense"); code != 2 {
		t.Errorf("an unknown command: exit %d, want 2", code)
	}
}

// A comparison that fails is a failure, never an empty "no new advisories": with
// an awk that cannot run, the head's new orphan must not read as nothing new.
func TestAdvisoryDeltaDiffFailsLoudly(t *testing.T) {
	t.Parallel()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "awk"), []byte("#!/bin/sh\necho 'awk: broken' >&2\nexit 2\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, code := advisoryDelta(t, "", []string{"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH")},
		"diff", keysFile(t), keysFile(t, orphanQ))
	if code == 0 || got != "" {
		t.Errorf("a failed comparison: exit %d, output %q; want a non-zero exit and no list", code, got)
	}
}

// `keys` extracts data/ of the named REVISION, not the working tree, runs the given
// metacheck over it with --advisory-keys, and succeeds exactly when keys were
// written - a tree with problems (exit 1) still has a census.
func TestAdvisoryDeltaKeys(t *testing.T) {
	t.Parallel()
	repo, git, write := gitFixture(t)
	write("data/advisories.txt", "orphan-person\tpeople: entry old: base\n")
	git("add", ".")
	git("commit", "-q", "-m", "base")
	base := strings.TrimSpace(git("rev-parse", "HEAD"))
	write("data/advisories.txt", "orphan-person\tpeople: entry new: head\n")
	git("commit", "-q", "-am", "head")

	// A stub metacheck: it copies the tree's advisories.txt to the --advisory-keys
	// path (or writes nothing when NOKEYS is set) and exits with STATUS.
	stub := filepath.Join(t.TempDir(), "metacheck")
	if err := os.WriteFile(stub, []byte(`#!/usr/bin/env bash
data= out=
while [ $# -gt 0 ]; do
  case "$1" in
    -data) data="$2"; shift 2 ;;
    --advisory-keys) out="$2"; shift 2 ;;
    *) shift ;;
  esac
done
[ -n "${NOKEYS:-}" ] || cp "$data/advisories.txt" "$out"
echo "stub log line" >&2
exit "${STATUS:-0}"
`), 0o755); err != nil {
		t.Fatal(err)
	}

	run := func(env []string, rev string) (string, int) {
		t.Helper()
		out := filepath.Join(t.TempDir(), "keys.txt")
		if _, code := advisoryDelta(t, repo, env, "keys", stub, rev, out); code != 0 {
			return "", code
		}
		return readFile(t, out), 0
	}
	if got, code := run(nil, base); code != 0 || !strings.Contains(got, "entry old") {
		t.Errorf("base revision: exit %d, keys %q; want the base tree's", code, got)
	}
	if got, code := run([]string{"STATUS=1"}, "HEAD"); code != 0 || !strings.Contains(got, "entry new") {
		t.Errorf("a tree with problems: exit %d, keys %q; want its keys and success", code, got)
	}
	if _, code := run([]string{"NOKEYS=1", "STATUS=1"}, "HEAD"); code == 0 {
		t.Error("no keys written: want a failure, not an empty census")
	}
	if _, code := run(nil, "no-such-rev"); code == 0 {
		t.Error("an unknown revision: want a failure")
	}
}

// `summary` renders the list for the job's step summary, in a fence longer than
// any backtick run the list holds, or says there is none.
func TestAdvisoryDeltaSummary(t *testing.T) {
	t.Parallel()
	list := keysFile(t, "1 new advisory (orphan-person 1):", "- [orphan-person] people: entry p: titled \"A ```` B\"")
	got, code := advisoryDelta(t, "", nil, "summary", list)
	if code != 0 || !strings.HasPrefix(got, "### Advisories this pull request introduces\n\n`````\n") ||
		!strings.HasSuffix(got, "\n`````\n") {
		t.Errorf("summary of a list (exit %d):\n%s", code, got)
	}
	got, code = advisoryDelta(t, "", nil, "summary", keysFile(t))
	if code != 0 || !strings.Contains(got, "None:") || strings.Contains(got, "```") {
		t.Errorf("summary of nothing (exit %d):\n%s", code, got)
	}
}

// ai-verify.sh with ADVISORIES_FILE: unset or empty, neither the system prompt nor
// the message mentions advisories (a run that computed no advisory delta); set,
// both do, the list arriving as its own section after the change.
func TestAIVerifyAdvisorySection(t *testing.T) {
	t.Parallel()
	requireTools(t, "bash", "jq", "iconv", "perl")
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	// A stub claude CLI recording the system prompt and stdin, answering a pass -
	// or, with REPLY_FILE set, that file's contents.
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(`#!/usr/bin/env bash
while [ $# -gt 0 ]; do
  case "$1" in
    --system-prompt) printf '%s' "$2" > "$OUTDIR/system.txt"; shift 2 ;;
    *) shift ;;
  esac
done
cat > "$OUTDIR/stdin.txt"
if [ -n "${REPLY_FILE:-}" ]; then cat "$REPLY_FILE"; exit 0; fi
printf '%s' '{"result":"{\"verdict\":\"pass\",\"findings\":[]}"}'
`), 0o755); err != nil {
		t.Fatal(err)
	}
	context := filepath.Join(dir, "context.txt")
	if err := os.WriteFile(context, []byte("+ work \"x\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	advisories := filepath.Join(dir, "advisories.txt")
	if err := os.WriteFile(advisories, []byte("1 new advisory (orphan-person 1):\n- [orphan-person] people: entry p: an orphan record\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(dir, "empty.txt")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	var lastOut string
	run := func(advisoryFile string, env ...string) (system, stdin string) {
		t.Helper()
		out := t.TempDir()
		lastOut = out
		cmd := exec.Command("bash", abs(t, filepath.Join("..", ".github", "scripts", "ai-verify.sh")),
			context, filepath.Join(out, "verdict.json"), filepath.Join(out, "comment.md"))
		cmd.Env = append([]string{"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"), "HOME=" + out,
			"CLAUDE_CODE_OAUTH_TOKEN=test", "OUTDIR=" + out}, env...)
		if advisoryFile != "" {
			cmd.Env = append(cmd.Env, "ADVISORIES_FILE="+advisoryFile)
		}
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("ai-verify.sh: %v\n%s", err, b)
		}
		return readFile(t, filepath.Join(out, "system.txt")), readFile(t, filepath.Join(out, "stdin.txt"))
	}
	plainSystem, plainStdin := run("")
	for name, f := range map[string]string{"empty": empty, "absent": filepath.Join(dir, "absent.txt")} {
		if s, in := run(f); s != plainSystem || in != plainStdin {
			t.Errorf("%s ADVISORIES_FILE changed the prompt or the message", name)
		}
	}
	if strings.Contains(plainSystem, "ADVISORIES") || strings.Contains(plainStdin, "ADVISORIES") {
		t.Error("without an advisory file the prompt mentions advisories")
	}
	note := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(note, []byte("NOTE: the advisories could not be computed.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(note)
	if verdict := readFile(t, filepath.Join(lastOut, "verdict.json")); !strings.Contains(verdict, `"verdict":"pass"`) {
		t.Errorf("a NOTE must not force a flag: %s", verdict)
	}
	system, stdin := run(advisories)
	if verdict := readFile(t, filepath.Join(lastOut, "verdict.json")); !strings.Contains(verdict, `"verdict":"flag"`) ||
		!strings.Contains(verdict, "introduces metacheck advisories: 1 new advisory (orphan-person 1): [orphan-person] people: entry p: an orphan record.") {
		t.Errorf("a pass beside introduced advisories must become a flag carrying them: %s", verdict)
	}
	if !strings.Contains(system, "EVERY LISTED ADVISORY IS A FINDING") {
		t.Error("with advisories the system prompt does not say they are findings")
	}
	if !strings.HasPrefix(stdin, plainStdin) || !strings.Contains(stdin, "===== ADVISORIES THIS PULL REQUEST INTRODUCES") ||
		!strings.HasSuffix(strings.TrimSpace(stdin), "an orphan record") {
		t.Errorf("with advisories the message is:\n%s", stdin)
	}

	// A pass whose findings are not an array still becomes a flag that lists
	// them and the forced finding, never an empty verdict beside a bare comment.
	reply := filepath.Join(dir, "reply.json")
	if err := os.WriteFile(reply, []byte(`{"result":"{\"verdict\":\"pass\",\"findings\":\"none\"}"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	run(advisories, "REPLY_FILE="+reply)
	verdict := readFile(t, filepath.Join(lastOut, "verdict.json"))
	comment := readFile(t, filepath.Join(lastOut, "comment.md"))
	if !strings.Contains(verdict, `"verdict":"flag"`) || !strings.Contains(verdict, `"findings":["none","This pull request`) ||
		!strings.Contains(comment, "- This pull request introduces metacheck advisories") {
		t.Errorf("a pass with string findings beside advisories:\nverdict %s\ncomment %s", verdict, comment)
	}

	// A series-completion bot pull request is never flagged for its advisories
	// (its flag feeds an automated resolver, not a person): the prompt and the
	// message are what they are without the list, the verdict stays the model's,
	// and the list is shown in the comment as information.
	syncSystem, syncStdin := run("", "PR_ORIGIN=sync-bot")
	if s, in := run(advisories, "PR_ORIGIN=sync-bot"); s != syncSystem || in != syncStdin {
		t.Error("a sync-bot pull request's advisories changed the prompt or the message")
	}
	verdict = readFile(t, filepath.Join(lastOut, "verdict.json"))
	comment = readFile(t, filepath.Join(lastOut, "comment.md"))
	if !strings.Contains(verdict, `"verdict":"pass"`) || !strings.Contains(comment, "#### Advisories this pull request introduces") ||
		!strings.Contains(comment, "```\n1 new advisory (orphan-person 1):\n- [orphan-person] people: entry p: an orphan record\n```") {
		t.Errorf("a sync-bot pass beside advisories:\nverdict %s\ncomment %s", verdict, comment)
	}
	run(note, "PR_ORIGIN=sync-bot")
	if comment := readFile(t, filepath.Join(lastOut, "comment.md")); strings.Contains(comment, "Advisories") {
		t.Errorf("a sync-bot NOTE must add no advisory section: %s", comment)
	}
}
