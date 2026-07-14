import { describe, it, expect } from 'vitest'
import {
  addWorkIssueUrl,
  addWorkIssueFormUrl,
  addRecordingIssueUrl,
  addRecordingIssueUrlForWork,
  addCharactersIssueUrl,
  addRecapsIssueUrl,
  factualSubset,
  importLibraryIssueUrl,
  newBooksPayload,
  recordDataPath,
  recordEditUrl,
  correctDataIssueUrl,
  issueChooserUrl,
} from './github-prefill'
import { parseExport } from './import-parse'
import type { ParsedBook, WorkMatch } from './import-parse'

function parsedBook(extra: Partial<ParsedBook> = {}): ParsedBook {
  return {
    title: 'A Title',
    authors: [],
    narrators: [],
    format: 'openaudible',
    raw: {},
    ...extra,
  }
}

// Parse the built URL and return its query params - assertions read these rather
// than string-matching the (order-dependent, encoding-sensitive) raw URL.
function params(url: string): URLSearchParams {
  return new URL(url).searchParams
}

describe('addWorkIssueUrl', () => {
  it('uses the add-work.yml template and issues host', () => {
    const url = addWorkIssueUrl(parsedBook())
    const u = new URL(url)
    expect(u.host).toBe('github.com')
    expect(u.pathname).toBe('/kodestar/audiosilo-meta/issues/new')
    expect(u.searchParams.get('template')).toBe('add-work.yml')
  })

  it('sets the work fields when present', () => {
    const p = params(
      addWorkIssueUrl(
        parsedBook({
          title: 'Skysworn',
          authors: ['Will Wight', 'Second Author'],
          language: 'en',
          seriesName: 'Cradle',
          seriesPosition: '4',
        })
      )
    )
    expect(p.get('work_title')).toBe('Skysworn')
    expect(p.get('work_authors')).toBe('Will Wight, Second Author')
    expect(p.get('work_language')).toBe('en')
    expect(p.get('work_series_name')).toBe('Cradle')
    expect(p.get('work_series_position')).toBe('4')
    expect(p.get('title')).toBe('[work] Skysworn')
  })

  it('omits work fields that are absent', () => {
    const p = params(addWorkIssueUrl(parsedBook({ title: 'Bare', authors: [] })))
    expect(p.has('work_authors')).toBe(false)
    expect(p.has('work_language')).toBe(false)
    expect(p.has('work_series_name')).toBe(false)
    expect(p.has('work_series_position')).toBe(false)
  })

  it('sets rec_abridged only when the source stated it (omit-unknown)', () => {
    expect(params(addWorkIssueUrl(parsedBook())).has('rec_abridged')).toBe(false)
    expect(
      params(addWorkIssueUrl(parsedBook({ abridged: false }))).get('rec_abridged')
    ).toBe('Unabridged')
    expect(
      params(addWorkIssueUrl(parsedBook({ abridged: true }))).get('rec_abridged')
    ).toBe('Abridged')
  })

  it('formats rec_asins as "REGION: asin", or the bare asin when region is unknown', () => {
    // No region -> no region prefix: prefixing US would assert a fact the
    // export never stated.
    expect(
      params(addWorkIssueUrl(parsedBook({ asin: 'B0ABCDEFGH' }))).get('rec_asins')
    ).toBe('B0ABCDEFGH')
    expect(
      params(addWorkIssueUrl(parsedBook({ asin: 'B0ABCDEFGH', region: 'uk' }))).get('rec_asins')
    ).toBe('UK: B0ABCDEFGH')
  })

  it('omits rec_asins entirely when the book has no asin', () => {
    expect(params(addWorkIssueUrl(parsedBook({}))).has('rec_asins')).toBe(false)
  })

  it('puts an export ISBN in rec_isbns (audiobook edition), never work_isbn', () => {
    const p = params(addWorkIssueUrl(parsedBook({ isbn: '9780857500076' })))
    expect(p.get('rec_isbns')).toBe('9780857500076')
    // work_isbn is the PRINT/ebook edition - an audiobook export cannot claim it.
    expect(p.has('work_isbn')).toBe(false)
    expect(params(addWorkIssueUrl(parsedBook({}))).has('rec_isbns')).toBe(false)
  })

  it('carries the other recording fields when present', () => {
    const p = params(
      addWorkIssueUrl(
        parsedBook({
          narrators: ['Vox Player', 'Second Voice'],
          runtimeMin: 610,
          releaseDate: '2021-05-04',
          publisher: 'Acme Audio',
          coverUrl: 'https://img.example/cover.jpg',
        })
      )
    )
    expect(p.get('rec_narrators')).toBe('Vox Player, Second Voice')
    expect(p.get('rec_runtime_min')).toBe('610')
    expect(p.get('rec_release_date')).toBe('2021-05-04')
    expect(p.get('rec_publisher')).toBe('Acme Audio')
    expect(p.get('rec_cover_url')).toBe('https://img.example/cover.jpg')
    // sources is always stamped with today's date.
    expect(p.get('sources')).toMatch(/^OpenAudible library export \(reviewed \d{4}-\d{2}-\d{2}\)$/)
  })

  it('emits rec_runtime_min for a zero runtime (only null/undefined omit it)', () => {
    // runtimeMin is guarded with `!= null`, so 0 is still emitted.
    expect(params(addWorkIssueUrl(parsedBook({ runtimeMin: 0 }))).get('rec_runtime_min')).toBe(
      '0'
    )
    expect(params(addWorkIssueUrl(parsedBook({}))).has('rec_runtime_min')).toBe(false)
  })
})

