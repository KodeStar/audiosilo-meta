#!/usr/bin/env bash
#
# Resolve ONE conflicted pack file by keeping BOTH sides' entries.
#
# Two pull requests that add records in the same slug range edit the same pack
# file, and git sees overlapping hunks. Nothing is actually in dispute: each side
# added its own entries to a sorted map. This takes the union of the two entry
# maps and leaves the result for `metafmt --write` to re-render canonically and
# re-place (an entry that lands in the wrong pack is relocated by the same pass).
#
# It REFUSES the one case that is a genuine conflict: the same entry key changed
# on both sides. That is two people editing one record, which a merge tool must
# not silently pick a winner for.
#
# Usage, from inside a rebase or merge that stopped on a conflict:
#
#   scripts/pack-union-merge.sh data/works/0/0.json
#   go run ./cmd/metafmt --write && go run ./cmd/metacheck
#   git add data && git rebase --continue
#
# Requires jq.
set -euo pipefail

if [ "$#" -ne 1 ]; then
  echo "usage: $0 <conflicted pack file>" >&2
  exit 2
fi
path="$1"

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

# Stage 2 is the side already committed on the branch being replayed onto
# (during a rebase: upstream); stage 3 is the side being replayed. A file that is
# missing from either stage was added or deleted whole, which is not a conflict
# this script is for.
if ! git show ":2:$path" > "$tmpdir/ours" 2>/dev/null; then
  echo "$path: no common-side version staged; resolve this one by hand" >&2
  exit 1
fi
if ! git show ":3:$path" > "$tmpdir/theirs" 2>/dev/null; then
  echo "$path: no incoming version staged; resolve this one by hand" >&2
  exit 1
fi

jq -n --slurpfile a "$tmpdir/ours" --slurpfile b "$tmpdir/theirs" '
  ($a[0].entries // {}) as $A
  | ($b[0].entries // {}) as $B
  | [ ($A | keys[]) as $k | select(($B | has($k)) and ($A[$k] != $B[$k])) | $k ] as $clash
  | if ($clash | length) > 0
    then error("both sides changed the same entries: " + ($clash | join(", ")))
    else {entries: ($A + $B)}
    end
' > "$tmpdir/merged"

mv "$tmpdir/merged" "$path"
git add "$path"
echo "$path: kept both sides' entries"
