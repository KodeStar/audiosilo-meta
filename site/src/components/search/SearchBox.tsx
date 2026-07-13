import { useCallback, useEffect, useId, useMemo, useRef, useState } from 'react'
import {
  search,
  lookup,
  href,
  looksLikeAsin,
  looksLikeIsbn,
  normaliseIsbn,
  personNames,
  type SearchResult,
  type LookupResponse,
} from '../../lib/api'
import { addWorkFromQueryUrl } from '../../lib/search-cta'

interface Props {
  /** Quiet example queries shown below the box; a tap fills and runs a search. */
  examples?: string[]
  autoFocus?: boolean
}

/** A flattened, navigable option (group headers are rendered separately and are
    not part of this list, so keyboard navigation only ever lands on a result). */
interface Option {
  key: string
  group: 'exact' | 'work' | 'person' | 'series'
  href: string
  primary: string
  secondary?: string
  coverUrl?: string | null
  seriesName?: string
  seriesPosition?: string
  worksCount?: number
  exact?: boolean
}

const DEBOUNCE_MS = 200
const MIN_CHARS = 2

/** Split `text` on the first case-insensitive occurrence of `q` and wrap the
    match in <mark>. Purely presentational (the query is short, plain text). */
function highlight(text: string, q: string) {
  const query = q.trim()
  if (!query) return text
  const idx = text.toLowerCase().indexOf(query.toLowerCase())
  if (idx === -1) return text
  return (
    <>
      {text.slice(0, idx)}
      <mark>{text.slice(idx, idx + query.length)}</mark>
      {text.slice(idx + query.length)}
    </>
  )
}

function resultsToOptions(
  results: SearchResult[],
  pinned: LookupResponse | null
): Option[] {
  // Bucket by kind so the dropdown always renders its groups in a FIXED order -
  // Exact match, Works, People, Series - no matter how the API interleaves
  // kinds (bm25 ranks across kinds). Ranking still orders results WITHIN each
  // group, because each bucket preserves the API's result order.
  const works: Option[] = []
  const people: Option[] = []
  const series: Option[] = []
  const exact: Option[] = []

  if (pinned) {
    const w = pinned.work
    exact.push({
      key: `exact-${w.id}`,
      group: 'exact',
      href: href.work(w.id),
      primary: w.title,
      secondary: w.authors ? personNames(w.authors) : undefined,
      coverUrl: w.cover_url,
      seriesName: w.series?.name,
      seriesPosition: w.series?.position,
      exact: true,
    })
  }
  for (const r of results) {
    if (r.kind === 'work') {
      // Avoid duplicating the pinned exact match.
      if (pinned && r.id === pinned.work.id) continue
      works.push({
        key: `work-${r.id}`,
        group: 'work',
        href: href.work(r.id),
        primary: r.title,
        secondary: r.authors ? personNames(r.authors) : undefined,
        coverUrl: r.cover_url,
        seriesName: r.series?.name,
        seriesPosition: r.series?.position ?? undefined,
      })
    } else if (r.kind === 'person') {
      people.push({
        key: `person-${r.id}`,
        group: 'person',
        href: href.person(r.id),
        primary: r.name,
      })
    } else {
      series.push({
        key: `series-${r.id}`,
        group: 'series',
        href: href.series(r.id),
        primary: r.name,
        worksCount: r.works,
      })
    }
  }
  return [...exact, ...works, ...people, ...series]
}

const GROUP_LABEL: Record<Option['group'], string> = {
  exact: 'Exact match',
  work: 'Works',
  person: 'People',
  series: 'Series',
}

