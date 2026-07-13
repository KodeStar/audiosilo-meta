import { describe, it, expect } from 'vitest'
import { addWorkFromQueryUrl } from './search-cta'

function params(url: string): URLSearchParams {
  return new URL(url).searchParams
}

describe('addWorkFromQueryUrl', () => {
  it('targets the add-work issue form', () => {
    const url = addWorkFromQueryUrl('The Winds of Araxos')
    expect(url.startsWith('https://github.com/kodestar/audiosilo-meta/issues/new?')).toBe(true)
    expect(params(url).get('template')).toBe('add-work.yml')
  })

  it('prefills the work title field and the issue title from the query', () => {
    const p = params(addWorkFromQueryUrl('The Winds of Araxos'))
    expect(p.get('work_title')).toBe('The Winds of Araxos')
    expect(p.get('title')).toBe('[work] The Winds of Araxos')
  })

  it('trims surrounding whitespace', () => {
    const p = params(addWorkFromQueryUrl('  Dune  '))
    expect(p.get('work_title')).toBe('Dune')
    expect(p.get('title')).toBe('[work] Dune')
  })

  it('URL-encodes special characters', () => {
    const url = addWorkFromQueryUrl("Harry Potter & the Sorcerer's Stone")
    // The query's own ampersand must be escaped (%26) so it is not read as a
    // param separator; parsing the URL back must recover the exact query.
    expect(url).toContain('%26')
    expect(params(url).get('work_title')).toBe("Harry Potter & the Sorcerer's Stone")
  })

  it('falls back to the unprefilled form for an empty/whitespace query', () => {
    const p = params(addWorkFromQueryUrl('   '))
    expect(p.get('template')).toBe('add-work.yml')
    expect(p.get('work_title')).toBeNull()
    expect(p.get('title')).toBeNull()
  })
})
