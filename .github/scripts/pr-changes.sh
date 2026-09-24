#!/usr/bin/env bash
# pr-changes.sh - classify a pull request's changed files for check.yml's jobs.
#
# Usage: bash .github/scripts/pr-changes.sh
#   env: GH_TOKEN, GITHUB_REPOSITORY, GITHUB_EVENT_NAME, GITHUB_OUTPUT, and PR
#        (the pull request number) on a pull_request event.
#
# Writes two step outputs, both decided from ONE listing of the diff:
#
#   go=true|false       the diff reaches the GO GATE - build, vet, `go test
#                       -race`, golangci-lint, govulncheck. TRUE unless every
#                       changed file is data or prose (`data/**`, `*.md`).
#   compose=true|false  the diff can move the two-tree COMPOSE. TRUE when any
#                       changed file is one the release build reads.
#
# WHY THE GO GATE IS GATED AT ALL: most pull requests here are the intake and
# sync bots', and a bot's tranche of pack files cannot change what `go vet` or
# the race detector say. Running the full Go gate on every one of them spends
# several minutes of runner per bot PR to re-prove a program nobody edited.
# metacheck and metafmt are NOT gated - they are the data's own check and run on
# every pull request.
#
# WHY IT IS A JOB OUTPUT RATHER THAN A `paths:` FILTER: the Go jobs are REQUIRED
# checks, and a filtered-out job never reports at all, which branch protection
# reads as "still waiting" forever. So the jobs always run and their STEPS carry
# the condition - the site job's long-standing pattern (see its comment).
#
# WHY ONE SCRIPT: the answer was being derived per job, which is three chances
# to disagree about what a Go change is, plus a second API call for the compose
# probe. One listing, one place, two answers.
#
# FAIL OPEN. Anything this cannot decide - a push rather than a pull request, an
# API call that failed, an empty listing - answers TRUE for both. An unreadable
# diff is a reason to run the gate, never a reason to skip it. (The compose
# probe this replaced failed CLOSED: its `gh api | grep -q` pipeline read an API
# failure as "no relevant paths" and skipped the job.)
set -euo pipefail

: "${GITHUB_OUTPUT:?GITHUB_OUTPUT must be set}"

emit() {
  echo "go=$1 compose=$2"
  {
    echo "go=$1"
    echo "compose=$2"
  } >> "$GITHUB_OUTPUT"
}

if [ "${GITHUB_EVENT_NAME:-}" != "pull_request" ]; then
  echo "not a pull request; every job runs."
  emit true true
  exit 0
fi

: "${PR:?PR must be the pull request number}"

# The exit status is kept, not discarded: --paginate streams each page as it
# lands, so a listing that failed on page three is a NON-EMPTY string holding
# pages one and two. Judged on emptiness alone, a large bot tranche whose Go
# files sorted onto the page that never arrived would read as data-only and
# skip the gate - the one silent answer this script promises never to give.
files=""
if ! files="$(gh api "repos/${GITHUB_REPOSITORY}/pulls/${PR}/files" --paginate --jq '.[].filename')" || [ -z "$files" ]; then
  echo "::warning::could not read the changed files of pull request ${PR}; every job runs."
  emit true true
  exit 0
fi
total="$(printf '%s\n' "$files" | wc -l | tr -d ' ')"
# Counted rather than asked as `grep -qv`: "-q with -v" is exactly where grep
# implementations disagree (ugrep answers 1 where GNU and busybox answer 0), and
# a silently inverted answer here would skip the gate rather than fail loudly.
# So the POSITIVE match is counted and compared against the total.
dataprose="$(printf '%s\n' "$files" | grep -cE '^data/|\.md$' || true)"
echo "${total} changed files, ${dataprose} of them data or prose"

go=false
compose=false
# One file that is neither data nor prose is enough to reach the Go gate.
if [ "$total" -ne "$dataprose" ]; then
  go=true
fi
# The same path set release.yml releases on, plus the workflow and this script
# - the two files that decide whether the compose runs at all.
if printf '%s\n' "$files" | grep -qE '^(data/|schema/|internal/build/|cmd/metabuild/|pkg/check/|pkg/pack/|\.github/workflows/check\.yml$|\.github/scripts/pr-changes\.sh$)'; then
  compose=true
fi
emit "$go" "$compose"
