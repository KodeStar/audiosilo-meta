import { describe, it, expect } from 'vitest'
import { positionStart, seriesNeighbors, hashForTab, tabFromHash, type SeriesWork } from './worknav'

function entry(id: string, position: string): SeriesWork {
  return { position, work: { id, title: id, authors: [] } }
}

describe('positionStart', () => {
  it('parses plain and decimal positions', () => {
    expect(positionStart('1')).toBe(1)
    expect(positionStart('2.5')).toBe(2.5)
  })
  it('takes the start of an omnibus range', () => {
    expect(positionStart('1-3.5')).toBe(1)
  })
  it('sorts an unparseable position last', () => {
    expect(positionStart('')).toBe(Number.POSITIVE_INFINITY)
    expect(positionStart('foo')).toBe(Number.POSITIVE_INFINITY)
  })
})

describe('seriesNeighbors', () => {
  const works = [entry('a', '1'), entry('b', '2'), entry('c', '3')]

  it('returns both neighbours for a middle work', () => {
    const { prev, next } = seriesNeighbors(works, 'b')
    expect(prev?.work.id).toBe('a')
    expect(next?.work.id).toBe('c')
  })
  it('returns only next for the first work', () => {
    const { prev, next } = seriesNeighbors(works, 'a')
    expect(prev).toBeNull()
    expect(next?.work.id).toBe('b')
  })
  it('returns only prev for the last work', () => {
    const { prev, next } = seriesNeighbors(works, 'c')
    expect(prev?.work.id).toBe('b')
    expect(next).toBeNull()
  })
  it('returns nulls when the current work is not in the list', () => {
    expect(seriesNeighbors(works, 'zz')).toEqual({ prev: null, next: null })
  })
  it('returns nulls for a single-member series', () => {
    expect(seriesNeighbors([entry('only', '1')], 'only')).toEqual({ prev: null, next: null })
  })
  it('sorts by position regardless of input order and does not mutate', () => {
    const unordered = [entry('c', '3'), entry('a', '1'), entry('b', '2.5')]
    const { prev, next } = seriesNeighbors(unordered, 'b')
    expect(prev?.work.id).toBe('a')
    expect(next?.work.id).toBe('c')
    expect(unordered.map((e) => e.work.id)).toEqual(['c', 'a', 'b'])
  })
})

describe('hashForTab', () => {
  it('clears the hash for general and names the sidecar tabs', () => {
    expect(hashForTab('general')).toBe('')
    expect(hashForTab('characters')).toBe('#characters')
    expect(hashForTab('recaps')).toBe('#story-so-far')
  })
})

describe('tabFromHash', () => {
  const both = { hasCharacters: true, hasRecaps: true }

  it('maps an empty hash to general', () => {
    expect(tabFromHash('', both)).toBe('general')
  })
  it('maps the known hashes when the tab exists', () => {
    expect(tabFromHash('#characters', both)).toBe('characters')
    expect(tabFromHash('#story-so-far', both)).toBe('recaps')
  })
  it('tolerates a missing leading hash', () => {
    expect(tabFromHash('characters', both)).toBe('characters')
  })
  it('falls back to general when the work lacks that tab', () => {
    expect(tabFromHash('#story-so-far', { hasCharacters: true, hasRecaps: false })).toBe('general')
    expect(tabFromHash('#characters', { hasCharacters: false, hasRecaps: true })).toBe('general')
  })
  it('falls back to general for an unknown hash', () => {
    expect(tabFromHash('#nope', both)).toBe('general')
  })
})
