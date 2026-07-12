import { useEffect, useRef, useState } from 'react'
import { lookup, formatRuntime } from '../../lib/api'
import {
  parseExport,
  partitionByIdentifier,
  isContributableOnMiss,
  type ParsedBook,
  type ParseOutcome,
} from '../../lib/import-parse'
import { addWorkIssueUrl, factualSubset, importLibraryIssueUrl } from '../../lib/github-prefill'

// Concurrency for the lookup sweep, and the hard safety cap on export size.
const POOL_SIZE = 8
const MAX_BOOKS = 5000

const PRIVACY =
  'Your export is read entirely in your browser. Only ASINs and ISBNs are sent to the API, to check what is already in the database. Personal fields never leave your device.'

// Button classes replicated from Button.astro so the island matches the site.
const BTN_BASE =
  'inline-flex items-center justify-center gap-2 rounded-lg px-6 py-3 font-medium transition-all duration-200 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-pink-500'
const BTN_PRIMARY = `${BTN_BASE} bg-pink-600 text-white shadow-lg shadow-pink-600/20 hover:-translate-y-0.5 hover:bg-pink-500 hover:shadow-pink-500/30`
const BTN_SECONDARY = `${BTN_BASE} border border-edge text-hi hover:-translate-y-0.5 hover:border-pink-500`

// Inline outline icons (path data shared with Icon.astro), since that Astro
// component cannot be used inside a React island.
const ICON_PATHS = {
  database:
    'M20.25 6.375c0 2.278-3.694 4.125-8.25 4.125S3.75 8.653 3.75 6.375m16.5 0c0-2.278-3.694-4.125-8.25-4.125S3.75 4.097 3.75 6.375m16.5 0v11.25c0 2.278-3.694 4.125-8.25 4.125s-8.25-1.847-8.25-4.125V6.375m16.5 0v3.75m-16.5-3.75v3.75m16.5 0v3.75C20.25 16.153 16.556 18 12 18s-8.25-1.847-8.25-4.125v-3.75m16.5 0c0 2.278-3.694 4.125-8.25 4.125s-8.25-1.847-8.25-4.125',
  external:
    'M13.5 6H5.25A2.25 2.25 0 0 0 3 8.25v10.5A2.25 2.25 0 0 0 5.25 21h10.5A2.25 2.25 0 0 0 18 18.75V10.5m-10.5 6L21 3m0 0h-5.25M21 3v5.25',
  x: 'M6 18 18 6M6 6l12 12',
  download:
    'M3 16.5v2.25A2.25 2.25 0 0 0 5.25 21h13.5A2.25 2.25 0 0 0 21 18.75V16.5m-13.5-6L12 15m0 0 4.5-4.5M12 15V3',
} as const

function Icon({
  name,
  className = 'h-5 w-5',
}: {
  name: keyof typeof ICON_PATHS
  className?: string
}) {
  return (
    <svg
      className={className}
      xmlns="http://www.w3.org/2000/svg"
      fill="none"
      viewBox="0 0 24 24"
      strokeWidth={1.5}
      stroke="currentColor"
      aria-hidden="true"
    >
      <path strokeLinecap="round" strokeLinejoin="round" d={ICON_PATHS[name]} />
    </svg>
  )
}

type Phase = 'idle' | 'unknown' | 'libation' | 'error' | 'diffing' | 'results'

interface Results {
  inDatabase: ParsedBook[]
  newBooks: ParsedBook[]
  cannotMatch: ParsedBook[]
  skipped: number
}

// A one-line summary of a book for the review rows.
function metaLine(b: ParsedBook): string {
  const parts: string[] = []
  if (b.authors.length) parts.push(b.authors.join(', '))
  if (b.narrators.length) parts.push(`Narrated by ${b.narrators.join(', ')}`)
  if (b.seriesName) {
    parts.push(b.seriesPosition ? `${b.seriesName} #${b.seriesPosition}` : b.seriesName)
  }
  const rt = formatRuntime(b.runtimeMin)
  if (rt) parts.push(rt)
  return parts.join(' · ')
}

