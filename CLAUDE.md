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
is CC0-1.0 (factual core) with a CC BY-SA 3.0 community layer (the
characters/recaps sidecars) - the full policy
in [LICENSING.md](LICENSING.md) is load-bearing, read it before touching data
handling.

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
go run ./cmd/metacheck            # validate the data tree (~10s over 133k works)
go run ./cmd/metafmt --check      # canonical formatting (--write to fix)
go run ./cmd/metabuild -o meta.sqlite   # build the release artifact
go run ./cmd/metaserve --db meta.sqlite --addr :8080   # serve the read-only API
```

**Before a change is done, all of the above must pass.** CI
(`.github/workflows/check.yml`) gates build/vet/test/metacheck/metafmt on every
PR and push to main; `release.yml` builds and publishes a dated release
(`data-vYYYY.MM.DD-<shortsha>`) when data, schema or the BUILDER
(`internal/build/**`, `cmd/metabuild/**`) changes land on main - the artifact is
compiled, so a new index or a `SchemaVersion` bump must reach a release without
waiting for an unrelated data edit. Asset
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

**Storage is range-packed** ([PACK-SPEC.md](PACK-SPEC.md), implemented in
`pkg/pack`): four families of pack files, each `{"entries": {"<slug>": {...}}}`,
each file covering `[its own bound, the next sibling's bound)` where the bound is
its name minus `.json`.

```
data/works/<dir-bound>/<bound>.json            CC0      work composites (work + its recordings)
data/works-community/<dir-bound>/<bound>.json  CC BY-SA characters + recaps, keyed by work slug
data/people/<bound>.json                       CC0
data/series/<bound>.json                       CC0
```

works and works-community carry a directory level from day one; people and series
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
sides wrote is still a refusal. The
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
  the WORK's slug): community-authored, spoiler-tagged character entries. Each
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
`CC0-1.0`; the CC BY-SA layer (characters/recaps) is `CC-BY-SA-3.0`. A core
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
the recap summary on >= 3, and genres on >= 4, so a table's absence is "no
data", not a 500. **Authoring the expressive
layer is documented in [AUTHORING.md](AUTHORING.md)** (the reusable process:
positions, spoiler model, copyright caps, checklist) - read it before adding
characters/recaps by hand or with an agent. The four exemplar series (First Law,
Scholomance, Lord of the Rings, Magic Faraway Tree) are the worked reference.

## Package layout

```
cmd/metacheck|metafmt|metabuild   thin CLIs; logic lives in internal/ (metafmt drives internal/format; metacheck prints advisories to stderr without affecting the exit status)
cmd/metaserve       thin CLI: the read-only HTTP API server (flag wiring only)
cmd/metaextract     thin CLI: epub -> chapter text + manifest (split), n-gram no-verbatim check (ngram); see EXTRACTION.md and EXTRACTION-AUDIO.md
cmd/metascan        thin CLI: scan a local audiobook folder -> import JSON (flag wiring only)
cmd/metaimport      thin CLI: ingest an external library export into data/ (openaudible, libation, libex)
cmd/metaissue       thin CLI: an issue-form body -> canonical records + a machine-readable verdict (flag wiring only)
cmd/metamigrate     thin CLI: the ONE-OFF conversion of a pre-pack file-per-record tree into the pack layout, with the added_at git-history backfill (--out rehearses out of place; logic in internal/migrate)
cmd/metaremediate   thin CLI: the ONE-OFF GraphicAudio part-product repair (dry-run by default; --write applies; logic in internal/remediate)
pkg/model           PUBLIC entity structs, slug rules (Slugify and PersonSlug live here, the leaf both internal/importer and pkg/check reach - the importer imports check, so the reverse would be a cycle and a second copy would be a contract with two definitions; the importer mints person ids through PersonSlug and pkg/check verifies them through the same call, including the reserved-word variant it composes), the RESERVED SLUGS (reserved.go: the route literals `search`/`latest`, IsReservedSlug and the one canonical ReservedPersonSlug both the minting and the checking read), and addressing: PackLocation (pack path + entry key [+ recording key], what pack-layout problem reports render). The file-path addressing the old layout needed (Shard, ParseLocation) is gone. THREE places still parse the retired per-record path shape, each its own copy and each for a different reason: internal/issueform (a correction form's `record` field is a reference a submitter types), internal/testpack (fixture addresses read better than family + two map keys), and internal/migrate (the only one that reads it as a STORAGE location, because it is the only thing that still reads the old tree). Also the SOURCE TRUST TIERS (trust.go): the one table ranking sources[] types and the one bulk-mirror-only test the overwrite policy is written in terms of. (leaf; also reached through pkg/check's exported Catalog)
pkg/canonical       PUBLIC canonical JSON (sorted keys, 2-space, trailing LF)
pkg/check           PUBLIC pack-layout load (packcheck.go walks each family's pack listing; a family that is not in the pack layout is ONE loud problem naming metamigrate, and every JSON file the listing does not account for is an unrecognized location - never silence) + schema validation (wrapper schemas load-bearing; $ref reasons preserved via detailed output) + the pack storage invariants (placement, caps + single-entry exemption, bound validity, key/id agreement) + integrity/uniqueness/chapter/series/credit-pair/person-slug (a person's id must BE `model.PersonSlug(name)` - the same call the importer mints it with - so a record minted under an older slug rule cannot sit at an address nothing will probe again, the shape that forked Madeleine L'Engle in two)/sidecar-uniqueness/reserved-slug (no work, person or series id may BE an API route literal - see the data-model section) rules; Result carries Problems (fail) and Warnings (advisory, e.g. a single entry over the 256KB target); schema-valid entries that fail Go decoding are reported, never silently dropped. ONE walk per load (pack.Listing supplies the file accounting, the layouts and every family's tree; a data root or family root that is not a directory is an error, never an empty family). The PER-PACK work (parse, schema-validate, decode, size) is independent between files, so it runs on a bounded pool of GOMAXPROCS workers (`parallel.go`) while everything cross-record still runs afterwards, single-threaded, over the assembled catalogue - 66s -> 10s over the 133k-work tree. DETERMINISM is a property of the merge, not of the scheduler: a worker fills a `packResult` of its own (its problems, its advisories, its slice of the catalogue, its path index) and the coordinating goroutine merges them in the LISTING's order, so problems, advisories and catalogue order are byte-identical to the sequential walk this replaced whatever order the workers finished in (`TestParallelLoadIsDeterministic` pins it across repeat runs and GOMAXPROCS 1/2/3/8; the real-tree output was diffed byte-for-byte before and after). `LoadStore(*pack.Store)` is the writers' door in: it validates the tree the store was opened on over the store's own walk, so a validating writer walks the tree once instead of twice. It is AS-OF-OPEN by construction (documented on LoadStore) and it never grows the writer's memory: a pack it reads is released as soon as it has been validated, and the canonical render memo the cap check leaves is released with it - the store re-reads the handful of packs it writes to, which is what it always did. After a Flush the store's walk is stale, so LoadStore takes a fresh one - post-write validation stays a full independent load. LoadStore is parallel TOO: the borrower only ASKS the store's parse cache (`pack.Reader.Cached`, a lookup in two maps that writes to neither) and never fills it, and LoadStore is synchronous in its caller, so the store's own goroutine is blocked for the pool's lifetime and no Read/Drop/Flush can race it - stated on `pack.Reader` and at `check.readPack`, and filling the cache from there would now be a data race as well as the residency mistake it already was. Peak RSS over the real tree rises ~1.2GB -> ~1.7GB, which is GC pacing under a 6.5x allocation rate (a GOMAXPROCS=1 run of either binary sits at ~1.65GB), not packs held: at most one pack file is resident per worker. pkg/check's real-data test SKIPS under -race (`raceEnabled`, `race_{on,off}_test.go`): the fixture suites cover every path of the parallel loader under the detector, the real tree adds data coverage rather than concurrency coverage, and it cost 13 minutes - past Go's 10-minute default
pkg/pack            PUBLIC pack-file storage: family/cap definitions, bound math + binary-search lookup, pack parse/serialize (canonical via pkg/canonical, raw entries, duplicate keys rejected), median split + directory split, and the read-through Store (queued-write-first reads; Flush performs due splits, directory splits and bound normalization, and PLANS EVERY FAMILY BEFORE IT WRITES A BYTE, so a refusal leaves the tree exactly as it found it rather than half-rewritten for a human to heal on top of. Two refusals live there: a pack holding an entry that belongs in a LATER pack (ErrMisplacedEntries - its range no longer describes what it holds, so splitting it would mint a bound the later pack already carries; the entries are invisible to every reader either way, so the writer stops and names the entry, and metafmt --write does the relocating, since only the healer's rules may decide which copy wins), and the two-plans-on-one-path backstop behind it, which names both packs). survey.go classifies every JSON file under a family root BEFORE bound math sees it, so a misfiled/misnamed/empty/too-deep file is relocatable content (Salvage), never an authoritative bound; Pending covers every structural invariant metacheck enforces (Salvage/Unreadable/Misplaced/Conflicts/Rebinds/Packs/Dirs) and Heal + Flush converges to a metacheck-green, entry-preserving tree in one pass (duplicate slugs: the correctly-placed copy wins, reported as a Conflict). The Store is layout-aware PER FAMILY (absent is writable; a family in the retired file-per-record layout fails with ErrLegacyLayout, which is the loud refusal every writer inherits - DetectLayout says pack as soon as ANY file under the root reads as one, so a stray file cannot flip a converted family back). Reading a tree costs ONE walk per run: Listing is that walk (file-to-family partition, per-family layout, per-family tree - Detect/ReadTree without re-walking) and Reader is the writer's parse cache, so a pack the store reads and rewrites is parsed once; a Store keeps both and exposes them (Store.Listing/Store.Reader), and gives both up at Flush - including a FAILED flush, which has at minimum composed the queue into the packs it holds. The listing is as-of-Open and is a reading optimization only: survey re-scans the family root, because what gets relocated, rewritten or deleted has to be decided from the files that are there now. A borrower validating the tree (check.LoadStore) reads the cache but never fills it - retaining every pack for the rest of a run costs hundreds of megabytes to save a handful of re-reads. Store.HealPending is Heal plus the Pending it computed, so metafmt surveys a family once instead of twice. The one shared implementation every reader and writer builds on (see PACK-SPEC.md)
pkg/extract         PUBLIC epub split (container/OPF/spine/toc -> plain text) + the word-shingle overlap check
pkg/scan            PUBLIC local folder scanner: embedded tags + path/filename heuristics + ffprobe -> the "audiosilo-folder-scan" import doc (per-field provenance, omit-never-guess, tag-evidence collection split)
                    (pkg/* are consumed by the sibling audiosilo-sidecars module as ordinary deps, mirroring how audiosilo-server promoted pkg/launcher + pkg/match; pkg/scan still imports internal/importer for its pure normalization helpers, which is legal within-module and does not leak into pkg/scan's exported API)
internal/importer   OpenAudible books.json + Libation export + libex rows -> work/recording/person/series, ASIN-dedup, writes through the pack.Store (write.go: one works-family composite entry per work, so a recording edit is a read-modify-write of its work's entry; nothing reaches disk until flush; a legacy-layout tree is refused before anything is planned; a create at a slug the store already holds fails loudly rather than overwriting - the guard against a loader-dropped entry being clobbered). added_at is stamped ONLY on the branches that create a record, never on an ASIN merge or enrichment backfill. No chain ever CLAIMS a reserved slug (the API route literals): the work chain marks the reserved base probe-only, the series chain shifts by one (SeriesSlugAt), and person ids come from model.PersonSlug, which composes the reserved variant itself. (Shared pipeline over a typed sourceBook, with three disjoint planning modes over it: create, enrich.go's ASIN-matched backfill, recordings.go's alternate-narration pass; conflicts.go is the optional NDJSON conflict worklist - the durable twin of the contradiction warnings, written where the guard fires and nil unless --conflicts was passed; audiblegenres.json maps Audible genre names/browse-node ids onto the schema's genre enum, drift-guard + golden-anchor tested)
internal/issueform  issue-form parse + compose (add-work/recording/correction/characters/recaps/import) -> dedup -> pack entries through the pack.Store (both community sidecars share ONE works-community entry, so placing recaps preserves an existing characters member; family gating is per template; a legacy tree is needs-human, not the contributor's fault; a person-name correction that would change the record's SLUG is a rename - the record moves and every work crediting it has to be rewritten - so it is needs-human too, rather than being written and then reported as "invalid" by the person-slug metacheck rule; a title or series name that slugs to a RESERVED route literal steps onto the same candidate the bulk importer would mint - AuthorSuffixedWorkSlug / SeriesSlugAt / PersonSlug - and says so in the verdict, rather than composing a record metacheck would bounce), the ok/duplicate/needs-human/invalid verdict the intake workflow branches on; Result.Files names the pack files flush rewrote. Corrections run off ONE op-typed allowlist (`correctableFields`, values are a `correctOp` carrying either a scalar coercion kind or an add func, so a listed field always has an op and can never be reachable with nothing to do - which would stamp provenance onto a record it left unchanged). Most fields are scalars the correction REPLACES; two on a recording are ADDITIVE - `isbn` and `publishers` contribute one entry to an array of independent facts rather than replacing the list - an ISBN nobody recorded is appended, an ISBN recorded BARE and corrected with a region is upgraded in place (a bare entry states no region, so scoping it only adds information), and a DIFFERENT stated region, or a different imprint for a region already listed, is needs-human because a stated fact is never rewritten mechanically. The add forms take the same two shapes (`region: ISBN` lines beside bare ones, `region: Publisher` lines) and refuse the schema's/pkg/check's own violations at compose time - a restated publisher of record, a repeated region, publishers[] with no publisher - so a submitter gets a verdict naming the fix instead of a raw metacheck line - and the same pair is guarded from the SCALAR side too (correcting `publisher` to a name publishers[] already lists for a region is needs-human, not a write that metacheck then bounces). Note the deliberate ASYMMETRY in the two identifier parsers: a bare ASIN defaults to `us` (an ASIN only exists inside a marketplace), a bare ISBN stays region-UNSTATED (that is a true state, and inventing one would be a fabricated fact)
internal/format     metafmt's business logic: canonical formatting (pkg/canonical) sequenced with the self-healing placement pass (--check surveys with pack.Pending; --write uses pack.HealPending -> Flush, one survey for both the report and the fix); a tree holding a non-pack family is refused before the first write (tidying files no reader will load would make a stale checkout look maintained); Report embeds pack.Pending so every category surfaces in --check/--write; unreadable files are reported and left for a human while the rest still heals; formatting runs FIRST because Flush judges an untouched pack by its on-disk size
internal/testpack   test support (imported only by _test.go files): seeds and reads a pack-layout tree addressed by the old per-record path syntax (a fixture syntax it parses itself), resolved onto family + entry key; shared by the importer, issueform and metaimport suites
internal/remediate  metaremediate's business logic: the ONE-OFF repair that folds GraphicAudio's multi-part dramatized products back into one work per book and gives the series slots those parts hijacked back to the plain text editions. Cohort gate is the PUBLISHER, not the title marker (other publishers really do spell a series volume "Book 1 of 10"); it plans the whole change - merges, same-ASIN twin collapses, series repairs - before writing a byte, and a book it cannot resolve unambiguously is left exactly as it was and named in the report (author splits, a part with several recordings, a slug another work holds, an ambiguous plain twin, a series position two works would share). Reads the tree ONCE outside the store's parse cache, writes through pkg/pack, deterministic and idempotent. See scripts/README.md for the operator flow
internal/migrate    metamigrate's business logic: its OWN parsing of the retired file-per-record layout as a storage location (issueform and testpack parse the same shape as a reference/fixture syntax - see pkg/model), the added_at git walk with release.yml's exact semantics extended to recordings, entry composition from RAW record bytes (only added_at spliced in), and greedy planning to ~50% of the caps (sized by pkg/pack's own renderer, never by re-spelled arithmetic). Renders every pack before deleting anything, and refuses what it cannot do safely: a shallow clone (which would date every record at the tip commit), an uncommitted in-place tree (git is the only recovery from an interrupted run), a tree holding no works, a non-JSON file, an already-converted family. Deterministic; the fixture-scale artifact equivalence proof lives in its tests
internal/build      SQLite builder (deterministic, FTS5 search_fts, asin/isbn indexes plus the per-(work,recording) and per-series covering indexes every serve request reads - guarded by `TestServeLookupsAreIndexed` in internal/serve, which EXPLAIN-QUERY-PLANs the very query CONSTANTS the request path uses and fails on a full table scan, so an edited query cannot drift away from its index; adding an index does NOT bump SchemaVersion, which versions what a reader may SELECT; added_at from the record, else the newest sources[].imported_at compared chronologically)
internal/serve      the API server: snapshot loader, JSON handlers, FTS search, the ABS provider endpoint, GitHub-release poller/hot-swap
schema/             JSON Schemas (the contract), embedded via schema.go; the four pack-*.schema.json wrappers are load-bearing (pkg/check validates every entry and entry key through its family's wrapper fragments)
data/               the database, in the PACK-SPEC.md range-packed layout: works/ (composites), works-community/ (the CC BY-SA sidecars), people/, series/
Dockerfile          image: site build + metaserve, NO baked data (the catalogue is fetched from the newest data release at boot; a UI change never rebuilds the database)
scripts/            operator scripts: the libex dump-to-rows SQL flow (+ README), and pack-union-merge.sh - the mechanical resolution for a pack-file conflict (a three-way merge of the entries, then metafmt --write), with packmerge_test.go driving it through real rebases including every case it must REFUSE
.github/            issue forms (machine-parseable ids, data:* routing labels); check + release + image + intake + ai-verify workflows. intake.yml also rebases the bot's own open PRs on every push to main that touches data/ (three-way merge + metafmt + metacheck, per-PR failures contained to a comment); permissions are per-job and the rebase sweep has its own concurrency group; release.yml is a SHALLOW checkout now that added_at lives in the data
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
on both surfaces. The series-position boost applies to `works/search` too and
to that scope ONLY, since the ids it resolves are always works; a literal path
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
FTS queries are built defensively (`ftsQuery`: every token quoted + escaped,
final token prefixed with `*`) so no user input can break the MATCH.
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
degrades to "no boost" and is logged, never a 500. Business
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
by `CleanCreditName` (a bounded fixpoint over four evidence-driven rules:
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
a concatenated studio credit removed, per the tiers above) - and then, ONCE and
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
list, zero of them real). The three-letter abbreviations stay exact-only on
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
- **Libex integration (in progress)**: libex (libex.lostcartographer.xyz, a
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
  [AUTHORING.md](AUTHORING.md)). Still to come: the **site render** (in
  progress), the **player render** (the server `/meta` + frontend three-repo
  seam - Stage 2). The near-verbatim check landed as `metaextract ngram`
  (run locally against the source text, which never enters the repo - a
  CI-side check is impossible by design), and the extraction pipeline landed
  as Phase 3 (below).
- **Phase 3 (pipeline landed)**: the source -> characters/recaps extraction
  pipeline: `cmd/metaextract` (epub split + n-gram check) plus the documented
  agent process in **[EXTRACTION.md](EXTRACTION.md)** (AUTHORING.md's sibling:
  rolling fact pass -> notes-only synthesis -> adversarial spoiler audit),
  validated end-to-end on Killing Floor (Jack Reacher #1). The audio-only
  variant in **[EXTRACTION-AUDIO.md](EXTRACTION-AUDIO.md)** adds local,
  chapter-isolated ASR plus proper-noun verification and was validated on the
  20-hour Silvers audiobook. Audio orchestration is documented rather than a
  `metaextract` subcommand. Source material and transcripts never enter the
  repo; only the derived CC BY-SA sidecars are committed. Possible follow-up:
  a friendlier packaged client (audiosilo-manager).
