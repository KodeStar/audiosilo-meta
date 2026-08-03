# scripts

Operator scripts for maintainer-run data operations. Nothing here runs in CI or
in the served application - these are the steps a maintainer performs by hand,
recorded so they are reproducible and reviewable.

## libex-export-rows.sql - dump to importable rows

`libex-export-rows.sql` is the validated query that turns a libex
(libex.lostcartographer.xyz) database dump into the NDJSON row shape
`metaimport libex` parses. It emits one JSON object per line with only the
factual columns the importer reads; the dump's description, summary, rating and
copyright columns are never selected. It excludes AI-narrated rows (see below)
and everything that is not a book (it keeps `content_delivery_type`
`SinglePartBook` and `MultiPartBook`, so podcasts, periodicals, parts, bundles
and series containers are out), and it orders by `sku_group` so the per-region
editions of one title come out adjacent - the importer folds those into one
recording through its same-narrator ASIN merge.

Authors, narrators, genres and series come from their join tables as JSON
arrays. Chapters come from `tracks`, whose `chapters` column is an **object
wrapping the list** (`{"chapters": [...]}`) - the query unwraps it with
`->'chapters'`, because the importer type-asserts an array and silently reads
"no chapters" from anything else. `books.isbn` is deliberately not exported: it
is sometimes the print edition's ISBN, and `recording.isbn` is a hard dedup key.

### AI narrations are excluded twice

A synthetic voice is not a person, so an AI-narrated production has no place in
a catalogue built around who narrated a book - importing one mints a person
record for a text-to-speech engine. libex has an `is_vvab` ("virtual voice")
flag that looks like the filter for this, and the query still applies it, **but
it does not work**: measured over the full 1.13M-row dump, 145,558 books credit
an AI voice and `is_vvab` is `false` on 145,550 of them. The evidence lives in
the narrator credit.

So the query also filters on the credit itself, using an evidence-driven list of
the forms the dump actually contains ("Virtual Voice" and its Spanish, French,
Italian and German siblings; Audible's "AI Voice *persona*" family; trailing
markers like "(Voz de IA)" and "(AI)"). Any one AI credit disqualifies the row.
That removes ~145,550 rows from a full export.

The same three shapes are implemented in `internal/importer/libex.go`
(`aiNarratorNames` / `aiNarratorPrefix` / `aiNarratorMarkers`), which refuses
the same rows at parse time for every `metaimport libex` mode - so a dump
exported without this filter, or one an older copy of this script produced, is
still safe. **The two lists must be kept in step**; each file's comment says so.
The refusals are reported as one aggregated warning line per run.

### Unidentifiable credits are excluded too

The second credit-side refusal: a row is refused whole
when **any** of its author or narrator credits names someone this catalogue
cannot identify. A person's identity here is their slug, and `Slugify` keeps only
ASCII letters and digits (accented Latin folds onto its base letter), so a name
written entirely in Korean, Cyrillic, Chinese, Japanese, Greek or Arabic slugs
away to nothing and falls back to the shared catch-all `person` record. On a bulk
seed that fallback would make one record the credited author or narrator of
thousands of unrelated books - a falsehood, where an absent book is merely a gap.
Measured over the 142,550-row seed selection: 2,075 rows (1.5%), 417 distinct
names. It reverses cheaply - when `Slugify` learns to transliterate, the refused
rows re-import from the same dump.

This one has **no SQL twin**: the rule is Unicode folding in Go, not a name list,
and re-spelling it in SQL would be a second implementation free to disagree with
the first. It is also deliberately **libex-only** - the OpenAudible, Libation and
`audiosilo-books` importers read a *user's own* library, where the conflation is
visible to that user and refusing their book to keep a shared database tidy would
be the wrong trade. Like the AI refusals, these are reported as one aggregated
warning line per run (naming both the rows and the offending credits).

### Collective and placeholder credits

A credit that names no individual is classified by **what it states**, never by
the language it states it in, into exactly four buckets. Only one of them is
refused:

1. **Collective statements** - "a full cast performed this", "several
   contributors wrote this". True, and true in every language: *Full Cast*, *a
   full cast*, *ensemble cast*, *diverse Sprecher*, *elenco*, *distribution
   complète*; *Various*, *Various Authors*, *Autori Vari*, *divers auteurs*,
   *varios narradores*, *Diverse*; *Anonymous*, *anónimo*, *anonyme*;
   *Uncredited*. These **import**, folded onto one canonical record per
   statement (`full-cast`, `various`, `anonymous`, `uncredited`).
2. **Unknown-identity statements** - "who this was is not known": *Unknown*,
   the bibliographic *N.N.* (nomen nescio), *auteur inconnu*, *narratore
   sconosciuto*, *autor desconocido*. These **import**, folded onto `unknown`.
   It is deliberately a different record from `anonymous`: anonymity is a
   choice, an unknown identity is a gap.
3. **Booking placeholders** - the retailer's scheduling artifact, naming nobody
   at all: *to be announced*, *to be confirmed*, *tbd*, *tba*, *tbc*, *n/a*.
   These are **refused**, whole-row, in both the query and the importer.
4. **Branded ensembles** - a named troupe (*The Colonial Radio Players*,
   *Museum Audiobooks cast*, *Das Schauspiel-Ensemble vom Landestheater
   Detmold*, *Linguistics Team*). A real group with a real name, so it keeps
   its own person record and nothing folds it.

