// Pure logic for the guided sidecar builder (/build). Everything here is free of
// React and the DOM so it is unit-testable in the node vitest environment: the
// editor-state types, name -> slug derivation, per-card/per-entry validation, the
// canonical JSON serialization (sorted keys, 2-space, trailing LF - matching what
// metafmt --write produces), the sibling-seeding transform, and the series
// position ordering used to find the nearest lower-position book to seed from.
//
// The two output shapes mirror schema/characters.schema.json and
// schema/recaps.schema.json exactly (both the CC BY-SA layer). Optional fields
// are OMITTED when empty rather than emitted null/empty, so the output validates.

import type { Character, Position } from './api'

// The output shapes reuse the wire Position (both mirror the schema's
// edition-independent chapter position); re-exported so builder consumers
// need not also import from api.
export type { Position }

// --- Editor state ---------------------------------------------------------

/** Derived from the wire Character so the role union can never drift from it. */
export type CharacterRole = NonNullable<Character['role']>
export type RecapScope = 'book' | 'series'

/** One editable character card. `reveal` is the raw text field (parsed to an int
    on output/validation); `role`/`scope` use '' for "none". `seeded` marks a card
    copied from an earlier book so the UI can nudge a per-book re-check. */
export interface CharacterDraft {
  id: string
  name: string
  aliasesText: string
  role: CharacterRole | ''
  reveal: string
  description: string
  wikidata: string
  seeded: boolean
}

/** One editable recap entry. `through` is the raw chapter field. */
export interface RecapEntryDraft {
  through: string
  scope: RecapScope | ''
  text: string
}

/** The whole recaps editor: the chaptered entries plus the two optional
    whole-book summaries. */
export interface RecapsDraft {
  entries: RecapEntryDraft[]
  inShort: string
  ending: string
}

/** Schema length caps (characters), enforced before download/issue. */
export const CAPS = {
  description: 1500,
  recapText: 3000,
  inShort: 1500,
  ending: 2000,
} as const

/** The schema's maxLength on id slugs (characters.schema.json). */
export const MAX_SLUG_LEN = 100

const SLUG_RE = /^[a-z0-9]+(-[a-z0-9]+)*$/
const QID_RE = /^Q\d+$/

export function emptyCharacterDraft(): CharacterDraft {
  return {
    id: '',
    name: '',
    aliasesText: '',
    role: '',
    reveal: '',
    description: '',
    wikidata: '',
    seeded: false,
  }
}

export function emptyRecapEntry(): RecapEntryDraft {
  return { through: '', scope: '', text: '' }
}

export function emptyRecapsDraft(): RecapsDraft {
  return { entries: [emptyRecapEntry()], inShort: '', ending: '' }
}

// --- Slug + field parsing -------------------------------------------------

/** Derive a valid character-id slug from a display name: lowercase, strip
    diacritics, collapse every run of non-alphanumerics to a single hyphen, and
    trim leading/trailing hyphens. Over-long names are truncated to the schema's
    slug cap, cutting at a hyphen boundary where possible so no word is chopped.
    Produces '' when nothing usable remains. */
export function slugify(name: string): string {
  const slug = name
    .normalize('NFKD')
    .replace(/[\u0300-\u036f]/g, '')
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
  if (slug.length <= MAX_SLUG_LEN) return slug
  // The cap falls exactly on a word boundary: keep the full head.
  if (slug[MAX_SLUG_LEN] === '-') return slug.slice(0, MAX_SLUG_LEN)
  const head = slug.slice(0, MAX_SLUG_LEN)
  const lastHyphen = head.lastIndexOf('-')
  // Cut at the last whole word inside the cap; a single giant word hard-cuts.
  return (lastHyphen > 0 ? head.slice(0, lastHyphen) : head).replace(/-+$/, '')
}

/** Format AND length: the schema caps slugs at MAX_SLUG_LEN characters. */
export function isValidSlug(s: string): boolean {
  return s.length <= MAX_SLUG_LEN && SLUG_RE.test(s)
}

/** Parse a chapter field: a non-negative whole number, else null. Rejects
    decimals, signs and blanks so reveal/through are always clean ints >= 0. */
export function parseChapter(s: string): number | null {
  const t = s.trim()
  if (!/^\d+$/.test(t)) return null
  return Number.parseInt(t, 10)
}

/** Split the comma-separated aliases field into trimmed, non-empty names. */
export function parseAliases(text: string): string[] {
  return text
    .split(',')
    .map((s) => s.trim())
    .filter((s) => s.length > 0)
}

// --- Validation -----------------------------------------------------------

