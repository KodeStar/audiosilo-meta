// The series watchlist: which series a reader follows, which entries they
// already have, which of the rest they have dismissed as "not interested", and
// which they have already been shown.
//
// It lives ENTIRELY in the reader's browser, under one localStorage key. There
// is no account to attach it to and the API is read-only, so nothing here is
// ever sent anywhere - which is also why it is the reader's job to back it up
// (exportJSON/importJSON below feed the /watching page's download + merge).
//
// Framework-free and pure apart from the two storage helpers, on the precedent
// of lib/marketplace.ts: every mutation takes a store and returns a NEW one, so
// the rules are unit-testable and an island persists by writing what a mutation
// handed back rather than by reaching into the object it is rendering.

import { isFutureRelease } from './dates'
import type { SeriesEntry } from './api'

/** The one localStorage key. Namespaced, because the site shares an origin
    with the API. */
export const WATCHLIST_STORAGE_KEY = 'audiosilo-meta:watchlist'

/** The stored schema's version. Bump it only for a change a reader's existing
    data cannot survive; readWatchlist discards any version it does not know,
    which is the whole point of storing one. */
export const WATCHLIST_VERSION = 1

/** The window CustomEvent a save dispatches, so anything outside the island
    that wrote it can notice. The header's "Watching" badge listens for it: its
    count is a pure function of the live store (see lib/watch-badge.ts), so a
    mark made on the watching page has to reach the header without a reload and
    without a refetch. Namespaced like the storage key. */
export const WATCHLIST_CHANGED_EVENT = 'audiosilo-meta:watchlist-changed'

/** One watched series. `owned`, `seen` and `skipped` hold WORK slugs, the
    catalogue's own identity - not titles and not positions, both of which a
    data repair may legitimately change under a reader. */
export interface WatchedSeries {
  /** The series name at the time it was watched, so the list renders before
      (or without) a successful fetch. */
  name: string
  /** `YYYY-MM-DD`, for ordering the list and for nothing else. */
  watchedAt: string
  /** Work slugs the reader has said they have. */
  owned: string[]
  /** Work slugs the reader has already been shown on /watching. What is NOT in
      here is what earns a "New" badge. */
  seen: string[]
  /** Work slugs the reader is not interested in. Deliberately its own list
      rather than a second meaning for `owned`: a reader dismissing a series'
      novellas is not saying they have them, and an ownership mark is what the
      "I have these" list and a later library import both read. */
  skipped: string[]
  /** Set on a series the reader does not want offered: it stays stored (so a
      later import does not suggest it again) but is left out of the watching
      page and of every import checklist. Absent, never false - one spelling of
      "not hidden". */
  hidden?: true
}

export interface Watchlist {
  version: typeof WATCHLIST_VERSION
  series: Record<string, WatchedSeries>
}

/** An empty store - also what every tolerant read degrades to. */
export function emptyWatchlist(): Watchlist {
  return { version: WATCHLIST_VERSION, series: {} }
}

// --- Parsing (tolerant by design) -------------------------------------------

/** Read an unknown value as a list of non-empty strings, dropping everything
    else and de-duplicating. Exported for lib/watch-badge.ts, whose cached work
    ids are the same tolerant shape. */
export function stringList(value: unknown): string[] {
  if (!Array.isArray(value)) return []
  const seen = new Set<string>()
  const out: string[] = []
  for (const v of value) {
    if (typeof v === 'string' && v && !seen.has(v)) {
      seen.add(v)
      out.push(v)
    }
  }
  return out
}

/** Keys that must never become an object's OWN key when a parsed document's
    keys turn into record keys below. `series['__proto__'] = entry` does not
    just store an entry under a slug called "__proto__" - bracket assignment of
    that name is a legacy accessor (Annex B) that REASSIGNS the object's own
    prototype to `entry`, so every slug looked up afterwards on that store (for
    the rest of the render, since a store is plain-object state threaded
    through the page) resolves through it instead of missing. `constructor` and
    `prototype` are blocked alongside it defensively, on the same reasoning:
    none of the three is ever a real series slug (the catalogue's slugs are
    `^[a-z0-9]+(-[a-z0-9]+)*$`, which "__proto__"'s underscores already fail),
    so skipping them costs no legitimate data. A stored watchlist is read from
    localStorage - editable by hand, by an extension, or by a stale/foreign
    version of the site - and an imported one is a backup file the reader
    picked, so neither's keys are trusted before this. */
