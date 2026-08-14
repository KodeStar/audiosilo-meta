import { useEffect, useMemo, useState } from 'react'
import { ApiError } from '../../lib/api'
import { entitySlugFromLocation, type EntityKind } from '../../lib/entity-url'
import { correctDataIssueUrl, issueChooserUrl, type RecordKind } from '../../lib/github-prefill'

/** Read a query-string parameter on the client (detail pages are static shells
    that carry the entity id in `?id=`). Returns null ONLY before hydration; once
    read, an absent parameter yields '' - the same value as a present-but-empty
    one - so callers can tell "not yet read" (null) from "not in the URL" ('').
    useEntity maps '' to its not-found state, which is what makes a bare /build
    or /work URL land on an empty/404 state instead of spinning forever. */
export function useQueryParam(name: string): string | null {
  const [value, setValue] = useState<string | null>(null)
  useEffect(() => {
    const params = new URLSearchParams(window.location.search)
    setValue(params.get(name) ?? '')
  }, [name])
  return value
}

/** Read the entity slug for a detail island: the path route first, the legacy
    `?id=` second (see lib/entity-url). Same null/'' contract as useQueryParam -
    null until the location has been read, '' when the URL names no entity. */
export function useEntitySlug(kind: EntityKind): string | null {
  const [value, setValue] = useState<string | null>(null)
  useEffect(() => {
    const { pathname, search } = window.location
    setValue(entitySlugFromLocation(pathname, search, kind) ?? '')
  }, [kind])
  return value
}

/** The two ids metaserve injects in place of the `<!--ssr:entity-->` marker: the
    server-rendered fact sheet, and the JSON payload it rendered that sheet from
    (byte-for-byte what the matching API route returns). Both are absent under
    `yarn dev`, on the legacy shells and against an older metaserve, which is the
    ordinary path - the island then fetches exactly as it always did. */
const ssrNodeId = 'ssr-entity'
const payloadNodeId = 'entity-data'

/** The embedded payload, parsed, but ONLY when it is about the entity this
    island is showing: a page served for one slug must never hydrate another's
    facts, so an id mismatch (or malformed JSON, or no payload at all) yields
    null and the island falls back to fetching. */
function readEmbeddedEntity<T extends { id: string }>(id: string): T | null {
  const node = document.getElementById(payloadNodeId)
  if (!node?.textContent) return null
  try {
    const parsed = JSON.parse(node.textContent) as T
    return parsed && parsed.id === id ? parsed : null
  } catch {
    return null
  }
}

/**
 * Hydrate from the server-rendered payload when there is one.
 *
 * Returns the entity the server already resolved (so useEntity can skip its
 * fetch) and, having taken over, removes the fact sheet the server rendered for
 * crawlers and no-JS readers - the island is about to render the same entity
 * properly, and leaving both would duplicate the page.
 */
export function useEmbeddedEntity<T extends { id: string }>(id: string | null): T | null {
  const data = useMemo(() => (id ? readEmbeddedEntity<T>(id) : null), [id])
  useEffect(() => {
    if (data) document.getElementById(ssrNodeId)?.remove()
  }, [data])
  return data
}

/** Set document.title while a detail view is mounted, restoring nothing (each
    navigation is a full page load in this static site).

    `skip` is passed by a detail view hydrating from the embedded payload: the
    title metaserve composed is richer than this one (it names the authors, and
    is what a crawler and a shared link already show), so the island must not
    overwrite it with the plain `<name> - AudioSilo Meta` form. */
export function usePageTitle(title: string | null, skip = false) {
  useEffect(() => {
    if (title && !skip) document.title = `${title} - AudioSilo Meta`
  }, [title, skip])
}

export type LoadState<T> =
  | { status: 'loading' }
  | { status: 'error'; notFound: boolean }
  | { status: 'ready'; data: T }

/** Generic loader for a detail entity keyed by an id read off the URL.
    `initialData`, when given, is the entity metaserve already embedded in the
    page (see useEmbeddedEntity) - it is rendered as-is and NO request is made. */
