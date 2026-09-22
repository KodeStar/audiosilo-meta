import { describe, it, expect, afterEach, vi } from 'vitest'
import {
  WATCH_BADGE_STORAGE_KEY,
  WATCH_BADGE_TTL_MS,
  badgeCount,
  badgeSlugKey,
  fetchWatchFeedWorkIDs,
  isBadgeFresh,
  newBadgeCache,
  parseBadgeCache,
  readBadgeCache,
  workIDsFromFeed,
  writeBadgeCache,
  type BadgeCache,
} from './watch-badge'
import { stubStorage } from './test-support'
import {
  emptyWatchlist,
  hide,
  markSeen,
  setOwned,
  setSkipped,
  watch,
  type Watchlist,
} from './watchlist'

const TODAY = '2026-09-21'
const NOW = 1_758_000_000_000

afterEach(() => {
  vi.unstubAllGlobals()
})

function cache(over: Partial<BadgeCache> = {}): BadgeCache {
  return { version: 1, slugKey: 'a,b', fetchedAt: NOW, works: ['w1', 'w2'], ...over }
}

function feed(urls: unknown[]): unknown {
  return { version: 'https://jsonfeed.org/version/1.1', items: urls.map((url) => ({ url })) }
}

describe('badgeSlugKey', () => {
  it('is the visible slugs, sorted and comma-joined', () => {
    let store = watch(emptyWatchlist(), 'zeta', 'Zeta', TODAY)
    store = watch(store, 'alpha', 'Alpha', TODAY)
    expect(badgeSlugKey(store)).toBe('alpha,zeta')
  })
  it('leaves out hidden series, and is empty when nothing is visible', () => {
    // A hidden series is not in the feed URL either, so it must not be in the
    // key - hiding one would otherwise invalidate the cache forever.
    const store = hide(watch(emptyWatchlist(), 'alpha', 'Alpha', TODAY), 'beta', 'Beta', TODAY)
    expect(badgeSlugKey(store)).toBe('alpha')
    expect(badgeSlugKey(emptyWatchlist())).toBe('')
  })
})

describe('workIDsFromFeed', () => {
  it('reads the work slug out of each item url, distinct and in feed order', () => {
    expect(
      workIDsFromFeed(
        feed([
          'https://meta.audiosilo.app/works/killing-floor',
          'https://meta.audiosilo.app/works/die-trying',
          'https://meta.audiosilo.app/works/killing-floor', // the preorder/released pair
        ])
      )
    ).toEqual(['killing-floor', 'die-trying'])
  })
  it('decodes a percent-encoded segment', () => {
    expect(workIDsFromFeed(feed(['https://meta.audiosilo.app/works/a%2Db']))).toEqual(['a-b'])
  })
  it('skips anything that is not a work page or not a string', () => {
    expect(
      workIDsFromFeed(
        feed([
          'https://meta.audiosilo.app/series/jack-reacher',
          'https://meta.audiosilo.app/works/',
          // The shared `/works/{slug}` reader (lib/entity-url.ts) matches the
          // path EXACTLY, so a trailing slash is not a work page. The feed
          // never writes one (internal/serve/watchfeed.go).
          'https://meta.audiosilo.app/works/die-trying/',
          'https://meta.audiosilo.app/works/killing-floor/recap',
          'not a url at all',
          42,
          null,
        ])
      )
    ).toEqual([])
    expect(workIDsFromFeed({ items: [null, 'a string', { id: 'no url' }] })).toEqual([])
  })
  it('returns an empty list for anything that is not a feed document', () => {
    expect(workIDsFromFeed(undefined)).toEqual([])
    expect(workIDsFromFeed(null)).toEqual([])
    expect(workIDsFromFeed('a string')).toEqual([])
    expect(workIDsFromFeed([])).toEqual([])
    expect(workIDsFromFeed({})).toEqual([]) // items missing
    expect(workIDsFromFeed({ items: 'nope' })).toEqual([])
  })
})

describe('badgeCount', () => {
  const works = ['w1', 'w2', 'w3']

  function watching(): Watchlist {
    return watch(emptyWatchlist(), 'series', 'Series', TODAY)
  }

  it('counts everything when nothing is marked', () => {
    expect(badgeCount(works, watching())).toBe(3)
    expect(badgeCount(works, emptyWatchlist())).toBe(3)
    expect(badgeCount([], watching())).toBe(0)
  })
  it('excludes a work marked seen, owned or skipped', () => {
    expect(badgeCount(works, markSeen(watching(), 'series', ['w1']))).toBe(2)
    expect(badgeCount(works, setOwned(watching(), 'series', 'w2', true))).toBe(2)
    expect(badgeCount(works, setSkipped(watching(), 'series', 'w3', true))).toBe(2)
  })
  it('counts a mark in a HIDDEN series - a mark is a mark', () => {
    // Otherwise the badge would go UP when a reader hides a series.
    let store = setOwned(watching(), 'series', 'w1', true)
    store = hide(store, 'series', 'Series', TODAY)
    expect(badgeCount(works, store)).toBe(2)
  })
  it('reads marks from every series, whichever one the work came from', () => {
    let store = watch(watching(), 'other', 'Other', TODAY)
    store = setOwned(store, 'other', 'w1', true)
    store = markSeen(store, 'series', ['w2'])
    expect(badgeCount(works, store)).toBe(1)
  })
})

