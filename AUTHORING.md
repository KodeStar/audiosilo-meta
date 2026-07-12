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

To PRODUCE these sidecars from the text of a book you own (rather than from
memory), follow the extraction pipeline in [EXTRACTION.md](EXTRACTION.md) -
it automates chapter-accurate positions and enforces the no-verbatim rule
mechanically. Everything in this guide still applies to its output.

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
  "in_short": "The whole book in one paragraph, ending included, for someone about to start the next one.",
  "ending": "How the book closes: where every major player stands, and which threads stay open.",
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

A recaps file has two layers: **chaptered `recaps`** (spoiler-gated "story so
far" entries, the differentiator) and two optional **whole-book summaries**
(`in_short` and `ending`) for a reader who has already finished the book and
wants a fast refresher before the sequel. Structure your entries like a
dedicated recap site, but keep the neutral reference-guide **voice** (see
Copyright) - never their jokey, editorializing tone.

- **`recaps[].through`** (required) - the recap is safe to show to a listener who
  has **finished this chapter** (see Positions).
- **`recaps[].scope`** (optional) - `book` (recaps only this book) or `series`
  (also covers earlier books). A `chapter: 0` + `scope: "series"` entry is the
  "previously, in earlier books" recap - the single most useful recap in a
  series, shown when someone starts the next book.
- **`recaps[].text`** (required) - your own words, **≤ 3000 characters**, target
  **~150-300 words** per entry. The cap is **per entry** and the entry count is
  unbounded: recap capacity scales with the book by **adding through-points**,
  never by cramming more into one entry. Scale the count with the book's length
  and density - a short middle-grade book might carry 3-4 points; a 30-60+ hour
  epic or serial volume 12-20 or more. Rule of thumb: one through-point every
  ~5-10 chapters or ~2-4 listening hours, at natural act/arc breaks. An entry
  bumping the cap is the signal to **split** its span into two through-points,
  not to compress harder. The **final** chaptered entry must carry through the
  **actual ending** - state outcomes plainly; "reveals just enough to..."
  teasing is a defect, not a style.
- No two recaps in one file may share a `through` chapter.
- **`in_short`** (optional) - the **whole arc in one paragraph, ending
  included**: the fastest possible refresher for someone about to start the next
  book. Own words, **≤ 1500 characters**, target **~120-200 words** (one
  paragraph).
- **`ending`** (optional) - **how the book ends**, the sequel-handoff state:
  where every major player stands at the close and which threads stay open.
  State it plainly - never a tease. Own words, **≤ 2000 characters**, target
  **~150-300 words**. It is deliberately capped tighter than a chaptered `text`
  entry: a crisp handoff into the next book, **not** another full recap.
- `in_short` and `ending` describe the finished book, so they are inherently
  full-spoiler; a consumer only shows them to a reader who has completed the
  book (or who opts in).

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
- For **recaps**, place a `through` at natural catch-up points (see the
  recaps.json section for how many and how often, and always include a
  `chapter: 0` series recap for book 2+). Each recap may freely reveal
  everything **up to and including** its `through` chapter, and nothing after.

## Copyright (this is the load-bearing part)

Publication is the risk surface, so these rules are hard requirements, not
style:

1. **Own words only. No verbatim or near-verbatim text** from the book, its
   jacket copy, or any wiki. Paraphrase from memory of the facts; do not
   reword sentences from a source. Short, factual, reference-guide phrasing.
2. **Length caps are enforced by the schema** (character description ≤ 1500,
   recap `text` ≤ 3000, `in_short` ≤ 1500, `ending` ≤ 2000 characters) and exist
   for a legal reason - a dense, blow-by-blow plot reconstruction is the danger
   zone. The raised recap cap buys **depth per entry**, not a licence to retell
   the book scene by scene. Summarize, do not retell.
3. **Neutral reference-guide voice.** Recap blogs are chatty and opinionated
   ("X is an arse"); we are not. No jokes, no editorializing, no profanity, no
   value judgements about the book or its characters - just what happens, stated
   plainly. Take the *structure* from those sites, never the tone.
4. **Facts, not invention.** Describe only what actually happens in the book. If
   you are unsure of a detail, **omit it** - an omitted field is always better
   than a fabricated one. (This is the same rule as the CC0 core.)
5. **Reference-guide framing**, non-commercial. This is an index to help a
   listener, not a substitute for the book.
6. Rightsholders can request removal per book; keep `sources` accurate so any
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
- [ ] Descriptions/texts are your own words, within the caps, and accurate
      (character ≤ 1500, recap `text` ≤ 3000, `in_short` ≤ 1500, `ending` ≤ 2000).
- [ ] The final chaptered recap states the **actual ending**, not a tease; `ending`
      and `in_short` (if present) carry the real outcome plainly.
- [ ] Voice is neutral reference-guide - no jokes, editorializing, or profanity.
- [ ] Positions use the book's own (logical) chapter numbers; `0` = prior-book.
- [ ] `metafmt --write` and `metacheck` both pass.
