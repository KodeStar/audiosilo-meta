// Resolving a parsed library export against the catalogue - the sweep that
// decides, for every book in a reader's export, whether the database already
// holds it.
//
// It lives here rather than inside the /import island because TWO surfaces need
// the same answer and must not disagree about it: /import turns the misses into
// contribution links, and /watching turns the HITS into "series you follow and
// the volumes you already have". One implementation, one set of matching rules.
//
// The rules themselves are import-parse.ts's (pure, tested there); this module
// is the request choreography over them:
//
//   1. Look every ASIN/ISBN up against the catalogue, pooled. A hit is a book
//      the database holds, and the LOOKUP'S OWN work card comes back with it -
//      series membership included, which is what /watching groups by.
//   2. For every miss, decide new-work vs new-recording by searching each
//      distinct author once and matching the title locally. Both sources of
//      candidates (the search page and a prolific author's paged shelf) carry
//      each work's series, so a work matched this way is groupable too, at no
//      extra request.
//
// Nothing here renders; the caller supplies an AbortSignal and a progress
// callback.

import { lookup, search, getPersonPage, type SearchResult, type SeriesRef, type WorkCard } from './api'
import {
  authorKey,
  authorSearchKeys,
  candidatesForBook,
  collectAuthoredWorks,
  dedupeCandidates,
  isContributableOnMiss,
  matchExistingWork,
  partitionByIdentifier,
  unconfirmedAuthors,
  type ParsedBook,
  type WorkCandidate,
  type WorkMatch,
} from './import-parse'

/** Concurrency for the lookup and author-search sweeps. */
export const POOL_SIZE = 8

/** Concurrency for the per-series document fetches both /watching surfaces do
    (the page's own list, and the library import's size lookups). Gentler than
    POOL_SIZE: these are whole series documents, one per series rather than one
    per book. */
export const SERIES_POOL = 4

/** The hard safety cap on export size - a bound on how much work one dropped
    file can ask for. Books past it are counted and reported, never silently
    dropped. */
export const MAX_BOOKS = 5000

// Author-search page size: enough to cover a prolific author's shelf so an
// existing work isn't missed by the cap.
const AUTHOR_WORKS_LIMIT = 50

/** A fixed-size worker pool over items; stops early if the signal aborts.
    Exported because every bounded sweep on these two pages uses it. */
export async function runPool<T>(
  items: readonly T[],
  size: number,
  signal: AbortSignal,
  work: (item: T) => Promise<void>
): Promise<void> {
  let idx = 0
  const next = async (): Promise<void> => {
    while (idx < items.length && !signal.aborted) {
      const i = idx++
      await work(items[i])
    }
  }
  await Promise.all(Array.from({ length: Math.min(size, items.length) }, () => next()))
}

/** A book whose ASIN or ISBN matched a catalogued recording, with the work card
    the lookup returned. */
export interface MatchedBook {
  book: ParsedBook
  work: WorkCard
}

/** A book to contribute: either a brand-new work, or (existingWork set) a new
    recording of a work already in the catalogue. `series` is that existing
    work's first series membership when the candidate that matched carried one -
    the reader still HAS this book, so /watching counts it. */
export interface NewBook {
  book: ParsedBook
  existingWork: WorkMatch | null
  series: SeriesRef | null
}

/** Everything one dropped export resolved to. */
export interface ResolvedLibrary {
  inDatabase: MatchedBook[]
  newBooks: NewBook[]
  cannotMatch: ParsedBook[]
  /** Books in cannotMatch that carry no ASIN/ISBN (so could not be checked). */
  noIdentifier: number
  /** Books across the result stats: deduped identified + all unidentified. */
  total: number
  /** Books past MAX_BOOKS that were not looked at. */
  skipped: number
  /** Authors whose catalogue shelf could not be read in full. Their books in
      `newBooks` are unconfirmed, so a caller says so rather than implying every
      listed book is definitely missing from the database. */
  partialAuthors: string[]
}

/** Where the sweep has got to. `matching` marks the second phase, which has no
    count of its own (it is per distinct author, not per book). */
export interface ResolveProgress {
  done: number
  total: number
  matching: boolean
}

