import { describe, it, expect, afterEach, vi } from 'vitest'
import type { SeriesEntry } from './api'
import {
  WATCHLIST_STORAGE_KEY,
  WATCHLIST_VERSION,
  classify,
  clearOwned,
  emptyWatchlist,
  exportJSON,
  hide,
  hiddenSeries,
  importJSON,
  isWatched,
  looksLikeWatchlist,
  markAllOwned,
  markSeen,
  parseWatchlist,
  readWatchlist,
  setOwned,
  setSkipped,
  unhide,
  unwatch,
  visibleSeries,
  watch,
  watchedSeries,
  writeWatchlist,
  type Watchlist,
} from './watchlist'

const TODAY = '2026-09-21'

// A store holding one watched series, built through the public mutations so the
// tests never hand-write the stored shape.
function oneSeries(slug = 'the-wandering-inn', name = 'The Wandering Inn'): Watchlist {
  return watch(emptyWatchlist(), slug, name, TODAY)
}

function entry(id: string, position: string, release_date?: string): SeriesEntry {
  return {
    position,
    work: { id, title: id, authors: [], release_date },
  }
}

// A minimal in-memory localStorage, as marketplace.test.ts stubs one.
function stubStorage(opts: { throws?: boolean } = {}) {
  const map = new Map<string, string>()
  vi.stubGlobal('localStorage', {
    getItem: (k: string) => {
      if (opts.throws) throw new Error('blocked')
      return map.get(k) ?? null
    },
    setItem: (k: string, v: string) => {
      if (opts.throws) throw new Error('blocked')
      map.set(k, v)
    },
  })
  return map
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('watch / unwatch', () => {
  it('starts a watch with no marks', () => {
    const store = oneSeries()
    expect(isWatched(store, 'the-wandering-inn')).toBe(true)
    expect(watchedSeries(store, 'the-wandering-inn')).toEqual({
      name: 'The Wandering Inn',
      watchedAt: TODAY,
      owned: [],
      seen: [],
      skipped: [],
    })
  })
  it('keeps the original watch date and marks when re-watched, refreshing the name', () => {
    const store = setOwned(oneSeries(), 'the-wandering-inn', 'volume-1', true)
    const again = watch(store, 'the-wandering-inn', 'The Wandering Inn (corrected)', '2026-12-01')
    expect(watchedSeries(again, 'the-wandering-inn')).toEqual({
      name: 'The Wandering Inn (corrected)',
      watchedAt: TODAY,
      owned: ['volume-1'],
      seen: [],
      skipped: [],
    })
  })
  it('unwatching forgets the series entirely', () => {
    const store = unwatch(setOwned(oneSeries(), 'the-wandering-inn', 'volume-1', true), 'the-wandering-inn')
    expect(isWatched(store, 'the-wandering-inn')).toBe(false)
  })
  it('never mutates the store it was given', () => {
    const before = oneSeries()
    const snapshot = JSON.stringify(before)
    setOwned(before, 'the-wandering-inn', 'volume-1', true)
    unwatch(before, 'the-wandering-inn')
    expect(JSON.stringify(before)).toBe(snapshot)
  })
})

describe('ownership marks', () => {
  it('sets, unsets, bulk-sets and clears', () => {
    let store = oneSeries()
    store = setOwned(store, 'the-wandering-inn', 'volume-1', true)
    store = setOwned(store, 'the-wandering-inn', 'volume-1', true) // idempotent
    expect(watchedSeries(store, 'the-wandering-inn')?.owned).toEqual(['volume-1'])

    store = markAllOwned(store, 'the-wandering-inn', ['volume-1', 'volume-2', 'volume-3'])
    expect(watchedSeries(store, 'the-wandering-inn')?.owned).toEqual([
      'volume-1',
      'volume-2',
      'volume-3',
    ])

    store = setOwned(store, 'the-wandering-inn', 'volume-2', false)
    expect(watchedSeries(store, 'the-wandering-inn')?.owned).toEqual(['volume-1', 'volume-3'])

    store = clearOwned(store, 'the-wandering-inn')
    expect(watchedSeries(store, 'the-wandering-inn')?.owned).toEqual([])
  })
  it('is a no-op on a series that is not watched', () => {
    const store = emptyWatchlist()
    expect(setOwned(store, 'nope', 'volume-1', true)).toBe(store)
    expect(markAllOwned(store, 'nope', ['volume-1'])).toBe(store)
    expect(clearOwned(store, 'nope')).toBe(store)
    expect(markSeen(store, 'nope', ['volume-1'])).toBe(store)
  })
})

describe('markSeen', () => {
  it('unions, and returns the same store when it records nothing new', () => {
    const store = markSeen(oneSeries(), 'the-wandering-inn', ['volume-1'])
    expect(watchedSeries(store, 'the-wandering-inn')?.seen).toEqual(['volume-1'])
    expect(markSeen(store, 'the-wandering-inn', ['volume-1'])).toBe(store)
    expect(
      watchedSeries(markSeen(store, 'the-wandering-inn', ['volume-2']), 'the-wandering-inn')?.seen
    ).toEqual(['volume-1', 'volume-2'])
  })
})

describe('skip marks', () => {
  it('sets and unsets, independently of ownership', () => {
    let store = setSkipped(oneSeries(), 'the-wandering-inn', 'novella-1', true)
    store = setSkipped(store, 'the-wandering-inn', 'novella-1', true) // idempotent
    expect(watchedSeries(store, 'the-wandering-inn')?.skipped).toEqual(['novella-1'])
    expect(watchedSeries(store, 'the-wandering-inn')?.owned).toEqual([])

    store = setSkipped(store, 'the-wandering-inn', 'novella-2', true)
    store = setSkipped(store, 'the-wandering-inn', 'novella-1', false)
    expect(watchedSeries(store, 'the-wandering-inn')?.skipped).toEqual(['novella-2'])
  })
  it('is a no-op on a series that is not watched', () => {
    const store = emptyWatchlist()
    expect(setSkipped(store, 'nope', 'volume-1', true)).toBe(store)
  })
  it('never mutates the store it was given', () => {
    const before = oneSeries()
    const snapshot = JSON.stringify(before)
    setSkipped(before, 'the-wandering-inn', 'volume-1', true)
    expect(JSON.stringify(before)).toBe(snapshot)
  })

  const entries = [
    entry('v1', '1', '2024-01-10'),
    entry('v2', '2', '2026-09-21'), // out today
    entry('v3', '3', '2026-12-01'), // preorder
  ]

  it('classify lands every entry in exactly one of the four buckets', () => {
    let store = setOwned(oneSeries(), 'the-wandering-inn', 'v1', true)
    store = setSkipped(store, 'the-wandering-inn', 'v2', true)
    const got = classify(entries, watchedSeries(store, 'the-wandering-inn'), TODAY)
    expect(got.owned.map((e) => e.work.id)).toEqual(['v1'])
    expect(got.skipped.map((e) => e.work.id)).toEqual(['v2'])
    expect(got.available).toEqual([])
    expect(got.preorder.map((c) => c.entry.work.id)).toEqual(['v3'])
  })
  it('owned beats skipped', () => {
    let store = setSkipped(oneSeries(), 'the-wandering-inn', 'v1', true)
    store = setOwned(store, 'the-wandering-inn', 'v1', true)
    const got = classify(entries, watchedSeries(store, 'the-wandering-inn'), TODAY)
    expect(got.owned.map((e) => e.work.id)).toEqual(['v1'])
    expect(got.skipped).toEqual([])
  })
  it('skipped beats preorder and available', () => {
    let store = setSkipped(oneSeries(), 'the-wandering-inn', 'v2', true)
    store = setSkipped(store, 'the-wandering-inn', 'v3', true)
    const got = classify(entries, watchedSeries(store, 'the-wandering-inn'), TODAY)
    expect(got.skipped.map((e) => e.work.id)).toEqual(['v2', 'v3'])
    expect(got.available.map((c) => c.entry.work.id)).toEqual(['v1'])
    expect(got.preorder).toEqual([])
  })
  it('parses a stored series written before skip marks existed', () => {
    // Additive, so WATCHLIST_VERSION did not move and the document still reads.
    const got = parseWatchlist(
      JSON.stringify({
        version: WATCHLIST_VERSION,
        series: { old: { name: 'Old', watchedAt: TODAY, owned: ['a'], seen: ['b'] } },
      })
    )
    expect(got.series.old).toEqual({
      name: 'Old',
      watchedAt: TODAY,
      owned: ['a'],
      seen: ['b'],
      skipped: [],
    })
  })
  it('importJSON unions it and exportJSON round-trips it', () => {
    const mine = setSkipped(oneSeries(), 'the-wandering-inn', 'v1', true)
    const backup = setSkipped(
      watch(emptyWatchlist(), 'the-wandering-inn', 'Stale Name', '2020-01-01'),
      'the-wandering-inn',
      'v2',
      true
    )
    const merged = importJSON(mine, exportJSON(backup))
    expect(watchedSeries(merged, 'the-wandering-inn')?.skipped).toEqual(['v1', 'v2'])
    expect(parseWatchlist(exportJSON(merged))).toEqual(merged)
  })
})

describe('hide / unhide', () => {
  it('hides a watched series without losing its marks', () => {
    let store = setOwned(oneSeries(), 'the-wandering-inn', 'volume-1', true)
    store = hide(store, 'the-wandering-inn', 'The Wandering Inn', TODAY)
    expect(visibleSeries(store)).toEqual([])
    expect(hiddenSeries(store).map((r) => r.slug)).toEqual(['the-wandering-inn'])
    expect(watchedSeries(store, 'the-wandering-inn')?.owned).toEqual(['volume-1'])

    store = unhide(store, 'the-wandering-inn')
    expect(visibleSeries(store).map((r) => r.slug)).toEqual(['the-wandering-inn'])
    expect(watchedSeries(store, 'the-wandering-inn')?.hidden).toBeUndefined()
  })
  it('hiding a series that is not watched starts a hidden watch', () => {
    // That is what makes "do not offer me this series again" survive the next
    // library import.
    const store = hide(emptyWatchlist(), 'dismissed', 'Dismissed', TODAY)
    expect(isWatched(store, 'dismissed')).toBe(true)
    expect(visibleSeries(store)).toEqual([])
  })
  it('watching a hidden series unhides it', () => {
    const store = watch(hide(emptyWatchlist(), 'x', 'X', TODAY), 'x', 'X', TODAY)
    expect(visibleSeries(store).map((r) => r.slug)).toEqual(['x'])
  })
})

describe('listing', () => {
  it('sorts by name', () => {
    let store = watch(emptyWatchlist(), 'z', 'Alpha', TODAY)
    store = watch(store, 'a', 'Omega', TODAY)
    expect(visibleSeries(store).map((r) => r.name)).toEqual(['Alpha', 'Omega'])
  })
})

describe('classify', () => {
  const entries = [
    entry('v1', '1', '2024-01-10'),
    entry('v2', '2', '2026-09-21'), // out today
    entry('v3', '3', '2026-12-01'), // preorder
    entry('v4', '4'), // no date at all
  ]

  it('splits into available, preorder and owned', () => {
    const store = setOwned(oneSeries(), 'the-wandering-inn', 'v1', true)
    const got = classify(entries, watchedSeries(store, 'the-wandering-inn'), TODAY)
    expect(got.owned.map((e) => e.work.id)).toEqual(['v1'])
    expect(got.available.map((c) => c.entry.work.id)).toEqual(['v2', 'v4'])
    expect(got.preorder.map((c) => c.entry.work.id)).toEqual(['v3'])
  })
  it('lands every entry in exactly one bucket', () => {
    const got = classify(entries, watchedSeries(oneSeries(), 'the-wandering-inn'), TODAY)
    expect(
      got.available.length + got.preorder.length + got.owned.length + got.skipped.length
    ).toBe(entries.length)
  })
  it('owning a preorder keeps it out of the preorder list', () => {
    const store = setOwned(oneSeries(), 'the-wandering-inn', 'v3', true)
    const got = classify(entries, watchedSeries(store, 'the-wandering-inn'), TODAY)
    expect(got.preorder).toEqual([])
    expect(got.owned.map((e) => e.work.id)).toEqual(['v3'])
  })
  it('flags everything not yet seen as new', () => {
    const store = markSeen(oneSeries(), 'the-wandering-inn', ['v2'])
    const got = classify(entries, watchedSeries(store, 'the-wandering-inn'), TODAY)
    expect(got.available.map((c) => [c.entry.work.id, c.isNew])).toEqual([
      ['v1', true],
      ['v2', false],
      ['v4', true],
    ])
    expect(got.preorder.map((c) => c.isNew)).toEqual([true])
  })
  it('treats an unwatched series as nothing owned and nothing seen', () => {
    const got = classify(entries, undefined, TODAY)
    expect(got.owned).toEqual([])
    expect(got.available.every((c) => c.isNew)).toBe(true)
  })
})

describe('parseWatchlist', () => {
  it('reads what exportJSON writes', () => {
    const store = markSeen(setOwned(oneSeries(), 'the-wandering-inn', 'v1', true), 'the-wandering-inn', ['v2'])
    expect(parseWatchlist(exportJSON(store))).toEqual(store)
  })
  it('degrades to empty on anything it does not recognise', () => {
    const empty = emptyWatchlist()
    expect(parseWatchlist(null)).toEqual(empty)
    expect(parseWatchlist('')).toEqual(empty)
    expect(parseWatchlist('not json')).toEqual(empty)
    expect(parseWatchlist('[]')).toEqual(empty)
    expect(parseWatchlist('"a string"')).toEqual(empty)
    expect(parseWatchlist(JSON.stringify({ version: 99, series: {} }))).toEqual(empty)
  })
  it('repairs a half-written series record rather than dropping the store', () => {
    const got = parseWatchlist(
      JSON.stringify({
        version: WATCHLIST_VERSION,
        series: {
          good: { name: 'Good', watchedAt: TODAY, owned: ['a', 'a', 3], seen: null, hidden: true },
          broken: 'not an object',
          '': { name: 'No slug' },
        },
      })
    )
    expect(Object.keys(got.series)).toEqual(['good'])
    expect(got.series.good).toEqual({
      name: 'Good',
      watchedAt: TODAY,
      owned: ['a'],
      seen: [],
      skipped: [],
      hidden: true,
    })
  })
})

describe('importJSON', () => {
  it('adds a series the reader does not have', () => {
    const backup = setOwned(oneSeries('other', 'Other'), 'other', 'v1', true)
    const got = importJSON(emptyWatchlist(), exportJSON(backup))
    expect(watchedSeries(got, 'other')?.owned).toEqual(['v1'])
  })
  it('merges a series both sides hold, keeping the local name and unioning the marks', () => {
    const mine = markSeen(setOwned(oneSeries(), 'the-wandering-inn', 'v1', true), 'the-wandering-inn', ['v1'])
    const backup = setOwned(
      watch(emptyWatchlist(), 'the-wandering-inn', 'Stale Name', '2020-01-01'),
      'the-wandering-inn',
      'v2',
      true
    )
    const got = watchedSeries(importJSON(mine, exportJSON(backup)), 'the-wandering-inn')
    expect(got?.name).toBe('The Wandering Inn')
    expect(got?.watchedAt).toBe(TODAY)
    expect(got?.owned).toEqual(['v1', 'v2'])
    expect(got?.seen).toEqual(['v1'])
  })
  it('never un-marks a book: a stale backup only ever adds', () => {
    const mine = setOwned(oneSeries(), 'the-wandering-inn', 'v1', true)
    const stale = oneSeries() // watched, nothing owned
    expect(watchedSeries(importJSON(mine, exportJSON(stale)), 'the-wandering-inn')?.owned).toEqual([
      'v1',
    ])
  })
  it('hidden is sticky from either side', () => {
    const mine = oneSeries()
    const backup = hide(emptyWatchlist(), 'the-wandering-inn', 'The Wandering Inn', TODAY)
    expect(watchedSeries(importJSON(mine, exportJSON(backup)), 'the-wandering-inn')?.hidden).toBe(true)
    expect(
      watchedSeries(importJSON(backup, exportJSON(mine)), 'the-wandering-inn')?.hidden
    ).toBe(true)
  })
  it('merges nothing from unreadable input', () => {
    const mine = oneSeries()
    expect(importJSON(mine, 'not json')).toEqual(mine)
  })
})

describe('looksLikeWatchlist', () => {
  it('recognises a backup and rejects anything else', () => {
    expect(looksLikeWatchlist(exportJSON(oneSeries()))).toBe(true)
    expect(looksLikeWatchlist('not json')).toBe(false)
    expect(looksLikeWatchlist('{"format":"audiosilo-books","version":1,"books":[]}')).toBe(false)
  })

  // `typeof null === 'object'`, so a null (or array) `series` used to read as a
  // backup and the import control reported a successful merge of nothing.
  it('rejects a document whose series is not an object', () => {
    expect(looksLikeWatchlist('{"version":1,"series":null}')).toBe(false)
    expect(looksLikeWatchlist('{"version":1,"series":[]}')).toBe(false)
    expect(looksLikeWatchlist('[{"version":1,"series":{}}]')).toBe(false)
  })
})

describe('storage helpers', () => {
  it('round-trips under the namespaced key', () => {
    const map = stubStorage()
    const store = setOwned(oneSeries(), 'the-wandering-inn', 'v1', true)
    writeWatchlist(store)
    expect(map.get(WATCHLIST_STORAGE_KEY)).toBeDefined()
    expect(readWatchlist()).toEqual(store)
  })
  it('reads an empty watchlist when nothing is stored', () => {
    stubStorage()
    expect(readWatchlist()).toEqual(emptyWatchlist())
  })
  it('degrades when storage throws', () => {
    stubStorage({ throws: true })
    expect(readWatchlist()).toEqual(emptyWatchlist())
    expect(() => writeWatchlist(oneSeries())).not.toThrow()
  })
  it('degrades when there is no localStorage at all', () => {
    vi.stubGlobal('localStorage', undefined)
    expect(readWatchlist()).toEqual(emptyWatchlist())
    expect(() => writeWatchlist(oneSeries())).not.toThrow()
  })
})
