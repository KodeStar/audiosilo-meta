#!/usr/bin/env bash
# check-verdict.sh - the verdict of check.yml's aggregate `check` job.
#
# Usage:
#   NEEDS='<toJSON(needs)>' check-verdict.sh <log-dir> [<advisory-job>...]
#       Read the `needs` context of the aggregate job (every job it waits on,
#       each with its `result`) from $NEEDS. Exit 0 when every job not named as
#       an advisory one concluded `success`; otherwise name each one that did
#       not, print the tail of every log found under <log-dir> (the failing
#       jobs' captured output, downloaded as artifacts), and exit 1. An absent,
#       empty or unreadable $NEEDS is a failure: a verdict nobody could read is
#       never a pass.
#
# WHY A VERDICT JOB. `check` used to be one job running the Go gate, metacheck
# and metafmt in sequence, and its check-run NAME is load-bearing: branch
# protection requires `check`, and audiosilo-meta-sync merges its pull requests
# once `check`, `compose`, `site` and `verify` are green (its watcher.Required).
# The work is now spread over parallel jobs (build, the test shards, metacheck,
# metafmt, advisories), and `check` is the one job that needs all of them and
# says whether they passed - so neither consumer had to learn a new name.
#
# WHY THE LOGS. When a required check fails twice, the sync bot hands the
# resolver the last lines of THAT check run's log (its evidence.CheckFlag). A
# verdict job whose log only said "metacheck: failure" would hand it nothing to
# fix, so each gated job tees its output to a file, uploads it when it fails, and
# this script prints those files last - where the bot's tail reads them - just
# as the single job's own log used to end with the failing step's output.
#
# RESULTS. `success` is the only pass. `failure`, `cancelled` and `skipped` are
# not: the gated jobs never skip at the job level (their STEPS are gated, so a
# data-only diff still concludes them green), so a skipped one means a job it
# needs failed, and a pass there would be a check that checked nothing.
set -uo pipefail

TAIL_LINES="${TAIL_LINES:-150}"

if [ "$#" -lt 1 ]; then
  echo "usage: NEEDS=<json> check-verdict.sh <log-dir> [<advisory-job>...]" >&2
  exit 2
fi
logdir="$1"
shift
advisory=" $* "

if [ -z "${NEEDS:-}" ]; then
  echo "check-verdict: NEEDS is empty; no job's result can be read" >&2
  exit 1
fi
if ! rows="$(jq -r 'to_entries[] | "\(.key)\t\(.value.result)"' <<<"$NEEDS" 2>/dev/null)" || [ -z "$rows" ]; then
  echo "check-verdict: NEEDS is not a non-empty needs context: $NEEDS" >&2
  exit 1
fi

bad=0
while IFS=$'\t' read -r job result; do
  if [[ "$advisory" == *" $job "* ]]; then
    echo "$job: $result (advisory, never the verdict)"
    continue
  fi
  echo "$job: $result"
  if [ "$result" != "success" ]; then
    bad=1
  fi
done <<<"$rows"

if [ "$bad" -eq 0 ]; then
  echo "every required job passed"
  exit 0
fi

# The failing jobs' own output, last, so a reader of this log's tail sees it.
if [ -d "$logdir" ]; then
  while IFS= read -r log; do
    echo
    echo "===== $(basename "$(dirname "$log")")/$(basename "$log"), last ${TAIL_LINES} lines ====="
    tail -n "$TAIL_LINES" "$log"
  done < <(find "$logdir" -type f -name '*.log' | LC_ALL=C sort)
fi
echo
echo "check-verdict: a required job did not pass (see above)" >&2
exit 1
