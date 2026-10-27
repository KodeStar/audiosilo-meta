# CLAUDE.md - AudioSilo Meta

Guidance for working in this repository. Keep this file updated as the codebase
evolves. This is the sixth repo in the AudioSilo workspace (`~/dev/audiosilo`) -
read the workspace [CLAUDE.md](../CLAUDE.md) and
[META-FEASIBILITY.md](../META-FEASIBILITY.md) (the research + design basis for
this project) before working here.

## What this is

An **open, community-editable audiobook metadata database** - the data behind
meta.audiosilo.app (deployment pending). The GitHub repository IS the database:
JSON pack files holding many records each ([PACK-SPEC.md](PACK-SPEC.md)),
contributed via pull requests and issue forms, validated by Go tooling in CI,
and compiled into a SQLite artifact published as a GitHub Release. The Go API server (`metaserve`) serves that artifact plus the
static site in `site/` (Astro, the audiosilo.app design system); the AudioSilo player
integration (Phase 1.5) is the priority consumer and shipped **before** the
Audiobookshelf-provider facade (`GET /abs/search`), which has since landed on top
of it (the player was always the defining AudioSilo feature; the ABS facade is a
bonus for a direct competitor's users).

Module path: `github.com/kodestar/audiosilo-meta`. Code is AGPL-3.0; the data
is CC0-1.0 (factual core) with a CC BY-SA 4.0 community layer (the
characters/recaps sidecars) - the full policy
in [LICENSING.md](LICENSING.md) is load-bearing, read it before touching data
handling.

**Since 2026-08-21 the CC BY-SA layer is a SECOND REPOSITORY**,
[`KodeStar/audiosilo-meta-community`](https://github.com/KodeStar/audiosilo-meta-community),
holding the `works-community` family alone (plus its own issue forms, intake and
AUTHORING/EXTRACTION guides). This repository is the CC0 core - works, people,
series and `redirects.json` - and holds no share-alike content; its tree profile
(PACK-SPEC.md) is `core`, which CI passes explicitly. All the TOOLING stays here
and the community repo consumes it, so there is one implementation of pack math,
canonical JSON and validation. The release stream is unchanged: `release.yml`
checks out both repositories and `metabuild -data data --community <dir>`
composes ONE artifact from the pair, so metaserve, the poller, the patch chain
and the webhook see exactly what they saw before. A community-side merge cannot
push here, so that repo's `notify-core.yml` sends a `community-data`
`repository_dispatch` and this repo's `release.yml` accepts it as a third
trigger. The migration plan is the maintainer's, in the untracked
`.claude/community/PLAN.md`.

## Model routing (every session follows this)

Work runs through the workspace `/ship` skill (`~/dev/audiosilo/.claude/skills/ship/`):
the main session plans, decides and checks; fresh Opus agents (`ship-implementer`,
`ship-reviewer`, `ship-verifier`) do the reading, writing, reviewing and live
verification, so the large main context is never what reviews or edits code.

## Build / test / gate

```sh
cd ~/dev/audiosilo/audiosilo-meta
go build ./... && go vet ./... && go test -race ./... && golangci-lint run
# ~45s wall for the -race suite; no -timeout flag needed. Every REAL-DATA test
# skips under -race (raceEnabled, race_{on,off}_test.go in pkg/check,
# pkg/canonical, internal/importer and internal/repair) - the fixture suites
# cover the concurrent code and the real tree is data coverage, still read by
# the non-race run and by metacheck/metafmt below. Unskipped, pkg/canonical's
# tree walk alone was ~400s and internal/importer's slug walk ~220s (403s wall).
# So check.yml never runs them: .github/workflows/real-data.yml runs the WHOLE
# suite without -race daily, on dispatch and on Go-touching pushes to main (see
# its header), and a red run opens or comments on the one open `ci-real-data`
# issue, which the next green run closes (.github/scripts/real-data-report.sh).
# Locally: `go test -count=1 ./...`.
go run ./cmd/metacheck --profile core       # validate the data tree (~10s over 133k works)
go run ./cmd/metafmt --check --profile core # canonical formatting (--write to fix)
go run ./cmd/metabuild -o meta.sqlite   # build the CORE-ONLY artifact (no sidecars)
go run ./cmd/metabuild -data data --community <community-repo>/data -o meta.sqlite  # ... the REAL artifact, from both repos
go run ./cmd/metaserve --db meta.sqlite --addr :8080   # serve the read-only API
go run ./cmd/metadiff --base <sha> --head <sha>    # entry-level summary of a data change (what ai-verify reads)
go run ./cmd/metaaudit -data data -o audit-report  # read-only data-quality audit (~30s)
go run ./cmd/metarepair -data data --community ../audiosilo-meta-community/data \
  --op merge-works --limit 50                     # DRY RUN of the repair (~30s)
```

`--profile core` is what this repository's data root now IS (PACK-SPEC.md's tree
profiles): works, people, series and `redirects.json`, with the CC BY-SA layer in
the community repo. THE DEFAULT DIFFERS BY TOOL, on purpose, and the axis is the
COST OF BEING WRONG:

- **metacheck / metafmt / metaissue default to `all`.** "A data root" with nothing
  said must keep meaning the whole database - that is what the fixtures and most
  tests are - and CI passes `--profile core` explicitly at every
  invocation site: check.yml, the intake bot's compose and normalize steps, the
  rebase sweep, and release.yml's core-side validation. A wrong reading there is a red check, or (for
  metaissue) a bot pull request a maintainer closes.
- **metaaudit / metarepair default to `core`** (approved deviation, 2026-08-21).
  They are operator tools a human points at THIS tree by typing a command, and
  metarepair DELETES RECORDS - so the dangerous case must not be reachable through
  the DEFAULT, which is what decides the asymmetry. Under
  `all` over a core tree it would see an empty works-community family and merge as
  though no book in the wave carried a sidecar - so the safe reading is the
  default and `--profile all` is available for a whole-database tree, of which
  there are now none. The two must always agree with each other: metarepair
  re-runs metaaudit's detectors before applying anything.

The teeth of the profile are the file ACCOUNTING, which turns a reappearing
`data/works-community/` here into a red check instead of a silently re-adopted
family.

