import { useCallback, useEffect, useState, type ReactNode } from 'react'
import {
  getWork,
  getSeries,
  getChapters,
  formatRuntime,
  formatYear,
  formatOffset,
  formatLanguage,
  href,
  type Work,
  type Recording,
  type Chapter,
  type Series,
  type Character,
} from '../../lib/api'
import { roleLabel, revealLabel, storyRows, type StoryRow as StoryRowData } from '../../lib/expressive'
import {
  seriesNeighbors,
  hashForTab,
  tabFromHash,
  type SeriesNeighbors,
  type SeriesWork,
  type WorkTab,
} from '../../lib/worknav'
import CoverImage from '../cards/CoverImage'
import PersonLinks from '../cards/PersonLinks'
import {
  useQueryParam,
  usePageTitle,
  useEntity,
  DetailSpinner,
  DetailError,
  BackLink,
} from './detail-common'

/** The shared disclosure chevron for the page's accordions (chapters, characters,
    recaps). Points down when closed and rotates 180deg when open. The optional
    className carries extra layout utilities (e.g. shrink-0 in a flex row). */
function Chevron({ open, className }: { open: boolean; className?: string }) {
  return (
    <svg
      className={`h-4 w-4 text-dim transition-transform ${open ? 'rotate-180' : ''}${className ? ` ${className}` : ''}`}
      xmlns="http://www.w3.org/2000/svg"
      fill="none"
      viewBox="0 0 24 24"
      strokeWidth={1.5}
      stroke="currentColor"
      aria-hidden="true"
    >
      <path strokeLinecap="round" strokeLinejoin="round" d="m19.5 8.25-7.5 7.5-7.5-7.5" />
    </svg>
  )
}

function CopyChip({ label, value }: { label: string; value: string }) {
  const [copied, setCopied] = useState(false)
  const onClick = useCallback(() => {
    navigator.clipboard?.writeText(value).then(
      () => {
        setCopied(true)
        setTimeout(() => setCopied(false), 1400)
      },
      () => {
        /* clipboard blocked - the value is still visible on the chip */
      }
    )
  }, [value])
  return (
    <button
      type="button"
      onClick={onClick}
      title={`Copy ${value}`}
      className="group inline-flex items-center gap-1.5 rounded-md border border-edge bg-raised px-2 py-1 font-mono text-xs text-body transition-colors hover:border-pink-500/50 hover:text-hi"
    >
      {label ? <span className="text-[0.65rem] uppercase text-dim">{label}</span> : null}
      <span>{value}</span>
      {copied ? (
        <svg
          className="h-3.5 w-3.5 text-success"
          xmlns="http://www.w3.org/2000/svg"
          fill="none"
          viewBox="0 0 24 24"
          strokeWidth={2}
          stroke="currentColor"
          aria-hidden="true"
        >
          <path strokeLinecap="round" strokeLinejoin="round" d="m4.5 12.75 6 6 9-13.5" />
        </svg>
      ) : (
        <svg
          className="h-3.5 w-3.5 text-dim group-hover:text-pink-400"
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
            d="M9 9.75A2.25 2.25 0 0 1 11.25 7.5h6A2.25 2.25 0 0 1 19.5 9.75v6A2.25 2.25 0 0 1 17.25 18h-6A2.25 2.25 0 0 1 9 15.75v-6Z M6.75 15A2.25 2.25 0 0 1 4.5 12.75v-6A2.25 2.25 0 0 1 6.75 4.5h6A2.25 2.25 0 0 1 15 6.75"
          />
        </svg>
      )}
      <span className="sr-only">{copied ? 'Copied' : 'Copy'}</span>
    </button>
  )
}

function MetaItem({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-baseline gap-2">
      <span className="text-xs uppercase tracking-wider text-dim">{label}</span>
      <span className="text-sm text-body">{value}</span>
    </div>
  )
}

