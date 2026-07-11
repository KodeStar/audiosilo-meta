# CLAUDE.md - AudioSilo Meta

Guidance for working in this repository. Keep this file updated as the codebase
evolves. This is the sixth repo in the AudioSilo workspace (`~/dev/audiosilo`) -
read the workspace [CLAUDE.md](../CLAUDE.md) and
[META-FEASIBILITY.md](../META-FEASIBILITY.md) (the research + design basis for
this project) before working here.

## What this is

An **open, community-editable audiobook metadata database** - the data behind
meta.audiosilo.app (planned). The GitHub repository IS the database: one JSON
file per entity, contributed via pull requests and issue forms, validated by Go
tooling in CI, and compiled into a SQLite artifact published as a GitHub
Release. A Go API server (Phase 1) consumes that artifact; the AudioSilo player
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
```

**Before a change is done, all of the above must pass.** CI
(`.github/workflows/check.yml`) gates build/vet/test/metacheck/metafmt on every
PR and push to main; `release.yml` builds and publishes `meta.sqlite.gz` (+
sha256) as a dated release (`data-vYYYY.MM.DD-<shortsha>`) when data or schema
changes land on main. Go 1.25; golangci-lint v2 at a green baseline.

## The data model (the contract)

Path is identity; slugs `^[a-z0-9]+(-[a-z0-9]+)*$`; shard dir = first 2 chars
of the slug. JSON Schemas in `schema/*.schema.json` (draft 2020-12,
`additionalProperties: false`) are the public contract - embedded into the
tooling via `schema.go`, so schema edits are code changes with tests.

- **work** `data/works/<shard>/<slug>/work.json` - the abstract book: title,
  authors (person ids), language, first_published, xrefs (wikidata/openlibrary/
  goodreads/print ISBNs).
- **recording** `data/works/<shard>/<slug>/recordings/<rec-slug>.json` - a
  specific narration/production: narrators, abridged, runtime_min,
  release_date, publisher, region-scoped `asin[]`, `isbn[]`, cover_url,
  chapters. One work, many recordings (Harry Potter: Stephen Fry AND Jim Dale,
  each with its own ASINs). The shard is the **parent work's** slug shard.
- **person** `data/people/<shard>/<slug>.json` - shared by author and narrator
  roles (a person can be both).
- **series** `data/series/<shard>/<slug>.json` - name + ordered works with
  **string** positions ("1", "2.5").

Every entity carries `license` (only `CC0-1.0` in Phase 0) and `sources[]`
(provenance: type/ref/imported_at) so any source can be audited or retracted
wholesale.

## Package layout

```
cmd/metacheck|metafmt|metabuild   thin CLIs; logic lives in internal/
internal/model      entity structs, slug/shard rules, location parsing
internal/canonical  canonical JSON (sorted keys, 2-space, trailing LF)
internal/check      schema validation + integrity/uniqueness/chapter/series rules
internal/build      SQLite builder (deterministic, FTS5 search_fts, asin/isbn indexes)
schema/             JSON Schemas (the contract), embedded via schema.go
data/               the database (works/recordings/people/series)
.github/            issue forms (machine-parseable ids), check + release workflows
```

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
  ASIN lookup assist, issue-form-to-PR automation.
- **Phase 1.5**: AudioSilo server/player integration (before any ABS facade).
- **Phase 2**: characters and recaps (spoiler-tagged, position-keyed), the CC
  BY-SA layer, under the copyright rules in META-FEASIBILITY.md §7.
- **Phase 3**: extraction clients (epub -> characters/recaps pipeline,
  likely in audiosilo-manager).
