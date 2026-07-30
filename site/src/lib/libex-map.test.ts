import { describe, it, expect, vi } from 'vitest'
import type { Mock } from 'vitest'
import {
  abridgedFromMarker,
  canOpenPrefilledIssue,
  cleanWorkTitle,
  libexToPrefill,
  mapLibexRegion,
  prefillFacts,
  releaseDatePart,
  resolveLibexCandidate,
  routeQuery,
} from './libex-map'
import type { LibexResolvers } from './libex-map'
import type { LookupResponse } from './api'
import type { LibexBook } from './libex'

const RETRIEVED = '2026-07-29'

function libexBook(extra: Partial<LibexBook> = {}): LibexBook {
  return {
    asin: 'B015RQON6I',
    title: 'Killing Floor',
    authors: [{ name: 'Lee Child' }],
    narrators: [{ name: 'Dick Hill' }],
    series: [],
    ...extra,
  }
}

describe('cleanWorkTitle', () => {
  it('strips a trailing edition marker in either bracket style', () => {
    expect(cleanWorkTitle('Mageling (Unabridged)')).toBe('Mageling')
    expect(cleanWorkTitle('Mageling [Abridged]')).toBe('Mageling')
    expect(cleanWorkTitle('Mageling (unabridged) ')).toBe('Mageling')
  })

  it('strips stacked markers in one pass', () => {
    expect(cleanWorkTitle('Mageling (Unabridged) (Abridged)')).toBe('Mageling')
  })

  it('leaves a title without a trailing marker alone', () => {
    expect(cleanWorkTitle('The Unabridged Journals')).toBe('The Unabridged Journals')
    expect(cleanWorkTitle('  Killing Floor  ')).toBe('Killing Floor')
  })

  it('never returns an empty title', () => {
    expect(cleanWorkTitle('(Unabridged)')).toBe('(Unabridged)')
  })
})

describe('abridgedFromMarker', () => {
  it('reads the edition the marker states, unabridged winning', () => {
    expect(abridgedFromMarker('X (Unabridged)')).toBe(false)
    expect(abridgedFromMarker('X [Abridged]')).toBe(true)
    expect(abridgedFromMarker('X (Unabridged) (Abridged)')).toBe(false)
  })

  it('is undefined without a marker', () => {
    expect(abridgedFromMarker('Killing Floor')).toBeUndefined()
  })
})

describe('mapLibexRegion', () => {
  it('accepts the known marketplaces', () => {
    for (const r of ['us', 'uk', 'ca', 'au', 'de', 'fr', 'es', 'it', 'jp', 'in', 'br']) {
      expect(mapLibexRegion(r)).toBe(r)
    }
  })

  it('normalizes case and aliases gb to uk', () => {
    expect(mapLibexRegion('US')).toBe('us')
    expect(mapLibexRegion('gb')).toBe('uk')
    expect(mapLibexRegion('GB')).toBe('uk')
  })

  it('rejects an unknown or missing region', () => {
    expect(mapLibexRegion('zz')).toBeUndefined()
    expect(mapLibexRegion('')).toBeUndefined()
    expect(mapLibexRegion(undefined)).toBeUndefined()
  })
})

describe('releaseDatePart', () => {
  it('trims the time from an ISO timestamp', () => {
    expect(releaseDatePart('2015-10-27T00:00:00+00:00')).toBe('2015-10-27')
    expect(releaseDatePart('2015-10-27')).toBe('2015-10-27')
  })

  it('drops anything that is not a full date', () => {
    expect(releaseDatePart('2015')).toBeUndefined()
    expect(releaseDatePart('October 2015')).toBeUndefined()
    expect(releaseDatePart(undefined)).toBeUndefined()
  })
})

