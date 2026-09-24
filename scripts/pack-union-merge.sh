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
#   present on both sides, different,
#     one side equal to the base          only the other side changed it: take
#                                          the changed version
#   present on one side, absent from base that side added it: keep it
#   present on one side, in base unchanged the other side DELETED it: delete it
#   present on one side, in base changed   delete versus modify: refuse
#   absent from both sides                 both deleted it: it stays deleted
#   present on both sides, different,
#     absent from base                     both ADDED it: merge the two versions
#                                          unless they contradict (see below)
#   present on both sides, different,
#     neither equal to the base            both CHANGED it: see further below
#
# The one-side-unchanged row holds at EVERY level the merge descends to: the
# entries map, a works entry's own fields (taken as one unit), its recordings
# map, and a works-community entry's member map - and it has ONE implementation,
# merge3With below, which every one of those levels goes through. Without it, main changing one
# series entry (the daily sync bot appending volumes) while a branch left that
# entry exactly as the base had it read as both sides changing it, and a series
# pack the branch had only added other entries to could never be rebased (#2338).
#
# BOTH SIDES ADDED THE SAME ENTRY. Two overlapping library imports mint the same
# record twice and the copies differ without disagreeing: each run stamped the
# source it happened to mint from, one run filled in a field the other never had,
# one listed the same authors in another order. Nobody is asserting a different
# fact, so the two versions are merged, recursively over objects:
#
#   a key on one side only                 take it
#   a key on both sides, equal             keep it
#   sources                                union, deduplicated by the whole
#                                          object, ours first then theirs' new
#                                          ones - provenance accumulates, it is
#                                          never the thing in dispute
#   works, on a series entry               union by (work, position); one
#                                          position naming two works, or one work
#                                          at two positions, IS a disagreement
#   authors / narrators                    the same set in another order keeps
#                                          OURS' order (in a rebase that is what
#                                          is already on main); a different set
#                                          is a disagreement
#   any other key whose two values
#     are both objects                     recurse with these same rules - which
#                                          is how a works entry's recordings map,
#                                          and each recording under it, merges
#   anything else that differs             a contradiction: the whole entry is
#                                          refused, exactly as before
#
# That rule is family-NEUTRAL apart from the series works list: two contributors
# adding one person, one work or one community member each get it, because the
# question it answers - did either side contradict the other? - does not depend on
# the shape of the record. It applies ONLY where the base has no such entry. An
# entry the base HAS that both sides changed is a real edit conflict and keeps the
# rules below.
#
# An entry both sides changed is a real conflict with TWO exceptions, each
# applied one level deeper and each to one family, because what sits one level
# down differs per family:
#
#   works            an entry whose own fields are identical, or changed on one
#                    side only, and whose "recordings" maps differ is merged by
#                    the same rules over those maps. Two pull requests adding
#                    different narrations of one book collide on exactly that and
#                    nothing else.
#   works-community  the entry IS a map of independent members ("characters",
#                    "recaps", "description"), each its own licensed document, so
#                    the base rules apply to that map directly. A characters pull
#                    request and a recaps pull request for the same book each
#                    create or edit ONE member of the same entry, which is the
#                    same collision one family over. The rule is over the entry's
#                    KEYS, so a member kind added to the model is covered by it
#                    without an edit here.
#
# Both are the most common intake collisions there are. Everything else - a
# member or a recording both sides wrote, a work's own fields both sides changed,
# a person or a series entry both sides changed - stays a refusal.
#
# WHICH exception applies is decided by the FAMILY the pack file belongs to (its
# path), not by sniffing the entry: the family is what fixes the shape of an
# entry, and a rule that guessed from the keys present would quietly start
# merging a family it was never reasoned about.
#
# Usage, from inside a rebase or merge that stopped on a conflict:
#
#   scripts/pack-union-merge.sh data/works/0/0.json
#   go run ./cmd/metafmt --write --profile core && go run ./cmd/metacheck --profile core
#   git add data && git rebase --continue
#
# It also runs as a git MERGE DRIVER, which is how the intake sweep uses it:
#
#   git config merge.packjson.name "three-way merge of pack-file entries"
#   git config merge.packjson.driver "scripts/pack-union-merge.sh %O %A %B %P"
#
# (That relative path is fine in your own checkout. git runs a driver from the
# working tree's root, so mid-rebase it names the REPLAYED branch's copy - which
# is why the intake sweep installs main's copy outside the tree and configures
# the driver by that absolute path instead.)
#
# .gitattributes already maps data/**/*.json to that driver, so configuring it is
# all it takes (git falls back to its own line merge where it is not configured,
# which is what every other checkout keeps doing). The driver is not an
# optimization: git's LINE merge is unsound for a pack file. Two branches that
# add the SAME entry key in one pack usually collide and stop, but when one side
# also changed the entry NEXT to it the two insertions can be anchored at
# different lines, and git then applies both - one file with the key in it TWICE
# (a works-community entry once with its characters member and once with its
# recaps member, exactly what should have merged). Nothing downstream can repair
# that: a duplicate key is not valid pack storage, so pkg/pack refuses the file
# and metafmt cannot re-render it. As a driver this script sees the same three
# versions BEFORE any line merge happens and merges the entry maps instead, so
# the shape cannot arise; when it refuses, git records the conflict as usual.
#
# It merges PACK files and nothing else: a version that is not a pack (no
# top-level "entries" object) is refused rather than treated as an empty one, so
# the tree's one non-pack file - data/redirects.json, the slug tombstone table -
# cannot be emptied by a merge that reports success. .gitattributes keeps that file
# away from the driver as well; this is the backstop for a stale checkout or a hand
# invocation.
#
# Exit codes: 0 merged, 1 the file cannot be merged this way, 2 usage,
# 5 a real conflict that needs a person (as a merge driver, any non-zero exit
# tells git the file is conflicted). Requires jq.
set -euo pipefail

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

