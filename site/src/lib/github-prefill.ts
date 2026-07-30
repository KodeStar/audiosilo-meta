// Pure helpers that turn a ParsedBook into a GitHub contribution hand-off:
//  - addWorkIssueUrl:      a prefilled "Add a work" issue (add-work.yml)
//  - addRecordingIssueUrl: a prefilled "Add a recording" issue (add-recording.yml)
//                          for a book whose work is already in the catalogue
//  - factualSubset:        the privacy-safe factual fields for a bulk download
// All are free of React and DOM. The issue builders read today's date at call
// time (in the browser), never at module scope, so the module is SSR-safe.

import { href, today } from './api'
import { FORMATS } from './import-parse'
import type { ParsedBook, WorkMatch } from './import-parse'
import type { LibexPrefill } from './libex-map'

// The repo's new-issue endpoint. Exported so the search CTA (search-cta.ts)
// builds its add-work URL from the same base instead of duplicating the literal.
export const ISSUE_BASE = 'https://github.com/kodestar/audiosilo-meta/issues/new'
// Canonical site origin, for linking an existing work in an add-recording issue.
// Mirrors `site` in astro.config.mjs; the work path itself reuses href.work.
const META_SITE = 'https://meta.audiosilo.app'
// The GitHub new-issue chooser (the template picker) - the "add it" hand-off for
// the not-found empty states, where no specific template/prefill applies yet.
export const issueChooserUrl = `${ISSUE_BASE}/choose`
// The repo's canonical blob base, for deep-linking a record's source JSON file.
const REPO_BLOB = 'https://github.com/kodestar/audiosilo-meta/blob/main'

/**
 * The library-import issue form - the hand-off for a Libation/other export and
 * for attaching a bulk new-books download. Kept here beside addWorkIssueUrl so
 * this module owns every GitHub-issue URL for the repo.
 */
export const importLibraryIssueUrl = `${ISSUE_BASE}?template=import-library.yml`

/**
 * The work-half facts every prefill builder needs. ParsedBook (an import export)
 * and LibexPrefill (a libex lookup) both satisfy it structurally, so the two
 * hand-offs share one mapping to the form's work_* fields.
 */
export interface WorkSource {
  title: string
  authors: string[]
  language?: string
  seriesName?: string
  seriesPosition?: string
}

/**
 * The recording-half facts every prefill builder needs. ParsedBook (an import
 * export) and LibexPrefill (a libex lookup) both satisfy it structurally, so
 * the two hand-offs share one mapping to the form's rec_* fields.
 */
export interface RecordingSource {
  narrators: string[]
  abridged?: boolean
  runtimeMin?: number
  releaseDate?: string
  publisher?: string
  asin?: string
  region?: string
  isbn?: string
  coverUrl?: string
}

// Set the work-half fields shared by add-work.yml's two hand-offs (an import
// export and a libex lookup). Only stated facts are prefilled (omit-unknown).
// Three of the form's work fields are deliberately never set from either source:
//  - work_first_published and work_isbn describe the PRINT edition, and a retail
//    audio listing states the RECORDING's date/ISBN, not the work's;
//  - work_subtitle, because a retail "subtitle" is a marketing tagline or a
//    series designation far more often than a bibliographic subtitle ("The first
//    Jack Reacher novel in the No.1 Sunday Times bestselling thriller series",
//    "Jack Reacher, Book 1"). Not one of the 7846 works in the tree carries a
//    subtitle and the Go importer never emits one, so prefilling it would seed a
//    field the database does not use with text that is usually not the fact. The
//    form keeps the input for a human who has a real one.
function applyWorkParams(p: URLSearchParams, book: WorkSource): void {
  if (book.title) p.set('work_title', book.title)
  if (book.authors.length) p.set('work_authors', book.authors.join(', '))
  if (book.language) p.set('work_language', book.language)
  if (book.seriesName) p.set('work_series_name', book.seriesName)
  if (book.seriesPosition) p.set('work_series_position', book.seriesPosition)
}