function isUnsafeRecordKey(key: string): boolean {
  return key === '__proto__' || key === 'constructor' || key === 'prototype'
}

function parseSeries(value: unknown): WatchedSeries | null {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return null
  const raw = value as Record<string, unknown>
  const entry: WatchedSeries = {
    name: typeof raw.name === 'string' ? raw.name : '',
    watchedAt: typeof raw.watchedAt === 'string' ? raw.watchedAt : '',
    owned: stringList(raw.owned),
    seen: stringList(raw.seen),
    // Additive, so a document stored before skip marks existed parses into an
    // empty list rather than bumping WATCHLIST_VERSION and discarding a
    // reader's whole watchlist over a field they never had.
    skipped: stringList(raw.skipped),
  }
  if (raw.hidden === true) entry.hidden = true
  return entry
}

/**
 * Parse stored text into a store, tolerating anything. A reader's localStorage
 * is edited by hand, shared between site versions and occasionally truncated by
 * the browser, so the only safe reading of "this is not what I wrote" is an
 * empty store - never a throw on a page that has not rendered yet.
 *
 * A version this build does not know is discarded WHOLE rather than read
 * field-by-field: a future shape's `owned` may not mean what this one's does.
 */
export function parseWatchlist(text: string | null | undefined): Watchlist {
  if (!text) return emptyWatchlist()
  let parsed: unknown
  try {
    parsed = JSON.parse(text)
  } catch {
    return emptyWatchlist()
  }
  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return emptyWatchlist()
  const raw = parsed as Record<string, unknown>
  if (raw.version !== WATCHLIST_VERSION) return emptyWatchlist()
  const series: Record<string, WatchedSeries> = {}
  if (raw.series && typeof raw.series === 'object' && !Array.isArray(raw.series)) {
    for (const [slug, value] of Object.entries(raw.series as Record<string, unknown>)) {
      if (isUnsafeRecordKey(slug)) continue
      const entry = parseSeries(value)
      if (slug && entry) series[slug] = entry
    }
  }
  return { version: WATCHLIST_VERSION, series }
}

// --- Storage ----------------------------------------------------------------

/** The stored watchlist, or an empty one. Every failure - private mode, blocked
    storage, no localStorage at all, garbage in the slot - degrades to "nothing
    watched" rather than throwing, so a reader with storage off still gets a
    working page. */
export function readWatchlist(): Watchlist {
  try {
    return parseWatchlist(globalThis.localStorage?.getItem(WATCHLIST_STORAGE_KEY))
  } catch {
    return emptyWatchlist()
  }
}

/** Replace the stored watchlist with `store`, atomically (one key, one write).
    Silent on failure: the change still applies to the page in front of the
    reader, it just will not outlive it. */
export function writeWatchlist(store: Watchlist): void {
  try {
    globalThis.localStorage?.setItem(WATCHLIST_STORAGE_KEY, JSON.stringify(store))
  } catch {
    /* storage blocked - the change still applies for this page load */
  }
}

// --- Mutations (pure: store in, new store out) ------------------------------

function replaceSeries(
  store: Watchlist,
  slug: string,
  entry: WatchedSeries | null
): Watchlist {
  const series = { ...store.series }
  if (entry) series[slug] = entry
  else delete series[slug]
  return { version: WATCHLIST_VERSION, series }
}

/** `a` then whatever of `b` it did not already hold, first-seen order kept. A
    Set decides membership rather than a scan of `out`: markSeen unions a whole
    series' work list on every visit to the watching page, and a reader with a
    long list would otherwise pay that quadratically. */
function union(a: readonly string[], b: readonly string[]): string[] {
  const seen = new Set(a)
  const out = [...a]
  for (const v of b) {
    if (v && !seen.has(v)) {
      seen.add(v)
      out.push(v)
    }
  }
  return out
}

