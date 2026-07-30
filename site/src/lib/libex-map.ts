// Map a libex record (libex.ts) onto the facts an add-work issue carries, route
// what the reader typed, and resolve one candidate ASIN into what the /add page
// should show next.
//
// This is the only place that decides what a libex lookup contributes, so the
// /add page's confirmation card and the issue URL are guaranteed to agree: the
// card renders exactly this object, and github-prefill turns exactly this
// object into form parameters. DOM-free throughout, and the one async function
// takes its fetchers injected, so every rule below is unit tested.
//
// The rules mirror the Go importer (internal/importer) wherever the two look at
// the same fact, so a libex-seeded work and an export-seeded work resolve to the
// same identity:
//  - a trailing (Unabridged)/(Abridged) edition marker is stripped from the work
//    title, and seeds the recording's abridged flag when the source did not
//    state it otherwise (cleanWorkTitle / abridgedFromMarker);
//  - a region is kept only when it names a real Audible marketplace;
//  - the release date is the RECORDING's release, never the work's first
//    publication - an audiobook of a 1997 novel is not a 2015 book.
// Genres are deliberately dropped: there is no genre field in the schema yet, and
// so are libex's `subtitle` and `isbn` - see the LibexBook doc in libex.ts for
// why neither is the fact its name suggests.

import { formatLanguage, formatRuntime, looksLikeAsin, today } from './api'
import type { LookupResponse } from './api'
// formatAsinLine only: github-prefill's own reference back to this module is
// type-only (erased at build time), so there is no runtime import cycle.
import { formatAsinLine } from './github-prefill'
import { mapLanguageLoose, mapRegion } from './import-parse'
import { LIBEX_HOST } from './libex'
import type { LibexBook } from './libex'

/**
 * The facts a libex record contributes to an add-work issue. Field names are
 * the vocabulary of import-parse's ParsedBook (not the form's field ids) so the
 * recording half can go through github-prefill's shared applyRecordingParams.
 */
export interface LibexPrefill {
  /** The work title, edition marker stripped. */
  title: string
  authors: string[]
  /** BCP-47, when the language word maps to one. Blank means "the contributor
      fills the required field in" - never a guess. */
  language?: string
  seriesName?: string
  /** A position in the intake form's grammar ("1", "2.5", "1-3.5"). A stated
      position we cannot parse is omitted and NOTED in `sources` instead. */
  seriesPosition?: string
  narrators: string[]
  /** Tri-state: undefined = the source did not state it. */
  abridged?: boolean
  runtimeMin?: number
  releaseDate?: string
  publisher?: string
  asin: string
  /** A validated marketplace code, lowercase; absent when libex named none we
      recognise (the ASIN then rides without a region prefix). */
  region?: string
  coverUrl?: string
  /** The issue's Sources text: the `libex: <ASIN>` marker line, the human
      provenance line, and a note for every stated fact no form field could
      carry (an extra series membership, an unrecognised region or position). */
  sources: string
}

// One or more stacked trailing (Unabridged)/(Abridged)/[Unabridged]/[Abridged]
// edition markers with their surrounding whitespace - the TS twin of the Go
// importer's editionMarkerRE.
const EDITION_MARKER_RE = /(?:\s*[([](?:un)?abridged[)\]])+\s*$/i
const UNABRIDGED_MARKER_RE = /[([]unabridged[)\]]/i
const ABRIDGED_MARKER_RE = /[([]abridged[)\]]/i

/**
 * Strip trailing edition markers from a work title, so "Mageling (Unabridged)"
 * and "Mageling" are one work. Never returns an empty string: a title that is
 * ONLY a marker is returned unchanged (mirrors Go cleanWorkTitle).
 */
export function cleanWorkTitle(title: string): string {
  const cleaned = title.trim()
  const stripped = cleaned.replace(EDITION_MARKER_RE, '').trim()
  return stripped === '' ? cleaned : stripped
}

/**
 * The abridged tri-state stated by a title's edition marker: "(Unabridged)" ->
 * false, "(Abridged)" -> true, no marker -> undefined. The title printing the
 * edition is a fact the publisher stated on the release, so reading it is not a
 * guess. Unabridged is checked first (mirrors Go abridgedFromMarker).
 */
