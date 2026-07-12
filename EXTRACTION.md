# Extracting characters and recaps from a book you own

This guide is the sibling of [AUTHORING.md](AUTHORING.md). AUTHORING.md defines
WHAT a good characters/recaps sidecar is (the spoiler model, the copyright
rules, the caps); this guide is a repeatable process for PRODUCING one from the
text of a book you own, using a chain of model passes plus the `metaextract`
tool. Read AUTHORING.md first - everything there applies to the output of this
process.

Use this process when authoring from model memory is too weak: long serials,
less-famous titles, or any book where chapter-accurate positions matter and
recall cannot be trusted. Working from the text solves both problems at once:
positions come from the book's own chapter list, and every fact in the output
can be traced to the chapter it appears in.

## Ground rules

- **The book text never enters the repository.** Not the epub, not extracted
  chapter text, not fact notes derived from it. Work in a temporary directory;
  only the two derived CC BY-SA sidecar files are ever committed. (This is the
  same rule as personal library exports: the repo holds derived metadata,
  never source material.)
- The input is **an epub you have**. How you came to have it is out of scope
  for this guide; converting or acquiring books is not covered here.
- The CC0 core must exist first: the work, at least one recording, the people,
  and the series entry (see CONTRIBUTING.md). Sidecars attach to a work.
- Everything in AUTHORING.md's Copyright section applies to the final output,
  and step 4 checks it mechanically.

## The pipeline at a glance

```
epub
 └─ 1. split      metaextract split (mechanical: chapter text + manifest)
 └─ 2. fact pass  rolling per-chapter notes, in order (cheap model)
 └─ 3. synthesis  characters.json + recaps.json from the NOTES ONLY (strong model)
 └─ 4. verify     metafmt + metacheck + metaextract ngram + a spoiler audit
```

The load-bearing design decision is between steps 2 and 3: the synthesis stage
never sees the book text, only the chapter-attributed fact notes. That makes
spoiler bounds auditable (every fact in a card or recap traces to a noted
chapter) and makes verbatim overlap with the text structurally impossible -
the mechanical check in step 4 then proves it.

## Step 1: split the epub

```sh
go run ./cmd/metaextract split --epub book.epub -o /tmp/book-split
```

This writes one plain-text file per spine document (`001.txt`, ...) plus
`manifest.json`: each doc's toc label, a conservatively inferred chapter
number (only labels like "Chapter 12" or "12" infer; anything else stays
unnumbered), and word counts.

Review the manifest before going further - it IS the position model:

- The chaptered docs should match the book's real chapter count. If the toc
  has no usable labels (or one file holds many chapters), the manifest carries
  warnings; you will need to split or map chapters by hand before step 2.
- Unlabeled docs at the edges are front matter and back matter. Check the back
  matter: epubs often carry a teaser excerpt of ANOTHER book (the Killing
  Floor epub ends with the opening chapter of Die Trying). Excerpts must not
  reach the fact pass.
- Position rule reminder from AUTHORING.md: positions are the book's own
  logical chapter numbers, edition-independent. If the book has Parts or
  unnumbered chapters, decide the mapping now and keep it fixed for the whole
  run.

## Step 2: the rolling fact pass

A cheap, fast model reads the chapters IN ORDER and writes per-chapter notes
plus a cumulative "what the reader knows" sheet. Chunk the book (8-9 chapters
per chunk works well; one chunk per model session/agent) and run the chunks
SEQUENTIALLY - each chunk starts from the previous chunk's cumulative sheet,
never from the raw earlier chapters.

Per chapter, the notes record:

- EVENTS: 6-15 chronological bullets; outcomes stated plainly.
- INTRODUCED: characters first meaningfully appearing in this chapter, as the
  reader knows them AT THAT POINT (no later knowledge).
- DEVELOPMENTS: new facts about already-known characters.
- STATE: the protagonist's situation, reader beliefs, open questions.
- ACT BREAK CANDIDATE where the chapter ends on a natural catch-up point
  (these become recap through-points in step 3).

The cumulative sheet carries ROSTER (name, first-appearance chapter, status,
chapter of death where relevant), REVEALS (chapter -> reveal timeline), and
THREADS. The final chunk also writes the whole-book sheet with a plain ENDING
statement (it feeds `in_short`/`ending`).

Prompt template: see "The prompt set" below.

Rules that make step 3 safe:

- Own words only, no quotes from the book at all - the notes feed published
  text downstream.
- Exact chapter attribution; an event belongs to the chapter where it is
  confirmed on the page.
- Never read ahead of the chunk; never re-read earlier chapters (trust the
  sheet - that is what keeps attribution honest).

## Step 3: synthesis

A strong model turns the notes - and ONLY the notes, never the book text -
into `characters.json` and `recaps.json` per AUTHORING.md. Points that need
deciding per book:

