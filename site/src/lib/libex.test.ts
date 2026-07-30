import { describe, it, expect, vi, afterEach } from 'vitest'
import { LIBEX_BASE, LibexError, libexBook, libexSearch, parseLibexBook, type LibexBook } from './libex'

/** A full libex BookResponse, including the fields our licensing policy forbids
    importing (description/summary/copyright/rating) and the two whose names
    mislead (subtitle/isbn). Shaped from a real /db/book response. */
function fullBookResponse(extra: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    asin: 'b015rqon6i',
    title: 'Killing Floor',
    subtitle: 'The first Jack Reacher novel in the No.1 bestselling thriller series',
    description: 'A publisher blurb that must never be imported.',
    summary: 'A longer publisher blurb that must never be imported.',
    region: 'US',
    regions: ['us'],
    publisher: 'Random House Audio',
    copyright: '(c)1997 Lee Child (P)2015 Random House Audio',
    isbn: '9780451482266',
    language: 'english',
    rating: 4.455519018841095,
    bookFormat: 'unabridged',
    releaseDate: '2015-10-27T00:00:00+00:00',
    explicit: false,
    imageUrl: 'https://m.media-amazon.com/images/I/71WZxAS+JUL.jpg',
    lengthMinutes: 1067,
    link: 'https://audible.com/pd/B015RQON6I',
    sku: 'BK_RAND_004357',
    authors: [{ id: 3300, asin: 'B000APO0PQ', name: 'Lee Child', image: null }],
    narrators: [{ name: 'Dick Hill', updatedAt: null }],
    genres: [{ asin: '18574426011', name: 'Literature & Fiction', type: 'Genres' }],
    series: [{ asin: 'B005NASKDG', name: 'Jack Reacher', region: 'us', position: '1' }],
    ...extra,
  }
}

/** Install a fetch stub that answers a queue of {status, body} responses and
    records the URLs it was called with. */
