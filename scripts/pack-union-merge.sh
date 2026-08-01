#!/usr/bin/env bash
#
# Resolve ONE conflicted pack file by merging the two sides' entries.
#
# Two pull requests that add records in the same slug range edit the same pack
# file, and git sees overlapping hunks. Usually nothing is in dispute: each side
# added its own entries to a sorted map. This merges the entry maps and leaves
# the result for `metafmt --write` to re-render canonically and re-place (an
# entry that lands in the wrong pack is relocated by the same pass).
#
# It is a THREE-WAY merge, not a union of the two sides. A union cannot express a
# deletion: the side that removed an entry would have it handed back by the side
# that still has it - silently, with a green gate. So the merge BASE decides what
# each difference means:
#
#   present on both sides, identical      keep it
#   present on one side, absent from base that side added it: keep it
#   present on one side, in base unchanged the other side DELETED it: delete it
#   present on one side, in base changed   delete versus modify: refuse
#   absent from both sides                 both deleted it: it stays deleted
#   present on both sides, different       see below
#
# An entry both sides changed is a real conflict with ONE exception, applied one
# level deeper: a works entry whose own fields are identical and whose
# "recordings" maps differ is merged by the same rules over those maps. Two pull
# requests adding different narrations of one book collide on exactly that and
# nothing else, and it is the most common intake collision there is.
#
# Usage, from inside a rebase or merge that stopped on a conflict:
#
#   scripts/pack-union-merge.sh data/works/0/0.json
#   go run ./cmd/metafmt --write && go run ./cmd/metacheck
#   git add data && git rebase --continue
#
# Exit codes: 0 merged, 1 the file cannot be merged this way, 2 usage,
# 5 a real conflict that needs a person. Requires jq.
set -euo pipefail

if [ "$#" -ne 1 ]; then
  echo "usage: $0 <conflicted pack file>" >&2
  exit 2
fi
path="$1"

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

# Stage 1 is the merge base, 2 the side already committed on the branch being
# replayed onto (during a rebase: upstream), 3 the side being replayed. A file
# missing from 2 or 3 was added or deleted whole, which is not a conflict this
# script is for. A missing BASE (both sides created the file) is fine: with no
# base there is nothing either side can have deleted.
if ! git show ":1:$path" > "$tmpdir/base" 2>/dev/null; then
  echo '{"entries":{}}' > "$tmpdir/base"
fi
if ! git show ":2:$path" > "$tmpdir/ours" 2>/dev/null; then
  echo "$path: no common-side version staged; resolve this one by hand" >&2
  exit 1
fi
if ! git show ":3:$path" > "$tmpdir/theirs" 2>/dev/null; then
  echo "$path: no incoming version staged; resolve this one by hand" >&2
  exit 1
fi

if ! jq -n \
  --slurpfile base "$tmpdir/base" \
  --slurpfile a "$tmpdir/ours" \
  --slurpfile b "$tmpdir/theirs" '
  # merge3 applies the base rules to one map, returning what survives and the
  # keys that need a person. It is used for the entries map and, one level down,
  # for a works entry recordings map.
  def merge3($B; $A; $T):
    ([$A, $T, $B | keys[]] | unique) as $keys
    | reduce $keys[] as $k ({kept: {}, clash: []};
        if ($A | has($k)) and ($T | has($k)) then
          (if $A[$k] == $T[$k] then .kept[$k] = $A[$k] else .clash += [$k] end)
        elif ($A | has($k)) then
          (if ($B | has($k))
           then (if $B[$k] == $A[$k] then . else .clash += [$k] end)  # deleted there, changed here
           else .kept[$k] = $A[$k]                                    # added here
           end)
        elif ($T | has($k)) then
          (if ($B | has($k))
           then (if $B[$k] == $T[$k] then . else .clash += [$k] end)
           else .kept[$k] = $T[$k]
           end)
        else .                                                        # deleted on both sides
        end);

  # recordingsOf reads an entry a side may not have, or may not have as an object.
  def recordingsOf: if type == "object" then (.recordings // {}) else {} end;

  # mergeEntry handles one entry both sides changed: identical apart from their
  # recordings maps is mergeable, anything else is a disagreement about a record.
  def mergeEntry($B; $A; $T; $k):
    if (($A | type) != "object") or (($T | type) != "object") then {clash: [$k]}
    else
      ($A | del(.recordings)) as $a0
      | ($T | del(.recordings)) as $t0
      | if $a0 != $t0 then {clash: [$k]}
        else
          merge3(($B | recordingsOf); ($A | recordingsOf); ($T | recordingsOf)) as $r
          | if ($r.clash | length) > 0
            then {clash: ($r.clash | map($k + ".recordings." + .))}
            else {value: (if ($r.kept | length) > 0 then ($a0 + {recordings: $r.kept}) else $a0 end)}
            end
        end
    end;

  ($base[0].entries // {}) as $B
  | ($a[0].entries // {}) as $A
  | ($b[0].entries // {}) as $T
  | ([$A, $T, $B | keys[]] | unique) as $keys
  | reduce $keys[] as $k ({kept: {}, clash: []};
      if ($A | has($k)) and ($T | has($k)) then
        (if $A[$k] == $T[$k] then .kept[$k] = $A[$k]
         else
           (mergeEntry(($B[$k] // {}); $A[$k]; $T[$k]; $k)) as $m
           | if ($m | has("value")) then .kept[$k] = $m.value else .clash += $m.clash end
         end)
      elif ($A | has($k)) then
        (if ($B | has($k))
         then (if $B[$k] == $A[$k] then . else .clash += [$k] end)
         else .kept[$k] = $A[$k]
         end)
      elif ($T | has($k)) then
        (if ($B | has($k))
         then (if $B[$k] == $T[$k] then . else .clash += [$k] end)
         else .kept[$k] = $T[$k]
         end)
      else .
      end)
  | if (.clash | length) > 0
    then error("both sides changed the same records: " + (.clash | join(", ")))
    else {entries: .kept}
    end
' > "$tmpdir/merged" 2> "$tmpdir/err"; then
  cat "$tmpdir/err" >&2
  exit 5
fi

mv "$tmpdir/merged" "$path"
git add "$path"
echo "$path: merged both sides' records"
