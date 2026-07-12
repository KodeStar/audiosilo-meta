# Authoring characters and recaps (the CC BY-SA layer)

This guide covers the **expressive layer** of the database: community-authored
**character** entries and **recaps** ("story so far" summaries). It is separate
from [CONTRIBUTING.md](CONTRIBUTING.md), which covers the factual CC0 core
(works, recordings, people, series). Read [LICENSING.md](LICENSING.md) first -
this layer is **CC BY-SA 3.0**, not CC0, and it carries real copyright
obligations the core does not.

If you are filling out a whole series, do the CC0 core first (the works,
recordings, people, and series must already exist and validate) and add these
sidecars on top.

## The two files

Both are **per-work sidecars** that live inside the work's directory:

```
data/works/<shard>/<work-slug>/characters.json   # the cast, spoiler-tagged
data/works/<shard>/<work-slug>/recaps.json       # position-keyed "story so far"
```

`<shard>` is the first two characters of the **work** slug (the same shard the
`work.json` is under). Each file carries `work` (the parent work slug, which
must equal the directory), `license` (**must** be `"CC-BY-SA-3.0"`), and
`sources`.

### characters.json

```json
{
  "work": "a-deadly-education",
  "license": "CC-BY-SA-3.0",
  "sources": [{ "type": "community" }],
  "characters": [
    {
      "id": "el",
      "name": "El",
      "aliases": ["Galadriel Higgins"],
      "role": "protagonist",
      "reveal": { "chapter": 1 },
      "description": "A senior at the Scholomance with an affinity for mass destruction she refuses to use, scraping through on spite and hard-won craft.",
      "xref": { "wikidata": "Q..." }
    }
  ]
}
```

- **`id`** - a slug, unique **within this file** (not globally: two different
  works may each have a `narrator` or a `the-king`). Derive it from the name.
- **`name`** - the character's primary name as a reader of *this* book would know
  it.
- **`aliases`** (optional) - other names/titles. Omit the key if there are none.
- **`role`** (optional) - one of `protagonist`, `antagonist`, `supporting`,
  `minor`. Omit if genuinely unclear.
- **`reveal`** (required) - the position where the character is first
  meaningfully introduced **in this book** (see Positions below).
- **`description`** (optional but expected) - your own words, **≤ 1500
  characters** (see Copyright).
- **`xref`** (optional) - `wikidata` (a `Q...` id) and/or `goodreads`. A shared
  `wikidata` QID is how the **same character across a series** is linked: each
  book gets its own card, and the QID ties them together. Only include a QID you
  have actually verified points at this character.

### recaps.json

```json
{
  "work": "the-last-graduate",
  "license": "CC-BY-SA-3.0",
  "sources": [{ "type": "community" }],
  "recaps": [
    {
      "through": { "chapter": 0 },
      "scope": "series",
      "text": "Previously: El survived her senior year's first term at the Scholomance and reluctantly let Orion Lake befriend her..."
    },
    {
      "through": { "chapter": 6 },
      "scope": "book",
      "text": "So far this book: ..."
    }
  ]
}
```

- **`through`** (required) - the recap is safe to show to a listener who has
  **finished this chapter** (see Positions).
- **`scope`** (optional) - `book` (recaps only this book) or `series` (also
  covers earlier books). A `chapter: 0` + `scope: "series"` entry is the
  "previously, in earlier books" recap - the single most useful recap in a
  series, shown when someone starts the next book.
- **`text`** (required) - your own words, **≤ 2000 characters**.
- No two recaps in one file may share a `through` chapter.

## Positions (the spoiler model)

Every character and recap is tagged with a **position**: `{ "chapter": N }`.

- `chapter` is the **logical chapter of the work** - the book's own chapter
  numbering, 1-based. It is **edition-independent**: it is NOT the recording's
  track/marker number (recordings vary - some mark Parts or Books, some number
  chapters, some are abridged). Use the chapter as printed in the book.
- `chapter: 0` means front matter or knowledge carried from **earlier books** in
  the series (a character the reader already knows; a "story so far" recap).
- A consumer (the site, the player) compares the listener's current position
  against these numbers and only reveals what is already safe. That is the whole
  point: **get the position right and the spoiler protection is automatic.**

Guidance:

- For a **character**, `reveal.chapter` is where they are first named/introduced
  in *this* book. A returning series character introduced on page one is
  `chapter: 1` (or `0` if they are purely prior-book knowledge at the start).
- Write the **description for a reader who has just reached `reveal.chapter`** -
  do not fold in a late-book twist about that character. If a character's role
  changes dramatically later, that is a *different, later* recap's job, or a
  second character card in a later book.
- For **recaps**, place a `through` at natural catch-up points (act breaks,
  roughly every several chapters, and always a `chapter: 0` series recap for
  book 2+). Each recap may freely reveal everything **up to and including** its
  `through` chapter, and nothing after.

## Copyright (this is the load-bearing part)

Publication is the risk surface, so these rules are hard requirements, not
style:

1. **Own words only. No verbatim or near-verbatim text** from the book, its
   jacket copy, or any wiki. Paraphrase from memory of the facts; do not
   reword sentences from a source. Short, factual, reference-guide phrasing.
2. **Length caps are enforced by the schema** (character description ≤ 1500,
   recap text ≤ 2000 characters) and exist for a legal reason - a dense,
   blow-by-blow plot reconstruction is the danger zone. Summarize, do not
   retell.
3. **Facts, not invention.** Describe only what actually happens in the book. If
   you are unsure of a detail, **omit it** - an omitted field is always better
   than a fabricated one. (This is the same rule as the CC0 core.)
4. **Reference-guide framing**, non-commercial. This is an index to help a
   listener, not a substitute for the book.
5. Rightsholders can request removal per book; keep `sources` accurate so any
   contribution can be audited or retracted.

## Format and validation

- `license` is `"CC-BY-SA-3.0"` and `sources` is `[{ "type": "community" }]`
  (add `ref`/`imported_at` if a specific source applies).
- Run the gate before submitting:
  ```sh
  go run ./cmd/metafmt --write     # canonical formatting (sorted keys, 2-space)
  go run ./cmd/metacheck           # schema + integrity + uniqueness
  ```
  metacheck enforces: valid JSON Schema, `work` matches the directory, the
  parent work exists, character ids are unique within the file, and recap
  positions are unique within the file.

## Checklist

- [ ] The work, its recording(s), author, and narrator already exist and validate.
- [ ] `work` equals the directory slug; file is under the work's shard.
- [ ] `license` is `"CC-BY-SA-3.0"`; `sources` present.
- [ ] Every character has an `id` (unique in file), `name`, and `reveal`.
- [ ] Descriptions/texts are your own words, within the caps, and accurate.
- [ ] Positions use the book's own (logical) chapter numbers; `0` = prior-book.
- [ ] `metafmt --write` and `metacheck` both pass.
