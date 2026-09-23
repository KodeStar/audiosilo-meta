#!/usr/bin/env bash
# require-on-main.sh - refuse to publish from a commit that is not on main.
#
# Usage: bash .github/scripts/require-on-main.sh "<what the operator should do>"
#
# Two workflows publish things the world then consumes from a ref a human chose:
# release.yml on `workflow_dispatch` (a data release becomes THE catalogue for
# every consumer the moment it lands, since they all select the newest release by
# asset presence) and image.yml on a `v*` tag push (a tag matches any commit on
# any branch, and the image is published under names people deploy from). Their
# other triggers are already main-only; these two doors take any ref, so the ref
# is checked rather than trusted. The guard was the same eight lines in both
# files, which is why it lives here.
#
# THE MECHANISM. Both checkouts are shallow (git history is not load-bearing in
# this repository - every record carries its own added_at), so main's commits
# have to be fetched before merge-base can answer at all. They are fetched
# TREELESS (--filter=tree:0): the question is reachability, which needs commit
# objects and nothing else, and a full unshallow of a 133k-work data tree is not
# what this check is worth.
#
# The shallowness is ASKED rather than assumed: --unshallow is an error on a
# complete repository, and actions/checkout's fetch-depth is a setting a caller
# can change. (A single `--depth=2147483647` does cover both cases - verified
# locally against a `clone --depth 1` and a complete clone - but the local
# transport ignores --filter, so the combination that actually runs here is not
# what was exercised. The branch is.)
#
# The message names HEAD, because the ref that was dispatched is the one thing
# the operator has to change; $1 is the caller's own sentence about how.
set -euo pipefail

remedy="${1:-Publish from a commit that has been merged to main.}"

if [ "$(git rev-parse --is-shallow-repository)" = "true" ]; then
  git fetch --no-tags --unshallow --filter=tree:0 origin main
else
  git fetch --no-tags --filter=tree:0 origin main
fi

if ! git merge-base --is-ancestor HEAD FETCH_HEAD; then
  echo "::error::$(git rev-parse HEAD) is not on origin/main. ${remedy}"
  exit 1
fi
