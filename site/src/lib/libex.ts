// Typed client for libex (libexdb.com) - a public, no-auth
// mirror of Audible's catalogue metadata. It is the lookup assist behind the
// /add page: when meta.audiosilo.app has no match, the reader searches libex,
// picks the right edition, and lands on a PREFILLED add-work issue.
//
// Two rules shape this module.
//
//  1. WHITELIST PARSING. libex returns Audible's full product record, which
//     includes `description`, `summary`, `copyright` and `rating` - publisher
//     copy and derived ratings that our licensing policy (LICENSING.md) forbids
//     us from importing. parseLibexBook therefore builds its result by copying
//     ONLY the fields named in the STRING_FIELDS list below (plus the ones with
//     their own coercion: asin, region, lengthMinutes, and the people/series/
//     genre-claim arrays); it never spreads the raw object and never reads any
//     other key. That list IS the whitelist - the forbidden fields cannot reach a
//     LibexBook structurally, so no downstream consumer has to remember to strip
//     them. The genre claims are the one whitelisted field that is not itself
//     storable: they are a retailer's category strings, kept solely so
//     audible-genres.ts can map them onto this project's own vocabulary (and drop
//     the rest), never carried through verbatim.
//
//  2. USER-INITIATED ONLY. Every call here is a third-party request from the
//     reader's browser. Nothing in this module is called speculatively - a fetch
//     happens only after the reader types a search or picks a result.
//
// The endpoints are documented at https://libexdb.com/docs.

import { isObject } from './import-parse'

/** The libex origin. Exported so the UI can name and link its source. */
export const LIBEX_BASE = 'https://libexdb.com'

/** The libex host, for the human-readable provenance line and UI copy. */
export const LIBEX_HOST = 'libexdb.com'

/** How long a libex call may run before it is abandoned. Third-party and
    occasionally cold, so this is generous compared with our own API. */
const TIMEOUT_MS = 15000

/** How many search rows to request. libex caps `limit` at 50. */
const SEARCH_LIMIT = 20

// --- Wire shapes (whitelisted) ---------------------------------------------

/** A credited person. libex carries ids/images/timestamps too; we keep only the
    name, which is the only thing an add-work issue can use. */
export interface LibexPerson {
  name: string
}

/** A series membership. `position` is a string in our model ("1", "2.5"); libex
    sometimes sends a JSON number, which is normalized to its string form. */
export interface LibexSeriesRef {
  name: string
  position?: string
}

/**
 * One raw genre claim from a record's `genres` array ([{asin, name, type}]),
 * where `asin` is a browse-NODE id, not a product ASIN. Both halves are optional
 * because a claim is usable with either one.
 *
 * A claim is NOT a genre: it is a retailer's category string, which LICENSING.md
 * forbids storing verbatim. It is kept only so audible-genres.ts can map it onto
 * this project's own controlled vocabulary and DROP whatever does not map -
 * exactly what the Go importer does with the same field. The node `type`
 * ("Genres"/"Tags") is deliberately not kept: the mapping table, not the node
 * type, decides what becomes a vocabulary genre.
 */
export interface LibexGenreClaim {
  /** The browse-node id, when the record states one. */
  node?: string
  name?: string
}

/**
 * The whitelisted subset of libex's BookResponse. Every field here is factual
 * catalogue data (CC0-compatible in our model). Deliberately ABSENT, and
 * unreachable by construction: description, summary, copyright, rating - see
 * the module comment.
 *
 * Two further fields libex DOES return are deliberately not whitelisted, because
 * neither is the fact its name suggests:
 *  - `subtitle`, which on Audible is usually a marketing tagline ("The first Jack
 *    Reacher novel in the No.1 Sunday Times bestselling thriller series") or a
 *    series designation ("Jack Reacher, Book 1"), never a bibliographic subtitle;
 *  - `isbn`, which is at least sometimes the PRINT edition's ISBN (B015RQON6I is
 *    published by Random House Audio but carries 9780451482266, a Berkley print
 *    imprint). recording.isbn is a hard dedup key upstream, so a wrong one would
 *    mechanically refuse a legitimate future narration.
 */
export interface LibexBook {
  asin: string
  title?: string
  /** The marketplace this record came from ("us", "uk", ...), lowercase. */
  region?: string
  publisher?: string
  /** A language WORD ("english"), not a BCP-47 code - mapped at prefill time. */
  language?: string
  /** "unabridged" / "abridged" when stated. */
  bookFormat?: string
  /** An ISO timestamp ("2015-10-27T00:00:00+00:00"). */
  releaseDate?: string
  /** An https cover URL. The https rule is applied HERE, at the parse boundary,
      so no consumer can render an http URL as mixed content. */
  imageUrl?: string
  lengthMinutes?: number
  authors: LibexPerson[]
  narrators: LibexPerson[]
  series: LibexSeriesRef[]
  /** Raw category claims, mapped onto our vocabulary at prefill time and never
      carried verbatim - see LibexGenreClaim. */
  genres: LibexGenreClaim[]
}

