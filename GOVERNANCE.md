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