/**
 * How a region-scoped ASIN is written: `US: B015RQON6I` when the source named a
 * real marketplace, the bare ASIN otherwise. Exported so the /add confirmation
 * card renders the SAME line the issue's rec_asins field carries (libex-map's
 * prefillFacts) - the review can never drift from the submission.
 */
export function formatAsinLine(asin: string, region?: string): string {
  return region ? `${region.toUpperCase()}: ${asin}` : asin
}

// Set the recording-half fields shared by add-work.yml and add-recording.yml.
// Only stated facts are prefilled (omit-unknown): rec_abridged is set only when
// the source stated it (the form's dropdown default covers review), an ASIN
// carries a region prefix only when the source named a real marketplace, and an
// ISBN goes in rec_isbns (it identifies the AUDIOBOOK edition - never
// work_isbn, which is the print/ebook edition).
//
// `sources` is passed in rather than derived here: the form's Sources field is
// required and is the provenance record, so each hand-off states its own origin
// (an export format and review date, or a libex lookup marker).
function applyRecordingParams(p: URLSearchParams, book: RecordingSource, sources: string): void {
  if (book.narrators.length) p.set('rec_narrators', book.narrators.join(', '))
  if (book.abridged !== undefined) {
    p.set('rec_abridged', book.abridged ? 'Abridged' : 'Unabridged')
  }
  if (book.runtimeMin != null) p.set('rec_runtime_min', String(book.runtimeMin))
  if (book.releaseDate) p.set('rec_release_date', book.releaseDate)
  if (book.publisher) p.set('rec_publisher', book.publisher)
  if (book.asin) p.set('rec_asins', formatAsinLine(book.asin, book.region))
  if (book.isbn) p.set('rec_isbns', book.isbn)
  if (book.coverUrl) p.set('rec_cover_url', book.coverUrl)
  p.set('sources', sources)
}

// The Sources line for an import-export hand-off: the format's label and the
// date the contributor reviewed it. The date is read at call time (in the
// browser), never at module scope, so this module stays SSR-safe.
function exportSources(book: ParsedBook): string {
  return `${FORMATS[book.format].label} (reviewed ${today()})`
}

/**
 * Build a prefilled-issue URL for the add-work.yml template - a book whose work
 * is not yet in the catalogue. Only fields we actually have are included.
 */
export function addWorkIssueUrl(book: ParsedBook): string {
  const p = new URLSearchParams()
  p.set('template', 'add-work.yml')
  p.set('title', `[work] ${book.title}`)
  applyWorkParams(p, book)
  applyRecordingParams(p, book, exportSources(book))
  return `${ISSUE_BASE}?${p.toString()}`
}

/**
 * Build a prefilled add-work.yml issue URL from a libex lookup - the /add page's
 * hand-off, for a book the catalogue does not have. Same template and the same
 * work/recording mapping as addWorkIssueUrl; the difference is the source of the
 * facts, which the prefill's own Sources text records (libex-map.ts).
 */
