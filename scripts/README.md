# scripts

Operator scripts for maintainer-run data operations. Nothing here runs in CI or
in the served application - these are the steps a maintainer performs by hand,
recorded so they are reproducible and reviewable.

## libex-export-rows.sql - dump to importable rows

`libex-export-rows.sql` is the validated query that turns a libex
(libex.lostcartographer.xyz) database dump into the NDJSON row shape
`metaimport libex` parses. It emits one JSON object per line with only the
factual columns the importer reads; the dump's description, summary, rating and
copyright columns are never selected. It excludes AI-narrated rows (`is_vvab`)
and everything that is not a book (it keeps `content_delivery_type`
`SinglePartBook` and `MultiPartBook`, so podcasts, periodicals, parts, bundles
and series containers are out), and it orders by `sku_group` so the per-region
editions of one title come out adjacent - the importer folds those into one
recording through its same-narrator ASIN merge.

## The import posture (read this first)

This project is **not a mirror** of a retailer database. Per
[LICENSING.md](../LICENSING.md) and [GOVERNANCE.md](../GOVERNANCE.md), libex is a
bounded source: we import **curated tranches** that complete series the
catalogue already tracks, each one maintainer-reviewed before it lands. The
selection step below is what makes that bound mechanical instead of a matter of
discipline, and its report is what a reviewer reads. A run that would import
"everything" is not a tranche - re-scope it.

## The operator flow

### 1. Restore the dump

The dump arrives in Postgres 16 custom format. Restore it into a scratch
database - a disposable container keeps it off any real server:

```sh
docker run -d --name libex-scratch -e POSTGRES_PASSWORD=scratch -p 55432:5432 postgres:16
createdb -h localhost -p 55432 -U postgres libex
pg_restore -h localhost -p 55432 -U postgres -d libex --no-owner --no-privileges libex.dump
```

### 2. Export the rows

```sh
psql -h localhost -p 55432 -U postgres -d libex -tA -f scripts/libex-export-rows.sql > full.ndjson
```

`-tA` is load-bearing: tuples-only, unaligned, so each line is exactly one JSON
object and nothing else.

Check psql's exit status before going any further (`echo $?`, or run it under
`set -e`). The redirect creates `full.ndjson` whether the query succeeded or
not, so a connection dropped halfway through leaves a short file that looks
perfectly importable - and the selection step reads it as the whole dump.

### 3. Select a tranche

```sh
go run ./cmd/metaimport libex-select full.ndjson --data data -o subset.ndjson [--max-per-series N]
```

This writes no records. It keeps only rows that genuinely complete a series
already in the catalogue: the ASIN must not already be recorded, the language
and marketplace must both map, and the row must claim a usable series position
that nothing else already holds (a position the catalogue fills, or that an
earlier row of the same run claimed, would import as a work stranded outside its
series). Those rows are re-emitted verbatim as NDJSON. Then read the report:
rows read and selected, the projected new works (counted per title, so the
per-region sibling rows of one title count once), the per-series breakdown, and
the exclusion counts - one per rule, adding up with the selected rows to every
row read. `--max-per-series` caps the new works taken from any
one series, in series-position order, so a runaway franchise stays reviewable;
whatever it cut is printed.

If the report describes a tranche that is too large or too broad to review,
tighten it (a smaller cap, or a catalogue with fewer stub series) and select
again. The report is the artifact a reviewer signs off on.

### 4. Dry-run the import

```sh
go run ./cmd/metaimport libex subset.ndjson --data data --dry-run
```

Read the plan and the warnings before anything is written.

### 5. Import in tranches

```sh
go run ./cmd/metaimport libex subset.ndjson --data data
```

Split a large subset into several files and land them as separate reviewable
PRs rather than one unreadable diff. After each run: `go run ./cmd/metacheck`
and `go run ./cmd/metafmt --check`.

### 6. Backfill the records we already have

The selection deliberately excludes rows whose ASIN is already in the catalogue,
because those are enrichment fodder rather than new imports. Backfill them from
the full export:

```sh
go run ./cmd/metaimport libex full.ndjson --data data --enrich
```

### 7. Pick up the alternate narrations

The steps so far only ever add books the catalogue does not have. A **second
narration of a book it already holds** falls through every one of them: its ASIN
matches nothing, so step 6 ignores it; it fills no free series position, so step
3 excludes it; and the plain create path would mint a duplicate work rather than
a sibling recording. That is why the catalogue could hold Harry Potter and the
Chamber of Secrets with only the Stephen Fry narration while the Jim Dale one
and the full-cast edition sat in the export, unreachable.

The recordings-only pass is the step that picks them up:

```sh
go run ./cmd/metaimport libex full.ndjson --data data --recordings-only --dry-run
go run ./cmd/metaimport libex full.ndjson --data data --recordings-only
```

It is bounded by **this** catalogue, like the enrichment pass. Each row is
resolved to a work already here by its cleaned title and its exact author set;
if it resolves, the row becomes a new recording under that work (or its ASIN
merges into a matching sibling recording, which is how a regional re-release
attaches to the narration that already exists). If it does not resolve, the row
is counted and dropped - the mode **never creates a work and never touches a
series file**.

Read the two skip buckets on the summary line:

- *skipped (already present)* - the ASIN is catalogued; nothing to do.
- *skipped (work not in the catalogue)* - the expected outcome for most rows.
  The run names a few examples in one aggregated warning; skim them. They should
  be books we genuinely do not hold, plus the things that are deliberately not
  the work at all: excerpts, trivia and companion titles, "making of" episodes.
  A row you recognize as a book we DO hold means the title matching missed it -
  worth a look before you dismiss it.

Run it after step 6 so the two passes do not compete for the same rows, and land
its diff as its own reviewable PR: the recordings it adds are mostly high-profile
alternate narrations, which is exactly the diff a reviewer can sanity-check by
eye.

**Memory.** This pass shares the libex parse layer, which slurps the whole file
into memory before planning discards the rows that do not apply - roughly 6GB of
live heap for the full 1.06M-row dump, the same caveat step 6 carries. Unlike
step 6 there is no pre-filter that helps: an alternate narration is by
definition a row `libex-select` excluded, so the natural input here IS the
unfiltered dump. On a machine that cannot hold it, split `full.ndjson` into
chunks with `split -l` and run the pass over each. A **streaming row iterator**
for the parse layer (the shape `libex-select` already uses) is the known
follow-up that would remove the caveat for all three libex passes.

### 8. Clean up

Drop the scratch container and the exported rows when you are done; neither the
dump nor `full.ndjson` belongs in the repo.

```sh
docker rm -f libex-scratch
```