function ChapterList({ workId, recording }: { workId: string; recording: Recording }) {
  const [open, setOpen] = useState(false)
  const [chapters, setChapters] = useState<Chapter[] | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState(false)

  const toggle = useCallback(() => {
    const next = !open
    setOpen(next)
    if (next && chapters === null && !loading) {
      setLoading(true)
      setError(false)
      getChapters(workId, recording.id)
        .then((res) => setChapters(res.chapters ?? []))
        .catch(() => setError(true))
        .finally(() => setLoading(false))
    }
  }, [open, chapters, loading, workId, recording.id])

  const count = recording.chapter_count
  if (!count && chapters === null) return null

  return (
    <div className="mt-4 border-t border-edge pt-3">
      <button
        type="button"
        onClick={toggle}
        aria-expanded={open}
        className="flex w-full items-center justify-between text-left text-sm font-medium text-body transition-colors hover:text-hi"
      >
        <span>
          {count ? `${count} chapters` : 'Chapters'}
        </span>
        <Chevron open={open} />
      </button>
      {open ? (
        <div className="mt-3">
          {loading ? (
            <p className="text-sm text-dim">Loading chapters...</p>
          ) : error ? (
            <p className="text-sm text-dim">Chapters could not be loaded.</p>
          ) : chapters && chapters.length > 0 ? (
            <ol className="divide-y divide-edge/60 overflow-hidden rounded-lg border border-edge">
              {chapters.map((c, i) => (
                <li
                  key={i}
                  className="flex items-center justify-between gap-4 bg-raised/40 px-3 py-2 text-sm"
                >
                  <span className="flex min-w-0 items-center gap-3">
                    <span className="w-6 shrink-0 text-right font-mono text-xs text-dim">
                      {i + 1}
                    </span>
                    <span className="truncate text-body">{c.title}</span>
                  </span>
                  <span className="shrink-0 font-mono text-xs text-dim">
                    {formatOffset(c.start_ms)}
                  </span>
                </li>
              ))}
            </ol>
          ) : (
            <p className="text-sm text-dim">No chapter list is available.</p>
          )}
        </div>
      ) : null}
    </div>
  )
}

function RecordingCard({ workId, recording }: { workId: string; recording: Recording }) {
  const runtime = formatRuntime(recording.runtime_min)
  const year = formatYear(recording.release_date)
  return (
    <article className="rounded-2xl border border-edge bg-surface p-5">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <h3 className="text-sm font-medium text-hi">
            {recording.narrators && recording.narrators.length > 0 ? (
              <>
                <span className="text-dim">Narrated by </span>
                <PersonLinks people={recording.narrators} />
              </>
            ) : (
              <span className="text-dim">Narrator unknown</span>
            )}
          </h3>
        </div>
        {recording.abridged ? (
          <span className="shrink-0 rounded-full border border-edge bg-raised px-2.5 py-0.5 text-xs text-dim">
            Abridged
          </span>
        ) : null}
      </div>

      <div className="mt-4 flex flex-wrap gap-x-6 gap-y-2">
        {runtime ? <MetaItem label="Runtime" value={runtime} /> : null}
        {recording.release_date ? (
          <MetaItem label="Released" value={year ?? recording.release_date} />
        ) : null}
        {recording.publisher ? <MetaItem label="Publisher" value={recording.publisher} /> : null}
      </div>

      {(recording.asin && recording.asin.length > 0) ||
      (recording.isbn && recording.isbn.length > 0) ? (
        <div className="mt-4 flex flex-wrap gap-2">
          {recording.asin?.map((a) => (
            <CopyChip key={`${a.region}-${a.asin}`} label={a.region} value={a.asin} />
          ))}
          {recording.isbn?.map((isbn) => (
            <CopyChip key={isbn} label="ISBN" value={isbn} />
          ))}
        </div>
      ) : null}

      <ChapterList workId={workId} recording={recording} />
    </article>
  )
}

