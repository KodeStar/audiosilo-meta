// /watching: what is new in the series this browser follows.
//
// Everything it renders comes from two places - lib/watchlist.ts (the reader's
// own localStorage: which series, which volumes they have, which they have been
// shown, which they have passed on) and the public API (each watched series'
// current entries). No account, no request that says anything about the reader
// beyond "what is in this series", and nothing stored anywhere but this
// browser.
//
// Four TABS, each addressable by a URL hash (lib/watchnav.ts):
//   Available  the FLAT cross-series view (lib/watch-flat.ts) - everything out
//              now, newest first, and everything coming, soonest first. This is
//              the question a reader opens the page with.
//   All        the per-series panels: where am I in each series I follow.
//   Get notified  the feed URLs plus the ways to turn one into a notification
//              (lib/notify-options.ts, shared with /docs/notifications).
//   Import & backup  seeding the list from a library export, and the only
//              backup there is.
// The fetch and seen-snapshot effects below are deliberately page-level, not
// per tab: they run whichever tab is showing, so flipping tabs never re-fetches
// a series and never re-badges one as new.

import { useEffect, useMemo, useRef, useState } from 'react'
import { getSeries, href, today, type Series, type SeriesEntry } from '../../lib/api'
import { downloadJson } from '../../lib/download'
import { buildFeedURLs, type FeedURLs } from '../../lib/feed-url'
import { NOTIFY_INTRO_NOTES, NOTIFY_OPTIONS, notifyDocHref } from '../../lib/notify-options'
import { runPool, SERIES_POOL } from '../../lib/resolve-books'
import { flattenAcrossSeries, type FlatEntry } from '../../lib/watch-flat'
import {
  WATCH_TABS,
  hashForWatchTab,
  watchTabFromHash,
  type WatchTab,
} from '../../lib/watchnav'
import {
  classify,
  exportJSON,
  hiddenSeries,
  hide,
  importJSON,
  looksLikeWatchlist,
  markSeen,
  setOwned,
  setSkipped,
  unhide,
  unwatch,
  visibleSeries,
  watchedSeries,
  type ClassifiedEntry,
  type SeriesClassification,
  type Watchlist,
  type WatchlistRow,
} from '../../lib/watchlist'
import { useHashTab } from '../use-hash-tab'
import { Badge, BTN_SECONDARY, Icon, TabButton, TEXT_LINK } from '../ui'
import { NewPill, OwnCheckbox, ReleaseLine, SkipButton } from './entry-ui'
import LibraryImport from './LibraryImport'
import { useWatchlist, type WatchlistHandle } from './use-watchlist'

type SeriesState =
  | { status: 'loading' }
  | { status: 'ready'; data: Series }
  | { status: 'error' }

/** What one panel renders. Only a READY series has a classification, so the two
    travel together rather than as independent props a panel would have to
    defend against disagreeing. */
type PanelState =
  | { status: 'loading' }
  | { status: 'error' }
  | { status: 'ready'; result: SeriesClassification }

/** Everything in a series the reader does NOT have: what they can get now plus
    what they can preorder, in the series' own order. One definition, because
    the summary count, the "mark all seen" action and its visibility condition
    must all mean the same set. Skipped entries are deliberately outside it: a
    book the reader passed on is neither news nor something "mark all seen"
    should sweep. */
function missing(result: SeriesClassification): ClassifiedEntry[] {
  return [...result.available, ...result.preorder]
}

/** What each tab is called. Keyed by WATCH_TABS' own ids, so a tab added there
    is a compile error here until it has a label. */
const TAB_LABELS: Record<WatchTab, string> = {
  available: 'Available',
  all: 'All',
  notify: 'Get notified',
  import: 'Import & backup',
}

