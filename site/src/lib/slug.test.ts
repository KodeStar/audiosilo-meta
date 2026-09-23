import { describe, it, expect } from 'vitest'
import { MAX_SLUG_LEN, isValidSlug } from './slug'

describe('isValidSlug', () => {
  it('accepts lowercase hyphen-joined tokens only', () => {
    expect(isValidSlug('el')).toBe(true)
    expect(isValidSlug('orion-lake')).toBe(true)
    expect(isValidSlug('a1-b2')).toBe(true)
  })
  it('rejects uppercase, spaces, doubled/edge hyphens and empties', () => {
    expect(isValidSlug('El')).toBe(false)
    expect(isValidSlug('orion lake')).toBe(false)
    expect(isValidSlug('-el')).toBe(false)
    expect(isValidSlug('el-')).toBe(false)
    expect(isValidSlug('orion--lake')).toBe(false)
    expect(isValidSlug('')).toBe(false)
  })

  it('enforces the schema length cap', () => {
    expect(isValidSlug('a'.repeat(MAX_SLUG_LEN))).toBe(true)
    expect(isValidSlug('a'.repeat(MAX_SLUG_LEN + 1))).toBe(false)
  })
})