case "$#" in
  1) mode=stage; path="$1" ;;
  4) mode=driver; basefile="$1"; oursfile="$2"; theirsfile="$3"; path="$4" ;;
  *)
    echo "usage: $0 <conflicted pack file>" >&2
    echo "       $0 <base> <ours> <theirs> <path>   (git merge driver: %O %A %B %P)" >&2
    exit 2
    ;;
esac

# The family decides which one-level-deeper exception applies. A path git resolves
# through the index is repository-root relative, and so is the %P a merge driver
# is handed (the temp files it merges are NOT, which is why the path is a separate
# argument), so the first directory under data/ IS the family; anything else names
# no family and gets no exception.
family="${path#data/}"
family="${family%%/*}"

if [ "$mode" = stage ]; then
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
else
  # As a driver the three versions arrive as files. An EMPTY base is git's way of
  # saying the file is new on both sides, the same "nothing to have deleted" case
  # a missing stage 1 is; an empty side is a deletion, which git resolves before
  # it ever calls a driver, so it is not a shape this can merge.
  cp "$basefile" "$tmpdir/base"
  if [ ! -s "$tmpdir/base" ]; then
    echo '{"entries":{}}' > "$tmpdir/base"
  fi
  cp "$oursfile" "$tmpdir/ours"
  cp "$theirsfile" "$tmpdir/theirs"
  for side in ours theirs; do
    if [ ! -s "$tmpdir/$side" ]; then
      echo "$path: the $side version is empty; resolve this one by hand" >&2
      exit 1
    fi
  done
fi

# Every version has to BE a pack. Nothing downstream checks this, and jq's `//`
# would read a file with no "entries" as an empty one - so handed a JSON file that
# is not a pack (data/redirects.json, the slug tombstone table, is the one the tree
# holds) this would "merge" it into {"entries":{}} and report success, replacing
# the file's whole contents with an empty pack. .gitattributes keeps that file away
# from the driver, but a stale checkout's attributes, a hand invocation or a fifth
# non-pack file must not be able to make a silent deletion out of a merge.
#
# The base's stand-in above is already a pack, so this judges what it is handed.
for side in base ours theirs; do
  if ! jq -e 'type == "object" and has("entries") and (.entries | type == "object")' \
      "$tmpdir/$side" > /dev/null 2>&1; then
    echo "$path: the $side version is not a pack file (no top-level \"entries\" object); this script merges pack entries only" >&2
    exit 1
  fi
done