// Map a search-work or person-authored entry to a work candidate, recording its
// series membership on the way past. The candidate shape stays exactly what
// import-parse's matching rules take; the series rides alongside, keyed by work
// id, because a WorkMatch is only an {id,title}.
function toCandidate(
  seriesByWork: Map<string, SeriesRef | null>
): (x: { id: string; title: string; authors: { name: string }[]; series?: SeriesRef | null }) => WorkCandidate {
  return (x) => {
    seriesByWork.set(x.id, x.series ?? null)
    return { id: x.id, title: x.title, authors: x.authors }
  }
}

/**
 * Resolve a parsed export against the catalogue. Returns null when the signal
 * aborted mid-sweep (the caller's view is going away, so there is no result to
 * report).
 */
export async function resolveLibrary(
  all: readonly ParsedBook[],
  signal: AbortSignal,
  onProgress: (p: ResolveProgress) => void
): Promise<ResolvedLibrary | null> {
  const skipped = all.length > MAX_BOOKS ? all.length - MAX_BOOKS : 0
  const books = all.slice(0, MAX_BOOKS)

  // Dedupe and split off books with no identifier (they can't be matched); the
  // rest are looked up against the database below. `unidentified` is the
  // no-identifier portion of "cannot auto-match" - reported so the UI can
  // explain that number honestly (couldn't be checked, not missing).
  const { identified, unidentified } = partitionByIdentifier(books)
  const totalBooks = identified.length + unidentified.length
  const cannotMatch: ParsedBook[] = [...unidentified]
  const inDatabase: MatchedBook[] = []
  const misses: ParsedBook[] = []

  let done = 0
  onProgress({ done, total: identified.length, matching: false })

  // Phase 1: look up each unique identifier against the catalogue.
  await runPool(identified, POOL_SIZE, signal, async (book) => {
    const value = book.asin ?? book.isbn
    if (!value) return
    try {
      const match = await lookup(book.asin ? 'asin' : 'isbn', value, signal)
      if (match) inDatabase.push({ book, work: match.work })
      else if (isContributableOnMiss(book)) misses.push(book)
      else cannotMatch.push(book) // not found, but unknown language -> cannot auto-match
    } catch {
      if (signal.aborted) return
      cannotMatch.push(book) // a real lookup failure, counted, never treated as new
    } finally {
      if (!signal.aborted) {
        done += 1
        onProgress({ done, total: identified.length, matching: false })
      }
    }
  })
  if (signal.aborted) return null

  // Phase 2: for every miss, decide new-work vs new-recording by checking
  // whether the work is already catalogued. The ASIN missed, so we can't look
  // up by id; instead search each distinct author once (cached) - a clean,
  // FTS-indexed query - and match the work title locally.
  onProgress({ done, total: identified.length, matching: true })
  const seriesByWork = new Map<string, SeriesRef | null>()
  const asCandidate = toCandidate(seriesByWork)
  const worksByAuthor = new Map<string, WorkCandidate[]>()
  // Author keys whose shelf we know we did NOT see in full. Any of their books
  // can be in the database without us matching it, so the results warn instead
  // of presenting them as confidently new.
  const partial = new Set<string>()
  const authorNames = authorSearchKeys(misses)
  await runPool([...authorNames], POOL_SIZE, signal, async ([key, name]) => {
    try {
      const res = await search(name, AUTHOR_WORKS_LIMIT, signal)
      let works: WorkCandidate[] = res.results
        .filter((r): r is Extract<SearchResult, { kind: 'work' }> => r.kind === 'work')
        .map(asCandidate)
      // A prolific author can have more works than the search cap returns. When
      // the result is truncated, resolve the author's person id and pull the
      // complete authored list, so an existing work past the cap still matches.
      // That list is itself PAGED by the API (a page, plus authored_total), so
      // it is collected page by page - reading only the first page would put a
      // 500-plus-credit author's later works back out of sight and propose them
      // as new.
      if (res.results.length >= AUTHOR_WORKS_LIMIT) {
        const person = res.results.find(
          (r): r is Extract<SearchResult, { kind: 'person' }> =>
            r.kind === 'person' && authorKey(r.name) === key
        )
        if (!person) {
          // The search was capped and there is no person record to page from,
          // so this shelf is knowably incomplete.
          partial.add(key)
        } else {
          try {
            const shelf = await collectAuthoredWorks(async (offset) => {
              const p = await getPersonPage(person.id, { offset }, signal)
              return {
                authored: p.authored.map(asCandidate),
                authored_total: p.authored_total,
                limit: p.limit,
              }
            })
            works = dedupeCandidates([...works, ...shelf.works])
            // partial is keyed by AUTHOR KEY (what candidatesForBook and the
            // notice filter both join on), never by display name.
            if (shelf.truncated) partial.add(key)
          } catch {
            // Keep the (truncated) search works if the person fetch fails - but
            // the shelf stayed capped, so the caller must not read a miss as
            // "not in the database".
            if (!signal.aborted) partial.add(key)
          }
        }
      }
      worksByAuthor.set(key, works)
    } catch {
      if (!signal.aborted) {
        worksByAuthor.set(key, [])
        partial.add(key)
      }
    }
  })
  if (signal.aborted) return null

  const newBooks: NewBook[] = misses.map((book) => {
    const existingWork = matchExistingWork(book, candidatesForBook(book, worksByAuthor))
    return {
      book,
      existingWork,
      series: existingWork ? (seriesByWork.get(existingWork.id) ?? null) : null,
    }
  })

  // Only warn about an incomplete shelf that actually affects the output: an
  // author whose books all matched an existing work (or produced none) tells
  // the contributor nothing useful.
  const unmatched = newBooks.filter((n) => !n.existingWork).flatMap((n) => n.book.authors)
  const partialAuthors = unconfirmedAuthors(partial, authorNames, unmatched)

  return {
    inDatabase,
    newBooks,
    cannotMatch,
    noIdentifier: unidentified.length,
    total: totalBooks,
    skipped,
    partialAuthors,
  }
}