export default function WatchingPage() {
  const watchlist = useWatchlist()
  const { store, save } = watchlist
  const [details, setDetails] = useState<Record<string, SeriesState>>({})
  // The `seen` lists as they were when each series was first rendered THIS
  // visit. The store is marked seen immediately (so the next visit is quiet),
  // but the badges are drawn from this snapshot - otherwise the reader would
  // never see the "New" they came for.
  const [seenAtLoad, setSeenAtLoad] = useState<Record<string, string[]>>({})

  const requested = useRef<Set<string>>(new Set())
  const abortRef = useRef<AbortController | null>(null)
  useEffect(() => () => abortRef.current?.abort(), [])

  const visible = visibleSeries(store)
  const hiddenRows = hiddenSeries(store)
  const now = today()

  // The tab is the URL hash (components/use-hash-tab.ts), which also picks up
  // /docs/notifications linking back here as /watching#notify while the reader
  // is already on the page.
  const [tab, selectTab] = useHashTab<WatchTab>(watchTabFromHash, hashForWatchTab)

  // Fetch every watched series once. A slug already requested is never asked
  // for again, so unwatching one does not re-fetch the rest, and the controller
  // lives for the island rather than per effect run - aborting it is what
  // unmount does, not what a changed watchlist does.
  // The dependency IS the slug list (a slug can hold no comma - see the data
  // model's slug rule), so the effect is a function of what it depends on
  // rather than of a `visible` array that is rebuilt on every render.
  const slugKey = visible.map((r) => r.slug).join(',')
  useEffect(() => {
    const pending = slugKey
      ? slugKey.split(',').filter((slug) => !requested.current.has(slug))
      : []
    if (pending.length === 0) return
    for (const slug of pending) requested.current.add(slug)
    setDetails((d) => {
      const next = { ...d }
      for (const slug of pending) next[slug] = { status: 'loading' }
      return next
    })
    if (!abortRef.current) abortRef.current = new AbortController()
    const signal = abortRef.current.signal
    void runPool(pending, SERIES_POOL, signal, async (slug) => {
      try {
        const data = await getSeries(slug, signal)
        if (!signal.aborted) setDetails((d) => ({ ...d, [slug]: { status: 'ready', data } }))
      } catch {
        if (!signal.aborted) setDetails((d) => ({ ...d, [slug]: { status: 'error' } }))
      }
    })
  }, [slugKey])

  // Snapshot each series' `seen` the first time it renders, then record every
  // listed entry as seen. Both are idempotent, so this settles after one pass.
  useEffect(() => {
    let nextStore = store
    const captured: Record<string, string[]> = {}
    for (const row of visibleSeries(store)) {
      const state = details[row.slug]
      if (state?.status !== 'ready') continue
      if (!(row.slug in seenAtLoad)) captured[row.slug] = row.seen
      nextStore = markSeen(
        nextStore,
        row.slug,
        state.data.works.map((e) => e.work.id)
      )
    }
    if (Object.keys(captured).length > 0) setSeenAtLoad((s) => ({ ...s, ...captured }))
    if (nextStore !== store) save(nextStore)
  }, [details, store, seenAtLoad, save])

  // Classifying is a pass over every watched series' entries, and the tab bar
  // re-renders the page on every click, so it is memoised on exactly what it
  // reads.
  const classified = useMemo(
    () =>
      visibleSeries(store).map((row): { row: WatchlistRow; panel: PanelState } => {
        const state = details[row.slug]
        if (state?.status !== 'ready') return { row, panel: { status: state?.status ?? 'loading' } }
        // Live ownership, snapshotted seen - see seenAtLoad above.
        const entry = watchedSeries(store, row.slug)
        const forBadges = entry ? { ...entry, seen: seenAtLoad[row.slug] ?? entry.seen } : undefined
        return { row, panel: { status: 'ready', result: classify(state.data.works, forBadges, now) } }
      }),
    [store, details, seenAtLoad, now]
  )

  // "New" counts every entry the reader has not been shown before, preorders
  // included - an announced next volume is news. "Preorders" counts all of
  // them, so a new preorder is honestly in both numbers.
  const totals = classified.reduce(
    (acc, { panel }) => {
      if (panel.status !== 'ready') return acc
      return {
        fresh: acc.fresh + missing(panel.result).filter((e) => e.isNew).length,
        preorders: acc.preorders + panel.result.preorder.length,
      }
    },
    { fresh: 0, preorders: 0 }
  )

  // The flat view's input: every READY panel, in the shape lib/watch-flat.ts
  // reads. Sorting is its job, not this file's. The two counts below ride along
  // in the same pass - they are only ever read as numbers.
  const { readyPanels, flat, stillLoading, unreadable } = useMemo(() => {
    const panels: { slug: string; name: string; result: SeriesClassification }[] = []
    let loading = 0
    let errored = 0
    for (const { row, panel } of classified) {
      if (panel.status === 'ready') panels.push({ slug: row.slug, name: row.name, result: panel.result })
      else if (panel.status === 'error') errored += 1
      else loading += 1
    }
    return {
      readyPanels: panels,
      flat: flattenAcrossSeries(panels),
      stillLoading: loading,
      unreadable: errored,
    }
  }, [classified])

  const markOwnedIn = (slug: string, workID: string, owned: boolean) =>
    save(setOwned(store, slug, workID, owned))
  const markSkippedIn = (slug: string, workID: string, skipped: boolean) =>
    save(setSkipped(store, slug, workID, skipped))

  /** "Mark all seen" for the flat view: every ready series at once, so the
      badges the reader just read through all clear together. One store write
      and one snapshot update, because a per-series loop over `save` would each
      time discard the previous result. */
  function markEverythingSeen() {
    let nextStore = store
    const captured: Record<string, string[]> = {}
    for (const row of visible) {
      const state = details[row.slug]
      if (state?.status !== 'ready') continue
      const ids = state.data.works.map((e) => e.work.id)
      captured[row.slug] = ids
      nextStore = markSeen(nextStore, row.slug, ids)
    }
    setSeenAtLoad((s) => ({ ...s, ...captured }))
    if (nextStore !== store) save(nextStore)
  }

  const watching = visible.length > 0
  const readyCount = flat.preorder.length + flat.available.length

  // The number beside a tab's label, where it has one. `undefined` is "no
  // count": Available has none until a series has actually loaded, and the two
  // static tabs never do.
  const tabCounts: Record<WatchTab, number | undefined> = {
    available: readyPanels.length > 0 ? readyCount : undefined,
    all: watching ? visible.length : undefined,
    notify: undefined,
    import: undefined,
  }

  return (
    <div className="space-y-8">
      {watching ? (
        <p className="text-lg text-body" aria-live="polite">
          <span className="font-semibold text-hi">{totals.fresh.toLocaleString()} new</span>,{' '}
          {totals.preorders.toLocaleString()}{' '}
          {totals.preorders === 1 ? 'preorder' : 'preorders'} across{' '}
          {visible.length.toLocaleString()} series.
        </p>
      ) : null}

      {/* The bar shows even with an empty watchlist: Import & backup is how a
          reader with an export in hand starts, and hiding it would hide the
          quickest way in. The bar is BUILT from WATCH_TABS, so the tab set, the
          hash rule and the order a reader sees are one list. */}
      <div
        role="tablist"
        aria-label="Watching sections"
        className="flex flex-wrap gap-x-6 border-b border-edge"
      >
        {WATCH_TABS.map((id) => (
          <TabButton
            key={id}
            active={tab === id}
            onClick={() => selectTab(id)}
            label={TAB_LABELS[id]}
            count={tabCounts[id]}
            id={`tab-${id}`}
            controls={`panel-${id}`}
          />
        ))}
      </div>

      {/* ONE panel, whose id and label follow the active tab - a hidden panel
          is not in the document at all, so four of them would only be four
          ways to spell the same pair of attributes. */}
      <div role="tabpanel" id={`panel-${tab}`} aria-labelledby={`tab-${tab}`}>
        {renderTab()}
      </div>
    </div>
  )

  function renderTab() {
    switch (tab) {
      case 'available':
        return !watching ? (
          <EmptyState onImport={() => selectTab('import')} />
        ) : (
          <div className="space-y-6">
            {flat.preorder.some((e) => e.isNew) || flat.available.some((e) => e.isNew) ? (
              <p className="text-sm">
                <button type="button" onClick={markEverythingSeen} className={TEXT_LINK}>
                  Mark all seen
                </button>
              </p>
            ) : null}
            <EntryGroup
              heading="Preorders"
              className=""
              rows={flatRows(flat.preorder, now, markOwnedIn, markSkippedIn)}
            />
            <EntryGroup
              heading="Available"
              className=""
              rows={flatRows(flat.available, now, markOwnedIn, markSkippedIn)}
            />
            {readyCount === 0 && stillLoading === 0 ? (
              <p className="text-sm text-dim">You have every released entry.</p>
            ) : null}
            <LoadNote loading={stillLoading} errored={unreadable} />
          </div>
        )

      case 'all':
        return (
          <div className="space-y-6">
            {!watching ? (
              <EmptyState onImport={() => selectTab('import')} />
            ) : (
              classified.map(({ row, panel }) => (
                <SeriesPanel
                  key={row.slug}
                  row={row}
                  panel={panel}
                  now={now}
                  watchlist={watchlist}
                  onMarkAllSeen={(ids) => {
                    setSeenAtLoad((s) => ({ ...s, [row.slug]: ids }))
                    save(markSeen(store, row.slug, ids))
                  }}
                />
              ))
            )}

            {hiddenRows.length > 0 ? (
              <details className="rounded-2xl border border-edge bg-surface p-6">
                <summary className="cursor-pointer font-semibold text-hi">
                  Hidden series ({hiddenRows.length.toLocaleString()})
                </summary>
                <p className="mt-3 text-sm leading-relaxed text-body">
                  These stay out of the list above and out of every library import, until you bring
                  one back.
                </p>
                <ul className="mt-4 space-y-2">
                  {hiddenRows.map((row) => (
                    <li
                      key={row.slug}
                      className="flex flex-wrap items-center justify-between gap-3 rounded-xl border border-edge bg-raised px-4 py-3"
                    >
                      <a
                        href={href.series(row.slug)}
                        className="min-w-0 truncate font-medium text-hi hover:text-pink-300"
                      >
                        {row.name || row.slug}
                      </a>
                      <span className="flex shrink-0 gap-4 text-sm">
                        <button
                          type="button"
                          onClick={() => save(unhide(store, row.slug))}
                          className={TEXT_LINK}
                        >
                          Unhide
                        </button>
                        <button
                          type="button"
                          onClick={() => save(unwatch(store, row.slug))}
                          className={TEXT_LINK}
                        >
                          Forget
                        </button>
                      </span>
                    </li>
                  ))}
                </ul>
              </details>
            ) : null}
          </div>
        )

      case 'notify':
        return <NotificationFeed store={store} slugKey={slugKey} />

      case 'import':
        return (
          <div className="space-y-8">
            <section>
              <h2 className="text-xl font-bold tracking-tight text-hi">Import from your library</h2>
              <p className="mt-2 text-sm leading-relaxed text-body">
                Already have an export from OpenAudible, Libation, Audiobookshelf or the{' '}
                <code className="rounded border border-edge bg-raised px-1.5 py-0.5 font-mono text-xs text-pink-300">
                  metascan
                </code>{' '}
                tool? Drop it here to start watching the series it covers, with the volumes you own
                already ticked off.
              </p>
              <div className="mt-5">
                <LibraryImport watchlist={watchlist} />
              </div>
            </section>

            <Backup watchlist={watchlist} />
          </div>
        )
    }
  }
}