/** Sidebar metadata: renders only what exists (facts-only, no placeholders). */
function MetadataBlock({ work }: { work: Work }) {
  const language = formatLanguage(work.language)
  const year = formatYear(work.first_published)
  const xrefLinks = [
    work.xrefs?.wikidata && {
      label: 'Wikidata',
      href: `https://www.wikidata.org/wiki/${encodeURIComponent(work.xrefs.wikidata)}`,
    },
    work.xrefs?.openlibrary && {
      label: 'Open Library',
      href: `https://openlibrary.org/works/${encodeURIComponent(work.xrefs.openlibrary)}`,
    },
    work.xrefs?.goodreads && {
      label: 'Goodreads',
      href: `https://www.goodreads.com/book/show/${encodeURIComponent(work.xrefs.goodreads)}`,
    },
  ].filter((x): x is { label: string; href: string } => Boolean(x))

  const rows: { label: string; content: React.ReactNode }[] = []
  if (language) rows.push({ label: 'Language', content: language })
  if (work.first_published)
    rows.push({ label: 'First published', content: year ?? work.first_published })
  rows.push({ label: 'Recordings', content: String(work.recordings?.length ?? 0) })
  if (work.isbn && work.isbn.length > 0)
    rows.push({
      label: 'Print ISBN',
      content: (
        <span className="flex flex-wrap gap-1.5">
          {work.isbn.map((isbn) => (
            <CopyChip key={isbn} label="" value={isbn} />
          ))}
        </span>
      ),
    })
  if (xrefLinks.length > 0)
    rows.push({
      label: 'Elsewhere',
      content: (
        <span className="flex flex-wrap gap-x-3 gap-y-1">
          {xrefLinks.map((x) => (
            <a
              key={x.label}
              href={x.href}
              target="_blank"
              rel="noopener"
              className="text-pink-400 underline-offset-2 transition-colors hover:text-pink-300 hover:underline"
            >
              {x.label}
            </a>
          ))}
        </span>
      ),
    })

  return (
    <dl className="mt-6 space-y-3 rounded-2xl border border-edge bg-surface p-4 text-sm">
      {rows.map((row) => (
        <div key={row.label} className="flex flex-col gap-1">
          <dt className="text-xs uppercase tracking-wider text-dim">{row.label}</dt>
          <dd className="text-body">{row.content}</dd>
        </div>
      ))}
    </dl>
  )
}

/** A small uppercase pill used for character roles and recap scopes. */
function Badge({ children }: { children: ReactNode }) {
  return (
    <span className="shrink-0 rounded-full border border-edge bg-raised px-2 py-0.5 text-[0.65rem] uppercase tracking-wide text-dim">
      {children}
    </span>
  )
}

/** One character card. The description is spoiler-bounded to the reveal position
    but still story detail, so it stays hidden behind a per-card accordion the
    reader opens via the reveal-position row. The name stays a real heading in
    both branches; a card with no description has no disclosure control. */
function CharacterCard({ character }: { character: Character }) {
  const [open, setOpen] = useState(false)
  const role = roleLabel(character.role)
  const hasDescription = Boolean(character.description)
  const descId = `char-desc-${character.id}`
  const reveal = <span className="text-xs font-medium text-pink-400/90">{revealLabel(character.reveal)}</span>

  return (
    <article className="overflow-hidden rounded-2xl border border-edge bg-surface p-4">
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0">
          <h3 className="font-semibold text-hi">{character.name}</h3>
          {character.aliases && character.aliases.length > 0 ? (
            <p className="mt-0.5 text-xs text-dim">also {character.aliases.join(', ')}</p>
          ) : null}
        </div>
        {role ? <Badge>{role}</Badge> : null}
      </div>
      {hasDescription ? (
        <button
          type="button"
          onClick={() => setOpen((v) => !v)}
          aria-expanded={open}
          aria-controls={descId}
          className="mt-2 flex w-full items-center justify-between gap-2 text-left transition-colors hover:opacity-80"
        >
          {reveal}
          <Chevron open={open} className="shrink-0" />
        </button>
      ) : (
        <p className="mt-2">{reveal}</p>
      )}
      {hasDescription && open ? (
        <p id={descId} className="mt-3 text-sm leading-relaxed text-body">
          {character.description}
        </p>
      ) : null}
    </article>
  )
}

/** The cast of a work: community-authored, spoiler-aware character cards, each a
    per-card accordion so descriptions stay hidden until opened. */
function CharactersPanel({ characters }: { characters: Character[] }) {
  return (
    <>
      <p className="mt-6 max-w-2xl text-sm text-dim">
        Community-written and spoiler-aware - open a character to read who they are, scoped to where
        they first appear.
      </p>
      <div className="mt-5 grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        {characters.map((c) => (
          <CharacterCard key={c.id} character={c} />
        ))}
      </div>
    </>
  )
}