// --- Grouping the hits by series (the /watching half) ------------------------

/** A catalogued work the reader turned out to HAVE, and the series it belongs
    to. `series` is null for a work in no series - still a hit, just not
    something a watchlist can follow. */
export interface OwnedWork {
  id: string
  title: string
  series: SeriesRef | null
}

/** One series the reader owns something in: the works of it their export
    resolved to. */
export interface OwnedSeries {
  slug: string
  name: string
  /** Work slugs, deduped, in first-seen order. */
  works: string[]
}

/**
 * Every catalogued work a resolved export produced, deduped by work id.
 *
 * BOTH kinds of hit count as "the reader has this book": an identifier that
 * matched a catalogued recording, and a book whose recording is not catalogued
 * but whose WORK is (import-parse's existing-work match - a different narration
 * of a book the database already holds). The second is exactly the case a
 * reader's own library is full of, so dropping it would under-report a series
 * badly.
 */
export function ownedWorks(resolved: ResolvedLibrary): OwnedWork[] {
  const byID = new Map<string, OwnedWork>()
  for (const m of resolved.inDatabase) {
    if (!byID.has(m.work.id)) {
      byID.set(m.work.id, { id: m.work.id, title: m.work.title, series: m.work.series ?? null })
    }
  }
  for (const n of resolved.newBooks) {
    if (!n.existingWork || byID.has(n.existingWork.id)) continue
    byID.set(n.existingWork.id, {
      id: n.existingWork.id,
      title: n.existingWork.title,
      series: n.series,
    })
  }
  return [...byID.values()]
}

/** Group owned works by their series, by series name. A work in no series is
    absent - `ownedWorks` still lists it, which is what the unresolved
    disclosure reports it from. Pure. */
export function groupBySeries(works: readonly OwnedWork[]): OwnedSeries[] {
  const bySlug = new Map<string, OwnedSeries>()
  for (const w of works) {
    if (!w.series) continue
    const found = bySlug.get(w.series.id)
    if (found) {
      if (!found.works.includes(w.id)) found.works.push(w.id)
    } else {
      bySlug.set(w.series.id, { slug: w.series.id, name: w.series.name, works: [w.id] })
    }
  }
  return [...bySlug.values()].sort((a, b) => a.name.localeCompare(b.name) || a.slug.localeCompare(b.slug))
}
