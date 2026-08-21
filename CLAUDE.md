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

- **Fable (the main session) is the orchestrator only.** Task decomposition,
  design taste, final QA. It never writes feature code directly; it may write
  orchestration artifacts (this file, briefs, governance docs, commit messages).
  Runs at high effort.
- **Opus subagents do the implementation**, one per task, parallel when tasks
  are disjoint. Each gets a self-contained brief and must leave the gate green.
- **Token-hungry chores go to cheaper models** (Sonnet/Haiku): fact research,
  bulk data sweeps, log triage.

## Build / test / gate

```sh
cd ~/dev/audiosilo/audiosilo-meta
go build ./... && go vet ./... && go test -race ./... && golangci-lint run
# ~3min wall for the -race suite; no -timeout flag needed. pkg/check's real-data
# test skips under -race (raceEnabled, race_{on,off}_test.go) - the fixture
# suites cover the parallel loader and the real tree is data coverage, still
# validated by the non-race run and by metacheck below.
go run ./cmd/metacheck --profile core       # validate the data tree (~10s over 133k works)
go run ./cmd/metafmt --check --profile core # canonical formatting (--write to fix)
go run ./cmd/metabuild -o meta.sqlite   # build the CORE-ONLY artifact (no sidecars)
go run ./cmd/metabuild -data data --community <community-repo>/data -o meta.sqlite  # ... the REAL artifact, from both repos
go run ./cmd/metaserve --db meta.sqlite --addr :8080   # serve the read-only API
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
  tests are - and CI passes `--profile core` explicitly at every one of the four
  sites: check.yml, the intake bot's compose step AND its re-render, and
  release.yml's core-side validation. A wrong reading there is a red check, or (for
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

**Before a change is done, all of the above must pass.** CI
(`.github/workflows/check.yml`) gates build/vet/test/metacheck/metafmt on every
PR and push to main; CI additionally runs a **compose** job - the pre-merge half of the
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
tag already exists - the same day, the same pair of SHAs, so genuinely nothing
new - skips rather than failing. Asset
contract: `meta.sqlite.gz` + `meta.sqlite.gz.sha256` (the universal anchor),
`meta.sqlite.sha256` (raw-file digest, for verifying a patched artifact), and a
best-effort `meta.sqlite.patch.from-<PREV_TAG>.zst` (a zstd `--patch-from` binary
delta against the previous data release, `--long=31`; a stock zstd CLI consumer
must pass `--long=31` at decompression time for these large artifacts). The prev
release is selected by asset presence - the non-draft, non-prerelease release
carrying `meta.sqlite.gz` with the **maximum `published_at`** (not the
first-listed: GitHub's release list order is not publish-chronological) -
because the repo also cuts code/image `v*` releases with no data assets, so
GitHub's "latest" can be either kind. The
workflow serializes on a `data-release` concurrency group so two quick merges
can't both base a patch on the same prev tag. Go 1.25; golangci-lint v2 at a
green baseline.

## The data model (the contract)

Slug is identity (`^[a-z0-9]+(-[a-z0-9]+)*$`); the FILE a record sits in is
storage, not address. JSON Schemas in `schema/*.schema.json` (draft 2020-12,
`additionalProperties: false`) are the public contract - embedded into the
tooling via `schema.go`, so schema edits are code changes with tests.

**Two slugs are RESERVED in all three id namespaces** (works, people, series):
`search` and `latest` (`pkg/model/reserved.go`, the one definition). They are
LITERAL segments of the API's own routes - `/api/v1/works/latest` and the three
type-scoped searches - and a literal beats a wildcard in the router, so a record
stored under one of them is findable by search and then unopenable through its
family's `{id}` route. The tree really held one (the work *Search* by Alyssa Rose
Ivy, renamed to `search-alyssa-rose-ivy` in the change that added the rule).
`pkg/check`'s `checkReservedSlug` refuses them and every minter steps off them
using the family's existing collision convention: a work takes the
author-suffixed candidate (`workCandidates` marks the reserved base probe-only,
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
else that touches the table. TWO GAPS are open by design and recorded on
`pkg/redirects`: the file has no merge driver of its own (git's line merge is
sound-or-loud on it, but conflicts often enough that the intake sweep may stall
once redirects are added regularly), and no minter steps OFF a tombstoned slug the
way the work chain steps off a reserved one, so a later import re-creating a merged
duplicate fails metacheck loudly rather than resolving itself.

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
data/works-community/<dir-bound>/<bound>.json  CC BY-SA characters + recaps, keyed by work slug
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
both sides changed is a refusal except in the two families that have something
independent one level down, and the FAMILY (the path) decides which rule applies:
a **works** entry with identical own fields and differing `recordings` maps
merges those maps (two narrations of one book), and a **works-community** entry,
which IS a map of members, merges DISJOINT members (a characters PR and a recaps
PR for the same book) - the same base rules one level deeper, so a member both
sides wrote is still a refusal. It is also a git MERGE DRIVER, which is how the
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
  0.027% of narrator credits, nearly all of it scraping junk).
- **recording** - a member of its work entry's recordings map: a specific
  narration/production, with narrators, abridged (**optional**: absence =
  unknown, so importers omit it rather than guess), runtime_min, release_date,
  publisher, region-scoped `asin[]`, `isbn[]`, cover_url, chapters. One work,
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
  (a person can be both). Optional `kind` (`person`/`group`/`publisher`) marks
  the records that are not an individual - a full cast, a corporate credit of
  record (Audible Studios, Marvel Press). ABSENCE MEANS person: nothing infers
  the field, so an unset kind is "an individual, or unclassified", never a
  guess. Nothing WRITES it automatically; the contributor path is the
  correct-data issue form, whose allowlist takes `kind` and validates it against
  the enum (`internal/issueform`), so classifying a record never needs a raw
  pack-file PR. Classifying the existing corporate records is deliberately left
  to a later, human pass.
- **series** - an entry in the series family: name + ordered works with **string**
  positions ("1", "2.5") including omnibus ranges ("1-3.5"); no two works may
  share a position.
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
`CC0-1.0`; the CC BY-SA layer (characters/recaps) is `CC-BY-SA-4.0`. A core
record can never carry the share-alike license and a sidecar can never carry
CC0 - the boundary is a schema enum, not a convention (see LICENSING.md).

Characters/recaps flow all the way through: `metabuild` writes them into the
SQLite artifact (`characters`/`character_aliases`/`recaps` tables, added in
**artifact schema_version 2**; the per-work `recap_summaries` table for
`in_short`/`ending` was added in **schema_version 3**, one row per work whose
recaps sidecar carries at least one of the two fields; work genres landed in
**schema_version 4** as the `work_genres(work_id, genre)` set table; character
`ord`/recap position order preserved, deterministic), and `metaserve` returns
them inline on `GET /works/{id}`
(`workDetail.characters`/`.recaps`/`.recap_summary`/`.genres`, `omitempty`). The
serve queries degrade gracefully if the tables are absent (a newer binary
briefly serving an older release): characters/recaps gate on schema_version >= 2,
the recap summary on >= 3, genres on >= 4 and the slug redirects on >= 5, so a
table's absence is "no data" (for redirects: a retired slug 404s as it did before
the mechanism existed), not a 500. The redirect gate is asked ONCE at load rather
than per request, so an artifact that CLAIMS version 5 without the table fails to
open, with an error naming that claim - the builder writes the two together, so
the combination is a corrupt file rather than an old one. **Authoring the expressive
layer is documented in the COMMUNITY repository's `AUTHORING.md`** (the reusable
process: positions, spoiler model, copyright caps, checklist) - it moved with the
layer, as EXTRACTION.md and EXTRACTION-AUDIO.md did, and this repo keeps only
pointer stubs at those three paths. The four exemplar series (First Law,
Scholomance, Lord of the Rings, Magic Faraway Tree) are the worked reference,
there.

## Package layout

```
cmd/metacheck|metafmt|metabuild   thin CLIs; logic lives in internal/ (metafmt drives internal/format; metacheck prints advisories to stderr without affecting the exit status). metacheck and metafmt take `--profile all|core|community` - the TREE PROFILE (pack.Profile, PACK-SPEC.md) naming which families the root holds - defaulting to `all` (the whole database in one tree, which is what the fixtures are and what an unqualified "data root" must keep meaning; this repository is `core` since the cutover and its CI says so explicitly rather than by changing the default); metabuild's twin of that flag is `--community <dir>`, a SECOND data root holding the works-community family alone, which makes `-data` the CC0 core and composes the two into ONE artifact (build.Sources -> check.LoadComposed; a root holding no sidecars is a hard error, since the flag ASKS for the layer). Absent, nothing changes - one root, `check.Load`, the same bytes out. metabuild prints advisories to stderr with metacheck's own prefix and census line, exit status unaffected: the position-scale and retired-sidecar-key rules fire for real only where both trees are present, so the release build is the one place they are observable once the split lands
cmd/metaserve       thin CLI: the read-only HTTP API server (flag wiring only)
cmd/metaextract     thin CLI: epub -> chapter text + manifest (split), n-gram no-verbatim check (ngram). The TOOL stays here; the PROCESS docs (EXTRACTION.md, EXTRACTION-AUDIO.md, AUTHORING.md) moved to the community repo with the layer they produce, and the three paths here are pointer stubs
cmd/metascan        thin CLI: scan a local audiobook folder -> import JSON (flag wiring only)
cmd/metaimport      thin CLI: ingest an external library export into data/ (openaudible, libation, libex)
cmd/metaissue       thin CLI: an issue-form body -> canonical records + a machine-readable verdict (flag wiring only). `--profile` is the tree profile of `--data` (default `all`, this repository) and `--works-db <meta.sqlite>` is the release artifact a root WITHOUT the works family verifies a sidecar's work slug against - required under `--profile community`, and a usage error under a profile that holds works, since the tree is the better answer
cmd/metamigrate     thin CLI: the ONE-OFF conversion of a pre-pack file-per-record tree into the pack layout, with the added_at git-history backfill (--out rehearses out of place; logic in internal/migrate)
cmd/metaremediate   thin CLI: the ONE-OFF GraphicAudio part-product repair (dry-run by default; --write applies; logic in internal/remediate)
cmd/metaaudit       thin CLI: the READ-ONLY data-quality audit -> a report directory (`-data data -o <dir>`; flag wiring only, logic in internal/audit over the internal/titlerule primitives)
cmd/metarepair      thin CLI: applies the audit's NON-ADVISORY proposals to data/ (dry-run by default; `--op`/`--subclass`/`--only`/`--limit` select the wave, `-report <dir>` narrows it to a reviewed worklist, `--write` applies, `-o <dir>` writes the apply-report; flag wiring only, logic in internal/repair). It and metaaudit both take `--profile` and MUST be given the same one (metarepair re-runs metaaudit's detectors in process, so a report taken under one reading of the tree and applied under another is the drift the fresh-audit gate exists to prevent); BOTH default to `core` rather than to metacheck's `all` - see the gate section for why the cost of being wrong sets the default. metarepair additionally takes `--community <dir>`, the community checkout's data/ opened READ-ONLY, WITHOUT WHICH EVERY MERGE IS REFUSED (`community-data-required`): the sidecar-collision guard cannot be asked of a tree that does not hold the layer, and inferring "no sidecars" from its absence is what made it blind
pkg/model           PUBLIC entity structs, slug rules (Slugify and PersonSlug live here, the leaf both internal/importer and pkg/check reach - the importer imports check, so the reverse would be a cycle and a second copy would be a contract with two definitions; the importer mints person ids through PersonSlug and pkg/check verifies them through the same call, including the reserved-word variant it composes), the RESERVED SLUGS (reserved.go: the route literals `search`/`latest`, IsReservedSlug and the one canonical ReservedPersonSlug both the minting and the checking read), and addressing: PackLocation (pack path + entry key [+ recording key], what pack-layout problem reports render). The file-path addressing the old layout needed (Shard, ParseLocation) is gone. THREE places still parse the retired per-record path shape, each its own copy and each for a different reason: internal/issueform (a correction form's `record` field is a reference a submitter types), internal/testpack (fixture addresses read better than family + two map keys), and internal/migrate (the only one that reads it as a STORAGE location, because it is the only thing that still reads the old tree). Also the SOURCE TRUST TIERS (trust.go): the one table ranking sources[] types and the one bulk-mirror-only test the overwrite policy is written in terms of. And the SLUG TOMBSTONE table (redirect.go: model.Redirects - the ONE canonical statement of what it is for and what must hold of it, which every other site points at instead of restating; RedirectKind, whose value is the family AND the route segment; ValidRedirectSlug, the one rule both the writer and the checker ask). (leaf; also reached through pkg/check's exported Catalog)
pkg/canonical       PUBLIC canonical JSON (sorted keys, 2-space, trailing LF); CheckTree/WriteTree walk a tree, and their `...Except` twins take dir-relative paths (a directory subtree or a single file) to leave alone - just paths, since this package knows nothing about families or profiles, and a skipped file is never READ so it can be neither rewritten nor reported
pkg/check           PUBLIC pack-layout load (packcheck.go walks each family's pack listing; a family that is not in the pack layout is ONE loud problem naming metamigrate, and every JSON file the listing does not account for is an unrecognized location - never silence) + schema validation (wrapper schemas load-bearing; $ref reasons preserved via detailed output) + the pack storage invariants (placement, caps + single-entry exemption, bound validity, key/id agreement) + integrity/uniqueness/chapter/series/credit-pair/person-slug (a person's id must BE `model.PersonSlug(name)` - the same call the importer mints it with - so a record minted under an older slug rule cannot sit at an address nothing will probe again, the shape that forked Madeleine L'Engle in two)/sidecar-uniqueness/reserved-slug (no work, person or series id may BE an API route literal - see the data-model section)/redirects (the tombstone table is read - through the LISTING, like every other file this package reads, so LoadStore's as-of-open walk cannot disagree with a live disk probe - schema-validated, then checked against the assembled catalogue: a source is never a live id, a target always is, no self-redirects, no chains, no reserved literal either side. A schema-rejected document skips those rules rather than reporting a consequence of its own shape, an empty table returns before any id set is built, and the sets it does need are the ones checkIntegrity already built, hoisted into the load) rules; Result carries Problems (fail) and Warnings (advisory, e.g. a single entry over the 256KB target); an advisory's CLASS is named by the exported `AdvisoryClass` over the one `advisoryMarkers` table `AdvisoryCensus` also counts through, so a consumer grouping advisories (internal/audit files them as its LOADER subclasses) reads the same classification the census prints - and `TestEveryAdvisoryHasAClass` pins that every rule's advisory belongs to one, which is how the pack-storage advisory was found missing from the census (979 of 991 counted). `IdentityEqualWorks` is exported for the same reason: it is the one restatement of the importer's work-identity rule, drift-guarded on the importer side, and internal/audit's duplicate detector groups by it rather than by an author-set equality that misses the fork the rule exists for - it is now `IdentityAuthorsMatch` (identity.go) with one side read off a work, so a WRITER holding an incoming row asks the same nesting rule without fabricating a record. identity.go is the NORMALIZED WORK IDENTITY index (`WorkIdentity`: `titlerule.IdentityTitleKey` per work, the series name each title is read against - `SeriesNameIn` resolves a series a free text names, over a list narrowed ONCE at build time to the two-significant-word names no other series' fold collides with - and `MatchKey`/`Match` over the one pairwise predicate: language + author nesting + no stated-volume disagreement, which the census asks of two catalogued works and a writer asks of an incoming row). ONE index per LOAD, handed out on `Result.Identity`: the census builds it, and internal/issueform's intake gate and internal/importer's create guard probe THAT one rather than rebuilding it (the build cleans every catalogued title, so a second build is seconds spent on an answer that is identical by construction), which is also what makes the three defences unable to disagree about what one book is. That is why this package may import internal/titlerule (legal in-module, as pkg/scan already reaches internal/importer): the alternative was a second spelling of what a decorated title reduces to. `checkNormalizedDuplicateWorks` is the tree CENSUS over it - the advisory class `normalized-duplicate-works`, one line per collision group, DISJOINT from its identity-equal neighbour (a group is reported only when two members' RAW title slugs differ, so one defect is never counted twice) and never a problem, because the fix is a merge no rule may demand. 2,881 groups on the seeded tree at the time it landed, 3,160 once the key's series strip was boundary-anchored (see internal/titlerule); the count is what a repair wave is measured by, and the load costs ~8% more over 279k works (20.6s -> 22.3s) - the CENSUS's price, paid by every load because it is a rule of every load, which is also why `Result.Identity` is a hand-over rather than a lazy accessor: the work has already happened. A consumer that HOLDS the index holds the catalogue alive with it, so the importer takes it for the create mode only and the other two modes release the tree exactly as before; schema-valid entries that fail Go decoding are reported, never silently dropped. ONE walk per load (pack.Listing supplies the file accounting, the layouts and every family's tree; a data root or family root that is not a directory is an error, never an empty family). The PER-PACK work (parse, schema-validate, decode, size) is independent between files, so it runs on a bounded pool of GOMAXPROCS workers (`parallel.go`) while everything cross-record still runs afterwards, single-threaded, over the assembled catalogue - 66s -> 10s over the 133k-work tree. DETERMINISM is a property of the merge, not of the scheduler: a worker fills a `packResult` of its own (its problems, its advisories, its slice of the catalogue, its path index) and the coordinating goroutine merges them in the LISTING's order, so problems, advisories and catalogue order are byte-identical to the sequential walk this replaced, whatever order the workers finished in (`TestParallelLoadIsDeterministic` pins it across repeat runs and GOMAXPROCS 1/2/3/8 - its teeth are the CATALOGUE-order comparison, since `sortProblems` would mask a completion-order merge in the other two sections; the real-tree output was diffed byte-for-byte before and after). `LoadStore(*pack.Store)` is the writers' door in: it validates the tree the store was opened on over the store's own walk, so a validating writer walks the tree once instead of twice. It is AS-OF-OPEN by construction (documented on LoadStore) and it never grows the writer's memory: a pack it reads is released as soon as it has been validated, and the canonical render memo the cap check leaves is released with it - the store re-reads the handful of packs it writes to, which is what it always did. After a Flush the store's walk is stale, so LoadStore takes a fresh one - post-write validation stays a full independent load. LoadStore is parallel TOO: the borrower only ASKS the store's parse cache (`pack.Reader.Cached`, a lookup in two maps that writes to neither) and never fills it, and LoadStore is synchronous in its caller, so the store's own goroutine is blocked for the pool's lifetime and no Read/Drop/Flush can race it - stated on `pack.Reader` and at `check.readPack`, and filling the cache from there would now be a data race as well as the residency mistake it already was. Peak RSS over the real tree rises ~1.2GB -> ~1.6GB (measured 1.19 -> 1.56GB), most of which is GC pacing under a 6.5x allocation rate rather than packs held - a GOMAXPROCS=1 run sits at ~1.6-1.7GB for EITHER binary, and at most one pack file is resident per worker. The one real retention is the per-pack results a family's pool buffers until it drains, since they are merged in listing order and a pack that finished first waits for the packs before it: +8.4% live set, bounded by the largest family, and the price of the determinism above. A panic on a worker is captured and re-raised on the coordinating goroutine (`workerPanic`, carrying the worker's own stack) rather than killing the process - the sequential walk propagated it up `Load`'s stack and pkg/check is public API, so a consumer that recovers around a load still can. pkg/check's real-data test SKIPS under -race (`raceEnabled`, `race_{on,off}_test.go`): the fixture suites cover every path of the parallel loader under the detector, the real tree adds data coverage rather than concurrency coverage, and it cost 13 minutes - past Go's 10-minute default. `LoadProfile` is Load over a root holding only a `pack.Profile`'s families (Load is it with ProfileAll, unchanged byte for byte): the family LOOP and the "not under any of the ... roots" message read the profile, and a CROSS-FAMILY rule whose other side the tree does not hold is SKIPPED, never failed - a community root's sidecars are keyed by core work slugs it cannot see, so `checkIntegrity`'s parent-work arm stands down (the one explicit gate; `checkSidecarPositionScale` needs none - with no works loaded its floor map is empty and the rule is vacuous by construction) and the release build over both checkouts asks instead. The redirect rules need no gate: a profile carrying no tombstone table leaves it out of `Listing.Aux`, so there is nothing to read and the file is reported as an unrecognized location like any other. Everything WITHIN a family still runs, which is what makes a community tree checkable standalone. `LoadComposed(coreDir, communityDir)` (compose.go) is where the skipped half DOES run: the load over the two roots the split produces, which is the only place holding the whole database at once. It reads coreDir as ProfileCore and communityDir as ProfileCommunity - not the caller's choice, since a compose of anything else is two databases rather than one - and after both loads runs four cross-tree rules over the pair - TWO of which are compose-only and two of which any ProfileAll load already runs: EXISTENCE (checkIntegrity's sidecar arm: a sidecar keyed by a work the core does not hold is a HARD error naming the pack entry, never a silently dropped contribution - the CC BY-SA layer is the most expensive data here and a quiet omission is worse than a stopped build), REDIRECT RESOLUTION (compose-only: a key naming a slug the core has RETIRED is re-keyed onto the survivor for this build; a core merge lands in one repository and the community re-key sweep in the other, so resolving here is what keeps the window between them from losing a sidecar - build-time only, nothing is written, the sweep is still the fix - and every ride is WARNED under its own advisory class `AdvisoryRetiredSidecarKey`, because a redirect ridden silently becomes a permanent dependency nobody can date; the census count IS the size of the open sweep), COLLISION (compose-only: two sidecars of the SAME KIND resolving onto one work is refused rather than folded - which entry describes the survivor is a human decision, internal/repair's sidecar-member-collision principle; DISJOINT members simply meet on one work, which is what a composed entry IS. The KEEPER is the INCUMBENT - an entry whose key was already the live slug beats every redirect rider, whatever the pack order, or the message tells the operator to fold away the entry that was correctly keyed all along), and the POSITION-SCALE ADVISORY (`checkSidecarPositionScale`, vacuous over a community root alone and real here, still a WARNING - a partial sidecar is a legitimate contribution and a release must not turn on it). EVERY RULE RUNS, RED OR NOT: a whole-database load has never let one malformed pack silence its dangling-sidecar reports, so neither does this - one run reports everything it can see. KEYS ARE REWRITTEN ONLY ON A GREEN PASS, so a failed compose hands back the catalogue as the trees really spell it rather than half-resolved. An EMPTY community root is itself a hard error naming the directory (pack.ListProfile tolerates a missing family root by design, so `--community` pointed at the community repo's top level instead of its data/ would otherwise compose clean and ship the artifact with the whole CC BY-SA layer gone); omitting the flag stays the way to build the core alone. Every community-side path is ATTRIBUTED (`communityMarker`, "community: ", applied once to the community load's problems and to its whole path index, so no rule below knows the marker exists) - two paths are spellable by either root and a composed list has to say which checkout a line belongs to; a single-root load's paths are untouched, byte for byte. EQUIVALENCE with a single tree is CONDITIONAL on every key being live: inside a tombstone window the pair is deliberately more permissive than one tree, which refuses a retired key outright. The path index the rules report against is `load`'s, handed back as a second return value rather than parked on Result - it maps every loaded record to its path, and retaining that on every load would cost hundreds of megabytes over the real tree to serve one consumer; the CORE index is dropped outright, since every cross-tree rule reports against a community path. The one hand-written enumeration of the sidecar member KINDS (`sidecarRefs`) sits beside the pathIndex maps it reads, under `TestSidecarRefsCoverEverySidecarKind`, which derives the kinds from the Catalog TYPE so a third member added to the model cannot silently escape the existence rule
pkg/pack            PUBLIC pack-file storage: family/cap definitions, bound math + binary-search lookup, pack parse/serialize (canonical via pkg/canonical, raw entries, duplicate keys rejected - CheckNoDuplicateKeys is exported because the rule is about any file the project rewrites, not about packs: pkg/redirects and pkg/check ask it for the tombstone table too), median split + directory split, and the read-through Store (queued-write-first reads; Flush performs due splits, directory splits and bound normalization, and PLANS EVERY FAMILY BEFORE IT WRITES A BYTE, so a refusal leaves the tree exactly as it found it rather than half-rewritten for a human to heal on top of. Two refusals live there: a pack holding an entry that belongs in a LATER pack (ErrMisplacedEntries - its range no longer describes what it holds, so splitting it would mint a bound the later pack already carries; the entries are invisible to every reader either way, so the writer stops and names the entry, and metafmt --write does the relocating, since only the healer's rules may decide which copy wins), and the two-plans-on-one-path backstop behind it, which names both packs). survey.go classifies every JSON file under a family root BEFORE bound math sees it, so a misfiled/misnamed/empty/too-deep file is relocatable content (Salvage), never an authoritative bound; Pending covers every structural invariant metacheck enforces (Salvage/Unreadable/Misplaced/Conflicts/Rebinds/Packs/Dirs) and Heal + Flush converges to a metacheck-green, entry-preserving tree in one pass (duplicate slugs: the correctly-placed copy wins, reported as a Conflict). The Store is layout-aware PER FAMILY (absent is writable; a family in the retired file-per-record layout fails with ErrLegacyLayout, which is the loud refusal every writer inherits - DetectLayout says pack as soon as ANY file under the root reads as one, so a stray file cannot flip a converted family back). Reading a tree costs ONE walk per run: Listing is that walk (file-to-family partition, per-family layout, per-family tree - Detect/ReadTree without re-walking) and Reader is the writer's parse cache, so a pack the store reads and rewrites is parsed once; a Store keeps both and exposes them (Store.Listing/Store.Reader), and gives both up at Flush - including a FAILED flush, which has at minimum composed the queue into the packs it holds. The listing is as-of-Open and is a reading optimization only: survey re-scans the family root, because what gets relocated, rewritten or deleted has to be decided from the files that are there now. A borrower validating the tree (check.LoadStore) reads the cache but never fills it - retaining every pack for the rest of a run costs hundreds of megabytes to save a handful of re-reads. Store.HealPending is Heal plus the Pending it computed, so metafmt surveys a family once instead of twice. It also owns the tree's FILE ACCOUNTING, which is why RedirectsFile lives here: a listing partitions every JSON file into a family's files, the one recognized non-pack file (data/redirects.json, the slug tombstone table - Listing.Aux, which is also how pkg/check reaches it) or the strays a caller reports, so metacheck's "every file is accounted for" rule stays total instead of gaining an exemption per caller - and nothing here reads or writes that file. profile.go is the TREE PROFILE (`Profile`: ProfileAll/ProfileCore/ProfileCommunity, the family set a root deliberately holds plus whether it carries redirects.json), built for the community-repo split: `Families()` stays the full table and a profile SELECTS from it, the zero value is ProfileAll so every existing caller is unchanged, and a Listing/Store carries the profile it was opened under (List/Open/Detect/OpenFor each gained a `...Profile` twin; check.LoadStore needs none, it takes the store's). It does exactly two things - a family outside the profile stops being a family ROOT, so its files are strays and metacheck's accounting rule reports every one of them (which is what makes a leftover works-community/ in core a red check, for free), and Store.def refuses every read or write addressed to one, so a misdirected write is loud rather than a silent drop into a directory nothing accounts for (`Store.Tree`/`Store.Layout` answer with a VALUE and cannot return that error, so they PANIC with def's own sentence through `mustHold` - a nil *Tree and a LayoutAbsent are both lies about a family whose packs are on disk, and asking a store for a family its root does not hold is a programming error where every neighbouring error describes a state of the DATA; it can never fire under ProfileAll). `Profile.Excluded()` is the SUBTRACTIVE twin of Families - the paths a profile disclaims - for the one pass that walks by path rather than by family (internal/format's canonical formatting). The one shared implementation every reader and writer builds on (see PACK-SPEC.md)
pkg/redirects       PUBLIC slug TOMBSTONE table (data/redirects.json): Load/Write/Add over model.Redirects. Add is the whole reason it is a package rather than a map assignment - it collapses chains in BOTH directions (a target since retired, and the tombstones already pointing at the source), so every entry names a live record directly and a resolver does one lookup; it refuses what it cannot decide (an unknown namespace, a non-slug, a reserved route literal, retargeting an existing tombstone, a target that collapses back onto the source) and is idempotent, so a repair pass can be re-run. It knows the FILE, not the database: whether a source is really retired and a target really exists are pkg/check's rules over the tree the writer produced. Its ONE caller is internal/repair, the duplicate-merge pass this mechanism was built ahead of: every slug a merge retires is tombstoned in the same change, and the chain collapse is what keeps the table one hop deep as waves land over each other. internal/remediate's GraphicAudio fold predates it and was not retrofitted (a one-off that has already run)
pkg/extract         PUBLIC epub split (container/OPF/spine/toc -> plain text) + the word-shingle overlap check
pkg/scan            PUBLIC local folder scanner: embedded tags + path/filename heuristics + ffprobe -> the "audiosilo-folder-scan" import doc (per-field provenance, omit-never-guess, tag-evidence collection split)
                    (pkg/* are consumed by the sibling audiosilo-sidecars module as ordinary deps, mirroring how audiosilo-server promoted pkg/launcher + pkg/match; pkg/scan still imports internal/importer for its pure normalization helpers, which is legal within-module and does not leak into pkg/scan's exported API)
internal/importer   OpenAudible books.json + Libation export + libex rows -> work/recording/person/series, ASIN-dedup, writes through the pack.Store under `Options.Profile` (the root's TREE PROFILE; zero value ProfileAll, so every existing caller is byte-identical, and it reaches both the store open and the post-write validation - a SCOPED root must never be validated as ProfileAll, which is the one reading under which a leftover family in the wrong repository looks clean. It never narrows what a run WRITES: writeFamilies is still the importer's own answer to that. The two standalone libex modes - libex-select's loadSeriesIndex and libex-fill's LoadCatalogForFill - take a bare dataDir from a CLI with no --profile flag, so there is no profile to thread there and they stay ProfileAll by the same default rule; adding that flag means threading it) (write.go: one works-family composite entry per work, so a recording edit is a read-modify-write of its work's entry; nothing reaches disk until flush; a legacy-layout tree is refused before anything is planned; a create at a slug the store already holds fails loudly rather than overwriting - the guard against a loader-dropped entry being clobbered). added_at is stamped ONLY on the branches that create a record, never on an ASIN merge or enrichment backfill. No chain ever CLAIMS a reserved slug (the API route literals): the work chain marks the reserved base probe-only, the series chain shifts by one (SeriesSlugAt), and person ids come from model.PersonSlug, which composes the reserved variant itself. (Shared pipeline over a typed sourceBook, with three disjoint planning modes over it: create, enrich.go's ASIN-matched backfill, recordings.go's alternate-narration pass; conflicts.go is the optional NDJSON conflict worklist - the durable twin of the contradiction warnings, written where the guard fires and nil unless --conflicts was passed; audiblegenres.json maps Audible genre names/browse-node ids onto the schema's genre enum, drift-guard + golden-anchor tested). Beside the pure helpers its in-module neighbours have long shared (the slug composers `BoundedSlugTail`/`NumberedSlugAt`/`AuthorSuffixedWorkSlug`/`SeriesSlugAt`, `ToSet`/`SameSet`, `YearOf`, the credit-name cleaning), it exports two RULES OF RECORD, each because a second copy would be a contract with two definitions: `MarkedNameKey` (the initials identity the importer merges spellings on, read by internal/audit) the ASIN-merge guard's own same-production pair `RuntimesCompatible`/`AbridgedConflict` (read by internal/repair, which asks the identical question of two recordings colliding on one key: the boundaries are within-10%-OF-THE-LARGER and absent-reads-as-unabridged, and a second spelling of either got both wrong), the SERIES-POSITION primitive `PositionSpan`/`PositionRange`/`SameSlot` (the NormalizeSequence-then-ParsePositionRange two-step, whose ORDER is load-bearing; it lives here beside the rule it composes over because internal/titlerule is a true leaf now, and it replaced the five spellings that two-step had grown across internal/audit and internal/repair), and the long-standing `NormalizeSequence` (position acceptance + canonical spelling, tolerant of the 104 real "1 - 3" range spellings the schema pattern rejects, read by internal/audit and internal/issueform). The edition-marker rule went the other way - it MOVED to internal/titlerule (`HasEditionMarker`/`StripEditionMarkers`, which `cleanWorkTitle` now strips through) when pkg/check and this package both became consumers of the normalized title identity built on it. **dupidentity.go is the CREATE path's duplicate-identity guard**: before a row mints a NEW work its normalized identity (`check.WorkIdentity`, TAKEN off the catalogue load's `Result.Identity` - the one index the intake gate and metacheck's census also read - and only for a create run) is probed against the catalogue AND against the works this run has already written; the key is derived ONCE per row and probed before the row's credits are resolved, so a row whose identity nothing holds costs two map lookups rather than a cleaning pass over its credit names, and a match the row's own slug candidates cannot reach is SKIPPED - counted as `Summary.SkippedDuplicateIdentity`, named in one aggregated warning, and written to the `--conflicts` worklist as a `work_identity` row carrying both titles. It skips rather than merging or routing to recordings.go on purpose: a refused row is re-importable and a wrong merge is not, and attaching a recording would assert "this IS that book" on exactly the evidence internal/audit measured five vetoes for. Five vetoes keep it off the shapes a title cannot judge - a row the serial pre-pass suffixed (its siblings collide by construction), a row whose series claim puts the matched work at a DIFFERENT position (`seriesClaim.compatible`, read from the series record), a row whose own TITLE states a volume the catalogue does not positively place the matched work at (`seriesClaim.places`: silence is a veto, not agreement, which is what stopped "Circus of the Dead, Book 2" being refused against the plain "Circus of the Dead" when no series, no membership or no claim could confirm it), a COLLECTION on exactly one side (inherited from `check.WorkIdentity.matches` - a boxed set is not the volume it collects), and a key naming SEVERAL works (which cannot say which one the row is, the bare-series-name serial shape). The intake gate applies the last three too, so an intake verdict can never contradict what a re-import would do. Nothing about enrich or recordings-only changes: neither takes the index
internal/issueform  issue-form parse + compose (add-work/recording/correction/characters/recaps/import) -> dedup -> pack entries through the pack.Store (both community sidecars share ONE works-community entry, so placing recaps preserves an existing characters member; family gating is per template; a legacy tree is needs-human, not the contributor's fault; a person-name correction that would change the record's SLUG is a rename - the record moves and every work crediting it has to be rewritten - so it is needs-human too, rather than being written and then reported as "invalid" by the person-slug metacheck rule; a title or series name that slugs to a RESERVED route literal steps onto the same candidate the bulk importer would mint - AuthorSuffixedWorkSlug / SeriesSlugAt / PersonSlug - and says so in the verdict, rather than composing a record metacheck would bounce), the ok/duplicate/needs-human/invalid verdict the intake workflow branches on; Result.Files names the pack files flush rewrote. **dupidentity.go adds the three gates the EXACT ones cannot answer** (the audit's 4,596 near-duplicate clusters walked past all of ASIN/ISBN, narrator set and work slug, because a decorated title compares exact to nothing): a SERIES-VOLUME gate first (a title stating a known series and a volume number that series already fills is that member - the most specific claim, so its verdict names the volume), then the DECORATION STRIP (`titlerule.StripDecoration` at compose time, which is also what lets the existing slug gate see the collision at all - "Mageling (Unabridged)" cleaned to "Mageling" collides where the decorated spelling did not; a strip whose residual names no book or reads as a fragment is needs-human naming the reason, and every other refusal leaves the title as submitted, since a title that IS its series' name is ordinary), then the NORMALIZED-IDENTITY gate (`check.WorkIdentity.Match`, the same index the bulk guard reads, with the language and author-NESTING rules so a catalogued work whose author list carries a role-credited translator still meets a form that names only the novelist). The two DUPLICATE gates route through `failDuplicateWork` - `failDuplicate`'s sibling for a work record - so the bulk-mirror tier discipline is the existing one at all five duplicate gates, while the strip is not a duplicate verdict at all (it rewrites the title or refuses on its own terms). All three read ONE derivation of the submitted title (`titleContext`: the series it is about, the record we hold for it, and whether it states a volume that record has no work at), so the series resolution happens once per submission. The bounds are the bulk guard's, restated in the intake bot's terms so the two doors cannot disagree: a title that STATES a volume is only a duplicate of a work the catalogue positively places at that volume (`placesMatch`, the mirror of `seriesClaim.places` - silence is a veto), a COLLECTION is not the volume it collects, and a key naming SEVERAL catalogued works decides nothing (the ambiguity veto). The STRIP reads the same rules before it rewrites anything (`sameBookAs`): a title whose cleaned form lands on a work this submission is not a second record of keeps its decoration instead, which is what stops "Hammered, Book 7" and "Hammered: The Complete Boxed Set" being handed the slug gate's duplicate verdict for books we do not hold. A series name the INDEX would not read a title against - one significant word, or a fold another series also spells - is not read against one here either (`WorkIdentity.Admits`), so what a submission composes never depends on how an unrelated series spells its name, and a one-word series' sequel ("Cars 2") keeps the number that is its whole identity. The work-slug gate itself stays author-BLIND as it always was, so a decorated title for a different book that cleans onto a taken slug reaches that verdict rather than being composed (the conservative failure - nothing is written). The import template inherits the bulk guard instead: a run whose only outcome was refused duplicates is needs-human, not `duplicate`. Corrections run off ONE op-typed allowlist (`correctableFields`, values are a `correctOp` carrying either a scalar coercion kind or an add func, so a listed field always has an op and can never be reachable with nothing to do - which would stamp provenance onto a record it left unchanged). Most fields are scalars the correction REPLACES; two on a recording are ADDITIVE - `isbn` and `publishers` contribute one entry to an array of independent facts rather than replacing the list - an ISBN nobody recorded is appended, an ISBN recorded BARE and corrected with a region is upgraded in place (a bare entry states no region, so scoping it only adds information), and a DIFFERENT stated region, or a different imprint for a region already listed, is needs-human because a stated fact is never rewritten mechanically. The add forms take the same two shapes (`region: ISBN` lines beside bare ones, `region: Publisher` lines) and refuse the schema's/pkg/check's own violations at compose time - a restated publisher of record, a repeated region, publishers[] with no publisher - so a submitter gets a verdict naming the fix instead of a raw metacheck line - and the same pair is guarded from the SCALAR side too (correcting `publisher` to a name publishers[] already lists for a region is needs-human, not a write that metacheck then bounces). Note the deliberate ASYMMETRY in the two identifier parsers: a bare ASIN defaults to `us` (an ASIN only exists inside a marketplace), a bare ISBN stays region-UNSTATED (that is a true state, and inventing one would be a fabricated fact). **It is PROFILE-AWARE** (`Options.Profile`, threaded to the pack store, to `check.LoadProfile`'s post-write validation and to which TEMPLATES are in scope), which is what lets the COMMUNITY repository run the same bot: a form is admitted iff every family `writeFamilies` says it writes is one the root holds, DERIVED rather than a second list of which forms are core and which are community, so ProfileCommunity admits the two sidecar forms alone (everything else is needs-human naming the core repository's issue chooser) and ProfileCore is its mirror image. The `import` template threads it one level FURTHER, into `importer.Options.Profile` (the store open and the post-write validation), because that is the one form that hands the tree to another writer and a scoped root validated as ProfileAll is the one reading under which a leftover family in the wrong repository looks clean. The WORK-EXISTENCE question is the other half: a sidecar's entry key IS a core work slug, and a community root cannot see the works family, so `worksdb.go` asks the newest data release artifact instead (`Options.WorksDB`, a read-only meta.sqlite - the same stand-in the community repo's own `scripts/key-check.sh` reads on the PR path) through the ONE `resolveWorkKey`, which is also where a root that HOLDS works answers - from the catalogue AND, where the works map has no answer, from that load's own `data/redirects.json`. The two branches are deliberately SYMMETRIC: a submitter pasting a work-page URL for a retired slug is 301'd by metaserve and never learns the slug changed, so the reference they type is the tombstoned one on either repository, and answering "not found" while holding the table that names the survivor would be the bot refusing to read its own data. Three verdicts, deliberately the key check's own with one divergence: live composes, unknown is REFUSED naming the slug (never compose a key that would dangle - the release build stops on one, and the CC BY-SA layer is the most expensive data here) as **needs-human**, since the artifact is stale in ONE direction BY DESIGN and "nothing holds this slug" may be the bot's gap rather than the submitter's mistake - the same verdict class a works-holding root gives for the same state, because one state should not change class with the repository it was sent to - and a RETIRED slug composes under the SURVIVOR with a note, because the bot is choosing the key rather than repairing a written one and writing the tombstoned spelling would open a pull request its own CI rejects. Resolving is not enough on its own: `retiredKeysFor` asks which slugs were retired ONTO the survivor and the sidecar path probes the member at every one of them, because the state a core merge actually leaves is the community entry still keyed by the OLD slug until the re-key sweep lands in the other repository - and a probe under the survivor alone sees nothing and composes a COMPETING member of the same kind for the same book, which the release build then refuses and a human has to unpick. Asked that way round it covers both directions (the form named the retired slug, or the live one). No artifact and no works family is needs-human, never a compose: the bot's inputs are wrong, not the submitter's. The artifact is opened through a URI-ENCODED `file:` DSN (`worksDBDSN`), not a splice - a `?` in an operator-supplied path otherwise ends the file name, silently dropping `mode=ro` and opening (and creating) a different file, which is pinned from the failing side
internal/format     metafmt's business logic: `CheckProfile`/`WriteProfile` take the root's `pack.Profile` (Check/Write are the ProfileAll twins) and BOTH passes are scoped to it, by two mechanisms - the STRUCTURAL pass is ADDITIVE (it surveys the profile's families and refuses a legacy layout among them) and the CANONICAL pass is SUBTRACTIVE (pkg/canonical is layout-agnostic, so it walks the whole root through `CheckTreeExcept`/`WriteTreeExcept` minus `Profile.Excluded()`: every out-of-profile family root plus redirects.json where the profile carries none). Subtractive because the two are not the same set - the difference is stray JSON at the data root, which belongs to no family and has always been formatted - and under ProfileAll nothing is excluded, so a whole-tree run is byte-identical to what it always was. The scoping is load-bearing rather than tidy: a whole-tree canonical pass under a subset profile left churn in the directory a repo split is about to extract byte-for-byte, and where the out-of-profile family was still LEGACY it rewrote those files in place while exiting 0, since refuseLegacy no longer reached them - the exact failure that refusal exists to prevent. A file outside the profile is neither touched nor judged here; what it IS stays metacheck's unrecognized location. Canonical formatting (pkg/canonical) sequenced with the self-healing placement pass (--check surveys with pack.Pending; --write uses pack.HealPending -> Flush, one survey for both the report and the fix); a tree holding a non-pack family is refused before the first write (tidying files no reader will load would make a stale checkout look maintained); Report embeds pack.Pending so every category surfaces in --check/--write; unreadable files are reported and left for a human while the rest still heals; formatting runs FIRST because Flush judges an untouched pack by its on-disk size
internal/testpack   test support (imported only by _test.go files): seeds and reads a pack-layout tree addressed by the old per-record path syntax (a fixture syntax it parses itself), resolved onto family + entry key; shared by the importer, issueform and metaimport suites. fixtures.go additionally RENDERS the minimal schema-valid records a fixture tree is seeded with (WorkJSON/RecJSON/PersonJSON/SeriesJSON/CharactersJSON/RecapsJSON plus their option funcs), shared by internal/audit's detector suite and internal/repair's writer suite - the two had each grown a copy and the copies had drifted on the two details that matter (which separator a "<work>@<position>" member is cut at, and whether a recording's globally-unique ASIN is derived from its id alone or from its work too, which handed two recordings of one slug the same identifier). A fixture that differs between the suite that PROPOSES a repair and the suite that APPLIES it is drift neither suite can catch
internal/rawentry   the RAW-MEMBER pack-entry editor (Obj: members kept as bytes, so a writer edits what it decided about and carries every other byte through - pkg/pack.WorkEntry's own instruction, because marshalling through the typed structs emits every zero-valued required field and stating a fact nobody gave us is the one thing this project does not do) plus the BY-VALUE union rules a merge folds two records with, each keyed exactly as pkg/check's matching uniqueness rule compares (ASIN by value, ISBN by value, a credit by (person, role), a source by its whole entry). internal/remediate/obj.go is its ANCESTOR and was not rewritten onto it - a spent one-off, so a rewrite would change nothing on disk - but its header now names this package as the successor, so the third consumer extends a leaf instead of forking a second copy
internal/reportdir  the shared plumbing of the two tools that write a REPORT DIRECTORY beside the data tree (internal/audit, internal/repair): the containment guard that refuses an -o inside the data root (with the SYMLINK resolution the comparison needs - a link outside the tree pointing into it defeats a lexical test), the buffered NDJSON writer both reports are made of, the thousands-separator renderer and the two-column table their markdown summaries read better for, and the one-line "first problem" of a failed load. None of it is a rule about data, which is why the two tools can share it without either reaching into the other
internal/remediate  metaremediate's business logic: the ONE-OFF repair that folds GraphicAudio's multi-part dramatized products back into one work per book and gives the series slots those parts hijacked back to the plain text editions. Cohort gate is the PUBLISHER, not the title marker (other publishers really do spell a series volume "Book 1 of 10"); it plans the whole change - merges, same-ASIN twin collapses, series repairs - before writing a byte, and a book it cannot resolve unambiguously is left exactly as it was and named in the report (author splits, a part with several recordings, a slug another work holds, an ambiguous plain twin, a series position two works would share). Reads the tree ONCE outside the store's parse cache, writes through pkg/pack, deterministic and idempotent. See scripts/README.md for the operator flow
internal/titlerule  the RULE PRIMITIVES the audit (and, next, the repair pass) judge titles, series names and person names by: what a retailer's decoration looks like (`Decorations` over a priority-ordered table), what a title reduces to once it is removed (`Clean`), the comparison keys (`CompareKey`, `FoldKey`, `SeriesKey`/`SeriesSagaKey`), the slug-shape predicates, `OneEditApart`, and the CANONICAL-CHOICE ladders (`WorkRank`/`SeriesRank`) - so a report and a repair can never name different survivors. A LEAF on purpose: these rules were born inside internal/audit, which imports pkg/check, so nothing pkg/check could reach was able to call them and every other consumer would have needed a copy. It is now a TRUE leaf - pkg/model and nothing else - which is what the intake-time duplicate prevention needed: `IdentityTitleKey` (the normalized work identity, `CompareKey` over `Clean`) is read by internal/issueform, internal/importer AND pkg/check, and the old arrangement (reaching internal/importer for the edition marker and the initials key) made those last two impossible to serve. So the edition-marker rule MOVED down here (edition.go records the move; internal/importer strips through `StripEditionMarkers`) and the `MarkedNameKey` re-export went away (internal/audit, its only caller, asks the importer directly). `IdentityTitleKey` returns NO IDENTITY for a title whose cleaned residual does not name a book (`CarriesIdentity`), which is the guard that makes it usable as a duplicate key at all: the packaging residuals ("the", "boxset", "omnibus") name no book, and a one-word series name used to be excised from its own titles, so "Cars 2" and "Hawk 2" both reduced to "2" - 199 purely numeric keys over 3,287 works in the tree (the boundary anchoring below is what retired that half of the hazard: a name mid-title is no longer removed at all). Beside it lives the key's SOUNDNESS CONDITION, `SameTitleUnderCommonSeries`: the key is one-sided (one title, one series name), so two records meeting on it is evidence only when the decoration each of them shed was the SAME decoration - read both titles against ONE name, either side's, and the keys must still agree. Two different books whose subjects were each removed as somebody else's series name meet on the template that is left ("Cold War: A History from Beginning to End" against "The Hundred Years War:" the same), which is how a live wave proposed 5 wrong merges non-advisory. It is read by the MERGE path only (internal/audit's `vetoStrippedSeriesDiffers`) and deliberately NOT by pkg/check's pairwise predicate: in the census direction the same rule costs 58 groups to gain 3, and about half of what it removes is a real duplicate under a series with two names, whose duplicate PREVENTION the two writer gates rest on - a refusal is recoverable, a deletion is not (the trade that withdrew the welded-function-words layer; the measurement is recorded on both sides).
                    **`Clean`'s SERIES REMOVAL IS BOUNDARY-ANCHORED**, through `stripSeriesAtBoundary` - the very rule the retitle proposal uses, not a second spelling of it: a whole leading segment, a whole trailing segment or a whole bracketed group comes off, and a name spelled mid-title stays. The excision it replaced was the copied `stripSeries` reached through `CleanTitle`, and it was the one mechanism behind the retitle proposal's measured 55% wrong rate; the KEY kept it after the proposal was fixed, which merged two different travel guides ("New Orleans: The Best of New Orleans for Short Stay Travel" and its New York twin both reduced to "The Best of for Short Stay Travel") non-advisory in a live repair wave. The anchoring is the WHOLE fix: the key deliberately does not read the retitle proposal's fragment test (`hasDanglingConnective`) in either arm, and both refusals are measured - its EDGE arms cost 8 correct merge proposals for no false one ("At the Mountains of Madness" is a poor title to WRITE and a perfectly discriminating key), and its INTERIOR arm (two function words abutting, the scar of a mid-title removal) was tried and withdrawn: it prevented zero wrong merges, cost one correct cluster, three correct retitles and one census group, and silently emptied the key of ~198 works whose titles merely abut two function words ("To Have and to Hold", "For Better or for Worse", "Murder in E Minor", eleven volumes of "Girls from da Hood") - which the intake gate and the importer's create guard read as "no identity to collide with", losing those works their duplicate PREVENTION. `hasDanglingConnective` does now tokenize through `identityWords` rather than a raw ASCII split, which had cut every accented word in half and read the half as a stranded article ("astronomía" -> "a"), refusing the Spanish catalogue wholesale. Measured over the 279k-work tree: W-DUP 4,596 -> 4,910 clusters, its non-advisory merge pairs 2,014 -> 2,112 (29 withdrawn, 127 found), metacheck's census 2,881 -> 3,160 groups, W-TITLE's non-advisory retitles 23,080 -> 23,133. One of the 127 found pairs is WRONG and is STILL a tracked follow-up ("Hellmervick" beside "Hellmervick, Book Two: The Black Forest"), though no longer for the reason it was filed under: the title states its volume in WORDS, `StatedVolume` READS that spelling now (below), and the measurement that landed the widening found the pair unmoved. Nothing contradicts anything - the plain twin states no volume and one-sided silence is never a disagreement (the "Hammered" calibration), while the catalogue places that twin at position 2 of the very series the title names, so `vetoStatedVolumeElsewhere` reads agreement too. What is left is a shape no volume vocabulary can see: the marker attaches to "Hellmervick", which reads as the SERIES, where "Hammered: The Iron Druid Chronicles, Book 3" attaches its own to the series reference - and separating those is a merge veto of its own, with its own measurement. It is the ONE word-form title left in the whole non-advisory merge set, and it stays out of repair waves through worklist narrowing until then. The one exception is a residual that IS its numbers and whose numbers name a book (a year, a date range - `numericIdentity` over the existing `numbersAreIdentity`), bounded to a residual with no WORDS in it, since "Level 1 Lessons 1-5" holds three numbers and named 36 Pimsleur courses alike. `StatedVolume` reads the three volume spellings `markerSeq` cannot - a DIVISION-class ordinal ("Season 2", "Level 3", which the key drops as packaging), a ROMAN numeral ("Volume II", which it drops as a marker) and a WORD number ("Book Two", which `wordVolumeMarker` matched all along while capturing no number, so the key lost it - inside a decorative group, "Wildwood (Book One)", or with the tail the series strip leaves dangling, "Hellmervick, Book Two: The Black Forest" - and nothing stated it back) - and `SameStatedVolume` compares the whole SEQUENCE of division markers, in EITHER number spelling, so "Level 1 Lessons 1-5" and "Level 1 Lessons 6-10" disagree where their first numbers agree; all three widenings are layered in rules.go/identity.go rather than in the copied `match.go`, so internal/audit's own volume measurement (taken with `BareSeq`) is untouched and no widening moved W-DUP's cluster count (the boundary anchoring above did: 4,596 -> 4,910). The word arm's vocabulary IS `wordVolumeMarker`'s own (one..twelve, now shared constants so the rule that STRIPS the shape and the rule that READS it back cannot drift): a title saying "Book Thirteen" keeps those words through `Clean` and discriminates on its own, so there is nothing there to read back. Measured over the 277,618-work tree it moved 7 clusters out of `title-author` into the advisory `volume-conflict` subclass ("Prepper's Crucible, Volume Two", "Cinders & Ashes Book Three", "The Infinity Dungeon (Book One)", "Legacy Wars: Part Three", "Long Road to Survival ... Book Two", "Other Seasons ... Volume One", "Twilantia ... Part One") and 9 groups out of metacheck's census (1,861 -> 1,852) - every one of the 7 ALREADY advisory, so the non-advisory merge set is unchanged at 78 proposals / 109 pairs with nothing entering it, and W-NOSERIES, W-TITLE and every other class byte-identical. `StripDecoration` is `ProposeTitle` with a typed REFUSAL code, so the intake gate branches on the reason - `residual-names-no-book` and `residual-is-a-fragment` are a maintainer's, the rest leave a submitted title alone - rather than on prose. `match.go` is a documented COPY of audiosilo-server's pkg/match (the seriesForms/seriesRefIn behaviour postdates that repo's v1.9.0 tag and a replace directive is not CI-resolvable); its header ENUMERATES all eight deltas from the source rather than claiming byte-identity, and every widening lives in rules.go so the copy stays re-diffable
internal/audit      metaaudit's business logic: the READ-ONLY, deterministic data-quality audit. Nine detector classes plus the LOADER carry-through, over one pkg/check load - near-duplicate works (W-DUP, keyed through `titlerule.IdentityTitleKey` - the same normalized identity pkg/check's census and both writers' duplicate guards key by, so a pair this class clusters is a pair they would refuse to re-create - plus `check.IdentityEqualWorks` (the project's own work-identity rule, so a fork whose author list gained a role-credited contributor still meets its twin) and confirmed pairwise, with the series-embedded-in-the-title derivation that finds a retailer's second record of one book; language splits use pkg/check's `languagesCompatible` semantics so an UNKNOWN language never separates a work from its twin, and a cluster whose titles CONTRADICT each other about a volume is filed as volume-conflict with no merge proposed - the contradiction is `titlerule.SameStatedVolume`'s, so the division-class ordinals, the roman numerals and a nested division SEQUENCE ("Level 1 Lessons 1-5" against "Level 1 Lessons 6-10") are all read by the one vocabulary, while W-NOSERIES's position proposals keep the narrower `BareSeq` they were measured with), decorated titles (W-TITLE, title-only - a slug is identity and is never proposed for change, and a proposal that would reduce a title to packaging is withheld rather than made), unmodeled series memberships (W-NOSERIES), per-series integrity (S-INTEGRITY, whose position grammar is `importer.NormalizeSequence` for acceptance and canonical spelling plus `model.ParsePositionRange` for the span - in that order, since the span reader is a ParseFloat and would otherwise mint slots for values the model rejects), duplicate and parenthetical-decorated series (SER-DUP / SER-PAREN, the latter proposing nothing because a parenthetical is often a deliberate second ordering; SER-DUP's merge is guarded by the three NAME-level vetoes in `seriesdup.go` - language, a collection name on one side, a parenthetical decoration - plus the four MEMBER-level ones in `vetoseries.go`, which came out of an EXHAUSTIVE review of every non-advisory merge-series proposal the class made (all 694, against the full membership/authorship/language of the 1,443 series involved): 586 were wrong and 39 undecidable, and 36 of the 106 a repair run would have applied cleanly would have folded two different franchises into one public series slug. The evidence the name-level rules could not reach lives in the MEMBERS. **AUTHOR AGREEMENT** (`vetoSeriesAuthorsDisjoint`) is dominant - 581 of the 694, and ALL 36 of the applied ones: a normalized name collision ("Absolution", "The Academy", "Renegades") is two romance or LitRPG franchises far more often than two spellings of one. Asked at both levels a fold makes a claim at - the SIDE (against the TARGET, not pairwise, or a third franchise rides along on the two that agree: `doctor` is two E L Todd spellings plus somebody else's "The Doctor") and the MEMBER (a work the fold MOVES that shares no author with the target, which is `kingkillerchronicle`, whose loser contributes the Dozois/Martin/Gaiman/Flynn anthology "Rogues" at 1.5) - and compared through `samePersonSpelling`, not by id, so a PEOPLE-family fork cannot manufacture a veto (`girlfromthestars` is disjoint only because Cheree Alsop's records forked into `cheree-alsop` and `cheree-lynn-alsop`; the rungs are P-DUP's own three keys plus the middle-name insertion P-DUP does not report). It refuses one CORRECT proposal, `jackryanjr`, and rightly: a ghostwritten franchise really is author-disjoint, and "a human should confirm" is what an advisory says. **MEMBER-LEVEL COLLECTION EVIDENCE** (`vetoSeriesCollectionMembers`, 17 of the 36): the name-level rule cannot see a plainly-named series whose members are all omnibuses, so the members are read two ways - every TITLE a collection (`titlerule.IsCollection`), or every POSITION a RANGE against single slots on the other side (a range IS the "covers those volumes" statement, which is what reaches the three Bayne omnibuses at 1-3/4-6/7-9 whose titles say nothing). Judged WHOLE, so a series that merely HOLDS an omnibus is untouched, and exempt for a loser whose memberships are already in the target at the same slots (nothing moves, so nothing can land in a volume's place - `mephistosmagiconline`). **ORDERING AGREEMENT** (`vetoSeriesOrderingDisagrees`, 39): a fold is only ever sound as retiring a spelling whose memberships the survivor already holds at the same slots, or adding ones it has room for; everything else - the same slot holding different works (`barsoom`), the same work at two slots (`parasolprotectorate`) - needs a human to choose an ordering. Slots compare through `importer.SameSlot`, the same rule internal/repair refuses on, so the two passes cannot disagree about what a conflict is; these were being refused at PLAN time rather than on the merits, so the audit was publishing them as mechanical work. **LANGUAGE ON THE MERITS** (`vetoSeriesLanguagesDisagree`, 9): the rule it replaced compared the sides' strict MAJORITY languages, and a tie has no majority, so a side contributed NOTHING to compare - which is how the French edition of Hyperion (en 2 / fr 2) stayed mechanical. A veto asks "does anything disagree", so it is now any two STATED member languages across two sides (an unknown still separates nothing). The one-work-loser shape the review measured separately (31 of the 36: a loser holding ONE work landing in a free slot, invisible to any position check) is deliberately NOT a veto of its own - author agreement already refuses every one of them, and 36 of the 69 CONFIRMED-correct proposals are one-work losers, so a veto on the shape would cost the class half its mechanical path to catch nothing new. Measured over the review: the non-advisory merge set goes 694 -> 66, holding 66 of the 69 confirmed-correct proposals and NONE of the 625 wrong-or-undecidable ones; the three it costs are `jackryanjr` (correctly advisory, above) and `crowbrothers`/`londoncoven`, where a box set at 1-4/1-3 folds into the series of the volumes it collects and which slot it should hold is a human call. The metarepair dry run over the same tree goes from 106 applied / 588 refused to 66 applied / 0 refused), possible duplicate people (P-DUP, advisory throughout), sidecar hazards (REF-SIDECAR, a spoiler-gated sidecar on one half of a duplicate pair), field hygiene (F-HYGIENE), and pkg/check's own problems/advisories carried through under the loader's own `AdvisoryClass` names (LOADER). Every finding carries a TYPED `Proposal` (op/target/others/field/from/to/**advisory**/reason) and the prose `action` is RENDERED from it in report.go, so the repair pass reads structure rather than parsing English and the two cannot drift. `Advisory` is the load-bearing flag: it marks a proposal a mechanical pass must NOT apply, and a proposal-soundness audit by stratified sampling is what set every class's flag. Only ~47% of records are non-advisory. The VETOES a merge must clear are all things a TITLE cannot answer, and each was measured as a wrong proposal: two members at different positions in one series (read from series_works, not from the titles - 520 clusters), entirely disjoint series memberships, a COLLECTION on one side only (multilingual, `titlerule.IsCollection`), a runtime ratio over 1.5x (the first draft argued this the wrong way, calling 409 vs 3,843 minutes 'two productions of one work'), a decorated target beside a clean-titled loser, and THREE found by MEASURING what a repair would actually retire rather than by reading proposals (`vetoruntime.go`, all advisory at SOURCE so internal/repair inherits them through its fresh-audit gate): a cluster whose members' CLEANED TITLES carry no identity and are not even the same string (`vetoUnaddressableTitle` - the comparison key folds away everything non-ASCII, so two different Russian novels by one author pair both reduce to the key "1"; the legitimate "1984"/"22-11-63"/"1177 B.C" members of that regime, whose cleaned titles are IDENTICAL, keep their merge), an UNEXPLAINED same-narrator runtime contradiction (`vetoUnexplainedRuntimeGap`: two members carrying recordings by the same narrator set whose runtimes fail `importer.RuntimesCompatible` with nothing to explain it - one narrator does not read one book at two lengths, a second narrator reading it is a second production. The ONE explanation the data can carry is an ABRIDGEMENT beside its unabridged twin, so a cluster whose sides DISAGREE about `abridged` keeps its merge - the Mageling shape, live on the tree: `to-carve-a-fae-heart` at 734 unabridged minutes merges with its 623-minute abridgement and the repair then re-keys the colliding recording. `model.Recording.Abridged` is a plain bool, so "stated false" and "unstated" are one value to any reader of the catalogue and the veto reads a DIFFERENCE as the explanation, which is what it can honestly say), and SLUG-ORDINAL evidence (`vetoSlugOrdinal`: members whose slugs differ only by a trailing volume ordinal, because the id preserves the number the title lost - `bravelands-book-1..6` is six Erin Hunter volumes titled the bare series name, in no series, with a 1.47x runtime spread that cleared the 1.5x rung. Narrow on purpose: both tails ordinal material, both carrying a number, the NUMBERS MUST DIFFER (`...-tarzan-series-3` against `...-tarzan-series-book-3` is one book whose ids spell "Book 3" two ways, and is exactly the duplicate a repair should fold) and a volume MARKER present, which keeps the importer's bare collision suffix `x` vs `x-2` out of reach; the table is the SLUG-segment vocabulary, deliberately its own because titlerule's is title syntax), and TWO MORE found by REVIEWING a live wave's own merges (`vetostrip.go`): a pair that met on a key each side reached by shedding a DIFFERENT series name (`vetoStrippedSeriesDiffers` over `titlerule.SameTitleUnderCommonSeries` - "Cold War: A History from Beginning to End" against "The Hundred Years War:" the same, "Ladybird Audio Adventures: Outer Space" against "... The Frozen World"; the key is one-sided, so a collision is evidence only when the decoration both sides shed was the same decoration, and the test is asked only of a pair that MET ON THE SAME KEY because a closed cluster's members need not have met each other), and a title stating a volume the CATALOGUE contradicts (`vetoStatedVolumeElsewhere`: the negative arm of the importer's own position veto asked of two catalogued works, which is what caught "Adaptation: ... Empty Bodies Series, Book 2" folding onto the "Empty Bodies" the tree places at position 1 - vetoPositionConflict cannot see it because the volume-stating side is modeled in nothing. Its POSITIVE arm, silence-is-a-veto, is measured and declined at 57 correct merges). Together they moved 59 clusters and 74 slugs out of the mechanical path - Bravelands, four series of the Alan Partridge podcast, three volumes of Ascendance of a Bookworm, two different Big Nate collections - which an independent merge-destruction review then re-verified as the complete set it had hand-classified as wrong. Clusters are then TRANSITIVELY CLOSED over shared works before anything is proposed, because the un-closed set contradicted itself - 27 overlapping pairs named different targets, 25 works were told to fold onto two, 9 were a target in one proposal and a loser in another - and a repair applying them in file order would produce a different catalogue than one applying them in reverse. `assertProposalsConsistent` pins that invariant over every fixture. W-DUP's `series-volume` subclass is ADVISORY WHOLESALE: a title's number is as often a part, an episode, a season or a collection index as a series position, measured at 34% wrong with no guard that fixes it. The retitle proposal is `titlerule.ProposeTitle`, which is still not the same function as the comparison key but now shares its series-strip mechanic: a series name is removed only at a title BOUNDARY (a whole leading segment, a whole trailing segment, a whole bracketed group), never excised mid-title - the excision turned "More Than Words" into "Words" and "The Screwtape Letters" into "The Letters", a 55% wrong rate that was all one mechanism, and while the KEY still carried it, it merged two different cities' travel guides (see internal/titlerule) - and a proposal is refused when the residual names no book, reads as a fragment, or would collide with a sibling in the same series (852 groups where a volume number is the only human-visible discriminator). Read-only BY CONSTRUCTION: it reaches the tree only through check.Load, never opens a pack.Store, and refuses an output directory inside the data root. Deterministic BY CONSTRUCTION: every list sorted, every group iterated in sorted key order (the one generic `groupBy`), no timestamps - two runs over one tree are byte-identical. Writes one NDJSON file per class plus SUMMARY.md; the per-work title derivation, the sidecar index, the per-person keys and the per-series keys are each computed once, and the series-name lookup is a two-word-bucketed prefix probe, which is what keeps a 280k-work run near half a minute. The rules themselves are internal/titlerule
internal/repair     metarepair's business logic: the WRITE half of the audit, and the most dangerous code here - a merge DELETES records from a public database. Four properties carry that weight. (1) THE WORKLIST IS A FILTER, THE LIVE TREE IS THE TRUTH: a run re-runs the detectors in process (audit.Analyze over one check.LoadStore, ~30s over 280k works) and applies only what the FRESH run proposes as non-advisory; a `-report` directory only narrows that set, and a record in it the fresh run no longer proposes the same way is refused and NAMED (`stale-proposal`, with both decisions rendered side by side). That is what makes the pass idempotent by construction - a second run over repaired data proposes nothing - and what makes an old reviewed report safe to re-run. (2) PLAN EVERYTHING BEFORE WRITING A BYTE, remediate's and migrate's discipline with the grain moved to the PROPOSAL: each is planned in a `txn` against the plan so far and either commits whole or is discarded whole (the tombstone additions are validated over a COPY of the table first, so a redirect refusal cannot leave the entries committed beside a tombstone that was never recorded); the plan carries its own series-membership index, so a later proposal sees what earlier ones did. (3) A MERGE LOSES NO RECORD AND NO SET-VALUED FACT, and every POSITIONAL fact it has to choose is chosen deliberately and REPORTED: loser recordings move - merged with a colliding sibling only on the importer's OWN same-production rules, CALLED not restated (`importer.RuntimesCompatible`: two known runtimes within 10% of the LARGER, an unknown compatible with anything; `importer.AbridgedConflict`: an ABSENT flag reads as unabridged, so an abridgement never folds into an unstated recording; `importer.SameSet` over the narrators) - else re-keyed through `importer.NumberedSlugAt` and moved intact; works-community sidecar members move; series memberships are re-pointed and same-slot duplicates collapsed, and every path that asks whether a slot is taken compares SLOTS rather than strings (`slotKey` over `importer.SameSlot`, so "3" and "03" are one place in the order - pkg/check compares the strings, so a merge that put two works there would have left a GREEN tree); identifiers, provenance, genres, credits, authors and xrefs are unioned on exactly the keys pkg/check enforces uniqueness with (the union rules and the raw-member editor are internal/rawentry; the ASIN union keys on the (REGION, ASIN) PAIR and the ISBN union on the value alone, mirroring pkg/check's two rules - a by-value ASIN fold collapsed `{us,B0X}` and `{uk,B0X}` and dropped a stated marketplace); a chapter LIST is chosen by LENGTH rather than by which side was the keeper, because both recordings describe the same production and a longer timeline is more of the same truth (a keeper with 3 chapters used to beat a mover with 42; measured over a 386-merge wave, 4 of 40 dropped lists were richer, about 7,198 chapter entries wave-wide); every value that could not survive - a scalar, a raw member, a chapter list, a second differing wikidata QID - is NAMED in the applied record's notes, which is the audit trail for a pass that deletes records; and every retired slug is tombstoned through pkg/redirects.Add. `added_at` is never touched on either record kind: it says when THIS record entered the database, and the loser's date travels in the unioned sources[]. (4) A RED TREE IS A FAILED RUN: after writing it heals and canonicalizes through internal/format (metafmt's rules, not a restatement of them) and loads the tree again through pkg/check, exiting non-zero with the problems named - and it refuses to write into a tree that does not validate, or one with uncommitted changes (git is the recovery story, as for metamigrate). All three of those - the store it opens, the format pass and the validation - read `Options.Profile` (zero value ProfileAll, so every existing caller is unchanged), because a pass that healed under one profile and was then judged under another would report a file it had just been told not to touch; `writeFamiliesIn` narrows the write list to the families the profile holds, which is not tidiness - pack.OpenForProfile REFUSES a named family the root disclaims, and under `core` works-community is one - and the WORKS-COMMUNITY VIEW is built over whichever root can answer for it (`sidecarSource`, plan.go), which is the half that was actually load-bearing. The merge planner asks every loser whether it carries a sidecar member UNCONDITIONALLY, so a refused READ killed every merge over the real core tree - and answering EMPTY instead, the first fix, was worse: it made the sidecar-collision refusal STRUCTURALLY BLIND, and a wave would have folded 25 of its first 30 clusters' CC BY-SA sidecars away, surfacing later as a release-blocking LoadComposed error nobody could attribute to it. So there are THREE modes and safety is never inferred: `sidecarInTree` (the root holds the family - the pre-split shape, read and written as before), `sidecarReadOnly` (`--community` names the community checkout's data/: the collision question is answered from there and the refusal works exactly as pre-split, but NOTHING is written to another repository - the members ride the slug tombstone to the survivor until the community re-key sweep lands them, which the applied record's notes say out loud), and `sidecarUnknown` (neither: every merge-works proposal is refused with `CatCommunityRequired`, naming the flag). The STAGING is identical in all three - a read-only run still stages the merged entry so a LATER proposal sees what an earlier one decided, which is the only thing that catches two clusters folding two characters-carrying works onto one target - and `view.queue` is the single place the modes part. `openCommunity` refuses at the door both a `--community` root the tree already holds the family for and one carrying no works-community packs (metabuild's own rule: pointing the flag at the community repo's TOP LEVEL rather than its data/ would answer "no sidecars anywhere" for every cluster, which is the original blindness wearing the flag meant to end it). `TestMergeWorksOverTheSplitPairMatchesTheWholeTree` pins the pair and the whole tree to the same decision and the same DATABASE (read through check.LoadComposed, so the tombstone and the compose-time re-key are what make it pass), and the real-tree measurement agrees exactly: 5 applied / 25 refused / 5 tombstones either way. REFUSAL TAXONOMY - the exported, NAMED type `Category` with `Categories()` as the list a triage script reads instead of writing its own, because REFUSED.ndjson is a published artifact, all reported and never applied: sidecar-member-collision (both halves carry characters, or both carry recaps - the CC BY-SA layer is the most expensive data in the repo and which entry describes the survivor is a human decision), series-position-conflict, record-retired, record-missing, stale-value, no-value-stated, malformed-proposal, recording-key-exhausted, redirect-refused, community-data-required (the one refusal about the RUN rather than the records), title-carries-no-identity (the ONE veto this pass adds to the audit's own, found by running it over the real tree: two different Russian novels whose cleaned titles both fold to the comparison key "1" - measured at exactly 2 of the 1,846 non-advisory merge-works proposals, with the legitimate "1984"/"22-11-63" members of that regime untouched). fill-field is wired but always refuses today: no detector states a VALUE for a missing field, and inventing one is not a mechanical pass's job. Dry run is the DEFAULT and its report (APPLIED/REFUSED NDJSON plus a PR-ready SUMMARY.md) is the review artifact; deterministic, no timestamps, so two runs over one tree are byte-identical
internal/migrate    metamigrate's business logic: its OWN parsing of the retired file-per-record layout as a storage location (issueform and testpack parse the same shape as a reference/fixture syntax - see pkg/model), the added_at git walk with release.yml's exact semantics extended to recordings, entry composition from RAW record bytes (only added_at spliced in), and greedy planning to ~50% of the caps (sized by pkg/pack's own renderer, never by re-spelled arithmetic). Renders every pack before deleting anything, and refuses what it cannot do safely: a shallow clone (which would date every record at the tip commit), an uncommitted in-place tree (git is the only recovery from an interrupted run), a tree holding no works, a non-JSON file, an already-converted family. Deterministic; the fixture-scale artifact equivalence proof lives in its tests
internal/build      SQLite builder (deterministic, FTS5 search_fts, asin/isbn indexes plus the per-(work,recording) and per-series covering indexes every serve request reads - guarded by `TestServeLookupsAreIndexed` in internal/serve, which EXPLAIN-QUERY-PLANs the very query CONSTANTS the request path uses and fails on a full table scan, so an edited query cannot drift away from its index; adding an index does NOT bump SchemaVersion, which versions what a reader may SELECT; added_at from the record, else the newest sources[].imported_at compared chronologically; the slug tombstone table as redirects(kind, old_slug, new_slug) at schema_version 5, rows in namespace-then-slug order and the primary key doubling as the lookup index). `sources.go` is the OTHER half: `Sources{Data, Community}` and the one choice `--community` exists to make - one root through `check.Load`, or two through `check.LoadComposed`. Nothing about the artifact changes either way (no SchemaVersion question, the same sorted-id inserts), and `TestComposedArtifactEqualsSingleTree` pins that for a pair whose keys are all LIVE: split a fixture tree's works-community into a second root, compose it back, and the SQLite file is byte-identical. The same proof was taken by hand over the 1.6GB seeded tree, which is what the cutover rests on. (Inside a tombstone window the compose is deliberately more permissive than one tree - see check.LoadComposed.) HOW the two roots meet is deliberately NOT here - the profiles, the cross-tree rules and the retired-key resolution are validation rules over a catalogue, so they live in pkg/check
internal/serve      the API server: snapshot loader, JSON handlers, FTS search, the ABS provider endpoint, GitHub-release poller/hot-swap, the server-rendered HTML ENTITY PAGES (html.go/shell.go/jsonld.go: /works|people|series/{id} injected into the built Astro shells) and the SITEMAPS over them (sitemap.go: /sitemap-index.xml + /sitemaps/<family>-<n>.xml, rendered from the snapshot)
schema/             JSON Schemas (the contract), embedded via schema.go; the four pack-*.schema.json wrappers are load-bearing (pkg/check validates every entry and entry key through its family's wrapper fragments); redirects.schema.json is the one whole-DOCUMENT schema (the tombstone table is a file, not an entry)
data/               the CC0 half of the database, in the PACK-SPEC.md range-packed layout: works/ (composites), people/, series/, plus the one non-pack file redirects.json (the slug tombstone table). The CC BY-SA works-community/ family lives in audiosilo-meta-community since the cutover, and a copy of it REAPPEARING here is a red metacheck (the `core` profile makes it an unrecognized location)
Dockerfile          image: site build + metaserve, NO baked data (the catalogue is fetched from the newest data release at boot; a UI change never rebuilds the database)
scripts/            operator scripts: the libex dump-to-rows SQL flow (+ README), and pack-union-merge.sh - the mechanical resolution for a pack-file conflict (a three-way merge of the entries, then metafmt --write), usable by hand on a conflicted file AND as the git merge driver .gitattributes routes data/**/*.json to, with packmerge_test.go driving both through real rebases including every case it must REFUSE (a non-pack file among them: handed data/redirects.json it used to empty the tombstone table and report success) and the add/add shape git's own line merge duplicates
.gitattributes      routes data/**/*.json to the packjson merge driver (scripts/pack-union-merge.sh); inert until a checkout configures the driver, which the intake sweep does; data/redirects.json is excluded (it is no pack, so the driver would refuse a conflict git's own line merge handles)
.github/            scripts/ai-verify.sh's PROMPT is core-only now: three families in the storage-layout paragraph, every record must be CC0-1.0, and any data/works-community/ path or CC-BY-SA-4.0 record in a diff here is ITSELF a finding (the sidecar length/spoiler/position rules went to the community repo's reviewer, which sees diffs that can actually contain one). Issue forms - the four CORE ones (add-work/add-recording/correct-data/import-library); add-characters.yml and add-recaps.yml moved to the community repo with the layer they contribute to, and ISSUE_TEMPLATE/config.yml carries a contact link pointing a sidecar contributor there. The COMPOSER for both stays in internal/issueform (the community repo runs this very cmd/metaissue under the community profile), so TemplateFromLabels still routes characters/recaps - and intake.yml passes `--profile core`, which is what makes a sidecar label that still arrives here (a maintainer applying data:recaps by hand, a bookmarked prefill, the sibling contributor tool) REFUSE-AND-REDIRECT through issueform.templateBelongsElsewhere, naming the community repo's issue chooser, instead of composing a works-community entry into a family this root does not hold - which would be a green-looking bot PR putting CC BY-SA content in the CC0 repo, permanently unmergeable since CI is core-profile too. Machine-parseable ids, data:* routing labels; check + release + image + intake + ai-verify workflows. check.yml and the intake bot pass --profile core; release.yml checks out audiosilo-meta-community beside this tree and composes the artifact with metabuild --community, accepts a `community-data` repository_dispatch as a third trigger, and names both SHAs in the release notes. intake.yml also rebases the bot's own open PRs on every push to main that touches data/ (the packjson merge driver + metafmt + metacheck, per-PR failures contained to a comment; a rebase the driver resolved is re-rendered and amended before the push); permissions are per-job and the rebase sweep has its own concurrency group; release.yml is a SHALLOW checkout now that added_at lives in the data
```

**The API server (`internal/serve`)** opens the SQLite artifact read-only and
serves JSON under `/api/v1` (stats, `search?q=`, `works/latest`, `works/{id}`,
recording `chapters`, `people/{id}`, `series/{id}`, `lookup?asin=|isbn=`) plus
`/healthz`; it can also serve a static site at `/`. Search comes in four
flavours: the combined `search?q=` plus the **type-scoped** `works/search`,
`people/search` and `series/search`, which are the same FTS query with an added
`kind = ?` predicate (a stored UNINDEXED column - no new index, no artifact
change, so they answer against every already-published release) returning the
same per-kind result shapes. The kind travels as DATA - `kindAny` is the
unscoped search - so ONE `snapshot.search(kind, q, limit)` and one
`Server.searchHandler(kind)` serve all four, `ftsHits` picks the SQL constant
and builds that constant's args together, and `snapshot.results` composes every
page, batching EVERY per-hit read (cards, work narrators, person names, series
name+count) over the ids the page holds - a scoped page is 100% one kind, so a
per-hit query there would be a whole page of sequential round-trips. The ABS
facade's work search goes through the same `ftsHits`, so a ranking change lands
on both surfaces (the two query-side boosts do NOT: they sit in
`snapshot.search`). Both apply to `works/search` too and
to that scope ONLY, since the ids they resolve are always works; a literal path
segment beats `{id}` in ServeMux's precedence, which is what lets the three sit
beside their families' detail routes as `works/latest` already does - and is
exactly why `search` and `latest` are RESERVED slugs (see the data-model
section): the one work that was stored at `search` had become unopenable, and was
renamed in the same change that added the rule. The whole
public surface is described by an **embedded OpenAPI 3.1 document**
(`internal/serve/openapi.json`, served at `GET /api/v1/openapi.json` OUTSIDE the
loaded-artifact gate - it is static, and a client discovering the API on a cold
boot must still get the contract; being embedded it is constant for the life of
the binary, so the route carries an `ETag` over those bytes and answers a
matching `If-None-Match` with 304).
`TestOpenAPICoversEveryRoute` diffs its path set against the route TABLE
buildMux registers from (`Server.routes`), so the guard observes the server
rather than a third hand-written list and a route added on one side only fails
the build. `TestOpenAPIProseStaysInTheRenderedSubset` additionally pins the
spec's prose to the one markdown construct the docs page renders (inline code
spans). The site's `/docs/api` page imports that very file at build time and
renders it in the site's own design system (`site/src/lib/openapi.ts` +
`site/src/pages/docs/api.astro`) - no vendored spec viewer, and no second copy of
the API's shape to keep in step. Membership lists are resolved
in a fixed number of queries (`cardsByID` batches works + authors + series +
covers over an id set) rather than four per work, and the two unbounded lists are
windowed: `people/{id}` pages by default (100, max 500 - a corporate credit like
"Full Cast" narrates thousands of works) while `series/{id}` returns the whole
series unless `?limit` is given, because audiosilo-server composes the player's
series rail from the full list. Both report unpaged totals, so the window is
additive to every existing consumer. It additionally exposes an
**Audiobookshelf custom metadata provider** at `GET /abs/search` (`abs.go`,
transport-only handler over testable `*snapshot` methods): ABS sends
`?mediaType=book&query=&author=&isbn=` (never an ASIN), and we return
`{"matches":[...]}`, one `absBook` per recording (ISBN exact-lookup first, else an
FTS work search with loose author boosting), capped at 10; duration in minutes;
genres from the work's normalized vocabulary when present, tags never (there is
no tag concept in the data model). It never writes: all data is
public so there is no auth, and responses carry permissive CORS. The current
artifact lives behind an atomic pointer (`snapshot`); with `--poll` a background
loop fetches the newest DATA release conditionally (`If-None-Match`/304) - the
non-draft, non-prerelease release carrying `meta.sqlite.gz` with the maximum
`published_at`, so code/image `v*` releases are skipped (`latestDataRelease`
scans the release list and selects by max `published_at`, since the list order
is not publish-chronological; GitHub's "latest" can be a code release with no
data assets). On a
new release the poller first tries a `--patch-from` binary delta against the
currently-loaded artifact (`tryPatch` -> `applyPatchFile`: zstd raw-dict id 0,
the CLI's patch-from convention; `--long=31` window; the patched file verified
byte-for-byte against `meta.sqlite.sha256` before it is installed) and falls back
unconditionally to a full `meta.sqlite.gz` download (`fullRefresh`, verified
against `meta.sqlite.gz.sha256`) whenever a patch is unavailable or fails - the
first refresh after boot is always full. **Every artifact-sized asset streams**,
never buffered: `fullRefresh` hashes the response body while decompressing it
straight into the artifact's temp file in ONE pass (`gunzipStreamTo`), so no
`.gz` is ever staged on the cache volume, and the patch asset streams to disk
(`downloadTo`). Both land through `installStream`, which runs the verification
gate AFTER the copy and BEFORE the rename, so unverified bytes never become a
file. Only the ~80-byte checksum assets are read into memory (`downloadSmall`,
size-capped); the one thing that cannot stream is the patch BASE, which a raw
zstd dictionary requires as a contiguous slice - and the decoder's window over it
costs about as much again, so peak transient is ~2x the base and `tryPatch`
refuses outright above `defaultMaxPatchBase` (1 GiB), taking the streamed full
download instead. Either way it hot-swaps the pointer; in-flight requests finish
on the old handle (closed after a grace delay), a rejected patch never swaps, and
a poll failure only logs and retries, never crashes the process. Before either
download it checks the CACHE: if the volume already holds a file for the newest
tag it is hashed against that release's published `meta.sqlite.sha256` and
adopted as is (`cachedRefresh`), so a restarted container does not re-download
the artifact it already has - verified, never trusted by name. Superseded cache
files are **pruned on every successful adopt** and again once each swap grace
elapses (`pruneCacheLocked`, under the refresh lock, never touching the live
artifact, a `--db` path, or ANY retired artifact - `swap` refcounts the file of
every snapshot that has been swapped out but not yet closed, because several can
be draining at once and SQLite reopens pooled connections lazily, so unlinking
one still in grace breaks its in-flight queries). Pruning at
adopt time is what cleans up after a cold boot, whose first adopt supersedes
nothing. Request deadlines are split by KIND rather than shared: the release
metadata gets a whole-request timeout (30s - a slow small JSON is a broken one)
while an asset download gets a generous ceiling plus a no-progress watchdog
(`stallGuard`, 60s), because one number cannot bound both an 80-byte checksum and
a hundreds-of-MB artifact without either aborting healthy slow downloads or
tolerating dead ones for just as long. The poll loop runs one refresh **immediately at
startup**, before the first `--interval` tick, which matters for any boot that
loaded a local `--db` artifact (`New()` skips the poll-only synchronous refresh
in that case); on a poll-only boot `New()` already refreshed, so the startup poll
is a cheap conditional 304.

**The production image ships no data** (see Dockerfile): the boot is poll-only,
so `New()`'s synchronous first refresh IS the load. A first fetch that fails is
not fatal - `New` logs it and falls back to the newest artifact on the CACHE
VOLUME (`adoptStaleCache`), the one the previous container was serving, flagged
as stale in the log and deliberately NOT recorded as `loaded`, so the first
reachable poll confirms it against the release (one checksum request, no
download). Only with nothing cached does it return a server with no snapshot,
which serves the
static site, answers `/healthz` with 503 `{"status":"starting"}` and every API
route with 503 (`requireSnapshot`), and retries until a release lands - first
after `bootRetry` (30s), then doubling up to `--interval` (`nextBootBackoff`),
because a failed fetch of a large artifact is not free and an outage can last
hours. Those 503s carry a `Retry-After` reporting the wait the loop is ACTUALLY
on (`nextRetry`), not the initial 30s it has long since backed off from. The
backoff resets as soon as an artifact loads. A crash loop would be the
worse failure mode; this one is visible in the logs and in the readiness check.
The image build context excludes `data/` (`.dockerignore`), so image build time
is genuinely independent of catalogue size rather than only nominally so.
Production can additionally set `METASERVE_WEBHOOK_SECRET` (at least 32 bytes)
while keeping `--poll` enabled. That registers
`POST /hooks/github/release`, which accepts only HMAC-SHA256-signed release
notifications for the configured repository. `release.yml` sends the signed
notification after `gh release create` has finished uploading every asset, so
the receiver cannot race an incomplete release. The request body is only a
trigger: the server re-queries GitHub and goes through `refresh`, including the
release-asset checksum and atomic-swap checks. The hourly loop remains the
fallback for missed notifications. The workflow reads
`METASERVE_WEBHOOK_URL` and `METASERVE_WEBHOOK_SECRET` from Actions secrets; if
either is absent or delivery fails, the data release still succeeds and polling
catches it later.
FTS queries are built defensively through one pair, `ftsMatch`/`tokenPhrases`
(over the shared `ftsTerms` rule), that every search surface and both boosts
reach: **punctuation is a word boundary** - a term is a maximal run of letters,
numbers and combining marks, approximating unicode61's own class - and each term
becomes its OWN quoted one-word phrase, the last one prefix-starred by
`ftsQuery`; no term at all yields the harmless empty-phrase match, never a syntax
error, and no user input can break the MATCH. Splitting is load-bearing: several
words inside ONE phrase are an FTS5 ADJACENCY constraint, so the whitespace-only
predecessor made punctuation written INSTEAD of a space
("Greg.Bear-Halo.Primordium", a filename-derived query) demand those words sit
consecutively in one column - unsatisfiable across `title`/`names` - and return
ZERO rows for a work the catalogue holds. **The one exception is an initialism**:
a token whose terms are ALL single runes stays ONE adjacent phrase, so `Q&A` and
`M*A*S*H` keep their selectivity instead of becoming a conjunction of the
commonest tokens in the index (measured on the 279k artifact: splitting emptied
the page for 12 of the 21 all-initials series, lost the right book off
`/abs/search`'s ten matches, and ran 30-45% slower for the class). Neither form
widens the walk - n one-word phrases read the same n posting lists a phrase of n
words does - and the same terms feed `nameKey`, so retrieval and equality judge
one vocabulary. The cost gates (`worthProbing`/`worthTitleProbing`) measure their
two-rune minimum on the WHITESPACE token for the same reason, refusing a token
whose terms are all stopwords ("the-a") rather than every initialism.
A `search?q=` that names a series and a number ("jack reacher 2", "jack reacher
02", "jack reacher book 2"/"band 2") additionally resolves that volume and
returns it FIRST, ahead of the FTS hits (`seriespos.go`): the trailing token is
read as a position, the rest (the "residual") resolves through the same FTS
index restricted to series rows, and the stated number is compared against
`series_works.position` through `parsePositionRange` - the package's one copy of
the position grammar, so `02` finds `2`, `2.50` finds `2.5`, and `2`, `2.5`,
`1-3.5` stay three different volumes. A residual that names a series OUTRIGHT
drops the partial matches (`preferWholeName`), so "jack reacher 2" is volume 2
of *Jack Reacher* and not also of *The Hunt for Jack Reacher*, and the whole
residual is tried before a trailing volume word is stripped, so "the jungle
book 2" is volume 2 of *The Jungle Book*. It is deliberately QUERY-side rather
than extra text in the artifact's FTS row: bm25 cannot be steered to rank the
volume first, the FTS tokenizer splits `"2.5"` into `2` and `5` (an indexed
position would blur the novella into volume 2), and nothing about the artifact
changes - no new table, no `SchemaVersion` question, nothing to version-gate,
and the feature works against every already-published release. It fires only
when the query ends in a number AND a series resolves AND that series holds a
work at that position, so `1984`, `Fahrenheit 451` and every ordinary title
search return exactly the page they returned before; the boost is additive and
`/abs/search` is deliberately untouched. Because the endpoint is
unauthenticated, CORS-open and hit per keystroke, the probe is bounded on both
sides: it matches the residual as WRITTEN (`ftsPhrase`, no prefix-star - a
half-typed name simply gets no boost) and skips a residual made only of
articles, one-letter tokens or bare volume words (`worthProbing`), stopwords
being left out of the MATCH entirely. Without those two bounds a "the 1"
keystroke walked a large fraction of the index (measured 110ms-237ms) and
prepended three arbitrary volumes; with them every such query is byte-identical
to the plain search page and within noise of its latency. A probe that errors
degrades to "no boost" and is logged, never a 500.
Its sibling is the **exact-title boost** (`exacttitle.go`, issue #1838): a
`search?q=` that IS a work's title - compared whole through the same `nameKey`
normalization, so case, spacing and punctuation are not identity - returns that
work FIRST, every work sharing the title, in id order. bm25 scores a ROW and a
work's FTS row carries its authors, narrators and series names beside the title,
so a short common title on a heavily credited work ("Spare") ranked below "Spare
Us!", "Spare Parts" and "Spare Room" and never reached the first page; no column
weighting fixes that, because the length normalization divides by the row's total
token count. It is query-side for the same reasons as its sibling - a
relevance score cannot express an equality, and nothing about the artifact
changes: one FTS query, COLUMN-FILTERED to the title over a PARENTHESIZED phrase
list (an FTS5 column filter otherwise binds to the first phrase only, which
matched "spare" in a title and "harry" anywhere), ordered by title LENGTH so the
exact matches lead the window, then confirmed in Go against `works.title` (the
indexed column is title + subtitle). Unlike the series probe it is reached by
EVERY query, so the cost gate is what bounds it: a query made only of articles
and one-letter tokens is never probed (`worthTitleProbing`, sharing
`probeStopwords` and the shared `hasIdentifyingToken` rule but deliberately NOT
the volume words - a work really can be titled "Book"). Measured on the 279k-work
artifact, "the" cost 426ms against the plain page's 196ms and can boost nothing;
everything the guard admits costs at or below the page it enriches (worst case
"book", 21ms against 24ms; "spare" 0.2ms). Both boosts apply to the combined
`search?q=` and to `works/search` only, an exact title leading a resolved volume
(an equality before an inference), and `mergeHits` collapses them when they name
the same work.
**A RETIRED slug answers 301**: every route that takes a family id -
`works/{id}`, its recording `chapters` route, `people/{id}`, `series/{id}` -
consults the artifact's redirect table where it would otherwise 404 and, on a
hit, sends `Location` at the same route under the surviving slug plus a
`{"redirect": "<new-slug>"}` body, so a client that does not follow redirects can
heal the id it stored rather than only learn it is gone, and a bounded
`Cache-Control: public, max-age=3600`, because a tombstone is NOT permanent the
way 301 invites a client to assume - reversing a bad merge has to withdraw it. It
is ONE helper (`redirected` + `snapshot.redirectTarget`) composed from ROUTE DATA
rather than four per-route closures: the namespace comes from
`redirectNamespaces` keyed by the `http.Request.Pattern` that matched, and the
`Location` is that same pattern with its wildcards filled in - the new slug for
the record wildcard, the request's own values for the rest (so the `chapters`
route needs no special case) - plus the request's query string, because a
`?limit`/`?offset` window describes the request rather than the id. Which
wildcard is the record's is DERIVED (`idWildcardOf`: the first single-segment
wildcard), so nothing depends on it being spelled `{id}`, and the `Location` is
built as the wire string with each segment escaped exactly once - handing it to
`url.URL` escapes a second time (`%` becomes `%25`) and setting `Path` alone does
not escape at all. `TestEveryIDRouteResolvesRetiredSlugs` diffs that table
against `Server.routes`, exactly as the OpenAPI guard does, with the candidates
derived from each pattern's SHAPE rather than from that spelling
(`redirectCoverageGaps`; a route may also be listed in `redirectExemptRoutes`,
empty today, to say out loud that its wildcard is not a record), so a fifth
family route cannot ship without redirect support whatever it calls its id. A row
pointing a slug at ITSELF is ignored rather than served, so the resolver cannot
loop even on an artifact `pkg/check` never saw. Cost is bounded at LOAD: the version gate
(schema_version >= 5) and "does the table hold anything at all" are both asked
once, in `loadStats` (`snapshot.hasRedirects`), so an empty table - every release
until the first merge lands - costs no query at all, and a hit costs one
primary-key point lookup. A lookup error degrades to "no redirect", not a 500.
The `chapters` route is the one that consults it on a 200 path: an unknown pair
is an empty list there, and only a slug the catalogue does not hold can be a
redirect source, so a live recording with no chapters still gets its empty list.
The site's fetches and audiosilo-server's `/meta` seam are following GETs, so the
301 is transparent to both.
**It also serves real HTML ENTITY PAGES** (`html.go`, `shell.go`, `jsonld.go` -
the SEO campaign's Phase A): `GET /works|people|series/{id}`, plus the legacy
`GET /work|/person|/series` which 301 a `?id=` to the path route (no snapshot
read, so a retired slug simply takes a second hop) and serve the static shell
without one. They live in a SECOND route table, `htmlRoutes()`, registered
between the API table and the "/" fallback and only when `--site` is set, so
`openapi.json` keeps describing the API alone and
`TestHTMLRoutesAreDisjointFromTheAPI` pins the separation. metaserve owns no
parallel page design: it loads each built shell ONCE at construction, splits it
on the markers the Astro build emits (`<!--ssr:head-->` / `<!--/ssr:head-->`
around the replaceable head, `<!--ssr:entity-->` where the fact sheet goes), and
per request concatenates the literals around a composed head (title, meta
description, canonical, og/twitter, JSON-LD) and a composed body (`#ssr-entity`,
a plain html/template fact sheet the island removes on mount, plus
`#entity-data` holding EXACTLY the bytes the matching API route returns, so
hydration costs no second fetch). Every absence DEGRADES: no artifact loaded, an
unmarked shell or a read that failed all serve the shell as built, and a missing
shell falls through to the static handler - never a 503 or a 500, which a
crawler would keep. The handlers call the SAME snapshot methods the JSON ones do
(`workDetail`, `person(id,100,0)`, `series(id,0,0)`) and add ZERO SQL, so
`TestServeLookupsAreIndexed` covers them by construction; rendering is
deterministic (golden files in `internal/serve/testdata/golden`), the pages
carry `ETag` + `Cache-Control: public, max-age=300`, and a retired slug 301s
here too - `redirectNamespaces` folds in the page patterns from the one
`htmlEntityRoutes` table and `redirected` picks the HTML writer for them, so the
guard covers both tables and a page cannot ship without redirect support. The
public origin is `--site-url` (default `https://meta.audiosilo.app`), because a
canonical link cannot be relative and the server cannot discover its own origin.
The new URL shape is a contributor-facing one too: `internal/issueform`'s
`resolveWorkRef` accepts `/works/{slug}` (strictly one segment, slug-shaped)
beside the legacy `?id=` URL, the data-tree path and a bare slug - and the two
guide-page spellings below.
**Two COMMUNITY GUIDE PAGES hang off the work page** (`guides.go` - the SEO
campaign's Phase F, targeting "<title> recap" / "<title> characters" queries the
sidecar layer could never rank for while it rode only in the island's
client-side accordions): `GET /works/{id}/recap` and `GET /works/{id}/characters`,
two more rows in `htmlEntityRoutes` with an EMPTY legacy route (they replaced no
`?id=` URL, and `htmlRoutes` skips registering an empty legacy), so shell
injection, the ETag, the retired-slug 301 and both coverage guards all apply
unchanged. The trailing literals FOLLOW the wildcard, so the reserved-slug set
does not grow. A page EXISTS iff the work does and carries the member
(`hasRecapGuide` counts a whole-book summary; `hasCharacterGuide`); everything
else takes the compose-nil path, so a stale URL 404s rather than indexing an
empty page. The BODY costs no new SQL - both compose over `snapshot.workDetail`
and embed its marshal as the payload, so the island
(`site/.../work-guide.tsx`, hydrating the same panels the work page renders)
needs no second fetch - and the one query they add is the PRESENCE PROBE that
runs FIRST (`hasRecapPage`/`hasCharacterPage`, one indexed `EXISTS` point query
per page, version-gated exactly as the sitemap families are and pinned
positively by `TestGuidePresenceProbesAreIndexed`): almost every work carries no
sidecar, so almost every request to these routes is a 404 that would otherwise
have paid workDetail's 6+4N-query cascade to learn there was nothing to render.
`hasRecapGuide`/`hasCharacterGuide` are still asked of the composed document, so
the probe stays an optimization of the definition the links and the sitemap are
built from rather than a second copy of it. The spoiler posture is
structural: titles/H1s lead with the query phrase, meta descriptions and JSON-LD
(`Article` with `about` = the work's Book node, `license` CC BY-SA) are composed
from FACTS only, and every recap/description body sits inside a closed
`<details>` under `<div data-nosnippet>` - indexable, never quoted into a
snippet, never shown unopened, functional with no JS. The pages carry the CC
BY-SA notice (`rel="license"`) and the `/build` contribute deep link; the work
fact sheet links them from a "Guides" section (the one place the work page says
"recap"/"characters" - the guide pages own the TEXT, so crawl weight is never
split), and `pageIdentity` mixes in the shell's NAME so a work and its guides
can never share a validator.
**Those pages are DISCOVERABLE through the SITEMAPS** (`sitemap.go`, Phase B):
`GET /sitemap-index.xml` - which shadows the Astro build's static file of that
name, the one robots.txt already points at - plus `GET /sitemaps/{file}` for the
entity shards. The index lists `/sitemap-0.xml` (the dist's own static-pages
sitemap, only when the dist carries it), then recaps, characters, series, people
and works shards in that order: the crawl-budget decision, few high-value files
first - the two GUIDE families lead (one shard each, the pages "<title> recap"
queries land on). A shard is
50,000 URLs (the protocol cap) of `<siteURL>/works/<slug>`-style locs in id
order (a `sitemapFamily` carries an optional SUFFIX, so the guide families list
`/works/<slug>/recap`-style pages over the same id space), with `<lastmod>` from
the record's own `added_at` where the artifact
carries one (works only; the other families have no such column, and nothing was
added to the artifact for this - zero SchemaVersion movement) and NO
changefreq/priority. The shard COUNT is arithmetic over per-load memos
(`snapshot.stats`, plus the guide-page counts settled in `loadStats` behind the
sidecar version gate - an older artifact honestly advertises no guide shards,
and the recap family's count and shard SQL come from ONE pure derivation over
the artifact's schema_version, `recapSitemapSQL`, since its URL set spans
`recaps` UNION `recap_summaries` only from schema_version 3 - so the count and
the listing are structurally unable to be computed against different tables), so
no request runs a COUNT; the shard queries walk their
family's primary key or sidecar work_id index and are guarded by
`TestServeLookupsAreIndexed` like every other query constant (the recap
compound's COUNT form must wrap a subquery, so it is pinned by its own positive
EXPLAIN test instead). A third route table (`sitemapRoutes()`) registered
unconditionally - a sitemap needs no shell, so an API-only deployment serves one
too - with gzip, no CORS, `Cache-Control: public, max-age=3600` and an ETag over
the artifact's identity plus the origin, both landing on the 304 and the composed
200 ONLY (a query or render failure in between - a hot swap closing the db
mid-request - would otherwise ship an error body under an hour of public caching
and the document's own validator, which a shared cache then 304-renews for the
life of the release). The dist's identity is deliberately absent from a SHARD (no
byte of the shell reaches one) and the index's validator carries exactly the one
bit of it that reaches a document: whether the dist has a static sitemap to list,
so a UI-only redeploy cannot 304-renew a wrong index. The file name is matched
STRICTLY (`<family>-<n>.xml` over the known family names, no leading zeros, so
one shard has one URL), and a bad name, an out-of-range shard or an empty family is a 404 that
costs no query. With no artifact the index degrades to the dist's static file -
`no-store`, because that stand-in describes the boot window and not the site -
and the shards answer the API's 503. Rendering is `encoding/xml` and
deterministic - two renders of one snapshot are byte-identical. Business
logic stays in `internal/serve`; `cmd/metaserve` is flag wiring only.

The importer maps one export entry to a work + recording (+ people + series),
importing **factual fields only** (LICENSING.md): it drops publisher copy, raw
retailer genre strings, ratings and personal state, deduplicates by ASIN against the catalogue,
and writes canonical files, then runs `pkg/check`. Identity rules: a
**person slug is the identity** (spelling/diacritic variants of one name merge
into the existing record; no numbered duplicates) - with two rules closing the
gaps that identity had, both measured over the full 1.13M-book dump
(`initials.go`, `studiotail.go`):

- **initials spellings are one person** (Class B). `Slugify` turns both the dot
  and the space into hyphens, so "A.B. Kovacs" (`a-b-kovacs`) and "AB Kovacs"
  (`ab-kovacs`) forked. Two spellings meet only when they share a **marked key**
  (a maximal single-letter run, a dotted cluster and an ALL-CAPS 2-4 letter
  cluster are one initials group `I:ab`; a mixed-case short token is a word
  `W:em`), and a cluster of EITHER spelling needs case evidence - so
  "E. M. Brown" stays off "Em Brown" and the dotted honorifics ("Mr.", "Dr.",
  "St.", "Jr.", "Ms.") key as words rather than as the initials they are spelled
  like. WHICH spelling survives is decided in a batch pre-pass
  (`initialsCensus`, `decideInitialsOf`): the catalogue's spelling if it already
  holds one, else the batch's majority, ties by slug order - so no id depends on
  row order or on where a `split -l` cut the input. `getOrCreatePerson` mints the
  slug exactly as before, consults the decision only when it would CREATE a
  record, and creates it under the DECIDED spelling; the variant slug is never
  written into `p.people` (it would be a dangling id), so every path that
  RESOLVES a credit rather than creating one goes through `personSlugTarget` -
  without which a merge silently dropped the role credit it had just resolved.
  No existing id changes and no migration: a slug that already names a record is
  always returned as-is.
- **studio concatenations are split off a name** (Class A) - a fourth step of the
  credit-cleaning fixpoint, in three tiers by how much evidence the string
  carries. The two tiers that need no census are the ones the public door
  (`CleanCreditName`/`SplitNames`) applies to unmeasured populations, so both are
  bounded to the shape the dump measured: a closed MULTI-WORD role-label tail
  over a **2+ token** head, and `<person, 2+ tokens> for <house, 2+ tokens ending
  in production(s)/studio(s)>` with no connector inside the head. Every other
  form (of/at connectors, a whitespace-delimited dash - EVERY dash form needs the
  whitespace, an en dash inside a surname is not a boundary - parens/brackets/
  slash/pipe, and the bare concatenation over a 2+ token tail) requires the
  person half to have been **independently seen as a credit**, and the bare form
  requires the removed tail to have been seen too. That evidence is the **credit
  census** (`creditCensusOf`): the catalogue's person slugs plus a census of the
  batch's credit names, snapshotted before planning so nothing depends on row
  order. It is deliberately NOT called "attested" - the trust-tier vocabulary
  (attest.go, LICENSING.md) owns that word for a different question. Single-word
  labels (vox/voz/voce/sprecher) are never in the vocabulary and the legal
  suffixes (llc/ltd/inc/gmbh) are excluded from the bare tier, so "John Voce",
  "Barry Press" and "Walt Disney Company Ltd." are untouched - as are the
  corporate names whose own name spans a connector ("The Foundation for Economic
  Education Press") and the one-token heads a role label alone cannot vouch for
  ("Aery Talento de Voz", "Producciones Talento de Voz"), both of which are
  hand-remediation items rather than rule targets. A role qualifier the source
  stacked in front of a separated studio ("Jane Doe - translator Punch Audio")
  survives the cut as a credit; the one construction left out of model is a
  two-token BRAND in front of a role label ("Cool Beans Voice Over"), pinned by
  a test.

Credit names are cleaned
by `CleanCreditName` (a bounded fixpoint over eight evidence-driven rules:
trailing `" - <role>"` qualifiers stripped iteratively against a multilingual
role list measured from the libex dump, with a separator whose whitespace is
OPTIONAL ON BOTH SIDES of the dash - "Gigi Rosa-traduttore" welds the role onto
the surname, and the closed role vocabulary is what makes that safe where
`dashSepRE`'s free-text tail is not (28 dump names change, all correct) -
tolerant of repeated hyphens and NFC-normalized case-insensitive role matching;
leading `Created by `/`Creato da `/`Read by ` prefix credits dropped (the first
two measured in the dump, the third off the user-library side, where a tag spells
the narrator field as a sentence - the dump attests neither it nor a German
`gelesen von ` as a credit prefix, and the required `by ` is what keeps the real
"Read Shepherd"/"Read Mercer Schuchardt" whole); exactly-doubled
names collapsed to one half, two-plus words per half so "Duran Duran" stays;
a concatenated studio credit removed, per the tiers above; and four
CENSUS-BACKED rules that mint no id of their own - a courtesy title folded onto
the bare twin the census already holds ("Dr. Jane Doe", `honorific.go`), the
SEPARATOR-LESS spelling
of a role qualifier ("Andrew Lang Editor", `barerole.go`, its own narrow
vocabulary because "Public Play Editora" and "Augustin Eugène Scribe" end in
role words too), a trailing ACADEMIC CREDENTIAL folded onto the bare twin
("Philip Zimbardo Ph.D.", `credential.go` - the honorific rule's mirror image,
same same-side census and two-word floor, doctorate spellings only), and a
DANGLING
leading conjunction ("and Melanie Mendez", `conjunction.go`), each of which
otherwise minted a second identity for somebody already in the catalogue) - and
then, ONCE and
after the fixpoint, folded onto a canonical **collective** record if that is
what the credit names (`collective.go`). A nameless credit is classified by what
it STATES, language-independently, in four buckets: collective statements
("Narratori Vari", "diverse Sprecher", "elenco", "Anónimo") fold onto
`full-cast`/`various`/`anonymous`/`uncredited`; unknown-identity statements
("N.N.", "auteur inconnu", "narratore sconosciuto") fold onto `unknown`
(anonymity is a choice, an unknown identity is a gap - two records, deliberately);
booking placeholders ("to be announced", tbd/tba/tbc, "n/a") stay REFUSED
whole-row (`placeholderCreditNames` plus `placeholderCreditPhrases`, whose SQL
twin in `scripts/libex-export-rows.sql` now carries that bucket only - the
flipped forms came off both lists in step); and branded ensembles (The Colonial Radio Players,
Museum Audiobooks cast, Linguistics Team) are real person-side records the table
never touches - matching is the WHOLE cleaned name, exact, case- and
accent-insensitive, never a pattern. The fold sits inside the shared cleaning
rather than at `getOrCreatePerson` so authors, narrators AND role credits pass
through it, every importer inherits it (not just libex), and the batch pre-passes
see the canonical: a variant never becomes evidence of its own spelling in the
credit census or the initials decision. Two ABBREVIATIONS were folded on a later
maintainer decision and carry their dump verification in the file header, because
a short opaque string could in principle be a real name: `div.`/`div` (2,101
credits, the German/Scandinavian "diverse", the table's largest fold) onto
`various`, and `anonymus` (16) onto `anonymous`. The fold is live AND the tree is
clean: the separate DATA change that migrated the minted twins (`n-n`,
`div`, `autori-vari`, ...) has landed, so no variant record remains on disk and
the fork risk the interim state carried - a work recorded under a variant AUTHOR
being addressed by a different author set than an incoming row states - is gone.
The importer's tolerance for an existing variant record is kept as a SAFETY
PROPERTY (a variant reappearing through a bad merge or a hand edit is inert, not
a crash or a re-fed twin), pinned by
`TestCollectiveFoldToleratesAnExistingVariantRecord`. Note that
the two tables still sit at different LAYERS - `placeholderCreditNames` refuses
the ROW at the libex parse layer, this one rewrites the CREDIT at the fixpoint
tail - but the layer GAP between them is closed: the placeholder refusal judges
the raw name and the cleaned one ("To Be Announced - narrator", "TBD - narrator"),
and carries a phrase tier for the placeholders that arrive with an imprint or a
studio welded on ("To Be Confirmed Audio", "To Be Confirmed Atria", "Holt Author
To Be Announced" - 10 names / 16 credits / 15 books dump-wide beyond the exact
list, zero of them real), plus a DOUBLED tier keyed off the same table (a
placeholder repeated - "TBD TBD" - is not the exact string, contains no phrase
and survives the doubled-name collapse, which needs two words a side so "Duran
Duran" stays; `doubledCreditHalf`, shared with `canonicalCreditName`, which
folds the doubled collectives "Anonymous Anonymous"/"unknown unknown" the same
way). The three-letter abbreviations stay exact-only on
purpose: "TBA Studios" is a real production company. Either way
the person stays in the credit list; a stripped qualifier is no longer
DISCARDED: `CreditWithRoles` returns the schema roles it stated (the
`roleQualifiers` table is both the strip vocabulary and the qualifier -> role
map; a nil value means "strip, state nothing", which is every qualifier with no
home in the enum), and `planner.workCredits` records them as the work's
`credits`, sorted by (person, role) and restricted to people the planner knows,
so the emitted list satisfies metacheck's credit-integrity rule by
construction - and NEVER naming the shared catch-all `person` record: a name
that slugs away to nothing (Korean/Cyrillic/CJK) is a known conflation on the
authors list but would be a false claim as a role credit, so those are dropped
and reported in aggregate. Credits fill-absent ACROSS runs (a recorded list is
somebody's account of the work and is never spliced into) and accrete WITHIN one
(`addRunCredits`: a work the run itself created or filled takes the pairs later
rows state, so a second region's row is not silently dropped). A trailing
credential/generational fragment that had to be trimmed for the role to match is
re-appended to the NAME ("X - editor Jr." is X Jr., an editor), never discarded.
Credit lists dedupe by slug and the
schema's `uniqueItems` rejects doubled credits; a **work** is (title slug
+ author set), but series volumes that share a `title_short` (or a book mapping
onto an existing work at a different position in the same series) derive their
work from the **full title** instead, so distinct volumes never merge - a batch
pre-pass (grouping by title slug only, since Audible's author field varies per
volume) detects this before any slug is claimed; different-author title
collisions get an author suffix (numeric only as last resort); series collide to
numeric. Every composed slug is **bounded to `MaxSlugLen` by
`BoundedSlugTail`/`NumberedSlugAt`** (shared by the work, recording and series
chains and by `internal/issueform`): the TITLE (or narrator, or series name) is
shortened at a word boundary and the disambiguating credit/year/number always
survives, so a long name can never mint an invalid id - and because truncation
can make two long titles by one author meet on one slug, a merge onto a
shortened candidate warns. A work title is **cleaned of a trailing
`(Unabridged)`/`(Abridged)` edition marker before identity**
(`cleanWorkTitle`), so "Mageling" and "Mageling
(Unabridged)" are the same work - the stripped marker is not lost: it seeds the
recording's tri-state `abridged` when the source did not state it
(`abridgedFromMarker`; the title printing the edition is a source statement, so
this stays facts-only). `cleanWorkTitle` also sees through a **narrator
qualifier embedded MID-title** in front of a volume marker ("... - gelesen von
Andreas Lange, Band 11" -> "..., Band 11", `stripTitleNarratorQualifier`) - the
same see-through `cleanSeriesName` performs one level up, bounded to the shape
that forked three works out of one book (13 of the dump's 1,058,981 distinct
titles, zero false positives) and reading a pinned SUBSET of the series
narrator vocabulary. And a same-work, same-narrator entry whose
only new fact is another ASIN **merges that ASIN into the existing recording**,
with provenance (the merge appends a `sources[]` entry ref'ing the merged ASIN)
and two guards - runtime (a >10% gap between two known runtimes is a genuinely
different production and gets its own recording under the same work) and
abridged (a known-abridged entry never merges into an unabridged/unstated
recording) - rather than
minting a sibling work or dropping the ASIN (`Summary.MergedASINs`). Series
positions accept omnibus ranges (`"1-3.5"`), spelled with or without whitespace
around the dash (`NormalizeSequence` tolerates it on INPUT and removes it on the
way to the canonical value, so what is stored still satisfies the schema's
whitespace-free pattern), and `recording.abridged` is optional
(emitted only when the source states it) - see the schema notes below. On the
intake side (`internal/issueform`): the bot **sniffs a self-identifying
`audiosilo-books` envelope and routes it to that importer regardless of the
form's export-type dropdown** (the file is trusted over the form). A
`duplicate` verdict requires `Skipped > 0` - an import that produced nothing AND
deduped nothing is `needs-human` (the file likely does not match the selected
export type), surfacing the importer warnings.

**Trust tiers / the user-overwrite rule** (`pkg/model/trust.go` +
`internal/importer/attest.go`; policy in LICENSING.md, governance consequence in
GOVERNANCE.md). Source types are ranked - user-library submissions (`user`,
`openaudible-import`, `libation-import`, `audiosilo-books-import`) outrank the
bulk mirror (`libex-import`), with everything else in an inert reference tier -
and a record is **bulk-mirror-only** iff it has sources and every one of them is
mirror-tier. `model.TierOfSource` / `model.BulkMirrorOnly(Types)` are the ONLY
place source-type strings are compared. When a user-library run matches such a
record by ASIN (the create/recordings-only dedup skip, and the ASIN-merge path,
which must attest BEFORE its own stamp ends the record's mirror-only status),
the facts the export states OVERWRITE the recorded ones and the run's source
entry is appended - the record is user-attested from then on, counted as
`Summary.Attested{Works,Recordings}`, and every later run is back to the
ordinary posture (existing wins, fill-only-absent, the runtime/date
contradiction guards, `added_at` never touched). A contradiction on a user run
is counted as `Summary.Conflicts` and warned: first writer wins, flagged for
review, never last-writer-wins churn - and a refused row writes NOTHING, not even
a stamp, so a mirror seed stays attestable by the next agreeing import.
`metaimport --conflicts <path>` additionally appends a durable **conflict
worklist** (`internal/importer/conflicts.go`): one NDJSON `Conflict` row per
refused contradiction (run/asin/work/recording/field/recorded/stated/
source_type/detected_at), written where the guard fires rather than re-derived
from the warning text, in EVERY tier - `Summary.Conflicts` counts only a user
run's, but a wrong RECORDED value (a recording stored at 81 minutes that the
dump and its own chapters both put at 492) hides in the mirror's disagreements.
Purely additive: an absent flag is the long-standing behaviour byte for byte,
and the file is appended to so a chunked wave accumulates one worklist.
`applyScope` in `enrich.go` is what keeps the three call paths honest - only
enrichment fills a record it may not overwrite, an exact-ASIN attestation still
flags contradictions, and the inferred ASIN-merge match is silent. The merge
scope is also the ONE place the release-date guard does not apply: a regional
re-release legitimately carries its own date, and the merge stamps the run's
provenance either way, so refusing the row there would end the record's
mirror-only status while freezing the mirror's facts in place (the runtime
guard, which is what tells a re-release from a different production, already ran
upstream). A stated date never coarsens a recorded one either
(`fillReleaseDate`: a recorded value that is a strict extension of the stated one
stays). On the intake side a duplicate of a bulk-mirror-only record is
`needs-human` rather than `duplicate` at all three gates - ASIN/ISBN, narrator
set and work slug (the bot only composes new records, so a maintainer applies the
takeover) - an import that only attested still opens a pull request, and an
import whose ONLY effect was a conflict is `needs-human` with the adjudication
note rather than a closed duplicate.

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
- **Deterministic builds**: metabuild inserts in sorted id order so identical
  data produces identical artifacts.
- **Governance**: merge policy and trust tiers live in
  [GOVERNANCE.md](GOVERNANCE.md); schema/tooling/.github changes always need
  maintainer review (CODEOWNERS).
- **Hyphens, never em dashes** (workspace-wide rule), in docs, comments, and
  generated text alike.

## Roadmap

- **Phase 0 (done)**: schemas, metacheck/metafmt/metabuild, governance docs,
  issue forms, CI, hand-curated seed data proving the model (multi-recording
  works, dual-narrator recording, decimal series positions).
- **Phase 1**: the Go API server (`cmd/metaserve`, `internal/serve`; consumes the
  release artifact, FTS search, `/lookup?asin=|isbn=`), the **Docker image**
  (`Dockerfile`, `image.yml`), works `added_at` (then derived from git history in
  `release.yml`; the storage migration moved it onto the records), the
  **OpenAudible and Libation importers**
  (`cmd/metaimport {openaudible,libation}`, `internal/importer` - a shared pipeline
  over a typed sourceBook), the **meta.audiosilo.app site** with search + the
  in-browser `/import` diff (OpenAudible / Libation / Audiobookshelf export /
  metascan folder-scan) plus the `/add` lookup assist (search libex from the
  browser -> whitelist-parsed facts, descriptions/ratings structurally excluded
  -> confirm card -> a prefilled add-work issue; `site/src/lib/libex.ts` +
  `libex-map.ts`, CORS-open, client-side only), and **issue-form-to-PR
  automation** (`cmd/metaissue` +
  `internal/issueform` + `intake.yml`) have all landed. Remaining: Open
  Library/Wikidata crosswalk seeding and per-title ASIN lookup assist.
- **Phase 1.5 (integration landed)**: AudioSilo server/player integration.
  audiosilo-server's `GET /libraries/{id}/meta` composes a book's enrichment from
  this project's `metaserve` (`lookup` -> `works/{id}` -> `series`) behind an admin
  off-switch and a cache, and the player renders it capability-gated. Consuming
  `metaserve`'s response shapes makes them a three-repo contract (audiosilo-meta ->
  audiosilo-server `internal/meta` -> the player only if the server's outward
  envelope changes; see workspace CROSS-REPO.md §17). The "before any ABS facade"
  ordering is now satisfied: the **Audiobookshelf metadata-provider facade**
  (`GET /abs/search`, `internal/serve/abs.go`) has since shipped on top of it.
- **Libex integration (in progress)**: libex (libexdb.com - the domain moved
  off libex.lostcartographer.xyz in 2026-08 and the old host goes dark around
  2026-11, so every reference here uses the new one; a
  public Audible-metadata mirror, ~1.1M books; scrape/export permitted by its
  developer) becomes a BOUNDED source per LICENSING.md's import posture - never
  a mirror. Landed: the normalized **genres** vocabulary end-to-end (schema
  `$defs/genre` enum -> `Work.Genres` -> `checkGenresSorted` -> artifact
  schema_version 4 `work_genres` -> `workDetail.genres` + ABS facade -> site
  chips) plus metabuild prepared statements; the site `/add` lookup assist
  (search libex -> confirm card -> prefilled add-work issue; whitelist-parsed,
  descriptions/ratings structurally excluded); and the `metaimport libex`
  source (JSON/NDJSON rows -> the shared sourceBook pipeline; Audible genre
  names/browse-node ids map onto the vocabulary via the embedded, drift-guarded
  `audiblegenres.json` - validated at 99.7% coverage against the full 1.13M-book
  dump, with per-node overrides disambiguating fiction/nonfiction name
  collisions via the browse-tree hierarchy); and `metaimport libex --enrich`
  (ASIN-matched backfill: fills ONLY absent facts on existing records with
  libex-import provenance, existing values always win, a runtime/date
  contradiction disqualifies the whole row, enrich never creates anything, and
  a second identical run is a byte-level no-op); `metaimport libex
  --recordings-only` (the ALTERNATE-NARRATION pass: a row is resolved to a work
  the catalogue already holds by cleaned title slug + exact author set - seeing
  through a trailing `, Book N` volume marker and a trailing `(Full-Cast
  Edition)` production qualifier, but never an `(Excerpt)` or a companion title -
  and lands as a new recording under it, or merges its ASIN into a matching
  sibling recording; a row whose work is not here is counted as `SkippedNoWork`
  and dropped, so the mode never creates a work and never touches a series file.
  It closes the gap the other modes leave: a second narration matches no ASIN, so
  `--enrich` ignores it, fills no free series position, so `libex-select`
  excludes it, and would mint a duplicate work on the create path. Mutually
  exclusive with `--enrich`); `metaimport libex-select`
  (streams the full dump, keeps only series-completing rows with a free
  position slot and mappable language/region, per-series cap, verbatim-row
  output, a report that partitions every row read; `scripts/
  libex-export-rows.sql` + `scripts/README.md` document the dump-to-rows
  operator flow - the received dump is Postgres 16 custom format); the
  **AI-credit exclusion** (an AI is not a person, so a row crediting one is
  refused at the libex PARSE layer - `aiNarratorNames` / `aiNarratorPrefix` /
  `aiNarratorMarkers` for a synthetic VOICE and `aiSystemTokens` for a
  generative SYSTEM credited as the author ("CLAUDE.AI", "ChatGPT ChatGPT"),
  every entry an evidence-driven form measured over the full dump. Both
  vocabularies are applied to BOTH credit lists (`firstAICredit`): the dump is
  not tidy about which column its non-people land in, and gating narrators only
  put a language model in the people table. ANY AI credit disqualifies the row,
  which applies to every libex mode and reports as one aggregated warning.
  libex's own `is_vvab` flag cannot do this job: 145,558 dump books credit an AI
  voice and the flag is `false` on 145,550 of them, so
  `scripts/libex-export-rows.sql` filters on the credit too and the two lists
  are kept in step by cross-referenced comments. The VOCABULARY is applied twice
  over, at two layers with different jobs: libex's own parse layer, which also
  has to feed `libex-select`'s exclusion reasons, and `runBooks`' shared gate
  (`refuseAIBooks`, importer.go) which covers EVERY other source - all three
  user-library importers read Audible content and all three can carry a Virtual
  Voice title, so gating only one of them was arbitrary. The shared gate reads
  its names through `sourceNames`, the one place the typed-vs-comma-joined
  choice is made, so it judges the exact list `sourceCredits` will credit and an
  AI name INSIDE a comma-joined element cannot slip past it. Refusals are
  counted as `Summary.SkippedRows` and reported as one aggregated warning; for a
  libex run the shared gate is a no-op by construction); its sibling the
  **unidentifiable-credit exclusion** (a row whose author or narrator list holds
  a name that slugs away to nothing - Korean, Cyrillic, CJK, Greek, Arabic - is
  refused whole at the same parse layer, `firstUnnamedCredit` in `libex.go`,
  judged on the CLEANED name the import would store; because `personSlug`
  otherwise substitutes the shared catch-all `person` record, which on a bulk
  seed would make ONE record the credited author or narrator of thousands of
  unrelated books - 2,075 of the 142,550 selected seed rows, 417 distinct names.
  An absent book is a gap; an invented shared author is a falsehood, and the
  refusal reverses cheaply once `Slugify` transliterates. Deliberately
  libex-only: the user-library importers keep the conflation, where it is visible
  to the user whose book it is, and `workCredits`' catch-all guard still protects
  those paths. No SQL twin - the rule is Unicode folding, not a name list);
  both refusals are applied through the one `refuseLibexCredits`, which
  `libex-select` calls too, so a row the import will refuse is never selected
  into a tranche and never claims a series slot ahead of an importable sibling.
  The same posture governs an unaddressable SERIES name, one step further in
  (`getOrCreateSeries`): a name that slugs away to nothing has no identity to
  mint, so the CLAIM is refused and aggregated rather than falling back to a
  `series`/`series-2`/... chain that addressed a series by the order its rows
  arrived - the row still imports, the work is simply not placed;
  and the
  intake features (typed `libex: <ASIN>` provenance sniffing, ASIN-backed only,
  and the add-work form's Genres field validated against the embedded schema
  enum, prefilled by /add through the shared audiblegenres.json); and the
  **trust tiers / user-overwrite rule** (`pkg/model/trust.go` +
  `internal/importer/attest.go`), landed BEFORE the seed so the takeover
  machinery exists the moment users start submitting against mirror-seeded
  records - see the importer section above and LICENSING.md; and
  **contributor-role modeling** (work `credits` + the `credit_role` enum +
  person `kind`), also landed BEFORE the seed and for the same reason: the
  qualifiers ride in on the libex credit names, `CleanCreditName` used to strip
  and DISCARD them, and re-deriving roles for 142k works after the fact means
  re-processing the whole dump. The importer now maps a stripped qualifier onto
  the enum (all measured language variants; combined strings like "editor and
  translator" emit two entries; an unmapped qualifier still just strips) on the
  create path and, fill-absent, on `--enrich`. A USER-tier attestation writes
  credits too: a personal export states no genres but does state a role
  whenever a credit name carries a qualifier, so `applyToWork`'s attest scope is
  live behaviour under the tier rule (mirror-only work takes it, attested work
  keeps what it has), not the inert guard the first draft described.
  `Summary.Credits` reports what a wave captured, and a run-level warning
  reports the role-qualified credits dropped for naming nobody identifiable. The artifact and the API deliberately do NOT surface credits
  yet - no schema_version bump - so the data accrues ahead of its readers.
  Remaining
  follow-ups tracked in the plan:
  surfacing genres in the player (three-repo seam), surfacing **credits** in the
  artifact/API/site (a later, separate change), classifying the existing
  corporate person records with `kind` (a human pass, deliberately not inferred),
  and a **streaming row
  iterator for the libex parse layer** - it currently slurps the whole file
  (~6GB of live heap for the 1.06M-row dump) before planning discards what does
  not apply, which `--recordings-only` feels most: an alternate narration is by
  definition a row `libex-select` excluded, so its natural input IS the
  unfiltered dump (`scripts/README.md` step 7 documents the `split -l`
  workaround).
- **Data quality (audit landed, PREVENTION landed, repairs next)**: `metaaudit`
  measured the seed's defects (4,596 near-duplicate work clusters, most of them
  retailer-decorated titles the bulk waves minted) and the prevention layer above
  closed the door on re-creating them - the intake gate, the importer's create
  guard and metacheck's advisory census (2,881 groups), all three over the one
  `titlerule.IdentityTitleKey`. Remaining: the REPAIR waves themselves (merges
  through `pkg/redirects`, whose tombstone table was built ahead of exactly this),
  and one deliberate follow-up on the guard - **routing a refused duplicate row to
  recordings.go** as an alternate narration instead of skipping it. It is not done
  because attaching a recording asserts "this IS that book" on the evidence the
  audit measured five merge vetoes for; the `--conflicts` worklist the guard writes
  is what makes the population measurable enough to decide about.
- **Serving at scale (pre-seed, landed)**: the measured serve/distribution fixes
  from the workspace's SCALE-RESEARCH.md (section D), all invisible at 9.5k works
  and real at 100k+. D1: the six covering indexes every request path was missing
  (guarded by `TestServeLookupsAreIndexed`), every membership list AND the search
  hit page batched through `cardsByID` (one implementation - `workCard` is a
  single-id call into it, so a card's composition rules exist once),
  `people/{id}`/`series/{id}` windowed additively with totals counted over the
  same join that produces the page, `works/latest` two-phase (series memberships
  for the candidates, full cards only for the winners), and the coverage
  browser's `?q=` moved off an unindexed `LIKE '%...%'` onto the FTS index (which
  widens it to narrators and series names and narrows it to word prefixes - the
  right trade, and stated that way everywhere it is described) plus
  a per-snapshot memo for the series-gap set. D2: release assets stream - the
  full refresh downloads, hashes and decompresses in ONE pass with no staged
  `.gz` - superseded cache files are pruned on every adopt and again after the
  swap grace (sparing every artifact still draining, not just the newest), and the
  patch path declines a base artifact over 1 GiB. D3: the image no
  longer bakes data - see the Dockerfile note above - and the cache volume is
  what makes that safe: a restart adopts the artifact it already holds after
  verifying it, and a boot that cannot reach GitHub serves it as stale rather
  than answering 503 with data on disk.
- **Phase 2**: characters and recaps (spoiler-tagged, position-keyed), the CC
  BY-SA layer, under the copyright rules in META-FEASIBILITY.md §7. The
  **schema + metacheck rules, the `metabuild`/`metaserve` wiring, and four
  fully-worked exemplar series have landed** (per-work characters/recaps
  members of the works-community family, `$defs/position`, the `$defs/license_content`
  share-alike enum, artifact schema_version 2 - plus the optional whole-book
  `in_short`/`ending` recap summaries in the `recap_summaries` table at
  schema_version 3, served as `recap_summary` - `GET /works/{id}` inline
  characters/recaps/recap_summary - see the data-model section above and
  the community repo's AUTHORING.md). **The DATA then moved out** (2026-08-21, the
  community-repo split): `data/works-community/` lives in
  `KodeStar/audiosilo-meta-community` and the release build composes the two
  checkouts, so everything above is unchanged from a consumer's side - same
  tables, same schema_version, same artifact. Still to come: the **site render**
  (in progress), the **player render** (the server `/meta` + frontend three-repo
  seam - Stage 2). The near-verbatim check landed as `metaextract ngram`
  (run locally against the source text, which never enters the repo - a
  CI-side check is impossible by design), and the extraction pipeline landed
  as Phase 3 (below).
- **Phase 3 (pipeline landed)**: the source -> characters/recaps extraction
  pipeline: `cmd/metaextract` (epub split + n-gram check) plus the documented
  agent process in **EXTRACTION.md** (AUTHORING.md's sibling: rolling fact pass ->
  notes-only synthesis -> adversarial spoiler audit), validated end-to-end on
  Killing Floor (Jack Reacher #1). The audio-only variant in
  **EXTRACTION-AUDIO.md** adds local, chapter-isolated ASR plus proper-noun
  verification and was validated on the 20-hour Silvers audiobook. Both documents
  now live in the community repo (stubs here); the tool did not move. Audio orchestration is documented rather than a
  `metaextract` subcommand. Source material and transcripts never enter the
  repo; only the derived CC BY-SA sidecars are committed. Possible follow-up:
  a friendlier packaged client (audiosilo-manager).