export function addWorkIssueUrlFromLibex(prefill: LibexPrefill): string {
  const p = new URLSearchParams()
  p.set('template', 'add-work.yml')
  p.set('title', `[work] ${prefill.title}`)
  applyWorkParams(p, prefill)
  applyRecordingParams(p, prefill, prefill.sources)
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
  applyRecordingParams(p, book, exportSources(book))
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

/** The record kinds that have a source JSON file and a correction affordance. */
export type RecordKind = 'work' | 'person' | 'series'

/**
 * The repo-relative data path of an entity's source JSON file - the identity the
 * correct-data issue form's `record` field is seeded with, and the tail of the
 * GitHub edit deep link. The shard directory is the slug's first two characters
 * (the data-model rule), matching how the tooling lays the tree out on disk.
 */
export function recordDataPath(kind: RecordKind, id: string): string {
  const shard = id.slice(0, 2)
  switch (kind) {
    case 'work':
      return `data/works/${shard}/${id}/work.json`
    case 'person':
      return `data/people/${shard}/${id}.json`
    case 'series':
      return `data/series/${shard}/${id}.json`
  }
}

/**
 * A GitHub "view / edit the source file" deep link for an entity record - the
 * "Edit this <kind> on GitHub" half of the ImproveRecord affordance. Each path
 * segment is percent-encoded (ids match the slug grammar, so this is a no-op in
 * practice, but an odd id can never break the URL).
 */
export function recordEditUrl(kind: RecordKind, id: string): string {
  const encoded = recordDataPath(kind, id)
    .split('/')
    .map((seg) => encodeURIComponent(seg))
    .join('/')
  return `${REPO_BLOB}/${encoded}`
}

/**
 * Build a prefilled correct-data.yml issue URL with the record pre-identified -
 * the "report a problem" half of ImproveRecord. Only `record` (the entity's data
 * path) rides in the URL; the contributor fills field/values/evidence. The param
 * key mirrors the form's `record` input id.
 */
export function correctDataIssueUrl(kind: RecordKind, id: string): string {
  const p = new URLSearchParams()
  p.set('template', 'correct-data.yml')
  p.set('record', recordDataPath(kind, id))
  return `${ISSUE_BASE}?${p.toString()}`
}

/**
 * Return only the whitelisted factual fields from book.raw for the book's source
 * format (the allowlist lives on the format's FORMATS descriptor). Personal/
 * marketing fields and the folder-scan's local-only path/file fields are
 * dropped. OpenAudible chapters are reduced to {title, start_offset_ms,
 * length_ms}. A present https cover is carried as `cover_url` (from the derived
 * ParsedBook.coverUrl), so it survives the envelopes below. Used to build a
 * privacy-safe new-books export the user can attach to an import issue.
 */
export function factualSubset(book: ParsedBook): Record<string, unknown> {
  const raw = book.raw
  const out: Record<string, unknown> = {}
  for (const key of FORMATS[book.format].factualKeys) {
    if (key in raw) out[key] = raw[key]
  }
  // The folder-scan 'chapters' is a COUNT by contract; enforce it so a future
  // producer emitting per-chapter objects (which could carry file paths) can
  // never leak structure into the download.
  if (book.format === 'folderscan' && 'chapters' in out && typeof out['chapters'] !== 'number') {
    delete out['chapters']
  }
  // OpenAudible carries per-chapter offset data; reduce it to the factual shape.
  if (book.format === 'openaudible') {
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
  }
  // The cover rides as a uniform `cover_url` taken from the ParsedBook's derived
  // coverUrl (https-or-undefined by construction in every parser) rather than a
  // per-format raw key. This is what carries the cover through the audiosilo-books
  // and folder-scan envelopes, which otherwise strip it; the Go importer - which
  // DOES see untrusted input - re-guards https before reading cover_url back into
  // the recording. Facts-only: only a cover the source actually provided, never
  // fabricated. OpenAudible/Libation also keep their native image_url/PictureId,
  // so this is additive for those bare arrays.
  if (book.coverUrl) {
    out['cover_url'] = book.coverUrl
  }
  return out
}

/**
 * The bulk new-books download payload: every book reduced to its factual
 * subset. Two formats are wrapped in a self-identifying envelope (format
 * discriminator + version, WITHOUT any local-only fields) so the download
 * round-trips through parseExport instead of misdetecting, and so it is
 * machine-parseable when attached to the import-library issue form:
 *  - folder-scan books re-wrap in the `audiosilo-folder-scan` scan envelope;
 *  - Audiobookshelf books wrap in the `audiosilo-books` envelope - their flat
 *    curated projections carry an `asin` key, which would otherwise misdetect
 *    as an OpenAudible export (silently dropping authors/narrators/series).
 * OpenAudible and Libation are already detectable as bare arrays of their own
 * keys, so they stay bare. Each book's factual subset carries an https `cover_url`
 * when the source provided one (factualSubset), so covers survive the envelopes.
 */
export function newBooksPayload(books: ParsedBook[]): unknown {
  const subsets = books.map(factualSubset)
  if (books[0]?.format === 'folderscan') {
    return { format: 'audiosilo-folder-scan', version: 1, books: subsets }
  }
  if (books[0]?.format === 'audiobookshelf') {
    return { format: 'audiosilo-books', version: 1, books: subsets }
  }
  return subsets
}
