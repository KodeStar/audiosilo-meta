// Pure, framework-free shaping of the /api/v1/coverage response into the rows the
// contribute page renders. Kept out of the island so it can be unit-tested (no
// DOM, no React). The island is a thin renderer over these shapes.
import type {
  CoverageResponse,
  CoverageMissing,
  CoverageDimension,
  CoverageTotals,
  PersonRef,
} from './api'

/** Which build CTAs a missing row should offer. `recap_summary` is folded into
    the recaps CTA (the builder authors both together), so there is no separate
    summary CTA. */
export interface CoverageCtas {
  characters: boolean
  recaps: boolean
}

/** One work that is missing part of the expressive layer, ready to render. */
export interface CoverageWorkRow {
  id: string
  title: string
  authors: PersonRef[]
  position?: string
  ctas: CoverageCtas
}

/** A series' worth of works needing characters/recaps. */
export interface CoverageSeriesGroup {
  id: string
  name: string
  works: CoverageWorkRow[]
}

/** The grouped view of the missing rows: series groups first, then standalone
    works. Both preserve the server's ordering (series-name -> position ->
    standalone-by-title). */
export interface CoverageGrouped {
  series: CoverageSeriesGroup[]
  standalone: CoverageWorkRow[]
}

/** One stat in the coverage band. `known` is false when the count was omitted
    (an older artifact that cannot report it) - the UI renders that as "unknown"
    rather than 0. `percent` is null when unknown or when there are no works. */
export interface CoverageStat {
  key: CoverageDimension
  label: string
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

function toWorkRow(row: CoverageMissing): CoverageWorkRow {
  return {
    id: row.id,
    title: row.title,
    authors: row.authors ?? [],
    position: row.series?.position,
    ctas: ctasFor(row.missing ?? []),
  }
}

/** Group the missing rows into series buckets + a standalone bucket. Series
    appear in first-seen order and each series' works keep their incoming order,
    so the server's series-name -> position ordering carries through. A row with
    no series (or a series with no id) falls into standalone. */
export function groupMissing(rows: CoverageMissing[] | undefined): CoverageGrouped {
  const series: CoverageSeriesGroup[] = []
  const byId = new Map<string, CoverageSeriesGroup>()
  const standalone: CoverageWorkRow[] = []

  for (const row of rows ?? []) {
    const s = row.series
    if (s && s.id) {
      let group = byId.get(s.id)
      if (!group) {
        group = { id: s.id, name: s.name, works: [] }
        byId.set(s.id, group)
        series.push(group)
      }
      group.works.push(toWorkRow(row))
    } else {
      standalone.push(toWorkRow(row))
    }
  }

  return { series, standalone }
}

/** Percentage of works carrying a dimension, or null when the count is unknown
    (omitted) or there are no works. Rounded to a whole percent. */
export function coveragePercent(count: number | undefined, total: number): number | null {
  if (count === undefined || total <= 0) return null
  return Math.round((count / total) * 100)
}

/** The three coverage stats for the band, in a fixed order. A `known` flag
    distinguishes an omitted count (older artifact -> "unknown") from a real 0. */
export function coverageStats(totals: CoverageTotals): CoverageStat[] {
  const total = totals.works
  const rows: { key: CoverageDimension; label: string; count?: number }[] = [
    { key: 'characters', label: 'Works with characters', count: totals.with_characters },
    { key: 'recaps', label: 'Works with a story so far', count: totals.with_recaps },
    {
      key: 'recap_summary',
      label: 'Works with a recap summary',
      count: totals.with_recap_summary,
    },
  ]
  return rows.map((r) => ({
    key: r.key,
    label: r.label,
    known: r.count !== undefined,
    count: r.count,
    total,
    percent: coveragePercent(r.count, total),
  }))
}

/** True when there is nothing to show under "books needing characters and
    recaps" - the missing list was omitted (older artifact) or empty. */
export function hasMissingRows(coverage: CoverageResponse): boolean {
  return (coverage.missing?.length ?? 0) > 0
}
