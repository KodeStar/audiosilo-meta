import { useEffect, useMemo, useState } from 'react'
import { getWork, getSeries, href, personNames, type Character, type Work } from '../../lib/api'
import { addCharactersIssueUrl, addRecapsIssueUrl } from '../../lib/github-prefill'
import { downloadJson } from '../../lib/download'
import {
  buildCharactersObject,
  buildRecapsObject,
  emptyCharacterDraft,
  emptyRecapEntry,
  emptyRecapsDraft,
  moveItem,
  nearestLowerSibling,
  seedCharactersFromSibling,
  serializeCanonical,
  slugify,
  validateCharacters,
  validateRecaps,
  type CharacterDraft,
  type CharactersValidation,
  type RecapsDraft,
  type RecapsValidation,
} from '../../lib/builder'
import {
  useEntity,
  useQueryParam,
  usePageTitle,
  DetailSpinner,
  DetailError,
} from '../detail/detail-common'
import CharactersEditor from './CharactersEditor'
import RecapsEditor from './RecapsEditor'
import { BTN_PRIMARY, BTN_SECONDARY, Icon } from '../ui'

type Kind = 'characters' | 'recaps'

const META_REPO = 'https://github.com/kodestar/audiosilo-meta'
const AUTHORING_URL = `${META_REPO}/blob/main/AUTHORING.md`

export default function BuildTool() {
  const workId = useQueryParam('work')
  const kindParam = useQueryParam('kind')
  const kind: Kind = kindParam === 'recaps' ? 'recaps' : 'characters'
  const work = useEntity<Work>(workId, getWork)

  usePageTitle(
    kind === 'recaps' ? 'Build a story-so-far recap' : 'Build a character sidecar'
  )

  if (work.status === 'loading') return <DetailSpinner />
  if (work.status === 'error') {
    // No ?work= at all - point the contributor at the coverage page rather than
    // showing a bare 404 for a work they never named.
    if (workId === '') return <NoWork />
    return <DetailError notFound={work.notFound} kind="work" />
  }
  return <Builder work={work.data} kind={kind} />
}

function NoWork() {
  return (
    <div className="container py-24 text-center">
      <h1 className="text-2xl font-bold text-hi">Pick a book to write about</h1>
      <p className="mx-auto mt-3 max-w-md text-body">
        Open a work in the database and use its &quot;Add characters&quot; or &quot;Add story so
        far&quot; button, or browse the books that still need them.
      </p>
      <div className="mt-8 flex flex-wrap items-center justify-center gap-3">
        <a href="/contribute" className={BTN_PRIMARY}>
          Books that need them
        </a>
        <a href="/" className={BTN_SECONDARY}>
          Search the database
        </a>
      </div>
    </div>
  )
}

