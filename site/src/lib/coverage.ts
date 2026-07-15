// Pure, framework-free shaping for the contribute page's coverage browser. Kept
// out of the island so it can be unit-tested (no DOM, no React). The island is a
// thin renderer over these shapes; the server does the filtering/search/paging.
import type {
  CoverageWork,
  CoverageDimension,
  CoverageTotals,
  CoverageFilter,
} from './api'

/** Which build CTAs a work row should offer. `recap_summary` is folded into the
    recaps CTA (the builder authors both together), so there is no separate
    summary CTA. */
export interface CoverageCtas {
  characters: boolean
  recaps: boolean
}

/** One work row ready to render (the row link + optional build CTAs). */
export interface CoverageWorkRow {
  id: string
  title: string
  authors: CoverageWork['authors']
  series?: CoverageWork['series']
  position?: string
  ctas: CoverageCtas
}

/** One stat in the coverage band. `known` is false when the count was omitted
    (an older artifact that cannot report it) - the UI renders that as "unknown"
    rather than 0. `percent` is null when unknown or when there are no works. */
export interface CoverageStat {
  key: CoverageDimension
  label: string
  hint: string
  known: boolean
  count?: number
  total: number
  percent: number | null
}

/** Map a row's missing dimensions to the two CTAs the builder exposes. */
export function ctasFor(missing: CoverageDimension[]): CoverageCtas {
  return {
    characters: missing.includes('characters'),
    recaps: missing.includes('recaps') || missing.includes('recap_summary'),
  }
}

/** Shape a wire work row into a render row (series ref carried through so each
    row can show its series inline, since the flat list is no longer grouped). */
export function toWorkRow(row: CoverageWork): CoverageWorkRow {
  return {
    id: row.id,
    title: row.title,
    authors: row.authors ?? [],
    series: row.series ?? null,
    position: row.series?.position,
    ctas: ctasFor(row.missing ?? []),
  }
}

/** Percentage of works carrying a dimension, or null when the count is unknown
    (omitted) or there are no works. Rounded to a whole percent. */
export function coveragePercent(count: number | undefined, total: number): number | null {
  if (count === undefined || total <= 0) return null
  return Math.round((count / total) * 100)
}

/** The three coverage stats for the band, in a fixed order, each with a short
    hint clarifying what the dimension is (story-so-far vs whole-book summary).
    A `known` flag distinguishes an omitted count (older artifact -> "unknown")
    from a real 0. */
export function coverageStats(totals: CoverageTotals): CoverageStat[] {
  const total = totals.works
  const rows: { key: CoverageDimension; label: string; hint: string; count?: number }[] = [
    {
      key: 'characters',
      label: 'Works with characters',
      hint: 'Spoiler-aware character guides',
      count: totals.with_characters,
    },
    {
      key: 'recaps',
      label: 'Works with a story so far',
      hint: 'Chapter-by-chapter recaps for resuming mid-book',
      count: totals.with_recaps,
    },
    {
      key: 'recap_summary',
      label: 'Works with a recap summary',
      hint: 'One whole-book wrap-up for when you have finished',
      count: totals.with_recap_summary,
    },
  ]
  return rows.map((r) => ({
    key: r.key,
    label: r.label,
    hint: r.hint,
    known: r.count !== undefined,
    count: r.count,
    total,
    percent: coveragePercent(r.count, total),
  }))
}

/** The coverage-browser filters, in tab order. The first ("Needs work") is the
    default; the other three mirror the three stat cards ("has X"). */
export const COVERAGE_FILTERS: { key: CoverageFilter; label: string }[] = [
  { key: 'missing', label: 'Needs work' },
  { key: 'has_characters', label: 'Has characters' },
  { key: 'has_recaps', label: 'Has story so far' },
  { key: 'has_recap_summary', label: 'Has recap summary' },
]

/** The browser filter that shows works which HAVE the given stat's dimension -
    what a stat card links to. */
export function filterForStat(dim: CoverageDimension): CoverageFilter {
  return `has_${dim}` as CoverageFilter
}

/** Derived pager state for a "showing from-to of total" control. `from`/`to`
    are 1-based inclusive and 0/0 for an empty result. `page`/`pageCount` are
    1-based; `hasPrev`/`hasNext` gate the buttons. */
export interface PageInfo {
  from: number
  to: number
  total: number
  page: number
  pageCount: number
  hasPrev: boolean
  hasNext: boolean
}

export function pageInfo(total: number, offset: number, limit: number): PageInfo {
  const safeLimit = limit > 0 ? limit : 1
  if (total <= 0) {
    return { from: 0, to: 0, total: 0, page: 1, pageCount: 1, hasPrev: false, hasNext: false }
  }
  const clampedOffset = Math.min(Math.max(offset, 0), Math.max(total - 1, 0))
  const from = clampedOffset + 1
  const to = Math.min(clampedOffset + safeLimit, total)
  const pageCount = Math.ceil(total / safeLimit)
  const page = Math.floor(clampedOffset / safeLimit) + 1
  return {
    from,
    to,
    total,
    page,
    pageCount,
    hasPrev: clampedOffset > 0,
    hasNext: to < total,
  }
}
