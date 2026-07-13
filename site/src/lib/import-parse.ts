// Pure, framework-free parser for a user's library export. Three formats are
// understood: OpenAudible (books.json), Libation ("Export Library" JSON), and
// the audiosilo folder-scan (the metascan tool's output). Everything here runs
// in the browser on a file the user drops - the file never leaves the device.
// Kept free of React and DOM so it is independently testable.
//
// The field mapping mirrors the Go importer (the source of truth for how an
// export entry becomes a work + recording):
//   ../../../internal/importer/openaudible.go  (loose coercion helpers)
//   ../../../internal/importer/libation.go      (Libation field mapping)
//   ../../../internal/importer/mapping.go       (language/region/sequence/name rules)
//   ../../../internal/importer/importer.go       (title, runtime, cover, series)
// Only factual fields are read; personal/marketing fields are ignored (see
// LICENSING.md). The folder-scan's local-only fields (root, per-book path and
// file list) are never carried into a ParsedBook, so they can never be uploaded
// or downloaded for submission.

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
  format: KnownFormat // the source format (drives the factual-subset allowlist)
  raw: Record<string, unknown> // the original entry (for the factual-subset download)
}

export type ExportFormat = 'openaudible' | 'libation' | 'folderscan' | 'unknown'
/** A format the parser understands - everything but 'unknown'. */
export type KnownFormat = Exclude<ExportFormat, 'unknown'>

