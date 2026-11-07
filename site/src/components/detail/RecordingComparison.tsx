import { formatLanguage, formatRuntime, href, personNames, type Work } from '../../lib/api'
import { abridgementLabel } from '../../lib/catalogue'
import { formatReleaseDate } from '../../lib/dates'
import { TEXT_LINK } from '../ui'

/** A compact choice aid; the linked detailed cards retain identifiers and shops. */
export default function RecordingComparison({ work }: { work: Work }) {
  if (!work.recordings.length) return null
  const cell = 'px-3 py-3 text-left align-top'
  return (
    <div id="recording-comparison" className="mt-5 overflow-x-auto rounded-xl border border-edge">
      <table className="w-full min-w-[42rem] text-sm">
        <caption className="px-3 py-3 text-left font-medium text-hi">Compare recordings</caption>
        <thead className="bg-raised text-xs text-dim">
          <tr>{['Recording / narrators', 'Language', 'Abridgement', 'Runtime', 'Released', 'Publisher', 'Chapters'].map((heading) => <th key={heading} scope="col" className={cell}>{heading}</th>)}</tr>
        </thead>
        <tbody className="divide-y divide-edge text-body">
          {work.recordings.map((recording) => (
            <tr key={recording.id}>
              <th scope="row" className={`${cell} font-normal`}>
                <a href={href.recording(work.id, recording.id)} className={TEXT_LINK}>
                  {personNames(recording.narrators) || 'Narrator unknown'}
                  <span className="mt-1 block text-xs text-dim">{recording.id}</span>
                </a>
              </th>
              <td className={cell}>{formatLanguage(recording.language) ?? 'Unknown'}</td>
              <td className={cell}>{abridgementLabel(recording)}</td>
              <td className={`${cell} whitespace-nowrap`}>{formatRuntime(recording.runtime_min) ?? 'Unknown'}</td>
              <td className={cell}>{formatReleaseDate(recording.release_date) ?? 'Unknown'}</td>
              <td className={cell}>{recording.publisher || 'Unknown'}</td>
              <td className={cell}>{recording.chapter_count === undefined ? 'Unknown' : recording.chapter_count > 0 ? <a className={TEXT_LINK} href={href.chapters(work.id, recording.id)}>{recording.chapter_count}</a> : 'None catalogued'}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
