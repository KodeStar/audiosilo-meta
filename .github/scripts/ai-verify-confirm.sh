#!/usr/bin/env bash
# ai-verify-confirm.sh - read the steward's confirmation evidence for a flagged
# pull request, when there is any for exactly this content.
#
# Usage: ai-verify-confirm.sh <context-file> <pr-number> <confirm-out>
#
# Always writes <confirm-out>: the evidence, or an EMPTY file when there is none
# (ai-verify-reuse.sh hashes it with the context, and an empty file adds nothing
# to the key, so a run without evidence keys exactly as before). Writes to
# $GITHUB_OUTPUT (stdout when unset):
#   found=true|false   whether evidence was read
#   id=<number>        the steward comment's id, when found
#   url=<html url>     the steward comment's link, when found
#
# WHAT IT IS. When ai-verify flags a pull request, audiosilo-meta-sync's
# steward may have its resolver re-check each flagged item. A resolver that
# changes nothing answers every item beside retailer records the service
# fetched from libex, and the steward posts that as a comment and re-runs this
# workflow. ai-verify.sh then reads the evidence (CONFIRM_FILE) as a second
# input, and a verdict that still flags parks the pull request for a person.
# The evidence never clears a flag by itself: only the model's own verdict does.
#
# TRUST. A pull request comment is anybody's, so this reads exactly one shape
# from exactly one account, and reads nothing at all otherwise:
#   - STEWARD_LOGIN (env, the repository variable) names the steward's account.
#     Unset or empty, no comment is ever read: the gate fails closed.
#   - The comment's author is that login AND its author_association is OWNER,
#     MEMBER or COLLABORATOR (a renamed or deleted account whose login is taken
#     by a stranger is not a collaborator).
#   - Of those comments carrying the steward's marker, the NEWEST is the one
#     judged, and it is accepted only when its body holds exactly ONE line
#     naming the marker, that line is the whole marker
#       <!-- audiosilo-steward:confirm context=<64 hex> head=<40 hex> flag=<64 hex> -->
#     and stands outside the evidence block, and its context is the sha256 of
#     <context-file>, the summary this run shows the model. That binds the
#     evidence to the content it answers rather than to a head: the intake sweep
#     rebases a mirror on every push to main, and a rebase over an unrelated
#     merge leaves the context byte for byte the same (ai-verify-reuse.sh), while
#     any change to an entry the pull request touches changes it.
#   - The evidence is the text between the full lines
#       <!-- confirm-evidence:begin -->   and   <!-- confirm-evidence:end -->
#     (exactly one of each, in that order, and not empty), cut to
#     MAX_CONFIRM_BYTES with a line saying so, and made valid UTF-8.
# An older comment is never looked past to: a newest one that fails any rule
# means no evidence, which is a verdict judged as if none had been posted.
#
# SECURITY: the evidence is UNTRUSTED DATA (its answers are an agent's). It is
# written to a file and reaches the model only through ai-verify.sh's jq
# --rawfile or stdin, never a shell command or an argument, as the context does.
#
# COMMENTS_FILE (a JSON array of issue comments) replaces the API read: the
# workflow lists the comments once for this script and its neighbour, and the
# tests hand in their own. A file that cannot be read is no comments.
set -uo pipefail

CONTEXT="${1:?context file required}"
PR="${2:?pull request number required}"
CONFIRM_OUT="${3:?confirm output path required}"
OUT="${GITHUB_OUTPUT:-/dev/stdout}"
MAX_CONFIRM_BYTES=32768

: > "$CONFIRM_OUT" || { echo "ai-verify-confirm: cannot write $CONFIRM_OUT" >&2; exit 2; }

none() {
  echo "ai-verify-confirm: no confirmation evidence ($1)"
  echo "found=false" >> "$OUT"
  exit 0
}

STEWARD_LOGIN="${STEWARD_LOGIN:-}"
[ -n "$STEWARD_LOGIN" ] || none "no STEWARD_LOGIN repository variable is set"

CONTEXT_SHA="$(sha256sum < "$CONTEXT" | cut -d' ' -f1)"
[[ "$CONTEXT_SHA" =~ ^[0-9a-f]{64}$ ]] || none "the context could not be hashed"

if [ -n "${COMMENTS_FILE:-}" ]; then
  comments="$(jq -c '.[]' "$COMMENTS_FILE")" || none "the comments could not be read"