export interface CharacterCardErrors {
  id?: string
  name?: string
  reveal?: string
  description?: string
  wikidata?: string
}

export interface CharactersValidation {
  cards: CharacterCardErrors[]
  form: string[]
  ok: boolean
}

/** Validate the character editor. Errors are returned per-card (keyed by field,
    for inline display) plus a form-level list; duplicate ids surface on every
    card that shares the id. Length is measured on the raw value (>= the trimmed
    output), so passing here guarantees the output validates. */
export function validateCharacters(drafts: CharacterDraft[]): CharactersValidation {
  const cards: CharacterCardErrors[] = drafts.map(() => ({}))
  const form: string[] = []
  if (drafts.length === 0) form.push('Add at least one character.')

  const idCounts = new Map<string, number>()
  for (const d of drafts) {
    const id = d.id.trim()
    if (id) idCounts.set(id, (idCounts.get(id) ?? 0) + 1)
  }

  drafts.forEach((d, i) => {
    const e = cards[i]
    const id = d.id.trim()
    if (!id) e.id = 'An id is required.'
    else if (id.length > MAX_SLUG_LEN) e.id = `Over the ${MAX_SLUG_LEN}-character cap.`
    else if (!isValidSlug(id)) e.id = 'Use lowercase letters, numbers and single hyphens.'
    else if ((idCounts.get(id) ?? 0) > 1) e.id = 'Duplicate id - each must be unique in the file.'

    if (!d.name.trim()) e.name = 'A name is required.'
    if (parseChapter(d.reveal) === null) e.reveal = 'Enter a whole chapter number (0 or more).'
    if (d.description.length > CAPS.description) {
      e.description = `Over the ${CAPS.description}-character cap.`
    }
    const wd = d.wikidata.trim()
    if (wd && !QID_RE.test(wd)) e.wikidata = 'A Wikidata QID looks like Q12345.'
  })

  const ok = form.length === 0 && cards.every((e) => Object.keys(e).length === 0)
  return { cards, form, ok }
}

export interface RecapEntryErrors {
  through?: string
  text?: string
}

export interface RecapsValidation {
  entries: RecapEntryErrors[]
  inShort?: string
  ending?: string
  form: string[]
  ok: boolean
}

/** Validate the recaps editor: each entry needs a unique whole through-chapter
    and non-empty text within the cap; the optional summaries are cap-checked. */
export function validateRecaps(draft: RecapsDraft): RecapsValidation {
  const entries: RecapEntryErrors[] = draft.entries.map(() => ({}))
  const form: string[] = []
  if (draft.entries.length === 0) form.push('Add at least one recap entry.')

  const throughCounts = new Map<number, number>()
  for (const e of draft.entries) {
    const ch = parseChapter(e.through)
    if (ch !== null) throughCounts.set(ch, (throughCounts.get(ch) ?? 0) + 1)
  }

  draft.entries.forEach((e, i) => {
    const err = entries[i]
    const ch = parseChapter(e.through)
    if (ch === null) err.through = 'Enter a whole chapter number (0 or more).'
    else if ((throughCounts.get(ch) ?? 0) > 1) {
      err.through = 'Duplicate through chapter - each must be unique.'
    }
    if (!e.text.trim()) err.text = 'Recap text is required.'
    else if (e.text.length > CAPS.recapText) err.text = `Over the ${CAPS.recapText}-character cap.`
  })

  const inShort =
    draft.inShort.length > CAPS.inShort ? `Over the ${CAPS.inShort}-character cap.` : undefined
  const ending =
    draft.ending.length > CAPS.ending ? `Over the ${CAPS.ending}-character cap.` : undefined

  const ok =
    form.length === 0 &&
    !inShort &&
    !ending &&
    entries.every((e) => Object.keys(e).length === 0)
  return { entries, inShort, ending, form, ok }
}

// --- Output shapes + object builders --------------------------------------

export interface CharacterOut {
  id: string
  name: string
  aliases?: string[]
  role?: CharacterRole
  reveal: Position
  description?: string
  xref?: { wikidata?: string }
}

export interface CharactersFile {
  work: string
  characters: CharacterOut[]
  license: 'CC-BY-SA-3.0'
  sources: { type: string }[]
}

export interface RecapOut {
  through: Position
  scope?: RecapScope
  text: string
}

export interface RecapsFile {
  work: string
  recaps: RecapOut[]
  in_short?: string
  ending?: string
  license: 'CC-BY-SA-3.0'
  sources: { type: string }[]
}

/** Build the characters.json object for a work from the editor drafts, omitting
    every empty optional field and trimming the emitted strings. */
