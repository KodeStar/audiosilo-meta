import { describe, it, expect } from 'vitest'
import { roleLabel, revealLabel, recapLabel, scopeLabel, sortRecaps } from './expressive'
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
