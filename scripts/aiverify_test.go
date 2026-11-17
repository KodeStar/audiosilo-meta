package scripts_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Tests of PR_ORIGIN in .github/scripts/ai-verify.sh and of the gather step in
// .github/workflows/ai-verify.yml that sets it: who opened a pull request decides
// one paragraph of the prompt, and adding an origin must leave every other
// origin's prompt exactly as it was.

// stubClaude is a claude CLI that records the system prompt and stdin under
// $OUTDIR and answers a pass - or, with REPLY_FILE set, that file's contents.
const stubClaude = `#!/usr/bin/env bash
while [ $# -gt 0 ]; do
  case "$1" in
    --system-prompt) printf '%s' "$2" > "$OUTDIR/system.txt"; shift 2 ;;
    *) shift ;;
  esac
done
cat > "$OUTDIR/stdin.txt"
if [ -n "${REPLY_FILE:-}" ]; then cat "$REPLY_FILE"; exit 0; fi
printf '%s' '{"result":"{\"verdict\":\"pass\",\"findings\":[]}"}'
`

// stubBin writes the stub claude (and, with a non-empty today, a date answering
// it, so the prompt's "Today's date" line is fixed) into dir/bin.
func stubBin(t *testing.T, dir, today string) string {
	t.Helper()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(stubClaude), 0o755); err != nil {
		t.Fatal(err)
	}
	if today != "" {
		if err := os.WriteFile(filepath.Join(bin, "date"), []byte("#!/usr/bin/env bash\necho "+today+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return bin
}

// aiVerifyFixture is a temp dir holding stubBin(today) and a one-line context file.
func aiVerifyFixture(t *testing.T, today string) (dir, bin, context string) {
	t.Helper()
	dir = t.TempDir()
	bin = stubBin(t, dir, today)
	context = filepath.Join(dir, "context.txt")
	if err := os.WriteFile(context, []byte("+ work \"x\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, bin, context
}

// aiVerifyRun is what one run of ai-verify.sh left behind: the system prompt and
// message the stub claude was handed ("" when the CLI was not called), whether a
// stub curl was and the request body it read on stdin, and the verdict and
// comment files.
type aiVerifyRun struct {
	system, stdin, verdict, comment, request string
	curl                                     bool
}

// runAIVerify runs ai-verify.sh over context with bin (stubBin) first on PATH
// and nothing else inherited but PATH, so a PR_ORIGIN, CONTEXT_FORMAT or API key
// in the caller's environment cannot reach the prompt.
func runAIVerify(t *testing.T, bin, context string, env ...string) aiVerifyRun {
	t.Helper()
	out := t.TempDir()
	cmd := exec.Command("bash", abs(t, filepath.Join("..", ".github", "scripts", "ai-verify.sh")),
		context, filepath.Join(out, "verdict.json"), filepath.Join(out, "comment.md"))
	cmd.Env = append([]string{"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"), "HOME=" + out,
		"CLAUDE_CODE_OAUTH_TOKEN=test", "OUTDIR=" + out}, env...)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ai-verify.sh %v: %v\n%s", env, err, b)
	}
	// The stub claude writes system.txt and stdin.txt only when the CLI is the
	// transport; a missing one reads as "".
	system, _ := os.ReadFile(filepath.Join(out, "system.txt"))
	stdin, _ := os.ReadFile(filepath.Join(out, "stdin.txt"))
	_, curlErr := os.Stat(filepath.Join(out, "curl-called"))
	request, _ := os.ReadFile(filepath.Join(out, "request.json"))
	return aiVerifyRun{
		system: string(system), stdin: string(stdin), request: string(request),
		verdict: readFile(t, filepath.Join(out, "verdict.json")), comment: readFile(t, filepath.Join(out, "comment.md")),
		curl: curlErr == nil,
	}
}

// originCaution splits a system prompt around its origin caution: the sentences
// opening with lead, up to the one every origin shares.
func originCaution(t *testing.T, system, lead string) (before, caution, after string) {
	t.Helper()
	const shared = " So never flag a record as fabricated"
	before, rest, okLead := strings.Cut(system, lead)
	caution, after, okShared := strings.Cut(rest, shared)
	if !okLead || !okShared {
		t.Fatalf("no origin caution opening %q before %q:\n%s", lead, shared, system)
	}
	return before, lead + caution, shared + after
}

// The prompts of the origins that predate the LibriVox lane, with the date fixed
// at 2026-01-01 and no advisories, are pinned BYTE FOR BYTE: the hashes were taken
// from the script before the lane's origin was added. A deliberate change to the
// shared prompt changes them; replace them then, in the same commit, and say why.
var originPromptSHA256 = []struct{ origin, format, sha256 string }{
	{"contributor", "summary", "f336fe28add4c7a41fcba190a88bb7e25b81b1e1db349d5610ebd4b15a50f6a5"},
	{"contributor", "raw-diff", "2bda5bccd2e7c6a5eb9645cec94313d6336edbdfab9884dbfe88dfe86162a7ed"},
	{"sync-bot", "summary", "4f1ae63d38e9473f30da167c6530ddc1aa035b9fb07a63feecd4a51e4dfc8807"},
	{"sync-bot", "raw-diff", "590a421b06d22d1f1cd6a765af6b0e377efd58b5856727185e627ce27bdfd50d"},
}

func TestAIVerifyPromptByOrigin(t *testing.T) {
	t.Parallel()
	requireTools(t, "bash", "jq", "iconv", "perl")
	dir, bin, context := aiVerifyFixture(t, "2026-01-01")
	advisories := filepath.Join(dir, "advisories.txt")
	if err := os.WriteFile(advisories, []byte("1 new advisory (orphan-person 1):\n- [orphan-person] people: entry p: an orphan record\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(env ...string) aiVerifyRun {
		t.Helper()
		return runAIVerify(t, bin, context, env...)
	}

	for _, pin := range originPromptSHA256 {
		sum := sha256.Sum256([]byte(run("PR_ORIGIN="+pin.origin, "CONTEXT_FORMAT="+pin.format).system))
		if got := hex.EncodeToString(sum[:]); got != pin.sha256 {
			t.Errorf("the %s prompt (%s) changed: sha256 %s, want %s", pin.origin, pin.format, got, pin.sha256)
		}
	}

	const verifyASIN = "flag it as 'verify ASIN <asin> against the retailer listing'"
	const laneLead = "THIS PULL REQUEST WAS OPENED BY THE LIBRIVOX LANE"
	for _, format := range []string{"summary", "raw-diff"} {
		contributor := run("PR_ORIGIN=contributor", "CONTEXT_FORMAT="+format)
		lane := run("PR_ORIGIN=librivox-lane", "CONTEXT_FORMAT="+format)
		// An origin the script does not know, or none at all, is a contributor's:
		// the stricter reading.
		for _, env := range [][]string{{"PR_ORIGIN=something-new"}, nil} {
			if got := run(append(env, "CONTEXT_FORMAT="+format)...); got.system != contributor.system {
				t.Errorf("%s: origin %v does not read as a contributor's", format, env)
			}
		}
		// The lane changes the origin caution and nothing else: what stands
		// before and after it is the contributor's prompt.
		before, caution, after := originCaution(t, lane.system, laneLead)
		cBefore, _, cAfter := originCaution(t, contributor.system, "THIS PULL REQUEST WAS NOT OPENED BY THE SERIES-COMPLETION BOT")
		if before != cBefore || after != cAfter {
			t.Errorf("%s: the lane's prompt differs from a contributor's outside the origin caution", format)
		}
		for _, want := range []string{
			"librivox_id and its archive.org item (archive_id) are the evidence it exists",
			"NO ASIN, ISBN or retailer source is expected",
			"the display name they chose on LibriVox",
			"centuries before the recording is normal",
			"so is any language",
			"which are NEGATIVE for a year BC), and neither the stamp nor an ancient author's years is a finding",
			"A new work in no series, or a pull request that changes no series, is EXPECTED",
			"a new series record (a '+ SERIES' line) or any change to a series record is ALWAYS a finding",
		} {
			if !strings.Contains(caution, want) {
				t.Errorf("%s: the lane's caution lacks %q", format, want)
			}
		}
		if strings.Contains(lane.system, verifyASIN) || strings.Contains(lane.system, "SERIES-COMPLETION BOT") {
			t.Errorf("%s: the lane's prompt speaks for a retailer ASIN or the series-completion bot", format)
		}
		if strings.Contains(contributor.system, laneLead) {
			t.Errorf("%s: a contributor's prompt speaks for the LibriVox lane", format)
		}
		// The message is the change itself, whoever opened it.
		if lane.stdin != contributor.stdin {
			t.Errorf("%s: the lane's message differs from a contributor's", format)
		}
	}

	// Like a sync-bot pull request, a lane one is never flagged for its
	// advisories (its flag holds projects back with no resolver): the prompt
	// and message are what they are without the list, the verdict stays the
	// model's, and the list is shown in the comment as information.
	plain := run("PR_ORIGIN=librivox-lane")
	got := run("PR_ORIGIN=librivox-lane", "ADVISORIES_FILE="+advisories)
	if got.system != plain.system || got.stdin != plain.stdin {
		t.Error("a lane pull request's advisories changed the prompt or the message")
	}
	if !strings.Contains(got.verdict, `"verdict":"pass"`) || !strings.Contains(got.comment, "#### Advisories this pull request introduces") ||
		!strings.Contains(got.comment, "a LibriVox lane pull request is not flagged for them") ||
		!strings.Contains(got.comment, "- [orphan-person] people: entry p: an orphan record") {
		t.Errorf("a lane pass beside advisories:\nverdict %s\ncomment %s", got.verdict, got.comment)
	}
	if sync := run("PR_ORIGIN=sync-bot", "ADVISORIES_FILE="+advisories); !strings.Contains(sync.comment, "a series-completion bot pull request is not flagged for them") {
		t.Errorf("a sync-bot comment lost its wording: %s", sync.comment)
	}
}

// originSelection matches the gather step's origin selection in ai-verify.yml:
// the lines after the one reading the branch, up to the `fi` closing it.
var originSelection = regexp.MustCompile(`(?s)PR_BRANCH="\$\(gh pr view[^\n]*\n(.*?\n[ \t]*fi)\n`)

func originBlock(t *testing.T) string {
	t.Helper()
	m := originSelection.FindStringSubmatch(readFile(t, filepath.Join("..", ".github", "workflows", "ai-verify.yml")))
	if m == nil {
		t.Fatal("ai-verify.yml: no origin selection after the PR_BRANCH line")
	}
	return m[1]
}

// The gather step reads a `sync/librivox-` branch as the LibriVox lane BEFORE the
// sync-bot test, which its branch and its `bot-sync` label would also pass.
func TestAIVerifyWorkflowOrigin(t *testing.T) {
	t.Parallel()
	requireTools(t, "bash", "grep")
	block := originBlock(t)
	laneLabels := "data\nbot-intake\nbot-sync\nsync:librivox"
	for _, tc := range []struct{ branch, labels, want string }{
		{"sync/librivox-2026-11-12", laneLabels, "librivox-lane"},
		{"sync/librivox-2026-11-12", "", "librivox-lane"},
		{"sync/2026-11-12", "data\nbot-intake\nbot-sync", "sync-bot"},
		{"sync/2026-11-12", "", "sync-bot"},
		{"sync/intake-2026-11-12", "", "sync-bot"},
		{"feature/librivox-x", "bot-sync", "sync-bot"},
		{"intake/issue-123", "data\nbot-intake", "contributor"},
		{"librivox-2026-11-12", "sync:librivox", "contributor"},
		{"fix/sync/librivox-1", "", "contributor"},
	} {
		out := filepath.Join(t.TempDir(), "output")
		cmd := exec.Command("bash", "-c", "set -euo pipefail\n"+block)
		cmd.Env = append(os.Environ(), "PR_BRANCH="+tc.branch, "PR_LABELS="+tc.labels, "GITHUB_OUTPUT="+out)
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v\n%s", tc.branch, err, b)
		}
		if got := strings.TrimSpace(readFile(t, out)); got != "origin="+tc.want {
			t.Errorf("branch %q labels %q: %s, want origin=%s", tc.branch, tc.labels, got, tc.want)
		}
	}
}

// TestAIVerifyCommentSaysWhatTheVerdictDoes: a maintainer reviews a contributor's
// pull request whatever the verdict, so its comment calls the verdict advisory; a
// bot merges its own on a pass and never on a flag, so for a bot's pull request
// the comment must not call a flag "not a merge block".
func TestAIVerifyCommentSaysWhatTheVerdictDoes(t *testing.T) {
	t.Parallel()
	requireTools(t, "bash", "jq", "iconv", "perl")
	dir, bin, context := aiVerifyFixture(t, "2026-01-01")
	flag := filepath.Join(dir, "flag.json")
	if err := os.WriteFile(flag, []byte(`{"result":"{\"verdict\":\"flag\",\"findings\":[\"a finding\"]}"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	const advisory, botMerges = "advisory - not a merge block", "does not merge it while it is flagged"
	for _, tc := range []struct {
		origin string
		bot    bool
	}{{"contributor", false}, {"sync-bot", true}, {"librivox-lane", true}} {
		pass := runAIVerify(t, bin, context, "PR_ORIGIN="+tc.origin)
		flagged := runAIVerify(t, bin, context, "PR_ORIGIN="+tc.origin, "REPLY_FILE="+flag)
		if !strings.Contains(flagged.comment, "- a finding") {
			t.Fatalf("%s: the flag's finding is missing: %s", tc.origin, flagged.comment)
		}
		if got := strings.Contains(flagged.comment, botMerges); got != tc.bot {
			t.Errorf("%s: flag comment names the bot's merge block = %v:\n%s", tc.origin, got, flagged.comment)
		}
		if got := strings.Contains(flagged.comment, advisory); got == tc.bot {
			t.Errorf("%s: flag comment calls itself advisory = %v:\n%s", tc.origin, got, flagged.comment)
		}
		if got := strings.Contains(pass.comment, "a maintainer still reviews before merge"); got == tc.bot {
			t.Errorf("%s: pass comment promises a maintainer's review = %v:\n%s", tc.origin, got, pass.comment)
		}
	}
}

// argLimit is the head of a stub that refuses what Linux's execve refuses: one
// argument over MAX_ARG_STRLEN (128 KiB with its NUL). macOS has no such limit,
// so without it a request passed as an argument would only fail on the runners.
const argLimit = `#!/usr/bin/env bash
for a in "$@"; do
  if [ "${#a}" -gt 131071 ]; then echo "$0: Argument list too long (${#a} bytes)" >&2; exit 126; fi
done
`

// stubCurl answers the Messages API with a pass, notes that it was called and
// keeps the request body it read on stdin.
const stubCurl = argLimit + `touch "$OUTDIR/curl-called"
cat > "$OUTDIR/request.json"
printf '%s' '{"content":[{"type":"text","text":"{\"verdict\":\"pass\",\"findings\":[]}"}]}'
`

// TestAIVerifyPrefersTheAPIKey: with both credentials set the API key is used
// (billed per pull request, not the subscription meta-sync's resolver shares),
// and with the subscription token alone the CLI still is.
func TestAIVerifyPrefersTheAPIKey(t *testing.T) {
	t.Parallel()
	requireTools(t, "bash", "jq", "iconv", "perl")
	_, bin, context := aiVerifyFixture(t, "2026-01-01")
	if err := os.WriteFile(filepath.Join(bin, "curl"), []byte(stubCurl), 0o755); err != nil {
		t.Fatal(err)
	}
	// runAIVerify always sets the subscription token.
	if r := runAIVerify(t, bin, context, "ANTHROPIC_API_KEY=key"); !r.curl || r.stdin != "" || !strings.Contains(r.verdict, `"verdict":"pass"`) {
		t.Errorf("both credentials: curl %v, cli %v, verdict %s; want the API alone", r.curl, r.stdin != "", r.verdict)
	}
	if r := runAIVerify(t, bin, context); r.curl || r.stdin == "" {
		t.Errorf("the subscription token alone: curl %v, cli %v; want the CLI alone", r.curl, r.stdin != "")
	}
}

// TestAIVerifyAPIRequestIsNeverAnArgument: on the API key transport the prompt and
// the message reach jq as files and the request reaches curl on stdin. A summary
// runs to recorddiff.DefaultMaxBytes (180 kB), past the 128 KiB Linux allows one
// argument, where jq or curl handed it as one does not start and the run is a skip.
func TestAIVerifyAPIRequestIsNeverAnArgument(t *testing.T) {
	t.Parallel()
	requireTools(t, "bash", "jq", "iconv", "perl")
	realJQ, err := exec.LookPath("jq")
	if err != nil {
		t.Fatal(err)
	}
	dir, bin, context := aiVerifyFixture(t, "2026-01-01")
	for name, body := range map[string]string{"curl": stubCurl, "jq": argLimit + `exec "` + realJQ + `" "$@"` + "\n"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	const line = "+ work \"a title\" by an-author [en]\n"
	summary := strings.Repeat(line, 150_000/len(line))
	if err := os.WriteFile(context, []byte(summary), 0o644); err != nil {
		t.Fatal(err)
	}
	advisories := filepath.Join(dir, "advisories.txt")
	if err := os.WriteFile(advisories, []byte("1 new advisory (orphan-person 1):\n- [orphan-person] people: entry p: an orphan record\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := runAIVerify(t, bin, context, "ANTHROPIC_API_KEY=key", "ADVISORIES_FILE="+advisories)
	var req struct {
		Model     string `json:"model"`
		MaxTokens int    `json:"max_tokens"`
		System    string `json:"system"`
		Messages  []struct{ Role, Content string }
	}
	if err := json.Unmarshal([]byte(r.request), &req); err != nil {
		t.Fatalf("the request curl read on stdin is not JSON (%v): %.200s", err, r.request)
	}
	if req.Model == "" || req.MaxTokens == 0 || !strings.Contains(req.System, "You are a careful data reviewer") {
		t.Errorf("request model %q, max_tokens %d, system %.80q", req.Model, req.MaxTokens, req.System)
	}
	if len(req.Messages) != 1 || req.Messages[0].Role != "user" {
		t.Fatalf("messages = %d, want the one user message", len(req.Messages))
	}
	if msg := req.Messages[0].Content; !strings.Contains(msg, strings.TrimSuffix(summary, "\n")) || !strings.Contains(msg, "[orphan-person] people: entry p") {
		t.Errorf("the message (%d bytes) does not carry the whole %d-byte summary and the advisories", len(msg), len(summary))
	}
}