/** One "story so far" row: a collapsible accordion that stays closed (spoiler-safe)
    until the reader opens it. Shared by the position-keyed chaptered recaps and the
    whole-book summary rows ("In short" / "How did it end?"), so all three read as
    one list; the caller supplies the header title, an optional scope/kind badge,
    and the revealed body text. */
function StoryRow({ title, badge, text }: { title: string; badge?: string; text: string }) {
  const [open, setOpen] = useState(false)
  return (
    <div className="bg-surface">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        className="flex w-full items-center justify-between gap-3 px-4 py-3 text-left transition-colors hover:bg-raised/40"
      >
        <span className="flex flex-wrap items-center gap-2">
          <span className="text-sm font-medium text-body">{title}</span>
          {badge ? <Badge>{badge}</Badge> : null}
        </span>
        <Chevron open={open} className="shrink-0" />
      </button>
      {open ? <p className="px-4 pb-4 text-sm leading-relaxed text-body">{text}</p> : null}
    </div>
  )
}

/** "Story so far": the rows built by storyRows (the whole-book "In short" row
    first, the position-ordered chaptered recaps, the whole-book "How did it end?"
    row last) - every row an accordion closed by default so the reader chooses how
    far to reveal. The chaptered rows are bounded to their position; the whole-book
    summary rows are full spoilers. A work may carry only the summary (no chaptered
    recaps), in which case just the "In short"/ending rows show. */
function RecapsPanel({ rows }: { rows: StoryRowData[] }) {
  return (
    <>
      <p className="mt-6 max-w-2xl text-sm text-dim">
        Open a recap only as far as you have listened - the whole-book rows are full spoilers.
      </p>
      <div className="mt-5 divide-y divide-edge/60 overflow-hidden rounded-2xl border border-edge">
        {rows.map((row, i) => (
          <StoryRow key={`${row.title}-${i}`} {...row} />
        ))}
      </div>
    </>
  )
}

/** "More in this series": the other member works of the work's (first) series,
    as a horizontal rail of square cover cards. The series is fetched once in the
    parent and passed down (shared with the series nav), so this renders nothing
    when it is still loading, absent, or has no other members. Rail links carry
    no tab hash - the rail only renders inside the General tab, where the active
    tab's hash is always empty; the prev/next nav is the hash carrier. */
function SeriesRail({ work, series }: { work: Work; series: Series | null }) {
  if (!series) return null
  const others = (series.works ?? []).filter((entry) => entry.work.id !== work.id)
  if (others.length === 0) return null

  return (
    <section className="mt-14">
      <h2 className="text-xl font-semibold text-hi">
        More in{' '}
        <a
          href={href.series(series.id)}
          className="text-pink-400 transition-colors hover:text-pink-300"
        >
          {series.name}
        </a>
      </h2>
      <div className="mt-5 flex gap-4 overflow-x-auto pb-2">
        {others.map((entry) => (
          <a
            key={`${entry.position}-${entry.work.id}`}
            href={href.work(entry.work.id)}
            className="group w-32 shrink-0 sm:w-36"
          >
            <CoverImage
              src={entry.work.cover_url}
              alt={`Cover of ${entry.work.title}`}
              title={entry.work.title}
              className="transition-colors group-hover:border-pink-500/40"
            />
            <p className="mt-2 line-clamp-2 text-xs leading-snug text-body group-hover:text-pink-300">
              <span className="font-semibold text-pink-400">#{entry.position}</span>{' '}
              {entry.work.title}
            </p>
          </a>
        ))}
      </div>
    </section>
  )
}

/** One side (previous / next) of the series nav: a compact link to a neighbouring
    volume, carrying the active tab hash so flipping stays on the current tab. The
    title truncates so a long name never blows out the row on a phone. `dir`
    orients the chevron and content (previous points left, next right). */
