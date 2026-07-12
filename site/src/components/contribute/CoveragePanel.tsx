import { useMemo, useState } from 'react'
import { getCoverage, href, personNames, type CoverageResponse } from '../../lib/api'
import {
  coverageStats,
  groupMissing,
  missingState,
  limitGrouped,
  type CoverageStat,
  type CoverageWorkRow,
  type CoverageSeriesGroup,
} from '../../lib/coverage'
import { addWorkIssueFormUrl } from '../../lib/github-prefill'
import { PILL_LINK } from '../ui'
import { useEntity, DetailSpinner } from '../detail/detail-common'

// Initial-render caps for the missing list: the full data is already loaded,
// this only bounds the first paint (the day-one state is ~800 rows) - a
// "show all" expander renders the rest on demand.
const MISSING_LIMITS = { maxSeries: 8, maxStandalone: 30 }

// Coverage is a singleton (no id); a fixed key satisfies the shared loader.
// Module-scope so its identity is stable across renders (useEntity depends on it).
const fetchCoverage = (_id: string, signal: AbortSignal) => getCoverage(signal)

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
        <a href={href.build(row.id, 'characters')} className={`${PILL_LINK} px-3 py-1.5`}>
          Add characters
        </a>
      ) : null}
      {row.ctas.recaps ? (
        <a href={href.build(row.id, 'recaps')} className={`${PILL_LINK} px-3 py-1.5`}>
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
          <p className="mt-1 text-sm text-dim">{personNames(row.authors)}</p>
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
  const [showAll, setShowAll] = useState(false)
  // Grouping is O(rows) and must not re-run when the Show-all toggle re-renders
  // this section - memoized on the rows themselves (safe on undefined).
  const grouped = useMemo(() => groupMissing(data.missing), [data.missing])
  const limited = useMemo(() => limitGrouped(grouped, MISSING_LIMITS), [grouped])

  const state = missingState(data)
  if (state !== 'has-rows') {
    return (
      <section>
        <h2 className="text-xl font-semibold text-hi">Books needing characters and recaps</h2>
        <p className="mt-4 rounded-2xl border border-edge bg-surface p-6 text-sm leading-relaxed text-body">
          {state === 'unavailable'
            ? 'This list is not available from the current database build. Pick any work from search and use its page to start adding characters or a story-so-far recap.'
            : 'All catalogued books have characters and recaps - nothing needs work right now. New books will appear here as they are added.'}
        </p>
      </section>
    )
  }

  const view = showAll ? grouped : limited
  const hiddenWorks = showAll ? 0 : limited.hiddenWorks
  const totalWorks = data.missing?.length ?? 0

  return (
    <section>
      <h2 className="text-xl font-semibold text-hi">Books needing characters and recaps</h2>
      <p className="mt-3 max-w-2xl text-sm leading-relaxed text-dim">
        These works are in the catalogue but have no community character cards or story-so-far
        recaps yet. Open a work to see what is there, or jump straight into the guided builder.
      </p>
      <div className="mt-8 space-y-10">
        {view.series.map((group) => (
          <SeriesGroup key={group.id} group={group} />
        ))}
        {view.standalone.length > 0 ? (
          <div>
            <h3 className="text-sm font-semibold uppercase tracking-wider text-dim">
              Standalone works
            </h3>
            <ul className="mt-3 space-y-3">
              {view.standalone.map((w) => (
                <WorkRow key={w.id} row={w} />
              ))}
            </ul>
          </div>
        ) : null}
      </div>
      {hiddenWorks > 0 ? (
        <button
          type="button"
          onClick={() => setShowAll(true)}
          className={`${PILL_LINK} mt-8 px-4 py-2`}
        >
          Show all {totalWorks.toLocaleString()} books
        </button>
      ) : null}
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
              href={addWorkIssueFormUrl}
              target="_blank"
              rel="noopener"
              className={`${PILL_LINK} shrink-0 px-3 py-1.5`}
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
  const state = useEntity<CoverageResponse>('coverage', fetchCoverage)

  if (state.status === 'loading') {
    return <DetailSpinner label="Loading what needs work..." className="py-16 text-center" />
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
