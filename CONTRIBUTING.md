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
  belong to a specific narration. It may also carry **credits**: contributors in
  a role that is not authorship (translator, editor, illustrator, introduction
  and the rest of the controlled list in
  `schema/common.schema.json` `$defs/credit_role`), each one a
  `{"person": "<slug>", "role": "<role>"}` pair. Credits are optional and only
  ever recorded when a source *stated* the role; the person also stays in
  `authors` when the source credited them there.
- **Recording** - a *specific narration* of a work: its narrators, whether it is
  abridged, its runtime, release date, publisher, region-scoped ASINs, ISBNs,
  cover URL, and chapters. One work can have many recordings. A production
  released in several marketplaces is still **one** recording - the region rides
  on the identifiers, not on a second record. An entry in `isbn[]` is normally a
  plain string, and becomes `{"isbn": "...", "region": "uk"}` when you know which
  marketplace it identifies; `publishers[]` holds the *other* regions' imprints
  as `{"region": ..., "publisher": ...}` pairs, with the top-level `publisher`
  staying the publisher of record (so it is never repeated in the list, and a
  record with `publishers[]` must have a `publisher`).
  Retailer URLs are not authored data: the API derives speculative,
  non-affiliate Audible and Libro.fm links from recording ASINs and 13-digit
  recording ISBNs. Current availability is volatile and is not stored in Git.
- **Person** - a human, shared across roles. The same `people/` entity is
  referenced by a work as an **author** and by a recording as a **narrator**. A
  person can be both. An optional `kind` (`person` / `group` / `publisher`)
  marks the records that are not an individual - a full cast credited as a unit,
  or a corporate credit of record. Leaving it off is normal: absence reads as
  "an individual, or nobody has classified it", never as a guess.
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

## Where records live (pack files)

Records are stored **many to a file**, in four families of *pack files*. This
repository holds three of them - the CC0 core:

```
data/
  works/<dir>/<bound>.json            each entry = a work, with its recordings nested
  people/<bound>.json                 each entry = a person
  series/<bound>.json                 each entry = a series
  redirects.json                      retired slug -> live slug (not a pack)
```

