import { useCallback, useState } from 'react'
import {
  getWork,
  getChapters,
  formatRuntime,
  formatYear,
  formatOffset,
  type Work,
  type Recording,
  type Chapter,
} from '../../lib/api'
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

function Loaded({ work }: { work: Work }) {
  usePageTitle(work.title)
  const cover =
    work.recordings?.find((r) => r.cover_url)?.cover_url ?? null
  const year = formatYear(work.first_published)

  return (
    <div className="container py-10">
      <div className="mb-8">
        <BackLink />
      </div>

      <div className="grid gap-8 md:grid-cols-[16rem_1fr] md:gap-10">
        {/* Cover */}
        <div className="mx-auto w-40 sm:w-52 md:mx-0 md:w-full md:max-w-64">
          <CoverImage src={cover} alt={`Cover of ${work.title}`} title={work.title} eager />
        </div>

        {/* Head */}
        <div>
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
                  href={`/series?id=${encodeURIComponent(s.id)}`}
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

          <div className="mt-5 flex flex-wrap gap-x-6 gap-y-2">
            {work.language ? <MetaItem label="Language" value={work.language} /> : null}
            {work.first_published ? (
              <MetaItem label="First published" value={year ?? work.first_published} />
            ) : null}
            <MetaItem
              label="Recordings"
              value={String(work.recordings?.length ?? 0)}
            />
          </div>

          {work.description ? (
            <p className="mt-6 max-w-2xl text-base leading-relaxed text-body">
              {work.description}
            </p>
          ) : null}
        </div>
      </div>

      {/* Recordings */}
      <section className="mt-12">
        <h2 className="text-xl font-semibold text-hi">
          Recordings
          <span className="ml-2 text-sm font-normal text-dim">
            {work.recordings?.length ?? 0}
          </span>
        </h2>
        {work.recordings && work.recordings.length > 0 ? (
          <div className="mt-5 grid gap-5 lg:grid-cols-2">
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
  )
}

export default function WorkDetail() {
  const id = useQueryParam('id')
  const state = useEntity<Work>(id, getWork)
  if (state.status === 'loading') return <DetailSpinner />
  if (state.status === 'error') return <DetailError notFound={state.notFound} kind="work" />
  return <Loaded work={state.data} />
}
