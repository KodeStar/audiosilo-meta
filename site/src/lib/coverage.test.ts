import { describe, it, expect } from 'vitest'
import {
  ctasFor,
  toWorkRow,
  coveragePercent,
  coverageStats,
  filterForStat,
  pageInfo,
  COVERAGE_FILTERS,
} from './coverage'
import type { CoverageWork, CoverageTotals } from './api'

function work(extra: Partial<CoverageWork> = {}): CoverageWork {
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

  it('handles an empty missing list (a fully-covered "has" row)', () => {
    expect(ctasFor([])).toEqual({ characters: false, recaps: false })
  })
})

describe('toWorkRow', () => {
  it('carries CTA flags, series and position onto the row', () => {
    const row = toWorkRow(
      work({ id: 'a', missing: ['characters'], series: { id: 's1', name: 'S1', position: '2' } })
    )
    expect(row.ctas).toEqual({ characters: true, recaps: false })
    expect(row.series?.id).toBe('s1')
    expect(row.position).toBe('2')
  })

  it('defaults authors and series when absent', () => {
    const row = toWorkRow({ id: 'a', title: 'A', authors: [], missing: [] })
    expect(row.authors).toEqual([])
    expect(row.series).toBeNull()
    expect(row.position).toBeUndefined()
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

  it('returns the three stats in a fixed order with percentages and hints', () => {
    const stats = coverageStats(totals)
    expect(stats.map((s) => s.key)).toEqual(['characters', 'recaps', 'recap_summary'])
    expect(stats[0]).toMatchObject({ known: true, count: 8, total: 40, percent: 20 })
    expect(stats[1].percent).toBe(15)
    expect(stats[2].percent).toBe(13)
    for (const s of stats) expect(s.hint.length).toBeGreaterThan(0)
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

describe('filterForStat', () => {
  it('maps a stat dimension to its "has X" browser filter', () => {
    expect(filterForStat('characters')).toBe('has_characters')
    expect(filterForStat('recaps')).toBe('has_recaps')
    expect(filterForStat('recap_summary')).toBe('has_recap_summary')
  })

  it('every stat filter is a known browser filter', () => {
    const keys = new Set(COVERAGE_FILTERS.map((f) => f.key))
    for (const dim of ['characters', 'recaps', 'recap_summary'] as const) {
      expect(keys.has(filterForStat(dim))).toBe(true)
    }
    // "Needs work" (missing) is the default first tab.
    expect(COVERAGE_FILTERS[0].key).toBe('missing')
  })
})

describe('pageInfo', () => {
  it('describes a full first page', () => {
    expect(pageInfo(100, 0, 25)).toEqual({
      from: 1,
      to: 25,
      total: 100,
      page: 1,
      pageCount: 4,
      hasPrev: false,
      hasNext: true,
    })
  })

  it('describes a middle page', () => {
    const p = pageInfo(100, 50, 25)
    expect(p).toMatchObject({ from: 51, to: 75, page: 3, hasPrev: true, hasNext: true })
  })

  it('describes a short final page', () => {
    const p = pageInfo(60, 50, 25)
    expect(p).toMatchObject({ from: 51, to: 60, page: 3, pageCount: 3, hasNext: false })
  })

  it('handles an empty result', () => {
    expect(pageInfo(0, 0, 25)).toMatchObject({
      from: 0,
      to: 0,
      page: 1,
      pageCount: 1,
      hasPrev: false,
      hasNext: false,
    })
  })

  it('clamps an offset past the end without inventing rows', () => {
    const p = pageInfo(10, 999, 25)
    expect(p.to).toBe(10)
    expect(p.hasNext).toBe(false)
  })
})