describe('addRecordingIssueUrl', () => {
  const work: WorkMatch = { id: 'skysworn', title: 'Skysworn' }

  it('uses the add-recording.yml template', () => {
    const p = params(addRecordingIssueUrl(parsedBook(), work))
    expect(p.get('template')).toBe('add-recording.yml')
  })

  it('sets work_ref to the absolute existing-work URL with the id encoded', () => {
    const encoded: WorkMatch = { id: 'a b/c', title: 'Odd Id' }
    const p = params(addRecordingIssueUrl(parsedBook(), encoded))
    expect(p.get('work_ref')).toBe('https://meta.audiosilo.app/work?id=a%20b%2Fc')
  })

  it('titles the issue with the recording prefix and carries recording fields', () => {
    const p = params(
      addRecordingIssueUrl(
        parsedBook({
          title: 'Skysworn',
          narrators: ['Vox Player'],
          asin: 'B0ABCDEFGH',
          region: 'us',
          abridged: false,
        }),
        work
      )
    )
    expect(p.get('title')).toBe('[recording] Skysworn')
    expect(p.get('rec_narrators')).toBe('Vox Player')
    expect(p.get('rec_asins')).toBe('US: B0ABCDEFGH')
    expect(p.get('rec_abridged')).toBe('Unabridged')
  })
})

describe('addCharactersIssueUrl / addRecapsIssueUrl', () => {
  it('use the sidecar templates and carry the absolute work_ref', () => {
    const chars = params(addCharactersIssueUrl('a-deadly-education'))
    expect(chars.get('template')).toBe('add-characters.yml')
    expect(chars.get('work_ref')).toBe('https://meta.audiosilo.app/work?id=a-deadly-education')

    const recaps = params(addRecapsIssueUrl('a-deadly-education'))
    expect(recaps.get('template')).toBe('add-recaps.yml')
    expect(recaps.get('work_ref')).toBe('https://meta.audiosilo.app/work?id=a-deadly-education')
  })

  it('encode an awkward work id in work_ref', () => {
    const p = params(addCharactersIssueUrl('a b/c'))
    expect(p.get('work_ref')).toBe('https://meta.audiosilo.app/work?id=a%20b%2Fc')
  })

  it('target the github issues host', () => {
    const u = new URL(addRecapsIssueUrl('w'))
    expect(u.host).toBe('github.com')
    expect(u.pathname).toBe('/kodestar/audiosilo-meta/issues/new')
  })
})