/** The one dim line saying the flat list is not the whole story yet: a series
    still being fetched, or one the API could not answer for. Renders nothing
    when every watched series is in. */
function LoadNote({ loading, errored }: { loading: number; errored: number }) {
  const parts: string[] = []
  if (loading > 0) parts.push(`${loading.toLocaleString()} series still loading`)
  if (errored > 0) parts.push(`${errored.toLocaleString()} series could not be read just now`)
  if (parts.length === 0) return null
  return (
    <p className="text-xs text-dim" aria-live="polite">
      {parts.join('. ')}.
    </p>
  )
}

function NotificationFeed({
  store,
  slugKey,
}: {
  store: Watchlist
  /** The visible series, comma-joined - the complete input to the feed URL, and
      therefore the effect's complete dependency. An EMPTY one means there is no
      URL to build, so the controls are replaced by the one line that says so;
      the ways to be told are still worth reading. */
  slugKey: string
}) {
  const [urls, setURLs] = useState<FeedURLs | null>(null)
  const [note, setNote] = useState('')
  const inputRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    let current = true
    setURLs(null)
    setNote('')
    if (!slugKey) return
    void buildFeedURLs(store)
      .then((next) => {
        if (current) setURLs(next)
      })
      .catch((err: unknown) => {
        // buildFeedURLs states WHY it refused (too many series to fit a URL);
        // anything else is the browser lacking CompressionStream.
        if (current) {
          setNote(
            err instanceof Error ? err.message : 'Could not build the feed URL in this browser.'
          )
        }
      })
    return () => {
      current = false
    }
    // The URL carries only visible slugs. Ownership, seen marks, skips and
    // stored series names do not affect it, so slugKey is the complete
    // dependency.
  }, [slugKey])

  async function copyURL() {
    if (!urls) return
    try {
      await navigator.clipboard.writeText(urls.atom)
      setNote('Copied Atom feed URL.')
      return
    } catch {
      const input = inputRef.current
      if (!input) return
      input.focus()
      input.select()
      input.setSelectionRange(0, input.value.length)
      setNote('The URL is selected. Copy it with your browser or keyboard shortcut.')
    }
  }

  return (
    <section className="rounded-2xl border border-edge bg-surface p-6">
      <h2 className="text-xl font-bold tracking-tight text-hi">Get notified</h2>
      <p className="mt-2 text-sm leading-relaxed text-body">
        The URL below is a private feed of your watched series. Nothing is stored on this site: the
        URL itself is the subscription.
      </p>
      {slugKey === '' ? (
        <p className="mt-5 text-sm text-dim">
          Watch a series first and this tab will build your feed URL.
        </p>
      ) : (
        <>
          <div className="mt-5 flex flex-col gap-3 sm:flex-row">
            <input
              ref={inputRef}
              type="url"
              readOnly
              aria-label="Atom feed URL"
              /* Once the build has failed, `note` carries the reason; leaving the
                 placeholder up would say the URL is still coming when nothing is
                 still trying. */
              value={urls?.atom ?? (note ? '' : 'Building your feed URL...')}
              onFocus={(event) => event.currentTarget.select()}
              className="min-w-0 flex-1 rounded-lg border border-edge bg-raised px-3 py-2 font-mono text-xs text-hi outline-none focus:border-pink-500"
            />
            <button
              type="button"
              onClick={() => void copyURL()}
              disabled={!urls}
              className={`${BTN_SECONDARY} shrink-0 px-5 py-2 text-sm`}
            >
              Copy
            </button>
          </div>
          <p className="mt-3 text-sm text-dim" aria-live="polite">
            {note}
          </p>
          {urls ? (
            <p className="mt-2 flex flex-wrap gap-x-4 gap-y-2 text-sm">
              <a className={TEXT_LINK} href={urls.webcal}>
                Calendar (webcal)
              </a>
              <a className={TEXT_LINK} href={urls.atom}>
                Atom feed
              </a>
              <a className={TEXT_LINK} href={urls.json}>
                JSON Feed
              </a>
            </p>
          ) : null}
        </>
      )}

      <ul className="mt-6 list-disc space-y-2 pl-5 text-sm leading-relaxed text-body">
        {NOTIFY_INTRO_NOTES.map((intro) => (
          <li key={intro}>{intro}</li>
        ))}
      </ul>

      <h3 className="mt-6 text-sm font-semibold uppercase tracking-[0.2em] text-pink-500">
        Pick where you want to be told
      </h3>
      <ul className="mt-3 space-y-3">
        {NOTIFY_OPTIONS.map((option) => (
          <li key={option.id} className="rounded-xl border border-edge bg-raised p-4">
            <div className="flex flex-wrap items-center gap-2">
              <span className="font-medium text-hi">{option.label}</span>
              {option.needsAccount ? <Badge>needs an account</Badge> : null}
            </div>
            <p className="mt-1 text-sm leading-relaxed text-body">{option.blurb}</p>
            <a href={notifyDocHref(option.id)} className={`${TEXT_LINK} mt-2 inline-block text-sm`}>
              Steps
            </a>
          </li>
        ))}
      </ul>
      <p className="mt-4 text-sm text-body">
        Every option, step by step, is on{' '}
        <a href="/docs/notifications" className={TEXT_LINK}>
          the notifications page
        </a>
        .
      </p>
    </section>
  )
}

