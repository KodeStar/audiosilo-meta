// The header's "Watching" badge: how many books a reader has not dealt with
// yet, on every page of the site rather than only on /watching.
//
// The count comes from the reader's OWN JSON feed - the same stateless
// `watch/feed.json` the watching page builds a subscription URL for - fetched
// at most once an hour and cached in localStorage. That is deliberate: the
// header renders on every page, and composing the count the way /watching does
// (one series request per watched series) would turn a docs page into thirty
// API calls.
//
// THE CACHE STORES WORK IDS, NOT A COUNT. That is the whole design: with the
// ids in hand the badge is a PURE FUNCTION of the cache and the LIVE watchlist
// (badgeCount below), so the moment a reader marks a book seen, owned or
// skipped - on /watching, on a series page, in another tab - the badge drops
// without waiting for the cache to expire and without a second fetch. A stored
// count could only be corrected by refetching, which would mean either a stale
// badge for up to an hour or an hourly cache that is not really a cache.
//
// KNOWN LIMITATION: the feed's default window is 90 days, so a work the reader
// has never seen that was catalogued before that window is counted on the
// watching page and NOT in the badge. The badge is deliberately the smaller,
// cheaper number - "anything new lately" - and the page is the complete answer.
//
// Everything except the DOM glue and the fetch itself lives here, pure and
// tested, on the precedent of lib/watchlist.ts.

import { entitySlugFromLocation } from './entity-url'
import { watchedSlugs } from './feed-url'
import { stringList, type Watchlist } from './watchlist'

/** The one localStorage key for the badge's cache. Namespaced, and separate
    from the watchlist's: this is derived data with a TTL, and losing it costs a
    refetch rather than a reader's marks. */
export const WATCH_BADGE_STORAGE_KEY = 'audiosilo-meta:watch-badge'

/** How long a fetched feed is reused. One hour, which is also the feed's own
    public cache lifetime (internal/serve/watchfeed.go), so a shorter TTL here
    would mostly re-read a CDN copy of what we already have. */
export const WATCH_BADGE_TTL_MS = 60 * 60 * 1000

export interface BadgeCache {
  version: 1
  /** The watchlist the ids were fetched FOR - see badgeSlugKey. A cache taken
      under a different set of series says nothing about this one. */
  slugKey: string
  /** Epoch milliseconds, for the TTL. */
  fetchedAt: number
  /** The work slugs the feed carried, in feed order. */
  works: string[]
}

const BADGE_VERSION = 1

/** Which series the cache belongs to: the feed URL's own series list, joined
    ('' when none). BY CONSTRUCTION the same set in the same order, because it
    is literally what buildFeedURLs reads (lib/feed-url.ts watchedSlugs) - a
    hidden series is not in the feed, so it must not be in the key either, or
    hiding one would look like a changed watchlist forever. */
export function badgeSlugKey(store: Watchlist): string {
  return watchedSlugs(store).join(',')
}

/** Parse a stored cache, tolerating anything - a truncated write, a shape from
    another version of the site, a hand edit. Anything unrecognised is `null`,
    which every caller reads as "no cache", never a throw on a page that has not
    rendered. */
export function parseBadgeCache(text: string | null | undefined): BadgeCache | null {
  if (!text) return null
  let parsed: unknown
  try {
    parsed = JSON.parse(text)
  } catch {
    return null
  }
  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return null
  const raw = parsed as Record<string, unknown>
  if (raw.version !== BADGE_VERSION) return null
  if (typeof raw.slugKey !== 'string') return null
  if (typeof raw.fetchedAt !== 'number' || !Number.isFinite(raw.fetchedAt)) return null
  if (!Array.isArray(raw.works)) return null
  return {
    version: BADGE_VERSION,
    slugKey: raw.slugKey,
    fetchedAt: raw.fetchedAt,
    works: stringList(raw.works),
  }
}

/** The stored cache, or null. Every storage failure - private mode, blocked
    storage, no localStorage at all - degrades to "no cache", which costs one
    fetch rather than a broken header. */
export function readBadgeCache(): BadgeCache | null {
  try {
    return parseBadgeCache(globalThis.localStorage?.getItem(WATCH_BADGE_STORAGE_KEY))
  } catch {
    return null
  }
}

/** Replace the stored cache. Silent on failure: the badge still renders for
    this page load, it just refetches on the next one. */
