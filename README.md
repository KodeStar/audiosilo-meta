# audiosilo-meta

The community audiobook metadata database behind
[meta.audiosilo.app](https://meta.audiosilo.app) *(planned, Phase 1)*.

**The GitHub repository is the database.** Metadata lives as plain JSON files, one
per entity, edited by pull request or issue form, validated by Go tooling in CI,
and compiled into a single SQLite artifact that servers consume.

> **Status: Phase 1 in progress** - governance, schemas, the validation/build
> pipeline, the OpenAudible importer, the read-only **API server** (`metaserve`),
> the **website** (`site/`, served by metaserve), and the Docker image are
> built. The public deployment at meta.audiosilo.app and the remaining import
> paths are still **planned**. Only claim what exists.

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
  data/*.json  ──►  CI validation  ──►  meta.sqlite  ──►  API server + site
  (the database)    (metacheck +        (release          (metaserve,
                     metafmt, Go)        artifact)          read-only)
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
cmd/           Go tooling: metacheck (validate), metafmt (canonicalise), metabuild (SQLite), metaserve (API server)
internal/      shared Go packages behind the tooling (build, check, serve, ...)
Dockerfile     container image: API server + baked data + static site
.github/       issue forms + CI workflows (check, release, image)
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
  `--added <file>` dates each work from a tab-separated `<ISO8601>\t<work.json
  path>` list (the release workflow derives it from git history); works absent
  from it fall back to the newest `sources[].imported_at`.

## Running the API server

`metaserve` is a read-only JSON API over the compiled artifact - FTS search,
work/person/series detail, chapter lists, and ASIN/ISBN lookup. All data is
public, so there is no auth; the API sends permissive CORS.

```sh
# Dev: build the artifact, then serve it locally.
go run ./cmd/metabuild -o meta.sqlite
go run ./cmd/metaserve --db meta.sqlite --addr :8080

curl localhost:8080/healthz
curl 'localhost:8080/api/v1/search?q=dragon'
curl 'localhost:8080/api/v1/works/latest'
curl 'localhost:8080/api/v1/lookup?asin=B08G9PRS1K'
```

Key endpoints (all under `/api/v1`): `stats`, `search?q=&limit=`,
`works/latest?limit=`, `works/{id}`, `works/{id}/recordings/{rid}/chapters`,
`people/{id}`, `series/{id}`, `lookup?asin=|isbn=`, plus `/healthz`.

Flags: `--db` (local artifact), `--site <dir>` (serve a static site at `/`),
`--poll` (fetch and hot-swap the latest published data release from GitHub),
`--repo` (default `KodeStar/audiosilo-meta`), `--interval` (default `1h`),
`--cache` (download dir). With `--poll` and no `--db`, the server fetches the
latest release on boot; with both, the baked artifact serves immediately and the
poller upgrades in place. Set `GITHUB_TOKEN` to raise the API rate limit.

### Docker

The image bundles the server, a baked copy of the current data, and the static
site (built from `site/`). It serves the baked artifact immediately
and polls for newer data releases.

```sh
docker build -t audiosilo-meta .
docker run -p 8080:8080 -v audiosilo-meta-cache:/data ghcr.io/kodestar/audiosilo-meta:latest
```

The `image` workflow builds and pushes `ghcr.io/kodestar/audiosilo-meta` on a
`v*` tag.

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
- **Phase 1** *(in progress)* - the read-only Go API server (`metaserve`, FTS
  search + ASIN/ISBN lookup), the website (`site/` - search-first landing,
  stats, latest additions, work/person/series pages), the Docker image, and the
  OpenAudible importer have **landed**. Still planned: the public deployment,
  seeding from Open Library / Wikidata identifier crosswalks, the in-browser
  import page, the Libation import path, and issue-form automation.
- **Phase 1.5 - AudioSilo player integration** *(planned, the priority
  integration)* - the AudioSilo server and player surface enriched metadata from
  this database. This is a defining product feature and ships before any
  Audiobookshelf-provider facade.
- **Phase 2** *(planned)* - community-authored characters and recaps under strict
  length and originality rules, in a separately-tagged CC BY-SA layer.
- **Phase 3+** *(planned)* - extraction tooling (spoiler-tagged character and
  recap data), and deeper player integration gated by the listener's progress
  position.

Community: questions, contribution help, and coordination happen on the
[AudioSilo Discord](https://discord.gg/nFFqRbkRn6) and in GitHub Discussions.

Design basis: the workspace feasibility study (`../META-FEASIBILITY.md`).