describe('libexToPrefill', () => {
  it('maps the title, stripping an edition marker', () => {
    const p = libexToPrefill(libexBook({ title: 'Mageling (Unabridged)' }), RETRIEVED)
    expect(p.title).toBe('Mageling')
  })

  it('seeds abridged from a stripped marker when libex stated no format', () => {
    expect(libexToPrefill(libexBook({ title: 'Mageling (Unabridged)' }), RETRIEVED).abridged).toBe(
      false
    )
    expect(libexToPrefill(libexBook({ title: 'Mageling [Abridged]' }), RETRIEVED).abridged).toBe(
      true
    )
  })

  it("prefers libex's stated bookFormat over the title marker", () => {
    const p = libexToPrefill(
      libexBook({ title: 'Mageling (Unabridged)', bookFormat: 'abridged' }),
      RETRIEVED
    )
    expect(p.abridged).toBe(true)
  })

  it('leaves abridged unstated when neither the format nor the title says', () => {
    expect(libexToPrefill(libexBook({ bookFormat: 'something-else' }), RETRIEVED).abridged)
      .toBeUndefined()
    expect(libexToPrefill(libexBook(), RETRIEVED).abridged).toBeUndefined()
  })

  it('carries the authors and narrators', () => {
    const p = libexToPrefill(
      libexBook({
        authors: [{ name: 'Lee Child' }, { name: 'Andrew Child' }],
        narrators: [{ name: 'Jeff Harding' }, { name: 'Philip Pullman' }],
      }),
      RETRIEVED
    )
    expect(p.authors).toEqual(['Lee Child', 'Andrew Child'])
    expect(p.narrators).toEqual(['Jeff Harding', 'Philip Pullman'])
  })

  it('normalizes a language word to a BCP-47 code', () => {
    expect(libexToPrefill(libexBook({ language: 'english' }), RETRIEVED).language).toBe('en')
    expect(libexToPrefill(libexBook({ language: 'German' }), RETRIEVED).language).toBe('de')
  })

  it('leaves the language blank when the word does not map', () => {
    // The form field is required, so the contributor fills it in - we never guess.
    expect(libexToPrefill(libexBook({ language: 'klingon' }), RETRIEVED).language).toBeUndefined()
    expect(libexToPrefill(libexBook(), RETRIEVED).language).toBeUndefined()
  })

  it('maps the first series membership to name + position', () => {
    const p = libexToPrefill(
      libexBook({ series: [{ name: 'Jack Reacher', position: '1' }] }),
      RETRIEVED
    )
    expect(p.seriesName).toBe('Jack Reacher')
    expect(p.seriesPosition).toBe('1')
  })

  it('accepts every position shape the intake grammar allows', () => {
    for (const position of ['0', '1', '12', '2.5', '1-3', '1-3.5']) {
      const p = libexToPrefill(libexBook({ series: [{ name: 'S', position }] }), RETRIEVED)
      expect(p.seriesPosition).toBe(position)
      expect(p.sources).not.toContain('Series position as listed')
    }
  })

  it('omits a position outside the grammar but NOTES it in the sources text', () => {
    // Intake would reject "1a", so leaving it in the field just fails there; and
    // dropping it silently would land the work in the series with no position at
    // all. The fact reaches a human instead.
    const p = libexToPrefill(libexBook({ series: [{ name: 'S', position: '1a' }] }), RETRIEVED)
    expect(p.seriesName).toBe('S')
    expect(p.seriesPosition).toBeUndefined()
    expect(p.sources).toContain('Series position as listed: "1a"')
  })

  it('notes additional series memberships in the sources text', () => {
    const p = libexToPrefill(
      libexBook({
        series: [
          { name: 'Jack Reacher', position: '1' },
          { name: 'Reacher Omnibus', position: '1-3' },
          { name: 'Thriller Collection' },
        ],
      }),
      RETRIEVED
    )
    expect(p.seriesName).toBe('Jack Reacher')
    expect(p.sources).toContain('Also listed in: Reacher Omnibus #1-3; Thriller Collection')
  })

  it('maps the runtime and the publisher', () => {
    const p = libexToPrefill(
      libexBook({ lengthMinutes: 1067, publisher: 'Random House Audio' }),
      RETRIEVED
    )
    expect(p.runtimeMin).toBe(1067)
    expect(p.publisher).toBe('Random House Audio')
  })

  it('maps the release date to its date part only', () => {
    const p = libexToPrefill(libexBook({ releaseDate: '2015-10-27T00:00:00+00:00' }), RETRIEVED)
    expect(p.releaseDate).toBe('2015-10-27')
  })

  it('carries the region and ASIN, dropping an unknown region', () => {
    expect(libexToPrefill(libexBook({ region: 'us' }), RETRIEVED).region).toBe('us')
    expect(libexToPrefill(libexBook({ region: 'zz' }), RETRIEVED).region).toBeUndefined()
    expect(libexToPrefill(libexBook(), RETRIEVED).asin).toBe('B015RQON6I')
  })

  it('notes an unrecognised region in the sources text', () => {
    // A bare ASIN DEFAULTS to us on intake, so a marketplace we could not
    // validate has to be visible to the reviewer rather than quietly discarded.
    const p = libexToPrefill(libexBook({ region: 'zz' }), RETRIEVED)
    expect(p.region).toBeUndefined()
    expect(p.sources).toContain('Region as listed: "zz"')
    // A recognised region is just carried; there is nothing to report.
    expect(libexToPrefill(libexBook({ region: 'uk' }), RETRIEVED).sources).not.toContain(
      'Region as listed'
    )
  })

  it('carries the cover URL the parser already filtered to https', () => {
    // The https rule lives at the parse boundary (libex.parseLibexBook), so a
    // LibexBook cannot carry an http URL to begin with - this only pins that the
    // mapping does not lose it.
    expect(
      libexToPrefill(libexBook({ imageUrl: 'https://m.media-amazon.com/i.jpg' }), RETRIEVED)
        .coverUrl
    ).toBe('https://m.media-amazon.com/i.jpg')
    expect(libexToPrefill(libexBook(), RETRIEVED).coverUrl).toBeUndefined()
  })

  it('composes the sources text: the marker line first, then the human line', () => {
    const p = libexToPrefill(libexBook(), RETRIEVED)
    const lines = p.sources.split('\n')
    expect(lines[0]).toBe('libex: B015RQON6I')
    expect(lines[1]).toBe(
      'Libex (libex.lostcartographer.xyz) lookup for B015RQON6I (retrieved 2026-07-29)'
    )
  })

  it('defaults the retrieved date to today', () => {
    const p = libexToPrefill(libexBook())
    expect(p.sources).toContain(`(retrieved ${new Date().toISOString().slice(0, 10)})`)
  })

  it('keeps the audio release date on the recording half', () => {
    // It describes the RECORDING; asserting it on the work would claim a 2015
    // first publication for a 1997 novel. That the work half never receives it is
    // pinned on the issue URL (github-prefill.test.ts: no work_first_published),
    // which is where a regression would actually show.
    const p = libexToPrefill(libexBook({ releaseDate: '2015-10-27T00:00:00+00:00' }), RETRIEVED)
    expect(p.releaseDate).toBe('2015-10-27')
  })

  it('never contributes a subtitle or an ISBN', () => {
    // Neither libex field is the fact its name suggests - an Audible "subtitle"
    // is a tagline or a series designation, and its "isbn" is at least sometimes
    // the PRINT edition's, which upstream treats as a hard recording dedup key.
    // Both are unreachable by construction (libex.ts's whitelist), so this pins
    // the PREFILL shape: nothing here may reintroduce them.
    const p = libexToPrefill(libexBook(), RETRIEVED)
    expect(Object.keys(p).sort()).not.toContain('subtitle')
    expect(Object.keys(p).sort()).not.toContain('isbn')
  })
})