describe('addRecordingIssueUrlForWork', () => {
  it('uses the add-recording.yml template with ONLY work_ref prefilled', () => {
    const u = new URL(addRecordingIssueUrlForWork('a-deadly-education'))
    expect(u.host).toBe('github.com')
    expect(u.pathname).toBe('/kodestar/audiosilo-meta/issues/new')
    const p = u.searchParams
    expect(p.get('template')).toBe('add-recording.yml')
    expect(p.get('work_ref')).toBe('https://meta.audiosilo.app/work?id=a-deadly-education')
    expect([...p.keys()].sort()).toEqual(['template', 'work_ref'])
  })

  it('encodes an awkward work id in work_ref', () => {
    const p = params(addRecordingIssueUrlForWork('a b/c'))
    expect(p.get('work_ref')).toBe('https://meta.audiosilo.app/work?id=a%20b%2Fc')
  })
})

describe('addWorkIssueFormUrl', () => {
  it('is the unprefilled add-work issue form', () => {
    expect(addWorkIssueFormUrl).toBe(
      'https://github.com/kodestar/audiosilo-meta/issues/new?template=add-work.yml'
    )
  })
})

describe('recordDataPath', () => {
  it('derives the work path with the two-char slug shard', () => {
    expect(recordDataPath('work', 'killing-floor-lee-child')).toBe(
      'data/works/ki/killing-floor-lee-child/work.json'
    )
  })

  it('derives the person path', () => {
    expect(recordDataPath('person', 'jeff-harding')).toBe('data/people/je/jeff-harding.json')
  })

  it('derives the series path', () => {
    expect(recordDataPath('series', 'jack-reacher')).toBe('data/series/ja/jack-reacher.json')
  })
})

describe('recordEditUrl', () => {
  it('deep-links the source file on the repo blob base', () => {
    expect(recordEditUrl('work', 'killing-floor-lee-child')).toBe(
      'https://github.com/kodestar/audiosilo-meta/blob/main/data/works/ki/killing-floor-lee-child/work.json'
    )
    expect(recordEditUrl('person', 'jeff-harding')).toBe(
      'https://github.com/kodestar/audiosilo-meta/blob/main/data/people/je/jeff-harding.json'
    )
  })

  it('percent-encodes each path segment without escaping the slashes', () => {
    // A pathological id keeps the URL well-formed (real ids match the slug grammar).
    expect(recordEditUrl('series', 'odd id')).toBe(
      'https://github.com/kodestar/audiosilo-meta/blob/main/data/series/od/odd%20id.json'
    )
  })
})

describe('correctDataIssueUrl', () => {
  it('uses the correct-data.yml template on the issues host', () => {
    const u = new URL(correctDataIssueUrl('work', 'killing-floor-lee-child'))
    expect(u.host).toBe('github.com')
    expect(u.pathname).toBe('/kodestar/audiosilo-meta/issues/new')
    expect(u.searchParams.get('template')).toBe('correct-data.yml')
  })

  it('seeds record with the entity data path for each kind', () => {
    expect(params(correctDataIssueUrl('work', 'killing-floor-lee-child')).get('record')).toBe(
      'data/works/ki/killing-floor-lee-child/work.json'
    )
    expect(params(correctDataIssueUrl('person', 'jeff-harding')).get('record')).toBe(
      'data/people/je/jeff-harding.json'
    )
    expect(params(correctDataIssueUrl('series', 'jack-reacher')).get('record')).toBe(
      'data/series/ja/jack-reacher.json'
    )
  })

  it('carries ONLY template and record', () => {
    const keys = [...params(correctDataIssueUrl('work', 'w-id')).keys()].sort()
    expect(keys).toEqual(['record', 'template'])
  })
})

describe('issueChooserUrl', () => {
  it('is the new-issue template chooser', () => {
    expect(issueChooserUrl).toBe('https://github.com/kodestar/audiosilo-meta/issues/new/choose')
  })
})

