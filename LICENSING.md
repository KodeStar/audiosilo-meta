# Licensing

audiosilo-meta separates **code** from **data**, and licenses each on its own
terms. Read this before contributing - by opening a pull request or an issue-form
submission you agree to the terms below.

## At a glance

| What | Licence | Notes |
|---|---|---|
| Code (tooling, schemas, CI, future server) | **AGPL-3.0-only** | See [`LICENSE`](LICENSE). Matches audiosilo-server. |
| Data - factual core | **CC0-1.0** (public domain dedication) | Everything in `data/` today. Every record carries `license: "CC0-1.0"`. |
| Data - derived layer (reserved) | **CC BY-SA 3.0** | Not yet accepted. Reserved for future characters/recaps and any Fandom / LibraryThing CK derived content. Kept in separately-tagged records. |
| Publisher blurbs, cover art | **Not accepted** | Referenced, never copied. Covers are URLs only; descriptions must be community-written. |

## Why two data licences

The factual metadata this project exists to collect - titles, authors,
narrators, series order, runtimes, chapter titles and timestamps, identifiers -
is not copyrightable (facts are free under Feist v. Rural). Dedicating it to the
public domain with **CC0-1.0** removes all friction: anyone can build on the data
without attribution obligations, and downstream forks stay maximally open.

A future **CC BY-SA 3.0** layer is reserved for *derived, expressive* content -
community-authored character descriptions and recaps, and anything sourced from
Fandom wikis or LibraryThing Common Knowledge (both CC BY-SA 3.0 at source).
Share-alike is desirable there: it keeps derivative works open. That layer does
**not** exist yet - no such records are accepted in Phase 0 - and when it lands it
lives in separately-tagged records so the CC0 core is never contaminated by
share-alike obligations.

## The CC0 core (all current data)

Everything under `data/` is dedicated to the public domain under
[CC0-1.0](https://creativecommons.org/publicdomain/zero/1.0/). This covers:

- Works: title, subtitle, authors, language, first-published year, series
  membership.
- Recordings: narrators, abridged flag, runtime, release date, publisher name,
  region-scoped ASINs, ISBNs, chapter titles and timestamps, cover URL.
- People: author and narrator names and their identifiers.
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

## Imports bring facts only

The import path (OpenAudible `books.json`, Libation exports, and per-title
Audible/Audnexus lookup at contribution time) ingests **factual fields only** -
title, ASIN, narrator names, series order, runtime, chapter titles and
timestamps. Publisher marketing copy (the `description` / `summary` fields) and
cover images are copyrighted expression and are stripped, not imported. We rely
on the tolerated per-title-lookup pattern, never bulk mirroring of any
third-party catalogue.

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
