import { describe, expect, it } from 'vitest'
import { href, type ListeningGuideEntry, type PersonRecording, type RecordingSummary, type SeriesListeningGuide } from './api'
import { abridgementLabel, cataloguePage, groupRecordings, isRecordingHash, listeningPlan, narratorContinuity, pageWindow, recordingChoiceLabel } from './catalogue'

const voice = { id: 'voice', name: 'Reader One' }
const otherVoice = { id: 'other', name: 'Reader Two' }
function recording(id: string, runtime_min?: number): RecordingSummary {
  return { id, narrators: [voice], ...(runtime_min === undefined ? {} : { runtime_min }) }
}
function entry(id: string, position: string, recordings = [recording(`${id}-a`, 60)]): ListeningGuideEntry {
  return { position, work: { id, title: id, authors: [] }, recordings, recordings_total: recordings.length, recordings_limit: 5 }
}
function guide(entries: ListeningGuideEntry[], total = entries.length, offset = 0): SeriesListeningGuide {
  return { id: 'series', name: 'Series', authors: [], entries, works_total: total, limit: 50, offset }
}

describe('recording comparison facts', () => {
  it('keeps absent and legacy false unknown, while preserving explicit unabridged', () => {
    expect(abridgementLabel({ ...recording('old'), abridged: false })).toBe('Unknown')
    expect(abridgementLabel(recording('unknown'))).toBe('Unknown')
    expect(abridgementLabel({ ...recording('old'), abridged: true })).toBe('Abridged')
    expect(abridgementLabel({ ...recording('known'), abridgement: 'unabridged', abridged: false })).toBe('Unabridged')
    expect(abridgementLabel({ ...recording('known'), abridgement: 'abridged' })).toBe('Abridged')
  })

  it('keeps cast choices readable while numbering otherwise identical productions', () => {
    const fullCast = { ...recording('cast'), narrators: Array.from({ length: 100 }, (_, index) => ({ id: String(index), name: `Reader ${index}` })) }
    expect(recordingChoiceLabel(fullCast, 0)).toContain('Reader 0, Reader 1 + 98 others')
    expect(recordingChoiceLabel(fullCast, 0).length).toBeLessThan(150)
    expect(recordingChoiceLabel(fullCast, 1)).toMatch(/^Recording 2/)
  })
})

describe('listening runtime choices', () => {
  it('starts empty and counts only an explicit, valid alternative per work', () => {
    const data = guide([entry('one', '1', [recording('short', 60), recording('long', 90)])])
    expect(listeningPlan(data, {})).toMatchObject({ minutes: 0, selected: 0, unchosen: 1, complete: false })
    expect(listeningPlan(data, { one: 'long', gone: 'short' })).toMatchObject({ minutes: 90, selected: 1, complete: true })
    expect(listeningPlan(data, { one: 'missing' })).toMatchObject({ minutes: 0, selected: 0, complete: false })
  })

  it('reports a partial known subtotal for a chosen recording with no usable runtime', () => {
    const data = guide([entry('one', '1'), entry('two', '2', [recording('two-a')])])
    expect(listeningPlan(data, { one: 'one-a', two: 'two-a' })).toMatchObject({ minutes: 60, selected: 2, unknownRuntime: 1, complete: false })
    expect(listeningPlan(guide([entry('zero', '1', [recording('zero-a', 0)])]), { zero: 'zero-a' })).toMatchObject({ minutes: 0, unknownRuntime: 1, complete: false })
  })

  it('never labels a page subtotal as complete when other pages exist', () => {
    expect(listeningPlan(guide([entry('one', '1')], 101), { one: 'one-a' })).toMatchObject({ otherPages: true, complete: false })
    expect(listeningPlan(guide([entry('last', '101')], 101, 100), { last: 'last-a' })).toMatchObject({ otherPages: true, complete: false })
  })

  it('detects overlapping omnibus positions, decimals and duplicate slots', () => {
    for (const [first, second] of [['1-3', '2.5'], ['1 – 3.5', '3.5'], ['2', '2']]) {
      const data = guide([entry('one', first), entry('two', second)])
      expect(listeningPlan(data, { one: 'one-a', two: 'two-a' })).toMatchObject({ minutes: 120, overlap: true, complete: false })
    }
    const data = guide([entry('one', '1'), entry('two', '1.5'), entry('three', '2-3')])
    expect(listeningPlan(data, { one: 'one-a', two: 'two-a', three: 'three-a' })).toMatchObject({ minutes: 180, overlap: false, complete: true })
  })

  it('withholds completeness for non-numeric positions and empty series', () => {
    expect(listeningPlan(guide([entry('one', 'Prequel')]), { one: 'one-a' })).toMatchObject({ uncertainOrder: true, complete: false })
    expect(listeningPlan(guide([]), {})).toMatchObject({ complete: false, selected: 0 })
  })
})

