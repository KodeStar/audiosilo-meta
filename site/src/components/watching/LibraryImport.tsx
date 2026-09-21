// "Import from your library" on /watching: drop the same export the /import
// page takes, and turn the books the catalogue recognises into watched series
// with the volumes you already have ticked off.
//
// It shares EVERY matching rule with /import - the sweep is lib/resolve-books.ts
// and the parsing is lib/import-parse.ts - so the two pages can never disagree
// about which of your books the database holds. What is different is only what
// is done with the answer: /import wants the misses, this wants the hits.

import { useEffect, useRef, useState } from 'react'
import { getSeries, today, type Series } from '../../lib/api'
import { parseExport, type ParsedBook, type ParseOutcome } from '../../lib/import-parse'
import {
  groupBySeries,
  ownedWorks,
  resolveLibrary,
  runPool,
  SERIES_POOL,
  type OwnedSeries,
  type OwnedWork,
  type ResolvedLibrary,
} from '../../lib/resolve-books'
import { hide, markAllOwned, watch, watchedSeries, type Watchlist } from '../../lib/watchlist'
import { BTN_PRIMARY, BTN_SECONDARY, Icon } from '../ui'
import type { WatchlistHandle } from './use-watchlist'

/** One proposed row: a series the reader owns something in. */
interface Row {
  series: OwnedSeries
  /** The series' total catalogued volumes, or null when the lookup failed - the
      row then reports what it knows rather than inventing a denominator. */
  total: number | null
  include: boolean
  /** Ticked on an EXCLUDED row: remember the refusal so a later import does not
      offer this series again. */
  never: boolean
}

type Phase = 'idle' | 'reading' | 'unknown' | 'error' | 'results' | 'applied'