export function writeBadgeCache(cache: BadgeCache): void {
  try {
    globalThis.localStorage?.setItem(WATCH_BADGE_STORAGE_KEY, JSON.stringify(cache))
  } catch {
    /* storage blocked - the count still applies for this page load */
  }
}

/**
 * May this cache be used as is? Only when it was taken for the SAME series
 * (watching or hiding one changes what the feed would return) and is inside the
 * TTL.
 *
 * A NEGATIVE age is stale too: a clock moved back would otherwise pin a cache
 * as fresh for as long as the offset lasts, and a laptop returning from a
 * different timezone is not rare enough to ignore.
 */
export function isBadgeFresh(
  cache: BadgeCache | null,
  slugKey: string,
  nowMs: number
): boolean {
  if (!cache || cache.slugKey !== slugKey) return false
  const age = nowMs - cache.fetchedAt
  return age >= 0 && age < WATCH_BADGE_TTL_MS
}

/**
 * The work slugs a JSON Feed 1.1 document names, distinct and in feed order.
 *
 * Read off each item's `url` rather than its `id`: the id carries a
 * `preorder`/`released` suffix so that a preorder becoming a release is a NEW
 * feed item, which is right for a feed reader and wrong for a count - the badge
 * would show one book twice.
 *
 * Tolerant throughout, because this parses a network response on every page of
 * the site: a non-object document, a missing `items`, an item that is not an
 * object, a url that is not a string or does not look like a work page are all
 * simply skipped. The badge under-counting is a nuisance; a header that throws
 * is a broken site.
 */
export function workIDsFromFeed(feed: unknown): string[] {
  if (!feed || typeof feed !== 'object' || Array.isArray(feed)) return []
  const items = (feed as Record<string, unknown>).items
  if (!Array.isArray(items)) return []
  const seen = new Set<string>()
  const out: string[] = []
  for (const item of items) {
    if (!item || typeof item !== 'object' || Array.isArray(item)) continue
    const url = (item as Record<string, unknown>).url
    if (typeof url !== 'string') continue
    const slug = workSlugFromURL(url)
    if (!slug || seen.has(slug)) continue
    seen.add(slug)
    out.push(slug)
  }
  return out
}

/** The work slug an item URL names, or null. The item URL is
    `<siteURL>/works/<slug>` (internal/serve/watchfeed.go), so the path is read
    by the islands' own `/works/{slug}` rule rather than a second regex - and
    the base only settles a relative value, since the feed always states an
    absolute one. A URL this cannot parse is skipped. */
function workSlugFromURL(url: string): string | null {
  let parsed: URL
  try {
    parsed = new URL(url, 'https://meta.audiosilo.app/')
  } catch {
    return null
  }
  return entitySlugFromLocation(parsed.pathname, parsed.search, 'work')
}

/**
 * How many of these works the reader has not dealt with: the ids that appear in
 * NO watched series' seen, owned or skipped list.
 *
 * HIDDEN series count too. A mark is a mark - a reader who owned a book and
 * then hid its series has still said they have it - and the alternative reads
 * as a badge that goes UP when you hide something.
 *
 * Pure, and over the LIVE store: that is what lets the cached ids stay valid
 * while the count moves the instant a mark changes.
 */
export function badgeCount(works: readonly string[], store: Watchlist): number {
  const marked = new Set<string>()
  for (const entry of Object.values(store.series)) {
    for (const work of entry.seen) marked.add(work)
    for (const work of entry.owned) marked.add(work)
    for (const work of entry.skipped) marked.add(work)
  }
  let count = 0
  for (const work of works) if (!marked.has(work)) count += 1
  return count
}

/**
 * Fetch one watch JSON feed and read the work slugs out of it. The header's
 * badge script calls this rather than holding a fetch of its own: network calls
 * live in lib/ (the rule lib/api.ts states), so an inline `fetch` in an Astro
 * <script> would be the one copy nothing tests.
 *
 * Throws on a non-2xx response; the caller decides whether a failed refresh is
 * worth telling anybody about (it is not - the badge keeps its cached count).
 */
export async function fetchWatchFeedWorkIDs(
  url: string,
  fetchImpl: typeof fetch = fetch
): Promise<string[]> {
  const res = await fetchImpl(url, { headers: { accept: 'application/feed+json' } })
  if (!res.ok) throw new Error(`watch feed: HTTP ${res.status}`)
  return workIDsFromFeed(await res.json())
}
