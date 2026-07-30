// The /add lookup assist: search libex (a public Audible metadata mirror), pick
// the right edition, review the facts we would propose, and land on a prefilled
// add-work issue. Every decision lives in the pure modules (lib/libex.ts,
// lib/libex-map.ts, lib/github-prefill.ts) - this island is chrome and state.
//
// Two things it deliberately never does: it never fetches anything before the
// reader asks (every call follows a submit or a click), and it never shows or
// carries a description, summary or rating - the whitelist parser makes that
// structural, and the copy says so out loud.

import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { formatRuntime, formatYear, href, lookup, personNames } from '../../lib/api'
import type { LookupResponse } from '../../lib/api'
import { libexBook, libexSearch, LIBEX_BASE, LIBEX_HOST } from '../../lib/libex'
import type { LibexBook } from '../../lib/libex'
import {
  canOpenPrefilledIssue,
  prefillFacts,
  resolveLibexCandidate,
  routeQuery,
} from '../../lib/libex-map'
import type { LibexPrefill, LibexResolvers } from '../../lib/libex-map'
import { ADD_QUERY_PARAM } from '../../lib/search-cta'
import { addWorkIssueFormUrl, addWorkIssueUrlFromLibex } from '../../lib/github-prefill'
import CoverImage from '../cards/CoverImage'
import { DetailSpinner } from '../detail/detail-common'
import { BTN_PRIMARY, BTN_SECONDARY, Icon } from '../ui'

/** What the reader is looking at. Each phase carries only what its card renders
    - the resolver (lib/libex-map) decided which one applies. */
type Phase =
  | { kind: 'idle' }
  | { kind: 'searching' }
  | { kind: 'results'; results: LibexBook[]; query: string }
  | { kind: 'checking' }
  | { kind: 'covered'; asin: string; match: LookupResponse }
  | { kind: 'propose'; prefill: LibexPrefill }
  | { kind: 'failed'; message: string }

const LOOKUP_FAILED =
  'The Libex lookup service could not be reached. It is a separate, community-run service, so this is usually temporary.'

/** The resolver's two calls, bound to the real services. Module-level so the
    identity is stable across renders. */
const RESOLVERS: LibexResolvers = {
  fetchBook: libexBook,
  fetchLookup: (asin, signal) => lookup('asin', asin, signal),
}

/** The card wrapper the two spinners share with every other panel here. */
const SPINNER_CARD = 'rounded-2xl border border-edge bg-surface p-10 text-center'

/** A result list kept aside so a reader who picked the wrong row can go back to
    it. libex lists ONE ROW PER MARKETPLACE, so several rows carry the same title
    and a mis-pick is likely rather than exceptional. */
type RememberedResults = { results: LibexBook[]; query: string }