export function useEntity<T>(
  id: string | null,
  fetcher: (id: string, signal: AbortSignal) => Promise<T>,
  initialData?: T | null
): LoadState<T> {
  const [state, setState] = useState<LoadState<T>>({ status: 'loading' })
  useEffect(() => {
    if (id === null) {
      // Location not read yet.
      return
    }
    if (initialData) {
      setState({ status: 'ready', data: initialData })
      return
    }
    if (id === '') {
      setState({ status: 'error', notFound: true })
      return
    }
    const ctrl = new AbortController()
    setState({ status: 'loading' })
    fetcher(id, ctrl.signal)
      .then((data) => setState({ status: 'ready', data }))
      .catch((err) => {
        if (ctrl.signal.aborted || (err as Error).name === 'AbortError') return
        const notFound = err instanceof ApiError && err.status === 404
        setState({ status: 'error', notFound })
      })
    return () => ctrl.abort()
  }, [id, fetcher, initialData])
  return state
}

/** The shared loading spinner. The detail pages use the defaults; an island
    embedded mid-page (the coverage panel) overrides the label and wrapper. */
export function DetailSpinner({
  label = 'Loading...',
  className = 'container py-24 text-center',
}: {
  label?: string
  className?: string
} = {}) {
  return (
    <div className={className} aria-live="polite" aria-busy="true">
      <div className="mx-auto h-8 w-8 animate-spin rounded-full border-2 border-edge border-t-pink-500"></div>
      <p className="mt-4 text-sm text-dim">{label}</p>
    </div>
  )
}

export function DetailError({ notFound, kind }: { notFound: boolean; kind: string }) {
  return (
    <div className="container py-24 text-center">
      <p className="text-5xl font-black text-edge">{notFound ? '404' : '!'}</p>
      <h1 className="mt-4 text-2xl font-bold text-hi">
        {notFound ? `That ${kind} is not here` : 'Something went wrong'}
      </h1>
      <p className="mx-auto mt-3 max-w-md text-body">
        {notFound
          ? `We could not find that ${kind} in the database. It may not have been catalogued yet.`
          : `The database could not be reached. Please try again in a moment.`}
      </p>
      <div className="mt-8 flex flex-wrap items-center justify-center gap-3">
        <a
          href="/"
          className="inline-flex items-center gap-2 rounded-lg bg-pink-600 px-6 py-3 font-medium text-white transition-colors hover:bg-pink-500"
        >
          Back to search
        </a>
        {notFound ? (
          <a
            href={issueChooserUrl}
            target="_blank"
            rel="noopener"
            className="inline-flex items-center gap-2 rounded-lg border border-edge px-6 py-3 font-medium text-hi transition-colors hover:border-pink-500"
          >
            Add it to the database
          </a>
        ) : null}
      </div>
      {notFound ? (
        <p className="mt-4 text-sm text-dim">
          Not in the database yet? See{' '}
          <a
            href="/contribute"
            className="text-pink-400 underline-offset-2 transition-colors hover:text-pink-300 hover:underline"
          >
            ways to contribute
          </a>
          .
        </p>
      ) : null}
    </div>
  )
}

/** The community-correction footer shared by every detail page: a prefilled
    correction issue with the record pre-identified. Generalised from the work
    page so person and series records carry the same affordance.

    There is no "edit the file on GitHub" link beside it. Records are stored many
    to a pack file, so a record has no file of its own to link to, and the
    correction form is the supported way to fix a single fact - it identifies the
    record, the intake bot applies it, and the contributor never has to find
    anything in the tree. */
export function ImproveRecord({ kind, id }: { kind: RecordKind; id: string }) {
  const link =
    'text-pink-400 underline-offset-2 transition-colors hover:text-pink-300 hover:underline'
  return (
    <p className="mt-16 border-t border-edge pt-6 text-sm text-dim">
      Spotted an error?{' '}
      <a href={correctDataIssueUrl(kind, id)} target="_blank" rel="noopener" className={link}>
        Report a problem with this {kind}
      </a>{' '}
      and the intake bot will open the correction for review.
    </p>
  )
}

/** A back-to-search breadcrumb link shared by the detail heads. */
export function BackLink() {
  return (
    <a
      href="/"
      className="inline-flex items-center gap-1.5 text-sm text-dim transition-colors hover:text-pink-400"
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
        <path strokeLinecap="round" strokeLinejoin="round" d="M10.5 19.5 3 12m0 0 7.5-7.5M3 12h18" />
      </svg>
      Search
    </a>
  )
}
