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
| `data/` only, opened by **anyone else** | One maintainer approval, then merge. For a pull request into `main` that approval is given by the maintainer's **steward** once required checks pass **and** `ai-verify` has applied `ai-verified` to its exact head - see "The steward" below. That covers a branch of this repository (intake, steward and contributor branches alike) and a fork pull request the steward mirrors to a branch here. A fork pull request it cannot mirror still waits for a person. |
| `data/` only, opened by the **series-completion bot** (`bot-sync`) | Merges itself once required checks pass **and** `ai-verify` has applied `ai-verified`. The one automated exception, bounded by "Series-completion bot" and "The LibriVox lane" below; a pull request that cannot reach that gate is closed by the bot (`sync:auto-excluded`), never merged. |
| `schema/`, `cmd/`, `internal/`, or `.github/` | **Always** one maintainer approval - never auto-merge. Enforced by `CODEOWNERS`. The one exception is a Dependabot update the steward merges on its own gate (see "The steward" below). |

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
series the catalogue already holds (plus other editions of the volumes already
at those positions, attached as recordings) and, in its reported-ASIN lane and
under that lane's own lower rate, reported standalones; `data/` only, capped per
cycle - and the
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
correction is the route to changing it. Genres are the one exception to
"replace": a user library states one category per book, so its genres are
added to the recorded set rather than replacing it (LICENSING.md, rule 5). Either way the review step is never
bypassed, and the catalogue never churns between contributors. A form
submission (Add a work, Add a recording) that names a mirror-seeded recording
BY ASIN is applied by the intake bot itself, through the same attestation a
library import performs: its pull request carries the modified record, and a
maintainer still approves it like any other `bot-intake` pull request. A
submission that disagrees with the seed on runtime or release date, that
describes a different book than the record its ASIN names (authors, narrators or
language, or an ISBN another recording carries), or that meets a mirror-seeded
record only by title, ISBN or narrator set (not an ASIN match, so not the
rule's), is routed to a maintainer (`data:needs-human`) with a message saying
which, rather than closed as a duplicate.

## Automated intake and AI verification

Two automations sit in front of the human review step. Neither bypasses it.

- **Issue-form intake → bot pull request.** A data issue-form submission (Add a
  work, Add a recording, Correct data, Correct several fields, Import a
  library) is routed by its `data:*` label to the `intake` workflow, which runs
  `cmd/metaissue` to compose the same canonical records a hand-authored pull
  request would carry, deduplicating against the catalog. On success it opens a
  `bot-intake` pull request on branch `intake/issue-<n>` crediting the submitter
  and naming the license layer; a submission that is a duplicate, needs a human,
  or is invalid is commented back on the issue instead (with a matching label).
  The bot only *drafts* the change - it runs the same untrusted-data-only,
  no-fork-execution security model as the rest of CI (`intake.yml` treats the
  issue body and attachments strictly as data).

  **Issues filed through the AudioSilo GitHub App** (by an AudioSilo Bridge, on
  behalf of the server admin who signed it in) arrive without labels, so
  `route.yml` routes them: an issue whose `performed_via_github_app.id` is the
  App's and whose title starts `[work] ` or `[corrections] ` is labelled with
  the workflow token and composed by `intake.yml`, called as a reusable
  workflow in the same run. The routing rules below are one reusable workflow,
  `route-reusable.yml`, shared with the community repository (which calls it at
  a pinned commit with its own two prefixes and cap), so there is one copy to
  change; the community repository picks a change up when it bumps that pin.
  Re-running a route run does nothing once the issue is decided (the conditions
  are in that workflow's header). The App id only says which tool filed the issue -
  anyone can sign in to a public App - so it is a routing hint, never trust: the
  body is validated like any form and the merge policy below is unchanged. The
  abuse bounds hold an issue (`intake:held` plus a comment) instead of routing
  it: an account younger than 30 days, more than 25 open App-filed issues from
  one account (50 on the community repository), or a user id listed in
  `.github/intake-denylist` - and so does a routing label the workflow could
  not apply. A maintainer releases a held issue by applying its
  routing label by hand; blocking the account or setting interaction limits are
  the stronger tools.

  The **correction-batch** form carries up to 25 corrections to one book. Only
  a work's `title` and a recording's `release_date` and `isbn` are applied by the
  bot; the rest of the batch is commented on the issue, which takes the
  `data:needs-human` label, and the pull request with the applied part
  references the issue without closing it.
  One change the bot cannot read refuses the whole issue.

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
  cannot resolve mechanically - the same record edited on both sides, one side
  deleting what the other edited, or one record left in two packs in differing
  copies (metafmt refuses to pick one) - is left alone with a comment asking for
  a maintainer. It only ever touches branches this workflow created,
  and it runs on `push` to `main`, so no fork code is executed. Humans use the
  same recipe (CONTRIBUTING.md, "When two pull requests touch the same pack").

- **AI verification (advisory).** The `ai-verify` workflow asks Claude to
  sanity-check a data pull request's records for judgement a machine check cannot
  make (factual consistency, plausible provenance, the correct license layer,
  no copied publisher prose, sane sidecar spoiler positions and length).

  **What it reads is an entry-level SUMMARY of the change, not its diff.** Data
  files are range-packed, so a single write re-renders its whole pack and may
  split it: a hundred-record pull request is over a hundred changed files and
  tens of thousands of changed lines, nearly all of them records nobody touched.
  The workflow therefore runs `cmd/metadiff`, which keys every changed pack's
  entries by family and slug across the whole change at once - an entry that
  merely moved between packs is counted and not listed - and renders one line per
  added or removed record, a field-level diff per modified one, and the slug
  tombstone rows, with `data/redirects.json`'s raw diff appended. The reviewer
  therefore sees the WHOLE change rather than whatever fitted in its input
  budget, and when a change is larger than even that summary allows, the summary
  says in line how many entries it left out. If the summary cannot be built at
  all, the workflow falls back to the raw diff and switches the reviewer's
  instructions to match it, so the reviewer is never told it is reading the whole
  change when it is not; if neither can be built, the check goes red rather than
  reporting a verdict on nothing. It
  posts a PASS/FLAG comment and applies an `ai-verified` or `ai-flagged` label.
  The VERDICT is **advisory only**: a `flag` is a prompt for a maintainer to
  look closer, not a veto, and the check is not branch protected, so neither
  verdict blocks a merge. The job's COLOUR answers a different question -
  whether a verdict happened at all - and a verification that could not run
  leaves it red rather than green; the rule and the reason live on
  `.github/scripts/ai-verify.sh` (the `EXIT STATUS` block). It runs on same-repo
  branches only (fork runs have no secret and a read-only token by design - the
  repo never adopts `pull_request_target`). A fork pull request is verified on
  its steward mirror (see "Pull requests from forks" below), or, when it cannot
  be mirrored, after a maintainer pushes the branch to the repository or runs
  the workflow by hand for it (a manual run starts from the default branch,
  with the secret).

  **A new series is read by who opened the pull request.** The
  series-completion bot and the LibriVox lane never found a series, so a new
  one in their pull requests is worth a closer look. A person founding the
  series their book belongs to (a fork and its steward mirror, an issue form, a
  steward pull request) is ordinary, a series of one work included, and the
  series is judged by its own record: a series name rather than a recording's
  or an edition's, authors that match its works, sane positions.

  **A second read with the steward's evidence.** When the steward (below) finds
  every item of a flag correct as it stands, it posts its resolver's answers
  beside retailer records its service fetched, bound to the exact content that
  was flagged, and runs the check again. That run reads the comment as a second
  input: the records are facts about the listings, the answers are claims, and
  the change is judged afresh. The workflow reads such a comment only from the
  account the `STEWARD_LOGIN` repository variable names (none while it is
  unset), only while that account is the repository's owner, a member or a
  collaborator, only from its newest such comment, only when that comment was
  never edited and ends with the steward's marker, and only for the content it
  answers, and the verdict comment says when it read one. The evidence clears
  nothing by itself: only the verdict does, and every advisory is still a
  finding.

**Merge policy is unchanged by these automations.** A `bot-intake` pull request
is treated exactly like one "opened by anyone else": it must pass CI, and it
still requires **one maintainer approval** before it merges. A green
`ai-verified` label does **not** by itself enable auto-merge. Auto-merge for
bot-drafted or AI-verified pull requests was a **deliberate future toggle**, and
it has since been turned on for **exactly one** automation - the
series-completion bot in the next section, which buys it with a bound a machine
enforces rather than with a track record. The maintainer approval every other
data pull request needs is given by the maintainer's steward (the section after
that one): a reviewer acting for the maintainer, not a widening of the toggle.
Widening the toggle itself (like the Trusted Contributor ladder) is a maintainer
decision made openly.

## Series-completion bot (audiosilo-meta-sync)

The one automation allowed to merge its own work.
[`KodeStar/audiosilo-meta-sync`](https://github.com/KodeStar/audiosilo-meta-sync)
is a private service that runs daily: it reads libex's new-releases and
coming-soon feeds across every marketplace region, keeps only the rows that
**complete series this catalogue already holds**, and opens one pull request per
cycle. Its one other way in from a retailer is the much narrower reported-ASIN
lane below; its LibriVox lane is a separate source with a section of its own.
This section is the whole of its licence for the first two; nothing else in the
merge policy moves.

**The bound.** Selection is the existing `metaimport libex-select` tranche
selector, which refuses any row whose series the catalogue does not already
track. So the bot can add a volume to a series we hold; it can never add a
**series**. A row for a volume the catalogue already holds is ATTACHED to that
work - as another recording, or as another ASIN on an existing recording - only
when its series, position, authors and title match the incumbent; every other
row at an occupied position is refused. It never adds a second work at an
occupied position and never changes the incumbent. It writes under `data/` and nowhere else -
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

**The reported-ASIN lane.** By the maintainer's decision of 2026-11-05 the bot
may also import what is named on metaserve's intake queue
(`/api/v1/intake/unknown-asins`, anonymous ASINs reported as missing from this
catalogue), and not only what the feeds carry. The lane is off unless the bot is
configured with the queue's address and token. What it adds, and nothing more:

- **A reported standalone**: a row that states no series may be imported as a new
  work in no series. `metaimport libex-select --reported` lets it past the
  completion test and nothing else: a language or marketplace we cannot map, an
  AI narrator and every other credit refusal still refuse it. If the importer
  resolves it to a work the catalogue holds, it lands as another recording of
  that work, or another ASIN on one. Otherwise the selection refuses it as
  `identity-duplicate` when a held work in a compatible language shares at least
  one author with it and the two titles reduce to the same normalized identity
  key, comparing the full titles and also each side's main title (before a `: `
  or ` - ` subtitle separator) against the other's full title. That catches a
  subtitle on either side ("Ford County" beside "Ford County: Stories"), an
  author list that is a subset of the held one ("Micro" credited to one of its
  two authors), a bare title naming several held volumes of a serial, and a
  title spelling its conjunction another way ("Daisy Jones and the Six" beside
  "Daisy Jones & The Six", or the German "und" beside "&"), which this guard
  alone reads alike: the normalized key every other path shares still keeps
  them apart (#2890). A row whose title states a volume is refused only beside
  a held work stating the same volume, in its title or as its position in a
  series. It is
  wider than the create path's own guard on purpose: a refused report is
  memoized and comes back later, a minted duplicate needs a repair. The price
  is a distinct book sharing a main title with a held book by the same author,
  which is refused too. What it still cannot see mints a work: a title whose key
  differs in any other way, which no advisory names, and two spellings of one
  new book in the same tranche, which
  the advisory census names when the two key alike, in the verdict comment
  alone, since a `bot-sync` pull request is never flagged for its advisories.
- **A reported volume of a held series**: a row that claims a series takes
  exactly the rules of "The bound" above. A row claiming a series the catalogue
  does not hold is refused, as it is from the feeds. **The lane never founds a
  series.**

Its limits are its own and tighter than the feeds': at most **25 records per pull
request**, new works and attached editions counted together, and at most **one
pull request per 48 hours**, spent when the pull request opens, so one that is
later closed or recycled still used its turn. It shares the bot's one open pull request, so it can never run beside a completion
tranche, and completion takes every cycle the lane is not due or has nothing to
add. It spends at most 150 listing lookups a cycle, and it runs no `--enrich` pass.

**One report is enough** (minimum count 1). The queue's `count` is only how often
an ASIN was reported: reports are anonymous and unverified, and the count says
nothing about how many servers, libraries or people stand behind it. It orders
the work. It is never evidence for a record, and nothing may describe it as such.

Why that is still bounded rather than an open door, given that anyone can write
to the queue:

- **A report is never a record.** Each ASIN has to come back from libex as a real
  retailer listing, read one at a time, and only what that listing states is
  imported. An ASIN with no listing writes nothing.
- **One slot, at a fixed rate.** However long the queue is, the limits above hold
  the lane to about 4,600 records a year. The worst case is a spammer nominating
  real retailer listings at that pace, which is true facts about real listings
  arriving slowly in reviewable pull requests. The lane reads each listing's
  content type before selection and keeps only a book (`SinglePartBook` or
  `MultiPartBook`, the bulk export's own rule), so a podcast episode, a periodical
  issue or a listing stating no type never reaches an import.
- **The same gate.** A lane pull request is a `bot-sync` pull request: `data/` only,
  the required checks, and an `ai-verified` verdict on its exact head before it
  merges, with the ladder below when the verdict does not come.
- **It can be taken back.** Its records carry the typed `libex-import` provenance,
  so they retract with that source and rank as bulk-mirror records. A lane run
  imports with `metaimport libex --intake`, which stamps each record it writes with
  the ref `intake:<ASIN>` where the feeds stamp the bare ASIN, so retracting the
  lane alone is a query over `sources[].ref` (meta#2890). Records a lane run wrote
  before the bot passed `--intake` carry the bare ASIN like the feeds'; for those,
  retraction still goes through the lane's pull requests, each labelled
  `sync:intake` and listing the ASINs it was given. A lane record that a later
  series-completion run changes (an enrichment, a post-release refresh) gains the
  feed's bare-ASIN stamp beside `intake:<ASIN>`, and keeps both on purpose: the
  bare one is the feed's own record of what it stated (meta-sync#91). The query
  still finds such a record by its lane stamp; the second stamp says a feed has
  vouched for part of it since, which is for whoever retracts it to weigh.

A flag on a lane pull request goes through the ladder below like any other, with
three differences, so a poisoned queue costs the lane its turns and cannot stall
series completion:

- Its exclusion does not count toward the stop on exclusions in a row. It pauses
  the lane for 7 days instead.
- A lane pull request closed for want of a verdict does not count toward the
  throttle on such closures, and it does not pause the lane.
- With no resolver configured, a lane pull request is excluded rather than parked,
  so public input cannot fill the parked slots. A token without the Actions
  permission still parks it, as it would any pull request.

The cycle after a lane pull request always belongs to completion.

**The merge gate.** The bot merges its own pull request only when the required
checks (`check`, `compose`) are green **and** the `ai-verify` workflow has
applied `ai-verified`. Here - and only here - that verdict is not advisory: it
is the review step standing in for the maintainer approval a batch import
otherwise needs, and nothing below relaxes it: a pull request merges only on
that verdict for its exact head. What the bot does when the verdict does not
come is an escalation ladder that ends in a decision it makes itself, never in
a merge. A failed required check is re-run once (flaky CI), then treated like
an `ai-flagged` verdict with the failed job's log as the evidence. A flag is
handed to a resolver (a Claude or Codex CLI on a subscription login) for **at
most three** fixing amendments, then one final amendment that may only REMOVE
the flagged records. The resolver edits `data/` only and only the records this
pull request added, and it never touches a record that was here before. If the
flag survives that, the bot **excludes** the pull request: it closes it
unmerged, labels it `sync:auto-excluded` (non-blocking, for a maintainer to
review at leisure), refuses its rows for 45 days and re-queues the rest of
their series. An exclusion caused by a failing check rather than a flag
refuses nothing - the bot validates every tree before pushing, so such a
failure is almost always systemic - and from the second exclusion in a row the
bot stops starting the next cycle at once and reports itself degraded, so a
systematic fault cannot churn through the queue. An `ai-verify` run that keeps failing, or no verdict at all for
six hours, closes the pull request and re-runs the cycle on fresh main, with
the pace throttled after three such closures in a row. Only a configuration
fault - no resolver configured, or a token without the Actions permission -
still parks a pull request as `sync:needs-human`, and the bot stops opening new
pull requests while **three** parked ones are open, so a systematic fault
produces a short queue rather than a backlog.

**Accountability.** Every merge is a pull request, so the record is the ordinary
reviewable one: labels `data`, `bot-intake` and `bot-sync`, a per-series table
of what the tranche adds (a lane pull request adds `sync:intake` and states the
lane's rationale and its ASINs instead), the required checks and the verifier's
verdict on it, and a comment on the pull request describing whatever the resolver
changed. A
bad cycle reverts like any other change (revert-first, below), and because the
provenance is typed, an entire source retracts in one pass.

**Kill switch.** Revoke the bot's personal access token, or stop its container.
It has no other way into this repository, and nothing here depends on it
running. The reported-ASIN lane alone stops by unsetting its address or token.

**Widening is a maintainer decision made openly**, exactly like the ladder it
came from. A new source, a new kind of record, authority to create a series
(from the lane or anywhere else), a larger cap or a faster lane, or the same
automation pointed at
[audiosilo-meta-community](https://github.com/KodeStar/audiosilo-meta-community)
(whose review bar is prose a human has to judge, which is why the repositories
split) is a change to this document first and a change to the bot second.

## The LibriVox lane

The series-completion bot's second source (LIBRIVOX-PLAN.md phase L4). The same
private service reads LibriVox's own catalogue once a day and imports the
projects LibriVox has catalogued since its last run, so the LibriVox mirror stays
current without a person cutting tranches. It works under [LICENSING.md's
public-domain exception](LICENSING.md#the-public-domain-exception-librivox),
which is the whole of its licence for the data, and this section is the whole of
its licence to merge its own work. The backfill that seeded the mirror is not
this lane: it landed as human-reviewed tranches (`metaimport librivox import
--ids <range>`, about 2,000 projects per pull request) under the ordinary merge
policy, and any change a person makes to LibriVox records still goes that way.

**The invariant.** The bot checks it itself, over every pack the run changed
(both sides of each, a whole pack family read together, deleted packs
included), after the tree is formatted and validated and before anything is
committed. A run that breaks it is discarded whole: nothing is pushed, no pull
request opens, and its projects are held as failed (the pending list below says
how they come back).

- It adds only `librivox`-sourced works, people and recordings. A new record
  whose `sources` name anything but `librivox` is refused, and so is a new
  recording whose `librivox_id` is not one of the projects the run was given.
- It never adds, edits or removes a series, and never touches
  `data/redirects.json`. Nothing outside the works and people families changes,
  and no entry is ever removed.
- It never edits a record another source owns. On a work the catalogue already
  holds it may only attach a new LibriVox recording and add a `librivox` source
  entry. An existing recording is never changed. On a person the catalogue
  already holds it may only **stamp**: write the person's LibriVox author or
  reader id where the record holds none, add a `librivox` source entry, and
  write the birth and death years LibriVox states only when the record states
  NEITHER year. A year another source or a hand edit wrote is never
  overwritten, and one half is never added beside the other's existing value,
  which could pair a death with a birth it precedes.
- It never hands the importer a project the catalogue already holds: for a held
  project the importer re-reads the recording, which is an edit, and editing is
  not this lane's to do.

**Cadence and size.** At most one run per UTC day. The lane shares the bot's one
open pull request, so it never runs beside a completion tranche; the cycle after
a lane pull request belongs to completion, and a merge lets that cycle start at
once. A pull request carries at most **200 projects**, and the rest wait for the
next run. The lane reads LibriVox's `since` from its own cursor, the midnight UTC
of the day its last merged window was read, which in practice is one request a
run; with no cursor yet (a first run) it reads from yesterday. The cursor moves
only when a lane pull request that read a window merges, or when a run finds
every project in its window already held or already on the pending list below.
Opening, closing, recycling or excluding a pull request, a failed import, a
retry of failed projects and a dry run move nothing, so no window is ever
skipped. Requests go out at least 5
seconds apart, honour `Retry-After`, and name meta.audiosilo.app and
kode@audiosilo.app in their User-Agent.

**The census.** LibriVox's `since` filters on a project's catalogue date, so the
daily window does reach a project that completes after it was first listed. The
census is the safety net behind it, for whatever a window missed. On its first
run, and every 30 days after, the lane lists the whole catalogue in about 45
light requests: only each project's id and runtime, 500 to a page, 5 seconds
apart. (The archive.org item is left out because it needs LibriVox's extended
answer, about twelve times slower.) A project LibriVox lists that the catalogue
lacks and the pending list does not hold is fetched by id and imported, or held
as in progress, which the census judges by a missing runtime alone; an
in-progress project that now has a runtime becomes due. At most 50 projects are
fetched by id in a run.

**The pending list.** A project the lane could not land is kept on a durable
list in the bot's state, never forgotten, and tried again on its own schedule:
in progress (when the census sees it complete), a worklist row (every 7 days),
refused by the importer or not landed (every 30 days; a project LibriVox no
longer lists is held as not landed), past the 200 cap or put back by the ladder
below (the next run), failed, which is a run discarded before any pull request
opened because its import, its validation or the invariant failed (after 7
days), or excluded by the ladder below (once more after 30 days, then held for a
person). A project whose by-id read fails is held as unread and tried again
after 7 days. A failed batch is never retried whole: when failed projects are due,
that day's run imports only the first half of them (or the one alone, when only
one is due) and leaves the day's window for the next run, and its pull request's
merge leaves the cursor where it is. A project leaves the list when the
catalogue holds it, whoever added it.

**The worklist issue.** The rows the importer will not decide alone go to one
issue on this repository that the bot keeps current. Above all that is
`same-name-author`: a LibriVox author whose name matches a catalogued person the
importer will not stamp without a judgement. The issue is labelled
`sync:librivox-worklist` and never `data`, so it stays outside the steward's
triage of data issues. `same-name-author` rows come first, each with its
candidates and how to resolve it, then the other reasons, then the excluded
projects, with the in-progress count folded away at the end. It lists at most
200 rows and says how many more there are, is rewritten only when its content
changes, and is closed when nothing is left on it. A person resolves a row with
an ordinary pull request (a reviewed `metaimport librivox import --ids`, or a
data fix); the lane then finds the project held and drops it.

**The merge gate and the ladder.** A lane pull request sits on a
`sync/librivox-<day>` branch, is labelled `data`, `bot-intake`, `bot-sync` and
`sync:librivox`, and lists every project id it carries. It merges exactly as a
completion pull request does: the required checks green and `ai-verified` on its
exact head. ai-verify reads such a branch as the LibriVox lane, whose prompt says
what a LibriVox record is (its `librivox_id` and archive.org item are the
evidence, no ASIN is expected, readers are volunteers under the names they
chose, old first-publication years and every language are normal, a stamp on a
held person is expected, a work in no series is expected) and makes any series
change a finding. Its advisories are shown in the verdict comment, unflagged, as
on a completion pull request.
**There is no resolver.** Only an ai-verify flag excludes: it closes the pull
request unmerged, the projects behind the works the finding names are excluded
(all of its projects when it names none), the rest go back on the list for the
next run, and the lane rests a day. A required check that fails again after its
one re-run closes the pull request too, but excludes nothing. The first time,
every project goes back on the list for the next run, with no rest day, since a
break on main is the likelier cause. If the next lane pull request is closed the
same way (a second in a row), or a check closes a retry, its projects are held
as failed and retried in halves, a week apart, until the project whose data
breaks the check is alone in a run of its own. Any lane merge resets the count.
A pull request closed for
want of a verdict moves nothing, and the next run reads the same window again.

**Accountability.** Every record carries the typed `librivox` provenance with
its project id, so it ranks as a bulk-mirror record and the whole source retracts
in one pass. Each pull request names its window and its projects, and the
worklist issue shows what is held and why.

**Kill switch.** `SYNC_LIBRIVOX=off` in the bot's environment stops the lane
alone. The bot's own kill switch above stops it with everything else.

**Edits are not in `since`.** A correction LibriVox makes to a project already
here is not picked up. A re-read of the last 90 days of projects is a later
follow-up: an edit path, which needs its own invariant written here first. Until
then a correction to a LibriVox record goes through an issue or a pull request
under the ordinary merge policy.

The series-completion bot's bound above (it never adds a series and never
mirrors a source) is unchanged by this lane, which is a separate source under
its own invariant. Widening either is a change to this document first.

## Store listings (maintainer imports)

A maintainer may import a store's public listing for books they name (Audiobook
Guild and GraphicAudio today), under [LICENSING.md's store listings
section](LICENSING.md#store-listings-a-maintainer-named-import), which is the
whole of its licence:

- the input is a reviewed file of the named listings (`metaimport store`), and
  the result is an ordinary `data/` pull request under the merge policy above;
  it never runs on a schedule;
- the series-completion bot and the LibriVox lane do not use it, and neither may
  start to without a change to this section first;
- it brings facts only (no blurbs, ratings, prices or store genres) and goes
  through the importer's identity, duplicate and person rules, never a
  hand-written pack edit.

How contributors and the bot handle such stores in the long run is a later
decision, recorded here when it is made.

## The steward (the maintainer's reviewer)

The maintainer's review of data pull requests and data issues is carried out by
a **steward**: a second service inside the same
[`KodeStar/audiosilo-meta-sync`](https://github.com/KodeStar/audiosilo-meta-sync)
container, acting for the maintainer with the maintainer's own token. It is how
a contributor's submission reaches `main` without waiting for a person, and it
is bounded as follows.

**What it merges.** A pull request whose whole change is under `data/`, from a
branch of this repository (a fork's arrives on a mirror branch, see "Pull
requests from forks" below), into `main`, no larger than its review bound
(about a hundred catalogue entries - the size a verifier can read whole). A
larger change is carried to `main` in parts within the bound; the bound is
lifted for one pull request only where nothing smaller exists (a group of
entries that must land together merges whole, and a part may carry the
steward's fix on top of its own entries). It merges only on exactly the gate
the series-completion bot uses: every required check concluded well on the
exact head, `ai-verify` has applied `ai-verified` for that head, the branch is
not behind `main`, and the merge is pinned to the head it judged. Apart from
the Dependabot updates below, it never merges anything touching `schema/`,
`cmd/`, `internal/`, `.github/` or any other path - a code pull request gets a
written review from it as a comment, and a maintainer decides. It never touches
the series-completion bot's own pull request.

**Dependabot updates.** By the maintainer's decision of 2026-10-04 the steward
also merges Dependabot's pull requests in each repository its configuration
names (`SYNC_STEWARD_DEPENDABOT_REPOS`, none by default), on a gate of their
own. One is eligible only when `dependabot[bot]` opened it from the repository
itself into its default branch, it is not a draft, every commit is authored by
`dependabot[bot]` and committed and signed by GitHub, and every file is a
dependency manifest, lockfile, Dockerfile, compose file, workflow or action
definition (`action.yml`); it then merges only when its head has sat still, at
least one check ran on that exact head and every check and status on it passed
(a head no check tested is never merged), GitHub reports it `clean`, a major
update has had a safe verdict from the contained agent's read-only review, and
it is up to date with its base, at most one per repository per tick, pinned to
the head it judged. Here an ineligible one is reviewed as code like any other.
The steward never pushes to a Dependabot branch: one that is behind or
conflicting gets `@dependabot rebase`, a conflict that outlives it `@dependabot
recreate`, and one Dependabot does not move is listed for the maintainer. One
exception, by the maintainer's decision of 2026-11-24, in this repository and
the community layer only: a pull request that is only behind (GitHub still
reporting it `clean`) merges as it is when it changes nothing under `data/` and
everything `main` changed since the merge base is under `data/`, as GitHub's
comparison lists it in full (at most 250 commits and fewer than 300 files;
anything larger takes the rebase route), so the tree outside `data/` that lands
is the one its CI passed on.

**Pull requests from forks.** By the maintainer's decision of 2026-11-24 the
steward also carries a fork's data pull request to `main`, through a mirror. A
fork's own pull request cannot pass the gate, since its checks and `ai-verify`
run without this repository's secrets. Before any push the service (never the
agent) checks the fork's exact head against its merge base with `main`. It is
eligible only when:

- every commit and the head itself change nothing but plain `data/**/*.json`
  files (regular files, mode 100644, or removals; no dotfile, symbolic link or
  submodule);
- the tree outside `data/` is the merge base's exactly, so `.github/`, the code
  and the scripts CI runs are the maintainer's, never the contributor's;
- the change is within the read bounds (3000 files, 16 MiB of data diff, 1 MiB
  of commit text);
- a secret scan over every commit's patch and text finds nothing.

An eligible head is pushed unchanged to
`steward/fork-<n>` in this repository, and a pull request "(mirrored from
#<n>)" is opened that credits the contributor. From then on the mirror goes
through the same gate, review bound and fixes as any other steward pull
request, with two differences: the steward merges it whole only squashed, and
it is withdrawn (closed unmerged, its branch deleted) when its fork is closed
or moves to a head that may not be mirrored. The squash carries the steward's
own commit message, which credits the contributor in plain prose ("Contributed
by @login in #n.") and, for a person rather than a bot account, ends in GitHub's
co-author trailer at their noreply address, so GitHub attributes the merged
commit to them (the maintainer's decision of 2026-11-25). A closing keyword in the fork's title or commits closes no
issue through the steward's merge: the title is neutralized ("Fixes #12" reads
"ref #12") and the squash carries none of the fork's commit messages. After
that:

- once the mirror has merged whole and every entry the fork changed is verified
  on `main`, the fork's pull request is closed as superseded with a link to the
  mirror and its merge commit (unless GitHub already marked it merged);
- a new push to the fork is checked again: an eligible head replaces the
  mirror's content (a steward fix on the old head is dropped and made again if
  still needed); a head that is not eligible gets no push and one comment
  saying why, the fork stays open for a person, and a mirror of an earlier head
  is withdrawn, since it no longer carries the contributor's change;
- a fork closed unmerged, by anyone, withdraws its mirror the same way, and
  reopening it mirrors it again;
- a mirror that leaves unmerged any other way (a maintainer closed it, or it
  was carried to `main` in parts and closed once every part landed) is not
  opened again for the head it carried, and the fork stays open for a person
  until a new push is judged afresh;
- with `SYNC_STEWARD_FORKS` off (see "Kill switch" below) a fork pull request
  not yet mirrored waits for a person, and a mirror already open carries on
  with the head it holds: a later push to the fork is not mirrored until the
  switch is on again.

**What it fixes.** A data pull request that is flagged or red is not left
waiting, and being flagged or red never gets it closed unmerged (a fork's
mirror is withdrawn only with its fork, as above). A failed check is re-run
once; after that the steward amends the data (on `intake/issue-*` and its own
`steward/*` branches, or on a superseding `steward/pr-*` pull request for
anyone else's branch) with an agent, and the amended head goes back through
`ai-verify` and the same gate. Attempts back off (hours, then a day) and never
end in a closure; one that keeps failing is listed on the steward's report for
the maintainer. A superseded pull request is closed only once its content is
verified to be on `main`.

**When a flag is wrong.** By the maintainer's decision of 2026-11-26, a flagged
core pull request whose every flagged item the resolver finds correct as it
stands is not amended and not parked. The resolver answers each item, citing
retailer records the service (never the agent) fetched from libex, and the
steward posts those answers and records in one comment bound to the flagged
content, then runs `ai-verify` again, which reads them as a second input (see
"AI verification" above). A pass on that second read merges through the same
gate as any other; a second flag parks the pull request for a person, and the
same content is never confirmed twice. The steward never applies `ai-verified`
itself, so a flag clears only on `ai-verify`'s own verdict, never on the
resolver's word. The community layer's flags are still amended by deletion, as
below.

**Issues.** A data issue the intake bot could not finish (`data:needs-human`,
invalid, or no pull request) is researched by the steward, which either opens a
`steward/issue-*` pull request (capped at a few dozen entries; it then goes
through the gate above like any other), closes the issue with evidence that
the data is already present, declines it with a reason, or asks the reporter
one specific question. Issues without a `data` label are never touched.

**The agent is contained.** Issue and pull request text is untrusted, so the
agent that reads it runs as a separate unprivileged user without the
maintainer's token, cannot read the service's state or credentials, and cannot
push anything itself: the service verifies its change (data-only,
`metafmt --write`, `metacheck` with the repository's profile, a secret scan)
and pushes it.

**The community layer.** By the maintainer's decision of 2026-09-28 the
steward also looks after
[audiosilo-meta-community](https://github.com/KodeStar/audiosilo-meta-community),
on the same terms and with that repository's own gate. Its prose is judged by
its own `ai-verify`, a reviewer written to
[AUTHORING.md](https://github.com/KodeStar/audiosilo-meta-community/blob/main/AUTHORING.md):
the spoiler model, own words, the neutral reference-guide voice, the
description contract and provenance. A community data pull request is merged
only when `keys`, `structure` and `verify` have concluded well on its exact
head and it carries `ai-verified`, it is not behind `main`, and it is within the
same review bound. A flagged one is amended by the contained agent BY DELETION
ONLY (the maintainer's decision of 2026-09-28): it may cut words or sentences
from a contributor's text, or remove a character card, a recap entry or a
member, and never writes, rephrases or moves prose - the agent has not read the
book, so nothing it composed could be grounded in it. A spoiler is cut rather
than moved, and a mechanical check refuses any amendment whose text is not the
contributor's own with words removed. The amended head goes back through the
gate. The steward **writes no prose from an issue** either: a characters or
recaps submission reaches a pull request through the intake bot, from a
contributor's own text, and the steward only closes an issue once the pull
request that answers it has merged.
Composing recaps or character cards itself would be writing CC BY-SA prose from
recollection, which the grounding rule there does not accept.
When a data release retires a work slug the layer is keyed by, the steward may
also open a mechanical re-key pull request (the maintainer's decision of
2026-11-07). It renames each such entry to the surviving work (the entry key
and its members' `work` fields only, never prose), is built by the service
rather than the agent, and merges through the same gate. A survivor that
already has an entry is a collision, left for a person since merging the two is
prose. No fix of any kind touches a re-key pull request: whatever stops it (a
flag, a failing check or status, a conflict) it too waits for a person (the
service still rebuilds it from `main` when `main` moves).

**Kill switch.** The steward is off unless `SYNC_STEWARD=on`, and each
repository it looks after is named in its configuration (the community layer
only when listed there); turning it off, revoking the token, or stopping the
container stops it. Fork pull requests are mirrored only with
`SYNC_STEWARD_FORKS=on` (off by default). **Widening** it further - a
larger bound, merging code beyond those Dependabot updates, loosening the
checks in "Pull requests from forks",
writing community prose, or pointing its catalogue work at any further
repository - is a change to this document first. Which repositories' Dependabot
updates it handles is the maintainer's configuration
(`SYNC_STEWARD_DEPENDABOT_REPOS`), not a widening.

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
