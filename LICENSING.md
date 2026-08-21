# Licensing

audiosilo-meta separates **code** from **data**, and licenses each on its own
terms. Read this before contributing - by opening a pull request or an issue-form
submission you agree to the terms below.

## At a glance

| What | Licence | Notes |
|---|---|---|
| Code (tooling, schemas, CI, future server) | **AGPL-3.0-only** | See [`LICENSE`](LICENSE). Matches audiosilo-server. |
| Data - factual core | **CC0-1.0** (public domain dedication) | The works, recordings, people, and series. Every such record carries `license: "CC0-1.0"`. |
| Data - derived layer | **CC BY-SA 4.0** | Community-authored characters and recaps, and any Fandom / LibraryThing CK derived content. Kept in a separate pack family, `data/works-community/` (each entry a work's `characters` and `recaps` members), so the boundary is directory-structural as well as schema-enforced. |
| Publisher blurbs, cover art | **Not accepted** | Referenced, never copied. Covers are URLs only; descriptions must be community-written. |

## Why two data licences

The factual metadata this project exists to collect - titles, authors,
narrators, series order, runtimes, chapter titles and timestamps, identifiers -
is not copyrightable (facts are free under Feist v. Rural). Dedicating it to the
public domain with **CC0-1.0** removes all friction: anyone can build on the data
without attribution obligations, and downstream forks stay maximally open.

The **CC BY-SA 4.0** layer covers *derived, expressive* content -
community-authored character descriptions and recaps, and anything derived from
Fandom wikis or LibraryThing Common Knowledge (both CC BY-SA 3.0 at source; see
"The 4.0 upgrade" below for why that still flows in).
Share-alike is desirable there: it keeps derivative works open. This layer lives
in a separate pack family, `data/works-community/`, where each entry holds one
work's `characters` and `recaps` members - so the CC0 core is never contaminated
by share-alike obligations, and the separation is visible in the directory tree
as well as in the records. The
boundary is **enforced structurally by the schema**, not by convention: a core
record (work/recording/person/series) can only carry `CC0-1.0`, and a sidecar can
only carry `CC-BY-SA-4.0` - the `license` enum differs between them, and
`metacheck` rejects any record that crosses the line. Authoring this layer is
documented in [AUTHORING.md](AUTHORING.md).

## The 4.0 upgrade (2026-08-21)

The community layer was **CC BY-SA 3.0** until 2026-08-21, when every entry was
relicensed to **CC BY-SA 4.0** in one change. Every sidecar in the tree at that
point had been authored in-house, so there was no third-party 3.0 content to
carry forward: the schema enum was *replaced* rather than widened, and there is
no dual-licensed period to reason about.

Why upgrade:

- **The original reason for 3.0 survives it.** Fandom and LibraryThing Common
  Knowledge are BY-SA 3.0 at source, and 3.0's ShareAlike clause permits
  licensing an adaptation under a later version of the licence. Our posture is
  derived, own-words content and never verbatim mirroring, so 3.0 sources still
  flow into a 4.0 project.
- **The reverse never held.** 4.0 content cannot flow into a 3.0 project, and
  Wikipedia has been BY-SA 4.0 since 2023. Staying on 3.0 would have foreclosed
  every 4.0 source permanently.
- **4.0 explicitly licenses sui generis database rights** - directly relevant to
  a project that *is* a database, and to the EU/UK right discussed under
  "Imports bring facts only" below. It also adds the 30-day cure provision and
  is one international instrument rather than ported jurisdiction variants.

A contribution made before the upgrade is covered by it because the contributor
and the maintainer were the same person; from 2026-08-21 the contribution
checkbox on the characters/recaps issue forms names 4.0.

A submission whose attachment was authored against the old instructions
therefore fails validation with a bare value-mismatch message
naming the `license` field - and the fix is mechanical: change the one value to
`"CC-BY-SA-4.0"` and nothing else about the file needs to move.

## The CC0 core

The factual core - works, recordings, people, and series - is dedicated to the
public domain under
[CC0-1.0](https://creativecommons.org/publicdomain/zero/1.0/). This covers:

- Works: title, subtitle, authors, language, first-published year, series
  membership, contributor credits (who translated, edited or illustrated the
  book - a role someone held is a fact about the book, stated by a source and
  never inferred), and genre labels drawn from the project's own controlled
  vocabulary (see "Genres" below).
- Recordings: narrators, abridged flag, runtime, release date, publisher name,
  region-scoped ASINs, ISBNs, chapter titles and timestamps, cover URL.
- People: author and narrator names, their identifiers, and the entity kind
  (individual / group / publisher) when someone has classified the record.
- Series: names and the ordered list of member works (with string positions such
  as `"2.5"`).
- Identifiers and cross-references of every kind.

**Your submission is a CC0 dedication.** When you contribute a record (by pull
request or issue form) you dedicate that contribution to the public domain under
CC0-1.0, and you confirm you have the right to do so. The pull-request and
issue-form templates carry an explicit confirmation checkbox; ticking it is the
lightweight contributor agreement for this project. Do not submit data you are
not free to dedicate this way.

## Referenced, never copied

Two categories of copyrighted expression are deliberately kept **out** of the
repository:

- **Cover art** is stored as a URL (`cover_url`) pointing at the publisher's or
  retailer's own hosting. The repository never contains image files. This keeps
  the repo text-only and avoids redistributing copyrighted artwork.
- **Publisher blurbs and marketing descriptions** are copyrighted and are not
  accepted. A book's description, if present, must be **community-written in the
  contributor's own words**. Pasting a publisher or retailer synopsis will be
  rejected.

## Genres

A work may carry `genres`: labels from a **controlled vocabulary this project
owns** (the enum in `schema/common.schema.json`). The vocabulary is a flat,
retailer-neutral list maintained by schema change (so every addition is
maintainer-reviewed), and a genre label on a work is a community-assigned
classification dedicated to the public domain with the rest of the record.

What is deliberately **not** accepted is any retailer's genre *taxonomy*: the
category trees that stores curate (their node names, hierarchy, and typed
tags) are their editorial arrangement, and copying one wholesale is exactly the
"selection and arrangement" that compilation copyright and database rights
protect. Importers therefore never store a retailer's genre strings verbatim -
they map them onto our vocabulary in code, and anything that does not map is
dropped.

## Imports bring facts only

The import path (OpenAudible `books.json`, Libation exports, per-title
Audible/Audnexus/Libex lookup at contribution time, and curated batch imports
from a cooperating catalogue) ingests **factual fields only** - title, ASIN,
narrator names, series order, runtime, chapter titles and timestamps,
contributor roles a source stated on a credit, and normalized genres per the
section above. Publisher marketing copy (the
`description` / `summary` fields), ratings, and cover images are copyrighted or
derived expression and are stripped, not imported.

Facts are free, but *bulk extraction from someone else's database* is not the
same act as recording a fact: the EU/UK sui generis database right protects a
catalogue against the extraction of a substantial part of it, whatever the
copyright status of the individual rows. The import posture is therefore
**bounded and auditable**, in three accepted shapes:

- **Per-title lookup at contribution time** - a contributor adds one book and a
  lookup fills in its facts.
- **Enrichment of records already here** - filling absent facts on catalogued
  works/recordings, matched by identifier, from a source whose operator permits
  it.
- **Curated batch imports** - bounded, human-reviewed tranches with a stated
  selection rationale (for example, completing series the catalogue already
  holds volumes of), landed as reviewable pull requests, never as an unbounded
  mirror.

Wholesale mirroring of a third-party catalogue remains out of bounds. Every
imported record is stamped with a typed `sources[]` entry naming its origin, so
the retraction promise below covers an entire source at once - and so a record's
origin can be ranked, which is what the next section is about.

## Trust tiers and the user-overwrite rule

A bulk import is a starting point, not an answer. A record seeded from a
third-party mirror states real facts, but **nobody has attested it** - no person
has said "this is my book and these are its details". The project's aim is that
real users become the provenance over time, so the import rules rank sources and
let an attestation win.

The ordering, over the `sources[]` entry `type`:

| Tier | Source types | What it may do |
|---|---|---|
| **User library** | `user` (issue forms and hand edits), `openaudible-import`, `libation-import`, `audiosilo-books-import` (metascan folder scans, the site's `/import`) | May overwrite a bulk-mirror-only record |
| Reference | `audible-lookup`, `openlibrary`, `wikidata`, `inventaire`, `community`, anything unclassified | No overwrite authority; its presence also means a record is not mirror-only |
| **Bulk mirror** | `libex-import` | Overwritable by the tier above |

The rule, applied at **record** granularity (not per field):

1. A record is **bulk-mirror-only** ("libex-only") when it has at least one
   source and *every* one of them is bulk-mirror tier.
2. When a user-library import matches such a record by ASIN, the facts the
   export **states** replace the recorded ones, and the run's source entry is
   appended. Facts the export does not state keep the mirror's values - silence
   is not an assertion. The record is now user-attested.
3. After that first attestation the ordinary rules resume, unchanged: the
   recorded value wins and a row that contradicts the record is refused whole.
   On the **create path** an already-catalogued book is a skip, and a skip stays
   a skip - a later user import does not backfill an attested record, even where
   a fact is absent from it. Only **enrichment** (`metaimport ... --enrich`)
   fills what is absent. Where two users disagree on a fact the import rules
   *check* - a runtime more than 10% apart, or a release date that is not the
   same date at another precision - the **first writer's value stands** and the
   disagreement is flagged for a maintainer to adjudicate, never resolved by
   letting the later writer win. Disagreements on the other fields (publisher
   spelling, cover URL, chapter tables) are **not** detected: the recorded value
   simply stands, silently, so the first writer wins there too but nobody is
   told. Correcting one is what the "Correct data" form is for.
4. The safety guards hold in every tier: a runtime more than 10% apart, or a
   genuinely different release date, means the row describes a different
   production and is not applied at all. The release-date guard has one
   deliberate exception - the **ASIN merge**, where a user's regional re-release
   folds its ASIN into an existing recording. That match is inferred (same work,
   same narrators) and a regional edition legitimately carries its own date, so
   the date is not treated as a disagreement there; the runtime guard, which is
   what distinguishes a re-release from a different production, still decides
   whether the merge happens at all. A stated date also never *coarsens* a
   recorded one: a year-only export leaves a recorded full date alone. Identity
   is never rewritten by an import - a work's title and authors, a recording's
   narrators, and the ASIN/ISBN identifier sets are corrections (the "Correct
   data" form), not imports. `added_at` is a creation stamp, never a fact from
   an export.

The intake bot applies the same rule from the other side: a form submission that
duplicates a bulk-mirror-only record is routed to a maintainer rather than
closed as a duplicate, because the submitter's data should replace the seed and
the bot can only compose new records.

**A known gap, accepted deliberately.** Provenance is per record, so "remove
everything the mirror gave us" cleanly deletes bulk-mirror-only records, but on
a *mixed* record we cannot tell which individual fields the mirror seeded.
Closing that would mean recording the filled field names in the source entry (or
per-field provenance); it is not done today, and the retraction promise below is
therefore whole-record for mixed records.

The same gap shapes what a retraction may *delete*. Once users have attested
part of a mirror tranche, a whole-record retraction of the mirror must keep the
records that attested works still depend on - in particular the **people**: a
credit resolves by slug, and a person record seeded by the mirror is reused by
later imports without ever being stamped by them, so it can still read as
bulk-mirror-only while attested works credit it. Deleting it would leave
dangling references. `metacheck` catches exactly that (an unresolvable author or
narrator id is a problem, not a warning), so a retraction sweep is run through
the validator before it is merged rather than trusted to the tier test alone.

## Provenance and retraction

Every entity carries a `sources[]` array recording where each fact came from
(`{type, ref?, imported_at?}`). This makes each record's provenance auditable and
means **any source can be retracted wholesale** if it later proves problematic -
we can find and remove every record derived from it.

## Takedown and rightsholder opt-out

If you are a rightsholder (author, narrator, publisher, or their agent) and
believe a record here infringes your rights, or you wish to opt a title out,
contact **kode@audiosilo.app**.

The process:

1. **Identify.** Tell us the specific records (paths, slugs, or ASINs/ISBNs) and
   the basis of the request.
2. **Review.** We acknowledge the request and review it. Because the core is
   factual data (not copyrightable), many requests will not have a legal basis -
   but we operate a good-faith opt-out channel regardless, and we honour valid
   DMCA-style notices.
3. **Dispute window.** Where a request is contested (for example, a contributor
   asserts a fact is uncopyrightable and independently sourced), there is a short
   window for the parties to respond before a final decision.
4. **Removal.** Valid requests result in the affected records being removed from
   `data/` and from the next SQLite release artifact. Because provenance is
   tracked, we can also retract an entire source.

This is a good-faith policy for a young project, not legal advice. It will be
formalised as the project matures.
