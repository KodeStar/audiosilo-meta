import { useEffect, useRef, useState } from 'react'
import { lookup, search, getPerson, formatRuntime, type SearchResult } from '../../lib/api'
import {
  parseExport,
  partitionByIdentifier,
  isIdentifierPoor,
  isContributableOnMiss,
  matchExistingWork,
  authorKey,
  authorSearchKeys,
  candidatesForBook,
  dedupeCandidates,
  type ParsedBook,
  type ParseOutcome,
  type WorkCandidate,
  type WorkMatch,
} from '../../lib/import-parse'
import {
  addWorkIssueUrl,
  addRecordingIssueUrl,
  importLibraryIssueUrl,
  newBooksPayload,
} from '../../lib/github-prefill'
import { downloadJson } from '../../lib/download'
import { BTN_PRIMARY, BTN_SECONDARY, Icon } from '../ui'

// Concurrency for the lookup + author-search sweeps, and the hard safety cap on
// export size.
const POOL_SIZE = 8
const MAX_BOOKS = 5000
// Author-search page size: enough to cover a prolific author's shelf so an
// existing work isn't missed by the cap.
const AUTHOR_WORKS_LIMIT = 50

// A fixed-size worker pool over items; stops early if the signal aborts.
async function runPool<T>(
  items: T[],
  size: number,
  signal: AbortSignal,
  work: (item: T) => Promise<void>
): Promise<void> {
  let idx = 0
  const next = async (): Promise<void> => {
    while (idx < items.length && !signal.aborted) {
      const i = idx++
      await work(items[i])
    }
  }
  await Promise.all(Array.from({ length: Math.min(size, items.length) }, () => next()))
}

// Map a search-work or person-authored entry to a work candidate (same shape).
const toCandidate = (x: {
  id: string
  title: string
  authors: { name: string }[]
}): WorkCandidate => ({ id: x.id, title: x.title, authors: x.authors })

const PRIVACY =
  'Your export is read entirely in your browser. The API receives only ASINs and ISBNs (to check what is already catalogued) and, for books that are not matched, the author names used to look for an existing work. Personal fields and the file itself never leave your device.'

type Phase = 'idle' | 'unknown' | 'error' | 'diffing' | 'results'

// A book to contribute: either a brand-new work, or (existingWork set) a new
// recording of a work already in the catalogue.
interface NewBook {
  book: ParsedBook
  existingWork: WorkMatch | null
}

