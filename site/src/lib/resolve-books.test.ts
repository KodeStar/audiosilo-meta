import { describe, it, expect, afterEach, vi } from 'vitest'
import type { ParsedBook } from './import-parse'
import { groupBySeries, ownedWorks, resolveLibrary, type ResolvedLibrary } from './resolve-books'

// A book as the parsers produce one. Only the fields the sweep reads matter.
function book(p: Partial<ParsedBook> & { title: string }): ParsedBook {
  return {
    authors: [],
    narrators: [],
    format: 'openaudible',
    raw: {},
    ...p,
  }
}

function workCard(id: string, title: string, series?: { id: string; name: string }) {
  return { id, title, authors: [{ id: 'a', name: 'An Author' }], series: series ?? null }
}

/** Stub the global fetch with a router over the API paths the sweep calls.
    Nothing here goes near a network; the point is the choreography. */
function stubApi(routes: Record<string, unknown>) {
  vi.stubGlobal('fetch', async (url: string) => {
    const path = String(url)
    const key = Object.keys(routes).find((k) => path.startsWith(k))
    if (key === undefined) return { ok: false, status: 404, json: async () => ({}) }
    return { ok: true, status: 200, json: async () => routes[key] }
  })
}

async function resolve(books: ParsedBook[]): Promise<ResolvedLibrary> {
  const got = await resolveLibrary(books, new AbortController().signal, () => {})
  if (!got) throw new Error('the sweep reported an abort it was not given')
  return got
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('resolveLibrary', () => {
  it('reports a matched identifier with the work card the lookup returned', async () => {
    stubApi({
      '/api/v1/lookup': {
        work: workCard('project-hail-mary', 'Project Hail Mary'),
        recording_id: 'ray-porter-2021',
      },
    })
    const got = await resolve([book({ title: 'Project Hail Mary', asin: 'B08G9PRS1K' })])
    expect(got.inDatabase.map((m) => m.work.id)).toEqual(['project-hail-mary'])
    expect(got.newBooks).toEqual([])
    expect(got.total).toBe(1)
  })

  it('routes a missed identifier to an existing work, carrying that work’s series', async () => {
    // The lookup 404s, so the author search decides - and the series rides in
    // on the SAME search response, which is what makes the watching page able
    // to group a book whose narration is not catalogued.
    stubApi({
      '/api/v1/search': {
        results: [
          {
            kind: 'work',
            id: 'the-way-of-kings',
            title: 'The Way of Kings',
            authors: [{ id: 'brandon-sanderson', name: 'Brandon Sanderson' }],
            series: { id: 'the-stormlight-archive', name: 'The Stormlight Archive', position: '1' },
          },
        ],
      },
    })
    const got = await resolve([
      book({
        title: 'The Way of Kings',
        asin: 'B0041OTH2E',
        authors: ['Brandon Sanderson'],
        language: 'en',
      }),
    ])
    expect(got.inDatabase).toEqual([])
    expect(got.newBooks).toHaveLength(1)
    expect(got.newBooks[0].existingWork?.id).toBe('the-way-of-kings')
    expect(got.newBooks[0].series?.id).toBe('the-stormlight-archive')
  })

  it('counts a book with no identifier as unchecked rather than new', async () => {
    stubApi({})
    const got = await resolve([book({ title: 'No Identifier' })])
    expect(got.noIdentifier).toBe(1)
    expect(got.cannotMatch).toHaveLength(1)
    expect(got.newBooks).toEqual([])
  })

  it('reports progress, then the matching phase', async () => {
    stubApi({ '/api/v1/lookup': { work: workCard('w', 'W'), recording_id: 'r' } })
    const seen: string[] = []
    await resolveLibrary(
      [book({ title: 'W', asin: 'B000000001' })],
      new AbortController().signal,
      (p) => seen.push(`${p.done}/${p.total}${p.matching ? ' matching' : ''}`)
    )
    expect(seen).toEqual(['0/1', '1/1', '1/1 matching'])
  })

  it('returns null when the caller aborts before the sweep starts', async () => {
    stubApi({})
    const ctrl = new AbortController()
    ctrl.abort()
    expect(
      await resolveLibrary([book({ title: 'W', asin: 'B000000001' })], ctrl.signal, () => {})
    ).toBeNull()
  })
})

describe('ownedWorks / groupBySeries', () => {
  const resolved = (over: Partial<ResolvedLibrary>): ResolvedLibrary => ({
    inDatabase: [],
    newBooks: [],
    cannotMatch: [],
    noIdentifier: 0,
    total: 0,
    skipped: 0,
    partialAuthors: [],
    ...over,
  })
  const stormlight = { id: 'the-stormlight-archive', name: 'The Stormlight Archive' }

  it('counts both kinds of hit, deduped by work id', () => {
    const got = ownedWorks(
      resolved({
        inDatabase: [
          { book: book({ title: 'A' }), work: workCard('wok', 'The Way of Kings', stormlight) },
          // The same work reached through a second edition in the export.
          { book: book({ title: 'A (UK)' }), work: workCard('wok', 'The Way of Kings', stormlight) },
        ],
        newBooks: [
          {
            book: book({ title: 'B' }),
            existingWork: { id: 'wor', title: 'Words of Radiance' },
            series: { id: stormlight.id, name: stormlight.name },
          },
          // A book whose work is not catalogued at all contributes nothing.
          { book: book({ title: 'C' }), existingWork: null, series: null },
        ],
      })
    )
    expect(got.map((w) => w.id)).toEqual(['wok', 'wor'])
  })

  it('groups by series and leaves a seriesless work out', () => {
    const works = [
      { id: 'wok', title: 'The Way of Kings', series: { ...stormlight, position: '1' } },
      { id: 'wor', title: 'Words of Radiance', series: { ...stormlight, position: '2' } },
      { id: 'phm', title: 'Project Hail Mary', series: null },
    ]
    expect(groupBySeries(works)).toEqual([
      { slug: stormlight.id, name: stormlight.name, works: ['wok', 'wor'] },
    ])
  })

  it('sorts series by name', () => {
    const got = groupBySeries([
      { id: 'a', title: 'A', series: { id: 'z', name: 'Zed' } },
      { id: 'b', title: 'B', series: { id: 'a', name: 'Alpha' } },
    ])
    expect(got.map((s) => s.name)).toEqual(['Alpha', 'Zed'])
  })
})
