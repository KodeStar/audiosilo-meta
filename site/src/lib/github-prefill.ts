// Pure helpers that turn a ParsedBook into a GitHub contribution hand-off:
//  - addWorkIssueUrl: a prefilled "Add a work" issue (the add-work.yml form)
//  - factualSubset:   the privacy-safe factual fields for a bulk download
// Both are free of React and DOM. addWorkIssueUrl reads today's date at call
// time (in the browser), never at module scope, so the module is SSR-safe.

import type { ParsedBook } from './import-parse'

const ISSUE_BASE = 'https://github.com/kodestar/audiosilo-meta/issues/new'

/**
 * The library-import issue form - the hand-off for a Libation/other export and
 * for attaching a bulk new-books download. Kept here beside addWorkIssueUrl so
 * this module owns every GitHub-issue URL for the repo.
 */
export const importLibraryIssueUrl = `${ISSUE_BASE}?template=import-library.yml`

/**
 * Build a prefilled-issue URL for the add-work.yml template. Only fields we
 * actually have are included; the required Abridged? dropdown defaults to
 * Unabridged (the contributor reviews it). ASINs are region-scoped, so the
 * region rides along (uppercased book region, or US when absent).
 */
export function addWorkIssueUrl(book: ParsedBook): string {
  const p = new URLSearchParams()
  p.set('template', 'add-work.yml')
  p.set('title', `[work] ${book.title}`)
  if (book.title) p.set('work_title', book.title)
  if (book.authors.length) p.set('work_authors', book.authors.join(', '))
  if (book.language) p.set('work_language', book.language)
  if (book.seriesName) p.set('work_series_name', book.seriesName)
  if (book.seriesPosition) p.set('work_series_position', book.seriesPosition)
  if (book.narrators.length) p.set('rec_narrators', book.narrators.join(', '))
  p.set('rec_abridged', book.abridged === true ? 'Abridged' : 'Unabridged')
  if (book.runtimeMin != null) p.set('rec_runtime_min', String(book.runtimeMin))
  if (book.releaseDate) p.set('rec_release_date', book.releaseDate)
  if (book.publisher) p.set('rec_publisher', book.publisher)
  if (book.asin) {
    const region = (book.region ?? 'us').toUpperCase()
    p.set('rec_asins', `${region}: ${book.asin}`)
  }
  if (book.coverUrl) p.set('rec_cover_url', book.coverUrl)
  const today = new Date().toISOString().slice(0, 10)
  p.set('sources', `OpenAudible library export (reviewed ${today})`)
  return `${ISSUE_BASE}?${p.toString()}`
}

// The only OpenAudible fields we keep for the bulk download - factual metadata
// (LICENSING.md). Everything else (purchase dates, ratings, file paths,
// descriptions/summaries) is stripped and never leaves the device beyond this.
const FACTUAL_KEYS = [
  'asin',
  'title',
  'title_short',
  'author',
  'narrated_by',
  'series_name',
  'series_sequence',
  'language',
  'release_date',
  'publisher',
  'image_url',
  'region',
  'seconds',
  'abridged',
] as const

/**
 * Return only the factual OpenAudible fields from book.raw, with chapters
 * reduced to {title, start_offset_ms, length_ms}. Used to build a privacy-safe
 * new-books export the user can attach to an import issue.
 */
export function factualSubset(book: ParsedBook): Record<string, unknown> {
  const raw = book.raw
  const out: Record<string, unknown> = {}
  for (const key of FACTUAL_KEYS) {
    if (key in raw) out[key] = raw[key]
  }
  const chapters = raw['chapters']
  if (Array.isArray(chapters)) {
    out['chapters'] = chapters.map((ch) => {
      const mapped: Record<string, unknown> = {}
      if (ch && typeof ch === 'object' && !Array.isArray(ch)) {
        const c = ch as Record<string, unknown>
        if ('title' in c) mapped['title'] = c['title']
        if ('start_offset_ms' in c) mapped['start_offset_ms'] = c['start_offset_ms']
        if ('length_ms' in c) mapped['length_ms'] = c['length_ms']
      }
      return mapped
    })
  }
  return out
}