function Builder({ work, kind }: { work: Work; kind: Kind }) {
  const [chars, setChars] = useState<CharacterDraft[]>(() => [emptyCharacterDraft()])
  const [recaps, setRecaps] = useState<RecapsDraft>(() => emptyRecapsDraft())
  const [seed, setSeed] = useState<{ title: string; cast: Character[] } | null>(null)
  const [copied, setCopied] = useState(false)

  // Offer to seed the cast from the nearest earlier book in the series (the
  // re-describe-per-book model). Best-effort: any failure just means no offer.
  useEffect(() => {
    if (kind !== 'characters') {
      setSeed(null)
      return
    }
    const seriesRef = work.series?.[0]
    if (!seriesRef) return
    const ctrl = new AbortController()
    void (async () => {
      try {
        const series = await getSeries(seriesRef.id, ctrl.signal)
        const sibling = nearestLowerSibling(series.works, work.id)
        if (!sibling) return
        const siblingWork = await getWork(sibling.work.id, ctrl.signal)
        if (siblingWork.characters && siblingWork.characters.length > 0) {
          setSeed({ title: siblingWork.title, cast: siblingWork.characters })
        }
      } catch {
        // Seeding is a convenience; a failed lookup is silently skipped.
      }
    })()
    return () => ctrl.abort()
  }, [work, kind])

  // --- Character editor handlers ------------------------------------------
  function setCharName(index: number, name: string) {
    setChars((prev) =>
      prev.map((d, i) => {
        if (i !== index) return d
        // Auto-derive the id from the name until the user edits the id by hand.
        const tracks = d.id === '' || d.id === slugify(d.name)
        return { ...d, name, id: tracks ? slugify(name) : d.id }
      })
    )
  }
  function patchChar(index: number, patch: Partial<CharacterDraft>) {
    setChars((prev) => prev.map((d, i) => (i === index ? { ...d, ...patch } : d)))
  }
  function addChar() {
    setChars((prev) => [...prev, emptyCharacterDraft()])
  }
  function removeChar(index: number) {
    setChars((prev) => (prev.length <= 1 ? [emptyCharacterDraft()] : prev.filter((_, i) => i !== index)))
  }
  function moveChar(index: number, dir: -1 | 1) {
    setChars((prev) => moveItem(prev, index, dir))
  }
  function applySeed() {
    if (!seed) return
    setChars(seedCharactersFromSibling(seed.cast))
    setSeed(null)
  }

  // --- Recap editor handlers ----------------------------------------------
  function patchEntry(index: number, patch: Partial<RecapsDraft['entries'][number]>) {
    setRecaps((prev) => ({
      ...prev,
      entries: prev.entries.map((e, i) => (i === index ? { ...e, ...patch } : e)),
    }))
  }
  function addEntry() {
    setRecaps((prev) => ({ ...prev, entries: [...prev.entries, emptyRecapEntry()] }))
  }
  function removeEntry(index: number) {
    setRecaps((prev) => ({
      ...prev,
      entries:
        prev.entries.length <= 1
          ? [emptyRecapEntry()]
          : prev.entries.filter((_, i) => i !== index),
    }))
  }
  function moveEntry(index: number, dir: -1 | 1) {
    setRecaps((prev) => ({ ...prev, entries: moveItem(prev.entries, index, dir) }))
  }
  function setSummary(field: 'inShort' | 'ending', value: string) {
    setRecaps((prev) => ({ ...prev, [field]: value }))
  }

  // --- Output -------------------------------------------------------------
  // The derived pipeline (validate -> build -> serialize) runs on every
  // keystroke, so it is memoized and only the ACTIVE kind's work is done - the
  // inactive editor's drafts are untouched state, never validated or serialized.
  const isChars = kind === 'characters'
  const output = useMemo<
    | { kind: 'characters'; val: CharactersValidation; json: string }
    | { kind: 'recaps'; val: RecapsValidation; json: string }
  >(() => {
    if (kind === 'characters') {
      return {
        kind,
        val: validateCharacters(chars),
        json: serializeCanonical(buildCharactersObject(work.id, chars)),
      }
    }
    return {
      kind,
      val: validateRecaps(recaps),
      json: serializeCanonical(buildRecapsObject(work.id, recaps)),
    }
  }, [chars, recaps, kind, work.id])
  const json = output.json
  const valid = output.val.ok
  const formErrors = output.val.form
  const filename = isChars ? 'characters.json' : 'recaps.json'
  const issueUrl = isChars ? addCharactersIssueUrl(work.id) : addRecapsIssueUrl(work.id)

  function download() {
    downloadJson(json, filename)
  }

  function copyJson() {
    void navigator.clipboard?.writeText(json).then(
      () => {
        setCopied(true)
        setTimeout(() => setCopied(false), 1500)
      },
      () => {}
    )
  }

  const seriesRef = work.series?.[0]

  return (
    <section className="relative pb-20">
      <div className="glow left-[-8rem] top-0 h-[26rem] w-[26rem]"></div>
      <div className="container relative z-10">
        {/* Header */}
        <div className="reveal mx-auto max-w-3xl">
          <a
            href={href.work(work.id)}
            className="inline-flex items-center gap-1.5 text-sm text-dim transition-colors hover:text-pink-400"
          >
            <Icon name="up" className="h-4 w-4 rotate-[-90deg]" />
            Back to {work.title}
          </a>
          <span className="mt-4 block text-xs font-semibold uppercase tracking-[0.2em] text-pink-500">
            Contribute
          </span>
          <h1 className="mt-2 text-3xl font-bold tracking-tight text-hi sm:text-4xl">
            {isChars ? 'Build a character sidecar' : 'Build a story-so-far recap'}
          </h1>
          <p className="mt-3 text-lg leading-relaxed text-body">
            {isChars ? 'The spoiler-tagged cast' : 'Position-keyed recaps'} for{' '}
            <span className="font-medium text-hi">{work.title}</span>
            {work.authors.length ? ` by ${personNames(work.authors)}` : ''}
            {seriesRef ? (
              <>
                {' '}
                <span className="text-dim">({seriesRef.name})</span>
              </>
            ) : null}
            .
          </p>
        </div>

        {/* The two-column workspace: editor + live output */}
        <div className="mx-auto mt-10 grid max-w-6xl gap-8 lg:grid-cols-[minmax(0,1fr)_22rem]">
          <div>
            <Explainer isChars={isChars} />

            {isChars && seed ? (
              <div className="mb-6 flex flex-col gap-3 rounded-2xl border border-pink-500/40 bg-pink-600/10 p-5 sm:flex-row sm:items-center sm:justify-between">
                <p className="text-sm leading-relaxed text-pink-100">
                  <span className="font-semibold">{seed.title}</span> already has a cast. Start from
                  it - each character is copied with its description cleared and reveal reset, ready
                  to re-check for this book.
                </p>
                <div className="flex shrink-0 items-center gap-3">
                  <button
                    type="button"
                    onClick={applySeed}
                    className={`${BTN_SECONDARY} px-4 py-2 text-sm`}
                  >
                    <Icon name="wand" className="h-4 w-4" />
                    Start from that cast
                  </button>
                  <button
                    type="button"
                    aria-label="Dismiss"
                    onClick={() => setSeed(null)}
                    className="text-dim transition-colors hover:text-hi"
                  >
                    <Icon name="x" className="h-5 w-5" />
                  </button>
                </div>
              </div>
            ) : null}

            {output.kind === 'characters' ? (
              <CharactersEditor
                drafts={chars}
                errors={output.val.cards}
                onName={setCharName}
                onPatch={patchChar}
                onAdd={addChar}
                onRemove={removeChar}
                onMove={moveChar}
              />
            ) : (
              <RecapsEditor
                draft={recaps}
                errors={output.val}
                onEntry={patchEntry}
                onAdd={addEntry}
                onRemove={removeEntry}
                onMove={moveEntry}
                onSummary={setSummary}
              />
            )}
          </div>

          {/* Output panel */}
          <aside className="lg:sticky lg:top-24 lg:self-start">
            <div className="rounded-2xl border border-edge bg-surface p-5">
              <div className="flex items-center justify-between gap-2">
                <h2 className="text-sm font-semibold text-hi">{filename}</h2>
                <button
                  type="button"
                  onClick={copyJson}
                  className="text-xs font-medium text-pink-400 transition-colors hover:text-pink-300"
                >
                  {copied ? 'Copied' : 'Copy'}
                </button>
              </div>
              <pre className="mt-3 max-h-80 overflow-auto rounded-lg border border-edge bg-deep p-3 text-xs leading-relaxed text-body">
                <code>{json}</code>
              </pre>

              {!valid ? (
                <div className="mt-4 rounded-lg border border-red-500/30 bg-red-500/5 px-3 py-2 text-xs leading-relaxed text-red-300">
                  <p className="font-medium">Fix these before downloading:</p>
                  <ul className="mt-1 list-disc space-y-0.5 pl-4">
                    {formErrors.map((msg, i) => (
                      <li key={`f${i}`}>{msg}</li>
                    ))}
                    <li>Every highlighted field above.</li>
                  </ul>
                </div>
              ) : null}

              <div className="mt-4 flex flex-col gap-3">
                <button
                  type="button"
                  onClick={download}
                  disabled={!valid}
                  className={`${BTN_PRIMARY} w-full px-4 py-2.5 text-sm`}
                >
                  <Icon name="download" className="h-4 w-4" />
                  Download {filename}
                </button>
                <a
                  href={issueUrl}
                  target="_blank"
                  rel="noopener"
                  className={`${BTN_SECONDARY} w-full px-4 py-2.5 text-sm`}
                >
                  <Icon name="external" className="h-4 w-4" />
                  Open a prefilled issue
                </a>
              </div>
            </div>

            <Handoff filename={filename} authoringUrl={AUTHORING_URL} />
          </aside>
        </div>
      </div>
    </section>
  )
}