/** Is this series watched (hidden or not)? A hidden series is still watched -
    it is simply not offered - so the series page's toggle reads as on. */
export function isWatched(store: Watchlist, slug: string): boolean {
  return slug in store.series
}

/** The stored record for a series, or undefined. */
export function watchedSeries(store: Watchlist, slug: string): WatchedSeries | undefined {
  return store.series[slug]
}

/** Start watching a series. Watching one already watched only refreshes its
    name (the catalogue's spelling may have been corrected since) and clears
    `hidden` - re-watching is the explicit undo of hiding. */
export function watch(store: Watchlist, slug: string, name: string, today: string): Watchlist {
  const existing = store.series[slug]
  return replaceSeries(store, slug, {
    name: name || existing?.name || slug,
    watchedAt: existing?.watchedAt || today,
    owned: existing?.owned ?? [],
    seen: existing?.seen ?? [],
    skipped: existing?.skipped ?? [],
  })
}

/** Stop watching a series, forgetting its ownership marks with it. */
export function unwatch(store: Watchlist, slug: string): Watchlist {
  return replaceSeries(store, slug, null)
}

/** Mark one work of a series as owned, or not. A no-op on a series that is not
    watched: ownership is only meaningful inside a watch. */
export function setOwned(
  store: Watchlist,
  slug: string,
  workSlug: string,
  owned: boolean
): Watchlist {
  const entry = store.series[slug]
  if (!entry) return store
  const next = owned
    ? union(entry.owned, [workSlug])
    : entry.owned.filter((w) => w !== workSlug)
  return replaceSeries(store, slug, { ...entry, owned: next })
}

/** Mark one work of a series as "not interested", or not. Symmetric with
    setOwned - one mutation for both directions, because a skip is a toggle in
    front of the reader and an "unskip" of its own would be a second spelling of
    the same fact. A no-op on a series that is not watched. */
export function setSkipped(
  store: Watchlist,
  slug: string,
  workSlug: string,
  skipped: boolean
): Watchlist {
  const entry = store.series[slug]
  if (!entry) return store
  const next = skipped
    ? union(entry.skipped, [workSlug])
    : entry.skipped.filter((w) => w !== workSlug)
  return replaceSeries(store, slug, { ...entry, skipped: next })
}

/** Mark every listed work of a series as owned (a union, so a work already
    marked stays marked). */
export function markAllOwned(store: Watchlist, slug: string, workSlugs: readonly string[]): Watchlist {
  const entry = store.series[slug]
  if (!entry) return store
  return replaceSeries(store, slug, { ...entry, owned: union(entry.owned, workSlugs) })
}

/** Forget every ownership mark on a series, keeping the watch. */
export function clearOwned(store: Watchlist, slug: string): Watchlist {
  const entry = store.series[slug]
  if (!entry) return store
  return replaceSeries(store, slug, { ...entry, owned: [] })
}

/** Record that these works have been SHOWN to the reader on the watching page.
    A union, and never pruned back to the works currently listed: a series whose
    entry was retired and re-added should not come back as new. */
export function markSeen(store: Watchlist, slug: string, workSlugs: readonly string[]): Watchlist {
  const entry = store.series[slug]
  if (!entry) return store
  const seen = union(entry.seen, workSlugs)
  if (seen.length === entry.seen.length) return store // nothing new to record
  return replaceSeries(store, slug, { ...entry, seen })
}

/** Hide a series from the watching page and from import suggestions, keeping
    what is stored about it. Hiding a series that is not watched STARTS a hidden
    watch, which is what makes "do not offer me this series again" survive the
    next library import. */
export function hide(store: Watchlist, slug: string, name: string, today: string): Watchlist {
  const entry = store.series[slug]
  return replaceSeries(store, slug, {
    name: name || entry?.name || slug,
    watchedAt: entry?.watchedAt || today,
    owned: entry?.owned ?? [],
    seen: entry?.seen ?? [],
    skipped: entry?.skipped ?? [],
    hidden: true,
  })
}

