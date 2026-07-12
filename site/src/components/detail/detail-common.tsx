import { useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'

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

/** Set document.title while a detail view is mounted, restoring nothing (each
    navigation is a full page load in this static site). */
export function usePageTitle(title: string | null) {
  useEffect(() => {
    if (title) document.title = `${title} - AudioSilo Meta`
  }, [title])
}

export type LoadState<T> =
  | { status: 'loading' }
  | { status: 'error'; notFound: boolean }
  | { status: 'ready'; data: T }

/** Generic loader for a detail entity keyed by an id from the query string. */
export function useEntity<T>(
  id: string | null,
  fetcher: (id: string, signal: AbortSignal) => Promise<T>
): LoadState<T> {
  const [state, setState] = useState<LoadState<T>>({ status: 'loading' })
  useEffect(() => {
    if (id === null) {
      // Query string not read yet.
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
  }, [id, fetcher])
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
      <a
        href="/"
        className="mt-8 inline-flex items-center gap-2 rounded-lg bg-pink-600 px-6 py-3 font-medium text-white transition-colors hover:bg-pink-500"
      >
        Back to search
      </a>
    </div>
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
