#!/usr/bin/env bash
# real-data-report.sh - make real-data.yml's unattended result VISIBLE.
#
# Usage:
#   bash .github/scripts/real-data-report.sh summarize <test-log>
#       Print the bounded failure summary of a `go test` log on stdout (the
#       FAIL and panic lines; at most MAX_LINES lines of MAX_WIDTH bytes).
#
#   bash .github/scripts/real-data-report.sh report
#       env: GH_TOKEN, GITHUB_REPOSITORY, RESULT (the test job's
#            needs.<job>.result), RUN_URL, SHA, FAILURES (the summary above,
#            may be empty).
#       RESULT=success closes the open tracking issue, if there is one, with a
#       comment linking the green run. Anything else (failure, cancelled - a
#       job timeout lands here too) opens the tracking issue, or comments on it
#       when one is already open, naming the run, the commit and the failures.
#
# Why it exists: see the header of .github/workflows/real-data.yml.
#
# ONE OPEN ISSUE, identified by the LABEL (not the title, which a human may
# edit): a failure while it is open is a comment on it, never a second issue,
# and a green run closes it. The label is created if missing, so the first
# failure on a fresh repository still files.
set -euo pipefail

LABEL="ci-real-data"
TITLE="Real-data tests are failing on main"
MAX_LINES=50
MAX_WIDTH=300

summarize() {
  local log="$1"
  if [ ! -f "$log" ]; then
    echo "(no test log was written: the job failed before or around the test step)"
    return 0
  fi
  local lines total
  # Subtests are indented, so the FAIL marker is matched after any leading
  # whitespace. A timed-out binary prints no `--- FAIL:` at all, only
  # `panic: test timed out`, then `running tests:` and the hung test names,
  # then its package `FAIL` line - so the first panic carries its next lines.
  lines="$( { grep -E '^[[:space:]]*--- FAIL:|^FAIL[[:space:]]' "$log" || true
              grep -m1 -A4 -E '^panic:' "$log" || true; } | cut -c1-"$MAX_WIDTH")"
  if [ -z "$lines" ]; then
    echo "(no --- FAIL: line in the test log: the failure is outside the tests - a build error, a checkout or setup step, or a timeout; see the run)"
    return 0
  fi
  # Here-strings, not `printf | head`: head exits after MAX_LINES, and a writer
  # still holding more than a pipe buffer then dies of SIGPIPE, which pipefail
  # turns into a failed script and an unterminated $GITHUB_OUTPUT block.
  total="$(wc -l <<<"$lines" | tr -d ' ')"
  head -n "$MAX_LINES" <<<"$lines"
  if [ "$total" -gt "$MAX_LINES" ]; then
    echo "... and $((total - MAX_LINES)) more"
  fi
}

open_issue() {
  gh issue list --repo "$GITHUB_REPOSITORY" --label "$LABEL" --state open \
    --json number --jq 'sort_by(.number) | .[0].number // empty'
}

# The issue body for a run that did not pass (reads RESULT, SHA, RUN_URL, FAILURES).
failure_body() {
  echo "The real-data test run (no -race) ended \`${RESULT}\` at ${SHA}."
  echo
  echo "Run: ${RUN_URL}"
  echo
  local failures="${FAILURES:-(no summary was produced; see the run)}"
  # The fence is one backtick longer than any run in the content, so a panic
  # message quoting a fence of its own cannot close this one early (a closing
  # fence must be at least as long as the opening one).
  local fence='```'
  while [[ "$failures" == *"$fence"* ]]; do fence="${fence}\`"; done
  echo "Failing tests (from the log, bounded):"
  echo
  echo "$fence"
  printf '%s\n' "$failures"
  echo "$fence"
  echo
  echo "These tests read the real data/ tree and skip under -race, so the pull-request gate never runs them. Reproduce with \`go test -count=1 ./...\` (no -race). This issue closes itself on the next green run."
}

report() {
  : "${GITHUB_REPOSITORY:?GITHUB_REPOSITORY must be set}"
  : "${RESULT:?RESULT must be the test job result}"
  : "${RUN_URL:?RUN_URL must be set}"
  : "${SHA:?SHA must be set}"
  local issue
  issue="$(open_issue)"

  if [ "$RESULT" = "success" ]; then
    if [ -z "$issue" ]; then
      echo "real-data tests passed; no open ${LABEL} issue to close."
      return 0
    fi
    gh issue close "$issue" --repo "$GITHUB_REPOSITORY" \
      --comment "The real-data tests passed at ${SHA}: ${RUN_URL}"
    echo "real-data tests passed; closed #${issue}."
    return 0
  fi

  if [ -n "$issue" ]; then
    failure_body | gh issue comment "$issue" --repo "$GITHUB_REPOSITORY" --body-file -
    echo "real-data tests ${RESULT}; commented on #${issue}."
  else
    gh label create "$LABEL" --repo "$GITHUB_REPOSITORY" --color B60205 \
      --description "The real-data test run (real-data.yml) is failing" --force
    failure_body | gh issue create --repo "$GITHUB_REPOSITORY" --title "$TITLE" \
      --label "$LABEL" --body-file -
    echo "real-data tests ${RESULT}; opened a ${LABEL} issue."
  fi
}

case "${1:-}" in
  summarize)
    summarize "${2:?usage: summarize <test-log>}"
    ;;
  report)
    report
    ;;
  *)
    echo "usage: $0 summarize <test-log> | report" >&2
    exit 2
    ;;
esac
