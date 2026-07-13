# CLAUDE.md - AudioSilo Meta

Guidance for working in this repository. Keep this file updated as the codebase
evolves. This is the sixth repo in the AudioSilo workspace (`~/dev/audiosilo`) -
read the workspace [CLAUDE.md](../CLAUDE.md) and
[META-FEASIBILITY.md](../META-FEASIBILITY.md) (the research + design basis for
this project) before working here.

## What this is

An **open, community-editable audiobook metadata database** - the data behind
meta.audiosilo.app (deployment pending). The GitHub repository IS the database:
one JSON file per entity, contributed via pull requests and issue forms,
validated by Go tooling in CI, and compiled into a SQLite artifact published as
a GitHub Release. The Go API server (`metaserve`) serves that artifact plus the
static site in `site/` (Astro, the audiosilo.app design system); the AudioSilo player
integration (Phase 1.5) is the priority consumer and ships **before** any
Audiobookshelf-provider facade (ABS is a direct competitor; this should be a
defining AudioSilo feature).

Module path: `github.com/kodestar/audiosilo-meta`. Code is AGPL-3.0; the data
is CC0-1.0 (factual core) with a reserved CC BY-SA 3.0 layer - the full policy
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

Path is identity; slugs `^[a-z0-9]+(-[a-z0-9]+)*$`; shard dir = first 2 chars
of the slug. JSON Schemas in `schema/*.schema.json` (draft 2020-12,
`additionalProperties: false`) are the public contract - embedded into the
tooling via `schema.go`, so schema edits are code changes with tests.

- **work** `data/works/<shard>/<slug>/work.json` - the abstract book: title,
  authors (person ids), language, first_published, xrefs (wikidata/openlibrary/
  goodreads/print ISBNs).
- **recording** `data/works/<shard>/<slug>/recordings/<rec-slug>.json` - a
  specific narration/production: narrators, abridged (**optional**: absence =
  unknown, so importers omit it rather than guess), runtime_min, release_date,
  publisher, region-scoped `asin[]`, `isbn[]`, cover_url, chapters. One work,
  many recordings (Harry Potter: Stephen Fry AND Jim Dale, each with its own
  ASINs). The shard is the **parent work's** slug shard.
- **person** `data/people/<shard>/<slug>.json` - shared by author and narrator
  roles (a person can be both).
- **series** `data/series/<shard>/<slug>.json` - name + ordered works with
  **string** positions ("1", "2.5") including omnibus ranges ("1-3.5"); no two
  works may share a position.
- **characters** `data/works/<shard>/<slug>/characters.json` - a per-work
  sidecar: community-authored, spoiler-tagged character entries. Each has an
  `id` (unique within the file, not globally - two works may each have a
  `bilbo-baggins`), `name`, optional `aliases`/`role`, a `reveal` position (the
  spoiler gate), a length-capped own-words `description`, and optional `xref`
  (a shared `wikidata` QID links a recurring character across a series' per-work
  files). Recurring characters are **re-described per book** (Kindle-X-Ray
  model), so spoilers stay bounded by which book you're in.
- **recaps** `data/works/<shard>/<slug>/recaps.json` - a per-work sidecar:
  position-keyed "story so far" summaries. Each has a `through` position (safe
  to show once the listener finishes that chapter), an optional `scope`
  (`book`/`series` - a `chapter:0`+`series` entry is the "previously, in earlier
  books" recap), and length-capped own-words `text` (cap 3000). No two recaps in
  a file share a `through` chapter. The file also carries two optional
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
recaps sidecar carries at least one of the two fields; character `ord`/recap
position order preserved, deterministic), and `metaserve` returns them inline on
`GET /works/{id}` (`workDetail.characters`/`.recaps`/`.recap_summary`,
`omitempty`). The serve queries degrade gracefully if the tables are absent (a
newer binary briefly serving a v1/v2 release): characters/recaps gate on
schema_version >= 2 and the recap summary on >= 3, so a table's absence is "no
data", not a 500. **Authoring the expressive
layer is documented in [AUTHORING.md](AUTHORING.md)** (the reusable process:
positions, spoiler model, copyright caps, checklist) - read it before adding
characters/recaps by hand or with an agent. The four exemplar series (First Law,
Scholomance, Lord of the Rings, Magic Faraway Tree) are the worked reference.

## Package layout

```
cmd/metacheck|metafmt|metabuild   thin CLIs; logic lives in internal/
cmd/metaserve       thin CLI: the read-only HTTP API server (flag wiring only)
cmd/metaimport      thin CLI: ingest an external library export into data/ (openaudible)
cmd/metaextract     thin CLI: epub -> chapter text + manifest (split), n-gram no-verbatim check (ngram); see EXTRACTION.md
cmd/metascan        thin CLI: scan a local audiobook folder -> import JSON (flag wiring only)
internal/model      entity structs, slug/shard rules, location parsing
internal/canonical  canonical JSON (sorted keys, 2-space, trailing LF)
internal/check      schema validation + integrity/uniqueness/chapter/series rules
internal/extract    epub split (container/OPF/spine/toc -> plain text) + the word-shingle overlap check
internal/importer   OpenAudible books.json -> work/recording/person/series, ASIN-dedup, canonical writes
internal/build      SQLite builder (deterministic, FTS5 search_fts, asin/isbn indexes, added_at)
internal/serve      the API server: snapshot loader, JSON handlers, FTS search, GitHub-release poller/hot-swap
internal/scan       local folder scanner: embedded tags + path/filename heuristics + ffprobe -> the "audiosilo-folder-scan" import doc (per-field provenance, omit-never-guess)
schema/             JSON Schemas (the contract), embedded via schema.go
data/               the database (works/recordings/people/series + per-work characters/recaps sidecars)
Dockerfile          image: site build + metaserve + baked data
.github/            issue forms (machine-parseable ids), check + release + image workflows
```