export default function LibraryImport({ watchlist }: { watchlist: WatchlistHandle }) {
  const [phase, setPhase] = useState<Phase>('idle')
  const [message, setMessage] = useState('')
  const [dragging, setDragging] = useState(false)
  const [rows, setRows] = useState<Row[]>([])
  const [unresolved, setUnresolved] = useState<string[]>([])
  const [applied, setApplied] = useState(0)

  const abortRef = useRef<AbortController | null>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)

  // Abort any in-flight sweep when the island unmounts.
  useEffect(() => () => abortRef.current?.abort(), [])

  function reset() {
    abortRef.current?.abort()
    abortRef.current = null
    setPhase('idle')
    setMessage('')
    setRows([])
    setUnresolved([])
    setApplied(0)
    if (fileInputRef.current) fileInputRef.current.value = ''
  }

  function readFile(file: File) {
    const reader = new FileReader()
    reader.onload = () => handleText(String(reader.result ?? ''))
    reader.onerror = () => {
      setMessage('Could not read that file. Please try again.')
      setPhase('error')
    }
    reader.readAsText(file)
  }

  function handleText(text: string) {
    let outcome: ParseOutcome
    try {
      outcome = parseExport(text)
    } catch (err) {
      setMessage(err instanceof Error ? err.message : 'Could not read that file.')
      setPhase('error')
      return
    }
    if (outcome.format === 'unknown') {
      setPhase('unknown')
      return
    }
    void sweep(outcome.books)
  }

  async function sweep(books: ParsedBook[]) {
    setRows([])
    setUnresolved([])
    setMessage('Checking your library against the database...')
    setPhase('reading')

    const ctrl = new AbortController()
    abortRef.current = ctrl

    const resolved = await resolveLibrary(books, ctrl.signal, (p) => {
      setMessage(
        p.matching
          ? 'Matching your editions...'
          : p.total === 0
            ? 'Sorting your library...'
            : `Checking ${Math.min(p.done + 1, p.total)} of ${p.total}...`
      )
    })
    if (!resolved) return // aborted: the view is going away

    const works = ownedWorks(resolved)
    const grouped = groupBySeries(works)

    // How big is each series? The rows say "N of M owned", and M is the
    // catalogue's count, not the reader's - so one lookup per proposed series.
    setMessage(`Reading ${grouped.length} series...`)
    const totals = new Map<string, number>()
    await runPool(grouped, SERIES_POOL, ctrl.signal, async (g) => {
      try {
        const s: Series = await getSeries(g.slug, ctrl.signal)
        totals.set(g.slug, s.works_total ?? s.works.length)
      } catch {
        /* the row reports what it knows instead */
      }
    })
    if (ctrl.signal.aborted) return

    abortRef.current = null
    setRows(
      grouped.map((series) => {
        // A series the reader has already dismissed stays dismissed: it is
        // offered unticked, with its refusal still remembered.
        const dismissed = Boolean(watchedSeries(watchlist.store, series.slug)?.hidden)
        return {
          series,
          total: totals.get(series.slug) ?? null,
          include: !dismissed,
          never: dismissed,
        }
      })
    )
    setUnresolved(unresolvedTitles(resolved, works))
    setMessage('')
    setPhase('results')
  }

  function apply() {
    const now = today()
    let next: Watchlist = watchlist.store
    let count = 0
    for (const row of rows) {
      if (row.include) {
        next = watch(next, row.series.slug, row.series.name, now)
        next = markAllOwned(next, row.series.slug, row.series.works)
        count += 1
      } else if (row.never) {
        next = hide(next, row.series.slug, row.series.name, now)
      }
    }
    watchlist.save(next)
    setApplied(count)
    setPhase('applied')
  }

  function setRow(slug: string, patch: Partial<Row>) {
    setRows((current) => current.map((r) => (r.series.slug === slug ? { ...r, ...patch } : r)))
  }

  // --- Renders --------------------------------------------------------------

  if (phase === 'reading') {
    return (
      <div className="rounded-2xl border border-edge bg-surface p-6">
        <div className="flex flex-wrap items-center justify-between gap-4">
          <p className="text-sm font-medium text-hi" aria-live="polite">
            {message}
          </p>
          <button type="button" onClick={reset} className={`${BTN_SECONDARY} px-4 py-2 text-sm`}>
            Cancel
          </button>
        </div>
      </div>
    )
  }

  if (phase === 'unknown' || phase === 'error') {
    return (
      <div className="rounded-2xl border border-edge bg-surface p-6">
        <h3 className="text-lg font-semibold text-hi">
          {phase === 'unknown' ? 'This does not look like a supported export' : 'We could not read that file'}
        </h3>
        <p className="mt-2 text-sm leading-relaxed text-body">
          {phase === 'unknown'
            ? 'We could not find any books in this file. Drop an OpenAudible books.json, a Libation library export, an Audiobookshelf export, an audiosilo folder scan, or the new-books file this site downloads.'
            : message}
        </p>
        <button type="button" onClick={reset} className={`${BTN_SECONDARY} mt-5 px-4 py-2 text-sm`}>
          Try another file
        </button>
      </div>
    )
  }

  if (phase === 'applied') {
    return (
      <div className="rounded-2xl border border-edge bg-surface p-6">
        <h3 className="text-lg font-semibold text-hi">
          {applied === 0 ? 'Nothing added' : `Now watching ${applied.toLocaleString()} series`}
        </h3>
        <p className="mt-2 text-sm leading-relaxed text-body">
          {applied === 0
            ? 'No series were selected, so your watchlist is unchanged.'
            : 'The volumes your export matched are marked as yours; anything else shows up above as available or a preorder.'}
        </p>
        <button type="button" onClick={reset} className={`${BTN_SECONDARY} mt-5 px-4 py-2 text-sm`}>
          Import another file
        </button>
      </div>
    )
  }

  if (phase === 'results') {
    const chosen = rows.filter((r) => r.include).length
    return (
      <div className="rounded-2xl border border-edge bg-surface p-6">
        <h3 className="text-lg font-semibold text-hi">
          {rows.length === 0
            ? 'No series to add'
            : `${rows.length.toLocaleString()} series from your library`}
        </h3>
        {rows.length === 0 ? (
          <p className="mt-2 text-sm leading-relaxed text-body">
            None of the books we could match belong to a catalogued series, so there is nothing to
            watch. Everything matched is listed below.
          </p>
        ) : (
          <p className="mt-2 text-sm leading-relaxed text-body">
            Tick the series you want to follow. Applying watches each one and marks the volumes your
            export matched as yours.
          </p>
        )}

        {rows.length > 0 ? (
          <ul className="mt-5 space-y-2">
            {rows.map((row) => (
              <li
                key={row.series.slug}
                className="flex flex-wrap items-center justify-between gap-3 rounded-xl border border-edge bg-raised p-4"
              >
                <label className="flex min-w-0 flex-1 cursor-pointer items-center gap-3">
                  <input
                    type="checkbox"
                    className="h-4 w-4 shrink-0 accent-pink-500"
                    checked={row.include}
                    onChange={(e) =>
                      setRow(row.series.slug, {
                        include: e.target.checked,
                        never: e.target.checked ? false : row.never,
                      })
                    }
                  />
                  <span className="min-w-0">
                    <span className="block truncate font-medium text-hi">{row.series.name}</span>
                    <span className="block text-xs text-dim">
                      {row.total === null
                        ? `${row.series.works.length} of your books matched`
                        : `${row.series.works.length} of ${row.total} owned`}
                    </span>
                  </span>
                </label>
                <label
                  className={`flex shrink-0 cursor-pointer items-center gap-2 text-xs ${
                    row.include ? 'text-edge' : 'text-dim'
                  }`}
                >
                  <input
                    type="checkbox"
                    className="h-4 w-4 accent-pink-500"
                    checked={row.never}
                    disabled={row.include}
                    onChange={(e) => setRow(row.series.slug, { never: e.target.checked })}
                  />
                  Never suggest again
                </label>
              </li>
            ))}
          </ul>
        ) : null}

        {unresolved.length > 0 ? (
          <details className="mt-5 rounded-xl border border-edge bg-raised p-4">
            <summary className="cursor-pointer text-sm font-medium text-hi">
              {unresolved.length.toLocaleString()} books were not added to a series
            </summary>
            <p className="mt-3 text-sm leading-relaxed text-body">
              These are either not in the database yet, or catalogued but not part of any series.
              Nothing was dropped silently - you can contribute the missing ones from the{' '}
              <a href="/import" className="text-pink-400 hover:text-pink-300">
                import page
              </a>
              .
            </p>
            <ul className="mt-3 max-h-64 space-y-1 overflow-y-auto text-sm text-dim">
              {unresolved.map((title, i) => (
                <li key={`${title}-${i}`} className="truncate">
                  {title}
                </li>
              ))}
            </ul>
          </details>
        ) : null}

        <div className="mt-6 flex flex-wrap gap-3">
          <button
            type="button"
            onClick={apply}
            disabled={chosen === 0}
            className={`${BTN_PRIMARY} px-5 py-2.5 text-sm`}
          >
            {chosen === 0 ? 'Select a series' : `Watch ${chosen.toLocaleString()} series`}
          </button>
          <button type="button" onClick={reset} className={`${BTN_SECONDARY} px-5 py-2.5 text-sm`}>
            Cancel
          </button>
        </div>
      </div>
    )
  }

  return (
    <div
      onDragOver={(e) => {
        e.preventDefault()
        setDragging(true)
      }}
      onDragLeave={() => setDragging(false)}
      onDrop={(e) => {
        e.preventDefault()
        setDragging(false)
        const file = e.dataTransfer.files?.[0]
        if (file) readFile(file)
      }}
    >
      <input
        ref={fileInputRef}
        type="file"
        accept=".json,application/json"
        className="sr-only"
        onChange={(e) => {
          const file = e.target.files?.[0]
          if (file) readFile(file)
        }}
      />
      <button
        type="button"
        onClick={() => fileInputRef.current?.click()}
        className={`flex w-full flex-col items-center justify-center gap-3 rounded-2xl border-2 border-dashed p-8 text-center transition-colors ${
          dragging ? 'border-pink-500 bg-pink-600/5' : 'border-edge bg-surface hover:border-pink-500/60'
        }`}
      >
        <span className="inline-flex h-12 w-12 items-center justify-center rounded-2xl border border-edge bg-raised text-pink-400">
          <Icon name="database" className="h-6 w-6" />
        </span>
        <span className="text-base font-semibold text-hi">Drop a library export here</span>
        <span className="text-sm text-dim">
          or <span className="text-pink-400">choose a file</span> - OpenAudible, Libation,
          Audiobookshelf, or an audiosilo folder scan
        </span>
      </button>
      <p className="mt-3 text-sm leading-relaxed text-dim">
        The file is read in your browser. The API is asked only about identifiers and, for books it
        cannot match that way, author names - the same requests the search box makes.
      </p>
    </div>
  )
}

/** The books a sweep could not turn into a watchable series entry, as titles.
    Two reasons, both honest: the database does not hold the book at all, or it
    does but the work belongs to no series. Never silently dropped. */
function unresolvedTitles(resolved: ResolvedLibrary, works: readonly OwnedWork[]): string[] {
  const titles = resolved.cannotMatch.map((b) => b.title)
  for (const n of resolved.newBooks) {
    if (!n.existingWork) titles.push(n.book.title)
  }
  for (const w of works) {
    if (!w.series) titles.push(w.title)
  }
  return titles
}