The fourth, `works-community/` (each entry a work's characters + recaps, CC
BY-SA), lives in
[audiosilo-meta-community](https://github.com/KodeStar/audiosilo-meta-community) -
same layout, same tooling, its own repository and its own issue forms. Everything
below about packs, slugs and merges applies to it identically; the release build
composes both into one artifact.

A pack is one JSON object, `{"entries": {"<slug>": {...}, ...}}`, holding a
slug range: the file name (minus `.json`) is its **lower bound**, and it covers
everything up to the next file's bound. A work's entry carries its recordings as
a `"recordings"` map keyed by recording slug, so one book is one entry.

- **Slugs** are lowercase, hyphen-separated: `^[a-z0-9]+(-[a-z0-9]+)*$`
  (for example `harry-potter-and-the-philosophers-stone-j-k-rowling`). The slug
  is the identity; the file it happens to sit in is not.
- Two slugs are **reserved** in the works, people and series families: `search`
  and `latest`. They are segments of the API's own routes
  (`/api/v1/works/latest`, `/api/v1/works/search` and its siblings), so a record
  stored under one of them can be found by search and then never opened. A book
  really called *Search* takes the same disambiguated slug a title collision
  would give it (`search-alyssa-rose-ivy`), a series called *Latest* takes
  `latest-2`, and a person named Search is `search-person`. `metacheck` says so
  if one slips through.
- Every entity has a `license` field and a `sources[]` array
  (`{type, ref?, imported_at?}`) recording where the facts came from. The core
  families are `CC0-1.0`; `works-community` entries are `CC-BY-SA-4.0`.
- Works and recordings also carry an optional `added_at` - the date the record
  entered the database. The importer and the intake bot stamp it; leave it out
  of a hand-written record and it will simply be absent.
- One file in `data/` is **not** a pack: `data/redirects.json`, the slug
  *tombstone* table (`{"works": {"<retired-slug>": "<live-slug>"}, "people":
  ..., "series": ...}`). When a repair merges two records for one book, the
  retired slug is recorded there and the API answers it with a `301` at the
  surviving record, so a URL or a stored id keeps working. You will not normally
  touch it by hand: `metacheck` requires every source to be a slug the database
  no longer holds, every target to be one it does, and no target to be a
  redirect itself.

**You never have to work out which pack a record goes in.** Add your entry to
roughly the right file - or the wrong one - and run `go run ./cmd/metafmt
--write`: it moves entries to the pack their slug belongs in, splits a pack that
has grown too big, and re-renders everything canonically. `metafmt --check`
(what CI runs) names the file an entry should have been in. This is deliberate:
placement is arithmetic, and arithmetic is the tooling's job.

The exact required fields are defined by the JSON Schemas in `schema/*.schema.json` -
those are authoritative. `metacheck` validates against them.

*(The tree used to be one file per record, `works/<shard>/<slug>/work.json` and
friends. That layout is gone - see [PACK-SPEC.md](PACK-SPEC.md) for why and
`cmd/metamigrate` for the one-off conversion. Older issues and links that spell
a per-record path still resolve: the intake bot reads those paths as a way of
naming a record.)*

## Two ways to contribute

### (a) Issue forms - no JSON required

**Fastest route for one book**: the [add page](https://meta.audiosilo.app/add)
looks a missing audiobook up on Libex (a public mirror of Audible's catalogue),
shows you exactly which facts would be recorded, and opens the add-work form
prefilled - you review and submit on GitHub. Descriptions, summaries and ratings
never cross (see [LICENSING.md](LICENSING.md)); a lookup failure just falls back
to the blank form.

If you would rather not edit JSON, open an issue and pick a form:

- **Add a work** - a new book plus its first recording.
- **Add a recording** - another narration of a work that already exists.
- **Correct data** - fix a field on an existing record.
- **Import a library** - attach an OpenAudible `books.json`, a Libation export,
  or a metascan folder scan
  and let us ingest the factual fields.

Adding **characters or recaps**? Those are CC BY-SA community content and their
two forms live in
[audiosilo-meta-community](https://github.com/KodeStar/audiosilo-meta-community/issues/new/choose),
along with the authoring guide. The forms and the bot behave identically; only
the repository differs.

The forms are structured (every field is captured), and each carries the CC0
confirmation checkbox. Submitting one is usually all you have to do: an intake
bot parses the form, deduplicates it against the catalogue, composes the same
canonical records a hand-authored pull request would carry, validates them
(`metafmt` + `metacheck`), and - on success - opens a pull request within minutes
crediting you as the submitter. If your submission duplicates an existing record,
needs a human decision, or does not validate, the bot comments back on the issue
explaining exactly what to fix instead of opening a pull request. Either way a
maintainer still reviews before anything merges (see
[GOVERNANCE.md](GOVERNANCE.md)).

**Spotted a wrong field?** Every work, person, and series page on
[meta.audiosilo.app](https://meta.audiosilo.app) carries "edit on GitHub" and
"report a problem" links that open a prefilled correction issue - the quickest
way to fix a single fact without touching JSON.

#### Regional releases

One production released in several marketplaces is still **one** recording, so
the recording forms let you say which region a fact belongs to:

- **Audiobook ISBN(s)** takes one per line, either a bare ISBN or
  `region: ISBN` (`GB: 9781473647633`). A bare ISBN is recorded with the region
  *unstated*, which is a perfectly ordinary state - do not guess a region you do
  not know. (The ASIN field is different: an ASIN only exists inside a
  marketplace, so a bare one is assumed to be `us`.)
- **Regional publisher(s)** takes `region: Publisher Name` lines for the OTHER
  regions' imprints of the same recording. The **Publisher** field stays the
  publisher of record; the bot rejects a submission that repeats it in the
  regional list, names one region twice, or fills the regional list while
  Publisher is empty (the schema needs a publisher of record first).

The correction form can **add** either fact to a recording that already exists.
Set Field to `isbn` or `publishers` (the form label, "Regional publisher(s)",
works too) and put one value in Corrected value:

| Field | Corrected value | What happens |
|---|---|---|
| `isbn` | `GB: 9781473647633` | added to the recording's ISBNs |
| `isbn` | `GB: <an ISBN already recorded with no region>` | that entry is scoped to the region - a bare entry claims no region, so this only adds information |
| `publishers` | `GB: Hodder & Stoughton` | added to the regional imprints, kept sorted by region |

These two corrections only ever ADD. If the record already states a *different*
region for that ISBN, or a *different* imprint for that region, the bot will not
choose between two sources - it hands the issue to a maintainer and changes
nothing. Stating exactly what is already recorded is reported as a duplicate.

### (b) Direct pull requests

If you are comfortable with JSON and Git, edit the files directly. Walk-through
for adding a new work with its first recording and a new author:

1. **Add the person** (if the author is not already in the `people` family). Slug
   them (for example `j-k-rowling`) and add a `"j-k-rowling": { ... }` entry -
   name, `license`, `sources[]` - to the people pack whose range covers that
   slug. Pick the closest one by name; `metafmt --write` will move it if you
   guessed wrong.
2. **Add the work.** Add a `"<work-slug>": { ... }` entry to a works pack with
   the title, authors (referencing the person slug), and language.
3. **Add the recording.** Inside that work's entry, add a `"recordings"` map
   keyed by recording slug, with the narrators (referencing person slugs),
   abridged flag, runtime, release date, publisher, ASINs (`{region, asin}`),
   ISBNs, cover URL, and chapters. If you are recording a *regional* release of
   a production that is already here, do not add a second recording: add its
   ASIN to `asin[]`, write its ISBN as `{"isbn": "...", "region": "..."}`, and
   add its imprint to `publishers[]` (`{"region": ..., "publisher": ...}`) -
   never repeating the top-level `publisher` or naming a region twice.
4. **Add the series** (if new), or add this work to an existing series entry's
   `works[]` list with a string position.
5. **Format and validate before pushing:**

   ```sh
   go run ./cmd/metafmt --write --profile core   # canonicalise, place entries, split full packs
   go run ./cmd/metacheck --profile core         # schema + referential integrity + uniqueness
   ```

   Fix anything `metacheck` reports, then commit and open a pull request.

Reference the entity model above and copy the shape of an existing entry of the
same kind - that is the fastest way to get the fields right.

### When two pull requests touch the same pack

Because records share files, a pull request that sits open while another one
merges can conflict in a pack file. Usually nothing is really in dispute - each
side added its own records - so the resolution is mechanical:

```sh
git fetch origin
git rebase origin/main
# for each conflicted pack file, merge the two sides' records:
scripts/pack-union-merge.sh data/works/0/0.json
go run ./cmd/metafmt --write --profile core   # re-render and re-place the merged entries
go run ./cmd/metacheck --profile core
git add data && git rebase --continue
git push --force-with-lease
```

The script is a **three-way** merge: it reads the merge base, so a record one
side deliberately deleted stays deleted instead of being handed back by the side
that still has it. Records added on either side are kept, and a record both sides
touched merges when the two changes are independent one level down:

- in `data/works/`, two pull requests adding different narrations of one book
  merge inside that book's `recordings` map (the work's own fields must be
  identical);
- in `data/works-community/` (the community repository, which runs the same
  script), a characters pull request and a recaps pull request for the same book
  merge, because each wrote a different **member** of that book's entry.

It refuses (exit 5) the cases that are real disagreements: the same record - or
the same recording, or the same community member - edited on both sides, and one
side deleting what the other edited. Those need a person to decide which fact is
right.

The intake bot runs exactly this recipe on its own pull requests after every
merge to main, so a bot PR is normally already rebased for you; when the script
refuses, the bot leaves the PR alone and comments instead.

**Let git call the script for you.** A pack file is a map, and git's line merge
can be *worse* than a conflict on one: when two branches add the same entry key
and their insertions are anchored at different lines, git applies both and leaves
the key in the file twice - which is not pack storage at all. `.gitattributes`
already routes `data/**/*.json` to a merge driver; configuring it once in your
clone makes every merge and rebase of a pack file go through the script above
instead:

```sh
git config merge.packjson.name "three-way merge of pack-file entries"
git config merge.packjson.driver "scripts/pack-union-merge.sh %O %A %B %P"
```

Then `git rebase origin/main` resolves the mechanical cases on its own and stops
only on the ones a person has to settle. Run `metafmt --write --profile core` and
`metacheck --profile core`
afterwards either way - the merged entries still have to be re-rendered and
re-placed.

## Importing a library export

**No command line?** The [import page](https://meta.audiosilo.app/import) does the
first step in your browser: drop an OpenAudible `books.json`, a Libation JSON
export (Libation - Export menu - Export Library - save as JSON), an Audiobookshelf
library export, or a metascan folder scan, and it checks, entirely client-side,
which of your books are already in the database and which are new (the API
receives only ASINs/ISBNs and, for unmatched books, the author names used for
matching - your file never leaves your device). It then hands the new ones off to
a pre-filled issue, or lets you download a factual-only export to attach to an
import issue. That is the easiest way to contribute a library. Running
Audiobookshelf? The [`/audiobookshelf`](https://meta.audiosilo.app/audiobookshelf)
page has the export steps and also covers wiring this database in as an ABS custom
metadata provider.

For a bulk import that becomes one reviewable pull request, the `metaimport` tool
turns an export into work/recording/person/series records directly. If you have
an [OpenAudible](https://openaudible.org/) or
[Libation](https://getlibation.com/) library:

```sh
go run ./cmd/metaimport openaudible <path-to/books.json> --data data --dry-run
go run ./cmd/metaimport openaudible <path-to/books.json> --data data

go run ./cmd/metaimport libation <path-to/LibationExport.json> --data data --dry-run
go run ./cmd/metaimport libation <path-to/LibationExport.json> --data data
```

There is also `metaimport libex <rows.json>` for JSON/NDJSON rows exported from
the Libex catalogue (a public Audible-metadata mirror whose operator permits it).
Unlike the personal-library paths above, libex imports are **curated batch
imports**: they follow the bounded posture in [LICENSING.md](LICENSING.md) and
the batch-import rules in [GOVERNANCE.md](GOVERNANCE.md) (named source, stated
selection rationale, reviewable tranches - never a catalogue mirror). Retailer
genre strings are mapped onto the normalized vocabulary in code; anything
unmapped is dropped with a warning. A trailing role qualifier on an author
credit ("Marco Rossi - Traduttore") is cleaned off the person's name, as it
always was, and the role it stated is now recorded as a work credit; a qualifier
with no place in the controlled vocabulary is still just stripped.

`metaimport libex <rows.json> --enrich [--dry-run]` is the **backfill** half:
instead of creating anything, it fills ONLY absent facts (genres, credits,
ISBNs, runtime, release date, publisher, cover, chapters) on works and
recordings the catalogue already holds, matched by ASIN, and places already-catalogued works
into already-catalogued series. Existing values always win; a row whose runtime
or release date contradicts the recorded production is not used at all; every
change is stamped with a `libex-import` source. Rows for books the catalogue
does not hold are counted and ignored, so a large row set is safe input.

`metaimport libex <rows.json> --recordings-only [--dry-run]` is the
**alternate-narration** half. The catalogue models one work with many
recordings, but a second narration of a book already here matches no ASIN (so
`--enrich` ignores it) and would mint a duplicate work on the create path. This
mode resolves each row to a work the catalogue already holds - by its cleaned
title and its exact author set, seeing through a trailing `, Book 2` volume
marker or a `(Full-Cast Edition)` qualifier - and adds it as a new recording
under that work, or merges its ASIN into a matching sibling recording. A row
whose work is not here is counted and dropped: the mode never creates a work and
never touches a series file, so a large row set is safe input here too. It is
mutually exclusive with `--enrich`.

`metaimport libex-select <export.ndjson> -o <subset.ndjson> [--max-per-series N]`
reduces a full catalogue export to the bounded subset the import posture allows:
rows that COMPLETE series the catalogue already tracks (valid position, slot not
already taken, language and region mappable, ASIN not already present), with a
per-series cap for runaway franchises and a report that accounts for every row
read. The full dump-to-rows operator flow (restore the Postgres dump, export
NDJSON, select, review, import in tranches, backfill with `--enrich`, then pick
up the alternate narrations with `--recordings-only`) is documented in
[scripts/README.md](scripts/README.md).

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
titles/timestamps come across; the publisher `description`/`summary`, raw
retailer genre strings, ratings, and your personal library state are dropped
(see [LICENSING.md](LICENSING.md)). The importer **deduplicates by ASIN** against the
existing catalogue, so re-running it, or importing a book someone else already
added, is a no-op rather than a duplicate.

After importing, format and validate as usual (`go run ./cmd/metafmt --write
--profile core` then `go run ./cmd/metacheck --profile core`), review the diff, and open a pull request. You are
still dedicating the imported facts to the public domain under CC0-1.0, so import
only from a library you may share this way.

## Data rules

- **Factual data only.** Titles, authors, narrators, series order, runtimes,
  chapter titles and timestamps, identifiers. Facts are not copyrightable; this
  is what we collect.
- **No publisher blurbs.** Do not paste a publisher or retailer synopsis. A
  description, if you add one, must be **your own words**.
- **Covers are URLs only** (`cover_url`). Never commit an image file.
- **Genres come from the controlled vocabulary.** A work's optional `genres`
  array takes values from the enum in `schema/common.schema.json`, sorted
  ascending. Never copy a retailer's genre strings verbatim; if none of the
  vocabulary fits, leave the field off (proposing a new genre is a schema
  change - open an issue).
- **`sources[]` is required** on every record - say where the facts came from.
- **CC0 dedication.** By submitting, you dedicate the data to the public domain
  (CC0-1.0) and confirm you may do so. See [LICENSING.md](LICENSING.md).

## The gate

Run this locally before opening a pull request - CI runs the same checks and will
block a red pull request:

```sh
go build ./... && go vet ./... && go test ./... && \
  go run ./cmd/metacheck --profile core && go run ./cmd/metafmt --check --profile core
```

`--profile core` is the exact invocation CI uses, and it matters: it says this
root holds works, people and series - not the CC BY-SA `works-community` family,
which lives in the community repository now. Without it the tools read the tree
as a whole database, where a stray `data/works-community/` file is a family they
quietly adopt; with it that file is an unrecognized location and a red check. Run
them the way CI does and a green local gate means a green pull request.

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