export interface ParseOutcome {
  format: ExportFormat
  // Books are mapped for openaudible, libation, and folderscan; empty for unknown.
  books: ParsedBook[]
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

// Normalize one raw credit: trim and strip a trailing role qualifier; null when
// nothing usable remains. Shared by every name-list shape (comma-joined strings
// and the folder-scan's arrays).
function cleanName(part: string): string | null {
  const name = part.trim()
  return name === '' ? null : stripRoleQualifier(name)
}

// Split a comma-joined name list, trim each, strip a trailing role qualifier,
// and drop empties.
function splitNames(joined: string): string[] {
  const out: string[] = []
  for (const part of joined.split(',')) {
    const name = cleanName(part)
    if (name !== null) out.push(name)
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
    format: 'openaudible',
    raw,
  }
}

// --- Libation mapping (mirrors internal/importer/libation.go) ---------------

// Reduce an ISO timestamp ("2018-10-18T23:00:00") to its YYYY-MM-DD date part,
// kept only when it is a full valid date.
function libationDate(ts: string): string | undefined {
  const d = ts.split('T')[0]
  return DATE_PATTERN.test(d) ? d : undefined
}

// Build the Amazon cover CDN URL from a Libation PictureId. A '+' (a valid
// image-id char) is percent-encoded so the URL is unambiguous; every other id
// char ([A-Za-z0-9-]) is already URL-safe.
function libationCover(pictureId: string): string | undefined {
  const id = pictureId.trim()
  if (id === '') return undefined
  return `https://m.media-amazon.com/images/I/${id.replace(/\+/g, '%2B')}._SL500_.jpg`
}

// Libation's sentinel SeriesOrder position for unorderable content (episodes):
// it means "no position", not book 999999999.
const LIBATION_UNSORTED = '999999999'

// Parse a Libation SeriesOrder ("{order} : {name}", multiple joined by ", ") and
// return the PRIMARY series for display/prefill: the first entry with a valid
// position, else the first entry's name with no position. The Go importer places
// a book in every one of its series; this client surfaces only one per book.
function primaryLibationSeries(
  order: string,
  names: string
): { seriesName?: string; seriesPosition?: string } {
  const entries: { name: string; position?: string }[] = []
  if (order.trim() !== '') {
    for (const part of order.split(', ')) {
      const ci = part.indexOf(':')
      if (ci < 0) continue
      const name = part.slice(ci + 1).trim()
      if (name === '') continue
      let ord = part.slice(0, ci).trim()
      if (ord === LIBATION_UNSORTED) ord = ''
      entries.push({
        name,
        position: SEQUENCE_PATTERN.test(ord) ? ord : undefined,
      })
    }
  }
  if (entries.length === 0) {
    for (const name of names.split(', ')) {
      const n = name.trim()
      if (n !== '') entries.push({ name: n })
    }
  }
  if (entries.length === 0) return {}
  const positioned = entries.find((e) => e.position !== undefined)
  const chosen = positioned ?? entries[0]
  return { seriesName: chosen.name, seriesPosition: chosen.position }
}

function parseLibationBook(raw: Record<string, unknown>): ParsedBook {
  const asin = normalizeAsin(coerceStr(raw['AudibleProductId']))
  const title = coerceStr(raw['Title'])
  const subtitle = coerceStr(raw['Subtitle'])
  const authors = splitNames(coerceStr(raw['AuthorNames']))
  const narrators = splitNames(coerceStr(raw['NarratorNames']))
  const { seriesName, seriesPosition } = primaryLibationSeries(
    coerceStr(raw['SeriesOrder']),
    coerceStr(raw['SeriesNames'])
  )
  const languageRaw = coerceStr(raw['Language'])
  const language = LANGUAGE_MAP[languageRaw.toLowerCase()]
  const minutes = coerceInt(raw['LengthInMinutes'])
  const runtimeMin = minutes !== undefined && minutes > 0 ? minutes : undefined
  const releaseDate = libationDate(coerceStr(raw['DatePublished']))
  const publisher = coerceStr(raw['Publisher'])
  const region = mapRegion(coerceStr(raw['Locale']))
  const coverUrl = libationCover(coerceStr(raw['PictureId']))
  const abridged = coerceBool(raw['IsAbridged'])

  return {
    asin: asin || undefined,
    isbn: undefined, // Libation exports carry no ISBN
    title,
    subtitle: subtitle || undefined,
    authors,
    narrators,
    seriesName,
    seriesPosition,
    language,
    languageRaw: languageRaw || undefined,
    runtimeMin,
    releaseDate,
    publisher: publisher || undefined,
    region,
    coverUrl,
    chapterCount: undefined, // Libation exports carry no chapter data
    abridged,
    format: 'libation',
    raw,
  }
}

// --- Folder-scan mapping (the metascan tool's output) -----------------------

// The folder-scan language may already be an ISO 639-1 code or a word. Map a
// known word, accept a bare 2-letter code, else undefined.
function mapLanguageLoose(raw: string): string | undefined {
  const w = raw.trim().toLowerCase()
  if (LANGUAGE_MAP[w]) return LANGUAGE_MAP[w]
  return /^[a-z]{2}$/.test(w) ? w : undefined
}

// Coerce a value that should be a string array (folder-scan authors/narrators),
// applying the same per-name normalization as splitNames (via cleanName).
function toNameArray(v: unknown): string[] {
  if (!Array.isArray(v)) return []
  const out: string[] = []
  for (const el of v) {
    const name = cleanName(coerceStr(el))
    if (name !== null) out.push(name)
  }
  return out
}

function parseFolderscanBook(raw: Record<string, unknown>): ParsedBook {
  const asin = normalizeAsin(coerceStr(raw['asin']))
  const isbn = normalizeIsbn(coerceStr(raw['isbn']))
  const title = coerceStr(raw['title'])
  const subtitle = coerceStr(raw['subtitle'])
  const authors = toNameArray(raw['authors'])
  const narrators = toNameArray(raw['narrators'])
  const seriesName = coerceStr(raw['series'])
  const seqRaw = coerceStr(raw['series_position'])
  const seriesPosition = SEQUENCE_PATTERN.test(seqRaw) ? seqRaw : undefined
  const languageRaw = coerceStr(raw['language'])
  const language = mapLanguageLoose(languageRaw)
  const runtime = coerceInt(raw['runtime_min'])
  const runtimeMin = runtime !== undefined && runtime > 0 ? runtime : undefined
  const releaseRaw = coerceStr(raw['release_date'])
  const releaseDate = DATE_PATTERN.test(releaseRaw) ? releaseRaw : undefined
  const publisher = coerceStr(raw['publisher'])
  const chapterCount = coerceInt(raw['chapters']) // a count in the folder-scan shape

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
    region: undefined, // folder-scan carries no marketplace region
    coverUrl: undefined,
    chapterCount,
    abridged: undefined,
    format: 'folderscan',
    raw,
  }
}

// --- Format registry ----------------------------------------------------------
// ONE descriptor per format: detection, entry parser, privacy allowlist, label.
// Adding a format is one entry here (the Record type makes the compiler demand
// every field) plus its slot in DETECTION_ORDER below.

