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
`people/{id}`, `series/{id}`, `lookup?asin=|isbn=`, `coverage`, plus `/healthz`.

The `coverage` endpoint reports expressive-layer coverage - how many works carry
characters/recaps/whole-book recap summaries, the works still missing each, and
per-series integer position gaps - for the site's coverage/wanted page.

Flags: `--db` (local artifact), `--site <dir>` (serve a static site at `/`),
`--poll` (fetch and hot-swap the latest published data release from GitHub),
`--repo` (default `KodeStar/audiosilo-meta`), `--interval` (default `1h`),
`--cache` (download dir). With `--poll` and no `--db`, the server fetches the
newest data release (the newest release carrying `meta.sqlite.gz` - code/image
`v*` releases are skipped) on boot; with both, the baked artifact serves
immediately and the poller runs a refresh at startup (not just once per
`--interval`), so a recreated container catches up to the newest release within
seconds instead of serving build-time data for a full interval. Set
`GITHUB_TOKEN` to raise the API rate limit.

### Docker

The image bundles the server, a baked copy of the current data, and the static
site (built from `site/`). It serves the baked artifact immediately
and polls for newer data releases.

```sh
docker build -t audiosilo-meta .
docker run -p 8080:8080 -v audiosilo-meta-cache:/data ghcr.io/kodestar/audiosilo-meta:latest
```

For a real deployment use the committed [`docker-compose.yml`](docker-compose.yml):
loopback-only port (a TLS reverse proxy such as nginx/Ploi fronts it), a named
volume for the release-download cache, and hourly data polling. The image
entrypoint carries all required flags; `command:` appends extras (for example
`command: ["--interval", "15m"]`). Health check endpoint: `/healthz`.

The `image` workflow builds and pushes `ghcr.io/kodestar/audiosilo-meta` on a
`v*` tag.

## Scanning a local library (metascan)

`metascan` is the low-friction way to contribute a library when you have only
audio files - no OpenAudible or Libation export. Point it at a folder and it
walks the tree, gathers whatever metadata it can find, and writes a JSON file the
meta.audiosilo.app import page accepts.

```sh
go run github.com/kodestar/audiosilo-meta/cmd/metascan@main /path/to/audiobooks -o scan.json
```

Then drop `scan.json` onto **meta.audiosilo.app/import**.

It runs entirely on your machine and **sends nothing anywhere** - it only reads
files. What it gathers, per book:

- **Embedded tags** (via [dhowden/tag](https://github.com/dhowden/tag)): title,
  authors, narrators, and an ASIN when Libation/OpenAudible embedded one.
- **The folder structure**, treated as a first-class source (tags are often
  missing or wrong, series data especially): `Author/Book`,
  `Author/Series/Book`, and name patterns like `01 - Title`, `Book 3 - Title`,
  `Title, Book 3`, and `Jack Reacher 03 - Title` yield author/series/position/
  title.
- **An ASIN** - the anchor that makes a sparse book matchable - hunted in tag
  atoms and in file/folder names (for example `Title [B076HYPQLK]`).
- **Runtime and chapter counts**, if `ffprobe` is on your `PATH`. Without it the
  scan still works; those two fields are simply omitted.

Have `ffprobe` installed if you can: it is also the deeper tag reader. The pure
Go reader covers the common title/author/narrator tags plus MP3 user frames,
but several audiobook-specific fields are reachable only through ffprobe -
Audible/Libation freeform MP4 atoms (ASIN and friends), m4b stream language,
and various container extras. Without ffprobe, embedded series/ASIN extraction
is limited (especially for m4b files); the folder-structure heuristics still
work in full.

Every field records where it came from (`tag` / `path` / `filename`) in the
book's `sources` map, and unknown fields are omitted rather than guessed.
Grouping follows the workspace convention: a folder that directly contains audio
is one book (its files are the parts), and loose files at the scan root are
individual single-file books. One evidence-gated exception: when a folder's
files carry mutually distinct album tags (or distinct, non-generic title tags
that each match their own filename), that is a flat folder of separate
single-file books - the common `Series/01 - A.m4b, 02 - B.m4b` layout - and each
file becomes its own book, with the folder feeding the series/author heuristics.
Without tag evidence the folder is always kept as one book (never split on
filenames alone), and a multi-file folder with no signal either way is counted
in the summary so you know where to check for collections. The JSON goes to
stdout by default (or `-o <file>`); a human-readable summary goes to stderr.
Pass `-ffprobe ""` to skip ffprobe enrichment.

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
  OpenAudible and Libation importers, the public deployment
  (meta.audiosilo.app), and the in-browser import page (`/import` - parses an
  OpenAudible export, a Libation export, or a metascan folder scan client-side
  and diffs it against the live catalogue) have **landed**. Still planned:
  seeding from Open Library / Wikidata identifier crosswalks and issue-form
  automation.
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