if ! jq -n \
  --arg family "$family" \
  --slurpfile base "$tmpdir/base" \
  --slurpfile a "$tmpdir/ours" \
  --slurpfile b "$tmpdir/theirs" '
  # merge3With applies the base rules to one map, returning what survives and the
  # keys that need a person. It is used for the entries map and, one level down,
  # for a works entry recordings map and a works-community entry member map - ONE
  # copy of the rules, so a rule added to them (the one-side-unchanged row) cannot
  # reach one level and miss another. `both` decides a key present on both sides
  # that neither side left as the base had it: it is handed the key and returns
  # {value:} or {clash:}. A value holding nothing (every member deleted) is a
  # deletion, not an empty record.
  def merge3With($B; $A; $T; both):
    ([$A, $T, $B | keys[]] | unique) as $keys
    | reduce $keys[] as $k ({kept: {}, clash: []};
        if ($A | has($k)) and ($T | has($k)) then
          (if $A[$k] == $T[$k] then .kept[$k] = $A[$k]
           elif ($B | has($k)) and $B[$k] == $A[$k] then .kept[$k] = $T[$k]  # changed there only
           elif ($B | has($k)) and $B[$k] == $T[$k] then .kept[$k] = $A[$k]  # changed here only
           else
             ($k | both) as $m
             | if ($m | has("value"))
               then (if ($m.value | length) > 0 then .kept[$k] = $m.value else . end)
               else .clash += $m.clash
               end
           end)
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

  # merge3 is the base rules alone: a key both sides changed needs a person.
  def merge3($B; $A; $T): merge3With($B; $A; $T; {clash: [.]});

  # unionSources merges two provenance lists. Provenance accumulates and is never
  # the thing two imports disagree about: ours first, then the objects on the
  # other side that are not already there, so one merge does not reshuffle what
  # another one wrote.
  def unionSources($A; $T):
    reduce ($A + $T)[] as $s ([]; if any(.[]; . == $s) then . else . + [$s] end);

  # unionSeriesWorks merges two series membership lists by (work, position). One
  # side listing more of the series than the other is an import that saw more
  # books; one position naming two works, or one work at two positions, is a
  # disagreement about the series itself and is reported instead.
  def unionSeriesWorks($A; $T):
    reduce $T[] as $w ({list: $A, clash: false};
      if any(.list[]; . == $w) then .
      elif any(.list[];
               ((.position == $w.position) and (.work != $w.work))
               or ((.work == $w.work) and (.position != $w.position))) then .clash = true
      else .list += [$w]
      end);

  # mergeAdded merges two versions of ONE entry that both sides ADDED, returning
  # {value:} or {clash:} holding the sub-paths that disagree, relative to the
  # entry. The base is not a parameter because there is none: the whole rule is
  # "did either side contradict the other", which is why it needs no family.
  # $depth is 0 at the entry itself, so the series works rule cannot be reached by
  # a works key that happens to sit deeper inside some other record.
  def mergeAdded($A; $T; $depth):
    if (($A | type) != "object") or (($T | type) != "object") then
      (if $A == $T then {value: $A} else {clash: [""]} end)
    else
      ([$A, $T | keys[]] | unique) as $keys
      | reduce $keys[] as $k ({value: {}, clash: []};
          if ($A | has($k) | not) then .value[$k] = $T[$k]
          elif ($T | has($k) | not) then .value[$k] = $A[$k]
          elif $A[$k] == $T[$k] then .value[$k] = $A[$k]
          elif ($k == "sources")
               and (($A[$k] | type) == "array") and (($T[$k] | type) == "array") then
            .value[$k] = unionSources($A[$k]; $T[$k])
          elif ($k == "works") and ($family == "series") and ($depth == 0)
               and (($A[$k] | type) == "array") and (($T[$k] | type) == "array")
               and (all(($A[$k] + $T[$k])[];
                        (type == "object") and has("work") and has("position"))) then
            (unionSeriesWorks($A[$k]; $T[$k])) as $u
            | if $u.clash then .clash += [$k] else .value[$k] = $u.list end
          elif (($k == "authors") or ($k == "narrators"))
               and (($A[$k] | type) == "array") and (($T[$k] | type) == "array")
               and (($A[$k] | sort) == ($T[$k] | sort)) then
            .value[$k] = $A[$k]
          elif (($A[$k] | type) == "object") and (($T[$k] | type) == "object") then
            (mergeAdded($A[$k]; $T[$k]; $depth + 1)) as $m
            | if ($m | has("value")) then .value[$k] = $m.value
              else .clash += ($m.clash | map(if . == "" then $k else $k + "." + . end))
              end
          else .clash += [$k]
          end)
      | if (.clash | length) > 0 then {clash: .clash} else {value: .value} end
    end;

  # prefix names a sub-path clash after the entry it came from.
  def prefix($m; $k):
    if ($m | has("value")) then $m
    else {clash: ($m.clash | map(if . == "" then $k else $k + "." + . end))}
    end;

  # recordingsOf reads an entry a side may not have, or may not have as an object.
  def recordingsOf: if type == "object" then (.recordings // {}) else {} end;

  # ownFieldsOf is an entry without its recordings map, the unit mergeEntry
  # compares the own fields of a work as.
  def ownFieldsOf: if type == "object" then del(.recordings) else . end;

  # mergeEntry handles one entry both sides changed: the own fields are one unit
  # that at most one side may have changed, and the recordings maps merge by the
  # base rules. The own-fields decision goes through merge3 too, as the single
  # key "own", so the one-side-unchanged rule has one implementation rather than
  # a copy here. Own fields both sides changed are a disagreement about a record.
  def mergeEntry($B; $A; $T; $k):
    if (($A | type) != "object") or (($T | type) != "object") then {clash: [$k]}
    else
      merge3({own: ($B | ownFieldsOf)}; {own: ($A | ownFieldsOf)}; {own: ($T | ownFieldsOf)}) as $o
      | if ($o.clash | length) > 0 then {clash: [$k]}
        else
          $o.kept.own as $own
          | merge3(($B | recordingsOf); ($A | recordingsOf); ($T | recordingsOf)) as $r
          | if ($r.clash | length) > 0
            then {clash: ($r.clash | map($k + ".recordings." + .))}
            else {value: (if ($r.kept | length) > 0 then ($own + {recordings: $r.kept}) else $own end)}
            end
        end
    end;

  # mergeMembers handles one works-community entry both sides changed. The entry
  # has no own fields: it IS the map of its members, so the base rules apply to it
  # one level down unchanged. Disjoint members (one side wrote characters, the
  # other recaps or the description) merge; the same member written on both sides
  # is two people disagreeing about one text and clashes. It never names a member
  # kind, so a new one rides it as it is.
  def mergeMembers($B; $A; $T; $k):
    if (($A | type) != "object") or (($T | type) != "object") then {clash: [$k]}
    else
      merge3($B; $A; $T) as $m
      | if ($m.clash | length) > 0
        then {clash: ($m.clash | map($k + "." + .))}
        else {value: $m.kept}
        end
    end;

  # mergeChanged dispatches an entry both sides changed to its family exception.
  # A family with none (people, series) gets the plain refusal.
  def mergeChanged($B; $A; $T; $k):
    if $family == "works" then mergeEntry($B; $A; $T; $k)
    elif $family == "works-community" then mergeMembers($B; $A; $T; $k)
    else {clash: [$k]}
    end;

  ($base[0].entries // {}) as $B
  | ($a[0].entries // {}) as $A
  | ($b[0].entries // {}) as $T
  # The entries map takes the base rules like every level below it; only a key
  # both sides changed or both sides added is decided here. The base decides
  # which of the two it is: an entry it HAS is one both sides changed, an entry
  # it lacks is one both sides added.
  | merge3With($B; $A; $T;
      . as $k
      | if ($B | has($k))
        then mergeChanged(($B[$k] // {}); $A[$k]; $T[$k]; $k)
        else prefix(mergeAdded($A[$k]; $T[$k]; 0); $k)
        end)
  | if (.clash | length) > 0
    then error("both sides changed the same records: " + (.clash | join(", ")))
    else {entries: .kept}
    end
' > "$tmpdir/merged" 2> "$tmpdir/err"; then
  cat "$tmpdir/err" >&2
  exit 5
fi

if [ "$mode" = stage ]; then
  mv "$tmpdir/merged" "$path"
  git add "$path"
else
  # git takes the driver's result from the file it passed as %A.
  mv "$tmpdir/merged" "$oursfile"
fi
# The intake sweep greps a rebase's output for "merged both sides", because a
# driver merge is the one way a CLEAN rebase can still need metafmt to re-render
# and re-place what was merged. Keep this line's wording.
echo "$path: merged both sides' records"