// The hand-off actions shared by the unknown/error and libation cards: open the
// import issue form, or start over.
function IssueFormActions({ onReset }: { onReset: () => void }) {
  return (
    <div className="mt-5 flex flex-wrap items-center gap-3">
      <a href={importLibraryIssueUrl} target="_blank" rel="noopener" className={BTN_SECONDARY}>
        <Icon name="external" />
        Open the import issue form
      </a>
      <button
        type="button"
        onClick={onReset}
        className="text-sm font-medium text-pink-400 transition-colors hover:text-pink-300"
      >
        Try another file
      </button>
    </div>
  )
}

export default function ImportTool() {
  const [phase, setPhase] = useState<Phase>('idle')
  const [errorMsg, setErrorMsg] = useState('')
  const [dragging, setDragging] = useState(false)
  const [progress, setProgress] = useState({ done: 0, total: 0 })
  const [results, setResults] = useState<Results | null>(null)

  const abortRef = useRef<AbortController | null>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)

  // Abort any in-flight sweep when the island unmounts.
  useEffect(() => () => abortRef.current?.abort(), [])

  function reset() {
    abortRef.current?.abort()
    abortRef.current = null
    setPhase('idle')
    setErrorMsg('')
    setProgress({ done: 0, total: 0 })
    setResults(null)
    if (fileInputRef.current) fileInputRef.current.value = ''
  }

  function readFile(file: File) {
    const reader = new FileReader()
    reader.onload = () => handleText(String(reader.result ?? ''))
    reader.onerror = () => {
      setErrorMsg('Could not read that file. Please try again.')
      setPhase('error')
    }
    reader.readAsText(file)
  }

  function handleText(text: string) {
    let outcome: ParseOutcome
    try {
      outcome = parseExport(text)
    } catch (err) {
      setErrorMsg(err instanceof Error ? err.message : 'Could not read that file.')
      setPhase('error')
      return
    }
    if (outcome.format === 'unknown') {
      setPhase('unknown')
      return
    }
    if (outcome.format === 'libation') {
      setPhase('libation')
      return
    }
    void runDiff(outcome.books)
  }

  async function runDiff(all: ParsedBook[]) {
    const skipped = all.length > MAX_BOOKS ? all.length - MAX_BOOKS : 0
    const books = all.slice(0, MAX_BOOKS)

    // Dedupe and split off books with no identifier (they can't be matched);
    // the rest are looked up against the database below.
    const { identified, unidentified } = partitionByIdentifier(books)
    const cannotMatch: ParsedBook[] = [...unidentified]
    const inDatabase: ParsedBook[] = []
    const newBooks: ParsedBook[] = []

    setResults(null)
    setProgress({ done: 0, total: identified.length })
    setPhase('diffing')

    const ctrl = new AbortController()
    abortRef.current = ctrl

    const worker = async (book: ParsedBook) => {
      const value = book.asin ?? book.isbn
      if (!value) return
      try {
        const match = await lookup(book.asin ? 'asin' : 'isbn', value, ctrl.signal)
        if (match) inDatabase.push(book)
        else if (isContributableOnMiss(book)) newBooks.push(book)
        else cannotMatch.push(book) // not found, but unknown language -> cannot auto-match
      } catch {
        if (ctrl.signal.aborted) return
        cannotMatch.push(book) // a real lookup failure, counted, never treated as new
      } finally {
        if (!ctrl.signal.aborted) {
          setProgress((p) => ({ ...p, done: p.done + 1 }))
        }
      }
    }

    // Fixed-size worker pool over the unique identifiers.
    let idx = 0
    const next = async (): Promise<void> => {
      while (idx < identified.length && !ctrl.signal.aborted) {
        const i = idx++
        await worker(identified[i])
      }
    }
    const workers = Array.from({ length: Math.min(POOL_SIZE, identified.length) }, () =>
      next()
    )
    await Promise.all(workers)

    if (ctrl.signal.aborted) return
    abortRef.current = null
    setResults({ inDatabase, newBooks, cannotMatch, skipped })
    setPhase('results')
  }

  function onFileChange(e: React.ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0]
    if (file) readFile(file)
  }

  function onDrop(e: React.DragEvent) {
    e.preventDefault()
    setDragging(false)
    const file = e.dataTransfer.files?.[0]
    if (file) readFile(file)
  }

  function downloadNewBooks() {
    if (!results) return
    const data = JSON.stringify(results.newBooks.map(factualSubset), null, 2)
    const blob = new Blob([data], { type: 'application/json' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = 'audiosilo-meta-new-books.json'
    document.body.appendChild(a)
    a.click()
    a.remove()
    // Defer the revoke: some browsers (Firefox/Safari) cancel the save if the
    // blob URL is freed before the download has started reading it.
    setTimeout(() => URL.revokeObjectURL(url), 0)
  }

  // --- Renders --------------------------------------------------------------

  if (phase === 'idle') {
    return (
      <div>
        <div
          onDragOver={(e) => {
            e.preventDefault()
            setDragging(true)
          }}
          onDragLeave={() => setDragging(false)}
          onDrop={onDrop}
        >
          <input
            ref={fileInputRef}
            type="file"
            accept=".json,application/json"
            className="sr-only"
            onChange={onFileChange}
          />
          <button
            type="button"
            onClick={() => fileInputRef.current?.click()}
            className={`flex w-full flex-col items-center justify-center gap-4 rounded-2xl border-2 border-dashed p-10 text-center transition-colors ${
              dragging
                ? 'border-pink-500 bg-pink-600/5'
                : 'border-edge bg-surface hover:border-pink-500/60'
            }`}
          >
            <span className="inline-flex h-14 w-14 items-center justify-center rounded-2xl border border-edge bg-raised text-pink-400">
              <Icon name="database" className="h-7 w-7" />
            </span>
            <span className="text-lg font-semibold text-hi">Drop your books.json here</span>
            <span className="text-sm text-dim">
              or <span className="text-pink-400">choose a file</span> - your OpenAudible export
            </span>
          </button>
        </div>
        <p className="mt-4 text-sm leading-relaxed text-dim">{PRIVACY}</p>
      </div>
    )
  }

  if (phase === 'unknown' || phase === 'error') {
    const heading =
      phase === 'unknown'
        ? 'This does not look like an OpenAudible export'
        : 'We could not read that file'
    const body =
      phase === 'unknown'
        ? 'We could not find OpenAudible books in this file. Make sure you selected the books.json export from OpenAudible. If you use a different tool, you can still contribute your library through the import issue form.'
        : errorMsg
    return (
      <div className="rounded-2xl border border-edge bg-surface p-6">
        <div className="flex items-start gap-4">
          <span className="inline-flex h-11 w-11 shrink-0 items-center justify-center rounded-xl border border-edge bg-raised text-pink-400">
            <Icon name="x" />
          </span>
          <div className="min-w-0">
            <h3 className="text-lg font-semibold text-hi">{heading}</h3>
            <p className="mt-2 text-sm leading-relaxed text-body">{body}</p>
            <IssueFormActions onReset={reset} />
          </div>
        </div>
      </div>
    )
  }

  if (phase === 'libation') {
    return (
      <div className="rounded-2xl border border-edge bg-surface p-6">
        <h3 className="text-lg font-semibold text-hi">Libation export detected</h3>
        <p className="mt-2 text-sm leading-relaxed text-body">
          In-browser parsing of Libation exports is coming. For now, contribute your Libation
          export through the import issue form - drop the file there and we will ingest the
          factual fields.
        </p>
        <IssueFormActions onReset={reset} />
      </div>
    )
  }

  if (phase === 'diffing') {
    const total = progress.total
    const shown = total === 0 ? 0 : Math.min(progress.done + 1, total)
    return (
      <div className="rounded-2xl border border-edge bg-surface p-6">
        <div className="flex items-center justify-between gap-4">
          <p className="text-sm font-medium text-hi" aria-live="polite">
            {total === 0 ? 'Sorting your library...' : `Checking ${shown} of ${total}...`}
          </p>
          <button type="button" onClick={reset} className={`${BTN_SECONDARY} px-4 py-2`}>
            Cancel
          </button>
        </div>
        <progress
          className="import-progress mt-4"
          value={progress.done}
          max={total || 1}
          aria-label="Checking your library against the database"
        />
      </div>
    )
  }

  // phase === 'results'
  if (!results) return null
  const stat = (value: number, label: string, tone: string) => (
    <div className="rounded-2xl border border-edge bg-surface p-6 text-center">
      <div className={`text-4xl font-bold ${tone}`}>{value.toLocaleString()}</div>
      <div className="mt-2 text-sm text-dim">{label}</div>
    </div>
  )

  return (
    <div className="space-y-8">
      <div className="grid gap-4 sm:grid-cols-3">
        {stat(results.inDatabase.length, 'In the database', 'text-success')}
        {stat(results.newBooks.length, 'New - you can contribute these', 'text-pink-400')}
        {stat(results.cannotMatch.length, 'Cannot auto-match', 'text-dim')}
      </div>

      {results.skipped > 0 ? (
        <p className="rounded-xl border border-edge bg-raised px-4 py-3 text-sm text-dim">
          This export is large. We checked the first {MAX_BOOKS.toLocaleString()} books;{' '}
          {results.skipped.toLocaleString()} more were skipped.
        </p>
      ) : null}

      {results.newBooks.length === 0 ? (
        <p className="rounded-2xl border border-edge bg-surface p-6 text-sm leading-relaxed text-body">
          Nothing new to contribute from this file - every book with an identifier is already in
          the database, or could not be auto-matched. Thank you for checking.
        </p>
      ) : (
        <div className="rounded-2xl border border-edge bg-surface p-6">
          <h3 className="text-lg font-semibold text-hi">Contribute the new books</h3>

          {results.newBooks.length <= 10 ? (
            <ul className="mt-5 space-y-3">
              {results.newBooks.map((b, i) => {
                const line = metaLine(b)
                return (
                <li
                  key={i}
                  className="flex flex-col gap-3 rounded-xl border border-edge bg-raised p-4 sm:flex-row sm:items-center sm:justify-between"
                >
                  <div className="min-w-0">
                    <p className="font-medium text-hi">{b.title}</p>
                    {line ? <p className="mt-1 text-sm text-dim">{line}</p> : null}
                  </div>
                  <a
                    href={addWorkIssueUrl(b)}
                    target="_blank"
                    rel="noopener"
                    className={`${BTN_SECONDARY} shrink-0 px-4 py-2 text-sm`}
                  >
                    <Icon name="external" className="h-4 w-4" />
                    Contribute this book
                  </a>
                </li>
                )
              })}
            </ul>
          ) : (
            <p className="mt-3 text-sm leading-relaxed text-body">
              There are {results.newBooks.length.toLocaleString()} new books - too many to review
              one by one. Download the factual export below and attach it to a single import
              issue.
            </p>
          )}

          <div className="mt-6 flex flex-wrap gap-3">
            <button type="button" onClick={downloadNewBooks} className={BTN_PRIMARY}>
              <Icon name="download" />
              Download new-books export (.json)
            </button>
            <a href={importLibraryIssueUrl} target="_blank" rel="noopener" className={BTN_SECONDARY}>
              <Icon name="external" />
              Attach it to an import issue
            </a>
          </div>
          <p className="mt-3 text-xs leading-relaxed text-dim">
            The download contains factual fields only - titles, authors, narrators, series,
            runtimes, ASINs and chapters. No purchase history, ratings, file paths or personal
            data.
          </p>
        </div>
      )}

      <button
        type="button"
        onClick={reset}
        className="inline-flex items-center gap-1.5 text-sm font-medium text-pink-400 transition-colors hover:text-pink-300"
      >
        Import another file
      </button>
    </div>
  )
}