export function buildCharactersObject(workId: string, drafts: CharacterDraft[]): CharactersFile {
  return {
    work: workId,
    license: 'CC-BY-SA-3.0',
    sources: [{ type: 'community' }],
    characters: drafts.map((d) => {
      const out: CharacterOut = {
        id: d.id.trim(),
        name: d.name.trim(),
        reveal: { chapter: parseChapter(d.reveal) ?? 0 },
      }
      const aliases = parseAliases(d.aliasesText)
      if (aliases.length) out.aliases = aliases
      if (d.role) out.role = d.role
      const desc = d.description.trim()
      if (desc) out.description = desc
      const wd = d.wikidata.trim()
      if (wd) out.xref = { wikidata: wd }
      return out
    }),
  }
}

/** Build the recaps.json object for a work from the editor draft. */
export function buildRecapsObject(workId: string, draft: RecapsDraft): RecapsFile {
  const file: RecapsFile = {
    work: workId,
    license: 'CC-BY-SA-3.0',
    sources: [{ type: 'community' }],
    recaps: draft.entries.map((e) => {
      const out: RecapOut = {
        through: { chapter: parseChapter(e.through) ?? 0 },
        text: e.text.trim(),
      }
      if (e.scope) out.scope = e.scope
      return out
    }),
  }
  const inShort = draft.inShort.trim()
  if (inShort) file.in_short = inShort
  const ending = draft.ending.trim()
  if (ending) file.ending = ending
  return file
}

// --- Canonical serialization ----------------------------------------------

function sortKeys(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(sortKeys)
  if (value !== null && typeof value === 'object') {
    const out: Record<string, unknown> = {}
    for (const key of Object.keys(value as Record<string, unknown>).sort()) {
      out[key] = sortKeys((value as Record<string, unknown>)[key])
    }
    return out
  }
  return value
}

/** Serialize to the repo's canonical form: recursively sorted keys, 2-space
    indent, trailing newline - byte-identical to what metafmt --write emits, so a
    downloaded file needs no reformatting before it is committed. */
export function serializeCanonical(value: unknown): string {
  return JSON.stringify(sortKeys(value), null, 2) + '\n'
}

// --- Sibling seeding ------------------------------------------------------

/** Turn an earlier book's cast into seed drafts for this book: keep the stable
    identity (id/name/aliases/role/wikidata xref) but CLEAR each description and
    reset reveal to 0, flagging the card `seeded` so the UI prompts a per-book
    re-check. This encodes AUTHORING's re-describe-per-book model - the same
    character gets a fresh, spoiler-bounded card in every book. */
export function seedCharactersFromSibling(chars: Character[]): CharacterDraft[] {
  return chars.map((c) => ({
    id: c.id,
    name: c.name,
    aliasesText: (c.aliases ?? []).join(', '),
    role: c.role ?? '',
    reveal: '0',
    description: '',
    wikidata: c.xref?.wikidata ?? '',
    seeded: true,
  }))
}

// --- Series position ordering ---------------------------------------------

/** Numeric sort value of a series position string. Positions are strings that
    may be decimals ("2.5") or omnibus ranges ("1-3.5"); the first number is used
    for ordering (a range sorts by its start). Returns null when unparseable. */
export function parsePositionValue(pos: string): number | null {
  const m = /\d+(\.\d+)?/.exec(pos)
  return m ? Number.parseFloat(m[0]) : null
}

/** Find the series entry with the highest position strictly below the current
    work's position - the nearest earlier book, whose cast we can seed from.
    Returns null when the current work is not in the list, has no parseable
    position, or is the first book. */
export function nearestLowerSibling<T extends { position: string; work: { id: string } }>(
  works: T[],
  currentId: string
): T | null {
  const current = works.find((w) => w.work.id === currentId)
  if (!current) return null
  const cur = parsePositionValue(current.position)
  if (cur === null) return null

  let best: T | null = null
  let bestVal = Number.NEGATIVE_INFINITY
  for (const w of works) {
    if (w.work.id === currentId) continue
    const v = parsePositionValue(w.position)
    if (v === null) continue
    if (v < cur && v > bestVal) {
      best = w
      bestVal = v
    }
  }
  return best
}

/** Move the item at `index` one slot in `dir` (-1 up, +1 down), returning a new
    array; a no-op (returns the same array reference) at either end. */
export function moveItem<T>(arr: T[], index: number, dir: -1 | 1): T[] {
  const target = index + dir
  if (target < 0 || target >= arr.length) return arr
  const next = arr.slice()
  ;[next[index], next[target]] = [next[target], next[index]]
  return next
}
