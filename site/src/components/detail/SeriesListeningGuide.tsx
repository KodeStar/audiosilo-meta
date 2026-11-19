import { useCallback, useState } from 'react'
import {
  formatLanguage,
  formatRuntime,
  getSeriesListeningGuide,
  href,
  type RecordingSummary,
  type Series,
  type SeriesListeningGuide as Guide,
} from '../../lib/api'
import { abridgementLabel, listeningPlan, narratorContinuity, recordingChoiceLabel } from '../../lib/catalogue'
import CoverImage from '../cards/CoverImage'
import PersonLinks from '../cards/PersonLinks'
import { TEXT_LINK } from '../ui'
import CataloguePager from './CataloguePager'
import { useEntity, useInitialFragmentScroll } from './detail-common'

function ContinuityGrid({ credits }: { credits: ReturnType<typeof narratorContinuity> }) {
  return (
    <ul className="grid gap-2 sm:grid-cols-2 lg:grid-cols-3">
      {credits.map(({ person, works }) => (
        <li key={person.id} className="flex min-w-0 items-center justify-between gap-3 rounded-lg border border-edge bg-deep/40 px-3 py-2.5 text-sm">
          <span className="min-w-0 break-words"><PersonLinks people={[person]} /></span>
          <span className="shrink-0 text-xs tabular-nums text-dim">{works} works</span>
        </li>
      ))}
    </ul>
  )
}

/** Native disclosures keep every credit available without letting long casts
    dominate the guide. Multiple names alone do not identify a cast format. */
export function NarratorContinuity({ entries }: { entries: Guide['entries'] }) {
  const continuity = narratorContinuity(entries)
  return (
    <div className="mt-6 rounded-xl border border-edge bg-surface p-4 sm:p-5">
      <div className="mb-3 flex flex-wrap items-baseline justify-between gap-2">
        <h3 className="font-medium text-hi">Narrator continuity</h3>
        <span className="text-xs text-dim">Recurring credits across the choices on this page</span>
      </div>
      {continuity.length ? (
        <>
          <ContinuityGrid credits={continuity.slice(0, 6)} />
          {continuity.length > 6 ? (
            <details className="mt-3">
              <summary className={`cursor-pointer py-1 text-sm ${TEXT_LINK}`}>Show {continuity.length - 6} more recurring narrators</summary>
              <div className="mt-3"><ContinuityGrid credits={continuity.slice(6)} /></div>
            </details>
          ) : null}
        </>
      ) : <p className="text-sm text-dim">No recurring narrator credit among these choices.</p>}
    </div>
  )
}

export function GuideRecording({ recording, index, workID }: { recording: RecordingSummary; index: number; workID: string }) {
  const names = recording.narrators
  const manyNames = names.length > 3
  return (
    <li className="min-w-0 rounded-xl border border-edge bg-deep/50 p-4">
      <div className="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1">
        <h4 className="text-sm font-medium text-hi">Recording {index + 1}</h4>
        <a className={`text-xs ${TEXT_LINK}`} href={href.recording(workID, recording.id)}>Recording details <span aria-hidden="true">↗</span></a>
      </div>
      <div className="mt-3 text-sm leading-relaxed">
        <p className="mb-1 text-xs text-dim">Narrated by</p>
        {names.length ? <PersonLinks people={manyNames ? names.slice(0, 2) : names} /> : <span className="text-dim">Narrator unknown</span>}
        {manyNames ? (
          <details className="mt-1">
            <summary className={`cursor-pointer text-xs ${TEXT_LINK}`}>All {names.length} narrator credits</summary>
            <ul className="mt-2 grid gap-1 border-l border-edge pl-3">
              {names.map((person) => <li key={person.id}><PersonLinks people={[person]} /></li>)}
            </ul>
          </details>
        ) : null}
      </div>
      <dl className="mt-4 grid grid-cols-2 gap-x-4 gap-y-3 text-sm">
        <div><dt className="text-xs text-dim">Runtime</dt><dd className="mt-0.5 text-hi">{formatRuntime(recording.runtime_min) || 'Unknown'}</dd></div>
        <div><dt className="text-xs text-dim">Abridgement</dt><dd className="mt-0.5">{abridgementLabel(recording)}</dd></div>
        <div><dt className="text-xs text-dim">Language</dt><dd className="mt-0.5">{formatLanguage(recording.language) || 'Unknown'}</dd></div>
        <div><dt className="text-xs text-dim">Released</dt><dd className="mt-0.5">{recording.release_date || 'Unknown'}</dd></div>
        <div className="col-span-2"><dt className="text-xs text-dim">Publisher</dt><dd className="mt-0.5 break-words">{recording.publisher || 'Unknown'}</dd></div>
      </dl>
    </li>
  )
}