**`metarepair --community <community-checkout>/data` is REQUIRED for a merge
wave**, and this is the sharpest edge the split left. The sidecar-collision
refusal - both halves of a duplicate carrying the same `characters` or `recaps`
member, which is a human decision - is a question about data this repository no
longer holds, so a core run must be handed the community root to answer it.
Without the flag every merge-works proposal is refused as
`community-data-required` rather than merged blind. The flag opens that root
READ-ONLY and is never written: the members ride the slug tombstone to the
surviving work (`check.LoadComposed`'s compose-time re-key, which warns on every
ride) until the community repo's re-key sweep lands them durably. Measured over
the real pair, the first 30 non-advisory merge proposals are **5 applied and 25
sidecar-member-collision refusals** - identical to the same wave over the
pre-cutover whole tree, and 25 CC BY-SA sidecars a blind run would have destroyed.

`metaaudit` is NOT part of the gate - it reports data defects (duplicate works,
decorated titles, unmodeled series memberships), which are a review queue rather
than a pass/fail. It never writes to `data/`.

`metarepair` is the pass that ACTS on that queue, and it is not part of the gate
either: it is a dry run by default and a `--write` is a data change with its own
pull request. It re-runs the audit in process over the tree it is about to modify
and applies only what that fresh run still proposes as non-advisory, so a stale
report cannot resurrect a decision the data no longer supports - see
`internal/repair`.

**The defect classes it found cannot be re-created**, which is the PREVENTION half
of that campaign and is in the gate (three layers over one rule -
`titlerule.IdentityTitleKey`, the normalized work identity):

- the intake bot refuses a submission whose work the catalogue already holds under
  a differently-spelled title, and strips a retailer-decorated title at compose
  time (`internal/issueform/dupidentity.go`);
- the bulk importer's CREATE path skips such a row rather than minting a sibling
  work - counted, warned and written to the `--conflicts` worklist
  (`internal/importer/dupidentity.go`); nothing merges automatically, because a
  refused row is recoverable and a wrong merge is not;
- `metacheck` counts the collisions still in the tree as an ADVISORY class
  (`normalized-duplicate-works`, 2,881 groups when it landed, 3,160 once the key's
  series strip was boundary-anchored), so a repair wave's
  progress is a number in the CI log and a regrowth is visible the wave after it
  happens. It never fails the check.

**The census is a READING LIST, so it honours the reading and reports what a pull
request ADDS.** It is deliberately broad (`checkIdentityEqualWorks`,
`differentVolumes`): most of its groups are not one book, and a census that keeps
naming pairs somebody has already ruled on - ~1,900 advisory lines scrolling past in
the `check` log - is one nobody reads, so a pull request that ADDED a duplicate was
never noticed (ai-verify read only the metadiff summary). Two halves:

- **Reviewed rejections suppress.** A `reject` of a `merge-works` / `merge-series` /
  `merge-people` proposal in `internal/audit/reviewed.json` (target + others, ids
  resolved through `data/redirects.json`) records every PAIR of its ids as distinct,
  and metacheck neither prints nor counts that pair under identity-equal-works,
  normalized-duplicate-works (a group drops the pair, a member left pairing with
  nobody drops out), duplicate-series or honorific-person-pair. The census line ends "(N reviewed and
  suppressed)", so the suppression is visible rather than silent. pkg/check cannot
  import the audit (the audit imports check), so the decisions arrive as data:
  `audit.ReviewedDistinct()` -> `check.LoadOptions.Distinct` -> `LoadProfileWith`,
  supplied by cmd/metacheck alone. So a triage records "not one book" as such a
  rejection - and rejects only the distinct subset of a group holding a real
  duplicate, which it asserts instead. `merge-people` (`audit.OpMergePeople`) exists
  only to be rejected - no detector proposes it and no repair applies it, so an
  accept or an assert of it does not parse - and is how "two people, not one" is
  recorded for an honorific pair (Dr. Alex George / Alex George). A merge
  rejection matching no fresh proposal is reported `census-only` in SUMMARY.md, not
  STALE, because the census honours it whatever the detectors propose and pruning
  it would bring the pair back - unless fewer than two of its records are still
  live (merged onto one survivor, or deleted), which withholds nothing and is STALE. Follow-up, not built: internal/importer's honorific
  fold could consult the same list before folding a "Dr. X" credit onto a bare
  record of another person. metabuild's census and metaaudit's LOADER carry-through load
  without the decisions (neither is the census anyone reads).
- **New advisories reach the automation.** On a pull request touching data/,
  `.github/scripts/advisory-delta.sh` runs the same metacheck binary over the base and
  the head (`--advisory-keys`: `check.AdvisoryKey`, class + family + entry +
  message, NO pack file, since a split renames packs under untouched records) and
  lists the head advisories no base advisory of the same class COVERS (names every
  record they name: the entry plus every quoted slug) - so a work joining a duplicate group is listed, a group a
  repair shrank or a duplicate series gaining a volume is not, and oversized-entry (a
  byte count) never is. check.yml prints it in the job SUMMARY; ai-verify.yml hands
  it to the model (`ADVISORIES_FILE`), which reports each one, and the script turns a
  pass beside a non-empty list into a FLAG itself (a deterministic signal must not
  depend on the model obeying), the forced finding carrying the list. A SYNC-BOT
  pull request is the exception: its flag feeds audiosilo-meta-sync's automated
  fix/drop/exclude ladder, whose resolver can neither fold a series nor record a
  reviewed rejection, so a legitimate volume making an existing duplicate visible
  would be dropped - there the list is shown in the verdict comment only, the
  prompt and verdict untouched. Neither is a failing check, on
  purpose: the bot auto-merges on green CI and a new red gate would stall it.
  check.yml diffs the base tip against the merge commit and ai-verify the merge
  base against the head, so the two lists can differ on a stale branch; both run
  the HEAD's metacheck (its reviewed.json, its pkg/check), so the list is
  trustworthy for a data-only pull request and one that also edits those can hide
  its own advisories (a code change, reviewed by a maintainer anyway). Cost: a second whole-tree metacheck in `check` (~1-2 min on a data
  PR) and two in ai-verify (beside a model call that dominates).

