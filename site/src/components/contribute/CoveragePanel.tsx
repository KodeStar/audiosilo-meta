import { useEffect, useRef, useState } from 'react'
import {
  getCoverage,
  getCoverageWorks,
  getSeriesGaps,
  href,
  personNames,
  type CoverageFilter,
  type CoverageResponse,
  type CoverageWork,
  type CoverageWorksResponse,
  type CoverageSeriesGap,
  type SeriesGapsResponse,
} from '../../lib/api'
import {
  coverageStats,
  toWorkRow,
  filterForStat,
  pageInfo,
  COVERAGE_FILTERS,
  type CoverageStat,
  type CoverageWorkRow,
} from '../../lib/coverage'
import { addWorkIssueFormUrl } from '../../lib/github-prefill'
import { PILL_LINK } from '../ui'
import { useEntity, DetailSpinner } from '../detail/detail-common'

// One page of the coverage/gap browsers. The full data lives server-side; each
// page is a bounded request, so the payload never grows with the catalogue.
const PAGE_SIZE = 20

// The pager buttons are the shared pill (border/hover/focus) plus disabled
// styling; only the padding/gap and disabled states are pager-specific.
const PAGER_BTN = `${PILL_LINK} gap-1 px-3 py-1.5 disabled:cursor-not-allowed disabled:opacity-40 disabled:hover:border-edge disabled:hover:text-hi`

// --- small async + debounce hooks -----------------------------------------

type Async<T> = { status: 'loading' } | { status: 'error' } | { status: 'ready'; data: T }

/** Run an abortable fetch, re-running whenever `deps` change (the previous
    request is aborted). `deps` are the fetch inputs, so the closed-over fetcher
    always reads current values. */