// --- Errors -----------------------------------------------------------------

/** Thrown for any non-OK libex response, mirroring api.ts's ApiError so callers
    can branch on the status (notably 404 = "nothing found", not a failure). */
export class LibexError extends Error {
  status: number
  constructor(status: number, message: string) {
    super(message)
    this.name = 'LibexError'
    this.status = status
  }
}

// --- Parsing ----------------------------------------------------------------

/** A trimmed non-empty string, else undefined. */
function str(v: unknown): string | undefined {
  if (typeof v !== 'string') return undefined
  const s = v.trim()
  return s === '' ? undefined : s
}

/** A positive integer, else undefined. A 0/negative runtime is not a fact. A
    fractional runtime is ROUNDED rather than dropped: the field is whole minutes
    in both models, so 1067.4 is a stated runtime with spurious precision. */
function posInt(v: unknown): number | undefined {
  if (typeof v !== 'number' || !Number.isFinite(v)) return undefined
  const n = Math.round(v)
  return n > 0 ? n : undefined
}

/** An https URL, else undefined - we link covers, never host them, and an http
    URL would be blocked as mixed content on our own https pages anyway. */
function httpsUrl(v: unknown): string | undefined {
  const s = str(v)
  return s && s.startsWith('https://') ? s : undefined
}

/** A series position as a string. libex's own model says string ("1", "2.5"),
    but its live responses sometimes carry a JSON number - which silently
    vanished here, landing the work in no series at all. */
function seriesPosition(v: unknown): string | undefined {
  if (typeof v === 'number') return Number.isFinite(v) ? String(v) : undefined
  return str(v)
}

/** Named-field copy of a person entry; unnamed entries are dropped. */
function people(v: unknown): LibexPerson[] {
  if (!Array.isArray(v)) return []
  const out: LibexPerson[] = []
  for (const el of v) {
    if (!isObject(el)) continue
    const name = str(el['name'])
    if (name) out.push({ name })
  }
  return out
}

/**
 * Named-field copy of a genre claim. libex sends objects ({asin, name, type});
 * a bare string is tolerated as a name-only claim, mirroring the Go importer's
 * libexGenreClaims. A claim with neither a node nor a name is dropped.
 */
function genreClaims(v: unknown): LibexGenreClaim[] {
  if (!Array.isArray(v)) return []
  const out: LibexGenreClaim[] = []
  for (const el of v) {
    if (typeof el === 'string') {
      const name = str(el)
      if (name) out.push({ name })
      continue
    }
    if (!isObject(el)) continue
    // The claim's "asin" is a browse-node id (Audible's own field name).
    const node = str(el['asin'])
    const name = str(el['name'])
    if (!node && !name) continue
    const claim: LibexGenreClaim = {}
    if (node) claim.node = node
    if (name) claim.name = name
    out.push(claim)
  }
  return out
}

/** Named-field copy of a series membership; unnamed entries are dropped. */
function seriesRefs(v: unknown): LibexSeriesRef[] {
  if (!Array.isArray(v)) return []
  const out: LibexSeriesRef[] = []
  for (const el of v) {
    if (!isObject(el)) continue
    const name = str(el['name'])
    if (!name) continue
    const position = seriesPosition(el['position'])
    out.push(position ? { name, position } : { name })
  }
  return out
}

/**
 * The whitelisted fields copied verbatim as trimmed strings. THIS LIST IS THE
 * COPYRIGHT BOUNDARY for the plain-string half of the record: a key that is not
 * named here is never read, so description/summary/copyright cannot appear in
 * the output no matter what libex adds to its response. The fields with their
 * own coercion (asin, region, imageUrl, lengthMinutes, and the people/series
 * arrays) are handled explicitly in parseLibexBook.
 */
const STRING_FIELDS = ['title', 'publisher', 'language', 'bookFormat', 'releaseDate'] as const

/** The ASIN grammar: 10 upper-case alphanumerics. Every downstream step keys on
    this value, so a record whose identity is malformed is unusable, not partial. */
const ASIN_RE = /^[A-Z0-9]{10}$/

/**
 * Build a LibexBook from one raw libex record by copying whitelisted fields
 * field by field. Returns null when the record carries no usable ASIN - the
 * identity every downstream step keys on, so a value outside the ASIN grammar
 * is rejected outright rather than carried into a prefilled issue.
 *
 * This is THE copyright boundary: the raw object is read only through
 * STRING_FIELDS and the named reads below, so description/summary/copyright/
 * rating are structurally incapable of reaching our output.
 */
