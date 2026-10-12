#!/usr/bin/env bash
# advisory-delta.sh - the metacheck ADVISORIES a pull request introduces.
#
# Usage:
#   advisory-delta.sh keys <metacheck-binary> <rev> <out-file>
#       Extract data/ of git revision <rev> into a temporary directory, run the
#       given metacheck over it and write its advisory keys (`--advisory-keys`,
#       check.AdvisoryKey: class, a tab, the record, the message) to <out-file>.
#       Exits 0 once the keys are written, whatever the tree's problems; non-zero
#       when they could not be (an unknown rev, a tree metacheck could not load).
#   advisory-delta.sh diff <base-keys> <head-keys>
#       Print the advisories <head-keys> holds and <base-keys> does not, bounded,
#       or NOTHING when there are none - an empty output is the "none" answer, so
#       a caller tests it with `-s`. Exits 2 when either file is missing.
#   advisory-delta.sh summary <diff-output>
#       Render a `diff` output as the markdown check.yml appends to the job's step
#       summary: a heading, then the list in a code fence longer than any backtick
#       run in it (the list quotes titles), or a line saying there are none.
#
# WHY. metacheck's advisory census is ~1,900 lines on every run, and nobody reads
# a log that long: a pull request that ADDED a duplicate work, a recording-variant
# series or an orphan person was never noticed, because the line it added scrolled
# past with the rest. The census is a maintainer's reading list for the WHOLE
# tree; what a reviewer of one pull request needs is the part of it that pull
# request made. check.yml prints it in the job summary and ai-verify.yml hands it
# to the model, which flags the pull request - that is the route a sync-bot pull
# request reaches its steward by, so the advisories themselves never fail a check
# (the bot auto-merges on green, and a new red gate would stall it instead of
# routing it).
#
# WHAT COUNTS AS INTRODUCED. A KEY carries no pack file (a split or rebind renames
# packs under untouched records) but does carry the whole message, and a message
# moves with things a pull request does not introduce: a duplicate series' member
# count when a volume is added to both spellings, a duplicate group's member list
# when a repair folds one of them away, its lead (and the title it quotes) when the
# old lead was the one folded. So a key that is textually new is listed only when
# no base advisory of the same class COVERS it - names every record it names (its
# entry, plus every slug its message quotes). What is left is an advisory naming a record, or a combination of
# records, the base never named together under that class: a new duplicate pair, a
# work JOINING an existing duplicate group, a new orphan person. Advisories a
# maintainer reviewed are absent from both sides already (metacheck honours
# internal/audit/reviewed.json's merge rejections), so a reviewed pair never
# returns as "new".
#
# EXCLUDED: oversized-entry, a STORAGE note whose message carries the entry's byte
# count, so every edit to an already-oversized work would read as new; it is not
# something a reviewer of the data can act on.
#
# BOUNDS: at most MAX_LINES advisory lines (env, default 40), each cut to
# MAX_WIDTH bytes (env, default 500) on a UTF-8 boundary, then one line saying how
# many more there were. The header counts every new advisory per class before the
# cut, so a cut list still says how large the delta was.
set -uo pipefail

MAX_LINES="${MAX_LINES:-40}"
MAX_WIDTH="${MAX_WIDTH:-500}"
EXCLUDED_CLASSES="oversized-entry"

# COVERED_AWK reads the textually new keys on stdin and prints the ones no base
# advisory COVERS: a base key of the same class naming every record the new one
# names. A key's records are its ENTRY (the locator "<family>: entry <slug>:")
# and every quoted run of slug characters in its message - every record a message
# names is quoted, and a title, which is free text, rarely is slug-shaped.
# shellcheck disable=SC2016 # awk's own $ fields, not the shell's
COVERED_AWK='
function records(s, set,    t) {
  split("", set)
  if (match(s, /^[a-z-]+: entry [a-z0-9-]+/)) {
    t = substr(s, RSTART, RLENGTH)
    set[substr(t, index(t, " entry ") + 7)] = 1
  }
  while (match(s, /"[a-z0-9]+(-[a-z0-9]+)*"/)) {
    set[substr(s, RSTART + 1, RLENGTH - 2)] = 1
    s = substr(s, RSTART + RLENGTH)
  }
}
BEGIN {
  while ((getline line < basefile) > 0) {
    tab = index(line, "\t")
    nb++
    bclass[nb] = substr(line, 1, tab - 1)
    records(substr(line, tab + 1), one)
    for (t in one) bhas[nb, t] = 1
  }
}
index(excluded, " " $1 " ") == 0 {
  records($2, mine)
  for (i = 1; i <= nb; i++) {
    if (bclass[i] != $1) continue
    all = 1
    for (t in mine) if (!((i, t) in bhas)) { all = 0; break }
    if (all) next
  }
  print
}'