function ListeningPlanner({ plan }: { plan: ReturnType<typeof listeningPlan> }) {
  return (
    <aside className="self-start rounded-2xl border border-pink-500/30 bg-surface p-5 xl:sticky xl:top-24 xl:col-start-2 xl:row-start-1" aria-label="Listening time planner">
      <h3 className="font-medium text-hi">Listening time planner</h3>
      <div className="mt-4" aria-live="polite" aria-atomic="true">
        <p className="text-xs text-dim">Chosen-list subtotal</p>
        <p className="mt-1 text-3xl font-bold tracking-tight text-hi">{plan.minutes > 0 ? formatRuntime(plan.minutes) : 'No known time'}</p>
        <p className="mt-3 text-sm text-body">{plan.selected} recording{plan.selected === 1 ? '' : 's'} chosen on this page</p>
        <p className="mt-3 text-sm leading-relaxed text-dim">{plan.complete ? 'One recording chosen for every catalogued entry, with known runtimes and no overlapping positions.' : 'Partial plan.'}</p>
        {plan.unknownRuntime ? <p className="mt-2 text-sm leading-relaxed text-dim">{plan.unknownRuntime} chosen recording{plan.unknownRuntime === 1 ? ' has' : 's have'} unknown runtime and adds no time to this subtotal.</p> : null}
        {plan.overlap ? <p className="mt-2 text-sm leading-relaxed text-dim">Selected positions overlap, so this subtotal may include repeated coverage. It is not a total for the series.</p> : null}
        {plan.uncertainOrder ? <p className="mt-2 text-sm text-dim">Some positions cannot be checked for overlap.</p> : null}
        {plan.unchosen ? <p className="mt-2 text-sm text-dim">{plan.unchosen} {plan.unchosen === 1 ? 'entry' : 'entries'} on this page still need{plan.unchosen === 1 ? 's' : ''} a recording choice.</p> : null}
        {plan.otherPages ? <p className="mt-2 text-sm leading-relaxed text-dim">More catalogued entries are on other pages. Choices apply to this page only.</p> : null}
      </div>
      <p className="mt-4 border-t border-edge pt-4 text-xs leading-relaxed text-dim">Choose one recording per work below. Only known runtimes are added; alternatives are never combined automatically.</p>
    </aside>
  )
}

function GuideContent({ guide }: { guide: Guide }) {
  const section = useInitialFragmentScroll('listening-guide')
  const [choices, setChoices] = useState<Record<string, string>>({})
  const plan = listeningPlan(guide, choices)
  return (
    <section ref={section} id="listening-guide" className="mt-14 border-t border-edge pt-10">
      <header className="max-w-3xl">
        <p className="text-xs font-semibold uppercase tracking-[0.18em] text-pink-400">Compare and plan</p>
        <h2 className="mt-2 text-3xl font-bold tracking-tight text-hi">Listening guide</h2>
        <p className="mt-3 text-sm leading-relaxed text-dim">Follow this series in its stated order and compare recording choices. Credits and runtimes cover the catalogued entries shown on this page; a catalogue entry does not confirm availability.</p>
      </header>
      <NarratorContinuity entries={guide.entries} />
      {guide.entries.length ? (
        <div className="mt-6 grid items-start gap-6 xl:grid-cols-[minmax(0,1fr)_18rem]">
          <ListeningPlanner plan={plan} />
          <ol className="min-w-0 space-y-6 xl:col-start-1 xl:row-start-1">
            {guide.entries.map((entry) => (
              <li key={`${entry.position}-${entry.work.id}`} className="min-w-0 rounded-2xl border border-edge bg-surface p-4 sm:p-5">
                <header className="flex items-center gap-4">
                  <a className="w-16 shrink-0 rounded-lg" href={href.work(entry.work.id)} tabIndex={-1} aria-hidden="true"><CoverImage src={entry.work.cover_url} alt="" /></a>
                  <div className="min-w-0">
                    <p className="text-xs font-medium text-pink-400">Series position {entry.position}</p>
                    <h3 className="mt-1 text-lg font-medium leading-snug text-hi"><a className="transition-colors hover:text-pink-300" href={href.work(entry.work.id)}>{entry.work.title}</a></h3>
                    <p className="mt-1 text-xs text-dim">{entry.recordings.length} recording choice{entry.recordings.length === 1 ? '' : 's'} shown</p>
                  </div>
                </header>
                {entry.recordings.length ? (
                  <>
                    <ol className="mt-5 grid gap-3 md:grid-cols-2">
                      {entry.recordings.map((recording, index) => <GuideRecording key={recording.id} recording={recording} index={index} workID={entry.work.id} />)}
                    </ol>
                    {entry.recordings_total > entry.recordings.length ? <p className="mt-3 text-xs leading-relaxed text-dim">Showing {entry.recordings.length} of {entry.recordings_total} catalogued recordings. <a className={TEXT_LINK} href={href.work(entry.work.id)}>Compare every recording</a>.</p> : null}
                    <label className="mt-5 block border-t border-edge pt-4 text-sm font-medium text-body">
                      Choose a recording for {entry.work.title}
                      <select className="mt-2 block w-full min-w-0 max-w-full rounded-lg border border-edge bg-deep px-3 py-3 text-sm font-normal text-body focus:border-pink-500" value={choices[entry.work.id] ?? ''} onChange={(event) => setChoices((previous) => ({ ...previous, [entry.work.id]: event.target.value }))}>
                        <option value="">Choose a recording</option>
                        {entry.recordings.map((recording, index) => <option key={recording.id} value={recording.id}>{recordingChoiceLabel(recording, index)}</option>)}
                      </select>
                    </label>
                  </>
                ) : <p className="mt-4 text-sm text-dim">No recording catalogued.</p>}
              </li>
            ))}
          </ol>
        </div>
      ) : <p className="mt-5 text-sm text-dim">No works have been added to this series yet.</p>}
      <CataloguePager offset={guide.offset} limit={guide.limit} total={guide.works_total} pageHref={(page) => `${href.series(guide.id, page)}#listening-guide`} label="Listening guide pages" />
    </section>
  )
}

export default function SeriesListeningGuide({ series }: { series: Series }) {
  const { limit, offset } = series
  const fetchGuide = useCallback((id: string, signal: AbortSignal) => getSeriesListeningGuide(id, { limit, offset }, signal), [limit, offset])
  const state = useEntity(series.id, fetchGuide, series.listening_guide)
  if (state.status === 'loading') return <p className="mt-12 text-sm text-dim" role="status">Loading listening guide...</p>
  if (state.status === 'error') return <p className="mt-12 text-sm text-dim">The listening guide could not be loaded. You can still open each work to compare its recordings.</p>
  return <GuideContent guide={state.data} />
}
