import { describe, it, expect, afterEach, vi } from 'vitest'
import { readFileSync } from 'node:fs'
import {
  SERIES_ORDERINGS,
  formatLanguage,
  getCoverageWorks,
  getLatestWorks,
  getStatsShared,
  search,
} from './api'
import { chosenPrefs } from './languages'
import { stubStorage } from './test-support'

// The series ordering vocabulary is restated here from the schema, which is its
// one definition: a value the schema gains or loses would otherwise let the site's
// types offer an ordering the data cannot hold, or reject one it does.
describe('SERIES_ORDERINGS', () => {
  it('matches the schema series_ordering enum exactly, in order', () => {
    const schema = JSON.parse(
      readFileSync(new URL('../../../schema/common.schema.json', import.meta.url), 'utf8')
    )
    expect([...SERIES_ORDERINGS]).toEqual(schema.$defs.series_ordering.enum)
  })
})

// The language filter is an EXPLICIT argument of the three fetchers that take
// one, and an unfiltered call sends no `lang` at all - so the request every
// existing caller makes is unchanged, and no fetcher can pick a filter up from
// storage behind its caller's back.
describe('the lang argument', () => {
  afterEach(() => vi.unstubAllGlobals())

  function capture(): string[] {
    const urls: string[] = []
    vi.stubGlobal('fetch', async (url: string) => {
      urls.push(String(url))
      return { ok: true, status: 200, json: async () => ({ results: [], works: [] }) }
    })
    return urls
  }

  it('is sent canonical when given', async () => {
    const urls = capture()
    await search('harry', 20, undefined, ['fr', 'de-AT'])
    await getLatestWorks(12, undefined, ['de'])
    await getCoverageWorks({ filter: 'missing', lang: ['de', 'en'] })
    expect(urls).toEqual([
      '/api/v1/search?q=harry&limit=20&lang=de%2Cfr',
      '/api/v1/works/latest?limit=12&lang=de',
      '/api/v1/coverage/works?filter=missing&lang=de%2Cen',
    ])
  })

  it('is absent when not given or empty, even with a preference stored', async () => {
    const map = stubStorage()
    map.set('audiosilo-meta:languages', JSON.stringify(chosenPrefs(['de'])))
    const urls = capture()
    await search('harry')
    await search('harry', 20, undefined, [])
    await getLatestWorks()
    await getCoverageWorks({ filter: 'missing' })
    expect(urls).toEqual([
      '/api/v1/search?q=harry&limit=20',
      '/api/v1/search?q=harry&limit=20',
      '/api/v1/works/latest?limit=12',
      '/api/v1/coverage/works?filter=missing',
    ])
  })
})

describe('formatLanguage', () => {
  it('names a code in English and passes a non-code through', () => {
    expect(formatLanguage('de')).toBe('German')
    expect(formatLanguage('not a code')).toBe('not a code')
    expect(formatLanguage(null)).toBeNull()
    expect(formatLanguage('')).toBeNull()
  })
})

// One /stats request per page, whichever island asks first (the stats band and
// the language census) - and a failure is retried by the next caller rather
// than remembered.
describe('getStatsShared', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('fetches once for every caller, and again after a failure', async () => {
    let calls = 0
    let fail = true
    vi.stubGlobal('fetch', async () => {
      calls++
      if (fail) return { ok: false, status: 503, json: async () => ({}) }
      return { ok: true, status: 200, json: async () => ({ works: 1 }) }
    })
    await expect(getStatsShared()).rejects.toThrow()
    fail = false
    const [a, b] = await Promise.all([getStatsShared(), getStatsShared()])
    expect(a).toBe(b)
    await getStatsShared()
    expect(calls).toBe(2)
  })
})