usage() {
  echo "usage: advisory-delta.sh keys <metacheck> <rev> <out> | diff <base-keys> <head-keys> | summary <diff-output>" >&2
  exit 2
}

tmp=""
trap 'if [ -n "$tmp" ]; then rm -rf "$tmp"; fi' EXIT

keys() {
  local bin="$1" rev="$2" out="$3" status=0
  tmp="$(mktemp -d)" || return 1
  # The tree goes under its own data/ so metacheck sees the layout it always does;
  # the log sits beside it, outside the data root, or it would be a stray file.
  if ! git archive --format=tar "$rev" data | tar -x -C "$tmp"; then
    echo "advisory-delta: cannot extract data/ of $rev" >&2
    return 1
  fi
  rm -f "$out"
  # The exit status is not the answer: a tree with problems still has a census
  # (and the base of a pull request is main, which is green), so success is the
  # keys file existing. metacheck writes none for a load that built no catalogue.
  "$bin" -data "$tmp/data" --profile "${PROFILE:-core}" --advisory-keys "$out" >/dev/null 2>"$tmp/metacheck.log" || status=$?
  if [ ! -f "$out" ]; then
    echo "advisory-delta: metacheck (exit $status) wrote no advisory keys for $rev: $(tail -n 3 "$tmp/metacheck.log" | head -c 600)" >&2
    return 1
  fi
}

diff_keys() {
  local base="$1" head="$2" new total noun line
  if [ ! -f "$base" ] || [ ! -f "$head" ]; then
    echo "advisory-delta: missing keys file ($base, $head)" >&2
    exit 2
  fi
  # Byte order (LC_ALL=C, exported by the dispatch below) on both sides, as comm
  # needs; the inputs are re-sorted rather than trusted to arrive sorted.
  new="$(comm -13 <(sort -u "$base") <(sort -u "$head") |
    awk -F'\t' -v excluded=" $EXCLUDED_CLASSES " -v basefile="$base" "$COVERED_AWK")"
  [ -n "$new" ] || return 0
  total="$(printf '%s\n' "$new" | wc -l | tr -d ' ')"
  noun=advisories
  if [ "$total" = 1 ]; then
    noun=advisory
  fi
  printf '%s new %s (%s):\n' "$total" "$noun" \
    "$(printf '%s\n' "$new" | cut -f1 | sort | uniq -c | awk '{printf "%s%s %s", sep, $2, $1; sep=", "}')"
  while IFS=$'\t' read -r class rest; do
    line="- [$class] $rest"
    if [ "${#line}" -gt "$MAX_WIDTH" ]; then
      # A byte cut can split a multi-byte character; iconv -c drops the broken
      # tail rather than handing a consumer invalid UTF-8.
      line="$(printf '%s' "${line:0:$MAX_WIDTH}" | iconv -f utf-8 -t utf-8 -c)..."
    fi
    printf '%s\n' "$line"
  done < <(head -n "$MAX_LINES" <<<"$new")
  if [ "$total" -gt "$MAX_LINES" ]; then
    printf -- '- ... and %s more not shown\n' "$((total - MAX_LINES))"
  fi
}

summary() {
  local list="$1" fence='```'
  echo "### Advisories this pull request introduces"
  echo
  if [ ! -s "$list" ]; then
    echo "None: every advisory the merged tree holds, the base already held."
    return 0
  fi
  while grep -qF -- "$fence" "$list"; do
    fence="$fence\`"
  done
  echo "$fence"
  cat "$list"
  echo "$fence"
}

[ "$#" -ge 1 ] || usage
cmd="$1"
shift
case "$cmd" in
keys)
  [ "$#" -eq 3 ] || usage
  keys "$@"
  ;;
diff)
  [ "$#" -eq 2 ] || usage
  # Byte order for sort/comm, and lengths and substrings counted in BYTES:
  # MAX_WIDTH bounds what reaches a step summary or a model's context, which is
  # measured in bytes.
  export LC_ALL=C
  diff_keys "$@"
  ;;
summary)
  [ "$#" -eq 1 ] || usage
  summary "$@"
  ;;
*) usage ;;
esac
