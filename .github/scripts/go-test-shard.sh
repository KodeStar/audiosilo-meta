#!/usr/bin/env bash
# go-test-shard.sh - which Go packages one shard of check.yml's `test` matrix runs.
#
# Usage:
#   go list ./... | go-test-shard.sh <shard> <count>
#       Read package import paths on stdin, one per line, and print the ones shard
#       <shard> (1-based) of <count> runs, one per line, sorted. Exits 2 on a bad
#       argument and 1 when stdin holds no package at all or this shard comes out
#       empty: `xargs go test` over an empty list would test the current
#       directory alone and pass, so an empty shard is never an answer.
#
# WHY. `go test -race ./...` was the longest step of the `check` job: ~215 s on
# a push to main and ~310 s on a pull request that also carried a large data
# change, because internal/recorddiff's TestAgainstTheRealCheckout reads the
# commit's whole data diff (HEAD~1..HEAD) under the detector. go test already
# runs packages in parallel inside one runner, so the wall time is bounded by its
# few long packages sharing four cores with everything else; splitting the list
# across runners is what shortens it.
#
# EVERY PACKAGE RUNS EXACTLY ONCE. The partition is a pure function of the
# listing and <count>: each package lands in exactly one shard of the same
# <count>, whatever the shard asking, so the union of the matrix is `./...` and
# no package is tested twice or never. scripts/gotestshard_test.go pins that,
# and pins that check.yml's matrix names every shard from 1 to its count.
#
# BALANCE is a greedy longest-first packing over the WEIGHTS below: the packages
# sorted by weight (then name), each placed in the currently lightest shard
# (ties to the lowest-numbered). A weight is roughly the package's seconds under
# -race on a GitHub runner, as measured when this was written; a package not
# listed weighs DEFAULT_WEIGHT. The weights only steer the balance, never the
# coverage, so a stale one costs wall time, not a test - re-measure from a
# check run's `ok` lines when a shard grows long.
set -euo pipefail

DEFAULT_WEIGHT=2

# Seconds under -race on a GitHub ubuntu-latest runner (check runs of
# 2026-10-27/28). recorddiff is ~3 s on most changes and ~240 s on a large data
# change; it is weighted between the two so it never shares a shard with the
# other long package.
WEIGHTS='
github.com/kodestar/audiosilo-meta/internal/importer 130
github.com/kodestar/audiosilo-meta/internal/recorddiff 120
github.com/kodestar/audiosilo-meta/internal/serve 60
github.com/kodestar/audiosilo-meta/pkg/check 56
github.com/kodestar/audiosilo-meta/internal/issueform 52
github.com/kodestar/audiosilo-meta/internal/audit 51
github.com/kodestar/audiosilo-meta/pkg/query 50
github.com/kodestar/audiosilo-meta/internal/migrate 44
github.com/kodestar/audiosilo-meta/internal/repair 33
github.com/kodestar/audiosilo-meta/internal/build 9
github.com/kodestar/audiosilo-meta/scripts 6
github.com/kodestar/audiosilo-meta/pkg/model 6
github.com/kodestar/audiosilo-meta/pkg/pack 6
'

usage() {
  echo "usage: go list ./... | go-test-shard.sh <shard> <count>" >&2
  exit 2
}

[ "$#" -eq 2 ] || usage
shard="$1"
count="$2"
[[ "$count" =~ ^[1-9][0-9]*$ ]] || usage
[[ "$shard" =~ ^[1-9][0-9]*$ ]] || usage
[ "$shard" -le "$count" ] || usage

export LC_ALL=C
pkgs="$(grep -v '^[[:space:]]*$' | sort -u || true)"
if [ -z "$pkgs" ]; then
  echo "go-test-shard: no packages on stdin" >&2
  exit 1
fi

# Weight each package, order longest first (name breaks a tie, so the order is
# total), then pack greedily.
mine="$(printf '%s\n' "$pkgs" |
  awk -v def="$DEFAULT_WEIGHT" '
    NR == FNR { if (NF == 2) w[$1] = $2; next }
    { print (($0 in w) ? w[$0] : def), $0 }' <(printf '%s\n' "$WEIGHTS") - |
  sort -k1,1nr -k2,2 |
  awk -v shard="$shard" -v count="$count" '
    BEGIN { for (b = 1; b <= count; b++) load[b] = 0 }
    {
      best = 1
      for (b = 2; b <= count; b++) if (load[b] < load[best]) best = b
      load[best] += $1
      if (best == shard) print $2
    }' |
  sort)"
if [ -z "$mine" ]; then
  echo "go-test-shard: shard $shard of $count holds no package" >&2
  exit 1
fi
printf '%s\n' "$mine"