function SeriesNavLink({ entry, dir, tabHash }: { entry: SeriesWork; dir: 'prev' | 'next'; tabHash: string }) {
  const chevron = (
    <span key="chevron" aria-hidden="true" className="shrink-0 text-base leading-none text-dim group-hover:text-pink-400">
      {dir === 'prev' ? '‹' : '›'}
    </span>
  )
  const position = <span key="position" className="shrink-0 text-sm font-semibold text-pink-400">#{entry.position}</span>
  const title = (
    <span key="title" className={`min-w-0 truncate text-sm text-body group-hover:text-pink-300${dir === 'next' ? ' text-right' : ''}`}>
      {entry.work.title}
    </span>
  )
  return (
    <a
      href={`${href.work(entry.work.id)}${tabHash}`}
      aria-label={`${dir === 'prev' ? 'Previous' : 'Next'} in series: ${entry.work.title} (#${entry.position})`}
      className={`group flex min-w-0 flex-1 items-center gap-2 rounded-xl border border-edge bg-surface px-3 py-2 transition-colors hover:border-pink-500/50 ${
        dir === 'next' ? 'justify-end' : ''
      }`}
    >
      {dir === 'prev' ? [chevron, position, title] : [title, position, chevron]}
    </a>
  )
}

/** Series prev/next navigation, visible above the tab bar (so it stays on every
    tab, and shows for works with no tabs). While the series fetch is pending it
    renders a same-height skeleton row (the work is known to be in a series, so
    the nav will almost always fill in - without the reservation the tab bar and
    body would shift down when it does). Renders nothing when the work has no
    series or no neighbours; a one-sided result keeps its slot so the present link
    stays on its own side of the row. */
function SeriesNav({ prev, next, tabHash, pending }: SeriesNeighbors & { tabHash: string; pending: boolean }) {
  if (pending) {
    return (
      <div aria-hidden="true" className="mt-4 flex items-stretch gap-3">
        <span className="flex flex-1 items-center rounded-xl border border-edge/40 bg-surface/40 px-3 py-2">
          <span className="invisible text-sm">#</span>
        </span>
        <span className="flex flex-1 items-center rounded-xl border border-edge/40 bg-surface/40 px-3 py-2">
          <span className="invisible text-sm">#</span>
        </span>
      </div>
    )
  }
  if (!prev && !next) return null
  return (
    <nav aria-label="Series navigation" className="mt-4 flex items-stretch gap-3">
      {prev ? (
        <SeriesNavLink entry={prev} dir="prev" tabHash={tabHash} />
      ) : (
        <span className="flex-1" aria-hidden="true" />
      )}
      {next ? (
        <SeriesNavLink entry={next} dir="next" tabHash={tabHash} />
      ) : (
        <span className="flex-1" aria-hidden="true" />
      )}
    </nav>
  )
}

/** The community hook: every work page links straight to its source file. */
function ImproveRecord({ id }: { id: string }) {
  const editUrl = `https://github.com/KodeStar/audiosilo-meta/blob/main/data/works/${encodeURIComponent(
    id.slice(0, 2)
  )}/${encodeURIComponent(id)}/work.json`
  return (
    <p className="mt-16 border-t border-edge pt-6 text-sm text-dim">
      Spotted an error?{' '}
      <a
        href={editUrl}
        target="_blank"
        rel="noopener"
        className="text-pink-400 underline-offset-2 transition-colors hover:text-pink-300 hover:underline"
      >
        Edit this work on GitHub
      </a>{' '}
      or{' '}
      <a
        href="https://github.com/KodeStar/audiosilo-meta/issues/new/choose"
        target="_blank"
        rel="noopener"
        className="text-pink-400 underline-offset-2 transition-colors hover:text-pink-300 hover:underline"
      >
        open an issue
      </a>
      .
    </p>
  )
}

/** The "General" tab: the work's description, its recordings, and the
    "more in this series" rail. This is the whole main-column body for a plain
    work (one with no characters/recaps sidecar). The series is fetched once by
    the parent and threaded through to the rail. */