function useAsync<T>(fetcher: (signal: AbortSignal) => Promise<T>, deps: unknown[]): Async<T> {
  const [state, setState] = useState<Async<T>>({ status: 'loading' })
  useEffect(() => {
    const ctrl = new AbortController()
    setState({ status: 'loading' })
    fetcher(ctrl.signal)
      .then((data) => {
        if (!ctrl.signal.aborted) setState({ status: 'ready', data })
      })
      .catch((err) => {
        if (ctrl.signal.aborted || (err as Error).name === 'AbortError') return
        setState({ status: 'error' })
      })
    return () => ctrl.abort()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps)
  return state
}

/** Debounce a rapidly-changing value (the search box) so keystrokes don't each
    fire a request. */
function useDebounced<T>(value: T, ms = 250): T {
  const [debounced, setDebounced] = useState(value)
  useEffect(() => {
    const t = setTimeout(() => setDebounced(value), ms)
    return () => clearTimeout(t)
  }, [value, ms])
  return debounced
}

/** Search + pagination state shared by the two browsers: a debounced query and
    a page offset that resets to 0 whenever the query (or an external `resetKey`
    such as the active filter) changes. The reset happens DURING render via a
    stored-previous-key compare, not in an effect - so the fetching child never
    renders with a stale offset, avoiding a wasted aborted request on every
    filter/search change past page 1. */
function usePagedSearch(resetKey = '') {
  const [rawQuery, setRawQuery] = useState('')
  const query = useDebounced(rawQuery.trim())
  const [offset, setOffset] = useState(0)
  const key = `${resetKey} ${query}`
  const [prevKey, setPrevKey] = useState(key)
  if (key !== prevKey) {
    setPrevKey(key)
    setOffset(0)
  }
  return { rawQuery, setRawQuery, query, offset, setOffset }
}

// --- shared bits ----------------------------------------------------------

function SearchBox({
  value,
  onChange,
  placeholder,
  label,
}: {
  value: string
  onChange: (v: string) => void
  placeholder: string
  label: string
}) {
  return (
    <div className="relative">
      <span className="pointer-events-none absolute inset-y-0 left-3 flex items-center text-dim">
        <svg
          className="h-4 w-4"
          xmlns="http://www.w3.org/2000/svg"
          fill="none"
          viewBox="0 0 24 24"
          strokeWidth={1.5}
          stroke="currentColor"
          aria-hidden="true"
        >
          <path
            strokeLinecap="round"
            strokeLinejoin="round"
            d="m21 21-5.197-5.197m0 0A7.5 7.5 0 1 0 5.196 5.196a7.5 7.5 0 0 0 10.607 10.607Z"
          />
        </svg>
      </span>
      <input
        type="search"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        aria-label={label}
        className="w-full rounded-xl border border-edge bg-surface py-2.5 pl-10 pr-3 text-sm text-hi placeholder:text-dim focus:border-pink-500 focus:outline-none"
      />
    </div>
  )
}

function Pager({
  total,
  offset,
  onOffset,
}: {
  total: number
  offset: number
  onOffset: (o: number) => void
}) {
  const info = pageInfo(total, offset, PAGE_SIZE)
  if (info.total <= PAGE_SIZE) return null
  return (
    <div className="mt-6 flex flex-wrap items-center justify-between gap-3">
      <p className="text-sm text-dim">
        Showing {info.from.toLocaleString()}-{info.to.toLocaleString()} of{' '}
        {info.total.toLocaleString()}
      </p>
      <div className="flex items-center gap-2">
        <button
          type="button"
          className={PAGER_BTN}
          disabled={!info.hasPrev}
          onClick={() => onOffset(Math.max(0, offset - PAGE_SIZE))}
        >
          Previous
        </button>
        <span className="text-sm text-dim">
          Page {info.page.toLocaleString()} of {info.pageCount.toLocaleString()}
        </span>
        <button
          type="button"
          className={PAGER_BTN}
          disabled={!info.hasNext}
          onClick={() => onOffset(offset + PAGE_SIZE)}
        >
          Next
        </button>
      </div>
    </div>
  )
}

function ListMessage({ children }: { children: React.ReactNode }) {
  return (
    <p className="rounded-2xl border border-edge bg-surface p-6 text-sm leading-relaxed text-body">
      {children}
    </p>
  )
}

// --- Stats band -----------------------------------------------------------

function StatCard({
  stat,
  active,
  onPick,
}: {
  stat: CoverageStat
  active: boolean
  onPick: () => void
}) {
  return (
    <button
      type="button"
      onClick={onPick}
      aria-pressed={active}
      className={`rounded-2xl border bg-surface p-6 text-center transition-colors focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-pink-500 ${
        active ? 'border-pink-500' : 'border-edge hover:border-pink-500/60'
      }`}
    >
      <div className="text-4xl font-bold text-pink-400">
        {stat.known ? stat.count?.toLocaleString() : 'unknown'}
      </div>
      <div className="mt-2 text-sm font-medium text-hi">{stat.label}</div>
      <div className="mt-1 text-xs text-dim">
        {stat.known && stat.percent !== null
          ? `${stat.percent}% of ${stat.total.toLocaleString()} works`
          : `of ${stat.total.toLocaleString()} works`}
      </div>
      <div className="mt-3 border-t border-edge pt-3 text-xs leading-relaxed text-dim">
        {stat.hint}
      </div>
      <div className="mt-3 text-xs font-semibold text-pink-400">
        {active ? 'Showing these below' : 'See which books'}
      </div>
    </button>
  )
}

function StatsBand({
  data,
  activeFilter,
  onPick,
}: {
  data: CoverageResponse
  activeFilter: CoverageFilter
  onPick: (f: CoverageFilter) => void
}) {
  const stats = coverageStats(data.totals)
  return (
    <div className="grid gap-4 sm:grid-cols-3">
      {stats.map((s) => (
        <StatCard
          key={s.key}
          stat={s}
          active={activeFilter === filterForStat(s.key)}
          onPick={() => onPick(filterForStat(s.key))}
        />
      ))}
    </div>
  )
}

// --- The works browser ----------------------------------------------------

function BuildLinks({ row }: { row: CoverageWorkRow }) {
  if (!row.ctas.characters && !row.ctas.recaps) {
    return (
      <a href={href.work(row.id)} className={`${PILL_LINK} shrink-0 px-3 py-1.5`}>
        View work
      </a>
    )
  }
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

function WorkRow({ work }: { work: CoverageWork }) {
  const row = toWorkRow(work)
  return (
    <li className="flex flex-col gap-3 rounded-xl border border-edge bg-raised p-4 sm:flex-row sm:items-center sm:justify-between">
      <div className="min-w-0">
        <a
          href={href.work(row.id)}
          className="font-medium text-hi underline-offset-2 transition-colors hover:text-pink-300 hover:underline"
        >
          {row.title}
        </a>
        {row.series ? (
          <p className="mt-1 text-sm text-dim">
            {row.position ? (
              <span className="mr-1 font-semibold text-pink-400">#{row.position}</span>
            ) : null}
            <a
              href={href.series(row.series.id)}
              className="underline-offset-2 transition-colors hover:text-pink-300 hover:underline"
            >
              {row.series.name}
            </a>
          </p>
        ) : null}
        {row.authors.length > 0 ? (
          <p className="mt-0.5 text-sm text-dim">{personNames(row.authors)}</p>
        ) : null}
      </div>
      <BuildLinks row={row} />
    </li>
  )
}

const FILTER_EMPTY: Record<CoverageFilter, string> = {
  missing: 'Every catalogued book has characters and recaps - nothing needs work right now.',
  has_characters: 'No catalogued books have a character guide yet. Be the first to add one.',
  has_recaps: 'No catalogued books have a story-so-far recap yet. Be the first to add one.',
  has_recap_summary: 'No catalogued books have a whole-book recap summary yet.',
}

function WorksList({
  filter,
  query,
  offset,
  onOffset,
}: {
  filter: CoverageFilter
  query: string
  offset: number
  onOffset: (o: number) => void
}) {
  const state = useAsync<CoverageWorksResponse>(
    (signal) => getCoverageWorks({ filter, q: query, limit: PAGE_SIZE, offset }, signal),
    [filter, query, offset]
  )

  if (state.status === 'loading') {
    return <DetailSpinner label="Loading books..." className="py-12 text-center" />
  }
  if (state.status === 'error') {
    return (
      <ListMessage>
        The list could not be loaded right now. You can still contribute from any work page.
      </ListMessage>
    )
  }
  const { works, total, available } = state.data
  if (!available) {
    return (
      <ListMessage>
        This view is not available from the current database build. Pick any work from search and
        use its page to start adding characters or a story-so-far recap.
      </ListMessage>
    )
  }
  if (total === 0) {
    return <ListMessage>{query ? `No books match "${query}".` : FILTER_EMPTY[filter]}</ListMessage>
  }
  return (
    <>
      <ul className="space-y-3">
        {works.map((w) => (
          <WorkRow key={w.id} work={w} />
        ))}
      </ul>
      <Pager total={total} offset={offset} onOffset={onOffset} />
    </>
  )
}

function WorksBrowser({
  filter,
  onFilter,
  browserRef,
}: {
  filter: CoverageFilter
  onFilter: (f: CoverageFilter) => void
  browserRef: React.RefObject<HTMLDivElement | null>
}) {
  // Filter (from a tab or a stat card) and search both reset to page 1.
  const { rawQuery, setRawQuery, query, offset, setOffset } = usePagedSearch(filter)

  return (
    <section ref={browserRef} className="scroll-mt-24">
      <h2 className="text-xl font-semibold text-hi">Browse books by coverage</h2>
      <p className="mt-3 max-w-2xl text-sm leading-relaxed text-dim">
        Pick a stat above or a tab below to see which books need work - or which already have each
        layer - then open a work or jump straight into the guided builder. Search by title or
        author.
      </p>

      <div className="mt-6 flex flex-wrap gap-2">
        {COVERAGE_FILTERS.map((f) => (
          <button
            key={f.key}
            type="button"
            onClick={() => onFilter(f.key)}
            aria-pressed={filter === f.key}
            className={`rounded-full border px-4 py-1.5 text-sm font-medium transition-colors focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-pink-500 ${
              filter === f.key
                ? 'border-pink-500 bg-pink-600/10 text-pink-300'
                : 'border-edge text-body hover:border-pink-500/60 hover:text-hi'
            }`}
          >
            {f.label}
          </button>
        ))}
      </div>

      <div className="mt-4">
        <SearchBox
          value={rawQuery}
          onChange={setRawQuery}
          placeholder="Search by title or author..."
          label="Search books by title or author"
        />
      </div>

      <div className="mt-6">
        <WorksList filter={filter} query={query} offset={offset} onOffset={setOffset} />
      </div>
    </section>
  )
}

// --- Series with missing books --------------------------------------------

function SeriesGapRow({ gap }: { gap: CoverageSeriesGap }) {
  return (
    <li className="flex flex-col gap-3 rounded-xl border border-edge bg-surface p-4 sm:flex-row sm:items-center sm:justify-between">
      <div className="min-w-0">
        <a
          href={href.series(gap.id)}
          className="font-medium text-hi underline-offset-2 transition-colors hover:text-pink-300 hover:underline"
        >
          {gap.name}
        </a>
        <p className="mt-1 text-sm text-dim">
          <span className="text-body">Have:</span>{' '}
          {gap.present.length > 0 ? gap.present.join(', ') : 'none'}{' '}
          <span className="text-body">Missing:</span>{' '}
          <span className="text-pink-300">{gap.missing_positions.join(', ')}</span>
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
  )
}

function SeriesGapsList({
  query,
  offset,
  onOffset,
}: {
  query: string
  offset: number
  onOffset: (o: number) => void
}) {
  const state = useAsync<SeriesGapsResponse>(
    (signal) => getSeriesGaps({ q: query, limit: PAGE_SIZE, offset }, signal),
    [query, offset]
  )

  if (state.status === 'loading') {
    return <DetailSpinner label="Loading series..." className="py-12 text-center" />
  }
  if (state.status === 'error') {
    return <ListMessage>The series list could not be loaded right now.</ListMessage>
  }
  const { gaps, total } = state.data
  if (total === 0) {
    return (
      <ListMessage>
        {query
          ? `No series match "${query}".`
          : 'No series are missing interior volumes right now.'}
      </ListMessage>
    )
  }
  return (
    <>
      <ul className="space-y-3">
        {gaps.map((g) => (
          <SeriesGapRow key={g.id} gap={g} />
        ))}
      </ul>
      <Pager total={total} offset={offset} onOffset={onOffset} />
    </>
  )
}

function SeriesGapsBrowser() {
  const { rawQuery, setRawQuery, query, offset, setOffset } = usePagedSearch()

  return (
    <section>
      <h2 className="text-xl font-semibold text-hi">Series with missing books</h2>
      <p className="mt-3 max-w-2xl text-sm leading-relaxed text-dim">
        These series have gaps in their numbered volumes. If you own a missing book, add it through
        the issue form.
      </p>
      <div className="mt-4">
        <SearchBox
          value={rawQuery}
          onChange={setRawQuery}
          placeholder="Search series by name..."
          label="Search series by name"
        />
      </div>
      <div className="mt-6">
        <SeriesGapsList query={query} offset={offset} onOffset={setOffset} />
      </div>
    </section>
  )
}

// --- The island -----------------------------------------------------------

// Coverage is a singleton (no id); a fixed key satisfies the shared loader.
// Module-scope so its identity is stable across renders (useEntity depends on it).
const fetchCoverage = (_id: string, signal: AbortSignal) => getCoverage(signal)

export default function CoveragePanel() {
  const state = useEntity<CoverageResponse>('coverage', fetchCoverage)
  const [filter, setFilter] = useState<CoverageFilter>('missing')
  const browserRef = useRef<HTMLDivElement | null>(null)

  // Selecting a stat card jumps the reader down to the (now-filtered) browser.
  const pick = (f: CoverageFilter) => {
    setFilter(f)
    requestAnimationFrame(() =>
      browserRef.current?.scrollIntoView({ behavior: 'smooth', block: 'start' })
    )
  }

  if (state.status === 'loading') {
    return <DetailSpinner label="Loading what needs work..." className="py-16 text-center" />
  }

  if (state.status === 'error') {
    return (
      <div className="rounded-2xl border border-edge bg-surface p-6 text-center">
        <p className="text-sm leading-relaxed text-body">
          The coverage data could not be loaded right now. You can still contribute from any work
          page, or open an issue on GitHub.
        </p>
      </div>
    )
  }

  return (
    <div className="space-y-14">
      <StatsBand data={state.data} activeFilter={filter} onPick={pick} />
      <WorksBrowser filter={filter} onFilter={setFilter} browserRef={browserRef} />
      <SeriesGapsBrowser />
    </div>
  )
}