describe('prefillFacts', () => {
  const full = libexToPrefill(
    libexBook({
      title: 'Killing Floor',
      region: 'us',
      publisher: 'Random House Audio',
      language: 'english',
      bookFormat: 'unabridged',
      releaseDate: '2015-10-27T00:00:00+00:00',
      imageUrl: 'https://m.media-amazon.com/images/I/71WZxAS.jpg',
      lengthMinutes: 1067,
      series: [{ name: 'Jack Reacher', position: '1' }],
    }),
    RETRIEVED
  )

  function value(facts: ReturnType<typeof prefillFacts>, key: string): string | null | undefined {
    return facts.find((f) => f.key === key)?.value
  }

  it('lists every fact the issue will carry', () => {
    const facts = prefillFacts(full)
    expect(facts.map((f) => f.key)).toEqual([
      'title',
      'authors',
      'narrators',
      'language',
      'series',
      'abridged',
      'runtime',
      'release',
      'publisher',
      'asin',
      'cover',
      'sources',
    ])
    expect(value(facts, 'title')).toBe('Killing Floor')
    expect(value(facts, 'authors')).toBe('Lee Child')
    expect(value(facts, 'narrators')).toBe('Dick Hill')
    expect(value(facts, 'language')).toBe('English (en)')
    expect(value(facts, 'series')).toBe('Jack Reacher #1')
    expect(value(facts, 'abridged')).toBe('Unabridged')
    expect(value(facts, 'runtime')).toBe('17h 47m (1067 minutes)')
    expect(value(facts, 'release')).toBe('2015-10-27')
    expect(value(facts, 'publisher')).toBe('Random House Audio')
    expect(value(facts, 'asin')).toBe('US: B015RQON6I')
    expect(value(facts, 'cover')).toBe('https://m.media-amazon.com/images/I/71WZxAS.jpg')
    expect(value(facts, 'sources')).toBe(full.sources)
  })

  it("shows the sources text verbatim, so an extra-series note is not hidden", () => {
    // The note is the ONE fact no other row carries: the form has a single series
    // field, so a second membership survives only in the provenance text.
    const facts = prefillFacts(
      libexToPrefill(
        libexBook({
          series: [
            { name: 'Jack Reacher', position: '1' },
            { name: 'Reacher Omnibus', position: '1-3' },
          ],
        }),
        RETRIEVED
      )
    )
    expect(value(facts, 'sources')).toContain('Also listed in: Reacher Omnibus #1-3')
  })

  it('writes a bare ASIN when no marketplace was recognised', () => {
    const facts = prefillFacts(libexToPrefill(libexBook({ region: 'zz' }), RETRIEVED))
    expect(value(facts, 'asin')).toBe('B015RQON6I')
  })

  it('shows an unstated fact as null rather than hiding the row', () => {
    const facts = prefillFacts(
      libexToPrefill(
        { asin: 'B0BARE0001', title: 'A Bare Record', authors: [], narrators: [], series: [] },
        RETRIEVED
      )
    )
    // Every row is present so the reader sees what they will need to complete.
    for (const key of ['authors', 'narrators', 'language', 'series', 'abridged', 'runtime']) {
      expect(value(facts, key)).toBeNull()
    }
    expect(value(facts, 'asin')).toBe('B0BARE0001')
  })
})