export default function SearchBox({ examples = [], autoFocus = false }: Props) {
  const [query, setQuery] = useState('')
  const [options, setOptions] = useState<Option[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState(false)
  const [open, setOpen] = useState(false)
  const [activeIndex, setActiveIndex] = useState(-1)

  const listboxId = useId()
  const inputRef = useRef<HTMLInputElement>(null)
  const rootRef = useRef<HTMLDivElement>(null)

  const trimmed = query.trim()
  const hasQuery = trimmed.length >= MIN_CHARS

  // Debounced fetch (+ optional exact ASIN/ISBN lookup), cancelling stale calls.
  useEffect(() => {
    if (!hasQuery) {
      setOptions([])
      setLoading(false)
      setError(false)
      setActiveIndex(-1)
      return
    }
    const ctrl = new AbortController()
    setLoading(true)
    setError(false)
    const timer = setTimeout(async () => {
      try {
        const wantLookup = looksLikeAsin(trimmed) || looksLikeIsbn(trimmed)
        const lookupPromise: Promise<LookupResponse | null> = wantLookup
          ? looksLikeAsin(trimmed)
            ? lookup('asin', trimmed, ctrl.signal)
            : lookup('isbn', normaliseIsbn(trimmed), ctrl.signal)
          : Promise.resolve(null)

        const [res, pinned] = await Promise.all([
          search(trimmed, 20, ctrl.signal),
          lookupPromise.catch(() => null),
        ])
        if (ctrl.signal.aborted) return
        setOptions(resultsToOptions(res.results ?? [], pinned))
        setActiveIndex(-1)
        setLoading(false)
      } catch (err) {
        if (ctrl.signal.aborted || (err as Error).name === 'AbortError') return
        setError(true)
        setOptions([])
        setLoading(false)
      }
    }, DEBOUNCE_MS)
    return () => {
      ctrl.abort()
      clearTimeout(timer)
    }
  }, [trimmed, hasQuery])

  // Close on outside click.
  useEffect(() => {
    function onDown(e: MouseEvent) {
      if (rootRef.current && !rootRef.current.contains(e.target as Node)) {
        setOpen(false)
      }
    }
    document.addEventListener('mousedown', onDown)
    return () => document.removeEventListener('mousedown', onDown)
  }, [])

  const navigate = useCallback((to: string) => {
    window.location.href = to
  }, [])

  const showPanel = open && hasQuery
  const activeId = activeIndex >= 0 ? `${listboxId}-opt-${activeIndex}` : undefined

  const onKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Escape') {
      setOpen(false)
      return
    }
    if (!showPanel || options.length === 0) {
      if (e.key === 'ArrowDown' && hasQuery) setOpen(true)
      return
    }
    if (e.key === 'ArrowDown') {
      e.preventDefault()
      setActiveIndex((i) => (i + 1) % options.length)
    } else if (e.key === 'ArrowUp') {
      e.preventDefault()
      setActiveIndex((i) => (i <= 0 ? options.length - 1 : i - 1))
    } else if (e.key === 'Enter') {
      if (activeIndex >= 0 && activeIndex < options.length) {
        e.preventDefault()
        navigate(options[activeIndex].href)
      }
    } else if (e.key === 'Home') {
      e.preventDefault()
      setActiveIndex(0)
    } else if (e.key === 'End') {
      e.preventDefault()
      setActiveIndex(options.length - 1)
    }
  }

  // Scroll the active option into view within the list.
  useEffect(() => {
    if (activeIndex < 0) return
    const el = document.getElementById(`${listboxId}-opt-${activeIndex}`)
    el?.scrollIntoView({ block: 'nearest' })
  }, [activeIndex, listboxId])

  // Render grouped: walk options, emitting a header when the group changes.
  const rows = useMemo(() => {
    const out: { header?: string; opt?: Option; index: number }[] = []
    let lastGroup: Option['group'] | null = null
    options.forEach((opt, index) => {
      if (opt.group !== lastGroup) {
        out.push({ header: GROUP_LABEL[opt.group], index: -1 })
        lastGroup = opt.group
      }
      out.push({ opt, index })
    })
    return out
  }, [options])

  return (
    <div ref={rootRef} className="relative w-full">
      {/* eslint-disable-next-line jsx-a11y/role-has-required-aria-props */}
      <div className="relative">
        <span className="pointer-events-none absolute left-4 top-1/2 -translate-y-1/2 text-dim">
          <svg
            className="h-5 w-5"
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
          ref={inputRef}
          type="text"
          role="combobox"
          aria-expanded={showPanel}
          aria-controls={listboxId}
          aria-activedescendant={activeId}
          aria-autocomplete="list"
          aria-label="Search the audiobook database"
          autoComplete="off"
          spellCheck={false}
          autoFocus={autoFocus}
          value={query}
          placeholder="Search by title, author, narrator, ASIN or ISBN..."
          onChange={(e) => {
            setQuery(e.target.value)
            setOpen(true)
          }}
          onFocus={() => hasQuery && setOpen(true)}
          onKeyDown={onKeyDown}
          className="w-full rounded-xl border border-edge bg-surface py-4 pl-12 pr-12 text-base text-hi placeholder:text-dim shadow-lg shadow-black/20 transition-colors focus:border-pink-500/60 focus:outline-none sm:text-lg"
        />
        {query ? (
          <button
            type="button"
            aria-label="Clear search"
            onClick={() => {
              setQuery('')
              setOpen(false)
              inputRef.current?.focus()
            }}
            className="absolute right-3 top-1/2 flex h-8 w-8 -translate-y-1/2 items-center justify-center rounded-md text-dim transition-colors hover:text-hi"
          >
            <svg
              className="h-5 w-5"
              xmlns="http://www.w3.org/2000/svg"
              fill="none"
              viewBox="0 0 24 24"
              strokeWidth={1.5}
              stroke="currentColor"
              aria-hidden="true"
            >
              <path strokeLinecap="round" strokeLinejoin="round" d="M6 18 18 6M6 6l12 12" />
            </svg>
          </button>
        ) : null}
      </div>

      {showPanel ? (
        /* z-50 + a fully opaque background (bg-surface is solid #161f2c, no
           alpha) so nothing in the hero - waveform bars, glow - can ever show
           through or paint over the open results panel. */
        <div className="absolute left-0 right-0 top-full z-50 mt-2 overflow-hidden rounded-xl border border-edge bg-surface shadow-2xl shadow-black/50">
          <ul
            id={listboxId}
            role="listbox"
            aria-label="Search results"
            className="max-h-[65vh] overflow-y-auto py-1"
          >
            {loading && options.length === 0 ? (
              <li className="px-4 py-6 text-center text-sm text-dim" role="presentation">
                Searching...
              </li>
            ) : null}

            {!loading && error ? (
              <li className="px-4 py-6 text-center text-sm text-dim" role="presentation">
                Search is unavailable right now. Please try again in a moment.
              </li>
            ) : null}

            {!loading && !error && options.length === 0 ? (
              <li className="px-4 py-5 text-center" role="presentation">
                <p className="text-sm text-dim">
                  No matches for &ldquo;{trimmed}&rdquo;.
                </p>
                <p className="mt-1 text-sm font-medium text-hi">Not in the database yet.</p>
                <div className="mt-3 flex flex-wrap items-center justify-center gap-x-4 gap-y-1.5">
                  <a
                    href={addWorkFromQueryUrl(trimmed)}
                    target="_blank"
                    rel="noopener"
                    className="inline-flex items-center gap-1.5 rounded-lg bg-pink-600 px-3 py-1.5 text-sm font-medium text-white shadow-lg shadow-pink-600/20 transition-colors hover:bg-pink-500"
                  >
                    <svg
                      className="h-4 w-4"
                      xmlns="http://www.w3.org/2000/svg"
                      fill="none"
                      viewBox="0 0 24 24"
                      strokeWidth={1.5}
                      stroke="currentColor"
                      aria-hidden="true"
                    >
                      <path strokeLinecap="round" strokeLinejoin="round" d="M12 4.5v15m7.5-7.5h-15" />
                    </svg>
                    Add this book
                  </a>
                  <a
                    href="/contribute"
                    className="text-xs text-dim underline-offset-2 transition-colors hover:text-pink-300 hover:underline"
                  >
                    or see what else is needed
                  </a>
                </div>
              </li>
            ) : null}

            {rows.map((row) =>
              row.header ? (
                <li
                  key={`h-${row.header}`}
                  role="presentation"
                  className="px-4 pb-1 pt-2 text-[0.7rem] font-semibold uppercase tracking-wider text-dim"
                >
                  {row.header}
                </li>
              ) : (
                <li
                  key={row.opt!.key}
                  id={`${listboxId}-opt-${row.index}`}
                  role="option"
                  aria-selected={row.index === activeIndex}
                  data-active={row.index === activeIndex}
                  onMouseEnter={() => setActiveIndex(row.index)}
                  onMouseDown={(e) => {
                    // Prevent the input blur so navigation still fires.
                    e.preventDefault()
                    navigate(row.opt!.href)
                  }}
                  className="search-option flex cursor-pointer items-center gap-3 px-3 py-2 text-sm"
                >
                  {renderOption(row.opt!, trimmed)}
                </li>
              )
            )}
          </ul>
        </div>
      ) : null}

      {examples.length > 0 ? (
        <div className="mt-4 flex flex-wrap items-center justify-center gap-2">
          <span className="text-xs text-dim">Try</span>
          {examples.map((ex) => (
            <button
              key={ex}
              type="button"
              onClick={() => {
                setQuery(ex)
                setOpen(true)
                inputRef.current?.focus()
              }}
              className="rounded-full border border-edge bg-surface px-3 py-1 text-xs text-body transition-colors hover:border-pink-500/50 hover:text-pink-300"
            >
              {ex}
            </button>
          ))}
        </div>
      ) : null}
    </div>
  )
}

