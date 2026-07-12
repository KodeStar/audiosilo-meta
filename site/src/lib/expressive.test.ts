import { describe, it, expect } from 'vitest'
import {
  roleLabel,
  revealLabel,
  recapLabel,
  scopeLabel,
  sortRecaps,
  storyRows,
} from './expressive'
import type { Recap } from './api'

describe('roleLabel', () => {
  it('maps known roles', () => {
    expect(roleLabel('protagonist')).toBe('Protagonist')
    expect(roleLabel('antagonist')).toBe('Antagonist')
    expect(roleLabel('supporting')).toBe('Supporting')
    expect(roleLabel('minor')).toBe('Minor')
  })
  it('returns null for absent role', () => {
    expect(roleLabel(undefined)).toBeNull()
  })
})

describe('revealLabel', () => {
  it('treats chapter 0 and 1 as from the start', () => {
    expect(revealLabel({ chapter: 0 })).toBe('From the start')
    expect(revealLabel({ chapter: 1 })).toBe('From the start')
  })
  it('names a later chapter', () => {
    expect(revealLabel({ chapter: 12 })).toBe('From chapter 12')
  })
})

describe('recapLabel', () => {
  it('labels a chapter-0 series recap as the prior-books catch-up', () => {
    expect(recapLabel({ through: { chapter: 0 }, scope: 'series', text: 'x' })).toBe(
      'Previously, in earlier books'
    )
  })
  it('labels a chapter-0 book recap as before this book', () => {
    expect(recapLabel({ through: { chapter: 0 }, scope: 'book', text: 'x' })).toBe(
      'Before this book'
    )
  })
  it('labels a within-book recap by chapter', () => {
    expect(recapLabel({ through: { chapter: 7 }, scope: 'book', text: 'x' })).toBe(
      'Up to chapter 7'
    )
  })
})

describe('scopeLabel', () => {
  it('maps known scopes and null otherwise', () => {
    expect(scopeLabel('series')).toBe('earlier books')
    expect(scopeLabel('book')).toBe('this book')
    expect(scopeLabel(undefined)).toBeNull()
  })
})

describe('sortRecaps', () => {
  it('orders by position ascending without mutating the input', () => {
    const input: Recap[] = [
      { through: { chapter: 9 }, scope: 'book', text: 'c' },
      { through: { chapter: 0 }, scope: 'series', text: 'a' },
      { through: { chapter: 4 }, scope: 'book', text: 'b' },
    ]
    const out = sortRecaps(input)
    expect(out.map((r) => r.through.chapter)).toEqual([0, 4, 9])
    // input untouched
    expect(input.map((r) => r.through.chapter)).toEqual([9, 0, 4])
  })
})

describe('storyRows', () => {
  const recaps: Recap[] = [
    { through: { chapter: 5 }, scope: 'book', text: 'b' },
    { through: { chapter: 0 }, scope: 'series', text: 'a' },
  ]

  it('is empty with no recaps and no summary', () => {
    expect(storyRows([], undefined)).toEqual([])
  })
  it('builds the chaptered rows alone, position-ordered, via the lib labels', () => {
    expect(storyRows(recaps, undefined)).toEqual([
      { title: 'Previously, in earlier books', badge: 'earlier books', text: 'a' },
      { title: 'Up to chapter 5', badge: 'this book', text: 'b' },
    ])
  })
  it('omits the badge for a scopeless chaptered recap', () => {
    expect(storyRows([{ through: { chapter: 3 }, text: 'x' }], undefined)).toEqual([
      { title: 'Up to chapter 3', badge: undefined, text: 'x' },
    ])
  })
  it('builds a lone in_short row', () => {
    expect(storyRows([], { in_short: 'the whole book' })).toEqual([
      { title: 'In short', badge: 'whole book', text: 'the whole book' },
    ])
  })
  it('builds a lone ending row', () => {
    expect(storyRows([], { ending: 'how it ends' })).toEqual([
      { title: 'How did it end?', badge: 'ending', text: 'how it ends' },
    ])
  })
  it('orders in_short first, chaptered rows in the middle, ending last', () => {
    const titles = storyRows(recaps, { in_short: 'x', ending: 'y' }).map((r) => r.title)
    expect(titles).toEqual([
      'In short',
      'Previously, in earlier books',
      'Up to chapter 5',
      'How did it end?',
    ])
  })
  it('treats empty-string summary fields as absent', () => {
    expect(storyRows([], { in_short: '', ending: '' })).toEqual([])
    expect(storyRows(recaps, { in_short: '' })).toHaveLength(2)
  })
})
