// Pure, framework-free parser for a user's library export (OpenAudible today).
// Everything here runs in the browser on a file the user drops - the file never
// leaves the device. Kept free of React and DOM so it is independently testable.
//
// The field mapping mirrors the Go importer (the source of truth for how an
// OpenAudible books.json entry becomes a work + recording):
//   ../../../internal/importer/openaudible.go  (loose coercion helpers)
//   ../../../internal/importer/mapping.go       (language/region/sequence/name rules)
//   ../../../internal/importer/importer.go       (title, runtime, cover mapping)
// Only factual fields are read; personal/marketing fields are ignored (see
// LICENSING.md).

export interface ParsedBook {
  asin?: string // normalized, else undefined
  isbn?: string // digits only (X allowed last for ISBN-10), else undefined
  title: string
  subtitle?: string
  authors: string[]
  narrators: string[]
  seriesName?: string
  seriesPosition?: string // validated position string, else undefined
  language?: string // ISO code if mappable, else undefined
  languageRaw?: string // the raw word, for display when unmappable
  runtimeMin?: number
  releaseDate?: string // YYYY-MM-DD if it matches, else undefined
  publisher?: string
  region?: string // lowercased marketplace if known (us/uk/...)
  coverUrl?: string // only if https://
  chapterCount?: number
  abridged?: boolean // tri-state: undefined when unknown
  raw: Record<string, unknown> // the original entry (for the factual-subset download)
}

export type ExportFormat = 'openaudible' | 'libation' | 'unknown'

export interface ParseOutcome {
  format: ExportFormat
  books: ParsedBook[] // empty unless format === 'openaudible'
}

// --- Coercion helpers (mirror the Go coerceStr/coerceInt/coerceBoolPtr) -----

function coerceStr(v: unknown): string {
  if (v === null || v === undefined) return ''
  if (typeof v === 'string') return v.trim()
  if (typeof v === 'number') return Number.isFinite(v) ? String(v) : ''
  if (typeof v === 'boolean') return v ? 'true' : 'false'
  return ''
}

function coerceInt(v: unknown): number | undefined {
  if (typeof v === 'number') return Number.isFinite(v) ? Math.trunc(v) : undefined
  if (typeof v === 'string') {
    const s = v.trim()
    if (s === '') return undefined
    const n = Number(s)
    return Number.isFinite(n) ? Math.trunc(n) : undefined
  }
  return undefined
}

// Tri-state boolean: a real true/false (bool or the strings "true"/"false"),
// otherwise undefined (absent/unknown - so importers omit `abridged`).
function coerceBool(v: unknown): boolean | undefined {
  if (typeof v === 'boolean') return v
  if (typeof v === 'string') {
    const s = v.trim().toLowerCase()
    if (s === 'true') return true
    if (s === 'false') return false
  }
  return undefined
}

// --- Field rules (mirror mapping.go) ----------------------------------------

// Word -> ISO 639-1 code; only the languages the importer accepts are mapped.
const LANGUAGE_MAP: Record<string, string> = {
  english: 'en',
  turkish: 'tr',
  german: 'de',
  french: 'fr',
  spanish: 'es',
  italian: 'it',
  japanese: 'ja',
  portuguese: 'pt',
  dutch: 'nl',
  polish: 'pl',
  russian: 'ru',
  chinese: 'zh',
}

// Audible marketplaces the recording schema accepts (recording.schema.json).
const MARKETPLACES = new Set([
  'us',
  'uk',
  'ca',
  'au',
  'de',
  'fr',
  'es',
  'it',
  'jp',
  'in',
  'br',
])

// Trailing " - <role>" credit qualifiers Audible appends to names. Stripped
// ONLY when the role is exactly one of these (case-insensitive) - never an
// arbitrary " - X", since a band/pen name can contain a spaced hyphen.
const ROLE_QUALIFIERS = new Set([
  'translator',
  'introduction',
  'intro',
  'foreword',
  'afterword',
  'preface',
  'editor',
  'illustrator',
  'adaptation',
  'contributor',
  'narrator',
  'ghostwriter',
  'compilation',
])

