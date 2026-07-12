import { useEffect, useState } from 'react'
import { getCoverage, href, type CoverageResponse } from '../../lib/api'
import {
  coverageStats,
  groupMissing,
  hasMissingRows,
  type CoverageStat,
  type CoverageWorkRow,
  type CoverageSeriesGroup,
} from '../../lib/coverage'

// The guided builder (agent S2's page) and the add-work issue form. The builder
// links carry the work id and the dimension being authored; the add-work link is
// unprefilled (the missing titles in a gap are unknown).
const BUILD_BASE = '/build'
const ADD_WORK_ISSUE =
  'https://github.com/kodestar/audiosilo-meta/issues/new?template=add-work.yml'

function buildUrl(workId: string, kind: 'characters' | 'recaps'): string {
  const params = new URLSearchParams({ work: workId, kind })
  return `${BUILD_BASE}?${params.toString()}`
}

function authorNames(authors: { name: string }[]): string {
  return authors.map((a) => a.name).join(', ')
}

type LoadState =
  | { status: 'loading' }
  | { status: 'error' }
  | { status: 'ready'; data: CoverageResponse }

// --- Stats band -----------------------------------------------------------

function StatCard({ stat }: { stat: CoverageStat }) {
  return (
    <div className="rounded-2xl border border-edge bg-surface p-6 text-center">
      <div className="text-4xl font-bold text-pink-400">
        {stat.known ? stat.count?.toLocaleString() : 'unknown'}
      </div>
      <div className="mt-2 text-sm text-dim">{stat.label}</div>
      <div className="mt-1 text-xs text-dim">
        {stat.known && stat.percent !== null
          ? `${stat.percent}% of ${stat.total.toLocaleString()} works`
          : `of ${stat.total.toLocaleString()} works`}
      </div>
    </div>
  )
}

function StatsBand({ data }: { data: CoverageResponse }) {
  const stats = coverageStats(data.totals)
  return (
    <div className="grid gap-4 sm:grid-cols-3">
      {stats.map((s) => (
        <StatCard key={s.key} stat={s} />
      ))}
    </div>
  )
}

// --- Books needing characters and recaps ----------------------------------

function BuildLinks({ row }: { row: CoverageWorkRow }) {
  return (
    <div className="flex shrink-0 flex-wrap items-center gap-2">
      {row.ctas.characters ? (
        <a
          href={buildUrl(row.id, 'characters')}
          className="inline-flex items-center rounded-lg border border-edge px-3 py-1.5 text-sm font-medium text-hi transition-colors hover:border-pink-500 hover:text-pink-300 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-pink-500"
        >
          Add characters
        </a>
      ) : null}
      {row.ctas.recaps ? (
        <a
          href={buildUrl(row.id, 'recaps')}
          className="inline-flex items-center rounded-lg border border-edge px-3 py-1.5 text-sm font-medium text-hi transition-colors hover:border-pink-500 hover:text-pink-300 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-pink-500"
        >
          Add recaps
        </a>
      ) : null}
    </div>
  )
}

function WorkRow({ row }: { row: CoverageWorkRow }) {
  return (
    <li className="flex flex-col gap-3 rounded-xl border border-edge bg-raised p-4 sm:flex-row sm:items-center sm:justify-between">
      <div className="min-w-0">
        <a
          href={href.work(row.id)}
          className="font-medium text-hi underline-offset-2 transition-colors hover:text-pink-300 hover:underline"
        >
          {row.position ? (
            <span className="mr-1.5 font-semibold text-pink-400">#{row.position}</span>
          ) : null}
          {row.title}
        </a>
        {row.authors.length > 0 ? (
          <p className="mt-1 text-sm text-dim">{authorNames(row.authors)}</p>
        ) : null}
      </div>
      <BuildLinks row={row} />
    </li>
  )
}

function SeriesGroup({ group }: { group: CoverageSeriesGroup }) {
  return (
    <div>
      <h3 className="text-sm font-semibold uppercase tracking-wider text-dim">
        <a
          href={href.series(group.id)}
          className="text-body underline-offset-2 transition-colors hover:text-pink-300 hover:underline"
        >
          {group.name}
        </a>
      </h3>
      <ul className="mt-3 space-y-3">
        {group.works.map((w) => (
          <WorkRow key={w.id} row={w} />
        ))}
      </ul>
    </div>
  )
}