- **Cast size**: 12-18 cards suits a typical novel; cover the cast a listener
  would look up, skip one-scene walk-ons.
- **Twist characters**: a `role` of `antagonist` on a character whose betrayal
  is a late reveal leaks the twist at their (early) reveal chapter. Use the
  role they APPEAR to have at reveal, or omit `role`.
- **Through-points**: pick 6-8 from the fact pass's act-break candidates,
  roughly every 4-6 chapters; the final entry is through the last chapter and
  states the actual ending plainly.
- **Book 1 vs book N**: only book 2+ gets the `chapter: 0` + `scope: "series"`
  "previously" recap. A series opener has none.
- Both files: `license` `"CC-BY-SA-3.0"`, `sources` `[{"type": "community"}]`.

Prompt template: see "The prompt set" below.

## Step 4: verify (never skip this)

Mechanical, from the repo root:

```sh
go run ./cmd/metafmt --write
go run ./cmd/metacheck
go run ./cmd/metaextract ngram --source /tmp/book-split \
  data/works/<shard>/<slug>/characters.json \
  data/works/<shard>/<slug>/recaps.json
```

`ngram` fails (exit 1) on any 8-word near-verbatim overlap between an authored
description/recap and the book text, after case/punctuation normalization.
A hit means a sentence needs rewriting in genuinely fresh words.

Then the spoiler audit: a SEPARATE model session (not the one that wrote the
sidecars) checks every card and recap against the fact notes:

- No fact in a description is attributed to a chapter later than the card's
  `reveal`.
- No fact in a recap is attributed to a chapter later than its `through`.
- Statuses, deaths, and the ending are accurate to the notes.
- Caps, voice, and the AUTHORING.md checklist hold.

Every extraction and authoring wave so far has had real defects caught in this
pass (leaked late-book facts, misattributed deaths, timelines running early).
Treat a clean audit as the exit criterion, not a formality.

## The prompt set

The templates below are the ones the exemplar run used, generalized. `<...>`
marks placeholders. They are model-agnostic; run each as a fresh session (or
agent) with file access to the working directory.

The templates deliberately restate the length caps and core rules so each
prompt is a self-contained copy-paste artifact - but AUTHORING.md and the
schemas own those numbers. If a cap or rule changes there, update these
templates in the same change.

### Step 2: fact pass (one prompt per chunk, run sequentially)

```
You are stage <K> of a rolling fact-extraction pass over the novel "<TITLE>"
by <AUTHOR> (<CHAPTER-COUNT> chapters<, first-person narration by X if so>).
Your notes are the ground truth for later spoiler-safe character cards and
recaps - exact chapter attribution is the whole point.

[Stages after the first:]
FIRST read the cumulative reader-knowledge sheet from the previous stage:
<dir>/facts/knowledge-through-ch<M>.md

THEN read, in order, chapters <M+1> through <N>:
<dir>/chapters/ch<M+1>.txt ... ch<N>.txt

Write TWO files:

1. <dir>/facts/facts-ch<M+1>-<N>.md - a section per chapter (## Chapter N),
   each with:
   - EVENTS: 6-15 chronological bullets of what happens. State outcomes
     plainly (deaths, identifications, reveals). Facts only.
   - INTRODUCED: characters first meaningfully appearing in this chapter -
     the name the reader knows them by AT THIS POINT, who they appear to be
     at introduction only (no later knowledge), aliases/titles used.
   - DEVELOPMENTS: new facts about already-introduced characters (cross-check
     the roster - do not re-introduce someone already known).
   - STATE: 2-4 bullets - protagonist situation, current reader beliefs,
     open questions.
   - If the chapter ends on a major turning point / natural act break:
     ACT BREAK CANDIDATE: <why>.

2. <dir>/facts/knowledge-through-ch<N>.md - the FULL updated cumulative sheet
   as of the END of chapter <N> (carry forward everything still true, update
   statuses, add new characters/reveals/threads):
   - ROSTER: every named character - name, aliases, first-appearance chapter,
     apparent role, current status (alive/dead with chapter of death), what
     the reader knows about them.
   - REVEALS: timeline of major reveals (chapter -> what was revealed).
   - THREADS: open questions/mysteries.

HARD RULES:
- OWN WORDS ONLY. Never copy or lightly reword sentences from the text - no
  quotes from the book at all. Short factual reference-guide phrasing.
- Neutral voice, no opinions about the book.
- Exact chapter attribution; an event is attributed to the chapter where it
  is confirmed on the page.
- Hyphens only, never em dashes.
- Do not read beyond ch<N>.txt, and do not re-read earlier chapters - trust
  the sheet.
```

For the final chunk, additionally have it write `knowledge-final.md`: the
whole-book ROSTER and REVEALS plus an ENDING section - a plain, factual
statement of how the book ends, where every surviving major player stands,
and which threads stay open into the next book.