export default function AddFromLibex() {
  const [query, setQuery] = useState('')
  const [phase, setPhase] = useState<Phase>({ kind: 'idle' })
  const [lastResults, setLastResults] = useState<RememberedResults | null>(null)
  // One controller per in-flight request set, so a new search or a second pick
  // cancels the previous one instead of racing it.
  const ctrlRef = useRef<AbortController | null>(null)

  const abortPrevious = useCallback(() => {
    ctrlRef.current?.abort()
    const ctrl = new AbortController()
    ctrlRef.current = ctrl
    return ctrl
  }, [])

  const runSearch = useCallback(
    async (q: string) => {
      const term = q.trim()
      if (!term) return
      const ctrl = abortPrevious()
      setPhase({ kind: 'searching' })
      try {
        const results = await libexSearch(term, ctrl.signal)
        if (ctrl.signal.aborted) return
        // Only a list with something in it is worth going back TO.
        if (results.length) setLastResults({ results, query: term })
        setPhase({ kind: 'results', results, query: term })
      } catch (err) {
        if (ctrl.signal.aborted || (err as Error).name === 'AbortError') return
        setPhase({ kind: 'failed', message: LOOKUP_FAILED })
      }
    },
    [abortPrevious]
  )

  /** Take one candidate ASIN to its verdict. `row` is the search result the
      reader clicked, when there was one - it is already the whole record, so
      the resolver skips the record fetch entirely (lib/libex-map). */
  const resolve = useCallback(
    async (asin: string, row?: LibexBook) => {
      const ctrl = abortPrevious()
      setPhase({ kind: 'checking' })
      try {
        const outcome = await resolveLibexCandidate(asin, row, RESOLVERS, ctrl.signal)
        if (ctrl.signal.aborted) return
        if (outcome.kind === 'missing') {
          setPhase({
            kind: 'failed',
            message: `Libex has no record for ${outcome.asin}. Search for the title instead, or fill the form in by hand.`,
          })
          return
        }
        setPhase(outcome)
      } catch (err) {
        if (ctrl.signal.aborted || (err as Error).name === 'AbortError') return
        setPhase({ kind: 'failed', message: LOOKUP_FAILED })
      }
    },
    [abortPrevious]
  )

  /** The one entry point for a typed/carried term. routeQuery (lib/libex-map)
      owns the decision - including that a product code is recognised whatever
      case it was pasted in - so the form and the ?q= hand-off cannot diverge. */
  const submit = useCallback(
    (raw: string) => {
      const route = routeQuery(raw)
      if (!route) return
      if (route.kind === 'asin') void resolve(route.asin)
      else void runSearch(route.q)
    },
    [resolve, runSearch]
  )

  // Read the carried query once on mount and act on it. This is the ONLY
  // automatic call: the reader arrived from a search that already failed, so the
  // lookup is still something they asked for.
  useEffect(() => {
    const q = new URLSearchParams(window.location.search).get(ADD_QUERY_PARAM)?.trim() ?? ''
    if (!q) return
    setQuery(q)
    submit(q)
    // Mount only - later searches come from the form.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  useEffect(() => () => ctrlRef.current?.abort(), [])

  /** Re-show the list the reader picked from. Offered on every verdict card,
      because "wrong row" is the most likely reason to be looking at one. */
  const backToResults = useCallback(() => {
    ctrlRef.current?.abort()
    setPhase((prev) =>
      lastResults ? { kind: 'results', results: lastResults.results, query: lastResults.query } : prev
    )
  }, [lastResults])
  const back = lastResults && phase.kind !== 'results' ? backToResults : null

  return (
    <div className="space-y-6">
      <form
        onSubmit={(e) => {
          e.preventDefault()
          submit(query)
        }}
        className="flex flex-col gap-3 sm:flex-row"
      >
        <label htmlFor="libex-q" className="sr-only">
          Search Libex by title, author or ASIN
        </label>
        <input
          id="libex-q"
          type="search"
          value={query}
          autoComplete="off"
          spellCheck={false}
          placeholder="Title, author, or an ASIN"
          onChange={(e) => setQuery(e.target.value)}
          className="w-full rounded-xl border border-edge bg-surface px-4 py-3 text-hi placeholder:text-dim transition-colors focus:border-pink-500/60 focus:outline-none"
        />
        <button
          type="submit"
          disabled={!query.trim() || phase.kind === 'searching'}
          className={`${BTN_PRIMARY} shrink-0`}
        >
          {phase.kind === 'searching' ? 'Searching...' : 'Search Libex'}
        </button>
      </form>

      {phase.kind === 'idle' ? (
        <p className="rounded-2xl border border-edge bg-surface p-6 text-sm leading-relaxed text-body">
          Search the Audible catalogue through{' '}
          <a
            href={LIBEX_BASE}
            target="_blank"
            rel="noopener"
            className="text-pink-400 transition-colors hover:text-pink-300"
          >
            {LIBEX_HOST}
          </a>
          , pick the exact narration you have, and check the facts we would propose. The
          last step opens a prefilled GitHub issue - nothing is submitted until you send
          it.
        </p>
      ) : null}

      {phase.kind === 'searching' ? (
        <DetailSpinner label="Searching Libex..." className={SPINNER_CARD} />
      ) : null}
      {phase.kind === 'checking' ? (
        <DetailSpinner
          label="Checking whether this book is already in the database..."
          className={SPINNER_CARD}
        />
      ) : null}

      {phase.kind === 'results' ? (
        <Results
          results={phase.results}
          query={phase.query}
          onChoose={(book) => void resolve(book.asin, book)}
        />
      ) : null}

      {phase.kind === 'covered' ? (
        <AlreadyHere asin={phase.asin} match={phase.match} onBack={back} />
      ) : null}

      {phase.kind === 'propose' ? <Confirm prefill={phase.prefill} onBack={back} /> : null}

      {phase.kind === 'failed' ? (
        <div className="rounded-2xl border border-edge bg-surface p-6">
          <h2 className="font-semibold text-hi">The lookup did not work</h2>
          <p className="mt-2 text-sm leading-relaxed text-body">{phase.message}</p>
          <p className="mt-2 text-sm leading-relaxed text-body">
            You can still add the book by hand - the link below opens the same form with
            nothing filled in.
          </p>
          {back ? (
            <div className="mt-5">
              <BackToResults onBack={back} />
            </div>
          ) : null}
        </div>
      ) : null}

      {/* The manual route is offered in EVERY state, so a reader who cannot find
          their edition - or whose lookup just failed - is never stuck here. */}
      <p className="text-center text-sm text-dim">
        Not finding the right edition?{' '}
        <a
          href={addWorkIssueFormUrl}
          target="_blank"
          rel="noopener"
          className="text-pink-400 underline-offset-2 transition-colors hover:text-pink-300 hover:underline"
        >
          Fill the form by hand
        </a>{' '}
        instead.
      </p>
    </div>
  )
}

function Results({
  results,
  query,
  onChoose,
}: {
  results: LibexBook[]
  query: string
  onChoose: (book: LibexBook) => void
}) {
  if (!results.length) {
    return (
      <div className="rounded-2xl border border-edge bg-surface p-6 text-center">
        <p className="text-sm text-body">
          Libex has no match for &ldquo;{query}&rdquo;.
        </p>
        <p className="mt-2 text-sm text-dim">
          Try the author's name, or a shorter title. Series volumes are often listed under
          the volume title alone.
        </p>
      </div>
    )
  }
  return (
    <div className="rounded-2xl border border-edge bg-surface p-4 sm:p-6">
      <h2 className="text-sm font-semibold uppercase tracking-wider text-dim">
        {results.length} {results.length === 1 ? 'edition' : 'editions'} on Libex
      </h2>
      <p className="mt-1 text-sm text-body">
        Pick the narration you have - each one is a separate recording, with its own
        narrator and runtime.
      </p>
      <ul className="mt-4 space-y-2">
        {results.map((book) => (
          <li key={`${book.asin}-${book.region ?? ''}`}>
            <button
              type="button"
              onClick={() => onChoose(book)}
              className="flex w-full items-center gap-3 rounded-xl border border-edge bg-raised p-3 text-left transition-colors hover:border-pink-500/60"
            >
              <Cover url={book.imageUrl} />
              <span className="min-w-0 flex-1">
                <span className="block truncate font-medium text-hi">
                  {book.title ?? book.asin}
                </span>
                {book.authors.length ? (
                  <span className="block truncate text-sm text-body">
                    {personNames(book.authors)}
                  </span>
                ) : null}
                <span className="mt-0.5 block truncate text-xs text-dim">
                  {[
                    book.narrators.length ? `Read by ${personNames(book.narrators)}` : null,
                    formatYear(book.releaseDate),
                    formatRuntime(book.lengthMinutes),
                  ]
                    .filter(Boolean)
                    .join(' - ')}
                </span>
              </span>
              {book.region ? (
                <span className="shrink-0 rounded-full border border-edge px-2 py-0.5 text-[0.7rem] uppercase text-dim">
                  {book.region}
                </span>
              ) : null}
            </button>
          </li>
        ))}
      </ul>
    </div>
  )
}

/** The one "I picked the wrong edition" affordance, shared by every verdict
    card so it always looks and reads the same. */
function BackToResults({ onBack }: { onBack: () => void }) {
  return (
    <button type="button" onClick={onBack} className={BTN_SECONDARY}>
      Back to results
    </button>
  )
}

function AlreadyHere({
  asin,
  match,
  onBack,
}: {
  asin: string
  match: LookupResponse
  onBack: (() => void) | null
}) {
  return (
    <div className="rounded-2xl border border-edge bg-surface p-6">
      <span className="inline-flex items-center gap-2 rounded-full bg-pink-600/15 px-3 py-1 text-xs font-semibold text-pink-300">
        Already in the catalogue
      </span>
      <h2 className="mt-3 text-lg font-bold text-hi">{match.work.title}</h2>
      {match.work.authors?.length ? (
        <p className="mt-1 text-sm text-body">{personNames(match.work.authors)}</p>
      ) : null}
      <p className="mt-3 text-sm leading-relaxed text-body">
        {asin} is already catalogued, so there is nothing to add here. Open the work
        to check its recordings - if a narration you own is missing, you can add that one.
      </p>
      <div className="mt-5 flex flex-wrap gap-3">
        <a href={href.work(match.work.id)} className={BTN_PRIMARY}>
          Open the work
        </a>
        {onBack ? <BackToResults onBack={onBack} /> : null}
      </div>
    </div>
  )
}

function Confirm({
  prefill,
  onBack,
}: {
  prefill: LibexPrefill
  onBack: (() => void) | null
}) {
  // Both derivations walk the whole prefill; the card re-renders on unrelated
  // parent state (the search box the reader can keep typing in), so keep them.
  const facts = useMemo(() => prefillFacts(prefill), [prefill])
  const issueUrl = useMemo(() => addWorkIssueUrlFromLibex(prefill), [prefill])
  // A record libex has no title for cannot open a usable issue (the rule and the
  // reasoning live in lib/libex-map, beside the rest of the form contract), so
  // the hand-off becomes the manual form rather than a submission that is
  // rejected on arrival.
  const titled = canOpenPrefilledIssue(prefill)
  return (
    <div className="rounded-2xl border border-edge bg-surface p-6">
      <h2 className="text-lg font-bold text-hi">Check the facts, then continue</h2>
      <p className="mt-2 text-sm leading-relaxed text-body">
        This is exactly what the prefilled issue will carry. Anything marked{' '}
        <span className="text-dim">not stated</span> is left blank for you to complete on
        GitHub - the title, author(s), narrator(s) and language are all required, so fill
        in any of those that are empty.
      </p>

      <dl className="mt-5 divide-y divide-edge border-y border-edge">
        {facts.map((fact) => (
          <div key={fact.key} className="grid grid-cols-1 gap-1 py-2.5 sm:grid-cols-[10rem_1fr] sm:gap-4">
            <dt className="text-sm text-dim">{fact.label}</dt>
            <dd className="text-sm text-hi">
              {fact.value === null ? (
                <span className="text-dim">not stated</span>
              ) : fact.key === 'cover' ? (
                <span className="flex items-center gap-3">
                  <Cover url={fact.value} />
                  <span className="min-w-0 break-all text-xs text-body">{fact.value}</span>
                </span>
              ) : fact.key === 'sources' ? (
                // The provenance text is multi-line (the marker line, the human
                // line, and a note for any stated fact no field could carry), so
                // keep its line breaks.
                <span className="whitespace-pre-line text-xs text-body">{fact.value}</span>
              ) : (
                fact.value
              )}
            </dd>
          </div>
        ))}
      </dl>

      <p className="mt-4 text-sm leading-relaxed text-dim">
        Descriptions, summaries and ratings are never imported - the database keeps
        factual metadata only, and covers are linked from the publisher's hosting rather
        than copied. See the{' '}
        <a
          href="https://github.com/kodestar/audiosilo-meta/blob/main/LICENSING.md"
          target="_blank"
          rel="noopener"
          className="text-pink-400 transition-colors hover:text-pink-300"
        >
          licensing terms
        </a>
        .
      </p>

      {titled ? null : (
        <p className="mt-4 rounded-xl border border-edge bg-raised p-4 text-sm leading-relaxed text-body">
          Libex states no title for {prefill.asin}, and the issue form requires one - so
          there is nothing useful to prefill here. Add the book by hand instead, using the
          link below; the details above are still worth copying across.
        </p>
      )}

      <div className="mt-6 flex flex-wrap items-center gap-3">
        {titled ? (
          <a href={issueUrl} target="_blank" rel="noopener" className={BTN_PRIMARY}>
            <Icon name="external" className="h-4 w-4" />
            Continue on GitHub
          </a>
        ) : (
          <a href={addWorkIssueFormUrl} target="_blank" rel="noopener" className={BTN_PRIMARY}>
            <Icon name="external" className="h-4 w-4" />
            Fill the form by hand
          </a>
        )}
        {onBack ? <BackToResults onBack={onBack} /> : null}
      </div>
    </div>
  )
}

/** The 48px cover slot used by a result row and the cover fact. The shared
    CoverImage owns the aspect box and the fallback for a missing OR dead URL
    (Audible's image hosts do 404 on withdrawn titles); the wrapper is what pins
    it to this row's size, since CoverImage is width-driven. No `title` is passed
    - at 48px the fallback's caption would be unreadable, and the row already
    prints the title right beside it. */
function Cover({ url }: { url?: string | null }) {
  return (
    <span className="block w-12 shrink-0">
      <CoverImage src={url} alt="" />
    </span>
  )
}
