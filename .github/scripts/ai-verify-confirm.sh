#!/usr/bin/env bash
# ai-verify-confirm.sh - read the steward's confirmation evidence for a flagged
# pull request, when there is any for exactly this content.
#
# Usage: ai-verify-confirm.sh <context-file> <pr-number> <confirm-out>
#
# Always writes <confirm-out>: the evidence as one JSON object
#   {"records": "<the retailer records>", "answers": "<the resolver's answers>"}
# or an EMPTY file when there is none (ai-verify-reuse.sh hashes it with the
# context, and an empty file adds nothing to the key, so a run without evidence
# keys exactly as before). Writes to $GITHUB_OUTPUT (stdout when unset):
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
#   - Only comments whose author is that login AND whose author_association is
#     OWNER, MEMBER or COLLABORATOR count, for the confirmation and for its
#     withdrawal alike (a renamed or deleted account whose login is taken by a
#     stranger is not a collaborator).
#   - WITHDRAWAL. When a confirmation ends without a pass (a second flag, no
#     second read, a re-run it may not ask for), the steward withdraws it: a
#     comment (its park comment, when it parks) whose last line that is not
#     blank is
#       <!-- audiosilo-steward:confirm-withdrawn context=<64 hex> -->
#     A withdrawal of this run's context that was never edited means no
#     evidence, whatever its time: the steward never confirms a content again
#     once it has withdrawn it, so the two reads that disagreed are never
#     followed by a third that reads the same evidence (the daily rebase of a
#     mirror re-runs this workflow on the same content). A withdrawal of other
#     content, or an edited one, is passed over.
#   - Of the other comments naming the steward's confirm marker, the NEWEST is
#     the one judged, and it is accepted only when it was never edited (its
#     updated_at is its created_at: anyone with write access can edit a
#     comment, and the API still names its author), its body holds exactly ONE
#     line naming the marker, that line is the whole marker
#       <!-- audiosilo-steward:confirm context=<64 hex> head=<40 hex> flag=<64 hex> -->
#     and is the comment's LAST line (so after the evidence block), and its
#     context is the sha256 of <context-file>, the summary this run shows the
#     model. The last line because the same account posts comments relaying an
#     agent's text (an amend's summary, a review), and the steward ends every
#     comment with lines of its own, so relayed text that forges a marker and a
#     block is never a comment's last line. The context binds the
#     evidence to the content it answers rather than to a head: the intake sweep
#     rebases a mirror on every push to main, and a rebase over an unrelated
#     merge leaves the context byte for byte the same (ai-verify-reuse.sh), while
#     any change to an entry the pull request touches changes it.
#   - The evidence is the text between the full lines
#       <!-- confirm-evidence:begin -->   and   <!-- confirm-evidence:end -->
#     holding two sub-blocks, the resolver's answers and then the retailer
#     records the steward's service rendered:
#       <!-- confirm-answers:begin --> ... <!-- confirm-answers:end -->
#       <!-- confirm-records:begin --> ... <!-- confirm-records:end -->
#     Each delimiter is named on exactly one line, which is the whole
#     delimiter, all six in that order, and neither sub-block is empty. The two
#     are kept apart so an answer can never pass for a record: ai-verify.sh
#     shows them as two sections and calls only the records evidence, and the
#     steward breaks every delimiter's name in the text it relays. The answers
#     are kept whole and the records cut at a row, so the two together fit
#     MAX_CONFIRM_BYTES; both are made valid UTF-8.
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

