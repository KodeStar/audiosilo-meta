#!/usr/bin/env bash
# ai-verify-reuse.sh - reuse the verdict of a pull request whose judged content
# has not changed.
#
# Usage: ai-verify-reuse.sh <context-file> <pr-number> <judging-file>...
#
# Writes to $GITHUB_OUTPUT (stdout when unset):
#   key=<sha256>        what this run would judge: the context file the model is
#                       shown plus every file that decides how it is judged (the
#                       scripts and the workflow, which pins the CLI). The post
#                       step appends it to a fresh verdict comment.
#   reuse=true|false    whether the newest verdict comment already judged
#                       exactly this key
#   verdict=pass|flag   that comment's verdict, when reuse=true
#
# WHY. Every merge to main makes the intake sweep rebase every open intake pull
# request, and every rebase re-ran the model on content it had already judged:
# clearing N ready pull requests took about N*(N+1)/2 model runs on the shared
# subscription (14 open pull requests, ~100 runs, on 2026-09-30), and a verdict
# on unchanged content could flip from one rebase to the next. A rebase over an
# unrelated merge leaves the context byte for byte the same, so its verdict
# stands; a merge that touched an entry this pull request changes shows up in
# the context (the merge-base side of each changed entry is rendered too) and is
# judged afresh.
#
# NEVER REUSED: a workflow_dispatch (someone asked for a fresh verdict), a
# re-run (github.run_attempt > 1: audiosilo-meta-sync re-runs verify to have a
# failed run or a disputed flag judged again), the opt-in `ai-verify` label, and
# any newest verdict that is not a pass or a flag (a skip, or a comment from
# before this key existed).
#
# TRUST. Only a comment by github-actions[bot] that starts with the verdict
# heading counts, and only its LAST line is read: the key is appended after
# everything the model wrote, so text the model echoed from the pull request can
# never stand in for it. COMMENTS_FILE (a JSON array of issue comments) replaces
# the API read, for testing.
set -uo pipefail

CONTEXT="${1:?context file required}"
PR="${2:?pull request number required}"
shift 2
[ "$#" -gt 0 ] || { echo "ai-verify-reuse: no judging files given" >&2; exit 2; }
OUT="${GITHUB_OUTPUT:-/dev/stdout}"

KEY="$(cat "$CONTEXT" "$@" | sha256sum | cut -d' ' -f1)" || { echo "ai-verify-reuse: cannot hash" >&2; exit 2; }
echo "key=$KEY" >> "$OUT"

fresh() {
  echo "ai-verify-reuse: judging afresh ($1)"
  echo "reuse=false" >> "$OUT"
  exit 0
}

[ "${EVENT:-}" = "workflow_dispatch" ] && fresh "a workflow_dispatch asks for a fresh verdict"
[ "${ATTEMPT:-1}" -gt 1 ] 2>/dev/null && fresh "a re-run asks for a fresh verdict"
[ "${EVENT:-}" = "pull_request" ] && [ "${ACTION:-}" = "labeled" ] && fresh "the ai-verify label asks for a fresh verdict"

if [ -n "${COMMENTS_FILE:-}" ]; then
  comments="$(jq -c '.[]' "$COMMENTS_FILE")" || fresh "the comments could not be read"
else
  comments="$(gh api --paginate "repos/${GITHUB_REPOSITORY:?}/issues/$PR/comments" --jq '.[]')" ||
    fresh "the comments could not be read"
fi
body="$(printf '%s\n' "$comments" | jq -rs '
  map(select(.user.login == "github-actions[bot]" and (.body | startswith("### AI verification:"))))
  | sort_by(.created_at) | last | .body // empty')" || fresh "the comments could not be parsed"
[ -n "$body" ] || fresh "no earlier verdict"

last="$(printf '%s\n' "$body" | tr -d '\r' | sed '/^[[:space:]]*$/d' | tail -n 1)"
if [[ "$last" =~ ^\<!--\ ai-verify-key:\ ([0-9a-f]{64})\ verdict:\ (pass|flag)\ --\>$ ]]; then
  if [ "${BASH_REMATCH[1]}" = "$KEY" ]; then
    echo "ai-verify-reuse: reusing the verdict for unchanged context (${BASH_REMATCH[2]}, key ${KEY:0:12})"
    echo "reuse=true" >> "$OUT"
    echo "verdict=${BASH_REMATCH[2]}" >> "$OUT"
    exit 0
  fi
  fresh "the content changed since the last verdict"
fi
fresh "the newest verdict carries no key"
