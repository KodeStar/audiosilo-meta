import { describe, it, expect, afterEach, vi } from 'vitest'
import { readFileSync } from 'node:fs'
import { SERIES_ORDERINGS, getCoverageWorks, getLatestWorks, search } from './api'
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