function EmptyState({ onImport }: { onImport: () => void }) {
  return (
    <div className="rounded-2xl border border-edge bg-surface p-8 text-center">
      <h2 className="text-xl font-bold text-hi">You are not watching anything yet</h2>
      <p className="mx-auto mt-3 max-w-lg text-sm leading-relaxed text-body">
        Open any series in the database and press <span className="text-hi">Watch this series</span>.
        This page then lists the entries you do not have yet, and anything still up for preorder.
        Everything is stored in this browser only - there is no account, and nothing is sent to the
        server.
      </p>
      <div className="mt-6 flex flex-wrap items-center justify-center gap-3">
        <a href="/" className={`${BTN_SECONDARY} px-5 py-2.5 text-sm`}>
          Search for a series
        </a>
      </div>
      <p className="mt-4 text-sm text-dim">
        Or{' '}
        <button type="button" onClick={onImport} className={TEXT_LINK}>
          import a library export
        </button>{' '}
        to start in one go.
      </p>
    </div>
  )
}

/** One watched series: what is missing, what is on preorder, what is already
    yours, what you passed on, and the controls for the series as a whole. */
function SeriesPanel({
  row,
  panel,
  now,
  watchlist: { store, save },
  onMarkAllSeen,
}: {
  row: WatchlistRow
  panel: PanelState
  now: string
  watchlist: WatchlistHandle
  onMarkAllSeen: (ids: string[]) => void
}) {
  const name = row.name || row.slug
  const markOwned = (workID: string, owned: boolean) =>
    save(setOwned(store, row.slug, workID, owned))
  const markSkipped = (workID: string, skipped: boolean) =>
    save(setSkipped(store, row.slug, workID, skipped))
  // The set the header's count, its action and its visibility condition all
  // mean - computed once, so they cannot drift apart within one render.
  const unowned = panel.status === 'ready' ? missing(panel.result) : []
  return (
    <section className="rounded-2xl border border-edge bg-surface p-6">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <h2 className="min-w-0 text-lg font-semibold text-hi">
          <a href={href.series(row.slug)} className="hover:text-pink-300">
            {name}
          </a>
        </h2>
        <div className="flex shrink-0 flex-wrap gap-4 text-sm">
          {unowned.some((e) => e.isNew) ? (
            <button
              type="button"
              onClick={() => onMarkAllSeen(unowned.map((e) => e.entry.work.id))}
              className={TEXT_LINK}
            >
              Mark all seen
            </button>
          ) : null}
          <button
            type="button"
            onClick={() => save(hide(store, row.slug, name, now))}
            className={TEXT_LINK}
          >
            Hide
          </button>
          <button type="button" onClick={() => save(unwatch(store, row.slug))} className={TEXT_LINK}>
            Unwatch
          </button>
        </div>
      </div>

      {panel.status === 'loading' ? (
        <p className="mt-4 text-sm text-dim" aria-live="polite">
          Loading...
        </p>
      ) : panel.status === 'error' ? (
        <p className="mt-4 text-sm text-dim">
          This series could not be read from the database just now. It is still on your list.
        </p>
      ) : (
        <>
          <EntryGroup
            heading="Available"
            empty="You have every released entry."
            rows={panelRows(panel.result.available, now, markOwned, markSkipped)}
          />
          <EntryGroup
            heading="Preorder"
            rows={panelRows(panel.result.preorder, now, markOwned, markSkipped)}
          />
          {panel.result.owned.length > 0 ? (
            <details className="mt-5">
              <summary className="cursor-pointer text-sm text-dim hover:text-hi">
                {panel.result.owned.length.toLocaleString()} you already have
              </summary>
              <ul className="mt-3 space-y-2">
                {panel.result.owned.map((entry) => (
                  <EntryRow
                    key={entry.work.id}
                    entry={entry}
                    now={now}
                    owned
                    onOwned={(next) => markOwned(entry.work.id, next)}
                  />
                ))}
              </ul>
            </details>
          ) : null}
          {panel.result.skipped.length > 0 ? (
            <details className="mt-3">
              <summary className="cursor-pointer text-sm text-dim hover:text-hi">
                {panel.result.skipped.length.toLocaleString()} skipped
              </summary>
              <ul className="mt-3 space-y-2">
                {panel.result.skipped.map((entry) => (
                  <EntryRow
                    key={entry.work.id}
                    entry={entry}
                    now={now}
                    owned={false}
                    skipped
                    onOwned={(next) => markOwned(entry.work.id, next)}
                    onSkipped={(next) => markSkipped(entry.work.id, next)}
                  />
                ))}
              </ul>
            </details>
          ) : null}
        </>
      )}
    </section>
  )
}

