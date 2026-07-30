import { describe, it, expect } from 'vitest'
import { ADD_QUERY_PARAM, addFromLibexUrl, addWorkFromQueryUrl } from './search-cta'

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

describe('addFromLibexUrl', () => {
  it('carries the trimmed query to the /add lookup assist', () => {
    expect(addFromLibexUrl('  Killing Floor  ')).toBe('/add?q=Killing%20Floor')
  })

  it('encodes characters that would otherwise break the query string', () => {
    const url = addFromLibexUrl("Harry Potter & the Sorcerer's Stone")
    expect(url).toContain('%26')
    const q = new URL(url, 'https://meta.audiosilo.app').searchParams.get('q')
    expect(q).toBe("Harry Potter & the Sorcerer's Stone")
  })

  it('falls back to the bare page for an empty query', () => {
    expect(addFromLibexUrl('   ')).toBe('/add')
  })

  it('carries a pasted ASIN as the query too - /add resolves it directly', () => {
    // There is deliberately no second ?asin= route: the page checks whether the
    // query looks like a product code and resolves it instead of searching.
    expect(addFromLibexUrl(' B015RQON6I ')).toBe('/add?q=B015RQON6I')
  })

  it('writes the parameter the /add island reads', () => {
    // This module WRITES the param and components/add/AddFromLibex.tsx READS it
    // through this same constant. Renaming one side alone would leave the page
    // sitting idle with a query it never sees, and nothing would report it - so
    // the name is pinned here and shared, not spelled out twice.
    expect(ADD_QUERY_PARAM).toBe('q')
    const url = new URL(addFromLibexUrl('Killing Floor'), 'https://meta.audiosilo.app')
    expect(url.searchParams.get(ADD_QUERY_PARAM)).toBe('Killing Floor')
  })
})
