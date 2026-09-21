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
| `data/` only, opened by the **series-completion bot** (`bot-sync`) | Merges itself once required checks pass **and** `ai-verify` has applied `ai-verified`. The one automated exception, bounded by "Series-completion bot" below; anything else parks with `sync:needs-human` for a maintainer. |
| `schema/`, `cmd/`, `internal/`, or `.github/` | **Always** one maintainer approval - never auto-merge. Enforced by `CODEOWNERS`. |

Auto-merge is GitHub's native feature (green required checks → merge), not a bot
that bypasses review. Mechanical correctness is proven by CI; the maintainer step
for untrusted or non-data changes is a judgement gate (is the data plausible, is
the source legitimate, is the schema change sound), not a re-check of what CI
already verified.

**Batch imports** (a pull request adding many records from one external source)
get extra scrutiny regardless of who opens them: the source must be named and
permitted (LICENSING.md's bounded import posture), the selection rationale
stated (never an unbounded mirror), the records stamped with the source's typed
`sources[]` entry so the whole source stays retractable, and the batch landed in
reviewable tranches rather than one giant diff. **A batch import opened by a
person, or composed from the "Import a library" issue form, never auto-merges**:
a maintainer approves each tranche, whoever opened it and however green it is.
The single carve-out is the **series-completion bot** below, whose pull requests
are batch imports by this definition: its selection rationale is enforced
mechanically instead of merely stated - only works filling free positions in
series the catalogue already holds, `data/` only, capped per cycle - and the
automated verifier's `ai-verified` verdict is the review that bound pays for.
Everything else in this paragraph applies to it unchanged.

**Overwriting an existing record.** Imports do not normally rewrite what is
already recorded - a recorded value wins and a run fills only what is absent.
The one exception is the trust-tier rule in
[LICENSING.md](LICENSING.md#trust-tiers-and-the-user-overwrite-rule): a record
whose provenance is nothing but a bulk-mirror import has never been attested by
a person, so the first user-library import that matches it by ASIN replaces its
facts and takes over its provenance. After that the ordinary rules resume, and
where two users disagree the first writer's value stands. The disagreement is
surfaced in the pull request for a maintainer to adjudicate **when the import
rules detect it** - a runtime more than 10% apart, or a release date that is not
the same date at another precision. A differing publisher spelling, cover URL or
chapter table is not detected: the recorded value stands silently and a
correction is the route to changing it. Either way the review step is never
bypassed, and the catalogue never churns between contributors. A form
submission that duplicates a mirror-seeded record is routed to a maintainer
(`data:needs-human`) rather than closed as a duplicate, because the bot can only
compose new records and the submitter's data should win.

## Automated intake and AI verification

Two automations sit in front of the human review step. Neither bypasses it.

- **Issue-form intake → bot pull request.** A data issue-form submission (Add a
  work, Add a recording, Correct data, Import a library) is routed by its
  `data:*` label to the `intake` workflow, which runs
  `cmd/metaissue` to compose the same canonical records a hand-authored pull
  request would carry, deduplicating against the catalog. On success it opens a
  `bot-intake` pull request on branch `intake/issue-<n>` crediting the submitter
  and naming the license layer; a submission that is a duplicate, needs a human,
  or is invalid is commented back on the issue instead (with a matching label).
  The bot only *drafts* the change - it runs the same untrusted-data-only,
  no-fork-execution security model as the rest of CI (`intake.yml` treats the
  issue body and attachments strictly as data).

  The two **sidecar** forms (Add characters, Add recaps) moved to
  [audiosilo-meta-community](https://github.com/KodeStar/audiosilo-meta-community)
  with the CC BY-SA layer itself, and are composed there by the same
  `cmd/metaissue` under the `community` tree profile. The review bar differs
  between the two repositories on purpose - core pull requests are facts a
  mechanical check can verify, community ones are prose a human has to judge for
  spoiler policy, own-words policy and quality - which is one of the reasons the
  split happened.

  Because records share pack files (see [PACK-SPEC.md](PACK-SPEC.md)), an open
  bot pull request can start conflicting when another one merges. The same
  workflow **rebases its own branches** after every push to `main` that touches
  the data: it three-way merges the two sides' records (the merge base included,
  so a deliberate deletion is never handed back), re-renders with
  `metafmt --write`, re-validates with `metacheck`, and force-pushes. Anything it
  cannot resolve mechanically - the same record edited on both sides, or one side
  deleting what the other edited - is left alone with a comment asking for a
  maintainer. It only ever touches branches this workflow created,
  and it runs on `push` to `main`, so no fork code is executed. Humans use the
  same recipe (CONTRIBUTING.md, "When two pull requests touch the same pack").

- **AI verification (advisory).** The `ai-verify` workflow asks Claude to
  sanity-check a data pull request's diff for judgement a machine check cannot
  make (factual consistency, plausible provenance, the correct license layer,
  no copied publisher prose, sane sidecar spoiler positions and length). It
  posts a PASS/FLAG comment and applies an `ai-verified` or `ai-flagged` label.
  The VERDICT is **advisory only**: a `flag` is a prompt for a maintainer to
  look closer, not a veto, and the check is not branch protected, so neither
  verdict blocks a merge. The job's COLOUR answers a different question -
  whether a verdict happened at all - and a verification that could not run
  leaves it red rather than green; the rule and the reason live on
  `.github/scripts/ai-verify.sh` (the `EXIT STATUS` block). It runs on same-repo
  branches only; fork pull requests are verified after a maintainer pushes the
  branch to the repository or re-runs the workflow by hand (fork runs have no
  secret and a read-only token by design - the repo never adopts
  `pull_request_target`).

**Merge policy is unchanged by these automations.** A `bot-intake` pull request
is treated exactly like one "opened by anyone else": it must pass CI, and it
still requires **one maintainer approval** before it merges. A green
`ai-verified` label does **not** enable auto-merge. Auto-merge for bot-drafted or
AI-verified pull requests was a **deliberate future toggle**, and it has since
been turned on for **exactly one** automation - the series-completion bot in the
next section, which buys it with a bound a machine enforces rather than with a
track record. For every other bot-drafted or AI-verified pull request the toggle
stays off until the pipeline has earned trust through a record of clean,
correctly-composed submissions, and widening it any further (like the Trusted
Contributor ladder) is a maintainer decision made openly.

## Series-completion bot (audiosilo-meta-sync)

The one automation allowed to merge its own work.
[`KodeStar/audiosilo-meta-sync`](https://github.com/KodeStar/audiosilo-meta-sync)
is a private service that runs daily: it reads libex's new-releases and
coming-soon feeds across every marketplace region, keeps only the rows that
**complete series this catalogue already holds**, and opens one pull request per
cycle. This section is the whole of its licence to do that; nothing else in the
merge policy moves.

**The bound.** Selection is the existing `metaimport libex-select` tranche
selector, which refuses any row whose series the catalogue does not already
track and any row whose position in that series is already filled. So the bot
can add a volume to a series we hold; it can never add a **series**, and it can
never contest an occupied position. It writes under `data/` and nowhere else -
never a schema, never the tooling, never a workflow (`CODEOWNERS` would stop it
anyway). One pull request per cycle, capped at about 100 new works, so a cycle
stays a thing a person can read. Alongside the new works it runs `metaimport
libex --enrich` over the touched series' existing members, which fills only
absent facts - a missing cover or chapter table, a year-only release date
refined to the stated day - and never overwrites a recorded value. Coming-soon
(preorder) titles are imported with their announced release date. Every record
it writes carries the typed `libex-import` provenance, so the whole source stays
retractable in one act and the trust tiers rank it exactly as any other
bulk-mirror record: the first user-library import that matches it takes the
record over.

**The merge gate.** The bot merges its own pull request only when the required
checks (`check`, `compose`) are green **and** the `ai-verify` workflow has
applied `ai-verified`. Here - and only here - that verdict is not advisory: it
is the review step standing in for the maintainer approval a batch import
otherwise needs. A failed required check parks the pull request; so does an
`ai-flagged` verdict, except that a resolver (a Claude or Codex CLI on a
subscription login) may first amend it **at most twice**. The resolver edits
`data/` only and only the records this pull request added - it may drop a
flagged record entirely, and it never touches a record that was here before. If
the flag survives two amendments the pull request is labelled `sync:needs-human`
and left for a maintainer. The bot stops opening new pull requests while
**three** parked ones are open, so a systematic fault produces a short queue
rather than a backlog.

**Accountability.** Every merge is a pull request, so the record is the ordinary
reviewable one: labels `data`, `bot-intake` and `bot-sync`, a per-series table
of what the tranche adds, the required checks and the verifier's verdict on it,
and a comment on the pull request describing whatever the resolver changed. A
bad cycle reverts like any other change (revert-first, below), and because the
provenance is typed, an entire source retracts in one pass.

**Kill switch.** Revoke the bot's personal access token, or stop its container.
It has no other way into this repository, and nothing here depends on it
running.

**Widening is a maintainer decision made openly**, exactly like the ladder it
came from. A new source, a new kind of record, authority to create a series, a
larger cap, or the same automation pointed at
[audiosilo-meta-community](https://github.com/KodeStar/audiosilo-meta-community)
(whose review bar is prose a human has to judge, which is why the repositories
split) is a change to this document first and a change to the bot second.

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