# Clears <confirm-out> again, so no evidence is always an empty file, even
# when the write of the evidence is what failed part way.
none() {
  : > "$CONFIRM_OUT"
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

# The steward's trusted comments naming either marker, each with its last line
# that is not blank (a carriage return and a NUL dropped, as below). A
# withdrawal is one whose last line is the whole withdrawal marker; it is never
# a confirmation, edited or not. Of the rest, the newest is judged below.
# withdrawn: an unedited withdrawal of this run's context.
selected="$(printf '%s\n' "$comments" | jq -cs --arg login "$STEWARD_LOGIN" \
  --arg withdrawal "<!-- audiosilo-steward:confirm-withdrawn context=$CONTEXT_SHA -->" '
  map(select(
      ((.user.login // "") | ascii_downcase) == ($login | ascii_downcase)
      and (.author_association | IN("OWNER", "MEMBER", "COLLABORATOR"))
      and ((.body // "") | contains("audiosilo-steward:confirm")))
    | . + {edited: ((.updated_at // .created_at) != .created_at),
        last: ((.body // "") | explode | map(select(. != 13 and . != 0)) | implode
          | split("\n") | map(select(test("[^ \t]"))) | last // "")})
  | {withdrawn: any(.[]; .edited == false and .last == $withdrawal),
     picked: (map(select(.last | startswith("<!-- audiosilo-steward:confirm-withdrawn ") | not)
         | {id, url: .html_url, body, edited, last, created_at})
       | sort_by(.created_at, .id) | last)}')" ||
  none "the comments could not be parsed"
[ "$(jq -r '.withdrawn' <<<"$selected")" = false ] || none "the steward withdrew its confirmation of this content"
picked="$(jq -c '.picked // empty' <<<"$selected")"
[ -n "$picked" ] || none "no comment by the steward carries its marker"

id="$(jq -r '.id' <<<"$picked")"
url="$(jq -r '.url // ""' <<<"$picked")"
[[ "$id" =~ ^[0-9]+$ ]] || none "the steward's comment has no numeric id"
[[ "$url" =~ ^https://github\.com/[A-Za-z0-9._/#-]+$ ]] || none "the steward's comment has no GitHub link"
[ "$(jq -r '.edited' <<<"$picked")" = false ] || none "the steward's comment was edited after it was posted"

# Line structure only: a carriage return (a comment edited on Windows) and a NUL
# (bash cannot hold one) are dropped before any line is judged.
body="$(jq -r '.body' <<<"$picked" | tr -d '\r\0')"

# Every line NAMING the marker or a delimiter is counted, so a near miss
# (indented, misspelled field, a second copy inside the evidence) is refused
# rather than passed over in favour of a well-formed one elsewhere. The marker
# must be the last line that is not blank (the selection's `last`), which also
# puts it after the block. Each delimiter's line number is its exact line's, 0
# when there is none.
read -r markers counts lines < <(awk '
  BEGIN { n = split("evidence:begin answers:begin answers:end records:begin records:end evidence:end", d, " ") }
  /audiosilo-steward:confirm/ { m++ }
  { for (i = 1; i <= n; i++) if (index($0, "confirm-" d[i])) { c[i]++; if ($0 == "<!-- confirm-" d[i] " -->") at[i] = NR } }
  END {
    cs = ""; ls = ""
    for (i = 1; i <= n; i++) { cs = cs (i > 1 ? "," : "") c[i] + 0; ls = ls (i > 1 ? "," : "") at[i] + 0 }
    printf "%d %s %s\n", m, cs, ls
  }' <<<"$body")
[ "$markers" -eq 1 ] || none "the steward's comment names its marker on $markers lines, not one"
[ "$counts" = "1,1,1,1,1,1" ] || none "the steward's comment does not name each evidence delimiter once ($counts)"
IFS=, read -r evidence_begin answers_begin answers_end records_begin records_end evidence_end <<<"$lines"
[ "$evidence_begin" -gt 0 ] && [ "$answers_begin" -gt "$evidence_begin" ] && [ "$answers_end" -gt "$answers_begin" ] &&
  [ "$records_begin" -gt "$answers_end" ] && [ "$records_end" -gt "$records_begin" ] &&
  [ "$evidence_end" -gt "$records_end" ] ||
  none "the steward's evidence is not one block of its answers and then its records"
marker="$(jq -r '.last' <<<"$picked")"
[[ "$marker" =~ ^\<!--\ audiosilo-steward:confirm\ context=([0-9a-f]{64})\ head=[0-9a-f]{40}\ flag=[0-9a-f]{64}\ --\>$ ]] ||
  none "the steward's comment does not end with its whole marker"
[ "${BASH_REMATCH[1]}" = "$CONTEXT_SHA" ] ||
  none "the steward's evidence answers other content (context ${BASH_REMATCH[1]:0:12}, this run's ${CONTEXT_SHA:0:12})"

# between <from> <to>: the body's lines strictly between the two.
between() { awk -v from="$1" -v to="$2" 'NR > from && NR < to' <<<"$body"; }
answers="$(between "$answers_begin" "$answers_end" | iconv -f utf-8 -t utf-8 -c)"
records="$(between "$records_begin" "$records_end" | iconv -f utf-8 -t utf-8 -c)"
[ -n "${answers//[[:space:]]/}" ] || none "the steward's evidence holds no answers"
[ -n "${records//[[:space:]]/}" ] || none "the steward's evidence holds no records"

# The cap: the answers whole (the steward bounds them well below it), the
# records cut at a row to the room that is left, saying so. A cut at a newline
# never splits a character.
answer_bytes="$(printf '%s' "$answers" | wc -c)"
room=$((MAX_CONFIRM_BYTES - answer_bytes))
if [ "$(printf '%s' "$records" | wc -c)" -gt "$room" ]; then
  note="$(printf '[the retailer records were cut at a row to keep the evidence within %d bytes]' "$MAX_CONFIRM_BYTES")"
  keep=$((room - ${#note} - 1))
  [ "$keep" -gt 0 ] || none "the steward's answers leave no room for its records"
  # The x keeps a newline the cut ends on, which $(...) would strip (and with
  # it the row it ends).
  kept="$(printf '%s' "$records" | head -c "$keep"; printf x)"
  kept="${kept%x}"
  [[ "$kept" == *$'\n'* ]] || none "the steward's answers leave no room for its records"
  records="${kept%$'\n'*}"
  [ -n "${records//[[:space:]]/}" ] || none "the steward's answers leave no room for its records"
  records="$records"$'\n'"$note"
fi

jq -n --arg records "$records" --arg answers "$answers" '{records: $records, answers: $answers}' > "$CONFIRM_OUT" ||
  none "the evidence could not be written"

echo "ai-verify-confirm: read the steward's confirmation evidence (comment $id, context ${CONTEXT_SHA:0:12})"
{
  echo "found=true"
  echo "id=$id"
  echo "url=$url"
} >> "$OUT"
