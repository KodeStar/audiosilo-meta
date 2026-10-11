import { describe, it, expect } from 'vitest'
import { SERIES_ORDERINGS } from './api'
import { ORDERING_LABELS, seriesRelation, seriesSubline } from './series-relation'

const original = { id: 'harry-potter', name: 'Harry Potter', language: 'en' }

describe('seriesRelation', () => {
  it('says nothing about the original series', () => {
    expect(seriesRelation({})).toBeNull()
    expect(seriesRelation({ works: 7, ordering: 'publication' })).toBeNull()
  })

  it('names the series a translation translates', () => {
    expect(seriesRelation({ translation_of: [original] })).toBe('Translation of Harry Potter')
    expect(
      seriesRelation({
        translation_of: [original, { id: 'fantastic-beasts', name: 'Fantastic Beasts' }],
      })
    ).toBe('Translation of Harry Potter and Fantastic Beasts')
  })

  it('falls back to the slug when an original has no name', () => {
    expect(seriesRelation({ translation_of: [{ id: 'harry-potter', name: '' }] })).toBe(
      'Translation of harry-potter'
    )
  })

  it('names a variant by its ordering and its primary', () => {
    expect(
      seriesRelation({
        ordering: 'chronological',
        ordering_of: 'harry-potter',
        ordering_of_name: 'Harry Potter',
      })
    ).toBe('Chronological order of Harry Potter')
    expect(seriesRelation({ ordering: 'recommended', ordering_of: 'harry-potter' })).toBe(
      'Recommended order of harry-potter'
    )
    expect(seriesRelation({ ordering_of: 'harry-potter', ordering_of_name: 'Harry Potter' })).toBe(
      'Reading order of Harry Potter'
    )
    expect(
      seriesRelation({ ordering: 'toString', ordering_of: 'x', ordering_of_name: 'X' })
    ).toBe('Reading order of X')
  })

  it('describes a translated variant as a variant', () => {
    expect(
      seriesRelation({
        ordering: 'chronological',
        ordering_of: 'harry-potter',
        ordering_of_name: 'Harry Potter',
        translation_of: [original],
      })
    ).toBe('Chronological order of Harry Potter')
  })

  it('words every ordering the schema has', () => {
    expect(Object.keys(ORDERING_LABELS).sort()).toEqual([...SERIES_ORDERINGS].sort())
  })
})

describe('seriesSubline', () => {
  it('joins the relation and the member count', () => {
    expect(seriesSubline({ works: 7, translation_of: [original] })).toBe(
      'Translation of Harry Potter · 7 works'
    )
    expect(seriesSubline({ works: 1 })).toBe('1 work')
    expect(seriesSubline({ works: 0 })).toBe('0 works')
    expect(seriesSubline({ works: 1234 })).toBe('1,234 works')
    expect(seriesSubline({ translation_of: [original] })).toBe('Translation of Harry Potter')
    expect(seriesSubline({})).toBeNull()
  })
})
