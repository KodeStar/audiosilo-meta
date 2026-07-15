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
    format: 'openaudible',
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

  it('detects a Libation-shaped array (PascalCase keys) and maps books', () => {
    const text = JSON.stringify([
      {
        Title: 'Some Book',
        AuthorNames: 'Jane Doe',
        NarratorNames: 'Vox Player',
        AudibleProductId: 'B0ABCDEFGH',
      },
    ])
    const out = parseExport(text)
    expect(out.format).toBe('libation')
    expect(out.books).toHaveLength(1)
    expect(out.books[0].title).toBe('Some Book')
    expect(out.books[0].asin).toBe('B0ABCDEFGH')
    expect(out.books[0].format).toBe('libation')
  })

  it('detects a Libation wrapper object ({ Books: [...] }) and maps books', () => {
    const text = JSON.stringify({
      Books: [{ Title: 'Wrapped', AuthorNames: 'Jane Doe', NarratorNames: 'Vox' }],
    })
    const out = parseExport(text)
    expect(out.format).toBe('libation')
    expect(out.books).toHaveLength(1)
    expect(out.books[0].title).toBe('Wrapped')
  })

  it('detects an audiosilo folder scan by its format discriminator and maps books', () => {
    const text = JSON.stringify({
      format: 'audiosilo-folder-scan',
      version: 1,
      root: '/Users/me/Audiobooks',
      books: [
        {
          path: 'Lee Child/Jack Reacher/01 - Killing Floor',
          title: 'Killing Floor',
          authors: ['Lee Child'],
          asin: 'B076HYPQLK',
          files: ['Killing Floor.m4b'],
          audio_files: 1,
        },
      ],
    })
    const out = parseExport(text)
    expect(out.format).toBe('folderscan')
    expect(out.books).toHaveLength(1)
    expect(out.books[0].title).toBe('Killing Floor')
    expect(out.books[0].asin).toBe('B076HYPQLK')
    expect(out.books[0].format).toBe('folderscan')
  })

  it('treats a folder scan with an unsupported version as unknown (skew fails loud)', () => {
    const text = JSON.stringify({
      format: 'audiosilo-folder-scan',
      version: 2,
      root: '/x',
      books: [{ path: 'a', title: 'A', files: ['a.mp3'], audio_files: 1 }],
    })
    expect(parseExport(text).format).toBe('unknown')
  })

  it('treats an array with no recognized keys as unknown', () => {
    const text = JSON.stringify([{ foo: 'bar', baz: 1 }])
    expect(parseExport(text).format).toBe('unknown')
  })

  it('does NOT misdetect a foreign export on generic keys like Title (Goodreads-ish)', () => {
    const text = JSON.stringify([
      { Title: 'Some Book', Author: 'Jane Doe', ISBN13: '9780000000000', 'My Rating': 5 },
    ])
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
    // 1-29s rounds to 0 minutes - omitted, not asserted as a 0-minute fact
    // (mirrors the Go importer's runtimeMin > 0 emit rule).
    expect(parseOne(openAudibleEntry({ seconds: 10 })).runtimeMin).toBeUndefined()
    expect(parseOne(openAudibleEntry({})).runtimeMin).toBeUndefined()
  })

  it('keeps releaseDate for YYYY, YYYY-MM, and YYYY-MM-DD (Go datePattern parity)', () => {
    expect(parseOne(openAudibleEntry({ release_date: '2021-05-04' })).releaseDate).toBe(
      '2021-05-04'
    )
    // The recording schema's date_flex accepts partial dates; a bare year is a
    // kept fact, never fabricated into -01-01.
    expect(parseOne(openAudibleEntry({ release_date: '2021' })).releaseDate).toBe('2021')
    expect(parseOne(openAudibleEntry({ release_date: '2021-05' })).releaseDate).toBe('2021-05')
    expect(parseOne(openAudibleEntry({ release_date: '05/04/2021' })).releaseDate).toBeUndefined()
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

describe('parseExport - Libation field mapping', () => {
  function parseOne(entry: Record<string, unknown>): ParsedBook {
    const out = parseExport(JSON.stringify([{ AudibleProductId: 'B0AAAAAAAA', ...entry }]))
    expect(out.format).toBe('libation')
    return out.books[0]
  }

  it('maps the core factual fields', () => {
    const b = parseOne({
      AudibleProductId: 'B0CQDJ3PND',
      Title: 'Wind and Truth',
      Subtitle: 'Stormlight Archive, Book 5',
      AuthorNames: 'Brandon Sanderson',
      NarratorNames: 'Kate Reading, Michael Kramer',
      Language: 'English',
      Locale: 'uk',
      Publisher: 'Gollancz',
      LengthInMinutes: 3768,
      DatePublished: '2024-12-06T03:00:00',
      IsAbridged: false,
      PictureId: '51ZFAWrapyL',
    })
    expect(b.asin).toBe('B0CQDJ3PND')
    expect(b.title).toBe('Wind and Truth')
    expect(b.subtitle).toBe('Stormlight Archive, Book 5')
    expect(b.authors).toEqual(['Brandon Sanderson'])
    expect(b.narrators).toEqual(['Kate Reading', 'Michael Kramer'])
    expect(b.language).toBe('en')
    expect(b.region).toBe('uk')
    expect(b.publisher).toBe('Gollancz')
    expect(b.runtimeMin).toBe(3768) // LengthInMinutes is already minutes
    expect(b.releaseDate).toBe('2024-12-06')
    expect(b.abridged).toBe(false)
    expect(b.coverUrl).toBe('https://m.media-amazon.com/images/I/51ZFAWrapyL._SL500_.jpg')
  })

  it('percent-encodes a + in the cover PictureId', () => {
    const b = parseOne({ PictureId: '51zVN+Q+LcL' })
    expect(b.coverUrl).toBe('https://m.media-amazon.com/images/I/51zVN%2BQ%2BLcL._SL500_.jpg')
  })

  it('strips a listed role qualifier from author names', () => {
    const b = parseOne({
      AuthorNames: 'Kirill Klevanski, Valeria Kornosenko - introduction',
    })
    expect(b.authors).toEqual(['Kirill Klevanski', 'Valeria Kornosenko'])
  })

  it('surfaces the primary series from SeriesOrder (first positioned entry)', () => {
    // The Cosmere has no position; The Stormlight Archive is book 5 - the latter
    // is surfaced. Note SeriesOrder arrives with a leading space, which the parser
    // splits on the first colon so it is robust to trimming.
    const b = parseOne({
      SeriesNames: 'The Cosmere, The Stormlight Archive',
      SeriesOrder: ' : The Cosmere, 5 : The Stormlight Archive',
    })
    expect(b.seriesName).toBe('The Stormlight Archive')
    expect(b.seriesPosition).toBe('5')
  })

  it('treats the 999999999 sentinel as an unknown position', () => {
    const b = parseOne({
      SeriesNames: "Stephen Fry's Victorian Secrets",
      SeriesOrder: "999999999 : Stephen Fry's Victorian Secrets",
    })
    expect(b.seriesName).toBe("Stephen Fry's Victorian Secrets")
    expect(b.seriesPosition).toBeUndefined()
  })

  it('handles a series name containing a colon', () => {
    const b = parseOne({
      SeriesNames: 'Discworld, Discworld: Rincewind',
      SeriesOrder: '1 : Discworld, 1 : Discworld: Rincewind',
    })
    expect(b.seriesName).toBe('Discworld') // first positioned entry
    expect(b.seriesPosition).toBe('1')
  })

  it('maps full AudibleApi locale names to marketplace codes', () => {
    // Libation's Locale is 2-letter only for us/uk; the other marketplaces are
    // full names (verified against AudibleApi/Localization.cs).
    expect(parseOne({ Locale: 'germany' }).region).toBe('de')
    expect(parseOne({ Locale: 'france' }).region).toBe('fr')
    expect(parseOne({ Locale: 'japan' }).region).toBe('jp')
    expect(parseOne({ Locale: 'australia' }).region).toBe('au')
    expect(parseOne({ Locale: 'pre-amazon - germany' }).region).toBe('de')
    expect(parseOne({ Locale: 'uk' }).region).toBe('uk')
  })

  it('rejects an unknown marketplace Locale', () => {
    expect(parseOne({ Locale: 'narnia' }).region).toBeUndefined()
  })

  it('keeps a comma inside a series name intact (splits only at real claim starts)', () => {
    const single = parseOne({
      SeriesNames: 'Ready, Set, Go: The Story',
      SeriesOrder: '1 : Ready, Set, Go: The Story',
    })
    expect(single.seriesName).toBe('Ready, Set, Go: The Story')
    expect(single.seriesPosition).toBe('1')

    const multi = parseOne({
      SeriesNames: 'Ready, Set, Go: The Story, The Stormlight Archive',
      SeriesOrder: '1 : Ready, Set, Go: The Story, 5 : The Stormlight Archive',
    })
    expect(multi.seriesName).toBe('Ready, Set, Go: The Story')
    expect(multi.seriesPosition).toBe('1')
  })

  it('leaves language undefined but keeps languageRaw for an unmapped word', () => {
    const b = parseOne({ Language: 'Turkish' })
    expect(b.language).toBe('tr')
    const k = parseOne({ Language: 'Klingon' })
    expect(k.language).toBeUndefined()
    expect(k.languageRaw).toBe('Klingon')
  })

  it('carries no isbn or chapter data (Libation exports have none)', () => {
    const b = parseOne({ Title: 'X' })
    expect(b.isbn).toBeUndefined()
    expect(b.chapterCount).toBeUndefined()
  })
})

describe('parseExport - folder-scan field mapping', () => {
  function parseOne(book: Record<string, unknown>): ParsedBook {
    const out = parseExport(
      JSON.stringify({
        format: 'audiosilo-folder-scan',
        version: 1,
        root: '/x',
        books: [book],
      })
    )
    expect(out.format).toBe('folderscan')
    return out.books[0]
  }

  it('maps entries 1:1 including array authors/narrators', () => {
    const b = parseOne({
      path: 'Lee Child/Jack Reacher/01 - Killing Floor',
      title: 'Killing Floor',
      subtitle: 'A Jack Reacher Novel',
      authors: ['Lee Child'],
      narrators: ['Jeff Harding'],
      series: 'Jack Reacher',
      series_position: '1',
      asin: 'B076HYPQLK',
      publisher: 'Random House',
      release_date: '2017-11-02',
      language: 'en',
      runtime_min: 823,
      chapters: 34,
      files: ['Killing Floor.m4b'],
      audio_files: 1,
    })
    expect(b.title).toBe('Killing Floor')
    expect(b.subtitle).toBe('A Jack Reacher Novel')
    expect(b.authors).toEqual(['Lee Child'])
    expect(b.narrators).toEqual(['Jeff Harding'])
    expect(b.seriesName).toBe('Jack Reacher')
    expect(b.seriesPosition).toBe('1')
    expect(b.asin).toBe('B076HYPQLK')
    expect(b.releaseDate).toBe('2017-11-02')
    expect(b.language).toBe('en')
    expect(b.runtimeMin).toBe(823)
    expect(b.chapterCount).toBe(34)
    expect(b.region).toBeUndefined()
    expect(b.coverUrl).toBeUndefined()
  })

  it('accepts a language word as well as an ISO code', () => {
    expect(parseOne({ title: 'A', language: 'English' }).language).toBe('en')
    expect(parseOne({ title: 'A', language: 'de' }).language).toBe('de')
    expect(parseOne({ title: 'A', language: 'zzz' }).language).toBeUndefined()
  })

  it('maps ISO 639-2/3 codes (ID3 TLAN) to 639-1', () => {
    // TLAN is ISO 639-2, so a properly tagged MP3 library arrives 3-letter.
    expect(parseOne({ title: 'A', language: 'eng' }).language).toBe('en')
    expect(parseOne({ title: 'A', language: 'deu' }).language).toBe('de')
    expect(parseOne({ title: 'A', language: 'ger' }).language).toBe('de')
    expect(parseOne({ title: 'A', language: 'fra' }).language).toBe('fr')
    expect(parseOne({ title: 'A', language: 'jpn' }).language).toBe('ja')
    // An unknown 3-letter code stays unmapped but visible via languageRaw.
    const unknown = parseOne({ title: 'A', language: 'xyz' })
    expect(unknown.language).toBeUndefined()
    expect(unknown.languageRaw).toBe('xyz')
  })

  it('keeps a bare-year release_date (ID3 TYER) as-is', () => {
    expect(parseOne({ title: 'A', release_date: '2017' }).releaseDate).toBe('2017')
    expect(parseOne({ title: 'A', release_date: '2017-11-02' }).releaseDate).toBe('2017-11-02')
    expect(parseOne({ title: 'A', release_date: 'circa 2017' }).releaseDate).toBeUndefined()
  })

  it('omits optional fields that are absent', () => {
    const b = parseOne({ title: 'Bare', files: ['a.mp3'], audio_files: 1 })
    expect(b.asin).toBeUndefined()
    expect(b.isbn).toBeUndefined()
    expect(b.seriesName).toBeUndefined()
    expect(b.runtimeMin).toBeUndefined()
    expect(b.chapterCount).toBeUndefined()
  })

  it('routes a folder-scan book with no asin/isbn to the cannot-match bucket', () => {
    const out = parseExport(
      JSON.stringify({
        format: 'audiosilo-folder-scan',
        version: 1,
        root: '/x',
        books: [{ title: 'No Ids', files: ['a.mp3'], audio_files: 1 }],
      })
    )
    const { identified, unidentified } = partitionByIdentifier(out.books)
    expect(identified).toEqual([])
    expect(unidentified).toHaveLength(1)
  })

  it('never carries the folder-scan root or per-book path/files into a ParsedBook typed field', () => {
    const b = parseOne({
      path: '/secret/local/path',
      title: 'Private',
      files: ['/secret/local/path/x.m4b'],
      audio_files: 1,
    })
    // The local-only fields survive only inside raw (for the format-aware factual
    // subset to filter); no typed ParsedBook field exposes them.
    expect(b.title).toBe('Private')
    expect(JSON.stringify({ ...b, raw: undefined })).not.toContain('/secret/local/path')
  })
})

describe('parseExport - Audiobookshelf detection', () => {
  // A minimal full-shape ABS library item (book fields under media.metadata).
  function absItem(metadata: Record<string, unknown>, media: Record<string, unknown> = {}) {
    return {
      id: 'li_abc',
      ino: '123456',
      libraryId: 'lib_1',
      folderId: 'fol_1',
      path: '/audiobooks/Author/Title',
      relPath: 'Author/Title',
      mediaType: 'book',
      media: { coverPath: '/metadata/items/li_abc/cover.jpg', metadata, ...media },
      numFiles: 1,
      size: 123456789,
    }
  }

  function absPayload(...items: unknown[]) {
    return JSON.stringify({ results: items, total: items.length, limit: 0, page: 0 })
  }

  it('detects an ABS results payload by its media.metadata nesting', () => {
    const out = parseExport(absPayload(absItem({ title: 'A Book' })))
    expect(out.format).toBe('audiobookshelf')
    expect(out.books).toHaveLength(1)
    expect(out.books[0].title).toBe('A Book')
    expect(out.books[0].format).toBe('audiobookshelf')
  })

  it('does NOT misdetect ABS as OpenAudible or Libation (fields are nested)', () => {
    // The ABS item has asin/authors/series ONLY under media.metadata, so neither
    // the OpenAudible nor Libation top-level markers can match.
    const out = parseExport(
      absPayload(absItem({ title: 'X', asin: 'B0ABCDEFGH', authorName: 'Jane Doe' }))
    )
    expect(out.format).toBe('audiobookshelf')
  })

  it('does NOT misdetect OpenAudible/Libation/folder-scan as ABS', () => {
    expect(parseExport(JSON.stringify([openAudibleEntry()])).format).toBe('openaudible')
    expect(
      parseExport(
        JSON.stringify([{ Title: 'X', AuthorNames: 'A', NarratorNames: 'B', AudibleProductId: 'B0AAAAAAAA' }])
      ).format
    ).toBe('libation')
    expect(
      parseExport(
        JSON.stringify({
          format: 'audiosilo-folder-scan',
          version: 1,
          root: '/x',
          books: [{ title: 'A', files: ['a.mp3'], audio_files: 1 }],
        })
      ).format
    ).toBe('folderscan')
  })

  it('treats a results payload of non-ABS objects as unknown', () => {
    expect(parseExport(JSON.stringify({ results: [{ foo: 'bar' }], total: 1 })).format).toBe(
      'unknown'
    )
  })
})

describe('parseExport - audiosilo-books envelope', () => {
  function envelope(...books: Record<string, unknown>[]): string {
    return JSON.stringify({ format: 'audiosilo-books', version: 1, books })
  }

  it('detects the envelope by its format marker, NOT as OpenAudible despite the asin key', () => {
    const out = parseExport(
      envelope({
        title: 'Killing Floor',
        authors: ['Lee Child'],
        narrators: ['Jeff Harding'],
        series: 'Jack Reacher',
        series_position: '1',
        asin: 'B076HYPQLK',
        isbn: '9780553505405',
        language: 'en',
        release_date: '1997',
      })
    )
    expect(out.format).toBe('audiosilobooks')
    expect(out.books).toHaveLength(1)
    const b = out.books[0]
    expect(b.format).toBe('audiosilobooks')
    expect(b.title).toBe('Killing Floor')
    expect(b.authors).toEqual(['Lee Child'])
    expect(b.narrators).toEqual(['Jeff Harding'])
    expect(b.seriesName).toBe('Jack Reacher')
    expect(b.seriesPosition).toBe('1')
    expect(b.asin).toBe('B076HYPQLK')
    expect(b.isbn).toBe('9780553505405')
    expect(b.language).toBe('en')
    expect(b.releaseDate).toBe('1997')
  })

  it('carries subtitle, publisher, runtime, chapter count, and abridged when present', () => {
    const out = parseExport(
      envelope({
        title: 'A Book',
        subtitle: 'The Subtitle',
        publisher: 'Acme Audio',
        runtime_min: 742,
        chapters: 34,
        abridged: false,
      })
    )
    const b = out.books[0]
    expect(b.subtitle).toBe('The Subtitle')
    expect(b.publisher).toBe('Acme Audio')
    expect(b.runtimeMin).toBe(742)
    expect(b.chapterCount).toBe(34)
    expect(b.abridged).toBe(false)
  })

  it('treats an envelope with an unsupported version as unknown (skew fails loud)', () => {
    const text = JSON.stringify({ format: 'audiosilo-books', version: 2, books: [{ title: 'A' }] })
    expect(parseExport(text).format).toBe('unknown')
  })
})

describe('parseExport - Audiobookshelf field mapping (full shape)', () => {
  function parseOne(metadata: Record<string, unknown>, media: Record<string, unknown> = {}): ParsedBook {
    const item = {
      id: 'li_1',
      path: '/local/secret/path',
      media: { coverPath: '/metadata/items/li_1/cover.jpg', metadata, ...media },
    }
    const out = parseExport(JSON.stringify({ results: [item], total: 1 }))
    expect(out.format).toBe('audiobookshelf')
    return out.books[0]
  }

  it('maps the core factual fields (authors as objects, narrators as strings)', () => {
    const b = parseOne(
      {
        title: 'Killing Floor',
        subtitle: 'Jack Reacher, Book 1',
        authors: [{ id: 'au_1', name: 'Lee Child' }],
        narrators: ['Jeff Harding'],
        series: [{ id: 'se_1', name: 'Jack Reacher', sequence: '1' }],
        asin: 'B076HYPQLK',
        isbn: '9780553505405',
        language: 'English',
        publishedYear: '1997',
        publisher: 'Random House',
        abridged: false,
      },
      { duration: 44521.7, chapters: [{}, {}, {}] }
    )
    expect(b.title).toBe('Killing Floor')
    expect(b.subtitle).toBe('Jack Reacher, Book 1')
    expect(b.authors).toEqual(['Lee Child'])
    expect(b.narrators).toEqual(['Jeff Harding'])
    expect(b.seriesName).toBe('Jack Reacher')
    expect(b.seriesPosition).toBe('1')
    expect(b.asin).toBe('B076HYPQLK')
    expect(b.isbn).toBe('9780553505405')
    expect(b.language).toBe('en')
    expect(b.releaseDate).toBe('1997')
    expect(b.publisher).toBe('Random House')
    expect(b.abridged).toBe(false)
    expect(b.runtimeMin).toBe(742) // round(44521.7 / 60)
    expect(b.chapterCount).toBe(3)
    expect(b.region).toBeUndefined()
    expect(b.coverUrl).toBeUndefined()
  })

  it('picks the first positioned series entry when a book is in several', () => {
    const b = parseOne({
      title: 'Wind and Truth',
      series: [
        { name: 'The Cosmere' }, // no sequence
        { name: 'The Stormlight Archive', sequence: '5' },
      ],
    })
    expect(b.seriesName).toBe('The Stormlight Archive')
    expect(b.seriesPosition).toBe('5')
  })

  it('prefers a full publishedDate over the bare publishedYear', () => {
    const b = parseOne({ title: 'A', publishedDate: '2017-11-02', publishedYear: '2017' })
    expect(b.releaseDate).toBe('2017-11-02')
  })

  it('maps a language word, an ISO-639-2 code, or a 2-letter code loosely', () => {
    expect(parseOne({ title: 'A', language: 'English' }).language).toBe('en')
    expect(parseOne({ title: 'A', language: 'eng' }).language).toBe('en')
    expect(parseOne({ title: 'A', language: 'de' }).language).toBe('de')
    const k = parseOne({ title: 'A', language: 'Klingon' })
    expect(k.language).toBeUndefined()
    expect(k.languageRaw).toBe('Klingon')
  })

  it('omits a zero/absent runtime and an absent chapter count', () => {
    expect(parseOne({ title: 'A' }, { duration: 0 }).runtimeMin).toBeUndefined()
    expect(parseOne({ title: 'A' }, {}).runtimeMin).toBeUndefined()
    expect(parseOne({ title: 'A' }, {}).chapterCount).toBeUndefined()
  })

  it('never carries the ABS local coverPath or item path into a ParsedBook or its raw', () => {
    const b = parseOne({ title: 'Private', asin: 'B076HYPQLK' })
    const serialized = JSON.stringify(b)
    expect(serialized).not.toContain('coverPath')
    expect(serialized).not.toContain('/local/secret/path')
    expect(serialized).not.toContain('/metadata/items')
  })
})

describe('parseExport - Audiobookshelf field mapping (minified shape)', () => {
  function parseOne(metadata: Record<string, unknown>, media: Record<string, unknown> = {}): ParsedBook {
    const out = parseExport(
      JSON.stringify({ results: [{ id: 'li_2', media: { metadata, ...media } }], total: 1 })
    )
    expect(out.format).toBe('audiobookshelf')
    return out.books[0]
  }

  it('maps comma-joined authorName/narratorName and a "Name #seq" seriesName', () => {
    const b = parseOne(
      {
        title: 'Words of Radiance',
        authorName: 'Brandon Sanderson',
        narratorName: 'Kate Reading, Michael Kramer',
        seriesName: 'The Stormlight Archive #2',
        asin: 'B00INAY9BC',
        language: 'en',
        publishedYear: '2014',
      },
      { duration: 172800, numChapters: 89 }
    )
    expect(b.authors).toEqual(['Brandon Sanderson'])
    expect(b.narrators).toEqual(['Kate Reading', 'Michael Kramer'])
    expect(b.seriesName).toBe('The Stormlight Archive')
    expect(b.seriesPosition).toBe('2')
    expect(b.asin).toBe('B00INAY9BC')
    expect(b.language).toBe('en')
    expect(b.runtimeMin).toBe(2880) // 172800 / 60
    expect(b.chapterCount).toBe(89) // from numChapters
  })

  it('parses the first "Name #seq" claim from a multi-series seriesName string', () => {
    const b = parseOne({ title: 'X', seriesName: 'Sub-series, The Stormlight Archive #5' })
    // First segment has no sequence; the second is positioned, so it wins.
    expect(b.seriesName).toBe('The Stormlight Archive')
    expect(b.seriesPosition).toBe('5')
  })

  it('keeps a seriesName with no sequence and no position', () => {
    const b = parseOne({ title: 'X', seriesName: 'Standalone Companions' })
    expect(b.seriesName).toBe('Standalone Companions')
    expect(b.seriesPosition).toBeUndefined()
  })
})

describe('parseExport - Audiobookshelf routing + factual projection', () => {
  function parseOne(metadata: Record<string, unknown>, media: Record<string, unknown> = {}): ParsedBook {
    const out = parseExport(
      JSON.stringify({ results: [{ id: 'li_3', media: { metadata, ...media } }], total: 1 })
    )
    return out.books[0]
  }

  it('routes an ABS book with no asin/isbn to the cannot-match bucket', () => {
    const b = parseOne({ title: 'No Ids', authorName: 'Someone' })
    const { identified, unidentified } = partitionByIdentifier([b])
    expect(identified).toEqual([])
    expect(unidentified).toHaveLength(1)
  })

  it('stores raw as a curated factual projection (only present facts, no locals)', () => {
    const b = parseOne(
      {
        title: 'Killing Floor',
        authorName: 'Lee Child',
        narratorName: 'Jeff Harding',
        seriesName: 'Jack Reacher #1',
        asin: 'B076HYPQLK',
        language: 'English',
        publishedYear: '1997',
        publisher: 'Random House',
      },
      { duration: 44521, chapters: [{}, {}] }
    )
    expect(b.raw).toEqual({
      title: 'Killing Floor',
      authors: ['Lee Child'],
      narrators: ['Jeff Harding'],
      series: 'Jack Reacher',
      series_position: '1',
      asin: 'B076HYPQLK',
      language: 'en',
      release_date: '1997',
      publisher: 'Random House',
      runtime_min: 742,
      chapters: 2,
    })
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