function stubFetch(responses: { status: number; body?: unknown }[]): string[] {
  const calls: string[] = []
  const queue = [...responses]
  vi.stubGlobal('fetch', (url: string) => {
    calls.push(String(url))
    const next = queue.shift() ?? { status: 500 }
    return Promise.resolve({
      ok: next.status >= 200 && next.status < 300,
      status: next.status,
      json: () => Promise.resolve(next.body),
    } as Response)
  })
  return calls
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('parseLibexBook', () => {
  it('never carries the forbidden copyrighted fields', () => {
    const book = parseLibexBook(fullBookResponse())
    expect(book).not.toBeNull()
    const keys = Object.keys(book as LibexBook)
    // The whitelist is field-by-field, so these cannot appear structurally.
    for (const forbidden of ['description', 'summary', 'copyright', 'rating']) {
      expect(keys).not.toContain(forbidden)
      expect((book as unknown as Record<string, unknown>)[forbidden]).toBeUndefined()
    }
    // Nor may any other unlisted field ride along (link, sku, explicit), nor the
    // two misleading ones: an Audible `subtitle` is a tagline or a series
    // designation, and its `isbn` is at least sometimes the PRINT edition's.
    // `genres` IS whitelisted, but only as raw claims to be mapped onto our own
    // vocabulary (audible-genres.ts) - never stored as the retailer wrote them.
    expect(keys.sort()).toEqual(
      [
        'asin',
        'authors',
        'bookFormat',
        'genres',
        'imageUrl',
        'language',
        'lengthMinutes',
        'narrators',
        'publisher',
        'region',
        'releaseDate',
        'series',
        'title',
      ].sort()
    )
    // And the whole object serialized carries none of the blurb text.
    expect(JSON.stringify(book)).not.toContain('publisher blurb')
  })

  it('copies the whitelisted facts', () => {
    const book = parseLibexBook(fullBookResponse()) as LibexBook
    expect(book.asin).toBe('B015RQON6I') // normalized to upper case
    expect(book.title).toBe('Killing Floor')
    expect(book.region).toBe('us') // normalized to lower case
    expect(book.publisher).toBe('Random House Audio')
    expect(book.language).toBe('english')
    expect(book.bookFormat).toBe('unabridged')
    expect(book.releaseDate).toBe('2015-10-27T00:00:00+00:00')
    expect(book.imageUrl).toBe('https://m.media-amazon.com/images/I/71WZxAS+JUL.jpg')
    expect(book.lengthMinutes).toBe(1067)
    expect(book.authors).toEqual([{ name: 'Lee Child' }])
    expect(book.narrators).toEqual([{ name: 'Dick Hill' }])
    expect(book.series).toEqual([{ name: 'Jack Reacher', position: '1' }])
    // A genre entry's "asin" is a browse-NODE id; the node `type` is not kept.
    expect(book.genres).toEqual([{ node: '18574426011', name: 'Literature & Fiction' }])
  })

  it('reads genre claims in every shape libex sends, dropping the empty ones', () => {
    const book = parseLibexBook({
      asin: 'B0TEST0009',
      genres: [
        { asin: '18574426011', name: 'Literature & Fiction', type: 'Genres' },
        { name: 'Epic' }, // no node id
        { asin: '12641527051' }, // no name
        'Crime Thrillers', // a bare string
        { type: 'Tags' }, // neither node nor name
        null,
      ],
    }) as LibexBook
    expect(book.genres).toEqual([
      { node: '18574426011', name: 'Literature & Fiction' },
      { name: 'Epic' },
      { node: '12641527051' },
      { name: 'Crime Thrillers' },
    ])
  })

  it('returns null without an ASIN, and for a non-object', () => {
    expect(parseLibexBook({ title: 'No identity' })).toBeNull()
    expect(parseLibexBook({ asin: '   ' })).toBeNull()
    expect(parseLibexBook(null)).toBeNull()
    expect(parseLibexBook('a string')).toBeNull()
    expect(parseLibexBook([])).toBeNull()
  })

  it('rejects a record whose ASIN is outside the ASIN grammar', () => {
    // The ASIN is the identity every downstream step keys on - a prefilled issue,
    // the catalogue lookup, the provenance line - so a malformed one makes the
    // whole record unusable rather than partially usable.
    expect(parseLibexBook({ asin: 'B015RQON6' })).toBeNull() // 9 chars
    expect(parseLibexBook({ asin: 'B015RQON6I7' })).toBeNull() // 11 chars
    expect(parseLibexBook({ asin: 'B015-QON6I' })).toBeNull() // punctuation
    expect(parseLibexBook({ asin: 'B015 QON6I' })).toBeNull() // interior space
    // Lower case is a valid ASIN written badly, so it is normalized, not rejected.
    expect(parseLibexBook({ asin: 'b015rqon6i' })?.asin).toBe('B015RQON6I')
    // Non-B0 product codes (older 10-char ASINs, ISBN-10-shaped) stay valid.
    expect(parseLibexBook({ asin: '0553588988' })?.asin).toBe('0553588988')
  })

  it('omits absent, null and empty fields rather than asserting them', () => {
    const book = parseLibexBook({
      asin: 'B0TEST0001',
      title: null,
      lengthMinutes: 0,
      authors: null,
      series: undefined,
    }) as LibexBook
    expect(book.title).toBeUndefined()
    expect(book.lengthMinutes).toBeUndefined() // 0 minutes is not a runtime
    expect(book.authors).toEqual([])
    expect(book.narrators).toEqual([])
    expect(book.series).toEqual([])
    expect(book.genres).toEqual([])
  })

  it('rounds a fractional runtime rather than dropping it', () => {
    // Both models carry whole minutes, so spurious precision is normalized -
    // deliberately, since a stated runtime is a fact worth keeping.
    expect((parseLibexBook({ asin: 'B0TEST0001', lengthMinutes: 1067.4 }) as LibexBook).lengthMinutes)
      .toBe(1067)
    expect((parseLibexBook({ asin: 'B0TEST0001', lengthMinutes: 1067.6 }) as LibexBook).lengthMinutes)
      .toBe(1068)
    // A sub-minute runtime still rounds to 0, which is not a runtime.
    expect((parseLibexBook({ asin: 'B0TEST0001', lengthMinutes: 0.4 }) as LibexBook).lengthMinutes)
      .toBeUndefined()
  })

  it('keeps only an https cover URL, at the parse boundary', () => {
    // Filtering here rather than downstream means no consumer - the results list
    // included - can render an http URL as mixed content.
    expect((parseLibexBook({ asin: 'B0TEST0001', imageUrl: 'https://x/i.jpg' }) as LibexBook).imageUrl)
      .toBe('https://x/i.jpg')
    expect((parseLibexBook({ asin: 'B0TEST0001', imageUrl: 'http://x/i.jpg' }) as LibexBook).imageUrl)
      .toBeUndefined()
    expect((parseLibexBook({ asin: 'B0TEST0001', imageUrl: '//x/i.jpg' }) as LibexBook).imageUrl)
      .toBeUndefined()
  })

  it('normalizes a numeric series position to its string form', () => {
    // libex's own model says string, but live responses sometimes send a number -
    // which used to vanish silently, landing the work in no series at all.
    const book = parseLibexBook({
      asin: 'B0TEST0003',
      series: [{ name: 'Jack Reacher', position: 1 }],
    }) as LibexBook
    expect(book.series).toEqual([{ name: 'Jack Reacher', position: '1' }])
    expect(
      (parseLibexBook({ asin: 'B0TEST0003', series: [{ name: 'S', position: 2.5 }] }) as LibexBook)
        .series
    ).toEqual([{ name: 'S', position: '2.5' }])
    // A position that is not a usable value is dropped, keeping the membership.
    expect(
      (
        parseLibexBook({
          asin: 'B0TEST0003',
          series: [{ name: 'S', position: Number.NaN }],
        }) as LibexBook
      ).series
    ).toEqual([{ name: 'S' }])
  })

  it('drops unnamed people and series entries', () => {
    const book = parseLibexBook({
      asin: 'B0TEST0002',
      authors: [{ name: 'Real Author' }, { asin: 'X' }, 'not an object'],
      series: [{ name: 'Real Series' }, { asin: 'Y', position: '2' }],
    }) as LibexBook
    expect(book.authors).toEqual([{ name: 'Real Author' }])
    expect(book.series).toEqual([{ name: 'Real Series' }])
  })
})

describe('libexSearch', () => {
  it('queries the keywords field (libex has no q parameter)', async () => {
    const calls = stubFetch([{ status: 200, body: [fullBookResponse()] }])
    const results = await libexSearch('killing floor')
    expect(calls).toHaveLength(1)
    const url = new URL(calls[0])
    expect(url.origin + url.pathname).toBe(`${LIBEX_BASE}/search`)
    expect(url.searchParams.get('keywords')).toBe('killing floor')
    expect(url.searchParams.get('limit')).toBe('20')
    expect(results).toHaveLength(1)
    expect(results[0].asin).toBe('B015RQON6I')
  })

  it('maps a 404 to no results (libex 404s instead of returning [])', async () => {
    stubFetch([{ status: 404, body: { error: 'No books found' } }])
    await expect(libexSearch('nothing at all')).resolves.toEqual([])
  })

  it('throws a LibexError for a real failure', async () => {
    stubFetch([{ status: 502 }])
    await expect(libexSearch('anything')).rejects.toBeInstanceOf(LibexError)
  })

  it('throws when a 200 body is not a list, rather than reporting no match', async () => {
    // Returning [] here would render as "Libex has no match for ...", so the day
    // libex wraps its rows in an envelope the whole route would die silently
    // instead of reporting a fault.
    stubFetch([{ status: 200, body: { books: [fullBookResponse()] } }])
    await expect(libexSearch('killing floor')).rejects.toBeInstanceOf(LibexError)
    stubFetch([{ status: 200, body: null }])
    await expect(libexSearch('killing floor')).rejects.toBeInstanceOf(LibexError)
  })

  it('does not call the API for an empty query', async () => {
    const calls = stubFetch([])
    await expect(libexSearch('   ')).resolves.toEqual([])
    expect(calls).toHaveLength(0)
  })

  it('skips rows that carry no ASIN', async () => {
    stubFetch([{ status: 200, body: [{ title: 'No asin' }, fullBookResponse()] }])
    const results = await libexSearch('killing floor')
    expect(results).toHaveLength(1)
  })
})

describe('libexBook', () => {
  it('reads the mirrored record first', async () => {
    const calls = stubFetch([{ status: 200, body: fullBookResponse() }])
    const book = await libexBook('B015RQON6I')
    expect(calls).toEqual([`${LIBEX_BASE}/db/book/B015RQON6I`])
    expect(book?.title).toBe('Killing Floor')
  })

  it('falls back to the live record when the mirror has not seen the ASIN', async () => {
    const calls = stubFetch([
      { status: 404, body: { error: 'not found' } },
      { status: 200, body: fullBookResponse({ asin: 'B0NEW00001' }) },
    ])
    const book = await libexBook('B0NEW00001')
    expect(calls).toEqual([`${LIBEX_BASE}/db/book/B0NEW00001`, `${LIBEX_BASE}/book/B0NEW00001`])
    expect(book?.asin).toBe('B0NEW00001')
  })

  it('falls back when the mirror answers 200 with an unusable body', async () => {
    // The mirror answering with something we cannot parse is the same situation
    // as it not having the title - and the live endpoint is where the record is.
    const calls = stubFetch([
      { status: 200, body: { detail: 'nothing useful here' } },
      { status: 200, body: fullBookResponse({ asin: 'B0NEW00002' }) },
    ])
    const book = await libexBook('B0NEW00002')
    expect(calls).toEqual([`${LIBEX_BASE}/db/book/B0NEW00002`, `${LIBEX_BASE}/book/B0NEW00002`])
    expect(book?.asin).toBe('B0NEW00002')
  })

  it('returns null when neither source has the ASIN', async () => {
    stubFetch([{ status: 404 }, { status: 404 }])
    await expect(libexBook('B0MISSING1')).resolves.toBeNull()
  })

  it('throws a LibexError for a non-404 failure', async () => {
    stubFetch([{ status: 500 }])
    await expect(libexBook('B015RQON6I')).rejects.toBeInstanceOf(LibexError)
  })

  it('percent-encodes the ASIN into the path', async () => {
    // A pasted value carrying a slash or a space can never escape the segment.
    const calls = stubFetch([{ status: 404 }, { status: 404 }])
    await libexBook('B0/1 5')
    expect(calls).toEqual([`${LIBEX_BASE}/db/book/B0%2F1%205`, `${LIBEX_BASE}/book/B0%2F1%205`])
  })
})

describe('cancellation', () => {
  it('works on an engine without the AbortSignal statics', async () => {
    // AbortSignal.any is Safari 17.4+ / Firefox 124+; on an older engine merely
    // reaching for it throws a TypeError, which the UI would report as "Libex
    // could not be reached" - blaming the service for the browser. Emulate such
    // an engine: the global loses both statics, the request still works.
    class NoStatics {}
    vi.stubGlobal('AbortSignal', NoStatics)
    stubFetch([{ status: 200, body: [fullBookResponse()] }])
    await expect(libexSearch('killing floor')).resolves.toHaveLength(1)
  })

  it("aborts the request with the caller's reason", async () => {
    const ctrl = new AbortController()
    // Answer only after the caller has cancelled, so the composed signal is what
    // rejects the call rather than the stub racing ahead of it.
    vi.stubGlobal('fetch', (_url: string, init?: RequestInit) => {
      const signal = init?.signal
      return new Promise((_resolve, reject) => {
        signal?.addEventListener('abort', () => reject(signal.reason))
      })
    })
    const pending = libexSearch('killing floor', ctrl.signal)
    ctrl.abort(new DOMException('the reader typed again', 'AbortError'))
    await expect(pending).rejects.toMatchObject({ name: 'AbortError' })
  })

  it('passes a signal that is already aborted straight through', async () => {
    const ctrl = new AbortController()
    ctrl.abort(new DOMException('gone', 'AbortError'))
    let seen: AbortSignal | undefined
    vi.stubGlobal('fetch', (_url: string, init?: RequestInit) => {
      seen = init?.signal ?? undefined
      return Promise.reject(seen?.reason)
    })
    await expect(libexSearch('killing floor', ctrl.signal)).rejects.toMatchObject({
      name: 'AbortError',
    })
    // Composed, not the caller's own signal - the timeout rides on it too.
    expect(seen).not.toBe(ctrl.signal)
    expect(seen?.aborted).toBe(true)
  })
})