export function parseLibexBook(raw: unknown): LibexBook | null {
  if (!isObject(raw)) return null
  const asin = str(raw['asin'])?.toUpperCase()
  if (!asin || !ASIN_RE.test(asin)) return null
  const book: LibexBook = {
    asin,
    authors: people(raw['authors']),
    narrators: people(raw['narrators']),
    series: seriesRefs(raw['series']),
    genres: genreClaims(raw['genres']),
  }
  for (const field of STRING_FIELDS) {
    const value = str(raw[field])
    if (value) book[field] = value
  }
  // The fields that are not a verbatim string copy.
  const region = str(raw['region'])
  if (region) book.region = region.toLowerCase()
  const imageUrl = httpsUrl(raw['imageUrl'])
  if (imageUrl) book.imageUrl = imageUrl
  const lengthMinutes = posInt(raw['lengthMinutes'])
  if (lengthMinutes !== undefined) book.lengthMinutes = lengthMinutes
  return book
}

// --- Fetching ---------------------------------------------------------------

/**
 * Combine the caller's cancellation with our own timeout, so whichever fires
 * first abandons the request.
 *
 * Composed by hand rather than with `AbortSignal.any` / `AbortSignal.timeout`:
 * those statics are Safari 17.4+ and Firefox 124+, and on an older engine merely
 * REACHING for them throws a TypeError - which this module's callers would report
 * to the reader as "Libex could not be reached", blaming the service for the
 * browser. The timeout still aborts with a TimeoutError (not a plain AbortError)
 * so a caller can still tell a timeout from its own cancellation, and the
 * returned `cleanup` must run when the request settles so a long-lived caller
 * signal does not accumulate listeners.
 */
function composeSignal(signal?: AbortSignal): { signal: AbortSignal; cleanup: () => void } {
  const ctrl = new AbortController()
  const timer = setTimeout(() => {
    ctrl.abort(new DOMException(`Libex request timed out after ${TIMEOUT_MS}ms`, 'TimeoutError'))
  }, TIMEOUT_MS)
  const onAbort = () => ctrl.abort(signal?.reason)
  if (signal) {
    if (signal.aborted) onAbort()
    else signal.addEventListener('abort', onAbort, { once: true })
  }
  return {
    signal: ctrl.signal,
    cleanup: () => {
      clearTimeout(timer)
      signal?.removeEventListener('abort', onAbort)
    },
  }
}

async function getJSON(path: string, signal?: AbortSignal): Promise<unknown> {
  const composed = composeSignal(signal)
  try {
    const res = await fetch(`${LIBEX_BASE}${path}`, {
      signal: composed.signal,
      headers: { Accept: 'application/json' },
    })
    if (!res.ok) {
      throw new LibexError(res.status, `Libex request failed: ${res.status}`)
    }
    return await res.json()
  } finally {
    composed.cleanup()
  }
}

/**
 * Search the Audible catalogue through libex.
 *
 * The endpoint's general-purpose field is `keywords` (there is no `q`), and it
 * answers 404 - not an empty array - when nothing matches, so a 404 is mapped
 * to `[]` here and only a real failure reaches the caller. Rows are parsed
 * through the whitelist; unusable rows (no ASIN) are dropped.
 *
 * A 200 whose body is NOT a list is a contract failure and THROWS. Returning
 * `[]` there would render as "Libex has no match", so a day when libex starts
 * wrapping its rows in an envelope would silently look like an empty catalogue
 * and the whole route would die quietly instead of reporting a fault.
 */
export async function libexSearch(q: string, signal?: AbortSignal): Promise<LibexBook[]> {
  const query = q.trim()
  if (!query) return []
  const params = new URLSearchParams({ keywords: query, limit: String(SEARCH_LIMIT) })
  let data: unknown
  try {
    data = await getJSON(`/search?${params.toString()}`, signal)
  } catch (err) {
    if (err instanceof LibexError && err.status === 404) return []
    throw err
  }
  if (!Array.isArray(data)) {
    // The status was fine (200); the BODY was not what the endpoint documents.
    throw new LibexError(200, 'Libex search did not return a list of books')
  }
  const out: LibexBook[] = []
  for (const row of data) {
    const book = parseLibexBook(row)
    if (book) out.push(book)
  }
  return out
}

/**
 * Fetch one full record by ASIN. libex's mirrored copy (`/db/book/{asin}`) is
 * the cheap path; the live `/book/{asin}` fetch is the fallback. Returns null
 * when neither has the ASIN.
 *
 * The fallback is taken on 404 AND on a 200 that does not parse to a usable
 * record: the mirror answering with something we cannot use is the same
 * situation as it not having the title, and the live endpoint is exactly where
 * the real record then is.
 */
export async function libexBook(asin: string, signal?: AbortSignal): Promise<LibexBook | null> {
  const id = asin.trim()
  if (!id) return null
  try {
    const mirrored = parseLibexBook(await getJSON(`/db/book/${encodeURIComponent(id)}`, signal))
    if (mirrored) return mirrored
  } catch (err) {
    if (!(err instanceof LibexError) || err.status !== 404) throw err
  }
  try {
    return parseLibexBook(await getJSON(`/book/${encodeURIComponent(id)}`, signal))
  } catch (err) {
    if (err instanceof LibexError && err.status === 404) return null
    throw err
  }
}