describe('canOpenPrefilledIssue', () => {
  it('refuses a record libex states no title for', () => {
    // The form's Title is required and intake hard-fails without it, so the /add
    // card must not offer "Continue on GitHub" for one - that hand-off would land
    // the reader on a submission that is rejected on arrival.
    const untitled = libexToPrefill(
      { asin: 'B0NOTITLE1', authors: [], narrators: [], series: [] },
      RETRIEVED
    )
    expect(untitled.title).toBe('')
    expect(canOpenPrefilledIssue(untitled)).toBe(false)
  })

  it('accepts a titled record even when every other field is unstated', () => {
    // Authors, narrators and language are required by the form too, but a reader
    // can type those in - a gap there is a prompt, not a blocker.
    const bare = libexToPrefill(
      { asin: 'B0BARE0001', title: 'A Bare Record', authors: [], narrators: [], series: [] },
      RETRIEVED
    )
    expect(canOpenPrefilledIssue(bare)).toBe(true)
  })
})

describe('routeQuery', () => {
  it('routes a product code to a direct record lookup, whatever its case', () => {
    // A lower-case paste used to fall through to a keyword search that finds
    // nothing and dead-ends, because looksLikeAsin anchors an upper-case "B0".
    expect(routeQuery('B015RQON6I')).toEqual({ kind: 'asin', asin: 'B015RQON6I' })
    expect(routeQuery('b015rqon6i')).toEqual({ kind: 'asin', asin: 'B015RQON6I' })
    expect(routeQuery('B015rqOn6i')).toEqual({ kind: 'asin', asin: 'B015RQON6I' })
    // Surrounding whitespace from a paste is trimmed before the test.
    expect(routeQuery('  b015rqon6i  ')).toEqual({ kind: 'asin', asin: 'B015RQON6I' })
  })

  it('routes an ordinary query to a search, preserving what was typed', () => {
    expect(routeQuery('Killing Floor')).toEqual({ kind: 'search', q: 'Killing Floor' })
    expect(routeQuery('  Lee Child  ')).toEqual({ kind: 'search', q: 'Lee Child' })
    // ASIN-ish but not an ASIN: wrong length, wrong prefix, or an ISBN.
    expect(routeQuery('B015RQON6')).toEqual({ kind: 'search', q: 'B015RQON6' })
    expect(routeQuery('A015RQON6I')).toEqual({ kind: 'search', q: 'A015RQON6I' })
    expect(routeQuery('9780451482266')).toEqual({ kind: 'search', q: '9780451482266' })
  })

  it('has nothing to do with an empty term', () => {
    expect(routeQuery('')).toBeNull()
    expect(routeQuery('   ')).toBeNull()
  })
})

