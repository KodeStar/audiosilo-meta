import type {
  ListeningGuideEntry,
  PersonRecording,
  PersonRef,
  RecordingSummary,
  SeriesListeningGuide,
} from './api'
import { formatLanguage, formatRuntime } from './api'

/** A legacy false means unknown: only the additive presence field can prove
    that an older recording is unabridged. */
export function abridgementLabel(recording: RecordingSummary & { abridged?: boolean }): string {
  if (recording.abridgement === 'unabridged') return 'Unabridged'
  if (recording.abridgement === 'abridged' || recording.abridged === true) return 'Abridged'
  return 'Unknown'
}

/** A short native-select label. The numbered list beside it carries full facts,
    and the number disambiguates productions with otherwise identical credits. */
export function recordingChoiceLabel(recording: RecordingSummary, index: number): string {
  const names = recording.narrators.slice(0, 2).map(({ name }) => name.length > 32 ? `${name.slice(0, 31)}…` : name).join(', ')
  const others = recording.narrators.length > 2 ? ` + ${recording.narrators.length - 2} others` : ''
  return [`Recording ${index + 1}`, names ? `${names}${others}` : 'Narrator unknown',
    formatLanguage(recording.language) || 'Language unknown', abridgementLabel(recording),
    formatRuntime(recording.runtime_min) || 'Runtime unknown'].join(' · ')
}

export function cataloguePage(search: string): number {
  const raw = new URLSearchParams(search).get('page')
  if (!raw || !/^[1-9]\d*$/.test(raw)) return 1
  const page = Number(raw)
  return Number.isSafeInteger(page) ? page : 1
}

/** Only keep a recording fragment that names a card this work can display. */
export function isRecordingHash(hash: string, recordings: readonly { id: string }[]): boolean {
  try {
    const id = decodeURIComponent(hash.replace(/^#recording-/, ''))
    return hash.startsWith('#recording-') && recordings.some((recording) => recording.id === id)
  } catch {
    return false
  }
}

export function pageWindow(offset: number, limit: number, total: number) {
  const page = Math.floor(offset / limit) + 1
  return { page, previous: page > 1 ? page - 1 : null, next: offset + limit < total ? page + 1 : null }
}

function positionRange(position: string): [number, number] | null {
  const match = /^(\d+(?:\.\d+)?)(?:\s*[-–]\s*(\d+(?:\.\d+)?))?$/.exec(position.trim())
  if (!match) return null
  const first = Number(match[1])
  const last = match[2] === undefined ? first : Number(match[2])
  return last >= first ? [first, last] : null
}

/** Count only a chosen recording in the current page, never all alternatives.
    Completeness means all catalogued entries, not every book ever published. */
export function listeningPlan(guide: SeriesListeningGuide, choices: Readonly<Record<string, string>>) {
  const selected = guide.entries.flatMap((entry) => {
    const recording = entry.recordings.find((r) => r.id === choices[entry.work.id])
    return recording ? [{ entry, recording }] : []
  })
  const known = selected.filter(({ recording }) =>
    typeof recording.runtime_min === 'number' && Number.isFinite(recording.runtime_min) && recording.runtime_min > 0)
  const ranges = selected.map(({ entry }) => positionRange(entry.position))
  const overlap = ranges.some((range, i) => range && ranges.some((other, j) =>
    j < i && other && range[0] <= other[1] && other[0] <= range[1]))
  const allEntriesHere = guide.offset === 0 && guide.entries.length === guide.works_total
  const complete = guide.works_total > 0 && allEntriesHere && selected.length === guide.entries.length &&
    known.length === selected.length && ranges.every(Boolean) && !overlap
  return {
    minutes: known.reduce((sum, { recording }) => sum + recording.runtime_min!, 0),
    selected: selected.length,
    unknownRuntime: selected.length - known.length,
    unchosen: guide.entries.length - selected.length,
    otherPages: !allEntriesHere,
    overlap,
    uncertainOrder: ranges.some((range) => !range),
    complete,
  }
}

/** A recurring credit is evidence of continuity only on the visible page. Count
    distinct works, so five alternatives for one book do not suggest a run. */
export function narratorContinuity(entries: ListeningGuideEntry[]): { person: PersonRef; works: number }[] {
  const people = new Map<string, { person: PersonRef; works: Set<string> }>()
  for (const entry of entries) {
    for (const recording of entry.recordings) {
      for (const person of recording.narrators) {
        if (!people.has(person.id)) people.set(person.id, { person, works: new Set() })
        people.get(person.id)!.works.add(entry.work.id)
      }
    }
  }
  return [...people.values()]
    .filter(({ works }) => works.size > 1)
    .map(({ person, works }) => ({ person, works: works.size }))
    .sort((a, b) => b.works - a.works || a.person.name.localeCompare(b.person.name) || a.person.id.localeCompare(b.person.id))
}

export interface RecordingGroup {
  key: string
  series?: PersonRecording['series'][number]
  language?: string
  recordings: PersonRecording[]
}

/** Each credit appears once. The first stated series supplies its group;
    additional memberships stay on the row. Do not deduplicate by work id. */
export function groupRecordings(recordings: PersonRecording[]): RecordingGroup[] {
  const groups = new Map<string, RecordingGroup>()
  for (const row of recordings) {
    const series = row.series[0]
    const language = row.recording.language
    const key = JSON.stringify([series?.id ?? null, language ?? null])
    if (!groups.has(key)) groups.set(key, { key, series, language, recordings: [] })
    groups.get(key)!.recordings.push(row)
  }
  return [...groups.values()]
}