function Explainer({ isChars }: { isChars: boolean }) {
  return (
    <div className="mb-6 rounded-2xl border border-edge bg-raised p-5 text-sm leading-relaxed text-body">
      <p>
        <span className="font-semibold text-hi">Positions are the book&apos;s own chapters.</span>{' '}
        Use the logical chapter number as printed (edition-independent, not the audiobook track).
        Chapter <span className="tabular-nums">0</span> means front matter or knowledge carried from
        earlier books.
      </p>
      {isChars ? (
        <p className="mt-2">
          The <span className="font-semibold text-hi">reveal</span> chapter is where a character is
          first meaningfully introduced in this book, and it gates the spoiler: a reader only sees
          the card once they have reached it. Describe each character for that moment - do not fold
          in a later twist.
        </p>
      ) : (
        <p className="mt-2">
          Each recap&apos;s <span className="font-semibold text-hi">through</span> chapter gates it:
          it is shown only once the listener has finished that chapter, and may reveal everything up
          to and including it. The <span className="font-semibold text-hi">final</span> entry must
          state the actual ending plainly - a tease is a defect, not a style.
        </p>
      )}
      <p className="mt-2 text-dim">
        Own words only, neutral reference-guide voice - no verbatim text, jokes, or editorializing.
        This is the community <span className="font-medium text-pink-300">CC BY-SA 3.0</span> layer.
      </p>
    </div>
  )
}