The same prevention-plus-census shape covers RECORDING-VARIANT SERIES - a retailer
listing each production of a serial as a series ("Harry Potter (Narrated by Stephen
Fry)", "Harry Potter (Full-Cast Editions)", "Discworld: Tiffany Aching (abridged)",
"Harry Potter - Gesprochen von Rufus Beck"), which stamped two or three series onto
every book a server matched. One rule, `titlerule.StripSeriesRecordingQualifiers`:
the importer cleans every claim through it (`CleanSeriesName`) and indexes a
catalogued variant under its base, an abridgement the cleaned qualifier stated
(`titlerule.SeriesAbridgement`) lands on the recording when the source stated none, the intake form reads its series name through it
(`formSeriesName`), the correct-data form refuses renaming a series to a qualified
name as `invalid` naming the base (`seriesNameFreeOfRecordingQualifier`), and `metacheck` counts two ADVISORY classes -
`series-recording-qualifier` (189 on 2026-10-11) and `duplicate-series` (197) - that
never fail the check (pkg/check's `seriesadvisories.go`).
A claim's RAW spelling (`seriesRef.raw`) is still walked, AFTER everything the
cleaned name reaches (`tierRaw`, `rawCandidates` in seriesresolve.go - NEVER an
anchor, only a cluster-step candidate after the row-language reach, so a plain claim's
outcome cannot turn on another row having spelled the variant; libex-select's
pre-filter and the form's `Resolve` ask it too), because the qualifier index cannot key a variant whose derived language
TIES and the cleaned chain may be empty: without it a claim "Foo (abridged)" founded
`foo` beside the held `foo-abridged`. A qualified claim's POSITION may be the
variant listing's own number for an IN-BETWEEN volume, so it does not contradict a
work the series LISTS at another fraction of the same whole volume
(`seriesClaim.compatible`'s `renumbers`): the dramatized Stormlight numbers Edgedancer
2.8 where the series says 2.5, and the row lands as a recording of `edgedancer`, the
series keeping its own entry and gaining no second membership (refusing the merge
there minted a duplicate work). It is no wider than that: a qualified claim at
another WHOLE volume is another volume (a narrator credit or an abridgement numbers a
serial as the plain listing does, and a serial sharing one short title would
otherwise file volume 3 as a recording of volume 2), and a work that only CLAIMED a
slot is not exempt.

**Before a change is done, all of the above must pass.** CI
(`.github/workflows/check.yml`) gates THE WHOLE of it on every PR and push to
main, in five jobs: `changes` (which of the others the diff can move),
`check` (build, vet, `go test -race`, metacheck, metafmt, and on a data pull request
the advisories it introduces in the job summary - never a failure), `lint`
(`golangci-lint run`), `vuln` (`govulncheck ./...`) and `site`. The race
detector, the linter and the vulnerability scan are recent arrivals: all three
were documented here and run by hand, so the green baseline this file promises
rested on somebody remembering. The linter SET is pinned too, by the committed
`.golangci.yml`: `linters: default: none` plus exactly the five v2.12 ships as its
"standard" set (errcheck, govet, ineffassign, staticcheck, unused) and
staticcheck's default check list spelled out, so a new golangci-lint release
cannot change which linters run - without the file the set was whatever the
installed release called its default. Adding a linter is an edit there, with its
findings fixed in the same change. The mechanics - the pinned linter version, the
`go-version-file: .go-version` + `check-latest: true` pair on every `setup-go`
(go.mod's `go` line is a FLOOR, not the toolchain), which steps `changes` gates and why
it is a step-level `if` rather than a `paths:` filter - are all in the
`.github/` entry of the package layout below. CI
additionally runs a **compose** job - the pre-merge half of the
release build, `metabuild -data data --community <community checkout>/data` over
a shallow clone of the community repo - because the CROSS-TREE rules (a sidecar
keyed by a work this core does not hold, a key riding a redirect, two sidecars of
one kind on one work) cannot be asked of one root, and without it a core PR that
retires or deletes a sidecar-keyed work went green and broke the RELEASE STREAM
after merge. `release.yml` builds and publishes a dated release
(`data-vYYYY.MM.DD-<core7>-<community7>` - BOTH short SHAs, because a community
merge can now trigger a release and the core sha alone stopped being unique;
nothing reads the tag's shape, since every consumer selects a data release by
ASSET PRESENCE at the maximum `published_at`, so older single-sha tags stay
valid) when data, schema, the BUILDER (`internal/build/**`, `cmd/metabuild/**`)
or the COMPOSE code (`pkg/check/**`, `pkg/pack/**` - `resolveSidecarKeys` re-keys
sidecars and the collision keeper decides what ships, so they shape artifact
CONTENT) changes land on main, and on a `community-data` repository_dispatch from
the community repo. The artifact is compiled, so a new index or a `SchemaVersion`
bump must reach a release without waiting for an unrelated data edit. A run whose
tag already exists as a PUBLISHED release - the same day, the same pair of SHAs,
so genuinely nothing new - skips rather than failing; the same tag sitting as a
DRAFT is a previous run that died before publishing, so it is deleted and this
one composes again (the probe reads the release LISTING, which shows drafts to a
token with contents:write, because a draft carries no git tag to be found by).
Asset
contract: `meta.sqlite.gz` + `meta.sqlite.gz.sha256` (the universal anchor),
`meta.sqlite.sha256` (raw-file digest, for verifying a patched artifact), and a
best-effort `meta.sqlite.patch.from-<PREV_TAG>.zst` (a zstd `--patch-from` binary
delta against the previous data release, `--long=31`; a stock zstd CLI consumer
must pass `--long=31` at decompression time for these large artifacts). The prev
release is selected by asset presence - the non-draft, non-prerelease release
carrying `meta.sqlite.gz` with the **maximum `published_at`** (not the
first-listed: GitHub's release list order is not publish-chronological) -
because the repo also cuts code/image `v*` releases with no data assets, so
GitHub's "latest" can be either kind. The release is cut as a DRAFT (at
`--target` the composed core sha), verified, and only then published
(`gh release edit --draft=false`, with `isDraft` read back rather than inferred
from an exit status): publishing on creation raced the uploads two ways - a
consumer selecting the newest non-draft release carrying `meta.sqlite.gz` could
pick one whose checksum files were still going up, and a verification that then
failed left a broken release visible. Every asset is VERIFIED
before that publish: the two checksum files are downloaded and compared
byte-for-byte with the ones just computed, and every asset the run uploaded - the
410MB gz and the delta included - is checked against the release API's own `size`
and `digest` (GitHub's SHA-256 of the bytes it stored, so nothing is re-downloaded
to hash locally; a null digest FAILS rather than passing quietly). The assets ARE
the consumer contract, so a truncated upload is a broken catalogue for everyone
rather than a broken run - and a verification that fails DELETES the draft, so
nothing half-made is ever visible and a re-run starts clean. The webhook
notification is sent after the publish. A `workflow_dispatch` is refused unless the checked-out
ref is an ancestor of `origin/main` (the push and dispatch triggers are already
main-only; dispatch is the one door that takes any ref, and a release cut from a
branch becomes THE catalogue the moment it lands). The
workflow serializes on a `data-release` concurrency group so two quick merges
can't both base a patch on the same prev tag. Go 1.26; golangci-lint v2 at a
green baseline over the linter set `.golangci.yml` pins.

## The data model (the contract)

Slug is identity (`^[a-z0-9]+(-[a-z0-9]+)*$`); the FILE a record sits in is
storage, not address. JSON Schemas in `schema/*.schema.json` (draft 2020-12,
`additionalProperties: false`) are the public contract - embedded into the
tooling via `schema.go`, so schema edits are code changes with tests.

**Three slugs are RESERVED in all three id namespaces** (works, people, series):
`search`, `latest` and `match` (`pkg/model/reserved.go`, the one definition). They are
LITERAL segments of the API's own routes - `/api/v1/works/latest`,
`/api/v1/works/match` and the three type-scoped searches - and a literal beats a
wildcard in the router, so a record
stored under one of them is findable by search and then unopenable through its
family's `{id}` route. The tree really held one (the work *Search* by Alyssa Rose
Ivy, renamed to `search-alyssa-rose-ivy` in the change that added the rule;
`match` joined with its route in 2026-10, when no record in any family held it).
`pkg/check`'s `checkReservedSlug` refuses them and every minter steps off them
using the family's existing collision convention: a work takes the
author-suffixed candidate (`primaryWorkCandidates` marks the reserved base probe-only,
so a legacy record there is still MERGED into rather than forked beside), a
series takes the numeric one (`importer.SeriesSlugAt`, which shifts the whole
chain so getOrCreateSeries and both read-only twins keep agreeing), and a person
takes the single canonical variant `model.ReservedPersonSlug` composes
(`search-person`) - which is what lets a person actually named "Search" satisfy
this rule and the person-slug rule at once, since `model.PersonSlug` is the one
call both the minting and the checking go through. Recording ids are deliberately
NOT reserved: their route's literal (`chapters`) FOLLOWS the wildcard, so nothing
can shadow.

**A retired slug keeps resolving** - the slug TOMBSTONE table,
`data/redirects.json` (`{"works": {"<old>": "<new>"}, "people": ..., "series":
...}`, schema `redirects.schema.json`, type `model.Redirects`, read/written by
`pkg/redirects`). A slug is public API: a meta.audiosilo.app URL, a
`books.work_id` in every audiosilo-sidecars install, a contributed sidecar's work
reference and audiosilo-server's community-metadata seam all hold one, so a
data-quality repair that MERGES duplicate records may not simply drop the id it
retires. The mechanism is deliberately narrow. It is **not a family**: a retired
slug is no entity, so there is nothing to place, bound, split or heal - it is the
one recognized non-pack file of the data tree, accounted for by
`pack.RedirectsFile`/`Listing.Aux` (so metacheck's "every file is accounted for"
rule stays total rather than gaining an exemption per caller) and by a
`.gitattributes` line keeping the pack merge driver off it (which
`scripts/pack-union-merge.sh` now backs up by REFUSING any file with no top-level
`entries` object - handed this one it used to "merge" it into `{"entries":{}}` and
report success). Duplicate JSON keys are refused by both readers through
`pack.CheckNoDuplicateKeys`, the same scan a pack goes through: last-wins would
drop a redirect invisibly to the schema, and a directory named
`data/redirects.json` is an error rather than a silent "no redirects". And it is not a
second addressing scheme: `pkg/check`'s `checkRedirects` refuses a source that
is a live id, a target that is not, a self-redirect, a chain (a target that is
itself a source) and a reserved route literal on either side, while
`pkg/redirects.Add` collapses chains in BOTH directions as they are written (a
target since retired, and yesterday's tombstones pointing at today's source), so
resolution is always ONE lookup and can never loop. `metabuild` writes the table
into the artifact (`redirects(kind, old_slug, new_slug)`, **schema_version 5**,
the primary key IS the lookup index) and `metaserve` answers a retired slug with
**301** at the same route under the surviving slug - see the serve section. The
`kind` is the FAMILY spelling (`works`/`people`/`series`), not `search_fts`'s
entity kind, because the namespace doubles as the route segment a `Location` is
built from. Recordings are deliberately not a namespace: a recording is addressed
inside its work, so it travels with the work slug. The full rationale and the
invariants live on `model.Redirects` - one statement, pointed at from everywhere
else that touches the table. One gap is open by design and recorded on
`pkg/redirects`: the file has no merge driver of its own (git's line merge is
sound-or-loud on it, but conflicts often enough that the intake sweep may stall
once redirects are added regularly). **No minter creates a record at a retired
slug**: a retired person slug resolves to its survivor, a retired series BASE
slug joins its survivor (a retired numbered candidate is stepped past), and a
retired work candidate is judged as its survivor - one lookup,
`model.Redirects.Survivor`; the per-family rule is stated once, in
`internal/importer/tombstone.go`. **A retitle records an ALIAS**: a work's slug
never follows its title, so a `retitle-work` that leaves the record off the chain
its NEW title composes ("Messenger's Legacy" at `messengers-legacy-novella`) would
let the next import of that title miss it and mint a duplicate beside it (the
duplicate-identity guard can only drop such a row, losing the edition). So
`internal/repair`'s retitleWork records the slug that import would mint at
(`importer.WorkTitleAlias`, the create walk's own first free candidate) as a works
tombstone onto the record, and the import then merges through it. The alias's
source never held a record, which the rules above allow (they say what a source
IS - never a live id - not what it was), and metaserve answers it with a harmless
301. A serial volume at `<title>-book-<n>` gets none (its position probes reach
it; an alias would pull every claim-free row into that one volume). The real-data
`TestDataWorkAndSeriesSlugsAreCurrent` counts a chain candidate tombstoned onto
the record as on the chain, which is the importer's own reach. Only metarepair
records aliases today: an intake correct-data TITLE correction does not (no
intake path writes the tombstone table), so one that leaves an apostrophe or
transliterated title off its chain turns that real-data test red until a
maintainer adds the alias.

**Storage is range-packed** ([PACK-SPEC.md](PACK-SPEC.md), implemented in
`pkg/pack`): four families of pack files, each `{"entries": {"<slug>": {...}}}`,
each file covering `[its own bound, the next sibling's bound)` where the bound is
its name minus `.json`.

```
THIS repository (tree profile `core`):
data/works/<dir-bound>/<bound>.json            CC0      work composites (work + its recordings)
data/people/<bound>.json                       CC0
data/series/<bound>.json                       CC0
data/redirects.json                            CC0      the slug tombstone table (no pack, no family)

audiosilo-meta-community (tree profile `community`):
data/works-community/<dir-bound>/<bound>.json  CC BY-SA characters + recaps + description, keyed by work slug
```

Four FAMILIES, two roots: the family table is unchanged by the split and a
family's layout is identical wherever its root sits, which is why the split
needed no new storage concept - only the tree profiles (PACK-SPEC.md). works and
works-community carry a directory level from day one; people and series
are flat until they exceed the per-directory pack cap. Caps: 256KB target / 512KB
hard / 1,000 entries (works-community 200) / 512 packs per directory, with a
single-entry exemption from the size cap (an entry bigger than the cap cannot be
split). Only a family's very FIRST pack and directory are named `0`; every other
pack is named by its own first slug and every other directory by its first pack's
bound. Bounds change only on split. **Placement self-heals**: `metafmt --write`
relocates a misplaced entry, resolves duplicate slugs, performs due splits and
rebinds, and re-renders canonically - so nobody computes placement by hand, and a
contributor's approximately-right edit is corrected mechanically. Two PRs editing
one pack conflict; the resolution is a THREE-WAY merge of the entries (base
included, so a deliberate deletion is not handed back) plus a re-render -
`scripts/pack-union-merge.sh`, driven through real rebases by
`scripts/packmerge_test.go`, and run by the intake bot on its own PRs. An entry
present on both sides that ONE side left exactly as the base had it was changed
by the other side alone, so that side's version is taken, in every family and at
every level the merge descends to (the entries map, a work's own fields as one
unit, its recordings map, a works-community entry's members), through ONE
implementation of the base rules (the script's `merge3With`) that every level
calls rather than a copy per level - without that
ordinary three-way rule a series the sync bot extended on main read as "both
sides changed" beside an intake branch that had only added other series to the
same pack, and the sweep failed that branch on every push (#2338). An entry
both sides really changed is a refusal except in the two families that have something
independent one level down, and the FAMILY (the path) decides which rule applies:
a **works** entry whose own fields are identical or changed on one side only,
and whose `recordings` maps differ, merges those maps (two narrations of one book), and a **works-community** entry,
which IS a map of members, merges DISJOINT members (a characters PR and a recaps
PR for the same book) - the same base rules one level deeper, so a member both
sides wrote is still a refusal. An entry both sides ADDED is not that conflict at
all - the base holds no version for either to have edited - so in EVERY family the
two copies merge when neither contradicts the other: `sources` unioned, a series
taking the (work, position) pairs only one side saw, authors/narrators in another
order keeping the order already on main, a key only one copy carries taken, and
anything the two state differently still refused. That is what two overlapping
library imports collide on (29 packs across issues #2274/#2275, all of them one
record minted twice). It is also a git MERGE DRIVER, which is how the
sweep runs it (`.gitattributes` names it for `data/**/*.json`; the driver is
configured in the sweep and by any contributor who wants it, and where it is not
configured git falls back to its own merge exactly as before). That is not a
convenience: git's LINE merge is unsound for a pack file. Two branches adding the
SAME entry key usually collide and stop, but when a side also changed the
neighbouring entry the two insertions can be anchored at different lines and git
applies BOTH, writing the key twice - a file `pkg/pack` refuses and `metafmt`
cannot re-render, so the sweep left the branch stuck instead of converging (issue
#1695, twice on 2026-08-09). As a driver the script sees the three versions
before any line merge happens, so the shape cannot arise; a clean rebase that
merged this way is re-rendered and re-placed by the `metafmt --write` +
`metacheck` pass the sweep runs when the driver reports it merged. The
pre-migration layout was one file per record
(`data/works/<shard>/<slug>/work.json` and friends); `cmd/metamigrate` converted
it, `pkg/check` and every writer now refuse it loudly.

- **work** - an entry in the works family, keyed by work slug: the abstract book
  (title, authors (person ids), language, first_published, xrefs (wikidata/
  openlibrary/goodreads/print ISBNs), optional `genres` (values from the
  controlled vocabulary enum `common.schema.json#/$defs/genre` - a flat,
  retailer-neutral list the project owns; sorted ascending, enforced by
  `checkGenresSorted`; never a retailer's taxonomy verbatim, see LICENSING.md
  "Genres"), optional `credits` (role-qualified contributors as
  `{person, role}` pairs, role from the controlled enum
  `common.schema.json#/$defs/credit_role`: adaptation, afterword, contributor,
  editor, foreword, illustrator, introduction, preface, translator. Additive and
  purely parallel to `authors`, which stays the identity list - a person credited
  here is not removed from it. One person MAY hold several roles (a source really
  does say "editor and translator") but never the same role twice:
  `checkCreditPairs`. Emitted only when a source STATED the role. ORDER is
  deliberately NOT checked - a credit list is a set of independent facts, not a
  billing order like `authors` - but the importer emits it sorted by
  (person, role) so one set of credits has exactly one byte-form)) PLUS its
  recordings, nested as `"recordings": {"<rec-slug>": {...}}`.
  One book is one entry, so a recording edit is a read-modify-write of its work.
  Contributor roles are a WORK-level fact: a qualifier on a narrator credit is
  edition-level evidence and is stripped without stating anything (measured at
  0.027% of narrator credits, nearly all of it scraping junk). Optional
  `translation_of` (`$defs/slug_set`, sorted ascending) links a TRANSLATION to
  the work(s) it translates - stated evidence only, never inferred; pkg/check
  (languages.go) requires every target to be a LIVE work (a retired one is named
  with its survivor), not the work itself, in a different primary subtag
  (`model.PrimarySubtag`), and not itself a translation (no chains).
- **recording** - a member of its work entry's recordings map: a specific
  narration/production, with narrators, abridged (**optional**: absence =
  unknown, so importers omit it rather than guess), runtime_min, release_date,
  publisher, region-scoped `asin[]`, `isbn[]`, cover_url, chapters, and for a
  LibriVox project `librivox_id` (its identity, unique), `archive_id`, `links`
  and a per-chapter `audio_url` (LICENSING.md's public-domain exception;
  `metaimport librivox`). One work,
  many recordings (Harry Potter: Stephen Fry AND Jim Dale, each with its own
  ASINs). It keeps its own `id`, `work` backref, `license` and `sources`, so its
  bytes are the same as when it was a file of its own.
  **A production released in several marketplaces stays ONE recording**: the
  region rides on the identifiers and the imprint, never on a second record.
  An `isbn[]` entry is a bare STRING (region unstated) or the object
  `{"isbn","region"}`, used ONLY when the region is known; both properties are
  required, so an unstated region has exactly one spelling. The bare string is
  not merely the shape that predates regions, it is the **scale** form: no bulk
  writer emits anything else (no import source states an ISBN's marketplace, and
  the issue forms contribute a scoped entry one at a time), and across 142k
  recordings one canonical line beats the object's four against the 256KB pack
  caps - which is what enrichment filling ISBNs in bulk will spend. Optional
  `publishers[]` (`{"region","publisher"}`) holds the OTHER regions' imprints -
  the top-level `publisher` stays THE publisher of record, so `publishers[]` may
  never restate it and may never name one region twice
  (`checkRegionalPublishers`), and the schema's `dependentRequired` makes the
  pairing structural. No importer emits it; its writers are the issue forms
  (`internal/issueform`, which composes it on the add forms and contributes one
  imprint at a time through the additive correction) and a hand-authored PR.
  ISBN uniqueness keys on the VALUE, so the two spellings of one identifier
  collide as they should. `metabuild` flattens both forms into the same
  `recording_isbns` row - the artifact keeps an ASIN's region but deliberately
  DROPS a stated ISBN region, so `lookup?isbn=` and `/abs/search` answer "which
  recording", never "which marketplace"; SchemaVersion is unmoved and a reader
  that ever needs the region is a later schema_version change. `publishers[]` is
  deliberately not in the artifact either - the data accrues ahead of its
  readers, as work credits do. The region vocabulary is one shared def
  (`common.schema.json#/$defs/region`), reached by `asin[]`, the region-scoped
  ISBN and `publishers[]` alike, with drift guards on both Go mirrors
  (`internal/importer` `marketplaces`, `internal/issueform` `regionAliases`).
  NOTE the WORK's print ISBNs (`xref.isbn`) are deliberately untouched: they stay
  a flat string list (`$defs/isbn_list`); the recording's `isbn[]` is defined
  inline in `recording.schema.json`, beside its `asin[]` neighbour.
  Retailer availability is deliberately NOT a catalogue field. Work-detail API
  responses derive `purchase_links[]` from the durable identifiers already in
  the artifact: a region-scoped ASIN becomes an Audible marketplace URL and a
  13-digit recording ISBN becomes a speculative Libro.fm URL. Each link says
  availability is `unknown`; work-level print ISBNs are never used. This keeps
  volatile observations and identifier-duplicating URL data out of Git.
- **person** - an entry in the people family, shared by author and narrator roles
  (a person can be both). Optional `kind`
  (`person`/`group`/`publisher`/`synthetic`) marks the records that are not an
  individual - a full cast, a corporate credit of record (Audible Studios,
  Marvel Press), a text-to-speech production. ABSENCE MEANS person: nothing
  infers the field, so an unset kind is "an individual, or unclassified", never
  a guess. The contributor path is the correct-data issue form, whose allowlist
  takes `kind` and validates it against the enum (built from
  `model.PersonKinds`, so the form and the schema cannot drift), so classifying
  a record never needs a raw pack-file PR. Classifying the existing corporate
  records is deliberately left to a later, human pass. `synthetic` is the ONE
  kind a writer sets by itself, and only on one record: `virtual-voice`, the
  canonical every AI-narration credit folds onto
  (`internal/importer/synthetic.go`) - its kind is not somebody's
  classification, it is what the credit said.
- **series** - an entry in the series family: name + ordered works with **string**
  positions ("1", "2.5") including omnibus ranges ("1-3.5"); no two works may
  share a position. Optional `translation_of` (the work's slug set and rules one
  level up; a series has no language field, so both sides are
  `model.SeriesLanguage` - the strict majority of the members' primary subtags -
  and the language rule stands down when either side ties), `ordering`
  (`publication`/`chronological`/`recommended`, `model.SeriesOrderings`) and
  `ordering_of` (a VARIANT's franchise primary; the schema's `dependentRequired`
  makes it carry `ordering`). An ordering FAMILY is a primary plus every series
  naming it (`model.Series.OrderingPrimary`): the target is a live series, never
  itself, ONE hop deep, and no two members state the same ordering (a second series
  in one order is a duplicate to fold). Two rules are ADVISORIES, never failures: a
  variant listing a work its primary does not (`ordering-variant-not-subset` - a
  chronological order holding a prequel novella), and a series `translation_of`
  whose two sides DERIVE one language (`series-translation-same-language` - the link
  is still true, a member is misfiled, and the sync bot must never go red on a link
  it did not write); the same rule over two WORKS' stated languages is a problem. No importer writes the three fields; every path that rewrites
  an entry edits it as a raw map, so they ride through (pinned per path in
  internal/importer's languagefields_test.go).
- **characters** - the `characters` member of a works-community entry (keyed by
  the WORK's slug), which lives in the community repo now, not here.
  Community-authored, spoiler-tagged character entries. Each
  has an `id` (unique within the member, not globally - two works may each have a
  `bilbo-baggins`), `name`, optional `aliases`/`role`, a `reveal` position (the
  spoiler gate), a length-capped own-words `description`, and optional `xref`
  (a shared `wikidata` QID links a recurring character across a series' per-work
  entries). Recurring characters are **re-described per book** (Kindle-X-Ray
  model), so spoilers stay bounded by which book you're in.
- **recaps** - the `recaps` member of the same works-community entry:
  position-keyed "story so far" summaries. Each has a `through` position (safe
  to show once the listener finishes that chapter), an optional `scope`
  (`book`/`series` - a `chapter:0`+`series` entry is the "previously, in earlier
  books" recap), and length-capped own-words `text` (cap 3000). No two recaps in
  one member share a `through` chapter. It also carries two optional
  whole-book summaries for a reader who has finished the book: `in_short` (the
  whole arc in one paragraph, ending included; cap 1500) and `ending` (how the
  book closes, stated plainly; cap 2000 - a crisp sequel-handoff, deliberately
  tighter than a chaptered recap entry).
- **description** - the `description` member of the same works-community entry,
  and the one that is SINGULAR: one work has one description, so the member name
  IS the kind (`model.KindDescription`) and the Catalog's slice is
  `Descriptions`. Community-authored `text` (200-1500 chars, floor and cap both
  enforced by the schema), plus the usual `work` backref, `license` and
  `sources[]`. **Spoiler-free is the contract**: the setup, the premise and the
  hook, with no reveal past the opening act - which is what makes it the one
  piece of community prose that is safe to quote into a `<meta name=description>`
  and to show unopened on a page, where a recap is position-gated and
  `in_short` states the ending outright. It is deliberately NOT the work
  record's own CC0 `description` field, which nothing writes: the two carry
  different licenses, so they stay two fields all the way to the wire. There is
  NO issue form for it (decided): the contributor path is a hand-authored PR on
  the community repository plus the generation pipeline, and what the intake bot
  owes it is only that composing a characters or recaps member must carry an
  existing description member through untouched (`internal/issueform`'s
  read-modify-write, pinned by a test). `metaextract ngram` scans its `text`
  like every other own-words field, and the COVERAGE surface sees it - a
  `with_descriptions` total, a `has_description` filter and `description` in a
  work's `missing` list, all behind schema_version 6 - because that browser is
  the queue a generation wave works through, and a member it cannot see is a
  member nobody is asked to write.

**Position model** (`common.schema.json#/$defs/position`): `{ "chapter": <int
>= 0> }`, the logical **edition-independent** work chapter (1-based; `0` = front
matter / prior-book knowledge). A consumer maps its recording-chapter timeline
onto these ordinals; text-to-audio alignment is a consumer concern, out of
schema scope. The object shape is deliberately extensible (a later `paragraph`/
`offset_ms` can be added without a breaking change).

**work** and **recording** additionally carry an optional `added_at`
(`common.schema.json#/$defs/date_or_datetime`): `YYYY-MM-DD` when stamped by
the importer/intake bot at creation, or a full RFC 3339 timestamp with offset
for records dated by the migration's one-time git-history backfill (those values
are the old release.yml git walk's, spliced in verbatim, which is what made the
artifact equivalence proof pass). `metabuild` reads the field and falls back to
the newest `sources[].imported_at` (compared chronologically, timezones
normalized) for a record carrying neither; the `--added` flag, the release.yml
git walk and its full-depth checkout are all retired, so git history is no
longer load-bearing anywhere.

Every entity carries `license` and `sources[]` (provenance: type/ref/
imported_at) so any source can be audited or retracted wholesale. **Two license
layers, enforced structurally by the schema** (`$defs/license` vs
`$defs/license_content`): the CC0 core (works/recordings/people/series) is
`CC0-1.0`; the CC BY-SA layer (characters/recaps/description) is
`CC-BY-SA-4.0`. A core
record can never carry the share-alike license and a sidecar can never carry
CC0 - the boundary is a schema enum, not a convention (see LICENSING.md).

Characters/recaps flow all the way through: `metabuild` writes them into the
SQLite artifact (`characters`/`character_aliases`/`recaps` tables, added in
**artifact schema_version 2**; the per-work `recap_summaries` table for
`in_short`/`ending` was added in **schema_version 3**, one row per work whose
recaps sidecar carries at least one of the two fields; work genres landed in
**schema_version 4** as the `work_genres(work_id, genre)` set table; the
spoiler-free description landed in **schema_version 6** as
`work_descriptions(work_id, text, license)`, one row per work carrying the
member, the primary key doubling as the lookup index; the LANGUAGES layer landed
in **schema_version 7** as the `translations(kind, id,
target)` table - every work and series `translation_of` link, `kind` the family
spelling as in `redirects`, `id` always the translation - plus three `series`
columns (`language`, DERIVED at build through `model.SeriesLanguage` and NULL on
a tie, `ordering`, `ordering_of`), `search_fts.language` as its LAST column and
`idx_works_language(language)`; the LIBRIVOX layer landed in **schema_version 8**
(the current version: recording LibriVox columns, `chapters.audio_url`, people
LibriVox ids and life years, `work_credits`, `recording_sources`; PACKAGES.md
"The LIBRIVOX layer"); character `ord`/recap
position order preserved, deterministic), and `metaserve` returns
them inline on `GET /works/{id}`
(`workDetail.characters`/`.recaps`/`.recap_summary`/`.genres`/
`.community_description`/`.translation_of`/`.translations`, `omitempty`). The
serve queries degrade gracefully if the tables are absent (a newer binary
briefly serving an older release): characters/recaps gate on schema_version >= 2,
the recap summary on >= 3, genres on >= 4, the slug redirects on >= 5, the
community description on >= 6, the languages layer on >= 7 and the LibriVox
layer on >= 8, so a
table's absence is "no data" (for redirects: a retired slug 404s as it did before
the mechanism existed), not a 500. The redirect, description and languages gates
are asked ONCE at load rather than per request - those tables are EMPTY in every
release until their first record lands, and all sit on ordinary 200 paths (the
description on every work page, both guide pages and every ABS candidate; the
translations and the ordering family on every work and series page) - so an
artifact that CLAIMS version 5, 6, 7 or 8 without its table (for 7: the
translations table or the series ordering columns; for 8: `librivoxShapeSQL`) fails to open, with an
error naming that claim; the builder writes the version and the table together,
so the combination is a corrupt file rather than an old one. **Authoring the expressive
layer is documented in the COMMUNITY repository's `AUTHORING.md`** (the reusable
process: positions, spoiler model, copyright caps, checklist) - it moved with the
layer, as EXTRACTION.md and EXTRACTION-AUDIO.md did, and this repo keeps only
pointer stubs at those three paths. The four exemplar series (First Law,
Scholomance, Lord of the Rings, Magic Faraway Tree) are the worked reference,
there.

## Reference: PACKAGES.md (read on demand)

These topics live in [PACKAGES.md](PACKAGES.md), not here, to keep this file small.
**Before changing code a topic below covers, read its entry** (`grep -n '<topic>'
PACKAGES.md`, then Read that range). When you add or change a rule, edit it there and
keep this index in step (one line per topic).

- **Package layout**
  - `cmd/metacheck|metafmt|metabuild`
  - `cmd/metaserve`
  - `cmd/metaextract`
  - `cmd/metascan`
  - `cmd/metaimport`
  - `cmd/metaissue`
  - `cmd/metamigrate`
  - `cmd/metaremediate`
  - `cmd/metaaudit`
  - `cmd/metarepair`
  - `cmd/metadiff`
  - `pkg/model`
  - `pkg/canonical`
  - `pkg/check`
  - `pkg/pack`
  - `pkg/redirects`
  - `pkg/extract`
  - `pkg/scan`
  - `internal/importer`
  - `internal/issueform`
  - `internal/format`
  - `internal/testpack`
  - `internal/rawentry`
  - `internal/recorddiff`
  - `internal/sqlitedsn`
  - `internal/ghhost`
  - `internal/unionfind`
  - `internal/atomicfile`
  - `internal/reportdir`
  - `internal/remediate`
  - `internal/titlerule`
  - `internal/audit`
  - `internal/repair`
  - `internal/migrate`
  - `internal/build`
  - `pkg/query`
  - `pkg/query/querytest`
  - `pkg/release`
  - `pkg/release/releasetest`
  - `internal/serve`
  - `internal/httpx`
  - `internal/artifacttest`
  - initials spellings are one person
  - studio concatenations are split off a name
- **Roadmap**
  - Phase 0 (done)
  - Phase 1
  - Phase 1.5 (integration landed)
  - Libex integration (in progress)
  - Data quality (audit landed, PREVENTION landed, repairs next)
  - Serving at scale (pre-seed, landed)
  - Phase 2
  - Phase 3 (pipeline landed)

## Conventions

- **Facts only, never fabricated.** Seed/contributed data is real and
  verifiable; if a fact can't be verified, omit the (optional) field rather
  than guess. No publisher blurbs, no cover files - covers are URLs,
  descriptions are community-written (see LICENSING.md).
- **Every rule ships with a test.** metacheck rules have a passing fixture and
  a violating fixture; `pkg/canonical` has a real-data test so the seed
  tree can never drift from canonical form.
- **CI security is deliberate**: `check.yml` uses plain `pull_request` (fork
  PRs get a read-only token, no secrets). Never introduce
  `pull_request_target` that executes fork code; privileged follow-ups go in a
  separate `workflow_run` workflow consuming artifacts only.
- **Workflow hygiene** (the rules a new job or step inherits): every job
  carries a `timeout-minutes` sized at about twice its observed maximum, with
  the measurement in a comment; every `actions/checkout` sets
  `persist-credentials: false` EXCEPT the intake rebase sweep's, the one job
  that pushes with plain git; a tool installed inside a `run:` string is pinned
  to an exact version (`@anthropic-ai/claude-code`, govulncheck), since
  dependabot cannot see one there (claude-code tracks the npm `stable`
  dist-tag); dependabot ignores the ODD `node` image majors, since only even
  ones become LTS, and every `golang` minor and major, since the image's minor
  is the Go version and moves by hand with go.mod and every setup-go. The
  rebase sweep runs only MAIN's tools (metafmt, metacheck and the merge
  driver, built and copied before the loop) and sweeps only same-repository
  branches whose diff is confined to `data/` and whose commits add or change
  no symbolic link; a branch it refuses gets ONE marked comment saying why,
  and a branch it fails to rebase gets one per (attempted head, reason) through
  the same `comment_once` helper, so a stuck branch is not re-told on every push
  to main while a new commit or a new failure still is - the reason carries the
  failing tool's first problem line (`noted`), since "metacheck rejected the
  merged tree" alone would mute a new cause. Only markers in the sweep's OWN
  comments count (its login resolved once through GraphQL's viewer), so a
  marker pasted by anyone else cannot mute it. The INTAKE job needs no such
  marker: it triggers on `labeled` (a routing label) and `edited`, not `opened`,
  because an issue opened with its routing label also fires that label's
  `labeled` event - admitting both composed and posted every verdict twice
  (#2406, #2408) - and one opened without it is skipped by `opened` anyway.
  intake.yml's `-noop` concurrency group carries a verbatim copy of the intake
  job's `if` (see the comments in intake.yml for both). Its third door is
  `workflow_call` from `route.yml`, which labels an issue filed through the
  AudioSilo GitHub App (no labels survive an API create by a non-collaborator)
  with GITHUB_TOKEN - which fires no `labeled` run - and calls intake in the same
  run; a called run is admitted only for the issue that started its caller, and
  routes on the `template` input. route.yml's caps and denylist are in
  GOVERNANCE.md.
- **Deterministic builds**: metabuild inserts in sorted id order so identical
  data produces identical artifacts.
- **Governance**: merge policy and trust tiers live in
  [GOVERNANCE.md](GOVERNANCE.md); schema/tooling/.github changes always need
  maintainer review (CODEOWNERS).
- **Hyphens, never em dashes** (workspace-wide rule), in docs, comments, and
  generated text alike.