interface Results {
  inDatabase: ParsedBook[]
  newBooks: NewBook[]
  cannotMatch: ParsedBook[]
  noIdentifier: number // books in cannotMatch that carry no ASIN/ISBN (couldn't be checked)
  total: number // distinct books processed (identified + unidentified)
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

// A card with an icon chip, heading, and free-form body - the shared chrome for
// the unknown/error notice and the identifier-poor callout.
function IconCard({
  icon,
  heading,
  children,
}: {
  icon: React.ComponentProps<typeof Icon>['name']
  heading: string
  children: React.ReactNode
}) {
  return (
    <div className="rounded-2xl border border-edge bg-surface p-6">
      <div className="flex items-start gap-4">
        <span className="inline-flex h-11 w-11 shrink-0 items-center justify-center rounded-xl border border-edge bg-raised text-pink-400">
          <Icon name={icon} />
        </span>
        <div className="min-w-0">
          <h3 className="text-lg font-semibold text-hi">{heading}</h3>
          {children}
        </div>
      </div>
    </div>
  )
}

// The hand-off actions for the unknown/error card: open the import issue form,
// or start over.
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
  const [matching, setMatching] = useState(false)
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
    setMatching(false)
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
    void runDiff(outcome.books)
  }

  async function runDiff(all: ParsedBook[]) {
    const skipped = all.length > MAX_BOOKS ? all.length - MAX_BOOKS : 0
    const books = all.slice(0, MAX_BOOKS)

    // Dedupe and split off books with no identifier (they can't be matched);
    // the rest are looked up against the database below. `unidentified` is the
    // no-identifier portion of "cannot auto-match" - reported so the UI can
    // explain that number honestly (couldn't be checked, not missing).
    const { identified, unidentified } = partitionByIdentifier(books)
    const totalBooks = identified.length + unidentified.length
    const cannotMatch: ParsedBook[] = [...unidentified]
    const inDatabase: ParsedBook[] = []
    const misses: ParsedBook[] = []

    setResults(null)
    setMatching(false)
    setProgress({ done: 0, total: identified.length })
    setPhase('diffing')

    const ctrl = new AbortController()
    abortRef.current = ctrl

    // Phase 1: look up each unique identifier against the catalogue.
    await runPool(identified, POOL_SIZE, ctrl.signal, async (book) => {
      const value = book.asin ?? book.isbn
      if (!value) return
      try {
        const match = await lookup(book.asin ? 'asin' : 'isbn', value, ctrl.signal)
        if (match) inDatabase.push(book)
        else if (isContributableOnMiss(book)) misses.push(book)
        else cannotMatch.push(book) // not found, but unknown language -> cannot auto-match
      } catch {
        if (ctrl.signal.aborted) return
        cannotMatch.push(book) // a real lookup failure, counted, never treated as new
      } finally {
        if (!ctrl.signal.aborted) {
          setProgress((p) => ({ ...p, done: p.done + 1 }))
        }
      }
    })
    if (ctrl.signal.aborted) return

    // Phase 2: for every miss, decide new-work vs new-recording by checking
    // whether the work is already catalogued. The ASIN missed, so we can't look
    // up by id; instead search each distinct author once (cached) - a clean,
    // FTS-indexed query - and match the work title locally.
    setMatching(true)
    const worksByAuthor = new Map<string, WorkCandidate[]>()
    await runPool([...authorSearchKeys(misses)], POOL_SIZE, ctrl.signal, async ([key, name]) => {
      try {
        const res = await search(name, AUTHOR_WORKS_LIMIT, ctrl.signal)
        let works: WorkCandidate[] = res.results
          .filter((r): r is Extract<SearchResult, { kind: 'work' }> => r.kind === 'work')
          .map(toCandidate)
        // A prolific author can have more works than the search cap returns. When
        // the result is truncated, resolve the author's person id and pull the
        // complete authored list, so an existing work past the cap still matches.
        if (res.results.length >= AUTHOR_WORKS_LIMIT) {
          const person = res.results.find(
            (r): r is Extract<SearchResult, { kind: 'person' }> =>
              r.kind === 'person' && authorKey(r.name) === key
          )
          if (person) {
            try {
              const p = await getPerson(person.id, ctrl.signal)
              works = dedupeCandidates([...works, ...p.authored.map(toCandidate)])
            } catch {
              // Keep the (truncated) search works if the person fetch fails.
            }
          }
        }
        worksByAuthor.set(key, works)
      } catch {
        if (!ctrl.signal.aborted) worksByAuthor.set(key, [])
      }
    })
    if (ctrl.signal.aborted) return

    const newBooks: NewBook[] = misses.map((book) => ({
      book,
      existingWork: matchExistingWork(book, candidatesForBook(book, worksByAuthor)),
    }))

    abortRef.current = null
    setResults({
      inDatabase,
      newBooks,
      cannotMatch,
      noIdentifier: unidentified.length,
      total: totalBooks,
      skipped,
    })
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
    const data = JSON.stringify(newBooksPayload(results.newBooks.map((n) => n.book)), null, 2)
    downloadJson(data, 'audiosilo-meta-new-books.json')
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
            <span className="text-lg font-semibold text-hi">Drop your library export here</span>
            <span className="text-sm text-dim">
              or <span className="text-pink-400">choose a file</span> - OpenAudible, Libation,
              Audiobookshelf, or an audiosilo folder scan
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
        ? 'This does not look like a supported export'
        : 'We could not read that file'
    const body =
      phase === 'unknown'
        ? 'We could not find any books in this file. Make sure you selected an OpenAudible books.json, a Libation library export, an Audiobookshelf export, or an audiosilo folder scan. If you use a different tool, you can still contribute your library through the import issue form.'
        : errorMsg
    return (
      <IconCard icon="x" heading={heading}>
        <p className="mt-2 text-sm leading-relaxed text-body">{body}</p>
        <IssueFormActions onReset={reset} />
      </IconCard>
    )
  }

  if (phase === 'diffing') {
    const total = progress.total
    const shown = total === 0 ? 0 : Math.min(progress.done + 1, total)
    return (
      <div className="rounded-2xl border border-edge bg-surface p-6">
        <div className="flex items-center justify-between gap-4">
          <p className="text-sm font-medium text-hi" aria-live="polite">
            {matching
              ? 'Matching your editions...'
              : total === 0
                ? 'Sorting your library...'
                : `Checking ${shown} of ${total}...`}
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
  const stat = (value: number, label: string, tone: string, note?: string) => (
    <div className="rounded-2xl border border-edge bg-surface p-6 text-center">
      <div className={`text-4xl font-bold ${tone}`}>{value.toLocaleString()}</div>
      <div className="mt-2 text-sm text-dim">{label}</div>
      {note ? <div className="mt-1 text-xs text-dim">{note}</div> : null}
    </div>
  )

  // "Cannot auto-match" fuses two reasons: books with no ASIN/ISBN (never
  // checkable) and books that were looked up but missed. Annotate the number with
  // its no-identifier share whenever there is one, so it never reads as "missing"
  // at any fraction; the prominent callout below only escalates when that share
  // dominates (isIdentifierPoor).
  const noIdentifierNote =
    results.noIdentifier > 0
      ? `${results.noIdentifier.toLocaleString()} have no identifier to check`
      : undefined

  return (
    <div className="space-y-8">
      <div className="grid gap-4 sm:grid-cols-3">
        {stat(results.inDatabase.length, 'In the database', 'text-success')}
        {stat(results.newBooks.length, 'New - you can contribute these', 'text-pink-400')}
        {stat(results.cannotMatch.length, 'Cannot auto-match', 'text-dim', noIdentifierNote)}
      </div>

      {isIdentifierPoor(results.noIdentifier, results.total) ? (
        <IconCard icon="database" heading="Most of this export has no identifier">
          <p className="mt-2 text-sm leading-relaxed text-body">
            {results.noIdentifier.toLocaleString()} of {results.total.toLocaleString()} books
            carry no ASIN or ISBN, so they could not be checked against the database. That is not
            the same as being missing from it - without an identifier there is no reliable way to
            match a book, so most of the &ldquo;Cannot auto-match&rdquo; count is simply
            &ldquo;could not be checked&rdquo;. An export like this usually means the library was
            never matched against a metadata provider.
          </p>
          <p className="mt-3 text-sm leading-relaxed text-body">
            To check these books, match your library against a provider in your audiobook app (in
            Audiobookshelf, use <span className="text-hi">Match</span>) and export again, or
            import an OpenAudible or Libation export - both include the identifiers.
          </p>
        </IconCard>
      ) : null}

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
              {results.newBooks.map((n, i) => {
                const b = n.book
                const line = metaLine(b)
                const href = n.existingWork
                  ? addRecordingIssueUrl(b, n.existingWork)
                  : addWorkIssueUrl(b)
                return (
                  <li
                    key={i}
                    className="flex flex-col gap-3 rounded-xl border border-edge bg-raised p-4 sm:flex-row sm:items-center sm:justify-between"
                  >
                    <div className="min-w-0">
                      <p className="font-medium text-hi">{b.title}</p>
                      {line ? <p className="mt-1 text-sm text-dim">{line}</p> : null}
                      {n.existingWork ? (
                        <p className="mt-1 text-xs text-pink-300">
                          New narration of an existing work: {n.existingWork.title}
                        </p>
                      ) : null}
                    </div>
                    <a
                      href={href}
                      target="_blank"
                      rel="noopener"
                      className={`${BTN_SECONDARY} shrink-0 px-4 py-2 text-sm`}
                    >
                      <Icon name="external" className="h-4 w-4" />
                      {n.existingWork ? 'Add a recording' : 'Contribute this book'}
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