// A series position: a number or an omnibus range ("1", "2.5", "1-3.5").
const SEQUENCE_PATTERN = /^\d+(\.\d+)?(-\d+(\.\d+)?)?$/
// This client keeps release dates strict (the add-work form wants YYYY-MM-DD).
const DATE_PATTERN = /^\d{4}-\d{2}-\d{2}$/

function stripRoleQualifier(name: string): string {
  const idx = name.lastIndexOf(' - ')
  if (idx < 0) return name
  const role = name.slice(idx + 3).trim().toLowerCase()
  if (!ROLE_QUALIFIERS.has(role)) return name
  const cleaned = name.slice(0, idx).trim()
  return cleaned === '' ? name : cleaned
}

// Split a comma-joined name list, trim each, strip a trailing role qualifier,
// and drop empties.
function splitNames(joined: string): string[] {
  const out: string[] = []
  for (const part of joined.split(',')) {
    const name = part.trim()
    if (name !== '') out.push(stripRoleQualifier(name))
  }
  return out
}

function mapRegion(word: string): string | undefined {
  const r = word.trim().toLowerCase()
  return r !== '' && MARKETPLACES.has(r) ? r : undefined
}

function firstNonEmpty(...vals: string[]): string {
  for (const v of vals) if (v !== '') return v
  return ''
}

// --- Public normalizers -----------------------------------------------------

/** Uppercase + trim; returns the value iff it is a 10-char ASIN, else "". */
export function normalizeAsin(s: string): string {
  const up = s.trim().toUpperCase()
  return /^[A-Z0-9]{10}$/.test(up) ? up : ''
}

/** Strip spaces/hyphens; keep a 10- or 13-char ISBN (X allowed as ISBN-10
    check digit), else "". */
export function normalizeIsbn(s: string): string {
  const stripped = s.replace(/[\s-]/g, '').toUpperCase()
  return /^(\d{9}[\dX]|\d{13})$/.test(stripped) ? stripped : ''
}

// --- Parsing ----------------------------------------------------------------

