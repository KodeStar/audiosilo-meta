import { describe, it, expect } from 'vitest'
import type { SeriesEntry } from './api'
import {
  compareAvailable,
  comparePreorder,
  flattenAcrossSeries,
  positionStart,
  type FlatEntry,
} from './watch-flat'
import type { SeriesClassification } from './watchlist'

function entry(id: string, position: string, release_date?: string): SeriesEntry {
  return { position, work: { id, title: id, authors: [], release_date } }
}

function panel(
  slug: string,
  name: string,
  parts: Partial<SeriesClassification>
): { slug: string; name: string; result: SeriesClassification } {
  return {
    slug,
    name,
    result: { available: [], preorder: [], owned: [], skipped: [], ...parts },
  }
}

function flat(
  series: string,
  id: string,
  position: string,
  release_date?: string
): FlatEntry {
  return { slug: series, series, entry: entry(id, position, release_date), isNew: false }
}

describe('flattenAcrossSeries', () => {
  it('yields two empty lists for no panels', () => {
    expect(flattenAcrossSeries([])).toEqual({ preorder: [], available: [] })
  })

  it('pools preorders across series, soonest first', () => {
    const lists = flattenAcrossSeries([
      panel('a', 'Alpha', {
        preorder: [{ entry: entry('a2', '2', '2026-12-01'), isNew: true }],
      }),
      panel('b', 'Beta', {
        preorder: [
          { entry: entry('b1', '1', '2026-10-05'), isNew: false },
          { entry: entry('b2', '2', '2027-01-01'), isNew: false },
        ],
      }),
    ])
    expect(lists.preorder.map((f) => f.entry.work.id)).toEqual(['b1', 'a2', 'b2'])
    expect(lists.available).toEqual([])
  })

  it('pools available entries across series, newest first', () => {
    const lists = flattenAcrossSeries([
      panel('a', 'Alpha', {
        available: [{ entry: entry('a1', '1', '2020-01-01'), isNew: true }],
      }),
      panel('b', 'Beta', {
        available: [{ entry: entry('b1', '1', '2026-09-01'), isNew: false }],
      }),
    ])
    expect(lists.available.map((f) => f.entry.work.id)).toEqual(['b1', 'a1'])
  })

  it('carries the slug, series name and isNew through', () => {
    const lists = flattenAcrossSeries([
      panel('the-slug', 'The Name', {
        available: [{ entry: entry('w1', '1', '2020-01-01'), isNew: true }],
      }),
    ])
    expect(lists.available[0]).toEqual({
      slug: 'the-slug',
      series: 'The Name',
      entry: entry('w1', '1', '2020-01-01'),
      isNew: true,
    })
  })

  it('falls back to the slug when the stored series name is empty', () => {
    const lists = flattenAcrossSeries([
      panel('only-slug', '', { available: [{ entry: entry('w1', '1'), isNew: false }] }),
    ])
    expect(lists.available[0].series).toBe('only-slug')
  })

  it('leaves owned and skipped entries out of both lists', () => {
    const lists = flattenAcrossSeries([
      panel('a', 'Alpha', {
        owned: [entry('o1', '1', '2026-09-01')],
        skipped: [entry('s1', '2', '2026-09-02')],
        available: [{ entry: entry('a1', '3', '2026-09-03'), isNew: false }],
      }),
    ])
    expect(lists.available.map((f) => f.entry.work.id)).toEqual(['a1'])
    expect(lists.preorder).toEqual([])
  })
})

describe('comparePreorder', () => {
  it('orders by date ascending', () => {
    const list = [
      flat('X', 'late', '1', '2027-01-01'),
      flat('X', 'soon', '2', '2026-10-05'),
    ].sort(comparePreorder)
    expect(list.map((f) => f.entry.work.id)).toEqual(['soon', 'late'])
  })

  it('compares mixed precision as plain strings, which is chronological', () => {
    // "2026-10" states a month and nothing more; string order puts it before
    // every day in that month, which is the same rule the server picks a card's
    // date by. Nothing is parsed, so no timezone touches a date that has none.
    const list = [
      flat('X', 'day', '2', '2026-10-05'),
      flat('X', 'month', '1', '2026-10'),
      flat('X', 'year', '3', '2026'),
    ].sort(comparePreorder)
    expect(list.map((f) => f.entry.work.id)).toEqual(['year', 'month', 'day'])
  })

  it('breaks a same-date tie by series name, then position', () => {
    const list = [
      flat('Beta', 'b1', '1', '2026-10-05'),
      flat('Alpha', 'a2', '2', '2026-10-05'),
      flat('Alpha', 'a1', '1', '2026-10-05'),
    ].sort(comparePreorder)
    expect(list.map((f) => f.entry.work.id)).toEqual(['a1', 'a2', 'b1'])
  })
})

describe('compareAvailable', () => {
  it('orders by date descending', () => {
    const list = [
      flat('X', 'old', '1', '2019-01-01'),
      flat('X', 'new', '2', '2026-09-01'),
      flat('X', 'mid', '3', '2023-05-05'),
    ].sort(compareAvailable)
    expect(list.map((f) => f.entry.work.id)).toEqual(['new', 'mid', 'old'])
  })

  it('puts every undated entry after every dated one, ordered by series then position', () => {
    const list = [
      flat('Beta', 'undated-b', '1'),
      flat('Alpha', 'undated-a2', '2'),
      flat('Alpha', 'undated-a1', '1'),
      flat('Zulu', 'dated', '1', '1998-01-01'),
    ].sort(compareAvailable)
    expect(list.map((f) => f.entry.work.id)).toEqual([
      'dated',
      'undated-a1',
      'undated-a2',
      'undated-b',
    ])
  })
})

describe('positionStart', () => {
  // The twin of Go positionStart in internal/serve/queries.go.
  it('reads a decimal, a range and a padded number', () => {
    expect(positionStart('2.5')).toBe(2.5)
    expect(positionStart('1-3')).toBe(1)
    expect(positionStart('1-3.5')).toBe(1)
    expect(positionStart('03')).toBe(3)
    expect(positionStart(' 4 ')).toBe(4)
    expect(positionStart('1-garbage')).toBe(1)
  })
  it('sorts an unparseable position last', () => {
    expect(positionStart('bonus')).toBe(Number.POSITIVE_INFINITY)
    expect(positionStart('')).toBe(Number.POSITIVE_INFINITY)
  })
  it('sorts 2.5 between 2 and 3, and a range at its start', () => {
    const list = [
      flat('X', 'three', '3', '2020-01-01'),
      flat('X', 'range', '1-3', '2020-01-01'),
      flat('X', 'novella', '2.5', '2020-01-01'),
      flat('X', 'two', '2', '2020-01-01'),
    ].sort(comparePreorder)
    expect(list.map((f) => f.entry.work.id)).toEqual(['range', 'two', 'novella', 'three'])
  })
  it('keeps a stable order for two unparseable positions', () => {
    // Both are +Infinity, and the difference of those is NaN - not an ordering.
    const list = [
      flat('X', 'b', 'bonus-b', '2020-01-01'),
      flat('X', 'a', 'bonus-a', '2020-01-01'),
    ].sort(comparePreorder)
    expect(list.map((f) => f.entry.work.id)).toEqual(['a', 'b'])
  })
})
