import { describe, it, expect } from 'vitest'
import {
  parseExport,
  normalizeAsin,
  normalizeIsbn,
  partitionByIdentifier,
  isContributableOnMiss,
  matchExistingWork,
  authorSearchKeys,
  candidatesForBook,
  dedupeCandidates,
  authorKey,
  type ParsedBook,
  type WorkCandidate,
} from './import-parse'

// A minimal, valid OpenAudible entry with just enough to be detected + parsed.
// Individual tests spread extra fields on top.
function openAudibleEntry(extra: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    asin: 'B0123ABCDE',
    title: 'A Title',
    narrated_by: 'A Narrator',
    ...extra,
  }
}

// A ParsedBook with sensible empty defaults, overridden per test. Not produced
// by the parser - a hand-built fixture for the classification helpers.
function parsedBook(extra: Partial<ParsedBook> = {}): ParsedBook {
  return {
    title: '',
    authors: [],
    narrators: [],
    raw: {},
    ...extra,
  }
}

describe('parseExport - format detection', () => {
  it('detects an OpenAudible array by its lowercase keys and maps books', () => {
    const text = JSON.stringify([openAudibleEntry({ title_short: 'Mapped Title' })])
    const out = parseExport(text)
    expect(out.format).toBe('openaudible')
    expect(out.books).toHaveLength(1)
    expect(out.books[0].title).toBe('Mapped Title')
  })

  it('detects a Libation-shaped array (PascalCase keys) but does not parse it', () => {
    const text = JSON.stringify([
      { Title: 'Some Book', Authors: 'Jane Doe', AudibleProductId: 'B0ABCDEFGH' },
    ])
    const out = parseExport(text)
    expect(out.format).toBe('libation')
    expect(out.books).toEqual([])
  })

  it('detects a Libation wrapper object ({ Books: [...] })', () => {
    const text = JSON.stringify({
      Books: [{ Title: 'Wrapped', Authors: 'Jane Doe' }],
    })
    const out = parseExport(text)
    expect(out.format).toBe('libation')
    expect(out.books).toEqual([])
  })

  it('treats an array with no recognized keys as unknown', () => {
    const text = JSON.stringify([{ foo: 'bar', baz: 1 }])
    expect(parseExport(text).format).toBe('unknown')
  })

  it('treats an empty array as unknown', () => {
    expect(parseExport('[]').format).toBe('unknown')
  })

  it('treats a non-array, non-wrapper value as unknown', () => {
    expect(parseExport('{"foo":"bar"}').format).toBe('unknown')
    expect(parseExport('42').format).toBe('unknown')
    expect(parseExport('"a string"').format).toBe('unknown')
  })

  it('throws a friendly Error on invalid JSON', () => {
    expect(() => parseExport('{not json')).toThrow()
    expect(() => parseExport('{not json')).toThrow(/not valid JSON/)
  })

  it('skips non-object entries inside an OpenAudible array', () => {
    const text = JSON.stringify([openAudibleEntry(), 'a string', 42, null])
    const out = parseExport(text)
    expect(out.format).toBe('openaudible')
    expect(out.books).toHaveLength(1)
  })
})