describe('factualSubset - the privacy contract', () => {
  it('keeps only whitelisted factual fields and drops every personal one', () => {
    const raw = {
      // factual (kept)
      asin: 'B0ABCDEFGH',
      title: 'Full Title',
      title_short: 'Short',
      author: 'Jane Doe',
      narrated_by: 'Vox Player',
      series_name: 'Cradle',
      series_sequence: '4',
      language: 'English',
      release_date: '2021-05-04',
      publisher: 'Acme Audio',
      image_url: 'https://img.example/cover.jpg',
      region: 'US',
      seconds: 36600,
      abridged: false,
      // personal / marketing (must be dropped)
      purchase_date: '2020-01-01',
      rating: 5,
      filename: '/Users/me/Audiobooks/x.m4b',
      description: 'A spoilery blurb.',
      summary: 'Publisher marketing copy.',
    }
    const out = factualSubset(parsedBook({ raw }))

    expect(out).toEqual({
      asin: 'B0ABCDEFGH',
      title: 'Full Title',
      title_short: 'Short',
      author: 'Jane Doe',
      narrated_by: 'Vox Player',
      series_name: 'Cradle',
      series_sequence: '4',
      language: 'English',
      release_date: '2021-05-04',
      publisher: 'Acme Audio',
      image_url: 'https://img.example/cover.jpg',
      region: 'US',
      seconds: 36600,
      abridged: false,
    })
    for (const personal of ['purchase_date', 'rating', 'filename', 'description', 'summary']) {
      expect(personal in out).toBe(false)
    }
  })

  it('reduces each chapter to exactly {title, start_offset_ms, length_ms}', () => {
    const raw = {
      title: 'A Title',
      chapters: [
        {
          title: 'Chapter 1',
          start_offset_ms: 0,
          length_ms: 60000,
          // extra per-chapter fields that must be dropped
          asin_offset: 12,
          internal_id: 'abc',
        },
      ],
    }
    const out = factualSubset(parsedBook({ raw }))
    expect(out.chapters).toEqual([{ title: 'Chapter 1', start_offset_ms: 0, length_ms: 60000 }])
  })

  it('omits the chapters key entirely when there are no chapters', () => {
    const out = factualSubset(parsedBook({ raw: { title: 'No Chapters' } }))
    expect('chapters' in out).toBe(false)
  })
  it('keeps only whitelisted Libation fields and drops personal ones', () => {
    const raw = {
      // factual (kept)
      AudibleProductId: 'B0CQDJ3PND',
      Title: 'Wind and Truth',
      Subtitle: 'Stormlight Archive, Book 5',
      AuthorNames: 'Brandon Sanderson',
      NarratorNames: 'Kate Reading',
      SeriesNames: 'The Stormlight Archive',
      SeriesOrder: '5 : The Stormlight Archive',
      Language: 'English',
      Locale: 'uk',
      LengthInMinutes: 3768,
      DatePublished: '2024-12-06T03:00:00',
      Publisher: 'Gollancz',
      PictureId: '51ZFAWrapyL',
      IsAbridged: false,
      // personal / marketing (must be dropped)
      Account: 'user@example.com',
      DateAdded: '2020-01-01T00:00:00',
      MyRatingOverall: 5,
      CommunityRatingOverall: 4.9,
      Description: 'A spoilery blurb.',
      CategoriesNames: 'Fantasy',
      ContentType: 'Product',
      HasPdf: false,
      BookStatus: 'Liberated',
    }
    const out = factualSubset(parsedBook({ format: 'libation', raw }))
    expect(out).toEqual({
      AudibleProductId: 'B0CQDJ3PND',
      Title: 'Wind and Truth',
      Subtitle: 'Stormlight Archive, Book 5',
      AuthorNames: 'Brandon Sanderson',
      NarratorNames: 'Kate Reading',
      SeriesNames: 'The Stormlight Archive',
      SeriesOrder: '5 : The Stormlight Archive',
      Language: 'English',
      Locale: 'uk',
      LengthInMinutes: 3768,
      DatePublished: '2024-12-06T03:00:00',
      Publisher: 'Gollancz',
      PictureId: '51ZFAWrapyL',
      IsAbridged: false,
    })
    for (const personal of [
      'Account',
      'DateAdded',
      'MyRatingOverall',
      'CommunityRatingOverall',
      'Description',
      'CategoriesNames',
      'ContentType',
      'HasPdf',
      'BookStatus',
    ]) {
      expect(personal in out).toBe(false)
    }
  })

  it('keeps only whitelisted folder-scan fields and drops the local-only ones', () => {
    const raw = {
      // factual (kept)
      asin: 'B076HYPQLK',
      isbn: '9780857500076',
      title: 'Killing Floor',
      subtitle: 'A Jack Reacher Novel',
      authors: ['Lee Child'],
      narrators: ['Jeff Harding'],
      series: 'Jack Reacher',
      series_position: '1',
      publisher: 'Random House',
      release_date: '2017-11-02',
      language: 'en',
      runtime_min: 823,
      chapters: 34,
      // local-only (must never leave the device)
      path: '/Users/me/Audiobooks/Lee Child/Killing Floor',
      files: ['/Users/me/Audiobooks/Lee Child/Killing Floor/Killing Floor.m4b'],
      audio_files: 1,
      sources: { title: 'tag', asin: 'filename' },
    }
    const out = factualSubset(parsedBook({ format: 'folderscan', raw }))
    expect(out).toEqual({
      asin: 'B076HYPQLK',
      isbn: '9780857500076',
      title: 'Killing Floor',
      subtitle: 'A Jack Reacher Novel',
      authors: ['Lee Child'],
      narrators: ['Jeff Harding'],
      series: 'Jack Reacher',
      series_position: '1',
      publisher: 'Random House',
      release_date: '2017-11-02',
      language: 'en',
      runtime_min: 823,
      chapters: 34,
    })
    for (const local of ['path', 'files', 'audio_files', 'sources', 'root']) {
      expect(local in out).toBe(false)
    }
    // Belt-and-braces: no local absolute path can appear anywhere in the output.
    expect(JSON.stringify(out)).not.toContain('/Users/me/Audiobooks')
  })

  it('drops a folder-scan chapters value that is not a number (count by contract)', () => {
    // A future producer emitting per-chapter objects (which could carry file
    // paths) must never leak structure into the download.
    const out = factualSubset(
      parsedBook({
        format: 'folderscan',
        raw: {
          title: 'Odd Chapters',
          chapters: [{ title: 'Ch 1', file: '/Users/me/secret/ch1.mp3' }],
        },
      })
    )
    expect('chapters' in out).toBe(false)
    expect(JSON.stringify(out)).not.toContain('/Users/me/secret')
  })
})