export function abridgedFromMarker(title: string): boolean | undefined {
  if (UNABRIDGED_MARKER_RE.test(title)) return false
  if (ABRIDGED_MARKER_RE.test(title)) return true
  return undefined
}

/** libex's bookFormat, when it states an edition. Any other value is unknown. */
function abridgedFromFormat(bookFormat?: string): boolean | undefined {
  const f = bookFormat?.trim().toLowerCase()
  if (f === 'unabridged') return false
  if (f === 'abridged') return true
  return undefined
}

/**
 * A validated, lowercase marketplace code, else undefined. libex may report
 * "gb" - the ISO country code for the store the schema calls "uk" - so that one
 * alias is applied before the shared schema check (import-parse's mapRegion).
 */
export function mapLibexRegion(raw?: string): string | undefined {
  const r = raw?.trim().toLowerCase()
  if (!r) return undefined
  return mapRegion(r === 'gb' ? 'uk' : r)
}

/** The date part of libex's ISO release timestamp ("2015-10-27T00:00:00+00:00"
    -> "2015-10-27"). Anything that is not a plain YYYY-MM-DD after trimming the
    time is dropped rather than reshaped. */
export function releaseDatePart(raw?: string): string | undefined {
  const value = raw?.trim()
  if (!value) return undefined
  const date = value.split('T')[0]
  return /^\d{4}-\d{2}-\d{2}$/.test(date) ? date : undefined
}

/** The intake form's series-position grammar: a number, a half-number, or an
    omnibus range ("1", "2.5", "1-3.5"). Mirrors the schema's position rule. */
const SERIES_POSITION_RE = /^\d+(\.\d+)?(-\d+(\.\d+)?)?$/

/**
 * The Sources text for a libex-seeded issue.
 *
 * The first line is exactly `libex: <ASIN>`. Today nothing sniffs it - it rides
 * as ordinary provenance text like the rest of the field - and it is written in
 * that fixed shape as GROUNDWORK for a PLANNED intake change that would
 * recognise a lookup-seeded submission by it. `notes` carries every stated fact
 * no form field can hold (a second series membership, a region or a position we
 * could not validate): the form has one series field and a validated region/
 * position, so dropping these silently would lose a fact the reviewer needs.
 */
function composeSources(book: LibexBook, retrieved: string, notes: string[]): string {
  return [
    `libex: ${book.asin}`,
    `Libex (${LIBEX_HOST}) lookup for ${book.asin} (retrieved ${retrieved})`,
    ...notes,
  ].join('\n')
}

/** A series membership as "Name #position" (or just the name). */
function seriesLabel(s: { name: string; position?: string }): string {
  return s.position ? `${s.name} #${s.position}` : s.name
}

/**
 * Map a libex record onto the add-work facts. `retrieved` (YYYY-MM-DD) is
 * injectable so the provenance line is testable; it defaults to today.
 *
 * Omit-never-guess throughout: a field libex did not state, or stated in a
 * shape we do not recognise, is left out for the contributor to complete.
 */