describe('parseExport - field mapping', () => {
  function parseOne(entry: Record<string, unknown>): ParsedBook {
    const out = parseExport(JSON.stringify([entry]))
    expect(out.format).toBe('openaudible')
    return out.books[0]
  }

  it('title prefers title_short and falls back to title', () => {
    expect(parseOne(openAudibleEntry({ title: 'Long', title_short: 'Short' })).title).toBe(
      'Short'
    )
    expect(parseOne(openAudibleEntry({ title: 'Only Long', title_short: '' })).title).toBe(
      'Only Long'
    )
  })

  it('carries subtitle when present, undefined when empty', () => {
    expect(parseOne(openAudibleEntry({ subtitle: 'A Subtitle' })).subtitle).toBe('A Subtitle')
    expect(parseOne(openAudibleEntry({})).subtitle).toBeUndefined()
  })

  it('splits authors/narrators on commas and strips a listed role qualifier only', () => {
    const b = parseOne(
      openAudibleEntry({
        author: 'Jane Doe, John Smith - translator',
        narrated_by: 'Vox Player - narrator, Some - Band',
      })
    )
    expect(b.authors).toEqual(['Jane Doe', 'John Smith'])
    // "Vox Player - narrator" -> role stripped; "Some - Band" is not a known role
    // qualifier so the spaced-hyphen name is preserved intact.
    expect(b.narrators).toEqual(['Vox Player', 'Some - Band'])
  })

  it('maps a known language word to ISO and leaves languageRaw for an unmapped one', () => {
    const en = parseOne(openAudibleEntry({ language: 'English' }))
    expect(en.language).toBe('en')
    expect(en.languageRaw).toBe('English')

    const kl = parseOne(openAudibleEntry({ language: 'Klingon' }))
    expect(kl.language).toBeUndefined()
    expect(kl.languageRaw).toBe('Klingon')
  })

  it('accepts a known marketplace region (lowercased) and rejects an unknown one', () => {
    expect(parseOne(openAudibleEntry({ region: 'UK' })).region).toBe('uk')
    expect(parseOne(openAudibleEntry({ region: 'narnia' })).region).toBeUndefined()
  })

  it('validates seriesPosition (number or omnibus range) and rejects garbage', () => {
    expect(parseOne(openAudibleEntry({ series_sequence: '1' })).seriesPosition).toBe('1')
    expect(parseOne(openAudibleEntry({ series_sequence: '2.5' })).seriesPosition).toBe('2.5')
    expect(parseOne(openAudibleEntry({ series_sequence: '1-3.5' })).seriesPosition).toBe('1-3.5')
    expect(parseOne(openAudibleEntry({ series_sequence: 'book one' })).seriesPosition).toBeUndefined()
  })

  it('computes runtimeMin as round(seconds/60), from a number or a string', () => {
    expect(parseOne(openAudibleEntry({ seconds: 3630 })).runtimeMin).toBe(61) // 60.5 -> 61
    expect(parseOne(openAudibleEntry({ seconds: '3630' })).runtimeMin).toBe(61)
    expect(parseOne(openAudibleEntry({ seconds: 0 })).runtimeMin).toBeUndefined()
    expect(parseOne(openAudibleEntry({})).runtimeMin).toBeUndefined()
  })

  it('keeps releaseDate only when it matches YYYY-MM-DD', () => {
    expect(parseOne(openAudibleEntry({ release_date: '2021-05-04' })).releaseDate).toBe(
      '2021-05-04'
    )
    expect(parseOne(openAudibleEntry({ release_date: '05/04/2021' })).releaseDate).toBeUndefined()
    expect(parseOne(openAudibleEntry({ release_date: '2021' })).releaseDate).toBeUndefined()
  })

  it('sets coverUrl only for an https image_url', () => {
    expect(
      parseOne(openAudibleEntry({ image_url: 'https://img.example/cover.jpg' })).coverUrl
    ).toBe('https://img.example/cover.jpg')
    expect(
      parseOne(openAudibleEntry({ image_url: 'http://img.example/cover.jpg' })).coverUrl
    ).toBeUndefined()
  })

  it('treats abridged as tri-state (true/false kept, absent/other undefined)', () => {
    expect(parseOne(openAudibleEntry({ abridged: true })).abridged).toBe(true)
    expect(parseOne(openAudibleEntry({ abridged: false })).abridged).toBe(false)
    expect(parseOne(openAudibleEntry({ abridged: 'true' })).abridged).toBe(true)
    expect(parseOne(openAudibleEntry({ abridged: 'false' })).abridged).toBe(false)
    expect(parseOne(openAudibleEntry({})).abridged).toBeUndefined()
    expect(parseOne(openAudibleEntry({ abridged: 'maybe' })).abridged).toBeUndefined()
  })

  it('coerces number/bool/null values defensively', () => {
    // A numeric asin would fail the ASIN regex (10 chars needed) so becomes
    // undefined - but the coercion itself must not throw.
    const b = parseOne(
      openAudibleEntry({
        asin: 1234567890, // numeric, coerces to "1234567890" (valid 10-char ASIN)
        title: 42, // number
        subtitle: null,
        author: true, // bool -> "true"
      })
    )
    expect(b.asin).toBe('1234567890')
    expect(b.title).toBe('42')
    expect(b.subtitle).toBeUndefined()
    expect(b.authors).toEqual(['true'])
  })

  it('counts chapters from an array and leaves chapterCount undefined otherwise', () => {
    expect(parseOne(openAudibleEntry({ chapters: [{}, {}, {}] })).chapterCount).toBe(3)
    expect(parseOne(openAudibleEntry({})).chapterCount).toBeUndefined()
  })
})

describe('normalizeAsin', () => {
  it('accepts a 10-char alphanumeric that does not start with B0 (looser than looksLikeAsin)', () => {
    expect(normalizeAsin('1234567890')).toBe('1234567890')
  })

  it('uppercases lowercase input', () => {
    expect(normalizeAsin('b0abcdefgh')).toBe('B0ABCDEFGH')
  })

  it('rejects wrong lengths and non-alphanumerics', () => {
    expect(normalizeAsin('SHORT')).toBe('')
    expect(normalizeAsin('B0ABCDEFGHI')).toBe('') // 11 chars
    expect(normalizeAsin('B0ABCDEF-H')).toBe('') // hyphen
    expect(normalizeAsin('')).toBe('')
  })
})