describe('resolveLibexCandidate', () => {
  const match: LookupResponse = {
    work: { id: 'killing-floor', title: 'Killing Floor', authors: [{ id: 'lee-child', name: 'Lee Child' }] },
    recording_id: 'dick-hill',
  }

  /** The injected resolvers, narrowed to spies so a test can assert not only the
      verdict but WHICH calls it cost. */
  interface SpyResolvers extends LibexResolvers {
    fetchBook: Mock<(asin: string, signal?: AbortSignal) => Promise<LibexBook | null>>
    fetchLookup: Mock<(asin: string, signal?: AbortSignal) => Promise<LookupResponse | null>>
  }

  /** Spy resolvers, defaulting to "libex has the record, we do not". Each test
      overrides only the behaviour it is about. */
  function resolvers(
    impl: {
      book?: () => Promise<LibexBook | null>
      lookup?: () => Promise<LookupResponse | null>
    } = {}
  ): SpyResolvers {
    const book = impl.book ?? (async () => libexBook())
    const lookup = impl.lookup ?? (async () => null)
    return {
      fetchBook: vi.fn((_asin: string, _signal?: AbortSignal) => book()),
      fetchLookup: vi.fn((_asin: string, _signal?: AbortSignal) => lookup()),
    }
  }

  it('reports a catalogued ASIN as covered, never proposing it', async () => {
    const r = resolvers({ lookup: async () => match })
    const out = await resolveLibexCandidate('b015rqon6i', undefined, r)
    expect(out).toEqual({ kind: 'covered', asin: 'B015RQON6I', match })
  })

  it('proposes a picked search row WITHOUT re-fetching the record', async () => {
    // libex /search rows are complete BookResponse objects, so the pick must
    // cost only the catalogue lookup - re-fetching would double every click.
    const r = resolvers()
    const row = libexBook({ title: 'Killing Floor' })
    const out = await resolveLibexCandidate('B015RQON6I', row, r)
    expect(out.kind).toBe('propose')
    expect(out.kind === 'propose' && out.prefill.title).toBe('Killing Floor')
    expect(r.fetchBook).not.toHaveBeenCalled()
    expect(r.fetchLookup).toHaveBeenCalledTimes(1)
  })

  it('fetches the record for a directly entered ASIN and proposes it', async () => {
    const r = resolvers({ book: async () => libexBook({ title: 'Mageling (Unabridged)' }) })
    const out = await resolveLibexCandidate(' b015rqon6i ', undefined, r)
    expect(r.fetchBook).toHaveBeenCalledWith('B015RQON6I', undefined)
    expect(out.kind === 'propose' && out.prefill.title).toBe('Mageling')
  })

  it('reports an ASIN libex has never heard of as missing', async () => {
    const r = resolvers({ book: async () => null })
    await expect(resolveLibexCandidate('B0MISSING1', undefined, r)).resolves.toEqual({
      kind: 'missing',
      asin: 'B0MISSING1',
    })
  })

  it('still proposes when OUR catalogue lookup fails', async () => {
    // A catalogue outage must not block contributing: a failed lookup counts as
    // "not found", so the reader reaches the review card either way.
    const r = resolvers({
      lookup: async () => {
        throw new Error('lookup is down')
      },
    })
    const out = await resolveLibexCandidate('B015RQON6I', libexBook(), r)
    expect(out.kind).toBe('propose')
  })

  it('propagates a libex record failure (which is NOT the same as missing)', async () => {
    const r = resolvers({
      book: async () => {
        throw new Error('libex is down')
      },
    })
    await expect(resolveLibexCandidate('B015RQON6I', undefined, r)).rejects.toThrow('libex is down')
  })

  it('still reports covered when libex is down but OUR catalogue has the ASIN', async () => {
    // The two calls run together, so a rejected record fetch used to reject the
    // whole resolve even though the lookup had ALREADY matched - sending the
    // reader off to file by hand a work we already hold, which is exactly how a
    // duplicate issue gets opened. An established verdict outranks the outage.
    const r = resolvers({
      lookup: async () => match,
      book: async () => {
        throw new Error('libex is down')
      },
    })
    await expect(resolveLibexCandidate('B015RQON6I', undefined, r)).resolves.toEqual({
      kind: 'covered',
      asin: 'B015RQON6I',
      match,
    })
  })

  it('reports covered when BOTH sides fail to produce a book but the lookup matched', async () => {
    // The same rule with libex merely having no record: nothing to propose either
    // way, and the catalogue answer is the one that matters.
    const r = resolvers({ lookup: async () => match, book: async () => null })
    const out = await resolveLibexCandidate('B015RQON6I', undefined, r)
    expect(out.kind).toBe('covered')
  })

  it('passes the abort signal through to both calls', async () => {
    const r = resolvers()
    const signal = new AbortController().signal
    await resolveLibexCandidate('B015RQON6I', undefined, r, signal)
    expect(r.fetchBook).toHaveBeenCalledWith('B015RQON6I', signal)
    expect(r.fetchLookup).toHaveBeenCalledWith('B015RQON6I', signal)
  })
})
