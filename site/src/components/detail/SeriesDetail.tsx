import { getSeries, href, personNames, today, type Series, type SeriesEntry } from '../../lib/api'
import {
  clearOwned,
  isWatched,
  markAllOwned,
  setOwned,
  setSkipped,
  unhide,
  unwatch,
  watch,
  watchedSeries,
} from '../../lib/watchlist'
import { useWatchlist, type WatchlistHandle } from '../watching/use-watchlist'
import { OwnCheckbox, ReleaseLine, SkipButton } from '../watching/entry-ui'
import CoverImage from '../cards/CoverImage'
import PersonLinks from '../cards/PersonLinks'
import { TEXT_LINK } from '../ui'
import {
  useEntitySlug,
  useEmbeddedEntity,
  usePageTitle,
  useEntity,
  DetailSpinner,
  DetailError,
  BackLink,
  ImproveRecord,
} from './detail-common'

/** The Watch / Watching toggle beside the series title. Watching is stored in
    this browser alone (there is no account), which the helper line says outright
    rather than leaving a reader to wonder where it went. */
function WatchToggle({
  series,
  watchlist: { store, save },
}: {
  series: Series
  watchlist: WatchlistHandle
}) {
  const watching = isWatched(store, series.id)
  const hidden = Boolean(watchedSeries(store, series.id)?.hidden)
  return (
    <div className="shrink-0 text-right">
      <button
        type="button"
        aria-pressed={watching}
        onClick={() =>
          save(
            watching
              ? unwatch(store, series.id)
              : watch(store, series.id, series.name, today())
          )
        }
        className={`inline-flex items-center gap-2 rounded-lg border px-4 py-2 text-sm font-medium transition-colors focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-pink-500 ${
          watching
            ? 'border-pink-500 bg-pink-600/10 text-pink-300 hover:bg-pink-600/20'
            : 'border-edge text-hi hover:border-pink-500'
        }`}
      >
        {watching ? 'Watching' : 'Watch this series'}
      </button>
      <p className="mt-2 max-w-[16rem] text-xs leading-relaxed text-dim">
        {watching ? (
          <>
            New and upcoming entries appear on{' '}
            <a href="/watching" className={TEXT_LINK}>
              your watching page
            </a>
            .
          </>
        ) : (
          'Kept in this browser only - never sent to the server.'
        )}
      </p>
      {hidden ? (
        <p className="mt-2 text-xs text-dim">
          Hidden from that page.{' '}
          <button
            type="button"
            onClick={() => save(unhide(store, series.id))}
            className={`${TEXT_LINK} font-medium`}
          >
            Show it again
          </button>
        </p>
      ) : null}
    </div>
  )
}

/** One volume. The whole row is a link to the work EXCEPT the two watching
    marks, which are interactive controls of their own - so the anchor wraps the
    reading half and they sit beside it, never inside it.

    A SKIPPED entry is still listed here in its own place: this is the series'
    own listing, so hiding a volume from it would misreport the series. The
    skip only takes anything out of the watching page. */
function EntryRow({
  entry,
  now,
  owned,
  skipped,
  onOwned,
  onSkipped,
}: {
  entry: SeriesEntry
  now: string
  owned: boolean | null
  skipped: boolean
  onOwned: (owned: boolean) => void
  onSkipped: (skipped: boolean) => void
}) {
  const work = entry.work
  // `owned === null` is "not watching", which is the one condition deciding
  // BOTH what sits at the row's right edge and whether there is a checkbox.
  const marking = owned !== null
  return (
    <li className="group flex items-center gap-4 rounded-2xl border border-edge bg-surface p-4 transition-colors hover:border-pink-500/40 sm:gap-5">
      <a href={href.work(work.id)} className="flex min-w-0 flex-1 items-center gap-4 sm:gap-5">
        <span className="w-10 shrink-0 text-center text-2xl font-black tabular-nums text-edge transition-colors group-hover:text-pink-500 sm:w-12 sm:text-3xl">
          {entry.position}
        </span>
        {/* Glyph-only fallback: the title sits right beside the thumb, and at
            this size the in-tile title text does not fit. */}
        <div className="w-16 shrink-0 sm:w-20">
          <CoverImage src={work.cover_url} alt={`Cover of ${work.title}`} />
        </div>
        <div className="min-w-0 flex-1">
          <h2 className="truncate text-base font-medium text-hi group-hover:text-pink-300">
            {work.title}
          </h2>
          {work.authors && work.authors.length > 0 ? (
            <p className="truncate text-sm text-dim">{personNames(work.authors)}</p>
          ) : null}
          <ReleaseLine date={work.release_date} today={now} />
        </div>
        {/* The row's own affordance, shown only when nothing else occupies the
            right edge - the ownership mark takes that place when watching. */}
        {marking ? null : (
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
        )}
      </a>
      {marking ? (
        <span className="flex shrink-0 items-center gap-2">
          <OwnCheckbox title={work.title} checked={owned} onChange={onOwned} />
          <SkipButton title={work.title} skipped={skipped} onChange={onSkipped} />
        </span>
      ) : null}
    </li>
  )
}

function Loaded({ series, hydrated }: { series: Series; hydrated: boolean }) {
  usePageTitle(series.name, hydrated)
  const watchlist = useWatchlist()
  const { store, save } = watchlist
  const watching = isWatched(store, series.id)
  const watched = watchedSeries(store, series.id)
  const owned = new Set(watched?.owned ?? [])
  const skipped = new Set(watched?.skipped ?? [])
  // Counted against the entries this page LISTS, not against everything the
  // store remembers: a mark survives a work leaving the series (and a `?limit`
  // window shows a page), so `owned.size` alone can read "12 of 10".
  const ownedHere = (series.works ?? []).filter((e) => owned.has(e.work.id)).length
  const now = today()

  return (
    <div className="container py-10">
      <div className="mb-8">
        <BackLink />
      </div>

      <header className="mb-10 flex flex-wrap items-start justify-between gap-6">
        <div className="min-w-0">
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
        </div>
        <WatchToggle series={series} watchlist={watchlist} />
      </header>

      {series.works && series.works.length > 0 ? (
        <>
          {watching ? (
            <div className="mb-4 flex flex-wrap items-center gap-x-4 gap-y-2 text-sm">
              <span className="text-dim">
                {ownedHere} of {series.works.length} marked as yours
              </span>
              <button
                type="button"
                onClick={() =>
                  save(markAllOwned(store, series.id, series.works.map((e) => e.work.id)))
                }
                className={`${TEXT_LINK} font-medium`}
              >
                Mark all as owned
              </button>
              <button
                type="button"
                onClick={() => save(clearOwned(store, series.id))}
                className={`${TEXT_LINK} font-medium`}
              >
                Clear
              </button>
            </div>
          ) : null}
          <ol className="space-y-4">
            {series.works.map((entry) => (
              <EntryRow
                key={`${entry.position}-${entry.work.id}`}
                entry={entry}
                now={now}
                owned={watching ? owned.has(entry.work.id) : null}
                skipped={skipped.has(entry.work.id)}
                onOwned={(next) => save(setOwned(store, series.id, entry.work.id, next))}
                onSkipped={(next) => save(setSkipped(store, series.id, entry.work.id, next))}
              />
            ))}
          </ol>
        </>
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
  const id = useEntitySlug('series')
  const embedded = useEmbeddedEntity<Series>(id)
  const state = useEntity<Series>(id, getSeries, embedded)
  if (state.status === 'loading') return <DetailSpinner />
  if (state.status === 'error') return <DetailError notFound={state.notFound} kind="series" />
  return <Loaded series={state.data} hydrated={embedded !== null} />
}
