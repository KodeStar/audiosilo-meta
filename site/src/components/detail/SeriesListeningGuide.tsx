import { useCallback, useState } from 'react'
import {
  formatLanguage,
  formatRuntime,
  getSeriesListeningGuide,
  href,
  personNames,
  type RecordingSummary,
  type Series,
  type SeriesListeningGuide as Guide,
} from '../../lib/api'
import { abridgementLabel, listeningPlan, narratorContinuity, recordingChoiceLabel } from '../../lib/catalogue'
import PersonLinks from '../cards/PersonLinks'
import { TEXT_LINK } from '../ui'
import CataloguePager from './CataloguePager'
import { useEntity, useInitialFragmentScroll } from './detail-common'

function recordingLabel(recording: RecordingSummary): string {
  return [personNames(recording.narrators) || 'Narrator unknown',
    formatLanguage(recording.language) || 'Language unknown',
    abridgementLabel(recording), formatRuntime(recording.runtime_min) || 'Runtime unknown',
    recording.publisher, recording.release_date, recording.id].filter(Boolean).join(' · ')
}

function GuideContent({ guide }: { guide: Guide }) {
  const section = useInitialFragmentScroll('listening-guide')
  const [choices, setChoices] = useState<Record<string, string>>({})
  const plan = listeningPlan(guide, choices)
  const continuity = narratorContinuity(guide.entries)
  return (
    <section ref={section} id="listening-guide" className="mt-12 rounded-2xl border border-edge bg-surface p-5 sm:p-6">
      <h2 className="text-xl font-semibold text-hi">Listening guide</h2>
      <p className="mt-3 text-sm leading-relaxed text-dim">
        Follow this series in its stated order and compare narrators before choosing a recording.
        Choices and runtimes cover the catalogued entries shown here; a catalogue entry does not confirm availability.
      </p>
      <div className="mt-5 rounded-xl border border-edge bg-raised/40 p-4">
        <h3 className="font-medium text-hi">Narrator continuity</h3>
        {continuity.length ? (
          <ul className="mt-2 space-y-1 text-sm text-body">
            {continuity.map(({ person, works }) => <li key={person.id}><PersonLinks people={[person]} /> is credited on {works} works among the choices on this page.</li>)}
          </ul>
        ) : <p className="mt-2 text-sm text-dim">No recurring narrator credit among the choices on this page.</p>}
      </div>
      {guide.entries.length ? (
        <ol className="mt-5 space-y-5">
          {guide.entries.map((entry) => (
            <li key={`${entry.position}-${entry.work.id}`} className="border-t border-edge pt-5">
              <h3 className="font-medium text-hi"><span className="mr-2 text-pink-400">#{entry.position}</span><a className={TEXT_LINK} href={href.work(entry.work.id)}>{entry.work.title}</a></h3>
              {entry.recordings.length ? (
                <>
                  <ol className="mt-3 list-inside list-decimal space-y-2 text-sm text-body">
                    {entry.recordings.map((recording) => <li key={recording.id}><a className={TEXT_LINK} href={href.recording(entry.work.id, recording.id)}>{recordingLabel(recording)}</a></li>)}
                  </ol>
                  {entry.recordings_total > entry.recordings.length ? <p className="mt-2 text-xs text-dim">Showing {entry.recordings.length} of {entry.recordings_total} catalogued recordings. <a className={TEXT_LINK} href={href.work(entry.work.id)}>Compare every recording</a>.</p> : null}
                  <label className="mt-3 block text-sm text-body">
                    Choose a recording for {entry.work.title}
                    <select className="mt-2 block w-full rounded-lg border border-edge bg-base px-3 py-2 text-body" value={choices[entry.work.id] ?? ''} onChange={(event) => setChoices((previous) => ({ ...previous, [entry.work.id]: event.target.value }))}>
                      <option value="">Choose a recording</option>
                      {entry.recordings.map((recording, index) => <option key={recording.id} value={recording.id}>{recordingChoiceLabel(recording, index)}</option>)}
                    </select>
                  </label>
                </>
              ) : <p className="mt-2 text-sm text-dim">No recording catalogued.</p>}
            </li>
          ))}
        </ol>
      ) : <p className="mt-5 text-sm text-dim">No works have been added to this series yet.</p>}
      {guide.entries.length ? (
        <div className="mt-6 rounded-xl border border-edge bg-raised/40 p-4" aria-live="polite">
          <h3 className="font-medium text-hi">Listening time planner</h3>
          {plan.selected ? (
            <>
              <p className="mt-2 text-lg text-hi">Chosen-list subtotal: {plan.minutes > 0 ? formatRuntime(plan.minutes) : 'no known runtimes selected'}</p>
              <p className="mt-1 text-sm text-dim">{plan.selected} recording{plan.selected === 1 ? '' : 's'} chosen on this page. {plan.complete ? 'One recording chosen for every catalogued entry, with known runtimes and no overlapping positions.' : 'Partial plan.'}</p>
              {plan.unknownRuntime ? <p className="mt-1 text-sm text-dim">{plan.unknownRuntime} chosen recording{plan.unknownRuntime === 1 ? ' has' : 's have'} unknown runtime and adds no time to this subtotal.</p> : null}
              {plan.overlap ? <p className="mt-1 text-sm text-dim">Selected positions overlap, so this subtotal may include repeated coverage. It is not a total for the series.</p> : null}
              {plan.uncertainOrder ? <p className="mt-1 text-sm text-dim">Some positions cannot be checked for overlap.</p> : null}
            </>
          ) : <p className="mt-2 text-sm text-dim">Choose a recording for each work to plan your listening time. Alternatives are never added together automatically.</p>}
          {plan.unchosen ? <p className="mt-1 text-sm text-dim">{plan.unchosen} {plan.unchosen === 1 ? 'entry' : 'entries'} on this page still need{plan.unchosen === 1 ? 's' : ''} a recording choice.</p> : null}
          {plan.otherPages ? <p className="mt-1 text-sm text-dim">More catalogued entries are on other pages. Choices apply to this page only.</p> : null}
        </div>
      ) : null}
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