function renderOption(opt: Option, q: string) {
  if (opt.group === 'exact' || opt.group === 'work') {
    return (
      <>
        <div className="h-10 w-10 shrink-0 overflow-hidden rounded border border-edge bg-raised">
          {opt.coverUrl ? (
            // eslint-disable-next-line jsx-a11y/alt-text
            <img
              src={opt.coverUrl}
              alt=""
              loading="lazy"
              className="h-full w-full object-cover"
            />
          ) : (
            <div className="flex h-full w-full items-center justify-center text-edge">
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
                  d="M12 6.042A8.967 8.967 0 0 0 6 3.75c-1.052 0-2.062.18-3 .512v14.25A8.987 8.987 0 0 1 6 18c2.305 0 4.408.867 6 2.292m0-14.25a8.966 8.966 0 0 1 6-2.292c1.052 0 2.062.18 3 .512v14.25A8.987 8.987 0 0 0 18 18a8.967 8.967 0 0 0-6 2.292m0-14.25v14.25"
                />
              </svg>
            </div>
          )}
        </div>
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <span className="truncate font-medium text-hi">{highlight(opt.primary, q)}</span>
            {opt.exact ? (
              <span className="shrink-0 rounded-full bg-pink-600/15 px-2 py-0.5 text-[0.65rem] font-semibold text-pink-300">
                Exact match
              </span>
            ) : null}
          </div>
          {opt.secondary ? (
            <div className="truncate text-xs text-dim">{highlight(opt.secondary, q)}</div>
          ) : null}
        </div>
        {opt.seriesName ? (
          <span className="hidden shrink-0 items-center gap-1 rounded-full border border-edge bg-raised px-2 py-0.5 text-[0.7rem] text-dim sm:inline-flex">
            <span className="max-w-[10rem] truncate">{opt.seriesName}</span>
            {opt.seriesPosition ? (
              <span className="font-semibold text-pink-400">#{opt.seriesPosition}</span>
            ) : null}
          </span>
        ) : null}
      </>
    )
  }
  if (opt.group === 'person') {
    return (
      <>
        <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full border border-edge bg-raised text-dim">
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
              d="M15.75 6a3.75 3.75 0 1 1-7.5 0 3.75 3.75 0 0 1 7.5 0ZM4.501 20.118a7.5 7.5 0 0 1 14.998 0A17.933 17.933 0 0 1 12 21.75c-2.676 0-5.216-.584-7.499-1.632Z"
            />
          </svg>
        </span>
        <span className="truncate font-medium text-hi">{highlight(opt.primary, q)}</span>
        <span className="ml-auto shrink-0 text-xs text-dim">Person</span>
      </>
    )
  }
  return (
    <>
      <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full border border-edge bg-raised text-dim">
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
            d="M3.75 12h16.5m-16.5 3.75h16.5M3.75 19.5h16.5M5.625 4.5h12.75a1.875 1.875 0 0 1 0 3.75H5.625a1.875 1.875 0 0 1 0-3.75Z"
          />
        </svg>
      </span>
      <span className="truncate font-medium text-hi">{highlight(opt.primary, q)}</span>
      <span className="ml-auto shrink-0 text-xs text-dim">
        {opt.worksCount != null ? `${opt.worksCount} works` : 'Series'}
      </span>
    </>
  )
}