/** A headed list of entry rows - both tabs' only list. It takes the rows
    already composed rather than a classification, which is what lets the FLAT
    view (rows carrying a `series`, pooled from every panel) and a series panel
    share it. */
function EntryGroup({
  heading,
  empty,
  className = 'mt-5',
  rows,
}: {
  heading: string
  /** Shown INSTEAD of the group when it is empty. A group with no empty line
      renders nothing at all - "no preorders" is not news. */
  empty?: string
  className?: string
  rows: EntryRowProps[]
}) {
  if (rows.length === 0) {
    if (!empty) return null
    return <p className="mt-4 text-sm text-dim">{empty}</p>
  }
  return (
    <div className={className}>
      <h3 className="text-xs font-semibold uppercase tracking-[0.2em] text-pink-500">
        {heading} ({rows.length.toLocaleString()})
      </h3>
      <ul className="mt-3 space-y-2">
        {rows.map((row) => (
          <EntryRow key={`${row.series?.slug ?? ''}-${row.entry.work.id}`} {...row} />
        ))}
      </ul>
    </div>
  )
}

/** The flat view's rows: an entry pooled out of its series panel, with the
    series it came from and the two marks pointed back at it. */
function flatRows(
  entries: readonly FlatEntry[],
  now: string,
  onOwned: (slug: string, workID: string, owned: boolean) => void,
  onSkipped: (slug: string, workID: string, skipped: boolean) => void
): EntryRowProps[] {
  return entries.map((flat) => ({
    entry: flat.entry,
    now,
    isNew: flat.isNew,
    owned: false,
    series: { slug: flat.slug, name: flat.series },
    onOwned: (next: boolean) => onOwned(flat.slug, flat.entry.work.id, next),
    onSkipped: (next: boolean) => onSkipped(flat.slug, flat.entry.work.id, next),
  }))
}

