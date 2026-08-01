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
go run ./cmd/metacheck            # validate the data tree
go run ./cmd/metafmt --check      # canonical formatting (--write to fix)
go run ./cmd/metabuild -o meta.sqlite   # build the release artifact
go run ./cmd/metaserve --db meta.sqlite --addr :8080   # serve the read-only API
```

**Before a change is done, all of the above must pass.** CI
(`.github/workflows/check.yml`) gates build/vet/test/metacheck/metafmt on every
PR and push to main; `release.yml` builds and publishes a dated release
(`data-vYYYY.MM.DD-<shortsha>`) when data or schema changes land on main. Asset
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
`scripts/packmerge_test.go`, and run by the intake bot on its own PRs. The
pre-migration layout was one file per record
(`data/works/<shard>/<slug>/work.json` and friends); `cmd/metamigrate` converted
it, `pkg/check` and every writer now refuse it loudly.

- **work** - an entry in the works family, keyed by work slug: the abstract book
  (title, authors (person ids), language, first_published, xrefs (wikidata/
  openlibrary/goodreads/print ISBNs), optional `genres` (values from the
  controlled vocabulary enum `common.schema.json#/$defs/genre` - a flat,
  retailer-neutral list the project owns; sorted ascending, enforced by
  `checkGenresSorted`; never a retailer's taxonomy verbatim, see LICENSING.md
  "Genres")) PLUS its recordings, nested as `"recordings": {"<rec-slug>": {...}}`.
  One book is one entry, so a recording edit is a read-modify-write of its work.
- **recording** - a member of its work entry's recordings map: a specific
  narration/production, with narrators, abridged (**optional**: absence =
  unknown, so importers omit it rather than guess), runtime_min, release_date,
  publisher, region-scoped `asin[]`, `isbn[]`, cover_url, chapters. One work,
  many recordings (Harry Potter: Stephen Fry AND Jim Dale, each with its own
  ASINs). It keeps its own `id`, `work` backref, `license` and `sources`, so its
  bytes are the same as when it was a file of its own.
