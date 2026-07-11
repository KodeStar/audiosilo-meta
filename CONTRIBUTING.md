# Contributing

Thank you for helping build an open audiobook metadata database. This guide
covers the data model, the two ways to contribute, and the rules every
submission must follow.

Before you start, please read [LICENSING.md](LICENSING.md) - every data
contribution is dedicated to the public domain (CC0-1.0).

## The data model in plain words

Four kinds of thing live in `data/`:

- **Work** - the *abstract book*: its title, subtitle, authors, language, and
  series membership. A work does not have a narrator or a runtime, because those
  belong to a specific narration.
- **Recording** - a *specific narration* of a work: its narrators, whether it is
  abridged, its runtime, release date, publisher, region-scoped ASINs, ISBNs,
  cover URL, and chapters. One work can have many recordings.
- **Person** - a human, shared across roles. The same `people/` entity is
  referenced by a work as an **author** and by a recording as a **narrator**. A
  person can be both.
- **Series** - a named series with an ordered list of member works. Positions are
  strings, so half-numbered entries like `"2.5"` work.

### The Harry Potter example

*Harry Potter and the Philosopher's Stone* is **one work**. It has **two
recordings**: one narrated by **Stephen Fry**, one narrated by **Jim Dale**, each
with its own ASINs (and, being sold in different regions, its own region-scoped
ASIN list). J.K. Rowling is a **person** referenced as the work's author; Stephen
Fry and Jim Dale are **people** referenced as narrators on their respective
recordings. The work belongs to the **Harry Potter series** at position `"1"`.

This is the whole point of the project: narrators and recordings are first-class
data, which no existing open database has.

## Where files live (and the shard rule)

```
data/
  works/<shard>/<work-slug>/work.json
  works/<shard>/<work-slug>/recordings/<recording-slug>.json
  people/<shard>/<slug>.json
  series/<shard>/<slug>.json
```

- **Slugs** are lowercase, hyphen-separated: `^[a-z0-9]+(-[a-z0-9]+)*$`
  (for example `harry-potter-and-the-philosophers-stone-j-k-rowling`).
- **Shard** is the **first two characters of the slug**. So a work slugged
  `ha...` lives under `data/works/ha/`, a person slugged `st...` under
  `data/people/st/`. Sharding keeps directories small and diffs clean.
- Every entity has a `license: "CC0-1.0"` field and a `sources[]` array
  (`{type, ref?, imported_at?}`) recording where the facts came from.

The exact required fields are defined by the JSON Schemas in `schema/*.schema.json` -
those are authoritative. `metacheck` validates against them.

## Two ways to contribute

### (a) Issue forms - no JSON required

If you would rather not edit JSON, open an issue and pick a form:

- **Add a work** - a new book plus its first recording.
- **Add a recording** - another narration of a work that already exists.
- **Correct data** - fix a field on an existing record.
- **Import a library** - attach an OpenAudible `books.json` or a Libation export
  and let us ingest the factual fields.

The forms are structured (every field is captured), and a maintainer or tool
turns your submission into a proper pull request. Each form carries the CC0
confirmation checkbox.

### (b) Direct pull requests

If you are comfortable with JSON and Git, edit the files directly. Walk-through
for adding a new work with its first recording and a new author:

1. **Add the person** (if the author is not already in `data/people/`). Slug them
   (for example `j-k-rowling`), pick the shard (`data/people/j-/`... note: the
   shard is the first two characters, so `j-`), and write
   `data/people/j-/j-k-rowling.json` with their name, `license`, and `sources[]`.
2. **Add the work.** Create
   `data/works/<shard>/<work-slug>/work.json` with the title, authors (referencing
   the person slug), language, and series if any. The shard is the first two
   characters of the work slug.
3. **Add the recording.** Create
   `data/works/<shard>/<work-slug>/recordings/<recording-slug>.json` with the
   narrators (referencing person slugs), abridged flag, runtime, release date,
   publisher, ASINs (`{region, asin}`), ISBNs, cover URL, and chapters.
4. **Add the series** (if new), or add this work to an existing series' `works[]`
   list with a string position.
5. **Format and validate before pushing:**

   ```sh
   go run ./cmd/metafmt --write    # canonicalise JSON (sorted keys, 2-space indent)
   go run ./cmd/metacheck          # schema + referential integrity + uniqueness
   ```

   Fix anything `metacheck` reports, then commit and open a pull request.

Reference the entity model above and copy the shape of an existing record of the
same kind - that is the fastest way to get the fields right.

## Importing an OpenAudible export

If you have an [OpenAudible](https://openaudible.org/) library, the `metaimport`
tool turns its `books.json` export into work/recording/person/series records so
your whole shelf becomes one reviewable pull request:

```sh
go run ./cmd/metaimport openaudible <path-to/books.json> --data data --dry-run
go run ./cmd/metaimport openaudible <path-to/books.json> --data data
```

- `--dry-run` prints the plan (how many works, recordings, people, and series
  would be created, how many books were skipped as already present, and any
  warnings) **without writing anything**. Run it first.
- A real run writes the new and changed files, then validates the whole tree and
  reports. It only fails on an I/O/parse error or if validation fails; per-book
  warnings (a title skipped for a missing narrator, an unknown language, odd
  chapter offsets) are informational.
- `--date YYYY-MM-DD` sets the `imported_at` stamp on every created record
  (defaults to today, UTC).

**Only factual fields are imported.** Titles, authors, narrators, series order
and position, runtime, region-scoped ASIN, publisher, cover URL, and chapter
titles/timestamps come across; the publisher `description`/`summary`, `genre`,
ratings, and your personal library state are dropped (see
[LICENSING.md](LICENSING.md)). The importer **deduplicates by ASIN** against the
existing catalogue, so re-running it, or importing a book someone else already
added, is a no-op rather than a duplicate.

After importing, format and validate as usual (`go run ./cmd/metafmt --write`
then `go run ./cmd/metacheck`), review the diff, and open a pull request. You are
still dedicating the imported facts to the public domain under CC0-1.0, so import
only from a library you may share this way.

## Data rules

- **Factual data only.** Titles, authors, narrators, series order, runtimes,
  chapter titles and timestamps, identifiers. Facts are not copyrightable; this
  is what we collect.
- **No publisher blurbs.** Do not paste a publisher or retailer synopsis. A
  description, if you add one, must be **your own words**.
- **Covers are URLs only** (`cover_url`). Never commit an image file.
- **`sources[]` is required** on every record - say where the facts came from.
- **CC0 dedication.** By submitting, you dedicate the data to the public domain
  (CC0-1.0) and confirm you may do so. See [LICENSING.md](LICENSING.md).

## The gate

Run this locally before opening a pull request - CI runs the same checks and will
block a red pull request:

```sh
go build ./... && go vet ./... && go test ./... && \
  go run ./cmd/metacheck && go run ./cmd/metafmt --check
```

- `go build` / `go vet` / `go test` - the Go tooling compiles and passes.
- `metacheck` - schema, referential integrity (every author/narrator/series
  reference resolves), and identifier uniqueness.
- `metafmt --check` - canonical JSON (sorted keys, 2-space indent, trailing
  newline). Run `go run ./cmd/metafmt --write` to fix formatting automatically.

You need Go 1.25 or newer. No cgo, no external services - the tooling is pure Go.

## Style

Prose in this repo (docs, descriptions, commit messages) uses hyphens, never em
dashes, and plain British-neutral English.

Questions? Open an issue or a discussion, or join the
[AudioSilo Discord](https://discord.gg/nFFqRbkRn6). Governance and merge policy
are in [GOVERNANCE.md](GOVERNANCE.md).