export function libexToPrefill(book: LibexBook, retrieved: string = today()): LibexPrefill {
  const rawTitle = book.title?.trim() ?? ''
  const title = cleanWorkTitle(rawTitle)
  // The recording's edition: libex's stated bookFormat first, else whatever the
  // title's stripped marker said.
  const abridged = abridgedFromFormat(book.bookFormat) ?? abridgedFromMarker(rawTitle)

  // Every stated fact the form has no field for, gathered for the Sources text.
  const notes: string[] = []
  const extraSeries = book.series.slice(1).map(seriesLabel)
  if (extraSeries.length) notes.push(`Also listed in: ${extraSeries.join('; ')}`)

  const first = book.series[0]
  // A position outside the intake grammar would be rejected there, so it is
  // omitted from the field and stated in the provenance text instead - the fact
  // reaches a human rather than quietly un-serieing the work.
  let seriesPosition: string | undefined
  if (first?.position) {
    if (SERIES_POSITION_RE.test(first.position)) seriesPosition = first.position
    else notes.push(`Series position as listed: "${first.position}"`)
  }

  const region = mapLibexRegion(book.region)
  // An unrecognised marketplace is not silently dropped: the bare ASIN it leaves
  // behind DEFAULTS to us on intake, so the reviewer must be able to see what
  // libex actually said.
  if (!region && book.region) notes.push(`Region as listed: "${book.region}"`)

  const prefill: LibexPrefill = {
    title,
    authors: book.authors.map((a) => a.name),
    narrators: book.narrators.map((n) => n.name),
    asin: book.asin,
    sources: composeSources(book, retrieved, notes),
  }
  const language = book.language ? mapLanguageLoose(book.language) : undefined
  if (language) prefill.language = language
  if (first) prefill.seriesName = first.name
  if (seriesPosition) prefill.seriesPosition = seriesPosition
  if (abridged !== undefined) prefill.abridged = abridged
  if (book.lengthMinutes !== undefined) prefill.runtimeMin = book.lengthMinutes
  const releaseDate = releaseDatePart(book.releaseDate)
  if (releaseDate) prefill.releaseDate = releaseDate
  if (book.publisher) prefill.publisher = book.publisher
  if (region) prefill.region = region
  // https-only by construction (libex.parseLibexBook filters it at the parse
  // boundary), so nothing to re-check here.
  if (book.imageUrl) prefill.coverUrl = book.imageUrl
  return prefill
}

// --- Review rendering -------------------------------------------------------

/** One line of the /add confirmation card. `value` is null when the lookup did
    not state the fact, so the reader can see what they will have to fill in
    themselves rather than being shown a shorter, quietly incomplete list. */
export interface PrefillFact {
  key: string
  label: string
  value: string | null
}

/**
 * The mapped facts, in the order the confirmation card lists them - EXACTLY
 * what the prefilled issue will carry, derived from the same object the issue
 * builder reads, so the review can never drift from the submission. The ASIN
 * row goes through github-prefill's own formatAsinLine, and the Sources row
 * shows the provenance text verbatim (including the notes for an extra series
 * membership or an unvalidated region/position, which no other row would reveal).
 *
 * Every row is always present, with a null value where the lookup stated
 * nothing, so the reader sees what they will have to complete themselves.
 */
export function prefillFacts(p: LibexPrefill): PrefillFact[] {
  return [
    { key: 'title', label: 'Title', value: p.title || null },
    { key: 'authors', label: 'Author(s)', value: p.authors.join(', ') || null },
    { key: 'narrators', label: 'Narrator(s)', value: p.narrators.join(', ') || null },
    {
      key: 'language',
      label: 'Language',
      value: p.language ? `${formatLanguage(p.language)} (${p.language})` : null,
    },
    {
      key: 'series',
      label: 'Series',
      value: p.seriesName
        ? seriesLabel({ name: p.seriesName, position: p.seriesPosition })
        : null,
    },
    {
      key: 'abridged',
      label: 'Abridged',
      value: p.abridged === undefined ? null : p.abridged ? 'Abridged' : 'Unabridged',
    },
    {
      key: 'runtime',
      label: 'Runtime',
      value: p.runtimeMin ? `${formatRuntime(p.runtimeMin)} (${p.runtimeMin} minutes)` : null,
    },
    { key: 'release', label: 'Release date', value: p.releaseDate ?? null },
    { key: 'publisher', label: 'Publisher', value: p.publisher ?? null },
    { key: 'asin', label: 'ASIN', value: formatAsinLine(p.asin, p.region) },
    { key: 'cover', label: 'Cover image', value: p.coverUrl ?? null },
    { key: 'sources', label: 'Sources', value: p.sources || null },
  ]
}

/**
 * Whether a prefilled issue is worth opening at all.
 *
 * The form's Title is required and intake hard-fails without it, so a record
 * libex states no title for cannot produce a usable submission - offering the
 * GitHub hand-off there would send the reader to a rejection. Every OTHER
 * required field (authors, narrators, language) the reader can simply type into
 * the form, so a gap in those is a prompt, not a blocker.
 */
export function canOpenPrefilledIssue(p: LibexPrefill): boolean {
  return p.title.trim() !== ''
}

// --- Routing what the reader typed ------------------------------------------

/** What one search term should do: resolve a product code straight to its
    record, or search the catalogue for it. */