/** A series panel's rows: the same shape, with the series left off (the panel's
    own heading already says which series this is). */
function panelRows(
  entries: readonly ClassifiedEntry[],
  now: string,
  onOwned: (workID: string, owned: boolean) => void,
  onSkipped: (workID: string, skipped: boolean) => void
): EntryRowProps[] {
  return entries.map(({ entry, isNew }) => ({
    entry,
    now,
    isNew,
    owned: false,
    onOwned: (next: boolean) => onOwned(entry.work.id, next),
    onSkipped: (next: boolean) => onSkipped(entry.work.id, next),
  }))
}

/** One row of any entry list, on either tab. `series` is what the FLAT view
    adds - the series the entry came from, which is the whole reason a
    cross-series list is readable - and it is a link of its own above the work
    line rather than inside it, since anchors do not nest. */
interface EntryRowProps {
  entry: SeriesEntry
  now: string
  isNew?: boolean
  owned: boolean
  skipped?: boolean
  series?: { slug: string; name: string }
  onOwned: (owned: boolean) => void
  /** Absent on an OWNED row: a book the reader has is already out of the way,
      so "not interested" would be a control with nothing to do. */
  onSkipped?: (skipped: boolean) => void
}

function EntryRow({
  entry,
  now,
  isNew = false,
  owned,
  skipped = false,
  series,
  onOwned,
  onSkipped,
}: EntryRowProps) {
  const work = entry.work
  return (
    <li className="flex items-center gap-3 rounded-xl border border-edge bg-raised p-3">
      <div className="min-w-0 flex-1">
        {series ? (
          <a
            href={href.series(series.slug)}
            className="block truncate text-xs text-dim hover:text-pink-300"
          >
            {series.name}
          </a>
        ) : null}
        <a href={href.work(work.id)} className={series ? 'mt-0.5 block' : 'block'}>
          <span className="flex flex-wrap items-center gap-2">
            <span className="shrink-0 text-sm font-black tabular-nums text-edge">{entry.position}</span>
            <span className="min-w-0 truncate font-medium text-hi hover:text-pink-300">{work.title}</span>
            {isNew ? <NewPill /> : null}
          </span>
          <ReleaseLine date={work.release_date} today={now} />
        </a>
      </div>
      <OwnCheckbox title={work.title} checked={owned} onChange={onOwned} />
      {onSkipped ? (
        <SkipButton title={work.title} skipped={skipped} onChange={onSkipped} />
      ) : null}
    </li>
  )
}