function isObject(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

function parseBook(raw: Record<string, unknown>): ParsedBook {
  const asin = normalizeAsin(coerceStr(raw['asin']))
  const isbn = normalizeIsbn(coerceStr(raw['isbn']))
  // Default work title is title_short falling back to title (mirrors the Go
  // importer); any distinct subtitle field is carried for display only.
  const title = firstNonEmpty(coerceStr(raw['title_short']), coerceStr(raw['title']))
  const subtitle = coerceStr(raw['subtitle'])
  const authors = splitNames(coerceStr(raw['author']))
  const narrators = splitNames(coerceStr(raw['narrated_by']))
  const seriesName = coerceStr(raw['series_name'])
  const seqRaw = coerceStr(raw['series_sequence'])
  const seriesPosition = SEQUENCE_PATTERN.test(seqRaw) ? seqRaw : undefined
  const languageRaw = coerceStr(raw['language'])
  const language = LANGUAGE_MAP[languageRaw.toLowerCase()]
  const seconds = coerceInt(raw['seconds'])
  const runtimeMin = seconds !== undefined && seconds > 0 ? Math.round(seconds / 60) : undefined
  const releaseRaw = coerceStr(raw['release_date'])
  const releaseDate = DATE_PATTERN.test(releaseRaw) ? releaseRaw : undefined
  const publisher = coerceStr(raw['publisher'])
  const region = mapRegion(coerceStr(raw['region']))
  const imageUrl = coerceStr(raw['image_url'])
  const coverUrl = imageUrl.startsWith('https://') ? imageUrl : undefined
  const chapters = raw['chapters']
  const chapterCount = Array.isArray(chapters) ? chapters.length : undefined
  const abridged = coerceBool(raw['abridged'])

  return {
    asin: asin || undefined,
    isbn: isbn || undefined,
    title,
    subtitle: subtitle || undefined,
    authors,
    narrators,
    seriesName: seriesName || undefined,
    seriesPosition,
    language,
    languageRaw: languageRaw || undefined,
    runtimeMin,
    releaseDate,
    publisher: publisher || undefined,
    region,
    coverUrl,
    chapterCount,
    abridged,
    raw,
  }
}

// Keys that mark an OpenAudible entry (lowercase). Libation uses PascalCase.
const OPENAUDIBLE_KEYS = ['asin', 'narrated_by', 'title_short', 'series_name', 'image_url']
const LIBATION_KEYS = ['Title', 'Authors', 'Narrators', 'AudibleProductId', 'Asin']

// Libation sometimes wraps its list in an object under one of these keys.
const WRAPPER_KEYS = ['Books', 'books', 'Items', 'items', 'Library', 'library']

function extractEntries(data: unknown): unknown[] | null {
  if (Array.isArray(data)) return data
  if (isObject(data)) {
    for (const key of WRAPPER_KEYS) {
      const v = data[key]
      if (Array.isArray(v)) return v
    }
  }
  return null
}

function sampleHasAnyKey(entries: unknown[], keys: string[]): boolean {
  for (const el of entries.slice(0, 20)) {
    if (isObject(el)) {
      for (const k of keys) if (k in el) return true
    }
  }
  return false
}

function detectFormat(data: unknown): ExportFormat {
  const entries = extractEntries(data)
  if (!entries || entries.length === 0) return 'unknown'
  // OpenAudible first: its lowercase keys never appear on a Libation entry.
  if (sampleHasAnyKey(entries, OPENAUDIBLE_KEYS)) return 'openaudible'
  if (sampleHasAnyKey(entries, LIBATION_KEYS)) return 'libation'
  return 'unknown'
}

/**
 * Parse a dropped export. Detects the format and, for an OpenAudible export,
 * returns every entry mapped to a ParsedBook. Libation is detected but not
 * parsed (no verified sample), so its books list is empty and the UI routes it
 * to the issue-form path. Throws a friendly Error on invalid JSON.
 */
export function parseExport(text: string): ParseOutcome {
  let data: unknown
  try {
    data = JSON.parse(text)
  } catch {
    throw new Error(
      'That file is not valid JSON. Make sure you selected your OpenAudible books.json export.'
    )
  }
  const format = detectFormat(data)
  if (format !== 'openaudible') {
    return { format, books: [] }
  }
  const entries = extractEntries(data) ?? []
  const books: ParsedBook[] = []
  for (const el of entries) {
    if (isObject(el)) books.push(parseBook(el))
  }
  return { format, books }
}

// --- Classification (pure, so the diff rules are testable without the UI) ----

/**
 * Split parsed books into those carrying a usable identifier (deduped by
 * asin||isbn, first wins) and those without one. A book with no ASIN or ISBN
 * can never be auto-matched against the database, so it never hits the API.
 */
export function partitionByIdentifier(books: ParsedBook[]): {
  identified: ParsedBook[]
  unidentified: ParsedBook[]
} {
  const seen = new Set<string>()
  const identified: ParsedBook[] = []
  const unidentified: ParsedBook[] = []
  for (const b of books) {
    const id = b.asin ?? b.isbn
    if (!id) {
      unidentified.push(b)
      continue
    }
    if (seen.has(id)) continue
    seen.add(id)
    identified.push(b)
  }
  return { identified, unidentified }
}

/**
 * After a lookup miss (the identifier is not in the database), a book is
 * contributable only when its language mapped to an ISO code the schema accepts
 * - otherwise the importer would skip it, so it counts as "cannot auto-match".
 */
export function isContributableOnMiss(book: ParsedBook): boolean {
  return book.language !== undefined
}

// --- Existing-work matching (new-recording vs new-work routing) --------------

/** A work search candidate (the compact shape the search API returns). */
export interface WorkCandidate {
  id: string
  title: string
  authors: { name: string }[]
}

/** The existing work a missed book turned out to be a new *recording* of. */
export interface WorkMatch {
  id: string
  title: string
}

// A comparison key: lowercase, fold diacritics, and collapse to space-separated
// alphanumeric words. NFKD splits an accented letter into base + a combining
// mark; the mark MUST be stripped to '' (not via the alphanumeric filter, which
// replaces it with a SPACE and so splits "Émile" into "e mile") so an accented
// name folds to the same key as its ASCII spelling ("Émile" == "Emile").
function normKey(s: string): string {
  return s
    .toLowerCase()
    .normalize('NFKD')
    .replace(/[̀-ͯ]/g, '') // combining diacritical marks -> drop
    .replace(/[^a-z0-9]+/g, ' ')
    .trim()
}

// True when one normalized title's token set is contained in the other's - so a
// catalogue work "Skysworn" matches a messy export title "Skysworn - Cradle,
// Book 4". Both must be non-empty.
function titleTokensCompatible(a: string, b: string): boolean {
  const ta = new Set(a.split(' ').filter(Boolean))
  const tb = new Set(b.split(' ').filter(Boolean))
  if (ta.size === 0 || tb.size === 0) return false
  const [small, big] = ta.size <= tb.size ? [ta, tb] : [tb, ta]
  for (const t of small) if (!big.has(t)) return false
  return true
}

/**
 * Decide whether a book that missed the ASIN/ISBN lookup is actually a new
 * *recording* of a work already in the catalogue (so it should route to
 * add-recording, not add-work - it would otherwise create a duplicate work).
 *
 * Prefers an exact-title candidate (its own work) before a looser
 * subset/superset title match, and requires a shared author for any non-exact
 * match (and for an exact match when the book lists authors), so a same-title
 * different-author book is never mistaken for an existing work. Pure. Returns
 * null when there is no confident match, so the caller defaults to a new work.
 */
export function matchExistingWork(
  book: ParsedBook,
  candidates: WorkCandidate[]
): WorkMatch | null {
  const bt = normKey(book.title)
  if (!bt) return null
  const bookAuthors = new Set(book.authors.map(normKey).filter(Boolean))
  const authorShared = (c: WorkCandidate): boolean =>
    bookAuthors.size > 0 && c.authors.some((a) => bookAuthors.has(normKey(a.name)))

  let loose: WorkMatch | null = null
  for (const c of candidates) {
    const ct = normKey(c.title)
    if (ct === bt) {
      if (bookAuthors.size === 0 || authorShared(c)) return { id: c.id, title: c.title }
    } else if (!loose && authorShared(c) && titleTokensCompatible(ct, bt)) {
      loose = { id: c.id, title: c.title }
    }
  }
  return loose
}

/**
 * The canonical grouping/lookup key for an author name. Uses the SAME normalizer
 * as matchExistingWork's author comparison, so the set of authors we search and
 * the authors we match against never disagree (e.g. "Émile Zola" == "Emile Zola").
 */
export function authorKey(name: string): string {
  return normKey(name)
}

/**
 * The distinct author keys across a set of books, each mapped to one display
 * spelling (the first seen) - i.e. the author searches to run, deduped. Pure.
 */
export function authorSearchKeys(books: ParsedBook[]): Map<string, string> {
  const out = new Map<string, string>()
  for (const b of books) {
    for (const a of b.authors) {
      const key = authorKey(a)
      if (key && !out.has(key)) out.set(key, a)
    }
  }
  return out
}

/**
 * The catalogue work candidates for a book: every work by any of its authors,
 * looked up from an author-key -> works map. Pure.
 */
export function candidatesForBook(
  book: ParsedBook,
  worksByAuthor: Map<string, WorkCandidate[]>
): WorkCandidate[] {
  return book.authors.flatMap((a) => worksByAuthor.get(authorKey(a)) ?? [])
}

/** Drop duplicate candidates by work id, keeping the first. Pure. Used when
 *  merging a truncated author search with the exact authored list. */
export function dedupeCandidates(candidates: WorkCandidate[]): WorkCandidate[] {
  const seen = new Set<string>()
  const out: WorkCandidate[] = []
  for (const c of candidates) {
    if (seen.has(c.id)) continue
    seen.add(c.id)
    out.push(c)
  }
  return out
}