export interface FormatSpec {
  /** Human label for issue prefills ("<label> (reviewed <date>)"). */
  label: string
  /** Whether the dropped JSON is this format; entries is the extracted array, if any. */
  detect: (data: unknown, entries: unknown[] | null) => boolean
  /** Map one raw entry to a ParsedBook. */
  parse: (raw: Record<string, unknown>) => ParsedBook
  /**
   * The factual fields kept from raw for the bulk download (LICENSING.md).
   * Everything else - purchase dates, ratings, account, descriptions, and the
   * folder-scan's local-only root/path/files - is stripped and never leaves the
   * device beyond this.
   */
  factualKeys: readonly string[]
}

// The folder-scan is a single object with this discriminator (not an array).
const FOLDERSCAN_FORMAT = 'audiosilo-folder-scan'

// Keys that mark an OpenAudible entry (lowercase; Libation uses PascalCase).
const OPENAUDIBLE_KEYS = ['asin', 'narrated_by', 'title_short', 'series_name', 'image_url']
const LIBATION_KEYS = ['AudibleProductId', 'AuthorNames', 'NarratorNames', 'Title', 'Asin']

export const FORMATS: Record<KnownFormat, FormatSpec> = {
  openaudible: {
    label: 'OpenAudible library export',
    detect: (_data, entries) => entriesHaveAnyKey(entries, OPENAUDIBLE_KEYS),
    parse: parseBook,
    factualKeys: [
      'asin',
      'title',
      'title_short',
      'author',
      'narrated_by',
      'series_name',
      'series_sequence',
      'language',
      'release_date',
      'publisher',
      'image_url',
      'region',
      'seconds',
      'abridged',
    ],
  },
  libation: {
    label: 'Libation library export',
    detect: (_data, entries) => entriesHaveAnyKey(entries, LIBATION_KEYS),
    parse: parseLibationBook,
    factualKeys: [
      'AudibleProductId',
      'Title',
      'Subtitle',
      'AuthorNames',
      'NarratorNames',
      'SeriesNames',
      'SeriesOrder',
      'Language',
      'Locale',
      'LengthInMinutes',
      'DatePublished',
      'Publisher',
      'PictureId',
      'IsAbridged',
    ],
  },
  folderscan: {
    label: 'audiosilo folder scan',
    detect: (data) => isObject(data) && coerceStr(data['format']) === FOLDERSCAN_FORMAT,
    parse: parseFolderscanBook,
    factualKeys: [
      'asin',
      'isbn',
      'title',
      'subtitle',
      'authors',
      'narrators',
      'series',
      'series_position',
      'publisher',
      'release_date',
      'language',
      'runtime_min',
      'chapters',
    ],
  },
}

// Detection order is load-bearing: the discriminated folder-scan first (its
// per-book keys overlap OpenAudible's), then OpenAudible's lowercase keys
// before Libation's generic PascalCase Title.
const DETECTION_ORDER: readonly KnownFormat[] = ['folderscan', 'openaudible', 'libation']

// Libation sometimes wraps its list in an object under one of these keys (the
// folder-scan's books array also rides under 'books').
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

// True when any of the first entries carries any of the marker keys (false for
// a missing or empty entries array).
function entriesHaveAnyKey(entries: unknown[] | null, keys: readonly string[]): boolean {
  if (!entries) return false
  for (const el of entries.slice(0, 20)) {
    if (isObject(el)) {
      for (const k of keys) if (k in el) return true
    }
  }
  return false
}

function detectFormat(data: unknown, entries: unknown[] | null): ExportFormat {
  for (const f of DETECTION_ORDER) {
    if (FORMATS[f].detect(data, entries)) return f
  }
  return 'unknown'
}

/**
 * Parse a dropped export. Detects the format and returns every entry mapped to a
 * ParsedBook (for openaudible, libation, and the audiosilo folder-scan); an
 * unknown file yields an empty books list and routes to the issue-form path.
 * Throws a friendly Error on invalid JSON.
 */
export function parseExport(text: string): ParseOutcome {
  let data: unknown
  try {
    data = JSON.parse(text)
  } catch {
    throw new Error(
      'That file is not valid JSON. Make sure you selected a supported library export (OpenAudible, Libation, or an audiosilo folder scan).'
    )
  }
  const entries = extractEntries(data)
  const format = detectFormat(data, entries)
  if (format === 'unknown') {
    return { format, books: [] }
  }
  const parse = FORMATS[format].parse
  const books: ParsedBook[] = []
  for (const el of entries ?? []) {
    if (isObject(el)) books.push(parse(el))
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
