import { describe, it, expect } from 'vitest'
import { formatReleaseDate, isFutureRelease } from './dates'

describe('formatReleaseDate', () => {
  it('renders each precision at the precision the data states', () => {
    expect(formatReleaseDate('2026-10-20')).toBe('20 Oct 2026')
    expect(formatReleaseDate('2026-10')).toBe('Oct 2026')
    expect(formatReleaseDate('2026')).toBe('2026')
  })
  it('drops the leading zero on a day', () => {
    expect(formatReleaseDate('2026-01-05')).toBe('5 Jan 2026')
  })
  it('renders both ends of the month table', () => {
    expect(formatReleaseDate('2026-01')).toBe('Jan 2026')
    expect(formatReleaseDate('2026-12')).toBe('Dec 2026')
  })
  it('returns nothing for an absent or empty value', () => {
    expect(formatReleaseDate(undefined)).toBeNull()
    expect(formatReleaseDate(null)).toBeNull()
    expect(formatReleaseDate('   ')).toBeNull()
  })
  it('passes an unrecognised value through as stated', () => {
    // The catalogue holds a fact; a renderer that cannot parse it must not
    // hide it.
    expect(formatReleaseDate('circa 1920')).toBe('circa 1920')
    expect(formatReleaseDate('2026-10-20T00:00:00Z')).toBe('2026-10-20T00:00:00Z')
  })
  it('degrades an out-of-range month to the year', () => {
    expect(formatReleaseDate('2026-13')).toBe('2026')
    expect(formatReleaseDate('2026-00-04')).toBe('2026')
  })
})

describe('isFutureRelease', () => {
  const today = '2026-09-21'
  it('compares a full date strictly against today', () => {
    expect(isFutureRelease('2026-09-22', today)).toBe(true)
    expect(isFutureRelease('2026-09-21', today)).toBe(false) // out today: available
    expect(isFutureRelease('2026-09-20', today)).toBe(false)
  })
  it('compares a month against this month, never against a made-up day', () => {
    expect(isFutureRelease('2026-10', today)).toBe(true)
    expect(isFutureRelease('2026-09', today)).toBe(false)
    expect(isFutureRelease('2026-08', today)).toBe(false)
  })
  it('compares a year against this year', () => {
    // The case that decides the rule: widening "2026" to 2026-01-01 would call
    // a book published in March a preorder for the rest of the year.
    expect(isFutureRelease('2026', today)).toBe(false)
    expect(isFutureRelease('2027', today)).toBe(true)
    expect(isFutureRelease('2025', today)).toBe(false)
  })
  it('never calls an absent or unparseable value a preorder', () => {
    expect(isFutureRelease(undefined, today)).toBe(false)
    expect(isFutureRelease(null, today)).toBe(false)
    expect(isFutureRelease('', today)).toBe(false)
    expect(isFutureRelease('next spring', today)).toBe(false)
  })
})
