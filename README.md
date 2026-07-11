# audiosilo-meta

The community audiobook metadata database behind
[meta.audiosilo.app](https://meta.audiosilo.app) *(planned, Phase 1)*.

**The GitHub repository is the database.** Metadata lives as plain JSON files, one
per entity, edited by pull request or issue form, validated by Go tooling in CI,
and compiled into a single SQLite artifact that servers consume.

> **Status: Phase 0** - governance, schemas, and the validation/build pipeline.
> The public API, the website, and the import tooling are **planned (Phase 1)** and
> are not built yet. Only claim what exists.

## Why this exists

No existing open database is both **audiobook-specific** and
**community-editable**:

- Open Library, Wikidata and Inventaire are open (CC0) but have essentially no
  audiobook structure - no narrator field of substance, no chapters, no runtime.
- BookBrainz has no audiobook format and no narrator field.
- Audnexus (the backend Audiobookshelf and Plex read from) is a read-only cache
  over Audible's own catalogue; its own README says it exists only "during the
  interim of waiting for a community driven audiobook database."

So narrators, specific recordings, and chapters are treated here as
**first-class data** - the fields every other database lacks, and the reason this
project exists.

## How the pipeline works

```
  contributor
      │  pull request  /  issue form
      ▼
  data/*.json  ──►  CI validation  ──►  meta.sqlite  ──►  API servers
  (the database)    (metacheck +        (release          (read-only;
                     metafmt, Go)        artifact)          planned Phase 1)
```

1. Contributors add or edit JSON files in `data/` (by pull request, or via issue
   forms that become pull requests).
2. CI validates every pull request - schema, canonical formatting, referential
   integrity, and identifier uniqueness. A red pull request never merges.
3. On merge to `main`, a release workflow builds `meta.sqlite` and attaches it
   (gzipped, with a checksum) to a dated GitHub Release.
4. Servers download that artifact and serve a read-only API. All writes go
   through GitHub; there are no server-side accounts.

## The data model

- **Work** - the abstract book (title, authors, language, series membership).
- **Recording** - a specific narration of a work (narrators, abridged flag,
  runtime, release date, publisher, region-scoped ASINs, ISBNs, cover URL,
  chapters). One work, many recordings.
- **Person** - a human shared across roles (author on works, narrator on
  recordings; can be both).
- **Series** - a named series with an ordered list of member works (string
  positions such as `"2.5"`).

*Harry Potter and the Philosopher's Stone* is one **work** with two
**recordings** - Stephen Fry and Jim Dale - each carrying its own ASINs. That is
the shape this project is built around. Full details in
[CONTRIBUTING.md](CONTRIBUTING.md).

## Repository layout

```
data/          the database: works/, recordings/, people/, series/ (sharded JSON)
schema/        JSON Schemas (one per entity) - authoritative field definitions
cmd/           Go tooling: metacheck (validate), metafmt (canonicalise), metabuild (SQLite)
internal/      shared Go packages behind the tooling
.github/       issue forms + CI workflows (check, release)
CONTRIBUTING.md  GOVERNANCE.md  LICENSING.md
```

## Quickstart

Requires **Go 1.25+** (pure Go, no cgo, no external services).

```sh
git clone https://github.com/kodestar/audiosilo-meta
cd audiosilo-meta

# The gate - run before opening a pull request:
go build ./... && go vet ./... && go test ./... && \
  go run ./cmd/metacheck && go run ./cmd/metafmt --check

# Build the SQLite artifact locally (validates first):
go run ./cmd/metabuild -o meta.sqlite
```

- `metacheck` - schema, referential integrity, and uniqueness validation.
- `metafmt --check` / `--write` - canonical JSON (sorted keys, 2-space indent,
  trailing newline).
- `metabuild -o meta.sqlite` - compile the database into a SQLite file.

## Contributing

New contributions are welcome - by direct pull request or by issue form (no JSON
required). Start with [CONTRIBUTING.md](CONTRIBUTING.md); the merge policy and
trust tiers are in [GOVERNANCE.md](GOVERNANCE.md).

Data rules in brief: factual fields only, no publisher blurbs, covers as URLs,
own-words descriptions, `sources[]` on every record, and a CC0 dedication for
every submission.

## Licensing

| What | Licence |
|---|---|
| Code (tooling, schemas, CI, future server) | **AGPL-3.0-only** ([`LICENSE`](LICENSE)) |
| Data - factual core (all current data) | **CC0-1.0** public domain dedication |
| Data - derived layer (reserved, not yet accepted) | **CC BY-SA 3.0** |

Publisher blurbs and cover art are referenced, never copied. Full policy,
including the takedown / rightsholder opt-out channel, in
[LICENSING.md](LICENSING.md).

## Roadmap

- **Phase 0** (now) - governance, schemas, CI validation, the SQLite builder, and
  hand-curated records that prove the pipeline (including multi-recording works).
- **Phase 1** *(planned)* - seed from Open Library / Wikidata identifier
  crosswalks, a read-only Go API server, meta.audiosilo.app with search and
  lookup assist, the OpenAudible / Libation import page, and live issue forms.
- **Phase 1.5 - AudioSilo player integration** *(planned, the priority
  integration)* - the AudioSilo server and player surface enriched metadata from
  this database. This is a defining product feature and ships before any
  Audiobookshelf-provider facade.
- **Phase 2** *(planned)* - community-authored characters and recaps under strict
  length and originality rules, in a separately-tagged CC BY-SA layer.
- **Phase 3+** *(planned)* - extraction tooling (spoiler-tagged character and
  recap data), and deeper player integration gated by the listener's progress
  position.

Design basis: the workspace feasibility study (`../META-FEASIBILITY.md`).
