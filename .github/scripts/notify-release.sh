#!/usr/bin/env bash
# notify-release.sh - tell a running metaserve that a new data release is up.
#
# Usage: bash .github/scripts/notify-release.sh [--print]
#   env: WEBHOOK_URL, WEBHOOK_SECRET (the METASERVE_* Actions secrets),
#        GITHUB_REPOSITORY (the repo the release was cut from).
#
# The payload is only a TRIGGER: metaserve re-queries GitHub and goes through the
# same refresh path the poller does, checksums and atomic swap included. What
# this script has to get exactly right is therefore not the content but the
# ENVELOPE - the bytes and the HMAC over them - which is the half that broke
# silently once already: composed with `printf` inside single quotes, the
# backslashes were literal, the body was not JSON, webhook.go rejected every
# delivery with 400, and hourly polling hid it for as long as it took somebody to
# read a log.
#
# So the sender is a script rather than twenty lines of YAML, and it is PINNED TO
# THE RECEIVER BY A TEST: internal/serve's TestReleaseNotifyScript* runs this in
# --print mode and posts what it emits to the real handler through httptest, so
# the two cannot drift again. That is scripts/packmerge_test.go's precedent - a
# shell script exercised the way it is used, from the Go suite.
#
#   --print  compose and sign, then write "<signature>\n<payload>" to stdout and
#            send nothing. WEBHOOK_SECRET is required; WEBHOOK_URL is not.
#
# Delivery is VERIFIED but never fatal: the HTTP status is what tells a rejected
# payload (400) apart from an unreachable receiver, and polling is the documented
# recovery path, so a published release is not failed by a deployment that is
# down. Absent secrets are likewise a message and exit 0 - most forks and every
# pre-deployment run have none.
set -euo pipefail

print_only=false
case "${1:-}" in
  --print | --dry-run) print_only=true ;;
  "") ;;
  *)
    echo "usage: notify-release.sh [--print]" >&2
    exit 2
    ;;
esac

: "${GITHUB_REPOSITORY:?GITHUB_REPOSITORY must be set}"

if [ "$print_only" = true ]; then
  : "${WEBHOOK_SECRET:?--print needs WEBHOOK_SECRET to sign with}"
elif [ -z "${WEBHOOK_URL:-}" ] || [ -z "${WEBHOOK_SECRET:-}" ]; then
  echo "metaserve webhook secrets are not configured; fallback polling will refresh it"
  exit 0
fi

# jq, not printf: it escapes the repository name for us and emits real JSON.
payload="$(jq -cn --arg repo "$GITHUB_REPOSITORY" '{action:"published",repository:{full_name:$repo}}')"
signature="$(PAYLOAD="$payload" python3 - <<'PY'
import hashlib
import hmac
import os

digest = hmac.new(
    os.environ["WEBHOOK_SECRET"].encode(),
    os.environ["PAYLOAD"].encode(),
    hashlib.sha256,
).hexdigest()
print("sha256=" + digest)
PY
)"

if [ "$print_only" = true ]; then
  # No trailing newline: the payload is the signed byte string, verbatim.
  printf '%s\n%s' "$signature" "$payload"
  exit 0
fi

# The signature is computed over exactly these bytes: $payload is what was
# signed and --data sends it verbatim.
rc=0
status="$(curl --fail --silent --show-error --retry 3 --retry-all-errors --max-time 15 \
  --output /dev/null --write-out '%{http_code}' \
  --request POST \
  --header "Content-Type: application/json" \
  --header "X-GitHub-Event: release" \
  --header "X-Hub-Signature-256: $signature" \
  --data "$payload" \
  "$WEBHOOK_URL")" || rc=$?
if [ "$rc" -ne 0 ]; then
  echo "::warning::metaserve webhook delivery failed (HTTP ${status:-000}, curl exit ${rc}); fallback polling will retry"
else
  echo "metaserve accepted the release notification (HTTP ${status})"
fi