/** Unhide a series: it returns to the watching page with its marks intact. */
export function unhide(store: Watchlist, slug: string): Watchlist {
  const entry = store.series[slug]
  if (!entry?.hidden) return store
  const { hidden: _hidden, ...rest } = entry
  return replaceSeries(store, slug, rest)
}

// --- Listing ----------------------------------------------------------------

/** One stored series with its slug, the shape every list renders. */
export interface WatchlistRow extends WatchedSeries {
  slug: string
}

function rows(store: Watchlist, hidden: boolean): WatchlistRow[] {
  return Object.entries(store.series)
    .filter(([, entry]) => Boolean(entry.hidden) === hidden)
    .map(([slug, entry]) => ({ slug, ...entry }))
    .sort((a, b) => a.name.localeCompare(b.name) || a.slug.localeCompare(b.slug))
}

/** The watched series to show, by name. */
export function visibleSeries(store: Watchlist): WatchlistRow[] {
  return rows(store, false)
}

/** The hidden series, by name - the "unhide one" disclosure's contents. */
export function hiddenSeries(store: Watchlist): WatchlistRow[] {
  return rows(store, true)
}

/**
 * The series a feed URL carries: every VISIBLE series' slug, sorted. Hidden
 * series never leave the browser.
 *
 * It lives HERE, in the leaf, rather than beside its first caller: both
 * lib/feed-url.ts (the URL's `s` value) and lib/watch-badge.ts (the cache key
 * that must name exactly the feed those ids came from) read it, and having the
 * badge reach through feed-url for it made the header's `await import(
 * '../lib/feed-url')` no lazy boundary at all - the module was already in the
 * header's chunk.
 */
export function watchedSlugs(store: Watchlist): string[] {
  return Object.entries(store.series)
    .filter(([, series]) => !series.hidden)
    .map(([slug]) => slug)
    .sort()
}

/**
 * Every work slug the reader has DEALT WITH, across every watched series -
 * seen, owned or skipped, HIDDEN series included.
 *
 * This and `classify` below are the two readers of those three lists, and they
 * read them differently on purpose. classify answers "where does this entry
 * belong in THIS series' panel", so it applies a per-series precedence (owned
 * beats skipped beats new). This answers "has the reader dealt with this book
 * at all", which is a global union with no precedence to apply - a mark made
 * under one series counts for the same work reached through another, and a
 * mark on a hidden series is still a mark (the alternative is a badge that goes
 * UP when you hide something).
 */
export function markedWorkIDs(store: Watchlist): Set<string> {
  const marked = new Set<string>()
  for (const entry of Object.values(store.series)) {
    for (const work of entry.seen) marked.add(work)
    for (const work of entry.owned) marked.add(work)
    for (const work of entry.skipped) marked.add(work)
  }
  return marked
}

// --- Classification ---------------------------------------------------------

/** A series entry the reader does not have, plus whether this is the first
    visit that lists it. */
export interface ClassifiedEntry {
  entry: SeriesEntry
  /** The work has not been recorded as SEEN yet - the "New" badge. */
  isNew: boolean
}

/** What a watched series looks like right now: what the reader is missing, what
    they can preorder, what they already have and what they have dismissed.
    Every entry lands in exactly one of the four. */
export interface SeriesClassification {
  available: ClassifiedEntry[]
  preorder: ClassifiedEntry[]
  owned: SeriesEntry[]
  skipped: SeriesEntry[]
}

/**
 * Split a series' entries for the watching page.
 *
 * Ownership wins: a work the reader has marked is `owned` whether or not it has
 * been released, because a preorder they have already placed is not something
 * to tell them about again. A work they have dismissed is `skipped` next -
 * owned beats skipped, since a book they went on to buy is no longer one they
 * passed on. Of the rest, an entry whose release date is still
 * ahead of `today` (see dates.isFutureRelease - compared at the precision the
 * date states) is a `preorder` and everything else is `available`, INCLUDING an
 * entry with no date at all: a catalogued work with no stated release date is
 * overwhelmingly an older book nobody recorded a date for, and calling it a
 * preorder would put it in the one bucket the reader cannot act on.
 *
 * A skip is a PAGE-side filter only: the feed URL and the calendar URL carry
 * series slugs and nothing else (lib/feed-url.ts), so the server cannot know
 * about it and a skipped work still arrives in the reader's feed and calendar.
 *
 * Order is the series' own, which is position order - so the list reads like
 * the series does.
 */