### Step 3: synthesis (one prompt)

```
You are the synthesis stage of the extraction pipeline. Author the CC BY-SA
sidecars for "<TITLE>" (<series position; chapter count; narration>) in the
audiosilo-meta repo.

THE CONTRACT: read AUTHORING.md first and follow it exactly (positions and
spoiler model, copyright caps, voice). Skim one existing exemplar pair of
characters.json + recaps.json for shape.

YOUR ONLY SOURCE MATERIAL is the fact notes (you do NOT have the book text -
deliberate: it makes spoiler bounds auditable and verbatim overlap impossible
by construction). Read all of <dir>/facts/*.md.

Write data/works/<shard>/<slug>/characters.json and recaps.json (the parent
work must already exist):

characters.json
- 12-18 cards covering the meaningful cast; skip one-scene walk-ons.
- reveal.chapter: first meaningful introduction per the facts (exact).
- role ONLY where it does not spoil: a late-revealed traitor gets the role
  they APPEAR to have at reveal, or no role at all.
- description: for a reader who has JUST reached the reveal chapter - no
  knowledge from any later chapter. Target 200-500 chars (cap 1500).
- No xref unless verified.

recaps.json
- Book 2+ gets a chapter-0 scope-series "previously" recap; a series opener
  gets none.
- 6-8 through-points chosen from the fact pass's ACT BREAK candidates,
  roughly every 4-6 chapters; each entry is a cumulative catch-up revealing
  ONLY facts attributed to chapters <= its through chapter. Target ~150-300
  words each (cap 3000 chars).
- The FINAL entry is through the last chapter and states the actual ending
  plainly - outcomes, deaths, where the protagonist goes. Never a tease.
- in_short (<= 1500 chars): the whole arc in one paragraph, ending included.
- ending (<= 2000 chars): the sequel-handoff state - where every surviving
  major player stands, which threads stay open.

HARD RULES: license CC-BY-SA-3.0, sources [{"type": "community"}]; fresh
prose in your own words (an 8-word-shingle check against the full text will
be run on the output); neutral reference-guide voice; hyphens never em
dashes; when a fact's chapter attribution is uncertain, leave the fact out.

Then run: go run ./cmd/metafmt --write && go run ./cmd/metacheck - both must
pass.
```

### Step 4: the spoiler audit (one prompt, a fresh session - never the author)

```
You are an independent ADVERSARIAL auditor. Another agent authored the
spoiler-tagged sidecars for "<TITLE>"; find defects, do not approve. Assume
defects exist until proven otherwise.

Read AUTHORING.md, then audit data/works/<shard>/<slug>/characters.json and
recaps.json against the ground truth in <dir>/facts/*.md.

Checks, in priority order:
1. SPOILER LEAKS: every claim in every description traces to a chapter <=
   the card's reveal; every claim in every recap to a chapter <= its through.
   Roles/aliases must not leak later twists. "Implies" counts as a leak.
2. REVEAL/THROUGH CORRECTNESS: reveal = first meaningful introduction (too
   late is also a defect); through-points at sensible act breaks.
3. ACCURACY: names, statuses, chapter-of-death, the ending - consistent with
   the facts; flag any claim not present in the facts at all.
4. COVERAGE: look-up-worthy characters with no card; spans with no recap.
5. CONTRACT: voice, caps, plain-stated ending, no em dashes, series-opener
   rule, license/sources fields.

Do NOT rewrite the files. Report numbered findings, each with severity
(BLOCKER / FIX / NIT), locus, offending text, fact-file evidence (which
chapter the fact belongs to), and the suggested correction. Say explicitly
when a category is clean.
```

Feed the findings back to the synthesis session (or a fresh one) to apply,
then re-run the step 4 mechanical checks. Iterate until the audit is clean of
BLOCKER/FIX findings.

## Worked example: Killing Floor

The pipeline was validated end-to-end on Killing Floor (Jack Reacher #1,
34 chapters, ~142k words of chapter text):

- split: 43 spine docs -> 34 toc-labeled chapters inferred exactly; front
  matter and a bonus excerpt of the NEXT book correctly left unlabeled (and
  excluded from the fact pass).
- fact pass: 4 sequential chunks (ch 1-9, 10-18, 19-26, 27-34), each ~30-40k
  words of reading, handing forward a cumulative knowledge sheet.
- synthesis: 16 character cards + 8 recap through-points (6, 9, 13, 18, 23,
  26, 30, 34) + in_short + ending, from the notes alone.
- audit: found 1 genuine cross-chapter leak (a ch13 fact in a ch9 card), a
  coverage gap, and 3 pre-reveal characterization nits - all fixed and
  re-verified (final set: 20 cards). The mechanical ngram check found 0
  overlaps.

The full prompts above are the ones this run used.