function Handoff({ filename, authoringUrl }: { filename: string; authoringUrl: string }) {
  return (
    <div className="mt-5 rounded-2xl border border-edge bg-surface p-5">
      <h2 className="text-sm font-semibold text-hi">How to submit</h2>
      <ol className="mt-3 space-y-2 text-sm leading-relaxed text-body">
        <li className="flex gap-2">
          <Step n={1} />
          <span>
            Download <code className="text-pink-300">{filename}</code> above.
          </span>
        </li>
        <li className="flex gap-2">
          <Step n={2} />
          <span>Open the prefilled issue - the work is filled in for you.</span>
        </li>
        <li className="flex gap-2">
          <Step n={3} />
          <span>
            Drag the downloaded file into the attachment box in the issue (long-form JSON cannot
            ride in the link).
          </span>
        </li>
        <li className="flex gap-2">
          <Step n={4} />
          <span>Tick the boxes and submit. A maintainer reviews and merges it.</span>
        </li>
      </ol>
      <p className="mt-4 text-xs leading-relaxed text-dim">
        Comfortable with Git? You can instead add{' '}
        <code className="text-pink-300">{filename}</code> under the work&apos;s directory and open a
        pull request -{' '}
        <a
          href={authoringUrl}
          target="_blank"
          rel="noopener"
          className="text-pink-400 transition-colors hover:text-pink-300"
        >
          the authoring guide
        </a>{' '}
        has the details.
      </p>
    </div>
  )
}

function Step({ n }: { n: number }) {
  return (
    <span className="mt-0.5 inline-flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-pink-600/20 text-xs font-semibold text-pink-300">
      {n}
    </span>
  )
}