function GeneralPanel({ work, series }: { work: Work; series: Series | null }) {
  return (
    <>
      {work.description ? (
        <p className="mt-6 max-w-2xl text-base leading-relaxed text-body">{work.description}</p>
      ) : null}

      {/* Recordings live in the main column so desktop width is used well */}
      <section className="mt-10">
        <h2 className="text-xl font-semibold text-hi">
          Recordings
          <span className="ml-2 text-sm font-normal text-dim">{work.recordings?.length ?? 0}</span>
        </h2>
        {work.recordings && work.recordings.length > 0 ? (
          <div className="mt-5 grid gap-5 xl:grid-cols-2">
            {work.recordings.map((r) => (
              <RecordingCard key={r.id} workId={work.id} recording={r} />
            ))}
          </div>
        ) : (
          <p className="mt-5 rounded-xl border border-edge bg-surface px-6 py-10 text-center text-sm text-dim">
            No recordings have been catalogued for this work yet.
          </p>
        )}
      </section>

      <SeriesRail work={work} series={series} />
    </>
  )
}

/** One tab in the work-page tab bar: an underlined active state in the accent,
    with an optional muted count beside the label. */
function TabButton({
  active,
  onClick,
  label,
  count,
  id,
  controls,
}: {
  active: boolean
  onClick: () => void
  label: string
  count?: number
  id: string
  controls: string
}) {
  return (
    <button
      type="button"
      role="tab"
      id={id}
      aria-selected={active}
      aria-controls={controls}
      onClick={onClick}
      className={`-mb-px flex items-center gap-1.5 border-b-2 px-1 pb-3 pt-1 text-sm font-medium transition-colors ${
        active ? 'border-pink-500 text-hi' : 'border-transparent text-dim hover:text-body'
      }`}
    >
      <span>{label}</span>
      {typeof count === 'number' ? <span className="text-xs font-normal text-dim">{count}</span> : null}
    </button>
  )
}

