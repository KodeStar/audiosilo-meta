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
import { Icon } from '../ui'

interface Props {
  /** Quiet example queries shown below the box; a tap fills and runs a search. */
  examples?: string[]
  autoFocus?: boolean
  /** Header dressing: a shorter field and a shorter placeholder. The behaviour,
      the tabs and the results are identical - only the chrome shrinks. */
  compact?: boolean
}

type Kind = 'work' | 'person' | 'series'
type Tab = 'all' | Kind

/** A flattened, navigable option. */
interface Option {
  key: string
  group: Kind
  href: string
  primary: string
  secondary?: string
  coverUrl?: string | null
  seriesName?: string
  seriesPosition?: string
  worksCount?: number
  /** An ASIN/ISBN lookup hit: pinned to the top of every tab. */
  exact?: boolean
}

/** Results bucketed by kind. The API ranks across kinds (bm25), so a query like
    "Stephen Fry" returns 18 works, 1 person and 1 series interleaved - which
    buried the person and the series below the fold. Bucketing lets the tab bar
    report each kind's count and lets the reader jump straight to one. */
interface Grouped {
  exact: Option | null
  work: Option[]
  person: Option[]
  series: Option[]
}

const EMPTY: Grouped = { exact: null, work: [], person: [], series: [] }
const KINDS: Kind[] = ['work', 'person', 'series']
const TAB_LABEL: Record<Tab, string> = {
  all: 'All',
  work: 'Works',
  person: 'People',
  series: 'Series',
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

function resultsToGroups(results: SearchResult[], pinned: LookupResponse | null): Grouped {
  const out: Grouped = { exact: null, work: [], person: [], series: [] }

  if (pinned) {
    const w = pinned.work
    out.exact = {
      key: `exact-${w.id}`,
      group: 'work',
      href: href.work(w.id),
      primary: w.title,
      secondary: w.authors ? personNames(w.authors) : undefined,
      coverUrl: w.cover_url,
      seriesName: w.series?.name,
      seriesPosition: w.series?.position,
      exact: true,
    }
  }
  for (const r of results) {
    if (r.kind === 'work') {
      // Avoid duplicating the pinned exact match.
      if (pinned && r.id === pinned.work.id) continue
      out.work.push({
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
      out.person.push({
        key: `person-${r.id}`,
        group: 'person',
        href: href.person(r.id),
        primary: r.name,
      })
    } else {
      out.series.push({
        key: `series-${r.id}`,
        group: 'series',
        href: href.series(r.id),
        primary: r.name,
        worksCount: r.works,
      })
    }
  }
  return out
}

export default function SearchBox({ examples = [], autoFocus = false, compact = false }: Props) {
  const [query, setQuery] = useState('')
  const [groups, setGroups] = useState<Grouped>(EMPTY)
  const [tab, setTab] = useState<Tab>('all')
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
      setGroups(EMPTY)
      setLoading(false)
      setError(false)
      setActiveIndex(-1)
      setTab('all')
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
        setGroups(resultsToGroups(res.results ?? [], pinned))
        setActiveIndex(-1)
        setLoading(false)
      } catch (err) {
        if (ctrl.signal.aborted || (err as Error).name === 'AbortError') return
        setError(true)
        setGroups(EMPTY)
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

  const counts = useMemo(
    () => ({
      work: groups.work.length,
      person: groups.person.length,
      series: groups.series.length,
    }),
    [groups]
  )
  const total = counts.work + counts.person + counts.series

  /** Only kinds that actually matched get a tab - never an empty "People 0".
      With a single matching kind the "All" tab would duplicate it, so the bar
      collapses to that one count. */
  const tabs = useMemo<Tab[]>(() => {
    const present = KINDS.filter((k) => counts[k] > 0)
    return present.length > 1 ? ['all', ...present] : present
  }, [counts])

  // A new query can empty the kind we were filtered to; fall back to All rather
  // than showing a correct-but-baffling empty list.
  useEffect(() => {
    if (tab !== 'all' && counts[tab] === 0) setTab('all')
  }, [tab, counts])

  const visible = useMemo(
    () => (tab === 'all' ? [...groups.work, ...groups.person, ...groups.series] : groups[tab]),
    [tab, groups]
  )

  /** Keyboard order = what is on screen: the pinned exact match, then the
      current tab's rows. Every option id below indexes into THIS array. */
  const nav = useMemo(
    () => (groups.exact ? [groups.exact, ...visible] : visible),
    [groups.exact, visible]
  )

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
    if (!showPanel || nav.length === 0) {
      if (e.key === 'ArrowDown' && hasQuery) setOpen(true)
      return
    }
    if (e.key === 'ArrowDown') {
      e.preventDefault()
      setActiveIndex((i) => (i + 1) % nav.length)
    } else if (e.key === 'ArrowUp') {
      e.preventDefault()
      setActiveIndex((i) => (i <= 0 ? nav.length - 1 : i - 1))
    } else if (e.key === 'Enter') {
      if (activeIndex >= 0 && activeIndex < nav.length) {
        e.preventDefault()
        navigate(nav[activeIndex].href)
      }
    } else if (e.key === 'Home') {
      e.preventDefault()
      setActiveIndex(0)
    } else if (e.key === 'End') {
      e.preventDefault()
      setActiveIndex(nav.length - 1)
    }
  }

  const selectTab = (next: Tab) => {
    setTab(next)
    setActiveIndex(-1)
    inputRef.current?.focus()
  }

  // Scroll the active option into view within the list.
  useEffect(() => {
    if (activeIndex < 0) return
    const el = document.getElementById(`${listboxId}-opt-${activeIndex}`)
    el?.scrollIntoView({ block: 'nearest' })
  }, [activeIndex, listboxId])

  /** In the All tab the kinds run together, so mark where one ends and the next
      begins with a hairline - the tab bar says WHAT is in the list, the rule
      says where the boundary falls. A filtered tab is one kind, so no rules. */
  const rows = useMemo(() => {
    const offset = groups.exact ? 1 : 0
    let last: Kind | null = null
    return visible.map((opt, i) => {
      const divider = tab === 'all' && last !== null && opt.group !== last
      last = opt.group
      return { opt, index: i + offset, divider }
    })
  }, [visible, tab, groups.exact])

  return (
    <div ref={rootRef} className="relative w-full">
      {/* eslint-disable-next-line jsx-a11y/role-has-required-aria-props */}
      <div className="relative">
        <span
          className={`pointer-events-none absolute top-1/2 -translate-y-1/2 text-dim ${compact ? 'left-3' : 'left-4'}`}
        >
          <svg
            className={compact ? 'h-4 w-4' : 'h-5 w-5'}
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
          placeholder={
            compact ? 'Search books, narrators...' : 'Search by title, author, narrator, ASIN or ISBN...'
          }
          onChange={(e) => {
            setQuery(e.target.value)
            setOpen(true)
          }}
          onFocus={() => hasQuery && setOpen(true)}
          onKeyDown={onKeyDown}
          className={`w-full border border-edge bg-surface text-hi placeholder:text-dim transition-colors focus:border-pink-500/60 focus:outline-none ${
            compact
              ? 'rounded-lg py-2 pl-9 pr-9 text-sm'
              : 'rounded-xl py-4 pl-12 pr-12 text-base shadow-lg shadow-black/20 sm:text-lg'
          }`}
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
            className={`absolute top-1/2 flex -translate-y-1/2 items-center justify-center rounded-md text-dim transition-colors hover:text-hi ${
              compact ? 'right-1.5 h-7 w-7' : 'right-3 h-8 w-8'
            }`}
          >
            <svg
              className={compact ? 'h-4 w-4' : 'h-5 w-5'}
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
           through or paint over the open results panel.
           text-left is load-bearing: the hero centres its content, and without
           it every secondary line in here inherits that and floats mid-row. */
        <div className="absolute left-0 right-0 top-full z-50 mt-2 overflow-hidden rounded-xl border border-edge bg-surface text-left shadow-2xl shadow-black/50">
          {tabs.length > 0 ? (
            <div
              role="group"
              aria-label="Filter results by kind"
              className="flex items-center gap-1 border-b border-edge px-2 py-1.5"
            >
              {tabs.map((t) => {
                const n = t === 'all' ? total : counts[t]
                const on = t === tab
                return (
                  <button
                    key={t}
                    type="button"
                    aria-pressed={on}
                    /* Keep the caret in the input so the reader can carry on
                       typing after narrowing the list. */
                    onMouseDown={(e) => e.preventDefault()}
                    onClick={() => selectTab(t)}
                    className={`flex items-center gap-1.5 rounded-lg px-2.5 py-1 text-xs font-medium transition-colors ${
                      on ? 'bg-raised text-hi' : 'text-dim hover:bg-raised/60 hover:text-body'
                    }`}
                  >
                    {TAB_LABEL[t]}
                    <span
                      className={`tabular-nums text-[0.7rem] ${on ? 'text-pink-400' : 'text-dim/70'}`}
                    >
                      {n}
                    </span>
                  </button>
                )
              })}
            </div>
          ) : null}

          <ul
            id={listboxId}
            role="listbox"
            aria-label="Search results"
            className="max-h-[60vh] overflow-y-auto py-1"
          >
            {loading && total === 0 ? (
              <li className="px-4 py-6 text-center text-sm text-dim" role="presentation">
                Searching...
              </li>
            ) : null}

            {!loading && error ? (
              <li className="px-4 py-6 text-center text-sm text-dim" role="presentation">
                Search is unavailable right now. Please try again in a moment.
              </li>
            ) : null}

            {!loading && !error && total === 0 && !groups.exact ? (
              <li className="px-4 py-5 text-center" role="presentation">
                <p className="text-sm text-dim">No matches for &ldquo;{trimmed}&rdquo;.</p>
                <p className="mt-1 text-sm font-medium text-hi">Not in the database yet.</p>
                <div className="mt-3 flex flex-wrap items-center justify-center gap-x-4 gap-y-1.5">
                  <a
                    href={addWorkFromQueryUrl(trimmed)}
                    target="_blank"
                    rel="noopener"
                    className="inline-flex items-center gap-1.5 rounded-lg bg-pink-600 px-3 py-1.5 text-sm font-medium text-white shadow-lg shadow-pink-600/20 transition-colors hover:bg-pink-500"
                  >
                    <Icon name="plus" className="h-4 w-4" />
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

            {groups.exact ? (
              <Row
                opt={groups.exact}
                q={trimmed}
                id={`${listboxId}-opt-0`}
                index={0}
                active={activeIndex === 0}
                onActivate={setActiveIndex}
                onChoose={navigate}
                compact={compact}
                className="border-b border-edge"
              />
            ) : null}

            {rows.map((row) => (
              <Row
                key={row.opt.key}
                opt={row.opt}
                q={trimmed}
                id={`${listboxId}-opt-${row.index}`}
                index={row.index}
                active={row.index === activeIndex}
                onActivate={setActiveIndex}
                onChoose={navigate}
                compact={compact}
                className={row.divider ? 'mt-1 border-t border-edge/70 pt-1' : undefined}
              />
            ))}
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

interface RowProps {
  opt: Option
  q: string
  id: string
  index: number
  active: boolean
  /** Narrow panel: drop the series pill so the title keeps the width. */
  compact?: boolean
  onActivate: (index: number) => void
  onChoose: (href: string) => void
  className?: string
}

/** One result. Every kind uses the SAME geometry - a 40px leading slot, a title
    line, a secondary line, an optional right-hand chip - so titles share one
    left edge and rows share one height all the way down the list. */
function Row({ opt, q, id, index, active, onActivate, onChoose, className, compact }: RowProps) {
  /* The secondary line is rendered only when the record actually carries one.
     A person result is just {id, name} - it states no role, and this repo does
     not invent facts, so nothing goes here rather than a guessed "narrator". */
  const sub =
    opt.group === 'work'
      ? opt.secondary
        ? highlight(opt.secondary, q)
        : null
      : opt.group === 'series' && opt.worksCount != null
        ? `${opt.worksCount} ${opt.worksCount === 1 ? 'work' : 'works'}`
        : null

  return (
    <li
      id={id}
      role="option"
      aria-selected={active}
      data-active={active}
      onMouseEnter={() => onActivate(index)}
      onMouseDown={(e) => {
        // Prevent the input blur so navigation still fires.
        e.preventDefault()
        onChoose(opt.href)
      }}
      className={`search-option flex cursor-pointer items-center gap-3 px-3 py-2 ${className ?? ''}`}
    >
      <Thumb opt={opt} />
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2">
          <span className="truncate text-sm font-medium text-hi">{highlight(opt.primary, q)}</span>
          {opt.exact ? (
            <span className="shrink-0 rounded-full bg-pink-600/15 px-2 py-0.5 text-[0.65rem] font-semibold text-pink-300">
              Exact match
            </span>
          ) : null}
        </div>
        {sub ? <div className="search-sub truncate text-xs text-dim">{sub}</div> : null}
      </div>
      {opt.seriesName && !compact ? (
        <span className="hidden shrink-0 items-center gap-1 rounded-full border border-edge bg-raised px-2 py-0.5 text-[0.7rem] text-dim sm:inline-flex">
          <span className="max-w-[10rem] truncate">{opt.seriesName}</span>
          {opt.seriesPosition ? (
            <span className="font-semibold text-pink-400">#{opt.seriesPosition}</span>
          ) : null}
        </span>
      ) : null}
    </li>
  )
}

/** The 40px leading slot. A cover for works; a kind glyph for people and
    series, which is what tells them apart in the unfiltered All tab. */
function Thumb({ opt }: { opt: Option }) {
  if (opt.group === 'work') {
    return (
      <div className="h-10 w-10 shrink-0 overflow-hidden rounded border border-edge bg-raised">
        {opt.coverUrl ? (
          // eslint-disable-next-line jsx-a11y/alt-text
          <img src={opt.coverUrl} alt="" loading="lazy" className="h-full w-full object-cover" />
        ) : (
          <div className="flex h-full w-full items-center justify-center text-edge">
            <BookGlyph />
          </div>
        )}
      </div>
    )
  }
  const round = opt.group === 'person' ? 'rounded-full' : 'rounded'
  return (
    <span
      className={`flex h-10 w-10 shrink-0 items-center justify-center border border-edge bg-raised text-dim ${round}`}
    >
      {opt.group === 'person' ? <PersonGlyph /> : <SeriesGlyph />}
    </span>
  )
}

function BookGlyph() {
  return (
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
  )
}

function PersonGlyph() {
  return (
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
  )
}

function SeriesGlyph() {
  return (
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
  )
}
