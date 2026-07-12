import { describe, it, expect } from 'vitest'
import {
  addWorkIssueUrl,
  addRecordingIssueUrl,
  addCharactersIssueUrl,
  addRecapsIssueUrl,
  factualSubset,
  importLibraryIssueUrl,
} from './github-prefill'
import type { ParsedBook, WorkMatch } from './import-parse'

function parsedBook(extra: Partial<ParsedBook> = {}): ParsedBook {
  return {
    title: 'A Title',
    authors: [],
    narrators: [],
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

  it('defaults rec_abridged to Unabridged and uses Abridged when abridged===true', () => {
    expect(params(addWorkIssueUrl(parsedBook())).get('rec_abridged')).toBe('Unabridged')
    expect(
      params(addWorkIssueUrl(parsedBook({ abridged: false }))).get('rec_abridged')
    ).toBe('Unabridged')
    expect(
      params(addWorkIssueUrl(parsedBook({ abridged: true }))).get('rec_abridged')
    ).toBe('Abridged')
  })

  it('formats rec_asins as "REGION: asin", defaulting REGION to US when absent', () => {
    expect(
      params(addWorkIssueUrl(parsedBook({ asin: 'B0ABCDEFGH' }))).get('rec_asins')
    ).toBe('US: B0ABCDEFGH')
    expect(
      params(addWorkIssueUrl(parsedBook({ asin: 'B0ABCDEFGH', region: 'uk' }))).get('rec_asins')
    ).toBe('UK: B0ABCDEFGH')
  })

  it('omits rec_asins entirely when the book has no asin', () => {
    expect(params(addWorkIssueUrl(parsedBook({}))).has('rec_asins')).toBe(false)
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
        parsedBook({ title: 'Skysworn', narrators: ['Vox Player'], asin: 'B0ABCDEFGH' }),
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
})

describe('importLibraryIssueUrl', () => {
  it('is the import-library.yml template URL', () => {
    const u = new URL(importLibraryIssueUrl)
    expect(u.host).toBe('github.com')
    expect(u.pathname).toBe('/kodestar/audiosilo-meta/issues/new')
    expect(u.searchParams.get('template')).toBe('import-library.yml')
  })
})
