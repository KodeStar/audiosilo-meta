import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { expect, it } from 'vitest'
import { GuideRecording, NarratorContinuity } from '../components/detail/SeriesListeningGuide'
import type { ListeningGuideEntry, RecordingSummary } from './api'

const narrators = Array.from({ length: 20 }, (_, index) => ({ id: `reader-${index}`, name: `Reader ${index}` }))
const recording: RecordingSummary = { id: 'exact-version', narrators, runtime_min: 60 }

it('bounds initial continuity while keeping every credit and count in native disclosures', () => {
  const entries: ListeningGuideEntry[] = ['one', 'two'].map((id) => ({
    position: id, work: { id, title: id, authors: [] }, recordings: [recording], recordings_total: 1, recordings_limit: 5,
  }))
  const markup = renderToStaticMarkup(createElement(NarratorContinuity, { entries }))
  const [initial, disclosure] = markup.split('<details')
  expect((initial.match(/href="\/people\//g) ?? [])).toHaveLength(6)
  expect(disclosure).toContain('Show 14 more recurring narrators')
  expect((markup.match(/2 works/g) ?? [])).toHaveLength(20)
  for (const narrator of narrators) expect(markup).toContain(`href="/people/${narrator.id}"`)
  expect(markup).not.toContain('<details open')
})

it('discloses long credits and preserves unknown facts and exact recording links', () => {
  const markup = renderToStaticMarkup(createElement(GuideRecording, { recording, index: 1, workID: 'work' }))
  expect(markup).toContain('Recording 2')
  expect(markup).toContain('href="/works/work#recording-exact-version"')
  expect(markup).toContain('All 20 narrator credits')
  expect(markup.split('<details')[0]).not.toContain('Reader 2')
  for (const narrator of narrators) expect(markup).toContain(`href="/people/${narrator.id}"`)
  expect(markup).toContain('>Abridgement</dt><dd class="mt-0.5">Unknown</dd>')
  expect(markup).not.toContain('<details open')
  expect(markup).not.toContain('Full cast')
})