export type QueryRoute = { kind: 'asin'; asin: string } | { kind: 'search'; q: string }

/**
 * Route a term the reader typed, pasted, or arrived with in `?q=`.
 *
 * ASIN detection is CASE-INSENSITIVE: `looksLikeAsin` anchors an upper-case
 * "B0", so a lower-case paste ("b015rqon6i") used to fall through to a keyword
 * search that finds nothing and dead-ends. The term is upper-cased BEFORE the
 * test, and the route carries the normalized ASIN so every caller resolves the
 * same value. Returns null for an empty term (nothing to do).
 */
export function routeQuery(term: string): QueryRoute | null {
  const trimmed = term.trim()
  if (!trimmed) return null
  const asin = trimmed.toUpperCase()
  return looksLikeAsin(asin) ? { kind: 'asin', asin } : { kind: 'search', q: trimmed }
}

// --- Resolving one candidate ------------------------------------------------

/** What a candidate ASIN turned out to be. */
export type LibexResolution =
  /** Our catalogue already has this ASIN - there is nothing to propose. */
  | { kind: 'covered'; asin: string; match: LookupResponse }
  /** New to us, and libex has the facts: this is what the issue would carry. */
  | { kind: 'propose'; prefill: LibexPrefill }
  /** New to us, but libex has no record either - nothing to prefill from. */
  | { kind: 'missing'; asin: string }

/** The two network calls the resolver needs, injected so the policy below is
    unit-testable without a fetch stub. */
export interface LibexResolvers {
  /** libex.libexBook - the full record for an ASIN, null when it has none. */
  fetchBook: (asin: string, signal?: AbortSignal) => Promise<LibexBook | null>
  /** api.lookup('asin', ...) - our own catalogue, null when it has no match. */
  fetchLookup: (asin: string, signal?: AbortSignal) => Promise<LookupResponse | null>
}

/**
 * Decide what one candidate ASIN should show, checking our catalogue and (only
 * when needed) libex in parallel.
 *
 * Two policies live here rather than in the UI:
 *
 *  - A CATALOGUE OUTAGE MUST NOT BLOCK CONTRIBUTING. A failed lookup counts as
 *    "not found", so the reader still reaches the review card; the worst case is
 *    a duplicate issue, which a maintainer closes, versus a dead end.
 *  - A SEARCH ROW IS ALREADY THE WHOLE RECORD. libex's /search returns complete
 *    BookResponse objects, so when the reader picked a row we do NOT re-fetch it
 *    - the pick costs one catalogue lookup and nothing more. Only a directly
 *    entered ASIN (no row) needs fetchBook, and a null result there is 'missing'.
 *  - AN ESTABLISHED 'covered' VERDICT SURVIVES A LIBEX OUTAGE. Both calls are
 *    started together, but a fetchBook FAILURE is only fatal when the catalogue
 *    lookup found nothing: if the lookup already matched, the book is catalogued
 *    and there is nothing to propose whatever libex says. Awaiting them jointly
 *    used to reject the whole resolve, telling a reader to file by hand a work we
 *    already have - which is exactly how duplicates get opened.
 *
 * A fetchBook failure propagates only in the no-match case (the caller shows its
 * lookup-failed state); an aborted signal surfaces as the usual AbortError.
 */
export async function resolveLibexCandidate(
  rawAsin: string,
  row: LibexBook | null | undefined,
  resolvers: LibexResolvers,
  signal?: AbortSignal
): Promise<LibexResolution> {
  const asin = rawAsin.trim().toUpperCase()
  const [lookupResult, bookResult] = await Promise.allSettled([
    resolvers.fetchLookup(asin, signal),
    row ? Promise.resolve(row) : resolvers.fetchBook(asin, signal),
  ])
  // A failed catalogue lookup counts as "not found" (see above).
  const match = lookupResult.status === 'fulfilled' ? lookupResult.value : null
  if (match) return { kind: 'covered', asin, match }
  if (bookResult.status === 'rejected') throw bookResult.reason
  if (!bookResult.value) return { kind: 'missing', asin }
  return { kind: 'propose', prefill: libexToPrefill(bookResult.value) }
}
