import { useCallback, useState } from 'react'
import { formatLanguage, formatRuntime, getPersonRecordings, href, personNames, type Person } from '../../lib/api'
import { abridgementLabel, cataloguePage, groupRecordings } from '../../lib/catalogue'
import { TEXT_LINK } from '../ui'
import CataloguePager from './CataloguePager'
import { useEntity } from './detail-common'

/** A recording is the unit of a narration credit. Two versions of one work
    remain two rows, each leading to its own recording card. */
export default function PersonRecordings({ person, fallback }: { person: Person; fallback: React.ReactNode }) {
  const [page] = useState(() => cataloguePage(window.location.search))
  const fetchRecordings = useCallback((id: string, signal: AbortSignal) => getPersonRecordings(id, { limit: 50, offset: (page - 1) * 50 }, signal), [page])
  const state = useEntity(person.id, fetchRecordings, person.recording_credits)
  if (state.status !== 'ready') return <>{fallback}<p className="mt-5 text-sm text-dim" role="status">{state.status === 'loading' ? 'Loading recording credits...' : 'Recording details could not be loaded.'}</p></>
  const { recordings, total, limit, offset } = state.data
  const groups = groupRecordings(recordings)
  return (
    <section id="recording-credits" className="mt-12">
      <h2 className="text-xl font-semibold text-hi">Narrated recordings <span className="ml-2 text-sm font-normal text-dim">{total.toLocaleString()} credit{total === 1 ? '' : 's'}</span></h2>
      {recordings.length ? <p className="mt-3 text-sm text-dim">Showing {offset + 1}-{offset + recordings.length} of {total.toLocaleString()} recording credits, grouped by series and language on this page. Separate versions of a work retain their own credits.</p> : <p className="mt-3 text-sm text-dim">No narrated recordings have been catalogued.</p>}
      <div className="mt-5 space-y-6">
        {groups.map((group) => (
          <section key={group.key} className="rounded-2xl border border-edge bg-surface p-5">
            <h3 className="font-medium text-hi">{group.series ? <a className={TEXT_LINK} href={href.series(group.series.id)}>{group.series.name}</a> : 'Standalone works'} <span className="text-dim">· {formatLanguage(group.language) ?? 'Language unknown'}</span></h3>
            <ul className="mt-3 divide-y divide-edge">
              {group.recordings.map(({ work, recording, series }) => (
                <li key={`${work.id}-${recording.id}`} className="py-3 text-sm">
                  <a className={`${TEXT_LINK} font-medium`} href={href.recording(work.id, recording.id)}>{series[0]?.position ? `#${series[0].position} · ` : ''}{work.title}</a>
                  <p className="mt-1 text-body">{personNames(recording.narrators) || 'Narrator unknown'} · {abridgementLabel(recording)} · {formatRuntime(recording.runtime_min) ?? 'Runtime unknown'}</p>
                  <p className="mt-1 text-xs text-dim">{[recording.publisher, recording.release_date, recording.id].filter(Boolean).join(' · ')}</p>
                  {series.length > 1 ? <p className="mt-1 text-xs text-dim">Also in {series.slice(1).map((membership, index) => <span key={membership.id}>{index ? ', ' : ''}<a className={TEXT_LINK} href={href.series(membership.id)}>{membership.name}{membership.position ? ` #${membership.position}` : ''}</a></span>)}</p> : null}
                </li>
              ))}
            </ul>
          </section>
        ))}
      </div>
      <CataloguePager offset={offset} limit={limit} total={total} pageHref={(page) => href.personRecordings(person.id, page)} label="Recording credit pages" />
    </section>
  )
}