function Loaded({ work }: { work: Work }) {
  usePageTitle(work.title)
  const cover = work.recordings?.find((r) => r.cover_url)?.cover_url ?? null

  const characters = work.characters ?? []
  const hasCharacters = characters.length > 0
  // The single source for the Story so far tab: the ordered row set (chaptered
  // recaps + the whole-book summary rows). The panel renders it; its length is
  // both the tab's count and its presence flag, so a work carrying only a
  // whole-book summary still gets the tab.
  const recapRows = storyRows(work.recaps ?? [], work.recap_summary)
  const hasRecaps = recapRows.length > 0
  const showTabs = hasCharacters || hasRecaps

  // Initialise the tab from the URL hash so a deep link like #story-so-far opens
  // on the right tab from the first frame - this island is client:only, so window
  // is available at first render and there is no SSR pass to agree with. Falls
  // back to General when this work lacks that sidecar.
  const [tab, setTab] = useState<WorkTab>(() => tabFromHash(window.location.hash, { hasCharacters, hasRecaps }))

  // Canonicalise a stale fragment once on mount: a deep link like #story-so-far
  // onto a work with no recaps fell back to General above, so drop the fragment
  // the fallback ignored (copying the URL onward would mislead). selectTab keeps
  // the hash in step from here on.
  useEffect(() => {
    const canonical = hashForTab(tab)
    if (window.location.hash !== canonical) {
      window.history.replaceState(null, '', `${window.location.pathname}${window.location.search}${canonical}`)
    }
  }, []) // eslint-disable-line react-hooks/exhaustive-deps

  // Switching a tab reflects the choice into the hash (no scroll jump, no history
  // spam) so it rides along when the reader flips to another work in the series;
  // General clears the fragment entirely.
  const selectTab = useCallback((next: WorkTab) => {
    setTab(next)
    const hash = hashForTab(next)
    const url = `${window.location.pathname}${window.location.search}${hash}`
    window.history.replaceState(null, '', url)
  }, [])

  const tabHash = hashForTab(tab)

  // Fetch the work's first series once (shared by the "more in this series" rail
  // and the prev/next nav) so we never fetch it twice. A series-less work passes
  // a null id, which useEntity leaves at 'loading' - mapped to null, so the rail
  // and nav render nothing; a fetch error maps to null the same quiet way (the
  // rail/nav is a bonus, never an error state).
  const seriesState = useEntity<Series>(work.series?.[0]?.id ?? null, getSeries)
  const series = seriesState.status === 'ready' ? seriesState.data : null

  // The work is known (synchronously) to be in a series while the fetch is still
  // in flight - that is the window the nav placeholder covers.
  const seriesPending = (work.series?.length ?? 0) > 0 && seriesState.status === 'loading'
  const { prev, next } = series ? seriesNeighbors(series.works ?? [], work.id) : { prev: null, next: null }

  return (
    <div className="container py-10">
      <div className="mb-8">
        <BackLink />
      </div>

      <div className="grid gap-8 lg:grid-cols-[18rem_1fr] lg:gap-12">
        {/* Sidebar: cover + metadata - always visible, outside the tabs */}
        <aside>
          <div className="mx-auto w-48 sm:w-56 lg:mx-0 lg:w-full">
            <CoverImage src={cover} alt={`Cover of ${work.title}`} title={work.title} eager />
          </div>
          <MetadataBlock work={work} />
        </aside>

        {/* Main column: header + (tabbed) body */}
        <div className="min-w-0">
          <h1 className="text-3xl font-bold leading-tight tracking-tight text-hi sm:text-4xl">
            {work.title}
          </h1>
          {work.subtitle ? <p className="mt-2 text-lg text-body">{work.subtitle}</p> : null}

          {work.authors && work.authors.length > 0 ? (
            <p className="mt-4 text-base">
              <span className="text-dim">By </span>
              <PersonLinks people={work.authors} className="font-medium" />
            </p>
          ) : null}

          {work.series && work.series.length > 0 ? (
            <div className="mt-4 flex flex-wrap gap-2">
              {work.series.map((s) => (
                <a
                  key={s.id}
                  href={href.series(s.id)}
                  className="inline-flex items-center gap-1.5 rounded-full border border-edge bg-surface px-3 py-1 text-sm text-body transition-colors hover:border-pink-500/50 hover:text-pink-300"
                >
                  <span>{s.name}</span>
                  {s.position ? (
                    <span className="font-semibold text-pink-400">#{s.position}</span>
                  ) : null}
                </a>
              ))}
            </div>
          ) : null}

          {/* Series prev/next: above the tab bar so it stays visible on every tab
              (and on works that show no tabs). */}
          <SeriesNav prev={prev} next={next} tabHash={tabHash} pending={seriesPending} />

          {showTabs ? (
            <>
              <div
                role="tablist"
                aria-label="Work sections"
                className="mt-8 flex gap-6 border-b border-edge"
              >
                <TabButton
                  id="tab-general"
                  controls="panel-general"
                  active={tab === 'general'}
                  onClick={() => selectTab('general')}
                  label="General"
                />
                {hasCharacters ? (
                  <TabButton
                    id="tab-characters"
                    controls="panel-characters"
                    active={tab === 'characters'}
                    onClick={() => selectTab('characters')}
                    label="Characters"
                    count={characters.length}
                  />
                ) : null}
                {hasRecaps ? (
                  <TabButton
                    id="tab-recaps"
                    controls="panel-recaps"
                    active={tab === 'recaps'}
                    onClick={() => selectTab('recaps')}
                    label="Story so far"
                    count={recapRows.length}
                  />
                ) : null}
              </div>

              {tab === 'general' ? (
                <div role="tabpanel" id="panel-general" aria-labelledby="tab-general">
                  <GeneralPanel work={work} series={series} />
                </div>
              ) : null}
              {tab === 'characters' ? (
                <div role="tabpanel" id="panel-characters" aria-labelledby="tab-characters">
                  <CharactersPanel characters={characters} />
                </div>
              ) : null}
              {tab === 'recaps' ? (
                <div role="tabpanel" id="panel-recaps" aria-labelledby="tab-recaps">
                  <RecapsPanel rows={recapRows} />
                </div>
              ) : null}
            </>
          ) : (
            <GeneralPanel work={work} series={series} />
          )}
        </div>
      </div>

      <ImproveRecord id={work.id} />
    </div>
  )
}

export default function WorkDetail() {
  const id = useQueryParam('id')
  const state = useEntity<Work>(id, getWork)
  if (state.status === 'loading') return <DetailSpinner />
  if (state.status === 'error') return <DetailError notFound={state.notFound} kind="work" />
  return <Loaded work={state.data} />
}
