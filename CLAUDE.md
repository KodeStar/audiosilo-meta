# CLAUDE.md - AudioSilo Meta

Guidance for working in this repository. Keep it SHORT: this file is loaded into
every session, so it states what you need to work safely and points at where the
detail lives. Rationale, measurements and history belong in package/type doc
comments (which already carry them) and in git history, not here.

This is the sixth repo in the AudioSilo workspace (`~/dev/audiosilo`) - read the
workspace [CLAUDE.md](../CLAUDE.md) and [META-FEASIBILITY.md](../META-FEASIBILITY.md)
first.

## What this is

An open, community-editable audiobook metadata database, served at
meta.audiosilo.app. The GitHub repository IS the database: JSON pack files
([PACK-SPEC.md](PACK-SPEC.md)), contributed via PRs and issue forms, validated by
Go tooling in CI, compiled into a SQLite artifact published as a GitHub Release.
`metaserve` serves that artifact (JSON API, Audiobookshelf provider facade, HTML
entity pages, sitemaps, watch feeds) plus the Astro site in `site/`. The AudioSilo
player (via audiosilo-server's `/meta`) is the priority consumer.

Module `github.com/kodestar/audiosilo-meta`. Code AGPL-3.0; data CC0-1.0 (core)
plus a CC BY-SA 4.0 community layer. [LICENSING.md](LICENSING.md) is load-bearing -
read it before touching data handling. Merge policy and trust tiers:
[GOVERNANCE.md](GOVERNANCE.md).

**Two repositories (since 2026-08-21).** This repo is the CC0 core: works, people,
series and `data/redirects.json` (tree profile `core`). The CC BY-SA
`works-community` family (characters, recaps, description) lives in
[`KodeStar/audiosilo-meta-community`](https://github.com/KodeStar/audiosilo-meta-community),
along with AUTHORING.md / EXTRACTION*.md (stubs here). All TOOLING stays here; the
community repo consumes it. `release.yml` checks out both and
`metabuild -data data --community <dir>` composes ONE artifact. A community merge
triggers a release here via a `community-data` repository_dispatch. Migration plan:
untracked `.claude/community/PLAN.md`.

## Model routing (every session follows this)

- **Fable (main session) orchestrates only**: decomposition, design taste, final
  QA. Never writes feature code; may write orchestration artifacts (this file,
  briefs, governance docs, commit messages). High effort.
- **Opus subagents implement**, one per task, parallel when disjoint. Each gets a
  self-contained brief and must leave the gate green.
- **Token-hungry chores go to cheaper models** (Sonnet/Haiku): research, bulk
  data sweeps, log triage.

## Build / test / gate

```sh
cd ~/dev/audiosilo/audiosilo-meta
go build ./... && go vet ./... && go test -race ./... && golangci-lint run   # ~3min
go run ./cmd/metacheck --profile core        # validate the data tree (~10s)
go run ./cmd/metafmt --check --profile core  # canonical formatting (--write to fix)
go run ./cmd/metabuild -o meta.sqlite        # core-only artifact
go run ./cmd/metabuild -data data --community <community>/data -o meta.sqlite  # the real artifact
go run ./cmd/metaserve --db meta.sqlite --addr :8080
go run ./cmd/metadiff --base <sha> --head <sha>   # entry-level summary of a data change
go run ./cmd/metaaudit -data data -o audit-report # read-only data-quality audit (~30s)
go run ./cmd/metarepair -data data --community ../audiosilo-meta-community/data \
  --op merge-works --limit 50                     # DRY RUN of a repair wave
```

**All of the above must pass before a change is done.** Go 1.25, golangci-lint v2
at a green baseline. pkg/check's real-data test skips under `-race` (too slow);
the fixture suites cover the parallel loader.

**Tree profiles** (`--profile all|core|community`, `pack.Profile`) name which
families a root holds. The default differs by tool, set by the cost of being wrong:
- metacheck / metafmt / metaissue default to `all`; CI passes `--profile core`
  explicitly everywhere (check.yml, intake bot, rebase sweep, release.yml).
- metaaudit / metarepair default to `core` (metarepair deletes records). Both must
  always be given the SAME profile.
- A `data/works-community/` reappearing here is a red metacheck (unrecognized
  location) - that is the profile's teeth.

**`metarepair` needs `--community <community-checkout>/data` for merge waves.**
Without it every merge-works proposal is refused `community-data-required`, because
the sidecar-collision guard can't be answered without the CC BY-SA layer. The root
is opened read-only; sidecar members ride the slug tombstone until the community
repo's re-key sweep lands.

`metaaudit` and `metarepair` are NOT in the gate. The audit is a review queue;
metarepair is dry-run by default, and `--write` is a data change with its own PR.
It re-runs the audit in process and applies only what the fresh run still proposes
as non-advisory (see `internal/repair`).

**Duplicate PREVENTION is in the gate**, three layers over one rule
(`titlerule.IdentityTitleKey`, via `check.WorkIdentity` on `Result.Identity`):
the intake bot refuses a differently-spelled duplicate
(`internal/issueform/dupidentity.go`), the importer's create path skips one to the
`--conflicts` worklist (`internal/importer/dupidentity.go`; never auto-merges), and
metacheck reports remaining collisions as the advisory `normalized-duplicate-works`
(never fails).

**CI**: `check.yml` gates build/vet/test/metacheck/metafmt on every PR and push to
main, plus a **compose** job (metabuild over a shallow clone of the community repo)
because cross-tree rules (a sidecar keyed by a work this core no longer holds) can't
be checked from one root. `release.yml` publishes `data-vYYYY.MM.DD-<core7>-<community7>`
when data, schema, `internal/build/**`, `cmd/metabuild/**`, `pkg/check/**` or
`pkg/pack/**` change on main, or on a `community-data` dispatch; an existing tag
skips. Assets: `meta.sqlite.gz` + `.sha256`, `meta.sqlite.sha256`, and a
best-effort zstd `meta.sqlite.patch.from-<PREV_TAG>.zst` (`--long=31`). Consumers
select the data release by ASSET PRESENCE at max `published_at` (code `v*`
releases carry no data assets; list order is not chronological). Serialized on the
`data-release` concurrency group. After upload it sends a signed webhook to
metaserve (optional secrets; polling is the fallback).

## The data model (the contract)

JSON Schemas in `schema/*.schema.json` (draft 2020-12, `additionalProperties:
false`) are the public contract, embedded via `schema.go` - schema edits are code
changes with tests.

- **Slug is identity** (`^[a-z0-9]+(-[a-z0-9]+)*$`); the file a record sits in is
  storage, not address.
- **Reserved slugs**: `search` and `latest` in works/people/series
  (`pkg/model/reserved.go`) - they are API route literals. Every minter steps off
  them by its family's collision convention; see the file for the rules.
- **Retired slugs keep resolving** via `data/redirects.json` (`pkg/redirects`,
  canonical statement on `model.Redirects`). Not a family; one-hop only (chains
  collapsed on write, refused by `pkg/check`). metaserve answers a retired slug with
  301. Open gaps (no merge driver for the file; minters don't step off tombstoned
  slugs) are documented on `pkg/redirects`.
- **Storage is range-packed** (`pkg/pack`, PACK-SPEC.md): `{"entries": {"<slug>":
  {...}}}` files, each covering `[own bound, next bound)`.
  ```
  data/works/<dir-bound>/<bound>.json   work composites (work + its recordings)
  data/people/<bound>.json
  data/series/<bound>.json
  data/redirects.json                   slug tombstones (no pack)
  (community repo) data/works-community/<dir-bound>/<bound>.json
  ```
  Caps: 256KB target / 512KB hard / 1,000 entries (works-community 200) / 512 packs
  per dir; single-entry exemption. **Never compute placement by hand**:
  `metafmt --write` relocates, dedups, splits, rebinds and re-renders. Pack
  conflicts resolve via `scripts/pack-union-merge.sh` (three-way entry merge; also a
  git merge driver per `.gitattributes`, which the intake sweep configures) - git's
  line merge is unsound on packs. The retired file-per-record layout is refused
  everywhere (`cmd/metamigrate` converted it).

**Entities** (field detail is in the schemas and `pkg/model`):
- **work** - title, authors (person ids, the identity list), language,
  first_published, xrefs, optional sorted `genres` (project-owned enum, never a
  retailer taxonomy), optional `credits` (`{person, role}`, role enum; additive to
  authors; sorted by importer, order not checked), plus nested `recordings`.
- **recording** - narrators, optional `abridged` (absent = unknown, never guess),
  runtime_min, release_date, publisher, region-scoped `asin[]`, `isbn[]` (bare
  string = region unstated; `{isbn, region}` only when known), optional
  `publishers[]` for other regions' imprints, cover_url, chapters. One production
  across marketplaces is ONE recording. Retailer availability is not stored;
  `purchase_links[]` are derived at serve time.
- **person** - shared by authors and narrators; optional `kind`
  (person/group/publisher/synthetic; absence = person, never inferred).
  `virtual-voice` is the one synthetic record all AI narration folds onto.
  A person id must equal `model.PersonSlug(name)`.
- **series** - name + works with STRING positions ("1", "2.5", "1-3.5"); no shared
  positions.
- **community layer** (other repo): per-work `characters` (spoiler-gated by
  `reveal`), `recaps` (position-keyed, plus `in_short`/`ending`), `description`
  (spoiler-free, 200-1500 chars; distinct from the work's CC0 `description`).
- **Position** = `{ "chapter": <int >= 0> }`, edition-independent (0 = front matter).
- `added_at` on works/recordings is stamped at creation only; metabuild falls back
  to newest `sources[].imported_at`.
- Every entity carries `license` and `sources[]`. The license boundary is a schema
  enum: core is `CC0-1.0`, sidecars `CC-BY-SA-4.0`.

**Artifact schema_version** (bump = what a reader may SELECT; adding an index does
not bump it): 2 characters/recaps, 3 recap_summaries, 4 work_genres, 5 redirects,
6 work_descriptions. metaserve gates each on the version and degrades to "no data";
an artifact claiming 5/6 without the table fails to open.

## Package layout

Thin CLIs in `cmd/` (flag wiring only); logic in `internal/` and public `pkg/`.
Each package's doc comment is the authoritative description.

- `cmd/metacheck|metafmt|metabuild|metaserve|metaissue|metaimport|metascan|metaextract|metadiff|metaaudit|metarepair` - see Build above. `metamigrate`/`metaremediate` are spent one-offs.
- `pkg/model` - entity structs, `Slugify`/`PersonSlug`, reserved slugs, trust tiers (`trust.go`, the only place source types are compared), `Redirects`.
- `pkg/canonical` - canonical JSON (sorted keys, 2-space, trailing LF).
- `pkg/pack` - pack storage, bound math, `Store` (plans every family before writing; refuses misplaced entries), `Listing` (one walk; file accounting), profiles.
- `pkg/check` - load + schema validation + all metacheck rules; parallel per-pack loader, deterministic merge; `LoadProfile`, `LoadComposed` (cross-tree rules: existence, retired-key re-key with advisory, collision, position-scale); `WorkIdentity`; `AdvisoryClass`. `sidecarRefs` is the one list of sidecar kinds (drift-guarded).
- `pkg/redirects` - tombstone table read/write, chain-collapsing `Add`.
- `pkg/extract` - epub split + n-gram no-verbatim check (`expressiveFields` is the source of truth for scanned fields).
- `pkg/scan` - local folder scanner -> import doc.
- `internal/importer` - OpenAudible/Libation/libex/audiosilo-books -> records; create / `--enrich` / `--recordings-only` modes; credit-name cleaning (`CleanCreditName`), collective and synthetic folds, AI-credit and unidentifiable-credit refusals, trust-tier attestation (`attest.go`), conflict worklist, series-position lookup/arbitration (`seriespos.go`).
- `internal/issueform` - issue form -> records + verdict (ok/duplicate/needs-human/invalid); profile-aware (the community repo runs it too); `worksdb.go` resolves sidecar work keys against the release artifact.
- `internal/format` - metafmt logic, profile-scoped.
- `internal/titlerule` - leaf of title/series/name rules (`Clean`, `IdentityTitleKey`, `StatedVolume`, ranks). `match.go` is a documented copy of audiosilo-server's pkg/match.
- `internal/audit` - read-only, deterministic audit; typed `Proposal` with `Advisory` flag; merge vetoes documented per file.
- `internal/repair` - applies non-advisory proposals; plans before writing; loses no record; tombstones retired slugs; refuses on a red or dirty tree. `Categories()` is the refusal taxonomy.
- `internal/rawentry` - raw-member entry editor + by-value union rules.
- `internal/recorddiff` - metadiff logic (entry-level, raw-bytes-first, `--no-renames`, base read at merge base, one record per line).
- `internal/reportdir` - shared report-directory plumbing.
- `internal/build` - deterministic SQLite builder; `sources.go` picks `Load` vs `LoadComposed`.
- `internal/serve` - the API server (below).
- `internal/testpack` - test fixtures and record renderers (test-only).
- `internal/migrate`, `internal/remediate` - spent one-offs.
- `schema/`, `data/`, `site/`, `scripts/` (libex SQL flow + README, pack merge driver), `Dockerfile` (no baked data).
- `.github/` - check, release, image, intake (bot PRs + rebase sweep keyed on the `bot-intake` label), ai-verify (feeds `metadiff` output to a model; falls back to raw diff loudly, never passes silently). Four core issue forms; sidecar forms live in the community repo and sidecar labels here refuse-and-redirect.

## The API server (`internal/serve`)

Read-only, no auth, permissive CORS. The full surface is the embedded OpenAPI
document (`internal/serve/openapi.json`, served at `/api/v1/openapi.json`);
`TestOpenAPICoversEveryRoute` keeps it in sync with `Server.routes`.

- **Routes**: `/api/v1` stats, search (combined + `works|people|series/search`),
  `works/latest`, `works/{id}`, recording `chapters`, `people/{id}` (paged),
  `series/{id}` (whole unless `?limit`), `lookup?asin=|isbn=`, watch feeds;
  `/healthz`; `/abs/search` (Audiobookshelf provider); HTML entity pages
  `/works|people|series/{id}` plus `/works/{id}/recap|characters` guide pages
  (only with `--site`); `/sitemap-index.xml` + `/sitemaps/<family>-<n>.xml`.
- **Every query must be indexed**: `TestServeLookupsAreIndexed` EXPLAINs the query
  constants. Batch per-hit reads (`cardsByID`); never a query per item.
- **Search**: FTS built only through `ftsMatch`/`tokenPhrases` (punctuation splits
  terms; all-single-rune tokens stay one phrase). Two query-side boosts on combined
  and works search: series-volume (`seriespos.go`) and exact-title
  (`exacttitle.go`), both cost-gated.
- **Retired slugs** 301 on every id route (API and HTML) via one `redirected`
  helper driven by route data; `TestEveryIDRouteResolvesRetiredSlugs` guards it.
- **HTML pages** inject into built Astro shells at `<!--ssr:...-->` markers, reuse
  the JSON snapshot methods (zero new SQL), embed the API payload for hydration,
  and always degrade to the static shell (never 5xx). Golden-tested.
- **Artifact lifecycle**: atomic snapshot pointer; `--poll` fetches the newest data
  release (patch first, full streamed download fallback, checksum-verified before
  rename), adopts a verified cached file, hot-swaps with a grace period, prunes the
  cache. The image ships no data: boot fetches, falls back to a stale cache, else
  serves 503 with `Retry-After` and backs off. `METASERVE_WEBHOOK_SECRET` enables
  the signed release webhook.
- **Watch feeds** (Atom, JSON Feed, iCalendar): stateless, series list in `s`
  (CSV or `z:` deflate+base64url), one item per work, per-representation cap, ETag
  includes the day. Date rules are hand-mirrored with `site/src/lib/dates.ts`.
- **Hand-mirrored Go/TS twins** (keep in step, both sides tested): date rules,
  `purchaseLabel`/`retailerLabel`, `positionStart`.

**Site `/watching`** is entirely client-side (localStorage via
`site/src/lib/watchlist.ts`); feed URLs from `lib/feed-url.ts`; notify options in
`lib/notify-options.ts` shared with `/docs/notifications`; header badge logic in
`lib/watch-badge.ts`.

## Importer rules to know

Factual fields only (LICENSING.md): no publisher copy, raw retailer genres,
ratings or personal state. Dedup by ASIN; a person slug is the identity; work
identity is title slug + author set with the series-volume and author-suffix
rules in `internal/importer`. Slugs are bounded by `BoundedSlugTail`/
`NumberedSlugAt`. Trust tiers: user-library sources outrank the libex bulk mirror;
a user run overwrites a bulk-mirror-only record, otherwise existing values win and
contradictions are counted and warned, never churned. Rules and measurements are
in each file's header.

The private `KodeStar/audiosilo-meta-sync` bot builds metaimport/metafmt/metacheck
from a PINNED commit of this repo - changing a CLI flag, report shape or selector
rule here means bumping that pin.

## Conventions

- **Facts only, never fabricated.** Omit an unverifiable optional field rather
  than guess. Covers are URLs; descriptions are community-written.
- **Every rule ships with a test** (passing and violating fixture).
- **CI security**: plain `pull_request` only; never `pull_request_target` running
  fork code; privileged follow-ups via `workflow_run` consuming artifacts.
- **Deterministic builds**: sorted-id inserts, byte-identical artifacts.
- Schema/tooling/.github changes need maintainer review (CODEOWNERS).
- **Hyphens, never em dashes**, everywhere.

## Roadmap (open items)

- Repair waves over the audit's duplicate clusters (merges + tombstones); possible
  routing of refused duplicate rows to recordings.go.
- Surfacing work `credits` and genres in the artifact/API/site/player.
- Classifying corporate person records with `kind` (human pass).
- Streaming row iterator for the libex parse layer.
- Open Library/Wikidata crosswalk seeding; per-title ASIN lookup assist.
- Community layer: site render in progress; player render (three-repo seam, see
  workspace CROSS-REPO.md section 17).