describe('importLibraryIssueUrl', () => {
  it('is the import-library.yml template URL', () => {
    const u = new URL(importLibraryIssueUrl)
    expect(u.host).toBe('github.com')
    expect(u.pathname).toBe('/kodestar/audiosilo-meta/issues/new')
    expect(u.searchParams.get('template')).toBe('import-library.yml')
  })
})

describe('newBooksPayload', () => {
  it('emits a bare array for OpenAudible books (already detectable)', () => {
    const payload = newBooksPayload([parsedBook({ raw: { asin: 'B0AAAAAAAA' } })])
    expect(Array.isArray(payload)).toBe(true)
  })

  it('re-wraps folder-scan books in the scan envelope, without root/files, and round-trips', () => {
    const book = parsedBook({
      format: 'folderscan',
      raw: {
        title: 'Killing Floor',
        authors: ['Lee Child'],
        asin: 'B076HYPQLK',
        chapters: 34,
        path: '/Users/me/Audiobooks/Killing Floor',
        files: ['/Users/me/Audiobooks/Killing Floor/kf.m4b'],
        audio_files: 1,
      },
    })
    const payload = newBooksPayload([book]) as Record<string, unknown>
    expect(payload['format']).toBe('audiosilo-folder-scan')
    expect(payload['version']).toBe(1)
    expect('root' in payload).toBe(false)
    const text = JSON.stringify(payload)
    expect(text).not.toContain('/Users/me/Audiobooks')
    // The download must round-trip through the parser as a folder scan.
    const out = parseExport(text)
    expect(out.format).toBe('folderscan')
    expect(out.books).toHaveLength(1)
    expect(out.books[0].title).toBe('Killing Floor')
    expect(out.books[0].chapterCount).toBe(34)
  })
})
