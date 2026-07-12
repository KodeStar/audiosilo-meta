// Pure helpers that turn a ParsedBook into a GitHub contribution hand-off:
//  - addWorkIssueUrl:      a prefilled "Add a work" issue (add-work.yml)
//  - addRecordingIssueUrl: a prefilled "Add a recording" issue (add-recording.yml)
//                          for a book whose work is already in the catalogue
//  - factualSubset:        the privacy-safe factual fields for a bulk download
// All are free of React and DOM. The issue builders read today's date at call
// time (in the browser), never at module scope, so the module is SSR-safe.

import { href } from './api'
import type { ParsedBook, WorkMatch } from './import-parse'

const ISSUE_BASE = 'https://github.com/kodestar/audiosilo-meta/issues/new'
// Canonical site origin, for linking an existing work in an add-recording issue.
// Mirrors `site` in astro.config.mjs; the work path itself reuses href.work.
const META_SITE = 'https://meta.audiosilo.app'

/**
 * The library-import issue form - the hand-off for a Libation/other export and
 * for attaching a bulk new-books download. Kept here beside addWorkIssueUrl so
 * this module owns every GitHub-issue URL for the repo.
 */
export const importLibraryIssueUrl = `${ISSUE_BASE}?template=import-library.yml`

// Set the recording-half fields shared by add-work.yml and add-recording.yml.
// The required Abridged? dropdown defaults to Unabridged (the contributor
// reviews it); ASINs are region-scoped, so the region rides along (uppercased
// book region, or US when absent).
function applyRecordingParams(p: URLSearchParams, book: ParsedBook): void {
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
}

/**
 * Build a prefilled-issue URL for the add-work.yml template - a book whose work
 * is not yet in the catalogue. Only fields we actually have are included.
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
  applyRecordingParams(p, book)
  return `${ISSUE_BASE}?${p.toString()}`
}

/**
 * Build a prefilled-issue URL for the add-recording.yml template - a new
 * narration of a work that is ALREADY in the catalogue (so we link the existing
 * work via work_ref rather than creating a duplicate work).
 */
export function addRecordingIssueUrl(book: ParsedBook, work: WorkMatch): string {
  const p = new URLSearchParams()
  p.set('template', 'add-recording.yml')
  p.set('title', `[recording] ${book.title}`)
  p.set('work_ref', META_SITE + href.work(work.id))
  applyRecordingParams(p, book)
  return `${ISSUE_BASE}?${p.toString()}`
}

// The absolute work URL used as work_ref, mirroring addRecordingIssueUrl (the
// server has no id-only reverse route, so the human-readable work page is the
// stable reference a maintainer follows).
function workRef(workId: string): string {
  return META_SITE + href.work(workId)
}

/**
 * Build a prefilled-issue URL for the add-characters.yml template - the CC BY-SA
 * character sidecar for a work already in the catalogue. Only work_ref rides in
 * the URL; the generated characters.json is attached to the issue by the
 * contributor (long-form JSON cannot ride a URL), matching the import hand-off.
 */
export function addCharactersIssueUrl(workId: string): string {
  const p = new URLSearchParams()
  p.set('template', 'add-characters.yml')
  p.set('work_ref', workRef(workId))
  return `${ISSUE_BASE}?${p.toString()}`
}

/**
 * Build a prefilled-issue URL for the add-recaps.yml template - the CC BY-SA
 * "story so far" sidecar for a work already in the catalogue. As with
 * add-characters, the generated recaps.json rides as an attachment.
 */
export function addRecapsIssueUrl(workId: string): string {
  const p = new URLSearchParams()
  p.set('template', 'add-recaps.yml')
  p.set('work_ref', workRef(workId))
  return `${ISSUE_BASE}?${p.toString()}`
}

/**
 * The unprefilled add-work.yml issue form (mirroring importLibraryIssueUrl) -
 * used where the missing book's details are unknown, e.g. a series-gap row on
 * the contribute page. Named apart from addWorkIssueUrl (the ParsedBook-prefilled
 * builder above), which stays the import tool's hand-off.
 */
export const addWorkIssueFormUrl = `${ISSUE_BASE}?template=add-work.yml`

/**
 * Build a prefilled-issue URL for the add-recording.yml template from just a
 * work id - the work-page "Add a recording" CTA for a catalogued work with no
 * recordings yet. Only work_ref rides in the URL (there is no source book to
 * prefill the recording half from, unlike addRecordingIssueUrl).
 */
export function addRecordingIssueUrlForWork(workId: string): string {
  const p = new URLSearchParams()
  p.set('template', 'add-recording.yml')
  p.set('work_ref', workRef(workId))
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
