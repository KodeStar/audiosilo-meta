#!/usr/bin/env bash
# failed-job-logs.sh - the failing jobs' own output, for check.yml's `check`
# verdict, read through the API from the CURRENT run attempt.
#
# Usage:
#   GH_TOKEN=<token> REPO=<owner/repo> RUN_ID=<id> RUN_ATTEMPT=<n> \
#     failed-job-logs.sh <out-dir> <job>...
#       List the jobs of run RUN_ID's attempt RUN_ATTEMPT and, for every one
#       that concluded `failure`, `cancelled` or `timed_out` and is one of the
#       named <job>s (check.yml names the verdict's own failing set: the
#       non-advisory needs whose result is not `success`; a matrix job
#       "<job> (<n>)" counts as <job>), write its log
#       to <out-dir>/<job>.log, or <job>-<n>.log for a matrix job - the file
#       names check-verdict.sh reads. Exit 2 on a usage error, 1 when the job
#       listing cannot be read, else 0: a log that cannot be fetched is written
#       as one line saying so, because the verdict must still print SOMETHING
#       under that job's name.
#
# WHAT A LOG HOLDS. The job's log as the API serves it, with the runner's
# per-line timestamps and colour codes removed, everything from the first
# post-job step on cut off ("Post job cleanup." / "Cleaning up orphan
# processes"), and what is left cut again after its LAST `##[error]` line - the
# runner's "Process completed with exit code N." of a failing run step, an
# action's own error, or "The operation was canceled." of a timed-out job. Most
# steps after a failing one are skipped and write nothing, but an `always()` one
# still runs (metacheck's upload of its advisory keys, on a pull request), and
# its chatter would otherwise be the tail. So the file ENDS with the failing
# step's output - the part check-verdict.sh tails into the sync bot's evidence
# window, which a checkout's cleanup or an upload's progress would otherwise
# fill. A log with no `##[error]` line at all is kept whole up to the post-job
# steps.
#
# WHY THE API AND NOT ARTIFACTS. Every gated job used to tee its output to a file
# and upload it when it failed. Artifacts belong to the RUN, not to an attempt,
# so after a "re-run failed jobs" (which the sync bot asks for before believing
# a failure) the download still held the logs of an earlier attempt - a shard
# that failed in attempt 1 and passed in attempt 2 printed its stale failure
# beside the shard failing now, under one `test: failure`. The attempt's job
# listing names each job's conclusion IN THAT ATTEMPT, so only what is failing
# now is fetched, and no job needs a tee or an upload step of its own.
# `gh run view --log-failed` would be simpler but refuses a run that is still in
# progress, which the verdict's own run always is; the per-job logs endpoint
# serves any job that has finished.
set -uo pipefail

if [ "$#" -lt 2 ]; then
  echo "usage: GH_TOKEN=... REPO=... RUN_ID=... RUN_ATTEMPT=... failed-job-logs.sh <out-dir> <job>..." >&2
  exit 2
fi
for v in REPO RUN_ID RUN_ATTEMPT; do
  if [ -z "${!v:-}" ]; then
    echo "failed-job-logs: $v is not set" >&2
    exit 2
  fi
done
outdir="$1"
shift
wanted=" $* "
mkdir -p "$outdir"

if ! jobs="$(gh api --paginate "repos/$REPO/actions/runs/$RUN_ID/attempts/$RUN_ATTEMPT/jobs?per_page=100" \
  --jq '.jobs[] | select(.conclusion == "failure" or .conclusion == "cancelled" or .conclusion == "timed_out") | "\(.id)\t\(.name)"')"; then
  echo "failed-job-logs: could not list the jobs of run $RUN_ID attempt $RUN_ATTEMPT" >&2
  exit 1
fi

# fetch_log <job id> <file>: the job's raw log, in up to three tries - a job
# that has only just finished can briefly have no log to serve.
fetch_log() {
  local try
  for try in 1 2 3; do
    if gh api "repos/$REPO/actions/jobs/$1/logs" > "$2"; then
      return 0
    fi
    [ "$try" -lt 3 ] && sleep "${RETRY_SLEEP:-5}"
  done
  return 1
}

raw="$(mktemp)"
trap 'rm -f "$raw"' EXIT

while IFS=$'\t' read -r id name; do
  [ -n "$id" ] || continue
  job="$name"
  stem="$name"
  if [[ "$name" =~ ^(.+)\ \(([0-9]+)\)$ ]]; then
    job="${BASH_REMATCH[1]}"
    stem="${BASH_REMATCH[1]}-${BASH_REMATCH[2]}"
  fi
  if [[ "$wanted" != *" $job "* ]]; then
    continue
  fi
  out="$outdir/$stem.log"
  if fetch_log "$id" "$raw"; then
    # The runner colours a step's script and env in its "Run" header
    # (ESC[36;1m ... ESC[0m); the codes are noise in the evidence.
    sed -E -e $'1s/^\xEF\xBB\xBF//' -e 's/^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9:.]+Z ?//' \
      -e $'s/\033\\[[0-9;]*m//g' "$raw" |
      awk '
        $0 == "Post job cleanup." || $0 == "Cleaning up orphan processes" { exit }
        { line[++n] = $0 }
        /^##\[error\]/ { last = n }
        END {
          if (!last) last = n
          for (i = 1; i <= last; i++) print line[i]
        }' > "$out"
    echo "fetched the log of $name ($(wc -l < "$out" | tr -d ' ') lines)"
  else
    echo "(the log of $name, job $id, could not be fetched from the API; see the run)" > "$out"
    echo "failed-job-logs: could not fetch the log of $name (job $id)" >&2
  fi
done <<<"$jobs"
exit 0
