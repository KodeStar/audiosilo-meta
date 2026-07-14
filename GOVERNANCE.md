# Governance

How decisions get made and how changes get merged in audiosilo-meta. The guiding
principle: **CI does 100% of the mechanical verification, and humans only supply
judgement where machines cannot.**

## Roles

### Maintainer

Currently **[@KodeStar](https://github.com/KodeStar)**. Maintainers:

- own the schemas, the Go tooling, the CI workflows, and everything under
  `.github/` (changes there always need maintainer review - enforced by
  [`CODEOWNERS`](.github/CODEOWNERS));
- approve pull requests that are not eligible for auto-merge;
- promote Trusted Contributors, and handle disputes, vandalism, and takedowns;
- cut releases (the release workflow does this automatically on merge to `main`).

### Trusted Contributor

Contributors who have earned merge trust on **data** through a track record of
clean, correct, merged contributions. This is a ladder, the same model
DefinitelyTyped and tldr-pages use: you start as anyone, and after a body of
submissions that pass CI cleanly and need no maintainer rework, a maintainer
grants Trusted Contributor status.

Trusted Contributors' **data-only** pull requests may auto-merge once required
checks pass (see below). Trust is scoped to `data/` - it never extends to
schemas, tooling, or workflows.

There is no application form. Trust is offered based on observed track record.
Losing it (through a bad-faith or careless contribution) is equally at
maintainer discretion.

### Anyone

You do not need any status to contribute. Every submission is welcome; the only
difference the tiers make is whether a human has to click "approve" before a
green pull request merges.

## Merge policy

**Every pull request must pass CI** - `go build`, `go vet`, `go test`,
`metacheck` (schema, referential integrity, uniqueness) and `metafmt --check`
(canonical formatting). A red pull request never merges, regardless of who opened
it. CI comments the exact error back so it can be fixed.

On top of a green pull request:

| Change touches | Who can merge |
|---|---|
| `data/` only, opened by a **Trusted Contributor** | Auto-merges via GitHub-native auto-merge once required checks pass. No human step. |
| `data/` only, opened by **anyone else** | One maintainer approval, then merge. |
| `schema/`, `cmd/`, `internal/`, or `.github/` | **Always** one maintainer approval - never auto-merge. Enforced by `CODEOWNERS`. |

Auto-merge is GitHub's native feature (green required checks → merge), not a bot
that bypasses review. Mechanical correctness is proven by CI; the maintainer step
for untrusted or non-data changes is a judgement gate (is the data plausible, is
the source legitimate, is the schema change sound), not a re-check of what CI
already verified.

## Automated intake and AI verification

Two automations sit in front of the human review step. Neither bypasses it.

- **Issue-form intake → bot pull request.** A data issue-form submission (Add a
  work, Add a recording, Correct data, Add characters/recaps, Import a library)
  is routed by its `data:*` label to the `intake` workflow, which runs
  `cmd/metaissue` to compose the same canonical records a hand-authored pull
  request would carry, deduplicating against the catalog. On success it opens a
  `bot-intake` pull request on branch `intake/issue-<n>` crediting the submitter
  and naming the license layer; a submission that is a duplicate, needs a human,
  or is invalid is commented back on the issue instead (with a matching label).
  The bot only *drafts* the change - it runs the same untrusted-data-only,
  no-fork-execution security model as the rest of CI (`intake.yml` treats the
  issue body and attachments strictly as data).

- **AI verification (advisory).** The `ai-verify` workflow asks Claude to
  sanity-check a data pull request's diff for judgement a machine check cannot
  make (factual consistency, plausible provenance, the correct license layer,
  no copied publisher prose, sane sidecar spoiler positions and length). It
  posts a PASS/FLAG comment and applies an `ai-verified` or `ai-flagged` label.
  This is **advisory only**: it never blocks a merge, and a `flag` is a prompt
  for a maintainer to look closer, not a veto. It runs on same-repo branches;
  fork pull requests are verified only after a maintainer pushes the branch to
  the repository (fork runs have no secret and a read-only token by design - the
  repo never adopts `pull_request_target`).

**Merge policy is unchanged by these automations.** A `bot-intake` pull request
is treated exactly like one "opened by anyone else": it must pass CI, and it
still requires **one maintainer approval** before it merges. A green
`ai-verified` label does **not** enable auto-merge. Auto-merge for bot-drafted or
AI-verified pull requests is a **deliberate future toggle** - it stays off until
the pipeline has earned trust through a track record of clean, correctly-composed
submissions, at which point widening the auto-merge scope (like the Trusted
Contributor ladder) is a maintainer decision made openly.

## Disputes

Data disagreements (which recording is canonical, how a series is ordered, a
contested fact) are resolved on the pull request or a linked issue, with sources.
Because the data is a wiki-style factual database, **wrongness is fixable** - a
later correction pull request is the normal remedy, not a veto up front. Where
contributors cannot agree, the maintainer decides, favouring the
better-sourced position.

## Vandalism and revert-first

Deliberate vandalism, spam, or bad-faith edits are handled **revert-first**: the
change is reverted immediately to restore a known-good state, and the discussion
(if any) happens afterwards. Repeated bad-faith behaviour results in loss of
Trusted Contributor status and, for serious or repeated cases, a block. See the
[Code of Conduct](CODE_OF_CONDUCT.md).

Because the repository *is* the database (plain files in git), any bad state is
fully recoverable from history, and the SQLite release artifact is only rebuilt
from a merged, green `main` - so a reverted change never reaches consumers.

## Changing this document

Governance changes are maintainer decisions, proposed and discussed openly (an
issue or pull request) before they take effect. As the contributor base grows,
expect the tiers and the auto-merge scope to be revisited.