/** Download the watchlist, or merge one back in. It is the only backup there
    is: nothing about it reaches the server, so clearing site data clears it. */
function Backup({ watchlist: { store, save } }: { watchlist: WatchlistHandle }) {
  const [note, setNote] = useState('')
  const inputRef = useRef<HTMLInputElement>(null)

  function importFile(file: File) {
    const reader = new FileReader()
    reader.onload = () => {
      const text = String(reader.result ?? '')
      if (!looksLikeWatchlist(text)) {
        setNote('That file is not a watchlist backup.')
        return
      }
      const merged = importJSON(store, text)
      save(merged)
      setNote(`Merged. You are watching ${Object.keys(merged.series).length} series.`)
    }
    reader.onerror = () => setNote('Could not read that file.')
    reader.readAsText(file)
    if (inputRef.current) inputRef.current.value = ''
  }

  return (
    <section className="rounded-2xl border border-edge bg-surface p-6">
      <h2 className="text-xl font-bold tracking-tight text-hi">Backup</h2>
      <p className="mt-2 text-sm leading-relaxed text-body">
        Your watchlist lives in this browser alone. Download it to keep a copy, or to move it to
        another browser or device - importing MERGES, so nothing you have marked is ever lost.
      </p>
      <div className="mt-5 flex flex-wrap gap-3">
        <button
          type="button"
          onClick={() => downloadJson(exportJSON(store), 'audiosilo-meta-watchlist.json')}
          className={`${BTN_SECONDARY} px-5 py-2.5 text-sm`}
        >
          <Icon name="download" className="h-4 w-4" />
          Download watchlist (.json)
        </button>
        <input
          ref={inputRef}
          type="file"
          accept=".json,application/json"
          className="sr-only"
          onChange={(e) => {
            const file = e.target.files?.[0]
            if (file) importFile(file)
          }}
        />
        <button
          type="button"
          onClick={() => inputRef.current?.click()}
          className={`${BTN_SECONDARY} px-5 py-2.5 text-sm`}
        >
          Import a backup
        </button>
      </div>
      {note ? (
        <p className="mt-3 text-sm text-dim" aria-live="polite">
          {note}
        </p>
      ) : null}
    </section>
  )
}