else
  comments="$(gh api --paginate "repos/${GITHUB_REPOSITORY:?}/issues/$PR/comments" --jq '.[]')" ||
    none "the comments could not be read"
fi

# The newest comment by the steward's account, as a trusted collaborator, that
# names the marker at all; whether it names it correctly is judged below.
picked="$(printf '%s\n' "$comments" | jq -cs --arg login "$STEWARD_LOGIN" '
  map(select(
    ((.user.login // "") | ascii_downcase) == ($login | ascii_downcase)
    and (.author_association | IN("OWNER", "MEMBER", "COLLABORATOR"))
    and ((.body // "") | contains("audiosilo-steward:confirm"))))
  | sort_by(.created_at) | last // empty
  | {id, url: .html_url, body}')" || none "the comments could not be parsed"
[ -n "$picked" ] || none "no comment by the steward carries its marker"

id="$(jq -r '.id' <<<"$picked")"
url="$(jq -r '.url // ""' <<<"$picked")"
[[ "$id" =~ ^[0-9]+$ ]] || none "the steward's comment has no numeric id"
[[ "$url" =~ ^https://github\.com/[A-Za-z0-9._/#-]+$ ]] || none "the steward's comment has no GitHub link"

# Line structure only: a carriage return (a comment edited on Windows) and a NUL
# (bash cannot hold one) are dropped before any line is judged.
body="$(jq -r '.body' <<<"$picked" | tr -d '\r\0')"

# Every line NAMING the marker or a block delimiter is counted, so a near miss
# (indented, misspelled field, a second copy inside the evidence) is refused
# rather than passed over in favour of a well-formed one elsewhere.
read -r markers marker_at begins begin_at ends end_at < <(awk '
  /audiosilo-steward:confirm/ { m++; ml = NR }
  /confirm-evidence:begin/ { b++; if ($0 == "<!-- confirm-evidence:begin -->") bl = NR }
  /confirm-evidence:end/ { e++; if ($0 == "<!-- confirm-evidence:end -->") el = NR }
  END { printf "%d %d %d %d %d %d\n", m, ml, b, bl, e, el }' <<<"$body")
[ "$markers" -eq 1 ] || none "the steward's comment names its marker on $markers lines, not one"
[ "$begins" -eq 1 ] && [ "$ends" -eq 1 ] && [ "$begin_at" -gt 0 ] && [ "$end_at" -gt "$begin_at" ] ||
  none "the steward's comment has no single evidence block"
if [ "$marker_at" -gt "$begin_at" ] && [ "$marker_at" -lt "$end_at" ]; then
  none "the steward's marker sits inside the evidence block"
fi
marker="$(sed -n "${marker_at}p" <<<"$body")"
[[ "$marker" =~ ^\<!--\ audiosilo-steward:confirm\ context=([0-9a-f]{64})\ head=[0-9a-f]{40}\ flag=[0-9a-f]{64}\ --\>$ ]] ||
  none "the steward's marker line is not the whole marker"
[ "${BASH_REMATCH[1]}" = "$CONTEXT_SHA" ] ||
  none "the steward's evidence answers other content (context ${BASH_REMATCH[1]:0:12}, this run's ${CONTEXT_SHA:0:12})"

evidence="$(awk -v from="$begin_at" -v to="$end_at" 'NR > from && NR < to' <<<"$body")"
[ -n "${evidence//[[:space:]]/}" ] || none "the steward's evidence block is empty"

# head -c can split a multi-byte character at the cut; iconv -c drops what is
# left of it, as ai-verify.sh does for the context.
{
  printf '%s\n' "$evidence" | head -c "$MAX_CONFIRM_BYTES" | iconv -f utf-8 -t utf-8 -c
  if [ "$(printf '%s\n' "$evidence" | wc -c)" -gt "$MAX_CONFIRM_BYTES" ]; then
    printf '\n[the evidence was cut at %d bytes]\n' "$MAX_CONFIRM_BYTES"
  fi
} > "$CONFIRM_OUT" || none "the evidence could not be written"

echo "ai-verify-confirm: read the steward's confirmation evidence (comment $id, context ${CONTEXT_SHA:0:12})"
{
  echo "found=true"
  echo "id=$id"
  echo "url=$url"
} >> "$OUT"
