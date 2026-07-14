import { getSeries, href, personNames, type Series } from '../../lib/api'
import CoverImage from '../cards/CoverImage'
import PersonLinks from '../cards/PersonLinks'
import {
  useQueryParam,
  usePageTitle,
  useEntity,
  DetailSpinner,
  DetailError,
  BackLink,
  ImproveRecord,
} from './detail-common'

function Loaded({ series }: { series: Series }) {
  usePageTitle(series.name)

  return (
    <div className="container py-10">
      <div className="mb-8">
        <BackLink />
      </div>

      <header className="mb-10">
        <span className="mb-2 block text-xs font-semibold uppercase tracking-[0.2em] text-pink-500">
          Series
        </span>
        <h1 className="text-3xl font-bold tracking-tight text-hi sm:text-4xl">{series.name}</h1>
        {series.authors && series.authors.length > 0 ? (
          <p className="mt-3 text-base">
            <span className="text-dim">By </span>
            <PersonLinks people={series.authors} className="font-medium" />
          </p>
        ) : null}
      </header>

      {series.works && series.works.length > 0 ? (
        <ol className="space-y-4">
          {series.works.map((entry) => (
            <li key={`${entry.position}-${entry.work.id}`}>
              <a
                href={href.work(entry.work.id)}
                className="group flex items-center gap-5 rounded-2xl border border-edge bg-surface p-4 transition-colors hover:border-pink-500/40"
              >
                <span className="w-12 shrink-0 text-center text-2xl font-black tabular-nums text-edge transition-colors group-hover:text-pink-500 sm:text-3xl">
                  {entry.position}
                </span>
                {/* Glyph-only fallback: the title sits right beside the thumb,
                    and at this size the in-tile title text does not fit. */}
                <div className="w-16 shrink-0 sm:w-20">
                  <CoverImage src={entry.work.cover_url} alt={`Cover of ${entry.work.title}`} />
                </div>
                <div className="min-w-0 flex-1">
                  <h2 className="truncate text-base font-medium text-hi group-hover:text-pink-300">
                    {entry.work.title}
                  </h2>
                  {entry.work.authors && entry.work.authors.length > 0 ? (
                    <p className="truncate text-sm text-dim">
                      {personNames(entry.work.authors)}
                    </p>
                  ) : null}
                </div>
                <svg
                  className="hidden h-5 w-5 shrink-0 text-dim transition-colors group-hover:text-pink-400 sm:block"
                  xmlns="http://www.w3.org/2000/svg"
                  fill="none"
                  viewBox="0 0 24 24"
                  strokeWidth={1.5}
                  stroke="currentColor"
                  aria-hidden="true"
                >
                  <path strokeLinecap="round" strokeLinejoin="round" d="M13.5 4.5 21 12m0 0-7.5 7.5M21 12H3" />
                </svg>
              </a>
            </li>
          ))}
        </ol>
      ) : (
        <p className="rounded-xl border border-edge bg-surface px-6 py-12 text-center text-sm text-dim">
          No works have been added to this series yet.
        </p>
      )}

      <ImproveRecord kind="series" id={series.id} />
    </div>
  )
}

export default function SeriesDetail() {
  const id = useQueryParam('id')
  const state = useEntity<Series>(id, getSeries)
  if (state.status === 'loading') return <DetailSpinner />
  if (state.status === 'error') return <DetailError notFound={state.notFound} kind="series" />
  return <Loaded series={state.data} />
}