**The API server (`internal/serve`)** opens the SQLite artifact read-only and
serves JSON under `/api/v1` (stats, `search?q=`, `works/latest`, `works/{id}`,
recording `chapters`, `people/{id}`, `series/{id}`, `lookup?asin=|isbn=`) plus
`/healthz`; it can also serve a static site at `/`. It never writes: all data is
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
FTS queries are built defensively (`ftsQuery`: every token quoted + escaped,
final token prefixed with `*`) so no user input can break the MATCH. Business
logic stays in `internal/serve`; `cmd/metaserve` is flag wiring only.

The importer maps one export entry to a work + recording (+ people + series),
importing **factual fields only** (LICENSING.md): it drops publisher
copy/genre/ratings/personal state, deduplicates by ASIN against the catalogue,
and writes canonical files, then runs `internal/check`. Identity rules: a
**person slug is the identity** (spelling/diacritic variants of one name merge
into the existing record; no numbered duplicates), and trailing Audible credit
qualifiers (`"J. Kharkova - translator"`) are stripped from names against a
fixed role list - the person stays in the credit list; a **work** is (title slug
+ author set), but series volumes that share a `title_short` (or a book mapping
onto an existing work at a different position in the same series) derive their
work from the **full title** instead, so distinct volumes never merge - a batch
pre-pass (grouping by title slug only, since Audible's author field varies per
volume) detects this before any slug is claimed; different-author title
collisions get an author suffix (numeric only as last resort); series collide to
numeric. Series positions accept omnibus ranges (`"1-3.5"`) and
`recording.abridged` is optional (emitted only when the source states it) - see
the schema notes below.

## Conventions

- **Facts only, never fabricated.** Seed/contributed data is real and
  verifiable; if a fact can't be verified, omit the (optional) field rather
  than guess. No publisher blurbs, no cover files - covers are URLs,
  descriptions are community-written (see LICENSING.md).
- **Every rule ships with a test.** metacheck rules have a passing fixture and
  a violating fixture; `internal/canonical` has a real-data test so the seed
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
- **Phase 1**: Open Library/Wikidata crosswalk seeding, the Go API server
  (consumes the release artifact, FTS search, `/lookup?asin=|isbn=`), Docker,
  meta.audiosilo.app site with search + import (OpenAudible/Libation) +
  ASIN lookup assist, issue-form-to-PR automation. The **OpenAudible importer**
  (`cmd/metaimport openaudible`, `internal/importer`) has landed. The **API
  server** (`cmd/metaserve`, `internal/serve`) + **Docker image** (`Dockerfile`,
  `image.yml`) have landed, as has works `added_at` (metabuild `--added`, git
  history-derived in `release.yml`). Remaining: the meta.audiosilo.app site,
  crosswalk seeding, Libation, and per-title ASIN lookup.
- **Phase 1.5 (integration landed)**: AudioSilo server/player integration, before
  any ABS facade. audiosilo-server's `GET /libraries/{id}/meta` composes a book's
  enrichment from this project's `metaserve` (`lookup` -> `works/{id}` -> `series`)
  behind an admin off-switch and a cache, and the player renders it capability-gated.
  Consuming `metaserve`'s response shapes now makes them a three-repo contract
  (audiosilo-meta -> audiosilo-server `internal/meta` -> the player only if the
  server's outward envelope changes; see workspace CROSS-REPO.md §17).
- **Phase 2**: characters and recaps (spoiler-tagged, position-keyed), the CC
  BY-SA layer, under the copyright rules in META-FEASIBILITY.md §7. The
  **schema + metacheck rules, the `metabuild`/`metaserve` wiring, and four
  fully-worked exemplar series have landed** (per-work `characters.json`/
  `recaps.json` sidecars, `$defs/position`, the `$defs/license_content`
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
  should carry the role.
- **Phase 3 (pipeline landed)**: the epub -> characters/recaps extraction
  pipeline: `cmd/metaextract` (epub split + n-gram check) plus the documented
  agent process in **[EXTRACTION.md](EXTRACTION.md)** (AUTHORING.md's sibling:
  rolling fact pass -> notes-only synthesis -> adversarial spoiler audit),
  validated end-to-end on Killing Floor (Jack Reacher #1). The book text
  never enters the repo; only the derived CC BY-SA sidecars are committed.
  Possible follow-ups: richer format support (non-epub sources), a
  friendlier packaged client (audiosilo-manager).