- **person** - an entry in the people family, shared by author and narrator roles
  (a person can be both).
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
pkg/model           PUBLIC entity structs, slug rules, and addressing: PackLocation (pack path + entry key [+ recording key], what pack-layout problem reports render). The file-path addressing the old layout needed (Shard, ParseLocation) is gone. THREE places still parse the retired per-record path shape, each its own copy and each for a different reason: internal/issueform (a correction form's `record` field is a reference a submitter types), internal/testpack (fixture addresses read better than family + two map keys), and internal/migrate (the only one that reads it as a STORAGE location, because it is the only thing that still reads the old tree). (leaf; also reached through pkg/check's exported Catalog)
pkg/canonical       PUBLIC canonical JSON (sorted keys, 2-space, trailing LF)
pkg/check           PUBLIC pack-layout load (packcheck.go walks each family's pack listing; a family that is not in the pack layout is ONE loud problem naming metamigrate, and every JSON file the listing does not account for is an unrecognized location - never silence) + schema validation (wrapper schemas load-bearing; $ref reasons preserved via detailed output) + the pack storage invariants (placement, caps + single-entry exemption, bound validity, key/id agreement) + integrity/uniqueness/chapter/series/sidecar-uniqueness rules; Result carries Problems (fail) and Warnings (advisory, e.g. a single entry over the 256KB target); schema-valid entries that fail Go decoding are reported, never silently dropped. ONE walk per load (pack.Listing supplies the file accounting, the layouts and every family's tree), and `LoadStore(*pack.Store)` is the writers' door in: it validates the tree the store was opened on through the store's own walk and parse cache, so a run that validates and then writes reads each pack ONCE (after a Flush the store's walk is stale, so LoadStore takes a fresh one - post-write validation stays a full independent load)
pkg/pack            PUBLIC pack-file storage: family/cap definitions, bound math + binary-search lookup, pack parse/serialize (canonical via pkg/canonical, raw entries, duplicate keys rejected), median split + directory split, and the read-through Store (queued-write-first reads; Flush performs due splits, directory splits and bound normalization, and errors rather than ever writing two plans to one path). survey.go classifies every JSON file under a family root BEFORE bound math sees it, so a misfiled/misnamed/empty/too-deep file is relocatable content (Salvage), never an authoritative bound; Pending covers every structural invariant metacheck enforces (Salvage/Unreadable/Misplaced/Conflicts/Rebinds/Packs/Dirs) and Heal + Flush converges to a metacheck-green, entry-preserving tree in one pass (duplicate slugs: the correctly-placed copy wins, reported as a Conflict). The Store is layout-aware PER FAMILY (absent is writable; a family in the retired file-per-record layout fails with ErrLegacyLayout, which is the loud refusal every writer inherits - DetectLayout says pack as soon as ANY file under the root reads as one, so a stray file cannot flip a converted family back). Reading a tree costs ONE walk and ONE parse per pack however many things ask for it: Listing is the walk (file-to-family partition, per-family layout, per-family tree - Detect/ReadTree without re-walking) and Reader is the parse cache; a Store keeps both and exposes them (Store.Listing/Store.Reader), and drops them at Flush. Store.HealPending is Heal plus the Pending it computed, so metafmt surveys a family once instead of twice. The one shared implementation every reader and writer builds on (see PACK-SPEC.md)
pkg/extract         PUBLIC epub split (container/OPF/spine/toc -> plain text) + the word-shingle overlap check
pkg/scan            PUBLIC local folder scanner: embedded tags + path/filename heuristics + ffprobe -> the "audiosilo-folder-scan" import doc (per-field provenance, omit-never-guess, tag-evidence collection split)
                    (pkg/* are consumed by the sibling audiosilo-sidecars module as ordinary deps, mirroring how audiosilo-server promoted pkg/launcher + pkg/match; pkg/scan still imports internal/importer for its pure normalization helpers, which is legal within-module and does not leak into pkg/scan's exported API)
internal/importer   OpenAudible books.json + Libation export + libex rows -> work/recording/person/series, ASIN-dedup, writes through the pack.Store (write.go: one works-family composite entry per work, so a recording edit is a read-modify-write of its work's entry; nothing reaches disk until flush; a legacy-layout tree is refused before anything is planned; a create at a slug the store already holds fails loudly rather than overwriting - the guard against a loader-dropped entry being clobbered). added_at is stamped ONLY on the branches that create a record, never on an ASIN merge or enrichment backfill. (Shared pipeline over a typed sourceBook, with three disjoint planning modes over it: create, enrich.go's ASIN-matched backfill, recordings.go's alternate-narration pass; audiblegenres.json maps Audible genre names/browse-node ids onto the schema's genre enum, drift-guard + golden-anchor tested)
internal/issueform  issue-form parse + compose (add-work/recording/correction/characters/recaps/import) -> dedup -> pack entries through the pack.Store (both community sidecars share ONE works-community entry, so placing recaps preserves an existing characters member; family gating is per template; a legacy tree is needs-human, not the contributor's fault), the ok/duplicate/needs-human/invalid verdict the intake workflow branches on; Result.Files names the pack files flush rewrote
internal/format     metafmt's business logic: canonical formatting (pkg/canonical) sequenced with the self-healing placement pass (--check surveys with pack.Pending; --write uses pack.HealPending -> Flush, one survey for both the report and the fix); a tree holding a non-pack family is refused before the first write (tidying files no reader will load would make a stale checkout look maintained); Report embeds pack.Pending so every category surfaces in --check/--write; unreadable files are reported and left for a human while the rest still heals; formatting runs FIRST because Flush judges an untouched pack by its on-disk size
internal/testpack   test support (imported only by _test.go files): seeds and reads a pack-layout tree addressed by the old per-record path syntax (a fixture syntax it parses itself), resolved onto family + entry key; shared by the importer, issueform and metaimport suites
internal/migrate    metamigrate's business logic: its OWN parsing of the retired file-per-record layout as a storage location (issueform and testpack parse the same shape as a reference/fixture syntax - see pkg/model), the added_at git walk with release.yml's exact semantics extended to recordings, entry composition from RAW record bytes (only added_at spliced in), and greedy planning to ~50% of the caps (sized by pkg/pack's own renderer, never by re-spelled arithmetic). Renders every pack before deleting anything, and refuses what it cannot do safely: a shallow clone (which would date every record at the tip commit), an uncommitted in-place tree (git is the only recovery from an interrupted run), a tree holding no works, a non-JSON file, an already-converted family. Deterministic; the fixture-scale artifact equivalence proof lives in its tests
internal/build      SQLite builder (deterministic, FTS5 search_fts, asin/isbn indexes; added_at from the record, else the newest sources[].imported_at compared chronologically)
internal/serve      the API server: snapshot loader, JSON handlers, FTS search, the ABS provider endpoint, GitHub-release poller/hot-swap
schema/             JSON Schemas (the contract), embedded via schema.go; the four pack-*.schema.json wrappers are load-bearing (pkg/check validates every entry and entry key through its family's wrapper fragments)
data/               the database, in the PACK-SPEC.md range-packed layout: works/ (composites), works-community/ (the CC BY-SA sidecars), people/, series/
Dockerfile          image: site build + metaserve + baked data
scripts/            operator scripts: the libex dump-to-rows SQL flow (+ README), and pack-union-merge.sh - the mechanical resolution for a pack-file conflict (a three-way merge of the entries, then metafmt --write), with packmerge_test.go driving it through real rebases including every case it must REFUSE
.github/            issue forms (machine-parseable ids, data:* routing labels); check + release + image + intake + ai-verify workflows. intake.yml also rebases the bot's own open PRs on every push to main that touches data/ (three-way merge + metafmt + metacheck, per-PR failures contained to a comment); permissions are per-job and the rebase sweep has its own concurrency group; release.yml is a SHALLOW checkout now that added_at lives in the data
```

**The API server (`internal/serve`)** opens the SQLite artifact read-only and
serves JSON under `/api/v1` (stats, `search?q=`, `works/latest`, `works/{id}`,
recording `chapters`, `people/{id}`, `series/{id}`, `lookup?asin=|isbn=`) plus
`/healthz`; it can also serve a static site at `/`. It additionally exposes an
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
currently-loaded artifact (`tryPatch` -> `applyPatch`: zstd raw-dict id 0, the
CLI's patch-from convention; `--long=31` window; the patched file verified
byte-for-byte against `meta.sqlite.sha256` before it is installed) and falls back
unconditionally to a full `meta.sqlite.gz` download (`fullRefresh`, verified
against `meta.sqlite.gz.sha256`) whenever a patch is unavailable or fails - the
first refresh after boot is always full. Either way it gunzips/reconstructs into
the cache and hot-swaps the pointer; in-flight requests finish on the old handle
(closed after a grace delay), a rejected patch never swaps, and a poll failure
only logs and retries, never crashes the process. The poll loop runs one refresh
**immediately at startup**, before the first `--interval` tick, so the production
Docker boot (a baked `--db` artifact **and** `--poll`, where `New()` skips the
poll-only synchronous first refresh) catches a recreated container up to the
newest release within seconds instead of serving build-time data for a full
interval; on a poll-only boot `New()` already refreshed, so the startup poll is a
cheap conditional 304.
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
final token prefixed with `*`) so no user input can break the MATCH. Business
logic stays in `internal/serve`; `cmd/metaserve` is flag wiring only.

The importer maps one export entry to a work + recording (+ people + series),
importing **factual fields only** (LICENSING.md): it drops publisher copy, raw
retailer genre strings, ratings and personal state, deduplicates by ASIN against the catalogue,
and writes canonical files, then runs `pkg/check`. Identity rules: a
**person slug is the identity** (spelling/diacritic variants of one name merge
into the existing record; no numbered duplicates), and credit names are cleaned
by `CleanCreditName` (a bounded fixpoint over three evidence-driven rules:
trailing `" - <role>"` qualifiers stripped iteratively against a multilingual
role list measured from the libex dump, with a separator tolerant of missing
spaces/repeated hyphens and NFC-normalized case-insensitive role matching;
leading `Created by `/`Creato da ` prefix credits dropped; exactly-doubled
names collapsed to one half, two-plus words per half so "Duran Duran" stays) -
the person stays in the credit list; credit lists dedupe by slug and the
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
this stays facts-only); and a same-work, same-narrator entry whose
only new fact is another ASIN **merges that ASIN into the existing recording**,
with provenance (the merge appends a `sources[]` entry ref'ing the merged ASIN)
and two guards - runtime (a >10% gap between two known runtimes is a genuinely
different production and gets its own recording under the same work) and
abridged (a known-abridged entry never merges into an unabridged/unstated
recording) - rather than
minting a sibling work or dropping the ASIN (`Summary.MergedASINs`). Series
positions accept omnibus ranges (`"1-3.5"`) and `recording.abridged` is optional
(emitted only when the source states it) - see the schema notes below. On the
intake side (`internal/issueform`): the bot **sniffs a self-identifying
`audiosilo-books` envelope and routes it to that importer regardless of the
form's export-type dropdown** (the file is trusted over the form), and a
`duplicate` verdict requires `Skipped > 0` - an import that produced nothing AND
deduped nothing is `needs-human` (the file likely does not match the selected
export type), surfacing the importer warnings.

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
  **AI-narration exclusion** (a synthetic voice is not a person, so a row
  crediting one is refused at the libex PARSE layer - `aiNarratorNames` /
  `aiNarratorPrefix` / `aiNarratorMarkers` in `libex.go`, an evidence-driven
  vocabulary measured over the full dump, ANY AI credit disqualifying the row -
  which applies to every libex mode and reports as one aggregated warning.
  libex's own `is_vvab` flag cannot do this job: 145,558 dump books credit an AI
  voice and the flag is `false` on 145,550 of them, so
  `scripts/libex-export-rows.sql` filters on the credit too and the two lists
  are kept in step by cross-referenced comments); and the
  intake features (typed `libex: <ASIN>` provenance sniffing, ASIN-backed only,
  and the add-work form's Genres field validated against the embedded schema
  enum, prefilled by /add through the shared audiblegenres.json). Remaining
  follow-ups tracked in the plan:
  surfacing genres in the player (three-repo seam), and a **streaming row
  iterator for the libex parse layer** - it currently slurps the whole file
  (~6GB of live heap for the 1.06M-row dump) before planning discards what does
  not apply, which `--recordings-only` feels most: an alternate narration is by
  definition a row `libex-select` excluded, so its natural input IS the
  unfiltered dump (`scripts/README.md` step 7 documents the `split -l`
  workaround).
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
  as Phase 3 (below). Also: **contributor role modeling** -
  translator/introduction/editor credits are currently plain people on the work
  (the importer strips the role qualifier from the name); a future schema field
  should carry the role. Related: **corporate credits** (Marvel Press, Lucasfilm
  Press, Audible Studios, Full Cast) are modeled as person records when they are
  the author/narrator of record - an established pattern from the earliest
  imports; if roles ever get modeled, an entity-kind field belongs in the same
  design.
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