function MissingSection({ data }: { data: CoverageResponse }) {
  if (!hasMissingRows(data)) {
    return (
      <section>
        <h2 className="text-xl font-semibold text-hi">Books needing characters and recaps</h2>
        <p className="mt-4 rounded-2xl border border-edge bg-surface p-6 text-sm leading-relaxed text-body">
          This list is not available from the current database build. Pick any work from search
          and use its page to start adding characters or a story-so-far recap.
        </p>
      </section>
    )
  }

  const grouped = groupMissing(data.missing)
  return (
    <section>
      <h2 className="text-xl font-semibold text-hi">Books needing characters and recaps</h2>
      <p className="mt-3 max-w-2xl text-sm leading-relaxed text-dim">
        These works are in the catalogue but have no community character cards or story-so-far
        recaps yet. Open a work to see what is there, or jump straight into the guided builder.
      </p>
      <div className="mt-8 space-y-10">
        {grouped.series.map((group) => (
          <SeriesGroup key={group.id} group={group} />
        ))}
        {grouped.standalone.length > 0 ? (
          <div>
            <h3 className="text-sm font-semibold uppercase tracking-wider text-dim">
              Standalone works
            </h3>
            <ul className="mt-3 space-y-3">
              {grouped.standalone.map((w) => (
                <WorkRow key={w.id} row={w} />
              ))}
            </ul>
          </div>
        ) : null}
      </div>
    </section>
  )
}

// --- Series with missing books --------------------------------------------

function SeriesGapsSection({ data }: { data: CoverageResponse }) {
  const gaps = data.series_gaps ?? []
  if (gaps.length === 0) return null
  return (
    <section>
      <h2 className="text-xl font-semibold text-hi">Series with missing books</h2>
      <p className="mt-3 max-w-2xl text-sm leading-relaxed text-dim">
        These series have gaps in their numbered volumes. If you own a missing book, add it
        through the issue form.
      </p>
      <ul className="mt-6 space-y-3">
        {gaps.map((gap) => (
          <li
            key={gap.id}
            className="flex flex-col gap-3 rounded-xl border border-edge bg-surface p-4 sm:flex-row sm:items-center sm:justify-between"
          >
            <div className="min-w-0">
              <a
                href={href.series(gap.id)}
                className="font-medium text-hi underline-offset-2 transition-colors hover:text-pink-300 hover:underline"
              >
                {gap.name}
              </a>
              <p className="mt-1 text-sm text-dim">
                <span className="text-body">Have:</span>{' '}
                {gap.present.length > 0 ? gap.present.join(', ') : 'none'}
                {gap.missing_positions.length > 0 ? (
                  <>
                    {' '}
                    <span className="text-body">Missing:</span>{' '}
                    <span className="text-pink-300">{gap.missing_positions.join(', ')}</span>
                  </>
                ) : null}
              </p>
            </div>
            <a
              href={ADD_WORK_ISSUE}
              target="_blank"
              rel="noopener"
              className="inline-flex shrink-0 items-center rounded-lg border border-edge px-3 py-1.5 text-sm font-medium text-hi transition-colors hover:border-pink-500 hover:text-pink-300 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-pink-500"
            >
              Add a book
            </a>
          </li>
        ))}
      </ul>
    </section>
  )
}

// --- The island -----------------------------------------------------------

export default function CoveragePanel() {
  const [state, setState] = useState<LoadState>({ status: 'loading' })

  useEffect(() => {
    const ctrl = new AbortController()
    getCoverage(ctrl.signal)
      .then((data) => setState({ status: 'ready', data }))
      .catch((err) => {
        if (ctrl.signal.aborted || (err as Error).name === 'AbortError') return
        setState({ status: 'error' })
      })
    return () => ctrl.abort()
  }, [])

  if (state.status === 'loading') {
    return (
      <div className="py-16 text-center" aria-live="polite" aria-busy="true">
        <div className="mx-auto h-8 w-8 animate-spin rounded-full border-2 border-edge border-t-pink-500"></div>
        <p className="mt-4 text-sm text-dim">Loading what needs work...</p>
      </div>
    )
  }

  if (state.status === 'error') {
    return (
      <div className="rounded-2xl border border-edge bg-surface p-6 text-center">
        <p className="text-sm leading-relaxed text-body">
          The coverage data could not be loaded right now. You can still contribute from any
          work page, or open an issue on GitHub.
        </p>
      </div>
    )
  }

  return (
    <div className="space-y-14">
      <StatsBand data={state.data} />
      <MissingSection data={state.data} />
      <SeriesGapsSection data={state.data} />
    </div>
  )
}
