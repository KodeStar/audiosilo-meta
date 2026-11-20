import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { expect, it } from 'vitest'
import { EntryRow as SeriesEntryRow } from '../components/detail/SeriesDetail'
import { EntryRow as WatchingEntryRow } from '../components/watching/WatchingPage'
import { ENTRY_ROW } from '../components/watching/entry-ui'
import type { SeriesEntry } from './api'

// #2932: at 375 px the two watching marks beside a work title squeezed it to
// 5-8 px. Below `sm` every watched row now stacks the marks on a line of their
// own under the title; from `sm` up they sit at the right edge as before. The
// markup is what a unit test can see - the verifier checks the 375 px render.
const entry: SeriesEntry = {
  position: '12',
  work: { id: 'a-long-title', title: 'A Very Long Work Title That Needs Room', authors: [{ id: 'author', name: 'Author' }] },
}
const noop = () => {}

function rowClass(markup: string): string {
  return /^<li class="([^"]*)"/.exec(markup)?.[1] ?? ''
}

it('stacks the series page marks under the title on a phone', () => {
  const markup = renderToStaticMarkup(createElement(SeriesEntryRow, {
    entry, now: '2026-11-20', owned: false, skipped: false, onOwned: noop, onSkipped: noop,
  }))
  expect(rowClass(markup)).toContain(ENTRY_ROW)
  expect(ENTRY_ROW.split(' ')).toEqual(expect.arrayContaining(['flex-col', 'sm:flex-row']))
  // The title wraps to two lines on a phone and keeps to one from `sm` up.
  expect(markup).toMatch(/<h2 class="line-clamp-2 [^"]*sm:line-clamp-1"/)
  // Both marks stay operable and named after the work.
  expect(markup).toContain('aria-label="I have A Very Long Work Title That Needs Room"')
  expect(markup).toContain('aria-label="Not interested in A Very Long Work Title That Needs Room"')
  // The ownership words are visible at every width now that the marks have a line.
  expect(markup).toContain('<span aria-hidden="true">I have this</span>')
})

it('renders no marks on the series page when the series is not watched', () => {
  const markup = renderToStaticMarkup(createElement(SeriesEntryRow, {
    entry, now: '2026-11-20', owned: null, skipped: false, onOwned: noop, onSkipped: noop,
  }))
  expect(markup).not.toContain('I have this')
  expect(markup).not.toContain('Not interested')
})

it('stacks the watching page marks the same way and keeps an owned row free of the skip', () => {
  const unowned = renderToStaticMarkup(createElement(WatchingEntryRow, {
    entry, now: '2026-11-20', owned: false, onOwned: noop, onSkipped: noop, series: { slug: 's', name: 'Series' },
  }))
  expect(rowClass(unowned)).toContain(ENTRY_ROW)
  expect(unowned).toContain('Not interested in A Very Long Work Title That Needs Room')
  const owned = renderToStaticMarkup(createElement(WatchingEntryRow, { entry, now: '2026-11-20', owned: true, onOwned: noop }))
  expect(rowClass(owned)).toContain(ENTRY_ROW)
  expect(owned).toContain('checked=""')
  expect(owned).not.toContain('Not interested')
})
