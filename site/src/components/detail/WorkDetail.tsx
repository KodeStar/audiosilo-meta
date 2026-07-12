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
  type Recap,
} from '../../lib/api'
import { roleLabel, revealLabel, recapLabel, scopeLabel, sortRecaps } from '../../lib/expressive'
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
        <svg
          className={`h-4 w-4 text-dim transition-transform ${open ? 'rotate-180' : ''}`}
          xmlns="http://www.w3.org/2000/svg"
          fill="none"
          viewBox="0 0 24 24"
          strokeWidth={1.5}
          stroke="currentColor"
          aria-hidden="true"
        >
          <path strokeLinecap="round" strokeLinejoin="round" d="m19.5 8.25-7.5 7.5-7.5-7.5" />
        </svg>
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

/** One character card. Descriptions are spoiler-bounded to the reveal position
    but still story detail, so the parent section blurs them until asked. */
function CharacterCard({ character, revealed }: { character: Character; revealed: boolean }) {
  const role = roleLabel(character.role)
  return (
    <article className="rounded-2xl border border-edge bg-surface p-4">
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0">
          <h3 className="font-semibold text-hi">{character.name}</h3>
          {character.aliases && character.aliases.length > 0 ? (
            <p className="mt-0.5 text-xs text-dim">also {character.aliases.join(', ')}</p>
          ) : null}
        </div>
        {role ? <Badge>{role}</Badge> : null}
      </div>
      <p className="mt-2 text-xs font-medium text-pink-400/90">{revealLabel(character.reveal)}</p>
      {character.description ? (
        <p
          className={`mt-3 text-sm leading-relaxed text-body transition duration-200 ${
            revealed ? '' : 'select-none blur-sm'
          }`}
          aria-hidden={!revealed}
        >
          {character.description}
        </p>
      ) : null}
    </article>
  )
}

/** The cast of a work: community-authored, spoiler-aware character cards behind a
    single reveal toggle (descriptions start blurred). Renders nothing when the
    work has no character sidecar. */
function CharactersSection({ work }: { work: Work }) {
  const characters = work.characters
  const [revealed, setRevealed] = useState(false)
  if (!characters || characters.length === 0) return null
  return (
    <section className="mt-14">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h2 className="text-xl font-semibold text-hi">
          Characters
          <span className="ml-2 text-sm font-normal text-dim">{characters.length}</span>
        </h2>
        <button
          type="button"
          onClick={() => setRevealed((v) => !v)}
          aria-pressed={revealed}
          className="rounded-md border border-edge bg-raised px-3 py-1 text-xs text-body transition-colors hover:border-pink-500/50 hover:text-hi"
        >
          {revealed ? 'Hide descriptions' : 'Show descriptions (spoilers)'}
        </button>
      </div>
      <p className="mt-2 max-w-2xl text-sm text-dim">
        Community-written and spoiler-aware - each entry is scoped to where the character first
        appears.
      </p>
      <div className="mt-5 grid gap-4 sm:grid-cols-2">
        {characters.map((c) => (
          <CharacterCard key={c.id} character={c} revealed={revealed} />
        ))}
      </div>
    </section>
  )
}

/** One "story so far" recap: a collapsible row that stays closed (spoiler-safe)
    until the reader opens it. */
function RecapRow({ recap }: { recap: Recap }) {
  const [open, setOpen] = useState(false)
  const scope = scopeLabel(recap.scope)
  return (
    <div className="bg-surface">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        className="flex w-full items-center justify-between gap-3 px-4 py-3 text-left transition-colors hover:bg-raised/40"
      >
        <span className="flex flex-wrap items-center gap-2">
          <span className="text-sm font-medium text-body">{recapLabel(recap)}</span>
          {scope ? <Badge>{scope}</Badge> : null}
        </span>
        <svg
          className={`h-4 w-4 shrink-0 text-dim transition-transform ${open ? 'rotate-180' : ''}`}
          xmlns="http://www.w3.org/2000/svg"
          fill="none"
          viewBox="0 0 24 24"
          strokeWidth={1.5}
          stroke="currentColor"
          aria-hidden="true"
        >
          <path strokeLinecap="round" strokeLinejoin="round" d="m19.5 8.25-7.5 7.5-7.5-7.5" />
        </svg>
      </button>
      {open ? (
        <p className="px-4 pb-4 text-sm leading-relaxed text-body">{recap.text}</p>
      ) : null}
    </div>
  )
}

/** "Story so far": position-keyed recaps as an accordion, ordered by position and
    closed by default so the reader chooses how far to reveal. Renders nothing
    when the work has no recap sidecar. */
function RecapsSection({ work }: { work: Work }) {
  const recaps = work.recaps
  if (!recaps || recaps.length === 0) return null
  const ordered = sortRecaps(recaps)
  return (
    <section className="mt-14">
      <h2 className="text-xl font-semibold text-hi">
        Story so far
        <span className="ml-2 text-sm font-normal text-dim">{ordered.length}</span>
      </h2>
      <p className="mt-2 max-w-2xl text-sm text-dim">
        Position-tagged recaps - open only as far as you have listened; each reveals the story up to
        its point.
      </p>
      <div className="mt-5 divide-y divide-edge/60 overflow-hidden rounded-2xl border border-edge">
        {ordered.map((r, i) => (
          <RecapRow key={`${r.through.chapter}-${i}`} recap={r} />
        ))}
      </div>
    </section>
  )
}

/** "More in this series": the other member works of the work's (first) series,
    as a horizontal rail of square cover cards. Renders nothing while loading,
    on error, or when the series has no other members. */
function SeriesRail({ work }: { work: Work }) {
  const first = work.series?.[0]
  const [series, setSeries] = useState<Series | null>(null)

  useEffect(() => {
    if (!first) return
    const ctrl = new AbortController()
    getSeries(first.id, ctrl.signal)
      .then(setSeries)
      .catch(() => {
        /* quiet: the rail is a bonus, never an error state */
      })
    return () => ctrl.abort()
  }, [first?.id]) // eslint-disable-line react-hooks/exhaustive-deps

  if (!first || !series) return null
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

function Loaded({ work }: { work: Work }) {
  usePageTitle(work.title)
  const cover = work.recordings?.find((r) => r.cover_url)?.cover_url ?? null

  return (
    <div className="container py-10">
      <div className="mb-8">
        <BackLink />
      </div>

      <div className="grid gap-8 lg:grid-cols-[18rem_1fr] lg:gap-12">
        {/* Sidebar: cover + metadata */}
        <aside>
          <div className="mx-auto w-48 sm:w-56 lg:mx-0 lg:w-full">
            <CoverImage src={cover} alt={`Cover of ${work.title}`} title={work.title} eager />
          </div>
          <MetadataBlock work={work} />
        </aside>

        {/* Main column: head + description + recordings */}
        <div className="min-w-0">
          <h1 className="text-3xl font-bold leading-tight tracking-tight text-hi sm:text-4xl">
            {work.title}
          </h1>
          {work.subtitle ? (
            <p className="mt-2 text-lg text-body">{work.subtitle}</p>
          ) : null}

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

          {work.description ? (
            <p className="mt-6 max-w-2xl text-base leading-relaxed text-body">
              {work.description}
            </p>
          ) : null}

          {/* Recordings live in the main column so desktop width is used well */}
          <section className="mt-10">
            <h2 className="text-xl font-semibold text-hi">
              Recordings
              <span className="ml-2 text-sm font-normal text-dim">
                {work.recordings?.length ?? 0}
              </span>
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
        </div>
      </div>

      <CharactersSection work={work} />
      <RecapsSection work={work} />
      <SeriesRail work={work} />
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