describe('normalizeIsbn', () => {
  it('strips spaces and hyphens', () => {
    expect(normalizeIsbn('978-0-13-468599-1')).toBe('9780134685991')
    expect(normalizeIsbn('0 306 40615 2')).toBe('0306406152')
  })

  it('keeps an ISBN-10 with a trailing X', () => {
    expect(normalizeIsbn('080442957X')).toBe('080442957X')
    expect(normalizeIsbn('080442957x')).toBe('080442957X')
  })

  it('rejects wrong lengths', () => {
    expect(normalizeIsbn('12345')).toBe('')
    expect(normalizeIsbn('12345678901')).toBe('') // 11 digits
    expect(normalizeIsbn('X230123456')).toBe('') // X only allowed last
  })
})

describe('partitionByIdentifier', () => {
  it('routes books with no asin/isbn to unidentified', () => {
    const a = parsedBook({ asin: 'B0AAAAAAAA' })
    const b = parsedBook({}) // no identifier
    const out = partitionByIdentifier([a, b])
    expect(out.identified).toEqual([a])
    expect(out.unidentified).toEqual([b])
  })

  it('dedupes by identifier, keeping the first', () => {
    const first = parsedBook({ asin: 'B0DUPEAAAA', title: 'first' })
    const second = parsedBook({ asin: 'B0DUPEAAAA', title: 'second' })
    const out = partitionByIdentifier([first, second])
    expect(out.identified).toEqual([first])
    expect(out.unidentified).toEqual([])
  })

  it('collides an asin with a matching isbn value (the dedupe key is asin||isbn)', () => {
    // A book keyed on asin "SHAREDVAL0" and another keyed on isbn "SHAREDVAL0"
    // produce the same id, so the second is treated as a duplicate.
    const byAsin = parsedBook({ asin: 'SHAREDVAL0', title: 'via asin' })
    const byIsbn = parsedBook({ isbn: 'SHAREDVAL0', title: 'via isbn' })
    const out = partitionByIdentifier([byAsin, byIsbn])
    expect(out.identified).toEqual([byAsin])
    expect(out.unidentified).toEqual([])
  })
})

describe('isContributableOnMiss', () => {
  it('is true only when language is defined', () => {
    expect(isContributableOnMiss(parsedBook({ language: 'en' }))).toBe(true)
    expect(isContributableOnMiss(parsedBook({}))).toBe(false)
    expect(isContributableOnMiss(parsedBook({ languageRaw: 'Klingon' }))).toBe(false)
  })
})

describe('matchExistingWork', () => {
  it('matches an exact normalized title with a shared author', () => {
    const book = parsedBook({ title: 'Skysworn', authors: ['Will Wight'] })
    const cands: WorkCandidate[] = [
      { id: 'w1', title: 'Skysworn', authors: [{ name: 'Will Wight' }] },
    ]
    expect(matchExistingWork(book, cands)).toEqual({ id: 'w1', title: 'Skysworn' })
  })

  it('matches when the work title is a token-subset of the book title (the Cradle case)', () => {
    const book = parsedBook({
      title: 'Skysworn - Cradle, Book 4',
      authors: ['Will Wight'],
    })
    const cands: WorkCandidate[] = [
      { id: 'w1', title: 'Skysworn', authors: [{ name: 'Will Wight' }] },
    ]
    expect(matchExistingWork(book, cands)).toEqual({ id: 'w1', title: 'Skysworn' })
  })

  it('does NOT match a subset title with a different author', () => {
    const book = parsedBook({
      title: 'Skysworn - Cradle, Book 4',
      authors: ['Someone Else'],
    })
    const cands: WorkCandidate[] = [
      { id: 'w1', title: 'Skysworn', authors: [{ name: 'Will Wight' }] },
    ]
    expect(matchExistingWork(book, cands)).toBeNull()
  })

  it('returns null with no candidates', () => {
    const book = parsedBook({ title: 'Skysworn', authors: ['Will Wight'] })
    expect(matchExistingWork(book, [])).toBeNull()
  })

  it('returns null for a book with an empty title', () => {
    const cands: WorkCandidate[] = [
      { id: 'w1', title: 'Skysworn', authors: [{ name: 'Will Wight' }] },
    ]
    expect(matchExistingWork(parsedBook({ title: '', authors: ['Will Wight'] }), cands)).toBeNull()
  })

  it('prefers the exact-title candidate over a looser one', () => {
    const book = parsedBook({ title: 'Skysworn', authors: ['Will Wight'] })
    const cands: WorkCandidate[] = [
      // A looser superset title first, then the exact one.
      { id: 'loose', title: 'Skysworn Cradle Book 4', authors: [{ name: 'Will Wight' }] },
      { id: 'exact', title: 'Skysworn', authors: [{ name: 'Will Wight' }] },
    ]
    expect(matchExistingWork(book, cands)).toEqual({ id: 'exact', title: 'Skysworn' })
  })

  it('matches an exact title even when the book lists no authors', () => {
    const book = parsedBook({ title: 'Skysworn', authors: [] })
    const cands: WorkCandidate[] = [
      { id: 'w1', title: 'Skysworn', authors: [{ name: 'Will Wight' }] },
    ]
    expect(matchExistingWork(book, cands)).toEqual({ id: 'w1', title: 'Skysworn' })
  })

  it('does NOT loosely match when the book lists no authors (loose needs a shared author)', () => {
    const book = parsedBook({ title: 'Skysworn - Cradle, Book 4', authors: [] })
    const cands: WorkCandidate[] = [
      { id: 'w1', title: 'Skysworn', authors: [{ name: 'Will Wight' }] },
    ]
    expect(matchExistingWork(book, cands)).toBeNull()
  })
})

