# audiosilo-meta

The community audiobook metadata database behind
[meta.audiosilo.app](https://meta.audiosilo.app).

**The GitHub repository is the database.** Metadata lives as plain JSON files, one
per entity, edited by pull request or issue form, validated by Go tooling in CI,
and compiled into a single SQLite artifact that servers consume.

> **Status: Phase 1 shipped, Phase 2 landed.** Governance, schemas, the
> validation/build pipeline, the OpenAudible and Libation importers, the
> read-only **API server** (`metaserve`), the **website** (`site/`, served by
> metaserve at meta.audiosilo.app), the Docker image, the in-browser `/import`
> diff, the **Audiobookshelf** metadata-provider endpoint, and issue-form intake
> automation are all built. The characters/recaps CC BY-SA layer (Phase 2) is
> live across the schema, tooling, and data. Still open: Open Library / Wikidata
> crosswalk seeding. Only claim what exists.

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
`people/{id}`, `series/{id}`, `lookup?asin=|isbn=`, `coverage`,
`coverage/works`, `coverage/series-gaps`, plus `/healthz`.

The coverage endpoints back the site's contribute page. `coverage` returns the
top-line band only - how many works carry characters/recaps/whole-book recap
summaries - so it stays tiny at any catalogue size. `coverage/works?filter=&q=
&limit=&offset=` is the paginated, searchable browser: `filter` is `missing`
(missing any dimension) or `has_characters`/`has_recaps`/`has_recap_summary`; `q`
matches title/author; the response carries a per-filter `available` flag that is
false when the dimension is not evaluable at the artifact's schema version.
`coverage/series-gaps?q=&limit=&offset=` is the paginated, name-searchable list
of series with interior position gaps.

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

For immediate production refreshes, set `METASERVE_WEBHOOK_SECRET` to a random
value of at least 32 bytes while keeping `--poll` enabled. This registers
`POST /hooks/github/release`, authenticated with the standard
`X-Hub-Signature-256: sha256=...` HMAC header. The release workflow calls the
endpoint only after every release asset has uploaded, then metaserve re-queries
GitHub, verifies the published checksums, and uses the same atomic hot-swap path
as polling. The webhook never trusts or installs data from its request body.
Hourly polling stays enabled as a fallback for a missed delivery.

Configure the deployment and GitHub repository with the same secret:

1. Generate a secret, for example with `openssl rand -hex 32`, and expose it to
   the container as `METASERVE_WEBHOOK_SECRET`.
2. Add the Actions secret `METASERVE_WEBHOOK_SECRET` with that value.
3. Add the Actions secret `METASERVE_WEBHOOK_URL` with the public endpoint, for
   example `https://meta.audiosilo.app/hooks/github/release`.

The endpoint is not registered when the deployment secret is absent. A missing
workflow configuration or failed delivery is non-fatal because the fallback
poller will still discover the release.

### Docker

The image bundles the server, a baked copy of the current data, and the static
site (built from `site/`). It serves the baked artifact immediately, accepts
signed release refreshes when configured, and polls as a fallback.

```sh
docker build -t audiosilo-meta .
docker run -p 8080:8080 -v audiosilo-meta-cache:/data ghcr.io/kodestar/audiosilo-meta:latest
```

For a real deployment use the committed [`docker-compose.yml`](docker-compose.yml):
loopback-only port (a TLS reverse proxy such as nginx/Ploi fronts it), a named
volume for the release-download cache, an optional signed release webhook, and
hourly fallback polling. The image entrypoint carries all required flags;
`command:` appends extras (for example `command: ["--interval", "15m"]`).
Health check endpoint: `/healthz`.

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

## Audiobookshelf

`metaserve` doubles as an **Audiobookshelf custom metadata provider**. An ABS
admin adds it under **Settings -> Item Metadata Utils -> Custom Metadata
Providers** with the URL `https://meta.audiosilo.app/abs` and no authentication
(the data is public); it needs ABS **v2.8.0 or newer**. Once added it appears in
each book's **Match** / **Quick Match** picker (it is not used by the background
scanner), for book libraries only.

The endpoint is `GET /abs/search`. ABS sends `?mediaType=book&query=<title>`
(with optional `&author=` and `&isbn=`) and never an ASIN; the server returns
`{"matches": [...]}` with one entry per **recording**, up to 10 - an exact ISBN
lookup first, otherwise an FTS title search with loose author boosting. Each
match carries title, subtitle, author, narrator, publisher, publishedYear,
description, cover, ISBN, ASIN, series + sequence, language, and duration (in
minutes). Genres and tags are deliberately never returned - the data model does
not carry publisher genres/tags. The [`/audiobookshelf`](https://meta.audiosilo.app/audiobookshelf)
site page walks through both directions (adding the provider, and exporting an
ABS library into `/import`).

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
| Data - factual core (works, recordings, people, series) | **CC0-1.0** public domain dedication |
| Data - derived layer (characters and recaps) | **CC BY-SA 3.0** |

Publisher blurbs and cover art are referenced, never copied. Full policy,
including the takedown / rightsholder opt-out channel, in
[LICENSING.md](LICENSING.md).

## Roadmap

- **Phase 0** (done) - governance, schemas, CI validation, the SQLite builder, and
  hand-curated records that prove the pipeline (including multi-recording works).
- **Phase 1** - the read-only Go API server (`metaserve`, FTS search + ASIN/ISBN
  lookup), the website (`site/` - search-first landing, stats, latest additions,
  work/person/series pages), the Docker image, the OpenAudible and Libation
  importers, the public deployment (meta.audiosilo.app), the in-browser import
  page (`/import` - parses an OpenAudible export, a Libation export, an
  Audiobookshelf library export, or a metascan folder scan client-side and diffs
  it against the live catalogue), and **issue-form intake automation** (a data
  issue form becomes a validated bot pull request - see
  [GOVERNANCE.md](GOVERNANCE.md)) have **landed**. Still planned: seeding from
  Open Library / Wikidata identifier crosswalks.
- **Phase 1.5 - AudioSilo player integration** - the AudioSilo server
  (`GET /libraries/{id}/meta`) and player surface enriched metadata from this
  database, capability-gated and behind an admin off-switch. This priority
  integration has **landed**, and the **Audiobookshelf metadata-provider facade**
  (`GET /abs/search`, see above) has now shipped on top of it.
- **Phase 2** - community-authored characters and recaps under strict length and
  originality rules, in a separately-tagged CC BY-SA layer, have **landed**: the
  schema enforces the layer structurally, `metacheck` validates it,
  `metabuild`/`metaserve` ship it, and the seed tree already carries
  characters/recaps sidecars. Authoring guide: [AUTHORING.md](AUTHORING.md).
- **Phase 3+** - the source-to-sidecar extraction tooling (`metaextract` plus the
  documented agent process) has **landed**; deeper player rendering gated by the
  listener's progress position is in progress.

Community: questions, contribution help, and coordination happen on the
[AudioSilo Discord](https://discord.gg/nFFqRbkRn6) and in GitHub Discussions.

Design basis: the workspace feasibility study (`../META-FEASIBILITY.md`).