describe('isBadgeFresh', () => {
  it('is true inside the window for the same series', () => {
    expect(isBadgeFresh(cache(), 'a,b', NOW)).toBe(true)
    expect(isBadgeFresh(cache(), 'a,b', NOW + WATCH_BADGE_TTL_MS - 1)).toBe(true)
  })
  it('is false with no cache, on a changed slug key, and at the TTL', () => {
    expect(isBadgeFresh(null, 'a,b', NOW)).toBe(false)
    expect(isBadgeFresh(cache(), 'a,b,c', NOW)).toBe(false)
    expect(isBadgeFresh(cache(), 'a,b', NOW + WATCH_BADGE_TTL_MS)).toBe(false)
  })
  it('is false for a negative age - the clock moved back', () => {
    expect(isBadgeFresh(cache(), 'a,b', NOW - 1)).toBe(false)
  })
})

describe('parseBadgeCache', () => {
  it('reads what writeBadgeCache writes', () => {
    expect(parseBadgeCache(JSON.stringify(cache()))).toEqual(cache())
  })
  it('drops a non-string work rather than the whole cache', () => {
    expect(parseBadgeCache(JSON.stringify(cache({ works: ['w1', 3, '', null] as unknown as string[] })))).toEqual(
      cache({ works: ['w1'] })
    )
  })
  it('returns null for anything it does not recognise', () => {
    expect(parseBadgeCache(null)).toBeNull()
    expect(parseBadgeCache('')).toBeNull()
    expect(parseBadgeCache('not json')).toBeNull()
    expect(parseBadgeCache('[]')).toBeNull()
    expect(parseBadgeCache(JSON.stringify(cache({ version: 2 as unknown as 1 })))).toBeNull()
    expect(parseBadgeCache(JSON.stringify({ ...cache(), slugKey: undefined }))).toBeNull()
    expect(parseBadgeCache(JSON.stringify({ ...cache(), fetchedAt: 'soon' }))).toBeNull()
    expect(parseBadgeCache(JSON.stringify({ ...cache(), works: 'w1' }))).toBeNull()
  })
})

describe('storage helpers', () => {
  it('round-trips under the namespaced key', () => {
    const map = stubStorage()
    writeBadgeCache(cache())
    expect(map.get(WATCH_BADGE_STORAGE_KEY)).toBeDefined()
    expect(readBadgeCache()).toEqual(cache())
  })
  it('reads null when nothing is stored', () => {
    stubStorage()
    expect(readBadgeCache()).toBeNull()
  })
  it('degrades when storage throws', () => {
    stubStorage({ throws: true })
    expect(readBadgeCache()).toBeNull()
    expect(() => writeBadgeCache(cache())).not.toThrow()
  })
  it('degrades when there is no localStorage at all', () => {
    vi.stubGlobal('localStorage', undefined)
    expect(readBadgeCache()).toBeNull()
    expect(() => writeBadgeCache(cache())).not.toThrow()
  })
})

describe('fetchWatchFeedWorkIDs', () => {
  it('asks for a JSON feed and returns the work slugs', async () => {
    const calls: [string, RequestInit | undefined][] = []
    const stub = (async (url: string, init?: RequestInit) => {
      calls.push([url, init])
      return {
        ok: true,
        status: 200,
        json: async () => feed(['https://meta.audiosilo.app/works/killing-floor']),
      }
    }) as unknown as typeof fetch
    await expect(fetchWatchFeedWorkIDs('https://x/api/v1/watch/feed.json?s=a', stub)).resolves.toEqual([
      'killing-floor',
    ])
    expect(calls).toHaveLength(1)
    expect(calls[0][0]).toBe('https://x/api/v1/watch/feed.json?s=a')
    expect(calls[0][1]?.headers).toEqual({ accept: 'application/feed+json' })
  })

  it('throws on a non-ok response', async () => {
    const stub = (async () => ({
      ok: false,
      status: 503,
      json: async () => ({}),
    })) as unknown as typeof fetch
    await expect(fetchWatchFeedWorkIDs('https://x/feed.json', stub)).rejects.toThrow('503')
  })
})

// The header used to spell `version: 1` itself, which a bump in watch-badge.ts
// would have left behind as a document parseBadgeCache rejects on the next
// load. The version is this module's to state.
describe('newBadgeCache', () => {
  it('stamps the current version and reads back through parseBadgeCache', () => {
    const cache = newBadgeCache('alpha,beta', ['w1', 'w2'], 1_700_000_000_000)
    expect(cache).toEqual({
      version: 1,
      slugKey: 'alpha,beta',
      fetchedAt: 1_700_000_000_000,
      works: ['w1', 'w2'],
    })
    expect(parseBadgeCache(JSON.stringify(cache))).toEqual(cache)
    expect(isBadgeFresh(cache, 'alpha,beta', 1_700_000_000_000)).toBe(true)
  })
  it('defaults the timestamp to now', () => {
    const before = Date.now()
    const cache = newBadgeCache('', [])
    expect(cache.fetchedAt).toBeGreaterThanOrEqual(before)
  })
})