describe('authorKey / authorSearchKeys', () => {
  it('normalizes case and collapses inter-word punctuation to one key', () => {
    expect(authorKey('Brandon Sanderson')).toBe('brandon sanderson')
    expect(authorKey('Brandon   Sanderson')).toBe('brandon sanderson')
    expect(authorKey('Brandon-Sanderson')).toBe('brandon sanderson')
    expect(authorKey('Brandon Sanderson!')).toBe('brandon sanderson')
  })

  // A within-word diacritic folds to its ASCII base (NFKD splits the accented
  // letter into base + a combining mark, which normKey strips to '' so it does
  // NOT become a spurious word boundary), so an accented spelling collapses to
  // the same key as its ASCII form.
  it('folds a within-word diacritic to its ASCII base', () => {
    expect(authorKey('Émile Zola')).toBe('emile zola')
    expect(authorKey('Emile Zola')).toBe('emile zola')
    expect(authorKey('Émile Zola')).toBe(authorKey('Emile Zola'))
    expect(authorKey('Charlotte Brontë')).toBe('charlotte bronte')
  })

  it('returns distinct author keys across books, first display spelling kept', () => {
    const books = [
      parsedBook({ authors: ['Brandon Sanderson', 'Jane Doe'] }),
      parsedBook({ authors: ['Brandon Sanderson!'] }), // same key, extra punctuation
    ]
    const keys = authorSearchKeys(books)
    expect(keys.size).toBe(2)
    // The first-seen display spelling wins for the shared key.
    expect(keys.get(authorKey('Brandon Sanderson'))).toBe('Brandon Sanderson')
    expect(keys.get(authorKey('Jane Doe'))).toBe('Jane Doe')
  })
})

describe('candidatesForBook', () => {
  it('gathers works for all of a book authors using normalized keys', () => {
    const w1: WorkCandidate = {
      id: 'w1',
      title: 'A',
      authors: [{ name: 'Brandon Sanderson' }],
    }
    const w2: WorkCandidate = { id: 'w2', title: 'B', authors: [{ name: 'Jane Doe' }] }
    const byAuthor = new Map<string, WorkCandidate[]>([
      // Stored under the same normalized key the book authors produce, even
      // when the display spelling carries punctuation.
      [authorKey('Brandon-Sanderson'), [w1]],
      [authorKey('Jane Doe'), [w2]],
    ])
    const book = parsedBook({ authors: ['Brandon Sanderson!', 'Jane Doe'] })
    expect(candidatesForBook(book, byAuthor)).toEqual([w1, w2])
  })

  it('returns an empty list when no author has works', () => {
    const book = parsedBook({ authors: ['Nobody Known'] })
    expect(candidatesForBook(book, new Map())).toEqual([])
  })
})

describe('dedupeCandidates', () => {
  it('drops duplicate work ids and keeps the first', () => {
    const a: WorkCandidate = { id: 'w1', title: 'First', authors: [] }
    const b: WorkCandidate = { id: 'w1', title: 'Second', authors: [] }
    const c: WorkCandidate = { id: 'w2', title: 'Other', authors: [] }
    expect(dedupeCandidates([a, b, c])).toEqual([a, c])
  })
})