export function classify(
  entries: readonly SeriesEntry[],
  watched: WatchedSeries | undefined,
  today: string
): SeriesClassification {
  const owned = new Set(watched?.owned ?? [])
  const seen = new Set(watched?.seen ?? [])
  const skipped = new Set(watched?.skipped ?? [])
  const out: SeriesClassification = { available: [], preorder: [], owned: [], skipped: [] }
  for (const entry of entries) {
    if (owned.has(entry.work.id)) {
      out.owned.push(entry)
      continue
    }
    if (skipped.has(entry.work.id)) {
      out.skipped.push(entry)
      continue
    }
    const classified: ClassifiedEntry = { entry, isNew: !seen.has(entry.work.id) }
    if (isFutureRelease(entry.work.release_date, today)) out.preorder.push(classified)
    else out.available.push(classified)
  }
  return out
}

// --- Backup -----------------------------------------------------------------

/** The watchlist as a downloadable document - the stored bytes, pretty-printed.
    Deliberately the SAME shape as the stored value rather than an export format
    of its own: one schema is one thing to keep in step, and a reader who pastes
    a backup straight into their browser storage gets what they expect. */
export function exportJSON(store: Watchlist): string {
  return JSON.stringify(store, null, 2)
}

/**
 * Merge a backup into `store`. A series in both keeps the CURRENT name and
 * watch date and gains the union of the two sides' owned, seen and skipped
 * lists, so a
 * merge can only ever add knowledge - importing a stale backup never un-marks a
 * book the reader has since said they have.
 *
 * `hidden` is the one field a merge can turn ON but not off (either side hiding
 * a series hides it), because the alternative is a backup silently re-offering
 * a series the reader dismissed. Unhiding stays an explicit click.
 *
 * Unreadable input yields an empty merge rather than a throw - it goes through
 * parseWatchlist, the same tolerant reader the stored value gets.
 */
export function importJSON(store: Watchlist, text: string): Watchlist {
  const incoming = parseWatchlist(text)
  let next = store
  for (const [slug, entry] of Object.entries(incoming.series)) {
    // Belt and braces: parseWatchlist above already drops an unsafe key (see
    // isUnsafeRecordKey), so `incoming.series` cannot carry one today - but
    // this loop also assigns INTO `next.series[slug]` below, and a future
    // change to how `incoming` is produced should not have to rediscover that
    // guard to stay safe.
    if (isUnsafeRecordKey(slug)) continue
    const mine = next.series[slug]
    if (!mine) {
      next = replaceSeries(next, slug, entry)
      continue
    }
    const merged: WatchedSeries = {
      name: mine.name || entry.name,
      watchedAt: mine.watchedAt || entry.watchedAt,
      owned: union(mine.owned, entry.owned),
      seen: union(mine.seen, entry.seen),
      skipped: union(mine.skipped, entry.skipped),
    }
    if (mine.hidden || entry.hidden) merged.hidden = true
    next = replaceSeries(next, slug, merged)
  }
  return next
}

/** Did this text hold a watchlist at all? The import control uses it to tell a
    reader "that is not a watchlist backup" instead of silently merging
    nothing. */
export function looksLikeWatchlist(text: string): boolean {
  try {
    const parsed: unknown = JSON.parse(text)
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return false
    const raw = parsed as Record<string, unknown>
    // `typeof null === 'object'`, so the series check has to exclude null (and
    // an array) explicitly - otherwise `{"version":1,"series":null}` reads as a
    // watchlist and the import control reports a successful merge of nothing.
    return (
      raw.version === WATCHLIST_VERSION &&
      Boolean(raw.series) &&
      typeof raw.series === 'object' &&
      !Array.isArray(raw.series)
    )
  } catch {
    return false
  }
}