describe('visible-page narration credits', () => {
  it('counts continuity across distinct works rather than alternatives or duplicate credits', () => {
    const data = [entry('one', '1', [recording('a'), { ...recording('b'), narrators: [voice, voice, otherVoice] }]), entry('two', '2')]
    expect(narratorContinuity(data)).toEqual([{ person: voice, works: 2 }])
  })

  it('preserves recording versions and page order, grouped by first membership and stated recording language', () => {
    const work = { id: 'one', title: 'One', authors: [], language: 'en' }
    const series = [{ id: 's', name: 'Series', position: '1' }, { id: 'secondary', name: 'Other series', position: '2' }]
    const rows: PersonRecording[] = [
      { work, recording: { ...recording('a'), language: 'en' }, series },
      { work, recording: recording('unknown'), series },
      { work, recording: { ...recording('b'), language: 'en' }, series },
      { work, recording: { ...recording('standalone'), language: 'en' }, series: [] },
    ]
    const groups = groupRecordings(rows)
    expect(groups.map((group) => group.recordings.map((row) => row.recording.id))).toEqual([['a', 'b'], ['unknown'], ['standalone']])
    expect(groups[0].series?.id).toBe('s')
    expect(groups[1].language).toBeUndefined()
    expect(groups[2].series).toBeUndefined()
    expect(groups[0].recordings[1].series).toHaveLength(2)
  })
})

describe('catalogue page links', () => {
  it('uses bounded page windows at the start, middle and end', () => {
    expect(pageWindow(0, 50, 101)).toEqual({ page: 1, previous: null, next: 2 })
    expect(pageWindow(50, 50, 101)).toEqual({ page: 2, previous: 1, next: 3 })
    expect(pageWindow(100, 50, 101)).toEqual({ page: 3, previous: 2, next: null })
    expect(pageWindow(0, 50, 0)).toEqual({ page: 1, previous: null, next: null })
    expect(cataloguePage('?page=2')).toBe(2)
    for (const query of ['', '?page=0', '?page=-1', '?page=1.5', '?page=Infinity', '?page=99999999999999999']) expect(cataloguePage(query)).toBe(1)
  })

  it('encodes identities and retains actual pagination and recording fragments', () => {
    expect(href.recording('work /?', 'recording /?')).toBe('/works/work%20%2F%3F#recording-recording%20%2F%3F')
    expect(href.chapters('one', 'a', 2)).toBe('/works/one/recordings/a/chapters?page=2')
    expect(href.chapters('one', 'a')).toBe('/works/one/recordings/a/chapters')
    expect(href.series('s', 2)).toBe('/series/s?page=2')
    expect(href.personRecordings('p', 2)).toBe('/people/p?page=2#recording-credits')
    expect(isRecordingHash('#recording-a%20b', [{ id: 'a b' }])).toBe(true)
    expect(isRecordingHash('#recording-%invalid', [{ id: 'a' }])).toBe(false)
    expect(isRecordingHash('#recording-missing', [{ id: 'a' }])).toBe(false)
    expect(isRecordingHash('#characters', [{ id: 'characters' }])).toBe(false)
  })
})