**One list, one rule.** The fold lives in one table,
`internal/importer/collective.go`, applied at one place - the tail of the shared
credit cleaning, so authors, narrators and role credits all pass through it and
every importer inherits it, not just libex. Matching is the **whole cleaned
name**, exactly, case- and accent-insensitively: never a pattern and never a
substring, which is what keeps bucket 4 (and the people named Cast, Castle,
Sorvari) intact. Every entry is measured over the full dump and pinned by a
test.

The SQL query's placeholder filter is the twin of bucket 3 **only**. It
deliberately has no twin for buckets 1 and 2: those rows are wanted, and
dropping them at export time would lose books that import perfectly well. An
export produced by an older copy of the query refused some of them; that is
harmless - the bound only loosened, and re-exporting picks them up.

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

The row count is roughly 918k on the current dump: 1.06M books minus the ~145k
AI-narrated ones the credit filter drops (see "AI narrations are excluded
twice"). If your export is nearer 1.06M, the credit filter did not run - your
copy of the query is out of date. Nothing breaks either way, because
`metaimport libex` refuses those rows again at parse time and says so in one
aggregated warning line, but the export is the cheaper place to lose them.

### 3. Select a tranche

```sh
go run ./cmd/metaimport libex-select full.ndjson --data data -o subset.ndjson [--max-per-series N]
```

This writes no records. It keeps only rows that genuinely complete a series
already in the catalogue: the ASIN must not already be recorded, the language
and marketplace must both map, the credits must pass both credit-side refusals
above (a row the import will refuse must not be selected - it would promise a
completion the tranche cannot deliver, and would claim the series slot ahead of
an importable sibling), and the row must claim a usable series position
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

`--conflicts` (step 6) is wired here too and can point at the same file, but
expect this pass to leave it empty: the create path reaches the contradiction
guard only through the ASIN-dedup attestation, which is a user-library behaviour
a libex run never performs. Same reason as step 7.

### 6. Backfill the records we already have

The selection deliberately excludes rows whose ASIN is already in the catalogue,
because those are enrichment fodder rather than new imports. Backfill them from
the full export:

```sh
go run ./cmd/metaimport libex full.ndjson --data data --enrich --conflicts conflicts.ndjson
```

`--conflicts` is the **conflict worklist**, and this is the pass that produces
most of it. A row whose runtime or release date disagrees with the record its
ASIN matched is refused whole (the disagreement is the run's own evidence that
the ASIN sits on a different production), and without the flag that refusal is
one warning line in a run that prints hundreds of thousands. With it, each
refusal also appends one NDJSON row naming both values and the record they
disagree about:

```json
{"run":"enrich","asin":"B0ABCDEFGH","work":"<work-slug>","recording":"<recording-id>","field":"runtime_min","recorded":81,"stated":492,"source_type":"libex-import","detected_at":"2026-08-02"}
```

The run is unchanged by the flag - same warnings, same counters, same writes -
so it is safe to add to a real wave, and `--dry-run --conflicts <path>` surveys
a dump's disagreements without touching data. Read the worklist AFTER the wave:
most rows are the mirror describing another edition, but a large `recorded` /
`stated` gap on a runtime is how a genuinely WRONG recorded value surfaces. The
81-vs-492 figures above are a real case that was found by hand: the recording
was stored at 81 minutes while the dump and the recording's own chapter table
both said 492. Sort by the gap, check a handful against the source, and fix the
records through the ordinary correction path.

The file is APPENDED to, so a dump split into chunks with `split -l`
accumulates one worklist across every chunk's run, and a run killed part-way
keeps every conflict it had already found.

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
go run ./cmd/metaimport libex full.ndjson --data data --recordings-only --conflicts conflicts.ndjson
```

`--conflicts` is accepted here too, and pointing it at the SAME file as step 6
is the intended use: the worklist is appended to and every row names the pass
that found it (`"run":"recordings-only"`), so one file holds the wave's whole
disagreement list. Expect it to stay empty in this pass, though: the
contradiction guard is reached here only through the ASIN-dedup attestation,
which is a user-library behaviour a libex run never performs. The mode is wired
for it so a worklist is never silently missing a pass, not because this pass is
expected to fill one.

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

## libex-fill - automatic cover/chapter enrichment

`metaimport libex-fill` is the automatic counterpart to `libex --enrich`. Rather
than being handed rows, it works out which recordings need filling, fetches just
those ASINs from the live libex service, and feeds them through the ordinary
enrichment pass.

```sh
go run ./cmd/metaimport libex-fill --data data [--works a,b] [--limit N] [--dry-run]
```

It selects recordings that carry an ASIN but have **no cover URL, no chapter
list, or both**, and by default covers only the **user-library tier**
(audiosilo-books / Libation / OpenAudible / hand submissions). That bound is
where the gap is: measured over the tree, audiosilo-books records are 32%
without a cover and 100% without chapters, because a personal library export
states neither, while libex-seeded records are 0.1% without a cover. Filling
covers across the whole catalogue would be ~136,000 lookups against a free
public service to fix about 130 records. `--all-tiers` lifts the bound.

Records are read from libex's mirrored copy (`/db/book/{asin}`) first and its
live Audible fetch (`/book/{asin}`) second - the mirror is cheap and covers most
of the catalogue, and the live path serves the recent releases the mirror has
not caught up with. Rows are projected down to exactly the shape
`libex-export-rows.sql` emits, so a fetched row is indistinguishable from a dump
row and goes through the same parse and enrichment path with no special case:
same fill-absent rule, same runtime/date contradiction guard, same
`libex-import` provenance.

A lookup that misses or fails is counted, never fatal - a partial fill is better
than none, and the gap is simply left for the next run.

`.github/workflows/intake.yml` runs it after an import-form contribution, which
is what keeps a newly contributed library from landing with placeholder covers.

