import { describe, it, expect } from 'vitest'
import {
  ctasFor,
  groupMissing,
  coveragePercent,
  coverageStats,
  hasMissingRows,
} from './coverage'
import type { CoverageMissing, CoverageResponse, CoverageTotals } from './api'

function missing(extra: Partial<CoverageMissing> = {}): CoverageMissing {
  return {
    id: 'w1',
    title: 'A Work',
    authors: [{ id: 'p1', name: 'An Author' }],
    missing: ['characters', 'recaps'],
    ...extra,
  }
}

describe('ctasFor', () => {
  it('sets characters only when characters is missing', () => {
    expect(ctasFor(['characters'])).toEqual({ characters: true, recaps: false })
  })

  it('folds recap_summary into the recaps CTA', () => {
    expect(ctasFor(['recap_summary'])).toEqual({ characters: false, recaps: true })
    expect(ctasFor(['recaps'])).toEqual({ characters: false, recaps: true })
    expect(ctasFor(['recaps', 'recap_summary'])).toEqual({ characters: false, recaps: true })
  })

  it('handles both dimensions missing', () => {
    expect(ctasFor(['characters', 'recaps', 'recap_summary'])).toEqual({
      characters: true,
      recaps: true,
    })
  })

  it('handles an empty missing list', () => {
    expect(ctasFor([])).toEqual({ characters: false, recaps: false })
  })
})

describe('groupMissing', () => {
  it('returns empty buckets for undefined or empty input', () => {
    expect(groupMissing(undefined)).toEqual({ series: [], standalone: [] })
    expect(groupMissing([])).toEqual({ series: [], standalone: [] })
  })

  it('groups works by series preserving first-seen and within-group order', () => {
    const rows = [
      missing({ id: 'a', title: 'A', series: { id: 's1', name: 'Series One', position: '1' } }),
      missing({ id: 'b', title: 'B', series: { id: 's1', name: 'Series One', position: '2' } }),
      missing({ id: 'c', title: 'C', series: { id: 's2', name: 'Series Two', position: '1' } }),
    ]
    const grouped = groupMissing(rows)
    expect(grouped.series.map((g) => g.id)).toEqual(['s1', 's2'])
    expect(grouped.series[0].name).toBe('Series One')
    expect(grouped.series[0].works.map((w) => w.id)).toEqual(['a', 'b'])
    expect(grouped.series[0].works[0].position).toBe('1')
    expect(grouped.series[1].works.map((w) => w.id)).toEqual(['c'])
    expect(grouped.standalone).toEqual([])
  })

  it('puts series-less works (and series with no id) in the standalone bucket', () => {
    const rows = [
      missing({ id: 'a', title: 'A' }),
      missing({ id: 'b', title: 'B', series: null }),
    ]
    const grouped = groupMissing(rows)
    expect(grouped.series).toEqual([])
    expect(grouped.standalone.map((w) => w.id)).toEqual(['a', 'b'])
  })

  it('carries CTA flags onto each work row', () => {
    const rows = [missing({ id: 'a', missing: ['characters'] })]
    const grouped = groupMissing(rows)
    expect(grouped.standalone[0].ctas).toEqual({ characters: true, recaps: false })
  })
})

describe('coveragePercent', () => {
  it('rounds a fraction to a whole percent', () => {
    expect(coveragePercent(1, 3)).toBe(33)
    expect(coveragePercent(2, 3)).toBe(67)
    expect(coveragePercent(5, 40)).toBe(13)
  })

  it('returns null when the count is unknown (omitted)', () => {
    expect(coveragePercent(undefined, 40)).toBeNull()
  })

  it('returns null when there are no works', () => {
    expect(coveragePercent(0, 0)).toBeNull()
  })

  it('treats a real 0 as 0 percent, not unknown', () => {
    expect(coveragePercent(0, 40)).toBe(0)
  })
})

describe('coverageStats', () => {
  const totals: CoverageTotals = {
    works: 40,
    with_characters: 8,
    with_recaps: 6,
    with_recap_summary: 5,
  }

  it('returns the three stats in a fixed order with percentages', () => {
    const stats = coverageStats(totals)
    expect(stats.map((s) => s.key)).toEqual(['characters', 'recaps', 'recap_summary'])
    expect(stats[0]).toMatchObject({ known: true, count: 8, total: 40, percent: 20 })
    expect(stats[1].percent).toBe(15)
    expect(stats[2].percent).toBe(13)
  })

  it('marks an omitted count as unknown with a null percent', () => {
    const stats = coverageStats({ works: 40 })
    for (const s of stats) {
      expect(s.known).toBe(false)
      expect(s.count).toBeUndefined()
      expect(s.percent).toBeNull()
    }
  })

  it('keeps a real 0 known (distinct from omitted)', () => {
    const stats = coverageStats({ works: 40, with_characters: 0 })
    expect(stats[0]).toMatchObject({ known: true, count: 0, percent: 0 })
    expect(stats[1].known).toBe(false)
  })
})

describe('hasMissingRows', () => {
  const base: CoverageResponse = { totals: { works: 1 }, series_gaps: [] }

  it('is false when missing is omitted or empty', () => {
    expect(hasMissingRows(base)).toBe(false)
    expect(hasMissingRows({ ...base, missing: [] })).toBe(false)
  })

  it('is true when there is at least one missing row', () => {
    expect(hasMissingRows({ ...base, missing: [missing()] })).toBe(true)
  })
})
